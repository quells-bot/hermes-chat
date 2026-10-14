"""A turn's tool steps, sent to the server in order: each tool's start, then
its finish against the activity the start made. Imports nothing from Hermes."""

from __future__ import annotations

import asyncio
import json
import logging
import threading
import unicodedata

from .client import ApiError, Unreachable

logger = logging.getLogger("hermes_chat.steps")

MAX_TOOL_CHARS = 64
MAX_PREVIEW_CHARS = 200
MAX_ARGUMENTS_BYTES = 65_536
_MAX_OPEN_STEPS = 1000


def _is_control(char: str) -> bool:
    return unicodedata.category(char) == "Cc"


def shape(tool_name: object, args: object) -> tuple[str, str, str]:
    """`(tool, preview, arguments)` cut to what the server accepts."""
    tool = "".join(c for c in str(tool_name or "") if not _is_control(c))[:MAX_TOOL_CHARS] or "tool"
    try:
        arguments = json.dumps(args, ensure_ascii=False, default=str)
    except (TypeError, ValueError):
        arguments = str(args)
    told = ""
    if isinstance(args, dict):
        told = next((value for value in args.values() if isinstance(value, str)), "")
    if not told and args:
        told = arguments
    preview = "".join(" " if _is_control(c) else c for c in told)[:MAX_PREVIEW_CHARS]
    arguments = arguments.encode("utf-8")[:MAX_ARGUMENTS_BYTES].decode("utf-8", errors="ignore")
    return tool, preview, arguments


class ToolSteps:
    """`started` and `finished` may be called from any thread; one worker on
    the loop sends them in the order they were called. Each call is tried
    once: a step is a record of the turn, not something the turn waits on."""

    def __init__(self, client, loop: asyncio.AbstractEventLoop) -> None:
        self._client = client
        self._loop = loop
        self._queue: asyncio.Queue = asyncio.Queue()
        self._worker: asyncio.Task | None = None
        self._activities: dict[str, str] = {}
        self._lock = threading.Lock()
        self._pending = 0
        self._closed = False

    def started(self, delivery_id: str, tool_call_id: str, tool: str, preview: str, arguments: str) -> None:
        self._submit(("start", delivery_id, tool_call_id, tool, preview, arguments))

    def finished(self, tool_call_id: str, duration_ms: int, succeeded: bool) -> None:
        self._submit(("finish", tool_call_id, duration_ms, succeeded))

    async def wait_idle(self) -> None:
        """Return when nothing is queued or in flight."""
        while self._pending:
            await asyncio.sleep(0.01)

    async def aclose(self) -> None:
        """Drop whatever is queued."""
        self._closed = True
        if self._worker is not None:
            self._worker.cancel()
            await asyncio.gather(self._worker, return_exceptions=True)
        with self._lock:
            self._pending = 0

    def _submit(self, item: tuple) -> None:
        if self._closed:
            return
        with self._lock:
            self._pending += 1
        try:
            self._loop.call_soon_threadsafe(self._enqueue, item)
        except RuntimeError:  # the loop is closed
            self._settle()

    def _enqueue(self, item: tuple) -> None:
        if self._closed:
            self._settle()
            return
        self._queue.put_nowait(item)
        if self._worker is None or self._worker.done():
            self._worker = asyncio.create_task(self._run())

    def _settle(self) -> None:
        with self._lock:
            self._pending = max(0, self._pending - 1)

    async def _run(self) -> None:
        while True:
            item = await self._queue.get()
            try:
                if item[0] == "start":
                    await self._start(*item[1:])
                else:
                    await self._finish(*item[1:])
            except Exception:
                logger.debug("tool step failed", exc_info=True)
            finally:
                self._settle()

    async def _start(self, delivery_id: str, tool_call_id: str, tool: str, preview: str, arguments: str) -> None:
        try:
            activity_id = await self._client.start_tool(delivery_id, tool, preview, arguments)
        except (ApiError, Unreachable) as exc:
            self._log_refusal("start", delivery_id, tool_call_id, exc)
            return
        if len(self._activities) >= _MAX_OPEN_STEPS:
            self._activities.pop(next(iter(self._activities)))
        self._activities[tool_call_id] = activity_id
        logger.info("tool step started delivery=%s activity=%s tool_call_id=%s", delivery_id, activity_id, tool_call_id)

    async def _finish(self, tool_call_id: str, duration_ms: int, succeeded: bool) -> None:
        activity_id = self._activities.pop(tool_call_id, None)
        if activity_id is None:
            return
        try:
            await self._client.finish_tool(activity_id, duration_ms, succeeded)
        except (ApiError, Unreachable) as exc:
            self._log_refusal("finish", activity_id, tool_call_id, exc)
            return
        logger.info("tool step finished activity=%s tool_call_id=%s", activity_id, tool_call_id)

    @staticmethod
    def _log_refusal(step: str, target: str, tool_call_id: str, exc: Exception) -> None:
        if isinstance(exc, ApiError) and exc.status == 409:
            logger.info("tool step %s for %s refused (paused or closed) tool_call_id=%s", step, target, tool_call_id)
        else:
            logger.warning("tool step %s for %s not recorded tool_call_id=%s: %s", step, target, tool_call_id, exc)
