package bus

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/storage"
)

// paused reports whether the room is closed to agents: paused itself, or
// everything is stopped.
func paused(ctx context.Context, r storage.Repository, row storage.Room) (bool, error) {
	if row.PausedAtMs != nil {
		return true, nil
	}
	halted, err := r.GetHaltedAtMs(ctx)
	if err != nil {
		return false, fmt.Errorf("get halt: %w", err)
	}
	return halted != nil, nil
}

// requireRoomOpen returns ErrPaused when the room is closed to agents, and the
// bus's own ErrNotFound when it is gone.
func requireRoomOpen(ctx context.Context, r storage.Repository, roomID string) error {
	row, err := r.GetRoom(ctx, roomID)
	if errors.Is(err, storage.ErrNotFound) {
		return fmt.Errorf("room %s: %w", roomID, ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("get room: %w", err)
	}
	closed, err := paused(ctx, r, row)
	if err != nil {
		return err
	}
	if closed {
		return ErrPaused
	}
	return nil
}

// pauseRoom marks the room paused for reason and writes its events: one room.changed, and one pause addressed to each agent in the room.
func (s *service) pauseRoom(ctx context.Context, r storage.Repository, row storage.Room, reason PauseReason, at time.Time) error {
	ms := at.UnixMilli()
	if err := r.SetRoomPause(ctx, row.ID, &ms, string(reason)); err != nil {
		return fmt.Errorf("set room pause: %w", err)
	}
	if err := putEvent(ctx, r, EventRoomChanged, row.ID, row.ID, at); err != nil {
		return err
	}
	agentRows, err := r.ListRoomAgents(ctx, row.ID)
	if err != nil {
		return fmt.Errorf("list room agents: %w", err)
	}
	for _, ar := range agentRows {
		a, err := agentFromRow(ar)
		if err != nil {
			return err
		}
		if err := putAgentEvent(ctx, r, EventControlPause, row.ID, row.ID, a.ID, at); err != nil {
			return err
		}
	}
	return nil
}

// resumeDeliveries hands each connected agent in the room what it is owed, if
// the room is open: a delivery still 'delivered' (its event was skipped while
// the room was closed) is handed over again with no state change, otherwise
// the agent's next waiting message is released. An agent with no stream is
// handed its delivery when it returns, by Subscribe.
func (s *service) resumeDeliveries(ctx context.Context, r storage.Repository, roomID string, at time.Time) error {
	row, err := r.GetRoom(ctx, roomID)
	if errors.Is(err, storage.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get room: %w", err)
	}
	if closed, err := paused(ctx, r, row); err != nil || closed {
		return err
	}
	agentRows, err := r.ListRoomAgents(ctx, roomID)
	if err != nil {
		return fmt.Errorf("list room agents: %w", err)
	}
	for _, ar := range agentRows {
		a, err := agentFromRow(ar)
		if err != nil {
			return err
		}
		if !s.hub.agentConnected(a.ID) {
			continue
		}
		d, err := r.GetDeliveredDelivery(ctx, roomID, a.ID.String())
		if err == nil {
			if err := putAgentEvent(ctx, r, EventDelivery, roomID, d.MessageID, a.ID, at); err != nil {
				return err
			}
			continue
		}
		if !errors.Is(err, storage.ErrNotFound) {
			return fmt.Errorf("get delivered: %w", err)
		}
		if err := s.releaseHead(ctx, r, roomID, a, at, true); err != nil {
			return err
		}
	}
	return nil
}

// PauseRoom closes a room the actor is in to its agents: nothing more is
// handed to them and they may add nothing, until a human resumes it. Messages
// already queued stay queued. Any room can be paused, a DM or an archived one
// included; pausing a paused room changes nothing.
func (s *service) PauseRoom(ctx context.Context, actor Actor, roomID ulid.ULID) (Room, error) {
	at := s.now()
	err := s.record(ctx, func(r storage.Repository) error {
		row, err := roomForMember(ctx, r, actor, roomID)
		if err != nil {
			return err
		}
		if row.PausedAtMs != nil {
			return nil
		}
		return s.pauseRoom(ctx, r, row, PauseHuman, at)
	})
	if err != nil {
		return Room{}, wrapOp("pause room", err)
	}
	return s.GetRoom(ctx, actor, roomID)
}

// ResumeRoom lifts the room's own pause, whatever its reason, starts the
// agent-turn count over, and hands each connected agent its next message.
// Resuming a room that is not paused changes nothing.
func (s *service) ResumeRoom(ctx context.Context, actor Actor, roomID ulid.ULID) (Room, error) {
	at := s.now()
	err := s.record(ctx, func(r storage.Repository) error {
		row, err := roomForMember(ctx, r, actor, roomID)
		if err != nil {
			return err
		}
		if row.PausedAtMs == nil {
			return nil
		}
		if err := r.SetRoomPause(ctx, row.ID, nil, ""); err != nil {
			return fmt.Errorf("clear room pause: %w", err)
		}
		if err := r.SetRoomAgentTurns(ctx, row.ID, 0); err != nil {
			return fmt.Errorf("reset agent turns: %w", err)
		}
		if err := putEvent(ctx, r, EventRoomChanged, row.ID, row.ID, at); err != nil {
			return err
		}
		return s.resumeDeliveries(ctx, r, row.ID, at)
	})
	if err != nil {
		return Room{}, wrapOp("resume room", err)
	}
	return s.GetRoom(ctx, actor, roomID)
}
