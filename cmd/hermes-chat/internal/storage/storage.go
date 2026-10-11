package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"modernc.org/sqlite"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/storage/sqlcgen"
)

var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("conflict")
	ErrNestedTx = errors.New("InTx called inside a transaction")
)

// SQLite extended result codes for unique and primary key violations.
const (
	sqliteConstraintPrimaryKey = 1555
	sqliteConstraintUnique     = 2067
)

// Repository is the storage surface the bus layer depends on.
type Repository interface {
	Ping(ctx context.Context) error
	// InTx runs fn in one write transaction. The Repository passed to fn
	// reads and writes through that transaction; calling InTx on it returns
	// ErrNestedTx. fn's error rolls back and is returned; a panic rolls back
	// and propagates. Inside fn, use only the Repository passed to it: calling a
	// write method on the outer repository waits forever, because the write pool
	// has one connection.
	InTx(ctx context.Context, fn func(Repository) error) error
	PutHuman(ctx context.Context, h Human) error                            // ErrConflict on duplicate id or username
	GetHumanByUsername(ctx context.Context, username string) (Human, error) // ErrNotFound
	ListHumans(ctx context.Context) ([]Human, error)                        // ordered by username
}

type repo struct {
	db     *DB
	reads  *sqlcgen.Queries
	writes *sqlcgen.Queries
	inTx   bool
}

// NewRepo returns a Repository over db.
func NewRepo(db *DB) *repo {
	return &repo{db: db, reads: sqlcgen.New(db.read), writes: sqlcgen.New(db.write)}
}

func (r *repo) Ping(ctx context.Context) error {
	if r.inTx {
		// The write connection is held by the transaction; pinging the pools would block.
		return ctx.Err()
	}
	if err := r.db.read.PingContext(ctx); err != nil {
		return fmt.Errorf("ping read pool: %w", err)
	}
	if err := r.db.write.PingContext(ctx); err != nil {
		return fmt.Errorf("ping write pool: %w", err)
	}
	return nil
}

func (r *repo) InTx(ctx context.Context, fn func(Repository) error) error {
	if r.inTx {
		return ErrNestedTx
	}
	tx, err := r.db.write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			// Runs on error and on panic; releases the only write connection.
			_ = tx.Rollback()
		}
	}()
	q := r.writes.WithTx(tx)
	if err := fn(&repo{db: r.db, reads: q, writes: q, inTx: true}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	committed = true
	return nil
}

// mapErr translates driver errors into the package's sentinel errors.
func mapErr(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	var se *sqlite.Error
	if errors.As(err, &se) {
		switch se.Code() {
		case sqliteConstraintUnique, sqliteConstraintPrimaryKey:
			return fmt.Errorf("%w: %w", ErrConflict, err)
		}
	}
	return err
}
