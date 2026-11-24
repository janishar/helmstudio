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

Needs `claude` installed and logged in with your subscription (`claude auth status`). The first run writes its env
file with a random `BRIDGE_TOKEN` (chmod 600) at `$BRIDGE_ENV`, by default `~/.helm/claude-code-bridge.env`: outside
plugins/, because the studio mounts that folder (an env from an old place, `plugins/claude-code-bridge/.env` or
`~/.config/helmstudio/claude-code-bridge.env`, is moved there, same token). It listens on 127.0.0.1:8420 only;
OrbStack containers reach it as `http://host.docker.internal:8420`.

## Add it in the dev stack's Admin

1. Plugins: turn Claude Code bridge on, then Add as provider with the `BRIDGE_TOKEN` value from the env file. The
   provider is Only you: no other account sees or uses it.
2. Admin › AI agents: pick it for the Assistant.

## How it works

- `GET /v1/models` and `POST /v1/chat/completions` (no streaming). A bearer token is checked on every request.
- System messages go to `--system-prompt`; the rest becomes the prompt.
- `tools` and `response_format` (json_schema / json_object) use `--json-schema` structured output; tool calls come
  back as OpenAI `tool_calls`.
- The child process gets no `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN` or other provider variables, so the
  subscription login is used.
- Thinking: `reasoning_effort` (low, medium or high; also xhigh, max) becomes `claude -p --effort <level>`; absent = the
  CLI's default; anything else is 400. A provider made in Plugins › Add as provider gets the levels low, medium and
  high per model (contracts/providers/presets.json `claude-code-bridge`), set in Admin › Model providers and AI agents.
  Test: `python3 plugins/claude-code-bridge/test_bridge.py`.
- Locked down: `--safe-mode --tools "" --strict-mcp-config --mcp-config '{"mcpServers":{}}' --setting-sources ""
  --disable-slash-commands --no-session-persistence --permission-prompts none`, run in an empty `work/` folder.
- One call at a time, 180 s timeout (`BRIDGE_TIMEOUT` in the env file). One log line per call: time, tokens, cost.

Up to 4 calls run at once (`BRIDGE_MAX_CONCURRENT` in the env file; about 250 MB each), the rest wait their turn. Claude
Code starts with its update, telemetry and other background checks off, so start-up adds well under a second.

Limits: your plan's usage limits apply (Anthropic may count `claude -p` against a separate Agent SDK allowance), no
streaming, `max_tokens` ignored. A fresh `claude` per call: the prompt cache is on Anthropic's side, so it still hits.
