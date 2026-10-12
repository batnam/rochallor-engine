package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/batnam/rochallor-engine/workflow-sdk-go/client"
	"github.com/batnam/rochallor-engine/workflow-sdk-go/handler"
	"github.com/batnam/rochallor-engine/workflow-sdk-go/kafkarunner"
	"github.com/batnam/rochallor-engine/workflow-sdk-go/runner"
)

func main() {
	engineURL := envOrDefault("ENGINE_REST_URL", "http://localhost:8080")
	grpcHost := envOrDefault("ENGINE_GRPC_HOST", "localhost:9090")
	workerTransport := envOrDefault("WORKER_TRANSPORT", "rest")
	workerID := envOrDefault("WORKER_ID", "worker-go-1")
	logLevel := envOrDefault("WE_LOG_LEVEL", "info")

	var level slog.Level
	switch logLevel {
	case "debug":
		level = slog.LevelDebug
	case "info":
		level = slog.LevelInfo
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})))

	registry := handler.New()
	var recoveryMu sync.Mutex
	recoveryCalls := map[string]int{}
	for _, jobType := range []string{"go-sdk-recovery", "go-sdk-failure"} {
		registry.Register(jobType, func(_ context.Context, job handler.JobContext) (handler.Result, error) {
			recoveryMu.Lock()
			recoveryCalls[job.InstanceID]++
			calls := recoveryCalls[job.InstanceID]
			recoveryMu.Unlock()
			if job.JobType == "go-sdk-failure" && job.RetriesRemaining == 2 {
				return handler.Result{}, fmt.Errorf("sdkRecovery business failure: %s", job.InstanceID)
			}
			return handler.Result{VariablesToSet: map[string]any{
				"sdkRecovery": true, "instanceId": job.InstanceID,
				"handlerCalls": calls, "attemptId": job.JobID, "remaining": job.RetriesRemaining,
			}}, nil
		})
	}

	// Linear scenario handlers
	registry.Register("go-step-a", func(_ context.Context, _ handler.JobContext) (handler.Result, error) {
		return handler.Result{VariablesToSet: map[string]any{"stepA": "done"}}, nil
	})
	registry.Register("go-step-b", func(_ context.Context, _ handler.JobContext) (handler.Result, error) {
		return handler.Result{VariablesToSet: map[string]any{"stepB": "done"}}, nil
	})
	registry.Register("go-step-c", func(_ context.Context, _ handler.JobContext) (handler.Result, error) {
		return handler.Result{VariablesToSet: map[string]any{"stepC": "done"}}, nil
	})

	// Decision scenario handlers
	registry.Register("go-prepare", func(_ context.Context, _ handler.JobContext) (handler.Result, error) {
		return handler.Result{VariablesToSet: map[string]any{"result": "approved"}}, nil
	})
	registry.Register("go-handle-approved", func(_ context.Context, _ handler.JobContext) (handler.Result, error) {
		return handler.Result{VariablesToSet: map[string]any{"handled": "approved"}}, nil
	})
	registry.Register("go-handle-rejected", func(_ context.Context, _ handler.JobContext) (handler.Result, error) {
		return handler.Result{VariablesToSet: map[string]any{"handled": "rejected"}}, nil
	})

	// Parallel scenario handlers
	registry.Register("go-branch-left", func(_ context.Context, _ handler.JobContext) (handler.Result, error) {
		return handler.Result{VariablesToSet: map[string]any{"branchLeft": "done"}}, nil
	})
	registry.Register("go-branch-right", func(_ context.Context, _ handler.JobContext) (handler.Result, error) {
		return handler.Result{VariablesToSet: map[string]any{"branchRight": "done"}}, nil
	})
	registry.Register("go-merge-done", func(_ context.Context, _ handler.JobContext) (handler.Result, error) {
		return handler.Result{VariablesToSet: map[string]any{"merged": "done"}}, nil
	})

	// User-task scenario handlers
	registry.Register("go-before-review", func(_ context.Context, _ handler.JobContext) (handler.Result, error) {
		return handler.Result{VariablesToSet: map[string]any{"beforeReview": "done"}}, nil
	})
	registry.Register("go-after-review", func(_ context.Context, _ handler.JobContext) (handler.Result, error) {
		return handler.Result{VariablesToSet: map[string]any{"afterReview": "done"}}, nil
	})

	// Timer scenario handlers
	registry.Register("go-before-wait", func(_ context.Context, _ handler.JobContext) (handler.Result, error) {
		return handler.Result{VariablesToSet: map[string]any{"beforeWait": "done"}}, nil
	})
	registry.Register("go-timer-fired", func(_ context.Context, _ handler.JobContext) (handler.Result, error) {
		return handler.Result{VariablesToSet: map[string]any{"timerFired": "done"}}, nil
	})

	// Retry-fail scenario handler: fails on first attempt (retriesRemaining == retryCount == 2)
	registry.Register("go-flaky", func(_ context.Context, job handler.JobContext) (handler.Result, error) {
		if job.RetriesRemaining == 2 {
			return handler.Result{}, fmt.Errorf("simulated transient failure")
		}
		return handler.Result{VariablesToSet: map[string]any{"flaky": "done"}}, nil
	})

	// Signal-user-task scenario handlers
	registry.Register("go-signalwaitstep-completeusertask-start", func(_ context.Context, _ handler.JobContext) (handler.Result, error) {
		return handler.Result{VariablesToSet: map[string]any{"started": true}}, nil
	})
	registry.Register("go-signalwaitstep-completeusertask-end", func(_ context.Context, _ handler.JobContext) (handler.Result, error) {
		return handler.Result{VariablesToSet: map[string]any{"ended": true}}, nil
	})

	// Chaining scenario handlers
	registry.Register("go-chain-start", func(_ context.Context, _ handler.JobContext) (handler.Result, error) {
		return handler.Result{VariablesToSet: map[string]any{"applicantId": "123", "amount": float64(100)}}, nil
	})
	registry.Register("go-chain-finalize", func(_ context.Context, _ handler.JobContext) (handler.Result, error) {
		return handler.Result{VariablesToSet: map[string]any{"finalized": true}}, nil
	})

	// Transformation scenario handler
	registry.Register("go-transform-init", func(_ context.Context, _ handler.JobContext) (handler.Result, error) {
		return handler.Result{VariablesToSet: map[string]any{"firstName": "Alice"}}, nil
	})

	// Retry-exhausted scenario handler: always fails to exhaust all retries
	registry.Register("go-always-fail", func(_ context.Context, _ handler.JobContext) (handler.Result, error) {
		return handler.Result{}, fmt.Errorf("always fails")
	})

	// Manual-retry scenario handler. With retryCount=1 in the definition the
	// engine performs at most 2 auto attempts (initial + 1 retry) before
	// flipping the instance to FAILED. We want both auto attempts to fail
	// and the third invocation — triggered by the scenario's manual
	// RetryStep call — to succeed.
	var manualRetryMu sync.Mutex
	manualRetryAttempts := map[string]int{}
	registry.Register("go-manual-retry", func(_ context.Context, job handler.JobContext) (handler.Result, error) {
		manualRetryMu.Lock()
		manualRetryAttempts[job.InstanceID]++
		n := manualRetryAttempts[job.InstanceID]
		manualRetryMu.Unlock()
		if n < 3 {
			return handler.Result{}, fmt.Errorf("simulated failure attempt %d", n)
		}
		return handler.Result{VariablesToSet: map[string]any{"manualRetry": "done", "attempts": float64(n)}}, nil
	})

	// Manual-retry-with-variables scenario handler. Fails until the instance
	// variables carry `corrected == true`, which only happens after the
	// scenario issues RetryStep with a {"corrected": true} patch. retryCount=0
	// in the definition makes the first failure terminal.
	registry.Register("go-needs-fix", func(_ context.Context, job handler.JobContext) (handler.Result, error) {
		corrected, _ := job.Variables["corrected"].(bool)
		if !corrected {
			return handler.Result{}, fmt.Errorf("missing corrected=true; bad input data")
		}
		return handler.Result{VariablesToSet: map[string]any{"fixed": true}}, nil
	})

	// Decision-no-match scenario handler: sets result to "rejected" so no branch matches
	registry.Register("go-prepare-no-match", func(_ context.Context, _ handler.JobContext) (handler.Result, error) {
		return handler.Result{VariablesToSet: map[string]any{"result": "rejected"}}, nil
	})

	// Parallel-user-task scenario handler
	registry.Register("go-put-svc-branch", func(_ context.Context, _ handler.JobContext) (handler.Result, error) {
		return handler.Result{VariablesToSet: map[string]any{"svcBranchDone": true}}, nil
	})

	// Timer-interrupting scenario handlers
	registry.Register("go-slow-task", func(_ context.Context, _ handler.JobContext) (handler.Result, error) {
		// Intentionally sleeps past the PT2S interrupting timer so the boundary fires first.
		time.Sleep(30 * time.Second)
		return handler.Result{}, nil
	})
	registry.Register("go-timeout-handler", func(_ context.Context, _ handler.JobContext) (handler.Result, error) {
		return handler.Result{VariablesToSet: map[string]any{"timedOut": true}}, nil
	})

	// Loan approval scenario handlers
	registry.Register("validate-application", func(_ context.Context, _ handler.JobContext) (handler.Result, error) {
		return handler.Result{VariablesToSet: map[string]any{"applicationValidated": true}}, nil
	})
	registry.Register("credit-score", func(_ context.Context, _ handler.JobContext) (handler.Result, error) {
		return handler.Result{VariablesToSet: map[string]any{"creditScoreChecked": true}}, nil
	})
	registry.Register("fraud-screen", func(_ context.Context, _ handler.JobContext) (handler.Result, error) {
		return handler.Result{VariablesToSet: map[string]any{"fraudScreened": true}}, nil
	})
	registry.Register("escalate-review", func(_ context.Context, _ handler.JobContext) (handler.Result, error) {
		return handler.Result{VariablesToSet: map[string]any{"reviewEscalated": true}}, nil
	})
	registry.Register("approve-loan", func(_ context.Context, _ handler.JobContext) (handler.Result, error) {
		return handler.Result{VariablesToSet: map[string]any{"loanApproved": true}}, nil
	})
	registry.Register("notify-approval-overdue", func(_ context.Context, _ handler.JobContext) (handler.Result, error) {
		return handler.Result{VariablesToSet: map[string]any{"approvalOverdueNotified": true}}, nil
	})
	registry.Register("prepare-disbursement", func(_ context.Context, _ handler.JobContext) (handler.Result, error) {
		return handler.Result{VariablesToSet: map[string]any{"disbursementPrepared": true}}, nil
	})
	registry.Register("transfer-funds", func(_ context.Context, _ handler.JobContext) (handler.Result, error) {
		return handler.Result{VariablesToSet: map[string]any{"fundsTransferred": true}}, nil
	})
	registry.Register("notify-disbursement", func(_ context.Context, _ handler.JobContext) (handler.Result, error) {
		return handler.Result{VariablesToSet: map[string]any{"disbursementNotified": true}}, nil
	})

	var engineClient client.EngineClient
	if workerTransport == "grpc" {
		gc, err := client.NewGrpc(grpcHost, workerID)
		if err != nil {
			slog.Error("failed to create grpc client", "err", err)
			os.Exit(1)
		}
		engineClient = gc
		slog.Info("worker transport: grpc", "host", grpcHost)
	} else {
		engineClient = client.NewRest(engineURL, workerID)
		slog.Info("worker transport: rest", "engine", engineURL)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	mode := envOrDefault("WE_DISPATCH_MODE", "polling")
	if mode == "kafka_outbox" {
		brokers := envOrDefault("WE_KAFKA_SEED_BROKERS", "localhost:9092")
		jobTypes := registry.JobTypes()
		r, err := kafkarunner.New(kafkarunner.Config{
			WorkerID:    workerID,
			SeedBrokers: brokers,
			JobTypes:    jobTypes,
		}, engineClient, registry)
		if err != nil {
			slog.Error("failed to create kafkarunner", "err", err)
			os.Exit(1)
		}
		slog.Info("worker starting (kafka mode)", "brokers", brokers, "workerID", workerID, "jobTypes", jobTypes)
		if err := r.Run(ctx); err != nil {
			slog.Error("Kafka runner stopped with unacknowledged work", "err", err)
			os.Exit(1)
		}
	} else {
		var heartbeatDone chan error
		if os.Getenv("E2E_HEARTBEAT") == "1" {
			heartbeatRegistry := handler.New()
			heartbeatRegistry.Register("go-sdk-heartbeat", func(ctx context.Context, j handler.JobContext) (handler.Result, error) {
				recoveryMu.Lock()
				recoveryCalls[j.InstanceID]++
				calls := recoveryCalls[j.InstanceID]
				recoveryMu.Unlock()
				duration, _ := j.Variables["durationMs"].(float64)
				timer := time.NewTimer(time.Duration(duration) * time.Millisecond)
				defer timer.Stop()
				select {
				case <-ctx.Done():
					return handler.Result{}, ctx.Err()
				case <-timer.C:
				}
				if failure, _ := j.Variables["failure"].(bool); failure && j.RetriesRemaining == 2 {
					return handler.Result{}, fmt.Errorf("sdkRecovery heartbeat business failure: %s", j.InstanceID)
				}
				return handler.Result{VariablesToSet: map[string]any{"sdkRecovery": true, "instanceId": j.InstanceID, "handlerCalls": calls, "attemptId": j.JobID, "remaining": j.RetriesRemaining}}, nil
			})
			heartbeatRunner := runner.New(runner.Config{WorkerID: workerID + "-heartbeat", Parallelism: 2, Heartbeat: envOrDefault("E2E_HEARTBEAT_ENABLED", "1") == "1", LeaseSeconds: 30}, engineClient, heartbeatRegistry)
			heartbeatDone = make(chan error, 1)
			go func() {
				err := heartbeatRunner.RunWithError(ctx)
				heartbeatDone <- err
				if err != nil {
					slog.Error("heartbeat runner failed", "err", err)
					stop()
				}
			}()
		}
		r := runner.New(runner.Config{WorkerID: workerID, Parallelism: 2}, engineClient, registry)
		slog.Info("worker starting (polling mode)", "workerID", workerID)
		err := r.RunWithError(ctx)
		stop()
		if heartbeatDone != nil {
			if heartbeatErr := <-heartbeatDone; err == nil {
				err = heartbeatErr
			}
		}
		if err != nil {
			slog.Error("polling runner failed", "err", err)
			os.Exit(1)
		}
	}
	slog.Info("worker stopped")
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
