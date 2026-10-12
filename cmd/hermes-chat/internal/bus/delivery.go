package bus

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/storage"
)

// DeliveryState says how one agent's copy of a message sits in its queue.
type DeliveryState string

const (
	DeliveryWaiting   DeliveryState = "waiting"
	DeliveryDelivered DeliveryState = "delivered"
	DeliveryWorking   DeliveryState = "working"
	DeliveryDone      DeliveryState = "done"
	DeliveryCancelled DeliveryState = "cancelled"
	DeliveryFailed    DeliveryState = "failed"
)

// RoomAgent is an agent in a room, as the viewer sees it.
type RoomAgent struct {
	Agent     Agent
	Connected bool // whether the agent has a live stream right now
}

// Delivery is one agent's copy of a message: who it is for and how it sits.
type Delivery struct {
	Agent Agent
	State DeliveryState
}

// mentionTrail is the punctuation a mention token may trail.
const mentionTrail = ".,;:!?)]}\"'"

// mentionedAgents returns the members of agents whose @handle text mentions, in
// handle order, each at most once. One mention is one whitespace-delimited
// token that starts with "@": one leading "(" is ignored, any run of
// ".,;:!?)]}\"'" at the end is ignored, and the rest lowercases and matches a
// member handle exactly. So "@scout", "@scout,", "(@scout)", and "@SCOUT" wake
// scout; "hi@scout" and "@sc" do not.
func mentionedAgents(text string, agents []Agent) []Agent {
	mentioned := map[string]bool{}
	for _, token := range strings.Fields(text) {
		mention := strings.TrimPrefix(token, "(")
		if !strings.HasPrefix(mention, "@") {
			continue
		}
		handle := strings.ToLower(strings.TrimRight(mention[1:], mentionTrail))
		if handle != "" {
			mentioned[handle] = true
		}
	}
	// walk the members, so matches come back in handle order, each at most once
	out := make([]Agent, 0, len(agents))
	for _, a := range agents {
		if mentioned[a.Handle] {
			out = append(out, a)
		}
	}
	return out
}

// matchingMembers returns the members whose id is in want, in handle order.
func matchingMembers(want map[string]bool, members []Agent) []Agent {
	out := make([]Agent, 0, len(want))
	for _, a := range members {
		if want[a.ID.String()] {
			out = append(out, a)
		}
	}
	return out
}

// wakeAgents returns the room's agents this message wakes, in handle order.
// A human author wakes every agent in a dm or conversation. In a channel the
// human wakes the agents the text mentions plus, when the message is a reply,
// the agent that wrote the message it replies to. An agent author wakes only
// the agents it mentions, never itself: an agent's reply-to wakes nobody.
func wakeAgents(author Actor, flavor RoomFlavor, text string, replyToRow *storage.Message, members []Agent) []Agent {
	want := map[string]bool{}
	for _, a := range mentionedAgents(text, members) {
		want[a.ID.String()] = true
	}
	if author.Kind == ActorAgent {
		delete(want, author.ID.String())
		return matchingMembers(want, members)
	}
	switch flavor {
	case FlavorDM, FlavorConversation:
		return members
	default: // a channel
		if replyToRow != nil && replyToRow.AuthorAgentID != nil {
			want[*replyToRow.AuthorAgentID] = true
		}
		return matchingMembers(want, members)
	}
}

// releaseHead hands the pair's waiting head to the agent: it sets the delivery
// to delivered and writes the agent-targeted delivery event. It does nothing
// when the room is gone, archived, or paused, the agent has no live stream, or
// the pair has no releasable head. It reads the room itself rather than trust
// a row its caller read earlier, so it sees a change made in this transaction.
// notify is false when a message.posted event in the same transaction already
// carries the current deliveries (its payload is loaded at poll time), so no
// delivery.changed row is needed.
func (s *service) releaseHead(ctx context.Context, r storage.Repository, roomID string, agent Agent, at time.Time, notify bool) error {
	room, err := r.GetRoom(ctx, roomID)
	if errors.Is(err, storage.ErrNotFound) {
		return nil // the room is gone (its human is deleted); nothing to release
	}
	if err != nil {
		return fmt.Errorf("get room: %w", err)
	}
	if room.ArchivedAtMs != nil {
		return nil
	}
	if closed, err := paused(ctx, r, room); err != nil || closed {
		return err
	}
	if !s.hub.agentConnected(agent.ID) {
		return nil
	}
	head, ok, err := r.HeadWaiting(ctx, room.ID, agent.ID.String())
	if err != nil {
		return fmt.Errorf("head waiting: %w", err)
	}
	if !ok {
		return nil
	}
	if err := r.SetDeliveryState(ctx, room.ID, agent.ID.String(), head.MessageID, string(DeliveryDelivered)); err != nil {
		return fmt.Errorf("set delivered: %w", err)
	}
	if err := putAgentEvent(ctx, r, EventDelivery, room.ID, head.MessageID, agent.ID, at); err != nil {
		return err
	}
	if notify {
		if err := putEvent(ctx, r, EventDeliveryChanged, room.ID, head.MessageID, at); err != nil {
			return err
		}
	}
	return nil
}

// CancelMessage cancels the waiting deliveries of the author's message. It is
// available only while a delivery is still waiting; after release the UI's
// control is Stop. A cancelled message can never reach the agent.
func (s *service) CancelMessage(ctx context.Context, actor Actor, roomID, messageID ulid.ULID) error {
	at := s.now()
	err := s.record(ctx, func(r storage.Repository) error {
		if _, err := roomForMember(ctx, r, actor, roomID); err != nil {
			return err
		}
		msg, found, err := messageInRoom(ctx, r, roomID, messageID)
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("message %s: %w", messageID, ErrNotFound)
		}
		if msg.AuthorHumanID == nil || *msg.AuthorHumanID != actor.ID.String() {
			return ErrForbidden
		}
		agentRows, err := r.ListRoomAgents(ctx, roomID.String())
		if err != nil {
			return fmt.Errorf("list room agents: %w", err)
		}
		agents := make([]Agent, 0, len(agentRows))
		for _, ar := range agentRows {
			a, err := agentFromRow(ar)
			if err != nil {
				return err
			}
			agents = append(agents, a)
		}
		// CancelDeliveries is room-wide, so cancel this one message's copies:
		// a waiting delivery only, message by message.
		deliveries, err := r.ListDeliveriesForMessage(ctx, roomID.String(), messageID.String())
		if err != nil {
			return fmt.Errorf("list message deliveries: %w", err)
		}
		cancelled := 0
		for _, d := range deliveries {
			if d.State != string(DeliveryWaiting) {
				continue
			}
			if err := r.SetDeliveryState(ctx, roomID.String(), d.AgentID, messageID.String(), string(DeliveryCancelled)); err != nil {
				return fmt.Errorf("cancel delivery: %w", err)
			}
			cancelled++
		}
		if cancelled == 0 {
			// every delivery for the message is already settled
			return ErrNothingWaiting
		}
		if err := putEvent(ctx, r, EventDeliveryChanged, roomID.String(), messageID.String(), at); err != nil {
			return err
		}
		for _, a := range agents {
			if err := s.releaseHead(ctx, r, roomID.String(), a, at, true); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return wrapOp("cancel message", err)
	}
	return nil
}

// loadDeliveries fills each message's copies from the delivery table, keyed by
// message id. The SQL orders rows by message id then handle, so each group
// keeps handle order, and each distinct agent is resolved once.
func loadDeliveries(ctx context.Context, r storage.Repository, rows []storage.Message) (map[string][]Delivery, error) {
	if len(rows) == 0 {
		return map[string][]Delivery{}, nil
	}
	first, last := rows[0].ID, rows[0].ID
	for _, row := range rows {
		if row.ID < first {
			first = row.ID
		}
		if row.ID > last {
			last = row.ID
		}
	}
	dlvs, err := r.ListDeliveriesInRange(ctx, rows[0].RoomID, first, last)
	if err != nil {
		return nil, fmt.Errorf("list deliveries: %w", err)
	}
	agents := make(map[string]Agent, len(dlvs))
	out := map[string][]Delivery{}
	for _, d := range dlvs {
		a, ok := agents[d.AgentID]
		if !ok {
			ar, err := r.GetAgentByID(ctx, d.AgentID)
			if err != nil {
				return nil, fmt.Errorf("get delivery agent: %w", err)
			}
			ag, err := agentFromRow(ar)
			if err != nil {
				return nil, err
			}
			a = ag
			agents[d.AgentID] = a
		}
		out[d.MessageID] = append(out[d.MessageID], Delivery{Agent: a, State: DeliveryState(d.State)})
	}
	return out, nil
}
