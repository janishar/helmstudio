# helmstudio plan — Workflows, batches and pipelines

[← Plan index](../README.md)

## 7. Workflow document and the Studio projection

```json
{
  "schema": 1, "id": "qwen-image", "name": "Qwen Image",
  "nodes": {
    "prompt":  {"kind": "prompt"},
    "model":   {"kind": "qwen-image/model", "params": {"variant": "base", "precision": "int8"}},
    "lora":    {"kind": "qwen-image/lora-stack", "in": {"model": "model.model"}},
    "enhance": {"kind": "qwen-image/enhance", "bypass": true, "in": {"prompt": "prompt.text"}},
    "encode":  {"kind": "qwen-image/encode",
                "in": {"model": "model.model", "prompt": "enhance.text"}, "params": {"refs": []}},
    "canvas":  {"kind": "canvas", "in": {"hint": "enhance.aspect"}, "params": {"mode": "auto"}},
    "sample":  {"kind": "qwen-image/sample",
                "in": {"model": "lora.model", "cond": "encode.cond", "canvas": "canvas.canvas"}},
    "decode":  {"kind": "qwen-image/decode", "in": {"model": "model.model", "latent": "sample.latent"}},
    "out":     {"kind": "output", "in": {"media": "decode.image"}}
  },
  "batch": "sample.seed",
  "studio": {
    "composer": ["prompt.text", "encode.refs"],
    "quick":    ["elements", "look:lighting", "model.variant", "canvas", "sample.seed"],
    "inspector": [
      {"title": "Prompt",  "items": ["enhance.bypass", "enhance.think"]},
      {"title": "Quality", "items": ["model.precision", "sample.steps", "decode.tiled"]},
      {"title": "LoRA",    "items": ["lora.rows"]}
    ],
    "presets": {
      "Draft": {"model.variant": "turbo", "model.precision": "int8"},
      "Final": {"model.variant": "base",  "model.precision": "bf16"}
    }
  },
  "canvas": {"positions": {}}
}
```

- Semantics (`nodes`), Studio layout (`studio`) and node positions (`canvas`)
  are separate keys.
- `batch` names the one seed that the seed count varies.
- `studio` is optional. Without it the Studio is derived: every parameter's
  schema names a default slot, label and widget, so any graph is usable as a
  Studio immediately. A test fails if a parameter is neither placed nor hidden,
  or if a `when`/`requires` expression does not parse.
- `studio.quick` lists what sits on the composer: parameters, or library groups
  (`elements`, `look:lighting`). There is no separate slot for looks.
- `studio.presets` is the only place presets live: named sets of values over
  this workflow's own paths. A derived Studio has none.
- In Graph view, "Show in Studio" on any parameter adds it to the layout.

### 7.1 Composing in the graph

- Adding a stage node wires it to the selected node's compatible outputs and to
  the family's Model already on the canvas.
- Dropping a wire on empty canvas lists only nodes that accept that type.
- One graph can hold several stages of one family (LTX two-stage). Work that
  crosses families is a pipeline, because only takes pass between families.

Composing without the graph happens through take actions (section 9.1).

### 7.2 Batches and pipelines — automation

Create is for watching one result arrive. A **batch** is for walking away: a
workflow or a pipeline run once per row of inputs, as one unattended job.

**Which tool for which job**

| Job | Done with |
|---|---|
| One prompt, several seeds | Create's seed count. Not a batch. |
| A list of prompts | Batch of one workflow over pasted lines |
| Parameter or LoRA comparison | Batch with a grid input |
| Animate or edit a set of takes | Gallery multi-select → "Use in…" makes the batch |
| Text → image → video for N prompts | Batch of a pipeline |
| Re-render a film's shots at Final | Timeline (section 15.3), which runs a batch |
| Reject bad takes, pick the best, rewrite prompts, direct a shot list | Agent nodes (section 16) |
| Nightly re-run | `helm run` from cron or launchd; the app has no scheduler |

**Inputs.** A batch is a table: columns are the inputs the workflow or pipeline
exposes, each row is one item.

- Pasted lines: one prompt per row.
- Gallery selection: one media column; each row also offers the source take's
  own prompt.
- CSV: the header names inputs; a file path in a cell is uploaded as a take.
- Grid: up to two parameters with a list of values each (a LoRA axis picks
  from the library); rows are the cross product, the seed is held fixed, and
  the results show as a labelled grid.

**Pipelines.** A pipeline is a graph run by LangGraph (section 16). Its nodes
are steps (each a template or saved workflow), gates, and agent nodes that
judge, write, decide and remember. Takes and small decision values pass between
nodes; there is no workflow nested inside a workflow. Every batch runs on this
engine: Create's "List…" and the gallery's multi-select "Use in…" build a
one-step pipeline in studio_service, kept in the batch snapshot rather than saved.
Interactive runs (Create, timeline shots, exports) go straight to the queue.

- Made in the Pipelines graph, or with "Save as pipeline" on a take whose
  lineage is a chain of templates (section 9.1).
- **Gates** stop for a human pick; parked rows do not hold up the queue, and
  picks can be made in any order from one grid. A **Judge** in Suggest mode
  parks the same way with its pick pre-selected; in Auto mode it picks alone
  (section 16.2).
- Branches and capped loops are allowed here and only here (section 16.2).

**Running**

- **Submit checks everything** (section 16.3). Each row is resolved (presets
  and library items included) and its seeds are drawn and stored. Rows that
  cannot run are listed before the batch starts, not at 3 a.m.
- **Row 1 goes first**, end to end, so a wrong binding in a late step costs one
  item rather than a night.
- **Order** is the dispatcher's job (section 17.5): it keeps running items that
  use the models already loaded, so each model loads as few times as possible.
- **Failures**: a failed item is marked with its error and its later steps are
  skipped; the batch continues. Three failures in a row with the same error
  pause the batch.
- **Resume**: after a crash the batch waits for Resume, then continues from
  where each row stopped (section 16.3). If a pack changed in between, Resume
  says so first.
- **Unattended**: on a Mac host the runner holds an idle-sleep assertion
  while it has work; storage is checked before each item and the batch pauses
  rather than fail when it runs short; the user is notified at the end. What stays loaded
  afterwards is the host's setting (section 6.2).
- **Headless**: `helm run pipeline.json --inputs rows.csv` submits the same
  batch through the API as the signed-in user (section 6).
