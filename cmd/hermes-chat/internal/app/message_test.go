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
