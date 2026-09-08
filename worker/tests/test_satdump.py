"""SatDump discovery and process control.

Pipeline discovery runs against the real installation when one is present, and
against fixture files otherwise. Process control uses a stub executable so
start, stop, exit codes and output capture are genuinely exercised.
"""

import json
import os
import shutil
import stat
import textwrap
import time

import pytest

from aagasa_worker.hardware.errors import SatDumpError
from pathlib import Path

from aagasa_worker.hardware.satdump import (
    MAX_CAPTURED_OUTPUT,
    SatDump,
    discover_pipelines,
    parse_pipeline_file,
    strip_jsonc_comments,
)


@pytest.fixture
def pipeline_dir(tmp_path):
    """Two pipeline files shaped like a real SatDump installation."""
    directory = tmp_path / "pipelines"
    directory.mkdir()
    (directory / "ACE.json").write_text(json.dumps({
        "ace_s_link": {"name": "ACE S-band downlink", "live": True, "work": {}},
    }))
    (directory / "NOAA.json").write_text(json.dumps({
        "noaa_apt": {"name": "NOAA APT", "live": True, "work": {}},
        "noaa_hrpt": {"name": "NOAA HRPT", "live": False, "work": {}},
    }))
    return directory


def test_discovers_pipelines_from_files(pipeline_dir):
    pipelines = discover_pipelines([pipeline_dir])

    identifiers = [pipeline.identifier for pipeline in pipelines]
    assert identifiers == ["ace_s_link", "noaa_apt", "noaa_hrpt"]

    by_id = {pipeline.identifier: pipeline for pipeline in pipelines}
    assert by_id["noaa_apt"].name == "NOAA APT"
    assert by_id["noaa_apt"].supports_live is True
    assert by_id["noaa_hrpt"].supports_live is False
    assert by_id["ace_s_link"].source_file == "ACE.json"


def test_one_malformed_file_does_not_hide_the_rest(pipeline_dir):
    (pipeline_dir / "Broken.json").write_text("{not valid json")
    (pipeline_dir / "NotAnObject.json").write_text("[1, 2, 3]")

    pipelines = discover_pipelines([pipeline_dir])

    assert [p.identifier for p in pipelines] == ["ace_s_link", "noaa_apt", "noaa_hrpt"]


def test_earlier_directory_wins(tmp_path, pipeline_dir):
    override = tmp_path / "user"
    override.mkdir()
    (override / "ACE.json").write_text(json.dumps({
        "ace_s_link": {"name": "Locally overridden", "live": False},
    }))

    pipelines = discover_pipelines([override, pipeline_dir])
    by_id = {pipeline.identifier: pipeline for pipeline in pipelines}

    assert by_id["ace_s_link"].name == "Locally overridden"


def test_missing_directory_is_not_an_error(tmp_path):
    assert discover_pipelines([tmp_path / "nope"]) == []


def test_discovers_pipelines_from_the_real_installation():
    """Verify against the SatDump actually installed, when there is one."""
    system_dir = "/usr/share/satdump/pipelines"
    if not os.path.isdir(system_dir):
        pytest.skip("no system SatDump pipeline directory")

    pipelines = discover_pipelines([system_dir])

    assert len(pipelines) > 10, "a real SatDump ships many pipelines"
    assert any(pipeline.supports_live for pipeline in pipelines)
    assert all(pipeline.identifier and pipeline.name for pipeline in pipelines)


def test_check_finds_the_real_satdump():
    if shutil.which("satdump") is None:
        pytest.skip("satdump is not installed")

    assert SatDump().check().endswith("satdump")


def test_check_reports_a_missing_binary():
    with pytest.raises(SatDumpError, match="not found"):
        SatDump(binary="satdump-does-not-exist").check()


# Command construction -----------------------------------------------------

SDR_SETTINGS = {"source": "rtlsdr", "samplerate": 2048000, "gain": 40}


def test_live_command_shape():
    command = SatDump(binary="satdump").build_live_command(
        "noaa_apt", "/out/pass-1", 137_100_000, SDR_SETTINGS)

    assert command[:4] == ["satdump", "live", "noaa_apt", "/out/pass-1"]
    assert "--frequency" in command
    assert command[command.index("--frequency") + 1] == "137100000"
    assert command[command.index("--samplerate") + 1] == "2048000"


def test_record_command_requests_baseband():
    command = SatDump(binary="satdump").build_record_command(
        "/out/pass-1", 137_100_000, SDR_SETTINGS)

    assert command[:3] == ["satdump", "record", "/out/pass-1"]
    assert command[command.index("--baseband_format") + 1] == "ziq"


def test_optional_settings_are_only_added_when_set():
    satdump = SatDump(binary="satdump")

    without = satdump.build_live_command("p", "/out", 100, SDR_SETTINGS)
    assert "--bias" not in without
    assert "--ppm_correction" not in without

    with_extras = satdump.build_live_command(
        "p", "/out", 100, {**SDR_SETTINGS, "bias_tee": True, "ppm": 12})
    assert "--bias" in with_extras
    assert with_extras[with_extras.index("--ppm_correction") + 1] == "12"


# A hostile pipeline name must stay a single argument, never shell syntax.
def test_arguments_are_never_shell_interpreted():
    hostile = "noaa_apt; rm -rf /"
    command = SatDump(binary="satdump").build_live_command(
        hostile, "/out", 137_100_000, SDR_SETTINGS)

    assert hostile in command
    assert command.count(hostile) == 1
    # No element got split on the shell metacharacters.
    assert not any(argument.strip() == "rm" for argument in command)


# Process lifecycle --------------------------------------------------------

def make_stub(tmp_path, body: str) -> str:
    """Write an executable stub standing in for the satdump binary."""
    path = tmp_path / "satdump_stub.py"
    path.write_text("#!/usr/bin/env python3\n" + textwrap.dedent(body))
    path.chmod(path.stat().st_mode | stat.S_IEXEC | stat.S_IXGRP | stat.S_IXOTH)
    return str(path)


def test_run_reports_success_and_captures_stdout(tmp_path):
    stub = make_stub(tmp_path, """
        import sys
        print("decoded 42 frames")
        sys.exit(0)
    """)

    result = SatDump(binary=stub).run([stub])

    assert result.succeeded is True
    assert result.exit_code == 0
    assert "decoded 42 frames" in result.stdout


def test_run_reports_failure_and_captures_stderr(tmp_path):
    stub = make_stub(tmp_path, """
        import sys
        print("device open failed", file=sys.stderr)
        sys.exit(3)
    """)

    result = SatDump(binary=stub).run([stub])

    assert result.succeeded is False
    assert result.exit_code == 3
    assert "device open failed" in result.stderr


def test_run_times_out_deterministically(tmp_path):
    stub = make_stub(tmp_path, """
        import time
        time.sleep(30)
    """)

    result = SatDump(binary=stub).run([stub], timeout_seconds=1)

    assert result.succeeded is False
    assert result.terminated is True
    assert result.exit_code is None


def test_start_and_stop_a_long_running_capture(tmp_path):
    stub = make_stub(tmp_path, """
        import time, sys
        print("capture running", flush=True)
        time.sleep(60)
    """)
    satdump = SatDump(binary=stub)

    process = satdump.start([stub])
    assert process.poll() is None

    result = satdump.stop(process)

    assert result.terminated is True
    assert process.poll() is not None


def test_stop_kills_a_process_that_ignores_termination(tmp_path):
    stub = make_stub(tmp_path, """
        import signal, time
        signal.signal(signal.SIGTERM, signal.SIG_IGN)
        time.sleep(60)
    """)
    satdump = SatDump(binary=stub)

    process = satdump.start([stub])
    time.sleep(0.3)
    result = satdump.stop(process, grace_seconds=1)

    assert result.succeeded is False
    assert result.terminated is True
    assert process.poll() is not None


def test_stop_collects_an_already_finished_process(tmp_path):
    stub = make_stub(tmp_path, """
        import sys
        print("done")
        sys.exit(0)
    """)
    satdump = SatDump(binary=stub)

    process = satdump.start([stub])
    time.sleep(0.5)
    result = satdump.stop(process)

    assert result.succeeded is True
    assert result.exit_code == 0
    assert result.terminated is False


def test_starting_a_missing_binary_raises():
    with pytest.raises(SatDumpError, match="not found"):
        SatDump(binary="satdump-does-not-exist").start(["satdump-does-not-exist"])


def test_captured_output_is_bounded(tmp_path):
    stub = make_stub(tmp_path, f"""
        print("x" * {MAX_CAPTURED_OUTPUT * 3})
    """)

    result = SatDump(binary=stub).run([stub])

    assert result.succeeded is True
    assert len(result.stdout) <= MAX_CAPTURED_OUTPUT


# JSONC handling -----------------------------------------------------------
# SatDump's pipeline files use comments, which strict JSON rejects. Parsing
# them strictly silently hid most of the catalogue on a stock install.

def test_line_and_block_comments_are_stripped():
    text = """
    {
        // a leading comment
        "a": {"name": "A", "live": true}, /* trailing block */
        "b": {"name": "B" /*, "live": true */}
    }
    """
    parsed = parse_pipeline_file(text)

    assert set(parsed) == {"a", "b"}
    assert parsed["b"] == {"name": "B"}


def test_comment_markers_inside_strings_are_preserved():
    text = '{"a": {"name": "http://example.com/x", "note": "not /* a comment */"}}'

    parsed = parse_pipeline_file(text)

    assert parsed["a"]["name"] == "http://example.com/x"
    assert parsed["a"]["note"] == "not /* a comment */"


def test_escaped_quotes_do_not_confuse_the_stripper():
    text = r'{"a": {"name": "say \"hi\" // not a comment"}}'

    parsed = parse_pipeline_file(text)

    assert parsed["a"]["name"] == r'say "hi" // not a comment'


def test_plain_json_is_unchanged():
    text = '{"a": {"name": "A"}}'
    assert strip_jsonc_comments(text) == text


def test_every_real_pipeline_file_parses():
    """No file in a stock SatDump install may be skipped.

    Strict JSON parsing dropped 25 of 71 files here, hiding 104 pipelines.
    """
    system_dir = Path("/usr/share/satdump/pipelines")
    if not system_dir.is_dir():
        pytest.skip("no system SatDump pipeline directory")

    unparsed = []
    for path in sorted(system_dir.glob("*.json")):
        try:
            parse_pipeline_file(path.read_text(encoding="utf-8"))
        except json.JSONDecodeError as error:
            unparsed.append(f"{path.name}: {error}")

    assert unparsed == [], f"pipeline files still unparsed: {unparsed}"


def test_real_installation_yields_a_full_catalogue():
    system_dir = Path("/usr/share/satdump/pipelines")
    if not system_dir.is_dir():
        pytest.skip("no system SatDump pipeline directory")

    pipelines = discover_pipelines([system_dir])

    # Well above the 81 that strict parsing produced.
    assert len(pipelines) > 150
    identifiers = {pipeline.identifier for pipeline in pipelines}
    # A pipeline from a comment-bearing file must now be visible.
    assert "gk2a_lrit" in identifiers
