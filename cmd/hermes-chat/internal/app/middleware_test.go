package app_test

import (
	"io"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/oklog/ulid/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/app"
	"github.com/quells-bot/hermes-chat/internal/reqid"
)

func TestRequestID_HeaderGinAndGoContextAgree(t *testing.T) {
	e, _ := newEngine(t, realSvc(t))
	var fromGin, fromCtx string
	e.GET("/probe", func(c *gin.Context) {
		fromGin = c.MustGet(reqid.GinKey).(ulid.ULID).String()
		id, ok := reqid.FromContext(c.Request.Context())
		require.True(t, ok)
		fromCtx = id.String()
	})
	w := do(e, "GET", "/probe")
	hdr := w.Header().Get(app.RequestIDHeader)
	_, err := ulid.ParseStrict(hdr)
	require.NoError(t, err)
	assert.Equal(t, hdr, fromGin)
	assert.Equal(t, hdr, fromCtx)
}

func TestRequestID_UniquePerRequest(t *testing.T) {
	e, _ := newEngine(t, realSvc(t))
	a := do(e, "GET", "/healthz").Header().Get(app.RequestIDHeader)
	b := do(e, "GET", "/healthz").Header().Get(app.RequestIDHeader)
	assert.NotEqual(t, a, b)
}

// Review Focus 3.
func TestRequestID_IgnoresClientSuppliedHeader(t *testing.T) {
	e, buf := newEngine(t, realSvc(t))
	w := do(e, "GET", "/healthz", app.RequestIDHeader, "attacker-chosen")
	assert.NotEqual(t, "attacker-chosen", w.Header().Get(app.RequestIDHeader))
	assert.NotContains(t, buf.String(), "attacker-chosen")
}

func TestRequestLogger_OneLineWithRoutePattern(t *testing.T) {
	e, buf := newEngine(t, realSvc(t))
	e.GET("/things/:id", func(c *gin.Context) { c.Status(204) })
	w := do(e, "GET", "/things/42?token=hunter2", "Authorization", "Bearer s3cr3t", "Cookie", "sid=c00kie")
	ls := lines(t, buf)
	require.Len(t, ls, 1)
	l := ls[0]
	assert.Equal(t, "request", l["msg"])
	assert.Equal(t, "INFO", l["level"])
	assert.Equal(t, "GET", l["method"])
	assert.Equal(t, "/things/:id", l["route"])
	assert.EqualValues(t, 204, l["status"])
	assert.IsType(t, float64(0), l["duration_ms"])
	assert.Equal(t, w.Header().Get(app.RequestIDHeader), l["request_id"])
	for _, secret := range []string{"hunter2", "token", "s3cr3t", "c00kie", "/things/42"} {
		assert.NotContains(t, buf.String(), secret)
	}
}

func TestRequestLogger_Unmatched(t *testing.T) {
	e, buf := newEngine(t, realSvc(t))
	do(e, "GET", "/nope/secret-path")
	l := lines(t, buf)[0]
	assert.Equal(t, "unmatched", l["route"])
	assert.EqualValues(t, 404, l["status"])
	assert.NotContains(t, buf.String(), "secret-path")
}

func TestRequestLogger_PanicIsRecoveredAndLoggedAsError(t *testing.T) {
	prev := gin.DefaultErrorWriter
	gin.DefaultErrorWriter = io.Discard
	t.Cleanup(func() { gin.DefaultErrorWriter = prev })
	e, buf := newEngine(t, realSvc(t))
	e.GET("/boom", func(*gin.Context) { panic("boom") })
	w := do(e, "GET", "/boom")
	assert.Equal(t, 500, w.Code)
	var req map[string]any
	for _, l := range lines(t, buf) {
		if l["msg"] == "request" {
			req = l
		}
	}
	require.NotNil(t, req)
	assert.Equal(t, "ERROR", req["level"])
	assert.EqualValues(t, 500, req["status"])
}
