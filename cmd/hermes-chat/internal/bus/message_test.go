package bus_test

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/bus"
	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/storage"
	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/storage/storagetest"
)

// shared is a room with topic "Shared" holding kai, ada, and scout: kai creates it, then invites ada (two events).
func shared(t *testing.T, w world) bus.Room {
	t.Helper()
	room, err := w.s.CreateRoom(ctx, w.kai, "Shared", ids(w.scout))
	require.NoError(t, err)
	room, err = w.s.InviteHuman(ctx, w.kai, room.ID, w.ada.ID)
	require.NoError(t, err)
	return room
}

func TestPostMessage(t *testing.T) {
	w := newWorld(t)
	room := shared(t, w)
	m, err := w.s.PostMessage(ctx, w.kai, room.ID, "  hello *world*\n", ulid.Zero)
	require.NoError(t, err)
	assert.Equal(t, room.ID, m.RoomID)
	assert.Equal(t, "  hello *world*\n", m.Text) // stored as sent
	assert.Equal(t, &bus.Author{Kind: bus.ActorHuman, ID: w.kai.ID, Name: "kai"}, m.Author)
	assert.True(t, m.ReplyToID.IsZero())
	assert.Equal(t, fixed, m.CreatedAt)
	assert.Equal(t, uint64(fixed.UnixMilli()), m.ID.Time())
	got, err := w.s.GetMessage(ctx, w.ada, room.ID, m.ID)
	require.NoError(t, err)
	assert.Equal(t, m, got)

	mine, _ := w.s.GetRoom(ctx, w.kai, room.ID)
	assert.Equal(t, m.ID, mine.LastMessageID)
	assert.Equal(t, m.ID, mine.LastReadMessageID) // posting marks it read
	hers, _ := w.s.GetRoom(ctx, w.ada, room.ID)
	assert.Equal(t, m.ID, hers.LastMessageID)
	assert.True(t, hers.LastReadMessageID.IsZero())
	k := w.kinds(t, w.kai)
	assert.Equal(t, "message.posted", k[len(k)-1])
}

func TestPostMessage_Validation(t *testing.T) {
	w := newWorld(t)
	room := shared(t, w)
	before := len(w.kinds(t, w.kai))
	for _, bad := range []string{"", " \n\t ", "nul\x00here", strings.Repeat("a", bus.MaxMessageBytes+1)} {
		_, err := w.s.PostMessage(ctx, w.kai, room.ID, bad, ulid.Zero)
		var ve bus.ValidationError
		require.ErrorAs(t, err, &ve, "%.10q", bad)
		assert.Equal(t, "message text must be 1 to 262144 bytes, not blank, with no NUL character", ve.Message)
	}
	_, err := w.s.PostMessage(ctx, w.kai, room.ID, strings.Repeat("a", bus.MaxMessageBytes), ulid.Zero)
	assert.NoError(t, err)
	assert.Len(t, w.kinds(t, w.kai), before+1)
}

func TestPostMessage_ReplyTo(t *testing.T) {
	w := newWorld(t)
	room := shared(t, w)
	other, _ := w.s.CreateRoom(ctx, w.kai, "Other", ids(w.scout))
	first, _ := w.s.PostMessage(ctx, w.kai, room.ID, "first", ulid.Zero)
	elsewhere, _ := w.s.PostMessage(ctx, w.kai, other.ID, "elsewhere", ulid.Zero)
	reply, err := w.s.PostMessage(ctx, w.ada, room.ID, "reply", first.ID)
	require.NoError(t, err)
	assert.Equal(t, first.ID, reply.ReplyToID)
	for _, bad := range []ulid.ULID{elsewhere.ID, ulid.Make()} { // Review Focus 3: another room's message is not a valid target
		_, err := w.s.PostMessage(ctx, w.kai, room.ID, "reply", bad)
		var ve bus.ValidationError
		require.ErrorAs(t, err, &ve)
		assert.Equal(t, "reply_to must be a message in this room", ve.Message)
	}
}

func TestPostMessage_Refused(t *testing.T) {
	w := newWorld(t)
	private, _ := w.s.CreateRoom(ctx, w.kai, "Private", ids(w.scout))
	_, err := w.s.PostMessage(ctx, w.ada, private.ID, "hi", ulid.Zero)
	assert.ErrorIs(t, err, bus.ErrNotFound)
	_, err = w.s.PostMessage(ctx, w.kai, ulid.Make(), "hi", ulid.Zero)
	assert.ErrorIs(t, err, bus.ErrNotFound)
	_, err = w.s.PostMessage(ctx, bus.Actor{Kind: bus.ActorAgent, ID: w.atlas.ID}, private.ID, "hi", ulid.Zero)
	assert.ErrorIs(t, err, bus.ErrNotFound) // an agent outside the room is refused like a non-member human
	_, _ = w.s.UpdateRoom(ctx, w.kai, private.ID, bus.RoomUpdate{Archived: ptr(true)})
	_, err = w.s.PostMessage(ctx, w.kai, private.ID, "hi", ulid.Zero)
	assert.ErrorIs(t, err, bus.ErrRoomArchived)
	got, err := w.s.ListMessages(ctx, w.kai, private.ID, ulid.Zero, 0) // readable while archived
	require.NoError(t, err)
	assert.NotNil(t, got)
	assert.Empty(t, got)
}

func TestPostMessage_RollsBackWhenTheEventCannotBeWritten(t *testing.T) {
	w := newWorld(t)
	room := shared(t, w)
	broken := bus.NewService(failingEvents{w.r}, func() time.Time { return fixed })
	_, err := broken.PostMessage(ctx, w.kai, room.ID, "lost", ulid.Zero)
	require.ErrorIs(t, err, errDisk)
	got, _ := w.s.ListMessages(ctx, w.kai, room.ID, ulid.Zero, 0)
	assert.Empty(t, got)
	r, _ := w.s.GetRoom(ctx, w.kai, room.ID)
	assert.True(t, r.LastReadMessageID.IsZero())
}

func TestListMessages_Pages(t *testing.T) {
	w := newWorld(t)
	room := shared(t, w)
	var all []bus.Message
	for i := range 205 {
		m, err := w.s.PostMessage(ctx, w.kai, room.ID, fmt.Sprint(i), ulid.Zero)
		require.NoError(t, err)
		all = append(all, m)
	}
	page, err := w.s.ListMessages(ctx, w.ada, room.ID, ulid.Zero, 3)
	require.NoError(t, err)
	assert.Equal(t, all[202:], page) // the newest three, oldest first
	page, _ = w.s.ListMessages(ctx, w.ada, room.ID, all[202].ID, 2)
	assert.Equal(t, all[200:202], page)
	page, _ = w.s.ListMessages(ctx, w.ada, room.ID, all[0].ID, 10)
	assert.Empty(t, page)
	page, _ = w.s.ListMessages(ctx, w.ada, room.ID, ulid.Zero, 0)
	assert.Equal(t, all[155:], page) // default 50
	page, _ = w.s.ListMessages(ctx, w.ada, room.ID, ulid.Zero, -1)
	assert.Len(t, page, 50)
	page, _ = w.s.ListMessages(ctx, w.ada, room.ID, ulid.Zero, 1000)
	assert.Equal(t, all[5:], page) // capped at 200
}

// Review Focus 3.
func TestMessages_AreReachedOnlyThroughTheirOwnRoom(t *testing.T) {
	w := newWorld(t)
	private, _ := w.s.CreateRoom(ctx, w.kai, "Private", ids(w.scout))
	room := shared(t, w)
	secret, _ := w.s.PostMessage(ctx, w.kai, private.ID, "secret", ulid.Zero)
	_, err := w.s.GetMessage(ctx, w.ada, private.ID, secret.ID)
	assert.ErrorIs(t, err, bus.ErrNotFound) // not her room
	_, err = w.s.GetMessage(ctx, w.ada, room.ID, secret.ID)
	assert.ErrorIs(t, err, bus.ErrNotFound) // her room, not its message
	_, err = w.s.GetMessage(ctx, w.kai, room.ID, secret.ID)
	assert.ErrorIs(t, err, bus.ErrNotFound) // even for a member of both
	_, err = w.s.GetMessage(ctx, w.kai, private.ID, ulid.Make())
	assert.ErrorIs(t, err, bus.ErrNotFound)
	_, err = w.s.ListMessages(ctx, w.ada, private.ID, ulid.Zero, 0)
	assert.ErrorIs(t, err, bus.ErrNotFound)
	assert.ErrorIs(t, w.s.MarkRoomRead(ctx, w.ada, private.ID, secret.ID), bus.ErrNotFound)
	assert.ErrorIs(t, w.s.MarkRoomRead(ctx, w.ada, room.ID, secret.ID), bus.ErrValidation)
}

func TestMarkRoomRead(t *testing.T) {
	w := newWorld(t)
	room := shared(t, w)
	m1, _ := w.s.PostMessage(ctx, w.kai, room.ID, "one", ulid.Zero)
	m2, _ := w.s.PostMessage(ctx, w.kai, room.ID, "two", ulid.Zero)
	before := len(w.kinds(t, w.ada))
	read := func() ulid.ULID {
		r, err := w.s.GetRoom(ctx, w.ada, room.ID)
		require.NoError(t, err)
		return r.LastReadMessageID
	}
	require.NoError(t, w.s.MarkRoomRead(ctx, w.ada, room.ID, m2.ID))
	assert.Equal(t, m2.ID, read())
	require.NoError(t, w.s.MarkRoomRead(ctx, w.ada, room.ID, m1.ID))
	assert.Equal(t, m2.ID, read(), "the marker never moves back")
	assert.Len(t, w.kinds(t, w.ada), before, "marking read writes no event")
	_, _ = w.s.UpdateRoom(ctx, w.kai, room.ID, bus.RoomUpdate{Archived: ptr(true)})
	assert.NoError(t, w.s.MarkRoomRead(ctx, w.ada, room.ID, m2.ID)) // allowed while archived
}

// Review Focus 4, and the agent author column stage 4 will write.
func TestMessage_AuthorKinds(t *testing.T) {
	w := newWorld(t)
	room := shared(t, w)
	adas, _ := w.s.PostMessage(ctx, w.ada, room.ID, "from ada", ulid.Zero)
	agentID := w.scout.ID.String()
	at := fixed.Add(time.Second)
	id, _ := ulid.New(ulid.Timestamp(at), ulid.DefaultEntropy())
	require.NoError(t, w.r.PutMessage(ctx, storage.Message{ID: id.String(), RoomID: room.ID.String(), AuthorAgentID: &agentID, Body: "from scout", CreatedAtMs: at.UnixMilli()}))
	require.NoError(t, w.s.RemoveHuman(ctx, "ada"))

	got, err := w.s.ListMessages(ctx, w.kai, room.ID, ulid.Zero, 0)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Nil(t, got[0].Author)
	assert.Equal(t, "from ada", got[0].Text) // the message outlives its author
	assert.Equal(t, &bus.Author{Kind: bus.ActorAgent, ID: w.scout.ID, Name: "Scout"}, got[1].Author)
	one, err := w.s.GetMessage(ctx, w.kai, room.ID, adas.ID)
	require.NoError(t, err)
	assert.Nil(t, one.Author)
	r, err := w.s.GetRoom(ctx, w.kai, room.ID)
	require.NoError(t, err)
	require.Len(t, r.Humans, 1)
	_, err = w.s.ListRooms(ctx, w.ada) // a removed human simply has no rooms
	assert.NoError(t, err)
}

// The ID is minted under the write lock, so ID order is commit order.
func TestPostMessage_IDsFollowCommitOrder(t *testing.T) {
	r := storagetest.NewRepo(t)
	s := bus.NewService(r, time.Now) // the real clock
	h, err := s.CreateHuman(ctx, "kai", pw)
	require.NoError(t, err)
	kai := bus.Actor{Kind: bus.ActorHuman, ID: h.ID}
	scout, _, _ := s.CreateAgent(ctx, "scout", "Scout")
	room, err := s.CreateRoom(ctx, kai, "Busy", []ulid.ULID{scout.ID})
	require.NoError(t, err)
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 25 {
				_, err := s.PostMessage(ctx, kai, room.ID, fmt.Sprint(g, i), ulid.Zero)
				assert.NoError(t, err)
			}
		}()
	}
	wg.Wait()
	events, err := r.ListEventsForHuman(ctx, kai.ID.String(), 1, 1000)
	require.NoError(t, err)
	require.Len(t, events, 200) // after the room's own event
	for i := 1; i < len(events); i++ {
		assert.Greater(t, events[i].SubjectID, events[i-1].SubjectID, "event %d", events[i].ID)
	}
}

func TestPostMessageOnce_RepeatReturnsTheFirst(t *testing.T) {
	w := newWorld(t)
	room := dmRoom(t, w)
	first, created, err := w.s.PostMessageOnce(ctx, w.kai, room.ID, "k1", "hello", ulid.Zero)
	require.NoError(t, err)
	assert.True(t, created)
	assert.Equal(t, "k1", first.ClientID)
	before := w.kinds(t, w.kai)

	again, created, err := w.s.PostMessageOnce(ctx, w.kai, room.ID, "k1", "something else", ulid.Zero)
	require.NoError(t, err)
	assert.False(t, created)
	assert.Equal(t, first.ID, again.ID)
	assert.Equal(t, "hello", again.Text) // the first request wins
	assert.Equal(t, "k1", again.ClientID)
	assert.Equal(t, before, w.kinds(t, w.kai)) // no second event
	msgs, err := w.s.ListMessages(ctx, w.kai, room.ID, ulid.Zero, 0)
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	assert.Len(t, msgs[0].Deliveries, 1)
}

func TestPostMessageOnce_NoKeyPostsEveryTime(t *testing.T) {
	w := newWorld(t)
	room := dmRoom(t, w)
	a, created, err := w.s.PostMessageOnce(ctx, w.kai, room.ID, "", "hello", ulid.Zero)
	require.NoError(t, err)
	assert.True(t, created)
	b, created, err := w.s.PostMessageOnce(ctx, w.kai, room.ID, "", "hello", ulid.Zero)
	require.NoError(t, err)
	assert.True(t, created)
	assert.NotEqual(t, a.ID, b.ID)
	assert.Equal(t, "", a.ClientID)
	assert.Equal(t, "", b.ClientID)
}

func TestPostMessageOnce_KeyIsPerHumanAndPerRoom(t *testing.T) {
	w := newWorld(t)
	room := shared(t, w)
	other, err := w.s.CreateRoom(ctx, w.kai, "Other", ids(w.scout))
	require.NoError(t, err)
	seen := map[ulid.ULID]bool{}
	for _, post := range []struct {
		who  bus.Actor
		room ulid.ULID
	}{{w.kai, room.ID}, {w.ada, room.ID}, {w.kai, other.ID}} {
		m, created, err := w.s.PostMessageOnce(ctx, post.who, post.room, "k1", "hello", ulid.Zero)
		require.NoError(t, err)
		assert.True(t, created)
		seen[m.ID] = true
	}
	assert.Len(t, seen, 3)
}

func TestPostMessageOnce_RepeatSurvivesArchiving(t *testing.T) { // Review Focus 1
	w := newWorld(t)
	room := shared(t, w)
	first, _, err := w.s.PostMessageOnce(ctx, w.kai, room.ID, "k1", "hello", ulid.Zero)
	require.NoError(t, err)
	archived := true
	_, err = w.s.UpdateRoom(ctx, w.kai, room.ID, bus.RoomUpdate{Archived: &archived})
	require.NoError(t, err)

	again, created, err := w.s.PostMessageOnce(ctx, w.kai, room.ID, "k1", "hello", ulid.Zero)
	require.NoError(t, err)
	assert.False(t, created)
	assert.Equal(t, first.ID, again.ID)
	_, _, err = w.s.PostMessageOnce(ctx, w.kai, room.ID, "k2", "hello", ulid.Zero)
	assert.ErrorIs(t, err, bus.ErrRoomArchived)
}

func TestPostMessageOnce_RepeatByANonMemberIsNotFound(t *testing.T) {
	w := newWorld(t)
	room := dmRoom(t, w) // kai's; ada is not in it
	_, _, err := w.s.PostMessageOnce(ctx, w.kai, room.ID, "k1", "hello", ulid.Zero)
	require.NoError(t, err)
	_, _, err = w.s.PostMessageOnce(ctx, w.ada, room.ID, "k1", "hello", ulid.Zero)
	assert.ErrorIs(t, err, bus.ErrNotFound)
}

func TestPostMessageOnce_Validation(t *testing.T) { // Review Focus 4
	w := newWorld(t)
	room := dmRoom(t, w)
	for _, bad := range []string{strings.Repeat("a", bus.MaxClientIDBytes+1), "a b", "a/b", "é", " k1"} {
		_, _, err := w.s.PostMessageOnce(ctx, w.kai, room.ID, bad, "hello", ulid.Zero)
		var ve bus.ValidationError
		require.ErrorAs(t, err, &ve, "%q", bad)
		assert.Equal(t, `client_id must be at most 64 characters: letters, digits, "-" or "_"`, ve.Message)
	}
	msgs, err := w.s.ListMessages(ctx, w.kai, room.ID, ulid.Zero, 0)
	require.NoError(t, err)
	assert.Empty(t, msgs)
	for _, good := range []string{strings.Repeat("a", bus.MaxClientIDBytes), "01JA-x_9"} {
		_, created, err := w.s.PostMessageOnce(ctx, w.kai, room.ID, good, "hello", ulid.Zero)
		require.NoError(t, err, "%q", good)
		assert.True(t, created)
	}
}

func TestPostMessageOnce_AgentIsForbidden(t *testing.T) {
	w := newWorld(t)
	room := dmRoom(t, w)
	_, _, err := w.s.PostMessageOnce(ctx, agentActor(w.scout), room.ID, "k1", "hello", ulid.Zero)
	assert.ErrorIs(t, err, bus.ErrForbidden)
}

func TestPostMessageOnce_RepeatDoesNotResetTheTurnCount(t *testing.T) {
	w := newWorld(t)
	room, _, _ := connectedChannel(t, w)
	_, _, err := w.s.PostMessageOnce(ctx, w.kai, room.ID, "k1", "carry on", ulid.Zero) // wakes nobody; the count starts at 0
	require.NoError(t, err)
	for i := 0; i < bus.DefaultTurnCap; i++ {
		require.Equal(t, bus.DeliveryDelivered, ping(t, w, room, w.scout, w.atlas).Deliveries[0].State, "turn %d", i+1)
	}
	_, created, err := w.s.PostMessageOnce(ctx, w.kai, room.ID, "k1", "carry on", ulid.Zero)
	require.NoError(t, err)
	require.False(t, created)

	ping(t, w, room, w.scout, w.atlas) // one over the cap, had the repeat not reset the count
	got, err := w.s.GetRoom(ctx, w.kai, room.ID)
	require.NoError(t, err)
	assert.Equal(t, bus.PauseTurnCap, got.PauseReason)
}
