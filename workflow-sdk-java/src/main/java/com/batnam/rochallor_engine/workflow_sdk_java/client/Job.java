package com.batnam.rochallor_engine.workflow_sdk_java.client;

import com.fasterxml.jackson.annotation.JsonIgnoreProperties;
import com.fasterxml.jackson.annotation.JsonProperty;
import java.util.Map;

/** A job returned by {@link EngineClient#pollJobs}. */
@JsonIgnoreProperties(ignoreUnknown = true)
public class Job {

    @JsonProperty("id")
    public String id;

    @JsonProperty("jobType")
    public String jobType;

    @JsonProperty("instanceId")
    public String instanceId;

    @JsonProperty("stepExecutionId")
    public String stepExecutionId;

    @JsonProperty("retriesRemaining")
    public int retriesRemaining;

    @JsonProperty("variables")
    public Map<String, Object> variables;
}
