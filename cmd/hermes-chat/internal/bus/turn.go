// Package bus holds the chat server's business logic. It knows nothing about HTTP.
package bus

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/oklog/ulid/v2"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/storage"
)

// ActivityKind says what one line of a turn's activity is.
type ActivityKind string

const (
	ActivityRemark ActivityKind = "remark"
	ActivityTool   ActivityKind = "tool"
)

// Activity is one line an agent contributed to a turn on a delivered message:
// a remark it made while reading, or a tool call it ran that yielded a preview.
type Activity struct {
	ID         ulid.ULID
	RoomID     ulid.ULID
	AgentID    ulid.ULID
	MessageID  ulid.ULID // the delivered message whose turn this belongs to
	Kind       ActivityKind
	Text       string // remark
	Tool       string // tool: name
	Preview    string // tool: short preview
	Arguments  string // tool: full arguments
	DurationMs *int64 // tool: nil while running
	Succeeded  *bool  // tool: nil while running
	CreatedAt  time.Time
}

var (
	errRemarkText   = ValidationError{Message: "remark text must be 1 to 262144 bytes, not blank, with no NUL character"}
	errToolName     = ValidationError{Message: "tool name must be 1-64 characters with no control characters"}
	errToolPreview  = ValidationError{Message: "tool preview must be at most 200 characters with no control characters"}
	errToolArgs     = ValidationError{Message: "tool arguments must be at most 65536 bytes"}
	errToolDuration = ValidationError{Message: "duration_ms must not be negative"}
)

func validToolName(name string) bool {
	n := utf8.RuneCountInString(name)
	return n >= 1 && n <= 64 && strings.IndexFunc(name, unicode.IsControl) < 0
}

func validToolPreview(p string) bool {
	n := utf8.RuneCountInString(p)
	return n <= 200 && strings.IndexFunc(p, unicode.IsControl) < 0
}

func validToolArguments(a string) bool {
	return len(a) <= 65536
}

// activityFromRow maps a stored activity to the bus's typed Activity.
func activityFromRow(row storage.Activity) (Activity, error) {
	id, err := ulid.ParseStrict(row.ID)
	if err != nil {
		return Activity{}, fmt.Errorf("stored activity has an invalid id %q: %w", row.ID, err)
	}
	roomID, err := ulid.ParseStrict(row.RoomID)
	if err != nil {
		return Activity{}, fmt.Errorf("stored activity %s has an invalid room id %q: %w", row.ID, row.RoomID, err)
	}
	agentID, err := ulid.ParseStrict(row.AgentID)
	if err != nil {
		return Activity{}, fmt.Errorf("stored activity %s has an invalid agent id %q: %w", row.ID, row.AgentID, err)
	}
	messageID, err := ulid.ParseStrict(row.MessageID)
	if err != nil {
		return Activity{}, fmt.Errorf("stored activity %s has an invalid message id %q: %w", row.ID, row.MessageID, err)
	}
	a := Activity{
		ID: id, RoomID: roomID, AgentID: agentID, MessageID: messageID,
		Kind: ActivityKind(row.Kind), Text: row.Text, Tool: row.Tool, Preview: row.Preview, Arguments: row.Arguments,
		CreatedAt: time.UnixMilli(row.CreatedAtMs).UTC(),
	}
	if row.DurationMs != nil {
		v := *row.DurationMs
		a.DurationMs = &v
	}
	if row.Succeeded != nil {
		v := *row.Succeeded
		a.Succeeded = &v
	}
	return a, nil
}

func activitiesFromRows(rows []storage.Activity) ([]Activity, error) {
	out := make([]Activity, 0, len(rows))
	for _, row := range rows {
		a, err := activityFromRow(row)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

// requireTurnDelivery returns the actor's delivery of the message, or the
// bus's own ErrNotFound when there is none.
func requireTurnDelivery(ctx context.Context, r storage.Repository, actorID ulid.ULID, messageID ulid.ULID) (storage.Delivery, error) {
	d, err := r.GetDelivery(ctx, actorID.String(), messageID.String())
	if errors.Is(err, storage.ErrNotFound) {
		return storage.Delivery{}, fmt.Errorf("delivery for %s: %w", messageID, ErrNotFound)
	}
	if err != nil {
		return storage.Delivery{}, fmt.Errorf("get delivery: %w", err)
	}
	return d, nil
}

// requireAgentInRoom returns the bus's own ErrNotFound unless the agent is a
// member of the room: an agent taken out of a room writes nothing more into it.
func requireAgentInRoom(ctx context.Context, r storage.Repository, roomID string, agentID ulid.ULID) error {
	rows, err := r.ListRoomAgents(ctx, roomID)
	if err != nil {
		return fmt.Errorf("list room agents: %w", err)
	}
	for _, a := range rows {
		if a.ID == agentID.String() {
			return nil
		}
	}
	return fmt.Errorf("room %s: %w", roomID, ErrNotFound)
}

// requireOpenTurn returns the actor's delivery of the message when the agent
// may report activity on it: the agent is still in the room, and the delivery
// was handed over and has not ended. A delivery still waiting, cancelled, or
// finished is ErrNoTurn.
func requireOpenTurn(ctx context.Context, r storage.Repository, actorID ulid.ULID, messageID ulid.ULID) (storage.Delivery, error) {
	d, err := requireTurnDelivery(ctx, r, actorID, messageID)
	if err != nil {
		return storage.Delivery{}, err
	}
	if err := requireAgentInRoom(ctx, r, d.RoomID, actorID); err != nil {
		return storage.Delivery{}, err
	}
	if st := DeliveryState(d.State); st != DeliveryDelivered && st != DeliveryWorking {
		return storage.Delivery{}, ErrNoTurn
	}
	return d, nil
}

// roomHasAgent reports whether agentID is a member of the room.
func roomHasAgent(ctx context.Context, r storage.Repository, roomID, agentID ulid.ULID) (bool, error) {
	rows, err := r.ListRoomAgents(ctx, roomID.String())
	if err != nil {
		return false, fmt.Errorf("list room agents: %w", err)
	}
	for _, a := range rows {
		if a.ID == agentID.String() {
			return true, nil
		}
	}
	return false, nil
}

// turnAllowed reports whether current may transition to target, per the plan's
// global constraint table. A no-op (current already equals target) is handled
// by the caller, so it is not listed here.
func turnAllowed(target, current DeliveryState) bool {
	switch {
	case target == DeliveryWorking:
		return current == DeliveryDelivered
	case target == DeliveryDone:
		return current == DeliveryDelivered || current == DeliveryWorking
	case target == DeliveryFailed:
		return current == DeliveryDelivered || current == DeliveryWorking
	default:
		return false
	}
}

// turnReport applies the state transition for the agent's delivery of the
// message. ErrNoTurn when the current state cannot reach it; a no-op when it
// already holds it. Each real change writes one delivery.changed event, and
// done and failed release the pair's next waiting head.
func (s *service) turnReport(ctx context.Context, actor Actor, messageID ulid.ULID, target DeliveryState) error {
	at := s.now()
	err := s.record(ctx, func(r storage.Repository) error {
		d, err := requireTurnDelivery(ctx, r, actor.ID, messageID)
		if err != nil {
			return err
		}
		cur := DeliveryState(d.State)
		if cur == target {
			return nil // a no-op: it already holds the state
		}
		if !turnAllowed(target, cur) {
			return ErrNoTurn
		}
		if err := r.SetDeliveryState(ctx, d.RoomID, actor.ID.String(), messageID.String(), string(target)); err != nil {
			return fmt.Errorf("set %s: %w", target, err)
		}
		if err := putEvent(ctx, r, EventDeliveryChanged, d.RoomID, messageID.String(), at); err != nil {
			return err
		}
		if target == DeliveryDone || target == DeliveryFailed {
			roomRow, err := r.GetRoom(ctx, d.RoomID)
			if errors.Is(err, storage.ErrNotFound) {
				return nil // the room is gone (its human is deleted); nothing to release
			}
			if err != nil {
				return fmt.Errorf("get room: %w", err)
			}
			ar, err := r.GetAgentByID(ctx, actor.ID.String())
			if err != nil {
				return fmt.Errorf("get agent: %w", err)
			}
			agent, err := agentFromRow(ar)
			if err != nil {
				return err
			}
			if err := s.releaseHead(ctx, r, roomRow, agent, at, true); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return wrapOp("report turn", err)
	}
	return nil
}

func (s *service) TurnWorking(ctx context.Context, actor Actor, messageID ulid.ULID) error {
	return s.turnReport(ctx, actor, messageID, DeliveryWorking)
}

func (s *service) TurnDone(ctx context.Context, actor Actor, messageID ulid.ULID) error {
	return s.turnReport(ctx, actor, messageID, DeliveryDone)
}

func (s *service) TurnFailed(ctx context.Context, actor Actor, messageID ulid.ULID) error {
	return s.turnReport(ctx, actor, messageID, DeliveryFailed)
}

// ReportRemark records a short between-step update the agent made while
// working on the delivered message, and returns the activity it created.
func (s *service) ReportRemark(ctx context.Context, actor Actor, messageID ulid.ULID, text string) (Activity, error) {
	if !validMessageText(text) {
		return Activity{}, errRemarkText
	}
	at := s.now()
	var created Activity
	err := s.record(ctx, func(r storage.Repository) error {
		d, err := requireOpenTurn(ctx, r, actor.ID, messageID)
		if err != nil {
			return err
		}
		id, err := newID(at)
		if err != nil {
			return fmt.Errorf("generate id: %w", err)
		}
		act := storage.Activity{ID: id.String(), RoomID: d.RoomID, AgentID: actor.ID.String(), MessageID: messageID.String(), Kind: string(ActivityRemark), Text: text, CreatedAtMs: at.UnixMilli()}
		if err := r.PutActivity(ctx, act); err != nil {
			return fmt.Errorf("put activity: %w", err)
		}
		if err := putEvent(ctx, r, EventActivityPosted, d.RoomID, id.String(), at); err != nil {
			return err
		}
		a, er := activityFromRow(act)
		if er != nil {
			return er
		}
		created = a
		return nil
	})
	if err != nil {
		return Activity{}, wrapOp("report remark", err)
	}
	return created, nil
}

// StartTool records a tool call the agent started while working on the
// delivered message. Its result is still pending (duration and success nil).
func (s *service) StartTool(ctx context.Context, actor Actor, messageID ulid.ULID, tool, preview, arguments string) (Activity, error) {
	if !validToolName(tool) {
		return Activity{}, errToolName
	}
	if !validToolPreview(preview) {
		return Activity{}, errToolPreview
	}
	if !validToolArguments(arguments) {
		return Activity{}, errToolArgs
	}
	at := s.now()
	var created Activity
	err := s.record(ctx, func(r storage.Repository) error {
		d, err := requireOpenTurn(ctx, r, actor.ID, messageID)
		if err != nil {
			return err
		}
		id, err := newID(at)
		if err != nil {
			return fmt.Errorf("generate id: %w", err)
		}
		act := storage.Activity{ID: id.String(), RoomID: d.RoomID, AgentID: actor.ID.String(), MessageID: messageID.String(), Kind: string(ActivityTool), Tool: tool, Preview: preview, Arguments: arguments, CreatedAtMs: at.UnixMilli()}
		if err := r.PutActivity(ctx, act); err != nil {
			return fmt.Errorf("put activity: %w", err)
		}
		if err := putEvent(ctx, r, EventActivityPosted, d.RoomID, id.String(), at); err != nil {
			return err
		}
		a, er := activityFromRow(act)
		if er != nil {
			return er
		}
		created = a
		return nil
	})
	if err != nil {
		return Activity{}, wrapOp("start tool", err)
	}
	return created, nil
}

// FinishTool records a tool call's result. The activity must be the actor's
// own and a tool (a remark has no result), so finishing one is ErrNotFound.
func (s *service) FinishTool(ctx context.Context, actor Actor, activityID ulid.ULID, durationMs int64, succeeded bool) (Activity, error) {
	if durationMs < 0 {
		return Activity{}, errToolDuration
	}
	at := s.now()
	var finished Activity
	err := s.record(ctx, func(r storage.Repository) error {
		row, err := r.GetActivity(ctx, activityID.String())
		if errors.Is(err, storage.ErrNotFound) {
			return fmt.Errorf("activity %s: %w", activityID, ErrNotFound)
		}
		if err != nil {
			return fmt.Errorf("get activity: %w", err)
		}
		if row.AgentID != actor.ID.String() {
			return ErrForbidden
		}
		if err := r.SetActivityResult(ctx, activityID.String(), durationMs, succeeded); err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				return ErrNotFound // remarks have no result, so finishing one is not found
			}
			return fmt.Errorf("set activity result: %w", err)
		}
		if err := putEvent(ctx, r, EventActivityPosted, row.RoomID, activityID.String(), at); err != nil {
			return err
		}
		got, err := r.GetActivity(ctx, activityID.String())
		if err != nil {
			return fmt.Errorf("get activity: %w", err)
		}
		a, er := activityFromRow(got)
		if er != nil {
			return er
		}
		finished = a
		return nil
	})
	if err != nil {
		return Activity{}, wrapOp("finish tool", err)
	}
	return finished, nil
}

// ListActivity returns a delivered message's activity, oldest first, for a
// human member of the room.
func (s *service) ListActivity(ctx context.Context, actor Actor, roomID, messageID ulid.ULID) ([]Activity, error) {
	if _, err := roomForMember(ctx, s.repo, actor, roomID); err != nil {
		return nil, err
	}
	if _, found, err := messageInRoom(ctx, s.repo, roomID, messageID); err != nil {
		return nil, err
	} else if !found {
		return nil, fmt.Errorf("message %s: %w", messageID, ErrNotFound)
	}
	rows, err := s.repo.ListActivities(ctx, roomID.String(), messageID.String())
	if err != nil {
		return nil, fmt.Errorf("list activities: %w", err)
	}
	return activitiesFromRows(rows)
}

// StopTurn tells one agent to stop its turn in the room. It is a control
// event naming the room, with no state change, so a turn that is not running
// is still a no-op success.
func (s *service) StopTurn(ctx context.Context, actor Actor, roomID, agentID ulid.ULID) error {
	if actor.Kind != ActorHuman {
		return ErrForbidden
	}
	at := s.now()
	err := s.record(ctx, func(r storage.Repository) error {
		if _, err := roomForMember(ctx, r, actor, roomID); err != nil {
			return err
		}
		member, err := roomHasAgent(ctx, r, roomID, agentID)
		if err != nil {
			return err
		}
		if !member {
			return fmt.Errorf("agent %s: %w", agentID, ErrNotFound)
		}
		if err := putAgentEvent(ctx, r, EventControlStop, roomID.String(), roomID.String(), agentID, at); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return wrapOp("stop turn", err)
	}
	return nil
}

// Steer sends the agent a control message during its running turn. It requires
// the pair's delivery to be working, and it creates no delivery row: steer is
// applied at the next tool boundary without ending the turn.
func (s *service) Steer(ctx context.Context, actor Actor, roomID, agentID ulid.ULID, text string) (Message, error) {
	if actor.Kind != ActorHuman {
		return Message{}, ErrForbidden
	}
	if !validMessageText(text) {
		return Message{}, errMessageText
	}
	at := s.now()
	var posted Message
	err := s.record(ctx, func(r storage.Repository) error {
		row, err := roomForMember(ctx, r, actor, roomID)
		if err != nil {
			return err
		}
		if row.ArchivedAtMs != nil {
			return ErrRoomArchived
		}
		member, err := roomHasAgent(ctx, r, roomID, agentID)
		if err != nil {
			return err
		}
		if !member {
			return fmt.Errorf("agent %s: %w", agentID, ErrNotFound)
		}
		_, err = r.GetWorkingDelivery(ctx, roomID.String(), agentID.String())
		if errors.Is(err, storage.ErrNotFound) {
			return ErrNoTurn
		}
		if err != nil {
			return fmt.Errorf("get working delivery: %w", err)
		}
		id, err := newID(at)
		if err != nil {
			return fmt.Errorf("generate id: %w", err)
		}
		msg := storage.Message{ID: id.String(), RoomID: row.ID, Body: text, CreatedAtMs: at.UnixMilli()}
		human := actor.ID.String()
		msg.AuthorHumanID = &human
		if err := r.PutMessage(ctx, msg); err != nil {
			return fmt.Errorf("put message: %w", err)
		}
		if err := putEvent(ctx, r, EventMessagePosted, row.ID, msg.ID, at); err != nil {
			return err
		}
		if err := putAgentEvent(ctx, r, EventControlSteer, row.ID, msg.ID, agentID, at); err != nil {
			return err
		}
		built, err := messagesFromRows(ctx, r, []storage.Message{msg})
		if err != nil {
			return err
		}
		posted = built[0]
		return nil
	})
	if err != nil {
		return Message{}, wrapOp("steer", err)
	}
	return posted, nil
}
