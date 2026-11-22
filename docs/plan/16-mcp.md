# helmstudio-next plan — MCP server for agents

[← Plan index](../README.md)

Status: a plan, not in a milestone. Nothing is built. The owner asked on 2026-11-21 how an MCP server would let an
agent (Claude Code, Claude Desktop, any MCP client) work with helmstudio.

## 20. `helm mcp`: the platform as MCP tools

An agent should do what a person does in the studio: find a workflow, run it, wait, look at the takes, pick, batch,
put takes on a Timeline and export. It does this through the public API, as the signed-in user, like the `helm` CLI.

### 20.1 Where it lives

**A `helm mcp` command in `cli/`, not a new service.** The CLI already has everything an MCP server needs: the
client with the Kong key and the user's token, refresh on 401, uploads, run following, saving outputs. `helm mcp`
speaks MCP over **stdio**, so an MCP client starts it as a local process:

```json
{ "mcpServers": { "helmstudio": { "command": "helm", "args": ["mcp"] } } }
```

- **Auth** is the user's `helm login` config. No new keys, no new auth path: owner scoping (RLS), another user's id
  is 404, and admin routes stay admin-only, all on the server side as today.
- **No service boundary moves.** It calls Kong only; no new contract, message or database. `contracts/` changes only
  if a tool needs an API the UI lacks (none in the first set).
- **One dependency:** the official Go MCP SDK (`github.com/modelcontextprotocol/go-sdk`), pinned in
  [14-dependencies.md](14-dependencies.md) when built. Hand-rolling JSON-RPC is possible but the SDK gets
  initialize, progress, cancel and schema details right.

Skipped for now: a remote MCP endpoint (Streamable HTTP behind Kong, with OAuth for remote clients). Add it when an
agent must reach the studio from another machine; it would then be its own `mcp_service`, calling Kong like the CLI.

### 20.2 Tools

Task-sized tools, not one per endpoint (there are about 100). Each maps to calls the CLI already makes.
The tools are defined once in the shared catalog `contracts/tools/catalog.json` ([18-assistant.md](18-assistant.md)
22.7), which the in-app assistant also reads: `cli/` copies it with `make gen` and registers every tool marked `mcp`.
The table below is its `mcp` surface; its read tools (`list_workflows`, `get_workflow`, `plan_run`, `search_takes`,
`get_take`, `list_library`, `batch_status`, `list_timelines`, `show_timeline`) are the assistant's too, which adds
its own reads (`sessions`, `get_run`, `models`, `hosts`, …) in the same catalog.

| Tool | Does | API |
|---|---|---|
| `list_workflows`, `get_workflow` | Workflows and their params (types, ranges, defaults), so the agent knows what `-p` means | `/workflows`, `/workflows/{id}`, `/nodes` |
| `plan_run` | What a run will cost (model loads, size) before it starts | `/runs/plan` |
| `run_workflow` | Starts a run (params, LoRAs, media inputs, session) and returns the run id at once (long work is 202) | `/sessions/{id}/runs` |
| `wait_run` | Waits for a run, sending MCP progress from the session's event stream; returns the takes. Bounded (e.g. 10 min), then returns "still running" | `/sessions/{id}/events`, `/runs/{id}` |
| `cancel_run` | Cancels a run | `/runs/{id}/cancel` |
| `search_takes`, `get_take` | Gallery search; one take with a small thumbnail as MCP image content, so the agent can see it | `/takes`, `/takes/{id}`, `/files` |
| `upload` | A local file (image, video, audio) becomes a take to use as an input | `/uploads` |
| `list_library` | Elements, looks, LoRAs and voices, for `@element` mentions and look ids | `/library`, `/loras` |
| `start_batch`, `batch_status`, `review_batch`, `pick` | Batches and pipelines over rows; answer a gate or a Suggest judge | `/batches/...` |
| `batch_chat` | Talk to a pipeline's director agent | `/batches/{id}/chat` |
| `list_timelines`, `show_timeline`, `add_to_timeline`, `export_timeline` | Put takes on a sequence and export it | `/timelines/...` |

Left out: admin tools (users, keys, providers, model downloads, host pins and unloads), deletes and the Timeline's
clip-level editing. Add them on request, deletes and admin with MCP's `destructiveHint`.

Clip-level editing is planned in [24-timeline-edit-api.md](24-timeline-edit-api.md) (§28): `edit_timeline` (ops, dry
run), `timeline_revisions`, `restore_timeline` and `timeline_frame` over `POST …/timelines/{id}/edits`, with
`try_timeline_edit` read-only and `destructiveHint` on applies and restore.

**Resources:** `helm://take/{id}` (the take's thumbnail and metadata) and `helm://workflow/{id}`, so a client can
attach them to a conversation. **Prompts:** none at first.

### 20.3 Rules the tools keep

- **Never bytes or URLs in tool inputs to the API**: inputs are take ids; `upload` turns a local path into one.
  Outputs give take ids, a small thumbnail, and optionally save files to a folder the agent names (like `-o`).
- **The GPU is shared.** The dispatcher already orders runs fairly; `run_workflow` adds nothing. A tool option
  caps sizes and counts so an agent loop cannot fill the queue (e.g. at most 4 queued runs per MCP process).
- **Errors** come back as the API's `{"detail": ...}` text, as an MCP tool error the agent can read and fix.
- **Long work is not held open.** `run_workflow` and `start_batch` return ids; `wait_run` and `batch_status` poll
  or stream with a time limit.

### 20.4 Testing

- Unit tests in `cli/` with the existing fake HTTP server: each tool's input schema, the API calls it makes, errors.
- One contract-suite case on `helmstudio-test` in fake mode: `helm mcp` over stdio runs the `echo` workflow,
  waits, and gets a take; another user's take id is a 404 through the tool.
- By hand: add it to Claude Code and ask for a Qwen still, then a short LTX clip from it, then a Timeline export.

### 20.5 Steps

1. Pin the SDK; `helm mcp` with `list_workflows`, `get_workflow`, `run_workflow`, `wait_run`, `get_take`
   (a run you can see end to end).
2. Uploads, search, library, `plan_run`, `cancel_run`, resources.
3. Batches, picks and the director's chat.
4. Timeline tools; a short "how to use" in `docs/`.

### 20.6 Open choices

**Decided by the owner, 2026-11-22 (one by one):** 1. Build all four steps (§20.5) as one build, after the assistant
merges. 2. Stdio only (local); no remote endpoint. 3. Test with Claude Code and Claude Desktop. 4. Add delete tools
(takes, finished runs, sessions) marked `destructiveHint`, so MCP clients ask before each one; admin tools stay out.


- Is it a milestone (M9) now, or after the backlog items? (Recommended: M9, steps 1–2 first.)
- Stdio only (recommended), or the remote endpoint too?
- Which agent clients matter: Claude Code and Claude Desktop (recommended), others?
