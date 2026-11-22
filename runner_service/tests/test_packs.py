"""Pack → registry values: the generated node schemas, workflows and models entries equal the canonical contract
fixtures, so a pack cannot drift from the contract."""

import json
from pathlib import Path

import pytest
from conftest import CONTRACTS, FakeQwen, readme_model_yaml, safetensors_file

from core import pack, registry
from messaging import schemas

FIXTURES = CONTRACTS / "fixtures/registry"


def fixture(name: str) -> dict:
    return json.loads((FIXTURES / name).read_text())


def test_readme_example_generates_the_canonical_qwen_image_nodes(tmp_path, monkeypatch):
    q = FakeQwen(tmp_path, monkeypatch)
    want = fixture("qwen-image.nodes.valid.json")["nodes"]
    assert list(q.schemas.values()) == want


@pytest.mark.parametrize("family", ["qwen-image", "minimax-h3-image", "qwen3-tts"])
def test_real_torch_packs_match_the_fixtures(family):
    """The torch runner publishes both families merged (contracts README): each family's canonical nodes, in its
    order (the core kinds they share are identical), and its workflows."""
    pytest.importorskip("torch")
    values, _, _ = registry.build("torch")
    want = fixture(f"{family}.nodes.valid.json")["nodes"]
    kinds = {n["kind"] for n in want}
    assert [n for n in values["nodes"]["nodes"] if n["kind"] in kinds] == want
    got = [w for w in values["workflows"]["workflows"] if w["family"] == family]
    assert got == fixture(f"{family}.workflows.valid.json")["workflows"]


@pytest.mark.parametrize("env", ["torch", "mlx"])
def test_echo_pack_is_published_for_env_fake_only(env):
    pytest.importorskip(env)
    assert [p.name for p, _ in pack.for_env("fake")] == ["echo"]
    assert all(p.name != "echo" for p, _ in pack.for_env(env))


def test_models_entry_measures_presence_and_own_bytes(tmp_path, monkeypatch):
    q = FakeQwen(tmp_path, monkeypatch)
    entry = pack.models_entry(q.pack)
    base, turbo = entry["variants"]
    assert base["presence"] == turbo["presence"] == "present"
    # base: transformer (64*32 bf16) + two 16*8 bf16 files; turbo: its own transformer only
    header = len((tmp_path / "Qwen-Image-2.1-Turbo/transformer/turbo.safetensors").read_bytes())
    assert turbo["bytes"] == header and base["bytes"] > turbo["bytes"]
    assert base["precisions"] == ["bf16", "int8"]
    value = {
        "schema_version": 1,
        "host": "mac",
        "env": "torch",
        "published_at": "2026-11-14T10:00:00Z",
    }
    value |= {"published_by": "t", "models": [entry]}
    assert schemas.registry_errors("models", value) == []
    (tmp_path / "Qwen-Image-2.1/vae/base.safetensors").unlink()
    base, turbo = pack.models_entry(q.pack)["variants"]
    assert base["presence"] == turbo["presence"] == "partial"  # turbo shares base's vae
    assert base["bytes"] < header * 3 and "vae" not in {f.get("component") for f in base["files"]}
    for d in ("Qwen-Image-2.1", "Qwen-Image-2.1-Turbo"):
        for f in (tmp_path / d).rglob("*.safetensors"):
            f.unlink()
    base, turbo = pack.models_entry(q.pack)["variants"]
    assert base["presence"] == turbo["presence"] == "missing" and "bytes" not in base


def test_component_size_is_computed_at_the_target_dtype(tmp_path, monkeypatch):
    q = FakeQwen(tmp_path, monkeypatch)
    f = tmp_path / "Qwen-Image-2.1/vae/base.safetensors"
    safetensors_file(f, {"a": ("F32", [10, 10])})  # stored fp32, loads as bf16
    bf16 = pack.ModelRef(q.pack, "base", "bf16")
    assert bf16.gb("vae") == (
        200 / 2**30,
        400 / 2**30,
    )  # (resident, transient: largest tensor in fp32)
    int8 = pack.ModelRef(q.pack, "turbo", "int8")
    # a quantized Linear weight: 1 byte per value + a bf16 scale per row
    assert int8.gb("transformer")[0] == (64 * 32 + 64 * 2) / 2**30
    assert int8.key("vae") == ("qwen-image-2.1", "base", "vae", "bf16")  # shared, offers only bf16
    assert int8.key("text_encoder") == ("qwen-image-2.1", "base", "text_encoder", "int8")


@pytest.mark.parametrize(
    ("formula", "values", "want"),
    [
        ("0.9 + 5.1 * mp", {"mp": 1.0}, 6.0),
        ("6 if tiled == 'on' else 8.5 * mp", {"mp": 2.0, "tiled": "on"}, 6.0),
        ("6 if tiled == 'on' else 8.5 * mp", {"mp": 2.0, "tiled": "auto"}, 17.0),
        ("max(2, mp) * 2", {"mp": 1.0}, 4.0),
    ],
)
def test_peak_formulas(formula, values, want):
    code = pack.compile_formula(formula, set(values))
    assert pack.evaluate(code, values) == pytest.approx(want)


@pytest.mark.parametrize(
    "formula", ["__import__('os')", "mp.real", "open('x')", "nope * 2", "[1][0]", "lambda: 1"]
)
def test_peak_formulas_refuse_anything_else(formula):
    with pytest.raises(ValueError):
        pack.compile_formula(formula, {"mp"})


def test_readme_yaml_quotes_on_and_off():
    tiled = readme_model_yaml()["stages"]["decode"]["params"]["tiled"]
    assert tiled["enum"] == ["auto", "on", "off"]


@pytest.mark.parametrize("env", ["fake", "torch", "mlx", "native"])
def test_published_nodes_equal_the_canonical_palette(env):
    """Each env's node schemas (title and help from model.yaml / nodes.yaml included) are the canonical palette's
    kinds of that env (contracts/fixtures/graphs/palette.json, without its fictional `test` family), and each
    family's group title is the model's title."""
    if env in ("torch", "mlx"):
        pytest.importorskip(env)
    palette = json.loads((CONTRACTS / "fixtures/graphs/palette.json").read_text())["groups"]
    want = {
        n["kind"]: {k: v for k, v in n.items() if k != "envs"}
        for g in palette
        for n in g["nodes"]
        if env in n["envs"] and n["family"] != "test"
    }
    values, _, _ = registry.build(env)
    assert {n["kind"]: n for n in values["nodes"]["nodes"]} == want
    titles = {g["family"]: g["title"] for g in palette}
    assert all(titles[m["family"]] == m["title"] for m in values["models"]["models"])


def test_stage_inputs_may_be_optional_and_never_share_a_param_name():
    stage = {
        "title": "Mix",
        "inputs": {"a": "Image", "b": {"type": "Image", "optional": True}},
        "params": {},
    }
    assert pack.stage_schema("mix", stage, "t")["inputs"] == [
        {"name": "a", "type": "Image"},
        {"name": "b", "type": "Image", "optional": True},
    ]
    stage["params"] = {"a": {"type": "int", "default": 1}}
    import types

    with pytest.raises(ValueError, match="share the names"):
        pack._nodes({"mix": stage}, "t", "c", None, types.SimpleNamespace(mix=lambda *a: {}))


def real_qwen_lora_block() -> dict:
    pytest.importorskip("torch")
    if not (pack.models_root() / "Qwen-Image-2.1" / "transformer" / "config.json").is_file():
        pytest.skip("the Qwen-Image-2.1 configs are not on this host")
    values, _, _ = registry.build("torch")
    return next(m for m in values["models"]["models"] if m["family"] == "qwen-image")["lora"]


def test_real_qwen_lora_block_comes_from_the_meta_model():
    """No weights are read: build() on the meta device lists the transformer's Linears."""
    from core import lora

    block = real_qwen_lora_block()
    mods = lora.expand(block["modules"])
    assert block["component"] == "transformer" and block["modules"] == sorted(block["modules"])
    assert len({m.replace(".", "_") for m in mods}) == len(mods)  # kohya names stay unique
    assert "transformer_blocks.0.attn.to_q" in mods and lora.compress(mods) == block["modules"]


def test_real_qwen_lora_block_equals_the_contract_fixture():
    want = fixture("qwen-image.models.valid.json")["models"][0]["lora"]
    assert real_qwen_lora_block() == want


def test_lora_stack_node_is_generated_from_the_lora_block(tmp_path, monkeypatch):
    q = FakeQwen(tmp_path, monkeypatch)
    want = next(n for n in fixture("qwen-image.nodes.valid.json")["nodes"] if n["kind"] == "qwen-image/lora-stack")  # fmt: skip
    assert pack.lora_stack_schema(q.pack) == want
    assert schemas.registry_errors("loras", fixture("qwen-image.loras.valid.json")) == []


def test_a_lora_block_with_a_runtime_holds_only_for_that_runtime():
    from types import SimpleNamespace

    spec = {"lora": {"runtime": "mlx", "component": "transformer"}}
    assert pack.lora_spec(SimpleNamespace(spec=spec, runtime="mlx")) == spec["lora"]
    assert pack.lora_spec(SimpleNamespace(spec=spec, runtime="torch")) is None
    assert pack.lora_spec(SimpleNamespace(spec={"lora": {"component": "t"}}, runtime="torch"))


def test_the_device_of_a_runtime_whose_framework_is_absent_is_the_hosts(monkeypatch):
    import sys
    from types import SimpleNamespace

    from core import models

    # as in the mlx env: no torch to import
    monkeypatch.setitem(sys.modules, "core.loaders_torch", None)
    host = "mps" if sys.platform == "darwin" else "cpu"
    assert models.device(SimpleNamespace(runtime="torch")) == host


def test_the_code_hash_covers_every_top_level_module_of_the_pack(tmp_path):
    (tmp_path / "model.yaml").write_text("id: x\n")
    (tmp_path / "mlx.py").write_text("")
    first = pack.code_hash(tmp_path)
    (tmp_path / "vendor").mkdir()
    (tmp_path / "vendor/lib.py").write_text("x = 1\n")
    assert pack.code_hash(tmp_path) == first  # vendored code is pinned by the pack's own modules
    (tmp_path / "common.py").write_text("x = 1\n")
    assert pack.code_hash(tmp_path) != first


def test_echo_lora_block_fits_the_contract_lora_fixtures():
    """The fake echo block names the modules of the contract LoRA fixtures that fit qwen-image, so they fit echo on
    a fake-only stack (no GPU); where qwen-image is also published, detection prefers it (contracts README)."""
    from core import lora

    (echo,) = [p for p, _ in pack.for_env("fake") if p.family == "echo"]
    block = pack.lora_block(echo)
    for f in sorted((CONTRACTS / "fixtures/loras").glob("*.json")):
        fx = json.loads(f.read_text())
        if fx["expect"]["family"] == "qwen-image" and "ignore" not in fx:
            assert lora.normalise(fx["name"], fx["tensors"], block, "echo")["error"] is None, f.name
    assert block == fixture("models.valid.json")["models"][0]["lora"]


@pytest.mark.parametrize("variant", ["base", "turbo"])
def test_memory_plan_lists_components_in_order_of_last_use_and_the_warm_peak(
    tmp_path, monkeypatch, variant
):
    """models[].variants[].memory (the hand-over): the canonical workflow at its default sizes (1024², no refs):
    decode's 8.5 * mp is the largest stage peak, on top of every kept component. The enhance node's add-ons come
    first (the default model's, unloaded after it; not on this host, so 0 GB)."""
    q = FakeQwen(tmp_path, monkeypatch)
    q.pack.workflows = fixture("qwen-image.workflows.valid.json")["workflows"]
    plan = registry.memory_plan(q.pack, variant, "int8", q.impls, q.schemas)
    owner = {"text_encoder": "base", "vae": "base", "transformer": variant}
    pes, comps = plan["components"][:2], plan["components"][2:]
    assert [(c["component"], c["variant"], c["precision"], c["gb"], c["unloads_after"]) for c in pes] == [
        ("text_enhancer", "base", "bf16", 0, True), ("edit_enhancer", "base", "bf16", 0, True)]  # fmt: skip
    want = [
        (c, owner[c], "bf16" if c == "vae" else "int8")
        for c in ("text_encoder", "transformer", "vae")
    ]
    assert [(c["component"], c["variant"], c["precision"]) for c in comps] == want
    kept = sum(c["gb"] for c in comps)
    assert plan["precision"] == "int8" and not any("unloads_after" in c for c in comps)
    assert plan["peak_gb"] == pytest.approx(kept + 8.5 * 1024 * 1024 / 1e6, abs=0.01)
    assert plan["planned_at"] == {"mp": 1.0486, "frames": 0}  # a larger run has no plan (studio)
    entry = pack.models_entry(q.pack)
    entry["variants"][0]["memory"] = [plan]
    value = {"schema_version": 1, "host": "mac", "env": "torch", "published_at": "2026-11-14T10:00:00Z",
             "published_by": "t", "models": [entry]}  # fmt: skip
    assert schemas.registry_errors("models", value) == []


def test_memory_plan_marks_unload_after_and_leaves_it_out_of_the_kept_set(tmp_path, monkeypatch):
    q = FakeQwen(tmp_path, monkeypatch)
    q.pack.workflows = fixture("qwen-image.workflows.valid.json")["workflows"]
    q.impls["qwen-image/encode"].unload_after = ("text_encoder",)
    q.impls["qwen-image/encode"].peak = pack.compile_formula("100", set())
    plan = registry.memory_plan(q.pack, "base", "bf16", q.impls, q.schemas)
    te = plan["components"][2]  # after the enhance node's two add-ons
    assert te["component"] == "text_encoder" and te["unloads_after"] is True
    others = sum(c["gb"] for c in plan["components"][3:])
    assert plan["peak_gb"] == pytest.approx(others + te["gb"] + 100, abs=0.01)  # its stage holds it


def test_a_text_workflow_gets_its_own_memory_plan_at_four_thumbnails(tmp_path, monkeypatch):
    """qwen-image-ask (contracts/README.md, memory plans): its own item, named by `workflow`, only the text encoder
    (the encode stage's entry), peak = kept + the README example's ask 2.5 + 1.5 x 4 thumbnails; the media plan is
    unchanged."""
    q = FakeQwen(tmp_path, monkeypatch)
    q.pack.workflows = fixture("qwen-image.workflows.valid.json")["workflows"]
    media, text = registry.memory_plans(q.pack, "turbo", "int8", q.impls, q.schemas)
    assert "workflow" not in media and media == registry.memory_plan(
        q.pack, "turbo", "int8", q.impls, q.schemas
    )
    assert text["workflow"] == "qwen-image-ask" and text["planned_at"] == {
        "mp": 0.0,
        "frames": 0,
        "images": 4,
    }
    (te,) = text["components"]
    assert (te["component"], te["variant"], te["precision"]) == ("text_encoder", "base", "int8")
    assert text["peak_gb"] == pytest.approx(te["gb"] + 2.5 + 1.5 * 4, abs=0.01)
    q.pack.workflows = q.pack.workflows[
        1:
    ]  # only text workflows (the llm pack): one item, no `workflow`
    (only,) = registry.memory_plans(q.pack, "turbo", "int8", q.impls, q.schemas)
    assert "workflow" not in only and only["planned_at"]["images"] == 4


def test_a_file_two_components_read_counts_once(tmp_path, monkeypatch):
    """LTX's vae and vae_encoder (and the connector inside the transformer file) read the same files: each file's
    bytes go to one component, the one listing the fewest files, the first on a tie."""
    q = FakeQwen(tmp_path, monkeypatch)
    comps = q.pack.spec["components"]
    comps["vae_encoder"] = dict(comps["vae"])
    comps["connector"] = {
        "files": [comps["transformer"]["files"], comps["vae"]["files"]],
        "dtype": "stored",
    }
    base = pack.models_entry(q.pack)["variants"][0]
    got = {c["component"]: c["bytes"] for c in base["components"]}
    vae = len(next((tmp_path / "Qwen-Image-2.1/vae").glob("*.safetensors")).read_bytes())
    assert got["vae"] == vae and got["vae_encoder"] == 0 and got["connector"] == 0, got
    assert got["transformer"] > 0


# --- the native env: the minimax_h3 pack (a child process; tests/test_child.py covers core.child)


def test_minimax_h3_pack_publishes_the_canonical_fixtures():
    values, _, packs = registry.build("native")
    assert [p.name for p in packs] == ["minimax_h3", "zonos3"]  # zonos3: tests/test_zonos3.py
    want_nodes = fixture("minimax-h3.nodes.valid.json")["nodes"]
    kinds = {n["kind"] for n in want_nodes}
    assert [n for n in values["nodes"]["nodes"] if n["kind"] in kinds] == want_nodes
    assert [w for w in values["workflows"]["workflows"] if w["family"] == "minimax-h3"] == fixture(
        "minimax-h3.workflows.valid.json"
    )["workflows"]
    (got,) = [m for m in values["models"]["models"] if m["family"] == "minimax-h3"]
    want = fixture("minimax-h3.models.valid.json")["models"][0]
    assert {k: got[k] for k in ("id", "family", "title", "media", "runtimes")} == {
        k: want[k] for k in ("id", "family", "title", "media", "runtimes")}  # fmt: skip
    variant = got["variants"][0]
    if variant["presence"] != "present":
        pytest.skip("MiniMax-H3 is not in the models folder")
    comps = lambda v: [(c["component"], c["precisions"], c["bytes"]) for c in v["components"]]
    assert comps(variant) == comps(want["variants"][0]) and variant["precisions"] == ["int8"]
    # the native runtime: no component in the plan, P = the child's peak at the template size (24 + 0.05 mp frames)
    assert variant["memory"] == [{"precision": "int8", "peak_gb": 26.57, "components": [],
                                  "planned_at": {"mp": 0.4147, "frames": 124}}]  # fmt: skip


def test_the_native_runner_imports_no_runtime_library():
    import subprocess
    import sys
    from pathlib import Path

    code = ("import sys; from core import registry; import main; v, i, p = registry.build('native'); "
            "r, dev, loader = main.residency_for(p); "
            "print(sorted({'torch', 'mlx'} & {m.split('.')[0] for m in sys.modules}), dev, loader.__name__)")  # fmt: skip
    out = subprocess.run([sys.executable, "-c", code], cwd=Path(__file__).resolve().parents[1],
                         capture_output=True, text=True, check=True).stdout.strip()  # fmt: skip
    assert out == "[] mps core.loaders_native"


def test_the_native_runner_starts_on_cuda(monkeypatch):
    """Off a Mac the native loader says cuda; it has no torch context to measure (the gpu host's runner-native)."""
    import main
    from core import loaders_native, registry

    monkeypatch.setattr(loaders_native, "device", lambda: "cuda")
    r, dev, loader = main.residency_for(registry.build("native")[2])
    assert dev == "cuda" and loader is loaders_native and r.total_gb > 0
    ram = loader.host_ram()  # the hosts key carries it on cuda (main.extra)
    assert ram["budget_gb"] > 0 and ram["used_gb"] >= 0


def test_the_native_runner_names_the_gpu_on_cuda(monkeypatch):
    """On a CUDA host the native env's device is the GPU (nvidia-smi), as the torch env's, not the CPU."""
    import subprocess

    from core import loaders_native, residency

    out = "NVIDIA RTX PRO 6000 Blackwell Server Edition, 97887, 595.58.03\n"
    monkeypatch.setattr(residency.subprocess, "run",
                        lambda *a, **k: subprocess.CompletedProcess(a, 0, stdout=out))  # fmt: skip
    monkeypatch.setattr(loaders_native, "device", lambda: "cuda")
    residency._gpu.cache_clear()
    try:
        dev = loaders_native.device_info()
    finally:
        residency._gpu.cache_clear()
    assert dev["kind"] == "cuda" and dev["name"] == "NVIDIA RTX PRO 6000 Blackwell Server Edition"
    assert dev["memory_total_gb"] == 95.6 and dev["driver"] == "595.58.03"


def test_a_native_pack_publishes_only_on_its_runtime_devices(monkeypatch):
    from core import models

    by_id = {p.spec["id"]: p for p, _ in pack.for_env("native")}
    monkeypatch.setattr(models, "device", lambda p: "cuda")
    assert not models.runs_here(by_id["minimax-h3"], "base")  # the h3 binary is Metal only
    assert models.runs_here(by_id["zonos3-mini"], "mini")


def test_minimax_h3_refusals_equal_the_contract_fixture():
    from packs.minimax_h3 import native

    fx = json.loads((CONTRACTS / "fixtures/video/minimax-h3.json").read_text())
    for c in fx["cases"]:
        assert native.generate_error(*c["args"]) == c["expect"], c["description"]
    for c in fx["render"]:  # API 1.37: the render size rule
        got = native.render_size(*c["args"])
        assert (list(got) if got else None) == c["expect"], c["description"]
    one = {"refs": [{"image": None}]}
    assert (
        native.check_generate(one, {"prompt", "first"})
        == ": references cannot go with a first or last frame"
    )
    assert native.check_generate(one, {"prompt"}) is None
    assert native.check_generate({"refs": []}, {"first", "last"}) is None
    for kind in ("videos", "audios"):  # API 1.22: any reference kind
        assert (
            native.check_generate({kind: [{}]}, {"last"})
            == ": references cannot go with a first or last frame"
        )


FAKE_H3 = """#!{python}
import json, os, subprocess, sys
a = sys.argv[1:]
out, w, h, n = a[a.index("-o") + 1], a[a.index("--width") + 1], a[a.index("--height") + 1], int(a[a.index("--frames") + 1])
images = [a[i + 1] for i, x in enumerate(a) if x in ("--first-frame", "--last-frame", "--ref-image")]
probe = lambda f: json.loads(subprocess.run(["ffprobe", "-v", "error", "-show_entries", "format=duration:stream=codec_type,codec_name",
                                            "-of", "json", f], capture_output=True, check=True).stdout)
media = [(x, probe(a[i + 1])) for i, x in enumerate(a) if x in ("--ref-video", "--ref-silent-video", "--ref-audio")]
json.dump({{"argv": a, "cwd": os.getcwd(), "env": sorted(os.environ),
           "images": [open(f, "rb").read(8).hex() for f in images], "media": media}}, open(os.environ["H3_FAKE_LOG"], "w"))
sys.stderr.write("text encoder     50/50\\n\\rdenoise   2/2\\n")
s = n / 24
subprocess.run(["ffmpeg", "-v", "error", "-y", "-f", "lavfi", "-i", f"testsrc=s={{w}}x{{h}}:r=24:d={{s}}", "-f", "lavfi",
                "-i", f"sine=sample_rate=32000:d={{s}}", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac",
                "-movflags", "+faststart", out], check=True)
sys.stderr.write("h3: wrote " + out + "\\n")
"""


def test_minimax_h3_generate_runs_the_binary_and_returns_a_video(tmp_path, monkeypatch):
    """generate with a fake h3 (a script that writes a test MP4 with ffmpeg): the argv in the goldens' order with
    absolute paths, cwd the binary's folder, Image inputs as PNGs deleted after, H3_* passed and secrets not, and the
    Video value like ltx's."""
    import re
    import shutil
    import subprocess
    import sys

    from PIL import Image

    from core import media
    from core.graph import RunError
    from packs.minimax_h3 import native

    if not (media.FFMPEG and shutil.which("ffmpeg")):
        pytest.skip("no ffmpeg")
    fake, log = tmp_path / "bin" / "h3", tmp_path / "log.json"
    fake.parent.mkdir()
    fake.write_text(FAKE_H3.format(python=sys.executable))
    fake.chmod(0o755)
    monkeypatch.setattr(native, "BINARY", fake)
    monkeypatch.setattr(native, "H3C", fake.parent)
    monkeypatch.setenv("H3_FAKE_LOG", str(log))
    monkeypatch.setenv("NATS_URL", "nats://runner:secret@nats:4222")

    class Model:
        variant = "base"

        def folder(self, component=None):
            return tmp_path / "MiniMax-H3"

    class Ctx:
        node, seen = "generate", []

        def check(self):
            pass

        def progress(self, step, of, phase=None, phase_id=None):
            assert 1 <= step <= of  # evt.run.progress counts from 1
            self.seen.append((step, of, phase, phase_id))

    inputs = {"model": Model(), "prompt": "-a fox", "seed": 2**53 - 1, "canvas": {"width": 256, "height": 288},
              "duration": {"frames": 22, "fps": 24}, "first": Image.new("RGBA", (300, 200), (255, 0, 0, 128)),
              "last": Image.new("RGB", (64, 64))}  # fmt: skip
    params = {"steps": 2, "layers": 45, "reuse": 2, "refs": []}  # the old speed fast
    ctx = Ctx()
    v = native.generate(ctx, params, inputs)["video"]
    run = json.loads(log.read_text())
    a = run["argv"]
    assert a[:6] == ["-d", str(tmp_path / "MiniMax-H3"), "-p", "-a fox", "-o", a[5]]
    assert a[6:22] == ["--width", "256", "--height", "288", "--frames", "22", "--steps", "2", "--seed",
                       str(2**53 - 1), "--layers", "45", "--reuse", "2", "--use-int8-row-fc2", "--first-frame"]  # fmt: skip
    assert a[23] == "--last-frame" and len(a) == 25
    assert run["cwd"] == str(fake.parent.resolve()) and all(
        x.startswith("/") for x in (a[5], a[22], a[24])
    )
    assert run["images"] == ["89504e470d0a1a0a"] * 2  # PNGs
    assert "H3_FAKE_LOG" in run["env"] and "NATS_URL" not in run["env"]
    tmp = Path(a[5]).parent
    assert not tmp.exists()  # the per-run folder went with the inputs and the MP4
    assert (v["frames"], v["fps"], v["has_audio"]) == (22, 24, True)
    assert v["poster"].size == v["last"].size == (256, 288) and v["mp4"][4:8] == b"ftyp"
    assert v["stream"]["codec"] == "h264" and v["stream"]["width"] == 256
    # the last line, mapped; API 1.36: each with the phase id of model.yaml generate.phases
    assert ctx.seen[0] == (1, 1, "starting h3", "encode") and ctx.seen[-1] == (
        2,
        2,
        "sampling",
        "sample",
    )
    # refused before the child starts
    log.unlink()
    with pytest.raises(
        RunError, match="^generate: references cannot go with a first or last frame$"
    ):
        native.generate(ctx, params | {"refs": [{"image": Image.new("RGB", (8, 8))}]}, inputs)
    with pytest.raises(
        RunError, match=r"^generate: the canvas is 1344 × 1344; at most 1032192 pixels"
    ):
        native.generate(ctx, params, inputs | {"canvas": {"width": 1344, "height": 1344}})
    assert not log.exists()
    # API 1.22: video and audio references, cut to their parts in the per-run folder, after the image ones; audios
    # before videos, so the audio row is <Audio 1> even next to a video kept with its sound
    take = lambda kind, data, **f: {"take_id": f"t-{kind}", "kind": kind, "duration_s": 3.0, "in": 0.5, "out": 3.0,
                                    "has_audio": True, "data": data} | f  # fmt: skip
    clip, sound = tmp_path / "clip.mp4", tmp_path / "sound.wav"
    subprocess.run(["ffmpeg", "-v", "error", "-y", "-f", "lavfi", "-i", "testsrc=s=64x64:r=24:d=3", "-f", "lavfi", "-i",
                    "sine=sample_rate=32000:d=3", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", str(clip)],
                   check=True)  # fmt: skip
    subprocess.run(["ffmpeg", "-v", "error", "-y", "-f", "lavfi", "-i", "sine=sample_rate=32000:d=3", str(sound)],
                   check=True)  # fmt: skip
    v, w = clip.read_bytes(), sound.read_bytes()
    refs = params | {"refs": [{"image": Image.new("RGB", (8, 8))}],
                     "videos": [{"video": take("video", v)}, {"video": take("video", v), "sound": "silent"},
                                {"video": take("video", v, has_audio=False), "sound": "keep"}],
                     "audios": [{"audio": take("audio", w, **{"in": 0, "out": 2.0})}]}  # fmt: skip
    frames = inputs | {"first": None, "last": None, "duration": {"frames": 56, "fps": 24}}
    native.generate(ctx, refs, {k: x for k, x in frames.items() if x is not None})
    run = json.loads(log.read_text())
    flags = [x for x in run["argv"] if x.startswith("--ref") or x.endswith("-frame")]
    assert flags == [
        "--ref-image",
        "--ref-audio",
        "--ref-video",
        "--ref-silent-video",
        "--ref-silent-video",
    ]
    got = [(f, [st["codec_type"] for st in m["streams"]], round(float(m["format"]["duration"]), 1))
           for f, m in run["media"]]  # fmt: skip
    assert got == [("--ref-audio", ["audio"], 2.0), ("--ref-video", ["video", "audio"], 2.5),
                   ("--ref-silent-video", ["video"], 2.5), ("--ref-silent-video", ["video"], 2.5)]  # fmt: skip
    # refused before the child starts, with the contract's texts
    log.unlink()
    for change, why in [
        (
            {"duration": {"frames": 22, "fps": 24}},
            "a video reference with sound needs a clip of at least 56 frames (22)",
        ),
        ({"first": Image.new("RGB", (8, 8))}, "references cannot go with a first or last frame"),
    ]:
        with pytest.raises(RunError, match=f"^generate: {re.escape(why)}$"):
            native.generate(ctx, refs, {k: x for k, x in frames.items() if x is not None} | change)
    with pytest.raises(
        RunError, match="^generate: audio references need an image or video reference$"
    ):
        native.generate(
            ctx,
            params | {"audios": refs["audios"]},
            {k: x for k, x in frames.items() if x is not None},
        )
    # a 2 s part with sound: h3 keeps 39 of its 48 frames, under 2 s of sound (the dev run that found it)
    with pytest.raises(
        RunError, match=r"part of at least 56 frames at 24 fps \(video 1 reads 48\)$"
    ):
        native.generate(
            ctx,
            params | {"videos": [{"video": take("video", v, **{"in": 0.5, "out": 2.5})}]},
            {k: x for k, x in frames.items() if x is not None},
        )
    assert not log.exists()
    # API 1.37: a replaced sound: the soundtrack's part becomes the cut clip's sound, the shorter setting the length;
    # it reads like a kept sound (--ref-video) and its refusals come first
    track = take("audio", w, take_id="t-track", **{"in": 0.5, "out": 3.0})  # 2.5 s of the 3 s sine
    replaced = params | {"refs": [{"image": Image.new("RGB", (8, 8))}],
                         "videos": [{"video": take("video", v, **{"in": 0.0, "out": 3.0}), "sound": "replace",
                                     "soundtrack": track}]}  # fmt: skip
    native.generate(ctx, replaced, {k: x for k, x in frames.items() if x is not None})
    run = json.loads(log.read_text())
    (got,) = [(f, [st["codec_type"] for st in m["streams"]], round(float(m["format"]["duration"]), 1))
              for f, m in run["media"]]  # fmt: skip
    assert got == ("--ref-video", ["video", "audio"], 2.5)
    for row, why in [({"sound": "replace"}, "generate.videos[1].soundtrack: Replace needs a soundtrack"),
                     ({"sound": "keep", "soundtrack": track},
                      "generate.videos[1].soundtrack: a soundtrack is read only with Sound: Replace")]:  # fmt: skip
        bad = replaced | {"videos": [{"video": take("video", v)} | row]}
        with pytest.raises(RunError, match=f"^{re.escape(why)}$"):
            native.generate(ctx, bad, {k: x for k, x in frames.items() if x is not None})


def test_minimax_h3_presets_and_advanced_map_onto_h3_flags(tmp_path):
    """API 1.37 (plan 20 24.3): each preset of the templates as h3 flags, Render as the exact same-shape size,
    core reuse only at reuse 1, and the refusals on Base."""
    from packs.minimax_h3 import native

    wf = json.loads(
        (Path(native.__file__).parent / "workflows/1-minimax-h3-text-to-video.json").read_text()
    )
    presets = wf["studio"]["presets"]
    assert wf["studio"]["preset_order"] == ["Draft", "Fast", "Default", "Reference"]
    params = wf["nodes"]["generate"]["params"]
    assert all(
        params[k.split(".")[1]] == v for k, v in presets["Default"].items()
    )  # the template opens on Default

    def flags(**kw):
        a = native.argv(
            tmp_path,
            "p",
            tmp_path / "o.mp4",
            width=864,
            height=480,
            frames=124,
            seed=1,
            images=[],
            **kw,
        )
        return a[a.index("--steps") :]

    want = {
        "Draft": ["--steps", "4", "--seed", "1", "--layers", "50", "--reuse", "1"],
        "Fast": [
            "--steps",
            "20",
            "--seed",
            "1",
            "--layers",
            "45",
            "--reuse",
            "2",
            "--token-reduction",
        ],
        "Default": ["--steps", "20", "--seed", "1", "--layers", "45", "--reuse", "2"],
        "Reference": ["--steps", "50", "--seed", "1", "--layers", "50", "--reuse", "1"],
    }
    for name, values in presets.items():
        p = {k.split(".")[1]: v for k, v in values.items()}
        assert flags(**p) == want[name] + ["--use-int8-row-fc2"], name
    assert flags(steps=4, core_reuse=4) == want["Draft"] + [
        "--core-reuse",
        "4",
        "--use-int8-row-fc2",
    ]
    assert flags(steps=20, reuse=2, core_reuse=4)[-1:] == [
        "--use-int8-row-fc2"
    ]  # h3 refuses both: not passed
    size = native.render_size(864, 480, "75%")
    assert flags(steps=4, render=size)[-5:] == [
        "--render-width",
        "576",
        "--render-height",
        "320",
        "--use-int8-row-fc2",
    ]
    assert "--render-width" not in flags(steps=4, render=(864, 480))


def test_compute_is_a_load_option_keyed_only_off_its_default(tmp_path, monkeypatch):
    """API 1.39 (model.yaml `model.compute`): the model node gets the param; float16 keys `components` apart (and
    their cache fingerprint), float32 adds nothing to any key; another runtime ignores it; the models key names the
    components."""
    q = FakeQwen(tmp_path, monkeypatch)
    q.pack.spec["model"]["compute"] = {"label": "Compute", "default": "float32", "enum": ["float32", "float16"],
                                       "components": ["transformer"], "runtime": "torch"}  # fmt: skip
    p = next(x for x in pack.model_schema(q.pack)["params"] if x["name"] == "compute")
    assert p == {"name": "compute", "type": "enum", "label": "Compute", "default": "float32", "enum": ["float32", "float16"]}  # fmt: skip
    plain, f32, f16 = (
        pack.ModelRef(q.pack, "base", "bf16", compute=c) for c in (None, "float32", "float16")
    )
    assert plain.key("transformer") == f32.key("transformer") == f16.key("transformer")[:4]
    assert f16.key("transformer")[4:] == ("float16",) and f16.key("vae") == plain.key("vae")
    assert (
        plain.fingerprint(["transformer"])
        == f32.fingerprint(["transformer"])
        != f16.fingerprint(["transformer"])
    )
    assert pack.models_entry(q.pack)["compute"] == {"components": ["transformer"]}
    q.pack.spec["model"]["compute"]["runtime"] = "mlx"
    assert f16.key("transformer") == plain.key(
        "transformer"
    ) and "compute" not in pack.models_entry(q.pack)
