"""Turn reports (`working`, `done`, `failed`) retried until the server
answers. Imports nothing from Hermes."""

from __future__ import annotations

import asyncio
import logging

from hermes_chat.client import ApiError, Unreachable

logger = logging.getLogger("hermes_chat.reports")


class TurnReporter:
    def __init__(self, client, *, backoff_first: float = 0.5, backoff_max: float = 30.0, sleep=asyncio.sleep) -> None:
        self._client = client
        self._backoff_first = backoff_first
        self._backoff_max = backoff_max
        self._sleep = sleep
        self._queues: dict[str, asyncio.Queue] = {}
        self._workers: dict[str, asyncio.Task] = {}
        self._busy = 0
        self._idle = asyncio.Event()
        self._idle.set()

    def submit(self, delivery_id: str, state: str) -> None:
        """Queue a report; the call returns at once."""
        queue = self._queues.get(delivery_id)
        if queue is None:
            queue = asyncio.Queue()
            self._queues[delivery_id] = queue
            self._workers[delivery_id] = asyncio.create_task(self._run(delivery_id, queue))
        self._busy += 1
        self._idle.clear()
        queue.put_nowait(state)

    async def wait_idle(self) -> None:
        """Return when nothing is queued or in flight."""
        if self._busy:
            await self._idle.wait()

    async def aclose(self) -> None:
        """Stop retrying; drop whatever is queued."""
        workers = list(self._workers.values())
        for worker in workers:
            worker.cancel()
        if workers:
            await asyncio.gather(*workers, return_exceptions=True)
        self._queues.clear()
        self._busy = 0
        self._idle.set()

    async def _run(self, delivery_id: str, queue: asyncio.Queue) -> None:
        try:
            while not queue.empty():
                state = queue.get_nowait()
                await self._send_one(delivery_id, state)
        finally:
            self._workers.pop(delivery_id, None)
            self._queues.pop(delivery_id, None)

    async def _send_one(self, delivery_id: str, state: str) -> None:
        delay = self._backoff_first
        try:
            while True:
                try:
                    await self._client.report(delivery_id, state)
                    return
                except Unreachable:
                    logger.warning(
                        "turn report %s for delivery %s unreachable; retrying in %.1fs", state, delivery_id, delay
                    )
                    await self._sleep(delay)
                    delay = min(delay * 2, self._backoff_max)
                except ApiError as exc:
                    if exc.status in (404, 409):
                        logger.info(
                            "turn report %s for delivery %s answered with %s; stopping",
                            state, delivery_id, exc.status,
                        )
                    else:
                        logger.warning("turn report %s for delivery %s dropped: %s", state, delivery_id, exc)
                    return
        finally:
            self._busy -= 1
            if self._busy <= 0:
                self._busy = 0
                self._idle.set()