"""Executing a PassPlan against the station.

The rotator is a recording transport and SatDump is a stub executable, so the
whole execution sequence runs for real without a G-550 or an RTL-SDR attached.
Only the physical devices are absent.
"""

import stat
import textwrap
import threading
import time
from datetime import datetime, timedelta, timezone
from pathlib import Path

import pytest
from google.protobuf.timestamp_pb2 import Timestamp

from aagasa.worker.v1 import passplan_pb2

from aagasa_worker.hardware.rotator import G550Rotator
from aagasa_worker.hardware.satdump import SatDump
from aagasa_worker.hardware.sdr import RTLSDRProbe
from aagasa_worker.hardware.station import Station
from aagasa_worker import pass_execution
from aagasa_worker.pass_execution import (
    MIN_POINTING_CHANGE_DEGREES,
    PlanNotExecutable,
    StationPassHandler,
)
from aagasa_worker.planstore import StoredPlan

BASE = datetime(2026, 8, 25, 12, 0, 0, tzinfo=timezone.utc)


def _stamp(moment: datetime) -> Timestamp:
    timestamp = Timestamp()
    timestamp.FromDatetime(moment)
    return timestamp


def build_plan(aos: datetime = BASE, duration=timedelta(minutes=5),
               mode=passplan_pb2.RECORDING_MODE_PROCESS,
               pipeline: str = "noaa_apt", track_points: int = 6,
               frequency: int = 137_100_000) -> passplan_pb2.PassPlan:
    los = aos + duration
    plan = passplan_pb2.PassPlan(
        plan_version=1,
        pass_id="pass-1", station_id="station-1", worker_id="worker-1",
        norad_id=25544, satellite_name="ISS (ZARYA)",
        aos=_stamp(aos), tca=_stamp(aos + duration / 2), los=_stamp(los),
        max_elevation_degrees=31,
        recording_start=_stamp(aos - timedelta(seconds=10)),
        recording_end=_stamp(los + timedelta(seconds=10)),
        band=passplan_pb2.RF_BAND_VHF,
        radio=passplan_pb2.RadioSettings(
            source="rtlsdr", frequency_hz=frequency,
            sample_rate_hz=2_048_000, gain_db=40, ppm_correction=3,
            bias_tee_enabled=True),
        recording_mode=mode,
        pipeline=passplan_pb2.SatDumpPipeline(identifier=pipeline),
        generation="a" * 64,
    )
    if track_points:
        step = duration / max(track_points - 1, 1)
        for index in range(track_points):
            at = aos + step * index
            plan.track.append(passplan_pb2.TrackPoint(
                at=_stamp(at),
                azimuth_degrees=180.0 + index * 15.0,
                elevation_degrees=10.0 + index * 5.0,
                range_km=1400.0 - index * 50.0,
            ))
    return plan


def stored_from(plan: passplan_pb2.PassPlan) -> StoredPlan:
    return StoredPlan(
        pass_id=plan.pass_id, generation=plan.generation, plan_version=plan.plan_version,
        encoded=plan.SerializeToString(deterministic=True),
        aos=plan.aos.ToDatetime(tzinfo=timezone.utc),
        los=plan.los.ToDatetime(tzinfo=timezone.utc),
        state="ready", received_at=BASE, updated_at=BASE,
    )


class RecordingSerial:
    """Captures rotator commands instead of driving a real port."""

    def __init__(self, writes, fail=False):
        self._writes = writes
        self._fail = fail

    def __call__(self, port, baud, timeout=None):
        if self._fail:
            raise OSError("no such port")
        return self

    def __enter__(self):
        return self

    def __exit__(self, *exc):
        return False

    def write(self, payload):
        self._writes.append(payload)
        return len(payload)


def make_stub(tmp_path: Path, name: str, body: str) -> str:
    path = tmp_path / name
    path.write_text("#!/usr/bin/env python3\n" + textwrap.dedent(body))
    path.chmod(path.stat().st_mode | stat.S_IEXEC | stat.S_IXGRP | stat.S_IXOTH)
    return str(path)


SATDUMP_STUB = """
    import sys, time, os
    # Mimic satdump: write into the output directory, then wait to be stopped.
    args = sys.argv[1:]
    out = args[2] if args[0] == 'live' else args[1]
    try:
        os.makedirs(out, exist_ok=True)
        open(os.path.join(out, 'capture.bin'), 'wb').write(b'x' * 2048)
    except Exception:
        pass
    print('capture running', flush=True)
    time.sleep(300)
"""

RTL_PRESENT = """
    import sys
    print('  0:  Realtek, RTL2838UHIDIR, SN: 1', file=sys.stderr)
"""

RTL_ABSENT = """
    import sys
    print('No supported devices found.', file=sys.stderr)
    sys.exit(1)
"""


@pytest.fixture
def station_parts(tmp_path):
    """A station whose devices are stubs, with the writes visible."""
    writes = []

    def build(rotator_fails=False, sdr_present=True, satdump_body=SATDUMP_STUB,
              satdump_name="satdump_stub.py"):
        rotator = G550Rotator(
            "/dev/ttyTEST", 9600, park_azimuth=180, park_elevation=5,
            serial_factory=RecordingSerial(writes, fail=rotator_fails))
        rtl = make_stub(tmp_path, f"rtl_{sdr_present}.py",
                        RTL_PRESENT if sdr_present else RTL_ABSENT)
        satdump_path = make_stub(tmp_path, satdump_name, satdump_body)
        pipelines = tmp_path / "pipelines"
        pipelines.mkdir(exist_ok=True)
        (pipelines / "P.json").write_text('{"noaa_apt": {"name": "NOAA APT", "live": true}}')
        station = Station(
            rotator=rotator,
            sdr=RTLSDRProbe(rtl_test_binary=rtl),
            satdump=SatDump(binary=satdump_path, pipeline_dirs=[pipelines]),
        )
        return station, writes

    return build


class Clock:
    def __init__(self, now=BASE):
        self.now = now

    def __call__(self):
        return self.now


def run_pass(station, tmp_path, plan, clock=None, stop=None):
    """Execute a pass with time collapsed, so waits are instant."""
    clock = clock or Clock(BASE - timedelta(minutes=1))
    stop = stop or threading.Event()

    def sleeper(seconds, stop_event):
        # Advance the clock instead of waiting, with a token real pause so a
        # spawned capture process actually gets scheduled.
        clock.now = clock.now + timedelta(seconds=seconds)
        time.sleep(0.02)

    handler = StationPassHandler(
        station, tmp_path / "recordings", clock=clock, sleeper=sleeper)
    return handler.execute(stored_from(plan), stop), clock


# The full sequence ---------------------------------------------------------

def test_a_complete_pass_runs_the_whole_sequence(station_parts, tmp_path):
    station, writes = station_parts()
    plan = build_plan()

    outcome, _ = run_pass(station, tmp_path, plan)

    assert outcome.succeeded is True, outcome.detail

    # Pre-positioned before the pass, then tracked, then parked.
    assert writes[0] == b"W0180 010\r", "antenna was not pre-positioned at the first track point"
    assert writes[-1] == b"W0180 005\r", "antenna was not parked afterwards"
    # Every command is a well-formed G-550 instruction.
    for payload in writes:
        assert payload.startswith(b"W") and payload.endswith(b"\r")
        assert len(payload) == len(b"W0000 000\r")

    # A capture directory with content was produced.
    output = tmp_path / "recordings" / "pass-1"
    assert output.is_dir()
    assert (output / "capture.bin").exists()
    assert "bytes=" in outcome.detail
    assert station.capture_active is False


def test_the_antenna_follows_the_track(station_parts, tmp_path):
    station, writes = station_parts()
    plan = build_plan(track_points=6)

    run_pass(station, tmp_path, plan)

    # The park command is last; the rest are the track.
    tracked = writes[:-1]
    assert len(tracked) == 6
    azimuths = [int(payload[1:5]) for payload in tracked]
    elevations = [int(payload[6:9]) for payload in tracked]
    assert azimuths == [180, 195, 210, 225, 240, 255]
    assert elevations == [10, 15, 20, 25, 30, 35]


# Redundant serial writes at a rotator are worth avoiding.
def test_negligible_pointing_changes_are_skipped(station_parts, tmp_path):
    station, writes = station_parts()
    plan = build_plan(track_points=4)
    # Flatten the track so consecutive points barely differ.
    for point in plan.track:
        point.azimuth_degrees = 180.0
        point.elevation_degrees = 10.0

    run_pass(station, tmp_path, plan)

    # One pre-position plus the park, with the identical points skipped.
    assert len(writes) == 2
    assert MIN_POINTING_CHANGE_DEGREES > 0


# Capture command ----------------------------------------------------------

def test_process_mode_runs_a_live_decode(station_parts, tmp_path):
    station, _ = station_parts()
    plan = build_plan(mode=passplan_pb2.RECORDING_MODE_PROCESS, pipeline="noaa_apt")
    started = {}

    original = station.satdump.start

    def capture_command(command):
        started["command"] = command
        return original(command)

    station.satdump.start = capture_command
    run_pass(station, tmp_path, plan)

    command = started["command"]
    assert command[1] == "live"
    assert command[2] == "noaa_apt"
    assert command[command.index("--frequency") + 1] == "137100000"
    assert command[command.index("--samplerate") + 1] == "2048000"
    assert "--bias" in command
    assert command[command.index("--ppm_correction") + 1] == "3"
    # SatDump bounds its own run, so a Worker crash cannot leave it going.
    assert "--timeout" in command


def test_raw_mode_records_baseband(station_parts, tmp_path):
    station, _ = station_parts()
    plan = build_plan(mode=passplan_pb2.RECORDING_MODE_RAW, pipeline="")
    started = {}

    original = station.satdump.start
    station.satdump.start = lambda command: (started.setdefault("command", command),
                                             original(command))[1]
    run_pass(station, tmp_path, plan)

    command = started["command"]
    assert command[1] == "record"
    assert command[command.index("--baseband_format") + 1] == "ziq"


# One RTL-SDR cannot feed two SatDump processes.
def test_raw_and_process_reports_the_shortfall(station_parts, tmp_path):
    station, _ = station_parts()
    plan = build_plan(mode=passplan_pb2.RECORDING_MODE_RAW_AND_PROCESS)

    outcome, _ = run_pass(station, tmp_path, plan)

    assert outcome.succeeded is True
    assert "raw_and_process ran as live decode only" in outcome.detail


# Refusals -----------------------------------------------------------------

def test_a_plan_with_no_track_is_refused(station_parts, tmp_path):
    station, writes = station_parts()
    plan = build_plan(track_points=0)

    outcome, _ = run_pass(station, tmp_path, plan)

    assert outcome.succeeded is False
    assert "no pointing timeline" in outcome.detail
    # Nothing was commanded except the safety park.
    assert all(payload == b"W0180 005\r" for payload in writes)


def test_a_plan_whose_window_has_closed_is_refused(station_parts, tmp_path):
    station, _ = station_parts()
    plan = build_plan(aos=BASE - timedelta(hours=2))

    handler = StationPassHandler(station, tmp_path / "recordings", clock=Clock(BASE))
    outcome = handler.execute(stored_from(plan), threading.Event())

    assert outcome.succeeded is False
    assert "already closed" in outcome.detail


def test_an_unsupported_plan_version_is_refused(station_parts, tmp_path):
    station, _ = station_parts()
    plan = build_plan()
    plan.plan_version = 99

    outcome, _ = run_pass(station, tmp_path, plan)

    assert outcome.succeeded is False
    assert "plan version 99" in outcome.detail


def test_a_plan_without_radio_settings_is_refused(station_parts, tmp_path):
    station, _ = station_parts()
    plan = build_plan(frequency=0)

    outcome, _ = run_pass(station, tmp_path, plan)

    assert outcome.succeeded is False
    assert "radio settings" in outcome.detail


def test_a_corrupt_plan_is_refused(station_parts, tmp_path):
    station, _ = station_parts()
    stored = StoredPlan(
        pass_id="pass-1", generation="x" * 64, plan_version=1,
        encoded=b"\xff\xfe\xfd\xfc", aos=BASE, los=BASE + timedelta(minutes=5),
        state="ready", received_at=BASE, updated_at=BASE,
    )
    handler = StationPassHandler(station, tmp_path / "recordings", clock=Clock(BASE))

    outcome = handler.execute(stored, threading.Event())

    assert outcome.succeeded is False
    assert "unreadable" in outcome.detail


# Hardware faults ----------------------------------------------------------

# Without SatDump there is nothing to capture with, so do not slew the antenna.
def test_missing_satdump_fails_the_pass_before_moving(station_parts, tmp_path):
    station, writes = station_parts(satdump_body="import sys\n")
    station.satdump._binary = "satdump-does-not-exist"  # noqa: SLF001
    plan = build_plan()

    outcome, _ = run_pass(station, tmp_path, plan)

    assert outcome.succeeded is False
    assert "satdump unavailable" in outcome.detail


# A recording made with a stuck antenna is still worth keeping.
def test_a_broken_rotator_does_not_by_itself_fail_the_pass(station_parts, tmp_path):
    station, _ = station_parts(rotator_fails=True)
    plan = build_plan()

    outcome, _ = run_pass(station, tmp_path, plan)

    assert outcome.succeeded is True
    assert "pointing failed" in outcome.detail


def test_a_broken_rotator_is_reported_once_not_per_point(station_parts, tmp_path):
    station, _ = station_parts(rotator_fails=True)
    plan = build_plan(track_points=8)

    outcome, _ = run_pass(station, tmp_path, plan)

    assert outcome.detail.count("pointing failed") == 1


def test_a_missing_sdr_is_reported(station_parts, tmp_path):
    station, _ = station_parts(sdr_present=False)
    plan = build_plan()

    outcome, _ = run_pass(station, tmp_path, plan)

    assert "sdr unavailable" in outcome.detail


def test_a_capture_that_will_not_start_fails_the_pass(station_parts, tmp_path):
    station, _ = station_parts()

    def refuse(command):
        from aagasa_worker.hardware.errors import SatDumpError
        raise SatDumpError("device busy")

    station.satdump.start = refuse
    plan = build_plan()

    outcome, _ = run_pass(station, tmp_path, plan)

    assert outcome.succeeded is False
    assert "capture did not start" in outcome.detail


# Shutdown -----------------------------------------------------------------

def test_shutdown_during_a_pass_stops_cleanly(station_parts, tmp_path):
    station, writes = station_parts()
    plan = build_plan()
    stop = threading.Event()
    stop.set()  # already shutting down

    outcome, _ = run_pass(station, tmp_path, plan, stop=stop)

    assert outcome.succeeded is False
    assert "shutting down" in outcome.detail
    assert station.capture_active is False
    # The antenna is still parked on the way out.
    assert writes[-1] == b"W0180 005\r"


# Whatever goes wrong, the station must end up safe.
def test_the_station_is_always_left_safe(station_parts, tmp_path):
    for kwargs in ({}, {"rotator_fails": False}, {"sdr_present": False}):
        station, writes = station_parts(**kwargs)
        plan = build_plan()

        run_pass(station, tmp_path, plan)

        assert station.capture_active is False, "a capture was left running"
        if writes:
            assert writes[-1] == b"W0180 005\r", "the antenna was not parked"


# Disk pressure -------------------------------------------------------------
#
# Filling the disk mid-pass loses the recording anyway and endangers the ones
# already waiting to upload (spec.md section 19.2), so a pass the disk cannot
# hold is refused before the antenna moves.

def test_a_pass_the_disk_cannot_hold_is_refused(station_parts, tmp_path, monkeypatch):
    station, writes = station_parts()
    plan = build_plan()

    monkeypatch.setattr(pass_execution, "free_bytes", lambda path: 4 * (1 << 20))

    outcome, _ = run_pass(station, tmp_path, plan)

    assert outcome.succeeded is False
    assert "not enough disk space" in outcome.detail
    # It says both numbers, or an operator cannot tell how short they are.
    assert "MiB" in outcome.detail
    # Nothing was captured, and the antenna never slewed to the pass: the one
    # command is the park that always follows, leaving the station safe.
    assert not (tmp_path / "recordings" / "pass-1").exists()
    assert writes == [b"W0180 005\r"]


def test_a_pass_that_fits_is_not_refused(station_parts, tmp_path, monkeypatch):
    station, _ = station_parts()
    plan = build_plan()

    monkeypatch.setattr(pass_execution, "free_bytes", lambda path: 500 * (1 << 30))

    outcome, _ = run_pass(station, tmp_path, plan)

    assert outcome.succeeded is True, outcome.detail


# An unmeasurable disk must not block a pass that might have been fine.
def test_an_unreadable_disk_does_not_refuse_the_pass(station_parts, tmp_path, monkeypatch):
    station, _ = station_parts()
    plan = build_plan()

    def unreadable(path):
        raise OSError("filesystem went away")

    monkeypatch.setattr(pass_execution, "free_bytes", unreadable)

    outcome, _ = run_pass(station, tmp_path, plan)

    assert outcome.succeeded is True, outcome.detail
