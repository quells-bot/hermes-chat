package app

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/bus"
)

// SessionCookie is the name of the browser session cookie.
const SessionCookie = "hermes_session"

// ActorKey is the gin context key holding the caller's bus.Actor.
const ActorKey = "actor"

// actorFrom returns the actor stored by an auth middleware.
func actorFrom(c *gin.Context) bus.Actor {
	return c.MustGet(ActorKey).(bus.Actor)
}

// HumanAuth identifies a human by session cookie.
func (h *controller) HumanAuth(c *gin.Context) {
	token, err := c.Cookie(SessionCookie)
	if err != nil || token == "" {
		writeError(c, bus.ErrUnauthenticated)
		return
	}
	actor, err := h.svc.ResolveSession(c.Request.Context(), token)
	if err != nil {
		writeError(c, err)
		return
	}
	c.Set(ActorKey, actor)
	c.Next()
}

// AgentAuth identifies an agent by bearer token.
func (h *controller) AgentAuth(c *gin.Context) {
	fields := strings.Fields(c.GetHeader("Authorization"))
	if len(fields) != 2 || !strings.EqualFold(fields[0], "bearer") {
		c.Header("WWW-Authenticate", "Bearer")
		writeError(c, bus.ErrUnauthenticated)
		return
	}
	actor, err := h.svc.ResolveAgentToken(c.Request.Context(), fields[1])
	if err != nil {
		if errors.Is(err, bus.ErrUnauthenticated) || errors.Is(err, bus.ErrInvalidCredentials) {
			c.Header("WWW-Authenticate", "Bearer")
		}
		writeError(c, err)
		return
	}
	c.Set(ActorKey, actor)
	c.Next()
}

// SameOrigin refuses state-changing requests whose Origin is not this server.
func SameOrigin() gin.HandlerFunc {
	return func(c *gin.Context) {
		switch c.Request.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			return
		}
		u, err := url.Parse(c.GetHeader("Origin"))
		if err != nil || u.Host == "" || !strings.EqualFold(u.Host, c.Request.Host) {
			writeError(c, bus.ErrForbidden)
			return
		}
	}
}
