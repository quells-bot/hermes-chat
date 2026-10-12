package storage

import (
	"context"
	"database/sql"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/storage/sqlcgen"
)

// Attachment is a stored file's metadata. The file itself is in Files, under ID.
type Attachment struct {
	ID, RoomID                       string
	MessageID                        *string // nil until a message carries it
	UploaderHumanID, UploaderAgentID *string // at most one is set
	Name, ContentType                string
	Size, CreatedAtMs                int64
}

func attachmentFromRow(row sqlcgen.Attachment) Attachment {
	return Attachment{
		ID: row.ID, RoomID: row.RoomID, Name: row.Name, ContentType: row.ContentType,
		Size: row.Size, CreatedAtMs: row.CreatedAt,
		MessageID:        stringPtr(row.MessageID),
		UploaderHumanID:  stringPtr(row.UploaderHumanID),
		UploaderAgentID:  stringPtr(row.UploaderAgentID),
	}
}

func (r *repo) PutAttachment(ctx context.Context, a Attachment) error {
	// MessageID is not written: a new attachment is on no message.
	return mapErr(r.writes.InsertAttachment(ctx, sqlcgen.InsertAttachmentParams{
		ID: a.ID, RoomID: a.RoomID, UploaderHumanID: nullString(a.UploaderHumanID), UploaderAgentID: nullString(a.UploaderAgentID),
		Name: a.Name, ContentType: a.ContentType, Size: a.Size, CreatedAt: a.CreatedAtMs,
	}))
}

func (r *repo) GetAttachment(ctx context.Context, id string) (Attachment, error) {
	row, err := r.reads.GetAttachment(ctx, id)
	if err != nil {
		return Attachment{}, mapErr(err)
	}
	return attachmentFromRow(row), nil
}

func (r *repo) AttachToMessage(ctx context.Context, id, messageID string) error {
	return requireRow(r.writes.AttachToMessage(ctx, sqlcgen.AttachToMessageParams{MessageID: sql.NullString{String: messageID, Valid: true}, ID: id}))
}

func (r *repo) ListAttachmentsInRange(ctx context.Context, roomID, firstID, lastID string) ([]Attachment, error) {
	rows, err := r.reads.ListAttachmentsInRange(ctx, sqlcgen.ListAttachmentsInRangeParams{
		RoomID: roomID, MessageID: sql.NullString{String: firstID, Valid: true}, MessageID_2: sql.NullString{String: lastID, Valid: true},
	})
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]Attachment, 0, len(rows))
	for _, row := range rows {
		out = append(out, attachmentFromRow(row))
	}
	return out, nil
}