# Qwen3-TTS: notes for the Assistant

## Card
Qwen3-TTS 1.7B speaks text in one of 9 preset speakers with a style in words (Custom voice), or in a voice made up
from a description (Voice design). Three workflows: Voice-over, Voice design and Dialogue (presets only). 10
languages, 24 kHz, about 6 GB, roughly 0.6 s of work per second of speech on a Mac. Pick it for a preset speaker
told how to say a line ("calm", "very angry"), or to invent a new voice from words; pick ZONOS3 instead for a cloned
voice, many languages, fine delivery sliders or speed.

## Good at
- Nine preset speakers, each with a fixed character: Vivian (bright, slightly edgy young woman, Chinese), Serena
  (warm, gentle young woman, Chinese), Uncle Fu (seasoned man, low and mellow, Chinese), Dylan (youthful Beijing man),
  Eric (lively Chengdu man, slightly husky), Ryan (dynamic man, strong rhythmic drive, English), Aiden (sunny
  American man, clear midrange, English), Ono Anna (playful Japanese woman), Sohee (warm Korean woman, rich
  emotion). [card] [pack]
- Style in plain words on a preset: tone, emotion and pace follow an instruction such as "calm", "whispering" or
  "very happy"; the maker's own example is "speak in a very angry tone". [card] [pack]
- Inventing a voice from a description (Voice design): timbre, emotion and prosody from natural language; the maker's
  examples name gender, age, vocal range, pitch and its variation, emotion and confidence. [card]
- Any preset can speak any of the 10 languages: Chinese, English, Japanese, Korean, German, French, Russian,
  Portuguese, Spanish, Italian. [card] [pack]
- The maker says it is robust to noisy input text and adapts tone to what the text means. [card]
- Repeats bit for bit: the same text, voice, settings and seed on the same machine give the same audio, so a liked
  take can be remade exactly. [pack]
- In Dialogue each line is spoken at the run's one seed, so a line sounds the same wherever it sits in the script;
  you can reorder or cut lines without the others changing. [pack]

## Weak at
- No cloning here: this pack holds only Custom voice and Voice design; cloning a clip is ZONOS3's job. [pack]
- A designed voice drifts from line to line, so Dialogue refuses designed voices (presets only). [pack]
- Each preset sounds best in its own language; another language works but quality can drop. [card]
- High Variation (temperature) makes the voice livelier but it drifts. [pack]
- The maker's own table puts this 12 Hz model behind its 25 Hz sibling on long-speech word errors (2.8 % vs 1.2 %
  in English): long passages can slur or drop words more often. [card]
- Slower and heavier than ZONOS3: about 0.6 s of work per second of speech and about 6 GB, against ZONOS3's well
  under 1 s per line in about 2 GB. [pack]
- The maker gives no format or guide for voice descriptions; what wording works best is not known yet. [card]
- No sliders for emotion, energy or speed: everything goes through the Style words (or the description). [pack]
- Owner's takes: not read (see Sources), so nothing here is grounded in seen results yet.

## Modes and tasks
- **Qwen3-TTS Voice-over** (`qwen3-tts-voice-over`): one text, one preset speaker. Model variant fixed to
  `custom_voice`; `speak.voice` takes presets only. Use for narration and single lines in a preset voice. [pack]
- **Qwen3-TTS Voice design** (`qwen3-tts-design-voice`): one text in a designed voice. Variant fixed to
  `voice_design`; `speak.voice` takes designed voices only. This is also how the Voice design dialog lets the user
  hear a new voice before saving it. [pack]
- **Qwen3-TTS Dialogue** (`qwen3-tts-dialogue`): a script of lines, each `@name: text` or `@name (style): text`,
  every @name an element with a preset voice in the Cast. One take with a cue (start and end) per line. Presets only.
  [pack]
- How a mode is chosen: by the workflow, never by a setting. Each workflow offers only its own kind of voice
  (Voice-over and Dialogue: presets; Voice design: designed voices), and studio refuses a wrong one with "this
  workflow cannot use a designed voice" (or "... a preset voice"). [pack]
- A designed voice saved with a clip (Save as voice from a take) can be spoken by ZONOS3 as a clip voice, and ZONOS3
  Dialogue takes it. That is the way to use a designed voice in a dialogue. [pack]

## Settings that matter
- **Voice** `speak.voice` / per line `dialogue.lines[].voice`: the speaker. Match it to the character and to the
  language of the text (Ryan or Aiden for English, Ono Anna for Japanese, Sohee for Korean, the Chinese five for
  Chinese). [card] [pack]
- **Style** `speak.style` (Voice-over only, up to 500 characters; per line `dialogue.lines[].style`, up to 200, or
  `(style)` in the script): how to say it, in words. Empty means the speaker's natural read. Costs nothing.
  Not shown in Voice design: a designed voice carries its own description (and style) into the instruction. [pack]
- **Variation** `speak.temperature` / `dialogue.temperature`: 0.1-1.2, step 0.05, default 0.3. Low (0.3, "Steady")
  keeps the voice steady and the same take after take; raise toward 0.9 ("Lively") for more expressive reads,
  accepting drift; above 0.9 is rarely worth it. No time or memory cost. [pack]
- **Language** `speak.language` / `dialogue.language`: auto, en, zh, ja, ko, de, fr, ru, pt, es, it; default auto.
  Auto does not detect the text: it uses the voice's own language. Set it when the text is not in the speaker's
  language (Ryan reading French: `fr`). The maker also says to set it when known. [pack] [card]
- **Pause between lines** `dialogue.pause`: 0-2 s, step 0.05, default 0.35. Per line `dialogue.lines[].pause_s`
  0-5 s (default 0.35) for a beat after one line; an empty script line adds one more pause. [pack]
- **Seed** `seed.seed`: -1 picks a new one; keep the seed of a liked take to remake it or to change only the
  style. [pack]

## Prompt style
- Voice-over and Voice design: the box holds only the words to say. No scene description, no stage directions,
  no speaker names: everything in the box is read aloud. [pack]
- Write how it should sound in Style, not in the text: short tone or emotion words or a short phrase ("calm",
  "whispering", "very angry", "tired, slow, a little hoarse"). Chinese or English instructions both work. [card] [pack]
- Voice description (Voice design dialog): one or two sentences naming gender, age, pitch or range, pace, texture,
  emotion, accent: "A deep, calm man in his fifties, speaking slowly." (the pack's own test voice) or the maker's
  "Male, 17 years old, tenor range, gaining confidence." Up to 2000 characters, but short is the tested form.
  [pack] [card]
- Dialogue script: one line each, `@name: text` or `@name (style): text`; an empty line is a longer pause; up to
  40 lines, 600 characters a line, 8000 in all. [pack]
- Punctuation shapes the read: long text is cut on sentence ends into pieces of about 200 characters with 0.25 s of
  silence between them, so end sentences with full stops where a breath belongs. [pack]
- Write numbers, dates and abbreviations as you want them spoken; the pack does no text normalization (ZONOS3's
  server does). [pack]

## Speed and memory
- Mac (M5 Pro, torch MPS, bf16): load about 3.3-3.6 s; speech takes about 0.57 s per second of audio once warm
  (a 5.3 s line in 3.0 s); the first line of a fresh process adds 0.7-1.4 s. [pack]
- NVIDIA (CUDA, the GCP host): a 2.4 s line in 1.29 s, already loaded. [pack]
- Memory: process peak about 5.3 GB measured by the pack (5.9 GB in the first probe; RSS reads higher, 7.7 GB),
  flat in length (1.7 s and 20 s lines peak the same); components 3.78 GB,
  `peak_gb` 1.3 + 0.01 x seconds above them. Voice design reloads the shared speech tokenizer (0.21 GB more). [pack]
- A take is at most 900 characters, about 60 s; Dialogue at most 40 lines within the same 60 s cap. [pack]
- Speed with Voice design on long text, and cold load from disk: not known yet.

## Pitfalls
- Switching to a voice workflow keeps the box's text: an image or video prompt left there would be read aloud.
  Rewrite the box to only the spoken words first. [pack]
- Language auto follows the voice, not the text: a preset reading another language needs Language set. [pack]
- A designed voice cannot go into Dialogue (and drifts between lines); use presets, or save the designed voice with
  a clip and use ZONOS3 Dialogue. [pack]
- The same seed on a Mac and on NVIDIA gives different audio; to remake a take exactly, run it on the same host.
  [pack]
- Over 900 characters (about 60 s) fails: split long narration into several takes. [pack]

## Recipes (draft)
- **steady-narration**: Qwen3-TTS Voice-over; `speak.voice` = a preset in the text's language, `speak.style` = "",
  `speak.temperature` = 0.3, `speak.language` = the text's language, `seed.seed` = -1. [pack]
- **acted-line**: Qwen3-TTS Voice-over; `speak.style` = the emotion in a few words ("very angry", "whispering,
  scared"), `speak.temperature` = 0.3 (higher is untested); keep the seed and change only the style to compare reads. [pack] [card]
- **lively-promo**: Qwen3-TTS Voice-over; `speak.voice` = Ryan or Aiden, `speak.style` = "energetic, upbeat",
  `speak.temperature` = 0.9 (the Lively preset). [pack] [card]
- **new-character-voice**: Qwen3-TTS Voice design; a voice described in one sentence (age, gender, pitch, pace,
  texture), `speak.temperature` = 0.3, a short test line; the user hears it, then saves it. [pack] [card]
- **preset-dialogue**: Qwen3-TTS Dialogue; every Cast member a preset, `dialogue.temperature` = 0.3,
  `dialogue.pause` = 0.35, styles per line in `(style)`. [pack]
- **dramatic-pause-dialogue**: as preset-dialogue with `dialogue.pause` = 0.6 and an empty script line before the
  turn that should land. [pack]
- **chinese-dialect**: Qwen3-TTS Voice-over; `speak.voice` = Dylan (Beijing) or Eric (Sichuan), `speak.language` =
  zh. [card] [pack]

## Sources
- Maker: https://huggingface.co/Qwen/Qwen3-TTS-12Hz-1.7B-CustomVoice (revision
  0c0e3051f131929182e2c023b9537f8b1c68adfe), https://huggingface.co/Qwen/Qwen3-TTS-12Hz-1.7B-VoiceDesign (revision
  5ecdb67327fd37bb2e042aab12ff7391903235d3), https://github.com/QwenLM/Qwen3-TTS (instruction examples).
- Pack: runner_service/packs/qwen3_tts/model.yaml, GOTCHAS.md, torch.py, workflows/1-qwen3-tts-voice-over.json,
  2-qwen3-tts-design-voice.json, 3-qwen3-tts-dialogue.json; contracts/fixtures/registry/qwen3-tts.voices.valid.json
  (preset styles); runner_service/tests/test_qwen3_tts_golden.py and tests/golden/qwen3-tts/*.json (times, CUDA);
  docs/reference-notes/voices.md sections 8-12 (measured speed and memory); pipelines_service/assistant_knowledge.md;
  plugins/cinematic-filmmaker/SKILL.md and character-sheet.md.
- Owner's takes: not read. The CLI and the helmstudio MCP both answered "session expired" (401) and a fresh login
  is not allowed in this task, so there are no [takes] claims.
