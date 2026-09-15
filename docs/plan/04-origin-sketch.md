# The first sketch — superseded

> **Superseded by the frozen design.** Kept because it records what the project
> looked like before the design sprint, and several things in it were
> deliberately reversed. Where this disagrees with `docs/design/`, the design
> wins. Nothing should be implemented from this page.

Dated 15 September 2026, before the sprint.

**The idea.** One local daemon — Go, standard library, single binary, port 8700
— that never imports studio code. It clones each studio repository, runs its
build steps, fetches weights from Hugging Face into a shared deduplicated
cache, spawns the studio server as a subprocess on an assigned port,
health-checks it, and reverse-proxies it at `/s/<id>/` with an "open in tab"
fallback.

**The adapter** was one YAML manifest per studio in `studios/*.yaml`: id, kind
(video, image, audio), repo, submodules, requires (os, arch, tools, ram),
`build[]` steps, `weights[]` mapping a Hugging Face repo to a destination, and
a `run` block with a command carrying `{port}` and `{models.x}` placeholders,
plus health and ui.

**The four studios**, all already on the machine:

- **ltx-2-studio** — Python 3.11+, `uv sync --all-extras`,
  `bash web/run.sh --model ./models/ltx-2.5`, port 8720, standard-library
  server.
- **h3c-studio** — Go standard library with an h3c submodule, `make mps` then
  `go build -o dist/h3studio .`, run as
  `./dist/h3studio --h3 ./h3c/h3 --model …`, port 8710.
- **iris-studio** — Go with an iris.c submodule, `make mps`, run as
  `./iris-studio --iris iris.c/iris --model <dir> --port 8720`.
- **AuK** — Python 3.10, FastAPI in `web/server.py`, `web/run.sh`; models AuK
  and AuK-Flash on mps in bf16.

**Decisions at the time.** Subprocess isolation; uv per studio; one shared
Hugging Face cache symlinked in; one GPU studio active by default; the registry
in the helmstudio repository with local path overrides.

**Build order at the time.** Manifest schema and registry → process manager and
proxy with h3 studio → installer and weights manager → ltx and AuK, the Python
path → iris → a thin launcher UI with a studio grid, an install checklist with
live logs, and a GPU-owner status bar.

## What the sprint changed

- A single `run` block became `processes[]` with roles and dependencies. A
  studio is not always one server.
- The manifest moved into the studio's own repository; the registry holds
  pointers, and three sources resolve first-match-wins.
- Weights are symlinked **nowhere into a studio checkout**; absolute paths are
  substituted into commands instead, because a symlink inside a checkout
  collides with `git pull`.
- The reverse proxy was deferred until all four studios accept `--base-path`.
  Own-origin tabs ship first.
- State stopped being each studio's problem: everything goes through the SDK
  into one SQLite file, with bytes in directories.
