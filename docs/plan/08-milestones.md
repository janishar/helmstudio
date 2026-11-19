# helmstudio-next plan — Milestones and testing

[← Plan index](../README.md)

## 11. Milestones

Each ends with something usable. Order matters more than dates.

| # | Milestone | Done when |
|---|---|---|
| M0 | Walking skeleton | The reference's skeleton, renamed: Kong with the API-key plugin, auth_service, files_service, web_service with sign-in, the root admin and the Admin Users screen (section 17.3), Postgres, NATS, the object store, `contracts/` and the contract suite. studio_service with sessions, a run, the outbox, the projector and the event stream. A **fake runner** and a fake `echo` family: a workflow runs from the UI and from `helm run`, reports progress live, cancels, and survives killing any one service. Goldens recorded from the old Qwen and LTX studios. |
| M1 | Create + Gallery, on Qwen Image | A native torch runner on the Mac: one "Qwen Image" template (text-to-image, multi-reference edit, Base and Turbo, Enhance), direct-to-GPU int8 loader, residency manager, host state in `hosts`, the dispatcher with fair ordering. The Studio of section 8.2. The gallery hub of section 9.1 with per-user isolation proved by the contract suite. Batches of one workflow on the pipeline engine (section 16.3) with crash-tested resume. Matches the old studio's golden within tolerance. |
| M2 | Graph, Pipelines, Elements | Editable graph with typed connections, auto-wiring, wire-drop search, "Show in Studio". Pipelines: several steps, gates, branches and capped loops, grid inputs, "Save as pipeline", the execution view — shown with Qwen text-to-image → Qwen edit over a list. Elements with `@`. |
| M3 | LoRA and Looks | LoRA library (shared and per-user), normaliser, adapter and bake, LoRA Stack; a strength change re-runs Sample without reloading; a test proves no keys are silently dropped. Looks and the visual picker. |
| M4 | MLX runner + LTX-2.5 | Distilled two-stage text-to-video and image-to-video with audio, keyframes, auto length, LoRA. Image / Video switch. Two runners on one host, with the dispatcher's evict hand-over. Qwen → LTX image-to-video from "Use in…", saved as a pipeline and run unattended over ten rows with each model loaded once. |
| M4b | Agent nodes | The `llm` pack and agent nodes (section 16): Judge in suggest mode, Write, Decide, Memory, capped retry — shown as best-of-4 Qwen stills → LTX over ten rows with no extra model load. Then the director Agent with chat, local-server and remote models, and custom nodes. |
| M5a | Models and admin | Model and LoRA management per host, downloads as jobs, the admin's Hosts screen (pins, the after-run setting), import from `~/.helmstudio`. |
| M5b | Timeline | Editor, export, sequence defaults and New shot (section 15). Needs M3 and M4. |
| M6 | CUDA | Runners in NVIDIA containers on Linux, a second GPU host joining one stack, validated for Qwen Image. Verified on the owner's existing spot GPU VM in GCP (spot only; decided 2026-11-14), which joins the Mac's stack over TLS. |
| M7 | More packs | In the owner's order (2026-11-16): LTX retake / extend / audio-to-video (which also light up as take actions on timeline clips), MiniMax H3 Image, then MiniMax H3 video. FLUX.3 Klein, Wan 2.2 (MLX) and ZONOS3 voices are not in a milestone: they are in [the backlog](../backlog.md) (owner, 2026-11-19). |

This table is the only place milestones are assigned.

### 11.1 Testing

- **Goldens**: fixed seed, smallest size, recorded from each old studio's CLI
  before M1. Compared by PSNR (≥ 40 dB), not bit-exact.
- **Contract suite** through Kong against a separate compose project in fake
  mode: every response checked against the OpenAPI spec, every message against
  its schema, and per-user isolation (another user's id is a 404).
- **`echo` family** suite for validation, cache keys, cancel and crashes; runs
  without a GPU.
- **Per-pack smoke** workflow at 256² and 2 steps.
- Studio layout goldens at three widths in both themes.
