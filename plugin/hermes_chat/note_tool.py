"""The tool an agent uses to suggest an entry for the note of the person it
is answering. The person approves an entry before it is kept."""

from __future__ import annotations

import asyncio
import logging

from . import hooks

logger = logging.getLogger("hermes_chat.note_tool")

NAME = "suggest_note_entry"
MAX_ENTRY_CHARS = 300
DESCRIPTION = (
    "Suggest one short entry (at most 300 characters) for the note of the person you are answering. "
    "They approve it before it is kept. Use it for a lasting preference or fact about them, "
    "not for the task at hand."
)
SCHEMA = {
    "name": NAME,
    "description": DESCRIPTION,
    "parameters": {
        "type": "object",
        "properties": {
            "text": {"type": "string", "maxLength": MAX_ENTRY_CHARS, "description": "The entry, in one or two sentences."},
        },
        "required": ["text"],
        "additionalProperties": False,
    },
}
_WAIT_SECONDS = 20


def _handle(args: dict, **_kw) -> str:
    """Runs on the thread Hermes runs the tool on; the request itself is made
    on the adapter's loop."""
    try:
        room_id = hooks.session_room()
        if room_id is None:
            return "This tool only works in a Hermes Chat room."
        text = str((args or {}).get("text") or "").strip()
        if not text or len(text) > MAX_ENTRY_CHARS:
            return "An entry needs between 1 and 300 characters."
        session = hooks.session_key()
        adapter = next((a for a in hooks.adapters() if a.has_open_turn(room_id, session_key=session)), None)
        if adapter is None:
            return "There is no open turn in this room to suggest an entry from."
        future = asyncio.run_coroutine_threadsafe(adapter.suggest_note_entry(room_id, text), adapter._loop)
        return future.result(timeout=_WAIT_SECONDS)
    except Exception as exc:
        logger.debug("suggest_note_entry failed: %s", type(exc).__name__)
        return "The suggestion could not be sent."


def register_note_tool(ctx) -> None:
    # No check_fn: Hermes evaluates one outside any session, so it cannot ask
    # which platform the turn is on. The handler refuses elsewhere instead.
    ctx.register_tool(name=NAME, toolset="hermes_chat", schema=SCHEMA, handler=_handle,
                      description=DESCRIPTION, emoji="📝")
