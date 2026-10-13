# Hermes Plugin Stage 1 (Chat) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A Hermes platform plugin that lets one real Hermes agent chat in a Hermes Chat room: receive a message, stream a reply, and always close the turn.

**Architecture:** A Hermes-free client layer (frame parser, HTTP calls, stream reader, retried turn reports, open-turn record) under a `BasePlatformAdapter` subclass that maps stream events to Hermes `MessageEvent`s and Hermes's calls to client calls. A logging-only probe on Hermes's tool hooks rides along for the first sitting.

**Tech Stack:** Python 3.14, `httpx` 0.28.1 (ships with Hermes), `pytest` 9.1.1 and `pytest-asyncio` 1.3.0 (Hermes's `dev` group), `uv` 0.11.31, the Hermes checkout at `~/Dev/hermes-agent` (commit `6e7b83a2`), and the `hermes-chat` Go binary built from this repository for the end-to-end test.

**Spec:** `docs/superpowers/specs/2026-10-13-hermes-plugin-design.md` (sections 4 to 9 and 11 to 13). The server's contract is `docs/openapi.yaml` and the "Agent stream", "Delivery and turns", and "The off switch" sections of `README.md`.

## Global Constraints

- Hermes is pinned at commit `6e7b83a2`. Every Hermes file and line this plan cites is from that commit, at `~/Dev/hermes-agent`. Do not edit anything in that checkout.
- The plugin adds no dependency. The only HTTP library is `httpx`.
- No change to the Go server, its routes, or `docs/openapi.yaml`.
- Platform name `hermes_chat`, label `Hermes Chat`. Env vars: `HERMES_CHAT_URL`, `HERMES_CHAT_TOKEN`, `HERMES_CHAT_HOME_ROOM`, `HERMES_CHAT_ALLOW_ALL_USERS`, `HERMES_CHAT_ALLOWED_USERS`.
- Settings are read with `gateway.platforms._shared.get_scoped_secret` / `extra_or_secret`, never `os.getenv` or `os.environ`.
- `sse.py`, `client.py`, `stream.py`, `reports.py`, `record.py`, and `checks.py` import nothing from Hermes (`gateway`, `hermes_cli`, `tools`, `agent`, `hermes_constants`). `__init__.py` imports `adapter` only inside `register`.
- The token is never logged. The probe logs names and identifiers, never tool arguments, message text, or hook argument values.
- Turn reports (`working`, `done`, `failed`) are retried until the server answers; a 404 or 409 answer ends the retrying. Content calls (post, edit) are never retried by the plugin: a failure is returned to Hermes as a failed `SendResult` (`retryable=True` for a network failure, which Hermes's base adapter retries itself).
- Never run `gofmt -w` on a directory in this repository.
- Commit messages follow the repository's style (`feat(plugin): …`, `docs: …`, `test(plugin): …`).

## Review Focus

Each line is pinned by a test in the task named.

1. **The server is down when the gateway starts.** `connect()` still succeeds and the stream keeps retrying; it does not disable the platform. (Task 6)
2. **A delivery whose author is `null`** (a human removed after posting). It becomes a normal event with a placeholder name, not a crash that leaves the turn open. (Task 6)
3. **A reply posted into a room that was paused mid-turn** (409 `paused`). `send` returns a failed result, Hermes's turn ends, and `done` or `failed` is still reported. (Task 7)
4. **The open-turn file is corrupt or half-written.** The plugin starts with an empty record and a warning; it does not refuse to connect. (Task 5)
5. **A stream frame split across two network chunks, including inside a multi-byte character.** It is parsed as one frame with the right text. (Task 1)

## File Structure

```
plugin/
  README.md                 setup steps and the first-sitting checklist (Task 11)
  pytest.ini                asyncio_mode = auto, pythonpath = .
  hermes_chat/
    plugin.yaml             kind: platform, env var descriptions
    __init__.py             lazy register(ctx)
    sse.py                  FrameParser: bytes -> Frame
    client.py               ChatClient: the agent API over httpx
    stream.py               EventStream: cursor, reconnect, silence watchdog
    reports.py              TurnReporter: ordered, retried turn reports
    record.py               OpenTurns: the open-turn file
    checks.py               display_warnings: config.yaml checks
    adapter.py              HermesChatAdapter and register()
    probe.py                the stage 1 hook probe
  tests/
    stubserver.py           a local HTTP server scripted by the tests
    conftest.py             fixtures; skips adapter tests without Hermes
    test_sse.py test_client.py test_stream.py test_reports.py
    test_record.py test_checks.py
    test_adapter_inbound.py test_adapter_turn.py test_adapter_restart.py
    test_probe.py
    test_e2e.py             against the real hermes-chat binary
```

Tests that need Hermes begin with `pytest.importorskip("gateway.platforms.base", reason="needs the Hermes checkout; set HERMES_SRC")`.

---

### Task 1: Scaffolding and the frame parser

**Files:**
- Create: `plugin/pytest.ini`, `plugin/hermes_chat/__init__.py`, `plugin/hermes_chat/plugin.yaml`, `plugin/hermes_chat/sse.py`
- Create: `plugin/tests/test_sse.py`
- Modify: `justfile` (add `plugin-check`)

**Interfaces:**
- Produces: `sse.Frame` (frozen dataclass: `event: str`, `data: str`, `id: str | None`), `sse.FrameParser` with `feed(chunk: bytes) -> list[Frame]`.
- Produces: `just plugin-check`, used by every later task.

- [ ] **Step 1: Add the `plugin-check` recipe to `justfile`**

```
plugin-check *args:
    #!/usr/bin/env bash
    set -euo pipefail
    src="${HERMES_SRC:-$HOME/Dev/hermes-agent}"
    cd plugin
    if [ -d "$src" ]; then
        uv run --project "$src" --group dev python -m pytest {{args}}
    else
        echo "plugin-check: no Hermes checkout at $src; adapter tests will skip" >&2
        uv run --no-project --with pytest==9.1.1 --with pytest-asyncio==1.3.0 --with httpx==0.28.1 python -m pytest {{args}}
    fi
```

With no arguments pytest uses `testpaths` from `plugin/pytest.ini`; `just plugin-check tests/test_sse.py -v` runs one file. It is not added to `check`. The first run creates `.venv` inside the Hermes checkout; that is expected and is the only thing written there.

- [ ] **Step 2: Write `plugin/pytest.ini`** with `asyncio_mode = auto`, `pythonpath = .`, `testpaths = tests`.

- [ ] **Step 3: Write `plugin/hermes_chat/__init__.py`**: a `register(ctx)` that imports `register` from `.adapter` inside the function body and calls it. Nothing else at module level.

- [ ] **Step 4: Write `plugin/hermes_chat/plugin.yaml`**: `name: hermes-chat`, `label: Hermes Chat`, `kind: platform`, `version: 0.1.0`. `requires_env`: `HERMES_CHAT_URL` (`password: false`), `HERMES_CHAT_TOKEN` (`password: true`). `optional_env`: `HERMES_CHAT_HOME_ROOM`, `HERMES_CHAT_ALLOW_ALL_USERS`, `HERMES_CHAT_ALLOWED_USERS`. Descriptions from the spec's section 4 tables. Model: `~/Dev/hermes-agent/plugins/platforms/irc/plugin.yaml`.

- [ ] **Step 5: Write the failing tests in `plugin/tests/test_sse.py`**

```python
from hermes_chat.sse import Frame, FrameParser

def test_one_frame():
    p = FrameParser()
    assert p.feed(b'id: 7\nevent: delivery\ndata: {"a":1}\n\n') == [Frame("delivery", '{"a":1}', "7")]

def test_comment_and_heartbeat_yield_nothing():
    assert FrameParser().feed(b": hb\n\n") == []

def test_multi_line_data_joined_with_newline():
    assert FrameParser().feed(b"event: x\ndata: a\ndata: b\n\n") == [Frame("x", "a\nb", None)]

def test_frame_split_across_chunks_inside_a_character():
    raw = 'event: delivery\ndata: {"t":"café"}\n\n'.encode()
    cut = raw.index("é".encode()) + 1          # between the two bytes of é
    p = FrameParser()
    assert p.feed(raw[:cut]) == []
    assert p.feed(raw[cut:]) == [Frame("delivery", '{"t":"café"}', None)]

def test_crlf_line_endings():
    assert FrameParser().feed(b"event: ready\r\ndata: {}\r\n\r\n") == [Frame("ready", "{}", None)]

def test_frame_without_event_field_is_message():
    assert FrameParser().feed(b"data: x\n\n") == [Frame("message", "x", None)]

def test_two_frames_in_one_chunk():
    assert len(FrameParser().feed(b"event: a\ndata: 1\n\nevent: b\ndata: 2\n\n")) == 2
```

- [ ] **Step 6: Run** `just plugin-check tests/test_sse.py -v`. Expected: FAIL, `ModuleNotFoundError: hermes_chat.sse`.

- [ ] **Step 7: Implement `sse.py`.** Buffer bytes, split on line ends, decode each complete line as UTF-8 (so a split character is never decoded early). A leading single space after the colon is stripped, as the SSE format says. The Go encoder this must match is `internal/sse`.

- [ ] **Step 8: Run** `just plugin-check tests/test_sse.py -v`. Expected: 7 passed.

- [ ] **Step 9: Commit** `feat(plugin): scaffolding and the event stream frame parser`.

---

### Task 2: The HTTP client

**Files:**
- Create: `plugin/hermes_chat/client.py`, `plugin/tests/stubserver.py`, `plugin/tests/conftest.py`, `plugin/tests/test_client.py`

**Interfaces:**
- Produces, in `client.py`:
  - `class ApiError(Exception)` with `status: int`, `code: str`, `message: str` (from the server's `{"error":{"code","message"}}`; `code` is `""` when the body is not that shape).
  - `class Unauthorized(ApiError)` for 401.
  - `class Unreachable(Exception)`: a network error, a timeout, or a 5xx.
  - `class ChatClient` with `__init__(self, base_url: str, token: str, *, timeout: float = 30.0)`; `async me() -> dict`; `async post_message(room_id: str, text: str, reply_to_id: str | None = None) -> str` (returns the new message's `id`); `async edit_message(message_id: str, text: str, final: bool) -> None`; `async report(delivery_id: str, state: str) -> None` with `state` one of `"working"`, `"done"`, `"failed"`; `open_events(cursor: int | None)` as an async context manager yielding an async iterator of `bytes`; `async aclose() -> None`.
- Produces, in `tests/stubserver.py`: `class StubServer` started on `127.0.0.1:0` in a thread or on the test loop, with `url: str`, `requests: list[Recorded]` (`method`, `path`, `headers`, `json`), `script(method, path, *responses)` to queue responses for a route (each a status, JSON body, or a callable; a `Hang` sentinel never answers; a `Drop` sentinel closes the socket), and `events(*chunks)` to script what `GET /agent/events` writes (`bytes` chunks, `Hang`, `Drop`). Fixture `stub` in `conftest.py`.

- [ ] **Step 1: Write `stubserver.py` and the `stub` fixture.** Standard library only (`asyncio.start_server` is enough). Unscripted routes answer 404 with an `Error` body so a wrong path fails loudly.

- [ ] **Step 2: Write the failing tests in `test_client.py`**

```python
async def test_me_sends_bearer_token(stub):
    stub.script("GET", "/agent/me", (200, {"id": "A1", "handle": "h", "display_name": "H"}))
    c = ChatClient(stub.url, "hca_secret")
    assert (await c.me())["id"] == "A1"
    assert stub.requests[0].headers["authorization"] == "Bearer hca_secret"

async def test_post_message_returns_id_and_sends_reply_to(stub):
    stub.script("POST", "/agent/rooms/R1/messages", (201, {"id": "M9"}))
    c = ChatClient(stub.url, "t")
    assert await c.post_message("R1", "hi", reply_to_id="M1") == "M9"
    assert stub.requests[0].json == {"text": "hi", "reply_to_id": "M1"}

async def test_post_message_omits_reply_to_when_none(stub): ...   # json == {"text": "hi"}

async def test_edit_message(stub): ...   # PUT /agent/messages/M9 json {"text": "x", "final": True}, 204 -> None

@pytest.mark.parametrize("state", ["working", "done", "failed"])
async def test_report_posts_to_the_state_route(stub, state): ...   # POST /agent/deliveries/D1/{state}

async def test_409_paused_is_api_error(stub):
    stub.script("POST", "/agent/rooms/R1/messages", (409, {"error": {"code": "paused", "message": "room is paused"}}))
    with pytest.raises(ApiError) as e:
        await ChatClient(stub.url, "t").post_message("R1", "hi")
    assert (e.value.status, e.value.code) == (409, "paused")

async def test_401_is_unauthorized(stub): ...          # pytest.raises(Unauthorized)
async def test_500_is_unreachable(stub): ...           # pytest.raises(Unreachable)
async def test_refused_connection_is_unreachable(): ...  # ChatClient("http://127.0.0.1:1", "t").me()
async def test_token_not_in_exception_text(stub): ...  # "hca_secret" not in str(e.value) for each error kind

async def test_open_events_sends_cursor_and_yields_chunks(stub):
    stub.events(b"event: ready\ndata: {\"cursor\":5}\n\n")
    async with ChatClient(stub.url, "t").open_events(5) as chunks:
        first = await anext(chunks)
    assert first.startswith(b"event: ready")
    assert stub.requests[0].headers["last-event-id"] == "5"

async def test_open_events_without_cursor_sends_no_header(stub): ...
async def test_open_events_401_raises_unauthorized_on_enter(stub): ...
```

- [ ] **Step 3: Run** `just plugin-check tests/test_client.py -v`. Expected: FAIL on import.

- [ ] **Step 4: Implement `client.py`** on one `httpx.AsyncClient`. `open_events` uses `client.stream("GET", "/agent/events", ...)` with `Accept: text/event-stream` and no read timeout (the stream reader in Task 3 owns the silence timeout); a non-200 status on enter is raised through the same mapping as the other calls. Trailing slashes on `base_url` are tolerated.

- [ ] **Step 5: Run** the file. Expected: all pass.

- [ ] **Step 6: Commit** `feat(plugin): the agent API client`.

---

### Task 3: The stream reader

**Files:**
- Create: `plugin/hermes_chat/stream.py`, `plugin/tests/test_stream.py`

**Interfaces:**
- Consumes: `ChatClient.open_events`, `Unauthorized`, `Unreachable`, `ApiError`, `FrameParser`.
- Produces: `class EventStream` with `__init__(self, client, on_event, *, on_revoked, silence_timeout: float = 60.0, backoff_first: float = 1.0, backoff_max: float = 30.0, sleep=asyncio.sleep)`, where `on_event` is `async (name: str, data: dict) -> None` and `on_revoked` is `() -> None`; `cursor: int | None`; `connected: bool`; `async run() -> None` (returns only after `on_revoked`, otherwise runs until cancelled).

Rules the tests pin: `ready` sets `cursor` from its data and resets the backoff; every frame with a numeric `id` advances `cursor` to it; `ready` is not passed to `on_event`; a frame whose data is not JSON is logged and skipped; an exception raised by `on_event` is logged and the stream continues (the cursor still advances); the delay doubles from `backoff_first` to `backoff_max` across consecutive failures; `Unauthorized` calls `on_revoked` once and ends `run`.

- [ ] **Step 1: Write the failing tests in `test_stream.py`** (each uses `stub.events(...)`, a recording `on_event`, and a fake `sleep` that records its argument and returns at once):

```python
async def test_delivers_events_in_order_and_tracks_cursor(stub):
    # ready cursor 3, then id:4 delivery, id:5 stop -> on_event got [("delivery", {...}), ("stop", {...})], cursor == 5
async def test_ready_is_not_passed_on(stub): ...
async def test_reconnects_with_last_cursor_after_drop(stub):
    # events(ready 3, id:4 frame, Drop) then events(ready 4, Hang) -> second request header last-event-id == "4"
async def test_backoff_doubles_and_caps(stub):
    # server refuses 6 times -> sleeps == [1.0, 2.0, 4.0, 8.0, 16.0, 30.0]
async def test_backoff_resets_after_ready(stub): ...
async def test_silence_is_treated_as_dead(stub):
    # events(ready, Hang) with silence_timeout=0.05 -> a second GET /agent/events arrives
async def test_heartbeat_comment_counts_as_life(stub):
    # ": hb" every 0.02s for 0.2s with silence_timeout=0.05 -> exactly one GET
async def test_unauthorized_calls_on_revoked_and_stops(stub): ...
async def test_handler_exception_does_not_stop_the_stream(stub): ...
async def test_non_json_data_is_skipped(stub): ...
async def test_connected_flag(stub):   # False before ready, True after, False after a drop
```

- [ ] **Step 2: Run.** Expected: FAIL on import.
- [ ] **Step 3: Implement `stream.py`.** The silence timeout wraps each `anext(chunks)` in `asyncio.wait_for`.
- [ ] **Step 4: Run.** Expected: all pass.
- [ ] **Step 5: Commit** `feat(plugin): the event stream reader`.

---

### Task 4: Retried turn reports

**Files:**
- Create: `plugin/hermes_chat/reports.py`, `plugin/tests/test_reports.py`

**Interfaces:**
- Consumes: `ChatClient.report`, `ApiError`, `Unreachable`.
- Produces: `class TurnReporter` with `__init__(self, client, *, backoff_first: float = 0.5, backoff_max: float = 30.0, sleep=asyncio.sleep)`; `submit(delivery_id: str, state: str) -> None` (returns at once); `async wait_idle() -> None` (returns when nothing is queued or in flight; for tests and for `disconnect`'s bounded flush); `async aclose() -> None` (stops retrying, drops what is queued).

Rules: reports for one delivery go out in the order submitted, each only after the one before it was answered; different deliveries do not wait on each other; `Unreachable` is retried forever with doubling delay; `ApiError` with status 404 or 409 is logged at info and dropped; any other `ApiError` is logged at warning and dropped (retrying a 400 cannot help); `Unauthorized` is dropped (the stream reader reports the revoked token).

- [ ] **Step 1: Write the failing tests**

```python
async def test_reports_for_one_delivery_go_in_order(stub):
    # submit D1 working, D1 done -> requests paths == [".../D1/working", ".../D1/done"]
async def test_retries_while_unreachable_then_succeeds(stub):
    # script D1/done: 500, 500, 204 -> three requests, sleeps == [0.5, 1.0]
async def test_second_report_waits_for_the_first_to_be_answered(stub):
    # D1/working: 500 then 204; D1/done: 204 -> order working, working, done
async def test_404_and_409_end_the_retrying(stub):   # one request each, wait_idle returns
async def test_other_deliveries_are_not_blocked(stub):
    # D1/working hangs; D2/done 204 -> D2/done is requested while D1 is still pending
async def test_aclose_stops_retrying(stub): ...
```

- [ ] **Step 2: Run.** Expected: FAIL on import.
- [ ] **Step 3: Implement `reports.py`** as one worker task per delivery ID, created on first `submit` and removed when its queue empties.
- [ ] **Step 4: Run.** Expected: all pass.
- [ ] **Step 5: Commit** `feat(plugin): turn reports retried until answered`.

---

### Task 5: The open-turn record and the config check

**Files:**
- Create: `plugin/hermes_chat/record.py`, `plugin/hermes_chat/checks.py`, `plugin/tests/test_record.py`, `plugin/tests/test_checks.py`

**Interfaces:**
- Produces: `class OpenTurns` with `__init__(self, path: pathlib.Path)`; `add(delivery_id: str, room_id: str) -> None`; `remove(delivery_id: str) -> None`; `all() -> dict[str, str]` (delivery ID → room ID, a copy); `for_room(room_id: str) -> str | None`.
- Produces: `checks.display_warnings(user_config: dict) -> list[str]`.

File format: `{"version": 1, "turns": {"<delivery id>": "<room id>"}}`, written to a temporary file in the same directory and moved into place with `os.replace`. The parent directory is created on first write.

`display_warnings` returns one sentence per setting that differs from the documented setup, naming the key and the value wanted: `display.platforms.hermes_chat.tool_progress` must be `"off"`; `display.platforms.hermes_chat.interim_assistant_messages` must be `False`; `streaming.enabled` must be `True`. A missing key counts as differing. Booleans written as YAML strings (`"false"`, `"off"`) are not accepted as `False`; the warning says so.

- [ ] **Step 1: Write the failing tests**

```python
def test_add_survives_reopen(tmp_path):
    p = tmp_path / "x" / "open.json"
    OpenTurns(p).add("D1", "R1")
    assert OpenTurns(p).all() == {"D1": "R1"}

def test_remove(tmp_path): ...
def test_remove_unknown_is_a_no_op(tmp_path): ...
def test_for_room(tmp_path): ...                      # "D1" for R1, None for R2
def test_missing_file_is_empty(tmp_path): ...

@pytest.mark.parametrize("content", [b"", b"{", b'{"version":1,"turns":[]}', b"\xff\xfe", b'{"version":99,"turns":{}}'])
def test_corrupt_file_is_empty_with_a_warning(tmp_path, caplog, content):
    p = tmp_path / "open.json"; p.write_bytes(content)
    assert OpenTurns(p).all() == {}
    assert "open-turn record" in caplog.text
    OpenTurns(p).add("D1", "R1")                      # and it can be written again
    assert OpenTurns(p).all() == {"D1": "R1"}

def test_no_temporary_file_left_behind(tmp_path): ...

def test_display_warnings_none_when_set_as_documented():
    cfg = {"streaming": {"enabled": True},
           "display": {"platforms": {"hermes_chat": {"tool_progress": "off", "interim_assistant_messages": False}}}}
    assert display_warnings(cfg) == []

def test_display_warnings_names_each_wrong_setting():
    w = display_warnings({})
    assert len(w) == 3
    assert any("tool_progress" in x for x in w) and any("interim_assistant_messages" in x for x in w) and any("streaming.enabled" in x for x in w)
```

- [ ] **Step 2: Run.** Expected: FAIL on import.
- [ ] **Step 3: Implement both modules.**
- [ ] **Step 4: Run.** Expected: all pass.
- [ ] **Step 5: Commit** `feat(plugin): the open-turn record and the display config check`.

---

### Task 6: The adapter: registration, connecting, and inbound mapping

**Files:**
- Create: `plugin/hermes_chat/adapter.py`, `plugin/tests/test_adapter_inbound.py`
- Modify: `plugin/tests/conftest.py`

**Interfaces:**
- Consumes: everything from Tasks 1 to 5.
- Produces, in `adapter.py`:
  - `PLATFORM = "hermes_chat"`, `REMOVED_AUTHOR_NAME = "removed user"`, `MAX_TEXT_BYTES = 262_144`.
  - `class HermesChatAdapter(BasePlatformAdapter)` with class attributes `MAX_MESSAGE_LENGTH = MAX_TEXT_BYTES` and `REQUIRES_EDIT_FINALIZE = True`, and a `message_len_fn` property returning UTF-8 byte length.
  - `__init__(self, config: PlatformConfig)`: reads URL and token with `extra_or_secret(config.extra, "url", "HERMES_CHAT_URL")` and `(…, "token", "HERMES_CHAT_TOKEN")`.
  - `async connect(self, *, is_reconnect: bool = False) -> bool`, `async disconnect(self) -> None`, `async get_chat_info(self, chat_id) -> dict`.
  - `event_from_delivery(self, data: dict) -> MessageEvent` (pure; no I/O).
  - `async _on_stream_event(self, name: str, data: dict) -> None`: the `EventStream` handler. In this task `delivery` drops repeats, records the open turn, builds the event, and calls `self.handle_message(event)`; the other four names are logged with their room ID. Task 7 puts the room gate in front of `handle_message`.
  - Module functions `check_requirements() -> bool`, `validate_config(config) -> bool`, `_env_enablement() -> dict | None`, `register(ctx) -> None`.
- Produces, in `conftest.py`: fixture `adapter_factory(stub, tmp_path, monkeypatch)` returning a function that builds a `HermesChatAdapter` pointed at the stub with the open-turn record under `tmp_path`, and fixture helper `delivery(message_id="M1", room_id="R1", flavor="dm", text="hi", author=..., note=None, context=(), reply=None) -> dict` building a `DeliveryEvent` body as `docs/openapi.yaml` defines it.

Decisions:

- `connect()`: take the token lock with `gateway.status.acquire_scoped_lock("hermes_chat", token)` and return `False` if not acquired (pattern: `plugins/platforms/irc/adapter.py`). Call `client.me()`: `Unauthorized` → `_set_fatal_error("unauthorized", <message naming HERMES_CHAT_TOKEN>, retryable=False)`, release the lock, return `False`; `Unreachable` → log a warning and carry on. Open the record at `get_hermes_home() / "hermes_chat" / f"open-turns-{name}.json"`, where `name` is the agent `id` from `me()` when it answered, else the first 16 hex characters of the SHA-256 of the token. Log each `display_warnings` line for the profile's config (`hermes_cli.config.load_config()`; a failure to load it is logged at debug and skipped) at warning level. Start the `EventStream` task. Call `_mark_connected()` and return `True`.
- The stream's `on_revoked` calls `_set_fatal_error("unauthorized", …, retryable=False)`.
- `disconnect()`: cancel the stream task, `await asyncio.wait_for(reporter.wait_idle(), 5)` ignoring a timeout, `reporter.aclose()`, `client.aclose()`, release the lock, `_mark_disconnected()`. The record file is left in place.
- `event_from_delivery`: the source is `build_source(chat_id=room["id"], chat_name=room["topic"] or author name for a DM, chat_type="dm" if room["flavor"] == "dm" else "group", user_id=author id, user_name=author name, is_bot=author["is_bot"], thread_id=None for a DM else room["id"])`. A `null` author gives `user_id=None`, `user_name=REMOVED_AUTHOR_NAME`, `is_bot=False`. The event is `MessageEvent(text=…, message_type=MessageType.TEXT, source=…, message_id=message["id"], reply_to_message_id/_text/_author_name as sent, channel_prompt=data["note"] or None, channel_context=…, allow_gateway_control=(author is not null and author["kind"] == "human"))`. `channel_context` is the `context` messages oldest first, one per line as `name: text` (a `null` author's name is `REMOVED_AUTHOR_NAME`), or `None` when `context` is empty. Check the flavor string for a DM against `Flavor` in `docs/openapi.yaml`.
- Repeats are dropped with `gateway.platforms.helpers.MessageDeduplicator`, keyed on the message ID, held as an attribute so Hermes's reconnect watcher can copy it.
- `register(ctx)`: `ctx.register_platform(name="hermes_chat", label="Hermes Chat", adapter_factory=HermesChatAdapter, check_fn=check_requirements, validate_config=validate_config, required_env=["HERMES_CHAT_URL", "HERMES_CHAT_TOKEN"], env_enablement_fn=_env_enablement, cron_deliver_env_var="HERMES_CHAT_HOME_ROOM", allowed_users_env="HERMES_CHAT_ALLOWED_USERS", allow_all_env="HERMES_CHAT_ALLOW_ALL_USERS", max_message_length=0, platform_hint=…, emoji="💬")`. `_env_enablement` returns `None` unless both URL and token are set, else `seed_extra_from_env((("HERMES_CHAT_URL", "url", None), ("HERMES_CHAT_TOKEN", "token", None)), home_env="HERMES_CHAT_HOME_ROOM")`. The `platform_hint` says: markdown is rendered; long, complete answers are fine; the reader usually comes back later rather than watching live.

- [ ] **Step 1: Write the failing tests in `test_adapter_inbound.py`**

```python
pytest.importorskip("gateway.platforms.base", reason="needs the Hermes checkout; set HERMES_SRC")

def test_dm_delivery_maps_to_event(adapter_factory, delivery):
    ev = adapter_factory().event_from_delivery(delivery(flavor="dm", text="hello", note="likes brevity"))
    assert (ev.text, ev.message_id, ev.channel_prompt) == ("hello", "M1", "likes brevity")
    assert (ev.source.chat_id, ev.source.chat_type, ev.source.thread_id) == ("R1", "dm", None)
    assert ev.allow_gateway_control is True

def test_channel_delivery_uses_room_as_thread(adapter_factory, delivery):
    ev = adapter_factory().event_from_delivery(delivery(flavor="channel"))
    assert (ev.source.chat_type, ev.source.thread_id) == ("group", "R1")

def test_two_authors_in_one_channel_share_a_session(adapter_factory, delivery):
    a = adapter_factory()
    k1 = a._event_session_key(a.event_from_delivery(delivery(flavor="channel", author=human("H1"))))
    k2 = a._event_session_key(a.event_from_delivery(delivery(flavor="channel", message_id="M2", author=human("H2"))))
    assert k1 == k2

def test_two_rooms_have_different_sessions(adapter_factory, delivery): ...

def test_agent_author_cannot_run_commands(adapter_factory, delivery):
    ev = adapter_factory().event_from_delivery(delivery(text="/new", author=agent("A2")))
    assert ev.allow_gateway_control is False and ev.source.is_bot is True

def test_null_author_gets_placeholder(adapter_factory, delivery):
    ev = adapter_factory().event_from_delivery(delivery(author=None))
    assert (ev.source.user_name, ev.source.user_id, ev.allow_gateway_control) == ("removed user", None, False)

def test_reply_fields_pass_through(adapter_factory, delivery): ...
def test_null_note_gives_no_channel_prompt(adapter_factory, delivery): ...
def test_context_is_formatted_oldest_first(adapter_factory, delivery):
    # context [("Ana","one"),("bot","two")] -> channel_context == "Ana: one\nbot: two"; () -> None

async def test_connect_succeeds_while_server_is_down(adapter_factory):
    a = adapter_factory(url="http://127.0.0.1:1")
    assert await a.connect() is True
    await a.disconnect()

async def test_connect_fails_on_bad_token_without_retry(adapter_factory, stub):
    stub.script("GET", "/agent/me", (401, {"error": {"code": "unauthenticated", "message": "x"}}))
    a = adapter_factory()
    assert await a.connect() is False
    assert a._fatal_error_retryable is False

async def test_connect_fails_when_token_lock_is_held(adapter_factory, monkeypatch): ...

async def test_delivery_reaches_the_message_handler_once(adapter_factory, stub, delivery):
    # stub.events(ready, delivery M1, delivery M1 again, Hang); handler records events
    # -> handler saw exactly one event with message_id "M1"; record contains {"M1": "R1"} while it runs

async def test_revoked_token_marks_the_adapter_fatal(adapter_factory, stub): ...   # events route answers 401

async def test_connect_warns_about_display_settings(adapter_factory, stub, monkeypatch, caplog):
    # hermes_cli.config.load_config patched to return {} -> caplog has three warnings naming the keys

def test_register_passes_the_spec_values(): ...
    # a fake ctx records register_platform kwargs: name, max_message_length == 0,
    # cron_deliver_env_var, allowed_users_env, allow_all_env

def test_env_enablement_needs_both(monkeypatch): ...
def test_message_length_is_counted_in_bytes(adapter_factory):
    assert adapter_factory().message_len_fn("é") == 2
```

`human(id)` and `agent(id)` are small helpers in `conftest.py` returning author objects. Tests install a handler with `adapter.set_message_handler(...)`.

- [ ] **Step 2: Run** `just plugin-check tests/test_adapter_inbound.py -v`. Expected: FAIL on import of `hermes_chat.adapter`.
- [ ] **Step 3: Implement `adapter.py`** as specified.
- [ ] **Step 4: Run.** Expected: all pass. If `test_two_authors_in_one_channel_share_a_session` fails, read `build_session_key` in `gateway/session.py` (around line 690) before changing anything: the thread rule is the mechanism the spec chose.
- [ ] **Step 5: Commit** `feat(plugin): the adapter connects and turns deliveries into Hermes events`.

---

### Task 7: The adapter: a turn from start to report

**Files:**
- Modify: `plugin/hermes_chat/adapter.py`
- Create: `plugin/tests/test_adapter_turn.py`

**Interfaces:**
- Consumes: Task 6's adapter; `TurnReporter.submit`; `OpenTurns`.
- Produces on `HermesChatAdapter`: `async send(self, chat_id, content, reply_to=None, metadata=None) -> SendResult`; `async edit_message(self, chat_id, message_id, content, *, finalize=False) -> SendResult`; `async on_processing_start(self, event) -> None`; `async on_processing_complete(self, event, outcome) -> None`; `_delivery_for(self, event) -> str | None` (the delivery this event's turn belongs to; Task 8 extends it); `_close_turn(self, delivery_id: str, state: str) -> None`.

Decisions:

- `send`: `client.post_message(chat_id, content, reply_to_id=reply_to)` → `SendResult(success=True, message_id=id)`. `ApiError` → `SendResult(success=False, error=f"{code}: {message}")`; a 409 `paused` is logged at info, others at warning. `Unreachable` → `SendResult(success=False, error=…, retryable=True)`.
- `edit_message`: the same mapping over `client.edit_message(message_id, content, finalize)`.
- `on_processing_start`: `d = _delivery_for(event)`; if there is one, `reporter.submit(d, "working")` and remember `asyncio.current_task()` as that delivery's task. The hook runs inside Hermes's per-message task (`_process_message_background`, `gateway/platforms/base.py` near line 4563).
- `on_processing_complete`: remember the outcome for the delivery and, the first time only, attach a done-callback to the remembered task. The callback calls `_close_turn(d, "failed" if outcome is FAILURE else "done")`. Reporting from the task's done-callback, not from the hook, is the point: `done` makes the server release the next message, and Hermes frees the session only after the hook returns (`_finish_session_task` in the `finally`).
- `_close_turn`: `reporter.submit(d, state)`, `record.remove(d)`, then release the room gate.
- **The room gate:** a room is busy from the moment a delivery for it is recorded until `_close_turn` for that delivery. `_on_stream_event("delivery")` hands the event to `handle_message` at once when the room is free, and otherwise parks it in that room's FIFO; closing a turn hands over the next parked delivery. Rooms are independent.
- **A delivery Hermes does not take:** after `await self.handle_message(event)`, if `event._gateway_accepted` is false, call `_close_turn(d, "failed")` and log a warning with the message ID. (`handle_message` sets that flag; see `base.py` near line 4041.)
- Every one of these four methods logs one info line under logger `hermes_chat.adapter` with the method name, room ID, message or delivery ID, and for `edit_message` the `finalize` flag, for `on_processing_complete` the outcome. No text.

- [ ] **Step 1: Write the failing tests in `test_adapter_turn.py`.** Each drives a real adapter with a fake message handler installed by `set_message_handler`; the handler may call `adapter.send` / `adapter.edit_message` itself (as Hermes's stream consumer does) and returns `None`, or returns text for Hermes's own final send.

```python
async def test_turn_reports_working_then_done(adapter_factory, stub, delivery):
    # handler returns "hello"; stub: POST messages -> 201 {"id":"M2"}
    # -> request paths, in order: /agent/deliveries/M1/working, /agent/rooms/R1/messages, /agent/deliveries/M1/done
    # -> record is empty afterwards

async def test_streamed_reply_ends_with_a_final_edit(adapter_factory, stub, delivery):
    # handler: send("R1","a") -> M2, edit_message("R1","M2","ab"), edit_message("R1","M2","abc", finalize=True)
    # -> PUT bodies [{"text":"ab","final":False},{"text":"abc","final":True}], then .../done

async def test_handler_exception_reports_failed(adapter_factory, stub, delivery): ...
    # handler raises RuntimeError -> last report path ends /M1/failed

async def test_done_is_sent_only_after_the_session_is_free(adapter_factory, stub, delivery):
    # when the stub receives .../M1/done, assert the event's session key is not in adapter._active_sessions

async def test_paused_room_refuses_the_reply_and_the_turn_still_closes(adapter_factory, stub, delivery):
    # POST messages -> 409 paused; handler returns "hello"
    # -> a report for M1 (done or failed) is requested, and record is empty

async def test_send_while_unreachable_is_a_retryable_failure(adapter_factory): ...

async def test_second_delivery_for_a_room_waits_for_the_first(adapter_factory, stub, delivery):
    # events: delivery M1 (R1), delivery M2 (R1); handler blocks on an asyncio.Event for M1
    # -> handler has seen only M1; set the event -> handler then sees M2; reports: M1 done before M2 working

async def test_other_rooms_do_not_wait(adapter_factory, stub, delivery):
    # delivery M1 (R1) blocked; delivery M3 (R2) -> handler sees M3 while M1 is still running

async def test_delivery_not_accepted_is_failed(adapter_factory, stub, delivery, monkeypatch):
    # monkeypatch adapter._drop_unresolved to return True -> .../M1/failed requested, record empty

async def test_human_slash_command_closes_its_turn(adapter_factory, stub, delivery):
    # delivery text "/new" from a human; handler returns "Started a new conversation."
    # -> working, a posted message, done

async def test_no_message_text_in_logs(adapter_factory, stub, delivery, caplog):
    # delivery text "s3cret-text"; run a turn -> "s3cret-text" not in caplog.text
```

- [ ] **Step 2: Run** `just plugin-check tests/test_adapter_turn.py -v`. Expected: FAIL (`send` is abstract or missing).
- [ ] **Step 3: Implement** the methods and the gate.
- [ ] **Step 4: Run** `just plugin-check`. Expected: every test so far passes.
- [ ] **Step 5: Commit** `feat(plugin): replies, streamed edits, and turn reports`.

---

### Task 8: The adapter: restart and shutdown

**Files:**
- Modify: `plugin/hermes_chat/adapter.py`
- Create: `plugin/tests/test_adapter_restart.py`

**Interfaces:**
- Consumes: Task 7's `_delivery_for`, `_close_turn`, room gate; `OpenTurns.all`, `for_room`.
- Produces on `HermesChatAdapter`: `RESUME_GRACE_SECONDS = 90.0` (class attribute, overridable per instance in tests); an override of `async handle_message(self, event)`; an override of `async cancel_background_tasks(self)`.

Decisions (the Hermes behaviour is in `gateway/run_startup.py`, `_schedule_resume_pending_sessions` and `_run_startup_resume_event`, near lines 84 and 596, and `gateway/platforms/base.py`, `cancel_background_tasks`, near line 4774):

- **On `connect()`**, before the stream starts: every delivery in the record becomes *awaiting resume*, its room is marked busy in the gate, and one timer of `RESUME_GRACE_SECONDS` is started. When it fires, each delivery still awaiting resume gets `_close_turn(d, "failed")`.
- **`handle_message` override:** an event with `event.internal` true and no `message_id`, whose `source.chat_id` has a delivery awaiting resume, is attached to that delivery: the delivery leaves the awaiting set, `event.metadata["hermes_chat_delivery"] = d`, and `event.reply_anchor_override = d`. Then `await super().handle_message(event)`; if `event._gateway_accepted` is false afterwards, `_close_turn(d, "failed")`. Every other event goes straight to `super()`.
- **`_delivery_for(event)`** now returns `event.metadata.get("hermes_chat_delivery")` when present, else `event.message_id` when it is an open delivery in the record, else `None`. An internal event with no attached delivery (a background-task notification) therefore reports nothing and does not touch the gate.
- **`cancel_background_tasks` override:** set `self._shutting_down = True`, then `await super().cancel_background_tasks()`. Hermes calls this before `disconnect()` on shutdown and on adapter replacement.
- **`on_processing_complete` while `_shutting_down`:** for outcome `CANCELLED`, attach no done-callback and report nothing; the delivery stays in the record for the next start. `SUCCESS` and `FAILURE` during shutdown are reported as usual (the turn really ended).
- A delivery frame that arrives for a message already in the record as awaiting resume is a repeat of the open turn: it is dropped, not parked.

- [ ] **Step 1: Write the failing tests in `test_adapter_restart.py`.** `resume_event(adapter, room_id)` in `conftest.py` builds what Hermes sends: `MessageEvent(text="", message_type=MessageType.TEXT, source=<a source for that room>, internal=True)`.

```python
async def test_open_turn_is_attached_to_a_resume_event(adapter_factory, stub):
    # record pre-seeded {"M1": "R1"}; connect; adapter.handle_message(resume_event(a, "R1")); handler returns "resumed"
    # -> no .../M1/failed; reports end with .../M1/done; the reply's POST carries "reply_to_id": "M1"; record empty

async def test_open_turn_not_resumed_is_failed_after_the_grace_period(adapter_factory, stub):
    # RESUME_GRACE_SECONDS = 0.05; record {"M1": "R1"}; connect; wait -> .../M1/failed requested; record empty

async def test_resume_after_the_grace_period_reports_nothing(adapter_factory, stub): ...
    # the internal event runs, its send succeeds, no report is made for M1 a second time

async def test_delivery_for_a_room_awaiting_resume_is_parked(adapter_factory, stub, delivery):
    # record {"M1": "R1"}; connect; stream delivers M2 (R1) -> handler has not seen M2
    # resume arrives and finishes -> handler then sees M2

async def test_redelivery_of_the_awaiting_message_is_dropped(adapter_factory, stub, delivery): ...

async def test_shutdown_cancellation_leaves_the_turn_open(adapter_factory, stub, delivery):
    # delivery M1; handler blocks; await a.cancel_background_tasks(); await a.disconnect()
    # -> no .../M1/done and no .../M1/failed were requested; record still {"M1": "R1"}

async def test_stop_cancellation_reports_done(adapter_factory, stub, delivery):
    # delivery M1; handler blocks; await a.cancel_session_processing(session_key) -> .../M1/done requested

async def test_internal_event_without_an_open_turn_reports_nothing(adapter_factory, stub):
    # empty record; handle_message(resume_event(a, "R9")); handler returns "note" -> no /agent/deliveries/ request at all

async def test_turn_that_finishes_during_shutdown_is_reported(adapter_factory, stub, delivery): ...
```

- [ ] **Step 2: Run** `just plugin-check tests/test_adapter_restart.py -v`. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `just plugin-check`. Expected: all pass.
- [ ] **Step 5: Commit** `feat(plugin): an interrupted turn continues after a restart`.

---

### Task 9: The hook probe

**Files:**
- Create: `plugin/hermes_chat/probe.py`, `plugin/tests/test_probe.py`
- Modify: `plugin/hermes_chat/adapter.py` (`register` calls `register_probe(ctx)`)

**Interfaces:**
- Produces: `probe.register_probe(ctx) -> None`; `probe.describe(hook: str, kwargs: dict, session: dict) -> str` (pure; builds the log line).

Decisions:

- `register_probe` calls `ctx.register_hook(name, callback)` for `pre_tool_call`, `post_tool_call`, and `on_interim_message`. Each callback accepts `**kwargs`, logs `describe(...)` at info under logger `hermes_chat.probe`, returns `None`, and never raises (a failure is logged at debug).
- The line is `probe hook=<name> tool=<tool_name or -> keys=<sorted kwarg names, comma-joined> session_id=… turn_id=… tool_call_id=… task_id=… platform=… chat_id=… session_key=…`. The identifier fields are taken from kwargs when present, else `-`. `platform`, `chat_id`, and `session_key` come from `gateway.session_context.get_session_env` for `HERMES_SESSION_PLATFORM`, `HERMES_SESSION_CHAT_ID`, and `HERMES_SESSION_KEY`; read the names actually defined in `gateway/session_context.py` (the tuple near line 44) and use those. `post_tool_call` also logs `duration_ms` and `status`, which are numbers and a fixed word.
- No kwarg value other than those named above appears in the line: not `args`, `result`, `text`, `error_message`, or anything unlisted.

- [ ] **Step 1: Write the failing tests**

```python
def test_describe_lists_names_and_ids_only():
    line = describe("pre_tool_call",
                    {"tool_name": "terminal", "args": {"command": "cat ~/.ssh/id_rsa"}, "session_id": "S1",
                     "turn_id": "T1", "tool_call_id": "C1", "task_id": ""},
                    {"platform": "hermes_chat", "chat_id": "R1", "session_key": "k"})
    assert "tool=terminal" in line and "session_id=S1" in line and "chat_id=R1" in line
    assert "keys=args,session_id,task_id,tool_call_id,tool_name,turn_id" in line
    assert "id_rsa" not in line and "cat" not in line

def test_describe_interim_message_has_no_text():
    assert "top secret" not in describe("on_interim_message", {"text": "top secret", "already_streamed": False}, {})

def test_describe_post_tool_call_includes_duration_and_status_not_result():
    line = describe("post_tool_call", {"tool_name": "t", "result": "PRIVATE", "duration_ms": 12, "status": "ok",
                                       "error_message": "PRIVATE2"}, {})
    assert "duration_ms=12" in line and "status=ok" in line and "PRIVATE" not in line

def test_register_probe_registers_three_hooks():      # fake ctx records register_hook names
def test_callback_never_raises(monkeypatch): ...      # describe patched to raise -> callback returns None
def test_adapter_register_also_registers_the_probe(): ...
```

- [ ] **Step 2: Run.** Expected: FAIL on import.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `just plugin-check`. Expected: all pass.
- [ ] **Step 5: Commit** `feat(plugin): a logging probe on the tool and remark hooks`.

---

### Task 10: End to end against a real server

**Files:**
- Create: `plugin/tests/test_e2e.py`
- Modify: `plugin/tests/conftest.py`

**Interfaces:**
- Consumes: the whole adapter; the `hermes-chat` binary.
- Produces, in `conftest.py`: session fixture `chat_binary` (runs `go build -o <tmp>/hermes-chat ./cmd/hermes-chat` from the repository root with `CGO_ENABLED=0`; skips the module with a clear reason when `go` is not on `PATH`); fixtures `e2e_adapter_factory` (builds a connected adapter on the server with a given message handler and record directory, and disconnects it at teardown) and `e2e_adapter` (one such adapter); fixture `chat_server` yielding an object with `url`, `agent_token`, `agent_id`, a logged-in human `httpx.Client` as `human`, and `stop()` / `start()` to restart the process on the same database and port.

Decisions: `chat_server` creates a temp directory, runs `migrate`, `user add kai` (password on standard input), `agent add bot Bot` (the token is the line matching `^token: (hca_[A-Za-z0-9_-]{43})$`), then `serve` with `HERMES_CHAT_DB_PATH`, `HERMES_CHAT_HTTP_ADDR=127.0.0.1:<free port>`, `HERMES_CHAT_DEBUG_ADDR=127.0.0.1:<free port>`, `HERMES_CHAT_TLS_MODE=off`, `HERMES_CHAT_COOKIE_SECURE=false`, and whatever else `README.md`'s environment table marks required (the attachments directory). It waits for the port to accept connections. The human logs in through `/api/login` with an `Origin` header that matches the host. Routes and bodies are in `docs/openapi.yaml`; `cmd/hermes-chat/serve_test.go` shows the same sequence in Go.

Each test builds an adapter on `chat_server.url` and the agent token, installs a fake message handler, and reads results back through the human API (`GET /api/rooms/{room}/messages`), polling with a 10-second deadline.

- [ ] **Step 1: Write the failing tests**

```python
pytest.importorskip("gateway.platforms.base", reason="needs the Hermes checkout; set HERMES_SRC")

async def test_human_posts_and_agent_replies(chat_server, e2e_adapter):
    # human opens the DM with the agent and posts "ping"; handler returns "pong"
    # -> the room's messages end with an agent message "pong"; the delivery state of "ping" for the agent is "done"

async def test_streamed_reply_is_final_and_not_streaming(chat_server, e2e_adapter):
    # handler sends "a", edits to "ab", finalizes "abc" -> last message text "abc", streaming false

async def test_second_message_is_answered_after_the_first(chat_server, e2e_adapter):
    # post "one" and "two" quickly; handler echoes -> replies "one", "two" in that order, both deliveries done

async def test_message_sent_while_agent_is_offline_is_answered_on_connect(chat_server, e2e_adapter_factory):
    # post before any adapter connects; then connect -> reply appears

async def test_server_restart_mid_turn(chat_server, e2e_adapter):
    # handler blocks; chat_server.stop(); chat_server.start(); release the handler
    # -> the reply appears and the delivery ends done (reports retried across the outage)

async def test_adapter_restart_mid_turn_resumes(chat_server, e2e_adapter_factory):
    # adapter 1: handler blocks; cancel_background_tasks(); disconnect()
    # adapter 2 (same record directory): connect; handle_message(resume_event(...)); handler returns "resumed"
    # -> "resumed" appears; the original delivery is done; a message posted afterwards is answered

async def test_reply_into_a_paused_room_closes_the_turn(chat_server, e2e_adapter):
    # handler waits; human pauses the room; handler returns "late"
    # -> no agent message "late"; the delivery is done or failed; after resume, the next message is answered
```

- [ ] **Step 2: Run** `just plugin-check tests/test_e2e.py -v`. Expected: the fixtures build and start the server; tests FAIL only if earlier tasks left a defect. A failure here is a finding: fix it in the module that owns it and add the missing unit test to that module's test file.
- [ ] **Step 3: Run** `just plugin-check` and `just check`. Expected: both pass. (`just check` must be unaffected: nothing under `plugin/` is a Go, web, or shell file.)
- [ ] **Step 4: Commit** `test(plugin): end to end against the real server`.

---

### Task 11: Setup steps, the checklist, and repository docs

**Files:**
- Create: `plugin/README.md`
- Modify: `AGENTS.md`, `README.md`

**Interfaces:**
- Consumes: the env var names, config keys, and logger names from Tasks 1 to 9.

- [ ] **Step 1: Write `plugin/README.md`** with these sections, in this order. Write for Kai following it alone on another machine. No real host or domain names: use `https://chat.example.com`.

  1. **What this is**: two sentences, and that it targets Hermes commit `6e7b83a2`.
  2. **Install**: on the chat server, `agent add` and add the agent to a room or open its DM; copy or symlink `plugin/hermes_chat` to `~/.hermes/plugins/hermes-chat`; add `hermes-chat` to `plugins.enabled` in `config.yaml` (confirm the exact key and value form against `hermes_cli/plugins_discovery.py`, `gate_manifest`, and state it literally).
  3. **Settings**: the two tables from the spec's section 4, and the note that each profile's `.env` needs all of them.
  4. **`config.yaml`**: the literal YAML block for `streaming.enabled: true`, `display.platforms.hermes_chat.tool_progress: off` (quoted so YAML does not read it as a boolean; state which form `checks.display_warnings` accepts), `display.platforms.hermes_chat.interim_assistant_messages: false`, and `agent.clarify_timeout: 120`; `approvals.timeout` left at its default. One paragraph on why, from the spec's section 8.
  5. **What works and what does not yet**: the two lists from the spec's section 3 goals and non-goals, in plain words, including that the agent's questions cannot be answered until stage 2.
  6. **Where the logs are**: the logger names `hermes_chat.adapter`, `hermes_chat.probe`, and where Hermes writes its gateway log for the desktop app and the command-line gateway (find this in the Hermes docs under `website/docs`; if it cannot be established from the source, say how to find it from the desktop app's diagnostics screen).
  7. **First-sitting checklist**: a numbered list. Each item has *Do*, *Expect in the room*, and *Send back* (the log lines to copy). Items, from the spec's section 8: a plain question; a long streamed reply; a question that makes the agent use a tool (send back the `hermes_chat.probe` lines); a second message sent while the first is being answered; stopping the gateway mid-reply and starting it again; a message sent while the gateway is stopped; `/new` typed by a human. End with the three questions from the spec's section 9 that the log answers.
  8. **Development**: `just plugin-check`, `HERMES_SRC`, and that the client modules import nothing from Hermes.

- [ ] **Step 2: Update `AGENTS.md`**: add `plugin/` to "Architecture & Data Flow" and "Key Directories"; add `just plugin-check` to "Development Commands" with the note that it needs the Hermes checkout and is not part of `just check`; add a "Hermes plugin" bullet to "Code Conventions" stating the import rule, the retry rule for turn reports, that `done` is reported from the processing task's done-callback, and that the probe logs no values; add the plugin tier to "Testing & QA".

- [ ] **Step 3: Update the root `README.md`**: the tool-version list (Python and uv are already there; add that they are used by the plugin's tests), and change the fake agent's "It is not the Hermes plugin" sentence to point at `plugin/README.md`.

- [ ] **Step 4: Verify the README's claims against the code.** Run:

```bash
grep -rn "HERMES_CHAT_" plugin/hermes_chat/plugin.yaml plugin/README.md | sort
grep -rnE "^(import|from) (gateway|hermes_cli|tools|agent|hermes_constants)" plugin/hermes_chat/{sse,client,stream,reports,record,checks}.py
```

Expected: the same five env var names in both files; the second command prints nothing.

- [ ] **Step 5: Run** `just plugin-check` and `just check`. Expected: both pass.
- [ ] **Step 6: Commit** `docs: installing and trying the Hermes plugin`.
