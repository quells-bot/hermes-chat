import asyncio

from hermes_chat.client import ChatClient
from hermes_chat.reports import TurnReporter
from stubserver import Hang


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


def reporter(stub, *args, **kwargs):
    return TurnReporter(ChatClient(stub.url, "t"), *args, **kwargs)


async def test_reports_for_one_delivery_go_in_order(stub):
    stub.script("POST", "/agent/deliveries/D1/working", (204,))
    stub.script("POST", "/agent/deliveries/D1/done", (204,))
    r = reporter(stub)
    r.submit("D1", "working")
    r.submit("D1", "done")
    await r.wait_idle()
    assert [req.path for req in stub.requests] == [
        "/agent/deliveries/D1/working",
        "/agent/deliveries/D1/done",
    ]
    await r.aclose()


async def test_retries_while_unreachable_then_succeeds(stub):
    stub.script("POST", "/agent/deliveries/D1/done", (500,), (500,), (204,))
    sleeps, fake = sleep_recorder()
    r = reporter(stub, sleep=fake)
    r.submit("D1", "done")
    await r.wait_idle()
    done = [req for req in stub.requests if req.path == "/agent/deliveries/D1/done"]
    assert len(done) == 3
    assert sleeps == [0.5, 1.0]
    await r.aclose()


async def test_second_report_waits_for_the_first_to_be_answered(stub):
    stub.script("POST", "/agent/deliveries/D1/working", (500,), (204,))
    stub.script("POST", "/agent/deliveries/D1/done", (204,))
    sleeps, fake = sleep_recorder()
    r = reporter(stub, sleep=fake)
    r.submit("D1", "working")
    r.submit("D1", "done")
    await r.wait_idle()
    assert [req.path for req in stub.requests] == [
        "/agent/deliveries/D1/working",
        "/agent/deliveries/D1/working",
        "/agent/deliveries/D1/done",
    ]
    assert sleeps == [0.5]
    await r.aclose()


async def test_404_and_409_end_the_retrying(stub):
    stub.script("POST", "/agent/deliveries/D1/working", (404, {"error": {"code": "no_turn", "message": "no"}}))
    stub.script("POST", "/agent/deliveries/D1/done", (409, {"error": {"code": "paused", "message": "p"}}))
    r = reporter(stub)
    r.submit("D1", "working")
    r.submit("D1", "done")
    await r.wait_idle()
    assert len(stub.requests) == 2
    await r.aclose()


async def test_other_deliveries_are_not_blocked(stub):
    stub.script("POST", "/agent/deliveries/D1/working", Hang)
    stub.script("POST", "/agent/deliveries/D2/done", (204,))
    r = reporter(stub)
    r.submit("D1", "working")
    r.submit("D2", "done")
    await wait_until(lambda: any(req.path == "/agent/deliveries/D2/done" for req in stub.requests))
    await r.aclose()


async def test_aclose_stops_retrying(stub):
    stub.script("POST", "/agent/deliveries/D1/failed", (500,), (500,), (500,), (204,))
    r = reporter(stub)  # the real sleep keeps the worker inside a backoff
    r.submit("D1", "failed")
    await wait_until(lambda: len([q for q in stub.requests if q.path.endswith("/failed")]) == 2)
    await r.aclose()
    await asyncio.sleep(0.05)
    failed = [q for q in stub.requests if q.path.endswith("/failed")]
    assert len(failed) == 2
    assert await r.wait_idle() is None