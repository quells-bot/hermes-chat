package reqid_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/oklog/ulid/v2"
	"github.com/quells-bot/hermes-chat/internal/reqid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContextRoundTrip(t *testing.T) {
	id := ulid.Make()
	got, ok := reqid.FromContext(reqid.NewContext(context.Background(), id))
	require.True(t, ok)
	assert.Equal(t, id, got)
}

func TestFromContext_Absent(t *testing.T) {
	_, ok := reqid.FromContext(context.Background())
	assert.False(t, ok)
}

// logLine runs fn against a JSON logger wrapped by reqid.NewHandler and
// returns the single line it wrote, decoded.
func logLine(t *testing.T, fn func(*slog.Logger)) map[string]any {
	t.Helper()
	var buf bytes.Buffer
	fn(slog.New(reqid.NewHandler(slog.NewJSONHandler(&buf, nil))))
	var m map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &m))
	return m
}

func TestHandler_AddsRequestID(t *testing.T) {
	id := ulid.Make()
	ctx := reqid.NewContext(context.Background(), id)
	line := logLine(t, func(l *slog.Logger) { l.InfoContext(ctx, "hi") })
	assert.Equal(t, id.String(), line["request_id"])
}

func TestHandler_NoRequestID_NoAttr(t *testing.T) {
	line := logLine(t, func(l *slog.Logger) { l.InfoContext(context.Background(), "hi") })
	assert.NotContains(t, line, "request_id")
}

// A logger derived with With or WithGroup must still be wrapped.
func TestHandler_SurvivesWithAndWithGroup(t *testing.T) {
	id := ulid.Make()
	ctx := reqid.NewContext(context.Background(), id)
	line := logLine(t, func(l *slog.Logger) { l.With("a", 1).InfoContext(ctx, "hi") })
	assert.Equal(t, id.String(), line["request_id"])
	assert.EqualValues(t, 1, line["a"])
	line = logLine(t, func(l *slog.Logger) { l.WithGroup("g").InfoContext(ctx, "hi", "b", 2) })
	assert.Equal(t, id.String(), line["request_id"]) // top level, not inside "g"
}
