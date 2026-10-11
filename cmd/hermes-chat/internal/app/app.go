// Package app is the HTTP layer: gin handlers, middleware, and JSON.
package app

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/bus"
)

// RequestIDHeader is the response header carrying the request ID.
const RequestIDHeader = "X-Request-ID"

// Controller is the set of HTTP handlers.
type Controller interface {
	Healthz(c *gin.Context)
	HumanAuth(c *gin.Context)
	AgentAuth(c *gin.Context)
	Login(c *gin.Context)
	Logout(c *gin.Context)
	Me(c *gin.Context)
	AgentMe(c *gin.Context)
}

// Options are the settings the handlers need.
type Options struct {
	CookieSecure bool // the Secure attribute on the session cookie
}

type controller struct {
	svc  bus.Service
	opts Options
}

// NewController returns a Controller backed by svc.
func NewController(svc bus.Service, opts Options) *controller {
	return &controller{svc: svc, opts: opts}
}

// Healthz reports whether the service can reach its storage.
func (h *controller) Healthz(c *gin.Context) {
	if err := h.svc.Health(c.Request.Context()); err != nil {
		slog.ErrorContext(c.Request.Context(), "health check failed", "error", err)
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unavailable"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
