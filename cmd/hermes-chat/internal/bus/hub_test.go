package bus_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/bus"
	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/storage"
	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/storage/storagetest"
)

// drain polls until the log is drained and returns everything read.
func drain(t *testing.T, sub *bus.Subscription) (out []bus.Event) {
	t.Helper()
	for {
		evs, err := sub.Poll(ctx)
		require.NoError(t, err)
		if len(evs) == 0 {
			return
		}
		out = append(out, evs...)
	}
}

// rung reports whether the subscription is woken within a second, and whether its channel is still open.
func rung(sub *bus.Subscription) (woken, open bool) {
	select {
	case _, ok := <-sub.Wake():
		return true, ok
	case <-time.After(time.Second):
		return false, true
	}
}

func quiet(sub *bus.Subscription) bool {
	select {
	case <-sub.Wake():
		return false
	default:
		return true
	}
}

func TestSubscribe_FromNowSeesOnlyWhatComesAfter(t *testing.T) {
	w := newWorld(t)
	room := shared(t, w)
	_, _ = w.s.PostMessage(ctx, w.kai, room.ID, "before", ulid.Zero)
	sub, err := w.s.Subscribe(ctx, w.kai, bus.FromNow)
	require.NoError(t, err)
	defer sub.Close()
	head := sub.Cursor()
	assert.EqualValues(t, 3, head) // create, invite, post
	assert.Empty(t, drain(t, sub))
	assert.True(t, quiet(sub))

	m, _ := w.s.PostMessage(ctx, w.ada, room.ID, "after", ulid.Zero)
	woken, open := rung(sub)
	assert.True(t, woken)
	assert.True(t, open)
	got := drain(t, sub)
	require.Len(t, got, 1)
	assert.Equal(t, bus.Event{ID: head + 1, Kind: bus.EventMessagePosted, RoomID: room.ID, At: fixed, Message: &m}, got[0])
	assert.Equal(t, head+1, sub.Cursor())
}

func TestSubscribe_ReplaysFromACursor(t *testing.T) {
	w := newWorld(t)
	room := shared(t, w)
	m, _ := w.s.PostMessage(ctx, w.kai, room.ID, "one", ulid.Zero)
	sub, err := w.s.Subscribe(ctx, w.kai, 0)
	require.NoError(t, err)
	defer sub.Close()
	assert.Zero(t, sub.Cursor())
	got := drain(t, sub)
	require.Len(t, got, 3)
	now, _ := w.s.GetRoom(ctx, w.kai, room.ID)
	for i, e := range got[:2] { // both room.changed events carry the room as it is now, not as it was
		assert.EqualValues(t, i+1, e.ID)
		assert.Equal(t, bus.EventRoomChanged, e.Kind)
		assert.Equal(t, &now, e.Room)
		assert.Nil(t, e.Message)
	}
	assert.Equal(t, &m, got[2].Message)
	assert.Nil(t, got[2].Room)

	later, _ := w.s.Subscribe(ctx, w.kai, 2)
	defer later.Close()
	got = drain(t, later)
	require.Len(t, got, 1)
	assert.EqualValues(t, 3, got[0].ID)
}

// Review Focus 1.
func TestSubscribe_Cursors(t *testing.T) {
	w := newWorld(t)
	room := shared(t, w) // the log holds 2 events
	ahead, err := w.s.Subscribe(ctx, w.kai, 500)
	require.NoError(t, err)
	defer ahead.Close()
	assert.EqualValues(t, 2, ahead.Cursor(), "a cursor past the end is pulled back to the end")
	m, _ := w.s.PostMessage(ctx, w.kai, room.ID, "next", ulid.Zero)
	got := drain(t, ahead)
	require.Len(t, got, 1)
	assert.Equal(t, m.ID, got[0].Message.ID) // and so does not miss event 3

	_, err = w.s.Subscribe(ctx, w.kai, -2)
	var ve bus.ValidationError
	require.ErrorAs(t, err, &ve)
	assert.Equal(t, "cursor must not be negative", ve.Message)
	_, err = w.s.Subscribe(ctx, bus.Actor{Kind: bus.ActorAgent, ID: w.scout.ID}, bus.FromNow)
	assert.ErrorIs(t, err, bus.ErrForbidden)
}

// Review Focus 3.
func TestSubscribe_OnlyTheSubscribersRooms(t *testing.T) {
	w := newWorld(t)
	hers, _ := w.s.Subscribe(ctx, w.ada, bus.FromNow)
	defer hers.Close()
	private, _ := w.s.CreateRoom(ctx, w.kai, "Private", ids(w.scout))
	secret, _ := w.s.PostMessage(ctx, w.kai, private.ID, "secret", ulid.Zero)
	woken, _ := rung(hers)
	assert.True(t, woken, "everyone is rung") // but
	assert.Empty(t, drain(t, hers), "only members are told anything")

	_, err := w.s.InviteHuman(ctx, w.kai, private.ID, w.ada.ID)
	require.NoError(t, err)
	got := drain(t, hers)
	require.Len(t, got, 3) // joining opens the room's log from her cursor on
	assert.Equal(t, bus.EventRoomChanged, got[0].Kind)
	assert.Equal(t, secret.ID, got[1].Message.ID)
	assert.Equal(t, bus.EventRoomChanged, got[2].Kind)
	require.Len(t, got[2].Room.Humans, 2)
	assert.True(t, got[2].Room.LastReadMessageID.IsZero(), "the room is shown as she sees it")
}

func TestEveryChangeRingsOnceItHasCommitted(t *testing.T) {
	w := newWorld(t)
	room := shared(t, w)
	first, _ := w.s.PostMessage(ctx, w.kai, room.ID, "first", ulid.Zero)
	sub, _ := w.s.Subscribe(ctx, w.kai, bus.FromNow)
	defer sub.Close()
	changes := []struct {
		name string
		kind bus.EventKind
		do   func() error
	}{
		{"create", bus.EventRoomChanged, func() error {
			_, err := w.s.CreateRoom(ctx, w.kai, "New", ids(w.scout))
			return err
		}},
		{"dm", bus.EventRoomChanged, func() error {
			_, _, err := w.s.OpenDM(ctx, w.kai, w.scout.ID)
			return err
		}},
		{"update", bus.EventRoomChanged, func() error {
			_, err := w.s.UpdateRoom(ctx, w.kai, room.ID, bus.RoomUpdate{Topic: ptr("Renamed")})
			return err
		}},
		{"add", bus.EventRoomChanged, func() error {
			_, err := w.s.AddRoomAgent(ctx, w.kai, room.ID, w.atlas.ID)
			return err
		}},
		{"remove", bus.EventRoomChanged, func() error {
			_, err := w.s.RemoveRoomAgent(ctx, w.kai, room.ID, w.atlas.ID)
			return err
		}},
		{"post", bus.EventMessagePosted, func() error {
			_, err := w.s.PostMessage(ctx, w.kai, room.ID, "second", first.ID)
			return err
		}},
	}
	for _, c := range changes {
		require.True(t, quiet(sub), c.name)
		require.NoError(t, c.do(), c.name)
		woken, _ := rung(sub)
		assert.True(t, woken, c.name)
		got := drain(t, sub)
		require.Len(t, got, 1, c.name)
		assert.Equal(t, c.kind, got[0].Kind, c.name)
	}
	_, err := w.s.PostMessage(ctx, w.kai, room.ID, "", ulid.Zero)
	require.Error(t, err)
	assert.True(t, quiet(sub), "a change that did not commit rings nobody")
	invited, _ := w.s.Subscribe(ctx, w.ada, bus.FromNow) // ada is already in the room
	defer invited.Close()
	private, _ := w.s.CreateRoom(ctx, w.kai, "P", ids(w.scout))
	rung(invited)
	drain(t, invited)
	_, _ = w.s.InviteHuman(ctx, w.kai, private.ID, w.ada.ID)
	woken, _ := rung(invited)
	assert.True(t, woken, "invite")
}

// Review Focus 5.
func TestASubscriberThatNeverPollsLosesNothingAndBlocksNobody(t *testing.T) {
	w := newWorld(t)
	room := shared(t, w)
	sub, _ := w.s.Subscribe(ctx, w.ada, bus.FromNow)
	defer sub.Close()
	var want []ulid.ULID
	for i := range 250 {
		m, err := w.s.PostMessage(ctx, w.kai, room.ID, fmt.Sprint(i), ulid.Zero)
		require.NoError(t, err)
		want = append(want, m.ID)
	}
	first, err := sub.Poll(ctx)
	require.NoError(t, err)
	assert.Len(t, first, 100, "a poll reads at most 100")
	all := append(first, drain(t, sub)...)
	require.Len(t, all, 250)
	for i, e := range all {
		assert.Equal(t, want[i], e.Message.ID, i)
		if i > 0 {
			assert.Equal(t, all[i-1].ID+1, e.ID)
		}
	}
}

func TestSubscriber_KeepsUpWithConcurrentPosters(t *testing.T) {
	r := storagetest.NewRepo(t)
	s := bus.NewService(r, time.Now)
	h, _ := s.CreateHuman(ctx, "kai", pw)
	kai := bus.Actor{Kind: bus.ActorHuman, ID: h.ID}
	scout, _, _ := s.CreateAgent(ctx, "scout", "Scout")
	room, _ := s.CreateRoom(ctx, kai, "Busy", []ulid.ULID{scout.ID})
	sub, err := s.Subscribe(ctx, kai, bus.FromNow)
	require.NoError(t, err)
	defer sub.Close()
	for g := range 4 {
		go func() {
			for i := range 25 {
				_, _ = s.PostMessage(ctx, kai, room.ID, fmt.Sprint(g, i), ulid.Zero)
			}
		}()
	}
	seen := map[ulid.ULID]bool{}
	last := sub.Cursor()
	deadline := time.After(20 * time.Second)
	for len(seen) < 100 {
		select {
		case <-sub.Wake():
		case <-deadline:
			t.Fatalf("got %d of 100", len(seen))
		}
		for _, e := range drain(t, sub) {
			assert.Greater(t, e.ID, last)
			last = e.ID
			assert.False(t, seen[e.Message.ID])
			seen[e.Message.ID] = true
		}
	}
}

func TestHub_RunStopsEverySubscription(t *testing.T) {
	w := newWorld(t)
	a, _ := w.s.Subscribe(ctx, w.kai, bus.FromNow)
	b, _ := w.s.Subscribe(ctx, w.ada, bus.FromNow)
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- w.hub.Run(runCtx) }()
	select {
	case err := <-done:
		t.Fatalf("Run returned early: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	cancel()
	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return")
	}
	for _, sub := range []*bus.Subscription{a, b} {
		woken, open := rung(sub)
		assert.True(t, woken)
		assert.False(t, open, "the wake channel is closed")
	}
	_, err := w.s.Subscribe(ctx, w.kai, bus.FromNow)
	assert.ErrorIs(t, err, bus.ErrUnavailable)
	a.Close() // closing after the stop, and twice, is fine
	a.Close()
	b.Close()
	room, err := w.s.CreateRoom(ctx, w.kai, "Still works", ids(w.scout)) // ringing a stopped hub is harmless
	require.NoError(t, err)
	assert.False(t, room.ID.IsZero())
}

func TestSubscription_CloseLeavesTheHub(t *testing.T) {
	w := newWorld(t)
	counter := w.s.(interface{ Subscribers() int })
	a, _ := w.s.Subscribe(ctx, w.kai, bus.FromNow)
	b, _ := w.s.Subscribe(ctx, w.kai, bus.FromNow) // one human, two tabs
	assert.Equal(t, 2, counter.Subscribers())
	a.Close()
	a.Close()
	assert.Equal(t, 1, counter.Subscribers())
	_, _ = w.s.CreateRoom(ctx, w.kai, "x", ids(w.scout))
	woken, _ := rung(b)
	assert.True(t, woken)
	b.Close()
	assert.Zero(t, counter.Subscribers())
}

type eventsFail struct{ storage.Repository }

func (eventsFail) ListEventsForHuman(context.Context, string, int64, int64) ([]storage.Event, error) {
	return nil, errDisk
}

func TestPoll_StorageFailureIsReportedAndTheCursorStays(t *testing.T) {
	w := newWorld(t)
	s := bus.NewService(eventsFail{w.r}, func() time.Time { return fixed })
	sub, err := s.Subscribe(ctx, w.kai, 0)
	require.NoError(t, err)
	defer sub.Close()
	_, err = sub.Poll(ctx)
	assert.ErrorIs(t, err, errDisk)
	assert.Zero(t, sub.Cursor())
}
