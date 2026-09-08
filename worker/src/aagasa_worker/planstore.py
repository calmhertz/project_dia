"""Durable local store for PassPlans and their execution state.

A Worker restart must not erase future work (worker-spec section 7), and the
Worker must keep executing through a Server outage, so this is the Worker's
own source of truth while disconnected.

SQLite is used rather than a JSON file: it gives real transactions and
survives a crash mid-write, which a rewrite-the-whole-file approach does not.
It is in the standard library, so this costs no dependency.
"""

import logging
import sqlite3
import threading
from dataclasses import dataclass
from datetime import datetime, timezone
from pathlib import Path

logger = logging.getLogger(__name__)

DATABASE_FILENAME = "worker_state.db"

# Owner-only: plans describe what the station will do and when.
DATABASE_MODE = 0o600


class ExecutionState:
    """Worker-side execution states (worker-spec section 8)."""

    RECEIVED = "received"
    READY = "ready"
    EXECUTING = "executing"
    COMPLETED = "completed"
    FAILED = "failed"
    MISSED = "missed"
    CANCELLED = "cancelled"


# A pass in a terminal state is never run again (spec.md sections 6.4 and 20).
TERMINAL_STATES = frozenset({
    ExecutionState.COMPLETED,
    ExecutionState.FAILED,
    ExecutionState.MISSED,
    ExecutionState.CANCELLED,
})

# States that still expect execution.
PENDING_STATES = frozenset({ExecutionState.RECEIVED, ExecutionState.READY})

SCHEMA = """
CREATE TABLE IF NOT EXISTS plans (
    pass_id       TEXT PRIMARY KEY,
    generation    TEXT NOT NULL,
    plan_version  INTEGER NOT NULL,
    encoded       BLOB NOT NULL,
    aos_epoch     REAL NOT NULL,
    los_epoch     REAL NOT NULL,
    state         TEXT NOT NULL,
    received_at   REAL NOT NULL,
    updated_at    REAL NOT NULL,
    started_at    REAL,
    finished_at   REAL,
    detail        TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS plans_by_aos ON plans (aos_epoch);
CREATE INDEX IF NOT EXISTS plans_by_state ON plans (state);

CREATE TABLE IF NOT EXISTS meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

-- Append-only local record of what actually happened, kept even after a plan
-- is superseded, so the Worker can report its history after a long outage.
CREATE TABLE IF NOT EXISTS execution_history (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    pass_id     TEXT NOT NULL,
    state       TEXT NOT NULL,
    detail      TEXT NOT NULL DEFAULT '',
    recorded_at REAL NOT NULL,
    synced      INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS history_unsynced ON execution_history (synced, id);
"""


RECORDING_SCHEMA = """
CREATE TABLE IF NOT EXISTS recordings (
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

CREATE INDEX IF NOT EXISTS recordings_pending ON recordings (uploaded, finished_at);
"""


@dataclass(frozen=True)
class StoredPlan:
    """A PassPlan as held locally, with its execution state."""

    pass_id: str
    generation: str
    plan_version: int
    encoded: bytes
    aos: datetime
    los: datetime
    state: str
    received_at: datetime
    updated_at: datetime
    started_at: datetime | None = None
    finished_at: datetime | None = None
    detail: str = ""


@dataclass(frozen=True)
class StoredRecording:
    """A local recording and its upload state."""

    recording_id: str
    pass_id: str
    relative_path: str
    absolute_path: str
    size_bytes: int
    checksum: str
    finished_at: datetime
    uploaded: bool
    attempts: int
    last_error: str
    deleted: bool


@dataclass(frozen=True)
class HistoryEntry:
    """One recorded execution outcome awaiting synchronization."""

    id: int
    pass_id: str
    state: str
    detail: str
    recorded_at: datetime


class PlanStore:
    """The Worker's durable local state."""

    def __init__(self, directory: Path):
        self._path = Path(directory) / DATABASE_FILENAME
        self._path.parent.mkdir(parents=True, exist_ok=True)
        self._lock = threading.RLock()

        self._connection = sqlite3.connect(
            self._path, check_same_thread=False, isolation_level=None
        )
        self._connection.row_factory = sqlite3.Row
        # WAL survives a crash mid-transaction and allows a reader alongside
        # the executor; FULL sync means a committed plan is really on disk.
        self._connection.execute("PRAGMA journal_mode=WAL")
        self._connection.execute("PRAGMA synchronous=FULL")
        self._connection.executescript(SCHEMA)
        self._connection.executescript(RECORDING_SCHEMA)
        self._path.chmod(DATABASE_MODE)

    def close(self) -> None:
        with self._lock:
            self._connection.close()

    # Desired state --------------------------------------------------------

    @property
    def generation(self) -> str:
        """The desired-state generation the Worker currently holds."""
        with self._lock:
            row = self._connection.execute(
                "SELECT value FROM meta WHERE key = 'generation'"
            ).fetchone()
        return row["value"] if row else ""

    def replace_desired_state(self, plans: list[dict], generation: str) -> dict:
        """Reconcile the local store to a new desired state.

        Latest-state reconciliation (spec.md section 6.2). Three rules make
        this safe:

        - a plan already in a terminal state keeps that state, so a completed
          or missed pass is never resurrected by a resync
        - a plan whose content is unchanged keeps its execution state
        - a plan that has vanished from the desired state is cancelled locally
          rather than deleted, so its history survives
        """
        now = _now_epoch()
        summary = {"added": 0, "updated": 0, "unchanged": 0, "cancelled": 0}
        incoming = {plan["pass_id"]: plan for plan in plans}

        with self._lock:
            self._connection.execute("BEGIN IMMEDIATE")
            try:
                existing = {
                    row["pass_id"]: row
                    for row in self._connection.execute(
                        "SELECT pass_id, generation, state FROM plans"
                    )
                }

                for pass_id, plan in incoming.items():
                    current = existing.get(pass_id)
                    if current is None:
                        self._connection.execute(
                            "INSERT INTO plans (pass_id, generation, plan_version, encoded,"
                            " aos_epoch, los_epoch, state, received_at, updated_at)"
                            " VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
                            (pass_id, plan["generation"], plan["plan_version"],
                             plan["encoded"], plan["aos"].timestamp(),
                             plan["los"].timestamp(), ExecutionState.RECEIVED, now, now),
                        )
                        summary["added"] += 1
                        continue

                    if current["state"] in TERMINAL_STATES:
                        # Already run, missed or cancelled: leave it alone.
                        summary["unchanged"] += 1
                        continue

                    if current["generation"] == plan["generation"]:
                        summary["unchanged"] += 1
                        continue

                    self._connection.execute(
                        "UPDATE plans SET generation = ?, plan_version = ?, encoded = ?,"
                        " aos_epoch = ?, los_epoch = ?, state = ?, updated_at = ?"
                        " WHERE pass_id = ?",
                        (plan["generation"], plan["plan_version"], plan["encoded"],
                         plan["aos"].timestamp(), plan["los"].timestamp(),
                         ExecutionState.RECEIVED, now, pass_id),
                    )
                    summary["updated"] += 1

                for pass_id, row in existing.items():
                    if pass_id in incoming or row["state"] in TERMINAL_STATES:
                        continue
                    self._connection.execute(
                        "UPDATE plans SET state = ?, updated_at = ?,"
                        " detail = 'removed from desired state' WHERE pass_id = ?",
                        (ExecutionState.CANCELLED, now, pass_id),
                    )
                    summary["cancelled"] += 1

                self._connection.execute(
                    "INSERT INTO meta (key, value) VALUES ('generation', ?)"
                    " ON CONFLICT(key) DO UPDATE SET value = excluded.value",
                    (generation,),
                )
                self._connection.execute("COMMIT")
            except Exception:
                self._connection.execute("ROLLBACK")
                raise

        logger.info(
            "desired state reconciled added=%d updated=%d unchanged=%d cancelled=%d",
            summary["added"], summary["updated"], summary["unchanged"], summary["cancelled"],
        )
        return summary

    # Queries --------------------------------------------------------------

    def get(self, pass_id: str) -> StoredPlan | None:
        with self._lock:
            row = self._connection.execute(
                "SELECT * FROM plans WHERE pass_id = ?", (pass_id,)
            ).fetchone()
        return _to_plan(row) if row else None

    def pending(self) -> list[StoredPlan]:
        """Plans still awaiting execution, soonest first."""
        placeholders = ",".join("?" * len(PENDING_STATES))
        with self._lock:
            rows = self._connection.execute(
                f"SELECT * FROM plans WHERE state IN ({placeholders}) ORDER BY aos_epoch",
                tuple(sorted(PENDING_STATES)),
            ).fetchall()
        return [_to_plan(row) for row in rows]

    def all_plans(self) -> list[StoredPlan]:
        with self._lock:
            rows = self._connection.execute(
                "SELECT * FROM plans ORDER BY aos_epoch"
            ).fetchall()
        return [_to_plan(row) for row in rows]

    def next_due(self, now: datetime) -> StoredPlan | None:
        """The soonest pending plan whose window has not already closed."""
        placeholders = ",".join("?" * len(PENDING_STATES))
        with self._lock:
            row = self._connection.execute(
                f"SELECT * FROM plans WHERE state IN ({placeholders})"
                " AND los_epoch > ? ORDER BY aos_epoch LIMIT 1",
                (*sorted(PENDING_STATES), now.timestamp()),
            ).fetchone()
        return _to_plan(row) if row else None

    # State transitions ----------------------------------------------------

    def mark_executing(self, pass_id: str, started_at: datetime) -> bool:
        """Claim a plan for execution.

        Conditional on it still being pending, so two executors cannot both
        start the same pass.
        """
        placeholders = ",".join("?" * len(PENDING_STATES))
        with self._lock:
            cursor = self._connection.execute(
                f"UPDATE plans SET state = ?, started_at = ?, updated_at = ?"
                f" WHERE pass_id = ? AND state IN ({placeholders})",
                (ExecutionState.EXECUTING, started_at.timestamp(),
                 _now_epoch(), pass_id, *sorted(PENDING_STATES)),
            )
            claimed = cursor.rowcount == 1
        if claimed:
            self._record_history(pass_id, ExecutionState.EXECUTING, "")
        return claimed

    def mark_finished(self, pass_id: str, state: str, detail: str = "") -> None:
        """Record a terminal outcome."""
        if state not in TERMINAL_STATES:
            raise ValueError(f"{state} is not a terminal state")
        now = _now_epoch()
        with self._lock:
            self._connection.execute(
                "UPDATE plans SET state = ?, finished_at = ?, updated_at = ?, detail = ?"
                " WHERE pass_id = ?",
                (state, now, now, detail, pass_id),
            )
        self._record_history(pass_id, state, detail)
        logger.info("pass %s finished state=%s", pass_id, state)

    def mark_missed_before(self, cutoff: datetime) -> list[str]:
        """Mark every pending plan whose window has closed as missed.

        Called at startup and each loop turn. A missed pass is never replayed
        (spec.md section 6.4), so this is how the Worker closes out work it
        slept or crashed through.
        """
        placeholders = ",".join("?" * len(PENDING_STATES))
        with self._lock:
            rows = self._connection.execute(
                f"SELECT pass_id FROM plans WHERE state IN ({placeholders}) AND los_epoch <= ?",
                (*sorted(PENDING_STATES), cutoff.timestamp()),
            ).fetchall()
            missed = [row["pass_id"] for row in rows]

        for pass_id in missed:
            self.mark_finished(pass_id, ExecutionState.MISSED, "execution window closed")
        if missed:
            logger.warning("marked %d pass(es) missed", len(missed))
        return missed

    def recover_interrupted(self) -> list[str]:
        """Close out passes that were executing when the Worker stopped.

        spec.md section 20: after a crash the Worker does not resume a pass
        midway. It records the interruption and moves on to the next one.
        """
        with self._lock:
            rows = self._connection.execute(
                "SELECT pass_id FROM plans WHERE state = ?", (ExecutionState.EXECUTING,)
            ).fetchall()
            interrupted = [row["pass_id"] for row in rows]

        for pass_id in interrupted:
            self.mark_finished(pass_id, ExecutionState.FAILED,
                               "worker restarted while the pass was executing")
        if interrupted:
            logger.warning("recovered %d interrupted pass(es)", len(interrupted))
        return interrupted

    # History --------------------------------------------------------------

    def _record_history(self, pass_id: str, state: str, detail: str) -> None:
        with self._lock:
            self._connection.execute(
                "INSERT INTO execution_history (pass_id, state, detail, recorded_at)"
                " VALUES (?, ?, ?, ?)",
                (pass_id, state, detail, _now_epoch()),
            )

    def unsynced_history(self, limit: int = 500) -> list[HistoryEntry]:
        """Outcomes not yet reported to the Server."""
        with self._lock:
            rows = self._connection.execute(
                "SELECT id, pass_id, state, detail, recorded_at FROM execution_history"
                " WHERE synced = 0 ORDER BY id LIMIT ?", (limit,),
            ).fetchall()
        return [
            HistoryEntry(
                id=row["id"], pass_id=row["pass_id"], state=row["state"],
                detail=row["detail"], recorded_at=_to_datetime(row["recorded_at"]),
            )
            for row in rows
        ]

    def mark_history_synced(self, entry_ids: list[int]) -> None:
        if not entry_ids:
            return
        placeholders = ",".join("?" * len(entry_ids))
        with self._lock:
            self._connection.execute(
                f"UPDATE execution_history SET synced = 1 WHERE id IN ({placeholders})",
                tuple(entry_ids),
            )

    def history_for(self, pass_id: str) -> list[HistoryEntry]:
        with self._lock:
            rows = self._connection.execute(
                "SELECT id, pass_id, state, detail, recorded_at FROM execution_history"
                " WHERE pass_id = ? ORDER BY id", (pass_id,),
            ).fetchall()
        return [
            HistoryEntry(
                id=row["id"], pass_id=row["pass_id"], state=row["state"],
                detail=row["detail"], recorded_at=_to_datetime(row["recorded_at"]),
            )
            for row in rows
        ]


    # Identity -------------------------------------------------------------

    def load_identity(self):
        """The cached identity, or None before the first registration."""
        from aagasa_worker.identity import Identity

        with self._lock:
            rows = {
                row["key"]: row["value"]
                for row in self._connection.execute(
                    "SELECT key, value FROM meta WHERE key IN"
                    " ('worker_id', 'station_id', 'station_name')"
                )
            }
        if not rows.get("worker_id") or not rows.get("station_id"):
            return None
        return Identity(
            worker_id=rows["worker_id"], station_id=rows["station_id"],
            station_name=rows.get("station_name", ""),
        )

    def save_identity(self, identity) -> None:
        """Cache the identity so a restart during an outage still knows it."""
        with self._lock:
            for key, value in (
                ("worker_id", identity.worker_id),
                ("station_id", identity.station_id),
                ("station_name", identity.station_name),
            ):
                self._connection.execute(
                    "INSERT INTO meta (key, value) VALUES (?, ?)"
                    " ON CONFLICT(key) DO UPDATE SET value = excluded.value",
                    (key, value),
                )

    # Recordings -----------------------------------------------------------

    def record_recordings(self, recordings: list) -> int:
        """Register recordings a pass produced, ready for upload.

        Registering the same recording twice is a no-op: the deterministic id
        is the primary key, so a re-scan after a restart does not duplicate
        work.
        """
        now = _now_epoch()
        added = 0
        with self._lock:
            for recording in recordings:
                cursor = self._connection.execute(
                    "INSERT OR IGNORE INTO recordings (recording_id, pass_id, relative_path,"
                    " absolute_path, size_bytes, checksum, finished_at)"
                    " VALUES (?, ?, ?, ?, ?, ?, ?)",
                    (recording.recording_id, recording.pass_id, recording.relative_path,
                     str(recording.absolute_path), recording.size_bytes,
                     recording.checksum_sha256, now),
                )
                added += cursor.rowcount
        if added:
            logger.info("registered %d recording(s) for upload", added)
        return added

    def pending_uploads(self, limit: int = 50) -> list["StoredRecording"]:
        """Recordings not yet confirmed by the Server, oldest first."""
        with self._lock:
            rows = self._connection.execute(
                "SELECT * FROM recordings WHERE uploaded = 0 AND deleted = 0"
                " ORDER BY finished_at LIMIT ?", (limit,),
            ).fetchall()
        return [_to_recording(row) for row in rows]

    def mark_uploaded(self, recording_id: str) -> None:
        """Record the Server's acknowledgement."""
        with self._lock:
            self._connection.execute(
                "UPDATE recordings SET uploaded = 1, uploaded_at = ?, last_error = ''"
                " WHERE recording_id = ?", (_now_epoch(), recording_id),
            )

    def mark_upload_failed(self, recording_id: str, reason: str) -> None:
        """Note a failed attempt. The local copy is kept regardless
        (worker-spec section 15)."""
        with self._lock:
            self._connection.execute(
                "UPDATE recordings SET attempts = attempts + 1, last_error = ?"
                " WHERE recording_id = ?", (reason[:500], recording_id),
            )

    def cleanup_candidates(self) -> list["StoredRecording"]:
        """Stored recordings whose local copy is still present."""
        with self._lock:
            rows = self._connection.execute(
                "SELECT * FROM recordings WHERE deleted = 0 ORDER BY finished_at"
            ).fetchall()
        return [_to_recording(row) for row in rows]

    def mark_deleted(self, recording_id: str) -> None:
        """Note that the local copy is gone. The metadata row stays, so the
        Worker still knows the recording existed."""
        with self._lock:
            self._connection.execute(
                "UPDATE recordings SET deleted = 1 WHERE recording_id = ?", (recording_id,),
            )

    def get_recording(self, recording_id: str):
        with self._lock:
            row = self._connection.execute(
                "SELECT * FROM recordings WHERE recording_id = ?", (recording_id,)
            ).fetchone()
        return _to_recording(row) if row else None


def _now_epoch() -> float:
    return datetime.now(timezone.utc).timestamp()


def _to_datetime(epoch: float | None) -> datetime | None:
    if epoch is None:
        return None
    return datetime.fromtimestamp(epoch, tz=timezone.utc)


def _to_plan(row: sqlite3.Row) -> StoredPlan:
    return StoredPlan(
        pass_id=row["pass_id"],
        generation=row["generation"],
        plan_version=row["plan_version"],
        encoded=row["encoded"],
        aos=_to_datetime(row["aos_epoch"]),
        los=_to_datetime(row["los_epoch"]),
        state=row["state"],
        received_at=_to_datetime(row["received_at"]),
        updated_at=_to_datetime(row["updated_at"]),
        started_at=_to_datetime(row["started_at"]),
        finished_at=_to_datetime(row["finished_at"]),
        detail=row["detail"],
    )


def _to_recording(row) -> StoredRecording:
    return StoredRecording(
        recording_id=row["recording_id"], pass_id=row["pass_id"],
        relative_path=row["relative_path"], absolute_path=row["absolute_path"],
        size_bytes=row["size_bytes"], checksum=row["checksum"],
        finished_at=_to_datetime(row["finished_at"]),
        uploaded=bool(row["uploaded"]), attempts=row["attempts"],
        last_error=row["last_error"], deleted=bool(row["deleted"]),
    )

