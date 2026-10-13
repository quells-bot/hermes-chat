"""The agent event stream: cursor, reconnect with backoff, silence watchdog.

Imports nothing from Hermes.
"""

from __future__ import annotations

import asyncio
import json
import logging
from typing import Callable

from .client import ApiError, Unauthorized, Unreachable
from .sse import Frame, FrameParser

logger = logging.getLogger("hermes_chat.stream")


class EventStream:
    def __init__(
        self,
        client,
        on_event: Callable[[str, dict], None],
        *,
        on_revoked: Callable[[], None],
        silence_timeout: float = 60.0,
        backoff_first: float = 1.0,
        backoff_max: float = 30.0,
        sleep=asyncio.sleep,
    ) -> None:
        self._client = client
        self._on_event = on_event
        self._on_revoked = on_revoked
        self._silence_timeout = silence_timeout
        self._backoff_first = backoff_first
        self._backoff_max = backoff_max
        self._sleep = sleep
        self.cursor: int | None = None
        self.connected = False

    async def run(self) -> None:
        """Read the stream forever. Returns only after `on_revoked` (a 401);
        otherwise runs until cancelled."""
        delay = self._backoff_first
        while True:
            try:
                async with self._client.open_events(self.cursor) as chunks:
                    self.connected = True
                    parser = FrameParser()
                    try:
                        while True:
                            try:
                                chunk = await asyncio.wait_for(anext(chunks), self._silence_timeout)
                            except StopAsyncIteration:
                                logger.info("events stream ended; reconnecting")
                                break
                            except TimeoutError:
                                logger.warning(
                                    "no bytes from the events stream for %.0fs; treating it as dead",
                                    self._silence_timeout,
                                )
                                break
                            for frame in parser.feed(chunk):
                                if await self._handle(frame):
                                    delay = self._backoff_first
                    finally:
                        self.connected = False
            except Unauthorized:
                self._on_revoked()
                return
            except (Unreachable, ApiError) as exc:
                logger.warning("events stream error: %s", exc)
            await self._sleep(delay)
            delay = min(delay * 2, self._backoff_max)

    async def _handle(self, frame: Frame) -> bool:
        """Act on one frame. Returns True when it was a `ready` (backoff resets)."""
        if frame.id is not None:
            try:
                self.cursor = int(frame.id)
            except ValueError:
                pass
        if frame.event == "ready":
            try:
                cursor = json.loads(frame.data).get("cursor")
            except ValueError:
                logger.warning("ignoring a ready frame whose data is not JSON")
                return True
            if isinstance(cursor, int):
                self.cursor = cursor
            return True
        try:
            data = json.loads(frame.data)
        except ValueError:
            logger.warning("skipping %s frame with non-JSON data", frame.event)
            return False
        try:
            await self._on_event(frame.event, data)
        except Exception:
            logger.exception("on_event handler failed for %s frame", frame.event)
        return False