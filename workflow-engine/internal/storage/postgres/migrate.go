package postgres

import (
	"context"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	pgmigrate "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/batnam/rochallor-engine/workflow-engine/migrations"
)

// Migrate runs all pending UP migrations against the pool's database.
// It is safe to call on every startup — already-applied migrations are skipped.
func Migrate(pool *pgxpool.Pool) error {
	db := stdlib.OpenDBFromPool(pool)
	defer db.Close()

	// Own the dedicated connection even if driver initialization fails. Without
	// closing it, pgxpool.Close (and migrate-only itself) waits forever.
	conn, err := db.Conn(context.Background())
	if err != nil {
		return fmt.Errorf("migrate: acquire connection: %w", err)
	}
	defer conn.Close()
	driver, err := pgmigrate.WithConnection(context.Background(), conn, &pgmigrate.Config{})
	if err != nil {
		return fmt.Errorf("migrate: create pg driver: %w", err)
	}

	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("migrate: create iofs source: %w", err)
	}
	defer src.Close()

	m, err := migrate.NewWithInstance("iofs", src, "postgres", driver)
	if err != nil {
		return fmt.Errorf("migrate: create migrator: %w", err)
	}

	if err = m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("migrate: run up: %w", err)
	}
	return nil
}

// MigrateDown rolls back all applied migrations (used in integration test teardown).
func MigrateDown(pool *pgxpool.Pool) error {
	db := stdlib.OpenDBFromPool(pool)
	defer db.Close()

	conn, err := db.Conn(context.Background())
	if err != nil {
		return fmt.Errorf("migrate-down: acquire connection: %w", err)
	}
	defer conn.Close()
	driver, err := pgmigrate.WithConnection(context.Background(), conn, &pgmigrate.Config{})
	if err != nil {
		return fmt.Errorf("migrate-down: create pg driver: %w", err)
	}

	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("migrate-down: create iofs source: %w", err)
	}
	defer src.Close()

	m, err := migrate.NewWithInstance("iofs", src, "postgres", driver)
	if err != nil {
		return fmt.Errorf("migrate-down: create migrator: %w", err)
	}

	if err = m.Down(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("migrate-down: run down: %w", err)
	}
	return nil
}
