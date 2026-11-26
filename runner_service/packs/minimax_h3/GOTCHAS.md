# minimax_h3 pack: gotchas

The pack runs the pinned h3 binary (`vendor/h3c`, h3.c 4cafd17, `make h3`) as a child process of the native runner
(`core/child.py`). Measurements: `docs/reference-notes/minimax-h3.md` (2026-11-19, M5 Pro 64 GB, macOS 27.2).

- **cwd is the binary's folder.** h3 opens `h3_shaders.metal` by a relative path and compiles it at run time, so the
  child runs in `vendor/h3c` and every path it gets (model folder, inputs, output) is absolute.
- **Never the old studio's binary.** Only the vendored copy runs from the pack; goldens may have been recorded with
  the old one (`tests/golden/record_h3.py --bin`), which gave the same T2VA bytes.
- **Determinism.** T2VA repeats bit for bit (same binary, flags, OS, GPU family): the golden test compares the decoded
  RGB24 and PCM hashes. FL2VA and Ref2VA do not always: two runs of one argv can differ by 37-40 dB PSNR (frames and
  sound), with the old binary too; not fixed by `H3_DISABLE_FUSED_PATCH_PACK=1`, `H3_CPU_SAMPLER=1` or
  `H3_QWEN_PREFETCH=0`. Their goldens pass at 30 dB. A macOS update can change bits (shaders are compiled by the OS):
  re-record.
- **Memory.** Child peak 18.5 GB at 512² × 22, 20.6-20.7 at 864 × 480 × 124, 24.2 at 1344 × 768 × 124, 30.3 at
  1344 × 768 × 243 (the denoise is the only phase that grows; steps do not change it). ~4.4 GB of DiT weights are
  mapped from the shards (not in phys_footprint, still in RAM); `H3_ZERO_COPY_WEIGHTS=0` copies them (+4.4 GB).
  `peak_gb: 24 + 0.05 * mp * frames` stays ≥ 5.6 GB above every measured peak; 42.7 at the largest canvas × 362
  frames (that shape was not run). The runner's measure counts the child: `used_gb` shows it during a run.
- **Silence.** The video VAE decode prints no progress: 104 s at 864 × 480 × 124, ~560 s at 1344 × 768 × 243. The
  child's idle limit (`native.py IDLE_S`) is a flat 30 min; the stage's `stall_s` never fires (cancel points every
  0.5 s).
- **Progress lines** are `\r`-updated `<phase> <n>/<total>`; on macOS 27 an MPSGraph log line (`Failed to import MPS
  module`) can be glued after one, so the parser matches the start of the line and ignores the rest.
- **Exit codes.** 0 ok; 1 with a last `h3: <error>` line; 2 bad arguments. `h3: warning: --reuse with only N
  denoising steps ...` is printed for reuse > 1 at ≤ 7 steps: a warning, not an error (Draft uses reuse 1).
- **Speed params (API 1.37).** `speed` is gone: layers / reuse / core reuse / token reduction / render go to h3 as
  they are. h3 refuses core reuse with reuse (`--core-reuse` is passed only at reuse 1) and any internal size that
  is not exactly the canvas's shape on the 32 px grid, so Render picks one (`render_size`): 864 × 480 has only 576 ×
  320 and 288 × 160 below it, so 75 % and 62.5 % both give 576 × 320; 832 × 480 has none (refused, "use Full").
  Measured 2026-11-22 (golden `minimax-h3-t2v-render75`, 512² × 22, 4 steps, h3 at 384²): 35 s, child peak 18.4 GB
  (18.5 at Full: the fixed ~18 GB dominates small canvases); it repeats bit for bit. The phase texts carry `renders
  <w> × <h> → <W> × <H>` (the Create stage tag).
- **Replace a video's sound (API 1.37).** `cut` writes the picture alone, then muxes the soundtrack's part in as
  PCM (`-map 0:v:0 -map 1:a:0 -shortest`): the shorter of the two sets the reference's length; then `--ref-video`
  (or an SGLang `video` condition with sound) as for a kept sound. Unmeasured on the GPU (no golden yet).
- **Cancel** kills the process group (h3 spawns ffmpeg for the mux): SIGTERM, SIGKILL after 3 s. A runner killed hard
  (stall exit, jetsam) leaves the child running; the next start of the env kills it from
  `RUNNER_CACHE/child-native.pid` (only when that pid still runs the same binary).
- **M5 only validated.** `--use-int8-row-fc2` and the int8 transformer need M5 (Metal 4 TensorOps); another Apple
  GPU takes the bf16 path (~36 GiB DiT) and is untested; `devices: [mps]` cannot say "M5".
- **The text encoder is shared.** `MiniMax-H3-Image/text_encoder` links into this variant's `Ref2VA/text_encoder`:
  deleting this variant's files breaks minimax-h3-image.
- **GPU watchdog.** Long command buffers can be killed by macOS while the display is busy; the old hint was
  `AGX_RELAX_CDM_CTXSTORE_TIMEOUT=1`. Not seen here; never set silently.
- **Video and audio references (API 1.22).** Each is cut to its in..out in the per-run folder (`native.cut`, `-ss`
  after `-i`, frame exact): a video as lossless H.264 in .mov (its sound as PCM only for `--ref-video`), an audio (or a
  video's sound) as a PCM WAV. h3 reads a video reference at its own size (at most its adapted ~1 MP canvas) for up
  to the clip's frames, and a video's sound only with >= 56 output frames; all reference sound <= 15 s
  (`generate_error` refuses these before the child starts). A video reference joins the denoise sequence, so
  peak_gb counts 1.04 MP per video (capped at 360 frames in all); only 512 x 512 x 22 was measured (18.65 GB, the same
  as without it). Ref2VA with a silent video repeats less than an image: two runs of one argv at 2 steps differ by
  29.5 dB (tests/test_h3_golden.py BARS).
- **Audio numbering spans kinds.** h3 gives `<Audio n>` from one counter to every reference with sound, in argv order,
  and a `--ref-video` (sound kept) counts; `<Picture n>` and `<Video n>` count per kind. So `native.generate` passes
  `--ref-audio` before the videos: audio row n stays `<Audio n>` as the mentions say, a kept video soundtrack comes
  after (h3_multimodal.c, the prefix loop; h3.c sets `has_audio`).
- **A sound video needs a long part.** A video reference with sound needs its own part to read >= 56 frames too (about
  2.34 s), not only the clip: h3 cuts the frames it decodes down to 5 + 17k and reads the sound only that long, so a 2.0
  s part keeps 39 frames (1.6 s of sound) and h3 fails "video soundtrack 1 requires at least 2 seconds; request at least
  56 output frames" even at 56 output frames (dev run 0de840b4, 2026-11-19). `generate_error` refuses it first ("needs a
  part of at least 56 frames at 24 fps (video <i> reads <n>)").

## The base-fp8 variant (SGLang on CUDA, `sgl.py`)

- **Its own venv.** sglang 0.5.23 pins torch 2.14.1 and has no Python 3.14 wheel: Dockerfile.cuda's `sglang` stage
  builds `/opt/sglang` on a uv Python 3.13 (`/opt/python`) from the lock `runner_service/sglang-requirements.txt`
  (`uv pip sync`; its header has the regenerate command), only in the `native` target. The child gets the cut env
  (`SGLANG_*` passes).
- **The folder is the official repo's skeleton** (every json/txt/py/yaml, both partitions' VAEs, no other
  safetensors) plus the Comfy-Org files; `--model-variant fl2va|ref2va` picks the partition, the DiT and text encoder
  come in as `--component-weights-paths.*` (formats detected from the files: Comfy static FP8 with the curve adaLN,
  ConvRot int8). Unverified on the VM: that SGLang loads a partition whose transformer and text encoder shards are
  missing (their `model.safetensors.index.json` are there) once the overrides are given.
- **Ready** is uvicorn's start line (SGLang starts the HTTP server after the workers loaded); `--warmup-mode off`.
- **SGLang's shape rules** (sgl.target): length from `duration_seconds` (4-15 s → 107-345 frames; we send frames / 24,
  so the clip is exactly the frames asked); canvas from short edge + aspect (adapt_shape_v1, sgl.shape), aspect one
  of six for t2va / ref2va, any for fl2va. Anything it would resize is refused, never silently changed.
- **No abort in SGLang** (`DELETE /v1/videos/{id}` only forgets the job): cancel kills the server's process group.
- **Quality and shifts (API 1.37).** SGLang's `quality` (exact / lossless / high) is not h3's reuse; `high` is
  audited for 4×H200 only (refused on our one-card FP8 deployment), so `generate.quality` offers lossless (sent as
  nothing: SGLang's default) and exact; `flow_shift` / `audio_flow_shift` are sent only when not 12 / 3. h3's speed
  params are hidden on Base fp8 and ignored here. Unmeasured on the VM: exact vs lossless s/step and VRAM, and that
  a shift of 8 takes.
- **Memory.** The native measure counts the child's pid only; SGLang's scheduler worker is a grandchild, so `used_gb`
  under-reads during a run. VRAM is not budgeted (the native budget is host RAM): keep the torch runner empty.
- **Unmeasured:** peak (VRAM and host RAM), load time, s/step, and whether the int8 text encoder runs with layerwise
  offload on SM120; the JIT ConvRot kernels may need nvcc: the lock carries the
  `nvidia-cuda-nvcc` 13.5 wheel (via humming-kernels), but it is not on PATH.

## Turbo (API 1.41, plan 20 24.11): lightx2v's 8-step LoRAs

- **Two files, two add-ons, one folder.** `turbo` (FL2V v1.2, Turbo LoRA) for FL2VA runs, `turbo_ref` (ref2v v1.1,
  Turbo Ref LoRA) for Ref2VA runs (any reference: `refs`, `videos` or `audios` holds a row); generate.lora's
  `addons` / `addons_with` pick it, the same rule studio's run check uses, so the runner never picks by name
  (`lora_file`). Both in `MiniMax-H3-Turbo/`: core.models gives two add-ons sharing a `dir` their own files, marker
  (its `variant`) and delete.
- **The two files differ (C0, headers read 2026-11-22).** FL2V: 1089 tensors, 363 modules (50 blocks × attention
  q/k/v/out, MLP fc1/fc2, AdaLN; 2 token-refiner blocks; norm_out), each with its own `.alpha` (128; AdaLN and
  norm_out rank 16 alpha 16), metadata alpha 128. ref2v: 624 tensors, 312 modules (no AdaLN, no norm_out), **no
  `.alpha` tensors**, rank 128 and metadata alpha 8. h3_lora.c's alpha: the module's `.alpha`, else the file's
  metadata `alpha`, else the rank. FL2V has `.alpha` everywhere (its metadata 128 would scale its rank-16 AdaLN 8×),
  so it never reads the metadata; ref2v takes 8 / 128 = 1/16. At scale 1 (alpha = rank, the first build) a References
  render is noise (C7, `docs/mock/checks/h3-turbo/grid_ref2va.jpg`, sharpness 8). Same key layout and shapes as FL2V
  for the modules both have: one key map.
- **h3 (Mac)** merges at load (vendor/h3c `h3_lora.c`, helmstudio's patch: `patches/h3-lora.diff`) every BF16 2-D
  transformer tensor it loads, so only the run's partition; CPU sgemm per weight, stochastic rounding seeded by the
  tensor name (repeats bit for bit; h3's int8 quantizer keeps the small delta). Measured 2026-11-22 (M5 Pro, 512 × 512
  × 22, 8 steps, seed 42, FL2V): `h3 lora: merged 363 modules in 19.3 s`, 67 s in all, peak footprint 24.9 GB
  (`/usr/bin/time -l`), a clean picture. Fewer layers load fewer blocks: `merged 300 of 363` at exit, not an error.
  A bad file: an `h3 lora: …` line, then h3 fails `cannot merge LoRA into <name>` (child.run shows the `h3:` line).
- **Every weight copied** (`H3_ZERO_COPY_WEIGHTS=0`, owner 24.12 point 4): merging into mapped bf16 shards dirties
  copy-on-write pages that stay resident (the spike swapped). The peak formula adds 4.5 GB on Turbo (20.7 → 25.0 GB at
  864 × 480 × 124 copied; the merge adds nothing on top). `H3_LORA` is always sent (empty with `none`): a stray one in
  the runner's environment never merges. `H3_LORA_STRENGTH` is sent as 1 (owner: fixed).
- **SGLang (NVIDIA)** gets an SGLang-ready copy as `--lora-path` on the run's one server (no `/set_lora`); on the FP8
  weights it applies it dynamically ("quantized weights cannot be merged in place"). Turbo sends no flow shifts.
- **SGLang cannot take the files as they are (sglang 0.5.23, read 2026-11-22).** Its fused qkv gathers to_q/k/v's
  `.alpha` into one 3-element tensor, then `_apply_lora_to_layers` calls `.item()` on it: FL2V fails "a Tensor with 3
  elements cannot be converted to Scalar". Its alpha otherwise: the layer's `.alpha` (cast to int), else
  `lora_alpha` from an `adapter_config.json` beside the file or the safetensors metadata (`alpha`, `lora_alpha`,
  `network_alpha`), else the rank; scale = lora_scale (1) × alpha / rank. So `sgl.ready_lora` writes a copy with
  every lora_B × h3_lora.c's alpha / rank, no `.alpha` tensors and no metadata alpha: SGLang's scale is then 1, the
  same delta as the Mac. At `<add-on dir>/.cache/helmstudio/sglang/<stem>-<key12>.safetensors` (key: the source's
  size and mtime; tmp + rename, bf16 kept, an older copy of the same file removed), reused after; ~2 s per file on
  the VM. The add-on's delete removes its copy with its file (core.models `SGLANG_COPIES`). Measured 2026-11-22 by
  hand in runner-native: FL2V copy loads, "applied to 259 layers", server ready.
- **No AdaLN modules on NVIDIA.** The pruned FP8 transformer's AdaLN takes an 8-wide time table
  (`adaln_proj.linear.weight` [96768, 8], `adaln_t_table` [1025, 8]); the LoRA's AdaLN `lora_A` is 2688 wide.
  SGLang's `prepare_lora_adapter` projects it only for a `MiniMaxH3PrunedTransformer3DModel` config whose checkpoint
  carries `adaln_basis` / `adaln_mean`; ours has neither (the transformer config is the unpruned one), so the run
  failed "mat1 and mat2 shapes cannot be multiplied (1x8 and 2688x16)". The copy leaves out `adaln_proj.*` and
  `norm_out.*` (51 FL2V modules; ref2v has none); `COPY_V` in the copy's key replaces older copies. So NVIDIA Turbo
  is the LoRA minus its AdaLN part (the Mac merges all of it). The proper fix needs the unpruned time embedder (not on
  the VM) to fit the 8-wide projection.
- **NVIDIA checks** (2026-11-22, helm-gpu RTX PRO 6000 Blackwell 96 GB, through the pack with `helm run --host gcp`,
  Base fp8, 864 × 480 × 124, seed 42, the fox prompt; `docs/mock/checks/h3-turbo/nvidia/`, `grid.jpg` rows in this
  order; generate s is the runner's `generate: ran in`, which includes starting the SGLang server; GPU peak from
  `nvidia-smi` at 1 s; sharpness: Laplacian variance, mean of 8 gray frames every 15th):

  | Run | generate s | GPU peak | sharpness | look |
  |---|---|---|---|---|
  | Text, Turbo LoRA, 8 steps (first run: also writes the copy) | 116.9 | 33.4 GB | 49.4 | clean |
  | Text, no LoRA, 8 steps | 76.7 | 32.1 GB | 71.4 | clean |
  | Text, Base default (20 steps) | 97.0 | 32.1 GB | 51.9 | clean |
  | References, one image, Turbo Ref LoRA, 8 steps | 206.4 | 36.6 GB | 53.2 | clean, the fox (not noise) |

  On this GPU the server start dominates a short run, so Turbo saves little time on Text, and the FP8 base is already
  clean at 8 steps without it. Turbo adds ~1.3 GB (dynamic LoRA). Mac clips for scale: base50 65.0, Fast 38.9.
- **2026-11-25: Turbo failed at worker start on the first host-native run** (no CUDA image), so dea67313 limited
  the add-ons to Base (`variants: [base]`). That failure was seen before the runner kept SGLang's stderr (20534a75,
  e6e6dac2) and before the host gaps were fixed the same afternoon: no gcc / build-essential, no python3.12-dev, a
  long hostname, and no cu13 `lib64` / `libcudart.so` links (all of which the CUDA image had). The likely cause is one
  of those gaps (SGLang's JIT kernels fail at worker start), not the LoRA, which ran clean on 11-22 in the image.
  alpha.6 row 1 lifts the limit again; row 3 (sgl.preflight) makes the links and refuses a long hostname or a missing gcc.
  Unproven until the GPU check: if Turbo still fails, the stderr tail shows the cause, and `variants: [base]` goes
  back until it is fixed.
- **Preset order by time** (owner 24.12 point 3: swap Turbo and Fast if Fast is quicker): measured 2026-11-22 through
  the pack at 864 × 480 × 124, seed 42, one after the other (the contract suite building on the same Mac, so both
  slower than the spike's 340 s): Fast 344.5 s (peak 19.1 GB), Turbo 420.0 s (peak 24.6 GB, the runner's measure;
  the formula plans 31.1). So the order is Draft · Fast · Turbo · Default · Reference.
- **Golden** `t2v-turbo` (512 × 512 × 22, 8 steps, seed 42): two pack runs bit-identical (62 s, peak 23.2 GB).
- **C7, Ref2VA at Turbo** (2026-11-22, M5 Pro, the h3 binary as the pack runs it, 512 × 512 × 62, one image
  reference, seed 42, copied weights; `docs/mock/checks/h3-turbo/ref2va-*`, `grid_ref2va.jpg`): Turbo Ref LoRA,
  8 steps: 128 s in all (merge 312 modules 15.0 s, denoise 56 s), peak footprint 25.7 GB, sharpness 74; Base 50
  steps: 490 s (denoise 426 s), 26.9 GB, sharpness 78. It holds: Turbo stays in References' presets. Ref2VA is not
  bit-repeatable in h3 (two Turbo runs 34.8 dB apart), so no Ref2VA Turbo golden.
- **Fast vs Turbo rechecked** (2026-11-22, the Mac otherwise idle, 864 × 480 × 124, seed 42, the binary): Fast 317 s
  (denoise 180 s, mapped, peak 20.5 GB, sharpness 39), Turbo 413 s (merge 19.3 s, denoise 259 s, copied, peak
  27.0 GB, sharpness 59; `grid_fast_turbo.jpg`). Fast stays before Turbo. The golden still matches exactly after
  the metadata-alpha change (59 s, peak 23.0 GB).
