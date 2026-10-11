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

// CreateHuman registers a user. The username is trimmed and lowercased first.
func (s *service) CreateHuman(ctx context.Context, username string) (Human, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	if !usernamePattern.MatchString(username) {
		return Human{}, fmt.Errorf("username must be 1-32 characters: lowercase letters, digits, '_' or '-', starting with a letter or digit: %w", ErrValidation)
	}
	at := s.now()
	id, err := newID(at)
	if err != nil {
		return Human{}, fmt.Errorf("generate id: %w", err)
	}
	err = s.repo.PutHuman(ctx, storage.Human{ID: id.String(), Username: username, CreatedAtMs: at.UnixMilli()})
	if errors.Is(err, storage.ErrConflict) {
		return Human{}, fmt.Errorf("username %q is already taken: %w", username, ErrConflict)
	}
	if err != nil {
		return Human{}, err
	}
	return Human{ID: id, Username: username, CreatedAt: at}, nil
}

// ListHumans returns all users ordered by username.
func (s *service) ListHumans(ctx context.Context) ([]Human, error) {
	rows, err := s.repo.ListHumans(ctx)
	if err != nil {
		return nil, err
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
