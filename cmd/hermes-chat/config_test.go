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
	assert.Equal(t, Config{HTTPAddr: ":8080", DebugAddr: "127.0.0.1:6060", DBPath: "/tmp/x.db", LogLevel: "info", GinMode: "release", CookieSecure: true, TLSMode: "autocert"}, c)
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

func TestConfig_AttachmentsDir(t *testing.T) {
	t.Setenv("HERMES_CHAT_DB_PATH", "/var/lib/hermes/chat.db")
	c, err := loadConfig()
	require.NoError(t, err)
	assert.Equal(t, "/var/lib/hermes/attachments", c.attachmentsDir())
	t.Setenv("HERMES_CHAT_ATTACHMENTS_DIR", "/srv/files")
	c, err = loadConfig()
	require.NoError(t, err)
	assert.Equal(t, "/srv/files", c.attachmentsDir())
}

func TestValidateForServe_BadGinMode(t *testing.T) { // gin.SetMode panics on an unknown mode
	t.Setenv("HERMES_CHAT_DB_PATH", "/tmp/x.db")
	t.Setenv("HERMES_CHAT_GIN_MODE", "fast")
	c, err := loadConfig()
	require.NoError(t, err)
	err = c.validateForServe()
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

func TestValidateForServe_DebugAddrLoopbackOnly(t *testing.T) {
	t.Setenv("HERMES_CHAT_DB_PATH", "/tmp/x.db")
	t.Setenv("HERMES_CHAT_TLS_MODE", "off")
	for _, addr := range []string{":6060", "0.0.0.0:6060", "192.168.1.5:6060", "example.com:6060", "nonsense"} {
		t.Setenv("HERMES_CHAT_DEBUG_ADDR", addr)
		c, err := loadConfig()
		require.NoError(t, err, addr)
		err = c.validateForServe()
		require.Error(t, err, addr)
		assert.Contains(t, err.Error(), "HERMES_CHAT_DEBUG_ADDR", addr)
	}
	for _, addr := range []string{"127.0.0.1:6060", "127.0.0.1:0", "[::1]:6060", "localhost:6060"} {
		t.Setenv("HERMES_CHAT_DEBUG_ADDR", addr)
		c, err := loadConfig()
		require.NoError(t, err, addr)
		assert.NoError(t, c.validateForServe(), addr)
	}
}

// B: only HERMES_CHAT_-prefixed variables are read.
func TestLoadConfig_IgnoresUnprefixedVariables(t *testing.T) {
	t.Setenv("HERMES_CHAT_DB_PATH", "/tmp/x.db")
	t.Setenv("LOG_LEVEL", "loud")
	t.Setenv("HTTP_ADDR", ":1")
	t.Setenv("DEBUG_ADDR", "0.0.0.0:1")
	t.Setenv("GIN_MODE", "fast")
	c, err := loadConfig()
	require.NoError(t, err)
	assert.Equal(t, Config{HTTPAddr: ":8080", DebugAddr: "127.0.0.1:6060", DBPath: "/tmp/x.db", LogLevel: "info", GinMode: "release", CookieSecure: true, TLSMode: "autocert"}, c)
}

func TestLoadConfig_UnprefixedDBPathIsNotRead(t *testing.T) {
	t.Setenv("HERMES_CHAT_DB_PATH", "") // registers restore of the original value
	os.Unsetenv("HERMES_CHAT_DB_PATH")
	t.Setenv("DB_PATH", "/tmp/x.db")
	_, err := loadConfig()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HERMES_CHAT_DB_PATH")
}

func TestLoadConfig_PrefixedVariablesOverrideDefaults(t *testing.T) {
	t.Setenv("HERMES_CHAT_DB_PATH", "/tmp/y.db")
	t.Setenv("HERMES_CHAT_HTTP_ADDR", ":9090")
	t.Setenv("HERMES_CHAT_DEBUG_ADDR", "localhost:7070")
	t.Setenv("HERMES_CHAT_LOG_LEVEL", "debug")
	t.Setenv("HERMES_CHAT_GIN_MODE", "test")
	c, err := loadConfig()
	require.NoError(t, err)
	assert.Equal(t, Config{HTTPAddr: ":9090", DebugAddr: "localhost:7070", DBPath: "/tmp/y.db", LogLevel: "debug", GinMode: "test", CookieSecure: true, TLSMode: "autocert"}, c)
}

func TestLoadConfig_AuthSettings(t *testing.T) {
	setDB(t)
	t.Setenv("HERMES_CHAT_COOKIE_SECURE", "false")
	t.Setenv("HERMES_CHAT_TRUSTED_PROXIES", "127.0.0.1,10.0.0.0/8")
	c, err := loadConfig()
	require.NoError(t, err)
	assert.False(t, c.CookieSecure)
	assert.Equal(t, []string{"127.0.0.1", "10.0.0.0/8"}, c.TrustedProxies)
	assert.NoError(t, c.validateForServe())
	c.TrustedProxies = []string{"not-an-ip"}
	err = c.validateForServe()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HERMES_CHAT_TRUSTED_PROXIES")
}

func TestLoadConfig_TLSDefaults(t *testing.T) {
	t.Setenv("HERMES_CHAT_DB_PATH", "/var/lib/hermes-chat/chat.db")
	c, err := loadConfig()
	require.NoError(t, err)
	assert.Equal(t, "autocert", c.TLSMode)
	assert.Equal(t, "", c.TLSDomain)
	assert.Equal(t, "/var/lib/hermes-chat/certs", c.tlsCacheDir())
	t.Setenv("HERMES_CHAT_TLS_CACHE_DIR", "/srv/certs")
	c, err = loadConfig()
	require.NoError(t, err)
	assert.Equal(t, "/srv/certs", c.tlsCacheDir())
}

func TestValidateForServe_TLS(t *testing.T) {
	for _, tc := range []struct{ mode, domain, wantErr string }{
		{"autocert", "chat.example.com", ""},
		{"off", "", ""},
		{"off", "not a domain", ""}, // ignored when off
		{"on", "chat.example.com", "HERMES_CHAT_TLS_MODE"},
		{"autocert", "", "HERMES_CHAT_TLS_DOMAIN"},
		{"autocert", "https://chat.example.com", "HERMES_CHAT_TLS_DOMAIN"},
		{"autocert", "chat.example.com:443", "HERMES_CHAT_TLS_DOMAIN"},
		{"autocert", "chat.example.com/", "HERMES_CHAT_TLS_DOMAIN"},
		{"autocert", "Chat.Example.com", "HERMES_CHAT_TLS_DOMAIN"},
		{"autocert", "*.example.com", "HERMES_CHAT_TLS_DOMAIN"},
		{"autocert", "203.0.113.10", "HERMES_CHAT_TLS_DOMAIN"},
		{"autocert", "localhost", "HERMES_CHAT_TLS_DOMAIN"},
		{"autocert", " chat.example.com", "HERMES_CHAT_TLS_DOMAIN"},
	} {
		t.Setenv("HERMES_CHAT_DB_PATH", "/tmp/x.db")
		t.Setenv("HERMES_CHAT_TLS_MODE", tc.mode)
		t.Setenv("HERMES_CHAT_TLS_DOMAIN", tc.domain)
		c, err := loadConfig()
		require.NoError(t, err)
		err = c.validateForServe()
		if tc.wantErr == "" {
			assert.NoError(t, err, "%s %q", tc.mode, tc.domain)
			continue
		}
		require.Error(t, err, "%s %q", tc.mode, tc.domain)
		assert.Contains(t, err.Error(), tc.wantErr)
	}
}
