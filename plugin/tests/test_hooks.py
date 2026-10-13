import inspect

import pytest

pytest.importorskip("gateway.platforms.base", reason="needs the Hermes checkout; set HERMES_SRC")

from hermes_chat import hooks  # noqa: E402


class FakeAdapter:
    def __init__(self, owns=True, boom=False):
        self.started, self.finished = [], []
        self._owns, self._boom = owns, boom

    def tool_started(self, room_id, tool_call_id, tool_name, args):
        if self._boom:
            raise RuntimeError("boom")
        self.started.append((room_id, tool_call_id, tool_name, args))
        return self._owns

    def tool_finished(self, tool_call_id, duration_ms, status):
        if self._boom:
            raise RuntimeError("boom")
        self.finished.append((tool_call_id, duration_ms, status))


def registered():
    callbacks = {}

    class Ctx:
        def register_hook(self, hook_name, callback):
            callbacks[hook_name] = callback

    hooks.register_hooks(Ctx())
    return callbacks


def session(monkeypatch, platform, chat_id="R1"):
    values = {"HERMES_SESSION_PLATFORM": platform, "HERMES_SESSION_CHAT_ID": chat_id}
    monkeypatch.setattr(hooks, "get_session_env", lambda name, default="": values.get(name, default))


@pytest.fixture
def attached():
    adapters = []

    def attach(adapter):
        hooks.attach(adapter)
        adapters.append(adapter)
        return adapter

    yield attach
    for adapter in adapters:
        hooks.detach(adapter)


def test_the_two_tool_hooks_are_registered_as_plain_functions():
    callbacks = registered()
    assert sorted(callbacks) == ["post_tool_call", "pre_tool_call"]
    assert not [name for name, cb in callbacks.items() if inspect.iscoroutinefunction(cb)]


def test_a_tool_on_another_platform_reaches_no_adapter(monkeypatch, attached):
    adapter = attached(FakeAdapter())
    session(monkeypatch, "telegram")
    callbacks = registered()
    callbacks["pre_tool_call"](tool_name="terminal", tool_call_id="c1", args={"command": "x"})
    callbacks["post_tool_call"](tool_name="terminal", tool_call_id="c1", duration_ms=3, status="ok")
    assert adapter.started == [] and adapter.finished == []


def test_a_tool_in_a_room_is_handed_to_the_adapter(monkeypatch, attached):
    adapter = attached(FakeAdapter())
    session(monkeypatch, "hermes_chat")
    callbacks = registered()
    assert callbacks["pre_tool_call"](tool_name="terminal", tool_call_id="c1", args={"command": "x"}) is None
    callbacks["post_tool_call"](tool_name="terminal", tool_call_id="c1", duration_ms=3, status="ok")
    assert adapter.started == [("R1", "c1", "terminal", {"command": "x"})]
    assert adapter.finished == [("c1", 3, "ok")]


def test_the_start_stops_at_the_adapter_that_owns_the_room(monkeypatch, attached):
    first, second = attached(FakeAdapter(owns=True)), attached(FakeAdapter(owns=True))
    session(monkeypatch, "hermes_chat")
    registered()["pre_tool_call"](tool_name="terminal", tool_call_id="c1", args={})
    assert len(first.started) + len(second.started) == 1


def test_a_raising_adapter_does_not_raise_into_hermes(monkeypatch, attached):
    attached(FakeAdapter(boom=True))
    session(monkeypatch, "hermes_chat")
    callbacks = registered()
    callbacks["pre_tool_call"](tool_name="terminal", tool_call_id="c1", args={})
    callbacks["post_tool_call"](tool_name="terminal", tool_call_id="c1", duration_ms=3, status="ok")


def test_a_hook_call_missing_its_fields_does_nothing(monkeypatch, attached):
    adapter = attached(FakeAdapter())
    session(monkeypatch, "hermes_chat")
    registered()["pre_tool_call"]()
    assert adapter.started == []
