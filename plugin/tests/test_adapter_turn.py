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