// Package bus holds the chat server's business logic. It knows nothing about HTTP.
package bus

import (
	"context"
	"errors"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/storage"
)

var (
	ErrNotFound   = errors.New("not found")
	ErrConflict   = errors.New("conflict")
	ErrValidation = errors.New("validation failed")
)

// ValidationError reports invalid input. Its text is the message alone;
// errors.Is(err, ErrValidation) still holds.
type ValidationError struct{ Message string }

func (e ValidationError) Error() string { return e.Message }

// Is reports whether target is ErrValidation.
func (e ValidationError) Is(target error) bool { return target == ErrValidation }

// Clock returns the current time. It is injected so tests control time.
type Clock func() time.Time

// Service is the business-logic surface the app layer depends on.
type Service interface {
	Health(ctx context.Context) error
	CreateHuman(ctx context.Context, username string) (Human, error)
	ListHumans(ctx context.Context) ([]Human, error)
}

var _ Service = (*service)(nil)

type service struct {
	repo  storage.Repository
	clock Clock
}

// NewService returns a Service backed by repo, reading time from clock.
func NewService(repo storage.Repository, clock Clock) *service {
	return &service{repo: repo, clock: clock}
}

// Health reports whether storage is reachable.
func (s *service) Health(ctx context.Context) error {
	return s.repo.Ping(ctx)
}

// now returns the clock's time in UTC at millisecond precision, so it
// survives a round trip through storage unchanged.
func (s *service) now() time.Time {
	return s.clock().UTC().Truncate(time.Millisecond)
}

// newID returns a ULID for the given time. These IDs are identifiers, not
// secrets: ulid.DefaultEntropy is not cryptographically secure and must not be
// used for session or agent tokens.
func newID(at time.Time) (ulid.ULID, error) {
	return ulid.New(ulid.Timestamp(at), ulid.DefaultEntropy())
}
