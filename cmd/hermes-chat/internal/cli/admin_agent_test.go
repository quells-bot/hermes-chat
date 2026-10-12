package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var tokenLine = regexp.MustCompile(`(?m)^token: (hca_[A-Za-z0-9_-]{43})$`)

// runAgent runs Agent and returns its exit code and output.
func runAgent(t *testing.T, dbPath string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = Agent(context.Background(), dbPath, args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestAgentAddListRevokeRotate(t *testing.T) {
	p := migrated(t)
	code, out, errOut := runAgent(t, p, "add", "@Scout", "Scout the Researcher")
	require.Equal(t, 0, code, errOut)
	require.Regexp(t, `^created agent @scout \([0-9A-HJKMNP-TV-Z]{26}\)\ntoken: hca_[A-Za-z0-9_-]{43}\n$`, out)
	first := tokenLine.FindStringSubmatch(out)[1]
	runAgent(t, p, "add", "atlas", "Atlas")

	_, out, _ = runAgent(t, p, "list")
	rows := strings.Split(strings.TrimSpace(out), "\n")
	require.Len(t, rows, 2)
	cols := strings.Split(rows[1], "\t")
	require.Len(t, cols, 5)
	assert.Equal(t, []string{"@scout", "Scout the Researcher", "active"}, cols[1:4])
	_, err := time.Parse(time.RFC3339, cols[4])
	assert.NoError(t, err)
	assert.NotContains(t, out, first) // list never shows a token

	code, out, _ = runAgent(t, p, "revoke", "scout")
	assert.Equal(t, 0, code)
	assert.Equal(t, "revoked agent @scout\n", out)
	_, out, _ = runAgent(t, p, "list")
	assert.Contains(t, out, "\trevoked\t")

	code, out, _ = runAgent(t, p, "rotate", "scout")
	assert.Equal(t, 0, code)
	require.Regexp(t, `^rotated token for @scout\ntoken: hca_[A-Za-z0-9_-]{43}\n$`, out)
	assert.NotEqual(t, first, tokenLine.FindStringSubmatch(out)[1])
	_, out, _ = runAgent(t, p, "list")
	assert.NotContains(t, out, "\trevoked\t")
}

func TestAgent_Errors(t *testing.T) {
	p := migrated(t)
	runAgent(t, p, "add", "scout", "Scout")
	code, out, errOut := runAgent(t, p, "add", "scout", "Again")
	assert.Equal(t, 1, code)
	assert.Empty(t, out)
	assert.Equal(t, "hermes-chat: agent \"@scout\" already exists\n", errOut)
	code, _, errOut = runAgent(t, p, "revoke", "nobody")
	assert.Equal(t, 1, code)
	assert.Equal(t, "hermes-chat: agent \"@nobody\" not found\n", errOut)
	code, _, errOut = runAgent(t, p, "add", "sc out", "Scout")
	assert.Equal(t, 1, code)
	assert.Contains(t, errOut, "handle must be")
	for _, args := range [][]string{{}, {"add"}, {"add", "scout"}, {"add", "a", "b", "c"}, {"revoke"}, {"rotate"}, {"list", "x"}, {"frob"}} {
		code, _, errOut := runAgent(t, p, args...)
		assert.Equal(t, 2, code, args)
		assert.NotEmpty(t, errOut, args)
	}
}

func TestAgent_UnmigratedDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	code, _, errOut := runAgent(t, path, "list")
	assert.Equal(t, 1, code)
	assert.Contains(t, errOut, `run "hermes-chat migrate"`)
}