package bus

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/storage"
)

const (
	MaxMessageBytes = 262144
	DefaultPageSize = 50
	MaxPageSize     = 200
)

// Author is who wrote a message.
type Author struct {
	Kind ActorKind
	ID   ulid.ULID
	Name string // a human's username, or an agent's display name
}

// Message is a message posted in a room.
type Message struct {
	ID        ulid.ULID
	RoomID    ulid.ULID
	Author    *Author // nil when the human who wrote it has been removed
	Text      string
	ReplyToID ulid.ULID // zero when not a reply
	CreatedAt time.Time
}

var (
	errMessageText   = ValidationError{Message: "message text must be 1 to 262144 bytes, not blank, with no NUL character"}
	errReplyTo       = ValidationError{Message: "reply_to must be a message in this room"}
	errReadMessageID = ValidationError{Message: "message_id must be a message in this room"}
)

func validMessageText(text string) bool {
	return len(text) <= MaxMessageBytes && strings.TrimSpace(text) != "" && !strings.ContainsRune(text, 0)
}

// messageInRoom returns the message if it exists and belongs to the room.
// found is false for a missing message and for one in another room.
func messageInRoom(ctx context.Context, r storage.Repository, roomID, messageID ulid.ULID) (row storage.Message, found bool, err error) {
	row, err = r.GetMessage(ctx, messageID.String())
	if errors.Is(err, storage.ErrNotFound) {
		return storage.Message{}, false, nil
	}
	if err != nil {
		return storage.Message{}, false, fmt.Errorf("get message: %w", err)
	}
	return row, row.RoomID == roomID.String(), nil
}

// messagesFromRows maps rows to Messages, looking each distinct author up once.
func messagesFromRows(ctx context.Context, r storage.Repository, rows []storage.Message) ([]Message, error) {
	authors := map[string]*Author{}
	lookup := func(kind ActorKind, id string) (*Author, error) {
		key := string(kind) + ":" + id
		if a, ok := authors[key]; ok {
			return a, nil
		}
		var a *Author
		switch kind {
		case ActorHuman:
			row, err := r.GetHumanByID(ctx, id)
			if err != nil {
				return nil, fmt.Errorf("get message author: %w", err)
			}
			h, err := humanFromRow(row)
			if err != nil {
				return nil, err
			}
			a = &Author{Kind: kind, ID: h.ID, Name: h.Username}
		default:
			row, err := r.GetAgentByID(ctx, id)
			if err != nil {
				return nil, fmt.Errorf("get message author: %w", err)
			}
			ag, err := agentFromRow(row)
			if err != nil {
				return nil, err
			}
			a = &Author{Kind: kind, ID: ag.ID, Name: ag.DisplayName}
		}
		authors[key] = a
		return a, nil
	}

	out := make([]Message, 0, len(rows))
	for _, row := range rows {
		id, err := ulid.ParseStrict(row.ID)
		if err != nil {
			return nil, fmt.Errorf("stored message has an invalid id %q: %w", row.ID, err)
		}
		roomID, err := ulid.ParseStrict(row.RoomID)
		if err != nil {
			return nil, fmt.Errorf("stored message %s has an invalid room id %q: %w", row.ID, row.RoomID, err)
		}
		m := Message{ID: id, RoomID: roomID, Text: row.Body, CreatedAt: time.UnixMilli(row.CreatedAtMs).UTC()}
		if row.ReplyToID != nil {
			if m.ReplyToID, err = parseOptionalID(*row.ReplyToID); err != nil {
				return nil, err
			}
		}
		switch {
		case row.AuthorHumanID != nil:
			m.Author, err = lookup(ActorHuman, *row.AuthorHumanID)
		case row.AuthorAgentID != nil:
			m.Author, err = lookup(ActorAgent, *row.AuthorAgentID)
		}
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}

// PostMessage adds a message to a room the actor belongs to. The id and time
// are taken inside the transaction, so id order is commit order.
func (s *service) PostMessage(ctx context.Context, actor Actor, roomID ulid.ULID, text string, replyTo ulid.ULID) (Message, error) {
	if actor.Kind != ActorHuman {
		return Message{}, ErrForbidden
	}
	if !validMessageText(text) {
		return Message{}, errMessageText
	}
	var posted Message
	err := s.record(ctx, func(r storage.Repository) error {
		row, err := roomForMember(ctx, r, actor, roomID)
		if err != nil {
			return err
		}
		if row.ArchivedAtMs != nil {
			return ErrRoomArchived
		}
		var replyToID *string
		if !replyTo.IsZero() {
			if _, found, err := messageInRoom(ctx, r, roomID, replyTo); err != nil {
				return err
			} else if !found {
				return errReplyTo
			}
			v := replyTo.String()
			replyToID = &v
		}
		at := s.now()
		id, err := newID(at)
		if err != nil {
			return fmt.Errorf("generate id: %w", err)
		}
		human := actor.ID.String()
		msg := storage.Message{ID: id.String(), RoomID: row.ID, AuthorHumanID: &human, ReplyToID: replyToID, Body: text, CreatedAtMs: at.UnixMilli()}
		if err := r.PutMessage(ctx, msg); err != nil {
			return fmt.Errorf("put message: %w", err)
		}
		if err := r.SetRoomHumanLastRead(ctx, row.ID, human, msg.ID); err != nil {
			return fmt.Errorf("set last read: %w", err)
		}
		if err := putEvent(ctx, r, EventMessagePosted, row.ID, msg.ID, at); err != nil {
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
		return Message{}, wrapOp("post message", err)
	}
	return posted, nil
}

// GetMessage returns a message in a room the actor belongs to.
func (s *service) GetMessage(ctx context.Context, actor Actor, roomID, messageID ulid.ULID) (Message, error) {
	if _, err := roomForMember(ctx, s.repo, actor, roomID); err != nil {
		return Message{}, err
	}
	row, found, err := messageInRoom(ctx, s.repo, roomID, messageID)
	if err != nil {
		return Message{}, err
	}
	if !found {
		return Message{}, fmt.Errorf("message %s: %w", messageID, ErrNotFound)
	}
	msgs, err := messagesFromRows(ctx, s.repo, []storage.Message{row})
	if err != nil {
		return Message{}, err
	}
	return msgs[0], nil
}

// ListMessages returns up to limit messages older than before (zero: the newest), oldest first.
// A limit under 1 means DefaultPageSize; one over MaxPageSize means MaxPageSize.
func (s *service) ListMessages(ctx context.Context, actor Actor, roomID, before ulid.ULID, limit int) ([]Message, error) {
	if _, err := roomForMember(ctx, s.repo, actor, roomID); err != nil {
		return nil, err
	}
	switch {
	case limit < 1:
		limit = DefaultPageSize
	case limit > MaxPageSize:
		limit = MaxPageSize
	}
	beforeID := ""
	if !before.IsZero() {
		beforeID = before.String()
	}
	rows, err := s.repo.ListMessages(ctx, roomID.String(), beforeID, int64(limit))
	if err != nil {
		return nil, fmt.Errorf("list messages: %w", err)
	}
	msgs, err := messagesFromRows(ctx, s.repo, rows)
	if err != nil {
		return nil, err
	}
	slices.Reverse(msgs)
	return msgs, nil
}

// MarkRoomRead moves the actor's read marker forward to messageID. A message
// that is not newer than the marker changes nothing. It writes no event.
func (s *service) MarkRoomRead(ctx context.Context, actor Actor, roomID, messageID ulid.ULID) error {
	err := s.repo.InTx(ctx, func(r storage.Repository) error {
		if _, err := roomForMember(ctx, r, actor, roomID); err != nil {
			return err
		}
		if _, found, err := messageInRoom(ctx, r, roomID, messageID); err != nil {
			return err
		} else if !found {
			return errReadMessageID
		}
		member, err := r.GetRoomHuman(ctx, roomID.String(), actor.ID.String())
		if err != nil {
			return fmt.Errorf("get room member: %w", err)
		}
		if messageID.String() <= member.LastReadMessageID {
			return nil
		}
		if err := r.SetRoomHumanLastRead(ctx, roomID.String(), actor.ID.String(), messageID.String()); err != nil {
			return fmt.Errorf("set last read: %w", err)
		}
		return nil
	})
	if err != nil {
		return wrapOp("mark room read", err)
	}
	return nil
}
