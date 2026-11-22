# ltx pack: gotchas

From the old studio's code and the scout's note (docs/reference-notes/ltx.md, section 8), plus what the port found.

## Exactness

- **The port is bit-exact.** On the goldens' mlx (0.32.2) every recorded latent (prompt embeddings, stage 1, the
  upscaled latent, stage 2, both audio latents), every decoded frame and the WAV equal the old studio's
  (`tests/test_ltx_golden.py`, case `t2v-448x256-49f-s42`). Any change to the order of noise draws, a seed offset, a
  blend or a dtype breaks that; re-run the check after touching `mlx.py` or `vendor/`.
- **Seeds**: stage-1 video noise `seed`, audio `seed+1`, the ancestral loop `seed+10000` (video then audio each step,
  float32), stage-2 renoise `seed+2` for video *and* audio (re-seeded: the two draws are correlated; keep it), the
  stage-2 ancestral loop `seed+20000` (0.18: both stages ancestral on 2.5, `common.ancestral`).
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
- **Frame 0 keeps its keyframe marker** (fixed 2026-11-20, the owner's decision 4). The old studio's
  `VideoConditionByLatentIndex.apply` drops `keyframes_mask`, so with an image at frame 0 the DiT got no keyframe
  embedding on latent frame 0, though the reference marks frame 0 always (t2v, retake, extend, a2v do). `mlx.py`
  `FirstFrame` kept the marker until the 0.18 sync, whose vendored `VideoConditionByLatentIndex` keeps it itself;
  later keyframe rows extend it with zeros.
  The `i2v` and `kf` goldens were re-recorded by `tests/golden/record_ltx.py` on the old studio with that one line
  fixed (a scratch copy of `ltx_core_mlx` first on `PYTHONPATH`, mlx 0.32.2): every input up to stage 1 is unchanged
  (images, embeddings, noise, positions, denoise masks), the keyframes mask is now 1/7 (i2v) and 1/8 (kf: the appended
  frame unmarked) of the tokens, and everything after it differs: old against new 42.5 / 39.1 dB (i2v, mean / worst
  frame), 43.5 / 42.6 (kf). The pack is bit-exact on 0.32.2 against the new goldens (every point, frame, WAV).
- Keyframe images (0.18, media_io bfa5755 / 2524595): EXIF orientation, ICC → sRGB, odd sides cropped → one libx264
  frame at the generation's CRF (18 from 2.4 on, 33 before: `common.image_crf` from the transformer's
  `model_version`) in yuv420p with bilinear RGB↔YUV and slice threads, and back (the pixels depend on the ffmpeg
  build) → bilinear (torch `align_corners=False`, no antialias) cover on float32 + centre crop to each stage's grid →
  x / 127.5 - 1 → the conv VAE encoder (`vae_encoder`, 0.59 GB), once per stage. The round trip is done once in
  `keyframes`; each sample resizes and encodes.
- **Steps**: only the whole tables end at sigma 0 (stage 1: 8, stage 2: 3); the old `--stageN-steps n` truncated
  them, leaving noise. The schema's `values_by` allows exactly those; `check_sample` refuses anything else.
- Prompt Relay: one prompt `prompt + " " + beat ...`, token ranges by incremental tokenization (refused over 1024
  tokens: the left truncation would shift every range); the additive mask gates the video cross-attention only.
  The studio's 0.18 also gates `audio_attn2` by default on the distilled path (0879826); the pack does not port that
  (the beats golden is recorded with `--relay-audio-strength 0`).
- Auto length: the DurationHead (3.8 MB, held outside the residency manager) on the connector outputs; deterministic.
  Across mlx versions its seconds move by a bf16 step (0.32.4: 2.92 against 2.89 s), which can cross a grid point
  (73 against 65 frames at 25 fps).
- **LoRA, bake only** (no MLX adapter exists to copy): normalised by core/lora.py (`at: end` renames, nothing
  silently dropped; the old studio ignored unmatched keys and kohya files), merged after int8 quantization module by
  module: dequantize, + float32 delta, requantize, the scales and biases cast back to their loaded dtype (0.18,
  f3c5bad: float32 ones made quantized_matmul run the layer in float32). Unchanged on 0.32.4 (bit-equal fused
  weights). Block streaming does not exist on our layout (the old `--low-ram` refused the official files).
- 0.32.4 against the stage-B goldens (mean / worst frame dB, log-mel dB): i2v 41.74 / 38.98 / 1.47, kf 43.45 /
  42.60 / 1.49 (both on the 2026-11-20 goldens), beats 23.25 / 16.53 / 1.53, lora 26.91 / 23.73 / 1.15; bars just
  under them.
- Memory: one process keeps components resident within the budget (RAM - 12 GB), so a baked transformer and the
  plain one can sit side by side (38 GB; decode peaked 50.5 GiB in the bench). Who evicts what across envs and runs
  is stage C. The Metal buffer cache was then capped at the budget too, so the process footprint reached 53 GB (now 2 GB: GPU and Metal).

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
- `gc.collect(); mx.clear_cache()` after a stage that frees memory. **The Metal buffer cache is capped at 2 GB**
  (core/loaders_mlx.py `CACHE_GB`): at MLX's default (the memory limit; the residency budget before) one VAE decode
  graph kept 27 GB of freed buffers beside a 9 GB active peak, so the process footprint reached 49 GB at 448x256x49
  (61 GB on the host with the torch runner) for 3 s, and the decode took 6.8 s instead of 1.9 s. At 0, 0.5, 2 and 8 GB
  the decode takes 1.8-1.9 s and the samplers run at the same speed; the MP4 is byte-identical (no numerics change).
- `mx.compile` only in the vendored RoPE helpers; nothing else (lazily loaded weights + compile have known hazards).

## Video

- Sizes floor to the 64 px grid (two-stage: stage 1 runs at half); frames are 8k + 1; the decode gives exactly
  `frames` frames. Audio is 48 kHz stereo, about 0.03 s shorter than the video; both streams are muxed whole (never
  `-shortest`).
- ffmpeg (system binary, checked when the pack loads): H.264 High yuv420p CRF 18 + AAC-LC from a temp WAV, then a
  `-c copy -movflags +faststart` remux (container only) so `moov` comes first for Range playback; the poster is frame
  0 read back from the MP4. ffmpeg's stderr never goes to a pipe the runner does not drain (#92).
- The conv VAE tiles in time once its block-3 activation passes `LTX2_VAE_DECODE_BUDGET_GB` (8): tiling changes
  pixels; the goldens never tile. That estimate misses the real peak: the full-resolution 128-channel stages hold
  about 0.76 GB per megapixel-frame (23.4 GB above the resident at 704x448x97, where block 3 is 0.26 GB), and block 3
  passes 8 GB only beyond 1920x1088x321, so in practice the decode never tiles and its memory grows linearly; the
  decode peak_gb says so and admission refuses what does not fit (a tiling estimate on the real peak would change
  pixels at those sizes: not done).

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

Physical memory per stage (2026-11-15, the Metal cache capped at 2 GB; the larger of the process footprint and MLX
active + cache, sampled every 20 ms, minus the components resident after the stage: what peak_gb plans; one cold
run each; before the cap the decode alone reached 49.1 / 49.8 GB footprint, 27 GB of it MLX's cache):

| stage | t2v 448x256x49 | t2v 704x448x97 | i2v 448x256x49 | resident after |
|---|---|---|---|---|
| encode (loads Gemma + connector) | 4.90 | 4.95 | 5.12 | 18.57 (then unloaded) |
| sample1 (loads the DiT; i2v also the VAE encoder) | 2.61 | 3.54 | 3.51 | 19.18 / 19.77 |
| upscale | 2.12 | 1.45 | 2.13 | 20.87 / 21.46 |
| sample2 | 3.33 | 4.18 | 4.20 | 20.87 / 21.46 |
| decode (audio, VAE, mux) | 10.57 | 23.41 | 10.57 | 21.41 / 22.00 |
| process footprint peak (whole run) | 27.7 (was 49.1) | 39.8 (was 49.8) | 28.1 | |
| run time | 26.0 s (was 30.8) | 62.7 s (was 64.5) | 25.1 s | |

## M7 pack 1: retake, extend, audio to video (docs/reference-notes/ltx.md section 12; contracts README, Media inputs)

- **Proven on the GPU** (Measured below). Without weights (CPU, `tests/test_mlx.py`) every state the loops start from
  equals the goldens' recorded sha: the retake mask (float32) and keyframes mask, extend's bf16 masks, both audio
  masks, the positions at the source fps, a2v stage 1's noise and positions, and the 30-step `ltx2_schedule`. The
  weight-loading proof is `tests/test_ltx_golden.py retake-448x256-49f-s42 extend-448x256-49f+8-s42
  a2v-448x256-49f-s42` on mlx 0.32.2 (bit-exact per recorded point), then on 0.32.4 against `DEV_BARS`.
- **Guidance**: 4 sequential DiT passes per step (cond, negative prompt, STG block 28, modality), x0 =
  cond + (cfg-1)(cond-neg) + stg(cond-ptb) + (mod-1)(cond-mod), rescaled 0.7; audio cfg 7 for retake / extend, the
  audio guider off for a2v (`MultiModalGuiderParams()`). Deterministic Euler; the mask blends x0 every step.
- **Noise**: `noise_latent_state` (mask x sigma, not the scalar blend) for retake / extend, video `seed`, audio
  `seed + 1`; a2v stage 1 is the scalar blend (as t2v). Retake's video mask is float32 (TemporalRegionMask), extend's
  bf16: the per-token timesteps differ by dtype; keep both.
- **Retake audio range** (0.18, fc399f3): the audio tokens whose time overlaps the latent frames `[S, E)`
  (`common.audio_window`, upstream TemporalRegionMask), not a proportional index. Audio off: the stream is frozen.
- **Frozen streams** (0.18, 9b91fe2): a2v's source audio (both stages; stage 2 deterministic) and a retake's audio
  when off pass sigma 0 for that modality (`video_sigma` / `audio_sigma`: its prompt AdaLN and the other modality's
  cross gate).
- **a2v RoPE** at the clip's fps (0.18, 4254a8f; `ltx/encode-audio.fps` allows only 24 today).
- **Sources** are VAE-encoded tiled (`TilingConfig.default()`) when they span more than one tile, else at once, and
  evaluated before the DiT loads (51b0332).
- **a2v stage 2** is `ltx/sample` stage2 on the handle `ltx/distill` makes: the dev transformer loaded again and the
  distilled LoRA (3320 keys, all applied) baked at load, a second residency entry (`loras: [{prefused id, 1}]`);
  the old code fused in place after stage 1. `sample-guided` unloads the plain dev transformer after it runs, so one
  transformer is resident at a time. The stage-2 audio starts from stage 1's output audio, which equals the encoded
  source exactly (mask 0: x0 is the clean latent, Euler leaves it unchanged).
- **a2v output** muxes the source part re-read at 48 kHz from `in`, `int(frames / fps * 48000)` samples; never the
  generated audio.
- **Sources**: the video is re-probed (vendored `probe_video_info`): sides on the 32 grid and at most 321 frames are
  re-checked on the file, frames floored to 8k + 1, read at their own size with the old ffmpeg arguments, encoded
  all at once (8.7 GiB MLX peak at 448x256x49, old studio 6.9; grows with mp x frames). A source without
  sound gets the old zero audio latent. Audio is read with the vendored `load_audio` (16 kHz stereo).
- **Memory plans** (`model.yaml` `plan_media`, `planned_alone`): retake / extend planned at the goldens' source
  (448x256x49), a2v alone at its template canvas; the dev templates pin int8 / int4 (`only`), so nothing is planned
  at bf16 for dev. `peak_gb` measured on this port (model.yaml header); `encode-video` re-fitted to its 8.7 GiB.
- **Measured 2026-11-19** (64 GB M-series, int8, 448x256x49 goldens): on mlx 0.32.2 retake, extend and a2v are
  bit-exact at every recorded point (embeds, source latents, loop states, finals; every frame PSNR inf, WAV
  identical), so the bake-at-reload of the distilled LoRA equals the old in-place fuse. On 0.32.4 (`DEV_BARS`):
  retake 36.06 / 34.50 dB, log-mel 0.248; extend 36.13 / 26.65 (the added frames drift most), 0.331; a2v 24.50 /
  20.79, 0.000 (source audio kept). Times: encode 9 s, retake 262 s, extend 283 s (30 steps x 4 passes), a2v 137 s
  (stage 1 105 s, the baked reload 13 s, stage 2 19 s). Process footprint: retake 26.8 GiB, extend 27.5, a2v 32.8;
  the baked load peaks 29.5 GiB over what was resident (20.3 kept). Residency may keep the baked copy after a2v: a
  retake right after it held both transformers (39.4 GiB) and peaked at a 45.7 GiB footprint, under the 52 budget.
- **Harness**: a load or loop resets MLX's peak counter; `Bench.reset_peak` keeps the node's peak across them (before
  it, a node's `peak_gib` was only what followed its last load).

## 0.18 sync (2026-11-22: ltx-2-studio 4b2f609, upstream ltx-2-mlx 0.18.0 / 747b30e)

- **Ported by hand** (mlx.py and torch.py alike; the fork's commit in brackets): stage 2 ancestral on 2.5 with noise
  `seed + 20000` (5c1ca4a); the generation's rules from the transformer header's `model_version`
  (`common.model_version`: image CRF 18 from 2.4, ancestral from 2.5; 2524595); keyframe image decode / yuv420p
  round trip / float bilinear resize (bfa5755); bake keeps the scales / biases dtype (f3c5bad); frozen streams at sigma 0 (9b91fe2); retake's
  audio window by time (fc399f3); sources tiled and evaluated before the DiT (51b0332); image latents evaluated while
  the encoder is loaded (aaa3d51); a2v positions at the clip's fps (4254a8f). The vendored
  `VideoConditionByLatentIndex` now keeps `keyframes_mask`, so `FirstFrame` is gone.
- **Not ported**: the audio Prompt Relay mask (0879826, the studio's default on distilled), DFR (`--dfr` with the
  refine-details IC-LoRA), the distilled retake / extend / a2v presets, NAG, float16 compute, block-sparse stage 2.
- **Goldens**: every ltx golden re-recorded on 4b2f609 (`tests/golden/record_ltx.py`, mlx 0.32.2; beats with
  `--relay-audio-strength 0`, as the pack). The pack is bit-exact against all ten on 0.32.2 (every point, every frame,
  the WAV) and the auto / noaudio / reseed variants pass; on 0.32.4 the bars were re-measured
  (tests/test_ltx_golden.py).

## Distilled actions (plan 19 23.10 phase 1, 2026-11-22: ltx-2-studio main e6e15b7)

- **Ported** from the fork's `retake --distilled` (`RetakePipeline(distilled=True)`: the 8-step table, one pass per
  step, deterministic Euler: upstream does not switch retake to ancestral on 2.5), `extend --distilled`
  (`ExtendDistilledPipeline`: the source's last 25 frames, 4 latent frames re-encoded at half size, pinned with
  `VideoConditionByLatentIndex` at the start of a 4 + N latent-frame window, and the audio tokens under them likewise;
  DistilledPipeline's two stages over the window; the window's new frames appended to the source's own latent) and
  `a2v --distilled` (`A2VidDistilledPipeline`: DistilledPipeline's stage 1 with the source audio frozen, and stage 2
  **ancestral** with seed + 20000, where the dev a2v's stage 2 is deterministic: `Latent.guided` tells them apart).
  On Distilled `encode-guided` encodes the prompt only; its cache key holds the variant (`by_variant`), since Dev and
  Distilled share Gemma and the connector. `distill` passes a distilled model through.
- **A distilled extend re-reads the source MP4** (`Latent.clip`, set by encode-video) for its half-size tail: the
  causal VAE's last latent frames depend on the whole clip, so the half-size encode covers all of it. Its refusals
  (64 px grid, 25 frames) come before the transformer loads, after encode-video (the check sees no media facts).
- **Measured** (int8, 448x256x49, the dev cases' sources and prompts; extend + 2 s): on mlx 0.32.2 all three are
  bit-exact against the fork (every frame PSNR inf, WAV equal). On 0.32.4 (`DISTILLED_BARS`): retake 35.45 / 33.51 dB,
  log-mel 0.407; extend 28.34 / 17.11, 0.898; a2v 22.48 / 20.89, 0.000. Times (0.32.4, one runner, retake cold):
  retake 30.7 s (the fork's CLI 32.2), extend 34.2 s (34.2: the node 31 s, both stages and the half-size encode), a2v
  32.3 s (26.3). MLX peaks per node with the int8 transformer resident (19.2 GiB): retake 21.5 GiB, extend 25.7,
  sample-guided 23.6, sample2 23.8, decode 29.9-32.4; the fork's recorded peaks 8.2 / 9.9 / 7.7 GiB are its decode's
  alone (its tiled decode resets MLX's peak counter). The formulas stay upper bounds; extend's gets the half-size encode's term.
