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

func TestMessage_ClientIDRoundTrips(t *testing.T) {
	r := roomed(t)
	keyed := msg("1", trip.ID, kai.ID)
	keyed.ClientID = sp("k1")
	require.NoError(t, r.PutMessage(ctx, keyed))
	plain := msg("2", trip.ID, kai.ID)
	require.NoError(t, r.PutMessage(ctx, plain))

	got, err := r.GetMessageByClientID(ctx, trip.ID, kai.ID, "k1")
	require.NoError(t, err)
	assert.Equal(t, keyed, got)
	got, err = r.GetMessage(ctx, plain.ID)
	require.NoError(t, err)
	assert.Nil(t, got.ClientID)
	_, err = r.GetMessageByClientID(ctx, trip.ID, kai.ID, "nope")
	assert.ErrorIs(t, err, storage.ErrNotFound)
}

func TestMessage_ClientIDIsUniquePerRoomAndHuman(t *testing.T) {
	r := roomed(t)
	keyed := func(n, roomID, humanID string) storage.Message {
		m := msg(n, roomID, humanID)
		m.ClientID = sp("k1")
		return m
	}
	require.NoError(t, r.PutMessage(ctx, keyed("1", trip.ID, kai.ID)))
	assert.ErrorIs(t, r.PutMessage(ctx, keyed("2", trip.ID, kai.ID)), storage.ErrConflict)
	assert.NoError(t, r.PutMessage(ctx, keyed("3", trip.ID, ada.ID))) // another human
	assert.NoError(t, r.PutMessage(ctx, keyed("4", dm.ID, kai.ID)))   // another room
	assert.NoError(t, r.PutMessage(ctx, msg("5", trip.ID, kai.ID)))   // no key never collides
	assert.NoError(t, r.PutMessage(ctx, msg("6", trip.ID, kai.ID)))
}

func TestMessage_StreamingList(t *testing.T) {
	r := roomed(t)
	byScout := func(n string) storage.Message {
		return storage.Message{ID: "01JA00000000000000000000M" + n, RoomID: trip.ID, AuthorAgentID: sp(scout.ID), Body: "body " + n, CreatedAtMs: 1791700001000}
	}
	m1, m2, m3 := byScout("1"), byScout("2"), byScout("3")
	for _, m := range []storage.Message{m1, m2, m3} {
		require.NoError(t, r.PutMessage(ctx, m))
	}
	require.NoError(t, r.SetMessageBody(ctx, m2.ID, "partial", 1791700002000))
	require.NoError(t, r.SetMessageBody(ctx, m3.ID, "partial", 1791700002000))
	require.NoError(t, r.FinalizeMessage(ctx, m3.ID, "whole", 1791700003000))

	got, err := r.ListStreamingMessages(ctx, trip.ID, scout.ID, "")
	require.NoError(t, err)
	require.Len(t, got, 1) // m1 was never edited, m3 is finalized
	assert.Equal(t, m2.ID, got[0].ID)
	assert.Equal(t, "partial", got[0].Body)
	assert.Equal(t, ms(1791700002000), got[0].EditedAtMs)

	after, err := r.ListStreamingMessages(ctx, trip.ID, scout.ID, m2.ID)
	require.NoError(t, err)
	assert.NotNil(t, after)
	assert.Empty(t, after)
	other, err := r.ListStreamingMessages(ctx, trip.ID, atlas.ID, "")
	require.NoError(t, err)
	assert.Empty(t, other)
}
