package storage

import (
	"context"
	"database/sql"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/storage/sqlcgen"
)

// Activity is one line an agent contributed to a message: a remark it made
// while reading, or a tool call it ran that yielded a preview.
type Activity struct {
	ID, RoomID, AgentID, MessageID       string
	Kind, Text, Tool, Preview, Arguments string // Kind: "remark" or "tool"
	DurationMs                           *int64 // nil while the tool runs
	Succeeded                            *bool  // nil while the tool runs
	CreatedAtMs                          int64  // Unix milliseconds, UTC
}

func activityFromRow(row sqlcgen.Activity) Activity {
	a := Activity{
		ID: row.ID, RoomID: row.RoomID, AgentID: row.AgentID, MessageID: row.MessageID,
		Kind: row.Kind, Text: row.Text, Tool: row.Tool, Preview: row.Preview, Arguments: row.Arguments,
		Succeeded: boolPtr(row.Succeeded), CreatedAtMs: row.CreatedAt,
	}
	if row.DurationMs.Valid {
		v := row.DurationMs.Int64
		a.DurationMs = &v
	}
	return a
}

func (r *repo) PutActivity(ctx context.Context, a Activity) error {
	return mapErr(r.writes.InsertActivity(ctx, sqlcgen.InsertActivityParams{
		ID: a.ID, RoomID: a.RoomID, AgentID: a.AgentID, MessageID: a.MessageID,
		Kind: a.Kind, Text: a.Text, Tool: a.Tool, Preview: a.Preview, Arguments: a.Arguments,
		DurationMs: nullInt64(a.DurationMs), Succeeded: nullBool(a.Succeeded), CreatedAt: a.CreatedAtMs,
	}))
}

func (r *repo) GetActivity(ctx context.Context, id string) (Activity, error) {
	row, err := r.reads.GetActivity(ctx, id)
	if err != nil {
		return Activity{}, mapErr(err)
	}
	return activityFromRow(row), nil
}

func (r *repo) SetActivityResult(ctx context.Context, id string, durationMs int64, succeeded bool) error {
	return requireRow(r.writes.SetActivityResult(ctx, sqlcgen.SetActivityResultParams{
		DurationMs: sql.NullInt64{Int64: durationMs, Valid: true}, Succeeded: nullBool(&succeeded), ID: id,
	}))
}

func (r *repo) ListActivities(ctx context.Context, roomID, messageID string) ([]Activity, error) {
	rows, err := r.reads.ListActivities(ctx, sqlcgen.ListActivitiesParams{RoomID: roomID, MessageID: messageID})
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]Activity, 0, len(rows))
	for _, row := range rows {
		out = append(out, activityFromRow(row))
	}
	return out, nil
}
