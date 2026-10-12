# Stage 8: Shapes for the Web UI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close three gaps in the human API before the web UI is built on it: a retried post must not post twice, a room must say which of its agents is working, and a message must say whether it is still streaming.

**Architecture:** All three are additions to existing rows and JSON objects, through the usual path (migration → sqlc query → `storage.Repository` → `bus` → `app` JSON). The idempotency key is a column on `messages`, not a separate store: it lives as long as the message and needs no clean-up job. `working` is computed when a room is loaded. `streaming` is derived from two columns on the message row, and the server closes a stream its agent abandoned when the turn ends, so the flag cannot stay true for ever.

**Tech Stack:** Go 1.26, SQLite (`modernc.org/sqlite`), goose migrations, sqlc 1.31.1, gin, testify.

**Spec:** There is no separate stage 8 spec; Kai skipped the design conversation and delegated the decisions, which are recorded under "Decisions" below (all *Claude's calls*). The standing documents are `docs/superpowers/specs/2026-10-11-hermes-chat-design.md` (product; section 8 lists the room-list badges and the composer) and `docs/superpowers/specs/2026-10-11-chat-server-architecture-design.md` (layers, storage, testing). Read `AGENTS.md` first.

## Decisions

### 1. Idempotent posting

- `POST /api/rooms/:room/messages` and `POST /api/rooms/:room/agents/:agent/steer` accept an optional `client_id` string in the body. Absent, `null`, and `""` all mean "no key", and behave exactly as today.
- A key is at most 64 bytes of `A-Z a-z 0-9 - _` (a UUID or a ULID fits). Anything else is a 400 `validation` with the message `client_id must be at most 64 characters: letters, digits, "-" or "_"`. It is not trimmed.
- The key is stored on the message (`messages.client_id`) and is unique per (room, author human). The same key from another human, or from the same human in another room, is a different message.
- A repeat (same room, same human, same key) returns the message the first request made, with **200** instead of 201, and changes nothing: no second message, event, delivery, read-marker move, or turn-count reset. The first request wins; the repeat's text and `reply_to_id` are not compared and not used.
- The lookup happens after the membership check (a non-member still gets 404) and **before** every other check, so a retry still returns the original after the room was archived or paused, or after the steered turn ended.
- Post and steer share one key space within a room.
- The message JSON gains `client_id` (string or `null`), shown to every human in the room, so the UI can match a `message.posted` event to its pending bubble. The agent-facing message JSON does not carry it.
- Agent posts get no key in this stage (non-goal; it belongs with the plugin).

### 2. `working` on a room's agents

- Each entry of a room's `agents` array gains `working` (bool): true exactly when that agent has a delivery in state `working` in that room. A delivery that is only `delivered` is not working.
- It is computed when the room is loaded, like `connected`. No new event: the UI already receives `delivery.changed` and applies it (state `working` → true; `done`, `failed` → false).

### 3. `streaming` on a message

- The message JSON gains `streaming` (bool): true exactly when an agent's non-final edit has been stored for the message and the message is not finalized. A human message, and an agent message posted in one piece, are never streaming.
- `messages.edited_at` records the time of the last stored non-final edit (null until there is one).
- **When a turn ends (`done` or `failed`), the server finalizes every message the agent was still streaming in that room that is newer than the turn's message**: it sets `finalized_at`, stores the text of the newest non-final edit (which browsers have seen and the throttled row may lack), and writes one `message.edited` event per message. A real final edit arriving later still lands (finalizing twice is allowed today).
- A stream outside any turn (a scheduled report) is closed only by its own final edit. *(Amended after review: the server cannot tell which turn a message belongs to, so a report begun in a room while the agent has a turn running there is closed with that turn. Accepted and documented in README "Known limits".)*

## Global Constraints

- Layering is enforced by `TestLayering_ThisRepository`: `app` never imports `storage`; `bus` knows no HTTP; only `storage` imports `sqlcgen`.
- Never edit `storage/sqlcgen/`; run `just generate` after touching a migration or a query, and commit the output.
- Every sqlc query on `messages` that returns rows selects **all** columns in table order (`id, room_id, author_human_id, author_agent_id, reply_to_id, body, created_at, finalized_at, client_id, edited_at`), so sqlc keeps returning `sqlcgen.Message`.
- Validation lives in `bus` and is a `ValidationError` carrying the user-visible text; handlers do not validate.
- Writes that must land together run in one `s.record(...)` transaction with their event rows.
- Tests: testify `assert`/`require`, black-box `_test` packages, real layers below (no mocks), existing helpers (`newWorld`, `shared`, `dmRoom`, `startedTurn`, `newChat`, `newAgentRig`, `roomed`).
- A changed route, body, or shape is updated in `docs/openapi.yaml` in the same change.
- `just check` is the gate. No cgo dependency.
- Commit messages follow the repository's style (`feat(bus): …`, `docs: …`).

## Review Focus

1. **A retry after the world moved on.** The room was archived, or the steered turn finished, between the first request and the retry. Expected: the original message, 200, not `room_archived` or `no_turn`. (Task 2)
2. **A repeat with different content.** Same key, different text. Expected: the original text comes back; exactly one `message.posted` event and one delivery exist. (Task 2)
3. **The same key from someone else, or somewhere else.** Another human in the room, or the same human in another room. Expected: two separate messages, both 201. (Tasks 1, 2)
4. **A malformed key.** 65 characters, a space, a slash. Expected: 400 and nothing posted. (Tasks 2, 5)
5. **An abandoned stream.** The agent streamed two edits, then reported `failed` (or was stopped) without a final edit. Expected: on a fresh load the message has `streaming: false`, and the human stream received `message.edited` for it. A one-piece agent message in the same turn gets no such event. (Task 3)
6. **`working` is per room.** The agent is working in one room and idle in another, or its delivery is only `delivered`. Expected: true only in the room with a `working` delivery. (Task 4)

## File Structure

| File | Change |
|---|---|
| `cmd/hermes-chat/internal/storage/migrations/00010_client_id_streaming.sql` | new: two columns, three indexes |
| `cmd/hermes-chat/internal/storage/queries/messages.sql`, `deliveries.sql` | columns added to every message select; three new queries |
| `cmd/hermes-chat/internal/storage/message.go`, `delivery.go`, `storage.go` | `Message` fields, three `Repository` methods |
| `cmd/hermes-chat/internal/bus/message.go`, `turn.go`, `delivery.go`, `room.go`, `bus.go` | key handling, stream closing, `Working`, `Streaming` |
| `cmd/hermes-chat/internal/app/json.go`, `message.go`, `room.go` | JSON fields, `client_id` in bodies, 200 on a repeat |
| `docs/openapi.yaml`, `README.md`, `AGENTS.md`, architecture spec | contract and prose |

---

### Task 1: Storage — columns, indexes, queries

**Files:**
- Create: `cmd/hermes-chat/internal/storage/migrations/00010_client_id_streaming.sql`
- Modify: `cmd/hermes-chat/internal/storage/queries/messages.sql`, `queries/deliveries.sql`, `message.go`, `delivery.go`, `storage.go`
- Generated: `cmd/hermes-chat/internal/storage/sqlcgen/` (via `just generate`)
- Test: `cmd/hermes-chat/internal/storage/message_test.go`, `delivery_test.go`, `migrate_test.go`

**Interfaces:**
- Produces, on `storage.Message`: `ClientID *string` (nil when the post carried no key) and `EditedAtMs *int64` (nil until a non-final edit was stored).
- Produces, on `storage.Repository`:
  - `PutMessage(ctx, m Message) error` — now also writes `ClientID`; `ErrConflict` on a duplicate (room, author human, client id) as well as a duplicate id.
  - `GetMessageByClientID(ctx context.Context, roomID, humanID, clientID string) (Message, error)` — `ErrNotFound`.
  - `SetMessageBody(ctx context.Context, id, body string, editedAtMs int64) error` — signature change: also sets `edited_at`.
  - `ListStreamingMessages(ctx context.Context, roomID, agentID, afterID string) ([]Message, error)` — the agent's messages in the room with `id > afterID`, `edited_at` set and `finalized_at` null, ascending id; empty, not nil.
  - `ListWorkingAgentIDs(ctx context.Context, roomID string) ([]string, error)` — distinct agents with a delivery in state `working` in the room; empty, not nil.

- [ ] **Step 1: Write the migration**

```sql
-- +goose Up
ALTER TABLE messages ADD COLUMN client_id TEXT;
ALTER TABLE messages ADD COLUMN edited_at INTEGER;
CREATE UNIQUE INDEX messages_client_id ON messages (room_id, author_human_id, client_id) WHERE client_id IS NOT NULL;
CREATE INDEX messages_streaming ON messages (room_id, author_agent_id, id) WHERE edited_at IS NOT NULL AND finalized_at IS NULL;
CREATE INDEX deliveries_working ON deliveries (room_id, agent_id) WHERE state = 'working';

-- +goose Down
DROP INDEX deliveries_working;
DROP INDEX messages_streaming;
DROP INDEX messages_client_id;
ALTER TABLE messages DROP COLUMN edited_at;
ALTER TABLE messages DROP COLUMN client_id;
```

The indexes are dropped first because SQLite refuses to drop a column an index names.

- [ ] **Step 2: Write the failing storage tests**

In `message_test.go` (use `roomed`, `msg`, `sp`, and the fixtures `trip`, `dm`, `kai`, `ada`, `scout`):

- `TestMessage_ClientIDRoundTrips`: put `msg("1", trip.ID, kai.ID)` with `ClientID: sp("k1")`; `GetMessageByClientID(ctx, trip.ID, kai.ID, "k1")` returns it with `ClientID` equal to `sp("k1")`; `GetMessage` of a message put without a key has `ClientID == nil`; `GetMessageByClientID(ctx, trip.ID, kai.ID, "nope")` is `storage.ErrNotFound`.
- `TestMessage_ClientIDIsUniquePerRoomAndHuman`: with message 1 as above, putting message 2 in `trip` by `kai` with `"k1"` is `storage.ErrConflict`; message 3 in `trip` by `ada` with `"k1"` and message 4 in `dm` by `kai` with `"k1"` both succeed; two messages with a nil key in one room by one human both succeed.
- `TestMessage_StreamingList`: put three agent messages by `scout` in `trip` (ids M1 < M2 < M3, `AuthorAgentID` set, no human author). `SetMessageBody(ctx, M2, "partial", 1791700002000)` and the same for M3; `FinalizeMessage` M3. Then `ListStreamingMessages(ctx, trip.ID, scout.ID, "")` returns exactly M2, with `Body == "partial"` and `*EditedAtMs == 1791700002000`; with `afterID` M2 it is empty and non-nil; for another agent id it is empty.

In `delivery_test.go`:

- `TestListWorkingAgentIDs`: with deliveries in `trip` for scout in states `working` and `done` (two messages) and none working in `dm`, `ListWorkingAgentIDs(ctx, trip.ID)` is `[]string{scout.ID}` and `ListWorkingAgentIDs(ctx, dm.ID)` is empty and non-nil. A delivery in `delivered` alone gives empty.

In `migrate_test.go`, follow whatever the existing tests assert about the latest version and up/down round trips, extended to version 10.

- [ ] **Step 3: Run them to see them fail**

Run: `go test ./cmd/hermes-chat/internal/storage/`
Expected: compile errors (`ClientID`, `GetMessageByClientID`, … undefined).

- [ ] **Step 4: Update the queries and regenerate**

`messages.sql`: add `client_id` to `InsertMessage`'s columns; append `client_id, edited_at` to the select list of `GetMessage`, `ListNewestMessages`, `ListMessagesBefore`; change `SetMessageBody` to `UPDATE messages SET body = ?, edited_at = ? WHERE id = ?`; add

```sql
-- name: GetMessageByClientID :one
SELECT id, room_id, author_human_id, author_agent_id, reply_to_id, body, created_at, finalized_at, client_id, edited_at FROM messages
WHERE room_id = ? AND author_human_id = ? AND client_id = ?;

-- name: ListStreamingMessages :many
SELECT id, room_id, author_human_id, author_agent_id, reply_to_id, body, created_at, finalized_at, client_id, edited_at FROM messages
WHERE room_id = ? AND author_agent_id = ? AND id > ? AND edited_at IS NOT NULL AND finalized_at IS NULL
ORDER BY id;
```

`deliveries.sql`: add

```sql
-- name: ListWorkingAgentIDs :many
SELECT DISTINCT agent_id FROM deliveries WHERE room_id = ? AND state = 'working' ORDER BY agent_id;
```

Run: `just generate`, then confirm `sqlcgen` still returns `sqlcgen.Message` (not a per-query row type) from all five message selects.

- [ ] **Step 5: Implement the repository methods**

Map the two new columns in `messageFromRow` and `PutMessage`; add the three methods and the changed `SetMessageBody` to `repo` and to the `Repository` interface in `storage.go`, each with the one-line contract comment the interface uses. Update the one existing caller of `SetMessageBody` in `bus/message.go` to pass `at.UnixMilli()` so the tree compiles.

- [ ] **Step 6: Run the tests**

Run: `just check-generated && go test ./cmd/hermes-chat/...`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add cmd/hermes-chat/internal/storage cmd/hermes-chat/internal/bus/message.go
git commit -m "feat(storage): a client id and an edit time on messages, and the working agents of a room"
```

---

### Task 2: Bus — a repeated post returns the first message

**Files:**
- Modify: `cmd/hermes-chat/internal/bus/bus.go` (interface), `message.go`, `turn.go`
- Modify: every caller of `Steer` (`app/room.go` and the tests) for the new signature
- Test: `cmd/hermes-chat/internal/bus/message_test.go`, `turn_test.go`

**Interfaces:**
- Consumes: `storage.Message.ClientID`, `Repository.GetMessageByClientID` (Task 1).
- Produces:
  - `const MaxClientIDBytes = 64`
  - `Message.ClientID string` — `""` when the post carried no key.
  - `PostMessageOnce(ctx context.Context, actor Actor, roomID ulid.ULID, clientID, text string, replyTo ulid.ULID) (msg Message, created bool, err error)` on `Service` — human only (`ErrForbidden` for an agent); `clientID == ""` behaves as `PostMessage` and reports `created == true`.
  - `Steer(ctx context.Context, actor Actor, roomID, agentID ulid.ULID, clientID, text string) (msg Message, created bool, err error)` — signature change.
  - `PostMessage` and `PostMessageWithAttachments` keep their signatures and carry no key.

- [ ] **Step 1: Write the failing tests**

In `bus/message_test.go`:

```go
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
	assert.Equal(t, "hello", again.Text)
	assert.Equal(t, before, w.kinds(t, w.kai)) // no second event
	msgs, err := w.s.ListMessages(ctx, w.kai, room.ID, ulid.Zero, 0)
	require.NoError(t, err)
	assert.Len(t, msgs, 1)
	assert.Len(t, msgs[0].Deliveries, 1)
}
```

Also, each as its own test:

- `TestPostMessageOnce_NoKeyPostsEveryTime`: two calls with `clientID ""` make two messages, both `created`, both with `ClientID == ""`.
- `TestPostMessageOnce_KeyIsPerHumanAndPerRoom`: in `shared`, kai and ada both post with `"k1"` → two messages; kai posts with `"k1"` in a second room → a third.
- `TestPostMessageOnce_RepeatSurvivesArchiving`: post with `"k1"`, archive the room with `UpdateRoom`, repeat → the original, `created == false`, no error; a *new* key in the archived room is still `ErrRoomArchived`.
- `TestPostMessageOnce_RepeatByANonMemberIsNotFound`: kai posts with `"k1"` in a room ada is not in; ada's call with `"k1"` is `ErrNotFound`.
- `TestPostMessageOnce_Validation` (table): `strings.Repeat("a", 65)`, `"a b"`, `"a/b"`, `"é"` → `ValidationError` with the message from Decisions; `strings.Repeat("a", 64)` and `"01JA-x_9"` are accepted. After a rejected call the room has no messages.
- `TestPostMessageOnce_AgentIsForbidden`: an agent actor gets `ErrForbidden`.
- `TestPostMessageOnce_RepeatDoesNotResetTheTurnCount`: use the turn-cap helpers (`connectedChannel`, `ping` in `turncap_test.go`) to put a channel one agent-woken turn below its cap after a keyed human post; repeat that human post; the next agent-woken turn still pauses the room with reason `turn_cap`.

In `bus/turn_test.go`:

- `TestSteer_RepeatReturnsTheFirst`: with `startedTurn`, steer with `"s1"` → `created`; drain scout's stream; steer again with `"s1"` and other text → same id, `created == false`, and scout's stream received no second `control.steer` event.
- `TestSteer_RepeatAfterTheTurnEnded`: steer with `"s1"`, report `TurnDone`, steer again with `"s1"` → the original, no `ErrNoTurn`; a new key is `ErrNoTurn`.
- `TestSteerAndPostShareAKeySpace`: post with `"k1"`, then steer with `"k1"` during the turn → returns the posted message, `created == false`.

Update the existing `Steer` call sites in tests to pass `""` and take three results.

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./cmd/hermes-chat/internal/bus/ -run 'PostMessageOnce|Steer'`
Expected: compile errors (`PostMessageOnce` undefined; `Steer` argument count).

- [ ] **Step 3: Implement**

- `validClientID(s string) bool` and `errClientID` in `bus/message.go`.
- Give the unexported `postMessage` a `clientID string` parameter and a `created bool` result. Inside the transaction, right after `roomForPoster` succeeds and before the archived check: when `clientID != ""`, call `GetMessageByClientID(row.ID, actor.ID.String(), clientID)`; on a hit, build it with `messagesFromRows`, set `posted`, and return nil without doing anything else. On a miss, continue and store the key on the new row.
- `PostMessageOnce` validates the key and the actor kind, then calls `postMessage`. `PostMessage` and `PostMessageWithAttachments` pass `""`.
- `Steer`: validate the key; do the same lookup right after `roomForMember`, before the archived, paused, agent-membership and working-turn checks.
- `messagesFromRows` sets `Message.ClientID` from the row.

- [ ] **Step 4: Run the tests**

Run: `go test ./cmd/hermes-chat/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/hermes-chat/internal
git commit -m "feat(bus): a post or a steer repeated with its client id returns the first message"
```

---

### Task 3: Bus — `Streaming`, and closing a stream its turn left open

**Files:**
- Modify: `cmd/hermes-chat/internal/bus/message.go`, `turn.go`
- Test: `cmd/hermes-chat/internal/bus/message_test.go` or `turn_test.go`

**Interfaces:**
- Consumes: `storage.Message.EditedAtMs`, `Repository.ListStreamingMessages`, `Repository.FinalizeMessage` (Task 1).
- Produces: `Message.Streaming bool` — true when the row has `EditedAtMs` set and `FinalizedAtMs` nil.

- [ ] **Step 1: Write the failing tests**

All in a DM (`dmRoom`), with `startedTurn` and scout's actor (`agentActor(w.scout)`):

- `TestMessage_StreamingFollowsEdits`: scout posts `"…"` (one piece): `GetMessage` shows `Streaming == false`. After `EditMessage(…, "par", false)`: true. After `EditMessage(…, "partial answer", true)`: false. Kai's own message is never streaming.
- `TestTurnEnd_ClosesAnAbandonedStream` (run for both `TurnDone` and `TurnFailed`): scout posts message A and edits it non-final to `"half"`; scout posts message B in one piece. Subscribe kai (`w.s.Subscribe(ctx, w.kai, bus.FromNow)`) before the turn report. Report the turn end. Then: `GetMessage(A)` has `Streaming == false` and `Text == "half"`; kai's drained events contain exactly one `bus.EventMessageEdited` whose subject is A, and none for B.
- `TestTurnEnd_LeavesOlderStreamsAlone`: scout streams message Z non-final **before** kai's first message (an unprompted post in the DM), then the turn runs and ends: Z is still `Streaming == true`.
- `TestTurnEnd_AFinalEditStillLands`: after the turn closed A, `EditMessage(A, "whole answer", true)` succeeds and `GetMessage(A).Text == "whole answer"`.
- `TestTurnWorking_ClosesNothing`: a `TurnWorking` report leaves a streaming message streaming.

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./cmd/hermes-chat/internal/bus/ -run 'Streaming|TurnEnd|TurnWorking_Closes'`
Expected: FAIL (`Streaming` undefined, then the abandoned stream stays open).

- [ ] **Step 3: Implement**

- `messagesFromRows` sets `Streaming`.
- In `turnReport` (`bus/turn.go`), in the existing `target == DeliveryDone || target == DeliveryFailed` branch, before `releaseHead`: `ListStreamingMessages(d.RoomID, actor.ID.String(), messageID.String())`; for each, `FinalizeMessage(id, row.Body, at.UnixMilli())` and `putEvent(EventMessageEdited, d.RoomID, id, at)`. Drop each id from `s.editWrites.last` the way `EditMessage` does on a final edit.

- [ ] **Step 4: Run the tests**

Run: `go test ./cmd/hermes-chat/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/hermes-chat/internal/bus
git commit -m "feat(bus): a message says whether it is streaming, and a finished turn closes its streams"
```

---

### Task 4: Bus — `Working` on a room's agents

**Files:**
- Modify: `cmd/hermes-chat/internal/bus/delivery.go` (`RoomAgent`), `room.go` (`buildRoomLayout`)
- Test: `cmd/hermes-chat/internal/bus/room_test.go` or `turn_test.go`

**Interfaces:**
- Consumes: `Repository.ListWorkingAgentIDs` (Task 1).
- Produces: `RoomAgent.Working bool` — whether the agent has a delivery in `working` in this room.

- [ ] **Step 1: Write the failing test**

```go
func TestRoom_AgentWorking(t *testing.T) {
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
	assert.False(t, working(room.ID)) // delivered is not working

	require.NoError(t, w.s.TurnWorking(ctx, agentActor(w.scout), m.ID))
	assert.True(t, working(room.ID))
	assert.False(t, working(other.ID)) // per room

	rooms, err := w.s.ListRooms(ctx, w.kai)
	require.NoError(t, err)
	for _, r := range rooms {
		assert.Equal(t, r.ID == room.ID, r.Agents[0].Working)
	}

	require.NoError(t, w.s.TurnDone(ctx, agentActor(w.scout), m.ID))
	assert.False(t, working(room.ID))
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./cmd/hermes-chat/internal/bus/ -run TestRoom_AgentWorking`
Expected: compile error (`Working` undefined).

- [ ] **Step 3: Implement**

In `buildRoomLayout`, one `ListWorkingAgentIDs(row.ID)` call per room, then set `Working` on each `RoomAgent` while building the list. Comment the field like its neighbor `Connected`.

- [ ] **Step 4: Run the tests**

Run: `go test ./cmd/hermes-chat/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/hermes-chat/internal/bus
git commit -m "feat(bus): a room says which of its agents is working"
```

---

### Task 5: HTTP — the new fields, `client_id` in the bodies, and the documents

**Files:**
- Modify: `cmd/hermes-chat/internal/app/json.go`, `message.go`, `room.go`
- Modify: `docs/openapi.yaml`, `README.md`, `AGENTS.md`, `docs/superpowers/specs/2026-10-11-chat-server-architecture-design.md`
- Test: `cmd/hermes-chat/internal/app/message_test.go`, `room_test.go`, `delivery_test.go`

**Interfaces:**
- Consumes: `Service.PostMessageOnce`, the new `Service.Steer`, `Message.ClientID`, `Message.Streaming`, `RoomAgent.Working` (Tasks 2–4).
- Produces (JSON): on the human message object `"client_id": string|null` and `"streaming": bool`; on each room agent `"working": bool`; request field `"client_id"` on post and steer; status 200 for a repeat, 201 for a new message.

- [ ] **Step 1: Write the failing tests**

Existing tests that compare whole message or room JSON objects will need the three new keys; update them as part of this step.

- `TestPostMessage_ClientID` (`app/message_test.go`, with `newChat` and a DM opened over HTTP): `POST …/messages` with `{"text":"hi","client_id":"k1"}` → 201, body has `"client_id":"k1"` and `"streaming":false`. The same request again with `"text":"other"` → **200**, the same `id`, `"text":"hi"`. `GET …/messages` lists one message. A post with no `client_id`, with `"client_id":null`, and with `"client_id":""` → 201 each, `"client_id":null` in the body.
- `TestPostMessage_ClientIDValidation`: `"client_id":"a b"` and a 65-character key → 400 with `badReq("client_id must be at most 64 characters: letters, digits, \"-\" or \"_\"")`.
- `TestSteer_ClientID` (`app/room_test.go` or wherever steer is tested): during a working turn, steer with `"client_id":"s1"` → 201; again → 200 with the same `id`.
- `TestRoomJSON_AgentWorking`: with `newAgentRig`-style setup or `newChat` plus a bus subscription for scout, `GET /api/rooms/:room` shows `"working":false` on the agent, then `"working":true` after the agent reports working over `POST /agent/deliveries/:message/working`; `GET /api/rooms` agrees.
- `TestMessageJSON_Streaming`: an agent posts, then `PUT /agent/messages/:message` with `{"text":"par","final":false}`; `GET /api/rooms/:room/messages/:message` as kai shows `"streaming":true`; after `"final":true` it shows `false`.
- `TestAgentMessageJSON_HasNoClientID`: the message an agent receives for a keyed human post has no `client_id` key.

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./cmd/hermes-chat/internal/app/`
Expected: FAIL.

- [ ] **Step 3: Implement**

- `json.go`: `messageBody` gains `ClientID *string` (`json:"client_id"`, nil for `""`) and `Streaming bool` (`json:"streaming"`); `roomAgentBody` gains `Working bool` (`json:"working"`); fill them in `messageJSON` and `roomJSON`. `agentMessageBody` is unchanged.
- `message.go` `PostMessage`: body gains `ClientID *string` (`json:"client_id"`); call `PostMessageOnce`; answer `http.StatusCreated` when `created`, else `http.StatusOK`.
- `room.go` `Steer`: the same, with the new `Steer` signature.

- [ ] **Step 4: Run the tests**

Run: `go test ./cmd/hermes-chat/...`
Expected: PASS.

- [ ] **Step 5: Update the contract and the prose**

- `docs/openapi.yaml`: `client_id` (string, `maxLength: 64`, `pattern: '^[A-Za-z0-9_-]*$'`, nullable) on the post and steer request bodies; a `200` response beside the `201` on both, described as "a repeat of an earlier request with this client_id; the message that request made"; `client_id` and `streaming` on the `Message` schema (both required); `working` on the room agent schema (required).
- `README.md`:
  - "HTTP API": describe `client_id` on post and steer in the words of Decisions 1.
  - "Event stream": `streaming` on the message, that a live non-final `message.edited` frame means streaming, and that the logged `message.edited` carries `streaming:false`, including when the server closed the stream at the end of a turn; `working` on a room's agents, and that the UI keeps it current from `delivery.changed`.
  - "Known limits": remove "Posting is not idempotent…"; replace "A message an agent was streaming when its room was paused stays unfinalized…" with "A message an agent was streaming outside a turn (a scheduled report) stays `streaming` until its final edit arrives; one streamed during a turn is closed when the turn ends."; add "A post or steer by an agent has no `client_id`; an agent that retries can post twice." and "Only posting and steering take a `client_id`. Creating a room twice makes two rooms; the other human writes (the note, read markers, pause, answers) are safe to repeat as they are."
- `AGENTS.md`: "stages 1–7" → "stages 1–8"; under Code Conventions add one bullet: "**Retries**: a human post or steer may carry `client_id`; the lookup (`GetMessageByClientID`) runs inside the write transaction, after the membership check and before every other check, and a hit returns the stored message and writes nothing. A new human write that creates a message goes through the same lookup."
- Architecture spec, section 14: "seven stages" → "eight stages", and add "8. **Shapes for the web UI.** A client id that makes posting and steering safe to retry, `working` on a room's agents, and `streaming` on a message."

- [ ] **Step 6: Run the gate**

Run: `just check`
Expected: `sqlc diff` clean, vet clean, shellcheck clean, every package `ok`.

- [ ] **Step 7: Commit**

```bash
git add cmd/hermes-chat/internal/app docs README.md AGENTS.md
git commit -m "feat(app): client_id on post and steer, working on room agents, streaming on messages"
```
