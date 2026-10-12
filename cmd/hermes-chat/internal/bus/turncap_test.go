package bus_test

import (
	"testing"

	"github.com/oklog/ulid/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/bus"
)

// ping has one agent wake another, and finishes the turn if it was released.
func ping(t *testing.T, w world, room bus.Room, from, to bus.Agent) bus.Message {
	t.Helper()
	m, err := w.s.PostMessage(ctx, agentActor(from), room.ID, "@"+to.Handle+" your turn", ulid.Zero)
	require.NoError(t, err)
	if m.Deliveries[0].State == bus.DeliveryDelivered {
		require.NoError(t, w.s.TurnDone(ctx, agentActor(to), m.ID))
	}
	return m
}

// connectedChannel is kai's channel with scout and atlas, both connected.
func connectedChannel(t *testing.T, w world) (bus.Room, *bus.Subscription, *bus.Subscription) {
	t.Helper()
	room := channelRoom(t, w)
	return room, agentSub(t, w, w.scout), agentSub(t, w, w.atlas)
}

func TestTurnCap_PausesTheRoomAndSaysWhy(t *testing.T) {
	w := newWorld(t)
	room, scout, atlas := connectedChannel(t, w)
	human, err := w.s.Subscribe(ctx, w.kai, bus.FromNow)
	require.NoError(t, err)
	defer human.Close()
	for i := 0; i < bus.DefaultTurnCap; i++ {
		assert.Equal(t, bus.DeliveryDelivered, ping(t, w, room, w.scout, w.atlas).Deliveries[0].State, "turn %d", i+1)
	}
	drain(t, scout)
	drain(t, atlas)
	drain(t, human)

	seventh := ping(t, w, room, w.scout, w.atlas) // the post itself succeeds
	assert.Equal(t, bus.DeliveryWaiting, seventh.Deliveries[0].State)
	got, _ := w.s.GetRoom(ctx, w.kai, room.ID)
	assert.True(t, got.Paused())
	assert.Equal(t, bus.PauseTurnCap, got.PauseReason)
	changed := of(drain(t, human), bus.EventRoomChanged)
	require.Len(t, changed, 1)
	assert.Equal(t, bus.PauseTurnCap, changed[0].Room.PauseReason)
	assert.Len(t, of(drain(t, scout), bus.EventControlPause), 1)
	atlasEvs := drain(t, atlas)
	assert.Len(t, of(atlasEvs, bus.EventControlPause), 1)
	assert.Empty(t, of(atlasEvs, bus.EventDelivery))
	_, err = w.s.PostMessage(ctx, agentActor(w.atlas), room.ID, "@scout again", ulid.Zero)
	assert.ErrorIs(t, err, bus.ErrPaused)

	// a human resume is a human in the loop: the held turn goes out as turn 1 of a new count
	_, err = w.s.ResumeRoom(ctx, w.kai, room.ID)
	require.NoError(t, err)
	assert.Equal(t, bus.DeliveryDelivered, stateOf(t, w, room, seventh.ID, w.atlas))
	require.NoError(t, w.s.TurnDone(ctx, agentActor(w.atlas), seventh.ID))
	for i := 1; i < bus.DefaultTurnCap; i++ {
		assert.Equal(t, bus.DeliveryDelivered, ping(t, w, room, w.scout, w.atlas).Deliveries[0].State)
	}
	assert.Equal(t, bus.DeliveryWaiting, ping(t, w, room, w.scout, w.atlas).Deliveries[0].State)
}

func TestTurnCap_AHumanMessageStartsTheCountAgain(t *testing.T) {
	w := newWorld(t)
	room, _, _ := connectedChannel(t, w)
	for i := 0; i < bus.DefaultTurnCap-1; i++ {
		ping(t, w, room, w.scout, w.atlas)
	}
	_, err := w.s.PostMessage(ctx, w.kai, room.ID, "carry on", ulid.Zero) // wakes nobody; still a human message
	require.NoError(t, err)
	for i := 0; i < bus.DefaultTurnCap; i++ {
		assert.Equal(t, bus.DeliveryDelivered, ping(t, w, room, w.scout, w.atlas).Deliveries[0].State)
	}
	assert.Equal(t, bus.DeliveryWaiting, ping(t, w, room, w.scout, w.atlas).Deliveries[0].State)
}

func TestTurnCap_ASteerStartsTheCountAgain(t *testing.T) {
	w := newWorld(t)
	room, _, _ := connectedChannel(t, w)
	for i := 0; i < bus.DefaultTurnCap-1; i++ {
		ping(t, w, room, w.scout, w.atlas)
	}
	last, err := w.s.PostMessage(ctx, agentActor(w.scout), room.ID, "@atlas your turn", ulid.Zero)
	require.NoError(t, err)
	require.NoError(t, w.s.TurnWorking(ctx, agentActor(w.atlas), last.ID))
	_, _, err = w.s.Steer(ctx, w.kai, room.ID, w.atlas.ID, "", "mind the budget")
	require.NoError(t, err)
	require.NoError(t, w.s.TurnDone(ctx, agentActor(w.atlas), last.ID))
	for i := 0; i < bus.DefaultTurnCap; i++ {
		assert.Equal(t, bus.DeliveryDelivered, ping(t, w, room, w.scout, w.atlas).Deliveries[0].State)
	}
	assert.Equal(t, bus.DeliveryWaiting, ping(t, w, room, w.scout, w.atlas).Deliveries[0].State)
}

func TestTurnCap_HumanWokenAndUnpromptedTurnsDoNotCount(t *testing.T) {
	w := newWorld(t)
	room, _, _ := connectedChannel(t, w)
	for i := 0; i < 2*bus.DefaultTurnCap; i++ {
		m, err := w.s.PostMessage(ctx, w.kai, room.ID, "@scout go", ulid.Zero)
		require.NoError(t, err)
		require.Equal(t, bus.DeliveryDelivered, m.Deliveries[0].State)
		require.NoError(t, w.s.TurnDone(ctx, agentActor(w.scout), m.ID))
	}
	dm := dmRoom(t, w)
	for i := 0; i < 2*bus.DefaultTurnCap; i++ { // a week of scheduled reports
		_, err := w.s.PostMessage(ctx, agentActor(w.scout), dm.ID, "daily report", ulid.Zero)
		require.NoError(t, err)
	}
	for _, id := range []ulid.ULID{room.ID, dm.ID} {
		got, _ := w.s.GetRoom(ctx, w.kai, id)
		assert.False(t, got.Paused())
	}
}

func TestTurnCap_OneMessageWakingSeveralStopsAtTheCap(t *testing.T) {
	w := newWorld(t)
	echo, _, err := w.s.CreateAgent(ctx, "echo", "Echo")
	require.NoError(t, err)
	room, err := w.s.CreateRoom(ctx, w.kai, "Trio", ids(w.atlas, echo, w.scout))
	require.NoError(t, err)
	for _, a := range []bus.Agent{w.atlas, echo, w.scout} {
		agentSub(t, w, a)
	}
	room, err = w.s.UpdateRoom(ctx, w.kai, room.ID, bus.RoomUpdate{TurnCap: ptr(1)})
	require.NoError(t, err)
	assert.Equal(t, 1, room.TurnCap)

	m, err := w.s.PostMessage(ctx, agentActor(w.scout), room.ID, "@atlas @echo both of you", ulid.Zero)
	require.NoError(t, err)
	require.Len(t, m.Deliveries, 2) // handle order: atlas, echo
	assert.Equal(t, bus.DeliveryDelivered, m.Deliveries[0].State)
	assert.Equal(t, bus.DeliveryWaiting, m.Deliveries[1].State, "the second wake is over the cap and waits")
	got, _ := w.s.GetRoom(ctx, w.kai, room.ID)
	assert.Equal(t, bus.PauseTurnCap, got.PauseReason)
}

func TestUpdateRoom_TurnCap(t *testing.T) {
	w := newWorld(t)
	room := convRoom(t, w)
	for _, bad := range []int{0, -1, bus.MaxTurnCap + 1} {
		_, err := w.s.UpdateRoom(ctx, w.kai, room.ID, bus.RoomUpdate{TurnCap: ptr(bad)})
		var ve bus.ValidationError
		require.ErrorAs(t, err, &ve, "%d", bad)
		assert.Equal(t, "turn_cap must be 1 to 100", ve.Message)
	}
	for _, ok := range []int{1, bus.MaxTurnCap} {
		got, err := w.s.UpdateRoom(ctx, w.kai, room.ID, bus.RoomUpdate{TurnCap: ptr(ok)})
		require.NoError(t, err)
		assert.Equal(t, ok, got.TurnCap)
	}
	before := len(w.kinds(t, w.kai))
	_, err := w.s.UpdateRoom(ctx, w.kai, room.ID, bus.RoomUpdate{TurnCap: ptr(bus.MaxTurnCap)})
	require.NoError(t, err)
	assert.Len(t, w.kinds(t, w.kai), before, "an unchanged cap writes no event")
	_, err = w.s.UpdateRoom(ctx, w.kai, dmRoom(t, w).ID, bus.RoomUpdate{TurnCap: ptr(3)})
	assert.ErrorIs(t, err, bus.ErrValidation) // a DM cannot be changed
	_, err = w.s.UpdateRoom(ctx, w.kai, room.ID, bus.RoomUpdate{Archived: ptr(true), TurnCap: ptr(3)})
	assert.ErrorIs(t, err, bus.ErrRoomArchived)
}

func TestTurnCap_FiresWhenTurnDoneReleasesAQueuedAgentMessage(t *testing.T) {
	w := newWorld(t)
	room, _, _ := connectedChannel(t, w)
	_, err := w.s.UpdateRoom(ctx, w.kai, room.ID, bus.RoomUpdate{TurnCap: ptr(1)})
	require.NoError(t, err)
	first, err := w.s.PostMessage(ctx, agentActor(w.scout), room.ID, "@atlas one", ulid.Zero)
	require.NoError(t, err)
	require.Equal(t, bus.DeliveryDelivered, first.Deliveries[0].State) // the count is now at the cap
	queued, err := w.s.PostMessage(ctx, agentActor(w.scout), room.ID, "@atlas two", ulid.Zero)
	require.NoError(t, err)
	require.Equal(t, bus.DeliveryWaiting, queued.Deliveries[0].State) // behind a busy atlas
	got, _ := w.s.GetRoom(ctx, w.kai, room.ID)
	require.False(t, got.Paused())

	require.NoError(t, w.s.TurnDone(ctx, agentActor(w.atlas), first.ID))
	assert.Equal(t, bus.DeliveryWaiting, stateOf(t, w, room, queued.ID, w.atlas))
	got, _ = w.s.GetRoom(ctx, w.kai, room.ID)
	assert.True(t, got.Paused())
	assert.Equal(t, bus.PauseTurnCap, got.PauseReason)
}
