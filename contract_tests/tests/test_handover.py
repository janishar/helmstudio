"""The dispatcher's hand-over (API 1.13, contracts/README.md "Two envs on one host: the hand-over") as far as the
fake stack shows it, and the M4 demonstration on the echo family: a still, "Use in…" image-to-video, Save as
pipeline on the clip, then that pipeline over ten rows.

The fake stack has one env (host fake, env fake) and reports no memory, so there is no admission, no unload and
nothing resident: hosts data carries no `host_memory`, the order is plain round-robin, and the demonstration's
release order (row 1 end to end, then every still, then every clip) comes from oldest-first alone. Admission, eviction
and the fairness bound are covered by studio's table tests over contracts/fixtures/dispatch."""

from collections import Counter, defaultdict

import pytest
from helpers import STUDIO, new_session, ok, start_run, take_of_run, wait_run
from test_batches import finish, fresh, rows_of, ts, wait_batch
from test_pipelines import PIPES, submit

I2V = "echo-image-to-video"
KF = "keyframes.images"
STILL = {
    "echo.steps": 5,
    "echo.delay_ms": 100,
}  # ~0.5 s: long enough for every row's still to queue
CLIP = {"sample.delay_ms": 50, "duration.frames": 17, "duration.fps": 8}
ROWS = 10


# ---------------------------------------------------------------- hosts data


def test_hosts_data_shape(api):
    hosts = ok(api.get(f"{STUDIO}/hosts"))["hosts"]
    allowed = {"host", "env", "state", "updated_at", "memory", "host_memory", "resident"}
    item = {"component", "model", "variant", "precision", "gb", "pinned"}
    by_host = defaultdict(list)
    for h in hosts:
        assert set(h) <= allowed, h  # never a run id or an owner
        by_host[h["host"]].append(h)
        for r in h.get("resident", []):
            assert item <= set(r) <= item | {"last_used", "loras"}, r
            for lora in r.get("loras", []):
                if lora["lora_id"] is None:  # another user's own LoRA: only its size
                    assert (lora["name"], lora["strength"]) == (None, None), lora
    for name, envs in by_host.items():
        reporting = [e for e in envs if "memory" in e]
        if not reporting:
            assert all("host_memory" not in e for e in envs), envs  # the fake host: no admission
            continue
        hm = {(e["host_memory"]["budget_gb"], e["host_memory"]["used_gb"]) for e in envs}
        assert len(hm) == 1, (name, hm)  # the same on every env of a host
        budget, used = hm.pop()
        assert budget == pytest.approx(min(e["memory"]["budget_gb"] for e in reporting), abs=0.05)
        # per env the larger of what its entries declare and what it measured (caches count too)
        held = sum(
            max(sum(r["gb"] for r in e.get("resident", [])), e["memory"]["used_gb"])
            for e in reporting
        )
        assert used == pytest.approx(held, abs=0.1), (name, used, held)
    fake = by_host["fake"]
    assert [e["env"] for e in fake] == ["fake"] and "host_memory" not in fake[0], fake


# ---------------------------------------------------------------- still -> Use in… -> Save as pipeline -> ten rows


@pytest.fixture(scope="module")
def still_to_clip(api) -> dict:
    """A's echo still, its clip made through "Use in…", and the pipeline saved from the clip."""
    ids = {w["id"] for w in ok(api.get(f"{STUDIO}/workflows"))["workflows"]}
    if I2V not in ids:
        pytest.skip(f"the fake runner has no {I2V}")
    sid = new_session(api, "Contract hand-over")["id"]
    run = wait_run(api, start_run(api, sid, STILL | {"prompt.text": "a fox in snow"})["id"])
    assert run["status"] == "succeeded", run
    still = take_of_run(api, run["id"])
    use = ok(api.post(f"{STUDIO}/takes/{still['id']}/use", json={"workflow_id": I2V}))
    assert use == {"workflow_id": I2V, "inputs": {KF: [{"image": {"take_id": still["id"]}}]}}
    inputs = CLIP | {"prompt.text": "the fox looks up"} | use["inputs"]
    run = wait_run(api, start_run(api, sid, inputs, I2V)["id"])
    assert run["status"] == "succeeded", run
    clip = take_of_run(api, run["id"])
    assert (clip["kind"], clip["parents"]) == ("video", [still["id"]])
    p = ok(api.post(f"{STUDIO}/takes/{clip['id']}/pipeline", json={"title": "Fox clip"}), 201)
    yield {"still": still, "clip": clip, "pipeline": p}
    api.delete(f"{PIPES}/{p['id']}")


def test_save_as_pipeline_from_a_clip(still_to_clip):
    p, still, clip = (still_to_clip[k] for k in ("pipeline", "still", "clip"))
    assert p["problems"] == [] and p["title"] == "Fox clip"
    # one family (echo) for both steps: the clip's prompt stays a fixed input, no prompt_2
    assert [(i["name"], i["type"]) for i in p["nodes"]["inputs"]["inputs"]] == [("prompt", "Text")]
    s1, s2 = p["nodes"]["step_1"], p["nodes"]["step_2"]
    assert (s1["workflow"], s2["workflow"]) == ("echo", I2V)
    assert s1["bind"] == {"prompt.text": "$prompt"}
    assert s2["bind"] == {KF: "step_1.takes"} and s2["inputs"]["prompt.text"] == "the fox looks up"
    assert KF not in s2["inputs"]
    for s, t in ((s1, still), (s2, clip)):
        assert all(k in t["made_with"]["inputs"] for k in s["inputs"]), (s, t["made_with"])
    assert p["nodes"]["output"] == {
        "kind": "output",
        "from": "step_2.takes",
        "collection": "Fox clip",
    }
    assert p["edges"] == [["inputs", "step_1"], ["step_1", "step_2"], ["step_2", "output"]]


def test_ten_rows_each_model_after_row_1(api, still_to_clip):
    """Ten rows: exactly 10 stills and 10 clips, each clip made from its row's still; row 1 goes first end to end,
    then every still before any clip (on the fake, oldest first already gives what resident-first gives on mac)."""
    p = still_to_clip["pipeline"]
    batch = submit(api, p["id"], fresh([f"row {n} a fox" for n in range(1, ROWS + 1)]))
    bid = batch["id"]
    try:
        assert wait_batch(api, bid)["status"] == "done"
        rows = rows_of(api, bid)
        assert [r["status"] for r in rows] == ["succeeded"] * ROWS, rows
        runs, kinds = [], Counter()
        for r in rows:
            cells = {c["node"]: c["attempts"][-1] for c in r["cells"]}
            (still_id,), (clip_id,) = cells["step_1"]["take_ids"], cells["step_2"]["take_ids"]
            assert r["take_ids"] == [clip_id]
            still = ok(api.get(f"{STUDIO}/takes/{still_id}"))
            clip = ok(api.get(f"{STUDIO}/takes/{clip_id}"))
            assert (still["kind"], still["parents"]) == ("image", [])
            assert (clip["kind"], clip["parents"]) == ("video", [still_id])
            for t, node in ((still, "step_1"), (clip, "step_2")):
                kinds[t["kind"]] += 1
                assert (t["source"], t["batch_id"]) == ("pipeline", bid)
                mp = t["made_with"]["pipeline"]
                assert (mp["id"], mp["node"], mp["row"], mp["attempt"]) == (
                    p["id"],
                    node,
                    r["row"],
                    1,
                )
            for node in ("step_1", "step_2"):
                (rid,) = cells[node]["run_ids"]
                run = ok(api.get(f"{STUDIO}/runs/{rid}"))
                assert (run["lane"], run["attempt"]) == ("batch", 1)
                runs.append((ts(run["started_at"]), r["row"], node))
        assert kinds == {"image": ROWS, "video": ROWS}
        order = [(row, node) for _, row, node in sorted(runs)]
        assert order[:2] == [(1, "step_1"), (1, "step_2")], order  # row 1 first, end to end
        nodes = [node for _, node in order[2:]]
        assert nodes == ["step_1"] * (ROWS - 1) + ["step_2"] * (ROWS - 1), (
            order
        )  # stills, then clips
    finally:
        finish(api, bid)
