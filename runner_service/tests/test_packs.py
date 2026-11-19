"""Pack → registry values: the generated node schemas, workflows and models entries equal the canonical contract
fixtures, so a pack cannot drift from the contract."""

import json

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


@pytest.mark.parametrize("family", ["qwen-image", "minimax-h3-image"])
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


@pytest.mark.parametrize("env", ["fake", "torch", "mlx"])
def test_published_nodes_equal_the_canonical_palette(env):
    """Each env's node schemas (title and help from model.yaml / nodes.yaml included) are the canonical palette's
    kinds of that env (contracts/fixtures/graphs/palette.json, without its fictional `test` family), and each
    family's group title is the model's title."""
    if env != "fake":
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
    decode's 8.5 * mp is the largest stage peak, on top of every kept component."""
    q = FakeQwen(tmp_path, monkeypatch)
    q.pack.workflows = fixture("qwen-image.workflows.valid.json")["workflows"]
    plan = registry.memory_plan(q.pack, variant, "int8", q.impls, q.schemas)
    owner = {"text_encoder": "base", "vae": "base", "transformer": variant}
    want = [
        (c, owner[c], "bf16" if c == "vae" else "int8")
        for c in ("text_encoder", "transformer", "vae")
    ]
    assert [(c["component"], c["variant"], c["precision"]) for c in plan["components"]] == want
    kept = sum(c["gb"] for c in plan["components"])
    assert plan["precision"] == "int8" and not any("unloads_after" in c for c in plan["components"])
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
    te = plan["components"][0]
    assert te["component"] == "text_encoder" and te["unloads_after"] is True
    others = sum(c["gb"] for c in plan["components"][1:])
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
