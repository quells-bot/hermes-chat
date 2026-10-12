package bus

import (
	"context"
	"fmt"

	"github.com/oklog/ulid/v2"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/storage"
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

// BroadcastEdit offers e to the given humans' subscriptions, dropping when full.
func (s *service) BroadcastEdit(e LiveEdit, humans ...ulid.ULID) { s.hub.broadcastEdit(e, humans) }

// MentionedAgents and WakeAgents export the addressing rules, which black-box
// tests reach through type assertions on the Service value.
func (s *service) MentionedAgents(text string, agents []Agent) []Agent { return mentionedAgents(text, agents) }
func (s *service) WakeAgents(author Actor, flavor RoomFlavor, text string, replyToRow *storage.Message, members []Agent) []Agent {
	return wakeAgents(author, flavor, text, replyToRow, members)
}

// ReleaseSweepForTest releases every releasable head for an agent — the loop
// Task 6 wires into Subscribe. It is exported now so the cancel test is honest
// and Task 6 is a wiring change.
func (s *service) ReleaseSweepForTest(ctx context.Context, agentID ulid.ULID) error {
	at := s.now()
	err := s.record(ctx, func(r storage.Repository) error {
		return s.releaseSweep(ctx, r, agentID, at)
	})
	if err != nil {
		return fmt.Errorf("release sweep: %w", err)
	}
	return nil
}

// HoldArgonSlot takes one slot until the returned function is called.
func (s *service) HoldArgonSlot(ctx context.Context) (func(), error) { return s.argon.acquire(ctx) }
