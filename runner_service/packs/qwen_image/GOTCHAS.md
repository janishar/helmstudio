# qwen_image pack: gotchas

## CUDA vs MPS (M6, measured 2026-11-16 on the gcp host: RTX PRO 6000 Blackwell, driver 595.91, torch 2.15, CUDA 13.2)

- **Same picture, not the same pixels.** Against the MPS goldens (`tests/golden/qwen-image*/<case>.png`) CUDA gives
  turbo-768-s42 42.44 dB, turbo-1024-s7 37.81, turbo-1344x768-s123 41.40, base-768-s42 49.13, edit-e1 48.29,
  edit-e2 31.40, edit-e3 43.31 (all int8; looked at side by side: same composition, edges and fine texture move).
  The MPS bar stays 30 dB; CUDA has its own goldens (`<case>.cuda.png`, bar 50 dB, `tests/test_qwen_golden.py`).
- **CUDA is deterministic here**: the same case is bit-identical in two processes and through the runner over NATS
  (the take of a Kong run equals the in-process bench byte for byte). Another GPU, driver or torch may change
  kernels: record the `.cuda.png` files again there (`tests/test_qwen_golden.py <cases> --out DIR`).
- **Where it diverges** (turbo-1024-s7, stage by stage): the initial noise comes from a CPU generator
  (`torch.Generator("cpu")`, `randn_tensor` on the CPU, then moved), yet 31 of 262144 bf16 elements differ by one
  ulp between the Mac (arm64) and the VM (x86-64): the CPU's own vectorised randn, rel 2.9e-5. The first real
  difference is the text encoder's output (prompt embeddings rel L2 2.8e-2, max 56 on the large outlier channels:
  bf16 matmul and attention kernels, int8 dequantised on both); the latent after step 1 is then rel 1.0e-3 apart and
  the final latent 1.6e-2. The VAE adds little: the MPS final latent decoded on CUDA is 54.4 dB from the MPS golden.
- **int8**: CUDA has `_weight_int8pack_mm` (torch 2.15 build), used as on MPS only for <= 8 rows (the ask stage's
  token-at-a-time decoding); image runs dequantise and use a bf16 matmul on both devices.
- **The Hub's Turbo repo carries its own VAE** (a bf16 re-save of Base's fp32 VAE, different SHA-256): a download
  to a fresh host keeps it as Turbo's own (`separate: [vae]`, one more 0.63 GB residency entry). Cast to bf16 the two
  are equal tensor for tensor, so it changes no pixel; a hand-made folder that links Turbo's `vae/` to Base's (the
  Mac's) shares one entry.
- **Memory on CUDA** (`measure_gb` = reserved + context): resident turbo int8 = text encoder 9.28 + transformer 6.76
  + VAE 0.63 GB; whole-device peaks of the runs (nvidia-smi) 22.0 (768²), 25.5 (1024²), 29.3 (base 768² with both
  transformers resident), bf16 turbo 62.9 with the int8 entries still resident. A cold encode reads 15.8 GB reserved
  with 9.9 GB of weights: the allocator keeps the load's freed blocks cached until a later stage reuses them, so the
  stage peaks above weights are an over-count on CUDA, not a plan error.

## Enhance prompt (API 1.36 add-ons, measured 2026-11-22 on the Mac: torch 2.15, transformers 5.23, MPS)

- **Two add-ons, one node.** PE-T2I runs without references, PE-I2I with them; the node publishes both as `addons`,
  so studio runs it only on a host holding both (no add-on-by-param rule yet). The plan and the stage's room count
  both (20.3 GB unloaded after), though a run loads one.
- **Which path is the reference.** The pack's text equals QwenLM/Qwen-Image-2.1 prompt_rewrite's transformers path
  (AutoProcessor + AutoModelForImageTextToText, bf16) byte for byte at the same seed (golden
  enhance-turbo-1024-s42). The old studio's T2I loads AutoModelForCausalLM: the same 2452 prompt ids, another text
  (bf16 numerics of the text-only class diverge at a sampled token). Neither is wrong.
- **think = enable_thinking**, as the old studio: off, the template's empty `<think>` block (41.5 s T2I here, 445
  tokens); on, minutes. prompt_rewrite's README says both models were trained with thinking and its production runs
  use a presence penalty of 1.5 for T2I: neither is ported (the model cards and the old studio use neither).
- **Licence**: the Qwen Research License, non-commercial only (`licences/qwen-research.md`), accepted per download.
