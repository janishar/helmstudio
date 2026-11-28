"""The ltx pack's NVIDIA weights against ltx-core's modules, with no weights: each community file's keys
(fixtures/ltx_nvidia_headers: the real headers' keys, dtypes and shapes, gzipped) renamed by packs/ltx/torch.py give
exactly the persistent tensors of the component built on meta, at their shapes, FP8 weights on plain Linears with an
[out, 1] scale and int8 ConvRot weights on Linears the quantize globs make int8; and, on a host with the official
files, every official component the same through torch.build itself."""

import gzip
import json
import types
from pathlib import Path

import pytest

torch = pytest.importorskip("torch")
import accelerate
import yaml
from safetensors.torch import save_file

from core import pack as packs
from packs.ltx import torch as ltx

FIX = Path(__file__).parent / "fixtures/ltx_nvidia_headers"
SPEC = yaml.safe_load((Path(ltx.__file__).parent / "model.yaml").read_text())
OFFICIAL = Path.home() / "GenAI/models/ltx-2.5"  # the local official files, read when present (headers only)


def fixture(name: str) -> tuple[dict, dict]:
    with gzip.open(FIX / f"{name}.json.gz") as f:
        d = json.load(f)
    return d["tensors"], d["metadata"]


def u8(b: bytes) -> torch.Tensor:
    return torch.frombuffer(bytearray(b), dtype=torch.uint8)


def header_file(path: Path, metadata: dict | None = None, tensors: dict | None = None) -> Path:
    """A small safetensors file carrying a real file's metadata (build reads only that)."""
    save_file(tensors or {"x": torch.zeros(1)}, str(path), metadata=metadata)
    return path


def te_file(tmp_path: Path) -> Path:
    """The DmitryDB text encoder's Gemma config with placeholder assets: the connector reads only the config."""
    _, meta = fixture("ddb-text-encoder-int8")
    assets = {"tokenizer_json": u8(b"{}"), "hf_asset__tokenizer_config.json": u8(b"{}"),
              "hf_asset__processor_config.json": u8(b"{}")}  # fmt: skip
    return header_file(tmp_path / "te.safetensors", {"gemma_config": meta["gemma_config"]}, assets)


def handle(**files) -> types.SimpleNamespace:
    return types.SimpleNamespace(files=lambda c: files[c])


def check(component: str, module, tensors: dict) -> dict:
    """Strict: the renamed keys (scales and comfy_quant tags aside) are the module's persistent tensors at their
    shapes; returns {fp8, int8} module names."""
    want = {k: list(v.shape) for k, v in module.state_dict(keep_vars=True).items()}
    got, scales, kinds = {}, {}, {"F8_E4M3": set(), "I8": set()}
    for key, (dtype, shape) in tensors.items():
        name = ltx.rename(component, key)
        if name is None:  # another component's, an asset, or a tag / scale tensor_hook reads
            continue
        if name.endswith(".scale"):
            scales[name.removesuffix(".scale")] = [dtype, shape]
            continue
        got[name] = shape
        if dtype in kinds:
            kinds[dtype].add(name.removesuffix(".weight"))
    assert got == want
    mods = dict(module.named_modules())
    fp8, int8 = kinds["F8_E4M3"], kinds["I8"]
    assert set(scales) == fp8
    assert all(type(mods[m]) is torch.nn.Linear and scales[m] == ["F32", [want[f"{m}.weight"][0], 1]] for m in fp8)
    spec = SPEC["components"][component]
    assert all(packs.quantized(spec, "int8", m) and want[f"{m}.weight"][1] % ltx.CONVROT_GROUP == 0 for m in int8)
    for m in int8:  # each int8 weight has its tag and scale in the file
        base = next(k.removesuffix(".weight") for k in tensors if ltx.rename(component, k) == f"{m}.weight")
        assert tensors[f"{base}.weight_scale"] == ["F32", [want[f"{m}.weight"][0], 1]]
        assert f"{base}.comfy_quant" in tensors
    return {"fp8": fp8, "int8": int8}


def test_unsloth_fp8_transformer_matches_ltx_core_with_the_packs_config(tmp_path):
    tensors, meta = fixture("unsloth-fp8-transformer")
    assert meta == {}  # no LTX config in its header: transformer-config.json stands in
    module = ltx.build("transformer", handle(transformer=[header_file(tmp_path / "t.safetensors")]))
    kinds = check("transformer", module, tensors)
    assert len(kinds["fp8"]) == 1360 and not kinds["int8"]
    assert "adaln_single.linear" in kinds["fp8"] and "patchify_proj" not in kinds["fp8"]  # bf16 in the file: dense


def test_dmitrydb_dev_transformer_matches_and_carries_the_same_config(tmp_path):
    tensors, meta = fixture("ddb-dev-transformer-int8")
    assert {k: json.loads(v) if k != "model_version" else v for k, v in meta.items()} == json.loads(
        ltx.CONFIG.read_text()
    )
    module = ltx.build("transformer", handle(transformer=[header_file(tmp_path / "t.safetensors", meta)]))
    kinds = check("transformer", module, tensors)
    assert len(kinds["int8"]) == 1435 - 2 * 8 * 6 and not kinds["fp8"]  # its connector copies are not the DiT's


def test_unsloth_connector_shards_are_the_whole_connector(tmp_path):
    tensors = {**fixture("unsloth-connectors-1")[0], **fixture("unsloth-connectors-2")[0]}
    files = handle(transformer=[header_file(tmp_path / "t.safetensors")], text_encoder=[te_file(tmp_path)])
    check("connector", ltx.build("connector", files), tensors)
    assert {ltx.rename("connector", k).split(".")[0] for k in tensors} == {
        "feature_extractor", "video_connector", "audio_connector"}  # fmt: skip


def test_dmitrydb_text_encoder_matches_gemma4(tmp_path):
    """The shell as build makes it (its lm_head dropped), from the file's Gemma config; build itself, with the
    tokenizer and processor assets, runs in the official-files test below."""
    tensors, _ = fixture("ddb-text-encoder-int8")
    configurator = ltx.PARTS["text_encoder"][0].with_gemma_model_path(str(te_file(tmp_path)))
    with accelerate.init_empty_weights(include_buffers=False):
        module = configurator.from_metadata({})
    module.model.lm_head = torch.nn.Identity()
    kinds = check("text_encoder", module, tensors)
    assert len(kinds["int8"]) == 320


FILES = {c: SPEC["components"][c]["files"] for c in ltx.PARTS}


@pytest.mark.skipif(not OFFICIAL.is_dir(), reason="the official LTX-2.5 files are not on this host")
@pytest.mark.parametrize("component", list(ltx.PARTS))
def test_the_official_files_build_and_match(component):
    files = handle(**{c: [OFFICIAL / f for f in ([g] if isinstance(g, str) else g)] for c, g in FILES.items()})
    module = ltx.build(component, files)
    tensors = {k: [t["dtype"], t["shape"]] for p in files.files(component) for k, t in packs.header(p).items()}
    check(component, module, tensors)
    assert not [n for n, p in module.named_parameters() if not p.is_meta]  # all from the files


def test_the_pack_config_is_the_official_transformers_metadata():
    path = OFFICIAL / FILES["transformer"]
    if not path.is_file():
        pytest.skip("the official LTX-2.5 files are not on this host")
    meta = ltx.metadata(path)
    meta.pop("license")
    assert meta == json.loads(ltx.CONFIG.read_text())
