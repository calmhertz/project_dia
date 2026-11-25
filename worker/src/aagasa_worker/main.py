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
from aagasa_worker.antenna import AntennaModeStore
from aagasa_worker.backoff import MIN_DELAY_SECONDS, next_delay
from aagasa_worker.config import Config, load_config
from aagasa_worker.executor import Executor
from aagasa_worker.hardware.cli import build_station
from aagasa_worker.identity import IdentityResolver
from aagasa_worker.pass_execution import StationPassHandler, capture_start
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
                 identity: IdentityResolver, station=None,
                 antenna_store: AntennaModeStore | None = None,
                 current_pass=None):
        """current_pass returns the id of the pass executing now, or "".

        It is read on every heartbeat so the Server can tell a busy station
        from an idle one.
        """
        self._config = config
        self._client = client
        self._store = store
        self._sync = sync
        self._uploader = uploader
        self._identity = identity
        self._station = station
        self._antenna_store = antenna_store
        self._current_pass = current_pass or (lambda: "")
        self._stop = threading.Event()
        self._connected = False
        self._registered = False
        self._upload_retry_delay = MIN_DELAY_SECONDS

    def stop(self) -> None:
        self._stop.set()

    def run(self) -> None:
        """Stay in touch with the Server until stopped.

        Reconnecting is one ordered cycle: resolve identity, report what
        happened offline, then reconcile the desired state (spec.md section
        6.2). Recordings are pushed by run_uploads on its own thread, and
        execution runs elsewhere; neither can hold up a heartbeat.
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
                logger.exception("synchronization cycle failed; continuing")
                wait_seconds = retry_delay
                retry_delay = next_delay(retry_delay)

            # Outside the reconcile step on purpose: the disk fills fastest
            # while the Server is down, which is exactly when reconcile raises.
            self._enforce_retention()

            self._stop.wait(wait_seconds)

    def _enforce_retention(self) -> None:
        try:
            self._uploader.enforce_retention()
        except Exception:  # noqa: BLE001 - retention must never stop the loop
            logger.exception("retention check failed; continuing")

    def run_uploads(self) -> None:
        """Push recordings to the Server until stopped.

        Runs on its own thread so a long upload never delays a heartbeat: the
        Server marks a Worker offline after about fifteen seconds of silence,
        and one baseband file can take far longer than that to send.
        """
        logger.info("upload loop started")
        while not self._stop.is_set():
            self._stop.wait(self.upload_once())
        logger.info("upload loop stopped")

    def upload_once(self) -> float:
        """One round of uploads; returns how long to wait before the next."""
        idle_wait = self._config.heartbeat_interval_seconds
        if not self._connected:
            # The heartbeat loop owns identity and reconnection; uploading
            # before it has succeeded would only fail or go out unattributed.
            self._upload_retry_delay = MIN_DELAY_SECONDS
            return idle_wait
        try:
            summary = self._uploader.upload_pending()
        except grpc.RpcError as error:
            logger.debug("upload paused code=%s", error.code().name)
            delay = self._upload_retry_delay
            self._upload_retry_delay = next_delay(delay)
            return delay
        except Exception:  # noqa: BLE001 - the loop must survive
            logger.exception("upload round failed; continuing")
            return MIN_DELAY_SECONDS

        self._upload_retry_delay = MIN_DELAY_SECONDS
        progressed = summary["uploaded"] + summary["already_held"] + summary["rejected"]
        if summary.get("deferred") or progressed:
            return 1.0
        return idle_wait

    def _reconcile(self) -> None:
        """One pass of the reconnect cycle."""
        identity = self._identity.current
        if not identity.resolved or not self._registered:
            identity = self._identity.resolve()
            self._client.identity = identity
            self._registered = True

        self._uploader.report_outcomes()

        pending_uploads = len(self._store.pending_uploads(limit=1000))
        pending_reports = len(self._store.unsynced_history(limit=1000))

        response = self._client.heartbeat(
            state_generation=self._store.generation,
            pending_uploads=pending_uploads,
            pending_reports=pending_reports,
            current_pass_id=self._current_pass() or "",
        )
        self._apply_antenna_mode(response.fixed_antenna)
        if not response.state_is_current:
            self._sync.sync_once()

    def _apply_antenna_mode(self, fixed_antenna: bool) -> None:
        """Follow the Server's rotator setting and remember it."""
        station = self._station
        if station is None or station.fixed_antenna == bool(fixed_antenna):
            return
        station.fixed_antenna = fixed_antenna
        if self._antenna_store is not None:
            try:
                self._antenna_store.save(fixed_antenna)
            except OSError as error:
                logger.error("could not remember the antenna mode: %s", error)
        if fixed_antenna:
            logger.info("antenna mode changed: fixed antenna, the rotator is no longer used")
            return
        logger.info("antenna mode changed: rotator in use")
        if not station.capture_active:
            try:
                station.park_if_rotator()
                logger.info("rotator parked")
            except Exception as error:  # noqa: BLE001 - a missing rotator is reported, not fatal
                logger.warning("rotator could not be parked: %s", error)


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

    antenna_store = AntennaModeStore(config.state_directory)
    station = build_station(config)
    station.fixed_antenna = antenna_store.load()
    status = station.safe_startup()
    logger.info(
        "hardware ready rotator=%s sdr=%s satdump=%s pipelines=%d",
        "fixed-antenna" if station.fixed_antenna else status.rotator_available,
        status.sdr_available,
        status.satdump_available, status.pipeline_count,
    )

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
    client.identity = identity.current if identity.current.resolved else None
    uploader = RecordingUploader(client, store, config.recordings_directory)

    handler = StationPassHandler(station, config.recordings_directory)
    executor = Executor(store, handler, on_finished=uploader.register_pass_output,
                        start_of=capture_start)
    execution_thread = threading.Thread(
        target=executor.run, name="pass-executor", daemon=True)
    execution_thread.start()

    worker = Worker(config, client, store,
                    DesiredStateSync(client, store), uploader, identity,
                    station=station, antenna_store=antenna_store,
                    current_pass=lambda: executor.current_pass_id)
    upload_thread = threading.Thread(
        target=worker.run_uploads, name="recording-uploads", daemon=True)
    upload_thread.start()

    signal_number = None

    def handle_signal(signum, _frame):
        nonlocal signal_number
        signal_number = signum
        worker.stop()
        executor.stop()

    signal.signal(signal.SIGINT, handle_signal)
    signal.signal(signal.SIGTERM, handle_signal)

    try:
        worker.run()
    finally:
        executor.stop()
        execution_thread.join(timeout=30)
        upload_thread.join(timeout=5)
        station.safe_shutdown()
        client.close()
        store.close()
        logger.info("worker stopped signal=%s", signal_number)
    return 0


if __name__ == "__main__":
    sys.exit(main())
