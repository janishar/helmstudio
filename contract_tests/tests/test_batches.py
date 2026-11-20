"""Batches of the echo workflow on the fake runner (pipelines_service): rows from lines, a table or a grid, Submit's
checks, row 1 first, pause / resume / stop, a failed row and Retry, the batch lane after interactive runs, batch
events, isolation and the batch's takes. The fake runner is one host: runs go one at a time."""

import time
import uuid
from datetime import datetime

import pytest
from helpers import (
    NO_SUCH_ID,
    STUDIO,
    TERMINAL,
    all_takes,
    new_session,
    ok,
    sse,
    start_run,
    wait_for,
    wait_run,
)

BATCHES = f"{STUDIO}/batches"
FINAL = {"done", "failed", "stopped"}
BATCH_TIMEOUT = 180
FAST = {"echo.steps": 2, "echo.delay_ms": 50}  # base inputs: a row takes ~0.1 s on the host
SLOWER = {"echo.steps": 5, "echo.delay_ms": 300}  # ~1.5 s a row: long enough to act between rows
PROMPTS = ["a red fox", "a blue heron", "a green frog", "a grey wolf", "a black cat", "a white owl"]


def fresh(values: list[str]) -> list[str]:
    """values made unique to this call: the runner caches a node's result by owner and params, so a row repeating an
    earlier one's prompt and inputs is a cache hit that ends in milliseconds, however slow its inputs."""
    tag = uuid.uuid4().hex[:8]
    return [f"{v} {tag}" for v in values]


def lines(values: list[str], param: str = "prompt.text") -> dict:
    return {"lines": {"param": param, "values": values}}


def submit(client, rows: dict, inputs: dict | None = None, **extra) -> dict:
    body = {"workflow_id": "echo", "rows": rows, "inputs": FAST if inputs is None else inputs}
    batch = ok(client.post(BATCHES, json=body | extra), 202)
    assert (batch["status"], batch["control"]) == ("queued", "run"), batch
    return batch


def get(client, bid: str) -> dict:
    return ok(client.get(f"{BATCHES}/{bid}"))


def wait_batch(client, bid: str, statuses=FINAL) -> dict:
    return wait_for(
        lambda: (b := get(client, bid))["status"] in statuses and b,
        timeout=BATCH_TIMEOUT,
        what=f"batch {bid} {statuses}",
    )


def rows_of(client, bid: str) -> list[dict]:
    rows, cursor = [], None
    while True:
        page = ok(client.get(f"{BATCHES}/{bid}/rows", params={"cursor": cursor} if cursor else {}))
        rows += page["rows"]
        if not (cursor := page["next_cursor"]):
            return rows


def wait_rows(client, bid: str, check, what: str) -> list[dict]:
    return wait_for(
        lambda: check(rows := rows_of(client, bid)) and rows, timeout=BATCH_TIMEOUT, what=what
    )


def finish(client, bid: str) -> None:
    """Leave the batch final (a user may have only 5 unfinished)."""
    if get(client, bid)["status"] not in FINAL:
        client.post(f"{BATCHES}/{bid}/stop")
        wait_batch(client, bid)


def ts(value: str) -> datetime:
    return datetime.fromisoformat(value)


def by_status(rows: list[dict]) -> dict[str, int]:
    out: dict[str, int] = {}
    for r in rows:
        out[r["status"]] = out.get(r["status"], 0) + 1
    return out


@pytest.fixture(scope="module")
def lines_batch(api) -> dict:
    """User A's finished batch of 6 echo rows from pasted lines: {"batch" (as submitted), "done", "rows"}."""
    batch = submit(api, lines(PROMPTS))
    try:
        done = wait_batch(api, batch["id"])
    finally:
        finish(api, batch["id"])
    return {"batch": batch, "done": done, "rows": rows_of(api, batch["id"])}


def test_lines_batch_completes(api, lines_batch):
    batch, done, rows = lines_batch["batch"], lines_batch["done"], lines_batch["rows"]
    assert batch["workflow_id"] == "echo" and batch["counts"]["total"] == 6
    assert batch["counts"]["queued"] == 6 and batch["title"]
    assert (done["status"], done["control"], done["reason"]) == ("done", "run", None)
    assert done["counts"] == {
        "total": 6,
        "queued": 0,
        "running": 0,
        "waiting": 0,
        "succeeded": 6,
        "failed": 0,
        "cancelled": 0,
    }
    assert done["started_at"] and done["finished_at"] and done["session_id"]
    assert [r["row"] for r in rows] == list(range(1, 7))
    for r, prompt in zip(rows, PROMPTS, strict=True):
        assert (r["status"], r["attempt"], r["error"]) == ("succeeded", 1, None), r
        assert r["inputs"] == FAST | {"prompt.text": prompt}
        assert r["run_id"] and len(r["take_ids"]) == 1 and r["thumb_url"]
        run = ok(api.get(f"{STUDIO}/runs/{r['run_id']}"))
        assert (run["lane"], run["batch_id"], run["batch_row"]) == ("batch", batch["id"], r["row"])
        assert (run["status"], run["session_id"]) == ("succeeded", done["session_id"])
        assert [o["take_id"] for o in run["outputs"]] == r["take_ids"]
    assert len({r["run_id"] for r in rows}) == 6
    listed = ok(api.get(BATCHES))["batches"]
    assert batch["id"] in [b["id"] for b in listed]


def test_row_one_runs_alone_first(api, lines_batch):
    rows = lines_batch["rows"]
    first = ok(api.get(f"{STUDIO}/runs/{rows[0]['run_id']}"))
    for r in rows[1:]:
        run = ok(api.get(f"{STUDIO}/runs/{r['run_id']}"))
        # the other rows' runs were not even asked for before row 1 ended
        assert ts(run["created_at"]) >= ts(first["finished_at"]), (first, run)
        assert ts(r["started_at"]) >= ts(rows[0]["finished_at"]), (rows[0], r)


def test_batch_takes_in_gallery(api, lines_batch):
    bid = lines_batch["batch"]["id"]
    ids = {t for r in lines_batch["rows"] for t in r["take_ids"]}
    takes = {t["id"]: t for t in all_takes(api, source="pipeline")}
    assert ids <= set(takes)
    for tid in ids:
        assert (takes[tid]["source"], takes[tid]["batch_id"]) == ("pipeline", bid)
        assert ok(api.get(f"{STUDIO}/takes/{tid}"))["batch_id"] == bid


def test_batch_collection(api, api_b, lines_batch):
    """API 1.27: batch_id lists the batch's takes (Open collection); another user's or an unknown batch matches
    nothing; it ANDs with the other filters."""
    bid = lines_batch["batch"]["id"]
    ids = {t for r in lines_batch["rows"] for t in r["take_ids"]}
    assert {t["id"] for t in all_takes(api, batch_id=bid)} == ids
    assert all_takes(api, batch_id=bid, kind="video") == []
    assert all_takes(api_b, batch_id=bid) == []
    assert all_takes(api, batch_id=NO_SUCH_ID) == []


def test_batch_events_ordered(api, lines_batch):
    bid = lines_batch["batch"]["id"]
    path = f"{BATCHES}/{bid}/events"
    page = ok(api.get(path, params={"since": 0}))
    seqs = [e["seq"] for e in page["events"]]
    assert seqs and seqs == sorted(set(seqs)) and page["last_seq"] == seqs[-1]
    assert {e["type"] for e in page["events"]} == {"batch", "row"}
    assert any(e["type"] == "batch" and e["data"]["status"] == "done" for e in page["events"])
    empty = ok(api.get(path, params={"since": page["last_seq"]}))
    assert (empty["events"], empty["last_seq"]) == ([], page["last_seq"])

    def is_done(frames):
        return any(f["event"] == "batch" and f["data"]["data"]["status"] == "done" for f in frames)

    frames = sse(api, bid, is_done, path=path, schema="BatchEvent")
    assert [f["id"] for f in frames] == [f["data"]["seq"] for f in frames]
    assert [f["id"] for f in frames] == sorted({f["id"] for f in frames})
    assert all(f["event"] == f["data"]["type"] for f in frames)
    # every row ended succeeded in the stream too
    ends = {
        f["data"]["data"]["row"]: f["data"]["data"]["status"] for f in frames if f["event"] == "row"
    }
    assert ends == dict.fromkeys(range(1, 7), "succeeded")
    # since= and Last-Event-ID replay only what follows (the header wins)
    mid = seqs[len(seqs) // 2]
    later = sse(api, bid, is_done, path=path, schema="BatchEvent", since=mid)
    assert later and all(f["id"] > mid for f in later)
    header = sse(api, bid, is_done, path=path, schema="BatchEvent", since=0, last_event_id=mid)
    assert [f["id"] for f in header] == [f["id"] for f in later]


def test_isolation(api_b, lines_batch):
    bid, rid = lines_batch["batch"]["id"], lines_batch["rows"][0]["run_id"]
    for path in (f"{BATCHES}/{bid}", f"{BATCHES}/{bid}/rows", f"{BATCHES}/{bid}/events"):
        assert api_b.get(path).status_code == 404, path
    for action in ("pause", "resume", "stop", "rows/1/retry"):
        assert api_b.post(f"{BATCHES}/{bid}/{action}").status_code == 404, action
    r = api_b.get(BATCHES)
    assert bid not in [b["id"] for b in ok(r)["batches"]] and bid not in r.text
    assert api_b.get(f"{STUDIO}/runs/{rid}").status_code == 404
    for path in (f"{BATCHES}/{NO_SUCH_ID}", f"{BATCHES}/not-a-uuid"):
        assert api_b.get(path).status_code == 404


def test_table_rows(api):
    table = {
        "columns": ["prompt.text", "echo.steps"],
        "rows": [["a fox", 1], ["a heron", 2], ["a frog", 3]],
    }
    batch = submit(api, {"table": table}, {"echo.delay_ms": 50}, title="  steps sweep  ")
    try:
        assert batch["title"] == "steps sweep" and batch["counts"]["total"] == 3
        rows = rows_of(api, batch["id"])
        assert [r["inputs"] for r in rows] == [
            {"echo.delay_ms": 50, "prompt.text": p, "echo.steps": s} for p, s in table["rows"]
        ]
        assert all(
            (r["status"], r["attempt"], r["run_id"]) == ("queued", 1, None) for r in rows[1:]
        )
    finally:
        finish(api, batch["id"])


def test_grid_rows(api):
    axes = [
        {"param": "prompt.text", "values": ["a", "b", "c"]},
        {"param": "echo.steps", "values": [1, 2]},
    ]
    batch = submit(api, {"grid": {"axes": axes}})
    try:
        assert batch["title"] == "Echo × 6"  # no title: "<workflow title> × <row count>"
        rows = rows_of(api, batch["id"])
        got = [(r["inputs"]["prompt.text"], r["inputs"]["echo.steps"]) for r in rows]
        # the first axis varies slowest
        assert got == [("a", 1), ("a", 2), ("b", 1), ("b", 2), ("c", 1), ("c", 2)]
        assert all(r["inputs"]["echo.delay_ms"] == 50 for r in rows)
    finally:
        finish(api, batch["id"])


def test_seeds_need_a_seed_param(api):
    r = api.post(
        BATCHES,
        json={"workflow_id": "echo", "rows": lines(["x"]), "seeds": {"count": 2, "start": 5}},
    )
    assert r.status_code == 422 and "seed" in r.json()["detail"], r.text


def test_seeds_repeat_rows(api):
    """echo-voice-over (env fake) has the one seed param (echo has none; qwen-image would load a model); its voice
    param has no default, so every row sends the starter voice echo/ada."""
    w = ok(api.get(f"{STUDIO}/workflows/echo-voice-over"))
    seed_key = next(
        f"{node}.{p['name']}"
        for node, n in w["nodes"].items()
        for p in w["node_schemas"].get(n["kind"], {}).get("params", [])
        if p["type"] == "seed"
    )
    text_key = "prompt.text"
    body = {
        "workflow_id": w["id"],
        "rows": lines(["one", "two"], text_key),
        "inputs": {"speak.voice": {"voice_id": "echo/ada"}, "speak.delay_ms": 0},
        "seeds": {"count": 3, "start": 100},
    }
    batch = ok(api.post(BATCHES, json=body), 202)
    try:
        rows = rows_of(api, batch["id"])
        texts = [r["inputs"][text_key] for r in rows]
        assert texts == ["one"] * 3 + ["two"] * 3  # repeats sit next to each other
        seeds = [r["inputs"][seed_key] for r in rows]
        assert seeds[:3] == [100, 101, 102]
        assert seeds[4] == seeds[3] + 1 and seeds[5] == seeds[3] + 2
    finally:
        finish(api, batch["id"])


@pytest.mark.parametrize(
    ("body", "detail"),
    [
        (
            {
                "rows": {
                    "table": {
                        "columns": ["prompt.text", "echo.steps"],
                        "rows": [["a", 2], ["b", 2], ["c", 999]],
                    }
                }
            },
            "row 3: echo.steps",
        ),
        (
            {
                "rows": {
                    "grid": {
                        "axes": [
                            {"param": "prompt.text", "values": [str(i) for i in range(30)]},
                            {"param": "echo.steps", "values": list(range(1, 21))},
                        ]
                    }
                }
            },
            "a batch has 1 to 500 rows; this one has 600",
        ),
        (
            {"workflow_id": "no-such-workflow", "rows": lines(["x"])},
            "unknown workflow no-such-workflow",
        ),
    ],
    ids=["invalid-row", "row-cap", "unknown-workflow"],
)
def test_submit_rejects(api, body, detail):
    before = {b["id"] for b in ok(api.get(BATCHES, params={"limit": 200}))["batches"]}
    r = api.post(BATCHES, json={"workflow_id": "echo"} | body)
    assert r.status_code == 422 and detail in r.json()["detail"], r.text
    after = {b["id"] for b in ok(api.get(BATCHES, params={"limit": 200}))["batches"]}
    assert after == before  # nothing stored


def test_submit_rejects_no_live_host(api):
    live = {h["env"] for h in ok(api.get(f"{STUDIO}/hosts"))["hosts"] if h["state"] != "offline"}
    dead = [w for w in ok(api.get(f"{STUDIO}/workflows"))["workflows"] if w["env"] not in live]
    if not dead:
        pytest.skip("every published workflow has a live host on this stack")
    w = dead[0]
    r = api.post(BATCHES, json={"workflow_id": w["id"], "rows": lines(["x"])})
    assert r.status_code == 422 and r.json()["detail"] == f"no live host for env {w['env']}"


def test_pause_holds_and_resume_continues(api):
    batch = submit(api, lines(fresh(PROMPTS)), SLOWER)
    bid = batch["id"]
    try:
        wait_rows(api, bid, lambda rs: rs[0]["status"] in TERMINAL, "row 1 to end")
        paused = ok(api.post(f"{BATCHES}/{bid}/pause"), 202)
        assert paused["control"] == "pause"
        assert api.post(f"{BATCHES}/{bid}/pause").status_code == 409
        assert wait_batch(api, bid, {"paused"})["control"] == "pause"
        # the row running at Pause finishes; nothing else starts
        held = wait_rows(
            api, bid, lambda rs: "running" not in by_status(rs), "the running row to end"
        )
        queued = [r["row"] for r in held if r["status"] == "queued"]
        assert queued, "every row ended before the pause took hold: slow the rows down"
        time.sleep(4)
        still = rows_of(api, bid)
        assert [r["row"] for r in still if r["status"] == "queued"] == queued
        for r in still:
            if r["status"] == "queued" and r["run_id"]:
                run = ok(api.get(f"{STUDIO}/runs/{r['run_id']}"))
                assert run["status"] == "queued" and "position" not in run  # held
        assert get(api, bid)["status"] == "paused"

        resumed = ok(api.post(f"{BATCHES}/{bid}/resume"), 202)
        assert resumed["control"] == "run"
        done = wait_batch(api, bid)
        assert done["status"] == "done" and done["counts"]["succeeded"] == 6
    finally:
        finish(api, bid)


def test_stop_cancels_the_rest(api):
    batch = submit(api, lines(fresh(PROMPTS)), SLOWER)
    bid = batch["id"]
    try:
        wait_rows(api, bid, lambda rs: rs[0]["status"] in TERMINAL, "row 1 to end")
        stopping = ok(api.post(f"{BATCHES}/{bid}/stop"), 202)
        assert stopping["control"] == "stop"
        stopped = wait_batch(api, bid)
        assert (stopped["status"], stopped["control"]) == ("stopped", "stop")
        assert stopped["finished_at"]
        rows = rows_of(api, bid)
        assert rows[0]["status"] == "succeeded"
        counts = by_status(rows)
        assert set(counts) <= {"succeeded", "failed", "cancelled"} and counts.get("cancelled"), rows
        assert stopped["counts"]["cancelled"] == counts["cancelled"]
        for action in ("stop", "resume", "pause"):
            assert api.post(f"{BATCHES}/{bid}/{action}").status_code == 409, action
        for r in rows:  # no run of the batch is left going
            if r["run_id"]:
                assert ok(api.get(f"{STUDIO}/runs/{r['run_id']}"))["status"] in TERMINAL
    finally:
        finish(api, bid)


def test_failed_row_and_retry(api):
    table = {
        "columns": ["prompt.text", "echo.fail"],
        "rows": [["ok one", False], ["bad", True], ["ok two", False], ["ok three", False]],
    }
    batch = submit(api, {"table": table})
    bid = batch["id"]
    try:
        done = wait_batch(api, bid)
        assert done["status"] == "done"  # one failed row does not stop the others
        assert (done["counts"]["succeeded"], done["counts"]["failed"]) == (3, 1)
        rows = rows_of(api, bid)
        bad = rows[1]
        assert bad["status"] == "failed" and bad["error"] and bad["take_ids"] == []
        assert all(r["status"] == "succeeded" for i, r in enumerate(rows) if i != 1)
        assert api.post(f"{BATCHES}/{bid}/rows/1/retry").status_code == 409  # only failed rows
        assert api.post(f"{BATCHES}/{bid}/rows/99/retry").status_code == 404

        again = ok(api.post(f"{BATCHES}/{bid}/rows/2/retry"), 202)
        assert (again["row"], again["attempt"], again["status"]) == (2, 2, "queued")
        assert (again["run_id"], again["error"], again["take_ids"]) == (None, None, [])
        assert get(api, bid)["status"] in ("done", "running")
        wait_rows(
            api,
            bid,
            lambda rs: rs[1]["attempt"] == 2 and rs[1]["status"] in TERMINAL,
            "the retried row to end",
        )
        done = wait_batch(api, bid)
        after = rows_of(api, bid)
        assert after[1]["run_id"] not in (None, bad["run_id"])  # a new run for the new attempt
        assert after[1]["status"] == "failed"  # echo.fail is still true
        old = ok(api.get(f"{STUDIO}/runs/{bad['run_id']}"))
        assert old["status"] == "failed"  # the old run stays
        for i in (0, 2, 3):  # the other rows were not run again
            assert (after[i]["run_id"], after[i]["attempt"]) == (rows[i]["run_id"], 1)
    finally:
        finish(api, bid)


def test_interactive_before_batch_rows(api):
    batch = submit(api, lines(fresh(PROMPTS[:5])), SLOWER)
    bid = batch["id"]
    try:
        # rows are waiting in the batch lane: row 1 ended and another row runs
        wait_rows(
            api,
            bid,
            lambda rs: rs[0]["status"] in TERMINAL and "running" in by_status(rs[1:]),
            "the batch rows to be released",
        )
        sid = new_session(api, "Contract interactive")["id"]
        run = wait_run(api, start_run(api, sid, FAST | {"prompt.text": "jump the line"})["id"])
        assert run["status"] == "succeeded"
        wait_batch(api, bid)
        rows = rows_of(api, bid)
        later = sorted(
            (r for r in rows if ts(r["started_at"]) > ts(run["created_at"])),
            key=lambda r: ts(r["started_at"]),
        )
        assert len(later) >= 2, ("the rows ran too fast to queue behind the interactive run", rows)
        # one host: the one row that may have been released (sent, not yet reported running) before the interactive
        # run was created can start after it was created; every row released after that waits for it
        assert all(ts(run["started_at"]) <= ts(r["started_at"]) for r in later[1:]), (
            run["created_at"],
            run["started_at"],
            [(r["row"], r["started_at"]) for r in rows],
        )
    finally:
        finish(api, bid)
