# Hermes Chat

Hermes Chat is a chat server written in Go, backed by SQLite. The design is in
`docs/superpowers/specs/2026-10-11-chat-server-architecture-design.md` and
`docs/superpowers/specs/2026-10-11-hermes-chat-design.md`.

## Tools

Install `just` and `sqlc` yourself. goose is used as a library, so its CLI is
not needed.

| Tool | Version |
|---|---|
| Go | 1.26.5 |
| sqlc | 1.31.1 |
| Node / npm | 24.18.1 / 11.16.0 |
| Python / uv | 3.14.4 / 0.11.31 |
| just | 1.45.0 |
| shellcheck | 0.11.0 |
| sqlite3 shell | 3.46.1 |

## Recipes

- `just generate`: run `sqlc generate` to regenerate the query code.
- `just check-generated`: run `sqlc diff`; fails if generated code is stale.
- `just vet`: run `go vet ./...`.
- `just test`: run `go test ./...`.
- `just build [goos] [goarch]`: build the web UI, then
  `bin/hermes-chat-<goos>-<goarch>` with `CGO_ENABLED=0`. Defaults to linux
  amd64.
- `just web-install`: `npm ci` in `web/`.
- `just web-build`: install, then build the web UI into `web/dist`.
- `just web-check`: install, then type-check and test the web UI.
- `just web-dev`: run the Vite dev server (see "Web UI").
- `just lint-scripts`: run `shellcheck` on `deploy/hermes-chat-upgrade` and `deploy/hermes-chat-admin`.
- `just check`: `check-generated`, `vet`, `lint-scripts`, `web-check`, and
  `test`. It needs Node; `go test ./...` on its own does not.

## Configuration

Set through environment variables. Only variables with the `HERMES_CHAT_` prefix are read; unprefixed names such as `LOG_LEVEL` are ignored.

| Variable | Default | Notes |
|---|---|---|
| `HERMES_CHAT_HTTP_ADDR` | `:8080` | Address of the HTTP server. |
| `HERMES_CHAT_DEBUG_ADDR` | `127.0.0.1:6060` | Serves pprof. Must be `localhost`, a 127.0.0.0/8 address, or `::1`; anything else is rejected when `serve` starts. |
| `HERMES_CHAT_DB_PATH` | none | Path to the SQLite file, relative (resolved against the working directory) or absolute. Required. |
| `HERMES_CHAT_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, or `error`. |
| `HERMES_CHAT_GIN_MODE` | `release` | `debug`, `release`, or `test`; checked when `serve` starts. |
| `HERMES_CHAT_COOKIE_SECURE` | `true` | The `Secure` attribute on the session cookie. Set `false` only for plain HTTP on a development machine. |
| `HERMES_CHAT_TRUSTED_PROXIES` | empty | Comma-separated IPs or CIDRs whose `X-Forwarded-For` is believed. Needed only behind a reverse proxy; empty trusts none, so the client IP is the TCP peer. Checked when `serve` starts. |
| `HERMES_CHAT_ATTACHMENTS_DIR` | `attachments` beside the database file | Where attachment bodies are stored. Created `0700` when `serve` starts. |
| `HERMES_CHAT_TLS_MODE` | `autocert` | `autocert` or `off`; checked when `serve` starts. The default makes a host that forgets the setting fail instead of serving plain HTTP. |
| `HERMES_CHAT_TLS_DOMAIN` | none | The one name certificates are issued for. Required when the mode is `autocert`, ignored when `off`. A lower-case DNS name with at least one dot: no scheme, port, path, wildcard, or IP address. |
| `HERMES_CHAT_TLS_CACHE_DIR` | `certs` beside the database file | Where issued certificates and the account key are cached. |

A development machine running `serve` on plain HTTP sets `HERMES_CHAT_TLS_MODE=off` and `HERMES_CHAT_COOKIE_SECURE=false`. A production environment file sets `HERMES_CHAT_HTTP_ADDR=:443`.

Login and logout compare the request's `Origin` header with its `Host` header, so a reverse proxy in front must pass the original `Host` through (nginx: `proxy_set_header Host $host;`). Otherwise every login returns 403.

## Commands

    hermes-chat serve                         run the HTTP server
    hermes-chat migrate                       apply database migrations
    hermes-chat user add NAME                 create a human
    hermes-chat user list                     list humans
    hermes-chat user passwd NAME              change a human's password
    hermes-chat user remove NAME              remove a human
    hermes-chat agent add HANDLE NAME         create an agent
    hermes-chat agent list                    list agents
    hermes-chat agent rotate HANDLE           issue an agent a new token
    hermes-chat agent revoke HANDLE           revoke an agent's token

With no command or an unknown one, `hermes-chat` prints usage and exits 2
without needing any configuration. `serve` and `migrate` apply migrations;
every other command requires the schema to be current, so run `migrate`
first.

`user add` and `user passwd` prompt for the password on a terminal and
otherwise read one line from standard input. `agent add` and `agent rotate`
print the token once; it cannot be shown again.

A human created before passwords existed needs `user passwd` before logging in.

## Deploying

`docs/deploy.md` is the runbook for a Debian 13 host: preparing it, the first start with a Let's Encrypt certificate, creating people and agents, upgrading, and rolling back. The files it installs are under `deploy/`:

- `hermes-chat.service`: the systemd unit.
- `hermes-chat.env.example`: the environment file, installed as `/etc/hermes-chat/env`.
- `hermes-chat-upgrade`: installs a new binary on the host, with a database copy and automatic rollback.
- `hermes-chat-admin`: runs `hermes-chat user ...` and `agent ...` as the service user with the service's environment.

## Web UI

The server serves the browser app that is embedded in the binary. Both routes
are public:

- `GET /` is the entry page, sent with `Cache-Control: no-cache` because it
  names the current hashed assets.
- `GET /assets/<file>` is one built file, sent with
  `Cache-Control: public, max-age=31536000, immutable`. Anything under
  `/assets/` that is not a file is 404.

Every one of these responses carries
`Content-Security-Policy: default-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'`,
`X-Content-Type-Options: nosniff`, and `Referrer-Policy: no-referrer`. The page
runs no inline script and loads nothing from another origin.

A binary built with plain `go build`, without building the UI first, answers
404 on `/` and logs a warning when it starts. `just build` builds the UI first.

After login the page shows the person's rooms in two groups, direct messages
and rooms, each in alphabetical order by name and each collapsible (which
groups are collapsed is the one thing the page keeps in the browser). A row
shows an unread dot and a bold name from the server's read markers, and a
posted message never moves a row. Direct messages show whether the agent is
connected. The header has **Stop all**, which stops everything in one tap and
shows `Stopped` and **Resume all** while stopped, and a **+** that opens
`#/new`: a button per agent that opens its direct message, and a form with a
topic, a checkbox per agent and **Create**. Either way the person lands in the
room.

While any room that is not archived is unread, an **Unread** row stands above
the groups. It names the unread rooms, counts them, says how many questions
are open in them, and leads to `#/unread`. That page lists the unread rooms,
those with an open question first and then the one with the newest message
first, each with its name, the age of its newest message, and that message's
author and the start of its text (the names of its attachments when it has no
text). **Mark all read** moves every one of those rooms' read markers to its
newest message and returns to the list. A room opened from the page leads back
to the page.

A row opens its room at `#/rooms/<id>`. The header names the room, says
whether each agent is working, idle or offline, and, like the list's, says
`reconnecting` while the store is loaded and the stream is not live. The feed
starts with the room's newest 50 messages; when the read marker is older than
all of them and older messages exist, the 150 before them are fetched too, and
the feed is not placed until that fetch has settled. A room with no read
marker gets the same 150 fetched before its newest page, so only a room with
more than 200 unread messages loses the `New` line. The feed is then placed on
a `New` divider above the first unread message it holds; when the oldest held
message is itself unread and older ones remain, there is no `New` and the feed
opens at its oldest held message; and it opens at its end when nothing is
unread. The divider stays where it was drawn while the room stays open; it
moves only when new unread messages arrive that the view cannot hold, and the
feed then stands on it above the first of them. Scrolling to the top loads the
50 messages before the oldest one held and keeps what was being read in
place. A message that fits arrives while the end is in view keeps the end in
view; new unread messages that do not all fit in the view are never followed
past the first of them, so the person is not carried past the unread. A hidden
page does not follow the feed at all, and coming back to the room leaves the
unread unread and the feed standing on `New` until it is scrolled to. The read
marker moves to the newest message when the end of the feed is in view on a
visible page, and again for each message that arrives while both hold; it is
not moved on a report taken before new messages were drawn. The end is watched
only once the feed has been placed, so a room that opens on its divider is not
read until the person scrolls to its end.

A message shows its author, its time, a line of the message it replies to, its
text, its attachments (an image inline, any other file as a download link with
its size), and each agent's copy: `waiting`, `delivered` or `cancelled` as a
line, and a copy that is `working`, `done` or `failed` as an activity block. A
prompt is a card with the question and its choices, or its answer, or
`Expired`. An activity block says the agent, the state and, once its list is
held, the number of steps; a working one also says what the agent is doing now
when its newest step is an unfinished tool or a remark, and an open one lists
the steps with the remarks too, each tool with its result, its duration and,
on a tap, its arguments when it has any. The list is fetched without being
asked for a turn that is working in a room whose messages are held, and for a
finished turn when its block is opened; `activity.posted` keeps a held list
current, and a reload does not empty the lists — a block that is open keeps
its list on screen, and a turn still working is asked for again. Under the
feed a line says when the room is paused, archived, or stopped with everything
else, and the Unread page states `Everything is stopped` under its header when
the store is halted.

Under the feed a composer writes a message: a text box, a Send button (Enter is
a newline; only the button sends), and, while an agent in the room is working
and the room is not paused and nothing else is stopped, **Queue** and **Steer**
choices — Queue posts to the room, Steer sends one copy to each working agent's
running turn — beside a reply chip when the message is a reply (a steer carries
no reply, so the chip is hidden while Steer is chosen and kept for the next
post). The text cannot be edited while a send is on its way. A failed send
shows its reason beside the box, keeps the text and the attempt's
`client_id`, and the button reads **Retry** to send it again, so the server
answers the first message instead of posting twice; editing the text drops
the id and makes a fresh send. While a delivery
of the person's own message is waiting, a **Cancel** cancels the message's
waiting deliveries. The room header's **Stop** asks every working agent to stop
while one is working, and a **⋯** menu pauses and resumes the room by its state
and, in a room that is not a direct message, leads to its settings. A failure of one of these is shown where it
happened and nowhere else: a failed Cancel under its message, a failed Stop,
Pause or Resume at the top of that room, until the next one works or it is
dismissed. An open prompt card answers with one button per
choice, or a text input and an **Answer** button when the prompt asks for
words; an answered or expired prompt keeps its buttons, disabled. Every write —
a post, a steer, an answer, a pause, a resume — is applied through the same
holding rule as an event, so it cannot be overwritten by a snapshot fetch in
flight, and a successful send moves the read marker to the message it returns.

Message text is markdown and is untrusted. `marked` lexes it, and the page
copies the tokens it knows into a tree of its own and draws that tree as
elements; it never builds an HTML string. Raw HTML and any token the tree does
not name are shown as text. A link is kept only when its address is `http`,
`https` or `mailto` (otherwise its text stands alone) and opens in a new tab; a
markdown image is shown as a link under the same rule, never loaded. Inline
lexing is bounded, because `marked` is slow on some inputs: an inline run
longer than 4096 characters, and every run after one message has spent 150 ms
on them, is shown as plain text.

One store holds what the screens show: the rooms, the agents, the halt, the
messages of the rooms that have been opened, the newest message known of a room
(fetched for the Unread page when no event brought it), the messages that
replies name, and the lists of activity. The page reads `GET /api/events` with
`fetch` (not `EventSource`, which hides the status code and the heartbeat),
loads on every `ready` and when the page becomes visible again, and applies
each later event to the store. A fetch whose answer is a snapshot (a load, a
page of messages, a list of activity) replaces what it covers, so events that
arrive while any of them is in flight are held, in order, and applied when none
is. A load replaces the rooms, the agents and the halt, keeping a read marker
the page holds when it is newer than the one that arrives. It drops the
messages of every room but the open one, and fetches that room's newest page:
the page is merged into the messages held, or replaces them when it is full and
wholly newer, since messages may be missing between the two. It empties the
lists of activity and fetches again those of the turns still working in the
open room. A room keeps its messages after it is left, until the next load, so
going straight back fetches nothing. A room whose first page failed holds no
messages, only the reason and a **Retry**; events are not put into it. The page
resumes a dropped connection with `?cursor=` set to the last id it received,
reconnecting after 1, 2, 4, 8, 16, then 30 seconds. It drops a connection that
has been silent for 60 seconds and then reconnects. Once the list has loaded,
the list's header says `reconnecting` while the stream is not live. Routes are
hash-based: `#/` is the list, `#/new` the form, `#/unread` the Unread page,
`#/note` the note, `#/rooms/<id>` a room and `#/rooms/<id>/settings` its
settings; an id that names no room the person is in leads back to the list, and
so does any other address.

The list's footer has a **Note** link beside the username and **Log out**. The
note is not part of the snapshot and no event announces a change to it, so the
store fetches it with every load (each `ready`, and the page becoming visible)
and the link carries a badge with the number of entries waiting for review. A
failed fetch fails nothing else. The note is held in memory only, outside the
store, and is emptied when the stream is torn down; a person never sees
another's. `#/note` shows the person's text in a box with a counter
(`1234 / 2000`) and **Save**, then the entries to review and the approved
entries. An entry is drawn as plain text, never as markdown, with the agent
that wrote it. A pending entry has **Approve**, **Edit** and **Delete**; an
approved one has **Edit**, **Unapprove**, which sends it back to review, and
**Delete**. Delete is one tap and cannot be undone. The text and the approved
entries together are counted against 4000 characters, an entry against 300;
characters are counted after trimming, as code points, as the server counts
them. A counter turns to the error colour past its limit and **Save** is
disabled, but the boxes have no `maxlength` and the server's message is shown
when it refuses. Only one note write is in flight at a time: while one is, the
screen's controls are disabled and a second call does nothing. The answer to a
load is dropped when a write was in flight at any moment between the load's
start and its answer.

**Settings** in a room's **⋯** menu opens `#/rooms/<id>/settings`; a direct
message has no such item, and its address leads to the room, since the server
refuses every change to one. One form holds the topic (1 to 200 characters) and
the turn cap (1 to 100) with one **Save**, which sends only the fields that
changed. Under it are the room's agents, each with **Remove** (one tap; the
agent can be added back, though its waiting copies are cancelled and its open
prompts expire; the last agent has none), the agents not in the room with
**Add**, and the people in the room, then the people who are not, each with
**Invite**. Invite takes two taps: it becomes **Confirm** and **Cancel** in the
same row, because the API has no way to remove a person, the newcomer sees the
room's history, and the note is no longer attached in that room. **Archive** is
one tap and is replaced by **Unarchive** on the same screen. While the room is
archived every other control is disabled, as the server answers 409
`room_archived`. One write at a time: while one is in flight every control on
the screen is disabled. Both screens have the **Stop all** control in their
header. Archived rooms are not listed, so until search exists an archived room
is reached only by its address.

The source is a Svelte 5 and TypeScript project in `web/src`, built by Vite
into `web/dist`, which `web/embed.go` embeds. `web/dist` is never committed
apart from a `.gitkeep` that lets `go build` and `go test ./...` work on a
clone that has no Node and has never built the UI. Dependencies are pinned to
exact versions and installed from `web/package-lock.json`; `marked` is the
only one that runs in the browser. TypeScript stays on
6.x: `svelte-check` does not start under TypeScript 7.

To work on the UI, run the server without TLS and the dev server beside it:

    HERMES_CHAT_DB_PATH=dev.db HERMES_CHAT_TLS_MODE=off HERMES_CHAT_COOKIE_SECURE=false go run ./cmd/hermes-chat serve
    just web-dev        # http://localhost:5173, /api proxied to :8080

The dev server does not send the content security policy; check a change
against the built binary before handing it over.

## HTTP API

The routes, request and response bodies, error codes, and JSON shapes are in
[`docs/openapi.yaml`](docs/openapi.yaml) (OpenAPI 3.1). Humans use `/api` with
the `hermes_session` cookie; agents use `/agent` with a bearer token. Every
state-changing `/api` route, and login, requires the `Origin` header to name
this server. Errors have one shape:

    {"error":{"code":"invalid_credentials","message":"wrong username or password"}}

The spec lists each `code`. The sections below describe behavior the spec
cannot: the two event streams, delivery and turns, the off switch, prompts, the
note, and attachments. They name shapes such as `<message>` and `<room>`, which
are the spec's `Message` and `Room` schemas (`<agent message>` and
`<agent room>` are `AgentMessage` and `AgentRoom`).

A post (`POST /api/rooms/:room/messages`) and a steer may carry a `client_id`:
a key the client makes up, at most 64 characters of letters, digits, `-` and
`_`. A request that repeats a key the same human already used in that room
answers 200 with the message the first request made, instead of 201 with a new
one, and changes nothing: no second message, event, or delivery. The first
request wins; the repeat's text is not compared. The key is looked up after
the membership check and before every other, so a retry gets its message back
even when the room has since been archived or the steered turn has ended. The
message carries its `client_id` to every human in the room, so a client can
match a `message.posted` event to the post it is still waiting on. Without a
`client_id` every request posts.

`TestRoutes_MatchTheOpenAPISpec` fails when a route is added or removed without
the spec following.

## Event stream

Open `GET /api/events` with the session cookie. The response is
`text/event-stream` and stays open. Every committed change to a room the
caller is in produces an event with an increasing integer ID.

The first frame is `ready`, with `id` and `data` `{"cursor":N}`, the cursor the
stream starts from. With no cursor the stream starts at the present, so open
the stream first, wait for `ready`, and only then load rooms and messages;
nothing that happens in between is lost. To resume after a disconnect, send
the last `id` seen as `Last-Event-ID` (a browser's `EventSource` does this
itself) or as `?cursor=`; the header wins if both are given. A cursor that is
not a non-negative integer is a 400. A cursor ahead of the event log is
treated as the end of the log, and `ready` says where the stream really
starts. Events after the cursor are sent in order, then new ones as they
happen.

| `event` | `data` |
|---|---|
| `message.posted` | `{"message":<message>}` |
| `message.edited` | `{"message":<message>}` — the finalized message |
| `delivery.changed` | `{"message":<message>}` — a delivery's state changed |
| `room.changed` | `{"room":<room>}` |
| `activity.posted` | `{"message_id":"01…","activity":<activity>}` |
| `agent.presence` | `{"agent":<agent>,"connected":true}` |
| `prompt.changed` | `{"message":<message>}` — a prompt was answered or expired |

`room.changed` carries the room as it is now, for a new room, a new topic,
archiving, and a change of members, so apply it by replacing the room you
hold. A person who is added to a room receives the room's earlier events too.
`delivery.changed` carries the message with its `deliveries` array, so the UI
can render each agent's copy moving through `waiting`, `delivered`, `working`,
`done`, `failed`, and `cancelled`. `agent.presence` tells the UI when an agent
connects or disconnects; apply `connected` to that agent in every room it is
in. `message.edited` is written once when a message is finalized (see the
agents' edit stream below).

While an agent streams edits, each one is also broadcast to the browser as a
live `message.edited` frame with `data`
`{"message_id":"01…","text":"…","final":false}`. These have no log row, arrive
in memory, and carry the current intermediate text so the UI can show typing
without a round trip. A slow browser can miss one — the row update (throttled
to once a second) and the finalize event carry the text it missed — and the
UI ignores a non-final edit for a message it knows is finalized.

A message's `streaming` is true from its first stored non-final edit until it
is finalized, so a browser that loads a room mid-stream knows more text is
coming; the logged `message.edited` carries `streaming:false`. When a turn
ends (`done` or `failed`) without a final edit, the server finalizes the
messages that agent was still streaming in the room since the turn's message,
with the text of their newest edit, and writes `message.edited` for each.

Each of a room's `agents` carries `working`: whether that agent has a delivery
in state `working` in that room. It is read when the room is loaded and no
event announces it; keep it current from `delivery.changed` (state `working`
sets it, `done` and `failed` clear it). The event carries every agent's copy
of that message as it is now, so `done` for an agent on an older message does
not mean that agent is idle.

An SSE frame may be replayed after a reconnect: the server resends everything
after the client's cursor, at least once. The plugin dedupes by message id.

A comment line (`: hb`) arrives every 25 seconds while the stream is idle. At
each one the server checks the session; if it has ended (logout, a new
password, removal, expiry) the stream is closed, so a stream closes within 25
seconds of its session ending. On shutdown the server closes every open
stream at once.

## Agent stream

Open `GET /agent/events` with the agent's bearer token. The response is
`text/event-stream` and stays open. The first frame is `ready`, with `data`
`{"cursor":N}`, exactly as for humans. The other frames are the agent's own:

| `event` | `data` |
|---|---|
| `delivery` | `{"message":<agent message>,"room":<agent room>,"context":[<agent message>…],"note":<string> or null}` |
| `stop` | `{"room_id":"01…"}` |
| `steer` | `{"room_id":"01…","message":<agent message>}` |
| `pause` | `{"room_id":"01…"}` |
| `prompt.answered` | `{"room_id":"01…","message_id":"01…","answer":"once"}` |

`delivery` hands the agent a message to work on; `context` is the room's
recent channel messages for context (see "Delivery and turns"), and `note` is
the author's note when the author is the room's only human, else `null` (see
"The note"). `stop` means
the human asked the agent to stop its running turn; `steer` redirects it with
a new instruction. `pause` means stop any turn in this room; nothing more will
be delivered there until a human resumes it. Report the stopped turn `done` or
`failed` as usual. A comment line (`: hb`) arrives every 25 seconds while the
stream is idle, and at each one the server re-checks the token, so a revoked
stream closes within 25 seconds. One stream per agent: a second connection
replaces the first, which ends. The plugin should keep its cursor and send it
back as `Last-Event-ID` (or `?cursor=`) to replay missed deliveries after a
reconnect, and buffer outbound reports while the stream is down so the next
`delivery` for an old message is reported once the connection returns. On
every connect, with a cursor or without, the agent is first handed again every
delivery still `delivered` (released, never reported `working`), at least once per
message, so a delivery sent to a connection that died is not lost; rows past
the cursor arrive by ordinary replay. Delivery is at least once: a message can
arrive twice on one connect when an older `delivery` row for it is behind the
cursor and a newer one is past it, so the plugin dedupes by message id. A stream opened with no cursor starts at
the end of the log.

## Delivery and turns

Every message an agent is responsible for moves through the state machine
`waiting → delivered → working → done | failed | cancelled`. Each agent gets
one delivery per message, and a room serves **one message at a time per
agent**: while an agent has a `delivered` or `working` copy, later messages
for it wait, and the next releases only when the current one reports `done`
or `failed`. A human can `cancel` a message while its copies are `waiting` —
a cancelled copy can never reach the agent, even if it reconnects later — and
`stop` or `steer` while the agent is `working`. Offline messages queue: a
delivery is written when the agent's stream reconnects, so nothing is lost to
a dropped connection.

A `delivery` frame's `context` is the room's recent messages for the agent's
context window: at most 20 messages and 4096 bytes of text, channels only,
older than the delivered message. The agent reports `working`, `done`, or
`failed` against the delivered message, streams edits with `PUT
/agent/messages/:message` (`final:false` while working, then `final:true`), and
posts its reply with `POST /agent/rooms/:room/messages` before reporting
`done`. Reporting `done` or `failed` releases the next queued message for that
pair.

## The off switch

A human can pause one room, or stop everything.

Pausing a room (`POST /api/rooms/:room/pause`) does three things, in this
order. First, the server releases nothing more in that room: queued messages
stay `waiting`, and none is cancelled, failed, or reordered. Second, it
refuses every agent write that adds content there: a post, a prompt, an edit (final
included), a remark, or a tool start answers 409 `paused`. Third, it sends each
agent in the room a `pause` event so a running turn can stop. The first two
are enforced by the server alone and hold even if the agent never receives or
obeys the third. A room stays paused until a human resumes it.

Still allowed in a paused room: a human posts (the message queues), cancels,
and stops; an agent reports its turn `working`, `done`, or `failed` and
finishes a tool it had already started. A human cannot steer. An agent that
reconnects during a pause is handed no `delivery` for that room; on resume,
the copy of a connected agent that is still `delivered` is sent again as a
fresh `delivery` event, and queued messages start flowing again. Unarchiving a
room does the same. Agents get no other resume event.

Stop everything (`POST /api/halt`) is the same, for every room at once. It is
stored apart from each room's own pause, so lifting it (`DELETE /api/halt`)
returns each room to whatever pause state it had before. Humans see it as one
`room.changed` per room, carrying `"halted":true`.

The turn cap bounds agents waking each other. It counts turns in a room that
were woken by an agent, not by a human, since the last human message; a turn
is counted when an agent-authored message's delivery is released to its agent.
Turns woken by a human, and unprompted deliveries, do not count. The release
that would exceed the cap does not happen: the room pauses with
`pause_reason` `turn_cap` and that delivery stays `waiting`. The default cap is
6, set per room with `PATCH` (1 to 100; a DM's cannot change). The counter
resets on a human message, a steer, and a human resume, so resuming a
cap-paused room buys another cap's worth of turns; lifting stop-everything
does not reset it. Resuming a room, or lifting stop-everything, can pause it
again at once with `turn_cap` if the released messages already reach the cap,
so the resume response may show `"paused":true`.

## Prompts

An agent that blocks on the human — an approval for a risky command, or a
clarifying question — posts a **prompt** with `POST /agent/rooms/:room/prompts`.
The prompt is a message: its text is the question, it rides the message feed,
paging, and unread markers, and it wakes no agent even if its text mentions
one. An open prompt puts a badge on the room: `open_prompts` counts the
prompts in a room still waiting, on the room object and its `room.changed`
events.

The prompt's `kind` says what may answer it:

- `approval` takes the `choices` you send, a non-empty subset of `once`,
  `session`, `always`, `deny`; the human's answer must be one of them.
- `clarify` takes 0 to 6 choices of up to 200 characters each; the human may
  tap a choice or type their own words, 1 to 2000 characters.

Either way a blank question is refused. The human answers with
`POST /api/rooms/:room/messages/:message/answer`; any human in the room may
answer, and answering does not count as a message (it never touches the turn
cap). The answer travels to the asking agent as a `prompt.answered` event on
its stream, and the room's streams see `prompt.changed` with the message.

Two humans tapping at once, or tapping twice: only the first answer is
stored, and the agent is told once. Answering an already answered prompt with
the *same* answer succeeds and changes nothing; a different answer, or any
answer to an expired prompt, is 409 `prompt_closed`. The plugin reports a
prompt it withdrew or that timed out by `POST /agent/prompts/:message/expire`;
expiring an answered or already expired prompt changes nothing.

Closing a room expires its open prompts: pausing (by a human or the turn
cap), stop everything, archiving, and removing the asking agent each close
the prompts they end, as `prompt.changed` events. An open prompt never sits in
a closed room, so there is no stale badge to clear. An answer the agent had
not read when its room closed is dropped, exactly as a steer is: the pause
has already told the plugin to stop that turn, and nothing is re-sent on
resume.

## The note

Each human has a note: text about themselves that every agent should know.
Its caps, counted in characters (runes, not bytes): 2,000 of the human's own
text, 300 per agent entry, and 4,000 total of text plus approved entries. An
empty note text is allowed and clears it. A human can never see another
human's note, and no agent can rewrite or delete the note or an entry.

An agent that learns something about a human mid-turn suggests it with `POST
/agent/deliveries/:message/note-entries` on the message it is working on: the
message must have been written by a human, the agent must still have an open
turn on it, and a paused room refuses the suggestion (409 `paused`). The
suggestion goes into **quarantine**: no agent sees it until its human
approves it. Suggesting the same text again returns the first entry. A
human's quarantine holds at most 50 pending entries.

The human views, edits, approves, rejects, and deletes everything from the
web UI (`GET`/`PUT /api/note`, and `PATCH`/`DELETE /api/note/entries/:entry`).
An entry keeps the agent that added it and when, through any edit. Changing
the note writes **no event**: another open tab sees a change only when it
next loads the note.

What an agent is given is composed at hand-over time, only when the delivered
message's author is the room's only human: the text, a blank line, then one
`- ` line per approved entry, oldest first — `""` when both are empty. The
decision is made when the agent reads the delivery, so a second human joining
a room before the read keeps the note out of it. Edits reach every delivery
not yet read.

## Attachments

Posting a file is two steps: the agent uploads it once with `POST
/agent/rooms/:room/attachments` (the file's bytes as the body, `?name=…` in
the query), then names its id in `POST /agent/rooms/:room/messages` under
`attachment_ids`. A message with at least one file may have blank text; an
upload that is never posted stays on disk. One upload is at most 25 MiB (413
`too_large` beyond it, refused on the declared length before reading) and one
message carries at most 10 files; each id must be the agent's own upload in
that room, not yet posted, once. A paused, archived, or unknown room refuses
an upload without reading any of it, and a refused upload leaves nothing
behind.

The stored type is sniffed from the first 512 bytes of the file; the request's
`Content-Type` header is ignored, so a file that lies about being an image is
served at its real type. Only `image/png`, `image/jpeg`, `image/gif`, and
`image/webp` are ever shown inline; everything else — SVG and HTML included —
is served as `application/octet-stream` with `Content-Disposition: attachment`.
All downloads carry `X-Content-Type-Options: nosniff`, a `sandbox` CSP, and
`Cache-Control: private, max-age=86400`; a file's name is its id on disk, so
no client input ever names a path. Uploads and downloads get a 10-minute
transfer deadline, replacing the server's write timeout.

Only an upload already on a message can be fetched (`GET
/api/rooms/:room/attachments/:attachment`), and only by a human in its room;
an agent gets 403. Downloads support byte ranges.

## Known limits

- Only a human's post and steer take a `client_id`. An agent that retries a
  post can post twice, and creating a room twice makes two rooms; the other
  human writes (the note, read markers, pause, answers) are safe to repeat as
  they are.
- `user remove` runs in another process and writes no event, so an open
  browser learns that a member has gone only when it next loads the room.
- Every committed change wakes every open stream, and each runs one indexed
  query. That is fine for two humans and a few tabs.
- The event log is never pruned, and a client that reconnects with a very old
  cursor is sent everything since.
- Over plain HTTP/1.1 a browser allows six streams per origin. TLS mode
  `autocert` negotiates HTTP/2, so the limit applies only to TLS mode `off`
  without an HTTP/2 proxy in front.
- A turn that never reports `done` or `failed` holds its agent's queue in that
  room forever, by design; `stop` exists for the human, and the plugin reports
  the stop as a finished turn.
- Activity rows, delivery-state rows, and presence rows are kept forever, like
  the event log.
- An intermediate edit dropped for a slow browser is gone; the row (updated at
  most once a second) and the finalize event carry the text it missed.
- A revoked or removed agent's open deliveries stall in `delivered`/`working`;
  nothing cancels another actor's turn.
- A message an agent was streaming outside a turn (a scheduled report) stays
  `streaming` until its final edit arrives. The server cannot tell which turn
  a message belongs to, so a report the agent begins in a room while it has a
  turn running there is closed when that turn ends: it stops at the text it
  had, its later non-final edits are ignored, and its final edit still lands.
- Stop everything writes one `room.changed` per room and one `pause` per agent
  per room.
- A prompt nobody answers and its agent never expires stays open, with its
  badge, until the room is paused or archived.
- An answer an agent had not read when its room was paused is not sent again
  on resume.
- Note changes write no event: another open tab sees a new suggestion only
  when it next loads the note.
- An upload that is never posted, and the files of a room deleted with its
  human, stay on disk; nothing sweeps the attachments directory yet.
- Humans cannot upload yet (v1.5); the table already has the column for it.

## Adding a migration

Migrations live in `cmd/hermes-chat/internal/storage/migrations/`.

1. Create the next numbered file, for example `00002_name.sql`.
2. Start it with `-- +goose Up`, then the statements, then `-- +goose Down`
   with the statements that undo them.
3. Declare every table `STRICT`.
4. Run `just generate` so sqlc picks up the schema.

For example, `00001_humans.sql`:

    -- +goose Up
    CREATE TABLE humans (
        id         TEXT    NOT NULL PRIMARY KEY,
        username   TEXT    NOT NULL UNIQUE,
        created_at INTEGER NOT NULL
    ) STRICT;

    -- +goose Down
    DROP TABLE humans;

## Layering

Code under `cmd/hermes-chat/internal` is layered `app` to `bus` to `storage`:
each layer may import only the one below it. Only `storage` may import its
generated `sqlcgen` package; `TestLayering_ThisRepository` checks this against
the real import graph.

The repository-root `internal/` holds packages that are not tied to the server:
`reqid`, `sse`, and `fakeagent`. They import none of the layers, by convention.

## Fake agent

`cmd/fakeagent` connects to a server as one agent, for trying it by hand:

    FAKEAGENT_URL=https://chat.example.com FAKEAGENT_TOKEN=hca_… go run ./cmd/fakeagent

It prints each event as a JSON line on standard output, answers every
`delivery` with `echo: <text>` (working, reply, done), and reopens a dropped
stream from its cursor, logging how long the stream stayed open. Status goes to
standard error. It is not the Hermes plugin.
