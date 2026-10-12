package app_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/oklog/ulid/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/app"
	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/bus"
)

const forbidden = `{"error":{"code":"forbidden","message":"not allowed"}}`

// agentEngine is newEngine plus the agent group: AgentAuth and the nine agent
// routes, mirroring routes.go. It returns the engine wired to svc.
func agentEngine(t *testing.T, svc bus.Service) *gin.Engine {
	t.Helper()
	e, _ := newEngine(t, svc)
	ctrl := app.NewController(svc, app.Options{})
	agent := e.Group("/agent", ctrl.AgentAuth)
	agent.GET("/rooms", ctrl.AgentRooms)
	agent.POST("/rooms/:room/messages", ctrl.AgentPostMessage)
	agent.PUT("/messages/:message", ctrl.AgentEditMessage)
	agent.POST("/deliveries/:message/working", ctrl.AgentTurnWorking)
	agent.POST("/deliveries/:message/done", ctrl.AgentTurnDone)
	agent.POST("/deliveries/:message/failed", ctrl.AgentTurnFailed)
	agent.POST("/deliveries/:message/remarks", ctrl.AgentRemark)
	agent.POST("/deliveries/:message/tools", ctrl.AgentStartTool)
	agent.PUT("/activities/:activity", ctrl.AgentFinishTool)
	return e
}

// agentRig is agentEngine plus a real service: human kai, two agents with live
// bearer tokens, and kai's DM with each agent.
type agentRig struct {
	e         *gin.Engine
	svc       bus.Service
	kai       bus.Human
	scout     bus.Agent
	scoutT    string
	atlas     bus.Agent
	atlasT    string
	room      bus.Room // kai's DM with scout
	atlasRoom bus.Room // kai's DM with atlas
}

func newAgentRig(t *testing.T) agentRig {
	t.Helper()
	svc := realSvc(t)
	e := agentEngine(t, svc)
	r := agentRig{e: e, svc: svc}
	var err error
	r.kai, err = svc.CreateHuman(ctx, "kai", pw)
	require.NoError(t, err)
	r.scout, r.scoutT, err = svc.CreateAgent(ctx, "scout", "Scout")
	require.NoError(t, err)
	r.atlas, r.atlasT, err = svc.CreateAgent(ctx, "atlas", "Atlas")
	require.NoError(t, err)
	r.room, _, err = svc.OpenDM(ctx, bus.Actor{Kind: bus.ActorHuman, ID: r.kai.ID}, r.scout.ID)
	require.NoError(t, err)
	r.atlasRoom, _, err = svc.OpenDM(ctx, bus.Actor{Kind: bus.ActorHuman, ID: r.kai.ID}, r.atlas.ID)
	require.NoError(t, err)
	return r
}

// connect opens the agent's stream so its deliveries are released on arrival.
// The returned subscription closes it; closing it early takes the agent offline.
func (r *agentRig) connect(t *testing.T, agentID ulid.ULID) *bus.Subscription {
	t.Helper()
	sub, err := r.svc.Subscribe(ctx, bus.Actor{Kind: bus.ActorAgent, ID: agentID}, bus.FromNow)
	require.NoError(t, err)
	t.Cleanup(sub.Close)
	return sub
}

// kaiPost posts text into the room as kai, returning the message.
func (r *agentRig) kaiPost(t *testing.T, room bus.Room, text string, replyTo ulid.ULID) bus.Message {
	t.Helper()
	m, err := r.svc.PostMessage(ctx, bus.Actor{Kind: bus.ActorHuman, ID: r.kai.ID}, room.ID, text, replyTo)
	require.NoError(t, err)
	return m
}

// agentReq performs a request as the agent bearer token, with an optional JSON
// body ("" sends none).
func agentReq(e *gin.Engine, token, method, target, body string) *httptest.ResponseRecorder {
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	r := httptest.NewRequest(method, target, rd)
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, r)
	return w
}

func TestAgentRooms_MineOnly(t *testing.T) {
	r := newAgentRig(t)

	w := do(r.e, "GET", "/agent/rooms", "Authorization", "Bearer "+r.scoutT)
	require.Equal(t, 200, w.Code)
	rooms := obj(t, w)["rooms"].([]any)
	require.Len(t, rooms, 1)
	room := rooms[0].(map[string]any)
	assert.Equal(t, r.room.ID.String(), room["id"])
	assert.Equal(t, "dm", room["flavor"])
	assert.Equal(t, "", room["topic"])
	humans := room["humans"].([]any)
	require.Len(t, humans, 1)
	assert.Equal(t, "kai", humans[0].(map[string]any)["username"])

	// a human session cookie is not an agent credential
	w = do(r.e, "GET", "/agent/rooms", "Cookie", app.SessionCookie+"=session")
	assert.Equal(t, 401, w.Code)

	// an agent in no rooms sees an empty list
	_, nobodyT, err := r.svc.CreateAgent(ctx, "nobody", "Nobody")
	require.NoError(t, err)
	w = do(r.e, "GET", "/agent/rooms", "Authorization", "Bearer "+nobodyT)
	assert.Equal(t, 200, w.Code)
	assert.JSONEq(t, `{"rooms":[]}`, w.Body.String())
}

func TestAgentPostMessage(t *testing.T) {
	r := newAgentRig(t)
	post := func(body string) *httptest.ResponseRecorder {
		return agentReq(r.e, r.scoutT, "POST", "/agent/rooms/"+r.room.ID.String()+"/messages", body)
	}

	w := post(`{"text":"all aboard","reply_to_id":null}`)
	require.Equal(t, 201, w.Code)
	m := obj(t, w)
	assert.Equal(t, r.room.ID.String(), m["room_id"])
	assert.Equal(t, "all aboard", m["text"])
	author := m["author"].(map[string]any)
	assert.Equal(t, "agent", author["kind"])
	assert.Equal(t, "Scout", author["name"]) // an agent's author name is its display name

	// blank text → 400 validation
	w = post(`{"text":"   ","reply_to_id":null}`)
	require.Equal(t, 400, w.Code)
	assert.JSONEq(t, `{"error":{"code":"validation","message":"message text must be 1 to 262144 bytes, not blank, with no NUL character"}}`, w.Body.String())

	// a reply_to naming a message in another room → 400 validation
	other := r.kaiPost(t, r.atlasRoom, "secrets", ulid.Zero)
	w = post(fmt.Sprintf(`{"text":"hi","reply_to_id":%q}`, other.ID.String()))
	require.Equal(t, 400, w.Code)
	assert.JSONEq(t, `{"error":{"code":"validation","message":"reply_to must be a message in this room"}}`, w.Body.String())

	// into a room scout is not in → 404
	w = agentReq(r.e, r.scoutT, "POST", "/agent/rooms/"+r.atlasRoom.ID.String()+"/messages", `{"text":"intrusion"}`)
	require.Equal(t, 404, w.Code)
	assert.JSONEq(t, notFound, w.Body.String())

	// a revoked token → 401
	require.NoError(t, r.svc.RevokeAgent(ctx, "scout"))
	w = post(`{"text":"ignored"}`)
	assert.Equal(t, 401, w.Code)
}

func TestAgentEditMessage(t *testing.T) {
	r := newAgentRig(t)

	// scout posts a message it can then edit
	w := agentReq(r.e, r.scoutT, "POST", "/agent/rooms/"+r.room.ID.String()+"/messages", `{"text":"first draft","reply_to_id":null}`)
	require.Equal(t, 201, w.Code)
	id := obj(t, w)["id"].(string)

	edit := func(fin string) *httptest.ResponseRecorder {
		return agentReq(r.e, r.scoutT, "PUT", "/agent/messages/"+id, `{"text":"final copy","final":`+fin+`}`)
	}
	assert.Equal(t, 204, edit("true").Code)
	// a second final edit is idempotent
	assert.Equal(t, 204, edit("true").Code)

	// another agent's message → 403
	w = agentReq(r.e, r.atlasT, "POST", "/agent/rooms/"+r.atlasRoom.ID.String()+"/messages", `{"text":"atlas says"}`)
	require.Equal(t, 201, w.Code)
	atlasID := obj(t, w)["id"].(string)
	w = agentReq(r.e, r.scoutT, "PUT", "/agent/messages/"+atlasID, `{"text":"hijack","final":true}`)
	require.Equal(t, 403, w.Code)
	assert.JSONEq(t, forbidden, w.Body.String())

	// a missing message → 404
	w = agentReq(r.e, r.scoutT, "PUT", "/agent/messages/"+ulid.Make().String(), `{"text":"x","final":true}`)
	require.Equal(t, 404, w.Code)
	assert.JSONEq(t, notFound, w.Body.String())
}

func TestAgentTurnReports(t *testing.T) {
	r := newAgentRig(t)
	report := func(state, id string) *httptest.ResponseRecorder {
		return do(r.e, "POST", "/agent/deliveries/"+id+"/"+state, "Authorization", "Bearer "+r.scoutT)
	}

	// a message with no delivery for scout → 404
	w := report("working", ulid.Make().String())
	require.Equal(t, 404, w.Code)
	assert.JSONEq(t, notFound, w.Body.String())

	// connected, kai's posts are released as delivered
	r.connect(t, r.scout.ID)
	working := r.kaiPost(t, r.room, "work on this", ulid.Zero)
	assert.Equal(t, 204, report("working", working.ID.String()).Code)
	assert.Equal(t, 204, report("done", working.ID.String()).Code) // release the head

	done := r.kaiPost(t, r.room, "just do it", ulid.Zero)
	assert.Equal(t, 204, report("done", done.ID.String()).Code)

	failed := r.kaiPost(t, r.room, "blow it up", ulid.Zero)
	assert.Equal(t, 204, report("failed", failed.ID.String()).Code)

	// offline, kai's post stays waiting, so done finds no turn → 409
	r.connect(t, r.scout.ID).Close() // scout drops off
	waiting := r.kaiPost(t, r.room, "you missed this", ulid.Zero)
	w = report("done", waiting.ID.String())
	require.Equal(t, 409, w.Code)
	assert.JSONEq(t, `{"error":{"code":"no_turn","message":"no turn is running"}}`, w.Body.String())
}

func TestAgentActivity(t *testing.T) {
	r := newAgentRig(t)
	r.connect(t, r.scout.ID)
	m := r.kaiPost(t, r.room, "your turn", ulid.Zero)
	msg := "/agent/deliveries/" + m.ID.String() + "/"

	// remark → 201 with the activity body
	w := agentReq(r.e, r.scoutT, "POST", msg+"remarks", `{"text":"thinking about it"}`)
	require.Equal(t, 201, w.Code)
	remark := obj(t, w)
	remarkID := remark["id"].(string)
	assert.Equal(t, "remark", remark["kind"])
	assert.Equal(t, "thinking about it", remark["text"])
	assert.Equal(t, m.ID.String(), remark["message_id"])
	agent := remark["agent"].(map[string]any)
	assert.Equal(t, r.scout.ID.String(), agent["id"])
	assert.Equal(t, "scout", agent["handle"])

	// tool start → 201 with the tool fields
	w = agentReq(r.e, r.scoutT, "POST", msg+"tools", `{"tool":"web_search","preview":"searching…","arguments":"{\"q\":\"x\"}"}`)
	require.Equal(t, 201, w.Code)
	tool := obj(t, w)
	assert.Equal(t, "tool", tool["kind"])
	assert.Equal(t, "web_search", tool["tool"])
	assert.Equal(t, "searching…", tool["preview"])
	assert.Equal(t, `{"q":"x"}`, tool["arguments"])
	assert.Equal(t, m.ID.String(), tool["message_id"])
	assert.Nil(t, tool["duration_ms"], "a running tool has no result yet")
	assert.Nil(t, tool["succeeded"])
	toolID := tool["id"].(string)

	// finish → 200 with duration/succeeded set
	w = agentReq(r.e, r.scoutT, "PUT", "/agent/activities/"+toolID, `{"duration_ms":123,"succeeded":true}`)
	require.Equal(t, 200, w.Code)
	finished := obj(t, w)
	assert.Equal(t, float64(123), finished["duration_ms"])
	assert.Equal(t, true, finished["succeeded"])

	// finishing a remark → 404
	w = agentReq(r.e, r.scoutT, "PUT", "/agent/activities/"+remarkID, `{"duration_ms":1,"succeeded":true}`)
	require.Equal(t, 404, w.Code)
	assert.JSONEq(t, notFound, w.Body.String())

	// another agent's activity → 403
	r.connect(t, r.atlas.ID)
	am := r.kaiPost(t, r.atlasRoom, "atlas's turn", ulid.Zero)
	at := agentReq(r.e, r.atlasT, "POST", "/agent/deliveries/"+am.ID.String()+"/tools", `{"tool":"probe"}`)
	require.Equal(t, 201, at.Code)
	atlasToolID := obj(t, at)["id"].(string)
	w = agentReq(r.e, r.scoutT, "PUT", "/agent/activities/"+atlasToolID, `{"duration_ms":9,"succeeded":true}`)
	require.Equal(t, 403, w.Code)
	assert.JSONEq(t, forbidden, w.Body.String())

	// negative duration → 400 validation
	w = agentReq(r.e, r.scoutT, "PUT", "/agent/activities/"+toolID, `{"duration_ms":-1,"succeeded":true}`)
	require.Equal(t, 400, w.Code)
	assert.JSONEq(t, `{"error":{"code":"validation","message":"duration_ms must not be negative"}}`, w.Body.String())
}

// Agent bodies that carry long text (messages, remarks, tool arguments) are read
// with the message-size limit, not maxBody, so their bus-valid lengths are reachable.
func TestAgentLargeBodies(t *testing.T) {
	r := newAgentRig(t)
	big := strings.Repeat("x", 8192)

	r.connect(t, r.scout.ID)
	m := r.kaiPost(t, r.room, "big job", ulid.Zero)

	// a tool start whose arguments are far larger than maxBody → 201
	w := agentReq(r.e, r.scoutT, "POST", "/agent/deliveries/"+m.ID.String()+"/tools",
		`{"tool":"write_file","preview":"writing","arguments":"`+big+`"}`)
	require.Equal(t, 201, w.Code)
	assert.Equal(t, big, obj(t, w)["arguments"])

	// a remark over maxBody → 201
	w = agentReq(r.e, r.scoutT, "POST", "/agent/deliveries/"+m.ID.String()+"/remarks", `{"text":"`+big+`"}`)
	require.Equal(t, 201, w.Code)
	assert.Equal(t, big, obj(t, w)["text"])

	// an edit whose text exceeds maxBody → 204
	e := agentReq(r.e, r.scoutT, "POST", "/agent/rooms/"+r.room.ID.String()+"/messages", `{"text":"draft","reply_to_id":null}`)
	require.Equal(t, 201, e.Code)
	id := obj(t, e)["id"].(string)
	w = agentReq(r.e, r.scoutT, "PUT", "/agent/messages/"+id, `{"text":"`+big+`","final":true}`)
	require.Equal(t, 204, w.Code)
}

func TestAgentRoutes_RequireTheToken(t *testing.T) {
	r := newAgentRig(t)
	id := ulid.Make().String()
	cases := []struct{ method, path string }{
		{"GET", "/agent/rooms"},
		{"POST", "/agent/rooms/" + id + "/messages"},
		{"PUT", "/agent/messages/" + id},
		{"POST", "/agent/deliveries/" + id + "/working"},
		{"POST", "/agent/deliveries/" + id + "/done"},
		{"POST", "/agent/deliveries/" + id + "/failed"},
		{"POST", "/agent/deliveries/" + id + "/remarks"},
		{"POST", "/agent/deliveries/" + id + "/tools"},
		{"PUT", "/agent/activities/" + id},
	}
	for _, tc := range cases {
		w := do(r.e, tc.method, tc.path)
		require.Equal(t, 401, w.Code, "%s %s", tc.method, tc.path)
		assert.JSONEq(t, `{"error":{"code":"unauthenticated","message":"authentication required"}}`, w.Body.String(), "%s %s", tc.method, tc.path)
	}
}

func TestAgentRoutes_PausedRoom(t *testing.T) {
	r := newAgentRig(t)
	kai := bus.Actor{Kind: bus.ActorHuman, ID: r.kai.ID}
	r.connect(t, r.scout.ID)
	m := r.kaiPost(t, r.room, "go", ulid.Zero)
	_, err := r.svc.PauseRoom(ctx, kai, r.room.ID)
	require.NoError(t, err)

	w := agentReq(r.e, r.scoutT, "POST", "/agent/rooms/"+r.room.ID.String()+"/messages", `{"text":"hi"}`)
	assert.Equal(t, 409, w.Code)
	assert.JSONEq(t, pausedBody, w.Body.String())
	w = agentReq(r.e, r.scoutT, "POST", "/agent/deliveries/"+m.ID.String()+"/remarks", `{"text":"hm"}`)
	assert.Equal(t, 409, w.Code)
	assert.Equal(t, 204, agentReq(r.e, r.scoutT, "POST", "/agent/deliveries/"+m.ID.String()+"/done", "").Code)

	w = agentReq(r.e, r.scoutT, "GET", "/agent/rooms", "")
	var body struct{ Rooms []map[string]any }
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Len(t, body.Rooms, 1)
	assert.Equal(t, true, body.Rooms[0]["paused"])
}
