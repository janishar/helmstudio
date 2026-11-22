# Vendored: ltx-core-mlx

`ltx_core_mlx/` here is a subset of the package `ltx-core-mlx` 0.18.0 from ltx-2-studio
(`packages/ltx-core-mlx/src/ltx_core_mlx`), commit `4b2f609bd96d299c741bf446203b5b2cd3cb7da5` (2026-11-22: the fork's
sync to upstream ltx-2-mlx 747b30e), copied **unchanged** (byte for byte): the 69 modules that the ltx pack
(`../mlx.py`) imports and everything they import, found by walking their imports. Nothing else of the package
(pipelines, trainer, CLI, web) is copied: the pipeline itself is re-written in `../mlx.py`, following
`ltx_pipelines_mlx.distilled` and its helpers.

`model/transformer/sparse_attention.py` is from the fork's `e6e15b7` (2026-11-22: the sparse kernel's self-check
compares against exact fp32; inert unless `LTX2_SOL_TAU` is set). Every other module is unchanged at `e6e15b7`.

- Licence: MIT, `LICENSE` next to this file (Copyright (c) 2025 dgrauet, (c) 2026 Janishar Ali). It is a port of
  Lightricks' LTX-2 reference code.
- `ltx_core_mlx/loader/official_pack.py` holds key-renaming, transpose and quantization rules vendored from
  mlx-forge (https://github.com/dgrauet/mlx-forge, Apache-2.0: `recipes/ltx_25.py`, `recipes/ltx_25_text_encoder.py`,
  `transpose.py`, `quantize.py`), as its docstring says. The pack uses only those rules from it (no virtual pack is
  ever built or written).
- The weights (`~/GenAI/models/ltx-2.5`) are under Lightricks' own model licence, which none of this covers.
- Runtime dependency: `mlx-arsenal` (timestep embedding, Euler step), pinned to 0.2.4 as in the studio's lockfile.

M4 stage B added, also unchanged: `conditioning/prompt_relay.py` (Prompt Relay) and `duration_head/` (the
DurationHead for auto length). `loader/fuse_loras.py` is the reference the core's MLX bake follows
(core/loaders_mlx.py); the image preprocessing (`ltx_pipelines_mlx.utils.media_io`) and the image conditioning
order (`_orchestration.combined_image_conditionings`, `helpers.create_noised_state`) are re-written in `../mlx.py`.

M7 pack 1 added, also unchanged: `utils/audio.py` (`load_audio`: ffmpeg → float32 PCM, as the old studio reads a
retake source's sound and audio-to-video's input). The retake / extend / audio-to-video pipelines
(`ltx_pipelines_mlx.retake`, `a2vid_two_stage`, `utils.samplers.guided_denoise_loop`, `scheduler.ltx2_schedule`,
`utils.constants.DEFAULT_NEGATIVE_PROMPT`, `ltx_core_mlx.utils.image.load_video_frames`) are re-written in `../mlx.py`.

0.18 refresh (from `b63613e`, 2026-11-22): 16 modules re-copied (transformer `model.py` / `transformer.py` /
`attention.py` / `feed_forward.py`, the four conditioning types, `prompt_relay.py`, `duration_head.py`,
`fuse_loras.py`, `loader/__init__.py`, `video_vae.py` / `tiling.py`, `diffusion_steps.py`, `guidance/__init__.py`) and
4 added because they import them (`guidance/nag.py`, `model/transformer/sparse_attention.py`, `loader/helpers.py`,
`loader/lora_adapters.py`). Their new switches (NAG, `LTX2_COMPUTE_DTYPE`, `LTX2_SOL_TAU`, `LTX2_LORA_MODE`, the audio
Prompt Relay mask) are inert unless set or passed; the pack passes the audio relay mask, NAG and the compute dtype
(plan 19 phase 2), and none of the others. What the pack ported by hand is in
GOTCHAS.md ("0.18 sync").

Plan 19 phase 4 (Recipe Detail) added, unchanged from `e6e15b7`: `conditioning/types/keyframe_slots.py` (DFR's
generated keyframe slots; it imports only modules already here). DFR's canvas layout (`ltx_pipelines_mlx.dfr_layout`
`resolve_canvas`), its RoPE fps (`dfr.conditioning_fps`), the slots' noise (`helpers._noise_generated_keyframe_slots`),
the IC reference (`iclora_utils.reference_conditioning_from_latent`) and the a2v padding (`a2vid_dfr.fit_audio_tokens`)
are pipeline code, re-written in `../common.py` and `../mlx.py`.

To refresh: copy the same files from a newer commit (re-walk the imports), re-record the goldens on the fork
(`tests/golden/record_ltx.py`), re-run `tests/test_ltx_golden.py` and update this note.

# Vendored: ltx-core (M9 phase 3, the NVIDIA variants)

`ltx_core/` here is a subset of Lightricks' `ltx-core` 1.6.2 (`packages/ltx-core/src/ltx_core` of
https://github.com/Lightricks/LTX-2), commit `daa6b16fc7f0f1628f2c9e36a303e3312b333cc6` (2026-11-19), for the torch
runtime on CUDA (plan 17, section 21.6). 148 modules plus the 4 Gemma system-prompt files
(`text_encoders/gemma/encoders/prompts/`), found by walking every import (top-level and in-function) from the
transformer, the connector (embeddings processor), the Gemma text encoder, the video VAE package, the audio VAE and
vocoder, the upsampler, the duration head, the guiders, diffusion steps, patchifiers, conditioning and perturbations.
Nothing of ltx-pipelines is copied (its av and openimageio dependencies stay out). The package `__init__` files pull
in DiffVAE (`model/video_vae/transformer/`) and `color/` although the pack uses the conv VAE; they are copied as they
are. The two `fallback_na/*triton*` modules import triton and are reached only lazily from DiffVAE.

- Licence: Apache-2.0, `ltx_core/LICENSE` (the repo's root `LICENSE`). The weights are under the LTX-2.x Community
  License, which this does not cover.
- Runtime dependencies, in the torch extra: `einops` and `torchaudio` (2.11.1, pure Python, works on torch 2.15:
  `model/audio_vae/ops.py` mel and resample). Not `colour-science` (`color/`): with no SciPy it puts a MagicMock in
  `sys.modules["scipy"]`, which breaks transformers' imports; the second patch keeps `color/` unimported. ltx-core's
  own pin `transformers>=5.8,<5.15` is not followed: the runner keeps 5.23 (first patch).

Copied unchanged (byte for byte) except two patches, marked `helmstudio patch` in the files:

- `text_encoders/gemma/encoders/encoder_configurator.py` `_populate_rotary_v5`: on transformers >= 5.15 the Gemma 4
  text config is heterogeneous and a global `config.head_dim` read raises `AmbiguousGlobalPerLayerAttributeError`.
  The rotary buffers are computed from `config.per_layer_config[layer_type]` (whose `head_dim` is already the
  layer type's, so no `global_head_dim` key), as transformers 5.23's own `Gemma4UnifiedTextRotaryEmbedding` does.
  Checked: the buffers equal transformers' own CPU init for both layer types, and a tiny random 2-layer model
  encodes through `LTXGemmaTextEncoder.encode` on CPU. Older transformers (no heterogeneous config) take the old path.
- `model/video_vae/diffusion_tiling.py`: `from ltx_core.color.yuv import frames_per_yuv_gemm` moved into
  `emit_convert_bytes`, its one use (DiffVAE's tiling): the video VAE package's `__init__` imports this module, so the
  conv VAE path no longer imports `color/` and its colour-science.

The pack (`../torch.py`) uses ltx-core's models, configurators and key rules; the pipeline (ltx-pipelines) is
re-written there after `../mlx.py`.

To refresh: copy the same files from a newer commit (re-walk the imports), re-apply the patch if still needed, re-run
the key tests (`tests/fixtures/ltx_nvidia_headers/`) and update this note.
