"""RTL-SDR device discovery.

V1 supports exactly one RTL-SDR and no generic SDR abstraction (spec.md
section 10). Capture itself is performed by SatDump, which opens the device
directly; this module only answers "is the radio present and free?".
"""

import logging
import re
import subprocess

from aagasa_worker.hardware.errors import SDRError

logger = logging.getLogger(__name__)

PROBE_TIMEOUT_SECONDS = 15

# rtl_test lists devices on stderr as "  0:  Realtek, RTL2838UHIDIR, SN: ...".
_DEVICE_LINE = re.compile(r"^\s*(\d+):\s+(.+)$")
_NO_DEVICES = "no supported devices found"


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
                text=True,
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
