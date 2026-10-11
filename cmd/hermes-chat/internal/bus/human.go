package bus

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/storage"
)

var usernamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

// Human is a registered user.
type Human struct {
	ID        ulid.ULID
	Username  string
	CreatedAt time.Time // UTC, millisecond precision
}

// normalizeUsername trims and lowercases username and reports whether the
// result is a valid username.
func normalizeUsername(username string) (string, bool) {
	username = strings.ToLower(strings.TrimSpace(username))
	return username, usernamePattern.MatchString(username)
}

// CreateHuman registers a user with a password. The username is trimmed and
// lowercased first, and checked before the password.
func (s *service) CreateHuman(ctx context.Context, username, password string) (Human, error) {
	username, ok := normalizeUsername(username)
	if !ok {
		return Human{}, ValidationError{Message: "username must be 1-32 characters: lowercase letters, digits, '_' or '-', starting with a letter or digit"}
	}
	if err := validatePassword(password); err != nil {
		return Human{}, err
	}
	hash, err := hashPassword(password)
	if err != nil {
		return Human{}, fmt.Errorf("hash password: %w", err)
	}
	at := s.now()
	id, err := newID(at)
	if err != nil {
		return Human{}, fmt.Errorf("generate id: %w", err)
	}
	err = s.repo.PutHuman(ctx, storage.Human{ID: id.String(), Username: username, PasswordHash: hash, CreatedAtMs: at.UnixMilli()})
	if errors.Is(err, storage.ErrConflict) {
		return Human{}, fmt.Errorf("username %q is already taken: %w", username, ErrConflict)
	}
	if err != nil {
		return Human{}, fmt.Errorf("create human: %w", err)
	}
	return Human{ID: id, Username: username, CreatedAt: at}, nil
}

// GetHuman returns the user with the given id, or ErrNotFound.
func (s *service) GetHuman(ctx context.Context, id ulid.ULID) (Human, error) {
	row, err := s.repo.GetHumanByID(ctx, id.String())
	if errors.Is(err, storage.ErrNotFound) {
		return Human{}, fmt.Errorf("user %s: %w", id, ErrNotFound)
	}
	if err != nil {
		return Human{}, fmt.Errorf("get human: %w", err)
	}
	return Human{ID: id, Username: row.Username, CreatedAt: time.UnixMilli(row.CreatedAtMs).UTC()}, nil
}

// SetHumanPassword replaces a user's password and ends all of that user's
// sessions. A username that matches no user, or cannot be one, is ErrNotFound.
func (s *service) SetHumanPassword(ctx context.Context, username, password string) error {
	username, ok := normalizeUsername(username)
	if !ok {
		return fmt.Errorf("user %q: %w", username, ErrNotFound)
	}
	if err := validatePassword(password); err != nil {
		return err
	}
	hash, err := hashPassword(password) // before the transaction: hashing is slow
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	err = s.repo.InTx(ctx, func(r storage.Repository) error {
		row, err := r.GetHumanByUsername(ctx, username)
		if err != nil {
			return err
		}
		if err := r.SetHumanPasswordHash(ctx, row.ID, hash); err != nil {
			return err
		}
		return r.DeleteSessionsByHuman(ctx, row.ID)
	})
	if errors.Is(err, storage.ErrNotFound) {
		return fmt.Errorf("user %q: %w", username, ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("set password: %w", err)
	}
	return nil
}

// RemoveHuman deletes a user and, with it, the user's sessions.
func (s *service) RemoveHuman(ctx context.Context, username string) error {
	username, ok := normalizeUsername(username)
	if !ok {
		return fmt.Errorf("user %q: %w", username, ErrNotFound)
	}
	err := s.repo.InTx(ctx, func(r storage.Repository) error {
		row, err := r.GetHumanByUsername(ctx, username)
		if err != nil {
			return err
		}
		return r.DeleteHuman(ctx, row.ID)
	})
	if errors.Is(err, storage.ErrNotFound) {
		return fmt.Errorf("user %q: %w", username, ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("remove human: %w", err)
	}
	return nil
}

// ListHumans returns all users ordered by username.
func (s *service) ListHumans(ctx context.Context) ([]Human, error) {
	rows, err := s.repo.ListHumans(ctx)
	if err != nil {
		return nil, fmt.Errorf("list humans: %w", err)
	}
	out := make([]Human, 0, len(rows))
	for _, r := range rows {
		id, err := ulid.ParseStrict(r.ID)
		if err != nil {
			return nil, fmt.Errorf("stored human %q has an invalid id: %w", r.Username, err)
		}
		out = append(out, Human{ID: id, Username: r.Username, CreatedAt: time.UnixMilli(r.CreatedAtMs).UTC()})
	}
	return out, nil
}
