# helmstudio plan — The NVIDIA quantized path

[← Plan index](../README.md)

Status: M9 (owner, 2026-11-21: "find a full rtx pro 6000 spot cheapest and prepare for them to use community version
in fp8 if not available int8", then "put this as alternate quantised deploy path for nvidia"). The scout, with every
repo, revision, size and tensor layout, is [reference-notes/nvidia-quantized.md](../reference-notes/nvidia-quantized.md).

## 21. Community FP8 / int8 weights as an alternate path for NVIDIA hosts

### 21.1 What it is

The official weights stay the default everywhere (bf16, or int8 made at load). An NVIDIA host can instead load
**community pre-quantized weights**: FP8 where the community publishes it, else int8. They are smaller to download
and keep on disk (about 145 GB for every pack instead of about 466 GB), and FP8 runs natively on Blackwell. They are
**extra variants** of the existing packs, marked `devices: [cuda]`, so a Mac never offers them and nothing about the
official path changes. The deploy guide is [deploy/gpu-host/README.md](../../deploy/gpu-host/README.md), "The
quantized path".

### 21.2 Which weights

| Pack | Part | Quantized build (community unless said) |
|---|---|---|
| Qwen Image 2.1 base / Turbo | transformer, text encoder | unsloth FP8 (per-row scales; text encoder a plain FP8 cast); VAE bf16 |
| MiniMax H3 Image | DiT | unsloth FP8 (curve-form adaLN: an 8-wide time table instead of the time MLP) |
| | text encoder (Qwen3-VL 32B) | no FP8 exists: Comfy-Org int8 ConvRot (block-Hadamard rotated, per-row int8) |
| | prompt enhancer | official NVFP4, as today |
| MiniMax H3 video | FL2VA, Ref2VA DiTs | community FP8 (phase 2) |
| LTX-2.5 | distilled transformer | unsloth FP8 (phase 3) |
| | dev transformer | no FP8 exists: community int8 ConvRot (DmitryDB; phase 3) |
| | text encoder (Gemma 4 12B, with the projection) | no FP8 exists: community int8 ConvRot (DmitryDB) |
| | connector | bf16 (unsloth's shards) |
| ZONOS3-Mini | all | the official GGUF q8_0 set (already int8), built for CUDA |

Qwen3-TTS stays on its official bf16 weights (1.7B: nothing to gain).

### 21.3 How it runs

- **FP8 Linear**: weights stay FP8 with one scale per output row. Compute dequantizes one layer at a time to bf16
  (works on any CUDA); where `torch._scaled_mm` row-wise works on sm_120 it multiplies in FP8 directly.
- **int8 ConvRot**: per-row int8 weights whose input axis was rotated by a fixed 256-wide Hadamard matrix; the
  input is rotated the same way before the existing `Int8Linear`.
- **Precision `fp8`** joins the contract's precision lists (an API bump); a variant can fix its own precision.
- **A variant can come from more than one repo** (configs from the official repo, weights from the community one).
- Phases 2 and 3 need new CUDA engines: MiniMax H3 video has no CUDA in h3.c (SGLang as a child process), LTX needs a
  torch port of the MLX pack.

### 21.4 Phases

1. Shared: the contract (`fp8`, multi-repo source, variant precision), `Fp8Linear`, ConvRot int8. Qwen Image and
   MiniMax H3 Image quantized variants, ZONOS3's CUDA build. Tested on the spot VM: CUDA goldens and PSNR against
   the official path on the same GPU.
2. MiniMax H3 video on CUDA with community FP8. Code done (2026-11-21): variant `base-fp8`, SGLang 0.5.23 as a child
   server (`packs/minimax_h3/sgl.py`), Comfy-Org pruned FP8 DiTs + int8 ConvRot text encoder. Status 2026-11-21: `base-fp8`
   downloaded (with `HF_HUB_DISABLE_XET=1`); text-to-video 864 × 480 × 107, 6 steps runs through the app on gcp (1.5 s/step,
   76 s start to MP4, 32.5 GB VRAM) after four SGLang fixes (docs/handoff.md, "M9 phase 2 notes", sixth pass). Open:
   Stop, first frame, Ref2VA. MiniMax H3 Image base-fp8 with
   Enhance on ran on gcp.
3. LTX-2.5 on CUDA (torch port) with community FP8 / int8. Code done (2026-11-21, section 21.6 steps 0-5, 7):
   `packs/ltx/torch.py` on the vendored ltx-core, variants `distilled-fp8` and `dev-int8`; all 5 workflows run on CPU
   with tiny weights, the port matches mlx.py on the same tiny weights. Not run on the VM yet (step 6). Refused on
   NVIDIA for now: LoRA rows, Prompt Relay beats (Auto length works: ltx-core has the duration head). 2026-11-22
   (owner): distill routing from model.yaml `distill_from` (audio to video eligible on gcp), the ltx `lora` block
   MLX only (`lora.runtime: mlx`: no LoRA Stack on NVIDIA), CPU-only torch runners strict.

### 21.5 Known issues

- The MiniMax H3 licence excludes the EU, the UK, the Republic of Korea and the US (its "Applicable Territory"):
  the owner decided (2026-11-21) to keep the spot VM in the EU and use MiniMax there for evaluation only, and
  accepted both MiniMax licences (docs/handoff.md, M9 phase 2 notes).
- Publisher-measured quality (LPIPS against bf16) is worse for FP8 than for int8 ConvRot in every case; the
  platform shows the variant names plainly so the choice stays visible.
- The Qwen Image weights are under the Qwen Research licence upstream, not Apache 2.0 as our pack says (found by
  the scout; to fix separately).

### 21.6 Phase 3: LTX-2.5 on NVIDIA

Status: built 2026-11-21 except step 6 (the VM renders); the owner's three decisions of 2026-11-22 (distill
routing by `distill_from`, LoRAs MLX only, CPU-only torch strict) built the same day: docs/handoff.md, "M9 phase 3
notes". The design below rests on two read-only research passes: HF API metadata, safetensors headers read by range request, and source trees. No weights were downloaded. The scout is [reference-notes/nvidia-quantized.md](../reference-notes/nvidia-quantized.md) section 1. The scratch headers are in the session scratchpad and are not kept.

**Engine: Lightricks' ltx-core, vendored and run in-process in the torch runner.** It is the only engine that covers all 5 workflows (text to video, image to video with keyframes, retake, extend, audio to video) with the Mac pack's behaviour. The vendored `ltx_core_mlx` is a port of this code (packs/ltx/vendor/VENDORED.md), so `mlx.py`'s stages translate nearly line for line. The pack gains a second runtime: `torch: {env: torch, devices: [cuda]}` and a `packs/ltx/torch.py`. Core already loads one module per runtime (core/pack.py `load`). There is no new venv and no child process.

Rejected options:
- diffusers 0.43: it loads the unsloth file most easily (4091 of 4091 keys after 2 renames). But it has no retake, extend or clean-audio audio to video, so 3 of 5 workflows would need our own loops. Its keyframe and noise rules differ from the Mac's. Effort 35–50 h.
- SGLang 0.5.23: only a first frame and an optional last frame, no audio input, no masks (`stages/ltx_2/denoising.py:850–946`). It covers 2 of 5 workflows and quantizes the gated bf16 Diffusers repo online.
- vLLM-Omni: T2V and first-frame I2V only (`recipes/LTX/LTX-2.5.md`). 2 of 5, and not in our stack.

**Coverage.** All 5 workflows run on NVIDIA. Left Mac-only for now:
- LoRA rows on the NVIDIA variants (refused with a RunError; owner, 2026-11-22: the torch runner publishes no ltx `lora` block and no `ltx/lora-stack`, model.yaml `lora.runtime: mlx`). The pack bakes only. A bake into FP8 already fails in core, and a bake into a ConvRot int8 weight would add an unrotated delta. Adapters on torch are a later step.
- Prompt beats (Prompt Relay) and Auto length (duration head), but only if ltx-core at the pin does not have them. Unknown today; the spike below checks. If they are missing, NVIDIA refuses them with a RunError, and porting each one is about 3 h later: `prompt_relay.py` is 269 lines and `duration_head.py` is 307 lines in the MLX vendor.

#### Weights (owner's rule: community FP8, else community int8, else official)

| Component | Build | Repo @ revision, file | Bytes |
|---|---|---|---|
| Distilled transformer | community FP8, per-row (torchao layout, fits core `Fp8Linear`) | unsloth/LTX-2.5-FP8 @ 9001a61f, `LTX-2.5-FP8.safetensors` | 19,056,002,016 |
| Dev transformer | no FP8 exists anywhere; community int8 ConvRot (comfy_quant `int8_tensorwise`, convrot, group 256, the phase-1 wire format) | DmitryDB/LTX-2.5-ComfyUI-Quants @ a7136f48, `diffusion_models/ltx-2.5-22b-dev-transformer-int8-convrot.safetensors` | 21,638,185,024 |
| Gemma 4 text encoder + projection | no FP8 exists (LTX's Gemma is a fine-tune: norm hashes differ from google/gemma-4-12b(-it)); community int8 ConvRot | DmitryDB @ a7136f48, `text_encoders/gemma4-12b-with-proj-ltx-2.5-int8.safetensors` | 15,504,650,554 |
| Connector (video and audio stacks + projection) | bf16 (small; dev and distilled are byte-identical per tensor) | unsloth @ 9001a61f, `connectors/*` (2 shards, diffusers keys) | 6,344,489,096 |
| Upscaler, conv video VAE, audio VAE + vocoder, duration head | official only | Lightricks/LTX-2.5 @ 5e6e710 (the pack's pin; same LFS sha at head 49b093a) | 2,816,758,904 |

Why the DmitryDB text encoder and not unsloth's (`LTX-2.5-text_encoder-INT8-ConvRot`, 13.03 GB):
- DmitryDB's file has the official file's own layout: `model.layers.*`, `text_embedding_projection.*`, and the tokenizer stored as U8 tensors, which `_tokenizer` in mlx.py already reads.
- Its format is the Comfy ConvRot that phase 1 proved (MiniMax: cosine 1.0000 against bf16).
- unsloth's file has HF-multimodal keys, no projection and no tokenizer, and its Hadamard ordering was not checked.
- Fallback if the cosine check below fails: unsloth's text encoder, with the projection taken from the connector shards.

Not downloaded on NVIDIA:
- The distilled LoRA 450 (8.9 GB). The distilled transformer is the dev one with that LoRA fused at strength 1 (the comment above model.yaml `prefused`). unsloth says its file is tensor-identical to the official distilled bf16 (README lines 31–59). So on CUDA, `ltx/distill` hands stage 2 the distilled-fp8 transformer instead of baking: no bake into int8, and one download less.
- The 42 GB official distilled file.
- The gated LTX-2.5-Diffusers repo (our token gets 403 there).

**Download:**
- distilled-fp8: 43.72 GB (43,721,900,570 B plus a few KB of connector configs)
- dev-int8 adds 21.64 GB
- Both: 65.36 GB, against 122.02 GB for the official pair.

Table 21.2 changes: the dev row becomes "community int8 ConvRot (DmitryDB)" instead of Lightricks'. Add rows for the text encoder (DmitryDB int8 ConvRot) and the connector (bf16, unsloth).

**Licence.** The LTX-2.x Community License Agreement (licence date August 11, 2026; text read from the unsloth LICENSE, the same text DmitryDB ships). Both community repos are Derivatives under it, so being ungated does not lift it: `must_accept: true`, and the owner passes `--accept-licence` once on the download.

Terms the owner should know:
- Free and worldwide.
- Entities with annual revenue of at least $10M need a paid licence (2.1).
- The Non-Commercial Purpose excludes revenue-generating use and use "in direct interactions with or that has impact on end users" (2.2).
- No territory limit except sanctions (7). The EU is fine.
- No military use (Attachment A, item 17), and no training of competing models (item 18).

Two open checks:
- The Gemma licence inside the text encoder.
- Lightricks/LTX-2.5 reports `gated: auto` in the HF API, but the pack says `gated: false`. The VM's HF token must have that gate accepted for the 4 official files.

#### Build steps

0. **Spike, the go/no-go gate (about 4 h, on the Mac, no GPU).**
   - Vendor the ltx-core subset at Lightricks/LTX-2 `daa6b16`: the transformer, connector, Gemma text encoder, conv VAE, audio VAE, vocoder, upsampler, guiders, conditioning and patchifiers. Find them by walking imports, as was done for MLX. Copy them unchanged into `packs/ltx/vendor/ltx_core/` with its Apache-2.0 LICENSE and a VENDORED.md entry. Do not vendor ltx-pipelines: its av and openimageio dependencies stay out, and ffmpeg is already in the image.
   - Build each component on the meta device in `runner_service/.venv` (torch 2.15, transformers 5.23).
   - Check 1: does ltx-core's Gemma 4 build on transformers 5.23? ltx-core pins `transformers>=5.8,<5.15` because 5.15 broke `gemma4_unified`. If it fails, make the smallest patch in the vendored copy and list it in VENDORED.md. Do not add a second venv.
   - Check 2: does ltx-core at this commit have Prompt Relay and the duration head?
   - Check 3: the key and shape match of the renamed headers for all 4 new files (step 4's first test).
   - Check 4: dequantize one DmitryDB text-encoder layer and one dev layer, and compare them with the local official bf16 by cosine (5 MB range reads, as for MiniMax).
1. **Contract (fixtures only).** `fp8`, per-variant `devices` and `sources` already exist since API 1.33, so no API bump is expected.
   - Update `contracts/fixtures/registry/ltx.*` (the model node's variant enum gains `distilled-fp8` and `dev-int8`, its precision enum gains `fp8`, and the variant help mentions NVIDIA) and the `graphs/palette` fixture, as was done for Qwen.
   - Run `make contracts-check`.
2. **Core (small).**
   - The pack code hash covers every top-level `*.py` of the pack, not only the runtime module. Today `minimax_h3/sgl.py` and a shared LTX module would escape cache keys.
   - A `lora.renames` entry may carry `runtime: mlx`, so the torch runner publishes an empty rename list. ltx-core keeps the files' own names (`to_out.0`, `ff.net.0.proj`, `linear_1`).
   - A bake into a rotated `Int8Linear` raises a RunError, if it does not already.
3. **Pack.**
   - Move mlx.py's runtime-neutral helpers into `packs/ltx/common.py` as a pure move: media I/O, schedules and sigma tables, keyframe and grid math, the checks. Re-run the Mac goldens (`tests/test_ltx_golden.py`) once.
   - `torch.py` ports the stages over vendored ltx-core:
     - encode, keyframes, two-stage sample, upscale, decode
     - encode-guided, encode-video, encode-audio, retake, extend, sample-guided
     - `distill` on CUDA returns the handle of the variant's `distill_from` (distilled-fp8). Data-driven: model.yaml `distill_from` (owner, 2026-11-22: a key of its own, published so the dispatcher can route a2v; it replaced the first build's `shares_from` reuse).
   - `rename(component, key)` covers three cases:
     - unsloth to ltx-core: `._weight_qdata`/`._weight_scale` → `.weight`/`.scale`; strip `unsloth_root::`; the inverse of diffusers' `convert_ltx2_transformer_to_diffusers` (loaders/single_file_utils.py:4151) plus `prompt_adaln` → `prompt_adaln_single`, `audio_prompt_adaln` → `audio_prompt_adaln_single`, `time_embed` → `adaln_single`, `audio_time_embed` → `audio_adaln_single`; add the `model.diffusion_model.` prefix. Checked 4091 of 4091 keys, 0 shape mismatches.
     - the connector shards: the inverse of `_convert_non_diffusers_ltx2_lora_to_diffusers` (`aggregate_embed` → `text_proj_in`). Not yet checked.
     - DmitryDB `comfy_quant` int8: the MiniMax Image pattern, through the `rotated` hook (group 256).
   - Layers kept in bf16 in the unsloth file (`to_gate_logits`, proj_in/out, timestep `linear_1`) stay dense. The quantized adaLN and timestep `linear_2` take core's existing `FP8_MIN_ROWS` dequant path.
4. **model.yaml** (sketch; byte counts from the HF API):

   ```yaml
   runtimes:
     mlx: {env: mlx}
     torch: {env: torch, devices: [cuda]}   # the NVIDIA variants: torch.py on vendored ltx-core
   variants:
     distilled: {..., devices: [mps]}       # official stays the Mac's; lift once run on CUDA
     dev: {..., devices: [mps]}
     distilled-fp8:
       dir: ltx-2.5-nvidia
       title: Distilled fp8
       precision: [fp8]
       component_precision: {transformer: [fp8], text_encoder: [int8]}
       devices: [cuda]
       files: {transformer: LTX-2.5-FP8.safetensors, connector: "connectors/*.safetensors",
               text_encoder: text_encoders/gemma4-12b-with-proj-ltx-2.5-int8.safetensors}
       source: {repo: Lightricks/LTX-2.5, revision: 5e6e71018ee1756ed329b697a7b4aedc934dfce9,
                allow_patterns: [latent_upscale_models/ltx-2.5-latent-spatial-upscaler-x2-bf16-1.0.safetensors,
                                 vae/ltx-2.5-video-vae-conv-bf16.safetensors, vae/ltx-2.5-audio-vae-bf16.safetensors,
                                 model_patches/ltx-2.5-duration-head-bf16.safetensors],
                licence: ltx-2-community, must_accept: true, gated: false, bytes: 2816758904}
       sources:
         - {repo: unsloth/LTX-2.5-FP8, revision: 9001a61f9b788e21ae7e7e2eca2ea2ee66041570,
            allow_patterns: [LTX-2.5-FP8.safetensors, "connectors/*"], gated: false, bytes: 25400491112}
         - {repo: DmitryDB/LTX-2.5-ComfyUI-Quants, revision: a7136f485a7a469cbcc4fbacc067cf880766d754,
            allow_patterns: [text_encoders/gemma4-12b-with-proj-ltx-2.5-int8.safetensors], gated: false,
            bytes: 15504650554}
     dev-int8:
       dir: ltx-2.5-nvidia
       title: Dev int8
       precision: [int8]
       component_precision: {transformer: [int8], text_encoder: [int8]}
       devices: [cuda]
       shares_from: distilled-fp8
       shares: [text_encoder, connector, upscaler, vae, vae_encoder, audio_vae, vocoder, audio_encoder]
       files: {transformer: diffusion_models/ltx-2.5-22b-dev-transformer-int8-convrot.safetensors}
       source: {repo: DmitryDB/LTX-2.5-ComfyUI-Quants, revision: a7136f485a7a469cbcc4fbacc067cf880766d754,
                allow_patterns: [diffusion_models/ltx-2.5-22b-dev-transformer-int8-convrot.safetensors],
                licence: ltx-2-community, must_accept: true, gated: false, bytes: 21638185024}
   ```

   The `bytes` for unsloth excludes the connector configs (a few KB): fill it from the API at the pin. The `peak_gb` values are MLX measurements. Keep them, and change them only if the VM run shows a plan refusing or running out of memory.
5. **Tests with fakes** (Mac and CI, no GPU):
   - `test_ltx_torch_keys.py`: rename and meta-build each component, then strict key and shape match against the committed header fixtures. These are keys, dtypes and shapes only, gzipped, for the unsloth FP8, the connector shards, the DmitryDB text encoder and the DmitryDB dev file.
   - `test_ltx_torch.py`:
     - (a) Parity: the same tiny random official-named weights loaded into `ltx_core_mlx` and `ltx_core` give a close transformer forward and VAE decode. This is the port's real correctness check, because CUDA bytes will never match the MLX goldens. Skipped without mlx.
     - (b) All 5 workflows run end to end on CPU with a tiny config, producing an MP4 of the right shape.
     - (c) On a faked cuda the models key lists distilled-fp8 and dev-int8 and not distilled or dev; the Mac lists the opposite.
     - (d) A LoRA row on a CUDA variant gives a RunError.
     - (e) `distill` on dev-int8 gives the distilled-fp8 transformer.
   - `tests/test_packs.py` already checks that both runtimes emit the canonical schemas.
   - Gates: `make test lint`, `make contracts-check`, and the contract suite on `helmstudio-test`.
6. **VM (RTX PRO 6000): one render per new path, at the golden size 448×256×49.** These two renders are recorded as `<case>.cuda.*` goldens, like qwen3_tts. Record s/step, peak VRAM and wall time for each in the reference notes, "Measured".
   - distilled-fp8, text to video. It covers the FP8 DiT, the int8 text encoder, the connector, the upscaler, the VAE and the audio.
   - dev-int8, audio to video with one keyframe. It covers the guided int8 DiT, the VAE and audio encoders, the `distill` swap to FP8 at stage 2, and the upscale.
   - Retake and extend share that guided loop and encoder. Their masks are checked by test (b), so they get no VM render.
   - There is no PSNR against the official path: that would need the 122 GB official download. Judge by eye instead, saved to docs/mock/checks/m9/.
7. **Docs.** Notes in docs/handoff.md ("M9 phase 3 notes"), the table in 21.2, the quantized-path list in deploy/gpu-host/README.md, and the "CUDA" section in packs/README.md for the second runtime.

#### Effort: about 34–42 h

| Step | Hours |
|---|---|
| 0 Spike (vendor, meta builds, transformers check, key and cosine checks) | 4 |
| 1–2 Fixtures, core (code hash, runtime renames, ConvRot bake guard) | 2–3 |
| 3 common.py split + Mac golden re-run | 2 |
| 3 torch.py: two-stage t2v and i2v, keyframes, decode with audio | 10–12 |
| 3 torch.py: guided encode, retake, extend, audio to video, distill swap | 6–8 |
| 4 model.yaml variants | 1 |
| 5 Tests with fakes (parity, workflows, gating) | 5–6 |
| 6 VM: 65 GB download, 2 renders, goldens | 3–4 |
| 7 Docs | 1–2 |

Add about 3 h each for Prompt Relay and the duration head if ltx-core lacks them.

#### Risks

- **transformers pin.** Whether ltx-core's Gemma 4 runs on transformers 5.23 is unknown until the spike. The fallback is a patch in the vendored copy.
- **The DmitryDB dev int8 is "pre-release".** Its README says the quality A/B is pending and it was tested only in ComfyUI on a 4090. The repo was modified 2026-11-20, so pin the revision. The fallback is only a source change: Lightricks' `ltx-2.5-22b-dev-transformer-comfy-int8-convrot.safetensors` (21.50 GB, same format), or the official dev bf16 cast to FP8 at load (42 GB).
- **FP8 quality.** unsloth's own LPIPS is 0.366 for FP8 against 0.265 for its INT8-ConvRot, and the owner's FP8-first rule picks the weaker file. On Qwen, `_scaled_mm` cost about 5 dB more than dequant. `RUNNER_FP8_MATMUL=dequant` is the knob, so compare both on the first render.
- **Speed of the guided dev path.** It runs 4 passes per step for 30 steps on int8 ConvRot (dequant per layer on CUDA). s/step is unknown until the VM run.
- **Connector rename and int8 checks.** The connector renames and the DmitryDB text-encoder and dev key match are not yet checked. Only the transformer was matched, 4091 of 4091. The spike settles them.
- **Two runtimes in one pack is new here.** Schema identity is tested, but residency and memory plans were only measured on MLX.
- **New goldens.** CUDA output will not match the MLX bytes. Only the CPU parity test ties the two ports together.
- **Behaviour gaps.** LoRAs (and possibly beats and Auto length) differ between Mac and NVIDIA until adapters and ports land. The variant names say "fp8" and "int8" plainly.

Sources:
- the two research passes: HF API `?blobs=true`, range-read headers of unsloth/LTX-2.5-FP8 @ 9001a61f, DmitryDB/LTX-2.5-ComfyUI-Quants @ a7136f48 and Lightricks/LTX-2.5 @ 5e6e710 / 49b093a
- unsloth README lines 31–59 and 197
- diffusers 0.43.0 (`loaders/single_file_utils.py:4151`, `loaders/lora_conversion_utils.py`, `pipeline_ltx2_dfr.py:1266`)
- Lightricks/LTX-2 @ daa6b16 (`fp8_scaled_mm.py:34`, the pyproject pins)
- sglang v0.5.23 (`docs/cookbook/diffusion/LTX/LTX2.5.mdx`, `stages/ltx_2/denoising.py`)
- vllm-omni @ 286d4a6 (`recipes/LTX/LTX-2.5.md`)
- the local official headers in ~/GenAI/models/ltx-2.5
- packs/ltx/model.yaml and mlx.py
- docs/handoff.md, "M9 phase 1 notes"
