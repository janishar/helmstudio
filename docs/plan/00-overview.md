# helmstudio-next plan — Overview

[← Plan index](../README.md)

## 1. Decisions already made

| Question | Decision |
|---|---|
| Granularity | Coarse, typed stage nodes (6–10 per model family). Multi-step work across models is a pipeline: a LangGraph graph of steps, gates and agent nodes joined by takes and decisions (sections 7.2, 16). |
| Native C engines (iris.c, h3c) | Dropped. MiniMax H3 video returns later through PyTorch or MLX. |
| Mac runtime | Both MLX and PyTorch MPS; each model config declares which runtimes implement it. MLX is preferred when a model has it. |
| Weight loading | One read straight into GPU memory, never a CPU model copy, official files unmodified (section 6.3). |
| Model memory | Per GPU host, set by the admin: after the queue drains, unload everything, keep models warm, or keep only pinned components (section 6.2). |
| Architecture | Microservices after the `oralab-catalog-agents` reference (section 17): Kong, Go services on helmgo (auth, studio, files), Python services (LangGraph pipelines, model runners), Next.js web with a BFF, Postgres, NATS JetStream, an S3-compatible object store. |
| Users | Multi-user and self-hosted: accounts, roles, every row and file owned by a user; GPUs shared fairly (section 17.3, 17.5). |
| Where it runs | Docker Compose for everything except the model runners, which run natively on Apple Silicon. Weights stay on the host's disk, never in an image. |
| First models | Qwen Image 2.1 (PyTorch MPS/CUDA) and LTX-2.5 (MLX), plus an image→video pipeline joining them. |
| UI | Fresh design (section 8), keeping the proven ideas from the current studios. |
| Navigation | Create (interactive: one image or clip while you watch), Pipelines (unattended: batches and saved recipes), Gallery, Timeline (editorial), Models (the catalog and LoRAs for everyone; weight management for admins). Admins also get Hosts and Users (section 8.1). |
| Sharing and reuse | The Gallery is the hub: everything made lands there, everything consumed is picked from there (section 9.1). |
| Pipeline engine | LangGraph. Pipelines are agent-powered graphs: nodes generate, judge, write, decide, remember and talk to LLM agents (section 16). Workflows stay typed generation graphs. |
| Directing shots | Lives in the Timeline, not in Create (section 15.3). Elements and Looks work in both (section 14). |
| Platform features kept | Gallery with lineage (per user), weights manager (admin, per host), LoRA library (shared plus each user's uploads), timeline and export, sessions, the queue (now the dispatcher, section 17.5). |
| Removed entirely | Manifests, registry, approval digest, capabilities, per-studio tokens, network allowlists, the three SDKs, the `/helm/` proxy, iframe embedding, clone/build/venv-per-studio install, handoff/inbox, stdout-marker scraping, one-subprocess-per-render. |

## 2. What the current code taught us

- Roughly 85% of every studio is the same shell copied by hand (server, queue,
  SSE, sessions, takes, refs, @-mentions, sizing widgets). The model-specific
  part is 300–500 lines per studio.
- Roughly 45% of the Go daemon exists only to host untrusted independent repos.
- Every render today reloads the model (process per render). Keeping models
  warm is the biggest speed win available.
- Memory discipline is hand-sequenced in every studio ("free the text encoder
  before the transformer", "free the DiT before decode"). It becomes one
  scheduler-owned residency manager.
- LoRA exists only in LTX and Wan, and only as merge-at-load, which de-quantizes
  int8 layers (Wan: a 28 GB regression per expert).
- No studio loads weights without a copy. MLX `mx.load` and safetensors ≥ 0.8
  (`device="mps"`) read each tensor once into a GPU buffer; true mmap no-copy
  needs page-aligned files (only h3c does it; faster on M5, slower on M3).
- The seeds of the new design already exist: LTX's `tasks.js` catalog (fields,
  rows, `when`, `requires`), LTX's `utils/blocks.py` load/encode/free blocks,
  diffusers' modular blocks for Qwen, helmstudio's JSON-Schema form generator,
  the gallery's `item_inputs` lineage graph, iris's memory-fit check.

## 3. Principles

1. **The system owns its models.** Inference runs in runner processes the
   system starts, on hosts it knows; the only exceptions are the LLM endpoints a
   pipeline opts into (section 16.4).
2. **The graph is the truth; the Studio is a projection of it.** One document,
   two views, no drift.
3. **Config describes models; code implements stages.** Adding a checkpoint,
   variant, precision or LoRA rule is a YAML edit.
4. **Wires are typed per model family.** A graph that cannot run cannot be built.
5. **Memory belongs to the scheduler.** Nodes ask for components; they never
   load, free or sequence them.
6. **Contracts first, services isolated.** Everything that crosses a service
   boundary is defined in `contracts/` before it is built; services share no
   code and no database (section 17.8).
7. **Every user sees only their own data.** Ownership is enforced in the
   database and the object keys, not in handlers (section 17.3). There is still
   no per-node permission or approval machinery.
