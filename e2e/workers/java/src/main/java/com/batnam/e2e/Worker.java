package com.batnam.e2e;

import com.batnam.rochallor_engine.workflow_sdk_java.client.EngineClient;
import com.batnam.rochallor_engine.workflow_sdk_java.client.GrpcEngineClient;
import com.batnam.rochallor_engine.workflow_sdk_java.client.RestEngineClient;
import com.batnam.rochallor_engine.workflow_sdk_java.handler.HandlerRegistry;
import com.batnam.rochallor_engine.workflow_sdk_java.runner.KafkaRunner;
import com.batnam.rochallor_engine.workflow_sdk_java.runner.Runner;

import java.util.Map;

public class Worker {

    public static void main(String[] args) throws InterruptedException {
        String engineUrl = env("ENGINE_REST_URL", "http://localhost:8080");
        String grpcHost = env("ENGINE_GRPC_HOST", "localhost:9090");
        String workerTransport = env("WORKER_TRANSPORT", "rest");
        String workerId = env("WORKER_ID", "worker-java-1");

        EngineClient client = "grpc".equals(workerTransport)
                ? new GrpcEngineClient(grpcHost)
                : new RestEngineClient(engineUrl);
        HandlerRegistry registry = new HandlerRegistry();

        // Linear scenario handlers
        registry.register("java-step-a", ctx -> Map.of("stepA", "done"));
        registry.register("java-step-b", ctx -> Map.of("stepB", "done"));
        registry.register("java-step-c", ctx -> Map.of("stepC", "done"));

        // Decision scenario handlers
        registry.register("java-prepare", ctx -> Map.of("result", "approved"));
        registry.register("java-handle-approved", ctx -> Map.of("handled", "approved"));
        registry.register("java-handle-rejected", ctx -> Map.of("handled", "rejected"));

        // Parallel scenario handlers
        registry.register("java-branch-left", ctx -> Map.of("branchLeft", "done"));
        registry.register("java-branch-right", ctx -> Map.of("branchRight", "done"));
        registry.register("java-merge-done", ctx -> Map.of("merged", "done"));

        // User-task scenario handlers
        registry.register("java-before-review", ctx -> Map.of("beforeReview", "done"));
        registry.register("java-after-review", ctx -> Map.of("afterReview", "done"));

        // Timer scenario handlers
        registry.register("java-before-wait", ctx -> Map.of("beforeWait", "done"));
        registry.register("java-timer-fired", ctx -> Map.of("timerFired", "done"));

        // Retry-fail scenario handler: fails on first attempt (retriesRemaining == 2 == retryCount)
        registry.register("java-flaky", ctx -> {
            if (ctx.retriesRemaining() == 2) {
                throw new RuntimeException("simulated transient failure");
            }
            return Map.of("flaky", "done");
        });

        // Signal-user-task scenario handlers
        registry.register("java-signalwaitstep-completeusertask-start", ctx -> Map.of("started", true));
        registry.register("java-signalwaitstep-completeusertask-end", ctx -> Map.of("ended", true));

        // Chaining scenario handlers
        registry.register("java-chain-start", ctx -> Map.of("applicantId", "123", "amount", 100.0));
        registry.register("java-chain-finalize", ctx -> Map.of("finalized", true));

        // Transformation scenario handler
        registry.register("java-transform-init", ctx -> Map.of("firstName", "Alice"));

        // Retry-exhausted scenario handler: always fails to exhaust all retries
        registry.register("java-always-fail", ctx -> { throw new RuntimeException("always fails"); });

        // Decision-no-match scenario handler: sets result to "rejected" so no branch matches
        registry.register("java-prepare-no-match", ctx -> Map.of("result", "rejected"));

        // Parallel-user-task scenario handler
        registry.register("java-put-svc-branch", ctx -> Map.of("svcBranchDone", true));

        // Timer-interrupting scenario handlers
        registry.register("java-slow-task", ctx -> {
            try { Thread.sleep(30_000); } catch (InterruptedException e) { Thread.currentThread().interrupt(); }
            return Map.of();
        });
        registry.register("java-timeout-handler", ctx -> Map.of("timedOut", true));

        // Loan approval scenario handlers
        registry.register("validate-application", ctx -> Map.of("applicationValidated", true));
        registry.register("credit-score", ctx -> Map.of("creditScoreChecked", true));
        registry.register("fraud-screen", ctx -> Map.of("fraudScreened", true));
        registry.register("escalate-review", ctx -> Map.of("reviewEscalated", true));
        registry.register("approve-loan", ctx -> Map.of("loanApproved", true));
        registry.register("notify-approval-overdue", ctx -> Map.of("approvalOverdueNotified", true));
        registry.register("prepare-disbursement", ctx -> Map.of("disbursementPrepared", true));
        registry.register("transfer-funds", ctx -> Map.of("fundsTransferred", true));
        registry.register("notify-disbursement", ctx -> Map.of("disbursementNotified", true));

        String mode = env("WE_DISPATCH_MODE", "polling");
        if ("kafka_outbox".equals(mode)) {
            String brokers = env("WE_KAFKA_SEED_BROKERS", "localhost:9092");
            System.out.println("worker starting (kafka mode): brokers=" + brokers + " workerId=" + workerId);
            KafkaRunner runner = new KafkaRunner(workerId, brokers, client, registry, null);
            runner.start();
        } else {
            System.out.println("worker starting (polling mode): engine=" + engineUrl + " workerId=" + workerId);
            Runner runner = new Runner(workerId, 64, 500, client, registry);
            runner.start();
        }

        // Block the main thread; the runner's daemon thread handles SIGTERM via shutdown hook
        try {
            Thread.currentThread().join();
        } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
        }
        System.out.println("worker stopped");
    }

    private static String env(String key, String defaultValue) {
        String value = System.getenv(key);
        return (value != null && !value.isBlank()) ? value : defaultValue;
    }
}
