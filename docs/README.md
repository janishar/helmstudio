# helmstudio-next — docs

Status: revision 5 · 2026-11-14 · reviewed against the Qwen Image and LTX code, audited for redundancy; agent-powered pipelines on LangGraph; microservice architecture after the oralab reference

A complete rewrite of helmstudio. Every studio is broken into typed, composable
**stage nodes**. A workflow (a small graph of nodes) is the only thing that
runs; it renders either as a **Studio** (a modern, generated creative UI) or as
a **Graph** (the same document on a node canvas). Everything it makes lands in
the **Gallery**, which is where content is shared and reused. Models are
described by config, including what LoRAs they accept. Apple Silicon (MLX and
PyTorch MPS) and CUDA are both first class. It is a multi-user, self-hosted
system of Go and Python services (section 17).

## Framework plan

Section numbers (§) are stable across files. Each topic has one owning section;
other sections point to it rather than restate it.

| File | Sections | Owns |
|---|---|---|
| [Overview](plan/00-overview.md) | §1, §2, §3 | Decisions, lessons from the old code, principles |
| [Concepts](plan/01-concepts.md) | §4 | Every noun, port types, node kinds |
| [Model config and nodes](plan/02-models-and-nodes.md) | §5 | Model YAML, stage code, packs, `when` / `requires` |
| [Inside a runner](plan/03-runtime.md) | §6 | Execution, cache, memory, loading, LoRA |
| [Workflows, batches and pipelines](plan/04-workflows.md) | §7 | Workflow document, presets, batches, pipelines |
| [UI](plan/05-ui.md) | §8 | Navigation, screens, widgets, visual language |
| [Platform services](plan/06-platform.md) | §9 | Gallery hub and take actions, models and weights, import |
| [Repository layout](plan/07-repo-layout.md) | §10 | Folders, service isolation |
| [Milestones and testing](plan/08-milestones.md) | §11 | The only milestone table |
| [Out of scope and risks](plan/09-scope-and-risks.md) | §12, §13 | Everything not in v1 |
| [Elements and Looks](plan/10-elements-and-looks.md) | §14 | Library items in use |
| [Timeline](plan/11-timeline.md) | §15 | Sequences, directing shots, export |
| [Agent-powered pipelines](plan/12-agents.md) | §16 | LangGraph engine, agent nodes, talking to LLMs, memory |
| [Service architecture](plan/13-services.md) | §17 | Services, users, databases, messaging, dispatcher, deployment, contracts |
| [Dependencies](plan/14-dependencies.md) | §18 | Version pins, what moves together, how they stay current |
| [Voices](plan/15-voices.md) | §19 | Qwen3-TTS and ZONOS3 packs, saved voices, dialogue, audio takes (a plan, not in a milestone) |
| [MCP server](plan/16-mcp.md) | §20 | `helm mcp`: the platform as MCP tools for agents (built 2026-11-22; how to use: [guides/mcp.md](guides/mcp.md)) |
| [NVIDIA quantized path](plan/17-nvidia-quantized.md) | §21 | Community FP8 / int8 variants for NVIDIA hosts (M9) |
| [In-app assistant](plan/18-assistant.md) | §22 | An AI assistant panel that works every screen, confirms costly actions, sees takes (a plan, not in a milestone) |
| [ltx studio features](plan/19-ltx-studio-features.md) | §23 | The new ltx studio options in the LTX pack: distilled actions, NAG, float16, audio relay, adapters, DFR detail, bindings, Refine details and Deblur, SDR to HDR; what is left out and why (a plan, not in a milestone) |
| [H3 options and optimised uploads](plan/20-h3-options-and-uploads.md) | §24 | MiniMax H3 presets, chaining from a frame, Mac speed controls, replacing a video reference's sound, NVIDIA quality and flow shift; the 8-step Turbo LoRA as an optional add-on with a Turbo preset; the browser optimising every media upload (images, audio, and new video uploads) before storage (a plan, not in a milestone) |
| [Sessions and clean-up](plan/21-sessions-and-cleanup.md) | §25 | Rename and delete a session, auto-name an untitled one with the model the admin picks (Admin › AI agents); delete takes on Create and failed or stopped runs (a design for the owner to approve, not in a milestone) |
| [Thumbnails](plan/22-thumbnails.md) | §26 | One WebP thumbnail per image and video take (short side 512 px) made where the take is made or by a runner thumbs job, a backfill, the sweep removing it; lists load it, viewers the full file (a design for the owner to decide, not in a milestone) |
| [Timeline tools](plan/23-timeline-tools.md) | §27 | Editing tools on the Timeline (Select, Trim with ripple / roll / slip / slide, Blade, Range, Overwrite), snapping, J/K/L, zoom from the whole sequence down to single frames with a filmstrip and waveform that grow with it, five UI options on the TimelineTools board (option B; built 2026-11-23: the ops, linked clips and markers in API 1.50, the tools, zoom, J/K/L, filmstrips and waveforms; not in a milestone) |
| [Timeline edit API](plan/24-timeline-edit-api.md) | §28 | `POST …/timelines/{id}/edits`: named ops applied by studio with dry run, diff and per-op errors on a required revision; revision history and restore; an agent view and frames at a time; `show_timeline` / `try_timeline_edit` / `edit_timeline` / `restore_timeline` tools for helm mcp and the CLI; the web editor saves its ops through it (first slice, today's ops, built 2026-11-23) |

## Guides

| Guide | For |
|---|---|
| [helm mcp](guides/mcp.md) | Using helmstudio from Claude Code or Claude Desktop: set-up, the tools, which ones ask first |
