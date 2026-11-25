"""The Worker's Server loop: heartbeat contents, retention and uploads.

The Server and the uploader are fakes; what is under test is how the loop
reacts to them, in particular while the Server is unreachable.
"""

import threading
from types import SimpleNamespace

import grpc
import pytest

from aagasa_worker.main import Worker
from aagasa_worker.planstore import PlanStore


class FakeRpcError(grpc.RpcError):
    def __init__(self, code=grpc.StatusCode.UNAVAILABLE):
        self._code = code

    def code(self):
        return self._code


class FakeClient:
    def __init__(self, heartbeat_error=None):
        self.identity = None
        self.heartbeat_error = heartbeat_error
        self.heartbeats = []

    def heartbeat(self, **kwargs):
        self.heartbeats.append(kwargs)
        if self.heartbeat_error is not None:
            raise self.heartbeat_error
        return SimpleNamespace(state_is_current=True, fixed_antenna=False)


class FakeUploader:
    def __init__(self, upload_error=None, retention_error=None, summary=None):
        self.upload_error = upload_error
        self.retention_error = retention_error
        self.summary = summary or {"uploaded": 0, "already_held": 0, "rejected": 0,
                                   "failed": 0, "deferred": False}
        self.uploads = 0
        self.retention_runs = 0

    def report_outcomes(self):
        return 0

    def upload_pending(self):
        self.uploads += 1
        if self.upload_error is not None:
            raise self.upload_error
        return self.summary

    def enforce_retention(self):
        self.retention_runs += 1
        if self.retention_error is not None:
            raise self.retention_error
        return {}


class FakeIdentity:
    def __init__(self):
        self.current = SimpleNamespace(resolved=True, station_name="test",
                                       worker_id="w", station_id="s")

    def resolve(self):
        return self.current


@pytest.fixture
def store(tmp_path):
    store = PlanStore(tmp_path)
    yield store
    store.close()


def make_worker(store, client, uploader, current_pass=None):
    config = SimpleNamespace(heartbeat_interval_seconds=5)
    sync = SimpleNamespace(sync_once=lambda: None)
    return Worker(config, client, store, sync, uploader, FakeIdentity(),
                  current_pass=current_pass)


def run_cycles(worker, cycles=1):
    """Run the Server loop for a few cycles without real waiting."""
    count = {"n": 0}
    real_wait = worker._stop.wait  # noqa: SLF001

    def wait(seconds):
        count["n"] += 1
        if count["n"] >= cycles:
            worker.stop()
        return real_wait(0)

    worker._stop.wait = wait  # noqa: SLF001
    worker.run()


def test_the_heartbeat_carries_the_executing_pass(store):
    client = FakeClient()
    current = {"id": "pass-42"}
    worker = make_worker(store, client, FakeUploader(), current_pass=lambda: current["id"])

    run_cycles(worker)
    current["id"] = ""
    worker._stop.clear()  # noqa: SLF001
    run_cycles(worker)

    assert [hb["current_pass_id"] for hb in client.heartbeats] == ["pass-42", ""]


def test_the_heartbeat_reports_idle_without_an_executor(store):
    client = FakeClient()
    run_cycles(make_worker(store, client, FakeUploader()))

    assert client.heartbeats[0]["current_pass_id"] == ""


def test_retention_runs_while_the_server_is_unreachable(store):
    client = FakeClient(heartbeat_error=FakeRpcError())
    uploader = FakeUploader()

    run_cycles(make_worker(store, client, uploader), cycles=3)

    assert len(client.heartbeats) == 3
    assert uploader.retention_runs == 3


def test_a_failing_retention_check_does_not_stop_the_loop(store):
    client = FakeClient()
    uploader = FakeUploader(retention_error=OSError("disk gone"))

    run_cycles(make_worker(store, client, uploader), cycles=2)

    assert len(client.heartbeats) == 2


def test_heartbeats_do_not_wait_for_uploads(store):
    client = FakeClient()
    uploader = FakeUploader()

    run_cycles(make_worker(store, client, uploader), cycles=2)

    assert uploader.uploads == 0, "uploads run on their own thread, not in the heartbeat"
    assert len(client.heartbeats) == 2


def test_uploads_wait_for_the_server_connection(store):
    uploader = FakeUploader()
    worker = make_worker(store, FakeClient(), uploader)

    assert worker.upload_once() == 5
    assert uploader.uploads == 0

    run_cycles(worker)
    worker.upload_once()
    assert uploader.uploads == 1


def test_uploads_back_off_while_the_server_is_unreachable(store):
    uploader = FakeUploader(upload_error=FakeRpcError())
    worker = make_worker(store, FakeClient(), uploader)
    run_cycles(worker)

    delays = [worker.upload_once() for _ in range(3)]

    assert delays == [5, 10, 20]


def test_a_backlog_is_worked_through_promptly(store):
    uploader = FakeUploader(summary={"uploaded": 10, "already_held": 0, "rejected": 0,
                                     "failed": 0, "deferred": False})
    worker = make_worker(store, FakeClient(), uploader)
    run_cycles(worker)

    assert worker.upload_once() == 1.0


def test_the_upload_loop_stops_when_asked(store):
    worker = make_worker(store, FakeClient(), FakeUploader())
    thread = threading.Thread(target=worker.run_uploads)
    thread.start()
    worker.stop()
    thread.join(timeout=5)

    assert not thread.is_alive()
