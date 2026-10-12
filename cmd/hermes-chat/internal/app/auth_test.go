package app_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
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
	ctrl := app.NewController(svc, app.Options{})
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
	ctrl := app.NewController(resolveFails{realSvc(t)}, app.Options{})
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

// apiEngine builds the real route table by hand, as routes.go does: public
// login, the human group, the agent group. secure sets Options.CookieSecure.
func apiEngine(t *testing.T, secure bool) (e *gin.Engine, buf *bytes.Buffer, svc bus.Service) {
	t.Helper()
	svc = realSvc(t)
	e, buf = newEngine(t, svc)
	ctrl := app.NewController(svc, app.Options{CookieSecure: secure})
	e.POST("/api/login", app.SameOrigin(), ctrl.Login)
	human := e.Group("/api", app.SameOrigin(), ctrl.HumanAuth)
	human.POST("/logout", ctrl.Logout)
	human.GET("/me", ctrl.Me)
	human.GET("/agents", ctrl.ListAgents)
	human.GET("/humans", ctrl.ListHumans)
	human.GET("/rooms", ctrl.ListRooms)
	human.POST("/rooms", ctrl.CreateRoom)
	human.POST("/dms", ctrl.OpenDM)
	human.GET("/rooms/:room", ctrl.GetRoom)
	human.PATCH("/rooms/:room", ctrl.UpdateRoom)
	human.POST("/rooms/:room/humans", ctrl.InviteHuman)
	human.POST("/rooms/:room/agents", ctrl.AddRoomAgent)
	human.DELETE("/rooms/:room/agents/:agent", ctrl.RemoveRoomAgent)
	human.PUT("/rooms/:room/read", ctrl.MarkRoomRead)
	human.GET("/rooms/:room/attachments/:attachment", ctrl.GetAttachment)
	human.GET("/rooms/:room/messages", ctrl.ListMessages)
	human.POST("/rooms/:room/messages", ctrl.PostMessage)
	human.GET("/rooms/:room/messages/:message", ctrl.GetMessage)
	human.POST("/rooms/:room/messages/:message/cancel", ctrl.CancelMessage)
	human.GET("/rooms/:room/messages/:message/activity", ctrl.ListActivity)
	human.POST("/rooms/:room/agents/:agent/stop", ctrl.StopTurn)
	human.POST("/rooms/:room/agents/:agent/steer", ctrl.Steer)
	human.POST("/rooms/:room/pause", ctrl.PauseRoom)
	human.POST("/rooms/:room/resume", ctrl.ResumeRoom)
	human.POST("/rooms/:room/messages/:message/answer", ctrl.AnswerPrompt)
	human.GET("/note", ctrl.GetNote)
	human.PUT("/note", ctrl.SetNoteText)
	human.PATCH("/note/entries/:entry", ctrl.UpdateNoteEntry)
	human.DELETE("/note/entries/:entry", ctrl.DeleteNoteEntry)
	human.GET("/halt", ctrl.GetHalt)
	human.POST("/halt", ctrl.StopEverything)
	human.DELETE("/halt", ctrl.ResumeEverything)
	human.GET("/events", ctrl.Events)
	e.Group("/agent", ctrl.AgentAuth).GET("/me", ctrl.AgentMe)
	return e, buf, svc
}

// post sends a JSON body with Origin: https://example.com and any extra headers
// (alternating names and values; a later Origin replaces the default).
func post(e *gin.Engine, target, body string, hdr ...string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "https://example.com")
	for i := 0; i+1 < len(hdr); i += 2 {
		r.Header.Set(hdr[i], hdr[i+1])
	}
	w := httptest.NewRecorder()
	e.ServeHTTP(w, r)
	return w
}

// sessionCookie returns the hermes_session cookie from Set-Cookie.
func sessionCookie(t *testing.T, w *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range w.Result().Cookies() {
		if c.Name == app.SessionCookie {
			return c
		}
	}
	require.FailNow(t, "no session cookie in the response")
	return nil
}

func TestLogin_SetsTheSessionCookie(t *testing.T) {
	e, buf, svc := apiEngine(t, true)
	kai, _ := svc.CreateHuman(ctx, "kai", pw)
	w := post(e, "/api/login", `{"username":" Kai ","password":"`+pw+`","extra":1}`)
	require.Equal(t, 200, w.Code)
	assert.JSONEq(t, `{"id":"`+kai.ID.String()+`","username":"kai"}`, w.Body.String())
	c := sessionCookie(t, w)
	assert.Regexp(t, `^hcs_[A-Za-z0-9_-]{43}$`, c.Value)
	assert.Equal(t, "/", c.Path)
	assert.Equal(t, 7776000, c.MaxAge)
	assert.True(t, c.HttpOnly)
	assert.True(t, c.Secure)
	assert.Equal(t, http.SameSiteLaxMode, c.SameSite)
	assert.Empty(t, c.Domain)
	assert.NotContains(t, w.Body.String(), c.Value)
	for _, secret := range []string{c.Value, pw} {
		assert.NotContains(t, buf.String(), secret)
	}

	me := do(e, "GET", "/api/me", "Cookie", app.SessionCookie+"="+c.Value)
	assert.Equal(t, 200, me.Code)
	assert.JSONEq(t, `{"id":"`+kai.ID.String()+`","username":"kai"}`, me.Body.String())
}

func TestLogin_CookieNotSecureWhenConfiguredOff(t *testing.T) {
	e, _, svc := apiEngine(t, false)
	_, _ = svc.CreateHuman(ctx, "kai", pw)
	assert.False(t, sessionCookie(t, post(e, "/api/login", `{"username":"kai","password":"`+pw+`"}`)).Secure)
}

// An unknown user and a wrong password must be indistinguishable.
func TestLogin_FailuresLookTheSame(t *testing.T) {
	e, _, svc := apiEngine(t, true)
	_, _ = svc.CreateHuman(ctx, "kai", pw)
	a := post(e, "/api/login", `{"username":"kai","password":"wrong password"}`)
	b := post(e, "/api/login", `{"username":"nobody","password":"wrong password"}`)
	assert.Equal(t, 401, a.Code)
	assert.Equal(t, a.Code, b.Code)
	assert.Equal(t, a.Body.String(), b.Body.String())
	assert.JSONEq(t, `{"error":{"code":"invalid_credentials","message":"wrong username or password"}}`, a.Body.String())
	assert.Empty(t, a.Header().Values("Set-Cookie"))
	assert.Empty(t, b.Header().Values("Set-Cookie"))
}

func TestLogin_BadBodies(t *testing.T) {
	e, _, _ := apiEngine(t, true)
	for _, body := range []string{"", "not json", `[]`, `"kai"`, `{"username":1}`, `{"username":"kai","password":"` + strings.Repeat("a", 5000) + `"}`} {
		w := post(e, "/api/login", body)
		assert.Equal(t, 400, w.Code, "%.20q", body)
		assert.JSONEq(t, `{"error":{"code":"bad_request","message":"request body must be a JSON object with username and password"}}`, w.Body.String())
	}
	assert.Equal(t, 401, post(e, "/api/login", `{}`).Code) // well-formed but empty is just wrong credentials
}

func TestLogin_NeedsSameOriginAndThrottles(t *testing.T) {
	e, _, svc := apiEngine(t, true)
	_, _ = svc.CreateHuman(ctx, "kai", pw)
	good := `{"username":"kai","password":"` + pw + `"}`
	assert.Equal(t, 403, post(e, "/api/login", good, "Origin", "https://evil.test").Code) // login CSRF
	for i := 0; i < 5; i++ {
		require.Equal(t, 401, post(e, "/api/login", `{"username":"kai","password":"wrong password"}`).Code)
	}
	w := post(e, "/api/login", good)
	assert.Equal(t, 429, w.Code)
	assert.Regexp(t, `^[1-9][0-9]?$`, w.Header().Get("Retry-After"))
	assert.Empty(t, w.Header().Values("Set-Cookie"))
}

func TestLogout(t *testing.T) {
	e, _, svc := apiEngine(t, true)
	_, _ = svc.CreateHuman(ctx, "kai", pw)
	tok := sessionCookie(t, post(e, "/api/login", `{"username":"kai","password":"`+pw+`"}`)).Value
	cookie := app.SessionCookie + "=" + tok
	assert.Equal(t, 403, post(e, "/api/logout", "", "Cookie", cookie, "Origin", "https://evil.test").Code) // another site cannot log you out
	assert.Equal(t, 200, do(e, "GET", "/api/me", "Cookie", cookie).Code)
	w := post(e, "/api/logout", "", "Cookie", cookie)
	assert.Equal(t, 204, w.Code)
	gone := sessionCookie(t, w)
	assert.Empty(t, gone.Value)
	assert.Less(t, gone.MaxAge, 0) // net/http reports Max-Age=0 as -1
	assert.Equal(t, 401, do(e, "GET", "/api/me", "Cookie", cookie).Code)
	assert.Equal(t, 401, post(e, "/api/logout", "", "Cookie", cookie).Code)
	assert.Equal(t, 401, post(e, "/api/logout", "").Code)
}

func TestAgentMe(t *testing.T) {
	e, _, svc := apiEngine(t, true)
	a, token, _ := svc.CreateAgent(ctx, "scout", "Scout")
	w := do(e, "GET", "/agent/me", "Authorization", "Bearer "+token)
	assert.Equal(t, 200, w.Code)
	assert.JSONEq(t, `{"id":"`+a.ID.String()+`","handle":"scout","display_name":"Scout"}`, w.Body.String())
	assert.Equal(t, 401, do(e, "GET", "/agent/me").Code)
	assert.Equal(t, 401, do(e, "GET", "/api/me", "Authorization", "Bearer "+token).Code) // the groups do not accept each other's credentials
}
