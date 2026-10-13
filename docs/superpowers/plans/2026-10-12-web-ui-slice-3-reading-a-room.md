# Web UI Slice 3: Reading a Room Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A room opens from the list and shows its messages: rendered markdown, replies that stream in place, each agent's delivery state, reply quotes, attachments, read-only prompt cards and a collapsible activity block per turn. Reading moves the server's read marker, so the Unread row, the Unread page and mark all read work. Nothing can be posted or acted on yet.

**Architecture:** The store gains the messages of opened rooms (`feeds`), the newest message per room (`latest`) and per-message activity, all fed by the same event stream. Every fetch whose answer is a snapshot of server state now holds incoming events until the answer is written, then applies them in order; slice 2 did this for `load()` only. Markdown is lexed to tokens by `marked`, reduced to a small allowlisted tree, and rendered by Svelte components, so no HTML string is ever produced and there is nothing to sanitize but link targets. Scroll position, paging and the read marker are driven by two `IntersectionObserver` sentinels at the feed's ends.

**Tech Stack:** Svelte 5, TypeScript 6, Vite 8; Vitest 5 with `@testing-library/svelte` on jsdom. One new npm dependency: `marked` 18.0.14 (no dependencies of its own). No Go change.

**Spec:** `docs/superpowers/specs/2026-10-12-web-ui-design.md` (slice 3 of section 8; sections 3, 4.3, 4.4, 4.7, 5 and the "Checked against the server" notes apply). Wire shapes are in `docs/openapi.yaml`; stream behaviour, live edits and `streaming` are in the README's "Event stream" section. Mockups: `.superpowers/brainstorm/webui-2026-10-12/content/icons-v6.html` screens 2 and 3, and `.superpowers/brainstorm/10263-1791735561/content/run-in-progress.html` for the activity block.

## Global Constraints

- No change to the JSON API or to any Go file. If one seems needed, stop and raise it with Kai.
- The page is served under `default-src 'self'`: no inline `<script>`, no `style="…"` attribute, no `{@html}`. SVG `fill` and `opacity` attributes are allowed.
- Agent text is untrusted. Raw HTML in markdown is shown as text. A link is an `<a>` only for `http:`, `https:` and `mailto:`, and always has `target="_blank" rel="noopener noreferrer"`. A markdown image never becomes an `<img>`.
- No request to another origin. The only `<img>` sources are `/api/rooms/{room}/attachments/{id}` for attachments with `image: true`.
- One new npm dependency, `marked`, pinned to exactly `18.0.14`. Other dependencies stay pinned; TypeScript stays on 6.x.
- No screen fetches on a timer. The only timers are the stream's reconnect delay and its silence watchdog.
- Nothing about read state is stored in the browser.
- A room never changes position in the room list because something was posted in it. The Unread page is the only place recency orders anything.
- No hand-holding copy: no helper text, no empty-state coaching, no tooltips, no confirmation dialogs. No dead controls: nothing is shown that slice 4 would make work.
- One header per screen and nothing else fixed at the top.
- Routes are hash-based: `#/` the list, `#/new` the form, `#/unread` the Unread page, `#/rooms/<id>` a room. Anything else shows the list.
- Colours are tokens in `web/src/app.css`, never literals in components. No animation.
- Run `just web-check` before each commit. Commit messages follow the repository's style (`feat(web): …`).

## Decisions this plan makes

| Question | Decision |
|---|---|
| Markdown renderer and sanitizer | `marked`'s lexer only (`gfm: true, breaks: true`); its HTML renderer is never called. `lib/markdown.ts` maps tokens to an allowlisted tree and `Markdown.svelte` renders the tree. Unknown token types and raw HTML become text. The repository already forbids `{@html}`, and this needs no sanitizer library. |
| `marked` version | 18.0.14 (2026-09-22). 18.1.1 was published the day this plan was written; too fresh to pin. |
| Markdown images | Rendered as a link whose text is the alt text, or the URL when there is none. |
| Syntax highlighting | None. Code is monospace and scrolls sideways. |
| Events against fetches | One rule for every snapshot fetch (rooms, a page of messages, activity): events that arrive while it is in flight are held, the answer is written, then the held events are applied in order. A stalled fetch delays events by at most the 15-second request timeout. |
| Page size | 50, the server's default. `more` is true when a page came back full. |
| Opening on a backlog | If the read marker is older than the oldest message of the first page, one more request for the 150 before it. If the marker is older still, or the room was never read, the feed opens at the oldest loaded message. At most two requests to open a room. |
| Feeds across a reload | `load()` (every `ready`, and the page becoming visible) keeps only the open room's feed and fetches its newest page in the same batch. The page is merged by id; if it does not reach the held messages (more than 50 were missed) it replaces them. |
| The divider | The first unread message is fixed when the room's messages first arrive and does not move while the room stays open, though the marker does. Label: `New`. |
| When the marker moves | When a sentinel after the last message is in view and `document.visibilityState` is `visible`. The marker is set to the newest held message. |
| Back from a room | To `#/unread` when the person came from there, otherwise `#/`. |
| Stop all in a room | Not in the room's header, which carries the room's own status line; slice 4 adds the room's Stop and menu there. The room states `Everything is stopped`, `Paused`, `Paused: turn cap of N reached` or `Archived` in a line at the foot of the feed. |
| A started turn | An agent's copy in state `working`, `done` or `failed` is shown as the activity block, whose summary line is the state (`Forge · working · 12 steps`). Copies in `waiting`, `delivered` or `cancelled` are a plain state line. One block per agent per message. |
| When activity is fetched | For a message with a `working` copy, as soon as the feed holds it. For a finished turn, when the person opens its block; until then the summary shows no step count. Counting steps of every finished turn up front would take a request per message, or a count on `Delivery` in the API. Raise the second with Kai if the missing count is felt. |
| Prompt cards | Read-only: the question, then the choices as text while open, `Answered: <answer> by <username>` or `Expired`. Slice 4 turns the choices into buttons. |
| Times | In the feed, 24-hour `HH:MM` for today and `<day> <month> HH:MM` otherwise. On the Unread page, `now`, `N min`, `N h`, `yesterday`, then the date. Computed when rendered; no timer refreshes them. |
| Not in this slice | Cancel, Stop, the composer, answering prompts (slice 4); message download (slice 6). |

## Review Focus

1. **Hostile markdown from an agent that has been reading the web:** `<script>`, `<img onerror>`, `[x](javascript:…)`, `![](https://tracker/p.gif)`, a `data:` link, a thousand nested `>`. Expected: shown as text or as a safe link, no element outside the allowlist, no request, no exception. Tests in Task 2.
2. **Events that race a fetch or repeat:** a message posted while the room's page is loading, a frame replayed after a reconnect, an event for a message older than the loaded window. Expected: each message once, in order, and no gap that paging can never fill. Tests in Task 4.
3. **A reply that streams for minutes on a phone that sleeps:** live edits are missed, a stale one arrives after the final text, the turn ends with no final edit. Expected: the text converges on the logged edit, `streaming` ends, a late live edit changes nothing. Tests in Task 4.
4. **Reading that did not happen:** the tab is hidden, or a long report is open but not scrolled to its end. Expected: the marker does not move. And a message that arrives while the person is at the bottom moves it. Tests in Task 7.
5. **Awkward messages:** an author who was removed (`author: null`), a reply to a message outside the loaded window or one that 404s, an attachment named `"><img src=x>.png`, a 262,144-byte text, a message with attachments and blank text. Expected: a rendered message every time. Tests in Tasks 2 and 6.

## File Structure

```
web/package.json, package-lock.json   + marked 18.0.14
web/src/
  app.css                    + tokens --ask-bg --ask-ink
  lib/types.ts               Message completed; + Author, Prompt, Attachment, Activity
  lib/api.ts                 + listMessages, getMessage, listActivity, markRoomRead, attachmentUrl
  lib/fixtures.ts            + message(), activity()
  lib/markdown.ts            parse, safeHref; the Block and Inline tree
  lib/feed.ts                pageSize, upsert, mergeNewest, firstUnread
  lib/format.ts              clock, ago, preview, size, duration, iconName
  lib/rooms.ts               + unreadRooms
  lib/route.svelte.ts        + route.prev, roomIdOf
  lib/seen.ts                whenSeen, the one use of IntersectionObserver
  lib/store.svelte.ts        + open, feeds, latest, quoted, activity and their functions
  Markdown.svelte            the tree, rendered
  Message.svelte             one message
  PromptCard.svelte          a prompt, read-only
  ActivityBlock.svelte       one turn's steps
  Room.svelte                header, feed, divider, paging, marker
  UnreadRow.svelte           the row above the groups
  Unread.svelte              the Unread page
  Header.svelte              + sub
  RoomRow.svelte             the row becomes a link
  RoomList.svelte            + UnreadRow
  Home.svelte                + the two routes
  (a *.test.ts beside each)
README.md, AGENTS.md
```

---

### Task 1: Wire shapes and the message calls

**Files:**
- Modify: `web/src/lib/types.ts`, `web/src/lib/api.ts`, `web/src/lib/api.test.ts`, `web/src/lib/fixtures.ts`

**Interfaces:**
- Produces, from `lib/types.ts` (field names are the wire's, from `docs/openapi.yaml`; `Message` replaces the slice 2 stub):

```ts
export type Author = { kind: 'human' | 'agent'; id: string; name: string };
export type Prompt = {
  kind: 'approval' | 'clarify'; choices: string[];
  state: 'open' | 'answered' | 'expired';
  answer: string | null; answered_by: Human | null; closed_at: string | null;
};
export type Attachment = { id: string; name: string; content_type: string; size: number; image: boolean };
export type Message = {
  id: string; room_id: string; author: Author | null; text: string;
  reply_to_id: string | null; deliveries: Delivery[]; created_at: string;
  prompt: Prompt | null; attachments: Attachment[]; client_id: string | null; streaming: boolean;
};
export type Activity = {
  id: string; message_id: string; agent: Agent; kind: 'remark' | 'tool';
  text: string; tool: string; preview: string; arguments: string;
  duration_ms: number | null; succeeded: boolean | null; created_at: string;
};
```

- Produces, from `lib/api.ts`:

```ts
// GET /api/rooms/{room}/messages?limit=N[&before=ID], unwraps {messages}; oldest first
export function listMessages(roomId: string, before?: string, limit?: number): Promise<Message[]>  // limit defaults to 50
export function getMessage(roomId: string, messageId: string): Promise<Message>
export function listActivity(roomId: string, messageId: string): Promise<Activity[]>  // unwraps {activities}
export function markRoomRead(roomId: string, messageId: string): Promise<void>  // PUT …/read {message_id}; 204
export function attachmentUrl(roomId: string, attachmentId: string): string     // no request; both ids URI-encoded
```

- Produces, from `lib/fixtures.ts`: `message(overrides?: Partial<Message>): Message` (id `'msg-1'`, room `'room-1'`, a human author `{kind:'human', id:'human-1', name:'kai'}`, text `'hello'`, `created_at: '2026-01-01T00:00:00.000Z'`, everything else empty, null or false) and `activity(overrides?: Partial<Activity>): Activity` (a finished, succeeded `tool` step by `agent()`).

- [ ] **Step 1: Write the failing tests** in `api.test.ts`, in the file's style. One per function: method, path, body, and the unwrapped value.

```ts
test('listMessages asks for a page before a message', async () => {
  const fetch = stubFetch(200, { messages: [{ id: '01M' }] });
  await expect(listMessages('01R', '01N', 150)).resolves.toEqual([{ id: '01M' }]);
  expect(fetch).toHaveBeenCalledWith('/api/rooms/01R/messages?limit=150&before=01N', expect.objectContaining({ method: 'GET' }));
});

test('markRoomRead puts the marker', async () => {
  const fetch = stubFetch(204);
  await expect(markRoomRead('01R', '01M')).resolves.toBeUndefined();
  expect(fetch).toHaveBeenCalledWith('/api/rooms/01R/read', expect.objectContaining({ method: 'PUT', body: '{"message_id":"01M"}' }));
});
```

  And `listMessages with no arguments asks for limit=50 and no before`, `getMessage`, `listActivity`, and `attachmentUrl encodes both ids` (`attachmentUrl('a/b', 'c d')` is `/api/rooms/a%2Fb/attachments/c%20d`).

- [ ] **Step 2: Run.** `cd web && npx vitest run src/lib/api.test.ts` — FAIL, the functions are not exported.
- [ ] **Step 3: Implement** the types, the five functions, and the two builders.
- [ ] **Step 4: Run.** `just web-check` — PASS. `svelte-check` proves the widened `Message` breaks nothing in slice 2's code.
- [ ] **Step 5: Commit.** `feat(web): the message, activity and read-marker calls`

---

### Task 2: Markdown as a tree

**Files:**
- Modify: `web/package.json`, `web/package-lock.json`
- Create: `web/src/lib/markdown.ts`, `web/src/lib/markdown.test.ts`, `web/src/Markdown.svelte`, `web/src/Markdown.test.ts`

**Interfaces:**
- Produces, from `lib/markdown.ts`:

```ts
export type Inline =
  | { type: 'text'; text: string }
  | { type: 'code'; text: string }
  | { type: 'strong' | 'em' | 'del'; children: Inline[] }
  | { type: 'link'; href: string; children: Inline[] }   // href has passed safeHref
  | { type: 'br' };
export type Block =
  | { type: 'paragraph'; children: Inline[] }
  | { type: 'heading'; depth: number; children: Inline[] }   // 1 to 6
  | { type: 'code'; text: string }
  | { type: 'blockquote'; children: Block[] }
  | { type: 'list'; ordered: boolean; start: number; items: Block[][] }
  | { type: 'table'; header: Inline[][]; rows: Inline[][][] }
  | { type: 'hr' };

export function parse(text: string): Block[];             // never throws
export function safeHref(href: string): string | null;    // the href, or null unless http:, https: or mailto:
```

- Produces: `Markdown.svelte` with props `{ text: string }`.

Rules for `parse`:

- Lex with `marked`'s lexer, options `gfm: true, breaks: true`. Never call `marked.parse`, `marked()` or a renderer.
- `html` tokens, block or inline, become text holding their raw source. So do token types this list does not name.
- A `link` whose href fails `safeHref` becomes its children with no link. An `image` becomes a `link` whose child is the alt text, or the href when the alt is empty; when its href fails `safeHref` it becomes text of the alt.
- `safeHref` trims, then decides by `new URL(href).protocol`; a string `URL` cannot parse on its own (a relative path, `#anchor`) is null.
- A task-list item starts with the text `☐ ` or `☑ `.
- Nesting is cut at depth 20: a block or inline deeper than that becomes text holding its raw source.
- If the lexer throws, or anything else does, the result is one paragraph holding the whole input as text.

`Markdown.svelte` renders each node as the plain element (`p`, `h1`–`h6`, `pre > code`, `blockquote`, `ul`/`ol start`, `li`, `table`, `hr`, `code`, `strong`, `em`, `del`, `a`, `br`) through recursive snippets. Every `a` has `target="_blank" rel="noopener noreferrer"`. `pre` is monospace with `overflow-x: auto; white-space: pre`; a table sits in a wrapper with `overflow-x: auto`. Headings are sized modestly (h1 no larger than 1.25rem).

- [ ] **Step 1: Add the dependency.** `cd web && npm install --save-exact marked@18.0.14`. It belongs in `dependencies`. Check `package.json` reads `"marked": "18.0.14"`.

- [ ] **Step 2: Write the failing tests** in `markdown.test.ts`.

```ts
const text = (s: string) => ({ type: 'text', text: s });

test('raw HTML is text', () => {
  expect(parse('<script>alert(1)</script>')).toEqual([
    { type: 'paragraph', children: [text('<script>alert(1)</script>')] },
  ]);
});

test('a javascript link keeps its words and loses its target', () => {
  expect(parse('[click](javascript:alert(1))')).toEqual([{ type: 'paragraph', children: [text('click')] }]);
});

test('an image is a link, never an image', () => {
  expect(parse('![chart](https://example.com/c.png)')).toEqual([
    { type: 'paragraph', children: [{ type: 'link', href: 'https://example.com/c.png', children: [text('chart')] }] },
  ]);
});

test.each([
  ['https://example.com/a?b=1', 'https://example.com/a?b=1'],
  ['mailto:kai@example.com', 'mailto:kai@example.com'],
  ['  http://example.com  ', 'http://example.com'],
  ['javascript:alert(1)', null], ['JaVaScRiPt:alert(1)', null], ['data:text/html,<b>', null],
  ['vbscript:x', null], ['/api/logout', null], ['#top', null], ['', null],
])('safeHref(%j) is %j', (href, want) => expect(safeHref(href)).toBe(want));
```

  And, by name: `inline HTML inside a paragraph is text` (`a <img src=x onerror=alert(1)> b`), `a fenced block keeps its text exactly` (indentation and blank lines), `a single newline is a break`, `strong, em, del and code nest`, `an ordered list keeps its start`, `a table has a header row and body rows`, `a task item starts with its box`, `a thousand nested quotes neither throws nor nests past 20`, `a 262,144-character text parses`, `blank input is no blocks`.

  In `Markdown.test.ts`, rendering through the component:
  - `a link opens in a new tab with noopener noreferrer` — `getByRole('link')` has both attributes and the href.
  - `hostile input renders no element outside the allowlist` — render the Review Focus 1 inputs joined by blank lines; `container.querySelector('script, img, iframe, object, embed, style, [onerror], [onclick]')` is null and the literal `<script>` is in the text content.
  - `a code block is a pre` — its `textContent` equals the source lines.

- [ ] **Step 3: Run.** `npx vitest run src/lib/markdown.test.ts src/Markdown.test.ts` — FAIL, modules not found.
- [ ] **Step 4: Implement** `markdown.ts` and `Markdown.svelte`.
- [ ] **Step 5: Run.** `just web-check` — PASS.
- [ ] **Step 6: Commit.** `feat(web): markdown rendered from a token tree, never from HTML`

---

### Task 3: Feed, unread-order and formatting rules

**Files:**
- Create: `web/src/lib/feed.ts`, `web/src/lib/feed.test.ts`, `web/src/lib/format.ts`, `web/src/lib/format.test.ts`
- Modify: `web/src/lib/rooms.ts`, `web/src/lib/rooms.test.ts`

**Interfaces:**
- Consumes: `Message`, `Author`, `Agent`, `Room` from Task 1; `isUnread` from `lib/rooms.ts`.
- Produces, from `lib/feed.ts`. Message ids are ULIDs, so string order is time order:

```ts
export const pageSize = 50;
// Puts m in its place by id, replacing a message with the same id. In place.
export function upsert(messages: Message[], m: Message): void;
// The held messages brought up to date with the newest page. When the page is
// full and its oldest message is newer than the newest held one, the page
// replaces them (replaced: true); otherwise each of its messages is upserted.
export function mergeNewest(held: Message[], page: Message[]): { messages: Message[]; replaced: boolean };
// The id of the first message newer than the marker; the first message when
// the marker is null; null when there is none.
export function firstUnread(messages: Message[], marker: string | null): string | null;
```

- Produces, from `lib/format.ts`:

```ts
export function clock(iso: string, now: Date): string;   // '14:02' on now's local day; otherwise toLocaleDateString(undefined, {day:'numeric', month:'short'}) + ' 14:02'
export function ago(iso: string, now: Date): string;     // under 1 min 'now'; under 60 min 'N min'; under 24 h 'N h'; under 48 h 'yesterday'; then the day and month as clock writes them
export function preview(text: string): string;           // runs of whitespace to one space, trimmed, cut to 280 code points
export function size(bytes: number): string;             // '812 B', '14 KB', '1.4 MB' (1024-based; one decimal from MB up)
export function duration(ms: number): string;            // '0.4s', '30.0s'; from 60 s, '1m 05s'
// The name an icon is generated from: an agent's handle from the directory
// (its name when the directory lacks it), a human's name, '' for no author.
export function iconName(author: Author | null, agents: Record<string, Agent>): string;
```

- Produces, from `lib/rooms.ts`: `export function unreadRooms(rooms: Room[]): Room[]` — the rooms `isUnread` accepts, those with `open_prompts > 0` first, then by `last_message_id` descending (a null id last).

- [ ] **Step 1: Write the failing tests.**

```ts
const m = (id: string, text = '') => message({ id, text });

test('upsert keeps id order and replaces by id', () => {
  const list = [m('01A'), m('01C')];
  upsert(list, m('01B'));
  upsert(list, m('01C', 'edited'));
  expect(list.map((x) => [x.id, x.text])).toEqual([['01A', ''], ['01B', ''], ['01C', 'edited']]);
});

test('a full page that does not reach the held messages replaces them', () => {
  const page = Array.from({ length: 50 }, (_, i) => m(`02${String(i).padStart(2, '0')}`));
  expect(mergeNewest([m('01A'), m('01B')], page)).toEqual({ messages: page, replaced: true });
});

test('ago', () => {
  const now = new Date('2026-10-12T12:00:00Z');
  const at = (ms: number) => new Date(now.getTime() - ms).toISOString();
  expect(ago(at(30_000), now)).toBe('now');
  expect(ago(at(4 * 60_000), now)).toBe('4 min');
  expect(ago(at(3 * 3_600_000), now)).toBe('3 h');
  expect(ago(at(30 * 3_600_000), now)).toBe('yesterday');
  expect(ago(at(72 * 3_600_000), now)).not.toMatch(/now|min|h$|yesterday/);
});
```

  And, by name: `a page that overlaps is merged and its copies win`, `a short page is merged even when it does not overlap` (fewer than 50 means it is the whole room), `firstUnread` (marker in the middle, marker equal to the newest gives null, null marker gives the first, empty list gives null), `clock is HH:MM today and carries the date otherwise` (build both dates from local parts, `new Date(2026, 9, 12, 14, 2)`; the other-day result ends with `' 14:02'` and is longer), `a time in the future reads now`, `preview collapses whitespace and cuts at 280 code points without splitting a pair`, `size`, `duration`, `iconName` (agent in the directory, agent not in it, human, null), `unreadRooms puts open prompts first, then newest first, and leaves out read and archived rooms`.

- [ ] **Step 2: Run.** `npx vitest run src/lib/feed.test.ts src/lib/format.test.ts src/lib/rooms.test.ts` — FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run.** `just web-check` — PASS.
- [ ] **Step 5: Commit.** `feat(web): feed merging, unread order and the time and size formats`

---

### Task 4: The store holds messages

**Files:**
- Modify: `web/src/lib/store.svelte.ts`, `web/src/lib/store.test.ts`

**Interfaces:**
- Consumes: `listMessages` (Task 1); `pageSize`, `upsert`, `mergeNewest` (Task 3).
- Produces:

```ts
export type Feed = {
  messages: Message[];  // ascending by id, each once
  more: boolean;        // the server may hold older ones
  loading: boolean;     // a page is being fetched
  error: string;        // why the first page failed; '' when none
};
// added to the store, all emptied by disconnect():
//   open: string | null             the room on screen
//   feeds: Record<string, Feed>     by room id
export function openRoom(id: string): Promise<void>;   // never rejects
export function closeRoom(id: string): void;           // clears open when it is id; the feed stays
export function loadOlder(id: string): Promise<void>;  // never rejects
```

Behaviour:

- **Holding.** Replace `load()`'s `loading` flag with a count of snapshot fetches in flight. While it is above zero `receive` holds events. When a fetch settles its answer is written first; when the count returns to zero the held events are applied in order, each in its own `try`. `disconnect()` zeroes the count, drops the held events, and an answer that arrives after it writes nothing. `load()`, `openRoom`'s fetches and `loadOlder` all go through this; `refetch` stays as it is.
- **`openRoom(id)`** sets `store.open`. If `store.loaded` is false it does nothing more: the coming `load()` fetches the page. If the feed is held with no error it does nothing more. Otherwise it sets the feed to empty with `loading: true`, fetches the newest page, and writes `{ messages: page, more: page.length === pageSize, loading: false, error: '' }`. On failure the feed is `{ messages: [], more: false, loading: false, error: <message> }` (a 401 records no message).
- **Backlog.** After the newest page is written, by either path, when the room's `last_read_message_id` is non-null and older than `messages[0].id` and `more` is true: one `listMessages(id, messages[0].id, 150)`, prepended, `more` set from `page.length === 150`. Its failure is ignored.
- **`loadOlder(id)`** does nothing unless the feed is held, `more` is true and `loading` is false. It fetches `listMessages(id, messages[0].id)`, prepends, sets `more`. A failure leaves `more` true and records nothing.
- **`load()`** adds `listMessages(open)` to its batch when `store.open` is set, with that one request's failure turned into `null` so it cannot fail the load. When the batch is written: `feeds` keeps only the open room's entry; a page that arrived is merged with `mergeNewest` (`more` becomes `page.length === pageSize` when the page replaced the held messages or no feed was held, and is otherwise left alone); then the backlog rule runs.
- **Events**, in `apply`. A message is put only when it has string `id`, `room_id`, `text` and `created_at` and array `deliveries` and `attachments`; slice 2's `last_message_id` and `working` handling keeps its own, looser checks and runs first.
  - `message.posted`, `delivery.changed`, `prompt.changed`, and `message.edited` whose data has a `message`: when the room's feed is held, `upsert` — unless the message is older than `messages[0]` and `more` is true, in which case it is ignored (it belongs to a page not yet loaded, and inserting it would make `loadOlder` skip what lies between).
  - `message.edited` with a `message` also records the id as finalized.
  - `message.edited` whose data has string `message_id` and `text` instead (a live edit): when the id is not finalized and a held feed has that message, set its `text` and set `streaming` to true. Otherwise nothing.
  - The finalized ids are forgotten on `disconnect()`.

- [ ] **Step 1: Write the failing tests** in `store.test.ts`, with `listMessages` added to the file's `vi.mock('./api')` list. Use the file's `deferred` helper to settle fetches when the test chooses.

```ts
test('a message posted while the page is loading is in the feed once', async () => {
  await loaded([room({ id: '01R' })]);           // helper: connect, ready, settle the load
  const page = deferred<Message[]>();
  vi.mocked(api.listMessages).mockReturnValueOnce(page.promise);
  const opening = openRoom('01R');
  handlers.event('message.posted', { message: message({ id: '01M2', room_id: '01R' }) });
  expect(store.feeds['01R'].messages).toEqual([]);
  page.resolve([message({ id: '01M1', room_id: '01R' }), message({ id: '01M2', room_id: '01R' })]);
  await opening;
  expect(store.feeds['01R'].messages.map((m) => m.id)).toEqual(['01M1', '01M2']);
});

test('a live edit sets the text, the logged edit replaces it, and a late live edit is ignored', async () => {
  await opened('01R', [message({ id: '01M1', room_id: '01R', text: '' })]);   // helper: loaded, then openRoom
  apply('message.edited', { message_id: '01M1', text: 'Bottom li', final: false });
  expect(store.feeds['01R'].messages[0]).toMatchObject({ text: 'Bottom li', streaming: true });
  apply('message.edited', { message: message({ id: '01M1', room_id: '01R', text: 'Bottom line.' }) });
  apply('message.edited', { message_id: '01M1', text: 'Bottom li', final: false });
  expect(store.feeds['01R'].messages[0]).toMatchObject({ text: 'Bottom line.', streaming: false });
});
```

  And, by name, each asserting what Behaviour says:
  - `openRoom loads the newest page and says whether older ones exist` — 50 messages gives `more: true`, 3 gives false
  - `openRoom before the first load fetches nothing, and the load brings the page`
  - `openRoom on a held feed fetches nothing`
  - `a failed page is recorded on the feed, and openRoom tries again`
  - `message.posted appends to a held feed, and applying it twice changes nothing`
  - `a message for a room whose feed is not held changes no feed`
  - `a message older than the oldest held is ignored while older pages remain, and inserted when none do`
  - `delivery.changed and prompt.changed replace the held message`
  - `a message event without text or attachments still moves last_message_id and changes no feed`
  - `a live edit for a message that is not held changes nothing`
  - `a marker older than the first page brings one request for the 150 before it` — second call is `('01R', <oldest id>, 150)`
  - `a null marker, and a marker inside the first page, bring no second request`
  - `loadOlder prepends the page before the oldest held` and `a second loadOlder while one is in flight does nothing`
  - `load keeps only the open feed and merges its newest page`
  - `load replaces the open feed when the page does not reach it`
  - `a page that fails inside load does not fail the load` — rooms are written, `store.error` is `''`
  - `events during loadOlder are held and applied after it`
  - `disconnect empties open and feeds, and a page that arrives afterwards writes nothing`

- [ ] **Step 2: Run.** `npx vitest run src/lib/store.test.ts` — the new tests FAIL; slice 2's pass.
- [ ] **Step 3: Implement.** Slice 2's tests in this file are the guard on the holding change; none of them may be edited to pass.
- [ ] **Step 4: Run.** `just web-check` — PASS.
- [ ] **Step 5: Commit.** `feat(web): the store holds the messages of opened rooms`

---

### Task 5: Read markers, newest messages and quoted messages

**Files:**
- Modify: `web/src/lib/store.svelte.ts`, `web/src/lib/store.test.ts`

**Interfaces:**
- Consumes: `markRoomRead`, `getMessage` (Task 1); `isUnread` from `lib/rooms.ts`.
- Produces:

```ts
// added to the store, emptied by disconnect():
//   latest: Record<string, Message>   by room id: the newest message known
//   quoted: Record<string, Message>   by message id: fetched because a reply names it
export function markRead(roomId: string, messageId: string): Promise<void>;  // never rejects
export function markAllRead(): Promise<void>;                                // never rejects
export function loadLatest(roomId: string): Promise<void>;                   // never rejects
export function loadQuoted(roomId: string, messageId: string): Promise<void>;// never rejects
```

Behaviour:

- **`markRead`** does nothing when the room is not held, when its `last_read_message_id` is already `messageId` or newer, or when the same marker for that room is in flight. Otherwise it calls `markRoomRead` and, on success, sets `last_read_message_id` to `messageId` unless the room now holds a newer one. A failure changes and records nothing; the marker is tried again the next time the end of the feed is seen. An answer after `disconnect()` writes nothing.
- **`markAllRead`** calls `markRead(room.id, room.last_message_id)` for every room `isUnread` accepts, together. If any underlying request failed it records the first failure in `store.error` through `failed`. (`markRead` therefore needs an inner form that reports its error.)
- **`latest`.** Every message `apply` puts (Task 4's shape check) becomes `latest[room_id]` when none is held there or the held one's id is not newer. A live edit also sets the text of a `latest` entry with that id, under the same finalized rule. This does not depend on a feed being held.
- **`loadLatest(roomId)`** does nothing when the room is not held, has no `last_message_id`, `latest[roomId].id` already equals it, or that fetch is in flight. Otherwise `getMessage(roomId, last_message_id)`, written under the same not-newer rule. A failure is ignored.
- **`loadQuoted(roomId, messageId)`** fetches a message once per id for the life of the connection: not when `quoted` has it, and not again after a failure (a 404 stays absent).
- `load()` leaves `latest` and `quoted` alone: replayed events keep them current, and an entry is only shown while its id still matches.

- [ ] **Step 1: Write the failing tests**, with `markRoomRead` and `getMessage` added to the mock list.

```ts
test('markRead puts the marker and then sets it on the room', async () => {
  await loaded([room({ id: '01R', last_message_id: '01M2', last_read_message_id: '01M1' })]);
  vi.mocked(api.markRoomRead).mockResolvedValue();
  await markRead('01R', '01M2');
  expect(api.markRoomRead).toHaveBeenCalledWith('01R', '01M2');
  expect(store.rooms['01R'].last_read_message_id).toBe('01M2');
});
```

  And, by name:
  - `markRead does nothing when the marker is already there or newer`
  - `the same marker asked twice while in flight is one request`
  - `a failed markRead leaves the room unread and store.error empty`
  - `a marker that settles after a newer one was set does not move it back`
  - `markAllRead marks every unread room to its newest message and skips read and archived rooms`
  - `markAllRead records a failure`
  - `a posted message becomes its room's latest, and an older one does not replace it`
  - `a live edit reaches latest`
  - `loadLatest fetches last_message_id once, and not at all when latest already has it`
  - `loadQuoted fetches once, and a 404 is not asked again`
  - `disconnect empties latest and quoted`

- [ ] **Step 2: Run.** `npx vitest run src/lib/store.test.ts` — FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run.** `just web-check` — PASS.
- [ ] **Step 5: Commit.** `feat(web): read markers, and the newest message of each room`

---

### Task 6: One message

**Files:**
- Create: `web/src/Message.svelte`, `web/src/Message.test.ts`, `web/src/PromptCard.svelte`, `web/src/PromptCard.test.ts`
- Modify: `web/src/app.css`

**Interfaces:**
- Consumes: `Markdown.svelte` (Task 2); `clock`, `preview`, `size`, `iconName` (Task 3); `attachmentUrl` (Task 1); `store.feeds`, `store.quoted`, `store.agents`, `loadQuoted` (Tasks 4, 5); `Icon.svelte`.
- Produces: `Message.svelte` with props `{ message: Message }`; `PromptCard.svelte` with props `{ message: Message }` (its `prompt` is non-null). Task 9 adds the activity blocks to `Message.svelte`.

What `Message.svelte` renders, as an `<article>`:

- The author's `Icon` at 34px (`iconName(message.author, store.agents)`), the name (`author.name`; `removed` when `author` is null), and `<time datetime={created_at}>` showing `clock(created_at, new Date())`.
- When `reply_to_id` is set: a quote line above the body with the quoted author's name and `preview` of its text, one line, cut by CSS. The quoted message is looked up in the room's feed, then in `store.quoted`; when it is in neither, an effect calls `loadQuoted(message.room_id, reply_to_id)` and until it lands (or if it never does) the quote reads `reply`.
- The body: `PromptCard` when `message.prompt` is non-null, otherwise `Markdown` of `message.text` (nothing when the text is blank). While `message.streaming`, a trailing `…` with `aria-label="writing"`.
- Attachments, each in its own line. `image: true`: `<a href={url} target="_blank" rel="noopener noreferrer"><img src={url} alt={name} loading="lazy"></a>`, the image capped at `max-width: 100%; max-height: 20rem`. Otherwise `<a href={url} download={name}>{name}</a>` followed by `size(att.size)`.
- A state line for each delivery in `waiting`, `delivered` or `cancelled`: `{agent.display_name} · {state}`. Deliveries in `working`, `done` or `failed` are left for Task 9.

`PromptCard.svelte`: the line `{author.name} asks`, the question through `Markdown`, then by `prompt.state`: `open` — the choices joined by ` · ` (nothing when there are none); `answered` — `Answered: {answer}`, and ` by {answered_by.username}` when it is non-null; `expired` — `Expired`. Card colours come from two new tokens in `app.css`: `--ask-bg` (`#fdf1d8`, dark `#3a2b12`) and `--ask-ink` (`#6b4400`, dark `#f6cf86`), with the existing `--ask` as its border.

- [ ] **Step 1: Write the failing tests.** Mock `./lib/api` and `./lib/stream` as `RoomList.live.test.ts` does; put rooms and feeds straight into `store`.

```ts
test('an image attachment shows inline from the server, and anything else is a download', () => {
  render(Message, { message: message({ room_id: '01R', text: '', attachments: [
    { id: '01A', name: 'chart.png', content_type: 'image/png', size: 2048, image: true },
    { id: '01B', name: '"><img src=x>.png', content_type: 'application/octet-stream', size: 1_500_000, image: false },
  ] }) });
  expect(screen.getByRole('img', { name: 'chart.png' })).toHaveAttribute('src', '/api/rooms/01R/attachments/01A');
  const file = screen.getByRole('link', { name: '"><img src=x>.png' });
  expect(file).toHaveAttribute('download', '"><img src=x>.png');
  expect(file).toHaveAttribute('href', '/api/rooms/01R/attachments/01B');
  expect(screen.getByText('1.4 MB')).toBeInTheDocument();
  expect(screen.getAllByRole('img')).toHaveLength(1);
});
```

  And, by name:
  - `shows the author, the time and the rendered text` — `**bold**` yields a `<strong>`
  - `a removed author reads removed and still gets an icon`
  - `an agent's icon comes from its handle in the directory` — compare the `<svg>` markup with one rendered by `Icon` for that handle
  - `a streaming message is marked as being written, and a finished one is not`
  - `a reply quotes a message in the feed`
  - `a reply to a message outside the feed fetches it once and then quotes it` — `loadQuoted` path; `getMessage` called with the room and the id
  - `a reply whose message cannot be fetched reads reply`
  - `waiting, delivered and cancelled copies are stated; working, done and failed are not`
  - `a prompt renders as a card, not as a plain message`
  - PromptCard: `an open prompt lists its choices as text and has no button`, `an answered prompt says what was answered and by whom`, `an answered prompt whose human was removed names nobody`, `an expired prompt says so`

- [ ] **Step 2: Run.** `npx vitest run src/Message.test.ts src/PromptCard.test.ts` — FAIL.
- [ ] **Step 3: Implement.** Layout follows screen 3 of `icons-v6.html`: flat, left-aligned, icon in a left column, 14.5px body.
- [ ] **Step 4: Run.** `just web-check` — PASS.
- [ ] **Step 5: Commit.** `feat(web): a message, with its quote, attachments, states and prompt`

---

### Task 7: The room screen

**Files:**
- Create: `web/src/lib/seen.ts`, `web/src/lib/seen.test.ts`, `web/src/Room.svelte`, `web/src/Room.test.ts`
- Modify: `web/src/lib/route.svelte.ts`, `web/src/lib/route.test.ts`, `web/src/Header.svelte`, `web/src/RoomRow.svelte`, `web/src/RoomRow.test.ts`, `web/src/Home.svelte`, `web/src/Home.test.ts`

**Interfaces:**
- Consumes: `Message.svelte` (Task 6); `openRoom`, `closeRoom`, `loadOlder`, `markRead`, `store` (Tasks 4, 5); `firstUnread` (Task 3); `roomName` from `lib/rooms.ts`.
- Produces:

```ts
// lib/seen.ts — a Svelte action. report(true) when the node enters the
// viewport, report(false) when it leaves. The only use of IntersectionObserver.
export function whenSeen(node: Element, report: (seen: boolean) => void): { destroy(): void };

// lib/route.svelte.ts
export const route: { path: string; prev: string };   // prev: the path before this one; '/' at start
export function roomIdOf(path: string): string | null; // '/rooms/<id>' gives the id (one segment, non-empty); anything else null
```

- Produces: `Room.svelte` with props `{ id: string }`; `Header.svelte` gains an optional `sub: string`, a second, smaller, one-line row under the title in `--on-aub2`.

`RoomRow`: the row's content sits in `<a href="#/rooms/{room.id}">` inside the `<li>`, filling the row and inheriting its colours.

`Home`: `#/rooms/<id>` renders `{#key id}<Room {id} />{/key}`. Task 8 adds `#/unread`.

`Room.svelte`:

- Calls `openRoom(id)` on mount and `closeRoom(id)` on unmount. When `store.loaded` and `store.rooms[id]` is absent, it calls `navigate('/')`.
- Header: title `roomName(room)`; `back` is `#/unread` when `route.prev === '/unread'`, otherwise `#/`; `sub` is each agent as `{display_name} working`, `{display_name} idle` (connected, not working) or `{display_name} offline`, joined by ` · `. No controls in the header.
- The screen is a column of `height: 100dvh`; the feed is the one scrolling element (`flex: 1; overflow-y: auto`).
- `feed.error`: the message as `role="alert"` and a **Retry** button that calls `openRoom(id)`.
- Inside the feed, in order: a top sentinel (`data-edge="top"`, only while `feed.more`); the messages, keyed by id; a divider `<div role="separator">New</div>` immediately before the first unread message; an end sentinel (`data-edge="end"`); the state line (see Decisions) when the store is halted or the room is paused or archived, halted taking precedence.
- **The divider** is `firstUnread(feed.messages, room.last_read_message_id)`, computed once, when the feed first has messages after mount, and kept. No divider when that is null.
- **First placement**, once, after the first render that has messages: the divider's `scrollIntoView({ block: 'start' })` when there is one, otherwise the feed's `scrollTop = scrollHeight`.
- **Keeping place**, on every later change to the messages. Measured before the DOM updates (`$effect.pre`): whether the feed was within 40px of its bottom, its `scrollHeight`, and the first message's id. After: if the first id changed (older messages were prepended), `scrollTop` grows by the growth in `scrollHeight`; otherwise, if it was at the bottom, `scrollTop = scrollHeight`. A streaming message growing counts as a change.
- **Paging:** the top sentinel reporting `true`, after first placement, calls `loadOlder(id)`.
- **The marker:** the end sentinel's reports are kept in `endSeen`; the document's visibility is kept in state from `<svelte:document onvisibilitychange>`. An effect calls `markRead(id, <id of the last message>)` whenever `endSeen` is true, the document is visible, and the feed has a message. It reruns when a message arrives.

- [ ] **Step 1: Write the failing tests.** `seen.test.ts` stubs the global `IntersectionObserver` with a class that records its callback and its `observe`/`disconnect` calls. `Room.test.ts` mocks `./lib/seen` so the test drives the sentinels, and stubs `Element.prototype.scrollIntoView`:

```ts
const edges = new Map<string, (seen: boolean) => void>();
vi.mock('./lib/seen', () => ({
  whenSeen: (node: HTMLElement, report: (seen: boolean) => void) => {
    edges.set(node.dataset.edge ?? '', report);
    return { destroy: () => edges.delete(node.dataset.edge ?? '') };
  },
}));

test('the marker does not move while the page is hidden, and moves when it is shown', async () => {
  vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('hidden');
  await showRoom({ last_read_message_id: '01M1' }, ['01M1', '01M2']);   // helper: store with the room and its feed, then render
  edges.get('end')!(true);
  await tick();
  expect(api.markRoomRead).not.toHaveBeenCalled();
  vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('visible');
  document.dispatchEvent(new Event('visibilitychange'));
  await waitFor(() => expect(api.markRoomRead).toHaveBeenCalledWith('room-1', '01M2'));
});
```

  And, by name:
  - seen: `reports entering and leaving`, `destroy disconnects the observer`
  - route: `prev is the path before the current one`, `roomIdOf` (`/rooms/01R` gives `01R`; `/rooms/`, `/rooms/a/b`, `/new`, `/` give null)
  - RoomRow: `the row links to its room`
  - Home: `#/rooms/<id> shows the room, and another id shows another room`
  - Room: `the header names the room and says what each agent is doing` — `Forge working · Scout idle · Atlas offline`
  - `back leads to the list, or to Unread when the person came from there`
  - `a room the store does not hold sends the person to the list`
  - `the divider sits before the first unread message, and the feed is placed on it` — `scrollIntoView` called on the separator
  - `with nothing unread there is no divider and the feed is placed at its end` — define `scrollHeight` on the feed element as 900; `scrollTop` is 900
  - `the divider stays where it was after the marker moves`
  - `the marker does not move until the end has been seen` — a long unread room: no `markRoomRead` until `edges.get('end')(true)`
  - `a message that arrives while the end is in view moves the marker to it`
  - `a message that arrives after the end has left view does not move the marker`
  - `seeing the top loads older messages, and not before first placement`
  - `older messages arriving keep the same message in view` — `scrollHeight` 900 then 1500 with `scrollTop` 0 gives `scrollTop` 600
  - `a failed page shows the error and Retry asks again`
  - `the foot of the feed states Paused, the turn cap, Archived, and Everything is stopped` — `Paused: turn cap of 6 reached`
  - `leaving the room closes it` — `store.open` is null after unmount

- [ ] **Step 2: Run.** `npx vitest run src/Room.test.ts src/lib/seen.test.ts src/lib/route.test.ts src/RoomRow.test.ts src/Home.test.ts` — FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run.** `just web-check` — PASS.
- [ ] **Step 5: Commit.** `feat(web): a room opens at its first unread message and reading moves the marker`

---

### Task 8: The Unread row and the Unread page

**Files:**
- Create: `web/src/UnreadRow.svelte`, `web/src/UnreadRow.test.ts`, `web/src/Unread.svelte`, `web/src/Unread.test.ts`
- Modify: `web/src/RoomList.svelte`, `web/src/RoomList.test.ts`, `web/src/Home.svelte`, `web/src/Home.test.ts`

**Interfaces:**
- Consumes: `unreadRooms`, `roomName` (Task 3, `lib/rooms.ts`); `ago`, `preview`, `iconName` (Task 3); `store.latest`, `loadLatest`, `markAllRead` (Task 5); `navigate` from `lib/route.svelte.ts`; `Header.svelte`, `Icon.svelte`.
- Produces: `UnreadRow.svelte` (no props) and `Unread.svelte` (no props).

`UnreadRow`: rendered by `RoomList` as the first thing inside its `<nav>`, above both groups. With no unread room it renders nothing. Otherwise one `<a href="#/unread">` styled as the mockup's `.pin` (a raised block on `--aub-hi`, not a list row): the word `Unread`; under it the names of `unreadRooms(...)` joined by `, ` on one line, cut by CSS; a tag `1 question` or `N questions` when the unread rooms' `open_prompts` sum to more than zero; and a pill with the number of unread rooms.

`Unread`: `Home` renders it at `#/unread`.

- Header: title `Unread`, back `#/`, and a **Mark all read** button that calls `markAllRead()` and then `navigate('/')`. The button is disabled while that is pending.
- One `<a href="#/rooms/{id}">` per room of `unreadRooms(...)`, full width, separated by a line. First line: the room's name in bold and `ago(latest.created_at, new Date())`. Second: a 15px `Icon` for the author, then `{name}:`, or `{name} asks:` when the message is a prompt, then `preview(text)`; when the text is blank, the attachment names joined by `, `. Two lines at most, cut by CSS (`-webkit-line-clamp: 2`).
- `latest` here is `store.latest[room.id]`, used only when its id equals the room's `last_message_id`. Until it does, the row shows the name alone.
- A room with `open_prompts > 0` gets the amber treatment (`--ask-bg` and an inset `--ask` edge).
- An effect calls `loadLatest(room.id)` for each unread room; it reruns when the set of unread rooms or a `last_message_id` changes.
- With nothing unread the page shows only its header.
- `store.error` is shown as on the other screens (Home's `problem` snippet).

- [ ] **Step 1: Write the failing tests.**

```ts
test('rooms with an open prompt come first, then the newest', () => {
  putRoom(room({ id: 'a', topic: 'Trip', last_message_id: '01M9' }));
  putRoom(room({ id: 'b', topic: 'Heat pump', last_message_id: '01M5' }));
  putRoom(room({ id: 'c', topic: 'Web UI', last_message_id: '01M1', open_prompts: 1 }));
  putRoom(room({ id: 'd', topic: 'Read', last_message_id: '01M2', last_read_message_id: '01M2' }));
  render(Unread);
  expect(screen.getAllByRole('link').filter((a) => a.getAttribute('href')?.startsWith('#/rooms/')).map((a) => a.getAttribute('href')))
    .toEqual(['#/rooms/c', '#/rooms/a', '#/rooms/b']);
});
```

  And, by name:
  - UnreadRow: `nothing unread renders nothing`, `shows the count and the names in Unread-page order`, `shows 1 question, 2 questions, and no tag for none`, `links to #/unread`
  - RoomList: `the Unread row comes before both groups`, `a room in a collapsed group still counts`
  - Unread: `a row shows the author and the start of the newest message` (from `store.latest`), `a prompt reads asks`, `a message with no text names its attachments`, `a removed author shows the text alone`
  - `the newest message is fetched when the store does not hold it` — `getMessage` called with the room and its `last_message_id`, and the row fills in
  - `a latest that is not the room's newest is not shown`
  - `a message that arrives adds its room to the page`
  - `Mark all read marks every unread room and returns to the list` — `markRoomRead` per room, then `location.hash` is `#/`
  - `Mark all read is disabled while it is pending`
  - Home: `#/unread shows the Unread page`

- [ ] **Step 2: Run.** `npx vitest run src/Unread.test.ts src/UnreadRow.test.ts src/RoomList.test.ts src/Home.test.ts` — FAIL.
- [ ] **Step 3: Implement.** Layout follows screens 1 and 2 of `icons-v6.html`.
- [ ] **Step 4: Run.** `just web-check` — PASS.
- [ ] **Step 5: Commit.** `feat(web): the Unread row, the Unread page and mark all read`

---

### Task 9: The activity block

**Files:**
- Create: `web/src/ActivityBlock.svelte`, `web/src/ActivityBlock.test.ts`
- Modify: `web/src/lib/store.svelte.ts`, `web/src/lib/store.test.ts`, `web/src/Message.svelte`, `web/src/Message.test.ts`

**Interfaces:**
- Consumes: `listActivity` (Task 1); `duration` (Task 3); the holding rule and feeds (Task 4).
- Produces:

```ts
// added to the store, emptied by disconnect():
//   activity: Record<string, Activity[]>   by message id, oldest first; absent until loaded
export function loadActivity(roomId: string, messageId: string): Promise<void>;  // never rejects
```

- Produces: `ActivityBlock.svelte` with props `{ message: Message; delivery: Delivery }`. `Message.svelte` renders one after the body for each delivery in `working`, `done` or `failed`.

Store behaviour:

- **`loadActivity`** is a snapshot fetch under Task 4's holding rule. One request per message at a time. The answer replaces `activity[messageId]`. A failure leaves the entry absent and records nothing.
- **`activity.posted`** (`{ message_id, activity }` with string ids): when `activity[message_id]` exists, the activity replaces the entry with its id or is appended. When it does not exist, nothing: the list is fetched whole when it is wanted.
- **Working turns load themselves.** Whenever a message with a delivery in `working` is written into a held feed (a page, a merge, or an event) and `activity` has no entry for it, `loadActivity` is called.
- **`load()`** empties `activity` when its batch is written; the rule above then reloads it for turns still working in the open room, and an open block reloads its own (below).

`ActivityBlock`, for one agent's turn on one message. Its steps are `store.activity[message.id]`, filtered to `delivery.agent.id`; `undefined` when the entry is absent.

- The summary is a `<button aria-expanded>`: `{display_name} · {state}`, then ` · N steps` (`1 step`) when the steps are loaded, N counting `tool` activities only. A filled dot in `--ok` precedes it while `working`.
- While `working` and loaded with at least one activity, a second line outside the button: `Now: {tool} · {preview}` for a tool (the tool's name alone when the preview is empty), or the remark's text.
- Opening it, or being open while the entry is absent, calls `loadActivity(message.room_id, message.id)`. It starts closed.
- Open: the activities in order. A `tool` is a row with a mark, `{tool} · {preview}` and a time: `✓` and `duration(duration_ms)` when `succeeded` is true; `✕` and the duration when false; `●` and `running` when unfinished and the turn is `working`; `–` and no time when unfinished and the turn is over. Marks carry `aria-label` `succeeded`, `failed`, `running`, `unfinished`. A tool with non-empty `arguments` is a `<button aria-expanded>` that shows them in a `<pre>` (wrapping, `word-break: break-word`); one with none is plain. A `remark` is its text in italics, as text, not markdown.

- [ ] **Step 1: Write the failing tests.**

```ts
test('a tool that finishes replaces its running entry', async () => {
  await opened('01R', [message({ id: '01M1', room_id: '01R', deliveries: [{ agent: agent({ id: '01A' }), state: 'working' }] })]);
  // the working copy loaded the list: one running step
  expect(api.listActivity).toHaveBeenCalledWith('01R', '01M1');
  apply('activity.posted', { message_id: '01M1', activity: activity({ id: '01S1', message_id: '01M1', duration_ms: 2100, succeeded: true }) });
  expect(store.activity['01M1']).toHaveLength(1);
  expect(store.activity['01M1'][0]).toMatchObject({ duration_ms: 2100, succeeded: true });
});
```

  (`listActivity` mocked to answer one running step `01S1`.) And, by name:
  - store: `a message that starts working loads its activity` (through `delivery.changed`), `a finished message loads none until asked`, `activity.posted appends a new step`, `activity.posted for a message with no entry changes nothing`, `a step posted while the list is loading is not lost`, `two loadActivity calls for one message are one request`, `load empties activity and reloads it for a turn still working in the open room`, `disconnect empties activity`
  - block: `closed, a working turn says how many steps and what it is doing now` — `Forge · working · 2 steps`, `Now: web_extract · xcelenergy.com/rates`
  - `a remark as the newest activity is what it is doing now`
  - `a finished turn shows its state and no count until opened; opening fetches and shows the count`
  - `one step is 1 step, and remarks are not counted`
  - `open, each tool shows its mark, preview and time` — `✓ … 2.1s`, `✕ … 30.0s`, `running`
  - `an unfinished tool on a finished turn is unfinished, not running`
  - `tapping a tool shows its arguments, and a tool with none is not a button`
  - `a remark is italic text and is not parsed as markdown` — `**x**` stays literal
  - `two agents on one message get a block each, with their own steps`
  - `an open block whose list was emptied by a reload asks for it again`
  - Message: `working, done and failed copies are activity blocks`

- [ ] **Step 2: Run.** `npx vitest run src/ActivityBlock.test.ts src/lib/store.test.ts src/Message.test.ts` — FAIL.
- [ ] **Step 3: Implement.** Layout follows `run-in-progress.html` in the page's own tokens (`--paper2`, `--line`, `--ok`, `--err`); the dot does not pulse.
- [ ] **Step 4: Run.** `just web-check` — PASS.
- [ ] **Step 5: Commit.** `feat(web): an activity block for each turn`

---

### Task 10: Documents, and a check against the built binary

**Files:**
- Modify: `README.md` (the "Web UI" section), `AGENTS.md` (the "Web UI" bullet under Code Conventions)

- [ ] **Step 1: README.** In "Web UI", replace `Rooms do not open yet.` with what a room now shows and how: the routes `#/rooms/<id>` and `#/unread`; that a room opens at its first unread message under a `New` divider, or at its end; that the read marker moves when the end of the feed is in view on a visible page; the Unread row and page and their order; that markdown is lexed by `marked` and rendered from a token tree with raw HTML shown as text, links limited to `http`, `https` and `mailto`, and markdown images shown as links; that activity is fetched for working turns and on opening a finished one. In the paragraph on the store, add the messages of opened rooms, the holding of events during any snapshot fetch, and what `load()` does to feeds. Say that posting and the room's controls are not there yet.
- [ ] **Step 2: AGENTS.md.** Extend the Web UI bullet: message text reaches the page only through `web/src/lib/markdown.ts` and `Markdown.svelte` (never an HTML string); `marked` is the one runtime dependency; `IntersectionObserver` is used only in `web/src/lib/seen.ts`; a fetch whose answer is a snapshot goes through the store's holding rule.
- [ ] **Step 3: Run the gate.** `just check` — PASS.
- [ ] **Step 4: Check under the real policy.** The dev server sends no content security policy, so build and run the binary:

```
just build
HERMES_CHAT_DB_PATH=dev.db HERMES_CHAT_TLS_MODE=off HERMES_CHAT_COOKIE_SECURE=false bin/hermes-chat-linux-amd64 serve
FAKEAGENT_URL=http://127.0.0.1:8080 FAKEAGENT_TOKEN=<token> go run ./cmd/fakeagent
```

  With a migrated `dev.db`, a user and an agent (README, "Commands"). Post to the agent's DM with `curl` as the README's "HTTP API" section shows, then in a browser: the room opens from the list; the echo appears without a reload; the room's unread dot clears once its end is seen; the Unread row and page list a room posted to while another is open; the browser console shows no Content-Security-Policy violation on any screen. Report what was and was not checked; this step has no automated test.
- [ ] **Step 5: Commit.** `docs: the web UI's room, unread page and markdown`
