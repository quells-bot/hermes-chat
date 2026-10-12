package storage_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/storage"
)

// prompted holds one open prompt by scout in trip, on message "1".
func prompted(t *testing.T) (storage.Repository, storage.Prompt) {
	r := roomed(t)
	m := msg("1", trip.ID, kai.ID)
	require.NoError(t, r.PutMessage(ctx, m))
	p := storage.Prompt{MessageID: m.ID, RoomID: trip.ID, AgentID: scout.ID, Kind: "approval", Choices: `["once","deny"]`}
	require.NoError(t, r.PutPrompt(ctx, p))
	p.State = "open"
	return r, p
}

func TestPrompt_PutGetAndClose(t *testing.T) {
	r, p := prompted(t)
	got, err := r.GetPrompt(ctx, p.MessageID)
	require.NoError(t, err)
	assert.Equal(t, p, got)
	assert.ErrorIs(t, r.PutPrompt(ctx, p), storage.ErrConflict)
	_, err = r.GetPrompt(ctx, "nope")
	assert.ErrorIs(t, err, storage.ErrNotFound)

	n, _ := r.CountOpenPrompts(ctx, trip.ID)
	assert.EqualValues(t, 1, n)
	require.NoError(t, r.ClosePrompt(ctx, p.MessageID, "answered", "once", sp(kai.ID), 7))
	got, _ = r.GetPrompt(ctx, p.MessageID)
	assert.Equal(t, "answered", got.State)
	assert.Equal(t, "once", got.Answer)
	assert.Equal(t, sp(kai.ID), got.AnsweredBy)
	assert.Equal(t, ms(7), got.ClosedAtMs)
	n, _ = r.CountOpenPrompts(ctx, trip.ID)
	assert.EqualValues(t, 0, n)
	// a closed prompt cannot be closed again: the first outcome stands
	assert.ErrorIs(t, r.ClosePrompt(ctx, p.MessageID, "expired", "", nil, 8), storage.ErrNotFound)
	assert.ErrorIs(t, r.ClosePrompt(ctx, "nope", "expired", "", nil, 8), storage.ErrNotFound)
}

func TestPrompt_Lists(t *testing.T) {
	r, p := prompted(t)
	m2 := msg("2", trip.ID, kai.ID)
	require.NoError(t, r.PutMessage(ctx, m2)) // a plain message between prompts
	in, err := r.ListPromptsInRange(ctx, trip.ID, p.MessageID, m2.ID)
	require.NoError(t, err)
	assert.Equal(t, []storage.Prompt{p}, in)
	none, err := r.ListPromptsInRange(ctx, trip.ID, m2.ID, m2.ID)
	require.NoError(t, err)
	assert.NotNil(t, none)
	assert.Empty(t, none)

	room, _ := r.ListOpenPrompts(ctx, trip.ID)
	all, _ := r.ListOpenPrompts(ctx, "")
	assert.Equal(t, []storage.Prompt{p}, room)
	assert.Equal(t, room, all)
	elsewhere, _ := r.ListOpenPrompts(ctx, dm.ID)
	assert.NotNil(t, elsewhere)
	assert.Empty(t, elsewhere)
	require.NoError(t, r.ClosePrompt(ctx, p.MessageID, "expired", "", nil, 9))
	all, _ = r.ListOpenPrompts(ctx, "")
	assert.Empty(t, all)
}