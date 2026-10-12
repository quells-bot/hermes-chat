package bus_test

import (
	"context"
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

// world is a service on a movable clock with two humans and two agents.
type world struct {
	s            bus.Service
	r            storage.Repository
	hub          *bus.Hub
	now          *time.Time
	kai, ada     bus.Actor
	scout, atlas bus.Agent
}

func newWorld(t *testing.T) world {
	t.Helper()
	now := fixed
	r := storagetest.NewRepo(t)
	svc := bus.NewService(r, func() time.Time { return now })
	var s bus.Service = svc
	human := func(name string) bus.Actor {
		h, err := s.CreateHuman(ctx, name, pw)
		require.NoError(t, err)
		return bus.Actor{Kind: bus.ActorHuman, ID: h.ID}
	}
	agent := func(handle, display string) bus.Agent {
		a, _, err := s.CreateAgent(ctx, handle, display)
		require.NoError(t, err)
		return a
	}
	return world{s: s, r: r, hub: svc.Hub(), now: &now, kai: human("kai"), ada: human("ada"), scout: agent("scout", "Scout"), atlas: agent("atlas", "Atlas")}
}

// kinds returns the kind of every event a can see, in log order.
func (w world) kinds(t *testing.T, a bus.Actor) []string {
	t.Helper()
	events, err := w.r.ListEventsForHuman(ctx, a.ID.String(), 0, 1000)
	require.NoError(t, err)
	out := []string{}
	for _, e := range events {
		out = append(out, e.Kind)
	}
	return out
}

func ids(as ...bus.Agent) (out []ulid.ULID) {
	for _, a := range as {
		out = append(out, a.ID)
	}
	return
}

func ptr[T any](v T) *T { return &v }

func TestCreateRoom(t *testing.T) {
	w := newWorld(t)
	room, err := w.s.CreateRoom(ctx, w.kai, "  Trip planning ", ids(w.scout, w.scout))
	require.NoError(t, err)
	assert.Equal(t, bus.FlavorConversation, room.Flavor)
	assert.Equal(t, "Trip planning", room.Topic)
	assert.Equal(t, fixed, room.CreatedAt)
	assert.False(t, room.Archived())
	require.Len(t, room.Humans, 1)
	assert.Equal(t, w.kai.ID, room.Humans[0].ID)
	assert.Equal(t, "kai", room.Humans[0].Username)
	assert.Equal(t, []bus.RoomAgent{{Agent: w.scout, Connected: false}}, room.Agents)
	assert.True(t, room.LastMessageID.IsZero())
	assert.True(t, room.LastReadMessageID.IsZero())

	both, err := w.s.CreateRoom(ctx, w.kai, "Both", ids(w.scout, w.atlas))
	require.NoError(t, err)
	assert.Equal(t, bus.FlavorChannel, both.Flavor)
	assert.Equal(t, []bus.RoomAgent{{Agent: w.atlas, Connected: false}, {Agent: w.scout, Connected: false}}, both.Agents)

	got, err := w.s.GetRoom(ctx, w.kai, room.ID)
	require.NoError(t, err)
	assert.Equal(t, room, got)
	mine, err := w.s.ListRooms(ctx, w.kai)
	require.NoError(t, err)
	assert.Equal(t, []bus.Room{room, both}, mine)
	hers, err := w.s.ListRooms(ctx, w.ada)
	require.NoError(t, err)
	assert.NotNil(t, hers)
	assert.Empty(t, hers)
	assert.Equal(t, []string{"room.changed", "room.changed"}, w.kinds(t, w.kai))
}

func TestCreateRoom_Validation(t *testing.T) {
	w := newWorld(t)
	for _, topic := range []string{"", "   ", "line\nbreak", strings.Repeat("é", 201)} {
		_, err := w.s.CreateRoom(ctx, w.kai, topic, ids(w.scout))
		var ve bus.ValidationError
		require.ErrorAs(t, err, &ve, "%q", topic)
		assert.Equal(t, "topic must be 1-200 characters with no control characters", ve.Message)
	}
	_, err := w.s.CreateRoom(ctx, w.kai, strings.Repeat("é", 200), ids(w.scout))
	assert.NoError(t, err)
	var ve bus.ValidationError
	_, err = w.s.CreateRoom(ctx, w.kai, "x", nil)
	require.ErrorAs(t, err, &ve)
	assert.Equal(t, "a room needs at least one agent", ve.Message)
	_, err = w.s.CreateRoom(ctx, w.kai, "x", []ulid.ULID{w.scout.ID, ulid.Make()})
	require.ErrorAs(t, err, &ve)
	assert.Equal(t, "no such agent", ve.Message)
	_, err = w.s.CreateRoom(ctx, bus.Actor{Kind: bus.ActorAgent, ID: w.scout.ID}, "x", ids(w.scout))
	assert.ErrorIs(t, err, bus.ErrForbidden)
	assert.Len(t, w.kinds(t, w.kai), 1, "a refused room leaves nothing behind")
}

func TestOpenDM(t *testing.T) {
	w := newWorld(t)
	room, created, err := w.s.OpenDM(ctx, w.kai, w.scout.ID)
	require.NoError(t, err)
	assert.True(t, created)
	assert.Equal(t, bus.FlavorDM, room.Flavor)
	assert.Equal(t, "", room.Topic)
	require.Len(t, room.Humans, 1)
	assert.Equal(t, w.kai.ID, room.Humans[0].ID)
	assert.Equal(t, []bus.RoomAgent{{Agent: w.scout, Connected: false}}, room.Agents)
	again, created, err := w.s.OpenDM(ctx, w.kai, w.scout.ID)
	require.NoError(t, err)
	assert.False(t, created)
	assert.Equal(t, room, again)
	hers, created, err := w.s.OpenDM(ctx, w.ada, w.scout.ID)
	require.NoError(t, err)
	assert.True(t, created)
	assert.NotEqual(t, room.ID, hers.ID) // each human has their own
	other, _, _ := w.s.OpenDM(ctx, w.kai, w.atlas.ID)
	assert.NotEqual(t, room.ID, other.ID)
	_, _, err = w.s.OpenDM(ctx, w.kai, ulid.Make())
	assert.ErrorIs(t, err, bus.ErrValidation)
	_, _, err = w.s.OpenDM(ctx, bus.Actor{Kind: bus.ActorAgent, ID: w.scout.ID}, w.scout.ID)
	assert.ErrorIs(t, err, bus.ErrForbidden)
	assert.Equal(t, []string{"room.changed", "room.changed"}, w.kinds(t, w.kai)) // reopening wrote nothing
}

func TestOpenDM_ConcurrentCallsMakeOneRoom(t *testing.T) {
	w := newWorld(t)
	const n = 8
	rooms := make([]bus.Room, n)
	made := make([]bool, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rooms[i], made[i], errs[i] = w.s.OpenDM(ctx, w.kai, w.scout.ID)
		}()
	}
	wg.Wait()
	count := 0
	for i := range n {
		require.NoError(t, errs[i])
		assert.Equal(t, rooms[0].ID, rooms[i].ID)
		if made[i] {
			count++
		}
	}
	assert.Equal(t, 1, count)
}

// Review Focus 3.
func TestRooms_ANonMemberSeesAndChangesNothing(t *testing.T) {
	w := newWorld(t)
	room, _ := w.s.CreateRoom(ctx, w.kai, "Private", ids(w.scout))
	calls := map[string]func(a bus.Actor, id ulid.ULID) error{
		"get": func(a bus.Actor, id ulid.ULID) error { _, err := w.s.GetRoom(ctx, a, id); return err },
		"update": func(a bus.Actor, id ulid.ULID) error {
			_, err := w.s.UpdateRoom(ctx, a, id, bus.RoomUpdate{Topic: ptr("Mine")})
			return err
		},
		"invite": func(a bus.Actor, id ulid.ULID) error { _, err := w.s.InviteHuman(ctx, a, id, w.ada.ID); return err }, // not even herself
		"add":    func(a bus.Actor, id ulid.ULID) error { _, err := w.s.AddRoomAgent(ctx, a, id, w.atlas.ID); return err },
		"remove": func(a bus.Actor, id ulid.ULID) error {
			_, err := w.s.RemoveRoomAgent(ctx, a, id, w.scout.ID)
			return err
		},
	}
	for name, call := range calls {
		err := call(w.ada, room.ID)
		assert.ErrorIs(t, err, bus.ErrNotFound, name)
		assert.NotErrorIs(t, err, storage.ErrNotFound, name)
		assert.ErrorIs(t, call(w.kai, ulid.Make()), bus.ErrNotFound, name)                                        // no such room looks the same
		assert.ErrorIs(t, call(bus.Actor{Kind: bus.ActorAgent, ID: w.scout.ID}, room.ID), bus.ErrForbidden, name) // a member agent is still not a human
	}
	got, _ := w.s.GetRoom(ctx, w.kai, room.ID)
	assert.Equal(t, room, got)
	assert.Empty(t, w.kinds(t, w.ada))
}

func TestInviteHuman(t *testing.T) {
	w := newWorld(t)
	room, _ := w.s.CreateRoom(ctx, w.kai, "Shared", ids(w.scout))
	got, err := w.s.InviteHuman(ctx, w.kai, room.ID, w.ada.ID)
	require.NoError(t, err)
	require.Len(t, got.Humans, 2)
	assert.Equal(t, "ada", got.Humans[0].Username)
	assert.Equal(t, "kai", got.Humans[1].Username)
	hers, err := w.s.GetRoom(ctx, w.ada, room.ID)
	require.NoError(t, err)
	assert.Equal(t, got, hers)
	_, err = w.s.InviteHuman(ctx, w.ada, room.ID, w.kai.ID) // already a member: nothing happens
	require.NoError(t, err)
	assert.Equal(t, []string{"room.changed", "room.changed"}, w.kinds(t, w.kai))
	var ve bus.ValidationError
	_, err = w.s.InviteHuman(ctx, w.kai, room.ID, ulid.Make())
	require.ErrorAs(t, err, &ve)
	assert.Equal(t, "no such user", ve.Message)
}

func TestAddAndRemoveRoomAgent(t *testing.T) {
	w := newWorld(t)
	room, _ := w.s.CreateRoom(ctx, w.kai, "Team", ids(w.scout))
	got, err := w.s.AddRoomAgent(ctx, w.kai, room.ID, w.atlas.ID)
	require.NoError(t, err)
	assert.Equal(t, bus.FlavorChannel, got.Flavor)
	assert.Equal(t, []bus.RoomAgent{{Agent: w.atlas, Connected: false}, {Agent: w.scout, Connected: false}}, got.Agents)
	_, err = w.s.AddRoomAgent(ctx, w.kai, room.ID, w.atlas.ID) // again: nothing happens
	require.NoError(t, err)
	got, err = w.s.RemoveRoomAgent(ctx, w.kai, room.ID, w.scout.ID)
	require.NoError(t, err)
	assert.Equal(t, bus.FlavorConversation, got.Flavor)
	assert.Equal(t, []bus.RoomAgent{{Agent: w.atlas, Connected: false}}, got.Agents)
	_, err = w.s.RemoveRoomAgent(ctx, w.kai, room.ID, w.scout.ID) // not in the room: nothing happens
	require.NoError(t, err)
	assert.Len(t, w.kinds(t, w.kai), 3) // create, add, remove
	var ve bus.ValidationError
	_, err = w.s.RemoveRoomAgent(ctx, w.kai, room.ID, w.atlas.ID)
	require.ErrorAs(t, err, &ve)
	assert.Equal(t, "a room needs at least one agent", ve.Message)
	_, err = w.s.AddRoomAgent(ctx, w.kai, room.ID, ulid.Make())
	require.ErrorAs(t, err, &ve)
	assert.Equal(t, "no such agent", ve.Message)
}

func TestUpdateRoom(t *testing.T) {
	w := newWorld(t)
	room, _ := w.s.CreateRoom(ctx, w.kai, "Before", ids(w.scout))
	got, err := w.s.UpdateRoom(ctx, w.kai, room.ID, bus.RoomUpdate{Topic: ptr(" After ")})
	require.NoError(t, err)
	assert.Equal(t, "After", got.Topic)
	_, err = w.s.UpdateRoom(ctx, w.kai, room.ID, bus.RoomUpdate{Topic: ptr("After")}) // same topic
	require.NoError(t, err)
	_, err = w.s.UpdateRoom(ctx, w.kai, room.ID, bus.RoomUpdate{}) // nothing asked
	require.NoError(t, err)
	_, err = w.s.UpdateRoom(ctx, w.kai, room.ID, bus.RoomUpdate{Topic: ptr("")})
	assert.ErrorIs(t, err, bus.ErrValidation)
	assert.Len(t, w.kinds(t, w.kai), 2)

	*w.now = fixed.Add(time.Hour)
	got, err = w.s.UpdateRoom(ctx, w.kai, room.ID, bus.RoomUpdate{Archived: ptr(true)})
	require.NoError(t, err)
	assert.True(t, got.Archived())
	assert.Equal(t, fixed.Add(time.Hour), got.ArchivedAt)
	*w.now = fixed.Add(2 * time.Hour)
	got, _ = w.s.UpdateRoom(ctx, w.kai, room.ID, bus.RoomUpdate{Archived: ptr(true)})
	assert.Equal(t, fixed.Add(time.Hour), got.ArchivedAt) // the first time is kept
	assert.Len(t, w.kinds(t, w.kai), 3)

	_, err = w.s.UpdateRoom(ctx, w.kai, room.ID, bus.RoomUpdate{Topic: ptr("New")})
	assert.ErrorIs(t, err, bus.ErrRoomArchived)
	_, err = w.s.InviteHuman(ctx, w.kai, room.ID, w.ada.ID)
	assert.ErrorIs(t, err, bus.ErrRoomArchived)
	_, err = w.s.AddRoomAgent(ctx, w.kai, room.ID, w.atlas.ID)
	assert.ErrorIs(t, err, bus.ErrRoomArchived)
	_, err = w.s.RemoveRoomAgent(ctx, w.kai, room.ID, w.scout.ID)
	assert.ErrorIs(t, err, bus.ErrRoomArchived)
	mine, _ := w.s.ListRooms(ctx, w.kai)
	require.Len(t, mine, 1)
	assert.True(t, mine[0].Archived()) // still listed

	got, err = w.s.UpdateRoom(ctx, w.kai, room.ID, bus.RoomUpdate{Archived: ptr(false), Topic: ptr("Back")})
	require.NoError(t, err)
	assert.False(t, got.Archived())
	assert.Equal(t, "Back", got.Topic)
	assert.Len(t, w.kinds(t, w.kai), 4, "one call, one event")
}

func TestDM_CannotBeChanged(t *testing.T) {
	w := newWorld(t)
	room, _, _ := w.s.OpenDM(ctx, w.kai, w.scout.ID)
	_, e1 := w.s.UpdateRoom(ctx, w.kai, room.ID, bus.RoomUpdate{Archived: ptr(true)})
	_, e2 := w.s.InviteHuman(ctx, w.kai, room.ID, w.ada.ID)
	_, e3 := w.s.AddRoomAgent(ctx, w.kai, room.ID, w.atlas.ID)
	_, e4 := w.s.RemoveRoomAgent(ctx, w.kai, room.ID, w.scout.ID)
	for _, err := range []error{e1, e2, e3, e4} {
		var ve bus.ValidationError
		require.ErrorAs(t, err, &ve)
		assert.Equal(t, "a DM cannot be changed", ve.Message)
	}
	got, _ := w.s.GetRoom(ctx, w.kai, room.ID)
	assert.Equal(t, room, got)
}

type eventFails struct{ storage.Repository }

func (eventFails) PutEvent(context.Context, storage.Event) (int64, error) { return 0, errDisk }

// failingEvents hands every transaction a repository whose PutEvent fails.
type failingEvents struct{ storage.Repository }

func (f failingEvents) InTx(ctx context.Context, fn func(storage.Repository) error) error {
	return f.Repository.InTx(ctx, func(tx storage.Repository) error { return fn(eventFails{tx}) })
}

// The change and its event land together or not at all.
func TestCreateRoom_RollsBackWhenTheEventCannotBeWritten(t *testing.T) {
	w := newWorld(t)
	broken := bus.NewService(failingEvents{w.r}, func() time.Time { return fixed })
	_, err := broken.CreateRoom(ctx, w.kai, "Doomed", ids(w.scout))
	require.ErrorIs(t, err, errDisk)
	mine, _ := w.s.ListRooms(ctx, w.kai)
	assert.Empty(t, mine)
}

func TestRemoveRoomAgent_CancelsWaiting(t *testing.T) {
	w := newWorld(t)
	room, err := w.s.CreateRoom(ctx, w.kai, "Channel", ids(w.atlas, w.scout))
	require.NoError(t, err)
	var msgs []ulid.ULID
	for i := range 3 {
		m, err := w.s.PostMessage(ctx, w.kai, room.ID, fmt.Sprint("@atlas @scout ", i), ulid.Zero)
		require.NoError(t, err)
		msgs = append(msgs, m.ID)
	}
	deliveries := func(mid ulid.ULID) []bus.Delivery {
		m, err := w.s.GetMessage(ctx, w.kai, room.ID, mid)
		require.NoError(t, err)
		return m.Deliveries
	}
	for _, mid := range msgs {
		assert.Equal(t, []bus.Delivery{{Agent: w.atlas, State: bus.DeliveryWaiting}, {Agent: w.scout, State: bus.DeliveryWaiting}}, deliveries(mid))
	}
	_, err = w.s.RemoveRoomAgent(ctx, w.kai, room.ID, w.scout.ID)
	require.NoError(t, err)
	for _, mid := range msgs {
		got := deliveries(mid)
		require.Len(t, got, 2)
		assert.Equal(t, bus.DeliveryCancelled, got[1].State, "scout: %s", mid)
		assert.Equal(t, bus.DeliveryWaiting, got[0].State, "atlas is untouched: %s", mid)
	}
}

func TestUpdateRoom_UnarchiveReleases(t *testing.T) {
	w := newWorld(t)
	queueAndArchive := func() (bus.Room, ulid.ULID) {
		room, err := w.s.CreateRoom(ctx, w.kai, "Conv", ids(w.scout))
		require.NoError(t, err)
		m, err := w.s.PostMessage(ctx, w.kai, room.ID, "queued", ulid.Zero)
		require.NoError(t, err)
		assert.Equal(t, []bus.Delivery{{Agent: w.scout, State: bus.DeliveryWaiting}}, m.Deliveries)
		_, err = w.s.UpdateRoom(ctx, w.kai, room.ID, bus.RoomUpdate{Archived: ptr(true)})
		require.NoError(t, err)
		return room, m.ID
	}
	state := func(room bus.Room, mid ulid.ULID) bus.DeliveryState {
		m, err := w.s.GetMessage(ctx, w.kai, room.ID, mid)
		require.NoError(t, err)
		require.Len(t, m.Deliveries, 1)
		return m.Deliveries[0].State
	}

	// with the agent connected, unarchiving releases the head
	roomA, mA := queueAndArchive()
	sub, err := w.s.Subscribe(ctx, bus.Actor{Kind: bus.ActorAgent, ID: w.scout.ID}, bus.FromNow)
	require.NoError(t, err)
	_, err = w.s.UpdateRoom(ctx, w.kai, roomA.ID, bus.RoomUpdate{Archived: ptr(false)})
	require.NoError(t, err)
	assert.Equal(t, bus.DeliveryDelivered, state(roomA, mA))
	sub.Close()

	// with the agent offline, unarchiving leaves it waiting
	roomB, mB := queueAndArchive()
	_, err = w.s.UpdateRoom(ctx, w.kai, roomB.ID, bus.RoomUpdate{Archived: ptr(false)})
	require.NoError(t, err)
	assert.Equal(t, bus.DeliveryWaiting, state(roomB, mB))
}
