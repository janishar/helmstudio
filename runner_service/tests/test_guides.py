"""Model guides (API 1.71, contracts/README.md "Assistant: model guides"): every pack's assistant.md has a card and fits
the caps, every model.yaml recipe fits its workflow's node schemas (the canonical fixtures, so no runtime is needed),
the fake runner publishes its guide, and a bad recipe stops the runner."""

import json
from pathlib import Path

import pytest
import yaml
from conftest import CONTRACTS

from core import pack, registry

PACKS = sorted(p for p in pack.PACKS.iterdir() if (p / "model.yaml").is_file())


def spec(d: Path) -> dict:
    return yaml.safe_load((d / "model.yaml").read_text())


@pytest.mark.parametrize("d", PACKS, ids=lambda d: d.name)
def test_every_pack_has_a_guide_with_a_card_within_the_caps(d):
    text = (d / "assistant.md").read_text()
    card = registry.card(text)
    assert card and len(card) <= registry.CARD_MAX and len(text) <= registry.GUIDE_MAX
    assert "## Card" not in card and "\n## " not in card


@pytest.mark.parametrize("d", [d for d in PACKS if d.name != "echo"], ids=lambda d: d.name)
def test_every_recipe_fits_its_workflow(d):
    s = spec(d)
    nodes = json.loads(
        (CONTRACTS / f"fixtures/registry/{s['family']}.nodes.valid.json").read_text()
    )
    schemas = {n["kind"]: n for n in nodes["nodes"]}
    workflows = {
        w["id"]: w for w in (json.loads(f.read_text()) for f in d.glob("workflows/*.json"))
    }
    assert s["recipes"] and len({r["name"] for r in s["recipes"]}) == len(s["recipes"])
    for r in s["recipes"]:
        assert registry.recipe_errors(r, workflows[r["workflow"]], schemas) == [], r["name"]


def test_the_fake_runner_publishes_its_guide_and_recipes():
    values, _, _ = registry.build("fake")
    (g,) = values["guides"]["guides"]
    assert (
        g["family"] == "echo"
        and g["title"] == "Echo (fake)"
        and g["card"].startswith("Echo (fake) is")
    )
    assert g["guide"] == (pack.PACKS / "echo/assistant.md").read_text()
    assert g["recipes"][0]["name"] == "quick"


def test_a_recipe_that_does_not_fit_stops_the_runner():
    loaded = [p for p, _ in pack.for_env("fake")]
    values, _, _ = registry.build("fake")
    workflows, schemas = values["workflows"]["workflows"], registry.node_schemas(values)
    echo = loaded[0]
    for bad, why in (({"echo.nope": 1}, "has no param nope"), ({"echo.steps": 999}, "steps"),
                     ({"ghost.steps": 1}, "no node ghost"), ({"echo.bypass": "yes"}, "not true or false")):  # fmt: skip
        echo.spec["recipes"] = [{"name": "bad", "title": "Bad", "workflow": "echo", "params": bad}]
        with pytest.raises(ValueError, match=why):
            registry.guides(loaded, workflows, schemas)
    echo.spec["recipes"] = [
        {"name": "bad", "title": "Bad", "workflow": "nowhere", "params": {"echo.steps": 1}}
    ]
    with pytest.raises(ValueError, match="no workflow nowhere"):
        registry.guides(loaded, workflows, schemas)


def test_a_guide_without_a_card_or_too_long_stops_the_runner(tmp_path):
    p = pack.for_env("fake")[0][0]
    p.dir = tmp_path
    for text in (
        "# No card\n",
        "## Card\n" + "x" * 801,
        "## Card\nok\n" + "y" * registry.GUIDE_MAX,
    ):
        (tmp_path / "assistant.md").write_text(text)
        with pytest.raises(ValueError, match="## Card"):
            registry.guides([p], [], {})
