package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/oklog/ulid/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/app"
	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/bus"
	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/reqid"
	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/sse"
	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/storage"
	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/storage/storagetest"
)

// streamWait is how long a stream test waits for something that should happen at once.
const streamWait = 5 * time.Second

// syncBuffer is a bytes.Buffer safe to write from the server while the test reads it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// rig is a real HTTP server with GET /api/events behind HumanAuth, the usual three middlewares logging into log,
// kai logged in through the bus, and kai's room with scout (so the log starts with one event and no request lines).
type rig struct {
	srv    *httptest.Server
	log    *syncBuffer
	svc    bus.Service
	hub    *bus.Hub
	kai    bus.Actor
	room   bus.Room
	scout  bus.Agent // the room's agent
	cookie string    // kai's Cookie header value
	token  string    // kai's session token
	scoutToken string // scout's bearer token
}

// newRig builds a rig. tune, if not nil, adjusts the server before it starts.
func newRig(t *testing.T, opts app.Options, tune func(*httptest.Server)) rig {
	t.Helper()
	return newFaultyRig(t, opts, tune, nil, nil)
}

// newFaultyRig is newRig with mocks to force errors: wrapRepo, if not nil, wraps
// the repository under the bus; wrapSvc, if not nil, wraps the service the
// controller calls. The rig's own svc is always the real one.
func newFaultyRig(t *testing.T, opts app.Options, tune func(*httptest.Server), wrapRepo func(storage.Repository) storage.Repository, wrapSvc func(bus.Service) bus.Service) rig {
	t.Helper()
	gin.SetMode(gin.TestMode)
	repo := storagetest.NewRepo(t)
	if wrapRepo != nil {
		repo = wrapRepo(repo)
	}
	svc := bus.NewService(repo, time.Now)
	r := rig{log: &syncBuffer{}, svc: svc, hub: svc.Hub()}
	var handled bus.Service = svc
	if wrapSvc != nil {
		handled = wrapSvc(svc)
	}

	log := slog.New(reqid.NewHandler(slog.NewJSONHandler(r.log, nil)))
	prev := slog.Default()
	slog.SetDefault(log) // Events logs through the default logger
	t.Cleanup(func() { slog.SetDefault(prev) })

	e := gin.New()
	e.Use(app.RequestID(), app.RequestLogger(log), gin.Recovery())
	ctrl := app.NewController(handled, opts)
	e.Group("/api", app.SameOrigin(), ctrl.HumanAuth).GET("/events", ctrl.Events)
	e.Group("/agent", ctrl.AgentAuth).GET("/events", ctrl.AgentEvents)

	kai, err := svc.CreateHuman(ctx, "kai", pw)
	require.NoError(t, err)
	sess, err := svc.Login(ctx, "kai", pw, "192.0.2.1")
	require.NoError(t, err)
	scout, scoutToken, err := svc.CreateAgent(ctx, "scout", "Scout")
	require.NoError(t, err)
	r.kai = bus.Actor{Kind: bus.ActorHuman, ID: kai.ID}
	r.token = sess.Token
	r.cookie = app.SessionCookie + "=" + sess.Token
	r.scout = scout
	r.scoutToken = scoutToken
	r.room, err = svc.CreateRoom(ctx, r.kai, "Trip planning", []ulid.ULID{scout.ID})
	require.NoError(t, err)

	r.srv = httptest.NewUnstartedServer(e)
	if tune != nil {
		tune(r.srv)
	}
	r.srv.Start()
	t.Cleanup(r.srv.Close) // waits for the handlers; the streams' own cleanups run first
	return r
}

// open starts a stream with the given Cookie header value and extra headers, and registers cleanup that closes it.
func (r rig) open(t *testing.T, cookie, query string, hdr ...string) (*http.Response, *sse.Decoder) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, r.srv.URL+"/api/events"+query, nil)
	require.NoError(t, err)
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := r.srv.Client().Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp, sse.NewDecoder(resp.Body)
}

// openAgent starts the agent stream (`/agent/events`) with the given bearer token.
func (r rig) openAgent(t *testing.T, token, query string) (*http.Response, *sse.Decoder) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, r.srv.URL+"/agent/events"+query, nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := r.srv.Client().Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp, sse.NewDecoder(resp.Body)
}

// nothingElse reports whether no non-comment frame arrives on the stream within ms.
func nothingElse(d *sse.Decoder, ms time.Duration) bool {
	got := make(chan error, 1)
	go func() {
		for {
			if f, err := d.Next(); err != nil {
				got <- err
				return
			} else if !(f.Comment != "" && f == sse.Frame{Comment: f.Comment}) {
				got <- nil
				return
			}
		}
	}()
	select {
	case <-got:
		return false
	case <-time.After(ms):
		return true
	}
}

// scoutActor returns scout's actor, for driving the bus as the agent.
func (r rig) scoutActor() bus.Actor { return bus.Actor{Kind: bus.ActorAgent, ID: r.scout.ID} }

// next returns the next frame that is not a comment, failing the test after 5 seconds.
func next(t *testing.T, d *sse.Decoder) sse.Frame {
	t.Helper()
	type result struct {
		f   sse.Frame
		err error
	}
	got := make(chan result, 1)
	go func() {
		for {
			f, err := d.Next()
			comment := f.Comment != "" && f == sse.Frame{Comment: f.Comment}
			if err != nil || !comment {
				got <- result{f, err}
				return
			}
		}
	}()
	select {
	case res := <-got:
		require.NoError(t, res.err, "reading the stream")
		return res.f
	case <-time.After(streamWait):
		require.FailNow(t, "no frame arrived on the stream")
		return sse.Frame{}
	}
}

// ended reports whether the stream reaches EOF within 5 seconds.
func ended(d *sse.Decoder) bool {
	done := make(chan error, 1)
	go func() {
		for {
			if _, err := d.Next(); err != nil {
				done <- err
				return
			}
		}
	}()
	select {
	case err := <-done:
		return errors.Is(err, io.EOF)
	case <-time.After(streamWait):
		return false
	}
}

// eventually polls cond every 10 ms for up to 5 seconds.
func eventually(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	require.Eventually(t, cond, streamWait, 10*time.Millisecond, msg)
}

// post has kai post to r.room through the bus.
func (r rig) post(t *testing.T, text string) bus.Message {
	t.Helper()
	m, err := r.svc.PostMessage(ctx, r.kai, r.room.ID, text, ulid.Zero)
	require.NoError(t, err)
	return m
}

func ptr[T any](v T) *T { return &v }

func TestEvents_ReadyThenLive(t *testing.T) {
	r := newRig(t, app.Options{}, nil)
	r.post(t, "before") // the log now holds 2 events
	resp, d := r.open(t, r.cookie, "")
	require.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))
	assert.Equal(t, "no-store", resp.Header.Get("Cache-Control"))
	assert.Equal(t, "no", resp.Header.Get("X-Accel-Buffering"))
	assert.Equal(t, sse.Frame{ID: "2", Event: "ready", Data: `{"cursor":2}`}, next(t, d)) // no cursor: start at the present

	m := r.post(t, "after")
	f := next(t, d)
	assert.Equal(t, "3", f.ID)
	assert.Equal(t, "message.posted", f.Event)
	assert.JSONEq(t, fmt.Sprintf(`{"message":{"id":%q,"room_id":%q,"author":{"kind":"human","id":%q,"name":"kai"},"text":"after","reply_to_id":null,"created_at":%q}}`,
		m.ID, r.room.ID, r.kai.ID, stamp(m.CreatedAt)), f.Data)

	_, err := r.svc.UpdateRoom(ctx, r.kai, r.room.ID, bus.RoomUpdate{Topic: ptr("Renamed")})
	require.NoError(t, err)
	f = next(t, d)
	assert.Equal(t, "4", f.ID)
	assert.Equal(t, "room.changed", f.Event)
	var body struct{ Room map[string]any }
	require.NoError(t, json.Unmarshal([]byte(f.Data), &body))
	assert.Equal(t, "Renamed", body.Room["topic"])
	assert.Equal(t, m.ID.String(), body.Room["last_read_message_id"])
}

// Review Focus 1.
func TestEvents_Cursors(t *testing.T) {
	r := newRig(t, app.Options{}, nil)
	m := r.post(t, "one") // events 1 (room) and 2 (message)
	_, d := r.open(t, r.cookie, "?cursor=0")
	assert.Equal(t, sse.Frame{ID: "0", Event: "ready", Data: `{"cursor":0}`}, next(t, d))
	assert.Equal(t, "1", next(t, d).ID)
	f := next(t, d)
	assert.Equal(t, "2", f.ID)
	assert.Contains(t, f.Data, m.ID.String())

	_, d = r.open(t, r.cookie, "?cursor=0", "Last-Event-ID", "1") // the header wins: this is a reconnect
	assert.Equal(t, "1", next(t, d).ID)
	assert.Equal(t, "2", next(t, d).ID)

	_, d = r.open(t, r.cookie, "", "Last-Event-ID", "500") // ahead of the log
	assert.Equal(t, sse.Frame{ID: "2", Event: "ready", Data: `{"cursor":2}`}, next(t, d))
	r.post(t, "two")
	assert.Equal(t, "3", next(t, d).ID)

	for _, bad := range []string{"?cursor=abc", "?cursor=-1", "?cursor=1.5", "?cursor=", "?cursor=99999999999999999999"} {
		resp, _ := r.open(t, r.cookie, bad)
		body, _ := io.ReadAll(resp.Body)
		assert.Equal(t, 400, resp.StatusCode, bad)
		assert.JSONEq(t, badReq("cursor must be a non-negative integer"), string(body), bad)
	}
	resp, _ := r.open(t, r.cookie, "", "Last-Event-ID", "nope")
	assert.Equal(t, 400, resp.StatusCode)
}

func TestEvents_NeedsASession(t *testing.T) {
	r := newRig(t, app.Options{}, nil)
	_, bearer, _ := r.svc.CreateAgent(ctx, "atlas", "Atlas")
	resp, _ := r.open(t, "", "")
	assert.Equal(t, 401, resp.StatusCode)
	resp, _ = r.open(t, "", "", "Authorization", "Bearer "+bearer)
	assert.Equal(t, 401, resp.StatusCode)
}

// Review Focus 3.
func TestEvents_OnlyMyRooms(t *testing.T) {
	r := newRig(t, app.Options{}, nil)
	_, err := r.svc.CreateHuman(ctx, "ada", pw)
	require.NoError(t, err)
	sess, err := r.svc.Login(ctx, "ada", pw, "192.0.2.2")
	require.NoError(t, err)
	_, hers := r.open(t, app.SessionCookie+"="+sess.Token, "")
	next(t, hers) // ready
	_, mine := r.open(t, r.cookie, "")
	next(t, mine)
	r.post(t, "for kai only")
	assert.Equal(t, "message.posted", next(t, mine).Event)
	_, err = r.svc.InviteHuman(ctx, r.kai, r.room.ID, sess.Human.ID)
	require.NoError(t, err)
	f := next(t, hers) // the first thing ada hears is what she is now entitled to, in log order
	assert.Equal(t, "message.posted", f.Event)
	assert.Contains(t, f.Data, "for kai only")
	assert.Equal(t, "room.changed", next(t, hers).Event)
}

func TestEvents_HeartbeatsAndOutlivesTheServerWriteTimeout(t *testing.T) {
	r := newRig(t, app.Options{HeartbeatInterval: 30 * time.Millisecond}, func(s *httptest.Server) { s.Config.WriteTimeout = 100 * time.Millisecond })
	_, d := r.open(t, r.cookie, "")
	f, err := d.Next()
	require.NoError(t, err)
	assert.Equal(t, "ready", f.Event)
	for range 3 {
		f, err = d.Next()
		require.NoError(t, err)
		assert.Equal(t, sse.Frame{Comment: "hb"}, f)
	}
	time.Sleep(400 * time.Millisecond) // four times the server's write timeout
	r.post(t, "still here")
	assert.Contains(t, next(t, d).Data, "still here")
}

// Review Focus 2.
func TestEvents_EndsWhenTheSessionDoes(t *testing.T) {
	ends := map[string]func(r rig) error{
		"logout": func(r rig) error { return r.svc.Logout(ctx, r.token) },
		"passwd": func(r rig) error { return r.svc.SetHumanPassword(ctx, "kai", "a whole new password") },
		"remove": func(r rig) error { return r.svc.RemoveHuman(ctx, "kai") },
	}
	for name, end := range ends {
		r := newRig(t, app.Options{HeartbeatInterval: 20 * time.Millisecond}, nil)
		_, d := r.open(t, r.cookie, "")
		next(t, d)
		require.NoError(t, end(r), name)
		assert.True(t, ended(d), name)
		resp, _ := r.open(t, r.cookie, "")
		assert.Equal(t, 401, resp.StatusCode, name) // and a reconnect is refused
	}
}

func TestEvents_ClosesWhenTheHubStops(t *testing.T) {
	r := newRig(t, app.Options{}, nil)
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- r.hub.Run(runCtx) }()
	_, d := r.open(t, r.cookie, "")
	next(t, d)
	cancel()
	require.NoError(t, <-done)
	assert.True(t, ended(d))
	resp, _ := r.open(t, r.cookie, "")
	body, _ := io.ReadAll(resp.Body)
	assert.Equal(t, 503, resp.StatusCode)
	assert.JSONEq(t, `{"error":{"code":"unavailable","message":"shutting down; try again shortly"}}`, string(body))
}

// One log line when the stream opens, one when it closes, and the handler does return when the client leaves.
func TestEvents_LogsOpenAndClose(t *testing.T) {
	r := newRig(t, app.Options{}, nil)
	resp, d := r.open(t, r.cookie, "")
	next(t, d)
	assert.Contains(t, r.log.String(), `"msg":"stream opened"`)
	assert.Contains(t, r.log.String(), `"actor":"`+r.kai.String()+`"`)
	assert.NotContains(t, r.log.String(), `"msg":"request"`, "the request line is written at close")
	resp.Body.Close()
	r.post(t, "wake the handler so it notices") // a write to a closed connection fails
	eventually(t, func() bool { return strings.Contains(r.log.String(), `"msg":"request"`) }, "the handler returned")
	assert.Contains(t, r.log.String(), `"route":"/api/events"`)
	assert.NotContains(t, r.log.String(), r.token)
}

// Review Focus 5. The client opens the stream and never reads. Enough is sent to fill any socket buffer;
// the write deadline must then end the handler.
func TestEvents_AClientThatStopsReadingIsDropped(t *testing.T) {
	r := newRig(t, app.Options{StreamWriteTimeout: 100 * time.Millisecond}, nil)
	r.open(t, r.cookie, "") // never read
	eventually(t, func() bool { return strings.Contains(r.log.String(), `"msg":"stream opened"`) }, "the stream opened")
	big := strings.Repeat("a", bus.MaxMessageBytes)
	for range 100 { // about 25 MiB; posting is never held up
		_, err := r.svc.PostMessage(ctx, r.kai, r.room.ID, big, ulid.Zero)
		require.NoError(t, err)
	}
	eventually(t, func() bool { return strings.Contains(r.log.String(), `"msg":"request"`) }, "the stalled stream was dropped")
}

// A response writer that cannot take a write deadline cannot carry a stream:
// the answer is a 500, sent before any stream header. A recorder is such a writer.
func TestEvents_NoWriteDeadlineIs500(t *testing.T) {
	e, _, svc := apiEngine(t, false)
	_, err := svc.CreateHuman(ctx, "kai", pw)
	require.NoError(t, err)
	sess, err := svc.Login(ctx, "kai", pw, "192.0.2.1")
	require.NoError(t, err)
	w := do(e, "GET", "/api/events", "Cookie", app.SessionCookie+"="+sess.Token)
	assert.Equal(t, 500, w.Code)
	assert.JSONEq(t, `{"error":{"code":"internal","message":"internal error"}}`, w.Body.String())
	assert.NotEqual(t, "text/event-stream", w.Header().Get("Content-Type"))
	assert.Empty(t, w.Header().Get("X-Accel-Buffering"))
}

// errorLine reports whether the log holds an ERROR line carrying text.
func errorLine(log, text string) bool {
	for _, line := range strings.Split(log, "\n") {
		if strings.Contains(line, `"level":"ERROR"`) && strings.Contains(line, text) {
			return true
		}
	}
	return false
}

// eventsFail is a repository whose event listing fails once broken is set.
type eventsFail struct {
	storage.Repository
	broken *atomic.Bool
}

func (r eventsFail) ListEventsForHuman(ctx context.Context, humanID string, after, limit int64) ([]storage.Event, error) {
	if r.broken.Load() {
		return nil, errors.New("disk on fire")
	}
	return r.Repository.ListEventsForHuman(ctx, humanID, after, limit)
}

// A Poll error that is not the request's own cancellation is logged and ends the stream.
func TestEvents_APollErrorIsLoggedAndEndsTheStream(t *testing.T) {
	var broken atomic.Bool
	r := newFaultyRig(t, app.Options{}, nil, func(repo storage.Repository) storage.Repository { return eventsFail{repo, &broken} }, nil)
	_, d := r.open(t, r.cookie, "")
	assert.Equal(t, "ready", next(t, d).Event)
	assert.NotContains(t, r.log.String(), `"level":"ERROR"`)
	broken.Store(true)
	r.post(t, "wake the handler so it polls")
	assert.True(t, ended(d))
	eventually(t, func() bool { return errorLine(r.log.String(), "disk on fire") }, "the poll error was logged")
	assert.Contains(t, r.log.String(), `"msg":"stream poll failed"`)
}

// sessionCheckFails is a service whose ResolveSession fails once broken is set.
type sessionCheckFails struct {
	bus.Service
	broken *atomic.Bool
}

func (s sessionCheckFails) ResolveSession(ctx context.Context, token string) (bus.Actor, error) {
	if s.broken.Load() {
		return bus.Actor{}, errors.New("disk on fire")
	}
	return s.Service.ResolveSession(ctx, token)
}

// At the heartbeat, a session check that fails for a reason other than the session
// having ended is logged; either way the stream ends.
func TestEvents_AFailedSessionCheckIsLoggedAndEndsTheStream(t *testing.T) {
	var broken atomic.Bool
	r := newFaultyRig(t, app.Options{HeartbeatInterval: 20 * time.Millisecond}, nil, nil, func(svc bus.Service) bus.Service { return sessionCheckFails{svc, &broken} })
	_, d := r.open(t, r.cookie, "")
	assert.Equal(t, "ready", next(t, d).Event)
	broken.Store(true)
	assert.True(t, ended(d))
	eventually(t, func() bool { return errorLine(r.log.String(), "disk on fire") }, "the session error was logged")
	assert.Contains(t, r.log.String(), `"msg":"stream session check failed"`)

	// A session that simply ended is not an error.
	r = newRig(t, app.Options{HeartbeatInterval: 20 * time.Millisecond}, nil)
	_, d = r.open(t, r.cookie, "")
	next(t, d)
	require.NoError(t, r.svc.Logout(ctx, r.token))
	assert.True(t, ended(d))
	assert.NotContains(t, r.log.String(), `"level":"ERROR"`)
}

// delivery is the shape of one delivery frame's data.
type delivery struct {
	Message struct {
		ID                ulid.ULID  `json:"id"`
		RoomID            ulid.ULID  `json:"room_id"`
		Author            struct {
			Kind  string    `json:"kind"`
			ID    ulid.ULID `json:"id"`
			Name  string    `json:"name"`
			IsBot bool      `json:"is_bot"`
		}
		Text              string     `json:"text"`
		ReplyToMessageID  *ulid.ULID `json:"reply_to_message_id"`
		ReplyToText       *string    `json:"reply_to_text"`
		ReplyToAuthorName *string    `json:"reply_to_author_name"`
		CreatedAt         time.Time  `json:"created_at"`
	}
	Room    map[string]any `json:"room"`
	Context []any          `json:"context"`
}

func TestAgentEvents_ReadyThenDelivery(t *testing.T) {
	r := newRig(t, app.Options{HeartbeatInterval: 20 * time.Millisecond}, nil)
	resp, d := r.openAgent(t, r.scoutToken, "")
	require.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))
	assert.Equal(t, "no-store", resp.Header.Get("Cache-Control"))
	assert.Equal(t, "no", resp.Header.Get("X-Accel-Buffering"))
	assert.Equal(t, sse.Frame{ID: "1", Event: "ready", Data: `{"cursor":1}`}, next(t, d))

	m := r.post(t, "after")
	f := next(t, d)
	assert.Equal(t, "delivery", f.Event)
	var first delivery
	require.NoError(t, json.Unmarshal([]byte(f.Data), &first))
	assert.Equal(t, m.ID, first.Message.ID)
	assert.Equal(t, r.room.ID, first.Message.RoomID)
	assert.Equal(t, "human", first.Message.Author.Kind)
	assert.Equal(t, r.kai.ID, first.Message.Author.ID)
	assert.Equal(t, "kai", first.Message.Author.Name)
	assert.False(t, first.Message.Author.IsBot)
	assert.Equal(t, "after", first.Message.Text)
	assert.Nil(t, first.Message.ReplyToMessageID)
	assert.Nil(t, first.Message.ReplyToText)
	assert.Nil(t, first.Message.ReplyToAuthorName)
	assert.Equal(t, r.room.ID.String(), first.Room["id"])
	assert.Empty(t, first.Context)

	// A reply carries the replied-to body and author to the agent.
	require.NoError(t, r.svc.TurnDone(ctx, r.scoutActor(), m.ID))
	rep, err := r.svc.PostMessage(ctx, r.kai, r.room.ID, "replying", m.ID)
	require.NoError(t, err)
	f = next(t, d)
	assert.Equal(t, "delivery", f.Event)
	var reply delivery
	require.NoError(t, json.Unmarshal([]byte(f.Data), &reply))
	assert.Equal(t, rep.ID, reply.Message.ID)
	require.NotNil(t, reply.Message.ReplyToMessageID)
	assert.Equal(t, m.ID, *reply.Message.ReplyToMessageID)
	require.NotNil(t, reply.Message.ReplyToText)
	assert.Equal(t, "after", *reply.Message.ReplyToText)
	require.NotNil(t, reply.Message.ReplyToAuthorName)
	assert.Equal(t, "kai", *reply.Message.ReplyToAuthorName)

	// Stop reaches the agent as a control event naming the room.
	require.NoError(t, r.svc.StopTurn(ctx, r.kai, r.room.ID, r.scout.ID))
	f = next(t, d)
	assert.Equal(t, "stop", f.Event)
	var stop struct{ RoomID string `json:"room_id"` }
	require.NoError(t, json.Unmarshal([]byte(f.Data), &stop))
	assert.Equal(t, r.room.ID.String(), stop.RoomID)

	// Steer names the room and carries the steer message; it needs a working turn.
	require.NoError(t, r.svc.TurnWorking(ctx, r.scoutActor(), rep.ID))
	steer, err := r.svc.Steer(ctx, r.kai, r.room.ID, r.scout.ID, "keep going")
	require.NoError(t, err)
	f = next(t, d)
	assert.Equal(t, "steer", f.Event)
	var str struct{ RoomID string `json:"room_id"`; Message map[string]any `json:"message"` }
	require.NoError(t, json.Unmarshal([]byte(f.Data), &str))
	assert.Equal(t, r.room.ID.String(), str.RoomID)
	assert.Equal(t, steer.ID.String(), str.Message["id"])

	assert.True(t, nothingElse(d, 150 * time.Millisecond), "nothing follows the steer")
}

func TestAgentEvents_RevokedWithinAHeartbeat(t *testing.T) {
	r := newRig(t, app.Options{HeartbeatInterval: 50 * time.Millisecond}, nil)
	_, d := r.openAgent(t, r.scoutToken, "")
	next(t, d) // ready
	require.NoError(t, r.svc.RevokeAgent(ctx, "scout"))
	assert.True(t, ended(d), "the stream closes within a couple of heartbeats")
	room, err := r.svc.GetRoom(ctx, r.kai, r.room.ID)
	require.NoError(t, err)
	assert.False(t, room.Agents[0].Connected, "closing on revocation flips presence off")
}

func TestAgentEvents_SecondConnectionReplacesTheFirst(t *testing.T) {
	r := newRig(t, app.Options{HeartbeatInterval: 20 * time.Millisecond}, nil)
	connected := func() bool {
		got, err := r.svc.GetRoom(ctx, r.kai, r.room.ID)
		return err == nil && got.Agents[0].Connected
	}
	assert.False(t, connected())

	resp1, d1 := r.openAgent(t, r.scoutToken, "")
	assert.Equal(t, 200, resp1.StatusCode)
	next(t, d1) // ready
	assert.True(t, connected(), "connected while the first stream is open")

	_, d2 := r.openAgent(t, r.scoutToken, "")
	assert.True(t, ended(d1), "the replaced stream ends")
	assert.True(t, connected(), "still connected after the first ends")
	next(t, d2) // second's ready

	m := r.post(t, "hello")
	f := next(t, d2)
	assert.Equal(t, "delivery", f.Event)
	assert.Contains(t, f.Data, m.ID.String())
	assert.True(t, connected(), "connected the whole time")
}

func TestAgentEvents_Cursor(t *testing.T) {
	r := newRig(t, app.Options{HeartbeatInterval: 20 * time.Millisecond}, nil)
	m1 := r.post(t, "one") // waiting: scout is offline, no stream yet
	connected := func() bool {
		got, err := r.svc.GetRoom(ctx, r.kai, r.room.ID)
		return err == nil && got.Agents[0].Connected
	}

	// Reconnect behind the log: the sweep releases m1 and the replay carries it.
	resp1, d1 := r.openAgent(t, r.scoutToken, "?cursor=0")
	next(t, d1) // ready
	f := next(t, d1)
	assert.Equal(t, "delivery", f.Event)
	assert.Contains(t, f.Data, m1.ID.String())

	// Close the first stream so scout is offline again, finish m1 to open the
	// gate, then queue m2.
	resp1.Body.Close()
	eventually(t, func() bool { return !connected() }, "the first stream closed")
	require.NoError(t, r.svc.TurnDone(ctx, r.scoutActor(), m1.ID))
	m2 := r.post(t, "two") // waiting

	// Reopen behind the log: both missed deliveries replay, in order.
	_, d2 := r.openAgent(t, r.scoutToken, "?cursor=0")
	next(t, d2)
	f1 := next(t, d2)
	f2 := next(t, d2)
	assert.Equal(t, "delivery", f1.Event)
	assert.Contains(t, f1.Data, m1.ID.String())
	assert.Equal(t, "delivery", f2.Event)
	assert.Contains(t, f2.Data, m2.ID.String())

	// A bogus cursor is refused.
	for _, bad := range []string{"?cursor=abc", "?cursor=-1", "?cursor=99999999999999999999"} {
		resp, _ := r.openAgent(t, r.scoutToken, bad)
		body, _ := io.ReadAll(resp.Body)
		assert.Equal(t, 400, resp.StatusCode, bad)
		assert.JSONEq(t, badReq("cursor must be a non-negative integer"), string(body), bad)
	}
}
