# Hermes Chat plugin for Hermes

A Hermes platform plugin that lets one real Hermes agent chat in a Hermes Chat
room: a message reaches the agent as an event, the reply streams back, and
every turn is reported finished so the room's queue moves on. It targets the
Hermes checkout at commit `6e7b83a2` and was tested against the `hermes-chat`
server built from this repository.

## Install

Do this once, on the machine that runs Hermes.

1. **On the chat server**, create the agent and copy its token:

   ```
   hermes-chat agent add <handle> "<Display Name>"
   ```

   The output prints the token once, on a line that starts with `token:`.
   Add the agent to a room, or open its DM with it, so there is a place to
   talk.

2. **Copy or symlink the plugin directory** to the Hermes home:

   ```
   ln -s <path to this repository>/plugin/hermes_chat ~/.hermes/plugins/hermes-chat
   ```

   A user-installed plugin loads only when it is listed under
   `plugins.enabled` in `config.yaml` (the allow-list is enforced in
   `hermes_cli/plugins_discovery.py`, `gate_manifest`; the entry is the
   manifest's `name:` key, `hermes-chat`). Add it literally:

   ```yaml
   plugins:
     enabled:
       - hermes-chat
   ```

   `hermes plugins enable hermes-chat` writes the same entry.

3. **Set the profile's `.env`** (section "Settings" below).

4. **Set `config.yaml`** (section "`config.yaml`" below).

5. **Restart the gateway.**

## Settings

Every value is read per profile with Hermes's scoped secret reader
(`get_scoped_secret`), never with `os.getenv`. When one gateway serves
several profiles, each profile's settings come from its own `.env`, and a
value a profile does not set is not taken from the default profile's `.env`
or from the process environment. So each profile's `.env` needs all of them.

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
| `HERMES_CHAT_HOME_ROOM` | A room this profile's agent is in, normally its DM with you. Optional. It is the fallback for output with no room to go back to (a scheduled job created outside any room, and Hermes's own notices). |

## `config.yaml`

```yaml
streaming:
  enabled: true
display:
  platforms:
    hermes_chat:
      tool_progress: "off"              # quoted: YAML would read `off` as a boolean
      interim_assistant_messages: false
agent:
  clarify_timeout: 120
```

`approvals.timeout` is left at its default (300 seconds, then treated as
denied). Why: with streaming on, replies appear as they are written; with
`tool_progress` and `interim_assistant_messages` off, Hermes does not post
tool progress and between-step commentary as chat messages (stage 2 sends
them to the server as activity instead). In stage 1 a question from the
agent cannot be answered: the server holds the human's reply in the room's
queue until the turn ends, and the turn is waiting for that reply. Short
timeouts let such a turn give up and carry on; stage 2 adds prompts and then
sets the long values.

The plugin cannot enforce these settings, so at `connect()` it reads the
profile's config and logs a warning naming each one that is not as above.
`checks.display_warnings` in `plugin/hermes_chat/checks.py` accepts exactly:
`tool_progress` as the string `"off"` (quoted, so YAML does not read it as a
boolean), `interim_assistant_messages` as the boolean `false`, and
`streaming.enabled` as the boolean `true`. A boolean written as a YAML string
(`"false"`, `"off"`) is not accepted as the boolean; the warning says so.

## What works and what does not yet

Works: a human posts in a room, the agent's reply appears and streams, and
the room's queue moves on to the next message; no turn is left open — every
delivery the plugin accepts ends reported `done` or `failed`, including when
Hermes fails, when the turn is cancelled, and when the gateway process is
restarted mid-turn; the first sitting produces a log that shows whether the
stage 2 mechanisms will work; you can install it from these steps without
asking anyone.

Does not yet: anything in the stage 2 list — tool steps and remarks shown in
the room, stop/pause/steer, approval and clarify prompts, the note and the
suggest-an-entry tool, file uploads, home-room delivery for scheduled jobs.
In stage 1 the plugin receives `stop`, `pause`, `steer`, and `prompt.answered`
events and only logs them. In particular the agent's questions cannot be
answered until stage 2: Hermes posts the question as plain text, but the
reply cannot reach it, so the question times out (that is why the timeouts
above are short).

## Where the logs are

The plugin logs under `hermes_chat.adapter` (each send, edit, and turn report
with room and message or delivery ids; never message text) and
`hermes_chat.probe` (the stage 1 hook probe; names and identifiers only,
never tool arguments or text).

Hermes writes the gateway log under the Hermes home: `~/.hermes/logs/agent.log`
for scheduler and gateway messages and `~/.hermes/logs/errors.log` for
warnings (`website/docs/guides/cron-troubleshooting.md`). If you run the
desktop app and want the same files without a shell, its diagnostics screen
shows where its logs live.

## First-sitting checklist

Each item: what to do, what should appear in the room, and which log lines
to send back.

1. **A plain question.** *Do*: post "hello". *Expect*: a reply in the room,
   and the delivery of your message done. *Send back*: the
   `hermes_chat.adapter` lines for that turn (working, send, done).
2. **A long streamed reply.** *Do*: ask something that gets a long answer.
   *Expect*: the reply appears a little at a time and ends complete.
   *Send back*: the `edit_message` lines with `finalize=true` on the last one.
3. **A question that makes the agent use a tool.** *Do*: ask something the
   agent answers with a tool. *Expect*: the reply in the room; the tool run
   is not shown there. *Send back*: the `hermes_chat.probe` lines from that
   turn — do the hooks fire, and can a hook call be tied to a room and to the
   delivery being worked on?
4. **A second message while the first is being answered.** *Do*: post a
   second message before the first reply finishes. *Expect*: the second
   message queues, then is answered after the first. *Send back*: the order
   of the two turns' report lines.
5. **Stopping the gateway mid-reply and starting it again.** *Do*: stop the
   gateway while a reply is streaming; start it again. *Expect*: the reply
   finishes once Hermes resumes the turn, and the next message is then
   answered. *Send back*: the turn's report lines around the restart — is
   `on_processing_complete` called exactly once per turn, after the reply is
   sent, for success, failure, and cancellation?
6. **A message sent while the gateway is stopped.** *Do*: stop the gateway,
   post a message, start the gateway. *Expect*: the message is answered once
   the gateway is back.
7. **`/new` typed by a human.** *Do*: type `/new` in the room. *Expect*: the
   agent's conversation in that room starts over. *Send back*: the turn's
   report lines.

The log answers three questions for stage 2: whether the hooks fire in the
gateway process and can be tied to a room and delivery; whether Hermes always
ends a streamed reply with a final edit; and whether `on_processing_complete`
is called exactly once per turn, after the reply is sent, for success,
failure, and cancellation.

## Development

`just plugin-check` runs the plugin's tests with `uv` and the Hermes checkout
on the import path. The checkout's location comes from `HERMES_SRC`
(default `~/Dev/hermes-agent`); with no checkout the client tests still run
and the adapter tests skip with a clear message. The client modules
(`sse.py`, `client.py`, `stream.py`, `reports.py`, `record.py`, `checks.py`)
import nothing from Hermes, so they run anywhere.
