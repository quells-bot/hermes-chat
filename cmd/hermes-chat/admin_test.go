package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/argon2"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/bus"
	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/storage"
)

const pw = "correct horse battery"

func migrated(t *testing.T) string {
	p := setDB(t)
	c, _, _ := runCmd(t, "migrate")
	require.Equal(t, 0, c)
	return p
}

func TestUserAddAndList(t *testing.T) {
	migrated(t)
	code, out, _ := runCmdIn(t, pw+"\n", "user", "add", "Kai")
	require.Equal(t, 0, code)
	require.Regexp(t, `^created user kai \([0-9A-HJKMNP-TV-Z]{26}\)\n$`, out)
	runCmdIn(t, pw+"\n", "user", "add", "ada")

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
	runCmdIn(t, pw+"\n", "user", "add", "kai")
	code, out, errOut := runCmdIn(t, pw+"\n", "user", "add", "KAI")
	assert.Equal(t, 1, code)
	assert.Empty(t, out)
	assert.Contains(t, errOut, `user "kai" already exists`)
}

func TestUserAdd_Invalid(t *testing.T) {
	migrated(t)
	code, _, errOut := runCmdIn(t, pw+"\n", "user", "add", "kai wells")
	assert.Equal(t, 1, code)
	assert.Equal(t, "hermes-chat: "+bus.ValidationError{Message: "username must be 1-32 characters: lowercase letters, digits, '_' or '-', starting with a letter or digit"}.Message+"\n", errOut)
}

// Review Focus 4.
func TestUserAdd_UnmigratedDatabase(t *testing.T) {
	path := setDB(t)
	code, _, errOut := runCmdIn(t, pw+"\n", "user", "add", "kai")
	assert.Equal(t, 1, code)
	assert.Contains(t, errOut, `run "hermes-chat migrate"`)
	db, err := storage.Open(context.Background(), path)
	require.NoError(t, err)
	defer db.Close()
	assert.ErrorIs(t, db.CheckSchema(context.Background()), storage.ErrSchemaBehind) // still not migrated
}

func TestUser_UsageErrors(t *testing.T) {
	migrated(t)
	for _, args := range [][]string{{"user"}, {"user", "add"}, {"user", "add", "a", "b"}, {"user", "frob"},
		{"user", "passwd"}, {"user", "passwd", "a", "b"}, {"user", "remove"}, {"user", "remove", "a", "b"}} {
		code, _, errOut := runCmd(t, args...)
		assert.Equal(t, 2, code, args)
		assert.NotEmpty(t, errOut, args)
	}
}

// storedPasswordMatches checks the stored hash without using the code under test.
func storedPasswordMatches(t *testing.T, path, username, password string) bool {
	t.Helper()
	db, err := storage.Open(context.Background(), path)
	require.NoError(t, err)
	defer db.Close()
	row, err := storage.NewRepo(db).GetHumanByUsername(context.Background(), username)
	require.NoError(t, err)
	parts := strings.Split(row.PasswordHash, "$")
	require.Len(t, parts, 6, row.PasswordHash)
	require.Equal(t, "argon2id", parts[1])
	var m, tm uint32
	var p uint8
	_, err = fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &tm, &p)
	require.NoError(t, err)
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	require.NoError(t, err)
	key, err := base64.RawStdEncoding.DecodeString(parts[5])
	require.NoError(t, err)
	return string(argon2.IDKey([]byte(password), salt, tm, m, p, uint32(len(key)))) == string(key)
}

func TestUserAdd_ReadsPasswordFromStdin(t *testing.T) {
	path := migrated(t)
	code, out, errOut := runCmdIn(t, pw+"\r\n", "user", "add", "kai")
	require.Equal(t, 0, code, errOut)
	assert.NotContains(t, out+errOut, pw)
	assert.True(t, storedPasswordMatches(t, path, "kai", pw))
}

func TestUserAdd_NoOrShortPassword(t *testing.T) {
	migrated(t)
	for _, in := range []string{"", "\n", "short\n"} {
		code, out, errOut := runCmdIn(t, in, "user", "add", "kai")
		assert.Equal(t, 1, code)
		assert.Empty(t, out)
		assert.Equal(t, "hermes-chat: password must be at least 10 characters and at most 256 bytes\n", errOut)
	}
	_, out, _ := runCmd(t, "user", "list")
	assert.Empty(t, out) // nobody was created
}

func TestUserPasswd(t *testing.T) {
	path := migrated(t)
	runCmdIn(t, pw+"\n", "user", "add", "kai")
	code, out, _ := runCmdIn(t, "another long password\n", "user", "passwd", "KAI")
	assert.Equal(t, 0, code)
	assert.Equal(t, "password updated for kai\n", out)
	assert.True(t, storedPasswordMatches(t, path, "kai", "another long password"))
	code, _, errOut := runCmdIn(t, pw+"\n", "user", "passwd", "nobody")
	assert.Equal(t, 1, code)
	assert.Equal(t, "hermes-chat: user \"nobody\" not found\n", errOut)
}

func TestUserRemove(t *testing.T) {
	migrated(t)
	runCmdIn(t, pw+"\n", "user", "add", "kai")
	code, out, _ := runCmd(t, "user", "remove", "kai")
	assert.Equal(t, 0, code)
	assert.Equal(t, "removed user kai\n", out)
	code, _, errOut := runCmd(t, "user", "remove", "kai")
	assert.Equal(t, 1, code)
	assert.Contains(t, errOut, `user "kai" not found`)
}
