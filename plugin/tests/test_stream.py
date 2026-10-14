import asyncio
import json

import pytest

from hermes_chat.client import ChatClient
from hermes_chat.stream import EventStream
from stubserver import Drop, Hang


def ready(cursor):
    return f"event: ready\ndata: {{\"cursor\": {cursor}}}\n\n".encode()


def frame(event, data, id_=None):
    prefix = f"id: {id_}\n" if id_ is not None else ""
    return (prefix + f"event: {event}\ndata: {json.dumps(data)}\n\n").encode()


def events_requests(stub):
    return [r for r in stub.requests if r.path == "/agent/events"]


async def wait_until(predicate, timeout=3.0):
    deadline = asyncio.get_event_loop().time() + timeout
    while asyncio.get_event_loop().time() < deadline:
        if predicate():
            return
        await asyncio.sleep(0.01)
    raise AssertionError("condition not met in time")


def sleep_recorder():
    sleeps: list[float] = []

    async def fake_sleep(secs):
        sleeps.append(secs)
        await asyncio.sleep(0)

    return sleeps, fake_sleep


async def noop(name, data):
    pass


def make_stream(stub, on_event=noop, on_revoked=lambda: None, *, sleep=asyncio.sleep, **kwargs):
    return EventStream(ChatClient(stub.url, "t"), on_event, on_revoked=on_revoked, sleep=sleep, **kwargs)





async def test_delivers_events_in_order_and_tracks_cursor(stub):
    stub.events(ready(3), frame("delivery", {"m": 1}, 4), frame("stop", {"room_id": "R1"}, 5), Hang)
    seen = []

    async def record(name, data):
        seen.append((name, data))

    s = make_stream(stub, record)
    task = asyncio.create_task(s.run())
    await wait_until(lambda: len(seen) == 2)
    assert seen == [("delivery", {"m": 1}), ("stop", {"room_id": "R1"})]
    assert s.cursor == 5
    task.cancel()
    with pytest.raises(asyncio.CancelledError):
        await task


async def test_ready_is_not_passed_on(stub):
    stub.events(ready(3), frame("delivery", {"a": 1}, 4), Hang)
    seen = []

    async def record(name, data):
        seen.append((name, data))

    s = make_stream(stub, record)
    task = asyncio.create_task(s.run())
    await wait_until(lambda: len(seen) == 1)
    assert seen == [("delivery", {"a": 1})]
    assert s.cursor == 4
    task.cancel()
    with pytest.raises(asyncio.CancelledError):
        await task


async def test_reconnects_with_last_cursor_after_drop(stub):
    stub.events(ready(3), frame("delivery", {}, 4), Drop)
    sleeps, fake_sleep = sleep_recorder()
    s = make_stream(stub, sleep=fake_sleep)
    task = asyncio.create_task(s.run())
    await wait_until(lambda: len(events_requests(stub)) >= 2)
    assert events_requests(stub)[1].headers.get("last-event-id") == "4"
    assert s.cursor == 4
    task.cancel()
    with pytest.raises(asyncio.CancelledError):
        await task


async def test_backoff_doubles_and_caps(stub):
    stub.events(Drop, Drop, Drop, Drop, Drop, Drop, Hang)
    sleeps, fake_sleep = sleep_recorder()
    s = make_stream(stub, sleep=fake_sleep)
    task = asyncio.create_task(s.run())
    await wait_until(lambda: len(events_requests(stub)) == 7)
    assert sleeps == [1.0, 2.0, 4.0, 8.0, 16.0, 30.0]
    task.cancel()
    with pytest.raises(asyncio.CancelledError):
        await task


async def test_backoff_resets_after_ready(stub):
    stub.events(Drop, ready(5), Drop, ready(6), Hang)
    sleeps, fake_sleep = sleep_recorder()
    s = make_stream(stub, sleep=fake_sleep)
    task = asyncio.create_task(s.run())
    await wait_until(lambda: len(events_requests(stub)) == 3)
    assert sleeps == [1.0, 1.0]
    assert s.cursor == 6
    task.cancel()
    with pytest.raises(asyncio.CancelledError):
        await task


async def test_silence_is_treated_as_dead(stub):
    stub.events(ready(1), Hang)
    s = make_stream(stub, silence_timeout=0.05)
    task = asyncio.create_task(s.run())
    await wait_until(lambda: len(events_requests(stub)) >= 2)
    task.cancel()
    with pytest.raises(asyncio.CancelledError):
        await task


async def test_heartbeat_comment_counts_as_life(stub):
    async def heartbeat(emit):
        try:
            while True:
                emit.write(b": hb\n")
                await emit.drain()
                await asyncio.sleep(0.02)
        except Exception:
            pass

    stub.events(ready(1), heartbeat, Hang)
    s = make_stream(stub, silence_timeout=0.05)
    task = asyncio.create_task(s.run())
    await asyncio.sleep(0.25)
    assert len(events_requests(stub)) == 1
    assert s.connected
    task.cancel()
    with pytest.raises(asyncio.CancelledError):
        await task


async def test_unauthorized_calls_on_revoked_and_stops(stub):
    stub.script("GET", "/agent/events", (401, {"error": {"code": "unauthenticated", "message": "revoked"}}))
    revoked = []
    s = make_stream(stub, on_revoked=lambda: revoked.append(1))
    await s.run()
    assert revoked == [1]


async def test_handler_exception_does_not_stop_the_stream(stub):
    stub.events(ready(1), frame("bad", {"x": 1}, 2), frame("good", {"y": 2}, 3), Hang)
    seen = []

    async def record(name, data):
        if name == "bad":
            raise RuntimeError("boom")
        seen.append((name, data))

    s = make_stream(stub, record)
    task = asyncio.create_task(s.run())
    await wait_until(lambda: len(seen) == 1)
    assert seen == [("good", {"y": 2})]
    assert s.cursor == 3
    task.cancel()
    with pytest.raises(asyncio.CancelledError):
        await task


async def test_non_json_data_is_skipped(stub):
    stub.events(ready(1), b"id: 2\nevent: junk\ndata: not-json\n\n", frame("ok", {"z": 9}, 3), Hang)
    seen = []

    async def record(name, data):
        seen.append((name, data))

    s = make_stream(stub, record)
    task = asyncio.create_task(s.run())
    await wait_until(lambda: len(seen) == 1)
    assert seen == [("ok", {"z": 9})]
    assert s.cursor == 3
    task.cancel()
    with pytest.raises(asyncio.CancelledError):
        await task


async def test_connected_flag(stub):
    keep = asyncio.Event()

    async def hold_then_drop(emit):
        await keep.wait()  # the connection stays open until the test lets go

    stub.events(ready(1), hold_then_drop, Drop)
    s = make_stream(stub)
    assert s.connected is False
    task = asyncio.create_task(s.run())
    await wait_until(lambda: s.connected and s.cursor == 1)
    keep.set()
    await wait_until(lambda: not s.connected)
    await wait_until(lambda: s.connected)
    task.cancel()
    with pytest.raises(asyncio.CancelledError):
        await task