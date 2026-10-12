# Web UI Slice 2: Live Room List Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** After login the page shows the person's rooms in two collapsible, alphabetical groups with unread dots, badges and generated icons, kept current by the event stream, with stop everything in the header and a form to create a room or open a DM. Rooms do not open yet.

**Architecture:** A fetch-based reader of `GET /api/events` (`lib/stream.ts`) feeds one in-memory store (`lib/store.svelte.ts`); every time the stream reaches `ready` the store reloads rooms, agents and the halt state, and events then patch it. Screens only read the store. Ordering and unread rules are pure functions (`lib/rooms.ts`), as is the icon (`lib/icon.ts`). A hash route picks between the room list and the new-room form.

**Tech Stack:** Svelte 5, TypeScript 6, Vite 8; Vitest 5 with `@testing-library/svelte` on jsdom. No new npm dependency. No Go change.

**Spec:** `docs/superpowers/specs/2026-10-12-web-ui-design.md` (slice 2 of section 8; sections 4.2, 4.5 "New room", 4.7, 4.8, 5 and the "Checked against the server" notes apply). The wire shapes are in `docs/openapi.yaml`; the stream's behaviour is in the README's "Event stream" section. Mockup: `.superpowers/brainstorm/webui-2026-10-12/content/icons-v6.html`, screen 1.

## Global Constraints

- No change to the JSON API or to any Go file. If one seems needed, stop and raise it with Kai.
- The page is served under `default-src 'self'`: no inline `<script>`, no `style="…"` attribute, no `{@html}`. SVG `fill` and `opacity` attributes are allowed and are how icons are coloured.
- No request to another origin, and no new npm dependency. Existing dependencies stay pinned; TypeScript stays on 6.x.
- No screen fetches on a timer. The only timers are the stream's reconnect delay and its silence watchdog.
- Nothing about read state is stored in the browser. The only thing in `localStorage` is which groups are collapsed.
- A room never changes position because something was posted in it. Only its name decides its place.
- No hand-holding copy: no helper text, no empty-state coaching, no tooltips, no confirmation dialogs.
- One header per screen and nothing else fixed.
- Routes are hash-based: `#/` is the room list, `#/new` the form. Anything else shows the room list.
- Colours come from the mockup and are not final; add tokens to `web/src/app.css`, never literals in components (the icon palette in `lib/icon.ts` is the exception).
- Run `just web-check` before each commit. Commit messages follow the repository's style (`feat(web): …`).

## Decisions this plan makes

| Question | Decision |
|---|---|
| `EventSource` or `fetch` | `fetch` with a small frame parser. `EventSource` hides the status code (a 401 must sign the person out; a 502 during an upgrade closes it for good) and hides the `: hb` heartbeat, which is the only way to notice a connection that died silently on a phone. |
| Noticing a dead stream | 60 seconds with no bytes (the server sends something every 25) aborts the connection and reconnects. |
| Reconnect delay | 1, 2, 4, 8, 16, then 30 seconds; back to 1 after a `ready`. Returning to the page reconnects at once. |
| Resuming | `?cursor=<id of the last frame received>`, the `ready` frame included. |
| What is loaded, and when | Rooms, agents and halt, on every `ready`, first or not, and when the page becomes visible again (spec section 9: no event announces a read marker). Server events are rendered from current state when sent, so applying them in order after a reload always converges. `/api/me` is already loaded by the session before the stream opens; it cannot change. |
| Sort order | `Intl.Collator(undefined, { sensitivity: 'accent', numeric: true })` on the row's name, ties broken by room ID. Case is ignored; "Run 2" sorts before "Run 10". |
| Collapsible groups | A header `<button aria-expanded>` and a conditional list, not `<details>`, so the state is one variable and testable. Stored as `localStorage["hermes-chat.collapsed"] = {"dms":bool,"rooms":bool}`. |
| Icon hash and palette | FNV-1a over code points with a murmur3 finalizer, and the 16 colours, both exactly as in the mockup's script. Input is lowercased. |
| Search icon in the header | Not shown until slice 6. No dead controls. |
| Log out | Moves from the header to a quiet line under the list: the username and **Log out**. |
| Stop everything | One tap, no confirmation (spec section 2). Label `Stop all`; while stopped the header shows `Stopped` and `Resume all`. |
| The form | One screen at `#/new`: a button per agent opens its DM; below, a topic field, a checkbox per agent, and **Create**. Revoked agents are not offered. On success the person is back on the list, where the room now is. |
| Unread in this slice | Dots and bold names are shown from the server's markers, but nothing in this slice moves a marker; that lands with slice 3. |

## Review Focus

1. **The phone comes back from the background, or the network drops.** Expected: the list is current again within a moment of returning, without a reload, and a `reconnecting` label shows while the stream is down. Tests in Tasks 2 and 4.
2. **Events replayed after a reconnect** (the server resends at least once). Expected: applying an event twice changes nothing; an older `message.posted` never moves `last_message_id` backwards. Test in Task 4.
3. **An event for a room the store does not hold** (posted before the first load finishes, or malformed data in a frame). Expected: ignored, no throw, the stream keeps running. Tests in Tasks 2 and 4.
4. **A double tap** on `Stop all`, on **Create**, or on an agent's DM button. Expected: one request; the control is disabled while it is pending. Tests in Tasks 7 and 8.
5. **Awkward names.** A 200-character topic, two topics that differ only in case, a non-ASCII or empty icon name, and `localStorage` that throws (private browsing). Expected: one truncated line with the badges still visible; a stable order; an icon, not an exception; groups that still collapse. Tests in Tasks 3, 5 and 6.

## File Structure

```
web/src/
  app.css                        + tokens --unread --ask --on-ask --ok; #app column
  lib/types.ts                   wire shapes: Human, Agent, RoomAgent, Room, Delivery, Message (the fields this slice reads)
  lib/api.ts                     + listRooms, listAgents, getHalt, stopEverything, resumeEverything, createRoom, openDM, sessionLost
  lib/stream.ts                  frameParser, openStream
  lib/rooms.ts                   roomName, isUnread, isWorking, groupRooms, byName
  lib/fixtures.ts                room() and agent() builders for tests
  lib/store.svelte.ts            store; connect, disconnect, load, apply, putRoom, stopAll, resumeAll
  lib/icon.ts                    iconFor
  lib/collapsed.ts               loadCollapsed, saveCollapsed
  lib/route.svelte.ts            route, startRouting, navigate
  Icon.svelte                    the SVG
  RoomRow.svelte                 one row
  RoomList.svelte                two groups
  HaltControl.svelte             Stop all / Stopped, Resume all
  NewRoom.svelte                 the form
  Header.svelte                  + title, back
  Home.svelte                    the signed-in shell: starts the store, picks the screen
  (a *.test.ts beside each)
README.md, AGENTS.md
```

---

### Task 1: Wire shapes and the API calls

**Files:**
- Create: `web/src/lib/types.ts`
- Modify: `web/src/lib/api.ts`, `web/src/lib/api.test.ts`

**Interfaces:**
- Produces, from `lib/types.ts` (field names are the wire's, copied from `docs/openapi.yaml`):

```ts
export type Human = { id: string; username: string };
export type Agent = { id: string; handle: string; display_name: string; revoked: boolean };
export type RoomAgent = Agent & { connected: boolean; working: boolean };
export type Flavor = 'dm' | 'channel' | 'conversation';
export type Room = {
  id: string; flavor: Flavor; topic: string; archived: boolean; created_at: string;
  humans: Human[]; agents: RoomAgent[];
  last_message_id: string | null; last_read_message_id: string | null;
  paused: boolean; pause_reason: 'human' | 'turn_cap' | null; turn_cap: number;
  halted: boolean; open_prompts: number;
};
export type DeliveryState = 'waiting' | 'delivered' | 'working' | 'done' | 'failed' | 'cancelled';
export type Delivery = { agent: Agent; state: DeliveryState };
// Only what slice 2 reads; slice 3 completes it.
export type Message = { id: string; room_id: string; deliveries: Delivery[] };
```

- Produces, from `lib/api.ts` (`Me` becomes an alias of `Human`):

```ts
export function listRooms(): Promise<Room[]>            // GET /api/rooms, unwraps {rooms}
export function listAgents(): Promise<Agent[]>          // GET /api/agents, unwraps {agents}
export function getHalt(): Promise<boolean>             // GET /api/halt, unwraps {halted}
export function stopEverything(): Promise<boolean>      // POST /api/halt
export function resumeEverything(): Promise<boolean>    // DELETE /api/halt
export function createRoom(topic: string, agentIds: string[]): Promise<Room>  // POST /api/rooms {topic, agent_ids}
export function openDM(agentId: string): Promise<Room>  // POST /api/dms {agent_id}; 200 and 201 alike
export function sessionLost(): void                     // runs the onUnauthenticated handler; for the stream, which does not use request()
```

- [ ] **Step 1: Write the failing tests** in `api.test.ts`, in the file's existing style (stubbed `fetch`). One per function: the method, path and body sent, and the unwrapped value returned. For example:

```ts
test('createRoom posts the topic and agent ids', async () => {
  const fetch = stubFetch(201, { id: '01R', topic: 'Trip' });
  await expect(createRoom('Trip', ['01A', '01B'])).resolves.toMatchObject({ id: '01R' });
  expect(fetch).toHaveBeenCalledWith('/api/rooms', expect.objectContaining({
    method: 'POST', body: '{"topic":"Trip","agent_ids":["01A","01B"]}',
  }));
});

test('sessionLost runs the unauthenticated handler', () => {
  const handler = vi.fn();
  onUnauthenticated(handler);
  sessionLost();
  expect(handler).toHaveBeenCalledTimes(1);
});
```

- [ ] **Step 2: Run.** `cd web && npx vitest run src/lib/api.test.ts` — FAIL, the functions are not exported.
- [ ] **Step 3: Implement** the types file and the eight functions, each one line over `request`.
- [ ] **Step 4: Run.** `just web-check` — PASS.
- [ ] **Step 5: Commit.** `feat(web): the room, agent and halt calls`

---

### Task 2: The event stream reader

**Files:**
- Create: `web/src/lib/stream.ts`, `web/src/lib/stream.test.ts`

**Interfaces:**
- Consumes: `sessionLost()` from Task 1.
- Produces:

```ts
export type Frame = { id: string; event: string; data: string };
// Returns a function to feed decoded text to, in chunks cut anywhere.
export function frameParser(onFrame: (f: Frame) => void): (text: string) => void;

export type StreamState = 'connecting' | 'live' | 'waiting';
export type StreamHandlers = {
  ready: () => void;                            // each time a connection receives `ready`
  event: (name: string, data: unknown) => void; // every other frame, data parsed as JSON
  state: (s: StreamState) => void;
};
export type Stream = { close(): void; kick(): void };
export function openStream(handlers: StreamHandlers): Stream;
```

Behaviour:

- `frameParser` follows the SSE format as `internal/sse` writes it: lines end in `\n` (accept `\r\n` too); `field: value` with one optional space after the colon; several `data` lines join with `\n`; a blank line ends the frame; a line starting with `:` is a comment and produces nothing; a frame with no `event` and no `data` (a comment alone) is not emitted.
- `openStream` fetches `/api/events` (`Accept: text/event-stream`), or `/api/events?cursor=N` once any frame with an `id` has arrived; `N` is always the latest such `id`, assigned, not maximised (after a database rollback `ready` may name a lower cursor). It decodes with `TextDecoder` in streaming mode.
- State is `connecting` from the request until `ready`, `live` after it, `waiting` during a reconnect delay.
- A frame whose data is not JSON, or whose event name is unknown to the handler, never ends the stream; bad JSON is dropped here.
- Status 401: call `sessionLost()` and stop for good. Status 400: forget the cursor, then retry as any other failure. Any other failure (non-200, a fetch rejection, the body ending, 60 000 ms with no bytes): go to `waiting` and retry after the next delay of `[1000, 2000, 4000, 8000, 16000, 30000]` ms, staying on the last; `ready` resets the sequence.
- `kick()` reconnects immediately when `waiting` and does nothing otherwise. `close()` aborts the request, cancels timers, and no handler runs afterwards.

- [ ] **Step 1: Write the failing tests.** For `openStream`, stub `fetch` to return a `Response` whose body is a `ReadableStream` the test pushes text into, and use `vi.useFakeTimers()`.

```ts
test('a frame cut across chunks arrives once, whole', () => {
  const frames: Frame[] = [];
  const feed = frameParser((f) => frames.push(f));
  feed('id: 7\nevent: room.cha');
  feed('nged\ndata: {"room":\ndata: 1}\n');
  expect(frames).toEqual([]);
  feed('\n');
  expect(frames).toEqual([{ id: '7', event: 'room.changed', data: '{"room":\n1}' }]);
});

test('a heartbeat comment emits nothing', () => {
  const frames: Frame[] = [];
  frameParser((f) => frames.push(f))(': hb\n\n');
  expect(frames).toEqual([]);
});
```

  And, by name, each asserting what the Behaviour list says:
  - `CRLF line endings parse the same`
  - `ready calls the handler and the state goes connecting, live`
  - `an event's data arrives parsed` — `event` called with `('room.changed', { room: { id: '01R' } })`
  - `a frame with bad JSON is dropped and the next one still arrives`
  - `a body that ends reconnects after 1s with the last id as cursor` — second `fetch` URL is `/api/events?cursor=12`
  - `the delays grow to 30s and reset after ready` — failures at 1, 2, 4, 8, 16, 30, 30 s; after a `ready` and a drop, 1 s again
  - `60s of silence reconnects; a heartbeat every 25s does not`
  - `a 401 calls sessionLost and never retries` — advance timers by 5 minutes, `fetch` called once
  - `a 400 retries without the cursor`
  - `a 502 retries`
  - `kick reconnects at once while waiting, and does nothing while live`
  - `close aborts, and a frame pushed afterwards reaches no handler`

- [ ] **Step 2: Run.** `npx vitest run src/lib/stream.test.ts` — FAIL, module not found.
- [ ] **Step 3: Implement** `frameParser` and `openStream`. One `AbortController` per connection serves both `close()` and the watchdog; the watchdog timer is reset on every chunk read.
- [ ] **Step 4: Run.** `just web-check` — PASS.
- [ ] **Step 5: Commit.** `feat(web): an event stream reader that reconnects from its cursor`

---

### Task 3: Ordering and unread rules

**Files:**
- Create: `web/src/lib/rooms.ts`, `web/src/lib/rooms.test.ts`

**Interfaces:**
- Consumes: `Room` from Task 1.
- Produces:

```ts
export function roomName(room: Room): string;   // dm: its first agent's display_name ('' if none); otherwise the topic
export function isUnread(room: Room): boolean;  // not archived, and last_message_id !== last_read_message_id
export function isWorking(room: Room): boolean; // any agent has working: true
export type Groups = { dms: Room[]; rooms: Room[] };
export function groupRooms(rooms: Room[]): Groups; // archived dropped; dm → dms, channel and conversation → rooms; each sorted
export function byName(a: string, b: string): number; // the collator's compare; Task 8 sorts agents with it
```

- [ ] **Step 1: Write the failing tests.** Add a `room(overrides: Partial<Room>): Room` fixture in `web/src/lib/fixtures.ts` (also `agent(overrides)`), reused by later tasks.

```ts
test('rooms sort by topic ignoring case, conversations and channels together', () => {
  const g = groupRooms([
    room({ id: '3', topic: 'trip planning', flavor: 'channel' }),
    room({ id: '1', topic: 'Garden' }),
    room({ id: '2', topic: 'heat pump' }),
  ]);
  expect(g.rooms.map((r) => r.topic)).toEqual(['Garden', 'heat pump', 'trip planning']);
});

test('a newer message does not move a room', () => {
  const a = room({ id: '1', topic: 'A', last_message_id: '01A' });
  const b = room({ id: '2', topic: 'B', last_message_id: '01Z' });
  expect(groupRooms([b, a]).rooms.map((r) => r.id)).toEqual(['1', '2']);
});

test('unread is the two markers differing', () => {
  expect(isUnread(room({ last_message_id: null, last_read_message_id: null }))).toBe(false);
  expect(isUnread(room({ last_message_id: '01B', last_read_message_id: null }))).toBe(true);
  expect(isUnread(room({ last_message_id: '01B', last_read_message_id: '01A' }))).toBe(true);
  expect(isUnread(room({ last_message_id: '01B', last_read_message_id: '01B' }))).toBe(false);
  expect(isUnread(room({ archived: true, last_message_id: '01B', last_read_message_id: null }))).toBe(false);
});
```

  And by name:
  - `DMs sort by the agent's display name, not the handle or topic`
  - `archived rooms are in neither group`
  - `names that differ only in case keep one order whatever the input order` — `groupRooms([x, y])` and `groupRooms([y, x])` give the same ID order (the ID tie-break)
  - `numbers in names sort by value` — `Run 2` before `Run 10`
  - `a DM with no agents is named '' and does not throw`
  - `isWorking is true when any agent is working`

- [ ] **Step 2: Run.** FAIL, module not found.
- [ ] **Step 3: Implement** with the collator from the Decisions table, built once at module level.
- [ ] **Step 4: Run.** `just web-check` — PASS.
- [ ] **Step 5: Commit.** `feat(web): room list order and the unread rule`

---

### Task 4: The store

**Files:**
- Create: `web/src/lib/store.svelte.ts`, `web/src/lib/store.test.ts`

**Interfaces:**
- Consumes: `openStream`, `StreamState` (Task 2); `listRooms`, `listAgents`, `getHalt`, `stopEverything`, `resumeEverything`, `ApiError` (Task 1).
- Produces:

```ts
type Store = {
  link: StreamState;              // the stream's state
  loaded: boolean;                // a load has succeeded since connect()
  error: string;                  // the last failed load, stop or resume; '' when none
  rooms: Record<string, Room>;    // by id, archived ones included
  agents: Record<string, Agent>;  // the directory, by id
  halted: boolean;
};
export const store: Store;        // $state
export function connect(): void;      // open the stream, listen for visibilitychange; a second call is a no-op
export function disconnect(): void;   // close the stream, stop listening, reset store to its initial value
export function load(): Promise<void>;            // never rejects
export function apply(name: string, data: unknown): void;
export function putRoom(room: Room): void;        // insert or replace
export function stopAll(): Promise<void>;         // never rejects
export function resumeAll(): Promise<void>;       // never rejects
```

Behaviour:

- `connect()` wires the stream: `ready` → `load()`, `event` → `apply`, `state` → `store.link`. On `visibilitychange` with `document.visibilityState === 'visible'`: `kick()` the stream, and `load()` if `store.link === 'live'` (otherwise the coming `ready` loads).
- `load()` fetches rooms, agents and halt together and replaces all three, so a room the person has left disappears. Only the newest call may write: a load that resolves after a later one started is discarded. On failure it sets `store.error` to the error's message, except for a 401, which the API layer has already handled. Success clears `store.error` and sets `loaded`.
- `apply`:

| Event | Effect |
|---|---|
| `room.changed` | `putRoom(data.room)`; `store.halted = data.room.halted` |
| `message.posted` | in the room `data.message.room_id`, if held: set `last_message_id` to the message's ID when the current one is `null` or sorts lower as a string (ULIDs sort by time) |
| `delivery.changed` | in that room, for each of `data.message.deliveries`: the room agent with that agent's ID gets `working = true` for state `working`, `false` for `done` or `failed`; other states leave it alone |
| `agent.presence` | `store.agents[data.agent.id] = data.agent`; in every room, the agent with that ID gets `connected = data.connected` |
| anything else, a room not held, or data of the wrong shape | nothing, and no throw |

- `stopAll` and `resumeAll` set `store.halted` from the response; on failure they set `store.error` and leave `halted` as it was.

- [ ] **Step 1: Write the failing tests.** Mock `./api` (keeping `ApiError`) and `./stream` (capture the handlers passed to `openStream`; return `{ close: vi.fn(), kick: vi.fn() }`). Call `disconnect()` in `beforeEach`.

```ts
test('ready loads rooms, agents and halt', async () => {
  vi.mocked(api.listRooms).mockResolvedValue([room({ id: '01R' })]);
  vi.mocked(api.listAgents).mockResolvedValue([agent({ id: '01A' })]);
  vi.mocked(api.getHalt).mockResolvedValue(true);
  connect();
  handlers.ready();
  await vi.waitFor(() => expect(store.loaded).toBe(true));
  expect(Object.keys(store.rooms)).toEqual(['01R']);
  expect(store.agents['01A']).toBeDefined();
  expect(store.halted).toBe(true);
});

test('a replayed older message does not move last_message_id back', () => {
  putRoom(room({ id: '01R', last_message_id: '01M5' }));
  apply('message.posted', { message: { id: '01M3', room_id: '01R', deliveries: [] } });
  expect(store.rooms['01R'].last_message_id).toBe('01M5');
  apply('message.posted', { message: { id: '01M7', room_id: '01R', deliveries: [] } });
  apply('message.posted', { message: { id: '01M7', room_id: '01R', deliveries: [] } });
  expect(store.rooms['01R'].last_message_id).toBe('01M7');
});
```

  And by name:
  - `room.changed replaces the room and carries the halt` — a new ID is inserted; a held ID is replaced whole; `halted` follows the room's
  - `delivery.changed sets and clears working` — `working` → true; `done` → false; `failed` → false; `waiting`, `delivered`, `cancelled` leave it
  - `agent.presence reaches the agent in every room it is in, and the directory`
  - `an event for a room that is not held does nothing` — `message.posted` and `delivery.changed`; no throw
  - `malformed data does nothing` — `apply('room.changed', null)`, `apply('message.posted', {})`, `apply('nope', 1)`
  - `a load replaces the rooms, dropping one that is gone`
  - `a slow load that finishes after a newer one is discarded` — resolve the first call's promises last; the store holds the second's rooms
  - `a failed load sets error and leaves the rooms; the next success clears it`
  - `a 401 during load sets no error`
  - `becoming visible kicks the stream and reloads when live` — set `store.link = 'live'` through the state handler, stub `document.visibilityState`, dispatch `visibilitychange`
  - `becoming visible while not live kicks and does not load`
  - `stopAll sets halted from the response; a failure sets error and leaves halted`
  - `disconnect closes the stream, resets the store, and stops listening`
  - `connect twice opens one stream`

- [ ] **Step 2: Run.** FAIL, module not found.
- [ ] **Step 3: Implement.** A module-level counter gives each `load()` its sequence number.
- [ ] **Step 4: Run.** `just web-check` — PASS.
- [ ] **Step 5: Commit.** `feat(web): one store, loaded on ready and kept current by events`

---

### Task 5: Generated icons

**Files:**
- Create: `web/src/lib/icon.ts`, `web/src/lib/icon.test.ts`, `web/src/Icon.svelte`, `web/src/Icon.test.ts`

**Interfaces:**
- Produces:

```ts
export const palette: readonly string[];  // the 16 colours, in the mockup's order
export type IconSpec = { color: string; cells: [x: number, y: number][] };  // x and y in 0..4
export function iconFor(name: string): IconSpec;
```

  and `Icon.svelte` with one prop, `name: string`. It renders `<svg viewBox="0 0 7 7" shape-rendering="crispEdges" aria-hidden="true">`: a white 7×7 `rect`, the same `rect` in the colour at `opacity="0.15"`, then one 1×1 `rect` per cell at `(x + 1, y + 1)` filled with the colour. Size comes from the parent's CSS; the component sets `display: block; width: 100%; height: 100%`.

The algorithm, which the tests alone do not determine:

```ts
const palette = ['#c0392b', '#d35400', '#b9770e', '#7d8c0a', '#2e8b3d', '#0e8f6e', '#0b8a9a', '#1f78c1',
  '#3555c9', '#6a45c9', '#9b3fb5', '#c2338b', '#c93560', '#8a5a3c', '#5c6b7a', '#3d3d46'];

function hash(s: string): number {
  let x = 2166136261;
  for (const c of s) { x ^= c.codePointAt(0)!; x = Math.imul(x, 16777619); }
  x ^= x >>> 16; x = Math.imul(x, 0x85ebca6b);
  x ^= x >>> 13; x = Math.imul(x, 0xc2b2ae35);
  x ^= x >>> 16;
  return x >>> 0;
}
// h = hash(name.toLowerCase()); color = palette[(h >>> 15) % 16];
// for y in 0..4, x in 0..2: bit (y * 3 + x) of h set → cell [x, y], and its mirror [4 - x, y] when x < 2.
```

- [ ] **Step 1: Write the failing tests.**

```ts
test('forge', () => {
  const icon = iconFor('forge');
  expect(icon.color).toBe('#c93560');
  expect(icon.cells).toEqual([[1, 0], [3, 0], [2, 0], [0, 2], [4, 2], [2, 2], [0, 4], [4, 4], [1, 4], [3, 4]]);
});

test('case does not change the icon', () => {
  expect(iconFor('Forge')).toEqual(iconFor('forge'));
});

test('kai, a non-ASCII name and an empty name each give an icon', () => {
  expect(iconFor('kai').color).toBe('#c2338b');
  expect(iconFor('é').color).toBe('#3d3d46');
  expect(iconFor('').color).toBe('#c93560');
});

test('every icon is mirrored left to right', () => {
  for (const name of ['atlas', 'scout', 'quill', 'ledger']) {
    const cells = iconFor(name).cells.map(([x, y]) => `${x},${y}`);
    for (const [x, y] of iconFor(name).cells) expect(cells).toContain(`${4 - x},${y}`);
  }
});
```

  `Icon.test.ts`: `renders one rect per cell plus two backgrounds` (for `forge`, 12 `rect` elements), and `has no style attribute anywhere` (`container.querySelector('[style]')` is null).

- [ ] **Step 2: Run.** FAIL, module not found.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run.** `just web-check` — PASS.
- [ ] **Step 5: Commit.** `feat(web): icons generated from a name`

---

### Task 6: The room list screen

**Files:**
- Create: `web/src/lib/collapsed.ts`, `web/src/lib/collapsed.test.ts`, `web/src/lib/route.svelte.ts`, `web/src/lib/route.test.ts`, `web/src/RoomRow.svelte`, `web/src/RoomRow.test.ts`, `web/src/RoomList.svelte`, `web/src/RoomList.test.ts`
- Modify: `web/src/Home.svelte`, `web/src/Home.test.ts`, `web/src/Header.svelte`, `web/src/app.css`

**Interfaces:**
- Consumes: `store`, `connect`, `disconnect`, `load` (Task 4); `groupRooms`, `roomName`, `isUnread`, `isWorking` (Task 3); `Icon.svelte` (Task 5).
- Produces:

```ts
// lib/collapsed.ts
export type Collapsed = { dms: boolean; rooms: boolean };
export function loadCollapsed(): Collapsed;        // {dms:false, rooms:false} when absent, unreadable, or storage throws
export function saveCollapsed(c: Collapsed): void; // swallows a storage error

// lib/route.svelte.ts
export const route: { path: string };              // $state; location.hash without '#', '/' when empty
export function startRouting(): () => void;        // reads the hash, listens for hashchange; returns the stop function
export function navigate(path: string): void;      // sets location.hash
```

  - `Header.svelte` props: `title?: string` (default `'Hermes Chat'`), `back?: string` (an href; when set, a link `‹` with `aria-label="Back"` before the title), and `children` as now.
  - `RoomRow.svelte` prop: `room: Room`. `RoomList.svelte` takes no props and reads the store.
  - `Home.svelte` is the signed-in shell later tasks add to: Task 7 puts `HaltControl` in both headers, Task 8 puts `NewRoom` in the `#/new` branch.

Markup the tests and later slices rely on:

- **RoomRow** is an `<li>`. A DM shows `Icon` (name: the first agent's `handle`) with a presence dot labelled `aria-label="connected"` or `"offline"`, then the agent's display name. Any other room shows `#` and the topic. When unread: class `unread` on the `<li>` (the name is bold) and a dot with `aria-label="unread"`. Badges, in this order, each only when it applies: the open prompt count with `aria-label="N open prompts"`; the text `working`; the text `paused`. The name is one line, truncated with an ellipsis (`min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap`), and badges never shrink (`flex: none`).
- **RoomList** is a `<nav>` on the aubergine surface with two sections, **Direct messages** then **Rooms**, each a `<button aria-expanded>` header and, when expanded, a `<ul>` of rows. A collapsed section whose rooms include an unread one shows an `aria-label="unread"` dot in its header; an expanded one does not. Toggling saves through `saveCollapsed`.
- **Home**: `onMount` calls `connect()` and `startRouting()`, and its cleanup calls `disconnect()` and the stop function. On the list route: `<Header>` holding a link `+` to `#/new` with `aria-label="New room"`; then, when `store.error` is set, `<p role="alert">` with the message and a **Retry** button calling `load()`; then `RoomList` once `store.loaded`; then a footer line with the username and a **Log out** button. Nothing is rendered in place of the list before the first load. When `store.loaded` and `store.link !== 'live'`, the header shows the text `reconnecting`.
- **app.css**: add `--unread: #ff8fb8; --ask: #f0b34a; --on-ask: #3b2600; --ok: #3fbf85;` to `:root` (the same in dark, since they sit on aubergine in both), and `#app { max-width: 32rem; margin-inline: auto; min-height: 100dvh; }` for the single centred column.

- [ ] **Step 1: Write the failing tests.** Component tests mock `./lib/store.svelte` with a plain object and `vi.fn()` actions, as `Home.test.ts` already does for the session.

```ts
test('an unread room has a dot and a prompt count', () => {
  render(RoomRow, { room: room({ topic: 'Trip', last_message_id: '01B', open_prompts: 2 }) });
  expect(screen.getByRole('listitem')).toHaveClass('unread');
  expect(screen.getByLabelText('unread')).toBeInTheDocument();
  expect(screen.getByLabelText('2 open prompts')).toHaveTextContent('2');
});

test('a collapsed group with an unread room shows the dot on its header', async () => {
  store.rooms = { '1': room({ id: '1', topic: 'Trip', last_message_id: '01B' }) };
  render(RoomList);
  const header = screen.getByRole('button', { name: /Rooms/ });
  expect(within(header).queryByLabelText('unread')).toBeNull();
  await userEvent.click(header);
  expect(header).toHaveAttribute('aria-expanded', 'false');
  expect(screen.queryByText('Trip')).toBeNull();
  expect(within(header).getByLabelText('unread')).toBeInTheDocument();
  expect(saveCollapsed).toHaveBeenCalledWith({ dms: false, rooms: true });
});
```

  And by name:
  - RoomRow: `a read room has no dot and no badges`; `a DM shows the agent's name, an icon, and connected or offline`; `working and paused badges`; `a room with no open prompts has no count`; `a 200-character topic renders in one element with the badges after it`
  - RoomList: `Direct messages comes before Rooms`; `rows are in groupRooms order`; `an archived room is not listed`; `starts collapsed as loadCollapsed says`; `an empty group shows its header and no rows`
  - collapsed: `round trip through localStorage`; `garbage in storage reads as both expanded`; `getItem throwing reads as both expanded`; `setItem throwing does not throw`
  - route: `an empty hash is '/'`; `a hashchange updates the path`; `the stop function stops listening`
  - Home: `connects on mount and disconnects on unmount`; `shows nothing in place of the list until loaded`; `shows the list and the username once loaded`; `an error shows with Retry, which loads`; `reconnecting shows when loaded and not live, and not before the first load`; `Log out calls signOut`; `the + link goes to #/new`

- [ ] **Step 2: Run.** FAIL, modules not found.
- [ ] **Step 3: Implement.** Sizes from the mockup: icon 22px square with a 5px radius; presence and unread dots 8px; rows `padding: 7px 14px`; group headers small, uppercase, in `--on-aub2`. All of it in component `<style>` blocks.
- [ ] **Step 4: Run.** `just web-check` — PASS.
- [ ] **Step 5: Commit.** `feat(web): the room list, in two groups that remember being collapsed`

---

### Task 7: Stop everything

**Files:**
- Create: `web/src/HaltControl.svelte`, `web/src/HaltControl.test.ts`
- Modify: `web/src/Home.svelte`, `web/src/Home.test.ts`

**Interfaces:**
- Consumes: `store.halted`, `stopAll()`, `resumeAll()` (Task 4); `Home.svelte`'s headers (Task 6).
- Produces: `HaltControl.svelte`, no props. Not stopped: a button `Stop all`. Stopped: the text `Stopped` and a button `Resume all`. The button is disabled from the tap until the action's promise settles. Failures surface through `store.error`, which `Home` already shows.

- [ ] **Step 1: Write the failing tests.**

```ts
test('a second tap while stopping sends nothing', async () => {
  let settle!: () => void;
  vi.mocked(stopAll).mockReturnValue(new Promise((r) => (settle = r)));
  render(HaltControl);
  const button = screen.getByRole('button', { name: 'Stop all' });
  await userEvent.click(button);
  expect(button).toBeDisabled();
  await userEvent.click(button);
  expect(stopAll).toHaveBeenCalledTimes(1);
  settle();
  await vi.waitFor(() => expect(button).toBeEnabled());
});
```

  And by name: `while stopped it says Stopped and offers Resume all, which calls resumeAll`; `no dialog is opened` (`window.confirm` spied, never called); in `Home.test.ts`, `the halt control is in the header on the list and on #/new`.

- [ ] **Step 2: Run.** FAIL.
- [ ] **Step 3: Implement,** and render `<HaltControl />` last in `Home`'s header on every route. The stop button uses the mockup's outline in `--err`.
- [ ] **Step 4: Run.** `just web-check` — PASS.
- [ ] **Step 5: Commit.** `feat(web): stop everything, and lifting it, from the header`

---

### Task 8: New room and open a DM

**Files:**
- Create: `web/src/NewRoom.svelte`, `web/src/NewRoom.test.ts`
- Modify: `web/src/Home.svelte`, `web/src/Home.test.ts`

**Interfaces:**
- Consumes: `store.agents`, `putRoom` (Task 4); `createRoom`, `openDM`, `ApiError` (Task 1); `navigate` (Task 6).
- Produces: `NewRoom.svelte`, no props; `Home` renders it at `#/new` under `<Header title="New room" back="#/">`.

Behaviour:

- Agents offered are `store.agents` with `revoked: false`, sorted by display name with the same collator rule as Task 3 (export the comparator from `lib/rooms.ts` as `byName(a: string, b: string): number` and use it in both places).
- Section **Direct message**: one button per agent, showing `Icon` and the display name. A tap calls `openDM(agent.id)`.
- Section **Room**: a text field labelled `Topic` (`maxlength="200"`), a checkbox per agent labelled with its display name, and a submit button `Create`. Submitting calls `createRoom(topic.trim(), ids)` with the checked IDs in the displayed order. `Create` is disabled while the trimmed topic is empty or no agent is checked.
- On either success: `putRoom(room)`, then `navigate('/')`.
- While either request is pending every button on the screen is disabled. A failure shows the error's message in a `<p role="alert">` and leaves the form as it was.

- [ ] **Step 1: Write the failing tests.**

```ts
test('Create posts the trimmed topic and the checked agents, then returns to the list', async () => {
  store.agents = { '01A': agent({ id: '01A', display_name: 'Atlas' }), '01F': agent({ id: '01F', display_name: 'Forge' }) };
  const made = room({ id: '01R', topic: 'Trip' });
  vi.mocked(createRoom).mockResolvedValue(made);
  render(NewRoom);
  await userEvent.type(screen.getByLabelText('Topic'), '  Trip ');
  await userEvent.click(screen.getByRole('checkbox', { name: 'Forge' }));
  await userEvent.click(screen.getByRole('button', { name: 'Create' }));
  expect(createRoom).toHaveBeenCalledWith('Trip', ['01F']);
  expect(putRoom).toHaveBeenCalledWith(made);
  expect(navigate).toHaveBeenCalledWith('/');
});
```

  And by name:
  - `Create is disabled without a topic, with a blank topic, and with no agent checked`
  - `an agent's DM button opens its DM and returns to the list`
  - `a revoked agent is offered in neither section`
  - `agents are listed by display name`
  - `the server's validation message is shown and the form keeps its values` — reject with `new ApiError(400, 'validation', 'topic must be 1-200 characters with no control characters')`; the alert has that text, the field still holds the topic, `navigate` not called
  - `every button is disabled while a request is pending, and a second tap sends nothing`
  - `with no agents there are no buttons but Create, which is disabled`
  - in `Home.test.ts`: `#/new shows the form under a header with a back link to #/`

- [ ] **Step 2: Run.** FAIL.
- [ ] **Step 3: Implement.** The form sits on `--paper`, not on aubergine: it is read and typed into.
- [ ] **Step 4: Run.** `just web-check` — PASS.
- [ ] **Step 5: Commit.** `feat(web): create a room or open a DM`

---

### Task 9: Documents, and a run against the real server

**Files:**
- Modify: `README.md` ("Web UI" section), `AGENTS.md` ("Web UI" convention bullet)

- [ ] **Step 1: README.** In "Web UI", replace the description of what the page shows after login with what it shows now: the room list, kept current by the event stream; stop everything; the new-room form; rooms do not open yet. State that the page reads `GET /api/events` with `fetch` and resumes with `?cursor=`.
- [ ] **Step 2: AGENTS.md.** Extend the "Web UI" bullet: one store (`web/src/lib/store.svelte.ts`) is fed by the stream (`web/src/lib/stream.ts`) and reloaded on every `ready`; screens read the store and none fetches on a timer; routes are hash-based (`web/src/lib/route.svelte.ts`).
- [ ] **Step 3: Run the gate.** `just check` — PASS. `just build` — produces `bin/hermes-chat-linux-amd64`.
- [ ] **Step 4: Try it by hand.** Start a server on a temporary database with `HERMES_CHAT_TLS_MODE=off` (README "Commands": `migrate`, add a user, add two agents), run `just web-dev`, and connect one agent with `cmd/fakeagent` (README "Fake agent"). Confirm each, and report any that fails instead of marking the step done:
  - The list appears after login with no console error and no Content-Security-Policy report.
  - Opening a DM and creating a room from `#/new` both land back on the list with the new row in its alphabetical place.
  - The fake agent's DM shows `connected`; killing the fake agent turns it `offline` without a reload.
  - `curl` a post into a room as the human (README "HTTP API"): the row turns bold with a dot and does not move.
  - `Stop all` shows `Stopped` on the list and on `#/new`; `Resume all` lifts it. A second browser window follows both without a reload.
  - Restart the server: `reconnecting` appears, then clears, and the list is intact.
  - Collapse **Rooms**, reload: it is still collapsed, with the dot on its header.
  - At a 360px-wide viewport a long topic truncates and its badges stay visible.
- [ ] **Step 5: Commit.** `docs: the web UI's room list, store and stream`
