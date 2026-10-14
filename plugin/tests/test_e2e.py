import asyncio

import pytest

pytest.importorskip("gateway.platforms.base", reason="needs the Hermes checkout; set HERMES_SRC")


async def wait_for(predicate, timeout=10.0):
    deadline = asyncio.get_event_loop().time() + timeout
    while asyncio.get_event_loop().time() < deadline:
        if predicate():
            return True
        await asyncio.sleep(0.1)
    return False


def agent_messages(server, room):
    return [m for m in server.messages(room) if (m.get("author") or {}).get("kind") == "agent"]


async def test_human_posts_and_agent_replies(chat_server, e2e_adapter):
    async def handler(event):
        return "pong"

    e2e_adapter.set_message_handler(handler)
    room = chat_server.open_dm()
    chat_server.post(room, "ping")
    assert await wait_for(lambda: any(m["text"] == "pong" for m in agent_messages(chat_server, room)))
    assert await wait_for(lambda: chat_server.delivery_state(room, "ping") == "done")


async def test_streamed_reply_is_final_and_not_streaming(chat_server, e2e_adapter):
    a = e2e_adapter

    async def handler(event):
        res = await a.send(event.source.chat_id, "a")
        mid = res.message_id
        await a.edit_message(event.source.chat_id, mid, "ab")
        await a.edit_message(event.source.chat_id, mid, "abc", finalize=True)

    a.set_message_handler(handler)
    room = chat_server.open_dm()
    chat_server.post(room, "ping")
    assert await wait_for(lambda: any(m["text"] == "abc" for m in agent_messages(chat_server, room)))
    abc = [m for m in agent_messages(chat_server, room) if m["text"] == "abc"]
    assert abc and abc[-1]["streaming"] is False


async def test_second_message_is_answered_after_the_first(chat_server, e2e_adapter):
    async def handler(event):
        return event.text

    e2e_adapter.set_message_handler(handler)
    room = chat_server.open_dm()
    chat_server.post(room, "one")
    chat_server.post(room, "two")
    assert await wait_for(lambda: any(m["text"] == "one" for m in agent_messages(chat_server, room)))
    assert await wait_for(lambda: any(m["text"] == "two" for m in agent_messages(chat_server, room)))
    replies = [m["text"] for m in agent_messages(chat_server, room)]
    assert replies.index("one") < replies.index("two")
    assert await wait_for(lambda: chat_server.delivery_state(room, "one") == "done")
    assert await wait_for(lambda: chat_server.delivery_state(room, "two") == "done")


async def test_message_sent_while_agent_is_offline_is_answered_on_connect(chat_server, e2e_adapter_factory):
    async def handler(event):
        return "early reply"

    a = await e2e_adapter_factory(handler, connect=False)
    room = chat_server.open_dm()
    chat_server.post(room, "early")
    assert await a.connect() is True
    assert await wait_for(lambda: any(m["text"] == "early reply" for m in agent_messages(chat_server, room)))
    assert await wait_for(lambda: chat_server.delivery_state(room, "early") == "done")


async def test_server_restart_mid_turn(chat_server, e2e_adapter_factory):
    go = asyncio.Event()

    async def handler(event):
        await go.wait()
        return "late reply"

    await e2e_adapter_factory(handler)
    room = chat_server.open_dm()
    chat_server.post(room, "ping")
    assert await wait_for(lambda: chat_server.delivery_state(room, "ping") == "working")
    chat_server.stop()
    chat_server.start()
    go.set()
    assert await wait_for(lambda: chat_server.delivery_state(room, "ping") == "done")
    assert await wait_for(lambda: any(m["text"] == "late reply" for m in agent_messages(chat_server, room)))


async def test_adapter_restart_mid_turn_resumes(chat_server, e2e_adapter_factory, resume_event):
    blocked = asyncio.Event()

    async def block(event):
        await blocked.wait()
        return None

    a1 = await e2e_adapter_factory(block)
    room = chat_server.open_dm()
    chat_server.post(room, "ping")
    assert await wait_for(lambda: chat_server.delivery_state(room, "ping") == "working")
    await a1.cancel_background_tasks()
    await a1.disconnect()

    async def handler(event):
        return "resumed" if not event.message_id else event.text

    a2 = await e2e_adapter_factory(handler)
    await a2.handle_message(resume_event(a2, room))
    assert await wait_for(lambda: any(m["text"] == "resumed" for m in agent_messages(chat_server, room)))
    assert await wait_for(lambda: chat_server.delivery_state(room, "ping") == "done")
    chat_server.post(room, "next")
    assert await wait_for(lambda: any(m["text"] == "next" for m in agent_messages(chat_server, room)))
    assert await wait_for(lambda: chat_server.delivery_state(room, "next") == "done")


async def test_reply_into_a_paused_room_closes_the_turn(chat_server, e2e_adapter_factory):
    go = asyncio.Event()

    async def handler(event):
        await go.wait()
        return event.text

    await e2e_adapter_factory(handler)
    room = chat_server.open_dm()
    chat_server.post(room, "ping")
    assert await wait_for(lambda: chat_server.delivery_state(room, "ping") == "working")
    chat_server.pause(room)
    go.set()
    assert await wait_for(lambda: chat_server.delivery_state(room, "ping") in ("done", "failed"))
    assert not any(m["text"] == "ping" for m in agent_messages(chat_server, room))
    chat_server.resume(room)
    chat_server.post(room, "next")
    assert await wait_for(lambda: any(m["text"] == "next" for m in agent_messages(chat_server, room)))
    assert await wait_for(lambda: chat_server.delivery_state(room, "next") == "done")

async def test_a_file_the_agent_sends_can_be_downloaded_by_the_human(chat_server, e2e_adapter, tmp_path, monkeypatch):
    a = e2e_adapter
    monkeypatch.setattr(type(a), "validate_media_delivery_path", staticmethod(lambda path, session_key="": path))
    report = tmp_path / "report.txt"
    report.write_bytes(b"the whole report\n")

    async def handler(event):
        result = await a.send_document(event.source.chat_id, str(report), caption="here it is")
        assert result.success, result.error

    a.set_message_handler(handler)
    room = chat_server.open_dm()
    chat_server.post(room, "send me the report")
    assert await wait_for(lambda: any(m["attachments"] for m in agent_messages(chat_server, room)))
    message = next(m for m in agent_messages(chat_server, room) if m["attachments"])
    assert message["text"] == "here it is"
    (attachment,) = message["attachments"]
    assert attachment["name"] == "report.txt" and attachment["size"] == len(b"the whole report\n")
    download = chat_server.human.get(f"/api/rooms/{room}/attachments/{attachment['id']}")
    assert download.status_code == 200 and download.content == b"the whole report\n"
    assert await wait_for(lambda: chat_server.delivery_state(room, "send me the report") == "done")
