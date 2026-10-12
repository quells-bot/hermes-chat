package storage_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/storage"
)

func ev(roomID, subject string) storage.Event {
	return storage.Event{RoomID: roomID, Kind: "message.posted", SubjectID: subject, CreatedAtMs: 9}
}

func TestEvents_PutAndList(t *testing.T) {
	r := roomed(t)
	last, err := r.GetLastEventID(ctx)
	require.NoError(t, err)
	assert.Zero(t, last)
	none, err := r.ListEventsForHuman(ctx, kai.ID, 0, 10)
	require.NoError(t, err)
	assert.NotNil(t, none)
	assert.Empty(t, none)
	var got []int64
	for _, e := range []storage.Event{ev(trip.ID, "a"), ev(dm.ID, "b"), ev(trip.ID, "c")} {
		e.ID = 99 // ignored
		id, err := r.PutEvent(ctx, e)
		require.NoError(t, err)
		got = append(got, id)
	}
	assert.Equal(t, []int64{1, 2, 3}, got)
	last, _ = r.GetLastEventID(ctx)
	assert.EqualValues(t, 3, last)

	mine, _ := r.ListEventsForHuman(ctx, kai.ID, 0, 10)
	require.Len(t, mine, 3)
	want := ev(trip.ID, "a")
	want.ID = 1
	assert.Equal(t, want, mine[0])
	subjects := func(es []storage.Event) (out []string) {
		for _, e := range es {
			out = append(out, e.SubjectID)
		}
		return
	}
	assert.Equal(t, []string{"a", "b", "c"}, subjects(mine))
	hers, _ := r.ListEventsForHuman(ctx, ada.ID, 0, 10)
	assert.Equal(t, []string{"a", "c"}, subjects(hers)) // ada is not in the DM
	after, _ := r.ListEventsForHuman(ctx, kai.ID, 1, 10)
	assert.Equal(t, []string{"b", "c"}, subjects(after))
	limited, _ := r.ListEventsForHuman(ctx, kai.ID, 0, 2)
	assert.Equal(t, []string{"a", "b"}, subjects(limited))
	_, err = r.PutEvent(ctx, ev("01JA00000000000000000000R9", "x"))
	assert.Error(t, err) // no such room
}

// Membership is read at query time: joining a room opens its whole log.
func TestEvents_FollowCurrentMembership(t *testing.T) {
	r := seeded(t)
	require.NoError(t, r.PutRoom(ctx, trip))
	_, err := r.PutEvent(ctx, ev(trip.ID, "a"))
	require.NoError(t, err)
	got, _ := r.ListEventsForHuman(ctx, ada.ID, 0, 10)
	assert.Empty(t, got)
	require.NoError(t, r.PutRoomHuman(ctx, storage.RoomHuman{RoomID: trip.ID, HumanID: ada.ID}))
	got, _ = r.ListEventsForHuman(ctx, ada.ID, 0, 10)
	assert.Len(t, got, 1)
}

func TestEvents_RollBackWithTheirTransaction(t *testing.T) {
	r := roomed(t)
	boom := errors.New("boom")
	err := r.InTx(ctx, func(tx storage.Repository) error {
		require.NoError(t, tx.PutMessage(ctx, msg("1", trip.ID, kai.ID)))
		_, err := tx.PutEvent(ctx, ev(trip.ID, "a"))
		require.NoError(t, err)
		seen, _ := tx.ListEventsForHuman(ctx, kai.ID, 0, 10)
		assert.Len(t, seen, 1) // visible inside the transaction
		return boom
	})
	require.ErrorIs(t, err, boom)
	last, _ := r.GetLastEventID(ctx)
	assert.Zero(t, last)
	_, err = r.GetMessage(ctx, "01JA00000000000000000000M1")
	assert.ErrorIs(t, err, storage.ErrNotFound)
}

func TestEvents_AgentTargeted(t *testing.T) {
	r := roomed(t) // kai is a member of trip; both scout and atlas are agents
	_, err := r.PutEvent(ctx, ev(trip.ID, "human"))
	require.NoError(t, err)
	id, err := r.PutEvent(ctx, storage.Event{RoomID: trip.ID, Kind: "agent.assigned", SubjectID: "s", AgentID: sp(scout.ID), CreatedAtMs: 9})
	require.NoError(t, err)

	// ListEventsForAgent(scout) returns the targeted event, not the NULL-agent row.
	got, err := r.ListEventsForAgent(ctx, scout.ID, 0, 100)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.EqualValues(t, id, got[0].ID)
	require.NotNil(t, got[0].AgentID)
	assert.Equal(t, scout.ID, *got[0].AgentID)

	// ListEventsForHuman still lists the human row but not the agent-targeted one.
	mine, err := r.ListEventsForHuman(ctx, kai.ID, 0, 100)
	require.NoError(t, err)
	subjects := func(es []storage.Event) (out []string) {
		for _, e := range es {
			out = append(out, e.SubjectID)
		}
		return
	}
	assert.Equal(t, []string{"human"}, subjects(mine))

	// ListEventsForAgent for atlas returns nothing.
	none, err := r.ListEventsForAgent(ctx, atlas.ID, 0, 100)
	require.NoError(t, err)
	assert.NotNil(t, none)
	assert.Empty(t, none)
}

// Review Focus 4.
func TestDeleteHuman_WithRoomsAndMessages(t *testing.T) {
	r := roomed(t)
	inDM := msg("1", dm.ID, kai.ID)
	kais := msg("2", trip.ID, kai.ID)
	adas := msg("3", trip.ID, ada.ID)
	adas.ReplyToID = sp(kais.ID)
	for _, m := range []storage.Message{inDM, kais, adas} {
		require.NoError(t, r.PutMessage(ctx, m))
	}
	for _, e := range []storage.Event{ev(trip.ID, kais.ID), ev(dm.ID, inDM.ID)} { // ids 1, 2
		_, err := r.PutEvent(ctx, e)
		require.NoError(t, err)
	}

	require.NoError(t, r.DeleteHuman(ctx, kai.ID))
	_, err := r.GetRoom(ctx, dm.ID)
	assert.ErrorIs(t, err, storage.ErrNotFound) // the DM went with its human
	_, err = r.GetMessage(ctx, inDM.ID)
	assert.ErrorIs(t, err, storage.ErrNotFound) // and its messages
	_, err = r.GetRoom(ctx, trip.ID)
	assert.NoError(t, err) // the shared room stays
	got, err := r.GetMessage(ctx, kais.ID)
	require.NoError(t, err)
	assert.Nil(t, got.AuthorHumanID)
	assert.Nil(t, got.AuthorAgentID)
	assert.Equal(t, kais.Body, got.Body)
	reply, _ := r.GetMessage(ctx, adas.ID)
	assert.Equal(t, adas, reply) // ada's reply is untouched
	hs, _ := r.ListRoomHumans(ctx, trip.ID)
	assert.Equal(t, []storage.Human{ada}, hs)

	id, err := r.PutEvent(ctx, ev(trip.ID, "later"))
	require.NoError(t, err)
	assert.EqualValues(t, 3, id, "event 2 was deleted with the DM; its id must not be handed out again")
}
