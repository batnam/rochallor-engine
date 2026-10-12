package com.batnam.rochallor_engine.workflow_sdk_java.client;

/** HTTP rejection status retained for callback retry classification. */
public final class HttpError extends RuntimeException {
    public final int statusCode;
    public HttpError(String message, int statusCode) {
        super(message);
        this.statusCode = statusCode;
    }
}
