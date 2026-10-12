package app_test

import (
	"strings"
	"testing"

	"github.com/oklog/ulid/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/bus"
)

func TestNote_HumanRoutes(t *testing.T) {
	c := newChat(t)
	assert.JSONEq(t, `{"text":"","entries":[]}`, c.call(c.kai, "GET", "/api/note", "").Body.String())
	w := c.call(c.kai, "PUT", "/api/note", `{"text":" I live in Lisbon. "}`)
	require.Equal(t, 200, w.Code, w.Body.String())
	assert.Equal(t, "I live in Lisbon.", obj(t, w)["text"])
	assert.Equal(t, 400, c.call(c.kai, "PUT", "/api/note", `{"text":"`+strings.Repeat("a", 2001)+`"}`).Code)
	assert.Equal(t, 401, c.call("", "GET", "/api/note", "").Code)

	// scout suggests during a turn on kai's message
	kai, scout := bus.Actor{Kind: bus.ActorHuman, ID: c.kaiH.ID}, bus.Actor{Kind: bus.ActorAgent, ID: c.scout.ID}
	room, _, _ := c.svc.OpenDM(ctx, kai, c.scout.ID)
	sub, err := c.svc.Subscribe(ctx, scout, bus.FromNow)
	require.NoError(t, err)
	defer sub.Close()
	m, _ := c.svc.PostMessage(ctx, kai, room.ID, "hi", ulid.Zero)
	e, _, err := c.svc.SuggestNoteEntry(ctx, scout, m.ID, "prefers trains")
	require.NoError(t, err)

	entry := "/api/note/entries/" + e.ID.String()
	assert.JSONEq(t, notFound, c.call(c.ada, "PATCH", entry, `{"approved":true}`).Body.String())
	assert.JSONEq(t, `{"text":"","entries":[]}`, c.call(c.ada, "GET", "/api/note", "").Body.String(), "ada sees none of kai's note")
	w = c.call(c.kai, "PATCH", entry, `{"text":"prefers night trains","approved":true}`)
	require.Equal(t, 200, w.Code, w.Body.String())
	got := obj(t, w)["entries"].([]any)[0].(map[string]any)
	assert.Equal(t, "prefers night trains", got["text"])
	assert.Equal(t, true, got["approved"])
	assert.Equal(t, "scout", got["agent"].(map[string]any)["handle"])
	assert.Equal(t, 200, c.call(c.kai, "PATCH", entry, `{}`).Code, "an empty change is not an error")
	w = c.call(c.kai, "DELETE", entry, "")
	require.Equal(t, 200, w.Code)
	assert.Empty(t, obj(t, w)["entries"])
	assert.JSONEq(t, notFound, c.call(c.kai, "DELETE", entry, "").Body.String())
	assert.JSONEq(t, notFound, c.call(c.kai, "DELETE", "/api/note/entries/nope", "").Body.String())
}

func TestNote_AgentSuggests(t *testing.T) {
	r := newAgentRig(t)
	r.connect(t, r.scout.ID)
	m := r.kaiPost(t, r.room, "hi", ulid.Zero)
	target := "/agent/deliveries/" + m.ID.String() + "/note-entries"
	w := agentReq(r.e, r.scoutT, "POST", target, `{"text":"prefers trains"}`)
	require.Equal(t, 201, w.Code, w.Body.String())
	first := obj(t, w)
	assert.Equal(t, false, first["approved"])
	w = agentReq(r.e, r.scoutT, "POST", target, `{"text":"prefers trains"}`)
	assert.Equal(t, 200, w.Code, "a repeat is not created again")
	assert.Equal(t, first["id"], obj(t, w)["id"])
	assert.Equal(t, 400, agentReq(r.e, r.scoutT, "POST", target, `{"text":""}`).Code)
	assert.Equal(t, 404, agentReq(r.e, r.atlasT, "POST", target, `{"text":"x"}`).Code)
}