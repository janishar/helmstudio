"""Timeline edits by ops (API 1.49, plan 24; contracts/README.md "Timeline: edits by ops"): the CLI, built from ../cli
and signed in as a new user, edits a three-clip sequence by ops (a dry run, an apply, a 409 from a parallel write,
a restore, the history); helm mcp lists the new tools with their hints, tries and applies an edit and reads a frame
as image content. Three uploaded images, no GPU. Needs Go on PATH (the CLI is built here)."""

import json
import os
import subprocess

import pytest
from conftest import API_KEY, BASE_URL, headers, make_client
from helpers import STUDIO, ok, png_bytes, upload
from test_mcp import Mcp, helm, structured  # noqa: F401 - helm is the module's fixture

TL = f"{STUDIO}/timelines"


@pytest.fixture(scope="module")
def editor(new_user, anon, tmp_path_factory) -> dict:
    """A new user signed in for the CLI (its HOME), its API client and a sequence of three image clips."""
    u = new_user("Edits")
    home = tmp_path_factory.mktemp("home")
    cfg = home / ".config" / "helm" / "config.json"
    cfg.parent.mkdir(parents=True)
    cfg.write_text(
        json.dumps(
            {
                "url": BASE_URL,
                "access_token": u["access_token"],
                "refresh_token": u["refresh_token"],
            }
        )
    )
    with make_client(headers(u["access_token"])) as api:
        takes = [upload(api, anon, png_bytes(64, 32), f"still{i}.png")["id"] for i in range(3)]
        yield {"home": home, "api": api, "takes": takes}


def run_helm(helm_bin, editor, *args: str) -> subprocess.CompletedProcess:
    env = os.environ | {"HOME": str(editor["home"]), "HELM_API_KEY": API_KEY}
    return subprocess.run(
        [str(helm_bin), *args], check=False, env=env, capture_output=True, text=True, timeout=60
    )


def ids(tl: dict) -> list[str]:
    return [c["id"] for c in tl["tracks"][0]["clips"]]


def test_cli_edits_by_ops(helm, editor, tmp_path):  # noqa: F811
    api = editor["api"]
    tl = ok(api.post(TL, json={"name": "By ops", "take_ids": editor["takes"]}), 201)
    tid, first = tl["id"], ids(tl)
    assert len(first) == 3 and tl["revision"] == 1
    ops = tmp_path / "ops.json"
    ops.write_text(
        json.dumps([{"op": "split", "track": "V1", "at": 1.5}, {"op": "remove", "clip": first[2]}])
    )

    # a dry run: the diff, nothing stored
    r = run_helm(
        helm, editor, "timeline", "edit", tid, "-f", str(ops), "--revision", "1", "--dry-run"
    )
    assert r.returncode == 0, r.stderr
    assert (
        "revision 1 (dry run: nothing stored)" in r.stdout and f"removed: {first[2]}" in r.stdout
    ), r.stdout
    assert ok(api.get(f"{TL}/{tid}"))["revision"] == 1

    # the apply: one revision, a studio-made id for the split's right part, labelled via api (helm)
    r = run_helm(helm, editor, "timeline", "edit", tid, "-f", str(ops), "--revision", "1")
    assert r.returncode == 0 and "revision 2\n" in r.stdout, r.stdout + r.stderr
    now = ok(api.get(f"{TL}/{tid}"))
    assert (
        now["revision"] == 2
        and len(ids(now)) == 3
        and ids(now)[:1] == first[:1]
        and first[2] not in ids(now)
    )
    newest = ok(api.get(f"{TL}/{tid}/revisions", params={"limit": 1}))["revisions"][0]
    assert (newest["by"], newest["via"], newest["ops_summary"]) == ("user", "api", "split, remove")

    # a parallel write moves the sequence on: the CLI's edit planned on revision 2 is the 409
    other = {"base_revision": 2, "ops": [{"op": "track", "track": "V1", "mute": True}]}
    assert ok(api.post(f"{TL}/{tid}/edits", json=other))["revision"] == 3
    r = run_helm(helm, editor, "timeline", "edit", tid, "-f", str(ops), "--revision", "2")
    assert r.returncode == 1 and "409" in r.stderr and "now revision 3" in r.stderr, r.stderr
    assert ok(api.get(f"{TL}/{tid}"))["revision"] == 3

    # restore the first revision: written forward, the three clips back
    r = run_helm(helm, editor, "timeline", "restore", tid, "--to", "1", "--revision", "3")
    assert r.returncode == 0 and "revision 4" in r.stdout, r.stdout + r.stderr
    assert ids(ok(api.get(f"{TL}/{tid}"))) == first
    r = run_helm(helm, editor, "timeline", "revisions", tid)
    lines = r.stdout.splitlines()
    assert (
        r.returncode == 0
        and len(lines) == 4
        and lines[0].startswith("r4")
        and lines[0].endswith("restore")
    ), r.stdout

    # the agent view, as the CLI prints it
    r = run_helm(helm, editor, "timeline", "show", tid, "--agent")
    view = json.loads(r.stdout)
    assert (
        view["revision"] == 4
        and [c["kind"] for c in view["clips"]] == ["image"] * 3
        and view["clips"][1]["at_f"] == 72
    )


def test_mcp_tries_applies_and_looks(helm, editor):  # noqa: F811
    api = editor["api"]
    tl = ok(api.post(TL, json={"name": "By mcp", "take_ids": editor["takes"]}), 201)
    tid = tl["id"]
    m = Mcp(helm, editor["home"])
    try:
        m.rpc("initialize", {"protocolVersion": "2025-06-18", "capabilities": {},
                             "clientInfo": {"name": "contract", "version": "1"}})  # fmt: skip
        m.p.stdin.write(
            json.dumps({"jsonrpc": "2.0", "method": "notifications/initialized"}) + "\n"
        )
        m.p.stdin.flush()
        tools = {t["name"]: t for t in m.rpc("tools/list", {})["tools"]}
        assert tools["try_timeline_edit"]["annotations"]["readOnlyHint"] is True
        assert tools["timeline_frame"]["annotations"]["readOnlyHint"] is True
        for name in ("edit_timeline", "restore_timeline"):
            assert tools[name]["annotations"]["destructiveHint"] is True, tools[name]
        ops = tools["edit_timeline"]["inputSchema"]["properties"]["ops"]["items"]["oneOf"]
        assert "fill" not in {o["properties"]["op"]["const"] for o in ops}

        view = structured(m.tool("show_timeline", {"timeline_id": tid}))["timeline"]
        clip = view["clips"][1]["id"]
        args = {
            "timeline_id": tid,
            "base_revision": view["revision"],
            "ops": [{"op": "remove", "clip": clip}],
        }
        tried = structured(m.tool("try_timeline_edit", args))
        assert (
            tried["dry_run"] is True
            and tried["diff"]["removed"] == [clip]
            and tried["revision"] == 1
        )
        done = structured(m.tool("edit_timeline", args))
        assert done["revision"] == 2 and clip not in ids(done["timeline"])
        again = m.tool("edit_timeline", args)  # planned on revision 1: refused
        assert again.get("isError") and "409" in again["content"][0]["text"], again
        hist = structured(m.tool("timeline_revisions", {"timeline_id": tid}))["revisions"][
            "revisions"
        ]
        assert (hist[0]["via"], hist[0]["ops_summary"]) == ("mcp", "remove")

        res = m.tool("timeline_frame", {"timeline_id": tid, "at": 0.5})
        frame = structured(res)
        images = [c for c in res["content"] if c["type"] == "image"]
        assert frame["shown"] == "image" and frame["empty"] is False, frame
        assert len(images) == 1 and images[0]["mimeType"] == "image/png" and images[0]["data"]
        assert structured(m.tool("timeline_frame", {"timeline_id": tid, "at": 60}))["empty"] is True

        back = structured(m.tool("restore_timeline", {"timeline_id": tid, "revision": 2, "to": 1}))
        assert back["revision"] == 3 and len(ids(back)) == 3
    finally:
        m.p.stdin.close()
        try:
            m.p.wait(10)
        except subprocess.TimeoutExpired:
            m.p.kill()
