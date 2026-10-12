package app_test

import (
	"strings"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/bus"
)

func TestMessages_PostGetAndList(t *testing.T) {
	c := newChat(t)
	room := "/api/rooms/" + obj(t, c.call(c.kai, "POST", "/api/rooms", `{"topic":"T","agent_ids":["`+c.scout.ID.String()+`"]}`))["id"].(string)
	w := c.call(c.kai, "POST", room+"/messages", `{"text":"hello *world*"}`)
	require.Equal(t, 201, w.Code, w.Body.String())
	first := obj(t, w)
	assert.Equal(t, "hello *world*", first["text"])
	assert.Nil(t, first["reply_to_id"])
	assert.Contains(t, first, "reply_to_id")
	assert.Equal(t, map[string]any{"kind": "human", "id": c.kaiH.ID.String(), "name": "kai"}, first["author"])
	_, err := time.Parse(time.RFC3339Nano, first["created_at"].(string))
	assert.NoError(t, err)
	assert.JSONEq(t, w.Body.String(), c.call(c.kai, "GET", room+"/messages/"+first["id"].(string), "").Body.String())

	w = c.call(c.kai, "POST", room+"/messages", `{"text":"a reply","reply_to_id":"`+first["id"].(string)+`"}`)
	require.Equal(t, 201, w.Code)
	second := obj(t, w)
	assert.Equal(t, first["id"], second["reply_to_id"])
	w = c.call(c.kai, "POST", room+"/messages", `{"text":"x","reply_to_id":null}`)
	assert.Equal(t, 201, w.Code)

	list := obj(t, c.call(c.kai, "GET", room+"/messages", ""))["messages"].([]any)
	require.Len(t, list, 3)
	assert.Equal(t, first["id"], list[0].(map[string]any)["id"]) // oldest first
	list = obj(t, c.call(c.kai, "GET", room+"/messages?limit=1&before="+second["id"].(string), ""))["messages"].([]any)
	require.Len(t, list, 1)
	assert.Equal(t, first["id"], list[0].(map[string]any)["id"])
	assert.JSONEq(t, `{"messages":[]}`, c.call(c.kai, "GET", room+"/messages?before="+first["id"].(string), "").Body.String())

	r := obj(t, c.call(c.kai, "GET", room, ""))
	assert.NotNil(t, r["last_message_id"])
	assert.Equal(t, r["last_message_id"], r["last_read_message_id"])
}

func TestMessages_Refused(t *testing.T) {
	c := newChat(t)
	room := "/api/rooms/" + obj(t, c.call(c.kai, "POST", "/api/rooms", `{"topic":"T","agent_ids":["`+c.scout.ID.String()+`"]}`))["id"].(string)
	for _, q := range []string{"limit=abc", "limit=0", "limit=201", "limit=-1", "limit="} {
		w := c.call(c.kai, "GET", room+"/messages?"+q, "")
		assert.Equal(t, 400, w.Code, q)
		assert.JSONEq(t, badReq("limit must be an integer from 1 to 200"), w.Body.String(), q)
	}
	w := c.call(c.kai, "GET", room+"/messages?before=nope", "")
	assert.Equal(t, 400, w.Code)
	assert.JSONEq(t, badReq("before must be an id"), w.Body.String())
	assert.Equal(t, 200, c.call(c.kai, "GET", room+"/messages?limit=200", "").Code)

	tooLong := `{"text":"` + strings.Repeat("a", bus.MaxMessageBytes+1) + `"}` // under the body cap, over the text cap
	for _, body := range []string{`{}`, `{"text":"   "}`, tooLong} {
		w := c.call(c.kai, "POST", room+"/messages", body)
		assert.Equal(t, 400, w.Code, "%.20q", body)
		assert.JSONEq(t, `{"error":{"code":"validation","message":"message text must be 1 to 262144 bytes, not blank, with no NUL character"}}`, w.Body.String())
	}
	w = c.call(c.kai, "POST", room+"/messages", `{"text":"hi","reply_to_id":"`+ulid.Make().String()+`"}`)
	assert.JSONEq(t, `{"error":{"code":"validation","message":"reply_to must be a message in this room"}}`, w.Body.String())
	assert.Equal(t, 404, c.call(c.kai, "GET", room+"/messages/nope", "").Code)
	assert.Equal(t, 404, c.call(c.kai, "GET", room+"/messages/"+ulid.Make().String(), "").Code)
}

func TestMarkRead(t *testing.T) {
	c := newChat(t)
	room := "/api/rooms/" + obj(t, c.call(c.kai, "POST", "/api/rooms", `{"topic":"T","agent_ids":["`+c.scout.ID.String()+`"]}`))["id"].(string)
	c.call(c.kai, "POST", room+"/humans", `{"human_id":"`+c.adaH.ID.String()+`"}`)
	msg := obj(t, c.call(c.kai, "POST", room+"/messages", `{"text":"hello"}`))["id"]
	r := obj(t, c.call(c.ada, "GET", room, ""))
	assert.Equal(t, msg, r["last_message_id"])
	assert.Nil(t, r["last_read_message_id"]) // unread for ada
	w := c.call(c.ada, "PUT", room+"/read", `{"message_id":"`+msg.(string)+`"}`)
	assert.Equal(t, 204, w.Code)
	assert.Empty(t, w.Body.String())
	assert.Equal(t, msg, obj(t, c.call(c.ada, "GET", room, ""))["last_read_message_id"])
}

// A message whose author was removed is sent with "author": null.
func TestMessages_RemovedAuthorIsNull(t *testing.T) {
	c := newChat(t)
	room := "/api/rooms/" + obj(t, c.call(c.kai, "POST", "/api/rooms", `{"topic":"T","agent_ids":["`+c.scout.ID.String()+`"]}`))["id"].(string)
	c.call(c.kai, "POST", room+"/humans", `{"human_id":"`+c.adaH.ID.String()+`"}`)
	c.call(c.ada, "POST", room+"/messages", `{"text":"from ada"}`)
	require.NoError(t, c.svc.RemoveHuman(ctx, "ada"))
	list := obj(t, c.call(c.kai, "GET", room+"/messages", ""))["messages"].([]any)
	require.Len(t, list, 1)
	m := list[0].(map[string]any)
	assert.Contains(t, m, "author")
	assert.Nil(t, m["author"])
	assert.Equal(t, "from ada", m["text"])
	assert.Equal(t, 401, c.call(c.ada, "GET", "/api/rooms", "").Code) // and her session went with her
}

// The message object carries each agent's copy of it, and the array is never null.
func TestMessageJSON_ShowsDeliveries(t *testing.T) {
	c := newChat(t)
	room := "/api/rooms/" + obj(t, c.call(c.kai, "POST", "/api/rooms", `{"topic":"T","agent_ids":["`+c.scout.ID.String()+`"]}`))["id"].(string)
	w := c.call(c.kai, "POST", room+"/messages", `{"text":"hi scout"}`) // scout is offline: the copy waits
	require.Equal(t, 201, w.Code, w.Body.String())
	m := obj(t, w)
	assert.Equal(t, []any{map[string]any{
		"agent": map[string]any{"id": c.scout.ID.String(), "handle": "scout", "display_name": "Scout", "revoked": false},
		"state": "waiting",
	}}, m["deliveries"])
	// GET agrees with POST
	assert.JSONEq(t, w.Body.String(), c.call(c.kai, "GET", room+"/messages/"+m["id"].(string), "").Body.String())
}

func TestCancelMessage(t *testing.T) {
	c := newChat(t)
	room := "/api/rooms/" + obj(t, c.call(c.kai, "POST", "/api/rooms", `{"topic":"T","agent_ids":["`+c.scout.ID.String()+`"]}`))["id"].(string)
	post := func(text string) map[string]any {
		w := c.call(c.kai, "POST", room+"/messages", `{"text":"`+text+`"}`)
		require.Equal(t, 201, w.Code, w.Body.String())
		return obj(t, w)
	}

	// a message waiting for the offline agent cancels
	m := post("hold that")
	w := c.call(c.kai, "POST", room+"/messages/"+m["id"].(string)+"/cancel", "")
	require.Equal(t, 204, w.Code)
	assert.Empty(t, w.Body.String())
	got := obj(t, c.call(c.kai, "GET", room+"/messages/"+m["id"].(string), ""))
	assert.Equal(t, "cancelled", got["deliveries"].([]any)[0].(map[string]any)["state"])

	// a second cancel finds nothing waiting
	w = c.call(c.kai, "POST", room+"/messages/"+m["id"].(string)+"/cancel", "")
	require.Equal(t, 409, w.Code)
	assert.JSONEq(t, `{"error":{"code":"nothing_waiting","message":"no waiting delivery for this message"}}`, w.Body.String())

	// a member who is not the author is forbidden
	c.call(c.kai, "POST", room+"/humans", `{"human_id":"`+c.adaH.ID.String()+`"}`)
	m2 := post("not yours")
	w = c.call(c.ada, "POST", room+"/messages/"+m2["id"].(string)+"/cancel", "")
	require.Equal(t, 403, w.Code)
	assert.JSONEq(t, forbidden, w.Body.String())

	// a message that addressed nobody is neither waitable nor cancellable
	channel := "/api/rooms/" + obj(t, c.call(c.kai, "POST", "/api/rooms", `{"topic":"C","agent_ids":["`+c.scout.ID.String()+`","`+c.atlas.ID.String()+`"]}`))["id"].(string)
	m3 := obj(t, c.call(c.kai, "POST", channel+"/messages", `{"text":"no one mentioned"}`))
	w = c.call(c.kai, "POST", channel+"/messages/"+m3["id"].(string)+"/cancel", "")
	require.Equal(t, 409, w.Code)
	assert.JSONEq(t, `{"error":{"code":"nothing_waiting","message":"no waiting delivery for this message"}}`, w.Body.String())
}

// The activity log lists a delivered turn's remarks and tool calls, oldest first.
func TestListActivity(t *testing.T) {
	c := newChat(t)
	room, _, err := c.svc.OpenDM(ctx, bus.Actor{Kind: bus.ActorHuman, ID: c.kaiH.ID}, c.scout.ID)
	require.NoError(t, err)
	scout := bus.Actor{Kind: bus.ActorAgent, ID: c.scout.ID}
	sub, err := c.svc.Subscribe(ctx, scout, bus.FromNow) // connect scout
	require.NoError(t, err)
	defer sub.Close()
	m, err := c.svc.PostMessage(ctx, bus.Actor{Kind: bus.ActorHuman, ID: c.kaiH.ID}, room.ID, "your turn", ulid.Zero)
	require.NoError(t, err)
	require.NoError(t, c.svc.TurnWorking(ctx, scout, m.ID))
	remark, err := c.svc.ReportRemark(ctx, scout, m.ID, "checking the weather")
	require.NoError(t, err)
	tool, err := c.svc.StartTool(ctx, scout, m.ID, "web_search", "searching", `{"q":"x"}`)
	require.NoError(t, err)
	_, err = c.svc.FinishTool(ctx, scout, tool.ID, 123, true)
	require.NoError(t, err)

	w := c.call(c.kai, "GET", "/api/rooms/"+room.ID.String()+"/messages/"+m.ID.String()+"/activity", "")
	require.Equal(t, 200, w.Code, w.Body.String())
	acts := obj(t, w)["activities"].([]any)
	require.Len(t, acts, 2)
	first := acts[0].(map[string]any)
	assert.Equal(t, remark.ID.String(), first["id"])
	assert.Equal(t, "remark", first["kind"])
	assert.Equal(t, "checking the weather", first["text"])
	assert.Equal(t, m.ID.String(), first["message_id"])
	assert.Equal(t, c.scout.ID.String(), first["agent"].(map[string]any)["id"])
	second := acts[1].(map[string]any)
	assert.Equal(t, tool.ID.String(), second["id"])
	assert.Equal(t, "tool", second["kind"])
	assert.Equal(t, "web_search", second["tool"])
	assert.Equal(t, float64(123), second["duration_ms"])
	assert.Equal(t, true, second["succeeded"])
	assert.NotContains(t, w.Body.String(), "connected") // the activity agent is the plain shape

	// a non-member, and a message outside the room, look missing
	assert.Equal(t, 404, c.call(c.ada, "GET", "/api/rooms/"+room.ID.String()+"/messages/"+m.ID.String()+"/activity", "").Code)
	assert.Equal(t, 404, c.call(c.kai, "GET", "/api/rooms/"+room.ID.String()+"/messages/"+ulid.Make().String()+"/activity", "").Code)
}
