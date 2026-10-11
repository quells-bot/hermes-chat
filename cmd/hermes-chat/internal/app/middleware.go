package app

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/oklog/ulid/v2"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/bus"
	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/reqid"
)

// RequestID generates a fresh ULID for every request, ignoring any inbound
// X-Request-ID, and exposes it via gin, the request context, and the response.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := ulid.Make()
		c.Set(reqid.GinKey, id)
		c.Request = c.Request.WithContext(reqid.NewContext(c.Request.Context(), id))
		c.Header(RequestIDHeader, id.String())
		c.Next()
	}
}

// RequestLogger logs one line per request. It records only the method, the
// matched route pattern, the status, and the duration.
func RequestLogger(log *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		route := c.FullPath()
		if route == "" {
			route = "unmatched"
		}
		status := c.Writer.Status()
		level := slog.LevelInfo
		if status >= http.StatusInternalServerError {
			level = slog.LevelError
		}
		attrs := []slog.Attr{
			slog.String("method", c.Request.Method),
			slog.String("route", route),
			slog.Int("status", status),
			slog.Float64("duration_ms", float64(time.Since(start))/float64(time.Millisecond)),
		}
		if actor, ok := c.Get(ActorKey); ok {
			if a, ok := actor.(bus.Actor); ok {
				attrs = append(attrs, slog.String("actor", a.String()))
			}
		}
		log.LogAttrs(c.Request.Context(), level, "request", attrs...)
	}
}
