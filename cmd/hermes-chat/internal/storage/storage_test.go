package storage_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/storage"
	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/storage/storagetest"
)

var kai = storage.Human{ID: "01JA0000000000000000000001", Username: "kai", CreatedAtMs: 1791700000000}
var ada = storage.Human{ID: "01JA0000000000000000000002", Username: "ada", CreatedAtMs: 1791700000001}

func TestPing(t *testing.T) { assert.NoError(t, storagetest.NewRepo(t).Ping(ctx)) }

func TestPutAndGetHuman(t *testing.T) {
	r := storagetest.NewRepo(t)
	require.NoError(t, r.PutHuman(ctx, kai))
	got, err := r.GetHumanByUsername(ctx, "kai")
	require.NoError(t, err)
	assert.Equal(t, kai, got)
}

func TestGetHuman_NotFound(t *testing.T) {
	_, err := storagetest.NewRepo(t).GetHumanByUsername(ctx, "nobody")
	assert.ErrorIs(t, err, storage.ErrNotFound)
}

func TestPutHuman_Conflict(t *testing.T) {
	r := storagetest.NewRepo(t)
	require.NoError(t, r.PutHuman(ctx, kai))
	sameName := storage.Human{ID: ada.ID, Username: "kai"}
	sameID := storage.Human{ID: kai.ID, Username: "other"}
	assert.ErrorIs(t, r.PutHuman(ctx, sameName), storage.ErrConflict)
	assert.ErrorIs(t, r.PutHuman(ctx, sameID), storage.ErrConflict)
}

func TestListHumans_OrderedByUsername(t *testing.T) {
	r := storagetest.NewRepo(t)
	require.NoError(t, r.PutHuman(ctx, kai))
	require.NoError(t, r.PutHuman(ctx, ada))
	got, err := r.ListHumans(ctx)
	require.NoError(t, err)
	assert.Equal(t, []storage.Human{ada, kai}, got)
}

func TestListHumans_EmptyIsEmptyNotNil(t *testing.T) {
	got, err := storagetest.NewRepo(t).ListHumans(ctx)
	require.NoError(t, err)
	assert.NotNil(t, got)
	assert.Empty(t, got)
}

func TestInTx_CommitsTogether(t *testing.T) {
	r := storagetest.NewRepo(t)
	require.NoError(t, r.InTx(ctx, func(tx storage.Repository) error {
		if err := tx.PutHuman(ctx, kai); err != nil {
			return err
		}
		return tx.PutHuman(ctx, ada)
	}))
	got, _ := r.ListHumans(ctx)
	assert.Len(t, got, 2)
}

func TestInTx_RollsBackAndReturnsTheCallbackError(t *testing.T) {
	r := storagetest.NewRepo(t)
	boom := errors.New("boom")
	err := r.InTx(ctx, func(tx storage.Repository) error {
		require.NoError(t, tx.PutHuman(ctx, kai))
		return boom
	})
	assert.ErrorIs(t, err, boom)
	got, _ := r.ListHumans(ctx)
	assert.Empty(t, got)
}

func TestInTx_ReadsSeeOwnWrites(t *testing.T) {
	r := storagetest.NewRepo(t)
	require.NoError(t, r.InTx(ctx, func(tx storage.Repository) error {
		require.NoError(t, tx.PutHuman(ctx, kai))
		_, err := tx.GetHumanByUsername(ctx, "kai")
		assert.NoError(t, err) // inside: visible
		_, err = r.GetHumanByUsername(ctx, "kai")
		assert.ErrorIs(t, err, storage.ErrNotFound) // outside: not yet
		return nil
	}))
}

func TestInTx_NestedIsAnError(t *testing.T) {
	r := storagetest.NewRepo(t)
	err := r.InTx(ctx, func(tx storage.Repository) error {
		return tx.InTx(ctx, func(storage.Repository) error { return nil })
	})
	assert.ErrorIs(t, err, storage.ErrNestedTx)
}

// Review Focus 1: a panic must not leave the only write connection checked out.
func TestInTx_PanicReleasesTheWriteConnection(t *testing.T) {
	r := storagetest.NewRepo(t)
	assert.Panics(t, func() {
		_ = r.InTx(ctx, func(tx storage.Repository) error {
			require.NoError(t, tx.PutHuman(ctx, kai))
			panic("boom")
		})
	})
	short, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	require.NoError(t, r.PutHuman(short, ada)) // would block forever if the connection leaked
	got, _ := r.ListHumans(ctx)
	assert.Equal(t, []storage.Human{ada}, got) // kai rolled back
}

// BEGIN IMMEDIATE: the write lock is held from the start of the transaction,
// before the callback writes anything.
func TestInTx_TakesWriteLockAtBegin(t *testing.T) {
	db := storagetest.Open(t)
	r := storage.NewRepo(db)
	path := db.Path()
	other, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(0)")
	require.NoError(t, err)
	defer other.Close()
	require.NoError(t, r.InTx(ctx, func(storage.Repository) error {
		_, err := other.ExecContext(ctx, "BEGIN IMMEDIATE")
		assert.Error(t, err) // SQLITE_BUSY: our transaction already holds the lock
		return nil
	}))
}

func TestInTx_CancelledContext(t *testing.T) {
	r := storagetest.NewRepo(t)
	c, cancel := context.WithCancel(ctx)
	cancel()
	called := false
	err := r.InTx(c, func(storage.Repository) error { called = true; return nil })
	assert.Error(t, err)
	assert.False(t, called)
}
