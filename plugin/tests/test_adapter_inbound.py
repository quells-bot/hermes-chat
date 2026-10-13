import asyncio
import json

import pytest

pytest.importorskip("gateway.platforms.base", reason="needs the Hermes checkout; set HERMES_SRC")

from stubserver import Hang  # noqa: E402

from hermes_chat.adapter import (  # noqa: E402
    _env_enablement,
    register,
)


async def wait_until(predicate, timeout=3.0):
    deadline = asyncio.get_event_loop().time() + timeout
    while asyncio.get_event_loop().time() < deadline:
        if predicate():
            return
        await asyncio.sleep(0.01)
    raise AssertionError("condition not met in time")


def ready(cursor=0):
    return f"event: ready\ndata: {{\"cursor\":{cursor}}}\n\n".encode()


def frame(event, data, id_=None):
    prefix = f"id: {id_}\n" if id_ is not None else ""
    return (prefix + f"event: {event}\ndata: {json.dumps(data)}\n\n").encode()


def me_ok(stub):
    stub.script("GET", "/agent/me", (200, {"id": "A1", "handle": "h", "display_name": "H"}))


def test_dm_delivery_maps_to_event(adapter_factory, delivery):
    ev = adapter_factory().event_from_delivery(
        delivery(flavor="dm", text="hello", note="likes brevity"))
    assert (ev.text, ev.message_id, ev.channel_prompt) == ("hello", "M1", "likes brevity")
    assert (ev.source.chat_id, ev.source.chat_type, ev.source.thread_id) == ("R1", "dm", None)
    assert ev.allow_gateway_control is True


def test_channel_delivery_uses_room_as_thread(adapter_factory, delivery):
    ev = adapter_factory().event_from_delivery(delivery(flavor="channel"))
    assert (ev.source.chat_type, ev.source.thread_id) == ("group", "R1")


def test_two_authors_in_one_channel_share_a_session(adapter_factory, delivery, human):
    a = adapter_factory()
    k1 = a._event_session_key(a.event_from_delivery(delivery(flavor="channel", author=human("H1"))))
    k2 = a._event_session_key(a.event_from_delivery(delivery(flavor="channel", message_id="M2", author=human("H2"))))
    assert k1 == k2


def test_two_rooms_have_different_sessions(adapter_factory, delivery, human):
    a = adapter_factory()
    k1 = a._event_session_key(a.event_from_delivery(delivery(author=human("H1"))))
    k2 = a._event_session_key(a.event_from_delivery(delivery(room_id="R2", message_id="M2", author=human("H1"))))
    assert k1 != k2


def test_agent_author_cannot_run_commands(adapter_factory, delivery, agent):
    ev = adapter_factory().event_from_delivery(delivery(text="/new", author=agent("A2")))
    assert ev.allow_gateway_control is False
    assert ev.source.is_bot is True


def test_null_author_gets_placeholder(adapter_factory, delivery):
    ev = adapter_factory().event_from_delivery(delivery(author=None))
    assert (ev.source.user_name, ev.source.user_id, ev.allow_gateway_control) == ("removed user", None, False)


def test_reply_fields_pass_through(adapter_factory, delivery):
    ev = adapter_factory().event_from_delivery(delivery(reply={
        "reply_to_message_id": "M0", "reply_to_text": "why", "reply_to_author_name": "Ana",
    }))
    assert (ev.reply_to_message_id, ev.reply_to_text, ev.reply_to_author_name) == ("M0", "why", "Ana")


def test_null_note_gives_no_channel_prompt(adapter_factory, delivery):
    assert adapter_factory().event_from_delivery(delivery(note=None)).channel_prompt is None


def test_context_is_formatted_oldest_first(adapter_factory, delivery):
    ev = adapter_factory().event_from_delivery(delivery(context=[("Ana", "one"), ("bot", "two")]))
    assert ev.channel_context == "Ana: one\nbot: two"
    assert adapter_factory().event_from_delivery(delivery(context=())).channel_context is None


async def test_connect_succeeds_while_server_is_down(adapter_factory):
    a = adapter_factory(url="http://127.0.0.1:1")
    assert await a.connect() is True
    await a.disconnect()


async def test_connect_fails_on_bad_token_without_retry(adapter_factory, stub):
    stub.script("GET", "/agent/me", (401, {"error": {"code": "unauthenticated", "message": "x"}}))
    a = adapter_factory()
    assert await a.connect() is False
    assert a._fatal_error_retryable is False
    await a.disconnect()


async def test_connect_fails_when_token_lock_is_held(adapter_factory, monkeypatch):
    import gateway.status as status_mod
    monkeypatch.setattr(status_mod, "acquire_scoped_lock", lambda *a, **k: (False, {}))
    a = adapter_factory()
    assert await a.connect() is False
    await a.disconnect()


async def test_delivery_reaches_the_message_handler_once(adapter_factory, stub, delivery):
    me_ok(stub)
    d1 = delivery()
    d2 = delivery()  # the same message again: at-least-once delivery
    stub.events(ready(), frame("delivery", d1, 1), frame("delivery", d2, 2), Hang)
    seen = []
    a = adapter_factory()

    async def handler(event):
        seen.append(event)
        # The open turn is in the record while it is being worked.
        assert a._open_turns.all() == {"M1": "R1"}

    a.set_message_handler(handler)
    assert await a.connect() is True
    await wait_until(lambda: len(seen) == 1)
    assert seen[0].message_id == "M1"
    await wait_until(lambda: a._open_turns.all() == {})
    await a.disconnect()


async def test_revoked_token_marks_the_adapter_fatal(adapter_factory, stub):
    me_ok(stub)
    stub.script("GET", "/agent/events", (401, {"error": {"code": "unauthenticated", "message": "revoked"}}))
    a = adapter_factory()
    assert await a.connect() is True
    await wait_until(lambda: a.has_fatal_error)
    assert a.fatal_error_retryable is False
    await a.disconnect()


async def test_connect_warns_about_display_settings(adapter_factory, stub, monkeypatch, caplog):
    me_ok(stub)
    stub.events(Hang)
    import logging
    import hermes_cli.config as config_mod
    monkeypatch.setattr(config_mod, "load_config", lambda: {})
    a = adapter_factory()
    assert await a.connect() is True
    await a.disconnect()
    warned = [r.message for r in caplog.records if r.levelno >= logging.WARNING]
    assert any("tool_progress" in m for m in warned)
    assert any("interim_assistant_messages" in m for m in warned)
    assert any("streaming.enabled" in m for m in warned)


def test_register_passes_the_spec_values():
    recorded = {}

    class Ctx:
        def register_platform(self, **kwargs):
            recorded.update(kwargs)

        def register_hook(self, hook_name, callback):
            recorded.setdefault("hooks", []).append(hook_name)

    register(Ctx())
    assert recorded["name"] == "hermes_chat"
    assert recorded["label"] == "Hermes Chat"
    assert recorded["max_message_length"] == 0
    assert recorded["cron_deliver_env_var"] == "HERMES_CHAT_HOME_ROOM"
    assert recorded["allowed_users_env"] == "HERMES_CHAT_ALLOWED_USERS"
    assert recorded["allow_all_env"] == "HERMES_CHAT_ALLOW_ALL_USERS"
    assert recorded["required_env"] == ["HERMES_CHAT_URL", "HERMES_CHAT_TOKEN"]


def test_env_enablement_needs_both(monkeypatch):
    values = {}
    from hermes_chat import adapter as adapter_mod
    monkeypatch.setattr(adapter_mod, "get_scoped_secret", lambda name, default=None: values.get(name, default))
    assert _env_enablement() is None
    values["HERMES_CHAT_URL"] = "https://chat.example.com"
    assert _env_enablement() is None
    values["HERMES_CHAT_TOKEN"] = "hca_test_token"
    out = _env_enablement()
    assert out["url"] == "https://chat.example.com"
    assert out["token"] == "hca_test_token"


def test_message_length_is_counted_in_bytes(adapter_factory):
    assert adapter_factory().message_len_fn("é") == 2