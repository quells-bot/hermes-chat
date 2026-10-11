package bus

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/storage"
)

const (
	sessionTokenPrefix = "hcs_"

	// SessionLifetime is how long a session lasts from login. It is fixed:
	// using a session does not extend it.
	SessionLifetime = 90 * 24 * time.Hour
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrThrottled          = errors.New("throttled")
)

// ThrottledError says how long to wait. errors.Is(err, ErrThrottled) holds.
type ThrottledError struct{ RetryAfter time.Duration }

func (e ThrottledError) Error() string {
	return fmt.Sprintf("too many attempts; try again in %s", e.RetryAfter.Round(time.Second))
}

// Is reports whether target is ErrThrottled.
func (e ThrottledError) Is(target error) bool { return target == ErrThrottled }

// Session is a logged-in human's session.
type Session struct {
	Token     string // the cookie value; exists only here and in the browser
	Human     Human
	ExpiresAt time.Time
}

var (
	dummyOnce sync.Once
	dummyHash string
)

// dummyPasswordHash is verified against when no real hash exists, so a missing
// user costs the same time as a wrong password.
func dummyPasswordHash() string {
	dummyOnce.Do(func() {
		dummyHash, _ = hashPassword("dummy password for timing")
	})
	return dummyHash
}

// Login checks a username and password and starts a session. Every attempt
// counts against the limits, successful or not.
func (s *service) Login(ctx context.Context, username, password, ip string) (Session, error) {
	now := s.now()
	s.throttle.sweep(now)
	if d, ok := s.throttle.takeIP(ip, now); !ok {
		return Session{}, ThrottledError{RetryAfter: d}
	}
	username, ok := normalizeUsername(username)
	if !ok || password == "" || len(password) > 256 {
		return Session{}, ErrInvalidCredentials
	}
	if d, ok := s.throttle.takeUser(username, now); !ok {
		return Session{}, ThrottledError{RetryAfter: d}
	}
	row, err := s.repo.GetHumanByUsername(ctx, username)
	if errors.Is(err, storage.ErrNotFound) {
		row = storage.Human{}
	} else if err != nil {
		return Session{}, fmt.Errorf("login: %w", err)
	}
	if row.PasswordHash == "" {
		verifyPassword(dummyPasswordHash(), password)
		return Session{}, ErrInvalidCredentials
	}
	if !verifyPassword(row.PasswordHash, password) {
		return Session{}, ErrInvalidCredentials
	}
	humanID, err := ulid.ParseStrict(row.ID)
	if err != nil {
		return Session{}, fmt.Errorf("stored human %q has an invalid id: %w", row.Username, err)
	}
	token, hash, err := newToken(sessionTokenPrefix)
	if err != nil {
		return Session{}, err
	}
	id, err := newID(now)
	if err != nil {
		return Session{}, fmt.Errorf("generate id: %w", err)
	}
	expires := now.Add(SessionLifetime)
	err = s.repo.InTx(ctx, func(r storage.Repository) error {
		if err := r.DeleteExpiredSessions(ctx, now.UnixMilli()); err != nil {
			return err
		}
		return r.PutSession(ctx, storage.Session{
			ID: id.String(), TokenHash: hash, HumanID: row.ID,
			CreatedAtMs: now.UnixMilli(), ExpiresAtMs: expires.UnixMilli(),
		})
	})
	if err != nil {
		return Session{}, fmt.Errorf("create session: %w", err)
	}
	return Session{
		Token:     token,
		Human:     Human{ID: humanID, Username: row.Username, CreatedAt: time.UnixMilli(row.CreatedAtMs).UTC()},
		ExpiresAt: expires,
	}, nil
}

// Logout ends one session. An unknown or empty token is not an error.
func (s *service) Logout(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	if err := s.repo.DeleteSessionByTokenHash(ctx, hashToken(token)); err != nil {
		return fmt.Errorf("logout: %w", err)
	}
	return nil
}

// ResolveSession returns the human actor a session token belongs to. An empty,
// unknown, or expired token is ErrUnauthenticated.
func (s *service) ResolveSession(ctx context.Context, token string) (Actor, error) {
	if token == "" {
		return Actor{}, ErrUnauthenticated
	}
	row, err := s.repo.GetSessionByTokenHash(ctx, hashToken(token))
	if errors.Is(err, storage.ErrNotFound) {
		return Actor{}, ErrUnauthenticated
	}
	if err != nil {
		return Actor{}, fmt.Errorf("resolve session: %w", err)
	}
	if s.now().UnixMilli() >= row.ExpiresAtMs {
		return Actor{}, ErrUnauthenticated
	}
	id, err := ulid.ParseStrict(row.HumanID)
	if err != nil {
		return Actor{}, fmt.Errorf("stored session has an invalid human id: %w", err)
	}
	return Actor{Kind: ActorHuman, ID: id}, nil
}
