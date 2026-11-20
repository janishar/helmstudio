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
