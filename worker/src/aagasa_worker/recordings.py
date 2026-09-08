"""Local recording management: identity, retention and the upload queue.

The Worker never deletes a recording merely because an upload failed
(worker-spec section 15). Local copies are kept for at least twelve hours, and
only disk pressure above ninety percent may remove something younger
(spec.md section 19.2).
"""

import hashlib
import logging
import shutil
from dataclasses import dataclass
from datetime import datetime, timedelta, timezone
from pathlib import Path

logger = logging.getLogger(__name__)

# spec.md section 19.2.
MINIMUM_RETENTION = timedelta(hours=12)
DISK_PRESSURE_THRESHOLD = 0.90

# Read size when hashing and uploading. Large enough to be efficient, small
# enough that a chunk fits comfortably in a gRPC message.
CHUNK_BYTES = 1 << 20  # 1 MiB


@dataclass(frozen=True)
class LocalRecording:
    """One captured file on the Worker's disk."""

    recording_id: str
    pass_id: str
    relative_path: str
    absolute_path: Path
    size_bytes: int
    checksum_sha256: str


def recording_id_for(pass_id: str, relative_path: str) -> str:
    """Derive the deterministic identity of a recording.

    Identity is the pass plus the file's place within it, so a retry after a
    lost acknowledgement produces exactly the same id and the Server can
    recognise it (spec.md section 19.3). Content is deliberately not part of
    the id: a partially written file must not become a different recording.
    """
    digest = hashlib.sha256()
    digest.update(pass_id.encode("utf-8"))
    digest.update(b"\x00")
    digest.update(relative_path.encode("utf-8"))
    return digest.hexdigest()


def checksum_of(path: Path) -> str:
    """SHA-256 of a file's content, read in chunks."""
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        while True:
            chunk = handle.read(CHUNK_BYTES)
            if not chunk:
                break
            digest.update(chunk)
    return digest.hexdigest()


def collect(pass_id: str, output_directory: Path) -> list[LocalRecording]:
    """Find the files a pass produced.

    A pass can produce several outputs; each becomes its own recording so a
    partial result is still uploadable.
    """
    directory = Path(output_directory)
    if not directory.is_dir():
        return []

    recordings = []
    for path in sorted(directory.rglob("*")):
        if not path.is_file():
            continue
        # Skip files still being written by a capture that has not finalised.
        if path.name.startswith("."):
            continue
        relative = path.relative_to(directory).as_posix()
        try:
            size = path.stat().st_size
        except OSError as error:
            logger.warning("skipping unreadable output %s: %s", path, error)
            continue
        if size == 0:
            continue
        recordings.append(LocalRecording(
            recording_id=recording_id_for(pass_id, relative),
            pass_id=pass_id,
            relative_path=relative,
            absolute_path=path,
            size_bytes=size,
            checksum_sha256=checksum_of(path),
        ))
    return recordings


def disk_usage_ratio(path: Path) -> float:
    """Fraction of the filesystem in use, 0.0 to 1.0."""
    usage = shutil.disk_usage(path)
    if usage.total == 0:
        return 0.0
    return usage.used / usage.total


@dataclass(frozen=True)
class CleanupCandidate:
    """A stored recording considered for deletion."""

    recording_id: str
    absolute_path: Path
    finished_at: datetime
    uploaded: bool


def select_for_cleanup(candidates: list[CleanupCandidate], now: datetime,
                       under_pressure: bool) -> list[CleanupCandidate]:
    """Choose which local recordings to delete, in order.

    Two rules, from spec.md section 19.2 and worker-spec section 16:

    - Normally only recordings past the twelve hour retention that the Server
      has confirmed may go, oldest first.
    - Above ninety percent usage, older local data may go early to keep the
      Worker running, but a recording the Server has not confirmed is still
      the last thing considered, because losing it would lose it for good.
    """
    confirmed_expired = []
    confirmed_recent = []
    unconfirmed = []

    for candidate in candidates:
        expired = now - candidate.finished_at >= MINIMUM_RETENTION
        if candidate.uploaded and expired:
            confirmed_expired.append(candidate)
        elif candidate.uploaded:
            confirmed_recent.append(candidate)
        else:
            unconfirmed.append(candidate)

    def by_age(group: list[CleanupCandidate]) -> list[CleanupCandidate]:
        return sorted(group, key=lambda item: item.finished_at)

    order = by_age(confirmed_expired)
    if under_pressure:
        # Reach past the retention window only to keep the station working,
        # and take confirmed copies before anything unconfirmed.
        order = order + by_age(confirmed_recent) + by_age(unconfirmed)
    return order


def delete_recording(path: Path) -> bool:
    """Remove a stored recording file, reporting whether it went."""
    try:
        path.unlink()
        logger.info("deleted local recording %s", path)
        return True
    except FileNotFoundError:
        return True
    except OSError as error:
        logger.error("could not delete %s: %s", path, error)
        return False


def prune_empty_directories(root: Path) -> None:
    """Remove pass directories left empty by cleanup."""
    if not root.is_dir():
        return
    for directory in sorted(root.rglob("*"), reverse=True):
        if directory.is_dir():
            try:
                next(directory.iterdir())
            except StopIteration:
                try:
                    directory.rmdir()
                except OSError:
                    pass
            except OSError:
                pass


def read_chunks(path: Path, chunk_bytes: int = CHUNK_BYTES):
    """Yield a file's content in upload-sized pieces."""
    with path.open("rb") as handle:
        while True:
            chunk = handle.read(chunk_bytes)
            if not chunk:
                return
            yield chunk


def free_bytes(path: Path) -> int:
    """Bytes still available on the filesystem holding path."""
    return shutil.disk_usage(path).free


def ensure_within(root: Path, candidate: Path) -> bool:
    """Confirm a path really sits under the recordings root.

    Deletion is destructive, so the check is explicit rather than assumed.
    """
    try:
        resolved_root = root.resolve()
        resolved = candidate.resolve()
    except OSError:
        return False
    return resolved == resolved_root or resolved_root in resolved.parents


__all__ = [
    "CHUNK_BYTES",
    "DISK_PRESSURE_THRESHOLD",
    "MINIMUM_RETENTION",
    "CleanupCandidate",
    "LocalRecording",
    "checksum_of",
    "collect",
    "delete_recording",
    "disk_usage_ratio",
    "ensure_within",
    "free_bytes",
    "prune_empty_directories",
    "read_chunks",
    "recording_id_for",
    "select_for_cleanup",
]
