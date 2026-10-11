package bus_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/bus"
	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/storage"
	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/storage/storagetest"
)

// clockSvc returns a service whose clock the test can move, with kai already created.
func clockSvc(t *testing.T) (bus.Service, *time.Time, bus.Human) {
	now := fixed
	s := bus.NewService(storagetest.NewRepo(t), func() time.Time { return now })
	kai, err := s.CreateHuman(ctx, "kai", pw)
	require.NoError(t, err)
	return s, &now, kai
}

func TestLogin_Success(t *testing.T) {
	s, _, kai := clockSvc(t)
	sess, err := s.Login(ctx, " KAI ", pw, "10.0.0.1")
	require.NoError(t, err)
	assert.Regexp(t, `^hcs_[A-Za-z0-9_-]{43}$`, sess.Token)
	assert.Equal(t, kai, sess.Human)
	assert.Equal(t, fixed.Add(bus.SessionLifetime), sess.ExpiresAt)
	actor, err := s.ResolveSession(ctx, sess.Token)
	require.NoError(t, err)
	assert.Equal(t, bus.Actor{Kind: bus.ActorHuman, ID: kai.ID}, actor)
	again, _ := s.Login(ctx, "kai", pw, "10.0.0.1")
	assert.NotEqual(t, sess.Token, again.Token)
	_, err = s.ResolveSession(ctx, sess.Token)
	assert.NoError(t, err) // a second login does not end the first
}

func TestLogin_OnlyTheTokenHashIsStored(t *testing.T) {
	r := storagetest.NewRepo(t)
	s := bus.NewService(r, func() time.Time { return fixed })
	_, _ = s.CreateHuman(ctx, "kai", pw)
	sess, _ := s.Login(ctx, "kai", pw, "10.0.0.1")
	_, err := r.GetSessionByTokenHash(ctx, sess.Token)
	assert.ErrorIs(t, err, storage.ErrNotFound)
	sum := sha256.Sum256([]byte(sess.Token))
	_, err = r.GetSessionByTokenHash(ctx, hex.EncodeToString(sum[:]))
	assert.NoError(t, err)
}

// Review Focus 3. Every failure is the same error, whatever the cause.
func TestLogin_InvalidCredentials(t *testing.T) {
	r := storagetest.NewRepo(t)
	s := bus.NewService(r, func() time.Time { return fixed })
	_, _ = s.CreateHuman(ctx, "kai", pw)
	require.NoError(t, r.PutHuman(ctx, storage.Human{ID: ulid.Make().String(), Username: "legacy"})) // stage 1 row: no password
	cases := [][2]string{
		{"kai", "wrong password"}, {"nobody", pw}, {"not a name", pw}, {"", pw},
		{"kai", ""}, {"legacy", ""}, {"legacy", pw}, {"kai", strings.Repeat("a", 257)},
	}
	for i, c := range cases {
		_, err := s.Login(ctx, c[0], c[1], fmt.Sprintf("10.0.1.%d", i)) // a fresh IP each time
		assert.ErrorIs(t, err, bus.ErrInvalidCredentials, "%q", c)
		assert.Equal(t, bus.ErrInvalidCredentials.Error(), err.Error(), "%q: the error must not say which part was wrong", c)
	}
}

func TestLogin_ThrottlePerUsername(t *testing.T) {
	s, now, _ := clockSvc(t)
	for i := 0; i < 5; i++ { // burst of 5, each from a different IP
		_, err := s.Login(ctx, "kai", "wrong password", fmt.Sprintf("10.0.2.%d", i))
		require.ErrorIs(t, err, bus.ErrInvalidCredentials)
	}
	_, err := s.Login(ctx, "kai", pw, "10.0.2.99") // even the right password
	var te bus.ThrottledError
	require.ErrorAs(t, err, &te)
	assert.ErrorIs(t, err, bus.ErrThrottled)
	assert.InDelta(t, time.Minute, te.RetryAfter, float64(time.Millisecond))
	_, err = s.Login(ctx, "ada", "wrong password", "10.0.2.99")
	assert.ErrorIs(t, err, bus.ErrInvalidCredentials) // other names are unaffected

	*now = now.Add(59 * time.Second)
	_, err = s.Login(ctx, "kai", pw, "10.0.2.99")
	require.ErrorAs(t, err, &te)
	assert.InDelta(t, time.Second, te.RetryAfter, float64(time.Millisecond))
	*now = now.Add(2 * time.Second) // being refused did not push the wait further out
	_, err = s.Login(ctx, "kai", pw, "10.0.2.99")
	assert.NoError(t, err)
}

func TestLogin_ThrottlePerIP(t *testing.T) {
	s, now, _ := clockSvc(t)
	for i := 0; i < 20; i++ { // burst of 20, a different name each time
		_, err := s.Login(ctx, fmt.Sprintf("user%d", i), "", "10.0.3.1")
		require.ErrorIs(t, err, bus.ErrInvalidCredentials)
	}
	_, err := s.Login(ctx, "kai", pw, "10.0.3.1")
	var te bus.ThrottledError
	require.ErrorAs(t, err, &te)
	assert.InDelta(t, 10*time.Second, te.RetryAfter, float64(time.Millisecond))
	_, err = s.Login(ctx, "kai", pw, "10.0.3.2")
	assert.NoError(t, err) // another IP is fine, and kai's own bucket was not charged
	*now = now.Add(11 * time.Second)
	_, err = s.Login(ctx, "kai", pw, "10.0.3.1")
	assert.NoError(t, err)
}

// Review Focus 2.
func TestLogin_ThrottleMapsDoNotGrowWithoutBound(t *testing.T) {
	now := fixed
	s := bus.NewService(storagetest.NewRepo(t), func() time.Time { return now })
	for i := 0; i < 3000; i++ { // empty password: refused at step 3, so no hashing
		_, _ = s.Login(ctx, fmt.Sprintf("user%d", i), "", fmt.Sprintf("10.1.%d.%d", i/250, i%250))
	}
	users, ips := s.ThrottleEntries()
	assert.Zero(t, users, "a name that never reaches the username limiter gets no entry")
	assert.Equal(t, 3000, ips) // the clock has not moved: none has refilled, so none may be dropped
	now = now.Add(time.Hour)
	_, _ = s.Login(ctx, "kai", "", "10.9.9.9")
	_, ips = s.ThrottleEntries()
	assert.LessOrEqual(t, ips, 1)
}

// Review Focus 5.
func TestResolveSession_Expiry(t *testing.T) {
	s, now, _ := clockSvc(t)
	sess, _ := s.Login(ctx, "kai", pw, "10.0.0.1")
	*now = fixed.Add(bus.SessionLifetime - time.Millisecond)
	_, err := s.ResolveSession(ctx, sess.Token)
	assert.NoError(t, err)
	*now = fixed.Add(bus.SessionLifetime)
	_, err = s.ResolveSession(ctx, sess.Token)
	assert.ErrorIs(t, err, bus.ErrUnauthenticated)
}

func TestResolveSession_BadTokens(t *testing.T) {
	s, _, _ := clockSvc(t)
	sess, _ := s.Login(ctx, "kai", pw, "10.0.0.1")
	for _, bad := range []string{"", "hcs_", sess.Token + "x", strings.ToUpper(sess.Token)} {
		_, err := s.ResolveSession(ctx, bad)
		assert.ErrorIs(t, err, bus.ErrUnauthenticated, "%q", bad)
	}
	_, agentToken, _ := s.CreateAgent(ctx, "scout", "Scout")
	_, err := s.ResolveSession(ctx, agentToken)
	assert.ErrorIs(t, err, bus.ErrUnauthenticated) // an agent token is not a session
	_, err = s.ResolveAgentToken(ctx, sess.Token)
	assert.ErrorIs(t, err, bus.ErrUnauthenticated) // and the reverse
}

// Review Focus 5.
func TestSession_EndsOnLogoutPasswdAndRemove(t *testing.T) {
	s, _, _ := clockSvc(t)
	a, _ := s.Login(ctx, "kai", pw, "10.0.0.1")
	b, _ := s.Login(ctx, "kai", pw, "10.0.0.1")
	require.NoError(t, s.Logout(ctx, a.Token))
	_, err := s.ResolveSession(ctx, a.Token)
	assert.ErrorIs(t, err, bus.ErrUnauthenticated)
	_, err = s.ResolveSession(ctx, b.Token)
	assert.NoError(t, err) // logout ends one session, not all
	assert.NoError(t, s.Logout(ctx, a.Token))
	assert.NoError(t, s.Logout(ctx, "")) // repeat and empty are fine

	require.NoError(t, s.SetHumanPassword(ctx, "kai", "a whole new password"))
	_, err = s.ResolveSession(ctx, b.Token)
	assert.ErrorIs(t, err, bus.ErrUnauthenticated)

	c, err := s.Login(ctx, "kai", "a whole new password", "10.0.0.2")
	require.NoError(t, err)
	require.NoError(t, s.RemoveHuman(ctx, "kai"))
	_, err = s.ResolveSession(ctx, c.Token)
	assert.ErrorIs(t, err, bus.ErrUnauthenticated)
}

func TestLogin_SweepsExpiredSessions(t *testing.T) {
	now := fixed
	r := storagetest.NewRepo(t)
	s := bus.NewService(r, func() time.Time { return now })
	_, _ = s.CreateHuman(ctx, "kai", pw)
	old, _ := s.Login(ctx, "kai", pw, "10.0.0.1")
	now = fixed.Add(bus.SessionLifetime + time.Hour)
	_, err := s.Login(ctx, "kai", pw, "10.0.0.1")
	require.NoError(t, err)
	sum := sha256.Sum256([]byte(old.Token))
	_, err = r.GetSessionByTokenHash(ctx, hex.EncodeToString(sum[:]))
	assert.ErrorIs(t, err, storage.ErrNotFound)
}

type sessionReadFails struct{ storage.Repository }

func (sessionReadFails) GetSessionByTokenHash(context.Context, string) (storage.Session, error) {
	return storage.Session{}, errDisk
}

// A database failure is not "you are logged out".
func TestResolveSession_StorageFailureIsNotUnauthenticated(t *testing.T) {
	_, err := bus.NewService(sessionReadFails{storagetest.NewRepo(t)}, time.Now).ResolveSession(ctx, "hcs_x")
	assert.ErrorIs(t, err, errDisk)
	assert.NotErrorIs(t, err, bus.ErrUnauthenticated)
}

// beforeTx runs fn through the real repository just before delegating to the
// real InTx, and records every PutSession made inside the transaction.
type beforeTx struct {
	storage.Repository
	fn   func()
	puts *int
}

func (b beforeTx) InTx(ctx context.Context, f func(storage.Repository) error) error {
	b.fn()
	return b.Repository.InTx(ctx, func(tx storage.Repository) error {
		return f(countPuts{Repository: tx, n: b.puts})
	})
}

type countPuts struct {
	storage.Repository
	n *int
}

func (c countPuts) PutSession(ctx context.Context, s storage.Session) error {
	*c.n++
	return c.Repository.PutSession(ctx, s)
}

// A login racing a password change must not produce a session.
func TestLogin_PasswordChangedBeforeSessionInsert(t *testing.T) {
	r := storagetest.NewRepo(t)
	_, err := bus.NewService(r, func() time.Time { return fixed }).CreateHuman(ctx, "kai", pw)
	require.NoError(t, err)
	newHash, err := bus.HashPassword("a whole new password")
	require.NoError(t, err)
	var puts int
	racing := beforeTx{Repository: r, puts: &puts, fn: func() {
		row, err := r.GetHumanByUsername(ctx, "kai")
		require.NoError(t, err)
		require.NoError(t, r.SetHumanPasswordHash(ctx, row.ID, newHash))
	}}
	sess, err := bus.NewService(racing, func() time.Time { return fixed }).Login(ctx, "kai", pw, "10.0.0.1")
	assert.Equal(t, bus.ErrInvalidCredentials, err)
	assert.Empty(t, sess.Token)
	assert.Zero(t, puts, "no session may be written")
}

func TestLogin_HumanRemovedBeforeSessionInsert(t *testing.T) {
	r := storagetest.NewRepo(t)
	_, err := bus.NewService(r, func() time.Time { return fixed }).CreateHuman(ctx, "kai", pw)
	require.NoError(t, err)
	var puts int
	racing := beforeTx{Repository: r, puts: &puts, fn: func() {
		row, err := r.GetHumanByUsername(ctx, "kai")
		require.NoError(t, err)
		require.NoError(t, r.DeleteHuman(ctx, row.ID))
	}}
	_, err = bus.NewService(racing, func() time.Time { return fixed }).Login(ctx, "kai", pw, "10.0.0.1")
	assert.Equal(t, bus.ErrInvalidCredentials, err)
	assert.Zero(t, puts, "no session may be written")
}

type putSessionConflicts struct{ storage.Repository }

func (putSessionConflicts) PutSession(context.Context, storage.Session) error {
	return storage.ErrConflict
}

func TestLogin_NoStorageSentinelEscapes(t *testing.T) {
	r := storagetest.NewRepo(t)
	_, err := bus.NewService(r, func() time.Time { return fixed }).CreateHuman(ctx, "kai", pw)
	require.NoError(t, err)
	_, err = bus.NewService(txWith{r}, func() time.Time { return fixed }).Login(ctx, "kai", pw, "10.0.0.1")
	require.Error(t, err)
	assert.NotErrorIs(t, err, storage.ErrConflict)
	assert.NotErrorIs(t, err, bus.ErrInvalidCredentials)
}

// txWith hands the transaction callback a repository whose PutSession conflicts.
type txWith struct{ storage.Repository }

func (t txWith) InTx(ctx context.Context, f func(storage.Repository) error) error {
	return t.Repository.InTx(ctx, func(tx storage.Repository) error {
		return f(putSessionConflicts{tx})
	})
}

func TestLogin_ArgonWorkIsBounded(t *testing.T) {
	s := bus.NewService(storagetest.NewRepo(t), func() time.Time { return fixed })
	kai, err := s.CreateHuman(ctx, "kai", pw)
	require.NoError(t, err)
	const n = 12
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			user := fmt.Sprintf("nobody%d", i) // unknown users still verify the dummy hash
			if i == 0 {
				user = "kai"
			}
			_, errs[i] = s.Login(ctx, user, "wrong password", fmt.Sprintf("10.4.0.%d", i))
		}()
	}
	wg.Wait()
	for i, err := range errs {
		assert.ErrorIs(t, err, bus.ErrInvalidCredentials, "login %d", i)
	}
	assert.LessOrEqual(t, s.ArgonPeak(), 2)
	assert.Positive(t, s.ArgonPeak())
	_, err = s.Login(ctx, "kai", pw, "10.4.1.1") // slots were all released
	require.NoError(t, err)
	_ = kai
}

func TestLogin_CancelledWhileWaitingForASlot(t *testing.T) {
	s := bus.NewService(storagetest.NewRepo(t), func() time.Time { return fixed })
	_, err := s.CreateHuman(ctx, "kai", pw)
	require.NoError(t, err)
	r1, err := s.HoldArgonSlot(ctx)
	require.NoError(t, err)
	r2, err := s.HoldArgonSlot(ctx)
	require.NoError(t, err)
	defer r1()
	defer r2()

	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := s.Login(cctx, "kai", "wrong password", "10.5.0.1")
		done <- err
	}()
	for s.ArgonWaiting() == 0 { // blocked on the semaphore, not on anything else
		runtime.Gosched()
	}
	cancel()
	err = <-done
	assert.ErrorIs(t, err, context.Canceled)
	assert.NotErrorIs(t, err, bus.ErrInvalidCredentials)
}

func TestLogin_IPv6ThrottlesPerSlash64(t *testing.T) {
	s := bus.NewService(storagetest.NewRepo(t), func() time.Time { return fixed })
	for i := 0; i < 20; i++ { // empty password: no hashing
		_, err := s.Login(ctx, "kai", "", fmt.Sprintf("2001:db8:1:2::%x", i+1))
		require.ErrorIs(t, err, bus.ErrInvalidCredentials, "attempt %d", i)
	}
	_, err := s.Login(ctx, "kai", "", "2001:db8:1:2:aaaa:bbbb:cccc:dddd")
	var te bus.ThrottledError
	assert.ErrorAs(t, err, &te)
	_, err = s.Login(ctx, "kai", "", "2001:db8:1:3::1") // a different /64
	assert.ErrorIs(t, err, bus.ErrInvalidCredentials)
}

func TestLogin_MappedIPv4SharesABucketWithIPv4(t *testing.T) {
	s := bus.NewService(storagetest.NewRepo(t), func() time.Time { return fixed })
	for i := 0; i < 20; i++ {
		ip := "10.0.3.7"
		if i%2 == 1 {
			ip = "::ffff:10.0.3.7"
		}
		_, err := s.Login(ctx, "kai", "", ip)
		require.ErrorIs(t, err, bus.ErrInvalidCredentials, "attempt %d", i)
	}
	_, err := s.Login(ctx, "kai", "", "10.0.3.7")
	assert.ErrorIs(t, err, bus.ErrThrottled)
	_, err = s.Login(ctx, "kai", "", "::ffff:10.0.3.7")
	assert.ErrorIs(t, err, bus.ErrThrottled)
}

func TestLogin_UsernameLimitersAreSweptToo(t *testing.T) {
	now := fixed
	s := bus.NewService(storagetest.NewRepo(t), func() time.Time { return now })
	for i := 0; i < 4; i++ { // a real name and password length, so each reaches the username limiter
		_, err := s.Login(ctx, fmt.Sprintf("user%d", i), pw, fmt.Sprintf("10.6.0.%d", i))
		require.ErrorIs(t, err, bus.ErrInvalidCredentials)
	}
	users, _ := s.ThrottleEntries()
	require.Equal(t, 4, users)
	now = now.Add(time.Hour)
	_, _ = s.Login(ctx, "other", pw, "10.6.1.1")
	users, _ = s.ThrottleEntries()
	assert.LessOrEqual(t, users, 1)
}
