"""The stage 1 hook probe: logs what Hermes's tool and remark hooks deliver,
without sending any of it anywhere.

Names and identifiers only: never tool arguments, message text, hook argument
values, or results. Removed when stage 2 replaces it.
"""

from __future__ import annotations

import logging

from gateway.session_context import get_session_env

logger = logging.getLogger("hermes_chat.probe")

_IDENTIFIER_FIELDS = ("session_id", "turn_id", "tool_call_id", "task_id")
_SESSION_FIELDS = ("platform", "chat_id", "session_key")
_SESSION_ENV = {
    "platform": "HERMES_SESSION_PLATFORM",
    "chat_id": "HERMES_SESSION_CHAT_ID",
    "session_key": "HERMES_SESSION_KEY",
}


def describe(hook: str, kwargs: dict, session: dict) -> str:
    """Build the one log line a hook writes."""
    parts = ["probe", f"hook={hook}"]
    tool = kwargs.get("tool_name") or "-"
    parts.append(f"tool={tool}")
    parts.append("keys=" + ",".join(sorted(kwargs)))
    for name in _IDENTIFIER_FIELDS:
        parts.append(f"{name}={kwargs.get(name) or '-'}")
    for name in _SESSION_FIELDS:
        parts.append(f"{name}={session.get(name) or '-'}")
    if hook == "post_tool_call":
        parts.append(f"duration_ms={kwargs.get('duration_ms') or '-'}")
        parts.append(f"status={kwargs.get('status') or '-'}")
    return " ".join(parts)


def _make_callback(hook_name: str):
    async def callback(**kwargs):
        session = {name: get_session_env(env) for name, env in _SESSION_ENV.items()}
        try:
            logger.info("%s", describe(hook_name, kwargs, session))
        except Exception:
            logger.debug("probe %s failed", hook_name, exc_info=True)
        return None

    return callback


def register_probe(ctx) -> None:
    for hook_name in ("pre_tool_call", "post_tool_call", "on_interim_message"):
        ctx.register_hook(hook_name, _make_callback(hook_name))