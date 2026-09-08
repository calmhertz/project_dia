"""Pass execution scheduling.

Decides *when* a pass runs and records what happened. *How* it runs against
the physical station is a separate concern, supplied as a handler, so this
loop is testable without hardware and V10 can plug the station in.

The loop depends on nothing but the local store, which is what lets the Worker
keep executing through a Server outage (spec.md section 6.1).
"""

import logging
import threading
from dataclasses import dataclass
from datetime import datetime, timedelta, timezone
from typing import Protocol

from aagasa_worker.planstore import ExecutionState, PlanStore, StoredPlan

logger = logging.getLogger(__name__)

# How long before AOS the executor wakes to prepare hardware.
DEFAULT_LEAD = timedelta(seconds=60)

# Longest the loop sleeps between checks, so a newly synced pass is noticed
# without waiting for the next scheduled one.
MAX_SLEEP_SECONDS = 30.0


@dataclass(frozen=True)
class ExecutionOutcome:
    """What a pass execution produced."""

    succeeded: bool
    detail: str = ""


class PassHandler(Protocol):
    """Runs one pass against the station.

    V9 ships a handler that only records the attempt. V10 replaces it with
    one that drives the rotator, the SDR and SatDump.
    """

    def execute(self, plan: StoredPlan, stop: threading.Event) -> ExecutionOutcome:
        ...


class RecordOnlyHandler:
    """Handler that performs no hardware action.

    Lets the scheduling half of the Worker be exercised end to end before the
    station is wired in.
    """

    def __init__(self):
        self.executed: list[str] = []

    def execute(self, plan: StoredPlan, stop: threading.Event) -> ExecutionOutcome:
        self.executed.append(plan.pass_id)
        logger.info("pass %s executed (no hardware action configured)", plan.pass_id)
        return ExecutionOutcome(succeeded=True, detail="no hardware action configured")


class Executor:
    """Runs due passes from the local store."""

    def __init__(self, store: PlanStore, handler: PassHandler,
                 lead: timedelta = DEFAULT_LEAD, clock=None, on_finished=None):
        self._store = store
        self._handler = handler
        # Called with a pass id once execution ends, so its output can be
        # queued for upload. Failures here must not fail the pass.
        self._on_finished = on_finished
        self._lead = lead
        # Injectable so tests can drive time without sleeping.
        self._clock = clock or (lambda: datetime.now(timezone.utc))
        self._stop = threading.Event()

    def stop(self) -> None:
        self._stop.set()

    @property
    def stopping(self) -> bool:
        return self._stop.is_set()

    def recover(self) -> dict:
        """Reconcile local state after a restart.

        spec.md section 20: do not resume a pass that was interrupted, do not
        replay completed work, and mark genuinely missed passes as missed.
        """
        interrupted = self._store.recover_interrupted()
        missed = self._store.mark_missed_before(self._clock())
        logger.info("recovery complete interrupted=%d missed=%d", len(interrupted), len(missed))
        return {"interrupted": interrupted, "missed": missed}

    def tick(self) -> StoredPlan | None:
        """Do one unit of scheduling work.

        Returns the plan executed this turn, or None. Separated from the loop
        so tests can step the executor deterministically.
        """
        now = self._clock()

        # Close out anything whose window has passed before looking for work.
        self._store.mark_missed_before(now)

        plan = self._store.next_due(now)
        if plan is None:
            return None
        if now < plan.aos - self._lead:
            return None

        return self._execute(plan)

    def _execute(self, plan: StoredPlan) -> StoredPlan | None:
        # Claim it first: the state change is what stops a second executor,
        # or a restart mid-pass, from running it again.
        if not self._store.mark_executing(plan.pass_id, self._clock()):
            logger.debug("pass %s was claimed elsewhere", plan.pass_id)
            return None

        logger.info("executing pass %s aos=%s los=%s",
                    plan.pass_id, plan.aos.isoformat(), plan.los.isoformat())

        try:
            outcome = self._handler.execute(plan, self._stop)
        except Exception as error:  # noqa: BLE001 - one bad pass must not stop the Worker
            logger.exception("pass %s raised during execution", plan.pass_id)
            self._store.mark_finished(plan.pass_id, ExecutionState.FAILED, str(error))
            # A failed pass can still have produced partial output worth
            # keeping, so its directory is scanned too.
            self._notify_finished(plan.pass_id)
            return plan

        state = ExecutionState.COMPLETED if outcome.succeeded else ExecutionState.FAILED
        self._store.mark_finished(plan.pass_id, state, outcome.detail)
        self._notify_finished(plan.pass_id)
        return plan

    def _notify_finished(self, pass_id: str) -> None:
        if self._on_finished is None:
            return
        try:
            self._on_finished(pass_id)
        except Exception:  # noqa: BLE001 - collecting output must not fail the pass
            logger.exception("collecting output for pass %s failed", pass_id)

    def seconds_until_next(self) -> float:
        """How long the loop may sleep before it must look again."""
        plan = self._store.next_due(self._clock())
        if plan is None:
            return MAX_SLEEP_SECONDS
        wait = (plan.aos - self._lead - self._clock()).total_seconds()
        if wait <= 0:
            return 0.0
        return min(wait, MAX_SLEEP_SECONDS)

    def run(self) -> None:
        """Execute passes until stopped.

        A Server outage is not a condition this loop knows or cares about: it
        reads only local state.
        """
        logger.info("execution loop started")
        self.recover()

        while not self._stop.is_set():
            try:
                self.tick()
            except Exception:  # noqa: BLE001 - the loop must survive
                logger.exception("execution tick failed")

            wait = self.seconds_until_next()
            if wait > 0:
                self._stop.wait(wait)

        logger.info("execution loop stopped")
