package bus_test

import (
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/argon2"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/bus"
)

const pw = "correct horse battery"

func TestHashPassword_FormatAndVerify(t *testing.T) {
	h, err := bus.HashPassword(pw)
	require.NoError(t, err)
	assert.Regexp(t, `^\$argon2id\$v=19\$m=19456,t=2,p=1\$[A-Za-z0-9+/]{22}\$[A-Za-z0-9+/]{43}$`, h)
	assert.True(t, bus.VerifyPassword(h, pw))
	assert.False(t, bus.VerifyPassword(h, pw+"x"))
	assert.False(t, bus.VerifyPassword(h, ""))
	h2, _ := bus.HashPassword(pw)
	assert.NotEqual(t, h, h2) // fresh salt each time
}

func TestVerifyPassword_HonorsStoredParameters(t *testing.T) {
	salt := []byte("saltsaltsaltsalt")
	b64 := base64.RawStdEncoding.EncodeToString
	weak := "$argon2id$v=19$m=8,t=1,p=1$" + b64(salt) + "$" + b64(argon2.IDKey([]byte(pw), salt, 1, 8, 1, 32))
	assert.True(t, bus.VerifyPassword(weak, pw))
	assert.False(t, bus.VerifyPassword(weak, pw+"x"))
}

func TestVerifyPassword_GarbageIsFalseNotPanic(t *testing.T) {
	for _, bad := range []string{
		"", "plain", "$argon2id$",
		"$argon2i$v=19$m=8,t=1,p=1$c2FsdA$a2V5",
		"$argon2id$v=19$m=x,t=1,p=1$c2FsdA$a2V5",
		"$argon2id$v=19$m=8,t=1,p=1$!!!$a2V5",
		"$argon2id$v=19$m=8,t=0,p=0$c2FsdA$a2V5",
	} {
		assert.False(t, bus.VerifyPassword(bad, ""), "%q", bad)
		assert.False(t, bus.VerifyPassword(bad, pw), "%q", bad)
	}
}
