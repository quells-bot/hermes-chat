# Hermes Chat plugin for Hermes: design

Date: 2026-10-13
Status: stage 1 built and tried; sections 4, 6, 9 and 10 revised the same day
with what the first sitting showed (section 9, "What the first sitting showed")

This document covers the Hermes platform plugin only. The product it belongs to
is described in `2026-10-11-hermes-chat-design.md` (the product spec), and the
server it talks to in `2026-10-11-chat-server-architecture-design.md`,
`docs/openapi.yaml`, and `README.md`. Where this document and the product spec
disagree about the plugin, this one wins.

Decisions are marked *(Kai)* when Kai made them and *(Claude's call)* when
Claude chose and Kai has not objected. Change a Claude's call freely if there
is a reason.

## 1. Intent

The server and the web UI are built and have only ever talked to the fake
agent. The plugin is the last part: it lets a real Hermes agent sit in a room.
Until it exists nothing can be tested end to end.

Kai installs the plugin by hand on another machine, where Hermes runs as the
desktop app (the command-line gateway later), and tries it against the deployed
chat server. Claude cannot reach that machine. Each try therefore costs Kai
time, so the plugin has to arrive already tested as far as it can be without a
model, and the first try has to answer the questions that only a real Hermes
can answer.

## 2. Stages

*(Kai: the first sitting only needs to be a chat with one agent, if that
derisks the rest. Claude's call: the split below.)*

- **Stage 1, the first sitting: chat.** One agent connects, receives a
  message, replies with streaming text, and reports the turn finished. It
  survives a dropped connection, and a turn interrupted by a gateway restart
  continues afterwards. It also logs what
  Hermes's tool and remark hooks deliver, without sending any of it anywhere.
  Sections 4 to 9 specify this stage and are the subject of the first plan.
- **Stage 2, after the first sitting.** Tool steps; stop, pause,
  and steer; approval and clarify prompts; the note and the suggest-an-entry
  tool; file uploads; home-room delivery for scheduled jobs. Section 10 gives
  the intended mechanism for each. It is provisional: it is revised with what
  the first sitting shows, and gets its own plan.

## 3. Goals, non-goals, anti-goals

### Goals (stage 1)

- A human posts in a room, the agent's reply appears and streams, and the
  room's queue moves on to the next message.
- No turn is left open. Every delivery the plugin accepts ends reported `done`
  or `failed`, including when Hermes fails, when the turn is cancelled, and
  when the gateway process is restarted mid-turn.
- The first sitting produces a log that shows whether the stage 2 mechanisms
  in section 10 will work on Kai's install.
- Kai can install it from written steps without asking Claude anything.

### Non-goals (stage 1)

- Anything in the stage 2 list. In stage 1 the plugin receives `stop`,
  `pause`, `steer`, and `prompt.answered` events and only logs them.
- Answering an agent's approval request or clarifying question. Hermes posts
  the question as plain text, but the reply cannot reach it (section 8,
  step 4), so the question times out.
- Changes to the chat server. The agent API already has every route the plugin
  needs, for both stages.
- Changes to Hermes.
- Packaging for anyone but Kai (no PyPI release, no catalog entry).

### Anti-goals

- **A room that silently stops delivering.** The server releases the next
  message for an agent in a room only when the current turn is reported `done`
  or `failed`. A missed report stalls that room with no error anywhere. This is
  the failure the design works hardest against (section 6).
- **Tool chatter posted as chat messages.** By default Hermes shows tool
  progress and between-step commentary by sending ordinary messages. In a room
  those would be agent messages: noise, and possibly a wake-up for another
  agent. See section 8.
- **A plugin that needs the off switch to be obeyed.** The server enforces
  pause on its own. Nothing here may be load-bearing for that.
- **Secrets in logs.** The token is never logged. The hook probe logs names
  and identifiers, never tool arguments or message text.

## 4. What it is built on

- **Hermes release `v0.21.7`** (`NousResearch/hermes-agent`, commit
  `71d34bad`). Kai's install follows release tags, not `main`, so the release
  it runs is the target. The document was first written against the `main`
  commit `6e7b83a2` of 2026-10-13, checked out at `~/Dev/hermes-agent` on the
  development machine; the behaviour in sections 6 and 9 was seen running at
  both. A new Hermes release is checked with the local gateway run in
  section 11 before Kai updates.
- **The plugin directory is `plugin/hermes_chat/`** in this repository, as the
  architecture spec places it. Installing is copying or linking that directory
  to `~/.hermes/plugins/hermes-chat/` and enabling it. A user plugin is loaded
  at startup only when it is listed under `plugins.enabled`; only Hermes's
  bundled platform plugins are loaded lazily.
- **Language and libraries** *(Claude's call)*: Python, the version Hermes runs
  (3.14). The only HTTP library is `httpx`, which is a core Hermes dependency,
  so the plugin installs nothing. The event stream is read with `httpx`
  streaming and a small frame parser of the plugin's own.
- **Platform name** *(Claude's call)*: `hermes_chat`, label "Hermes Chat".
- **Settings** *(Claude's call on names)*. Every one is read per profile, with
  `get_scoped_secret` / `extra_or_secret` and never `os.getenv`. When one
  gateway serves several profiles, each profile's settings come from its own
  `.env` (`build_profile_secret_scope`), and a value it does not set is not
  taken from the default profile's `.env` or from the process environment. So
  all of them go in each profile's `.env`. They differ in whether the value
  changes from one profile to the next. (Two exceptions exist and the setup
  does not rely on either: a gateway serving a single profile also sees its
  process environment, and Hermes has an administrator-managed env file that
  is applied to every profile.)

  The same value in every profile on a gateway (repeated in each `.env`):

  | Env var | Meaning |
  |---|---|
  | `HERMES_CHAT_URL` | Base URL of the chat server. Required. |
  | `HERMES_CHAT_ALLOW_ALL_USERS` | Set to `true` in the documented setup: who may talk to the agent is the chat server's decision. |
  | `HERMES_CHAT_ALLOWED_USERS` | Registered because Hermes expects it; not used by the documented setup. |

  A different value in each profile:

  | Env var | Meaning |
  |---|---|
  | `HERMES_CHAT_TOKEN` | The bearer token of the chat-server agent this profile is. Required. Two profiles must not share one; the plugin refuses the second. |
  | `HERMES_CHAT_HOME_ROOM` | A room this profile's agent is in, normally Kai's DM with it. Optional. It is the fallback for output with no room to go back to: a scheduled job created outside any room, and Hermes's own notices to the profile. A job created in a room delivers to that room and does not use it. Registered in stage 1, exercised in stage 2. |

  These share a prefix with the server's own settings. They are read on a
  different machine by a different program, and none of the names collide.

## 5. Parts

*(Claude's call on the split; the builder may adjust file boundaries.)*

- **`plugin.yaml`**: `kind: platform`, with the env vars above in
  `requires_env` / `optional_env` so they appear in Hermes's settings screens.
- **Client** (no Hermes imports): the HTTP calls to the agent API, the event
  stream reader (frame parser, cursor, reconnect, silence watchdog), and the
  retry rule for turn reports. Because it imports nothing from Hermes its tests
  run anywhere.
- **Adapter**: a `BasePlatformAdapter` subclass that turns stream events into
  Hermes `MessageEvent`s and Hermes's calls into client calls.
- **Open-turn record**: a small file of the deliveries this agent has accepted
  and not yet reported finished, with each one's room (section 6).
- **Hook probe**: stage 1 only (section 9).
- **`register(ctx)`**: registers the platform and the probe hooks.

## 6. Behaviour (stage 1)

### Connecting

`connect()` takes the scoped token lock (`acquire_scoped_lock`) so two profiles
cannot share a token, calls `GET /agent/me`, and starts the stream task. A 401
is a configuration error: `connect()` fails and says so, and the plugin does
not retry it. Any other failure to reach the server is retried by the stream
task with backoff, and `connect()` still succeeds, so a server that is down
when the gateway starts does not disable the platform.

The stream task holds `GET /agent/events` open. It sends its cursor as
`Last-Event-ID` when it has one. It reconnects with a growing, capped delay
after any end or error, and treats 60 seconds without a byte (the server sends
a heartbeat comment every 25) as a dead connection. A 401 on the stream means
the token was revoked: the task stops and marks the adapter as failed with a
non-retryable error.

The cursor is kept in memory only. After a gateway restart the stream opens
with no cursor, which starts at the end of the log; that is safe because on
every connect the server hands over again every delivery still `delivered`.

### A delivery

For each `delivery` frame:

1. Drop it if the message ID was seen (`MessageDeduplicator`; delivery is at
   least once).
2. Write the message ID and room ID to the open-turn record.
3. Build the source with `build_source`: `chat_id` is the room ID, `chat_name`
   the topic, `chat_type` `"dm"` for a DM and `"group"` otherwise, `user_id`
   and `user_name` from the author, `is_bot` from the author's `is_bot`. A
   message whose author is `null` (a removed human) gets a fixed placeholder
   name and an empty user ID.
4. Build the `MessageEvent`: `text`, `message_id`, the three reply-to fields as
   the server sent them, `channel_prompt` from `note` (omitted when `null`),
   and `channel_context` from `context`, one line per message as
   `name: text`, oldest first (omitted when empty).
5. Set `allow_gateway_control` on the event to true only when the author is
   a human *(Kai)*. A human can type `/new`, `/stop`, and Hermes's other
   commands in a room; text from another agent is always conversation, so an
   agent can never approve a command or reset a session.
6. Call `handle_message(event)`.

**One Hermes session per room** *(product spec; Claude's call on the
mechanism)*. `chat_id` being the room ID is enough for a DM. For any other
room Hermes by default adds the author's ID to the session key
(`group_sessions_per_user`, on by default), which would give each human and
each agent author a separate conversation in the same room. The adapter
therefore sets the source's `thread_id` to the room ID for non-DM rooms:
Hermes shares a thread's session between its participants. The global setting
is left alone because it would change Kai's other platforms. The builder
checks what else `thread_id` touches (it comes back in `metadata` on sends)
and that the session key is the same for two different authors in one room.

**One delivery per room at a time, inside the adapter too** *(Claude's call)*.
The server releases the next message as soon as `done` arrives, but Hermes
frees the room's session only after `on_processing_complete` returns. A
delivery handed to Hermes in that gap meets a busy session: Hermes may post a
"queued" notice into the room, interrupt or steer the running turn, or drop
the message, and a dropped message gets no hooks and stalls the room. So the
adapter holds a room's next delivery until Hermes has finished with the
previous one for that room. Rooms do not wait on each other.

### The turn

- `on_processing_start(event)`: `POST /agent/deliveries/{id}/working`.
- `send(chat_id, content, reply_to)`: `POST /agent/rooms/{room}/messages`, with
  `reply_to_id` when Hermes gives one. Returns the new message's ID.
- `edit_message(chat_id, message_id, content, finalize)`:
  `PUT /agent/messages/{id}` with `final` set from `finalize`. This is how
  Hermes streams: one `send`, then edits, the last one final.
- `on_processing_complete(event, outcome)`: `done` for `SUCCESS` and
  `CANCELLED`, `failed` for `FAILURE`. Then the delivery is removed from the
  open-turn record.

Three settings on the adapter class make the above true at the pinned commit:

- **No splitting.** `max_message_length=0` is registered, and the class's
  `MAX_MESSAGE_LENGTH` is set to the server's message limit (262,144 bytes),
  because the stream consumer reads the class attribute and treats 0 as 4,096.
- **Always a final edit.** `REQUIRES_EDIT_FINALIZE = True`, because otherwise
  the stream consumer skips the final edit when the text has not changed and
  the message would stay `streaming` until the turn's `done` closes it.
- **Message length** is counted the way the server counts it (bytes), if
  Hermes lets the adapter say so; the builder checks.

### Never leaving a turn open

- **Turn reports are retried until they succeed.** `working`, `done`, and
  `failed` are retried with backoff across connection loss for as long as the
  adapter is connected. A 404 or 409 ends the retrying: the server no longer
  has that turn open, which is the state wanted. Reports for one delivery go
  out in order.
- **Content calls are not.** A `send` or edit that fails after a short retry
  returns a failed `SendResult` and Hermes carries on. A 409 `paused` is
  returned as a failure at once and is expected, not an error to log loudly.
- **A turn interrupted by a restart continues** *(Kai)*. When the gateway
  starts again Hermes resumes each interrupted session itself: it calls
  `handle_message` with a synthetic event that is `internal`, has empty text,
  and has no `message_id`. The server, meanwhile, still has that delivery
  `working` and hands over again only deliveries still `delivered`. So:
  - the open-turn record survives the restart, and on `connect()` each
    delivery in it is held as *awaiting resume* for its room;
  - an internal event with no `message_id` for a room with a delivery awaiting
    resume is attached to that delivery: its processing hooks report against
    that delivery, and its reply is sent with `reply_to` that message;
  - a delivery not resumed within a grace period after connect is reported
    `failed` *(Claude's call: a fixed period, about a minute; the builder
    picks the number from how long Hermes takes to schedule resumes)*;
  - while a room has a delivery awaiting resume, the one-at-a-time rule above
    applies to it like any running turn.

  A graceful stop interrupts running turns before `disconnect()`. The
  interrupted turn reaches `on_processing_complete` as `SUCCESS` (as
  `CANCELLED` on some paths), with its session marked `resume_pending`. In
  that case the plugin must not report `done`, or the server would release
  the next message into a gateway that answers new work with a canned
  refusal, and the resumed reply would land outside any turn. The adapter
  leaves the delivery open when the gateway is draining and the session is
  marked `resume_pending`, or when it is shutting down and the outcome is
  `CANCELLED`. A delivery that arrives while the gateway is draining is not
  handed to Hermes and not recorded: the server still has it `delivered` and
  hands it over again on the next connect. The record lives under the
  profile's Hermes home.
- **The processing hooks are not the turn.** Hermes runs
  `on_processing_start` and `on_processing_complete` around an event even
  when it only queues it, and then dispatches the same event again. Two cases
  were seen: a message that arrives while the gateway is still starting is
  held in the gateway's startup queue and replayed when startup is over; and
  Hermes's own resume event after a restart is put back into the session's
  pending slot once or twice, because the session is already claimed for the
  resume. In both, the first pass ends as `SUCCESS` within milliseconds and
  nothing has run. The adapter closes a turn only on a pass after which the
  event is in neither place.
- **A delivery Hermes refuses** still ends. The authorization check runs
  inside Hermes's message handler, after `on_processing_start`, so a refused
  message goes through both processing hooks and is reported `done`. The
  builder looks for any path where `handle_message` accepts an event and
  neither hook runs (a dropped or merged event), and reports such a delivery
  `failed`.

### Control events

`stop`, `pause`, `steer`, and `prompt.answered` are logged with their room ID
and otherwise ignored in stage 1. The server's pause holds regardless.

### Disconnecting

`disconnect()` stops the stream task, stops retrying, releases the token lock,
and leaves the open-turn record in place for the next start.

## 7. Registration

`register_platform` with: `max_message_length=0` (and the class attribute
described in section 6); a `platform_hint` saying
markdown is rendered, long reports are fine, and replies are read later rather
than live; `cron_deliver_env_var="HERMES_CHAT_HOME_ROOM"`;
`allowed_users_env` and `allow_all_env`; `check_fn` and `validate_config` that
are true when URL and token are both set; and an `env_enablement_fn` so setting
the two env vars is enough to enable the platform.

## 8. What Kai sets up

Written as `plugin/README.md`, in order, for one profile:

1. On the chat server: `agent add`, copy the token; add the agent to a room.
2. Copy or link `plugin/hermes_chat` to `~/.hermes/plugins/hermes-chat` and
   add it to `plugins.enabled`.
3. In the profile's `.env`: `HERMES_CHAT_URL`, `HERMES_CHAT_TOKEN`,
   `HERMES_CHAT_ALLOW_ALL_USERS=true`.
4. In the profile's `config.yaml`:
   - streaming on for the gateway;
   - under `display.platforms.hermes_chat`: `tool_progress: off` and
     `interim_assistant_messages: false`, so Hermes does not post tool
     progress and commentary as chat messages (stage 2 sends them to the
     server as activity instead);
   - `approvals.timeout` left at Hermes's default (300 seconds, then treated
     as denied) and `agent.clarify_timeout: 120`. In stage 1 a question from
     the agent cannot be answered: the server holds the human's reply in the
     room's queue until the turn ends, and the turn is waiting for that
     reply. Short timeouts let such a turn give up and carry on. Stage 2 adds
     prompts and then sets the long values the product spec requires
     (`approvals.timeout` raised, which Hermes clamps at about a year, and
     `agent.clarify_timeout: 0`).
5. Restart the gateway.

The plugin cannot enforce step 4's display settings, so at `connect()` it
reads the profile's config and logs a warning naming each one that is not as
documented. *(Claude's call.)*

The README ends with **the first-sitting checklist**: what to do, what should
appear in the room, and which log lines to send back. It covers: a plain
question; a long streamed reply; a question that makes the agent use a tool; a
second message sent while the first is being answered (it should queue, then
be answered); stopping the gateway mid-reply and starting it again (the turn
should stay open, then finish once Hermes resumes it, and the next message
should then be answered); `/new` typed by a human (the agent's conversation in
that room starts over); and a message
sent while the gateway is stopped (answered once it starts).

## 9. The hook probe (stage 1 only)

The product spec assumed the adapter receives Hermes's structured stream events
(`ToolCallChunk`, `ToolCallFinished`, `Commentary`). At the pinned commit those
types are defined and nothing in the gateway produces them. Stage 2 therefore
rests on Hermes's plugin hooks, which have been read but not seen running.

`register(ctx)` registers `pre_tool_call`, `post_tool_call`, and
`on_interim_message`. Each callback writes one log line and returns nothing:
the hook's name, the tool's name where there is one, the names of the keyword
arguments it received, the session, turn, and tool-call identifiers, and what
Hermes's session context reports for platform and chat ID on that thread. It
logs no argument values and no text. It does nothing for sessions on other
platforms beyond that same line.

The plugin also logs each of its own adapter calls (`send`, `edit_message`
with its `finalize` flag, `on_processing_start`, `on_processing_complete` with
its outcome) at the same level with the room and message IDs.

Together these answer, from one conversation's log:

- Do the hooks fire in the gateway process, and can a hook call be tied to a
  room and to the delivery being worked on?
- Does Hermes always end a streamed reply with a final edit?
- Is `on_processing_complete` called exactly once per turn, after the reply is
  sent, for success, failure, and cancellation?

The probe is removed when stage 2 replaces it.

### What the first sitting showed

From Kai's log of 2026-10-13 and from the same turns run locally against a
real gateway with a scripted model (section 11):

- **The tool hooks fire in the gateway process.** `pre_tool_call` and
  `post_tool_call` carry the tool's name, a `tool_call_id`, and on the post
  call `duration_ms` and `status`; Hermes's session context gives the
  platform and the room. Calls made in parallel interleave and are matched by
  `tool_call_id`. The room gives the delivery, because the adapter runs one
  turn per room.
- **`on_interim_message` fires once per stretch of commentary**, just before
  that step's `pre_tool_call`, with the text and `already_streamed`. Hermes
  calls it from a worker thread as a plain function; a coroutine function
  registered for it is created and dropped, which is why the first log had no
  such line.
- **Commentary reaches the room as chat messages regardless.** With streaming
  on, the text before a tool call is streamed as a reply and closed at the
  tool call, so one turn posts one message per stretch of commentary and one
  for the answer. `interim_assistant_messages: false` does not change this.
- **A streamed reply ends with a final edit**, sent twice a few milliseconds
  apart. A stretch of commentary sometimes gets none and is closed by the
  turn's `done`.
- **`on_processing_complete` is called once per pass, not once per turn**, and
  a turn Hermes interrupts or a model call that fails both end as `SUCCESS`
  (section 6). `FAILURE` was seen only for an exception in the handler.
- **The streaming cursor (`▉`) is part of the message text** while a reply
  streams. A reply cut off with no final edit keeps it.
- **Hermes posts its own notices as messages**: a shutdown notice to each
  active room and to the home room (twice in a room that is both), a "no home
  channel is set" notice in the first room when `HERMES_CHAT_HOME_ROOM` is
  unset, and a "Confirm /new" message that waits for `/approve`, `/always` or
  `/cancel` typed as a message.

## 10. Stage 2 (provisional)

Recorded so the stage 1 structure does not block it. Each line is the intended
mechanism at the pinned release, revised with section 9's findings.

- **Tool steps**: `pre_tool_call` → `POST /agent/deliveries/{id}/tools` (name,
  preview, arguments); `post_tool_call` → `PUT /agent/activities/{id}` with its
  `duration_ms` and status. The delivery is the open one for the room in the
  hook's session context; the activity is found again by `tool_call_id`. A
  hook call for a room with no open delivery is logged and sent nowhere.
- **Remarks**: not in stage 2 *(Kai, 2026-10-13: leave commentary as messages
  for now)*. The hook that would feed them works, but Hermes has already
  posted the same text as a chat message by the time it fires, and the agent
  API has no way to withdraw a message or turn one into a remark. Doing it
  properly means either holding streamed text back in the adapter, which
  costs live replies, or a server route that converts a posted message.
  Stage 2 trims the blank lines Hermes leaves at the start of a later
  stretch and otherwise leaves these messages alone.
- **Stop and pause**: `cancel_session_processing(session_key)` on the adapter
  for that room's session. Dispatching `/stop` as a message is the fallback.
- **Steer**: Hermes has no adapter call for it. Hand Hermes a `/steer <text>`
  message for that room; Hermes queues it as a follow-up when no turn is
  running.
- **Prompts**: override `_send_exec_approval_prompt` and `send_clarify` to post
  a prompt; resolve `prompt.answered` through
  `tools.approval.resolve_gateway_approval` and
  `tools.clarify_gateway.resolve_gateway_clarify`; expire a prompt Hermes gives
  up on.
- **The note**: already delivered as `channel_prompt` in stage 1. Stage 2 adds
  the tool that posts a suggested entry.
- **Uploads**: `send_document`, `send_image_file` and relatives →
  `POST /agent/rooms/{room}/attachments`, then a message naming the IDs.
- **Home room**: unprompted `send` to `HERMES_CHAT_HOME_ROOM` already works
  through stage 1's `send` (Hermes's startup and shutdown notices arrive
  there); stage 2 checks it with a real scheduled job.
- **Confirming a command**: Hermes's "Confirm /new" message becomes a prompt
  like an approval *(Kai, 2026-10-13)*, answered through `prompt.answered`
  instead of by typing `/approve`. The plan finds the adapter call Hermes
  makes for it.
- **The shutdown notice** stays a message *(Kai, 2026-10-13)*.
- **The streaming cursor** is stripped by the adapter from what it sends
  *(Kai, 2026-10-13; done in stage 1)*, so a cut-off reply does not keep it.
  Text that is only the cursor is sent as it is, because the server refuses a
  blank message.

## 11. Testing

*(Claude's call.)* Nothing here needs a model or Kai's machine.

- **Client tests**: frame parsing (split frames, comments, multi-line data),
  cursor handling, reconnect and the silence watchdog, and the retry rule for
  turn reports, against a local stub HTTP server.
- **Adapter tests** against the real Hermes classes from the checkout: a
  `delivery` frame becomes the expected `MessageEvent`; a repeat is dropped;
  the processing hooks produce `working` then `done` or `failed`; a delivery
  in the open-turn record is attached to a resume event, and failed when none
  comes; a shutdown cancellation leaves the delivery open; a second delivery
  for a room waits for the first; an agent-authored message cannot run a
  command; a 401 stops the stream for good.
  Hermes's message handler is replaced by a fake that returns or streams fixed
  text, so the base adapter's own turn machinery runs unmodified.
- **End to end** against a real `hermes-chat` server built from this
  repository, started on a temporary database: a human posts through the human
  API, the adapter (with the fake handler) replies, and the test reads the
  room back to see the reply and the delivery `done`; then the same with the
  server restarted mid-turn and with the adapter restarted mid-turn.

- **A real gateway, locally** *(added after the first sitting)*. The checks
  above replace Hermes's message handler, so none of them saw how Hermes
  loads a plugin or when it calls the hooks, and three defects reached Kai's
  machine. Hermes's own test suite has a scripted model server
  (`tests/fakes/fake_llm_provider.py`); with it, a gateway started from the
  checkout on a temporary Hermes home, with the plugin linked in and a real
  `hermes-chat` server, runs whole turns with no model: commentary before a
  tool call, a message waiting at startup, a stop mid-reply and the resume
  after it. Stage 2 adds this as a test tier and runs it against the release
  Kai's install is on.

`just plugin-check` runs them with `uv`, with the Hermes checkout on the
import path (location from an env var, default `~/Dev/hermes-agent`). It is
not part of `just check`, which must keep working without a Hermes checkout;
the client tests need none and the others skip with a clear message when it is
absent. Tool versions follow `README.md` (Python 3.14.4, uv 0.11.31).

## 12. Left to the builder

- File boundaries inside `plugin/hermes_chat/`, and the layout of tests.
- Backoff numbers, the dedup window, and the log format, within the behaviour
  above.
- The open-turn record's file format and exact location under the profile's
  Hermes home.
- How the end-to-end test starts and stops the server (the Go e2e tests and
  `internal/fakeagent` show the calls).
- The wording of `platform_hint` and of the README.

## 13. Documentation

`plugin/README.md` (section 8). `AGENTS.md` gains the plugin's directory, its
check command, and the rule that the client module imports nothing from
Hermes. The root `README.md`'s tool list and its "not the Hermes plugin" note
about the fake agent are updated to point at the plugin.
