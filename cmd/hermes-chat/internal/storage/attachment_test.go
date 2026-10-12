package storage_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/storage"
)

func TestAttachment_PutAttachAndList(t *testing.T) {
	r := roomed(t)
	m1, m2 := msg("1", trip.ID, kai.ID), msg("2", trip.ID, kai.ID)
	require.NoError(t, r.PutMessage(ctx, m1))
	require.NoError(t, r.PutMessage(ctx, m2))
	a := storage.Attachment{ID: "01JA00000000000000000000A1", RoomID: trip.ID, UploaderAgentID: sp(scout.ID),
		Name: "report.pdf", ContentType: "application/pdf", Size: 1234, CreatedAtMs: 5}
	b := storage.Attachment{ID: "01JA00000000000000000000A2", RoomID: trip.ID, UploaderAgentID: sp(scout.ID),
		Name: "cat.png", ContentType: "image/png", Size: 99, CreatedAtMs: 6}
	require.NoError(t, r.PutAttachment(ctx, a))
	require.NoError(t, r.PutAttachment(ctx, b))
	assert.ErrorIs(t, r.PutAttachment(ctx, a), storage.ErrConflict)
	got, err := r.GetAttachment(ctx, a.ID)
	require.NoError(t, err)
	assert.Equal(t, a, got)
	_, err = r.GetAttachment(ctx, "nope")
	assert.ErrorIs(t, err, storage.ErrNotFound)

	none, err := r.ListAttachmentsInRange(ctx, trip.ID, m1.ID, m2.ID)
	require.NoError(t, err)
	assert.NotNil(t, none)
	assert.Empty(t, none, "an upload on no message is in no range")

	require.NoError(t, r.AttachToMessage(ctx, b.ID, m1.ID))
	require.NoError(t, r.AttachToMessage(ctx, a.ID, m1.ID))
	assert.ErrorIs(t, r.AttachToMessage(ctx, a.ID, m2.ID), storage.ErrNotFound, "an attachment is posted once")
	assert.ErrorIs(t, r.AttachToMessage(ctx, "nope", m2.ID), storage.ErrNotFound)
	a.MessageID, b.MessageID = sp(m1.ID), sp(m1.ID)
	list, _ := r.ListAttachmentsInRange(ctx, trip.ID, m1.ID, m2.ID)
	assert.Equal(t, []storage.Attachment{a, b}, list)
	later, _ := r.ListAttachmentsInRange(ctx, trip.ID, m2.ID, m2.ID)
	assert.Empty(t, later)
}