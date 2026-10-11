package storage_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/storage"
	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/storage/storagetest"
)

// sess builds a session whose ID and token hash are derived from n.
func sess(n, humanID string, expiresAtMs int64) storage.Session {
	return storage.Session{ID: "01JA00000000000000000000S" + n, TokenHash: "hash-" + n, HumanID: humanID, CreatedAtMs: 1, ExpiresAtMs: expiresAtMs}
}

func TestSession_PutGetDelete(t *testing.T) {
	r := storagetest.NewRepo(t)
	require.NoError(t, r.PutHuman(ctx, kai))
	s := sess("1", kai.ID, 100)
	require.NoError(t, r.PutSession(ctx, s))
	got, err := r.GetSessionByTokenHash(ctx, "hash-1")
	require.NoError(t, err)
	assert.Equal(t, s, got)
	again := sess("2", kai.ID, 100)
	again.TokenHash = "hash-1"
	assert.ErrorIs(t, r.PutSession(ctx, again), storage.ErrConflict)
	require.NoError(t, r.DeleteSessionByTokenHash(ctx, "hash-1"))
	_, err = r.GetSessionByTokenHash(ctx, "hash-1")
	assert.ErrorIs(t, err, storage.ErrNotFound)
	assert.NoError(t, r.DeleteSessionByTokenHash(ctx, "hash-1")) // absent is not an error
}

func TestSession_UnknownHumanIsRejected(t *testing.T) { // foreign keys are on
	assert.Error(t, storagetest.NewRepo(t).PutSession(ctx, sess("1", kai.ID, 100)))
}

func TestDeleteSessionsByHuman(t *testing.T) {
	r := storagetest.NewRepo(t)
	require.NoError(t, r.PutHuman(ctx, kai))
	require.NoError(t, r.PutHuman(ctx, ada))
	for _, s := range []storage.Session{sess("1", kai.ID, 100), sess("2", kai.ID, 100), sess("3", ada.ID, 100)} {
		require.NoError(t, r.PutSession(ctx, s))
	}
	require.NoError(t, r.DeleteSessionsByHuman(ctx, kai.ID))
	_, err := r.GetSessionByTokenHash(ctx, "hash-1")
	assert.ErrorIs(t, err, storage.ErrNotFound)
	_, err = r.GetSessionByTokenHash(ctx, "hash-2")
	assert.ErrorIs(t, err, storage.ErrNotFound)
	_, err = r.GetSessionByTokenHash(ctx, "hash-3")
	assert.NoError(t, err)
}

func TestDeleteExpiredSessions_BoundaryIsInclusive(t *testing.T) {
	r := storagetest.NewRepo(t)
	require.NoError(t, r.PutHuman(ctx, kai))
	require.NoError(t, r.PutSession(ctx, sess("1", kai.ID, 99)))
	require.NoError(t, r.PutSession(ctx, sess("2", kai.ID, 100)))
	require.NoError(t, r.PutSession(ctx, sess("3", kai.ID, 101)))
	require.NoError(t, r.DeleteExpiredSessions(ctx, 100))
	_, err := r.GetSessionByTokenHash(ctx, "hash-1")
	assert.ErrorIs(t, err, storage.ErrNotFound)
	_, err = r.GetSessionByTokenHash(ctx, "hash-2")
	assert.ErrorIs(t, err, storage.ErrNotFound)
	_, err = r.GetSessionByTokenHash(ctx, "hash-3")
	assert.NoError(t, err)
}
