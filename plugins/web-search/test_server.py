"""`uv run pytest`: the tool's answers with a fake ddgs (nothing is ever searched)."""

from ddgs.exceptions import DDGSException, RatelimitException

import server


def test_search_answers_and_errors(monkeypatch):
    got = []

    def fake(q, limit):
        got.append((q, limit))
        return [{"title": f"T{i}", "href": f"https://e.org/{i}", "body": "s"} for i in range(8)]

    monkeypatch.setattr(server, "search", fake)
    code, body = server.answer({"input": {"query": " ltx ", "limit": 7}})
    assert code == 200 and got == [("ltx", 7)]
    assert body["line"] == 'Searched the web · DuckDuckGo · "ltx"'
    assert len(body["result"]["results"]) == 7 and len(body["sources"]) == 5
    assert body["result"]["results"][0] == {"title": "T0", "url": "https://e.org/0", "snippet": "s"}
    assert server.answer({"input": {"query": "x"}})[0] == 200 and got[-1] == ("x", 5)
    for bad in ({}, {"input": {"query": ""}}, {"input": {"query": "x", "limit": 11}},
                {"input": {"query": "x", "limit": True}}):  # fmt: skip
        assert server.answer(bad)[0] == 422
    for exc, code in ((RatelimitException("x"), 429), (DDGSException("boom"), 502)):

        def fail(q, limit, exc=exc):
            raise exc

        monkeypatch.setattr(server, "search", fail)
        assert server.answer({"input": {"query": "x"}})[0] == code
