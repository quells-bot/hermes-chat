package bus

import (
	"context"
	"fmt"

	"github.com/oklog/ulid/v2"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/storage"
)

// roomAgentsByRoom lists every room id once, with its agents. Every room has at least one
// agent, so the room-agent pairs cover every room.
func roomAgentsByRoom(ctx context.Context, r storage.Repository) ([]string, map[string][]storage.RoomAgent, error) {
	pairs, err := r.ListAllRoomAgents(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("list all room agents: %w", err)
	}
	var ids []string
	byRoom := map[string][]storage.RoomAgent{}
	for _, p := range pairs {
		if _, ok := byRoom[p.RoomID]; !ok {
			ids = append(ids, p.RoomID)
		}
		byRoom[p.RoomID] = append(byRoom[p.RoomID], p)
	}
	return ids, byRoom, nil
}

// StopEverything closes every room to its agents at once, apart from each
// room's own pause. Stopping while already stopped changes nothing.
func (s *service) StopEverything(ctx context.Context, actor Actor) error {
	if actor.Kind != ActorHuman {
		return ErrForbidden
	}
	at := s.now()
	err := s.record(ctx, func(r storage.Repository) error {
		halted, err := r.GetHaltedAtMs(ctx)
		if err != nil {
			return fmt.Errorf("get halt: %w", err)
		}
		if halted != nil {
			return nil
		}
		ms := at.UnixMilli()
		if err := r.SetHaltedAtMs(ctx, &ms); err != nil {
			return fmt.Errorf("set halt: %w", err)
		}
		if _, err := s.expireOpenPrompts(ctx, r, "", "", at); err != nil {
			return err
		}
		ids, byRoom, err := roomAgentsByRoom(ctx, r)
		if err != nil {
			return err
		}
		for _, id := range ids {
			if err := putEvent(ctx, r, EventRoomChanged, id, id, at); err != nil {
				return err
			}
			for _, ra := range byRoom[id] {
				agentID, err := ulid.ParseStrict(ra.AgentID)
				if err != nil {
					return fmt.Errorf("stored agent has an invalid id %q: %w", ra.AgentID, err)
				}
				if err := putAgentEvent(ctx, r, EventControlPause, id, id, agentID, at); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return wrapOp("stop everything", err)
	}
	return nil
}

// ResumeEverything lifts the halt. Each room returns to its own pause state:
// a room paused on its own stays paused. Not stopped is a no-op.
func (s *service) ResumeEverything(ctx context.Context, actor Actor) error {
	if actor.Kind != ActorHuman {
		return ErrForbidden
	}
	at := s.now()
	err := s.record(ctx, func(r storage.Repository) error {
		halted, err := r.GetHaltedAtMs(ctx)
		if err != nil {
			return fmt.Errorf("get halt: %w", err)
		}
		if halted == nil {
			return nil
		}
		if err := r.SetHaltedAtMs(ctx, nil); err != nil {
			return fmt.Errorf("clear halt: %w", err)
		}
		ids, _, err := roomAgentsByRoom(ctx, r)
		if err != nil {
			return err
		}
		for _, id := range ids {
			if err := putEvent(ctx, r, EventRoomChanged, id, id, at); err != nil {
				return err
			}
		}
		for _, id := range ids {
			if err := s.resumeDeliveries(ctx, r, id, at); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return wrapOp("resume everything", err)
	}
	return nil
}

// Halted reports whether everything is stopped.
func (s *service) Halted(ctx context.Context) (bool, error) {
	halted, err := s.repo.GetHaltedAtMs(ctx)
	if err != nil {
		return false, wrapOp("get halt", err)
	}
	return halted != nil, nil
}
