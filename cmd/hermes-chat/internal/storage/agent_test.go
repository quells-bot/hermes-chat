package storage_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/storage"
	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/storage/storagetest"
)

func ms(v int64) *int64 { return &v }

var scout = storage.Agent{ID: "01JA0000000000000000000011", Handle: "scout", DisplayName: "Scout", TokenHash: "hash-scout", CreatedAtMs: 1791700000000}
var atlas = storage.Agent{ID: "01JA0000000000000000000012", Handle: "atlas", DisplayName: "Atlas", TokenHash: "hash-atlas", CreatedAtMs: 1791700000001}

func TestAgent_PutAndGetThreeWays(t *testing.T) {
	r := storagetest.NewRepo(t)
	require.NoError(t, r.PutAgent(ctx, scout))
	for _, get := range []func() (storage.Agent, error){
		func() (storage.Agent, error) { return r.GetAgentByID(ctx, scout.ID) },
		func() (storage.Agent, error) { return r.GetAgentByHandle(ctx, "scout") },
		func() (storage.Agent, error) { return r.GetAgentByTokenHash(ctx, "hash-scout") },
	} {
		got, err := get()
		require.NoError(t, err)
		assert.Equal(t, scout, got) // RevokedAtMs is nil
	}
	_, err := r.GetAgentByID(ctx, atlas.ID)
	assert.ErrorIs(t, err, storage.ErrNotFound)
	_, err = r.GetAgentByHandle(ctx, "atlas")
	assert.ErrorIs(t, err, storage.ErrNotFound)
	_, err = r.GetAgentByTokenHash(ctx, "hash-atlas")
	assert.ErrorIs(t, err, storage.ErrNotFound)
}

func TestAgent_Conflicts(t *testing.T) {
	r := storagetest.NewRepo(t)
	require.NoError(t, r.PutAgent(ctx, scout))
	require.NoError(t, r.PutAgent(ctx, atlas))
	dup := func(mut func(*storage.Agent)) error {
		a := storage.Agent{ID: "01JA0000000000000000000013", Handle: "new", DisplayName: "New", TokenHash: "hash-new"}
		mut(&a)
		return r.PutAgent(ctx, a)
	}
	assert.ErrorIs(t, dup(func(a *storage.Agent) { a.ID = scout.ID }), storage.ErrConflict)
	assert.ErrorIs(t, dup(func(a *storage.Agent) { a.Handle = "scout" }), storage.ErrConflict)
	assert.ErrorIs(t, dup(func(a *storage.Agent) { a.TokenHash = "hash-scout" }), storage.ErrConflict)
	assert.ErrorIs(t, r.SetAgentTokenHash(ctx, atlas.ID, "hash-scout"), storage.ErrConflict)
}

func TestListAgents(t *testing.T) {
	r := storagetest.NewRepo(t)
	got, err := r.ListAgents(ctx)
	require.NoError(t, err)
	assert.NotNil(t, got)
	assert.Empty(t, got)
	require.NoError(t, r.PutAgent(ctx, scout))
	require.NoError(t, r.PutAgent(ctx, atlas))
	got, err = r.ListAgents(ctx)
	require.NoError(t, err)
	assert.Equal(t, []storage.Agent{atlas, scout}, got)
}

func TestAgent_SetTokenHashAndRevokedAt(t *testing.T) {
	r := storagetest.NewRepo(t)
	require.NoError(t, r.PutAgent(ctx, scout))
	require.NoError(t, r.SetAgentTokenHash(ctx, scout.ID, "hash-2"))
	require.NoError(t, r.SetAgentRevokedAt(ctx, scout.ID, ms(42)))
	got, err := r.GetAgentByTokenHash(ctx, "hash-2") // revoked agents are still returned
	require.NoError(t, err)
	require.NotNil(t, got.RevokedAtMs)
	assert.EqualValues(t, 42, *got.RevokedAtMs)
	_, err = r.GetAgentByTokenHash(ctx, "hash-scout")
	assert.ErrorIs(t, err, storage.ErrNotFound)
	require.NoError(t, r.SetAgentRevokedAt(ctx, scout.ID, nil))
	got, err = r.GetAgentByID(ctx, scout.ID)
	require.NoError(t, err)
	assert.Nil(t, got.RevokedAtMs)
	assert.ErrorIs(t, r.SetAgentTokenHash(ctx, atlas.ID, "x"), storage.ErrNotFound)
	assert.ErrorIs(t, r.SetAgentRevokedAt(ctx, atlas.ID, ms(1)), storage.ErrNotFound)
}
