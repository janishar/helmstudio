"""Plan 27 (API 1.52; contracts/README.md "Model sources: Keys"): a provider key saved from the admin's form. A provider
at the fake OpenAI-compatible server (remote while the switch is on, else a server), a new key name: saved after the
check (Found), refused by the provider (401: nothing written, Missing), not checkable (503: unreachable, then Save
anyway: Found), replaced, removed (Missing). The test stack writes only to pipelines' own /tmp/provider-keys, never the
real folder. At the end no service's log holds any test key."""

import os
import subprocess

from helpers import STUDIO, ok
from test_director import PROVIDERS, provider, remote_body, server_body, tag

KEYS = f"{STUDIO}/admin/provider-keys"


def logs(service: str) -> str:
    project = os.environ.get("COMPOSE_PROJECT", "helmstudio-test")
    out = subprocess.run(
        ["docker", "logs", f"{project}-{service}-1"], capture_output=True, timeout=60, check=False
    )
    return (out.stdout + out.stderr).decode(errors="replace")


def test_save_replace_and_remove_a_key(api, admin):
    t = tag()
    name, other = f"key-{t}", f"key-{t}-b"
    remote = ok(admin.get(PROVIDERS))["remote_enabled"]
    body = (remote_body if remote else server_body)(f"m-{t}") | {"key_secret": name}
    keys = {f"fake-ok-{t}", f"fake-new-{t}", f"refused-{t}", f"down-{t}"}
    with provider(admin, f"prov-{t}", body) as prov:
        status = ok(admin.get(f"{KEYS}/{name}"))
        assert status == {"key_secret": name, "path": f"deploy/secrets/providers/{name}", "found": False,
                          "writable": True}  # fmt: skip

        def put(n: str, key: str, **kw):
            return admin.put(f"{KEYS}/{n}", json={"key": key, "provider": prov} | kw)

        assert api.put(f"{KEYS}/{name}", json={"key": "x", "provider": prov}).status_code == 403
        assert api.delete(f"{KEYS}/{name}").status_code == 403
        assert (
            admin.put(
                f"{KEYS}/{name}", json={"key": "x", "provider": prov, "base_url": "http://x/v1"}
            ).status_code
            == 422
        )
        assert put(name, "has space").status_code == 422
        assert put(name, "x", preset="ollama").status_code == 422  # both sources
        assert put(name, "x", provider="nope").status_code == 404
        assert put("Bad..Name", "x").status_code == 404

        got = ok(put(name, f"  fake-ok-{t}\n"))
        assert (got["check"], got["saved"], got["found"]) == ("ok", True, True), got
        assert ok(admin.get(f"{KEYS}/{name}"))["found"] is True

        got = ok(put(other, f"refused-{t}"))
        assert got == {"key_secret": other, "path": f"deploy/secrets/providers/{other}", "found": False,
                       "saved": False, "check": "refused", "detail": "the provider refused this key; not saved"}  # fmt: skip
        got = ok(put(other, f"down-{t}"))
        assert (got["check"], got["saved"], got["found"]) == ("unreachable", False, False), got
        assert got["detail"] == "HTTP 503", got
        got = ok(put(other, f"down-{t}", force=True))
        assert (got["check"], got["saved"], got["found"]) == ("skipped", True, True), got

        got = ok(put(name, f"fake-new-{t}"))  # Replace key
        assert got["check"] == "ok" and got["found"] is True

        for n in (name, other):
            assert ok(admin.delete(f"{KEYS}/{n}")) == {"key_secret": n, "path": f"deploy/secrets/providers/{n}",
                                                      "found": False, "writable": True}  # fmt: skip
        assert ok(admin.delete(f"{KEYS}/{name}"))["found"] is False  # missing: fine

    for service in ("studio", "pipelines", "kong", "web", "nats"):
        text = logs(service)
        assert not [k for k in keys if k in text], f"a test key is in {service}'s log"
