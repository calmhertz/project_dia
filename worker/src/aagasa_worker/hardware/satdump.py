"""SatDump discovery and process control.

Pipeline discovery reads the installed SatDump pipeline directory. Each file
there is JSON mapping one or more pipeline identifiers to their definition,
verified against a real SatDump 1.x installation:

    {"ace_s_link": {"name": "ACE S-band downlink", "live": true, "work": {...}}}

Processes are always launched with an argument list, never a shell string, so
a frequency or pipeline name can never be interpreted as a command
(spec.md section 21).
"""

import json
import logging
import os
import shutil
import subprocess
from dataclasses import dataclass
from pathlib import Path

from aagasa_worker.hardware.errors import SatDumpError

logger = logging.getLogger(__name__)

# Searched in order; a user-installed pipeline overrides the system one.
DEFAULT_PIPELINE_DIRS = (
    Path.home() / ".local/share/satdump/pipelines",
    Path("/usr/share/satdump/pipelines"),
    Path("/usr/local/share/satdump/pipelines"),
)

STOP_GRACE_SECONDS = 10
# Enough output to diagnose a failure without unbounded memory growth.
MAX_CAPTURED_OUTPUT = 64 * 1024


def strip_jsonc_comments(text: str) -> str:
    """Remove // and /* */ comments from a SatDump pipeline file.

    SatDump parses its pipelines with a JSON reader that allows comments, and
    the shipped files use them heavily: about a third of the catalogue on a
    stock install is not strict JSON. Comment markers inside string literals
    are left alone.
    """
    out = []
    index = 0
    length = len(text)
    in_string = False

    while index < length:
        char = text[index]

        if in_string:
            out.append(char)
            if char == "\\" and index + 1 < length:
                # Keep the escaped character so an escaped quote does not
                # look like the end of the string.
                out.append(text[index + 1])
                index += 2
                continue
            if char == '"':
                in_string = False
            index += 1
            continue

        if char == '"':
            in_string = True
            out.append(char)
            index += 1
            continue

        if char == "/" and index + 1 < length:
            following = text[index + 1]
            if following == "/":
                end = text.find("\n", index)
                index = length if end == -1 else end
                continue
            if following == "*":
                end = text.find("*/", index + 2)
                index = length if end == -1 else end + 2
                continue

        out.append(char)
        index += 1

    return "".join(out)


def parse_pipeline_file(text: str) -> dict:
    """Parse a pipeline file, tolerating the comments SatDump allows."""
    try:
        return json.loads(text)
    except json.JSONDecodeError:
        return json.loads(strip_jsonc_comments(text))


@dataclass(frozen=True)
class Pipeline:
    """One SatDump pipeline as offered to the scheduling UI."""

    identifier: str
    name: str
    # Only a live-capable pipeline can decode straight from the SDR.
    supports_live: bool
    source_file: str


@dataclass(frozen=True)
class ProcessResult:
    """Deterministic outcome of a SatDump run."""

    succeeded: bool
    exit_code: int | None
    stdout: str
    stderr: str
    # True when the Worker stopped the process on purpose, as at end of pass.
    terminated: bool = False


def discover_pipelines(pipeline_dirs=None) -> list[Pipeline]:
    """Read the pipelines the installed SatDump offers.

    A malformed file is skipped with a warning rather than failing discovery:
    one bad pipeline must not hide the other seventy.
    """
    directories = [Path(d) for d in (pipeline_dirs or DEFAULT_PIPELINE_DIRS)]
    found: dict[str, Pipeline] = {}

    for directory in directories:
        if not directory.is_dir():
            continue
        for path in sorted(directory.glob("*.json")):
            try:
                definitions = parse_pipeline_file(path.read_text(encoding="utf-8"))
            except (OSError, json.JSONDecodeError) as error:
                logger.warning("skipping unreadable pipeline file %s: %s", path.name, error)
                continue
            if not isinstance(definitions, dict):
                logger.warning("skipping pipeline file %s: not a JSON object", path.name)
                continue

            for identifier, definition in definitions.items():
                if not isinstance(definition, dict):
                    continue
                # First directory wins, so a user override is not replaced.
                if identifier in found:
                    continue
                found[identifier] = Pipeline(
                    identifier=identifier,
                    name=str(definition.get("name", identifier)),
                    supports_live=bool(definition.get("live", False)),
                    source_file=path.name,
                )

    return sorted(found.values(), key=lambda pipeline: pipeline.identifier)


class SatDump:
    """Runs SatDump and reports deterministic results."""

    def __init__(self, binary: str = "satdump", pipeline_dirs=None):
        self._binary = binary
        self._pipeline_dirs = pipeline_dirs

    def check(self) -> str:
        """Confirm SatDump is installed and return its resolved path."""
        resolved = shutil.which(self._binary)
        if resolved is None:
            raise SatDumpError(f"satdump binary {self._binary!r} not found on PATH")
        return resolved

    def pipelines(self) -> list[Pipeline]:
        """List the available pipelines."""
        return discover_pipelines(self._pipeline_dirs)

    def has_pipeline(self, identifier: str) -> bool:
        return any(pipeline.identifier == identifier for pipeline in self.pipelines())

    def build_live_command(self, pipeline: str, output_dir: str, frequency_hz: int,
                           sdr_settings: dict) -> list[str]:
        """Build a live-decode command line.

        Mirrors the invocation shape proven by the reference implementation.
        """
        command = [
            self._binary, "live", pipeline, output_dir,
            "--source", str(sdr_settings.get("source", "rtlsdr")),
            "--frequency", str(int(frequency_hz)),
            "--samplerate", str(int(sdr_settings["samplerate"])),
            "--gain", str(sdr_settings["gain"]),
        ]
        if sdr_settings.get("ppm"):
            command += ["--ppm_correction", str(int(sdr_settings["ppm"]))]
        if sdr_settings.get("bias_tee"):
            command.append("--bias")
        return command

    def build_record_command(self, output_path: str, frequency_hz: int,
                             sdr_settings: dict) -> list[str]:
        """Build a baseband recording command line."""
        command = [
            self._binary, "record", output_path,
            "--source", str(sdr_settings.get("source", "rtlsdr")),
            "--frequency", str(int(frequency_hz)),
            "--samplerate", str(int(sdr_settings["samplerate"])),
            "--gain", str(sdr_settings["gain"]),
            "--baseband_format", str(sdr_settings.get("baseband_format", "ziq")),
        ]
        if sdr_settings.get("ppm"):
            command += ["--ppm_correction", str(int(sdr_settings["ppm"]))]
        if sdr_settings.get("bias_tee"):
            command.append("--bias")
        return command

    def run(self, command: list[str], timeout_seconds: float | None = None) -> ProcessResult:
        """Run SatDump to completion and capture its outcome."""
        try:
            completed = subprocess.run(
                command, capture_output=True, text=True,
                timeout=timeout_seconds, check=False,
            )
        except FileNotFoundError as error:
            raise SatDumpError(f"satdump binary {self._binary!r} not found") from error
        except subprocess.TimeoutExpired as error:
            return ProcessResult(
                succeeded=False, exit_code=None,
                stdout=_trim(error.stdout), stderr=_trim(error.stderr),
                terminated=True,
            )

        return ProcessResult(
            succeeded=completed.returncode == 0,
            exit_code=completed.returncode,
            stdout=_trim(completed.stdout),
            stderr=_trim(completed.stderr),
        )

    def start(self, command: list[str]) -> subprocess.Popen:
        """Start SatDump in the background for a pass."""
        logger.info("starting satdump argv0=%s mode=%s", command[0], command[1] if len(command) > 1 else "")
        try:
            # start_new_session isolates the child so a signal to the Worker
            # does not kill a capture before it can be finalised cleanly.
            return subprocess.Popen(
                command, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                text=True, start_new_session=True,
            )
        except FileNotFoundError as error:
            raise SatDumpError(f"satdump binary {self._binary!r} not found") from error
        except OSError as error:
            raise SatDumpError(f"could not start satdump: {error}") from error

    def stop(self, process: subprocess.Popen, grace_seconds: float = STOP_GRACE_SECONDS) -> ProcessResult:
        """Stop a running SatDump and collect its output.

        Asks politely first so SatDump can finalise its output files, then
        kills it if it will not exit.
        """
        if process.poll() is None:
            process.terminate()
            try:
                stdout, stderr = process.communicate(timeout=grace_seconds)
                return ProcessResult(
                    succeeded=True, exit_code=process.returncode,
                    stdout=_trim(stdout), stderr=_trim(stderr), terminated=True,
                )
            except subprocess.TimeoutExpired:
                logger.warning("satdump did not exit within %ss; killing", grace_seconds)
                process.kill()
                stdout, stderr = process.communicate()
                return ProcessResult(
                    succeeded=False, exit_code=process.returncode,
                    stdout=_trim(stdout), stderr=_trim(stderr), terminated=True,
                )

        stdout, stderr = process.communicate()
        return ProcessResult(
            succeeded=process.returncode == 0, exit_code=process.returncode,
            stdout=_trim(stdout), stderr=_trim(stderr),
        )


def _trim(output) -> str:
    """Bound captured output so a chatty process cannot exhaust memory."""
    if not output:
        return ""
    if isinstance(output, bytes):
        output = output.decode("utf-8", errors="replace")
    if len(output) <= MAX_CAPTURED_OUTPUT:
        return output
    return output[-MAX_CAPTURED_OUTPUT:]


def default_output_dir(base: str, pass_id: str) -> str:
    """Return the output directory for one pass."""
    return os.path.join(base, pass_id)
