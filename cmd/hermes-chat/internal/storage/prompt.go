package storage

import (
	"context"
	"database/sql"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/storage/sqlcgen"
)

// Prompt is the question an agent's message asks. Choices is a JSON array of strings.
type Prompt struct {
	MessageID, RoomID, AgentID string
	Kind, Choices              string  // Kind: "approval" or "clarify"
	State, Answer              string  // State: "open", "answered", or "expired"
	AnsweredBy                 *string // a human id; nil unless answered, and once that human is removed
	ClosedAtMs                 *int64  // nil while open
}

func promptFromRow(row sqlcgen.Prompt) Prompt {
	p := Prompt{
		MessageID: row.MessageID, RoomID: row.RoomID, AgentID: row.AgentID,
		Kind: row.Kind, Choices: row.Choices, State: row.State, Answer: row.Answer,
		AnsweredBy: stringPtr(row.AnsweredBy),
	}
	if row.ClosedAt.Valid {
		v := row.ClosedAt.Int64
		p.ClosedAtMs = &v
	}
	return p
}

func promptsFromRows(rows []sqlcgen.Prompt) []Prompt {
	out := make([]Prompt, 0, len(rows))
	for _, row := range rows {
		out = append(out, promptFromRow(row))
	}
	return out
}

func (r *repo) PutPrompt(ctx context.Context, p Prompt) error {
	// Only the first five fields are written: a new prompt is open.
	return mapErr(r.writes.InsertPrompt(ctx, sqlcgen.InsertPromptParams{
		MessageID: p.MessageID, RoomID: p.RoomID, AgentID: p.AgentID, Kind: p.Kind, Choices: p.Choices,
	}))
}

func (r *repo) GetPrompt(ctx context.Context, messageID string) (Prompt, error) {
	row, err := r.reads.GetPrompt(ctx, messageID)
	if err != nil {
		return Prompt{}, mapErr(err)
	}
	return promptFromRow(row), nil
}

func (r *repo) ListPromptsInRange(ctx context.Context, roomID, firstID, lastID string) ([]Prompt, error) {
	rows, err := r.reads.ListPromptsInRange(ctx, sqlcgen.ListPromptsInRangeParams{RoomID: roomID, MessageID: firstID, MessageID_2: lastID})
	if err != nil {
		return nil, mapErr(err)
	}
	return promptsFromRows(rows), nil
}

func (r *repo) ListOpenPrompts(ctx context.Context, roomID string) ([]Prompt, error) {
	var rows []sqlcgen.Prompt
	var err error
	if roomID == "" {
		rows, err = r.reads.ListOpenPrompts(ctx)
	} else {
		rows, err = r.reads.ListOpenPromptsInRoom(ctx, roomID)
	}
	if err != nil {
		return nil, mapErr(err)
	}
	return promptsFromRows(rows), nil
}

func (r *repo) CountOpenPrompts(ctx context.Context, roomID string) (int64, error) {
	return r.reads.CountOpenPrompts(ctx, roomID)
}

func (r *repo) ClosePrompt(ctx context.Context, messageID, state, answer string, answeredBy *string, closedAtMs int64) error {
	return requireRow(r.writes.ClosePrompt(ctx, sqlcgen.ClosePromptParams{
		State: state, Answer: answer, AnsweredBy: nullString(answeredBy), ClosedAt: sql.NullInt64{Int64: closedAtMs, Valid: true}, MessageID: messageID,
	}))
}