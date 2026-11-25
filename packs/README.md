# Packs: the model API of runner_service

A **pack** teaches the runner one model family. It is a folder `runner_service/packs/<name>/` (`<name>` is a
Python identifier, e.g. `qwen_image`) holding:

| File | What |
|---|---|
| `model.yaml` | the model: variants, weight components, stages (node kinds) and their params, ports, memory |
| `<runtime>.py` | one module per runtime named in `runtimes` (`torch.py`, `mlx.py`, `native.py`; the fake echo pack has `fake.py`): one function per stage, plus `build()` / `rename()` (and on MLX `convert()`) for weight components |
| `workflows/*.json` | the workflows the runner publishes, one registry `workflow` object per file |
| `looks/*.yaml` | (optional) starter looks, shown to every user read-only (below) |
| `voices/*.yaml` | (optional, API 1.30) starter voices: preset speakers, shown to every user read-only (below) |
| `GOTCHAS.md` | (optional) hard-won notes |

The runner (`RUNNER_ENV`) loads every pack with a runtime whose `env` equals it: `fake` → `echo`, `torch` → the
torch packs (uv project `runner_service/`, extra `torch`), `mlx` → the mlx packs (`ltx`; uv project
`runner_service/envs/mlx`, its own lockfile), `native` → the native packs (`minimax_h3`: a child process; uv project
`runner_service/` with no extra, below). A runner process imports only its own runtime: the core imports neither
torch nor mlx (`tests/test_mlx.py`). From each pack the core builds the registry values `<host>.<env>.{nodes,workflows,models}`
(`core/pack.py`, `core/registry.py`), plus `.loras` (the host's shared LoRA index), `.looks` (the packs' starter
looks, `core/looks.py`) and `.voices` (the packs' starter voices, `core/voices.py`); they are checked against `contracts/registry.schema.json` and every
workflow against the node schemas at start, so a broken pack stops the runner instead of reaching studio.
`runner_service/tests/test_packs.py` fails when a pack's values drift from the canonical contract fixtures
(`contracts/fixtures/registry/<family>.{nodes,workflows}.valid.json`).

The core also provides five nodes in the same grammar (`runner_service/core/nodes.yaml`), published when a
workflow uses them or a pack lists them in `publish`: `prompt` (→ `text: Text`), `seed` (→ `seed: Seed`), `canvas`
(`mode` auto | fixed, `width`, `height` → `canvas: Canvas`), `image` (param `image: image` → `image: Image`),
`output` (`media: Media` in, an Image or a Video; stores it as the run's next output: a PNG, or an MP4 with its
poster PNG and, from API 1.17, its last frame as `<n>.last.png` (`video.last`) and ffprobe's stream facts
(`video.stream`, defs videoStream): a pack's Video value carries `last` (a PIL image) and `stream` for them; the
fake's come from `packs/echo/mp4.py`, ltx's from `core/media.py` on its MP4) and `answer` (`text: Text` in, no outputs: the sink of a text workflow, below).

## Weights on disk: presence, downloads, delete (API 1.16)

The runner publishes each variant's folder as it is on disk (`core/models.py`): the manifest (`files`, each with its
SHA-256 from the huggingface_hub sidecar or hashed once in the background, kept in `<RUNNER_CACHE>/model-hashes-<env>.json`
by path, size and mtime), its `components` (with `from_variant`) and `presence`: present (every component's globs
match where it loads from, no unfinished download), partial, missing, or mismatch (a file changed since its recorded
hash). It rescans every 60 s and after each model job. A download (`core/download.py`, `messaging/models.py`) is a
`cmd.model.job` handled beside the runs: the Hub's file list for `source`, a free-space check, then each file with
`hf_hub_download(local_dir=<root>/<dir>)` (plain files, a temporary cache_dir, `HF_ENDPOINT` selects the hub),
checked against its LFS SHA-256. Files already complete are skipped, so a cancelled or restarted job resumes; a
delete removes only the variant's files inside its folder and keeps a shared component another variant loads. Tests
use `runner_service/tests/fake_hub.py`, never the internet or `~/GenAI/models`.

## model.yaml

```yaml
# runner_service/packs/qwen_image/model.yaml — generates exactly the canonical qwen-image node schemas
id: qwen-image-2.1                # the model id (registry `models`, residency keys)
family: qwen-image                # node kinds are <family>/<stage>; the model node is <family>/model
title: Qwen Image 2.1             # the model's title and the model node's title
publish: [image]                  # core nodes to publish even when no workflow uses them (for graphs)
runtimes:
  torch: {env: torch, devices: [mps, cuda]}   # runtime key = module file (torch.py); env = the runner env that
                                  # loads it; devices (API 1.18, optional) = where it runs, published in `runtimes`
model:                            # the generated <family>/model node (see below)
  help: Which weights to load and at what precision.
  variant: {label: Variant, help: "Turbo: 8 fixed steps, much faster. fp8: smaller community weights, NVIDIA hosts only."}
  precision:
    label: Precision
    default: int8
    help: "int8 quantizes the transformer and text encoder at load: about half the memory."
variants:                         # first one = the default variant and the owner of shared components
  base: {dir: Qwen-Image-2.1}     # dir: a folder under HELMSTUDIO_MODELS (default ~/GenAI/models)
  turbo: {dir: Qwen-Image-2.1-Turbo, shares: [text_encoder, vae]}   # these load from the first variant's dir
  base-fp8: {dir: Qwen-Image-2.1-FP8, precision: [fp8], devices: [cuda]}   # community FP8 weights: NVIDIA only
  turbo-fp8: {dir: Qwen-Image-2.1-Turbo-FP8, precision: [fp8], devices: [cuda],
              shares: [text_encoder, vae], shares_from: base-fp8}          # from base-fp8's dir, not the first's
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
  text_enhancer:                  # an optional add-on (API 1.36): a folder of its own, no variant needs it
    dir: Qwen-Image-2.1-PE-T2I
    addon: {title: Text enhancer, short: Enhancer, tag: PE-T2I, refs: false,   # API 1.40: text to image's
            help: "Qwen-Image-2.1-PE-T2I. Enhance prompt without references."}
    files: "model-*.safetensors"
  edit_enhancer:
    dir: Qwen-Image-2.1-PE-I2I
    addon: {title: Edit enhancer, short: Enhancer, tag: PE-I2I, refs: true,    # an edit's
            help: "Qwen-Image-2.1-PE-I2I. Enhance prompt with references."}
    files: "model-*.safetensors"
lora:                             # LoRAs: the generated qwen-image/lora-stack node and the registry lora block
  component: transformer          # the component LoRAs patch; only stages that use it see the Model's LoRA set
  renames: []                     # {from, to} prefix rewrites of a key's module part (most families: none)
  ignore: []                      # fnmatch globs over raw keys counted as ignored, not unapplied
  modes: [adapter, bake]          # [bake] for a runtime without adapters yet
  max_rows: 8
  strength: {min: 0, max: 2, default: 1}
stages:                           # one node kind each: <family>/<stage>; the function <stage>() in torch.py
  enhance:
    title: Enhance prompt
    component: prompt enhancer
    help: "Rewrites the prompt with Qwen-Image-2.1-PE: PE-T2I without references, PE-I2I with them (9B each), and picks an aspect for an auto canvas; it sees the references of the encode node its text feeds. Bypassed: the prompt and canvas pass through."
    uses: [text_enhancer, edit_enhancer]   # add-ons: published as the node's `addons`
    unload_after: [text_enhancer, edit_enhancer]
    alone: true
    sees: {refs: text}
    reports: [text]
    inputs: {prompt: Text, canvas: Canvas, seed: Seed}
    outputs: {text: Text, canvas: Canvas}
    params:
      think: {type: bool, label: Think, default: false,
              help: "Think longer: better prompts, slower (minutes)."}
    peak_gb: "2.5 + images"
  encode:
    title: Encode
    component: text encoder
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
    component: transformer
    help: Denoises from the seed's noise.
    uses: [transformer]
    inputs: {model: Model, cond: Conditioning, canvas: Canvas, seed: Seed}
    outputs: {latent: Latent}
    params:
      steps: {type: int, label: Steps, default: 40, min: 1, max: 200, step: 1,
              when: "model.variant != 'turbo' && model.variant != 'turbo-fp8'",
              help: Turbo uses its own 8-step schedule and ignores this.}
    peak_gb: "0.9 + 5.1 * mp"     # placeholders: fit the real numbers from measured runs
    stall_s: 600                  # optional: seconds without a cancel point before the run is failed
  decode:
    title: Decode
    component: vae
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
    component: text encoder
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
  `runtimes` (`<runtime>: {env, devices?}`), `model`, `variants`, `components`, `lora`, `stages`, and from API 1.16 `media`
  (what the model makes: `[image]`, `[video, audio]`) and `licences` (`<id>: {title, url?}`; the text is
  `licences/<id>.md` in the pack, shown to the admin before a download). A pack without `variants` has no model
  node and its `models` entry is `{id, family, title}`. From API 1.19 `plan_media` (`{video: {width, height,
  frames, fps, duration_s, has_audio}, audio: {duration_s, has_audio, in, out}}`: the take a memory plan assumes for
  an empty `media` param) and `planned_alone` (media workflow ids planned in a `memory` item of their own, named by
  `workflow`).
- **`variants.<id>`**: `dir`, `shares` (component names loaded from the first variant's folder, so they are one
  residency entry and one cache identity across variants; a share made by hand with links into that folder is still
  a share, a real copy in the variant's own folder is `separate`), `shares_from` (API 1.33: the variant `shares`
  load from instead of the first: qwen-image's turbo-fp8 takes base-fp8's FP8 text encoder; a download into a lender
  that lacks them fetches them, with its configs, from its `source` and `sources`), `distill_from` (M9: the variant
  whose weights are this one's distilled model, published as is; ltx dev-int8's is distilled-fp8), `title` (the display name; default the id) and
  `source` (where a download fetches it; absent = not downloadable): `{repo, revision, allow_patterns, licence,
  must_accept, gated, bytes?}`. `revision` is best a commit SHA (the one in the folder's
  `.cache/huggingface/download/*.metadata` sidecars when the weights are already on disk); `allow_patterns` are
  fnmatch globs over repo paths naming the variant's own components and their configs, never a shared component
  (that comes from the first variant's source into its folder); `must_accept` makes the admin accept the licence
  text; `gated` makes the runner send the host's Hub token (RUNNER_HF_TOKEN_FILE). `files` (`{<component>:
  glob}`) gives a component other files for this variant: ltx's `dev` keeps its transformer beside the distilled
  files, in the first variant's `dir` (one folder, two variants: such a variant owns only its own components' files
  there, for its manifest, bytes and delete; every shared component stays the first variant's). From API 1.33 (a
  community pre-quantized variant for NVIDIA hosts, contracts/README.md "Quantized variants for NVIDIA hosts"):
  `precision` (the model node's precisions for it: `[fp8]`), `component_precision` (`{<component>: [..]}`, its
  components' precisions in this variant: `transformer: [fp8]`), `devices` (`[cuda]`: a runner on another device
  never publishes it) and `sources` (`[{repo, revision, allow_patterns, gated?, bytes?, dir?}]`, more repos fetched after
  `source` into the same `dir`: weights from the community repo, configs from the official one; with `dir`, into
  that folder instead: a component's own `dir`, as MiniMax H3 Image's prompt enhancer).
- **`components.<name>`**: `files` (a glob, or a list of globs: one component may take keys from several files),
  `dtype` (`bf16` | `fp16` | `fp32`, default bf16; `stored` keeps each tensor's own dtype, MLX and torch: MiniMax's
  float32 tensors), `precision` (default `[bf16]`; `int8`, and `int4` on MLX; `fp32` from API 1.20, a component that
  only loads full precision, MiniMax's VAE: never in the model node's enum), `quantize` (fnmatch globs over module
  names, used only at a quantized precision; a stored `U8` weight counts as two values per byte, NVFP4) and `dir` (a
  folder of its own under the models root, for every variant: a model the family loads beside its own, MiniMax's
  prompt enhancer; its files count towards presence, never towards the variant's own files, bytes or delete). Only the keys the module's `rename()` keeps count towards the component's size.
  From API 1.36 `addon: {title, help?, source?}` makes it an **optional add-on** (contracts/README.md "Optional
  add-ons"): no variant needs it to be present, it is in no variant's components, files, bytes or delete, and it is
  downloaded (`source`, as a variant's, into its `dir` or else the first variant's folder) and deleted on its own. A
  stage whose `uses` names it publishes `addons: [<name>]`: studio runs such a node (not bypassed) only on a host
  where the add-on is present, and the stage itself fails `<node>: <short or title> not downloaded on host <h>` without its
  files (MiniMax H3 Image's enhancer, Qwen Image's two). From API 1.40 a stage's `addons: [<name>]` publishes it too
  without loading it through residency (no memory plan counts it: ltx/distill's bake reads the distilled LoRA), and the
  component's `variants` scopes an add-on to the runs of those variants: published as its `variants`, listed only by a
  runner that runs one of them (ltx's two add-ons are the Mac's). Also from 1.40 the `addon` block may carry
  `refs` (the task whose runs need it: true = a run with references, an edit; false = one without; absent = both:
  Qwen's PE-I2I and PE-T2I), `short` (the name in `<short> not downloaded`, the controls' and the stage's refusal;
  absent = `title`) and `tag` (the Models card's mono line; absent = the component id). From API 1.41 an add-on may
  be needed by one value of a stage's enum param instead: the param's `addons: {<value>: [<name>]}` and
  `addons_with: {<input or rows param>: {<value>: [<name>]}}` (that list instead while the input is wired or the rows
  hold a row), published as they are; the stage reads the file itself (`core.pack.value_addons` gives the names).
  MiniMax H3's `generate.lora: turbo` needs `turbo`, with any reference `turbo_ref`. Two add-ons may share a `dir`
  (`MiniMax-H3-Turbo`): each then goes by its own `files` there (presence, its download marker, delete).
- **`lora`** (optional; contracts/README.md "LoRAs", "LoRA normaliser"): `component` (the component LoRAs patch),
  `renames` (ordered `{from, to[, at: end]}` rewrites of a key's module part: a prefix, or with `at: end` a suffix,
  as ltx's `.to_out.0` → `.to_out`), `runtime` (optional: the one runtime the block holds for; ltx's is `mlx`, so
  its torch runner publishes no lora block and no lora-stack node and never meta-builds the model for one), `ignore` (fnmatch globs over raw keys counted as ignored), `modes`
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
  may reorder it), output `model: Model`. A model pack's source stages (no inputs: `ltx/canvas`) come before it,
  unless the stage says `source: false` (API 1.32: `echo/dialogue` keeps its place after `echo/speak`).
- **`stages.<name>`**: `title`, `help`, `component` (API 1.24: the model component it loads, in words, as the Graph
  inspector shows it: `uses the text encoder`; absent = none), `params`, `inputs`, `outputs`, `uses`, `unload_after` (components of `uses`
  unloaded as soon as the stage has run: ltx's Gemma never sits beside its DiT), `peak_gb`, `stall_s`, and (API 1.20,
  minimax) `alone` (true: before the stage the model's other resident components are unloaded, and the memory plan
  counts it without the kept ones: MiniMax's enhancer, text encoder and transformer never fit two at a time),
  `sees` (`{param: output}`: the stage also gets that param of the first node, graph order, whose input is wired to
  its `output`, read and cache-keyed like its own: minimax/enhance reads the encode node's `refs`) and `reports`
  (Text outputs sent as the run's `resolved.texts` `{"<node>.<output>": …}` on succeeded, a cache hit too), and (API
  1.36) `phases` (`[{id, label}]`, ids unique: the ordered phases of a node that runs one engine end to end, published
  as is; its progress then names one with `ctx.progress(step, of, phase, phase_id)`, and core.child's `phases`
  mapping may give `(text, phase id)`: MiniMax H3's generate), and `by_variant` (true: its cache key also holds its Model
  input's variant, for a stage whose output depends on it over components variants share: ltx/encode-guided encodes
  the negative prompt on Dev only). A stage
  with `uses` and no Model input loads from `core.pack.default_model` (the first variant at the model node's default
  precision): minimax/enhance.
  - `params`: name → a registry `paramSchema` without `name`, copied verbatim (`type` int | float | bool |
    string | text | enum | seed | image | rows | frames (a frame count on `min + k × step`, `fps` naming the node's
    frame-rate param; `auto {min_s, max_s}` allows the value `"auto"`: the pack resolves it, `core.graph.auto_frames`
    puts seconds on the grid, and the run reports the stored clip's frames as `resolved.params`); `label`,
    `default`, `min`, `max`, `step`, `enum`, `help`, `when`, for int `values` (the only values allowed) and
    `values_by {param, values: {<enum value>: [...]}}` (the subset for the node's value of that enum param), and for
    rows `item` (field → `Image`, `Lora` or a param schema; a float field may say `position: seconds`, a time on the
    clip) and `mention` (`<image{n}>`)). Order is kept. Item fields left out of a row take their defaults; a rows
    param with an `Image` field may be `[]` in a workflow, and a run needs its `min` items. From API 1.19 (contracts
    README "Media inputs") `media` (one video or audio take: `accepts` [video] | [audio], optional `trim`, `grid`,
    `max_s`, `max_frames`) and `span` (`{start, end}` seconds on the workflow's one video source): null in a
    workflow, required in a run (`<node>.<param>: a take is required` / `a range is required`); the core re-checks
    the caps on the facts studio sent, with studio's texts. From API 1.30 (contracts README "Voices") `voice` (one
    saved voice: `accepts` 1-3 of `preset`, `clip`, `design`; no wire, never a rows field): null in a workflow, and in
    a run the voice studio resolved (defs `voice`: `{item_id, version, source, family?, speaker?, text?, language?,
    style?}`), which reaches the stage in `params` as that dict; the core refuses `<node>.<param>: a voice is
    required` and re-checks the source and a preset's family (`this workflow cannot use a preset voice` | `cannot
    clone a voice` | `cannot use a designed voice`; `voice <item_id> is for <family>`).
    **YAML 1.1 reads bare `on`, `off`, `yes`, `no` as booleans: quote them.**
  - `inputs` / `outputs`: port name → port type, in order. Types: `Text`, `Image`, `Int`, `Float`, `Seed`,
    `Canvas`, `Duration`, `Video`, (API 1.30) `Audio` (plain values; `Media` is an input that takes Image, Video or
    Audio) and the
    family-scoped handles `Model`, `Conditioning`, `Latent`. An input that may be
    left unwired is `name: {type: Image, optional: true}`. Param and input names are disjoint, and no param is named
    `bypass` (the runner refuses to start otherwise).
  - `title` and `help` are what the Graph view's palette and node cards show (the core publishes them as is).
  - `peak_gb`: a Python expression over `mp` (megapixels of the nearest upstream Canvas, 0 without one),
    `images` (how many Image values the params of the stage, the params it `sees` and every node upstream of it hold), `mp_in`
    (megapixels of the largest of them, 0 without), `frames` (the length an input carries as an int `frames`
    attribute, such as a Latent after an auto length resolved; else the `frames` param of the nearest upstream node
    that has one, `"auto"` counting as its longest clip, 0 without; without a Canvas or a frames param a media value
    upstream gives `mp` and `frames`: a video take's size and frames, an audio part at 24 fps), `fps` (an input's
    `fps`, else a video take's, else 24), (API 1.30) `speech_seconds` (the planned speech length: the code points of the
    text reaching the stage's first wired `Text` input, a prompt's text or, through other nodes, the nearest upstream
    prompt's (fewest wires, then node id), else of the stage's nearest upstream prompt, / 15, capped at the stage's
    optional key `max_seconds` (a number > 0, not published); 0 without text; memory plans evaluate it on the
    workflow's default text, except that a stage with `max_seconds` is planned at `max_seconds`, so a speech stage
    should declare it; API 1.32, a stage with a script's `lines` rows: Σ its lines' text / 15 + Σ their `pause_s` but
    the last line's, capped the same way) and the
    stage's own params (not named `mp`, `mp_in` or `speech_seconds`: the pack fails at load; a `frames`, `fps` or
    `images` param feeds the size of its name): numbers, arithmetic, comparisons, `and`/`or`/`not`, `a if cond else b`, `min()`,
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
    return key                                            # (a skipped key is never read: MiniMax's 13 unused layers)

def tensor_hook(component: str, model):                  # optional: None, or hook(name, tensor) -> tensor, run on
    return None                                           # the device before the cast (MiniMax: NVFP4 → bf16)

def rotated(component: str, model):                       # optional (API 1.33): None, or rotated(module name) ->
    return None                                           # its ConvRot group (256) or None, for int8 Linears
```

A Linear whose stored weight (after `rename`) is float8 loads as `Fp8Linear` (`qweight` e4m3 [out, in] as stored,
`scale` fp32 [out, 1] from the key `<module>.scale`, ones when the file has none): rename a torchao file's
`X._weight_qdata` to `X.weight` and `X._weight_scale` to `X.scale`. An int8 ConvRot Linear: rename its weight and
scale to `X.qweight` / `X.scale` (one scale per row), match it by `quantize`, and name its group in `rotated`.

- **`params`**: the node's params, resolved and validated against its schema (a `seed` param arrives as an
  int: null and -1 were replaced by a random 0..2^53-1, reported as the run's `resolved.seed` and each output's
  `seed`). An Image value (an `image` param, an Image field of a `rows` item) arrives as a `PIL.Image`: before the
  stage's first step the core reads the command's resolved value (`{take_id, key, …, bytes, etag}`) from the
  object store, only under `u/<owner_id>/`, at most 64 MB and exactly `bytes` long (else the run fails `input
  image <take_id> is gone`), decodes it with Pillow (PNG, JPEG or WebP) and applies its EXIF orientation. The
  mode is kept as decoded (RGB, RGBA, P, …); the bytes are dropped. A `rows` param is a list of dicts. A `media`
  param arrives as its resolved value (defs media: `take_id`, `kind`, the video facts or `duration_s`, the snapped
  `in` / `out` on a `trim` param) plus `data`, the file's bytes (read the same way, at most 1 GiB, failing `input
  take <take_id> is gone`); the pack decodes them itself and re-reads the file's own facts. A `span` is
  `{"start", "end"}`.
- **`inputs`**: port → upstream value; an unwired optional input has no key. A value param (`int`, `float`, `seed`,
  `string`, `text`, `image`) wired from upstream in a user's graph arrives in `params` with the upstream value (an
  `image` param wired from an `image` node: a `PIL.Image`), so a stage reads params the same way either way.
  Port values: `Text`: `str`. `Seed`: `int`. `Canvas`: `{"width", "height", "mode"}`
  (the stage applies `mode`: auto may size from the references; ltx's has no mode). `Duration`: `{"frames", "fps"}`.
  `Image`: a `PIL.Image` (the core `output` node stores it as PNG; RGBA is fine). `Video`: the encoded clip
  `{"mp4": bytes, "poster": PIL.Image (frame 0, the video's size), "frames", "fps", "has_audio"}`; the `output` node
  stores `<prefix><n>.poster.png`, then `<prefix><n>.mp4` (H.264 + AAC, `+faststart`), and reports
  `video {duration_s, fps, frames, has_audio, poster}`; (API 1.30) `Audio`: `{"wav": bytes, "sample_rate",
  "duration_s"}`, `wav` the PCM 16-bit mono samples (or a mono 16-bit RIFF WAVE file of them), stored by the
  `output` node as `<prefix><n>.wav` (`audio/wav`) and reported `kind: audio`, `audio {duration_s = samples /
  sample_rate, sample_rate}`, no width or height; a clip made from a media take adds `"source_map":
  {take_id, offset_s, new_in_s, new_out_s}` (output time = source time + offset; the generated part), reported as
  `evt.run.output` `source_map`. `Model`: a `core.pack.ModelRef`
  (`variant`, `precision`, `loras` (the ordered LoRA rows of the stacks upstream), `pack.spec`, `folder(component=None)` → the variant's folder for configs, the
  tokenizer and the scheduler, which the pack reads itself). `Conditioning` / `Latent`: whatever object the
  pack returns. Handles never leave the process.
- **Graphs**: a run may be a user's own graph (contracts/README.md, "Graph rules"). The core checks it before it
  starts (known kinds, params, typed wires, required inputs, no cycle, an output, at most 64 nodes) and fails the
  run naming the node; it runs only what an `output` node needs, a kind may appear several times, and each `output`
  node stores its own take (index = the order outputs are stored). A stage only sees its params and inputs.
- **Results are cached** (the 24 most recently used, across nodes and owners) by a key over the owner, kind, pack
  code hash (model.yaml + every top-level `*.py` of the pack: a runtime's module may import a shared one, ltx's
  `common.py`), params (a wired one: its upstream instead), each upstream node's key and
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
  the safetensors headers at the target dtype) plus `peak_gb`; when short, cached node results of 64 MB or more are
  dropped (a small encoding stays: reloading its encoder costs more), the footprint is given up to 10 s to fall (macOS
  takes freed GPU pages back late),
  then components: first those the rest of the run does not need, within each group a baked LoRA set the run does
  not use before plain ones, then least recently used. The budget is RAM (on CUDA: VRAM) minus `RUNNER_RESERVE_GB` (12), capped
  for one run by its command's `budget_gb` (what the host's other env leaves: contracts/README.md "Two envs on one
  host: the hand-over"). After a run that used a baked set, the plain copy of that component is unloaded (unless
  pinned or used too), so the two never stay resident together. The hosts key lists every entry with `gb`,
  `last_used`, `pinned` and a baked set's `loras`; the `models` key carries each present variant's `memory` plan per
  precision, computed from the pack's workflows at their default sizes (components in order of last use,
  `unloads_after` from `unload_after`, `peak_gb` = max over stages of kept components + the stage's own + its
  `peak_gb`; an `alone` stage without the kept ones), so write `peak_gb` for the default sizes honestly: studio admits runs across envs with it.
  `runner.unload` naming components unloads exactly those (never a RUNNER_PIN one). After a run `RUNNER_AFTER_RUN` = `keep_warm` (default; idle unload after
  `RUNNER_IDLE_UNLOAD_S`), `unload`, or `keep_pinned` (`RUNNER_PIN=transformer,vae`). Eviction drops the
  module, runs `gc` and `torch.mps.empty_cache()` (synchronised first; on CUDA `torch.cuda.synchronize()`, then
  `empty_cache()` and the pinned host cache). A pack that needs an `auto` decision
  (tiling) decides it inside the stage; its `peak_gb` should then describe the larger case.

## CUDA: the torch runner on Linux + NVIDIA (API 1.18, M6)

v1 runs Qwen Image on CUDA only (llm stays MLX; LTX's NVIDIA variants came in M9, below). The runner is `runner_service/Dockerfile.cuda` (`make
image-cuda`; `deploy/gpu-host/` runs it with the NVIDIA runtime): the slim Python image plus the `torch` extra from
the same `uv.lock` (on Linux torch 2.15's wheels bundle CUDA 13; the driver comes from the host), ffmpeg, user
`runner`, weights bind-mounted at `HELMSTUDIO_MODELS=/models` (read-only unless the host takes model jobs: the hosts
key's `models_disk.writable`), `/cache` writable (`RUNNER_CACHE`, `TMPDIR`). What changes for a pack: nothing, if it
follows these rules.

- **The device** is picked once per process (`core.loaders_torch.device()`: mps, cuda, cpu; `RUNNER_DEVICE` forces
  one that is present). Use `ctx.device`, never a literal; sync with `getattr(torch, dev).synchronize()` when
  `dev != "cpu"`, never `torch.mps.*` directly.
- **Loading**: each tensor is read into one pinned host buffer (the largest tensor, never a file) and copied with
  `non_blocking`; the next read waits only for that copy. LoRA files load the same way. Growing the buffer past the
  host-RAM budget (`RAM - RUNNER_RAM_RESERVE_GB`, 4) fails the stage `needs <x> GB of host RAM at its peak; host <H>
  has <y> GB`.
- **dtypes**: bf16 is native from compute capability 8.0; below it a `bf16` component loads as fp16 (Qwen in fp16
  may overflow: unsupported). The int8 path is the MPS one (int8 weights on the device); torch's
  `_weight_int8pack_mm` is used only where the build has it for the device (`_int8_kernel`), else dequantize + matmul.
- **Memory**: budget = VRAM total - reserve; used = `torch.cuda.memory_reserved()` + the CUDA context (measured once
  at start). The hosts key adds `device` (name, VRAM, capability, driver, runtime) and `host_ram`. No CPU offload in
  v1 (no `device: cpu` entries), no `ram_peak_gb` in plans (absent = not checked by studio).
- **Seeds and parity**: noise comes from a CPU generator (same noise on every device); the ask stage is greedy. Pixels
  still differ from the MPS goldens: SDPA picks flash / memory-efficient kernels on CUDA, bf16 matmuls accumulate
  differently, and the int8 path may dequantize where MPS uses the kernel. Compare CUDA runs with a tolerance (PSNR),
  never bit-exact, and keep CUDA goldens apart from MPS ones.
- **Media jobs**: `h264_nvenc` when the container's ffmpeg lists it and a 0.2 s test encode works (it needs
  `NVIDIA_DRIVER_CAPABILITIES` with `video`), else `libx264`.

- **A second runtime** (M9, LTX): a pack may list both `mlx` and `torch` (`torch: {env: torch, devices: [cuda]}`),
  one module each (`mlx.py`, `torch.py`) and their shared code in another top-level module (`common.py`: in the code
  hash). A runtime's `devices` holds for every runtime (core/models.py `runs_here`): the Mac's torch runner loads the
  pack but publishes none of its variants. Both runtimes publish the same node schemas; each workflow is one
  runtime's (`env`), and the other runtime's runner leaves it out, as it leaves out a `lora` block with another
  `runtime` (ltx's LoRA Stack is the MLX runner's only). A variant's `distill_from` names the variant whose weights
  are its distilled model (dev-int8: distilled-fp8): torch.py's `ltx/distill` loads it, and studio routes a distill
  run to a host that has it.
  `packs/ltx/torch.py` is mlx.py's stages over the vendored ltx-core with a CPU generator, for the community FP8 and
  int8 ConvRot files only (`devices: [cuda]` variants); its CPU tests are `tests/test_ltx_torch*.py` (the keys of the
  real headers, the five workflows on tiny weights, the port against mlx.py through `envs/mlx`).

**Checked only on a real GPU** (the GPU host's checklist; CPU tests cover the rest): `pytest -m gpu` there; the
pinned staging speed and peak RSS on the real Qwen files; `_weight_int8pack_mm` on CUDA (present or the dequantize
path) and int8 quality; `context_gb` and `memory_reserved` against `nvidia-smi`; the `/proc/driver/nvidia` driver
field; Qwen base and turbo at 1 MP (int8 and bf16), an edit with references, adapter and bake LoRAs, the ask stage;
`empty_cache` returning memory after unload; NVENC detection; a download onto the read-write models root.

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
  lora: {file: window.safetensors, strength: 0.8}   # optional; a file name (no path), strength 0..2 (default 1)
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

## voices/*.yaml

Starter voices (contracts/README.md "Voices"): a pack's preset speakers, each file a list, read in name order,
voices in file order.

```yaml
# an example of runner_service/packs/<pack>/voices/<any>.yaml, every key shown
- id: ryan                    # ^[a-z0-9][a-z0-9-]{0,63}$, unique in the family and no look id of it; published as <family>/<id>
  name: Ryan                  # 1-80 characters, no control characters
  speaker: Ryan               # the model's own speaker id: ^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$ (sent as defs voice `speaker`)
  language: en                # a BCP 47 tag the pack maps to its model's codes
  style: bright and quick     # optional, 1-500 characters
  version: 1                  # >= 1, default 1: bump it when the voice changes
  preview: ryan.wav           # optional clip next to the YAML: PCM WAV, mono or stereo, at most 2 MB and 30 s
```

- At most 200 voices per pack; no other keys. A broken file (schema, a duplicate id, a missing or unreadable
  preview) stops the runner like a broken pack (`core/voices.py`).
- `preview` is PUT once at start to `packs/<family>/voices/<id>-<sha256[:16]>.wav`; an existing key is never
  overwritten. It is only the voice's thumb_url, never an input.
- The runner publishes `<host>.<env>.voices` (an empty list when no pack ships voices). It reads no library at run
  time: a run's voice param arrives resolved (defs `voice`).
- The echo pack's voices are `contracts/fixtures/registry/voices.valid.json` exactly (`tests/test_registry.py`).

## assistant.md and recipes (API 1.71; contracts/README.md "Assistant: model guides")

- `assistant.md` is the pack's guide for the in-app assistant: it starts with a `## Card` section (at most 800
  characters: what the model does, its modes, when to pick it over its neighbours), then good / weak at, settings,
  prompt style, speed and memory, pitfalls, recipes and sources; at most 24576 characters in all. The card is in the
  assistant's prompt while a host holds the family; the whole guide is read with read_model_guide.
- model.yaml `recipes`: named settings, `{name, title, workflow, params: {<node>.<param>: value}, note}`
  (`<node>.bypass` sets a node's bypass). A recipe of a workflow the runner publishes is checked against its node
  schemas at start (a bad one stops the runner); `tests/test_guides.py` checks every one against the canonical node
  fixtures. Quote YAML values that look like numbers or hold commas (`note: "16:9."`, `title: "Final, enhanced"`).
- The runner publishes `<host>.<env>.guides`; the echo pack's is `contracts/fixtures/registry/guides.valid.json`.

## MLX packs (`mlx.py`)

The same contract as `torch.py`, on `core/loaders_mlx.py`: `build(component, model)` returns the `mlx.nn` module
(its arrays stay lazy, never evaluated); the loader reads the files with `mx.load` (lazy), maps each key through
`rename(component, key)` (None skips it), then the optional `convert(component, name, value)` (conv weights to
channels-last), casts only for an explicit `dtype`, quantizes the matching 2-D `.weight`s one at a time
(`mx.quantize`, group 64, after the kept tensors are evaluated), applies `nn.quantize` to exactly those Linears and
loads with `strict=True`. Phase `loading <component>` (underscores as spaces) is reported under the node that needs
it. Memory: the process footprint; eviction runs `gc` + `mx.clear_cache()`; the Metal cache is capped at 2 GB
(`CACHE_GB`: at the default one VAE decode kept 27 GB of freed buffers).
A stage that must refuse a wiring before anything runs defines `check_<stage>(params, wired, models) -> str | None`
(`models`: input port → the Model handle wired there; the text after the node id, e.g. `": stage 2 needs latent,
not canvas or duration"`, `": needs model variant dev"`); the checks run in run order, and the memory plans leave
out a workflow a check refuses at that variant. A Model → Model stage (ltx's `distill`) is pure, like the model
node: the core also builds its handle with `ctx` None. LoRA on MLX is bake only
(`modes: [bake]`): the rows are normalised like torch's, then merged after quantization module by module
(dequantize, + float32 delta, requantize; a float Linear: W.f32 + delta cast back), phase `baking LoRAs` after
`loading <component>` (`fusing LoRA` when the rows are only the pack's `prefused` files: ltx's `distill`); a baked
component is its own residency entry. There are no MLX adapters.

## Native packs (`native.py`, API 1.21)

A stage that runs a **child process** loading its own weights (`minimax_h3`: the `h3` binary, h3.c on Metal, or
for its `base-fp8` variant on NVIDIA hosts `sglang serve`, SGLang Diffusion in its own venv, one per run (`sgl.py`);
`zonos3`: `zonos3-server`, the ZONOS3 ggml server, one per run)
instead of components in the runner. The env `native` runs on the runner core's dependencies only (`RUNNER_ENV=native
uv run python main.py`, health port 8093 from the root Makefile); its loader `core/loaders_native.py` loads nothing:
device `mps`, the budget cut from RAM, `free` a no-op, and the measure is the runner's footprint plus the live
child's (`phys_footprint`), so the hosts key's `used_gb` shows the run's real use.

- **The stage** has `uses: []` and puts the child's whole peak in `peak_gb` (its memory item has `components: []`,
  P = `peak_gb`); the components are still declared, for presence, bytes and downloads. Nothing is resident after
  the run (the child exited), so unload requests, pins and `RUNNER_AFTER_RUN` do nothing.
- **`core.child.run(ctx, argv, cwd=, name=, phases=, idle_s=)`** starts the child in its own session with a cut-down
  environment (`PATH`, `HOME`, `TMPDIR`, `LANG`, `LC_ALL`, `USER`, `<NAME>_*` such as `H3_*`: never the runner's
  secrets), reads its
  stderr split on `\r` and `\n` (a line `<phase> <n>/<total>` is progress, mapped through `phases`, forwarded at most
  every 0.5 s), passes a cancel point every 0.5 s (so the stage's `stall_s` never fires: `idle_s` is the child's own
  limit, failing the run when no progress line came for that long), kills the process group on cancel, shutdown or
  any error (SIGTERM, SIGKILL after 3 s) and fails a non-zero exit with the child's last `<name>: ` line (`h3: `;
  paths replaced). Every path in `argv` must be absolute (cwd is the binary's folder for h3). The child's pid is kept in
  `RUNNER_CACHE/child-<env>.pid`; at start the runner kills a child a killed runner left behind, only when that pid
  still runs the same binary.
- **`with core.child.serve(ctx, argv, cwd=, name=, ready=, ready_s=) as port:`** is the same for a server child
  (zonos3-server): it waits for the ready line (`ready`, a regex whose group 1 is the port; cancel points meanwhile;
  RunError after `ready_s` or on an exit), yields the port to the stage's body and kills the group when the body
  ends, raises or is cancelled; a body error while the server is dead becomes the server's last `<name>: ` line.
- **The binary** is a pinned, vendored copy (`runner_service/vendor/h3c`, `VERSION`, with its licence and notices),
  built by `make h3` in runner_service (Apple clang, about 1 s), or `runner_service/vendor/zonos3` by `make zonos3`
  (CMake + cargo, static, offline, about 2 min); never a binary from another checkout.
- Inputs are files: the stage writes Image values as PNGs into a per-run temp folder (deleted after, with the
  child's MP4) and builds the Video value as ltx does (`core.media.probe`, poster and last frame).

## Checking a pack

`cd runner_service && uv sync --extra torch && uv run pytest -q` (test_packs compares the generated values with
the fixtures); `uv run pytest -m gpu` runs the tests that load real weights. The mlx env:
`uv sync --project envs/mlx && uv run --project envs/mlx pytest -q`; the ltx goldens:
`uv run --project envs/mlx python tests/test_ltx_golden.py [case ...] [--reseed]` (stop both runners first; the
M7 dev cases `retake-448x256-49f-s42 extend-448x256-49f+8-s42 a2v-448x256-49f-s42` take 2-5 min each).
The native env: `uv run pytest -q` covers `core.child` with a fake child script (`tests/test_child.py`) and the
minimax_h3 and zonos3 packs' values (`tests/test_zonos3.py`); zonos3's goldens (`make zonos3`):
`.venv/bin/python -m pytest -m gpu tests/test_zonos3_golden.py`, recorded by `tests/golden/record_zonos3.py` from a
build of the duplex checkout; its goldens (`make h3`, stop the runners first):
`uv run python tests/test_h3_golden.py [t2v fl2va-first fl2va-first-last ref2va-image]` or
`uv run pytest -m gpu tests/test_h3_golden.py`.
