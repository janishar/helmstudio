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


def test_real_qwen_pack_matches_the_fixtures_when_present():
    if not (pack.PACKS / "qwen_image" / "model.yaml").is_file():
        pytest.skip("packs/qwen_image is not written yet")
    pytest.importorskip("torch")
    values, _, _ = registry.build("torch")
    assert values["nodes"]["nodes"] == fixture("qwen-image.nodes.valid.json")["nodes"]
    assert (
        values["workflows"]["workflows"] == fixture("qwen-image.workflows.valid.json")["workflows"]
    )


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
    assert base["presence"] == turbo["presence"] == "missing"  # turbo shares base's vae
    assert "bytes" not in base


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
