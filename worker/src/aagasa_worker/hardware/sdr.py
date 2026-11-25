"""RTL-SDR device discovery.

V1 supports exactly one RTL-SDR and no generic SDR abstraction (spec.md
section 10). Capture itself is performed by SatDump, which opens the device
directly; this module only answers "is the radio present and free?".
"""

import logging
import re
import socket
import subprocess

from aagasa_worker.hardware.errors import SDRError

logger = logging.getLogger(__name__)

PROBE_TIMEOUT_SECONDS = 15

_DEVICE_LINE = re.compile(r"^\s*(\d+):\s+(.+)$")
_NO_DEVICES = "no supported devices found"
_OPEN_ERRORS = ("usb_open error", "failed to open rtlsdr device")


class RTLSDRProbe:
    """Reports whether the configured RTL-SDR is present."""

    def __init__(self, rtl_test_binary: str = "rtl_test", device_index: int = 0):
        self._binary = rtl_test_binary
        self._device_index = device_index

    def list_devices(self) -> list[str]:
        """Return the descriptions of attached RTL-SDR devices.

        An empty list means no radio is attached, which is a normal state to
        report rather than an error.
        """
        try:
            completed = subprocess.run(
                [self._binary, "-t"],
                capture_output=True,
                encoding="utf-8", errors="replace",
                timeout=PROBE_TIMEOUT_SECONDS,
                check=False,
            )
        except FileNotFoundError as error:
            raise SDRError(f"{self._binary} is not installed") from error
        except subprocess.TimeoutExpired as error:
            raise SDRError(f"{self._binary} did not respond") from error

        output = f"{completed.stdout}\n{completed.stderr}"
        if _NO_DEVICES in output.lower():
            return []
        for line in output.splitlines():
            if any(error in line.lower() for error in _OPEN_ERRORS):
                raise SDRError(
                    "RTL-SDR device is present but cannot be opened "
                    f"({line.strip()}); check device permissions and other consumers")

        devices = []
        for line in output.splitlines():
            match = _DEVICE_LINE.match(line)
            if match:
                devices.append(match.group(2).strip())
        return devices

    def check(self) -> str:
        """Confirm the configured device exists and return its description."""
        devices = self.list_devices()
        if not devices:
            raise SDRError("no RTL-SDR device detected")
        if self._device_index >= len(devices):
            raise SDRError(
                f"RTL-SDR index {self._device_index} not present; {len(devices)} device(s) attached"
            )
        description = devices[self._device_index]
        logger.info("rtl-sdr detected index=%d device=%s", self._device_index, description)
        return description


class RTLTCPProbe:
    """Checks the host-side rtl_tcp bridge instead of the local USB device.

    The bridge owns the actual RTL-SDR, so a successful TCP connection means
    the radio is available to SatDump's rtltcp source.
    """

    def __init__(self, host: str = "host.containers.internal", port: int = 1234,
                 timeout_seconds: float = 5.0):
        self._host = host
        self._port = port
        self._timeout_seconds = timeout_seconds

    def check(self) -> str:
        """Confirm the bridge accepts connections and return its address."""
        try:
            with socket.create_connection(
                    (self._host, self._port), timeout=self._timeout_seconds):
                pass
        except OSError as error:
            raise SDRError(
                f"RTL-TCP bridge {self._host}:{self._port} unavailable: {error}") from error
        description = f"RTL-TCP {self._host}:{self._port}"
        logger.info("rtl-sdr detected via %s", description)
        return description
