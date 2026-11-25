# ZONOS3: notes for the Assistant

## Card
ZONOS3-Mini is a small, fast speech model: it reads text in one of 27 preset voices or in a voice cloned from a 3-30
s clip, in 43 languages (44 language codes), at 44.1 kHz. Two workflows: Voice-over and Dialogue. Delivery is set with sliders (Emotion,
Energy, Whisper, Speaking rate, Guidance) or per line with style words. About 2 GB and under 2 s a line. Pick it for
cloned or saved voices, languages beyond Qwen3-TTS's 10, fine delivery control, or speed; pick Qwen3-TTS for its
preset speakers with a style in words, or to invent a voice from a description.

## Good at
- Cloning: any saved voice with a clip (cloned, or designed and saved with its clip) speaks here; the clip is 3 to
  30 s. The maker's example uses a 5-10 s clip and needs no transcript. [pack] [card]
- Speaks a clip made by another model: the pack's own test clones a Qwen3-TTS preset (Uncle Fu) from 3 s of its
  preview. So a voice designed in Qwen3-TTS and saved with a clip keeps working here, also in Dialogue. [pack]
- 27 presets with a clear character: English Cillian (Irish man), Imogen (British woman), Rupert (British man), Jett
  (energetic man), Morgana (witch), Sakura (anime woman), Savannah (American woman), Wyatt (American man), Thaddeus
  (announcer), Willow (ASMR, soft and close); French (Gaspard, a man only); and a man and a woman for German (Liesel,
  Wolfgang), Hindi (Ananya, Arjun), Italian (Ginevra, Lorenzo), Korean (Minjun, Seoyeon), Chinese (Haoran, Meilin,
  both energetic), Brazilian Portuguese (Leonor, Thiago), Spanish (Rodrigo, Ximena) and Telugu (Chaitanya, Sravya).
  [pack]
- Many languages: 44 language codes in the picker, from Arabic to Vietnamese (en-US and en-GB both listed) [pack];
  43 languages by the maker [card].
- Text normalization: numbers and abbreviations are read in the chosen language, for English, Mandarin, Japanese,
  Korean, German, Spanish, French, Italian and Portuguese. [pack] [card]
- Fine delivery on sliders (emotion, energy, whisper, rate, closeness to the voice), and per line in Dialogue
  through style words. The maker says delivery can be steered down to single words; this pack exposes the sliders
  only. [pack] [card]
- Fast and light: a 2-4 s line in about 1 s, a 20 s text in about 2 s, about 1.7 GB, server start included. [pack]
- Repeats bit for bit: same text, voice, Delivery and seed give the same audio. [pack]

## Weak at
- Cannot invent a voice from words: design it in Qwen3-TTS Voice design, save it with its clip, then use it here.
  [pack]
- No style instruction in free words: only the style words the pack knows change anything (list under Prompt
  style); other words are ignored. [pack]
- Languages outside the nine normalized ones are read as written: digits and abbreviations there may come out
  wrong. [card]
- High Guidance strains the voice. [pack]
- Very short lines ran long (2-8 s for three words) before the pack always sent a speaking rate; that is fixed, but
  shows the rate matters for short lines. [pack]
- The maker publishes no limitations, quality numbers for cloning, or recommended slider values: not known yet.
  [card]
- Owner's takes: not read, so nothing here is grounded in seen results yet.

## Modes and tasks
- **ZONOS3 Voice-over** (`zonos3-voice-over`): one text in one voice, preset or clip. Narration, single character
  lines, any cloned voice. [pack]
- **ZONOS3 Dialogue** (`zonos3-dialogue`): a script of `@name: text` or `@name (style): text` lines, every @name an
  element with a voice in the Cast (presets or clip voices, mixed freely). One take with a cue per line. The
  cinematic-filmmaker plugin uses this for scenes (`dialogue-scene`). [pack]
- How a mode is chosen: by the workflow. Model variant is always `mini`, precision always `int8` (stored, not a
  choice). [pack]
- Clone a voice (cloneVoice in the voice picker) from an audio take or a video's sound, 3 to 30 s of clean speech,
  only from a voice the user has the right to use; let the user hear it, save it, link it to the element. [pack]

## Settings that matter
All Delivery settings exist on both workflows: `speak.<name>` in Voice-over, `dialogue.<name>` in Dialogue.
- **Voice** `speak.voice` / `dialogue.lines[].voice`: a preset or a clip voice. Match the preset's language to the
  text. [pack]
- **Language** `speak.language` / `dialogue.language`: default en-US; decides how numbers and abbreviations are
  read (en-GB for British readings). Set it to the text's language; there is no auto. [pack]
- **Emotion** `emotion`: 0-1, step 0.05, default 0.5 (adds nothing). Lower sounds sad, higher happy. Warm uses 0.7,
  Tense 0.3. The slider's ends are the model's safe limits (-4..4), not past them. No cost. [pack]
- **Energy** `energy`: 0-1, default 0.5. Low is calm and soft, high excited. Whisper preset uses 0.25, Tense 0.75.
  [pack]
- **Whisper** `whisper`: 0-1, default 0. 1 whispers every word; 0.9 is the Whisper preset. [pack]
- **Speaking rate** `speaking_rate`: 0.5-1.5, default 1 (a normal pace, the model's middle rate). 0.85-0.95 for
  weight, 1.1-1.15 for urgency. A slow rate makes the pack cut long text into shorter pieces. [pack]
- **Guidance** `guidance`: 1-5, step 0.1, default 2. How closely it keeps to the voice; 1 turns it off; high values strain;
  the Tense preset uses 2.5. Above 1 runs a twin pass (a little more time). Whether 2.5-3 holds a drifting cloned
  voice better is not known yet. [pack]
- **Pause between lines** `dialogue.pause`: 0-2 s, default 0.35 (cinematic-filmmaker uses 0.5); per line
  `dialogue.lines[].pause_s` 0-5 s. [pack]
- **Seed** `seed.seed`: -1 picks one; the server uses it modulo 2^32. Keep a liked take's seed to change only the
  Delivery. In Dialogue every line uses the run's seed. [pack]

## Prompt style
- The box holds only the words to say; anything else is read aloud. [pack]
- Write normally in the nine normalized languages (numbers and "Dr." are fine); in other languages spell numbers
  and abbreviations out. [card] [pack]
- Delivery goes in the sliders, or per Dialogue line in `(style)` words. Known words, each setting only its line
  (later words win, the rest keep the run's values): whisper/whispering (whisper 0.9, energy 0.25, rate 0.9);
  quiet, soft, calm, gentle (energy 0.3); warm, kind, tender (emotion 0.7, energy 0.45); happy, cheerful (emotion
  0.8); sad, tired (emotion 0.25, energy 0.35); tense, urgent, nervous, scared (emotion 0.3, energy 0.75, rate
  1.1); angry, shouting, loud (emotion 0.2, energy 0.9); excited, eager (emotion 0.7, energy 0.8); slow, slowly
  (rate 0.85); fast, quick, quickly, hurried (rate 1.15). Any other word ("with a shrug") does nothing. [pack]
- Long text is cut on sentence ends (pieces of up to 200 characters, about 13 s; 100 characters in Chinese, Japanese and Korean, fewer at a slow
  rate) with 0.25 s of silence between
  them: put full stops where a breath belongs. [pack]
- Clips for cloning: clean, single speaker, no music; 5-10 s is the maker's example length. [card] [pack]

## Speed and memory
- Mac (M5 Pro, Metal): one line of 2-4 s in 0.7-1.2 s wall, server start included; 20 s of text in 2.1 s. The first
  run after a reboot or a build adds about 4 s (Metal compile). [pack]
- Cloning from a clip adds well under a second (the clip is embedded on every run, never stored). [pack]
- Memory: peak 1.67-1.69 GB, flat in length; `peak_gb` 2.0. It fits next to almost anything. [pack]
- NVIDIA (CUDA build): runs, but its speed is not known yet.
- At most 900 characters (about 60 s) a take; Dialogue up to 40 lines within the same 60 s. [pack]

## Pitfalls
- Switching from an image or video workflow keeps the box's text; rewrite it to only the spoken words. [pack]
- A clip outside 3-30 s fails ("a voice clip is 3 to 30 s"); trim the clip first. [pack]
- Language is not detected: a French text with en-US reads numbers in English. [pack]
- Unknown style words are silently ignored: use the listed words, or set the sliders. [pack]
- Over 900 characters fails: split long narration into takes. [pack]
- Cloning only from voices the user has the right to use. [pack]

## Recipes (draft)
- **voice-line** (neutral): ZONOS3 Voice-over; `speak.emotion` 0.5, `speak.energy` 0.5, `speak.whisper` 0,
  `speak.speaking_rate` 1, `speak.guidance` 2 (the Neutral preset). [pack]
- **warm-narration**: Voice-over; emotion 0.7, energy 0.45, whisper 0, speaking_rate 0.95, guidance 2 (Warm).
  [pack]
- **tense-line**: Voice-over; emotion 0.3, energy 0.75, whisper 0, speaking_rate 1.1, guidance 2.5 (Tense). [pack]
- **whisper**: Voice-over; emotion 0.5, energy 0.25, whisper 0.9, speaking_rate 0.9, guidance 2 (Whisper). [pack]
- **cloned-character**: Voice-over or Dialogue with a clip voice; Neutral values; `guidance` 2.5 to try if the voice
  wanders from its clip (untested). [pack]
- **dialogue-scene**: ZONOS3 Dialogue; Neutral values, `dialogue.pause` 0.5, `(style)` words per line; every
  @name in the Cast. [pack]
- **announcer**: Voice-over; `speak.voice` Thaddeus (the announcer preset), Neutral values; the slider values for
  more punch are not known yet. [pack]
- **asmr**: Voice-over; `speak.voice` Willow (ASMR, soft and close), Neutral values or the Whisper preset. [pack]

## Sources
- Maker: https://huggingface.co/Zyphra/ZONOS3-Mini (revision d2d142a86b1e6aeb4252679673c86ecd86177bf8).
- Pack: runner_service/packs/zonos3/model.yaml, GOTCHAS.md, native.py, voices/defaults.yaml,
  workflows/1-zonos3-voice-over.json, 2-zonos3-dialogue.json; runner_service/tests/golden/zonos3/*.json (times,
  the cross-model clone); docs/reference-notes/voices.md sections 1-7; pipelines_service/assistant_knowledge.md;
  plugins/cinematic-filmmaker/SKILL.md and character-sheet.md.
- Owner's takes: not read. The CLI and the helmstudio MCP both answered "session expired" (401) and a fresh login
  is not allowed in this task, so there are no [takes] claims.
