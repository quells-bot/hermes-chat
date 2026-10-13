# Web UI Slice 6: Power-User Layer Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A person can find any room by name (archived ones too), move and send from the keyboard, complete an @mention in a channel, download a message as a `.md` file, add the app to a home screen, and on a desktop browser see the room list beside whatever is open.

**Architecture:** Everything is in the browser; the server and the JSON API do not change. Search is a pure filter over the rooms the store already holds, with its open/query state in one small module that the header button, the keyboard and the list all read. The desktop layout is one flag, `layout.wide`, set from `matchMedia`; `Home.svelte` draws the list beside the open screen when it is true and in place of it when it is false, and the open screen's component is never remounted when the flag flips. Shortcuts are one `keydown` listener on the document, live while `Home` is mounted. Mention completion and download are pure functions in `lib/` with a thin layer in `Composer.svelte` and `Message.svelte`. The manifest is a file Vite hashes, naming icons copied unhashed from `web/public/assets/`.

**Tech Stack:** Svelte 5, TypeScript 6, Vite 8; Vitest 5 with `@testing-library/svelte` on jsdom. No new npm dependency. One Go test file changes (`web/embed_test.go`); no other Go file.

**Spec:** `docs/superpowers/specs/2026-10-12-web-ui-design.md` (slice 6 of section 8; sections 2, 3, 4.1, 4.2, 4.4, 4.6 and 4.8 apply). The mention rule is the server's, in `cmd/hermes-chat/internal/bus/delivery.go` (`mentionedAgents`, `wakeAgents`). The room list mockup is `.superpowers/brainstorm/webui-2026-10-12/content/icons-v6.html` (the `⌕` in the header). There is no mockup for the desktop layout, the search field, or the completion list.

## Global Constraints

- No change to the JSON API, to any route, or to any Go file other than `web/embed_test.go`. If one seems needed, stop and raise it with Kai.
- The page is served under `default-src 'self'`: no inline `<script>`, no `style="…"` attribute, no `{@html}`, nothing inlined as a `data:` URI, no request to another origin.
- `go build` and `go test ./...` work with no Node and no UI build; a Go test that reads `web/dist` skips when `index.html` is absent.
- The room list never rearranges itself when messages arrive. Search results follow the same alphabetical order.
- No hand-holding: no helper text, no empty-state coaching (no "No results", no "Select a room"), no tooltips, no legend of shortcuts on screen.
- One header per screen and nothing else fixed at the top. No tab bar.
- No screen fetches on a timer. Nothing in this slice fetches at all.
- Agent-written text (display names, handles, topics set through the API, message text) is drawn as plain text, or through `Markdown.svelte` for a message body, never as HTML.
- Nothing new is stored in the browser. The search query is emptied when `Home` unmounts.
- Routes stay hash-based and none is added.
- Colours are tokens in `web/src/app.css`, never literals in components. No animation. npm dependencies stay pinned; TypeScript stays on 6.x.
- Run `just web-check` before each commit. Commit messages follow the repository's style (`feat(web): …`).

## Decisions this plan makes

Each is Claude's call. The four marked **(ask Kai)** change what the person does every day and are worth a look before building.

| Question | Decision |
|---|---|
| Where the search field is | A `⌕` button in the list's header, between the title and `+`. It opens a one-line field under the header, above the Unread row; `×` or Escape closes it and empties it. |
| What a query matches | Case-insensitive substring. Each whitespace-separated term must be found in the room's name, its topic, or the display name or handle of any agent in it. One leading `@` or `#` on a term is ignored. Accents are not folded. |
| What the list shows while a query is typed | Only the matching rows, flat: no Unread row, no group headings. Direct messages first, then rooms, each by name; archived rooms after those, tagged `archived`. An empty field shows the normal list. |
| Opening a result | Enter opens the first row; a tap opens the one tapped. Either closes and empties search. |
| Breakpoint | Wide at `min-width: 56rem` (896px): a 20rem list and at least 36rem of pane. A tablet in portrait keeps the phone layout. |
| Wide layout at `#/` | The pane is empty paper. Nothing is opened for the person. |
| Wide layout, back control | A back link to `#/` is hidden (the list is already there). Back to `#/unread` and back to a room from its settings stay. |
| Wide layout, Stop all | Drawn once, in the list's header. The pane's headers leave it out while wide. |
| Line length in a wide pane | A message is at most 46rem wide, left-aligned. Headers and the composer span the pane. |
| Search shortcut | `/` when the focus is not in a field, and `Ctrl+K` or `⌘K` anywhere. On the phone layout it goes to the list first. |
| Moving between rooms | `Alt+↓` and `Alt+↑` open the next and previous room in list order (direct messages, then rooms; collapsed groups included; archived rooms skipped), stopping at the ends. `Alt+Shift+↓/↑` do the same over unread rooms only. They work while typing. **(ask Kai)** |
| Sending | `Ctrl+Enter` or `⌘Enter` sends from the composer. Enter stays a newline at every width, as it is today. **(ask Kai: Slack sends on Enter; say so if desktop should.)** |
| Where completion is offered | Only in a channel. In a direct message or a conversation every agent is woken by every message, so a mention changes nothing. |
| What completion offers | The room's agents that are not revoked, whose handle or display name starts with what follows `@`, by handle. `↑`/`↓` choose, Enter or Tab inserts `@handle` and a space, Escape puts the list away until the token changes. Offered only with the caret at the end of its token. |
| What a download holds | The message's text exactly as the agent or person wrote it, with a final newline. No front matter, no author line. **(ask Kai)** |
| A download's name | `<room name>-<YYYYMMDD>-<HHmm>.md`, local time of the message, the name cut to 60 characters with every run of anything but letters and digits made one `-`. |
| Which messages can be downloaded | Any with text that is not still streaming and is not a prompt. The control is `⤓`, beside the time. |
| The manifest's icons | `web/public/assets/icon-192-v1.png`, `icon-512-v1.png`, `icon-maskable-512-v1.png`. They are not hashed, the server sends everything under `/assets/` as immutable for a year, so a changed icon gets a new `-vN` name. |
| The manifest's content type | Whatever Go's table gives `.webmanifest`. Browsers do not check a manifest's type, and adding one would be a Go change. |
| Installed app | `display: standalone`, `start_url: /`. No service worker (spec non-goal), so no offline page and no install prompt of our own. **(ask Kai: skip `standalone` if the browser's own back and reload are wanted on the phone.)** |

## Review Focus

1. **A query that is not a tidy word.** `(`, `[a-`, `\`, `  `, `@`, `#`, `SCOUT`, a query matching nothing followed by Enter. Expected: no exception, no navigation, `[]` for a blank one, and case ignored. Tests in Tasks 1 and 2.
2. **A layout change with typing on screen.** A tablet is rotated, or a desktop window dragged narrower, mid-sentence. Expected: the room is not remounted, the composer keeps its text, the feed keeps its place. Test in Task 3.
3. **A shortcut pressed while typing.** `/` in the composer, the note, the search field or a prompt's answer box types a slash; an IME composing does nothing; a key pressed after logout does nothing. Tests in Task 4.
4. **`@` that is not the start of a mention, and a caret that is not at the end.** `kai@example.com`, `hi@sc`, `(@sc`, a caret inside `@scout`, text after the caret, Enter with no candidate, an agent removed from the room while the list is open. Expected: the server's token rule is followed, text after the caret survives, and Enter is a newline whenever no option is shown. Tests in Task 5.
5. **A room name that is not a file name.** `../../etc/passwd`, `a/b\c:d`, 300 characters, only punctuation, emoji. Expected: no separator in the name, at most 60 characters before the date, `message` when nothing is left, and the object URL is always revoked. Tests in Task 6.

## File Structure

```
web/src/
  lib/search.svelte.ts    search's open/query state, and matchRooms                      (new)
  lib/layout.svelte.ts    layout.wide, kept by matchMedia                                (new)
  lib/keys.ts             which shortcut a keydown is, and the document listener         (new)
  lib/mention.ts          the mention token at a caret, its candidates, the insertion    (new)
  lib/download.ts         a message's file name, and saving text as a file               (new)
  lib/rooms.ts            + byRoomName exported, listOrder, neighbour
  ListScreen.svelte       the list's header, search field, list and footer, out of Home  (new)
  Search.svelte           the search field                                               (new)
  RoomList.svelte         draws results while a query is typed
  RoomRow.svelte          + archived tag
  Home.svelte             the shell: list beside or in place of the open screen
  Note.svelte, RoomSettings.svelte   no HaltControl while wide
  Composer.svelte         + Ctrl/⌘+Enter, + the completion list
  Message.svelte          + Download, + max-width
  app.css                 #app loses its max-width while the shell is wide
  manifest.webmanifest    (new)
  (a *.test.ts beside each)
web/public/assets/        icon-192-v1.png, icon-512-v1.png, icon-maskable-512-v1.png     (new)
web/index.html            + manifest link, theme-color
web/embed_test.go         + the manifest check
README.md, AGENTS.md
```

---

### Task 1: Matching rooms, and search's state

**Files:**
- Create: `web/src/lib/search.svelte.ts`, `web/src/lib/search.test.ts`
- Modify: `web/src/lib/rooms.ts`, `web/src/lib/rooms.test.ts`

**Interfaces:**
- Produces, from `lib/rooms.ts`:

```ts
export function byRoomName(a: Room, b: Room): number;   // the existing private comparator, exported
export function listOrder(rooms: Room[]): Room[];        // groupRooms(rooms): dms, then rooms
```

- Produces, from `lib/search.svelte.ts`:

```ts
export const search: { open: boolean; query: string };   // $state, { open: false, query: '' }
export function openSearch(): void;                       // open = true; the query is kept
export function closeSearch(): void;                      // open = false, query = ''

// Rooms matching query, unarchived in listOrder first, then archived ones
// (direct messages, then rooms, each by byRoomName). [] for a blank query.
export function matchRooms(rooms: Room[], query: string): Room[];
```

`matchRooms`: lowercase the query with `toLocaleLowerCase()`, split on whitespace, drop one leading `@` or `#` from each term, drop empty terms. A room matches when every term is a substring of at least one of its lowercased `roomName(room)`, `topic`, or an agent's `display_name` or `handle`. No regular expression is built from the query.

- [ ] **Step 1: Write the failing tests.**

```ts
const scout = agent({ id: 'a1', handle: 'scout', display_name: 'Scout' });
const atlas = agent({ id: 'a2', handle: 'atlas-2', display_name: 'Atlas' });
const rooms = [
  room({ id: 'r1', topic: 'Trip planning', agents: [scout] }),
  room({ id: 'r2', topic: 'Heat pump vs. gas furnace', agents: [atlas] }),
  room({ id: 'r3', topic: 'Old trip', agents: [atlas], archived: true }),
  room({ id: 'd1', flavor: 'dm', topic: '', agents: [scout] }),
];
const ids = (q: string) => matchRooms(rooms, q).map((r) => r.id);

test('a term matches a topic, an agent name or a handle, whatever its case', () => {
  expect(ids('HEAT')).toEqual(['r2']);
  expect(ids('atlas-2')).toEqual(['r2', 'r3']);
});

test('an agent’s name finds its DM and every room it is in, DM first', () => {
  expect(ids('scout')).toEqual(['d1', 'r1']);
  expect(ids('@scout')).toEqual(['d1', 'r1']);
});

test('every term must match, and archived rooms come last', () => {
  expect(ids('trip')).toEqual(['r1', 'r3']);
  expect(ids('trip atlas')).toEqual(['r3']);
  expect(ids('#trip')).toEqual(['r1', 'r3']);
});

test('a blank or unmatched query finds nothing and odd characters do not throw', () => {
  for (const q of ['', '   ', '@', '#', '(', '[a-', '\\', 'zzz']) {
    expect(ids(q)).toEqual([]);
  }
  expect(ids('vs.')).toEqual(['r2']);
});

test('closing search empties the query and opening keeps it', () => {
  search.query = 'tr';
  openSearch();
  expect(search).toEqual({ open: true, query: 'tr' });
  closeSearch();
  expect(search).toEqual({ open: false, query: '' });
});
```

  In `rooms.test.ts`: `listOrder puts direct messages before rooms and leaves archived ones out` (the four rooms above give `['d1', 'r2', 'r1']`).

- [ ] **Step 2: Run.** `cd web && npx vitest run src/lib/search.test.ts src/lib/rooms.test.ts` — FAIL, module not found.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run.** `just web-check` — PASS.
- [ ] **Step 5: Commit.** `feat(web): matching rooms by name, agent and handle`

---

### Task 2: The search field and its results

**Files:**
- Create: `web/src/ListScreen.svelte`, `web/src/ListScreen.test.ts`, `web/src/Search.svelte`, `web/src/Search.test.ts`
- Modify: `web/src/Home.svelte`, `web/src/Home.test.ts`, `web/src/RoomList.svelte`, `web/src/RoomList.test.ts`, `web/src/RoomRow.svelte`, `web/src/RoomRow.test.ts`

**Interfaces:**
- Consumes: `search`, `openSearch`, `closeSearch`, `matchRooms` from Task 1; `navigate` from the router.
- Produces: `ListScreen.svelte`, no props: everything `Home.svelte` draws today in its final `{:else}` branch (the header with `reconnecting`, `+` and `HaltControl`; the `problem` snippet's content; `RoomList` once `store.loaded`; the footer), moved as it is. `Home.svelte` draws `<ListScreen />` there and keeps `problem` for `#/new`. `Search.svelte`, no props.

`ListScreen.svelte` adds, in the header before `+`, `<button type="button" aria-label="Search" aria-expanded={search.open}>⌕</button>`, which calls `openSearch()` when closed and `closeSearch()` when open. It is styled as the `+` beside it (no border, `--on-aub`), which needs a rule that outranks Header's `header :global(button)`, as `HaltControl`'s does. Under the header, `{#if search.open}<Search />{/if}`.

`Search.svelte`: on `--aub`, an `<input type="search" aria-label="Search" autocomplete="off" autocapitalize="none" spellcheck="false">` bound to `search.query`, and `<button type="button" aria-label="Close search">×</button>`. The input takes the focus when the component mounts. Escape in the input calls `closeSearch()`. Enter opens the first of `matchRooms(Object.values(store.rooms), search.query)`: `closeSearch()` then `navigate('/rooms/' + id)`; with no match it does nothing.

`RoomList.svelte`: `const results = $derived(search.open && search.query.trim() !== '' ? matchRooms(Object.values(store.rooms), search.query) : null)`. When `results` is not null the `<nav>` holds only a `<ul>` of `RoomRow`, one per result, and a click anywhere in that `<ul>` calls `closeSearch()`. Otherwise it is as it is today.

`RoomRow.svelte`: when `room.archived`, a `<span class="tag pause">archived</span>` after the name.

- [ ] **Step 1: Write the failing tests.** `ListScreen.test.ts` mocks as `Home.test.ts` does (session, store, route's `navigate`, `collapsed`); `beforeEach` calls `closeSearch()`.

```ts
test('the search button opens a field that has the focus, and closes it', async () => {
  render(ListScreen);
  expect(screen.queryByRole('searchbox')).toBeNull();
  await userEvent.click(screen.getByRole('button', { name: 'Search' }));
  expect(screen.getByRole('searchbox', { name: 'Search' })).toHaveFocus();
  await userEvent.click(screen.getByRole('button', { name: 'Search' }));
  expect(screen.queryByRole('searchbox')).toBeNull();
});

test('typing narrows the list to matches, in a collapsed group too, and without the Unread row', async () => {
  store.rooms = {
    r1: room({ id: 'r1', topic: 'Trip planning', last_message_id: 'm2', last_read_message_id: 'm1' }),
    r2: room({ id: 'r2', topic: 'Tax paperwork' }),
    r3: room({ id: 'r3', topic: 'Old trip', archived: true }),
  };
  render(ListScreen);
  await userEvent.click(screen.getByRole('button', { name: 'Rooms' }));   // collapse
  await userEvent.click(screen.getByRole('button', { name: 'Search' }));
  await userEvent.type(screen.getByRole('searchbox'), 'trip');
  const rows = screen.getAllByRole('listitem');
  expect(rows.map((r) => r.textContent)).toEqual([expect.stringContaining('Trip planning'), expect.stringContaining('Old trip')]);
  expect(rows[1]).toHaveTextContent('archived');
  expect(screen.queryByRole('link', { name: /Unread/ })).toBeNull();
  expect(screen.queryByRole('button', { name: 'Rooms' })).toBeNull();
});

test('Enter opens the first match and closes search', async () => {
  store.rooms = { r1: room({ id: 'r1', topic: 'Trip planning' }) };
  render(ListScreen);
  await userEvent.click(screen.getByRole('button', { name: 'Search' }));
  await userEvent.type(screen.getByRole('searchbox'), 'trip{Enter}');
  expect(navigate).toHaveBeenCalledWith('/rooms/r1');
  expect(search).toEqual({ open: false, query: '' });
});
```

  And, by name:
  - `Enter with no match goes nowhere and keeps the field` — type `zzz{Enter}`; `navigate` not called, the field still holds `zzz`.
  - `Escape closes and empties the field`, `× closes and empties the field`.
  - `an open field with nothing typed shows the normal list` — the Unread row and both group buttons are present.
  - `tapping a result closes search` — click the row's link; `search.open` is false.
  - `a query of regex characters draws no rows and does not throw` — `[a-`.

  `RoomRow.test.ts`: `an archived room is tagged archived and never unread`. `Home.test.ts`: the existing list tests pass unchanged against the moved markup; add `the list’s header has the search button`.

- [ ] **Step 2: Run.** `npx vitest run src/ListScreen.test.ts src/Search.test.ts src/RoomRow.test.ts src/RoomList.test.ts src/Home.test.ts` — FAIL, components not found.
- [ ] **Step 3: Implement.** Move the list branch into `ListScreen.svelte` first and run `Home.test.ts` green before adding search.
- [ ] **Step 4: Run.** `just web-check` — PASS.
- [ ] **Step 5: Commit.** `feat(web): search over room and agent names`

---

### Task 3: The list beside the room on a wide screen

**Files:**
- Create: `web/src/lib/layout.svelte.ts`, `web/src/lib/layout.test.ts`
- Modify: `web/src/Home.svelte`, `web/src/Home.test.ts`, `web/src/Note.svelte`, `web/src/Note.test.ts`, `web/src/RoomSettings.svelte`, `web/src/RoomSettings.test.ts`, `web/src/Message.svelte`, `web/src/app.css`

**Interfaces:**
- Consumes: `ListScreen.svelte` from Task 2.
- Produces, from `lib/layout.svelte.ts`:

```ts
export const wideQuery = '(min-width: 56rem)';
export const layout: { wide: boolean };   // $state, false until startLayout says otherwise

// startLayout sets layout.wide from matchMedia(wideQuery) now and on every
// change. It returns the stop function, which also sets wide back to false.
// Where matchMedia does not exist (jsdom) it leaves wide false.
export function startLayout(): () => void;
```

`Home.svelte` calls `startLayout()` in `onMount` beside `startRouting()`, and both stops (and `closeSearch()`) run on unmount. Its markup becomes:

```svelte
<div class="shell" class:wide={layout.wide}>
  {#if layout.wide || atList}
    <div class="side"><ListScreen /></div>
  {/if}
  {#if layout.wide || !atList}
    <div class="pane">
      <!-- today's if/else chain for a room, settings, #/unread, #/note and #/new, with no final {:else} -->
    </div>
  {/if}
</div>
```

`atList` is true when the path is none of those five. The `.pane` block must stay one block whose condition does not change when `layout.wide` flips with a screen open, so the screen inside is not remounted.

Styles, in `Home.svelte` unless said otherwise:

- `.shell.wide`: `display: grid; grid-template-columns: 20rem minmax(0, 1fr); height: 100dvh`. `.wide .side` and `.wide .pane`: `overflow-y: auto; min-height: 0`. `.wide .side` has `background: var(--aub)`; `.wide .pane` has `background: var(--paper)` and a `border-left: 1px solid var(--line)`.
- `.wide .pane :global(a.back[href='#/'])`: `display: none`.
- `app.css`: `#app:has(.shell.wide) { max-width: none; }`.
- `Message.svelte`: `article { max-width: 46rem; }`.

`Note.svelte`, `RoomSettings.svelte` and `Home.svelte`'s `#/new` header draw `HaltControl` only when `!layout.wide`.

- [ ] **Step 1: Write the failing tests.**

  `layout.test.ts`, with `window.matchMedia` stubbed by a fake that records its listener (`vi.stubGlobal`, restored in `afterEach`):

```ts
test('startLayout follows the media query and stop puts it back', () => {
  const mq = fakeMedia(true);                       // { matches, addEventListener, removeEventListener, fire(matches) }
  vi.stubGlobal('matchMedia', vi.fn(() => mq));
  const stop = startLayout();
  expect(matchMedia).toHaveBeenCalledWith('(min-width: 56rem)');
  expect(layout.wide).toBe(true);
  mq.fire(false);
  expect(layout.wide).toBe(false);
  mq.fire(true);
  stop();
  expect(layout.wide).toBe(false);
  expect(mq.removeEventListener).toHaveBeenCalled();
});

test('without matchMedia the layout is narrow', () => {
  vi.stubGlobal('matchMedia', undefined);
  startLayout()();
  expect(layout.wide).toBe(false);
});
```

  `Home.test.ts` (`beforeEach` sets `layout.wide = false`):

```ts
test('a room keeps its typing when the layout changes', async () => {
  store.rooms = { '01R': room({ id: '01R', topic: 'Trip' }) };
  route.path = '/rooms/01R';
  render(Home);
  expect(screen.queryByRole('navigation')).toBeNull();
  const box = screen.getByRole('textbox', { name: 'Message' });
  await userEvent.type(box, 'half a sentence');
  layout.wide = true;
  await waitFor(() => expect(screen.getByRole('navigation')).toBeInTheDocument());
  expect(screen.getByRole('textbox', { name: 'Message' })).toBe(box);
  expect(box).toHaveValue('half a sentence');
  layout.wide = false;
  await waitFor(() => expect(screen.queryByRole('navigation')).toBeNull());
  expect(box).toHaveValue('half a sentence');
  expect(openRoom).toHaveBeenCalledTimes(1);
});
```

  And, by name:
  - `wide shows the list beside #/new, #/unread, #/note and a room’s settings` — each path has both the `navigation` and its own heading.
  - `wide at #/ shows the list and an empty pane` — `container.querySelector('.pane')` exists and has no child element.
  - `narrow at #/ has no pane`.
  - `wide draws Stop all once` — at `/new`, `/note` and `/rooms/01R/settings`, `getAllByRole('button', { name: 'Stop all' })` has length 1; narrow at `/note` has 1 too.
  - `Home stops the layout and empties search on unmount`.

  `Note.test.ts` and `RoomSettings.test.ts`: `no Stop all in the header while wide`.

- [ ] **Step 2: Run.** `npx vitest run src/lib/layout.test.ts src/Home.test.ts src/Note.test.ts src/RoomSettings.test.ts` — FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run.** `just web-check` — PASS. Then `just web-dev` against a local server and look at it at 1280px and at 390px: two columns that scroll separately, a room whose composer stays at the bottom of the pane, and the phone layout unchanged.
- [ ] **Step 5: Commit.** `feat(web): the room list as a sidebar on a wide screen`

---

### Task 4: Keyboard shortcuts

**Files:**
- Create: `web/src/lib/keys.ts`, `web/src/lib/keys.test.ts`
- Modify: `web/src/lib/rooms.ts`, `web/src/lib/rooms.test.ts`, `web/src/Home.svelte`, `web/src/Home.test.ts`, `web/src/Composer.svelte`, `web/src/Composer.test.ts`

**Interfaces:**
- Consumes: `openSearch` (Task 1), `layout` (Task 3), `listOrder` (Task 1), `isUnread`, `route`, `navigate`, `roomIdOf`, `settingsIdOf`, `store`.
- Produces, from `lib/rooms.ts`:

```ts
// The id of the next (step 1) or previous (step -1) room after current in
// listOrder, skipping read rooms when unreadOnly. It does not wrap: null at
// an end. With current null or not in the list, step 1 gives the first and
// step -1 the last.
export function neighbour(rooms: Room[], current: string | null, step: 1 | -1, unreadOnly?: boolean): string | null;
```

- Produces, from `lib/keys.ts`:

```ts
export type Shortcut = 'search' | 'next' | 'prev' | 'next-unread' | 'prev-unread';
export function shortcut(e: KeyboardEvent): Shortcut | null;
// startKeys listens for keydown on the document and returns the stop function.
export function startKeys(): () => void;
```

`shortcut`, in this order: `e.isComposing` gives null. `k` or `K` with exactly one of Ctrl and Meta, and neither Alt nor Shift, gives `search`. `/` with no modifier gives `search` unless `e.target` is an `input`, `textarea`, `select` or `isContentEditable`. `ArrowDown` or `ArrowUp` with Alt and neither Ctrl nor Meta gives `next` or `prev`, and the `-unread` form with Shift. Anything else gives null.

`startKeys`: for a shortcut, `e.preventDefault()`, then: `search` calls `openSearch()` and, when `!layout.wide` and `route.path !== '/'`, `navigate('/')`; the others take `current = roomIdOf(route.path) ?? settingsIdOf(route.path)` and, when `neighbour(Object.values(store.rooms), current, step, unreadOnly)` is not null, `navigate('/rooms/' + id)`. `Home.svelte` starts it in `onMount` and stops it on unmount.

`Composer.svelte`: `keydown` on the textarea with `key === 'Enter'` and Ctrl or Meta (and not `isComposing`) calls `preventDefault()` and `submit()` when the Send button would be enabled. Plain Enter is left alone.

- [ ] **Step 1: Write the failing tests.**

  `keys.test.ts`: a table over `shortcut(new KeyboardEvent('keydown', init))`, dispatched on the element named so that `target` is set:

```ts
test.each([
  ['/', {}, 'body', 'search'],
  ['/', {}, 'textarea', null],
  ['/', {}, 'input', null],
  ['/', { shiftKey: true }, 'body', null],
  ['k', { ctrlKey: true }, 'textarea', 'search'],
  ['k', { metaKey: true }, 'body', 'search'],
  ['k', { ctrlKey: true, metaKey: true }, 'body', null],
  ['k', {}, 'body', null],
  ['ArrowDown', { altKey: true }, 'textarea', 'next'],
  ['ArrowUp', { altKey: true }, 'body', 'prev'],
  ['ArrowDown', { altKey: true, shiftKey: true }, 'body', 'next-unread'],
  ['ArrowUp', { altKey: true, shiftKey: true }, 'body', 'prev-unread'],
  ['ArrowDown', {}, 'body', null],
  ['ArrowDown', { altKey: true, ctrlKey: true }, 'body', null],
  ['/', { isComposing: true }, 'body', null],
])('%s %o on %s is %s', (key, init, on, want) => { /* … */ });
```

  And, with `./route.svelte`'s `navigate` mocked and `store.rooms` set to a DM `d1` and rooms `r1` (read), `r2` (unread), by name:
  - `Alt+↓ from the DM opens the first room` — `route.path = '/rooms/d1'`; `navigate('/rooms/r1')`.
  - `Alt+↓ on the last room goes nowhere` and `Alt+↑ from the list opens the last room`.
  - `Alt+Shift+↓ skips read rooms` — from `d1`, `navigate('/rooms/r2')`.
  - `a room’s settings count as that room` — at `/rooms/r1/settings`, Alt+↓ gives `r2`.
  - `/ opens search, and goes to the list first on the phone layout` — at `/rooms/r1` narrow: `search.open` true and `navigate('/')`; wide: `navigate` not called.
  - `/ in a textarea is left alone` — dispatch on a textarea in the document; `defaultPrevented` false, `search.open` false.
  - `a key after stop does nothing`.

  `rooms.test.ts`: `neighbour` at each end, with `current` null both ways, with an archived `current`, with `unreadOnly` and nothing unread (null), and with an empty list (null).

  `Composer.test.ts`: `Ctrl+Enter sends` and `⌘Enter sends` (`{Control>}{Enter}{/Control}`; `send` called once with the text), `Enter alone is a newline and sends nothing`, `Ctrl+Enter with nothing typed sends nothing`, `Ctrl+Enter while a send is in flight sends nothing more`.

  `Home.test.ts`: `Home starts the shortcuts and stops them on unmount` (mock `./lib/keys`).

- [ ] **Step 2: Run.** `npx vitest run src/lib/keys.test.ts src/lib/rooms.test.ts src/Composer.test.ts src/Home.test.ts` — FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run.** `just web-check` — PASS.
- [ ] **Step 5: Commit.** `feat(web): shortcuts for search, moving between rooms and sending`

---

### Task 5: @mention completion in a channel

**Files:**
- Create: `web/src/lib/mention.ts`, `web/src/lib/mention.test.ts`
- Modify: `web/src/Composer.svelte`, `web/src/Composer.test.ts`

**Interfaces:**
- Consumes: the `keydown` handler Task 4 put on the composer's textarea; `byName` from `lib/rooms.ts`; `Icon.svelte`.
- Produces, from `lib/mention.ts`:

```ts
export type MentionAt = { start: number; query: string };   // start is the index of '@'

// The mention being typed at caret, or null. The token is what lies between
// the last whitespace before caret (or the start) and caret, less one leading
// '('. It is a mention when it starts with '@' and the character at caret is
// whitespace or the end of the text.
export function mentionAt(text: string, caret: number): MentionAt | null;

// Agents not revoked whose handle or display_name starts with query, case
// ignored, by handle.
export function candidates(agents: RoomAgent[], query: string): RoomAgent[];

// The text with '@handle' in place of what was typed from at.start to caret,
// followed by one space unless whitespace already follows; and where the
// caret goes: after that space.
export function complete(text: string, at: MentionAt, caret: number, handle: string): { text: string; caret: number };
```

`Composer.svelte`: it tracks the textarea's `selectionStart` on `input`, `keyup`, `click` and `focus`. `options` is `candidates(room.agents, at.query)` when the room's flavor is `channel`, `mentionAt(text, caret)` is not null, and that mention has not been put away; otherwise empty. While `options` is not empty a `<div role="listbox" aria-label="Mention">` sits above the bar, one `<button type="button" role="option" aria-selected={i === chosen}>` per agent with its icon, `display_name` and `@handle`. `chosen` starts at 0 and returns to 0 when the query changes.

Keys, checked before Task 4's rule and only while `options` is not empty: `ArrowDown` and `ArrowUp` move `chosen`, wrapping; `Enter` (with no Ctrl or Meta) and `Tab` insert the chosen agent; `Escape` puts the list away for this `at.start` until the token is no longer a mention. Each calls `preventDefault()`. Ctrl/⌘+Enter still sends. A tap on an option inserts it. Inserting sets `text` from `complete(...)`, clears `attempt` (it is an edit), returns the focus to the textarea and sets its selection to the new caret after `tick()`.

- [ ] **Step 1: Write the failing tests.**

```ts
test.each([
  ['hi @sc', 6, { start: 3, query: 'sc' }],
  ['@', 1, { start: 0, query: '' }],
  ['(@sc', 4, { start: 1, query: 'sc' }],
  ['a\n@s', 4, { start: 2, query: 's' }],
  ['@sc now', 3, { start: 0, query: 'sc' }],
  ['hi@sc', 5, null],
  ['kai@example.com', 15, null],
  ['@scout', 3, null],          // the caret is inside the token
  ['((@sc', 5, null],           // only one bracket is ignored, as on the server
  ['hi ', 3, null],
])('mentionAt(%j, %i)', (text, caret, want) => {
  expect(mentionAt(text, caret)).toEqual(want);
});

test('complete replaces what was typed and keeps what follows', () => {
  expect(complete('hi @sc', { start: 3, query: 'sc' }, 6, 'scout')).toEqual({ text: 'hi @scout ', caret: 10 });
  expect(complete('@sc now', { start: 0, query: 'sc' }, 3, 'scout')).toEqual({ text: '@scout now', caret: 7 });
  expect(complete('(@', { start: 1, query: '' }, 2, 'scout')).toEqual({ text: '(@scout ', caret: 8 });
});

test('candidates match the start of a handle or a name, skip revoked agents, and come by handle', () => {
  const agents = [
    agent({ id: '1', handle: 'scout', display_name: 'Scout' }),
    agent({ id: '2', handle: 'atlas', display_name: 'Sky Atlas' }),
    agent({ id: '3', handle: 'sage', display_name: 'Sage', revoked: true }),
  ];
  expect(candidates(agents, 'S').map((a) => a.handle)).toEqual(['atlas', 'scout']);
  expect(candidates(agents, '').map((a) => a.handle)).toEqual(['atlas', 'scout']);
  expect(candidates(agents, 'cout')).toEqual([]);
});
```

  `Composer.test.ts`, with the room a channel holding `scout` and `atlas`, by name:
  - `typing @ in a channel lists the room’s agents` — two options, the first `aria-selected`.
  - `the list narrows as the handle is typed` — `@sc` leaves one.
  - `Enter inserts the chosen handle and sends nothing` — the box holds `@scout ` and `send` was not called.
  - `Tab inserts too`, `↓ then Enter inserts the second`, `a tap inserts and the box has the focus again`.
  - `Escape puts the list away and Enter is a newline again` — after Escape, `{Enter}` leaves a `\n` in the box; typing a space and a new `@` lists again.
  - `Enter with no candidate is a newline` — `@zz{Enter}`.
  - `text after the caret survives` — set the value to `@sc now` with the caret at 3 (`setSelectionRange`, then `fireEvent.keyUp`), Enter; the box holds `@scout now`.
  - `Ctrl+Enter sends while the list is open` — the text sent is `@sc`.
  - `no list in a conversation or a direct message`.
  - `an agent removed from the room leaves the list` — replace `store.rooms[id]` without it while the list is open.
  - `an address is not a mention` — `kai@example.com` shows no list.
  - `completing drops a failed attempt’s id` — after a refused send, a completion makes the button read `Send`.

- [ ] **Step 2: Run.** `npx vitest run src/lib/mention.test.ts src/Composer.test.ts` — FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run.** `just web-check` — PASS.
- [ ] **Step 5: Commit.** `feat(web): @mention completion in a channel`

---

### Task 6: Downloading a message

**Files:**
- Create: `web/src/lib/download.ts`, `web/src/lib/download.test.ts`
- Modify: `web/src/Message.svelte`, `web/src/Message.test.ts`

**Interfaces:**
- Consumes: `roomName` from `lib/rooms.ts`; `store.rooms`.
- Produces, from `lib/download.ts`:

```ts
// '<room>-<YYYYMMDD>-<HHmm>.md', the date and time local. room is NFKC
// normalized, every run of characters other than \p{L} and \p{N} becomes one
// '-', leading and trailing '-' go, and it is cut to 60 code points (then any
// trailing '-' goes again). When nothing is left it is 'message'.
export function fileName(room: string, at: Date): string;

// saveText hands the browser text as a file to save: a Blob of type
// 'text/markdown;charset=utf-8' holding text and a final '\n' when it lacks
// one, an object URL, a click on an <a download={name}> that is never added
// to the document, and the URL revoked whether or not the click throws.
export function saveText(name: string, text: string): void;
```

`Message.svelte`: after the time (and before Reply), when `message.prompt === null`, `message.text.trim() !== ''` and `!message.streaming`, `<button type="button" class="reply" aria-label="Download">⤓</button>`. It calls `saveText(fileName(name, new Date(message.created_at)), message.text)`, where `name` is `roomName` of `store.rooms[message.room_id]` when the store holds that room (`Object.hasOwn`) and `''` otherwise. It is drawn in an archived room too, where Reply is not.

- [ ] **Step 1: Write the failing tests.**

```ts
const at = new Date(2026, 9, 13, 14, 2);   // local, so the test does not depend on the zone

test.each([
  ['Heat pump vs. gas furnace', 'Heat-pump-vs-gas-furnace-20261013-1402.md'],
  ['../../etc/passwd', 'etc-passwd-20261013-1402.md'],
  ['a/b\\c:d', 'a-b-c-d-20261013-1402.md'],
  ['Küche 🍳 Umbau', 'Küche-Umbau-20261013-1402.md'],
  ['...', 'message-20261013-1402.md'],
  ['', 'message-20261013-1402.md'],
])('fileName(%j)', (room, want) => {
  expect(fileName(room, at)).toBe(want);
});

test('a long name is cut to 60 characters and does not end in a dash', () => {
  const name = fileName(`${'a'.repeat(59)} ${'b'.repeat(240)}`, at);
  expect(name).toBe(`${'a'.repeat(59)}-20261013-1402.md`);
});

test('saveText saves the text with a final newline and revokes the URL', async () => {
  const create = vi.fn((_: Blob) => 'blob:x');
  const revoke = vi.fn();
  vi.stubGlobal('URL', Object.assign(URL, { createObjectURL: create, revokeObjectURL: revoke }));
  const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(function (this: HTMLAnchorElement) {
    expect(this.download).toBe('n.md');
    expect(this.getAttribute('href')).toBe('blob:x');
  });
  saveText('n.md', '# Report\r\nbody 👍');
  const blob = create.mock.calls[0][0];
  expect(blob.type).toBe('text/markdown;charset=utf-8');
  expect(await blob.text()).toBe('# Report\r\nbody 👍\n');
  expect(click).toHaveBeenCalledTimes(1);
  expect(revoke).toHaveBeenCalledWith('blob:x');
});
```

  And, by name: `text that ends in a newline gets no second one`; `the URL is revoked when the click throws` (the spy throws; `saveText` rethrows; `revoke` called).

  `Message.test.ts`, with `./lib/download`'s `saveText` mocked and `fileName` real:
  - `Download saves the message’s own text under the room’s name` — `store.rooms` holds the room with topic `Trip planning`; `saveText` called with a name starting `Trip-planning-` and ending `.md`, and the exact `message.text` (markdown characters untouched).
  - `a DM’s download is named for its agent`.
  - `a message that is still streaming, a prompt, and a message with only an attachment have no Download`.
  - `Download is offered where Reply is not` — no `onreply` passed.
  - `a message whose room is not held downloads as message-…`.

- [ ] **Step 2: Run.** `npx vitest run src/lib/download.test.ts src/Message.test.ts` — FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run.** `just web-check` — PASS.
- [ ] **Step 5: Commit.** `feat(web): download a message as a .md file`

---

### Task 7: The home-screen manifest

**Files:**
- Create: `web/src/manifest.webmanifest`, `web/public/assets/icon-192-v1.png`, `web/public/assets/icon-512-v1.png`, `web/public/assets/icon-maskable-512-v1.png`
- Modify: `web/index.html`, `web/embed_test.go`

**Interfaces:**
- Produces: nothing another task uses.

`web/src/manifest.webmanifest`, exactly:

```json
{
  "name": "Hermes Chat",
  "short_name": "Hermes",
  "id": "/",
  "start_url": "/",
  "scope": "/",
  "display": "standalone",
  "background_color": "#3b1b42",
  "theme_color": "#3b1b42",
  "icons": [
    { "src": "/assets/icon-192-v1.png", "sizes": "192x192", "type": "image/png", "purpose": "any" },
    { "src": "/assets/icon-512-v1.png", "sizes": "512x512", "type": "image/png", "purpose": "any" },
    { "src": "/assets/icon-maskable-512-v1.png", "sizes": "512x512", "type": "image/png", "purpose": "maskable" }
  ]
}
```

`web/index.html` gains, in `<head>`:

```html
<link rel="manifest" href="/src/manifest.webmanifest" />
<meta name="theme-color" content="#3b1b42" media="(prefers-color-scheme: light)" />
<meta name="theme-color" content="#2a1230" media="(prefers-color-scheme: dark)" />
```

Vite rewrites the link to a hashed file under `/assets/`, and copies `web/public/assets/` to `web/dist/assets/` as it is.

The icons are the drawing in `web/src/icons/favicon.svg` (a 64-unit square): a `#3b1b42` tile; a `#f3e9f5` bubble, which is a rounded rectangle from (12,18) to (52,46) with radius 8 and a tail, the triangle (20,46) (31,46) (20,56); and a `#ff8fb8` dot at (49,19) of radius 9 with a 4-unit `#3b1b42` ring. The two `any` icons have the tile's corners rounded at radius 14 on a transparent ground. The maskable icon has a square tile to every edge and the bubble and dot scaled to 62% about the centre, so that they sit inside the mask's safe zone. Draw them with a throwaway Pillow script in the scratchpad (`uv run --with pillow`), at four times the size and scaled down with `LANCZOS`; the script is not committed.

- [ ] **Step 1: Write the failing test** in `web/embed_test.go`, skipping when the UI is not built as its neighbour does:

```go
// The manifest is one of the page's own files, and every icon it names is
// one the binary serves.
func TestDist_ManifestNamesIconsThatAreServed(t *testing.T) {
	raw, err := fs.ReadFile(Dist(), "index.html")
	if err != nil {
		t.Skip("web UI not built")
	}
	m := regexp.MustCompile(`<link rel="manifest"[^>]* href="/(assets/[^"]+)"`).FindSubmatch(raw)
	require.NotNil(t, m, "no manifest link")
	body, err := fs.ReadFile(Dist(), string(m[1]))
	require.NoError(t, err)
	var manifest struct {
		StartURL string `json:"start_url"`
		Display  string `json:"display"`
		Icons    []struct{ Src, Sizes, Purpose string }
	}
	require.NoError(t, json.Unmarshal(body, &manifest))
	assert.Equal(t, "/", manifest.StartURL)
	assert.Equal(t, "standalone", manifest.Display)
	require.Len(t, manifest.Icons, 3)
	for _, icon := range manifest.Icons {
		require.True(t, strings.HasPrefix(icon.Src, "/assets/"), icon.Src)
		info, err := fs.Stat(Dist(), strings.TrimPrefix(icon.Src, "/"))
		require.NoError(t, err, icon.Src)
		assert.Positive(t, info.Size())
	}
}
```

- [ ] **Step 2: Run.** `just web-build && go test ./web` — FAIL with "no manifest link".
- [ ] **Step 3: Implement:** draw the three icons, write the manifest, add the three lines to `index.html`. Read each PNG back and look at it.
- [ ] **Step 4: Run.** `just web-build && go test ./web` — PASS, and `TestDist_EntryPageHasNoInlineScriptOrStyle` still passes (the manifest's `href` starts `/assets/`). `git status` shows nothing new under `web/dist`. Then `just check` — PASS.
- [ ] **Step 5: Commit.** `feat(web): a manifest for the home screen`

---

### Task 8: Documents and the whole check

**Files:**
- Modify: `README.md` (the "Web UI" section), `AGENTS.md` (the "Web UI" convention), `docs/superpowers/specs/2026-10-12-web-ui-design.md` (section 8's last paragraph)

- [ ] **Step 1: README.** In "Web UI": a paragraph each for search, the wide layout, the shortcuts, mention completion, download and the manifest, stating what this plan's decision table decided as it was built (what a query matches and the order of results; the 56rem breakpoint and what the pane and the headers do; every key; that completion is in channels only and follows the server's token rule; what a download holds and how it is named; where the icons live and why their names carry a version). Replace the sentence that says an archived room is reached only by its address: search finds it. Change "Enter is a newline; only the button sends" to name Ctrl/⌘+Enter.
- [ ] **Step 2: AGENTS.md.** In the Web UI convention, add: search state and matching live in `web/src/lib/search.svelte.ts` and nothing else filters rooms; `layout.wide` (`web/src/lib/layout.svelte.ts`) is the one place the breakpoint is written, and `Home.svelte` must not remount the open screen when it flips; document-level keys go through `web/src/lib/keys.ts`; a file under `web/public/assets/` is served as immutable, so a changed one gets a new name. In "Key Directories", the `web/` row gains `public/assets/`.
- [ ] **Step 3: Spec.** Section 8's last paragraph ("Until slice 6, the phone layout is the only layout…") becomes a sentence saying the desktop layout applies from 56rem.
- [ ] **Step 4: Run.** `just check` — PASS, with no change under `cmd/` or `internal/` in `git status`.
- [ ] **Step 5: Try it against the built binary**, since the dev server sends no content security policy. `just build`, run the binary with a scratch database, one channel with two agents, one archived room, and `cmd/fakeagent` connected. With the browser's console open and showing no policy violation:
  - search finds the archived room, Enter opens it, and its settings unarchive it;
  - at 1280px the list stays beside the room, and narrowing the window with text in the composer keeps the text;
  - `/`, `Ctrl+K`, `Alt+↓`, `Alt+Shift+↓` and `Ctrl+Enter` do what the table says;
  - `@` in the channel completes, and the message wakes the agent named;
  - `⤓` saves a `.md` file whose content is the message's text. **If the policy blocks the `blob:` download, stop and raise it with Kai:** the other way is a server route, which is an API change.
  - the browser's application panel shows the manifest and all three icons loaded, and on a phone "Add to Home Screen" gives the icon and opens without browser chrome.
- [ ] **Step 6: Commit.** `docs: the web UI's search, shortcuts, sidebar, completion, download and manifest`
