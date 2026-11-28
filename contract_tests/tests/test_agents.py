"""Agent nodes (API 1.14, M4b stage A) on the fake runner: judge, write, decide and memory nodes in pipelines, their
decisions, replay and memory. The fake ask workflow `echo-ask` answers the first JSON value after the last
`ECHO-ANSWER:` in its prompt (contracts/README.md, The llm pack: the fake), so every answer here is scripted in the
row's prompt: the judge's brief is the step's prompt; a write reads the judge's reasons after the prompt, so a
judge reason scripts the write; a decide reads its `from` texts.

Echo has no seed param, so "best of 4" is a user workflow with four echo → output chains: one run, four takes.
Expected problems, bounds and models come from contracts/check.py against the live workflows and palette; expected
verdicts from check.judge_verdict. Skipped when the fake runner has no `echo-ask` (before API 1.14)."""

import json

import pytest
from conftest import headers, make_client
from helpers import NO_SUCH_ID, STUDIO, all_takes, check, ok, wait_for, wait_run
from test_batches import BATCHES, FINAL, finish, fresh, lines, rows_of, wait_batch, wait_rows
from test_pipelines import (
    PIPELINE_FIXTURES,
    PIPES,
    batch_takes,
    cell,
    create,
    echo_step,
    ended,
    execution,
    inputs_node,
    live_sources,
    live_workflows,
    parked,
)
from test_user_workflows import WF, copy_echo, doc, keys, put

ASK = "echo-ask"
FINAL_RUN = {"succeeded", "failed", "cancelled"}
I2V = "echo-image-to-video"
MEMORY = f"{STUDIO}/memory"
CRITERIA = {"must": ["Shows the subject"], "nice": ["Sharp"]}
PASS, NICE_FAILS, FAILS = [True, True], [True, False], [False, True]
CLIP_INPUTS = {"sample.delay_ms": 50, "duration.frames": 17, "duration.fps": 8}
AGENT_KINDS = {"judge", "write", "decide", "memory"}
NO_TOOLS = {"tool_calls": 0, "remote_calls": 0, "remote_tokens": 0}  # the 1.15 bounds, always sent
AGENT_FIXTURES = [
    p
    for p in PIPELINE_FIXTURES
    if AGENT_KINDS & {n["kind"] for n in json.loads(p.read_text())["pipeline"]["nodes"].values()}
]


@pytest.fixture(scope="module", autouse=True)
def ask_live(api) -> dict:
    r = api.get(f"{WF}/{ASK}")
    if r.status_code != 200:
        pytest.skip(f"the fake runner has no {ASK} workflow (before API 1.14)")
    return r.json()


# ---------------------------------------------------------------- scripts and documents


def answer(*checks: list[bool], ranking: list[int] | None = None, reasons=None) -> dict:
    """A judge answer (judge/v2 schema): per take its checks (must, then nice) and a reason; ranking 1..N."""
    reasons = reasons or {}
    return {
        "takes": [
            {"take": i, "checks": c, "reason": reasons.get(i, "fine" if c[0] else "no subject")}
            for i, c in enumerate(checks, start=1)
        ],
        "ranking": ranking or list(range(1, len(checks) + 1)),
    }


def compact(value) -> str:
    return json.dumps(value, separators=(",", ":"))


def scripted(text: str, value) -> str:
    """`text`, then the fake's answer. A script nested in a string of that answer (the next node's) is written with
    its colon as \\u003a, so this one stays the last `ECHO-ANSWER:` until the JSON is decoded."""
    return f"{text} ECHO-ANSWER: " + compact(value).replace("ECHO-ANSWER:", "ECHO-ANSWER\\u003a")


def best_of_4(k: int) -> dict:
    """Take k passes every check, the next passes the must only, the rest fail the must: k is kept."""
    nxt = k % 4 + 1
    return answer(*[PASS if i == k else NICE_FAILS if i == nxt else FAILS for i in range(1, 5)])


@pytest.fixture(scope="module")
def four(api) -> str:
    """A's user workflow: one prompt, four echo → output chains (four takes per run)."""
    w = copy_echo(api, "Contract four takes")
    d = doc(w)
    d["nodes"]["echo"]["params"] |= {"steps": 2, "delay_ms": 50}
    for i in (2, 3, 4):
        d["nodes"][f"echo_{i}"] = {
            "kind": "echo/echo",
            "in": {"text": "prompt.text"},
            "params": {"steps": 1, "delay_ms": 0},
        }
        d["nodes"][f"output_{i}"] = {"kind": "output", "in": {"media": f"echo_{i}.image"}}
    put(api, w["id"], d, w["version"])
    return w["id"]


@pytest.fixture(scope="module")
def i2v(api) -> None:
    if api.get(f"{WF}/{I2V}").status_code != 200:
        pytest.skip(f"the fake runner has no {I2V} workflow")


def judged_clip(wid: str, title: str, mode: str = "auto", memory: bool = False) -> dict:
    """inputs → [mem →] still (four takes) → judge (keep 1) → [note →] clip (first frame = the pick) → out."""
    nodes = {
        "inputs": inputs_node("prompt"),
        "still": {"kind": "step", "workflow": wid, "bind": {"prompt.text": "$prompt"}},
        "judge": {"kind": "judge", "takes": "still", "criteria": CRITERIA, "keep": 1, "mode": mode},
        "clip": {
            "kind": "step",
            "workflow": I2V,
            "inputs": CLIP_INPUTS,
            "bind": {"prompt.text": "$prompt", "keyframes.images": "judge.picked"},
        },
        "out": {"kind": "output", "from": "clip.takes", "collection": title},
    }
    edges = [["inputs", "still"], ["still", "judge"], ["judge", "clip"], ["clip", "out"]]
    if memory:
        nodes["mem"] = {"kind": "memory", "op": "read", "limit": 8}
        nodes["judge"]["memory"] = "mem"
        nodes["note"] = {
            "kind": "memory",
            "op": "write",
            "key": "reasons",
            "from": ["judge.reasons"],
        }
        edges = [
            ["inputs", "mem"],
            ["mem", "still"],
            ["still", "judge"],
            ["judge", "note"],
            ["note", "clip"],
            ["clip", "out"],
        ]
    return {"title": title, "nodes": nodes, "edges": edges}


def rewrite_loop(title: str = "Contract rewrite") -> dict:
    """inputs → still (echo) → judge (auto; fail → rewrite → still, at most once, then out) → out (the pick)."""
    return {
        "title": title,
        "nodes": {
            "inputs": inputs_node("prompt"),
            "still": echo_step({"prompt.text": "$prompt"}),
            "judge": {"kind": "judge", "takes": "still", "criteria": CRITERIA, "mode": "auto"},
            "rewrite": {"kind": "write", "from": ["$prompt", "judge.reasons"], "to": "$prompt"},
            "out": {"kind": "output", "from": "judge.picked", "collection": title},
        },
        "edges": [
            ["inputs", "still"],
            ["still", "judge"],
            ["judge", {"pass": "out", "fail": "rewrite", "max": 1, "then": "out"}],
            ["rewrite", "still"],
        ],
    }


def routed(title: str = "Contract decide") -> dict:
    """inputs → still (echo) → check (decide: retry → still at most once, then accept → out)."""
    return {
        "title": title,
        "nodes": {
            "inputs": inputs_node("prompt"),
            "still": echo_step({"prompt.text": "$prompt"}),
            "check": {
                "kind": "decide",
                "question": "Make the still again?",
                "from": ["$prompt"],
                "routes": {"retry": "it misses the brief", "accept": "good enough"},
            },
            "out": {"kind": "output", "from": "still.takes", "collection": title},
        },
        "edges": [
            ["inputs", "still"],
            ["still", "check"],
            ["check", {"routes": {"retry": "still", "accept": "out"}, "max": 1, "then": "accept"}],
        ],
    }


# ---------------------------------------------------------------- reading results


def submit(client, pid: str, values: list[str]) -> dict:
    body = {"pipeline_id": pid, "rows": lines(values, "inputs.prompt")}
    batch = ok(client.post(BATCHES, json=body), 202)
    assert (batch["status"], batch["control"]) == ("queued", "run"), batch
    return batch


def decisions(client, bid: str, n: int, **params) -> list[dict]:
    return ok(client.get(f"{BATCHES}/{bid}/rows/{n}/decisions", params=params))["decisions"]


def ask_runs(client, batch: dict) -> list[dict]:
    """The batch's runs of the ask workflow (its session's runs: at most 100)."""
    runs = ok(client.get(f"{STUDIO}/sessions/{batch['session_id']}/runs"))["runs"]
    return [r for r in runs if r.get("batch_id") == batch["id"] and r["workflow_id"] == ASK]


def judge_pick(client, bid: str, n: int, take_ids: list[str], note: str | None = None) -> dict:
    body = {"gate": "judge", "take_ids": take_ids} | ({"note": note} if note else {})
    return ok(client.post(f"{BATCHES}/{bid}/rows/{n}/pick", json=body), 202)


def expected(doc_: dict, client, palette: dict) -> tuple[set, dict]:
    info = {}
    problems = check.pipeline_problems(
        doc_, live_workflows(client, doc_), palette, [], {}, info, **live_sources(client)
    )
    return problems, info


# ---------------------------------------------------------------- rules


@pytest.mark.parametrize("path", AGENT_FIXTURES, ids=lambda p: p.stem)
def test_agent_fixture_rules(api, palette, path):
    """Problems as check.py finds them against what is live, and the worst case and models once saving is allowed."""
    fx = json.loads(path.read_text())
    want, info = expected(fx["pipeline"], api, palette)
    v = ok(api.post(f"{PIPES}/validate", json=fx["pipeline"]))
    assert keys(v["problems"]) == want, v["problems"]
    if v["can_save"]:
        assert v["bounds"] == info["bounds"], v
    assert v.get("models", {}) == info["models"], v


@pytest.mark.parametrize(
    "make, bounds",
    [
        (lambda wid: judged_clip(wid, "Contract rules"), {"runs": 2, "llm_calls": 1} | NO_TOOLS),
        (
            lambda wid: judged_clip(wid, "Contract rules", "suggest", True),
            {"runs": 2, "llm_calls": 1} | NO_TOOLS,
        ),
        (lambda wid: rewrite_loop(), {"runs": 2, "llm_calls": 4} | NO_TOOLS),
        (lambda wid: routed(), {"runs": 2, "llm_calls": 2} | NO_TOOLS),
    ],
    ids=["judge", "judge-memory", "rewrite", "decide"],
)
def test_echo_agent_documents_run(api, palette, four, i2v, make, bounds):
    d = make(four)
    want, info = expected(d, api, palette)
    assert want == set() and info["bounds"] == bounds
    v = ok(api.post(f"{PIPES}/validate", json=d))
    assert (v["problems"], v["can_run"], v["bounds"]) == ([], True, bounds), v
    assert v["models"] == {
        n: ASK for n, x in d["nodes"].items() if x["kind"] in ("judge", "write", "decide")
    }


def test_agent_rule_codes(api, palette, four):
    """One broken echo document: each 1.14 code on its node, equal to check.py."""
    d = rewrite_loop("Contract broken agents")
    d["nodes"]["inputs"] = {
        "kind": "inputs",
        "inputs": [{"name": "prompt", "type": "Text"}, {"name": "n", "type": "Int"}],
    }
    d["nodes"]["judge"]["criteria"] = {"must": ["A", "A"]}  # judge_criteria: a line twice
    d["nodes"]["judge"]["model"] = "echo"  # a step workflow: unknown_model (run)
    d["nodes"]["rewrite"]["to"] = "$n"  # write_target
    d["nodes"]["rewrite"]["from"] = ["still.takes"]  # bind_source from
    d["edges"][3] = ["rewrite", {"pass": "still", "fail": "still", "max": 1, "then": "still"}]
    want, _ = expected(d, api, palette)
    v = ok(api.post(f"{PIPES}/validate", json=d))
    assert keys(v["problems"]) == want
    codes = {(p["code"], p.get("node")) for p in v["problems"]}
    for c in (
        ("judge_criteria", "judge"),
        ("unknown_model", "judge"),
        ("write_target", "rewrite"),
        ("bind_source", "rewrite"),
        ("branch_not_gate", "rewrite"),
    ):
        assert c in codes, v["problems"]
    step = rewrite_loop("Contract ask as a step")
    step["nodes"]["still"]["workflow"] = ASK
    assert ("workflow_cannot_run", "still") in {
        (p["code"], p.get("node")) for p in ok(api.post(f"{PIPES}/validate", json=step))["problems"]
    }


def test_text_workflows_are_internal(api, ask_live):
    assert ask_live["id"] == ASK
    assert ASK not in {w["id"] for w in ok(api.get(WF))["workflows"]}
    detail = f"workflow {ASK} answers text; only a judge, write or decide node runs it"
    sid = ok(api.post(f"{STUDIO}/sessions", json={"title": "Contract ask"}), 201)["id"]
    r = api.post(f"{STUDIO}/sessions/{sid}/runs", json={"workflow_id": ASK, "inputs": {}})
    assert r.status_code == 422 and r.json()["detail"] == detail, r.text
    r = api.post(BATCHES, json={"workflow_id": ASK, "rows": lines(["x"], "ask.prompt")})
    assert r.status_code == 422 and r.json()["detail"] == detail, r.text
    assert ok(api.get(f"{STUDIO}/sessions/{sid}/runs"))["runs"] == []


# ---------------------------------------------------------------- best of 4, Auto


@pytest.fixture(scope="module")
def auto_batch(api, four, i2v) -> dict:
    """A's finished Auto best-of-4 batch over 3 rows; row r keeps take r + 1."""
    p = create(api, judged_clip(four, "Contract best of 4"))
    picks = {1: 2, 2: 3, 3: 4}
    prompts = [scripted(t, best_of_4(picks[n])) for n, t in enumerate(fresh(["a fox"] * 3), 1)]
    batch = submit(api, p["id"], prompts)
    try:
        done = wait_batch(api, batch["id"])
    finally:
        finish(api, batch["id"])
    return {"pipeline": p, "batch": batch, "done": done, "picks": picks, "prompts": prompts}


def test_best_of_4_auto(api, api_b, auto_batch, ask_live):
    batch, bid = auto_batch["batch"], auto_batch["batch"]["id"]
    assert auto_batch["done"]["status"] == "done" and auto_batch["done"]["counts"]["succeeded"] == 3
    assert batch["bounds"] == {"runs": 2, "llm_calls": 1} | NO_TOOLS and batch["models"] == {
        "judge": ASK
    }
    assert (
        len(batch_takes(api, bid)) == 3 * 4 + 3
    )  # four stills and a clip per row; the judge makes none
    asks = ask_runs(api, batch)
    assert len(asks) == 3
    for r in rows_of(api, bid):
        n = r["row"]
        assert (r["status"], len(r["take_ids"])) == ("succeeded", 1), r
        assert [(c["node"], c["kind"]) for c in r["cells"]] == [
            ("still", "step"),
            ("judge", "judge"),
            ("clip", "step"),
            ("out", "output"),
        ]
        (still,) = cell(r, "still")["attempts"]
        cands = still["take_ids"]
        assert len(cands) == 4
        ans = best_of_4(auto_batch["picks"][n])
        want = check.judge_verdict(ans, CRITERIA, 1, cands)
        picked = cands[auto_batch["picks"][n] - 1]
        assert want["picked"] == [picked]

        (j,) = cell(r, "judge")["attempts"]
        jd = j["judged"]
        assert {
            k: jd[k] for k in ("verdict", "picked", "of", "keep", "first_failed", "agreed")
        } == {
            "verdict": "pass",
            "picked": 1,
            "of": 4,
            "keep": 1,
            "first_failed": None,
            "agreed": None,
        }
        assert jd["takes"] == [
            {
                "take_id": t,
                "passes": want["passes"][i],
                "first_failed": None if want["passes"][i] else "Shows the subject",
                "reason": ans["takes"][i]["reason"],
                "checks": ans["takes"][i]["checks"],
            }
            for i, t in enumerate(cands)
        ]
        assert j["take_ids"] == [picked] and j["decision_id"] == f"{bid}:{n}:1:judge:1"

        clip = ok(api.get(f"{STUDIO}/takes/{r['take_ids'][0]}"))
        assert (clip["kind"], clip["parents"]) == (
            "video",
            [picked],
        )  # lineage: clip → the kept still

        (d,) = decisions(api, bid, n)
        assert (d["id"], d["kind"], d["node"], d["visit"], d["attempt"]) == (
            j["decision_id"],
            "judge",
            "judge",
            1,
            1,
        )
        assert (d["replayed"], d["replayed_from"], d["workflow"], d["template"]) == (
            False,
            None,
            ASK,
            "judge/v2",
        )
        assert (d["seen"], d["error"], d["memory_read"]) == ([], None, [])  # the fake sees nothing
        assert auto_batch["prompts"][n - 1].split(" ECHO-ANSWER")[0] in d["prompt"]
        assert json.loads(d["answers"][-1]["text"]) == ans and d["answers"][-1]["error"] is None
        dj = d["judge"]
        assert (dj["picked"], dj["verdict"], dj["first_failed"], dj["mode"], dj["keep"]) == (
            [picked],
            "pass",
            None,
            "auto",
            1,
        )
        assert [(t["take_id"], t["passes"], t["rank"]) for t in dj["takes"]] == list(
            zip(cands, want["passes"], want["ranks"], strict=True)
        )
        assert dj["takes"][0]["checks"] == [
            {
                "criterion": "Shows the subject",
                "level": "must",
                "pass": ans["takes"][0]["checks"][0],
            },
            {"criterion": "Sharp", "level": "nice", "pass": ans["takes"][0]["checks"][1]},
        ]

        # the decision on the takes it weighed
        outcome = {True: "passed", False: "failed"}
        for i, t in enumerate(cands):
            got = ok(api.get(f"{STUDIO}/takes/{t}"))["decisions"]
            assert got == [
                {
                    "decision_id": d["id"],
                    "batch_id": bid,
                    "row": n,
                    "node": "judge",
                    "outcome": "picked" if t == picked else outcome[want["passes"][i]],
                    "first_failed": None if want["passes"][i] else "Shows the subject",
                }
            ], got
        assert [
            x["id"] for x in ok(api.get(f"{STUDIO}/takes/{picked}/decisions"))["decisions"]
        ] == [d["id"]]

        # the ask run: an answer, no take, not in the gallery, the owner's only
        (rid,) = d["run_ids"]
        run = ok(api.get(f"{STUDIO}/runs/{rid}"))
        assert (
            run["workflow_id"],
            run["status"],
            run["outputs"],
            run["lane"],
            run["batch_id"],
        ) == (
            ASK,
            "succeeded",
            [],
            "batch",
            bid,
        )
        assert json.loads(run["answer"]["text"]) == ans
        assert rid in {x["id"] for x in asks}
        assert api_b.get(f"{STUDIO}/runs/{rid}").status_code == 404
    gallery = {t["run_id"] for t in all_takes(api, session=batch["session_id"])}
    assert gallery and not gallery & {x["id"] for x in asks}


def test_judge_execution_view(api, auto_batch, ask_live):
    bid = auto_batch["batch"]["id"]
    ex = execution(api, bid, 1)
    assert [(s["node"], s["part"]) for s in ex["steps"]] == [
        ("inputs", "inputs"),
        ("still", "submit"),
        ("still", "wait"),
        ("judge", "submit"),
        ("judge", "wait"),
        ("judge", "verdict"),
        ("judge", "route"),
        ("clip", "submit"),
        ("clip", "wait"),
        ("out", "output"),
    ]
    (d,) = decisions(api, bid, 1)
    by_part = {s["part"]: s for s in ex["steps"] if s["node"] == "judge"}
    assert by_part["wait"]["run_ids"] == d["run_ids"]
    v = by_part["verdict"]["decision"]  # the step details: checks, thumbnails, model, memory read
    unsigned = lambda x: {k: x[k] for k in x if k not in ("judge", "seen")}
    assert unsigned(v) == unsigned(d) and v["judge"]["picked"] == d["judge"]["picked"]
    assert v.get("workflow_title", ask_live["title"]) == ask_live["title"]
    assert by_part["route"]["edge"]["to"] == "clip"


def test_decisions_isolation(api, api_b, auto_batch):
    bid = auto_batch["batch"]["id"]
    picked = cell(rows_of(api, bid)[0], "judge")["attempts"][0]["take_ids"][0]
    for method, path, body in [
        ("GET", f"{BATCHES}/{bid}/rows/1/decisions", None),
        ("GET", f"{STUDIO}/takes/{picked}/decisions", None),
        ("POST", f"{BATCHES}/{bid}/again", {"decisions": "same"}),
    ]:
        r = api_b.request(method, path, json=body)
        assert r.status_code == 404, f"{method} {path}: {r.status_code} {r.text}"
    assert api.get(f"{BATCHES}/{bid}/rows/1/decisions", params={"attempt": 2}).status_code == 404
    assert api.get(f"{BATCHES}/{bid}/rows/9/decisions").status_code == 404


# ---------------------------------------------------------------- replay


def test_pause_resume_and_run_again(api, four, i2v, auto_batch):
    """Pause / Resume asks nothing twice; Run again with the same decisions asks nothing; deciding again asks anew."""
    p = auto_batch["pipeline"]
    prompts = [scripted(t, best_of_4(1)) for t in fresh(["a heron", "a crane"])]
    batch = submit(api, p["id"], prompts)
    bid = batch["id"]
    try:
        ok(api.post(f"{BATCHES}/{bid}/pause"), 202)
        assert wait_batch(api, bid, {"paused", *FINAL})["status"] == "paused"
        r = api.post(f"{BATCHES}/{bid}/again", json={"decisions": "same"})
        assert r.status_code == 409 and r.json()["detail"] == f"batch {bid} has not ended", r.text
        ok(api.post(f"{BATCHES}/{bid}/resume"), 202)
        assert wait_batch(api, bid)["status"] == "done"
    finally:
        finish(api, bid)
    source = {n: decisions(api, bid, n) for n in (1, 2)}
    assert all(len(ds) == 1 and not ds[0]["replayed"] for ds in source.values()), source
    assert len(ask_runs(api, batch)) == 2  # one per judge visit: nothing asked twice

    same = ok(api.post(f"{BATCHES}/{bid}/again", json={"decisions": "same"}), 201)
    try:
        assert (same["again_of"], same["same_decisions"], same["session_id"]) == (
            bid,
            True,
            batch["session_id"],
        )
        assert same["title"] == f"{batch['title']} again"
        assert wait_batch(api, same["id"])["status"] == "done"
    finally:
        finish(api, same["id"])
    assert ask_runs(api, same) == []  # every decision reused: no model asked
    for n in (1, 2):
        (d,) = decisions(api, same["id"], n)
        assert (d["replayed"], d["replayed_from"], d["run_ids"]) == (True, source[n][0]["id"], [])
        assert d["judge"]["verdict"] == "pass"
        r = rows_of(api, same["id"])[n - 1]
        assert r["status"] == "succeeded" and len(r["take_ids"]) == 1
        clip = ok(api.get(f"{STUDIO}/takes/{r['take_ids'][0]}"))
        assert clip["parents"] == [
            cell(r, "still")["attempts"][0]["take_ids"][0]
        ]  # take 1 kept again

    again = ok(api.post(f"{BATCHES}/{bid}/again", json={"decisions": "again"}), 201)
    try:
        assert (again["again_of"], again["same_decisions"]) == (bid, False)
        assert wait_batch(api, again["id"])["status"] == "done"
    finally:
        finish(api, again["id"])
    assert len(ask_runs(api, again)) == 2
    for n in (1, 2):
        (d,) = decisions(api, again["id"], n)
        assert (d["replayed"], d["replayed_from"], len(d["run_ids"])) == (False, None, 1)


# ---------------------------------------------------------------- Suggest and memory


def test_suggest_parks_and_learns_from_the_next_batch(api, four, i2v, ask_live):
    p = create(api, judged_clip(four, "Contract suggest", "suggest", memory=True))
    pid = p["id"]
    prompts = [
        scripted(t, best_of_4(k)) for t, k in zip(fresh(["a fox", "a hare"]), (2, 3), strict=True)
    ]
    batch = submit(api, pid, prompts)
    bid = batch["id"]
    note = "the warmer light reads better"
    try:
        rows = parked(api, bid, [1, 2])
        cands = {r["row"]: cell(r, "still")["attempts"][0]["take_ids"] for r in rows}
        suggested = {1: [cands[1][1]], 2: [cands[2][2]]}
        for r in rows:
            n = r["row"]
            pk = r["parked"]
            assert (pk["gate"], pk["kind"], pk["suggested"], pk["candidates"]) == (
                "judge",
                "judge",
                suggested[n],
                cands[n],
            )
            assert pk["decision_id"] == f"{bid}:{n}:1:judge:1"
            jd = cell(r, "judge")["attempts"][0]["judged"]
            assert (jd["verdict"], jd["suggested"], jd["agreed"]) == ("pass", suggested[n], None)
        review = ok(api.get(f"{BATCHES}/{bid}/review"))["items"]
        assert [i["row"] for i in review] == [1, 2]
        for item in review:
            n = item["row"]
            assert (item["kind"], item["gate"], item["suggested"], item["verdict"]) == (
                "judge",
                "judge",
                suggested[n],
                "pass",
            )
            assert (
                item["model"] == ask_live["title"] and item["decision_id"] == f"{bid}:{n}:1:judge:1"
            )
            want = check.judge_verdict(best_of_4(n + 1), CRITERIA, 1, cands[n])
            assert [c["judge"]["rank"] for c in item["candidates"]] == want["ranks"]
            assert [c["judge"]["passes"] for c in item["candidates"]] == want["passes"]

        judge_pick(api, bid, 1, suggested[1])  # agrees
        judge_pick(api, bid, 2, [cands[2][0]], note=note)  # another take, with a note
        assert wait_batch(api, bid)["status"] == "done"
    finally:
        finish(api, bid)

    for n, agreed, kept in ((1, True, suggested[1]), (2, False, [cands[2][0]])):
        r = rows_of(api, bid)[n - 1]
        j = cell(r, "judge")["attempts"][0]
        assert (j["judged"]["agreed"], j["take_ids"]) == (agreed, kept)
        (d,) = decisions(api, bid, n)
        assert (d["judge"]["agreed"], d["judge"]["user_pick"], d["judge"]["picked"]) == (
            agreed,
            kept,
            suggested[n],
        )
        clip = ok(api.get(f"{STUDIO}/takes/{r['take_ids'][0]}"))
        assert clip["parents"] == kept
    assert ok(api.get(f"{STUDIO}/takes/{cands[2][0]}"))["decisions"][0]["outcome"] == "picked"

    mem = ok(api.get(MEMORY, params={"pipeline_id": pid}))
    assert mem["counts"] == {"judge_example": 1, "note": 2}
    (ex,) = [i for i in mem["items"] if i["kind"] == "judge_example"]
    assert (ex["key"], ex["text"], ex["agreed"], [t["id"] for t in ex["kept"]]) == (
        "judge",
        note,
        False,
        [cands[2][0]],
    )
    assert ex["source"] == {"kind": "pick", "batch_id": bid, "row": 2, "node": "judge", "visit": 1}
    notes = [i for i in mem["items"] if i["kind"] == "note"]
    assert {(i["key"], i["source"]["kind"], i["source"]["row"]) for i in notes} == {
        ("reasons", "node", 1),
        ("reasons", "node", 2),
    }
    first = ok(api.get(f"{BATCHES}/{bid}"))["memory"]
    assert (first["examples"], first["notes"]) == (0, 0)  # its snapshot was taken before the pick
    assert all(d["memory_read"] == [] for n in (1, 2) for d in decisions(api, bid, n))

    nxt = submit(api, pid, [scripted(t, best_of_4(1)) for t in fresh(["a stoat"])])
    try:
        (r,) = parked(api, nxt["id"], [1])
        snap = wait_for_memory(api, nxt["id"])
        assert (snap["examples"], snap["notes"]) == (1, 2)
        (d,) = decisions(api, nxt["id"], 1)
        assert set(d["memory_read"]) == {ex["id"], *(i["id"] for i in notes)}
        assert note in d["prompt"]
        steps = execution(api, nxt["id"], 1)["steps"]
        reads = {x for s in steps if s.get("memory") for x in s["memory"]["read"]}
        assert reads == set(d["memory_read"])
        judge_pick(api, nxt["id"], 1, r["parked"]["suggested"])
        assert wait_batch(api, nxt["id"])["status"] == "done"
    finally:
        finish(api, nxt["id"])
    assert api.delete(f"{PIPES}/{pid}").status_code == 204


def wait_for_memory(client, bid: str) -> dict:
    get = lambda: ok(client.get(f"{BATCHES}/{bid}"))["memory"]
    return wait_for(get, what=f"batch {bid}'s memory snapshot")


# ---------------------------------------------------------------- fail → write → retry, capped


def test_judge_fail_rewrites_and_gives_up_at_the_cap(api):
    p = create(api, rewrite_loop())
    tag = fresh(["v"])[0]
    j2 = answer(FAILS, reasons={1: "still no subject"})
    p2 = scripted(f"a fox, second try {tag}", j2)
    w1 = {"text": p2, "note": "named the subject"}
    j1 = answer(FAILS, reasons={1: scripted("no subject", w1)})
    assert len(j1["takes"][0]["reason"]) <= 300
    p1 = scripted(f"a fox {tag}", j1)
    batch = submit(api, p["id"], [p1])
    bid = batch["id"]
    try:
        r = ended(api, bid, 1)
    finally:
        finish(api, bid)
    assert (r["status"], r["take_ids"], r["error"]) == (
        "succeeded",
        [],
        None,
    )  # then = out, nothing kept
    assert [c["node"] for c in r["cells"]] == ["still", "judge", "rewrite", "out"]
    still, judge = cell(r, "still")["attempts"], cell(r, "judge")
    assert len(still) == 2 and len({a["run_ids"][0] for a in still}) == 2
    second = ok(api.get(f"{STUDIO}/takes/{still[1]['take_ids'][0]}"))
    assert second["made_with"]["inputs"]["prompt.text"] == p2  # the rewritten prompt ran
    assert judge["loop"] == {"turns": 1, "max": 1, "then": "out", "gave_up": True}
    assert [a["judged"]["verdict"] for a in judge["attempts"]] == ["fail", "fail"]
    assert [a["judged"]["first_failed"] for a in judge["attempts"]] == ["Shows the subject"] * 2
    assert [a["judged"]["takes"][0]["reason"] for a in judge["attempts"]] == [
        j1["takes"][0]["reason"],
        "still no subject",
    ]

    ds = decisions(api, bid, 1)
    assert [(d["node"], d["kind"], d["visit"], d["template"]) for d in ds] == [
        ("judge", "judge", 1, "judge/v2"),
        ("rewrite", "write", 1, "write/v2"),
        ("judge", "judge", 2, "judge/v2"),
    ]
    assert ds[1]["write"] == {
        "to": "$prompt",
        "before": p1,
        "after": p2,
        "note": "named the subject",
    }
    assert len(ask_runs(api, batch)) == 3
    (rw,) = cell(r, "rewrite")["attempts"]
    assert rw["decision_id"] == ds[1]["id"]
    ex = execution(api, bid, 1)
    assert ex["loops"] == {"judge": 1}
    apply = [s for s in ex["steps"] if (s["node"], s["part"]) == ("rewrite", "apply")]
    assert [s["decision"]["id"] for s in apply] == [ds[1]["id"]]
    routes = [s["edge"] for s in ex["steps"] if (s["node"], s["part"]) == ("judge", "route")]
    assert routes == [{"to": "rewrite", "via": "fail"}, {"to": "out", "via": "then"}]


# ---------------------------------------------------------------- decide


def test_decide_routes(api):
    p = create(api, routed())
    tag = fresh(["d"])[0]
    # once bad, then good: the fake's retry answers what follows ECHO-RETRY: (contracts/README.md, the fake)
    good = {"route": "accept", "reason": "second try"}
    once_bad = {"route": "accept", "reason": "x", "stray": 1}
    prompts = [
        scripted(f"accept {tag}", {"route": "accept", "reason": "fine"}),
        scripted(f"retry {tag}", {"route": "retry", "reason": "misses it"}),
        scripted(f"nowhere {tag}", {"route": "nowhere", "reason": "?"}),
        scripted(f"once bad {tag}", once_bad) + f" ECHO-RETRY: {compact(good)}",
    ]
    batch = submit(api, p["id"], prompts)
    bid = batch["id"]
    try:
        rows = wait_rows(
            api, bid, lambda rs: all(r["status"] in ("succeeded", "failed") for r in rs), "rows end"
        )
    finally:
        finish(api, bid)

    one, two, three, four_ = rows
    assert (one["status"], len(cell(one, "still")["attempts"])) == ("succeeded", 1)
    (d,) = decisions(api, bid, 1)
    assert d["decide"] == {"route": "accept", "to": "out", "reason": "fine", "forced": False}
    assert cell(one, "check")["attempts"][0]["route"] == {"label": "accept", "to": "out"}

    assert (two["status"], len(cell(two, "still")["attempts"])) == ("succeeded", 2)
    ds = decisions(api, bid, 2)
    assert [
        (x["visit"], x["decide"]["route"], x["decide"]["to"], x["decide"]["forced"]) for x in ds
    ] == [
        (1, "retry", "still", False),
        (2, "accept", "out", True),  # taken max times: then is forced whatever the answer
    ]
    check_ = cell(two, "check")
    assert check_["loop"] == {"turns": 1, "max": 1, "then": "out", "gave_up": True}
    assert [a["route"]["label"] for a in check_["attempts"]] == ["retry", "accept"]

    assert three["status"] == "failed"
    assert three["error"].startswith("check: answer did not match the schema: "), three["error"]
    (d3,) = decisions(api, bid, 3)
    (rid,) = d3["run_ids"]
    run = ok(api.get(f"{STUDIO}/runs/{rid}"))
    assert run["status"] == "failed" and run["error"].startswith(
        "answer did not match the schema: "
    )
    assert len(d3["answers"]) == 2 and all(
        a["error"] for a in d3["answers"]
    )  # asked twice in the run

    assert four_["status"] == "succeeded", four_
    (d4,) = decisions(api, bid, 4)
    assert [a["error"] is None for a in d4["answers"]] == [
        False,
        True,
    ]  # retried once, in the same run
    assert len(d4["run_ids"]) == 1 and d4["decide"]["reason"] == "second try"


# ---------------------------------------------------------------- memory API


def test_memory_crud_and_isolation(api, api_b):
    p = create(api, routed("Contract memory"))
    pid = p["id"]
    item = ok(
        api.post(MEMORY, json={"pipeline_id": pid, "text": "  likes warm light  ", "key": "style"}),
        201,
    )
    assert (item["kind"], item["text"], item["key"], item["pipeline_id"]) == (
        "note",
        "likes warm light",
        "style",
        pid,
    )
    assert item["source"] == {
        "kind": "user",
        "batch_id": None,
        "row": None,
        "node": None,
        "visit": None,
    }
    assert api.post(MEMORY, json={"pipeline_id": pid, "text": "   "}).status_code == 422
    listed = ok(api.get(MEMORY, params={"pipeline_id": pid}))
    assert (listed["pipeline_id"], [i["id"] for i in listed["items"]]) == (pid, [item["id"]])
    assert listed["counts"] == {"judge_example": 0, "note": 1}
    assert ok(api.get(MEMORY, params={"pipeline_id": pid, "kind": "judge_example"}))["items"] == []
    assert api.get(MEMORY, params={"pipeline_id": pid, "kind": "bogus"}).status_code == 422
    for bad in (NO_SUCH_ID, "not-a-uuid"):
        assert api.get(MEMORY, params={"pipeline_id": bad}).status_code == 404
    assert api.post(MEMORY, json={"pipeline_id": NO_SUCH_ID, "text": "x"}).status_code == 404

    # B: A's pipeline and item are 404 everywhere, and nothing changes
    assert api_b.get(MEMORY, params={"pipeline_id": pid}).status_code == 404
    assert api_b.post(MEMORY, json={"pipeline_id": pid, "text": "mine"}).status_code == 404
    assert api_b.delete(f"{MEMORY}/{item['id']}").status_code == 404
    assert [i["id"] for i in ok(api.get(MEMORY, params={"pipeline_id": pid}))["items"]] == [
        item["id"]
    ]

    assert api.delete(f"{MEMORY}/{item['id']}").status_code == 204
    assert api.delete(f"{MEMORY}/{item['id']}").status_code == 404
    assert api.delete(f"{MEMORY}/not-a-uuid").status_code == 404
    assert ok(api.get(MEMORY, params={"pipeline_id": pid}))["items"] == []
    assert api.delete(f"{PIPES}/{pid}").status_code == 204
    assert api.get(MEMORY, params={"pipeline_id": pid}).status_code == 404


def test_memory_cap(new_user):
    """At most 500 items per pipeline (409)."""
    with make_client(headers(new_user("Mem")["access_token"])) as c:
        pid = create(c, routed("Contract memory cap"))["id"]
        for i in range(500):
            ok(c.post(MEMORY, json={"pipeline_id": pid, "text": f"note {i}"}), 201)
        r = c.post(MEMORY, json={"pipeline_id": pid, "text": "one too many"})
        assert (
            r.status_code == 409
            and r.json()["detail"] == "memory is full (500 items); delete some first"
        )
        assert c.delete(f"{PIPES}/{pid}").status_code == 204


# ---------------------------------------------------------------- API 1.28: Then: Ask me, Test on takes


def test_ask_me_parks_at_the_cap_and_a_reject_fails_the_row(api):
    """A judge branch without `then` (the board's "Then: Ask me"): Auto too parks at the cap with nothing suggested,
    and a reject there fails the row; a gate's branch still needs then (edge_invalid)."""
    doc_ = rewrite_loop("Contract ask me")
    doc_["edges"][2] = ["judge", {"pass": "out", "fail": "rewrite", "max": 1}]
    p = create(api, doc_)
    assert p["problems"] == [] and p["edges"][2][1] == {"pass": "out", "fail": "rewrite", "max": 1}
    tag = fresh(["v"])[0]
    p2 = scripted(f"a fox, second try {tag}", answer(FAILS))
    p1 = scripted(f"a fox {tag}", answer(FAILS, reasons={1: scripted("no subject", {"text": p2})}))
    bid = submit(api, p["id"], [p1])["id"]
    try:
        (r,) = parked(api, bid, [1], visit=2)
        assert r["parked"]["kind"] == "judge" and r["parked"].get("suggested", []) == []
        assert cell(r, "judge")["loop"] == {"turns": 1, "max": 1, "then": None, "gave_up": False}
        review = ok(api.get(f"{BATCHES}/{bid}/review"))["items"]
        assert [(i["row"], i["on_reject"]) for i in review] == [(1, "fail")]
        body = {"gate": "judge", "reject": True}
        ok(api.post(f"{BATCHES}/{bid}/rows/1/pick", json=body), 202)
        r = ended(api, bid, 1)
        assert (r["status"], r["error"]) == ("failed", "rejected at judge")
    finally:
        finish(api, bid)
    gate = {
        "title": "Contract gate ask me",
        "nodes": {
            "inputs": inputs_node("prompt"),
            "still": echo_step({"prompt.text": "$prompt"}),
            "g": {"kind": "gate", "takes": "still"},
            "out": {"kind": "output", "from": "g.picked", "collection": "x"},
        },
        "edges": [
            ["inputs", "still"],
            ["still", "g"],
            ["g", {"pass": "out", "fail": "still", "max": 1}],
        ],
    }
    v = ok(api.post(f"{PIPES}/validate", json=gate))
    assert {"code": "edge_invalid", "node": "g"}.items() <= next(
        x for x in v["problems"] if x["code"] == "edge_invalid"
    ).items()


def four_takes(client, sid: str, run_id: str) -> list[str]:
    """The four takes of a run of `four`, oldest first."""
    assert wait_run(client, run_id)["status"] == "succeeded"
    takes = [
        t for t in all_takes(client, f"{STUDIO}/sessions/{sid}/takes") if t["run_id"] == run_id
    ]
    assert len(takes) == 4, takes
    return [t["id"] for t in reversed(takes)]


def judge_test(client, pid: str, take_ids: list[str], node: str = "judge"):
    return client.post(f"{PIPES}/{pid}/judge-tests", json={"node": node, "take_ids": take_ids})


def test_judge_test_on_picked_takes(api, api_b, four):
    """Test on takes (POST /pipelines/{id}/judge-tests, GET /judge-tests/{id}): the saved judge's one ask run on
    the caller's takes (the first take's prompt is the brief, so the fake's answer is scripted there), each take's
    verdict and reason as check.judge_verdict derives them; isolation and the refusals."""
    p = create(api, judged_clip(four, "Contract judge test"))
    script = answer(FAILS, PASS, NICE_FAILS, FAILS, reasons={1: "no fox here"})
    sid = ok(api.post(f"{STUDIO}/sessions", json={"title": "Contract judge test"}), 201)["id"]
    run = ok(api.post(f"{STUDIO}/sessions/{sid}/runs", json={"workflow_id": four,
              "inputs": {"prompt.text": scripted(fresh(["a fox"])[0], script)}}), 202)  # fmt: skip
    ids = four_takes(api, sid, run["id"])

    t = ok(judge_test(api, p["id"], ids), 202)
    assert (t["pipeline_id"], t["node"], t["take_ids"], t["takes"], t["verdict"]) == (
        p["id"], "judge", ids, [], None,
    )  # fmt: skip
    assert t["status"] in ("queued", "running") and t["run_id"]
    path = f"{STUDIO}/judge-tests/{t['id']}"
    done = wait_for(
        lambda: (x := ok(api.get(path)))["status"] in FINAL_RUN and x, what="judge test"
    )
    want = check.judge_verdict(script, CRITERIA, 1, ids)
    assert (done["status"], done["verdict"], done["first_failed"]) == (
        "succeeded", want["verdict"], want["first_failed"],
    )  # fmt: skip
    assert [x["take_id"] for x in done["takes"]] == ids
    assert [x["passes"] for x in done["takes"]] == want["passes"]
    assert [x["rank"] for x in done["takes"]] == want["ranks"]
    assert [x["take_id"] for x in done["takes"] if x["picked"]] == want["picked"]
    assert done["takes"][0]["reason"] == "no fox here"
    assert done["takes"][0]["checks"] == [
        {"criterion": "Shows the subject", "level": "must", "pass": False},
        {"criterion": "Sharp", "level": "nice", "pass": True},
    ]
    assert ok(api.get(path)) == done  # kept, not asked again
    ask = ok(api.get(f"{STUDIO}/runs/{done['run_id']}"))
    assert ask["workflow_id"] == ASK and ask["session_id"] == sid and ask["outputs"] == []
    assert len(ask["inputs"]["ask.media"]) == 4

    # isolation and refusals: another user's pipeline, test or take is 404; not a judge, a repeat: 422
    assert judge_test(api_b, p["id"], ids).status_code == 404
    assert api_b.get(path).status_code == 404
    assert api.get(f"{STUDIO}/judge-tests/{NO_SUCH_ID}").status_code == 404
    assert api.get(f"{STUDIO}/judge-tests/nope").status_code == 404
    assert judge_test(api, NO_SUCH_ID, ids).status_code == 404
    assert judge_test(api, p["id"], [NO_SUCH_ID]).status_code == 404
    assert judge_test(api, p["id"], ids, node="still").status_code == 422
    assert judge_test(api, p["id"], [ids[0], ids[0]]).status_code == 422
    assert judge_test(api, p["id"], []).status_code == 422
    r = api.post(f"{PIPES}/{p['id']}/judge-tests", json={"node": "judge", "take_ids": ids, "x": 1})
    assert r.status_code == 422 and r.json()["detail"] == "unknown field x"


def test_judge_test_answer_failing_the_checks_is_a_failed_test(api, four):
    p = create(api, judged_clip(four, "Contract judge test bad"))
    sid = ok(api.post(f"{STUDIO}/sessions", json={"title": "Contract judge test bad"}), 201)["id"]
    bad = answer(PASS)  # one take's answer for four takes: judge_verdict refuses it
    run = ok(api.post(f"{STUDIO}/sessions/{sid}/runs", json={"workflow_id": four,
              "inputs": {"prompt.text": scripted(fresh(["a fox"])[0], bad)}}), 202)  # fmt: skip
    t = ok(judge_test(api, p["id"], four_takes(api, sid, run["id"])), 202)
    path = f"{STUDIO}/judge-tests/{t['id']}"
    done = wait_for(
        lambda: (x := ok(api.get(path)))["status"] in FINAL_RUN and x, what="judge test"
    )
    assert done["status"] == "failed" and done["verdict"] is None and done["takes"] == []
    assert done["error"]
