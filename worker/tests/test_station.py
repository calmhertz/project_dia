"""RTL-SDR probing and station hardware lifecycle."""

import shutil
import stat
import subprocess
import textwrap
import time

import pytest

from aagasa_worker.hardware.errors import RotatorError, SDRError
from aagasa_worker.hardware.rotator import G550Rotator
from aagasa_worker.hardware.satdump import SatDump
from aagasa_worker.hardware.sdr import RTLSDRProbe
from aagasa_worker.hardware.station import Station


def make_stub(tmp_path, name: str, body: str) -> str:
    path = tmp_path / name
    path.write_text("#!/usr/bin/env python3\n" + textwrap.dedent(body))
    path.chmod(path.stat().st_mode | stat.S_IEXEC | stat.S_IXGRP | stat.S_IXOTH)
    return str(path)


# RTL-SDR probing -----------------------------------------------------------

def test_no_devices_is_reported_not_raised(tmp_path):
    stub = make_stub(tmp_path, "rtl_test_none.py", """
        import sys
        print("No supported devices found.", file=sys.stderr)
        sys.exit(1)
    """)

    assert RTLSDRProbe(rtl_test_binary=stub).list_devices() == []


def test_check_raises_when_no_device_is_attached(tmp_path):
    stub = make_stub(tmp_path, "rtl_test_none.py", """
        import sys
        print("No supported devices found.", file=sys.stderr)
        sys.exit(1)
    """)

    with pytest.raises(SDRError, match="no RTL-SDR device detected"):
        RTLSDRProbe(rtl_test_binary=stub).check()


def test_attached_device_is_parsed(tmp_path):
    stub = make_stub(tmp_path, "rtl_test_one.py", """
        import sys
        print("Found 1 device(s):", file=sys.stderr)
        print("  0:  Realtek, RTL2838UHIDIR, SN: 00000001", file=sys.stderr)
        sys.exit(0)
    """)
    probe = RTLSDRProbe(rtl_test_binary=stub)

    assert probe.list_devices() == ["Realtek, RTL2838UHIDIR, SN: 00000001"]
    assert "RTL2838" in probe.check()


def test_configured_index_beyond_attached_devices_is_rejected(tmp_path):
    stub = make_stub(tmp_path, "rtl_test_one.py", """
        import sys
        print("  0:  Realtek, RTL2838UHIDIR, SN: 00000001", file=sys.stderr)
    """)

    with pytest.raises(SDRError, match="index 1 not present"):
        RTLSDRProbe(rtl_test_binary=stub, device_index=1).check()


def test_missing_rtl_test_binary_is_reported():
    with pytest.raises(SDRError, match="not installed"):
        RTLSDRProbe(rtl_test_binary="rtl_test-does-not-exist").list_devices()


def test_real_rtl_test_runs_if_installed():
    """Exercise the genuine rtl_test if it is present.

    With no radio attached this asserts the no-device path against real tool
    output rather than a stub.
    """
    if shutil.which("rtl_test") is None:
        pytest.skip("rtl_test is not installed")

    # Must not raise regardless of whether hardware is attached.
    assert isinstance(RTLSDRProbe().list_devices(), list)


# Station lifecycle ---------------------------------------------------------

class RecordingSerial:
    def __init__(self, writes, fail=False):
        self._writes = writes
        self._fail = fail

    def __call__(self, port, baud, timeout=None):
        if self._fail:
            raise OSError("port unavailable")
        return self

    def __enter__(self):
        return self

    def __exit__(self, *exc):
        return False

    def write(self, payload):
        self._writes.append(payload)
        return len(payload)


def build_station(tmp_path, writes, rotator_fails=False, sdr_present=False):
    rotator = G550Rotator(
        "/dev/ttyTEST", 9600, park_azimuth=180, park_elevation=10,
        serial_factory=RecordingSerial(writes, fail=rotator_fails),
    )
    if sdr_present:
        rtl = make_stub(tmp_path, "rtl_ok.py", """
            import sys
            print("  0:  Realtek, RTL2838UHIDIR, SN: 1", file=sys.stderr)
        """)
    else:
        rtl = make_stub(tmp_path, "rtl_none.py", """
            import sys
            print("No supported devices found.", file=sys.stderr)
            sys.exit(1)
        """)
    satdump_stub = make_stub(tmp_path, "satdump_sleep.py", """
        import time
        print("running", flush=True)
        time.sleep(60)
    """)
    pipelines = tmp_path / "pipelines"
    pipelines.mkdir(exist_ok=True)
    (pipelines / "P.json").write_text('{"noaa_apt": {"name": "NOAA APT", "live": true}}')

    station = Station(
        rotator=rotator,
        sdr=RTLSDRProbe(rtl_test_binary=rtl),
        satdump=SatDump(binary=satdump_stub, pipeline_dirs=[pipelines]),
    )
    return station, satdump_stub


# worker-spec section 11.1: after any restart the antenna position is unknown,
# so startup establishes a known one.
def test_safe_startup_parks_the_rotator(tmp_path):
    writes = []
    station, _ = build_station(tmp_path, writes, sdr_present=True)

    status = station.safe_startup()

    assert writes == [b"W0180 010\r"]
    assert status.rotator_available is True
    assert status.sdr_available is True
    assert status.satdump_available is True
    assert status.pipeline_count == 1
    assert status.last_commanded_position == (180, 10)


# A missing device must be reported, not crash the Worker.
def test_safe_startup_continues_when_hardware_is_missing(tmp_path):
    writes = []
    station, _ = build_station(tmp_path, writes, rotator_fails=True, sdr_present=False)

    status = station.safe_startup()

    assert status.rotator_available is False
    assert status.rotator_error
    assert status.sdr_available is False
    assert status.sdr_error
    # SatDump is still discoverable, so one missing device does not hide the rest.
    assert status.satdump_available is True


def test_probe_changes_nothing(tmp_path):
    writes = []
    station, _ = build_station(tmp_path, writes, sdr_present=True)

    station.probe()

    assert writes == [], "probing must not command the rotator"


def test_safe_shutdown_stops_capture_and_parks(tmp_path):
    writes = []
    station, stub = build_station(tmp_path, writes, sdr_present=True)
    station.start_capture([stub], output_path="/tmp/out")
    assert station.capture_active is True

    station.safe_shutdown()

    assert station.capture_active is False
    assert writes[-1] == b"W0180 010\r"


def test_safe_shutdown_parks_even_if_capture_stop_fails(tmp_path):
    writes = []
    station, stub = build_station(tmp_path, writes, sdr_present=True)
    station.start_capture([stub])

    # Simulate a stop path that blows up; parking must still happen.
    def exploding_stop(process, grace_seconds=10):
        raise RuntimeError("stop failed")

    station.satdump.stop = exploding_stop
    station.safe_shutdown()

    assert writes[-1] == b"W0180 010\r"


def test_safe_shutdown_does_not_raise_when_rotator_is_gone(tmp_path):
    writes = []
    station, _ = build_station(tmp_path, writes, rotator_fails=True)

    # Must not raise: shutdown is best effort across every device.
    station.safe_shutdown()


# worker-spec section 12: the station has one RTL-SDR, so one capture.
def test_second_concurrent_capture_is_refused(tmp_path):
    writes = []
    station, stub = build_station(tmp_path, writes, sdr_present=True)

    station.start_capture([stub])
    with pytest.raises(SDRError, match="already running"):
        station.start_capture([stub])

    station.safe_shutdown()


def test_capture_can_be_restarted_after_stopping(tmp_path):
    writes = []
    station, stub = build_station(tmp_path, writes, sdr_present=True)

    station.start_capture([stub])
    result = station.stop_capture()
    assert result is not None
    assert result.terminated is True
    assert station.capture_active is False

    # The device is free again.
    station.start_capture([stub])
    station.safe_shutdown()


def test_stop_capture_without_one_returns_nothing(tmp_path):
    station, _ = build_station(tmp_path, [], sdr_present=True)

    assert station.stop_capture() is None


def test_finished_capture_is_reaped_before_starting_another(tmp_path):
    writes = []
    station, _ = build_station(tmp_path, writes, sdr_present=True)
    quick = make_stub(tmp_path, "quick.py", """
        import sys
        sys.exit(0)
    """)

    station.start_capture([quick])
    time.sleep(0.4)
    assert station.capture_active is False

    # Starting again must succeed and must not leave a zombie behind.
    station.start_capture([quick])
    time.sleep(0.4)
    station.safe_shutdown()


def test_status_serialises_for_telemetry(tmp_path):
    station, _ = build_station(tmp_path, [], sdr_present=True)

    payload = station.probe().as_dict()

    assert payload["satdump_available"] is True
    assert payload["pipeline_count"] == 1
    assert "rotator_available" in payload
