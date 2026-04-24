package com.batnam.rochallor_engine.workflow_sdk_java.runner;

import java.time.Duration;
import java.time.Instant;
import java.util.Map;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.Executors;
import java.util.concurrent.ScheduledExecutorService;
import java.util.concurrent.TimeUnit;

/**
 * DeduplicationCache tracks seen dedup_id values with a TTL to prevent duplicate
 * job execution within a short window.
 */
public class DeduplicationCache implements AutoCloseable {
    private final Map<String, Instant> cache = new ConcurrentHashMap<>();
    private final Duration window;
    private final ScheduledExecutorService scheduler;

    public DeduplicationCache(Duration window) {
        this.window = window;
        this.scheduler = Executors.newSingleThreadScheduledExecutor(r -> {
            Thread t = new Thread(r, "dedup-cache-sweeper");
            t.setDaemon(true);
            return t;
        });
        
        long sweepIntervalSeconds = Math.max(1, window.toSeconds() / 4);
        this.scheduler.scheduleAtFixedRate(this::sweep, sweepIntervalSeconds, sweepIntervalSeconds, TimeUnit.SECONDS);
    }

    /**
     * seenRecently returns true if the id was seen within the window, and
     * records the current observation if not seen or expired.
     */
    public boolean seenRecently(String id) {
        if (id == null || id.isEmpty()) {
            return false;
        }
        
        Instant now = Instant.now();
        Instant seenAt = cache.get(id);
        
        if (seenAt != null && Duration.between(seenAt, now).compareTo(window) < 0) {
            return true;
        }
        
        cache.put(id, now);
        return false;
    }

    private void sweep() {
        Instant cutoff = Instant.now().minus(window);
        cache.entrySet().removeIf(entry -> entry.getValue().isBefore(cutoff));
    }

    @Override
    public void close() {
        scheduler.shutdown();
        try {
            if (!scheduler.awaitTermination(1, TimeUnit.SECONDS)) {
                scheduler.shutdownNow();
            }
        } catch (InterruptedException e) {
            scheduler.shutdownNow();
            Thread.currentThread().interrupt();
        }
        cache.clear();
    }
}
