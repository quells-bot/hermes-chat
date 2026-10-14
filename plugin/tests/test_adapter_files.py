import pytest

pytest.importorskip("gateway.platforms.base", reason="needs the Hermes checkout; set HERMES_SRC")

from hermes_chat import adapter as adapter_mod  # noqa: E402

UPLOAD = "/agent/rooms/R1/attachments"
POST = "/agent/rooms/R1/messages"
PAUSED = (409, {"error": {"code": "paused", "message": "the room is paused"}})


@pytest.fixture
def a(adapter_factory, monkeypatch):
    """An adapter that sends without connecting, with Hermes's path check
    replaced by one that lets every path through."""
    adapter = adapter_factory()
    monkeypatch.setattr(type(adapter), "validate_media_delivery_path", staticmethod(lambda path, session_key="": path))
    return adapter


@pytest.fixture
def report(tmp_path):
    path = tmp_path / "quarterly report.pdf"
    path.write_bytes(b"%PDF-1.7 the report")
    return path


def accept(stub):
    stub.script("POST", UPLOAD, (201, {"id": "A1"}))
    stub.script("POST", POST, (201, {"id": "M9"}))


def by_path(stub, path):
    return [r for r in stub.requests if r.path == path]


async def test_a_document_is_uploaded_and_posted_with_its_caption(a, stub, report):
    accept(stub)
    result = await a.send_document("R1", str(report), caption="the report ▉", reply_to="M1")
    assert result.success and result.message_id == "M9"
    (upload,) = by_path(stub, UPLOAD)
    assert upload.body == b"%PDF-1.7 the report" and "quarterly" in upload.query
    (post,) = by_path(stub, POST)
    assert post.json == {"text": "the report", "reply_to_id": "M1", "attachment_ids": ["A1"]}
    assert [r.path for r in stub.requests] == [UPLOAD, POST]
    await a._client.aclose()


async def test_a_file_with_no_caption_is_posted_with_blank_text(a, stub, report):
    accept(stub)
    await a.send_document("R1", str(report))
    assert by_path(stub, POST)[0].json == {"text": "", "attachment_ids": ["A1"]}
    await a._client.aclose()


async def test_the_given_file_name_is_the_uploads_name(a, stub, report):
    accept(stub)
    await a.send_document("R1", str(report), file_name="re\x00port.pdf")
    assert by_path(stub, UPLOAD)[0].query == "name=report.pdf"
    await a._client.aclose()


@pytest.mark.parametrize("method", ["send_image_file", "send_voice", "send_video"])
async def test_images_voice_and_video_are_sent_the_same_way(a, stub, report, method):
    accept(stub)
    result = await getattr(a, method)("R1", str(report), caption="look")
    assert result.success and result.message_id == "M9"
    assert by_path(stub, POST)[0].json == {"text": "look", "attachment_ids": ["A1"]}
    await a._client.aclose()


async def test_a_path_hermes_refuses_is_not_sent(a, stub, report, monkeypatch):
    monkeypatch.setattr(type(a), "validate_media_delivery_path", staticmethod(lambda path, session_key="": None))
    result = await a.send_document("R1", str(report))
    assert result.success is False and stub.requests == []
    await a._client.aclose()


async def test_a_missing_file_is_not_sent(a, stub, tmp_path):
    result = await a.send_document("R1", str(tmp_path / "nothing-here.pdf"))
    assert result.success is False and stub.requests == []
    await a._client.aclose()


async def test_an_empty_file_is_not_sent(a, stub, tmp_path):
    (tmp_path / "empty.txt").write_bytes(b"")
    result = await a.send_document("R1", str(tmp_path / "empty.txt"))
    assert result.success is False and stub.requests == []
    await a._client.aclose()


async def test_a_file_over_the_limit_is_not_sent(a, stub, report, monkeypatch):
    monkeypatch.setattr(adapter_mod, "MAX_ATTACHMENT_BYTES", 10)
    result = await a.send_document("R1", str(report))
    assert result.success is False and "25 MiB" in result.error and stub.requests == []
    await a._client.aclose()


async def test_the_limit_is_25_mebibytes():
    assert adapter_mod.MAX_ATTACHMENT_BYTES == 25 * 1024 * 1024


async def test_an_upload_the_room_refuses_posts_no_message(a, stub, report):
    stub.script("POST", UPLOAD, PAUSED)
    result = await a.send_document("R1", str(report), caption="the report")
    assert result.success is False
    assert [r.path for r in stub.requests] == [UPLOAD]
    await a._client.aclose()


async def test_an_unreachable_server_is_a_retryable_failure(adapter_factory, report, monkeypatch):
    a = adapter_factory(url="http://127.0.0.1:9")
    monkeypatch.setattr(type(a), "validate_media_delivery_path", staticmethod(lambda path, session_key="": path))
    result = await a.send_document("R1", str(report))
    assert result.success is False and result.retryable is True
    await a._client.aclose()


async def test_the_log_names_the_room_and_attachment_but_not_the_file(a, stub, report, caplog):
    accept(stub)
    caplog.set_level("DEBUG", logger="hermes_chat")
    await a.send_document("R1", str(report), caption="private caption")
    ours = "\n".join(r.getMessage() for r in caplog.records if r.name.startswith("hermes_chat"))
    assert "room=R1" in ours and "attachment=A1" in ours
    assert "quarterly" not in ours and "private" not in ours
    await a._client.aclose()


async def test_an_image_url_is_still_posted_as_text(a, stub):
    stub.script("POST", POST, (201, {"id": "M9"}))
    await a.send_image("R1", "https://example.com/cat.png", caption="a cat")
    assert by_path(stub, POST)[0].json == {"text": "a cat\nhttps://example.com/cat.png"}
    await a._client.aclose()
