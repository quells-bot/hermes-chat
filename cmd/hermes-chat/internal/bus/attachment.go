package bus

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/oklog/ulid/v2"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/storage"
)

const (
	MaxAttachmentBytes       = 25 << 20
	MaxAttachmentsPerMessage = 10
)

// Attachment is a stored file's metadata. The file itself is in the FileStore.
type Attachment struct {
	ID, RoomID  ulid.ULID
	MessageID   ulid.ULID // zero until a message carries it
	Name        string
	ContentType string
	Size        int64
	CreatedAt   time.Time
}

// Image reports whether the content type is one a browser may show inline:
// image/png, image/jpeg, image/gif, or image/webp.
func (a Attachment) Image() bool {
	switch a.ContentType {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return true
	}
	return false
}

var (
	errFileName     = ValidationError{Message: "file name must be 1-255 characters with no control characters"}
	errFileEmpty    = ValidationError{Message: "file is empty"}
	errAttachmentIDs = ValidationError{Message: "attachment_ids must be at most 10 of your own uploads in this room, not yet posted, each at most once"}
)

// cleanAttachmentName keeps the name's last path element, split on "/" and
// "\", trimmed: a client's path can never reach the storage directory.
func cleanAttachmentName(name string) (string, error) {
	keep := name
	for i, r := range name {
		if r == '/' || r == '\\' {
			keep = name[i+1:]
		}
	}
	base := strings.TrimSpace(keep)
	n := utf8.RuneCountInString(base)
	if n < 1 || n > 255 || strings.IndexFunc(base, unicode.IsControl) >= 0 {
		return "", errFileName
	}
	return base, nil
}

// attachmentFromStorage maps a stored row to the bus's Attachment.
func attachmentFromStorage(row storage.Attachment) (Attachment, error) {
	id, err := ulid.ParseStrict(row.ID)
	if err != nil {
		return Attachment{}, fmt.Errorf("stored attachment has an invalid id %q: %w", row.ID, err)
	}
	roomID, err := ulid.ParseStrict(row.RoomID)
	if err != nil {
		return Attachment{}, fmt.Errorf("stored attachment %s has an invalid room id %q: %w", row.ID, row.RoomID, err)
	}
	a := Attachment{ID: id, RoomID: roomID, Name: row.Name, ContentType: row.ContentType, Size: row.Size, CreatedAt: time.UnixMilli(row.CreatedAtMs).UTC()}
	if row.MessageID != nil {
		if a.MessageID, err = parseOptionalID(*row.MessageID); err != nil {
			return Attachment{}, err
		}
	}
	return a, nil
}

// loadAttachments maps the rows' message ids to the attachments riding those
// messages, one range query over the rows, as loadDeliveries does.
func loadAttachments(ctx context.Context, r storage.Repository, rows []storage.Message) (map[string][]Attachment, error) {
	if len(rows) == 0 {
		return map[string][]Attachment{}, nil
	}
	first, last := rows[0].ID, rows[0].ID
	for _, row := range rows {
		if row.ID < first {
			first = row.ID
		}
		if row.ID > last {
			last = row.ID
		}
	}
	atts, err := r.ListAttachmentsInRange(ctx, rows[0].RoomID, first, last)
	if err != nil {
		return nil, fmt.Errorf("list attachments: %w", err)
	}
	out := map[string][]Attachment{}
	for _, a := range atts {
		att, err := attachmentFromStorage(a)
		if err != nil {
			return nil, err
		}
		if a.MessageID != nil {
			out[*a.MessageID] = append(out[*a.MessageID], att)
		}
	}
	return out, nil
}

// UploadAttachment stores one file an agent uploads to a room. The body is
// read before any transaction, so a room that is closed, archived, or gone
// refuses without reading a byte, and a refused upload leaves nothing in the
// store.
func (s *service) UploadAttachment(ctx context.Context, actor Actor, roomID ulid.ULID, name, contentType string, body io.Reader) (Attachment, error) {
	if actor.Kind != ActorAgent {
		return Attachment{}, ErrForbidden
	}
	clean, err := cleanAttachmentName(name)
	if err != nil {
		return Attachment{}, err
	}
	ct := contentType
	if ct == "" {
		ct = "application/octet-stream"
	}
	// The gate comes before any byte of the body is read.
	row, _, err := roomForPoster(ctx, s.repo, actor, roomID)
	if err != nil {
		return Attachment{}, err
	}
	if row.ArchivedAtMs != nil {
		return Attachment{}, ErrRoomArchived
	}
	if closed, err := paused(ctx, s.repo, row); err != nil {
		return Attachment{}, err
	} else if closed {
		return Attachment{}, ErrPaused
	}
	if s.files == nil {
		return Attachment{}, errors.New("no file store configured")
	}
	at := s.now()
	id, err := newID(at)
	if err != nil {
		return Attachment{}, fmt.Errorf("generate id: %w", err)
	}
	size, err := s.files.Create(ctx, id.String(), io.LimitReader(body, MaxAttachmentBytes+1))
	if err != nil {
		return Attachment{}, fmt.Errorf("store attachment: %w", err)
	}
	if size > MaxAttachmentBytes {
		_ = s.files.Remove(id.String())
		return Attachment{}, ErrTooLarge
	}
	if size == 0 {
		_ = s.files.Remove(id.String())
		return Attachment{}, errFileEmpty
	}
	err = s.repo.InTx(ctx, func(r storage.Repository) error {
		row, _, err := roomForPoster(ctx, r, actor, roomID)
		if err != nil {
			return err
		}
		if row.ArchivedAtMs != nil {
			return ErrRoomArchived
		}
		if closed, err := paused(ctx, r, row); err != nil {
			return err
		} else if closed {
			return ErrPaused
		}
		agent := actor.ID.String()
		if err := r.PutAttachment(ctx, storage.Attachment{
			ID: id.String(), RoomID: row.ID, UploaderAgentID: &agent,
			Name: clean, ContentType: ct, Size: size, CreatedAtMs: at.UnixMilli(),
		}); err != nil {
			return fmt.Errorf("put attachment: %w", err)
		}
		return nil
	})
	if err != nil {
		_ = s.files.Remove(id.String())
		return Attachment{}, wrapOp("upload attachment", err)
	}
	return Attachment{ID: id, RoomID: roomID, Name: clean, ContentType: ct, Size: size, CreatedAt: at}, nil
}

// OpenAttachment opens an attachment's file for a human member of its room.
// An upload that is on no message yet cannot be fetched. The caller closes
// the returned file.
func (s *service) OpenAttachment(ctx context.Context, actor Actor, roomID, attachmentID ulid.ULID) (Attachment, io.ReadSeekCloser, error) {
	if _, err := roomForMember(ctx, s.repo, actor, roomID); err != nil {
		return Attachment{}, nil, err
	}
	row, err := s.repo.GetAttachment(ctx, attachmentID.String())
	if errors.Is(err, storage.ErrNotFound) {
		return Attachment{}, nil, fmt.Errorf("attachment %s: %w", attachmentID, ErrNotFound)
	}
	if err != nil {
		return Attachment{}, nil, fmt.Errorf("get attachment: %w", err)
	}
	if row.RoomID != roomID.String() || row.MessageID == nil {
		return Attachment{}, nil, fmt.Errorf("attachment %s: %w", attachmentID, ErrNotFound)
	}
	if s.files == nil {
		return Attachment{}, nil, errors.New("no file store configured")
	}
	f, err := s.files.Open(attachmentID.String())
	if errors.Is(err, fs.ErrNotExist) {
		return Attachment{}, nil, fmt.Errorf("attachment %s: %w", attachmentID, ErrNotFound)
	}
	if err != nil {
		return Attachment{}, nil, fmt.Errorf("open attachment file: %w", err)
	}
	a, err := attachmentFromStorage(row)
	if err != nil {
		return Attachment{}, nil, err
	}
	return a, f, nil
}