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
- **mlx versions drift; the port does not.** The new pack under mlx 0.32.2 (a scratch venv = envs/mlx with only mlx
  pinned) is bit-exact on both goldens: every latent sha, every frame, the WAV. Under 0.32.4 (the lock) the same
  code gives 448x256x49 mean 23.09 / worst 18.72 dB (first / mid / last 25.12 / 23.62 / 18.93; raw frames before
  H.264 23.11 / 18.63, so the mux costs nothing), log-mel 1.13 dB; 512x320x57 32.46 / 30.78 dB (raw 32.78 / 31.08),
  0.83 dB. Measured stage by stage (2026-11-15): the stage-1 noise is identical; the first difference is the
  prompt embeddings (rel 0.9 %, max 4.75); with 0.32.2's embeddings fed in, the first DiT call on identical inputs
  still differs (rel 0.3 %), and the 8-step ancestral stage 1 grows that to 7-10 % of the stage-1 latent and 18-19 %
  after stage 2 (composition holds, details move). The decoders alone (0.32.2's final latent decoded on 0.32.4):
  frames 43.5 dB, audio log-mel 0.71 dB. The kernels that changed, op by op on fixed inputs (everything else tested
  is bit-equal: int8 quantize / quantized_matmul, SDPA without a mask, rms/layer norm, rope, convs, RNG):
  `mx.fast.scaled_dot_product_attention` with an additive mask at head_dim 256 (Gemma's local layers; 0.32.4 is the
  more accurate, rel error vs float64 0.16 % against 0.36 %), the compiled `nn.silu` (1 ulp on ~0.03 % of bf16
  elements, f32 too: DiT timestep MLP, both VAEs), and bf16 matmul/addmm with a ragged M (51 audio tokens). Pinning
  mlx 0.32.2 gives bit-exact goldens but conflicts with mlx-vlm's >= 0.32.4 (docs/plan/14-dependencies.md). The
  cross-version bars in `tests/test_ltx_golden.py` are per case, just under the 0.32.4 numbers.
- **Gemma 4**: no chat template, no system prompt, prompt stripped, left-padded to 1024 tokens (upstream right-pads;
  the goldens embody left), the *last* of the 49 hidden states post-norm and the other 48 raw.
- **fps is a model input** (RoPE time positions), not just the container rate: a different fps is a different clip.

## Stage B: image to video, keyframes, beats, auto length, LoRA (docs/reference-notes/ltx.md part 11)

- **Bit-exact on the goldens' mlx (0.32.2)** (`tests/test_ltx_golden.py`, 2026-11-15): `i2v`, `kf` (frames 0 and 48),
  `beats`, `lora` (the 4 recorded fused weights included), auto length (embeddings and the DurationHead's seconds for
  the 4 prompts), audio off (frames = the t2v golden's) and the t2v goldens again: every loop's input state (noise,
  clean latent, denoise / keyframe masks, positions, relay mask), every output, every image latent, PSNR inf, WAV
  equal. Without weights (CPU): the preprocessed image pixels, the keyframe positions, the relay ranges and masks.
- **Order is everything**: the noise blend runs on the plain grid first, then the conditionings (frame 0 replaces
  latent frame 0's tokens, later frames append theirs), so conditioned tokens start clean even at strength < 1; masks
  are `1 - strength` in float32 (so stage 2's Euler output is float32); a mask that is not all 1 passes per-token
  timesteps `mask * sigma`; the ancestral stage 1 re-applies the mask after its noise; appended tokens are cut before
  the upscale and the decode.
- **Frame-0 replacement drops `keyframes_mask`** (`VideoConditionByLatentIndex.apply`): with an image at frame 0 the
  DiT gets no keyframe embedding, though the reference marks frame 0 always. Copied (the goldens embody it); fixing it
  needs new goldens from a fixed old studio.
- Keyframe images: RGB → one libx264 frame at CRF 33 and back (yuv444p with ffmpeg 9.0.2: the pixels depend on the
  ffmpeg build) → LANCZOS cover + centre crop to each stage's grid → the conv VAE encoder (`vae_encoder`, 0.59 GB),
  once per stage. The round trip is done once in `keyframes`; each sample resizes and encodes.
- **Steps**: only the whole tables end at sigma 0 (stage 1: 8, stage 2: 3); the old `--stageN-steps n` truncated
  them, leaving noise. The schema's `values_by` allows exactly those; `check_sample` refuses anything else.
- Prompt Relay: one prompt `prompt + " " + beat ...`, token ranges by incremental tokenization (refused over 1024
  tokens: the left truncation would shift every range); the additive mask gates the video cross-attention only.
- Auto length: the DurationHead (3.8 MB, held outside the residency manager) on the connector outputs; deterministic.
  Across mlx versions its seconds move by a bf16 step (0.32.4: 2.92 against 2.89 s), which can cross a grid point
  (73 against 65 frames at 25 fps).
- **LoRA, bake only** (no MLX adapter exists to copy): normalised by core/lora.py (`at: end` renames, nothing
  silently dropped; the old studio ignored unmatched keys and kohya files), merged after int8 quantization module by
  module: dequantize, + float32 delta, requantize (float32 scales and biases). Unchanged on 0.32.4 (bit-equal fused
  weights). Block streaming does not exist on our layout (the old `--low-ram` refused the official files).
- 0.32.4 against the stage-B goldens (mean / worst frame dB, log-mel dB): i2v 42.19 / 39.34 / 1.48, kf 43.15 /
  42.42 / 1.47, beats 23.25 / 16.53 / 1.53, lora 26.91 / 23.73 / 1.15; bars just under them.
- Memory: one process keeps components resident within the budget (RAM - 12 GB), so a baked transformer and the
  plain one can sit side by side (38 GB; decode peaked 50.5 GiB in the bench). Who evicts what across envs and runs
  is stage C. The Metal buffer cache is capped at the budget too, so the process footprint reached 53 GB.

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
