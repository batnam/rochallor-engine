"""Tests for the exponential backoff policy (R-009)."""

from __future__ import annotations

import pytest

from workflow_sdk.retry.backoff import BASE_DELAY, FACTOR, JITTER_FRAC, MAX_DELAY, delay


class TestDelayFunction:
    def test_attempt_zero_treated_as_one(self) -> None:
        d = delay(0)
        assert 0 <= d <= MAX_DELAY

    def test_negative_attempt_treated_as_one(self) -> None:
        d = delay(-5)
        assert 0 <= d <= MAX_DELAY

    def test_attempt_one_is_near_base_delay(self) -> None:
        # attempt=1 → d = BASE_DELAY (0.1s), jitter ±20%
        d = delay(1)
        low = BASE_DELAY * (1 - JITTER_FRAC)
        high = BASE_DELAY * (1 + JITTER_FRAC)
        assert low <= d <= high, f"attempt=1 delay {d} out of [{low}, {high}]"

    def test_delay_increases_with_attempts(self) -> None:
        # Run multiple samples to smooth out jitter
        samples = 50
        for attempt in range(2, 8):
            avg_lower = sum(delay(attempt - 1) for _ in range(samples)) / samples
            avg_higher = sum(delay(attempt) for _ in range(samples)) / samples
            assert avg_higher > avg_lower, (
                f"average delay(attempt={attempt}) should be > delay(attempt={attempt - 1})"
            )

    def test_delay_capped_at_max(self) -> None:
        # After enough doublings, delay must be capped at MAX_DELAY
        for attempt in range(20, 30):
            d = delay(attempt)
            assert d <= MAX_DELAY, f"delay(attempt={attempt}) = {d} exceeds MAX_DELAY={MAX_DELAY}"

    def test_jitter_within_bounds(self) -> None:
        for attempt in range(1, 11):
            # Base (un-jittered) value
            base = BASE_DELAY
            for _ in range(1, attempt):
                base = min(base * FACTOR, MAX_DELAY)
            low = base * (1 - JITTER_FRAC)
            high = base * (1 + JITTER_FRAC)
            for _ in range(20):  # multiple samples to check jitter range
                d = delay(attempt)
                assert low <= d <= high or d == MAX_DELAY, (
                    f"attempt={attempt} delay {d} outside jitter range [{low}, {high}]"
                )

    def test_constants(self) -> None:
        assert BASE_DELAY == pytest.approx(0.1)
        assert FACTOR == pytest.approx(2.0)
        assert JITTER_FRAC == pytest.approx(0.20)
        assert MAX_DELAY == pytest.approx(30.0)

    def test_all_attempts_1_to_10_are_non_negative(self) -> None:
        for attempt in range(1, 11):
            assert delay(attempt) >= 0
