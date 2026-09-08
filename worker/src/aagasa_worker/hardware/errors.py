"""Hardware failure types.

Hardware errors are explicit and typed so callers can decide between retrying,
parking the rotator and failing the pass. Nothing is swallowed into a log line
(RULES.md sections 13 and 15).
"""


class HardwareError(Exception):
    """Base class for ground-station hardware failures."""


class RotatorError(HardwareError):
    """The rotator could not be commanded or read."""


class SDRError(HardwareError):
    """The RTL-SDR is unavailable or already in use."""


class SatDumpError(HardwareError):
    """SatDump is unavailable, misconfigured, or failed to run."""
