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

DEFAULT_LEAD = timedelta(seconds=60)

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
                 lead: timedelta = DEFAULT_LEAD, clock=None, on_finished=None,
                 start_of=None):
        """start_of says when a plan needs the station; it defaults to AOS.

        The station handler supplies the earlier of AOS and recording start,
        so a pre-roll longer than the lead still starts capture on time.
        """
        self._store = store
        self._handler = handler
        self._on_finished = on_finished
        self._lead = lead
        self._clock = clock or (lambda: datetime.now(timezone.utc))
        self._start_of = start_of or (lambda plan: plan.aos)
        self._stop = threading.Event()
        self._current_lock = threading.Lock()
        self._current_pass_id = ""

    @property
    def current_pass_id(self) -> str:
        """The pass executing right now, or "" when idle.

        Read from the heartbeat thread, hence the lock.
        """
        with self._current_lock:
            return self._current_pass_id

    def _set_current(self, pass_id: str) -> None:
        with self._current_lock:
            self._current_pass_id = pass_id

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
        # An interrupted pass may already have captured something worth
        # keeping; only the finished-pass hook queues output for upload, so
        # run it here too or those files would sit on disk unregistered.
        for pass_id in interrupted:
            self._notify_finished(pass_id)
        logger.info("recovery complete interrupted=%d missed=%d", len(interrupted), len(missed))
        return {"interrupted": interrupted, "missed": missed}

    def tick(self) -> StoredPlan | None:
        """Do one unit of scheduling work.

        Returns the plan executed this turn, or None. Separated from the loop
        so tests can step the executor deterministically.
        """
        now = self._clock()

        self._store.mark_missed_before(now)

        plan = self._store.next_due(now)
        if plan is None:
            return None
        if now < self._wake_time(plan):
            return None

        return self._execute(plan)

    def _execute(self, plan: StoredPlan) -> StoredPlan | None:
        if not self._store.mark_executing(plan.pass_id, self._clock()):
            logger.debug("pass %s was claimed elsewhere", plan.pass_id)
            return None

        logger.info("executing pass %s aos=%s los=%s",
                    plan.pass_id, plan.aos.isoformat(), plan.los.isoformat())

        self._set_current(plan.pass_id)
        try:
            outcome = self._handler.execute(plan, self._stop)
        except Exception as error:  # noqa: BLE001 - one bad pass must not stop the Worker
            logger.exception("pass %s raised during execution", plan.pass_id)
            self._store.mark_finished(plan.pass_id, ExecutionState.FAILED, str(error))
            self._notify_finished(plan.pass_id)
            return plan
        finally:
            self._set_current("")

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

    def _wake_time(self, plan: StoredPlan) -> datetime:
        try:
            start = min(plan.aos, self._start_of(plan))
        except Exception:  # noqa: BLE001 - a bad plan must not stall scheduling
            logger.exception("could not read the start of pass %s; using AOS", plan.pass_id)
            start = plan.aos
        return start - self._lead

    def seconds_until_next(self) -> float:
        """How long the loop may sleep before it must look again."""
        plan = self._store.next_due(self._clock())
        if plan is None:
            return MAX_SLEEP_SECONDS
        wait = (self._wake_time(plan) - self._clock()).total_seconds()
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
