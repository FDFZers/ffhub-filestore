package db

import (
	"context"
	"embed"
	"errors"
	"ffhub-filestore/internal/cfg"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PDB struct {
	*pgxpool.Pool
}

func NewDB() (*PDB, error) {
	c, err := pgxpool.ParseConfig(cfg.C.PostgresURL)
	if err != nil {
		return nil, fmt.Errorf("postgresql: failed to parse dsn: %w", err)
	}

	c.MaxConns = 25
	c.MinConns = 5
	c.MaxConnLifetime = 5 * time.Minute
	c.MaxConnIdleTime = 1 * time.Minute
	c.HealthCheckPeriod = 1 * time.Minute

	pool, err := pgxpool.NewWithConfig(context.Background(), c)
	if err != nil {
		return nil, fmt.Errorf("postgresql: failed to create pool: %w", err)
	}

	// 验证连通性
	pingCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgresql: failed to ping database: %w", err)
	}

	return &PDB{Pool: pool}, nil
}

//go:embed migrations/*.sql
var fs embed.FS

func RunMigrations() error {
	source, err := iofs.New(fs, "migrations")
	if err != nil {
		return err
	}

	r := strings.NewReplacer(
		"postgres://", "pgx5://",
		"postgresql://", "pgx5://",
	)
	m, err := migrate.NewWithSourceInstance("iofs", source, r.Replace(cfg.C.PostgresURL))
	if err != nil {
		return err
	}
	defer func(m *migrate.Migrate) {
		sourceErr, dbErr := m.Close()
		slog.Warn("Failed to close migration database", "sourceErr", sourceErr, "dbErr", dbErr)
	}(m)

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return err
	}
	return nil
}
