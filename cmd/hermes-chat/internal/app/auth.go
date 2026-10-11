package app

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

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

// maxLoginBody bounds the login request body.
const maxLoginBody = 4096

// sessionMaxAge is the cookie lifetime in seconds, matching the session's.
const sessionMaxAge = int(bus.SessionLifetime / time.Second)

// badLoginBody is the app-level error for a login body that is not usable.
func badLoginBody(c *gin.Context) {
	c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": gin.H{
		"code":    "bad_request",
		"message": "request body must be a JSON object with username and password",
	}})
}

// setSessionCookie sets the session cookie; an empty value with maxAge 0 clears it.
func (h *controller) setSessionCookie(c *gin.Context, value string, maxAge int) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     SessionCookie,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   h.opts.CookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
}

// Login exchanges a username and password for a session cookie.
func (h *controller) Login(c *gin.Context) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	r := http.MaxBytesReader(c.Writer, c.Request.Body, maxLoginBody)
	if err := json.NewDecoder(r).Decode(&body); err != nil {
		badLoginBody(c)
		return
	}
	sess, err := h.svc.Login(c.Request.Context(), body.Username, body.Password, c.ClientIP())
	if err != nil {
		writeError(c, err)
		return
	}
	h.setSessionCookie(c, sess.Token, sessionMaxAge)
	c.JSON(http.StatusOK, gin.H{"id": sess.Human.ID, "username": sess.Human.Username})
}

// Logout ends the caller's session and clears the cookie.
func (h *controller) Logout(c *gin.Context) {
	token, _ := c.Cookie(SessionCookie) // HumanAuth has already vouched for it
	if err := h.svc.Logout(c.Request.Context(), token); err != nil {
		writeError(c, err)
		return
	}
	h.setSessionCookie(c, "", -1)
	c.Status(http.StatusNoContent)
}

// Me describes the signed-in human.
func (h *controller) Me(c *gin.Context) {
	human, err := h.svc.GetHuman(c.Request.Context(), actorFrom(c).ID)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": human.ID, "username": human.Username})
}

// AgentMe describes the authenticated agent.
func (h *controller) AgentMe(c *gin.Context) {
	agent, err := h.svc.GetAgent(c.Request.Context(), actorFrom(c).ID)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": agent.ID, "handle": agent.Handle, "display_name": agent.DisplayName})
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
