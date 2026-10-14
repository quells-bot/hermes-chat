# Hermes Chat plugin for Hermes

A Hermes platform plugin that lets one real Hermes agent work in a Hermes Chat
room: a message reaches the agent as an event, the reply streams back with its
tool steps, the room's stop, pause and steer reach the running turn, the
agent's questions are asked as prompts, and every turn is reported finished so
the room's queue moves on. It targets
Hermes release `v0.21.7` (commit `71d34bad`; also run at the `main` commit
`6e7b83a2`) and was tested against the `hermes-chat` server built from this
repository.

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
approvals:
  timeout: 31536000                     # one year, the most Hermes accepts
agent:
  clarify_timeout: 0                    # no limit
```

Why: with streaming on, replies appear as they are written. With
`tool_progress` off, Hermes does not post tool progress as chat messages; the
plugin records each tool under the message being answered instead. The two
timeouts are long because a question from the agent is a prompt in the room
and waits for a person: with Hermes's defaults an approval is treated as
denied after 300 seconds and a clarifying question gives up after an hour.

The plugin cannot enforce these settings, so at `connect()` it reads the
profile's config and logs a warning naming each display setting that is not
as above. `checks.display_warnings` in `plugin/hermes_chat/checks.py` accepts
exactly: `tool_progress` as the string `"off"` (quoted, so YAML does not read
it as a boolean), `interim_assistant_messages` as the boolean `false`, and
`streaming.enabled` as the boolean `true`. A boolean written as a YAML string
(`"false"`, `"off"`) is not accepted as the boolean; the warning says so.

## What works and what to expect

- **Chat.** A human posts in a room, the agent's reply appears and streams,
  and the room's queue moves on to the next message. Every delivery the
  plugin accepts ends reported `done` or `failed`, including when the gateway
  is stopped mid-reply: the turn stays open, Hermes resumes it after the next
  start, and the next message is answered after that.
- **Tool steps.** Each tool the agent runs is recorded under the message it
  is answering, with its name, a preview of its arguments, how long it took
  and whether it succeeded.
- **Stop, pause and steer.** Stopping a turn or pausing the room ends the
  running turn (Hermes posts "⚡ Stopped. You can continue this session.").
  A steer reaches the running turn as Hermes's `/steer`.
- **Questions from the agent.** An approval, a clarifying question, and a
  command's confirmation (such as `/new`) are each asked as a prompt in the
  room, and the answer you pick there is what Hermes gets. A prompt the turn
  no longer waits on is closed.
- **The note.** The agent can suggest an entry for the note of the person it
  is answering (`suggest_note_entry`, which Hermes offers through its
  tool-search bridge). The entry waits on the note screen for approval.
- **Files.** A file the agent sends is uploaded to the room and posted with
  its caption. A file over 25 MiB, or one Hermes's own path check refuses, is
  not sent.
- **The home room.** Output with no room to go back to (a scheduled job
  created outside any room, Hermes's own notices) goes to
  `HERMES_CHAT_HOME_ROOM`.

What to expect that may surprise:

- **Commentary is posted as messages.** When the agent writes something
  before using a tool, that text is its own message, so one turn can leave
  several.
- **Hermes's notices are messages.** "Hermes is shutting down", "No home
  channel is set" and the like are posted by Hermes as ordinary agent
  messages. A clarifying question carries Hermes's own hint about skipping.
- **A model error is not a failed turn.** Hermes posts its own error message
  and ends the turn normally, so the delivery shows `done`.
- **A prompt asked before a gateway restart cannot be answered after it.**
  Hermes is no longer waiting on it; the answer is logged and ignored.

## Where the logs are

The plugin logs under `hermes_chat.adapter` (each send, edit, prompt, file,
control event and turn report), `hermes_chat.steps` (tool steps), and
`hermes_chat.hooks` and `hermes_chat.note_tool` (failures only, at debug).
Lines carry rooms and ids and tool names; never message text, prompt text,
answers, tool arguments, file names, or the token.

Hermes writes the gateway log under the Hermes home: `~/.hermes/logs/agent.log`
for scheduler and gateway messages and `~/.hermes/logs/errors.log` for
warnings (`website/docs/guides/cron-troubleshooting.md`). If you run the
desktop app and want the same files without a shell, its diagnostics screen
shows where its logs live.

## Second-sitting checklist

Each item: what to do, what should appear in the room, and which log lines
to send back if it does not.

1. **A question that uses a tool.** *Do*: ask something the agent answers
   with a tool. *Expect*: the step under your message, with a duration.
   *Send back*: the `tool_started` and `hermes_chat.steps` lines.
2. **Stop.** *Do*: ask for a long answer and press Stop. *Expect*: the reply
   stops growing, "⚡ Stopped." appears, and your next message is answered.
   *Send back*: the `stop for room` line and the turn's report lines.
3. **Steer.** *Do*: during a turn that uses several tools, send a steer.
   *Expect*: the agent takes it into account after its next tool. *Send
   back*: the `steer` line.
4. **An approval.** *Do*: ask for something Hermes holds for approval, such
   as a recursive delete of a scratch directory. *Expect*: a prompt with
   once, session, always and deny; the turn continues with what you pick. Try
   each answer once. *Send back*: the `prompt` lines.
5. **A clarifying question.** *Do*: ask something ambiguous and tell the
   agent to ask you first. *Expect*: a prompt with its choices; your answer
   reaches the agent.
6. **`/new`.** *Do*: type `/new`. *Expect*: a "Confirm /new" prompt; choosing
   once starts the conversation over.
7. **A note entry.** *Do*: tell the agent a lasting preference and ask it to
   remember it. *Expect*: a pending entry on the note screen.
8. **A file.** *Do*: ask the agent to write a small file and send it.
   *Expect*: a message with the attachment, which downloads.
9. **A scheduled job.** *Do*: with `HERMES_CHAT_HOME_ROOM` set, create a
   scheduled job from Hermes's own interface, outside any room. *Expect*: its
   output in the home room. This one has no automated test.
10. **Stopping the gateway mid-reply.** *Do*: stop the gateway while a reply
    streams, post a second message, start the gateway. *Expect*: the first
    reply is resumed, then the second message is answered.

## Development

`just plugin-check` runs the plugin's tests with `uv` and the Hermes checkout
on the import path. The checkout's location comes from `HERMES_SRC`
(default `~/Dev/hermes-agent`); with no checkout the client tests still run
and the others skip with a clear message. The client modules (`sse.py`,
`client.py`, `stream.py`, `reports.py`, `record.py`, `checks.py`, `steps.py`,
`prompts.py`) import nothing from Hermes, so they run anywhere.

The gateway tier (`plugin/tests/test_gateway.py`, `just plugin-check -m gateway`)
starts a real Hermes gateway from the checkout on a temporary Hermes home,
with this plugin linked in, a real `hermes-chat` server, and Hermes's scripted
model server (`tests/fakes/fake_llm_provider.py`), so whole turns run with no
model. It is how the plugin is checked against the Hermes release it targets:
check that release out beside the main checkout and run the tier with both
`HERMES_SRC` and `PYTHONPATH` pointing at it, using the main checkout's
interpreter.

```
git -C ~/Dev/hermes-agent worktree add --detach /tmp/hermes-v0.21.7 v0.21.7
cd plugin && HERMES_SRC=/tmp/hermes-v0.21.7 PYTHONPATH=/tmp/hermes-v0.21.7 \
  ~/Dev/hermes-agent/.venv/bin/python -m pytest -m gateway
```
