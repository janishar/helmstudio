"""The zonos3 pack without the GPU: its registry values equal the canonical fixtures (the 27 previews pinned by their
keys), the language and Delivery mappings of contracts/README.md "zonos3", a clip voice's cut and its bounds, the
server's refusal text, and a voice's clip read like a media input."""

import asyncio
import http.server
import io
import json
import re
import threading
import wave
from pathlib import Path

import pytest
from conftest import CONTRACTS

from core import images
from core.graph import RunError
from packs.zonos3 import native

FIXTURES = CONTRACTS / "fixtures/registry"
REGISTRIES = native.ZONOS3 / "model/src/zonos3/registries/languages.json"
QWEN_WAV = Path(__file__).resolve().parents[1] / "packs/qwen3_tts/voices/uncle-fu.wav"


def fixture(key: str) -> dict:
    return json.loads((FIXTURES / f"zonos3.{key}.valid.json").read_text())


@pytest.fixture(scope="module")
def values():
    from core import registry

    return registry.build("native")[0]


def test_nodes_workflows_voices_and_models_match_the_fixtures(values):
    kinds = {n["kind"] for n in fixture("nodes")["nodes"]}
    assert [n for n in values["nodes"]["nodes"] if n["kind"] in kinds] == fixture("nodes")["nodes"]
    assert [w for w in values["workflows"]["workflows"] if w["family"] == "zonos3"] == fixture(
        "workflows"
    )["workflows"]
    # the previews' keys carry their sha256: equal values pin the 27 converted WAVs byte for byte
    assert [v for v in values["voices"]["voices"] if v["family"] == "zonos3"] == fixture("voices")[
        "voices"
    ]
    (got,) = [m for m in values["models"]["models"] if m["family"] == "zonos3"]
    (want,) = fixture("models")["models"]
    keys = ("id", "family", "title", "media", "runtimes")
    assert {k: got[k] for k in keys} == {k: want[k] for k in keys}
    g, w = got["variants"][0], want["variants"][0]
    assert {k: g[k] for k in ("id", "title", "dir", "source", "precisions")} == {
        k: w[k] for k in ("id", "title", "dir", "source", "precisions")}  # fmt: skip
    if (
        g["presence"] == "present"
    ):  # on a host with the weights: the same sizes and the flat 2 GB plan
        assert (g["components"], g["memory"]) == (w["components"], w["memory"])


def test_every_language_maps_to_a_server_code_in_the_registrys_order():
    (speak,) = [n for n in fixture("nodes")["nodes"] if n["kind"] == "zonos3/speak"]
    tags = next(p["enum"] for p in speak["params"] if p["name"] == "language")
    codes = [x["code"] for x in json.loads(REGISTRIES.read_text())["server"]]
    assert [native.language(t) for t in tags] == codes
    assert (
        native.language("zh") == "cmn"
        and native.language("pt-BR") == "pt_br"
        and native.language("no") == "no"
    )


PRESETS = fixture("workflows")["workflows"][0]["studio"]["presets"]


@pytest.mark.parametrize(
    ("preset", "want"),
    [
        ("Neutral", {"condition_values": {"speaking_rate": 15}, "speaker_cfg_scale": 2}),
        ("Warm", {"controls": {"mood.valence": 1.6, "mood.arousal": -0.4},
                  "condition_values": {"speaking_rate": 14.25}, "speaker_cfg_scale": 2}),
        ("Tense", {"controls": {"mood.valence": -1.6, "mood.arousal": 2.0},
                   "condition_values": {"speaking_rate": 16.5}, "speaker_cfg_scale": 2.5}),
        ("Whisper", {"controls": {"mood.arousal": -2.0, "delivery.whisper": 1.35},
                     "condition_values": {"speaking_rate": 13.5}, "speaker_cfg_scale": 2}),
    ],
)  # fmt: skip
def test_delivery_maps_the_workflows_presets(preset, want):
    params = {k.removeprefix("speak."): v for k, v in PRESETS[preset].items()}
    assert native.delivery(params) == want


def test_delivery_at_its_neutral_values_sends_only_the_rate_and_the_ends_reach_the_safe_doses():
    flat = {"emotion": 0.5, "energy": 0.5, "whisper": 0, "speaking_rate": 1, "guidance": 1}
    assert native.delivery(flat) == {
        "condition_values": {"speaking_rate": 15}
    }  # 1: the middle bucket, never unknown
    ends = native.delivery(flat | {"emotion": 0, "energy": 1, "whisper": 1, "speaking_rate": 0.5})
    assert ends == {"controls": {"mood.valence": -4.0, "mood.arousal": 4.0, "delivery.whisper": 1.5},
                    "condition_values": {"speaking_rate": 7.5}}  # fmt: skip
    body = native.body(
        "Hi.", 2**32 + 7, flat | {"language": "en-GB"}, {"speaker_mode": "embedding"}
    )
    assert body == {"text": "Hi.", "stream": False, "format": "wav", "seed": 7, "language": "en_gb",
                    "condition_values": {"speaking_rate": 15}, "speaker_mode": "embedding"}  # fmt: skip


def clip(**kw) -> dict:
    data = QWEN_WAV.read_bytes()
    return {"take_id": "t1", "key": "u/o/t1.wav", "bytes": len(data), "etag": "e", "in": 0.1, "out": 3.1,
            "data": data} | kw  # fmt: skip


@pytest.mark.parametrize(
    ("part", "why"),
    [
        ({"in": 1.0, "out": 1.0}, "speak.voice: the clip's in must be before its out"),
        ({"in": -1.0, "out": 3.0}, "speak.voice: the clip's in must be before its out"),
        ({"in": 0.5, "out": 3.4}, "speak.voice: the clip lasts 2.9 s; a voice clip is 3 to 30 s"),
        ({"in": 0, "out": 30.5}, "speak.voice: the clip lasts 30.5 s; a voice clip is 3 to 30 s"),
    ],
)
def test_a_clip_outside_3_to_30_s_is_refused(part, why):
    with pytest.raises(RunError, match=f"^{why}$"):
        native.clip_part("speak", part)


def test_a_clip_at_3_s_in_floating_point_is_taken():
    # 4.1 - 1.1 is 2.9999999999999996: at the limit, as studio snaps it
    assert native.clip_part("speak", {"in": 1.1, "out": 4.1}) == (1.1, 4.1 - 1.1)


@pytest.mark.parametrize(
    ("language", "rate", "chars"),
    [("en-US", 1, 200), ("en-US", 0.5, 150), ("zh", 1, 100), ("ja", 0.5, 50), ("ko", 1.5, 150)],
)
def test_pieces_stay_well_under_the_servers_30_s(language, rate, chars):
    assert native.piece_chars({"language": language, "speaking_rate": rate}) == chars
    # 20 s at most at the language's pace: CJK about 5 characters a second, else 15, slowed by the rate
    assert chars / (5 if language in native.CJK else 15) / rate <= native.PIECE_S


def test_a_silent_or_missing_server_fails_with_a_readable_reason(monkeypatch):
    def hang(*a, **kw):
        raise TimeoutError("timed out")

    monkeypatch.setattr(native.urllib.request, "urlopen", hang)
    with pytest.raises(RunError, match=r"^speak: zonos3-server sent no answer within 300 s$"):
        native.generate("speak", 1, {"text": "x"})
    srv = http.server.HTTPServer(("127.0.0.1", 0), Refuse)
    port = srv.server_port
    srv.server_close()  # nobody listens there now
    monkeypatch.undo()
    with pytest.raises(RunError, match=r"^speak: zonos3-server could not be reached \("):
        native.generate("speak", port, {"text": "x"})


def test_the_clip_is_cut_to_its_part_as_a_mono_wav(tmp_path):
    out = native.cut_clip("speak", clip(), tmp_path / "clip.wav")
    with wave.open(str(out)) as w:
        assert (w.getnchannels(), w.getsampwidth(), w.getframerate()) == (1, 2, 24000)
        assert w.getnframes() == 3 * 24000
    assert native.cut_clip("speak", clip(), tmp_path / "again.wav").read_bytes() == out.read_bytes()
    assert sorted(p.name for p in tmp_path.iterdir()) == [
        "again.wav",
        "clip.wav",
    ]  # the take copy is deleted
    with pytest.raises(RunError, match=r"^speak.voice: cutting the clip of take t1 failed: "):
        native.cut_clip("speak", clip(data=b"not audio"), tmp_path / "bad.wav")


def test_voices_and_their_fields(tmp_path):
    gguf = tmp_path / "gguf"
    (gguf / "voices/English").mkdir(parents=True)
    (gguf / "voices/English/Savannah_en-American_F_1.embedding.npy").write_bytes(b"npy")
    preset = {
        "item_id": "zonos3/savannah",
        "version": 1,
        "source": "preset",
        "family": "zonos3",
        "speaker": "x",
    }
    assert native.speaker("speak", preset, gguf, tmp_path) == {"speaker_mode": "embedding",
                                                                "speaker_embedding_base64": "bnB5"}  # fmt: skip
    with pytest.raises(RunError, match=r"^speak.voice: ZONOS3 has no default voice zonos3/nobody$"):
        native.speaker("speak", preset | {"item_id": "zonos3/nobody"}, gguf, tmp_path)
    with pytest.raises(
        RunError, match=r"^speak.voice: ZONOS3 has no default voice qwen3-tts/ryan$"
    ):
        native.speaker(
            "speak", preset | {"item_id": "qwen3-tts/ryan", "family": "qwen3-tts"}, gguf, tmp_path
        )
    with pytest.raises(
        RunError, match=r"^speak.voice: ZONOS3 cannot speak a voice of source 'design'$"
    ):
        native.speaker("speak", {"source": "design", "text": "low"}, gguf, tmp_path)
    got = native.speaker("speak", {"source": "clip", "clip": clip()}, gguf, tmp_path)
    assert got["speaker_mode"] == "embedding" and got["speaker_audio_base64"].startswith(
        "UklGR"
    )  # RIFF


class Refuse(http.server.BaseHTTPRequestHandler):
    def do_POST(self):
        self.rfile.read(int(self.headers["Content-Length"]))
        body = json.dumps(
            {"error": "steering: Unknown control 'nope' in /Users/me/x.gguf; see /v2/controls."}
        )
        self.send_response(400)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(body.encode())

    def log_message(self, *a):
        pass


def test_a_refused_request_fails_with_the_servers_message_without_paths():
    srv = http.server.HTTPServer(("127.0.0.1", 0), Refuse)
    threading.Thread(target=srv.handle_request, daemon=True).start()
    try:
        with pytest.raises(RunError) as e:
            native.generate("speak", srv.server_port, {"text": "x"})
    finally:
        srv.server_close()
    assert str(e.value) == (
        "speak: zonos3-server refused the request (400): steering: Unknown control 'nope' in <file> see <file>"
    )


def test_the_server_must_send_mono_16_bit_at_44_1_khz():
    buf = io.BytesIO()
    with wave.open(buf, "wb") as w:
        w.setnchannels(1), w.setsampwidth(2), w.setframerate(24000), w.writeframes(bytes(4))
    with pytest.raises(RunError, match=r"not 16-bit mono at 44100 Hz"):
        native.pcm("speak", buf.getvalue())
    with pytest.raises(RunError, match=r"^speak: zonos3-server sent no WAV"):
        native.pcm("speak", b"nope")


def test_empty_or_long_text_fails_before_the_server_starts():
    for text, why in [("  ", "speak: nothing to say"),
                      ("x" * 901, "speak: at most 900 characters (about 60 s of speech), not 901")]:  # fmt: skip
        with pytest.raises(RunError, match=f"^{re.escape(why)}$"):
            native.speak(type("C", (), {"node": "speak"})(), {}, {"text": text})


def test_a_voices_clip_is_read_like_a_media_input():
    spec = [{"name": "voice", "type": "voice", "accepts": ["preset", "clip"]}]
    v = {"item_id": "i", "version": 1, "source": "clip",
         "clip": {"take_id": "t1", "key": "u/o/t1.wav", "bytes": 3, "etag": "e", "in": 0, "out": 3}}  # fmt: skip
    seen = []

    async def fetch(key, limit, etag):
        seen.append((key, limit, etag))
        return b"wav"

    got = asyncio.run(images.load(spec, {"voice": v}, "o", fetch))
    assert got["voice"]["clip"]["data"] == b"wav" and seen == [("u/o/t1.wav", 3, "e")]
    assert images.media(spec, {"voice": {"source": "preset"}}) == []
    with pytest.raises(RunError, match="is not the run owner's"):
        asyncio.run(images.load(spec, {"voice": v}, "someone-else", fetch))


def test_argv_asks_for_the_gpu():
    """Off Apple the server defaults to the CPU (zonos3.h ZONOS3_DEFAULT_USE_GPU): --gpu makes it CUDA there."""
    assert "--gpu" in native.argv(Path("/w/gguf"), Path("/b/zonos3-server"))
