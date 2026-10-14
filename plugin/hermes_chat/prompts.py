"""Which open prompt message in a room belongs to which question Hermes is
waiting on. Imports nothing from Hermes."""

from __future__ import annotations

import unicodedata
from dataclasses import dataclass

# The server's limits for a clarify prompt's choices.
MAX_CHOICES = 6
MAX_CHOICE_CHARS = 200


@dataclass(frozen=True)
class OpenPrompt:
    message_id: str
    room_id: str
    kind: str  # "approval", "clarify", or "confirm" (a command's confirmation)
    session_key: str
    ref: str  # the clarify id, confirm id, or approval request id ("" when Hermes gave none)


class PromptTable:
    """Kept in memory only: after a restart Hermes is no longer waiting on
    the questions asked before it."""

    def __init__(self) -> None:
        self._open: dict[str, OpenPrompt] = {}

    def add(self, prompt: OpenPrompt) -> None:
        self._open[prompt.message_id] = prompt

    def take(self, message_id: str) -> OpenPrompt | None:
        return self._open.pop(message_id, None)

    def take_ref(self, ref: str) -> OpenPrompt | None:
        if not ref:
            return None
        for prompt in self._open.values():
            if prompt.ref == ref:
                return self._open.pop(prompt.message_id)
        return None

    def take_room(self, room_id: str, kinds: tuple[str, ...]) -> list[OpenPrompt]:
        taken = [p for p in self._open.values() if p.room_id == room_id and p.kind in kinds]
        for prompt in taken:
            del self._open[prompt.message_id]
        return taken


def clarify_choices(choices: list | None) -> list[str] | None:
    """The choices as the server takes them, or None when it would refuse
    them: more than six, or one that is empty, over 200 characters, holds a
    control character, or repeats another. The server trims each choice
    before it looks, so this does too."""
    texts = [str(choice).strip() for choice in choices or []]
    if len(texts) > MAX_CHOICES or len(set(texts)) != len(texts):
        return None
    for text in texts:
        if not 1 <= len(text) <= MAX_CHOICE_CHARS or any(unicodedata.category(c) == "Cc" for c in text):
            return None
    return texts


def numbered(question: str, choices: list) -> str:
    return question + "\n\n" + "\n".join(f"{n}. {choice}" for n, choice in enumerate(choices, 1))
