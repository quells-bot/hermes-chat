package bus_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/bus"
	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/storage"
	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/storage/storagetest"
)

var ctx = context.Background()

var fixed = time.Date(2026, 10, 11, 12, 0, 0, 123_000_000, time.UTC)

func newSvc(t *testing.T) bus.Service {
	return bus.NewService(storagetest.NewRepo(t), func() time.Time { return fixed })
}

func TestHealth_OK(t *testing.T) { assert.NoError(t, newSvc(t).Health(ctx)) }

func TestCreateHuman(t *testing.T) {
	h, err := newSvc(t).CreateHuman(ctx, "kai")
	require.NoError(t, err)
	assert.Equal(t, "kai", h.Username)
	assert.Equal(t, fixed, h.CreatedAt)
	assert.Equal(t, time.UTC, h.CreatedAt.Location())
	assert.Equal(t, uint64(fixed.UnixMilli()), h.ID.Time()) // the ULID's timestamp comes from the injected clock
}

func TestCreateHuman_NormalizesUsername(t *testing.T) {
	h, err := newSvc(t).CreateHuman(ctx, "  Kai_W-2 ")
	require.NoError(t, err)
	assert.Equal(t, "kai_w-2", h.Username)
}

func TestCreateHuman_Validation(t *testing.T) {
	for _, bad := range []string{"", "   ", "-kai", "_kai", "kai wells", "käi", "kai!", strings.Repeat("a", 33)} {
		_, err := newSvc(t).CreateHuman(ctx, bad)
		assert.ErrorIs(t, err, bus.ErrValidation, "%q", bad)
	}
	_, err := newSvc(t).CreateHuman(ctx, strings.Repeat("a", 32))
	assert.NoError(t, err)
}

func TestCreateHuman_DuplicateIsConflict(t *testing.T) {
	s := newSvc(t)
	_, err := s.CreateHuman(ctx, "kai")
	require.NoError(t, err)
	_, err = s.CreateHuman(ctx, "KAI")
	assert.ErrorIs(t, err, bus.ErrConflict)
	assert.NotErrorIs(t, err, storage.ErrConflict) // the bus never returns a storage sentinel
}

func TestListHumans_MapsAndOrders(t *testing.T) {
	s := newSvc(t)
	_, _ = s.CreateHuman(ctx, "kai")
	_, _ = s.CreateHuman(ctx, "ada")
	got, err := s.ListHumans(ctx)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "ada", got[0].Username)
	assert.Equal(t, "kai", got[1].Username)
	assert.Equal(t, fixed, got[0].CreatedAt)
	assert.NotEqual(t, got[0].ID, got[1].ID)
}

// Mock only to force an error the real repository cannot produce on demand.
type brokenRepo struct{ storage.Repository }

var errDisk = errors.New("disk on fire")

func (brokenRepo) Ping(context.Context) error                          { return errDisk }
func (brokenRepo) ListHumans(context.Context) ([]storage.Human, error) { return nil, errDisk }

func TestUnexpectedStorageErrorsPassThrough(t *testing.T) {
	s := bus.NewService(brokenRepo{storagetest.NewRepo(t)}, time.Now)
	assert.ErrorIs(t, s.Health(ctx), errDisk)
	_, err := s.ListHumans(ctx)
	assert.ErrorIs(t, err, errDisk)
}

// A row whose ID is not a ULID is a corrupted database, reported as an error, not a panic.
type badIDRepo struct{ storage.Repository }

func (badIDRepo) ListHumans(context.Context) ([]storage.Human, error) {
	return []storage.Human{{ID: "not-a-ulid", Username: "x"}}, nil
}

func TestListHumans_BadStoredID(t *testing.T) {
	_, err := bus.NewService(badIDRepo{storagetest.NewRepo(t)}, time.Now).ListHumans(ctx)
	assert.Error(t, err)
}

func TestValidationError(t *testing.T) {
	_, err := newSvc(t).CreateHuman(ctx, "kai wells")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "validation failed")
	assert.ErrorIs(t, err, bus.ErrValidation)
	var ve bus.ValidationError
	require.ErrorAs(t, err, &ve)
	assert.Equal(t, ve.Message, err.Error())
	assert.Contains(t, ve.Message, "username")
}

type putFailsRepo struct{ storage.Repository }

func (putFailsRepo) PutHuman(context.Context, storage.Human) error { return errDisk }

func TestUnexpectedStorageErrorsAreWrappedWithTheOperation(t *testing.T) {
	_, err := bus.NewService(putFailsRepo{storagetest.NewRepo(t)}, time.Now).CreateHuman(ctx, "kai")
	assert.ErrorIs(t, err, errDisk)
	assert.Contains(t, err.Error(), "create human: ")
	_, err = bus.NewService(brokenRepo{storagetest.NewRepo(t)}, time.Now).ListHumans(ctx)
	assert.Contains(t, err.Error(), "list humans: ")
}
