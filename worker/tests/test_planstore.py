"""Durable local plan store.

The Worker's own source of truth while the Server is unreachable, so these
tests focus on what survives a restart and what must never be replayed.
"""

from datetime import datetime, timedelta, timezone

import pytest

from aagasa_worker.planstore import (
    TERMINAL_STATES,
    ExecutionState,
    PlanStore,
)

BASE = datetime(2026, 8, 25, 12, 0, 0, tzinfo=timezone.utc)


def plan_dict(pass_id: str, aos: datetime, duration=timedelta(minutes=10),
              generation: str | None = None, encoded: bytes | None = None) -> dict:
    return {
        "pass_id": pass_id,
        "generation": generation or f"gen-{pass_id}",
        "plan_version": 1,
        "encoded": encoded or f"encoded-{pass_id}".encode(),
        "aos": aos,
        "los": aos + duration,
    }


@pytest.fixture
def store(tmp_path):
    store = PlanStore(tmp_path)
    yield store
    store.close()


def test_starts_empty(store):
    assert store.generation == ""
    assert store.pending() == []
    assert store.next_due(BASE) is None


def test_stores_and_reads_back_a_plan(store):
    store.replace_desired_state([plan_dict("pass-1", BASE + timedelta(hours=2))], "set-1")

    assert store.generation == "set-1"
    stored = store.get("pass-1")
    assert stored is not None
    assert stored.state == ExecutionState.RECEIVED
    assert stored.encoded == b"encoded-pass-1"
    assert stored.aos == BASE + timedelta(hours=2)


# The central durability requirement: a restart must not erase future work.
def test_plans_survive_a_restart(tmp_path):
    first = PlanStore(tmp_path)
    first.replace_desired_state([
        plan_dict("pass-1", BASE + timedelta(hours=2)),
        plan_dict("pass-2", BASE + timedelta(hours=4)),
    ], "set-1")
    first.close()

    # A brand new process opening the same directory.
    second = PlanStore(tmp_path)
    try:
        assert second.generation == "set-1"
        assert len(second.pending()) == 2
        assert second.get("pass-1").encoded == b"encoded-pass-1"
    finally:
        second.close()


def test_execution_state_survives_a_restart(tmp_path):
    first = PlanStore(tmp_path)
    first.replace_desired_state([
        plan_dict("done", BASE), plan_dict("todo", BASE + timedelta(hours=4)),
    ], "set-1")
    first.mark_finished("done", ExecutionState.COMPLETED, "all good")
    first.close()

    second = PlanStore(tmp_path)
    try:
        assert second.get("done").state == ExecutionState.COMPLETED
        assert second.get("done").detail == "all good"
        assert [plan.pass_id for plan in second.pending()] == ["todo"]
    finally:
        second.close()


# Reconciliation -----------------------------------------------------------

def test_resync_adds_updates_and_cancels(store):
    store.replace_desired_state([
        plan_dict("keep", BASE + timedelta(hours=2)),
        plan_dict("change", BASE + timedelta(hours=3)),
        plan_dict("drop", BASE + timedelta(hours=4)),
    ], "set-1")

    summary = store.replace_desired_state([
        plan_dict("keep", BASE + timedelta(hours=2)),
        plan_dict("change", BASE + timedelta(hours=3), generation="gen-change-v2"),
        plan_dict("new", BASE + timedelta(hours=5)),
    ], "set-2")

    assert summary == {"added": 1, "updated": 1, "unchanged": 1, "cancelled": 1}
    assert store.generation == "set-2"
    # A plan removed from the desired state is cancelled, not deleted.
    dropped = store.get("drop")
    assert dropped.state == ExecutionState.CANCELLED
    assert "removed from desired state" in dropped.detail


# Global test property 9: a completed pass is never replayed.
def test_resync_does_not_resurrect_a_completed_pass(store):
    store.replace_desired_state([plan_dict("pass-1", BASE)], "set-1")
    store.mark_finished("pass-1", ExecutionState.COMPLETED)

    # The Server sends the same pass again, with different content.
    store.replace_desired_state(
        [plan_dict("pass-1", BASE, generation="gen-changed")], "set-2")

    assert store.get("pass-1").state == ExecutionState.COMPLETED
    assert store.pending() == []


def test_resync_does_not_resurrect_a_missed_pass(store):
    store.replace_desired_state([plan_dict("pass-1", BASE)], "set-1")
    store.mark_missed_before(BASE + timedelta(hours=1))
    assert store.get("pass-1").state == ExecutionState.MISSED

    store.replace_desired_state(
        [plan_dict("pass-1", BASE, generation="gen-changed")], "set-2")

    assert store.get("pass-1").state == ExecutionState.MISSED


def test_unchanged_plan_keeps_its_state(store):
    store.replace_desired_state([plan_dict("pass-1", BASE + timedelta(hours=2))], "set-1")
    store.mark_executing("pass-1", BASE)

    store.replace_desired_state([plan_dict("pass-1", BASE + timedelta(hours=2))], "set-1")

    assert store.get("pass-1").state == ExecutionState.EXECUTING


# A partially applied reconciliation would leave the Worker with a schedule
# that matches neither side.
def test_a_failed_reconciliation_leaves_the_previous_state(store):
    store.replace_desired_state([plan_dict("pass-1", BASE + timedelta(hours=2))], "set-1")

    broken = plan_dict("pass-2", BASE + timedelta(hours=3))
    broken["encoded"] = object()  # not storable

    with pytest.raises(Exception):
        store.replace_desired_state([broken], "set-2")

    assert store.generation == "set-1"
    assert store.get("pass-1") is not None


# Queue ordering -----------------------------------------------------------

def test_next_due_returns_the_soonest_pending_pass(store):
    store.replace_desired_state([
        plan_dict("later", BASE + timedelta(hours=5)),
        plan_dict("sooner", BASE + timedelta(hours=2)),
    ], "set-1")

    assert store.next_due(BASE).pass_id == "sooner"


def test_next_due_skips_windows_that_have_closed(store):
    store.replace_desired_state([
        plan_dict("past", BASE - timedelta(hours=2)),
        plan_dict("future", BASE + timedelta(hours=2)),
    ], "set-1")

    assert store.next_due(BASE).pass_id == "future"


def test_next_due_skips_terminal_passes(store):
    store.replace_desired_state([
        plan_dict("cancelled", BASE + timedelta(hours=1)),
        plan_dict("live", BASE + timedelta(hours=2)),
    ], "set-1")
    store.mark_finished("cancelled", ExecutionState.CANCELLED)

    assert store.next_due(BASE).pass_id == "live"


# Claiming -----------------------------------------------------------------

def test_a_pass_can_only_be_claimed_once(store):
    store.replace_desired_state([plan_dict("pass-1", BASE)], "set-1")

    assert store.mark_executing("pass-1", BASE) is True
    # A second claim, from a restart or a second executor, is refused.
    assert store.mark_executing("pass-1", BASE) is False


def test_a_terminal_pass_cannot_be_claimed(store):
    store.replace_desired_state([plan_dict("pass-1", BASE)], "set-1")
    store.mark_finished("pass-1", ExecutionState.COMPLETED)

    assert store.mark_executing("pass-1", BASE) is False


def test_mark_finished_rejects_a_non_terminal_state(store):
    store.replace_desired_state([plan_dict("pass-1", BASE)], "set-1")

    with pytest.raises(ValueError):
        store.mark_finished("pass-1", ExecutionState.EXECUTING)


# Missed and interrupted ---------------------------------------------------

def test_missed_marks_only_closed_windows(store):
    store.replace_desired_state([
        plan_dict("closed", BASE - timedelta(hours=2)),
        plan_dict("open", BASE + timedelta(hours=2)),
    ], "set-1")

    missed = store.mark_missed_before(BASE)

    assert missed == ["closed"]
    assert store.get("closed").state == ExecutionState.MISSED
    assert store.get("open").state == ExecutionState.RECEIVED


# spec.md section 20: a pass interrupted by a restart is not resumed midway.
def test_recovery_fails_an_interrupted_pass_rather_than_resuming_it(tmp_path):
    first = PlanStore(tmp_path)
    first.replace_desired_state([plan_dict("pass-1", BASE + timedelta(hours=4))], "set-1")
    first.mark_executing("pass-1", BASE)
    first.close()  # simulate a crash mid-pass

    second = PlanStore(tmp_path)
    try:
        interrupted = second.recover_interrupted()

        assert interrupted == ["pass-1"]
        stored = second.get("pass-1")
        assert stored.state == ExecutionState.FAILED
        assert "restarted" in stored.detail
        # And it does not come back as pending work.
        assert second.pending() == []
    finally:
        second.close()


# History ------------------------------------------------------------------

def test_history_records_every_transition(store):
    store.replace_desired_state([plan_dict("pass-1", BASE)], "set-1")
    store.mark_executing("pass-1", BASE)
    store.mark_finished("pass-1", ExecutionState.COMPLETED, "decoded 42 frames")

    history = store.history_for("pass-1")

    assert [entry.state for entry in history] == [
        ExecutionState.EXECUTING, ExecutionState.COMPLETED]
    assert history[-1].detail == "decoded 42 frames"


# Results gathered offline must survive until the Server can take them.
def test_unsynced_history_survives_a_restart(tmp_path):
    first = PlanStore(tmp_path)
    first.replace_desired_state([plan_dict("pass-1", BASE)], "set-1")
    first.mark_finished("pass-1", ExecutionState.COMPLETED)
    first.close()

    second = PlanStore(tmp_path)
    try:
        unsynced = second.unsynced_history()
        assert len(unsynced) == 1
        assert unsynced[0].pass_id == "pass-1"

        second.mark_history_synced([unsynced[0].id])
        assert second.unsynced_history() == []
    finally:
        second.close()


def test_terminal_states_cover_every_finished_outcome():
    assert TERMINAL_STATES == {
        ExecutionState.COMPLETED, ExecutionState.FAILED,
        ExecutionState.MISSED, ExecutionState.CANCELLED,
    }
