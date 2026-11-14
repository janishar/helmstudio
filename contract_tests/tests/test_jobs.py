"""The dev Jobs view: the caller's processing jobs and runs, newest first, and one job's steps, logs, images and
model usage; another user sees none of them."""

from helpers import CATALOG, new_id, ok, run, started


def test_jobs_list_and_detail(api, api_b):
    d = started(api, names=["ring-01.png"], guideline=False)
    sid = d["session"]["id"]
    d = run(api, sid, scope="sample", category="rings")
    run_id = d["runs"][0]["id"]
    mine = [j for j in ok(api.get(f"{CATALOG}/jobs"))["jobs"] if j["session_id"] == sid]
    assert [j["kind"] for j in mine] == ["run", "process"]  # newest first
    assert mine[0]["id"] == run_id and mine[0]["scope"] == "sample" and mine[0]["status"] == "succeeded"
    assert mine[1]["scope"] is None and mine[1]["status"] == "succeeded"

    process = ok(api.get(f"{CATALOG}/jobs/{mine[1]['id']}"))
    assert process["job"] == mine[1]
    steps = [e["data"]["step"] for e in process["events"] if e["type"] == "step" and e["data"]["state"] == "done"]
    assert steps[:2] == ["ingest", "sort"]
    assert all(e["work_id"] == mine[1]["id"] for e in process["events"])

    job = ok(api.get(f"{CATALOG}/jobs/{run_id}"))
    # one entry per image: a try-in-progress event is served with the image's current state, like its final event
    images = list({e["data"]["attempts"][0]["url"]: e["data"] for e in job["events"]
                   if e["type"] == "image" and e["data"] and e["data"]["attempts"]}.values())
    assert images and all(i["run_id"] == run_id and i["attempts"] for i in images)  # each image with its tries
    u = job["usage"]  # fake mode: one image call per try on the fake endpoint, no LLM call
    calls = [e["data"] for e in job["events"] if e["type"] == "usage"]
    assert u["image_calls"] == len(calls) == sum(len(i["attempts"]) for i in images) and u["llm_calls"] == 0
    assert [(m["kind"], m["model"], m["calls"]) for m in u["models"]] == [("image", "/fake-a", len(calls))]
    assert u["credits"] == sum(c["credits"] for c in calls) > 0 and u["cost_usd"] is None
    assert process["usage"] == {"models": [], "llm_calls": 0, "image_calls": 0, "input_tokens": 0, "output_tokens": 0,
                                "cost_usd": None, "credits": None}

    assert sid not in {j["session_id"] for j in ok(api_b.get(f"{CATALOG}/jobs"))["jobs"]}
    for job_id in (run_id, mine[1]["id"], new_id(), "nope"):
        assert api_b.get(f"{CATALOG}/jobs/{job_id}").status_code == 404
