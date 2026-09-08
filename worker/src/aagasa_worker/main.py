"""Entrypoint for the Aagasa Worker.

V1 runs the connectivity loop: prepare local state, then heartbeat the Server,
backing off while it is unreachable. Pass execution arrives in V9/V10.
"""

import logging
import signal
import sys
import threading

import grpc

from aagasa_worker import __version__
from aagasa_worker.backoff import MIN_DELAY_SECONDS, next_delay
from aagasa_worker.config import Config, load_config
from aagasa_worker.executor import Executor
from aagasa_worker.hardware.cli import build_station
from aagasa_worker.identity import IdentityResolver
from aagasa_worker.pass_execution import StationPassHandler
from aagasa_worker.planstore import PlanStore
from aagasa_worker.server_client import ServerClient
from aagasa_worker.storage import prepare_directories
from aagasa_worker.sync import DesiredStateSync
from aagasa_worker.uploads import RecordingUploader

logger = logging.getLogger("aagasa_worker")


class Worker:
    """Owns the Worker's Server connection and desired-state synchronization.

    Execution runs in its own loop and does not depend on this one: a Server
    outage stops synchronization, never execution (spec.md section 6.1).
    """

    def __init__(self, config: Config, client: ServerClient, store: PlanStore,
                 sync: DesiredStateSync, uploader: RecordingUploader,
                 identity: IdentityResolver):
        self._config = config
        self._client = client
        self._store = store
        self._sync = sync
        self._uploader = uploader
        self._identity = identity
        self._stop = threading.Event()
        self._connected = False
        # Registration happens once per run; see _reconcile.
        self._registered = False

    def stop(self) -> None:
        self._stop.set()

    def run(self) -> None:
        """Stay in touch with the Server until stopped.

        Reconnecting is one ordered cycle: resolve identity, report what
        happened offline, push recordings, then reconcile the desired state
        (spec.md section 6.2). Execution runs elsewhere and is untouched by any
        of it.
        """
        retry_delay = MIN_DELAY_SECONDS
        while not self._stop.is_set():
            try:
                self._reconcile()
                if not self._connected:
                    logger.info("server connected station=%s",
                                self._identity.current.station_name or "<unknown>")
                    self._connected = True
                retry_delay = MIN_DELAY_SECONDS
                wait_seconds = self._config.heartbeat_interval_seconds
            except grpc.RpcError as error:
                if self._connected:
                    logger.warning("server connection lost code=%s", error.code().name)
                    self._connected = False
                else:
                    logger.debug("server unreachable code=%s", error.code().name)
                wait_seconds = retry_delay
                retry_delay = next_delay(retry_delay)
            except Exception:  # noqa: BLE001
                # The Worker must keep executing whatever goes wrong here.
                # Synchronization is a convenience; execution is the job.
                logger.exception("synchronization cycle failed; continuing")
                wait_seconds = retry_delay
                retry_delay = next_delay(retry_delay)

            self._stop.wait(wait_seconds)

    def _reconcile(self) -> None:
        """One pass of the reconnect cycle."""
        identity = self._identity.current
        # Register once per run even when the identity is already cached.
        # Registration is how the Server learns what this station's SatDump
        # provides, and SatDump can be reinstalled while the Worker is down.
        # A cached identity still covers an outage: only the report is lost.
        if not identity.resolved or not self._registered:
            identity = self._identity.resolve()
            self._client.identity = identity
            self._registered = True

        # Results first: they were gathered locally and only the Server can
        # keep them permanently.
        self._uploader.report_outcomes()
        self._uploader.upload_pending()

        pending_uploads = len(self._store.pending_uploads(limit=1000))
        pending_reports = len(self._store.unsynced_history(limit=1000))

        response = self._client.heartbeat(
            state_generation=self._store.generation,
            pending_uploads=pending_uploads,
            pending_reports=pending_reports,
        )
        # Only pull the desired state when the Server says it has moved.
        if not response.state_is_current:
            self._sync.sync_once()

        self._uploader.enforce_retention()


def main() -> int:
    try:
        config = load_config()
    except ValueError as exc:
        print(f"configuration error: {exc}", file=sys.stderr)
        return 1

    logging.basicConfig(
        level=getattr(logging, config.log_level, logging.INFO),
        format="%(asctime)s %(levelname)s %(name)s %(message)s",
    )

    try:
        prepare_directories(config.state_directory, config.recordings_directory)
    except OSError as exc:
        logger.error("local storage unavailable: %s", exc)
        return 1

    logger.info("starting worker version=%s name=%s", __version__, config.worker_name)

    # After any restart, expected or abnormal, the antenna position is unknown.
    # Establish a known safe state before doing anything else, and keep running
    # even if a device is missing so the Worker can still report its state
    # (worker-spec sections 11.1 and 25).
    station = build_station(config)
    status = station.safe_startup()
    logger.info(
        "hardware ready rotator=%s sdr=%s satdump=%s pipelines=%d",
        status.rotator_available, status.sdr_available,
        status.satdump_available, status.pipeline_count,
    )

    # The durable local store is what lets the Worker keep executing when the
    # Server is unreachable (worker-spec section 7).
    store = PlanStore(config.state_directory)
    logger.info("local state loaded plans=%d generation=%s",
                len(store.all_plans()), store.generation or "<none>")

    client = ServerClient(config)
    def available_pipelines() -> list[str]:
        """Identifiers this station's SatDump provides, read fresh."""
        try:
            return sorted(p.identifier for p in station.satdump.pipelines())
        except Exception:  # noqa: BLE001 - a missing SatDump must not stop us
            logger.warning("could not list satdump pipelines")
            return []

    identity = IdentityResolver(client, store, config.worker_name,
                                pipelines=available_pipelines)
    # A cached identity lets a Worker restarted during an outage keep working.
    client.identity = identity.current if identity.current.resolved else None
    uploader = RecordingUploader(client, store, config.recordings_directory)

    # Drives the rotator, the SDR and SatDump for each pass.
    handler = StationPassHandler(station, config.recordings_directory)
    executor = Executor(store, handler, on_finished=uploader.register_pass_output)
    execution_thread = threading.Thread(
        target=executor.run, name="pass-executor", daemon=True)
    execution_thread.start()

    worker = Worker(config, client, store,
                    DesiredStateSync(client, store), uploader, identity)

    received = {}

    # A signal handler must not log: it can interrupt the main thread while it
    # holds the stream lock, which raises a reentrant-call error.
    def handle_signal(signum, _frame):
        received.setdefault("signal", signum)
        worker.stop()
        executor.stop()

    signal.signal(signal.SIGINT, handle_signal)
    signal.signal(signal.SIGTERM, handle_signal)

    try:
        worker.run()
    finally:
        executor.stop()
        execution_thread.join(timeout=30)
        station.safe_shutdown()
        client.close()
        store.close()
        logger.info("worker stopped signal=%s", received.get("signal"))
    return 0


if __name__ == "__main__":
    sys.exit(main())
