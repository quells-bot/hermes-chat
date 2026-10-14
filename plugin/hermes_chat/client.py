"""The chat server's agent API over httpx. Imports nothing from Hermes."""

from __future__ import annotations

from contextlib import asynccontextmanager
from typing import Any, AsyncIterator

import httpx


class ApiError(Exception):
    """The server answered with an error status (a 4xx other than 401)."""

    def __init__(self, status: int, code: str, message: str) -> None:
        super().__init__(f"{status} {code}: {message}" if code else f"HTTP {status}")
        self.status = status
        self.code = code
        self.message = message


class Unauthorized(ApiError):
    """A 401: the token is wrong or was revoked."""


class Unreachable(Exception):
    """A network error, a timeout, or a 5xx: the call can be retried."""


def _raise_for_status(status: int, body: Any) -> None:
    if 200 <= status < 300:
        return
    code, message = "", ""
    if isinstance(body, dict):
        err = body.get("error")
        if isinstance(err, dict):
            code = err.get("code")
            message = err.get("message")
            if not isinstance(code, str):
                code = ""
            if not isinstance(message, str):
                message = ""
    if status == 401:
        raise Unauthorized(status, code, message)
    if status >= 500:
        raise Unreachable(f"{code}: {message}" if code else f"HTTP {status}")
    raise ApiError(status, code, message)


class ChatClient:
    def __init__(self, base_url: str, token: str, *, timeout: float = 30.0) -> None:
        self._timeout = timeout
        self._client = httpx.AsyncClient(
            base_url=base_url.rstrip("/"),
            headers={"Authorization": f"Bearer {token}"},
            timeout=timeout,
        )

    async def me(self) -> dict:
        return (await self._send("GET", "/agent/me")).json()

    async def post_message(self, room_id: str, text: str, reply_to_id: str | None = None) -> str:
        body: dict[str, Any] = {"text": text}
        if reply_to_id is not None:
            body["reply_to_id"] = reply_to_id
        resp = await self._send("POST", f"/agent/rooms/{room_id}/messages", json=body)
        return resp.json()["id"]

    async def edit_message(self, message_id: str, text: str, final: bool) -> None:
        await self._send("PUT", f"/agent/messages/{message_id}", json={"text": text, "final": final})

    async def report(self, delivery_id: str, state: str) -> None:
        await self._send("POST", f"/agent/deliveries/{delivery_id}/{state}")

    async def start_tool(self, delivery_id: str, tool: str, preview: str, arguments: str) -> str:
        """Record a tool call the turn started; returns the activity's id."""
        resp = await self._send("POST", f"/agent/deliveries/{delivery_id}/tools",
                                json={"tool": tool, "preview": preview, "arguments": arguments})
        return resp.json()["id"]

    async def finish_tool(self, activity_id: str, duration_ms: int, succeeded: bool) -> None:
        await self._send("PUT", f"/agent/activities/{activity_id}",
                         json={"duration_ms": duration_ms, "succeeded": succeeded})

    async def post_prompt(self, room_id: str, kind: str, text: str, choices: list[str]) -> str:
        """Ask the room a question as a prompt message; returns its id."""
        resp = await self._send("POST", f"/agent/rooms/{room_id}/prompts",
                                json={"kind": kind, "text": text, "choices": choices})
        return resp.json()["id"]

    async def expire_prompt(self, message_id: str) -> None:
        await self._send("POST", f"/agent/prompts/{message_id}/expire")

    @asynccontextmanager
    async def open_events(self, cursor: int | None) -> AsyncIterator[AsyncIterator[bytes]]:
        """The agent event stream. Yields an async iterator of raw bytes;
        the caller owns the silence timeout (there is none here)."""
        headers = {"Accept": "text/event-stream"}
        if cursor is not None:
            headers["Last-Event-ID"] = str(cursor)
        req = self._client.build_request(
            "GET", "/agent/events", headers=headers, timeout=httpx.Timeout(self._timeout, read=None)
        )
        try:
            resp = await self._client.send(req, stream=True)
        except httpx.TransportError as e:
            raise Unreachable(str(e)) from e
        try:
            if resp.status_code != 200:
                await resp.aread()
                body = None
                try:
                    body = resp.json()
                except ValueError:
                    pass
                _raise_for_status(resp.status_code, body)
            yield resp.aiter_bytes()
        finally:
            await resp.aclose()

    async def aclose(self) -> None:
        await self._client.aclose()

    async def _send(self, method: str, path: str, **kwargs) -> httpx.Response:
        try:
            resp = await self._client.request(method, path, **kwargs)
        except httpx.TransportError as e:
            raise Unreachable(str(e)) from e
        if resp.status_code >= 400:
            body = None
            try:
                body = resp.json()
            except ValueError:
                pass
            _raise_for_status(resp.status_code, body)
        return resp