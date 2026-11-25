# plugins

Opt-in add-ons, each a small separate service (or a skill) in its own folder with a `plugin.json`
(contracts/README.md, Plugins). They are off until an admin turns one on in Plugins; one with a `run` block starts
itself on its host while it is on. The Mac app bundles every folder here (tracked files only: never an .env, a log or a
token). Each folder has its own README with its own terms; read it before turning anything on.

- `claude-code-bridge`: Your Claude subscription. Personal: answers with your own signed-in Claude Code, for you only
  (its provider is Only you). You install and sign in to Claude Code yourself; helmstudio never handles your Claude
  credentials.

Rule: no secrets or logs inside plugins/. The studio mounts this folder read-only to read each `plugin.json`, so a
plugin keeps its keys, tokens and logs elsewhere (the bridge's: `~/.helm/`).
