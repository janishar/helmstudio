"""The session event log: JSON polling with ?since= and SSE replay with Last-Event-ID or ?since=."""

from helpers import STUDIO, ok, sse


def shape(events: list[dict]) -> list[tuple]:
    return [(e["seq"], e["type"], e["run_id"]) for e in events]


def test_json_since(api, echo_done):
    url = f"{STUDIO}/sessions/{echo_done['session']['id']}/events"
    page = ok(api.get(url))
    seqs = [e["seq"] for e in page["events"]]
    assert seqs == sorted(set(seqs)) and seqs[0] >= 1 and page["last_seq"] == seqs[-1]
    assert shape(page["events"]) == shape(echo_done["events"])  # the session has this one run
    k = seqs[2]
    assert [e["seq"] for e in ok(api.get(url, params={"since": k}))["events"]] == seqs[3:]
    assert ok(api.get(url, params={"since": page["last_seq"]})) == {
        "events": [],
        "last_seq": page["last_seq"],
    }
    for since in ("-1", "abc"):
        assert api.get(url, params={"since": since}).status_code == 422, since


def test_sse_replay(api, echo_done):
    sid = echo_done["session"]["id"]
    seqs = [e["seq"] for e in echo_done["events"]]
    k, rest = seqs[2], seqs[3:]

    def replay(**kw) -> list[dict]:
        return sse(api, sid, lambda fs: len(fs) >= len(rest), **kw)

    # Last-Event-ID, ?since=, and both (the header wins)
    for frames in (
        replay(last_event_id=k),
        replay(since=k),
        replay(since=0, last_event_id=k),
    ):
        assert [f["id"] for f in frames] == rest
        assert all(f["id"] == f["data"]["seq"] and f["event"] == f["data"]["type"] for f in frames)
    full = sse(api, sid, lambda fs: len(fs) >= len(seqs))
    assert shape([f["data"] for f in full]) == shape(echo_done["events"])
