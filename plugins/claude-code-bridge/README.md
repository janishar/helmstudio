# Your Claude subscription (claude-code-bridge)

> **Disclaimer: personal use only.** It runs *your own* locally signed-in Claude Code (`claude`) with *your own* Claude
> plan, for yourself, on your own machine. Never host it for others, expose it on a network, or route other people's
> requests through it. Each user is responsible for following Anthropic's terms:
> https://code.claude.com/docs/en/legal-and-compliance.md
>
> "Claude" and "Claude Code" are trademarks of Anthropic. This project is not affiliated with or endorsed by Anthropic.

A small OpenAI-compatible endpoint (Python 3 stdlib only) that answers with `claude -p`, so helmstudio can use your own
Claude plan as a model provider. It ships in the Mac app (from 0.1.0-alpha.3) and in the repo; the folder and name stay
`claude-code-bridge`, so an existing install and its provider carry on.

What helmstudio does and doesn't do (Anthropic's conditions for products that run Claude Code):

- It runs the unmodified `claude` you installed yourself. You install Claude Code and sign in through Anthropic's own
  flow (https://claude.com/claude-code); helmstudio never installs it and never signs you in.
- It never reads, stores or passes on your Claude credentials: it only asks `claude auth status` whether you are signed
  in. Its one secret is the bridge's own token, which run.sh makes.
- It never pays for or resells usage: your plan's limits apply.

## Turn it on

1. Install Claude Code and sign in (`claude`, then follow its sign-in). `claude auth status` should say you are signed
   in.
2. Plugins: turn Your Claude subscription on. The Mac's runner starts `run.sh` (the Mac app, or `make runners` in dev);
   with no `claude` or not signed in it shows Couldn't start · Install Claude Code and sign in, then Try again.
3. Add as provider with the `BRIDGE_TOKEN` value from `~/.helm/claude-code-bridge.env`. The provider is Only you: no
   other account sees or uses it.
4. Admin › AI agents: pick it for the Assistant.

To run it by hand instead: `plugins/claude-code-bridge/run.sh`.

The first run writes its env file with a random `BRIDGE_TOKEN` (chmod 600) at `$BRIDGE_ENV`, by default
`~/.helm/claude-code-bridge.env`: outside plugins/, because the studio mounts that folder and the Mac app's copy is
inside the app (an env from an old place, `plugins/claude-code-bridge/.env` or
`~/.config/helmstudio/claude-code-bridge.env`, is moved there, same token). It listens on 127.0.0.1:8420 only;
containers reach it as `http://host.docker.internal:8420` (OrbStack in dev; the Mac app's VM, whose DNS answers with
the Mac's address). Its log, when the runner starts it: `~/.helm/logs/claude-code-bridge.log`.

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
  --disable-slash-commands --no-session-persistence --permission-prompts none`, run in an empty folder under the system's temp folder.
- One call at a time, 180 s timeout (`BRIDGE_TIMEOUT` in the env file). One log line per call: time, tokens, cost.

Up to 4 calls run at once (`BRIDGE_MAX_CONCURRENT` in the env file; about 250 MB each), the rest wait their turn. Claude
Code starts with its update, telemetry and other background checks off, so start-up adds well under a second.

Limits: your plan's usage limits apply (Anthropic may count `claude -p` against a separate Agent SDK allowance), no
streaming, `max_tokens` ignored. A fresh `claude` per call: the prompt cache is on Anthropic's side, so it still hits.
