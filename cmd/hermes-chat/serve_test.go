package main

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// startServe runs serve on two 127.0.0.1:0 listeners and returns their base URLs,
// a cancel func, and a channel that receives serve's return value. The test's
// cleanup cancels serve and waits for it to return.
func startServe(t *testing.T) (api, debug string, cancel func(), done <-chan error) {
	t.Helper()
	setDB(t)
	cfg, err := loadConfig()
	require.NoError(t, err)

	apiLn, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	debugLn, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	ctx, cancelCtx := context.WithCancel(context.Background())
	result := make(chan error, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		result <- serve(ctx, cfg, newLogger(io.Discard, slog.LevelError), listeners{api: apiLn, debug: debugLn})
	}()
	t.Cleanup(func() {
		cancelCtx()
		<-finished
		http.DefaultClient.CloseIdleConnections()
	})
	return "http://" + apiLn.Addr().String(), "http://" + debugLn.Addr().String(), cancelCtx, result
}

func TestServe_EndToEnd(t *testing.T) {
	api, debug, cancel, done := startServe(t)

	resp, err := http.Get(api + "/healthz")
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	assert.Equal(t, 200, resp.StatusCode)
	assert.JSONEq(t, `{"status":"ok"}`, string(body))
	_, err = ulid.ParseStrict(resp.Header.Get("X-Request-ID"))
	assert.NoError(t, err)

	resp, err = http.Get(debug + "/debug/pprof/")
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, 200, resp.StatusCode)

	resp, err = http.Get(api + "/debug/pprof/")
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, 404, resp.StatusCode) // pprof is never on the public server

	cancel()
	select {
	case err := <-done:
		assert.NoError(t, err) // a cancelled context is a clean shutdown
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not return after cancel")
	}
}

func TestServe_AddressInUse(t *testing.T) {
	setDB(t)
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer taken.Close()
	t.Setenv("HERMES_CHAT_HTTP_ADDR", taken.Addr().String())
	t.Setenv("HERMES_CHAT_DEBUG_ADDR", "127.0.0.1:0")
	cfg, err := loadConfig()
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() { done <- serve(context.Background(), cfg, newLogger(io.Discard, slog.LevelError), listeners{}) }()
	select {
	case err := <-done:
		assert.Error(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("serve kept running with its address in use")
	}
}
