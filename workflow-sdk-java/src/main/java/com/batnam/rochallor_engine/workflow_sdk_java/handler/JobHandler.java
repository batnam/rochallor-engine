package com.batnam.rochallor_engine.workflow_sdk_java.handler;

import java.util.Map;

/**
 * Functional interface for job handlers. Implementations must not throw
 * unchecked exceptions they don't want to be treated as retryable failures —
 * the Runner catches all Throwables and routes them to FailJob.
 */
@FunctionalInterface
public interface JobHandler {

    /**
     * Executes the job described by {@code context}.
     *
     * @return a (possibly empty) map of variables to merge into the instance on completion
     * @throws NonRetryableException to signal a terminal failure that bypasses the retry budget
     * @throws Exception             for any other retryable failure
     */
    Map<String, Object> handle(JobContext context) throws Exception;
}
