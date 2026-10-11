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

	ErrUnauthenticated = errors.New("unauthenticated")
	ErrForbidden       = errors.New("forbidden")

	// ErrUnavailable reports that the server is shutting down.
	ErrUnavailable = errors.New("unavailable")
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
	CreateHuman(ctx context.Context, username, password string) (Human, error) // ErrValidation, ErrConflict
	SetHumanPassword(ctx context.Context, username, password string) error     // ErrNotFound, ErrValidation; ends all that human's sessions
	RemoveHuman(ctx context.Context, username string) error                    // ErrNotFound
	GetHuman(ctx context.Context, id ulid.ULID) (Human, error)                 // ErrNotFound
	ListHumans(ctx context.Context) ([]Human, error)

	CreateAgent(ctx context.Context, handle, displayName string) (Agent, string, error) // the token, returned this once; ErrValidation, ErrConflict
	RotateAgentToken(ctx context.Context, handle string) (string, error)                // ErrNotFound; the old token stops working; a revoked agent is live again
	RevokeAgent(ctx context.Context, handle string) error                               // ErrNotFound; already revoked is a no-op that keeps the first time
	ListAgents(ctx context.Context) ([]Agent, error)                                    // ordered by handle
	GetAgent(ctx context.Context, id ulid.ULID) (Agent, error)                          // ErrNotFound
	ResolveAgentToken(ctx context.Context, token string) (Actor, error)                 // ErrUnauthenticated: empty, unknown, or revoked

	Login(ctx context.Context, username, password, ip string) (Session, error) // ErrInvalidCredentials, ThrottledError
	Logout(ctx context.Context, token string) error                            // an unknown or empty token is not an error
	ResolveSession(ctx context.Context, token string) (Actor, error)           // ErrUnauthenticated: empty, unknown, or expired

	CreateRoom(ctx context.Context, actor Actor, topic string, agentIDs []ulid.ULID) (Room, error)   // ErrValidation
	OpenDM(ctx context.Context, actor Actor, agentID ulid.ULID) (room Room, created bool, err error) // ErrValidation: no such agent
	ListRooms(ctx context.Context, actor Actor) ([]Room, error)                                      // the actor's rooms, ordered by id; archived included
	GetRoom(ctx context.Context, actor Actor, roomID ulid.ULID) (Room, error)                        // ErrNotFound
	UpdateRoom(ctx context.Context, actor Actor, roomID ulid.ULID, u RoomUpdate) (Room, error)       // ErrNotFound, ErrValidation, ErrRoomArchived
	InviteHuman(ctx context.Context, actor Actor, roomID, humanID ulid.ULID) (Room, error)           // the same three
	AddRoomAgent(ctx context.Context, actor Actor, roomID, agentID ulid.ULID) (Room, error)          // the same three
	RemoveRoomAgent(ctx context.Context, actor Actor, roomID, agentID ulid.ULID) (Room, error)       // the same three

	PostMessage(ctx context.Context, actor Actor, roomID ulid.ULID, text string, replyTo ulid.ULID) (Message, error) // ErrNotFound, ErrValidation, ErrRoomArchived
	GetMessage(ctx context.Context, actor Actor, roomID, messageID ulid.ULID) (Message, error)                       // ErrNotFound, also for a message in another room
	// ListMessages returns up to limit messages older than before (zero: the newest), oldest first.
	// A limit under 1 means DefaultPageSize; one over MaxPageSize means MaxPageSize.
	ListMessages(ctx context.Context, actor Actor, roomID, before ulid.ULID, limit int) ([]Message, error) // ErrNotFound
	MarkRoomRead(ctx context.Context, actor Actor, roomID, messageID ulid.ULID) error                      // ErrNotFound, ErrValidation

	// Subscribe opens a stream of the actor's events after the given event ID; FromNow starts at the end of the log.
	Subscribe(ctx context.Context, actor Actor, after int64) (*Subscription, error) // ErrForbidden, ErrValidation, ErrUnavailable
}

var _ Service = (*service)(nil)

type service struct {
	repo     storage.Repository
	clock    Clock
	throttle *throttle
	argon    argonLimiter
	hub      *Hub
}

// NewService returns a Service backed by repo, reading time from clock.
func NewService(repo storage.Repository, clock Clock) *service {
	return &service{repo: repo, clock: clock, throttle: newThrottle(), argon: newArgonLimiter(), hub: newHub()}
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
