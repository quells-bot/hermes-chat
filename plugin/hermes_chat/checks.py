"""Setup checks the plugin can run at connect time. Imports nothing from Hermes."""

from __future__ import annotations


def display_warnings(user_config: dict) -> list[str]:
    """One sentence per display-related setting that differs from the
    documented setup. A missing key counts as differing, and a boolean
    written as a YAML string is not accepted as the boolean."""
    warnings: list[str] = []
    display = (user_config.get("display") or {}).get("platforms") or {}
    hermes_chat = display.get("hermes_chat") or {}

    tool_progress = hermes_chat.get("tool_progress")
    if tool_progress != "off":
        warnings.append(
            'display.platforms.hermes_chat.tool_progress must be the string "off" (quoted, so YAML does not read it as a boolean); it is %r'
            % (tool_progress,)
        )

    interim = hermes_chat.get("interim_assistant_messages")
    if interim is not False:
        warnings.append(
            "display.platforms.hermes_chat.interim_assistant_messages must be the boolean false "
            "(a string like \"false\" is not accepted as False); it is %r" % (interim,)
        )

    streaming_enabled = (user_config.get("streaming") or {}).get("enabled")
    if streaming_enabled is not True:
        warnings.append(
            "streaming.enabled must be the boolean true; it is %r" % (streaming_enabled,)
        )

    return warnings