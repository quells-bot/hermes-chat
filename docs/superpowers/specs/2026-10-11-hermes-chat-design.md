# Hermes Chat: design

Date: 2026-10-11
Status: draft for approval

## 1. Intent

A small, private chat server that Hermes Agent profiles connect to as a custom
messaging platform, so Kai can experiment with Hermes without routing
conversations through Slack, Telegram, or any other third-party chat service.

The core use is phone-first: open the app, ask an agent a question or kick off a
long web-research run, put the phone away, and come back later to read the
result. Everything else supports that loop.

The API and UI are shaped around what Hermes actually does (streaming replies,
tool activity, approvals, clarifying questions, stop/steer, scheduled
deliveries), not around Slack's idioms.

Kai chose Go so that he can read the server, reason about its performance and
security, and catch the gotchas himself. Code that is easy to follow matters
more here than code that is clever.

## 2. Goals, non-goals, anti-goals

### Goals (v1)

- Humans and agents exchange messages in rooms; agent replies stream in live.
- A long run can be started from a phone and read later, with a collapsed,
  expandable list of what the agent did.
- Messages to an offline agent queue and are delivered, in order, when it
  reconnects. A queued message can be cancelled before delivery.
- Several agents can share a room, under a clear and reliable off switch.
- Approvals and clarifying questions from agents wait for the human and show as
  a badge on the room.
- A per-human note is shared with every agent, with agent-suggested additions
  held in quarantine until the human approves them.
- One or two humans, up to roughly two dozen agents, on one cheap VPS.

### Non-goals (v1)

- Push notifications.
- Managing humans, agents, or tokens from the web UI.
- Threads inside a room (reply-to is the lightweight substitute, and is in v1).
- Emoji reactions, humans editing sent messages, human typing indicators.
- Search across history.
- Voice messages or voice replies.
- Native mobile apps.
- Switching an agent's model from the chat UI.
- Showing tool output (what a search returned). Only what the agent asked for.
- Agent ownership or per-human agent permissions. Every human can use every
  agent. Kai decided to skip this: the humans are not adversarial, and sharing a
  tuned agent with a colleague is a feature.

### Planned for v1.x (do not block it)

- Uploading files and images from the phone to an agent. v1.0 must already store
  attachments in a way that makes this an addition, not a rework (see 6.6).

### Anti-goals

- **An off switch that depends on agents behaving.** Pausing must hold even if a
  plugin is buggy or an agent ignores the signal.
- **Surprise token spend.** No path where agents keep triggering each other
  without a human in the loop beyond the turn cap.
- **Any third-party request from the browser.** No CDN scripts, styles, or
  fonts. Everything is served by the app.
- **React.** Kai ruled it out.
- **Becoming a general Slack clone.** Features exist because Hermes or the core
  loop needs them.

## 3. Constraints

- Server: Go, SQLite, one binary, cheap VPS, served over the public internet.
- The Hermes machine and the chat server are separate machines. Agents may run
  on a laptop behind home wifi (Hermes Desktop today) or on another VPS later.
- Human accounts are created and given passwords only through an admin CLI on
  the box.
- Web UI: lightweight, phone-optimized, all assets vendored and self-served.
  A Node toolchain for building is acceptable.

## 4. System shape

Three pieces:

1. **Chat server** (Go). Owns all state. Serves the web UI, the human API, and
   the agent API. Also provides the admin CLI as subcommands of the same binary.
2. **Web UI** (Preact). Built to static files and embedded in the Go binary.
3. **Hermes platform plugin** (Python). A `kind: platform` plugin dropped into
   `~/.hermes/plugins/`. It subclasses Hermes's `BasePlatformAdapter` and
   translates between Hermes and the chat server's agent API. It requires no
   changes to Hermes itself.

Each agent is one Hermes **profile** (its own persona file, memory, model,
skills, sessions) with its own token for the chat server. One Hermes gateway
process can serve many profiles, each running its own instance of the plugin.

**Connection direction.** Agents always dial out to the chat server. The server
never connects to a Hermes machine. This is what lets an agent run behind NAT
and move between machines by copying its token.

**Transport (decided by Kai: SSE, not WebSockets).** Both kinds of client use
the same pattern:

- **Down:** one long-lived Server-Sent Events stream. The client supplies the
  ID of the last event it saw; the server sends everything after it, then holds
  the stream open and pushes new events. Catch-up after being offline and live
  delivery are the same mechanism.
- **Up:** ordinary HTTP requests.

Browsers authenticate with a session cookie; agents with a bearer token.

Delivery to agents is at-least-once. The plugin drops repeats by message ID
using Hermes's `MessageDeduplicator` helper.

## 5. Concepts

### 5.1 Rooms

There is one kind of container, a **room**: a topic, a member list, and a
message history. To Hermes each room is a separate chat, so each agent keeps
separate context per room. Three flavors, distinguished only by membership:

| Flavor | Members | Agent responds to |
|---|---|---|
| **DM** | one human, one agent; permanent; no topic | every message |
| **Conversation** | one agent, a topic | every message |
| **Channel** | two or more agents, a topic | only when addressed (5.2) |

Adding a second agent to a conversation makes it a channel; nothing else
changes. DMs are where a human shapes an agent's persona and memory. Each human
has their own DM with a given agent, but the agent's Hermes profile (and so its
memory) is shared across humans.

Visibility: a human sees only rooms they created or were invited into.

Smaller points, all Claude's calls: a DM is created the first time a human
opens one with an agent. Rooms can be archived but not deleted in v1, and
history is kept indefinitely. Unread state is a per-human, per-room last-read
marker.

### 5.2 Addressing rules in channels

- An agent is woken by an **@mention**, from a human or from another agent.
- A **human's reply-to** on an agent's message wakes that agent without a
  mention.
- An **agent's reply-to** wakes nobody. Agents tend to mark every answer as a
  reply to whatever triggered them, so counting those as addressing would let
  two agents ping-pong with no mentions. An explicit @mention is the only way
  one agent wakes another. *(Claude's call.)*
- A message that addresses nobody wakes nobody, but stays in the room history.
  Whether un-addressed channel messages are handed to an agent as background
  context when it is next woken (Hermes has a `channel_context` field for this)
  is left to the builder; default to yes, capped in size.

The server decides who is woken and delivers only to those agents. The plugin
does not make this decision.

### 5.3 Messages and reply-to

A message has an author (human or agent), a room, markdown text, optional
attachments, and an optional reply-to pointer to another message in the same
room. Reply-to maps directly onto Hermes: inbound `MessageEvent` carries
`reply_to_message_id`, `reply_to_text`, and `reply_to_author_name`; outbound
`send()` takes `reply_to`.

A human message addressed to an agent has a visible life:

`waiting` → `delivered` → `working` → `done`, or `cancelled`, or `failed`

Cancel is available while `waiting`. After delivery, the control is Stop.

### 5.4 Turns and activity

A **turn** is one agent working on one delivered message. During a turn the
plugin reports, and the UI shows:

- **Streaming reply text.** Hermes sends a message and then edits it as text
  arrives, with a finalize flag on the last edit.
- **Tool steps.** For each tool call: name, short preview, full arguments,
  duration, and succeeded/failed. These come from Hermes's `ToolCallChunk` and
  `ToolCallFinished` stream events. Tool output is not sent and not shown.
- **Remarks.** The agent's short between-step updates (Hermes `Commentary`
  events), shown inline in the activity list.

Reports arrive as one message: the plugin declares no message length limit
(`max_message_length=0`) so Hermes does not chunk long replies.

### 5.5 Queueing and offline agents

- Messages for an agent that is offline wait in order and are delivered on
  reconnect. All of them are worked through unless cancelled first. *(Kai's
  decision.)*
- **One message per (room, agent) at a time.** By default Hermes treats a new
  message arriving mid-run as an interruption of that run. So the server
  releases the next queued message for a room only after the plugin reports the
  current turn finished (Hermes's `on_processing_complete` hook). Different
  rooms run concurrently. *(Claude's call.)*
- A follow-up sent while a turn is running therefore queues by default. The
  composer also offers **steer**, which is sent as a control event and applied
  by Hermes at the next tool boundary without ending the turn. *(Claude's call;
  drop steer from v1 if it proves awkward.)*
- If an agent's connection drops mid-turn, the Hermes run usually continues.
  The plugin buffers its outbound calls and retries on reconnect. The server
  shows the agent as offline and the turn as still open; it does not redeliver
  a message the plugin already acknowledged.

### 5.6 Agent-initiated messages

Hermes delivers scheduled-job results and background-task completions to a
designated "home" chat. For each agent, home is its DM with the human named in
that profile's config (`cron_deliver_env_var` / home channel). The server
accepts unprompted agent posts only into rooms the agent is a member of and
that are not paused.

## 6. Behaviors

### 6.1 The off switch

Two levels, both operated from the web UI by any human:

- **Pause a room.**
- **Stop everything** (all rooms).

Pausing a room does three things:

1. The server stops delivering that room's messages to agents. Queued messages
   stay queued.
2. The server rejects agent posts into that room.
3. The server sends a pause event to each member agent's plugin, which issues
   Hermes's own stop for any turn in progress in that room.

Steps 1 and 2 are enforced by the server alone and hold even if step 3 fails.
That property is the requirement. A paused room stays paused until a human
resumes it.

**Stop everything** sets a global halt with the same three effects across all
rooms. Lifting it returns each room to whatever pause state it had before.

**Turn cap (the real backstop).** The server counts turns in a room that were
woken by an agent, not by a human, since the last human message. At the cap,
the room pauses itself and says why. Default cap: 6, configurable per room.
Turns woken by humans and unprompted deliveries (scheduled jobs, see 5.6) do
not count, so a DM receiving a week of scheduled reports never pauses
itself. *(Rule and number are Claude's call.)*

**Hermes loop guard (bonus layer).** Hermes core drops bot-authored messages
after 20 in a conversation within 5 minutes, for 10 minutes. It applies to any
platform whose adapter marks the source as a bot, so the server includes an
"author is an agent" flag on each delivered message and the plugin passes
`is_bot=True` to `build_source()`. It counts per Hermes process and runs on the
agent side, so it is not relied upon.

**Stop** on a single turn (the header button) sends a stop event for that room
and agent only, without pausing the room.

### 6.2 Approvals and clarifying questions

Hermes agents sometimes block on the human: an approval for a risky command
(once / session / always / deny) or a clarifying question with choices.

- The plugin implements Hermes's interactive hooks (`_send_exec_approval_prompt`,
  `send_clarify`) and posts each as a **prompt** in the room.
- The UI renders tappable choices. The answer travels back to the plugin as an
  event and is resolved through Hermes's resolvers
  (`tools.approval.resolve_gateway_approval`,
  `tools.clarify_gateway.resolve_gateway_clarify`).
- An open prompt puts a **badge on the room** and a count on the room list.
- **The agent waits for the human.** *(Kai's decision.)* This is a Hermes-side
  setting the chat server cannot enforce, so agent setup instructions raise
  `approvals.timeout` (default 300 s, then treated as denied) and set
  `agent.clarify_timeout` to `0` (default 3600 s, then the agent guesses).
- The UI still handles expiry: when the plugin reports a prompt withdrawn or
  timed out, it shows as expired and its buttons are disabled.

### 6.3 The per-human note

- Each human has a note: text about themselves that every agent should know.
- The server attaches the author's note to each delivered message; the plugin
  passes it to Hermes as `channel_prompt`, a per-message system prompt applied
  at call time and never written to the transcript. Edits take effect on the
  next message, for every agent.
- **Notes are attached only in rooms where the author is the only human.**
  Otherwise an agent could repeat one human's note in a reply another human
  reads, and humans must not see each other's notes. *(Claude's call, to make
  Kai's privacy rule true.)*
- **Agents can suggest additions.** The plugin registers a tool (for example
  `remember_about_user`) that posts a suggestion to the server.
- **Suggestions are quarantined.** *(Kai's decision.)* They are not sent to any
  agent until the human approves them in the web UI.
- The human's own text and agent entries are stored separately. Agents append;
  they cannot rewrite or delete. Each entry records which agent added it and
  when.
- Size caps, since the note costs tokens on every message: 2,000 characters of
  human text, 300 per agent entry, 4,000 total approved. *(Numbers are Claude's
  call.)*
- The human can view, edit, approve, reject, and delete everything in the note
  from the web UI. A human can never see another human's note.

### 6.4 Rendering agent output

Agent text is untrusted: an agent that has been reading the web may relay
hostile content.

- Markdown is rendered with raw HTML disabled and the result sanitized.
- Links open in a new tab with `rel="noopener noreferrer"`.
- Remote images are not loaded automatically (shown as a link or a
  tap-to-load placeholder).
- Any message can be downloaded as a `.md` file. *(Kai's decision.)*

### 6.5 Presence

The room header and room list show whether each agent is connected. Sending to
an offline agent is allowed and shows the message as `waiting`.

### 6.6 Attachments

Agents can send files and images (Hermes `send_document`, `send_image_file`,
and related). The plugin uploads them over HTTP; the server stores them as
files on disk, with metadata in SQLite, and serves them only to members of the
room, with `Content-Disposition: attachment` for anything that is not a known
safe image type. Human uploads are v1.5 and will reuse this storage.

## 7. Auth and admin

### Humans

- Created only by the admin CLI. Username and password; no self-signup, no
  password reset in the UI.
- Passwords hashed with argon2id.
- Login is throttled per username and per IP.
- Sessions are server-side rows referenced by a cookie that is `HttpOnly`,
  `Secure`, and `SameSite=Lax`. State-changing requests also check the `Origin`
  header.

### Agents

- Created only by the admin CLI, which prints a random 256-bit token once and
  stores only its SHA-256 hash.
- The token goes in that Hermes profile's `.env`. The plugin sends it as a
  bearer token.
- Revoking a token in the CLI closes that agent's live stream within one
  heartbeat interval (the CLI is a separate process; the stream re-checks its
  token on each heartbeat).
- One live stream per agent token; a second connection replaces the first.

### Admin CLI

Subcommands of the server binary, operating directly on the database file:

- `user add | passwd | remove | list`
- `agent add | revoke | rotate | list` (display name and @handle set here)

### What humans can do in the web UI

Create rooms; add or remove agents; invite the other human; pause and resume a
room; stop everything; set a room's turn cap; cancel a waiting message; stop or
steer a turn; answer prompts; manage their own note and its quarantine.

## 8. Web UI

Phone-first, usable on desktop. Installable to the home screen via a web app
manifest. Screens:

- **Login.**
- **Room list.** Each row: name, flavor, last activity, and badges for open
  prompt (with count), working, paused, and unread. A stop-everything control.
- **Room.** Header with topic, members and their presence, Stop when a turn is
  running, pause/resume. Message feed with rendered markdown, reply-to quoting,
  message state, and download. Composer with reply-to, @mention completion in
  channels, and queue vs. steer while a turn is running.
- **Activity block** inside the feed, one per turn. Collapsed by default:
  working indicator, step count, and the current step. Expanded: each tool step
  with preview, duration, and success/failure; tap a step for its arguments;
  remarks inline. A mockup Kai approved is saved at
  `.superpowers/brainstorm/10263-1791735561/content/run-in-progress.html`.
- **Prompt card** for approvals and clarifying questions, with an expired state.
- **Note.** My text, approved agent entries, and the quarantine queue.
- **New room / room settings.**

## 9. Technology decisions

How the Go server's code is organized, and the libraries it is built from, are
specified in `2026-10-11-chat-server-architecture-design.md`.

Decided by Kai:

- Go server, SQLite database, single cheap VPS.
- SSE for the down channel.
- Not React. All JS and CSS vendored and served by the app.

Claude's calls (Kai delegated the frontend choice and has not objected to the
rest; change freely if there is a reason):

- **Preact with TypeScript and a normal build step** (Vite or similar). Built
  output is embedded in the Go binary with `embed`.
- **Strict Content-Security-Policy**, `default-src 'self'` with no inline
  script, made possible by vendoring everything.
- **Pure-Go SQLite driver** (`modernc.org/sqlite`) so the binary needs no cgo
  and cross-compiles cleanly. WAL mode.
- **The binary serves HTTPS itself** and obtains its certificate from Let's
  Encrypt using `golang.org/x/crypto/acme/autocert`, with the certificate cache
  in a directory on disk. A config setting runs it as plain HTTP for local
  development or behind a reverse proxy. CertMagic (the library Caddy uses) is the alternative
  if DNS challenges or more robust renewal are ever needed.
- **Storage layout:** one SQLite file, one attachments directory, one
  certificate cache directory.

## 10. Notes for the builder

- **Event log as the cursor.** Events get a monotonically increasing ID. A
  client's stream is the subset of events it is entitled to (room membership
  for humans; addressed deliveries and control events for agents) after its
  cursor.
- **Do not log every streaming edit.** Token-by-token edits are fanned out to
  connected browsers from memory. The message row is updated on a throttle and
  on finalize. A browser that reconnects mid-stream refetches the message body.
  Otherwise a long run writes thousands of rows.
- **SSE details:** send periodic comment heartbeats so idle connections are not
  dropped; set `X-Accel-Buffering: no` in case a proxy is in front; disable the
  server write timeout for stream handlers only.
- **Build a fake agent first.** A small test client that speaks the agent API
  lets the server and UI be built and tested without Hermes. Suggested order:
  server and fake agent; web UI; Hermes plugin.
- **Reference adapters** in the Hermes repo: `plugins/platforms/irc/adapter.py`
  (complete, dependency-free plugin) and `plugins/platforms/mattermost/` (a
  Slack-like service). The plugin guide is
  `website/docs/developer-guide/adding-platform-adapters.md`.
- **Plugin registration:** set `max_message_length=0`, a `platform_hint` saying
  markdown is rendered and long reports are fine, `cron_deliver_env_var` for the
  home room, and `allowed_users_env` / `allow_all_env`. Authorization is the
  chat server's job, so the documented setup allows all users on this platform.
  Read env through `get_scoped_secret`, never `os.getenv`, so multi-profile
  gateways work.
- **Tests:** Go tests for delivery ordering, the one-at-a-time queue, pause
  enforcement, the turn cap, addressing rules, note attachment rules, and auth.
  Component tests for the UI. Plugin tests against the fake server.

### To verify against Hermes before relying on it

These come from reading the Hermes source on 2026-10-11. Hermes moves quickly;
confirm each against the installed version.

- How a plugin adapter receives structured stream events (`ToolCallChunk`,
  `ToolCallFinished`, `Commentary`) as data. The base class has
  `render_message_event` and `format_tool_event`; confirm the cleanest override
  point for forwarding raw events instead of formatted text.
- The right call for stopping an in-flight turn from the plugin (dispatching a
  `/stop` event versus `cancel_session_processing`), and for steer.
- That `channel_prompt` is honored for plugin platforms on every turn.
- The upper bound accepted by `approvals.timeout`.
- Whether Hermes Desktop loads user plugins from `~/.hermes/plugins/` the same
  way the CLI gateway does.

## 11. Agent setup (per Hermes profile)

Documented for the user, not automated in v1:

1. On the chat server: `agent add`, copy the token.
2. Install the plugin into the Hermes plugins directory and enable it.
3. In the profile's `.env`: server URL, token, home room.
4. In the profile's `config.yaml`: raise `approvals.timeout`, set
   `agent.clarify_timeout: 0`.
5. Start or restart the gateway.
