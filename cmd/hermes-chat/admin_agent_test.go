package main

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var tokenLine = regexp.MustCompile(`(?m)^token: (hca_[A-Za-z0-9_-]{43})$`)

func TestAgentAddListRevokeRotate(t *testing.T) {
	migrated(t)
	code, out, errOut := runCmd(t, "agent", "add", "@Scout", "Scout the Researcher")
	require.Equal(t, 0, code, errOut)
	require.Regexp(t, `^created agent @scout \([0-9A-HJKMNP-TV-Z]{26}\)\ntoken: hca_[A-Za-z0-9_-]{43}\n$`, out)
	first := tokenLine.FindStringSubmatch(out)[1]
	runCmd(t, "agent", "add", "atlas", "Atlas")

	_, out, _ = runCmd(t, "agent", "list")
	rows := strings.Split(strings.TrimSpace(out), "\n")
	require.Len(t, rows, 2)
	cols := strings.Split(rows[1], "\t")
	require.Len(t, cols, 5)
	assert.Equal(t, []string{"@scout", "Scout the Researcher", "active"}, cols[1:4])
	_, err := time.Parse(time.RFC3339, cols[4])
	assert.NoError(t, err)
	assert.NotContains(t, out, first) // list never shows a token

	code, out, _ = runCmd(t, "agent", "revoke", "scout")
	assert.Equal(t, 0, code)
	assert.Equal(t, "revoked agent @scout\n", out)
	_, out, _ = runCmd(t, "agent", "list")
	assert.Contains(t, out, "\trevoked\t")

	code, out, _ = runCmd(t, "agent", "rotate", "scout")
	assert.Equal(t, 0, code)
	require.Regexp(t, `^rotated token for @scout\ntoken: hca_[A-Za-z0-9_-]{43}\n$`, out)
	assert.NotEqual(t, first, tokenLine.FindStringSubmatch(out)[1])
	_, out, _ = runCmd(t, "agent", "list")
	assert.NotContains(t, out, "\trevoked\t")
}

func TestAgent_Errors(t *testing.T) {
	migrated(t)
	runCmd(t, "agent", "add", "scout", "Scout")
	code, out, errOut := runCmd(t, "agent", "add", "scout", "Again")
	assert.Equal(t, 1, code)
	assert.Empty(t, out)
	assert.Equal(t, "hermes-chat: agent \"@scout\" already exists\n", errOut)
	code, _, errOut = runCmd(t, "agent", "revoke", "nobody")
	assert.Equal(t, 1, code)
	assert.Equal(t, "hermes-chat: agent \"@nobody\" not found\n", errOut)
	code, _, errOut = runCmd(t, "agent", "add", "sc out", "Scout")
	assert.Equal(t, 1, code)
	assert.Contains(t, errOut, "handle must be")
	for _, args := range [][]string{{"agent"}, {"agent", "add"}, {"agent", "add", "scout"}, {"agent", "add", "a", "b", "c"}, {"agent", "revoke"}, {"agent", "rotate"}, {"agent", "list", "x"}, {"agent", "frob"}} {
		code, _, errOut := runCmd(t, args...)
		assert.Equal(t, 2, code, args)
		assert.NotEmpty(t, errOut, args)
	}
}

func TestAgent_UnmigratedDatabase(t *testing.T) {
	setDB(t)
	code, _, errOut := runCmd(t, "agent", "list")
	assert.Equal(t, 1, code)
	assert.Contains(t, errOut, `run "hermes-chat migrate"`)
}
