# plugins

Personal, opt-in add-ons that run outside the product. They are not part of helmstudio's services, releases or
deployments, nothing in the stack starts or depends on them, and they are not supported. Each folder has its own
README with its own terms; read it before running anything here.

Rule: no secrets or logs inside plugins/. The studio mounts this folder read-only to read each `plugin.json`, so a
plugin keeps its keys, tokens and logs elsewhere (the bridge's: `~/.config/helmstudio/`).
