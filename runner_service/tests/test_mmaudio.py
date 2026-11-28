"""The mmaudio pack without a GPU or its weights: its models entry equals the canonical fixture (nodes and workflows:
test_packs), its download planned against the fake hub (three repos, each into its own folder, only the files the
code loads, a licence to accept), the frame picking of read_frames, and the video stage's cut and mux around a
stubbed generate. The real model: tests/test_mmaudio_golden.py (-m gpu)."""

import json
import subprocess
import types

import numpy as np
import pytest
from conftest import CONTRACTS
from fake_hub import FakeHub, Repo

import config
from core import download, media, models
from core import pack as packs
from core.graph import RunError

mm = pytest.importorskip("packs.mmaudio.torch", reason="the torch env")


@pytest.fixture(scope="module")
def values():
    from core import registry

    return registry.build("torch")[0]


def test_the_models_entry_matches_the_fixture(values):
    (got,) = [m for m in values["models"]["models"] if m["family"] == "mmaudio"]
    (want,) = json.loads((CONTRACTS / "fixtures/registry/mmaudio.models.valid.json").read_text())["models"]
    top = ("id", "family", "title", "media", "runtimes", "sound")
    assert {k: got[k] for k in top} == {k: want[k] for k in top}
    keys = ("id", "title", "precisions", "source", "sources", "dir")
    assert [{k: v[k] for k in keys} for v in got["variants"]] == [{k: v[k] for k in keys} for v in want["variants"]]
    for g, w in zip(got["variants"], want["variants"], strict=True):
        if g["presence"] == "present":  # on a host with the weights: the same sizes and memory plans
            assert (g["components"], g["memory"]) == (w["components"], w["memory"])


# --- the download, on the fake hub

BLOB = bytes(range(256)) * 40


def hub_files() -> dict[str, dict[str, bytes]]:
    """Each repo with the files the code loads and some it never reads."""
    return {
        "hkchengrex/MMAudio": {"weights/mmaudio_large_44k_v2.pth": BLOB, "weights/mmaudio_small_16k.pth": b"no",
                               "ext_weights/v1-44.pth": BLOB[::-1], "ext_weights/v1-16.pth": b"no",
                               "ext_weights/synchformer_state_dict.pth": BLOB[:999], "README.md": b"no"},
        "apple/DFN5B-CLIP-ViT-H-14-384": {"open_clip_config.json": b"{}", "open_clip_pytorch_model.bin": BLOB[:777],
                                          "pytorch_model.bin": b"no: the transformers copy", "LICENSE": b"no"},
        "nvidia/bigvgan_v2_44khz_128band_512x": {"config.json": b"{}", "bigvgan_generator.pt": BLOB[:555],
                                                 "bigvgan_discriminator_optimizer.pt": b"no"},
    }  # fmt: skip


def test_the_download_fills_three_folders_with_only_the_loaded_files(tmp_path, monkeypatch):
    monkeypatch.setattr(config, "MODELS", tmp_path / "models")
    monkeypatch.setattr(models, "hashes", models.Hashes())
    (tmp_path / "models").mkdir()
    p, _ = packs.load(packs.PACKS / "mmaudio", "torch")
    v = p.spec["variants"]["large_44k_v2"]
    revs = {v["source"]["repo"]: v["source"]["revision"]} | {s["repo"]: s["revision"] for s in v["sources"]}
    repos = {name: Repo(files, commit=revs[name]) for name, files in hub_files().items()}
    assert v["source"]["must_accept"] and models.licences([p])[0]["id"] == "mmaudio-weights"
    with FakeHub(repos) as hub:
        result = download.download(p, "large_44k_v2", endpoint=hub.url, token=None, cache=tmp_path / "cache",
                                   host="t", progress=download.Progress())  # fmt: skip
    have = sorted(x.relative_to(tmp_path / "models").as_posix() for x in (tmp_path / "models").rglob("*")
                  if x.is_file() and ".cache" not in x.parts)  # fmt: skip
    assert have == ["DFN5B-CLIP-ViT-H-14-384/open_clip_config.json",
                    "DFN5B-CLIP-ViT-H-14-384/open_clip_pytorch_model.bin",
                    "MMAudio/ext_weights/synchformer_state_dict.pth", "MMAudio/ext_weights/v1-44.pth",
                    "MMAudio/weights/mmaudio_large_44k_v2.pth", "bigvgan_v2_44khz_128band_512x/bigvgan_generator.pt",
                    "bigvgan_v2_44khz_128band_512x/config.json"]  # fmt: skip
    assert result["files"] == 7 and not any(r[3] for r in hub.requests)  # no token asked: none is gated
    entry = models.entry(p)
    assert entry["variants"][0]["presence"] == "present"
    # the components read from their own folders: the CLIP and the vocoder from their `dir`
    m = packs.ModelRef(p, "large_44k_v2", "bf16")
    assert m.files("clip") == [tmp_path / "models/DFN5B-CLIP-ViT-H-14-384/open_clip_pytorch_model.bin"]
    assert m.files("bigvgan") == [tmp_path / "models/bigvgan_v2_44khz_128band_512x/bigvgan_generator.pt"]
    assert m.gb("net") == (len(BLOB) // 4 * 2 / 2**30, 0.0)  # stored fp32, loads bf16


# --- frames and the video stage


@pytest.fixture
def clip(tmp_path):
    """A 1.5 s, 24 fps, 96 x 64 H.264 test clip without sound."""
    path = tmp_path / "clip.mp4"
    subprocess.run([media.FFMPEG or "ffmpeg", "-v", "error", "-y", "-f", "lavfi", "-i",
                    "testsrc=size=96x64:rate=24:duration=1.5", "-c:v", "libx264", "-pix_fmt", "yuv420p",
                    str(path)], check=True)  # fmt: skip
    return path


def test_read_frames_picks_8_and_25_fps_and_truncates_to_the_last_frame(clip):
    f = mm.read_frames(clip, 96, 64, 24, 1.0)
    assert f.clip.shape == (1, 8, 3, 384, 384) and f.sync.shape == (1, 25, 3, 224, 224) and f.seconds == 1.0
    assert 0 <= f.clip.min() and f.clip.max() <= 1 and -1 <= f.sync.min() and f.first.size == (96, 64)
    whole = mm.read_frames(clip, 96, 64, 24, 1.5)  # the last frame is at 35 / 24 s: 37 sync slots, 1.48 s
    assert whole.seconds == pytest.approx(1.48) and whole.sync.shape[1] == 37 and whole.clip.shape[1] == 11


def source(path) -> dict:
    data = path.read_bytes()
    return {"take_id": "t1", "kind": "video", "key": "u/o/t1.mp4", "bytes": len(data), "etag": "e", "data": data,
            "width": 96, "height": 64, "fps": 24.0, "frames": 36, "duration_s": 1.5, "has_audio": False}  # fmt: skip


def run_foley(monkeypatch, clip, seconds: float) -> tuple[dict, list]:
    seen = []

    def generate(ctx, comps, text, avoid, secs, seed, guidance, steps, frames=None):
        seen.append((text, secs, frames.clip.shape[1]))
        return np.full(round(secs * mm.RATE), 0.25, dtype=np.float32)

    monkeypatch.setattr(mm, "generate", generate)
    ctx = types.SimpleNamespace(node="foley", component=lambda *a: None, check=lambda: None, progress=lambda *a: None)
    params = {"source": source(clip), "seconds": seconds, "avoid": "", "guidance": 4.5, "steps": 25}
    return mm.foley(ctx, params, {"model": None, "text": " a truck ", "seed": 1}), seen


def test_foley_cuts_the_picture_to_length_and_muxes_the_sound(monkeypatch, clip, tmp_path):
    out, seen = run_foley(monkeypatch, clip, 1.0)
    assert seen == [("a truck", 1.0, 8)]
    assert out["audio"]["sample_rate"] == 44100 and out["audio"]["duration_s"] == 1.0
    v = out["video"]
    (tmp_path / "out.mp4").write_bytes(v["mp4"])
    info = media.probe(tmp_path / "out.mp4")
    assert info["stream"]["codec"] == "h264" and info["audio"]["codec_name"] == "aac" and v["has_audio"]
    assert 24 <= v["frames"] <= 27 and v["fps"] == 24 and v["poster"].size == (96, 64)  # stream copy: a packet or two


def test_foley_auto_takes_the_whole_video_and_refuses_a_length_past_it(monkeypatch, clip):
    out, seen = run_foley(monkeypatch, clip, 0)
    assert seen[0][1] == pytest.approx(1.48) and out["video"]["frames"] == 36  # the picture as it is
    with pytest.raises(RunError, match=r"^foley: Length 2 s is past the video's end \(1\.50 s\)$"):
        run_foley(monkeypatch, clip, 2.0)


def test_sound_needs_a_description():
    ctx = types.SimpleNamespace(node="sound")
    with pytest.raises(RunError, match="^sound: describe the sound$"):
        mm.sound(
            ctx,
            {"avoid": "", "seconds": 2, "guidance": 4.5, "steps": 25},
            {"model": None, "text": " ", "seed": 1},
        )
