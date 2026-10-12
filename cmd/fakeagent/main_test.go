package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeServer plays the agent API: its first stream hands out one delivery and
// then drops, and every later stream stays open until the test ends.
type fakeServer struct {
	mu      sync.Mutex
	calls   []string // "METHOD path body", in order
	cursors []string // Last-Event-ID of each stream, in order
	opened  chan struct{}
}

func (s *fakeServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer hca_test" {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":{"code":"unauthorized","message":"bad token"}}`)
		return
	}
	if r.URL.Path != "/agent/events" {
		body, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.calls = append(s.calls, r.Method+" "+r.URL.Path+" "+string(body))
		s.mu.Unlock()
		if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/messages") {
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, `{"id":"01REPLY"}`)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	s.mu.Lock()
	s.cursors = append(s.cursors, r.Header.Get("Last-Event-ID"))
	first := len(s.cursors) == 1
	s.mu.Unlock()
	w.Header().Set("Content-Type", "text/event-stream")
	fmt.Fprint(w, "event: ready\ndata: {\"cursor\":6}\n\n")
	if first {
		fmt.Fprint(w, "id: 7\nevent: delivery\ndata: {\"message\":{\"id\":\"01MSG\",\"room_id\":\"01ROOM\",\"text\":\"hello\"}}\n\n")
		w.(http.Flusher).Flush()
		return // the stream drops
	}
	w.(http.Flusher).Flush()
	s.opened <- struct{}{}
	<-r.Context().Done()
}

func (s *fakeServer) snapshot() (calls, cursors []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.calls...), append([]string(nil), s.cursors...)
}

// syncBuffer is a bytes.Buffer the test can read while run writes to it.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func env(url, token string) func(string) string {
	return func(k string) string {
		return map[string]string{"FAKEAGENT_URL": url, "FAKEAGENT_TOKEN": token}[k]
	}
}

func TestRun_EchoesADeliveryAndReconnectsFromItsCursor(t *testing.T) {
	s := &fakeServer{opened: make(chan struct{}, 1)}
	srv := httptest.NewServer(s)
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	var stdout, stderr syncBuffer
	done := make(chan int, 1)
	go func() { done <- run(ctx, nil, env(srv.URL, "hca_test"), &stdout, &stderr, time.Millisecond) }()

	select {
	case <-s.opened:
	case <-time.After(5 * time.Second):
		t.Fatalf("no second stream; stderr: %s", stderr.String())
	}
	require.Eventually(t, func() bool { return strings.Count(stdout.String(), "\n") == 3 }, 5*time.Second, time.Millisecond)
	cancel()
	assert.Equal(t, 0, <-done)

	calls, cursors := s.snapshot()
	assert.Equal(t, []string{
		"POST /agent/deliveries/01MSG/working ",
		`POST /agent/rooms/01ROOM/messages {"text":"echo: hello","reply_to_id":"01MSG"}`,
		"POST /agent/deliveries/01MSG/done ",
	}, calls)
	assert.Equal(t, []string{"", "7"}, cursors)

	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	require.Len(t, lines, 3) // ready, delivery, ready
	assert.JSONEq(t, `{"event":"ready","id":"","data":{"cursor":6}}`, lines[0])
	assert.Contains(t, lines[1], `"event":"delivery"`)
	assert.Contains(t, stderr.String(), "stream closed")
	assert.NotContains(t, stdout.String()+stderr.String(), "hca_test")
}

func TestRun_BadTokenOnFirstConnectExits1(t *testing.T) {
	srv := httptest.NewServer(&fakeServer{})
	defer srv.Close()
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), nil, env(srv.URL, "hca_wrong"), &stdout, &stderr, time.Millisecond)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr.String(), "bad token")
}

func TestRun_Usage(t *testing.T) {
	for _, tc := range []struct {
		args       []string
		url, token string
	}{
		{[]string{"extra"}, "http://127.0.0.1:1", "hca_test"},
		{nil, "", "hca_test"},
		{nil, "http://127.0.0.1:1", ""},
	} {
		var stdout, stderr bytes.Buffer
		code := run(context.Background(), tc.args, env(tc.url, tc.token), &stdout, &stderr, time.Millisecond)
		assert.Equal(t, 2, code)
		assert.Contains(t, stderr.String(), "FAKEAGENT_URL")
	}
}
