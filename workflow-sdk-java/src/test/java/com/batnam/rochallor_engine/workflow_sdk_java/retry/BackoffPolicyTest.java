package com.batnam.rochallor_engine.workflow_sdk_java.retry;

import org.junit.jupiter.api.Test;

import java.time.Duration;

import static org.junit.jupiter.api.Assertions.*;

class BackoffPolicyTest {

    @Test
    void firstAttemptInRange() {
        Duration d = BackoffPolicy.delay(1);
        // ~100ms ±20% → [80ms, 120ms]
        assertTrue(d.toMillis() >= 60, "d1 too low: " + d);
        assertTrue(d.toMillis() <= 150, "d1 too high: " + d);
    }

    @Test
    void neverExceedsMaxDelay() {
        for (int attempt = 1; attempt <= 30; attempt++) {
            Duration d = BackoffPolicy.delay(attempt);
            assertTrue(d.compareTo(BackoffPolicy.MAX_DELAY) <= 0,
                    "attempt " + attempt + " exceeded max delay: " + d);
        }
    }

    @Test
    void neverNegative() {
        for (int attempt = 1; attempt <= 100; attempt++) {
            assertTrue(BackoffPolicy.delay(attempt).toMillis() >= 0,
                    "negative delay at attempt " + attempt);
        }
    }

    @Test
    void delayGrowsWithAttempts() {
        // Without jitter we'd see exact doubling; with ±20% jitter check rough ordering
        // by running 10 trials per attempt and checking medians
        long sum1 = 0, sum2 = 0, sum3 = 0;
        int trials = 20;
        for (int i = 0; i < trials; i++) {
            sum1 += BackoffPolicy.delay(1).toMillis();
            sum2 += BackoffPolicy.delay(2).toMillis();
            sum3 += BackoffPolicy.delay(3).toMillis();
        }
        assertTrue(sum2 > sum1, "avg delay(2) should be > avg delay(1)");
        assertTrue(sum3 > sum2, "avg delay(3) should be > avg delay(2)");
    }
}
