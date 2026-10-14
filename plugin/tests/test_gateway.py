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
    assert not [m for m in g.messages() if m["text"] != m["text"].lstrip()]
    assert "hermes_chat.probe" not in g.log()


def test_tool_steps_are_recorded_against_the_turn(gateway_factory):
    g = gateway_factory(COMMENTARY)
    g.start()
    g.wait_started()
    first = g.post("run the two echoes")
    g.wait(lambda: g.state(first) == "done")
    acts = g.chat.human.get(f"/api/rooms/{g.room_id}/messages/{first}/activity").json()["activities"]
    assert [a["tool"] for a in acts] == ["terminal", "terminal"]
    assert all(a["succeeded"] is True and a["duration_ms"] >= 0 for a in acts)
    assert "echo one" in acts[0]["preview"]
    assert "echo one" not in g.log_lines("hermes_chat")


LONG = "word " * 200


def test_stop_ends_the_running_turn_and_the_room_moves_on(gateway_factory):
    g = gateway_factory([Text(LONG, chunk_chars=10, delay_per_chunk=0.25), Text("next answer.")])
    g.start()
    g.wait_started()
    first = g.post("write a long answer")
    g.wait(lambda: any(m["streaming"] for m in g.messages()))
    r = g.chat.human.post(f"/api/rooms/{g.room_id}/agents/{g.chat.agent_id}/stop")
    assert r.status_code < 300, r.text
    g.wait(lambda: g.state(first) == "done", timeout=20)
    reply = next(m for m in g.messages() if m["text"].startswith("word"))
    assert reply["streaming"] is False and len(reply["text"]) < len(LONG)
    second = g.post("another question")
    g.wait(lambda: g.state(second) == "done")
    assert "next answer." in g.agent_texts()


def test_pause_ends_the_running_turn(gateway_factory):
    g = gateway_factory([Text(LONG, chunk_chars=10, delay_per_chunk=0.25)])
    g.start()
    g.wait_started()
    first = g.post("write a long answer")
    g.wait(lambda: any(m["streaming"] for m in g.messages()))
    g.chat.pause(g.room_id)
    g.wait(lambda: g.state(first) == "done", timeout=20)
    reply = next(m for m in g.messages() if m["text"].startswith("word"))
    assert len(reply["text"]) < len(LONG)


# Hermes holds a recursive delete for approval. The target does not exist.
FLAGGED_COMMAND = "rm -rf ./hermes-chat-gateway-test-nothing-here"


def test_an_approval_is_asked_as_a_prompt_and_its_answer_continues_the_turn(gateway_factory):
    g = gateway_factory([ToolCall("terminal", {"command": FLAGGED_COMMAND}), Text("finished.")])
    g.start()
    g.wait_started()
    first = g.post("do it")
    g.wait(lambda: any(m["prompt"] for m in g.messages()))
    prompt = next(m for m in g.messages() if m["prompt"])
    assert prompt["prompt"]["kind"] == "approval" and "deny" in prompt["prompt"]["choices"]
    assert g.state(first) == "working"
    r = g.chat.human.post(f"/api/rooms/{g.room_id}/messages/{prompt['id']}/answer", json={"answer": "deny"})
    assert r.status_code < 300, r.text
    g.wait(lambda: g.state(first) == "done")
    assert "finished." in g.agent_texts()
    assert "BLOCKED" in str(g.llm.main_requests()[1]["messages"][-1]["content"]).upper() \
        or "denied" in str(g.llm.main_requests()[1]["messages"][-1]["content"]).lower()


def test_a_clarifying_question_is_asked_as_a_prompt_and_answered(gateway_factory):
    g = gateway_factory([
        ToolCall("clarify", {"questions": [{"question": "Which one?", "choices": ["red", "blue"]}]}),
        Text("blue it is."),
    ])
    g.start()
    g.wait_started()
    first = g.post("pick a colour")
    g.wait(lambda: any(m["prompt"] for m in g.messages()))
    prompt = next(m for m in g.messages() if m["prompt"])
    choices = prompt["prompt"]["choices"]  # Hermes marks the first as its recommendation
    assert prompt["prompt"]["kind"] == "clarify" and len(choices) == 2
    assert choices[0].startswith("red") and choices[1] == "blue"
    assert prompt["text"].startswith("Which one?")  # Hermes adds how to skip
    r = g.chat.human.post(f"/api/rooms/{g.room_id}/messages/{prompt['id']}/answer", json={"answer": "blue"})
    assert r.status_code < 300, r.text
    g.wait(lambda: g.state(first) == "done")
    assert "blue" in str(g.llm.main_requests()[1]["messages"][-1]["content"])
    assert "blue it is." in g.agent_texts()


def test_new_is_confirmed_from_a_prompt_and_starts_the_conversation_over(gateway_factory):
    g = gateway_factory([Text("one."), Text("two.")])
    g.start()
    g.wait_started()
    first = g.post("first question")
    g.wait(lambda: g.state(first) == "done")
    g.post("/new")
    g.wait(lambda: any(m["prompt"] for m in g.messages()))
    prompt = next(m for m in g.messages() if m["prompt"])
    assert "Confirm /new" in prompt["text"] and prompt["prompt"]["choices"] == ["once", "always", "deny"]
    assert not [t for t in g.agent_texts() if "Text fallback" in t]
    r = g.chat.human.post(f"/api/rooms/{g.room_id}/messages/{prompt['id']}/answer", json={"answer": "once"})
    assert r.status_code < 300, r.text
    g.wait(lambda: any("Session reset" in t for t in g.agent_texts()))
    second = g.post("second question")
    g.wait(lambda: g.state(second) == "done")
    requests = g.llm.main_requests()
    assert len(requests[-1]["messages"]) == len(requests[0]["messages"])
