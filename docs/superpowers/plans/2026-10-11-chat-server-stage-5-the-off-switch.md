# Stage 5: The Off Switch Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A human can pause a room, stop everything, and rely on a per-room turn cap, and all three hold on the server alone even when an agent ignores the signal.

**Architecture:** Pause state lives on the room row (`paused_at`, `pause_reason`), the global halt in a one-row `halt` table, and the turn cap as a per-room cap plus a counter of agent-woken turns. One gate decides everything: `releaseHead` re-reads the room and the halt inside the transaction and releases nothing while the room is closed, and every agent write into a closed room is refused with `ErrPaused`. Agents are told with an agent-addressed `pause` event per (room, agent); humans see `room.changed`.

**Tech Stack:** Go 1.26, gin, SQLite (`modernc.org/sqlite`), goose migrations, sqlc 1.31.1, testify. No new dependencies.

**Spec:** `docs/superpowers/specs/2026-10-11-chat-server-architecture-design.md` (section 14, stage 5) and `docs/superpowers/specs/2026-10-11-hermes-chat-design.md` (section 6.1, anti-goals in section 2, section 5.6). Read `AGENTS.md` first.

## Global Constraints

- Layering is enforced by `TestLayering_ThisRepository`: rules live in `bus`, SQL in `storage`, HTTP in `app`. `bus` never returns a storage sentinel and never builds an HTTP response.
- "Steps 1 and 2 are enforced by the server alone and hold even if step 3 fails." No pause behavior may depend on an agent receiving or obeying the `pause` event.
- "Queued messages stay queued": pausing never cancels, fails, or reorders a delivery.
- "A paused room stays paused until a human resumes it." Nothing but `ResumeRoom` clears a room's pause.
- "Lifting it returns each room to whatever pause state it had before": the halt is stored apart from each room's own pause and never touches it.
- Turn cap: "counts turns in a room that were woken by an agent, not by a human, since the last human message. … Default cap: 6, configurable per room. Turns woken by humans and unprompted deliveries … do not count."
- Every state change writes its event rows in the same transaction (`s.record`), and the hub is rung only after commit. Time comes from `s.now()`; IDs are ULIDs; the event cursor stays an integer.
- Every table is `STRICT`; after touching migrations or queries run `just generate`; `sqlcgen/` is committed and never hand-edited.
- New error kinds go in three places: `bus/bus.go`, `app/errors.go`'s `errorTable`, and `README.md`'s table.
- Tests: testify `assert`/`require`, black-box `_test` packages, real layers below, a mock only to force an error. `CGO_ENABLED=0`; no cgo dependency.
- `just check` must pass at the end of every task. End commit messages with the attribution lines the session gives you.

## Decisions this plan makes (Claude's calls; change before execution if wrong)

1. **A closed room refuses every agent write that adds content**: posting, editing (final included), remarks, and tool starts answer `ErrPaused`. Turn reports (`working`, `done`, `failed`) and finishing an already-started tool are bookkeeping and still land, so a plugin that stops its turn can say so. A streaming message cut off by a pause stays unfinalized, with the text of its last stored edit.
2. **A human may still post, cancel, and stop in a paused room**; the messages queue. Steer is refused with `ErrPaused`.
3. **A "turn" is counted when an agent-authored message's delivery is released** to its agent. The release that would be turn cap+1 does not happen: the room pauses with reason `turn_cap` and that delivery stays `waiting`.
4. **The counter resets on a human message, a steer, and a human resume.** Resuming a cap-paused room is a human in the loop, so it buys another cap's worth of turns. Lifting the global halt does not reset it.
5. **Cap range 1–100.** A DM's cap cannot be changed (DMs are already unchangeable, and a DM has one agent, so it never counts a turn).
6. **Stop everything is shown to humans as one `room.changed` per room** carrying `"halted":true`, plus `GET /api/halt` for a human with no rooms. The event log requires a room, and the UI already replaces a room on `room.changed`.
7. **Agents get no resume event.** Deliveries simply start again. `GET /agent/rooms` gains `"paused"` so a plugin that reconnects without a cursor can learn what it missed.
8. **One error for both levels**: `ErrPaused` → 409 `paused`.
9. **Pausing works on any room the human is in**, DMs and archived rooms included: the off switch never refuses.

## Review Focus

1. **An agent reconnects while its room is paused** (with an old cursor, or with none): it must be handed no `delivery` for that room, and the delivery must not be lost once the room resumes. → Task 3.
2. **One agent message wakes several agents when the room is one turn short of the cap**: only the turns up to the cap are released; the rest wait. A room row read before the pause must not let the later ones through. → Task 5, `TestTurnCap_OneMessageWakingSeveralStopsAtTheCap`.
3. **A pause lands between a release and the agent's first report**: the late `working`/`done` reports are accepted, nothing stalls, and the next queued message is not released until resume. → Task 2, `TestPauseRoom_LateTurnReportsStillLand`.
4. **Double taps and crossed switches**: pausing twice, resuming twice, stopping everything twice, and resuming a room while everything is stopped must not error, write duplicate events, or release anything. → Tasks 2 and 4.
5. **The server restarts while paused or halted**: the state is in the database, so a fresh service over the same file is still closed and a reconnecting agent's sweep releases nothing. → Task 4, `TestHalt_SurvivesARestart`.

---

## File Structure

| File | Change |
|---|---|
| `cmd/hermes-chat/internal/storage/migrations/00006_off_switch.sql` | new: four `rooms` columns, the `halt` table |
| `cmd/hermes-chat/internal/storage/queries/rooms.sql`, `deliveries.sql` | new columns in the three room SELECTs; five new queries |
| `cmd/hermes-chat/internal/storage/queries/halt.sql`, `storage/halt.go`, `storage/halt_test.go` | new: the halt row |
| `cmd/hermes-chat/internal/storage/storage.go`, `room.go`, `delivery.go` | `Repository` methods and `Room` fields |
| `cmd/hermes-chat/internal/bus/pause.go`, `pause_test.go` | new: pause, resume, the gate, re-hand on resume |
| `cmd/hermes-chat/internal/bus/halt.go`, `halt_test.go` | new: stop everything |
| `cmd/hermes-chat/internal/bus/turncap.go`, `turncap_test.go` | new: the count and the self-pause |
| `cmd/hermes-chat/internal/bus/bus.go`, `room.go`, `delivery.go`, `message.go`, `turn.go`, `hub.go`, `event.go` | `ErrPaused`, `Service` methods, `Room` fields, the call sites of the gate |
| `cmd/hermes-chat/internal/app/pause.go`, `pause_test.go` | new: five handlers |
| `cmd/hermes-chat/internal/app/app.go`, `json.go`, `errors.go`, `room.go`, `stream.go` | `Controller`, room JSON, error row, `turn_cap` in PATCH, the `pause` frame |
| `cmd/hermes-chat/routes.go`, `routes_test.go`, `offswitch_e2e_test.go` | five routes; end-to-end tests |
| `README.md`, `AGENTS.md` | the API contract and the stage list |

---

### Task 1: Storage for pause, halt, and the turn count

**Files:**
- Create: `cmd/hermes-chat/internal/storage/migrations/00006_off_switch.sql`, `queries/halt.sql`, `halt.go`, `halt_test.go`
- Modify: `queries/rooms.sql`, `queries/deliveries.sql`, `storage.go`, `room.go`, `delivery.go`
- Test: `room_test.go`, `delivery_test.go`, `halt_test.go`, `migrate_test.go` (all under `cmd/hermes-chat/internal/storage/`)

**Interfaces:**
- Produces, on `storage.Room`: `PausedAtMs *int64` (nil while running), `PauseReason string` (`""`, `"human"`, or `"turn_cap"`), `TurnCap int64`, `AgentTurns int64`. `PutRoom` does not write them: a new room is unpaused with cap 6 and count 0.
- Produces, on `storage.Repository`:

```go
SetRoomPause(ctx context.Context, id string, pausedAtMs *int64, reason string) error // ErrNotFound; nil and "" clear
SetRoomTurnCap(ctx context.Context, id string, turnCap int64) error                  // ErrNotFound
SetRoomAgentTurns(ctx context.Context, id string, turns int64) error                 // ErrNotFound
ListAllRoomAgents(ctx context.Context) ([]RoomAgent, error)                          // ordered by room id, then agent id; empty, not nil
GetHaltedAtMs(ctx context.Context) (*int64, error)                                   // nil while not halted
SetHaltedAtMs(ctx context.Context, haltedAtMs *int64) error                          // nil clears
GetDeliveredDelivery(ctx context.Context, roomID, agentID string) (Delivery, error)  // ErrNotFound: the pair has no delivery in 'delivered'
```

- [ ] **Step 1: Write the failing tests**

In `room_test.go`, add `TurnCap: 6` to the `trip` and `dm` fixtures (a read-back room now carries the default), then:

```go
func TestRoom_PauseCapAndTurns(t *testing.T) {
	r := seeded(t)
	require.NoError(t, r.PutRoom(ctx, trip))
	got, _ := r.GetRoom(ctx, trip.ID)
	assert.Nil(t, got.PausedAtMs)
	assert.Equal(t, "", got.PauseReason)
	assert.EqualValues(t, 6, got.TurnCap)
	assert.EqualValues(t, 0, got.AgentTurns)

	require.NoError(t, r.SetRoomPause(ctx, trip.ID, ms(5), "turn_cap"))
	require.NoError(t, r.SetRoomTurnCap(ctx, trip.ID, 3))
	require.NoError(t, r.SetRoomAgentTurns(ctx, trip.ID, 2))
	got, _ = r.GetRoom(ctx, trip.ID)
	assert.Equal(t, ms(5), got.PausedAtMs)
	assert.Equal(t, "turn_cap", got.PauseReason)
	assert.EqualValues(t, 3, got.TurnCap)
	assert.EqualValues(t, 2, got.AgentTurns)

	require.NoError(t, r.SetRoomPause(ctx, trip.ID, nil, ""))
	got, _ = r.GetRoom(ctx, trip.ID)
	assert.Nil(t, got.PausedAtMs)

	assert.Error(t, r.SetRoomTurnCap(ctx, trip.ID, 0))   // the table CHECKs 1..100
	assert.Error(t, r.SetRoomTurnCap(ctx, trip.ID, 101))
	assert.Error(t, r.SetRoomPause(ctx, trip.ID, ms(5), "because"))
	assert.ErrorIs(t, r.SetRoomPause(ctx, "nope", ms(5), "human"), storage.ErrNotFound)
	assert.ErrorIs(t, r.SetRoomTurnCap(ctx, "nope", 3), storage.ErrNotFound)
	assert.ErrorIs(t, r.SetRoomAgentTurns(ctx, "nope", 1), storage.ErrNotFound)

	// the list queries carry the same columns
	require.NoError(t, r.PutRoomHuman(ctx, storage.RoomHuman{RoomID: trip.ID, HumanID: kai.ID, JoinedAtMs: 1}))
	list, _ := r.ListRoomsByHuman(ctx, kai.ID)
	require.Len(t, list, 1)
	assert.EqualValues(t, 3, list[0].TurnCap)
}

func TestListAllRoomAgents(t *testing.T) {
	r := seeded(t)
	none, err := r.ListAllRoomAgents(ctx)
	require.NoError(t, err)
	assert.NotNil(t, none)
	assert.Empty(t, none)
	require.NoError(t, r.PutRoom(ctx, trip))
	require.NoError(t, r.PutRoom(ctx, dm))
	a := storage.RoomAgent{RoomID: trip.ID, AgentID: scout.ID, JoinedAtMs: 1}
	b := storage.RoomAgent{RoomID: trip.ID, AgentID: atlas.ID, JoinedAtMs: 1}
	c := storage.RoomAgent{RoomID: dm.ID, AgentID: scout.ID, JoinedAtMs: 1}
	for _, m := range []storage.RoomAgent{a, b, c} {
		require.NoError(t, r.PutRoomAgent(ctx, m))
	}
	got, err := r.ListAllRoomAgents(ctx)
	require.NoError(t, err)
	want := []storage.RoomAgent{a, b, c}
	sort.Slice(want, func(i, j int) bool {
		if want[i].RoomID != want[j].RoomID {
			return want[i].RoomID < want[j].RoomID
		}
		return want[i].AgentID < want[j].AgentID
	})
	assert.Equal(t, want, got)
}
```

In `halt_test.go`:

```go
func TestHalt(t *testing.T) {
	r := storagetest.NewRepo(t)
	got, err := r.GetHaltedAtMs(ctx)
	require.NoError(t, err)
	assert.Nil(t, got) // a migrated database is not halted
	require.NoError(t, r.SetHaltedAtMs(ctx, ms(7)))
	got, _ = r.GetHaltedAtMs(ctx)
	assert.Equal(t, ms(7), got)
	require.NoError(t, r.SetHaltedAtMs(ctx, nil))
	got, _ = r.GetHaltedAtMs(ctx)
	assert.Nil(t, got)
}
```

In `delivery_test.go`:

```go
func TestGetDeliveredDelivery(t *testing.T) {
	r := roomed(t)
	m1 := msg("1", trip.ID, kai.ID)
	require.NoError(t, r.PutMessage(ctx, m1))
	d := storage.Delivery{RoomID: trip.ID, AgentID: scout.ID, MessageID: m1.ID, State: "waiting"}
	require.NoError(t, r.PutDelivery(ctx, d))
	_, err := r.GetDeliveredDelivery(ctx, trip.ID, scout.ID)
	assert.ErrorIs(t, err, storage.ErrNotFound)
	require.NoError(t, r.SetDeliveryState(ctx, trip.ID, scout.ID, m1.ID, "delivered"))
	got, err := r.GetDeliveredDelivery(ctx, trip.ID, scout.ID)
	require.NoError(t, err)
	d.State = "delivered"
	assert.Equal(t, d, got)
	_, err = r.GetDeliveredDelivery(ctx, trip.ID, atlas.ID)
	assert.ErrorIs(t, err, storage.ErrNotFound)
}
```

In `migrate_test.go`, beside the existing `assert.Contains(t, tables(t, db), "humans")`, add `assert.Contains(t, tables(t, db), "halt")`.

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./cmd/hermes-chat/internal/storage/`
Expected: FAIL to compile (`r.SetRoomPause undefined`, `got.TurnCap undefined`).

- [ ] **Step 3: Write the migration `00006_off_switch.sql`**

This exact file was applied up and down against sqlite3 3.46.1 and generated with sqlc 1.31.1 while planning. The CHECKs are column constraints on purpose: SQLite can drop a column whose only CHECK is its own.

```sql
-- +goose Up
ALTER TABLE rooms ADD COLUMN paused_at INTEGER;
ALTER TABLE rooms ADD COLUMN pause_reason TEXT NOT NULL DEFAULT '' CHECK (pause_reason IN ('', 'human', 'turn_cap'));
ALTER TABLE rooms ADD COLUMN turn_cap INTEGER NOT NULL DEFAULT 6 CHECK (turn_cap BETWEEN 1 AND 100);
ALTER TABLE rooms ADD COLUMN agent_turns INTEGER NOT NULL DEFAULT 0 CHECK (agent_turns >= 0);

CREATE TABLE halt (
    id        INTEGER NOT NULL PRIMARY KEY CHECK (id = 1),
    halted_at INTEGER
) STRICT;

INSERT INTO halt (id) VALUES (1);

-- +goose Down
DROP TABLE halt;
ALTER TABLE rooms DROP COLUMN agent_turns;
ALTER TABLE rooms DROP COLUMN turn_cap;
ALTER TABLE rooms DROP COLUMN pause_reason;
ALTER TABLE rooms DROP COLUMN paused_at;
```

- [ ] **Step 4: Add the queries and run `just generate`**

In `queries/rooms.sql`, append `paused_at, pause_reason, turn_cap, agent_turns` (in that order, `r.`-prefixed in `ListRoomsByHuman`) to the column lists of `GetRoom`, `GetDMRoom`, and `ListRoomsByHuman`, so all three still return `sqlcgen.Room`. `InsertRoom` is unchanged. Then add:

```sql
-- name: SetRoomPause :execrows
UPDATE rooms SET paused_at = ?, pause_reason = ? WHERE id = ?;

-- name: SetRoomTurnCap :execrows
UPDATE rooms SET turn_cap = ? WHERE id = ?;

-- name: SetRoomAgentTurns :execrows
UPDATE rooms SET agent_turns = ? WHERE id = ?;

-- name: ListAllRoomAgents :many
SELECT room_id, agent_id, joined_at FROM room_agents ORDER BY room_id, agent_id;
```

`queries/halt.sql`:

```sql
-- name: GetHaltedAt :one
SELECT halted_at FROM halt WHERE id = 1;

-- name: SetHaltedAt :execrows
UPDATE halt SET halted_at = ? WHERE id = 1;
```

`queries/deliveries.sql`:

```sql
-- name: GetDeliveredDelivery :one
SELECT room_id, agent_id, message_id, state FROM deliveries
WHERE room_id = ? AND agent_id = ? AND state = 'delivered'
ORDER BY message_id LIMIT 1;
```

Run: `just generate`

- [ ] **Step 5: Implement the seven `Repository` methods and the `Room` fields**

Declare them in `storage.go`'s interface with the comments from the Interfaces block. `roomFromRow` fills the four new fields. The setters follow `SetRoomArchivedAt` (`requireRow`, `nullInt64`); the two halt methods live in `halt.go`; `GetDeliveredDelivery` follows `GetWorkingDelivery`.

- [ ] **Step 6: Run the tests**

Run: `just check`
Expected: PASS (`sqlc diff` clean, vet clean, all packages ok).

- [ ] **Step 7: Commit**

```bash
git add cmd/hermes-chat/internal/storage
git commit -m "feat(storage): room pause state, turn cap and count, and the halt row"
```

---

### Task 2: Pause and resume a room

**Files:**
- Create: `cmd/hermes-chat/internal/bus/pause.go`, `pause_test.go`
- Modify: `bus/bus.go`, `room.go`, `delivery.go`, `message.go`, `turn.go`, `hub.go`, `event.go`

**Interfaces:**
- Consumes: Task 1's `SetRoomPause`, `SetRoomAgentTurns`, and the `storage.Room` fields.
- Produces:

```go
// bus.go
var ErrPaused = errors.New("paused") // the room is paused, or everything is stopped

// Service
PauseRoom(ctx context.Context, actor Actor, roomID ulid.ULID) (Room, error)  // human member only; ErrForbidden, ErrNotFound; already paused is a no-op
ResumeRoom(ctx context.Context, actor Actor, roomID ulid.ULID) (Room, error) // the same; not paused is a no-op

// room.go
type PauseReason string
const (
	PauseNone    PauseReason = ""
	PauseHuman   PauseReason = "human"
	PauseTurnCap PauseReason = "turn_cap"
)
// Room gains: PausedAt time.Time (zero while running), PauseReason PauseReason, TurnCap int, Halted bool
func (r Room) Paused() bool // !r.PausedAt.IsZero()

// event.go
EventControlPause EventKind = "pause" // agent: subject is the room

// pause.go
// paused reports whether the room is closed to agents: paused itself, or (Task 4) everything is stopped.
func paused(ctx context.Context, r storage.Repository, row storage.Room) (bool, error)
// pauseRoom marks the room paused for reason and writes its events: one room.changed, and one pause addressed to each agent in the room.
func (s *service) pauseRoom(ctx context.Context, r storage.Repository, row storage.Room, reason PauseReason, at time.Time) error
// resumeDeliveries hands each agent in the room its next message, if the room is open.
func (s *service) resumeDeliveries(ctx context.Context, r storage.Repository, roomID string, at time.Time) error

// delivery.go — the signature changes: it takes the room's id and reads the room itself
func (s *service) releaseHead(ctx context.Context, r storage.Repository, roomID string, agent Agent, at time.Time, notify bool) error
```

- [ ] **Step 1: Write the failing tests in `pause_test.go`**

Helpers, used by Tasks 3–5 as well:

```go
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
```

```go
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
	_, err = w.s.Steer(ctx, w.kai, room.ID, w.scout.ID, "faster")
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
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./cmd/hermes-chat/internal/bus/ -run TestPauseRoom`
Expected: FAIL to compile (`w.s.PauseRoom undefined`).

- [ ] **Step 3: Add the types, the error, and the room fields**

`ErrPaused`, the two `Service` methods, `PauseReason`, `Room`'s four fields and `Paused()`, and `EventControlPause`, exactly as in the Interfaces block. `buildRoomLayout` fills `PausedAt`, `PauseReason`, and `TurnCap` from the row (`Halted` stays false until Task 4). In `hub.go`'s `Subscription.event`, `EventControlPause` loads nothing, like `EventControlStop`.

- [ ] **Step 4: Make `releaseHead` read the room itself**

New signature in the Interfaces block. It calls `r.GetRoom(ctx, roomID)` first (a room that is gone releases nothing, returning nil) and returns nil when the room is archived, `paused(...)` is true, or the agent has no live stream. It must not trust a row its caller read earlier: Task 5 pauses a room from inside this function, and the next call in the same transaction has to see that.

Update the five callers to pass the room id: `CancelMessage`, `PostMessage`, `turnReport` (drop its own `GetRoom`), `releaseSweep` (drop its `GetRoom`), and `thawQueue`.

- [ ] **Step 5: Implement `pause.go`**

`paused` returns `row.PausedAtMs != nil` for now. `PauseRoom` and `ResumeRoom` run in `s.record`, check membership with `roomForMember` (which gives `ErrForbidden` for an agent), do nothing when the room is already in the wanted state, and return `s.GetRoom` after the commit. `PauseRoom` calls `pauseRoom(..., PauseHuman, at)`. `ResumeRoom` clears the pause with `SetRoomPause(ctx, id, nil, "")`, sets the agent-turn count to 0, writes one `room.changed`, then calls `resumeDeliveries`.

`resumeDeliveries` replaces `thawQueue` (delete it; `UpdateRoom`'s unarchive branch calls `resumeDeliveries` instead): for each agent in the room, `releaseHead(ctx, r, roomID, agent, at, true)`.

- [ ] **Step 6: Refuse agent writes into a closed room**

Each returns `ErrPaused` when `paused(...)` is true for the room:

| Where | Check goes |
|---|---|
| `PostMessage` | after the archived check, only when `actor.Kind == ActorAgent` |
| `EditMessage` | after `requireAgentInRoom`, before the stale-edit return |
| `requireOpenTurn` (remarks and tool starts) | after `requireAgentInRoom`; it loads the room with `r.GetRoom(ctx, d.RoomID)` |
| `Steer` | after the archived check |

`turnReport`, `FinishTool`, `StopTurn`, `CancelMessage`, and a human's `PostMessage` are not gated.

- [ ] **Step 7: Run the tests**

Run: `just check`
Expected: PASS, including every pre-existing bus, app, and e2e test.

- [ ] **Step 8: Commit**

```bash
git add cmd/hermes-chat/internal/bus
git commit -m "feat(bus): pause and resume a room, enforced at release and on agent writes"
```

---

### Task 3: Nothing is handed over on reconnect, and nothing is lost on resume

**Files:**
- Modify: `cmd/hermes-chat/internal/bus/hub.go` (`Subscription.event`), `pause.go` (`resumeDeliveries`)
- Test: `cmd/hermes-chat/internal/bus/pause_test.go`

**Interfaces:**
- Consumes: `paused`, `resumeDeliveries` (Task 2); `GetDeliveredDelivery` (Task 1).
- Produces: no new names. Two behaviors: a `delivery` log row is skipped while its room is closed, and resuming a room writes a fresh `delivery` event for a connected agent's delivery that is still `delivered`.

Why: a `delivery` row stays in the log. An agent that reconnects during a pause would otherwise be handed it again, by cursor replay or by the no-cursor re-hand in `Subscribe`. Skipping it moves the cursor past the row, so resume has to hand it over again or the pair's queue is stuck behind a `delivered` copy nobody holds.

- [ ] **Step 1: Write the failing tests**

```go
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
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./cmd/hermes-chat/internal/bus/ -run 'TestPause_|TestResume_'`
Expected: FAIL — `TestPause_ReconnectingIsHandedNothing` finds a delivery event; `TestResume_HandsOverAgainWhatWasHeldBack` finds none.

- [ ] **Step 3: Skip `delivery` rows of a closed room in `Subscription.event`**

In the `EventDelivery` case, once the room row is loaded, return `Event{}, false, nil` when `paused(ctx, r, roomRow)` is true. This covers both the `again` rows and cursor replay, because both go through `event`.

- [ ] **Step 4: Hand over again on resume, in `resumeDeliveries`**

For each agent in the room that has a live stream (`s.hub.agentConnected`): if `r.GetDeliveredDelivery(ctx, roomID, agentID)` finds one, write `putAgentEvent(ctx, r, EventDelivery, roomID, d.MessageID, agent.ID, at)` — no state change and no `delivery.changed`; otherwise call `releaseHead` as before. An agent with no live stream is skipped here: `Subscribe` hands it over when it returns. Do nothing at all when `paused(...)` is still true for the room (Task 4 resumes a room while everything is stopped).

- [ ] **Step 5: Run the tests**

Run: `just check`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add cmd/hermes-chat/internal/bus
git commit -m "fix(bus): hand no delivery to a reconnecting agent in a paused room, and hand it over on resume"
```

---

### Task 4: Stop everything

**Files:**
- Create: `cmd/hermes-chat/internal/bus/halt.go`, `halt_test.go`
- Modify: `bus/bus.go`, `pause.go` (`paused`), `room.go` (`buildRoomLayout`)

**Interfaces:**
- Consumes: `GetHaltedAtMs`, `SetHaltedAtMs`, `ListAllRoomAgents` (Task 1); `paused`, `resumeDeliveries` (Tasks 2–3).
- Produces, on `Service`:

```go
StopEverything(ctx context.Context, actor Actor) error   // human only: ErrForbidden; already stopped is a no-op
ResumeEverything(ctx context.Context, actor Actor) error // human only: ErrForbidden; not stopped is a no-op
Halted(ctx context.Context) (bool, error)
```

`Room.Halted` is true while everything is stopped.

- [ ] **Step 1: Write the failing tests in `halt_test.go`**

```go
func TestHalt_HoldsEveryRoomAndLiftRestoresEachRoomsOwnState(t *testing.T) {
	w := newWorld(t)
	dm, conv := dmRoom(t, w), convRoom(t, w)
	sub := agentSub(t, w, w.scout)
	_, err := w.s.PauseRoom(ctx, w.kai, conv.ID) // paused before the halt
	require.NoError(t, err)
	drain(t, sub)
	human, err := w.s.Subscribe(ctx, w.kai, bus.FromNow)
	require.NoError(t, err)
	defer human.Close()

	require.NoError(t, w.s.StopEverything(ctx, w.kai))
	halted, err := w.s.Halted(ctx)
	require.NoError(t, err)
	assert.True(t, halted)
	assert.Len(t, of(drain(t, sub), bus.EventControlPause), 2, "one pause per room the agent is in")
	changed := of(drain(t, human), bus.EventRoomChanged)
	require.Len(t, changed, 2, "one room.changed per room")
	assert.True(t, changed[0].Room.Halted)
	got, _ := w.s.GetRoom(ctx, w.kai, dm.ID)
	assert.True(t, got.Halted)
	assert.False(t, got.Paused(), "the halt is not the room's own pause")

	inDM, _ := w.s.PostMessage(ctx, w.kai, dm.ID, "held", ulid.Zero)
	inConv, _ := w.s.PostMessage(ctx, w.kai, conv.ID, "held too", ulid.Zero)
	assert.Equal(t, bus.DeliveryWaiting, inDM.Deliveries[0].State)
	_, err = w.s.PostMessage(ctx, agentActor(w.scout), dm.ID, "unprompted", ulid.Zero)
	assert.ErrorIs(t, err, bus.ErrPaused)
	assert.Empty(t, of(drain(t, sub), bus.EventDelivery))

	require.NoError(t, w.s.ResumeEverything(ctx, w.kai))
	assert.Equal(t, bus.DeliveryDelivered, stateOf(t, w, dm, inDM.ID, w.scout))
	assert.Equal(t, bus.DeliveryWaiting, stateOf(t, w, conv, inConv.ID, w.scout), "a room paused before the halt is still paused")
	got, _ = w.s.GetRoom(ctx, w.kai, conv.ID)
	assert.True(t, got.Paused())
	assert.False(t, got.Halted)
}

func TestHalt_DoubleTapsAndCrossedSwitches(t *testing.T) {
	w := newWorld(t)
	room := dmRoom(t, w)
	agentSub(t, w, w.scout)
	human, err := w.s.Subscribe(ctx, w.kai, bus.FromNow)
	require.NoError(t, err)
	defer human.Close()
	require.NoError(t, w.s.ResumeEverything(ctx, w.kai)) // not stopped
	assert.Empty(t, drain(t, human))
	require.NoError(t, w.s.StopEverything(ctx, w.kai))
	drain(t, human)
	require.NoError(t, w.s.StopEverything(ctx, w.kai))
	assert.Empty(t, drain(t, human), "a second stop writes nothing")

	// pausing and resuming one room while everything is stopped changes only that room
	m, _ := w.s.PostMessage(ctx, w.kai, room.ID, "held", ulid.Zero)
	_, err = w.s.PauseRoom(ctx, w.kai, room.ID)
	require.NoError(t, err)
	got, err := w.s.ResumeRoom(ctx, w.kai, room.ID)
	require.NoError(t, err)
	assert.False(t, got.Paused())
	assert.True(t, got.Halted)
	assert.Equal(t, bus.DeliveryWaiting, stateOf(t, w, room, m.ID, w.scout), "resuming a room does not lift the halt")

	// a room made while everything is stopped is stopped too
	made := convRoom(t, w)
	assert.True(t, made.Halted)
	held, _ := w.s.PostMessage(ctx, w.kai, made.ID, "held", ulid.Zero)
	assert.Equal(t, bus.DeliveryWaiting, held.Deliveries[0].State)

	assert.ErrorIs(t, w.s.StopEverything(ctx, agentActor(w.scout)), bus.ErrForbidden)
	assert.ErrorIs(t, w.s.ResumeEverything(ctx, agentActor(w.scout)), bus.ErrForbidden)
}

func TestHalt_SurvivesARestart(t *testing.T) {
	w := newWorld(t)
	room := dmRoom(t, w)
	m, _ := w.s.PostMessage(ctx, w.kai, room.ID, "held", ulid.Zero)
	require.NoError(t, w.s.StopEverything(ctx, w.kai))

	restarted := bus.NewService(w.r, func() time.Time { return fixed }) // a new process over the same database
	halted, err := restarted.Halted(ctx)
	require.NoError(t, err)
	assert.True(t, halted)
	sub, err := restarted.Subscribe(ctx, agentActor(w.scout), bus.FromNow)
	require.NoError(t, err)
	defer sub.Close()
	assert.Empty(t, of(drain(t, sub), bus.EventDelivery), "the sweep on connect releases nothing")
	got, _ := restarted.GetMessage(ctx, w.kai, room.ID, m.ID)
	assert.Equal(t, bus.DeliveryWaiting, got.Deliveries[0].State)
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./cmd/hermes-chat/internal/bus/ -run TestHalt`
Expected: FAIL to compile (`w.s.StopEverything undefined`).

- [ ] **Step 3: Implement `halt.go` and widen the gate**

`paused` now also returns true when `r.GetHaltedAtMs` is not nil. `buildRoomLayout` sets `Room.Halted` from the same read. `Halted` reads it outside a transaction.

`StopEverything` and `ResumeEverything` run in `s.record`, refuse a non-human with `ErrForbidden`, and return nil without writing when the halt already has the wanted state. Both walk `r.ListAllRoomAgents`: every room has at least one agent, so its distinct room ids are every room.
- `StopEverything`: `SetHaltedAtMs(&at)`, one `room.changed` per room, one `pause` agent event per (room, agent).
- `ResumeEverything`: `SetHaltedAtMs(nil)`, one `room.changed` per room, then `resumeDeliveries` per room (it leaves a room that is still paused, or archived, alone).

- [ ] **Step 4: Run the tests**

Run: `just check`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/hermes-chat/internal/bus
git commit -m "feat(bus): stop everything, kept apart from each room's own pause"
```

---

### Task 5: The turn cap

**Files:**
- Create: `cmd/hermes-chat/internal/bus/turncap.go`, `turncap_test.go`
- Modify: `bus/delivery.go` (`releaseHead`), `message.go` (`PostMessage`), `turn.go` (`Steer`), `room.go` (`RoomUpdate`, `UpdateRoom`)

**Interfaces:**
- Consumes: `pauseRoom` (Task 2); `SetRoomAgentTurns`, `SetRoomTurnCap` (Task 1).
- Produces:

```go
// turncap.go
const (
	DefaultTurnCap = 6 // the migration's column default; kept here for the README and tests
	MaxTurnCap     = 100
)
// countAgentTurn is called by releaseHead for a head about to be released. It reports false, having
// paused the room for PauseTurnCap, when the head's message was written by an agent and the room
// has already had its cap of agent-woken turns; otherwise it counts the turn (agent-written
// messages only) and reports true.
func (s *service) countAgentTurn(ctx context.Context, r storage.Repository, room storage.Room, head storage.Delivery, at time.Time) (release bool, err error)

// room.go
type RoomUpdate struct {
	Topic    *string
	Archived *bool
	TurnCap  *int // 1 to MaxTurnCap
}
```

- [ ] **Step 1: Write the failing tests in `turncap_test.go`**

```go
// ping has one agent wake another, and finishes the turn if it was released.
func ping(t *testing.T, w world, room bus.Room, from, to bus.Agent) bus.Message {
	t.Helper()
	m, err := w.s.PostMessage(ctx, agentActor(from), room.ID, "@"+to.Handle+" your turn", ulid.Zero)
	require.NoError(t, err)
	if m.Deliveries[0].State == bus.DeliveryDelivered {
		require.NoError(t, w.s.TurnDone(ctx, agentActor(to), m.ID))
	}
	return m
}

// connectedChannel is kai's channel with scout and atlas, both connected.
func connectedChannel(t *testing.T, w world) (bus.Room, *bus.Subscription, *bus.Subscription) {
	t.Helper()
	room := channelRoom(t, w)
	return room, agentSub(t, w, w.scout), agentSub(t, w, w.atlas)
}

func TestTurnCap_PausesTheRoomAndSaysWhy(t *testing.T) {
	w := newWorld(t)
	room, scout, atlas := connectedChannel(t, w)
	human, err := w.s.Subscribe(ctx, w.kai, bus.FromNow)
	require.NoError(t, err)
	defer human.Close()
	for i := 0; i < bus.DefaultTurnCap; i++ {
		assert.Equal(t, bus.DeliveryDelivered, ping(t, w, room, w.scout, w.atlas).Deliveries[0].State, "turn %d", i+1)
	}
	drain(t, scout)
	drain(t, atlas)
	drain(t, human)

	seventh := ping(t, w, room, w.scout, w.atlas) // the post itself succeeds
	assert.Equal(t, bus.DeliveryWaiting, seventh.Deliveries[0].State)
	got, _ := w.s.GetRoom(ctx, w.kai, room.ID)
	assert.True(t, got.Paused())
	assert.Equal(t, bus.PauseTurnCap, got.PauseReason)
	changed := of(drain(t, human), bus.EventRoomChanged)
	require.Len(t, changed, 1)
	assert.Equal(t, bus.PauseTurnCap, changed[0].Room.PauseReason)
	assert.Len(t, of(drain(t, scout), bus.EventControlPause), 1)
	atlasEvs := drain(t, atlas)
	assert.Len(t, of(atlasEvs, bus.EventControlPause), 1)
	assert.Empty(t, of(atlasEvs, bus.EventDelivery))
	_, err = w.s.PostMessage(ctx, agentActor(w.atlas), room.ID, "@scout again", ulid.Zero)
	assert.ErrorIs(t, err, bus.ErrPaused)

	// a human resume is a human in the loop: the held turn goes out as turn 1 of a new count
	_, err = w.s.ResumeRoom(ctx, w.kai, room.ID)
	require.NoError(t, err)
	assert.Equal(t, bus.DeliveryDelivered, stateOf(t, w, room, seventh.ID, w.atlas))
	require.NoError(t, w.s.TurnDone(ctx, agentActor(w.atlas), seventh.ID))
	for i := 1; i < bus.DefaultTurnCap; i++ {
		assert.Equal(t, bus.DeliveryDelivered, ping(t, w, room, w.scout, w.atlas).Deliveries[0].State)
	}
	assert.Equal(t, bus.DeliveryWaiting, ping(t, w, room, w.scout, w.atlas).Deliveries[0].State)
}

func TestTurnCap_AHumanMessageStartsTheCountAgain(t *testing.T) {
	w := newWorld(t)
	room, _, _ := connectedChannel(t, w)
	for i := 0; i < bus.DefaultTurnCap-1; i++ {
		ping(t, w, room, w.scout, w.atlas)
	}
	_, err := w.s.PostMessage(ctx, w.kai, room.ID, "carry on", ulid.Zero) // wakes nobody; still a human message
	require.NoError(t, err)
	for i := 0; i < bus.DefaultTurnCap; i++ {
		assert.Equal(t, bus.DeliveryDelivered, ping(t, w, room, w.scout, w.atlas).Deliveries[0].State)
	}
	assert.Equal(t, bus.DeliveryWaiting, ping(t, w, room, w.scout, w.atlas).Deliveries[0].State)
}

func TestTurnCap_HumanWokenAndUnpromptedTurnsDoNotCount(t *testing.T) {
	w := newWorld(t)
	room, _, _ := connectedChannel(t, w)
	for i := 0; i < 2*bus.DefaultTurnCap; i++ {
		m, err := w.s.PostMessage(ctx, w.kai, room.ID, "@scout go", ulid.Zero)
		require.NoError(t, err)
		require.Equal(t, bus.DeliveryDelivered, m.Deliveries[0].State)
		require.NoError(t, w.s.TurnDone(ctx, agentActor(w.scout), m.ID))
	}
	dm := dmRoom(t, w)
	for i := 0; i < 2*bus.DefaultTurnCap; i++ { // a week of scheduled reports
		_, err := w.s.PostMessage(ctx, agentActor(w.scout), dm.ID, "daily report", ulid.Zero)
		require.NoError(t, err)
	}
	for _, id := range []ulid.ULID{room.ID, dm.ID} {
		got, _ := w.s.GetRoom(ctx, w.kai, id)
		assert.False(t, got.Paused())
	}
}

func TestTurnCap_OneMessageWakingSeveralStopsAtTheCap(t *testing.T) {
	w := newWorld(t)
	echo, _, err := w.s.CreateAgent(ctx, "echo", "Echo")
	require.NoError(t, err)
	room, err := w.s.CreateRoom(ctx, w.kai, "Trio", ids(w.atlas, echo, w.scout))
	require.NoError(t, err)
	for _, a := range []bus.Agent{w.atlas, echo, w.scout} {
		agentSub(t, w, a)
	}
	room, err = w.s.UpdateRoom(ctx, w.kai, room.ID, bus.RoomUpdate{TurnCap: ptr(1)})
	require.NoError(t, err)
	assert.Equal(t, 1, room.TurnCap)

	m, err := w.s.PostMessage(ctx, agentActor(w.scout), room.ID, "@atlas @echo both of you", ulid.Zero)
	require.NoError(t, err)
	require.Len(t, m.Deliveries, 2) // handle order: atlas, echo
	assert.Equal(t, bus.DeliveryDelivered, m.Deliveries[0].State)
	assert.Equal(t, bus.DeliveryWaiting, m.Deliveries[1].State, "the second wake is over the cap and waits")
	got, _ := w.s.GetRoom(ctx, w.kai, room.ID)
	assert.Equal(t, bus.PauseTurnCap, got.PauseReason)
}

func TestUpdateRoom_TurnCap(t *testing.T) {
	w := newWorld(t)
	room := convRoom(t, w)
	for _, bad := range []int{0, -1, bus.MaxTurnCap + 1} {
		_, err := w.s.UpdateRoom(ctx, w.kai, room.ID, bus.RoomUpdate{TurnCap: ptr(bad)})
		var ve bus.ValidationError
		require.ErrorAs(t, err, &ve, "%d", bad)
		assert.Equal(t, "turn_cap must be 1 to 100", ve.Message)
	}
	for _, ok := range []int{1, bus.MaxTurnCap} {
		got, err := w.s.UpdateRoom(ctx, w.kai, room.ID, bus.RoomUpdate{TurnCap: ptr(ok)})
		require.NoError(t, err)
		assert.Equal(t, ok, got.TurnCap)
	}
	before := len(w.kinds(t, w.kai))
	_, err := w.s.UpdateRoom(ctx, w.kai, room.ID, bus.RoomUpdate{TurnCap: ptr(bus.MaxTurnCap)})
	require.NoError(t, err)
	assert.Len(t, w.kinds(t, w.kai), before, "an unchanged cap writes no event")
	_, err = w.s.UpdateRoom(ctx, w.kai, dmRoom(t, w).ID, bus.RoomUpdate{TurnCap: ptr(3)})
	assert.ErrorIs(t, err, bus.ErrValidation) // a DM cannot be changed
	_, err = w.s.UpdateRoom(ctx, w.kai, room.ID, bus.RoomUpdate{Archived: ptr(true), TurnCap: ptr(3)})
	assert.ErrorIs(t, err, bus.ErrRoomArchived)
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./cmd/hermes-chat/internal/bus/ -run 'TestTurnCap|TestUpdateRoom_TurnCap'`
Expected: FAIL to compile (`bus.DefaultTurnCap undefined`, unknown field `TurnCap`).

- [ ] **Step 3: Implement `countAgentTurn` and call it from `releaseHead`**

`releaseHead` calls it after `HeadWaiting` finds a head and before `SetDeliveryState`; when it reports false, `releaseHead` returns nil. `countAgentTurn` loads the head's message with `r.GetMessage`. A message with no agent author is not counted and releases. For an agent-authored one: if `room.AgentTurns >= room.TurnCap`, call `s.pauseRoom(ctx, r, room, PauseTurnCap, at)` and report false; otherwise `SetRoomAgentTurns(ctx, room.ID, room.AgentTurns+1)` and report true. The single write connection makes read-then-set safe inside the transaction.

- [ ] **Step 4: Reset the count on a human message**

In `PostMessage` (human author) and in `Steer`, when the room's `AgentTurns` is not 0, `SetRoomAgentTurns(ctx, row.ID, 0)`. In `PostMessage` do it before the `releaseHead` loop. (`ResumeRoom` already resets it, from Task 2.)

- [ ] **Step 5: Add `TurnCap` to `UpdateRoom`**

Applied after `Topic`, with the same archived rule as `Topic` (`ErrRoomArchived` when the room is, or has just become, archived). Out of range is `ValidationError{Message: "turn_cap must be 1 to 100"}`; equal to the current cap changes nothing; otherwise `SetRoomTurnCap` and report a change. Update the `RoomUpdate` comment: "Archived is applied first, then Topic, then TurnCap."

- [ ] **Step 6: Run the tests**

Run: `just check`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add cmd/hermes-chat/internal/bus
git commit -m "feat(bus): the turn cap pauses a room after its cap of agent-woken turns"
```

---

### Task 6: The HTTP API

**Files:**
- Create: `cmd/hermes-chat/internal/app/pause.go`, `pause_test.go`
- Modify: `app/app.go`, `json.go`, `errors.go`, `room.go`, `stream.go`, `app/auth_test.go` (`apiEngine`), `app/errors_test.go`, `cmd/hermes-chat/routes.go`, `routes_test.go`, `README.md`

**Interfaces:**
- Consumes: `PauseRoom`, `ResumeRoom`, `StopEverything`, `ResumeEverything`, `Halted`, `RoomUpdate.TurnCap`, `ErrPaused`, `EventControlPause`, and the `Room` fields.
- Produces: `Controller` methods `PauseRoom`, `ResumeRoom`, `GetHalt`, `StopEverything`, `ResumeEverything`, and these routes in the human group:

| Route | Replies |
|---|---|
| `POST /api/rooms/:room/pause` | 200, the room |
| `POST /api/rooms/:room/resume` | 200, the room |
| `GET /api/halt` | 200 `{"halted":false}` |
| `POST /api/halt` | 200 `{"halted":true}` |
| `DELETE /api/halt` | 200 `{"halted":false}` |

JSON: the room object gains `"paused":false,"pause_reason":null,"turn_cap":6,"halted":false` (`pause_reason` is `"human"` or `"turn_cap"` while paused); the agent room gains `"paused":false`, true when the room is paused or everything is stopped. `PATCH /api/rooms/:room` accepts `"turn_cap"`. The agent stream gains `pause` with data `{"room_id":"01…"}`. Error row: `{bus.ErrPaused, http.StatusConflict, "paused", "the room is paused or everything is stopped"}`.

- [ ] **Step 1: Write the failing tests**

Add the five routes to `apiEngine` in `auth_test.go` and to the expected list in `routes_test.go`'s `TestRoutes_Table` (`"DELETE /api/halt"`, `"GET /api/halt"`, `"POST /api/halt"`, `"POST /api/rooms/:room/pause"`, `"POST /api/rooms/:room/resume"`, in sorted position). Add `bus.ErrPaused` → 409, `paused`, the message above, to `TestWriteError_Table`.

`pause_test.go`:

```go
const pausedBody = `{"error":{"code":"paused","message":"the room is paused or everything is stopped"}}`

func TestPauseAndResumeRoutes(t *testing.T) {
	c := newChat(t)
	w := c.call(c.kai, "POST", "/api/rooms", fmt.Sprintf(`{"topic":"Trip","agent_ids":[%q]}`, c.scout.ID.String()))
	require.Equal(t, 201, w.Code)
	room := obj(t, w)
	assert.Equal(t, false, room["paused"])
	assert.Nil(t, room["pause_reason"])
	assert.EqualValues(t, 6, room["turn_cap"])
	assert.Equal(t, false, room["halted"])
	base := "/api/rooms/" + room["id"].(string)

	w = c.call(c.kai, "POST", base+"/pause", "")
	require.Equal(t, 200, w.Code)
	assert.Equal(t, true, obj(t, w)["paused"])
	assert.Equal(t, "human", obj(t, c.call(c.kai, "GET", base, ""))["pause_reason"])
	assert.Equal(t, 200, c.call(c.kai, "POST", base+"/pause", "").Code) // a second tap is fine

	w = c.call(c.kai, "POST", base+"/resume", "")
	require.Equal(t, 200, w.Code)
	resumed := obj(t, w)
	assert.Equal(t, false, resumed["paused"])
	assert.Nil(t, resumed["pause_reason"])

	assert.Equal(t, 404, c.call(c.ada, "POST", base+"/pause", "").Code) // not a member
	assert.Equal(t, 404, c.call(c.kai, "POST", "/api/rooms/not-an-id/pause", "").Code)
	assert.Equal(t, 401, c.call("", "POST", base+"/pause", "").Code)
}

func TestTurnCapInPatch(t *testing.T) {
	c := newChat(t)
	room := obj(t, c.call(c.kai, "POST", "/api/rooms", fmt.Sprintf(`{"topic":"Trip","agent_ids":[%q]}`, c.scout.ID.String())))
	base := "/api/rooms/" + room["id"].(string)
	w := c.call(c.kai, "PATCH", base, `{"turn_cap":3}`)
	require.Equal(t, 200, w.Code)
	assert.EqualValues(t, 3, obj(t, w)["turn_cap"])
	w = c.call(c.kai, "PATCH", base, `{"turn_cap":0}`)
	assert.Equal(t, 400, w.Code)
	assert.JSONEq(t, `{"error":{"code":"validation","message":"turn_cap must be 1 to 100"}}`, w.Body.String())
	w = c.call(c.kai, "PATCH", base, `{"topic":"Renamed"}`) // an absent turn_cap is left alone
	assert.EqualValues(t, 3, obj(t, w)["turn_cap"])
	assert.Equal(t, 400, c.call(c.kai, "PATCH", base, `{"turn_cap":"six"}`).Code)
}

func TestHaltRoutes(t *testing.T) {
	c := newChat(t)
	room := obj(t, c.call(c.kai, "POST", "/api/rooms", fmt.Sprintf(`{"topic":"Trip","agent_ids":[%q]}`, c.scout.ID.String())))
	w := c.call(c.ada, "GET", "/api/halt", "") // ada is in no room and can still see and use it
	require.Equal(t, 200, w.Code)
	assert.JSONEq(t, `{"halted":false}`, w.Body.String())
	w = c.call(c.ada, "POST", "/api/halt", "")
	require.Equal(t, 200, w.Code)
	assert.JSONEq(t, `{"halted":true}`, w.Body.String())
	assert.JSONEq(t, `{"halted":true}`, c.call(c.kai, "GET", "/api/halt", "").Body.String())
	got := obj(t, c.call(c.kai, "GET", "/api/rooms/"+room["id"].(string), ""))
	assert.Equal(t, true, got["halted"])
	assert.Equal(t, false, got["paused"])
	w = c.call(c.kai, "DELETE", "/api/halt", "")
	require.Equal(t, 200, w.Code)
	assert.JSONEq(t, `{"halted":false}`, w.Body.String())
	assert.Equal(t, 401, c.call("", "POST", "/api/halt", "").Code)
}
```

In `delivery_test.go` (the agent side; `agentRig` has kai's DM with scout as `r.room`):

```go
func TestAgentRoutes_PausedRoom(t *testing.T) {
	r := newAgentRig(t)
	kai := bus.Actor{Kind: bus.ActorHuman, ID: r.kai.ID}
	r.connect(t, r.scout.ID)
	m := r.kaiPost(t, r.room, "go", ulid.Zero)
	_, err := r.svc.PauseRoom(ctx, kai, r.room.ID)
	require.NoError(t, err)

	w := agentReq(r.e, r.scoutT, "POST", "/agent/rooms/"+r.room.ID.String()+"/messages", `{"text":"hi"}`)
	assert.Equal(t, 409, w.Code)
	assert.JSONEq(t, pausedBody, w.Body.String())
	w = agentReq(r.e, r.scoutT, "POST", "/agent/deliveries/"+m.ID.String()+"/remarks", `{"text":"hm"}`)
	assert.Equal(t, 409, w.Code)
	assert.Equal(t, 204, agentReq(r.e, r.scoutT, "POST", "/agent/deliveries/"+m.ID.String()+"/done", "").Code)

	w = agentReq(r.e, r.scoutT, "GET", "/agent/rooms", "")
	var body struct{ Rooms []map[string]any }
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Len(t, body.Rooms, 1)
	assert.Equal(t, true, body.Rooms[0]["paused"])
}
```

In `stream_test.go` (the `rig` has kai's conversation with scout as `r.room`):

```go
func TestAgentEvents_PauseFrame(t *testing.T) {
	r := newRig(t, app.Options{}, nil)
	_, d := r.openAgent(t, r.scoutToken, "")
	require.Equal(t, "ready", next(t, d).Event)
	_, err := r.svc.PauseRoom(ctx, r.kai, r.room.ID)
	require.NoError(t, err)
	f := next(t, d)
	assert.Equal(t, "pause", f.Event)
	assert.JSONEq(t, fmt.Sprintf(`{"room_id":%q}`, r.room.ID.String()), f.Data)
	assert.NotEmpty(t, f.ID)
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./cmd/hermes-chat/...`
Expected: FAIL to compile (`ctrl.PauseRoom undefined`).

- [ ] **Step 3: Implement the handlers, JSON, error row, and frame**

Existing tests that compare a whole room object (`room_test.go`, `stream_test.go`, `delivery_test.go` in `app`) need the new fields added to their expected JSON.

- `pause.go`: `PauseRoom` and `ResumeRoom` use `roomAction`. `GetHalt` answers `{"halted":<bool>}` from `svc.Halted`; `StopEverything` and `ResumeEverything` call the bus and then answer the same body from `svc.Halted`.
- `json.go`: `roomBody` gains `Paused bool`, `PauseReason *string`, `TurnCap int`, `Halted bool` with the JSON names above; `agentRoomBody` gains `Paused bool`, set to `r.Paused() || r.Halted`.
- `room.go`: `UpdateRoom`'s body gains `TurnCap *int \`json:"turn_cap"\``, passed through.
- `errors.go`: the `ErrPaused` row. `app.go`: the five `Controller` methods.
- `stream.go`: `agentEventFrame` handles `EventControlPause` like `EventControlStop`.
- `routes.go`: the five routes in the human group, the room ones beside `stop`/`steer`.

- [ ] **Step 4: Run the tests**

Run: `just check`
Expected: PASS.

- [ ] **Step 5: Document it in `README.md`**

- Routes table: the five routes, and `"turn_cap":6` in the PATCH row ("1 to 100; not on a DM").
- Error table: `409 | paused | The room is paused or everything is stopped; an agent cannot post, edit, remark, or start a tool, and a human cannot steer.`
- Shapes: the four room fields and the agent room's `paused`, with a sentence each.
- Agent stream table: `pause | {"room_id":"01…"}` — "stop any turn in this room; nothing more will be delivered there until a human resumes it. Report the stopped turn `done` or `failed` as usual."
- A new section, "The off switch", after "Delivery and turns", stating in this order: what pausing a room does on the server (no release, agent writes refused, `pause` sent) and that the first two do not depend on the agent; what stays allowed (human posts queue, cancel, stop, turn reports, finishing a tool); stop everything and that lifting it leaves each room's own pause as it was; the turn cap rule in the words of decisions 3 and 4 above, with the default 6; and that an agent reconnecting during a pause is handed nothing for that room until it resumes.
- Known limits: "A message an agent was streaming when its room was paused stays unfinalized, with the text of its last stored edit." and "Stop everything writes one `room.changed` per room and one `pause` per agent per room."

- [ ] **Step 6: Commit**

```bash
git add cmd/hermes-chat README.md
git commit -m "feat(app): pause, resume, stop everything, and the turn cap over HTTP"
```

---

### Task 7: End to end with the fake agent

**Files:**
- Create: `cmd/hermes-chat/offswitch_e2e_test.go`
- Modify: `AGENTS.md`

**Interfaces:**
- Consumes: the routes of Task 6; from `agent_e2e_test.go`: `newAgentWorld`, `openDM`, `post`, `openAgent`, `openAgentFor`, `nextAgent`, `expectNothing`, `deliveryStates`, `agentID`; from `serve_test.go`: `apiCall`, `runCmd`, `tokenLine`; `fakeagent.Client`.
- Produces: nothing later tasks use.

- [ ] **Step 1: Write the tests**

```go
// frameOf reads agent frames until one has the wanted event, skipping the others.
func frameOf(t *testing.T, s *fakeagent.Stream, event string) sse.Frame {
	t.Helper()
	for {
		if f := nextAgent(t, s); f.Event == event {
			return f
		}
	}
}

// The requirement of the stage: the pause holds against an agent that ignores it.
func TestOffSwitchE2E_PauseHoldsAgainstAnAgentThatIgnoresIt(t *testing.T) {
	w := newAgentWorld(t)
	room := openDM(t, w, w.scoutID)
	scout, stream := w.agent(), openAgent(t, w)
	bg := context.Background()
	m1 := post(t, w, room, "start")
	frameOf(t, stream, "delivery")
	require.NoError(t, scout.TurnWorking(bg, m1))
	reply, err := scout.PostMessage(bg, room, "thinking", "")
	require.NoError(t, err)

	code, body := apiCall(t, w.human, "POST", w.api, "/api/rooms/"+room+"/pause", "")
	require.Equal(t, 200, code)
	assert.Equal(t, true, body["paused"])
	assert.JSONEq(t, fmt.Sprintf(`{"room_id":%q}`, room), frameOf(t, stream, "pause").Data)

	// the agent carries on regardless
	_, err = scout.PostMessage(bg, room, "still talking", "")
	assert.ErrorContains(t, err, "409 paused")
	assert.ErrorContains(t, scout.EditMessage(bg, reply, "still typing", true), "409 paused")
	assert.ErrorContains(t, scout.Remark(bg, m1, "still working"), "409 paused")
	m2 := post(t, w, room, "queued behind the pause")
	require.NoError(t, scout.TurnDone(bg, m1))
	expectNothing(t, stream, 300*time.Millisecond)
	assert.Equal(t, "waiting", deliveryStates(t, w, room)[m2])
	_, list := apiCall(t, w.human, "GET", w.api, "/api/rooms/"+room+"/messages", "")
	assert.Len(t, list["messages"].([]any), 3, "nothing from the agent got in after the pause")
}
```

The stream closed by `expectNothing` is spent, so resume is checked in the next test, on a fresh one.

```go
func TestOffSwitchE2E_StopEverythingAndLift(t *testing.T) {
	w := newAgentWorld(t)
	dm := openDM(t, w, w.scoutID)
	code, conv := apiCall(t, w.human, "POST", w.api, "/api/rooms", fmt.Sprintf(`{"topic":"Trip","agent_ids":[%q]}`, w.scoutID))
	require.Equal(t, 201, code)
	convID := conv["id"].(string)
	code, _ = apiCall(t, w.human, "POST", w.api, "/api/rooms/"+convID+"/pause", "") // paused on its own first
	require.Equal(t, 200, code)

	code, halt := apiCall(t, w.human, "POST", w.api, "/api/halt", "")
	require.Equal(t, 200, code)
	assert.Equal(t, true, halt["halted"])
	inDM, inConv := post(t, w, dm, "held"), post(t, w, convID, "held too")
	stream := openAgent(t, w) // connecting while everything is stopped releases nothing
	assert.Equal(t, "ready", nextAgent(t, stream).Event)
	assert.Equal(t, "waiting", deliveryStates(t, w, dm)[inDM])
	_, rooms := apiCall(t, w.human, "GET", w.api, "/api/rooms", "")
	for _, r := range rooms["rooms"].([]any) {
		assert.Equal(t, true, r.(map[string]any)["halted"])
	}

	code, halt = apiCall(t, w.human, "DELETE", w.api, "/api/halt", "")
	require.Equal(t, 200, code)
	assert.Equal(t, false, halt["halted"])
	f := frameOf(t, stream, "delivery")
	assert.Contains(t, f.Data, inDM)
	assert.Equal(t, "waiting", deliveryStates(t, w, convID)[inConv], "the room paused before the halt is still paused")

	code, _ = apiCall(t, w.human, "POST", w.api, "/api/rooms/"+convID+"/resume", "")
	require.Equal(t, 200, code)
	assert.Contains(t, frameOf(t, stream, "delivery").Data, inConv)
}

func TestOffSwitchE2E_TurnCapStopsAPingPong(t *testing.T) {
	w := newAgentWorld(t)
	_, out, _ := runCmd(t, "agent", "add", "atlas", "Atlas") // the CLI may run beside the server
	atlasToken := tokenLine.FindStringSubmatch(out)[1]
	atlasID := agentID(t, w, "atlas")
	code, room := apiCall(t, w.human, "POST", w.api, "/api/rooms", fmt.Sprintf(`{"topic":"Debate","agent_ids":[%q,%q]}`, w.scoutID, atlasID))
	require.Equal(t, 201, code)
	roomID := room["id"].(string)
	code, _ = apiCall(t, w.human, "PATCH", w.api, "/api/rooms/"+roomID, `{"turn_cap":2}`)
	require.Equal(t, 200, code)

	bg := context.Background()
	scout, atlas := w.agent(), &fakeagent.Client{BaseURL: w.api, Token: atlasToken}
	scoutStream, atlasStream := openAgent(t, w), openAgentFor(t, w.api, atlasToken)

	// kai starts it; from then on each agent answers by mentioning the other
	kicked := post(t, w, roomID, "@scout debate @atlas")
	frameOf(t, scoutStream, "delivery")
	frameOf(t, atlasStream, "delivery")
	require.NoError(t, atlas.TurnDone(bg, kicked))
	require.NoError(t, scout.TurnDone(bg, kicked))
	one, err := scout.PostMessage(bg, roomID, "@atlas one", "") // agent-woken turn 1
	require.NoError(t, err)
	assert.Contains(t, frameOf(t, atlasStream, "delivery").Data, one)
	require.NoError(t, atlas.TurnDone(bg, one)) // each turn ends before the next, so nothing queues behind it
	two, err := atlas.PostMessage(bg, roomID, "@scout two", "") // agent-woken turn 2
	require.NoError(t, err)
	assert.Contains(t, frameOf(t, scoutStream, "delivery").Data, two)
	require.NoError(t, scout.TurnDone(bg, two))
	three, err := scout.PostMessage(bg, roomID, "@atlas three", "") // would be turn 3: accepted, not delivered
	require.NoError(t, err)

	assert.JSONEq(t, fmt.Sprintf(`{"room_id":%q}`, roomID), frameOf(t, scoutStream, "pause").Data)
	frameOf(t, atlasStream, "pause")
	_, got := apiCall(t, w.human, "GET", w.api, "/api/rooms/"+roomID, "")
	assert.Equal(t, true, got["paused"])
	assert.Equal(t, "turn_cap", got["pause_reason"])
	_, err = atlas.PostMessage(bg, roomID, "@scout four", "")
	assert.ErrorContains(t, err, "409 paused")
	_, list := apiCall(t, w.human, "GET", w.api, "/api/rooms/"+roomID+"/messages", "")
	for _, m := range list["messages"].([]any) {
		if mm := m.(map[string]any); mm["id"] == three {
			assert.Equal(t, "waiting", mm["deliveries"].([]any)[0].(map[string]any)["state"])
		}
	}
}
```

- [ ] **Step 2: Run them**

Run: `go test ./cmd/hermes-chat/ -run TestOffSwitchE2E -v`
Expected: PASS for all three. These exercise code already written, so a failure here is a defect in Tasks 2–6: fix it in the layer that owns it and add the bus or app test that would have caught it.

- [ ] **Step 3: Update `AGENTS.md`**

- Project Overview: "stages 1–5 of the architecture plan are implemented".
- `internal/bus` bullet: add `pause.go`, `halt.go`, `turncap.go` ("the off switch: room pause, stop everything, the turn cap") to its file list.
- Queries row of Key Directories: add `halt` (and `activities`, `deliveries`, which the row is missing).
- Code Conventions, a new bullet: "**The off switch** is one gate: `releaseHead` (`bus/delivery.go`) re-reads the room and the halt inside the transaction and releases nothing while `paused()` is true, and agent writes check the same function. A new way to deliver to or accept content from an agent must go through one of the two."

- [ ] **Step 4: Run everything**

Run: `just check`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/hermes-chat/offswitch_e2e_test.go AGENTS.md
git commit -m "test: the off switch end to end with the fake agent"
```
