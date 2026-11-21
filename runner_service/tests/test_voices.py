"""Voices (API 1.30, contracts/README.md "Voices"): the core output's WAV, the `voice` param checks, peak_gb's
`speech_seconds` (and max_seconds in memory plans), the starter voices of packs/<pack>/voices/*.yaml and echo/speak."""

import io
import json
import types
import wave

import pytest
from conftest import CONTRACTS, SCHEMAS, FakeJS, FakeMsg, FakeStore, consumer

from core import graph, nodes, pack, voices
from core.graph import RunError
from messaging import schemas
from packs.echo import fake

FIXTURES = CONTRACTS / "fixtures"
PALETTE = {n["kind"]: n for g in json.loads((FIXTURES / "graphs/palette.json").read_text())["groups"]
           for n in g["nodes"]}  # fmt: skip
ADA = {"item_id": "echo/ada", "version": 1, "source": "preset", "family": "echo", "speaker": "ada", "language": "en"}  # fmt: skip


def speak_command(n: int = 10, **params) -> dict:
    cmd = json.loads((FIXTURES / f"messages/cmd.run.execute.valid.{n}.json").read_text())
    cmd["data"]["workflow"]["nodes"]["speak"]["params"] |= {"delay_ms": 0} | params
    return cmd


def read_wav(data: bytes) -> tuple:
    with wave.open(io.BytesIO(data)) as w:
        return w.getnchannels(), w.getsampwidth(), w.getframerate(), w.getnframes()


@pytest.mark.parametrize("n", [10, 11])  # a preset and a designed voice
async def test_echo_voice_over_stores_a_wav_and_reports_an_audio_output(n):
    js, store = FakeJS(), FakeStore()
    c = consumer(js)
    c.store = store
    cmd = speak_command(n)
    await c.handle(FakeMsg(cmd, js))
    evts = js.events()
    assert all(schemas.message_errors(e) == [] for e in evts)
    assert evts[-1]["data"]["status"] == "succeeded", evts[-1]
    out = next(e for e in evts if e["type"] == "evt.run.output")["data"]
    assert out["kind"] == "audio" and out["key"] == cmd["data"]["output_prefix"] + "0.wav"
    assert "width" not in out and "height" not in out
    assert out["audio"] == {"duration_s": 2.0, "sample_rate": 24000}  # 30 characters / 15
    data, content_type = store.objects[out["key"]]
    assert content_type == "audio/wav" and out["bytes"] == len(data)
    assert read_wav(data) == (1, 2, 24000, 48000)


def test_output_writes_pcm_or_a_mono_wav_and_refuses_stereo():
    got = []
    ctx = types.SimpleNamespace(node="output", output=lambda data, **kw: got.append((data, kw)))
    pcm = bytes(2 * 8000)
    nodes.output(ctx, {}, {"media": {"wav": pcm, "sample_rate": 16000, "duration_s": 0.5}})
    data, kw = got[-1]
    assert kw == {"ext": "wav", "kind": "audio", "audio": {"duration_s": 0.5, "sample_rate": 16000}}
    assert read_wav(data) == (1, 2, 16000, 8000)
    nodes.output(
        ctx, {}, {"media": {"wav": data, "sample_rate": 0, "duration_s": 0}}
    )  # a WAV file: its own facts
    assert got[-1] == (data, kw)
    buf = io.BytesIO()
    with wave.open(buf, "wb") as w:
        w.setnchannels(2), w.setsampwidth(2), w.setframerate(24000), w.writeframes(bytes(400))
    with pytest.raises(RunError, match="output: audio must be PCM 16-bit mono"):
        nodes.output(
            ctx, {}, {"media": {"wav": buf.getvalue(), "sample_rate": 24000, "duration_s": 0}}
        )
    with pytest.raises(RunError, match="outside 8-96 kHz"):
        nodes.output(ctx, {}, {"media": {"wav": b"", "sample_rate": 24000, "duration_s": 0}})
    for rate in (0, None, "24000", True, 24000.5, 100000):  # checked before it divides
        with pytest.raises(RunError, match="output: audio at .* Hz is outside 8-96 kHz"):
            nodes.output(ctx, {}, {"media": {"wav": pcm, "sample_rate": rate, "duration_s": 0}})


def run_nodes(voice, extra: dict | None = None) -> dict:
    nodes = json.loads(json.dumps(speak_command()["data"]["workflow"]["nodes"]))
    nodes["speak"]["params"]["voice"] = voice
    nodes["speak"] |= extra or {}
    return nodes


CLIP = {"item_id": "5a0c1e2f-3b4d-4e5f-8a6b-7c8d9e0f1a21", "version": 1, "source": "clip",
        "clip": {"take_id": "t1", "key": "u/o/takes/t1.wav", "bytes": 10, "etag": "e1", "kind": "audio", "in": 0,
                 "out": 3}}  # fmt: skip


@pytest.mark.parametrize(
    ("voice", "why", "extra"),
    [
        (ADA, None, None),
        ({"item_id": "5a0c1e2f-3b4d-4e5f-8a6b-7c8d9e0f1a21", "version": 2, "source": "design", "text": "low"}, None, None),
        (None, "speak.voice: a voice is required", None),
        ({"voice_id": "echo/ada"}, "speak.voice must be a voice", None),  # unresolved: studio resolves it
        (CLIP, None, None),  # API 1.31: echo/speak takes a clip
        (ADA | {"item_id": "qwen3-tts/ryan", "family": "qwen3-tts"}, "speak.voice: voice qwen3-tts/ryan is for qwen3-tts", None),
    ],
)  # fmt: skip
def test_a_run_needs_a_resolved_voice_that_fits(voice, why, extra):
    assert graph.validate(run_nodes(voice, extra), SCHEMAS) == ([why] if why else [])


def test_echo_speaks_a_clip_voice_in_a_pitch_from_its_etag():
    ctx = types.SimpleNamespace(node="speak", check=lambda: None, progress=lambda *a: None)
    say = lambda v: fake.speak(ctx, {"voice": v, "seed": 1, "delay_ms": 0}, {"text": "hello"})[
        "audio"
    ]["wav"]
    other = CLIP | {"clip": CLIP["clip"] | {"etag": "e2"}}
    assert (
        say(CLIP)
        == say(CLIP | {"clip": CLIP["clip"] | {"data": b"x", "take_id": "t9"}})
        != say(other)
    )


def test_a_registry_workflow_holds_a_null_voice():
    wf = next(w for w in json.loads((FIXTURES / "registry/workflows.valid.json").read_text())["workflows"]
              if w["id"] == "echo-voice-over")  # fmt: skip
    assert graph.validate(wf["nodes"], SCHEMAS, run=False) == []
    assert graph.workflow_media(wf["nodes"], SCHEMAS) == "audio"


def test_workflow_media_follows_the_contract_fixture():
    fx = json.loads((FIXTURES / "voices/media.json").read_text())
    for c in fx["cases"]:
        assert (
            graph.workflow_media(fx["workflows"][c["workflow"]]["nodes"], PALETTE) == c["expect"]
        ), c


def test_seconds_follows_the_contract_fixture():
    fx = json.loads((FIXTURES / "voices/seconds.json").read_text())
    for c in fx["cases"]:
        got = graph.speech_seconds(fx["workflows"][c["workflow"]]["nodes"], PALETTE, c["stage"], c.get("max_seconds"))  # fmt: skip
        assert got == c["expect"], c


def speech_node(peak: str, max_seconds=None):
    stage = {
        "title": "S",
        "inputs": {"text": "Text"},
        "outputs": {"audio": "Audio"},
        "peak_gb": peak,
    }
    if max_seconds is not None:
        stage["max_seconds"] = max_seconds
    module = types.SimpleNamespace(__name__="m", speak=lambda *a: None)
    return pack._nodes({"speak": stage}, "echo", "c0de", None, module)["echo/speak"][1]


def test_seconds_in_peak_gb_and_memory_plans_plan_at_max_seconds():
    fx = json.loads((FIXTURES / "voices/seconds.json").read_text())["workflows"]
    impl = speech_node("1 + 0.5 * speech_seconds", max_seconds=40)
    long, empty = fx["long"]["nodes"], fx["default-text"]["nodes"]
    peak = lambda n, plan: graph.stage_peak(n, PALETTE, "speak", impl, {}, {}, plan=plan)
    assert peak(long, False) == 21  # 900 characters = 60 s, capped at 40
    assert peak(empty, False) == 1  # the run's own (empty) text
    assert peak(empty, True) == 21  # a plan: at max_seconds
    assert (
        graph.stage_peak(
            empty, PALETTE, "speak", speech_node("1 + speech_seconds"), {}, {}, plan=True
        )
        == 1
    )
    with pytest.raises(ValueError, match="max_seconds must be a number > 0"):
        speech_node("1", max_seconds=0)


def test_a_stage_param_keeps_its_own_name_and_size_names_are_refused():
    # ltx/extend: its own `seconds` param, not the speech length
    stage = {"title": "E", "params": {"seconds": {"type": "float", "label": "S", "default": 2, "min": 0.1, "max": 10}},
             "peak_gb": "seconds"}  # fmt: skip
    module = types.SimpleNamespace(__name__="m", extend=lambda *a: None)
    impl = pack._nodes({"extend": stage}, "echo", "c0de", None, module)["echo/extend"][1]
    assert pack.evaluate(impl.peak, {"seconds": 3, "speech_seconds": 0}) == 3
    for name in ("mp", "mp_in", "speech_seconds"):
        bad = stage | {"params": {name: stage["params"]["seconds"]}, "peak_gb": "1"}
        with pytest.raises(ValueError, match="peak_gb size names"):
            pack._nodes({"extend": bad}, "echo", "c0de", None, module)


def test_echo_speak_is_deterministic_and_fails_without_text():
    ctx = types.SimpleNamespace(
        node="speak", check=lambda: None, progress=lambda *a: steps.append(a)
    )
    steps = []
    run = lambda text, **p: fake.speak(
        ctx, {"voice": ADA, "seed": 42, "delay_ms": 0} | p, {"text": text}
    )["audio"]
    a = run("Hello there, how are you, Ada?")
    assert a["sample_rate"] == 24000 and a["duration_s"] == 2.0 and len(a["wav"]) == 2 * 48000
    assert [s[:2] for s in steps] == [(1, 2), (2, 2)]  # one step per 15 characters
    assert run("Hello there, how are you, Ada?")["wav"] == a["wav"]
    assert run("Hello there, how are you, Ada?", seed=43)["wav"] != a["wav"]
    assert run("Hello there, how are you, Ada?", voice=ADA | {"item_id": "echo/bo", "speaker": "bo"})["wav"] != a["wav"]  # fmt: skip
    with pytest.raises(RunError, match="^speak: nothing to say$"):
        run("")


# --- starter voices


def wav_file(seconds: float = 0.5, rate: int = 24000, channels: int = 1) -> bytes:
    buf = io.BytesIO()
    with wave.open(buf, "wb") as w:
        w.setnchannels(channels), w.setsampwidth(2), w.setframerate(rate)
        w.writeframes(bytes(2 * channels * int(seconds * rate)))
    return buf.getvalue()


def voice_pack(tmp_path, files: dict, looks=()):
    for name, body in files.items():
        p = tmp_path / "voices" / name
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_bytes(body if isinstance(body, bytes) else body.encode())
    p = types.SimpleNamespace(
        name="t", dir=tmp_path, family="echo", looks=[{"id": x} for x in looks]
    )
    p.voices = voices.load(p)
    return p


def test_echo_pack_publishes_the_canonical_voices_fixture():
    from conftest import VALUES

    strip = {"published_at", "published_by"}
    want = json.loads((FIXTURES / "registry/voices.valid.json").read_text())
    assert {k: v for k, v in VALUES["voices"].items() if k not in strip} == {k: v for k, v in want.items() if k not in strip}  # fmt: skip


async def test_a_preview_is_published_on_contract_and_put_once(tmp_path):
    clip = wav_file()
    p = voice_pack(tmp_path, {"a.yaml": "- {id: ryan, name: Ryan, speaker: Ryan, language: en-US, version: 2, preview: ryan.wav}\n",
                              "ryan.wav": clip})  # fmt: skip
    (entry,) = voices.entries([p])
    assert entry["id"] == "echo/ryan" and entry["version"] == 2 and "style" not in entry
    assert entry["preview"]["duration_s"] == 0.5 and entry["preview"]["sample_rate"] == 24000
    assert entry["preview"]["key"].startswith("packs/echo/voices/ryan-") and entry["preview"][
        "bytes"
    ] == len(clip)
    value = {"schema_version": 1, "host": "h", "env": "fake", "published_at": "2026-11-21T10:00:00Z",
             "published_by": "t", "voices": [entry]}  # fmt: skip
    assert schemas.registry_errors("voices", value) == []
    store = FakeStore()
    await voices.put_previews([p], store)
    assert store.objects[entry["preview"]["key"]] == (clip, "audio/wav")
    store.objects[entry["preview"]["key"]] = (b"kept", "audio/wav")
    await voices.put_previews([p], store)
    assert store.objects[entry["preview"]["key"]][0] == b"kept"  # never overwritten


@pytest.mark.parametrize(
    ("files", "looks", "why"),
    [
        ({"a.yaml": "{id: a}"}, (), "a voices file is a list"),
        ({"a.yaml": "- {id: a, name: A, speaker: a, language: en, colour: red}"}, (), "unknown keys"),
        ({"a.yaml": "- {id: A, name: A, speaker: a, language: en}"}, (), "id must match"),
        ({"a.yaml": "- {id: a, name: A, speaker: 'a b', language: en}"}, (), "speaker must match"),
        ({"a.yaml": "- {id: a, name: A, speaker: a, language: English}"}, (), "language must be a BCP 47 tag"),
        ({"a.yaml": "- {id: a, name: A, speaker: a, language: en, style: ''}"}, (), "style must be 1-500"),
        ({"a.yaml": "- {id: a, name: A, speaker: a, language: en, version: 0}"}, (), "version must be"),
        ({"a.yaml": "- {id: a, name: A, speaker: a, language: en}\n- {id: a, name: B, speaker: b, language: en}"}, (), "used twice"),
        ({"a.yaml": "- {id: a, name: A, speaker: a, language: en}"}, ("a",), "used twice in echo \\(voices and looks\\)"),
        ({"a.yaml": "- {id: a, name: A, speaker: a, language: en, preview: no.wav}"}, (), "is not a file next to the YAML"),
        ({"a.yaml": "- {id: a, name: A, speaker: a, language: en, preview: a.wav}", "a.wav": b"RIFFnope"}, (), "is not a PCM WAV"),
        ({"a.yaml": "- {id: a, name: A, speaker: a, language: en, preview: a.wav}", "a.wav": wav_file(31, 8000)}, (), "up to 30 s"),
        ({"a.yaml": "- {id: a, name: A, speaker: a, language: en, preview: a.wav}", "a.wav": wav_file(25, 24000, 2)}, (), "larger than 2 MB"),
    ],
)  # fmt: skip
def test_a_broken_voices_file_stops_the_runner(tmp_path, files, looks, why):
    with pytest.raises(ValueError, match=why):
        voice_pack(tmp_path, files, looks)


def test_a_starter_voice_may_not_take_a_starter_look_id(monkeypatch):
    import dataclasses

    from core import registry

    loaded = pack.for_env("fake")
    echo = next(p for p, _ in loaded if p.family == "echo")
    same = dataclasses.replace(echo, voices=[echo.voices[0] | {"id": echo.looks[0]["id"]}])
    monkeypatch.setattr(
        pack, "for_env", lambda env: [(same if p is echo else p, n) for p, n in loaded]
    )
    with pytest.raises(ValueError, match="are also look ids"):
        registry.build("fake")
