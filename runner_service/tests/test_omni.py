"""The omni pack (plan 42 step 2) without the model: its values equal the canonical fixtures (the mlx env), the
fallback (the 3B present and loaded when Qwen3-Omni's drive is unplugged), the llama-server argv, a listen run against a stand-in server (a video's picture and sound, an audio take, the
transcript, a stopped server started again) and the residency manager stopping a resident server it drops.
The real model: packs/omni/model.yaml has the numbers measured on the Mac."""

import base64
import json
from pathlib import Path
from types import SimpleNamespace

import pytest
from conftest import CONTRACTS

import config
from core import child, media, models, registry
from core import pack as packs
from core.graph import RunError
from core.residency import Residency
from packs.omni import mlx as omni

FIXTURES = CONTRACTS / "fixtures/registry"


def test_omni_pack_publishes_the_canonical_fixtures():
    pytest.importorskip("mlx")
    values, impls, _ = registry.build("mlx")
    for kind in ("nodes", "workflows"):
        want = json.loads((FIXTURES / f"omni.{kind}.valid.json").read_text())[kind]
        got = values[kind][kind]
        if kind == "nodes":
            kinds = {n["kind"] for n in want}
            got = [n for n in got if n["kind"] in kinds]
        else:
            got = [w for w in got if w["family"] == "omni"]
        assert got == want
    assert impls["omni/listen"].uses == ("model", "asr") and impls["omni/listen"].reports == ("transcript",)


SMALL = ["Qwen2.5-Omni-3B-Q8_0.gguf", "mmproj-Qwen2.5-Omni-3B-Q8_0.gguf"]


def test_the_3b_is_present_and_loaded_when_the_omni_drive_is_unplugged(tmp_path, monkeypatch):
    mac, t9 = tmp_path / "Mac", tmp_path / "Volumes/T9/models"
    for name in SMALL:
        (mac / "Qwen2.5-Omni-3B-GGUF").mkdir(parents=True, exist_ok=True)
        (mac / "Qwen2.5-Omni-3B-GGUF" / name).write_bytes(b"GGUF")
    (mac / "Qwen3-ASR-1.7B").mkdir()
    (mac / "Qwen3-ASR-1.7B/model-00001-of-00001.safetensors").write_bytes(b"\2\0\0\0\0\0\0\0{}")
    monkeypatch.setattr(config, "MODELS", mac)
    monkeypatch.setattr(config, "ROOTS", [str(mac), str(t9)])
    monkeypatch.setattr(config, "DOWNLOADS", 0)
    monkeypatch.setattr(config, "CACHE", tmp_path / "cache")
    monkeypatch.setattr(models, "hashes", models.Hashes())
    monkeypatch.setattr(models, "seen", {packs.folder_id(str(t9)): ["Qwen3-Omni-30B-A3B-Instruct-GGUF"]})
    pack, _ = packs.load(packs.PACKS / "omni", "mlx")
    assert {v["id"]: v["presence"] for v in models.entry(pack)["variants"]} == {"q8": "unplugged", "3b": "present"}
    started = []
    monkeypatch.setattr(child, "start", lambda ctx, argv, **kw: (started.append(argv), (SimpleNamespace(), 1))[1])
    monkeypatch.setattr(omni, "binary", lambda: Path("/bin/sh"))
    ctx = SimpleNamespace(node="listen")
    omni.load_component("model", packs.ModelRef(pack, "3b", "int8"), ctx)
    (argv,) = started
    assert [Path(argv[2]).name, Path(argv[4]).name] == SMALL
    with pytest.raises(RunError, match="the Q8_0 GGUF or its mmproj is missing"):
        omni.load_component("model", packs.ModelRef(pack, "q8", "int8"), ctx)


def test_the_server_is_one_slot_on_metal_with_a_free_port():
    a = omni.argv(Path("/b/llama-server"), Path("/m/q8.gguf"), Path("/m/mmproj.gguf"))
    assert a[:5] == ["/b/llama-server", "-m", "/m/q8.gguf", "--mmproj", "/m/mmproj.gguf"]
    assert a[a.index("--port") + 1] == "0" and a[a.index("-np") + 1] == "1" and a[a.index("-ngl") + 1] == "999"
    assert omni.READY.search("srv  server_on_li: listening on http://127.0.0.1:18731").group(1) == "18731"


class Stand:
    """A server stand-in: records what it was sent; `alive` False the first time when `dead`."""

    def __init__(self, dead=False):
        self.sent, self.dead, self.stopped = [], dead, False

    def alive(self):
        return not self.dead

    def evicted(self):
        self.stopped = True

    def chat(self, node, content, max_tokens):
        self.sent.append(content)
        return "It ends abruptly.", 120, 5


class Asr:
    def generate(self, path):
        assert Path(path).read_bytes()[:4] == b"RIFF"
        return SimpleNamespace(text=" Good morning. ")


def ctx_for(server, released=None):
    model = SimpleNamespace(key=lambda c: ("qwen3-omni-30b-a3b", "q8", c, "int8"))
    servers = [server] if not isinstance(server, list) else server

    def component(m, name):
        return Asr() if name == "asr" else servers[0]

    def release(keys):
        released.append(keys)
        servers.pop(0)

    ctx = SimpleNamespace(node="listen", check=lambda: None, progress=lambda *a: None, component=component,
                          residency=SimpleNamespace(release=release))  # fmt: skip
    return ctx, {"model": model}


def clip(tmp_path: Path, sound: bool) -> bytes:
    out = tmp_path / "clip.mp4"
    args = ["-f", "lavfi", "-i", "color=c=red:s=64x64:d=1:r=24"]
    if sound:
        args += ["-f", "lavfi", "-i", "sine=frequency=440:duration=1", "-shortest"]
    media.ffmpeg([*args, "-pix_fmt", "yuv420p", str(out)])
    return out.read_bytes()


needs_ffmpeg = pytest.mark.skipif(not (media.FFMPEG and media.FFPROBE), reason="ffmpeg and ffprobe on PATH")
PARAMS = {"question": "Does it end abruptly?", "transcript": True, "max_tokens": 600}


@needs_ffmpeg
def test_a_video_goes_whole_with_its_sound_and_a_transcript(tmp_path):
    server = Stand()
    ctx, inputs = ctx_for(server)
    out = omni.listen(ctx, PARAMS | {"source": {"data": clip(tmp_path, True)}}, inputs)
    (content,) = server.sent
    assert [c["type"] for c in content] == ["video_url", "input_audio", "text"]
    assert (
        content[0]["video_url"]["url"].startswith("data:video/mp4;base64,") and content[2]["text"] == PARAMS["question"]
    )
    assert out["answer"] == "It ends abruptly." and out["transcript"] == "Good morning."
    assert out["answer"].record == {"tries": [{"text": "It ends abruptly.", "error": None}], "seen": [],
                                    "tokens_in": 120, "tokens_out": 5, "seconds": out["answer"].record["seconds"]}  # fmt: skip


@needs_ffmpeg
def test_only_the_part_in_to_out_is_heard(tmp_path):
    out = tmp_path / "long.mp4"
    media.ffmpeg(["-f", "lavfi", "-i", "color=c=red:s=64x64:d=3:r=24", "-f", "lavfi", "-i",
                  "sine=frequency=440:duration=3", "-shortest", "-pix_fmt", "yuv420p", str(out)])  # fmt: skip
    server = Stand()
    ctx, inputs = ctx_for(server)
    omni.listen(ctx, PARAMS | {"source": {"data": out.read_bytes(), "in": 1.0, "out": 2.0}}, inputs)
    (content,) = server.sent
    sent = tmp_path / "sent.mp4"
    sent.write_bytes(base64.b64decode(content[0]["video_url"]["url"].split(",", 1)[1]))
    assert media.probe(sent)["seconds"] < 2.5
    sound = tmp_path / "sound.wav"
    sound.write_bytes(base64.b64decode(content[1]["input_audio"]["data"]))
    assert abs(media.probe(sound)["seconds"] - 1.0) < 0.1


@needs_ffmpeg
def test_a_silent_video_says_so_and_has_no_transcript(tmp_path):
    server = Stand()
    ctx, inputs = ctx_for(server)
    out = omni.listen(ctx, PARAMS | {"source": {"data": clip(tmp_path, False)}}, inputs)
    (content,) = server.sent
    assert [c["type"] for c in content] == ["video_url", "text"] and content[1]["text"].startswith(
        "(The take has no sound.)"
    )
    assert out["transcript"] == ""


@needs_ffmpeg
def test_an_audio_take_and_a_stopped_server_started_again(tmp_path):
    wav = media.wav(b"\0\0" * 16000, 16000, 1)
    dead, fresh, released = Stand(dead=True), Stand(), []
    ctx, inputs = ctx_for([dead, fresh], released)
    omni.listen(ctx, PARAMS | {"transcript": False, "source": {"data": wav}}, inputs)
    assert released == [{("qwen3-omni-30b-a3b", "q8", "model", "int8")}] and not dead.sent
    assert [c["type"] for c in fresh.sent[0]] == ["input_audio", "text"]


def test_an_empty_question_or_an_unreadable_take_fails(tmp_path):
    ctx, inputs = ctx_for(Stand())
    with pytest.raises(RunError, match=r"^listen.question: ask something$"):
        omni.listen(ctx, PARAMS | {"question": " ", "source": {"data": b""}}, inputs)
    if media.FFPROBE:
        with pytest.raises(RunError, match=r"^listen.source: not a readable video or audio take$"):
            omni.listen(ctx, PARAMS | {"source": {"data": b"nope"}}, inputs)


def test_the_residency_manager_stops_a_server_it_drops():
    r, server = Residency(100), Stand()
    r.get(("m", "q8", "model", "int8"), 30, 0, lambda: server)
    r.unload()
    assert server.stopped


def test_child_start_returns_a_live_server_and_its_port(tmp_path):
    script = tmp_path / "srv.sh"
    script.write_text("#!/bin/sh\necho 'listening on http://127.0.0.1:4321' >&2\nsleep 30\n")
    script.chmod(0o755)

    ctx = SimpleNamespace(node="listen", check=lambda: None)
    c, port = child.start(ctx, [str(script)], cwd=tmp_path, name="llama", ready=omni.READY, ready_s=10)
    try:
        assert port == 4321 and c.proc.poll() is None
    finally:
        c.stop()
    assert c.proc.poll() is not None
