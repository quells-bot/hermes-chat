package storage_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/storage"
)

var ctx = context.Background()

func open(t *testing.T) *storage.DB {
	t.Helper()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, db.Close()) })
	return db
}

type rowQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func pragma(t *testing.T, q rowQuerier, name string) string {
	t.Helper()
	var v string
	require.NoError(t, q.QueryRowContext(ctx, "PRAGMA "+name).Scan(&v))
	return v
}

func TestOpen_PragmasOnBothPools(t *testing.T) {
	read, write := open(t).Pools()
	for _, p := range []*sql.DB{read, write} {
		assert.Equal(t, "wal", pragma(t, p, "journal_mode"))
		assert.Equal(t, "5000", pragma(t, p, "busy_timeout"))
		assert.Equal(t, "1", pragma(t, p, "synchronous")) // NORMAL
		assert.Equal(t, "-64000", pragma(t, p, "cache_size"))
		assert.Equal(t, "1", pragma(t, p, "foreign_keys"))
		assert.Equal(t, "2", pragma(t, p, "temp_store")) // MEMORY
	}
	assert.Equal(t, "1", pragma(t, read, "query_only"))
	assert.Equal(t, "0", pragma(t, write, "query_only"))
}

// Pragmas come from the connection string, so every pooled connection has them.
func TestOpen_PragmasOnEveryReadConnection(t *testing.T) {
	read, _ := open(t).Pools()
	for i := 0; i < 3; i++ { // hold three connections open at once
		c, err := read.Conn(ctx)
		require.NoError(t, err)
		defer c.Close()
		assert.Equal(t, "1", pragma(t, c, "foreign_keys"))
		assert.Equal(t, "5000", pragma(t, c, "busy_timeout"))
	}
}

func TestOpen_PoolSizes(t *testing.T) {
	read, write := open(t).Pools()
	assert.Equal(t, 1, write.Stats().MaxOpenConnections)
	assert.Equal(t, max(4, runtime.NumCPU()), read.Stats().MaxOpenConnections)
}

func TestOpen_ReadPoolRejectsWrites(t *testing.T) {
	read, _ := open(t).Pools()
	_, err := read.ExecContext(ctx, "CREATE TABLE x (a INTEGER) STRICT")
	assert.Error(t, err)
}

func TestWriteDSN_TxLockImmediate(t *testing.T) {
	assert.Contains(t, storage.WriteDSN("/x/a.db"), "_txlock=immediate")
	assert.NotContains(t, storage.ReadDSN("/x/a.db"), "_txlock")
}

func TestOpen_PathWithSpecialCharacters(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "my db #1?100%.db")
	db, err := storage.Open(ctx, path)
	require.NoError(t, err)
	defer db.Close()
	require.NoError(t, db.Migrate(ctx))
	_, err = os.Stat(path)
	assert.NoError(t, err)
	entries, _ := os.ReadDir(dir)
	for _, e := range entries { // only the database and its -wal / -shm siblings
		assert.True(t, strings.HasPrefix(e.Name(), "my db #1?100%.db"), e.Name())
	}
}

func TestOpen_RelativePaths(t *testing.T) {
	t.Chdir(t.TempDir())
	require.NoError(t, os.Mkdir("sub", 0o755))
	for _, rel := range []string{"rel.db", "./rel2.db", "sub/rel3.db"} {
		db, err := storage.Open(ctx, rel)
		require.NoError(t, err, rel)
		require.NoError(t, db.Migrate(ctx), rel)
		require.NoError(t, db.Close())
		_, err = os.Stat(rel)
		assert.NoError(t, err, rel)
	}
}
