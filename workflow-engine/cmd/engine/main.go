// Command engine is the Workflow Engine process. It wires config, logger,
// metrics, postgres pool, REST router (port 8080), gRPC server (port 9090),
// and a Prometheus metrics endpoint (port 9091) and runs them until a signal.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"google.golang.org/grpc"

	grpcserver "github.com/batnam/rochallor-engine/workflow-engine/internal/api/grpc"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/api/rest"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/boundary"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/config"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/definition"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/dispatch"
	kafkaoutbox "github.com/batnam/rochallor-engine/workflow-engine/internal/dispatch/kafka_outbox"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/dispatch/polling"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/expression"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/instance"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/job"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/obs"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/storage/postgres"
	"github.com/prometheus/client_golang/prometheus"
)

func main() {
	if err := run(); err != nil {
		slog.Error("engine: fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	obs.InitLogger(cfg.LogLevel)
	slog.Info("engine: starting",
		"rest_port", cfg.RESTPort,
		"grpc_port", cfg.GRPCPort,
		"metrics_port", cfg.MetricsPort,
	)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// ── Database ──────────────────────────────────────────────────────────────
	pool, err := postgres.NewPoolWithOptions(ctx, cfg.PostgresDSN, postgres.PoolOptions{
		MaxConns: int32(cfg.DBMaxConns),
		MinConns: int32(cfg.DBMinConns),
	})
	if err != nil {
		return fmt.Errorf("postgres pool: %w", err)
	}
	defer pool.Close()

	if err := postgres.Migrate(pool); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	// ── Dispatch runtime (mode switch per FR-001) ────────────────────────────
	// cfg.DispatchMode has already been validated in config.Load; this switch
	// is the wiring seam that chooses which Runtime + Dispatcher pair is live
	// for this process. No silent fallback: unknown modes fail startup loudly.
	var dispatchRT dispatch.Runtime
	switch cfg.DispatchMode {
	case config.DispatchModePolling:
		dispatchRT = polling.New()
		obs.RegisterPollingMetrics(prometheus.DefaultRegisterer)
	case config.DispatchModeKafkaOutbox:

		// Pre-fetch all registered job types for topic validation (R-008).
		// We use a temporary repository just for this query.
		jobTypes, err := definition.NewRepository(pool).ListAllJobTypes(ctx)
		if err != nil {
			return fmt.Errorf("list job types for validation: %w", err)
		}
		dispatchRT = kafkaoutbox.New(kafkaoutbox.Config{
			Pool:          pool,
			SeedBrokers:   cfg.Kafka.SeedBrokers,
			JobTypes:      jobTypes,
			Transport:     cfg.Kafka.Transport,
			SASLMechanism: cfg.Kafka.SASLMechanism,
			SASLUsername:  cfg.Kafka.SASLUsername,
			SASLPassword:  cfg.Kafka.SASLPassword,
			TLSCAPath:     cfg.Kafka.TLSCAPath,
			TLSServerName: cfg.Kafka.TLSServerName,
			BatchSize:     cfg.Kafka.OutboxBatchSize,
			Logger:        slog.Default().With("component", "dispatch"),
		})

	default:
		return fmt.Errorf("unknown WE_DISPATCH_MODE %q", cfg.DispatchMode)
	}

	if err := dispatchRT.Start(ctx); err != nil {
		return fmt.Errorf("dispatch runtime start: %w", err)
	}
	defer func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		if err := dispatchRT.Stop(stopCtx); err != nil {
			slog.Error("engine: dispatch runtime stop", "err", err)
		}
	}()
	slog.Info("engine: dispatch runtime ready", "mode", cfg.DispatchMode)

	// ── Services ──────────────────────────────────────────────────────────────
	instance.SetExpressionEvaluator(expression.Evaluate)
	defRepo := definition.NewRepository(pool)
	instSvc := instance.NewService(pool, defRepo, dispatchRT.Dispatcher())

	// ── Background workers ────────────────────────────────────────────────────
	job.StartLeaseSweeper(ctx, pool, dispatchRT.Dispatcher(), 15*time.Second)
	boundary.StartTimerSweeper(ctx, pool, instSvc, 5*time.Second)
	// ── REST server ───────────────────────────────────────────────────────────
	restHandler := rest.NewRouter(pool, defRepo, instSvc, cfg.DispatchMode)
	restServer := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.RESTPort),
		Handler:      restHandler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// ── Metrics server ────────────────────────────────────────────────────────
	metricsServer := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.MetricsPort),
		Handler: rest.MetricsHandler(),
	}

	// ── gRPC server ───────────────────────────────────────────────────────────
	grpcLis, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.GRPCPort))
	if err != nil {
		return fmt.Errorf("grpc listen: %w", err)
	}
	grpcSrv := grpc.NewServer(
		grpc.UnaryInterceptor(grpcserver.LoggingUnaryInterceptor()),
	)
	grpcserver.NewEngineServer(defRepo, instSvc, pool, cfg.DispatchMode).Register(grpcSrv)

	// ── Start all servers ─────────────────────────────────────────────────────
	errCh := make(chan error, 3)

	go func() {
		slog.Info("engine: REST listening", "addr", restServer.Addr)
		if err := restServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- fmt.Errorf("rest: %w", err)
		}
	}()

	go func() {
		slog.Info("engine: metrics listening", "addr", metricsServer.Addr)
		if err := metricsServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- fmt.Errorf("metrics: %w", err)
		}
	}()

	go func() {
		slog.Info("engine: gRPC listening", "addr", grpcLis.Addr())
		if err := grpcSrv.Serve(grpcLis); err != nil {
			errCh <- fmt.Errorf("grpc: %w", err)
		}
	}()

	// ── Wait for shutdown signal or fatal error ───────────────────────────────
	select {
	case <-ctx.Done():
		slog.Info("engine: shutdown signal received")
	case fatalErr := <-errCh:
		slog.Error("engine: fatal server error", "err", fatalErr)
		return fatalErr
	}

	// ── Graceful shutdown ─────────────────────────────────────────────────────
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	grpcSrv.GracefulStop()

	if err := restServer.Shutdown(shutdownCtx); err != nil {
		slog.Error("engine: REST shutdown error", "err", err)
	}
	if err := metricsServer.Shutdown(shutdownCtx); err != nil {
		slog.Error("engine: metrics shutdown error", "err", err)
	}

	slog.Info("engine: stopped")
	return nil
}
