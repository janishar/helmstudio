"""The minimax_h3_image pack without the GPU: its text and size rules against the old studio (enhance.py's own
self-checks, the goldens' sidecars), its weight plumbing against the files on disk (skipped without them), and the
core features it added: a component's own `dir`, `dtype: stored`, the tensor hook, `sees`, `reports`, `alone`, a
stage without a Model input, the footprint settle and Cache.shed. The GPU goldens are tests/test_minimax_golden.py."""

import fnmatch
import json
from pathlib import Path

import pytest

torch = pytest.importorskip("torch")

from core import graph, pack, registry
from core import loaders_torch as lt
from core.residency import Residency
from packs.minimax_h3_image import torch as mm

GOLDEN = Path(__file__).resolve().parent / "golden"
PE = pack.models_root() / "MiniMax-H3-Image-PE"
MODEL = pack.models_root() / "MiniMax-H3-Image"


def test_enhancer_answer_parsing_and_edit_message():
    assert mm.reduced_ratio(1920, 1080) == "16:9" and mm.reduced_ratio(1000, 1000) == "1:1"
    assert mm.parse_answer('x\n</think>\n\n{"expanded_caption": " A cat. ", "aspect_ratio": "3:2"}', "aspect_ratio") == ("A cat.", "3:2")  # fmt: skip
    assert mm.parse_answer('</think>```json\n{"expanded_caption": "A", "target_aspect_ratio": "1024:768"}\n```', "target_aspect_ratio") == ("A", "4:3")  # fmt: skip
    assert mm.parse_answer('</think>{"expanded_caption": "A", "aspect_ratio": "wide"}', "aspect_ratio") == ("A", None)
    for bad in ("no think tags", "</think> not json", '</think>{"caption": "A"}', "</think>[1]"):
        assert mm.parse_answer(bad, "aspect_ratio") is None
    from PIL import Image

    content = mm.edit_content("T", 'make "it" night', [Image.new("RGB", (4032, 3024))], "16:9")
    assert len(content) == 5 and "aspect_ratio 4:3]" in content[2]["text"] and "Selection:\n16:9" in content[4]["text"]
    assert content[3]["image"].size == (1182, 887)  # ~1 MP, still 4:3
    assert "Selection:\nadaptive" in mm.edit_content("T", "x", [Image.new("RGB", (64, 64))])[-1]["text"]


def test_nvfp4_dequant():
    # packed byte 0x2F: first element code 0xF (-6), second 0x2 (1); block scales 2 and 0.5, global 0.25
    packed = torch.tensor([[0x2F] + [0] * 7 + [0x2F] + [0] * 7], dtype=torch.uint8)
    w = mm.nvfp4_dequant(packed, torch.tensor([[2.0, 0.5]]).to(torch.float8_e4m3fn), torch.tensor(0.25))
    assert w.shape == (1, 32) and w[0, :2].tolist() == [-3.0, 0.5] and w[0, 16:18].tolist() == [-0.75, 0.125]


def test_sizes_seeds_and_the_20_mp_cap():
    from PIL import Image

    assert mm.size_for(1.0, 1.0) == (1024, 1024) and mm.size_for(0.75, 4.0) == mm.size_for(4 / 3, 4.0)[::-1]
    assert mm.size_for(4 / 3, 16) == (4736, 3552)  # MiniMax's own 16 MP table
    wide, tall = Image.new("RGB", (900, 100)), Image.new("RGB", (100, 200))
    auto = {"mode": "auto", "width": 2048, "height": 2048}
    assert mm.output_size(auto, []) == (2048, 2048)
    assert mm.output_size(auto, [tall, wide]) == mm.size_for(0.5, 4.0)  # the first reference
    assert mm.output_size(auto, [wide]) == mm.size_for(4.0, 4.0)  # clamped to 4:1
    assert mm.output_size(auto | {"mode": "fixed"}, [tall]) == (2048, 2048)
    assert mm.check_canvas({"width": 4096, "height": 5120}, set(), {}) is None  # 20 MP exactly
    assert mm.check_canvas({"width": 8192, "height": 4096}, set(), {}) == ": at most 20 MP (8192 × 4096)"
    edit = json.loads((GOLDEN / "minimax-h3-image-edit/edit-1024-s42.json").read_text())
    from core import images

    ref = images.decode((GOLDEN / "minimax-h3-image-edit/input-turbo-768-s42.png").read_bytes(), "t").convert("RGB")
    assert mm.noise_seed(edit["seed"], [ref]) == edit["noise_seed"] and mm.noise_seed(42, []) == 42
    assert list(mm.output_size({"mode": "auto", "width": 1024, "height": 1024}, [ref])) == edit["output_size"]


def test_text_encoder_keeps_51_layers_and_drops_the_head():
    assert mm.rename("text_encoder", "lm_head.weight") is None
    assert mm.rename("text_encoder", "model.language_model.layers.51.mlp.up_proj.weight") is None
    assert mm.rename("text_encoder", "model.language_model.layers.50.mlp.up_proj.weight")
    assert mm.rename("text_encoder", "model.visual.blocks.60.attn.qkv.weight")  # the vision tower stays whole
    assert mm.rename("enhancer", "model.language_model.layers.0.mlp.up_proj.weight_scale") is None
    assert mm.rename("transformer", "lm_head.weight") == "lm_head.weight"


@pytest.mark.skipif(not PE.is_dir(), reason="MiniMax-H3-Image-PE is not in the models folder")
def test_enhancer_quantize_globs_are_exactly_its_nvfp4_linears():
    weight_map = json.loads((PE / "model.safetensors.index.json").read_text())["weight_map"]
    nvfp4 = {k.removesuffix(".weight_scale") for k in weight_map if k.endswith(".weight_scale")}
    linears = {k.removesuffix(".weight") for k in weight_map if k.endswith(".weight")}
    (p, _), *_ = [x for x in pack.for_env("torch") if x[0].family == "minimax-h3-image"]
    globs = p.component("enhancer")["quantize"]
    assert {m for m in linears if any(fnmatch.fnmatchcase(m, g) for g in globs)} == nvfp4


@pytest.mark.skipif(not (PE.is_dir() and MODEL.is_dir()), reason="the MiniMax weights are not in the models folder")
def test_memory_plan_never_holds_two_models():
    """enhance and encode are `alone` and unload after themselves: the plan's peak is one model + its stage, and the
    enhancer's NVFP4 bytes count as the int8 it becomes (two values per stored byte)."""
    values, _impls, _packs = registry.build("torch")
    (m,) = [x for x in values["models"]["models"] if x["family"] == "minimax-h3-image"]
    (plan,) = m["variants"][0]["memory"]
    comps = {c["component"]: c for c in plan["components"]}
    assert list(comps) == ["enhancer", "text_encoder", "transformer", "vae"]
    assert comps["enhancer"].get("unloads_after") and comps["text_encoder"].get("unloads_after")
    assert comps["vae"]["precision"] == "fp32" and 26 < comps["enhancer"]["gb"] < 28.5
    image = comps["transformer"]["gb"] + comps["vae"]["gb"]
    assert plan["peak_gb"] < image + 8 < 52 and plan["peak_gb"] >= comps["enhancer"]["gb"] + 11 - 0.01
    assert plan["planned_at"]["mp"] == pytest.approx(4.1943, abs=1e-4)


def workflow_graph() -> tuple[dict, dict, dict]:
    values, impls, packs = registry.build("torch")
    schemas = registry.node_schemas(values)
    wf = next(w for p in packs for w in p.workflows if w["id"] == "minimax-h3-image")
    nodes = json.loads(json.dumps(wf["nodes"]))
    nodes["enhance"].pop("bypass")
    return nodes, impls, schemas


def test_enhance_sees_the_refs_of_the_encode_node_its_text_feeds():
    nodes, impls, schemas = workflow_graph()
    ref = {"take_id": "00000000-0000-4000-8000-000000000001", "key": "u/o/a.png", "content_type": "image/png",
           "width": 768, "height": 768, "bytes": 10, "etag": "e1"}  # fmt: skip
    assert graph.seen(nodes, schemas, "enhance", {"refs": "text"}) == ({"refs": []}, [schemas["minimax-h3-image/encode"]["params"][0]])  # fmt: skip
    before = graph.keys(nodes, impls, schemas, "o")
    nodes["encode"]["params"]["refs"] = [{"image": ref}]
    after = graph.keys(nodes, impls, schemas, "o")
    assert graph.seen(nodes, schemas, "enhance", {"refs": "text"})[0] == {"refs": [{"image": ref}]}
    assert before["enhance"] != after["enhance"] and before["prompt"] == after["prompt"]
    # no Model input: the enhancer still counts as the stage's component (admission, plan, key)
    (key,) = graph._components(nodes, impls, schemas, "enhance")
    assert key == ("minimax-h3-image", "base", "enhancer", "int8")
    impl = impls["minimax-h3-image/enhance"]
    assert impl.alone and impl.reports == ("text",) and impl.unload_after == ("enhancer",)
    # its peak counts the seen refs (graph.stage_peak): 11 + 1 GB per reference
    nodes["encode"]["params"]["refs"] = [{"image": ref}] * 3
    p = nodes["enhance"].get("params", {})
    assert graph.stage_peak(nodes, schemas, "enhance", impl, p, {}) == pytest.approx(14)


def test_reference_peaks_cover_the_measured_eight_reference_edit():
    """The robust pass's edit with eight 384² references at a 512² canvas peaked 14.6 GB above the text encoder
    (encode) and 14.4 GB above transformer + VAE (sample); the plan must not admit it below that."""
    nodes, impls, schemas = workflow_graph()
    ref = {"take_id": "00000000-0000-4000-8000-000000000001", "key": "u/o/a.png", "content_type": "image/png",
           "width": 384, "height": 384, "bytes": 10, "etag": "e1"}  # fmt: skip
    nodes["encode"]["params"]["refs"] = [{"image": ref}] * 8
    nodes["canvas"]["params"] |= {"width": 512, "height": 512}
    peak = {n: graph.stage_peak(nodes, schemas, n, impls[nodes[n]["kind"]], nodes[n].get("params", {}), {})
            for n in ("encode", "sample")}  # fmt: skip
    assert peak["encode"] >= 14.6 and peak["sample"] >= 14.4


def test_a_family_canvas_on_auto_keeps_its_area():
    nodes, _impls, schemas = workflow_graph()
    nodes["encode"]["params"]["refs"] = [{"image": {"take_id": "t", "key": "u/o/a.png", "width": 100, "height": 900}}]
    nodes["canvas"]["params"] |= {"width": 4096, "height": 4096}
    assert graph.stage_sizes(nodes, schemas, "sample", {})["mp"] == pytest.approx(4096 * 4096 / 1e6)


def test_load_keeps_stored_dtypes_runs_the_hook_and_reads_only_kept_keys(tmp_path):
    from safetensors.torch import save_file
    from torch import nn

    with torch.device("meta"):
        m = nn.Sequential(nn.Linear(32, 32), nn.Linear(32, 4))
    save_file({"0.weight": torch.ones(32, 32, dtype=torch.bfloat16), "0.bias": torch.ones(32),
               "1.weight": torch.ones(4, 32), "1.bias": torch.ones(4), "extra.scale": torch.ones(3)},
              str(tmp_path / "w.safetensors"))  # fmt: skip
    steps = []
    out = lt.load(m, [tmp_path / "w.safetensors"], dev="cpu", dtype=None,
                  rename=lambda k: None if k.startswith("extra.") else k,
                  hook=lambda name, t: t * 2 if name == "1.bias" else t,
                  progress=lambda done, total: steps.append(total))  # fmt: skip
    assert out[0].weight.dtype == torch.bfloat16 and out[1].weight.dtype == torch.float32
    assert out[1].bias.tolist() == [2.0] * 4 and set(steps) == {4}  # the dropped key is neither read nor counted


def test_admission_waits_for_the_footprint_to_settle(monkeypatch):
    """macOS gives freed GPU pages back late: a measure that falls within SETTLE_S admits instead of failing."""
    reads = iter([30.0, 30.0, 25.0, 10.0, 1.0])
    monkeypatch.setattr("core.residency.time.sleep", lambda s: None)
    res = Residency(52, measure=lambda: next(reads, 1.0))
    res.admit({("m", "v", "c", "int8"): 35.0}, 2.0, set())  # 37 GB needed: fits once the footprint is ~1 GB


def test_cache_shed_keeps_small_results():
    c = graph.Cache()
    c["small"], c["big"] = {"x": b"0" * 10}, {"x": b"0" * graph.Cache.SHED}
    c.shed()
    assert list(c) == ["small"]
