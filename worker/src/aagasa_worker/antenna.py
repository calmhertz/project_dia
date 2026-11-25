"""Remembered antenna mode.

The Server decides whether the station has a rotator or a fixed antenna
(monopole, dipole, QFH). The Worker keeps the last decision on disk so a
restart, or a Server outage, does not start driving a rotator that has been
unplugged, or stop driving one that is in use.
"""

import json
import logging
import os
import tempfile
from pathlib import Path

logger = logging.getLogger(__name__)

FILE_NAME = "antenna_mode.json"


class AntennaModeStore:
    """Reads and writes the fixed-antenna flag in the state directory."""

    def __init__(self, state_directory: str | os.PathLike):
        self._path = Path(state_directory) / FILE_NAME

    def load(self) -> bool:
        """Return True for a fixed antenna; a rotator when nothing is stored."""
        try:
            data = json.loads(self._path.read_text())
        except FileNotFoundError:
            return False
        except (OSError, ValueError) as error:
            logger.warning("antenna mode unreadable, assuming a rotator: %s", error)
            return False
        if not isinstance(data, dict):
            return False
        return bool(data.get("fixed_antenna", False))

    def save(self, fixed_antenna: bool) -> None:
        """Persist atomically, so a crash never leaves a half-written file."""
        self._path.parent.mkdir(parents=True, exist_ok=True)
        descriptor, temporary = tempfile.mkstemp(dir=self._path.parent, prefix=".antenna-")
        try:
            with os.fdopen(descriptor, "w") as handle:
                json.dump({"fixed_antenna": bool(fixed_antenna)}, handle)
            os.replace(temporary, self._path)
        except BaseException:
            Path(temporary).unlink(missing_ok=True)
            raise
