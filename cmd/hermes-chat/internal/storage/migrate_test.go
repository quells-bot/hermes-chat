package storage_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/storage"
	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/storage/storagetest"
)

func tables(t *testing.T, db *storage.DB) []string {
	t.Helper()
	read, _ := db.Pools()
	rows, err := read.QueryContext(ctx, "SELECT name FROM sqlite_master WHERE type='table'")
	require.NoError(t, err)
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		require.NoError(t, rows.Scan(&n))
		names = append(names, n)
	}
	require.NoError(t, rows.Err())
	return names
}

func TestMigrate_CreatesStrictHumans(t *testing.T) {
	db := open(t)
	require.NoError(t, db.Migrate(ctx))
	assert.Contains(t, tables(t, db), "humans")
	assert.Contains(t, tables(t, db), "halt")
	assert.Contains(t, tables(t, db), "prompts")
	assert.Contains(t, tables(t, db), "note_entries")
	assert.Contains(t, tables(t, db), "attachments")
	_, write := db.Pools()
	_, err := write.ExecContext(ctx, `INSERT INTO humans (id, username, created_at) VALUES ('a','b','not a number')`)
	assert.Error(t, err) // STRICT rejects text in an INTEGER column
}

func TestMigrate_Idempotent(t *testing.T) {
	db := open(t)
	require.NoError(t, db.Migrate(ctx))
	require.NoError(t, db.Migrate(ctx))
}

func TestCheckSchema(t *testing.T) {
	db := open(t)
	assert.ErrorIs(t, db.CheckSchema(ctx), storage.ErrSchemaBehind)
	assert.Empty(t, tables(t, db)) // checking must not create anything
	require.NoError(t, db.Migrate(ctx))
	assert.NoError(t, db.CheckSchema(ctx))
}

func TestStoragetestOpen_IsMigrated(t *testing.T) {
	assert.NoError(t, storagetest.Open(t).CheckSchema(ctx))
}
