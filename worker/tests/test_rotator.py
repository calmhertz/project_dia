"""Yaesu G-550 command safety.

The byte-level tests use a recording transport so the exact wire format is
verified without hardware. Global test property 17 requires commands to stay
inside the valid azimuth and elevation ranges.
"""

import threading

import pytest
import serial

from aagasa_worker.hardware.errors import RotatorError
from aagasa_worker.hardware.rotator import (
    MAX_AZIMUTH,
    MAX_ELEVATION,
    G550Rotator,
    clamp_azimuth,
    clamp_elevation,
    format_command,
)


class RecordingSerial:
    """Stands in for a serial port, capturing everything written to it."""

    def __init__(self, writes, fail_with=None):
        self._writes = writes
        self._fail_with = fail_with
        self.opened = []

    def __call__(self, port, baud, timeout=None):
        if self._fail_with is not None:
            raise self._fail_with
        self.opened.append((port, baud, timeout))
        return self

    def __enter__(self):
        return self

    def __exit__(self, *exc):
        return False

    def write(self, payload):
        self._writes.append(payload)
        return len(payload)


def new_rotator(writes, **kwargs):
    return G550Rotator(
        port="/dev/ttyTEST", baud_rate=9600,
        serial_factory=RecordingSerial(writes), **kwargs,
    )


def test_command_format_matches_reference():
    # The reference implementation's proven format: W<az:04d> <el:03d>.
    assert format_command(0, 0) == "W0000 000"
    assert format_command(180, 45) == "W0180 045"
    assert format_command(359, 90) == "W0359 090"


def test_move_writes_carriage_terminated_ascii():
    writes = []
    rotator = new_rotator(writes)

    rotator.move_to(180, 45)

    assert writes == [b"W0180 045\r"]


def test_park_uses_configured_safe_position():
    writes = []
    rotator = new_rotator(writes, park_azimuth=270, park_elevation=15)

    assert rotator.park() == (270, 15)
    assert writes == [b"W0270 015\r"]


def test_park_defaults_to_zero_zero():
    writes = []
    rotator = new_rotator(writes)

    rotator.park()

    # Matches the reference implementation's park command.
    assert writes == [b"W0000 000\r"]


# Global test property 17: no command may leave the valid ranges.
@pytest.mark.parametrize(
    "azimuth,elevation",
    [(-1, 0), (360, 0), (0, -1), (0, 91), (10_000, 10_000), (-10_000, -10_000)],
)
def test_out_of_range_positions_are_refused(azimuth, elevation):
    writes = []
    rotator = new_rotator(writes)

    with pytest.raises(RotatorError):
        rotator.move_to(azimuth, elevation)

    # Nothing reached the hardware.
    assert writes == []


def test_tracking_clamps_instead_of_failing():
    writes = []
    rotator = new_rotator(writes)

    # A real pass produces a slightly negative elevation near the horizon.
    assert rotator.track_to(-5, -3) == (0, 0)
    assert rotator.track_to(400, 120) == (MAX_AZIMUTH, MAX_ELEVATION)
    assert writes == [b"W0000 000\r", b"W0359 090\r"]


def test_clamp_helpers():
    assert clamp_azimuth(-20) == 0
    assert clamp_azimuth(400) == 359
    assert clamp_azimuth(180.7) == 180
    assert clamp_elevation(-20) == 0
    assert clamp_elevation(120) == 90


def test_fractional_angles_are_truncated_to_whole_degrees():
    writes = []
    rotator = new_rotator(writes)

    rotator.move_to(180.9, 45.9)

    assert writes == [b"W0180 045\r"]


# An impossible park position is a setup error, caught before the station runs.
@pytest.mark.parametrize("azimuth,elevation", [(400, 0), (0, 120), (-1, 0)])
def test_invalid_park_position_is_refused_at_construction(azimuth, elevation):
    with pytest.raises(RotatorError):
        G550Rotator("/dev/ttyTEST", 9600, park_azimuth=azimuth, park_elevation=elevation,
                    serial_factory=RecordingSerial([]))


def test_serial_failure_raises_instead_of_being_swallowed():
    # The reference implementation printed and continued; that would let a
    # pass proceed believing the antenna moved.
    factory = RecordingSerial([], fail_with=serial.SerialException("port busy"))
    rotator = G550Rotator("/dev/ttyMISSING", 9600, serial_factory=factory)

    with pytest.raises(RotatorError, match="failed"):
        rotator.move_to(10, 10)
    with pytest.raises(RotatorError, match="unavailable"):
        rotator.check()


def test_configured_port_and_baud_are_used():
    writes = []
    factory = RecordingSerial(writes)
    rotator = G550Rotator("/dev/ttyUSB7", 19200, serial_factory=factory)

    rotator.move_to(1, 2)

    assert factory.opened == [("/dev/ttyUSB7", 19200, 2)]


def test_concurrent_commands_do_not_interleave():
    writes = []
    rotator = new_rotator(writes)

    def send(angle):
        rotator.move_to(angle, angle % 91)

    threads = [threading.Thread(target=send, args=(index,)) for index in range(20)]
    for thread in threads:
        thread.start()
    for thread in threads:
        thread.join()

    assert len(writes) == 20
    # Each write is one complete, well-formed command.
    for payload in writes:
        assert payload.endswith(b"\r")
        assert len(payload) == len(b"W0000 000\r")


def test_last_commanded_position_is_tracked():
    rotator = new_rotator([])
    assert rotator.last_commanded is None

    rotator.move_to(123, 45)

    assert rotator.last_commanded == (123, 45)


def test_real_pyserial_loopback_accepts_the_command():
    """Drive a genuine pyserial transport, not a stub.

    This exercises pyserial's own encoding and write path; only the physical
    G-550 is absent.
    """
    writes = []

    def loopback_factory(port, baud, timeout=None):
        connection = serial.serial_for_url("loop://", baudrate=baud, timeout=timeout)
        original_write = connection.write

        def recording_write(payload):
            writes.append(payload)
            return original_write(payload)

        connection.write = recording_write
        return connection

    rotator = G550Rotator("loop://", 9600, serial_factory=loopback_factory)
    rotator.move_to(359, 90)

    assert writes == [b"W0359 090\r"]
