"""Server-Sent Events framing as the chat server encodes it (`internal/sse`).

`FrameParser` ingests raw bytes and yields complete frames. A frame split
across chunks — even inside a multi-byte character — is never decoded early,
because only complete lines are decoded.
"""

from __future__ import annotations

from dataclasses import dataclass


@dataclass(frozen=True)
class Frame:
    event: str
    data: str
    id: str | None


class FrameParser:
    def __init__(self) -> None:
        self._buf = bytearray()
        self._event: str | None = None
        self._id: str | None = None
        self._data: list[str] = []
        self._started = False

    def feed(self, chunk: bytes) -> list[Frame]:
        """Append `chunk` and return the frames completed by it."""
        self._buf.extend(chunk)
        frames: list[Frame] = []
        while True:
            nl = self._buf.find(b"\n")
            if nl < 0:
                break
            raw = bytes(self._buf[:nl])
            del self._buf[: nl + 1]
            line = raw[:-1] if raw.endswith(b"\r") else raw
            if not line:
                if self._started:
                    frames.append(self._finish())
                continue
            name, sep, value = line.partition(b":")
            if sep and value.startswith(b" "):
                value = value[1:]
            if not name:
                continue  # a comment, e.g. the server's heartbeat
            field = name.decode("utf-8")
            text = value.decode("utf-8")
            if field == "event":
                self._event = text
            elif field == "id":
                self._id = text
            elif field == "data":
                self._data.append(text)
            else:
                continue  # other fields are ignored, as the Go decoder does
            self._started = True
        return frames

    def _finish(self) -> Frame:
        frame = Frame(
            event=self._event if self._event is not None else "message",
            data="\n".join(self._data),
            id=self._id,
        )
        self._event = None
        self._id = None
        self._data = []
        self._started = False
        return frame