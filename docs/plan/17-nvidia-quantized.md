# helmstudio-next plan — The NVIDIA quantized path

[← Plan index](../README.md)

Status: M9 (owner, 2026-11-21: "find a full rtx pro 6000 spot cheapest and prepare for them to use community version
in fp8 if not available int8", then "put this as alternate quantised deploy path for nvidia"). The scout, with every
repo, revision, size and tensor layout, is [reference-notes/nvidia-quantized.md](../reference-notes/nvidia-quantized.md).

## 21. Community FP8 / int8 weights as an alternate path for NVIDIA hosts

### 21.1 What it is

The official weights stay the default everywhere (bf16, or int8 made at load). An NVIDIA host can instead load
**community pre-quantized weights**: FP8 where the community publishes it, else int8. They are smaller to download
and keep on disk (about 145 GB for every pack instead of about 466 GB), and FP8 runs natively on Blackwell. They are
**extra variants** of the existing packs, marked `devices: [cuda]`, so a Mac never offers them and nothing about the
official path changes. The deploy guide is [deploy/gpu-host/README.md](../../deploy/gpu-host/README.md), "The
quantized path".

### 21.2 Which weights

| Pack | Part | Quantized build (community unless said) |
|---|---|---|
| Qwen Image 2.1 base / Turbo | transformer, text encoder | unsloth FP8 (per-row scales; text encoder a plain FP8 cast); VAE bf16 |
| MiniMax H3 Image | DiT | unsloth FP8 (curve-form adaLN: an 8-wide time table instead of the time MLP) |
| | text encoder (Qwen3-VL 32B) | no FP8 exists: Comfy-Org int8 ConvRot (block-Hadamard rotated, per-row int8) |
| | prompt enhancer | official NVFP4, as today |
| MiniMax H3 video | FL2VA, Ref2VA DiTs | community FP8 (phase 2) |
| LTX-2.5 | distilled transformer | unsloth FP8 (phase 3) |
| | dev transformer | no FP8 exists: Lightricks' int8 ConvRot (phase 3) |
| ZONOS3-Mini | all | the official GGUF q8_0 set (already int8), built for CUDA |

Qwen3-TTS stays on its official bf16 weights (1.7B: nothing to gain).

### 21.3 How it runs

- **FP8 Linear**: weights stay FP8 with one scale per output row. Compute dequantizes one layer at a time to bf16
  (works on any CUDA); where `torch._scaled_mm` row-wise works on sm_120 it multiplies in FP8 directly.
- **int8 ConvRot**: per-row int8 weights whose input axis was rotated by a fixed 256-wide Hadamard matrix; the
  input is rotated the same way before the existing `Int8Linear`.
- **Precision `fp8`** joins the contract's precision lists (an API bump); a variant can fix its own precision.
- **A variant can come from more than one repo** (configs from the official repo, weights from the community one).
- Phases 2 and 3 need new CUDA engines: MiniMax H3 video has no CUDA in h3.c (SGLang as a child process), LTX needs a
  torch port of the MLX pack.

### 21.4 Phases

1. Shared: the contract (`fp8`, multi-repo source, variant precision), `Fp8Linear`, ConvRot int8. Qwen Image and
   MiniMax H3 Image quantized variants, ZONOS3's CUDA build. Tested on the spot VM: CUDA goldens and PSNR against
   the official path on the same GPU.
2. MiniMax H3 video on CUDA with community FP8. Code done (2026-11-21): variant `fp8`, SGLang 0.5.23 as a child
   server (`packs/minimax_h3/sgl.py`), Comfy-Org pruned FP8 DiTs + int8 ConvRot text encoder. Status 2026-11-21: not
   yet run on the VM; the `fp8` download stopped on an HF 429 (text encoder and Ref2VA DiT missing) and needs a
   re-queue with `--accept-licence` (docs/handoff.md, "M9 phase 2 notes", fifth pass). MiniMax H3 Image base-fp8 with
   Enhance on ran on gcp.
3. LTX-2.5 on CUDA (torch port) with community FP8 / int8.

### 21.5 Known issues

- The MiniMax H3 licence excludes the EU, the UK, the Republic of Korea and the US (its "Applicable Territory"):
  the owner decided (2026-11-21) to keep the spot VM in the EU and use MiniMax there for evaluation only, and
  accepted both MiniMax licences (docs/handoff.md, M9 phase 2 notes).
- Publisher-measured quality (LPIPS against bf16) is worse for FP8 than for int8 ConvRot in every case; the
  platform shows the variant names plainly so the choice stays visible.
- The Qwen Image weights are under the Qwen Research licence upstream, not Apache 2.0 as our pack says (found by
  the scout; to fix separately).
