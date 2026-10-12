package app_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const pausedBody = `{"error":{"code":"paused","message":"the room is paused or everything is stopped"}}`

func TestPauseAndResumeRoutes(t *testing.T) {
	c := newChat(t)
	w := c.call(c.kai, "POST", "/api/rooms", fmt.Sprintf(`{"topic":"Trip","agent_ids":[%q]}`, c.scout.ID.String()))
	require.Equal(t, 201, w.Code)
	room := obj(t, w)
	assert.Equal(t, false, room["paused"])
	assert.Nil(t, room["pause_reason"])
	assert.EqualValues(t, 6, room["turn_cap"])
	assert.Equal(t, false, room["halted"])
	base := "/api/rooms/" + room["id"].(string)

	w = c.call(c.kai, "POST", base+"/pause", "")
	require.Equal(t, 200, w.Code)
	assert.Equal(t, true, obj(t, w)["paused"])
	assert.Equal(t, "human", obj(t, c.call(c.kai, "GET", base, ""))["pause_reason"])
	assert.Equal(t, 200, c.call(c.kai, "POST", base+"/pause", "").Code) // a second tap is fine

	w = c.call(c.kai, "POST", base+"/resume", "")
	require.Equal(t, 200, w.Code)
	resumed := obj(t, w)
	assert.Equal(t, false, resumed["paused"])
	assert.Nil(t, resumed["pause_reason"])

	assert.Equal(t, 404, c.call(c.ada, "POST", base+"/pause", "").Code) // not a member
	assert.Equal(t, 404, c.call(c.kai, "POST", "/api/rooms/not-an-id/pause", "").Code)
	assert.Equal(t, 401, c.call("", "POST", base+"/pause", "").Code)
}

func TestTurnCapInPatch(t *testing.T) {
	c := newChat(t)
	room := obj(t, c.call(c.kai, "POST", "/api/rooms", fmt.Sprintf(`{"topic":"Trip","agent_ids":[%q]}`, c.scout.ID.String())))
	base := "/api/rooms/" + room["id"].(string)
	w := c.call(c.kai, "PATCH", base, `{"turn_cap":3}`)
	require.Equal(t, 200, w.Code)
	assert.EqualValues(t, 3, obj(t, w)["turn_cap"])
	w = c.call(c.kai, "PATCH", base, `{"turn_cap":0}`)
	assert.Equal(t, 400, w.Code)
	assert.JSONEq(t, `{"error":{"code":"validation","message":"turn_cap must be 1 to 100"}}`, w.Body.String())
	w = c.call(c.kai, "PATCH", base, `{"topic":"Renamed"}`) // an absent turn_cap is left alone
	assert.EqualValues(t, 3, obj(t, w)["turn_cap"])
	assert.Equal(t, 400, c.call(c.kai, "PATCH", base, `{"turn_cap":"six"}`).Code)
}

func TestHaltRoutes(t *testing.T) {
	c := newChat(t)
	room := obj(t, c.call(c.kai, "POST", "/api/rooms", fmt.Sprintf(`{"topic":"Trip","agent_ids":[%q]}`, c.scout.ID.String())))
	w := c.call(c.ada, "GET", "/api/halt", "") // ada is in no room and can still see and use it
	require.Equal(t, 200, w.Code)
	assert.JSONEq(t, `{"halted":false}`, w.Body.String())
	w = c.call(c.ada, "POST", "/api/halt", "")
	require.Equal(t, 200, w.Code)
	assert.JSONEq(t, `{"halted":true}`, w.Body.String())
	assert.JSONEq(t, `{"halted":true}`, c.call(c.kai, "GET", "/api/halt", "").Body.String())
	got := obj(t, c.call(c.kai, "GET", "/api/rooms/"+room["id"].(string), ""))
	assert.Equal(t, true, got["halted"])
	assert.Equal(t, false, got["paused"])
	w = c.call(c.kai, "DELETE", "/api/halt", "")
	require.Equal(t, 200, w.Code)
	assert.JSONEq(t, `{"halted":false}`, w.Body.String())
	assert.Equal(t, 401, c.call("", "POST", "/api/halt", "").Code)
}
