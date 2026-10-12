# Hermes Chat web UI: design

Date: 2026-10-12
Status: draft for approval

This document covers the web UI only. The product it belongs to is described in
`2026-10-11-hermes-chat-design.md` (the product spec), and the server it talks
to in `2026-10-11-chat-server-architecture-design.md` and `docs/openapi.yaml`.
Where this document and section 8 of the product spec disagree, this one wins.

Decisions are marked *(Kai)* when Kai made them and *(Claude's call)* when
Claude chose and Kai has not objected. Change a Claude's call freely if there
is a reason.

## 1. Intent

Kai uses Hermes Chat to check on deep-research results, ask follow-up
questions, ask general questions, and have an agent do lightweight programming
tasks. The pattern is: start something, leave, come back, read what happened.
The UI exists to make coming back quick: see at a glance which rooms have
something new or are waiting on an answer, and get to them in one tap.

The users are one or two people who use it every day. It is built for them,
not for someone seeing it for the first time.

## 2. Goals, non-goals, anti-goals

### Goals

- Everything a human can do through the human API is reachable from the UI
  (product spec section 7, "What humans can do in the web UI").
- The room list never rearranges itself when messages arrive.
- Nothing new is missed, even in a room that is scrolled out of view or inside
  a collapsed group.
- Works well on a phone and on a desktop browser.
- Small: the framework compiles away, and the page makes no request to any
  other origin.

### Non-goals

- Push or browser notifications. Unread indicators are the whole mechanism.
  *(Kai)*
- Search over message text. Search covers room and agent names only.
- Offline use. There is no service worker in this version. *(Claude's call)*
- Uploading files from the browser. The server has no human upload route yet.
- A specific typeface, a final colour system, or animation polish. Kai expects
  to adjust the look later; structure matters more than finish now. *(Kai)*
- Anything the product spec lists as a non-goal (threads, reactions, editing
  sent messages, typing indicators, managing accounts from the UI).

### Anti-goals

- **Sorting by recency.** A room must not change position because something
  was posted in it. *(Kai)*
- **Hand-holding.** No tours, tooltips that explain the obvious, coaching text
  in empty states, or confirmation dialogs for actions that can be undone.
  *(Kai: "we are targeting power users, not casual ones")*
- **A persistent tab bar or any other fixed strip beyond one header.** Vertical
  space on a phone is precious. *(Kai)*
- **React, or a virtual-DOM model that makes the developer manage state to
  serve the renderer.** *(Kai; see section 6)*
- **Any third-party request from the browser**, including fonts.

## 3. Constraints

- Built output is static files embedded in the Go binary and served by it.
- Strict Content-Security-Policy: `default-src 'self'`, no inline script.
- Sessions use the existing cookie; state-changing requests already pass the
  server's same-origin check when made from the served page.
- Agent text is untrusted (product spec 6.4): markdown rendered with raw HTML
  disabled and the result sanitized, links open in a new tab with
  `rel="noopener noreferrer"`, remote images are never loaded automatically.
- `go test ./...` and `go build` must keep working on a fresh clone that has
  no Node installed and has never built the UI.

## 4. Screens

Mockups Kai reviewed are in `.superpowers/brainstorm/webui-2026-10-12/content/`
(git-ignored). `icons-v6.html` is the latest and shows the room list, the
Unread page, a room, and the generated icons. The expanded activity block is in
`.superpowers/brainstorm/10263-1791735561/content/run-in-progress.html`. The
mockups fix layout and behaviour, not exact colours or spacing.

### 4.1 Layout

- **Phone:** one screen at a time. Room list → room, or room list → Unread →
  room, with a back control in the header.
- **Desktop:** the room list is a left sidebar that stays visible; the room,
  the Unread page, the note, and settings open in the main pane. *(Claude's
  call)*
- One header per screen and nothing else fixed at the top.

### 4.2 Room list

- Two groups, in this order: **Direct messages**, then **Rooms**
  (conversations and channels together). *(Kai)*
- Each group is a collapsible section. Which groups are collapsed is
  remembered in that browser. *(Kai for the accordion; Claude's call for
  remembering it)*
- Inside a group, rows are sorted alphabetically, case-insensitively: DMs by
  the agent's display name, rooms by topic. A room moves only when it is
  renamed. *(Kai)*
- Rows are single-line and dense. A row shows:
  - DMs: the agent's icon (4.7) with a connected/offline dot, and its name.
  - Rooms: `#` and the topic.
  - An unread dot, and the name in bold, when the room has unread messages.
  - A badge with the count of open prompts, a "working" badge when any of the
    room's agents is working, and a "paused" badge.
- A collapsed group shows an unread dot on its header when any room inside it
  is unread. *(Claude's call)*
- Archived rooms are not listed and never count as unread; they are reached
  through search (4.6). *(Claude's call)*
- The header holds the app name, a search icon, a new-room control, and
  **Stop everything**. While everything is stopped, that control becomes the
  way to lift it, and the state is visible on every screen. *(Claude's call for
  the placement)*

### 4.3 Unread

- **Unread state comes from the server.** A room is unread when its
  `last_message_id` differs from the caller's `last_read_message_id`. Nothing
  about read state is stored in the browser, so it follows the person between
  devices. *(Kai, once told the server already keeps it)*
- **The Unread row** is the first row of the room list, above both groups,
  styled apart from the rooms. It shows the number of unread rooms, their
  names, and a badge if any of them has an open prompt. It is shown only when
  at least one room is unread; with nothing unread it is absent and the list
  starts with the first group. *(Kai)*
- **The Unread page** opens when that row is tapped. One full-width row per
  unread room: the room's name, how long ago its newest message arrived, and
  the start of that newest message (author, then text, cut to two lines).
  Tapping a row opens the room. *(Kai)*
- Order on the Unread page: rooms with an open prompt first, then newest
  first. This is the only place recency orders anything. *(Claude's call)*
- **Mark all read** is on the Unread page and sets every unread room's marker
  to its newest message. *(Kai asked for it before server-side state was on
  the table; Claude kept it for clearing a backlog)*
- A room's marker advances when the room is open, the page is visible, and its
  newest message has been scrolled into view. *(Claude's call)*
- Opening a room with unread messages positions the feed at the first unread
  message, with a divider above it, so a long report is read from its start.
  Opening a room with nothing unread positions it at the bottom. *(Claude's
  call)*

### 4.4 Room

As in the product spec, with these specifics:

- **Header:** topic (or the agent's name for a DM), a line saying which agents
  are working, idle, or offline, **Stop** while a turn is running, and a menu
  for pause/resume and room settings.
- **Feed:** messages are flat and left-aligned, each with the author's icon,
  name, and time, in the manner of Slack, not chat bubbles. *(Claude's call;
  Kai named Slack as the reference point)*
- A human's message shows the state of each agent's copy (`waiting`,
  `delivered`, `working`, `done`, `failed`, `cancelled`), with **Cancel** while
  waiting.
- Reply-to shows a short quote of the message replied to.
- A message that is still streaming updates in place.
- **Code blocks** are monospace and scroll sideways instead of wrapping.
  *(Claude's call)* Programming tasks need nothing else special: a finished
  task is a message, and a blocker is a prompt. *(Kai)*
- Attachments: images of a safe type show inline from the server; everything
  else is a download link.
- Any message can be downloaded as a `.md` file.
- **Activity block**, one per turn, collapsed by default, as in the approved
  mockup: working indicator, step count, current step; expanded, each tool
  step with preview, duration, and success or failure, arguments on tap,
  remarks inline.
- **Prompt card** for approvals and clarifying questions: the question, the
  choices as buttons, and answered and expired states with the buttons
  disabled.
- **Composer:** text, reply-to, @mention completion in channels, and a choice
  between queueing and steering while a turn is running. Posts and steers
  carry a `client_id` so a retry cannot post twice.
- A paused room, and stop-everything, are stated in the room, with the reason
  when the turn cap paused it.

### 4.5 Other screens

- **Login.** Username and password; shows the server's throttling message
  when it applies.
- **New room / open a DM.** Topic and one or more agents, or pick an agent to
  open its DM.
- **Room settings.** Topic, agents, inviting the other human, turn cap,
  archive.
- **Note.** The human's own text, approved agent entries, and the quarantine
  queue with approve, reject, edit, and delete.

### 4.6 Search

A filter over names, done entirely in the browser from the room list already
loaded. *(Kai asked how Claude would do it; this is Claude's proposal)*

- Opened from the search icon in the header; it takes no space until then. On
  desktop a keyboard shortcut opens it.
- Matches room topics, and agent display names and handles, so an agent's name
  finds its DM and every room it is in.
- The list narrows as the person types, ignoring collapsed groups. Enter opens
  the first match.
- Archived rooms appear in results, marked as archived.

### 4.7 Generated icons

Every human and agent has an icon generated in the browser from their name.
*(Kai)*

- A 5×5 grid of filled and empty cells, mirrored left to right, in the manner
  of GitHub's default avatars.
- One colour per icon, chosen by the hash from a fixed palette of 16 clearly
  different colours, on a pale tint of the same colour. *(Kai: 16 fixed
  colours, not an arbitrary hue)*
- The input is the agent's handle or the human's username, so the icon is the
  same on every device and nothing is stored. *(Claude's call for which field;
  a renamed handle gets a new icon)* Two names can land on the same colour;
  the pattern still tells them apart.
- Drawn as inline SVG with fill attributes, which the content security policy
  allows.
- Used beside DMs in the room list, beside each message, and beside the author
  on the Unread page. Rooms use `#`.

### 4.8 Look and feel

- **Aubergine and sans-serif.** *(Kai)* Aubergine is used for navigation
  surfaces (headers and the room list); anything read at length sits on a
  neutral background. *(Claude's call)*
- The system sans-serif stack, so no font files are shipped. *(Claude's call)*
- Light and dark themes that follow the system setting. *(Claude's call)*
- Dense by default: compact rows, short labels, state shown as small badges.
- Keyboard shortcuts on desktop for search, moving between rooms, and sending.
  *(Claude's call)*

## 5. Data flow

- On start: open `GET /api/events`, wait for `ready`, then load `/api/me`,
  rooms, agents, and the halt state, as the README's "Event stream" section
  prescribes. Reconnection uses the last event ID.
- One in-memory store holds rooms, agents, and the messages of rooms that have
  been opened. Events update it; screens read from it. No screen fetches on a
  timer.
- A room's messages are loaded a page at a time, newest page first, with older
  pages fetched on scrolling up.
- The Unread page needs the newest message of each unread room. Use the
  message from an event already received when there is one, otherwise fetch it
  by `last_message_id`.
- Live, non-final `message.edited` frames update the streaming message's text
  directly; the finalized event replaces it.
- A 401 from any request returns the person to the login screen.

## 6. Technology

- **Svelte 5 with TypeScript, built by Vite.** *(Kai chose Svelte over Preact
  on Claude's recommendation)* Kai's reason for ruling out React is that it is
  heavier than this needs and makes the developer think about state in order
  to serve its virtual DOM; see
  <https://infrequently.org/2024/11/if-not-react-then-what/>. Svelte compiles
  components to direct DOM updates and treats state as plain values. This
  replaces the Preact line in the product spec.
- **Plain Svelte, not SvelteKit.** *(Claude's call)* The app is static files
  behind an existing API; SvelteKit's server features are unused and its
  inline bootstrap script works against the content security policy.
- **Source in `web/`**, with `web/embed.go` exposing the built files, as the
  architecture spec already lays out.
- Every dependency is installed from the lockfile and bundled. Nothing is
  loaded from a CDN at run time.

## 7. Work on the server

Small, and part of this project:

- Serve the embedded files from `/` in the public route group, with long-lived
  caching for hashed assets and none for the entry page.
- Send the Content-Security-Policy header on those responses.
- Keep `docs/openapi.yaml`, the README, and `AGENTS.md` in step, as the
  repository's rules require.

No change to the JSON API is expected. If the builder finds one is needed, it
is raised with Kai before it is made.

## 8. Stages

*(Claude's call; Kai said to use whatever order is best.)* Each stage ends in
something usable and gets its own implementation plan.

1. **Daily use.** The build and embed, the security header, login, the event
   stream and store, the room list with groups, badges, the Unread row and
   page, generated icons, the room with feed, streaming, activity block,
   prompt card, composer with queue and steer, stop, pause and resume, stop
   everything, and a minimal new room / open DM form.
2. **The rest.** The note, room settings, search, @mention completion,
   keyboard shortcuts, message download, the home-screen manifest, and the
   desktop sidebar layout.

Stage 1 ships the phone layout, which on a desktop browser appears as a single
centred column. *(Claude's call)*

## 9. Left to the builder

- The markdown renderer and sanitizer, provided they meet section 3.
- Routing. Hash-based routes need nothing from the server; path-based routes
  need a fallback handler that must not shadow `/api`, `/agent`, or
  `/healthz`.
- How `go build` finds the output directory before the UI has been built (for
  example a committed placeholder), given the constraint in section 3.
- How the UI build joins `just check` and `just build`, and what CI needs.
- The hash function for icons (it need not be cryptographic, only stable and
  well mixed) and the 16 colours, which must be distinguishable in both
  themes.
- The exact palette, spacing, and badge wording.
- Component test tooling. The product spec asks for component tests for the
  UI; the cases that matter most are the unread rules in 4.3, the ordering
  rules in 4.2, and rendering of untrusted markdown.
- Whether inline `style` attributes are avoided or the policy's style rules
  are relaxed; the script rules are not negotiable.

### Checked against the server on 2026-10-12

- Posting does not advance the poster's read marker; only
  `PUT /api/rooms/{room}/read` does. The UI therefore sets the marker after a
  successful post, so a person's own message never shows as unread.
- No event announces a change of read marker. A room read on one device stays
  marked unread on another that is already open until that device reloads its
  rooms. Reloading the room list when the page becomes visible again is
  enough. *(Claude's call)*
- `GET /api/rooms` includes archived rooms, which search (4.6) relies on.
- `Author.name` is an agent's display name and a human's username. The icon
  needs the agent's handle, which the agents list gives by ID.

## 10. Edits to the other specs

Made alongside this document so they agree:

- Product spec sections 4 and 9, and the architecture spec's layout: Preact
  becomes Svelte.
