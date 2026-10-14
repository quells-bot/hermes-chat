import asyncio

import pytest

pytest.importorskip("gateway.platforms.base", reason="needs the Hermes checkout; set HERMES_SRC")

from stubserver import Hang  # noqa: E402
from test_adapter_turn import frame, me_ok, paths, ready, wait_until  # noqa: E402


def steer(message_id="S1", text="use the other file", room_id="R1"):
    author = {"kind": "human", "id": "H1", "name": "Human H1", "is_bot": False}
    return {"room_id": room_id, "message": {"id": message_id, "room_id": room_id, "author": author, "text": text}}


async def connect_with_a_running_turn(adapter_factory, stub, delivery, *control):
    """A turn for M1 in R1 whose first handler call never returns; `seen`
    collects every event the handler is given."""
    me_ok(stub)
    stub.script("POST", "/agent/rooms/R1/messages", (201, {"id": "M2"}), (201, {"id": "M3"}))
    stub.events(ready(), frame("delivery", delivery(), 1), *control, Hang)
    a = adapter_factory()
    seen = []

    async def handler(event):
        seen.append(event)
        if event.message_id == "M1":
            await asyncio.Event().wait()
        return None

    a.set_message_handler(handler)
    assert await a.connect() is True
    return a, seen


def commands(seen):
    """The control events among what the handler saw. A control frame that
    follows its delivery at once can reach the handler before the turn does."""
    return [e for e in seen if e.text.startswith("/")]


def reports(stub):
    return [p for p in paths(stub) if p.endswith(("/working", "/done", "/failed"))]


@pytest.mark.parametrize("control", ["stop", "pause"])
async def test_stop_and_pause_hand_hermes_its_stop_command(adapter_factory, stub, delivery, control):
    a, seen = await connect_with_a_running_turn(
        adapter_factory, stub, delivery, frame(control, {"room_id": "R1"}, 2))
    await wait_until(lambda: commands(seen))
    (command,) = commands(seen)
    assert command.text == "/stop" and command.allow_gateway_control is True
    assert command.source.chat_id == "R1" and not command.message_id
    await wait_until(lambda: "/agent/deliveries/M1/done" in paths(stub))
    assert reports(stub)[-1] == "/agent/deliveries/M1/done"
    assert reports(stub).count("/agent/deliveries/M1/done") == 1
    await a.disconnect()


async def test_steer_hands_hermes_its_steer_command_with_the_text(adapter_factory, stub, delivery):
    a, seen = await connect_with_a_running_turn(adapter_factory, stub, delivery, frame("steer", steer(), 2))
    await wait_until(lambda: commands(seen))
    (command,) = commands(seen)
    assert command.text == "/steer use the other file" and command.allow_gateway_control is True
    assert command.source.chat_id == "R1"
    await asyncio.sleep(0.1)
    assert reports(stub) == ["/agent/deliveries/M1/working"]
    await a.cancel_background_tasks()
    await a.disconnect()


async def test_a_repeated_steer_is_handed_over_once(adapter_factory, stub, delivery):
    a, seen = await connect_with_a_running_turn(
        adapter_factory, stub, delivery, frame("steer", steer(), 2), frame("steer", steer(), 3))
    await wait_until(lambda: commands(seen))
    await asyncio.sleep(0.2)
    assert len(commands(seen)) == 1
    await a.cancel_background_tasks()
    await a.disconnect()


@pytest.mark.parametrize("name,data", [
    ("stop", {"room_id": "R1"}), ("pause", {"room_id": "R1"}), ("steer", steer()),
])
async def test_control_for_a_room_with_no_running_turn_reaches_nothing(adapter_factory, stub, name, data):
    me_ok(stub)
    stub.events(ready(), frame(name, data, 1), Hang)
    a = adapter_factory()
    seen = []

    async def handler(event):
        seen.append(event)

    a.set_message_handler(handler)
    assert await a.connect() is True
    await wait_until(lambda: any(r.path == "/agent/events" for r in stub.requests))
    await asyncio.sleep(0.3)
    assert seen == []
    assert [p for p in paths(stub) if p not in ("/agent/me", "/agent/events")] == []
    await a.disconnect()


async def test_control_in_one_room_leaves_another_rooms_turn_alone(adapter_factory, stub, delivery):
    a, seen = await connect_with_a_running_turn(
        adapter_factory, stub, delivery, frame("stop", {"room_id": "R2"}, 2))
    await wait_until(lambda: len(seen) == 1)
    await asyncio.sleep(0.3)
    assert commands(seen) == [] and reports(stub) == ["/agent/deliveries/M1/working"]
    await a.cancel_background_tasks()
    await a.disconnect()
