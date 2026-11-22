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
| `list_timelines`, `show_timeline` | Sequences; one for an agent (every clip flat: ids, times in seconds and frames, its revision) with its exports |
| `try_timeline_edit` | Tries edit ops on a sequence without saving: the diff it would make, or the op that fails and why |
| `timeline_revisions` | A sequence's history (the last 100: who, via what, which ops), or one revision's document |
| `timeline_frame` | The picture at a time as an image (an image take, or a video's nearest stored frame: first or last) |
| `wait_run` | Waits for a run (up to 10 min, with progress) and returns its take ids; `out_dir` saves the files |

Writes (your client may ask, depending on its settings):

| Tool | Does |
|---|---|
| `run_workflow` | Starts a run and returns at once; at most 4 of this server's runs wait at a time |
| `cancel_run` | Stops a run |
| `upload` | A local png, jpeg or webp image becomes a take |
| `start_batch`, `pick`, `batch_chat` | Start a batch; answer a gate or judge; talk to the director |
| `add_to_timeline`, `export_timeline` | Append takes (a new sequence without an id); export the MP4 (`out` waits and saves it) |

Marked `destructiveHint` (the client asks before each one). The deletes cannot be undone; a sequence edit can
(Undo in the app, or `restore_timeline`):

| Tool | Does |
|---|---|
| `edit_timeline` | Applies edit ops (split, trim, move, remove, lift, replace, set, add, insert, …) in order, all or nothing, on the `base_revision` it was planned on; one Undo step |
| `restore_timeline` | Brings back a kept revision as a new one |
| `delete_take` | A take leaves the gallery |
| `delete_run` | A finished run (its takes stay) |
| `delete_session` | A session with nothing running (its takes stay) |

Editing a sequence: `show_timeline` (the clip ids and the revision), `try_timeline_edit`, then `edit_timeline` with
the same ops and revision. A stale revision is refused (`409 … now revision <n>`): read again and plan again. Clips
an op makes get ids from the server when the op leaves them out (in the answer's `diff.added`). Batch ops into one
call: 100 revisions are kept. The history shows these edits as `via mcp`.

Local paths (`upload`'s `path`, `out_dir`, `out`) must be absolute or start with `~/`: the server runs in the
client's folder (Claude Desktop starts it in `/`), not yours.

Resources: `helm://take/{id}` (metadata and thumbnail) and `helm://workflow/{id}`. No prompts. Admin tools are not
offered. Errors come back as the API's own text (for example `404: take not found`).

An API call that gets no answer fails after 2 minutes; file saves and `wait_run`'s wait run longer. Cancelling a
tool call in the client stops its requests.

## Try it

Ask: "make a Qwen still of a lighthouse at dusk, then a short LTX clip from it, and put both on a new Timeline and
export it to ~/Desktop/lighthouse.mp4". Then: "cut the clip at 1 s, drop the second half and show me the frame at
0.5 s".
