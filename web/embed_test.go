package web

import (
	"io/fs"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDist_IsRootedAtTheBuildDirectory(t *testing.T) {
	_, err := fs.Stat(Dist(), ".gitkeep")
	require.NoError(t, err)
	_, err = fs.Stat(Dist(), "dist")
	assert.ErrorIs(t, err, fs.ErrNotExist)
}
