package com.batnam.rochallor_engine.workflow_sdk_java.client;

import com.batnam.workflow.api.v1.CompleteJobRequest;
import com.batnam.workflow.api.v1.FailJobRequest;
import com.batnam.workflow.api.v1.PollJobsRequest;
import com.batnam.workflow.api.v1.WorkflowEngineGrpc;
import com.fasterxml.jackson.core.type.TypeReference;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.google.protobuf.Struct;
import com.google.protobuf.Value;
import com.google.protobuf.util.JsonFormat;
import io.grpc.ManagedChannel;
import io.grpc.ManagedChannelBuilder;

import java.util.List;
import java.util.Map;
import java.util.stream.Collectors;

/**
 * gRPC transport implementation of {@link EngineClient}.
 *
 * <p>Uses the Gradle-generated stubs from {@code proto/workflow/v1/engine.proto}.
 * The Gradle protobuf plugin generates them under {@code build/generated/source/proto}
 * at build time — no manual code generation step is required.
 *
 * <p>Uses insecure (plaintext) credentials suitable for internal/development use.
 * For production TLS, pass a custom {@link ManagedChannel} via the package-private
 * constructor.
 */
public class GrpcEngineClient implements EngineClient {

    private final WorkflowEngineGrpc.WorkflowEngineBlockingStub stub;
    private final ManagedChannel channel;
    private static final ObjectMapper MAPPER = new ObjectMapper();

    /** Creates a client with insecure plaintext credentials. */
    public GrpcEngineClient(String target) {
        this.channel = ManagedChannelBuilder.forTarget(target)
                .usePlaintext()
                .build();
        this.stub = WorkflowEngineGrpc.newBlockingStub(channel);
    }

    /** Package-private constructor for tests that supply a pre-built channel. */
    GrpcEngineClient(ManagedChannel channel) {
        this.channel = channel;
        this.stub = WorkflowEngineGrpc.newBlockingStub(channel);
    }

    @Override
    public List<Job> pollJobs(String workerId, List<String> jobTypes, int maxJobs) throws Exception {
        var req = PollJobsRequest.newBuilder()
                .setWorkerId(workerId)
                .addAllJobTypes(jobTypes)
                .setMaxJobs(maxJobs)
                .build();
        var resp = stub.pollJobs(req);
        return resp.getJobsList().stream()
                .map(this::protoToJob)
                .collect(Collectors.toList());
    }

    @Override
    public void completeJob(String jobId, String workerId, Map<String, Object> variables) throws Exception {
        var reqBuilder = CompleteJobRequest.newBuilder()
                .setJobId(jobId)
                .setWorkerId(workerId);
        if (variables != null && !variables.isEmpty()) {
            reqBuilder.setVariablesToSet(mapToStruct(variables));
        }
        stub.completeJob(reqBuilder.build());
    }

    @Override
    public void failJob(String jobId, String workerId, String errorMessage, boolean retryable) throws Exception {
        stub.failJob(FailJobRequest.newBuilder()
                .setJobId(jobId)
                .setWorkerId(workerId)
                .setErrorMessage(errorMessage != null ? errorMessage : "")
                .setRetryable(retryable)
                .build());
    }

    /** Shuts down the underlying channel. Call when done to release resources. */
    public void shutdown() {
        channel.shutdown();
    }

    // ── Helpers ──────────────────────────────────────────────────────────────

    private Job protoToJob(com.batnam.workflow.api.v1.Job proto) {
        Job job = new Job();
        job.id = proto.getId();
        job.jobType = proto.getJobType();
        job.instanceId = proto.getInstanceId();
        job.stepExecutionId = proto.getStepExecutionId();
        job.retriesRemaining = proto.getRetriesRemaining();
        if (proto.hasVariables()) {
            job.variables = structToMap(proto.getVariables());
        }
        return job;
    }

    @SuppressWarnings("unchecked")
    private Map<String, Object> structToMap(Struct struct) {
        try {
            String json = JsonFormat.printer().print(struct);
            return MAPPER.readValue(json, new TypeReference<Map<String, Object>>() {});
        } catch (Exception e) {
            throw new RuntimeException("Failed to convert Struct to Map", e);
        }
    }

    private Struct mapToStruct(Map<String, Object> map) throws Exception {
        String json = MAPPER.writeValueAsString(map);
        Struct.Builder builder = Struct.newBuilder();
        JsonFormat.parser().merge(json, builder);
        return builder.build();
    }
}
