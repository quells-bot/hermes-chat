package storage_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/storage"
)

func TestActivities_RoundTrip(t *testing.T) {
	r := roomed(t)
	m1 := msg("1", trip.ID, kai.ID)
	m2 := msg("2", trip.ID, kai.ID)
	require.NoError(t, r.PutMessage(ctx, m1))
	require.NoError(t, r.PutMessage(ctx, m2))
	remark := storage.Activity{ID: "01JA00000000000000000000A1", RoomID: trip.ID, AgentID: scout.ID, MessageID: m1.ID, Kind: "remark", Text: "hi", CreatedAtMs: 100}
	tool := storage.Activity{ID: "01JA00000000000000000000A2", RoomID: trip.ID, AgentID: atlas.ID, MessageID: m1.ID, Kind: "tool", Tool: "bash", Preview: "out", Arguments: "echo hi", CreatedAtMs: 200}
	require.NoError(t, r.PutActivity(ctx, remark))
	require.NoError(t, r.PutActivity(ctx, tool))

	got, err := r.GetActivity(ctx, remark.ID)
	require.NoError(t, err)
	assert.Equal(t, remark, got)
	got, err = r.GetActivity(ctx, tool.ID)
	require.NoError(t, err)
	assert.Equal(t, tool, got)
	assert.ErrorIs(t, r.PutActivity(ctx, remark), storage.ErrConflict) // duplicate id

	require.NoError(t, r.PutActivity(ctx, storage.Activity{ID: "01JA00000000000000000000A3", RoomID: trip.ID, AgentID: scout.ID, MessageID: m2.ID, Kind: "remark", Text: "other", CreatedAtMs: 300}))
	list, err := r.ListActivities(ctx, trip.ID, m1.ID)
	require.NoError(t, err)
	assert.Equal(t, []storage.Activity{remark, tool}, list) // oldest first, only that message's
}

func TestActivities_SetActivityResult(t *testing.T) {
	r := roomed(t)
	m1 := msg("1", trip.ID, kai.ID)
	require.NoError(t, r.PutMessage(ctx, m1))
	remark := storage.Activity{ID: "01JA00000000000000000000A1", RoomID: trip.ID, AgentID: scout.ID, MessageID: m1.ID, Kind: "remark", Text: "hi", CreatedAtMs: 100}
	tool := storage.Activity{ID: "01JA00000000000000000000A2", RoomID: trip.ID, AgentID: scout.ID, MessageID: m1.ID, Kind: "tool", Tool: "bash", Preview: "out", Arguments: "echo hi", CreatedAtMs: 200}
	require.NoError(t, r.PutActivity(ctx, remark))
	require.NoError(t, r.PutActivity(ctx, tool))

	require.NoError(t, r.SetActivityResult(ctx, tool.ID, 123, true))
	got, err := r.GetActivity(ctx, tool.ID)
	require.NoError(t, err)
	require.NotNil(t, got.DurationMs)
	assert.EqualValues(t, 123, *got.DurationMs)
	require.NotNil(t, got.Succeeded)
	assert.True(t, *got.Succeeded)

	require.NoError(t, r.SetActivityResult(ctx, tool.ID, 124, false)) // idempotent update
	got, err = r.GetActivity(ctx, tool.ID)
	require.NoError(t, err)
	require.NotNil(t, got.DurationMs)
	assert.EqualValues(t, 124, *got.DurationMs)
	require.NotNil(t, got.Succeeded)
	assert.False(t, *got.Succeeded)

	assert.ErrorIs(t, r.SetActivityResult(ctx, remark.ID, 1, true), storage.ErrNotFound) // a remark
	assert.ErrorIs(t, r.SetActivityResult(ctx, "01JA00000000000000000000A9", 1, true), storage.ErrNotFound)
}
