package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
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
	return runCmdIn(t, "", args...)
}

func runCmdIn(t *testing.T, stdin string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = run(context.Background(), args, strings.NewReader(stdin), &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestRun_Usage(t *testing.T) {
	setDB(t)
	code, _, errOut := runCmd(t)
	assert.Equal(t, 2, code)
	assert.Contains(t, errOut, "serve")
	assert.Contains(t, errOut, "migrate")
	assert.Contains(t, errOut, "user")
	code, _, _ = runCmd(t, "frobnicate")
	assert.Equal(t, 2, code)
}

func TestRun_UsageErrorsComeBeforeConfig(t *testing.T) {
	t.Setenv("HERMES_CHAT_DB_PATH", "") // registers restore of the original value
	os.Unsetenv("HERMES_CHAT_DB_PATH")
	code, _, errOut := runCmd(t)
	assert.Equal(t, 2, code)
	assert.Contains(t, errOut, "serve")
	assert.NotContains(t, errOut, "HERMES_CHAT_DB_PATH")
	code, _, errOut = runCmd(t, "frobnicate")
	assert.Equal(t, 2, code)
	assert.NotContains(t, errOut, "HERMES_CHAT_DB_PATH")
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
	assert.NotEmpty(t, w.CleanUp) // the clean-ups collected so far come back on failure
}

func TestWireUp_UnknownMode(t *testing.T) {
	setDB(t)
	cfg, err := loadConfig()
	require.NoError(t, err)
	w, err := wireUp(context.Background(), cfg, wireMode(99))
	defer w.cleanUp()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown")
	assert.NotEmpty(t, w.CleanUp)
	assert.Nil(t, w.Svc)
}

func TestRun_ExtraArgumentsAreUsageErrors(t *testing.T) {
	path := setDB(t)
	for _, args := range [][]string{{"migrate", "--help"}, {"serve", "extra"}} {
		code, out, errOut := runCmd(t, args...)
		assert.Equal(t, 2, code, args)
		assert.Empty(t, out, args)
		assert.Contains(t, errOut, "usage: hermes-chat", args)
	}
	db, err := storage.Open(context.Background(), path)
	require.NoError(t, err)
	defer db.Close()
	assert.ErrorIs(t, db.CheckSchema(context.Background()), storage.ErrSchemaBehind) // migrate --help did nothing
}

func TestUsage_ListsEveryCommand(t *testing.T) {
	require.NotEmpty(t, commandList())
	for _, c := range commandList() {
		assert.Contains(t, usage(), c.name)
		assert.Contains(t, usage(), c.summary)
	}
	for _, name := range []string{"serve", "migrate", "user"} {
		assert.Contains(t, usage(), name)
	}
}

// C: serve-only settings do not break other commands.
func TestRun_BadDebugAddrOnlyAffectsServe(t *testing.T) {
	path := setDB(t)
	t.Setenv("HERMES_CHAT_DEBUG_ADDR", "0.0.0.0:6060")
	code, _, _ := runCmd(t, "serve")
	assert.Equal(t, 1, code)
	_, err := os.Stat(path)
	assert.True(t, os.IsNotExist(err), "serve must fail before creating the database")
	code, _, errOut := runCmd(t, "serve")
	assert.Equal(t, 1, code)
	assert.Contains(t, errOut, "HERMES_CHAT_DEBUG_ADDR")
	code, _, _ = runCmd(t, "migrate")
	assert.Equal(t, 0, code)
}
