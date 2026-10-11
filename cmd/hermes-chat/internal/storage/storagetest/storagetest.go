// Package storagetest provides test helpers for code that needs a database.
package storagetest

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/storage"
)

// Open returns a migrated database in t.TempDir(), closed by t.Cleanup.
func Open(t testing.TB) *storage.DB {
	t.Helper()
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate database: %v", err)
	}
	return db
}

// NewRepo returns a Repository over a migrated temp-file database.
func NewRepo(t testing.TB) storage.Repository {
	t.Helper()
	return storage.NewRepo(Open(t))
}
