"""Upload queue behaviour against Server refusals.

The Server is a fake that can be told to refuse particular recordings with a
particular gRPC status, so the classification is exercised without a network.
"""

import sqlite3
from datetime import datetime, timezone
from types import SimpleNamespace

import grpc
import pytest

from aagasa_worker.planstore import DATABASE_FILENAME, ExecutionState, PlanStore
from aagasa_worker.recordings import collect
from aagasa_worker.uploads import UPLOAD_BUDGET_SECONDS, RecordingUploader


class FakeRpcError(grpc.RpcError):
    def __init__(self, code, details=""):
        self._code = code
        self._details = details

    def code(self):
        return self._code

    def details(self):
        return self._details


class FakeServer:
    """Accepts uploads unless told to refuse a given file name."""

    def __init__(self, refusals=None, accepted=None):
        self.refusals = refusals or {}
        self.sent = []
        self.accepted = accepted
        self.reports = []

    def upload_recording(self, recording, path):
        self.sent.append(recording.relative_path)
        refusal = self.refusals.get(recording.relative_path)
        if refusal is not None:
            raise FakeRpcError(*refusal)
        return SimpleNamespace(stored=True)

    def report_executions(self, entries):
        self.reports = list(entries)
        accepted = len(entries) if self.accepted is None else self.accepted
        return SimpleNamespace(accepted=accepted)


@pytest.fixture
def store(tmp_path):
    store = PlanStore(tmp_path / "state")
    yield store
    store.close()


def queue(store, tmp_path, names):
    directory = tmp_path / "recordings" / "pass-1"
    directory.mkdir(parents=True, exist_ok=True)
    for name in names:
        (directory / name).write_bytes(b"x" * 16)
    store.record_recordings(collect("pass-1", directory))
    return directory


def uploader_for(store, tmp_path, server, monotonic=None):
    return RecordingUploader(server, store, tmp_path / "recordings", monotonic=monotonic)


@pytest.mark.parametrize("code", [
    grpc.StatusCode.INVALID_ARGUMENT,
    grpc.StatusCode.FAILED_PRECONDITION,
    grpc.StatusCode.NOT_FOUND,
    grpc.StatusCode.PERMISSION_DENIED,
    grpc.StatusCode.DATA_LOSS,
])
def test_a_permanently_refused_recording_does_not_block_the_queue(store, tmp_path, code):
    directory = queue(store, tmp_path, ["a.bin", "b.bin", "c.bin"])
    server = FakeServer({"a.bin": (code, "checksum mismatch")})

    summary = uploader_for(store, tmp_path, server).upload_pending()

    assert summary["rejected"] == 1
    assert summary["uploaded"] == 2
    assert server.sent == ["a.bin", "b.bin", "c.bin"]
    assert store.pending_uploads() == []
    rejected = store.rejected_uploads()
    assert [r.relative_path for r in rejected] == ["a.bin"]
    assert rejected[0].last_error.startswith(code.name)
    assert "checksum mismatch" in rejected[0].last_error
    assert (directory / "a.bin").exists(), "a rejected recording is never deleted"


def test_a_rejected_recording_is_never_retried(store, tmp_path):
    queue(store, tmp_path, ["a.bin"])
    server = FakeServer({"a.bin": (grpc.StatusCode.INVALID_ARGUMENT, "bad path")})
    uploader = uploader_for(store, tmp_path, server)

    uploader.upload_pending()
    uploader.upload_pending()

    assert server.sent == ["a.bin"]


def test_already_exists_counts_as_confirmed(store, tmp_path):
    queue(store, tmp_path, ["a.bin"])
    server = FakeServer({"a.bin": (grpc.StatusCode.ALREADY_EXISTS, "")})

    summary = uploader_for(store, tmp_path, server).upload_pending()

    assert summary["already_held"] == 1
    assert store.pending_uploads() == []
    assert store.rejected_uploads() == []
    assert store.cleanup_candidates()[0].uploaded is True


@pytest.mark.parametrize("code", [
    grpc.StatusCode.UNAVAILABLE,
    grpc.StatusCode.DEADLINE_EXCEEDED,
    grpc.StatusCode.RESOURCE_EXHAUSTED,
    grpc.StatusCode.UNAUTHENTICATED,
    grpc.StatusCode.INTERNAL,
    grpc.StatusCode.UNKNOWN,
    grpc.StatusCode.CANCELLED,
    grpc.StatusCode.ABORTED,
])
def test_a_transient_failure_stops_the_round_and_keeps_everything_queued(
        store, tmp_path, code):
    queue(store, tmp_path, ["a.bin", "b.bin"])
    server = FakeServer({"a.bin": (code, "")})

    with pytest.raises(grpc.RpcError):
        uploader_for(store, tmp_path, server).upload_pending()

    assert server.sent == ["a.bin"]
    pending = store.pending_uploads()
    assert [r.relative_path for r in pending] == ["a.bin", "b.bin"]
    assert pending[0].attempts == 1
    assert pending[0].last_error == code.name
    assert store.rejected_uploads() == []


def test_no_new_upload_starts_once_the_budget_is_spent(store, tmp_path):
    queue(store, tmp_path, ["a.bin", "b.bin", "c.bin", "d.bin"])
    now = {"t": 0.0}

    class SlowServer(FakeServer):
        def upload_recording(self, recording, path):
            now["t"] += UPLOAD_BUDGET_SECONDS / 2 + 1
            return super().upload_recording(recording, path)

    server = SlowServer()
    uploader = uploader_for(store, tmp_path, server, monotonic=lambda: now["t"])

    summary = uploader.upload_pending()

    assert summary["deferred"] is True
    assert server.sent == ["a.bin", "b.bin"]
    assert len(store.pending_uploads()) == 2

    now["t"] = 1000.0
    uploader.upload_pending()
    assert store.pending_uploads() == []


def test_a_partially_accepted_execution_report_keeps_every_entry_unsynced(store, tmp_path):
    moment = datetime(2026, 1, 1, tzinfo=timezone.utc)
    store.replace_desired_state([
        {"pass_id": pass_id, "generation": pass_id, "plan_version": 1,
         "encoded": pass_id.encode(), "aos": moment, "los": moment}
        for pass_id in ("pass-1", "pass-2")
    ], "set-1")
    for pass_id in ("pass-1", "pass-2"):
        store.mark_finished(pass_id, ExecutionState.COMPLETED)
    server = FakeServer(accepted=1)

    uploader_for(store, tmp_path, server).report_outcomes()

    assert len(server.reports) == 2
    assert len(store.unsynced_history()) == 2


def test_unsupported_execution_states_are_dropped_locally(store, tmp_path):
    moment = datetime(2026, 1, 1, tzinfo=timezone.utc)
    store.replace_desired_state([
        {"pass_id": "pass-1", "generation": "g1", "plan_version": 1,
         "encoded": b"a", "aos": moment, "los": moment}
    ], "set-1")
    store.mark_executing("pass-1", moment)
    store.mark_finished("pass-1", ExecutionState.CANCELLED)
    server = FakeServer()

    reported = uploader_for(store, tmp_path, server).report_outcomes()

    assert reported == 0
    assert server.reports == []
    assert store.unsynced_history() == []


def test_an_existing_state_database_gains_the_rejected_column(tmp_path):
    state = tmp_path / "state"
    state.mkdir()
    old = sqlite3.connect(state / DATABASE_FILENAME)
    old.executescript("""
        CREATE TABLE recordings (
            recording_id  TEXT PRIMARY KEY,
            pass_id       TEXT NOT NULL,
            relative_path TEXT NOT NULL,
            absolute_path TEXT NOT NULL,
            size_bytes    INTEGER NOT NULL,
            checksum      TEXT NOT NULL,
            finished_at   REAL NOT NULL,
            uploaded      INTEGER NOT NULL DEFAULT 0,
            uploaded_at   REAL,
            attempts      INTEGER NOT NULL DEFAULT 0,
            last_error    TEXT NOT NULL DEFAULT '',
            deleted       INTEGER NOT NULL DEFAULT 0
        );
        INSERT INTO recordings (recording_id, pass_id, relative_path, absolute_path,
                                size_bytes, checksum, finished_at)
        VALUES ('r1', 'pass-1', 'a.bin', '/x/a.bin', 16, 'c', 1.0);
    """)
    old.commit()
    old.close()

    store = PlanStore(state)
    try:
        pending = store.pending_uploads()
        assert [r.recording_id for r in pending] == ["r1"]
        assert pending[0].rejected is False

        store.mark_upload_rejected("r1", "INVALID_ARGUMENT: nope")
        assert store.pending_uploads() == []
    finally:
        store.close()

    reopened = PlanStore(state)
    try:
        assert [r.recording_id for r in reopened.rejected_uploads()] == ["r1"]
    finally:
        reopened.close()
