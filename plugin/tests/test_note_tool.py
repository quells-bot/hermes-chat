import asyncio
import inspect

import pytest

pytest.importorskip("gateway.platforms.base", reason="needs the Hermes checkout; set HERMES_SRC")

from hermes_chat import hooks, note_tool  # noqa: E402


class FakeAdapter:
    def __init__(self, loop, answer="Suggested.", boom=False, owns=True):
        self._loop = loop
        self.asked = []
        self._answer, self._boom, self._owns = answer, boom, owns

    def has_open_turn(self, room_id, session_key=None):
        return self._owns

    async def suggest_note_entry(self, room_id, text):
        if self._boom:
            raise RuntimeError("boom")
        self.asked.append((room_id, text))
        return self._answer


def registered():
    tools = {}

    class Ctx:
        def register_tool(self, **kwargs):
            tools[kwargs["name"]] = kwargs

    note_tool.register_note_tool(Ctx())
    return tools["suggest_note_entry"]


def session(monkeypatch, platform="hermes_chat", chat_id="R1"):
    values = {"HERMES_SESSION_PLATFORM": platform, "HERMES_SESSION_CHAT_ID": chat_id}
    monkeypatch.setattr(hooks, "get_session_env", lambda name, default="": values.get(name, default))


@pytest.fixture
def attach():
    attached = []

    def add(adapter):
        hooks.attach(adapter)
        attached.append(adapter)
        return adapter

    yield add
    for adapter in attached:
        hooks.detach(adapter)


def test_the_tool_is_registered_with_its_schema():
    tool = registered()
    assert tool["toolset"] == "hermes_chat"
    assert tool["schema"]["name"] == "suggest_note_entry"
    params = tool["schema"]["parameters"]
    assert params["required"] == ["text"] and params["properties"]["text"]["maxLength"] == 300
    assert "300" in tool["schema"]["description"]
    assert not inspect.iscoroutinefunction(tool["handler"])


def test_the_tool_has_no_availability_check():
    """Hermes runs a check_fn outside any session, where the platform is not
    known; one that asked for it kept the tool from every turn."""
    assert registered().get("check_fn") is None


@pytest.mark.parametrize("text", ["", "   ", "x" * 301])
async def test_text_the_server_would_refuse_is_refused_without_asking(monkeypatch, attach, text):
    adapter = attach(FakeAdapter(asyncio.get_running_loop()))
    session(monkeypatch)
    answer = await asyncio.to_thread(registered()["handler"], {"text": text})
    assert adapter.asked == [] and "300" in answer


async def test_a_suggestion_from_the_tools_thread_reaches_the_adapter(monkeypatch, attach):
    adapter = attach(FakeAdapter(asyncio.get_running_loop()))
    session(monkeypatch)
    answer = await asyncio.to_thread(registered()["handler"], {"text": "  Prefers short answers  "})
    assert answer == "Suggested."
    assert adapter.asked == [("R1", "Prefers short answers")]


async def test_the_adapter_with_the_rooms_turn_is_the_one_asked(monkeypatch, attach):
    other = attach(FakeAdapter(asyncio.get_running_loop(), owns=False))
    owner = attach(FakeAdapter(asyncio.get_running_loop(), owns=True))
    session(monkeypatch)
    await asyncio.to_thread(registered()["handler"], {"text": "Prefers short answers"})
    assert other.asked == [] and len(owner.asked) == 1


async def test_with_no_turn_open_anywhere_the_tool_says_so(monkeypatch, attach):
    attach(FakeAdapter(asyncio.get_running_loop(), owns=False))
    session(monkeypatch)
    answer = await asyncio.to_thread(registered()["handler"], {"text": "Prefers short answers"})
    assert answer == "There is no open turn in this room to suggest an entry from."


async def test_an_adapter_that_raises_gives_a_plain_answer(monkeypatch, attach, caplog):
    attach(FakeAdapter(asyncio.get_running_loop(), boom=True))
    session(monkeypatch)
    caplog.set_level("DEBUG", logger="hermes_chat")
    answer = await asyncio.to_thread(registered()["handler"], {"text": "Prefers secret answers"})
    assert answer == "The suggestion could not be sent."
    assert "secret" not in caplog.text


async def test_outside_a_hermes_chat_session_the_tool_says_so(monkeypatch, attach):
    adapter = attach(FakeAdapter(asyncio.get_running_loop()))
    session(monkeypatch, platform="telegram")
    answer = await asyncio.to_thread(registered()["handler"], {"text": "Prefers short answers"})
    assert adapter.asked == [] and "Hermes Chat" in answer
