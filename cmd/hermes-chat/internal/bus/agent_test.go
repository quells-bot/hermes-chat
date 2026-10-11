package bus_test

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/bus"
	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/storage"
	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/storage/storagetest"
)

func TestCreateAgent(t *testing.T) {
	s, r := newSvcRepo(t)
	a, token, err := s.CreateAgent(ctx, " @Scout ", "  Scout the Researcher ")
	require.NoError(t, err)
	assert.Equal(t, "scout", a.Handle)
	assert.Equal(t, "Scout the Researcher", a.DisplayName)
	assert.Equal(t, fixed, a.CreatedAt)
	assert.False(t, a.Revoked())
	assert.Regexp(t, `^hca_[A-Za-z0-9_-]{43}$`, token)
	row, _ := r.GetAgentByHandle(ctx, "scout")
	sum := sha256.Sum256([]byte(token))
	assert.Equal(t, hex.EncodeToString(sum[:]), row.TokenHash) // only the hash is stored
	_, token2, _ := s.CreateAgent(ctx, "atlas", "Atlas")
	assert.NotEqual(t, token, token2)
}

func TestCreateAgent_ValidationAndConflict(t *testing.T) {
	s := newSvc(t)
	for _, bad := range []string{"", "@", "@@scout", "-scout", "sc out", strings.Repeat("a", 33)} {
		_, _, err := s.CreateAgent(ctx, bad, "Scout")
		assert.ErrorIs(t, err, bus.ErrValidation, "%q", bad)
	}
	for _, bad := range []string{"", "   ", "line\nbreak", "tab\tbed", strings.Repeat("é", 65)} {
		_, _, err := s.CreateAgent(ctx, "scout", bad)
		assert.ErrorIs(t, err, bus.ErrValidation, "%q", bad)
	}
	_, _, err := s.CreateAgent(ctx, "scout", strings.Repeat("é", 64))
	require.NoError(t, err)
	_, _, err = s.CreateAgent(ctx, "@SCOUT", "Other")
	assert.ErrorIs(t, err, bus.ErrConflict)
	assert.NotErrorIs(t, err, storage.ErrConflict)
}

func TestResolveAgentToken(t *testing.T) {
	s := newSvc(t)
	a, token, _ := s.CreateAgent(ctx, "scout", "Scout")
	actor, err := s.ResolveAgentToken(ctx, token)
	require.NoError(t, err)
	assert.Equal(t, bus.Actor{Kind: bus.ActorAgent, ID: a.ID}, actor)
	assert.Equal(t, "agent:"+a.ID.String(), actor.String())
	for _, bad := range []string{"", "hca_", token + "x", token[:len(token)-1], strings.ToUpper(token), " " + token} {
		_, err := s.ResolveAgentToken(ctx, bad)
		assert.ErrorIs(t, err, bus.ErrUnauthenticated, "%q", bad)
	}
}

func TestRevokeAndRotate(t *testing.T) {
	clock := fixed
	s := bus.NewService(storagetest.NewRepo(t), func() time.Time { return clock })
	a, t1, _ := s.CreateAgent(ctx, "scout", "Scout")

	require.NoError(t, s.RevokeAgent(ctx, "@scout"))
	_, err := s.ResolveAgentToken(ctx, t1)
	assert.ErrorIs(t, err, bus.ErrUnauthenticated)
	got, _ := s.GetAgent(ctx, a.ID)
	assert.True(t, got.Revoked())
	assert.Equal(t, fixed, got.RevokedAt)
	clock = fixed.Add(time.Hour)
	require.NoError(t, s.RevokeAgent(ctx, "scout"))
	got, _ = s.GetAgent(ctx, a.ID)
	assert.Equal(t, fixed, got.RevokedAt) // the first revocation time is kept

	t2, err := s.RotateAgentToken(ctx, "scout")
	require.NoError(t, err)
	assert.NotEqual(t, t1, t2)
	_, err = s.ResolveAgentToken(ctx, t1)
	assert.ErrorIs(t, err, bus.ErrUnauthenticated) // old token is dead
	actor, err := s.ResolveAgentToken(ctx, t2)
	require.NoError(t, err)
	assert.Equal(t, a.ID, actor.ID) // rotation un-revokes
	got, _ = s.GetAgent(ctx, a.ID)
	assert.False(t, got.Revoked())

	assert.ErrorIs(t, s.RevokeAgent(ctx, "nobody"), bus.ErrNotFound)
	_, err = s.RotateAgentToken(ctx, "no body")
	assert.ErrorIs(t, err, bus.ErrNotFound)
	_, err = s.GetAgent(ctx, ulid.Make())
	assert.ErrorIs(t, err, bus.ErrNotFound)
}

func TestListAgents_OrderedByHandle(t *testing.T) {
	s := newSvc(t)
	_, _, _ = s.CreateAgent(ctx, "scout", "Scout")
	_, _, _ = s.CreateAgent(ctx, "atlas", "Atlas")
	got, err := s.ListAgents(ctx)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "atlas", got[0].Handle)
	assert.Equal(t, "scout", got[1].Handle)
}

func TestHumanActorString(t *testing.T) {
	id := ulid.Make()
	assert.Equal(t, "human:"+id.String(), bus.Actor{Kind: bus.ActorHuman, ID: id}.String())
}
