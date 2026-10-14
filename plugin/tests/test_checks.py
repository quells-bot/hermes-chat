from hermes_chat.checks import display_warnings


def test_display_warnings_none_when_set_as_documented():
    cfg = {
        "streaming": {"enabled": True},
        "display": {"platforms": {"hermes_chat": {"tool_progress": "off", "interim_assistant_messages": False}}},
    }
    assert display_warnings(cfg) == []


def test_display_warnings_names_each_wrong_setting():
    w = display_warnings({})
    assert len(w) == 3
    assert any("tool_progress" in x for x in w)
    assert any("interim_assistant_messages" in x for x in w)
    assert any("streaming.enabled" in x for x in w)


def test_display_warnings_strings_are_not_booleans():
    cfg = {
        "streaming": {"enabled": "true"},
        "display": {"platforms": {"hermes_chat": {"tool_progress": True, "interim_assistant_messages": "false"}}},
    }
    w = display_warnings(cfg)
    assert len(w) == 3
    assert any('"off"' in x for x in w)  # tool_progress names the wanted string
    assert any("boolean" in x for x in w)  # the string-as-boolean case is called out