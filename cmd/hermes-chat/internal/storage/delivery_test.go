package storage_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/storage"
)

func TestDeliveries_PutGetList(t *testing.T) {
	r := roomed(t)
	m1 := msg("1", trip.ID, kai.ID)
	m2 := msg("2", trip.ID, kai.ID)
	require.NoError(t, r.PutMessage(ctx, m1))
	require.NoError(t, r.PutMessage(ctx, m2))
	w1 := storage.Delivery{RoomID: trip.ID, AgentID: scout.ID, MessageID: m1.ID, State: "waiting"}
	w2 := storage.Delivery{RoomID: trip.ID, AgentID: atlas.ID, MessageID: m1.ID, State: "waiting"}
	require.NoError(t, r.PutDelivery(ctx, w1))
	require.NoError(t, r.PutDelivery(ctx, w2))

	got, err := r.GetDelivery(ctx, scout.ID, m1.ID)
	require.NoError(t, err)
	assert.Equal(t, w1, got)
	assert.ErrorIs(t, r.PutDelivery(ctx, w1), storage.ErrConflict) // duplicate (room, agent, message)

	list, err := r.ListDeliveriesForMessage(ctx, trip.ID, m1.ID)
	require.NoError(t, err)
	assert.Equal(t, []storage.Delivery{w2, w1}, list) // ordered atlas, scout by handle

	w3 := storage.Delivery{RoomID: trip.ID, AgentID: scout.ID, MessageID: m2.ID, State: "waiting"}
	require.NoError(t, r.PutDelivery(ctx, w3))
	gotRange, err := r.ListDeliveriesInRange(ctx, trip.ID, m1.ID, m2.ID)
	require.NoError(t, err)
	assert.Equal(t, []storage.Delivery{w2, w1, w3}, gotRange) // by message id, then handle

	none, err := r.ListDeliveriesInRange(ctx, dm.ID, m1.ID, m2.ID)
	require.NoError(t, err)
	assert.NotNil(t, none)
	assert.Empty(t, none)
}

func TestDeliveries_HeadWaiting(t *testing.T) {
	r := roomed(t)
	m0 := msg("0", trip.ID, kai.ID)
	m1 := msg("1", trip.ID, kai.ID)
	m2 := msg("2", trip.ID, kai.ID)
	for _, m := range []storage.Message{m0, m1, m2} {
		require.NoError(t, r.PutMessage(ctx, m))
	}

	// (scout, m1) waiting → head is m1.
	require.NoError(t, r.PutDelivery(ctx, storage.Delivery{RoomID: trip.ID, AgentID: scout.ID, MessageID: m1.ID, State: "waiting"}))
	head, ok, err := r.HeadWaiting(ctx, trip.ID, scout.ID)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, m1.ID, head.MessageID)

	// Set m1 working → no head.
	require.NoError(t, r.SetDeliveryState(ctx, trip.ID, scout.ID, m1.ID, "working"))
	_, ok, err = r.HeadWaiting(ctx, trip.ID, scout.ID)
	require.NoError(t, err)
	assert.False(t, ok)

	// Set m1 done, add (scout, m2) waiting → head is m2.
	require.NoError(t, r.SetDeliveryState(ctx, trip.ID, scout.ID, m1.ID, "done"))
	require.NoError(t, r.PutDelivery(ctx, storage.Delivery{RoomID: trip.ID, AgentID: scout.ID, MessageID: m2.ID, State: "waiting"}))
	head, ok, err = r.HeadWaiting(ctx, trip.ID, scout.ID)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, m2.ID, head.MessageID)

	// Add (scout, m0) waiting — m0 sorts before m2 → head is m0.
	require.NoError(t, r.PutDelivery(ctx, storage.Delivery{RoomID: trip.ID, AgentID: scout.ID, MessageID: m0.ID, State: "waiting"}))
	head, ok, err = r.HeadWaiting(ctx, trip.ID, scout.ID)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, m0.ID, head.MessageID)

	// A pair with only done rows → no head.
	require.NoError(t, r.SetDeliveryState(ctx, trip.ID, scout.ID, m0.ID, "done"))
	require.NoError(t, r.SetDeliveryState(ctx, trip.ID, scout.ID, m2.ID, "done"))
	_, ok, err = r.HeadWaiting(ctx, trip.ID, scout.ID)
	require.NoError(t, err)
	assert.False(t, ok)

	// A different agent's pair is unaffected.
	require.NoError(t, r.PutDelivery(ctx, storage.Delivery{RoomID: trip.ID, AgentID: atlas.ID, MessageID: m1.ID, State: "waiting"}))
	head, ok, err = r.HeadWaiting(ctx, trip.ID, atlas.ID)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, m1.ID, head.MessageID)
	_, ok, err = r.HeadWaiting(ctx, trip.ID, scout.ID)
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestDeliveries_ReleasableDeliveries(t *testing.T) {
	r := roomed(t)
	aHead := msg("1", trip.ID, kai.ID)    // room A's releasable head
	aBlocked := msg("2", trip.ID, kai.ID) // room A's blocked tail
	bDelivered := msg("3", dm.ID, kai.ID) // room B's blocked pair
	bWaiting := msg("4", dm.ID, kai.ID)
	for _, m := range []storage.Message{aHead, aBlocked, bDelivered, bWaiting} {
		require.NoError(t, r.PutMessage(ctx, m))
	}
	require.NoError(t, r.PutDelivery(ctx, storage.Delivery{RoomID: trip.ID, AgentID: scout.ID, MessageID: aHead.ID, State: "waiting"}))
	require.NoError(t, r.PutDelivery(ctx, storage.Delivery{RoomID: trip.ID, AgentID: scout.ID, MessageID: aBlocked.ID, State: "waiting"}))
	require.NoError(t, r.PutDelivery(ctx, storage.Delivery{RoomID: dm.ID, AgentID: scout.ID, MessageID: bDelivered.ID, State: "delivered"}))
	require.NoError(t, r.PutDelivery(ctx, storage.Delivery{RoomID: dm.ID, AgentID: scout.ID, MessageID: bWaiting.ID, State: "waiting"}))

	got, err := r.ReleasableDeliveries(ctx, scout.ID)
	require.NoError(t, err)
	want := storage.Delivery{RoomID: trip.ID, AgentID: scout.ID, MessageID: aHead.ID, State: "waiting"}
	assert.Equal(t, []storage.Delivery{want}, got) // room A's m1 only, ordered by room id
}

func TestDeliveries_SetStateAndCancel(t *testing.T) {
	r := roomed(t)
	m1 := msg("1", trip.ID, kai.ID)
	m2 := msg("2", trip.ID, kai.ID)
	m3 := msg("3", trip.ID, kai.ID)
	for _, m := range []storage.Message{m1, m2, m3} {
		require.NoError(t, r.PutMessage(ctx, m))
	}
	require.NoError(t, r.PutDelivery(ctx, storage.Delivery{RoomID: trip.ID, AgentID: scout.ID, MessageID: m1.ID, State: "waiting"}))
	require.NoError(t, r.PutDelivery(ctx, storage.Delivery{RoomID: trip.ID, AgentID: scout.ID, MessageID: m2.ID, State: "waiting"}))

	require.NoError(t, r.SetDeliveryState(ctx, trip.ID, scout.ID, m1.ID, "delivered"))
	got, err := r.GetDelivery(ctx, scout.ID, m1.ID)
	require.NoError(t, err)
	assert.Equal(t, "delivered", got.State)
	assert.ErrorIs(t, r.SetDeliveryState(ctx, trip.ID, scout.ID, "01JA00000000000000000000M9", "delivered"), storage.ErrNotFound)

	require.NoError(t, r.PutDelivery(ctx, storage.Delivery{RoomID: trip.ID, AgentID: scout.ID, MessageID: m3.ID, State: "waiting"}))
	n, err := r.CancelDeliveries(ctx, trip.ID, scout.ID)
	require.NoError(t, err)
	assert.EqualValues(t, 2, n) // m1 was already delivered and is left alone
	got, _ = r.GetDelivery(ctx, scout.ID, m1.ID)
	assert.Equal(t, "delivered", got.State)
	n, err = r.CancelDeliveries(ctx, trip.ID, scout.ID)
	require.NoError(t, err)
	assert.EqualValues(t, 0, n)
}

func TestListAgentRoomIDs(t *testing.T) {
	r := roomed(t) // scout is in trip and dm; atlas is in neither
	got, err := r.ListAgentRoomIDs(ctx, scout.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{trip.ID, dm.ID}, got)
	none, err := r.ListAgentRoomIDs(ctx, atlas.ID)
	require.NoError(t, err)
	assert.NotNil(t, none)
	assert.Empty(t, none)
}
