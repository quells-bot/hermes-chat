package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
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
