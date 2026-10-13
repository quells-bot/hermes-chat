package web

import (
	"encoding/json"
	"io/fs"
	"regexp"
	"strings"
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

// The manifest is one of the page's own files, and every icon it names is
// one the binary serves.
func TestDist_ManifestNamesIconsThatAreServed(t *testing.T) {
	raw, err := fs.ReadFile(Dist(), "index.html")
	if err != nil {
		t.Skip("web UI not built")
	}
	m := regexp.MustCompile(`<link rel="manifest"[^>]* href="/(assets/[^"]+)"`).FindSubmatch(raw)
	require.NotNil(t, m, "no manifest link")
	body, err := fs.ReadFile(Dist(), string(m[1]))
	require.NoError(t, err)
	var manifest struct {
		StartURL string `json:"start_url"`
		Display  string `json:"display"`
		Icons    []struct{ Src, Sizes, Purpose string }
	}
	require.NoError(t, json.Unmarshal(body, &manifest))
	assert.Equal(t, "/", manifest.StartURL)
	assert.Equal(t, "standalone", manifest.Display)
	require.Len(t, manifest.Icons, 3)
	for _, icon := range manifest.Icons {
		require.True(t, strings.HasPrefix(icon.Src, "/assets/"), icon.Src)
		info, err := fs.Stat(Dist(), strings.TrimPrefix(icon.Src, "/"))
		require.NoError(t, err, icon.Src)
		assert.Positive(t, info.Size())
	}
}
