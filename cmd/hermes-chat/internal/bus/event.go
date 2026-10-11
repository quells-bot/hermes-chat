package bus

import (
	"context"
	"fmt"
	"time"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/storage"
)

// EventKind says what an entry in the event log is about.
type EventKind string

const (
	EventRoomChanged   EventKind = "room.changed"   // subject: the room's id
	EventMessagePosted EventKind = "message.posted" // subject: the message's id
)

// putEvent appends an event to the log. Call it inside the transaction that
// made the change, so the two land together or not at all.
func putEvent(ctx context.Context, r storage.Repository, kind EventKind, roomID, subjectID string, at time.Time) error {
	if _, err := r.PutEvent(ctx, storage.Event{RoomID: roomID, Kind: string(kind), SubjectID: subjectID, CreatedAtMs: at.UnixMilli()}); err != nil {
		return fmt.Errorf("put %s event: %w", kind, err)
	}
	return nil
}
