package storage

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"

	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// ErrSchemaBehind reports that the database has unapplied migrations.
var ErrSchemaBehind = errors.New("database schema is out of date")

func (d *DB) provider() (*goose.Provider, error) {
	sub, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		return nil, err
	}
	return goose.NewProvider(goose.DialectSQLite3, d.write, sub)
}

// Migrate applies all pending migrations.
func (d *DB) Migrate(ctx context.Context) error {
	p, err := d.provider()
	if err != nil {
		return fmt.Errorf("create migration provider: %w", err)
	}
	if _, err := p.Up(ctx); err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}

// CheckSchema returns ErrSchemaBehind if any migration is pending.
func (d *DB) CheckSchema(ctx context.Context) error {
	p, err := d.provider()
	if err != nil {
		return fmt.Errorf("create migration provider: %w", err)
	}
	// HasPending creates goose's version table when it is missing, and a check
	// must not change the database. A missing table means everything is pending.
	var n int
	err = d.read.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", goose.DefaultTablename).Scan(&n)
	if err != nil {
		return fmt.Errorf("look up migration table: %w", err)
	}
	if n == 0 {
		return ErrSchemaBehind
	}
	pending, err := p.HasPending(ctx)
	if err != nil {
		return fmt.Errorf("check pending migrations: %w", err)
	}
	if pending {
		return ErrSchemaBehind
	}
	return nil
}
