package storage

import (
	"context"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/storage/sqlcgen"
)

// Message is a message posted in a room by a human or an agent.
type Message struct {
	ID, RoomID                   string  // ULID text
	AuthorHumanID, AuthorAgentID *string // at most one is set; both nil once the human who wrote it is removed
	ReplyToID                    *string
	Body                         string
	CreatedAtMs                  int64 // Unix milliseconds, UTC
}

func messageFromRow(row sqlcgen.Message) Message {
	return Message{
		ID: row.ID, RoomID: row.RoomID,
		AuthorHumanID: stringPtr(row.AuthorHumanID), AuthorAgentID: stringPtr(row.AuthorAgentID),
		ReplyToID: stringPtr(row.ReplyToID),
		Body:      row.Body, CreatedAtMs: row.CreatedAt,
	}
}

func (r *repo) PutMessage(ctx context.Context, m Message) error {
	return mapErr(r.writes.InsertMessage(ctx, sqlcgen.InsertMessageParams{
		ID: m.ID, RoomID: m.RoomID,
		AuthorHumanID: nullString(m.AuthorHumanID), AuthorAgentID: nullString(m.AuthorAgentID),
		ReplyToID: nullString(m.ReplyToID),
		Body:      m.Body, CreatedAt: m.CreatedAtMs,
	}))
}

func (r *repo) GetMessage(ctx context.Context, id string) (Message, error) {
	row, err := r.reads.GetMessage(ctx, id)
	if err != nil {
		return Message{}, mapErr(err)
	}
	return messageFromRow(row), nil
}

func (r *repo) ListMessages(ctx context.Context, roomID, beforeID string, limit int64) ([]Message, error) {
	var rows []sqlcgen.Message
	var err error
	if beforeID == "" {
		rows, err = r.reads.ListNewestMessages(ctx, sqlcgen.ListNewestMessagesParams{RoomID: roomID, Limit: limit})
	} else {
		rows, err = r.reads.ListMessagesBefore(ctx, sqlcgen.ListMessagesBeforeParams{RoomID: roomID, ID: beforeID, Limit: limit})
	}
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]Message, 0, len(rows))
	for _, row := range rows {
		out = append(out, messageFromRow(row))
	}
	return out, nil
}

func (r *repo) GetLastMessageID(ctx context.Context, roomID string) (string, error) {
	id, err := r.reads.GetLastMessageID(ctx, roomID)
	if err != nil {
		return "", mapErr(err)
	}
	return id, nil
}
