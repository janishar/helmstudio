# ltx pack: gotchas

From the old studio's code and the scout's note (docs/reference-notes/ltx.md, section 8), plus what the port found.

## Exactness

- **The port is bit-exact.** On the goldens' mlx (0.32.2) every recorded latent (prompt embeddings, stage 1, the
  upscaled latent, stage 2, both audio latents), every decoded frame and the WAV equal the old studio's
  (`tests/test_ltx_golden.py`, case `t2v-448x256-49f-s42`). Any change to the order of noise draws, a seed offset, a
  blend or a dtype breaks that; re-run the check after touching `mlx.py` or `vendor/`.
- **Seeds**: stage-1 video noise `seed`, audio `seed+1`, the ancestral loop `seed+10000` (video then audio each step,
  float32), stage-2 renoise `seed+2` for video *and* audio (re-seeded: the two draws are correlated; keep it).
- **Blends**: video uses the scalar blend `noise * sigma + clean * (1 - sigma)` with a Python sigma in both stages
  (bf16 cannot hold 0.05; the mask blend adds ~3e-3 per element); stage-2 audio uses `noise_latent_state`'s
  `mask * sigma` blend. Do not "fix" either.
- **mlx versions drift**: the ancestral stage-1 sampler amplifies kernel differences. On mlx 0.32.4 the embeddings
  already differ in the 6th decimal and the clips differ in detail, not composition (448x256x49: mean PSNR 23.1 dB,
  worst frame 18.7, log-mel 1.13 dB; 512x320x57: 32.5 / 30.8 dB, 0.83 dB). The cross-version bars in
  `tests/test_ltx_golden.py` are those numbers with a margin.
- **Gemma 4**: no chat template, no system prompt, prompt stripped, left-padded to 1024 tokens (upstream right-pads;
  the goldens embody left), the *last* of the 49 hidden states post-norm and the other 48 raw.
- **fps is a model input** (RoPE time positions), not just the container rate: a different fps is a different clip.

## Weights and memory

- The official split files are read in place: `mx.load` is lazy, keys are renamed and conv weights transposed with
  the vendored mlx-forge rules (`vendor/ltx_core_mlx/loader/official_pack.py`); nothing is converted to disk (the old
  studio wrote a "virtual pack" into its checkout; this pack writes nothing, the tokenizer goes to a temp dir).
- int8 / int4 quantize only `transformer_blocks.*` 2-D weights of the DiT and Gemma's attention/MLP projections;
  VAEs, upscaler, vocoder and connector never. Kept tensors are evaluated before quantizing (quantizing can evict lazy
  buffers), then one tensor at a time.
- Tensors keep their stored dtype (`dtype: stored`: the DiT's scale-shift tables are F32); only the vocoder is cast,
  to fp32 (BigVGAN in bf16 loses 40-90 % on spectral metrics).
- The connector is two components' worth of files: its 8-layer stacks live in the transformer file, its
  `text_embedding_projection` in the text encoder file.
- **Gemma never sits beside the DiT** (`unload_after: [text_encoder, connector]` on encode): reloading Gemma right
  before the DiT is a documented cause of macOS GPU watchdog crashes, and the old studio kept the peak near 21 GiB
  this way. Encode is cached, so a re-roll with the same prompt does not reload it.
- The DiT stays resident through decode when the budget allows (64 GB: yes); the old studio freed it because a
  resident q8 DiT across the ~8 GiB decode peak was SIGKILLed (jetsam, silently) on 32 GB machines.
- A missing upscaler file must fail loud (it does: strict load); a random upscaler gives a silent mosaic.

## GPU and Metal

- **Watchdog** (`MTLCommandBufferErrorInternal` code 14): the graph is cut into command buffers by evaluating per
  connector layer (`LTX2_GEMMA_EVAL_EVERY`, default 1; the Gemma 4 tower itself runs as one graph), every 8 DiT
  blocks (`LTX2_DIT_EVAL_EVERY`), before each loop (the noised states) and after each step. If it still fires,
  relaunch with `AGX_RELAX_CDM_CTXSTORE_TIMEOUT=1`.
- `gc.collect(); mx.clear_cache()` after a stage that frees memory; the Metal cache is capped at the residency budget.
- `mx.compile` only in the vendored RoPE helpers; nothing else (lazily loaded weights + compile have known hazards).

## Video

- Sizes floor to the 64 px grid (two-stage: stage 1 runs at half); frames are 8k + 1; the decode gives exactly
  `frames` frames. Audio is 48 kHz stereo, about 0.03 s shorter than the video; both streams are muxed whole (never
  `-shortest`).
- ffmpeg (system binary, checked when the pack loads): H.264 High yuv420p CRF 18 + AAC-LC from a temp WAV, then a
  `-c copy -movflags +faststart` remux (container only) so `moov` comes first for Range playback; the poster is frame
  0 read back from the MP4. ffmpeg's stderr never goes to a pipe the runner does not drain (#92).
- The conv VAE tiles in time once its block-3 activation passes `LTX2_VAE_DECODE_BUDGET_GB` (8): tiling changes
  pixels; the goldens never tile.

## Measured (64 GB M-series, macOS 27.2, mlx 0.32.4, int8, warm page cache; `tests/test_ltx_golden.py`)

| node | 448x256x49 @24 | 512x320x57 @25 | MLX peak GiB (process, incl. resident) |
|---|---|---|---|
| encode (load Gemma + connector, encode) | 9.2 s | 6.6 s | 21.6 / 21.9 |
| sample1 (load DiT, 8 ancestral steps) | 13.1 s | 12.2 s | 20.8 / 20.8 |
| upscale (load upscaler + VAE) | 0.4 s | 0.3 s | 21.5 / 21.5 |
| sample2 (3 steps) | 5.7 s | 8.1 s | 22.6 / 22.7 |
| decode (load audio, decode, mux, remux, poster) | 8.9 s | 2.1 s | 30.2 / 29.6 |
| **total** (cold) / re-roll with a new seed (no encode, no loads) | **37.6 s** / 30.4 s | **29.5 s** / 17.6 s | resident after: 21.4 GB |

The first case's first run paid a cold read of some files (the old studio: 26.1 s / 30.8 s for the same cases).
