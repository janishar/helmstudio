# web-search

> **Disclaimer.** DuckDuckGo has no official search results API. This plugin uses the unofficial
> [`ddgs`](https://pypi.org/project/ddgs/) library, which reads DuckDuckGo's public pages: it can be rate-limited
> or stop working when they change, and it is not affiliated with or endorsed by DuckDuckGo. Each user is
> responsible for following DuckDuckGo's terms: https://duckduckgo.com/terms

Web search for the Assistant (an assistant-tools plugin, `plugin.json`): one tool, `search` {query, limit ≤ 10,
default 5}. Free, no key. Only the query leaves the machine, and the Assistant asks first (`sends_off_machine`).

## Start it

    plugins/web-search/run.sh

Needs [uv](https://docs.astral.sh/uv/). It listens on 127.0.0.1:8421 (`WEB_SEARCH_HOST`, `WEB_SEARCH_PORT`);
OrbStack containers reach it as `http://host.docker.internal:8421`. Then turn it on in the Plugins page (admins).

## API

- `GET /health` → `{"ok": true}`
- `POST /tools/search` `{"input": {"query": "…", "limit": 5}}` → `{"result": {"results": [{title, url, snippet}]},
  "line": "Searched the web · DuckDuckGo · \"…\"", "sources": [{title, url}]}`
- Errors are `{"detail": "…"}`: 422 bad input, 429 rate-limited, 502 any other search failure.

## Test

    uv run pytest
