"""Plan 25 (API 1.51; contracts/README.md "Model sources: presets"): the presets list (the contracts file, admin only),
a preset's Fetch (the URL always the preset's: a body base_url is 422; 404 unknown), a saved provider's Fetch (its Test
without a model, on the fake OpenAI-compatible server), and a key file's status (the fake's placeholder `fake-openai`
found, `nope` missing, a malformed name 404). Never a remote preset's Fetch: the test stack must never reach a real
provider. A local preset's Fetch only checks the shape (whatever answers on the host, if anything)."""

import json
from pathlib import Path

from helpers import STUDIO, ok
from test_director import KEY_SECRET, PROVIDERS, provider, server_body, tag

PRESETS = f"{STUDIO}/admin/provider-presets"
KEYS = f"{STUDIO}/admin/provider-keys"
FILE = json.loads((Path(__file__).parents[2] / "contracts/providers/presets.json").read_text())


def test_presets_list_is_the_file_admin_only(api, admin):
    assert api.get(PRESETS).status_code == 403
    got = ok(admin.get(PRESETS))
    assert got["presets"] == FILE["presets"]
    assert got["remote_enabled"] == ok(admin.get(PROVIDERS))["remote_enabled"]


def test_a_preset_fetch_takes_only_a_key_file(api, admin):
    assert api.post(f"{PRESETS}/ollama/models", json={}).status_code == 403
    assert admin.post(f"{PRESETS}/nope/models", json={}).status_code == 404
    r = admin.post(f"{PRESETS}/ollama/models", json={"base_url": "http://169.254.169.254/latest"})
    assert r.status_code == 422, r.text
    r = admin.post(f"{PRESETS}/deepseek/models", json={"key_secret": "../deepseek"})
    assert r.status_code == 422, r.text  # never reaches pipelines, so nothing leaves the stack
    got = ok(
        admin.post(f"{PRESETS}/llama-server/models")
    )  # a local preset: the host's port 9931, if anything
    assert got["failure"] in (None, "network", "refused", "answer"), got
    assert isinstance(got["models"], list)


def test_a_saved_provider_fetches_through_its_test(admin):
    t = tag()
    with provider(admin, f"srv-{t}", server_body(f"m-{t}")) as name:
        got = ok(admin.post(f"{PROVIDERS}/{name}/test"))
        assert got["ok"] is True and got["answer"] is None, got
        assert {"fake-large", "fake-small"} <= set(got["models"])


def test_key_file_status(api, admin):
    assert api.get(f"{KEYS}/{KEY_SECRET}").status_code == 403
    found = ok(admin.get(f"{KEYS}/{KEY_SECRET}"))
    assert found == {
        "key_secret": KEY_SECRET,
        "path": f"deploy/secrets/providers/{KEY_SECRET}",
        "found": True,
        "writable": True,  # API 1.52: the test stack writes /tmp/provider-keys
    }
    assert ok(admin.get(f"{KEYS}/nope"))["found"] is False
    assert admin.get(f"{KEYS}/Bad%20Name").status_code == 404
