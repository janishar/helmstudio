"""Plan 28 (API 1.53; contracts/README.md "Model sources: thinking"): thinking per model. A PUT's levels are checked
against its preset's rules (422 naming the model), a strings-only PUT reads as before, `thinking_levels` and a model
source's `thinking` show what is set, and a call sends the saved level's fragment with its tokens added (seen on the
fake OpenAI-compatible server). The preset only names the rules: every provider here points at the fake, never at a
real provider."""

import pytest
from helpers import STUDIO, ok
from test_director import (  # noqa: F401 - fixtures
    PROVIDERS,
    admin_view,
    fake,
    fake_ctl,
    provider,
    remote_body,
    sent,
    server_body,
    tag,
)

DEEPSEEK = {"levels": ["off", "low", "high"], "default": "high"}


def test_put_checks_levels_against_the_rules(admin):
    t = tag()
    body = server_body(f"m-{t}") | {"preset": "ollama", "models": [f"qwen3:{t}", f"llama:{t}"]}
    name = f"thk-{t}"
    for thinking, detail in (
        ({f"qwen3:{t}": "high"}, f"thinking: qwen3:{t} takes off, on"),
        ({f"llama:{t}": "off"}, f"thinking: llama:{t} has no thinking setting"),
        ({"other": "off"}, "thinking: other is not one of models"),
    ):
        r = admin.put(f"{PROVIDERS}/{name}", json=body | {"thinking": thinking})
        assert r.status_code == 422 and r.json() == {"detail": detail}, r.text
    assert (
        admin.put(
            f"{PROVIDERS}/{name}", json=body | {"thinking": {f"qwen3:{t}": "max"}}
        ).status_code
        == 422
    )
    with provider(admin, name, body):
        got = next(p for p in ok(admin.get(PROVIDERS))["providers"] if p["name"] == name)
        assert "thinking" not in got  # strings only: as before
        assert got["thinking_levels"] == {f"qwen3:{t}": {"levels": ["off", "on"]}}
        put = ok(admin.put(f"{PROVIDERS}/{name}", json=body | {"thinking": {f"qwen3:{t}": "off"}}))
        assert put["thinking"] == {f"qwen3:{t}": "off"} and put["models"] == body["models"]
        sources = ok(admin.get(f"{STUDIO}/model-sources"))["sources"]
        assert next(s for s in sources if s["name"] == name)["thinking"] == {f"qwen3:{t}": "off"}


def test_a_call_sends_the_saved_level(admin, admin_view, fake_ctl):  # noqa: F811
    if not admin_view["remote_enabled"]:
        pytest.skip("remote models are off on this deployment (REMOTE_MODELS)")
    t = tag()
    flash = f"deepseek-flash-{t}"
    # the preset's rule matches the exact ids; these are custom names of the test, so no rule: 422
    body = remote_body(flash) | {
        "preset": "deepseek",
        "models": ["deepseek-flash", "deepseek-v4-pro", flash],
    }
    r = admin.put(f"{PROVIDERS}/thk-{t}", json=body | {"thinking": {flash: "off"}})
    assert r.status_code == 422, r.text
    body["thinking"] = {"deepseek-flash": "off", "deepseek-v4-pro": "low"}
    with provider(admin, f"thk-{t}", body) as name:
        listed = next(p for p in ok(admin.get(PROVIDERS))["providers"] if p["name"] == name)
        assert listed["thinking_levels"] == {
            "deepseek-flash": DEEPSEEK,
            "deepseek-v4-pro": DEEPSEEK,
        }
        for model in ("deepseek-flash", "deepseek-v4-pro", flash):
            got = ok(admin.post(f"{PROVIDERS}/{name}/test", params={"model": model}))
            assert got["ok"] is True, got
        off, low, custom = (
            sent(m)[-1]["body"] for m in ("deepseek-flash", "deepseek-v4-pro", flash)
        )
        limit = lambda b: b.get("max_completion_tokens", b.get("max_tokens"))
        assert (
            off["thinking"] == {"type": "disabled"}
            and "reasoning_effort" not in off
            and limit(off) == 8
        )
        assert low["thinking"] == {"type": "enabled"} and low["reasoning_effort"] == "low"
        assert limit(low) == 8 + 1024
        assert "thinking" not in custom and limit(custom) == 8  # no rule: nothing new
