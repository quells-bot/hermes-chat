from hermes_chat.prompts import OpenPrompt, PromptTable, clarify_choices, numbered


def prompt(message_id="P1", room_id="R1", kind="approval", ref=""):
    return OpenPrompt(message_id=message_id, room_id=room_id, kind=kind, session_key="sk", ref=ref)


def test_no_choices_is_an_empty_list():
    assert clarify_choices(None) == []
    assert clarify_choices([]) == []


def test_choices_that_fit_are_kept_as_strings():
    assert clarify_choices(["a", "b"]) == ["a", "b"]
    assert clarify_choices([1, 2]) == ["1", "2"]
    assert clarify_choices(["x" * 200] + list("abcde")) == ["x" * 200, "a", "b", "c", "d", "e"]


def test_choices_that_do_not_fit_the_server_are_none():
    assert clarify_choices(list("abcdefg")) is None
    assert clarify_choices(["x" * 201]) is None
    assert clarify_choices(["a", ""]) is None
    assert clarify_choices(["a", "a"]) is None


def test_numbered_lists_the_choices_under_the_question():
    assert numbered("Which?", ["a", "b"]) == "Which?\n\n1. a\n2. b"


def test_a_prompt_is_taken_once():
    table = PromptTable()
    table.add(prompt())
    assert table.take("P1").message_id == "P1"
    assert table.take("P1") is None


def test_a_prompt_is_taken_by_its_ref():
    table = PromptTable()
    table.add(prompt("P1", kind="clarify", ref="c1"))
    assert table.take_ref("c1").message_id == "P1"
    assert table.take_ref("c1") is None and table.take("P1") is None
    assert table.take_ref("") is None


def test_a_rooms_prompts_are_taken_by_kind():
    table = PromptTable()
    table.add(prompt("P1"))
    table.add(prompt("P2", kind="clarify", ref="c1"))
    table.add(prompt("P3", kind="confirm", ref="x1"))
    table.add(prompt("P4", room_id="R2"))
    assert sorted(p.message_id for p in table.take_room("R1", ("approval", "clarify"))) == ["P1", "P2"]
    assert table.take("P3").kind == "confirm" and table.take("P4").room_id == "R2"
    assert table.take_room("R1", ("approval", "clarify")) == []
