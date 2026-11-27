"""helm mcp over stdio (plan 16, docs/plan/16-mcp.md 20.4): the CLI, built from ../cli and signed in as a new user,
runs the echo workflow through the run_workflow tool, waits for it with wait_run and sees its take with get_take
(a thumbnail as image content); another user's take id is a 404 through the tool. Fake mode, no GPU. Needs Go on PATH
(the CLI is built here)."""

import json
import os
import shutil
import subprocess
from pathlib import Path

import pytest
from conftest import API_KEY, BASE_URL
from helpers import QUICK, STUDIO, ok, wait_for

CLI = Path(__file__).resolve().parents[2] / "cli"


class Mcp:
    """One helm mcp process: newline-delimited JSON-RPC on its stdin and stdout."""

    def __init__(self, helm: Path, home: Path):
        env = os.environ | {"HOME": str(home), "HELM_API_KEY": API_KEY}
        self.p = subprocess.Popen(
            [str(helm), "mcp"], stdin=subprocess.PIPE, stdout=subprocess.PIPE, env=env, text=True
        )
        self.n = 0
        self.progress: list[str] = []

    def rpc(self, method: str, params: dict) -> dict:
        self.n += 1
        self.p.stdin.write(
            json.dumps({"jsonrpc": "2.0", "id": self.n, "method": method, "params": params}) + "\n"
        )
        self.p.stdin.flush()
        while line := self.p.stdout.readline():
            msg = json.loads(line)
            if msg.get("method") == "notifications/progress":
                self.progress.append(msg["params"].get("message", ""))
            elif msg.get("id") == self.n:
                assert "error" not in msg, msg
                return msg["result"]
        raise AssertionError(f"helm mcp ended: {self.p.wait(5)}")

    def tool(self, name: str, args: dict) -> dict:
        return self.rpc(
            "tools/call", {"name": name, "arguments": args, "_meta": {"progressToken": name}}
        )


@pytest.fixture(scope="module")
def helm(tmp_path_factory) -> Path:
    if not shutil.which("go"):
        pytest.skip("go is not on PATH: helm mcp is built from ../cli")
    out = tmp_path_factory.mktemp("bin") / "helm"
    subprocess.run(["go", "build", "-o", str(out), "."], cwd=CLI, check=True, timeout=300)
    return out


@pytest.fixture
def mcp(helm, new_user, tmp_path):
    u = new_user("Mcp")
    cfg = tmp_path / ".config" / "helm" / "config.json"
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
    m = Mcp(helm, tmp_path)
    init = m.rpc(
        "initialize",
        {
            "protocolVersion": "2025-06-18",
            "capabilities": {},
            "clientInfo": {"name": "contract", "version": "1"},
        },
    )
    assert init["serverInfo"]["name"] == "helmstudio"
    m.p.stdin.write(json.dumps({"jsonrpc": "2.0", "method": "notifications/initialized"}) + "\n")
    m.p.stdin.flush()
    yield m
    m.p.stdin.close()
    try:
        m.p.wait(10)
    except subprocess.TimeoutExpired:
        m.p.kill()


def structured(res: dict) -> dict:
    assert not res.get("isError"), res
    return res["structuredContent"]


def test_mcp_runs_echo_waits_and_gets_the_take(mcp, api, echo_done):
    tools = {t["name"]: t for t in mcp.rpc("tools/list", {})["tools"]}
    assert {"run_workflow", "wait_run", "get_take", "delete_take"} <= set(tools)
    assert "sessions" not in tools  # the assistant's
    assert tools["delete_take"]["annotations"]["destructiveHint"] is True

    run = structured(mcp.tool("run_workflow", {"workflow_id": "echo", "inputs": QUICK}))
    assert run["status"] == "queued" and run["workflow_id"] == "echo"
    done = structured(mcp.tool("wait_run", {"run_id": run["id"], "timeout_s": 120}))
    assert done["still_running"] is False and done["run"]["status"] == "succeeded", done
    take_id = done["run"]["outputs"][0]["take_id"]

    res = mcp.tool("get_take", {"take_id": take_id})
    assert structured(res)["take"]["id"] == take_id
    images = [c for c in res["content"] if c["type"] == "image"]
    assert len(images) == 1 and images[0]["mimeType"].startswith("image/") and images[0]["data"]

    # user A's take, through B's tool: the API's 404 as a tool error
    a_run = ok(api.get(f"{STUDIO}/runs/{echo_done['run']['id']}"))
    res = mcp.tool("get_take", {"take_id": a_run["outputs"][0]["take_id"]})
    assert res.get("isError") and "404" in res["content"][0]["text"], res


def test_mcp_magic_board_from_empty_to_a_sequence(mcp):
    """plan 16 §20.8: a board made, edited, run on the echo model, looked at and sent, all through helm mcp. No card
    over MCP: edit_board applies at once (destructiveHint, as edit_timeline), run_blocks answers its plan."""
    tools = {t["name"]: t for t in mcp.rpc("tools/list", {})["tools"]}
    magic = {"list_boards", "create_board", "show_board", "try_board_edit", "edit_board", "restore_board"}
    magic |= {"plan_board", "run_blocks", "make_views", "board_frame", "send_to_timeline"}
    assert magic <= set(tools)
    assert tools["edit_board"]["annotations"]["destructiveHint"] is True
    assert tools["try_board_edit"]["annotations"]["readOnlyHint"] is True
    assert tools["run_blocks"]["annotations"]["destructiveHint"] is False
    assert "card" not in tools["edit_board"]["description"]

    b = structured(mcp.tool("create_board", {"name": "Mcp board"}))
    bid = b["board_id"]
    assert b["revision"] == 1
    assert bid in [x["id"] for x in structured(mcp.tool("list_boards", {}))["boards"]]
    request = {"kind": "video", "model": "echo-video", "prompt": "a fox", "advanced": {"video.delay_ms": 0}}
    shot = {"id": "s1", "lane": "shots", "at": 0, "length": 24, "kind": "video", "name": "Shot 1"}
    ops = [
        {"op": "lane_add", "lane": {"id": "shots", "name": "Shots", "role": "shots"}},
        {"op": "add", "mode": "insert", "block": shot | {"request": request, "takes": [], "effects": []}},
        {"op": "asset_add", "asset": {"id": "a1", "kind": "character", "handle": "fox", "name": "Fox"}},
    ]
    edit = {"board_id": bid, "base_revision": 1, "ops": ops}
    tried = structured(mcp.tool("try_board_edit", edit))
    assert tried["revision"] == 1 and tried["diff"]["blocks"]["added"] == ["s1"], tried
    done = structured(mcp.tool("edit_board", edit))
    assert done["revision"] == 2 and done["summary"], done
    stale = mcp.tool("edit_board", edit)  # base_revision 1 is stale now
    assert stale.get("isError") and "409" in stale["content"][0]["text"], stale

    shown = structured(mcp.tool("show_board", {"board_id": bid}))
    assert [x["id"] for x in shown["board"]["blocks"]] == ["s1"] and shown["plan"]["blocks"]
    plan = structured(mcp.tool("plan_board", {"board_id": bid}))
    assert [x["block"] for x in plan["blocks"]] == ["s1"]
    ran = structured(mcp.tool("run_blocks", {"board_id": bid, "revision": 2, "blocks": ["s1"]}))
    assert ran["plan"]["blocks"][0]["block"] == "s1" and [x["block"] for x in ran["started"]] == ["s1"], ran
    views = structured(
        mcp.tool("make_views", {"board_id": bid, "revision": ran["revision"], "asset": "a1", "views": ["Front"]})
    )
    assert [x["view"] for x in views["started"]] == ["Front"] and views["seconds"] is None, views
    assert "seconds" in ran and "summary" not in tried, (ran, tried)

    def board():
        v = structured(mcp.tool("show_board", {"board_id": bid}))["board"]
        assets = {a["id"]: a for a in v["assets"]}
        ready = v["blocks"][0]["status"] == "done" and assets["a1"]["sheet"]
        return v if ready else None

    v = wait_for(board, what="the shot and the view done")
    frame = structured(mcp.tool("board_frame", {"board_id": bid, "at": 0.5}))
    assert frame["frame"] == 12 and frame["shown"][0]["block"] == "s1" and frame["shown"][0]["take_id"], frame

    sent = structured(mcp.tool("send_to_timeline", {"board_id": bid, "revision": v["revision"]}))
    assert sent["send"]["status"] == "done" and sent["plan"]["bakes"] == 0, sent
    back = structured(mcp.tool("restore_board", {"board_id": bid, "revision": v["revision"], "to": 1}))
    assert back == {"revision": v["revision"] + 1}
