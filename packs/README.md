# Packs: the model API of runner_service

A **pack** teaches the runner one model family. It is a folder `runner_service/packs/<name>/` (`<name>` is a
Python identifier, e.g. `qwen_image`) holding:

| File | What |
|---|---|
| `model.yaml` | the model: variants, weight components, stages (node kinds) and their params, ports, memory |
| `<runtime>.py` | one module per runtime named in `runtimes` (`torch.py`, `mlx.py`; the fake echo pack has `fake.py`): one function per stage, plus `build()` / `rename()` (and on MLX `convert()`) for weight components |
| `workflows/*.json` | the workflows the runner publishes, one registry `workflow` object per file |
| `looks/*.yaml` | (optional) starter looks, shown to every user read-only (below) |
| `GOTCHAS.md` | (optional) hard-won notes |

The runner (`RUNNER_ENV`) loads every pack with a runtime whose `env` equals it: `fake` → `echo`, `torch` → the
torch packs (uv project `runner_service/`, extra `torch`), `mlx` → the mlx packs (`ltx`; uv project
`runner_service/envs/mlx`, its own lockfile). A runner process imports only its own runtime: the core imports neither
torch nor mlx (`tests/test_mlx.py`). From each pack the core builds the registry values `<host>.<env>.{nodes,workflows,models}`
(`core/pack.py`, `core/registry.py`), plus `.loras` (the host's shared LoRA index) and `.looks` (the packs' starter
looks, `core/looks.py`); they are checked against `contracts/registry.schema.json` and every
workflow against the node schemas at start, so a broken pack stops the runner instead of reaching studio.
`runner_service/tests/test_packs.py` fails when a pack's values drift from the canonical contract fixtures
(`contracts/fixtures/registry/<family>.{nodes,workflows}.valid.json`).

The core also provides five nodes in the same grammar (`runner_service/core/nodes.yaml`), published when a
workflow uses them or a pack lists them in `publish`: `prompt` (→ `text: Text`), `seed` (→ `seed: Seed`), `canvas`
(`mode` auto | fixed, `width`, `height` → `canvas: Canvas`), `image` (param `image: image` → `image: Image`),
`output` (`media: Media` in, an Image or a Video; stores it as the run's next output: a PNG, or an MP4 with its
poster PNG) and `answer` (`text: Text` in, no outputs: the sink of a text workflow, below).

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
lora:                             # LoRAs: the generated qwen-image/lora-stack node and the registry lora block
  component: transformer          # the component LoRAs patch; only stages that use it see the Model's LoRA set
  renames: []                     # {from, to} prefix rewrites of a key's module part (most families: none)
  ignore: []                      # fnmatch globs over raw keys counted as ignored, not unapplied
  modes: [adapter, bake]          # [bake] for a runtime without adapters yet
  max_rows: 8
  strength: {min: -4, max: 4, default: 1}
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
  ask:                            # an ask stage (API 1.14): the text encoder is a vision-language model
    title: Ask
    help: "Asks Qwen Image's own text encoder (Qwen3-VL 8B) one question about its takes: no extra load when it is resident. Temperature 0, thinking off, one more try in the same run when the answer does not match the schema."
    uses: [text_encoder]          # the same residency entry as encode: after a still it loads nothing
    inputs: {model: Model}
    outputs: {answer: Text}
    params:
      system: {type: string, label: System, default: "", max: 4000, help: The system turn.}
      prompt: {type: string, label: Prompt, default: "", max: 32768,
               help: "The filled template (pipelines writes it). Not scanned for mentions."}
      media: {type: rows, label: Takes, default: [], min: 0, max: 8, item: {take: Media},
              help: "Image or video takes, in order; each is sent as a thumbnail (a video as 3 keyframes)."}
      schema: {type: string, label: Answer schema, default: "", max: 16384,
               help: "A JSON schema the answer must match; empty = plain text."}
      max_tokens: {type: int, label: Max tokens, default: 800, min: 16, max: 4096}
    peak_gb: "2.5 + 1.5 * images"
```

Keys the core understands; anything else is ignored:

- **Top level**: `id`, `family`, `title`, `publish` (core node kinds to publish without a workflow using them),
  `runtimes` (`<runtime>: {env}`), `model`, `variants`, `components`, `lora`, `stages`. A pack without `variants` (echo) has no model node and its `models` entry is `{id, family, title}`.
- **`variants.<id>`**: `dir`, `shares` (component names loaded from the first variant's folder, so they are one
  residency entry and one cache identity across variants).
- **`components.<name>`**: `files` (a glob, or a list of globs: one component may take keys from several files),
  `dtype` (`bf16` | `fp16` | `fp32`, default bf16; `stored` keeps each tensor's own dtype, MLX), `precision` (default
  `[bf16]`; `int8`, and `int4` on MLX), `quantize` (fnmatch globs over module names, used only at a quantized
  precision). Only the keys the module's `rename()` keeps count towards the component's size.
- **`lora`** (optional; contracts/README.md "LoRAs", "LoRA normaliser"): `component` (the component LoRAs patch),
  `renames` (ordered `{from, to[, at: end]}` rewrites of a key's module part: a prefix, or with `at: end` a suffix,
  as ltx's `.to_out.0` → `.to_out`), `ignore` (fnmatch globs over raw keys counted as ignored), `modes`
  (`[adapter, bake]`, or `[bake]` for a runtime without adapters: MLX), `max_rows` (8), `strength {min, max,
  default}`, optional `help` / `rows_help` / `mode_help` (the stack node's texts) and `prefused` (`[{file, variant}]`:
  a LoRA already fused into a variant, `file` inside its folder; published with the shared index's id, none when the
  file is not on the host). The core generates the `<family>/lora-stack` node (`Model → Model`, param `rows` of
  `{lora: Lora, strength, mode}`) and the registry `models[].lora` block, whose `modules` are the component's Linear
  names read from `build()` on the meta device (no weights), compressed to `{a..b}` ranges. A stage whose `uses`
  holds the component gets it from `ctx.component` with exactly its Model's adapters attached (strengths set; no
  reload) and its bake rows merged at load (its own residency entry); only those stages' cache keys see the LoRA
  set. A key that does not apply fails the run with the contract's text: no key is silently dropped.
- **`model`**: `help`, and `variant` / `precision`, merged over the generated params (`label`, `help`, `default`).
  The generated node is `<family>/model` with param `variant` (enum: the variant ids, default the first) and
  `precision` (enum: every precision a component offers, in the order bf16, fp16, int8, int4; `model.precision.enum`
  may reorder it), output `model: Model`. A model pack's source stages (no inputs: `ltx/canvas`) come before it.
- **`stages.<name>`**: `title`, `help`, `params`, `inputs`, `outputs`, `uses`, `unload_after` (components of `uses`
  unloaded as soon as the stage has run: ltx's Gemma never sits beside its DiT), `peak_gb`, `stall_s`.
  - `params`: name → a registry `paramSchema` without `name`, copied verbatim (`type` int | float | bool |
    string | text | enum | seed | image | rows | frames (a frame count on `min + k × step`, `fps` naming the node's
    frame-rate param; `auto {min_s, max_s}` allows the value `"auto"`: the pack resolves it, `core.graph.auto_frames`
    puts seconds on the grid, and the run reports the stored clip's frames as `resolved.params`); `label`,
    `default`, `min`, `max`, `step`, `enum`, `help`, `when`, for int `values` (the only values allowed) and
    `values_by {param, values: {<enum value>: [...]}}` (the subset for the node's value of that enum param), and for
    rows `item` (field → `Image`, `Lora` or a param schema; a float field may say `position: seconds`, a time on the
    clip) and `mention` (`<image{n}>`)). Order is kept. Item fields left out of a row take their defaults; a rows
    param with an `Image` field may be `[]` in a workflow, and a run needs its `min` items.
    **YAML 1.1 reads bare `on`, `off`, `yes`, `no` as booleans: quote them.**
  - `inputs` / `outputs`: port name → port type, in order. Types: `Text`, `Image`, `Int`, `Float`, `Seed`,
    `Canvas`, `Duration`, `Video` (plain values; `Media` is an input that takes Image or Video) and the
    family-scoped handles `Model`, `Conditioning`, `Latent`. An input that may be
    left unwired is `name: {type: Image, optional: true}`. Param and input names are disjoint, and no param is named
    `bypass` (the runner refuses to start otherwise).
  - `title` and `help` are what the Graph view's palette and node cards show (the core publishes them as is).
  - `peak_gb`: a Python expression over `mp` (megapixels of the nearest upstream Canvas, 0 without one),
    `images` (how many Image values the params of the stage and of every node upstream of it hold), `mp_in`
    (megapixels of the largest of them, 0 without), `frames` (the length an input carries as an int `frames`
    attribute, such as a Latent after an auto length resolved; else the `frames` param of the nearest upstream node
    that has one, `"auto"` counting as its longest clip, 0 without) and the
    stage's own params: numbers, arithmetic, comparisons, `and`/`or`/`not`, `a if cond else b`, `min()`,
    `max()`. Anything else fails at start. The value is GB the stage holds at its peak above the components resident
    after it, as the runner measures memory (the process footprint: device buffers and the allocators' caches
    included, not only the framework's active count); fit it from measured runs at two sizes or more.

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
- **`inputs`**: port → upstream value; an unwired optional input has no key. A value param (`int`, `float`, `seed`,
  `string`, `text`, `image`) wired from upstream in a user's graph arrives in `params` with the upstream value (an
  `image` param wired from an `image` node: a `PIL.Image`), so a stage reads params the same way either way.
  Port values: `Text`: `str`. `Seed`: `int`. `Canvas`: `{"width", "height", "mode"}`
  (the stage applies `mode`: auto may size from the references; ltx's has no mode). `Duration`: `{"frames", "fps"}`.
  `Image`: a `PIL.Image` (the core `output` node stores it as PNG; RGBA is fine). `Video`: the encoded clip
  `{"mp4": bytes, "poster": PIL.Image (frame 0, the video's size), "frames", "fps", "has_audio"}`; the `output` node
  stores `<prefix><n>.poster.png`, then `<prefix><n>.mp4` (H.264 + AAC, `+faststart`), and reports
  `video {duration_s, fps, frames, has_audio, poster}`. `Model`: a `core.pack.ModelRef`
  (`variant`, `precision`, `loras` (the ordered LoRA rows of the stacks upstream), `pack.spec`, `folder(component=None)` → the variant's folder for configs, the
  tokenizer and the scheduler, which the pack reads itself). `Conditioning` / `Latent`: whatever object the
  pack returns. Handles never leave the process.
- **Graphs**: a run may be a user's own graph (contracts/README.md, "Graph rules"). The core checks it before it
  starts (known kinds, params, typed wires, required inputs, no cycle, an output, at most 64 nodes) and fails the
  run naming the node; it runs only what an `output` node needs, a kind may appear several times, and each `output`
  node stores its own take (index = the order outputs are stored). A stage only sees its params and inputs.
- **Results are cached** (the 24 most recently used, across nodes and owners) by a key over the owner, kind, pack
  code hash (model.yaml + the module), params (a wired one: its upstream instead), each upstream node's key and
  output name (never node ids: a renamed node, or two identical nodes in one graph, share a result), and for a Model
  input only the files (size, mtime) and precision of the components the stage `uses`. A new seed re-runs sample and decode, not encode; base → turbo re-runs sample
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
    For the family's `lora.component`, the core then makes the module carry exactly the Model's adapter LoRAs
    (`core/loaders_torch.py` LoraLinear: `y = base(x) + s · B(A x)`, `s` a tensor; detaching the last restores
    the plain Linear), fetching an own LoRA to the host's LoRA cache first (phase `fetching LoRA <name>`).
  - `ctx.check()`: a cancel point. Call it at least once per step; a stage that passes none for `stall_s`
    seconds fails its run and restarts the runner.
  - `ctx.progress(step, of, phase=None)`: `1 <= step <= of`; a phase with no steps is `progress(1, 1, phase)`.
    Use the phases `encoding`, `sampling`, `decoding` (the core sends `loading <component>`).
  - `ctx.device`, `ctx.owner`, `ctx.seed`, `ctx.node` (the node id).
  - Fail a run with a readable message: `raise core.graph.RunError("empty prompt")`. Any other exception
    fails it as `<node>: internal error (<Type>)` and is logged with its traceback.
- **Memory**: before a stage runs, admission needs its not-yet-loaded `uses` components (their size computed from
  the safetensors headers at the target dtype) plus `peak_gb`; when short, cached node results are dropped,
  then components: first those the rest of the run does not need, within each group a baked LoRA set the run does
  not use before plain ones, then least recently used. The budget is RAM minus `RUNNER_RESERVE_GB` (12), capped
  for one run by its command's `budget_gb` (what the host's other env leaves: contracts/README.md "Two envs on one
  host: the hand-over"). After a run that used a baked set, the plain copy of that component is unloaded (unless
  pinned or used too), so the two never stay resident together. The hosts key lists every entry with `gb`,
  `last_used`, `pinned` and a baked set's `loras`; the `models` key carries each present variant's `memory` plan per
  precision, computed from the pack's workflows at their default sizes (components in order of last use,
  `unloads_after` from `unload_after`, `peak_gb` = max over stages of kept components + the stage's own + its
  `peak_gb`), so write `peak_gb` for the default sizes honestly: studio admits runs across envs with it.
  `runner.unload` naming components unloads exactly those (never a RUNNER_PIN one). After a run `RUNNER_AFTER_RUN` = `keep_warm` (default; idle unload after
  `RUNNER_IDLE_UNLOAD_S`), `unload`, or `keep_pinned` (`RUNNER_PIN=transformer,vae`). Eviction drops the
  module, runs `gc` and `torch.mps.empty_cache()` (synchronised first). A pack that needs an `auto` decision
  (tiling) decides it inside the stage; its `peak_gb` should then describe the larger case.

## Ask stages and text workflows (API 1.14; contracts/README.md "The llm pack and agent nodes")

A judge, write or decide node asks a vision-language model through a **text workflow** (`"media": "text"`):
`<family>/model → <family>/ask → answer`. Three packs have one: `llm` (`llm-ask`, env mlx: Qwen3.8-27B-4bit on
mlx-vlm, the mlx env's `llm` extra), `qwen_image` (`qwen-image-ask`: its text encoder, Qwen3-VL 8B, `uses:
[text_encoder]`, so after a still it loads nothing) and the fake `echo` (`echo-ask`). LTX has none: its Gemma
encoder has no vision blocks. Name text workflow files `text-<id>.json` (they sort after the media ones).

- **The stage** is one call: `core.ask.ask(ctx, params, generate)` with the runtime's `generate(prompt,
  thumbnails) -> (text, tokens_in, tokens_out)`, greedy, seed `core.ask.SEED` (0), thinking off. The core does
  the rest, the same for every runtime: thumbnails of the `media` takes (an image: over white, long edge
  min(1024, native), sides floored to 32, JPEG q90, PUT once to `u/<owner_id>/llm/thumbs/<sha256>.jpg`; a video:
  3 keyframes at ⌊F·(2k+1)/6⌋ with ffprobe/ffmpeg, each as an image; at most 24), the schema appended to the
  prompt, the outermost `{…}` parsed and validated with jsonschema, one more try with `Your answer was invalid:
  <error>. Answer again with JSON only.`, then `answer did not match the schema: <error>` fails the run. Where the
  library can, the runtime also constrains decoding to the schema (mlx: llguidance through mlx-vlm).
- **`Media`** (a rows item ref kind): an image or a video take, resolved like an Image value; the stage gets
  `{"take_id", "image": PIL image}` or `{"take_id", "mp4": bytes}` (read and checked by the core, not decoded).
  `peak_gb`'s `images` counts each video as its 3 keyframes.
- **The answer** is not a take: the stage returns a `core.ask.Answer` (the text, carrying the record `{tries, seen,
  tokens_in, tokens_out, seconds}`), the `answer` node sets it on the run, and the terminal `evt.run.status` carries
  it (also on a schema failure). A repeated ask (same prompt, schema, takes `(owner, key, etag)`, model files) is a
  node-cache hit, record included. Templates for judge/write/decide (filled by pipelines) ship as
  `packs/llm/prompts/*.txt`.
- **Memory plans**: each text workflow gets its own `memory` item (`workflow`, planned at 4 thumbnails of 1024 px:
  `planned_at.images`); a pack with only text workflows (llm) has one item without `workflow`.
- **A pack whose library loads its own checkpoint format** (mlx-vlm) defines `load_component(component, model,
  ctx)` in its module: `core.loaders_mlx` calls it instead of build/rename/quantize (the residency manager, the
  phase `loading <component>` and the size from the headers stay the core's; packed `U32` weights count 4 bytes).
- The fake `echo/ask` answers the first JSON value after the last `ECHO-ANSWER:` in its prompt (a JSON string: its
  text), else `{}` with a schema and `echo` without; its retry answers the one after `ECHO-RETRY:` when present.
  An agent turn (API 1.15): when the prompt's last `This is turn <n> of` line has a matching `ECHO-TURN <n>:`, it
  answers the JSON after that marker instead, so a test scripts a whole tool loop turn by turn.
- **Tool calling** (API 1.15, contracts/README.md "Tool calling on the ask contract"): no ask kind changes. An agent's
  turn rides in `schema` (the canonical turn schema `{say, calls ≤ 4 × {tool, args}}`, `calls: []` = the final
  answer), so the tool set is in the node cache key like any schema and the caps (4 calls, `say` ≤ 4000, args) are
  checked by the schema validation and its one retry. Every ask stage supports it: `llm/ask` constrains decoding
  with llguidance, dropping only the keywords llguidance does not implement (`propertyNames` on `rows_add` /
  `rows_edit` inputs) from the grammar, never from the check; `qwen-image/ask` (no constrained decoding) by the
  schema in the prompt and the check. Tools are not rendered through the model's own tool-calling chat template:
  the turn JSON is the answer. Native OpenAI tools belong to `server-ask` (pipelines_service), not a pack.

## workflows/*.json

A registry `workflow` object (`id`, `title`, `family`, `env`, `nodes`), every param given; for the canonical
ones, copy the workflow from `contracts/fixtures/registry/<family>.workflows.valid.json` verbatim.

## looks/*.yaml

Starter looks (contracts/README.md "Looks"): each file is a list; files are read in name order, looks in file order.

```yaml
# an example of runner_service/packs/<pack>/looks/<any>.yaml, every key shown
- id: window-light            # ^[a-z0-9][a-z0-9-]{0,63}$, unique in the family; published as <family>/<id>
  group: lighting             # film | camera | colour | lighting
  name: Window                # 1-80 characters
  text: "soft daylight from a single window, deep falloff into shadow"   # <= 2000; default ""
  version: 1                  # >= 1, default 1: bump it when the look changes (reuse then says `changed`)
  lora: {file: window.safetensors, strength: 0.8}   # optional; a file name (no path), strength -4..4 (default 1)
  image: window-light.webp    # optional thumbnail next to the YAML: PNG, JPEG or WebP, at most 2 MB
```

- A look needs non-empty `text` or a `lora`; at most 200 looks per pack; no other keys.
- At load the runner checks every file. A broken one (schema, duplicate id, a missing or undecodable image) stops
  the runner like a broken pack.
- `lora.file` is looked up in the shared LoRA index: `<models>/loras/` and the `loras/` folder of each of the family's
  model folders. A file it does not find is still published, with `lora_id: null`, and logged; studio then takes
  another live host's id, or greys the look out (`LoRA <name> is on no live host`). A file of another family keeps its
  id (logged); studio greys the look out with `LoRA <name> is for <family>` (or `fits no known model`).
- `image` is only a thumbnail, never a reference image. At start it is PUT once to
  `packs/<family>/looks/<id>-<sha256[:16]>.<ext>`; an existing key is never overwritten.
- The runner publishes `<host>.<env>.looks` (an empty list when no pack ships looks). It needs no look logic at run
  time: studio's resolver has already put the text in the prompt, the images in the reference rows and the LoRAs in
  the stack rows of the graph it sends.
- The echo pack's looks are `contracts/fixtures/registry/looks.valid.json` exactly (`tests/test_looks.py`).
- Write short, concrete phrases: no brand or film-stock trademarks, no living persons.

## MLX packs (`mlx.py`)

The same contract as `torch.py`, on `core/loaders_mlx.py`: `build(component, model)` returns the `mlx.nn` module
(its arrays stay lazy, never evaluated); the loader reads the files with `mx.load` (lazy), maps each key through
`rename(component, key)` (None skips it), then the optional `convert(component, name, value)` (conv weights to
channels-last), casts only for an explicit `dtype`, quantizes the matching 2-D `.weight`s one at a time
(`mx.quantize`, group 64, after the kept tensors are evaluated), applies `nn.quantize` to exactly those Linears and
loads with `strict=True`. Phase `loading <component>` (underscores as spaces) is reported under the node that needs
it. Memory: the process footprint; eviction runs `gc` + `mx.clear_cache()`; the Metal cache is capped at 2 GB
(`CACHE_GB`: at the default one VAE decode kept 27 GB of freed buffers).
A stage that must refuse a wiring before anything runs defines `check_<stage>(params, wired) -> str | None` (the
text after the node id, e.g. `": stage 2 needs latent, not canvas or duration"`). LoRA on MLX is bake only
(`modes: [bake]`): the rows are normalised like torch's, then merged after quantization module by module
(dequantize, + float32 delta, requantize; a float Linear: W.f32 + delta cast back), phase `baking LoRAs` after
`loading <component>`; a baked component is its own residency entry. There are no MLX adapters.

## Checking a pack

`cd runner_service && uv sync --extra torch && uv run pytest -q` (test_packs compares the generated values with
the fixtures); `uv run pytest -m gpu` runs the tests that load real weights. The mlx env:
`uv sync --project envs/mlx && uv run --project envs/mlx pytest -q`; the ltx goldens:
`uv run --project envs/mlx python tests/test_ltx_golden.py [case ...] [--reseed]` (stop both runners first).
