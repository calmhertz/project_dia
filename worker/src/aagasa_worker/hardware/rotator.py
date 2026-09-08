"""Yaesu G-550 rotator control.

The command sequence comes from the working reference implementation in
REFERENCE/project_dia/rotor.py, which is the only verified-against-hardware
source available: a "W" command with zero-padded azimuth and elevation,
terminated by a carriage return, sent over a short-lived serial connection.

Two things differ from the reference. Serial failures raise instead of being
printed and ignored, and bounds violations are reported rather than silently
clamped, because a caller asking for an impossible angle has a bug worth
surfacing (RULES.md sections 13 and 15).
"""

import logging
import threading

import serial

from aagasa_worker.hardware.errors import RotatorError

logger = logging.getLogger(__name__)

# spec.md section 9: the G-550 accepts these ranges and no others.
MIN_AZIMUTH = 0
MAX_AZIMUTH = 359
MIN_ELEVATION = 0
MAX_ELEVATION = 90

SERIAL_TIMEOUT_SECONDS = 2


def format_command(azimuth: int, elevation: int) -> str:
    """Build the G-550 position command for already-validated angles."""
    return f"W{azimuth:04d} {elevation:03d}"


def clamp_azimuth(azimuth: float) -> int:
    """Clamp an azimuth into the range the hardware accepts."""
    return max(MIN_AZIMUTH, min(MAX_AZIMUTH, int(azimuth)))


def clamp_elevation(elevation: float) -> int:
    """Clamp an elevation into the range the hardware accepts."""
    return max(MIN_ELEVATION, min(MAX_ELEVATION, int(elevation)))


class G550Rotator:
    """Commands a Yaesu G-550 over a serial port.

    The port is opened per command, matching the reference implementation and
    keeping the Worker from holding the device open between passes.
    """

    def __init__(self, port: str, baud_rate: int, park_azimuth: int = 0, park_elevation: int = 0,
                 serial_factory=serial.Serial):
        self._port = port
        self._baud_rate = baud_rate
        # serial_factory is injected so tests can drive a loopback transport.
        self._serial_factory = serial_factory
        self._lock = threading.Lock()

        # Validate the park position once, at construction: a station
        # configured with an impossible safe position is a setup error, not
        # something to discover during recovery.
        self._park_azimuth = self._require_azimuth(park_azimuth)
        self._park_elevation = self._require_elevation(park_elevation)

        self.last_commanded: tuple[int, int] | None = None

    @staticmethod
    def _require_azimuth(azimuth) -> int:
        value = int(azimuth)
        if not MIN_AZIMUTH <= value <= MAX_AZIMUTH:
            raise RotatorError(
                f"azimuth {value} outside {MIN_AZIMUTH}..{MAX_AZIMUTH}"
            )
        return value

    @staticmethod
    def _require_elevation(elevation) -> int:
        value = int(elevation)
        if not MIN_ELEVATION <= value <= MAX_ELEVATION:
            raise RotatorError(
                f"elevation {value} outside {MIN_ELEVATION}..{MAX_ELEVATION}"
            )
        return value

    def move_to(self, azimuth, elevation) -> tuple[int, int]:
        """Command an exact position. Out-of-range angles are rejected."""
        target_azimuth = self._require_azimuth(azimuth)
        target_elevation = self._require_elevation(elevation)
        self._send(format_command(target_azimuth, target_elevation))
        self.last_commanded = (target_azimuth, target_elevation)
        return self.last_commanded

    def track_to(self, azimuth, elevation) -> tuple[int, int]:
        """Command a tracking position, clamping to the hardware limits.

        Tracking a real pass legitimately produces angles just outside the
        range (a negative elevation at the horizon), so those are clamped
        rather than treated as a caller error.
        """
        return self.move_to(clamp_azimuth(azimuth), clamp_elevation(elevation))

    def park(self) -> tuple[int, int]:
        """Drive the rotator to its configured safe position."""
        logger.info("parking rotator az=%d el=%d", self._park_azimuth, self._park_elevation)
        return self.move_to(self._park_azimuth, self._park_elevation)

    @property
    def park_position(self) -> tuple[int, int]:
        return (self._park_azimuth, self._park_elevation)

    def check(self) -> None:
        """Verify the serial port can be opened.

        The G-550 has no position-readback command in the reference
        implementation, so this confirms the link only, not the antenna angle.
        """
        try:
            with self._serial_factory(self._port, self._baud_rate, timeout=SERIAL_TIMEOUT_SECONDS):
                pass
        except (serial.SerialException, OSError, ValueError) as error:
            raise RotatorError(f"rotator port {self._port} unavailable: {error}") from error

    def _send(self, command: str) -> None:
        # One command at a time: overlapping writes would interleave bytes on
        # the wire and could drive the antenna somewhere unintended.
        with self._lock:
            try:
                with self._serial_factory(
                    self._port, self._baud_rate, timeout=SERIAL_TIMEOUT_SECONDS
                ) as connection:
                    connection.write(f"{command}\r".encode("ascii"))
            except (serial.SerialException, OSError, ValueError) as error:
                raise RotatorError(f"rotator command {command!r} failed: {error}") from error
        logger.debug("rotator command sent %s", command)
