# Chat Server Stage 3 (Rooms, Messages, and the Human Stream) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A logged-in human can create rooms, manage who is in them, post and read messages with reply-to, and hold one Server-Sent Events stream that replays what they missed and then pushes every later change live.

**Architecture:** Same three layers. `storage` gains rooms, membership, messages, and an autoincrement event log. `bus` owns every rule (who may see a room, flavors, archiving, validation), writes one event row in the same transaction as each change, and rings an in-memory hub after the commit. The hub is a doorbell, not a pipe: a subscription re-reads the event log from its own cursor when rung, so a slow reader can never lose an event or hold up a writer. `app` gains the JSON routes and one stream handler that turns bus events into SSE frames. The hub is the third grace runner, so shutdown closes every stream.

**Tech Stack:** As stage 2. No new dependencies.

**Spec:** `docs/superpowers/specs/2026-10-11-chat-server-architecture-design.md` (stage 3 of its section 14; sections 5.2, 5.3, 6.1, 6.5, 7.2, 7.4, 8 especially). Product rules: sections 4, 5.1, 5.3, and 10 of `docs/superpowers/specs/2026-10-11-hermes-chat-design.md`. Read both before starting any task. The stage 2 plan, `2026-10-11-chat-server-stage-2-identity-and-auth.md`, shows the test and commit style to match.

## Global Constraints

Everything in the Global Constraints of stages 1 and 2 still holds (layering, ULIDs, `STRICT`, integer-millisecond timestamps, temp-file database per test, black-box tests, mocks only to force errors, `…Context` logging in `bus` and `storage`, no secrets in logs, `CGO_ENABLED=0`, the bus never returns a storage sentinel, `app` never sends a bus error's text except a `ValidationError`'s message). Added for this stage:

- **Only humans act in this stage.** Every new bus method takes the `Actor` as its first argument after `ctx`. An actor that is not a human gets `ErrForbidden`. A human who is not a member of the room gets `ErrNotFound`, the same as for a room that does not exist, so a room's existence is never revealed.
- **Flavor is derived, never stored:** a room with a DM pair is `dm`; otherwise two or more agents is `channel`; otherwise `conversation`. Every room that is not a DM has at least one agent.
- **One event row per change, in the same transaction as the change.** The hub is rung only after that transaction has committed (spec 7.2). Every such transaction goes through `service.record`, and nothing else rings the hub.
- **A message's ID and timestamp are minted inside the transaction callback** *(this plan's call)*. The write lock then serializes them, so message ID order is commit order, and "the newest message" and "unread" can be decided by comparing IDs.
- **The event ID is the only cursor.** It is the `events` table's autoincrement key (spec 7.4). Nothing else refers to an event.
- Limits *(this plan's numbers)*: a topic is 1 to 200 characters after trimming, with no control characters. Message text is at most 262144 bytes, not blank, with no NUL character, and is stored exactly as sent. A page of messages is 50 by default and 200 at most. A subscription reads at most 100 events per poll. Request bodies are capped at 4096 bytes, except a posted message at 1 MiB.
- Stream *(this plan's numbers)*: a heartbeat comment every 25 seconds; each write gets a fresh 10-second deadline. The product spec says to disable the write timeout for streams; a rolling deadline does that and also drops a client that has stopped reading.
- JSON: IDs are ULID strings; a missing ID is `null`; times are `time.Time` as `encoding/json` writes them (RFC 3339, UTC). Unknown request fields are ignored.
- Not in this stage: the agent API and agent stream, agents posting, addressing and waking, the queue, message states, turns and activity, streaming edits, presence, pause and the turn cap, prompts, notes, attachments, TLS, any web UI.

Known limits, accepted for now:

- Posting is not idempotent. A phone that retries a `POST` after a timeout can post the message twice.
- `user remove` runs in another process and writes no event, so an open browser learns that a member has gone only when it next loads the room.
- Every committed change wakes every open stream, and each runs one indexed query. That is fine for two humans and a few tabs.
- The event log is never pruned, and a client that reconnects with a very old cursor is sent everything since.
- Over plain HTTP/1.1 a browser allows six streams per origin. Stage 7 checks HTTP/2.

## Review Focus

1. **A stream opened with no cursor, a nonsense cursor, or a cursor ahead of the log.** No cursor must start at the present, not replay all history. A cursor that is not a non-negative integer is a 400. A cursor ahead of the log (the database was restored) is clamped to the log's end, and the `ready` frame says where the stream really starts. Pinned in Tasks 5 and 7.
2. **A stream left open by a session that has since ended.** After logout, `user passwd`, `user remove`, or expiry, the stream must close within one heartbeat, not keep delivering messages forever. Pinned in Task 7.
3. **A human guessing IDs.** A non-member must get 404 from every room route, must never see a room's events, and must not reach a message through the wrong room: not as a reply-to target, not as a read marker, not by `GET`. Pinned in Tasks 3, 4, 5, and 6.
4. **`user remove` for a human who has rooms and messages.** It must still succeed. Their DMs go; shared rooms keep their messages, shown with no author; every read path must cope with a message whose author is gone; and the event log must not reuse an ID a client may hold as its cursor. Pinned in Tasks 2 and 4.
5. **A reader that is slow, stalled, or gone.** Posting must never wait on a subscriber; a subscriber that polls late must get everything, in order; a browser that stops reading must be dropped, not left holding a goroutine. Pinned in Tasks 5 and 7.

## File Structure

```
cmd/hermes-chat/
    wires.go                       wires.Hub
    serve.go                       the hub is the third runner
    routes.go                      the human group grows to 17 routes
    layering.go                    sse is a leaf
    internal/
        sse/sse.go                 Frame, Write, Decoder                     (new leaf package)
        storage/
            migrations/00003_rooms.sql                                       (new)
            queries/rooms.sql, messages.sql, events.sql                      (new)
            storage.go             Repository grows
            room.go, message.go, event.go                                    (new)
        bus/
            bus.go                 Service grows; new sentinels
            human.go               humanFromRow extracted
            room.go                rooms and membership                      (new)
            message.go             posting, reading, read markers            (new)
            event.go               EventKind, Event, putEvent                (new)
            hub.go                 Hub, Subscription, Subscribe, record      (new)
            export_test.go         Subscribers
        app/
            app.go                 Options and Controller grow
            errors.go              two more rows
            json.go                response shapes, readJSON, badRequest     (new)
            room.go                room, member, and directory handlers      (new)
            message.go             message handlers                          (new)
            stream.go              Events                                    (new)
```

---

### Task 1: Storage: schema, rooms, and membership

**Files:**
- Create: `cmd/hermes-chat/internal/storage/migrations/00003_rooms.sql`, `queries/rooms.sql`, `room.go`
- Modify: `storage.go`, `sqlcgen/` (regenerated)
- Test: `cmd/hermes-chat/internal/storage/room_test.go`

**Interfaces:**
- Consumes: `repo`, `mapErr`, `requireRow`, `nullInt64`, `storagetest.NewRepo`; the fixtures `kai`, `ada` (`storage_test.go`), `scout`, `atlas`, `ms` (`agent_test.go`).
- Produces:
  ```go
  type Room struct {
      ID, Topic            string  // Topic is "" for a DM
      DMHumanID, DMAgentID *string // both set for a DM, both nil otherwise
      CreatedAtMs          int64
      ArchivedAtMs         *int64 // nil while live
  }
  type RoomHuman struct {
      RoomID, HumanID   string
      JoinedAtMs        int64
      LastReadMessageID string // "" when nothing is read
  }
  type RoomAgent struct {
      RoomID, AgentID string
      JoinedAtMs      int64
  }

  // added to Repository
  PutRoom(ctx context.Context, r Room) error                                  // ErrConflict on duplicate id, or a second DM for the same human and agent
  GetRoom(ctx context.Context, id string) (Room, error)                       // ErrNotFound
  GetDMRoom(ctx context.Context, humanID, agentID string) (Room, error)       // ErrNotFound
  ListRoomsByHuman(ctx context.Context, humanID string) ([]Room, error)       // ordered by id; archived rooms included; empty, not nil
  SetRoomTopic(ctx context.Context, id, topic string) error                   // ErrNotFound
  SetRoomArchivedAt(ctx context.Context, id string, archivedAtMs *int64) error // ErrNotFound; nil clears

  PutRoomHuman(ctx context.Context, m RoomHuman) error                        // ErrConflict if already a member
  GetRoomHuman(ctx context.Context, roomID, humanID string) (RoomHuman, error) // ErrNotFound: not a member
  ListRoomHumans(ctx context.Context, roomID string) ([]Human, error)         // ordered by username; PasswordHash is left empty; empty, not nil
  SetRoomHumanLastRead(ctx context.Context, roomID, humanID, messageID string) error // ErrNotFound

  PutRoomAgent(ctx context.Context, m RoomAgent) error                        // ErrConflict if already a member
  DeleteRoomAgent(ctx context.Context, roomID, agentID string) error          // ErrNotFound
  ListRoomAgents(ctx context.Context, roomID string) ([]Agent, error)         // ordered by handle; empty, not nil
  ```

- [ ] **Step 1: Write the migration** `migrations/00003_rooms.sql`

This file holds the whole stage's schema. sqlc 1.31.1 generated code from exactly this file, and SQLite 3.46.1 ran the cascade in Review Focus 4 against it, on 2026-10-11.

```sql
-- +goose Up
CREATE TABLE rooms (
    id          TEXT    NOT NULL PRIMARY KEY,
    topic       TEXT    NOT NULL,
    dm_human_id TEXT    REFERENCES humans (id) ON DELETE CASCADE,
    dm_agent_id TEXT    REFERENCES agents (id),
    created_at  INTEGER NOT NULL,
    archived_at INTEGER,
    CHECK ((dm_human_id IS NULL) = (dm_agent_id IS NULL))
) STRICT;

CREATE UNIQUE INDEX rooms_dm ON rooms (dm_human_id, dm_agent_id) WHERE dm_human_id IS NOT NULL;

CREATE TABLE room_humans (
    room_id              TEXT    NOT NULL REFERENCES rooms (id) ON DELETE CASCADE,
    human_id             TEXT    NOT NULL REFERENCES humans (id) ON DELETE CASCADE,
    joined_at            INTEGER NOT NULL,
    last_read_message_id TEXT    NOT NULL DEFAULT '',
    PRIMARY KEY (room_id, human_id)
) STRICT;

CREATE INDEX room_humans_human_id ON room_humans (human_id);

CREATE TABLE room_agents (
    room_id   TEXT    NOT NULL REFERENCES rooms (id) ON DELETE CASCADE,
    agent_id  TEXT    NOT NULL REFERENCES agents (id),
    joined_at INTEGER NOT NULL,
    PRIMARY KEY (room_id, agent_id)
) STRICT;

CREATE TABLE messages (
    id              TEXT    NOT NULL PRIMARY KEY,
    room_id         TEXT    NOT NULL REFERENCES rooms (id) ON DELETE CASCADE,
    author_human_id TEXT    REFERENCES humans (id) ON DELETE SET NULL,
    author_agent_id TEXT    REFERENCES agents (id),
    reply_to_id     TEXT    REFERENCES messages (id),
    body            TEXT    NOT NULL,
    created_at      INTEGER NOT NULL,
    CHECK (author_human_id IS NULL OR author_agent_id IS NULL)
) STRICT;

CREATE INDEX messages_room_id ON messages (room_id, id);

CREATE TABLE events (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    room_id    TEXT    NOT NULL REFERENCES rooms (id) ON DELETE CASCADE,
    kind       TEXT    NOT NULL,
    subject_id TEXT    NOT NULL,
    created_at INTEGER NOT NULL
) STRICT;

CREATE INDEX events_room_id ON events (room_id, id);

-- +goose Down
DROP TABLE events;
DROP TABLE messages;
DROP TABLE room_agents;
DROP TABLE room_humans;
DROP TABLE rooms;
```

What the foreign keys decide: removing a human removes their memberships and their DMs (and so those DMs' messages and events); their messages in other rooms stay, with no author. `author_agent_id` is here so stage 4 needs no table rebuild; nothing in this stage's API writes it.

- [ ] **Step 2: Write the failing tests** (`room_test.go`, package `storage_test`)

```go
func sp(s string) *string { return &s }

var trip = storage.Room{ID: "01JA00000000000000000000R1", Topic: "Trip planning", CreatedAtMs: 1791700000100}
var dm = storage.Room{ID: "01JA00000000000000000000R2", DMHumanID: sp(kai.ID), DMAgentID: sp(scout.ID), CreatedAtMs: 1791700000200}

// seeded returns a repository holding kai, ada, scout, and atlas.
func seeded(t *testing.T) storage.Repository

func TestRoom_PutAndGet(t *testing.T) {
    r := seeded(t); require.NoError(t, r.PutRoom(ctx, trip)); require.NoError(t, r.PutRoom(ctx, dm))
    got, err := r.GetRoom(ctx, trip.ID); require.NoError(t, err); assert.Equal(t, trip, got)
    got, err = r.GetRoom(ctx, dm.ID); require.NoError(t, err); assert.Equal(t, dm, got)
    got, err = r.GetDMRoom(ctx, kai.ID, scout.ID); require.NoError(t, err); assert.Equal(t, dm, got)
    _, err = r.GetDMRoom(ctx, ada.ID, scout.ID); assert.ErrorIs(t, err, storage.ErrNotFound)
    _, err = r.GetRoom(ctx, "01JA00000000000000000000R9"); assert.ErrorIs(t, err, storage.ErrNotFound)
}

func TestRoom_Conflicts(t *testing.T) {
    r := seeded(t); require.NoError(t, r.PutRoom(ctx, trip)); require.NoError(t, r.PutRoom(ctx, dm))
    assert.ErrorIs(t, r.PutRoom(ctx, trip), storage.ErrConflict)
    again := dm; again.ID = "01JA00000000000000000000R3"
    assert.ErrorIs(t, r.PutRoom(ctx, again), storage.ErrConflict) // one DM per human and agent
    other := again; other.DMHumanID = sp(ada.ID); assert.NoError(t, r.PutRoom(ctx, other))
    half := storage.Room{ID: "01JA00000000000000000000R4", DMHumanID: sp(kai.ID)}
    err := r.PutRoom(ctx, half); require.Error(t, err); assert.NotErrorIs(t, err, storage.ErrConflict) // the CHECK, left unmapped
}

func TestRoom_SetTopicAndArchivedAt(t *testing.T) {
    r := seeded(t); require.NoError(t, r.PutRoom(ctx, trip))
    require.NoError(t, r.SetRoomTopic(ctx, trip.ID, "Lisbon")); require.NoError(t, r.SetRoomArchivedAt(ctx, trip.ID, ms(42)))
    got, _ := r.GetRoom(ctx, trip.ID); assert.Equal(t, "Lisbon", got.Topic)
    require.NotNil(t, got.ArchivedAtMs); assert.EqualValues(t, 42, *got.ArchivedAtMs)
    require.NoError(t, r.SetRoomArchivedAt(ctx, trip.ID, nil))
    got, _ = r.GetRoom(ctx, trip.ID); assert.Nil(t, got.ArchivedAtMs)
    assert.ErrorIs(t, r.SetRoomTopic(ctx, dm.ID, "x"), storage.ErrNotFound)
    assert.ErrorIs(t, r.SetRoomArchivedAt(ctx, dm.ID, ms(1)), storage.ErrNotFound)
}

func TestRoomHumans(t *testing.T) {
    r := seeded(t); require.NoError(t, r.PutRoom(ctx, trip)); require.NoError(t, r.PutRoom(ctx, dm))
    none, err := r.ListRoomHumans(ctx, trip.ID); require.NoError(t, err); assert.NotNil(t, none); assert.Empty(t, none)
    k := storage.RoomHuman{RoomID: trip.ID, HumanID: kai.ID, JoinedAtMs: 5}
    require.NoError(t, r.PutRoomHuman(ctx, k))
    require.NoError(t, r.PutRoomHuman(ctx, storage.RoomHuman{RoomID: trip.ID, HumanID: ada.ID, JoinedAtMs: 6}))
    require.NoError(t, r.PutRoomHuman(ctx, storage.RoomHuman{RoomID: dm.ID, HumanID: kai.ID, JoinedAtMs: 7}))
    assert.ErrorIs(t, r.PutRoomHuman(ctx, k), storage.ErrConflict)

    got, err := r.GetRoomHuman(ctx, trip.ID, kai.ID); require.NoError(t, err); assert.Equal(t, k, got) // LastReadMessageID is ""
    _, err = r.GetRoomHuman(ctx, dm.ID, ada.ID); assert.ErrorIs(t, err, storage.ErrNotFound)
    hs, _ := r.ListRoomHumans(ctx, trip.ID); assert.Equal(t, []storage.Human{ada, kai}, hs) // by username, no password hash

    require.NoError(t, r.SetRoomArchivedAt(ctx, trip.ID, ms(1)))
    archived := trip; archived.ArchivedAtMs = ms(1)
    mine, _ := r.ListRoomsByHuman(ctx, kai.ID); assert.Equal(t, []storage.Room{archived, dm}, mine) // by id, archived included
    hers, _ := r.ListRoomsByHuman(ctx, ada.ID); assert.Equal(t, []storage.Room{archived}, hers)
    nobody, err := r.ListRoomsByHuman(ctx, "01JA0000000000000000000009"); require.NoError(t, err); assert.NotNil(t, nobody); assert.Empty(t, nobody)

    require.NoError(t, r.SetRoomHumanLastRead(ctx, trip.ID, kai.ID, "01JA00000000000000000000M1"))
    got, _ = r.GetRoomHuman(ctx, trip.ID, kai.ID); assert.Equal(t, "01JA00000000000000000000M1", got.LastReadMessageID)
    other, _ := r.GetRoomHuman(ctx, trip.ID, ada.ID); assert.Equal(t, "", other.LastReadMessageID) // the marker is per human
    assert.ErrorIs(t, r.SetRoomHumanLastRead(ctx, dm.ID, ada.ID, "x"), storage.ErrNotFound)
}

func TestRoomAgents(t *testing.T) {
    r := seeded(t); require.NoError(t, r.PutRoom(ctx, trip))
    none, err := r.ListRoomAgents(ctx, trip.ID); require.NoError(t, err); assert.NotNil(t, none); assert.Empty(t, none)
    s := storage.RoomAgent{RoomID: trip.ID, AgentID: scout.ID, JoinedAtMs: 5}
    require.NoError(t, r.PutRoomAgent(ctx, s)); require.NoError(t, r.PutRoomAgent(ctx, storage.RoomAgent{RoomID: trip.ID, AgentID: atlas.ID, JoinedAtMs: 6}))
    assert.ErrorIs(t, r.PutRoomAgent(ctx, s), storage.ErrConflict)
    as, _ := r.ListRoomAgents(ctx, trip.ID); assert.Equal(t, []storage.Agent{atlas, scout}, as) // by handle
    require.NoError(t, r.DeleteRoomAgent(ctx, trip.ID, scout.ID))
    as, _ = r.ListRoomAgents(ctx, trip.ID); assert.Equal(t, []storage.Agent{atlas}, as)
    assert.ErrorIs(t, r.DeleteRoomAgent(ctx, trip.ID, scout.ID), storage.ErrNotFound)
}

func TestRoomMembers_UnknownRoomOrMemberIsRejected(t *testing.T) { // foreign keys are on
    r := seeded(t)
    assert.Error(t, r.PutRoomHuman(ctx, storage.RoomHuman{RoomID: trip.ID, HumanID: kai.ID}))
    require.NoError(t, r.PutRoom(ctx, trip))
    assert.Error(t, r.PutRoomHuman(ctx, storage.RoomHuman{RoomID: trip.ID, HumanID: "01JA0000000000000000000009"}))
    assert.Error(t, r.PutRoomAgent(ctx, storage.RoomAgent{RoomID: trip.ID, AgentID: "01JA0000000000000000000009"}))
}
```

- [ ] **Step 3: Run to verify failure**

Run: `go test ./cmd/hermes-chat/internal/storage/...`
Expected: FAIL, `storage.Room` and the new methods are undefined.

- [ ] **Step 4: Write `queries/rooms.sql` and generate**

One query per repository method, named for it (`InsertRoom`, `InsertRoomHuman`, `InsertRoomAgent` for the three `Put…`). `:execrows` for every `UPDATE` and for `DeleteRoomAgent`. Select columns by name, never `*`. `ListRoomsByHuman`, `ListRoomHumans`, and `ListRoomAgents` join through the membership table; `ListRoomHumans` selects `id`, `username`, `created_at` only.

Run: `just generate`
Expected: `sqlcgen/rooms.sql.go` appears; `models.go` gains `Room`, `RoomHuman`, `RoomAgent`, `Message`, `Event`; `sqlcgen.Room.DmHumanID` is `sql.NullString`.

- [ ] **Step 5: Implement `room.go` and the `Repository` additions**

Reads through `r.reads`, writes through `r.writes`, every driver error through `mapErr`. `sql.NullString` is converted to and from `*string` inside the package. A foreign-key or `CHECK` failure is left unmapped.

- [ ] **Step 6: Run to verify pass**

Run: `just check`
Expected: PASS; `sqlc diff` clean. If `migrate_test.go` pins a schema version or a table list, bring it up to 3 and the five new tables.

- [ ] **Step 7: Commit**

```bash
git add cmd/hermes-chat/internal/storage
git commit -m "Add rooms and membership to storage"
```

---

### Task 2: Storage: messages and the event log

**Files:**
- Create: `cmd/hermes-chat/internal/storage/queries/messages.sql`, `queries/events.sql`, `message.go`, `event.go`
- Modify: `storage.go`, `sqlcgen/` (regenerated)
- Test: `cmd/hermes-chat/internal/storage/message_test.go`, `event_test.go`

**Interfaces:**
- Consumes: Task 1's tables, `seeded`, `trip`, `dm`, `sp`.
- Produces:
  ```go
  type Message struct {
      ID, RoomID                   string
      AuthorHumanID, AuthorAgentID *string // at most one is set; both nil once the human who wrote it is removed
      ReplyToID                    *string
      Body                         string
      CreatedAtMs                  int64
  }
  type Event struct {
      ID                      int64 // assigned by the log
      RoomID, Kind, SubjectID string
      CreatedAtMs             int64
  }

  // added to Repository
  PutMessage(ctx context.Context, m Message) error                                    // ErrConflict on duplicate id
  GetMessage(ctx context.Context, id string) (Message, error)                         // ErrNotFound
  ListMessages(ctx context.Context, roomID, beforeID string, limit int64) ([]Message, error) // newest first; beforeID "" starts at the newest; empty, not nil
  GetLastMessageID(ctx context.Context, roomID string) (string, error)                // ErrNotFound when the room has no messages

  PutEvent(ctx context.Context, e Event) (int64, error)                               // e.ID is ignored; returns the id the log assigned
  ListEventsForHuman(ctx context.Context, humanID string, afterID, limit int64) ([]Event, error) // ascending id; only rooms the human is a member of now; empty, not nil
  GetLastEventID(ctx context.Context) (int64, error)                                  // 0 when the log is empty
  ```

- [ ] **Step 1: Write the failing tests** (package `storage_test`)

```go
// message_test.go
// msg builds message n ("1".."9") in a room, written by a human.
func msg(n, roomID, humanID string) storage.Message {
    return storage.Message{ID: "01JA00000000000000000000M" + n, RoomID: roomID, AuthorHumanID: sp(humanID), Body: "body " + n, CreatedAtMs: 1791700001000}
}

// roomed is seeded plus trip (kai and ada, scout) and dm (kai, scout).
func roomed(t *testing.T) storage.Repository

func TestMessage_PutAndGet(t *testing.T) {
    r := roomed(t); m1 := msg("1", trip.ID, kai.ID); require.NoError(t, r.PutMessage(ctx, m1))
    reply := msg("2", trip.ID, ada.ID); reply.ReplyToID = sp(m1.ID); require.NoError(t, r.PutMessage(ctx, reply))
    byAgent := storage.Message{ID: "01JA00000000000000000000M3", RoomID: trip.ID, AuthorAgentID: sp(scout.ID), Body: "hi"}
    require.NoError(t, r.PutMessage(ctx, byAgent))
    for _, want := range []storage.Message{m1, reply, byAgent} {
        got, err := r.GetMessage(ctx, want.ID); require.NoError(t, err); assert.Equal(t, want, got)
    }
    _, err := r.GetMessage(ctx, "01JA00000000000000000000M9"); assert.ErrorIs(t, err, storage.ErrNotFound)
    assert.ErrorIs(t, r.PutMessage(ctx, m1), storage.ErrConflict)
}

func TestMessage_Rejected(t *testing.T) { // foreign keys and the CHECK
    r := roomed(t)
    assert.Error(t, r.PutMessage(ctx, msg("1", "01JA00000000000000000000R9", kai.ID)))
    both := msg("1", trip.ID, kai.ID); both.AuthorAgentID = sp(scout.ID); assert.Error(t, r.PutMessage(ctx, both))
    orphan := msg("1", trip.ID, kai.ID); orphan.ReplyToID = sp("01JA00000000000000000000M9"); assert.Error(t, r.PutMessage(ctx, orphan))
}

func TestListMessages(t *testing.T) {
    r := roomed(t)
    none, err := r.ListMessages(ctx, trip.ID, "", 10); require.NoError(t, err); assert.NotNil(t, none); assert.Empty(t, none)
    _, err = r.GetLastMessageID(ctx, trip.ID); assert.ErrorIs(t, err, storage.ErrNotFound)
    var all []storage.Message
    for _, n := range []string{"1", "2", "3", "4", "5"} { m := msg(n, trip.ID, kai.ID); all = append(all, m); require.NoError(t, r.PutMessage(ctx, m)) }
    require.NoError(t, r.PutMessage(ctx, msg("6", dm.ID, kai.ID))) // another room
    got, _ := r.ListMessages(ctx, trip.ID, "", 2); assert.Equal(t, []storage.Message{all[4], all[3]}, got) // newest first
    got, _ = r.ListMessages(ctx, trip.ID, all[3].ID, 10); assert.Equal(t, []storage.Message{all[2], all[1], all[0]}, got)
    got, _ = r.ListMessages(ctx, trip.ID, all[0].ID, 10); assert.Empty(t, got)
    last, err := r.GetLastMessageID(ctx, trip.ID); require.NoError(t, err); assert.Equal(t, all[4].ID, last)
}

// event_test.go
func ev(roomID, subject string) storage.Event { return storage.Event{RoomID: roomID, Kind: "message.posted", SubjectID: subject, CreatedAtMs: 9} }

func TestEvents_PutAndList(t *testing.T) {
    r := roomed(t)
    last, err := r.GetLastEventID(ctx); require.NoError(t, err); assert.Zero(t, last)
    none, err := r.ListEventsForHuman(ctx, kai.ID, 0, 10); require.NoError(t, err); assert.NotNil(t, none); assert.Empty(t, none)
    var got []int64
    for _, e := range []storage.Event{ev(trip.ID, "a"), ev(dm.ID, "b"), ev(trip.ID, "c")} {
        e.ID = 99 // ignored
        id, err := r.PutEvent(ctx, e); require.NoError(t, err); got = append(got, id)
    }
    assert.Equal(t, []int64{1, 2, 3}, got)
    last, _ = r.GetLastEventID(ctx); assert.EqualValues(t, 3, last)

    mine, _ := r.ListEventsForHuman(ctx, kai.ID, 0, 10); require.Len(t, mine, 3)
    want := ev(trip.ID, "a"); want.ID = 1; assert.Equal(t, want, mine[0])
    subjects := func(es []storage.Event) (out []string) { for _, e := range es { out = append(out, e.SubjectID) }; return }
    assert.Equal(t, []string{"a", "b", "c"}, subjects(mine))
    hers, _ := r.ListEventsForHuman(ctx, ada.ID, 0, 10); assert.Equal(t, []string{"a", "c"}, subjects(hers)) // ada is not in the DM
    after, _ := r.ListEventsForHuman(ctx, kai.ID, 1, 10); assert.Equal(t, []string{"b", "c"}, subjects(after))
    limited, _ := r.ListEventsForHuman(ctx, kai.ID, 0, 2); assert.Equal(t, []string{"a", "b"}, subjects(limited))
    _, err = r.PutEvent(ctx, ev("01JA00000000000000000000R9", "x")); assert.Error(t, err) // no such room
}

// Membership is read at query time: joining a room opens its whole log.
func TestEvents_FollowCurrentMembership(t *testing.T) {
    r := seeded(t); require.NoError(t, r.PutRoom(ctx, trip))
    _, err := r.PutEvent(ctx, ev(trip.ID, "a")); require.NoError(t, err)
    got, _ := r.ListEventsForHuman(ctx, ada.ID, 0, 10); assert.Empty(t, got)
    require.NoError(t, r.PutRoomHuman(ctx, storage.RoomHuman{RoomID: trip.ID, HumanID: ada.ID}))
    got, _ = r.ListEventsForHuman(ctx, ada.ID, 0, 10); assert.Len(t, got, 1)
}

func TestEvents_RollBackWithTheirTransaction(t *testing.T) {
    r := roomed(t); boom := errors.New("boom")
    err := r.InTx(ctx, func(tx storage.Repository) error {
        require.NoError(t, tx.PutMessage(ctx, msg("1", trip.ID, kai.ID)))
        _, err := tx.PutEvent(ctx, ev(trip.ID, "a")); require.NoError(t, err)
        seen, _ := tx.ListEventsForHuman(ctx, kai.ID, 0, 10); assert.Len(t, seen, 1) // visible inside the transaction
        return boom
    })
    require.ErrorIs(t, err, boom)
    last, _ := r.GetLastEventID(ctx); assert.Zero(t, last)
    _, err = r.GetMessage(ctx, "01JA00000000000000000000M1"); assert.ErrorIs(t, err, storage.ErrNotFound)
}

// Review Focus 4.
func TestDeleteHuman_WithRoomsAndMessages(t *testing.T) {
    r := roomed(t)
    inDM := msg("1", dm.ID, kai.ID); kais := msg("2", trip.ID, kai.ID); adas := msg("3", trip.ID, ada.ID); adas.ReplyToID = sp(kais.ID)
    for _, m := range []storage.Message{inDM, kais, adas} { require.NoError(t, r.PutMessage(ctx, m)) }
    for _, e := range []storage.Event{ev(trip.ID, kais.ID), ev(dm.ID, inDM.ID)} { _, err := r.PutEvent(ctx, e); require.NoError(t, err) } // ids 1, 2

    require.NoError(t, r.DeleteHuman(ctx, kai.ID))
    _, err := r.GetRoom(ctx, dm.ID); assert.ErrorIs(t, err, storage.ErrNotFound)        // the DM went with its human
    _, err = r.GetMessage(ctx, inDM.ID); assert.ErrorIs(t, err, storage.ErrNotFound)    // and its messages
    _, err = r.GetRoom(ctx, trip.ID); assert.NoError(t, err)                            // the shared room stays
    got, err := r.GetMessage(ctx, kais.ID); require.NoError(t, err)
    assert.Nil(t, got.AuthorHumanID); assert.Nil(t, got.AuthorAgentID); assert.Equal(t, kais.Body, got.Body)
    reply, _ := r.GetMessage(ctx, adas.ID); assert.Equal(t, adas, reply)                // ada's reply is untouched
    hs, _ := r.ListRoomHumans(ctx, trip.ID); assert.Equal(t, []storage.Human{ada}, hs)

    id, err := r.PutEvent(ctx, ev(trip.ID, "later")); require.NoError(t, err)
    assert.EqualValues(t, 3, id, "event 2 was deleted with the DM; its id must not be handed out again")
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./cmd/hermes-chat/internal/storage/...`
Expected: FAIL, `storage.Message`, `storage.Event`, and the new methods are undefined.

- [ ] **Step 3: Write the queries and generate**

`messages.sql`: `InsertMessage :exec`; `GetMessage :one`; `ListNewestMessages :many` (`WHERE room_id = ? ORDER BY id DESC LIMIT ?`); `ListMessagesBefore :many` (the same with `AND id < ?`); `GetLastMessageID :one` (`SELECT id … ORDER BY id DESC LIMIT 1`).

`events.sql`, exactly:

```sql
-- name: InsertEvent :one
INSERT INTO events (room_id, kind, subject_id, created_at) VALUES (?, ?, ?, ?) RETURNING id;

-- name: ListEventsForHuman :many
SELECT e.id, e.room_id, e.kind, e.subject_id, e.created_at FROM events e
WHERE e.id > ? AND e.room_id IN (SELECT m.room_id FROM room_humans m WHERE m.human_id = ?)
ORDER BY e.id LIMIT ?;

-- name: GetLastEventID :one
SELECT id FROM events ORDER BY id DESC LIMIT 1;
```

Run: `just generate`
Expected: `sqlcgen/messages.sql.go` and `events.sql.go` appear.

- [ ] **Step 4: Implement `message.go`, `event.go`, and the `Repository` additions**

`ListMessages` picks between the two list queries on `beforeID == ""`. `GetLastEventID` turns no rows into `0, nil`; every other no-rows result goes through `mapErr` as before.

- [ ] **Step 5: Run to verify pass**

Run: `just check`
Expected: PASS; `sqlc diff` clean.

- [ ] **Step 6: Commit**

```bash
git add cmd/hermes-chat/internal/storage
git commit -m "Add messages and the event log to storage"
```

---

### Task 3: Bus: rooms and membership

**Files:**
- Create: `cmd/hermes-chat/internal/bus/room.go`, `event.go`
- Modify: `bus/bus.go` (`Service`, sentinel), `bus/human.go` (extract `humanFromRow`)
- Test: `bus/room_test.go`

**Interfaces:**
- Consumes: Tasks 1 and 2's repository methods; `Actor`, `Human`, `Agent`, `agentFromRow`, `newID`, `s.now`, `ValidationError`.
- Produces:
  ```go
  // event.go
  type EventKind string
  const (
      EventRoomChanged   EventKind = "room.changed"   // subject: the room's id
      EventMessagePosted EventKind = "message.posted" // subject: the message's id
  )
  func putEvent(ctx context.Context, r storage.Repository, kind EventKind, roomID, subjectID string, at time.Time) error

  // room.go
  var ErrRoomArchived = errors.New("room is archived")

  type RoomFlavor string
  const (
      FlavorDM           RoomFlavor = "dm"
      FlavorConversation RoomFlavor = "conversation"
      FlavorChannel      RoomFlavor = "channel"
  )
  // Room is a room as one human sees it.
  type Room struct {
      ID                ulid.ULID
      Flavor            RoomFlavor
      Topic             string    // "" for a DM
      CreatedAt         time.Time
      ArchivedAt        time.Time // zero while live
      Humans            []Human   // ordered by username
      Agents            []Agent   // ordered by handle
      LastMessageID     ulid.ULID // zero when the room has no messages
      LastReadMessageID ulid.ULID // the viewer's read marker; zero when nothing is read
  }
  func (r Room) Archived() bool
  // RoomUpdate changes what is non-nil. Archived is applied before Topic.
  type RoomUpdate struct {
      Topic    *string
      Archived *bool
  }

  // added to Service
  CreateRoom(ctx context.Context, actor Actor, topic string, agentIDs []ulid.ULID) (Room, error) // ErrValidation
  OpenDM(ctx context.Context, actor Actor, agentID ulid.ULID) (room Room, created bool, err error) // ErrValidation: no such agent
  ListRooms(ctx context.Context, actor Actor) ([]Room, error)                                    // the actor's rooms, ordered by id; archived included
  GetRoom(ctx context.Context, actor Actor, roomID ulid.ULID) (Room, error)                      // ErrNotFound
  UpdateRoom(ctx context.Context, actor Actor, roomID ulid.ULID, u RoomUpdate) (Room, error)     // ErrNotFound, ErrValidation, ErrRoomArchived
  InviteHuman(ctx context.Context, actor Actor, roomID, humanID ulid.ULID) (Room, error)         // the same three
  AddRoomAgent(ctx context.Context, actor Actor, roomID, agentID ulid.ULID) (Room, error)        // the same three
  RemoveRoomAgent(ctx context.Context, actor Actor, roomID, agentID ulid.ULID) (Room, error)     // the same three

  // unexported; Tasks 4 and 5 use them
  func humanFromRow(row storage.Human) (Human, error)
  // roomForMember returns the room if actor is a human member of it: ErrForbidden for any other kind of actor, ErrNotFound otherwise.
  func roomForMember(ctx context.Context, r storage.Repository, actor Actor, roomID ulid.ULID) (storage.Room, error)
  // loadRoom builds the Room viewer sees: members, flavor, the newest message, viewer's read marker.
  func loadRoom(ctx context.Context, r storage.Repository, row storage.Room, viewer ulid.ULID) (Room, error)
  // record runs fn in one write transaction. Task 5 makes it ring the hub after the commit.
  func (s *service) record(ctx context.Context, fn func(storage.Repository) error) error
  ```

Rules pinned by this task:

- Every mutator does its membership check, its other checks, its writes, and its `putEvent` inside one `s.record` callback, then returns `s.GetRoom` read after the commit. `CreateRoom` and `OpenDM` have no room to check and start with the `ErrForbidden` test.
- A change that changes nothing (the same topic, already archived, already a member, removing an agent that is not in the room, reopening a DM, an empty `RoomUpdate`) succeeds and writes no event. Every real change writes exactly one `room.changed` event, whose subject is the room's ID.
- Topic: trimmed; message `topic must be 1-200 characters with no control characters`.
- `CreateRoom`: duplicate agent IDs count once; none is `a room needs at least one agent`; one that does not exist is `no such agent`. The creator is the room's only human.
- `OpenDM`: get-or-create inside one transaction, so concurrent calls make one room. The DM's topic is `""` and its members are that human and that agent. An agent that does not exist is `no such agent`.
- `InviteHuman`: a human that does not exist is `no such user`. `AddRoomAgent`: `no such agent`. `RemoveRoomAgent` of the room's only agent: `a room needs at least one agent`.
- On a DM, `UpdateRoom`, `InviteHuman`, `AddRoomAgent`, and `RemoveRoomAgent` are `ValidationError{"a DM cannot be changed"}`.
- On an archived room, `InviteHuman`, `AddRoomAgent`, `RemoveRoomAgent`, and a topic change are `ErrRoomArchived`. Un-archiving is allowed, and because `Archived` is applied first, one `UpdateRoom` may un-archive and retitle.
- Check order inside a mutator: membership (`ErrNotFound`), then DM, then archived, then the arguments.

- [ ] **Step 1: Write the failing tests** (`room_test.go`, package `bus_test`)

```go
// world is a service on a movable clock with two humans and two agents.
type world struct {
    s            bus.Service
    r            storage.Repository
    now          *time.Time
    kai, ada     bus.Actor
    scout, atlas bus.Agent
}
func newWorld(t *testing.T) world // clock starts at fixed; humans "kai" and "ada" with password pw; agents "scout"/"Scout" and "atlas"/"Atlas"
// kinds returns the kind of every event a can see, in log order, read with w.r.ListEventsForHuman.
func (w world) kinds(t *testing.T, a bus.Actor) []string
func ids(as ...bus.Agent) (out []ulid.ULID) { for _, a := range as { out = append(out, a.ID) }; return }
func ptr[T any](v T) *T { return &v }

func TestCreateRoom(t *testing.T) {
    w := newWorld(t)
    room, err := w.s.CreateRoom(ctx, w.kai, "  Trip planning ", ids(w.scout, w.scout)); require.NoError(t, err)
    assert.Equal(t, bus.FlavorConversation, room.Flavor); assert.Equal(t, "Trip planning", room.Topic)
    assert.Equal(t, fixed, room.CreatedAt); assert.False(t, room.Archived())
    require.Len(t, room.Humans, 1); assert.Equal(t, w.kai.ID, room.Humans[0].ID); assert.Equal(t, "kai", room.Humans[0].Username)
    assert.Equal(t, []bus.Agent{w.scout}, room.Agents)
    assert.True(t, room.LastMessageID.IsZero()); assert.True(t, room.LastReadMessageID.IsZero())

    both, err := w.s.CreateRoom(ctx, w.kai, "Both", ids(w.scout, w.atlas)); require.NoError(t, err)
    assert.Equal(t, bus.FlavorChannel, both.Flavor); assert.Equal(t, []bus.Agent{w.atlas, w.scout}, both.Agents)

    got, err := w.s.GetRoom(ctx, w.kai, room.ID); require.NoError(t, err); assert.Equal(t, room, got)
    mine, err := w.s.ListRooms(ctx, w.kai); require.NoError(t, err); assert.Equal(t, []bus.Room{room, both}, mine)
    hers, err := w.s.ListRooms(ctx, w.ada); require.NoError(t, err); assert.NotNil(t, hers); assert.Empty(t, hers)
    assert.Equal(t, []string{"room.changed", "room.changed"}, w.kinds(t, w.kai))
}

func TestCreateRoom_Validation(t *testing.T) {
    w := newWorld(t)
    for _, topic := range []string{"", "   ", "line\nbreak", strings.Repeat("é", 201)} {
        _, err := w.s.CreateRoom(ctx, w.kai, topic, ids(w.scout))
        var ve bus.ValidationError; require.ErrorAs(t, err, &ve, "%q", topic)
        assert.Equal(t, "topic must be 1-200 characters with no control characters", ve.Message)
    }
    _, err := w.s.CreateRoom(ctx, w.kai, strings.Repeat("é", 200), ids(w.scout)); assert.NoError(t, err)
    var ve bus.ValidationError
    _, err = w.s.CreateRoom(ctx, w.kai, "x", nil); require.ErrorAs(t, err, &ve); assert.Equal(t, "a room needs at least one agent", ve.Message)
    _, err = w.s.CreateRoom(ctx, w.kai, "x", []ulid.ULID{w.scout.ID, ulid.Make()}); require.ErrorAs(t, err, &ve); assert.Equal(t, "no such agent", ve.Message)
    _, err = w.s.CreateRoom(ctx, bus.Actor{Kind: bus.ActorAgent, ID: w.scout.ID}, "x", ids(w.scout)); assert.ErrorIs(t, err, bus.ErrForbidden)
    assert.Len(t, w.kinds(t, w.kai), 1, "a refused room leaves nothing behind")
}

func TestOpenDM(t *testing.T) {
    w := newWorld(t)
    room, created, err := w.s.OpenDM(ctx, w.kai, w.scout.ID); require.NoError(t, err); assert.True(t, created)
    assert.Equal(t, bus.FlavorDM, room.Flavor); assert.Equal(t, "", room.Topic)
    require.Len(t, room.Humans, 1); assert.Equal(t, w.kai.ID, room.Humans[0].ID); assert.Equal(t, []bus.Agent{w.scout}, room.Agents)
    again, created, err := w.s.OpenDM(ctx, w.kai, w.scout.ID); require.NoError(t, err); assert.False(t, created); assert.Equal(t, room, again)
    hers, created, err := w.s.OpenDM(ctx, w.ada, w.scout.ID); require.NoError(t, err); assert.True(t, created); assert.NotEqual(t, room.ID, hers.ID) // each human has their own
    other, _, _ := w.s.OpenDM(ctx, w.kai, w.atlas.ID); assert.NotEqual(t, room.ID, other.ID)
    _, _, err = w.s.OpenDM(ctx, w.kai, ulid.Make()); assert.ErrorIs(t, err, bus.ErrValidation)
    _, _, err = w.s.OpenDM(ctx, bus.Actor{Kind: bus.ActorAgent, ID: w.scout.ID}, w.scout.ID); assert.ErrorIs(t, err, bus.ErrForbidden)
    assert.Equal(t, []string{"room.changed", "room.changed"}, w.kinds(t, w.kai)) // reopening wrote nothing
}

func TestOpenDM_ConcurrentCallsMakeOneRoom(t *testing.T) {
    w := newWorld(t); const n = 8
    rooms := make([]bus.Room, n); made := make([]bool, n); errs := make([]error, n)
    var wg sync.WaitGroup
    for i := range n { wg.Add(1); go func() { defer wg.Done(); rooms[i], made[i], errs[i] = w.s.OpenDM(ctx, w.kai, w.scout.ID) }() }
    wg.Wait()
    count := 0
    for i := range n { require.NoError(t, errs[i]); assert.Equal(t, rooms[0].ID, rooms[i].ID); if made[i] { count++ } }
    assert.Equal(t, 1, count)
}

// Review Focus 3.
func TestRooms_ANonMemberSeesAndChangesNothing(t *testing.T) {
    w := newWorld(t); room, _ := w.s.CreateRoom(ctx, w.kai, "Private", ids(w.scout))
    calls := map[string]func(a bus.Actor, id ulid.ULID) error{
        "get":    func(a bus.Actor, id ulid.ULID) error { _, err := w.s.GetRoom(ctx, a, id); return err },
        "update": func(a bus.Actor, id ulid.ULID) error { _, err := w.s.UpdateRoom(ctx, a, id, bus.RoomUpdate{Topic: ptr("Mine")}); return err },
        "invite": func(a bus.Actor, id ulid.ULID) error { _, err := w.s.InviteHuman(ctx, a, id, w.ada.ID); return err }, // not even herself
        "add":    func(a bus.Actor, id ulid.ULID) error { _, err := w.s.AddRoomAgent(ctx, a, id, w.atlas.ID); return err },
        "remove": func(a bus.Actor, id ulid.ULID) error { _, err := w.s.RemoveRoomAgent(ctx, a, id, w.scout.ID); return err },
    }
    for name, call := range calls {
        err := call(w.ada, room.ID)
        assert.ErrorIs(t, err, bus.ErrNotFound, name); assert.NotErrorIs(t, err, storage.ErrNotFound, name)
        assert.ErrorIs(t, call(w.kai, ulid.Make()), bus.ErrNotFound, name)                                            // no such room looks the same
        assert.ErrorIs(t, call(bus.Actor{Kind: bus.ActorAgent, ID: w.scout.ID}, room.ID), bus.ErrForbidden, name)     // a member agent is still not a human
    }
    got, _ := w.s.GetRoom(ctx, w.kai, room.ID); assert.Equal(t, room, got)
    assert.Empty(t, w.kinds(t, w.ada))
}

func TestInviteHuman(t *testing.T) {
    w := newWorld(t); room, _ := w.s.CreateRoom(ctx, w.kai, "Shared", ids(w.scout))
    got, err := w.s.InviteHuman(ctx, w.kai, room.ID, w.ada.ID); require.NoError(t, err)
    require.Len(t, got.Humans, 2); assert.Equal(t, "ada", got.Humans[0].Username); assert.Equal(t, "kai", got.Humans[1].Username)
    hers, err := w.s.GetRoom(ctx, w.ada, room.ID); require.NoError(t, err); assert.Equal(t, got, hers)
    _, err = w.s.InviteHuman(ctx, w.ada, room.ID, w.kai.ID); require.NoError(t, err) // already a member: nothing happens
    assert.Equal(t, []string{"room.changed", "room.changed"}, w.kinds(t, w.kai))
    var ve bus.ValidationError
    _, err = w.s.InviteHuman(ctx, w.kai, room.ID, ulid.Make()); require.ErrorAs(t, err, &ve); assert.Equal(t, "no such user", ve.Message)
}

func TestAddAndRemoveRoomAgent(t *testing.T) {
    w := newWorld(t); room, _ := w.s.CreateRoom(ctx, w.kai, "Team", ids(w.scout))
    got, err := w.s.AddRoomAgent(ctx, w.kai, room.ID, w.atlas.ID); require.NoError(t, err)
    assert.Equal(t, bus.FlavorChannel, got.Flavor); assert.Equal(t, []bus.Agent{w.atlas, w.scout}, got.Agents)
    _, err = w.s.AddRoomAgent(ctx, w.kai, room.ID, w.atlas.ID); require.NoError(t, err) // again: nothing happens
    got, err = w.s.RemoveRoomAgent(ctx, w.kai, room.ID, w.scout.ID); require.NoError(t, err)
    assert.Equal(t, bus.FlavorConversation, got.Flavor); assert.Equal(t, []bus.Agent{w.atlas}, got.Agents)
    _, err = w.s.RemoveRoomAgent(ctx, w.kai, room.ID, w.scout.ID); require.NoError(t, err) // not in the room: nothing happens
    assert.Len(t, w.kinds(t, w.kai), 3) // create, add, remove
    var ve bus.ValidationError
    _, err = w.s.RemoveRoomAgent(ctx, w.kai, room.ID, w.atlas.ID); require.ErrorAs(t, err, &ve); assert.Equal(t, "a room needs at least one agent", ve.Message)
    _, err = w.s.AddRoomAgent(ctx, w.kai, room.ID, ulid.Make()); require.ErrorAs(t, err, &ve); assert.Equal(t, "no such agent", ve.Message)
}

func TestUpdateRoom(t *testing.T) {
    w := newWorld(t); room, _ := w.s.CreateRoom(ctx, w.kai, "Before", ids(w.scout))
    got, err := w.s.UpdateRoom(ctx, w.kai, room.ID, bus.RoomUpdate{Topic: ptr(" After ")}); require.NoError(t, err); assert.Equal(t, "After", got.Topic)
    _, err = w.s.UpdateRoom(ctx, w.kai, room.ID, bus.RoomUpdate{Topic: ptr("After")}); require.NoError(t, err) // same topic
    _, err = w.s.UpdateRoom(ctx, w.kai, room.ID, bus.RoomUpdate{}); require.NoError(t, err)                    // nothing asked
    _, err = w.s.UpdateRoom(ctx, w.kai, room.ID, bus.RoomUpdate{Topic: ptr("")}); assert.ErrorIs(t, err, bus.ErrValidation)
    assert.Len(t, w.kinds(t, w.kai), 2)

    *w.now = fixed.Add(time.Hour)
    got, err = w.s.UpdateRoom(ctx, w.kai, room.ID, bus.RoomUpdate{Archived: ptr(true)}); require.NoError(t, err)
    assert.True(t, got.Archived()); assert.Equal(t, fixed.Add(time.Hour), got.ArchivedAt)
    *w.now = fixed.Add(2 * time.Hour)
    got, _ = w.s.UpdateRoom(ctx, w.kai, room.ID, bus.RoomUpdate{Archived: ptr(true)}); assert.Equal(t, fixed.Add(time.Hour), got.ArchivedAt) // the first time is kept
    assert.Len(t, w.kinds(t, w.kai), 3)

    _, err = w.s.UpdateRoom(ctx, w.kai, room.ID, bus.RoomUpdate{Topic: ptr("New")}); assert.ErrorIs(t, err, bus.ErrRoomArchived)
    _, err = w.s.InviteHuman(ctx, w.kai, room.ID, w.ada.ID); assert.ErrorIs(t, err, bus.ErrRoomArchived)
    _, err = w.s.AddRoomAgent(ctx, w.kai, room.ID, w.atlas.ID); assert.ErrorIs(t, err, bus.ErrRoomArchived)
    _, err = w.s.RemoveRoomAgent(ctx, w.kai, room.ID, w.scout.ID); assert.ErrorIs(t, err, bus.ErrRoomArchived)
    mine, _ := w.s.ListRooms(ctx, w.kai); require.Len(t, mine, 1); assert.True(t, mine[0].Archived()) // still listed

    got, err = w.s.UpdateRoom(ctx, w.kai, room.ID, bus.RoomUpdate{Archived: ptr(false), Topic: ptr("Back")}); require.NoError(t, err)
    assert.False(t, got.Archived()); assert.Equal(t, "Back", got.Topic)
    assert.Len(t, w.kinds(t, w.kai), 4, "one call, one event")
}

func TestDM_CannotBeChanged(t *testing.T) {
    w := newWorld(t); room, _, _ := w.s.OpenDM(ctx, w.kai, w.scout.ID)
    _, e1 := w.s.UpdateRoom(ctx, w.kai, room.ID, bus.RoomUpdate{Archived: ptr(true)})
    _, e2 := w.s.InviteHuman(ctx, w.kai, room.ID, w.ada.ID)
    _, e3 := w.s.AddRoomAgent(ctx, w.kai, room.ID, w.atlas.ID)
    _, e4 := w.s.RemoveRoomAgent(ctx, w.kai, room.ID, w.scout.ID)
    for _, err := range []error{e1, e2, e3, e4} {
        var ve bus.ValidationError; require.ErrorAs(t, err, &ve); assert.Equal(t, "a DM cannot be changed", ve.Message)
    }
    got, _ := w.s.GetRoom(ctx, w.kai, room.ID); assert.Equal(t, room, got)
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
    w := newWorld(t); broken := bus.NewService(failingEvents{w.r}, func() time.Time { return fixed })
    _, err := broken.CreateRoom(ctx, w.kai, "Doomed", ids(w.scout)); require.ErrorIs(t, err, errDisk)
    mine, _ := w.s.ListRooms(ctx, w.kai); assert.Empty(t, mine)
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./cmd/hermes-chat/internal/bus/`
Expected: FAIL, `bus.Room`, `CreateRoom`, and the rest are undefined.

- [ ] **Step 3: Implement `event.go`, `room.go`, `humanFromRow`, and the `Service` additions**

In this task `record` is one line: `return s.repo.InTx(ctx, fn)`. `loadRoom` takes the repository it is given, so it works inside or outside a transaction; it reads the two member lists, the newest message ID (`ErrNotFound` means none), and the viewer's `RoomHuman`. Wrap errors as the existing methods do (`fmt.Errorf("create room: %w", err)`), mapping `storage.ErrNotFound` to `bus.ErrNotFound` with `%w` on the bus sentinel only.

- [ ] **Step 4: Run to verify pass**

Run: `go test ./... && go vet ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/hermes-chat/internal/bus
git commit -m "Add rooms and membership to the bus"
```

---

### Task 4: Bus: messages, reply-to, and read markers

**Files:**
- Create: `cmd/hermes-chat/internal/bus/message.go`
- Modify: `bus/bus.go` (`Service`)
- Test: `bus/message_test.go`

**Interfaces:**
- Consumes: `roomForMember`, `loadRoom`, `record`, `putEvent`, `EventMessagePosted`, `newID`, `s.now`; Task 2's message repository methods; `SetRoomHumanLastRead`.
- Produces:
  ```go
  const (
      MaxMessageBytes = 262144
      DefaultPageSize = 50
      MaxPageSize     = 200
  )
  // Author is who wrote a message.
  type Author struct {
      Kind ActorKind
      ID   ulid.ULID
      Name string // a human's username, or an agent's display name
  }
  type Message struct {
      ID        ulid.ULID
      RoomID    ulid.ULID
      Author    *Author   // nil when the human who wrote it has been removed
      Text      string
      ReplyToID ulid.ULID // zero when not a reply
      CreatedAt time.Time
  }

  // added to Service
  PostMessage(ctx context.Context, actor Actor, roomID ulid.ULID, text string, replyTo ulid.ULID) (Message, error) // ErrNotFound, ErrValidation, ErrRoomArchived
  GetMessage(ctx context.Context, actor Actor, roomID, messageID ulid.ULID) (Message, error)                       // ErrNotFound, also for a message in another room
  // ListMessages returns up to limit messages older than before (zero: the newest), oldest first.
  // A limit under 1 means DefaultPageSize; one over MaxPageSize means MaxPageSize.
  ListMessages(ctx context.Context, actor Actor, roomID, before ulid.ULID, limit int) ([]Message, error)           // ErrNotFound
  MarkRoomRead(ctx context.Context, actor Actor, roomID, messageID ulid.ULID) error                                // ErrNotFound, ErrValidation

  // unexported; Task 5 uses it
  // messagesFromRows maps rows to Messages, looking each distinct author up once.
  func messagesFromRows(ctx context.Context, r storage.Repository, rows []storage.Message) ([]Message, error)
  ```

Rules pinned by this task:

- Text is checked before the transaction: not blank after `strings.TrimSpace`, at most `MaxMessageBytes` bytes, no NUL. Message: `message text must be 1 to 262144 bytes, not blank, with no NUL character`. It is stored exactly as given.
- `PostMessage`'s `record` callback, in order: `roomForMember`; archived is `ErrRoomArchived`; a non-zero `replyTo` must be a message in this room, else `ValidationError{"reply_to must be a message in this room"}`; take `at := s.now()` and `newID(at)` **here, inside the callback**; `PutMessage`; `SetRoomHumanLastRead` for the author (posting implies having read); `putEvent(EventMessagePosted, roomID, messageID)`.
- `MarkRoomRead`: the message must be in this room, else `ValidationError{"message_id must be a message in this room"}`. The marker only moves forward: an ID that is not greater than the current marker changes nothing. It writes no event. It works in an archived room and in a DM.
- `GetMessage` and `ListMessages` work in an archived room.

- [ ] **Step 1: Write the failing tests** (`message_test.go`, package `bus_test`)

```go
// shared is a room with topic "Shared" holding kai, ada, and scout: kai creates it, then invites ada (two events).
func shared(t *testing.T, w world) bus.Room

func TestPostMessage(t *testing.T) {
    w := newWorld(t); room := shared(t, w)
    m, err := w.s.PostMessage(ctx, w.kai, room.ID, "  hello *world*\n", ulid.Zero); require.NoError(t, err)
    assert.Equal(t, room.ID, m.RoomID); assert.Equal(t, "  hello *world*\n", m.Text) // stored as sent
    assert.Equal(t, &bus.Author{Kind: bus.ActorHuman, ID: w.kai.ID, Name: "kai"}, m.Author)
    assert.True(t, m.ReplyToID.IsZero()); assert.Equal(t, fixed, m.CreatedAt); assert.Equal(t, uint64(fixed.UnixMilli()), m.ID.Time())
    got, err := w.s.GetMessage(ctx, w.ada, room.ID, m.ID); require.NoError(t, err); assert.Equal(t, m, got)

    mine, _ := w.s.GetRoom(ctx, w.kai, room.ID); assert.Equal(t, m.ID, mine.LastMessageID); assert.Equal(t, m.ID, mine.LastReadMessageID) // posting marks it read
    hers, _ := w.s.GetRoom(ctx, w.ada, room.ID); assert.Equal(t, m.ID, hers.LastMessageID); assert.True(t, hers.LastReadMessageID.IsZero())
    k := w.kinds(t, w.kai); assert.Equal(t, "message.posted", k[len(k)-1])
}

func TestPostMessage_Validation(t *testing.T) {
    w := newWorld(t); room := shared(t, w); before := len(w.kinds(t, w.kai))
    for _, bad := range []string{"", " \n\t ", "nul\x00here", strings.Repeat("a", bus.MaxMessageBytes+1)} {
        _, err := w.s.PostMessage(ctx, w.kai, room.ID, bad, ulid.Zero)
        var ve bus.ValidationError; require.ErrorAs(t, err, &ve, "%.10q", bad)
        assert.Equal(t, "message text must be 1 to 262144 bytes, not blank, with no NUL character", ve.Message)
    }
    _, err := w.s.PostMessage(ctx, w.kai, room.ID, strings.Repeat("a", bus.MaxMessageBytes), ulid.Zero); assert.NoError(t, err)
    assert.Len(t, w.kinds(t, w.kai), before+1)
}

func TestPostMessage_ReplyTo(t *testing.T) {
    w := newWorld(t); room := shared(t, w); other, _ := w.s.CreateRoom(ctx, w.kai, "Other", ids(w.scout))
    first, _ := w.s.PostMessage(ctx, w.kai, room.ID, "first", ulid.Zero)
    elsewhere, _ := w.s.PostMessage(ctx, w.kai, other.ID, "elsewhere", ulid.Zero)
    reply, err := w.s.PostMessage(ctx, w.ada, room.ID, "reply", first.ID); require.NoError(t, err); assert.Equal(t, first.ID, reply.ReplyToID)
    for _, bad := range []ulid.ULID{elsewhere.ID, ulid.Make()} { // Review Focus 3: another room's message is not a valid target
        _, err := w.s.PostMessage(ctx, w.kai, room.ID, "reply", bad)
        var ve bus.ValidationError; require.ErrorAs(t, err, &ve); assert.Equal(t, "reply_to must be a message in this room", ve.Message)
    }
}

func TestPostMessage_Refused(t *testing.T) {
    w := newWorld(t); private, _ := w.s.CreateRoom(ctx, w.kai, "Private", ids(w.scout))
    _, err := w.s.PostMessage(ctx, w.ada, private.ID, "hi", ulid.Zero); assert.ErrorIs(t, err, bus.ErrNotFound)
    _, err = w.s.PostMessage(ctx, w.kai, ulid.Make(), "hi", ulid.Zero); assert.ErrorIs(t, err, bus.ErrNotFound)
    _, err = w.s.PostMessage(ctx, bus.Actor{Kind: bus.ActorAgent, ID: w.scout.ID}, private.ID, "hi", ulid.Zero); assert.ErrorIs(t, err, bus.ErrForbidden)
    _, _ = w.s.UpdateRoom(ctx, w.kai, private.ID, bus.RoomUpdate{Archived: ptr(true)})
    _, err = w.s.PostMessage(ctx, w.kai, private.ID, "hi", ulid.Zero); assert.ErrorIs(t, err, bus.ErrRoomArchived)
    got, err := w.s.ListMessages(ctx, w.kai, private.ID, ulid.Zero, 0); require.NoError(t, err); assert.NotNil(t, got); assert.Empty(t, got) // readable while archived
}

func TestPostMessage_RollsBackWhenTheEventCannotBeWritten(t *testing.T) {
    w := newWorld(t); room := shared(t, w); broken := bus.NewService(failingEvents{w.r}, func() time.Time { return fixed })
    _, err := broken.PostMessage(ctx, w.kai, room.ID, "lost", ulid.Zero); require.ErrorIs(t, err, errDisk)
    got, _ := w.s.ListMessages(ctx, w.kai, room.ID, ulid.Zero, 0); assert.Empty(t, got)
    r, _ := w.s.GetRoom(ctx, w.kai, room.ID); assert.True(t, r.LastReadMessageID.IsZero())
}

func TestListMessages_Pages(t *testing.T) {
    w := newWorld(t); room := shared(t, w); var all []bus.Message
    for i := range 205 { m, err := w.s.PostMessage(ctx, w.kai, room.ID, fmt.Sprint(i), ulid.Zero); require.NoError(t, err); all = append(all, m) }
    page, err := w.s.ListMessages(ctx, w.ada, room.ID, ulid.Zero, 3); require.NoError(t, err); assert.Equal(t, all[202:], page) // the newest three, oldest first
    page, _ = w.s.ListMessages(ctx, w.ada, room.ID, all[202].ID, 2); assert.Equal(t, all[200:202], page)
    page, _ = w.s.ListMessages(ctx, w.ada, room.ID, all[0].ID, 10); assert.Empty(t, page)
    page, _ = w.s.ListMessages(ctx, w.ada, room.ID, ulid.Zero, 0); assert.Equal(t, all[155:], page)    // default 50
    page, _ = w.s.ListMessages(ctx, w.ada, room.ID, ulid.Zero, -1); assert.Len(t, page, 50)
    page, _ = w.s.ListMessages(ctx, w.ada, room.ID, ulid.Zero, 1000); assert.Equal(t, all[5:], page)   // capped at 200
}

// Review Focus 3.
func TestMessages_AreReachedOnlyThroughTheirOwnRoom(t *testing.T) {
    w := newWorld(t); private, _ := w.s.CreateRoom(ctx, w.kai, "Private", ids(w.scout)); room := shared(t, w)
    secret, _ := w.s.PostMessage(ctx, w.kai, private.ID, "secret", ulid.Zero)
    _, err := w.s.GetMessage(ctx, w.ada, private.ID, secret.ID); assert.ErrorIs(t, err, bus.ErrNotFound)   // not her room
    _, err = w.s.GetMessage(ctx, w.ada, room.ID, secret.ID); assert.ErrorIs(t, err, bus.ErrNotFound)       // her room, not its message
    _, err = w.s.GetMessage(ctx, w.kai, room.ID, secret.ID); assert.ErrorIs(t, err, bus.ErrNotFound)       // even for a member of both
    _, err = w.s.GetMessage(ctx, w.kai, private.ID, ulid.Make()); assert.ErrorIs(t, err, bus.ErrNotFound)
    _, err = w.s.ListMessages(ctx, w.ada, private.ID, ulid.Zero, 0); assert.ErrorIs(t, err, bus.ErrNotFound)
    assert.ErrorIs(t, w.s.MarkRoomRead(ctx, w.ada, private.ID, secret.ID), bus.ErrNotFound)
    assert.ErrorIs(t, w.s.MarkRoomRead(ctx, w.ada, room.ID, secret.ID), bus.ErrValidation)
}

func TestMarkRoomRead(t *testing.T) {
    w := newWorld(t); room := shared(t, w)
    m1, _ := w.s.PostMessage(ctx, w.kai, room.ID, "one", ulid.Zero); m2, _ := w.s.PostMessage(ctx, w.kai, room.ID, "two", ulid.Zero)
    before := len(w.kinds(t, w.ada))
    read := func() ulid.ULID { r, err := w.s.GetRoom(ctx, w.ada, room.ID); require.NoError(t, err); return r.LastReadMessageID }
    require.NoError(t, w.s.MarkRoomRead(ctx, w.ada, room.ID, m2.ID)); assert.Equal(t, m2.ID, read())
    require.NoError(t, w.s.MarkRoomRead(ctx, w.ada, room.ID, m1.ID)); assert.Equal(t, m2.ID, read(), "the marker never moves back")
    assert.Len(t, w.kinds(t, w.ada), before, "marking read writes no event")
    _, _ = w.s.UpdateRoom(ctx, w.kai, room.ID, bus.RoomUpdate{Archived: ptr(true)})
    assert.NoError(t, w.s.MarkRoomRead(ctx, w.ada, room.ID, m2.ID)) // allowed while archived
}

// Review Focus 4, and the agent author column stage 4 will write.
func TestMessage_AuthorKinds(t *testing.T) {
    w := newWorld(t); room := shared(t, w)
    adas, _ := w.s.PostMessage(ctx, w.ada, room.ID, "from ada", ulid.Zero)
    agentID := w.scout.ID.String(); at := fixed.Add(time.Second); id, _ := ulid.New(ulid.Timestamp(at), ulid.DefaultEntropy())
    require.NoError(t, w.r.PutMessage(ctx, storage.Message{ID: id.String(), RoomID: room.ID.String(), AuthorAgentID: &agentID, Body: "from scout", CreatedAtMs: at.UnixMilli()}))
    require.NoError(t, w.s.RemoveHuman(ctx, "ada"))

    got, err := w.s.ListMessages(ctx, w.kai, room.ID, ulid.Zero, 0); require.NoError(t, err); require.Len(t, got, 2)
    assert.Nil(t, got[0].Author); assert.Equal(t, "from ada", got[0].Text) // the message outlives its author
    assert.Equal(t, &bus.Author{Kind: bus.ActorAgent, ID: w.scout.ID, Name: "Scout"}, got[1].Author)
    one, err := w.s.GetMessage(ctx, w.kai, room.ID, adas.ID); require.NoError(t, err); assert.Nil(t, one.Author)
    r, err := w.s.GetRoom(ctx, w.kai, room.ID); require.NoError(t, err); require.Len(t, r.Humans, 1)
    _, err = w.s.ListRooms(ctx, w.ada); assert.NoError(t, err) // a removed human simply has no rooms
}

// The ID is minted under the write lock, so ID order is commit order.
func TestPostMessage_IDsFollowCommitOrder(t *testing.T) {
    r := storagetest.NewRepo(t); s := bus.NewService(r, time.Now) // the real clock
    h, err := s.CreateHuman(ctx, "kai", pw); require.NoError(t, err); kai := bus.Actor{Kind: bus.ActorHuman, ID: h.ID}
    scout, _, _ := s.CreateAgent(ctx, "scout", "Scout"); room, err := s.CreateRoom(ctx, kai, "Busy", []ulid.ULID{scout.ID}); require.NoError(t, err)
    var wg sync.WaitGroup
    for g := range 8 { wg.Add(1); go func() { defer wg.Done(); for i := range 25 { _, err := s.PostMessage(ctx, kai, room.ID, fmt.Sprint(g, i), ulid.Zero); assert.NoError(t, err) } }() }
    wg.Wait()
    events, err := r.ListEventsForHuman(ctx, kai.ID.String(), 1, 1000); require.NoError(t, err); require.Len(t, events, 200) // after the room's own event
    for i := 1; i < len(events); i++ { assert.Greater(t, events[i].SubjectID, events[i-1].SubjectID, "event %d", events[i].ID) }
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./cmd/hermes-chat/internal/bus/`
Expected: FAIL, `bus.Message`, `PostMessage`, and the rest are undefined.

- [ ] **Step 3: Implement `message.go` and the `Service` additions**

`ListMessages` asks storage for newest-first and reverses. `PostMessage` returns the message built from what it wrote; it does not re-read.

- [ ] **Step 4: Run to verify pass**

Run: `go test ./... && go vet ./... && go test -race ./cmd/hermes-chat/internal/bus/`
Expected: PASS, and no race reported.

- [ ] **Step 5: Commit**

```bash
git add cmd/hermes-chat/internal/bus
git commit -m "Add messages, reply-to, and read markers to the bus"
```

---

### Task 5: Bus: the hub and subscriptions

**Files:**
- Create: `cmd/hermes-chat/internal/bus/hub.go`
- Modify: `bus/event.go` (`Event`), `bus/room.go` (`record`), `bus/bus.go` (`Service`, the `service` struct, sentinel), `bus/export_test.go`, `bus/room_test.go` (`world.hub`)
- Test: `bus/hub_test.go`

**Interfaces:**
- Consumes: `record`, `loadRoom`, `messagesFromRows`, `ListEventsForHuman`, `GetLastEventID`.
- Produces:
  ```go
  var ErrUnavailable = errors.New("unavailable")

  // FromNow, as Subscribe's cursor, starts a stream at the end of the log.
  const FromNow int64 = -1

  // Event is one entry of the log, as one subscriber sees it.
  type Event struct {
      ID      int64     // the stream cursor
      Kind    EventKind
      RoomID  ulid.ULID
      At      time.Time
      Room    *Room    // room.changed: the room as it is now, for this subscriber
      Message *Message // message.posted
  }

  // Hub holds the live subscriptions. It is a grace.Runner: when Run's context ends it closes them all.
  type Hub struct{ /* unexported */ }
  func (h *Hub) Run(ctx context.Context) error // blocks until ctx is done; always returns nil
  func (s *service) Hub() *Hub                 // not on the Service interface

  // Subscription is one stream. It is not safe for use by more than one goroutine.
  type Subscription struct{ /* unexported */ }
  func (s *Subscription) Cursor() int64            // the ID of the last event read, or where the stream started
  func (s *Subscription) Wake() <-chan struct{}    // receives when Poll may have something; closed when the hub stops
  func (s *Subscription) Poll(ctx context.Context) ([]Event, error)
  func (s *Subscription) Close()                   // safe to call twice, and after the hub has stopped

  // added to Service
  Subscribe(ctx context.Context, actor Actor, after int64) (*Subscription, error) // ErrForbidden, ErrValidation, ErrUnavailable

  // export_test.go
  func (s *service) Subscribers() int
  ```

How it works, and the rules pinned by this task:

- **The hub is a doorbell.** It keeps a set of subscriptions under one mutex. Each has a wake channel with a buffer of one. Ringing does a non-blocking send to every subscription, so a ring is never lost, never queued twice, and never waits. The hub knows nothing about rooms: every subscriber is rung for every change, and `Poll` decides what each may see.
- **`record` rings after the commit:** `InTx`, then ring only if it returned nil. Nothing else rings.
- **The hub works without `Run`.** `Run` adds only shutdown: when its context ends, the hub marks itself stopped and closes every wake channel. `Subscribe` on a stopped hub is `ErrUnavailable`. The admin CLI and most tests never call `Run`.
- **`Subscribe`:** `ErrForbidden` unless the actor is a human. `after` below `FromNow` is `ValidationError{"cursor must not be negative"}`. It reads the log's last ID; `FromNow`, or any `after` beyond it, starts the cursor there. It registers with the hub before returning, so nothing committed after `Subscribe` returns can be missed.
- **`Poll`** reads up to 100 rows with `ListEventsForHuman(humanID, cursor, 100)`, moves the cursor to the last row read, and turns each row into an `Event`: `room.changed` loads the room with `loadRoom` for this subscriber; `message.posted` loads the message. A row whose subject can no longer be loaded is skipped. If a full batch produced nothing, `Poll` reads the next batch instead of returning empty, so an empty result always means the log is drained. Entitlement is the SQL in `ListEventsForHuman` and nothing else.
- A caller uses it like this, and the stream handler in Task 7 is the only caller: call `Poll` until it returns nothing, wait on `Wake`, repeat.

- [ ] **Step 1: Write the failing tests** (`hub_test.go`, package `bus_test`)

`newWorld` now builds its service with `svc := bus.NewService(r, clock)` and sets a new `hub *bus.Hub` field from `svc.Hub()`.

```go
// drain polls until the log is drained and returns everything read.
func drain(t *testing.T, sub *bus.Subscription) (out []bus.Event) {
    t.Helper()
    for { evs, err := sub.Poll(ctx); require.NoError(t, err); if len(evs) == 0 { return }; out = append(out, evs...) }
}
// rung reports whether the subscription is woken within a second, and whether its channel is still open.
func rung(sub *bus.Subscription) (woken, open bool) {
    select { case _, ok := <-sub.Wake(): return true, ok; case <-time.After(time.Second): return false, true }
}
func quiet(sub *bus.Subscription) bool { select { case <-sub.Wake(): return false; default: return true } }

func TestSubscribe_FromNowSeesOnlyWhatComesAfter(t *testing.T) {
    w := newWorld(t); room := shared(t, w); _, _ = w.s.PostMessage(ctx, w.kai, room.ID, "before", ulid.Zero)
    sub, err := w.s.Subscribe(ctx, w.kai, bus.FromNow); require.NoError(t, err); defer sub.Close()
    head := sub.Cursor(); assert.EqualValues(t, 3, head) // create, invite, post
    assert.Empty(t, drain(t, sub)); assert.True(t, quiet(sub))

    m, _ := w.s.PostMessage(ctx, w.ada, room.ID, "after", ulid.Zero)
    woken, open := rung(sub); assert.True(t, woken); assert.True(t, open)
    got := drain(t, sub); require.Len(t, got, 1)
    assert.Equal(t, bus.Event{ID: head + 1, Kind: bus.EventMessagePosted, RoomID: room.ID, At: fixed, Message: &m}, got[0])
    assert.Equal(t, head+1, sub.Cursor())
}

func TestSubscribe_ReplaysFromACursor(t *testing.T) {
    w := newWorld(t); room := shared(t, w); m, _ := w.s.PostMessage(ctx, w.kai, room.ID, "one", ulid.Zero)
    sub, err := w.s.Subscribe(ctx, w.kai, 0); require.NoError(t, err); defer sub.Close()
    assert.Zero(t, sub.Cursor())
    got := drain(t, sub); require.Len(t, got, 3)
    now, _ := w.s.GetRoom(ctx, w.kai, room.ID)
    for i, e := range got[:2] { // both room.changed events carry the room as it is now, not as it was
        assert.EqualValues(t, i+1, e.ID); assert.Equal(t, bus.EventRoomChanged, e.Kind); assert.Equal(t, &now, e.Room); assert.Nil(t, e.Message)
    }
    assert.Equal(t, &m, got[2].Message); assert.Nil(t, got[2].Room)

    later, _ := w.s.Subscribe(ctx, w.kai, 2); defer later.Close()
    got = drain(t, later); require.Len(t, got, 1); assert.EqualValues(t, 3, got[0].ID)
}

// Review Focus 1.
func TestSubscribe_Cursors(t *testing.T) {
    w := newWorld(t); room := shared(t, w) // the log holds 2 events
    ahead, err := w.s.Subscribe(ctx, w.kai, 500); require.NoError(t, err); defer ahead.Close()
    assert.EqualValues(t, 2, ahead.Cursor(), "a cursor past the end is pulled back to the end")
    m, _ := w.s.PostMessage(ctx, w.kai, room.ID, "next", ulid.Zero)
    got := drain(t, ahead); require.Len(t, got, 1); assert.Equal(t, m.ID, got[0].Message.ID) // and so does not miss event 3

    _, err = w.s.Subscribe(ctx, w.kai, -2)
    var ve bus.ValidationError; require.ErrorAs(t, err, &ve); assert.Equal(t, "cursor must not be negative", ve.Message)
    _, err = w.s.Subscribe(ctx, bus.Actor{Kind: bus.ActorAgent, ID: w.scout.ID}, bus.FromNow); assert.ErrorIs(t, err, bus.ErrForbidden)
}

// Review Focus 3.
func TestSubscribe_OnlyTheSubscribersRooms(t *testing.T) {
    w := newWorld(t); hers, _ := w.s.Subscribe(ctx, w.ada, bus.FromNow); defer hers.Close()
    private, _ := w.s.CreateRoom(ctx, w.kai, "Private", ids(w.scout))
    secret, _ := w.s.PostMessage(ctx, w.kai, private.ID, "secret", ulid.Zero)
    woken, _ := rung(hers); assert.True(t, woken, "everyone is rung") // but
    assert.Empty(t, drain(t, hers), "only members are told anything")

    _, err := w.s.InviteHuman(ctx, w.kai, private.ID, w.ada.ID); require.NoError(t, err)
    got := drain(t, hers); require.Len(t, got, 3) // joining opens the room's log from her cursor on
    assert.Equal(t, bus.EventRoomChanged, got[0].Kind); assert.Equal(t, secret.ID, got[1].Message.ID); assert.Equal(t, bus.EventRoomChanged, got[2].Kind)
    require.Len(t, got[2].Room.Humans, 2); assert.True(t, got[2].Room.LastReadMessageID.IsZero(), "the room is shown as she sees it")
}

func TestEveryChangeRingsOnceItHasCommitted(t *testing.T) {
    w := newWorld(t); room := shared(t, w); first, _ := w.s.PostMessage(ctx, w.kai, room.ID, "first", ulid.Zero)
    sub, _ := w.s.Subscribe(ctx, w.kai, bus.FromNow); defer sub.Close()
    changes := []struct{ name string; kind bus.EventKind; do func() error }{
        {"create", bus.EventRoomChanged, func() error { _, err := w.s.CreateRoom(ctx, w.kai, "New", ids(w.scout)); return err }},
        {"dm", bus.EventRoomChanged, func() error { _, _, err := w.s.OpenDM(ctx, w.kai, w.scout.ID); return err }},
        {"update", bus.EventRoomChanged, func() error { _, err := w.s.UpdateRoom(ctx, w.kai, room.ID, bus.RoomUpdate{Topic: ptr("Renamed")}); return err }},
        {"add", bus.EventRoomChanged, func() error { _, err := w.s.AddRoomAgent(ctx, w.kai, room.ID, w.atlas.ID); return err }},
        {"remove", bus.EventRoomChanged, func() error { _, err := w.s.RemoveRoomAgent(ctx, w.kai, room.ID, w.atlas.ID); return err }},
        {"post", bus.EventMessagePosted, func() error { _, err := w.s.PostMessage(ctx, w.kai, room.ID, "second", first.ID); return err }},
    }
    for _, c := range changes {
        require.True(t, quiet(sub), c.name); require.NoError(t, c.do(), c.name)
        woken, _ := rung(sub); assert.True(t, woken, c.name)
        got := drain(t, sub); require.Len(t, got, 1, c.name); assert.Equal(t, c.kind, got[0].Kind, c.name)
    }
    _, err := w.s.PostMessage(ctx, w.kai, room.ID, "", ulid.Zero); require.Error(t, err)
    assert.True(t, quiet(sub), "a change that did not commit rings nobody")
    invited, _ := w.s.Subscribe(ctx, w.ada, bus.FromNow); defer invited.Close() // ada is already in the room
    private, _ := w.s.CreateRoom(ctx, w.kai, "P", ids(w.scout)); rung(invited); drain(t, invited)
    _, _ = w.s.InviteHuman(ctx, w.kai, private.ID, w.ada.ID)
    woken, _ := rung(invited); assert.True(t, woken, "invite")
}

// Review Focus 5.
func TestASubscriberThatNeverPollsLosesNothingAndBlocksNobody(t *testing.T) {
    w := newWorld(t); room := shared(t, w); sub, _ := w.s.Subscribe(ctx, w.ada, bus.FromNow); defer sub.Close()
    var want []ulid.ULID
    for i := range 250 { m, err := w.s.PostMessage(ctx, w.kai, room.ID, fmt.Sprint(i), ulid.Zero); require.NoError(t, err); want = append(want, m.ID) }
    first, err := sub.Poll(ctx); require.NoError(t, err); assert.Len(t, first, 100, "a poll reads at most 100")
    all := append(first, drain(t, sub)...); require.Len(t, all, 250)
    for i, e := range all { assert.Equal(t, want[i], e.Message.ID, i); if i > 0 { assert.Equal(t, all[i-1].ID+1, e.ID) } }
}

func TestSubscriber_KeepsUpWithConcurrentPosters(t *testing.T) {
    r := storagetest.NewRepo(t); s := bus.NewService(r, time.Now)
    h, _ := s.CreateHuman(ctx, "kai", pw); kai := bus.Actor{Kind: bus.ActorHuman, ID: h.ID}
    scout, _, _ := s.CreateAgent(ctx, "scout", "Scout"); room, _ := s.CreateRoom(ctx, kai, "Busy", []ulid.ULID{scout.ID})
    sub, err := s.Subscribe(ctx, kai, bus.FromNow); require.NoError(t, err); defer sub.Close()
    for g := range 4 { go func() { for i := range 25 { _, _ = s.PostMessage(ctx, kai, room.ID, fmt.Sprint(g, i), ulid.Zero) } }() }
    seen := map[ulid.ULID]bool{}; last := sub.Cursor(); deadline := time.After(20 * time.Second)
    for len(seen) < 100 {
        select { case <-sub.Wake(): case <-deadline: t.Fatalf("got %d of 100", len(seen)) }
        for _, e := range drain(t, sub) { assert.Greater(t, e.ID, last); last = e.ID; assert.False(t, seen[e.Message.ID]); seen[e.Message.ID] = true }
    }
}

func TestHub_RunStopsEverySubscription(t *testing.T) {
    w := newWorld(t); a, _ := w.s.Subscribe(ctx, w.kai, bus.FromNow); b, _ := w.s.Subscribe(ctx, w.ada, bus.FromNow)
    runCtx, cancel := context.WithCancel(ctx); done := make(chan error, 1)
    go func() { done <- w.hub.Run(runCtx) }()
    select { case err := <-done: t.Fatalf("Run returned early: %v", err); case <-time.After(50 * time.Millisecond): }
    cancel()
    select { case err := <-done: assert.NoError(t, err); case <-time.After(5 * time.Second): t.Fatal("Run did not return") }
    for _, sub := range []*bus.Subscription{a, b} { woken, open := rung(sub); assert.True(t, woken); assert.False(t, open, "the wake channel is closed") }
    _, err := w.s.Subscribe(ctx, w.kai, bus.FromNow); assert.ErrorIs(t, err, bus.ErrUnavailable)
    a.Close(); a.Close(); b.Close() // closing after the stop, and twice, is fine
    room, err := w.s.CreateRoom(ctx, w.kai, "Still works", ids(w.scout)); require.NoError(t, err); assert.False(t, room.ID.IsZero()) // ringing a stopped hub is harmless
}

func TestSubscription_CloseLeavesTheHub(t *testing.T) {
    w := newWorld(t); counter := w.s.(interface{ Subscribers() int })
    a, _ := w.s.Subscribe(ctx, w.kai, bus.FromNow); b, _ := w.s.Subscribe(ctx, w.kai, bus.FromNow) // one human, two tabs
    assert.Equal(t, 2, counter.Subscribers())
    a.Close(); a.Close(); assert.Equal(t, 1, counter.Subscribers())
    _, _ = w.s.CreateRoom(ctx, w.kai, "x", ids(w.scout)); woken, _ := rung(b); assert.True(t, woken)
    b.Close(); assert.Zero(t, counter.Subscribers())
}

type eventsFail struct{ storage.Repository }
func (eventsFail) ListEventsForHuman(context.Context, string, int64, int64) ([]storage.Event, error) { return nil, errDisk }

func TestPoll_StorageFailureIsReportedAndTheCursorStays(t *testing.T) {
    w := newWorld(t); s := bus.NewService(eventsFail{w.r}, func() time.Time { return fixed })
    sub, err := s.Subscribe(ctx, w.kai, 0); require.NoError(t, err); defer sub.Close()
    _, err = sub.Poll(ctx); assert.ErrorIs(t, err, errDisk); assert.Zero(t, sub.Cursor())
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./cmd/hermes-chat/internal/bus/`
Expected: FAIL, `bus.FromNow`, `Subscribe`, `Hub` are undefined.

- [ ] **Step 3: Implement `hub.go`, `Event`, the ring in `record`, `Hub()`, `Subscribers()`**

`NewService` keeps its signature and builds the hub itself. A `Subscription` holds its service, its actor, its cursor, and its wake channel. `Close` removes the subscription from the hub's set under the mutex and never closes the channel; only the hub's stop closes channels, and it does so under the same mutex, so no channel is closed twice. The hub's stop also empties the set, so `Subscribers()` is 0 afterwards.

- [ ] **Step 4: Run to verify pass**

Run: `go test ./... && go vet ./... && go test -race ./cmd/hermes-chat/internal/bus/`
Expected: PASS, no race reported, and `TestLayering_ThisRepository` still passes (the bus imports neither gin nor `net/http`).

- [ ] **Step 5: Commit**

```bash
git add cmd/hermes-chat/internal/bus
git commit -m "Add the live hub and event subscriptions to the bus"
```

---

### Task 6: App: the rooms and messages API

**Files:**
- Create: `cmd/hermes-chat/internal/app/json.go`, `room.go`, `message.go`
- Modify: `app/app.go` (`Controller`), `app/errors.go`, `cmd/hermes-chat/routes.go`
- Test: `app/room_test.go`, `app/message_test.go`, `app/errors_test.go`, `app/auth_test.go` (`apiEngine`), `cmd/hermes-chat/routes_test.go`

**Interfaces:**
- Consumes: every `Service` method from Tasks 3 and 4; `ListAgents`, `ListHumans`; `actorFrom`, `writeError`, `SameOrigin`, `HumanAuth`.
- Produces:
  ```go
  // added to Controller
  ListAgents(c *gin.Context)
  ListHumans(c *gin.Context)
  ListRooms(c *gin.Context)
  CreateRoom(c *gin.Context)
  OpenDM(c *gin.Context)
  GetRoom(c *gin.Context)
  UpdateRoom(c *gin.Context)
  InviteHuman(c *gin.Context)
  AddRoomAgent(c *gin.Context)
  RemoveRoomAgent(c *gin.Context)
  MarkRoomRead(c *gin.Context)
  ListMessages(c *gin.Context)
  PostMessage(c *gin.Context)
  GetMessage(c *gin.Context)

  // unexported, in json.go; Task 7 uses the first three
  func roomJSON(r bus.Room) any       // the room object below
  func messageJSON(m bus.Message) any // the message object below
  func badRequest(c *gin.Context, message string) // 400 {"error":{"code":"bad_request","message":…}}, aborts
  // readJSON decodes at most limit bytes of body into dst. On any failure it answers
  // badRequest "request body must be a JSON object" and returns false.
  func readJSON(c *gin.Context, limit int64, dst any) bool
  ```

Behavior pinned by this task:

- Routes. All are in the human group (`app.SameOrigin()`, `ctrl.HumanAuth`), registered in `routes.go` and nowhere else:

  | Route | Body | Success |
  |---|---|---|
  | `GET /api/agents` | | 200 `{"agents":[agent…]}`, by handle |
  | `GET /api/humans` | | 200 `{"humans":[human…]}`, by username |
  | `GET /api/rooms` | | 200 `{"rooms":[room…]}`, by ID |
  | `POST /api/rooms` | `{"topic":"…","agent_ids":["…"]}` | 201 room |
  | `POST /api/dms` | `{"agent_id":"…"}` | 201 room if it was created, 200 room if it existed |
  | `GET /api/rooms/:room` | | 200 room |
  | `PATCH /api/rooms/:room` | `{"topic":"…","archived":true}`, both optional | 200 room |
  | `POST /api/rooms/:room/humans` | `{"human_id":"…"}` | 200 room |
  | `POST /api/rooms/:room/agents` | `{"agent_id":"…"}` | 200 room |
  | `DELETE /api/rooms/:room/agents/:agent` | | 200 room |
  | `PUT /api/rooms/:room/read` | `{"message_id":"…"}` | 204 |
  | `GET /api/rooms/:room/messages?before=<id>&limit=<n>` | | 200 `{"messages":[message…]}`, oldest first |
  | `POST /api/rooms/:room/messages` | `{"text":"…","reply_to_id":"…"}`, `reply_to_id` optional | 201 message |
  | `GET /api/rooms/:room/messages/:message` | | 200 message |

- Shapes. Arrays are never `null`.

  ```json
  {"id":"01…","username":"kai"}
  {"id":"01…","handle":"scout","display_name":"Scout","revoked":false}
  {"id":"01…","flavor":"conversation","topic":"Trip planning","archived":false,"created_at":"2026-10-11T12:00:00.123Z",
   "humans":[human…],"agents":[agent…],"last_message_id":null,"last_read_message_id":null}
  {"id":"01…","room_id":"01…","author":{"kind":"human","id":"01…","name":"kai"},"text":"hello","reply_to_id":null,"created_at":"2026-10-11T12:00:00.123Z"}
  ```

  `author` is `null` for a message whose human has been removed. `last_read_message_id` is the caller's own marker.
- A path parameter that is not a ULID cannot name anything: `writeError(c, bus.ErrNotFound)`.
- Bodies are read with `readJSON` at 4096 bytes, except `POST …/messages` at `1 << 20`. An ID in a body is a JSON string parsed with `ulid.ParseStrict`; if it is missing or does not parse, the answer is `badRequest` with `<field> must be an id` (`agent_ids must be a list of ids` for that field). `reply_to_id` absent or `null` means no reply. In `PATCH`, an absent or `null` field is left alone.
- Query parameters on `GET …/messages`: `limit` absent means the default; otherwise it must be an integer from 1 to 200, else `badRequest` `limit must be an integer from 1 to 200`. `before` absent means the newest; otherwise it must be a ULID, else `badRequest` `before must be an id`.
- Every bus error goes through `writeError`, which gains two rows:

  | Bus error | Status | `code` | `message` |
  |---|---|---|---|
  | `ErrRoomArchived` | 409 | `room_archived` | `room is archived` |
  | `ErrUnavailable` | 503 | `unavailable` | `shutting down; try again shortly` |

- The directory routes never include a token hash or a password hash.

- [ ] **Step 1: Write the failing tests**

`apiEngine` in `auth_test.go` registers every route in the table above on its human group, so it stays a copy of `routes.go`; `TestRoutes_Table` below pins the real one.

```go
// room_test.go (package app_test)
// chat is apiEngine plus two logged-in humans and two agents.
type chat struct {
    e            *gin.Engine
    svc          bus.Service
    kai, ada     string // Cookie header values, "hermes_session=…"
    kaiH, adaH   bus.Human
    scout, atlas bus.Agent
}
func newChat(t *testing.T) chat
// call sends a request as the human whose Cookie header is given, with Origin https://example.com.
// An empty cookie sends none; an empty body sends none.
func (c chat) call(cookie, method, target, body string) *httptest.ResponseRecorder
// obj decodes a JSON object response.
func obj(t *testing.T, w *httptest.ResponseRecorder) map[string]any
func stamp(at time.Time) string { return at.Format(time.RFC3339Nano) } // how encoding/json writes a time
const notFound = `{"error":{"code":"not_found","message":"not found"}}`
func badReq(message string) string { return `{"error":{"code":"bad_request","message":"` + message + `"}}` }

func TestRooms_CreateGetAndList(t *testing.T) {
    c := newChat(t)
    w := c.call(c.kai, "POST", "/api/rooms", `{"topic":" Trip planning ","agent_ids":["`+c.scout.ID.String()+`"],"extra":1}`)
    require.Equal(t, 201, w.Code, w.Body.String()); id := obj(t, w)["id"].(string)
    rooms, _ := c.svc.ListRooms(ctx, bus.Actor{Kind: bus.ActorHuman, ID: c.kaiH.ID}); require.Len(t, rooms, 1)
    want := fmt.Sprintf(`{"id":%q,"flavor":"conversation","topic":"Trip planning","archived":false,"created_at":%q,
        "humans":[{"id":%q,"username":"kai"}],
        "agents":[{"id":%q,"handle":"scout","display_name":"Scout","revoked":false}],
        "last_message_id":null,"last_read_message_id":null}`, id, stamp(rooms[0].CreatedAt), c.kaiH.ID, c.scout.ID)
    assert.JSONEq(t, want, w.Body.String())
    assert.JSONEq(t, want, c.call(c.kai, "GET", "/api/rooms/"+id, "").Body.String())
    assert.JSONEq(t, `{"rooms":[`+want+`]}`, c.call(c.kai, "GET", "/api/rooms", "").Body.String())
    assert.JSONEq(t, `{"rooms":[]}`, c.call(c.ada, "GET", "/api/rooms", "").Body.String()) // an empty array, not null
}

func TestRooms_BadBodies(t *testing.T) {
    c := newChat(t); room := obj(t, c.call(c.kai, "POST", "/api/rooms", `{"topic":"T","agent_ids":["`+c.scout.ID.String()+`"]}`))["id"].(string)
    object := "request body must be a JSON object"
    cases := []struct{ method, target, body, message string }{
        {"POST", "/api/rooms", ``, object}, {"POST", "/api/rooms", `not json`, object}, {"POST", "/api/rooms", `[]`, object},
        {"POST", "/api/rooms", `{"topic":5}`, object}, {"POST", "/api/rooms", `{"topic":"` + strings.Repeat("a", 5000) + `"}`, object},
        {"POST", "/api/rooms", `{"topic":"T","agent_ids":["nope"]}`, "agent_ids must be a list of ids"},
        {"POST", "/api/rooms", `{"topic":"T","agent_ids":"` + c.scout.ID.String() + `"}`, object},
        {"POST", "/api/dms", `{}`, "agent_id must be an id"}, {"POST", "/api/dms", `{"agent_id":"nope"}`, "agent_id must be an id"},
        {"PATCH", "/api/rooms/" + room, `{"archived":"yes"}`, object},
        {"POST", "/api/rooms/" + room + "/humans", `{}`, "human_id must be an id"},
        {"POST", "/api/rooms/" + room + "/agents", `{"agent_id":""}`, "agent_id must be an id"},
        {"PUT", "/api/rooms/" + room + "/read", `{"message_id":"nope"}`, "message_id must be an id"},
        {"POST", "/api/rooms/" + room + "/messages", `{"text":"hi","reply_to_id":"nope"}`, "reply_to_id must be an id"},
        {"POST", "/api/rooms/" + room + "/messages", `{"text":"` + strings.Repeat("a", 1<<20) + `"}`, object},
    }
    for _, tc := range cases {
        w := c.call(c.kai, tc.method, tc.target, tc.body)
        assert.Equal(t, 400, w.Code, "%s %s %.30q", tc.method, tc.target, tc.body); assert.JSONEq(t, badReq(tc.message), w.Body.String(), "%s %s %.30q", tc.method, tc.target, tc.body)
    }
    w := c.call(c.kai, "POST", "/api/rooms", `{"topic":"","agent_ids":["`+c.scout.ID.String()+`"]}`) // well-formed, refused by the bus
    assert.Equal(t, 400, w.Code); assert.JSONEq(t, `{"error":{"code":"validation","message":"topic must be 1-200 characters with no control characters"}}`, w.Body.String())
    w = c.call(c.kai, "POST", "/api/rooms", `{"topic":"T"}`)
    assert.JSONEq(t, `{"error":{"code":"validation","message":"a room needs at least one agent"}}`, w.Body.String())
}

func TestDMs(t *testing.T) {
    c := newChat(t); body := `{"agent_id":"` + c.scout.ID.String() + `"}`
    w := c.call(c.kai, "POST", "/api/dms", body); require.Equal(t, 201, w.Code); first := obj(t, w)
    assert.Equal(t, "dm", first["flavor"]); assert.Equal(t, "", first["topic"])
    w = c.call(c.kai, "POST", "/api/dms", body); assert.Equal(t, 200, w.Code); assert.Equal(t, first["id"], obj(t, w)["id"])
    w = c.call(c.ada, "POST", "/api/dms", body); assert.Equal(t, 201, w.Code); assert.NotEqual(t, first["id"], obj(t, w)["id"])
    w = c.call(c.kai, "PATCH", "/api/rooms/"+first["id"].(string), `{"archived":true}`)
    assert.Equal(t, 400, w.Code); assert.JSONEq(t, `{"error":{"code":"validation","message":"a DM cannot be changed"}}`, w.Body.String())
}

func TestRoomMembersAndSettings(t *testing.T) {
    c := newChat(t); room := "/api/rooms/" + obj(t, c.call(c.kai, "POST", "/api/rooms", `{"topic":"T","agent_ids":["`+c.scout.ID.String()+`"]}`))["id"].(string)
    w := c.call(c.kai, "POST", room+"/agents", `{"agent_id":"`+c.atlas.ID.String()+`"}`)
    require.Equal(t, 200, w.Code); assert.Equal(t, "channel", obj(t, w)["flavor"]); assert.Len(t, obj(t, w)["agents"], 2)
    w = c.call(c.kai, "DELETE", room+"/agents/"+c.scout.ID.String(), ""); require.Equal(t, 200, w.Code); assert.Equal(t, "conversation", obj(t, w)["flavor"])
    w = c.call(c.kai, "DELETE", room+"/agents/"+c.atlas.ID.String(), "")
    assert.Equal(t, 400, w.Code); assert.JSONEq(t, `{"error":{"code":"validation","message":"a room needs at least one agent"}}`, w.Body.String())
    assert.Equal(t, 404, c.call(c.kai, "DELETE", room+"/agents/nope", "").Code)

    assert.Equal(t, 404, c.call(c.ada, "GET", room, "").Code)
    w = c.call(c.kai, "POST", room+"/humans", `{"human_id":"`+c.adaH.ID.String()+`"}`); require.Equal(t, 200, w.Code); assert.Len(t, obj(t, w)["humans"], 2)
    assert.Equal(t, 200, c.call(c.ada, "GET", room, "").Code)

    w = c.call(c.ada, "PATCH", room, `{"topic":"Renamed","archived":null}`); require.Equal(t, 200, w.Code)
    assert.Equal(t, "Renamed", obj(t, w)["topic"]); assert.Equal(t, false, obj(t, w)["archived"])
    w = c.call(c.ada, "PATCH", room, `{"archived":true}`); require.Equal(t, 200, w.Code); assert.Equal(t, true, obj(t, w)["archived"])
    w = c.call(c.kai, "POST", room+"/messages", `{"text":"too late"}`)
    assert.Equal(t, 409, w.Code); assert.JSONEq(t, `{"error":{"code":"room_archived","message":"room is archived"}}`, w.Body.String())
    w = c.call(c.kai, "PATCH", room, `{}`); assert.Equal(t, 200, w.Code); assert.Equal(t, true, obj(t, w)["archived"]) // nothing asked, nothing changed
}

// Review Focus 3: every room route, as a human who is not in the room, and with an ID that names nothing.
func TestRoomRoutes_HideRoomsFromNonMembers(t *testing.T) {
    c := newChat(t); room := obj(t, c.call(c.kai, "POST", "/api/rooms", `{"topic":"Private","agent_ids":["`+c.scout.ID.String()+`"]}`))["id"].(string)
    msg := obj(t, c.call(c.kai, "POST", "/api/rooms/"+room+"/messages", `{"text":"secret"}`))["id"].(string)
    routes := []struct{ method, path, body string }{
        {"GET", "", ""}, {"PATCH", "", `{"topic":"Mine"}`},
        {"POST", "/humans", `{"human_id":"` + c.adaH.ID.String() + `"}`}, {"POST", "/agents", `{"agent_id":"` + c.atlas.ID.String() + `"}`},
        {"DELETE", "/agents/" + c.scout.ID.String(), ""}, {"PUT", "/read", `{"message_id":"` + msg + `"}`},
        {"GET", "/messages", ""}, {"POST", "/messages", `{"text":"hi"}`}, {"GET", "/messages/" + msg, ""},
    }
    for _, r := range routes {
        for _, id := range []string{room, ulid.Make().String(), "nope"} {
            w := c.call(c.ada, r.method, "/api/rooms/"+id+r.path, r.body)
            assert.Equal(t, 404, w.Code, "%s %s as ada, room %s", r.method, r.path, id); assert.JSONEq(t, notFound, w.Body.String())
            assert.NotContains(t, w.Body.String(), "secret")
        }
    }
    assert.JSONEq(t, `{"rooms":[]}`, c.call(c.ada, "GET", "/api/rooms", "").Body.String())
    assert.Equal(t, "Private", obj(t, c.call(c.kai, "GET", "/api/rooms/"+room, ""))["topic"]) // and nothing changed
}

func TestRoomRoutes_NeedASessionAndTheSameOrigin(t *testing.T) {
    c := newChat(t); _, bearer, _ := c.svc.CreateAgent(ctx, "third", "Third")
    for _, target := range []string{"/api/rooms", "/api/agents", "/api/humans"} {
        assert.Equal(t, 401, c.call("", "GET", target, "").Code, target)
        assert.Equal(t, 401, do(c.e, "GET", target, "Authorization", "Bearer "+bearer).Code, target) // an agent's token is not a session
    }
    assert.Equal(t, 401, c.call("", "POST", "/api/rooms", `{}`).Code)
    assert.Equal(t, 403, do(c.e, "POST", "/api/rooms", "Cookie", c.kai).Code)                                // no Origin
    assert.Equal(t, 403, do(c.e, "DELETE", "/api/rooms/x/agents/y", "Cookie", c.kai, "Origin", "https://evil.test").Code)
}

func TestDirectory(t *testing.T) {
    c := newChat(t); require.NoError(t, c.svc.RevokeAgent(ctx, "atlas"))
    w := c.call(c.kai, "GET", "/api/agents", ""); require.Equal(t, 200, w.Code)
    assert.JSONEq(t, fmt.Sprintf(`{"agents":[{"id":%q,"handle":"atlas","display_name":"Atlas","revoked":true},{"id":%q,"handle":"scout","display_name":"Scout","revoked":false}]}`, c.atlas.ID, c.scout.ID), w.Body.String())
    w = c.call(c.ada, "GET", "/api/humans", ""); require.Equal(t, 200, w.Code)
    assert.JSONEq(t, fmt.Sprintf(`{"humans":[{"id":%q,"username":"ada"},{"id":%q,"username":"kai"}]}`, c.adaH.ID, c.kaiH.ID), w.Body.String())
    assert.NotContains(t, w.Body.String(), "argon2")
}

// message_test.go (package app_test)
func TestMessages_PostGetAndList(t *testing.T) {
    c := newChat(t); room := "/api/rooms/" + obj(t, c.call(c.kai, "POST", "/api/rooms", `{"topic":"T","agent_ids":["`+c.scout.ID.String()+`"]}`))["id"].(string)
    w := c.call(c.kai, "POST", room+"/messages", `{"text":"hello *world*"}`); require.Equal(t, 201, w.Code, w.Body.String()); first := obj(t, w)
    assert.Equal(t, "hello *world*", first["text"]); assert.Nil(t, first["reply_to_id"]); assert.Contains(t, first, "reply_to_id")
    assert.Equal(t, map[string]any{"kind": "human", "id": c.kaiH.ID.String(), "name": "kai"}, first["author"])
    _, err := time.Parse(time.RFC3339Nano, first["created_at"].(string)); assert.NoError(t, err)
    assert.JSONEq(t, w.Body.String(), c.call(c.kai, "GET", room+"/messages/"+first["id"].(string), "").Body.String())

    w = c.call(c.kai, "POST", room+"/messages", `{"text":"a reply","reply_to_id":"`+first["id"].(string)+`"}`); require.Equal(t, 201, w.Code); second := obj(t, w)
    assert.Equal(t, first["id"], second["reply_to_id"])
    w = c.call(c.kai, "POST", room+"/messages", `{"text":"x","reply_to_id":null}`); assert.Equal(t, 201, w.Code)

    list := obj(t, c.call(c.kai, "GET", room+"/messages", ""))["messages"].([]any); require.Len(t, list, 3)
    assert.Equal(t, first["id"], list[0].(map[string]any)["id"]) // oldest first
    list = obj(t, c.call(c.kai, "GET", room+"/messages?limit=1&before="+second["id"].(string), ""))["messages"].([]any)
    require.Len(t, list, 1); assert.Equal(t, first["id"], list[0].(map[string]any)["id"])
    assert.JSONEq(t, `{"messages":[]}`, c.call(c.kai, "GET", room+"/messages?before="+first["id"].(string), "").Body.String())

    r := obj(t, c.call(c.kai, "GET", room, "")); assert.NotNil(t, r["last_message_id"]); assert.Equal(t, r["last_message_id"], r["last_read_message_id"])
}

func TestMessages_Refused(t *testing.T) {
    c := newChat(t); room := "/api/rooms/" + obj(t, c.call(c.kai, "POST", "/api/rooms", `{"topic":"T","agent_ids":["`+c.scout.ID.String()+`"]}`))["id"].(string)
    for _, q := range []string{"limit=abc", "limit=0", "limit=201", "limit=-1", "limit="} {
        w := c.call(c.kai, "GET", room+"/messages?"+q, ""); assert.Equal(t, 400, w.Code, q); assert.JSONEq(t, badReq("limit must be an integer from 1 to 200"), w.Body.String(), q)
    }
    w := c.call(c.kai, "GET", room+"/messages?before=nope", ""); assert.Equal(t, 400, w.Code); assert.JSONEq(t, badReq("before must be an id"), w.Body.String())
    assert.Equal(t, 200, c.call(c.kai, "GET", room+"/messages?limit=200", "").Code)

    tooLong := `{"text":"` + strings.Repeat("a", bus.MaxMessageBytes+1) + `"}` // under the body cap, over the text cap
    for _, body := range []string{`{}`, `{"text":"   "}`, tooLong} {
        w := c.call(c.kai, "POST", room+"/messages", body); assert.Equal(t, 400, w.Code, "%.20q", body)
        assert.JSONEq(t, `{"error":{"code":"validation","message":"message text must be 1 to 262144 bytes, not blank, with no NUL character"}}`, w.Body.String())
    }
    w = c.call(c.kai, "POST", room+"/messages", `{"text":"hi","reply_to_id":"`+ulid.Make().String()+`"}`)
    assert.JSONEq(t, `{"error":{"code":"validation","message":"reply_to must be a message in this room"}}`, w.Body.String())
    assert.Equal(t, 404, c.call(c.kai, "GET", room+"/messages/nope", "").Code)
    assert.Equal(t, 404, c.call(c.kai, "GET", room+"/messages/"+ulid.Make().String(), "").Code)
}

func TestMarkRead(t *testing.T) {
    c := newChat(t); room := "/api/rooms/" + obj(t, c.call(c.kai, "POST", "/api/rooms", `{"topic":"T","agent_ids":["`+c.scout.ID.String()+`"]}`))["id"].(string)
    c.call(c.kai, "POST", room+"/humans", `{"human_id":"`+c.adaH.ID.String()+`"}`)
    msg := obj(t, c.call(c.kai, "POST", room+"/messages", `{"text":"hello"}`))["id"]
    r := obj(t, c.call(c.ada, "GET", room, "")); assert.Equal(t, msg, r["last_message_id"]); assert.Nil(t, r["last_read_message_id"]) // unread for ada
    w := c.call(c.ada, "PUT", room+"/read", `{"message_id":"`+msg.(string)+`"}`); assert.Equal(t, 204, w.Code); assert.Empty(t, w.Body.String())
    assert.Equal(t, msg, obj(t, c.call(c.ada, "GET", room, ""))["last_read_message_id"])
}

// A message whose author was removed is sent with "author": null.
func TestMessages_RemovedAuthorIsNull(t *testing.T) {
    c := newChat(t); room := "/api/rooms/" + obj(t, c.call(c.kai, "POST", "/api/rooms", `{"topic":"T","agent_ids":["`+c.scout.ID.String()+`"]}`))["id"].(string)
    c.call(c.kai, "POST", room+"/humans", `{"human_id":"`+c.adaH.ID.String()+`"}`); c.call(c.ada, "POST", room+"/messages", `{"text":"from ada"}`)
    require.NoError(t, c.svc.RemoveHuman(ctx, "ada"))
    list := obj(t, c.call(c.kai, "GET", room+"/messages", ""))["messages"].([]any); require.Len(t, list, 1)
    m := list[0].(map[string]any); assert.Contains(t, m, "author"); assert.Nil(t, m["author"]); assert.Equal(t, "from ada", m["text"])
    assert.Equal(t, 401, c.call(c.ada, "GET", "/api/rooms", "").Code) // and her session went with her
}

// errors_test.go: add to TestWriteError_Table
//   {bus.ErrRoomArchived, 409, `{"error":{"code":"room_archived","message":"room is archived"}}`},
//   {fmt.Errorf("subscribe: %w", bus.ErrUnavailable), 503, `{"error":{"code":"unavailable","message":"shutting down; try again shortly"}}`},

// cmd/hermes-chat/routes_test.go (package main). Task 7 adds "GET /api/events".
func TestRoutes_Table(t *testing.T) {
    gin.SetMode(gin.TestMode); e := gin.New()
    routes(e, app.NewController(bus.NewService(storagetest.NewRepo(t), nil), app.Options{}), newLogger(io.Discard, -4))
    var got []string
    for _, r := range e.Routes() { got = append(got, r.Method+" "+r.Path) }
    sort.Strings(got)
    assert.Equal(t, []string{
        "DELETE /api/rooms/:room/agents/:agent",
        "GET /agent/me", "GET /api/agents", "GET /api/humans", "GET /api/me", "GET /api/rooms", "GET /api/rooms/:room",
        "GET /api/rooms/:room/messages", "GET /api/rooms/:room/messages/:message", "GET /healthz",
        "PATCH /api/rooms/:room",
        "POST /api/dms", "POST /api/login", "POST /api/logout", "POST /api/rooms", "POST /api/rooms/:room/agents",
        "POST /api/rooms/:room/humans", "POST /api/rooms/:room/messages",
        "PUT /api/rooms/:room/read",
    }, got)
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./cmd/hermes-chat/...`
Expected: FAIL to compile: `ctrl.ListRooms` and the other handlers are undefined.

- [ ] **Step 3: Implement `json.go`, `room.go`, `message.go`, the two `errorTable` rows, the `Controller` additions, and the routes**

Response shapes are structs with `json` tags in `json.go`; a nil slice is replaced by an empty one before encoding, and a zero ULID or nil author becomes `null`. Handlers take the actor from `actorFrom(c)` and pass it to the bus; none of them decides who may do what.

- [ ] **Step 4: Run to verify pass**

Run: `just check`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/hermes-chat
git commit -m "Add the rooms and messages HTTP API"
```

---

### Task 7: The event stream: `internal/sse` and `GET /api/events`

**Files:**
- Create: `cmd/hermes-chat/internal/sse/sse.go`, `cmd/hermes-chat/internal/app/stream.go`
- Modify: `app/app.go` (`Options`, `Controller`), `cmd/hermes-chat/routes.go`, `cmd/hermes-chat/layering.go`
- Test: `internal/sse/sse_test.go`, `app/stream_test.go`, `app/auth_test.go` (`apiEngine`), `cmd/hermes-chat/routes_test.go`, `cmd/hermes-chat/layering_test.go`

**Interfaces:**
- Consumes: `Service.Subscribe`, `Subscription`, `bus.FromNow`, `Service.ResolveSession`, `roomJSON`, `messageJSON`, `badRequest`, `actorFrom`, `SessionCookie`.
- Produces:
  ```go
  // Package sse reads and writes Server-Sent Events frames. It imports no layer.
  package sse
  // Frame is one event, or one comment when only Comment is set.
  type Frame struct {
      ID      string // "" writes no id line
      Event   string // "" writes no event line
      Data    string // may hold newlines
      Comment string
  }
  func Write(w io.Writer, f Frame) error
  type Decoder struct{ /* unexported */ }
  func NewDecoder(r io.Reader) *Decoder
  func (d *Decoder) Next() (Frame, error) // io.EOF at the end of the stream; an unfinished frame is dropped

  package app
  const (
      DefaultHeartbeatInterval  = 25 * time.Second
      DefaultStreamWriteTimeout = 10 * time.Second
  )
  // added to Options; zero means the default
  HeartbeatInterval  time.Duration
  StreamWriteTimeout time.Duration
  // added to Controller
  Events(c *gin.Context)
  ```

Behavior pinned by this task:

- `sse.Write` writes, in this order, `: <comment>` if `Comment` is set; and unless the frame is a comment alone, `id: <ID>` if set, `event: <Event>` if set, and one `data: <line>` for each line of `Data` (so an empty `Data` still writes one `data: ` line); then a blank line. Lines end in `\n`. An `ID` or `Event` containing `\r` or `\n` is an error and nothing is written.
- `sse.Decoder` accepts `\n` and `\r\n` line endings, strips one space after the colon, joins several `data` lines with `\n`, and returns comment-only frames too. It must read a single line of at least 1 MiB (a message's JSON is one line), so it reads with `bufio.Reader`, not a `bufio.Scanner` at its default buffer.
- `Events`, route `GET /api/events` in the human group:
  1. The cursor is the `Last-Event-ID` header if it is present and not empty, else the `cursor` query parameter if present, else `bus.FromNow`. The header wins because a browser's `EventSource` keeps the original URL when it reconnects. A value that is not a base-10 integer of zero or more is `badRequest` `cursor must be a non-negative integer`.
  2. `Subscribe`; an error goes to `writeError`. `defer sub.Close()`.
  3. Headers `Content-Type: text/event-stream`, `Cache-Control: no-store`, `X-Accel-Buffering: no`; status 200.
  4. Log once at INFO with `slog.InfoContext`: message `stream opened`, attributes `actor` (the actor's `String()`) and `cursor` (`sub.Cursor()`). The request logger's usual `request` line, written when the handler returns, is the close record.
  5. Write the frame `id: <sub.Cursor()>`, `event: ready`, `data: {"cursor":<sub.Cursor()>}`, and flush.
  6. Loop: call `Poll` until it returns nothing, writing each event as a frame and flushing after each batch; then wait for whichever comes first:
     - the wake channel: a receive loops again; a closed channel ends the stream;
     - the heartbeat: call `ResolveSession` with the request's cookie; if it fails, or names a different actor, end the stream; otherwise write the comment frame `hb` and flush;
     - the request context ending: end the stream.
  7. A `Poll` error is logged with `slog.ErrorContext` and ends the stream. A write or flush error ends the stream silently.
- Event frames: `id` is the event ID; `event` is the kind; `data` is `{"message":<message>}` for `message.posted` and `{"room":<room>}` for `room.changed`.
- Every write, the `ready` frame included, is preceded by `http.NewResponseController(c.Writer).SetWriteDeadline(time.Now().Add(<StreamWriteTimeout>))`. If the first of these calls fails, answer `writeError` with that error (a 500) before any header is sent.
- Ending the stream means returning from the handler. Nothing in `app` starts a goroutine.

- [ ] **Step 1: Write the failing tests**

```go
// internal/sse/sse_test.go (package sse_test)
func TestWrite(t *testing.T) {
    cases := []struct{ f sse.Frame; want string }{
        {sse.Frame{ID: "7", Event: "message.posted", Data: `{"a":1}`}, "id: 7\nevent: message.posted\ndata: {\"a\":1}\n\n"},
        {sse.Frame{Data: "one\ntwo"}, "data: one\ndata: two\n\n"},
        {sse.Frame{Event: "ready"}, "event: ready\ndata: \n\n"},
        {sse.Frame{Comment: "hb"}, ": hb\n\n"},
        {sse.Frame{Comment: "note", Data: "x"}, ": note\ndata: x\n\n"},
    }
    for _, tc := range cases { var b strings.Builder; require.NoError(t, sse.Write(&b, tc.f)); assert.Equal(t, tc.want, b.String()) }
    for _, bad := range []sse.Frame{{ID: "1\n2", Data: "x"}, {Event: "a\rb", Data: "x"}} {
        var b strings.Builder; assert.Error(t, sse.Write(&b, bad)); assert.Empty(t, b.String())
    }
}

func TestDecoder(t *testing.T) {
    in := ": hb\n\nid: 7\nevent: message.posted\ndata: one\ndata:two\n\r\ndata: crlf\r\n\r\nid: 9\ndata: unfinished\n"
    d := sse.NewDecoder(strings.NewReader(in))
    for _, want := range []sse.Frame{{Comment: "hb"}, {ID: "7", Event: "message.posted", Data: "one\ntwo"}, {Data: "crlf"}} {
        got, err := d.Next(); require.NoError(t, err); assert.Equal(t, want, got)
    }
    _, err := d.Next(); assert.ErrorIs(t, err, io.EOF) // the unfinished frame is dropped
}

func TestRoundTrip_OneLineOfAMebibyte(t *testing.T) {
    big := sse.Frame{ID: "1", Event: "message.posted", Data: strings.Repeat("a", 1<<20)}
    var b bytes.Buffer; require.NoError(t, sse.Write(&b, big)); require.NoError(t, sse.Write(&b, sse.Frame{Data: "next"}))
    d := sse.NewDecoder(&b)
    got, err := d.Next(); require.NoError(t, err); assert.Equal(t, big, got)
    got, err = d.Next(); require.NoError(t, err); assert.Equal(t, "next", got.Data)
}

// app/stream_test.go (package app_test)
// syncBuffer is a bytes.Buffer safe to write from the server while the test reads it.
type syncBuffer struct{ /* a mutex and a bytes.Buffer */ }
func (b *syncBuffer) Write(p []byte) (int, error)
func (b *syncBuffer) String() string

// rig is a real HTTP server with GET /api/events behind HumanAuth, the usual three middlewares logging into log,
// kai logged in through the bus, and kai's room with scout (so the log starts with one event and no request lines). tune, if not nil, adjusts the server before it starts.
type rig struct {
    srv    *httptest.Server
    log    *syncBuffer
    svc    bus.Service
    hub    *bus.Hub
    kai    bus.Actor
    room   bus.Room
    cookie string // kai's Cookie header value
    token  string // kai's session token
}
func newRig(t *testing.T, opts app.Options, tune func(*httptest.Server)) rig
// open starts a stream with the given Cookie header value and extra headers, and registers cleanup that closes it.
func (r rig) open(t *testing.T, cookie, query string, hdr ...string) (*http.Response, *sse.Decoder)
// next returns the next frame that is not a comment, failing the test after 5 seconds.
func next(t *testing.T, d *sse.Decoder) sse.Frame
// ended reports whether the stream reaches EOF within 5 seconds.
func ended(d *sse.Decoder) bool
// eventually polls cond every 10 ms for up to 5 seconds.
func eventually(t *testing.T, cond func() bool, msg string)
func (r rig) post(t *testing.T, text string) bus.Message // kai posts to r.room through the bus
func ptr[T any](v T) *T { return &v }

func TestEvents_ReadyThenLive(t *testing.T) {
    r := newRig(t, app.Options{}, nil); r.post(t, "before") // the log now holds 2 events
    resp, d := r.open(t, r.cookie, "")
    require.Equal(t, 200, resp.StatusCode)
    assert.Equal(t, "text/event-stream", resp.Header.Get("Content-Type")); assert.Equal(t, "no-store", resp.Header.Get("Cache-Control"))
    assert.Equal(t, "no", resp.Header.Get("X-Accel-Buffering"))
    assert.Equal(t, sse.Frame{ID: "2", Event: "ready", Data: `{"cursor":2}`}, next(t, d)) // no cursor: start at the present

    m := r.post(t, "after"); f := next(t, d)
    assert.Equal(t, "3", f.ID); assert.Equal(t, "message.posted", f.Event)
    assert.JSONEq(t, fmt.Sprintf(`{"message":{"id":%q,"room_id":%q,"author":{"kind":"human","id":%q,"name":"kai"},"text":"after","reply_to_id":null,"created_at":%q}}`,
        m.ID, r.room.ID, r.kai.ID, stamp(m.CreatedAt)), f.Data)

    _, err := r.svc.UpdateRoom(ctx, r.kai, r.room.ID, bus.RoomUpdate{Topic: ptr("Renamed")}); require.NoError(t, err)
    f = next(t, d); assert.Equal(t, "4", f.ID); assert.Equal(t, "room.changed", f.Event)
    var body struct{ Room map[string]any }; require.NoError(t, json.Unmarshal([]byte(f.Data), &body))
    assert.Equal(t, "Renamed", body.Room["topic"]); assert.Equal(t, m.ID.String(), body.Room["last_read_message_id"])
}

// Review Focus 1.
func TestEvents_Cursors(t *testing.T) {
    r := newRig(t, app.Options{}, nil); m := r.post(t, "one") // events 1 (room) and 2 (message)
    _, d := r.open(t, r.cookie, "?cursor=0")
    assert.Equal(t, sse.Frame{ID: "0", Event: "ready", Data: `{"cursor":0}`}, next(t, d))
    assert.Equal(t, "1", next(t, d).ID); f := next(t, d); assert.Equal(t, "2", f.ID); assert.Contains(t, f.Data, m.ID.String())

    _, d = r.open(t, r.cookie, "?cursor=0", "Last-Event-ID", "1") // the header wins: this is a reconnect
    assert.Equal(t, "1", next(t, d).ID); assert.Equal(t, "2", next(t, d).ID)

    _, d = r.open(t, r.cookie, "", "Last-Event-ID", "500") // ahead of the log
    assert.Equal(t, sse.Frame{ID: "2", Event: "ready", Data: `{"cursor":2}`}, next(t, d))
    r.post(t, "two"); assert.Equal(t, "3", next(t, d).ID)

    for _, bad := range []string{"?cursor=abc", "?cursor=-1", "?cursor=1.5", "?cursor=", "?cursor=99999999999999999999"} {
        resp, _ := r.open(t, r.cookie, bad); body, _ := io.ReadAll(resp.Body)
        assert.Equal(t, 400, resp.StatusCode, bad); assert.JSONEq(t, badReq("cursor must be a non-negative integer"), string(body), bad)
    }
    resp, _ := r.open(t, r.cookie, "", "Last-Event-ID", "nope"); assert.Equal(t, 400, resp.StatusCode)
}

func TestEvents_NeedsASession(t *testing.T) {
    r := newRig(t, app.Options{}, nil); _, bearer, _ := r.svc.CreateAgent(ctx, "atlas", "Atlas")
    resp, _ := r.open(t, "", ""); assert.Equal(t, 401, resp.StatusCode)
    resp, _ = r.open(t, "", "", "Authorization", "Bearer "+bearer); assert.Equal(t, 401, resp.StatusCode)
}

// Review Focus 3.
func TestEvents_OnlyMyRooms(t *testing.T) {
    r := newRig(t, app.Options{}, nil)
    _, err := r.svc.CreateHuman(ctx, "ada", pw); require.NoError(t, err); sess, err := r.svc.Login(ctx, "ada", pw, "192.0.2.2"); require.NoError(t, err)
    _, hers := r.open(t, app.SessionCookie+"="+sess.Token, ""); next(t, hers) // ready
    _, mine := r.open(t, r.cookie, ""); next(t, mine)
    r.post(t, "for kai only"); assert.Equal(t, "message.posted", next(t, mine).Event)
    _, err = r.svc.InviteHuman(ctx, r.kai, r.room.ID, sess.Human.ID); require.NoError(t, err)
    f := next(t, hers) // the first thing ada hears is what she is now entitled to, in log order
    assert.Equal(t, "message.posted", f.Event); assert.Contains(t, f.Data, "for kai only"); assert.Equal(t, "room.changed", next(t, hers).Event)
}

func TestEvents_HeartbeatsAndOutlivesTheServerWriteTimeout(t *testing.T) {
    r := newRig(t, app.Options{HeartbeatInterval: 30 * time.Millisecond}, func(s *httptest.Server) { s.Config.WriteTimeout = 100 * time.Millisecond })
    _, d := r.open(t, r.cookie, ""); f, err := d.Next(); require.NoError(t, err); assert.Equal(t, "ready", f.Event)
    for range 3 { f, err = d.Next(); require.NoError(t, err); assert.Equal(t, sse.Frame{Comment: "hb"}, f) }
    time.Sleep(400 * time.Millisecond) // four times the server's write timeout
    r.post(t, "still here"); assert.Contains(t, next(t, d).Data, "still here")
}

// Review Focus 2.
func TestEvents_EndsWhenTheSessionDoes(t *testing.T) {
    ends := map[string]func(r rig) error{
        "logout": func(r rig) error { return r.svc.Logout(ctx, r.token) },
        "passwd": func(r rig) error { return r.svc.SetHumanPassword(ctx, "kai", "a whole new password") },
        "remove": func(r rig) error { return r.svc.RemoveHuman(ctx, "kai") },
    }
    for name, end := range ends {
        r := newRig(t, app.Options{HeartbeatInterval: 20 * time.Millisecond}, nil)
        _, d := r.open(t, r.cookie, ""); next(t, d)
        require.NoError(t, end(r), name); assert.True(t, ended(d), name)
        resp, _ := r.open(t, r.cookie, ""); assert.Equal(t, 401, resp.StatusCode, name) // and a reconnect is refused
    }
}

func TestEvents_ClosesWhenTheHubStops(t *testing.T) {
    r := newRig(t, app.Options{}, nil); runCtx, cancel := context.WithCancel(ctx); done := make(chan error, 1)
    go func() { done <- r.hub.Run(runCtx) }()
    _, d := r.open(t, r.cookie, ""); next(t, d)
    cancel(); require.NoError(t, <-done); assert.True(t, ended(d))
    resp, _ := r.open(t, r.cookie, ""); body, _ := io.ReadAll(resp.Body)
    assert.Equal(t, 503, resp.StatusCode); assert.JSONEq(t, `{"error":{"code":"unavailable","message":"shutting down; try again shortly"}}`, string(body))
}

// One log line when the stream opens, one when it closes, and the handler does return when the client leaves.
func TestEvents_LogsOpenAndClose(t *testing.T) {
    r := newRig(t, app.Options{}, nil); resp, d := r.open(t, r.cookie, ""); next(t, d)
    assert.Contains(t, r.log.String(), `"msg":"stream opened"`); assert.Contains(t, r.log.String(), `"actor":"`+r.kai.String()+`"`)
    assert.NotContains(t, r.log.String(), `"msg":"request"`, "the request line is written at close")
    resp.Body.Close()
    r.post(t, "wake the handler so it notices") // a write to a closed connection fails
    eventually(t, func() bool { return strings.Contains(r.log.String(), `"msg":"request"`) }, "the handler returned")
    assert.Contains(t, r.log.String(), `"route":"/api/events"`); assert.NotContains(t, r.log.String(), r.token)
}

// Review Focus 5. The client opens the stream and never reads. Enough is sent to fill any socket buffer;
// the write deadline must then end the handler.
func TestEvents_AClientThatStopsReadingIsDropped(t *testing.T) {
    r := newRig(t, app.Options{StreamWriteTimeout: 100 * time.Millisecond}, nil)
    r.open(t, r.cookie, "") // never read
    eventually(t, func() bool { return strings.Contains(r.log.String(), `"msg":"stream opened"`) }, "the stream opened")
    big := strings.Repeat("a", bus.MaxMessageBytes)
    for range 100 { _, err := r.svc.PostMessage(ctx, r.kai, r.room.ID, big, ulid.Zero); require.NoError(t, err) } // about 25 MiB; posting is never held up
    eventually(t, func() bool { return strings.Contains(r.log.String(), `"msg":"request"`) }, "the stalled stream was dropped")
}
```

Also: add `GET /api/events` to `apiEngine`, add `"GET /api/events"` to the expected list in `TestRoutes_Table` (in sorted position, after `GET /api/agents`), and add `{I + "/sse", []string{I + "/bus"}}` to the forbidden cases and `{I + "/app", […, I + "/sse"]}` to the allowed ones in `layering_test.go`.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./cmd/hermes-chat/...`
Expected: FAIL to compile: package `sse`, `ctrl.Events`, and `Options.HeartbeatInterval` are undefined.

- [ ] **Step 3: Implement `internal/sse`**, and add `internalRoot + "/sse": {internalRoot + "/app", internalRoot + "/bus", internalRoot + "/storage"}` to `forbidden` in `layering.go`.

Run: `go test ./cmd/hermes-chat/internal/sse/`
Expected: PASS.

- [ ] **Step 4: Implement `stream.go`, the `Options` fields, the `Controller` addition, and the route**

Use one `time.Ticker` for the heartbeat, stopped with `defer`. Encode each frame's data with `encoding/json` from `roomJSON` and `messageJSON`.

- [ ] **Step 5: Run to verify pass**

Run: `just check && go test -race ./cmd/hermes-chat/internal/app/ ./cmd/hermes-chat/internal/sse/`
Expected: PASS, no race reported.

- [ ] **Step 6: Commit**

```bash
git add cmd/hermes-chat
git commit -m "Add the SSE frame package and the human event stream"
```

---

### Task 8: The hub as a runner, end to end, and the README

**Files:**
- Modify: `cmd/hermes-chat/wires.go`, `serve.go`, `README.md`
- Test: `cmd/hermes-chat/serve_test.go`

**Interfaces:**
- Consumes: `(*service).Hub`, `(*Hub).Run`, `sse.NewDecoder`, the routes from Tasks 6 and 7, the helpers `startServeWithEnv`, `migrated`, `runCmd`, `runCmdIn`, `login`, `tokenLine`.
- Produces:
  ```go
  // added to wires
  Hub *bus.Hub
  // test helpers in serve_test.go
  // apiCall sends a JSON request with Origin set to api and returns the status and the decoded object (nil for an empty body).
  func apiCall(t *testing.T, client *http.Client, method, api, path, body string) (int, map[string]any)
  // openEvents opens GET /api/events with an optional Last-Event-ID and registers cleanup that closes it.
  func openEvents(t *testing.T, client *http.Client, api, lastEventID string) (*http.Response, *sse.Decoder)
  // nextEvent returns the next frame that is not a comment, failing the test after 5 seconds.
  func nextEvent(t *testing.T, d *sse.Decoder) sse.Frame
  ```

Behavior pinned by this task:

- `wireUp` keeps the concrete service long enough to set `w.Hub = svc.Hub()`. It builds the hub in every mode; only `serve` runs it.
- `serve` passes three runners to `grace.Run`: the API server, the debug server, and `w.Hub`.
- With an open stream, cancelling `serve`'s context makes it return nil well inside grace's 5-second grace period, because the hub closes the stream and the API server then has nothing to wait for.

- [ ] **Step 1: Write the failing tests** (`serve_test.go`, package `main`)

```go
// The whole stage across the CLI and a running server: two humans, one agent, one room, one stream each.
func TestServe_RoomsAndStreamEndToEnd(t *testing.T) {
    migrated(t); t.Setenv("HERMES_CHAT_COOKIE_SECURE", "false")
    runCmdIn(t, pw+"\n", "user", "add", "kai"); runCmdIn(t, pw+"\n", "user", "add", "ada"); runCmd(t, "agent", "add", "scout", "Scout")
    api, _, _, _ := startServeWithEnv(t)
    client := func(name string) *http.Client {
        jar, err := cookiejar.New(nil); require.NoError(t, err); c := &http.Client{Jar: jar}
        require.Equal(t, 200, login(t, c, api, name, pw).StatusCode); return c
    }
    kai, ada := client("kai"), client("ada")

    _, agents := apiCall(t, kai, "GET", api, "/api/agents", ""); scout := agents["agents"].([]any)[0].(map[string]any)["id"].(string)
    _, humans := apiCall(t, kai, "GET", api, "/api/humans", ""); adaID := humans["humans"].([]any)[0].(map[string]any)["id"].(string)

    _, mine := openEvents(t, kai, api, ""); ready := nextEvent(t, mine); assert.Equal(t, "ready", ready.Event)
    _, hers := openEvents(t, ada, api, ""); nextEvent(t, hers)

    code, room := apiCall(t, kai, "POST", api, "/api/rooms", `{"topic":"Trip","agent_ids":["`+scout+`"]}`); require.Equal(t, 201, code)
    path := "/api/rooms/" + room["id"].(string)
    assert.Equal(t, "room.changed", nextEvent(t, mine).Event)
    code, _ = apiCall(t, ada, "GET", api, path, ""); assert.Equal(t, 404, code)

    code, _ = apiCall(t, kai, "POST", api, path+"/humans", `{"human_id":"`+adaID+`"}`); require.Equal(t, 200, code)
    assert.Equal(t, "room.changed", nextEvent(t, mine).Event)
    assert.Equal(t, "room.changed", nextEvent(t, hers).Event) // the room's creation, now hers to see
    assert.Equal(t, "room.changed", nextEvent(t, hers).Event) // and the invitation

    code, msg := apiCall(t, ada, "POST", api, path+"/messages", `{"text":"hello from ada"}`); require.Equal(t, 201, code)
    for _, d := range []*sse.Decoder{mine, hers} {
        f := nextEvent(t, d); assert.Equal(t, "message.posted", f.Event); assert.Contains(t, f.Data, msg["id"].(string)); assert.Contains(t, f.Data, "hello from ada")
    }
    _, list := apiCall(t, kai, "GET", api, path+"/messages", ""); assert.Len(t, list["messages"], 1)

    _, again := openEvents(t, kai, api, ready.ID) // a reconnect replays what the first stream saw, in order
    assert.Equal(t, ready.ID, nextEvent(t, again).ID)
    for _, want := range []string{"room.changed", "room.changed", "message.posted"} { assert.Equal(t, want, nextEvent(t, again).Event) }

    code, _, _ = runCmd(t, "user", "remove", "ada"); require.Equal(t, 0, code) // Review Focus 4, while the server is running
    _, list = apiCall(t, kai, "GET", api, path+"/messages", "")
    m := list["messages"].([]any)[0].(map[string]any); assert.Nil(t, m["author"]); assert.Equal(t, "hello from ada", m["text"])
}

func TestServe_ShutdownClosesOpenStreams(t *testing.T) {
    migrated(t); t.Setenv("HERMES_CHAT_COOKIE_SECURE", "false"); runCmdIn(t, pw+"\n", "user", "add", "kai")
    api, _, cancel, done := startServeWithEnv(t)
    jar, _ := cookiejar.New(nil); client := &http.Client{Jar: jar}; require.Equal(t, 200, login(t, client, api, "kai", pw).StatusCode)
    _, d := openEvents(t, client, api, ""); nextEvent(t, d)

    start := time.Now(); cancel()
    select {
    case err := <-done:
        assert.NoError(t, err); assert.Less(t, time.Since(start), 3*time.Second, "the stream must not hold shutdown for the whole grace period")
    case <-time.After(10 * time.Second):
        t.Fatal("serve did not return")
    }
    _, err := d.Next(); assert.Error(t, err) // the stream is over
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./cmd/hermes-chat/ -run 'TestServe_(Rooms|Shutdown)'`
Expected: `TestServe_ShutdownClosesOpenStreams` FAILS: `serve` takes about 5 seconds and returns an error, because nothing closes the stream. (The end-to-end test may already pass; the stream works without `Run`.)

- [ ] **Step 3: Implement** `wires.Hub`, and the third runner in `serve.go`.

- [ ] **Step 4: Run to verify pass**

Run: `just check && go test -race ./cmd/hermes-chat/...`
Expected: PASS; `sqlc diff` clean; no race reported.

- [ ] **Step 5: Update `README.md`**

In "HTTP API": add the fourteen routes of Task 6 and `GET /api/events` to the route table; add the four JSON shapes from Task 6; add the `room_archived` (409) and `unavailable` (503) rows to the status table, and widen the `bad_request` row to "a body, query parameter, or ID that cannot be read". Add a section "Event stream" that says: open `GET /api/events`; the first frame is `ready` with the cursor the stream starts from; with no cursor it starts at the present, so open the stream first and load rooms and messages after `ready`; to resume, send the last `id` seen as `Last-Event-ID` (a browser's `EventSource` does this itself) or `?cursor=`; the two event kinds and their `data`; `room.changed` carries the room as it is now, so apply it by replacing; a comment line arrives every 25 seconds; the stream closes within 25 seconds of the session ending. Add the stage's known limits from the top of this plan. In "Layering", name `sse` beside `reqid` as a leaf.

- [ ] **Step 6: Run it by hand**

```bash
export HERMES_CHAT_DB_PATH=/tmp/hermes-stage3.db HERMES_CHAT_COOKIE_SECURE=false
rm -f /tmp/hermes-stage3.db*
go run ./cmd/hermes-chat migrate
echo 'correct horse battery' | go run ./cmd/hermes-chat user add kai
go run ./cmd/hermes-chat agent add scout 'Scout'
go run ./cmd/hermes-chat serve &
sleep 1
O='Origin: http://localhost:8080'; J='Content-Type: application/json'
curl -s -c /tmp/jar -H "$O" -d '{"username":"kai","password":"correct horse battery"}' localhost:8080/api/login
curl -sN -b /tmp/jar localhost:8080/api/events > /tmp/stream.txt &
sleep 1
AGENT=$(curl -s -b /tmp/jar localhost:8080/api/agents | sed 's/.*"id":"\([^"]*\)".*/\1/')
ROOM=$(curl -s -b /tmp/jar -H "$O" -H "$J" -d "{\"topic\":\"Trip\",\"agent_ids\":[\"$AGENT\"]}" localhost:8080/api/rooms | sed 's/^{"id":"\([^"]*\)".*/\1/')
curl -s -b /tmp/jar -H "$O" -H "$J" -d '{"text":"hello"}' "localhost:8080/api/rooms/$ROOM/messages"
sleep 1
kill -INT %1; wait %1; wait %2
cat /tmp/stream.txt
```

Expected: `/tmp/stream.txt` holds three frames, `event: ready` with `id: 0`, `event: room.changed` with `id: 1`, and `event: message.posted` with `id: 2` whose data contains `"text":"hello"`. `serve` exits within a second of the interrupt with status 0, and `curl` exits by itself because the server closed the stream. The JSON log on stderr has a `stream opened` line and, at shutdown, one `request` line for `/api/events`; no line contains the cookie value.

- [ ] **Step 7: Cross-compile and commit**

```bash
just build linux amd64 && just build linux arm64
git add README.md cmd/hermes-chat
git commit -m "Run the hub under grace and document the rooms API and event stream"
```
