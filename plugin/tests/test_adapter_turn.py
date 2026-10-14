import asyncio
import json

import pytest

pytest.importorskip("gateway.platforms.base", reason="needs the Hermes checkout; set HERMES_SRC")

from stubserver import Hang  # noqa: E402


async def wait_until(predicate, timeout=3.0):
    deadline = asyncio.get_event_loop().time() + timeout
    while asyncio.get_event_loop().time() < deadline:
        if predicate():
            return
        await asyncio.sleep(0.01)
    raise AssertionError("condition not met in time")


def ready(cursor=0):
    return f"event: ready\ndata: {{\"cursor\":{cursor}}}\n\n".encode()


def frame(event, data, id_=None):
    prefix = f"id: {id_}\n" if id_ is not None else ""
    return (prefix + f"event: {event}\ndata: {json.dumps(data)}\n\n").encode()


def me_ok(stub):
    stub.script("GET", "/agent/me", (200, {"id": "A1", "handle": "h", "display_name": "H"}))


def paths(stub):
    return [r.path for r in stub.requests]


def agent_paths(stub):
    return [r.path for r in stub.requests if r.path not in ("/agent/me", "/agent/events")]


async def run_turn(a, stub, d, handler, *extra_scripts):
    me_ok(stub)
    stub.script("POST", "/agent/rooms/R1/messages", (201, {"id": "M2"}))
    stub.events(ready(), frame("delivery", d, 1), *extra_scripts, Hang)
    a.set_message_handler(handler)
    assert await a.connect() is True


async def test_turn_reports_working_then_done(adapter_factory, stub, delivery):
    a = adapter_factory()

    async def handler(event):
        return "hello"

    await run_turn(a, stub, delivery(), handler)
    await wait_until(lambda: any(p.endswith("/M1/done") for p in paths(stub)))
    await wait_until(lambda: a._open_turns.all() == {})
    p = agent_paths(stub)
    assert p[0] == "/agent/deliveries/M1/working", p
    assert "/agent/rooms/R1/messages" in p
    assert p[-1] == "/agent/deliveries/M1/done", p
    await a.disconnect()


async def test_streamed_reply_ends_with_a_final_edit(adapter_factory, stub, delivery):
    stub.script("PUT", "/agent/messages/M2", (204,))
    a = adapter_factory()

    async def handler(event):
        await a.send("R1", "a")
        await a.edit_message("R1", "M2", "ab")
        await a.edit_message("R1", "M2", "abc", finalize=True)

    await run_turn(a, stub, delivery(), handler)
    await wait_until(lambda: any(p.endswith("/M1/done") for p in paths(stub)))
    puts = [r for r in stub.requests if r.method == "PUT"]
    assert [r.json for r in puts] == [{"text": "ab", "final": False}, {"text": "abc", "final": True}]
    assert paths(stub)[-1] == "/agent/deliveries/M1/done"
    await a.disconnect()


async def test_handler_exception_reports_failed(adapter_factory, stub, delivery):
    a = adapter_factory()

    async def boom(event):
        raise RuntimeError("kaboom")

    await run_turn(a, stub, delivery(), boom)
    await wait_until(lambda: any(p.endswith("/M1/failed") for p in paths(stub)))
    assert paths(stub)[-1] == "/agent/deliveries/M1/failed"
    await a.disconnect()


async def test_done_is_sent_only_after_the_session_is_free(adapter_factory, stub, delivery):
    a = adapter_factory()
    key = a._event_session_key(a.event_from_delivery(delivery()))
    observed = {}

    async def check_done(record):
        observed["session_free"] = key not in a._active_sessions
        return (204,)

    stub.script("POST", "/agent/deliveries/M1/done", check_done)

    async def handler(event):
        return None

    me_ok(stub)
    stub.script("POST", "/agent/rooms/R1/messages", (201, {"id": "M2"}))
    stub.events(ready(), frame("delivery", delivery(), 1), Hang)
    a.set_message_handler(handler)
    assert await a.connect() is True
    await wait_until(lambda: observed.get("session_free") is not None)
    assert observed["session_free"] is True
    await a.disconnect()


async def test_paused_room_refuses_the_reply_and_the_turn_still_closes(adapter_factory, stub, delivery):
    stub.script("POST", "/agent/rooms/R1/messages",
                (409, {"error": {"code": "paused", "message": "room is paused"}}))
    a = adapter_factory()

    async def handler(event):
        return "hello"

    await run_turn(a, stub, delivery(), handler)
    await wait_until(lambda: any(p.endswith("/M1/done") or p.endswith("/M1/failed") for p in paths(stub)))
    assert a._open_turns.all() == {}
    await a.disconnect()


async def test_send_while_unreachable_is_a_retryable_failure(adapter_factory, stub):
    stub.script("POST", "/agent/rooms/R1/messages", (500, {"error": {"code": "unavailable", "message": "boom"}}))
    a = adapter_factory()
    res = await a.send("R1", "hi")
    assert res.success is False
    assert res.retryable is True
    await a.disconnect()


async def test_second_delivery_for_a_room_waits_for_the_first(adapter_factory, stub, delivery):
    a = adapter_factory()
    go = asyncio.Event()
    seen = []
    d1 = delivery()
    d2 = delivery(message_id="M2", text="two")

    async def handler(event):
        seen.append(event.message_id)
        if event.message_id == "M1":
            await go.wait()
        return None

    me_ok(stub)
    stub.script("POST", "/agent/rooms/R1/messages", (201, {"id": "M2"}))
    stub.events(ready(), frame("delivery", d1, 1), frame("delivery", d2, 2), Hang)
    a.set_message_handler(handler)
    assert await a.connect() is True
    await wait_until(lambda: seen == ["M1"])
    await asyncio.sleep(0.05)
    assert seen == ["M1"]  # M2 is parked, not handled
    go.set()
    await wait_until(lambda: seen == ["M1", "M2"])
    await wait_until(lambda: a._open_turns.all() == {})
    p = agent_paths(stub)
    assert any(x.endswith("/M1/done") for x in p), p
    assert any(x.endswith("/M2/working") for x in p), p
    await a.disconnect()


async def test_other_rooms_do_not_wait(adapter_factory, stub, delivery):
    a = adapter_factory()
    block = asyncio.Event()
    seen = []
    d1 = delivery()
    d3 = delivery(message_id="M3", room_id="R2", text="other")

    async def handler(event):
        seen.append(event.message_id)
        if event.message_id == "M1":
            await block.wait()
        return None

    me_ok(stub)
    stub.script("POST", "/agent/rooms/R1/messages", (201, {"id": "M2"}))
    stub.script("POST", "/agent/rooms/R2/messages", (201, {"id": "M4"}))
    stub.events(ready(), frame("delivery", d1, 1), frame("delivery", d3, 2), Hang)
    a.set_message_handler(handler)
    assert await a.connect() is True
    await wait_until(lambda: "M1" in seen and "M3" in seen)
    block.set()
    await wait_until(lambda: a._open_turns.all() == {})
    await a.disconnect()


async def test_delivery_not_accepted_is_failed(adapter_factory, stub, delivery, monkeypatch):
    a = adapter_factory()
    monkeypatch.setattr(a, "_drop_unresolved", lambda event: True)

    async def handler(event):
        return None

    await run_turn(a, stub, delivery(), handler)
    await wait_until(lambda: any(p.endswith("/M1/failed") for p in paths(stub)))
    assert a._open_turns.all() == {}
    await a.disconnect()


async def test_human_slash_command_closes_its_turn(adapter_factory, stub, delivery, human):
    a = adapter_factory()

    async def handler(event):
        return "Started a new conversation."

    await run_turn(a, stub, delivery(text="/new", author=human("H1")), handler)
    await wait_until(lambda: any(p.endswith("/M1/done") for p in paths(stub)))
    assert a._open_turns.all() == {}
    p = agent_paths(stub)
    post = [x for x in p if x == "/agent/rooms/R1/messages"]
    assert post == ["/agent/rooms/R1/messages"]
    assert any(x.endswith("/M1/working") for x in p), p
    assert p[-1] == "/agent/deliveries/M1/done", p
    await a.disconnect()


async def test_no_message_text_in_logs(adapter_factory, stub, delivery, caplog):
    a = adapter_factory()

    async def handler(event):
        return None

    await run_turn(a, stub, delivery(text="s3cret-text"), handler)
    await wait_until(lambda: a._open_turns.all() == {})
    assert "s3cret-text" not in caplog.text
    await a.disconnect()

async def test_streaming_cursor_is_stripped_from_what_is_sent(adapter_factory, stub):
    """Hermes ends in-progress text with a cursor. Stored in the message, it
    would stay for good on a reply cut off before its final edit."""
    stub.script("POST", "/agent/rooms/R1/messages", (201, {"id": "M2"}))
    stub.script("PUT", "/agent/messages/M2", (204,))
    stub.script("PUT", "/agent/messages/M2", (204,))
    a = adapter_factory()
    await a.send("R1", "Hello ▉")
    await a.edit_message("R1", "M2", "Hello there ▉")
    await a.edit_message("R1", "M2", "Hello there.", finalize=True)
    texts = [r.json["text"] for r in stub.requests]
    assert texts == ["Hello", "Hello there", "Hello there."], texts
    await a._client.aclose()


async def test_text_that_is_only_the_cursor_is_sent_as_it_is(adapter_factory, stub):
    """The server refuses a blank message, and Hermes needs the id back."""
    stub.script("POST", "/agent/rooms/R1/messages", (201, {"id": "M2"}))
    a = adapter_factory()
    result = await a.send("R1", " ▉")
    assert result.success
    assert stub.requests[0].json["text"] == " ▉"
    await a._client.aclose()


async def test_leading_blank_lines_are_trimmed_from_what_is_sent(adapter_factory, stub):
    stub.script("POST", "/agent/rooms/R1/messages", (201, {"id": "M2"}))
    stub.script("PUT", "/agent/messages/M2", (204,))
    a = adapter_factory()
    await a.send("R1", "\n\nNext I will run it. ▉")
    await a.edit_message("R1", "M2", "\n\n  Next I will run it.", finalize=True)
    assert [r.json["text"] for r in stub.requests] == ["Next I will run it.", "Next I will run it."]
    await a._client.aclose()


async def test_tool_step_is_recorded_against_the_rooms_open_delivery(adapter_factory, stub, delivery, caplog):
    me_ok(stub)
    stub.script("POST", "/agent/deliveries/M1/tools", (201, {"id": "ACT1"}))
    stub.script("PUT", "/agent/activities/ACT1", (200, {"id": "ACT1"}))
    stub.events(ready(), frame("delivery", delivery(), 1), Hang)
    a = adapter_factory()
    release = asyncio.Event()

    async def handler(event):
        await release.wait()
        return None

    a.set_message_handler(handler)
    caplog.set_level("INFO")
    assert await a.connect() is True
    await wait_until(lambda: a._open_turns.all() == {"M1": "R1"})
    assert a.tool_started("R1", "c1", "terminal", {"command": "echo hi"}) is True
    a.tool_finished("c1", 12.7, "ok")
    await wait_until(lambda: any(p == "/agent/activities/ACT1" for p in paths(stub)))
    started = next(r for r in stub.requests if r.path == "/agent/deliveries/M1/tools")
    assert started.json["tool"] == "terminal" and started.json["preview"] == "echo hi"
    finished = next(r for r in stub.requests if r.path == "/agent/activities/ACT1")
    assert finished.json == {"duration_ms": 12, "succeeded": True}
    assert "tool_started delivery=M1" in caplog.text and "tool=terminal" in caplog.text
    assert "echo hi" not in caplog.text
    release.set()
    await a.disconnect()


async def test_tool_step_for_a_room_with_no_open_delivery_is_sent_nowhere(adapter_factory, stub):
    me_ok(stub)
    stub.events(ready(), Hang)
    a = adapter_factory()
    a.set_message_handler(lambda event: None)
    assert await a.connect() is True
    assert a.tool_started("R9", "c1", "terminal", {"command": "echo hi"}) is False
    a.tool_finished("c1", 5, "ok")
    await asyncio.sleep(0.1)
    assert not [p for p in paths(stub) if "/tools" in p or "/activities/" in p]
    await a.disconnect()


async def test_a_failed_tool_is_recorded_as_not_succeeded(adapter_factory, stub, delivery):
    me_ok(stub)
    stub.script("POST", "/agent/deliveries/M1/tools", (201, {"id": "ACT1"}))
    stub.script("PUT", "/agent/activities/ACT1", (200, {"id": "ACT1"}))
    stub.events(ready(), frame("delivery", delivery(), 1), Hang)
    a = adapter_factory()
    release = asyncio.Event()

    async def handler(event):
        await release.wait()

    a.set_message_handler(handler)
    assert await a.connect() is True
    await wait_until(lambda: a._open_turns.all() == {"M1": "R1"})
    a.tool_started("R1", "c1", "terminal", None)
    a.tool_finished("c1", None, "error")
    await wait_until(lambda: any(p == "/agent/activities/ACT1" for p in paths(stub)))
    finished = next(r for r in stub.requests if r.path == "/agent/activities/ACT1")
    assert finished.json == {"duration_ms": 0, "succeeded": False}
    release.set()
    await a.disconnect()


async def running_turn(adapter_factory, stub, delivery):
    me_ok(stub)
    stub.events(ready(), frame("delivery", delivery(), 1), Hang)
    a = adapter_factory()
    release = asyncio.Event()

    async def handler(event):
        await release.wait()

    a.set_message_handler(handler)
    assert await a.connect() is True
    await wait_until(lambda: a._open_turns.all() == {"M1": "R1"})
    return a, release


@pytest.mark.parametrize("response,sentence", [
    ((201, {"id": "E1", "text": "x"}), "Suggested. The person will see it in their note and decide whether to keep it."),
    ((200, {"id": "E1", "text": "x"}), "Suggested. The person will see it in their note and decide whether to keep it."),
    ((409, {"error": {"code": "conflict", "message": "the quarantine is full"}}),
     "The suggestion was not accepted: the quarantine is full"),
    ((503, {"error": {"code": "unavailable", "message": "down"}}), "The chat server could not be reached."),
])
async def test_a_note_suggestion_goes_to_the_rooms_open_delivery(adapter_factory, stub, delivery, response, sentence):
    stub.script("POST", "/agent/deliveries/M1/note-entries", response)
    a, release = await running_turn(adapter_factory, stub, delivery)
    try:
        assert a.has_open_turn("R1") is True
        assert await a.suggest_note_entry("R1", "Prefers short answers") == sentence
        sent = [r.json for r in stub.requests if r.path == "/agent/deliveries/M1/note-entries"]
        assert sent == [{"text": "Prefers short answers"}]
    finally:
        release.set()
        await a.disconnect()


async def test_a_note_suggestion_with_no_open_turn_is_sent_nowhere(adapter_factory, stub, delivery):
    a, release = await running_turn(adapter_factory, stub, delivery)
    try:
        assert a.has_open_turn("R9") is False
        assert await a.suggest_note_entry("R9", "x") == "There is no open turn in this room to suggest an entry from."
        assert not [p for p in paths(stub) if "note-entries" in p]
    finally:
        release.set()
        await a.disconnect()


async def test_a_tool_from_another_profiles_session_is_not_recorded_here(adapter_factory, stub, delivery):
    """Both profiles' adapters have the same delivery open when a message
    wakes both agents. The step belongs to the one whose session ran the tool."""
    stub.script("POST", "/agent/deliveries/M1/tools", (201, {"id": "ACT1"}))
    a, release = await running_turn(adapter_factory, stub, delivery)
    try:
        await wait_until(lambda: "/agent/deliveries/M1/working" in paths(stub))
        mine = a._room_sessions["R1"]
        assert a.tool_started("R1", "c1", "terminal", {}, session_key="agent:other:hermes_chat:dm:R1") is False
        assert a.has_open_turn("R1", session_key="agent:other:hermes_chat:dm:R1") is False
        assert a.tool_started("R1", "c2", "terminal", {}, session_key=mine) is True
        assert a.has_open_turn("R1", session_key=mine) is True
        assert a.tool_started("R1", "c3", "terminal", {}) is True
    finally:
        release.set()
        await a.disconnect()


def config_for(stub, token="hca_test_token"):
    from gateway.config import PlatformConfig
    return PlatformConfig(extra={"url": stub.url, "token": token})


async def test_a_send_with_no_connected_adapter_posts_to_the_room(adapter_factory, stub):
    """A scheduled job that Hermes runs outside the gateway process."""
    from hermes_chat.adapter import _standalone_send
    stub.script("POST", "/agent/rooms/R1/messages", (201, {"id": "M9"}))
    result = await _standalone_send(config_for(stub), "R1", "\n\nJob output ▉")
    assert result == {"success": True, "message_id": "M9"}
    (request,) = stub.requests
    assert request.json == {"text": "Job output"}
    assert request.headers["authorization"] == "Bearer hca_test_token"


async def test_a_standalone_send_the_room_refuses_is_an_error(adapter_factory, stub):
    from hermes_chat.adapter import _standalone_send
    stub.script("POST", "/agent/rooms/R1/messages", (409, {"error": {"code": "paused", "message": "the room is paused"}}))
    result = await _standalone_send(config_for(stub), "R1", "Job output")
    assert result == {"error": "paused: the room is paused"}


async def test_a_standalone_send_with_nothing_configured_is_an_error(adapter_factory, monkeypatch):
    from gateway.config import PlatformConfig
    from hermes_chat import adapter as adapter_mod
    monkeypatch.setattr(adapter_mod, "extra_or_secret", lambda extra, key, env: extra.get(key, ""))
    result = await adapter_mod._standalone_send(PlatformConfig(extra={}), "R1", "Job output")
    assert "HERMES_CHAT_URL" in result["error"]


async def test_a_standalone_send_with_only_files_says_they_were_not_sent(adapter_factory, stub):
    from hermes_chat.adapter import _standalone_send
    result = await _standalone_send(config_for(stub), "R1", "", media_files=["/tmp/report.pdf"])
    assert "error" in result and stub.requests == []


@pytest.mark.parametrize("code", ["paused", "conflict", "room_archived"])
async def test_a_refusal_is_logged_with_the_servers_reason(adapter_factory, stub, caplog, code):
    """A 409 is not always a pause: a full note quarantine and an archived
    room answer 409 too, and the log is what Kai sends back."""
    stub.script("POST", "/agent/rooms/R1/messages", (409, {"error": {"code": code, "message": "no"}}))
    a = adapter_factory()
    caplog.set_level("INFO", logger="hermes_chat")
    assert (await a.send("R1", "hello")).success is False
    assert f"send room=R1 refused ({code})" in caplog.text
    await a._client.aclose()
