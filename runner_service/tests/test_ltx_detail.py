"""Plan 19 phases 4 and 4b, Recipe Detail (DFR) without weights: the canvas layout, the run checks, variant-scoped
components, the detailer as an optional add-on, ltx/ic-lora's adapter use, and no weight file named in the pack's
code. The renders are tests/test_ltx_golden.py DETAIL_CASES."""

import re
from pathlib import Path
from types import SimpleNamespace

import pytest
import yaml

from core import pack as packs
from packs.ltx import common

LTX = Path(__file__).resolve().parents[1] / "packs" / "ltx"
SPEC = yaml.safe_load((LTX / "model.yaml").read_text())


def test_the_canvas_pads_to_whole_segments_with_a_slot_at_each_boundary():
    """dfr_layout.resolve_canvas: the least padding (24 or 32), the longer segment on a tie."""
    assert common.dfr_canvas(49) == (49, [24, 48])
    assert common.dfr_canvas(57) == (65, [32, 64])
    assert common.dfr_canvas(9) == (25, [24])
    assert common.dfr_canvas(97) == (97, [32, 64, 96])
    assert common.dfr_fps(24) == 24 and common.dfr_fps(25) == 25 and common.dfr_fps(48) == 60


def test_the_detail_pass_needs_the_distilled_transformer():
    dev, distilled = (
        SimpleNamespace(variant="dev", loras=()),
        SimpleNamespace(variant="distilled", loras=()),
    )
    wired = {"model", "cond", "seed", "latent", "reference"}
    stage2 = {"role": "stage2", "steps": 3, "detail": True}
    assert (
        common.check_sample(stage2, wired, {"model": dev})
        == ".detail: needs model variant distilled"
    )
    assert common.check_sample(stage2, wired, {"model": distilled}) is None


def test_the_duration_head_is_a_component_of_the_distilled_variants_only():
    """`variants` scopes a component: Dev needs no duration head (its shares stay 8), the distilled ones do."""
    p = SimpleNamespace(spec=SPEC, component=lambda c: SPEC["components"][c])
    assert "duration_head" in packs.needed(p, "distilled")
    assert "duration_head" not in packs.needed(p, "dev")
    assert "duration_head" in packs.needed(p)  # without a variant: every non-add-on component
    assert "ic_lora" not in packs.needed(p, "distilled")  # an add-on is no variant's
    assert len(SPEC["variants"]["dev"]["shares"]) == 8


def test_the_detailer_is_an_optional_add_on_whose_published_source_has_no_dir(
    tmp_path, monkeypatch
):
    from core import models

    monkeypatch.setattr(packs, "models_root", lambda: tmp_path)
    p = SimpleNamespace(spec=SPEC, component=lambda c: SPEC["components"][c])
    assert packs.addons(p) == ["ic_lora"]
    assert models.addon_dest(p, "ic_lora") == tmp_path / "ltx-2.5/loras"
    out = models.addon_entry(p, "ic_lora")
    assert (
        out["title"] == "Detailer" and out["presence"] == "missing" and "dir" not in out["source"]
    )
    (tmp_path / "ltx-2.5/loras").mkdir(parents=True)
    (
        tmp_path / "ltx-2.5/loras/ltx-2.5-22b-ic-lora-pixel-spatial-upscaler-x2-1.0.safetensors"
    ).write_bytes(b"x")
    marker = tmp_path / "ltx-2.5/loras" / models.MARKER
    marker.parent.mkdir(parents=True)
    marker.write_text("{}")
    assert models.addon_entry(p, "ic_lora")["presence"] == "partial"  # its download is unfinished
    marker.unlink()
    assert models.addon_entry(p, "ic_lora")["presence"] == "present"


def test_the_ltx_pack_names_no_weight_file_in_code():
    """Every file a stage opens comes from model.yaml (plan 23.4, File names)."""
    for f in ("mlx.py", "common.py", "torch.py"):
        assert not re.search(r"[\w.-]+\.safetensors", (LTX / f).read_text()), f


def test_ic_lora_adds_the_detailer_as_an_adapter_use(tmp_path, monkeypatch):
    from core import models

    monkeypatch.setattr(packs, "models_root", lambda: tmp_path)
    monkeypatch.setattr(models, "hashes", models.Hashes())
    pack = packs.Pack("ltx", LTX, SPEC, "mlx", None, "")
    model = packs.ModelRef(pack, "distilled", "int8")
    assert (
        common.ic_lora(None, {"strength": 0.5}, {"model": model})["model"] is model
    )  # not downloaded, no run
    f = tmp_path / "ltx-2.5" / SPEC["components"]["ic_lora"]["files"]
    f.parent.mkdir(parents=True)
    f.write_bytes(b"lora")
    (use,) = common.ic_lora(None, {"strength": 0.5}, {"model": model})["model"].loras
    assert (use.mode, use.strength, use.path, use.bytes) == ("adapter", 0.5, str(f), 4)
    assert use.sha256 == models.sha256_file(f) and model.adapters("transformer") == {}


def test_nvidia_refuses_the_detail_pass_before_anything_loads():
    pytest.importorskip("torch")
    from packs.ltx import torch as ltx_torch

    stage1 = {"role": "stage1", "steps": 8, "detail": True}
    assert ltx_torch.check_sample(stage1, {"model", "cond", "seed", "canvas", "duration"}) == (
        ".detail: the detail pass is Mac only for now"
    )
    assert (
        getattr(ltx_torch, "check_sample-guided")({"detail": True}, set())
        == ".detail: the detail pass is Mac only for now"
    )
