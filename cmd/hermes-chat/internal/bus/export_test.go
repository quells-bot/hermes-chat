package bus

import (
	"context"

	"github.com/oklog/ulid/v2"
)

var HashPassword, VerifyPassword = hashPassword, verifyPassword

// ThrottleEntries reports how many limiters each map holds.
func (s *service) ThrottleEntries() (usernames, ips int) { return s.throttle.entries() }

// ArgonPeak reports the most argon2 computations that ever ran at once.
func (s *service) ArgonPeak() int { return int(s.argon.peak.Load()) }

// ArgonWaiting reports how many callers are blocked waiting for a slot.
func (s *service) ArgonWaiting() int { return int(s.argon.waiting.Load()) }

// Subscribers reports how many subscriptions the hub holds.
func (s *service) Subscribers() int { return s.hub.count() }

// AgentConnected reports whether the agent has a live stream.
func (s *service) AgentConnected(id ulid.ULID) bool { return s.hub.agentConnected(id) }

// BroadcastEdit offers e to every human subscription, dropping when full.
func (s *service) BroadcastEdit(e LiveEdit) { s.hub.broadcastEdit(e) }

// HoldArgonSlot takes one slot until the returned function is called.
func (s *service) HoldArgonSlot(ctx context.Context) (func(), error) { return s.argon.acquire(ctx) }
