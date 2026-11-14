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


def test_echo_pack_is_published_for_env_fake_only():
    assert [p.name for p, _ in pack.for_env("fake")] == ["echo"]
    assert all(p.name != "echo" for p, _ in pack.for_env("torch"))


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
