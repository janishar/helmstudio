"""Image inputs (API 1.4) on the fake echo workflow: `echo.refs` (rows of {image: Image}) takes uploaded or made
takes as references; studio resolves them as the caller (422 naming the param and item), the take records them in
`made_with` and as `parents`, the reference lists its `children`, "Use in…" puts a take into the param, reuse gives
them back as recorded, a batch line can be a take id. Skipped when the live echo workflow has no image param."""

import pytest
from helpers import (
    NO_SUCH_ID,
    QUICK,
    STUDIO,
    new_session,
    ok,
    png_bytes,
    ref,
    start_run,
    take_of_run,
    upload,
    wait_run,
)
from test_batches import finish, lines, rows_of, submit, wait_batch

PARAM = "echo.refs"


@pytest.fixture(scope="module", autouse=True)
def echo_refs(api) -> dict:
    """The echo/echo `refs` param schema; skips the module when the fake runner does not publish it."""
    schema = ok(api.get(f"{STUDIO}/workflows/echo"))["node_schemas"]["echo/echo"]
    refs = {p["name"]: p for p in schema["params"]}.get("refs")
    if not refs or refs["type"] != "rows" or refs.get("item") != {"image": "Image"}:
        pytest.skip("the echo workflow has no image rows param (fake runner before API 1.4)")
    return refs


@pytest.fixture(scope="module")
def sid(api) -> str:
    return new_session(api, "Contract references")["id"]


@pytest.fixture(scope="module")
def two(api, anon) -> list[dict]:
    """Two of A's uploaded PNGs."""
    return [
        upload(api, anon, png_bytes(40, 30), "ref one.png"),
        upload(api, anon, png_bytes(30, 40)),
    ]


def run_take(client, sid: str, inputs: dict) -> dict:
    """A quick echo run with these inputs; its take."""
    run = start_run(client, sid, QUICK | inputs)
    assert wait_run(client, run["id"])["status"] == "succeeded"
    return take_of_run(client, run["id"])


def refused(client, sid: str, inputs: dict) -> str:
    """The 422 detail of a run request that must be refused."""
    r = client.post(
        f"{STUDIO}/sessions/{sid}/runs", json={"workflow_id": "echo", "inputs": QUICK | inputs}
    )
    assert r.status_code == 422, f"{inputs}: {r.status_code} {r.text}"
    return r.json()["detail"]


@pytest.fixture(scope="module")
def child(api, sid, two) -> dict:
    """A's take made from [one, two, one]: the duplicate is recorded but is one parent."""
    a, b = (t["id"] for t in two)
    return run_take(api, sid, {PARAM: [ref(a), ref(b), ref(a)]})


def test_schema(echo_refs):
    assert (echo_refs["default"], echo_refs["max"], echo_refs["mention"]) == ([], 10, "<image{n}>")


def test_take_records_its_references(api, two, child):
    a, b = (t["id"] for t in two)
    assert child["made_with"]["inputs"][PARAM] == [ref(a), ref(b), ref(a)]
    assert child["parents"] == [a, b]  # input order, each once
    used_in = {c["id"]: c for c in ok(api.get(f"{STUDIO}/takes/{a}"))["children"]}
    assert set(used_in[child["id"]]) == {"id", "kind", "title", "thumb_url"}
    assert (used_in[child["id"]]["kind"], used_in[child["id"]]["title"]) == (
        "image",
        child["title"],
    )
    assert ok(api.get(f"{STUDIO}/takes/{child['id']}"))["children"] == []
    # a run's output can be a reference too
    grandchild = run_take(api, child["session_id"], {PARAM: [ref(child["id"])]})
    assert grandchild["parents"] == [child["id"]]


def test_reuse_returns_the_references(api, two, child):
    reuse = ok(api.post(f"{STUDIO}/takes/{child['id']}/reuse"))
    assert reuse["inputs"][PARAM] == child["made_with"]["inputs"][PARAM]


def test_bad_references_are_422(api, api_b, anon, sid, two):
    a = two[0]["id"]
    gone = upload(api, anon, png_bytes(8, 8))["id"]
    assert api.delete(f"{STUDIO}/takes/{gone}").status_code == 204
    foreign = upload(api_b, anon, png_bytes(8, 8))["id"]
    pending = upload(api, anon, png_bytes(8, 8), complete=False)["take_id"]
    for bad in (gone, foreign, pending, NO_SUCH_ID):
        detail = refused(api, sid, {PARAM: [ref(a), ref(bad)]})
        assert f"{PARAM}[2].image: take {bad} not found" in detail, detail
    detail = refused(api, sid, {PARAM: [ref(a)] * 11})
    assert f"{PARAM}: at most 10 items" in detail, detail
    detail = refused(api, sid, {PARAM: [ref(a) | {"mask": {"take_id": a}}]})
    assert f"{PARAM}[1]: unknown field mask" in detail, detail
    for item in ({"image": a}, {"image": {"key": f"u/x/uploads/{a}.png"}}, {}, "x"):
        assert PARAM in refused(api, sid, {PARAM: [item]})


def test_b_cannot_use_a_takes(api_b, two, child):
    b_sid = new_session(api_b, "Contract B references")["id"]
    a = two[0]["id"]
    assert f"{PARAM}[1].image: take {a} not found" in refused(api_b, b_sid, {PARAM: [ref(a)]})
    for tid in (a, child["id"]):
        assert api_b.get(f"{STUDIO}/takes/{tid}").status_code == 404
        assert (
            api_b.post(f"{STUDIO}/takes/{tid}/use", json={"workflow_id": "echo"}).status_code == 404
        )


def test_use_in(api, anon, sid, two):
    a = two[0]["id"]
    want = {"workflow_id": "echo", "inputs": {PARAM: [ref(a)]}}
    assert ok(api.post(f"{STUDIO}/takes/{a}/use", json={"workflow_id": "echo"})) == want
    assert (
        ok(api.post(f"{STUDIO}/takes/{a}/use", json={"workflow_id": "echo", "param": PARAM}))
        == want
    )
    # merged over the form, it runs
    assert run_take(api, sid, want["inputs"])["parents"] == [a]

    for body in (
        {"workflow_id": "no-such-workflow"},
        {"workflow_id": "echo", "param": "echo.steps"},  # not an image input
        {"workflow_id": "echo", "param": "echo.nope"},
    ):
        r = api.post(f"{STUDIO}/takes/{a}/use", json=body)
        assert r.status_code == 422, f"{body}: {r.status_code} {r.text}"
    pending = upload(api, anon, png_bytes(8, 8), complete=False)["take_id"]
    for tid in (NO_SUCH_ID, pending):
        assert (
            api.post(f"{STUDIO}/takes/{tid}/use", json={"workflow_id": "echo"}).status_code == 404
        )


def test_use_in_a_workflow_without_image_input(api, two):
    for w in ok(api.get(f"{STUDIO}/workflows"))["workflows"]:
        schemas = ok(api.get(f"{STUDIO}/workflows/{w['id']}"))["node_schemas"]
        params = [p for s in schemas.values() for p in s["params"]]
        if not any(
            p["type"] == "image" or "Image" in (p.get("item") or {}).values() for p in params
        ):
            r = api.post(f"{STUDIO}/takes/{two[0]['id']}/use", json={"workflow_id": w["id"]})
            assert r.status_code == 422 and "takes no image input" in r.json()["detail"], r.text
            return
    pytest.skip("every live workflow has an image input")


def test_deleted_reference(api, anon, sid, two):
    a = two[0]["id"]
    doomed = upload(api, anon, png_bytes(16, 16))["id"]
    take = run_take(api, sid, {PARAM: [ref(doomed), ref(a)]})
    assert take["parents"] == [doomed, a]
    assert api.delete(f"{STUDIO}/takes/{doomed}").status_code == 204

    kept = ok(api.get(f"{STUDIO}/takes/{take['id']}"))
    assert kept["parents"] == [a]  # the deleted parent is left out
    assert kept["made_with"]["inputs"][PARAM] == [ref(doomed), ref(a)]  # the snapshot stays
    reuse = ok(api.post(f"{STUDIO}/takes/{take['id']}/reuse"))
    assert reuse["inputs"][PARAM] == [ref(doomed), ref(a)]  # as recorded ...
    detail = refused(api, sid, reuse["inputs"])  # ... so posting it again names the gone one
    assert f"{PARAM}[1].image: take {doomed} not found" in detail, detail


def test_batch_lines_of_take_ids(api, two):
    ids = [t["id"] for t in two]
    batch = submit(api, lines(ids, PARAM))
    try:
        assert wait_batch(api, batch["id"])["status"] == "done"
        rows = rows_of(api, batch["id"])
        assert [r["inputs"][PARAM] for r in rows] == [[ref(t)] for t in ids]
        for row, tid in zip(rows, ids, strict=True):
            (out,) = row["take_ids"]
            assert ok(api.get(f"{STUDIO}/takes/{out}"))["parents"] == [tid]
    finally:
        finish(api, batch["id"])


def test_batch_table_of_references(api, two):
    a, b = (t["id"] for t in two)
    table = {"columns": ["prompt.text", PARAM], "rows": [["a fox", [ref(a), ref(b)]]]}
    batch = submit(api, {"table": table})
    try:
        assert wait_batch(api, batch["id"])["status"] == "done"
        (row,) = rows_of(api, batch["id"])
        (out,) = row["take_ids"]
        assert ok(api.get(f"{STUDIO}/takes/{out}"))["parents"] == [a, b]
    finally:
        finish(api, batch["id"])
