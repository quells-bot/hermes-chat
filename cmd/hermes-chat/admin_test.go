package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/bus"
	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/storage"
)

func migrated(t *testing.T) string {
	p := setDB(t)
	c, _, _ := runCmd(t, "migrate")
	require.Equal(t, 0, c)
	return p
}

func TestUserAddAndList(t *testing.T) {
	migrated(t)
	code, out, _ := runCmd(t, "user", "add", "Kai")
	require.Equal(t, 0, code)
	require.Regexp(t, `^created user kai \([0-9A-HJKMNP-TV-Z]{26}\)\n$`, out)
	runCmd(t, "user", "add", "ada")

	code, out, _ = runCmd(t, "user", "list")
	require.Equal(t, 0, code)
	rows := strings.Split(strings.TrimSpace(out), "\n")
	require.Len(t, rows, 2)
	ada := strings.Split(rows[0], "\t")
	require.Len(t, ada, 3)
	assert.Equal(t, "ada", ada[1])
	_, err := ulid.ParseStrict(ada[0])
	assert.NoError(t, err)
	ts, err := time.Parse(time.RFC3339, ada[2])
	require.NoError(t, err)
	assert.Equal(t, time.UTC, ts.Location())
	assert.Contains(t, rows[1], "\tkai\t")
}

func TestUserList_Empty(t *testing.T) {
	migrated(t)
	code, out, _ := runCmd(t, "user", "list")
	assert.Equal(t, 0, code)
	assert.Empty(t, out)
}

func TestUserAdd_Duplicate(t *testing.T) {
	migrated(t)
	runCmd(t, "user", "add", "kai")
	code, out, errOut := runCmd(t, "user", "add", "KAI")
	assert.Equal(t, 1, code)
	assert.Empty(t, out)
	assert.Contains(t, errOut, `user "kai" already exists`)
}

func TestUserAdd_Invalid(t *testing.T) {
	migrated(t)
	code, _, errOut := runCmd(t, "user", "add", "kai wells")
	assert.Equal(t, 1, code)
	assert.Equal(t, "hermes-chat: "+bus.ValidationError{Message: "username must be 1-32 characters: lowercase letters, digits, '_' or '-', starting with a letter or digit"}.Message+"\n", errOut)
}

// Review Focus 4.
func TestUserAdd_UnmigratedDatabase(t *testing.T) {
	path := setDB(t)
	code, _, errOut := runCmd(t, "user", "add", "kai")
	assert.Equal(t, 1, code)
	assert.Contains(t, errOut, `run "hermes-chat migrate"`)
	db, err := storage.Open(context.Background(), path)
	require.NoError(t, err)
	defer db.Close()
	assert.ErrorIs(t, db.CheckSchema(context.Background()), storage.ErrSchemaBehind) // still not migrated
}

func TestUser_UsageErrors(t *testing.T) {
	migrated(t)
	for _, args := range [][]string{{"user"}, {"user", "add"}, {"user", "add", "a", "b"}, {"user", "frob"}} {
		code, _, errOut := runCmd(t, args...)
		assert.Equal(t, 2, code, args)
		assert.NotEmpty(t, errOut, args)
	}
}
