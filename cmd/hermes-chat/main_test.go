package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/storage"
)

// setDB points HERMES_CHAT_DB_PATH at a fresh path in t.TempDir() and returns it.
func setDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	t.Setenv("HERMES_CHAT_DB_PATH", path)
	return path
}

func runCmd(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = run(context.Background(), args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestRun_Usage(t *testing.T) {
	setDB(t)
	code, _, errOut := runCmd(t)
	assert.Equal(t, 2, code)
	assert.Contains(t, errOut, "serve")
	code, _, _ = runCmd(t, "frobnicate")
	assert.Equal(t, 2, code)
}

func TestRun_ConfigErrorExitsOne(t *testing.T) {
	t.Setenv("HERMES_CHAT_DB_PATH", "") // registers restore of the original value
	os.Unsetenv("HERMES_CHAT_DB_PATH")
	code, _, errOut := runCmd(t, "migrate")
	assert.Equal(t, 1, code)
	assert.Contains(t, errOut, "HERMES_CHAT_DB_PATH")
}

func TestRun_Migrate(t *testing.T) {
	path := setDB(t)
	code, out, _ := runCmd(t, "migrate")
	assert.Equal(t, 0, code)
	assert.Contains(t, out, "migrations applied")
	code, _, _ = runCmd(t, "migrate")
	assert.Equal(t, 0, code) // idempotent
	db, err := storage.Open(context.Background(), path)
	require.NoError(t, err)
	defer db.Close()
	assert.NoError(t, db.CheckSchema(context.Background()))
}

func TestWireUp_CheckModeOnFreshDatabase(t *testing.T) {
	setDB(t)
	cfg, err := loadConfig()
	require.NoError(t, err)
	w, err := wireUp(context.Background(), cfg, wireCheck)
	defer w.cleanUp()
	assert.ErrorIs(t, err, storage.ErrSchemaBehind)
}
