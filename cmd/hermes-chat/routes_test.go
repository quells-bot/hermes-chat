package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/oklog/ulid/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/app"
	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/bus"
	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/storage/storagetest"
)

// The real middleware chain from routes(): request ID, request log, recovery.
func TestRoutes_MiddlewareChain(t *testing.T) {
	gin.SetMode(gin.TestMode)
	prev := gin.DefaultErrorWriter
	gin.DefaultErrorWriter = io.Discard // silence gin.Recovery's stack dump
	t.Cleanup(func() { gin.DefaultErrorWriter = prev })

	var buf bytes.Buffer
	e := gin.New()
	routes(e, app.NewController(bus.NewService(storagetest.NewRepo(t), nil), app.Options{}), newLogger(&buf, -4))
	e.GET("/boom", func(*gin.Context) { panic("boom") })

	serveReq := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		e.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		return w
	}
	requestLines := func() []map[string]any {
		var out []map[string]any
		for _, l := range jsonLines(t, &buf) {
			if l["msg"] == "request" {
				out = append(out, l)
			}
		}
		buf.Reset()
		return out
	}

	w := serveReq("/healthz")
	assert.Equal(t, 200, w.Code)
	id := w.Header().Get("X-Request-ID")
	_, err := ulid.ParseStrict(id)
	require.NoError(t, err)
	ls := requestLines()
	require.Len(t, ls, 1)
	assert.Equal(t, id, ls[0]["request_id"])
	assert.Equal(t, "INFO", ls[0]["level"])

	w = serveReq("/boom")
	assert.Equal(t, 500, w.Code)
	ls = requestLines()
	require.Len(t, ls, 1)
	assert.Equal(t, "ERROR", ls[0]["level"])
	assert.EqualValues(t, 500, ls[0]["status"])

	// The auth groups are wired: no credentials is 401, even for a same-origin POST.
	w = httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/logout", nil)
	r.Header.Set("Origin", "http://"+r.Host)
	e.ServeHTTP(w, r)
	assert.Equal(t, 401, w.Code)
	assert.Equal(t, 401, serveReq("/agent/me").Code)

	// The same-origin check runs before authentication: a cross-origin POST with no cookie is 403, not 401.
	w = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodPost, "/api/logout", nil)
	r.Header.Set("Origin", "https://evil.test")
	e.ServeHTTP(w, r)
	assert.Equal(t, 403, w.Code)
}

func TestRoutes_Table(t *testing.T) {
	gin.SetMode(gin.TestMode)
	e := gin.New()
	routes(e, app.NewController(bus.NewService(storagetest.NewRepo(t), nil), app.Options{}), newLogger(io.Discard, -4))
	var got []string
	for _, r := range e.Routes() {
		got = append(got, r.Method+" "+r.Path)
	}
	sort.Strings(got)
	assert.Equal(t, []string{
		"DELETE /api/halt", "DELETE /api/note/entries/:entry", "DELETE /api/rooms/:room/agents/:agent",
		"GET /agent/events", "GET /agent/me", "GET /agent/rooms", "GET /api/agents", "GET /api/events", "GET /api/halt", "GET /api/humans", "GET /api/me", "GET /api/note", "GET /api/rooms",
		"GET /api/rooms/:room", "GET /api/rooms/:room/attachments/:attachment", "GET /api/rooms/:room/messages", "GET /api/rooms/:room/messages/:message", "GET /api/rooms/:room/messages/:message/activity", "GET /healthz",
		"HEAD /healthz",
		"PATCH /api/note/entries/:entry", "PATCH /api/rooms/:room",
		"POST /agent/deliveries/:message/done", "POST /agent/deliveries/:message/failed", "POST /agent/deliveries/:message/note-entries", "POST /agent/deliveries/:message/remarks",
		"POST /agent/deliveries/:message/tools", "POST /agent/deliveries/:message/working", "POST /agent/prompts/:message/expire", "POST /agent/rooms/:room/attachments", "POST /agent/rooms/:room/messages",
		"POST /agent/rooms/:room/prompts",
		"POST /api/dms", "POST /api/halt", "POST /api/login", "POST /api/logout", "POST /api/rooms", "POST /api/rooms/:room/agents",
		"POST /api/rooms/:room/agents/:agent/steer", "POST /api/rooms/:room/agents/:agent/stop",
		"POST /api/rooms/:room/humans", "POST /api/rooms/:room/messages", "POST /api/rooms/:room/messages/:message/answer", "POST /api/rooms/:room/messages/:message/cancel",
		"POST /api/rooms/:room/pause", "POST /api/rooms/:room/resume",
		"PUT /agent/activities/:activity", "PUT /agent/messages/:message", "PUT /api/note", "PUT /api/rooms/:room/read",
	}, got)
}
