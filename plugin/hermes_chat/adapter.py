"""The Hermes Chat platform adapter: stream events become Hermes MessageEvents
and Hermes's calls become chat-server client calls.

Only this module, `hooks.py` and `note_tool.py` import Hermes; the client modules
(`sse.py`, `client.py`, `stream.py`, `reports.py`, `record.py`, `checks.py`,
`steps.py`, `prompts.py`) import nothing from it.
"""

from __future__ import annotations

import asyncio
import contextlib
import functools
import hashlib
import logging
import pathlib
import re
import unicodedata
from collections import deque

from gateway.config import Platform
from gateway.platforms._shared import extra_or_secret, get_scoped_secret, seed_extra_from_env
from gateway.platforms.base import BasePlatformAdapter, SendResult
from gateway.platforms.event import MessageEvent, MessageType, ProcessingOutcome
from gateway.platforms.helpers import MessageDeduplicator
from hermes_constants import get_hermes_home

from . import hooks
from .client import ApiError, ChatClient, Unauthorized, Unreachable
from .checks import display_warnings
from .prompts import OpenPrompt, PromptTable, clarify_choices, numbered
from .record import OpenTurns
from .reports import TurnReporter
from .steps import ToolSteps, shape
from .stream import EventStream

logger = logging.getLogger("hermes_chat.adapter")

PLATFORM = "hermes_chat"
REMOVED_AUTHOR_NAME = "removed user"
MAX_TEXT_BYTES = 262_144  # the server's MessageText limit, in bytes
MAX_ATTACHMENT_BYTES = 26_214_400  # the server's upload limit, 25 MiB
MAX_ATTACHMENT_NAME_CHARS = 255
# Hermes ends in-progress text with this cursor (its `streaming.cursor`
# default). The server already says whether a message is still streaming.
_STREAMING_CURSOR = re.compile(r"\s*▉\Z")
# A later stretch of a reply starts with the blank lines that separated it
# from the stretch before, which is now a message of its own.
_LEADING_BLANK_LINES = re.compile(r"\A\s*\n\s*")


# A command confirmation's choices in the room, and what Hermes calls them.
_CONFIRM_CHOICES = {"once": "once", "always": "always", "deny": "cancel"}


def _read_within_limit(path: pathlib.Path) -> bytes | None:
    """The file's bytes, or None when it is larger than the server takes."""
    with path.open("rb") as handle:
        data = handle.read(MAX_ATTACHMENT_BYTES + 1)
    return None if len(data) > MAX_ATTACHMENT_BYTES else data


def _attachment_name(name: str) -> str:
    cleaned = "".join(c for c in name if unicodedata.category(c) != "Cc")[:MAX_ATTACHMENT_NAME_CHARS]
    return cleaned or "file"


def _is_typed_question(metadata) -> bool:
    """Hermes marks the text fallback of an approval or a clarify this way.
    A command's confirmation carries the same mark, but no turn waits on it,
    so its typed reply does arrive; Hermes sends that one as a forced send."""
    if not isinstance(metadata, dict):
        return False
    return bool(metadata.get("is_approval_prompt")) and not metadata.get("force_proactive_send")


def _confirm_text(title: str, message: str) -> str:
    """What a command's confirmation asks, without the list of choices and
    the typed fallback Hermes writes for platforms that have no buttons."""
    asked = re.split(r"^Choose:\s*$", message, maxsplit=1, flags=re.MULTILINE)[0].strip() or message.strip()
    return asked if title in asked else f"**{title}**\n\n{asked}"


def _outgoing(content: str) -> str:
    """The text as the room should store it."""
    cleaned = _LEADING_BLANK_LINES.sub("", _STREAMING_CURSOR.sub("", content))
    # Text that is nothing else stays as it is: the server refuses a blank
    # message, and Hermes needs the message id to edit.
    return cleaned if cleaned.strip() else content


class HermesChatAdapter(BasePlatformAdapter):
    MAX_MESSAGE_LENGTH = MAX_TEXT_BYTES
    REQUIRES_EDIT_FINALIZE = True

    def __init__(self, config) -> None:
        super().__init__(config=config, platform=Platform(PLATFORM))
        self._url = extra_or_secret(config.extra, "url", "HERMES_CHAT_URL")
        self._token = extra_or_secret(config.extra, "token", "HERMES_CHAT_TOKEN")
        self._client = ChatClient(self._url, self._token)
        self._reporter = TurnReporter(self._client)
        self._dedup = MessageDeduplicator()
        self._open_turns: OpenTurns | None = None
        self._stream: EventStream | None = None
        self._stream_task: asyncio.Task | None = None
        self._loop: asyncio.AbstractEventLoop | None = None
        self._steps: ToolSteps | None = None
        self._prompts = PromptTable()
        self._expiring: set[asyncio.Task] = set()
        self._lock_held = False
        # The room gate: a room is busy from the moment a delivery for it is
        # recorded until that delivery's turn is closed.
        self._room_busy_rooms: set[str] = set()
        # Each room's source as its last delivery gave it, for control events.
        self._room_sources: dict[str, object] = {}
        # The Hermes session each room's turn runs in: on a gateway serving
        # several profiles, two adapters can have the same delivery open.
        self._room_sessions: dict[str, str] = {}
        self._parked: dict[str, deque] = {}
        # Turn state, keyed on the delivery id.
        self._turn_tasks: dict[str, asyncio.Task] = {}
        self._turn_outcomes: dict[str, ProcessingOutcome] = {}
        self._turn_callbacks: set[str] = set()
        # Restart: deliveries left open from a previous run, awaiting Hermes's
        # resume event, and the shutdown flag.
        self._awaiting_resume: set[str] = set()
        self._resume_watcher: asyncio.Task | None = None
        self._shutting_down = False

    # How long connect() waits for Hermes to resume an interrupted session
    # before reporting that delivery failed. Overridable per instance in tests.
    RESUME_GRACE_SECONDS = 90.0

    @property
    def message_len_fn(self):
        """The server counts message length in UTF-8 bytes."""
        return lambda text: len(text.encode("utf-8"))

    async def send(self, chat_id: str, content: str, reply_to=None, metadata=None):
        if _is_typed_question(metadata):
            # Hermes's fallback when a prompt could not be posted: a message
            # asking for a typed reply. Here that reply would wait in the
            # room's queue behind the turn that is asking, so the question
            # could never be answered. Refused, Hermes gives the question up.
            logger.info("send room=%s refused: a typed question cannot be answered during a turn", chat_id)
            return SendResult(success=False, error="a typed question cannot be answered in this room")
        try:
            message_id = await self._client.post_message(chat_id, _outgoing(content), reply_to_id=reply_to)
        except ApiError as exc:
            self._log_send_error("send", chat_id, exc)
            return SendResult(success=False, error=f"{exc.code}: {exc.message}")
        except Unreachable as exc:
            logger.warning("send room=%s unreachable: %s", chat_id, exc)
            return SendResult(success=False, error=str(exc), retryable=True)
        logger.info("send room=%s message=%s", chat_id, message_id)
        return SendResult(success=True, message_id=message_id)

    async def edit_message(self, chat_id: str, message_id: str, content: str, *, finalize: bool = False):
        try:
            await self._client.edit_message(message_id, _outgoing(content), finalize)
        except ApiError as exc:
            self._log_send_error("edit_message", chat_id, exc)
            return SendResult(success=False, error=f"{exc.code}: {exc.message}")
        except Unreachable as exc:
            logger.warning("edit_message room=%s message=%s unreachable: %s", chat_id, message_id, exc)
            return SendResult(success=False, error=str(exc), retryable=True)
        logger.info("edit_message room=%s message=%s finalize=%s", chat_id, message_id, finalize)
        return SendResult(success=True)

    # ── Files ───────────────────────────────────────────────────────────────

    async def _send_file(self, chat_id: str, path: str, caption: str | None, reply_to: str | None,
                         file_name: str | None = None) -> SendResult:
        """Upload one local file and post it as a message. Nothing is sent
        unless the whole file can be: no upload without its message."""
        resolved = self.validate_media_delivery_path(path)
        if not resolved:
            logger.info("file room=%s not sent: the path is not one Hermes allows", chat_id)
            return SendResult(success=False, error="that file may not be sent")
        try:
            data = await asyncio.to_thread(_read_within_limit, pathlib.Path(resolved))
        except OSError as exc:
            logger.info("file room=%s not sent: %s", chat_id, type(exc).__name__)
            return SendResult(success=False, error="the file could not be read")
        if data is None:
            logger.info("file room=%s not sent: larger than the limit", chat_id)
            return SendResult(success=False, error="the file is larger than 25 MiB")
        if not data:
            logger.info("file room=%s not sent: empty", chat_id)
            return SendResult(success=False, error="the file is empty")
        try:
            attachment_id = await self._client.upload_attachment(
                chat_id, _attachment_name(file_name or pathlib.Path(resolved).name), data)
            message_id = await self._client.post_message(
                chat_id, _outgoing(caption or ""), reply_to_id=reply_to, attachment_ids=[attachment_id])
        except ApiError as exc:
            self._log_send_error("file", chat_id, exc)
            return SendResult(success=False, error=f"{exc.code}: {exc.message}")
        except Unreachable as exc:
            logger.warning("file room=%s unreachable: %s", chat_id, exc)
            return SendResult(success=False, error=str(exc), retryable=True)
        logger.info("file room=%s message=%s attachment=%s", chat_id, message_id, attachment_id)
        return SendResult(success=True, message_id=message_id)

    async def send_document(self, chat_id: str, file_path: str, caption=None, file_name=None,
                            reply_to=None, metadata=None, **kwargs) -> SendResult:
        return await self._send_file(chat_id, file_path, caption, reply_to, file_name)

    async def send_image_file(self, chat_id: str, image_path: str, caption=None, reply_to=None,
                              metadata=None, **kwargs) -> SendResult:
        return await self._send_file(chat_id, image_path, caption, reply_to)

    async def send_voice(self, chat_id: str, audio_path: str, caption=None, reply_to=None,
                         metadata=None, **kwargs) -> SendResult:
        return await self._send_file(chat_id, audio_path, caption, reply_to)

    async def send_video(self, chat_id: str, video_path: str, caption=None, reply_to=None,
                         metadata=None, **kwargs) -> SendResult:
        return await self._send_file(chat_id, video_path, caption, reply_to)

    @staticmethod
    def _log_send_error(method: str, chat_id: str, exc: ApiError) -> None:
        if exc.status == 409:
            logger.info("%s room=%s refused (paused)", method, chat_id)
        else:
            logger.warning("%s room=%s failed: %s", method, chat_id, exc)

    # ── Connection ──────────────────────────────────────────────────────────

    async def connect(self, *, is_reconnect: bool = False) -> bool:
        from gateway import status
        from hermes_cli.config import load_config

        acquired, _existing = status.acquire_scoped_lock(PLATFORM, self._token)
        if not acquired:
            logger.warning("token already in use by another profile; not connecting")
            return False
        self._lock_held = True
        try:
            await self._client.me()
        except Unauthorized as exc:
            self._set_fatal_error(
                "unauthorized",
                "HERMES_CHAT_TOKEN was refused by the chat server; check the agent's token",
                retryable=False,
            )
            self._release_lock()
            return False
        except Unreachable as exc:
            logger.warning("chat server unreachable at connect (%s); connecting anyway", exc)
        # The record name comes from the token, not from /agent/me: a restart
        # while the server is down must open the same record as the run before,
        # or an interrupted turn is never resumed and never reported.
        self._open_turns = OpenTurns(
            get_hermes_home() / "hermes_chat" / f"open-turns-{self._record_name()}.json")
        try:
            user_config = load_config() or {}
        except Exception:
            logger.debug("could not load the hermes config; skipping setup checks", exc_info=True)
        else:
            for line in display_warnings(user_config):
                logger.warning("%s", line)
        # Every delivery left open from a previous run is awaiting Hermes's
        # resume event; its room is busy until then.
        self._awaiting_resume = set(self._open_turns.all())
        for room_id in set(self._open_turns.all().values()):
            self._room_busy_rooms.add(room_id)
        self._resume_watcher = asyncio.create_task(self._resume_grace_watch())
        self._stream = EventStream(
            self._client,
            self._on_stream_event,
            on_revoked=lambda: self._set_fatal_error(
                "unauthorized",
                "the chat server revoked this HERMES_CHAT_TOKEN; the stream has stopped",
                retryable=False,
            ),
        )
        self._stream_task = asyncio.create_task(self._stream.run())
        self._loop = asyncio.get_running_loop()
        self._steps = ToolSteps(self._client, self._loop)
        hooks.attach(self)
        self._mark_connected()
        return True

    async def disconnect(self) -> None:
        hooks.detach(self)
        if self._resume_watcher is not None:
            self._resume_watcher.cancel()
            with contextlib.suppress(asyncio.CancelledError):
                await self._resume_watcher
        if self._stream_task is not None:
            self._stream_task.cancel()
            with contextlib.suppress(asyncio.CancelledError):
                await self._stream_task
        with contextlib.suppress(asyncio.TimeoutError):
            await asyncio.wait_for(self._reporter.wait_idle(), 5)
        await self._reporter.aclose()
        if self._steps is not None:
            with contextlib.suppress(asyncio.TimeoutError):
                await asyncio.wait_for(self._steps.wait_idle(), 5)
            await self._steps.aclose()
        await self._client.aclose()
        self._release_lock()
        self._mark_disconnected()

    def _record_name(self) -> str:
        digest = hashlib.sha256(self._token.encode("utf-8")).hexdigest()
        return digest[:16]

    def _release_lock(self) -> None:
        if self._lock_held:
            from gateway import status
            status.release_scoped_lock(PLATFORM, self._token)
            self._lock_held = False

    # ── Tool steps (called by hooks.py, from the thread that runs the tool) ──

    def _open_delivery(self, room_id: str, session_key: str | None) -> str | None:
        """The delivery this adapter has open in the room, when `session_key`
        (the session a tool is running in, if known) is this adapter's."""
        if self._open_turns is None:
            return None
        ours = self._room_sessions.get(room_id)
        if session_key and ours and session_key != ours:
            return None
        return self._open_turns.for_room(room_id)

    def tool_started(self, room_id: str, tool_call_id: str, tool_name: object, args: object,
                     session_key: str | None = None) -> bool:
        """Record a tool the room's turn started. False when this adapter has
        no turn open in that room, so the step belongs to no delivery."""
        if self._steps is None:
            return False
        delivery_id = self._open_delivery(room_id, session_key)
        if delivery_id is None:
            return False
        tool, preview, arguments = shape(tool_name, args)
        logger.info("tool_started delivery=%s tool=%s tool_call_id=%s", delivery_id, tool, tool_call_id)
        self._steps.started(delivery_id, tool_call_id, tool, preview, arguments)
        return True

    def tool_finished(self, tool_call_id: str, duration_ms: object, status: object) -> None:
        if self._steps is None:
            return
        try:
            elapsed = max(0, int(float(duration_ms or 0)))
        except (TypeError, ValueError):
            elapsed = 0
        self._steps.finished(tool_call_id, elapsed, status == "ok")

    # ── The note (called by note_tool.py) ───────────────────────────────────

    def has_open_turn(self, room_id: str, session_key: str | None = None) -> bool:
        return self._open_delivery(room_id, session_key) is not None

    async def suggest_note_entry(self, room_id: str, text: str) -> str:
        """Suggest an entry for the note of the person being answered in the
        room. Returns the sentence the tool gives back to the model."""
        delivery_id = self._open_turns.for_room(room_id) if self._open_turns is not None else None
        if delivery_id is None:
            return "There is no open turn in this room to suggest an entry from."
        try:
            entry = await self._client.suggest_note_entry(delivery_id, text)
        except ApiError as exc:
            self._log_send_error("note entry", room_id, exc)
            return f"The suggestion was not accepted: {exc.message or exc.code}"
        except Unreachable as exc:
            logger.warning("note entry room=%s unreachable: %s", room_id, exc)
            return "The chat server could not be reached."
        logger.info("note entry suggested delivery=%s entry=%s", delivery_id, entry.get("id"))
        return "Suggested. The person will see it in their note and decide whether to keep it."

    async def get_chat_info(self, chat_id: str) -> dict:
        return {"name": chat_id, "type": "dm"}

    # ── Inbound ─────────────────────────────────────────────────────────────

    async def _on_stream_event(self, name: str, data: dict) -> None:
        if name == "delivery":
            await self._on_delivery(data)
        elif name in ("stop", "pause"):
            await self._stop_room(name, data["room_id"])
        elif name == "steer":
            await self._steer_room(data["room_id"], data["message"])
        elif name == "prompt.answered":
            await self._on_prompt_answered(data)
        else:
            room_id = data.get("room_id") or (data.get("room") or {}).get("id")
            logger.info("ignoring %s for room %s", name, room_id)

    # ── Prompts: Hermes's questions, asked in the room and answered there ───

    async def _post_prompt(self, room_id: str, kind: str, text: str, choices: list[str],
                           prompt_kind: str, session_key: str, ref: str) -> SendResult:
        try:
            message_id = await self._client.post_prompt(room_id, kind, text, choices)
        except ApiError as exc:
            self._log_send_error("prompt", room_id, exc)
            return SendResult(success=False, error=f"{exc.code}: {exc.message}")
        except Unreachable as exc:
            logger.warning("prompt room=%s unreachable: %s", room_id, exc)
            return SendResult(success=False, error=str(exc), retryable=True)
        self._prompts.add(OpenPrompt(message_id=message_id, room_id=room_id, kind=prompt_kind,
                                     session_key=session_key, ref=ref))
        logger.info("prompt room=%s message=%s kind=%s", room_id, message_id, prompt_kind)
        return SendResult(success=True, message_id=message_id)

    async def _send_exec_approval_prompt(self, prompt) -> SendResult:
        from tools import approval

        # Hermes queues the request before it asks; the newest is this one.
        # Its id says which approval an answer is for when two are open.
        try:
            pending = approval.list_gateway_approvals(prompt.session_key)
            request_id = str((pending[-1] if pending else {}).get("request_id") or "")
        except Exception:
            logger.debug("could not read the pending approval's id", exc_info=True)
            request_id = ""
        result = await self._post_prompt(prompt.chat_id, "approval", prompt.text, prompt.choices,
                                         "approval", prompt.session_key, request_id)
        if not result.success and request_id:
            # Nobody can answer it now, and the turn would wait on it for the
            # whole approval timeout.
            try:
                approval.withdraw_gateway_approval(
                    prompt.session_key, request_id, "the approval could not be asked in the room")
                logger.info("approval for room %s withdrawn: its prompt could not be posted", prompt.chat_id)
            except Exception:
                logger.warning("could not withdraw an approval that was not asked", exc_info=True)
        return result

    async def send_clarify(self, chat_id: str, question: str, choices, clarify_id: str,
                           session_key: str, metadata=None) -> SendResult:
        """The answer comes back only as `prompt.answered`: a typed reply
        would wait in the room's queue behind the turn that is asking."""
        fitted = clarify_choices(choices)
        if fitted is None:
            question, fitted = numbered(question, choices), []
        return await self._post_prompt(chat_id, "clarify", question, fitted, "clarify", session_key, clarify_id)

    async def send_slash_confirm(self, chat_id: str, title: str, message: str, session_key: str,
                                 confirm_id: str, metadata=None) -> SendResult:
        """A command's confirmation (/new and the like). Its own turn ends as
        soon as it has asked, so the prompt outlives the turn; a newer one for
        the room replaces it, as it does in Hermes."""
        for stale in self._prompts.take_room(chat_id, ("confirm",)):
            await self._expire_prompt(stale)
        return await self._post_prompt(chat_id, "approval", _confirm_text(title, message), list(_CONFIRM_CHOICES),
                                       "confirm", session_key, confirm_id)

    async def retire_clarify_card(self, clarify_id: str, notice: str) -> None:
        """Hermes gave up on a clarify (timeout, or the turn moved on)."""
        prompt = self._prompts.take_ref(clarify_id)
        if prompt is not None:
            await self._expire_prompt(prompt)

    async def _expire_prompt(self, prompt: OpenPrompt) -> None:
        try:
            await self._client.expire_prompt(prompt.message_id)
        except (ApiError, Unreachable) as exc:
            logger.info("prompt message=%s not expired: %s", prompt.message_id, exc)
            return
        logger.info("prompt message=%s expired", prompt.message_id)

    async def _on_prompt_answered(self, data: dict) -> None:
        prompt = self._prompts.take(data["message_id"])
        if prompt is None:
            logger.info("answer for prompt %s, which is not open here; ignoring", data["message_id"])
            return
        logger.info("prompt room=%s message=%s kind=%s answered", prompt.room_id, prompt.message_id, prompt.kind)
        try:
            if prompt.kind == "approval":
                from tools import approval
                which = {"request_id": prompt.ref} if prompt.ref else {}
                approval.resolve_gateway_approval(prompt.session_key, data["answer"], **which)
            elif prompt.kind == "clarify":
                from tools import clarify_gateway
                clarify_gateway.resolve_gateway_clarify(prompt.ref, data["answer"])
            else:
                from tools import slash_confirm
                choice = _CONFIRM_CHOICES.get(data["answer"], "cancel")
                shown = await slash_confirm.resolve(prompt.session_key, prompt.ref, choice)
                if shown:
                    await self.send(prompt.room_id, shown)
        except Exception:
            logger.warning("prompt message=%s could not be resolved in Hermes", prompt.message_id, exc_info=True)

    # ── Control: what a human does to a running turn ────────────────────────

    def _control_event(self, room_id: str, text: str) -> MessageEvent | None:
        """A command for the room's running turn, or None when no turn is
        running there: handed to Hermes then, it would start a turn that
        belongs to no delivery."""
        source = self._room_sources.get(room_id)
        if source is None or self._open_turns.for_room(room_id) is None:
            return None
        return MessageEvent(text=text, message_type=MessageType.TEXT, source=source, allow_gateway_control=True)

    async def _stop_room(self, name: str, room_id: str) -> None:
        """Hermes's own /stop: it interrupts the agent and its tools, and the
        turn then ends through the processing hooks like any other."""
        event = self._control_event(room_id, "/stop")
        if event is None:
            logger.info("%s for room %s: no turn is running", name, room_id)
            return
        logger.info("%s for room %s: stopping the running turn", name, room_id)
        # The delivery being stopped, read now: once it closes, the room's
        # open delivery is whichever was parked behind it.
        delivery_id = self._open_turns.for_room(room_id)
        await self.handle_message(event)
        # A stop that lands before the turn's task has run cancels it with no
        # processing hook called, so nothing would ever close the delivery.
        if (delivery_id in self._open_turns.all() and delivery_id not in self._turn_tasks
                and delivery_id not in self._awaiting_resume
                and self._event_session_key(event) not in self._session_tasks):
            logger.info("delivery=%s was stopped before its turn began", delivery_id)
            self._close_turn(delivery_id, "done")

    async def _steer_room(self, room_id: str, message: dict) -> None:
        if self._dedup.is_duplicate(message["id"]):
            logger.info("dropping repeat steer %s", message["id"])
            return
        event = self._control_event(room_id, "/steer " + (message.get("text") or ""))
        if event is None:
            logger.info("steer %s for room %s: no turn is running", message["id"], room_id)
            return
        logger.info("steer %s for room %s", message["id"], room_id)
        await self.handle_message(event)

    async def _on_delivery(self, data: dict) -> None:
        message = data["message"]
        if self._dedup.is_duplicate(message["id"]):
            logger.info("dropping repeat delivery of message %s", message["id"])
            return
        room_id = data["room"]["id"]
        if message["id"] in self._awaiting_resume:
            # A repeat of the open turn itself; it is not handed over again.
            logger.info("dropping redelivery of awaiting message %s", message["id"])
            return
        if self._gateway_stopping():
            # Hermes refuses new work while it stops. Unrecorded and
            # unreported, the delivery stays `delivered` on the server, which
            # hands it over again on the next connect.
            logger.info("leaving delivery of message %s for the next start; the gateway is stopping", message["id"])
            return
        self._open_turns.add(message["id"], room_id)
        event = self.event_from_delivery(data)
        self._room_sources[room_id] = event.source
        if room_id in self._room_busy_rooms:
            self._parked.setdefault(room_id, deque()).append(event)
            return
        await self._run_room_turn(event, room_id)

    async def _run_room_turn(self, event: MessageEvent, room_id: str) -> None:
        self._room_busy_rooms.add(room_id)
        await self.handle_message(event)
        if not event._gateway_accepted:
            logger.warning("Hermes did not accept delivery of message %s", event.message_id)
            if event.message_id:
                self._close_turn(event.message_id, "failed")

    async def _resume_grace_watch(self) -> None:
        try:
            await asyncio.sleep(self.RESUME_GRACE_SECONDS)
        except asyncio.CancelledError:
            return
        for delivery_id in list(self._awaiting_resume):
            self._awaiting_resume.discard(delivery_id)
            self._close_turn(delivery_id, "failed")

    async def handle_message(self, event: MessageEvent) -> None:
        """Attach an interrupted turn's delivery to Hermes's resume event, so
        its hooks report against that delivery and its reply quotes it."""
        if event.internal and not event.message_id:
            delivery_id = self._open_turns.for_room(event.source.chat_id)
            if delivery_id in self._awaiting_resume:
                self._awaiting_resume.discard(delivery_id)
                # This delivery came from the record, not the stream.
                self._room_sources[event.source.chat_id] = event.source
                event.metadata["hermes_chat_delivery"] = delivery_id
                event.reply_anchor_override = delivery_id
                await super().handle_message(event)
                if not event._gateway_accepted:
                    self._close_turn(delivery_id, "failed")
                return
        await super().handle_message(event)

    async def cancel_background_tasks(self) -> None:
        """Shutdown: a CANCELLED turn is left open for the next start."""
        self._shutting_down = True
        await super().cancel_background_tasks()

    def _delivery_for(self, event: MessageEvent) -> str | None:
        """The delivery this event's turn belongs to, or None. A resume event
        carries its delivery in metadata; a fresh delivery is its own id."""
        metadata_delivery = (event.metadata or {}).get("hermes_chat_delivery")
        if metadata_delivery:
            return metadata_delivery
        if event.message_id and event.message_id in self._open_turns.all():
            return event.message_id
        return None

    async def on_processing_start(self, event: MessageEvent) -> None:
        delivery_id = self._delivery_for(event)
        if delivery_id is None:
            return
        logger.info("on_processing_start delivery=%s room=%s", delivery_id, event.source.chat_id)
        self._room_sessions[event.source.chat_id] = self._event_session_key(event)
        self._reporter.submit(delivery_id, "working")
        self._turn_tasks[delivery_id] = asyncio.current_task()

    async def on_processing_complete(self, event: MessageEvent, outcome: ProcessingOutcome) -> None:
        delivery_id = self._delivery_for(event)
        if delivery_id is None:
            return
        logger.info(
            "on_processing_complete delivery=%s room=%s outcome=%s",
            delivery_id, event.source.chat_id, outcome.value,
        )
        if self._interrupted_by_shutdown(event, outcome):
            # The interrupted turn continues after the next start; leave the
            # delivery open in the record.
            logger.info("delivery=%s interrupted by the gateway stopping; left open for resume", delivery_id)
            return
        if self._turn_still_to_come(event):
            logger.info("delivery=%s was queued by Hermes, not run; its turn is still to come", delivery_id)
            return
        self._turn_outcomes[delivery_id] = outcome
        task = self._turn_tasks.get(delivery_id)
        if task is not None and delivery_id not in self._turn_callbacks:
            self._turn_callbacks.add(delivery_id)
            task.add_done_callback(functools.partial(self._on_turn_task_done, delivery_id))

    def _gateway_stopping(self) -> bool:
        return bool(getattr(getattr(self, "gateway_runner", None), "_draining", False))

    def _interrupted_by_shutdown(self, event: MessageEvent, outcome: ProcessingOutcome) -> bool:
        """True when the turn did not finish but was cut short by the gateway
        stopping. Hermes cancels some such turns and ends others as a success
        with the session marked for resume; either way it resumes the session
        after the next start."""
        if self._shutting_down and outcome is ProcessingOutcome.CANCELLED:
            return True
        if not self._gateway_stopping():
            return False
        try:
            entry = self.gateway_runner.session_store.lookup_by_session_key(self._event_session_key(event))
        except Exception:
            logger.debug("could not read the session's resume flag", exc_info=True)
            return False
        return bool(getattr(entry, "resume_pending", False))

    def _turn_still_to_come(self, event: MessageEvent) -> bool:
        """True when Hermes ran the processing hooks around an event it only
        queued. It dispatches the same event again, and that pass is the turn.
        Two places hold such an event: the gateway's startup queue, while it is
        still starting, and the session's pending slot, when the handler found
        the session busy elsewhere (Hermes's own resume after a restart does)."""
        queue = getattr(getattr(self, "gateway_runner", None), "_startup_restore_queue", None) or ()
        if any(queued is event for queued in queue):
            return True
        pending = self._pending_messages.get(self._event_session_key(event))
        if pending is None:
            return False
        # The base adapter's own test for "the event just dispatched came
        # straight back": the same id, or for an id-less event the same time.
        if pending.message_id or event.message_id:
            return pending.message_id == event.message_id
        return pending.timestamp == event.timestamp

    def _on_turn_task_done(self, delivery_id: str, task: asyncio.Task) -> None:
        outcome = self._turn_outcomes.pop(delivery_id, None)
        self._turn_tasks.pop(delivery_id, None)
        if outcome is None:
            return
        state = "failed" if outcome is ProcessingOutcome.FAILURE else "done"
        self._close_turn(delivery_id, state)

    def _close_turn(self, delivery_id: str, state: str) -> None:
        """Report a turn finished and let the room's next delivery in. Calling
        it twice for one delivery is a no-op: the record no longer has it."""
        room_id = self._open_turns.all().get(delivery_id)
        self._open_turns.remove(delivery_id)
        if room_id is None:
            return
        self._reporter.submit(delivery_id, state)
        # Whatever the turn was still asking, nobody is waiting on it now.
        for prompt in self._prompts.take_room(room_id, ("approval", "clarify")):
            task = asyncio.create_task(self._expire_prompt(prompt))
            self._expiring.add(task)
            task.add_done_callback(self._expiring.discard)
        self._room_busy_rooms.discard(room_id)
        parked = self._parked.get(room_id)
        if parked:
            next_event = parked.popleft()
            if not parked:
                self._parked.pop(room_id, None)
            asyncio.create_task(self._run_room_turn(next_event, room_id))

    def event_from_delivery(self, data: dict) -> MessageEvent:
        """Turn a DeliveryEvent body into the MessageEvent Hermes sees."""
        message = data["message"]
        room = data["room"]
        author = message["author"]
        is_dm = room["flavor"] == "dm"
        if author is None:
            user_id, user_name, is_bot = None, REMOVED_AUTHOR_NAME, False
        else:
            user_id = author.get("id")
            user_name = author.get("name")
            is_bot = author["is_bot"]
        source = self.build_source(
            chat_id=room["id"],
            chat_name=room.get("topic") or (user_name if is_dm else None),
            chat_type="dm" if is_dm else "group",
            user_id=user_id,
            user_name=user_name,
            is_bot=is_bot,
            thread_id=None if is_dm else room["id"],
        )
        context = data.get("context") or []
        channel_context = None
        if context:
            channel_context = "\n".join(
                f"{self._author_name(src['author'])}: {src['text']}" for src in context)
        return MessageEvent(
            text=message.get("text") or "",
            message_type=MessageType.TEXT,
            source=source,
            message_id=message["id"],
            reply_to_message_id=message.get("reply_to_message_id"),
            reply_to_text=message.get("reply_to_text"),
            reply_to_author_name=message.get("reply_to_author_name"),
            channel_prompt=data.get("note") or None,
            channel_context=channel_context,
            allow_gateway_control=author is not None and author.get("kind") == "human",
        )

    @staticmethod
    def _author_name(author) -> str:
        if author is not None:
            return author.get("name") or REMOVED_AUTHOR_NAME
        return REMOVED_AUTHOR_NAME


async def _standalone_send(pconfig, chat_id: str, message: str, *, thread_id=None, media_files=None,
                           force_document: bool = False) -> dict:
    """Post to a room with no connected adapter. Hermes calls this when a
    scheduled job runs in a process that is not the gateway (`hermes cron
    tick`, the desktop app's scheduler). Text only: a file would need the
    adapter's path check and upload, so a job's files are not sent from here."""
    extra = getattr(pconfig, "extra", None) or {}
    url = extra_or_secret(extra, "url", "HERMES_CHAT_URL")
    token = extra_or_secret(extra, "token", "HERMES_CHAT_TOKEN")
    if not (url and token):
        return {"error": "Hermes Chat is not set up for this profile: HERMES_CHAT_URL and HERMES_CHAT_TOKEN are needed"}
    text = _outgoing(message or "")
    if not text.strip():
        if media_files:
            return {"error": "Hermes Chat cannot send files from a scheduled job run outside the gateway"}
        return {"error": "there is nothing to send"}
    client = ChatClient(url, token)
    try:
        message_id = await client.post_message(chat_id, text)
    except ApiError as exc:
        logger.info("standalone send room=%s refused: %s", chat_id, exc.code)
        return {"error": f"{exc.code}: {exc.message}"}
    except Unreachable as exc:
        logger.warning("standalone send room=%s unreachable: %s", chat_id, exc)
        return {"error": f"the chat server could not be reached: {exc}"}
    finally:
        await client.aclose()
    logger.info("standalone send room=%s message=%s", chat_id, message_id)
    return {"success": True, "message_id": message_id}


def check_requirements() -> bool:
    return bool(get_scoped_secret("HERMES_CHAT_URL", "") and get_scoped_secret("HERMES_CHAT_TOKEN", ""))


def validate_config(config) -> bool:
    return bool(
        extra_or_secret(config.extra, "url", "HERMES_CHAT_URL")
        and extra_or_secret(config.extra, "token", "HERMES_CHAT_TOKEN")
    )


def _env_enablement() -> dict | None:
    url = str(get_scoped_secret("HERMES_CHAT_URL", "") or "").strip()
    token = str(get_scoped_secret("HERMES_CHAT_TOKEN", "") or "").strip()
    if not (url and token):
        return None
    seed = seed_extra_from_env(
        (("HERMES_CHAT_URL", "url", None), ("HERMES_CHAT_TOKEN", "token", None)),
        home_env="HERMES_CHAT_HOME_ROOM",
    )
    return {"url": url, "token": token, **seed}


def register(ctx) -> None:
    from .note_tool import register_note_tool

    hooks.register_hooks(ctx)
    register_note_tool(ctx)
    ctx.register_platform(
        name="hermes_chat",
        label="Hermes Chat",
        adapter_factory=HermesChatAdapter,
        check_fn=check_requirements,
        validate_config=validate_config,
        required_env=["HERMES_CHAT_URL", "HERMES_CHAT_TOKEN"],
        env_enablement_fn=_env_enablement,
        cron_deliver_env_var="HERMES_CHAT_HOME_ROOM",
        standalone_sender_fn=_standalone_send,
        allowed_users_env="HERMES_CHAT_ALLOWED_USERS",
        allow_all_env="HERMES_CHAT_ALLOW_ALL_USERS",
        max_message_length=0,
        emoji="💬",
        platform_hint=(
            "You are chatting through Hermes Chat. Markdown is rendered, so "
            "long, complete answers are fine; the reader usually comes back "
            "later rather than watching live."
        ),
    )