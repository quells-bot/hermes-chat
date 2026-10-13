import pytest

pytest.importorskip("gateway.platforms.base", reason="needs the Hermes checkout; set HERMES_SRC")

from hermes_chat.adapter import register  # noqa: E402
from hermes_chat.probe import describe, register_probe  # noqa: E402


def test_describe_lists_names_and_ids_only():
    line = describe(
        "pre_tool_call",
        {"tool_name": "terminal", "args": {"command": "cat ~/.ssh/id_rsa"}, "session_id": "S1",
         "turn_id": "T1", "tool_call_id": "C1", "task_id": ""},
        {"platform": "hermes_chat", "chat_id": "R1", "session_key": "k"})
    assert "tool=terminal" in line
    assert "session_id=S1" in line
    assert "turn_id=T1" in line
    assert "tool_call_id=C1" in line
    assert "chat_id=R1" in line
    assert "session_key=k" in line
    assert "keys=args,session_id,task_id,tool_call_id,tool_name,turn_id" in line
    assert "id_rsa" not in line
    assert "cat" not in line


def test_describe_interim_message_has_no_text():
    line = describe("on_interim_message", {"text": "top secret", "already_streamed": False}, {})
    assert "top secret" not in line
    assert "tool=-" in line
    assert "chat_id=-" in line


def test_describe_post_tool_call_includes_duration_and_status_not_result():
    line = describe("post_tool_call", {"tool_name": "t", "result": "PRIVATE", "duration_ms": 12,
                                       "status": "ok", "error_message": "PRIVATE2"}, {})
    assert "duration_ms=12" in line
    assert "status=ok" in line
    assert "PRIVATE" not in line
    assert "PRIVATE2" not in line


def test_register_probe_registers_three_hooks():
    names = []

    class Ctx:
        def register_hook(self, hook_name, callback):
            names.append(hook_name)

    register_probe(Ctx())
    assert names == ["pre_tool_call", "post_tool_call", "on_interim_message"]


def test_callback_never_raises(monkeypatch):
    def boom(*args, **kwargs):
        raise RuntimeError("boom")

    monkeypatch.setattr("hermes_chat.probe.describe", boom)
    callbacks = {}

    class Ctx:
        def register_hook(self, hook_name, callback):
            callbacks[hook_name] = callback

    register_probe(Ctx())
    result = callbacks["pre_tool_call"](tool_name="x", args={"secret": 1})
    assert result is None


def test_callbacks_are_plain_functions():
    """Hermes runs a stream hook without awaiting it; a coroutine would be dropped."""
    import inspect

    callbacks = {}

    class Ctx:
        def register_hook(self, hook_name, callback):
            callbacks[hook_name] = callback

    register_probe(Ctx())
    assert not [name for name, cb in callbacks.items() if inspect.iscoroutinefunction(cb)]


def test_adapter_register_also_registers_the_probe():
    recorded = {"platform": None, "hooks": []}

    class Ctx:
        def register_platform(self, **kwargs):
            recorded["platform"] = kwargs

        def register_hook(self, hook_name, callback):
            recorded["hooks"].append(hook_name)

    register(Ctx())
    assert recorded["hooks"] == ["pre_tool_call", "post_tool_call", "on_interim_message"]