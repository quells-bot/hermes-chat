package storage

import (
	"context"
	"database/sql"
	"errors"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/storage/sqlcgen"
)

// NoteEntry is one thing an agent added to a human's note. It is pending until approved.
type NoteEntry struct {
	ID, HumanID, AgentID, Text string
	ApprovedAtMs               *int64 // nil while in quarantine
	CreatedAtMs                int64
}

func noteEntryFromRow(row sqlcgen.NoteEntry) NoteEntry {
	e := NoteEntry{ID: row.ID, HumanID: row.HumanID, AgentID: row.AgentID, Text: row.Text, CreatedAtMs: row.CreatedAt}
	if row.ApprovedAt.Valid {
		v := row.ApprovedAt.Int64
		e.ApprovedAtMs = &v
	}
	return e
}

func (r *repo) GetNoteText(ctx context.Context, humanID string) (string, error) {
	text, err := r.reads.GetNoteText(ctx, humanID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil // the human has written no note yet
	}
	if err != nil {
		return "", mapErr(err)
	}
	return text, nil
}

func (r *repo) PutNoteText(ctx context.Context, humanID, text string, updatedAtMs int64) error {
	return mapErr(r.writes.UpsertNoteText(ctx, sqlcgen.UpsertNoteTextParams{HumanID: humanID, Text: text, UpdatedAt: updatedAtMs}))
}

func (r *repo) PutNoteEntry(ctx context.Context, e NoteEntry) error {
	return mapErr(r.writes.InsertNoteEntry(ctx, sqlcgen.InsertNoteEntryParams{
		ID: e.ID, HumanID: e.HumanID, AgentID: e.AgentID, Text: e.Text,
		ApprovedAt: nullInt64(e.ApprovedAtMs), CreatedAt: e.CreatedAtMs,
	}))
}

func (r *repo) GetNoteEntry(ctx context.Context, id string) (NoteEntry, error) {
	row, err := r.reads.GetNoteEntry(ctx, id)
	if err != nil {
		return NoteEntry{}, mapErr(err)
	}
	return noteEntryFromRow(row), nil
}

func (r *repo) ListNoteEntries(ctx context.Context, humanID string) ([]NoteEntry, error) {
	rows, err := r.reads.ListNoteEntries(ctx, humanID)
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]NoteEntry, 0, len(rows))
	for _, row := range rows {
		out = append(out, noteEntryFromRow(row))
	}
	return out, nil
}

func (r *repo) SetNoteEntryText(ctx context.Context, id, text string) error {
	return requireRow(r.writes.SetNoteEntryText(ctx, sqlcgen.SetNoteEntryTextParams{Text: text, ID: id}))
}

func (r *repo) SetNoteEntryApprovedAt(ctx context.Context, id string, approvedAtMs *int64) error {
	return requireRow(r.writes.SetNoteEntryApprovedAt(ctx, sqlcgen.SetNoteEntryApprovedAtParams{ApprovedAt: nullInt64(approvedAtMs), ID: id}))
}

func (r *repo) DeleteNoteEntry(ctx context.Context, id string) error {
	return requireRow(r.writes.DeleteNoteEntry(ctx, id))
}