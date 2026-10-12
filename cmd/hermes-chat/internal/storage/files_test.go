package storage_test

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/storage"
)

func TestFiles_CreateOpenRemove(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "attachments")
	f, err := storage.OpenFiles(dir)
	require.NoError(t, err)
	info, err := os.Stat(dir)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), info.Mode().Perm())

	n, err := f.Create(ctx, "01JA00000000000000000000A1", strings.NewReader("hello"))
	require.NoError(t, err)
	assert.EqualValues(t, 5, n)
	rd, err := f.Open("01JA00000000000000000000A1")
	require.NoError(t, err)
	body, _ := io.ReadAll(rd)
	require.NoError(t, rd.Close())
	assert.Equal(t, "hello", string(body))

	_, err = f.Open("01JA00000000000000000000A2")
	assert.ErrorIs(t, err, fs.ErrNotExist)
	require.NoError(t, f.Remove("01JA00000000000000000000A1"))
	require.NoError(t, f.Remove("01JA00000000000000000000A1"), "removing nothing is not an error")
	_, err = f.Open("01JA00000000000000000000A1")
	assert.ErrorIs(t, err, fs.ErrNotExist)
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("connection reset") }

func TestFiles_AFailedWriteLeavesNothing(t *testing.T) {
	dir := t.TempDir()
	f, err := storage.OpenFiles(dir)
	require.NoError(t, err)
	_, err = f.Create(ctx, "01JA00000000000000000000A1", io.MultiReader(strings.NewReader("half"), failingReader{}))
	require.Error(t, err)
	entries, _ := os.ReadDir(dir)
	assert.Empty(t, entries, "no file and no temporary file")

	gone, cancel := context.WithCancel(ctx)
	cancel()
	_, err = f.Create(gone, "01JA00000000000000000000A1", strings.NewReader("x"))
	assert.ErrorIs(t, err, context.Canceled)
	entries, _ = os.ReadDir(dir)
	assert.Empty(t, entries)
}

// Review Focus 2: a name is never a path.
func TestFiles_RejectsIDsThatAreNotPlainNames(t *testing.T) {
	f, err := storage.OpenFiles(t.TempDir())
	require.NoError(t, err)
	for _, bad := range []string{"", ".", "..", "../escape", "a/b", `a\b`, "/etc/passwd"} {
		_, err := f.Create(ctx, bad, strings.NewReader("x"))
		assert.Error(t, err, "%q", bad)
		_, err = f.Open(bad)
		assert.Error(t, err, "%q", bad)
		assert.Error(t, f.Remove(bad), "%q", bad)
	}
}