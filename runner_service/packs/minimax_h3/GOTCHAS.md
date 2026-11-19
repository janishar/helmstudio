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
