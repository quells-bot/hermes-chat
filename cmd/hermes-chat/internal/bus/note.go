package bus

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/oklog/ulid/v2"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/storage"
)

const (
	MaxNoteTextChars      = 2000 // the human's own text
	MaxNoteEntryChars     = 300  // one agent entry
	MaxNoteApprovedChars  = 4000 // the human's text plus every approved entry
	MaxPendingNoteEntries = 50   // a human's quarantine
)

// Note is one human's note: text they wrote, plus the entries agents added.
type Note struct {
	Text    string
	Entries []NoteEntry // oldest first, pending and approved; never nil
}

// NoteEntry is one thing an agent added to a human's note.
type NoteEntry struct {
	ID        ulid.ULID
	Agent     Agent
	Text      string
	Approved  bool
	CreatedAt time.Time
}

// NoteEntryUpdate changes what is non-nil: Text first, then Approved.
type NoteEntryUpdate struct {
	Text     *string
	Approved *bool
}

var (
	errNoteText      = ValidationError{Message: "note text must be at most 2000 characters with no NUL character"}
	errNoteEntry     = ValidationError{Message: "a note entry must be 1-300 characters with no NUL character"}
	errNoteApproved  = ValidationError{Message: "the approved note would exceed 4000 characters; shorten or delete something first"}
	errNoteQuarantine = ValidationError{Message: "this human's quarantine is full"}
	errNoHumanAuthor = ValidationError{Message: "the delivered message has no human author"}
)

// composeNote is what an agent is given: the text, a blank line, then one "- "
// line per approved entry. "" when both are empty.
func composeNote(text string, approved []string) string {
	if text == "" && len(approved) == 0 {
		return ""
	}
	var out string
	if text != "" {
		out = text
		if len(approved) > 0 {
			out += "\n\n"
		}
	}
	for i, a := range approved {
		if i > 0 {
			out += "\n"
		}
		out += "- " + a
	}
	return out
}

// approvedRunes is how many characters the note would be: the text plus every
// approved entry. Runes, because the caps count characters, not bytes.
func approvedRunes(text string, rows []storage.NoteEntry) int {
	n := utf8.RuneCountInString(text)
	for _, e := range rows {
		if e.ApprovedAtMs != nil {
			n += utf8.RuneCountInString(e.Text)
		}
	}
	return n
}

// noteEntryFromStorage maps a stored entry to the bus's, resolving the agent
// that added it.
func noteEntryFromStorage(ctx context.Context, r storage.Repository, row storage.NoteEntry) (NoteEntry, error) {
	ar, err := r.GetAgentByID(ctx, row.AgentID)
	if err != nil {
		return NoteEntry{}, fmt.Errorf("get note entry agent: %w", err)
	}
	agent, err := agentFromRow(ar)
	if err != nil {
		return NoteEntry{}, err
	}
	id, err := ulid.ParseStrict(row.ID)
	if err != nil {
		return NoteEntry{}, fmt.Errorf("stored note entry has an invalid id %q: %w", row.ID, err)
	}
	return NoteEntry{
		ID: id, Agent: agent, Text: row.Text,
		Approved: row.ApprovedAtMs != nil, CreatedAt: time.UnixMilli(row.CreatedAtMs).UTC(),
	}, nil
}

// noteFor loads a human's note: its text and every entry, oldest first.
func (s *service) noteFor(ctx context.Context, r storage.Repository, humanID string) (Note, error) {
	text, err := r.GetNoteText(ctx, humanID)
	if err != nil {
		return Note{}, fmt.Errorf("get note text: %w", err)
	}
	rows, err := r.ListNoteEntries(ctx, humanID)
	if err != nil {
		return Note{}, fmt.Errorf("list note entries: %w", err)
	}
	out := make([]NoteEntry, 0, len(rows))
	for _, row := range rows {
		entry, err := noteEntryFromStorage(ctx, r, row)
		if err != nil {
			return Note{}, err
		}
		out = append(out, entry)
	}
	return Note{Text: text, Entries: out}, nil
}

// GetNote returns the caller's own note. A human sees only their own.
func (s *service) GetNote(ctx context.Context, actor Actor) (Note, error) {
	if actor.Kind != ActorHuman {
		return Note{}, ErrForbidden
	}
	return s.noteFor(ctx, s.repo, actor.ID.String())
}

// SetNoteText replaces the caller's note text. It is not record: the change
// writes no event, so it must not ring the hub. The empty text clears the note.
func (s *service) SetNoteText(ctx context.Context, actor Actor, text string) (Note, error) {
	if actor.Kind != ActorHuman {
		return Note{}, ErrForbidden
	}
	trimmed := strings.TrimSpace(text)
	if utf8.RuneCountInString(trimmed) > MaxNoteTextChars || strings.ContainsRune(trimmed, 0) {
		return Note{}, errNoteText
	}
	var got Note
	err := s.repo.InTx(ctx, func(r storage.Repository) error {
		rows, err := r.ListNoteEntries(ctx, actor.ID.String())
		if err != nil {
			return fmt.Errorf("list note entries: %w", err)
		}
		if approvedRunes(trimmed, rows) > MaxNoteApprovedChars {
			return errNoteApproved
		}
		if err := r.PutNoteText(ctx, actor.ID.String(), trimmed, s.now().UnixMilli()); err != nil {
			return fmt.Errorf("put note text: %w", err)
		}
		n, err := s.noteFor(ctx, r, actor.ID.String())
		if err != nil {
			return err
		}
		got = n
		return nil
	})
	if err != nil {
		return Note{}, wrapOp("set note text", err)
	}
	return got, nil
}

// UpdateNoteEntry changes one of the caller's own entries: its text, whether
// it is approved, or both. The approved total must hold after the change.
func (s *service) UpdateNoteEntry(ctx context.Context, actor Actor, entryID ulid.ULID, u NoteEntryUpdate) (Note, error) {
	if actor.Kind != ActorHuman {
		return Note{}, ErrForbidden
	}
	var got Note
	err := s.repo.InTx(ctx, func(r storage.Repository) error {
		row, err := r.GetNoteEntry(ctx, entryID.String())
		if errors.Is(err, storage.ErrNotFound) {
			return fmt.Errorf("note entry %s: %w", entryID, ErrNotFound)
		}
		if err != nil {
			return fmt.Errorf("get note entry: %w", err)
		}
		if row.HumanID != actor.ID.String() {
			return fmt.Errorf("note entry %s: %w", entryID, ErrNotFound)
		}
		if u.Text != nil {
			trimmed := strings.TrimSpace(*u.Text)
			n := utf8.RuneCountInString(trimmed)
			if n < 1 || n > MaxNoteEntryChars || strings.ContainsRune(trimmed, 0) {
				return errNoteEntry
			}
			if err := r.SetNoteEntryText(ctx, entryID.String(), trimmed); err != nil {
				return fmt.Errorf("set note entry text: %w", err)
			}
		}
		if u.Approved != nil {
			var ms *int64
			if *u.Approved {
				v := s.now().UnixMilli()
				ms = &v
			}
			if err := r.SetNoteEntryApprovedAt(ctx, entryID.String(), ms); err != nil {
				return fmt.Errorf("set note entry approved: %w", err)
			}
		}
		rows, err := r.ListNoteEntries(ctx, row.HumanID)
		if err != nil {
			return fmt.Errorf("list note entries: %w", err)
		}
		text, err := r.GetNoteText(ctx, row.HumanID)
		if err != nil {
			return fmt.Errorf("get note text: %w", err)
		}
		if approvedRunes(text, rows) > MaxNoteApprovedChars {
			return errNoteApproved
		}
		n, err := s.noteFor(ctx, r, row.HumanID)
		if err != nil {
			return err
		}
		got = n
		return nil
	})
	if err != nil {
		return Note{}, wrapOp("update note entry", err)
	}
	return got, nil
}

// DeleteNoteEntry removes one of the caller's own entries.
func (s *service) DeleteNoteEntry(ctx context.Context, actor Actor, entryID ulid.ULID) (Note, error) {
	if actor.Kind != ActorHuman {
		return Note{}, ErrForbidden
	}
	var got Note
	err := s.repo.InTx(ctx, func(r storage.Repository) error {
		row, err := r.GetNoteEntry(ctx, entryID.String())
		if errors.Is(err, storage.ErrNotFound) {
			return fmt.Errorf("note entry %s: %w", entryID, ErrNotFound)
		}
		if err != nil {
			return fmt.Errorf("get note entry: %w", err)
		}
		if row.HumanID != actor.ID.String() {
			return fmt.Errorf("note entry %s: %w", entryID, ErrNotFound)
		}
		if err := r.DeleteNoteEntry(ctx, entryID.String()); err != nil {
			return fmt.Errorf("delete note entry: %w", err)
		}
		n, err := s.noteFor(ctx, r, row.HumanID)
		if err != nil {
			return err
		}
		got = n
		return nil
	})
	if err != nil {
		return Note{}, wrapOp("delete note entry", err)
	}
	return got, nil
}

// SuggestNoteEntry adds an agent's suggestion to the note of the human who
// wrote the delivered message, during the agent's open turn on it. The entry
// is quarantined until its human approves it. Suggesting the same text again
// returns the first entry with created false.
func (s *service) SuggestNoteEntry(ctx context.Context, actor Actor, messageID ulid.ULID, text string) (NoteEntry, bool, error) {
	if actor.Kind != ActorAgent {
		return NoteEntry{}, false, ErrForbidden
	}
	trimmed := strings.TrimSpace(text)
	n := utf8.RuneCountInString(trimmed)
	if n < 1 || n > MaxNoteEntryChars || strings.ContainsRune(trimmed, 0) {
		return NoteEntry{}, false, errNoteEntry
	}
	var got NoteEntry
	var created bool
	err := s.repo.InTx(ctx, func(r storage.Repository) error {
		d, err := requireOpenTurn(ctx, r, actor.ID, messageID)
		if err != nil {
			return err
		}
		mrow, err := r.GetMessage(ctx, d.MessageID)
		if err != nil {
			return fmt.Errorf("get message: %w", err)
		}
		if mrow.AuthorHumanID == nil {
			// the delivered message was not written by a human still registered
			return errNoHumanAuthor
		}
		humanID := *mrow.AuthorHumanID
		rows, err := r.ListNoteEntries(ctx, humanID)
		if err != nil {
			return fmt.Errorf("list note entries: %w", err)
		}
		for _, row := range rows {
			if row.AgentID == actor.ID.String() && row.Text == trimmed {
				e, err := noteEntryFromStorage(ctx, r, row)
				if err != nil {
					return err
				}
				got = e
				return nil // a retry after a reconnect: the first entry stands
			}
		}
		pending := 0
		for _, row := range rows {
			if row.ApprovedAtMs == nil {
				pending++
			}
		}
		if pending >= MaxPendingNoteEntries {
			return errNoteQuarantine
		}
		at := s.now()
		id, err := newID(at)
		if err != nil {
			return fmt.Errorf("generate id: %w", err)
		}
		if err := r.PutNoteEntry(ctx, storage.NoteEntry{
			ID: id.String(), HumanID: humanID, AgentID: actor.ID.String(), Text: trimmed, CreatedAtMs: at.UnixMilli(),
		}); err != nil {
			return fmt.Errorf("put note entry: %w", err)
		}
		row, err := r.GetNoteEntry(ctx, id.String())
		if err != nil {
			return fmt.Errorf("get note entry: %w", err)
		}
		e, err := noteEntryFromStorage(ctx, r, row)
		if err != nil {
			return err
		}
		got = e
		created = true
		return nil
	})
	if err != nil {
		return NoteEntry{}, false, wrapOp("suggest note entry", err)
	}
	return got, created, nil
}