package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/cookiejar"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/cli"
	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/sse"
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

const pw = "correct horse battery"

var tokenLine = regexp.MustCompile(`(?m)^token: (hca_[A-Za-z0-9_-]{43})$`)

// migrated points HERMES_CHAT_DB_PATH at a fresh temp file and applies the migrations.
func migrated(t *testing.T) string {
	t.Helper()
	p := setDB(t)
	require.NoError(t, cli.Migrate(context.Background(), p, io.Discard))
	return p
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

// apiCall sends a JSON request with Origin set to api and returns the status
// and the decoded object (nil for an empty body).
func apiCall(t *testing.T, client *http.Client, method, api, path, body string) (int, map[string]any) {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, api+path, rd)
	require.NoError(t, err)
	req.Header.Set("Origin", api)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	if len(raw) == 0 {
		return resp.StatusCode, nil
	}
	var out map[string]any
	require.NoError(t, json.Unmarshal(raw, &out), string(raw))
	return resp.StatusCode, out
}

// openEvents opens GET /api/events with an optional Last-Event-ID and registers
// cleanup that closes it.
func openEvents(t *testing.T, client *http.Client, api, lastEventID string) (*http.Response, *sse.Decoder) {
	t.Helper()
	req, err := http.NewRequest("GET", api+"/api/events", nil)
	require.NoError(t, err)
	if lastEventID != "" {
		req.Header.Set("Last-Event-ID", lastEventID)
	}
	resp, err := client.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { resp.Body.Close() })
	require.Equal(t, 200, resp.StatusCode)
	return resp, sse.NewDecoder(resp.Body)
}

// nextEvent returns the next frame that is not a comment, failing the test
// after 5 seconds.
func nextEvent(t *testing.T, d *sse.Decoder) sse.Frame {
	t.Helper()
	type result struct {
		f   sse.Frame
		err error
	}
	ch := make(chan result, 1)
	go func() {
		for {
			f, err := d.Next()
			if err != nil || f.Comment == "" || f.Event != "" || f.Data != "" || f.ID != "" {
				ch <- result{f, err}
				return
			}
		}
	}()
	select {
	case r := <-ch:
		require.NoError(t, r.err)
		return r.f
	case <-time.After(5 * time.Second):
		t.Fatal("no event within 5 seconds")
		return sse.Frame{}
	}
}

// The whole stage across the CLI and a running server: two humans, one agent, one room, one stream each.
func TestServe_RoomsAndStreamEndToEnd(t *testing.T) {
	migrated(t)
	t.Setenv("HERMES_CHAT_COOKIE_SECURE", "false")
	runCmdIn(t, pw+"\n", "user", "add", "kai")
	runCmdIn(t, pw+"\n", "user", "add", "ada")
	runCmd(t, "agent", "add", "scout", "Scout")
	api, _, _, _ := startServeWithEnv(t)
	client := func(name string) *http.Client {
		jar, err := cookiejar.New(nil)
		require.NoError(t, err)
		c := &http.Client{Jar: jar}
		require.Equal(t, 200, login(t, c, api, name, pw).StatusCode)
		return c
	}
	kai, ada := client("kai"), client("ada")

	_, agents := apiCall(t, kai, "GET", api, "/api/agents", "")
	scout := agents["agents"].([]any)[0].(map[string]any)["id"].(string)
	_, humans := apiCall(t, kai, "GET", api, "/api/humans", "")
	adaID := humans["humans"].([]any)[0].(map[string]any)["id"].(string)

	_, mine := openEvents(t, kai, api, "")
	ready := nextEvent(t, mine)
	assert.Equal(t, "ready", ready.Event)
	_, hers := openEvents(t, ada, api, "")
	nextEvent(t, hers)

	code, room := apiCall(t, kai, "POST", api, "/api/rooms", `{"topic":"Trip","agent_ids":["`+scout+`"]}`)
	require.Equal(t, 201, code)
	path := "/api/rooms/" + room["id"].(string)
	assert.Equal(t, "room.changed", nextEvent(t, mine).Event)
	code, _ = apiCall(t, ada, "GET", api, path, "")
	assert.Equal(t, 404, code)

	code, _ = apiCall(t, kai, "POST", api, path+"/humans", `{"human_id":"`+adaID+`"}`)
	require.Equal(t, 200, code)
	assert.Equal(t, "room.changed", nextEvent(t, mine).Event)
	assert.Equal(t, "room.changed", nextEvent(t, hers).Event) // the room's creation, now hers to see
	assert.Equal(t, "room.changed", nextEvent(t, hers).Event) // and the invitation

	code, msg := apiCall(t, ada, "POST", api, path+"/messages", `{"text":"hello from ada"}`)
	require.Equal(t, 201, code)
	for _, d := range []*sse.Decoder{mine, hers} {
		f := nextEvent(t, d)
		assert.Equal(t, "message.posted", f.Event)
		assert.Contains(t, f.Data, msg["id"].(string))
		assert.Contains(t, f.Data, "hello from ada")
	}
	_, list := apiCall(t, kai, "GET", api, path+"/messages", "")
	assert.Len(t, list["messages"], 1)

	_, again := openEvents(t, kai, api, ready.ID) // a reconnect replays what the first stream saw, in order
	assert.Equal(t, ready.ID, nextEvent(t, again).ID)
	for _, want := range []string{"room.changed", "room.changed", "message.posted"} {
		assert.Equal(t, want, nextEvent(t, again).Event)
	}

	code, _, _ = runCmd(t, "user", "remove", "ada") // Review Focus 4, while the server is running
	require.Equal(t, 0, code)
	_, list = apiCall(t, kai, "GET", api, path+"/messages", "")
	m := list["messages"].([]any)[0].(map[string]any)
	assert.Nil(t, m["author"])
	assert.Equal(t, "hello from ada", m["text"])
}

func TestServe_ShutdownClosesOpenStreams(t *testing.T) {
	migrated(t)
	t.Setenv("HERMES_CHAT_COOKIE_SECURE", "false")
	runCmdIn(t, pw+"\n", "user", "add", "kai")
	api, _, cancel, done := startServeWithEnv(t)
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	client := &http.Client{Jar: jar}
	require.Equal(t, 200, login(t, client, api, "kai", pw).StatusCode)
	_, d := openEvents(t, client, api, "")
	nextEvent(t, d)

	start := time.Now()
	cancel()
	select {
	case err := <-done:
		assert.NoError(t, err)
		assert.Less(t, time.Since(start), 3*time.Second, "the stream must not hold shutdown for the whole grace period")
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not return")
	}
	_, err = d.Next() // the stream is over
	assert.Error(t, err)
}
