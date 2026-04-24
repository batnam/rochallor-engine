package com.batnam.rochallor_engine.workflow_sdk_java.handler;

import com.batnam.rochallor_engine.workflow_sdk_java.client.Job;
import java.util.Map;
import java.util.Optional;

/** Gives handler implementations typed access to a Job's fields and variables. */
public class JobContext {

    private final Job job;

    public JobContext(Job job) {
        this.job = job;
    }

    public String jobId()             { return job.id; }
    public String instanceId()        { return job.instanceId; }
    public String jobType()           { return job.jobType; }
    public int    retriesRemaining()  { return job.retriesRemaining; }

    public Optional<Object> get(String key) {
        if (job.variables == null) return Optional.empty();
        return Optional.ofNullable(job.variables.get(key));
    }

    public Map<String, Object> variables() {
        return job.variables != null ? Map.copyOf(job.variables) : Map.of();
    }
}
