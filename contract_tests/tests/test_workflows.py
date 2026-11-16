"""Workflows from the registry (the fake runner publishes echo) and the hosts view."""

from helpers import STUDIO, ok


def test_echo_is_listed(api):
    workflows = {w["id"]: w for w in ok(api.get(f"{STUDIO}/workflows"))["workflows"]}
    assert "echo" in workflows and workflows["echo"]["env"] == "fake"


def test_echo_with_node_schemas(api):
    w = ok(api.get(f"{STUDIO}/workflows/echo"))
    assert (w["id"], w["family"], w["env"]) == ("echo", "echo", "fake")
    assert {n: v["kind"] for n, v in w["nodes"].items()} == {
        "prompt": "prompt",
        "echo": "echo/echo",
        "output": "output",
    }
    assert w["nodes"]["echo"]["in"] == {"text": "prompt.text"}
    assert w["nodes"]["output"]["in"] == {"media": "echo.image"}
    schemas = w["node_schemas"]
    assert set(schemas) == {"prompt", "echo/echo", "output"}
    echo = schemas["echo/echo"]
    params = {p["name"]: p for p in echo["params"]}
    assert [p["name"] for p in echo["params"]] == ["steps", "delay_ms", "fail", "refs"]
    assert (
        params["steps"]["type"],
        params["steps"]["default"],
        params["steps"]["min"],
        params["steps"]["max"],
    ) == ("int", 10, 1, 200)
    assert (params["delay_ms"]["type"], params["delay_ms"]["default"]) == ("int", 300)
    assert (params["fail"]["type"], params["fail"]["default"]) == ("bool", False)
    assert not any(
        "step" in p or "when" in p for p in echo["params"]
    )  # carried only when published
    assert echo["inputs"] == [{"name": "text", "type": "Text"}]
    assert echo["outputs"] == [{"name": "image", "type": "Image"}]
    assert [p["name"] for p in schemas["prompt"]["params"]] == ["text"]
    assert schemas["output"]["inputs"] == [
        {"name": "media", "type": "Media"}
    ]  # Image or Video (API 1.11)


def test_unknown_workflow(api):
    for wid in ("no-such-workflow", "Not An Id!"):
        assert api.get(f"{STUDIO}/workflows/{wid}").status_code == 404


def test_fake_host_is_live(api):
    hosts = ok(api.get(f"{STUDIO}/hosts"))["hosts"]
    fake = [h for h in hosts if (h["host"], h["env"]) == ("fake", "fake")]
    assert len(fake) == 1 and fake[0]["state"] in ("idle", "busy"), hosts
    # never a run id or an owner; a runner adds its memory and resident components (test_handover: their shape),
    # 1.16 its machine and cached GB; `admin` only for admins (test_hosts_admin)
    allowed = {
        "host",
        "env",
        "state",
        "updated_at",
        "memory",
        "host_memory",
        "resident",
        "cached_gb",
        "machine",
        "device",  # API 1.18
        "host_ram",
    }
    assert all(set(h) <= allowed for h in hosts)
