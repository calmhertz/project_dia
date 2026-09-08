"""Recording identity, retention and the upload queue."""

from datetime import datetime, timedelta, timezone
from pathlib import Path

import pytest

from aagasa_worker.planstore import PlanStore
from aagasa_worker.recordings import (
    MINIMUM_RETENTION,
    CleanupCandidate,
    checksum_of,
    collect,
    ensure_within,
    read_chunks,
    recording_id_for,
    select_for_cleanup,
)

BASE = datetime(2026, 8, 25, 12, 0, 0, tzinfo=timezone.utc)


# Identity -----------------------------------------------------------------

# The whole idempotency story rests on this being stable.
def test_recording_id_is_deterministic():
    first = recording_id_for("pass-1", "baseband.ziq")
    second = recording_id_for("pass-1", "baseband.ziq")

    assert first == second
    assert len(first) == 64
    assert all(character in "0123456789abcdef" for character in first)


def test_recording_id_distinguishes_passes_and_paths():
    assert recording_id_for("pass-1", "a.bin") != recording_id_for("pass-2", "a.bin")
    assert recording_id_for("pass-1", "a.bin") != recording_id_for("pass-1", "b.bin")


# A partially written file must not become a different recording, or a retry
# after a truncated capture would upload as a second one.
def test_recording_id_does_not_depend_on_content(tmp_path):
    first = recording_id_for("pass-1", "out.bin")
    (tmp_path / "out.bin").write_bytes(b"different content entirely")
    second = recording_id_for("pass-1", "out.bin")

    assert first == second


def test_collect_finds_every_output(tmp_path):
    output = tmp_path / "pass-1"
    (output / "images").mkdir(parents=True)
    (output / "baseband.ziq").write_bytes(b"a" * 100)
    (output / "images" / "channel1.png").write_bytes(b"b" * 50)

    found = collect("pass-1", output)

    paths = sorted(item.relative_path for item in found)
    assert paths == ["baseband.ziq", "images/channel1.png"]
    assert all(item.checksum_sha256 for item in found)
    assert {item.size_bytes for item in found} == {100, 50}


def test_collect_skips_empty_and_partial_files(tmp_path):
    output = tmp_path / "pass-1"
    output.mkdir(parents=True)
    (output / "good.bin").write_bytes(b"x" * 10)
    (output / "empty.bin").write_bytes(b"")
    (output / ".partial.tmp").write_bytes(b"y" * 10)

    found = collect("pass-1", output)

    assert [item.relative_path for item in found] == ["good.bin"]


def test_collect_on_a_pass_with_no_output(tmp_path):
    assert collect("pass-1", tmp_path / "missing") == []


def test_checksum_matches_content(tmp_path):
    import hashlib
    path = tmp_path / "data.bin"
    payload = b"z" * (3 * 1024 * 1024)
    path.write_bytes(payload)

    assert checksum_of(path) == hashlib.sha256(payload).hexdigest()


def test_read_chunks_reassembles_the_file(tmp_path):
    path = tmp_path / "data.bin"
    payload = bytes(range(256)) * 8192
    path.write_bytes(payload)

    assert b"".join(read_chunks(path, chunk_bytes=1024)) == payload


# Retention ----------------------------------------------------------------

def candidate(name, hours_old, uploaded):
    return CleanupCandidate(
        recording_id=name, absolute_path=Path(f"/tmp/{name}"),
        finished_at=BASE - timedelta(hours=hours_old), uploaded=uploaded)


# spec.md section 19.2: keep local copies at least twelve hours.
def test_nothing_is_deleted_before_the_retention_window():
    candidates = [candidate("recent", 2, uploaded=True)]

    assert select_for_cleanup(candidates, BASE, under_pressure=False) == []


def test_confirmed_and_expired_recordings_go_oldest_first():
    candidates = [
        candidate("newer", 13, uploaded=True),
        candidate("oldest", 40, uploaded=True),
        candidate("middle", 20, uploaded=True),
    ]

    order = [item.recording_id for item in
             select_for_cleanup(candidates, BASE, under_pressure=False)]

    assert order == ["oldest", "middle", "newer"]


# An unconfirmed recording would be lost for good, so it is never touched
# under normal conditions (worker-spec section 15).
def test_unconfirmed_recordings_are_never_deleted_normally():
    candidates = [candidate("never-uploaded", 100, uploaded=False)]

    assert select_for_cleanup(candidates, BASE, under_pressure=False) == []


# spec.md section 19.2: above ninety percent, older data may go early.
def test_disk_pressure_reaches_past_the_retention_window():
    candidates = [
        candidate("expired", 20, uploaded=True),
        candidate("recent", 1, uploaded=True),
    ]

    order = [item.recording_id for item in
             select_for_cleanup(candidates, BASE, under_pressure=True)]

    assert order == ["expired", "recent"]


# Even under pressure, a confirmed copy goes before an unconfirmed one.
def test_disk_pressure_prefers_confirmed_copies(): 
    candidates = [
        candidate("unconfirmed-old", 50, uploaded=False),
        candidate("confirmed-recent", 1, uploaded=True),
    ]

    order = [item.recording_id for item in
             select_for_cleanup(candidates, BASE, under_pressure=True)]

    assert order == ["confirmed-recent", "unconfirmed-old"]


def test_retention_window_matches_the_specification():
    assert MINIMUM_RETENTION == timedelta(hours=12)


# Deletion is destructive, so the path check is explicit.
def test_paths_outside_the_root_are_rejected(tmp_path):
    root = tmp_path / "recordings"
    root.mkdir()
    inside = root / "pass-1" / "file.bin"
    inside.parent.mkdir(parents=True)
    inside.write_bytes(b"x")

    assert ensure_within(root, inside) is True
    assert ensure_within(root, tmp_path / "elsewhere.bin") is False
    assert ensure_within(root, Path("/etc/passwd")) is False


# Local queue --------------------------------------------------------------

@pytest.fixture
def store(tmp_path):
    store = PlanStore(tmp_path / "state")
    yield store
    store.close()


def make_output(tmp_path, pass_id, files):
    directory = tmp_path / "recordings" / pass_id
    directory.mkdir(parents=True, exist_ok=True)
    for name, payload in files.items():
        (directory / name).write_bytes(payload)
    return directory


def test_registering_output_queues_it_for_upload(store, tmp_path):
    output = make_output(tmp_path, "pass-1", {"a.bin": b"x" * 10, "b.bin": b"y" * 20})

    added = store.record_recordings(collect("pass-1", output))

    assert added == 2
    assert len(store.pending_uploads()) == 2


# A re-scan after a restart must not queue the same file twice.
def test_registering_the_same_output_twice_is_a_no_op(store, tmp_path):
    output = make_output(tmp_path, "pass-1", {"a.bin": b"x" * 10})

    assert store.record_recordings(collect("pass-1", output)) == 1
    assert store.record_recordings(collect("pass-1", output)) == 0
    assert len(store.pending_uploads()) == 1


def test_an_acknowledged_recording_leaves_the_queue(store, tmp_path):
    output = make_output(tmp_path, "pass-1", {"a.bin": b"x" * 10})
    store.record_recordings(collect("pass-1", output))
    recording = store.pending_uploads()[0]

    store.mark_uploaded(recording.recording_id)

    assert store.pending_uploads() == []
    assert store.get_recording(recording.recording_id).uploaded is True


# A failed upload must never cost the recording.
def test_a_failed_upload_keeps_the_recording_queued(store, tmp_path):
    output = make_output(tmp_path, "pass-1", {"a.bin": b"x" * 10})
    store.record_recordings(collect("pass-1", output))
    recording = store.pending_uploads()[0]

    store.mark_upload_failed(recording.recording_id, "UNAVAILABLE")
    store.mark_upload_failed(recording.recording_id, "UNAVAILABLE")

    still_pending = store.pending_uploads()
    assert len(still_pending) == 1
    assert still_pending[0].attempts == 2
    assert still_pending[0].last_error == "UNAVAILABLE"
    # And the file is still on disk.
    assert (output / "a.bin").exists()


# Worker restarted with an upload pending: the queue must survive.
def test_the_upload_queue_survives_a_restart(tmp_path):
    output = make_output(tmp_path, "pass-1", {"a.bin": b"x" * 10})

    first = PlanStore(tmp_path / "state")
    first.record_recordings(collect("pass-1", output))
    first.mark_upload_failed(first.pending_uploads()[0].recording_id, "server down")
    first.close()

    second = PlanStore(tmp_path / "state")
    try:
        pending = second.pending_uploads()
        assert len(pending) == 1
        assert pending[0].attempts == 1
    finally:
        second.close()
