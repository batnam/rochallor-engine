package com.batnam.rochallor_engine.workflow_sdk_java.client;

import java.util.List;
import java.util.Map;

/**
 * Contract for Engine connectivity. Both {@link RestEngineClient} and
 * {@link GrpcEngineClient} implement this interface so the Runner is
 * transport-agnostic.
 *
 * <p>The only job identification mechanism is the {@code jobType} string (R-010).
 */
public interface EngineClient {

    /**
     * Claims up to {@code maxJobs} jobs of the given types.
     */
    List<Job> pollJobs(String workerId, List<String> jobTypes, int maxJobs) throws Exception;

    /**
     * Marks a job completed and merges {@code variables} into the instance.
     */
    void completeJob(String jobId, String workerId, Map<String, Object> variables) throws Exception;

    /**
     * Records a job failure. {@code retryable=false} bypasses the retry budget.
     */
    void failJob(String jobId, String workerId, String errorMessage, boolean retryable) throws Exception;
}
