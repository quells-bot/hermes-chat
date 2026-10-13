"""Whole turns through a real Hermes gateway and a real hermes-chat server,
with a scripted model. Slow (seconds each); run alone with `-m gateway`."""
import pytest

from realgateway import gateway_factory  # noqa: F401  (also skips this module without Hermes)
from tests.fakes.fake_llm_provider import Text, ToolCall

pytestmark = pytest.mark.gateway

COMMENTARY = [
    ToolCall("terminal", {"command": "echo one"}, text="I am about to run the first echo."),
    ToolCall("terminal", {"command": "echo two"}, text="Next I will run the second echo."),
    Text("Both echoes ran."),
]


def index_of(events, predicate):
    return next(i for i, (name, data) in enumerate(list(events)) if predicate(name, data))


def test_plugin_loads_through_the_hermes_loader_and_answers(gateway_factory):
    g = gateway_factory([Text("hello back")])
    g.start()
    g.wait_started()
    first = g.post("hello")
    g.wait(lambda: g.state(first) == "done")
    assert g.agent_texts()[-1] == "hello back"  # Hermes may first post its own "no home channel" notice
    assert "Failed to load plugin" not in g.log()


def test_message_waiting_at_startup_is_done_after_its_reply(gateway_factory):
    g = gateway_factory([Text("hello back")])
    first = g.post("hello")  # on the server before the gateway exists
    g.start()
    g.wait(lambda: g.state(first) == "done")
    reply = index_of(g.events, lambda n, d: n == "message.posted" and d["message"]["author"]["kind"] == "agent")
    done = index_of(g.events, lambda n, d: n == "delivery.changed" and d["message"]["id"] == first
                    and d["message"]["deliveries"][0]["state"] == "done")
    assert reply < done


def test_stop_mid_reply_resumes_then_answers_the_next(gateway_factory):
    g = gateway_factory([Text("word " * 120, chunk_chars=10, delay_per_chunk=0.25),
                         Text("resumed and finished."), Text("second answer.")])
    g.start()
    g.wait_started()
    first = g.post("write a long answer")
    g.wait(lambda: any(m["streaming"] for m in g.messages()))
    second = g.post("and a second question")
    g.stop()
    assert g.state(first) == "working" and g.state(second) == "waiting"
    g.start()
    g.wait(lambda: g.state(first) == "done" and g.state(second) == "done", timeout=120)
    texts = g.agent_texts()
    assert texts.index("resumed and finished.") < texts.index("second answer.")
    assert not any("not accepting new work" in t for t in texts)


def test_no_stored_message_keeps_the_streaming_cursor(gateway_factory):
    g = gateway_factory(COMMENTARY)
    g.start()
    g.wait_started()
    first = g.post("run the two echoes")
    g.wait(lambda: g.state(first) == "done")
    assert not [m for m in g.messages() if "▉" in m["text"]]
    assert "probe hook=on_interim_message" in g.log()  # removed in Task 3 with the probe
