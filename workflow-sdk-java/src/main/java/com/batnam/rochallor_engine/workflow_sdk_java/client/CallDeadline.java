package com.batnam.rochallor_engine.workflow_sdk_java.client;

import java.time.Duration;

/** Per-thread transport budget; custom EngineClient method signatures stay unchanged. */
public final class CallDeadline implements AutoCloseable {
    private static final ThreadLocal<Duration> CURRENT = new ThreadLocal<>();
    private final Duration previous;

    private CallDeadline(Duration timeout) {
        previous = CURRENT.get();
        CURRENT.set(timeout);
    }

    public static CallDeadline within(Duration timeout) { return new CallDeadline(timeout); }

    public static Duration timeout() {
        Duration timeout = CURRENT.get();
        return timeout == null ? Duration.ofSeconds(30) : timeout;
    }

    @Override public void close() {
        if (previous == null) CURRENT.remove();
        else CURRENT.set(previous);
    }
}
