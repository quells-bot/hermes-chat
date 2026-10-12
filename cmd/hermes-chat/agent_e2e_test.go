// Package main: end-to-end coverage of stage 4 with a real server, a real CLI,
// a real human session, and the fake agent client speaking the agent API. These
// tests exercise the whole stage across "processes" the way the shipped server
// works: a CLI-created database, a running serve, and fakeagent driving the
// agent stream exactly like the Hermes plugin would.
package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/quells-bot/hermes-chat/internal/fakeagent"
	"github.com/quells-bot/hermes-chat/internal/sse"
)

// agentWorld is the end-to-end fixture: a running server, a logged-in human
// client, and scout's id and token from the CLI.
type agentWorld struct {
	api        string
	human      *http.Client
	scoutID    string
	scoutToken string
}

// newAgentWorld builds a migrated database, a human and scout via the CLI, a
// running server, and a logged-in human client.
func newAgentWorld(t *testing.T) agentWorld {
	t.Helper()
	migrated(t)
	t.Setenv("HERMES_CHAT_COOKIE_SECURE", "false")
	runCmdIn(t, pw+"\n", "user", "add", "kai")
	_, out, _ := runCmd(t, "agent", "add", "scout", "Scout")
	scoutToken := tokenLine.FindStringSubmatch(out)[1]
	api, _, _, _ := startServeWithEnv(t)
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	human := &http.Client{Jar: jar}
	require.Equal(t, 200, login(t, human, api, "kai", pw).StatusCode)
	_, agents := apiCall(t, human, "GET", api, "/api/agents", "")
	var scoutID string
	for _, a := range agents["agents"].([]any) {
		am := a.(map[string]any)
		if am["handle"].(string) == "scout" {
			scoutID = am["id"].(string)
		}
	}
	require.True(t, scoutID != "", "scout exists")
	return agentWorld{api: api, human: human, scoutID: scoutID, scoutToken: scoutToken}
}

// agent returns a fake Client speaking for the world's agent token.
func (w agentWorld) agent() *fakeagent.Client {
	return &fakeagent.Client{BaseURL: w.api, Token: w.scoutToken}
}

// openDM returns the human's DM with the given agent, creating it.
func openDM(t *testing.T, w agentWorld, agentID string) string {
	t.Helper()
	code, room := apiCall(t, w.human, "POST", w.api, "/api/dms", fmt.Sprintf(`{"agent_id":%q}`, agentID))
	require.Equal(t, 201, code)
	return room["id"].(string)
}

// agentID looks up one agent's id by handle.
func agentID(t *testing.T, w agentWorld, handle string) string {
	t.Helper()
	_, agents := apiCall(t, w.human, "GET", w.api, "/api/agents", "")
	for _, a := range agents["agents"].([]any) {
		am := a.(map[string]any)
		if am["handle"].(string) == handle {
			return am["id"].(string)
		}
	}
	t.Fatalf("no agent %q in the world", handle)
	return ""
}

// post has the human post a message into room and returns its id.
func post(t *testing.T, w agentWorld, room, text string) string {
	t.Helper()
	code, msg := apiCall(t, w.human, "POST", w.api, "/api/rooms/"+room+"/messages", fmt.Sprintf(`{"text":%q}`, text))
	require.Equal(t, 201, code)
	return msg["id"].(string)
}

// openAgentFor opens an agent's event stream for token and registers cleanup.
func openAgentFor(t *testing.T, api, token string) *fakeagent.Stream {
	t.Helper()
	c := &fakeagent.Client{BaseURL: api, Token: token}
	s, err := c.Open(context.Background(), "")
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	return s
}

// openAgent opens scout's event stream.
func openAgent(t *testing.T, w agentWorld) *fakeagent.Stream {
	return openAgentFor(t, w.api, w.scoutToken)
}

// nextAgent returns the next frame from an agent stream, failing after 5 seconds.
func nextAgent(t *testing.T, s *fakeagent.Stream) sse.Frame {
	t.Helper()
	type result struct {
		f   sse.Frame
		err error
	}
	got := make(chan result, 1)
	go func() {
		f, err := s.Next(context.Background())
		got <- result{f, err}
	}()
	select {
	case r := <-got:
		require.NoError(t, r.err, "reading the agent stream")
		return r.f
	case <-time.After(5 * time.Second):
		t.Fatal("no frame within 5 seconds")
		return sse.Frame{}
	}
}

// expectNothing requires the agent stream to stay quiet for ms.
func expectNothing(t *testing.T, s *fakeagent.Stream, ms time.Duration) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), ms)
	_, err := s.Next(ctx)
	cancel()
	require.Error(t, err, "expected no frame within the window")
}

// agentEnded reports whether the stream has ended, within 5 seconds.
func agentEnded(s *fakeagent.Stream) bool {
	done := make(chan error, 1)
	go func() {
		for {
			if _, err := s.Next(context.Background()); err != nil {
				done <- err
				return
			}
		}
	}()
	select {
	case <-done:
		return true
	case <-time.After(5 * time.Second):
		return false
	}
}

// deliveryStates maps each message id in the room to its scout delivery state.
func deliveryStates(t *testing.T, w agentWorld, room string) map[string]string {
	t.Helper()
	_, list := apiCall(t, w.human, "GET", w.api, "/api/rooms/"+room+"/messages", "")
	out := map[string]string{}
	for _, m := range list["messages"].([]any) {
		mm := m.(map[string]any)
		id := mm["id"].(string)
		state := "none"
		for _, d := range mm["deliveries"].([]any) {
			dm := d.(map[string]any)
			if dm["agent"].(map[string]any)["handle"].(string) == "scout" {
				state = dm["state"].(string)
			}
		}
		out[id] = state
	}
	return out
}

// drainHumanUntil reads human-stream frames until check matches, discarding the rest.
func drainHumanUntil(t *testing.T, d *sse.Decoder, check func(sse.Frame) bool) {
	t.Helper()
	for {
		f := nextEvent(t, d)
		if check(f) {
			return
		}
	}
}

func TestAgentE2E_QueueOneAtATimeWithCancel(t *testing.T) {
	w := newAgentWorld(t)
	room := openDM(t, w, w.scoutID)

	// scout is offline: m1, m2, m3 all wait.
	m1 := post(t, w, room, "m1")
	m2 := post(t, w, room, "m2")
	m3 := post(t, w, room, "m3")
	states := deliveryStates(t, w, room)
	require.Equal(t, "waiting", states[m1])
	require.Equal(t, "waiting", states[m2])
	require.Equal(t, "waiting", states[m3])

	// Cancel m2 while it waits: it can never reach the agent.
	code, _ := apiCall(t, w.human, "POST", w.api, "/api/rooms/"+room+"/messages/"+m2+"/cancel", "")
	require.Equal(t, 204, code)
	assert.Equal(t, "cancelled", deliveryStates(t, w, room)[m2])

	// Reconnect: the sweep hands scout m1 first, never the cancelled m2.
	s := openAgent(t, w)
	require.Equal(t, "ready", nextAgent(t, s).Event)
	f := nextAgent(t, s)
	require.Equal(t, "delivery", f.Event)
	assert.Contains(t, f.Data, m1)

	ctx := context.Background()
	require.NoError(t, w.agent().TurnWorking(ctx, m1))
	require.NoError(t, w.agent().TurnDone(ctx, m1))
	f = nextAgent(t, s)
	require.Equal(t, "delivery", f.Event)
	assert.Contains(t, f.Data, m3) // m2 is skipped, never delivered
	assert.NotContains(t, f.Data, m2)

	require.NoError(t, w.agent().TurnDone(ctx, m3))
	expectNothing(t, s, 500 * time.Millisecond)

	states = deliveryStates(t, w, room)
	assert.Equal(t, "done", states[m1])
	assert.Equal(t, "cancelled", states[m2])
	assert.Equal(t, "done", states[m3])
}

func TestAgentE2E_AddressRules(t *testing.T) {
	w := newAgentWorld(t)
	_, atlasOut, _ := runCmd(t, "agent", "add", "atlas", "Atlas")
	atlasToken := tokenLine.FindStringSubmatch(atlasOut)[1]
	atlas := agentID(t, w, "atlas")
	code, ch := apiCall(t, w.human, "POST", w.api, "/api/rooms",
		fmt.Sprintf(`{"topic":"Ops","agent_ids":[%q,%q]}`, w.scoutID, atlas))
	require.Equal(t, 201, code)
	room := ch["id"].(string)

	sScout := openAgent(t, w)
	nextAgent(t, sScout) // ready
	sAtlas := openAgentFor(t, w.api, atlasToken)
	nextAgent(t, sAtlas) // ready

	ctx := context.Background()

	// A human mention wakes only the named agent: scout gets it. Atlas's
	// silence is pinned below by ordering: atlas's very first delivery is the
	// agent-authored one, so none of these earlier messages reached it.
	m := post(t, w, room, "@scout run it")
	f := nextAgent(t, sScout)
	require.Equal(t, "delivery", f.Event)
	assert.Contains(t, f.Data, m)
	require.NoError(t, w.agent().TurnDone(ctx, m))

	// A plain message wakes nobody in a channel, and an agent never delivers to
	// itself. Scout's stream is still open, so any wrongful delivery to scout —
	// from the plain message or from its own @atlas post — would queue and the
	// terminal probe below would see it.
	post(t, w, room, "hello everyone")

	// An agent-author mention wakes only the named agent, never the author.
	scoutMsg, err := w.agent().PostMessage(ctx, room, "@atlas FYI", "")
	require.NoError(t, err)
	f = nextAgent(t, sAtlas)
	require.Equal(t, "delivery", f.Event)
	assert.Contains(t, f.Data, scoutMsg)

	// Scout received nothing after its one delivery: neither the plain message
	// nor its own mention.
	expectNothing(t, sScout, 400 * time.Millisecond)
}

func TestAgentE2E_TurnEditsAndActivity(t *testing.T) {
	w := newAgentWorld(t)
	room := openDM(t, w, w.scoutID)

	_, hum := openEvents(t, w.human, w.api, "")
	nextEvent(t, hum) // ready

	s := openAgent(t, w)
	nextAgent(t, s) // ready

	m := post(t, w, room, "run the report")
	f := nextAgent(t, s)
	require.Equal(t, "delivery", f.Event)
	assert.Contains(t, f.Data, m)

	ctx := context.Background()
	c := w.agent()
	require.NoError(t, c.TurnWorking(ctx, m))
	require.NoError(t, c.Remark(ctx, m, "digging in"))
	toolID, err := c.ToolStart(ctx, m, "web_search", "searching", "")
	require.NoError(t, err)
	require.NoError(t, c.ToolEnd(ctx, toolID, 123, true))

	replyID, err := c.PostMessage(ctx, room, "draft", m)
	require.NoError(t, err)
	for i := range 5 {
		require.NoError(t, c.EditMessage(ctx, replyID, fmt.Sprintf("draft %d", i), false))
	}
	require.NoError(t, c.EditMessage(ctx, replyID, "the final answer", true))
	require.NoError(t, c.TurnDone(ctx, m))

	// The human stream saw the activity, a live non-final edit, the reply, and
	// the finalize with the final text, in commit order.
	var (
		activity  bool
		liveEdit  bool
		replyPost bool
		finalEdit bool
		doneSeen  bool
	)
	for !doneSeen {
		f := nextEvent(t, hum)
		if f.Event == "activity.posted" {
			activity = true
		}
		if f.Event == "message.edited" {
			if strings.Contains(f.Data, `"final":false`) {
				liveEdit = true
			}
			if strings.Contains(f.Data, "the final answer") {
				finalEdit = true
			}
		}
		if f.Event == "message.posted" && strings.Contains(f.Data, replyID) {
			replyPost = true
		}
		if f.Event == "delivery.changed" && strings.Contains(f.Data, `"state":"done"`) && strings.Contains(f.Data, m) {
			doneSeen = true
		}
		t.Helper()
	}
	require.True(t, activity, "an activity.posted frame")
	require.True(t, liveEdit, "a live non-final edit frame")
	require.True(t, replyPost, "the agent reply's message.posted")
	require.True(t, finalEdit, "the finalize message.edited with the final text")

	// The reply row was finalized: a fresh GET returns the final text and the
	// agent author.
	code, msg := apiCall(t, w.human, "GET", w.api, "/api/rooms/"+room+"/messages/"+replyID, "")
	require.Equal(t, 200, code)
	assert.Equal(t, "the final answer", msg["text"].(string))
	assert.Equal(t, "agent", msg["author"].(map[string]any)["kind"].(string))

	// The feed shows m delivered done and the reply authored by the agent.
	_, list := apiCall(t, w.human, "GET", w.api, "/api/rooms/"+room+"/messages", "")
	var mState, replyKind string
	for _, raw := range list["messages"].([]any) {
		mm := raw.(map[string]any)
		if mm["id"].(string) == m {
			for _, d := range mm["deliveries"].([]any) {
				dm := d.(map[string]any)
				if dm["agent"].(map[string]any)["handle"].(string) == "scout" {
					mState = dm["state"].(string)
				}
			}
		}
		if mm["id"].(string) == replyID {
			replyKind = mm["author"].(map[string]any)["kind"].(string)
		}
	}
	assert.Equal(t, "done", mState)
	assert.Equal(t, "agent", replyKind)
}

func TestAgentE2E_PresenceAndReplacement(t *testing.T) {
	w := newAgentWorld(t)
	room := openDM(t, w, w.scoutID)
	connected := func() bool {
		_, r := apiCall(t, w.human, "GET", w.api, "/api/rooms/"+room, "")
		agents := r["agents"].([]any)
		return agents[0].(map[string]any)["connected"].(bool)
	}
	require.False(t, connected(), "offline before any stream")

	_, hum := openEvents(t, w.human, w.api, "")
	nextEvent(t, hum) // ready

	// A first stream connects scout and reports connected to the human.
	s1 := openAgent(t, w)
	nextAgent(t, s1) // ready
	require.True(t, connected(), "connected while the first stream is open")
	f := nextEvent(t, hum)
	require.Equal(t, "agent.presence", f.Event)
	assert.Contains(t, f.Data, `"connected":true`)

	// A second stream replaces the first without a presence flicker.
	s2 := openAgent(t, w)
	nextAgent(t, s2) // the replacement's ready
	require.True(t, agentEnded(s1), "the replaced stream ends")
	require.True(t, connected(), "still connected after the first ends")
	expectNoPresenceOff(t, hum)

	// Closing the streams takes scout offline again.
	s1.Close()
	s2.Close()
	post(t, w, room, "wake") // nudge the closed handler with a write it cannot do
	require.Eventually(t, func() bool { return !connected() }, 10 * time.Second, 50 * time.Millisecond, "presence-off after both streams close")
}

func TestAgentE2E_SteerAndStop(t *testing.T) {
	w := newAgentWorld(t)
	room := openDM(t, w, w.scoutID)
	_, hum := openEvents(t, w.human, w.api, "")
	nextEvent(t, hum) // ready
	s := openAgent(t, w)
	nextAgent(t, s) // ready

	m := post(t, w, room, "do the thing")
	f := nextAgent(t, s)
	require.Equal(t, "delivery", f.Event)
	ctx := context.Background()
	c := w.agent()
	require.NoError(t, c.TurnWorking(ctx, m))
	m2 := post(t, w, room, "then this") // waits behind m

	// The human steers: the message reaches the feed and the steer frame arrives.
	code, steer := apiCall(t, w.human, "POST", w.api, "/api/rooms/"+room+"/agents/"+w.scoutID+"/steer", `{"text":"do y instead"}`)
	require.Equal(t, 201, code)
	steerID := steer["id"].(string)
	drainHumanUntil(t, hum, func(f sse.Frame) bool {
		return f.Event == "message.posted" && strings.Contains(f.Data, steerID)
	})
	f = nextAgent(t, s)
	require.Equal(t, "steer", f.Event)
	assert.Contains(t, f.Data, room)
	assert.Contains(t, f.Data, steerID)

	// The human stops, and the stop frame arrives.
	code, _ = apiCall(t, w.human, "POST", w.api, "/api/rooms/"+room+"/agents/"+w.scoutID+"/stop", "")
	require.Equal(t, 204, code)
	f = nextAgent(t, s)
	require.Equal(t, "stop", f.Event)
	assert.Contains(t, f.Data, room)

	// Scout finishes m; the next queued message releases.
	require.NoError(t, c.TurnDone(ctx, m))
	f = nextAgent(t, s)
	require.Equal(t, "delivery", f.Event)
	assert.Contains(t, f.Data, m2)
}

// expectNoPresenceOff reads the human stream for a short while, failing if an
// agent.presence connected:false frame arrives (a replacement flicker).
func expectNoPresenceOff(t *testing.T, d *sse.Decoder) {
	t.Helper()
	type r struct {
		f   sse.Frame
		err error
	}
	got := make(chan r, 1)
	go func() {
		for {
			f, err := d.Next()
			if f.Comment != "" && f.Event == "" && f.Data == "" && f.ID == "" {
				continue
			}
			got <- r{f, err}
			return
		}
	}()
	select {
	case x := <-got:
		if x.f.Event == "agent.presence" && strings.Contains(x.f.Data, `"connected":false`) {
			t.Fatal("presence flicker: a connected:false frame arrived")
		}
	case <-time.After(600 * time.Millisecond):
		// quiet: no flicker
	}
}