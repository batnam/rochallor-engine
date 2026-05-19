package config

import (
	"os"
	"strconv"
	"time"
)

type Config struct {
	EngineURL    string
	WorkerID     string
	Parallelism  int
	PollInterval time.Duration
}

func Load() Config {
	return Config{
		EngineURL:    env("ENGINE_URL", "http://localhost:8080"),
		WorkerID:     env("WORKER_ID", "los-worder-1"),
		Parallelism:  envInt("PARALLELISM", 64),
		PollInterval: envDuration("POLL_INTERVAL_MS", 500) * time.Millisecond,
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func envDuration(key string, fallbackMs int) time.Duration {
	return time.Duration(envInt(key, fallbackMs))
}
