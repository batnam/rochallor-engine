package com.batnam.rochallor_engine.workflow_sdk_java.handler;

import java.util.Map;
import java.util.Optional;
import java.util.Set;
import java.util.concurrent.ConcurrentHashMap;

/**
 * Registers {@link JobHandler} implementations by {@code jobType} string.
 *
 * <p>Handlers are keyed strictly by {@code jobType} — never by Java delegate
 * class path (R-010).
 */
public class HandlerRegistry {

    private final Map<String, JobHandler> handlers = new ConcurrentHashMap<>();

    /**
     * Registers {@code handler} for {@code jobType}.
     *
     * @throws IllegalArgumentException if jobType is null or blank
     */
    public void register(String jobType, JobHandler handler) {
        if (jobType == null || jobType.isBlank()) {
            throw new IllegalArgumentException("jobType must not be null or blank");
        }
        handlers.put(jobType, handler);
    }

    /**
     * Returns the handler for {@code jobType}, or empty if none registered.
     */
    public Optional<JobHandler> get(String jobType) {
        return Optional.ofNullable(handlers.get(jobType));
    }

    /**
     * Returns all registered jobType strings.
     */
    public Set<String> jobTypes() {
        return Set.copyOf(handlers.keySet());
    }
}
