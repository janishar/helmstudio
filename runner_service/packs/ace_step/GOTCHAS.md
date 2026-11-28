# ACE-Step 1.5: gotchas

- **No repo code at run time.** The runner never imports ACE-Step's own package (its `transformers < 4.58` cap, its
  habit of copying its modeling `.py` files into the checkpoint folder). The DiT runs on diffusers 0.43's
  `AceStepPipeline` classes, built on meta from the original checkpoint's `config.json` exactly as
  `scripts/convert_ace_step_to_diffusers.py` builds them, its keys renamed on the fly (`rename`); nothing is written
  into `~/GenAI/models`. The planner is a port of `llm_inference.py`'s two phases on transformers 5.23.
- **One DiT checkpoint, four modules.** `model.safetensors` (2B) and XL's four shards each hold the decoder
  (`decoder.*` → the transformer), the condition encoder (`encoder.*` + `null_condition_emb`), the audio tokenizer
  and detokenizer (cover and the plan's codes). The decoder's `proj_in.1` / `proj_out.1` (a Sequential) are
  diffusers' `proj_in_conv` / `proj_out_conv`; `q_proj … o_proj`, `q_norm`, `k_norm` are `to_q … to_out.0`,
  `norm_q`, `norm_k`. `silence_latent.pt` ([1, 64, 15000]) goes in transposed as the condition encoder's buffer
  (`build`): zeros there make drone-like audio.
- **Two DiT components, one each variant.** A component's dtype is its own, so the 2B (`transformer`, stored bf16,
  run float32 as the repo runs it on MPS) and XL (`transformer_xl`, stored float32, run bf16: 24 GB peak against
  35.6 GB, twice as fast; owner's brief) are two components scoped by `variants`. Every DiT stage `uses` both; the
  other variant's has no files in this variant's folder, so it costs nothing (it shows as 0 GB in the memory plan).
  The model node's precision is a fixed `bf16` (no choice): the components' own precisions hold.
- **The float32 text encoder under the bf16 XL**: its hidden states are cast to the DiT's dtype (`Pipe.encode_prompt`),
  or the condition encoder's Linear refuses them. The VAE is float16 (as the repo runs it), so latents are cast to it
  before decode.
- **The LM's head is tied** (`tie_word_embeddings`, no `lm_head` in the file): `TiedHead` reads the embedding's weight,
  or the loader finds `lm_head.weight` still on meta.
- **The planner is seeded here, not upstream.** The repo's same seed gave another key and caption (results.md);
  every token is drawn on the CPU from a generator seeded with the run's seed (top-p on the plain softmax, then
  temperature, as `_apply_top_p_filter` / `_sample_tokens` do), so a seed repeats bit for bit (the goldens check it).
- **The planner's phase 1 is unconstrained.** The repo's FSM (`constrained_logits_processor.py`, 2300 lines) forces
  the YAML keys and injects set values; here a set BPM is written into the thought before it starts (the first key)
  and the other set values replace what it wrote; audio-code tokens are blocked; the thought is capped at 600 tokens.
  Values it writes that are not valid (BPM 30-300, length 10-600 s, a known key, time 2/3/4/6) are dropped.
  ponytail: port the FSM if the free thought proves unreliable.
- **Phase 2 writes exactly duration x 5 codes** (5 Hz), CFG 2.0 against the "NO USER INPUT" prompt over the 64000
  code tokens only (contiguous ids from 151669), as the repo's constrained decoder forces EOS at the target.
- **Text to music with codes is a cover.** The pipeline (and the repo's handler) switch text2music with audio codes
  to the cover task and its instruction. Repaint, Cover and Extend have no plan (the repo's DIRECT_CONDITIONING_TASKS).
- **Extend is a repaint of silence**: the source padded with zeros at the end or start, the padding repainted, the
  source spliced back. The song may last 600 s in all.
- **Repaint keeps the source's own samples** outside the range (10 ms ramps, `repaint_waveform_splice.py`); the
  repo's per-step latent injection ("balanced" mode) is not in diffusers and is left out.
- **Levels**: the pipeline's two-stage levelling (a peak over 1 divided out, then -1 dBFS peak) on the whole output,
  spliced parts included.
- **No DCW.** The samples' DCW (pytorch_wavelets, needs setuptools 80's pkg_resources) is repo code, not in
  diffusers' path; it is left out.
- **Sections** come from one extra DiT pass at t = 1/8 with the generation's conditions, its cross-attention of the
  alignment heads (2B: lyric_alignment_common.py's defaults; XL: its config.json's) against the lyric tokens, the
  bidirectional consensus and DTW (`_dtw.py`'s tie rule, vectorised by anti-diagonal). A failure leaves the take
  without markers, never fails it; past about 1500 lyric tokens x 7500 frames `torch.quantile` refuses (no markers).
- **The LM tokenizer loads in about 15 s** (65 k added audio-code tokens); it is cached per process.
- **No `alone` on the plan.** Everything fits at once (LM 6.9 GB + 2B DiT 8.9 GB + text encoder 2.2 GB + VAE; XL's
  DiT 7.5 GB), and the LM stays resident: unloading the DiT before each plan would reload it every run (XL 45 s from
  the T9 drive).
- **Measured** (M5 Pro 64 GB, torch 2.15 MPS, process footprint; tests/test_ace_step_golden.py and 30 s / 2 min
  songs, seed 42): 2B 10 s 24.5 s cold (the LM tokenizer's 15 s included) / 9.4 s warm, 21.3 GB; 30 s 36.8 s cold /
  16.1 s warm, 24.4 GB; 2 min 50.6 s warm (the plan 34 s of it), 25.7 GB. XL 30 s 42.2 s cold (the DiT from the T9
  drive) / 16.0 s warm, 24.4 GB. Repaint, Cover and Extend of a 6 s song 1.4-2.1 s warm. Resident: 18.4 GB (2B),
  18.7 GB (XL). A seed repeats bit for bit (every golden twice in one process).
