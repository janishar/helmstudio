# Changelog

## 0.1.0-alpha.1 (2026-11-24)

The first public alpha. Expect rough edges and breaking changes between releases.

### What you can do

- Create images, video and voice with open-weight models on your own Mac (MLX, PyTorch MPS) and NVIDIA machines.
  Call characters and places by name with `@`, and set a look (film, camera, colour, lighting) once.
- Work with the Assistant: it fills the form, wires the graph, places clips, asks before it uses the GPU, and each
  step has its own Undo.
- See every workflow two ways: a Studio form or a Graph of typed steps on a node canvas; editing one edits the other.
- Run pipelines over a list while you are away; agent nodes judge each take against a checklist, retry, and learn
  from the takes you keep.
- Keep every take in the Gallery with the exact settings and inputs that made it.
- Cut on the Timeline (trim, blade, ripple, direct new shots in place) and export an MP4.
- Manage models per machine: download weights, pick variants, add add-ons, upload LoRAs to a shared library.
  Remote models (OpenAI, Anthropic, DeepSeek and others) with your own keys.
- Use it from Claude: `helm mcp` gives MCP clients the Assistant's tools.
- Host it for a team: self-hosted, multi-user, each person sees only their own work.

### The Mac app (prototype)

- `helmstudio.dmg` on the GitHub release: the whole studio on one Mac, the services in a small VM of the app's own,
  the runners on the Mac's GPU, a menu-bar item. No Docker or OrbStack needed: the images are downloaded from
  GitHub's registry on first open.

### Known limits

- The Mac app needs Apple Silicon.
- The app is ad-hoc signed (no Developer ID): open it the first time with right-click › Open.
- No uninstall tool and no auto-update for the app; delete its folders by hand (docs/guides/mac-app.md).
- NVIDIA hosts are set up by hand (deploy/gpu-host/README.md).
- An Assistant steer is kept only in memory: if pipelines restarts mid-answer, it becomes the next message.
- Models are downloaded separately (on the Models screen) and keep their own licences; no weights ship with
  helmstudio.
