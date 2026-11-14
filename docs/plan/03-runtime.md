# helmstudio-next plan — Runtime architecture

[← Plan index](../README.md)

## 6. Inside a runner

The services around the runner — APIs, queue, dispatcher, messaging — are
section 17. This section is what a `runner_service` process does with a run.

- One long-lived process per **env** (`mlx`, `torch`; a uv project under
  `runner_service/envs/`) on each GPU host. A pack names its env, so a second
  torch env for a model with clashing pins is a new folder, not a redesign.
  Separate processes also keep MLX's and torch's Metal allocators apart.
- **Inside a runner** nodes pass Python handles to live `mx.array` /
  `torch.Tensor` objects. **Between runs** only media and values travel, as
  takes in the object store.
- Inference runs off the event loop, so heartbeats, cancel and progress keep
  flowing while the GPU is busy.
- Media is mirrored from the object store into a per-run work folder and the
  results are uploaded when a node succeeds; weights are read from the host's
  own disk.
- **Headless**: `helm run workflow.json --set sample.seed=7` is an API client.
  It sends the CLI's API key and the token of the user signed in with
  `helm login`, and submits the same run through studio_service as that user.
- **Stall limit**: the runner keeps its heartbeat and host key alive only
  while the GPU thread has passed a cancel point within the stage's stall
  limit (ten minutes by default, set per stage in the pack). Past it, the run
  is reported as stalled and the worker restarts.

### 6.1 Execution

- Pull-based: only ancestors of the requested Output run. "Run to here" works
  on any node.
- Cache key = hash(merged node schema, pack code hash, parameters, upstream
  hashes). A `Model` input is hashed **per component the stage uses**
  (`uses:` in the YAML), so a LoRA change on the transformer does not re-run
  the text encoder, and switching Base → Turbo does not re-encode.
- Consequences: a new seed re-runs Sample and Decode; a LoRA strength change
  re-runs Sample and Decode; nothing reloads. (Qwen's Enhance has its own seed,
  defaulting to the workflow seed, so it re-runs only if you ask it to.)
- Media and value results are cached by hash as objects under the owner's
  prefix; the janitor removes cache objects not used for two weeks. Cache keys
  include the owner. Handle results are kept
  one per node per owner (the latest), materialised at the node boundary (`mx.eval`; torch
  views cloned) so they cannot pin evicted weights, and counted by the
  residency manager, which evicts them first.
- **Queue**: owned by the dispatcher (section 17.5). When an interactive run
  would evict a batch's models, Generate says so before it is pressed
  ("pauses batch, reloads ≈ N GB").
- **Cancel** is cooperative through `ctx.check()`, called by `ctx.steps`, by the
  loader per tensor, by text generation, and per decode tile. Killing the worker
  is the escalation after a grace period, and the UI says which warm models
  were lost.
- ETA comes from the history of matching node runs.

### 6.2 Residency manager

- **Entries**: every component, keyed by (file set, precision, quantisation,
  baked LoRAs). Size is computed from the target dtype, not header bytes.
- **Budget**: physical RAM minus a configurable reserve, checked against live
  system memory pressure. Each worker reports its true footprint after every
  node (process footprint; MLX active + cache; torch MPS *driver* allocation,
  since torch's own counter does not see safetensors' buffers).
- **Admission**: before a node runs, need = components + the stage's `peak_gb`
  formula, corrected by the measured peak of earlier matching runs. If short,
  evict cached handles, then least-recently-used components the rest of this
  run does not need. "Free the text encoder after encode" and "free the DiT
  before decode" happen by themselves, and only when memory requires it.
- `auto` parameters that trade memory for pixels (decode tiling) are resolved
  here and recorded in the take.
- A run that cannot fit even alone is refused in under a second, with numbers.
- Handing work from one worker to the other is a request/ack: the other worker evicts,
  reports its measured footprint, and only then is the next node admitted.
- Runtime hygiene lives in the worker loop: MLX `gc` + `mx.clear_cache()` after
  eviction; torch MPS `synchronize()` before `empty_cache()` (torch 2.14 GIL
  deadlock) and per step.

**What stays loaded after the queue drains** — one setting per host, set by
the admin:

| Setting | Effect |
|---|---|
| Unload everything | Worker processes are stopped. This is the only way to guarantee the memory goes back to the system. |
| Keep warm (default) | Components stay loaded until memory is needed or an idle timeout passes (default 10 minutes). |
| Keep pinned only | Pinned components stay; everything else is unloaded. |

**Pin** is a per-component setting of a host, set by the admin on the Hosts
screen: pin the transformer so re-rolls skip the expensive load, while the
text encoder and enhancer do not sit in memory. The Hosts screen lists every
resident component with its size and offers Unload and Unload all. A pin never
blocks a run: if a run needs a pinned component's memory (a pipeline step on
the other runtime, say), Generate says so before it starts ("unloads pinned
transformer, reloads ≈ N GB after").

Everyone sees the memory chip: used and total for the host their run will use,
and whether it is busy. Only admins can unload or pin.

### 6.3 Loading weights (direct to GPU)

One loader interface, `open(component) → (key, device tensor)…`, with a
per-tensor hook chain: rename → cast → LoRA bake (optional) → quantize.

| Runtime | v1 path |
|---|---|
| MLX | `mx.load` (lazy, one read into a Metal buffer) → quantize one tensor at a time → build model, `strict=True` |
| torch MPS | safetensors ≥ 0.8 `safe_open(device="mps", backend="pread")`, streamed into a meta-initialised model |
| torch CUDA | per-tensor pinned staging (one tensor of host memory at a time — a stated exception to "no CPU copy"); safetensors 0.9 `prefetch` straight to CUDA once it is final |
| later | `packed` backend: page-aligned cache + `newBufferWithBytesNoCopy` + DLPack, opt-in per model |

The int8 streaming loader that Qwen and MiniMax each copied becomes this one
primitive. Admission adds the load-time transient (largest tensor in fp32).

### 6.4 LoRA

- **Library**: shared LoRAs live under `<models>/loras/` and any `loras/`
  folder inside a model folder, indexed on the host. A user's own LoRAs are
  uploads under their prefix, indexed when uploaded and fetched to the host's
  LoRA cache on first use. Both are indexed from safetensors headers (rank,
  size, detected family, licence metadata). The picker shows the shared ones
  plus the user's own, and only those that fit the connected model's family.
- **Normaliser**: one module turns PEFT/diffusers, kohya and ComfyUI keys into
  canonical `module → (A, B, alpha)`; family-specific renames come from the YAML.
  It reports how many keys were applied, and zero applied is an error.
- **Mode is chosen per LoRA**:
  - *Adapter* (`y = Wx + s·B(Ax)`) for ordinary LoRAs: works on quantized
    bases, strength changes and swaps do not reload the model.
  - *Bake* (merged before quantisation, during load) for very large LoRAs or
    when the user asks. A baked model is its own residency entry.
  - A model may name a pre-fused file for a known LoRA (LTX's distilled
    LoRA is rank 450 and 7.6 GB; at strength 1 the pre-fused transformer is used).
- One adapter wrapper per runtime, covering plain and quantized linears, with
  the scale held as tensor state so compiled graphs see changes. The adapter on
  MLX quantized layers is new code; until it lands a family may declare
  bake-only, and two Model wires with different stacks are then two entries.
- **LoRA Stack node**: `Model → Model`, a rows parameter of (LoRA, strength,
  slot). Slots are declared where a family has them (Wan: high/low expert).

### 6.5 Failure

- A runner that dies or stalls mid-run is handled by the dispatcher
  (section 17.5); it restarts under its supervisor and re-registers.
- A run killed for memory is reported as such, with the numbers.
- Outputs are written straight to the keys studio assigned; a take row points
  at one only after the run's final event. The janitor deletes objects with no
  row after a day.
- If studio, NATS or the object store is down, the runner keeps its models
  warm and reconnects.

### 6.6 Versions

- Saved workflows and takes record `schema: 1` and each pack's code hash.
- A document with unknown or removed parameters still opens; the node shows a
  warning and keeps the values. A pack adds a `migrate()` only when a real
  rename happens.
