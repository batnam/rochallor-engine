package com.batnam.rochallor_engine.workflow_sdk_java.client;

public class LeaseLostException extends IllegalStateException {
    public LeaseLostException() { super("job lease lost"); }
}
