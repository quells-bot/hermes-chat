package bus_test

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/bus"
	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/storage/storagetest"
)

var png = "\x89PNG\r\n\x1a\n" + strings.Repeat("\x00", 32)

func upload(t *testing.T, w world, room bus.Room, name, body string) bus.Attachment {
	t.Helper()
	a, err := w.s.UploadAttachment(ctx, agentActor(w.scout), room.ID, name, "image/png", strings.NewReader(body))
	require.NoError(t, err)
	return a
}

func TestUploadAttachment_ThenPostIt(t *testing.T) {
	w := newWorld(t)
	room := dmRoom(t, w)
	scout := agentActor(w.scout)
	a := upload(t, w, room, "/tmp/out/cat.png", png)
	assert.Equal(t, "cat.png", a.Name, "only the last path element is kept")
	assert.EqualValues(t, len(png), a.Size)
	assert.Equal(t, "image/png", a.ContentType)
	assert.True(t, a.Image())
	assert.True(t, a.MessageID.IsZero())
	assert.Equal(t, fixed, a.CreatedAt)
	_, _, err := w.s.OpenAttachment(ctx, w.kai, room.ID, a.ID)
	assert.ErrorIs(t, err, bus.ErrNotFound, "nobody can fetch an upload that is on no message")

	b := upload(t, w, room, "notes.txt", "hello")
	m, err := w.s.PostMessageWithAttachments(ctx, scout, room.ID, "", ulid.Zero, []ulid.ULID{a.ID, b.ID})
	require.NoError(t, err, "a message with a file needs no text")
	require.Len(t, m.Attachments, 2)
	assert.Equal(t, []ulid.ULID{a.ID, b.ID}, []ulid.ULID{m.Attachments[0].ID, m.Attachments[1].ID})
	assert.Equal(t, m.ID, m.Attachments[0].MessageID)

	got, f, err := w.s.OpenAttachment(ctx, w.kai, room.ID, a.ID)
	require.NoError(t, err)
	body, _ := io.ReadAll(f)
	require.NoError(t, f.Close())
	assert.Equal(t, png, string(body))
	assert.Equal(t, "cat.png", got.Name)

	plain, _ := w.s.PostMessage(ctx, w.kai, room.ID, "thanks", ulid.Zero)
	assert.Equal(t, []bus.Attachment{}, plain.Attachments)
	list, _ := w.s.ListMessages(ctx, w.kai, room.ID, ulid.Zero, 0)
	require.Len(t, list, 2)
	assert.Len(t, list[0].Attachments, 2)
	assert.Empty(t, list[1].Attachments)
}

func TestOpenAttachment_MembersOnly(t *testing.T) {
	w := newWorld(t)
	room := dmRoom(t, w)
	a := upload(t, w, room, "cat.png", png)
	_, err := w.s.PostMessageWithAttachments(ctx, agentActor(w.scout), room.ID, "look", ulid.Zero, []ulid.ULID{a.ID})
	require.NoError(t, err)
	_, _, err = w.s.OpenAttachment(ctx, w.ada, room.ID, a.ID)
	assert.ErrorIs(t, err, bus.ErrNotFound, "ada is not in the room")
	other := convRoom(t, w)
	_, _, err = w.s.OpenAttachment(ctx, w.kai, other.ID, a.ID)
	assert.ErrorIs(t, err, bus.ErrNotFound, "the right file through the wrong room")
	_, _, err = w.s.OpenAttachment(ctx, agentActor(w.scout), room.ID, a.ID)
	assert.ErrorIs(t, err, bus.ErrForbidden)
	_, _, err = w.s.OpenAttachment(ctx, w.kai, room.ID, ulid.Make())
	assert.ErrorIs(t, err, bus.ErrNotFound)
}

func TestUploadAttachment_Refusals(t *testing.T) {
	w := newWorld(t)
	room := channelRoom(t, w)
	scout := agentActor(w.scout)
	try := func(actor bus.Actor, name, body string) error {
		_, err := w.s.UploadAttachment(ctx, actor, room.ID, name, "", strings.NewReader(body))
		return err
	}
	assert.ErrorIs(t, try(w.kai, "a.txt", "x"), bus.ErrForbidden, "human uploads are v1.5")
	stranger, _, _ := w.s.CreateAgent(ctx, "ghost", "Ghost")
	assert.ErrorIs(t, try(agentActor(stranger), "a.txt", "x"), bus.ErrNotFound)
	for _, bad := range []string{"", "  ", "dir/", strings.Repeat("a", 256), "a\nb.txt", "a\x00.txt"} {
		assert.ErrorIs(t, try(scout, bad, "x"), bus.ErrValidation, "%q", bad)
	}
	assert.ErrorIs(t, try(scout, "empty.txt", ""), bus.ErrValidation)
	a, err := w.s.UploadAttachment(ctx, scout, room.ID, "a.bin", "", strings.NewReader("x"))
	require.NoError(t, err)
	assert.Equal(t, "application/octet-stream", a.ContentType)
	assert.False(t, a.Image())

	_, _ = w.s.PauseRoom(ctx, w.kai, room.ID)
	assert.ErrorIs(t, try(scout, "a.txt", "x"), bus.ErrPaused)
	_, err = w.s.PostMessageWithAttachments(ctx, scout, room.ID, "late", ulid.Zero, []ulid.ULID{a.ID})
	assert.ErrorIs(t, err, bus.ErrPaused)
	_, _ = w.s.ResumeRoom(ctx, w.kai, room.ID)
	_, _ = w.s.UpdateRoom(ctx, w.kai, room.ID, bus.RoomUpdate{Archived: ptr(true)})
	assert.ErrorIs(t, try(scout, "a.txt", "x"), bus.ErrRoomArchived)
}

// countingFiles counts what is in the store, to show a refused upload leaves nothing.
type countingFiles struct {
	bus.FileStore
	live map[string]bool
}

func (c countingFiles) Create(ctx context.Context, id string, r io.Reader) (int64, error) {
	n, err := c.FileStore.Create(ctx, id, r)
	if err == nil {
		c.live[id] = true
	}
	return n, err
}
func (c countingFiles) Remove(id string) error { delete(c.live, id); return c.FileStore.Remove(id) }

// Review Focus 3.
func TestUploadAttachment_TooLargeLeavesNothing(t *testing.T) {
	r := storagetest.NewRepo(t)
	files := countingFiles{FileStore: storagetest.NewFiles(t), live: map[string]bool{}}
	var s bus.Service = bus.NewService(r, func() time.Time { return fixed }, bus.WithFiles(files))
	h, _ := s.CreateHuman(ctx, "kai", pw)
	scout, _, _ := s.CreateAgent(ctx, "scout", "Scout")
	room, _, err := s.OpenDM(ctx, bus.Actor{Kind: bus.ActorHuman, ID: h.ID}, scout.ID)
	require.NoError(t, err)
	actor := agentActor(scout)

	exactly := io.LimitReader(zeros{}, bus.MaxAttachmentBytes)
	a, err := s.UploadAttachment(ctx, actor, room.ID, "max.bin", "", exactly)
	require.NoError(t, err, "exactly the cap is allowed")
	assert.EqualValues(t, bus.MaxAttachmentBytes, a.Size)
	_, err = s.UploadAttachment(ctx, actor, room.ID, "big.bin", "", io.LimitReader(zeros{}, bus.MaxAttachmentBytes+1))
	assert.ErrorIs(t, err, bus.ErrTooLarge)
	_, err = s.UploadAttachment(ctx, actor, room.ID, "empty.bin", "", strings.NewReader(""))
	assert.ErrorIs(t, err, bus.ErrValidation)
	assert.Len(t, files.live, 1, "only the accepted upload is on disk")
}

type zeros struct{}

func (zeros) Read(p []byte) (int, error) { clear(p); return len(p), nil }

func TestPostMessageWithAttachments_OnlyYourOwnFreshUploadsHere(t *testing.T) {
	w := newWorld(t)
	room, other := channelRoom(t, w), convRoom(t, w)
	scout, atlas := agentActor(w.scout), agentActor(w.atlas)
	mine := upload(t, w, room, "a.png", png)
	elsewhere := upload(t, w, other, "b.png", png)
	theirs, err := w.s.UploadAttachment(ctx, atlas, room.ID, "c.png", "image/png", strings.NewReader(png))
	require.NoError(t, err)
	post := func(ids ...ulid.ULID) error {
		_, err := w.s.PostMessageWithAttachments(ctx, scout, room.ID, "look", ulid.Zero, ids)
		return err
	}
	assert.ErrorIs(t, post(elsewhere.ID), bus.ErrValidation, "uploaded to another room")
	assert.ErrorIs(t, post(theirs.ID), bus.ErrValidation, "atlas's upload")
	assert.ErrorIs(t, post(ulid.Make()), bus.ErrValidation)
	assert.ErrorIs(t, post(mine.ID, mine.ID), bus.ErrValidation, "a repeat")
	eleven := make([]ulid.ULID, 11)
	for i := range eleven {
		eleven[i] = upload(t, w, room, fmt.Sprintf("%d.png", i), png).ID
	}
	assert.ErrorIs(t, post(eleven...), bus.ErrValidation)
	_, err = w.s.PostMessageWithAttachments(ctx, scout, room.ID, "  ", ulid.Zero, nil)
	assert.ErrorIs(t, err, bus.ErrValidation, "no text and no file")

	require.NoError(t, post(mine.ID))
	assert.ErrorIs(t, post(mine.ID), bus.ErrValidation, "already posted")
	list, _ := w.s.ListMessages(ctx, w.kai, room.ID, ulid.Zero, 0)
	assert.Len(t, list, 1, "a refused post leaves no message behind")
	require.NoError(t, post(eleven[:10]...))
}