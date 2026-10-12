package storage_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/storage"
)

// msg builds message n ("1".."9") in a room, written by a human.
func msg(n, roomID, humanID string) storage.Message {
	return storage.Message{ID: "01JA00000000000000000000M" + n, RoomID: roomID, AuthorHumanID: sp(humanID), Body: "body " + n, CreatedAtMs: 1791700001000}
}

// roomed is seeded plus trip (kai and ada, scout) and dm (kai, scout).
func roomed(t *testing.T) storage.Repository {
	t.Helper()
	r := seeded(t)
	require.NoError(t, r.PutRoom(ctx, trip))
	require.NoError(t, r.PutRoom(ctx, dm))
	for _, m := range []storage.RoomHuman{
		{RoomID: trip.ID, HumanID: kai.ID},
		{RoomID: trip.ID, HumanID: ada.ID},
		{RoomID: dm.ID, HumanID: kai.ID},
	} {
		require.NoError(t, r.PutRoomHuman(ctx, m))
	}
	for _, m := range []storage.RoomAgent{
		{RoomID: trip.ID, AgentID: scout.ID},
		{RoomID: dm.ID, AgentID: scout.ID},
	} {
		require.NoError(t, r.PutRoomAgent(ctx, m))
	}
	return r
}

func TestMessage_PutAndGet(t *testing.T) {
	r := roomed(t)
	m1 := msg("1", trip.ID, kai.ID)
	require.NoError(t, r.PutMessage(ctx, m1))
	reply := msg("2", trip.ID, ada.ID)
	reply.ReplyToID = sp(m1.ID)
	require.NoError(t, r.PutMessage(ctx, reply))
	byAgent := storage.Message{ID: "01JA00000000000000000000M3", RoomID: trip.ID, AuthorAgentID: sp(scout.ID), Body: "hi"}
	require.NoError(t, r.PutMessage(ctx, byAgent))
	for _, want := range []storage.Message{m1, reply, byAgent} {
		got, err := r.GetMessage(ctx, want.ID)
		require.NoError(t, err)
		assert.Equal(t, want, got)
	}
	_, err := r.GetMessage(ctx, "01JA00000000000000000000M9")
	assert.ErrorIs(t, err, storage.ErrNotFound)
	assert.ErrorIs(t, r.PutMessage(ctx, m1), storage.ErrConflict)
}

func TestMessage_Rejected(t *testing.T) { // foreign keys and the CHECK
	r := roomed(t)
	assert.Error(t, r.PutMessage(ctx, msg("1", "01JA00000000000000000000R9", kai.ID)))
	both := msg("1", trip.ID, kai.ID)
	both.AuthorAgentID = sp(scout.ID)
	assert.Error(t, r.PutMessage(ctx, both))
	orphan := msg("1", trip.ID, kai.ID)
	orphan.ReplyToID = sp("01JA00000000000000000000M9")
	assert.Error(t, r.PutMessage(ctx, orphan))
}

func TestListMessages(t *testing.T) {
	r := roomed(t)
	none, err := r.ListMessages(ctx, trip.ID, "", 10)
	require.NoError(t, err)
	assert.NotNil(t, none)
	assert.Empty(t, none)
	_, err = r.GetLastMessageID(ctx, trip.ID)
	assert.ErrorIs(t, err, storage.ErrNotFound)
	var all []storage.Message
	for _, n := range []string{"1", "2", "3", "4", "5"} {
		m := msg(n, trip.ID, kai.ID)
		all = append(all, m)
		require.NoError(t, r.PutMessage(ctx, m))
	}
	require.NoError(t, r.PutMessage(ctx, msg("6", dm.ID, kai.ID))) // another room
	got, _ := r.ListMessages(ctx, trip.ID, "", 2)
	assert.Equal(t, []storage.Message{all[4], all[3]}, got) // newest first
	got, _ = r.ListMessages(ctx, trip.ID, all[3].ID, 10)
	assert.Equal(t, []storage.Message{all[2], all[1], all[0]}, got)
	got, _ = r.ListMessages(ctx, trip.ID, all[0].ID, 10)
	assert.Empty(t, got)
	last, err := r.GetLastMessageID(ctx, trip.ID)
	require.NoError(t, err)
	assert.Equal(t, all[4].ID, last)
}
