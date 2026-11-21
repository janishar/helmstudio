# ggml patches

Local fixes carried on top of the upstream `ggml` submodule **without forking or
vendoring it**. The submodule stays pinned to a pristine upstream SHA; these
patches are (re)applied to its working tree at CMake **configure** time
(see the `find_package(Git)` / `git apply` block in the top-level `CMakeLists.txt`).

Each `*.patch` is a `git diff` taken from the **ggml repo root** (paths look like
`a/src/ggml-cuda/...`). Application is idempotent: configure first runs
`git apply --reverse --check`; if that succeeds the tree is already patched and
the step is skipped, so re-running cmake never double-applies. Patches are applied
in sorted filename order.

## Current patches (ggml v0.27.0)

The CUDA patches were ported blind from the 0.15.1 pin (no CUDA toolkit on the
porting machine) and are marked **UNVERIFIED (no CUDA build)**. On a CUDA host,
build with `-DGGML_CUDA=ON`, run
`test-backend-ops -o MUL_MAT,MUL_MAT_ID,NORM,SOFT_MAX,PAD,CONV_TRANSPOSE_1D -b CUDA0`
and the zonos3 CUDA captures before relying on them.

- `cuda-f32-matmul-precision.patch` — **UNVERIFIED (no CUDA build).** Honor
  `GGML_PREC_F32` (`op_params[0]`, set by `ggml_prec_set_acc`) for ordinary and
  routed expert GEMMs instead of entering custom TF32 arithmetic: the MMF path is
  skipped in `ggml_cuda_mul_mat`, `ggml_cuda_mul_mat_id` and
  `ggml_cuda_mul_mat_id_needs_sync` (which must agree, or the fallback's
  `GGML_ASSERT(needs_sync)` fires). Expert slices inheriting the parent's
  precision is upstream now. On the old pin, the F32 backbone passed all 182
  Python/native captures on H100 with this correction.
- `cuda-bf16-matmul-precision.patch` — **UNVERIFIED (no CUDA build).** Retain
  genuine BF16 operands but permit requested F32 GEMM output: in
  `ggml_cuda_mul_mat_cublas` an F32 request no longer promotes BF16 compute to
  F32, and `ggml_cuda_mul_mat_cublas_impl` writes F32 C directly for BF16 compute
  (single and batched GEMMs). `GGML_CUDA_CUBLAS_COMPUTE_TYPE` still overrides.
- `cuda-matmul-id-f32-input.patch` — **UNVERIFIED (no CUDA build).** Preserve F32
  sorted expert activations when requested, instead of rounding them to the
  expert-weight dtype. Keeps sorted fallback arithmetic consistent with the
  isolated vector path. Unchanged from the old pin apart from offsets.
- `cuda-layernorm-welford-affine.patch` — **UNVERIFIED (no CUDA build).** Replace
  cancellation-prone `E[x²]-E[x]²` normalization with a stable Welford reduction
  inside v0.27's grid-stride `norm_f32` (one `__syncthreads()` added because the
  shared scratch is reused across passes) and fuse the per-channel affine via a
  NORM→MUL→ADD fusion (`ggml_cuda_op_norm_fused_add`). The fusion additionally
  requires a contiguous, non-aliasing ADD output; otherwise the unfused path runs.
  RMSNorm is unchanged. Addresses the SSL frontend's high-mean, low-variance rows.
- `cuda-accurate-math.patch` — **UNVERIFIED (no CUDA build).** Remove global
  fast-math substitution for scalar exponentiation, sine and division before BF16
  rounding. On the old pin, first-layer teacher FFN1 captures matched the
  CUDA-autocast oracle exactly. Unchanged apart from offsets.
- `cuda-softmax-source-order.patch` — **UNVERIFIED (no CUDA build).** Use Torch's
  lane/reduction ordering and direct division for plain F32 softmax;
  scaled/masked/sink paths keep their separate implementations. Unchanged apart
  from offsets.
- `metal-conv1d-implicit.patch` — new op `GGML_OP_CONV_1D_IMPLICIT`
  (`ggml_conv_1d_implicit(ctx, w, x, K, stride, dilation, pad_left)`, BF16 `w`
  `[K*IC, OC]`, F32 `x` `[IC, T]`), with a CPU reference (`ops.cpp`), a Metal
  kernel `kernel_conv1d_implicit_bf16` (`kernels/conv.metal`) and
  `test-backend-ops` cases for every codec-encoder conv shape. The Metal kernel is
  the non-tensor `kernel_mul_mm` (BF16) with only the B-tile loader replaced by a
  strided/dilated/padded gather from `x`, so it accumulates in the same order as
  `pad + transpose + im2col + mul_mat` and is bit-identical to it (reference codes
  for all three default voices, F32 and Q8_0, are byte-identical) while skipping
  the im2col buffer: 3.4–6.1x faster per conv on M3. zonos3 uses it only on Metal
  (`zac_encoder_load` probe); the CPU reference sums differently from the CPU BF16
  GEMM, so CPU keeps im2col.
- `metal-encoder-throughput.patch` — two bandwidth fixes for the codec encoder's
  narrow, very long activations (`[C=64..128, T≈883k]`). Contiguous float
  `CPY`/`CONT`/`DUP` (F32/F16/BF16 casts, including the encoder's BF16 rounding
  pairs) use a flat four-elements-per-thread kernel (`kernels/quantize.metal`)
  instead of one threadgroup per row. `IM2COL` uses a destination-ordered
  one-thread-per-element kernel (`kernels/conv.metal`) whenever all indices fit in
  32 bits. Same element values and order, so results are bit-identical; reference
  encoding is about 2x faster on M3. Verified on v0.27 with
  `test-backend-ops -o {CPY,CONT,DUP,IM2COL} -b MTL0` (199/78/7/102 passed).
- `metal-fusion-exact.patch` — unregisters two Metal fusions new since 0.15.1 that
  change floating-point results versus the unfused ops: `RMS_NORM/NORM + SCALE`
  (the GDN q/k L2 norm) and the MoE weighted-reduction chain (`MUL` + expert views
  + `ADD`s). With them, GPU backbone logits drift (argmax 99.79% vs the 0.15.1
  build); without them, every validation capture, KLD base, greedy/sampled code
  sequence and decoder output is bit-identical to the 0.15.1 build.
- `metal-ncb-env.patch` — let the Metal backend read `n_cb` from `GGML_METAL_NCB`
  (upstream hard-codes 1). Since v0.27 `n_cb` is the number of *extra* encoder
  threads (0 = the calling thread encodes everything). `zonos3_model_load` sets a
  default for the GPU backbone (overridable; see `src/zonos3.cpp`). Behavior is
  identical to upstream when the env var is unset.
- `metal-rope-back-template.patch` — upstream added `ROPE_BACK` to the Metal rope
  kernels through a function constant (`FC_rope_is_back`) branch in `rope_yarn`.
  Even when false, that branch changes the forward kernels' sin/cos codegen by
  ~1 ulp, which flips BF16 roundings in the codec encoder's MOSS attention and
  changes 3–5% of reference codes. The patch makes `is_back` a template parameter
  with separate `kernel_rope_*_back_*` instantiations, restoring the old forward
  codegen: reference codes for all three default voices (F32 and Q8_0) are
  byte-identical to the 0.15.1 build. `test-backend-ops -o ROPE,ROPE_BACK -b MTL0`
  passes (470/404).

Dropped at the v0.27 bump (fixed upstream): `conv-transpose-1d-fix` (upstream
CUDA kernel is O(L·K); it sums in a different order, so CUDA decoder output may
differ at rounding level), `cuda-pad-grid-stride` (upstream caps grid y/z and
loops; 32-bit indexing still fits the encoder's shapes), `metal-pad-left`
(upstream Metal `PAD` supports left padding).

## Caveats

- Patches are applied at **configure** time. If you reset the submodule worktree
  (`git submodule update`) without re-running cmake, an incremental build would use
  unpatched sources — re-run `cmake` (configure) after any submodule reset.
- After a submodule **bump**, a patch may no longer apply against the new sources;
  configure then fails with a `FATAL_ERROR`. Refresh the patch: revert the worktree
  (`git -C ggml checkout <file>`), re-apply the change by hand against the new code,
  and regenerate with `git -C ggml diff <file> > patches/<name>.patch`.
