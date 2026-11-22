# helm mcp: helmstudio in Claude Code and Claude Desktop

[← Docs index](../README.md) · design: [plan 16](../plan/16-mcp.md)

`helm mcp` is an MCP server over stdio. An agent uses it to do what you do in the studio: find a workflow, run it,
wait, look at the takes, batch, pick, put takes on a Timeline and export. It calls the API as you, with your
`helm login`, so it sees only your things.

## Set up

1. Build the CLI and sign in once, with the key file so no env var is needed:

   ```sh
   make -C cli build                      # cli/bin/helm
   cli/bin/helm login --url http://127.0.0.1:8300 --email you@example.com --api-key-file /path/to/helm.key
   ```

2. **Claude Code** (for every project: `-s user`):

   ```sh
   claude mcp add -s user helmstudio -- /absolute/path/to/cli/bin/helm mcp
   ```

3. **Claude Desktop**: add it to `~/Library/Application Support/Claude/claude_desktop_config.json` and restart the
   app. Use the absolute path: Desktop does not read your shell's PATH.

   ```json
   { "mcpServers": { "helmstudio": { "command": "/absolute/path/to/cli/bin/helm", "args": ["mcp"] } } }
   ```

If the session expires, run `helm login` again; the server picks up the new tokens on its next start.

## Tools

Reads (never ask):

| Tool | Does |
|---|---|
| `list_workflows`, `get_workflow` | Workflows and their params (types, ranges, defaults) |
| `plan_run` | Where a run would go and what it would load, before it starts |
| `search_takes`, `get_take` | The gallery; one take with a small thumbnail the agent can see |
| `list_library` | Elements, looks, voices and LoRAs |
| `batch_status`, `review_batch` | A batch's rows, steps, decisions and chat; the rows waiting at a gate |
| `list_timelines`, `show_timeline` | Sequences; one with its exports |
| `wait_run` | Waits for a run (up to 10 min, with progress) and returns its take ids; `out_dir` saves the files |

Writes (your client may ask, depending on its settings):

| Tool | Does |
|---|---|
| `run_workflow` | Starts a run and returns at once; at most 4 of this server's runs wait at a time |
| `cancel_run` | Stops a run |
| `upload` | A local png, jpeg or webp image becomes a take |
| `start_batch`, `pick`, `batch_chat` | Start a batch; answer a gate or judge; talk to the director |
| `add_to_timeline`, `export_timeline` | Append takes (a new sequence without an id); export the MP4 (`out` waits and saves it) |

Deletes (marked `destructiveHint`: the client asks before each one; they cannot be undone):

| Tool | Does |
|---|---|
| `delete_take` | A take leaves the gallery |
| `delete_run` | A finished run (its takes stay) |
| `delete_session` | A session with nothing running (its takes stay) |

Resources: `helm://take/{id}` (metadata and thumbnail) and `helm://workflow/{id}`. No prompts. Admin tools are not
offered. Errors come back as the API's own text (for example `404: take not found`).

## Try it

Ask: "make a Qwen still of a lighthouse at dusk, then a short LTX clip from it, and put both on a new Timeline and
export it to ~/Desktop/lighthouse.mp4".
