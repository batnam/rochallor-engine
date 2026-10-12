package com.batnam.rochallor_engine.workflow_sdk_java.client;

public class HeartbeatUnsupportedException extends IllegalStateException {
    public HeartbeatUnsupportedException() { super("heartbeat requires an engine/client supporting ExtendJobLease"); }
}
