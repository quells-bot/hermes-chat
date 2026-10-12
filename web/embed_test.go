package web

import (
	"io/fs"
	"regexp"
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

// The content security policy forbids inline script, and the page must load
// only its own built files. Checked only when the UI has been built.
func TestDist_EntryPageHasNoInlineScriptOrStyle(t *testing.T) {
	raw, err := fs.ReadFile(Dist(), "index.html")
	if err != nil {
		t.Skip("web UI not built")
	}
	page := string(raw)
	for _, tag := range regexp.MustCompile(`<script[^>]*>`).FindAllString(page, -1) {
		assert.Contains(t, tag, " src=", "inline script: %s", tag)
	}
	assert.NotContains(t, page, "<style")
	assert.NotContains(t, page, " style=")
	refs := regexp.MustCompile(`(?:src|href)="([^"]*)"`).FindAllStringSubmatch(page, -1)
	require.NotEmpty(t, refs)
	for _, ref := range refs {
		assert.Regexp(t, `^/assets/`, ref[1])
	}
}
