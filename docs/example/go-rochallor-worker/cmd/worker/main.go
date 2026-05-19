package main

import (
	"context"
	"go-rochallor-worker/internal/application"
	"go-rochallor-worker/internal/config"
	"go-rochallor-worker/internal/disbursment"
	"log/slog"
	"os/signal"
	"syscall"

	"github.com/batnam/rochallor-engine/workflow-sdk-go/client"
	"github.com/batnam/rochallor-engine/workflow-sdk-go/handler"
	"github.com/batnam/rochallor-engine/workflow-sdk-go/runner"
)

func main() {
	cfg := config.Load()

	engine := client.NewRest(cfg.EngineURL, cfg.WorkerID)

	registry := handler.New()
	application.Register(registry)
	disbursment.Register(registry)

	r := runner.New(runner.Config{
		WorkerID:     cfg.WorkerID,
		Parallelism:  cfg.Parallelism,
		PollInterval: cfg.PollInterval,
	}, engine, registry)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	slog.Info("los-worker started", "worker_id", cfg.WorkerID, "engine", cfg.EngineURL)
	r.Run(ctx)
	slog.Info("los-worker stopped")
}
