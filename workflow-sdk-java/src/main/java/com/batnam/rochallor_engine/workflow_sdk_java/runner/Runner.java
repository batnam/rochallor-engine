package com.batnam.rochallor_engine.workflow_sdk_java.runner;

import com.batnam.rochallor_engine.workflow_sdk_java.client.EngineClient;
import com.batnam.rochallor_engine.workflow_sdk_java.client.Job;
import com.batnam.rochallor_engine.workflow_sdk_java.handler.HandlerRegistry;
import com.batnam.rochallor_engine.workflow_sdk_java.handler.JobContext;
import com.batnam.rochallor_engine.workflow_sdk_java.handler.JobHandler;
import com.batnam.rochallor_engine.workflow_sdk_java.handler.NonRetryableException;

import java.util.List;
import java.util.Map;
import java.util.Optional;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.TimeUnit;
import java.util.logging.Logger;

/**
 * Poll/lock/dispatch loop for the Java SDK.
 *
 * <p>Creates a bounded thread pool, polls the Engine, and dispatches each job
 * to its registered handler. Shuts down gracefully on {@link #stop()}.
 */
public class Runner {

    private static final Logger LOG = Logger.getLogger(Runner.class.getName());

    private final String workerId;
    private final int parallelism;
    private final long pollIntervalMs;
    private final EngineClient engine;
    private final HandlerRegistry registry;
    private final ExecutorService executor;

    private volatile boolean running = false;

    public Runner(String workerId, int parallelism, long pollIntervalMs,
                  EngineClient engine, HandlerRegistry registry) {
        this.workerId = workerId;
        this.parallelism = parallelism > 0 ? parallelism : 64;
        this.pollIntervalMs = pollIntervalMs > 0 ? pollIntervalMs : 500;
        this.engine = engine;
        this.registry = registry;
        this.executor = Executors.newVirtualThreadPerTaskExecutor();
    }

    /** Starts the poll loop in a background thread. */
    public void start() {
        running = true;
        Thread thread = new Thread(this::pollLoop, "workflow-runner-" + workerId);
        thread.setDaemon(true);
        thread.start();
        Runtime.getRuntime().addShutdownHook(new Thread(() -> stop(30)));
    }

    /** Signals the runner to stop and waits up to {@code timeoutSeconds}. */
    public void stop(int timeoutSeconds) {
        LOG.info("Runner stopping: workerId=" + workerId);
        running = false;
        executor.shutdown();
        try {
            if (!executor.awaitTermination(timeoutSeconds, TimeUnit.SECONDS)) {
                LOG.warning("Runner did not terminate gracefully, forcing shutdown");
                executor.shutdownNow();
            }
        } catch (InterruptedException e) {
            executor.shutdownNow();
            Thread.currentThread().interrupt();
        }
        LOG.info("Runner stopped: workerId=" + workerId);
    }

    private void pollLoop() {
        List<String> jobTypes = List.copyOf(registry.jobTypes());
        LOG.info("Runner started: workerId=" + workerId + " jobTypes=" + jobTypes + " parallelism=" + parallelism);
        while (running) {
            try {
                List<Job> jobs = engine.pollJobs(workerId, jobTypes, parallelism);
                if (jobs == null || jobs.isEmpty()) {
                    Thread.sleep(pollIntervalMs);
                    continue;
                }
                for (Job job : jobs) {
                    executor.submit(() -> dispatch(job));
                }
            } catch (InterruptedException e) {
                Thread.currentThread().interrupt();
                break;
            } catch (Exception e) {
                LOG.warning("runner: poll error — " + e.getMessage());
                try { Thread.sleep(pollIntervalMs); } catch (InterruptedException ie) { break; }
            }
        }
    }

    private void dispatch(Job job) {
        Optional<JobHandler> handlerOpt = registry.get(job.jobType);
        if (handlerOpt.isEmpty()) {
            LOG.warning("runner: no handler for jobType=" + job.jobType + " jobId=" + job.id);
            try {
                engine.failJob(job.id, workerId, "no handler registered for " + job.jobType, false);
            } catch (Exception e) {
                LOG.warning("runner: FailJob failed for " + job.id + ": " + e.getMessage());
            }
            return;
        }

        LOG.info("runner: dispatching job_id=" + job.id + " job_type=" + job.jobType);
        try {
            JobContext ctx = new JobContext(job);
            Map<String, Object> result = handlerOpt.get().handle(ctx);
            LOG.info("runner: job completed job_id=" + job.id);
            engine.completeJob(job.id, workerId, result);
        } catch (NonRetryableException e) {
            LOG.warning("runner: job failed (non-retryable) job_id=" + job.id + ": " + e.getMessage());
            try { engine.failJob(job.id, workerId, e.getMessage(), false); }
            catch (Exception ex) { LOG.warning("runner: failJob failed: " + ex.getMessage()); }
        } catch (Exception e) {
            LOG.warning("runner: job failed (retryable) job_id=" + job.id + ": " + e.getMessage());
            try { engine.failJob(job.id, workerId, e.getMessage(), true); }
            catch (Exception ex) { LOG.warning("runner: failJob failed: " + ex.getMessage()); }
        }
    }
}
