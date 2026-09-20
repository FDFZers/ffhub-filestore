package db

import (
	"context"
	"embed"
	"errors"
	"fdfz-filestore/internal/config"
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
	cfg, err := pgxpool.ParseConfig(config.C.PostgresURL)
	if err != nil {
		return nil, fmt.Errorf("postgresql: failed to parse dsn: %w", err)
	}

	cfg.MaxConns = 25
	cfg.MinConns = 5
	cfg.MaxConnLifetime = 5 * time.Minute
	cfg.MaxConnIdleTime = 1 * time.Minute
	cfg.HealthCheckPeriod = 1 * time.Minute

	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
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
	m, err := migrate.NewWithSourceInstance("iofs", source, r.Replace(config.C.PostgresURL))
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
