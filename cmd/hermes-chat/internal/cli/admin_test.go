package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"path/filepath"
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

// migrated returns a fresh database path with the migrations applied.
func migrated(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "test.db")
	require.NoError(t, Migrate(context.Background(), p, io.Discard))
	return p
}

// runUser runs User with a stdin string and returns its exit code and output.
func runUser(t *testing.T, dbPath, stdin string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = User(context.Background(), dbPath, args, strings.NewReader(stdin), &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestUserAddAndList(t *testing.T) {
	p := migrated(t)
	code, out, _ := runUser(t, p, pw+"\n", "add", "Kai")
	require.Equal(t, 0, code)
	require.Regexp(t, `^created user kai \([0-9A-HJKMNP-TV-Z]{26}\)\n$`, out)
	runUser(t, p, pw+"\n", "add", "ada")

	code, out, _ = runUser(t, p, "", "list")
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
	code, out, _ := runUser(t, migrated(t), "", "list")
	assert.Equal(t, 0, code)
	assert.Empty(t, out)
}

func TestUserAdd_Duplicate(t *testing.T) {
	p := migrated(t)
	runUser(t, p, pw+"\n", "add", "kai")
	code, out, errOut := runUser(t, p, pw+"\n", "add", "KAI")
	assert.Equal(t, 1, code)
	assert.Empty(t, out)
	assert.Contains(t, errOut, `user "kai" already exists`)
}

func TestUserAdd_Invalid(t *testing.T) {
	code, _, errOut := runUser(t, migrated(t), pw+"\n", "add", "kai wells")
	assert.Equal(t, 1, code)
	assert.Equal(t, "hermes-chat: "+bus.ValidationError{Message: "username must be 1-32 characters: lowercase letters, digits, '_' or '-', starting with a letter or digit"}.Message+"\n", errOut)
}

// Review Focus 4.
func TestUserAdd_UnmigratedDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	code, _, errOut := runUser(t, path, pw+"\n", "add", "kai")
	assert.Equal(t, 1, code)
	assert.Contains(t, errOut, `run "hermes-chat migrate"`)
	db, err := storage.Open(context.Background(), path)
	require.NoError(t, err)
	defer db.Close()
	assert.ErrorIs(t, db.CheckSchema(context.Background()), storage.ErrSchemaBehind) // still not migrated
}

func TestUser_UsageErrors(t *testing.T) {
	p := migrated(t)
	for _, args := range [][]string{{}, {"add"}, {"add", "a", "b"}, {"frob"},
		{"passwd"}, {"passwd", "a", "b"}, {"remove"}, {"remove", "a", "b"}} {
		code, _, errOut := runUser(t, p, "", args...)
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
	code, out, errOut := runUser(t, path, pw+"\r\n", "add", "kai")
	require.Equal(t, 0, code, errOut)
	assert.NotContains(t, out+errOut, pw)
	assert.True(t, storedPasswordMatches(t, path, "kai", pw))
}

func TestUserAdd_NoOrShortPassword(t *testing.T) {
	p := migrated(t)
	for _, in := range []string{"", "\n", "short\n"} {
		code, out, errOut := runUser(t, p, in, "add", "kai")
		assert.Equal(t, 1, code)
		assert.Empty(t, out)
		assert.Equal(t, "hermes-chat: password must be at least 10 characters and at most 256 bytes\n", errOut)
	}
	_, out, _ := runUser(t, p, "", "list")
	assert.Empty(t, out) // nobody was created
}

func TestUserPasswd(t *testing.T) {
	path := migrated(t)
	runUser(t, path, pw+"\n", "add", "kai")
	code, out, _ := runUser(t, path, "another long password\n", "passwd", "KAI")
	assert.Equal(t, 0, code)
	assert.Equal(t, "password updated for kai\n", out)
	assert.True(t, storedPasswordMatches(t, path, "kai", "another long password"))
	code, _, errOut := runUser(t, path, pw+"\n", "passwd", "nobody")
	assert.Equal(t, 1, code)
	assert.Equal(t, "hermes-chat: user \"nobody\" not found\n", errOut)
}

func TestUserRemove(t *testing.T) {
	p := migrated(t)
	runUser(t, p, pw+"\n", "add", "kai")
	code, out, _ := runUser(t, p, "", "remove", "kai")
	assert.Equal(t, 0, code)
	assert.Equal(t, "removed user kai\n", out)
	code, _, errOut := runUser(t, p, "", "remove", "kai")
	assert.Equal(t, 1, code)
	assert.Contains(t, errOut, `user "kai" not found`)
}