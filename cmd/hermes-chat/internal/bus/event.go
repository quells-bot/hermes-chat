package bus

import (
	"context"
	"fmt"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/storage"
)

// EventKind says what an entry in the event log is about.
type EventKind string

const (
	EventRoomChanged     EventKind = "room.changed"     // subject: the room's id
	EventMessagePosted   EventKind = "message.posted"   // subject: the message's id
	EventDelivery        EventKind = "delivery"         // agent: subject is the delivered message
	EventDeliveryChanged EventKind = "delivery.changed" // human: subject is the message
	EventActivityPosted  EventKind = "activity.posted"  // human: subject is the activity
	EventMessageEdited   EventKind = "message.edited"   // human: subject is the message, finalize only
	EventControlStop     EventKind = "stop"             // agent: subject is the room
	EventControlSteer    EventKind = "steer"            // agent: subject is the steer message
	EventControlPause    EventKind = "pause"            // agent: subject is the room
	EventAgentPresence   EventKind = "agent.presence"   // human: subject is the agent
)

// contextMessages and contextTextBytes bound the context an agent-targeted
// delivery event carries: at most contextMessages messages, older than the
// delivered one, totalling at most contextTextBytes of text, channels only.
const (
	contextMessages  = 20
	contextTextBytes = 4096
)

// Event is one entry of the log, as one subscriber sees it.
type Event struct {
	ID        int64 // the stream cursor
	Kind      EventKind
	RoomID    ulid.ULID
	At        time.Time
	Room      *Room     // room.changed, and delivery for an agent: the room as it is now
	Message   *Message  // message.posted, delivery, delivery.changed, message.edited, and steer
	Activity  *Activity // activity.posted: the activity, which carries its turn's message id
	Agent     *Agent    // agent.presence: the agent whose connection changed
	Context   []Message // delivery on a channel: the messages leading up to the delivered one
	Connected bool      // agent.presence: whether the agent is connected now
}

// putEvent appends an event to the log. Call it inside the transaction that
// made the change, so the two land together or not at all.
func putEvent(ctx context.Context, r storage.Repository, kind EventKind, roomID, subjectID string, at time.Time) error {
	if _, err := r.PutEvent(ctx, storage.Event{RoomID: roomID, Kind: string(kind), SubjectID: subjectID, CreatedAtMs: at.UnixMilli()}); err != nil {
		return fmt.Errorf("put %s event: %w", kind, err)
	}
	return nil
}

// putAgentEvent is putEvent for rows addressed to one agent, so only that
// agent's stream sees them.
func putAgentEvent(ctx context.Context, r storage.Repository, kind EventKind, roomID, subjectID string, agentID ulid.ULID, at time.Time) error {
	agent := agentID.String()
	if _, err := r.PutEvent(ctx, storage.Event{RoomID: roomID, Kind: string(kind), SubjectID: subjectID, AgentID: &agent, CreatedAtMs: at.UnixMilli()}); err != nil {
		return fmt.Errorf("put %s event: %w", kind, err)
	}
	return nil
}
