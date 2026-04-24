package com.batnam.rochallor_engine.workflow_sdk_java.handler;

/**
 * Thrown by a {@link JobHandler} to signal that the failure is terminal and
 * the retry budget should not be consulted. The Runner calls FailJob with
 * {@code retryable=false}.
 */
public class NonRetryableException extends RuntimeException {

    public NonRetryableException(String message) {
        super(message);
    }

    public NonRetryableException(String message, Throwable cause) {
        super(message, cause);
    }
}
