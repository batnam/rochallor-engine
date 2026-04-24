package com.batnam.rochallor_engine.workflow_sdk_java.retry;

import java.time.Duration;
import java.util.concurrent.ThreadLocalRandom;

/**
 * R-009 backoff policy: base 100 ms, factor 2.0, jitter ±20%, max 30 s.
 */
public class BackoffPolicy {

    public static final Duration BASE_DELAY  = Duration.ofMillis(100);
    public static final double   FACTOR      = 2.0;
    public static final double   JITTER_FRAC = 0.20;
    public static final Duration MAX_DELAY   = Duration.ofSeconds(30);

    /**
     * Returns the back-off duration for the given attempt (1-based).
     */
    public static Duration delay(int attempt) {
        if (attempt <= 0) attempt = 1;
        double millis = BASE_DELAY.toMillis();
        for (int i = 1; i < attempt; i++) {
            millis *= FACTOR;
            if (millis >= MAX_DELAY.toMillis()) {
                millis = MAX_DELAY.toMillis();
                break;
            }
        }
        // ±20% jitter
        double jitter = millis * JITTER_FRAC * (2 * ThreadLocalRandom.current().nextDouble() - 1);
        long total = Math.max(0L, Math.min(MAX_DELAY.toMillis(), Math.round(millis + jitter)));
        return Duration.ofMillis(total);
    }
}
