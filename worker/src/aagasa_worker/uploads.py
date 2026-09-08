"""Uploading recordings and reporting outcomes, plus local retention.

A failed upload never costs a recording: the local copy stays and the attempt
is retried later (worker-spec section 15). Only the retention rules delete
anything, and they prefer copies the Server has already confirmed.
"""

import logging
from datetime import datetime, timezone
from pathlib import Path

import grpc

from aagasa_worker.planstore import PlanStore
from aagasa_worker.recordings import (
    DISK_PRESSURE_THRESHOLD,
    CleanupCandidate,
    collect,
    delete_recording,
    disk_usage_ratio,
    ensure_within,
    prune_empty_directories,
    select_for_cleanup,
)

logger = logging.getLogger(__name__)


class RecordingUploader:
    """Moves completed recordings to the Server and prunes local copies."""

    def __init__(self, client, store: PlanStore, recordings_directory: Path, clock=None):
        self._client = client
        self._store = store
        self._recordings = Path(recordings_directory)
        self._clock = clock or (lambda: datetime.now(timezone.utc))

    def register_pass_output(self, pass_id: str) -> int:
        """Scan a finished pass's output and queue it for upload."""
        directory = self._recordings / pass_id
        found = collect(pass_id, directory)
        if not found:
            logger.info("pass %s produced no output to upload", pass_id)
            return 0
        return self._store.record_recordings(found)

    def upload_pending(self, limit: int = 10) -> dict:
        """Upload queued recordings.

        Raises grpc.RpcError when the Server is unreachable, so the caller can
        back off. Everything already uploaded stays uploaded.
        """
        summary = {"uploaded": 0, "already_held": 0, "failed": 0}

        for recording in self._store.pending_uploads(limit=limit):
            path = Path(recording.absolute_path)
            if not path.is_file():
                # The file went while the metadata stayed; nothing to send.
                logger.warning("queued recording %s is missing from disk",
                               recording.recording_id)
                self._store.mark_deleted(recording.recording_id)
                continue

            try:
                response = self._client.upload_recording(recording, path)
            except grpc.RpcError as error:
                # An unreachable Server is not a recording problem. Note it and
                # let the caller decide when to try again.
                self._store.mark_upload_failed(recording.recording_id, error.code().name)
                raise
            except OSError as error:
                self._store.mark_upload_failed(recording.recording_id, str(error))
                summary["failed"] += 1
                continue

            # Both outcomes mean the Server holds it, so stop retrying.
            self._store.mark_uploaded(recording.recording_id)
            if response.stored:
                summary["uploaded"] += 1
            else:
                summary["already_held"] += 1
                logger.info("recording %s was already held by the server",
                            recording.recording_id)

        return summary

    def report_outcomes(self, limit: int = 200) -> int:
        """Send execution results gathered locally, including while offline."""
        entries = self._store.unsynced_history(limit=limit)
        if not entries:
            return 0

        response = self._client.report_executions(entries)
        # Only mark them synced once the Server has accepted them.
        self._store.mark_history_synced([entry.id for entry in entries])
        logger.info("reported %d execution record(s), accepted=%d",
                    len(entries), response.accepted)
        return len(entries)

    def enforce_retention(self) -> dict:
        """Apply the retention and disk-pressure rules.

        Normally only confirmed recordings past twelve hours go. Above ninety
        percent usage, older local data may go early to keep the Worker
        running (spec.md section 19.2).
        """
        usage = disk_usage_ratio(self._recordings)
        under_pressure = usage >= DISK_PRESSURE_THRESHOLD

        candidates = [
            CleanupCandidate(
                recording_id=record.recording_id,
                absolute_path=Path(record.absolute_path),
                finished_at=record.finished_at,
                uploaded=record.uploaded,
            )
            for record in self._store.cleanup_candidates()
        ]

        order = select_for_cleanup(candidates, self._clock(), under_pressure)
        summary = {"usage": round(usage, 4), "under_pressure": under_pressure, "deleted": 0}

        for candidate in order:
            # Deletion is destructive, so confirm the path really is ours.
            if not ensure_within(self._recordings, candidate.absolute_path):
                logger.error("refusing to delete %s: outside the recordings root",
                             candidate.absolute_path)
                continue
            if not delete_recording(candidate.absolute_path):
                continue
            self._store.mark_deleted(candidate.recording_id)
            summary["deleted"] += 1

            if under_pressure and disk_usage_ratio(self._recordings) < DISK_PRESSURE_THRESHOLD:
                # Enough room recovered; stop rather than clearing everything.
                break

        if summary["deleted"]:
            prune_empty_directories(self._recordings)
            logger.info("retention removed %d recording(s) usage=%.1f%% pressure=%s",
                        summary["deleted"], usage * 100, under_pressure)
        return summary
