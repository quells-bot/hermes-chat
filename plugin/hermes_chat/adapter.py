"""The Hermes Chat platform adapter: stream events become Hermes MessageEvents
and Hermes's calls become chat-server client calls.

Only this module and `probe.py` import Hermes; the client modules
(`sse.py`, `client.py`, `stream.py`, `reports.py`, `record.py`, `checks.py`)
import nothing from it.
"""

from __future__ import annotations

import asyncio
import contextlib
import functools
import hashlib
import logging
import re
from collections import deque

from gateway.config import Platform
from gateway.platforms._shared import extra_or_secret, get_scoped_secret, seed_extra_from_env
from gateway.platforms.base import BasePlatformAdapter, SendResult
from gateway.platforms.event import MessageEvent, MessageType, ProcessingOutcome
from gateway.platforms.helpers import MessageDeduplicator
from hermes_constants import get_hermes_home

from .client import ApiError, ChatClient, Unauthorized, Unreachable
from .checks import display_warnings
from .record import OpenTurns
from .reports import TurnReporter
from .stream import EventStream

logger = logging.getLogger("hermes_chat.adapter")

PLATFORM = "hermes_chat"
REMOVED_AUTHOR_NAME = "removed user"
MAX_TEXT_BYTES = 262_144  # the server's MessageText limit, in bytes
# Hermes ends in-progress text with this cursor (its `streaming.cursor`
# default). The server already says whether a message is still streaming.
_STREAMING_CURSOR = re.compile(r"\s*▉\Z")


def _without_cursor(content: str) -> str:
    stripped = _STREAMING_CURSOR.sub("", content)
    # Text that is only the cursor stays as it is: the server refuses a blank
    # message, and Hermes needs the message id to edit.
    return stripped if stripped.strip() else content


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
        self._lock_held = False
        # The room gate: a room is busy from the moment a delivery for it is
        # recorded until that delivery's turn is closed.
        self._room_busy_rooms: set[str] = set()
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
        try:
            message_id = await self._client.post_message(chat_id, _without_cursor(content), reply_to_id=reply_to)
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
            await self._client.edit_message(message_id, _without_cursor(content), finalize)
        except ApiError as exc:
            self._log_send_error("edit_message", chat_id, exc)
            return SendResult(success=False, error=f"{exc.code}: {exc.message}")
        except Unreachable as exc:
            logger.warning("edit_message room=%s message=%s unreachable: %s", chat_id, message_id, exc)
            return SendResult(success=False, error=str(exc), retryable=True)
        logger.info("edit_message room=%s message=%s finalize=%s", chat_id, message_id, finalize)
        return SendResult(success=True)

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
        self._mark_connected()
        return True

    async def disconnect(self) -> None:
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

    async def get_chat_info(self, chat_id: str) -> dict:
        return {"name": chat_id, "type": "dm"}

    # ── Inbound ─────────────────────────────────────────────────────────────

    async def _on_stream_event(self, name: str, data: dict) -> None:
        if name == "delivery":
            await self._on_delivery(data)
            return
        room_id = data.get("room_id") or (data.get("room") or {}).get("id")
        logger.info("ignoring %s for room %s (stage 1)", name, room_id)

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
    from .probe import register_probe

    register_probe(ctx)
    ctx.register_platform(
        name="hermes_chat",
        label="Hermes Chat",
        adapter_factory=HermesChatAdapter,
        check_fn=check_requirements,
        validate_config=validate_config,
        required_env=["HERMES_CHAT_URL", "HERMES_CHAT_TOKEN"],
        env_enablement_fn=_env_enablement,
        cron_deliver_env_var="HERMES_CHAT_HOME_ROOM",
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