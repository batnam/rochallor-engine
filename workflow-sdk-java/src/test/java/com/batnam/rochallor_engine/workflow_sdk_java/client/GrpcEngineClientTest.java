package com.batnam.rochallor_engine.workflow_sdk_java.client;

import com.batnam.workflow.api.v1.CompleteJobRequest;
import com.batnam.workflow.api.v1.FailJobRequest;
import com.batnam.workflow.api.v1.PollJobsRequest;
import com.batnam.workflow.api.v1.PollJobsResponse;
import com.batnam.workflow.api.v1.WorkflowEngineGrpc;
import com.google.protobuf.Struct;
import com.google.protobuf.Value;
import io.grpc.ManagedChannel;
import io.grpc.Server;
import io.grpc.inprocess.InProcessChannelBuilder;
import io.grpc.inprocess.InProcessServerBuilder;
import io.grpc.stub.StreamObserver;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import java.util.List;
import java.util.Map;
import java.util.concurrent.atomic.AtomicReference;

import static org.assertj.core.api.Assertions.assertThat;

class GrpcEngineClientTest {

    private Server server;
    private ManagedChannel channel;
    private GrpcEngineClient client;

    // Captured requests from test server
    private final AtomicReference<CompleteJobRequest> capturedComplete = new AtomicReference<>();
    private final AtomicReference<FailJobRequest> capturedFail = new AtomicReference<>();

    // Jobs returned by test server's PollJobs
    private List<com.batnam.workflow.api.v1.Job> jobsToReturn = List.of();

    @BeforeEach
    void setUp() throws Exception {
        String serverName = InProcessServerBuilder.generateName();

        server = InProcessServerBuilder.forName(serverName)
                .directExecutor()
                .addService(new WorkflowEngineGrpc.WorkflowEngineImplBase() {
                    @Override
                    public void pollJobs(PollJobsRequest req,
                                        StreamObserver<PollJobsResponse> resp) {
                        resp.onNext(PollJobsResponse.newBuilder()
                                .addAllJobs(jobsToReturn)
                                .build());
                        resp.onCompleted();
                    }

                    @Override
                    public void completeJob(CompleteJobRequest req,
                                            StreamObserver<com.batnam.workflow.api.v1.CompleteJobResponse> resp) {
                        capturedComplete.set(req);
                        resp.onNext(com.batnam.workflow.api.v1.CompleteJobResponse.newBuilder().build());
                        resp.onCompleted();
                    }

                    @Override
                    public void failJob(FailJobRequest req,
                                        StreamObserver<com.batnam.workflow.api.v1.FailJobResponse> resp) {
                        capturedFail.set(req);
                        resp.onNext(com.batnam.workflow.api.v1.FailJobResponse.newBuilder().build());
                        resp.onCompleted();
                    }
                })
                .build()
                .start();

        channel = InProcessChannelBuilder.forName(serverName)
                .directExecutor()
                .build();

        client = new GrpcEngineClient(channel);
    }

    @AfterEach
    void tearDown() throws Exception {
        channel.shutdown();
        server.shutdown();
    }

    @Test
    void testPollJobsHappyPath() throws Exception {
        jobsToReturn = List.of(
                com.batnam.workflow.api.v1.Job.newBuilder()
                        .setId("j1")
                        .setJobType("my-job")
                        .setInstanceId("i1")
                        .setRetriesRemaining(2)
                        .build()
        );

        List<Job> jobs = client.pollJobs("w1", List.of("my-job"), 1);

        assertThat(jobs).hasSize(1);
        assertThat(jobs.get(0).id).isEqualTo("j1");
        assertThat(jobs.get(0).jobType).isEqualTo("my-job");
        assertThat(jobs.get(0).instanceId).isEqualTo("i1");
        assertThat(jobs.get(0).retriesRemaining).isEqualTo(2);
    }

    @Test
    void testPollJobsWithVariables() throws Exception {
        Struct vars = Struct.newBuilder()
                .putFields("loanId", Value.newBuilder().setStringValue("L-001").build())
                .build();
        jobsToReturn = List.of(
                com.batnam.workflow.api.v1.Job.newBuilder()
                        .setId("j2")
                        .setJobType("loan-check")
                        .setVariables(vars)
                        .build()
        );

        List<Job> jobs = client.pollJobs("w1", List.of("loan-check"), 1);

        assertThat(jobs).hasSize(1);
        assertThat(jobs.get(0).variables).isNotNull();
        assertThat(jobs.get(0).variables.get("loanId")).isEqualTo("L-001");
    }

    @Test
    void testCompleteJob() throws Exception {
        client.completeJob("j1", "w1", Map.of("result", "ok"));

        CompleteJobRequest req = capturedComplete.get();
        assertThat(req).isNotNull();
        assertThat(req.getJobId()).isEqualTo("j1");
        assertThat(req.getWorkerId()).isEqualTo("w1");
    }

    @Test
    void testFailJob() throws Exception {
        client.failJob("j1", "w1", "boom", true);

        FailJobRequest req = capturedFail.get();
        assertThat(req).isNotNull();
        assertThat(req.getJobId()).isEqualTo("j1");
        assertThat(req.getRetryable()).isTrue();
        assertThat(req.getErrorMessage()).isEqualTo("boom");
    }

    @Test
    void testPollJobsEmptyList() throws Exception {
        jobsToReturn = List.of();
        List<Job> jobs = client.pollJobs("w1", List.of("no-such-type"), 1);
        assertThat(jobs).isEmpty();
    }
}
