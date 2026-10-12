package app_test

import (
	"testing"

	"github.com/oklog/ulid/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/bus"
)

const promptClosed = `{"error":{"code":"prompt_closed","message":"the prompt is no longer open"}}`

func TestPrompts_AskAnswerAndExpire(t *testing.T) {
	r := newAgentRig(t)
	room := "/agent/rooms/" + r.room.ID.String()
	w := agentReq(r.e, r.scoutT, "POST", room+"/prompts", `{"kind":"approval","text":"run it?","choices":["once","deny"]}`)
	require.Equal(t, 201, w.Code, w.Body.String())
	m := obj(t, w)
	assert.JSONEq(t, `{"kind":"approval","choices":["once","deny"],"state":"open","answer":null,"answered_by":null,"closed_at":null}`,
		string(mustJSON(t, m["prompt"])))
	id := m["id"].(string)

	w = agentReq(r.e, r.scoutT, "POST", room+"/prompts", `{"kind":"clarify","text":"which?"}`)
	require.Equal(t, 201, w.Code, w.Body.String())
	assert.Equal(t, []any{}, obj(t, w)["prompt"].(map[string]any)["choices"])
	w = agentReq(r.e, r.scoutT, "POST", room+"/prompts", `{"kind":"poll","text":"x"}`)
	assert.Equal(t, 400, w.Code)
	assert.Contains(t, w.Body.String(), "kind must be approval or clarify")

	assert.Equal(t, 204, agentReq(r.e, r.scoutT, "POST", "/agent/prompts/"+id+"/expire", "").Code)
	assert.Equal(t, 204, agentReq(r.e, r.scoutT, "POST", "/agent/prompts/"+id+"/expire", "").Code)
	assert.Equal(t, 404, agentReq(r.e, r.atlasT, "POST", "/agent/prompts/"+id+"/expire", "").Code)
	assert.Equal(t, 404, agentReq(r.e, r.scoutT, "POST", "/agent/prompts/nope/expire", "").Code)
}

func TestPrompts_HumanAnswers(t *testing.T) {
	c := newChat(t)
	kai := bus.Actor{Kind: bus.ActorHuman, ID: c.kaiH.ID}
	room, _, err := c.svc.OpenDM(ctx, kai, c.scout.ID)
	require.NoError(t, err)
	scout := bus.Actor{Kind: bus.ActorAgent, ID: c.scout.ID}
	m, err := c.svc.PostPrompt(ctx, scout, room.ID, bus.PromptApproval, "run it?", []string{"once", "deny"})
	require.NoError(t, err)
	base := "/api/rooms/" + room.ID.String()
	assert.EqualValues(t, 1, obj(t, c.call(c.kai, "GET", base, ""))["open_prompts"])

	target := base + "/messages/" + m.ID.String() + "/answer"
	assert.Equal(t, 400, c.call(c.kai, "POST", target, `{"answer":"maybe"}`).Code)
	assert.JSONEq(t, notFound, c.call(c.ada, "POST", target, `{"answer":"once"}`).Body.String())
	w := c.call(c.kai, "POST", target, `{"answer":"once"}`)
	require.Equal(t, 200, w.Code, w.Body.String())
	p := obj(t, w)["prompt"].(map[string]any)
	assert.Equal(t, "answered", p["state"])
	assert.Equal(t, "once", p["answer"])
	assert.Equal(t, "kai", p["answered_by"].(map[string]any)["username"])
	assert.NotNil(t, p["closed_at"])
	assert.Equal(t, 200, c.call(c.kai, "POST", target, `{"answer":"once"}`).Code)
	w = c.call(c.kai, "POST", target, `{"answer":"deny"}`)
	assert.Equal(t, 409, w.Code)
	assert.JSONEq(t, promptClosed, w.Body.String())
	assert.EqualValues(t, 0, obj(t, c.call(c.kai, "GET", base, ""))["open_prompts"])

	// an ordinary message carries "prompt": null
	plain, _ := c.svc.PostMessage(ctx, kai, room.ID, "hi", ulid.Zero)
	got := obj(t, c.call(c.kai, "GET", base+"/messages/"+plain.ID.String(), ""))
	v, present := got["prompt"]
	assert.True(t, present)
	assert.Nil(t, v)
}