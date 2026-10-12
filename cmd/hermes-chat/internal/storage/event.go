package storage

import (
	"context"
	"database/sql"
	"errors"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/storage/sqlcgen"
)

// Event is one entry in the event log; SubjectID names what the event is about.
type Event struct {
	ID                      int64 // assigned by the log
	RoomID, Kind, SubjectID string
	AgentID                 *string // set on agent-targeted rows, nil otherwise
	CreatedAtMs             int64   // Unix milliseconds, UTC
}

func eventFromRow(id int64, roomID, kind, subjectID string, agentID sql.NullString, createdAt int64) Event {
	return Event{
		ID: id, RoomID: roomID, Kind: kind, SubjectID: subjectID,
		AgentID: stringPtr(agentID), CreatedAtMs: createdAt,
	}
}

func (r *repo) PutEvent(ctx context.Context, e Event) (int64, error) {
	id, err := r.writes.InsertEvent(ctx, sqlcgen.InsertEventParams{
		RoomID: e.RoomID, Kind: e.Kind, SubjectID: e.SubjectID, AgentID: nullString(e.AgentID), CreatedAt: e.CreatedAtMs,
	})
	if err != nil {
		return 0, mapErr(err)
	}
	return id, nil
}

func (r *repo) ListEventsForHuman(ctx context.Context, humanID string, afterID, limit int64) ([]Event, error) {
	rows, err := r.reads.ListEventsForHuman(ctx, sqlcgen.ListEventsForHumanParams{ID: afterID, HumanID: humanID, Limit: limit})
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]Event, 0, len(rows))
	for _, row := range rows {
		out = append(out, eventFromRow(row.ID, row.RoomID, row.Kind, row.SubjectID, row.AgentID, row.CreatedAt))
	}
	return out, nil
}

func (r *repo) ListEventsForAgent(ctx context.Context, agentID string, afterID, limit int64) ([]Event, error) {
	rows, err := r.reads.ListEventsForAgent(ctx, sqlcgen.ListEventsForAgentParams{ID: afterID, AgentID: nullString(&agentID), Limit: limit})
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]Event, 0, len(rows))
	for _, row := range rows {
		out = append(out, eventFromRow(row.ID, row.RoomID, row.Kind, row.SubjectID, row.AgentID, row.CreatedAt))
	}
	return out, nil
}

func (r *repo) ListUnacknowledgedDeliveryEvents(ctx context.Context, agentID string, throughID int64) ([]Event, error) {
	rows, err := r.reads.ListUnacknowledgedDeliveryEvents(ctx, sqlcgen.ListUnacknowledgedDeliveryEventsParams{AgentID: nullString(&agentID), ID: throughID})
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]Event, 0, len(rows))
	for _, row := range rows {
		out = append(out, eventFromRow(row.ID, row.RoomID, row.Kind, row.SubjectID, row.AgentID, row.CreatedAt))
	}
	return out, nil
}

func (r *repo) GetLastEventID(ctx context.Context) (int64, error) {
	id, err := r.reads.GetLastEventID(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, mapErr(err)
	}
	return id, nil
}
