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
    assert [p["name"] for p in echo["params"]] == ["steps", "delay_ms", "fail"]
    assert (
        params["steps"]["type"],
        params["steps"]["default"],
        params["steps"]["min"],
        params["steps"]["max"],
    ) == ("int", 10, 1, 200)
    assert (params["delay_ms"]["type"], params["delay_ms"]["default"]) == ("int", 300)
    assert (params["fail"]["type"], params["fail"]["default"]) == ("bool", False)
    assert echo["inputs"] == [{"name": "text", "type": "Text"}]
    assert echo["outputs"] == [{"name": "image", "type": "Image"}]
    assert [p["name"] for p in schemas["prompt"]["params"]] == ["text"]
    assert schemas["output"]["inputs"] == [{"name": "media", "type": "Image"}]


def test_unknown_workflow(api):
    for wid in ("no-such-workflow", "Not An Id!"):
        assert api.get(f"{STUDIO}/workflows/{wid}").status_code == 404


def test_fake_host_is_live(api):
    hosts = ok(api.get(f"{STUDIO}/hosts"))["hosts"]
    fake = [h for h in hosts if (h["host"], h["env"]) == ("fake", "fake")]
    assert len(fake) == 1 and fake[0]["state"] in ("idle", "busy"), hosts
    # never a run id or an owner
    assert all(set(h) <= {"host", "env", "state", "updated_at"} for h in hosts)
