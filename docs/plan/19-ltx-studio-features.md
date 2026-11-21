# helmstudio-next plan — The new ltx studio features in the LTX pack

[← Plan index](../README.md)

Status: a plan, not in a milestone yet: it becomes one in [08-milestones.md](08-milestones.md) when the owner
schedules it (owner, 2026-11-22: "new changes are on ltx studio and loras downloaded for new feature, bring it inside
this project", meaning everything new in ltx studio, "as in ltx studio"). Design only: nothing here is built.

Facts come from a read-only scout on 2026-11-22:
- the owner's fork `ltx-2-studio` at main `4b2f609` (`web/` is ltx studio, `scripts/ltx_run.py`,
  `packages/ltx-pipelines-mlx`); main is now `e6e15b7`, which adds the sync session's three fixes (`f6b47c9` HDR on
  an official folder, `eb53b03` the 2.5 IC-LoRA stage-2 upsampler, `e6e15b7` the sparse-attention self-check; the
  last is also upstream as dgrauet/ltx-2-mlx PRs #244 and #245, open). Port from `e6e15b7`;
- the sync session's renders (576×384×49 at 24 fps, official 2.5 weights, int8 at load; times include a 20–30 s load),
  including the Deblur IC-LoRA run of 2026-11-22 (23.1);
- the sync session's adoption note;
- HF API metadata;
- the LoRA headers in `~/GenAI/models/ltx-2.5/loras`;
- this repo.

Build this only after the two pieces of work running now have landed:
- **Run on** (contracts, studio, web, cli; API 1.35);
- **the upstream-fixes port** in `runner_service/packs/ltx` (it re-copies the vendored `ltx_core_mlx`, which brings
  `guidance/nag.py`, `loader/lora_adapters.py`, `model/transformer/sparse_attention.py` and the compute-dtype hook).

## 23. The new ltx studio features

### 23.1 Corrections to the feature list

- **DFR and "pixel spatial upscaler ×2" are one feature.** DFR's detailing LoRA is the Pixel-Spatial-Upscaler IC-LoRA
  at a fixed strength of 0.5 (fork `dfr.py:89-91`). It is not Refine-Details.
- **Refine-Details is a separate feature**: ltx studio's "Control Video → Video" task, with a video as the control.
- **The fork's measured DFR (50 s, clearly sharper) used the conv decoder.** That decoder ignores DFR's keyframes. The
  keyframe-aware diffusion decode was not tried; it is several times slower (23.2).
- **The three fixes are on the fork's main now** (`e6e15b7`). `4b2f609` alone fails each of them on this Mac, and
  each failure came from a hard-coded name or check:
  - `eb53b03`: `ic_lora.py` always loaded the LTX-2.3 upsampler name (`spatial_upscaler_x2_v1_1`); a 2.5 folder has
    `v1_0`, so stage 2 silently built an untrained upsampler and decoded a coloured mosaic;
  - `f6b47c9`: `hdr_ic_lora.py` read `embedded_config.json` from the model folder, which an official folder does not
    have, so it refused official weights;
  - `e6e15b7`: `sparse_attention.kernel_available()` compared the kernel with dense SDPA, which MLX ≥ 0.32 runs in
    TF32 on M5, so the fp32 check failed and `LTX2_SOL_TAU` silently stayed dense. The fixed check runs exact fp32 on
    the CPU.
  helmstudio's rule (23.4, "File names"): every IC-LoRA, upsampler, embeddings and decoder file comes from model.yaml,
  never from a name or a pack check in code.
- **Deblur is a fourth IC-LoRA, of the same kind as Refine-Details** (downloaded and proven in ltx studio on
  2026-11-22). Lightricks/LTX-2.5-22b-IC-LoRA-Deblur: a blurry clip as the reference, the same clip back in sharp
  focus. Facts:
  - the file `ltx-2.5-22b-ic-lora-deblur-0.9.safetensors`, 906,080,571 bytes, rank 128 on `attn1` and the
    feed-forward only (576 bf16 tensors);
  - its metadata keys are `model_version` (2.5), `license` and `reference_downscale_factor` (1), and nothing else (no
    `reference_kind`, unlike Refine-Details);
  - the card asks for stage-1-only inference at the output size (the reference attached for the whole denoise;
    "avoid the two-stage path"), LoRA strength 1.0 (0.8 if it halos), and its two-part "DEBLUR" caption; it inverts
    defocus blur, not motion blur;
  - ltx studio has no Deblur screen. It is a LoRA row in the "Control Video → Video" task (`ic-lora --lora <file> 1.0
    --video-conditioning <blurry clip> 1.0`, default two-stage topology). The sync session's run (576×384×49, a
    Gaussian-blurred copy of the base clip, two stages) came back sharp in 99 s.
  So it rides phase 6 with Refine-Details (23.4).
- **SDR to HDR decodes with the diffusion decoder**, not the conv one: `hdr_ic_lora.py` builds its decoder with
  `video_decoder="diffusion"` at fp32, and its seam keyframes need the keyframe-trained decoder (it refuses a decoder
  without a trained `type_emb`). So phase 7 needs the DiffVAE (`vae/ltx-2.5-video-vae-bf16.safetensors`, 1.47 GB, in
  Lightricks/LTX-2.5 at the pinned revision and already on the owner's Mac) and the vendored
  `model/video_vae/diffusion_decoder/` package (11 files). The fork's 151 s HDR render includes that decode.
- **Refine-Details runs at the output size, not at half size.** Lightricks' card: the reference is the source
  Lanczos-resized to the output canvas, frame for frame (`reference_downscale_factor: 1`), 8 distilled steps, LoRA
  strength 1, generic rendering prompts ("sharpness, texture, grain"), tiled at 1024×576 above that. The fork's
  default two-stage IC-LoRA topology halves the reference and refines without the LoRA, which loses the point; its
  `single_stage` topology matches the card. The file's `reference_kind: image_frames` / `negative_position_gap`
  (an optional subject-crop image at frame −1) are read by nothing in the fork; not adopted.
- **ltx studio also offers DFR on Audio to video** (`a2v --dfr`, `a2vid_dfr.py`: distilled a2v's frozen audio on
  DFR's canvas, padded with zero tokens; no temporal rounds or epilogue). The first draft of this plan missed it.
- **The temporal upscaler is already on the Mac** (`latent_upscale_models/ltx-2.5-latent-temporal-upscaler-x2-bf16-1.0`,
  262 MB, fetched with the IC-LoRAs on 2026-11-22). Leaving temporal rounds out is a scope call, not a download one.

### 23.2 What comes in, and what does not

| ltx studio option | In helmstudio | Why |
|---|---|---|
| Distilled retake, extend, audio to video | **Yes** (phase 1) | 5–20× faster on actions already shipped, no new weights. Mac renders: retake 53 s, extend 54 s, a2v 38 s; the dev a2v takes 171 s. |
| Prompt Relay on audio | **Yes, always on for distilled** (phase 2) | A behaviour fix (beats steer the sound too), and it costs nothing: 36 s against 37 s. No knob: a fixed constant, like the video strength today. |
| `LTX2_COMPUTE_DTYPE=float16` | **Yes, as a model option set by the Fast preset** (phase 2) | 37 → 33 s here, −20 % on an M1 Max. It changes pixels, so it lives in the cache key, never in an env var. |
| NAG (negative prompt on distilled) | **Yes, one field** (phase 2) | 37 → 41 s. Today the board says "switch to Dev for a negative prompt". Scale, alpha and tau stay at upstream's defaults. |
| LoRA mode Unfused | **Yes, as ltx `modes: [bake, adapter]`** (phase 3) | It uses the existing contract enum. It keeps small LoRAs' detail on int8 weights and changes strength without a reload. DFR and the IC-LoRAs need it. |
| DFR detail pass (with the pixel upscaler IC-LoRA) | **Yes, as Recipe: Detail** (phase 4) | Sharper hair, edges and texture: 50 s against 37 s. Conv decode only, spatial ×1, no temporal rounds. |
| DFR + unfused LoRA | **Covered**: DFR always attaches its IC-LoRA as an adapter | Measured "almost identical" (48 s against 50 s). With an adapter there is no second transformer in memory. The fork's default fuses in place, so the goldens are recorded with `LTX2_LORA_MODE=unfused` (23.8). |
| DFR on Audio to video (`a2v --dfr`) | **Yes, small, after phase 4** (phase 4b) | ltx studio offers it. With Distilled, workflow 5's Recipe gets Detail: `sample-guided` takes the same `detail` param (the frozen audio zero-padded to the canvas, as `a2vid_dfr.py`). Not measured on this Mac yet. |
| Image bindings (replace / guide) | **Yes, one row field** (phase 5) | Small. Auto, the default, stays bit-identical. |
| Refine-Details (control video → video) | **Optional** (phase 6) | Shares the IC reference code with DFR. One stage at the output size, as Lightricks' card says (23.1), not the fork's two-stage default; the sync session's 57 s (after `eb53b03`) used the default topology, so re-measure. The owner decides. |
| Deblur (control video → video) | **Yes, with phase 6** (owner decides, with Refine) | The same shape as Refine-Details (factor 1, one stage at the output size), so it costs a variant, a template and a golden on top of phase 6. A visible win: a heavily blurred clip came back sharp in 99 s (two stages; one stage is the card's recipe, so re-measure). |
| SDR → HDR (ACEScct, HLG + EXR) | **Yes, last** (phase 7) | The only feature that needs new output kinds (an HDR master, an EXR frames sidecar, a browser preview): an API change. It also brings the DiffVAE and the diffusion decoder (23.1). 151 s. |
| Keyframe-aware diffusion decode (DFR's) | **Owner decides, after phase 7** (phase 7b) | Phase 7 vendors the decoder and adds the DiffVAE anyway, so what is left is a Decoder choice (Conv · Diffusion) that hands DFR's slots to the decoder as keyframes. Sharper fine detail, but about 465 s for 1152×768×25 on an M2 Pro (fork docs) and an fp32 decode's memory. |
| Spatial upscalings 2, temporal upscalings | **Not now** (backlog) | Each is large. Spatial 2 forces a 128 px grid and a tiled epilogue. Temporal changes the frame count and fps (the 262 MB upsampler is already on the Mac) and refuses Prompt Relay. |
| Two-stage `--refine-audio` | **No** | helmstudio has no dev two-stage generate to hang it on. Morph is covered by distilled first + last keyframes. |
| Low-RAM block streaming (`--low-ram`) | **No** | The fork refuses it on official-weights folders, and helmstudio reads official files in place. The residency manager and `unload_after` already plan memory stage by stage. |
| Block-sparse stage 2 (`LTX2_SOL_TAU`) | **No, for now** | The first measure (68 s off, 71 s "on", 768×512×81) was dense both times: the self-check failed on M5 (`e6e15b7`). Fixed, 141 stage-2 attention calls run sparse and land 31.6 dB from the dense render; with float16, sparse took 77 s against 66 s dense. No gain at helmstudio's sizes (just above its 4096-token threshold), it changes pixels and adds a knob. Revisit for long or large renders. The vendored `sparse_attention.py` still comes from `e6e15b7`, so a later switch-on works. |
| Generated keyframes (`--num-generated-keyframes`) | **No** | No measured benefit outside DFR, which places its own. |
| Distilled extend's image anchors | **Later** | ltx studio has them (rows for the new part). Low value; a keyframes node in front of extend can carry them later. |
| A typed negative prompt on the Dev paths (retake, extend, a2v) | **Owner decides** | ltx studio has it (`--negative-prompt` on Dev). Cheap once `ltx/encode.negative` exists: `ltx/encode-guided` takes the same field, empty = today's fixed one. Not new in ltx studio, so not in a phase. |
| TeaCache, stage-step shortening, the keyframe command | **No** | TeaCache is 2.3 only. Steps are fixed by design (model.yaml `values_by`). The keyframe command was removed upstream. |

### 23.3 What the user sees

All new options are Mac only until the NVIDIA follow-up (23.9). Each option's help says "Mac only". The torch runner
publishes the same node schemas, so on NVIDIA its run checks refuse them before anything loads, as it refuses beats
and LoRA rows today (`torch.py` `check_encode`): `encode.negative: NAG is Mac only for now`, `sample.detail: …`, a
non-auto binding. `compute` is ignored there. Add-on variants (`hdr`, `refine`, `deblur`) are `devices: [mps]`, so Run on
NVIDIA fails them with the existing `host <H> does not have model ltx-2.5 variant hdr …`.

**Create · Video (text and image to video)**
- Quality › **Recipe** gets a third option: **Two-stage · One-stage · Detail**. Help: "Detail renders like
  Two-stage, then refines stage 2 with Lightricks' detailing LoRA against stage 1: sharper hair, edges and texture,
  about a third slower. Mac only."
- A new **Negative prompt** section (a text box). Help: "What the clip should not show. Applied with NAG, about 10 %
  slower. Empty: off." It replaces the Quality note "Switch the model to Dev for guidance and a negative prompt."
- Quality gets **Compute: float32 · float16**. Help: "float16 runs attention and feed-forward in half precision:
  10–20 % faster, the picture changes slightly. NVIDIA ignores it." The **Fast** preset sets float16; Quality and
  Low memory set float32 (a preset sets only the keys it names, so without that Fast's float16 would stick).
- The Variant tile offers **Distilled** only on the Mac (Distilled fp8 on NVIDIA; Dev is refused by `check_sample` today: "needs model variant
  distilled"; the old Quality note promised otherwise).
- Image to video: each keyframe row gets **Binding: Auto · Replace · Guide**. Help: "Auto: a frame at 0 s replaces
  the start, later ones guide. Replace pins the frame exactly. Guide lets the clip pass near it."
- Prompt beats' help changes to "each beat steers the picture and the sound for its stretch".
- LoRA rows get the **Mode** control (Adapter · Bake), as Qwen has. The section help becomes "Adapter: applied at
  run time, a strength change needs no reload. Bake: merged at load."

**Create · Retake, Extend, Audio to video, and the Timeline's take actions** (Retake a section, Extend, Audio to
video)
- The **Model** tile offers **Distilled · Dev**. The variant help already says what each does.
- With Distilled, **Steps, CFG and STG are hidden**: distilled runs its own 8 steps without guidance.
- Extend's **Where** offers only **After** with Distilled. Extend at a clip's start on the Timeline pre-fills Dev,
  the only model that extends before.
- The default model is an open point (23.11). The recommendation is Distilled.

**Gallery · "Use in…" on a video take**
- **Make HDR** (phase 7) opens the LTX-2.5 SDR to HDR form with these fields:
  - Source video;
  - **Source colours**: Display sRGB · Linear Rec.709;
  - **Frames colour space**: ACEScg · sRGB linear · ACEScct;
  - **High quality**, help "2× the frames inside, about 2× slower".
- The result is an HDR take with an **HDR** badge. It plays a tone-mapped preview. Its menu has **Download HDR
  master** (HLG, 10-bit HEVC) and **Download EXR frames** (zip).
- **Refine details** (phase 6, optional) opens a form with a source video, an output size (Same · ×1.5 · ×2, capped
  at the untiled limit, 23.4) and a prompt pre-filled with a generic rendering prompt.
- **Deblur** (phase 6) opens the same form without the output size (the card runs at the source size) and with the
  prompt pre-filled with Lightricks' "DEBLUR" caption, "the scene" standing where the scene goes. Help: "For clips
  out of focus; it does not remove motion blur."

**Create · Audio to video**: with Distilled, Recipe offers Two-stage · Detail (phase 4b).

**Models (admin)**: LTX-2.5 lists three more add-on variants, each with its size and "shares 9 with Distilled":
- **SDR to HDR**: 2.79 GB (the IC-LoRA and scene embeddings 1.32 GB, the DiffVAE 1.47 GB);
- **Refine details**: 1.31 GB;
- **Deblur**: 0.91 GB.

Distilled itself grows by 327 MB (the detailing LoRA).

**Board** (the UI spec, drawn 2026-11-22, to be approved by the owner before any build): one board,
`docs/mock/LtxFeatures.dc.html` ("Create · LTX studio features …"), at 13680, 2280 · 1440×5080 in
`docs/mock/canvas.json` (checked free of every other rectangle). It is in the existing style (RunOn's numbered
frames, the TextToVideo / ImageToVideo / VideoSource / TakeActions / Models parts), with the Run on chip on every
composer and each frame tagged with its phase. The existing boards are left as they are: they stay the spec of what
is built, and change when a phase lands.

| # | Frame | Shows |
|---|---|---|
| 1 | Detail pass running | Progress names the detail layout and the detail pass; the Generate key's estimate includes it |
| 2 | Preset and Quality | Recipe Two-stage · One-stage · Detail (menu with help), Compute float32 · float16 (menu, which preset sets which), Quality help without the Dev sentence, Variant Distilled only |
| 3 | Negative prompt, beats, LoRA | The Negative prompt section (NAG), the beats' new help, the LoRA row's Mode Adapter · Bake |
| 4 | Keyframe binding | Each Frames row gets Binding Auto · Replace · Guide; Replace off the latent grid is refused |
| 5 | Refusals | Run on NVIDIA with the new options (the run check's lines), an add-on variant on NVIDIA, an IC-LoRA upload |
| 6 | Retake on Distilled | The version menu Distilled · Dev, Quality without Steps / CFG / STG |
| 7 | Extend on Distilled | Where After only (Before faded, "Before needs Dev"), the distilled extend refusals, the 422 for Before |
| 8 | Timeline take actions | The inline form's Model row; Extend at a clip's start pre-fills Dev |
| 9 | Audio to video on Distilled | Recipe Two-stage · Detail (it stays on Dev: a choice cannot hide); Detail with Dev refused |
| 10 | Use in… on a video take | Deblur, Refine details, Make HDR after today's three actions |
| 11 | Deblur | Source video, the DEBLUR caption pre-filled, Strength (0.8 if it halos), the untiled size refusal |
| 12 | Refine details | The same with Output size Same · ×1.5 · ×2 and a generic prompt |
| 13 | SDR to HDR | Source colours, Frames colour space, High quality; no prompt |
| 14 | The HDR take | HDR badge, tone-mapped preview, Download HDR master / EXR frames, refused on the Timeline and as an input |
| 15 | Models | Distilled + 0.33 GB detailer (partial on a host until fetched), add-ons Deblur 0.91, Refine details 1.31, SDR to HDR 2.79 GB, each Mac only and "shares 9 with Distilled" |

### 23.4 Nodes, params and workflows

One rule throughout: new behaviour is a param or an optional input on an existing kind, and a new kind only where
nothing fits. Two new kinds in all. Everything else is data in model.yaml and the five workflows.

**Phase 1: distilled actions.** No new kind.
- Workflows 3–5:
  - `only: {variant: [distilled, dev]}` (today `[dev]`);
  - `model.variant` placed in `quick`.
- `ltx/retake`, `ltx/extend`, `ltx/sample-guided`:
  - `steps`, `cfg` and `stg` get `when: "model.variant == 'dev' || model.variant == 'dev-int8'"`. This is Qwen
    Turbo's pattern: "ignores this".
  - On a distilled model each stage runs upstream's distilled path:
    - retake: one stage, deterministic Euler, the 8-step table;
    - extend: chunk continuation on the last 25 source frames, two stages inside the node, which then also `uses`
      the upscaler;
    - sample-guided: the distilled stage 1 with the source audio frozen at sigma 0.
- `ltx/extend.direction` gets a **cross-node `values_by`**:
  `{param: model.variant, values: {distilled: [after], dev: [after, before], distilled-fp8: [after], dev-int8: [after, before]}}`.
  A small contract extension, 23.5.
- `ltx/encode-guided` on a distilled model skips the fixed negative prompt (it saves one Gemma pass, about 5 s).
- `ltx/distill` on a distilled model is the identity. The LoRA is already fused in, and fusing it again would apply
  it twice.
- A distilled extend needs sides on a 64 px grid. The runner refuses before loading: "Distilled extend needs sides on
  a 64 px grid (this take is W × H); pick Dev". LTX's two-stage takes are on that grid; One-stage takes (half the canvas, 32 px grid) and uploads may not be. It also needs a source of at least 25 frames (the carried tail):
  `Distilled extend needs at least 25 source frames (this take has n)`.
- Distilled extend's image anchors for the new part: later (low value; a keyframes node in front of extend can carry
  them).

**Phase 2: audio relay, compute, NAG.**
- `ltx/encode.beats`: mlx.py's `relay_mask` also builds the audio mask (vendored `build_audio_relay_mask`, strength
  1.0) and passes it to the DiT's audio-mask argument. Help text only in the schema.
- `ltx/model` gets **`compute`** (enum `float32 | float16`, default `float32`, label Compute). It is a **load option**:
  model.yaml `model.compute` names the components it applies to (`transformer`). Core adds it to those components'
  residency key, beside precision (and reports it in the hosts value's `resident`, 23.5), and the stage calls
  `LTXModel.set_compute_dtype` at load. A load option, not a
  per-run cast, because `set_compute_dtype` also casts float parameters; a baked layer's float32 scales would not
  come back from float16. The torch runner publishes the same schema and ignores the value (ltx-core runs bf16).
  Upstream's overflow guard (`LTXModel` recomputes a non-finite forward and calls `set_compute_dtype(None)` for good)
  would leave a resident float16-keyed transformer running another way: after each float16 denoise the stage checks
  `compute_dtype`, and if the guard dropped it, it evicts that residency entry (the next run reloads) and the run
  reports the warning.
- `ltx/encode` gets **`negative`** (string, 0–500 characters, default `""`, label Negative prompt). Non-empty: the
  stage also encodes it (the `_encode_nag` path) and the Conditioning carries the NAG context. `ltx/sample` applies
  NAG when the Conditioning has it, with upstream's `NAGConfig` defaults (scale 11, alpha 0.25, tau 2.5, video and
  audio). Empty adds nothing to any key. NAG lives on t2v and i2v only, as in ltx studio.
- Workflows 1 and 2:
  - place `encode.negative` in a "Negative prompt" section and `model.compute` in Quality;
  - the Fast preset adds `model.compute: float16`;
  - Quality's help loses the Dev sentence;
  - `only: {variant: [distilled, distilled-fp8]}`: they have none today, so the new add-on variants would otherwise
    show in their Variant tile, and Dev already fails `check_sample` there. `distilled-fp8` stays in, or Run on
    NVIDIA loses text and image to video (`only` names variant ids, `core/registry.py` `allows`);
  - the Quality and Low memory presets add `model.compute: float32`.

**Phase 3: LTX adapters.**
- model.yaml `lora.modes: [bake, adapter]`. The item default becomes `adapter`, as the contract already says for
  families that have both.
- Core's MLX loader `activate` (today a stub that refuses) becomes the twin of `loaders_torch.py`'s `LoraLinear`:
  - it wraps `nn.Linear` and `nn.QuantizedLinear` of a resident component: `y = base(x) + s · B(A x)`;
  - `s` is a held scalar, so a strength change is free;
  - removing the last adapter puts the module back as it was.
- The vendored `lora_adapters.py` is the reference for MLX details (stacked ranks, compute-dtype casting). It is not
  imported: core stays family-neutral.
- Today's refusal `LoRA adapters are not available on MLX` (`loaders_mlx.py:93`) goes.
- **IC-LoRAs are not stack LoRAs.** A file whose safetensors metadata has `reference_downscale_factor` is an IC-LoRA:
  - the shared index leaves it out and logs why, like a non-LoRA file today;
  - an upload of one is `rejected: an IC-LoRA (it needs a reference video), not a stack LoRA`.
  - So the three new files in `ltx-2.5/loras/` never show in the LoRA picker. The scene-embeddings file is already
    left out ("no LoRA keys found").

**Phase 4: DFR.** One new kind, `ltx/ic-lora`.
- **`ltx/ic-lora`** (title IC-LoRA; Model → Model, like `ltx/distill`):
  - `uses: [ic_lora]`;
  - one param, `strength` (float 0–2, default 1);
  - it adds the variant's `ic_lora` file to the Model handle as an **adapter** use (its sha256, the strength, mode
    `adapter`), so core's existing adapter path, cache keys and the hosts value handle it;
  - bypassed, it is the Model as it is.
- **`ltx/sample`** changes:
  - an optional input **`reference: Latent`**: the IC reference. The stage builds the reference conditioning when
    its Model carries an IC-LoRA (downscale factor from the file's metadata). Without an IC-LoRA the input is not
    read.
  - a bool param **`detail`** (default false, label Detail pass), DFR's layout:
    - in stage 1 the canvas is padded to whole keyframe segments (24 or 32 frames), with one generated keyframe slot
      per segment boundary (vendored `keyframe_slots.py`, the fork's `dfr_layout.py` ported into `common.py`); the
      Latent carries the slots and the padding;
    - `ltx/upscale` passes the slots on, upscaled;
    - in stage 2 the upscaled slots condition, stage 1's audio is kept, and the padding is trimmed.
- Workflows 1 and 2:
  - a node `detail` (`ltx/ic-lora`, strength 0.5, bypassed, not placed) between `lora` and `sample2.model`;
  - `sample2.reference ← sample1.latent`, always wired;
  - Recipe's options become:
    - Two-stage: `{upscale.bypass: false, sample2.bypass: false, detail.bypass: true, sample1.detail: false, sample2.detail: false}`
    - One-stage: `{upscale.bypass: true, sample2.bypass: true, detail.bypass: true, sample1.detail: false, sample2.detail: false}`
    - Detail: `{upscale.bypass: false, sample2.bypass: false, detail.bypass: false, sample1.detail: true, sample2.detail: true}`
  - The new wire changes the stage-2 key text once. The pixels of Two-stage and One-stage do not change.
- DFR's refusals hold:
  - no generated keyframes;
  - no CFG or STG (none exist on distilled);
  - beats and NAG are allowed at spatial ×1 with no temporal rounds.
- The decoder ignores the slots (conv decode), as the fork's measured run; phase 7b may hand them to the diffusion
  decoder.

**Phase 4b: DFR on Audio to video.** No new kind. `ltx/sample-guided.detail` (bool, `when` Distilled): on a
distilled model its stage 1 runs on DFR's padded canvas with the frozen source audio zero-padded to it
(`a2vid_dfr.py`); workflow 5 gets the `detail` node and `sample2.reference` wire as workflows 1 and 2, and a Recipe
choice (Two-stage · Detail). Detail with Dev is the run check's 422 (`sample1.detail: needs model variant
distilled`), as ltx studio offers DFR on distilled only. Golden: distilled a2v with Detail.

**Phase 5: bindings.**
- `ltx/keyframes.images` item gets `binding` (enum `auto | replace | guide`, default `auto`).
- mlx.py `_conditionings` maps replace to `VideoConditionByLatentIndex` / FirstFrame and guide to
  `VideoConditionByKeyframeIndex`. Auto is today's rule. Guide works at any frame; Replace needs a frame on the latent
  grid (0 or 1 + 8k), as the fork (`utils/args.py` refuses it off the grid). Rows hold seconds, so the run check
  refuses before loading: `keyframes.images[n].binding: Replace needs frame 0 or 1 + 8k (s s is frame f; the nearest
  is s' s, frame g)`. Snapping instead is an owner call (23.11).
- `last` (follow Auto length) is not added: `seconds` already reaches the end of a fixed-length clip.

**Phase 6: Refine details and Deblur (owner decides each).** A new workflow `6-ltx-refine.json` ("LTX-2.5 Refine details", media video),
one stage at the output size, as Lightricks' card (23.1) and the fork's `single_stage` topology:
- the nodes: `model` (variant `refine`, `only: {variant: [refine]}`) → `lora` → `ic` (`ltx/ic-lora`, strength 1) →
  `sample1` (`role: stage1` at the full canvas: reference ← `source`, an `ltx/encode-video`; canvas and duration from
  the source) → `decode`. No upscale and no stage 2.
- `ltx/encode-video` gets an optional `scale` param (enum 1 · 1.5 · 2, default 1): the source is Lanczos-resized by
  it before encoding, and the output canvas is the source size times it, on the 32 px grid.
- `ltx/sample` stage 1 runs at half the canvas today; when its Model carries an IC-LoRA of factor 1 it runs at the
  full canvas, 8 distilled steps (read from the file's metadata, so no new param). The golden is the fork's
  `ic-lora --single-stage` at the same seed.
- Size: untiled only, so the canvas is capped where the plan's peak fits (the reference doubles the tokens); the
  card's 1024×576 tiles need `components/modality_tiling` vendored: backlog.
- The prompt is pre-filled with a generic rendering prompt (the template's `prompt.text`).
- It is offered in "Use in…" on video takes by the existing `take_uses` rule (one unwired video media param).

**Phase 6, Deblur.** A second template on the same nodes, `7-ltx-deblur.json` ("LTX-2.5 Deblur", media video):
- the same graph as `6-ltx-refine.json` with `model` `only: {variant: [deblur]}` and `ic` strength 1 (placed, 0–1.5:
  the card's "lower toward 0.8 if it halos");
- `encode-video.scale` not placed (fixed 1: the card renders at the source size);
- `prompt.text` pre-filled with the card's caption, "the scene" in both slots: "Reference shows the scene, heavily out
  of focus with soft defocused blur and no fine detail. Edited shows the same scene in sharp focus with crisp detail
  and clean edges. DEBLUR the scene. Subject identity, framing, and background geometry are identical to the
  reference; only focus and sharpness differ between reference and edited." The card says the reference does most of
  the work, so the generic text is a working default;
- no new code beyond Refine's: the factor 1 and the full-canvas stage come from the file's metadata, as for Refine.
  If Refine is skipped, Deblur still needs the phase-6 code (`encode-video` as a reference, the factor-1 full-canvas
  stage); only Refine's `scale` and template drop.

**File names (all phases).** The fork's three fixes (23.1) were each a name or a pack check in code. In the LTX pack:
- every file a stage opens comes from model.yaml through `ctx.component`: `ic_lora` (per variant: the detailer,
  Refine, Deblur, HDR), `hdr_scene`, `upscaler`, `vae_diffusion`. Code ported from the fork into `common.py` takes
  paths as arguments and never names a file, an upsampler version or a pack layout;
- a missing component fails the load with its name (as `upscaler` does today, `mlx.py`): never a silently built,
  untrained module;
- the model version is the variant's (model.yaml), so no `embedded_config.json` check is ported;
- the one name in code today, `common.py` `DURATION_HEAD` (read by `mlx.py` and `torch.py`), moves to a
  `duration_head` component in phase 4, scoped `variants: [distilled, distilled-fp8]` (auto length lives in
  workflows 1 and 2 only), so Dev's "shares 8" and the add-ons' "shares 9" do not change;
- the vendored `sparse_attention.py` comes from `e6e15b7` (the upstream-fixes port copies from fork main, not from
  `4b2f609`);
- one runner unit test fails when a `.safetensors` name appears in `packs/ltx/{mlx,common,torch}.py`.

**Phase 7: SDR → HDR.** One new kind, `ltx/sdr-to-hdr` (title SDR to HDR), and a new workflow
`8-ltx-sdr-to-hdr.json`:
- Nodes: `model` (variant `hdr`, `only: {variant: [hdr]}`) → `ic` (`ltx/ic-lora`, strength 1) → `hdr` → `output`.
- `ltx/sdr-to-hdr`:
  - inputs `model: Model`, `seed: Seed`; output `video: Video`;
  - `uses: [transformer, vae_encoder, vae_diffusion, hdr_scene]` (`vae_diffusion`: the DiffVAE, run at fp32, a
    component of the `hdr` variant only);
  - params:
    - `source` (media, video, grid 2, max 321 frames: the fork reflect-pads to 32 and crops back, and the HLG 4:2:0
      master needs even sides; its frames are floored to 8k + 1 like `ltx/encode-video`);
    - `input_colorspace` (`srgb_gamma | srgb`, default `srgb_gamma`, labels Display sRGB / Linear Rec.709: the
      fork's `--input-colorspace` values as they are, where `srgb` means linear);
    - `exr_colorspace` (`acescg | srgb_linear | acescct`, default acescg);
    - `high_quality` (bool);
  - one fat stage, because none of its parts has a use alone. It is a single distilled stage with the scene
    embeddings in place of a prompt (no Gemma, no connector, no audio), the seam keyframes at upstream's 0.95, the
    keyframe-aware fp32 diffusion decode in ACEScct (the DiT freed first, as the fork), and then three outputs:
    - the HLG BT.2020 10-bit HEVC master (`yuv420p10le`, `arib-std-b67`);
    - the EXR frames as one zip;
    - an 8-bit H.264 preview, tone-mapped from the linear frames (a fixed Reinhard curve on luminance, so it does not
      look flat or dark like the sync page's re-encode).
- The seam-keyframe and strength knobs are not exposed (upstream defaults). `--frame-rate` (for EXR-folder inputs) is
  not needed: inputs are takes.

**Phase 7b: diffusion decode for Detail (owner decides).** A Decoder choice (Conv · Diffusion) in Quality: with
Diffusion, DFR's stage-2 slots go to the decoder as keyframes. `vae_diffusion` then also belongs to Distilled
(`variants: [distilled, hdr]`, the 1.47 GB joins Distilled's download). A stage's `uses` is static, so the shape
(one more kind, or `uses` chosen by a param) is settled with the boards; no contract today lets `uses` vary.

### 23.5 Contract changes (contracts first, then `make contracts-check`)

| Phase | Change | API |
|---|---|---|
| 1 | `registry.schema.json` / check.py / studio `ValuesBy` / web: `values_by.param` may name another node's param (`model.variant`, the `when` reference form), read from the merged graph values; and `values_by` may narrow an **enum** param, not only an int. Studio's run check and Graph `param_invalid` use it. **Offers rule**: when a take action's pre-fill (the Timeline range's `extend.direction`) falls outside the template's `values_by`, the offer also sets the named param to the first value of its enum that allows it (Extend before → Model Dev). check.py `extend_room` / `take_uses` and their fixtures. ltx fixtures: `when` on steps / cfg / stg, the templates' `only`. | none (registry only, as M9's `distill_from`) |
| 2 | ltx fixtures: `ltx/model.compute`, `ltx/encode.negative`, the beats help, the templates' studio blocks and presets. **Hosts value**: a `resident` item gains optional `compute` (absent = float32), and the dispatcher's Entry identity and "resident" test (README, Order and Admission) compare it like `precision`; without it a float16 run would count a float32 transformer as resident and skip its load in the plan. `req.runner.unload` names it too. | **the next after Run on's 1.35** (additive) |
| 3 | README "LoRAs": the ltx sentence becomes "ltx: bake and adapter (MLX); NVIDIA LTX publishes none yet"; ltx fixtures' `lora-stack` mode enum `[bake, adapter]` default adapter; the LoRA upload checks gain the IC-LoRA reason. | none |
| 4 | ltx fixtures: kind `ltx/ic-lora`, `ltx/sample.reference` and `.detail` (4b: `ltx/sample-guided.detail`), the Recipe options; the palette lists `ltx/ic-lora` as `mlx` only (like `lora-stack`). A component's `variants` scope (23.6) is model.yaml only; if the hosts `models` key or the memory items list components per variant, their fixtures follow (check when building). | none |
| 5 | ltx fixtures: the `binding` item field. | none |
| 6 | ltx fixtures: variants `refine` and `deblur`, `encode-video.scale`, workflows `ltx-refine` and `ltx-deblur`; offers fixture for "Refine details" and "Deblur". | none |
| 7 | openapi: `Take.hdr` (`hlg` or null) and `Take.extras` (`[{role: master \| frames, content_type, bytes, url}]`, signed links like the take's own); `evt.run.output` gains optional `extras` (keys under `u/<owner_id>/`; additive, message version stays 1). Media inputs refuse an HDR take (`take <id> is HDR; this input needs an SDR video`); the Timeline refuses it (`an HDR take cannot go on the timeline yet`); export unchanged. The echo family gets a fake HDR output so the contract suite covers extras. ltx fixtures: variant `hdr` (with `vae_diffusion`), kind `ltx/sdr-to-hdr`, workflow `ltx-sdr-to-hdr`. | **the next free version** (after phase 2's) |

Patterns kept: errors `{"detail"}`, another user's take is 404, owner scoping by RLS, object keys under
`u/<owner_id>/`, never bytes or URLs in messages.

### 23.6 Weights and model.yaml

All LTX-2.x Community License (`licence: ltx-2-community`, already in model.yaml). The HF API reports
`gated: auto` (click-through) for all four IC-LoRA repos (Deblur too), so the host's HF token must have accepted each one. This
is the same open check as Lightricks/LTX-2.5 in 21.6.

| File (`~/GenAI/models/ltx-2.5/…`) | Repo @ revision | Bytes | Becomes |
|---|---|---|---|
| `loras/ltx-2.5-22b-ic-lora-pixel-spatial-upscaler-x2-1.0.safetensors` | Lightricks/LTX-2.5-22b-IC-LoRA-Pixel-Spatial-Upscaler @ 59f26f1e0c11af42f1affa924b4ef231e9e98b2a | 327,322,640 | component `ic_lora` of variant **distilled** (a `sources` entry) |
| `loras/ltx-2.5-22b-ic-lora-refine-details-1.0.safetensors` | Lightricks/LTX-2.5-22b-IC-LoRA-Refine-Details @ 4912c478b35dd96b6cdcc5f7c7ff9d72c2c57929 | 1,308,787,534 | `ic_lora` of the add-on variant **refine** |
| `loras/ltx-2.5-22b-ic-lora-deblur-0.9.safetensors` | Lightricks/LTX-2.5-22b-IC-LoRA-Deblur @ c122ce9224a6002bdb44efc55b882c903387dca6 | 906,080,571 | `ic_lora` of the add-on variant **deblur** |
| `loras/ltx-2.5-22b-ic-lora-sdr-to-hdr-1.0.safetensors` | Lightricks/LTX-2.5-22b-IC-LoRA-SDR-To-HDR @ c7578ebddb54f4210e534e4d8cad698a5a520692 | 1,308,787,472 | `ic_lora` of the add-on variant **hdr** |
| `loras/ltx-2.5-22b-ic-lora-sdr-to-hdr-scene-emb.safetensors` | the same repo and revision | 12,614,165 | component `hdr_scene` of **hdr** |
| `vae/ltx-2.5-video-vae-bf16.safetensors` (already on the Mac) | Lightricks/LTX-2.5 @ 5e6e71018ee1756ed329b697a7b4aedc934dfce9 | 1,472,223,346 | component `vae_diffusion` of **hdr** (and of distilled with 7b) |

The temporal upscaler (262 MB, already on the Mac) is not added (23.2).

**Why add-on variants for HDR, Refine and Deblur, and a component of Distilled for the detailer.**
- The detailer is part of a recipe of every distilled render and is small.
- HDR, Refine and Deblur are separate workflows with 0.9–1.3 GB files that an admin may never want.
- An add-on variant reuses all the Models machinery as it is: presence, the download job and its licence, delete,
  "shares 9 with Distilled", and the dispatcher's "variant present" eligibility for offers.
- The add-on variants share every other component with distilled, so the transformer is the same residency entry:
  no second 19 GB load.

**One small core change makes this work.** Today every variant has every component (`core/models.py`, `own_globs`
and `variant_entry`). A component may now carry **`variants: [..]`**: it exists only in those variants, and absent
means all, as today. Without it, the HDR scene file would be required for Dev and for the NVIDIA variants.

Sketch:

```yaml
model:
  compute: {label: Compute, default: float32, enum: [float32, float16], components: [transformer],
            help: "float16: attention and feed-forward in half precision, 10-20 % faster, the picture changes slightly. NVIDIA ignores it."}
variants:
  distilled:
    # ...as today, plus:
    sources:
      - {repo: Lightricks/LTX-2.5-22b-IC-LoRA-Pixel-Spatial-Upscaler, revision: 59f26f1e0c11af42f1affa924b4ef231e9e98b2a,
         allow_patterns: [ltx-2.5-22b-ic-lora-pixel-spatial-upscaler-x2-1.0.safetensors], dir: ltx-2.5/loras,
         gated: false, bytes: 327322640}
  hdr:
    dir: ltx-2.5
    title: SDR to HDR
    devices: [mps]
    shares: [text_encoder, connector, transformer, upscaler, vae, vae_encoder, audio_vae, vocoder, audio_encoder]
    files: {ic_lora: loras/ltx-2.5-22b-ic-lora-sdr-to-hdr-1.0.safetensors,
            hdr_scene: loras/ltx-2.5-22b-ic-lora-sdr-to-hdr-scene-emb.safetensors}
    source: {repo: Lightricks/LTX-2.5-22b-IC-LoRA-SDR-To-HDR, revision: c7578ebddb54f4210e534e4d8cad698a5a520692,
             allow_patterns: ["ltx-2.5-22b-ic-lora-sdr-to-hdr-*.safetensors"], dir: ltx-2.5/loras,
             licence: ltx-2-community, must_accept: true, gated: false, bytes: 1321401637}
    sources:   # the DiffVAE, for the keyframe-aware fp32 decode
      - {repo: Lightricks/LTX-2.5, revision: 5e6e71018ee1756ed329b697a7b4aedc934dfce9,
         allow_patterns: [vae/ltx-2.5-video-vae-bf16.safetensors], gated: false, bytes: 1472223346}
  refine:
    dir: ltx-2.5
    title: Refine details
    devices: [mps]
    shares: [text_encoder, connector, transformer, upscaler, vae, vae_encoder, audio_vae, vocoder, audio_encoder]
    files: {ic_lora: loras/ltx-2.5-22b-ic-lora-refine-details-1.0.safetensors}
    source: {repo: Lightricks/LTX-2.5-22b-IC-LoRA-Refine-Details, revision: 4912c478b35dd96b6cdcc5f7c7ff9d72c2c57929,
             allow_patterns: [ltx-2.5-22b-ic-lora-refine-details-1.0.safetensors], dir: ltx-2.5/loras,
             licence: ltx-2-community, must_accept: true, gated: false, bytes: 1308787534}
  deblur:
    dir: ltx-2.5
    title: Deblur
    devices: [mps]
    shares: [text_encoder, connector, transformer, upscaler, vae, vae_encoder, audio_vae, vocoder, audio_encoder]
    files: {ic_lora: loras/ltx-2.5-22b-ic-lora-deblur-0.9.safetensors}
    source: {repo: Lightricks/LTX-2.5-22b-IC-LoRA-Deblur, revision: c122ce9224a6002bdb44efc55b882c903387dca6,
             allow_patterns: [ltx-2.5-22b-ic-lora-deblur-0.9.safetensors], dir: ltx-2.5/loras,
             licence: ltx-2-community, must_accept: true, gated: false, bytes: 906080571}
components:
  ic_lora: {files: loras/ltx-2.5-22b-ic-lora-pixel-spatial-upscaler-x2-1.0.safetensors, dtype: stored,
            variants: [distilled, hdr, refine, deblur]}
  hdr_scene: {files: loras/ltx-2.5-22b-ic-lora-sdr-to-hdr-scene-emb.safetensors, dtype: stored, variants: [hdr]}
  vae_diffusion: {files: vae/ltx-2.5-video-vae-bf16.safetensors, dtype: fp32, variants: [hdr]}   # 7b: + distilled
```

Notes on the sketch:
- `gated: false` follows the pack's current convention for Lightricks repos. Whether `auto` should be stated is the
  open check above.
- Whether a source's `dir` can point into a subfolder, or the patterns must carry the `loras/` path, is settled
  against `core/download.py` when building.
- Existing distilled hosts show **partial** until the 327 MB is fetched; a download job completes them. Partial is
  not present, so until then that host runs no Distilled workflow (text and image to video, and the distilled
  actions). The owner's Mac already has the file; any other Mac needs the download when phase 4 lands.

Vendoring (after the upstream-fixes port; copied unchanged, with VENDORED.md entries):
- `conditioning/types/keyframe_slots.py`;
- `hdr.py` and `color/` (hlg, primaries, yuv);
- `model/video_vae/diffusion_decoder/` (11 files: the DiffVAE and its keyframe-aware decode; needed by phase 7);
- the fork's `utils/hdr_media.py` (ACEScct load, EXR write, HLG encode) is pipeline code: ported into `common.py`, as `iclora_utils` is;
- the IC reference helpers. The fork's `iclora_utils.reference_conditioning_from_latent` is pipeline code, so it is
  ported into `common.py`.

### 23.7 Memory (peak_gb)

The formulas are estimates until each phase's golden measures them at 448×256×49 (and one larger size for DFR). They
are then fitted as in M4 and M7.

| Stage | Plan |
|---|---|
| retake, extend, sample-guided on distilled | Today's formulas: memory is one pass's, and distilled makes one pass where dev makes four. They are upper bounds. Distilled a2v has no second (baked) transformer, so its footprint drops from 32.8 GB to about 20 GB (the planner sees this from residency; no formula changes). |
| encode with a negative prompt | 5.3 as today (one more Gemma forward, same transient). |
| sample with NAG | +0.5 GB (fork, M1 Max), folded into the constant. |
| compute float16 | No change in the plan (activations shrink; kept as an upper bound). |
| adapters (phase 3) | Counted at the file's size before attaching, as the contract says for adapters. The pixel IC-LoRA is 0.33 GB, Refine and HDR 1.31 GB, Deblur 0.91 GB. |
| sample stage 1, `detail` | + the padded frames (at most 31) and one latent frame per slot: `frames` → `frames + 32` and `+ frames / 24` slot frames in the token term when `detail`. |
| sample stage 2, `detail` with the IC reference | The reference adds a quarter of the stage's tokens (factor 2) plus the slots: `+ (0.26 * ((frames / 8 + 1) / 4 + frames / 24) * mp if detail else 0)`. The fork measured a 39 GB process footprint on an M4 Pro at 512×768×97. Here the transformer is resident once (19.2 GB) with the 0.33 GB adapter, not reloaded. |
| refine and deblur stage 1 | The reference at factor 1 doubles the tokens (the adapter is 1.31 GB for Refine, 0.91 GB for Deblur). That needs a `ref` term that core binds like `images` (planned in phase 6). |
| sdr-to-hdr | No formula until the golden measures it: no `memory` item, so the planner takes P = B (the whole host), as the contract does for a run without a plan. The peak is the later of the video-only DiT stage (the reference doubles the tokens; ×2 frames with High quality) and the fp32 diffusion decode, which the fork runs after freeing the DiT. The fork's low-RAM run peaked at 13.4 GB; this one is not low-RAM. |

### 23.8 Tests (lean)

One Mac golden per new path, recorded from the fork's CLI (the sync worktree, with its fixes) at the same seed and
size, as the M4 and M7 goldens were, and matched at PSNR ≥ 40 dB. That ties helmstudio to "as in ltx studio".

| Phase | Golden (448×256×49) | Other tests |
|---|---|---|
| 1 | distilled retake, distilled extend (after), distilled a2v | check.py fixtures for cross-node `values_by` and the Extend-before offer; studio run 422 for distilled + before; web: Steps / CFG / STG hidden on distilled |
| 2 | the beats golden re-recorded (audio relay on); one t2v with a negative prompt at Fast (float16 and NAG in one render) | residency key differs by compute; an empty negative adds nothing to keys |
| 3 | the existing synthetic rank-8 LoRA as an adapter (the bake golden stays) | a strength change does not reload; an IC-LoRA file is left out of the index and rejected on upload |
| 4 | t2v Recipe Detail (fork with `LTX2_LORA_MODE=unfused`, since helmstudio attaches the detailer as an adapter); 4b: distilled a2v Detail | Two-stage and One-stage goldens unchanged (bypassed detail, unread reference) |
| 5 | none | unit test: auto matches today's conditionings bit for bit; replace and guide map to the right types |
| 6 | refine on a golden clip, deblur on a blurred golden clip (fork `ic-lora --single-stage`, each file as `--lora … 1.0`) | offers fixture; the no-file-names test (23.4) |
| 7 | HDR on a golden clip (PSNR on the decoded HLG frames, and the EXR count and shape) | contract suite: extras signed links, an HDR take refused as a media input and on the Timeline, isolation (another user's extras are 404) |

Gates per phase: runner `make test lint`, `make contracts-check`, and `make test` on the `helmstudio-test` stack. Per
the M7 lean rule, one combined reviewer per phase, and the suite once per phase.

### 23.9 NVIDIA follow-up (torch.py on vendored ltx-core `daa6b16`)

On the VM, after M9 phase 3's renders. Each step adds one `.cuda` golden.

| Step | What | Hours |
|---|---|---|
| N1 | Distilled retake, extend and a2v on `distilled-fp8`. No LoRA is needed; ltx-core's RetakePipeline defaults to distilled. | 4–6 |
| N2 | LTX adapters on torch. Core's `LoraLinear` wraps `nn.Linear` and `Int8Linear`; add ltx-core's `Fp8Linear` and the ConvRot `Int8Linear` (check first whether Qwen's fp8 adapter path already covers them). Publish an ltx `lora` block for torch with `modes: [adapter]` (bake stays refused), with ltx-core's module names as renames. This lifts "no LoRAs on NVIDIA LTX". | 6–8 |
| N3 | NAG, and Prompt Relay for video and audio. Both are absent from ltx-core; port the MLX vendored modules (relay's `context_mask` takes `build_relay_mask` as is). | 6 |
| N4 | `ltx/ic-lora` and `sample.detail` / `reference` on torch, from ltx-core's `keyframe_slots.py` and `reference_video_cond.py`; needs N2. The `ic_lora` component on `distilled-fp8` (a `sources` entry; scope it there). | 8–10 |
| N5 | SDR to HDR on torch: ltx-core `color/hlg.py`, and upstream ltx-pipelines' ACEScct `hdr_ic_lora` as the reference (not vendored); needs N2. | 6 |

fp16 compute, Sol-tau and low-RAM are MLX-only knobs: torch ignores `compute`.

### 23.10 Phases and effort (Mac)

| # | Phase | Hours |
|---|---|---|
| 0 | The board of 23.3 (drawn), approved by the owner | 1 |
| 1 | Distilled retake, extend, a2v (cross-node `values_by`, the offers rule, three goldens) | 12–14 |
| 2 | Audio relay, compute load option (core, and the hosts `resident.compute` contract field), NAG negative prompt, two goldens | 12–14 |
| 3 | LTX adapters on MLX (core `activate`), the IC-LoRA index rule | 6–8 |
| 4 | DFR: variant-scoped components (core), `ltx/ic-lora`, `sample.detail` and `reference`, the distilled source, vendoring, golden | 14–18 |
| 4b | DFR on Audio to video (`sample-guided.detail`), golden | 3–4 |
| 5 | Image bindings | 2–3 |
| 6 | Refine details and Deblur (owner decides each; Deblur alone 6–8, Refine adds 2–3) | 8–11 |
| 7 | SDR to HDR: an API version, extras through studio, files and web, the kind, the DiffVAE and the vendored diffusion decoder, the preview tone map, golden | 20–26 |
| 7b | Diffusion decode for Detail (optional) | 3–4 |
| | **Mac total** | **about 78–99** (70–88 without phase 6; 7b adds 3–4) |
| | NVIDIA follow-up (23.9) | 30–36 |

Order: phases 1–3 can ship one at a time. Phase 4 needs 3; 4b needs 1 and 4. Phases 6 and 7 need 3 and the `ltx/ic-lora` kind from 4; 7b needs 7.

### 23.11 Open points for the owner

1. **Default model for Retake, Extend and Audio to video** (and so for the Timeline's actions): Distilled
   (recommended: 5–20× faster) or Dev, as today.
2. **float16 compute**: only in the Fast preset (recommended), or the default for everything (−11 % here, pixels
   move slightly).
3. **LTX LoRA mode default**: Adapter (recommended, as Qwen) or Bake. Existing workflows keep the mode they hold.
4. **+327 MB on the Distilled download** for the detailer (recommended), or a fourth add-on variant "Detail pass" that
   Recipe: Detail asks for. The cost of the first: a host without the file turns Distilled partial, so it runs no
   Distilled workflow until the 327 MB download (the owner's Mac has it).
5. **Refine details and Deblur** (phase 6, each one stage at the output size as Lightricks' cards, capped at the
   untiled size): build both, Deblur only (recommended: the clearer win, and it carries the shared code), or skip.
   Tiled runs for 1080p and up need modality tiling vendored (backlog).
6. **SDR to HDR scope**: the HLG master, a tone-mapped preview and an EXR zip (recommended), or skip it. It is the
   costliest phase, needs an API version, and pays off only on HDR displays and in grading.
7. **Diffusion decode for Detail** (phase 7b): build after phase 7 (it is then small, but very slow and +1.47 GB on
   Distilled) or backlog.
8. **DFR on Audio to video** (phase 4b): build with phase 4 (recommended: ltx studio has it, small) or skip.
9. **A typed negative prompt on the Dev paths** (retake, extend, a2v): add with phase 2 (cheap) or keep the fixed one.
10. **Backlog or drop**: spatial upscalings 2, temporal upscalings (48 fps; the upsampler is already on the Mac), and
   distilled extend's anchors.
11. **The HF gate**: the four IC-LoRA repos are `gated: auto` (HF API, 2026-11-22). The hosts' token must accept each one, as for
   LTX-2.5.
12. **Deblur's prompt**: the card's caption with "the scene" pre-filled (recommended, the clip does most of the work),
   or a "Scene" field that helmstudio wraps into the caption (a template feature that does not exist yet).
13. **Replace off the latent grid**: refuse it, naming the nearest grid frame (recommended, as ltx studio), or snap
   the row to that frame silently.
