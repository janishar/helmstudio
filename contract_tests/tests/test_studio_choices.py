"""API 1.26: an inspector section's choice tiles (StudioLayout.inspector[].choices; LTX's Recipe: two-stage or
one-stage). Kept as saved, shaped by the schema (422 detail), and each option's set checked as a preset."""

import json
from pathlib import Path

from helpers import STUDIO, ok

WF = f"{STUDIO}/workflows"
FIXTURES = Path(__file__).parents[2] / "contracts" / "fixtures" / "http"


def body() -> dict:
    return json.loads((FIXTURES / "WorkflowCreate.valid.1.json").read_text())


def with_choices(b: dict, choices: list) -> dict:
    studio = b["studio"]
    return b | {"studio": studio | {"inspector": [studio["inspector"][0] | {"choices": choices}]}}


def test_choices_are_kept(api):
    b = body()
    want = b["studio"]["inspector"][0]["choices"]
    w = ok(api.post(WF, json=b), 201)
    try:
        assert w["studio"]["inspector"][0]["choices"] == want
        assert ok(api.get(f"{WF}/{w['id']}"))["studio"]["inspector"][0]["choices"] == want
    finally:
        assert api.delete(f"{WF}/{w['id']}").status_code == 204


def test_a_choice_needs_two_options(api):
    one = [{"label": "Speed", "options": [{"name": "Quick", "set": {"echo.steps": 2}}]}]
    r = api.post(WF, json=with_choices(body(), one))
    assert r.status_code == 422, r.text
    assert (
        r.json()["detail"]
        == "studio.inspector[0].choices[0]: a label of 1-40 characters and 2-8 options"
    )


def test_an_option_set_is_checked_as_a_preset(api):
    bad = [
        {
            "label": "Speed",
            "options": [
                {"name": "Quick", "set": {"echo.steps": 0}},
                {"name": "Ghost", "set": {"ghost.x": 1}},
            ],
        }
    ]
    r = api.post(WF, json=with_choices(body(), bad))
    assert r.status_code == 422, r.text
    got = {(p["code"], p.get("node"), p.get("param")) for p in r.json()["problems"]}
    assert got == {("preset_invalid", "echo", "steps"), ("preset_invalid", "ghost", "x")}
    assert ok(api.post(f"{WF}/validate", json=with_choices(body(), bad)))["can_save"] is False


def test_ltx_templates_publish_the_recipe(api):
    """Only when an mlx host has published LTX (the test stack runs fake hosts: then nothing to check)."""
    ids = {w["id"] for w in ok(api.get(WF, params={"source": "template"}))["workflows"]}
    for wid in {"ltx-text-to-video", "ltx-image-to-video"} & ids:
        quality = next(
            s for s in ok(api.get(f"{WF}/{wid}"))["studio"]["inspector"] if s["title"] == "Quality"
        )
        recipe = quality["choices"][0]
        assert recipe["label"] == "Recipe"
        assert [o["name"] for o in recipe["options"]] == ["Two-stage", "One-stage"]
        assert recipe["options"][1]["set"] == {"upscale.bypass": True, "sample2.bypass": True}
