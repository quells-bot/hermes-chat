package storage_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/storage/storagetest"
)

func TestHalt(t *testing.T) {
	r := storagetest.NewRepo(t)
	got, err := r.GetHaltedAtMs(ctx)
	require.NoError(t, err)
	assert.Nil(t, got) // a migrated database is not halted
	require.NoError(t, r.SetHaltedAtMs(ctx, ms(7)))
	got, _ = r.GetHaltedAtMs(ctx)
	assert.Equal(t, ms(7), got)
	require.NoError(t, r.SetHaltedAtMs(ctx, nil))
	got, _ = r.GetHaltedAtMs(ctx)
	assert.Nil(t, got)
}
