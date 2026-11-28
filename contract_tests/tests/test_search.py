"""Search in lists (API 1.92; contracts/README.md, Search in lists): `q` and `cursor` / `limit` on sessions, Magic
boards, sequences, chats, pipelines and a session's runs; `q` and `state` on batches; `q` on a batch's rows; the
library's `words`. Every word in any order and case; another user's items never match. No GPU."""

import uuid

import pytest
from conftest import headers, make_client
from helpers import QUICK, STUDIO, new_session, ok, start_run, wait_run
from test_batches import BATCHES, finish, lines, submit, wait_batch
from test_pipelines import PIPES, chain

LIB = f"{STUDIO}/library"


@pytest.fixture(scope="module")
def api_b(new_user):
    """Another user of this module only: the shared user B's lists stay as other tests expect them."""
    with make_client(headers(new_user("SearchB")["access_token"])) as c:
        yield c


@pytest.fixture
def tag() -> str:
    """A word no other test's items hold."""
    return f"srch{uuid.uuid4().hex[:8]}"


def names(page: dict, key: str, field: str = "title") -> list[str]:
    return [x[field] for x in page[key]]


def test_sessions_words_paging_and_isolation(api, api_b, tag):
    for title in ("Mara, rooftop", "Snow owl", "mara on the night bus", "Fox at dusk"):
        new_session(api, f"{title} {tag}")
    new_session(api_b, f"Mara rooftop {tag}")
    found = ok(api.get(f"{STUDIO}/sessions", params={"q": f"ROOFTOP {tag} mara"}))
    assert names(found, "sessions") == [f"Mara, rooftop {tag}"] and found["next_cursor"] is None
    assert names(ok(api.get(f"{STUDIO}/sessions", params={"q": f"{tag} mara"})), "sessions") == [
        f"mara on the night bus {tag}",
        f"Mara, rooftop {tag}",
    ]  # newest first
    assert names(ok(api_b.get(f"{STUDIO}/sessions", params={"q": tag})), "sessions") == [
        f"Mara rooftop {tag}"
    ]
    # pages of 3, joined by the cursor: the same four as one page
    whole = names(ok(api.get(f"{STUDIO}/sessions", params={"q": tag})), "sessions")
    first = ok(api.get(f"{STUDIO}/sessions", params={"q": tag, "limit": 3}))
    rest = ok(
        api.get(f"{STUDIO}/sessions", params={"q": tag, "limit": 3, "cursor": first["next_cursor"]})
    )
    assert len(whole) == 4 and names(first, "sessions") + names(rest, "sessions") == whole
    assert rest["next_cursor"] is None and "naming" in rest
    # no q, no limit: the newest first, as before 1.92
    assert ok(api.get(f"{STUDIO}/sessions"))["sessions"][0]["title"] == f"Fox at dusk {tag}"
    for bad, detail in (
        ({"q": "x" * 201}, "q must be at most 200 characters"),
        ({"limit": 0}, "limit must be a whole number from 1 to 200"),
        ({"cursor": "bm9wZQ"}, "cursor is not a next_cursor"),
    ):
        assert ok(api.get(f"{STUDIO}/sessions", params=bad), 422) == {"detail": detail}, bad


def test_boards_sequences_and_pipelines(api, api_b, tag):
    for client, name in (
        (api, f"The Last Tide {tag}"),
        (api, f"Neon rain {tag}"),
        (api_b, f"Low tide {tag}"),
    ):
        ok(client.post(f"{STUDIO}/magic/boards", json={"name": name}), 201)
        ok(client.post(f"{STUDIO}/timelines", json={"name": name}), 201)
        ok(client.post(PIPES, json=chain(name)), 201)
    for path, key, field in (
        ("magic/boards", "boards", "name"),
        ("timelines", "timelines", "name"),
        ("pipelines", "pipelines", "title"),
    ):
        url = f"{STUDIO}/{path}"
        assert names(ok(api.get(url, params={"q": f"tide {tag}"})), key, field) == [
            f"The Last Tide {tag}"
        ], path
        assert names(ok(api_b.get(url, params={"q": f"tide {tag}"})), key, field) == [
            f"Low tide {tag}"
        ], path
        assert ok(api.get(url, params={"q": f"rooftop {tag}"}))[key] == [], path
        one = ok(api.get(url, params={"q": tag, "limit": 1}))
        more = ok(api.get(url, params={"q": tag, "limit": 1, "cursor": one["next_cursor"]}))
        assert names(one, key, field) + names(more, key, field) == [
            f"Neon rain {tag}",
            f"The Last Tide {tag}",
        ], path
        assert more["next_cursor"] is None and ok(api.get(url))["next_cursor"] is None, (
            path
        )  # the cap: one page
        assert api.get(url, params={"cursor": "%%"}).status_code == 422, path


def test_chats(api):
    page = ok(
        api.get(
            f"{STUDIO}/assistant/threads", params={"q": f"nothing{uuid.uuid4().hex}", "limit": 5}
        )
    )
    assert page["threads"] == [] and page["next_cursor"] is None and "status" in page
    assert api.get(f"{STUDIO}/assistant/threads", params={"limit": 201}).status_code == 422


def test_runs_of_a_session(api, api_b, tag):
    sid = new_session(api, f"Runs {tag}")["id"]
    fox = start_run(api, sid, QUICK | {"prompt.text": f"a red fox {tag}"})
    owl = start_run(api, sid, QUICK | {"prompt.text": f"a snow owl {tag}"})
    for run in (fox, owl):
        wait_run(api, run["id"])
    runs = f"{STUDIO}/sessions/{sid}/runs"
    assert [r["id"] for r in ok(api.get(runs, params={"q": f"OWL {tag}"}))["runs"]] == [owl["id"]]
    assert [r["id"] for r in ok(api.get(runs, params={"q": "echo"}))["runs"]] == [
        owl["id"],
        fox["id"],
    ]  # workflow
    assert (
        ok(api.get(runs, params={"q": "prompt.text"}))["runs"] == []
    )  # input names are not searched
    first = ok(api.get(runs, params={"limit": 1}))
    rest = ok(api.get(runs, params={"limit": 1, "cursor": first["next_cursor"]}))
    assert [r["id"] for r in first["runs"] + rest["runs"]] == [owl["id"], fox["id"]] and rest[
        "next_cursor"
    ] is None
    assert api_b.get(runs, params={"q": "owl"}).status_code == 404  # another user's session


def test_batches_and_their_rows(api, api_b, tag):
    batch = submit(
        api, lines([f"a blue heron {tag}", f"a grey wolf {tag}"]), title=f"Heron sweep {tag}"
    )
    other = submit(api_b, lines([f"a blue heron {tag}"]), title=f"Heron sweep {tag}")
    try:
        wait_batch(api, batch["id"])
    finally:
        finish(api, batch["id"])
        finish(api_b, other["id"])
    found = ok(api.get(BATCHES, params={"q": f"sweep HERON {tag}"}))
    assert [b["id"] for b in found["batches"]] == [batch["id"]] and found["next_cursor"] is None
    assert [b["id"] for b in ok(api.get(BATCHES, params={"q": f"echo {tag}"}))["batches"]] == [
        batch["id"]
    ]
    done = ok(api.get(BATCHES, params={"q": tag, "state": ["done", "failed"]}))["batches"]
    assert [b["id"] for b in done] == [batch["id"]]
    assert ok(api.get(BATCHES, params={"q": tag, "state": "running"}))["batches"] == []
    assert ok(api.get(BATCHES, params={"state": "nope"}), 422) == {
        "detail": "state must be one of queued, running, waiting, paused, done, failed, stopped"
    }
    rows = f"{BATCHES}/{batch['id']}/rows"
    assert [r["row"] for r in ok(api.get(rows, params={"q": f"wolf {tag}"}))["rows"]] == [2]
    assert [r["row"] for r in ok(api.get(rows, params={"q": "succeeded"}))["rows"]] == [
        1,
        2,
    ]  # the status
    page = ok(api.get(rows, params={"q": tag, "limit": 1}))
    assert [r["row"] for r in page["rows"]] == [1] and page["next_cursor"]
    assert api_b.get(rows, params={"q": "wolf"}).status_code == 404


def test_library_words(api, api_b, tag):
    def look(client, name: str, text: str) -> dict:
        body = {"kind": "look", "group": "film", "name": name, "text": text}
        return ok(client.post(LIB, json=body), 201)

    rain = look(api, f"Window light {tag}", "soft rain on the glass")
    dusk = look(api, f"Dusk {tag}", "warm window glow")
    secret = look(api_b, f"Window rain {tag}", "rain")
    mine = {"source": "mine"}
    assert [
        i["id"] for i in ok(api.get(LIB, params=mine | {"words": f"RAIN {tag} window"}))["items"]
    ] == [rain["id"]]
    both = ok(api.get(LIB, params=mine | {"words": f"window {tag}"}))["items"]
    assert [i["id"] for i in both] == [
        dusk["id"],
        rain["id"],
    ]  # the list's own order: newest updated first
    assert secret["id"] not in {i["id"] for i in ok(api.get(LIB, params={"words": tag}))["items"]}
    # q keeps the @ menu's prefix rule: "light" is not how the name starts
    assert rain["id"] not in {
        i["id"] for i in ok(api.get(LIB, params=mine | {"q": "light"}))["items"]
    }
    assert api.get(LIB, params={"words": "x" * 201}).status_code == 422
