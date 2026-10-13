# Hermes Chat Plugin Stage 2 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A real Hermes agent in a room shows its tool steps, obeys stop, pause and steer, asks its approval, clarify and command-confirmation questions as prompts, can suggest a note entry, and can send files.

**Architecture:** Everything is in the plugin under `plugin/hermes_chat/`; the server and Hermes are not changed. Hermes's tool hooks and adapter calls are turned into calls on the agent API through the existing `ChatClient`. Because the stage 1 tests replaced Hermes's message handler and so missed how Hermes really loads the plugin and calls its hooks, the first task adds a test tier that runs a real gateway against a scripted model, and every later task proves its behaviour there as well as against the stub server.

**Tech Stack:** Python 3.14, `httpx` (already a Hermes dependency; the plugin installs nothing), pytest with `pytest-asyncio`, the Hermes checkout at `HERMES_SRC` for the adapter and gateway tiers, the `hermes-chat` Go binary for the end-to-end tiers.

**Spec:** `docs/superpowers/specs/2026-10-13-hermes-plugin-design.md` (section 10 is this plan's subject; sections 6 and 9 say how Hermes behaves). The agent API is `docs/openapi.yaml`, paths under `/agent/`.

## Global Constraints

- Target Hermes release `v0.21.7` (commit `71d34bad`). Where this plan names a Hermes function, read it in that release before relying on it.
- No changes to the chat server and none to Hermes.
- The client modules (`sse.py`, `client.py`, `stream.py`, `reports.py`, `record.py`, `checks.py`, and the new `steps.py` and `prompts.py`) import nothing from Hermes (`gateway`, `hermes_cli`, `tools`, `agent`, `hermes_constants`). Only `adapter.py`, `hooks.py` and `note_tool.py` do.
- The only HTTP library is `httpx`.
- Turn reports (`working`, `done`, `failed`) are retried until the server answers; a 404 or 409 ends the retrying. Content calls (post, edit, prompt, tool step, upload, note entry) are never retried by the plugin.
- A 409 `paused` on a content call is expected: logged at info, returned as a failure, never raised into Hermes.
- Nothing in the plugin may be needed for the server's pause to hold.
- The token is never logged. Log lines carry names and identifiers (room, delivery, message, activity, tool name, `tool_call_id`), never tool arguments, message text, prompt text, or file contents. Tool arguments are sent to the server; they are still not logged.
- A hook callback or tool handler never raises into Hermes; it logs at debug and returns.
- Hook callbacks are plain functions, not coroutine functions (Hermes drops a coroutine returned by a stream hook).
- Remarks are not built: commentary stays as chat messages *(Kai, 2026-10-13)*. The shutdown notice stays a message *(Kai, 2026-10-13)*.
- Run `just plugin-check` before each commit; `just check` must keep passing with no Hermes checkout.

## Review Focus

1. A tool hook fires for a room with no open delivery (a scheduled job, a turn already closed): nothing is sent and nothing raises. Pinned in Task 3.
2. A tool's start call fails (room paused, server unreachable): its finish is dropped quietly, later tools still report, and nothing retries. Pinned in Task 3.
3. A `stop`, `pause` or `steer` arrives for a room with no running turn: nothing is handed to Hermes, so no turn starts outside a delivery and no "nothing to stop" message is posted. Pinned in Task 4.
4. A `prompt.answered` arrives for a prompt the adapter does not know (answered after a gateway restart, or twice): logged and ignored. Pinned in Task 5.
5. A file to send is larger than 25 MiB, unreadable, or refused by Hermes's path check: a failed `SendResult`, no upload, no message. Pinned in Task 8.

---

## File Structure

| File | Responsibility |
|---|---|
| `plugin/hermes_chat/client.py` (modify) | One method per agent API route used. Gains tool, prompt, note and upload calls. |
| `plugin/hermes_chat/steps.py` (create) | `ToolSteps`: orders a turn's tool start and finish calls on the gateway loop, callable from any thread. Shapes `preview` and `arguments` to the server's limits. No Hermes imports. |
| `plugin/hermes_chat/prompts.py` (create) | `PromptTable`: which open prompt message belongs to which Hermes question. `clarify_choices` fits choices to the server's limits. No Hermes imports. |
| `plugin/hermes_chat/hooks.py` (create, replaces `probe.py`) | Registers `pre_tool_call` and `post_tool_call`; finds the adapter for the hook's room and hands it the step. |
| `plugin/hermes_chat/note_tool.py` (create) | Registers the `suggest_note_entry` tool. |
| `plugin/hermes_chat/adapter.py` (modify) | Control events, prompt overrides, file sends, and the methods `hooks.py` and `note_tool.py` call. |
| `plugin/tests/gateway.py`, `plugin/tests/test_gateway.py` (create) | The real-gateway tier. |
| `plugin/tests/test_steps.py`, `test_prompts.py`, `test_adapter_control.py`, `test_adapter_prompts.py`, `test_adapter_files.py`, `test_hooks.py` (create) | Stub-server and adapter tests per part. `test_probe.py` is deleted with `probe.py`. |

---

### Task 1: The real-gateway test tier

**Files:**
- Create: `plugin/tests/gateway.py`, `plugin/tests/test_gateway.py`
- Modify: `plugin/pytest.ini` (register the `gateway` marker)

**Interfaces:**
- Consumes: `ChatServer` and `chat_binary` from `plugin/tests/conftest.py`; `FakeLLMServer`, `Text`, `ToolCall`, `Error`, `write_hermes_home` from `tests/fakes/fake_llm_provider.py` in the Hermes checkout.
- Produces, in `plugin/tests/gateway.py`:
  - `HERMES_SRC: pathlib.Path` (env `HERMES_SRC`, default `~/Dev/hermes-agent`).
  - `class Gateway`: `__init__(chat: ChatServer, workdir: Path, script: list)`; `start() -> None`; `wait_started(nth: int = 1) -> None` (waits for the nth "Gateway housekeeping started" in `logs/agent.log`); `stop() -> None` (SIGTERM, waits up to 40 s); `log() -> str`; `llm: FakeLLMServer`; `room_id: str` (a room with the bot, created before the gateway starts); `post(text: str) -> str` (as the human, returns the message id); `messages() -> list[dict]`; `state(message_id: str) -> str` (the bot's delivery state); `events: list[tuple[str, dict]]` (every human-stream event, collected by a background thread from `GET /api/events`); `wait(predicate, timeout=60.0) -> None` (raises `AssertionError` on timeout).
  - A pytest fixture `gateway_factory(chat_server, tmp_path)` returning `build(script) -> Gateway`, stopping every gateway it built on teardown.
- The Hermes home it writes: `write_hermes_home(home, llm.base_url, extra_config=...)` with `plugins.enabled: [hermes-chat]`, `streaming.enabled: true`, `display.platforms.hermes_chat.tool_progress: "off"`, `interim_assistant_messages: false`, `terminal.backend: local`; `plugin/hermes_chat` symlinked to `<home>/plugins/hermes-chat`; `HERMES_CHAT_URL`, `HERMES_CHAT_TOKEN`, `HERMES_CHAT_ALLOW_ALL_USERS=true` appended to `<home>/.env`. `HERMES_CHAT_HOME_ROOM` is set only when a test asks for it.
- The process: `<HERMES_SRC>/.venv/bin/python -m hermes_cli.main gateway run --no-supervise`, `cwd=HERMES_SRC`, environment stripped of every `HERMES_*`, `OPENAI_*`, `ANTHROPIC_*` and `OPENROUTER_*` variable, then `HERMES_HOME` and `HOME` set to directories under `workdir`.

- [ ] **Step 1: Write `plugin/tests/gateway.py`** as specified above. The module skips its importers (`pytest.skip(..., allow_module_level=True)`) with a reason naming `HERMES_SRC` when the checkout, its `.venv/bin/python`, or `tests/fakes/fake_llm_provider.py` is missing, and when `go` is not on `PATH`.

- [ ] **Step 2: Write the tests that pin stage 1** in `plugin/tests/test_gateway.py`, each marked `@pytest.mark.gateway`:

```python
COMMENTARY = [
    ToolCall("terminal", {"command": "echo one"}, text="I am about to run the first echo."),
    ToolCall("terminal", {"command": "echo two"}, text="Next I will run the second echo."),
    Text("Both echoes ran."),
]


def index_of(events, predicate):
    return next(i for i, (name, data) in enumerate(events) if predicate(name, data))


def test_plugin_loads_through_the_hermes_loader_and_answers(gateway_factory):
    g = gateway_factory([Text("hello back")])
    g.start(); g.wait_started()
    first = g.post("hello")
    g.wait(lambda: g.state(first) == "done")
    assert [m["text"] for m in g.messages() if m["author"]["kind"] == "agent"] == ["hello back"]
    assert "Failed to load plugin" not in g.log()


def test_message_waiting_at_startup_is_done_after_its_reply(gateway_factory):
    g = gateway_factory([Text("hello back")])
    first = g.post("hello")          # on the server before the gateway exists
    g.start()
    g.wait(lambda: g.state(first) == "done")
    reply = index_of(g.events, lambda n, d: n == "message.posted" and d["message"]["author"]["kind"] == "agent")
    done = index_of(g.events, lambda n, d: n == "delivery.changed" and d["message"]["id"] == first
                    and d["message"]["deliveries"][0]["state"] == "done")
    assert reply < done


def test_stop_mid_reply_resumes_then_answers_the_next(gateway_factory):
    g = gateway_factory([Text("word " * 120, chunk_chars=10, delay_per_chunk=0.25),
                         Text("resumed and finished."), Text("second answer.")])
    g.start(); g.wait_started()
    first = g.post("write a long answer")
    g.wait(lambda: any(m["streaming"] for m in g.messages()))
    second = g.post("and a second question")
    g.stop()
    assert g.state(first) == "working" and g.state(second) == "waiting"
    g.start()
    g.wait(lambda: g.state(first) == "done" and g.state(second) == "done", timeout=120)
    texts = [m["text"] for m in g.messages() if m["author"]["kind"] == "agent"]
    assert texts.index("resumed and finished.") < texts.index("second answer.")
    assert not any("not accepting new work" in t for t in texts)


def test_no_stored_message_keeps_the_streaming_cursor(gateway_factory):
    g = gateway_factory(COMMENTARY)
    g.start(); g.wait_started()
    first = g.post("run the two echoes")
    g.wait(lambda: g.state(first) == "done")
    assert not [m for m in g.messages() if "▉" in m["text"]]
    assert "probe hook=on_interim_message" in g.log()   # removed in Task 3 with the probe
```

- [ ] **Step 3: Run them.** `just plugin-check -m gateway -v`. Expected: 4 passed (they pin behaviour already built). If one fails, the failure is a stage 1 defect: fix it before going on.

- [ ] **Step 4: Run them against the release.** `git -C "$HERMES_SRC" fetch origin tag v0.21.7 && git -C "$HERMES_SRC" worktree add --detach /tmp/hermes-v0.21.7 v0.21.7`, then `HERMES_SRC=/tmp/hermes-v0.21.7 just plugin-check -m gateway`. Expected: 4 passed. Every later task's gateway tests are run both ways.

- [ ] **Step 5: Commit.** `git add plugin/tests/gateway.py plugin/tests/test_gateway.py plugin/pytest.ini && git commit -m "test(plugin): a tier that runs a real gateway against a scripted model"`

---

### Task 2: Trim the blank lines Hermes leaves before a later stretch of text

**Files:**
- Modify: `plugin/hermes_chat/adapter.py` (`_without_cursor`, used by `send` and `edit_message`)
- Test: `plugin/tests/test_adapter_turn.py`, `plugin/tests/test_gateway.py`

**Interfaces:**
- Produces: `_outgoing(content: str) -> str` in `adapter.py`, replacing `_without_cursor`: strips the trailing cursor as now, and strips leading newlines and the spaces between them. Text that would become blank is returned unchanged.

- [ ] **Step 1: Write the failing tests.** In `test_adapter_turn.py`:

```python
async def test_leading_blank_lines_are_trimmed_from_what_is_sent(adapter_factory, stub):
    stub.script("POST", "/agent/rooms/R1/messages", (201, {"id": "M2"}))
    stub.script("PUT", "/agent/messages/M2", (204,))
    a = adapter_factory()
    await a.send("R1", "\n\nNext I will run it. ▉")
    await a.edit_message("R1", "M2", "\n\n  Next I will run it.", finalize=True)
    assert [r.json["text"] for r in stub.requests] == ["Next I will run it.", "Next I will run it."]
    await a._client.aclose()
```

In `test_gateway.py`, add to `test_no_stored_message_keeps_the_streaming_cursor`: `assert not [m for m in g.messages() if m["text"] != m["text"].lstrip()]`.

- [ ] **Step 2: Run them.** `just plugin-check -k "leading_blank or streaming_cursor" -v`. Expected: the new test FAILS with the untrimmed text; the gateway test FAILS on the second commentary message.

- [ ] **Step 3: Implement `_outgoing`** and use it in `send` and `edit_message`. Rename the existing cursor tests' references.

- [ ] **Step 4: Run.** `just plugin-check`. Expected: all pass.

- [ ] **Step 5: Commit.** `git commit -am "feat(plugin): trim the blank lines before a later stretch of a reply"`

---

### Task 3: Tool steps

**Files:**
- Create: `plugin/hermes_chat/steps.py`, `plugin/hermes_chat/hooks.py`, `plugin/tests/test_steps.py`, `plugin/tests/test_hooks.py`
- Modify: `plugin/hermes_chat/client.py`, `plugin/hermes_chat/adapter.py`, `plugin/tests/test_client.py`, `plugin/tests/test_gateway.py`
- Delete: `plugin/hermes_chat/probe.py`, `plugin/tests/test_probe.py`

**Interfaces:**
- Produces in `client.py`:
  - `async def start_tool(self, delivery_id: str, tool: str, preview: str, arguments: str) -> str` — `POST /agent/deliveries/{id}/tools`, returns the activity's `id`.
  - `async def finish_tool(self, activity_id: str, duration_ms: int, succeeded: bool) -> None` — `PUT /agent/activities/{id}`.
- Produces in `steps.py`:
  - `def shape(tool_name: str, args: object) -> tuple[str, str, str]` returning `(tool, preview, arguments)`: `tool` is the name with control characters removed, cut to 64 characters, `"tool"` if that leaves nothing; `arguments` is `json.dumps(args, ensure_ascii=False, default=str)` cut to at most 65,536 UTF-8 bytes on a character boundary; `preview` is the first string value in `args` (the whole JSON when there is none), control characters replaced by spaces, cut to 200 characters.
  - `class ToolSteps`: `__init__(self, client, loop: asyncio.AbstractEventLoop)`; `started(self, delivery_id: str, tool_call_id: str, tool: str, preview: str, arguments: str) -> None`; `finished(self, tool_call_id: str, duration_ms: int, succeeded: bool) -> None`; `async def wait_idle(self) -> None`; `async def aclose(self) -> None`. `started` and `finished` may be called from any thread; they hand the item to one worker task on `loop` with `loop.call_soon_threadsafe`, so a tool's finish is sent after its start. The worker keeps `tool_call_id -> activity id`; a finish with no recorded start is dropped. Each call is tried once; `ApiError` and `Unreachable` are logged (409 at info) and swallowed.
- Produces in `adapter.py`:
  - `self._loop` set in `connect()` to the running loop; `self._steps = ToolSteps(self._client, self._loop)` there; `disconnect()` awaits `self._steps.wait_idle()` for at most 5 s, then `aclose()`.
  - `def tool_started(self, room_id: str, tool_call_id: str, tool_name: str, args: object) -> bool` — False when the room has no open delivery (`self._open_turns.for_room(room_id)` is None); otherwise shapes the step, calls `self._steps.started`, logs `tool_started delivery=… tool=… tool_call_id=…`, returns True.
  - `def tool_finished(self, tool_call_id: str, duration_ms: object, status: object) -> None` — `succeeded` is `status == "ok"`; `duration_ms` is `int(duration_ms or 0)`, floored at 0.
- Produces in `hooks.py`:
  - `def attach(adapter) -> None`, `def detach(adapter) -> None` (a module-level `weakref.WeakSet`); `adapter.py` calls them from `connect()` and `disconnect()`.
  - `def register_hooks(ctx) -> None` registering plain-function callbacks for `pre_tool_call` and `post_tool_call`. Each reads `gateway.session_context.get_session_env("HERMES_SESSION_PLATFORM")` and `"HERMES_SESSION_CHAT_ID"`, returns unless the platform is `hermes_chat`, then offers the call to each attached adapter (`tool_started` until one returns True; `tool_finished` on all). Keyword arguments used: `tool_name`, `tool_call_id`, `args`, and on the post call `duration_ms`, `status`.
- `adapter.register` calls `register_hooks(ctx)` where it called `register_probe(ctx)`.

- [ ] **Step 1: Write the failing client tests** in `test_client.py`: `start_tool` posts `{"tool": "terminal", "preview": "echo hi", "arguments": "{\"command\": \"echo hi\"}"}` to `/agent/deliveries/M1/tools` and returns `"ACT1"` from a `201 {"id": "ACT1", ...}`; `finish_tool("ACT1", 12, True)` puts `{"duration_ms": 12, "succeeded": true}` to `/agent/activities/ACT1`; a 409 from either raises `ApiError` with `status == 409`.

- [ ] **Step 2: Write the failing `steps` tests** in `test_steps.py`:

```python
def test_shape_fits_the_servers_limits():
    tool, preview, arguments = shape("terminal\x00" + "x" * 80, {"command": "echo\thi\n" + "y" * 300})
    assert len(tool) == 64 and "\x00" not in tool
    assert len(preview) == 200 and "\t" not in preview and "\n" not in preview
    assert preview.startswith("echo hi")
    assert json.loads(arguments)["command"].startswith("echo\thi")

def test_shape_cuts_arguments_to_65536_bytes_on_a_character():
    _, _, arguments = shape("t", {"text": "é" * 70_000})
    assert len(arguments.encode("utf-8")) <= 65_536
    arguments.encode("utf-8").decode("utf-8")

def test_shape_without_a_string_argument_previews_the_json():
    assert shape("t", {"n": 3})[1] == '{"n": 3}'

async def test_finish_follows_its_start_and_uses_its_activity(stub): ...
    # started("M1","c1",...) then finished("c1", 12, True) from a worker thread;
    # requests are POST /agent/deliveries/M1/tools then PUT /agent/activities/ACT1

async def test_a_refused_start_drops_its_finish_and_later_tools_still_report(stub): ...
    # first start answers 409; finished("c1") sends nothing; started("M1","c2") then finished("c2") both arrive

async def test_finish_without_a_start_sends_nothing(stub): ...
```

- [ ] **Step 3: Write the failing hook tests** in `test_hooks.py` (they need the Hermes checkout; `importorskip` like the adapter tests): the callbacks registered are plain functions (`inspect.iscoroutinefunction` is false for both); with `get_session_env` patched to platform `telegram` nothing reaches an attached fake adapter; with platform `hermes_chat` and chat id `R1`, `tool_started("R1", "c1", "terminal", {"command": "x"})` is called once; a fake adapter whose `tool_started` raises does not make the callback raise.

- [ ] **Step 4: Write the failing adapter and gateway tests.** In `test_adapter_turn.py`: with a delivery open in `R1`, `a.tool_started("R1", "c1", "terminal", {"command": "echo hi"})` returns True and posts to `/agent/deliveries/M1/tools`; `a.tool_started("R9", ...)` returns False and posts nothing; the log has `tool_started` with the delivery and tool name and does not contain `echo hi`. In `test_gateway.py`:

```python
def test_tool_steps_are_recorded_against_the_turn(gateway_factory):
    g = gateway_factory(COMMENTARY)
    g.start(); g.wait_started()
    first = g.post("run the two echoes")
    g.wait(lambda: g.state(first) == "done")
    acts = g.chat.human.get(f"/api/rooms/{g.room_id}/messages/{first}/activity").json()["activities"]
    assert [a["tool"] for a in acts] == ["terminal", "terminal"]
    assert all(a["succeeded"] is True and a["duration_ms"] >= 0 for a in acts)
    assert "echo one" in acts[0]["preview"]
    assert "echo one" not in g.log_lines("hermes_chat")
```

Read the `Activity` schema in `docs/openapi.yaml` for the exact field names before writing the assertions, and add `log_lines(logger_prefix: str) -> str` to `Gateway`. Replace the `probe hook=on_interim_message` assertion from Task 1 with `assert "hermes_chat.probe" not in g.log()`.

- [ ] **Step 5: Run.** `just plugin-check`. Expected: the new tests fail (missing names, then missing behaviour).

- [ ] **Step 6: Implement** `start_tool`, `finish_tool`, `steps.py`, `hooks.py`, the adapter methods; delete `probe.py` and `test_probe.py`; update the module docstrings that name `probe.py`.

- [ ] **Step 7: Run.** `just plugin-check`, then `HERMES_SRC=/tmp/hermes-v0.21.7 just plugin-check -m gateway`. Expected: all pass.

- [ ] **Step 8: Commit.** `git add -A plugin && git commit -m "feat(plugin): tool steps recorded against the turn"`

---

### Task 4: Stop, pause and steer

**Files:**
- Modify: `plugin/hermes_chat/adapter.py`
- Create: `plugin/tests/test_adapter_control.py`
- Test also: `plugin/tests/test_gateway.py`

**Interfaces:**
- Consumes: the stream's `stop` and `pause` (`{"room_id"}`) and `steer` (`{"room_id", "message": {"id", "text", "author"}}`) events.
- Produces in `adapter.py`:
  - `self._room_sources: dict[str, SessionSource]`, set in `_on_delivery` to the event's source, removed when the room has no open delivery left.
  - `async def _stop_room(self, room_id: str) -> None` — returns after an info log when the room has no open delivery; otherwise hands Hermes `MessageEvent(text="/stop", message_type=MessageType.TEXT, source=self._room_sources[room_id], allow_gateway_control=True)` through `self.handle_message`. Used for both `stop` and `pause`.
  - `async def _steer_room(self, room_id: str, message: dict) -> None` — returns after an info log when the room has no open delivery, or when the message id was seen (`self._dedup`); otherwise hands Hermes `MessageEvent(text="/steer " + message["text"], ..., allow_gateway_control=True)` for that room's source.
  - `_on_stream_event` routes `stop`, `pause`, `steer` to these. Control events carry no `message_id`, so the processing hooks ignore them (`_delivery_for` returns None).
- Decision: `/stop` is dispatched as a command, not `cancel_session_processing`, because Hermes's own command also interrupts the running agent thread and cleans the session's claim (`gateway/run_inbound.py`, the `stop` branch). The gateway test below is the proof; if it fails at `v0.21.7`, call `await self.cancel_session_processing(self._event_session_key(event))` for the open turn's event instead and keep the same tests.

- [ ] **Step 1: Write the failing adapter tests** in `test_adapter_control.py` (helpers copied from `test_adapter_turn.py`): a `stop` frame for `R1` while a handler is blocked in a turn makes the handler see a second event whose text is `/stop` with `allow_gateway_control` true; a `pause` frame does the same; a `steer` frame with text `use the other file` produces an event whose text is `/steer use the other file`; a repeated `steer` frame with the same message id produces one event; `stop`, `pause` and `steer` for a room with no open delivery produce no event and no request to the server; none of these events causes a `/working`, `/done` or `/failed` request of its own.

- [ ] **Step 2: Write the failing gateway test:**

```python
def test_stop_ends_the_running_turn_and_the_room_moves_on(gateway_factory):
    g = gateway_factory([Text("word " * 200, chunk_chars=10, delay_per_chunk=0.25), Text("next answer.")])
    g.start(); g.wait_started()
    first = g.post("write a long answer")
    g.wait(lambda: any(m["streaming"] for m in g.messages()))
    r = g.chat.human.post(f"/api/rooms/{g.room_id}/agents/{g.chat.agent_id}/stop")
    assert r.status_code < 300, r.text
    g.wait(lambda: g.state(first) == "done", timeout=20)
    reply = next(m for m in g.messages() if m["text"].startswith("word"))
    assert reply["streaming"] is False and len(reply["text"]) < len("word " * 200)
    second = g.post("another question")
    g.wait(lambda: g.state(second) == "done")
    assert any(m["text"] == "next answer." for m in g.messages())
```

Add a second test that pauses the room (`POST /api/rooms/{room}/pause`) mid-reply and asserts the first delivery reaches `done` within 20 s and the reply stops growing.

- [ ] **Step 3: Run.** `just plugin-check -k "control or stop_ends or pause" -v`. Expected: FAIL (events are only logged).

- [ ] **Step 4: Implement** the routing and the two methods.

- [ ] **Step 5: Run.** `just plugin-check`, then the gateway tier against `v0.21.7`. Expected: all pass. Read what Hermes posts in the room for `/stop` and `/steer` in the gateway run and record it in Task 9's README text.

- [ ] **Step 6: Commit.** `git commit -am "feat(plugin): stop, pause and steer reach the running turn"`

---

### Task 5: Approval and clarify prompts

**Files:**
- Create: `plugin/hermes_chat/prompts.py`, `plugin/tests/test_prompts.py`, `plugin/tests/test_adapter_prompts.py`
- Modify: `plugin/hermes_chat/client.py`, `plugin/hermes_chat/adapter.py`, `plugin/tests/test_client.py`, `plugin/tests/test_gateway.py`

**Interfaces:**
- Produces in `client.py`:
  - `async def post_prompt(self, room_id: str, kind: str, text: str, choices: list[str]) -> str` — `POST /agent/rooms/{room}/prompts`, returns the message id.
  - `async def expire_prompt(self, message_id: str) -> None` — `POST /agent/prompts/{id}/expire`.
- Produces in `prompts.py`:
  - `@dataclass(frozen=True) class OpenPrompt: message_id: str; room_id: str; kind: str; session_key: str; ref: str` — `kind` is `"approval"`, `"clarify"` or `"confirm"`; `ref` is the clarify id or confirm id, `""` for an approval.
  - `class PromptTable`: `add(p: OpenPrompt) -> None`; `take(message_id: str) -> OpenPrompt | None`; `take_ref(ref: str) -> OpenPrompt | None`; `take_room(room_id: str, kinds: tuple[str, ...]) -> list[OpenPrompt]`.
  - `def clarify_choices(choices: list | None) -> list[str] | None` — `[]` for none; the choices as strings when there are 1 to 6, each 1 to 200 characters, none repeated; `None` when they do not fit.
  - `def numbered(question: str, choices: list) -> str` — the question, a blank line, then `1. …` lines.
- Produces in `adapter.py`:
  - `async def _send_exec_approval_prompt(self, prompt) -> SendResult` — posts kind `approval` with `prompt.text` and `prompt.choices`, records `OpenPrompt(kind="approval", session_key=prompt.session_key, ref="")`.
  - `async def send_clarify(self, chat_id, question, choices, clarify_id, session_key, metadata=None) -> SendResult` — posts kind `clarify` with `clarify_choices(choices)`; when that is `None`, posts `numbered(question, choices)` with no choices. Records `ref=clarify_id`. Does not call `mark_awaiting_text`: an answer arrives only as `prompt.answered`.
  - `async def retire_clarify_card(self, clarify_id: str, notice: str) -> None` — expires the prompt recorded for that id.
  - `async def _on_prompt_answered(self, data: dict) -> None` — `take(data["message_id"])`; unknown: info log, return. `approval`: `tools.approval.resolve_gateway_approval(p.session_key, data["answer"])`. `clarify`: `tools.clarify_gateway.resolve_gateway_clarify(p.ref, data["answer"])`.
  - `_close_turn` expires, in a background task, every `approval` and `clarify` prompt still recorded for the turn's room.
  - Both send overrides return `SendResult(success=False, error=…)` on `ApiError` or `Unreachable`, with the stage 1 logging rule for 409.

- [ ] **Step 1: Write the failing `prompts` and client tests.** `clarify_choices(None) == []`; `clarify_choices(["a", "b"]) == ["a", "b"]`; seven choices, a 201-character choice, an empty choice, and `["a", "a"]` each give `None`; `numbered("Which?", ["a", "b"]) == "Which?\n\n1. a\n2. b"`; `PromptTable.take` returns a prompt once; `take_room("R1", ("approval", "clarify"))` leaves a `confirm` prompt in place. Client: `post_prompt("R1", "approval", "Run it?", ["once", "deny"])` posts `{"kind": "approval", "text": "Run it?", "choices": ["once", "deny"]}` and returns the id; `expire_prompt("M5")` posts to `/agent/prompts/M5/expire`.

- [ ] **Step 2: Write the failing adapter tests** in `test_adapter_prompts.py`, patching `tools.approval.resolve_gateway_approval` and `tools.clarify_gateway.resolve_gateway_clarify` with recorders: an approval prompt built with actions for `once`, `session`, `always`, `deny` posts those four choices and returns the new message id; a `prompt.answered` frame `{"room_id": "R1", "message_id": "P1", "answer": "session"}` calls the approval resolver with that prompt's session key and `"session"`; a clarify with three choices posts them and its answer calls the clarify resolver with the clarify id and the answer text; a clarify with seven choices posts no choices and a text containing `7. `; an answer for an unknown `message_id` calls neither resolver and raises nothing; the same answer delivered twice resolves once; a turn that closes with an approval open posts `/agent/prompts/P1/expire`; `retire_clarify_card("c1", "…")` expires that prompt; a 409 on the prompt post gives `success is False`; the adapter's log lines contain neither the prompt text nor the answer.

- [ ] **Step 3: Write the failing gateway test.** The scripted model calls `terminal` with a command Hermes holds for approval (read `tools/approval.py` in the checkout for one its default rules flag, and use it with a harmless target under the test's `HOME`):

```python
def test_an_approval_is_asked_as_a_prompt_and_its_answer_continues_the_turn(gateway_factory):
    g = gateway_factory([ToolCall("terminal", {"command": FLAGGED_COMMAND}), Text("finished.")])
    g.start(); g.wait_started()
    first = g.post("do it")
    g.wait(lambda: any(m["prompt"] for m in g.messages()))
    prompt = next(m for m in g.messages() if m["prompt"])
    assert prompt["prompt"]["kind"] == "approval" and "deny" in prompt["prompt"]["choices"]
    assert g.state(first) == "working"
    r = g.chat.human.post(f"/api/rooms/{g.room_id}/messages/{prompt['id']}/answer", json={"answer": "deny"})
    assert r.status_code < 300, r.text
    g.wait(lambda: g.state(first) == "done")
    assert any(m["text"] == "finished." for m in g.messages())
```

Add a clarify test the same way with `ToolCall("clarify", {"question": "Which one?", "choices": ["red", "blue"]})`, answering `blue` and asserting the next model request's last tool message contains `blue` (`g.llm.main_requests()[1]["messages"][-1]`). Read the answer route's body in `docs/openapi.yaml` before writing the calls.

- [ ] **Step 4: Run.** `just plugin-check -k "prompt or approval or clarify" -v`. Expected: FAIL.

- [ ] **Step 5: Implement.**

- [ ] **Step 6: Run.** `just plugin-check`, then the gateway tier against `v0.21.7`. Expected: all pass.

- [ ] **Step 7: Commit.** `git add -A plugin && git commit -m "feat(plugin): approval and clarify questions asked as prompts"`

---

### Task 6: A command's confirmation asked as a prompt

**Files:**
- Modify: `plugin/hermes_chat/adapter.py`, `plugin/tests/test_adapter_prompts.py`, `plugin/tests/test_gateway.py`

**Interfaces:**
- Consumes: `PromptTable`, `OpenPrompt`, `post_prompt`, `_on_prompt_answered` from Task 5; Hermes's `tools.slash_confirm.resolve(session_key, confirm_id, choice)` (a coroutine; `choice` is `"once"`, `"always"` or `"cancel"`; it returns text to show, or nothing).
- Produces in `adapter.py`:
  - `async def send_slash_confirm(self, chat_id, title, message, session_key, confirm_id, metadata=None) -> SendResult` — first expires any `confirm` prompt still recorded for the room, then posts kind `approval` with text `f"**{title}**\n\n{message}"` and choices `["once", "always", "deny"]`, recording `OpenPrompt(kind="confirm", ref=confirm_id)`.
  - `_on_prompt_answered` gains the `confirm` branch: the answer maps `once -> "once"`, `always -> "always"`, anything else -> `"cancel"`; it awaits `slash_confirm.resolve(...)` and sends the returned text to the room with `self.send` when there is any.
- A `confirm` prompt is not expired by `_close_turn`: the command's own turn ends as soon as the question is posted.

- [ ] **Step 1: Write the failing adapter tests** with `tools.slash_confirm.resolve` patched by a recorder returning `"✨ Session reset!"`: `send_slash_confirm("R1", "Confirm /new", "This starts a fresh session.", "sk", "cid")` posts kind `approval`, choices `["once", "always", "deny"]`, text starting `**Confirm /new**`; answering `once` calls the resolver with `("sk", "cid", "once")` and posts `✨ Session reset!` to `R1`; answering `deny` calls it with `"cancel"`; a second `send_slash_confirm` for the room expires the first prompt; closing a turn in the room does not expire a `confirm` prompt.

- [ ] **Step 2: Write the failing gateway test:** the human posts `/new`; a prompt message appears whose text contains `Confirm /new` and no message contains `Text fallback`; the human answers `once`; a message containing `Session reset` appears; the next question's model request carries no earlier history (`len(g.llm.main_requests()[-1]["messages"])` equals that of the first request of the run).

- [ ] **Step 3: Run.** Expected: FAIL (Hermes posts the text fallback).

- [ ] **Step 4: Implement.**

- [ ] **Step 5: Run.** `just plugin-check`, then the gateway tier against `v0.21.7`. Expected: all pass.

- [ ] **Step 6: Commit.** `git commit -am "feat(plugin): a command's confirmation asked as a prompt"`

---

### Task 7: The suggest-a-note-entry tool

**Files:**
- Create: `plugin/hermes_chat/note_tool.py`, `plugin/tests/test_note_tool.py`
- Modify: `plugin/hermes_chat/client.py`, `plugin/hermes_chat/adapter.py`, `plugin/tests/test_client.py`, `plugin/tests/test_gateway.py`

**Interfaces:**
- Consumes: `hooks.attach`'s set of live adapters (Task 3) through a new `hooks.adapters() -> list`; `adapter._loop` (Task 3).
- Produces in `client.py`: `async def suggest_note_entry(self, delivery_id: str, text: str) -> dict` — `POST /agent/deliveries/{id}/note-entries`; returns the entry for 200 and for 201.
- Produces in `adapter.py`: `async def suggest_note_entry(self, room_id: str, text: str) -> str` — returns the sentence the tool gives back to the model: `"Suggested. The person will see it in their note and decide whether to keep it."` on success; `"There is no open turn in this room to suggest an entry from."` when the room has no open delivery; `"The suggestion was not accepted: <server message>"` on `ApiError`; `"The chat server could not be reached."` on `Unreachable`.
- Produces in `note_tool.py`: `def register_note_tool(ctx) -> None`, called from `adapter.register`. It registers with `ctx.register_tool(name="suggest_note_entry", toolset="hermes_chat", schema=SCHEMA, handler=handler, check_fn=in_hermes_chat, description=DESCRIPTION)`:
  - `SCHEMA`: one required string property `text`, `maxLength` 300.
  - `DESCRIPTION`: `"Suggest one short entry (at most 300 characters) for the note of the person you are answering. They approve it before it is kept. Use it for a lasting preference or fact about them, not for the task at hand."`
  - `in_hermes_chat() -> bool`: the session platform is `hermes_chat`.
  - `handler`: a plain function with the calling convention Hermes uses for a plugin tool (copy it from `plugins/google_meet/__init__.py` in the checkout). It reads the room from the session context, trims the text, refuses blank or over-300-character text with a sentence saying so, runs `adapter.suggest_note_entry(room, text)` on the adapter's loop with `asyncio.run_coroutine_threadsafe(...).result(timeout=20)` for the adapter that has an open delivery in that room, and returns the sentence. Any exception is logged at debug and answered with `"The suggestion could not be sent."`.

- [ ] **Step 1: Write the failing tests.** Client: the route, the body `{"text": "Prefers short answers"}`, a dict back for both 200 and 201. Adapter (`test_adapter_turn.py`): the four return sentences, each from the matching stub response, and the request going to the open delivery's id. `test_note_tool.py`: the registered name, toolset and schema; `in_hermes_chat` false for platform `telegram`; blank text and 301 characters return a refusal without touching the adapter; the handler called from a worker thread returns the adapter's sentence; a raising adapter gives `"The suggestion could not be sent."`; no log line contains the entry text.

- [ ] **Step 2: Write the failing gateway test:**

```python
def test_the_model_is_offered_the_tool_and_a_suggestion_reaches_the_note(gateway_factory):
    g = gateway_factory([ToolCall("suggest_note_entry", {"text": "Prefers short answers"}), Text("noted.")])
    g.start(); g.wait_started()
    first = g.post("remember I like short answers")
    g.wait(lambda: g.state(first) == "done")
    offered = [t["function"]["name"] for t in g.llm.main_requests()[0]["tools"]]
    assert "suggest_note_entry" in offered
    assert "Suggested." in g.llm.main_requests()[1]["messages"][-1]["content"]
    note = g.chat.human.get("/api/note").json()
    assert any(e["text"] == "Prefers short answers" for e in note["pending"])
```

Read the `Note` schema in `docs/openapi.yaml` for where a quarantined suggestion appears and fix the last assertion's field names to it.

- [ ] **Step 3: Run.** Expected: FAIL. If `offered` lacks the tool once the code exists, the `hermes_chat` toolset is not on for the platform by default: find the setting that turns a plugin toolset on for a platform in `toolsets.py` and `hermes_cli/tools_config.py`, add it to the test home in `gateway.py`, and add the same lines to the README's `config.yaml` section in Task 9.

- [ ] **Step 4: Implement.**

- [ ] **Step 5: Run.** `just plugin-check`, then the gateway tier against `v0.21.7`. Expected: all pass.

- [ ] **Step 6: Commit.** `git add -A plugin && git commit -m "feat(plugin): a tool that suggests an entry for the person's note"`

---

### Task 8: Sending files

**Files:**
- Modify: `plugin/hermes_chat/client.py`, `plugin/hermes_chat/adapter.py`, `plugin/tests/test_client.py`, `plugin/tests/stubserver.py` (record a raw body's length when the request is not JSON)
- Create: `plugin/tests/test_adapter_files.py`

**Interfaces:**
- Produces in `client.py`:
  - `async def upload_attachment(self, room_id: str, name: str, data: bytes) -> str` — `POST /agent/rooms/{room}/attachments?name=…` with `Content-Type: application/octet-stream`, returns the attachment id.
  - `post_message` gains `attachment_ids: list[str] | None = None`, sent as `attachment_ids` when given.
- Produces in `adapter.py`:
  - `MAX_ATTACHMENT_BYTES = 26_214_400` (25 MiB).
  - `async def _send_file(self, chat_id: str, path: str, caption: str | None, reply_to: str | None, file_name: str | None = None) -> SendResult` — resolves the path with `self.validate_media_delivery_path(path)`; a refused path, a missing or unreadable file, an empty file, or one over the limit returns `SendResult(success=False, error=…)` having sent nothing. Otherwise it uploads under `file_name` or the path's base name (control characters removed, cut to 255 characters), then posts a message with `text=_outgoing(caption or "")`, the `reply_to`, and the one attachment id, and returns that message's id. The file is read with `asyncio.to_thread`.
  - `send_document`, `send_image_file`, `send_voice` and `send_video` override the base methods with their exact signatures and call `_send_file`.
  - `send_image` (a URL) keeps the base behaviour: the URL is posted as text.

- [ ] **Step 1: Write the failing tests.** Client: the upload's path, `name` query parameter, content type and body length; the id returned; `post_message("R1", "", attachment_ids=["A1"])` sends `{"text": "", "attachment_ids": ["A1"]}`. Adapter, with `validate_media_delivery_path` patched to return the path it is given: `send_document("R1", str(path), caption="the report")` uploads then posts `{"text": "the report", "attachment_ids": ["A1"]}` and returns the message id; with no caption the text is `""`; `file_name="report.pdf"` is the upload's `name`; `send_image_file`, `send_voice` and `send_video` each upload and post; a path the validator refuses (patched to return None) gives `success is False` and no request; a missing file, an empty file, and a file one byte over `MAX_ATTACHMENT_BYTES` (patch the constant to 10 for the test) each give `success is False` and no request; a 409 on the upload gives `success is False` and no message post; the log has the room and attachment id and not the file's name.

- [ ] **Step 2: Run.** `just plugin-check -k "file or upload or attachment" -v`. Expected: FAIL.

- [ ] **Step 3: Implement.**

- [ ] **Step 4: Run.** `just plugin-check`. Expected: all pass. Extend `plugin/tests/test_e2e.py` with one case against the real server: the adapter's fake handler calls `send_document` for a small temporary file, and the human's `GET /api/rooms/{room}/messages` shows a bot message with one attachment whose download returns the file's bytes.

- [ ] **Step 5: Commit.** `git add -A plugin && git commit -m "feat(plugin): files Hermes sends are uploaded and attached"`

---

### Task 9: Documents

**Files:**
- Modify: `plugin/README.md`, `AGENTS.md`, `README.md` (only if it names the probe or the stage), `docs/superpowers/specs/2026-10-13-hermes-plugin-design.md` (status line and section 10)

- [ ] **Step 1: `plugin/README.md`.**
  - "What works and what does not yet": move tool steps, stop/pause/steer, prompts (approval, clarify, command confirmation), the note tool and file sends to "Works"; keep "commentary before a tool is posted as its own message" and "Hermes's shutdown notice is posted as a message" as known behaviour; remove the stage 1 statement that questions cannot be answered.
  - `config.yaml`: replace the short timeouts with the long ones the product spec requires, `agent.clarify_timeout: 0` and `approvals.timeout` set to the largest value Hermes accepts (read the clamp in the checkout and write the number), with one sentence saying why: a prompt waits for a person. Add the toolset lines if Task 7 found they are needed.
  - Replace the first-sitting checklist with a second-sitting one, each item with *Do*, *Expect*, *Send back*: a question that uses a tool (the step shows under the message with its duration); Stop on a running turn; Steer on a running turn; a command that needs approval, answered each way once; a clarifying question; `/new` confirmed from the prompt; "suggest a note entry" and approving it on the note screen; asking the agent to send a file; a scheduled job created outside any room, delivered to `HERMES_CHAT_HOME_ROOM`; stopping the gateway mid-reply.
  - "Where the logs are": name `hermes_chat.steps` and `hermes_chat.hooks`; remove `hermes_chat.probe`.
  - "Development": the gateway tier, `-m gateway`, and running it against the release with `HERMES_SRC`.

- [ ] **Step 2: `AGENTS.md`.** In the plugin lines: the client modules are `sse`, `client`, `stream`, `reports`, `record`, `checks`, `steps`, `prompts`; the Hermes-importing modules are `adapter.py`, `hooks.py`, `note_tool.py`; the stage 1 probe sentence is replaced by "hook callbacks are plain functions and never raise into Hermes; log lines carry identifiers, never tool arguments, message or prompt text"; the plugin test tiers gain the gateway tier.

- [ ] **Step 3: The spec.** Status line: stage 2 built. Section 10: mark each mechanism as built, with the one-line correction where the built mechanism differs from the line (stop is `/stop` dispatched as a command). Section 9: note the probe is gone.

- [ ] **Step 4: Check.** `just check` (no Hermes needed) and `just plugin-check`. Expected: both pass.

- [ ] **Step 5: Commit.** `git commit -am "docs(plugin): stage 2 setup, the second-sitting checklist, and the new modules"`
