import asyncio
import json

import pytest

pytest.importorskip("gateway.platforms.base", reason="needs the Hermes checkout; set HERMES_SRC")

from stubserver import Hang  # noqa: E402
from hermes_chat.record import OpenTurns  # noqa: E402


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


def seed_record(tmp_path, entries):
    for delivery_id, room_id in entries.items():
        OpenTurns(_record_path(tmp_path)).add(delivery_id, room_id)


def _record_path(tmp_path):
    import hashlib
    name = hashlib.sha256(b"hca_test_token").hexdigest()[:16]
    return tmp_path / "hermes_chat" / f"open-turns-{name}.json"


async def test_open_turn_survives_a_restart_while_the_server_is_down(adapter_factory, stub, delivery):
    me_ok(stub)
    blocked = asyncio.Event()
    seen = []
    stub.events(ready(), frame("delivery", delivery(), 1), Hang)
    a1 = adapter_factory()

    async def handler(event):
        seen.append(event.message_id)
        await blocked.wait()
        return None

    a1.set_message_handler(handler)
    assert await a1.connect() is True
    await wait_until(lambda: seen == ["M1"])
    await a1.cancel_background_tasks()
    await a1.disconnect()
    assert a1._open_turns.all() == {"M1": "R1"}
    # Restart while the server is down: the same record must be found, so the
    # interrupted turn is still awaiting resume (Review finding: the record
    # name must not depend on whether /agent/me answered).
    a2 = adapter_factory(url="http://127.0.0.1:1")
    assert await a2.connect() is True
    assert a2._awaiting_resume == {"M1"}
    await a2.disconnect()


async def test_open_turn_is_attached_to_a_resume_event(adapter_factory, stub, tmp_path, resume_event):
    me_ok(stub)
    stub.script("POST", "/agent/rooms/R1/messages", (201, {"id": "M2"}))
    stub.events(Hang)
    seed_record(tmp_path, {"M1": "R1"})
    a = adapter_factory()

    async def handler(event):
        return "resumed"

    a.set_message_handler(handler)
    assert await a.connect() is True
    await a.handle_message(resume_event(a, "R1"))
    await wait_until(lambda: any(x.endswith("/M1/done") for x in paths(stub)))
    await wait_until(lambda: a._open_turns.all() == {})
    p = paths(stub)
    assert not any(x.endswith("/M1/failed") for x in p)
    assert any(x.endswith("/M1/working") for x in p), p
    posts = [r for r in stub.requests if r.path == "/agent/rooms/R1/messages"]
    assert posts and posts[0].json == {"text": "resumed", "reply_to_id": "M1"}
    await a.disconnect()


async def test_open_turn_not_resumed_is_failed_after_the_grace_period(adapter_factory, stub, tmp_path):
    me_ok(stub)
    stub.events(Hang)
    seed_record(tmp_path, {"M1": "R1"})
    a = adapter_factory()
    a.RESUME_GRACE_SECONDS = 0.05
    assert await a.connect() is True
    await wait_until(lambda: any(x.endswith("/M1/failed") for x in paths(stub)))
    assert a._open_turns.all() == {}
    await a.disconnect()


async def test_resume_after_the_grace_period_reports_nothing(adapter_factory, stub, tmp_path, resume_event):
    me_ok(stub)
    stub.script("POST", "/agent/rooms/R1/messages", (201, {"id": "M2"}))
    stub.events(Hang)
    seed_record(tmp_path, {"M1": "R1"})
    a = adapter_factory()
    a.RESUME_GRACE_SECONDS = 0.05

    async def handler(event):
        return "note"

    a.set_message_handler(handler)
    assert await a.connect() is True
    await wait_until(lambda: any(x.endswith("/M1/failed") for x in paths(stub)))
    await a.handle_message(resume_event(a, "R1"))
    await wait_until(lambda: any(x == "/agent/rooms/R1/messages" for x in paths(stub)))
    p = paths(stub)
    assert [x for x in p if x.endswith("/M1/failed")] == ["/agent/deliveries/M1/failed"]
    assert not any(x.endswith("/M1/done") for x in p)
    await a.disconnect()


async def test_delivery_for_a_room_awaiting_resume_is_parked(adapter_factory, stub, tmp_path, resume_event, delivery):
    me_ok(stub)
    stub.script("POST", "/agent/rooms/R1/messages", (201, {"id": "M2"}))
    d2 = delivery(message_id="M2", text="two")
    stub.events(ready(), frame("delivery", d2, 1), Hang)
    seed_record(tmp_path, {"M1": "R1"})
    a = adapter_factory()
    seen = []

    async def handler(event):
        seen.append(event.message_id)
        return None

    a.set_message_handler(handler)
    assert await a.connect() is True
    await asyncio.sleep(0.05)
    assert seen == []  # M2 is parked behind the awaiting resume
    await a.handle_message(resume_event(a, "R1"))
    await wait_until(lambda: seen == [None, "M2"])
    await wait_until(lambda: a._open_turns.all() == {})
    await a.disconnect()


async def test_redelivery_of_the_awaiting_message_is_dropped(adapter_factory, stub, tmp_path, delivery):
    me_ok(stub)
    stub.events(ready(), frame("delivery", delivery(), 1), Hang)
    seed_record(tmp_path, {"M1": "R1"})
    a = adapter_factory()
    seen = []

    async def handler(event):
        seen.append(event.message_id)

    a.set_message_handler(handler)
    assert await a.connect() is True
    await asyncio.sleep(0.05)
    assert seen == []  # a repeat of the open turn is not handed over
    assert a._open_turns.all() == {"M1": "R1"}
    await a.disconnect()


async def test_shutdown_cancellation_leaves_the_turn_open(adapter_factory, stub, delivery):
    me_ok(stub)
    blocked = asyncio.Event()
    seen = []
    stub.events(ready(), frame("delivery", delivery(), 1), Hang)
    a = adapter_factory()

    async def handler(event):
        seen.append(event.message_id)
        await blocked.wait()
        return None

    a.set_message_handler(handler)
    assert await a.connect() is True
    await wait_until(lambda: seen == ["M1"])
    await a.cancel_background_tasks()
    await a.disconnect()
    p = paths(stub)
    assert not any(x.endswith("/M1/done") for x in p), p
    assert not any(x.endswith("/M1/failed") for x in p), p
    assert a._open_turns.all() == {"M1": "R1"}


async def test_stop_cancellation_reports_done(adapter_factory, stub, delivery):
    me_ok(stub)
    blocked = asyncio.Event()
    seen = []
    stub.events(ready(), frame("delivery", delivery(), 1), Hang)
    a = adapter_factory()
    key = a._event_session_key(a.event_from_delivery(delivery()))

    async def handler(event):
        seen.append(event.message_id)
        await blocked.wait()
        return None

    a.set_message_handler(handler)
    assert await a.connect() is True
    await wait_until(lambda: seen == ["M1"])
    await a.cancel_session_processing(key)
    await wait_until(lambda: any(x.endswith("/M1/done") for x in paths(stub)))
    await wait_until(lambda: a._open_turns.all() == {})
    await a.disconnect()


async def test_internal_event_without_an_open_turn_reports_nothing(adapter_factory, stub, tmp_path, resume_event):
    me_ok(stub)
    stub.script("POST", "/agent/rooms/R9/messages", (201, {"id": "M2"}))
    stub.events(Hang)
    a = adapter_factory()

    async def handler(event):
        return "note"

    a.set_message_handler(handler)
    assert await a.connect() is True
    await a.handle_message(resume_event(a, "R9"))
    await wait_until(lambda: any(x == "/agent/rooms/R9/messages" for x in paths(stub)))
    assert not any(x.startswith("/agent/deliveries/") for x in paths(stub))
    await a.disconnect()


async def test_turn_that_finishes_during_shutdown_is_reported(adapter_factory, stub, delivery):
    me_ok(stub)
    stub.script("POST", "/agent/rooms/R1/messages", (201, {"id": "M2"}))
    stub.events(ready(), frame("delivery", delivery(), 1), Hang)
    a = adapter_factory()
    a._shutting_down = True

    async def handler(event):
        return None

    a.set_message_handler(handler)
    assert await a.connect() is True
    await wait_until(lambda: any(x.endswith("/M1/done") for x in paths(stub)))
    assert a._open_turns.all() == {}
    await a.disconnect()