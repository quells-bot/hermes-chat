package bus_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/bus"
)

// scoutSub opens scout's stream and returns it.
func scoutSub(t *testing.T, w world) *bus.Subscription {
	t.Helper()
	sub, err := w.s.Subscribe(ctx, bus.Actor{Kind: bus.ActorAgent, ID: w.scout.ID}, bus.FromNow)
	require.NoError(t, err)
	return sub
}

// startedTurn posts kai's first message in the room with scout connected and
// reports the delivery working. It leaves scout subscribed, so the caller can
// watch scout's stream; the caller closes sub.
func startedTurn(t *testing.T, w world, room bus.Room) (mID ulid.ULID, sub *bus.Subscription) {
	t.Helper()
	sub = scoutSub(t, w)
	m, err := w.s.PostMessage(ctx, w.kai, room.ID, "first", ulid.Zero)
	require.NoError(t, err)
	drain(t, sub) // the delivery event
	require.NoError(t, w.s.TurnWorking(ctx, bus.Actor{Kind: bus.ActorAgent, ID: w.scout.ID}, m.ID))
	return m.ID, sub
}

func TestTurnReports_StateMachine(t *testing.T) {
	w := newWorld(t)
	room := dmRoom(t, w)
	humanSub, err := w.s.Subscribe(ctx, w.kai, bus.FromNow)
	require.NoError(t, err)
	defer humanSub.Close()
	scoutActor := bus.Actor{Kind: bus.ActorAgent, ID: w.scout.ID}
	sub := scoutSub(t, w)
	// post a message with scout connected: it is released to delivered.
	post := func(text string) bus.Message {
		m, err := w.s.PostMessage(ctx, w.kai, room.ID, text, ulid.Zero)
		require.NoError(t, err)
		drain(t, sub)      // the delivery event
		drain(t, humanSub) // message.posted
		return m
	}

	// working from delivered
	m1 := post("one")
	require.NoError(t, w.s.TurnWorking(ctx, scoutActor, m1.ID))
	evs := drain(t, humanSub)
	require.Len(t, evs, 1, "a real change writes exactly one delivery.changed")
	assert.Equal(t, bus.EventDeliveryChanged, evs[0].Kind)
	assert.Equal(t, m1.ID, evs[0].Message.ID)

	// working from working: a no-op, no second event
	require.NoError(t, w.s.TurnWorking(ctx, scoutActor, m1.ID))
	assert.Empty(t, drain(t, humanSub), "a no-op working writes nothing")
	assert.Empty(t, drain(t, sub))

	// done from working
	require.NoError(t, w.s.TurnDone(ctx, scoutActor, m1.ID))
	evs = drain(t, humanSub)
	require.Len(t, evs, 1)
	assert.Equal(t, bus.EventDeliveryChanged, evs[0].Kind)
	assert.Equal(t, m1.ID, evs[0].Message.ID)

	// done from done: a no-op
	require.NoError(t, w.s.TurnDone(ctx, scoutActor, m1.ID))
	assert.Empty(t, drain(t, humanSub))

	// failed from working, then idempotent
	m2 := post("two")
	require.NoError(t, w.s.TurnWorking(ctx, scoutActor, m2.ID))
	drain(t, humanSub)
	require.NoError(t, w.s.TurnFailed(ctx, scoutActor, m2.ID))
	evs = drain(t, humanSub)
	require.Len(t, evs, 1)
	assert.Equal(t, bus.EventDeliveryChanged, evs[0].Kind)
	require.NoError(t, w.s.TurnFailed(ctx, scoutActor, m2.ID))
	assert.Empty(t, drain(t, humanSub))

	// a report from the wrong agent looks missing
	atlasActor := bus.Actor{Kind: bus.ActorAgent, ID: w.atlas.ID}
	require.ErrorIs(t, w.s.TurnWorking(ctx, atlasActor, m2.ID), bus.ErrNotFound)

	// a report for a message with no delivery is missing
	ghost, err := w.s.PostMessage(ctx, scoutActor, room.ID, "no one", ulid.Zero) // an agent post wakes nobody
	require.NoError(t, err)
	drain(t, humanSub) // message.posted
	require.ErrorIs(t, w.s.TurnWorking(ctx, scoutActor, ghost.ID), bus.ErrNotFound)

	// a report from waiting is no turn: offline so the next delivery waits
	sub.Close()
	m3, err := w.s.PostMessage(ctx, w.kai, room.ID, "three", ulid.Zero)
	require.NoError(t, err)
	drain(t, humanSub) // message.posted; no delivery event, the agent is offline
	require.ErrorIs(t, w.s.TurnWorking(ctx, scoutActor, m3.ID), bus.ErrNoTurn)
	require.ErrorIs(t, w.s.TurnDone(ctx, scoutActor, m3.ID), bus.ErrNoTurn)
	require.ErrorIs(t, w.s.TurnFailed(ctx, scoutActor, m3.ID), bus.ErrNoTurn)
}

// TestTurnDone_ReleasesNextHead: done hands out the pair's next waiting head
// when it can, and never invents a double release.
func TestTurnDone_ReleasesNextHead(t *testing.T) {
	w := newWorld(t)
	room := dmRoom(t, w)
	scoutActor := bus.Actor{Kind: bus.ActorAgent, ID: w.scout.ID}
	sub := scoutSub(t, w) // scout connected; hello one
	m1, _ := w.s.PostMessage(ctx, w.kai, room.ID, "one", ulid.Zero)
	drain(t, sub) // m1 is delivered
	m2, _ := w.s.PostMessage(ctx, w.kai, room.ID, "two", ulid.Zero)
	m3, _ := w.s.PostMessage(ctx, w.kai, room.ID, "three", ulid.Zero)
	assert.Empty(t, drain(t, sub), "m2 and m3 wait behind the open turn")

	sub.Close() // scout is offline: nothing can be released
	require.NoError(t, w.s.TurnDone(ctx, scoutActor, m1.ID))

	sub2 := scoutSub(t, w)
	defer sub2.Close()
	sweep := w.s.(interface {
		ReleaseSweepForTest(context.Context, ulid.ULID) error
	})
	require.NoError(t, sweep.ReleaseSweepForTest(ctx, w.scout.ID))
	evs := drain(t, sub2)
	require.Len(t, evs, 1, "the sweep hands out m2, never m3")
	assert.Equal(t, bus.EventDelivery, evs[0].Kind)
	assert.Equal(t, m2.ID, evs[0].Message.ID)

	require.NoError(t, w.s.TurnDone(ctx, scoutActor, m2.ID))
	evs = drain(t, sub2)
	require.Len(t, evs, 1, "m3 follows once m2 is done")
	assert.Equal(t, m3.ID, evs[0].Message.ID)
}

func TestEditMessage_ThrottleBroadcastFinalize(t *testing.T) {
	w := newWorld(t)
	room := dmRoom(t, w)
	scoutActor := bus.Actor{Kind: bus.ActorAgent, ID: w.scout.ID}
	m, err := w.s.PostMessage(ctx, scoutActor, room.ID, "original", ulid.Zero) // an agent reply
	require.NoError(t, err)
	humanSub, err := w.s.Subscribe(ctx, w.kai, bus.FromNow)
	require.NoError(t, err)
	defer humanSub.Close()

	// three non-final edits within one tick: only the first writes the row
	require.NoError(t, w.s.EditMessage(ctx, scoutActor, m.ID, "edit 1", false))
	require.NoError(t, w.s.EditMessage(ctx, scoutActor, m.ID, "edit 2", false))
	require.NoError(t, w.s.EditMessage(ctx, scoutActor, m.ID, "edit 3", false))
	got, err := w.s.GetMessage(ctx, w.kai, room.ID, m.ID)
	require.NoError(t, err)
	assert.Equal(t, "edit 1", got.Text, "the first non-final edit writes, the rest are throttled")
	edits := readAllAvailable(humanSub.Edits())
	require.Len(t, edits, 3, "every edit is broadcast")
	assert.Equal(t, []bus.LiveEdit{
		{MessageID: m.ID, Text: "edit 1"},
		{MessageID: m.ID, Text: "edit 2"},
		{MessageID: m.ID, Text: "edit 3"},
	}, edits)
	for _, k := range w.kinds(t, w.kai) {
		assert.NotEqual(t, string(bus.EventMessageEdited), k, "no message.edited event rows before finalize")
	}

	// once the throttle expires the row updates
	*w.now = fixed.Add(2 * time.Second)
	require.NoError(t, w.s.EditMessage(ctx, scoutActor, m.ID, "edit 4", false))
	got, _ = w.s.GetMessage(ctx, w.kai, room.ID, m.ID)
	assert.Equal(t, "edit 4", got.Text)
	assert.Equal(t, "edit 4", readAllAvailable(humanSub.Edits())[0].Text)

	// a final edit always writes and emits exactly one message.edited
	require.NoError(t, w.s.EditMessage(ctx, scoutActor, m.ID, "final text", true))
	got, _ = w.s.GetMessage(ctx, w.kai, room.ID, m.ID)
	assert.Equal(t, "final text", got.Text)
	assert.Equal(t, "final text", readAllAvailable(humanSub.Edits())[0].Text)
	evs := drain(t, humanSub)
	require.Len(t, evs, 1, "a final edit writes exactly one message.edited")
	assert.Equal(t, bus.EventMessageEdited, evs[0].Kind)
	require.NotNil(t, evs[0].Message)
	assert.Equal(t, m.ID, evs[0].Message.ID)
	assert.Equal(t, "final text", evs[0].Message.Text)

	// another agent, a human's message, a missing one, invalid text
	atlasActor := bus.Actor{Kind: bus.ActorAgent, ID: w.atlas.ID}
	require.ErrorIs(t, w.s.EditMessage(ctx, atlasActor, m.ID, "x", false), bus.ErrForbidden)
	hmsg, err := w.s.PostMessage(ctx, w.kai, room.ID, "human message", ulid.Zero)
	require.NoError(t, err)
	require.ErrorIs(t, w.s.EditMessage(ctx, scoutActor, hmsg.ID, "x", false), bus.ErrForbidden)
	require.ErrorIs(t, w.s.EditMessage(ctx, scoutActor, ulid.Make(), "x", false), bus.ErrNotFound)
	var ve bus.ValidationError
	err = w.s.EditMessage(ctx, scoutActor, m.ID, "   ", false)
	require.ErrorAs(t, err, &ve)
	assert.Equal(t, "message text must be 1 to 262144 bytes, not blank, with no NUL character", ve.Message)
}

func TestActivity_ReportsAndListing(t *testing.T) {
	w := newWorld(t)
	room := dmRoom(t, w)
	scoutActor := bus.Actor{Kind: bus.ActorAgent, ID: w.scout.ID}
	atlasActor := bus.Actor{Kind: bus.ActorAgent, ID: w.atlas.ID}
	humanSub, err := w.s.Subscribe(ctx, w.kai, bus.FromNow)
	require.NoError(t, err)
	defer humanSub.Close()
	mID, sub := startedTurn(t, w, room)
	defer sub.Close()
	drain(t, humanSub) // the turn setup events
	assert.Empty(t, drain(t, sub))

	// a remark
	remark, err := w.s.ReportRemark(ctx, scoutActor, mID, "checking the weather")
	require.NoError(t, err)
	assert.Equal(t, "checking the weather", remark.Text)
	assert.Equal(t, mID, remark.MessageID)
	assert.Equal(t, bus.ActivityRemark, remark.Kind)
	evs := drain(t, humanSub)
	require.Len(t, evs, 1)
	assert.Equal(t, bus.EventActivityPosted, evs[0].Kind)
	require.NotNil(t, evs[0].Activity)
	assert.Equal(t, "checking the weather", evs[0].Activity.Text)
	assert.Equal(t, mID, evs[0].Activity.MessageID)

	// a tool step
	tool, err := w.s.StartTool(ctx, scoutActor, mID, "web_search", "searching for flights", `{"q":"flights"}`)
	require.NoError(t, err)
	assert.Equal(t, mID, tool.MessageID)
	require.Nil(t, tool.DurationMs)
	require.Nil(t, tool.Succeeded)
	evs = drain(t, humanSub)
	require.Len(t, evs, 1)
	assert.Equal(t, bus.EventActivityPosted, evs[0].Kind)
	require.NotNil(t, evs[0].Activity)
	assert.Equal(t, tool.ID, evs[0].Activity.ID)

	// finish it
	finished, err := w.s.FinishTool(ctx, scoutActor, tool.ID, 123, true)
	require.NoError(t, err)
	require.NotNil(t, finished.DurationMs)
	assert.EqualValues(t, 123, *finished.DurationMs)
	assert.True(t, *finished.Succeeded)
	evs = drain(t, humanSub)
	require.Len(t, evs, 1)
	assert.Equal(t, tool.ID, evs[0].Activity.ID)

	// listing shows the remark then the finished tool
	list, err := w.s.ListActivity(ctx, w.kai, room.ID, mID)
	require.NoError(t, err)
	require.Len(t, list, 2)
	assert.Equal(t, bus.ActivityRemark, list[0].Kind)
	assert.Equal(t, "checking the weather", list[0].Text)
	assert.Equal(t, bus.ActivityTool, list[1].Kind)
	assert.Equal(t, "web_search", list[1].Tool)
	require.NotNil(t, list[1].DurationMs)
	assert.EqualValues(t, 123, *list[1].DurationMs)

	// finishing a remark is not found
	_, err = w.s.FinishTool(ctx, scoutActor, list[0].ID, 1, true)
	require.ErrorIs(t, err, bus.ErrNotFound)
	// another agent cannot finish scout's tool
	_, err = w.s.FinishTool(ctx, atlasActor, tool.ID, 1, true)
	require.ErrorIs(t, err, bus.ErrForbidden)
	// listing is a human viewer's call
	_, err = w.s.ListActivity(ctx, scoutActor, room.ID, mID)
	require.ErrorIs(t, err, bus.ErrForbidden)
	// a room the caller is not in, and a message not in the room, look missing
	_, err = w.s.ListActivity(ctx, w.kai, ulid.Make(), mID)
	require.ErrorIs(t, err, bus.ErrNotFound)
	_, err = w.s.ListActivity(ctx, w.kai, room.ID, ulid.Make())
	require.ErrorIs(t, err, bus.ErrNotFound)

	// validation
	var ve bus.ValidationError
	_, err = w.s.ReportRemark(ctx, scoutActor, mID, "   ")
	require.ErrorAs(t, err, &ve)
	assert.Equal(t, "remark text must be 1 to 262144 bytes, not blank, with no NUL character", ve.Message)
	_, err = w.s.StartTool(ctx, scoutActor, mID, strings.Repeat("a", 65), "p", "a")
	require.ErrorAs(t, err, &ve)
	assert.Equal(t, "tool name must be 1-64 characters with no control characters", ve.Message)
	_, err = w.s.StartTool(ctx, scoutActor, mID, "web\nsearch", "p", "a")
	require.ErrorAs(t, err, &ve)
	assert.Equal(t, "tool name must be 1-64 characters with no control characters", ve.Message)
	_, err = w.s.StartTool(ctx, scoutActor, mID, "web_search", strings.Repeat("p", 201), "a")
	require.ErrorAs(t, err, &ve)
	assert.Equal(t, "tool preview must be at most 200 characters with no control characters", ve.Message)
	_, err = w.s.StartTool(ctx, scoutActor, mID, "web_search", "p", strings.Repeat("a", 65537))
	require.ErrorAs(t, err, &ve)
	assert.Equal(t, "tool arguments must be at most 65536 bytes", ve.Message)
	_, err = w.s.FinishTool(ctx, scoutActor, tool.ID, -1, true)
	require.ErrorAs(t, err, &ve)
	assert.Equal(t, "duration_ms must not be negative", ve.Message)
}

func TestStopTurnAndSteer(t *testing.T) {
	w := newWorld(t)
	room := dmRoom(t, w)
	scoutActor := bus.Actor{Kind: bus.ActorAgent, ID: w.scout.ID}
	humanSub, err := w.s.Subscribe(ctx, w.kai, bus.FromNow)
	require.NoError(t, err)
	defer humanSub.Close()
	mID, sub := startedTurn(t, w, room)
	defer sub.Close()
	drain(t, humanSub) // the turn setup events
	assert.Empty(t, drain(t, sub))

	// steer applies to the running turn and creates no delivery
	steerMsg, _, err := w.s.Steer(ctx, w.kai, room.ID, w.scout.ID, "", "also check trains")
	require.NoError(t, err)
	assert.Equal(t, room.ID, steerMsg.RoomID)
	assert.Equal(t, "also check trains", steerMsg.Text)
	listed, err := w.s.ListMessages(ctx, w.kai, room.ID, ulid.Zero, 0)
	require.NoError(t, err)
	found := false
	for _, mm := range listed {
		found = found || mm.ID == steerMsg.ID
	}
	assert.True(t, found, "the steer shows in the room")
	hed := drain(t, humanSub)
	require.Len(t, hed, 1)
	assert.Equal(t, bus.EventMessagePosted, hed[0].Kind)
	require.NotNil(t, hed[0].Message)
	assert.Equal(t, steerMsg.ID, hed[0].Message.ID)
	sed := drain(t, sub)
	require.Len(t, sed, 1, "the agent gets one steer event")
	assert.Equal(t, bus.EventControlSteer, sed[0].Kind)
	require.NotNil(t, sed[0].Message)
	assert.Equal(t, steerMsg.ID, sed[0].Message.ID)
	assert.Equal(t, "also check trains", sed[0].Message.Text)
	got, err := w.s.GetMessage(ctx, w.kai, room.ID, steerMsg.ID)
	require.NoError(t, err)
	require.Len(t, got.Deliveries, 0, "steer is a control, not a delivery")

	// no working delivery for the pair is no turn
	require.NoError(t, w.s.TurnDone(ctx, scoutActor, mID))
	drain(t, humanSub) // delivery.changed
	_, _, err = w.s.Steer(ctx, w.kai, room.ID, w.scout.ID, "", "again")
	require.ErrorIs(t, err, bus.ErrNoTurn)

	// an archived room refuses steer
	conv, _ := w.s.CreateRoom(ctx, w.kai, "Conv", ids(w.scout))
	_, err = w.s.UpdateRoom(ctx, w.kai, conv.ID, bus.RoomUpdate{Archived: ptr(true)})
	require.NoError(t, err)
	_, _, err = w.s.Steer(ctx, w.kai, conv.ID, w.scout.ID, "", "x")
	require.ErrorIs(t, err, bus.ErrRoomArchived)
	// an agent cannot steer
	_, _, err = w.s.Steer(ctx, scoutActor, room.ID, w.scout.ID, "", "x")
	require.ErrorIs(t, err, bus.ErrForbidden)
	// a non-member agent is not found
	_, _, err = w.s.Steer(ctx, w.kai, room.ID, w.atlas.ID, "", "x")
	require.ErrorIs(t, err, bus.ErrNotFound)

	// stop is idempotent and needs no running turn
	require.NoError(t, w.s.StopTurn(ctx, w.kai, room.ID, w.scout.ID))
	sed = drain(t, sub)
	require.Len(t, sed, 1)
	assert.Equal(t, bus.EventControlStop, sed[0].Kind)
	assert.Equal(t, room.ID, sed[0].RoomID)
	require.NoError(t, w.s.StopTurn(ctx, w.kai, room.ID, w.scout.ID), "stop with no turn running still succeeds")
	require.ErrorIs(t, w.s.StopTurn(ctx, w.kai, room.ID, w.atlas.ID), bus.ErrNotFound)
}

// A live edit carries message text, so it goes only to the humans of the
// message's room: a human in no room with it hears nothing.
func TestEditMessage_BroadcastsOnlyToTheRoomsHumans(t *testing.T) {
	w := newWorld(t)
	room := dmRoom(t, w) // kai and scout; ada is not in it
	scoutActor := bus.Actor{Kind: bus.ActorAgent, ID: w.scout.ID}
	m, err := w.s.PostMessage(ctx, scoutActor, room.ID, "original", ulid.Zero)
	require.NoError(t, err)
	kaiSub, err := w.s.Subscribe(ctx, w.kai, bus.FromNow)
	require.NoError(t, err)
	defer kaiSub.Close()
	adaSub, err := w.s.Subscribe(ctx, w.ada, bus.FromNow)
	require.NoError(t, err)
	defer adaSub.Close()

	require.NoError(t, w.s.EditMessage(ctx, scoutActor, m.ID, "kai's private answer", false))
	require.NoError(t, w.s.EditMessage(ctx, scoutActor, m.ID, "kai's private answer, done", true))

	assert.Len(t, readAllAvailable(kaiSub.Edits()), 2, "the room's human sees both edits")
	assert.Empty(t, readAllAvailable(adaSub.Edits()), "a human outside the room sees none")
}

// Review Focus 4: once a message is finalized, a late non-final edit (a retried
// or crossed request) must not overwrite the final text or reach a browser.
func TestEditMessage_NonFinalAfterFinalChangesNothing(t *testing.T) {
	w := newWorld(t)
	room := dmRoom(t, w)
	scoutActor := bus.Actor{Kind: bus.ActorAgent, ID: w.scout.ID}
	m, err := w.s.PostMessage(ctx, scoutActor, room.ID, "original", ulid.Zero)
	require.NoError(t, err)
	require.NoError(t, w.s.EditMessage(ctx, scoutActor, m.ID, "final text", true))
	humanSub, err := w.s.Subscribe(ctx, w.kai, bus.FromNow)
	require.NoError(t, err)
	defer humanSub.Close()

	*w.now = fixed.Add(2 * time.Second) // well past the throttle
	require.NoError(t, w.s.EditMessage(ctx, scoutActor, m.ID, "stale partial", false))
	got, err := w.s.GetMessage(ctx, w.kai, room.ID, m.ID)
	require.NoError(t, err)
	assert.Equal(t, "final text", got.Text, "the final text stands")
	assert.Empty(t, readAllAvailable(humanSub.Edits()), "the stale edit is not broadcast")

	// a repeated final still writes and reports
	require.NoError(t, w.s.EditMessage(ctx, scoutActor, m.ID, "final text, corrected", true))
	got, err = w.s.GetMessage(ctx, w.kai, room.ID, m.ID)
	require.NoError(t, err)
	assert.Equal(t, "final text, corrected", got.Text)
	require.Len(t, drain(t, humanSub), 1)
}

// Activity belongs to a turn the agent actually holds: a delivery that was
// never handed over, or was cancelled, takes none.
func TestActivity_NeedsADeliveredTurn(t *testing.T) {
	w := newWorld(t)
	room := dmRoom(t, w)
	scoutActor := bus.Actor{Kind: bus.ActorAgent, ID: w.scout.ID}
	m, err := w.s.PostMessage(ctx, w.kai, room.ID, "queued", ulid.Zero) // scout is offline: waiting
	require.NoError(t, err)

	_, err = w.s.ReportRemark(ctx, scoutActor, m.ID, "peeking")
	require.ErrorIs(t, err, bus.ErrNoTurn, "a waiting delivery was never handed over")
	_, err = w.s.StartTool(ctx, scoutActor, m.ID, "search", "p", "{}")
	require.ErrorIs(t, err, bus.ErrNoTurn)

	require.NoError(t, w.s.CancelMessage(ctx, w.kai, room.ID, m.ID))
	_, err = w.s.ReportRemark(ctx, scoutActor, m.ID, "still here")
	require.ErrorIs(t, err, bus.ErrNoTurn, "a cancelled delivery takes no activity")
	_, err = w.s.StartTool(ctx, scoutActor, m.ID, "search", "p", "{}")
	require.ErrorIs(t, err, bus.ErrNoTurn)

	acts, err := w.s.ListActivity(ctx, w.kai, room.ID, m.ID)
	require.NoError(t, err)
	assert.Empty(t, acts)
}

// An agent taken out of a room writes nothing more into it: no activity for its
// old delivery, no edit of its old message.
func TestRemovedAgent_WritesNothingMore(t *testing.T) {
	w := newWorld(t)
	room := channelRoom(t, w) // atlas and scout
	scoutActor := bus.Actor{Kind: bus.ActorAgent, ID: w.scout.ID}
	sub := scoutSub(t, w)
	defer sub.Close()
	m, err := w.s.PostMessage(ctx, w.kai, room.ID, "@scout go", ulid.Zero)
	require.NoError(t, err)
	reply, err := w.s.PostMessage(ctx, scoutActor, room.ID, "draft", ulid.Zero)
	require.NoError(t, err)
	_, err = w.s.RemoveRoomAgent(ctx, w.kai, room.ID, w.scout.ID)
	require.NoError(t, err)
	humanSub, err := w.s.Subscribe(ctx, w.kai, bus.FromNow)
	require.NoError(t, err)
	defer humanSub.Close()

	_, err = w.s.ReportRemark(ctx, scoutActor, m.ID, "still thinking")
	require.ErrorIs(t, err, bus.ErrNotFound)
	_, err = w.s.StartTool(ctx, scoutActor, m.ID, "search", "p", "{}")
	require.ErrorIs(t, err, bus.ErrNotFound)
	require.ErrorIs(t, w.s.EditMessage(ctx, scoutActor, reply.ID, "sneaky", true), bus.ErrNotFound)

	assert.Empty(t, drain(t, humanSub), "nothing reached the room")
	assert.Empty(t, readAllAvailable(humanSub.Edits()))
	got, err := w.s.GetMessage(ctx, w.kai, room.ID, reply.ID)
	require.NoError(t, err)
	assert.Equal(t, "draft", got.Text)
}

func TestSteer_RepeatReturnsTheFirst(t *testing.T) {
	w := newWorld(t)
	room := dmRoom(t, w)
	_, sub := startedTurn(t, w, room)
	defer sub.Close()
	first, created, err := w.s.Steer(ctx, w.kai, room.ID, w.scout.ID, "s1", "also check trains")
	require.NoError(t, err)
	assert.True(t, created)
	assert.Equal(t, "s1", first.ClientID)
	require.Len(t, of(drain(t, sub), bus.EventControlSteer), 1)

	again, created, err := w.s.Steer(ctx, w.kai, room.ID, w.scout.ID, "s1", "something else")
	require.NoError(t, err)
	assert.False(t, created)
	assert.Equal(t, first.ID, again.ID)
	assert.Equal(t, "also check trains", again.Text)
	assert.Empty(t, of(drain(t, sub), bus.EventControlSteer)) // the agent is not steered twice
}

func TestSteer_RepeatAfterTheTurnEnded(t *testing.T) { // Review Focus 1
	w := newWorld(t)
	room := dmRoom(t, w)
	mID, sub := startedTurn(t, w, room)
	defer sub.Close()
	first, _, err := w.s.Steer(ctx, w.kai, room.ID, w.scout.ID, "s1", "also check trains")
	require.NoError(t, err)
	require.NoError(t, w.s.TurnDone(ctx, agentActor(w.scout), mID))

	again, created, err := w.s.Steer(ctx, w.kai, room.ID, w.scout.ID, "s1", "also check trains")
	require.NoError(t, err)
	assert.False(t, created)
	assert.Equal(t, first.ID, again.ID)
	_, _, err = w.s.Steer(ctx, w.kai, room.ID, w.scout.ID, "s2", "also check trains")
	assert.ErrorIs(t, err, bus.ErrNoTurn)
}

func TestSteerAndPostShareAKeySpace(t *testing.T) {
	w := newWorld(t)
	room := dmRoom(t, w)
	_, sub := startedTurn(t, w, room)
	defer sub.Close()
	posted, _, err := w.s.PostMessageOnce(ctx, w.kai, room.ID, "k1", "second", ulid.Zero)
	require.NoError(t, err)
	got, created, err := w.s.Steer(ctx, w.kai, room.ID, w.scout.ID, "k1", "faster")
	require.NoError(t, err)
	assert.False(t, created)
	assert.Equal(t, posted.ID, got.ID)
}

func TestSteer_ClientIDValidation(t *testing.T) {
	w := newWorld(t)
	room := dmRoom(t, w)
	_, sub := startedTurn(t, w, room)
	defer sub.Close()
	_, _, err := w.s.Steer(ctx, w.kai, room.ID, w.scout.ID, "a b", "faster")
	var ve bus.ValidationError
	require.ErrorAs(t, err, &ve)
	assert.Equal(t, `client_id must be at most 64 characters: letters, digits, "-" or "_"`, ve.Message)
}

// streamed has scout post a message in the room and store one non-final edit of it.
func streamed(t *testing.T, w world, room bus.Room, text string) bus.Message {
	t.Helper()
	m, err := w.s.PostMessage(ctx, agentActor(w.scout), room.ID, "…", ulid.Zero)
	require.NoError(t, err)
	require.NoError(t, w.s.EditMessage(ctx, agentActor(w.scout), m.ID, text, false))
	return m
}

func isStreaming(t *testing.T, w world, room bus.Room, id ulid.ULID) bool {
	t.Helper()
	m, err := w.s.GetMessage(ctx, w.kai, room.ID, id)
	require.NoError(t, err)
	return m.Streaming
}

func TestMessage_StreamingFollowsEdits(t *testing.T) {
	w := newWorld(t)
	room := dmRoom(t, w)
	mID, sub := startedTurn(t, w, room)
	defer sub.Close()
	scout := agentActor(w.scout)
	reply, err := w.s.PostMessage(ctx, scout, room.ID, "…", ulid.Zero)
	require.NoError(t, err)
	assert.False(t, reply.Streaming)
	assert.False(t, isStreaming(t, w, room, reply.ID)) // posted in one piece, so far
	assert.False(t, isStreaming(t, w, room, mID))      // a human's message never streams

	require.NoError(t, w.s.EditMessage(ctx, scout, reply.ID, "par", false))
	assert.True(t, isStreaming(t, w, room, reply.ID))
	require.NoError(t, w.s.EditMessage(ctx, scout, reply.ID, "partial answer", true))
	assert.False(t, isStreaming(t, w, room, reply.ID))
}

func TestTurnEnd_ClosesAnAbandonedStream(t *testing.T) { // Review Focus 5
	for name, report := range map[string]func(bus.Service, bus.Actor, ulid.ULID) error{
		"done":   func(s bus.Service, a bus.Actor, id ulid.ULID) error { return s.TurnDone(ctx, a, id) },
		"failed": func(s bus.Service, a bus.Actor, id ulid.ULID) error { return s.TurnFailed(ctx, a, id) },
	} {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			room := dmRoom(t, w)
			mID, sub := startedTurn(t, w, room)
			defer sub.Close()
			abandoned := streamed(t, w, room, "half")
			whole, err := w.s.PostMessage(ctx, agentActor(w.scout), room.ID, "in one piece", ulid.Zero)
			require.NoError(t, err)
			human, err := w.s.Subscribe(ctx, w.kai, bus.FromNow)
			require.NoError(t, err)
			defer human.Close()

			require.NoError(t, report(w.s, agentActor(w.scout), mID))

			got, err := w.s.GetMessage(ctx, w.kai, room.ID, abandoned.ID)
			require.NoError(t, err)
			assert.False(t, got.Streaming)
			assert.Equal(t, "half", got.Text)
			edited := of(drain(t, human), bus.EventMessageEdited)
			require.Len(t, edited, 1) // none for the one-piece message
			assert.Equal(t, abandoned.ID, edited[0].Message.ID)
			assert.False(t, edited[0].Message.Streaming)
			assert.NotEqual(t, whole.ID, edited[0].Message.ID)
		})
	}
}

func TestTurnEnd_LeavesOlderStreamsAlone(t *testing.T) {
	w := newWorld(t)
	room := dmRoom(t, w)
	report := streamed(t, w, room, "a scheduled report, so far") // before the turn's message
	mID, sub := startedTurn(t, w, room)
	defer sub.Close()
	require.NoError(t, w.s.TurnDone(ctx, agentActor(w.scout), mID))
	assert.True(t, isStreaming(t, w, room, report.ID))
}

func TestTurnEnd_AFinalEditStillLands(t *testing.T) {
	w := newWorld(t)
	room := dmRoom(t, w)
	mID, sub := startedTurn(t, w, room)
	defer sub.Close()
	abandoned := streamed(t, w, room, "half")
	require.NoError(t, w.s.TurnDone(ctx, agentActor(w.scout), mID))
	require.NoError(t, w.s.EditMessage(ctx, agentActor(w.scout), abandoned.ID, "whole answer", true))
	got, err := w.s.GetMessage(ctx, w.kai, room.ID, abandoned.ID)
	require.NoError(t, err)
	assert.Equal(t, "whole answer", got.Text)
	assert.False(t, got.Streaming)
}

func TestTurnWorking_ClosesNothing(t *testing.T) {
	w := newWorld(t)
	room := dmRoom(t, w)
	sub := scoutSub(t, w)
	defer sub.Close()
	m, err := w.s.PostMessage(ctx, w.kai, room.ID, "first", ulid.Zero)
	require.NoError(t, err)
	early := streamed(t, w, room, "half")
	require.NoError(t, w.s.TurnWorking(ctx, agentActor(w.scout), m.ID))
	assert.True(t, isStreaming(t, w, room, early.ID))
}

func TestRoom_AgentWorking(t *testing.T) { // Review Focus 6
	w := newWorld(t)
	room := dmRoom(t, w)
	other, err := w.s.CreateRoom(ctx, w.kai, "Other", ids(w.scout))
	require.NoError(t, err)
	working := func(id ulid.ULID) bool {
		r, err := w.s.GetRoom(ctx, w.kai, id)
		require.NoError(t, err)
		return r.Agents[0].Working
	}
	assert.False(t, working(room.ID))

	sub := scoutSub(t, w)
	defer sub.Close()
	m, err := w.s.PostMessage(ctx, w.kai, room.ID, "first", ulid.Zero)
	require.NoError(t, err)
	require.Equal(t, bus.DeliveryDelivered, m.Deliveries[0].State)
	assert.False(t, working(room.ID)) // delivered is not working

	require.NoError(t, w.s.TurnWorking(ctx, agentActor(w.scout), m.ID))
	assert.True(t, working(room.ID))
	assert.False(t, working(other.ID)) // per room

	rooms, err := w.s.ListRooms(ctx, w.kai)
	require.NoError(t, err)
	require.Len(t, rooms, 2)
	for _, r := range rooms {
		assert.Equal(t, r.ID == room.ID, r.Agents[0].Working)
	}

	require.NoError(t, w.s.TurnDone(ctx, agentActor(w.scout), m.ID))
	assert.False(t, working(room.ID))
}

// Edits reach browsers at once but the row at most once a second, so the close
// must keep what people last saw, not what was last stored.
func TestTurnEnd_ClosesWithTheLastTextBroadcast(t *testing.T) {
	w := newWorld(t)
	room := dmRoom(t, w)
	mID, sub := startedTurn(t, w, room)
	defer sub.Close()
	reply := streamed(t, w, room, "half") // stored
	require.NoError(t, w.s.EditMessage(ctx, agentActor(w.scout), reply.ID, "half and a bit more", false))
	row, err := w.r.GetMessage(ctx, reply.ID.String())
	require.NoError(t, err)
	require.Equal(t, "half", row.Body) // the second edit came inside the throttle: broadcast, not stored

	require.NoError(t, w.s.TurnFailed(ctx, agentActor(w.scout), mID))
	got, err := w.s.GetMessage(ctx, w.kai, room.ID, reply.ID)
	require.NoError(t, err)
	assert.Equal(t, "half and a bit more", got.Text)
	assert.False(t, got.Streaming)
}

func TestTurnEnd_ClosesOnlyItsOwnAgentAndRoom(t *testing.T) {
	w := newWorld(t)
	room, scoutS, atlasS := connectedChannel(t, w)
	defer scoutS.Close()
	defer atlasS.Close()
	dm := dmRoom(t, w)
	turn, err := w.s.PostMessage(ctx, w.kai, room.ID, "@scout go", ulid.Zero)
	require.NoError(t, err)
	require.NoError(t, w.s.TurnWorking(ctx, agentActor(w.scout), turn.ID))
	mine := streamed(t, w, room, "scout, in the turn's room")
	elsewhere := streamed(t, w, dm, "scout, in another room")
	theirs, err := w.s.PostMessage(ctx, agentActor(w.atlas), room.ID, "…", ulid.Zero)
	require.NoError(t, err)
	require.NoError(t, w.s.EditMessage(ctx, agentActor(w.atlas), theirs.ID, "atlas, in the turn's room", false))

	require.NoError(t, w.s.TurnDone(ctx, agentActor(w.scout), turn.ID))
	assert.False(t, isStreaming(t, w, room, mine.ID))
	assert.True(t, isStreaming(t, w, dm, elsewhere.ID))
	assert.True(t, isStreaming(t, w, room, theirs.ID))
}

func TestTurnEnd_ClosesAStreamInAPausedRoom(t *testing.T) {
	w := newWorld(t)
	room := dmRoom(t, w)
	mID, sub := startedTurn(t, w, room)
	defer sub.Close()
	reply := streamed(t, w, room, "half")
	_, err := w.s.PauseRoom(ctx, w.kai, room.ID)
	require.NoError(t, err)
	require.NoError(t, w.s.TurnDone(ctx, agentActor(w.scout), mID)) // the plugin reports the stop as a finished turn
	assert.False(t, isStreaming(t, w, room, reply.ID))
}

func TestEditMessage_NonFinalAfterTheCloseChangesNothing(t *testing.T) {
	w := newWorld(t)
	room := dmRoom(t, w)
	mID, sub := startedTurn(t, w, room)
	defer sub.Close()
	reply := streamed(t, w, room, "half")
	require.NoError(t, w.s.TurnDone(ctx, agentActor(w.scout), mID))
	require.NoError(t, w.s.EditMessage(ctx, agentActor(w.scout), reply.ID, "late", false))
	got, err := w.s.GetMessage(ctx, w.kai, room.ID, reply.ID)
	require.NoError(t, err)
	assert.Equal(t, "half", got.Text)
	assert.False(t, got.Streaming)
}
