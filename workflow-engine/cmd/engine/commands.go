package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/storage/postgres"
)

func parseCommand(args []string) (string, error) {
	flags := flag.NewFlagSet("engine", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	up := flags.Bool("migrate-only", false, "apply pending migrations and exit")
	down := flags.Bool("migrate-down", false, "remove all migrated schema (disposable test databases only)")
	if err := flags.Parse(args); err != nil {
		return "", err
	}
	if flags.NArg() != 0 {
		return "", fmt.Errorf("unexpected arguments: %v", flags.Args())
	}
	if *up && *down {
		return "", fmt.Errorf("-migrate-only and -migrate-down are mutually exclusive")
	}
	if *up {
		return "up", nil
	}
	if *down {
		return "down", nil
	}
	return "", nil
}

// Migration commands intentionally read only the database configuration. They
// must work without Kafka, listener settings, or the rest of the runtime.
func runMigration(ctx context.Context, direction string) error {
	dsn := os.Getenv("WE_POSTGRES_DSN")
	if dsn == "" {
		return fmt.Errorf("WE_POSTGRES_DSN is required for migration commands")
	}
	connectCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	pool, err := postgres.NewPoolWithOptions(connectCtx, dsn, postgres.PoolOptions{MaxConns: 1})
	if err != nil {
		return err
	}
	defer pool.Close()
	if direction == "down" {
		return postgres.MigrateDown(pool)
	}
	return postgres.Migrate(pool)
}
