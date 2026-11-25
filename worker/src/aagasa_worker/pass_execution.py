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
from aagasa_worker.hardware.satdump import LOG_FILE_NAME, ProcessResult
from aagasa_worker.hardware.station import Station
from aagasa_worker.planstore import StoredPlan
from aagasa_worker.recordings import free_bytes

logger = logging.getLogger(__name__)

BYTES_PER_SAMPLE = 4

RESERVE_BYTES = 1 << 30


SUPPORTED_PLAN_VERSION = 1

SATDUMP_GRACE_SECONDS = 15

MIN_POINTING_CHANGE_DEGREES = 1.0

CAPTURE_POLL_SECONDS = 5.0

CUSTOM_PIPELINE_REFUSAL = "custom pipelines are not supported by this station yet"


@dataclass
class ExecutionReport:
    """What happened during one pass, for the local history."""

    pointing_commands: int = 0
    capture_started: bool = False
    capture_result: ProcessResult | None = None
    output_path: str = ""
    output_bytes: int = 0
    pointing_failures: int = 0
    capture_died: bool = False
    problems: list[str] = field(default_factory=list)

    @property
    def capture_failed(self) -> bool:
        """True when a started capture did not end the way a good one does."""
        if not self.capture_started:
            return False
        if self.capture_died or self.capture_result is None:
            return True
        return not (self.capture_result.succeeded or self.capture_result.terminated)

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
        if self.capture_failed and self.capture_result is not None \
                and self.capture_result.log_tail:
            # SatDump explains its failures in its own log; without the tail
            # an operator only sees an exit code.
            tail = " | ".join(line.strip() for line in
                              self.capture_result.log_tail.splitlines() if line.strip())
            parts.append(f"satdump_log_tail={tail}")
        return " ".join(parts)


class PlanNotExecutable(Exception):
    """The plan cannot be run, and the reason is not a hardware fault."""


class StationPassHandler:
    """Runs a PassPlan against the real station.

    Replaces the placeholder handler from V9. The executor still owns *when* a
    pass runs; this owns *how*.
    """

    def __init__(self, station: Station, recordings_directory: Path,
                 clock=None, sleeper=None,
                 capture_poll_seconds: float = CAPTURE_POLL_SECONDS):
        self._station = station
        self._recordings = Path(recordings_directory)
        self._capture_poll_seconds = capture_poll_seconds
        self._clock = clock or (lambda: datetime.now(timezone.utc))
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
            self._safe_finish(report)


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
            raise PlanNotExecutable("the plan carries no pointing timeline")

        if plan.radio.frequency_hz <= 0 or plan.radio.sample_rate_hz <= 0:
            raise PlanNotExecutable("the plan carries no usable radio settings")

    def _run(self, plan: passplan_pb2.PassPlan, stored: StoredPlan,
             stop: threading.Event, report: ExecutionReport) -> ExecutionOutcome:
        status = self._station.probe()
        if status.rotator_enabled and not status.rotator_available:
            report.problems.append(f"rotator unavailable: {status.rotator_error}")
        if not status.sdr_available:
            report.problems.append(f"sdr unavailable: {status.sdr_error}")
        if not status.satdump_available:
            return ExecutionOutcome(
                succeeded=False,
                detail=f"satdump unavailable: {status.satdump_error}")

        refusal = self._pipeline_refusal(plan)
        if refusal is not None:
            logger.error("pass %s refused: %s", stored.pass_id, refusal)
            return ExecutionOutcome(succeeded=False, detail=refusal)

        shortfall = self._space_shortfall(plan)
        if shortfall is not None:
            logger.error("pass %s refused: %s", stored.pass_id, shortfall)
            return ExecutionOutcome(succeeded=False, detail=shortfall)

        first = plan.track[0]
        self._point(first.azimuth_degrees, first.elevation_degrees, report, force=True)

        recording_start = _to_datetime(plan.recording_start)
        recording_end = _to_datetime(plan.recording_end)
        self._wait_until(recording_start, stop)
        if stop.is_set():
            return ExecutionOutcome(succeeded=False, detail="worker shutting down")

        self._start_capture(plan, stored, recording_end, report)
        if not report.capture_started:
            # Tracking a pass nothing is recording only wears the rotator.
            return ExecutionOutcome(succeeded=False, detail=report.summary())

        self._track(plan, stop, report)

        if not report.capture_died:
            self._wait_while_capturing(recording_end, stop, report)

        result = self._station.stop_capture()
        if result is not None and not report.capture_died:
            report.capture_result = result
        if report.output_path:
            report.output_bytes = _dir_size_bytes(Path(report.output_path))

        succeeded = self._succeeded(report)
        return ExecutionOutcome(succeeded=succeeded, detail=report.summary())

    def _pipeline_refusal(self, plan: passplan_pb2.PassPlan) -> str | None:
        """Report why the plan's pipeline cannot run here, or None if it can.

        Checked before the antenna moves: SatDump given an unknown pipeline
        exits at once, and the pass would otherwise be tracked for nothing.
        A custom pipeline would need its definition handed to SatDump, which
        this station has no mechanism for yet; running a stock pipeline of the
        same name instead would silently produce the wrong product.
        """
        if plan.recording_mode == passplan_pb2.RECORDING_MODE_RAW:
            return None
        identifier = plan.pipeline.identifier
        if plan.pipeline.is_custom:
            return f"{CUSTOM_PIPELINE_REFUSAL} (pipeline {identifier!r})"
        if not identifier:
            return None
        try:
            installed = self._station.satdump.has_pipeline(identifier)
        except OSError as error:
            logger.warning("could not list satdump pipelines, not checking %s: %s",
                           identifier, error)
            return None
        if not installed:
            return f"pipeline {identifier!r} is not installed on this station"
        return None

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
                report.problems.append(
                    "raw_and_process ran as live decode only: "
                    "simultaneous baseband capture is unavailable with one SDR")

        remaining = int((recording_end - self._clock()).total_seconds())
        if remaining > 0:
            command = command + ["--timeout", str(remaining + SATDUMP_GRACE_SECONDS)]

        try:
            self._station.start_capture(command, output_path=str(output),
                                        log_path=str(output / LOG_FILE_NAME))
            report.capture_started = True
            report.output_path = str(output)
            logger.info("capture started for pass %s output=%s", stored.pass_id, output)
        except HardwareError as error:
            report.problems.append(f"capture did not start: {error}")
            logger.error("capture did not start for pass %s: %s", stored.pass_id, error)

    def _track(self, plan: passplan_pb2.PassPlan, stop: threading.Event,
               report: ExecutionReport) -> None:
        """Drive the antenna along the plan's pointing timeline.

        Stops early if the capture ends on its own: there is no point
        following a satellite nothing is recording.
        """
        for point in plan.track:
            if stop.is_set():
                report.problems.append("tracking stopped by shutdown")
                return
            if not self._wait_while_capturing(_to_datetime(point.at), stop, report):
                return
            if stop.is_set():
                report.problems.append("tracking stopped by shutdown")
                return
            self._point(point.azimuth_degrees, point.elevation_degrees, report)

    def _point(self, azimuth: float, elevation: float,
               report: ExecutionReport, force: bool = False) -> None:
        """Command the rotator, skipping negligible movements.

        A fixed antenna has nothing to point, so nothing is sent.
        """
        if self._station.fixed_antenna:
            return
        rotator = self._station.rotator
        if not force and rotator.last_commanded is not None:
            previous_azimuth, previous_elevation = rotator.last_commanded
            if (abs(previous_azimuth - azimuth) < MIN_POINTING_CHANGE_DEGREES
                    and abs(previous_elevation - elevation) < MIN_POINTING_CHANGE_DEGREES):
                return

        try:
            rotator.track_to(azimuth, elevation)
            report.pointing_commands += 1
        except RotatorError as error:
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
            self._station.park_if_rotator()
        except HardwareError as error:
            logger.error("parking after the pass failed: %s", error)


    def _wait_while_capturing(self, moment: datetime, stop: threading.Event,
                              report: ExecutionReport) -> bool:
        """Wait until moment, watching that SatDump is still running.

        Returns False when the capture ended by itself. Waking every few
        seconds is what lets a capture that died at the start (a missing
        device, a bad pipeline) end the pass promptly instead of the station
        tracking an empty recording for the whole window.
        """
        while True:
            if stop.is_set():
                return True
            if not self._station.capture_active:
                self._capture_ended_early(report)
                return False
            remaining = (moment - self._clock()).total_seconds()
            if remaining <= 0:
                return True
            self._sleep(min(remaining, self._capture_poll_seconds), stop)

    def _capture_ended_early(self, report: ExecutionReport) -> None:
        report.capture_died = True
        result = self._station.stop_capture()
        if result is not None:
            report.capture_result = result
        exit_code = result.exit_code if result is not None else None
        report.problems.append(
            f"satdump exited before the recording window ended (exit code {exit_code})")
        logger.error("satdump exited early exit_code=%s log tail:\n%s",
                     exit_code, result.log_tail if result is not None else "")

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
        return not report.capture_failed


def capture_start(stored: StoredPlan) -> datetime:
    """When the station must be ready for a pass.

    The earlier of AOS and the recording start: a plan with a long pre-roll
    starts recording well before AOS, and waking relative to AOS alone would
    start that capture late. An unreadable plan falls back to AOS; execution
    will refuse it properly.
    """
    plan = passplan_pb2.PassPlan()
    try:
        plan.ParseFromString(stored.encoded)
    except Exception:  # noqa: BLE001 - protobuf raises assorted types
        return stored.aos
    if not plan.HasField("recording_start"):
        return stored.aos
    return min(stored.aos, _to_datetime(plan.recording_start))


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


