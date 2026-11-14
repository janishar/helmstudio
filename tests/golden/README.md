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

Prints `psnr_db` and `mean_abs_diff`; exit 0 if PSNR >= threshold, 1 below it, 2 on a size mismatch. Needs Pillow
and numpy. `python compare.py --self-test` checks the maths.
