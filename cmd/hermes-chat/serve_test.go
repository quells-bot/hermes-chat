package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/cookiejar"
	"strconv"
	"strings"
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
	return startServeWithEnv(t)
}

// startServeWithEnv is startServe for a test that has already set its own
// environment, including HERMES_CHAT_DB_PATH.
func startServeWithEnv(t *testing.T) (api, debug string, cancel func(), done <-chan error) {
	t.Helper()
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

// login posts to /api/login with Origin set to the API's own base URL and
// returns the response, with its body already closed.
func login(t *testing.T, client *http.Client, api, username, password string, hdr ...string) *http.Response {
	t.Helper()
	body := fmt.Sprintf(`{"username":%q,"password":%q}`, username, password)
	req, err := http.NewRequest(http.MethodPost, api+"/api/login", strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", api)
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := client.Do(req)
	require.NoError(t, err)
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp
}

// status sends req and returns the status code.
func status(t *testing.T, client *http.Client, req *http.Request) int {
	t.Helper()
	resp, err := client.Do(req)
	require.NoError(t, err)
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp.StatusCode
}

// The whole stage, across two "processes": the CLI and a running server sharing one database file.
func TestServe_AuthEndToEnd(t *testing.T) {
	migrated(t)
	t.Setenv("HERMES_CHAT_COOKIE_SECURE", "false") // the test speaks plain HTTP
	runCmdIn(t, pw+"\n", "user", "add", "kai")
	_, out, _ := runCmd(t, "agent", "add", "scout", "Scout")
	bearer := tokenLine.FindStringSubmatch(out)[1]
	api, _, _, _ := startServeWithEnv(t)
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	client := &http.Client{Jar: jar}
	me, err := http.NewRequest("GET", api+"/api/me", nil)
	require.NoError(t, err)
	agentMe, err := http.NewRequest("GET", api+"/agent/me", nil)
	require.NoError(t, err)
	agentMe.Header.Set("Authorization", "Bearer "+bearer)

	assert.Equal(t, 401, status(t, client, me))
	resp := login(t, client, api, "kai", pw)
	require.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, 200, status(t, client, me)) // the jar carried the cookie
	assert.Equal(t, 200, status(t, http.DefaultClient, agentMe))

	code, _, _ := runCmdIn(t, "a whole new password\n", "user", "passwd", "kai") // while the server is running
	require.Equal(t, 0, code)
	assert.Equal(t, 401, status(t, client, me))
	code, _, _ = runCmd(t, "agent", "revoke", "scout")
	require.Equal(t, 0, code)
	assert.Equal(t, 401, status(t, http.DefaultClient, agentMe))
}

// A spoofed X-Forwarded-For must not dodge the IP throttle. An empty password
// is refused before any hashing, but still costs the IP a token.
func TestServe_ForwardedForCannotDodgeTheIPThrottle(t *testing.T) {
	api, _, _, _ := startServe(t)
	for i := 0; i < 20; i++ {
		resp := login(t, http.DefaultClient, api, fmt.Sprintf("user%d", i), "", "X-Forwarded-For", fmt.Sprintf("203.0.113.%d", i))
		require.Equal(t, 401, resp.StatusCode, i)
	}
	resp := login(t, http.DefaultClient, api, "user20", "", "X-Forwarded-For", "203.0.113.20")
	assert.Equal(t, 429, resp.StatusCode)
	n, err := strconv.Atoi(resp.Header.Get("Retry-After"))
	require.NoError(t, err)
	assert.True(t, n >= 1 && n <= 10, n)
}

func TestServe_TrustedProxyForwardedForIsHonored(t *testing.T) {
	t.Setenv("HERMES_CHAT_TRUSTED_PROXIES", "127.0.0.1")
	api, _, _, _ := startServe(t)
	for i := 0; i < 25; i++ { // more than one IP's burst, but each from a "different client"
		resp := login(t, http.DefaultClient, api, fmt.Sprintf("user%d", i), "", "X-Forwarded-For", fmt.Sprintf("203.0.113.%d", i))
		require.Equal(t, 401, resp.StatusCode, i)
	}
}
