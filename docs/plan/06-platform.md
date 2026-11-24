# helmstudio plan — Platform services

[← Plan index](../README.md)

## 9. Platform services

### 9.1 Gallery — the hub

The gallery is the one place content is shared and reused. Create, Pipelines
and the Timeline never hand content to each other directly.

- **Everything made lands here** as a take: Create runs, every pipeline step,
  timeline shots, timeline exports, uploads and imports.
- **Everything consumed is picked from here.** Any media input anywhere — a
  reference row, a first frame, a pipeline's starting input, a timeline clip,
  a library item's images — opens the same gallery picker (this session's
  recent takes first, then search and filters). Dropping a file is an upload
  followed by a pick.
- **One list of take actions**, the same wherever a take appears (the filmstrip
  under a stage, the gallery, a timeline clip):

  | Action | What it does |
  |---|---|
  | Use in… | Lists every template whose media input accepts this take (an image offers "Edit" and "Animate"; a video offers "Extend" and "Retake"). Choosing one opens it in Create with the take bound. With several takes selected it makes a batch, one row per take (section 7.2). |
  | Add to timeline | Appends to a sequence; a multi-selection appends in order. |
  | Save as element / look | Makes a library item from the take's images, prompt or LoRAs. |
  | Reuse settings | Loads the take's resolved workflow into Create. |
  | Save as pipeline | Turns the chain of templates in the take's lineage into a pipeline. |
  | Export as dataset | On a selection or collection: a folder of numbered images with each take's prompt as a caption file, for LoRA training. |
  | Star, export file, delete | — |

- **Collections**: a batch's takes form one collection, shown as a single tile
  per run with intermediate steps folded away. They do not flood Create's
  filmstrip or the picker's recent list.
- **Lineage**: a take records its input takes, so "what was this made from" and
  "what was made from this" are one recursive query. A timeline export's inputs
  are its clips.
- **Storage**: takes are rows in the `studio` database (resolved workflow
  snapshot, parameters, `inputs[]` edges, stars; full-text search over
  prompts) pointing at immutable objects under `u/<owner>/` (section 17.2).
  Every user has their own gallery.

### 9.2 Models and weights

Weights are plain files on each GPU host's own disk (`~/GenAI/models`,
`HELMSTUDIO_MODELS` overrides), never in an image, the object store or the
Hugging Face cache. They are host property, managed by the `admin` role.

- The runner downloads with `huggingface_hub.snapshot_download(local_dir=…,
  allow_patterns=…)`: resumable, gated-repo token from the host's secrets,
  licence shown before download. A download is a queued job like any other.
- **Quantized weights** (decided 2026-11-14): a variant may point at files
  that are already quantized (`quantized: int8`), so a model runs without the
  full-precision download. Such a variant loads as it is: no precision choice
  is shown and nothing is quantized at load. Only fp16 / bf16 weights offer
  load-time quantization, through the component's `precision:` list.
- **Shared components**: `shares:` lets one text encoder or VAE serve several
  models, one copy on disk and one in memory. Each file's content hash is
  recorded at download; a component is reused only when the hashes match.
  A mismatch keeps separate copies and is shown on the Models screen.
- Each runner publishes what it has in the `registry` and `hosts` KV, so the
  UI shows per host which models are present, missing or downloading.
- LoRAs: shared ones live under `<models>/loras/`; a user's own are uploaded
  as files and fetched to the host's cache on first use (section 6.4).

### 9.3 Import

`helm import ~/.helmstudio --user <email>` (admin CLI) brings an old
helmstudio gallery into one user's gallery: items, lineage and stars, each
with a read-only legacy settings snapshot. Old sessions are not imported.

Services, databases and messaging are section 17. The timeline is section 15.
