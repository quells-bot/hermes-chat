package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/app"
	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/bus"
	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/reqid"
	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/storage/storagetest"
)

// newEngine builds repository → service → controller → gin engine, logging JSON into buf.
func newEngine(t *testing.T, svc bus.Service) (*gin.Engine, *bytes.Buffer) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	buf := &bytes.Buffer{}
	log := slog.New(reqid.NewHandler(slog.NewJSONHandler(buf, nil)))
	prev := slog.Default()
	slog.SetDefault(log) // Healthz logs through the default logger
	t.Cleanup(func() { slog.SetDefault(prev) })
	e := gin.New()
	e.Use(app.RequestID(), app.RequestLogger(log), gin.Recovery())
	e.GET("/healthz", app.NewController(svc).Healthz)
	return e, buf
}

func realSvc(t *testing.T) bus.Service {
	t.Helper()
	return bus.NewService(storagetest.NewRepo(t), time.Now)
}

// do performs a request; hdr is alternating header names and values.
func do(e *gin.Engine, method, target string, hdr ...string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, nil)
	for i := 0; i+1 < len(hdr); i += 2 {
		r.Header.Set(hdr[i], hdr[i+1])
	}
	w := httptest.NewRecorder()
	e.ServeHTTP(w, r)
	return w
}

// lines decodes one JSON object per log line.
func lines(buf *bytes.Buffer) []map[string]any {
	var out []map[string]any
	for _, s := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if s == "" {
			continue
		}
		m := map[string]any{}
		if err := json.Unmarshal([]byte(s), &m); err != nil {
			panic(err)
		}
		out = append(out, m)
	}
	return out
}

func TestHealthz_OK(t *testing.T) {
	e, _ := newEngine(t, realSvc(t))
	w := do(e, "GET", "/healthz")
	assert.Equal(t, 200, w.Code)
	assert.JSONEq(t, `{"status":"ok"}`, w.Body.String())
}

type sickSvc struct{ bus.Service }

func (sickSvc) Health(context.Context) error { return errors.New("disk on fire") }

func TestHealthz_Unavailable(t *testing.T) {
	e, buf := newEngine(t, sickSvc{realSvc(t)})
	w := do(e, "GET", "/healthz")
	assert.Equal(t, 503, w.Code)
	assert.JSONEq(t, `{"status":"unavailable"}`, w.Body.String())
	assert.NotContains(t, w.Body.String(), "disk on fire")
	var found bool
	for _, l := range lines(buf) {
		if l["level"] == "ERROR" && strings.Contains(fmt.Sprint(l), "disk on fire") {
			found = true
			assert.Equal(t, w.Header().Get(app.RequestIDHeader), l["request_id"])
		}
	}
	assert.True(t, found, "the real error is logged")
}
