package com.batnam.rochallor_engine.workflow_sdk_java.client;

import java.time.Instant;

/** Optional capability, separate from the required EngineClient methods. */
public interface LeaseExtender {
    Instant extendJobLease(String jobId, String workerId, int leaseSeconds) throws Exception;
}
