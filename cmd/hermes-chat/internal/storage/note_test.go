package storage_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/storage"
)

func TestNoteText(t *testing.T) {
	r := seeded(t)
	got, err := r.GetNoteText(ctx, kai.ID)
	require.NoError(t, err)
	assert.Equal(t, "", got)
	require.NoError(t, r.PutNoteText(ctx, kai.ID, "I live in Lisbon.", 1))
	require.NoError(t, r.PutNoteText(ctx, kai.ID, "I live in Porto.", 2))
	got, _ = r.GetNoteText(ctx, kai.ID)
	assert.Equal(t, "I live in Porto.", got)
	other, _ := r.GetNoteText(ctx, ada.ID)
	assert.Equal(t, "", other)
}

func TestNoteEntries(t *testing.T) {
	r := seeded(t)
	none, err := r.ListNoteEntries(ctx, kai.ID)
	require.NoError(t, err)
	assert.NotNil(t, none)
	assert.Empty(t, none)

	a := storage.NoteEntry{ID: "01JA00000000000000000000N1", HumanID: kai.ID, AgentID: scout.ID, Text: "prefers trains", CreatedAtMs: 5}
	b := storage.NoteEntry{ID: "01JA00000000000000000000N2", HumanID: kai.ID, AgentID: atlas.ID, Text: "vegetarian", CreatedAtMs: 6}
	require.NoError(t, r.PutNoteEntry(ctx, b))
	require.NoError(t, r.PutNoteEntry(ctx, a))
	assert.ErrorIs(t, r.PutNoteEntry(ctx, a), storage.ErrConflict)
	list, _ := r.ListNoteEntries(ctx, kai.ID)
	assert.Equal(t, []storage.NoteEntry{a, b}, list)
	theirs, _ := r.ListNoteEntries(ctx, ada.ID)
	assert.Empty(t, theirs)

	require.NoError(t, r.SetNoteEntryApprovedAt(ctx, a.ID, ms(9)))
	require.NoError(t, r.SetNoteEntryText(ctx, a.ID, "prefers night trains"))
	got, err := r.GetNoteEntry(ctx, a.ID)
	require.NoError(t, err)
	assert.Equal(t, ms(9), got.ApprovedAtMs)
	assert.Equal(t, "prefers night trains", got.Text)
	require.NoError(t, r.SetNoteEntryApprovedAt(ctx, a.ID, nil))
	got, _ = r.GetNoteEntry(ctx, a.ID)
	assert.Nil(t, got.ApprovedAtMs)

	require.NoError(t, r.DeleteNoteEntry(ctx, a.ID))
	for _, err := range []error{
		r.DeleteNoteEntry(ctx, a.ID), r.SetNoteEntryText(ctx, a.ID, "x"), r.SetNoteEntryApprovedAt(ctx, a.ID, ms(1)),
	} {
		assert.ErrorIs(t, err, storage.ErrNotFound)
	}
	_, err = r.GetNoteEntry(ctx, a.ID)
	assert.ErrorIs(t, err, storage.ErrNotFound)

	// a removed human's note goes with them
	require.NoError(t, r.PutNoteText(ctx, kai.ID, "x", 1))
	require.NoError(t, r.DeleteHuman(ctx, kai.ID))
	_, err = r.GetNoteEntry(ctx, b.ID)
	assert.ErrorIs(t, err, storage.ErrNotFound)
}