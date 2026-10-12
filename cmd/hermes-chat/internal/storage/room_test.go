package storage_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/storage"
	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/storage/storagetest"
)

func sp(s string) *string { return &s }

var trip = storage.Room{ID: "01JA00000000000000000000R1", Topic: "Trip planning", CreatedAtMs: 1791700000100}
var dm = storage.Room{ID: "01JA00000000000000000000R2", DMHumanID: sp(kai.ID), DMAgentID: sp(scout.ID), CreatedAtMs: 1791700000200}

// seeded returns a repository holding kai, ada, scout, and atlas.
func seeded(t *testing.T) storage.Repository {
	t.Helper()
	r := storagetest.NewRepo(t)
	for _, h := range []storage.Human{kai, ada} {
		require.NoError(t, r.PutHuman(ctx, h))
	}
	for _, a := range []storage.Agent{scout, atlas} {
		require.NoError(t, r.PutAgent(ctx, a))
	}
	return r
}

func TestRoom_PutAndGet(t *testing.T) {
	r := seeded(t)
	require.NoError(t, r.PutRoom(ctx, trip))
	require.NoError(t, r.PutRoom(ctx, dm))
	got, err := r.GetRoom(ctx, trip.ID)
	require.NoError(t, err)
	assert.Equal(t, trip, got)
	got, err = r.GetRoom(ctx, dm.ID)
	require.NoError(t, err)
	assert.Equal(t, dm, got)
	got, err = r.GetDMRoom(ctx, kai.ID, scout.ID)
	require.NoError(t, err)
	assert.Equal(t, dm, got)
	_, err = r.GetDMRoom(ctx, ada.ID, scout.ID)
	assert.ErrorIs(t, err, storage.ErrNotFound)
	_, err = r.GetRoom(ctx, "01JA00000000000000000000R9")
	assert.ErrorIs(t, err, storage.ErrNotFound)
}

func TestRoom_Conflicts(t *testing.T) {
	r := seeded(t)
	require.NoError(t, r.PutRoom(ctx, trip))
	require.NoError(t, r.PutRoom(ctx, dm))
	assert.ErrorIs(t, r.PutRoom(ctx, trip), storage.ErrConflict)
	again := dm
	again.ID = "01JA00000000000000000000R3"
	assert.ErrorIs(t, r.PutRoom(ctx, again), storage.ErrConflict) // one DM per human and agent
	other := again
	other.DMHumanID = sp(ada.ID)
	assert.NoError(t, r.PutRoom(ctx, other))
	half := storage.Room{ID: "01JA00000000000000000000R4", DMHumanID: sp(kai.ID)}
	err := r.PutRoom(ctx, half)
	require.Error(t, err)
	assert.NotErrorIs(t, err, storage.ErrConflict) // the CHECK, left unmapped
}

func TestRoom_SetTopicAndArchivedAt(t *testing.T) {
	r := seeded(t)
	require.NoError(t, r.PutRoom(ctx, trip))
	require.NoError(t, r.SetRoomTopic(ctx, trip.ID, "Lisbon"))
	require.NoError(t, r.SetRoomArchivedAt(ctx, trip.ID, ms(42)))
	got, _ := r.GetRoom(ctx, trip.ID)
	assert.Equal(t, "Lisbon", got.Topic)
	require.NotNil(t, got.ArchivedAtMs)
	assert.EqualValues(t, 42, *got.ArchivedAtMs)
	require.NoError(t, r.SetRoomArchivedAt(ctx, trip.ID, nil))
	got, _ = r.GetRoom(ctx, trip.ID)
	assert.Nil(t, got.ArchivedAtMs)
	assert.ErrorIs(t, r.SetRoomTopic(ctx, dm.ID, "x"), storage.ErrNotFound)
	assert.ErrorIs(t, r.SetRoomArchivedAt(ctx, dm.ID, ms(1)), storage.ErrNotFound)
}

func TestRoomHumans(t *testing.T) {
	r := seeded(t)
	require.NoError(t, r.PutRoom(ctx, trip))
	require.NoError(t, r.PutRoom(ctx, dm))
	none, err := r.ListRoomHumans(ctx, trip.ID)
	require.NoError(t, err)
	assert.NotNil(t, none)
	assert.Empty(t, none)
	k := storage.RoomHuman{RoomID: trip.ID, HumanID: kai.ID, JoinedAtMs: 5}
	require.NoError(t, r.PutRoomHuman(ctx, k))
	require.NoError(t, r.PutRoomHuman(ctx, storage.RoomHuman{RoomID: trip.ID, HumanID: ada.ID, JoinedAtMs: 6}))
	require.NoError(t, r.PutRoomHuman(ctx, storage.RoomHuman{RoomID: dm.ID, HumanID: kai.ID, JoinedAtMs: 7}))
	assert.ErrorIs(t, r.PutRoomHuman(ctx, k), storage.ErrConflict)

	got, err := r.GetRoomHuman(ctx, trip.ID, kai.ID)
	require.NoError(t, err)
	assert.Equal(t, k, got) // LastReadMessageID is ""
	_, err = r.GetRoomHuman(ctx, dm.ID, ada.ID)
	assert.ErrorIs(t, err, storage.ErrNotFound)
	hs, _ := r.ListRoomHumans(ctx, trip.ID)
	assert.Equal(t, []storage.Human{ada, kai}, hs) // by username, no password hash

	require.NoError(t, r.SetRoomArchivedAt(ctx, trip.ID, ms(1)))
	archived := trip
	archived.ArchivedAtMs = ms(1)
	mine, _ := r.ListRoomsByHuman(ctx, kai.ID)
	assert.Equal(t, []storage.Room{archived, dm}, mine) // by id, archived included
	hers, _ := r.ListRoomsByHuman(ctx, ada.ID)
	assert.Equal(t, []storage.Room{archived}, hers)
	nobody, err := r.ListRoomsByHuman(ctx, "01JA0000000000000000000009")
	require.NoError(t, err)
	assert.NotNil(t, nobody)
	assert.Empty(t, nobody)

	require.NoError(t, r.SetRoomHumanLastRead(ctx, trip.ID, kai.ID, "01JA00000000000000000000M1"))
	got, _ = r.GetRoomHuman(ctx, trip.ID, kai.ID)
	assert.Equal(t, "01JA00000000000000000000M1", got.LastReadMessageID)
	other, _ := r.GetRoomHuman(ctx, trip.ID, ada.ID)
	assert.Equal(t, "", other.LastReadMessageID) // the marker is per human
	assert.ErrorIs(t, r.SetRoomHumanLastRead(ctx, dm.ID, ada.ID, "x"), storage.ErrNotFound)
}

func TestRoomAgents(t *testing.T) {
	r := seeded(t)
	require.NoError(t, r.PutRoom(ctx, trip))
	none, err := r.ListRoomAgents(ctx, trip.ID)
	require.NoError(t, err)
	assert.NotNil(t, none)
	assert.Empty(t, none)
	s := storage.RoomAgent{RoomID: trip.ID, AgentID: scout.ID, JoinedAtMs: 5}
	require.NoError(t, r.PutRoomAgent(ctx, s))
	require.NoError(t, r.PutRoomAgent(ctx, storage.RoomAgent{RoomID: trip.ID, AgentID: atlas.ID, JoinedAtMs: 6}))
	assert.ErrorIs(t, r.PutRoomAgent(ctx, s), storage.ErrConflict)
	as, _ := r.ListRoomAgents(ctx, trip.ID)
	assert.Equal(t, []storage.Agent{atlas, scout}, as) // by handle
	require.NoError(t, r.DeleteRoomAgent(ctx, trip.ID, scout.ID))
	as, _ = r.ListRoomAgents(ctx, trip.ID)
	assert.Equal(t, []storage.Agent{atlas}, as)
	assert.ErrorIs(t, r.DeleteRoomAgent(ctx, trip.ID, scout.ID), storage.ErrNotFound)
}

func TestRoomMembers_UnknownRoomOrMemberIsRejected(t *testing.T) { // foreign keys are on
	r := seeded(t)
	assert.Error(t, r.PutRoomHuman(ctx, storage.RoomHuman{RoomID: trip.ID, HumanID: kai.ID}))
	require.NoError(t, r.PutRoom(ctx, trip))
	assert.Error(t, r.PutRoomHuman(ctx, storage.RoomHuman{RoomID: trip.ID, HumanID: "01JA0000000000000000000009"}))
	assert.Error(t, r.PutRoomAgent(ctx, storage.RoomAgent{RoomID: trip.ID, AgentID: "01JA0000000000000000000009"}))
}
