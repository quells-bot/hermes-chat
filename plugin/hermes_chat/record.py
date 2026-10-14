"""The open-turn record: deliveries accepted and not yet reported finished.

Persisted as `{"version": 1, "turns": {"<delivery id>": "<room id>"}}`,
written to a temporary file in the same directory and moved into place so a
half-written record never lands. A corrupt record starts empty with a
warning. Imports nothing from Hermes.
"""

from __future__ import annotations

import json
import logging
import os
import pathlib
import tempfile

logger = logging.getLogger("hermes_chat.record")


class OpenTurns:
    def __init__(self, path: pathlib.Path) -> None:
        self._path = pathlib.Path(path)

    def add(self, delivery_id: str, room_id: str) -> None:
        turns = self._load()
        turns[delivery_id] = room_id
        self._write(turns)

    def remove(self, delivery_id: str) -> None:
        turns = self._load()
        if delivery_id not in turns:
            return
        del turns[delivery_id]
        self._write(turns)

    def all(self) -> dict[str, str]:
        return self._load()

    def for_room(self, room_id: str) -> str | None:
        for delivery_id, its_room in self._load().items():
            if its_room == room_id:
                return delivery_id
        return None

    def _load(self) -> dict[str, str]:
        try:
            raw = self._path.read_text("utf-8")
            data = json.loads(raw)
        except FileNotFoundError:
            return {}
        except ValueError:
            data = None
        if not isinstance(data, dict) or data.get("version") != 1 or not isinstance(data.get("turns"), dict):
            logger.warning(
                "open-turn record at %s is unreadable; starting with an empty record", self._path
            )
            return {}
        return {k: v for k, v in data["turns"].items() if isinstance(k, str) and isinstance(v, str)}

    def _write(self, turns: dict[str, str]) -> None:
        self._path.parent.mkdir(parents=True, exist_ok=True)
        body = json.dumps({"version": 1, "turns": turns})
        fd, tmp_path = tempfile.mkstemp(dir=self._path.parent, prefix=self._path.name + ".", suffix=".tmp")
        try:
            with os.fdopen(fd, "w", encoding="utf-8") as f:
                f.write(body)
            os.replace(tmp_path, self._path)
        except BaseException:
            if os.path.exists(tmp_path):
                os.unlink(tmp_path)
            raise