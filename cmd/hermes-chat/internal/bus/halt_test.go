package bus_test

import (
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/bus"
)

func TestHalt_HoldsEveryRoomAndLiftRestoresEachRoomsOwnState(t *testing.T) {
	w := newWorld(t)
	dm, conv := dmRoom(t, w), convRoom(t, w)
	sub := agentSub(t, w, w.scout)
	_, err := w.s.PauseRoom(ctx, w.kai, conv.ID) // paused before the halt
	require.NoError(t, err)
	drain(t, sub)
	human, err := w.s.Subscribe(ctx, w.kai, bus.FromNow)
	require.NoError(t, err)
	defer human.Close()

	require.NoError(t, w.s.StopEverything(ctx, w.kai))
	halted, err := w.s.Halted(ctx)
	require.NoError(t, err)
	assert.True(t, halted)
	assert.Len(t, of(drain(t, sub), bus.EventControlPause), 2, "one pause per room the agent is in")
	changed := of(drain(t, human), bus.EventRoomChanged)
	require.Len(t, changed, 2, "one room.changed per room")
	assert.True(t, changed[0].Room.Halted)
	got, _ := w.s.GetRoom(ctx, w.kai, dm.ID)
	assert.True(t, got.Halted)
	assert.False(t, got.Paused(), "the halt is not the room's own pause")

	inDM, _ := w.s.PostMessage(ctx, w.kai, dm.ID, "held", ulid.Zero)
	inConv, _ := w.s.PostMessage(ctx, w.kai, conv.ID, "held too", ulid.Zero)
	assert.Equal(t, bus.DeliveryWaiting, inDM.Deliveries[0].State)
	_, err = w.s.PostMessage(ctx, agentActor(w.scout), dm.ID, "unprompted", ulid.Zero)
	assert.ErrorIs(t, err, bus.ErrPaused)
	assert.Empty(t, of(drain(t, sub), bus.EventDelivery))

	require.NoError(t, w.s.ResumeEverything(ctx, w.kai))
	assert.Equal(t, bus.DeliveryDelivered, stateOf(t, w, dm, inDM.ID, w.scout))
	assert.Equal(t, bus.DeliveryWaiting, stateOf(t, w, conv, inConv.ID, w.scout), "a room paused before the halt is still paused")
	got, _ = w.s.GetRoom(ctx, w.kai, conv.ID)
	assert.True(t, got.Paused())
	assert.False(t, got.Halted)
}

func TestHalt_DoubleTapsAndCrossedSwitches(t *testing.T) {
	w := newWorld(t)
	room := dmRoom(t, w)
	agentSub(t, w, w.scout)
	human, err := w.s.Subscribe(ctx, w.kai, bus.FromNow)
	require.NoError(t, err)
	defer human.Close()
	require.NoError(t, w.s.ResumeEverything(ctx, w.kai)) // not stopped
	assert.Empty(t, drain(t, human))
	require.NoError(t, w.s.StopEverything(ctx, w.kai))
	drain(t, human)
	require.NoError(t, w.s.StopEverything(ctx, w.kai))
	assert.Empty(t, drain(t, human), "a second stop writes nothing")

	// pausing and resuming one room while everything is stopped changes only that room
	m, _ := w.s.PostMessage(ctx, w.kai, room.ID, "held", ulid.Zero)
	_, err = w.s.PauseRoom(ctx, w.kai, room.ID)
	require.NoError(t, err)
	got, err := w.s.ResumeRoom(ctx, w.kai, room.ID)
	require.NoError(t, err)
	assert.False(t, got.Paused())
	assert.True(t, got.Halted)
	assert.Equal(t, bus.DeliveryWaiting, stateOf(t, w, room, m.ID, w.scout), "resuming a room does not lift the halt")

	// a room made while everything is stopped is stopped too
	made := convRoom(t, w)
	assert.True(t, made.Halted)
	held, _ := w.s.PostMessage(ctx, w.kai, made.ID, "held", ulid.Zero)
	assert.Equal(t, bus.DeliveryWaiting, held.Deliveries[0].State)

	assert.ErrorIs(t, w.s.StopEverything(ctx, agentActor(w.scout)), bus.ErrForbidden)
	assert.ErrorIs(t, w.s.ResumeEverything(ctx, agentActor(w.scout)), bus.ErrForbidden)
}

func TestHalt_SurvivesARestart(t *testing.T) {
	w := newWorld(t)
	room := dmRoom(t, w)
	m, _ := w.s.PostMessage(ctx, w.kai, room.ID, "held", ulid.Zero)
	require.NoError(t, w.s.StopEverything(ctx, w.kai))

	restarted := bus.NewService(w.r, func() time.Time { return fixed }) // a new process over the same database
	halted, err := restarted.Halted(ctx)
	require.NoError(t, err)
	assert.True(t, halted)
	sub, err := restarted.Subscribe(ctx, agentActor(w.scout), bus.FromNow)
	require.NoError(t, err)
	defer sub.Close()
	assert.Empty(t, of(drain(t, sub), bus.EventDelivery), "the sweep on connect releases nothing")
	got, _ := restarted.GetMessage(ctx, w.kai, room.ID, m.ID)
	assert.Equal(t, bus.DeliveryWaiting, got.Deliveries[0].State)
}
