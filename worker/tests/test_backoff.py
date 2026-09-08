"""Reconnect backoff must grow and stay capped."""

from aagasa_worker.backoff import MAX_DELAY_SECONDS, MIN_DELAY_SECONDS, next_delay


def test_starts_at_minimum():
    assert next_delay(0) == MIN_DELAY_SECONDS


def test_doubles_then_caps():
    delay = MIN_DELAY_SECONDS
    seen = []
    for _ in range(12):
        delay = next_delay(delay)
        seen.append(delay)

    assert seen[0] == MIN_DELAY_SECONDS * 2
    assert max(seen) == MAX_DELAY_SECONDS
    assert next_delay(MAX_DELAY_SECONDS) == MAX_DELAY_SECONDS
