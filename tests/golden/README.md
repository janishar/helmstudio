# Qwen Image goldens

Reference outputs from the old Qwen Image 2.1 studio (`~/GenAI/qwen-image-2.1-studio`), used to
check the new torch runner's `qwen-image` workflow. `qwen-image/<case>.png` is the image (RGBA, as the studio saves
it); `<case>.json` holds every setting needed to reproduce it, the sigmas and timesteps actually used, the scheduler
config, versions, device, timings and peak memory.

## How they were made

    ~/GenAI/qwen-image-2.1-studio/.venv/bin/python tests/golden/record_qwen.py

`record_qwen.py` imports the studio's own `qwen_image_2_1.generate` (no web server) and does what its CLI does for a
text-to-image render: `load_pipe(..., int8=True)`, `free_text_encoder_after_encoding`, then `pipe(prompt, width,
height, num_inference_steps=40, generator=torch.Generator("cpu").manual_seed(seed))`. One pipeline is loaded per
case and freed before the next, in one process. No negative prompt, `true_cfg_scale` 1.0 (no CFG), KV cache on,
no tiling, not transparent. The CLI always passes `steps=40`; Turbo ignores it because its `model_index.json` bakes
in 8 `sample_sigmas` (no dynamic shift). Base uses `linspace(1, 1/40, 40)` with exponential dynamic shift
(`mu` from the latent sequence length) and `shift_terminal` 0.02.

Recorded on: Apple M-series, 64 GB, macOS (Darwin 27.2), MPS, torch 2.14.0, diffusers 0.42.0.dev0 (git main),
transformers 5.17.0, Python 3.12.

| case | variant / precision | size | seed | load s | s/step | decode s | total s | peak GB (MPS driver) |
|---|---|---|---|---|---|---|---|---|
| turbo-768-s42 | turbo int8 | 768x768 | 42 | 11.8 | 2.00 | 2.9 | 29.9 | 21.0 |
| turbo-1024-s7 | turbo int8 | 1024x1024 | 7 | 10.4 | 3.42 | 4.9 | 40.4 | 24.4 |
| turbo-1344x768-s123 | turbo int8 | 1344x768 | 123 | 10.3 | 3.39 | 5.0 | 40.1 | 22.3 |
| base-768-s42 | base int8 | 768x768 | 42 | 12.6 | 2.28 (40 steps) | 3.1 | 105.8 | 21.0 |

Memory after load is 18.4 GB for both variants at int8 (transformer blocks and text-encoder layers int8; vision
tower, embeddings and VAE bf16). Peak is sampled every 100 ms from `torch.mps.driver_allocated_memory()`, so short
spikes may be missed. Load times are with the checkpoint warm in the page cache.

## Edit goldens (`qwen-image-edit/`)

Multi-reference edits, recorded by the same script with the old studio's edit path (what its CLI does for
`--input a.png,b.png` and no `--ratio`/`--enhance`):

    ~/GenAI/qwen-image-2.1-studio/.venv/bin/python tests/golden/record_qwen.py --cases edit-e1 edit-e2 edit-e3

The references are text-to-image goldens from `qwen-image/` (RGBA PNGs), so every input is reproducible; each
sidecar names them with their sha256, plus `noise_seed`, `size_rule`, `auto_size_would_be` and every ref's
`condition_size`. All turbo int8, 8 steps, no CFG, KV cache on, not tiled, not transparent.

| case | refs | prompt (short) | seed | size (how chosen) | load s | s/step | decode s | total s | peak GB |
|---|---|---|---|---|---|---|---|---|---|
| edit-e1 | turbo-768-s42 (fox, 768²) | `<image1>` -> summer meadow, keep the fox | 42 | 1024x1024 (auto) | 10.2 | 3.74 | 5.4 | 48.1 | 25.8 |
| edit-e2 | fox 768², then turbo-1024-s7 (neon sign, 1024²) | fox from `<image1>` under the sign from `<image2>` | 7 | 1024x1024 (auto, last ref) | 10.5 | 4.38 | 5.7 | 56.3 | 27.7 |
| edit-e3 | turbo-1344x768-s123 (terraces, 1344x768) | `<image1>` -> snowy winter | 123 | 1024x1024 (explicit; auto would be 1344x768) | 9.5 | 3.76 | 5.3 | 47.1 | 25.8 |

After load 18.4 GB, as text-to-image; references add about 1.5 GB each to the peak (24.4 GB for a 1024² text-to-image; their tokens join the sequence).
e1 recorded twice in separate processes: identical PNG bytes (PSNR inf), so the same-stack bit-exact rule holds
for edits too. The output is RGBA, like text-to-image.

### How the old studio preprocesses references (the pack must do the same)

Sources: `qwen_image_2_1/generate.py` (`main`, `plan_size`, `noise_seed`) and diffusers'
`pipelines/qwenimage21/pipeline_qwenimage21.py` (`__call__`, `_get_qwen_prompt_embeds`, `prepare_latents`).

- **Limits.** At most 10 references (`generate.py`: "supports at most 10 input images"); order matters. Files are
  opened with `PIL.Image.open` and passed as-is (mode kept; the goldens are RGBA). Any size; nothing is rejected.
- **Output size.** Edit with no size given: the CLI passes `width=height=None` and the pipeline uses
  `calculate_dimensions(1024*1024, w/h of the LAST reference)`: `width = sqrt(area*ratio)`, `height = width/ratio`,
  each `round(x/32)*32` (about 1 MP, last ref's aspect; a square ref gives 1024x1024, 1344x768 gives 1344x768). Both
  sides given: used as given. (The CLI's one-side form follows the last ref's aspect via `plan_size`, sides
  `round(x/32)*32` clamped 256-4096.) Then both sides are floored to a multiple of 32.
- **Reference resize (one resize feeds both encoders).** Each ref is converted to RGBA (`img.convert("RGBA")` if
  not already), then sized independently to `calculate_dimensions(1024*1024, its own w/h)`, i.e. ~1 MP at its own
  aspect, sides rounded to 32, regardless of the output size (a 768² ref is upscaled to 1024²). Resample is
  Lanczos (`VaeImageProcessor`, `resample="lanczos"`).
- **VAE input.** `VaeImageProcessor(vae_scale_factor=16, vae_latent_channels=64).preprocess(img, w, h)`: Lanczos
  resize, all **4 channels (RGBA)** scaled to [-1, 1], shape (1, 4, 1, h, w). Encoded with `sample_mode="argmax"`
  (the posterior mean, no sampling, so the generator is not consumed), then `(z - latents_mean) / latents_std`.
  Each ref becomes (h/16)*(w/16) tokens of 64 channels (plain flatten, no 2x2 patching), refs concatenated in order
  and **prepended** to the noise tokens at every step; `img_shapes` lists each ref's (1, h/16, w/16) then the target's.
- **Vision encoder (Qwen3-VL) input.** The same Lanczos-resized RGBA image, **alpha composited over white** to RGB
  (only this copy). The `Qwen2VLImageProcessorFast` then: patch 16, merge 2 (sizes already multiples of 32, so no
  further resize within its 65 536-16 777 216 pixel bounds), rescale 1/255, mean = std = 0.5. A 1024² ref is
  (1024/32)² = 1024 vision tokens.
- **Prompt template** (raw string, not `apply_chat_template`; an empty prompt becomes `" "`), for n refs:

      <|im_start|>system\nComprehend and analyze the provided prompt.<|im_end|>\n<|im_start|>user\n<image1><|vision_start|><|image_pad|><|vision_end|> <image2><|vision_start|><|image_pad|><|vision_end|> ... <imageN><|vision_start|><|image_pad|><|vision_end|>{prompt}<|im_end|>\n<|im_start|>assistant\n

  Placeholders are separated by one space and the prompt follows the last `<|vision_end|>` directly. The user
  names references as literal `<image1>`, `<image2>` ... in the prompt text (1-based, list order). The leading
  system tokens are dropped from the hidden states (`_drop_idx`), left padding. Text-to-image has no placeholders.
- **Noise seed.** An edit does not seed the CPU generator with `seed` but with `generate.noise_seed(seed, images)`:
  `sha256(str(seed))`, then for each ref in order `update(f"{img.mode}{img.size}")` (e.g. `"RGBA(768, 768)"`) and
  `update(img.tobytes())` of the **original, un-resized** image, then `int.from_bytes(digest[:8], "big") >> 1`.
  Recorded as `noise_seed` in each sidecar. The target noise is `randn` of shape (1, 1, 64, H/16, W/16) from that
  generator.
- Unchanged from text-to-image: Turbo's 8 baked sigmas, `mu` from the **target** token count only, KV cache
  (refs and text are step-independent under `causal_condition`, prefilled on step 1, which is why step 1 is slower).

## Tolerance

- **Same stack (this machine, same venv, same torch/diffusers): bit-exact.** Case 1 recorded twice in separate
  processes gave identical PNG bytes (PSNR inf, mean abs diff 0). The noise comes from a CPU generator, and the MPS
  kernels were deterministic here, so any difference on the same stack is a real change in the pipeline.
- **Different torch / diffusers / transformers, or a re-implemented pipeline: not bit-exact; `compare.py` defaults
  to 30 dB.** bf16 matmul and attention kernels change their reduction order between torch releases, and a flow-
  matching sampler feeds each step's tiny error into the next, so the image drifts while staying the same picture.
  30 dB (mean abs diff around 2/255) is a "same image, small numeric drift" bar; this threshold is a judgement call
  not measured across versions yet. A real wiring mistake falls far below it: turbo vs base, same prompt and seed,
  is 17.3 dB (mean abs diff 23.7), and the studio measured int8 vs bf16 at about 22 dB.
- Precision (int8 vs bf16), variant, size, seed, prompt, KV cache, tiling and the RGBA prompt wrap all change the
  pixels outright; compare only against a golden with the same settings.

## Compare

    python tests/golden/compare.py tests/golden/qwen-image/turbo-768-s42.png out.png [--threshold 30]
    python tests/golden/compare.py tests/golden/qwen-image-edit/edit-e1.png out.png

Prints `psnr_db` and `mean_abs_diff`; exit 0 if PSNR >= threshold, 1 below it, 2 on a size mismatch. Needs Pillow
and numpy. `python compare.py --self-test` checks the maths.
