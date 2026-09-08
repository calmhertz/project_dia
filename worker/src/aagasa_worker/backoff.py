"""Reconnect backoff.

The Worker must retry a missing Server without tight-looping
(worker-spec section 18).
"""

MIN_DELAY_SECONDS = 5
# Capped at a minute: a station that waited five minutes to notice the Server
# had returned would look broken to an operator who just restarted it.
MAX_DELAY_SECONDS = 60


def next_delay(current: int) -> int:
    """Return the next backoff delay, doubling up to the cap."""
    if current < MIN_DELAY_SECONDS:
        return MIN_DELAY_SECONDS
    return min(current * 2, MAX_DELAY_SECONDS)
