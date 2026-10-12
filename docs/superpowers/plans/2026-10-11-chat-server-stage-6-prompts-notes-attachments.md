# Stage 6: Prompts, Notes, and Attachments Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** An agent can ask a human for an approval or an answer and wait for it, each human keeps a note that reaches agents only after the human approves what agents add to it, and an agent can send files that only the room's members can fetch.

**Architecture:** Three parts that share nothing but the layers. A **prompt** is an agent's message with a row in `prompts` beside it, so it rides the message feed, paging, and unread marker; its answer goes back to the agent as an agent-addressed `prompt.answered` event. A **note** is one row of human text plus `note_entries` rows that agents append as pending; the bus composes text and approved entries into the `delivery` event at hand-over time. An **attachment** is a file on disk named by its ULID with a metadata row; an agent uploads it, then names it when posting a message. All three accept content from agents, so all three go through `paused()`.

**Tech Stack:** Go 1.26, gin, SQLite (`modernc.org/sqlite`), goose migrations, sqlc 1.31.1, testify. No new dependencies.

**Spec:** `docs/superpowers/specs/2026-10-11-chat-server-architecture-design.md` (section 14, stage 6; sections 4, 5.4, 7.2) and `docs/superpowers/specs/2026-10-11-hermes-chat-design.md` (sections 6.2, 6.3, 6.6, 5.3, 5.6). Read `AGENTS.md` first.

## Global Constraints

- Layering is enforced by `TestLayering_ThisRepository`: rules live in `bus`, SQL and files in `storage`, HTTP in `app`. `bus` imports neither `net/http` nor gin, never returns a storage sentinel, and never builds an HTTP response.
- The off switch is one gate (`AGENTS.md`): every new way to accept content from an agent (a prompt, a note suggestion, an upload) and every new thing handed to an agent (an answer) checks `paused()`.
- Prompts: "The agent waits for the human." "An open prompt puts a **badge on the room** and a count on the room list." "when the plugin reports a prompt withdrawn or timed out, it shows as expired".
- Notes: "Notes are attached only in rooms where the author is the only human." "Suggestions are quarantined … not sent to any agent until the human approves them". "Agents append; they cannot rewrite or delete. Each entry records which agent added it and when." "A human can never see another human's note."
- Note caps: "2,000 characters of human text, 300 per agent entry, 4,000 total approved". Characters are runes.
- Attachments: "stores them as files on disk, with metadata in SQLite, and serves them only to members of the room, with `Content-Disposition: attachment` for anything that is not a known safe image type. Human uploads are v1.5 and will reuse this storage."
- "A transaction callback does database work only. No network calls": a request body is read and a file written before `InTx`, never inside it.
- Every state change that humans or agents must see writes its event rows in the same transaction (`s.record`); the hub is rung only after commit. Time comes from `s.now()`; IDs are ULIDs.
- Every table is `STRICT`; after touching migrations or queries run `just generate`; `sqlcgen/` is committed and never hand-edited.
- New error kinds go in three places: `bus/bus.go`, `app/errors.go`'s `errorTable`, and `README.md`'s table.
- App tests build their route tables by hand (`apiEngine` in `app/auth_test.go`, `agentEngine` in `app/delivery_test.go`): a new route is added there as well as in `routes.go` and `TestRoutes_Table`.
- Tests: testify `assert`/`require`, black-box `_test` packages, real layers below, a mock only to force an error. `CGO_ENABLED=0`; no cgo dependency.
- `just check` must pass at the end of every task. End commit messages with the attribution lines the session gives you.

## Decisions this plan makes (Claude's calls; change before execution if wrong)

1. **A prompt is a message.** `POST /agent/rooms/:room/prompts` writes an agent-authored message (its text is the question or the command) and a `prompts` row keyed by the message id. The message object gains `"prompt"`. A prompt message wakes nobody, even if its text has an @mention.
2. **The plugin supplies the choices.** An approval's are a non-empty subset of `once`, `session`, `always`, `deny`; a clarifying question's are 0 to 6 free strings. An approval's answer must be one of its choices; a clarifying question's answer is any text of 1–2000 characters (a choice, or the human's own words).
3. **Any human in the room may answer.** Answering does not reset the turn count: it is not a message.
4. **Double taps are quiet.** Answering an answered prompt with the same answer succeeds and changes nothing; a different answer, or any answer to an expired prompt, is 409 `prompt_closed`. Expiring a prompt that is already answered or expired succeeds and changes nothing.
5. **Closing a room expires its open prompts.** Pausing (by a human or the turn cap), stop everything, archiving, and removing the asking agent all expire them, because each ends the turn that was waiting. So an open prompt never sits in a closed room, and there is no stale badge to clear.
6. **An answer the agent has not read when its room closes is dropped**, exactly as a steer is: `prompt.answered` is skipped at read time while the room is closed and is not re-sent on resume. The pause has already told the plugin to stop that turn.
7. **A suggestion is tied to a turn.** `POST /agent/deliveries/:message/note-entries` suggests an entry for the human who wrote the delivered message. No turn, or an agent-written message, means no suggestion. Suggesting the same text twice returns the first entry; a human's quarantine holds at most 50 pending entries.
8. **A human may edit, un-approve, and delete agent entries**; the entry keeps the agent and time it was added. Rejecting a suggestion deletes it.
9. **The server composes the note**: the human's text, a blank line, then each approved entry as a `- ` line, oldest first. It is attached at hand-over time, so an edit reaches every delivery not yet read.
10. **Note changes write no event.** The event log needs a room and a note has none; the UI loads the note when its screen opens.
11. **Uploading is two steps**: `POST /agent/rooms/:room/attachments` stores one file and returns its id; `POST /agent/rooms/:room/messages` then takes `"attachment_ids"`. A message with an attachment may have blank text. An upload never posted stays on disk (see Known limits).
12. **The server decides the content type** by sniffing the first 512 bytes (`http.DetectContentType`); the client's `Content-Type` is ignored. The safe image types are `image/png`, `image/jpeg`, `image/gif`, `image/webp`; everything else, SVG included, is served as `application/octet-stream` with `Content-Disposition: attachment`.
13. **Limits:** 25 MiB per file, 10 files per message, 413 `too_large` beyond it. Uploads and downloads get a 10-minute read and write deadline in place of the server's 30 s.
14. **`HERMES_CHAT_ATTACHMENTS_DIR`** defaults to a directory named `attachments` beside the database file, created `0700` when `serve` starts. Files are named by attachment id, never by anything a client sent.
15. **The schema is ready for human uploads**: `attachments` has both `uploader_human_id` and `uploader_agent_id`. In this stage the bus refuses a human uploader and no human route exists.

## Review Focus

1. **Two answers race** (a double tap, two humans, or the plugin expiring as the human taps): exactly one outcome is stored, the agent is told at most once, and nobody gets a 500. → Task 2, `TestAnswerPrompt_TwiceAndAfterExpiry`.
2. **A file that is not what it claims** (HTML or SVG named `cat.png`, a name with quotes, a path, or a newline): it is never served inline or as its claimed type, and its name cannot break out of the header or the attachments directory. → Task 7 `TestFiles_RejectsIDsThatAreNotPlainNames`, Task 9 `TestAttachments_UnsafeTypesAreDownloads`.
3. **A big or slow upload**: over 25 MiB answers 413 and leaves nothing on disk; one that takes longer than the server's write timeout still gets its 201. → Task 8 `TestUploadAttachment_TooLargeLeavesNothing`, Task 9 `TestAttachments_SlowUploadOutlivesTheWriteTimeout`.
4. **A second human joins between the release and the hand-over**, or the delivered message is an agent's: no note is attached. → Task 5, `TestDeliveryNote_OnlyWhenTheAuthorIsTheOnlyHuman`.
5. **An agent floods or repeats suggestions** (the plugin retries after a reconnect): repeats return the first entry, and the quarantine stops at 50. → Task 5, `TestSuggestNoteEntry_RepeatsAndTheCap`.

---

## File Structure

All paths below are under `cmd/hermes-chat/` unless they start with `README` or `AGENTS`.

| File | Change |
|---|---|
| `internal/storage/migrations/00007_prompts.sql`, `00008_notes.sql`, `00009_attachments.sql` | new tables |
| `internal/storage/queries/prompts.sql`, `notes.sql`, `attachments.sql` | new queries |
| `internal/storage/prompt.go`, `note.go`, `attachment.go` (+ `_test.go`) | `Repository` methods and row types |
| `internal/storage/files.go`, `files_test.go` | new: the file store |
| `internal/storage/storage.go`, `storagetest/storagetest.go` | `Repository` interface; `NewFiles` helper |
| `internal/bus/prompt.go`, `note.go`, `attachment.go` (+ `_test.go`) | new: the three features |
| `internal/bus/bus.go`, `event.go`, `message.go`, `room.go`, `pause.go`, `halt.go`, `hub.go` | errors, `Service` methods, `Option`, event kinds, `Message`/`Room` fields, expiry call sites, event loading |
| `internal/app/prompt.go`, `note.go`, `attachment.go` (+ `_test.go`) | new: handlers |
| `internal/app/app.go`, `json.go`, `errors.go`, `stream.go`, `delivery.go` | `Controller`, `Options`, JSON shapes, error rows, frames, `attachment_ids` |
| `config.go`, `wires.go`, `routes.go`, `routes_test.go`, `config_test.go` | attachments dir, wiring, ten routes |
| `internal/fakeagent/fakeagent.go`, `stage6_e2e_test.go` | client methods; end-to-end tests |
| `README.md`, `AGENTS.md` | the API contract and the stage list |

Tasks 1–3 are prompts, 4–6 notes, 7–9 attachments, 10 ties them together. The three groups do not depend on each other and can be built in any order; inside a group the order is storage, bus, app.

---

### Task 1: Storage for prompts

**Files:**
- Create: `internal/storage/migrations/00007_prompts.sql`, `queries/prompts.sql`, `prompt.go`, `prompt_test.go`
- Modify: `internal/storage/storage.go`, `migrate_test.go`

**Interfaces:**
- Produces:

```go
// Prompt is the question an agent's message asks. Choices is a JSON array of strings.
type Prompt struct {
	MessageID, RoomID, AgentID string
	Kind, Choices              string  // Kind: "approval" or "clarify"
	State, Answer              string  // State: "open", "answered", or "expired"
	AnsweredBy                 *string // a human id; nil unless answered, and once that human is removed
	ClosedAtMs                 *int64  // nil while open
}

// on Repository
PutPrompt(ctx context.Context, p Prompt) error                                              // ErrConflict on a second prompt for the message; only the first five fields are written: a new prompt is open
GetPrompt(ctx context.Context, messageID string) (Prompt, error)                            // ErrNotFound
ListPromptsInRange(ctx context.Context, roomID, firstID, lastID string) ([]Prompt, error)   // by message id; empty, not nil
ListOpenPrompts(ctx context.Context, roomID string) ([]Prompt, error)                       // roomID "" means every room; by message id; empty, not nil
CountOpenPrompts(ctx context.Context, roomID string) (int64, error)
ClosePrompt(ctx context.Context, messageID, state, answer string, answeredBy *string, closedAtMs int64) error // ErrNotFound when there is no open prompt for the message
```

- [ ] **Step 1: Write the failing tests in `prompt_test.go`**

```go
func prompted(t *testing.T) (storage.Repository, storage.Prompt) {
	r := roomed(t)
	m := msg("1", trip.ID, kai.ID)
	require.NoError(t, r.PutMessage(ctx, m))
	p := storage.Prompt{MessageID: m.ID, RoomID: trip.ID, AgentID: scout.ID, Kind: "approval", Choices: `["once","deny"]`}
	require.NoError(t, r.PutPrompt(ctx, p))
	p.State = "open"
	return r, p
}

func TestPrompt_PutGetAndClose(t *testing.T) {
	r, p := prompted(t)
	got, err := r.GetPrompt(ctx, p.MessageID)
	require.NoError(t, err)
	assert.Equal(t, p, got)
	assert.ErrorIs(t, r.PutPrompt(ctx, p), storage.ErrConflict)
	_, err = r.GetPrompt(ctx, "nope")
	assert.ErrorIs(t, err, storage.ErrNotFound)

	n, _ := r.CountOpenPrompts(ctx, trip.ID)
	assert.EqualValues(t, 1, n)
	require.NoError(t, r.ClosePrompt(ctx, p.MessageID, "answered", "once", sp(kai.ID), 7))
	got, _ = r.GetPrompt(ctx, p.MessageID)
	assert.Equal(t, "answered", got.State)
	assert.Equal(t, "once", got.Answer)
	assert.Equal(t, sp(kai.ID), got.AnsweredBy)
	assert.Equal(t, ms(7), got.ClosedAtMs)
	n, _ = r.CountOpenPrompts(ctx, trip.ID)
	assert.EqualValues(t, 0, n)
	// a closed prompt cannot be closed again: the first outcome stands
	assert.ErrorIs(t, r.ClosePrompt(ctx, p.MessageID, "expired", "", nil, 8), storage.ErrNotFound)
	assert.ErrorIs(t, r.ClosePrompt(ctx, "nope", "expired", "", nil, 8), storage.ErrNotFound)
}

func TestPrompt_Lists(t *testing.T) {
	r, p := prompted(t)
	m2 := msg("2", trip.ID, kai.ID)
	require.NoError(t, r.PutMessage(ctx, m2)) // a plain message between prompts
	in, err := r.ListPromptsInRange(ctx, trip.ID, p.MessageID, m2.ID)
	require.NoError(t, err)
	assert.Equal(t, []storage.Prompt{p}, in)
	none, err := r.ListPromptsInRange(ctx, trip.ID, m2.ID, m2.ID)
	require.NoError(t, err)
	assert.NotNil(t, none)
	assert.Empty(t, none)

	room, _ := r.ListOpenPrompts(ctx, trip.ID)
	all, _ := r.ListOpenPrompts(ctx, "")
	assert.Equal(t, []storage.Prompt{p}, room)
	assert.Equal(t, room, all)
	elsewhere, _ := r.ListOpenPrompts(ctx, dm.ID)
	assert.NotNil(t, elsewhere)
	assert.Empty(t, elsewhere)
	require.NoError(t, r.ClosePrompt(ctx, p.MessageID, "expired", "", nil, 9))
	all, _ = r.ListOpenPrompts(ctx, "")
	assert.Empty(t, all)
}
```

In `migrate_test.go`, beside the `halt` assertion, add `assert.Contains(t, tables(t, db), "prompts")`.

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./cmd/hermes-chat/internal/storage/`
Expected: FAIL to compile (`storage.Prompt undefined`).

- [ ] **Step 3: Write the migration `00007_prompts.sql`**

This file and the queries below were applied up and down against sqlite3 3.46.1 and generated with sqlc 1.31.1 while planning.

```sql
-- +goose Up
CREATE TABLE prompts (
    message_id  TEXT    NOT NULL PRIMARY KEY REFERENCES messages (id) ON DELETE CASCADE,
    room_id     TEXT    NOT NULL REFERENCES rooms (id) ON DELETE CASCADE,
    agent_id    TEXT    NOT NULL REFERENCES agents (id),
    kind        TEXT    NOT NULL CHECK (kind IN ('approval', 'clarify')),
    choices     TEXT    NOT NULL,
    state       TEXT    NOT NULL DEFAULT 'open' CHECK (state IN ('open', 'answered', 'expired')),
    answer      TEXT    NOT NULL DEFAULT '',
    answered_by TEXT    REFERENCES humans (id) ON DELETE SET NULL,
    closed_at   INTEGER,
    CHECK ((state = 'open') = (closed_at IS NULL))
) STRICT;

CREATE INDEX prompts_open ON prompts (room_id, message_id) WHERE state = 'open';

-- +goose Down
DROP TABLE prompts;
```

- [ ] **Step 4: Add `queries/prompts.sql` and run `just generate`**

```sql
-- name: InsertPrompt :exec
INSERT INTO prompts (message_id, room_id, agent_id, kind, choices) VALUES (?, ?, ?, ?, ?);

-- name: GetPrompt :one
SELECT message_id, room_id, agent_id, kind, choices, state, answer, answered_by, closed_at FROM prompts WHERE message_id = ?;

-- name: ListPromptsInRange :many
SELECT message_id, room_id, agent_id, kind, choices, state, answer, answered_by, closed_at FROM prompts
WHERE room_id = ? AND message_id >= ? AND message_id <= ?
ORDER BY message_id;

-- name: ListOpenPrompts :many
SELECT message_id, room_id, agent_id, kind, choices, state, answer, answered_by, closed_at FROM prompts
WHERE state = 'open' ORDER BY message_id;

-- name: ListOpenPromptsInRoom :many
SELECT message_id, room_id, agent_id, kind, choices, state, answer, answered_by, closed_at FROM prompts
WHERE room_id = ? AND state = 'open' ORDER BY message_id;

-- name: CountOpenPrompts :one
SELECT COUNT(*) FROM prompts WHERE room_id = ? AND state = 'open';

-- name: ClosePrompt :execrows
UPDATE prompts SET state = ?, answer = ?, answered_by = ?, closed_at = ? WHERE message_id = ? AND state = 'open';
```

Run: `just generate`

- [ ] **Step 5: Implement the six `Repository` methods in `prompt.go`**

Declare them in `storage.go`'s interface with the comments from the Interfaces block. Follow `delivery.go`: `mapErr`, `requireRow` for `ClosePrompt`, `nullString`/`stringPtr`/`nullInt64`. `ListOpenPrompts` picks between the two generated queries on `roomID == ""`.

- [ ] **Step 6: Run the tests**

Run: `just check`
Expected: PASS (`sqlc diff` clean, vet clean, all packages ok).

- [ ] **Step 7: Commit**

```bash
git add cmd/hermes-chat/internal/storage
git commit -m "feat(storage): prompts, one per asking message"
```

---

### Task 2: Prompts in the bus

**Files:**
- Create: `internal/bus/prompt.go`, `prompt_test.go`
- Modify: `internal/bus/bus.go`, `event.go`, `message.go`, `room.go`, `pause.go`, `halt.go`, `hub.go`

**Interfaces:**
- Consumes: Task 1's `storage.Prompt` and six methods.
- Produces:

```go
// bus.go
var ErrPromptClosed = errors.New("prompt is closed") // answered otherwise, or expired

// Service
PostPrompt(ctx context.Context, actor Actor, roomID ulid.ULID, kind PromptKind, text string, choices []string) (Message, error) // agent member only: ErrForbidden for a human; ErrNotFound, ErrValidation, ErrRoomArchived, ErrPaused
AnswerPrompt(ctx context.Context, actor Actor, roomID, messageID ulid.ULID, answer string) (Message, error)                     // human member; ErrNotFound, ErrValidation, ErrRoomArchived, ErrPaused, ErrPromptClosed; the same answer again is a no-op
ExpirePrompt(ctx context.Context, actor Actor, messageID ulid.ULID) error                                                       // the agent that asked: ErrNotFound otherwise; already closed is a no-op

// prompt.go
type PromptKind string
const (
	PromptApproval PromptKind = "approval"
	PromptClarify  PromptKind = "clarify"
)
type PromptState string
const (
	PromptOpen     PromptState = "open"
	PromptAnswered PromptState = "answered"
	PromptExpired  PromptState = "expired"
)
type Prompt struct {
	Kind       PromptKind
	Choices    []string // never nil
	State      PromptState
	Answer     string    // "" unless answered
	AnsweredBy *Human    // nil unless answered, and once that human is removed
	ClosedAt   time.Time // zero while open
}
const (
	MaxPromptChoices     = 6
	MaxPromptChoiceChars = 200
	MaxPromptAnswerChars = 2000
)

// expireOpenPrompts expires the open prompts of a room ("" means every room) asked by an agent
// ("" means any), writing one prompt.changed per prompt. It reports how many it expired; the
// caller writes room.changed.
func (s *service) expireOpenPrompts(ctx context.Context, r storage.Repository, roomID, agentID string, at time.Time) (int, error)

// message.go: Message gains  Prompt *Prompt // nil for an ordinary message
// room.go:    Room gains     OpenPrompts int // prompts in the room still waiting for a human

// event.go
EventPromptChanged  EventKind = "prompt.changed"  // human: subject is the prompt's message
EventPromptAnswered EventKind = "prompt.answered" // agent: subject is the prompt's message
```

Validation messages, verbatim:

| Condition | `ValidationError.Message` |
|---|---|
| kind is neither | `kind must be approval or clarify` |
| approval choices empty, repeated, or outside the four | `an approval's choices must be one or more of once, session, always, deny, each at most once` |
| clarify choices: more than 6, one blank or over 200 characters or with a control character, or a repeat | `a clarifying question takes at most 6 choices, each 1-200 characters with no control characters, none repeated` |
| approval answer not one of the prompt's choices | `answer must be one of the prompt's choices` |
| clarify answer blank, over 2000 characters, or with a NUL | `answer must be 1-2000 characters with no NUL character` |

Prompt text uses `validMessageText` and `errMessageText`. Clarify choices and answers are trimmed before they are checked and stored.

- [ ] **Step 1: Write the failing tests in `prompt_test.go`**

```go
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
```

`shared` is the existing helper in `message_test.go`: a room holding kai, ada, and scout.

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./cmd/hermes-chat/internal/bus/ -run 'Prompt'`
Expected: FAIL to compile (`w.s.PostPrompt undefined`).

- [ ] **Step 3: Add the types, the error, the event kinds, and the two fields**

As in the Interfaces block. `Service` gains the three methods with those comments.

- [ ] **Step 4: Load prompts and the badge**

`messagesFromRows` fills `Message.Prompt` the way it fills `Deliveries`: one `ListPromptsInRange` over the rows' id range (follow `loadDeliveries`), choices decoded with `encoding/json` into a non-nil slice, `AnsweredBy` resolved through `GetHumanByID` (a missing human leaves it nil). `buildRoomLayout` sets `Room.OpenPrompts` from `CountOpenPrompts`.

- [ ] **Step 5: Implement `prompt.go`**

- `PostPrompt`: validate, then in `s.record`: `roomForPoster`, `ErrRoomArchived`, `paused()` → `ErrPaused`; put the message (agent author, no deliveries, no `wakeAgents`) and the prompt; write `message.posted` then `room.changed`.
- `AnswerPrompt`: in `s.record`, in this order: `roomForMember`; the message in this room with a prompt, else `ErrNotFound`; an answered prompt whose answer equals the trimmed answer returns with no write; any other non-open state is `ErrPromptClosed`; `ErrRoomArchived`; `paused()` → `ErrPaused`; validate the answer against the prompt's kind; `ClosePrompt(…, "answered", …)`; write `prompt.changed`, `room.changed`, and `prompt.answered` addressed to the prompt's agent (`putAgentEvent`).
- `ExpirePrompt`: in `s.record`: the prompt by message id, `ErrNotFound` unless `AgentID` is the actor; not open returns with no write; else `ClosePrompt(…, "expired", "", nil, …)`, `prompt.changed`, `room.changed`. It checks neither membership nor `paused()`: it is bookkeeping, like a turn report.
- `expireOpenPrompts` as documented: `ListOpenPrompts`, filter by agent, close each, one `prompt.changed` each.

- [ ] **Step 6: Call `expireOpenPrompts` where a room closes**

- `pauseRoom` (`pause.go`): the room's prompts, any agent. This covers the turn cap too.
- `StopEverything` (`halt.go`): `roomID ""`, once, after setting the halt.
- `UpdateRoom` (`room.go`), in the branch that sets the room archived.
- `RemoveRoomAgent`: that room, that agent, beside `CancelDeliveries`.

All four already write `room.changed`.

- [ ] **Step 7: Read the new kinds in `Subscription.event` (`hub.go`)**

`EventPromptChanged` joins the `EventMessagePosted, EventMessageEdited, EventDeliveryChanged` case. `EventPromptAnswered` follows `EventControlSteer`: skip the row when the room is gone or `paused()`, load the message, and skip it unless its prompt is `answered`.

- [ ] **Step 8: Run the tests**

Run: `just check`
Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add cmd/hermes-chat/internal/bus
git commit -m "feat(bus): prompts an agent asks and a human answers, expired when the room closes"
```

---

### Task 3: Prompts over HTTP

**Files:**
- Create: `internal/app/prompt.go`, `prompt_test.go`
- Modify: `internal/app/app.go`, `json.go`, `errors.go`, `stream.go`, `errors_test.go`, `auth_test.go` (`apiEngine`), `delivery_test.go` (`agentEngine`), `routes.go`, `routes_test.go`, `README.md`

**Interfaces:**
- Consumes: Task 2's three `Service` methods, `Message.Prompt`, `Room.OpenPrompts`, the two event kinds, `ErrPromptClosed`.
- Produces: `Controller` methods `AgentPostPrompt`, `AgentExpirePrompt`, `AnswerPrompt`, and:

| Route | Body | Reply |
|---|---|---|
| `POST /agent/rooms/:room/prompts` | `{"kind":"approval","text":"…","choices":["once","deny"]}`; `choices` optional | 201, the message |
| `POST /agent/prompts/:message/expire` | none | 204 |
| `POST /api/rooms/:room/messages/:message/answer` | `{"answer":"once"}` | 200, the message |

```
message gains  "prompt": null
          or   "prompt": {"kind":"approval","choices":["once","deny"],"state":"open",
                          "answer":null,"answered_by":null,"closed_at":null}
room gains     "open_prompts": 0
```

`answer` is a string once answered, `answered_by` a human object (or `null` once that human is removed), `closed_at` a time once answered or expired. Frames: human `prompt.changed` → `{"message":<message>}`; agent `prompt.answered` → `{"room_id":"01…","message_id":"01…","answer":"once"}`. Error row: `{bus.ErrPromptClosed, http.StatusConflict, "prompt_closed", "the prompt is no longer open"}`, placed before `bus.ErrConflict`.

- [ ] **Step 1: Write the failing tests in `prompt_test.go`**

```go
const promptClosed = `{"error":{"code":"prompt_closed","message":"the prompt is no longer open"}}`

func TestPrompts_AskAnswerAndExpire(t *testing.T) {
	r := newAgentRig(t)
	room := "/agent/rooms/" + r.room.ID.String()
	w := agentReq(r.e, r.scoutT, "POST", room+"/prompts", `{"kind":"approval","text":"run it?","choices":["once","deny"]}`)
	require.Equal(t, 201, w.Code, w.Body.String())
	m := obj(t, w)
	assert.JSONEq(t, `{"kind":"approval","choices":["once","deny"],"state":"open","answer":null,"answered_by":null,"closed_at":null}`,
		string(mustJSON(t, m["prompt"])))
	id := m["id"].(string)

	w = agentReq(r.e, r.scoutT, "POST", room+"/prompts", `{"kind":"clarify","text":"which?"}`)
	require.Equal(t, 201, w.Code, w.Body.String())
	assert.Equal(t, []any{}, obj(t, w)["prompt"].(map[string]any)["choices"])
	w = agentReq(r.e, r.scoutT, "POST", room+"/prompts", `{"kind":"poll","text":"x"}`)
	assert.Equal(t, 400, w.Code)
	assert.Contains(t, w.Body.String(), "kind must be approval or clarify")

	assert.Equal(t, 204, agentReq(r.e, r.scoutT, "POST", "/agent/prompts/"+id+"/expire", "").Code)
	assert.Equal(t, 204, agentReq(r.e, r.scoutT, "POST", "/agent/prompts/"+id+"/expire", "").Code)
	assert.Equal(t, 404, agentReq(r.e, r.atlasT, "POST", "/agent/prompts/"+id+"/expire", "").Code)
	assert.Equal(t, 404, agentReq(r.e, r.scoutT, "POST", "/agent/prompts/nope/expire", "").Code)
}

func TestPrompts_HumanAnswers(t *testing.T) {
	c := newChat(t)
	kai := bus.Actor{Kind: bus.ActorHuman, ID: c.kaiH.ID}
	room, _, err := c.svc.OpenDM(ctx, kai, c.scout.ID)
	require.NoError(t, err)
	scout := bus.Actor{Kind: bus.ActorAgent, ID: c.scout.ID}
	m, err := c.svc.PostPrompt(ctx, scout, room.ID, bus.PromptApproval, "run it?", []string{"once", "deny"})
	require.NoError(t, err)
	base := "/api/rooms/" + room.ID.String()
	assert.EqualValues(t, 1, obj(t, c.call(c.kai, "GET", base, ""))["open_prompts"])

	target := base + "/messages/" + m.ID.String() + "/answer"
	assert.Equal(t, 400, c.call(c.kai, "POST", target, `{"answer":"maybe"}`).Code)
	assert.JSONEq(t, notFound, c.call(c.ada, "POST", target, `{"answer":"once"}`).Body.String())
	w := c.call(c.kai, "POST", target, `{"answer":"once"}`)
	require.Equal(t, 200, w.Code, w.Body.String())
	p := obj(t, w)["prompt"].(map[string]any)
	assert.Equal(t, "answered", p["state"])
	assert.Equal(t, "once", p["answer"])
	assert.Equal(t, "kai", p["answered_by"].(map[string]any)["username"])
	assert.NotNil(t, p["closed_at"])
	assert.Equal(t, 200, c.call(c.kai, "POST", target, `{"answer":"once"}`).Code)
	w = c.call(c.kai, "POST", target, `{"answer":"deny"}`)
	assert.Equal(t, 409, w.Code)
	assert.JSONEq(t, promptClosed, w.Body.String())
	assert.EqualValues(t, 0, obj(t, c.call(c.kai, "GET", base, ""))["open_prompts"])

	// an ordinary message carries "prompt": null
	plain, _ := c.svc.PostMessage(ctx, kai, room.ID, "hi", ulid.Zero)
	got := obj(t, c.call(c.kai, "GET", base+"/messages/"+plain.ID.String(), ""))
	v, present := got["prompt"]
	assert.True(t, present)
	assert.Nil(t, v)
}
```

Add `mustJSON(t, v any) []byte` (a `json.Marshal` that fails the test) to `room_test.go` beside `obj`.

In `stream_test.go`, one test in the style of the existing delivery-frame tests, using `newRig` and its `open`/`openAgent` helpers:

```go
func TestStream_PromptFrames(t *testing.T) {
	r := newRig(t, app.Options{}, nil)
	_, human := r.open(t, r.cookie, "")
	_, agent := r.openAgent(t, r.scoutToken, "")
	next(t, human)
	next(t, agent) // ready
	m, err := r.svc.PostPrompt(ctx, r.scoutActor(), r.room.ID, bus.PromptApproval, "run it?", []string{"once"})
	require.NoError(t, err)
	_, err = r.svc.AnswerPrompt(ctx, r.kai, r.room.ID, m.ID, "once")
	require.NoError(t, err)

	hq, aq := newFrameQueue(t, human), newFrameQueue(t, agent)
	f := hq.want(func(f sse.Frame) bool { return f.Event == "prompt.changed" })
	assert.Contains(t, f.Data, `"state":"answered"`)
	f = aq.want(func(f sse.Frame) bool { return f.Event == "prompt.answered" })
	assert.JSONEq(t, fmt.Sprintf(`{"room_id":%q,"message_id":%q,"answer":"once"}`, r.room.ID, m.ID), f.Data)
}
```

In `errors_test.go`, add a row to the table test: `bus.ErrPromptClosed` → 409, `prompt_closed`, `the prompt is no longer open`. In `routes_test.go`, add the three routes to `TestRoutes_Table`'s sorted list.

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./cmd/hermes-chat/...`
Expected: FAIL to compile (`ctrl.AgentPostPrompt undefined`).

- [ ] **Step 3: Implement the handlers, the JSON, the error row, and the frames**

`prompt.go` holds the three handlers (bodies read with `readJSON(c, maxMessageBody, …)` for the post, `maxBody` for the answer; ids with `pathID`). `messageBody` gains `Prompt *promptBody` with tag `json:"prompt"`; `roomBody` gains `OpenPrompts int` with tag `json:"open_prompts"`. `eventFrame` and `agentEventFrame` each gain one case. Register the routes in `routes.go`, `apiEngine`, and `agentEngine`.

- [ ] **Step 4: Run the tests**

Run: `just check`
Expected: PASS.

- [ ] **Step 5: Document it in `README.md`**

Three route rows, the `prompt_closed` error row, `prompt` and `open_prompts` in the JSON shapes with a sentence each, the two event rows, and a new section "Prompts" after "The off switch" stating decisions 1–6 as behavior (what the plugin sends, what the human may answer, that double taps are quiet, that closing a room expires its prompts, that an unread answer is dropped when the room closes). In "The off switch", add prompts to the list of agent writes a paused room refuses.

- [ ] **Step 6: Commit**

```bash
git add cmd/hermes-chat README.md
git commit -m "feat(app): prompts over HTTP, with the room badge and both stream frames"
```

---

### Task 4: Storage for notes

**Files:**
- Create: `internal/storage/migrations/00008_notes.sql`, `queries/notes.sql`, `note.go`, `note_test.go`
- Modify: `internal/storage/storage.go`, `migrate_test.go`

**Interfaces:**
- Produces:

```go
// NoteEntry is one thing an agent added to a human's note. It is pending until approved.
type NoteEntry struct {
	ID, HumanID, AgentID, Text string
	ApprovedAtMs               *int64 // nil while in quarantine
	CreatedAtMs                int64
}

// on Repository
GetNoteText(ctx context.Context, humanID string) (string, error)                  // "" when the human has written none
PutNoteText(ctx context.Context, humanID, text string, updatedAtMs int64) error   // replaces
PutNoteEntry(ctx context.Context, e NoteEntry) error                              // ErrConflict on duplicate id
GetNoteEntry(ctx context.Context, id string) (NoteEntry, error)                   // ErrNotFound
ListNoteEntries(ctx context.Context, humanID string) ([]NoteEntry, error)         // oldest first, pending and approved; empty, not nil
SetNoteEntryText(ctx context.Context, id, text string) error                      // ErrNotFound
SetNoteEntryApprovedAt(ctx context.Context, id string, approvedAtMs *int64) error // ErrNotFound; nil returns it to quarantine
DeleteNoteEntry(ctx context.Context, id string) error                             // ErrNotFound
```

- [ ] **Step 1: Write the failing tests in `note_test.go`**

```go
func TestNoteText(t *testing.T) {
	r := seeded(t)
	got, err := r.GetNoteText(ctx, kai.ID)
	require.NoError(t, err)
	assert.Equal(t, "", got)
	require.NoError(t, r.PutNoteText(ctx, kai.ID, "I live in Lisbon.", 1))
	require.NoError(t, r.PutNoteText(ctx, kai.ID, "I live in Porto.", 2))
	got, _ = r.GetNoteText(ctx, kai.ID)
	assert.Equal(t, "I live in Porto.", got)
	other, _ := r.GetNoteText(ctx, ada.ID)
	assert.Equal(t, "", other)
}

func TestNoteEntries(t *testing.T) {
	r := seeded(t)
	none, err := r.ListNoteEntries(ctx, kai.ID)
	require.NoError(t, err)
	assert.NotNil(t, none)
	assert.Empty(t, none)

	a := storage.NoteEntry{ID: "01JA00000000000000000000N1", HumanID: kai.ID, AgentID: scout.ID, Text: "prefers trains", CreatedAtMs: 5}
	b := storage.NoteEntry{ID: "01JA00000000000000000000N2", HumanID: kai.ID, AgentID: atlas.ID, Text: "vegetarian", CreatedAtMs: 6}
	require.NoError(t, r.PutNoteEntry(ctx, b))
	require.NoError(t, r.PutNoteEntry(ctx, a))
	assert.ErrorIs(t, r.PutNoteEntry(ctx, a), storage.ErrConflict)
	list, _ := r.ListNoteEntries(ctx, kai.ID)
	assert.Equal(t, []storage.NoteEntry{a, b}, list)
	theirs, _ := r.ListNoteEntries(ctx, ada.ID)
	assert.Empty(t, theirs)

	require.NoError(t, r.SetNoteEntryApprovedAt(ctx, a.ID, ms(9)))
	require.NoError(t, r.SetNoteEntryText(ctx, a.ID, "prefers night trains"))
	got, err := r.GetNoteEntry(ctx, a.ID)
	require.NoError(t, err)
	assert.Equal(t, ms(9), got.ApprovedAtMs)
	assert.Equal(t, "prefers night trains", got.Text)
	require.NoError(t, r.SetNoteEntryApprovedAt(ctx, a.ID, nil))
	got, _ = r.GetNoteEntry(ctx, a.ID)
	assert.Nil(t, got.ApprovedAtMs)

	require.NoError(t, r.DeleteNoteEntry(ctx, a.ID))
	for _, err := range []error{
		r.DeleteNoteEntry(ctx, a.ID), r.SetNoteEntryText(ctx, a.ID, "x"), r.SetNoteEntryApprovedAt(ctx, a.ID, ms(1)),
	} {
		assert.ErrorIs(t, err, storage.ErrNotFound)
	}
	_, err = r.GetNoteEntry(ctx, a.ID)
	assert.ErrorIs(t, err, storage.ErrNotFound)

	// a removed human's note goes with them
	require.NoError(t, r.PutNoteText(ctx, kai.ID, "x", 1))
	require.NoError(t, r.DeleteHuman(ctx, kai.ID))
	_, err = r.GetNoteEntry(ctx, b.ID)
	assert.ErrorIs(t, err, storage.ErrNotFound)
}
```

In `migrate_test.go` add `assert.Contains(t, tables(t, db), "note_entries")`.

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./cmd/hermes-chat/internal/storage/`
Expected: FAIL to compile (`storage.NoteEntry undefined`).

- [ ] **Step 3: Write the migration `00008_notes.sql`**

Verified up, down, and through sqlc while planning.

```sql
-- +goose Up
CREATE TABLE notes (
    human_id   TEXT    NOT NULL PRIMARY KEY REFERENCES humans (id) ON DELETE CASCADE,
    text       TEXT    NOT NULL,
    updated_at INTEGER NOT NULL
) STRICT;

CREATE TABLE note_entries (
    id          TEXT    NOT NULL PRIMARY KEY,
    human_id    TEXT    NOT NULL REFERENCES humans (id) ON DELETE CASCADE,
    agent_id    TEXT    NOT NULL REFERENCES agents (id),
    text        TEXT    NOT NULL,
    approved_at INTEGER,
    created_at  INTEGER NOT NULL
) STRICT;

CREATE INDEX note_entries_human ON note_entries (human_id, id);

-- +goose Down
DROP TABLE note_entries;
DROP TABLE notes;
```

- [ ] **Step 4: Add `queries/notes.sql` and run `just generate`**

```sql
-- name: GetNoteText :one
SELECT text FROM notes WHERE human_id = ?;

-- name: UpsertNoteText :exec
INSERT INTO notes (human_id, text, updated_at) VALUES (?, ?, ?)
ON CONFLICT (human_id) DO UPDATE SET text = excluded.text, updated_at = excluded.updated_at;

-- name: InsertNoteEntry :exec
INSERT INTO note_entries (id, human_id, agent_id, text, approved_at, created_at) VALUES (?, ?, ?, ?, ?, ?);

-- name: GetNoteEntry :one
SELECT id, human_id, agent_id, text, approved_at, created_at FROM note_entries WHERE id = ?;

-- name: ListNoteEntries :many
SELECT id, human_id, agent_id, text, approved_at, created_at FROM note_entries WHERE human_id = ? ORDER BY id;

-- name: SetNoteEntryText :execrows
UPDATE note_entries SET text = ? WHERE id = ?;

-- name: SetNoteEntryApprovedAt :execrows
UPDATE note_entries SET approved_at = ? WHERE id = ?;

-- name: DeleteNoteEntry :execrows
DELETE FROM note_entries WHERE id = ?;
```

Run: `just generate`

- [ ] **Step 5: Implement the eight `Repository` methods in `note.go`**

`GetNoteText` maps `sql.ErrNoRows` to `""` with a nil error, as `GetLastEventID` does for an empty log. The rest follow `activity.go`.

- [ ] **Step 6: Run the tests**

Run: `just check`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add cmd/hermes-chat/internal/storage
git commit -m "feat(storage): a human's note text and the entries agents add to it"
```

---

### Task 5: Notes in the bus

**Files:**
- Create: `internal/bus/note.go`, `note_test.go`
- Modify: `internal/bus/bus.go`, `event.go`, `hub.go`

**Interfaces:**
- Consumes: Task 4's `storage.NoteEntry` and eight methods; the existing `requireOpenTurn`.
- Produces:

```go
// note.go
const (
	MaxNoteTextChars      = 2000 // the human's own text
	MaxNoteEntryChars     = 300  // one agent entry
	MaxNoteApprovedChars  = 4000 // the human's text plus every approved entry
	MaxPendingNoteEntries = 50   // a human's quarantine
)

type Note struct {
	Text    string
	Entries []NoteEntry // oldest first, pending and approved; never nil
}
type NoteEntry struct {
	ID        ulid.ULID
	Agent     Agent
	Text      string
	Approved  bool
	CreatedAt time.Time
}
// NoteEntryUpdate changes what is non-nil: Text first, then Approved.
type NoteEntryUpdate struct {
	Text     *string
	Approved *bool
}

// composeNote is what an agent is given: the text, a blank line, then one "- " line per approved entry. "" when both are empty.
func composeNote(text string, approved []string) string

// Service
GetNote(ctx context.Context, actor Actor) (Note, error)                                                  // human only: ErrForbidden
SetNoteText(ctx context.Context, actor Actor, text string) (Note, error)                                 // ErrForbidden, ErrValidation
UpdateNoteEntry(ctx context.Context, actor Actor, entryID ulid.ULID, u NoteEntryUpdate) (Note, error)    // the actor's own entries: ErrNotFound otherwise; ErrValidation
DeleteNoteEntry(ctx context.Context, actor Actor, entryID ulid.ULID) (Note, error)                       // the same
SuggestNoteEntry(ctx context.Context, actor Actor, messageID ulid.ULID, text string) (NoteEntry, bool, error) // agent with an open turn on the message; created is false for a repeat; ErrNotFound, ErrNoTurn, ErrPaused, ErrValidation

// event.go: Event gains  Note string // delivery: the author's note, when the author is the room's only human
```

Validation messages, verbatim:

| Condition | `ValidationError.Message` |
|---|---|
| note text over 2000 characters or with a NUL | `note text must be at most 2000 characters with no NUL character` |
| entry text blank, over 300 characters, or with a NUL | `a note entry must be 1-300 characters with no NUL character` |
| text plus approved entries would pass 4000 | `the approved note would exceed 4000 characters; shorten or delete something first` |
| the quarantine already holds 50 | `this human's quarantine is full` |
| the delivered message was not written by a human still registered | `the delivered message has no human author` |

Text is trimmed before it is counted and stored; counts are runes. An empty note text is allowed and clears it.

- [ ] **Step 1: Write the failing tests in `note_test.go`**

```go
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
	for i := 0; i < 7; i++ { // 2000 + 6×300 = 3800; the seventh would make 4100
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
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./cmd/hermes-chat/internal/bus/ -run 'Note'`
Expected: FAIL to compile (`w.s.GetNote undefined`).

- [ ] **Step 3: Implement `note.go`**

- The four human methods refuse a non-human with `ErrForbidden`. The three writers run in `s.repo.InTx` (not `s.record`: they write no event and must not ring the hub) and return the note as it is after the commit. An entry whose `HumanID` is not the actor's is `ErrNotFound`.
- The 4000 check runs inside the transaction on what the note would be after the change: on `SetNoteText`, on approving, and on editing the text of an approved entry. Un-approving, deleting, and editing a pending entry never fail it.
- `SuggestNoteEntry`: `ErrForbidden` unless the actor is an agent; validate the text; in `s.repo.InTx`: `requireOpenTurn` (it brings `ErrNotFound`, `ErrPaused`, `ErrNoTurn`); the delivered message's `AuthorHumanID` must be set, else the "no human author" error; list that human's entries; one by this agent with the same trimmed text is returned with `created` false; 50 pending is the "quarantine is full" error; else put a pending entry.

- [ ] **Step 4: Attach the note to the delivery event (`hub.go`)**

In `Subscription.event`'s `EventDelivery` case, after the room is loaded: when `e.Message.Author` is a human and `room.Humans` is exactly that one human, set `e.Note = composeNote(text, approved)` from `GetNoteText` and the approved entries of `ListNoteEntries`. Otherwise it stays `""`.

- [ ] **Step 5: Run the tests**

Run: `just check`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add cmd/hermes-chat/internal/bus
git commit -m "feat(bus): the per-human note, its quarantine, and the note on a delivery"
```

---

### Task 6: Notes over HTTP

**Files:**
- Create: `internal/app/note.go`, `note_test.go`
- Modify: `internal/app/app.go`, `json.go`, `stream.go`, `stream_test.go`, `auth_test.go` (`apiEngine`), `delivery_test.go` (`agentEngine`), `routes.go`, `routes_test.go`, `README.md`

**Interfaces:**
- Consumes: Task 5's five `Service` methods, `Note`, `NoteEntry`, `Event.Note`.
- Produces: `Controller` methods `GetNote`, `SetNoteText`, `UpdateNoteEntry`, `DeleteNoteEntry`, `AgentSuggestNoteEntry`, and:

| Route | Body | Reply |
|---|---|---|
| `GET /api/note` | | 200, the note |
| `PUT /api/note` | `{"text":"…"}` | 200, the note |
| `PATCH /api/note/entries/:entry` | `{"text":"…","approved":true}`, each optional | 200, the note |
| `DELETE /api/note/entries/:entry` | | 200, the note |
| `POST /agent/deliveries/:message/note-entries` | `{"text":"…"}` | 201 with the entry; 200 with it when it already existed |

```
note        {"text":"I live in Lisbon.","entries":[note entry…]}
note entry  {"id":"01…","agent":{agent…},"text":"prefers trains","approved":false,
             "created_at":"2026-10-11T12:00:00.123Z"}
```

The agent's `delivery` frame gains `"note"`: the composed string, or `null` when there is none.

- [ ] **Step 1: Write the failing tests in `note_test.go`**

```go
func TestNote_HumanRoutes(t *testing.T) {
	c := newChat(t)
	assert.JSONEq(t, `{"text":"","entries":[]}`, c.call(c.kai, "GET", "/api/note", "").Body.String())
	w := c.call(c.kai, "PUT", "/api/note", `{"text":" I live in Lisbon. "}`)
	require.Equal(t, 200, w.Code, w.Body.String())
	assert.Equal(t, "I live in Lisbon.", obj(t, w)["text"])
	assert.Equal(t, 400, c.call(c.kai, "PUT", "/api/note", `{"text":"`+strings.Repeat("a", 2001)+`"}`).Code)
	assert.Equal(t, 401, c.call("", "GET", "/api/note", "").Code)

	// scout suggests during a turn on kai's message
	kai, scout := bus.Actor{Kind: bus.ActorHuman, ID: c.kaiH.ID}, bus.Actor{Kind: bus.ActorAgent, ID: c.scout.ID}
	room, _, _ := c.svc.OpenDM(ctx, kai, c.scout.ID)
	sub, err := c.svc.Subscribe(ctx, scout, bus.FromNow)
	require.NoError(t, err)
	defer sub.Close()
	m, _ := c.svc.PostMessage(ctx, kai, room.ID, "hi", ulid.Zero)
	e, _, err := c.svc.SuggestNoteEntry(ctx, scout, m.ID, "prefers trains")
	require.NoError(t, err)

	entry := "/api/note/entries/" + e.ID.String()
	assert.JSONEq(t, notFound, c.call(c.ada, "PATCH", entry, `{"approved":true}`).Body.String())
	assert.JSONEq(t, `{"text":"","entries":[]}`, c.call(c.ada, "GET", "/api/note", "").Body.String(), "ada sees none of kai's note")
	w = c.call(c.kai, "PATCH", entry, `{"text":"prefers night trains","approved":true}`)
	require.Equal(t, 200, w.Code, w.Body.String())
	got := obj(t, w)["entries"].([]any)[0].(map[string]any)
	assert.Equal(t, "prefers night trains", got["text"])
	assert.Equal(t, true, got["approved"])
	assert.Equal(t, "scout", got["agent"].(map[string]any)["handle"])
	assert.Equal(t, 200, c.call(c.kai, "PATCH", entry, `{}`).Code, "an empty change is not an error")
	w = c.call(c.kai, "DELETE", entry, "")
	require.Equal(t, 200, w.Code)
	assert.Empty(t, obj(t, w)["entries"])
	assert.JSONEq(t, notFound, c.call(c.kai, "DELETE", entry, "").Body.String())
	assert.JSONEq(t, notFound, c.call(c.kai, "DELETE", "/api/note/entries/nope", "").Body.String())
}

func TestNote_AgentSuggests(t *testing.T) {
	r := newAgentRig(t)
	r.connect(t, r.scout.ID)
	m := r.kaiPost(t, r.room, "hi", ulid.Zero)
	target := "/agent/deliveries/" + m.ID.String() + "/note-entries"
	w := agentReq(r.e, r.scoutT, "POST", target, `{"text":"prefers trains"}`)
	require.Equal(t, 201, w.Code, w.Body.String())
	first := obj(t, w)
	assert.Equal(t, false, first["approved"])
	w = agentReq(r.e, r.scoutT, "POST", target, `{"text":"prefers trains"}`)
	assert.Equal(t, 200, w.Code, "a repeat is not created again")
	assert.Equal(t, first["id"], obj(t, w)["id"])
	assert.Equal(t, 400, agentReq(r.e, r.scoutT, "POST", target, `{"text":""}`).Code)
	assert.Equal(t, 404, agentReq(r.e, r.atlasT, "POST", target, `{"text":"x"}`).Code)
}
```

In `stream_test.go`, add `Note *string` with tag `json:"note"` to the existing `delivery` struct, and in the first existing delivery-frame test assert the raw frame contains `"note":null`; then one new test: kai sets a note text through `r.svc.SetNoteText`, posts, and the agent stream's `delivery` frame decodes with `*first.Note == "<the text>"`.

In `routes_test.go`, add the five routes to `TestRoutes_Table`.

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./cmd/hermes-chat/...`
Expected: FAIL to compile (`ctrl.GetNote undefined`).

- [ ] **Step 3: Implement the handlers, the JSON, and the frame field**

`note.go` holds the five handlers; bodies are read with `readJSON(c, maxBody, …)` into structs with pointer fields for the `PATCH`. `noteJSON` and `noteEntryJSON` go in `json.go`, arrays never null. `agentEventFrame`'s delivery body gains `"note"`: `nil` for `""`, else the string. Register the routes in `routes.go`, `apiEngine`, and `agentEngine`.

- [ ] **Step 4: Run the tests**

Run: `just check`
Expected: PASS.

- [ ] **Step 5: Document it in `README.md`**

Five route rows, the two JSON shapes, `"note"` on the `delivery` frame, and a section "The note" stating: the caps (2000, 300, 4000, counted in characters) and the 50-entry quarantine; that a suggestion is tied to a turn on a human's message and a repeat returns the first entry; that only the text and approved entries reach an agent, composed as text, blank line, `- ` lines; that the note is attached only when the delivered message's author is the room's only human, decided when the agent reads the delivery; that note changes write no event.

- [ ] **Step 6: Commit**

```bash
git add cmd/hermes-chat README.md
git commit -m "feat(app): the note and its quarantine over HTTP, and the note on a delivery frame"
```

---

### Task 7: Storage for attachments, and the file store

**Files:**
- Create: `internal/storage/migrations/00009_attachments.sql`, `queries/attachments.sql`, `attachment.go`, `attachment_test.go`, `files.go`, `files_test.go`
- Modify: `internal/storage/storage.go`, `migrate_test.go`, `storagetest/storagetest.go`

**Interfaces:**
- Produces:

```go
// attachment.go
// Attachment is a stored file's metadata. The file itself is in Files, under ID.
type Attachment struct {
	ID, RoomID                       string
	MessageID                        *string // nil until a message carries it
	UploaderHumanID, UploaderAgentID *string // at most one is set
	Name, ContentType                string
	Size, CreatedAtMs                int64
}

// on Repository
PutAttachment(ctx context.Context, a Attachment) error                                            // ErrConflict on duplicate id; MessageID is not written: a new attachment is on no message
GetAttachment(ctx context.Context, id string) (Attachment, error)                                 // ErrNotFound
AttachToMessage(ctx context.Context, id, messageID string) error                                  // ErrNotFound when it is missing or already on a message
ListAttachmentsInRange(ctx context.Context, roomID, firstID, lastID string) ([]Attachment, error) // those on messages in the id range; by message id, then id; empty, not nil

// files.go
// Files stores attachment bodies as files in one directory, each named by its attachment id.
type Files struct{ /* dir */ }
func OpenFiles(dir string) (*Files, error)                                          // creates dir (0700) if it is missing
func (f *Files) Create(ctx context.Context, id string, r io.Reader) (int64, error) // writes all of r; returns its size; a failed or cancelled write leaves nothing
func (f *Files) Open(id string) (io.ReadSeekCloser, error)                         // an error wrapping fs.ErrNotExist when there is none
func (f *Files) Remove(id string) error                                            // nil when there is none

// storagetest
func NewFiles(t testing.TB) *storage.Files // over t.TempDir()
```

- [ ] **Step 1: Write the failing tests**

`attachment_test.go`:

```go
func TestAttachment_PutAttachAndList(t *testing.T) {
	r := roomed(t)
	m1, m2 := msg("1", trip.ID, kai.ID), msg("2", trip.ID, kai.ID)
	require.NoError(t, r.PutMessage(ctx, m1))
	require.NoError(t, r.PutMessage(ctx, m2))
	a := storage.Attachment{ID: "01JA00000000000000000000A1", RoomID: trip.ID, UploaderAgentID: sp(scout.ID),
		Name: "report.pdf", ContentType: "application/pdf", Size: 1234, CreatedAtMs: 5}
	b := storage.Attachment{ID: "01JA00000000000000000000A2", RoomID: trip.ID, UploaderAgentID: sp(scout.ID),
		Name: "cat.png", ContentType: "image/png", Size: 99, CreatedAtMs: 6}
	require.NoError(t, r.PutAttachment(ctx, a))
	require.NoError(t, r.PutAttachment(ctx, b))
	assert.ErrorIs(t, r.PutAttachment(ctx, a), storage.ErrConflict)
	got, err := r.GetAttachment(ctx, a.ID)
	require.NoError(t, err)
	assert.Equal(t, a, got)
	_, err = r.GetAttachment(ctx, "nope")
	assert.ErrorIs(t, err, storage.ErrNotFound)

	none, err := r.ListAttachmentsInRange(ctx, trip.ID, m1.ID, m2.ID)
	require.NoError(t, err)
	assert.NotNil(t, none)
	assert.Empty(t, none, "an upload on no message is in no range")

	require.NoError(t, r.AttachToMessage(ctx, b.ID, m1.ID))
	require.NoError(t, r.AttachToMessage(ctx, a.ID, m1.ID))
	assert.ErrorIs(t, r.AttachToMessage(ctx, a.ID, m2.ID), storage.ErrNotFound, "an attachment is posted once")
	assert.ErrorIs(t, r.AttachToMessage(ctx, "nope", m2.ID), storage.ErrNotFound)
	a.MessageID, b.MessageID = sp(m1.ID), sp(m1.ID)
	list, _ := r.ListAttachmentsInRange(ctx, trip.ID, m1.ID, m2.ID)
	assert.Equal(t, []storage.Attachment{a, b}, list)
	later, _ := r.ListAttachmentsInRange(ctx, trip.ID, m2.ID, m2.ID)
	assert.Empty(t, later)
}
```

`files_test.go`:

```go
func TestFiles_CreateOpenRemove(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "attachments")
	f, err := storage.OpenFiles(dir)
	require.NoError(t, err)
	info, err := os.Stat(dir)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), info.Mode().Perm())

	n, err := f.Create(ctx, "01JA00000000000000000000A1", strings.NewReader("hello"))
	require.NoError(t, err)
	assert.EqualValues(t, 5, n)
	rd, err := f.Open("01JA00000000000000000000A1")
	require.NoError(t, err)
	body, _ := io.ReadAll(rd)
	require.NoError(t, rd.Close())
	assert.Equal(t, "hello", string(body))

	_, err = f.Open("01JA00000000000000000000A2")
	assert.ErrorIs(t, err, fs.ErrNotExist)
	require.NoError(t, f.Remove("01JA00000000000000000000A1"))
	require.NoError(t, f.Remove("01JA00000000000000000000A1"), "removing nothing is not an error")
	_, err = f.Open("01JA00000000000000000000A1")
	assert.ErrorIs(t, err, fs.ErrNotExist)
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("connection reset") }

func TestFiles_AFailedWriteLeavesNothing(t *testing.T) {
	dir := t.TempDir()
	f, err := storage.OpenFiles(dir)
	require.NoError(t, err)
	_, err = f.Create(ctx, "01JA00000000000000000000A1", io.MultiReader(strings.NewReader("half"), failingReader{}))
	require.Error(t, err)
	entries, _ := os.ReadDir(dir)
	assert.Empty(t, entries, "no file and no temporary file")

	gone, cancel := context.WithCancel(ctx)
	cancel()
	_, err = f.Create(gone, "01JA00000000000000000000A1", strings.NewReader("x"))
	assert.ErrorIs(t, err, context.Canceled)
	entries, _ = os.ReadDir(dir)
	assert.Empty(t, entries)
}

// Review Focus 2: a name is never a path.
func TestFiles_RejectsIDsThatAreNotPlainNames(t *testing.T) {
	f, err := storage.OpenFiles(t.TempDir())
	require.NoError(t, err)
	for _, bad := range []string{"", ".", "..", "../escape", "a/b", `a\b`, "/etc/passwd"} {
		_, err := f.Create(ctx, bad, strings.NewReader("x"))
		assert.Error(t, err, "%q", bad)
		_, err = f.Open(bad)
		assert.Error(t, err, "%q", bad)
		assert.Error(t, f.Remove(bad), "%q", bad)
	}
}
```

In `migrate_test.go` add `assert.Contains(t, tables(t, db), "attachments")`.

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./cmd/hermes-chat/internal/storage/`
Expected: FAIL to compile (`storage.Attachment undefined`, `storage.OpenFiles undefined`).

- [ ] **Step 3: Write the migration `00009_attachments.sql`**

Verified up, down, and through sqlc while planning.

```sql
-- +goose Up
CREATE TABLE attachments (
    id                TEXT    NOT NULL PRIMARY KEY,
    room_id           TEXT    NOT NULL REFERENCES rooms (id) ON DELETE CASCADE,
    message_id        TEXT    REFERENCES messages (id) ON DELETE CASCADE,
    uploader_human_id TEXT    REFERENCES humans (id) ON DELETE SET NULL,
    uploader_agent_id TEXT    REFERENCES agents (id),
    name              TEXT    NOT NULL,
    content_type      TEXT    NOT NULL,
    size              INTEGER NOT NULL CHECK (size >= 0),
    created_at        INTEGER NOT NULL,
    CHECK (uploader_human_id IS NULL OR uploader_agent_id IS NULL)
) STRICT;

CREATE INDEX attachments_message ON attachments (message_id, id) WHERE message_id IS NOT NULL;

-- +goose Down
DROP TABLE attachments;
```

- [ ] **Step 4: Add `queries/attachments.sql` and run `just generate`**

```sql
-- name: InsertAttachment :exec
INSERT INTO attachments (id, room_id, uploader_human_id, uploader_agent_id, name, content_type, size, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetAttachment :one
SELECT id, room_id, message_id, uploader_human_id, uploader_agent_id, name, content_type, size, created_at FROM attachments WHERE id = ?;

-- name: AttachToMessage :execrows
UPDATE attachments SET message_id = ? WHERE id = ? AND message_id IS NULL;

-- name: ListAttachmentsInRange :many
SELECT id, room_id, message_id, uploader_human_id, uploader_agent_id, name, content_type, size, created_at FROM attachments
WHERE room_id = ? AND message_id >= ? AND message_id <= ?
ORDER BY message_id, id;
```

Run: `just generate`. sqlc names the range parameters `MessageID` and `MessageID_2`, both `sql.NullString`.

- [ ] **Step 5: Implement `attachment.go` and `files.go`**

`attachment.go` follows `message.go`. In `files.go`: an id is accepted only when it is not empty, not `.` or `..`, and has no `/` or `\`. `Create` copies to `<dir>/<id>.tmp` (mode `0600`), stopping with `ctx.Err()` when the context ends, syncs, closes, and renames to `<dir>/<id>`; on any failure it removes the temporary file. `storagetest.NewFiles` wraps `OpenFiles(t.TempDir())`.

- [ ] **Step 6: Run the tests**

Run: `just check`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add cmd/hermes-chat/internal/storage
git commit -m "feat(storage): attachment rows and the file store"
```

---

### Task 8: Attachments in the bus, and the wiring

**Files:**
- Create: `internal/bus/attachment.go`, `attachment_test.go`
- Modify: `internal/bus/bus.go`, `message.go`, `room_test.go` (`newWorld`), `config.go`, `config_test.go`, `wires.go`

**Interfaces:**
- Consumes: Task 7's `storage.Attachment`, four methods, and `*storage.Files`.
- Produces:

```go
// bus.go
var ErrTooLarge = errors.New("too large")

// FileStore keeps attachment bodies, each under its attachment's id. *storage.Files is one.
type FileStore interface {
	Create(ctx context.Context, id string, r io.Reader) (int64, error)
	Open(id string) (io.ReadSeekCloser, error) // an error wrapping fs.ErrNotExist when there is none
	Remove(id string) error
}

type Option func(*service)
func WithFiles(files FileStore) Option
func NewService(repo storage.Repository, clock Clock, opts ...Option) *service // existing callers compile unchanged

// Service
UploadAttachment(ctx context.Context, actor Actor, roomID ulid.ULID, name, contentType string, body io.Reader) (Attachment, error) // agent member only: ErrForbidden for a human; ErrNotFound, ErrValidation, ErrRoomArchived, ErrPaused, ErrTooLarge
// PostMessageWithAttachments is PostMessage carrying files the actor uploaded to this room. The text may be blank when there is at least one.
PostMessageWithAttachments(ctx context.Context, actor Actor, roomID ulid.ULID, text string, replyTo ulid.ULID, attachmentIDs []ulid.ULID) (Message, error)
OpenAttachment(ctx context.Context, actor Actor, roomID, attachmentID ulid.ULID) (Attachment, io.ReadSeekCloser, error) // human member; ErrNotFound, also for an upload on no message yet; the caller closes the file

// attachment.go
const (
	MaxAttachmentBytes       = 25 << 20
	MaxAttachmentsPerMessage = 10
)
type Attachment struct {
	ID, RoomID  ulid.ULID
	MessageID   ulid.ULID // zero until a message carries it
	Name        string
	ContentType string
	Size        int64
	CreatedAt   time.Time
}
// Image reports whether the content type is one a browser may show inline: image/png, image/jpeg, image/gif, or image/webp.
func (a Attachment) Image() bool

// message.go: Message gains  Attachments []Attachment // in upload order; never nil after a load from storage

// config.go
// Config gains  AttachmentsDir string `split_words:"true"` // HERMES_CHAT_ATTACHMENTS_DIR
func (c Config) attachmentsDir() string // AttachmentsDir, or "attachments" beside the database file when it is empty
```

Validation messages, verbatim:

| Condition | `ValidationError.Message` |
|---|---|
| name (its last path element, split on `/` and `\`, trimmed) is empty, over 255 characters, or has a control character | `file name must be 1-255 characters with no control characters` |
| the body is empty | `file is empty` |
| more than 10 ids, a repeat, or an id that is not the actor's own upload in this room, not yet posted | `attachment_ids must be at most 10 of your own uploads in this room, not yet posted, each at most once` |

`contentType` is stored as given; `""` is stored as `application/octet-stream`. The app supplies it (Task 9).

- [ ] **Step 1: Write the failing tests in `attachment_test.go`**

First make every bus test service a real file store: in `newWorld` (`room_test.go`), build the service with `bus.NewService(r, clock, bus.WithFiles(storagetest.NewFiles(t)))`.

```go
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
```

In `config_test.go`, in the style of the tests there:

```go
func TestConfig_AttachmentsDir(t *testing.T) {
	t.Setenv("HERMES_CHAT_DB_PATH", "/var/lib/hermes/chat.db")
	c, err := loadConfig()
	require.NoError(t, err)
	assert.Equal(t, "/var/lib/hermes/attachments", c.attachmentsDir())
	t.Setenv("HERMES_CHAT_ATTACHMENTS_DIR", "/srv/files")
	c, err = loadConfig()
	require.NoError(t, err)
	assert.Equal(t, "/srv/files", c.attachmentsDir())
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./cmd/hermes-chat/...`
Expected: FAIL to compile (`bus.WithFiles undefined`).

- [ ] **Step 3: Add the option, the error, the types, and the field**

As in the Interfaces block. A service built without `WithFiles` answers `UploadAttachment` and `OpenAttachment` with a plain `errors.New("no file store configured")` (a 500: it is a wiring mistake).

- [ ] **Step 4: Implement `UploadAttachment`**

The order matters, because the body is read outside any transaction:

1. `ErrForbidden` unless the actor is an agent. Validate the name.
2. Read-only gate, no transaction: `roomForPoster` on `s.repo`, `ErrRoomArchived`, `paused()` → `ErrPaused`. This refuses before any byte of the body is read.
3. New id from `s.now()`. `files.Create(ctx, id, io.LimitReader(body, MaxAttachmentBytes+1))`. A size over `MaxAttachmentBytes` → `Remove`, `ErrTooLarge`; zero → `Remove`, `file is empty`.
4. `s.repo.InTx` (no event, so not `s.record`): repeat step 2's three checks inside the transaction, then `PutAttachment`. If the transaction fails, `Remove` the file and return its error.

- [ ] **Step 5: Implement `PostMessageWithAttachments` and the loading**

Move `PostMessage`'s body into `func (s *service) postMessage(ctx, actor, roomID, text, replyTo, attachmentIDs)`; both exported methods call it. Text may be blank only when `attachmentIDs` is not empty (a blank body is stored as `""`). Check the count and repeats before the transaction. Inside it, after the room checks and before `PutMessage`'s event: each id must `GetAttachment` to a row in this room whose uploader is the actor and whose `MessageID` is nil, then `AttachToMessage`; any failure is the `attachment_ids` validation error, which rolls the message back. `messagesFromRows` fills `Message.Attachments` with one `ListAttachmentsInRange`, as it does deliveries.

- [ ] **Step 6: Implement `OpenAttachment`**

`roomForMember` (so an agent is `ErrForbidden`); the attachment must be in that room and on a message, else `ErrNotFound`; `files.Open`, mapping an error that `errors.Is` `fs.ErrNotExist` to `ErrNotFound`.

- [ ] **Step 7: Wire it for `serve`**

`config.go`: the field and `attachmentsDir()`. `wires.go`: `storage.OpenFiles(cfg.attachmentsDir())` after the migration, then `bus.NewService(storage.NewRepo(db), time.Now, bus.WithFiles(files))`. The admin CLI's `openSvc` is unchanged: it needs no files.

- [ ] **Step 8: Run the tests**

Run: `just check`
Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add cmd/hermes-chat
git commit -m "feat(bus): agents upload files and post them; room members open them"
```

---

### Task 9: Attachments over HTTP

**Files:**
- Create: `internal/app/attachment.go`, `attachment_test.go`
- Modify: `internal/app/app.go`, `app_test.go` (`realSvc`), `json.go`, `errors.go`, `errors_test.go`, `delivery.go`, `auth_test.go` (`apiEngine`), `delivery_test.go` (`agentEngine`), `routes.go`, `routes_test.go`, `README.md`

**Interfaces:**
- Consumes: Task 8's three `Service` methods, `Attachment`, `Attachment.Image`, `ErrTooLarge`, `MaxAttachmentBytes`.
- Produces: `Controller` methods `AgentUploadAttachment` and `GetAttachment`; `Options.TransferTimeout time.Duration` (zero means `DefaultTransferTimeout = 10 * time.Minute`); and:

| Route | Request | Reply |
|---|---|---|
| `POST /agent/rooms/:room/attachments?name=report.pdf` | the file's bytes as the body | 201, the attachment |
| `POST /agent/rooms/:room/messages` | body gains optional `"attachment_ids":["01…"]` | as before |
| `GET /api/rooms/:room/attachments/:attachment` | | 200, the file |

```
attachment     {"id":"01…","name":"cat.png","content_type":"image/png","size":40,"image":true}
message gains  "attachments":[attachment…]     never null
```

Error row: `{bus.ErrTooLarge, http.StatusRequestEntityTooLarge, "too_large", "the file is larger than 25 MiB"}`.

Download headers, all of them on every 200:

| Header | When `Image()` | Otherwise |
|---|---|---|
| `Content-Type` | the stored type | `application/octet-stream` |
| `Content-Disposition` | `mime.FormatMediaType("inline", {"filename": name})` | `mime.FormatMediaType("attachment", {"filename": name})` |
| `X-Content-Type-Options` | `nosniff` | `nosniff` |
| `Content-Security-Policy` | `sandbox` | `sandbox` |
| `Cache-Control` | `private, max-age=86400` | `private, max-age=86400` |

- [ ] **Step 1: Write the failing tests in `attachment_test.go`**

First give app tests a file store: `realSvc` in `app_test.go` becomes `bus.NewService(storagetest.NewRepo(t), time.Now, bus.WithFiles(storagetest.NewFiles(t)))`.

```go
var png = "\x89PNG\r\n\x1a\n" + strings.Repeat("\x00", 32)

// uploadAs sends body as the agent's file; hdr is alternating header names and values.
func uploadAs(e *gin.Engine, token, room, name, body string, hdr ...string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/agent/rooms/"+room+"/attachments?name="+url.QueryEscape(name), strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+token)
	for i := 0; i+1 < len(hdr); i += 2 {
		r.Header.Set(hdr[i], hdr[i+1])
	}
	w := httptest.NewRecorder()
	e.ServeHTTP(w, r)
	return w
}

func TestAttachments_UploadAndPost(t *testing.T) {
	r := newAgentRig(t)
	room := r.room.ID.String()
	w := uploadAs(r.e, r.scoutT, room, "cat.png", png, "Content-Type", "text/html")
	require.Equal(t, 201, w.Code, w.Body.String())
	a := obj(t, w)
	assert.Equal(t, "cat.png", a["name"])
	assert.Equal(t, "image/png", a["content_type"], "sniffed; the claimed type is ignored")
	assert.Equal(t, true, a["image"])
	assert.EqualValues(t, len(png), a["size"])

	w = agentReq(r.e, r.scoutT, "POST", "/agent/rooms/"+room+"/messages", fmt.Sprintf(`{"text":"","attachment_ids":[%q]}`, a["id"]))
	require.Equal(t, 201, w.Code, w.Body.String())
	assert.JSONEq(t, string(mustJSON(t, []any{a})), string(mustJSON(t, obj(t, w)["attachments"])))
	w = agentReq(r.e, r.scoutT, "POST", "/agent/rooms/"+room+"/messages", `{"text":"plain"}`)
	assert.Equal(t, []any{}, obj(t, w)["attachments"])

	assert.Equal(t, 400, uploadAs(r.e, r.scoutT, room, "", png).Code)
	assert.Equal(t, 400, uploadAs(r.e, r.scoutT, room, "empty.txt", "").Code)
	assert.Equal(t, 404, uploadAs(r.e, r.atlasT, room, "cat.png", png).Code)
	assert.Equal(t, 400, agentReq(r.e, r.scoutT, "POST", "/agent/rooms/"+room+"/messages", `{"text":"x","attachment_ids":["nope"]}`).Code)
	big := httptest.NewRequest("POST", "/agent/rooms/"+room+"/attachments?name=big.bin", strings.NewReader("x"))
	big.Header.Set("Authorization", "Bearer "+r.scoutT)
	big.ContentLength = bus.MaxAttachmentBytes + 1
	w = httptest.NewRecorder()
	r.e.ServeHTTP(w, big)
	assert.Equal(t, 413, w.Code, "refused on the declared length, before reading")
	assert.JSONEq(t, `{"error":{"code":"too_large","message":"the file is larger than 25 MiB"}}`, w.Body.String())
}

// posted has scout upload one file to its DM with kai and post it, and returns the download path.
func posted(t *testing.T, c chat, name, contentType, body string) string {
	t.Helper()
	kai, scout := bus.Actor{Kind: bus.ActorHuman, ID: c.kaiH.ID}, bus.Actor{Kind: bus.ActorAgent, ID: c.scout.ID}
	room, _, err := c.svc.OpenDM(ctx, kai, c.scout.ID)
	require.NoError(t, err)
	a, err := c.svc.UploadAttachment(ctx, scout, room.ID, name, contentType, strings.NewReader(body))
	require.NoError(t, err)
	_, err = c.svc.PostMessageWithAttachments(ctx, scout, room.ID, "", ulid.Zero, []ulid.ULID{a.ID})
	require.NoError(t, err)
	return "/api/rooms/" + room.ID.String() + "/attachments/" + a.ID.String()
}

func TestAttachments_ImagesAreServedInline(t *testing.T) {
	c := newChat(t)
	path := posted(t, c, "cat.png", "image/png", png)
	w := c.call(c.kai, "GET", path, "")
	require.Equal(t, 200, w.Code)
	assert.Equal(t, png, w.Body.String())
	assert.Equal(t, "image/png", w.Header().Get("Content-Type"))
	assert.Equal(t, "inline; filename=cat.png", w.Header().Get("Content-Disposition"))
	assert.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, "sandbox", w.Header().Get("Content-Security-Policy"))
	assert.Equal(t, "private, max-age=86400", w.Header().Get("Cache-Control"))

	assert.JSONEq(t, notFound, c.call(c.ada, "GET", path, "").Body.String(), "ada is not in the room")
	assert.Equal(t, 401, c.call("", "GET", path, "").Code)
	assert.JSONEq(t, notFound, c.call(c.kai, "GET", path[:len(path)-26]+"nope", "").Body.String())
}

// Review Focus 2.
func TestAttachments_UnsafeTypesAreDownloads(t *testing.T) {
	c := newChat(t)
	for _, f := range []struct{ name, stored, body string }{
		{"cat.png", "text/html; charset=utf-8", "<html><script>alert(1)</script></html>"},
		{"logo.svg", "image/svg+xml", `<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"/>`},
		{`we"ird; name.pdf`, "application/pdf", "%PDF-1.7"},
		{"résumé.pdf", "application/pdf", "%PDF-1.7"},
	} {
		w := c.call(c.kai, "GET", posted(t, c, f.name, f.stored, f.body), "")
		require.Equal(t, 200, w.Code, f.name)
		assert.Equal(t, "application/octet-stream", w.Header().Get("Content-Type"), f.name)
		disposition, params, err := mime.ParseMediaType(w.Header().Get("Content-Disposition"))
		require.NoError(t, err, f.name)
		assert.Equal(t, "attachment", disposition, f.name)
		assert.Equal(t, f.name, params["filename"], "the name survives the header intact")
		assert.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
	}
	// what the upload route itself stores for a file that lies about being an image
	r := newAgentRig(t)
	w := uploadAs(r.e, r.scoutT, r.room.ID.String(), "cat.png", "<html><script>alert(1)</script></html>", "Content-Type", "image/png")
	require.Equal(t, 201, w.Code)
	assert.Equal(t, false, obj(t, w)["image"])
	assert.Equal(t, "text/html; charset=utf-8", obj(t, w)["content_type"])
	w = uploadAs(r.e, r.scoutT, r.room.ID.String(), "../../etc/passwd", "root:x:0:0", "Content-Type", "image/png")
	require.Equal(t, 201, w.Code)
	assert.Equal(t, "passwd", obj(t, w)["name"])
}

// Review Focus 3: the server's write timeout is counted from the request's headers, so a slow
// upload would otherwise get no response at all.
func TestAttachments_SlowUploadOutlivesTheWriteTimeout(t *testing.T) {
	r := newAgentRig(t)
	srv := httptest.NewUnstartedServer(r.e)
	srv.Config.WriteTimeout = 100 * time.Millisecond
	srv.Start()
	defer srv.Close()

	pr, pw := io.Pipe()
	go func() {
		_, _ = pw.Write([]byte(png[:8]))
		time.Sleep(400 * time.Millisecond) // well past the write timeout
		_, _ = pw.Write([]byte(png[8:]))
		pw.Close()
	}()
	req, _ := http.NewRequest("POST", srv.URL+"/agent/rooms/"+r.room.ID.String()+"/attachments?name=cat.png", pr)
	req.Header.Set("Authorization", "Bearer "+r.scoutT)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, 201, resp.StatusCode)
}
```

In `errors_test.go`, add the row: `bus.ErrTooLarge` → 413, `too_large`, `the file is larger than 25 MiB`. In `routes_test.go`, add the two new routes to `TestRoutes_Table`.

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./cmd/hermes-chat/...`
Expected: FAIL to compile (`ctrl.AgentUploadAttachment undefined`).

- [ ] **Step 3: Implement `AgentUploadAttachment`**

In this order: `pathID(c, "room")`; a `c.Request.ContentLength` over `bus.MaxAttachmentBytes` → `writeError(c, bus.ErrTooLarge)` without reading; set the read and write deadlines to now plus `h.opts.TransferTimeout` through `http.NewResponseController(c.Writer)`, as `Events` sets its write deadline; read up to 512 bytes of the body (`io.ReadFull`, where `io.EOF` and `io.ErrUnexpectedEOF` mean a short file, not a failure); `contentType := http.DetectContentType(head)`; call `UploadAttachment` with `c.Query("name")`, that type, and `io.MultiReader(bytes.NewReader(head), c.Request.Body)`; answer 201 with `attachmentJSON`. The request's `Content-Type` header is never read.

- [ ] **Step 4: Implement `GetAttachment`, the JSON, and `attachment_ids`**

`GetAttachment`: `OpenAttachment`, `defer` the close, set the write deadline to now plus `TransferTimeout`, set the five headers from the table, then `http.ServeContent(c.Writer, c.Request, "", att.CreatedAt, file)` (the empty name keeps it from guessing a type; it serves ranges). `messageBody` gains `Attachments []attachmentBody` with tag `json:"attachments"`, never nil. `AgentPostMessage`'s body gains `AttachmentIDs []string` with tag `json:"attachment_ids"`, each parsed with `parseID(c, "attachment_ids", …)`, and calls `PostMessageWithAttachments`. Add the error row, `Options.TransferTimeout` with its default in `NewController`, and the routes in `routes.go`, `apiEngine`, and `agentEngine`.

- [ ] **Step 5: Run the tests**

Run: `just check`
Expected: PASS.

- [ ] **Step 6: Document it in `README.md`**

`HERMES_CHAT_ATTACHMENTS_DIR` in the configuration table (default: `attachments` beside the database file; created `0700` when `serve` starts). The two route rows and the changed one, the `too_large` error row, the `attachment` shape and `attachments` on a message. A section "Attachments": the two steps; the 25 MiB and 10-file limits; that the type is sniffed from the first 512 bytes and the declared one ignored; the four inline image types and that everything else downloads as `application/octet-stream`; that only an upload already on a message can be fetched, and only by a human in its room; the 10-minute transfer deadline; that the file's name on disk is its id.

- [ ] **Step 7: Commit**

```bash
git add cmd/hermes-chat README.md
git commit -m "feat(app): upload, post, and download attachments, with only safe images shown inline"
```

---

### Task 10: End to end with the fake agent

**Files:**
- Create: `stage6_e2e_test.go`
- Modify: `internal/fakeagent/fakeagent.go`, `README.md`, `AGENTS.md`

**Interfaces:**
- Consumes: every route from Tasks 3, 6, and 9; the e2e helpers in `agent_e2e_test.go`, `offswitch_e2e_test.go`, and `serve_test.go` (`newAgentWorld`, `openDM`, `post`, `openAgent`, `frameOf`, `apiCall`, `runCmdIn`, `login`).
- Produces, on `fakeagent.Client` (the package still imports only the standard library and `internal/sse`):

```go
func (c *Client) Prompt(ctx context.Context, roomID, kind, text string, choices []string) (messageID string, err error)
func (c *Client) ExpirePrompt(ctx context.Context, messageID string) error
func (c *Client) SuggestNote(ctx context.Context, messageID, text string) (entryID string, err error) // 200 and 201 both succeed
func (c *Client) Upload(ctx context.Context, roomID, name string, body io.Reader) (attachmentID string, err error)
func (c *Client) PostMessageWith(ctx context.Context, roomID, text string, attachmentIDs []string) (messageID string, err error)
```

- [ ] **Step 1: Add the five client methods**

Follow the existing methods: build the body by hand with `jq`, check the status, `decodeError` otherwise. `Upload` cannot use `c.do` (its body is a reader, not JSON): build the request directly with the bearer header and `?name=` query-escaped.

- [ ] **Step 2: Write the tests in `stage6_e2e_test.go`**

```go
// Package main: end-to-end coverage of stage 6 with a real server and the fake agent.
package main

// data decodes one frame's JSON data.
func data(t *testing.T, f sse.Frame) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(f.Data), &m), f.Data)
	return m
}

func TestStage6E2E_AnApprovalWaitsForTheHuman(t *testing.T) {
	w := newAgentWorld(t)
	room := openDM(t, w, w.scoutID)
	scout, stream := w.agent(), openAgent(t, w)
	bg := context.Background()
	m1 := post(t, w, room, "clean the build")
	frameOf(t, stream, "delivery")
	require.NoError(t, scout.TurnWorking(bg, m1))

	ask, err := scout.Prompt(bg, room, "approval", "run `rm -rf build`?", []string{"once", "deny"})
	require.NoError(t, err)
	_, got := apiCall(t, w.human, "GET", w.api, "/api/rooms/"+room, "")
	assert.EqualValues(t, 1, got["open_prompts"], "the badge")

	code, answered := apiCall(t, w.human, "POST", w.api, "/api/rooms/"+room+"/messages/"+ask+"/answer", `{"answer":"once"}`)
	require.Equal(t, 200, code)
	assert.Equal(t, "answered", answered["prompt"].(map[string]any)["state"])
	assert.JSONEq(t, fmt.Sprintf(`{"room_id":%q,"message_id":%q,"answer":"once"}`, room, ask), frameOf(t, stream, "prompt.answered").Data)
	_, got = apiCall(t, w.human, "GET", w.api, "/api/rooms/"+room, "")
	assert.EqualValues(t, 0, got["open_prompts"])

	// a second question, and the human hits pause instead of answering
	second, err := scout.Prompt(bg, room, "clarify", "delete the cache too?", []string{"yes", "no"})
	require.NoError(t, err)
	code, _ = apiCall(t, w.human, "POST", w.api, "/api/rooms/"+room+"/pause", "")
	require.Equal(t, 200, code)
	frameOf(t, stream, "pause")
	code, body := apiCall(t, w.human, "POST", w.api, "/api/rooms/"+room+"/messages/"+second+"/answer", `{"answer":"yes"}`)
	assert.Equal(t, 409, code)
	assert.Equal(t, "prompt_closed", body["error"].(map[string]any)["code"])
	_, err = scout.Prompt(bg, room, "clarify", "hello?", nil)
	assert.ErrorContains(t, err, "409 paused")
	assert.NoError(t, scout.ExpirePrompt(bg, second), "the plugin may still tidy up")
}

func TestStage6E2E_ASuggestionReachesAgentsOnlyOnceApproved(t *testing.T) {
	w := newAgentWorld(t)
	room := openDM(t, w, w.scoutID)
	scout, stream := w.agent(), openAgent(t, w)
	bg := context.Background()
	turn := func(text string) (id string, note any) {
		id = post(t, w, room, text)
		note = data(t, frameOf(t, stream, "delivery"))["note"]
		return
	}

	m1, note := turn("hello")
	assert.Nil(t, note)
	entry, err := scout.SuggestNote(bg, m1, "prefers trains")
	require.NoError(t, err)
	again, err := scout.SuggestNote(bg, m1, "prefers trains")
	require.NoError(t, err)
	assert.Equal(t, entry, again)
	require.NoError(t, scout.TurnDone(bg, m1))

	code, _ := apiCall(t, w.human, "PUT", w.api, "/api/note", `{"text":"I live in Lisbon."}`)
	require.Equal(t, 200, code)
	m2, note := turn("again")
	assert.Equal(t, "I live in Lisbon.", note, "the suggestion is still in quarantine")
	require.NoError(t, scout.TurnDone(bg, m2))

	code, n := apiCall(t, w.human, "PATCH", w.api, "/api/note/entries/"+entry, `{"approved":true}`)
	require.Equal(t, 200, code)
	assert.Len(t, n["entries"], 1)
	_, note = turn("and again")
	assert.Equal(t, "I live in Lisbon.\n\n- prefers trains", note)
}

func TestStage6E2E_AFileGoesUpAndOnlyMembersGetIt(t *testing.T) {
	w := newAgentWorld(t)
	runCmdIn(t, pw+"\n", "user", "add", "ada") // the CLI may run beside the server
	room := openDM(t, w, w.scoutID)
	scout := w.agent()
	bg := context.Background()
	png := "\x89PNG\r\n\x1a\n" + strings.Repeat("\x00", 32)

	pic, err := scout.Upload(bg, room, "chart.png", strings.NewReader(png))
	require.NoError(t, err)
	page, err := scout.Upload(bg, room, "report.html", strings.NewReader("<html><script>alert(1)</script></html>"))
	require.NoError(t, err)
	msg, err := scout.PostMessageWith(bg, room, "the report", []string{pic, page})
	require.NoError(t, err)
	_, got := apiCall(t, w.human, "GET", w.api, "/api/rooms/"+room+"/messages/"+msg, "")
	assert.Len(t, got["attachments"], 2)

	fetch := func(client *http.Client, id string) *http.Response {
		resp, err := client.Get(w.api + "/api/rooms/" + room + "/attachments/" + id)
		require.NoError(t, err)
		t.Cleanup(func() { resp.Body.Close() })
		return resp
	}
	resp := fetch(w.human, pic)
	require.Equal(t, 200, resp.StatusCode)
	body, _ := io.ReadAll(resp.Body)
	assert.Equal(t, png, string(body))
	assert.Equal(t, "image/png", resp.Header.Get("Content-Type"))
	assert.Equal(t, "inline; filename=chart.png", resp.Header.Get("Content-Disposition"))
	resp = fetch(w.human, page)
	assert.Equal(t, "application/octet-stream", resp.Header.Get("Content-Type"))
	assert.Equal(t, "attachment; filename=report.html", resp.Header.Get("Content-Disposition"))

	jar, _ := cookiejar.New(nil)
	ada := &http.Client{Jar: jar}
	require.Equal(t, 200, login(t, ada, w.api, "ada", pw).StatusCode)
	assert.Equal(t, 404, fetch(ada, pic).StatusCode)
	assert.Equal(t, 401, fetch(http.DefaultClient, pic).StatusCode)

	// the files sit beside the database, named by id
	entries, err := os.ReadDir(filepath.Join(filepath.Dir(os.Getenv("HERMES_CHAT_DB_PATH")), "attachments"))
	require.NoError(t, err)
	names := []string{}
	for _, e := range entries {
		names = append(names, e.Name())
	}
	assert.ElementsMatch(t, []string{pic, page}, names)
}
```

- [ ] **Step 3: Run them**

Run: `go test ./cmd/hermes-chat/ -run 'Stage6E2E' -v`
Expected: PASS, three tests. A failure here is a bug in an earlier task: fix it there, with a test at that layer.

- [ ] **Step 4: Update `README.md` and `AGENTS.md`**

`README.md`, "Known limits", add:

- A prompt nobody answers and its agent never expires stays open, with its badge, until the room is paused or archived.
- An answer an agent had not read when its room was paused is not sent again on resume.
- Note changes write no event: another open tab sees a new suggestion only when it next loads the note.
- An upload that is never posted, and the files of a room deleted with its human, stay on disk; nothing sweeps the attachments directory yet.
- Humans cannot upload yet (v1.5); the table already has the column for it.

`AGENTS.md`: "stages 1–6" in the overview; `prompt.go`, `note.go`, `attachment.go` in the `app`, `bus`, and `storage` bullets, and `files.go` (the file store) under `storage`; `attachments`, `notes`, `prompts` in the queries row of the Key Directories table; under Configuration, that `serve` opens the attachments directory in `wires.go`; and extend the off-switch bullet: "Closing a room also expires its open prompts (`expireOpenPrompts`), and a prompt, a note suggestion, and an upload are agent writes that check `paused()`."

- [ ] **Step 5: Run everything**

Run: `just check`
Expected: PASS (`sqlc diff` clean, vet clean, all packages ok).

- [ ] **Step 6: Commit**

```bash
git add cmd/hermes-chat README.md AGENTS.md
git commit -m "test: prompts, the note, and attachments end to end with the fake agent"
```
