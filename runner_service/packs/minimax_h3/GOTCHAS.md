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
  denoising steps ...` is printed for `fast` at ≤ 7 steps: a warning, not an error.
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

## The fp8 variant (SGLang on CUDA, `sgl.py`)

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
- **Speed.** SGLang's `quality` (exact / lossless / high) is not h3's reuse; `high` is audited for 4×H200 only. The
  run sends no quality (SGLang's default) and refuses `speed: fast` (check_generate).
- **Memory.** The native measure counts the child's pid only; SGLang's scheduler worker is a grandchild, so `used_gb`
  under-reads during a run. VRAM is not budgeted (the native budget is host RAM): keep the torch runner empty.
- **Unmeasured:** peak (VRAM and host RAM), load time, s/step, and whether the int8 text encoder runs with layerwise
  offload on SM120; the JIT ConvRot kernels may need nvcc: the lock carries the
  `nvidia-cuda-nvcc` 13.5 wheel (via humming-kernels), but it is not on PATH.
