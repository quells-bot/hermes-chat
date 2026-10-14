"""Hermes's tool hooks, handed to the adapter that has the room's turn open.

Hermes calls a hook from whichever thread runs the tool, so a callback is a
plain function, does no I/O of its own, and never raises into Hermes.
"""

from __future__ import annotations

import logging
import weakref

from gateway.session_context import get_session_env

logger = logging.getLogger("hermes_chat.hooks")

PLATFORM = "hermes_chat"
_adapters: "weakref.WeakSet" = weakref.WeakSet()


def attach(adapter) -> None:
    _adapters.add(adapter)


def detach(adapter) -> None:
    _adapters.discard(adapter)


def adapters() -> list:
    return list(_adapters)


def session_room() -> str | None:
    """The room of the session this thread is working for, when it is ours."""
    if get_session_env("HERMES_SESSION_PLATFORM", "") != PLATFORM:
        return None
    return get_session_env("HERMES_SESSION_CHAT_ID", "") or None


def session_key() -> str | None:
    return get_session_env("HERMES_SESSION_KEY", "") or None


def _pre_tool_call(**kwargs) -> None:
    try:
        room_id = session_room()
        tool_call_id = kwargs.get("tool_call_id")
        if not room_id or not tool_call_id:
            return
        for adapter in adapters():
            if adapter.tool_started(room_id, tool_call_id, kwargs.get("tool_name"), kwargs.get("args"),
                                    session_key=session_key()):
                return
    except Exception:
        logger.debug("pre_tool_call failed", exc_info=True)


def _post_tool_call(**kwargs) -> None:
    try:
        tool_call_id = kwargs.get("tool_call_id")
        if not session_room() or not tool_call_id:
            return
        for adapter in adapters():
            adapter.tool_finished(tool_call_id, kwargs.get("duration_ms"), kwargs.get("status"))
    except Exception:
        logger.debug("post_tool_call failed", exc_info=True)


def register_hooks(ctx) -> None:
    ctx.register_hook("pre_tool_call", _pre_tool_call)
    ctx.register_hook("post_tool_call", _post_tool_call)
