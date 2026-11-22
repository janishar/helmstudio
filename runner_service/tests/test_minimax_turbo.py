"""MiniMax H3 Turbo (API 1.41, plan 20 24.11): the lora param picks the add-on by partition (Turbo LoRA on FL2VA,
Turbo Ref LoRA with any reference), h3 gets it as H3_LORA with every weight copied, SGLang as --lora-path, the peak
gains 4.5 GB, the Turbo preset's flags, and the pack refuses a wrong add-on per value. No GPU, no h3 run."""

import json
from pathlib import Path

import pytest

import config
from core import pack
from core.graph import RunError
from packs.minimax_h3 import native, sgl

PACK = Path(native.__file__).parent
WORKFLOWS = sorted((PACK / "workflows").glob("*.json"))


class Model:
    """A Base model handle whose add-on files are `have` (component → file) under tmp."""

    variant = "base"

    def __init__(self, tmp: Path, have: dict[str, str], variant: str = "base") -> None:
        self.tmp, self.have, self.variant = tmp, have, variant
        self.pack = pack.load(PACK, "native")[0]

    def folder(self, component=None):
        return self.tmp / "MiniMax-H3"

    def files(self, component):
        return [self.tmp / self.have[component]] if component in self.have else []


FILES = {"turbo": "fl2v.safetensors", "turbo_ref": "ref2v.safetensors"}
REF = [{"image": object()}]


def test_the_lora_value_picks_the_add_on_by_partition(tmp_path):
    m = Model(tmp_path, FILES)
    pick = lambda params, inputs=None: native.lora_file(
        "generate", m, params, inputs or {"model": m}
    )
    assert pick({"lora": "none"}) is None and pick({}) is None  # the default asks for nothing
    assert pick({"lora": "turbo", "refs": []}) == (tmp_path / FILES["turbo"]).resolve()
    for rows in ("refs", "videos", "audios"):  # any reference runs Ref2VA: its own file
        assert pick({"lora": "turbo", rows: REF}) == (tmp_path / FILES["turbo_ref"]).resolve()
    assert (
        pick({"lora": "turbo"}, {"model": m, "first": object()})
        == (tmp_path / FILES["turbo"]).resolve()
    )
    with pytest.raises(
        RunError, match=f"^generate: Turbo Ref LoRA not downloaded on host {config.HOST}$"
    ):
        native.lora_file("generate", Model(tmp_path, {"turbo": "fl2v.safetensors"}), {"lora": "turbo", "refs": REF},
                         {})  # fmt: skip
    with pytest.raises(RunError, match="^generate: Turbo LoRA not downloaded on host"):
        native.lora_file("generate", Model(tmp_path, {}), {"lora": "turbo"}, {})


def test_h3_gets_the_file_with_every_weight_copied_and_none_never_merges():
    assert native.lora_env(Path("/m/t.safetensors")) == {
        "H3_LORA": "/m/t.safetensors", "H3_LORA_STRENGTH": "1", "H3_ZERO_COPY_WEIGHTS": "0"}  # fmt: skip
    assert native.lora_env(None) == {
        "H3_LORA": ""
    }  # a stray H3_LORA of the runner's environment is overridden


def test_generate_hands_h3_the_lora_env_and_8_steps(tmp_path, monkeypatch):
    from core import child

    seen = {}

    class Stop(Exception):
        pass

    def run(ctx, argv, **kw):
        seen.update(argv=argv, env=kw.get("env"))
        raise Stop

    monkeypatch.setattr(child, "run", run)
    monkeypatch.setattr(native, "BINARY", tmp_path / "h3")
    (tmp_path / "h3").write_text("")

    class Ctx:
        node = "generate"

        def progress(self, *a):
            pass

    m = Model(tmp_path, FILES)
    turbo = json.loads(WORKFLOWS[0].read_text())["studio"]["presets"]["Turbo"]
    params = {k.split(".")[1]: v for k, v in turbo.items()} | {
        "refs": [],
        "videos": [],
        "audios": [],
    }
    inputs = {"model": m, "prompt": "a fox", "seed": 1, "canvas": {"width": 512, "height": 512},
              "duration": {"frames": 22, "fps": 24}}  # fmt: skip
    with pytest.raises(Stop):
        native.generate(Ctx(), params, inputs)
    a = seen["argv"]
    assert a[a.index("--steps") + 1] == "8" and "--lora" not in " ".join(a)  # argv as without it
    assert seen["env"] == native.lora_env((tmp_path / FILES["turbo"]).resolve())
    with pytest.raises(Stop):
        native.generate(Ctx(), params | {"lora": "none"}, inputs)
    assert seen["env"] == {"H3_LORA": ""}


def test_sglang_gets_the_partitions_file_as_lora_path(tmp_path, monkeypatch):
    m = Model(tmp_path, FILES | {"transformer_fl2va": "fl.st", "transformer_ref2va": "ref.st",
                                 "text_encoder": "te.st"}, variant="base-fp8")  # fmt: skip
    a = sgl.argv(m, "fl2va", 1, tmp_path)
    assert "--lora-path" not in a
    a = sgl.argv(m, "ref2va", 1, tmp_path, tmp_path / "ref2v.safetensors")
    assert a[a.index("--lora-path") + 1] == str(tmp_path / "ref2v.safetensors")
    got = {}
    monkeypatch.setattr(
        sgl, "generate", lambda ctx, model, body, out, tmp, lora=None: got.update(lora=lora, b=body)
    )
    monkeypatch.setattr(native, "video_value", lambda out, tmp: {})

    class Ctx:
        node = "generate"

    inputs = {"model": m, "prompt": "a fox", "seed": 1, "canvas": {"width": 864, "height": 480},
              "duration": {"frames": 124, "fps": 24}}  # fmt: skip
    params = {"steps": 8, "lora": "turbo", "refs": [], "flow_shift": 12, "audio_flow_shift": 3}
    native.generate(Ctx(), params, inputs)
    assert (
        got["lora"] == (tmp_path / FILES["turbo"]).resolve()
        and got["b"]["num_inference_steps"] == 8
    )
    assert (
        "flow_shift" not in got["b"] and "audio_flow_shift" not in got["b"]
    )  # the model's shifts: none sent


def test_the_peak_gains_4_5_gb_on_turbo():
    _, nodes = pack.load(PACK, "native")
    code = nodes["minimax-h3/generate"][1].peak
    at = {"mp": 0.4147, "frames": 124, "render": "Full", "videos": []}
    base = pack.evaluate(code, at | {"lora": "none"})
    assert base == pytest.approx(26.57, abs=0.01)
    assert pack.evaluate(code, at | {"lora": "turbo"}) == pytest.approx(base + 4.5)


def test_turbo_after_fast_by_time_and_every_other_preset_turns_the_lora_off():
    for f in WORKFLOWS:
        studio = json.loads(f.read_text())["studio"]
        assert studio["preset_order"] == ["Draft", "Fast", "Turbo", "Default", "Reference"], f.name
        lora = {n: p["generate.lora"] for n, p in studio["presets"].items()}
        assert lora == {
            "Draft": "none",
            "Turbo": "turbo",
            "Fast": "none",
            "Default": "none",
            "Reference": "none",
        }
        assert studio["presets"]["Turbo"] | {} == {
            "generate.steps": 8, "generate.layers": 50, "generate.reuse": 1, "generate.token_reduction": False,
            "generate.core_reuse": 1, "generate.lora": "turbo", "generate.flow_shift": 12,
            "generate.audio_flow_shift": 3}  # fmt: skip


def test_a_wrong_add_on_per_value_is_refused():
    schema = {"inputs": [{"name": "first", "type": "Image", "optional": True}],
              "params": [{"name": "refs", "type": "rows"},
                         {"name": "lora", "type": "enum", "enum": ["none", "turbo"], "addons": {"turbo": ["turbo"]},
                          "addons_with": {"refs": {"turbo": ["turbo_ref"]}}}]}  # fmt: skip
    ok = ["turbo", "turbo_ref"]
    assert pack.value_addon_errors(schema, ok) is None
    lora = schema["params"][1]
    for change, why in [({"addons": {"turbo4": ["turbo"]}}, "must key values"),
                        ({"addons_with": {"seed": {"turbo": ["turbo_ref"]}}}, "names no optional input"),
                        ({"addons_with": {"refs": {"none": ["turbo_ref"]}}}, "must key values of its addons"),
                        ({"addons": {"turbo": ["detailer"]}}, "names no add-on")]:  # fmt: skip
        assert why in pack.value_addon_errors(
            {**schema, "params": [schema["params"][0], lora | change]}, ok
        )
    assert pack.value_addons(lora, "turbo", {"first"}) == ["turbo"]
    assert pack.value_addons(lora, "turbo", {"refs"}) == ["turbo_ref"]
    assert pack.value_addons(lora, "none", {"refs"}) == []


def test_two_add_ons_in_one_folder_go_by_their_own_files(tmp_path, monkeypatch):
    """MiniMax-H3-Turbo holds both: each one's presence, unfinished marker and delete are its own file's."""
    from core import models

    monkeypatch.setattr(config, "MODELS", tmp_path)
    p = pack.load(PACK, "native")[0]
    root = tmp_path / "MiniMax-H3-Turbo"
    root.mkdir()
    (root / "minimax_h3_fl2v_turbo_8step_v1.2_768p_bf16.safetensors").write_bytes(b"x" * 8)
    state = lambda: {c: models.addon_entry(p, c)["presence"] for c in ("turbo", "turbo_ref")}
    assert state() == {"turbo": "present", "turbo_ref": "missing"}
    assert models.addon_entry(p, "turbo")["bytes"] == 8  # never the other's file
    (root / models.MARKER).parent.mkdir(parents=True)
    (root / models.MARKER).write_text(
        '{"repo": "lightx2v/Minimax-h3-Turbo", "variant": "turbo_ref"}\n'
    )
    (root / "minimax_h3_ref2v_turbo_8step_v1.1_768p_bf16.safetensors").write_bytes(b"y" * 4)
    assert state() == {
        "turbo": "present",
        "turbo_ref": "partial",
    }  # turbo_ref's download is unfinished
    (root / models.MARKER).unlink()
    assert state() == {"turbo": "present", "turbo_ref": "present"}
    copy = (
        root
        / models.SGLANG_COPIES
        / "minimax_h3_ref2v_turbo_8step_v1.1_768p_bf16-0123456789ab.safetensors"
    )
    other = (
        root
        / models.SGLANG_COPIES
        / "minimax_h3_fl2v_turbo_8step_v1.2_768p_bf16-0123456789ab.safetensors"
    )
    for f in (copy, other):
        f.parent.mkdir(parents=True, exist_ok=True)
        f.write_bytes(b"z")
    assert models.delete_addon(p, "turbo_ref") == (
        1,
        4,
    )  # its SGLang-ready copy goes with it, the other's stays
    assert state() == {"turbo": "present", "turbo_ref": "missing"}
    assert not copy.exists() and other.exists()


def test_sglang_gets_a_lora_copy_with_alpha_folded(tmp_path):
    """sgl.ready_lora: lora_B × alpha / rank (the module's .alpha, else the metadata's, else the rank: h3_lora.c's), no
    alpha left for SGLang, bf16 kept, written once next to the file and reused."""
    torch = pytest.importorskip("torch")
    from safetensors import safe_open
    from safetensors.torch import load_file, save_file

    def lora(name, with_alpha, meta):
        g, t = torch.Generator().manual_seed(0), {}
        for m, r in (("blocks.0.attn.to_q", 4), ("blocks.0.attn.to_k", 2)):
            t[f"{m}.lora_A.default.weight"] = torch.randn(r, 8, generator=g).bfloat16()
            t[f"{m}.lora_B.default.weight"] = torch.randn(8, r, generator=g).bfloat16()
            if with_alpha:
                t[f"{m}.alpha"] = torch.tensor(float(r * 2)).bfloat16()  # alpha 2 × rank: scale 2
        save_file(t, str(tmp_path / name), metadata=meta)
        return t

    cases = {"own.safetensors": (True, {"alpha": "128", "format": "pt"}, lambda r: 2.0),
             "meta.safetensors": (False, {"alpha": "8", "format": "pt"}, lambda r: 8 / r),
             "none.safetensors": (False, {"format": "pt"}, lambda r: 1.0)}  # fmt: skip
    for name, (with_alpha, meta, scale) in cases.items():
        src = lora(name, with_alpha, meta)
        out = sgl.ready_lora(tmp_path / name)
        assert out.parent == tmp_path / ".cache/helmstudio/sglang" and out.name.startswith(
            name[:-12] + "-"
        )
        got = load_file(str(out))
        assert not any(k.endswith(".alpha") for k in got)
        with safe_open(str(out), "pt") as f:
            assert f.metadata() == {"format": "pt"}
        for k, v in got.items():
            assert v.dtype == torch.bfloat16
            r = src[k].shape[0 if ".lora_A." in k else 1]
            want = src[k].float() * (scale(r) if ".lora_B." in k else 1)
            assert torch.equal(v, want.bfloat16()), (name, k)
    t = out.stat().st_mtime_ns
    assert (
        sgl.ready_lora(tmp_path / "none.safetensors") == out and out.stat().st_mtime_ns == t
    )  # reused
    lora(
        "none.safetensors", False, {"format": "pt", "x": "1"}
    )  # replaced: a new copy, the old one gone
    new = sgl.ready_lora(tmp_path / "none.safetensors")
    assert new != out and not out.exists() and new.exists()
