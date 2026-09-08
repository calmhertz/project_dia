"""Local durable storage layout for the Worker.

V1 creates and validates the directories. The durable PassPlan store is built
in V9; this module only guarantees the Worker has writable local state before
it does anything else.
"""

import logging
import os
from pathlib import Path

logger = logging.getLogger(__name__)

# Local state may contain the Worker credential's effects and pass data, so it
# is not world-readable.
STATE_DIR_MODE = 0o700


def prepare_directories(*directories: Path) -> None:
    """Create each directory if needed and verify it is writable."""
    for directory in directories:
        directory.mkdir(parents=True, exist_ok=True, mode=STATE_DIR_MODE)
        if not os.access(directory, os.W_OK):
            raise PermissionError(f"directory not writable: {directory}")
        logger.debug("directory ready path=%s", directory)
