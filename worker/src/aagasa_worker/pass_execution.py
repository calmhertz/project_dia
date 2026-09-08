"""Executing a PassPlan against the physical station.

The order matters and follows plan.md V10: validate the pass is still
executable, verify hardware, pre-position the antenna, start capture at the
recording margin, track through the pass, stop after the post-roll, then park.

Every step reports rather than raises where it can, because a partial failure
must still end with the antenna parked and the radio released.
"""

import logging
import threading
from dataclasses import dataclass, field
from datetime import datetime, timezone
from pathlib import Path

from aagasa.worker.v1 import passplan_pb2

from aagasa_worker.executor import ExecutionOutcome
from aagasa_worker.hardware.errors import HardwareError, RotatorError
from aagasa_worker.hardware.satdump import ProcessResult
from aagasa_worker.hardware.station import Station
from aagasa_worker.planstore import StoredPlan
from aagasa_worker.recordings import free_bytes

logger = logging.getLogger(__name__)

# Raw capture writes two bytes per sample per channel, I and Q.
BYTES_PER_SAMPLE = 4

# Headroom kept free beyond the pass itself, so a refused pass still leaves
# room for the recordings already waiting to upload.
RESERVE_BYTES = 1 << 30


# Highest plan schema version this Worker can execute.
SUPPORTED_PLAN_VERSION = 1

# How long after the recording window the Worker waits for SatDump to finish
# on its own before stopping it.
SATDUMP_GRACE_SECONDS = 15

# Pointing is only re-commanded when the antenna needs to move at least this
# far, so a slow pass does not stream redundant serial writes at the rotator.
MIN_POINTING_CHANGE_DEGREES = 1.0


@dataclass
class ExecutionReport:
    """What happened during one pass, for the local history."""

    pointing_commands: int = 0
    capture_started: bool = False
    capture_result: ProcessResult | None = None
    output_path: str = ""
    output_bytes: int = 0
    pointing_failures: int = 0
    problems: list[str] = field(default_factory=list)

    def summary(self) -> str:
        parts = [f"pointing_commands={self.pointing_commands}"]
        if self.pointing_failures:
            parts.append(f"pointing_failures={self.pointing_failures}")
        if self.capture_started:
            parts.append(f"output={self.output_path}")
            parts.append(f"bytes={self.output_bytes}")
            if self.capture_result is not None:
                parts.append(f"capture_exit={self.capture_result.exit_code}")
        else:
            parts.append("capture=not started")
        if self.problems:
            parts.append("problems=" + "; ".join(self.problems))
        return " ".join(parts)


class PlanNotExecutable(Exception):
    """The plan cannot be run, and the reason is not a hardware fault."""


class StationPassHandler:
    """Runs a PassPlan against the real station.

    Replaces the placeholder handler from V9. The executor still owns *when* a
    pass runs; this owns *how*.
    """

    def __init__(self, station: Station, recordings_directory: Path,
                 clock=None, sleeper=None):
        self._station = station
        self._recordings = Path(recordings_directory)
        self._clock = clock or (lambda: datetime.now(timezone.utc))
        # Injected so tests can run a pass without waiting for real time.
        self._sleep = sleeper or (lambda seconds, stop: stop.wait(seconds))

    def execute(self, stored: StoredPlan, stop: threading.Event) -> ExecutionOutcome:
        report = ExecutionReport()
        try:
            plan = self._decode(stored)
            self._validate(plan, stored)
        except PlanNotExecutable as error:
            logger.error("pass %s is not executable: %s", stored.pass_id, error)
            return ExecutionOutcome(succeeded=False, detail=str(error))

        try:
            return self._run(plan, stored, stop, report)
        finally:
            # Whatever happened, leave the station safe.
            self._safe_finish(report)

    # Steps ----------------------------------------------------------------

    def _decode(self, stored: StoredPlan) -> passplan_pb2.PassPlan:
        plan = passplan_pb2.PassPlan()
        try:
            plan.ParseFromString(stored.encoded)
        except Exception as error:  # noqa: BLE001 - protobuf raises assorted types
            raise PlanNotExecutable(f"stored plan is unreadable: {error}") from error
        return plan

    def _validate(self, plan: passplan_pb2.PassPlan, stored: StoredPlan) -> None:
        """Step 1: confirm the pass is still executable."""
        if not 1 <= plan.plan_version <= SUPPORTED_PLAN_VERSION:
            raise PlanNotExecutable(
                f"plan version {plan.plan_version} is not supported by this worker")

        now = self._clock()
        recording_end = _to_datetime(plan.recording_end)
        if recording_end <= now:
            raise PlanNotExecutable("the recording window has already closed")

        if not plan.track:
            # Without pointing the antenna would sit still through the pass.
            raise PlanNotExecutable("the plan carries no pointing timeline")

        if plan.radio.frequency_hz <= 0 or plan.radio.sample_rate_hz <= 0:
            raise PlanNotExecutable("the plan carries no usable radio settings")

    def _run(self, plan: passplan_pb2.PassPlan, stored: StoredPlan,
             stop: threading.Event, report: ExecutionReport) -> ExecutionOutcome:
        # Step 2: verify hardware before committing to the pass.
        status = self._station.probe()
        if not status.rotator_available:
            report.problems.append(f"rotator unavailable: {status.rotator_error}")
        if not status.sdr_available:
            report.problems.append(f"sdr unavailable: {status.sdr_error}")
        if not status.satdump_available:
            # Without SatDump there is nothing to capture with, so stop here
            # rather than slewing the antenna for no reason.
            return ExecutionOutcome(
                succeeded=False,
                detail=f"satdump unavailable: {status.satdump_error}")

        # Step 2b: refuse a pass the disk cannot hold. Filling the disk
        # mid-pass costs the recording anyway and endangers the ones already
        # waiting to upload (spec.md section 19.2).
        shortfall = self._space_shortfall(plan)
        if shortfall is not None:
            logger.error("pass %s refused: %s", stored.pass_id, shortfall)
            return ExecutionOutcome(succeeded=False, detail=shortfall)

        # Step 3: pre-position the antenna at the first track point.
        first = plan.track[0]
        self._point(first.azimuth_degrees, first.elevation_degrees, report, force=True)

        # Step 4: start capture at the configured recording margin.
        recording_start = _to_datetime(plan.recording_start)
        recording_end = _to_datetime(plan.recording_end)
        self._wait_until(recording_start, stop)
        if stop.is_set():
            return ExecutionOutcome(succeeded=False, detail="worker shutting down")

        self._start_capture(plan, stored, recording_end, report)

        # Step 5: track the satellite through the pass.
        self._track(plan, stop, report)

        # Step 6: let the post-roll elapse before stopping.
        self._wait_until(recording_end, stop)

        # Step 7: finalize the capture.
        result = self._station.stop_capture()
        if result is not None:
            report.capture_result = result
        if report.output_path:
            report.output_bytes = _dir_size_bytes(Path(report.output_path))

        succeeded = self._succeeded(report)
        return ExecutionOutcome(succeeded=succeeded, detail=report.summary())

    def _space_shortfall(self, plan: passplan_pb2.PassPlan) -> str | None:
        """Report why the disk cannot hold this pass, or None if it can.

        Raw capture is the worst case and is predictable: two bytes per
        sample per channel, for the whole recording window.
        """
        window = (_to_datetime(plan.recording_end)
                  - _to_datetime(plan.recording_start)).total_seconds()
        if window <= 0:
            return None

        estimate = int(window * plan.radio.sample_rate_hz * BYTES_PER_SAMPLE)
        try:
            available = free_bytes(self._recordings)
        except OSError:
            # Unable to measure: attempt the pass rather than refusing one
            # that might have been fine.
            return None

        if available < estimate + RESERVE_BYTES:
            return (f"not enough disk space: the pass needs about "
                    f"{estimate // (1 << 20)} MiB and {available // (1 << 20)} MiB "
                    f"is free")
        return None

    def _start_capture(self, plan: passplan_pb2.PassPlan, stored: StoredPlan,
                       recording_end: datetime, report: ExecutionReport) -> None:
        output = self._recordings / stored.pass_id
        output.mkdir(parents=True, exist_ok=True)

        radio = {
            "source": plan.radio.source or "rtlsdr",
            "samplerate": plan.radio.sample_rate_hz,
            "gain": plan.radio.gain_db,
            "ppm": plan.radio.ppm_correction,
            "bias_tee": plan.radio.bias_tee_enabled,
        }

        satdump = self._station.satdump
        mode = plan.recording_mode
        pipeline = plan.pipeline.identifier

        if mode == passplan_pb2.RECORDING_MODE_RAW or not pipeline:
            command = satdump.build_record_command(
                str(output / "baseband"), plan.radio.frequency_hz, radio)
        else:
            command = satdump.build_live_command(
                pipeline, str(output), plan.radio.frequency_hz, radio)
            if mode == passplan_pb2.RECORDING_MODE_RAW_AND_PROCESS:
                # One RTL-SDR cannot feed two SatDump processes, and this
                # build offers no verified way to save baseband while
                # decoding live, so the decode runs and the shortfall is
                # recorded rather than silently ignored.
                report.problems.append(
                    "raw_and_process ran as live decode only: "
                    "simultaneous baseband capture is unavailable with one SDR")

        # Let SatDump bound its own run as well as our stop, so a Worker crash
        # cannot leave a capture running forever.
        remaining = int((recording_end - self._clock()).total_seconds())
        if remaining > 0:
            command = command + ["--timeout", str(remaining + SATDUMP_GRACE_SECONDS)]

        try:
            self._station.start_capture(command, output_path=str(output))
            report.capture_started = True
            report.output_path = str(output)
            logger.info("capture started for pass %s output=%s", stored.pass_id, output)
        except HardwareError as error:
            report.problems.append(f"capture did not start: {error}")
            logger.error("capture did not start for pass %s: %s", stored.pass_id, error)

    def _track(self, plan: passplan_pb2.PassPlan, stop: threading.Event,
               report: ExecutionReport) -> None:
        """Drive the antenna along the plan's pointing timeline."""
        for point in plan.track:
            if stop.is_set():
                report.problems.append("tracking stopped by shutdown")
                return
            self._wait_until(_to_datetime(point.at), stop)
            if stop.is_set():
                report.problems.append("tracking stopped by shutdown")
                return
            self._point(point.azimuth_degrees, point.elevation_degrees, report)

    def _point(self, azimuth: float, elevation: float,
               report: ExecutionReport, force: bool = False) -> None:
        """Command the rotator, skipping negligible movements."""
        rotator = self._station.rotator
        if not force and rotator.last_commanded is not None:
            previous_azimuth, previous_elevation = rotator.last_commanded
            if (abs(previous_azimuth - azimuth) < MIN_POINTING_CHANGE_DEGREES
                    and abs(previous_elevation - elevation) < MIN_POINTING_CHANGE_DEGREES):
                return

        try:
            # track_to clamps: a real pass dips slightly below the horizon at
            # its edges, which is not a caller error.
            rotator.track_to(azimuth, elevation)
            report.pointing_commands += 1
        except RotatorError as error:
            # Report once. A broken serial link fails at every track point, and
            # keying the check on the full message would not deduplicate it
            # because each message names a different commanded angle.
            report.pointing_failures += 1
            if report.pointing_failures == 1:
                report.problems.append(f"pointing failed: {error}")
                logger.error("pass pointing failed: %s", error)

    def _safe_finish(self, report: ExecutionReport) -> None:
        """Stop any capture and park, whatever went wrong."""
        try:
            result = self._station.stop_capture()
            if result is not None and report.capture_result is None:
                report.capture_result = result
        except Exception as error:  # noqa: BLE001 - safing must continue
            logger.error("stopping capture failed: %s", error)

        try:
            self._station.rotator.park()
        except HardwareError as error:
            logger.error("parking after the pass failed: %s", error)

    # Helpers --------------------------------------------------------------

    def _wait_until(self, moment: datetime, stop: threading.Event) -> None:
        remaining = (moment - self._clock()).total_seconds()
        if remaining > 0:
            self._sleep(remaining, stop)

    @staticmethod
    def _succeeded(report: ExecutionReport) -> bool:
        """A pass counts as completed when it actually captured something.

        Pointing problems are recorded but do not by themselves fail the pass:
        a recording made with a stuck antenna is still worth keeping.
        """
        if not report.capture_started:
            return False
        if report.capture_result is None:
            return False
        # A capture the Worker stopped on purpose at the end of the window is
        # the normal, successful path.
        return report.capture_result.succeeded or report.capture_result.terminated


def _to_datetime(timestamp) -> datetime:
    return timestamp.ToDatetime(tzinfo=timezone.utc)


def _dir_size_bytes(path: Path) -> int:
    """Total size of a capture directory, for reporting."""
    if not path.is_dir():
        return 0
    total = 0
    for entry in path.rglob("*"):
        if entry.is_file():
            try:
                total += entry.stat().st_size
            except OSError:
                pass
    return total


