package app_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/oklog/ulid/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/app"
	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/bus"
)

// chat is apiEngine plus two logged-in humans and two agents.
type chat struct {
	e            *gin.Engine
	svc          bus.Service
	kai, ada     string // Cookie header values, "hermes_session=…"
	kaiH, adaH   bus.Human
	scout, atlas bus.Agent
}

func newChat(t *testing.T) chat {
	t.Helper()
	e, _, svc := apiEngine(t, false)
	c := chat{e: e, svc: svc}
	var err error
	login := func(name string) (bus.Human, string) {
		h, err := svc.CreateHuman(ctx, name, pw)
		require.NoError(t, err)
		sess, err := svc.Login(ctx, name, pw, "192.0.2.1")
		require.NoError(t, err)
		return h, app.SessionCookie + "=" + sess.Token
	}
	c.kaiH, c.kai = login("kai")
	c.adaH, c.ada = login("ada")
	c.scout, _, err = svc.CreateAgent(ctx, "scout", "Scout")
	require.NoError(t, err)
	c.atlas, _, err = svc.CreateAgent(ctx, "atlas", "Atlas")
	require.NoError(t, err)
	return c
}

// call sends a request as the human whose Cookie header is given, with Origin https://example.com.
// An empty cookie sends none; an empty body sends none.
func (c chat) call(cookie, method, target, body string) *httptest.ResponseRecorder {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, target, nil)
	} else {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	r.Header.Set("Origin", "https://example.com")
	if cookie != "" {
		r.Header.Set("Cookie", cookie)
	}
	w := httptest.NewRecorder()
	c.e.ServeHTTP(w, r)
	return w
}

// obj decodes a JSON object response.
func obj(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &m), w.Body.String())
	return m
}

func stamp(at time.Time) string { return at.Format(time.RFC3339Nano) } // how encoding/json writes a time

const notFound = `{"error":{"code":"not_found","message":"not found"}}`

func badReq(message string) string {
	return `{"error":{"code":"bad_request","message":"` + message + `"}}`
}

func TestRooms_CreateGetAndList(t *testing.T) {
	c := newChat(t)
	w := c.call(c.kai, "POST", "/api/rooms", `{"topic":" Trip planning ","agent_ids":["`+c.scout.ID.String()+`"],"extra":1}`)
	require.Equal(t, 201, w.Code, w.Body.String())
	id := obj(t, w)["id"].(string)
	rooms, _ := c.svc.ListRooms(ctx, bus.Actor{Kind: bus.ActorHuman, ID: c.kaiH.ID})
	require.Len(t, rooms, 1)
	want := fmt.Sprintf(`{"id":%q,"flavor":"conversation","topic":"Trip planning","archived":false,"created_at":%q,
        "humans":[{"id":%q,"username":"kai"}],
        "agents":[{"id":%q,"handle":"scout","display_name":"Scout","revoked":false}],
        "last_message_id":null,"last_read_message_id":null}`, id, stamp(rooms[0].CreatedAt), c.kaiH.ID, c.scout.ID)
	assert.JSONEq(t, want, w.Body.String())
	assert.JSONEq(t, want, c.call(c.kai, "GET", "/api/rooms/"+id, "").Body.String())
	assert.JSONEq(t, `{"rooms":[`+want+`]}`, c.call(c.kai, "GET", "/api/rooms", "").Body.String())
	assert.JSONEq(t, `{"rooms":[]}`, c.call(c.ada, "GET", "/api/rooms", "").Body.String()) // an empty array, not null
}

func TestRooms_BadBodies(t *testing.T) {
	c := newChat(t)
	room := obj(t, c.call(c.kai, "POST", "/api/rooms", `{"topic":"T","agent_ids":["`+c.scout.ID.String()+`"]}`))["id"].(string)
	object := "request body must be a JSON object"
	cases := []struct{ method, target, body, message string }{
		{"POST", "/api/rooms", ``, object}, {"POST", "/api/rooms", `not json`, object}, {"POST", "/api/rooms", `[]`, object},
		{"POST", "/api/rooms", `{"topic":5}`, object}, {"POST", "/api/rooms", `{"topic":"` + strings.Repeat("a", 5000) + `"}`, object},
		{"POST", "/api/rooms", `{"topic":"T","agent_ids":["nope"]}`, "agent_ids must be a list of ids"},
		{"POST", "/api/rooms", `{"topic":"T","agent_ids":"` + c.scout.ID.String() + `"}`, object},
		{"POST", "/api/dms", `{}`, "agent_id must be an id"}, {"POST", "/api/dms", `{"agent_id":"nope"}`, "agent_id must be an id"},
		{"PATCH", "/api/rooms/" + room, `{"archived":"yes"}`, object},
		{"POST", "/api/rooms/" + room + "/humans", `{}`, "human_id must be an id"},
		{"POST", "/api/rooms/" + room + "/agents", `{"agent_id":""}`, "agent_id must be an id"},
		{"PUT", "/api/rooms/" + room + "/read", `{"message_id":"nope"}`, "message_id must be an id"},
		{"POST", "/api/rooms/" + room + "/messages", `{"text":"hi","reply_to_id":"nope"}`, "reply_to_id must be an id"},
		{"POST", "/api/rooms/" + room + "/messages", `{"text":"` + strings.Repeat("a", 1<<20) + `"}`, object},
	}
	for _, tc := range cases {
		w := c.call(c.kai, tc.method, tc.target, tc.body)
		assert.Equal(t, 400, w.Code, "%s %s %.30q", tc.method, tc.target, tc.body)
		assert.JSONEq(t, badReq(tc.message), w.Body.String(), "%s %s %.30q", tc.method, tc.target, tc.body)
	}
	w := c.call(c.kai, "POST", "/api/rooms", `{"topic":"","agent_ids":["`+c.scout.ID.String()+`"]}`) // well-formed, refused by the bus
	assert.Equal(t, 400, w.Code)
	assert.JSONEq(t, `{"error":{"code":"validation","message":"topic must be 1-200 characters with no control characters"}}`, w.Body.String())
	w = c.call(c.kai, "POST", "/api/rooms", `{"topic":"T"}`)
	assert.JSONEq(t, `{"error":{"code":"validation","message":"a room needs at least one agent"}}`, w.Body.String())
}

func TestDMs(t *testing.T) {
	c := newChat(t)
	body := `{"agent_id":"` + c.scout.ID.String() + `"}`
	w := c.call(c.kai, "POST", "/api/dms", body)
	require.Equal(t, 201, w.Code)
	first := obj(t, w)
	assert.Equal(t, "dm", first["flavor"])
	assert.Equal(t, "", first["topic"])
	w = c.call(c.kai, "POST", "/api/dms", body)
	assert.Equal(t, 200, w.Code)
	assert.Equal(t, first["id"], obj(t, w)["id"])
	w = c.call(c.ada, "POST", "/api/dms", body)
	assert.Equal(t, 201, w.Code)
	assert.NotEqual(t, first["id"], obj(t, w)["id"])
	w = c.call(c.kai, "PATCH", "/api/rooms/"+first["id"].(string), `{"archived":true}`)
	assert.Equal(t, 400, w.Code)
	assert.JSONEq(t, `{"error":{"code":"validation","message":"a DM cannot be changed"}}`, w.Body.String())
}

func TestRoomMembersAndSettings(t *testing.T) {
	c := newChat(t)
	room := "/api/rooms/" + obj(t, c.call(c.kai, "POST", "/api/rooms", `{"topic":"T","agent_ids":["`+c.scout.ID.String()+`"]}`))["id"].(string)
	w := c.call(c.kai, "POST", room+"/agents", `{"agent_id":"`+c.atlas.ID.String()+`"}`)
	require.Equal(t, 200, w.Code)
	assert.Equal(t, "channel", obj(t, w)["flavor"])
	assert.Len(t, obj(t, w)["agents"], 2)
	w = c.call(c.kai, "DELETE", room+"/agents/"+c.scout.ID.String(), "")
	require.Equal(t, 200, w.Code)
	assert.Equal(t, "conversation", obj(t, w)["flavor"])
	w = c.call(c.kai, "DELETE", room+"/agents/"+c.atlas.ID.String(), "")
	assert.Equal(t, 400, w.Code)
	assert.JSONEq(t, `{"error":{"code":"validation","message":"a room needs at least one agent"}}`, w.Body.String())
	assert.Equal(t, 404, c.call(c.kai, "DELETE", room+"/agents/nope", "").Code)

	assert.Equal(t, 404, c.call(c.ada, "GET", room, "").Code)
	w = c.call(c.kai, "POST", room+"/humans", `{"human_id":"`+c.adaH.ID.String()+`"}`)
	require.Equal(t, 200, w.Code)
	assert.Len(t, obj(t, w)["humans"], 2)
	assert.Equal(t, 200, c.call(c.ada, "GET", room, "").Code)

	w = c.call(c.ada, "PATCH", room, `{"topic":"Renamed","archived":null}`)
	require.Equal(t, 200, w.Code)
	assert.Equal(t, "Renamed", obj(t, w)["topic"])
	assert.Equal(t, false, obj(t, w)["archived"])
	w = c.call(c.ada, "PATCH", room, `{"archived":true}`)
	require.Equal(t, 200, w.Code)
	assert.Equal(t, true, obj(t, w)["archived"])
	w = c.call(c.kai, "POST", room+"/messages", `{"text":"too late"}`)
	assert.Equal(t, 409, w.Code)
	assert.JSONEq(t, `{"error":{"code":"room_archived","message":"room is archived"}}`, w.Body.String())
	w = c.call(c.kai, "PATCH", room, `{}`) // nothing asked, nothing changed
	assert.Equal(t, 200, w.Code)
	assert.Equal(t, true, obj(t, w)["archived"])
}

// Review Focus 3: every room route, as a human who is not in the room, and with an ID that names nothing.
func TestRoomRoutes_HideRoomsFromNonMembers(t *testing.T) {
	c := newChat(t)
	room := obj(t, c.call(c.kai, "POST", "/api/rooms", `{"topic":"Private","agent_ids":["`+c.scout.ID.String()+`"]}`))["id"].(string)
	msg := obj(t, c.call(c.kai, "POST", "/api/rooms/"+room+"/messages", `{"text":"secret"}`))["id"].(string)
	routes := []struct{ method, path, body string }{
		{"GET", "", ""}, {"PATCH", "", `{"topic":"Mine"}`},
		{"POST", "/humans", `{"human_id":"` + c.adaH.ID.String() + `"}`}, {"POST", "/agents", `{"agent_id":"` + c.atlas.ID.String() + `"}`},
		{"DELETE", "/agents/" + c.scout.ID.String(), ""}, {"PUT", "/read", `{"message_id":"` + msg + `"}`},
		{"GET", "/messages", ""}, {"POST", "/messages", `{"text":"hi"}`}, {"GET", "/messages/" + msg, ""},
	}
	for _, r := range routes {
		for _, id := range []string{room, ulid.Make().String(), "nope"} {
			w := c.call(c.ada, r.method, "/api/rooms/"+id+r.path, r.body)
			assert.Equal(t, 404, w.Code, "%s %s as ada, room %s", r.method, r.path, id)
			assert.JSONEq(t, notFound, w.Body.String())
			assert.NotContains(t, w.Body.String(), "secret")
		}
	}
	assert.JSONEq(t, `{"rooms":[]}`, c.call(c.ada, "GET", "/api/rooms", "").Body.String())
	assert.Equal(t, "Private", obj(t, c.call(c.kai, "GET", "/api/rooms/"+room, ""))["topic"]) // and nothing changed
}

func TestRoomRoutes_NeedASessionAndTheSameOrigin(t *testing.T) {
	c := newChat(t)
	_, bearer, _ := c.svc.CreateAgent(ctx, "third", "Third")
	for _, target := range []string{"/api/rooms", "/api/agents", "/api/humans"} {
		assert.Equal(t, 401, c.call("", "GET", target, "").Code, target)
		assert.Equal(t, 401, do(c.e, "GET", target, "Authorization", "Bearer "+bearer).Code, target) // an agent's token is not a session
	}
	assert.Equal(t, 401, c.call("", "POST", "/api/rooms", `{}`).Code)
	assert.Equal(t, 403, do(c.e, "POST", "/api/rooms", "Cookie", c.kai).Code) // no Origin
	assert.Equal(t, 403, do(c.e, "DELETE", "/api/rooms/x/agents/y", "Cookie", c.kai, "Origin", "https://evil.test").Code)
}

func TestDirectory(t *testing.T) {
	c := newChat(t)
	require.NoError(t, c.svc.RevokeAgent(ctx, "atlas"))
	w := c.call(c.kai, "GET", "/api/agents", "")
	require.Equal(t, 200, w.Code)
	assert.JSONEq(t, fmt.Sprintf(`{"agents":[{"id":%q,"handle":"atlas","display_name":"Atlas","revoked":true},{"id":%q,"handle":"scout","display_name":"Scout","revoked":false}]}`, c.atlas.ID, c.scout.ID), w.Body.String())
	w = c.call(c.ada, "GET", "/api/humans", "")
	require.Equal(t, 200, w.Code)
	assert.JSONEq(t, fmt.Sprintf(`{"humans":[{"id":%q,"username":"ada"},{"id":%q,"username":"kai"}]}`, c.adaH.ID, c.kaiH.ID), w.Body.String())
	assert.NotContains(t, w.Body.String(), "argon2")
}
