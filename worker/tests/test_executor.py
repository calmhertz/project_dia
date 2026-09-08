"""Pass execution scheduling.

The executor reads only the local store, which is what lets the Worker keep
running through a Server outage (spec.md section 6.1). These tests drive a
controllable clock so behaviour is deterministic and fast.
"""

import threading
from datetime import datetime, timedelta, timezone

import pytest

from aagasa_worker.executor import (
    DEFAULT_LEAD,
    MAX_SLEEP_SECONDS,
    ExecutionOutcome,
    Executor,
    RecordOnlyHandler,
)
from aagasa_worker.planstore import ExecutionState, PlanStore

BASE = datetime(2026, 8, 25, 12, 0, 0, tzinfo=timezone.utc)


class Clock:
    """A hand-cranked clock."""

    def __init__(self, now: datetime = BASE):
        self.now = now

    def __call__(self) -> datetime:
        return self.now

    def advance(self, delta: timedelta) -> None:
        self.now += delta


class RecordingHandler:
    """Handler that records calls and can be told to fail or raise."""

    def __init__(self, succeed=True, raises=None):
        self.calls: list[str] = []
        self.succeed = succeed
        self.raises = raises

    def execute(self, plan, stop) -> ExecutionOutcome:
        self.calls.append(plan.pass_id)
        if self.raises is not None:
            raise self.raises
        return ExecutionOutcome(succeeded=self.succeed, detail="handled")


def plan_dict(pass_id: str, aos: datetime, duration=timedelta(minutes=10)) -> dict:
    return {
        "pass_id": pass_id, "generation": f"gen-{pass_id}", "plan_version": 1,
        "encoded": f"encoded-{pass_id}".encode(),
        "aos": aos, "los": aos + duration,
    }


@pytest.fixture
def store(tmp_path):
    store = PlanStore(tmp_path)
    yield store
    store.close()


def build(store, handler=None, clock=None):
    clock = clock or Clock()
    handler = handler or RecordingHandler()
    return Executor(store, handler, clock=clock), handler, clock


# Timing -------------------------------------------------------------------

def test_a_pass_far_in_the_future_is_not_executed(store):
    store.replace_desired_state([plan_dict("pass-1", BASE + timedelta(hours=4))], "set-1")
    executor, handler, _ = build(store)

    assert executor.tick() is None
    assert handler.calls == []
    assert store.get("pass-1").state == ExecutionState.RECEIVED


def test_a_pass_inside_the_lead_window_is_executed(store):
    aos = BASE + timedelta(seconds=30)  # inside the 60s lead
    store.replace_desired_state([plan_dict("pass-1", aos)], "set-1")
    executor, handler, _ = build(store)

    executed = executor.tick()

    assert executed is not None and executed.pass_id == "pass-1"
    assert handler.calls == ["pass-1"]
    assert store.get("pass-1").state == ExecutionState.COMPLETED


def test_the_executor_waits_then_runs_as_time_passes(store):
    aos = BASE + timedelta(minutes=30)
    store.replace_desired_state([plan_dict("pass-1", aos)], "set-1")
    executor, handler, clock = build(store)

    assert executor.tick() is None
    clock.advance(timedelta(minutes=29, seconds=30))  # now inside the lead
    assert executor.tick() is not None
    assert handler.calls == ["pass-1"]


def test_passes_run_in_chronological_order(store):
    store.replace_desired_state([
        plan_dict("second", BASE + timedelta(minutes=40)),
        plan_dict("first", BASE + timedelta(minutes=10)),
    ], "set-1")
    executor, handler, clock = build(store)

    clock.advance(timedelta(minutes=10))
    executor.tick()
    clock.advance(timedelta(minutes=30))
    executor.tick()

    assert handler.calls == ["first", "second"]


# Outcomes -----------------------------------------------------------------

def test_a_failing_handler_records_failure(store):
    store.replace_desired_state([plan_dict("pass-1", BASE)], "set-1")
    executor, _, _ = build(store, RecordingHandler(succeed=False))

    executor.tick()

    assert store.get("pass-1").state == ExecutionState.FAILED


# One bad pass must not take the Worker down.
def test_a_raising_handler_is_contained(store):
    store.replace_desired_state([
        plan_dict("bad", BASE), plan_dict("good", BASE + timedelta(minutes=40)),
    ], "set-1")
    handler = RecordingHandler(raises=RuntimeError("rotator exploded"))
    executor, _, clock = build(store, handler)

    executor.tick()

    stored = store.get("bad")
    assert stored.state == ExecutionState.FAILED
    assert "rotator exploded" in stored.detail

    # The Worker carries on to the next pass.
    handler.raises = None
    clock.advance(timedelta(minutes=40))
    executor.tick()
    assert handler.calls == ["bad", "good"]


# Global test property 9: completed and missed passes are not replayed.
def test_a_completed_pass_is_not_run_twice(store):
    store.replace_desired_state([plan_dict("pass-1", BASE)], "set-1")
    executor, handler, _ = build(store)

    executor.tick()
    executor.tick()
    executor.tick()

    assert handler.calls == ["pass-1"]


def test_a_missed_pass_is_never_executed(store):
    # The window closed while the Worker was asleep.
    store.replace_desired_state([plan_dict("stale", BASE - timedelta(hours=3))], "set-1")
    executor, handler, _ = build(store)

    executor.tick()

    assert handler.calls == []
    assert store.get("stale").state == ExecutionState.MISSED


def test_missed_passes_do_not_block_later_ones(store):
    store.replace_desired_state([
        plan_dict("stale", BASE - timedelta(hours=3)),
        plan_dict("upcoming", BASE + timedelta(minutes=10)),
    ], "set-1")
    executor, handler, clock = build(store)

    executor.tick()
    clock.advance(timedelta(minutes=10))
    executor.tick()

    assert handler.calls == ["upcoming"]
    assert store.get("stale").state == ExecutionState.MISSED
    assert store.get("upcoming").state == ExecutionState.COMPLETED


# Recovery -----------------------------------------------------------------

def test_recovery_closes_interrupted_and_missed_work(tmp_path):
    first = PlanStore(tmp_path)
    first.replace_desired_state([
        plan_dict("interrupted", BASE + timedelta(hours=4)),
        plan_dict("stale", BASE - timedelta(hours=3)),
        plan_dict("future", BASE + timedelta(hours=6)),
    ], "set-1")
    first.mark_executing("interrupted", BASE)
    first.close()

    second = PlanStore(tmp_path)
    try:
        executor, handler, _ = build(second)
        result = executor.recover()

        assert result["interrupted"] == ["interrupted"]
        assert result["missed"] == ["stale"]
        # Future work is untouched and still runnable.
        assert [plan.pass_id for plan in second.pending()] == ["future"]
        assert handler.calls == []
    finally:
        second.close()


def test_recovery_does_not_replay_completed_work(tmp_path):
    first = PlanStore(tmp_path)
    first.replace_desired_state([plan_dict("done", BASE + timedelta(hours=2))], "set-1")
    first.mark_finished("done", ExecutionState.COMPLETED)
    first.close()

    second = PlanStore(tmp_path)
    try:
        executor, handler, clock = build(second)
        executor.recover()
        clock.advance(timedelta(hours=2))
        executor.tick()

        assert handler.calls == []
        assert second.get("done").state == ExecutionState.COMPLETED
    finally:
        second.close()


# Sleep pacing -------------------------------------------------------------

def test_sleep_is_bounded_when_nothing_is_scheduled(store):
    executor, _, _ = build(store)
    assert executor.seconds_until_next() == MAX_SLEEP_SECONDS


def test_sleep_is_bounded_even_with_a_distant_pass(store):
    store.replace_desired_state([plan_dict("far", BASE + timedelta(days=3))], "set-1")
    executor, _, _ = build(store)

    # Capped, so a newly synced pass is noticed promptly.
    assert executor.seconds_until_next() == MAX_SLEEP_SECONDS


def test_sleep_is_zero_when_a_pass_is_due(store):
    store.replace_desired_state([plan_dict("now", BASE)], "set-1")
    executor, _, _ = build(store)

    assert executor.seconds_until_next() == 0.0


def test_sleep_shrinks_as_a_pass_approaches(store):
    aos = BASE + timedelta(seconds=DEFAULT_LEAD.total_seconds() + 10)
    store.replace_desired_state([plan_dict("soon", aos)], "set-1")
    executor, _, _ = build(store)

    assert 0 < executor.seconds_until_next() <= 10


# The whole point: the loop reads only local state.
def test_the_executor_needs_no_server(store):
    store.replace_desired_state([
        plan_dict("one", BASE + timedelta(minutes=10)),
        plan_dict("two", BASE + timedelta(minutes=40)),
        plan_dict("three", BASE + timedelta(minutes=70)),
    ], "set-1")
    executor, handler, clock = build(store)

    # No client, no network, no Server anywhere in this test. The clock is
    # stepped to each pass in turn rather than jumping past their windows.
    for offset in (10, 40, 70):
        clock.now = BASE + timedelta(minutes=offset)
        executor.tick()

    assert handler.calls == ["one", "two", "three"]
    for pass_id in ("one", "two", "three"):
        assert store.get(pass_id).state == ExecutionState.COMPLETED


def test_the_run_loop_stops_when_asked(store):
    store.replace_desired_state([plan_dict("pass-1", BASE)], "set-1")
    executor, handler, _ = build(store)

    thread = threading.Thread(target=executor.run)
    thread.start()
    try:
        deadline = threading.Event()
        deadline.wait(1.0)
    finally:
        executor.stop()
        thread.join(timeout=5)

    assert not thread.is_alive()
    assert handler.calls == ["pass-1"]


def test_the_default_handler_performs_no_hardware_action(store):
    store.replace_desired_state([plan_dict("pass-1", BASE)], "set-1")
    handler = RecordOnlyHandler()
    executor = Executor(store, handler, clock=Clock())

    executor.tick()

    assert handler.executed == ["pass-1"]
    assert store.get("pass-1").state == ExecutionState.COMPLETED
