package bus

import "github.com/oklog/ulid/v2"

// ActorKind says what sort of caller an Actor is.
type ActorKind string

const (
	ActorHuman ActorKind = "human"
	ActorAgent ActorKind = "agent"
)

// Actor is who is calling. Middleware builds it; handlers pass it to bus methods.
type Actor struct {
	Kind ActorKind
	ID   ulid.ULID
}

// String returns "human:<ULID>" or "agent:<ULID>".
func (a Actor) String() string { return string(a.Kind) + ":" + a.ID.String() }
