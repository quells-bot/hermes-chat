package bus

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strings"
	"sync/atomic"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// argon2id parameters for new hashes. Stored hashes carry their own, so these
// can be raised later without locking anyone out.
const (
	argonMemoryKiB = 19456
	argonTime      = 2
	argonThreads   = 1
	argonSaltLen   = 16
	argonKeyLen    = 32
)

const passwordMessage = "password must be at least 10 characters and at most 256 bytes"

func validatePassword(password string) error {
	if utf8.RuneCountInString(password) < 10 || len(password) > 256 {
		return ValidationError{Message: passwordMessage}
	}
	return nil
}

// maxConcurrentArgon caps how many argon2 computations run at once. Each one
// allocates argonMemoryKiB, so an unbounded burst of logins could exhaust memory.
const maxConcurrentArgon = 2

// argonLimiter is a counting semaphore for argon2 work. inflight, peak and
// waiting exist so tests can observe it.
type argonLimiter struct {
	slots    chan struct{}
	inflight atomic.Int32
	peak     atomic.Int32
	waiting  atomic.Int32
}

func newArgonLimiter() argonLimiter {
	return argonLimiter{slots: make(chan struct{}, maxConcurrentArgon)}
}

// acquire waits for a free slot or for ctx to be done. On success the caller
// must call the returned release, ideally with defer.
func (l *argonLimiter) acquire(ctx context.Context) (release func(), err error) {
	select {
	case l.slots <- struct{}{}:
	default:
		l.waiting.Add(1)
		defer l.waiting.Add(-1)
		select {
		case l.slots <- struct{}{}:
		case <-ctx.Done():
			return nil, fmt.Errorf("wait for password hashing slot: %w", ctx.Err())
		}
	}
	n := l.inflight.Add(1)
	for p := l.peak.Load(); n > p && !l.peak.CompareAndSwap(p, n); p = l.peak.Load() {
	}
	return func() {
		l.inflight.Add(-1)
		<-l.slots
	}, nil
}

// hash is hashPassword under the semaphore.
func (s *service) hash(ctx context.Context, password string) (string, error) {
	release, err := s.argon.acquire(ctx)
	if err != nil {
		return "", err
	}
	defer release()
	return hashPassword(password)
}

// verify is verifyPassword under the semaphore.
func (s *service) verify(ctx context.Context, encoded, password string) (bool, error) {
	release, err := s.argon.acquire(ctx)
	if err != nil {
		return false, err
	}
	defer release()
	return verifyPassword(encoded, password), nil
}

// hashPassword returns a PHC-format argon2id hash of password with a fresh salt.
func hashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemoryKiB, argonThreads, argonKeyLen)
	b64 := base64.RawStdEncoding.EncodeToString
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemoryKiB, argonTime, argonThreads, b64(salt), b64(key)), nil
}

// verifyPassword reports whether password matches the PHC string encoded. It
// reads the cost parameters from encoded. Anything unparseable, including "",
// is false.
func verifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return false
	}
	var version int
	if n, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || n != 1 || version != argon2.Version {
		return false
	}
	var m, t uint32
	var p uint8
	if n, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil || n != 3 || t < 1 || p < 1 || m < 1 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) == 0 {
		return false
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(key) == 0 {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, t, m, p, uint32(len(key)))
	return subtle.ConstantTimeCompare(got, key) == 1
}
