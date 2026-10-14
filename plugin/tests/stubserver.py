"""A scripted local HTTP server for the plugin's tests.

Standard library only (`asyncio.start_server`). Routes are queued with
`script`; the agent event stream is scripted with `events`. Whatever is not
scripted answers 404 so a wrong path fails loudly.
"""

from __future__ import annotations

import asyncio
import json
from dataclasses import dataclass
from typing import Any


class Hang:
    """A scripted response that never answers."""


class Drop:
    """A scripted response that closes the connection at once."""


@dataclass
class Recorded:
    method: str
    path: str
    headers: dict[str, str]
    json: Any | None
    query: str = ""
    body: bytes = b""


class Emitter:
    """Writes to the event stream; a scripted events item may keep writing."""

    def __init__(self, writer) -> None:
        self._writer = writer

    def write(self, data: bytes) -> None:
        self._writer.write(data)

    async def drain(self) -> None:
        await self._writer.drain()


class StubServer:
    def __init__(self) -> None:
        self.requests: list[Recorded] = []
        self._scripts: dict[tuple[str, str], list[Any]] = {}
        self._events_script: list[Any] | None = None
        self._events_index = 0
        self._stop = asyncio.Event()
        self._server: asyncio.AbstractServer | None = None
        self._writers: set = set()

    @property
    def url(self) -> str:
        return f"http://127.0.0.1:{self._port}"

    def script(self, method: str, path: str, *responses: Any) -> None:
        """Queue responses for a route. Each is a status, `(status, body)`,
        a callable returning one of those, `Hang`, or `Drop`."""
        self._scripts.setdefault((method.upper(), path), []).extend(responses)

    def events(self, *chunks: Any) -> None:
        """Replace the script for GET /agent/events. Items: bytes to write,
        `Hang` to hold the stream open, `Drop` to close it, or a callable
        taking an `Emitter`."""
        self._events_script = list(chunks)
        self._events_index = 0

    async def start(self) -> None:
        self._server = await asyncio.start_server(self._handle, "127.0.0.1", 0)
        self._port = self._server.sockets[0].getsockname()[1]

    async def stop(self) -> None:
        self._stop.set()
        for w in list(self._writers):
            w.close()
        self._server.close()
        await self._server.wait_closed()

    async def _handle(self, reader, writer) -> None:
        self._writers.add(writer)
        try:
            while True:
                record = await self._read_request(reader)
                if record is None:
                    return
                key = (record.method, record.path)
                queued = self._scripts.get(key)
                if queued:
                    spec = queued.pop(0)
                    if callable(spec):
                        spec = await spec(record)
                    if spec is Hang:
                        await self._stop.wait()
                        return
                    if spec is Drop:
                        return
                    status, body = self._coerce(spec)
                    await self._respond(writer, status, body)
                    continue
                if key == ("GET", "/agent/events") and self._events_script is not None:
                    await self._serve_events(writer)
                    return
                await self._respond(
                    writer, 404,
                    {"error": {"code": "not_found", "message": f"unscripted {key[0]} {key[1]}"}},
                )
        finally:
            self._writers.discard(writer)
            writer.close()

    async def _read_request(self, reader) -> Recorded | None:
        line = await reader.readline()
        if not line:
            return None
        method, target, _ = line.decode("latin-1").rstrip("\r\n").split(" ", 2)
        path, _, query = target.partition("?")
        headers: dict[str, str] = {}
        while True:
            hline = await reader.readline()
            if not hline:
                return None
            if hline in (b"\r\n", b"\n"):
                break
            name, _, value = hline.decode("latin-1").rstrip("\r\n").partition(":")
            headers[name.strip().lower()] = value.strip()
        body = b""
        if "content-length" in headers:
            body = await reader.readexactly(int(headers["content-length"]))
        parsed = None
        if body and "json" in headers.get("content-type", ""):
            parsed = json.loads(body)
        record = Recorded(method, path, headers, parsed, query, body)
        self.requests.append(record)
        return record

    async def _serve_events(self, writer) -> None:
        writer.write(b"HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nConnection: close\r\n\r\n")
        await writer.drain()
        emit = Emitter(writer)
        while True:
            if self._events_index >= len(self._events_script or []):
                await self._stop.wait()  # script exhausted: hold the stream open
                return
            item = self._events_script[self._events_index]
            self._events_index += 1
            if item is Hang:
                await self._stop.wait()
                return
            if item is Drop:
                return
            if callable(item):
                await item(emit)
                continue
            emit.write(item)
            await emit.drain()

    @staticmethod
    def _coerce(spec) -> tuple[int, Any]:
        if isinstance(spec, int):
            return spec, None
        return spec[0], (spec[1] if len(spec) > 1 else None)

    async def _respond(self, writer, status: int, body: Any) -> None:
        reason = {
            200: "OK", 201: "Created", 204: "No Content", 400: "Bad Request",
            401: "Unauthorized", 404: "Not Found", 409: "Conflict",
            500: "Internal Server Error",
        }.get(status, "Status")
        if body is None:
            writer.write(f"HTTP/1.1 {status} {reason}\r\nContent-Length: 0\r\n\r\n".encode())
        else:
            data = json.dumps(body).encode()
            writer.write(
                f"HTTP/1.1 {status} {reason}\r\nContent-Type: application/json\r\n"
                f"Content-Length: {len(data)}\r\n\r\n".encode() + data
            )
        await writer.drain()