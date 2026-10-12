package bus_test

import (
	"strings"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/bus"
)

var approvalChoices = []string{"once", "session", "always", "deny"}

// asked has scout ask kai for an approval in their DM, with scout and kai both streaming.
func asked(t *testing.T, w world) (room bus.Room, m bus.Message, agent, human *bus.Subscription) {
	t.Helper()
	room = dmRoom(t, w)
	agent = agentSub(t, w, w.scout)
	var err error
	human, err = w.s.Subscribe(ctx, w.kai, bus.FromNow)
	require.NoError(t, err)
	t.Cleanup(human.Close)
	m, err = w.s.PostPrompt(ctx, agentActor(w.scout), room.ID, bus.PromptApproval, "run `rm -rf build`?", approvalChoices)
	require.NoError(t, err)
	return
}

func TestPostPrompt_IsAMessageWithABadge(t *testing.T) {
	w := newWorld(t)
	room, m, agent, human := asked(t, w)
	require.NotNil(t, m.Prompt)
	assert.Equal(t, bus.Prompt{Kind: bus.PromptApproval, Choices: approvalChoices, State: bus.PromptOpen}, *m.Prompt)
	assert.Equal(t, w.scout.ID, m.Author.ID)
	assert.Empty(t, m.Deliveries, "a prompt wakes nobody")

	evs := drain(t, human)
	require.Len(t, of(evs, bus.EventMessagePosted), 1)
	assert.Equal(t, bus.PromptOpen, of(evs, bus.EventMessagePosted)[0].Message.Prompt.State)
	rooms := of(evs, bus.EventRoomChanged)
	require.NotEmpty(t, rooms)
	assert.Equal(t, 1, rooms[len(rooms)-1].Room.OpenPrompts)
	assert.Empty(t, of(drain(t, agent), bus.EventPromptAnswered))

	got, err := w.s.GetRoom(ctx, w.kai, room.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, got.OpenPrompts)
	list, _ := w.s.ListMessages(ctx, w.kai, room.ID, ulid.Zero, 0)
	require.Len(t, list, 1)
	assert.Equal(t, m.ID, list[0].ID)
	require.NotNil(t, list[0].Prompt)
}

func TestPostPrompt_Refusals(t *testing.T) {
	w := newWorld(t)
	room := channelRoom(t, w)
	scout := agentActor(w.scout)
	_, err := w.s.PostPrompt(ctx, w.kai, room.ID, bus.PromptApproval, "x", approvalChoices)
	assert.ErrorIs(t, err, bus.ErrForbidden)
	stranger, _, _ := w.s.CreateAgent(ctx, "ghost", "Ghost")
	_, err = w.s.PostPrompt(ctx, agentActor(stranger), room.ID, bus.PromptApproval, "x", approvalChoices)
	assert.ErrorIs(t, err, bus.ErrNotFound)

	long := strings.Repeat("a", 201)
	for name, c := range map[string]struct {
		kind    bus.PromptKind
		text    string
		choices []string
	}{
		"kind":             {"poll", "x", nil},
		"blank text":       {bus.PromptClarify, "  ", nil},
		"no approvals":     {bus.PromptApproval, "x", nil},
		"foreign approval": {bus.PromptApproval, "x", []string{"once", "maybe"}},
		"repeat approval":  {bus.PromptApproval, "x", []string{"once", "once"}},
		"seven choices":    {bus.PromptClarify, "x", []string{"1", "2", "3", "4", "5", "6", "7"}},
		"blank choice":     {bus.PromptClarify, "x", []string{"a", " "}},
		"long choice":      {bus.PromptClarify, "x", []string{long}},
		"repeat choice":    {bus.PromptClarify, "x", []string{"a", " a "}},
	} {
		_, err := w.s.PostPrompt(ctx, scout, room.ID, c.kind, c.text, c.choices)
		assert.ErrorIs(t, err, bus.ErrValidation, name)
	}
	open, err := w.s.PostPrompt(ctx, scout, room.ID, bus.PromptClarify, "which @atlas city?", nil)
	require.NoError(t, err, "a clarifying question needs no choices")
	assert.Equal(t, []string{}, open.Prompt.Choices)
	assert.Empty(t, open.Deliveries, "a mention in a prompt wakes nobody")

	_, err = w.s.PauseRoom(ctx, w.kai, room.ID)
	require.NoError(t, err)
	_, err = w.s.PostPrompt(ctx, scout, room.ID, bus.PromptApproval, "x", approvalChoices)
	assert.ErrorIs(t, err, bus.ErrPaused)
	_, _ = w.s.ResumeRoom(ctx, w.kai, room.ID)
	_, err = w.s.UpdateRoom(ctx, w.kai, room.ID, bus.RoomUpdate{Archived: ptr(true)})
	require.NoError(t, err)
	_, err = w.s.PostPrompt(ctx, scout, room.ID, bus.PromptApproval, "x", approvalChoices)
	assert.ErrorIs(t, err, bus.ErrRoomArchived)
}

func TestAnswerPrompt_ReachesTheAgent(t *testing.T) {
	w := newWorld(t)
	room, m, agent, human := asked(t, w)
	drain(t, human)
	*w.now = fixed.Add(time.Minute)

	_, err := w.s.AnswerPrompt(ctx, w.kai, room.ID, m.ID, "maybe")
	assert.ErrorIs(t, err, bus.ErrValidation)
	_, err = w.s.AnswerPrompt(ctx, w.ada, room.ID, m.ID, "once")
	assert.ErrorIs(t, err, bus.ErrNotFound, "ada is not in the room")
	_, err = w.s.AnswerPrompt(ctx, agentActor(w.scout), room.ID, m.ID, "once")
	assert.ErrorIs(t, err, bus.ErrForbidden)

	got, err := w.s.AnswerPrompt(ctx, w.kai, room.ID, m.ID, "once")
	require.NoError(t, err)
	assert.Equal(t, bus.PromptAnswered, got.Prompt.State)
	assert.Equal(t, "once", got.Prompt.Answer)
	assert.Equal(t, w.kai.ID, got.Prompt.AnsweredBy.ID)
	assert.Equal(t, fixed.Add(time.Minute), got.Prompt.ClosedAt)

	answered := of(drain(t, agent), bus.EventPromptAnswered)
	require.Len(t, answered, 1)
	assert.Equal(t, m.ID, answered[0].Message.ID)
	assert.Equal(t, "once", answered[0].Message.Prompt.Answer)
	evs := drain(t, human)
	require.Len(t, of(evs, bus.EventPromptChanged), 1)
	assert.Equal(t, 0, of(evs, bus.EventRoomChanged)[0].Room.OpenPrompts)
}

func TestAnswerPrompt_ClarifyTakesAChoiceOrFreeText(t *testing.T) {
	w := newWorld(t)
	room := dmRoom(t, w)
	ask := func() bus.Message {
		m, err := w.s.PostPrompt(ctx, agentActor(w.scout), room.ID, bus.PromptClarify, "which city?", []string{"Lisbon", "Porto"})
		require.NoError(t, err)
		return m
	}
	for _, bad := range []string{"", "   ", strings.Repeat("a", 2001), "a\x00b"} {
		_, err := w.s.AnswerPrompt(ctx, w.kai, room.ID, ask().ID, bad)
		assert.ErrorIs(t, err, bus.ErrValidation, "%q", bad)
	}
	got, err := w.s.AnswerPrompt(ctx, w.kai, room.ID, ask().ID, "  Faro, actually ")
	require.NoError(t, err)
	assert.Equal(t, "Faro, actually", got.Prompt.Answer)
}

// Review Focus 1.
func TestAnswerPrompt_TwiceAndAfterExpiry(t *testing.T) {
	w := newWorld(t)
	room, m, agent, _ := asked(t, w)
	both := shared(t, w) // kai and ada with scout
	m2, err := w.s.PostPrompt(ctx, agentActor(w.scout), both.ID, bus.PromptApproval, "x", approvalChoices)
	require.NoError(t, err)

	_, err = w.s.AnswerPrompt(ctx, w.kai, room.ID, m.ID, "once")
	require.NoError(t, err)
	again, err := w.s.AnswerPrompt(ctx, w.kai, room.ID, m.ID, "once")
	require.NoError(t, err, "a double tap is quiet")
	assert.Equal(t, "once", again.Prompt.Answer)
	_, err = w.s.AnswerPrompt(ctx, w.kai, room.ID, m.ID, "deny")
	assert.ErrorIs(t, err, bus.ErrPromptClosed)
	assert.Len(t, of(drain(t, agent), bus.EventPromptAnswered), 1, "the agent is told once")
	assert.NoError(t, w.s.ExpirePrompt(ctx, agentActor(w.scout), m.ID), "expiring an answered prompt changes nothing")
	got, _ := w.s.GetMessage(ctx, w.kai, room.ID, m.ID)
	assert.Equal(t, bus.PromptAnswered, got.Prompt.State)

	// two humans: the first answer stands
	_, err = w.s.AnswerPrompt(ctx, w.ada, both.ID, m2.ID, "deny")
	require.NoError(t, err)
	_, err = w.s.AnswerPrompt(ctx, w.kai, both.ID, m2.ID, "always")
	assert.ErrorIs(t, err, bus.ErrPromptClosed)

	// the plugin gave up first
	m3, _ := w.s.PostPrompt(ctx, agentActor(w.scout), room.ID, bus.PromptApproval, "y", approvalChoices)
	require.NoError(t, w.s.ExpirePrompt(ctx, agentActor(w.scout), m3.ID))
	require.NoError(t, w.s.ExpirePrompt(ctx, agentActor(w.scout), m3.ID))
	_, err = w.s.AnswerPrompt(ctx, w.kai, room.ID, m3.ID, "once")
	assert.ErrorIs(t, err, bus.ErrPromptClosed)
	assert.ErrorIs(t, w.s.ExpirePrompt(ctx, agentActor(w.atlas), m3.ID), bus.ErrNotFound, "not atlas's prompt")
	plain, _ := w.s.PostMessage(ctx, agentActor(w.scout), room.ID, "no question", ulid.Zero)
	assert.ErrorIs(t, w.s.ExpirePrompt(ctx, agentActor(w.scout), plain.ID), bus.ErrNotFound)
	_, err = w.s.AnswerPrompt(ctx, w.kai, room.ID, plain.ID, "once")
	assert.ErrorIs(t, err, bus.ErrNotFound)
}

func TestPrompts_ExpireWhenTheRoomCloses(t *testing.T) {
	for name, closeRoom := range map[string]func(w world, room bus.Room){
		"pause":   func(w world, room bus.Room) { _, _ = w.s.PauseRoom(ctx, w.kai, room.ID) },
		"halt":    func(w world, room bus.Room) { _ = w.s.StopEverything(ctx, w.kai) },
		"archive": func(w world, room bus.Room) { _, _ = w.s.UpdateRoom(ctx, w.kai, room.ID, bus.RoomUpdate{Archived: ptr(true)}) },
		"removal": func(w world, room bus.Room) { _, _ = w.s.RemoveRoomAgent(ctx, w.kai, room.ID, w.scout.ID) },
	} {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			room := channelRoom(t, w)
			human, _ := w.s.Subscribe(ctx, w.kai, bus.FromNow)
			defer human.Close()
			mine, _ := w.s.PostPrompt(ctx, agentActor(w.scout), room.ID, bus.PromptApproval, "x", approvalChoices)
			theirs, _ := w.s.PostPrompt(ctx, agentActor(w.atlas), room.ID, bus.PromptApproval, "y", approvalChoices)
			drain(t, human)

			closeRoom(w, room)
			got, _ := w.s.GetMessage(ctx, w.kai, room.ID, mine.ID)
			assert.Equal(t, bus.PromptExpired, got.Prompt.State)
			other, _ := w.s.GetMessage(ctx, w.kai, room.ID, theirs.ID)
			wantOther := bus.PromptExpired
			if name == "removal" {
				wantOther = bus.PromptOpen // only the removed agent's prompts go
			}
			assert.Equal(t, wantOther, other.Prompt.State)
			changed := of(drain(t, human), bus.EventPromptChanged)
			assert.NotEmpty(t, changed)
			_, err := w.s.AnswerPrompt(ctx, w.kai, room.ID, mine.ID, "once")
			assert.ErrorIs(t, err, bus.ErrPromptClosed)
		})
	}
}

// The gate holds on its own, without the expiry: a room closed behind the bus's back.
func TestAnswerPrompt_ClosedRoomRefusesAndHandsNothingOver(t *testing.T) {
	w := newWorld(t)
	room, m, _, _ := asked(t, w)
	m2, _ := w.s.PostPrompt(ctx, agentActor(w.scout), room.ID, bus.PromptApproval, "second", approvalChoices)
	_, err := w.s.AnswerPrompt(ctx, w.kai, room.ID, m.ID, "once") // answered while open, not yet read
	require.NoError(t, err)
	at := fixed.UnixMilli()
	require.NoError(t, w.r.SetRoomPause(ctx, room.ID.String(), &at, "human"))

	_, err = w.s.AnswerPrompt(ctx, w.kai, room.ID, m2.ID, "once")
	assert.ErrorIs(t, err, bus.ErrPaused)
	late, err := w.s.Subscribe(ctx, agentActor(w.scout), 0) // a reconnect from the start of the log
	require.NoError(t, err)
	defer late.Close()
	assert.Empty(t, of(drain(t, late), bus.EventPromptAnswered), "a closed room hands over no answer")
}