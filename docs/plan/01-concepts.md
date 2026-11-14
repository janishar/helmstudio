# helmstudio-next plan — Concepts

[← Plan index](../README.md)

## 4. Concepts

This section owns every noun in the plan. Other sections use these words and
do not redefine them.

- **Model** — a YAML file describing one model family: components (weight
  files), variants, precisions, runtimes, stage parameters, LoRA rules, memory
  formulas, licence (section 5).
- **Node** — one workflow stage for one family, e.g. `ltx/sample`. Pipeline
  nodes are a separate set (section 16.2). Ports and
  parameters come from a generic kind schema merged with the model's YAML; the
  implementation is one Python function per runtime.
- **Workflow** — a JSON graph of nodes plus an optional Studio layout
  (section 7). A **template** is a workflow shipped in a pack; it is not a
  separate type.
- **Batch** — a workflow or pipeline run once per row of inputs, unattended, as
  one job (section 7.2).
- **Pipeline** — a LangGraph graph of steps, gates and agent nodes, joined by
  takes and decisions (sections 7.2, 16). Running it over rows is a batch.
- **Take** — one output, stored in the gallery with the exact resolved workflow
  and input assets that made it. Uploads and imports are takes with no workflow.
- **Gallery** — the one store of takes, and the one place content is shared and
  reused: everything made lands there, everything consumed is picked from there
  (section 9).
- **Library item** — a named, grouped record with optional text, optional
  reference images, an optional LoRA with strength, and a thumbnail
  (section 14). Two families of groups:
  - **Elements** (character, location, prop), used with `@name`.
  - **Looks** (film, camera, colour, lighting), chosen from a picker.
- **User** — an account with roles; owns everything below except models
  (section 17.3).
- **Host** — a machine with GPUs running runners; owns model weights
  (section 17.5).
- **Session** — a project: prompt history and the takes made in it. Switching
  workflow inside a session keeps the prompt, references, library items, canvas
  and seed where the paths match.
- **Sequence** — a timeline document (section 15): tracks of clips, plus a list
  of library items used as defaults for new shots. A sequence can use takes
  from any session.
- **Clip** — a slot in a sequence pointing at one take. "Shot" is the UI word
  for a video clip, not a separate thing.
- **Agent node** — a pipeline node that judges, writes, decides, remembers or
  talks to an LLM agent instead of generating (section 16).
- **Pick** — choosing one take among the alternatives of the same run. Used by
  a pipeline's review gate (section 7.2) and by replacing a clip (section 15.3).

### 4.1 Port types

| Group | Types | Crosses workers? |
|---|---|---|
| Media | `Text`, `Image`, `Video` (may carry an audio track), `Audio` | Yes — takes in the gallery |
| Values | `Int`, `Float`, `Bool`, `Seed`, `Aspect`, `Canvas{w,h}`, `Duration{frames,fps}` | Yes |
| Handles | `Model[f]`, `Conditioning[f]`, `Latent[f]` (f = family) | No — live inside one worker |
| Lists | `List[T]` of any media or value type; order is significant | as `T` |

Rules that keep this from becoming ComfyUI:

- **A value is a parameter and a port at once.** Every value input has an
  inline widget. Unwired, the widget edits it; wired, the widget shows the
  upstream value read-only. Studio paths always address the consuming node
  (`sample.seed`). Source nodes (Seed, Canvas, Duration) exist only to share
  one value between several consumers.
- **Handles are bundles.** `Model[f]` carries checkpoint, variant, precision and
  the active LoRA set. `Conditioning[f]` carries text embeddings plus
  *unresolved* media items (take + per-item fields); the family's Sample
  resolves them at its own latent shape, which is what lets one Encode feed
  LTX's half-resolution stage 1 and full-resolution stage 2.
  `Latent[ltx]` carries video and audio latents together.
- **Rows.** A parameter can be a list of records whose fields are media or
  values — references, keyframes `{image, time_s, strength}`, prompt beats
  `{text, seconds}`, LoRA rows. The item schema is declared in the model YAML.
- **`@` always names an element.** A mention expands, in order, to the family's
  reference rows using the YAML's `mention` token. An image dropped into the
  prompt becomes an unsaved element named after its file; save it to keep it.
- **Optional inputs and bypass.** An unwired optional input means the feature
  is off: Qwen Encode with no references is text-to-image, with references it
  is edit — one workflow, the mode shown as a read-only badge. Any node can be
  bypassed with a passthrough declared by its kind (Enhance: prompt → text), so
  a Studio toggle can switch a stage off without a second template.
- **Tensors never cross a process.** Handles are family-scoped and a family
  runs in one worker per run, so there is no tensor IPC to build.

### 4.2 Workflow node kinds (the whole v1 catalog)

| Group | Kinds |
|---|---|
| Sources | Prompt, Image, Video, Audio, Seed, Canvas, Duration |
| Model | Model, LoRA Stack |
| Stages | Enhance Prompt, Encode, Sample, Upscale Latent, Decode |
| Media tools | Extract Frame, Trim, Output |

A family provides only the stage kinds it has (LTX-2.5 has no Enhance). Notes:

- **Canvas** takes optional `follow: Image|Video` and `hint: Aspect` inputs and
  resolves `fixed | match input | auto`, snapping to the grid of the Sample
  role it feeds. Qwen's Enhance outputs `text` and an optional `aspect`.
- **Encode** takes the family's conditioning rows. LTX Encode also outputs
  `duration`, predicted from the text when Duration is set to auto.
- **Sample** takes `cond`, `seed`, and either `canvas` (+ `duration`) or an
  input `latent` — never both. Its `role` (declared per family) fixes the grid,
  sigma table, sampler and seed offsets:
  LTX distilled two-stage is
  `Encode → Sample(stage1) → Upscale Latent → Sample(stage2) → Decode`.
- **Decode** outputs what the family makes: `image`, or `video` + `audio`.
- Switches that only add or remove a stage use bypass. Recipes that really
  change topology (LTX one-stage vs two-stage vs retake) are separate templates.
