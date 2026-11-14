"""Stand-ins for NATS and the object store, so the runner is tested without either. Test modules import the helpers
with `from conftest import ...`."""

import asyncio
import json
from pathlib import Path

from nats.js.errors import KeyNotFoundError

from consumer import Consumer
from core import registry
from messaging.hosts import Host

CONTRACTS = Path(__file__).resolve().parents[2] / "contracts"
GEN = Path(__file__).resolve().parents[1] / "gen"


def command(**echo_params) -> dict:
    """The canonical cmd.run.execute fixture, with echo params overridden (delay 0 by default)."""
    cmd = json.loads((CONTRACTS / "fixtures/messages/cmd.run.execute.valid.json").read_text())
    cmd["data"]["workflow"]["nodes"]["echo"]["params"] |= {"delay_ms": 0} | echo_params
    return cmd


class FakeJS:
    """JetStream stand-in: records every publish; calls `on_publish(subject, body)` after each."""

    def __init__(self, on_publish=None) -> None:
        self.sent: list[tuple[str, bytes, dict | None]] = []
        self.on_publish = on_publish

    async def publish(self, subject: str, data: bytes, headers: dict | None = None) -> None:
        self.sent.append((subject, data, headers))
        if self.on_publish:
            self.on_publish(subject, json.loads(data) if subject.startswith("evt.") else data)

    def events(self) -> list[dict]:
        return [json.loads(d) for s, d, _ in self.sent if s.startswith("evt.")]


class FakeWatcher:
    def __init__(self) -> None:
        self.queue: asyncio.Queue = asyncio.Queue()

    def __aiter__(self):
        return self

    async def __anext__(self):
        entry = await self.queue.get()
        if entry is StopAsyncIteration:
            raise StopAsyncIteration
        return entry

    async def stop(self) -> None:
        self.queue.put_nowait(StopAsyncIteration)


class FakeKV:
    """KV bucket stand-in: put, get, delete, and watch of one key."""

    def __init__(self) -> None:
        self.data: dict[str, bytes] = {}
        self.watchers: dict[str, FakeWatcher] = {}

    async def put(self, key: str, value: bytes) -> None:
        self.data[key] = value
        if w := self.watchers.get(key):
            w.queue.put_nowait(key)

    def put_nowait(self, key: str, value: bytes = b"x") -> None:
        asyncio.get_running_loop().create_task(self.put(key, value))

    async def get(self, key: str):
        if key not in self.data:
            raise KeyNotFoundError
        return self.data[key]

    async def delete(self, key: str) -> None:
        self.data.pop(key, None)

    async def watch(self, key: str, ignore_deletes: bool = False) -> FakeWatcher:
        w = self.watchers[key] = FakeWatcher()
        w.queue.put_nowait(None)  # end of the initial values
        return w


class FakeStore:
    def __init__(self) -> None:
        self.objects: dict[str, tuple[bytes, str]] = {}

    async def put(self, key: str, data: bytes, content_type: str) -> None:
        self.objects[key] = (data, content_type)


class FakeMsg:
    """A delivery: records ack/term in `log`, next to the publishes (via `js.sent`)."""

    def __init__(self, cmd, js: FakeJS, subject: str = "cmd.run.fake.fake") -> None:
        self.subject, self.js = subject, js
        self.data = cmd if isinstance(cmd, bytes) else json.dumps(cmd).encode()
        self.log: list[tuple[str, int]] = []  # (action, publishes before it)

    async def ack_sync(self) -> None:
        self.log.append(("ack", len(self.js.sent)))

    async def term(self) -> None:
        self.log.append(("term", len(self.js.sent)))

    async def in_progress(self) -> None:
        pass


def consumer(js: FakeJS) -> Consumer:
    """A Consumer on fakes, with the runner's real registry and node implementations."""
    return Consumer(
        js=js,
        store=FakeStore(),
        cancels_kv=FakeKV(),
        host=Host(FakeKV()),
        impls=registry.IMPLS,
        node_schemas=registry.node_schemas(registry.values()),
    )
