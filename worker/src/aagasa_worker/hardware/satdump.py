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

DEFAULT_PIPELINE_DIRS = (
    Path.home() / ".local/share/satdump/pipelines",
    Path("/usr/share/satdump/pipelines"),
    Path("/usr/share/satdump/resources/pipelines"),
    Path("/usr/local/share/satdump/pipelines"),
)

STOP_GRACE_SECONDS = 10
MAX_CAPTURED_OUTPUT = 64 * 1024

LOG_FILE_NAME = "satdump.log"
LOG_TAIL_BYTES = 2 * 1024


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


def _primary_frequency_hz(definition: dict) -> int | None:
    """Return the first declared frequency of a pipeline, in Hz.

    SatDump pipelines declare a list such as::

        "frequencies": [["Primary", 137.9e6], ["Backup", 137.1e6]]

    The first usable entry is the one SatDump's own GUI selects by default.
    """
    frequencies = definition.get("frequencies")
    if not isinstance(frequencies, list):
        return None

    for entry in frequencies:
        value = entry[1] if isinstance(entry, (list, tuple)) and len(entry) > 1 else entry
        if isinstance(value, bool) or value is None:
            continue
        try:
            frequency_hz = int(float(value))
        except (TypeError, ValueError):
            continue
        if frequency_hz > 0:
            return frequency_hz
    return None


@dataclass(frozen=True)
class Pipeline:
    """One SatDump pipeline as offered to the scheduling UI."""

    identifier: str
    name: str
    supports_live: bool
    source_file: str
    primary_frequency_hz: int | None = None


@dataclass(frozen=True)
class ProcessResult:
    """Deterministic outcome of a SatDump run."""

    succeeded: bool
    exit_code: int | None
    stdout: str
    stderr: str
    terminated: bool = False
    log_tail: str = ""


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
                if identifier in found:
                    continue
                found[identifier] = Pipeline(
                    identifier=identifier,
                    name=str(definition.get("name", identifier)),
                    supports_live=bool(definition.get("live", False)),
                    source_file=path.name,
                    primary_frequency_hz=_primary_frequency_hz(definition),
                )

    return sorted(found.values(), key=lambda pipeline: pipeline.identifier)


class SatDump:
    """Runs SatDump and reports deterministic results."""

    def __init__(self, binary: str = "satdump", pipeline_dirs=None,
                 legacy_cli: bool = False, sdr_source: str = "rtlsdr",
                 rtltcp_host: str = "host.containers.internal",
                 rtltcp_port: int = 1234):
        self._binary = binary
        self._pipeline_dirs = pipeline_dirs
        self._legacy_cli = legacy_cli
        self._sdr_source = sdr_source.lower()
        self._rtltcp_host = rtltcp_host
        self._rtltcp_port = rtltcp_port

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

    def _declared_frequency(self, identifier: str) -> int | None:
        for pipeline in self.pipelines():
            if pipeline.identifier == identifier:
                return pipeline.primary_frequency_hz
        return None

    def _source(self, sdr_settings: dict) -> str:
        if self._sdr_source in ("rtltcp", "rtl_tcp"):
            return "rtltcp"
        return str(sdr_settings.get("source", self._sdr_source))

    def _source_args(self, sdr_settings: dict) -> list[str]:
        if self._sdr_source not in ("rtltcp", "rtl_tcp"):
            return []
        return [
            "--ip_address", str(sdr_settings.get("ip_address", self._rtltcp_host)),
            "--port", str(int(sdr_settings.get("port", self._rtltcp_port))),
        ]

    def build_live_command(self, pipeline: str, output_dir: str, frequency_hz: int,
                           sdr_settings: dict) -> list[str]:
        """Build a live-decode command line.

        Mirrors the invocation shape proven by the reference implementation.
        A pipeline's own Primary frequency wins over the one in the plan: the
        plan only knows the station band centre, while the pipeline definition
        knows which downlink the decoder expects.
        """
        requested_hz = int(frequency_hz)
        declared_hz = self._declared_frequency(pipeline)
        if declared_hz is not None:
            if declared_hz != requested_hz:
                logger.info("pipeline %s uses declared frequency %s Hz "
                            "instead of plan frequency %s Hz",
                            pipeline, declared_hz, requested_hz)
            frequency_hz = declared_hz

        command = [self._binary]
        if self._legacy_cli:
            command.append("legacy")
        command += [
            "live", pipeline, output_dir,
            "--source", self._source(sdr_settings),
            "--frequency", str(int(frequency_hz)),
            "--samplerate", str(int(sdr_settings["samplerate"])),
            "--gain", str(sdr_settings["gain"]),
        ]
        command += self._source_args(sdr_settings)
        if sdr_settings.get("ppm"):
            command += ["--ppm_correction", str(int(sdr_settings["ppm"]))]
        if sdr_settings.get("bias_tee"):
            command.append("--bias")
        return command

    def build_record_command(self, output_path: str, frequency_hz: int,
                             sdr_settings: dict) -> list[str]:
        """Build a baseband recording command line."""
        command = [self._binary]
        if self._legacy_cli:
            command.append("legacy")
        command += [
            "record", output_path,
            "--source", self._source(sdr_settings),
            "--frequency", str(int(frequency_hz)),
            "--samplerate", str(int(sdr_settings["samplerate"])),
            "--gain", str(sdr_settings["gain"]),
            "--baseband_format", str(sdr_settings.get("baseband_format", "ziq")),
        ]
        command += self._source_args(sdr_settings)
        if sdr_settings.get("ppm"):
            command += ["--ppm_correction", str(int(sdr_settings["ppm"]))]
        if sdr_settings.get("bias_tee"):
            command.append("--bias")
        return command

    def run(self, command: list[str], timeout_seconds: float | None = None) -> ProcessResult:
        """Run SatDump to completion and capture its outcome."""
        try:
            completed = subprocess.run(
                command, capture_output=True,
                encoding="utf-8", errors="replace",
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

    def start(self, command: list[str], log_path: str | os.PathLike | None = None) -> subprocess.Popen:
        """Start SatDump in the background for a pass.

        Output goes to a log file, never to a pipe. Nothing reads a pipe while
        a pass runs, so a chatty pipeline would fill the ~64 KiB buffer, block
        on its next write and stall the capture. Without a log path the output
        is discarded for the same reason.
        """
        logger.info("starting satdump argv0=%s mode=%s log=%s", command[0],
                    command[1] if len(command) > 1 else "", log_path or "<discarded>")
        log_handle = None
        try:
            if log_path is not None:
                Path(log_path).parent.mkdir(parents=True, exist_ok=True)
                log_handle = open(log_path, "ab")  # noqa: SIM115 - closed below
                output = log_handle
            else:
                output = subprocess.DEVNULL
            return subprocess.Popen(
                command, stdin=subprocess.DEVNULL, stdout=output,
                stderr=subprocess.STDOUT, start_new_session=True,
            )
        except FileNotFoundError as error:
            raise SatDumpError(f"satdump binary {self._binary!r} not found") from error
        except OSError as error:
            raise SatDumpError(f"could not start satdump: {error}") from error
        finally:
            # The child holds its own copy of the descriptor; the Worker's
            # copy is not needed once the process exists (or failed to).
            if log_handle is not None:
                log_handle.close()

    def stop(self, process: subprocess.Popen, grace_seconds: float = STOP_GRACE_SECONDS,
             log_path: str | os.PathLike | None = None) -> ProcessResult:
        """Stop a running SatDump and report its outcome.

        Asks politely first so SatDump can finalise its output files, then
        kills it if it will not exit. The result carries the end of the log,
        which is where SatDump explains a failure.
        """
        if process.poll() is None:
            process.terminate()
            try:
                process.wait(timeout=grace_seconds)
                return ProcessResult(
                    succeeded=True, exit_code=process.returncode,
                    stdout="", stderr="", terminated=True,
                    log_tail=read_log_tail(log_path),
                )
            except subprocess.TimeoutExpired:
                logger.warning("satdump did not exit within %ss; killing", grace_seconds)
                process.kill()
                process.wait()
                return ProcessResult(
                    succeeded=False, exit_code=process.returncode,
                    stdout="", stderr="", terminated=True,
                    log_tail=read_log_tail(log_path),
                )

        process.wait()
        return ProcessResult(
            succeeded=process.returncode == 0, exit_code=process.returncode,
            stdout="", stderr="", log_tail=read_log_tail(log_path),
        )


def read_log_tail(log_path, limit: int = LOG_TAIL_BYTES) -> str:
    """The last few KiB of a SatDump log, or "" when there is none."""
    if log_path is None:
        return ""
    try:
        with open(log_path, "rb") as handle:
            handle.seek(0, os.SEEK_END)
            size = handle.tell()
            handle.seek(max(0, size - limit))
            data = handle.read()
    except OSError:
        return ""
    return data.decode("utf-8", errors="replace").strip()


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
