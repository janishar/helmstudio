# minimax_music3 gotchas

Measured 2026-11-27 (M5 Pro 64 GB, torch 2.15 MPS, bf16, diffusers 0.43.0, transformers 5.23;
`tests/test_minimax_music3_golden.py`, phys_footprint per node).

- **diffusers 0.43's module is the model card's code, refactored.** Against PR #14456 at dafe3733 (the samples'
  editable clone): `before_denoise`, `denoise`, `decoders` and the four model classes are the same code (return
  annotations aside); `encoders` renamed its steps (`MiniMaxMusic3TokenizeStep`, `...AutoregressiveStep`) and passes
  the two models instead of the pipeline. Nothing vendored: the golden proves it the same song bit for bit.
- **Same song as one pipeline call.** The golden `song-10s-s42` is the samples' song cut to 10 s, recorded by
  `tests/golden/record_minimax_music3.py` with the card's path (`ModularPipeline.from_pretrained` +
  `load_components(dtype=bf16)`, one call). The pack's three nodes give the same PCM hash, cold and warm. The trick:
  one `torch.Generator(device)` for the run; lm hands its state (`get_state()`) to flow with the frames, so flow's
  noise starts where the language model's sampling stopped. A cached lm output replays the same state.
- **Cancel per frame.** The autoregressive loop is the library's; a forward pre-hook on `language_model.model` (one
  call for the prompt, then one per frame) is the cancel point and progress step. Flow hooks the transformer (two
  passes per step, CFG), vocode the vocoder (one per window).
- **Times** (10 s, cold: loads included): lm 50.0 s (load ~23 s), flow 23.3 s, vocode 8.9 s, 84 s in all; warm lm
  27.1, flow 9.6, vocode 8.5, 45 s. The samples' 30 s song: lm 83 s, flow 60 s, vocoder 4.5 s (about 5x real time).
- **Memory.** Components 21.9 GB declared (language model 16.0, depth decoder 1.2, transformer 4.53 cast from fp32,
  condition encoder 0.05, vocoder 0.1). Peaks per node (warm): lm 25.25, flow 25.32, vocode 25.35 GB; whole process
  27.2 GB (`/usr/bin/time -l`), the card's path 28.2. peak_gb: lm 2.8 + the KV cache (7.4 MB per second of song,
  Auto planned at 300 s: 26.9 GB in all), flow and vocode 3 (one window at a time).
- **Instrumental has no switch**: the style gains "Instrumental, no vocals." and the lyrics are only
  `[instrumental]` (the model's own rule; lyrics may not be empty, so empty lyrics do the same).
- **Length is an upper bound**: the model may stop early (the samples' 30 s cue stopped at 22 s). Auto = 300 s.
- **Download**: the repo also holds SGLang-Omni's layout (qwen_7B/, flowmatching_vae.pth, dav.pth, 28.8 GB) the
  runner never reads; `allow_patterns` lists the diffusers folders only (28.5 GB).
- **CUDA untested**: the card's own device; the goldens are MPS (sampling diverges on another device).
