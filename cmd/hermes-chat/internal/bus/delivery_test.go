package bus_test

import (
	"context"
	"testing"

	"github.com/oklog/ulid/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/bus"
	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/storage"
)

// dmRoom is the human kai's DM with scout.
func dmRoom(t *testing.T, w world) bus.Room {
	t.Helper()
	room, _, err := w.s.OpenDM(ctx, w.kai, w.scout.ID)
	require.NoError(t, err)
	return room
}

// channelRoom is a topic room holding human kai and two agents, atlas and scout.
func channelRoom(t *testing.T, w world) bus.Room {
	t.Helper()
	room, err := w.s.CreateRoom(ctx, w.kai, "Channel", ids(w.atlas, w.scout))
	require.NoError(t, err)
	return room
}

// convRoom is a topic room with one agent, scout.
func convRoom(t *testing.T, w world) bus.Room {
	t.Helper()
	room, err := w.s.CreateRoom(ctx, w.kai, "Conversation", ids(w.scout))
	require.NoError(t, err)
	return room
}

func TestMentionedAgents(t *testing.T) {
	w := newWorld(t)
	mention := func(text string, members []bus.Agent) []bus.Agent {
		return w.s.(interface{ MentionedAgents(string, []bus.Agent) []bus.Agent }).MentionedAgents(text, members)
	}
	members := []bus.Agent{w.atlas, w.scout} // handle order
	cases := []struct {
		text string
		want []bus.Agent
	}{
		{"@scout", []bus.Agent{w.scout}},
		{"@scout,", []bus.Agent{w.scout}},
		{"(@scout)", []bus.Agent{w.scout}},
		{"@SCOUT", []bus.Agent{w.scout}},
		{"@scout please", []bus.Agent{w.scout}},
		{"@atlas @scout", []bus.Agent{w.atlas, w.scout}},
		{"@scout @atlas", []bus.Agent{w.atlas, w.scout}}, // handle order, not mention order
		{"@scout @scout", []bus.Agent{w.scout}},          // each at most once
		{"hi@scout", []bus.Agent{}},                      // a trailing @ in a word is not a mention
		{"@sc", []bus.Agent{}},                           // a prefix is not a handle
		{"@ghost hi", []bus.Agent{}},                     // a non-member handle wakes nobody
		{"no mention", []bus.Agent{}},
		{"", []bus.Agent{}},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, mention(c.text, members), "%.10q", c.text)
	}
	// a handle naming a non-member wakes nobody
	assert.Empty(t, mention("@atlas", []bus.Agent{w.scout}))
}

func TestWakeAgents(t *testing.T) {
	w := newWorld(t)
	wake := func(author bus.Actor, flavor bus.RoomFlavor, text string, reply *storage.Message, members []bus.Agent) []bus.Agent {
		return w.s.(interface {
			WakeAgents(bus.Actor, bus.RoomFlavor, string, *storage.Message, []bus.Agent) []bus.Agent
		}).WakeAgents(author, flavor, text, reply, members)
	}
	scoutID := w.scout.ID.String()
	kaiID := w.kai.ID.String()
	humanReply := &storage.Message{AuthorHumanID: &kaiID}
	agentReply := &storage.Message{AuthorAgentID: &scoutID}
	dm := []bus.Agent{w.scout}          // a dm or conversation fixture: human + scout
	ch := []bus.Agent{w.atlas, w.scout} // a channel fixture: human + scout + atlas
	cases := []struct {
		name    string
		author  bus.Actor
		flavor  bus.RoomFlavor
		text    string
		reply   *storage.Message
		members []bus.Agent
		want    []bus.Agent
	}{
		{"human dm", w.kai, bus.FlavorDM, "anything", nil, dm, []bus.Agent{w.scout}},
		{"human conversation", w.kai, bus.FlavorConversation, "anything", nil, dm, []bus.Agent{w.scout}},
		{"human channel mention", w.kai, bus.FlavorChannel, "@scout please", nil, ch, []bus.Agent{w.scout}},
		{"human channel both", w.kai, bus.FlavorChannel, "@atlas @scout please", nil, ch, []bus.Agent{w.atlas, w.scout}},
		{"human channel plain replying to agent", w.kai, bus.FlavorChannel, "plain", agentReply, ch, []bus.Agent{w.scout}},
		{"human channel plain replying to human", w.kai, bus.FlavorChannel, "plain", humanReply, ch, []bus.Agent{}},
		{"scout channel mentions", bus.Actor{Kind: bus.ActorAgent, ID: w.scout.ID}, bus.FlavorChannel, "@atlas look", nil, ch, []bus.Agent{w.atlas}},
		{"scout channel plain replying to human", bus.Actor{Kind: bus.ActorAgent, ID: w.scout.ID}, bus.FlavorChannel, "plain", humanReply, ch, []bus.Agent{}},
		{"scout channel self mention", bus.Actor{Kind: bus.ActorAgent, ID: w.scout.ID}, bus.FlavorChannel, "@scout note to self", nil, ch, []bus.Agent{}},
		{"human channel ghost", w.kai, bus.FlavorChannel, "@ghost hi", nil, ch, []bus.Agent{}},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, wake(c.author, c.flavor, c.text, c.reply, c.members), "%s", c.name)
	}
}

func TestPostMessage_WakesAndQueues(t *testing.T) {
	w := newWorld(t)
	dm := dmRoom(t, w)
	conv := convRoom(t, w)
	ch := channelRoom(t, w)
	// seeds for the reply rows; a plain post wakes nobody, so they add no deliveries.
	scouts, err := w.s.PostMessage(ctx, bus.Actor{Kind: bus.ActorAgent, ID: w.scout.ID}, ch.ID, "scout seed", ulid.Zero)
	require.NoError(t, err)
	humans, err := w.s.PostMessage(ctx, w.kai, ch.ID, "human seed", ulid.Zero)
	require.NoError(t, err)
	_ = scouts
	_ = humans

	cases := []struct {
		name    string
		actor   bus.Actor
		room    bus.Room
		text    string
		replyTo ulid.ULID
		want    []bus.Delivery
	}{
		{"human dm", w.kai, dm, "anything", ulid.Zero, []bus.Delivery{{Agent: w.scout, State: bus.DeliveryWaiting}}},
		{"human conversation", w.kai, conv, "anything", ulid.Zero, []bus.Delivery{{Agent: w.scout, State: bus.DeliveryWaiting}}},
		{"human channel mention", w.kai, ch, "@scout please", ulid.Zero, []bus.Delivery{{Agent: w.scout, State: bus.DeliveryWaiting}}},
		{"human channel both", w.kai, ch, "@atlas @scout please", ulid.Zero, []bus.Delivery{{Agent: w.atlas, State: bus.DeliveryWaiting}, {Agent: w.scout, State: bus.DeliveryWaiting}}},
		{"human channel reply to agent", w.kai, ch, "plain", scouts.ID, []bus.Delivery{{Agent: w.scout, State: bus.DeliveryWaiting}}},
		{"human channel reply to human", w.kai, ch, "plain", humans.ID, []bus.Delivery{}},
		{"scout channel mentions", bus.Actor{Kind: bus.ActorAgent, ID: w.scout.ID}, ch, "@atlas look", ulid.Zero, []bus.Delivery{{Agent: w.atlas, State: bus.DeliveryWaiting}}},
		{"scout channel reply to human", bus.Actor{Kind: bus.ActorAgent, ID: w.scout.ID}, ch, "plain", humans.ID, []bus.Delivery{}},
		{"scout channel self mention", bus.Actor{Kind: bus.ActorAgent, ID: w.scout.ID}, ch, "@scout note to self", ulid.Zero, []bus.Delivery{}},
		{"human channel ghost", w.kai, ch, "@ghost hi", ulid.Zero, []bus.Delivery{}},
	}
	for _, c := range cases {
		m, err := w.s.PostMessage(ctx, c.actor, c.room.ID, c.text, c.replyTo)
		require.NoError(t, err, "%s", c.name)
		assert.Equal(t, c.want, m.Deliveries, "%s", c.name)
		listed, err := w.s.ListMessages(ctx, w.kai, c.room.ID, ulid.Zero, 0)
		require.NoError(t, err, "%s", c.name)
		found := false
		for _, mm := range listed {
			found = found || mm.ID == m.ID
		}
		assert.True(t, found, "%s: the message is listed", c.name)
	}
}

func TestPostMessage_ReleaseWhenConnected(t *testing.T) {
	w := newWorld(t)
	room := dmRoom(t, w)
	sub, err := w.s.Subscribe(ctx, bus.Actor{Kind: bus.ActorAgent, ID: w.scout.ID}, bus.FromNow)
	require.NoError(t, err)
	defer sub.Close()

	first, err := w.s.PostMessage(ctx, w.kai, room.ID, "first", ulid.Zero)
	require.NoError(t, err)
	assert.Equal(t, []bus.Delivery{{Agent: w.scout, State: bus.DeliveryDelivered}}, first.Deliveries)
	events := drain(t, sub)
	require.Len(t, events, 1)
	assert.Equal(t, bus.EventDelivery, events[0].Kind)
	require.NotNil(t, events[0].Message)
	assert.Equal(t, first.ID, events[0].Message.ID)
	require.NotNil(t, events[0].Room)
	assert.Equal(t, room.ID, events[0].Room.ID)

	second, err := w.s.PostMessage(ctx, w.kai, room.ID, "second", ulid.Zero)
	require.NoError(t, err)
	assert.Equal(t, []bus.Delivery{{Agent: w.scout, State: bus.DeliveryWaiting}}, second.Deliveries, "one at a time")
	assert.Empty(t, drain(t, sub), "no second delivery while the first is not done")

	sub.Close() // scout is offline again
	conv := convRoom(t, w)
	offline, err := w.s.PostMessage(ctx, w.kai, conv.ID, "offline", ulid.Zero)
	require.NoError(t, err)
	assert.Equal(t, []bus.Delivery{{Agent: w.scout, State: bus.DeliveryWaiting}}, offline.Deliveries, "sending to an offline agent shows waiting")
}

func TestPostMessage_AgentAuthorAndChannelContext(t *testing.T) {
	w := newWorld(t)
	ch := channelRoom(t, w)
	seed, err := w.s.PostMessage(ctx, bus.Actor{Kind: bus.ActorAgent, ID: w.scout.ID}, ch.ID, "scout plain", ulid.Zero)
	require.NoError(t, err)

	human, err := w.s.Subscribe(ctx, w.kai, bus.FromNow)
	require.NoError(t, err)
	defer human.Close()
	agent, err := w.s.Subscribe(ctx, bus.Actor{Kind: bus.ActorAgent, ID: w.scout.ID}, bus.FromNow)
	require.NoError(t, err)
	defer agent.Close()
	_ = drain(t, human) // scout's presence-on row for the channel room

	m, err := w.s.PostMessage(ctx, w.kai, ch.ID, "@scout go", ulid.Zero)
	require.NoError(t, err)
	assert.Equal(t, []bus.Delivery{{Agent: w.scout, State: bus.DeliveryDelivered}}, m.Deliveries)

	hev := drain(t, human)
	require.Len(t, hev, 1, "the human's own message.posted still arrives")
	assert.Equal(t, bus.EventMessagePosted, hev[0].Kind)
	assert.Equal(t, m.ID, hev[0].Message.ID)

	aev := drain(t, agent)
	require.Len(t, aev, 1)
	assert.Equal(t, bus.EventDelivery, aev[0].Kind)
	require.NotNil(t, aev[0].Message)
	assert.Equal(t, m.ID, aev[0].Message.ID)
	require.Len(t, aev[0].Context, 1, "the channel context holds scout's earlier message and nothing else")
	assert.Equal(t, seed.ID, aev[0].Context[0].ID)
	assert.Equal(t, "scout plain", aev[0].Context[0].Text)

	// the agent-authored message is visible to the human with its kind
	listed, err := w.s.ListMessages(ctx, w.kai, ch.ID, ulid.Zero, 0)
	require.NoError(t, err)
	for i := range listed {
		if listed[i].ID == seed.ID {
			require.NotNil(t, listed[i].Author)
			assert.Equal(t, bus.ActorAgent, listed[i].Author.Kind)
			assert.Equal(t, "Scout", listed[i].Author.Name)
		}
	}

	dm := dmRoom(t, w)
	dmSub, err := w.s.Subscribe(ctx, bus.Actor{Kind: bus.ActorAgent, ID: w.scout.ID}, bus.FromNow)
	require.NoError(t, err)
	defer dmSub.Close()
	_, err = w.s.PostMessage(ctx, w.kai, dm.ID, "dm message", ulid.Zero)
	require.NoError(t, err)
	// The new stream named no cursor while the channel delivery was still
	// unacknowledged, so that one is handed over again ahead of the DM's.
	dev := drain(t, dmSub)
	require.Len(t, dev, 2)
	assert.Equal(t, m.ID, dev[0].Message.ID)
	assert.Equal(t, bus.EventDelivery, dev[1].Kind)
	assert.Nil(t, dev[1].Context, "a non-channel has no context")
}

// TestCancelMessage (Review Focus 1). A conversation so a second human can be a
// member who is not the author.
func TestCancelMessage(t *testing.T) {
	w := newWorld(t)
	room, err := w.s.CreateRoom(ctx, w.kai, "Conv", ids(w.scout))
	require.NoError(t, err)
	_, err = w.s.InviteHuman(ctx, w.kai, room.ID, w.ada.ID)
	require.NoError(t, err)
	scoutID := w.scout.ID.String()
	// scout is offline; every delivery stays waiting
	m1, err := w.s.PostMessage(ctx, w.kai, room.ID, "one", ulid.Zero)
	require.NoError(t, err)
	m2, err := w.s.PostMessage(ctx, w.kai, room.ID, "two", ulid.Zero)
	require.NoError(t, err)
	m3, err := w.s.PostMessage(ctx, w.kai, room.ID, "three", ulid.Zero)
	require.NoError(t, err)

	deliveries := func(mid ulid.ULID) []bus.Delivery {
		m, err := w.s.GetMessage(ctx, w.kai, room.ID, mid)
		require.NoError(t, err)
		return m.Deliveries
	}
	// a member who is not the author is forbidden
	assert.ErrorIs(t, w.s.CancelMessage(ctx, w.ada, room.ID, m1.ID), bus.ErrForbidden)
	// a room the caller is not in looks missing
	assert.ErrorIs(t, w.s.CancelMessage(ctx, w.kai, ulid.Make(), m1.ID), bus.ErrNotFound)
	// an agent is never a canceller
	assert.ErrorIs(t, w.s.CancelMessage(ctx, bus.Actor{Kind: bus.ActorAgent, ID: w.scout.ID}, room.ID, m1.ID), bus.ErrForbidden)

	require.NoError(t, w.s.CancelMessage(ctx, w.kai, room.ID, m2.ID))
	assert.Equal(t, []bus.Delivery{{Agent: w.scout, State: bus.DeliveryCancelled}}, deliveries(m2.ID))
	assert.Equal(t, []bus.Delivery{{Agent: w.scout, State: bus.DeliveryWaiting}}, deliveries(m1.ID), "m1 is untouched")
	assert.Equal(t, []bus.Delivery{{Agent: w.scout, State: bus.DeliveryWaiting}}, deliveries(m3.ID), "m3 is untouched")

	sub, err := w.s.Subscribe(ctx, bus.Actor{Kind: bus.ActorAgent, ID: w.scout.ID}, bus.FromNow)
	require.NoError(t, err)
	defer sub.Close()
	sweep := w.s.(interface{ ReleaseSweepForTest(context.Context, ulid.ULID) error })
	require.NoError(t, sweep.ReleaseSweepForTest(ctx, w.scout.ID))
	events := drain(t, sub)
	require.Len(t, events, 1, "the sweep delivers the head, never the cancelled message")
	assert.Equal(t, bus.EventDelivery, events[0].Kind)
	assert.Equal(t, m1.ID, events[0].Message.ID)

	require.NoError(t, w.r.SetDeliveryState(ctx, room.ID.String(), scoutID, m1.ID.String(), string(bus.DeliveryDone)))
	require.NoError(t, sweep.ReleaseSweepForTest(ctx, w.scout.ID))
	events = drain(t, sub)
	require.Len(t, events, 1)
	assert.Equal(t, m3.ID, events[0].Message.ID, "m3 follows once m1 is done")

	// m1 is no longer waiting, so cancelling it finds nothing
	assert.ErrorIs(t, w.s.CancelMessage(ctx, w.kai, room.ID, m1.ID), bus.ErrNothingWaiting)
}

func TestRoomAgents_ShowConnection(t *testing.T) {
	w := newWorld(t)
	room := dmRoom(t, w)
	connected := func() bool {
		got, err := w.s.GetRoom(ctx, w.kai, room.ID)
		require.NoError(t, err)
		require.Len(t, got.Agents, 1)
		return got.Agents[0].Connected
	}
	assert.False(t, connected())
	sub, err := w.s.Subscribe(ctx, bus.Actor{Kind: bus.ActorAgent, ID: w.scout.ID}, bus.FromNow)
	require.NoError(t, err)
	assert.True(t, connected())
	sub.Close()
	assert.False(t, connected())
}