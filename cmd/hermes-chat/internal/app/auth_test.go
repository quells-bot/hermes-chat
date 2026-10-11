package app_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/app"
	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/bus"
)

const pw = "correct horse battery"

var ctx = context.Background()

// authEngine is newEngine plus /h (HumanAuth) and /a (AgentAuth), each answering 200 with the actor's String().
// It returns a live session token for kai and a live bearer token for @scout.
func authEngine(t *testing.T) (e *gin.Engine, buf *bytes.Buffer, svc bus.Service, session, bearer string, kai bus.Human, scout bus.Agent) {
	t.Helper()
	svc = realSvc(t)
	e, buf = newEngine(t, svc)
	ctrl := app.NewController(svc)
	reply := func(c *gin.Context) { c.String(200, app.ActorFrom(c).String()) }
	e.GET("/h", ctrl.HumanAuth, reply)
	e.GET("/a", ctrl.AgentAuth, reply)

	kai, err := svc.CreateHuman(ctx, "kai", pw)
	require.NoError(t, err)
	sess, err := svc.Login(ctx, "kai", pw, "192.0.2.1")
	require.NoError(t, err)
	scout, bearer, err = svc.CreateAgent(ctx, "scout", "Scout")
	require.NoError(t, err)
	buf.Reset()
	return e, buf, svc, sess.Token, bearer, kai, scout
}

func TestHumanAuth(t *testing.T) {
	e, buf, svc, session, bearer, kai, _ := authEngine(t)
	w := do(e, "GET", "/h", "Cookie", app.SessionCookie+"="+session)
	assert.Equal(t, 200, w.Code)
	assert.Equal(t, "human:"+kai.ID.String(), w.Body.String())
	assert.Equal(t, "human:"+kai.ID.String(), lines(t, buf)[0]["actor"]) // the request log names the actor
	assert.NotContains(t, buf.String(), session)

	for _, hdr := range [][]string{nil, {"Cookie", app.SessionCookie + "="}, {"Cookie", app.SessionCookie + "=nope"}, {"Cookie", "other=" + session}, {"Cookie", app.SessionCookie + "=" + bearer}, {"Authorization", "Bearer " + session}} {
		buf.Reset()
		w := do(e, "GET", "/h", hdr...)
		assert.Equal(t, 401, w.Code, hdr)
		assert.JSONEq(t, `{"error":{"code":"unauthenticated","message":"authentication required"}}`, w.Body.String())
		assert.NotContains(t, lines(t, buf)[0], "actor")
	}
	require.NoError(t, svc.Logout(ctx, session))
	assert.Equal(t, 401, do(e, "GET", "/h", "Cookie", app.SessionCookie+"="+session).Code)
}

func TestAgentAuth(t *testing.T) {
	e, buf, svc, session, bearer, _, scout := authEngine(t)
	for _, scheme := range []string{"Bearer", "bearer", "BEARER"} {
		w := do(e, "GET", "/a", "Authorization", scheme+" "+bearer)
		assert.Equal(t, 200, w.Code, scheme)
		assert.Equal(t, "agent:"+scout.ID.String(), w.Body.String())
	}
	assert.NotContains(t, buf.String(), bearer)
	for _, v := range []string{"", bearer, "Bearer", "Bearer ", "Bearer " + bearer + " extra", "Basic " + bearer, "Bearer " + session} {
		var hdr []string
		if v != "" {
			hdr = []string{"Authorization", v}
		}
		w := do(e, "GET", "/a", hdr...)
		assert.Equal(t, 401, w.Code, "%q", v)
		assert.Equal(t, "Bearer", w.Header().Get("WWW-Authenticate"), "%q", v)
	}
	assert.Equal(t, 401, do(e, "GET", "/a", "Cookie", app.SessionCookie+"="+session).Code) // a human cookie is not an agent credential
	require.NoError(t, svc.RevokeAgent(ctx, "scout"))
	assert.Equal(t, 401, do(e, "GET", "/a", "Authorization", "Bearer "+bearer).Code)
}

type resolveFails struct{ bus.Service }

func (resolveFails) ResolveSession(context.Context, string) (bus.Actor, error) {
	return bus.Actor{}, errors.New("disk on fire")
}
func (resolveFails) ResolveAgentToken(context.Context, string) (bus.Actor, error) {
	return bus.Actor{}, errors.New("disk on fire")
}

func TestAuth_StorageFailureIs500AndNeverReachesTheHandler(t *testing.T) {
	e, _ := newEngine(t, nil)
	ctrl := app.NewController(resolveFails{realSvc(t)})
	reached := false
	e.GET("/h", ctrl.HumanAuth, func(*gin.Context) { reached = true })
	e.GET("/a", ctrl.AgentAuth, func(*gin.Context) { reached = true })
	assert.Equal(t, 500, do(e, "GET", "/h", "Cookie", app.SessionCookie+"=x").Code)
	assert.Equal(t, 500, do(e, "GET", "/a", "Authorization", "Bearer x").Code)
	assert.False(t, reached)
}

// Review Focus 4. httptest.NewRequest sets Host to "example.com".
func TestSameOrigin(t *testing.T) {
	e, _ := newEngine(t, realSvc(t))
	hits := 0
	e.Any("/x", app.SameOrigin(), func(c *gin.Context) { hits++; c.Status(204) })
	for _, m := range []string{"GET", "HEAD", "OPTIONS"} {
		assert.Equal(t, 204, do(e, m, "/x").Code, m) // no Origin needed to read
		assert.Equal(t, 204, do(e, m, "/x", "Origin", "https://evil.test").Code, m)
	}
	for _, m := range []string{"POST", "PUT", "PATCH", "DELETE"} {
		for _, ok := range []string{"https://example.com", "http://example.com", "https://EXAMPLE.com"} {
			assert.Equal(t, 204, do(e, m, "/x", "Origin", ok).Code, "%s %s", m, ok)
		}
		before := hits
		for _, bad := range []string{"", "null", "https://evil.test", "https://example.com.evil.test", "https://example.com:8443", "https://evil.test/example.com", "example.com", "://"} {
			var hdr []string
			if bad != "" {
				hdr = []string{"Origin", bad}
			}
			w := do(e, m, "/x", hdr...)
			assert.Equal(t, 403, w.Code, "%s %q", m, bad)
			assert.JSONEq(t, `{"error":{"code":"forbidden","message":"not allowed"}}`, w.Body.String())
		}
		assert.Equal(t, before, hits, "a refused request never reaches the handler")
	}
}
