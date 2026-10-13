import pytest

from hermes_chat.record import OpenTurns


def test_add_survives_reopen(tmp_path):
    p = tmp_path / "x" / "open.json"
    OpenTurns(p).add("D1", "R1")
    assert OpenTurns(p).all() == {"D1": "R1"}


def test_remove(tmp_path):
    p = tmp_path / "open.json"
    turns = OpenTurns(p)
    turns.add("D1", "R1")
    turns.add("D2", "R2")
    turns.remove("D1")
    assert turns.all() == {"D2": "R2"}


def test_remove_unknown_is_a_no_op(tmp_path):
    p = tmp_path / "open.json"
    turns = OpenTurns(p)
    turns.add("D1", "R1")
    turns.remove("D9")  # just must not raise or rewrite anything
    assert turns.all() == {"D1": "R1"}


def test_for_room(tmp_path):
    p = tmp_path / "open.json"
    turns = OpenTurns(p)
    turns.add("D1", "R1")
    turns.add("D2", "R2")
    assert turns.for_room("R1") == "D1"
    assert turns.for_room("R2") == "D2"
    assert turns.for_room("R9") is None


def test_missing_file_is_empty(tmp_path):
    assert OpenTurns(tmp_path / "open.json").all() == {}


@pytest.mark.parametrize("content", [b"", b"{", b'{"version":1,"turns":[]}', b"\xff\xfe", b'{"version":99,"turns":{}}'])
def test_corrupt_file_is_empty_with_a_warning(tmp_path, caplog, content):
    p = tmp_path / "open.json"
    p.write_bytes(content)
    assert OpenTurns(p).all() == {}
    assert "open-turn record" in caplog.text
    OpenTurns(p).add("D1", "R1")  # and it can be written again
    assert OpenTurns(p).all() == {"D1": "R1"}


def test_no_temporary_file_left_behind(tmp_path):
    p = tmp_path / "open.json"
    OpenTurns(p).add("D1", "R1")
    OpenTurns(p).remove("D1")
    assert list(tmp_path.iterdir()) == [p]