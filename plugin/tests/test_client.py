import pytest

from hermes_chat.client import ApiError, ChatClient, Unauthorized, Unreachable


async def test_me_sends_bearer_token(stub):
    stub.script("GET", "/agent/me", (200, {"id": "A1", "handle": "h", "display_name": "H"}))
    c = ChatClient(stub.url, "hca_secret")
    try:
        assert (await c.me())["id"] == "A1"
    finally:
        await c.aclose()
    assert stub.requests[0].headers["authorization"] == "Bearer hca_secret"


async def test_post_message_returns_id_and_sends_reply_to(stub):
    stub.script("POST", "/agent/rooms/R1/messages", (201, {"id": "M9"}))
    c = ChatClient(stub.url, "t")
    try:
        assert await c.post_message("R1", "hi", reply_to_id="M1") == "M9"
    finally:
        await c.aclose()
    assert stub.requests[0].json == {"text": "hi", "reply_to_id": "M1"}


async def test_post_message_omits_reply_to_when_none(stub):
    stub.script("POST", "/agent/rooms/R1/messages", (201, {"id": "M9"}))
    c = ChatClient(stub.url, "t")
    try:
        await c.post_message("R1", "hi")
    finally:
        await c.aclose()
    assert stub.requests[0].json == {"text": "hi"}


async def test_edit_message(stub):
    stub.script("PUT", "/agent/messages/M9", (204,))
    c = ChatClient(stub.url, "t")
    try:
        await c.edit_message("M9", "x", True)
    finally:
        await c.aclose()
    r = stub.requests[0]
    assert (r.method, r.path) == ("PUT", "/agent/messages/M9")
    assert r.json == {"text": "x", "final": True}


@pytest.mark.parametrize("state", ["working", "done", "failed"])
async def test_report_posts_to_the_state_route(stub, state):
    stub.script("POST", f"/agent/deliveries/D1/{state}", (204,))
    c = ChatClient(stub.url, "t")
    try:
        await c.report("D1", state)
    finally:
        await c.aclose()
    assert stub.requests[0].path == f"/agent/deliveries/D1/{state}"


async def test_409_paused_is_api_error(stub):
    stub.script("POST", "/agent/rooms/R1/messages", (409, {"error": {"code": "paused", "message": "room is paused"}}))
    c = ChatClient(stub.url, "t")
    try:
        with pytest.raises(ApiError) as e:
            await c.post_message("R1", "hi")
        assert (e.value.status, e.value.code) == (409, "paused")
    finally:
        await c.aclose()


async def test_401_is_unauthorized(stub):
    stub.script("GET", "/agent/me", (401, {"error": {"code": "unauthenticated", "message": "nope"}}))
    c = ChatClient(stub.url, "t")
    try:
        with pytest.raises(Unauthorized):
            await c.me()
    finally:
        await c.aclose()


async def test_500_is_unreachable(stub):
    stub.script("GET", "/agent/me", (500, {"error": {"code": "unavailable", "message": "boom"}}))
    c = ChatClient(stub.url, "t")
    try:
        with pytest.raises(Unreachable):
            await c.me()
    finally:
        await c.aclose()


async def test_refused_connection_is_unreachable():
    c = ChatClient("http://127.0.0.1:1", "t")
    try:
        with pytest.raises(Unreachable):
            await c.me()
    finally:
        await c.aclose()


async def test_token_not_in_exception_text(stub):
    one = ChatClient(stub.url, "hca_secret")
    try:
        stub.script("GET", "/agent/me", (500, {"error": {"code": "unavailable", "message": "boom"}}))
        with pytest.raises(Unreachable) as e:
            await one.me()
        assert "hca_secret" not in str(e.value)
        stub.script("GET", "/agent/me", (401, {"error": {"code": "unauthenticated", "message": "nope"}}))
        with pytest.raises(Unauthorized) as e:
            await one.me()
        assert "hca_secret" not in str(e.value)
        stub.script("GET", "/agent/me", (409, {"error": {"code": "conflict", "message": "dup"}}))
        with pytest.raises(ApiError) as e:
            await one.me()
        assert "hca_secret" not in str(e.value)
    finally:
        await one.aclose()


async def test_open_events_sends_cursor_and_yields_chunks(stub):
    stub.events(b"event: ready\ndata: {\"cursor\":5}\n\n")
    c = ChatClient(stub.url, "t")
    async with c.open_events(5) as chunks:
        first = await anext(chunks)
    assert first.startswith(b"event: ready")
    assert stub.requests[0].headers["last-event-id"] == "5"
    await c.aclose()


async def test_open_events_without_cursor_sends_no_header(stub):
    stub.events(b"event: ready\ndata: {\"cursor\":1}\n\n")
    c = ChatClient(stub.url, "t")
    async with c.open_events(None) as chunks:
        await anext(chunks)
    assert "last-event-id" not in stub.requests[0].headers
    await c.aclose()


async def test_open_events_401_raises_unauthorized_on_enter(stub):
    stub.script("GET", "/agent/events", (401, {"error": {"code": "unauthenticated", "message": "revoked"}}))
    c = ChatClient(stub.url, "t")
    with pytest.raises(Unauthorized):
        async with c.open_events(None) as chunks:
            await anext(chunks)
    await c.aclose()

async def test_start_tool_posts_the_step_and_returns_the_activity(stub):
    stub.script("POST", "/agent/deliveries/M1/tools", (201, {"id": "ACT1", "kind": "tool"}))
    c = ChatClient(stub.url, "t")
    try:
        assert await c.start_tool("M1", "terminal", "echo hi", '{"command": "echo hi"}') == "ACT1"
    finally:
        await c.aclose()
    assert stub.requests[0].json == {"tool": "terminal", "preview": "echo hi", "arguments": '{"command": "echo hi"}'}


async def test_finish_tool_puts_duration_and_result(stub):
    stub.script("PUT", "/agent/activities/ACT1", (200, {"id": "ACT1"}))
    c = ChatClient(stub.url, "t")
    try:
        await c.finish_tool("ACT1", 12, True)
    finally:
        await c.aclose()
    assert stub.requests[0].json == {"duration_ms": 12, "succeeded": True}


async def test_tool_calls_raise_api_error_when_refused(stub):
    paused = (409, {"error": {"code": "paused", "message": "the room is paused"}})
    stub.script("POST", "/agent/deliveries/M1/tools", paused)
    stub.script("PUT", "/agent/activities/ACT1", paused)
    c = ChatClient(stub.url, "t")
    try:
        with pytest.raises(ApiError) as started:
            await c.start_tool("M1", "terminal", "", "{}")
        with pytest.raises(ApiError) as finished:
            await c.finish_tool("ACT1", 1, False)
    finally:
        await c.aclose()
    assert started.value.status == 409 and finished.value.status == 409
