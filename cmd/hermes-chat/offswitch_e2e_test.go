// Package main: end-to-end coverage of stage 5, the off switch, with a real
// server and the fake agent client: the pause holds against an agent that
// ignores it, stop everything lifts back to each room's own state, and the
// turn cap stops a ping-pong between two agents.
package main

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/quells-bot/hermes-chat/internal/fakeagent"
	"github.com/quells-bot/hermes-chat/internal/sse"
)

// frameOf reads agent frames until one has the wanted event, skipping the others.
func frameOf(t *testing.T, s *fakeagent.Stream, event string) sse.Frame {
	t.Helper()
	for {
		if f := nextAgent(t, s); f.Event == event {
			return f
		}
	}
}

// The requirement of the stage: the pause holds against an agent that ignores it.
func TestOffSwitchE2E_PauseHoldsAgainstAnAgentThatIgnoresIt(t *testing.T) {
	w := newAgentWorld(t)
	room := openDM(t, w, w.scoutID)
	scout, stream := w.agent(), openAgent(t, w)
	bg := context.Background()
	m1 := post(t, w, room, "start")
	frameOf(t, stream, "delivery")
	require.NoError(t, scout.TurnWorking(bg, m1))
	reply, err := scout.PostMessage(bg, room, "thinking", "")
	require.NoError(t, err)

	code, body := apiCall(t, w.human, "POST", w.api, "/api/rooms/"+room+"/pause", "")
	require.Equal(t, 200, code)
	assert.Equal(t, true, body["paused"])
	assert.JSONEq(t, fmt.Sprintf(`{"room_id":%q}`, room), frameOf(t, stream, "pause").Data)

	// the agent carries on regardless
	_, err = scout.PostMessage(bg, room, "still talking", "")
	assert.ErrorContains(t, err, "409 paused")
	assert.ErrorContains(t, scout.EditMessage(bg, reply, "still typing", true), "409 paused")
	assert.ErrorContains(t, scout.Remark(bg, m1, "still working"), "409 paused")
	m2 := post(t, w, room, "queued behind the pause")
	require.NoError(t, scout.TurnDone(bg, m1))
	expectNothing(t, stream, 300*time.Millisecond)
	assert.Equal(t, "waiting", deliveryStates(t, w, room)[m2])
	_, list := apiCall(t, w.human, "GET", w.api, "/api/rooms/"+room+"/messages", "")
	assert.Len(t, list["messages"].([]any), 3, "nothing from the agent got in after the pause")
}
func TestOffSwitchE2E_StopEverythingAndLift(t *testing.T) {
	w := newAgentWorld(t)
	dm := openDM(t, w, w.scoutID)
	code, conv := apiCall(t, w.human, "POST", w.api, "/api/rooms", fmt.Sprintf(`{"topic":"Trip","agent_ids":[%q]}`, w.scoutID))
	require.Equal(t, 201, code)
	convID := conv["id"].(string)
	code, _ = apiCall(t, w.human, "POST", w.api, "/api/rooms/"+convID+"/pause", "") // paused on its own first
	require.Equal(t, 200, code)

	code, halt := apiCall(t, w.human, "POST", w.api, "/api/halt", "")
	require.Equal(t, 200, code)
	assert.Equal(t, true, halt["halted"])
	inDM, inConv := post(t, w, dm, "held"), post(t, w, convID, "held too")
	stream := openAgent(t, w) // connecting while everything is stopped releases nothing
	assert.Equal(t, "ready", nextAgent(t, stream).Event)
	assert.Equal(t, "waiting", deliveryStates(t, w, dm)[inDM])
	_, rooms := apiCall(t, w.human, "GET", w.api, "/api/rooms", "")
	for _, r := range rooms["rooms"].([]any) {
		assert.Equal(t, true, r.(map[string]any)["halted"])
	}

	code, halt = apiCall(t, w.human, "DELETE", w.api, "/api/halt", "")
	require.Equal(t, 200, code)
	assert.Equal(t, false, halt["halted"])
	f := frameOf(t, stream, "delivery")
	assert.Contains(t, f.Data, inDM)
	assert.Equal(t, "waiting", deliveryStates(t, w, convID)[inConv], "the room paused before the halt is still paused")

	code, _ = apiCall(t, w.human, "POST", w.api, "/api/rooms/"+convID+"/resume", "")
	require.Equal(t, 200, code)
	assert.Contains(t, frameOf(t, stream, "delivery").Data, inConv)
}

func TestOffSwitchE2E_TurnCapStopsAPingPong(t *testing.T) {
	w := newAgentWorld(t)
	_, out, _ := runCmd(t, "agent", "add", "atlas", "Atlas") // the CLI may run beside the server
	atlasToken := tokenLine.FindStringSubmatch(out)[1]
	atlasID := agentID(t, w, "atlas")
	code, room := apiCall(t, w.human, "POST", w.api, "/api/rooms", fmt.Sprintf(`{"topic":"Debate","agent_ids":[%q,%q]}`, w.scoutID, atlasID))
	require.Equal(t, 201, code)
	roomID := room["id"].(string)
	code, _ = apiCall(t, w.human, "PATCH", w.api, "/api/rooms/"+roomID, `{"turn_cap":2}`)
	require.Equal(t, 200, code)

	bg := context.Background()
	scout, atlas := w.agent(), &fakeagent.Client{BaseURL: w.api, Token: atlasToken}
	scoutStream, atlasStream := openAgent(t, w), openAgentFor(t, w.api, atlasToken)

	// kai starts it; from then on each agent answers by mentioning the other
	kicked := post(t, w, roomID, "@scout debate @atlas")
	frameOf(t, scoutStream, "delivery")
	frameOf(t, atlasStream, "delivery")
	require.NoError(t, atlas.TurnDone(bg, kicked))
	require.NoError(t, scout.TurnDone(bg, kicked))
	one, err := scout.PostMessage(bg, roomID, "@atlas one", "") // agent-woken turn 1
	require.NoError(t, err)
	assert.Contains(t, frameOf(t, atlasStream, "delivery").Data, one)
	require.NoError(t, atlas.TurnDone(bg, one))                 // each turn ends before the next, so nothing queues behind it
	two, err := atlas.PostMessage(bg, roomID, "@scout two", "") // agent-woken turn 2
	require.NoError(t, err)
	assert.Contains(t, frameOf(t, scoutStream, "delivery").Data, two)
	require.NoError(t, scout.TurnDone(bg, two))
	three, err := scout.PostMessage(bg, roomID, "@atlas three", "") // would be turn 3: accepted, not delivered
	require.NoError(t, err)

	assert.JSONEq(t, fmt.Sprintf(`{"room_id":%q}`, roomID), frameOf(t, scoutStream, "pause").Data)
	frameOf(t, atlasStream, "pause")
	_, got := apiCall(t, w.human, "GET", w.api, "/api/rooms/"+roomID, "")
	assert.Equal(t, true, got["paused"])
	assert.Equal(t, "turn_cap", got["pause_reason"])
	_, err = atlas.PostMessage(bg, roomID, "@scout four", "")
	assert.ErrorContains(t, err, "409 paused")
	_, list := apiCall(t, w.human, "GET", w.api, "/api/rooms/"+roomID+"/messages", "")
	found := false
	for _, m := range list["messages"].([]any) {
		if mm := m.(map[string]any); mm["id"] == three {
			found = true
			assert.Equal(t, "waiting", mm["deliveries"].([]any)[0].(map[string]any)["state"])
		}
	}
	require.True(t, found, "message three is in the list")
}
