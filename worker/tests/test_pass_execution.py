"""Executing a PassPlan against the station.

The rotator is a recording transport and SatDump is a stub executable, so the
whole execution sequence runs for real without a G-550 or an RTL-SDR attached.
Only the physical devices are absent.
"""

import json
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
from aagasa_worker.hardware.satdump import ProcessResult, SatDump
from aagasa_worker.hardware.sdr import RTLSDRProbe
from aagasa_worker.hardware.station import Station
from aagasa_worker import pass_execution
from aagasa_worker.pass_execution import (
    MIN_POINTING_CHANGE_DEGREES,
    ExecutionReport,
    PlanNotExecutable,
    StationPassHandler,
    capture_start,
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

EMPTY_CADU_STUB = """
    import os, signal, sys, time
    args = sys.argv[1:]
    out = args[2] if args[0] == 'live' else args[1]
    def finish(signum, frame):
        sys.exit(0)
    signal.signal(signal.SIGTERM, finish)
    os.makedirs(out, exist_ok=True)
    open(os.path.join(out, 'meteor_m2-x_lrpt.cadu'), 'wb').close()
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
              satdump_name="satdump_stub.py", pipeline_definition=None):
        rotator = G550Rotator(
            "/dev/ttyTEST", 9600, park_azimuth=180, park_elevation=5,
            serial_factory=RecordingSerial(writes, fail=rotator_fails))
        rtl = make_stub(tmp_path, f"rtl_{sdr_present}.py",
                        RTL_PRESENT if sdr_present else RTL_ABSENT)
        satdump_path = make_stub(tmp_path, satdump_name, satdump_body)
        pipelines = tmp_path / "pipelines"
        pipelines.mkdir(exist_ok=True)
        definition = pipeline_definition if pipeline_definition is not None else {
            "noaa_apt": {"name": "NOAA APT", "live": True},
        }
        (pipelines / "P.json").write_text(json.dumps(definition))
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
        clock.now = clock.now + timedelta(seconds=seconds)
        time.sleep(0.02)

    handler = StationPassHandler(
        station, tmp_path / "recordings", clock=clock, sleeper=sleeper,
        capture_poll_seconds=30)
    return handler.execute(stored_from(plan), stop), clock



def test_a_complete_pass_runs_the_whole_sequence(station_parts, tmp_path):
    station, writes = station_parts()
    plan = build_plan()

    outcome, _ = run_pass(station, tmp_path, plan)

    assert outcome.succeeded is True, outcome.detail

    assert writes[0] == b"W0180 010\r", "antenna was not pre-positioned at the first track point"
    assert writes[-1] == b"W0180 005\r", "antenna was not parked afterwards"
    for payload in writes:
        assert payload.startswith(b"W") and payload.endswith(b"\r")
        assert len(payload) == len(b"W0000 000\r")

    output = tmp_path / "recordings" / "pass-1"
    assert output.is_dir()
    assert (output / "capture.bin").exists()
    assert "bytes=" in outcome.detail
    assert station.capture_active is False


def test_the_antenna_follows_the_track(station_parts, tmp_path):
    station, writes = station_parts()
    plan = build_plan(track_points=6)

    run_pass(station, tmp_path, plan)

    tracked = writes[:-1]
    assert len(tracked) == 6
    azimuths = [int(payload[1:5]) for payload in tracked]
    elevations = [int(payload[6:9]) for payload in tracked]
    assert azimuths == [180, 195, 210, 225, 240, 255]
    assert elevations == [10, 15, 20, 25, 30, 35]


def test_negligible_pointing_changes_are_skipped(station_parts, tmp_path):
    station, writes = station_parts()
    plan = build_plan(track_points=4)
    for point in plan.track:
        point.azimuth_degrees = 180.0
        point.elevation_degrees = 10.0

    run_pass(station, tmp_path, plan)

    assert len(writes) == 2
    assert MIN_POINTING_CHANGE_DEGREES > 0



def test_process_mode_runs_a_live_decode(station_parts, tmp_path):
    station, _ = station_parts()
    plan = build_plan(mode=passplan_pb2.RECORDING_MODE_PROCESS, pipeline="noaa_apt")
    started = {}

    original = station.satdump.start

    def capture_command(command, **kwargs):
        started["command"] = command
        return original(command, **kwargs)

    station.satdump.start = capture_command
    run_pass(station, tmp_path, plan)

    command = started["command"]
    assert command[1] == "live"
    assert command[2] == "noaa_apt"
    assert command[command.index("--frequency") + 1] == "137100000"
    assert command[command.index("--samplerate") + 1] == "2048000"
    assert "--bias" in command
    assert command[command.index("--ppm_correction") + 1] == "3"
    assert "--timeout" in command


def test_process_mode_uses_the_pipelines_primary_frequency(station_parts, tmp_path):
    station, _ = station_parts(pipeline_definition={
        "meteor_m2-x_lrpt": {
            "name": "METEOR M2 LRPT",
            "live": True,
            "frequencies": [["Primary", 137.9e6], ["Backup", 137.1e6]],
        },
    })
    plan = build_plan(mode=passplan_pb2.RECORDING_MODE_PROCESS,
                      pipeline="meteor_m2-x_lrpt")
    started = {}

    original = station.satdump.start

    def capture_command(command, **kwargs):
        started["command"] = command
        return original(command, **kwargs)

    station.satdump.start = capture_command
    run_pass(station, tmp_path, plan)

    command = started["command"]
    assert command[2] == "meteor_m2-x_lrpt"
    assert command[command.index("--frequency") + 1] == "137900000"


def test_a_capture_with_only_an_empty_cadu_fails_the_pass(station_parts, tmp_path):
    station, _ = station_parts(satdump_body=EMPTY_CADU_STUB)
    plan = build_plan()

    outcome, _ = run_pass(station, tmp_path, plan)

    assert outcome.succeeded is False
    assert "no non-empty decoded output" in outcome.detail


def test_a_shutdown_segfault_does_not_fail_a_pass_with_output(tmp_path):
    output = tmp_path / "out"
    output.mkdir()
    (output / "meteor.cadu").write_bytes(b"x" * 1024)
    report = ExecutionReport(
        capture_started=True,
        capture_result=ProcessResult(
            succeeded=True, exit_code=-11, stdout="", stderr="", terminated=True),
        output_path=str(output),
        output_bytes=1024,
    )

    assert StationPassHandler._succeeded(report) is True


def test_a_shutdown_segfault_still_fails_a_pass_without_output(tmp_path):
    output = tmp_path / "out"
    output.mkdir()
    (output / "meteor.cadu").touch()
    (output / "satdump.log").write_text("Done! Goodbye")
    report = ExecutionReport(
        capture_started=True,
        capture_result=ProcessResult(
            succeeded=True, exit_code=-11, stdout="", stderr="", terminated=True),
        output_path=str(output),
        output_bytes=14,
    )

    assert StationPassHandler._succeeded(report) is False


def test_raw_mode_records_baseband(station_parts, tmp_path):
    station, _ = station_parts()
    plan = build_plan(mode=passplan_pb2.RECORDING_MODE_RAW, pipeline="")
    started = {}

    original = station.satdump.start
    station.satdump.start = lambda command, **kwargs: (
        started.setdefault("command", command), original(command, **kwargs))[1]
    run_pass(station, tmp_path, plan)

    command = started["command"]
    assert command[1] == "record"
    assert command[command.index("--baseband_format") + 1] == "ziq"


def test_raw_and_process_reports_the_shortfall(station_parts, tmp_path):
    station, _ = station_parts()
    plan = build_plan(mode=passplan_pb2.RECORDING_MODE_RAW_AND_PROCESS)

    outcome, _ = run_pass(station, tmp_path, plan)

    assert outcome.succeeded is True
    assert "raw_and_process ran as live decode only" in outcome.detail



def test_a_plan_with_no_track_is_refused(station_parts, tmp_path):
    station, writes = station_parts()
    plan = build_plan(track_points=0)

    outcome, _ = run_pass(station, tmp_path, plan)

    assert outcome.succeeded is False
    assert "no pointing timeline" in outcome.detail
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



def test_missing_satdump_fails_the_pass_before_moving(station_parts, tmp_path):
    station, writes = station_parts(satdump_body="import sys\n")
    station.satdump._binary = "satdump-does-not-exist"  # noqa: SLF001
    plan = build_plan()

    outcome, _ = run_pass(station, tmp_path, plan)

    assert outcome.succeeded is False
    assert "satdump unavailable" in outcome.detail


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

    def refuse(command, **kwargs):
        from aagasa_worker.hardware.errors import SatDumpError
        raise SatDumpError("device busy")

    station.satdump.start = refuse
    plan = build_plan()

    outcome, _ = run_pass(station, tmp_path, plan)

    assert outcome.succeeded is False
    assert "capture did not start" in outcome.detail



def test_shutdown_during_a_pass_stops_cleanly(station_parts, tmp_path):
    station, writes = station_parts()
    plan = build_plan()
    stop = threading.Event()
    stop.set()

    outcome, _ = run_pass(station, tmp_path, plan, stop=stop)

    assert outcome.succeeded is False
    assert "shutting down" in outcome.detail
    assert station.capture_active is False
    assert writes[-1] == b"W0180 005\r"


def test_the_station_is_always_left_safe(station_parts, tmp_path):
    for kwargs in ({}, {"rotator_fails": False}, {"sdr_present": False}):
        station, writes = station_parts(**kwargs)
        plan = build_plan()

        run_pass(station, tmp_path, plan)

        assert station.capture_active is False, "a capture was left running"
        if writes:
            assert writes[-1] == b"W0180 005\r", "the antenna was not parked"



def test_a_pass_the_disk_cannot_hold_is_refused(station_parts, tmp_path, monkeypatch):
    station, writes = station_parts()
    plan = build_plan()

    monkeypatch.setattr(pass_execution, "free_bytes", lambda path: 4 * (1 << 20))

    outcome, _ = run_pass(station, tmp_path, plan)

    assert outcome.succeeded is False
    assert "not enough disk space" in outcome.detail
    assert "MiB" in outcome.detail
    assert not (tmp_path / "recordings" / "pass-1").exists()
    assert writes == [b"W0180 005\r"]


def test_a_pass_that_fits_is_not_refused(station_parts, tmp_path, monkeypatch):
    station, _ = station_parts()
    plan = build_plan()

    monkeypatch.setattr(pass_execution, "free_bytes", lambda path: 500 * (1 << 30))

    outcome, _ = run_pass(station, tmp_path, plan)

    assert outcome.succeeded is True, outcome.detail


def test_an_unreadable_disk_does_not_refuse_the_pass(station_parts, tmp_path, monkeypatch):
    station, _ = station_parts()
    plan = build_plan()

    def unreadable(path):
        raise OSError("filesystem went away")

    monkeypatch.setattr(pass_execution, "free_bytes", unreadable)

    outcome, _ = run_pass(station, tmp_path, plan)

    assert outcome.succeeded is True, outcome.detail


def test_a_fixed_antenna_records_without_touching_the_rotator(station_parts, tmp_path):
    station, writes = station_parts(rotator_fails=True)
    station.fixed_antenna = True
    plan = build_plan(track_points=6)

    outcome, _ = run_pass(station, tmp_path, plan)

    assert outcome.succeeded is True, outcome.detail
    assert writes == [], "a fixed antenna must never be commanded or parked"
    assert "rotator" not in outcome.detail
    assert "pointing failed" not in outcome.detail
    assert "pointing_commands=0" in outcome.detail
    assert (tmp_path / "recordings" / "pass-1" / "capture.bin").exists()


def test_switching_back_to_a_rotator_points_again(station_parts, tmp_path):
    station, writes = station_parts()
    station.fixed_antenna = True
    station.fixed_antenna = False
    plan = build_plan()

    outcome, _ = run_pass(station, tmp_path, plan)

    assert outcome.succeeded is True, outcome.detail
    assert writes[0] == b"W0180 010\r"
    assert writes[-1] == b"W0180 005\r"


SATDUMP_DIES_STUB = """
    import sys
    print('rtlsdr: no device found at index 0', flush=True)
    print('Fatal error: could not open source', file=sys.stderr, flush=True)
    sys.exit(1)
"""


def run_pass_slowly(station, tmp_path, plan, stop=None):
    """Like run_pass, but gives a stub enough real time to exit by itself."""
    clock = Clock(BASE - timedelta(minutes=1))
    stop = stop or threading.Event()

    def sleeper(seconds, stop_event):
        clock.now = clock.now + timedelta(seconds=seconds)
        time.sleep(0.15)

    handler = StationPassHandler(
        station, tmp_path / "recordings", clock=clock, sleeper=sleeper,
        capture_poll_seconds=30)
    return handler.execute(stored_from(plan), stop)


def test_satdump_output_goes_to_a_log_beside_the_capture(station_parts, tmp_path):
    station, _ = station_parts()
    plan = build_plan()

    outcome, _ = run_pass(station, tmp_path, plan)

    assert outcome.succeeded is True, outcome.detail
    log = tmp_path / "recordings" / "pass-1" / "satdump.log"
    assert "capture running" in log.read_text()


def test_a_capture_that_dies_ends_the_pass_early(station_parts, tmp_path):
    station, writes = station_parts(satdump_body=SATDUMP_DIES_STUB,
                                    satdump_name="dies.py")
    plan = build_plan(track_points=6)

    outcome = run_pass_slowly(station, tmp_path, plan)

    assert outcome.succeeded is False
    assert "satdump exited before the recording window ended (exit code 1)" in outcome.detail
    assert "no device found" in outcome.detail, "the log tail must explain the failure"
    assert "could not open source" in outcome.detail
    tracked = writes[:-1]
    assert len(tracked) < 6, "tracking must stop once the capture is gone"
    assert writes[-1] == b"W0180 005\r", "the antenna was not parked"
    assert station.capture_active is False


def test_a_capture_that_dies_with_a_fixed_antenna_never_touches_the_rotator(
        station_parts, tmp_path):
    station, writes = station_parts(satdump_body=SATDUMP_DIES_STUB,
                                    satdump_name="dies.py")
    station.fixed_antenna = True

    outcome = run_pass_slowly(station, tmp_path, build_plan())

    assert outcome.succeeded is False
    assert "exit code 1" in outcome.detail
    assert writes == []


def test_a_deliberate_stop_is_not_reported_as_a_dead_capture(station_parts, tmp_path):
    station, writes = station_parts()
    plan = build_plan(track_points=6)
    stop = threading.Event()
    clock = Clock(BASE - timedelta(minutes=1))
    calls = []

    def sleeper(seconds, stop_event):
        clock.now = clock.now + timedelta(seconds=seconds)
        calls.append(seconds)
        if len(calls) == 4:
            stop_event.set()
        time.sleep(0.02)

    handler = StationPassHandler(station, tmp_path / "recordings", clock=clock,
                                 sleeper=sleeper, capture_poll_seconds=30)
    outcome = handler.execute(stored_from(plan), stop)

    assert "tracking stopped by shutdown" in outcome.detail
    assert "exited before" not in outcome.detail
    assert "satdump_log_tail" not in outcome.detail
    assert station.capture_active is False
    assert writes[-1] == b"W0180 005\r"


def test_a_capture_that_will_not_start_does_not_track(station_parts, tmp_path):
    station, writes = station_parts()

    def refuse(command, **kwargs):
        from aagasa_worker.hardware.errors import SatDumpError
        raise SatDumpError("device busy")

    station.satdump.start = refuse
    outcome, _ = run_pass(station, tmp_path, build_plan(track_points=6))

    assert outcome.succeeded is False
    assert writes == [b"W0180 010\r", b"W0180 005\r"], \
        "only the pre-position and the park; no tracking without a capture"


def test_a_pipeline_that_is_not_installed_is_refused_before_moving(station_parts, tmp_path):
    station, writes = station_parts()
    started = []
    station.satdump.start = lambda command, **kwargs: started.append(command)
    plan = build_plan(pipeline="meteor_m2_lrpt")

    outcome, _ = run_pass(station, tmp_path, plan)

    assert outcome.succeeded is False
    assert "pipeline 'meteor_m2_lrpt' is not installed on this station" in outcome.detail
    assert started == []
    assert b"W0180 010\r" not in writes, "the antenna must not be pre-positioned"


def test_a_custom_pipeline_is_refused_rather_than_run_as_a_stock_one(station_parts, tmp_path):
    station, writes = station_parts()
    started = []
    station.satdump.start = lambda command, **kwargs: started.append(command)
    plan = build_plan(pipeline="noaa_apt")
    plan.pipeline.is_custom = True
    plan.pipeline.definition_json = '{"noaa_apt": {"name": "mine"}}'

    outcome, _ = run_pass(station, tmp_path, plan)

    assert outcome.succeeded is False
    assert "custom pipelines are not supported by this station yet" in outcome.detail
    assert started == []
    assert b"W0180 010\r" not in writes


def test_raw_mode_does_not_need_the_pipeline_installed(station_parts, tmp_path):
    station, _ = station_parts()
    plan = build_plan(mode=passplan_pb2.RECORDING_MODE_RAW, pipeline="not_here")

    outcome, _ = run_pass(station, tmp_path, plan)

    assert outcome.succeeded is True, outcome.detail


def test_capture_start_is_the_earlier_of_aos_and_recording_start():
    plan = build_plan()
    plan.recording_start.FromDatetime(BASE - timedelta(minutes=3))

    assert capture_start(stored_from(plan)) == BASE - timedelta(minutes=3)

    plan.recording_start.FromDatetime(BASE + timedelta(seconds=5))
    assert capture_start(stored_from(plan)) == BASE


def test_capture_start_falls_back_to_aos_for_an_unreadable_plan():
    stored = StoredPlan(
        pass_id="p", generation="g", plan_version=1, encoded=b"\xff\xfe\xfd",
        aos=BASE, los=BASE + timedelta(minutes=5), state="ready",
        received_at=BASE, updated_at=BASE,
    )

    assert capture_start(stored) == BASE
