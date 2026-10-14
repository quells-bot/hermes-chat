import asyncio
import json

from hermes_chat.client import ChatClient
from hermes_chat.steps import ToolSteps, shape


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


def test_shape_names_a_nameless_tool():
    assert shape("\x00\x01", None)[0] == "tool"


def test_shape_survives_arguments_that_are_not_json():
    tool, preview, arguments = shape("t", {"when": object()})
    assert tool == "t" and "object" in arguments


def paths(stub):
    return [(r.method, r.path) for r in stub.requests]


async def test_finish_follows_its_start_and_uses_its_activity(stub):
    stub.script("POST", "/agent/deliveries/M1/tools", (201, {"id": "ACT1"}))
    stub.script("PUT", "/agent/activities/ACT1", (200, {"id": "ACT1"}))
    client = ChatClient(stub.url, "t")
    steps = ToolSteps(client, asyncio.get_running_loop())

    def from_another_thread():
        steps.started("M1", "c1", "terminal", "echo hi", "{}")
        steps.finished("c1", 12, True)

    await asyncio.to_thread(from_another_thread)
    await steps.wait_idle()
    assert paths(stub) == [("POST", "/agent/deliveries/M1/tools"), ("PUT", "/agent/activities/ACT1")]
    assert stub.requests[1].json == {"duration_ms": 12, "succeeded": True}
    await steps.aclose()
    await client.aclose()


async def test_a_refused_start_drops_its_finish_and_later_tools_still_report(stub):
    stub.script("POST", "/agent/deliveries/M1/tools",
                (409, {"error": {"code": "paused", "message": "paused"}}), (201, {"id": "ACT2"}))
    stub.script("PUT", "/agent/activities/ACT2", (200, {"id": "ACT2"}))
    client = ChatClient(stub.url, "t")
    steps = ToolSteps(client, asyncio.get_running_loop())
    steps.started("M1", "c1", "terminal", "", "{}")
    steps.finished("c1", 5, True)
    steps.started("M1", "c2", "terminal", "", "{}")
    steps.finished("c2", 7, False)
    await steps.wait_idle()
    assert paths(stub) == [("POST", "/agent/deliveries/M1/tools"), ("POST", "/agent/deliveries/M1/tools"),
                           ("PUT", "/agent/activities/ACT2")]
    assert stub.requests[2].json == {"duration_ms": 7, "succeeded": False}
    await steps.aclose()
    await client.aclose()


async def test_finish_without_a_start_sends_nothing(stub):
    client = ChatClient(stub.url, "t")
    steps = ToolSteps(client, asyncio.get_running_loop())
    steps.finished("never-started", 5, True)
    await steps.wait_idle()
    assert stub.requests == []
    await steps.aclose()
    await client.aclose()


async def test_an_unreachable_server_is_not_retried(stub):
    client = ChatClient("http://127.0.0.1:9", "t")
    steps = ToolSteps(client, asyncio.get_running_loop())
    steps.started("M1", "c1", "terminal", "", "{}")
    steps.finished("c1", 5, True)
    await asyncio.wait_for(steps.wait_idle(), 5)
    await steps.aclose()
    await client.aclose()
