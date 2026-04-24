"""Exponential backoff retry policy — R-009.

Policy: base 100 ms, factor 2.0, jitter ±20%, max delay 30 s.
The attempt budget comes from the job's retriesRemaining field.
Returning NonRetryableError from a handler bypasses all retry logic.
"""

from __future__ import annotations

import random

# Policy constants (R-009)
BASE_DELAY: float = 0.1   # 100 ms
FACTOR: float = 2.0
JITTER_FRAC: float = 0.20  # ±20%
MAX_DELAY: float = 30.0    # 30 seconds


def delay(attempt: int) -> float:
    """Calculate the back-off delay in seconds for the given attempt number.

    Args:
        attempt: 1-based attempt number. Values ≤ 0 are treated as 1.

    Returns:
        Duration in seconds (already includes jitter, capped at MAX_DELAY).
    """
    if attempt <= 0:
        attempt = 1

    d = BASE_DELAY
    for _ in range(1, attempt):
        d *= FACTOR
        if d >= MAX_DELAY:
            d = MAX_DELAY
            break

    # Apply ±JITTER_FRAC jitter
    jitter = d * JITTER_FRAC * (2 * random.random() - 1)
    total = d + jitter

    # Clamp to [0, MAX_DELAY]
    return max(0.0, min(total, MAX_DELAY))
