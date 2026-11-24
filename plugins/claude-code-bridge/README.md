# claude-code-bridge

> **Disclaimer: personal use only.** This is not a supported helmstudio provider and not part of any helmstudio
> release or deployment. It runs *your own* locally logged-in `claude` CLI with *your own* Claude subscription, for
> yourself, on your own machine. Never host it for others, expose it on a network, or route other people's requests
> through it. Each user is responsible for following Anthropic's terms:
> https://code.claude.com/docs/en/legal-and-compliance.md
>
> "Claude" and "Claude Code" are trademarks of Anthropic. This project is not affiliated with or endorsed by Anthropic.

A small OpenAI-compatible endpoint (Python 3 stdlib only) that answers with `claude -p`, so a local dev stack can use
it as a model provider.

## Start it

    plugins/claude-code-bridge/run.sh

Needs `claude` installed and logged in with your subscription (`claude auth status`). The first run writes `.env`
here with a random `BRIDGE_TOKEN` (chmod 600, git-ignored). It listens on 127.0.0.1:8420 only; OrbStack containers
reach it as `http://host.docker.internal:8420`.

## Add it in the dev stack's Admin

1. Admin › Model providers: add a provider with the "llama-server" preset (OpenAI-compatible local server).
   Base URL `http://host.docker.internal:8420/v1`, key = the `BRIDGE_TOKEN` value from `.env`.
   Models: `claude-sonnet`, `claude-opus`, `claude-haiku`.
2. Admin › AI agents: pick it for the Assistant.

## How it works

- `GET /v1/models` and `POST /v1/chat/completions` (no streaming). A bearer token is checked on every request.
- System messages go to `--system-prompt`; the rest becomes the prompt.
- `tools` and `response_format` (json_schema / json_object) use `--json-schema` structured output; tool calls come
  back as OpenAI `tool_calls`.
- The child process gets no `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN` or other provider variables, so the
  subscription login is used.
- Locked down: `--safe-mode --tools "" --strict-mcp-config --mcp-config '{"mcpServers":{}}' --setting-sources ""
  --disable-slash-commands --no-session-persistence --permission-prompts none`, run in an empty `work/` folder.
- One call at a time, 180 s timeout (`BRIDGE_TIMEOUT` in `.env`). One log line per call: time, tokens, cost.

Limits: a few seconds per call (CLI start-up), your plan's usage limits apply, no streaming, `max_tokens` ignored.
