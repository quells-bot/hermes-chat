import asyncio

import pytest

pytest.importorskip("gateway.platforms.base", reason="needs the Hermes checkout; set HERMES_SRC")

from stubserver import Hang  # noqa: E402
from test_adapter_turn import frame, me_ok, paths, ready, wait_until  # noqa: E402

PAUSED = (409, {"error": {"code": "paused", "message": "the room is paused"}})


@pytest.fixture
def resolved(monkeypatch):
    """Hermes's two resolvers, recorded instead of run."""
    calls = []
    monkeypatch.setattr("tools.approval.resolve_gateway_approval",
                        lambda session_key, choice, **kw: calls.append(("approval", session_key, choice)) or 1)
    monkeypatch.setattr("tools.clarify_gateway.resolve_gateway_clarify",
                        lambda clarify_id, response: calls.append(("clarify", clarify_id, response)) or True)
    return calls


@pytest.fixture
async def adapter_factory(adapter_factory):
    """The shared factory, with every adapter it built disconnected at the
    end: a test that fails before its own disconnect would otherwise leave
    the stream open and the stub server waiting on it."""
    built = []

    def build(*args, **kwargs):
        adapter = adapter_factory(*args, **kwargs)
        built.append(adapter)
        return adapter

    yield build
    for adapter in built:
        if adapter._stream_task is not None and not adapter._stream_task.done():
            await adapter.cancel_background_tasks()
            await adapter.disconnect()


async def connected(adapter_factory, stub, *frames):
    me_ok(stub)
    stub.events(ready(), *frames, Hang)
    a = adapter_factory()

    async def handler(event):
        return None

    a.set_message_handler(handler)
    assert await a.connect() is True
    return a


def answered(message_id="P1", answer="session", id_=5):
    return frame("prompt.answered", {"room_id": "R1", "message_id": message_id, "answer": answer}, id_)


def prompt_posts(stub):
    return [r.json for r in stub.requests if r.path == "/agent/rooms/R1/prompts"]


async def test_an_approval_is_posted_as_a_prompt_with_hermes_choices(adapter_factory, stub):
    stub.script("POST", "/agent/rooms/R1/prompts", (201, {"id": "P1"}))
    a = await connected(adapter_factory, stub)
    result = await a.send_exec_approval("R1", "rm -rf build", "sk-1")
    assert result.success and result.message_id == "P1"
    (body,) = prompt_posts(stub)
    assert body["kind"] == "approval" and body["choices"] == ["once", "session", "always", "deny"]
    assert "rm -rf build" in body["text"]


async def test_an_approvals_answer_resolves_it_in_hermes(adapter_factory, stub, resolved):
    stub.script("POST", "/agent/rooms/R1/prompts", (201, {"id": "P1"}))
    a = await connected(adapter_factory, stub)
    await a.send_exec_approval("R1", "rm -rf build", "sk-1")
    await a._on_prompt_answered({"room_id": "R1", "message_id": "P1", "answer": "session"})
    assert resolved == [("approval", "sk-1", "session")]


async def test_a_prompt_answered_frame_reaches_the_resolver(adapter_factory, stub, resolved):
    stub.script("POST", "/agent/rooms/R1/prompts", (201, {"id": "P1"}))
    me_ok(stub)
    release = asyncio.Event()

    async def after_the_prompt(emitter):
        await release.wait()
        emitter.write(answered("P1", "deny"))
        await emitter.drain()

    stub.events(ready(), after_the_prompt, Hang)
    a = adapter_factory()
    a.set_message_handler(lambda event: None)
    assert await a.connect() is True
    await a.send_exec_approval("R1", "rm -rf build", "sk-1")
    release.set()
    await wait_until(lambda: resolved == [("approval", "sk-1", "deny")])


async def test_a_clarify_with_choices_is_posted_and_answered(adapter_factory, stub, resolved):
    stub.script("POST", "/agent/rooms/R1/prompts", (201, {"id": "P1"}))
    a = await connected(adapter_factory, stub)
    result = await a.send_clarify("R1", "Which one?", ["red", "green", "blue"], "c-1", "sk-1")
    assert result.success and result.message_id == "P1"
    assert prompt_posts(stub) == [{"kind": "clarify", "text": "Which one?", "choices": ["red", "green", "blue"]}]
    await a._on_prompt_answered({"room_id": "R1", "message_id": "P1", "answer": "blue"})
    assert resolved == [("clarify", "c-1", "blue")]


async def test_an_open_question_is_posted_with_no_choices(adapter_factory, stub):
    stub.script("POST", "/agent/rooms/R1/prompts", (201, {"id": "P1"}))
    a = await connected(adapter_factory, stub)
    await a.send_clarify("R1", "What should it be called?", None, "c-1", "sk-1")
    assert prompt_posts(stub) == [{"kind": "clarify", "text": "What should it be called?", "choices": []}]


async def test_a_clarify_with_too_many_choices_lists_them_in_the_question(adapter_factory, stub):
    stub.script("POST", "/agent/rooms/R1/prompts", (201, {"id": "P1"}))
    a = await connected(adapter_factory, stub)
    await a.send_clarify("R1", "Which one?", list("abcdefg"), "c-1", "sk-1")
    (body,) = prompt_posts(stub)
    assert body["choices"] == [] and body["text"].startswith("Which one?\n\n1. a\n") and "7. g" in body["text"]


async def test_an_answer_for_an_unknown_prompt_resolves_nothing(adapter_factory, stub, resolved):
    a = await connected(adapter_factory, stub)
    await a._on_prompt_answered({"room_id": "R1", "message_id": "P404", "answer": "once"})
    assert resolved == []


async def test_an_answer_delivered_twice_resolves_once(adapter_factory, stub, resolved):
    stub.script("POST", "/agent/rooms/R1/prompts", (201, {"id": "P1"}))
    a = await connected(adapter_factory, stub)
    await a.send_exec_approval("R1", "rm -rf build", "sk-1")
    for _ in range(2):
        await a._on_prompt_answered({"room_id": "R1", "message_id": "P1", "answer": "once"})
    assert resolved == [("approval", "sk-1", "once")]


async def test_a_resolver_that_raises_does_not_break_the_stream(adapter_factory, stub, monkeypatch):
    def boom(*args, **kwargs):
        raise RuntimeError("boom")

    monkeypatch.setattr("tools.approval.resolve_gateway_approval", boom)
    stub.script("POST", "/agent/rooms/R1/prompts", (201, {"id": "P1"}))
    a = await connected(adapter_factory, stub)
    await a.send_exec_approval("R1", "rm -rf build", "sk-1")
    await a._on_prompt_answered({"room_id": "R1", "message_id": "P1", "answer": "once"})


async def test_a_turn_that_closes_expires_the_prompts_it_left_open(adapter_factory, stub, delivery):
    me_ok(stub)
    stub.script("POST", "/agent/rooms/R1/prompts", (201, {"id": "P1"}))
    stub.script("POST", "/agent/prompts/P1/expire", (204,))
    stub.events(ready(), frame("delivery", delivery(), 1), Hang)
    a = adapter_factory()

    async def handler(event):
        await a.send_exec_approval("R1", "rm -rf build", "sk-1")
        return None

    a.set_message_handler(handler)
    assert await a.connect() is True
    await wait_until(lambda: "/agent/prompts/P1/expire" in paths(stub))
    assert "/agent/deliveries/M1/done" in paths(stub)


async def test_an_answered_prompt_is_not_expired_when_its_turn_closes(adapter_factory, stub, delivery, resolved):
    me_ok(stub)
    stub.script("POST", "/agent/rooms/R1/prompts", (201, {"id": "P1"}))
    stub.events(ready(), frame("delivery", delivery(), 1), Hang)
    a = adapter_factory()

    async def handler(event):
        await a.send_exec_approval("R1", "rm -rf build", "sk-1")
        await a._on_prompt_answered({"room_id": "R1", "message_id": "P1", "answer": "once"})
        return None

    a.set_message_handler(handler)
    assert await a.connect() is True
    await wait_until(lambda: "/agent/deliveries/M1/done" in paths(stub))
    await asyncio.sleep(0.2)
    assert "/agent/prompts/P1/expire" not in paths(stub)


async def test_hermes_giving_up_on_a_clarify_expires_its_prompt(adapter_factory, stub):
    stub.script("POST", "/agent/rooms/R1/prompts", (201, {"id": "P1"}))
    stub.script("POST", "/agent/prompts/P1/expire", (204,))
    a = await connected(adapter_factory, stub)
    await a.send_clarify("R1", "Which one?", ["red", "blue"], "c-1", "sk-1")
    await a.retire_clarify_card("c-1", "timed out")
    assert "/agent/prompts/P1/expire" in paths(stub)
    await a.retire_clarify_card("c-unknown", "timed out")


async def test_a_prompt_refused_by_a_paused_room_is_a_failed_send(adapter_factory, stub):
    stub.script("POST", "/agent/rooms/R1/prompts", PAUSED, PAUSED)
    a = await connected(adapter_factory, stub)
    assert (await a.send_exec_approval("R1", "rm -rf build", "sk-1")).success is False
    assert (await a.send_clarify("R1", "Which one?", ["red"], "c-1", "sk-1")).success is False


async def test_no_prompt_text_or_answer_in_the_plugins_logs(adapter_factory, stub, resolved, caplog):
    stub.script("POST", "/agent/rooms/R1/prompts", (201, {"id": "P1"}), (201, {"id": "P2"}))
    a = await connected(adapter_factory, stub)
    caplog.set_level("DEBUG", logger="hermes_chat")
    await a.send_exec_approval("R1", "rm -rf secret-build-dir", "sk-1")
    await a.send_clarify("R1", "Which colour of secret?", ["crimson"], "c-1", "sk-1")
    await a._on_prompt_answered({"room_id": "R1", "message_id": "P2", "answer": "crimson"})
    ours = "\n".join(r.getMessage() for r in caplog.records if r.name.startswith("hermes_chat"))
    assert "P1" in ours and "P2" in ours
    assert "secret" not in ours and "crimson" not in ours


@pytest.fixture
def confirmed(monkeypatch):
    """Hermes's slash-confirm resolver, recorded; it answers with a line to show."""
    calls = []

    async def resolve(session_key, confirm_id, choice, **kwargs):
        calls.append((session_key, confirm_id, choice))
        return "✨ Session reset!"

    monkeypatch.setattr("tools.slash_confirm.resolve", resolve)
    return calls


async def ask_to_confirm(a, confirm_id="cid"):
    return await a.send_slash_confirm("R1", "Confirm /new", "This starts a fresh session.", "sk", confirm_id)


async def test_a_commands_confirmation_is_posted_as_a_prompt(adapter_factory, stub):
    stub.script("POST", "/agent/rooms/R1/prompts", (201, {"id": "P1"}))
    a = await connected(adapter_factory, stub)
    result = await ask_to_confirm(a)
    assert result.success and result.message_id == "P1"
    (body,) = prompt_posts(stub)
    assert body["kind"] == "approval" and body["choices"] == ["once", "always", "deny"]
    assert body["text"] == "**Confirm /new**\n\nThis starts a fresh session."


@pytest.mark.parametrize("answer,choice", [("once", "once"), ("always", "always"), ("deny", "cancel")])
async def test_a_confirmations_answer_runs_the_command_and_shows_its_result(
        adapter_factory, stub, confirmed, answer, choice):
    stub.script("POST", "/agent/rooms/R1/prompts", (201, {"id": "P1"}))
    stub.script("POST", "/agent/rooms/R1/messages", (201, {"id": "M9"}))
    a = await connected(adapter_factory, stub)
    await ask_to_confirm(a)
    await a._on_prompt_answered({"room_id": "R1", "message_id": "P1", "answer": answer})
    assert confirmed == [("sk", "cid", choice)]
    shown = [r.json for r in stub.requests if r.path == "/agent/rooms/R1/messages"]
    assert shown == [{"text": "✨ Session reset!"}]


async def test_a_confirmation_with_nothing_to_show_posts_nothing(adapter_factory, stub, monkeypatch):
    async def resolve(session_key, confirm_id, choice, **kwargs):
        return None

    monkeypatch.setattr("tools.slash_confirm.resolve", resolve)
    stub.script("POST", "/agent/rooms/R1/prompts", (201, {"id": "P1"}))
    a = await connected(adapter_factory, stub)
    await ask_to_confirm(a)
    await a._on_prompt_answered({"room_id": "R1", "message_id": "P1", "answer": "deny"})
    assert not [r for r in stub.requests if r.path == "/agent/rooms/R1/messages"]


async def test_a_second_confirmation_in_a_room_expires_the_first(adapter_factory, stub):
    stub.script("POST", "/agent/rooms/R1/prompts", (201, {"id": "P1"}), (201, {"id": "P2"}))
    stub.script("POST", "/agent/prompts/P1/expire", (204,))
    a = await connected(adapter_factory, stub)
    await ask_to_confirm(a, "cid-1")
    await ask_to_confirm(a, "cid-2")
    assert paths(stub).index("/agent/prompts/P1/expire") < [
        i for i, p in enumerate(paths(stub)) if p == "/agent/rooms/R1/prompts"][-1]


async def test_a_turn_closing_leaves_a_confirmation_open(adapter_factory, stub, delivery):
    me_ok(stub)
    stub.script("POST", "/agent/rooms/R1/prompts", (201, {"id": "P1"}))
    stub.events(ready(), frame("delivery", delivery(), 1), Hang)
    a = adapter_factory()

    async def handler(event):
        await ask_to_confirm(a)
        return None

    a.set_message_handler(handler)
    assert await a.connect() is True
    await wait_until(lambda: "/agent/deliveries/M1/done" in paths(stub))
    await asyncio.sleep(0.2)
    assert "/agent/prompts/P1/expire" not in paths(stub)


async def test_hermes_own_list_of_choices_is_left_out_of_the_prompt(adapter_factory, stub):
    """Hermes writes the three choices and a typed fallback into the message
    for platforms without buttons. The prompt has the choices already."""
    stub.script("POST", "/agent/rooms/R1/prompts", (201, {"id": "P1"}))
    a = await connected(adapter_factory, stub)
    message = ("⚠️ **Confirm /new**\n\nThis starts a fresh session.\n\nChoose:\n• **Approve Once** — proceed\n"
               "• **Cancel** — keep it\n\n_Text fallback: reply `/approve`, `/always`, or `/cancel`._")
    await a.send_slash_confirm("R1", "/new", message, "sk", "cid")
    (body,) = prompt_posts(stub)
    assert body["text"] == "⚠️ **Confirm /new**\n\nThis starts a fresh session."


BAD_REQUEST = (400, {"error": {"code": "validation", "message": "a choice is not acceptable"}})


@pytest.fixture
def approvals(monkeypatch):
    """Hermes's approval queue for session sk-1, holding one request."""
    calls = []
    monkeypatch.setattr("tools.approval.list_gateway_approvals",
                        lambda session_key: [{"request_id": "rq-0"}, {"request_id": "rq-1"}])
    monkeypatch.setattr("tools.approval.resolve_gateway_approval",
                        lambda session_key, choice, **kw: calls.append(("resolve", session_key, choice, kw)) or 1)
    monkeypatch.setattr("tools.approval.withdraw_gateway_approval",
                        lambda session_key, request_id, cause: calls.append(("withdraw", session_key, request_id)) or True)
    return calls


async def test_an_answer_resolves_the_approval_it_was_asked_for(adapter_factory, stub, approvals):
    """Two approvals can be open in a room at once; Hermes resolves the oldest
    unless told which one."""
    stub.script("POST", "/agent/rooms/R1/prompts", (201, {"id": "P1"}))
    a = await connected(adapter_factory, stub)
    await a.send_exec_approval("R1", "rm -rf build", "sk-1")
    await a._on_prompt_answered({"room_id": "R1", "message_id": "P1", "answer": "once"})
    assert approvals == [("resolve", "sk-1", "once", {"request_id": "rq-1"})]


@pytest.mark.parametrize("refusal", [PAUSED, BAD_REQUEST, (503, {"error": {"code": "unavailable", "message": "x"}})])
async def test_an_approval_that_cannot_be_asked_is_withdrawn(adapter_factory, stub, approvals, refusal):
    """Nobody can answer it, and with the long timeout the turn would wait on
    it for a year."""
    stub.script("POST", "/agent/rooms/R1/prompts", refusal)
    a = await connected(adapter_factory, stub)
    result = await a.send_exec_approval("R1", "rm -rf build", "sk-1")
    assert result.success is False
    assert approvals == [("withdraw", "sk-1", "rq-1")]


async def test_hermes_typed_fallback_for_a_question_is_not_posted(adapter_factory, stub):
    """When a prompt cannot be posted Hermes falls back to a message asking
    for a typed reply. In a room that reply waits behind the turn that is
    asking, so the message would be a question nobody can answer."""
    a = await connected(adapter_factory, stub)
    result = await a.send("R1", "Reply with the number of your choice", metadata={"is_approval_prompt": True})
    assert result.success is False
    assert not [r for r in stub.requests if r.path == "/agent/rooms/R1/messages"]


async def test_a_commands_typed_fallback_is_posted(adapter_factory, stub):
    """No turn is waiting on a command's confirmation, so a typed reply works."""
    stub.script("POST", "/agent/rooms/R1/messages", (201, {"id": "M9"}))
    a = await connected(adapter_factory, stub)
    result = await a.send("R1", "Confirm /new: reply /approve", metadata={
        "is_approval_prompt": True, "force_proactive_send": True})
    assert result.success is True
