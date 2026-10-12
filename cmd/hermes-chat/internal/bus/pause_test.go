package bus_test

import (
	"testing"

	"github.com/oklog/ulid/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/bus"
)

func agentActor(a bus.Agent) bus.Actor { return bus.Actor{Kind: bus.ActorAgent, ID: a.ID} }

// agentSub opens the agent's stream; the test's cleanup closes it.
func agentSub(t *testing.T, w world, a bus.Agent) *bus.Subscription {
	t.Helper()
	sub, err := w.s.Subscribe(ctx, agentActor(a), bus.FromNow)
	require.NoError(t, err)
	t.Cleanup(sub.Close)
	return sub
}

// of keeps the events of one kind.
func of(evs []bus.Event, kind bus.EventKind) (out []bus.Event) {
	for _, e := range evs {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return
}

// stateOf is the agent's delivery state for a message, as kai sees it.
func stateOf(t *testing.T, w world, room bus.Room, messageID ulid.ULID, a bus.Agent) bus.DeliveryState {
	t.Helper()
	m, err := w.s.GetMessage(ctx, w.kai, room.ID, messageID)
	require.NoError(t, err)
	for _, d := range m.Deliveries {
		if d.Agent.ID == a.ID {
			return d.State
		}
	}
	return ""
}

func TestPauseRoom_HoldsTheQueueUntilResume(t *testing.T) {
	w := newWorld(t)
	room := dmRoom(t, w) // a DM can be paused
	sub := agentSub(t, w, w.scout)
	drain(t, sub)

	got, err := w.s.PauseRoom(ctx, w.kai, room.ID)
	require.NoError(t, err)
	assert.True(t, got.Paused())
	assert.Equal(t, fixed, got.PausedAt)
	assert.Equal(t, bus.PauseHuman, got.PauseReason)
	assert.Equal(t, 6, got.TurnCap)

	m, err := w.s.PostMessage(ctx, w.kai, room.ID, "held", ulid.Zero) // a human may still post
	require.NoError(t, err)
	assert.Equal(t, bus.DeliveryWaiting, m.Deliveries[0].State)
	evs := drain(t, sub)
	require.Len(t, evs, 1, "the agent hears the pause and is handed nothing")
	assert.Equal(t, bus.EventControlPause, evs[0].Kind)
	assert.Equal(t, room.ID, evs[0].RoomID)

	got, err = w.s.ResumeRoom(ctx, w.kai, room.ID)
	require.NoError(t, err)
	assert.False(t, got.Paused())
	assert.Equal(t, bus.PauseNone, got.PauseReason)
	evs = drain(t, sub)
	require.Len(t, evs, 1)
	assert.Equal(t, bus.EventDelivery, evs[0].Kind)
	assert.Equal(t, m.ID, evs[0].Message.ID)
	assert.Equal(t, bus.DeliveryDelivered, stateOf(t, w, room, m.ID, w.scout))
}

func TestPauseRoom_RefusesAgentWrites(t *testing.T) {
	w := newWorld(t)
	room := dmRoom(t, w)
	m1, sub := startedTurn(t, w, room)
	defer sub.Close()
	scout := agentActor(w.scout)
	reply, err := w.s.PostMessage(ctx, scout, room.ID, "partial", ulid.Zero)
	require.NoError(t, err)
	tool, err := w.s.StartTool(ctx, scout, m1, "web_search", "", "")
	require.NoError(t, err)
	m2, err := w.s.PostMessage(ctx, w.kai, room.ID, "second", ulid.Zero)
	require.NoError(t, err)

	_, err = w.s.PauseRoom(ctx, w.kai, room.ID)
	require.NoError(t, err)

	_, err = w.s.PostMessage(ctx, scout, room.ID, "more", ulid.Zero)
	assert.ErrorIs(t, err, bus.ErrPaused)
	assert.ErrorIs(t, w.s.EditMessage(ctx, scout, reply.ID, "more", false), bus.ErrPaused)
	assert.ErrorIs(t, w.s.EditMessage(ctx, scout, reply.ID, "more", true), bus.ErrPaused)
	_, err = w.s.ReportRemark(ctx, scout, m1, "thinking")
	assert.ErrorIs(t, err, bus.ErrPaused)
	_, err = w.s.StartTool(ctx, scout, m1, "web_search", "", "")
	assert.ErrorIs(t, err, bus.ErrPaused)
	_, _, err = w.s.Steer(ctx, w.kai, room.ID, w.scout.ID, "", "faster")
	assert.ErrorIs(t, err, bus.ErrPaused)
	got, _ := w.s.GetMessage(ctx, w.kai, room.ID, reply.ID)
	assert.Equal(t, "partial", got.Text)

	// bookkeeping still lands, and the human keeps cancel and stop
	_, err = w.s.FinishTool(ctx, scout, tool.ID, 5, false)
	assert.NoError(t, err)
	assert.NoError(t, w.s.StopTurn(ctx, w.kai, room.ID, w.scout.ID))
	require.NoError(t, w.s.TurnDone(ctx, scout, m1))
	assert.Equal(t, bus.DeliveryWaiting, stateOf(t, w, room, m2.ID, w.scout), "a finished turn releases nothing while paused")
	m3, _ := w.s.PostMessage(ctx, w.kai, room.ID, "third", ulid.Zero)
	assert.NoError(t, w.s.CancelMessage(ctx, w.kai, room.ID, m3.ID))
	assert.Equal(t, bus.DeliveryWaiting, stateOf(t, w, room, m2.ID, w.scout))

	_, err = w.s.ResumeRoom(ctx, w.kai, room.ID)
	require.NoError(t, err)
	assert.Equal(t, bus.DeliveryDelivered, stateOf(t, w, room, m2.ID, w.scout))
	_, err = w.s.PostMessage(ctx, scout, room.ID, "back", ulid.Zero)
	assert.NoError(t, err)
}

func TestPauseRoom_LateTurnReportsStillLand(t *testing.T) {
	w := newWorld(t)
	room := dmRoom(t, w)
	agentSub(t, w, w.scout)
	m1, _ := w.s.PostMessage(ctx, w.kai, room.ID, "one", ulid.Zero) // released: delivered, not yet working
	m2, _ := w.s.PostMessage(ctx, w.kai, room.ID, "two", ulid.Zero)
	_, err := w.s.PauseRoom(ctx, w.kai, room.ID)
	require.NoError(t, err)
	scout := agentActor(w.scout)
	require.NoError(t, w.s.TurnWorking(ctx, scout, m1.ID))
	require.NoError(t, w.s.TurnFailed(ctx, scout, m1.ID))
	assert.Equal(t, bus.DeliveryFailed, stateOf(t, w, room, m1.ID, w.scout))
	assert.Equal(t, bus.DeliveryWaiting, stateOf(t, w, room, m2.ID, w.scout))
}

func TestPauseRoom_EventsAndDoubleTaps(t *testing.T) {
	w := newWorld(t)
	room := channelRoom(t, w)
	scout, atlas := agentSub(t, w, w.scout), agentSub(t, w, w.atlas)
	human, err := w.s.Subscribe(ctx, w.kai, bus.FromNow)
	require.NoError(t, err)
	defer human.Close()
	drain(t, scout)
	drain(t, atlas)
	drain(t, human)

	_, err = w.s.PauseRoom(ctx, w.kai, room.ID)
	require.NoError(t, err)
	evs := drain(t, human)
	require.Len(t, evs, 1)
	assert.Equal(t, bus.EventRoomChanged, evs[0].Kind)
	assert.True(t, evs[0].Room.Paused())
	assert.Equal(t, bus.PauseHuman, evs[0].Room.PauseReason)
	assert.Len(t, of(drain(t, scout), bus.EventControlPause), 1)
	assert.Len(t, of(drain(t, atlas), bus.EventControlPause), 1)

	again, err := w.s.PauseRoom(ctx, w.kai, room.ID)
	require.NoError(t, err)
	assert.True(t, again.Paused())
	assert.Empty(t, drain(t, human), "a second pause writes nothing")
	assert.Empty(t, drain(t, scout))

	_, err = w.s.ResumeRoom(ctx, w.kai, room.ID)
	require.NoError(t, err)
	evs = drain(t, human)
	require.Len(t, evs, 1)
	assert.False(t, evs[0].Room.Paused())
	assert.Empty(t, drain(t, scout), "agents get no resume event")
	_, err = w.s.ResumeRoom(ctx, w.kai, room.ID)
	require.NoError(t, err)
	assert.Empty(t, drain(t, human), "a second resume writes nothing")
}

func TestPauseRoom_WhoMayPause(t *testing.T) {
	w := newWorld(t)
	room := convRoom(t, w)
	_, err := w.s.PauseRoom(ctx, agentActor(w.scout), room.ID)
	assert.ErrorIs(t, err, bus.ErrForbidden)
	_, err = w.s.ResumeRoom(ctx, agentActor(w.scout), room.ID)
	assert.ErrorIs(t, err, bus.ErrForbidden)
	_, err = w.s.PauseRoom(ctx, w.ada, room.ID) // not a member
	assert.ErrorIs(t, err, bus.ErrNotFound)
	_, err = w.s.PauseRoom(ctx, w.kai, ulid.Make())
	assert.ErrorIs(t, err, bus.ErrNotFound)
	_, err = w.s.UpdateRoom(ctx, w.kai, room.ID, bus.RoomUpdate{Archived: ptr(true)})
	require.NoError(t, err)
	got, err := w.s.PauseRoom(ctx, w.kai, room.ID) // the off switch never refuses an archived room
	require.NoError(t, err)
	assert.True(t, got.Paused())
}

func TestPauseRoom_AConnectingAgentIsHandedNothingFromIt(t *testing.T) {
	w := newWorld(t)
	held, open := dmRoom(t, w), convRoom(t, w)
	_, _ = w.s.PostMessage(ctx, w.kai, held.ID, "held", ulid.Zero)
	free, _ := w.s.PostMessage(ctx, w.kai, open.ID, "free", ulid.Zero)
	_, err := w.s.PauseRoom(ctx, w.kai, held.ID)
	require.NoError(t, err)
	got := of(drain(t, agentSub(t, w, w.scout)), bus.EventDelivery)
	require.Len(t, got, 1, "the sweep on connect skips the paused room")
	assert.Equal(t, free.ID, got[0].Message.ID)
}

// A delivery released to a stream that died unread, in a room that was then paused.
func unreadDeliveryInPausedRoom(t *testing.T, w world) (bus.Room, bus.Message) {
	t.Helper()
	room := dmRoom(t, w)
	sub, err := w.s.Subscribe(ctx, agentActor(w.scout), bus.FromNow)
	require.NoError(t, err)
	m, err := w.s.PostMessage(ctx, w.kai, room.ID, "one", ulid.Zero)
	require.NoError(t, err)
	require.Equal(t, bus.DeliveryDelivered, m.Deliveries[0].State)
	sub.Close() // gone before it read the delivery
	_, err = w.s.PauseRoom(ctx, w.kai, room.ID)
	require.NoError(t, err)
	return room, m
}

func TestPause_ReconnectingIsHandedNothing(t *testing.T) {
	w := newWorld(t)
	_, _ = unreadDeliveryInPausedRoom(t, w)

	noCursor, err := w.s.Subscribe(ctx, agentActor(w.scout), bus.FromNow)
	require.NoError(t, err)
	assert.Empty(t, of(drain(t, noCursor), bus.EventDelivery))
	noCursor.Close()

	replay, err := w.s.Subscribe(ctx, agentActor(w.scout), 0) // everything since the start of the log
	require.NoError(t, err)
	defer replay.Close()
	evs := drain(t, replay)
	assert.Empty(t, of(evs, bus.EventDelivery))
	assert.Len(t, of(evs, bus.EventControlPause), 1, "the replay still carries the pause")
}

func TestResume_HandsOverAgainWhatWasHeldBack(t *testing.T) {
	w := newWorld(t)
	room, m := unreadDeliveryInPausedRoom(t, w)
	sub := agentSub(t, w, w.scout)
	drain(t, sub)
	human, err := w.s.Subscribe(ctx, w.kai, bus.FromNow)
	require.NoError(t, err)
	defer human.Close()

	_, err = w.s.ResumeRoom(ctx, w.kai, room.ID)
	require.NoError(t, err)
	got := of(drain(t, sub), bus.EventDelivery)
	require.Len(t, got, 1)
	assert.Equal(t, m.ID, got[0].Message.ID)
	assert.Equal(t, bus.DeliveryDelivered, stateOf(t, w, room, m.ID, w.scout))
	assert.Empty(t, of(drain(t, human), bus.EventDeliveryChanged), "handing over again is not a state change")
	// Two delivery rows now sit in the log for m; a stream with no cursor is handed it once.
	sub.Close()
	again := agentSub(t, w, w.scout)
	gotAgain := of(drain(t, again), bus.EventDelivery)
	require.Len(t, gotAgain, 1)
	assert.Equal(t, m.ID, gotAgain[0].Message.ID)
	require.NoError(t, w.s.TurnDone(ctx, agentActor(w.scout), m.ID)) // and the queue moves on
}

func TestResume_AnOfflineAgentIsHandedItWhenItReturns(t *testing.T) {
	w := newWorld(t)
	room, m := unreadDeliveryInPausedRoom(t, w)
	_, err := w.s.ResumeRoom(ctx, w.kai, room.ID)
	require.NoError(t, err)
	got := of(drain(t, agentSub(t, w, w.scout)), bus.EventDelivery)
	require.Len(t, got, 1, "exactly once: resume wrote no second row for an agent that was away")
	assert.Equal(t, m.ID, got[0].Message.ID)
}

func TestResume_AnAgentThatReconnectedWithACursorIsStillHandedIt(t *testing.T) {
	w := newWorld(t)
	room, m := unreadDeliveryInPausedRoom(t, w)
	during, err := w.s.Subscribe(ctx, agentActor(w.scout), 0) // replays during the pause: the delivery is skipped
	require.NoError(t, err)
	assert.Empty(t, of(drain(t, during), bus.EventDelivery))
	cursor := during.Cursor()
	during.Close() // and it is away when the room resumes
	_, err = w.s.ResumeRoom(ctx, w.kai, room.ID)
	require.NoError(t, err)

	back, err := w.s.Subscribe(ctx, agentActor(w.scout), cursor)
	require.NoError(t, err)
	defer back.Close()
	got := of(drain(t, back), bus.EventDelivery)
	require.Len(t, got, 1, "its cursor is past the row, so the subscribe hands it over again, once")
	assert.Equal(t, m.ID, got[0].Message.ID)
	require.NoError(t, w.s.TurnDone(ctx, agentActor(w.scout), m.ID))
}

func TestPause_AStreamThatDroppedIsNotHandedASteerWrittenBeforeThePause(t *testing.T) {
	w := newWorld(t)
	room := dmRoom(t, w)
	_, sub := startedTurn(t, w, room)
	drain(t, sub)
	cursor := sub.Cursor()
	sub.Close() // the stream drops mid-turn

	_, _, err := w.s.Steer(ctx, w.kai, room.ID, w.scout.ID, "", "faster")
	require.NoError(t, err)
	_, err = w.s.PauseRoom(ctx, w.kai, room.ID)
	require.NoError(t, err)

	back, err := w.s.Subscribe(ctx, agentActor(w.scout), cursor)
	require.NoError(t, err)
	defer back.Close()
	evs := drain(t, back)
	assert.Empty(t, of(evs, bus.EventControlSteer), "a paused room hands the agent no human text")
	assert.Len(t, of(evs, bus.EventControlPause), 1)
}

func TestUnarchiveAndResume_ReleaseNothingWhileTheOtherClosureHolds(t *testing.T) {
	w := newWorld(t)
	room := convRoom(t, w)
	agentSub(t, w, w.scout)
	_, err := w.s.PauseRoom(ctx, w.kai, room.ID)
	require.NoError(t, err)
	m, err := w.s.PostMessage(ctx, w.kai, room.ID, "held", ulid.Zero)
	require.NoError(t, err)
	_, err = w.s.UpdateRoom(ctx, w.kai, room.ID, bus.RoomUpdate{Archived: ptr(true)})
	require.NoError(t, err)

	_, err = w.s.UpdateRoom(ctx, w.kai, room.ID, bus.RoomUpdate{Archived: ptr(false)})
	require.NoError(t, err)
	assert.Equal(t, bus.DeliveryWaiting, stateOf(t, w, room, m.ID, w.scout), "unarchiving a paused room releases nothing")

	_, err = w.s.UpdateRoom(ctx, w.kai, room.ID, bus.RoomUpdate{Archived: ptr(true)})
	require.NoError(t, err)
	_, err = w.s.ResumeRoom(ctx, w.kai, room.ID)
	require.NoError(t, err)
	assert.Equal(t, bus.DeliveryWaiting, stateOf(t, w, room, m.ID, w.scout), "resuming an archived room releases nothing")
}
