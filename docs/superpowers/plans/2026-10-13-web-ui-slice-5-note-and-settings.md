# Web UI Slice 5: Note and Room Settings Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A person can read and edit their note, review what agents suggested for it, and change a room's topic, turn cap, agents, people and archive state, all from the web UI.

**Architecture:** The note is not in the event stream (the server writes no event for it), so it lives in its own small module, `lib/note.svelte.ts`, loaded whenever the store loads and emptied whenever the store is. Every note route answers with the whole note, so a write's answer simply replaces what is held, and one write runs at a time. Room settings add four acting functions to the store; each answer is applied as the `room.changed` event it duplicates, exactly as `pause` and `resume` already are. Two new screens, `Note.svelte` at `#/note` and `RoomSettings.svelte` at `#/rooms/<id>/settings`, hold their unsaved typing in drafts that are `null` until touched, so a reload or an event never overwrites it.

**Tech Stack:** Svelte 5, TypeScript 6, Vite 8; Vitest 5 with `@testing-library/svelte` on jsdom. No new npm dependency. No Go change.

**Spec:** `docs/superpowers/specs/2026-10-12-web-ui-design.md` (slice 5 of section 8; sections 2, 3, 4.5 and 4.8 apply). Wire shapes are in `docs/openapi.yaml` (`Note`, `NoteEntry`, `updateRoom`, `inviteHuman`, `addRoomAgent`, `removeRoomAgent`, `listHumans`); the note's caps and the quarantine are in the README's "The note" section. There is no mockup for either screen; they follow `NewRoom.svelte`'s sections and controls.

## Global Constraints

- No change to the JSON API or to any Go file. If one seems needed, stop and raise it with Kai.
- The page is served under `default-src 'self'`: no inline `<script>`, no `style="…"` attribute, no `{@html}`.
- An entry's text is written by an agent and is untrusted. It is drawn as plain text, never through `Markdown.svelte`.
- A human never sees another human's note. Nothing of the note is stored in the browser, and it is emptied when the stream is torn down.
- No screen fetches on a timer.
- No hand-holding copy: no helper text, no empty-state coaching, no tooltips. No confirmation for an action that can be undone. No dead controls.
- One header per screen and nothing else fixed at the top. No tab bar.
- Routes are hash-based. This slice adds `#/note` and `#/rooms/<id>/settings`. Anything unknown shows the list.
- Colours are tokens in `web/src/app.css`, never literals in components. No animation. npm dependencies stay pinned; TypeScript stays on 6.x.
- Server limits, copied from the API: note text at most 2,000 characters; an entry 1 to 300; text plus approved entries at most 4,000; topic 1 to 200; turn cap 1 to 100. Characters are code points (`[...s].length`), counted after trimming, as the server counts them.
- Run `just web-check` before each commit. Commit messages follow the repository's style (`feat(web): …`).

## Decisions this plan makes

Each is Claude's call. The four marked **(ask Kai)** change what the person sees and are worth a look before building.

| Question | Decision |
|---|---|
| Where the note is reached from | A `Note` link in the room list's footer, beside the username and Log out. The header keeps what it has. |
| Knowing that suggestions wait | The footer link carries the count of pending entries as a badge. The note is fetched on every store load (each `ready`, and the page becoming visible), which is the only way to learn of one: no event announces a suggestion. **(ask Kai)** |
| Where the note is held | `lib/note.svelte.ts`, not the store. No event touches it, so the store's holding rule has nothing to do for it. |
| Two note writes at once | Not possible: while one is in flight the others do nothing and the screen disables its controls. A load's answer is dropped if a write started after it did. |
| Entry controls | Pending: `Approve`, `Edit`, `Delete`. Approved: `Edit`, `Unapprove`, `Delete`. Unapprove is `PATCH {approved:false}`, the API's "reject", and sends the entry back to the queue; a pending suggestion the person does not want is deleted, since leaving it would hold one of the 50 quarantine places. **(ask Kai)** |
| Deleting an entry | One tap, no confirmation, though it cannot be undone. An entry is at most 300 characters. **(ask Kai)** |
| Caps in the form | No `maxlength` attribute (it counts UTF-16 units and truncates a paste silently). A counter (`1234 / 2000`, and `3100 / 4000` for what agents are given) turns to `--err` past the cap and Save is disabled. The server stays the judge; its message is shown. |
| Where settings are reached from | A `Settings` item in the room's ⋯ menu. A DM has none: the server refuses every change to one. |
| Topic and turn cap | One form, one Save, one `PATCH` carrying only the fields that changed. |
| Removing an agent | One tap. It can be added back, though its waiting copies are cancelled and its open prompts expire. The room's last agent has no Remove. |
| Inviting a person | Two taps: `Invite`, then `Confirm` or `Cancel` in the same row. It is the one action here with no way back (the API has no remove), it shows the newcomer the room's history, and it stops the note being attached in that room. |
| Archive | One tap; `Unarchive` takes its place and the person stays on the settings screen. While archived, every other control is disabled (the server answers 409 `room_archived`). |
| Getting back to an archived room | Not solved here. Archived rooms are not listed, and search is slice 6, so until then an archived room is reached only by its address. **(ask Kai: accept for one slice, or pull an "Archived" list forward.)** |
| Stop everything | `HaltControl` sits in both new headers, as it does on New room. |
| Not in this slice | Search, @mention completion, shortcuts, message download, the desktop sidebar (slice 6). |

## Review Focus

1. **A second person signs in on the same browser.** Expected: the first person's note is never drawn, including by an answer that lands after sign-out. Tests in Task 2.
2. **Typing that outlives a reload.** The phone sleeps mid-sentence and the page reloads its data on waking; or the other human renames the room while the topic is being edited. Expected: the typing stays, untouched fields follow the server. Tests in Tasks 3 and 5.
3. **Text the browser and the server count differently.** Emoji, trailing blank lines, a note at exactly 2,000, an approval that would pass 4,000. Expected: the counter agrees with the server, and a refusal shows the server's message and leaves the entry where it was. Tests in Tasks 2 and 3.
4. **A hostile entry from an agent.** `<img src=x onerror=alert(1)>`, `[x](javascript:alert(1))`, 300 characters with no space, embedded newlines. Expected: plain text that wraps inside its row, and no element made from it. Test in Task 3.
5. **The room changes under the settings screen.** The other human archives it, an agent is removed elsewhere, the address names a DM or a room the person is not in, a control is tapped twice. Expected: controls follow the room, a DM or unknown room leaves the screen, a refusal is shown, and no request is sent twice. Tests in Tasks 4 and 5.

## File Structure

```
web/src/
  lib/types.ts            + NoteEntry, Note
  lib/api.ts              + getNote, setNoteText, updateNoteEntry, deleteNoteEntry,
                            listHumans, updateRoom, inviteHuman, addRoomAgent, removeRoomAgent
  lib/fixtures.ts         + human(), noteEntry(), note()
  lib/note.svelte.ts      the note held, its load, its five writes, the character counts   (new)
  lib/store.svelte.ts     load() and disconnect() reach the note; + changeRoom, addAgent,
                          removeAgent, invite; setPaused becomes roomWrite
  lib/route.svelte.ts     + settingsIdOf
  Note.svelte             the note screen                                                  (new)
  NoteEntry.svelte        one entry: who, when, text, its controls, its edit box           (new)
  RoomSettings.svelte     the settings screen                                              (new)
  RoomMenu.svelte         + Settings item
  Home.svelte             + the two routes, the footer's Note link and badge
  (a *.test.ts beside each)
README.md, AGENTS.md
```

---

### Task 1: Wire shapes and the calls

**Files:**
- Modify: `web/src/lib/types.ts`, `web/src/lib/api.ts`, `web/src/lib/api.test.ts`, `web/src/lib/fixtures.ts`

**Interfaces:**
- Produces, from `lib/types.ts`:

```ts
export type NoteEntry = { id: string; agent: Agent; text: string; approved: boolean; created_at: string };
export type Note = { text: string; entries: NoteEntry[] };   // entries oldest first, pending and approved
```

- Produces, from `lib/api.ts` (every id is passed through `encodeURIComponent`):

```ts
export function getNote(): Promise<Note>                                     // GET /api/note
export function setNoteText(text: string): Promise<Note>                     // PUT /api/note {text}
export function updateNoteEntry(entryId: string, changes: { text?: string; approved?: boolean }): Promise<Note>  // PATCH /api/note/entries/{id}
export function deleteNoteEntry(entryId: string): Promise<Note>              // DELETE /api/note/entries/{id}
export function listHumans(): Promise<Human[]>                               // GET /api/humans, unwraps {humans}
export type RoomChanges = { topic?: string; archived?: boolean; turn_cap?: number };
export function updateRoom(roomId: string, changes: RoomChanges): Promise<Room>        // PATCH /api/rooms/{id}
export function inviteHuman(roomId: string, humanId: string): Promise<Room>            // POST …/humans {human_id}
export function addRoomAgent(roomId: string, agentId: string): Promise<Room>           // POST …/agents {agent_id}
export function removeRoomAgent(roomId: string, agentId: string): Promise<Room>        // DELETE …/agents/{agent}
```

- Produces, from `lib/fixtures.ts`: `human(overrides?: Partial<Human>): Human` (`{id:'human-1', username:'kai'}`), `noteEntry(overrides?: Partial<NoteEntry>): NoteEntry` (id `'entry-1'`, `agent()` narrowed to the four `Agent` fields, text `'likes tea'`, `approved: false`, `created_at: '2026-01-01T00:00:00.000Z'`), `note(overrides?: Partial<Note>): Note` (`{text:'', entries:[]}`).

- [ ] **Step 1: Write the failing tests** in `api.test.ts`, in the file's style, one per function: method, path, body, and the unwrapped value.

```ts
test('updateNoteEntry patches only what it is given', async () => {
  const fetch = stubFetch(200, { text: '', entries: [] });
  await expect(updateNoteEntry('01E', { approved: true })).resolves.toEqual({ text: '', entries: [] });
  expect(fetch).toHaveBeenCalledWith('/api/note/entries/01E', expect.objectContaining({ method: 'PATCH', body: '{"approved":true}' }));
});

test('updateRoom patches the room', async () => {
  const fetch = stubFetch(200, { id: '01R' });
  await expect(updateRoom('01R', { turn_cap: 12 })).resolves.toEqual({ id: '01R' });
  expect(fetch).toHaveBeenCalledWith('/api/rooms/01R', expect.objectContaining({ method: 'PATCH', body: '{"turn_cap":12}' }));
});
```

  And, by name: `getNote`, `setNoteText puts the text` (body `{"text":""}` for an empty one), `deleteNoteEntry`, `listHumans unwraps humans`, `inviteHuman posts the human`, `addRoomAgent posts the agent`, `removeRoomAgent deletes the agent and encodes both ids` (`removeRoomAgent('a/b', 'c d')` asks for `/api/rooms/a%2Fb/agents/c%20d`).

- [ ] **Step 2: Run.** `cd web && npx vitest run src/lib/api.test.ts` — FAIL, the functions are not exported.
- [ ] **Step 3: Implement** the two types, the nine functions and the three builders.
- [ ] **Step 4: Run.** `just web-check` — PASS.
- [ ] **Step 5: Commit.** `feat(web): the note and room-settings calls`

---

### Task 2: The note, held

**Files:**
- Create: `web/src/lib/note.svelte.ts`, `web/src/lib/note.test.ts`
- Modify: `web/src/lib/store.svelte.ts`, `web/src/lib/store.test.ts`

**Interfaces:**
- Consumes: `Note`, `NoteEntry` and the four note calls from Task 1; `reason` is **not** imported from the store (the store imports this module). Copy the rule: an `ApiError` with status 401 gives `''`, any other error its `message`.
- Produces, from `lib/note.svelte.ts`:

```ts
export const maxText = 2000;      // the person's own text
export const maxEntry = 300;      // one entry
export const maxApproved = 4000;  // the text plus every approved entry

type Held = {
  value: Note | null;   // null until the first load succeeds
  error: string;        // why the last load failed; '' when none
  busy: boolean;        // a write is in flight
};
export const note: Held;          // $state

export function chars(text: string): number;                    // code points of text.trim()
export function approvedChars(n: Note, text?: string): number;  // chars(text ?? n.text) + chars of each approved entry
export function pending(n: Note | null): NoteEntry[];           // entries with approved false, in order; [] for null

export function loadNote(): Promise<void>;                      // never rejects
export function resetNote(): void;                              // empties note; late answers are dropped

// Each write replaces note.value with the answer. Each rejects with the
// ApiError. A call made while note.busy is true does nothing and resolves.
export function saveText(text: string): Promise<void>;
export function approve(entryId: string): Promise<void>;        // updateNoteEntry(id, {approved: true})
export function unapprove(entryId: string): Promise<void>;      // updateNoteEntry(id, {approved: false})
export function editEntry(entryId: string, text: string): Promise<void>;
export function removeEntry(entryId: string): Promise<void>;
```

- Produces, in `lib/store.svelte.ts`: `load()` starts `void loadNote()` on every call, outside its `Promise.all` and outside `fetchStarted`/`fetchSettled`; `disconnect()` calls `resetNote()`.

Rules:

- Three counters, in the store's manner: `loads` (only the newest load writes), `writes` (bumped when a write starts; a load whose `writes` has moved since it started writes nothing), `resets` (bumped by `resetNote`; anything in flight across one writes nothing and leaves `busy` false).
- A failed load sets `error` and leaves `value` as it was. A successful one clears `error`.
- A failed write leaves `value` as it was, clears `busy`, and rejects.

- [ ] **Step 1: Write the failing tests** in `note.test.ts`. Mock `./api` as `store.test.ts` does; `beforeEach` calls `resetNote()` and resets the four mocks. In every test file of this plan the builder is imported as `note as fixture`, since `note` is the held state.

```ts
test('chars counts code points after trimming', () => {
  expect(chars('  héllo 👍\n\n')).toBe(7);
});

test('approvedChars adds the text to the approved entries only', () => {
  const n = fixture({ text: 'abc', entries: [noteEntry({ id: 'a', text: 'four', approved: true }), noteEntry({ id: 'b', text: 'ignored' })] });
  expect(approvedChars(n)).toBe(7);
  expect(approvedChars(n, 'a longer draft')).toBe(18);
});

test('a note answered after a reset is not held', async () => {
  const slow = deferred<Note>();
  vi.mocked(api.getNote).mockReturnValueOnce(slow.promise);
  const loading = loadNote();
  resetNote();
  slow.resolve(fixture({ text: 'the first person’s' }));
  await loading;
  expect(note.value).toBeNull();
});

test('a write answered after a reset is not held and leaves nothing busy', async () => {
  note.value = fixture();
  const slow = deferred<Note>();
  vi.mocked(api.setNoteText).mockReturnValueOnce(slow.promise);
  const saving = saveText('mine');
  resetNote();
  slow.resolve(fixture({ text: 'mine' }));
  await saving;
  expect(note.value).toBeNull();
  expect(note.busy).toBe(false);
});

test('a load that started before a write does not undo it', async () => {
  const slow = deferred<Note>();
  vi.mocked(api.getNote).mockReturnValueOnce(slow.promise);
  const loading = loadNote();
  vi.mocked(api.setNoteText).mockResolvedValue(fixture({ text: 'new' }));
  await saveText('new');
  slow.resolve(fixture({ text: 'old' }));
  await loading;
  expect(note.value?.text).toBe('new');
});

test('a second write while one is in flight asks for nothing', async () => {
  const slow = deferred<Note>();
  vi.mocked(api.updateNoteEntry).mockReturnValueOnce(slow.promise);
  const first = approve('a');
  await removeEntry('b');
  expect(api.deleteNoteEntry).not.toHaveBeenCalled();
  slow.resolve(fixture());
  await first;
});

test('a refused approval rejects and changes nothing', async () => {
  const before = fixture({ entries: [noteEntry({ id: 'a' })] });
  note.value = before;
  const refusal = new api.ApiError(400, 'validation', 'the approved note would exceed 4000 characters; shorten or delete something first');
  vi.mocked(api.updateNoteEntry).mockRejectedValue(refusal);
  await expect(approve('a')).rejects.toBe(refusal);
  expect(note.value).toEqual(before);
  expect(note.busy).toBe(false);
});
```

  And, by name: `loadNote holds the note and clears the error`, `a failed load records why and keeps the note held`, `a 401 records nothing`, `the newer of two loads writes`, `pending gives the entries not yet approved, in order`, and one per write naming the call it makes (`saveText`, `approve`, `unapprove`, `editEntry`, `removeEntry`).

  In `store.test.ts`: add `getNote: vi.fn()` to the mock and `vi.mocked(api.getNote).mockReset().mockResolvedValue(fixture())` to `beforeEach`, then:
  - `a load fetches the note` — after `ready`, `note.value` equals what `getNote` answered.
  - `a note that cannot be fetched does not fail the load` — `getNote` rejects; `store.loaded` is true and `store.error` is `''`.
  - `disconnect empties the note` — set `note.value`, call `disconnect()`, expect `null`.

- [ ] **Step 2: Run.** `npx vitest run src/lib/note.test.ts src/lib/store.test.ts` — FAIL, module not found.
- [ ] **Step 3: Implement** `note.svelte.ts` and the two lines in the store.
- [ ] **Step 4: Run.** `just web-check` — PASS.
- [ ] **Step 5: Commit.** `feat(web): the note is held and follows each load`

---

### Task 3: The note screen

**Files:**
- Create: `web/src/Note.svelte`, `web/src/Note.test.ts`, `web/src/NoteEntry.svelte`, `web/src/NoteEntry.test.ts`
- Modify: `web/src/Home.svelte`, `web/src/Home.test.ts`

**Interfaces:**
- Consumes: everything Task 2 produces; `Icon.svelte` (`name` is the agent's handle); `clock(iso, now)` from `lib/format.ts`; `reason` from the store; `Header.svelte`, `HaltControl.svelte`.
- Produces: `NoteEntry.svelte` with props `{ entry: NoteEntry; disabled: boolean; onerror: (why: string) => void }`. `Note.svelte` takes no props. `Home.svelte` draws `Note` at `route.path === '/note'`.

`Note.svelte`, top to bottom:

- `<Header title="Note" back="#/">` holding `HaltControl`.
- `note.error`, as `role="alert"` with a `Retry` button that calls `loadNote`. Then the screen's own `problem` (the last refused write), as `role="alert"` with `Dismiss`, as `Room.svelte` does.
- Nothing more until `note.value` is not null.
- **Text.** A `<textarea aria-label="Note">`. `let draft = $state<string | null>(null)`; the box shows `draft ?? note.value.text`, and typing sets `draft`. Under it a counter `{chars(shown)} / 2000` and a `Save` button, disabled unless `draft !== null`, `draft.trim() !== note.value.text`, the count is within `maxText`, and `note.busy` is false. Save calls `saveText(draft)`; on success `draft = null`, on failure the draft stays and `problem = reason(err)`.
- **To review** (`<h2>`), only when `pending(note.value)` is not empty: one `NoteEntry` each.
- **Approved** (`<h2>`), only when there is an approved entry: one `NoteEntry` each.
- A last line, `{approvedChars(note.value, shown)} / 4000`, with class `over` (colour `--err`) past `maxApproved`. The text's own counter takes the same class past `maxText`.

`NoteEntry.svelte`:

- The agent's icon, `display_name`, and `clock(entry.created_at, new Date())`; then the text in a `<p>` with `white-space: pre-wrap; overflow-wrap: anywhere`.
- Buttons: `Approve` (pending only), `Unapprove` (approved only), `Edit`, `Delete`. Every one is disabled when `disabled` is true. Each calls its function from `lib/note.svelte.ts`; a rejection goes to `onerror(reason(err))`, a success to `onerror('')`.
- `Edit` swaps the text for a `<textarea aria-label="Entry">` holding it, a counter `{n} / 300`, and `Save` and `Cancel`. Save is disabled when the trimmed text is empty, unchanged or over `maxEntry`. A successful save, or Cancel, leaves edit mode; a refused save stays in it with the typing kept.

`Home.svelte`: the footer gains, before the username, `<a class="note" href="#/note">Note</a>`, followed by `<span class="badge">{n}</span>` inside the link when `pending(note.value).length` is above zero. The badge uses `--ask` and `--on-ask`, as the room list's prompt badge does.

- [ ] **Step 1: Write the failing tests.** Mock `./lib/api` (the four note calls) and `./lib/stream`, as `RoomMenu.test.ts` does; `beforeEach` calls `disconnect()`.

  `Note.test.ts`:

```ts
test('typing survives a reload of the note', async () => {
  note.value = fixture({ text: 'old' });
  render(Note);
  const box = screen.getByRole('textbox', { name: 'Note' });
  await userEvent.clear(box);
  await userEvent.type(box, 'half a sentence');
  vi.mocked(api.getNote).mockResolvedValue(fixture({ text: 'changed elsewhere' }));
  await loadNote();
  expect(box).toHaveValue('half a sentence');
});

test('an untouched box follows a reload', async () => {
  note.value = fixture({ text: 'old' });
  render(Note);
  vi.mocked(api.getNote).mockResolvedValue(fixture({ text: 'changed elsewhere' }));
  await loadNote();
  await waitFor(() => expect(screen.getByRole('textbox', { name: 'Note' })).toHaveValue('changed elsewhere'));
});

test('a refused save shows the server’s words and keeps the typing', async () => {
  note.value = fixture();
  vi.mocked(api.setNoteText).mockRejectedValue(new api.ApiError(400, 'validation', 'the approved note would exceed 4000 characters; shorten or delete something first'));
  render(Note);
  await userEvent.type(screen.getByRole('textbox', { name: 'Note' }), 'mine');
  await userEvent.click(screen.getByRole('button', { name: 'Save' }));
  expect(await screen.findByRole('alert')).toHaveTextContent('would exceed 4000 characters');
  expect(screen.getByRole('textbox', { name: 'Note' })).toHaveValue('mine');
});
```

  And, by name:
  - `Save is disabled until the text differs` — also still disabled after typing only trailing spaces.
  - `a text of 2,000 characters can be saved and one of 2,001 cannot` — set through `fireEvent.input`; at 2,001 Save is disabled and the counter has class `over`. Use `'👍'.repeat(2000)` for the passing case, which is 4,000 UTF-16 units.
  - `a saved text clears the draft` — `setNoteText` called with the typed text; Save disabled again.
  - `pending entries sit under To review and approved ones under Approved` — neither heading is drawn when its list is empty.
  - `the total counts the draft and the approved entries`.
  - `a load that failed shows why and Retry loads again`.
  - `every control is disabled while a write is in flight` — a deferred `updateNoteEntry`; the textarea's Save and the other entries' buttons are disabled until it settles.

  `NoteEntry.test.ts`:
  - `a pending entry offers Approve, Edit and Delete`, `an approved entry offers Unapprove, Edit and Delete`.
  - `Approve`, `Unapprove` and `Delete` each make their call with the entry's id.
  - `Edit saves the trimmed text` and `Cancel leaves the entry as it was` (no call).
  - `a refused edit stays open with the typing and reports why` — `onerror` called with the server's message.
  - `an entry of 301 characters cannot be saved`.
  - `a hostile entry is text` — render the four Review Focus 4 texts; `container.querySelector('img, a, script, [onerror]')` is null and the literal `<img src=x onerror=alert(1)>` is in `textContent`.

  `Home.test.ts`:
  - `the footer links to the note` — `getByRole('link', { name: /Note/ })` has `href="#/note"`.
  - `the note link counts what waits for review` — two pending and one approved entry show `2`; none pending shows no badge.
  - `#/note shows the note screen` — set the hash, expect the `Note` heading.

- [ ] **Step 2: Run.** `npx vitest run src/Note.test.ts src/NoteEntry.test.ts src/Home.test.ts` — FAIL, components not found.
- [ ] **Step 3: Implement** the two components and the changes to `Home.svelte`.
- [ ] **Step 4: Run.** `just web-check` — PASS.
- [ ] **Step 5: Commit.** `feat(web): the note and its review queue`

---

### Task 4: The store changes a room

**Files:**
- Modify: `web/src/lib/store.svelte.ts`, `web/src/lib/store.test.ts`, `web/src/lib/route.svelte.ts`, `web/src/lib/route.test.ts`

**Interfaces:**
- Consumes: `updateRoom`, `inviteHuman`, `addRoomAgent`, `removeRoomAgent`, `RoomChanges` from Task 1.
- Produces, from `lib/store.svelte.ts`:

```ts
export function changeRoom(roomId: string, changes: RoomChanges): Promise<void>;
export function addAgent(roomId: string, agentId: string): Promise<void>;
export function removeAgent(roomId: string, agentId: string): Promise<void>;
export function invite(roomId: string, humanId: string): Promise<void>;
```

  Each makes its call and hands the answer's room to `receive('room.changed', { room })`, unless `disconnect()` ran meanwhile. Each rejects with the `ApiError`. The private `setPaused(roomId, call)` becomes `roomWrite(call: () => Promise<Room>)`, and `pause` and `resume` use it too.

- Produces, from `lib/route.svelte.ts`:

```ts
// settingsIdOf gives the id in '/rooms/<id>/settings', and null for any other path.
export function settingsIdOf(path: string): string | null;
```

- [ ] **Step 1: Write the failing tests.** In `store.test.ts`, add the four calls to the mock and to `beforeEach`.

```ts
test('changeRoom writes the answer as the room’s event', async () => {
  await ready([room({ id: '01R', topic: 'Old' })]);
  vi.mocked(api.updateRoom).mockResolvedValue(room({ id: '01R', topic: 'New' }));
  await changeRoom('01R', { topic: 'New' });
  expect(api.updateRoom).toHaveBeenCalledWith('01R', { topic: 'New' });
  expect(store.rooms['01R'].topic).toBe('New');
});
```

  (`ready` stands for whatever helper the file already uses to bring the store to a loaded state with given rooms.) And, by name:
  - `removeAgent drops the agent from the room` and `addAgent`, `invite`: the call made, the room replaced.
  - `a refused change rejects and leaves the room alone` — 409 `room_archived`.
  - `a change answered after disconnect writes nothing`.
  - `a change answered while a load is in flight is not undone by the load` — start `load()` with a deferred `listRooms` holding the old topic, settle `changeRoom` with the new one, then settle the load: the topic is the new one. (The holding rule already gives this; the test pins it for the new functions.)
  - `a change keeps the read marker the page has put` — the answer carries an older `last_read_message_id` than the held room; the held one stays.

  In `route.test.ts`: `settingsIdOf('/rooms/01R/settings')` is `'01R'`; `'/rooms/01R'`, `'/rooms//settings'`, `'/rooms/01R/settings/x'` and `'/note'` are null; and `roomIdOf('/rooms/01R/settings')` stays null.

- [ ] **Step 2: Run.** `npx vitest run src/lib/store.test.ts src/lib/route.test.ts` — FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run.** `just web-check` — PASS.
- [ ] **Step 5: Commit.** `feat(web): the store renames, archives and changes who is in a room`

---

### Task 5: The settings screen

**Files:**
- Create: `web/src/RoomSettings.svelte`, `web/src/RoomSettings.test.ts`
- Modify: `web/src/RoomMenu.svelte`, `web/src/RoomMenu.test.ts`, `web/src/Home.svelte`, `web/src/Home.test.ts`

**Interfaces:**
- Consumes: the four functions and `settingsIdOf` from Task 4; `listHumans` from Task 1; `store`, `reason`, `load`; `byName` from `lib/rooms.ts`; `navigate`, `replace` from the router; `session.me`; `Icon.svelte`, `Header.svelte`, `HaltControl.svelte`.
- Produces: `RoomSettings.svelte` with props `{ id: string }`. `Home.svelte` draws it, keyed by id, when `settingsIdOf(route.path)` is not null. `RoomMenu.svelte` gains the item.

`RoomMenu.svelte`: after Pause or Resume, when the room is held and its flavor is not `dm`, `<a role="menuitem" href="#/rooms/{id}/settings">Settings</a>`, styled as the button beside it. Tapping it closes the menu. Escape on it closes the menu, as on the button.

`RoomSettings.svelte`:

- `room` is read from `store.rooms` with `Object.hasOwn`, as `Room.svelte` does. Once `store.loaded`: no room → `navigate('/')`; a DM → `replace('/rooms/' + id)`.
- `<Header title="Settings" sub={room.topic} back="#/rooms/{id}">` holding `HaltControl`. Then `store.error` with `Retry`, and the screen's own `problem` with `Dismiss`, as `Room.svelte` draws them.
- One `busy` flag for the screen: every control is disabled while a write is in flight. A success clears `problem`; a failure sets it to `reason(err)`.
- `frozen` is `room.archived`. While frozen every control but `Unarchive` is disabled.
- **Topic and turn cap**, one `<form>`. `topicDraft` and `capDraft` are `string | null`, null until typed in; each input shows its draft, or the room's value. `Topic` is a text input; `Turn cap` is `<input type="number" min="1" max="100" inputmode="numeric">`. `Save` is enabled when at least one draft differs from the room (topic compared after trimming), the topic is not blank, and the cap is a whole number from 1 to 100. It calls `changeRoom(id, changes)` with only the fields that differ, and on success sets both drafts to null.
- **Agents** (`<h2>`). The room's agents by `display_name` (`byName`), each with icon, name and `Remove`; no Remove is drawn when the room has one agent. Then every agent in `store.agents` that is not revoked and not in the room, each with `Add`. A revoked agent still in the room is listed, with Remove.
- **People** (`<h2>`). The room's humans by username. Then, from `listHumans()` called once on mount, everyone not in the room, each with `Invite`. Tapping Invite arms that row: it shows `Confirm` and `Cancel` instead, and arming another row disarms the first. Confirm calls `invite`. A failed `listHumans` sets `problem` and lists nobody to invite.
- **Archive.** One button, `Archive` or `Unarchive`, calling `changeRoom(id, { archived: !room.archived })`. The person stays on the screen.

- [ ] **Step 1: Write the failing tests.** Mock the four room calls and `listHumans` in `./lib/api`, and `./lib/stream`; `beforeEach` calls `disconnect()`, sets `store.loaded = true`, and gives `listHumans` `[human()]`.

```ts
test('Save sends only what changed', async () => {
  store.rooms['01R'] = room({ id: '01R', topic: 'Old', turn_cap: 6 });
  vi.mocked(api.updateRoom).mockResolvedValue(room({ id: '01R', topic: 'Old', turn_cap: 12 }));
  render(RoomSettings, { id: '01R' });
  const cap = screen.getByRole('spinbutton', { name: 'Turn cap' });
  await userEvent.clear(cap);
  await userEvent.type(cap, '12');
  await userEvent.click(screen.getByRole('button', { name: 'Save' }));
  expect(api.updateRoom).toHaveBeenCalledWith('01R', { turn_cap: 12 });
});

test('a topic being typed survives the room changing', async () => {
  store.rooms['01R'] = room({ id: '01R', topic: 'Old', turn_cap: 6 });
  render(RoomSettings, { id: '01R' });
  const topic = screen.getByRole('textbox', { name: 'Topic' });
  await userEvent.clear(topic);
  await userEvent.type(topic, 'Mine');
  store.rooms['01R'] = room({ id: '01R', topic: 'Theirs', turn_cap: 9 });
  await waitFor(() => expect(screen.getByRole('spinbutton', { name: 'Turn cap' })).toHaveValue(9));
  expect(topic).toHaveValue('Mine');
});

test('inviting takes a second tap', async () => {
  store.rooms['01R'] = room({ id: '01R', humans: [human()] });
  vi.mocked(api.listHumans).mockResolvedValue([human(), human({ id: '01H2', username: 'sam' })]);
  vi.mocked(api.inviteHuman).mockResolvedValue(room({ id: '01R', humans: [human(), human({ id: '01H2', username: 'sam' })] }));
  render(RoomSettings, { id: '01R' });
  await userEvent.click(await screen.findByRole('button', { name: 'Invite' }));
  expect(api.inviteHuman).not.toHaveBeenCalled();
  await userEvent.click(screen.getByRole('button', { name: 'Confirm' }));
  expect(api.inviteHuman).toHaveBeenCalledWith('01R', '01H2');
  await waitFor(() => expect(screen.queryByRole('button', { name: 'Invite' })).toBeNull());
});
```

  And, by name:
  - `Save is disabled for a blank topic, a cap of 0, 101 or 2.5, and when nothing differs`.
  - `a topic that differs only by surrounding spaces is not a change`.
  - `Cancel disarms an invite` — no call.
  - `the only agent has no Remove`, `Remove asks once however often it is tapped` (a deferred answer, two clicks, one call), `Add lists agents not in the room and leaves out revoked ones`.
  - `Archive becomes Unarchive and the screen stays` — `route.path` unchanged.
  - `an archived room disables everything but Unarchive`.
  - `a room archived by someone else freezes the screen` — replace `store.rooms['01R']` with an archived one after render.
  - `a refusal is shown and Dismiss clears it` — `removeRoomAgent` rejects with 409 `room_archived`.
  - `a DM’s settings address goes to the DM` and `an unknown room’s goes to the list` — check `location.hash`.
  - `people who cannot be listed leave nobody to invite and say why`.

  `RoomMenu.test.ts`: `a room’s menu links to its settings` (href `#/rooms/01R/settings`), `a DM’s menu has no Settings`. `Home.test.ts`: `#/rooms/<id>/settings shows the settings screen`.

- [ ] **Step 2: Run.** `npx vitest run src/RoomSettings.test.ts src/RoomMenu.test.ts src/Home.test.ts` — FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run.** `just web-check` — PASS.
- [ ] **Step 5: Commit.** `feat(web): room settings: topic, turn cap, agents, people and archive`

---

### Task 6: Documents and the whole check

**Files:**
- Modify: `README.md` (the "Web UI" section), `AGENTS.md` (the "Web UI" convention)

- [ ] **Step 1: README.** In "Web UI", add the two routes to the sentence that lists them, and one paragraph each for the note screen and the settings screen, stating what this plan's decision table decided as it was built: where each is reached from, the pending badge and when the note is fetched, the entry controls, the counters, the one-write-at-a-time rule, what a DM and an archived room allow, and the two-tap invite. Say that an archived room is reached only by its address until search exists.
- [ ] **Step 2: AGENTS.md.** In the Web UI convention: the route list gains `#/note` and `#/rooms/<id>/settings`; the acting functions gain `changeRoom`, `addAgent`, `removeAgent`, `invite`; and one sentence says the note is held in `web/src/lib/note.svelte.ts` outside the store because no event carries it, is fetched by every `load()`, emptied by `disconnect()`, and that an entry's text is drawn as plain text.
- [ ] **Step 3: Run.** `just check` — PASS, with no change under `cmd/` or `internal/` in `git status`.
- [ ] **Step 4: Try it.** `just web-build`, run the server against a scratch database with one agent and two humans, and with `cmd/fakeagent` connected: save a note, rename a room and see the list reorder, remove and re-add the agent, invite the second human and see the room in their list, archive and unarchive. A note suggestion needs an agent with an open turn (`POST /agent/deliveries/{message}/note-entries` with the agent's token); make one with `curl` and see the footer badge after the page next becomes visible.
- [ ] **Step 5: Commit.** `docs: the web UI's note and room settings`
