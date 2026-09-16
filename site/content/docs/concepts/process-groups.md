# Process groups

A studio is not always one server. A native engine and the web server in front of it, a queue worker, a step that prepares a cache: each is a **process** under `processes`, and together they are the studio's **group**. helmstudio starts and stops a group as one thing, with one code path for every member, so an extra process is a few lines of YAML and never a special case.

@sample groups/helmstudio.yaml

The manifest above validates. The repository it names is an illustration, and does not exist.

## Roles

| Role | What it is |
|---|---|
| `main` | The process that owns the studio's page. Every studio has exactly one, and it carries the health check that says the studio is ready. |
| `sidecar` | A long-running helper, such as an engine holding a model. |
| `worker` | Long-running, usually with no page and no port. |
| `oneshot` | Runs to completion; the processes that depend on it start only when it exits with zero. |

A process with no `role` is `main`.

## Order

`depends_on` names the processes that must be healthy before this one starts. helmstudio starts the group in that order, waits on each member's health check before starting what depends on it, and stops the group in reverse. `helm validate` refuses a name that is not declared, and a cycle.

## Commands

A command is a template. These placeholders are substituted before it runs, and `helm validate` refuses any other:

| Placeholder | Becomes |
|---|---|
| `{port}` | The port assigned to this process. |
| `{ports.<name>}` | The port assigned to another process in the group. |
| `{models.<name>}` | The real path of a weight. See [weights](/docs/concepts/weights/). |
| `{models.selected}` | The path of the weight chosen from a selectable set. |
| `{root}` | The studio's checkout. |
| `{data}` | The studio's own data directory. |
| `{venv}` | The studio's Python environment, when it declares `python`. |

A command runs in the studio's root, or in `cwd` relative to it, with the manifest's `env` added.

## Ports

`port.prefer` is advisory: the process gets that port when it is free, and another when it is not, so a command takes its port from `{port}` and never assumes one. `port.fixed` is for a process that cannot be told a port: it refuses to start when the port is taken, naming what holds it, and it fails [criterion 5](/docs/publishing/). A process that listens on nothing declares no port.

## Health, and what it never does

A health check is exactly one of:

- `path` — an HTTP GET on the process's port, answering 200;
- `tcp` — a successful connection to the port;
- `exec` — a command that exits with zero, for a process with no port.

helmstudio checks every `interval_s` seconds (2 by default) until `timeout_s` (180 by default), and shows the time elapsed against that budget. A model that loads 20 GB before it answers needs a large budget; a thin wrapper needs a small one, so a dead process is reported quickly.

**A failed health check never restarts a main process.** An eight-minute generation has to survive a slow probe. A main process that exits unexpectedly fails the group, and is never restarted automatically. A `sidecar` with `restart: on-failure` is retried with backoff, at most three times in ten minutes, and then the group fails.

## One heavy studio at a time

`heavy: true` marks the process that holds the model, and a group has at most one. Only one group with a heavy member runs at a time: starting a second states the memory arithmetic, from each studio's `peak_ram_gb`, and stops the first only when you confirm.

`busy` lets that statement say whether the running studio is in the middle of something. It is an HTTP GET on the process's port that answers with `{"busy": true}` or `{"busy": false}`, and may add `loaded`, `message` and `progress`. An answer that is late, malformed or missing reads as unknown, which is never shown as idle.

## Not built yet

`autostart: false` is meant to leave a worker for someone to start from the studio's page. That page does not exist yet, so today a process with `autostart: false` does not run at all, and a process that depends on one cannot launch.
