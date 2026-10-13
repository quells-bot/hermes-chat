import pytest

from stubserver import StubServer


@pytest.fixture
async def stub():
    server = StubServer()
    await server.start()
    yield server
    await server.stop()


def _human(hid):
    return {"kind": "human", "id": hid, "name": f"Human {hid}", "is_bot": False}


def _agent(aid):
    return {"kind": "agent", "id": aid, "name": f"Agent {aid}", "is_bot": True}


def _message(message_id, room_id, author, text, reply):
    msg = {
        "id": message_id, "room_id": room_id, "author": author, "text": text,
        "reply_to_message_id": None, "reply_to_text": None, "reply_to_author_name": None,
        "created_at": "2026-10-13T12:00:00.000Z",
    }
    if reply:
        msg.update(reply)
    return msg


_NO_AUTHOR = object()


def _delivery(message_id="M1", room_id="R1", flavor="dm", text="hi", author=_NO_AUTHOR, note=None,
              context=(), reply=None):
    """A DeliveryEvent body as docs/openapi.yaml defines it."""
    if author is _NO_AUTHOR:
        author = _human("H1")
    room = {"id": room_id, "flavor": flavor, "topic": f"Room {room_id}", "humans": [], "paused": False}
    ctx = []
    for name, ctext in context:
        is_bot = name == "bot"
        ctx.append(_message(
            f"{message_id}-c{len(ctx)}", room_id,
            {"kind": "agent" if is_bot else "human", "id": "X" + str(len(ctx)), "name": name, "is_bot": is_bot},
            ctext, None))
    return {"message": _message(message_id, room_id, author, text, reply), "room": room,
            "context": ctx, "note": note}


@pytest.fixture
def delivery():
    return _delivery


@pytest.fixture
def human():
    return _human


@pytest.fixture
def agent():
    return _agent


@pytest.fixture
def resume_event():
    """What Hermes sends to resume an interrupted session: an internal
    MessageEvent with empty text, no message_id, for a room."""
    def build(adapter, room_id):
        from gateway.platforms.event import MessageEvent, MessageType
        return MessageEvent(
            text="", message_type=MessageType.TEXT,
            source=adapter.build_source(chat_id=room_id, chat_name=room_id),
            internal=True,
        )

    return build


@pytest.fixture
def adapter_factory(stub, tmp_path, monkeypatch):
    """Build a HermesChatAdapter pointed at the stub, its open-turn record
    under tmp_path. Adapter tests need the Hermes checkout (they skip at
    importorskip); the imports here are lazy so other modules do not."""
    from gateway.platform_registry import PlatformEntry, platform_registry
    if not platform_registry.is_registered("hermes_chat"):
        platform_registry.register(PlatformEntry(
            name="hermes_chat", label="Hermes Chat",
            adapter_factory=lambda config: None, check_fn=lambda: True, source="bundled"))
    import gateway.status as status_mod
    monkeypatch.setattr(status_mod, "publish_runtime_status", lambda **kw: 0)
    from hermes_chat import adapter as adapter_mod
    monkeypatch.setattr(adapter_mod, "get_hermes_home", lambda: tmp_path)

    def build(url=None, token="hca_test_token"):
        from gateway.config import PlatformConfig
        from hermes_chat.adapter import HermesChatAdapter
        return HermesChatAdapter(PlatformConfig(extra={"url": url or stub.url, "token": token}))

    return build