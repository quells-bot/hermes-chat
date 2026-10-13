import os
import re
import shutil
import socket
import subprocess
import time
from pathlib import Path

import pytest

from stubserver import StubServer

REPO_ROOT = Path(__file__).resolve().parents[2]


def _ensure_hermes_chat_platform_registered():
    from gateway.platform_registry import PlatformEntry, platform_registry
    if not platform_registry.is_registered("hermes_chat"):
        platform_registry.register(PlatformEntry(
            name="hermes_chat", label="Hermes Chat",
            adapter_factory=lambda config: None, check_fn=lambda: True, source="bundled"))


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
    _ensure_hermes_chat_platform_registered()
    import gateway.status as status_mod
    monkeypatch.setattr(status_mod, "publish_runtime_status", lambda **kw: 0)
    from hermes_chat import adapter as adapter_mod
    monkeypatch.setattr(adapter_mod, "get_hermes_home", lambda: tmp_path)

    def build(url=None, token="hca_test_token"):
        from gateway.config import PlatformConfig
        from hermes_chat.adapter import HermesChatAdapter
        return HermesChatAdapter(PlatformConfig(extra={"url": url or stub.url, "token": token}))

    return build

def _free_port() -> int:
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


def _wait_for_port(port: int, timeout: float = 20.0) -> None:
    deadline = time.time() + timeout
    while time.time() < deadline:
        with socket.socket() as s:
            s.settimeout(0.5)
            try:
                s.connect(("127.0.0.1", port))
                return
            except OSError:
                time.sleep(0.05)
    raise AssertionError(f"port {port} never accepted connections")


@pytest.fixture(scope="session")
def chat_binary(tmp_path_factory):
    """The hermes-chat server binary, built once from this repository."""
    if shutil.which("go") is None:
        pytest.skip("go is not on PATH; the end-to-end test needs the hermes-chat binary")
    out = tmp_path_factory.mktemp("bin") / "hermes-chat"
    env = {**os.environ, "CGO_ENABLED": "0"}
    subprocess.run(["go", "build", "-o", str(out), "./cmd/hermes-chat"],
                   cwd=REPO_ROOT, env=env, check=True)
    return out


class ChatServer:
    """A real hermes-chat process on a temp database, restartable in place."""

    def __init__(self, binary, base_dir):
        self._binary = binary
        self._dir = Path(base_dir)
        self._port = _free_port()
        self._db = self._dir / "chat.db"
        self._proc = None
        self.agent_token = None
        self.agent_id = None
        self.human = None
        self.env = {
            "HERMES_CHAT_DB_PATH": str(self._db),
            "HERMES_CHAT_ATTACHMENTS_DIR": str(self._dir / "attachments"),
            "HERMES_CHAT_HTTP_ADDR": f"127.0.0.1:{self._port}",
            "HERMES_CHAT_DEBUG_ADDR": f"127.0.0.1:{_free_port()}",
            "HERMES_CHAT_TLS_MODE": "off",
            "HERMES_CHAT_COOKIE_SECURE": "false",
        }
        self.url = f"http://127.0.0.1:{self._port}"

    def _run_cli(self, *args, stdin=None):
        r = subprocess.run([str(self._binary), *args],
                           env={**os.environ, **self.env},
                           input=stdin, capture_output=True, text=True, timeout=60)
        assert r.returncode == 0, f"{args} failed: {r.stderr}"
        return r

    def start_full(self):
        """migrate, add kai and the bot agent, then serve and log the human in."""
        self._run_cli("migrate")
        self._run_cli("user", "add", "kai", stdin="kai-password\n")
        r = self._run_cli("agent", "add", "bot", "Bot")
        self.agent_token = re.search(r"^token: (hca_[A-Za-z0-9_-]{43})$", r.stdout, re.M).group(1)
        self.agent_id = re.search(r"@bot \(([0-9A-HJKMNP-TV-Z]{26})\)", r.stdout).group(1)
        self.start()
        import httpx
        self.human = httpx.Client(base_url=self.url, headers={"Origin": self.url}, timeout=10.0)
        r = self.human.post("/api/login", json={"username": "kai", "password": "kai-password"})
        assert r.status_code == 200, r.text

    def start(self):
        self._proc = subprocess.Popen(
            [str(self._binary), "serve"],
            env={**os.environ, **self.env},
            stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
        _wait_for_port(self._port)

    def stop(self):
        if self._proc is not None and self._proc.poll() is None:
            self._proc.terminate()
            try:
                self._proc.wait(timeout=10)
            except subprocess.TimeoutExpired:
                self._proc.kill()
                self._proc.wait()
        self._proc = None

    # Human API helpers (the human is logged in).

    def open_dm(self) -> str:
        r = self.human.post("/api/rooms", json={"topic": "DM", "agent_ids": [self.agent_id]})
        assert r.status_code == 201, r.text
        return r.json()["id"]

    def post(self, room_id: str, text: str) -> str:
        r = self.human.post(f"/api/rooms/{room_id}/messages", json={"text": text})
        assert r.status_code in (200, 201), r.text
        return r.json()["id"]

    def messages(self, room_id: str) -> list:
        r = self.human.get(f"/api/rooms/{room_id}/messages", params={"limit": 200})
        assert r.status_code == 200, r.text
        return r.json()["messages"]

    def pause(self, room_id: str) -> None:
        r = self.human.post(f"/api/rooms/{room_id}/pause")
        assert r.status_code == 200, r.text

    def resume(self, room_id: str) -> None:
        r = self.human.post(f"/api/rooms/{room_id}/resume")
        assert r.status_code == 200, r.text

    def delivery_state(self, room_id: str, text: str) -> str | None:
        for m in self.messages(room_id):
            if m.get("text") == text:
                for d in m["deliveries"]:
                    if d["agent"]["id"] == self.agent_id:
                        return d["state"]
        return None


@pytest.fixture
def chat_server(chat_binary, tmp_path):
    server = ChatServer(chat_binary, tmp_path)
    server.start_full()
    yield server
    server.stop()


@pytest.fixture
async def e2e_adapter_factory(chat_server, tmp_path, monkeypatch):
    """Build a connected HermesChatAdapter against the real server; the open
    turn record lives under tmp_path. Disconnects every built adapter at
    teardown."""
    _ensure_hermes_chat_platform_registered()
    import gateway.status as status_mod
    monkeypatch.setattr(status_mod, "publish_runtime_status", lambda **kw: 0)
    from hermes_chat import adapter as adapter_mod
    monkeypatch.setattr(adapter_mod, "get_hermes_home", lambda: tmp_path / "record")
    built = []

    async def build(handler, *, connect=True):
        from gateway.config import PlatformConfig
        from hermes_chat.adapter import HermesChatAdapter
        a = HermesChatAdapter(PlatformConfig(
            extra={"url": chat_server.url, "token": chat_server.agent_token}))
        a.set_message_handler(handler)
        if connect:
            assert await a.connect() is True
        built.append(a)
        return a

    yield build
    for a in built:
        await a.cancel_background_tasks()
        await a.disconnect()


@pytest.fixture
async def e2e_adapter(e2e_adapter_factory):
    async def noop(event):
        return None

    return await e2e_adapter_factory(noop)
