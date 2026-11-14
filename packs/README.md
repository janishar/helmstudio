# Packs: the model API of runner_service

A **pack** teaches the runner one model family. It is a folder `runner_service/packs/<name>/` (`<name>` is a
Python identifier, e.g. `qwen_image`) holding:

| File | What |
|---|---|
| `model.yaml` | the model: variants, weight components, stages (node kinds) and their params, ports, memory |
| `<runtime>.py` | one module per runtime named in `runtimes` (`torch.py`; the fake echo pack has `fake.py`): one function per stage, plus `build()` / `rename()` for weight components |
| `workflows/*.json` | the workflows the runner publishes, one registry `workflow` object per file |
| `GOTCHAS.md` | (optional) hard-won notes |

The runner (`RUNNER_ENV`) loads every pack with a runtime whose `env` equals it: `fake` → `echo`, `torch` → the
torch packs. From each pack the core builds the registry values `<host>.<env>.{nodes,workflows,models}`
(`core/pack.py`, `core/registry.py`); they are checked against `contracts/registry.schema.json` and every
workflow against the node schemas at start, so a broken pack stops the runner instead of reaching studio.
`runner_service/tests/test_packs.py` fails when a pack's values drift from the canonical contract fixtures
(`contracts/fixtures/registry/<family>.{nodes,workflows}.valid.json`).

The core also provides five nodes in the same grammar (`runner_service/core/nodes.yaml`), published when a
workflow uses them or a pack lists them in `publish`: `prompt` (→ `text: Text`), `seed` (→ `seed: Seed`), `canvas`
(`mode` auto | fixed, `width`, `height` → `canvas: Canvas`), `image` (param `image: image` → `image: Image`) and
`output` (`media: Image` in; stores it as the run's next output).

## model.yaml

```yaml
# runner_service/packs/qwen_image/model.yaml — generates exactly the canonical qwen-image node schemas
id: qwen-image-2.1                # the model id (registry `models`, residency keys)
family: qwen-image                # node kinds are <family>/<stage>; the model node is <family>/model
title: Qwen Image 2.1             # the model's title and the model node's title
publish: [image]                  # core nodes to publish even when no workflow uses them (for graphs)
runtimes:
  torch: {env: torch}             # runtime key = module file (torch.py); env = the runner env that loads it
model:                            # the generated <family>/model node (see below)
  help: Which weights to load and at what precision.
  variant: {label: Variant, help: "Turbo: 8 fixed steps, much faster."}
  precision:
    label: Precision
    default: int8
    help: "int8 quantizes the transformer and text encoder at load: about half the memory."
variants:                         # first one = the default variant and the owner of shared components
  base: {dir: Qwen-Image-2.1}     # dir: a folder under HELMSTUDIO_MODELS (default ~/GenAI/models)
  turbo: {dir: Qwen-Image-2.1-Turbo, shares: [text_encoder, vae]}   # these load from the first variant's dir
components:                       # weight components the residency manager loads, evicts and reports
  transformer:
    files: "transformer/*.safetensors"   # glob inside the variant's dir
    dtype: bf16                          # floating tensors are cast to this (the VAE is stored fp32)
    precision: [bf16, int8]              # offered precisions; a component without the model's precision
    quantize: ["transformer_blocks.*"]   # loads at its first one. int8: Linears whose module name matches
  text_encoder:
    files: "text_encoder/*.safetensors"
    dtype: bf16
    precision: [bf16, int8]
    quantize: ["*.language_model.layers.*", "lm_head"]
  vae: {files: "vae/*.safetensors", dtype: bf16, precision: [bf16]}
stages:                           # one node kind each: <family>/<stage>; the function <stage>() in torch.py
  encode:
    title: Encode prompt
    help: Encodes the prompt and up to 10 reference images; with references the run is an edit.
    uses: [text_encoder, vae]     # the vae encodes the references          # the components it may ask for (admission, cache keys)
    inputs: {model: Model, prompt: Text}
    outputs: {cond: Conditioning}
    params:
      transparent: {type: bool, label: Transparent, default: false,
                    help: Ask for an RGBA image with a transparent background.}
      refs: {type: rows, label: References, default: [], min: 0, max: 10, item: {image: Image},
             mention: "<image{n}>",
             help: "Images to edit or combine, in order; the prompt names them <image1>, <image2>, …"}
    peak_gb: "2.5 + 1.5 * images"   # images: how many Image values reach the stage
  sample:
    title: Sample
    help: Denoises from the seed's noise.
    uses: [transformer]
    inputs: {model: Model, cond: Conditioning, canvas: Canvas, seed: Seed}
    outputs: {latent: Latent}
    params:
      steps: {type: int, label: Steps, default: 40, min: 1, max: 200, step: 1,
              when: "model.variant != 'turbo'",
              help: Turbo uses its own 8-step schedule and ignores this.}
    peak_gb: "0.9 + 5.1 * mp"     # placeholders: fit the real numbers from measured runs
    stall_s: 600                  # optional: seconds without a cancel point before the run is failed
  decode:
    title: Decode
    help: Turns the latent into an RGBA image with the VAE.
    uses: [vae]
    inputs: {model: Model, latent: Latent}
    outputs: {image: Image}
    params:
      tiled: {type: enum, label: Tiled decode, default: auto, enum: ["auto", "on", "off"],
              help: "Tiling cuts decode memory (27 GB vs 65 GB at 4 MP) but changes pixels; auto tiles only when needed."}
    peak_gb: "6 if tiled == 'on' else 8.5 * mp"
```

Keys the core understands; anything else is ignored:

- **Top level**: `id`, `family`, `title`, `publish` (core node kinds to publish without a workflow using them),
  `runtimes` (`<runtime>: {env}`), `model`, `variants`, `components`, `stages`. A pack without `variants` (echo) has no model node and its `models` entry is `{id, family, title}`.
- **`variants.<id>`**: `dir`, `shares` (component names loaded from the first variant's folder, so they are one
  residency entry and one cache identity across variants).
- **`components.<name>`**: `files`, `dtype` (`bf16` | `fp16` | `fp32`, default bf16), `precision` (default
  `[bf16]`), `quantize` (fnmatch globs over module names, used only at int8).
- **`model`**: `help`, and `variant` / `precision`, merged over the generated params (`label`, `help`, `default`).
  The generated node is `<family>/model` with param `variant` (enum: the variant ids, default the first) and
  `precision` (enum: every precision a component offers, in the order bf16, fp16, int8), output `model: Model`.
- **`stages.<name>`**: `title`, `help`, `params`, `inputs`, `outputs`, `uses`, `peak_gb`, `stall_s`.
  - `params`: name → a registry `paramSchema` without `name`, copied verbatim (`type` int | float | bool |
    string | text | enum | seed | image | rows; `label`, `default`, `min`, `max`, `step`, `enum`, `help`, `when`,
    and for rows `item` (field → `Image` or a param schema) and `mention` (`<image{n}>`)). Order is kept.
    **YAML 1.1 reads bare `on`, `off`, `yes`, `no` as booleans: quote them.**
  - `inputs` / `outputs`: port name → port type, in order. Types: `Text`, `Image`, `Int`, `Float`, `Seed`,
    `Canvas` (plain values) and the family-scoped handles `Model`, `Conditioning`, `Latent`.
  - `peak_gb`: a Python expression over `mp` (megapixels of the nearest upstream Canvas, 0 without one),
    `images` (how many Image values the params of the stage and of every node upstream of it hold) and the
    stage's own params: numbers, arithmetic, comparisons, `and`/`or`/`not`, `a if cond else b`, `min()`,
    `max()`. Anything else fails at start.

## The runtime module (`torch.py`)

One function per stage, named after it, plus the component hooks. All of them run on the runner's single GPU
thread, one at a time, off the event loop (heartbeats, cancel and progress keep flowing); the module is
imported only by its env, so it may import torch at the top.

```python
def encode(ctx, params: dict, inputs: dict) -> dict:     # returns {output port: value}
    te = ctx.component(inputs["model"], "text_encoder")   # a loaded nn.Module on ctx.device
    ...
    return {"cond": Cond(embeds.clone(), mask.clone())}

def sample(ctx, params, inputs):
    dit = ctx.component(inputs["model"], "transformer")
    for i in range(n):
        ctx.check()                                       # cancel point: raises; also feeds the stall watchdog
        ...
        ctx.progress(i + 1, n, "sampling")                # node, step, of, phase (+ an ETA) → evt.run.progress
    return {"latent": Latent(...)}

def build(component: str, model) -> torch.nn.Module:      # the component on the meta device, no weights
    with accelerate.init_empty_weights(include_buffers=False):
        return Transformer.from_config(Transformer.load_config(model.folder(component) / component))

def rename(component: str, key: str) -> str | None:     # optional: checkpoint key → module name, None = skip
    return key
```

- **`params`**: the node's params, resolved and validated against its schema (a `seed` param arrives as an
  int: null and -1 were replaced by a random 0..2^53-1, reported as the run's `resolved.seed` and each output's
  `seed`). An Image value (an `image` param, an Image field of a `rows` item) arrives as a `PIL.Image`: before the
  stage's first step the core reads the command's resolved value (`{take_id, key, …, bytes, etag}`) from the
  object store, only under `u/<owner_id>/`, at most 64 MB and exactly `bytes` long (else the run fails `input
  image <take_id> is gone`), decodes it with Pillow (PNG, JPEG or WebP) and applies its EXIF orientation. The
  mode is kept as decoded (RGB, RGBA, P, …); the bytes are dropped. A `rows` param is a list of dicts.
- **`inputs`**: port → upstream value. `Text`: `str`. `Seed`: `int`. `Canvas`: `{"width", "height", "mode"}`
  (the stage applies `mode`: auto may size from the references). `Image`: a `PIL.Image` (the core `output` node
  stores it as PNG; RGBA is fine). `Model`: a `core.pack.ModelRef`
  (`variant`, `precision`, `pack.spec`, `folder(component=None)` → the variant's folder for configs, the
  tokenizer and the scheduler, which the pack reads itself). `Conditioning` / `Latent`: whatever object the
  pack returns. Handles never leave the process.
- **Results are cached** per (owner, node id) by a key over the owner, kind, pack code hash (model.yaml + the
  module), params, upstream keys, and for a Model input only the files (size, mtime) and precision of the
  components the stage `uses`. A new seed re-runs sample and decode, not encode; base → turbo re-runs sample
  but not encode. An Image param counts as its resolved value, so `(owner, key, etag)`: a changed reference
  re-runs encode, an unchanged one is a hit and is not read again. So a handle must be materialised (`.clone()`, `.contiguous()`): never a view into weights, and
  it must not depend on anything outside `params` and `inputs`.
- **`ctx`**:
  - `ctx.component(model, name)`: the component loaded on `ctx.device` (`mps`, `cuda` or `cpu`) at
    `model.precision_of(name)`. `name` must be in the stage's `uses`. Not resident: the residency manager makes
    room (LRU eviction) and the core loader calls `build()`, then streams the safetensors straight to the GPU
    tensor by tensor through rename → cast to `dtype` → int8 quantize (`quantize` globs; an `Int8Linear` with an
    int8 weight and one bf16 scale per row), with a cancel point and `progress(n, total, "loading <name>")` as
    it goes. Do not keep components in module globals: the residency manager must be able to free them.
  - `ctx.check()`: a cancel point. Call it at least once per step; a stage that passes none for `stall_s`
    seconds fails its run and restarts the runner.
  - `ctx.progress(step, of, phase=None)`: `1 <= step <= of`; a phase with no steps is `progress(1, 1, phase)`.
    Use the phases `encoding`, `sampling`, `decoding` (the core sends `loading <component>`).
  - `ctx.device`, `ctx.owner`, `ctx.seed`, `ctx.node` (the node id).
  - Fail a run with a readable message: `raise core.graph.RunError("empty prompt")`. Any other exception
    fails it as `<node>: internal error (<Type>)` and is logged with its traceback.
- **Memory**: before a stage runs, admission needs its not-yet-loaded `uses` components (their size computed from
  the safetensors headers at the target dtype) plus `peak_gb`; when short, cached node results are dropped,
  then least recently used components (first those the rest of the run does not need). The budget is RAM minus
  `RUNNER_RESERVE_GB` (12); after a run `RUNNER_AFTER_RUN` = `keep_warm` (default; idle unload after
  `RUNNER_IDLE_UNLOAD_S`), `unload`, or `keep_pinned` (`RUNNER_PIN=transformer,vae`). Eviction drops the
  module, runs `gc` and `torch.mps.empty_cache()` (synchronised first). A pack that needs an `auto` decision
  (tiling) decides it inside the stage; its `peak_gb` should then describe the larger case.

## workflows/*.json

A registry `workflow` object (`id`, `title`, `family`, `env`, `nodes`), every param given; for the canonical
ones, copy the workflow from `contracts/fixtures/registry/<family>.workflows.valid.json` verbatim.

## Checking a pack

`cd runner_service && uv sync --extra torch && uv run pytest -q` (test_packs compares the generated values with
the fixtures); `uv run pytest -m gpu` runs the tests that load real weights.
