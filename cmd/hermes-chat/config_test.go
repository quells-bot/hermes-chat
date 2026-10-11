package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"testing"

	"github.com/oklog/ulid/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/reqid"
)

func TestLoadConfig_Defaults(t *testing.T) {
	t.Setenv("HERMES_CHAT_DB_PATH", "/tmp/x.db")
	c, err := loadConfig()
	require.NoError(t, err)
	assert.Equal(t, Config{HTTPAddr: ":8080", DebugAddr: "127.0.0.1:6060", DBPath: "/tmp/x.db", LogLevel: "info", GinMode: "release"}, c)
}

func TestLoadConfig_MissingDBPath(t *testing.T) {
	t.Setenv("HERMES_CHAT_DB_PATH", "") // registers restore of the original value
	os.Unsetenv("HERMES_CHAT_DB_PATH")
	_, err := loadConfig()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HERMES_CHAT_DB_PATH")
	t.Setenv("HERMES_CHAT_DB_PATH", "") // set but empty: envconfig's `required` accepts this, loadConfig must not
	_, err = loadConfig()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HERMES_CHAT_DB_PATH")
}

func TestLoadConfig_BadGinMode(t *testing.T) { // gin.SetMode panics on an unknown mode
	t.Setenv("HERMES_CHAT_DB_PATH", "/tmp/x.db")
	t.Setenv("HERMES_CHAT_GIN_MODE", "fast")
	_, err := loadConfig()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HERMES_CHAT_GIN_MODE")
}

func TestLoadConfig_BadLogLevel(t *testing.T) {
	t.Setenv("HERMES_CHAT_DB_PATH", "/tmp/x.db")
	t.Setenv("HERMES_CHAT_LOG_LEVEL", "loud")
	_, err := loadConfig()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HERMES_CHAT_LOG_LEVEL")
}

func TestNewLogger_JSONWithRequestID(t *testing.T) {
	var buf bytes.Buffer
	id := ulid.Make()
	newLogger(&buf, slog.LevelInfo).InfoContext(reqid.NewContext(context.Background(), id), "hi")
	var l map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &l))
	assert.Equal(t, id.String(), l["request_id"])
	buf.Reset()
	newLogger(&buf, slog.LevelWarn).Info("quiet")
	assert.Empty(t, buf.String())
}

func TestLoadConfig_DebugAddrLoopbackOnly(t *testing.T) {
	t.Setenv("HERMES_CHAT_DB_PATH", "/tmp/x.db")
	for _, addr := range []string{":6060", "0.0.0.0:6060", "192.168.1.5:6060", "example.com:6060", "nonsense"} {
		t.Setenv("HERMES_CHAT_DEBUG_ADDR", addr)
		_, err := loadConfig()
		require.Error(t, err, addr)
		assert.Contains(t, err.Error(), "HERMES_CHAT_DEBUG_ADDR", addr)
	}
	for _, addr := range []string{"127.0.0.1:6060", "127.0.0.1:0", "[::1]:6060", "localhost:6060"} {
		t.Setenv("HERMES_CHAT_DEBUG_ADDR", addr)
		_, err := loadConfig()
		assert.NoError(t, err, addr)
	}
}
