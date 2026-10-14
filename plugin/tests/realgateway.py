"""A real Hermes gateway, started from the checkout at HERMES_SRC on a
temporary Hermes home with this plugin linked in, talking to a real
hermes-chat server and to Hermes's own scripted model server. Whole turns run
with no model."""

from __future__ import annotations

import json
import os
import pathlib
import shutil
import signal
import subprocess
import sys
import threading
import time

import httpx
import pytest

HERMES_SRC = pathlib.Path(os.environ.get("HERMES_SRC", "~/Dev/hermes-agent")).expanduser()
PLUGIN_DIR = pathlib.Path(__file__).resolve().parents[1] / "hermes_chat"
_FAKES = HERMES_SRC / "tests" / "fakes" / "fake_llm_provider.py"

if not _FAKES.is_file():
    pytest.skip(f"needs the Hermes checkout with its scripted model server; set HERMES_SRC (looked in {HERMES_SRC})",
                allow_module_level=True)
if shutil.which("go") is None:
    pytest.skip("go is not on PATH; the gateway tier needs the hermes-chat binary", allow_module_level=True)
pytest.importorskip("gateway.platforms.base", reason="needs the Hermes checkout; set HERMES_SRC")

from tests.fakes.fake_llm_provider import FakeLLMServer, write_hermes_home  # noqa: E402

from hermes_chat.sse import FrameParser  # noqa: E402

_STARTED = "Gateway housekeeping started"
_STRIPPED_ENV = ("HERMES_", "OPENAI_", "ANTHROPIC_", "OPENROUTER_")
_CONFIG = (
    "plugins:\n  enabled:\n    - hermes-chat\n"
    "streaming:\n  enabled: true\n"
    "display:\n  platforms:\n    hermes_chat:\n"
    '      tool_progress: "off"\n'
    "      interim_assistant_messages: false\n"
    "terminal:\n  backend: local\n"
)


class Gateway:
    def __init__(self, chat, workdir: pathlib.Path, script: list, *, home_room: bool = False) -> None:
        self.chat = chat
        self.llm = FakeLLMServer(script)
        self.llm.start()
        self._work = pathlib.Path(workdir)
        self._home = self._work / "hermes-home"
        self._proc: subprocess.Popen | None = None
        self.room_id = chat.open_dm()
        write_hermes_home(self._home, self.llm.base_url, extra_config=_CONFIG)
        (self._home / "plugins").mkdir(exist_ok=True)
        (self._home / "plugins" / "hermes-chat").symlink_to(PLUGIN_DIR)
        with (self._home / ".env").open("a", encoding="utf-8") as env_file:
            env_file.write(f"HERMES_CHAT_URL={chat.url}\nHERMES_CHAT_TOKEN={chat.agent_token}\n"
                           "HERMES_CHAT_ALLOW_ALL_USERS=true\n")
            if home_room:
                env_file.write(f"HERMES_CHAT_HOME_ROOM={self.room_id}\n")
        (self._work / "home").mkdir(exist_ok=True)
        self.events: list[tuple[str, dict]] = []
        self._events_stop = threading.Event()
        self._events_thread = threading.Thread(target=self._collect_events, daemon=True)
        self._events_thread.start()

    # ── the process ─────────────────────────────────────────────────────────

    def _env(self) -> dict:
        env = {k: v for k, v in os.environ.items() if not k.startswith(_STRIPPED_ENV)}
        env.update(HERMES_HOME=str(self._home), HOME=str(self._work / "home"))
        return env

    def hermes(self, *args: str) -> subprocess.CompletedProcess:
        """Run one `hermes` command against this Hermes home, as its own process."""
        return subprocess.run([sys.executable, "-m", "hermes_cli.main", *args], cwd=HERMES_SRC, env=self._env(),
                              capture_output=True, text=True, timeout=180)

    def start(self) -> None:
        env = self._env()
        out = (self._work / "gateway.out").open("a")
        self._proc = subprocess.Popen(
            [sys.executable, "-m", "hermes_cli.main", "gateway", "run", "--no-supervise"],
            cwd=HERMES_SRC, env=env, stdout=out, stderr=subprocess.STDOUT)

    def wait_started(self, nth: int = 1) -> None:
        self.wait(lambda: self.log().count(_STARTED) >= nth, timeout=90)
        time.sleep(1.0)

    def stop(self) -> None:
        if self._proc is None or self._proc.poll() is not None:
            return
        self._proc.send_signal(signal.SIGTERM)
        try:
            self._proc.wait(40)
        except subprocess.TimeoutExpired:
            self._proc.kill()
            self._proc.wait(10)

    def close(self) -> None:
        self.stop()
        self._events_stop.set()
        self.llm.stop()

    def log(self) -> str:
        path = self._home / "logs" / "agent.log"
        return path.read_text(errors="replace") if path.exists() else ""

    def log_lines(self, logger_prefix: str) -> str:
        return "\n".join(line for line in self.log().splitlines() if f" {logger_prefix}" in line)

    # ── the human's side ────────────────────────────────────────────────────

    def post(self, text: str) -> str:
        return self.chat.post(self.room_id, text)

    def messages(self) -> list[dict]:
        return self.chat.messages(self.room_id)

    def agent_texts(self) -> list[str]:
        return [m["text"] for m in self.messages() if (m["author"] or {}).get("kind") == "agent"]

    def state(self, message_id: str) -> str:
        for m in self.messages():
            if m["id"] == message_id:
                return m["deliveries"][0]["state"] if m["deliveries"] else "-"
        return "-"

    def wait(self, predicate, timeout: float = 60.0) -> None:
        deadline = time.time() + timeout
        while time.time() < deadline:
            if predicate():
                return
            if self._proc is not None and self._proc.poll() is not None and self._proc.returncode not in (0, 1):
                raise AssertionError(f"the gateway exited with {self._proc.returncode}; see {self._work}/gateway.out")
            time.sleep(0.2)
        raise AssertionError(f"condition not met in {timeout}s; gateway log tail:\n" + self.log()[-3000:])

    def _collect_events(self) -> None:
        cookies = dict(self.chat.human.cookies)
        parser = FrameParser()
        try:
            with httpx.Client(base_url=self.chat.url, cookies=cookies, timeout=None) as client:
                with client.stream("GET", "/api/events") as response:
                    for chunk in response.iter_bytes():
                        for frame in parser.feed(chunk):
                            try:
                                self.events.append((frame.event, json.loads(frame.data)))
                            except ValueError:
                                pass
                        if self._events_stop.is_set():
                            return
        except httpx.HTTPError:
            return


@pytest.fixture
def gateway_factory(chat_server, tmp_path):
    built: list[Gateway] = []

    def build(script: list, **kwargs) -> Gateway:
        gateway = Gateway(chat_server, tmp_path / f"gw{len(built)}", script, **kwargs)
        built.append(gateway)
        return gateway

    yield build
    for gateway in built:
        gateway.close()
