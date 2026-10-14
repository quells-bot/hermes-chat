from hermes_chat.sse import Frame, FrameParser


def test_one_frame():
    p = FrameParser()
    assert p.feed(b'id: 7\nevent: delivery\ndata: {"a":1}\n\n') == [Frame("delivery", '{"a":1}', "7")]


def test_comment_and_heartbeat_yield_nothing():
    assert FrameParser().feed(b": hb\n\n") == []


def test_multi_line_data_joined_with_newline():
    assert FrameParser().feed(b"event: x\ndata: a\ndata: b\n\n") == [Frame("x", "a\nb", None)]


def test_frame_split_across_chunks_inside_a_character():
    raw = 'event: delivery\ndata: {"t":"café"}\n\n'.encode()
    cut = raw.index("é".encode()) + 1          # between the two bytes of é
    p = FrameParser()
    assert p.feed(raw[:cut]) == []
    assert p.feed(raw[cut:]) == [Frame("delivery", '{"t":"café"}', None)]


def test_crlf_line_endings():
    assert FrameParser().feed(b"event: ready\r\ndata: {}\r\n\r\n") == [Frame("ready", "{}", None)]


def test_frame_without_event_field_is_message():
    assert FrameParser().feed(b"data: x\n\n") == [Frame("message", "x", None)]


def test_two_frames_in_one_chunk():
    assert len(FrameParser().feed(b"event: a\ndata: 1\n\nevent: b\ndata: 2\n\n")) == 2