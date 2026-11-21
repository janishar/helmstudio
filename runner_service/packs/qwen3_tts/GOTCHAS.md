# qwen3_tts gotchas

- **torch.compile stays off.** On torch 2.15 MPS the compiled code predictor never writes end-of-speech ("Good
  morning, everyone." ran to its 257-frame cap). `enable_fast_decoding(compile=False)` gives the reference's codes.
- **embed_tokens.** Each `Qwen3Model` has a 1-row token table the checkpoint lacks (inputs are always embeddings);
  `build()` makes it real on the device or the core loader fails "not in the checkpoint".
- **The codec runs bf16**, cast from its stored fp32 as the port does: the waveform then equals the reference's byte
  for byte. Its `encoder.*` keys (cloning) are never read.
- **Seeding is global** (`torch.manual_seed` before each piece, the port's `_seed`): the torch env runs one run at a
  time. A per-run `torch.Generator(device="mps")` threaded through `sample()` would be a vendored patch.
- **Repeats bit for bit** on the same device and torch (codes and WAV): the goldens are exact.
- **VoiceDesign's folder holds a real copy** of `speech_tokenizer/` (same bytes as CustomVoice's), so the core treats
  it as `separate` and loads it again for voice_design (0.21 GB); a link into CustomVoice's folder would make it one
  entry.
- **Voice fit is per stage, not per workflow.** Both templates' `speak.voice` accepts `[preset, design]` (one node
  schema), so studio's fit lists a designed voice for the voice-over template too; `check_speak` refuses the wrong
  pair before loading (`speak: voice <id> needs model variant voice_design`). Narrowing per template needs a contract
  rule (e.g. `only` on a voice param).
- **Dialogue (API 1.32)**: each line is spoken as `speak` speaks a text (`utter`: pieces of 200 characters, 0.25 s
  between pieces), reseeded with the run's one seed before every piece (the port's `_seed`), so a line sounds the
  same wherever it sits in the script; presets only (`check_dialogue` refuses voice_design). The golden
  `dialogue-ryan-aiden-s1` equals AI-EXP's own `Synthesizer.speak` per line, joined by `core.media.stitch`, byte for
  byte (tests/golden/record_qwen3_tts.py).
