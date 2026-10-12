package bus

import (
	"context"
	"fmt"
	"time"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/storage"
)

const (
	DefaultTurnCap = 6 // the migration's column default; kept here for the README and tests
	MaxTurnCap     = 100
)

var errTurnCap = ValidationError{Message: "turn_cap must be 1 to 100"}

// countAgentTurn is called by releaseHead for a head about to be released. It reports false, having
// paused the room for PauseTurnCap, when the head's message was written by an agent and the room
// has already had its cap of agent-woken turns; otherwise it counts the turn (agent-written
// messages only) and reports true. A turn a human woke, or one whose human author is since
// removed, is never counted.
func (s *service) countAgentTurn(ctx context.Context, r storage.Repository, room storage.Room, head storage.Delivery, at time.Time) (release bool, err error) {
	msg, err := r.GetMessage(ctx, head.MessageID)
	if err != nil {
		return false, fmt.Errorf("get head message: %w", err)
	}
	if msg.AuthorAgentID == nil {
		return true, nil
	}
	if room.AgentTurns >= room.TurnCap {
		// the delivery stays waiting; a human resume hands it over
		return false, s.pauseRoom(ctx, r, room, PauseTurnCap, at)
	}
	if err := r.SetRoomAgentTurns(ctx, room.ID, room.AgentTurns+1); err != nil {
		return false, fmt.Errorf("count agent turn: %w", err)
	}
	return true, nil
}

// resetAgentTurns starts the room's agent-turn count over: a human has spoken.
func resetAgentTurns(ctx context.Context, r storage.Repository, room storage.Room) error {
	if room.AgentTurns == 0 {
		return nil
	}
	if err := r.SetRoomAgentTurns(ctx, room.ID, 0); err != nil {
		return fmt.Errorf("reset agent turns: %w", err)
	}
	return nil
}
