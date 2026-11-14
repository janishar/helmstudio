"""The dispatcher on the one fake host: round-robin between users, queue positions, cancel before release,
the queue view, and no live host."""

import pytest
from conftest import headers, make_client
from helpers import (
    QUICK,
    SLOW,
    STUDIO,
    new_session,
    ok,
    run_events,
    start_run,
    statuses,
    wait_for,
    wait_run,
)

# the first run holds the host long enough to line up the others behind it
HOLD = {"prompt.text": "hold the host", "echo.steps": 10, "echo.delay_ms": 300}
LINE = {"prompt.text": "in line", "echo.steps": 3, "echo.delay_ms": 200}


@pytest.fixture
def fresh_pair(new_user):
    """Two new users who never had a run released (the round-robin order depends on it)."""
    with (
        make_client(headers(new_user("Y")["access_token"])) as y,
        make_client(headers(new_user("Z")["access_token"])) as z,
    ):
        yield y, z


def run_of(client, rid: str) -> dict:
    return ok(client.get(f"{STUDIO}/runs/{rid}"))


def running(client, rid: str) -> dict:
    return wait_for(
        lambda: (r := run_of(client, rid))["status"] != "queued" and r, what=f"{rid} released"
    )


def test_round_robin_between_users(api, fresh_pair):
    users = {"x": api, "y": fresh_pair[0], "z": fresh_pair[1]}
    sids = {u: new_session(c, "Contract queue")["id"] for u, c in users.items()}
    first = start_run(api, sids["x"], HOLD)
    assert running(api, first["id"])["status"] == "running"
    # x queues two more, then y three, then z three: the host is busy with x's first run
    order = [("x", first)]
    for u, n in (("x", 2), ("y", 3), ("z", 3)):
        order += [(u, start_run(users[u], sids[u], LINE)) for _ in range(n)]
    for _, r in order:
        assert (r["lane"], r["attempt"]) == ("interactive", 1)

    # y and z never had a run released, x just had one; y's oldest run is older than z's
    expected = ["x", "y", "z", "x", "y", "z", "x", "y", "z"]
    queued = [(u, run_of(users[u], r["id"])) for u, r in order[1:]]
    if all(r["status"] == "queued" for _, r in queued):  # x's first run still holds the host
        by_position = sorted(queued, key=lambda ur: ur[1]["position"])
        assert [u for u, _ in by_position] == expected[1:]
        assert [r["position"] for _, r in by_position] == list(range(1, 9))
        assert all(r["host"] == "fake" for _, r in queued)

    # the last in line moves up as the others finish
    last_user, last = order[-1]
    seen = []

    def moved_on():
        r = run_of(users[last_user], last["id"])
        if r["status"] != "queued":
            return True
        if not seen or seen[-1] != r["position"]:
            seen.append(r["position"])

    wait_for(moved_on, interval=0.1, what="the last run to leave the queue")
    assert len(seen) >= 2 and seen == sorted(seen, reverse=True), seen

    done = [(u, wait_run(users[u], r["id"])) for u, r in order]
    assert all(r["status"] == "succeeded" and "position" not in r for _, r in done), done
    by_start = sorted(done, key=lambda ur: ur[1]["started_at"])
    assert [u for u, _ in by_start] == expected


def test_queue_view_hides_others(api, api_b):
    sid_a, sid_b = new_session(api)["id"], new_session(api_b)["id"]
    hold = start_run(api, sid_a, SLOW)
    running(api, hold["id"])
    mine = [start_run(api_b, sid_b, QUICK)["id"] for _ in range(2)]
    theirs = start_run(api, sid_a, QUICK)["id"]
    try:
        r = api_b.get(f"{STUDIO}/queue")
        q = ok(r)
        assert [x["id"] for x in q["runs"]] == mine  # only B's, queued by position
        assert [x["position"] for x in q["runs"]] == sorted(x["position"] for x in q["runs"])
        assert hold["id"] not in r.text and theirs not in r.text  # nothing of A's, not even an id
        (fake,) = [h for h in q["hosts"] if h["host"] == "fake"]
        assert set(fake) == {"host", "state", "others_queued"}
        assert fake["state"] == "busy" and fake["others_queued"] == 1  # A's queued run, as a count

        qa = ok(api.get(f"{STUDIO}/queue"))
        assert [x["id"] for x in qa["runs"]] == [hold["id"], theirs]  # running first
        (fake_a,) = [h for h in qa["hosts"] if h["host"] == "fake"]
        assert fake_a["others_queued"] == 2
    finally:
        for client, rid in ((api, hold["id"]), (api, theirs), *((api_b, m) for m in mine)):
            client.post(f"{STUDIO}/runs/{rid}/cancel")
    for client, rid in ((api, hold["id"]), (api, theirs), *((api_b, m) for m in mine)):
        wait_run(client, rid)


def test_cancel_before_release(api):
    sid = new_session(api)["id"]
    hold = start_run(api, sid, SLOW)
    running(api, hold["id"])
    waiting = start_run(api, sid, QUICK)
    assert run_of(api, waiting["id"])["status"] == "queued"
    cancelled = ok(api.post(f"{STUDIO}/runs/{waiting['id']}/cancel"), 202)
    assert cancelled["status"] == "cancelled" and "position" not in cancelled  # at once
    assert api.post(f"{STUDIO}/runs/{waiting['id']}/cancel").status_code == 409
    ok(api.post(f"{STUDIO}/runs/{hold['id']}/cancel"), 202)
    assert wait_run(api, hold["id"])["status"] == "cancelled"
    done = run_of(api, waiting["id"])
    assert (done["status"], done["started_at"], done["outputs"]) == ("cancelled", None, [])
    events = run_events(api, sid, waiting["id"])
    assert statuses(events) == ["queued", "cancelled"]  # it never ran
    assert all(e["type"] == "status" for e in events)


def test_no_live_host_fails_at_once(api):
    """Needs a workflow in the registry whose env has no live host (e.g. qwen-image after its runner left)."""
    live = {h["env"] for h in ok(api.get(f"{STUDIO}/hosts"))["hosts"] if h["state"] != "offline"}
    dead = [w for w in ok(api.get(f"{STUDIO}/workflows"))["workflows"] if w["env"] not in live]
    if not dead:
        pytest.skip("every published workflow has a live host on this stack")
    w = dead[0]
    sid = new_session(api)["id"]
    run = ok(api.post(f"{STUDIO}/sessions/{sid}/runs", json={"workflow_id": w["id"]}), 202)
    assert (run["status"], run["error"], run["host"]) == (
        "failed",
        f"no live host for env {w['env']}",
        None,
    )
    assert "position" not in run
