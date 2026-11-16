"""Models and model jobs (API 1.16, contracts/README.md "Models and model jobs") on the echo pack's tiny fake model.

The fake runner (host fake, env fake) publishes model `echo` with variants tiny, plus (shares tiny's vae) and gated
(shares it too, needs the host's token); their sources are repos of the fake hub (contract_tests/fake_hub, service
fake-hub of deploy/compose/fake.yml, HF_ENDPOINT), downloaded into the runner's scratch models root (volume
fake-models at /models), never the internet and never ~/GenAI/models. The module starts by deleting whatever an
earlier run left, so the host starts with all three missing. The fake hub, the token file and the runner's disk are
reached with docker exec (no host port; the module skips when that fails). Tests run in file order and build on each
other: download tiny, cancel and resume plus, the gated one, then the deletes."""

import hashlib
import re

import pytest
from helpers import (
    STUDIO,
    TERMINAL,
    disk_hashes,
    fake_hub,
    new_session,
    ok,
    start_run,
    wait_for,
    wait_run,
)

MODEL, TITLE, HOST, ENV = "echo", "Echo (fake)", "fake", "fake"
LICENCE = "echo-test"
DIRS = {"tiny": "Echo-Tiny", "plus": "Echo-Tiny-Plus", "gated": "Echo-Tiny-Gated"}
REPOS = {v: f"helm-test/{d.lower()}" for v, d in DIRS.items()}
OWN = {"tiny": ("config.json", "transformer/", "vae/"), "plus": ("config.json", "transformer/"),
       "gated": ("config.json", "transformer/")}  # fmt: skip
ADMIN = f"{STUDIO}/admin"
DOWNLOAD_TIMEOUT = 180  # plus is 24 MiB at the fake hub's 1 MiB/s
SHARD = 12 * 1024 * 1024  # one of plus's two transformer shards
TINY_RUN = {
    "prompt.text": "a tiny fox"
}  # workflow echo-tiny: loads transformer and vae of model echo


# ---------------------------------------------------------------- helpers (test_hosts_admin uses them too)


def echo_model(client) -> dict:
    (m,) = [m for m in ok(client.get(f"{STUDIO}/models"))["models"] if m["id"] == MODEL]
    return m


def state(client, variant: str, host: str = HOST) -> dict:
    """The variant's ModelVariantHost on `host`."""
    (v,) = [v for v in echo_model(client)["variants"] if v["id"] == variant]
    (h,) = [h for h in v["hosts"] if h["host"] == host]
    return h


def job(admin, job_id: str) -> dict:
    return ok(admin.get(f"{ADMIN}/model-jobs/{job_id}"))


def wait_job(admin, job_id: str, timeout: float = DOWNLOAD_TIMEOUT) -> dict:
    return wait_for(
        lambda: (j := job(admin, job_id))["state"] in TERMINAL and j,
        timeout=timeout,
        interval=0.5,
        what=f"model job {job_id}",
    )


def wait_state(admin, variant: str, want: str, timeout: float = 90) -> dict:
    """The rescan after a job (or every 60 s) publishes the new presence."""
    return wait_for(
        lambda: (h := state(admin, variant))["state"] == want and h,
        timeout=timeout,
        interval=0.5,
        what=f"{variant} {want} on {HOST}",
    )


def download(admin, variant: str) -> dict:
    """POST download with the licence accepted: the job as answered (202 new, 200 the unfinished one)."""
    r = admin.post(
        f"{ADMIN}/models/{MODEL}/download",
        json={"host": HOST, "variant": variant, "accept_licence": True},
    )
    assert r.status_code in (200, 202), r.text
    return r.json()


def ensure_present(admin, variant: str) -> None:
    if state(admin, variant)["state"] != "present":
        j = wait_job(admin, download(admin, variant)["id"])
        assert j["state"] == "succeeded", j
        wait_state(admin, variant, "present")


def residents(client, host: str = HOST, env: str = ENV) -> list[dict]:
    """The echo model's resident entries of host.env."""
    hosts = ok(client.get(f"{STUDIO}/hosts"))["hosts"]
    (h,) = [h for h in hosts if (h["host"], h["env"]) == (host, env)]
    return [r for r in h.get("resident", []) if r["model"] == MODEL]


def unload_all(admin) -> None:
    ok(admin.post(f"{ADMIN}/hosts/{HOST}/unload", json={"all": True}), 202)
    wait_for(lambda: not residents(admin), timeout=60, what="nothing of echo resident on fake")


def tiny_run(client, **params) -> dict:
    """A finished echo-tiny run (variant tiny)."""
    sid = new_session(client, "Contract tiny")["id"]
    run = wait_run(client, start_run(client, sid, TINY_RUN | params, "echo-tiny")["id"])
    assert run["status"] == "succeeded", run
    return run


def manifest() -> dict:
    return fake_hub("GET", "/__manifest")


def own_files(variant: str) -> dict:
    """path → sha256 of the variant's own files on the hub."""
    files = manifest()[REPOS[variant]]
    return {p: m["sha256"] for p, m in files.items() if p.startswith(OWN[variant])}


def licence_sha(client) -> str:
    (lic,) = [x for x in ok(client.get(f"{STUDIO}/models"))["licences"] if x["id"] == LICENCE]
    return hashlib.sha256(lic["text"].encode()).hexdigest()


@pytest.fixture(scope="module")
def fresh(admin):
    """Every echo variant missing on fake, the token file present, the hub's request log empty."""
    try:
        fake_hub("POST", "/__token", {"present": True})
        fake_hub("POST", "/__reset", {})
    except (OSError, RuntimeError) as e:
        pytest.skip(f"the fake hub is not reachable with docker exec: {e}")
    for j in ok(admin.get(f"{ADMIN}/model-jobs", params={"host": HOST, "active": True}))["jobs"]:
        if j["model"] == MODEL and j["kind"] == "download":
            admin.post(f"{ADMIN}/model-jobs/{j['id']}/cancel")
            wait_job(admin, j["id"], 60)
    if any(state(admin, v)["state"] != "missing" for v in DIRS):
        unload_all(admin)
        r = admin.delete(f"{ADMIN}/models/{MODEL}", params={"host": HOST})
        assert r.status_code in (200, 202), r.text
        assert wait_job(admin, r.json()["id"], 60)["state"] == "succeeded"
    for v in DIRS:
        wait_state(admin, v, "missing")


# ---------------------------------------------------------------- for everyone


def test_models_list_for_a_user(api):
    body = ok(api.get(f"{STUDIO}/models"))
    (fake,) = [h for h in body["hosts"] if h["host"] == HOST]
    assert fake["live"] and ENV in fake["envs"], fake
    assert (
        fake["models_root"] == "/models" and fake["free_bytes"] > 0 and fake["total_bytes"] > 0
    ), fake
    assert "hub_token" not in fake  # admins only
    (lic,) = [x for x in body["licences"] if x["id"] == LICENCE]
    assert lic["title"] == "Echo Test Licence" and lic["text"].startswith("# Echo Test Licence"), (
        lic
    )
    assert len([x for x in body["licences"] if x["id"] == LICENCE]) == 1
    m = echo_model(api)
    assert (m["family"], m["title"], m["media"]) == ("echo", TITLE, ["image"]), m
    assert m["runtimes"] == [{"runtime": "fake", "env": "fake"}], m
    variants = {v["id"]: v for v in m["variants"]}
    assert list(variants) == ["tiny", "plus", "gated"], list(variants)
    for vid, v in variants.items():
        assert (v["dir"], v["precisions"]) == (DIRS[vid], ["bf16"]), v
        assert v["licence"] == {"id": LICENCE, "must_accept": True}, v
        assert v["gated"] == (vid == "gated"), v
        assert v["download_bytes"] == {"tiny": 98502, "plus": 25166027, "gated": 16500}[vid], v
        comps = {c["component"]: c["from_variant"] for c in v["components"]}
        assert comps == {"transformer": vid, "vae": "tiny"}, v  # plus and gated share tiny's vae
        (h,) = [h for h in v["hosts"] if h["host"] == HOST]
        assert h["state"] in ("present", "partial", "missing", "downloading", "mismatch"), h


def test_admin_endpoints_refuse_a_user(api):
    pin = {"env": ENV, "component": "vae", "model": MODEL, "variant": "tiny", "precision": "bf16"}
    calls = [
        ("POST", f"{ADMIN}/models/{MODEL}/download", {"host": HOST, "variant": "tiny", "accept_licence": True}),
        ("DELETE", f"{ADMIN}/models/{MODEL}?host={HOST}", None),
        ("DELETE", f"{ADMIN}/loras/{'0' * 64}?host={HOST}", None),
        ("GET", f"{ADMIN}/model-jobs", None),
        ("GET", f"{ADMIN}/model-jobs/00000000-0000-4000-8000-000000000000", None),
        ("POST", f"{ADMIN}/model-jobs/00000000-0000-4000-8000-000000000000/cancel", None),
        ("POST", f"{ADMIN}/hosts/{HOST}/unload", {"all": True}),
        ("PUT", f"{ADMIN}/hosts/{HOST}/settings", {"after_run": "unload", "idle_unload_min": 0, "reserve_gb": 1}),
        ("PUT", f"{ADMIN}/hosts/{HOST}/pins", {"pins": [pin]}),
    ]  # fmt: skip
    for method, path, body in calls:
        r = api.request(method, path, json=body)
        assert r.status_code == 403 and r.json()["detail"] == "admin role required", (
            f"{method} {path}: {r.text}"
        )
    for v in echo_model(api)["variants"]:  # a user sees no job ids beyond the state
        assert all(h["job"] is None or set(h["job"]) == {"id", "state", "done_bytes", "total_bytes"}
                   for h in v["hosts"])  # fmt: skip


def test_hub_token_is_shown_to_admins_only(admin, fresh):
    (fake,) = [h for h in ok(admin.get(f"{STUDIO}/models"))["hosts"] if h["host"] == HOST]
    assert fake["hub_token"] is True, fake  # the fake overlay's placeholder file


# ---------------------------------------------------------------- downloads


def test_fresh_host_has_the_model_missing(admin, fresh):
    for v, folder in DIRS.items():
        h = state(admin, v)
        assert (h["state"], h["bytes"], h["job"]) == ("missing", None, None), h
        assert disk_hashes(folder) == {}


def test_download_refusals(admin, fresh):
    def detail(model, body, status=422):
        r = admin.post(f"{ADMIN}/models/{model}/download", json=body)
        assert r.status_code == status, r.text
        return r.json()["detail"]

    assert detail(MODEL, {"host": HOST, "variant": "tiny"}) == f"licence {LICENCE} must be accepted"
    assert detail(MODEL, {"host": HOST, "variant": "tiny", "accept_licence": False}) == (
        f"licence {LICENCE} must be accepted"
    )
    assert detail(MODEL, {"host": HOST, "variant": "nope", "accept_licence": True}) == (
        f"{MODEL} has no variant nope"
    )
    assert detail(MODEL, {"host": "nohost", "variant": "tiny", "accept_licence": True}) == (
        f"host nohost has no env for {MODEL}"
    )
    assert detail("no-such-model", {"host": HOST, "accept_licence": True}, 404)
    assert (
        admin.post(f"{ADMIN}/models/{MODEL}/download", json={"variant": "tiny"}).status_code == 422
    )
    assert state(admin, "tiny")["state"] == "missing"  # nothing was queued


def test_download_tiny(admin, fresh):
    fake_hub("POST", "/__reset", {})
    j = ok(admin.post(f"{ADMIN}/models/{MODEL}/download",
                      json={"host": HOST, "variant": "tiny", "accept_licence": True}), 202)  # fmt: skip
    assert (j["kind"], j["host"], j["env"], j["model"], j["variant"]) == (
        "download",
        HOST,
        ENV,
        MODEL,
        "tiny",
    ), j
    assert j["state"] == "queued" and j["title"] == f"{TITLE} · Tiny" and j["keeps"] == [], j
    assert j["licence"] == {"id": LICENCE, "sha256": licence_sha(admin)}, j
    done = wait_job(admin, j["id"])
    assert done["state"] == "succeeded" and done["failure"] is None, done
    files = own_files("tiny")
    total = sum(m["size"] for p, m in manifest()[REPOS["tiny"]].items() if p in files)
    p = done["progress"]
    assert (p["done_bytes"], p["total_bytes"], p["files_done"], p["files_total"]) == (
        total,
        total,
        3,
        3,
    ), p
    assert done["result"]["files"] == 3 and done["started_at"] and done["ended_at"], done
    h = wait_state(admin, "tiny", "present")
    assert h["bytes"] == total and h["job"] is None, h
    assert disk_hashes(DIRS["tiny"]) == files  # plain files, the hub's SHA-256 each
    assert {r["path"] for r in fake_hub("GET", "/__requests")} == set(files)
    # a second download of a present variant changes nothing
    r = admin.post(
        f"{ADMIN}/models/{MODEL}/download",
        json={"host": HOST, "variant": "tiny", "accept_licence": True},
    )
    assert r.status_code == 409, r.text
    assert re.fullmatch(rf"{re.escape(TITLE)} [Tt]iny is already on {HOST}", r.json()["detail"]), (
        r.text
    )
    assert len(fake_hub("GET", "/__requests")) == 3
    jobs = ok(admin.get(f"{ADMIN}/model-jobs", params={"host": HOST}))["jobs"]
    assert jobs[0]["id"] == j["id"], jobs[:2]  # newest first
    assert all(x["state"] in ("queued", "running")
               for x in ok(admin.get(f"{ADMIN}/model-jobs", params={"active": True}))["jobs"])  # fmt: skip


def test_cancel_then_resume(admin, api, fresh):
    """plus (2 shards of 12 MiB at 1 MiB/s): progress while it runs, a cancel after the first shard, then a new
    download that fetches no finished file again and reuses tiny's vae (the same hashes)."""
    ensure_present(admin, "tiny")
    fake_hub("POST", "/__reset", {})
    j = download(admin, "plus")
    assert j["state"] == "queued", j
    again = download(admin, "plus")  # idempotent while unfinished
    assert again["id"] == j["id"], again
    seen = set()

    def first_shard_done():
        p = job(admin, j["id"])["progress"]
        if p:
            seen.add(p["done_bytes"])
        return p and p["done_bytes"] > SHARD and p["done_bytes"] < p["total_bytes"]

    wait_for(first_shard_done, timeout=60, interval=0.5, what="plus past its first shard")
    h = state(api, "plus")  # a user sees it downloading with the job's numbers
    assert h["state"] == "downloading" and h["job"]["id"] == j["id"], h
    assert (
        h["job"]["state"] == "running" and 0 < h["job"]["done_bytes"] < h["job"]["total_bytes"]
    ), h
    ok(admin.post(f"{ADMIN}/model-jobs/{j['id']}/cancel"))
    cancelled = wait_job(admin, j["id"], 30)
    assert cancelled["state"] == "cancelled", cancelled
    assert len(seen) >= 2, seen  # progress arrived while it ran
    r = admin.post(f"{ADMIN}/model-jobs/{j['id']}/cancel")
    assert r.status_code == 409 and r.json()["detail"] == "job already ended", r.text
    assert wait_state(admin, "plus", "partial")["job"] is None  # partial files are kept
    finished = {r["path"] for r in fake_hub("GET", "/__requests") if r["done"]}
    assert finished, "no file of plus finished before the cancel"

    j2 = ok(admin.post(f"{ADMIN}/models/{MODEL}/download",
                       json={"host": HOST, "variant": "plus", "accept_licence": True}), 202)  # fmt: skip
    done = wait_job(admin, j2["id"])
    assert done["state"] == "succeeded", done
    assert "vae" in done["result"].get("reused", []), done["result"]  # tiny's copy, same hashes
    gets = fake_hub("GET", "/__requests")
    for path in finished:  # nothing that had landed is fetched again
        assert [g["done"] for g in gets if g["path"] == path] == [True], (path, gets)
    assert not any(g["path"].startswith("vae/") for g in gets), gets
    wait_state(admin, "plus", "present")
    assert disk_hashes(DIRS["plus"]) == own_files("plus")  # no vae copy in plus's folder


def test_gated_variant_needs_the_token(admin, fresh):
    ensure_present(admin, "tiny")
    fake_hub("POST", "/__token", {"present": False})
    try:
        failed = wait_job(admin, download(admin, "gated")["id"], 60)
    finally:
        fake_hub("POST", "/__token", {"present": True})
    assert failed["state"] == "failed", failed
    assert failed["failure"]["class"] == "gated", failed
    assert failed["failure"]["detail"] == (
        f"repo {REPOS['gated']} is gated: put a Hub token in this host's secrets file (RUNNER_HF_TOKEN_FILE)"
    ), failed
    assert state(admin, "gated")["state"] in ("missing", "partial")
    fake_hub("POST", "/__reset", {})
    done = wait_job(admin, download(admin, "gated")["id"])
    assert done["state"] == "succeeded", done
    assert all(r["auth"] for r in fake_hub("GET", "/__requests"))  # the token went to the hub
    wait_state(admin, "gated", "present")
    assert disk_hashes(DIRS["gated"]) == own_files("gated")


# ---------------------------------------------------------------- deletes


def test_delete_refused_while_resident_or_in_use(admin, api, fresh):
    for v in DIRS:
        ensure_present(admin, v)
    ok(admin.put(f"{ADMIN}/hosts/{HOST}/settings",
                 json={"after_run": "keep_warm", "idle_unload_min": 0, "reserve_gb": 1}))  # fmt: skip
    wait_for(lambda: settings_applied(admin), timeout=60, what="settings applied on fake")
    tiny_run(api)
    assert {r["component"] for r in residents(admin)} == {"transformer", "vae"}
    r = admin.delete(f"{ADMIN}/models/{MODEL}", params={"host": HOST, "variant": "tiny"})
    assert r.status_code == 409, r.text
    assert re.fullmatch(rf"(transformer|vae) of {re.escape(TITLE)} is loaded on {HOST}; unload it first",
                        r.json()["detail"]), r.text  # fmt: skip
    # plus's own files are not loaded, but a run of the model is not over
    sid = new_session(api, "Contract tiny slow")["id"]
    run = start_run(api, sid, TINY_RUN | {"tiny.steps": 200, "tiny.delay_ms": 100}, "echo-tiny")
    try:
        r = admin.delete(f"{ADMIN}/models/{MODEL}", params={"host": HOST, "variant": "plus"})
        assert r.status_code == 409, r.text
        assert r.json()["detail"] == f"{TITLE} is in use by a queued or running run on {HOST}", (
            r.text
        )
    finally:
        api.post(f"{STUDIO}/runs/{run['id']}/cancel")
        wait_run(api, run["id"])
    assert state(admin, "plus")["state"] == "present"


def test_delete_keeps_a_shared_component_then_removes_all(admin, fresh):
    for v in DIRS:
        ensure_present(admin, v)
    unload_all(admin)
    j = ok(admin.delete(f"{ADMIN}/models/{MODEL}", params={"host": HOST, "variant": "tiny"}), 202)
    assert (j["kind"], j["variant"]) == ("delete", "tiny"), j
    (keep,) = j["keeps"]  # plus and gated still load tiny's vae
    assert (keep["component"], keep["from_variant"], sorted(keep["for"])) == (
        "vae",
        "tiny",
        ["gated", "plus"],
    ), j
    done = wait_job(admin, j["id"], 60)
    assert done["state"] == "succeeded" and done["result"]["kept"] == ["vae"], done
    assert wait_state(admin, "tiny", "partial")["bytes"] > 0  # its vae stays in its folder
    assert state(admin, "plus")["state"] == state(admin, "gated")["state"] == "present"
    # the kept vae and the folder's files of no component (config.json) stay; the transformer goes
    assert set(disk_hashes(DIRS["tiny"])) == {"config.json", "vae/model.safetensors"}
    # every variant (no `variant`): nothing is kept, the model is missing again
    j = ok(admin.delete(f"{ADMIN}/models/{MODEL}", params={"host": HOST}), 202)
    assert j["variant"] is None and j["keeps"] == [], j
    done = wait_job(admin, j["id"], 60)
    assert done["state"] == "succeeded" and done["result"].get("freed_bytes", 0) > 0, done
    for v, folder in DIRS.items():
        wait_state(admin, v, "missing")
        assert disk_hashes(folder) == {}
    r = admin.delete(f"{ADMIN}/models/{MODEL}", params={"host": HOST, "variant": "tiny"})
    assert r.status_code == 409, r.text
    assert re.fullmatch(rf"{re.escape(TITLE)} [Tt]iny is not on {HOST}", r.json()["detail"]), r.text
    r = admin.delete(f"{ADMIN}/models/{MODEL}", params={"host": HOST, "variant": "nope"})
    assert r.status_code == 422 and r.json()["detail"] == f"{MODEL} has no variant nope", r.text
    assert admin.delete(f"{ADMIN}/models/{MODEL}").status_code == 422  # host is required


def test_model_job_and_lora_unknown_ids(admin):
    assert admin.get(f"{ADMIN}/model-jobs/00000000-0000-4000-8000-000000000000").status_code == 404
    assert admin.get(f"{ADMIN}/model-jobs/not-a-uuid").status_code == 404
    r = admin.post(f"{ADMIN}/model-jobs/00000000-0000-4000-8000-000000000000/cancel")
    assert r.status_code == 404, r.text
    r = admin.delete(f"{ADMIN}/loras/{'0' * 64}", params={"host": HOST})  # not in fake's index
    assert r.status_code == 404, r.text


def settings_applied(admin, host: str = HOST) -> bool:
    """Every fresh key of the host reports the stored settings' version (HostSettings.applied)."""
    hosts = [
        h
        for h in ok(admin.get(f"{STUDIO}/hosts"))["hosts"]
        if h["host"] == host and h["env"] == ENV
    ]
    a = hosts[0]["admin"] if hosts else {}
    s, policy = a.get("settings"), a.get("policy") or {}
    return bool(s and s["applied"] and policy.get("settings_version") == s["version"])


def wait_gone(client, component: str, timeout: float = 60) -> None:
    wait_for(
        lambda: all(r["component"] != component for r in residents(client)),
        timeout=timeout,
        what=f"{component} unloaded",
    )
