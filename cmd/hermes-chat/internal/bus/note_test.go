package bus_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/oklog/ulid/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/quells-bot/hermes-chat/cmd/hermes-chat/internal/bus"
)

func TestNote_TextAndCaps(t *testing.T) {
	w := newWorld(t)
	n, err := w.s.GetNote(ctx, w.kai)
	require.NoError(t, err)
	assert.Equal(t, bus.Note{Text: "", Entries: []bus.NoteEntry{}}, n)
	_, err = w.s.GetNote(ctx, agentActor(w.scout))
	assert.ErrorIs(t, err, bus.ErrForbidden)

	n, err = w.s.SetNoteText(ctx, w.kai, "  I live in Lisbon. ")
	require.NoError(t, err)
	assert.Equal(t, "I live in Lisbon.", n.Text)
	_, err = w.s.SetNoteText(ctx, w.kai, strings.Repeat("é", 2000))
	assert.NoError(t, err, "the cap counts characters, not bytes")
	_, err = w.s.SetNoteText(ctx, w.kai, strings.Repeat("a", 2001))
	assert.ErrorIs(t, err, bus.ErrValidation)
	_, err = w.s.SetNoteText(ctx, w.kai, "a\x00b")
	assert.ErrorIs(t, err, bus.ErrValidation)
	n, _ = w.s.SetNoteText(ctx, w.kai, "")
	assert.Equal(t, "", n.Text)
	theirs, _ := w.s.GetNote(ctx, w.ada)
	assert.Equal(t, "", theirs.Text, "ada never sees kai's note")
}

// suggest has scout, mid-turn on kai's message, suggest an entry.
func suggest(t *testing.T, w world, messageID ulid.ULID, text string) bus.NoteEntry {
	t.Helper()
	e, _, err := w.s.SuggestNoteEntry(ctx, agentActor(w.scout), messageID, text)
	require.NoError(t, err)
	return e
}

func TestNoteEntries_QuarantineApproveEditDelete(t *testing.T) {
	w := newWorld(t)
	m, _ := startedTurn(t, w, dmRoom(t, w))
	e := suggest(t, w, m, "  prefers trains ")
	assert.Equal(t, "prefers trains", e.Text)
	assert.False(t, e.Approved)
	assert.Equal(t, w.scout.ID, e.Agent.ID)
	assert.Equal(t, fixed, e.CreatedAt)

	_, err := w.s.UpdateNoteEntry(ctx, w.ada, e.ID, bus.NoteEntryUpdate{Approved: ptr(true)})
	assert.ErrorIs(t, err, bus.ErrNotFound, "not ada's entry")
	_, err = w.s.DeleteNoteEntry(ctx, w.ada, e.ID)
	assert.ErrorIs(t, err, bus.ErrNotFound)

	n, err := w.s.UpdateNoteEntry(ctx, w.kai, e.ID, bus.NoteEntryUpdate{Text: ptr("prefers night trains"), Approved: ptr(true)})
	require.NoError(t, err)
	require.Len(t, n.Entries, 1)
	assert.True(t, n.Entries[0].Approved)
	assert.Equal(t, "prefers night trains", n.Entries[0].Text)
	assert.Equal(t, w.scout.ID, n.Entries[0].Agent.ID, "an edited entry keeps who added it")
	_, err = w.s.UpdateNoteEntry(ctx, w.kai, e.ID, bus.NoteEntryUpdate{Text: ptr(strings.Repeat("a", 301))})
	assert.ErrorIs(t, err, bus.ErrValidation)
	n, _ = w.s.UpdateNoteEntry(ctx, w.kai, e.ID, bus.NoteEntryUpdate{Approved: ptr(false)})
	assert.False(t, n.Entries[0].Approved)
	n, err = w.s.DeleteNoteEntry(ctx, w.kai, e.ID)
	require.NoError(t, err)
	assert.Empty(t, n.Entries)
}

func TestNote_ApprovedTotalIsCapped(t *testing.T) {
	w := newWorld(t)
	m, _ := startedTurn(t, w, dmRoom(t, w))
	_, err := w.s.SetNoteText(ctx, w.kai, strings.Repeat("a", 2000))
	require.NoError(t, err)
	var last bus.NoteEntry
	for i := range 7 { // 2000 + 6×300 = 3800; the seventh would make 4100
		last = suggest(t, w, m, strings.Repeat("b", 299)+string(rune('0'+i)))
		_, err = w.s.UpdateNoteEntry(ctx, w.kai, last.ID, bus.NoteEntryUpdate{Approved: ptr(true)})
		if i < 6 {
			require.NoError(t, err)
		}
	}
	assert.ErrorIs(t, err, bus.ErrValidation)
	assert.ErrorContains(t, err, "would exceed 4000 characters")
	n, _ := w.s.GetNote(ctx, w.kai)
	assert.False(t, n.Entries[6].Approved)

	// shrinking the text makes room; growing it back is refused
	_, err = w.s.SetNoteText(ctx, w.kai, strings.Repeat("a", 1900))
	require.NoError(t, err)
	_, err = w.s.UpdateNoteEntry(ctx, w.kai, last.ID, bus.NoteEntryUpdate{Approved: ptr(true)})
	require.NoError(t, err)
	_, err = w.s.SetNoteText(ctx, w.kai, strings.Repeat("a", 1901))
	assert.ErrorIs(t, err, bus.ErrValidation)
}

// Review Focus 5.
func TestSuggestNoteEntry_RepeatsAndTheCap(t *testing.T) {
	w := newWorld(t)
	room := dmRoom(t, w)
	m, _ := startedTurn(t, w, room)
	scout := agentActor(w.scout)
	first, created, err := w.s.SuggestNoteEntry(ctx, scout, m, "prefers trains")
	require.NoError(t, err)
	assert.True(t, created)
	again, created, err := w.s.SuggestNoteEntry(ctx, scout, m, " prefers trains ")
	require.NoError(t, err)
	assert.False(t, created, "a retry returns the first entry")
	assert.Equal(t, first.ID, again.ID)

	for _, bad := range []string{"", "  ", strings.Repeat("a", 301), "a\x00b"} {
		_, _, err := w.s.SuggestNoteEntry(ctx, scout, m, bad)
		assert.ErrorIs(t, err, bus.ErrValidation, "%q", bad)
	}
	for i := 1; i < bus.MaxPendingNoteEntries; i++ {
		suggest(t, w, m, fmt.Sprintf("fact %d", i))
	}
	_, _, err = w.s.SuggestNoteEntry(ctx, scout, m, "one too many")
	assert.ErrorIs(t, err, bus.ErrValidation)
	assert.ErrorContains(t, err, "quarantine is full")
	_, err = w.s.UpdateNoteEntry(ctx, w.kai, first.ID, bus.NoteEntryUpdate{Approved: ptr(true)})
	require.NoError(t, err)
	_, _, err = w.s.SuggestNoteEntry(ctx, scout, m, "one too many")
	assert.NoError(t, err, "approving one frees a place")
}

func TestSuggestNoteEntry_NeedsAnOpenTurnOnAHumansMessage(t *testing.T) {
	w := newWorld(t)
	room := channelRoom(t, w)
	scout, atlas := agentActor(w.scout), agentActor(w.atlas)
	subS, subA := agentSub(t, w, w.scout), agentSub(t, w, w.atlas)
	_, _, err := w.s.SuggestNoteEntry(ctx, w.kai, ulid.Make(), "x")
	assert.ErrorIs(t, err, bus.ErrForbidden)
	_, _, err = w.s.SuggestNoteEntry(ctx, scout, ulid.Make(), "x")
	assert.ErrorIs(t, err, bus.ErrNotFound)

	m, _ := w.s.PostMessage(ctx, w.kai, room.ID, "@scout hello", ulid.Zero)
	drain(t, subS)
	_, _, err = w.s.SuggestNoteEntry(ctx, atlas, m.ID, "x")
	assert.ErrorIs(t, err, bus.ErrNotFound, "not atlas's delivery")
	woke, _ := w.s.PostMessage(ctx, scout, room.ID, "@atlas over to you", ulid.Zero)
	drain(t, subA)
	_, _, err = w.s.SuggestNoteEntry(ctx, atlas, woke.ID, "x")
	assert.ErrorIs(t, err, bus.ErrValidation, "an agent woke this turn")
	assert.ErrorContains(t, err, "no human author")

	_, _ = w.s.PauseRoom(ctx, w.kai, room.ID)
	_, _, err = w.s.SuggestNoteEntry(ctx, scout, m.ID, "x")
	assert.ErrorIs(t, err, bus.ErrPaused)
	_, _ = w.s.ResumeRoom(ctx, w.kai, room.ID)
	require.NoError(t, w.s.TurnDone(ctx, scout, m.ID))
	_, _, err = w.s.SuggestNoteEntry(ctx, scout, m.ID, "x")
	assert.ErrorIs(t, err, bus.ErrNoTurn)
}

// noteOf is the note on the one delivery event in evs.
func noteOf(t *testing.T, evs []bus.Event) string {
	t.Helper()
	d := of(evs, bus.EventDelivery)
	require.Len(t, d, 1)
	return d[0].Note
}

func TestDeliveryNote_TextAndApprovedEntriesOnly(t *testing.T) {
	w := newWorld(t)
	room := dmRoom(t, w)
	m, sub := startedTurn(t, w, room)
	defer sub.Close()
	scout := agentActor(w.scout)
	a, b := suggest(t, w, m, "prefers trains"), suggest(t, w, m, "is vegetarian")
	pending := suggest(t, w, m, "owns a boat")
	_ = pending
	for _, e := range []bus.NoteEntry{a, b} {
		_, err := w.s.UpdateNoteEntry(ctx, w.kai, e.ID, bus.NoteEntryUpdate{Approved: ptr(true)})
		require.NoError(t, err)
	}
	require.NoError(t, w.s.TurnDone(ctx, scout, m))

	next := func(text string) string {
		msg, err := w.s.PostMessage(ctx, w.kai, room.ID, text, ulid.Zero)
		require.NoError(t, err)
		note := noteOf(t, drain(t, sub))
		require.NoError(t, w.s.TurnDone(ctx, scout, msg.ID))
		return note
	}
	assert.Equal(t, "- prefers trains\n- is vegetarian", next("two"), "the quarantined entry is held back")
	_, _ = w.s.SetNoteText(ctx, w.kai, "I live in Lisbon.")
	assert.Equal(t, "I live in Lisbon.\n\n- prefers trains\n- is vegetarian", next("three"), "an edit reaches the next message")
	_, _ = w.s.DeleteNoteEntry(ctx, w.kai, a.ID)
	_, _ = w.s.DeleteNoteEntry(ctx, w.kai, b.ID)
	assert.Equal(t, "I live in Lisbon.", next("four"))
	_, _ = w.s.SetNoteText(ctx, w.kai, "")
	assert.Equal(t, "", next("five"))
}

// Review Focus 4.
func TestDeliveryNote_OnlyWhenTheAuthorIsTheOnlyHuman(t *testing.T) {
	w := newWorld(t)
	_, _ = w.s.SetNoteText(ctx, w.kai, "kai's secret")
	room := channelRoom(t, w) // kai, atlas, scout
	subS, subA := agentSub(t, w, w.scout), agentSub(t, w, w.atlas)
	scout := agentActor(w.scout)

	m1, _ := w.s.PostMessage(ctx, w.kai, room.ID, "@scout one", ulid.Zero)
	assert.Equal(t, "kai's secret", noteOf(t, drain(t, subS)))
	woke, _ := w.s.PostMessage(ctx, scout, room.ID, "@atlas over to you", ulid.Zero)
	_ = woke
	assert.Equal(t, "", noteOf(t, drain(t, subA)), "an agent's message carries no note")
	require.NoError(t, w.s.TurnDone(ctx, scout, m1.ID))

	// released while kai was alone, read after ada joined: no note
	_, _ = w.s.PostMessage(ctx, w.kai, room.ID, "@scout two", ulid.Zero)
	_, err := w.s.InviteHuman(ctx, w.kai, room.ID, w.ada.ID)
	require.NoError(t, err)
	assert.Equal(t, "", noteOf(t, drain(t, subS)))
}