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

async def test_delivery_queued_during_gateway_startup_closes_after_its_replay(adapter_factory, stub, delivery):
    """While the gateway is still starting, Hermes queues an inbound message
    and returns at once, then hands the same event back when startup is over.
    The turn is the replay, so that is what `done` must follow."""
    me_ok(stub)
    stub.script("POST", "/agent/rooms/R1/messages", (201, {"id": "M2"}))
    stub.events(ready(), frame("delivery", delivery(), 1), Hang)
    a = adapter_factory()

    class Runner:
        _startup_restore_queue = []

        def _profile_name_for_source(self, *args, **kwargs):
            return None

    a.gateway_runner = Runner()
    passes = []

    async def handler(event):
        if not getattr(event, "_hermes_startup_restore_replay", False):
            Runner._startup_restore_queue.append(event)
            passes.append("queued")
            return None
        passes.append("replayed")
        return "hello"

    a.set_message_handler(handler)
    assert await a.connect() is True
    await wait_until(lambda: passes == ["queued"])
    await asyncio.sleep(0.2)
    p = paths(stub)
    assert not any(x.endswith("/M1/done") for x in p), p
    assert a._open_turns.all() == {"M1": "R1"}

    event = Runner._startup_restore_queue.pop(0)
    event._hermes_startup_restore_replay = True
    await a.handle_message(event)
    await wait_until(lambda: any(x.endswith("/M1/done") for x in paths(stub)))
    p = paths(stub)
    assert p.index("/agent/rooms/R1/messages") < p.index("/agent/deliveries/M1/done"), p
    assert a._open_turns.all() == {}
    await a.disconnect()


class StoppingRunner:
    """What the adapter reads from the gateway runner while it is stopping."""

    def __init__(self, resume_pending):
        self._draining = False
        self._startup_restore_queue = []
        self.session_store = self
        self._resume_pending = resume_pending

    def lookup_by_session_key(self, session_key):
        class Entry:
            resume_pending = self._resume_pending

        return Entry()

    def _profile_name_for_source(self, *args, **kwargs):
        return None


async def test_turn_interrupted_by_a_stopping_gateway_is_left_open(adapter_factory, stub, delivery):
    """A stopping gateway interrupts the turn, which then ends as a success
    with its session marked for resume. Reporting it done would release the
    room's next message into a gateway that refuses new work."""
    me_ok(stub)
    stub.events(ready(), frame("delivery", delivery(), 1), Hang)
    a = adapter_factory()
    a.gateway_runner = StoppingRunner(resume_pending=True)
    finished = []

    async def handler(event):
        a.gateway_runner._draining = True
        finished.append(event.message_id)
        return None

    a.set_message_handler(handler)
    assert await a.connect() is True
    await wait_until(lambda: finished == ["M1"])
    await asyncio.sleep(0.2)
    await a.disconnect()
    p = paths(stub)
    assert not any(x.endswith("/M1/done") for x in p), p
    assert not any(x.endswith("/M1/failed") for x in p), p
    assert a._open_turns.all() == {"M1": "R1"}


async def test_turn_that_finishes_while_the_gateway_stops_is_reported(adapter_factory, stub, delivery):
    me_ok(stub)
    stub.events(ready(), frame("delivery", delivery(), 1), Hang)
    a = adapter_factory()
    a.gateway_runner = StoppingRunner(resume_pending=False)

    async def handler(event):
        a.gateway_runner._draining = True
        return None

    a.set_message_handler(handler)
    assert await a.connect() is True
    await wait_until(lambda: any(x.endswith("/M1/done") for x in paths(stub)))
    assert a._open_turns.all() == {}
    await a.disconnect()


async def test_delivery_arriving_while_the_gateway_stops_is_left_for_the_next_start(adapter_factory, stub, delivery):
    """Hermes answers new work with a canned refusal while it stops. The
    delivery is not handed over and not recorded, so the server still has it
    `delivered` and hands it over again on the next connect."""
    me_ok(stub)
    stub.events(ready(), frame("delivery", delivery(), 1), Hang)
    a = adapter_factory()
    a.gateway_runner = StoppingRunner(resume_pending=False)
    a.gateway_runner._draining = True
    seen = []

    async def handler(event):
        seen.append(event.message_id)
        return None

    a.set_message_handler(handler)
    assert await a.connect() is True
    await wait_until(lambda: any(r.path == "/agent/events" for r in stub.requests))
    await asyncio.sleep(0.3)
    await a.disconnect()
    assert seen == []
    assert a._open_turns.all() == {}
    assert not any("/deliveries/" in x for x in paths(stub)), paths(stub)


async def test_event_the_handler_puts_back_closes_after_its_real_turn(adapter_factory, stub, delivery):
    """When the session is busy elsewhere (as it is for Hermes's own resume
    after a restart), the handler puts the event back and returns; Hermes
    dispatches it again. The processing hooks run on every pass, and only the
    last one is the turn."""
    me_ok(stub)
    stub.script("POST", "/agent/rooms/R1/messages", (201, {"id": "M2"}))
    stub.events(ready(), frame("delivery", delivery(), 1), Hang)
    a = adapter_factory()
    passes = []

    async def handler(event):
        passes.append(event.message_id)
        if len(passes) == 1:
            a._pending_messages[a._event_session_key(event)] = event
            return None
        await asyncio.sleep(0.2)
        return "hello"

    a.set_message_handler(handler)
    assert await a.connect() is True
    await wait_until(lambda: len(passes) == 2)
    assert not any(x.endswith("/M1/done") for x in paths(stub)), paths(stub)
    await wait_until(lambda: any(x.endswith("/M1/done") for x in paths(stub)))
    p = paths(stub)
    assert p.index("/agent/rooms/R1/messages") < p.index("/agent/deliveries/M1/done"), p
    assert p.count("/agent/deliveries/M1/done") == 1, p
    await a.disconnect()
