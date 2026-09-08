"""Station hardware lifecycle.

Owns the safe-startup, safe-shutdown and single-capture rules so no other part
of the Worker has to remember them (worker-spec sections 11.1 and 12).
"""

import logging
import subprocess
import threading
from dataclasses import dataclass, field

from aagasa_worker.hardware.errors import HardwareError, SDRError
from aagasa_worker.hardware.rotator import G550Rotator
from aagasa_worker.hardware.satdump import ProcessResult, SatDump
from aagasa_worker.hardware.sdr import RTLSDRProbe

logger = logging.getLogger(__name__)


@dataclass
class HardwareStatus:
    """What the Worker knows about the station's hardware right now."""

    rotator_available: bool = False
    rotator_error: str = ""
    sdr_available: bool = False
    sdr_description: str = ""
    sdr_error: str = ""
    satdump_available: bool = False
    satdump_path: str = ""
    satdump_error: str = ""
    pipeline_count: int = 0
    capture_active: bool = False
    last_commanded_position: tuple[int, int] | None = None

    def as_dict(self) -> dict:
        return {
            "rotator_available": self.rotator_available,
            "rotator_error": self.rotator_error,
            "sdr_available": self.sdr_available,
            "sdr_description": self.sdr_description,
            "sdr_error": self.sdr_error,
            "satdump_available": self.satdump_available,
            "satdump_path": self.satdump_path,
            "satdump_error": self.satdump_error,
            "pipeline_count": self.pipeline_count,
            "capture_active": self.capture_active,
            "last_commanded_position": list(self.last_commanded_position)
            if self.last_commanded_position
            else None,
        }


@dataclass
class CaptureHandle:
    """A running capture."""

    process: subprocess.Popen
    command: list[str] = field(default_factory=list)
    output_path: str = ""


class Station:
    """Coordinates the rotator, the SDR and SatDump for one ground station."""

    def __init__(self, rotator: G550Rotator, sdr: RTLSDRProbe, satdump: SatDump):
        self._rotator = rotator
        self._sdr = sdr
        self._satdump = satdump
        # One RTL-SDR means one capture; the lock makes that a hard rule
        # rather than a convention (worker-spec section 12).
        self._capture_lock = threading.Lock()
        self._capture: CaptureHandle | None = None

    @property
    def rotator(self) -> G550Rotator:
        return self._rotator

    @property
    def satdump(self) -> SatDump:
        return self._satdump

    def probe(self) -> HardwareStatus:
        """Report hardware availability without changing anything.

        Every component is probed even when an earlier one failed, so a single
        missing device does not hide the state of the rest.
        """
        status = HardwareStatus()

        try:
            self._rotator.check()
            status.rotator_available = True
        except HardwareError as error:
            status.rotator_error = str(error)

        try:
            status.sdr_description = self._sdr.check()
            status.sdr_available = True
        except HardwareError as error:
            status.sdr_error = str(error)

        try:
            status.satdump_path = self._satdump.check()
            status.satdump_available = True
            status.pipeline_count = len(self._satdump.pipelines())
        except HardwareError as error:
            status.satdump_error = str(error)

        status.capture_active = self.capture_active
        status.last_commanded_position = self._rotator.last_commanded
        return status

    def safe_startup(self) -> HardwareStatus:
        """Bring hardware to a known state.

        After any restart, expected or abnormal, the antenna position is
        unknown. Parking establishes a known state before anything else
        (worker-spec section 11.1). A missing rotator is reported, not raised:
        the Worker must keep running and keep reporting.
        """
        logger.info("hardware startup beginning")
        status = self.probe()

        if status.rotator_available:
            try:
                self._rotator.park()
                status.last_commanded_position = self._rotator.last_commanded
                logger.info("rotator parked at startup")
            except HardwareError as error:
                status.rotator_available = False
                status.rotator_error = f"park failed: {error}"
                logger.error("rotator park at startup failed: %s", error)
        else:
            logger.warning("rotator unavailable at startup: %s", status.rotator_error)

        if not status.sdr_available:
            logger.warning("sdr unavailable at startup: %s", status.sdr_error)
        if not status.satdump_available:
            logger.warning("satdump unavailable at startup: %s", status.satdump_error)

        return status

    def safe_shutdown(self) -> None:
        """Stop any capture and park the antenna.

        Best effort by design: shutdown must not raise, or a failure in one
        device would skip the safing of another.
        """
        logger.info("hardware shutdown beginning")

        if self._capture is not None:
            try:
                self.stop_capture()
            except Exception as error:  # noqa: BLE001 - shutdown must continue
                logger.error("stopping capture during shutdown failed: %s", error)

        try:
            self._rotator.park()
        except HardwareError as error:
            logger.error("parking rotator during shutdown failed: %s", error)

    @property
    def capture_active(self) -> bool:
        capture = self._capture
        return capture is not None and capture.process.poll() is None

    def start_capture(self, command: list[str], output_path: str = "") -> CaptureHandle:
        """Start a capture, refusing a second concurrent one."""
        with self._capture_lock:
            if self.capture_active:
                raise SDRError("a capture is already running; the station has one RTL-SDR")

            # A finished capture that was never collected would otherwise leak
            # a zombie process.
            if self._capture is not None:
                self._reap_locked()

            process = self._satdump.start(command)
            self._capture = CaptureHandle(process=process, command=command, output_path=output_path)
            logger.info("capture started output=%s", output_path or "<unset>")
            return self._capture

    def stop_capture(self) -> ProcessResult | None:
        """Stop the running capture and return its result."""
        with self._capture_lock:
            if self._capture is None:
                return None
            result = self._satdump.stop(self._capture.process)
            logger.info(
                "capture stopped exit_code=%s terminated=%s",
                result.exit_code, result.terminated,
            )
            self._capture = None
            return result

    def _reap_locked(self) -> None:
        """Collect a finished capture. Caller holds the lock."""
        if self._capture is None:
            return
        self._satdump.stop(self._capture.process)
        self._capture = None
