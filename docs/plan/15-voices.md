# helmstudio-next plan — Voices

[← Plan index](../README.md)

Status: draft 1 · 2026-11-21 · a plan, not built, not in a milestone yet. Scouted from the weights on disk, the
Qwen3-TTS model card, the old AI-EXP port (`~/GenAI/AI-EXP/src/qwen3_tts/`) and the ZONOS3 checkout
(`~/GenAI/duplex/zonos3`, commit 96d9e0e). Nothing was downloaded or built.

## 19. Voices: Qwen3-TTS and ZONOS3 as model packs

Two speech families make **audio takes**: voice-over from a text, and dialogue with several speakers. Those takes
then feed what exists already: LTX audio to video, the MiniMax H3 audio references, and the Timeline's audio
tracks. Both families sit behind the same voice params, the same saved voices and the same screens; only their
packs differ. The backlog's two entries (ZONOS3 2026-11-18, Qwen3-TTS 2026-11-19) become this section.

### 19.1 What is on disk, and what each model can do

| | Qwen3-TTS 1.7B CustomVoice | Qwen3-TTS 1.7B VoiceDesign | ZONOS3-Mini |
|---|---|---|---|
| Folder | `~/GenAI/models/Qwen3-TTS-12Hz-1.7B-CustomVoice` | `~/GenAI/models/Qwen3-TTS-12Hz-1.7B-VoiceDesign` | `~/GenAI/models/ZONOS3-Mini/gguf` (below) |
| Files | `model.safetensors` 3.8 GB bf16 (talker + code predictor), `speech_tokenizer/` 0.68 GB fp32 | the same layout; the speech tokenizer is byte-identical (same SHA-256) | GGUF: backbone (`backbone-ptq`), decoder (`zac-keep2`), speaker encoder, codec encoder + SSL teacher (raw-code cloning), `steer.gguf`, `accents.gguf`, `edit/` |
| Preset voices | 9: Vivian, Serena, Uncle_Fu, Dylan (Beijing), Eric (Sichuan), Ryan, Aiden, Ono_Anna, Sohee | none | 27 default voices in the weights' `gguf/voices/` (10 English with styles such as British, Irish, announcer, ASMR; 1 fr; 2 each of de, hi, it, ko, zh, pt, es, te), each a clip (mp3, wav or ogg) + codes + 192-d embedding, with ids in its README; their licence and the speakers' consent are not stated |
| Voice from a description | no | yes: `instruct` describes the voice ("male, 17, tenor, nervous") | no free text; "design" = steering sliders (gender, age, pitch, timbre…) |
| Clone from a clip | **no** (no speaker encoder in these weights; needs the 1.7B or 0.6B **Base** model, not on disk) | no | yes: a 3–60 s clip → speaker embedding (or raw reference codes) |
| Emotion and style | `instruct` in words ("very angry", "whisper") on a preset | inside the description | numeric controls: valence, arousal, dominance, loudness, tempo, clarity, whisper, moods, accents, disfluency, emphasis; per-span controls; speaking rate |
| Languages | 10: zh, en, ja, ko, de, fr, ru, pt, es, it (+ 2 Chinese dialects on presets); `auto` | the same 10 | 43 (`en_us`, `en_gb`, `fr_fr`, `pt_br`, `cmn`, …) |

Language codes differ (Qwen `english`, ZONOS3 `en_us`). Voices and params use BCP 47 tags (`en`, `en-US`, `zh`,
`pt-BR`); each pack maps them to its own codes and publishes only the tags it has.
| Output | 24 kHz mono | 24 kHz mono | 44.1 kHz mono |
| Sampling | stochastic (checkpoint default temperature 0.9, top-k 50) | the same | stochastic, `--seed` |
| Licence | Apache-2.0 (model card) | Apache-2.0 | code MIT (repo `LICENSE`); the weights' licence is unknown (no model card on disk) |

Other speech weights on disk, not packs here: `Qwen3-ASR-1.7B` (speech to text; useful later to transcribe a clone
clip), `Qwen2.5-Omni-3B`, `silero-vad`.

**ZONOS3 weights**: `~/GenAI/models/ZONOS3-Mini/gguf`, 9.2 GB, Hub revision `d2d142a8…` (copied from the SSD by
the owner on 2026-11-21; the four files below match the Hub's SHA-256 in the `.cache` sidecars). The pack needs four files, about 1.1 GB: `backbone-ptq.gguf` 632 MB,
`zac-keep2.gguf` 431 MB, `spk-encoder.gguf` 43 MB, `steer.gguf` 0.2 MB, plus `voices/` (about 25 MB). The rest is
for later or never: `zac.gguf` (2 GB, full decoder), `zac-encoder.gguf` + `ssl-teacher.gguf` (5.2 GB, raw-code
cloning), `backbone-ptq-edit.gguf` + `edit/` (speech editing). There is no top-level `accents.gguf`, so accent
adapters are not available.

Lessons from the old code:

- AI-EXP's port (about 1,000 lines: talker, codec, decoder, synthesizer) runs Qwen3-TTS on torch with transformers 5
  and **without** the `qwen-tts` package (which pins transformers 4.57). It found: about 2× real time on the GPU,
  ~1× when sharing it; temperature 0.3 with a fixed seed keeps a voice stable, 0.9 drifts; a designed voice drifts
  from line to line even with a fixed seed.
- The model card's own advice for a consistent designed voice: design a short clip, then clone it. That is the
  pattern here (19.5, Save as voice).
- ZONOS3's Python packages pin torch 2.9.1 and transformers ≤ 4.57.3, and its MLX server imports torch, so it does
  not fit either Python env. Its **ggml C++ CLI** (`servers/ggml`: `zonos3-cli`, `spk-encoder-cli`) runs on Metal
  with no Python, like h3.c.
- Zonos Voice Studio's data model (`studio/src/state/`): a script of lines `{text, spans, voice}` with a 0.35 s
  default gap, a cast of personas, own voices `{clip, embedding}`. The plan keeps the same three ideas.

### 19.2 Runner env, memory, sharing the GPU

| Pack | Env | Why | Resident | Peak (to measure) |
|---|---|---|---|---|
| `qwen3_tts` | **torch** (MPS; CUDA later) | a port on the env's own pins (torch 2.15, transformers 5.23), as AI-EXP did; no new env | talker 3.8 GB + speech tokenizer 0.7 GB per variant; the tokenizer is one shared entry | ≈ 6 GB (placeholder: fit `peak_gb` from runs at 2 lengths) |
| `zonos3` | **native** | a vendored, pinned `zonos3-cli` as a child process (core.child), like `minimax_h3` | nothing after a run | the child's whole footprint, measured (unknown; the files it loads are small: backbone-ptq 0.66 GB plus decoder, speaker encoder and steer, so likely 2–4 GB) |

- Not MLX: mlx-audio 0.6.5 (voiceflow) runs Qwen3-TTS, but it brings mlx-lm and its own pins into the mlx env that
  LTX and the llm pack share, and the torch port is already proven. Revisit only if MPS is too slow.
- **Sharing the one GPU** needs nothing new. The dispatcher runs one run per host across envs; a voice run is small,
  so it usually fits beside a warm LTX (45 GB of the 52 GB budget) without a hand-over, and when it does not, the
  existing hand-over evicts first. Both packs publish honest memory items, so the planned peak and "≈ time · N GB"
  under Generate work as for the other packs.
- Long work: a line is capped (19.3) so a run stays seconds to a minute; `stall_s` covers the talker loop, which
  passes `ctx.check()` every generated frame.

### 19.3 Contract changes (contracts first)

All in one API minor (1.30). The fake `echo` family gains `echo/speak` and `echo/dialogue` (a sine tone per line,
deterministic) so the contract suite covers everything without a GPU.

**Registry (`registry.schema.json`)**

- Port type **`Audio`** (registry `portType` and openapi `PortType`): a plain value `{wav: bytes, sample_rate,
  duration_s, cues?}`. The core `output` node's `media` input (type `Media`) also accepts `Audio` (the `portType`
  text and Graph rules' fits change with it) and stores it as a WAV, `<prefix><n>.wav`; the Workflow.media rule
  adds: `audio` when the output is wired from an Audio. A model's `media` already allows `audio`.
- Param type **`voice`**: one saved voice, default null (a run needs one: `<node>.<param>: a voice is required`),
  with `accepts` ⊆ `[preset, clip, design]` and, for `preset`, the family's speaker ids. It takes no wire. Allowed
  as a `rows` item field (as media fields are since 1.22).
- Param flag **`script: true`** on a `text` param: studio expands it into the node's `lines` rows before the run
  (below). Not scanned for ordinary mentions.
- **Mentions in spoken text**: in a workflow whose media is `audio`, `prompt.text` is not expanded (an element's
  description would be read aloud); `@` there is refused (`prompt.text: mentions are not read aloud; use a dialogue
  script`). Looks do not apply to audio workflows (`fits` false: `looks do not apply to speech`).
- Formula variable **`seconds`** in `peak_gb`: the planned speech length, from the stage's text (characters ÷ 15,
  capped at the stage's longest); the memory plan uses the default text. `plan_media` is not used (it plans input
  takes).
- Packs may ship **starter voices**, `voices/*.yaml` (id, name, speaker, language, a preview clip as WAV beside it),
  published as `<host>.<env>.voices`, a new registry key with its own schema, following the starter looks' rules
  (`core/looks.py`: checked at start, a broken file stops the runner, the clip PUT once under
  `packs/<family>/voices/`). Every preview exists before any run (ZONOS3's clips from the weights' `gguf/voices/`, converted to WAV
  by ffmpeg at start; the 9 Qwen presets spoken once by the pack author at a fixed seed and committed).

**Messages**

- `evt.run.output`: `kind` adds `audio`; key `<output_prefix><n>.wav` (PCM 16-bit, the model's own rate, mono);
  data adds `audio {duration_s, sample_rate, cues?}`.
- `defs.schema.json` adds **`voice`** = `{item_id, version, source: preset | clip | design, speaker?, clip?: media,
  text?, language?}`, used by `cmd.run.execute`; a clip is a defs `media` value with the claim-check rules of Media
  inputs (the runner reads it under `u/<owner_id>/`, checks bytes and etag).

**OpenAPI (`openapi.yaml`)**

- **A saved voice is a library item**: `LibraryKind` adds `voice`, groups `preset | cloned | designed`. A voice has
  no handle (`@` always names an element). New field `voice {source, family?, speaker?, clip?: MediaRef (3–30 s,
  trim), design?: text ≤ 2000, language?, style?}`. `GET /library?kind=voice&workflow=` answers `fits` / `reason`
  as for looks (`this workflow cannot clone a voice`, `voice Ryan is for qwen3-tts`). Starter voices are read-only
  items `<family>/<id>`, like starter looks. Cap: the existing 200 items per user.
- **A character element may carry a voice**: element field `voice_id` (null, or one of the caller's voices or a
  starter voice). A deleted voice leaves `voice_id` null on read, as a deleted take drops out of an element.
- **Save as voice** (`POST /library` with `from_take`, which today makes looks only, so its rule and refusal text
  change): from an audio take or a video's sound, a `cloned` voice of that part (in/out, 3–30 s); from a
  VoiceDesign take, a `designed` voice keeping the description **and** the clip, so it can also be cloned.
- **A voice keeps its own clip.** Saving copies the part, as WAV, to `u/<owner>/voices/<item_id>.wav` (studio asks
  a media job to cut it), so deleting the source take later never breaks the voice; `from_take_id` goes null as a
  look's does. Deleting the voice deletes the copy.
- **Scripts**: in a `script` param, each non-empty line is `@handle: text` or `@handle (style): text`; an empty
  line is a pause (default 0.35 s). Studio expands it into `lines` rows `{voice, text, style, pause_s}` from each
  element's `voice_id`, at create time, like mentions (`made_with.typed` keeps the script; `made_with.voices` lists
  `{id, name, version}`). 422 texts: `dialogue.script line 3: @jon has no voice`, `… unknown element @jon`, `… voice
  Ryan does not fit this workflow`, `dialogue.lines: at most 40 lines`.
- **Takes**: `Take.audio` adds `sample_rate` and `cues [{line, start_s, end_s, voice_id, text}]` (a dialogue's
  lines); `Workflow.media` and `?media=` add `audio`; the Gallery's kind filter already has `audio`.
- **Pipelines**: `bind_type` lets a takes / picked source of one take fit a `media` param, or a rows param with
  exactly one media field (today only `image` params and Image rows), so a voice step's take can feed LTX's
  `audio.source` or an H3 `audios` row.
- **Offers**: "rows of media are never offered into" changes for a rows param with exactly one media field and no
  item yet (the take as item 1), so Use in… lists the H3 references template for an audio take. H3 still refuses
  audio references without an image or video reference (`audio references need an image or video reference`), so
  that offer opens Create with the audio bound and asks for the picture.
- **Timeline**: the tracks, sample grid and mixing exist; only `wrong_track` refuses audio takes. Audio takes go on
  A2–A9 at `at`, trimmed on the sample grid; the 1.19 refusal `an audio take cannot go on the timeline yet` goes.
  `export_plan` gets an audio take's facts (`audio.duration_s`, `sample_rate`, mono) and always resamples it to
  `target.sample_rate` (48 kHz) and up-mixes to stereo, as audio is always re-encoded. "New line" (19.5) is a
  server-made change like New shot: `POST …/timelines/{id}/lines`.
- **Agent tools**: `library.read` returns `kind: voice` items and an element's `voice` name, so a Write node or the
  director knows which characters can speak.

### 19.4 Pack layout (`runner_service/packs/`)

```text
packs/qwen3_tts/
  model.yaml            # family qwen3-tts; media [audio]; runtimes torch {env: torch, devices: [mps]}
  torch.py              # speak(), dialogue(), build(), rename(): the AI-EXP port, vendored below
  vendor/qwen3_tts/     # talker, code predictor, speech tokenizer decoder; VENDORED.md with the source and licence
  voices/presets.yaml   # the 9 CustomVoice speakers (starter voices; no preview clip: made on first audition)
  licences/apache-2.0.md
  workflows/1-qwen3-tts-voice-over.json
  workflows/2-qwen3-tts-design-voice.json
  workflows/3-qwen3-tts-dialogue.json
  GOTCHAS.md
packs/zonos3/
  model.yaml            # family zonos3; media [audio]; runtimes native {env: native, devices: [mps]}; one variant
                        # `mini` (dir ZONOS3-Mini) with components backbone (gguf/backbone-ptq.gguf), decoder
                        # (gguf/zac-keep2.gguf), speaker_encoder (gguf/spk-encoder.gguf), steer (gguf/steer.gguf) for
                        # presence, and `source {repo: Zyphra/ZONOS3-Mini, allow_patterns: those four files}`: not the
                        # whole 9.3 GB folder (edit, f16 and qat files are left out)
  native.py             # speak(), dialogue(): core.child.run of the vendored zonos3-cli
  voices/defaults.yaml  # the 27 default voices: names and ids; clip + embedding read from the weights' gguf/voices/
  licences/zonos3.md
  workflows/1-zonos3-voice-over.json
  workflows/2-zonos3-dialogue.json
  GOTCHAS.md
runner_service/vendor/zonos3/   # servers/ggml at commit 96d9e0e + ggml + textnorm; VERSION, LICENSE, NOTICE and
                                # third_party licences; built by `make zonos3`
```

Building `zonos3-cli` needs CMake, Apple clang, the ggml sources and a Rust toolchain (cargo builds the textnorm
library); `--clone` from a file needs ffmpeg (already a runner dependency). These go in 14-dependencies.md as build
tools of the native env when the pack is built.

`qwen3_tts/model.yaml` in short (`peak_gb` are placeholders):

```yaml
id: qwen3-tts-1.7b
family: qwen3-tts
title: Qwen3-TTS
media: [audio]
licences: {apache-2.0: {title: Apache License 2.0}}
runtimes:
  torch: {env: torch, devices: [mps]}
model:
  variant: {label: Voice model, help: "Custom voice: 9 preset speakers with a style. Voice design: a voice from words."}
variants:
  custom_voice: {dir: Qwen3-TTS-12Hz-1.7B-CustomVoice, title: Custom voice,
                 source: {repo: Qwen/Qwen3-TTS-12Hz-1.7B-CustomVoice, revision: <sha from .cache sidecar>,
                          allow_patterns: ["*.json", "*.txt", "model.safetensors", "speech_tokenizer/*"],
                          licence: apache-2.0}}
  voice_design: {dir: Qwen3-TTS-12Hz-1.7B-VoiceDesign, title: Voice design, shares: [speech_tokenizer],
                 source: {repo: Qwen/Qwen3-TTS-12Hz-1.7B-VoiceDesign, revision: <sha>,
                          allow_patterns: ["*.json", "*.txt", "model.safetensors"],   # never the shared tokenizer
                          licence: apache-2.0}}
components:
  talker: {files: "model.safetensors", dtype: bf16, precision: [bf16]}
  speech_tokenizer: {files: "speech_tokenizer/model.safetensors", dtype: stored, precision: [fp32]}
stages:
  speak:
    title: Speak
    component: talker
    help: Reads the text in one voice. Long text is split on sentences and joined.
    uses: [talker, speech_tokenizer]
    inputs: {model: Model, text: Text, seed: Seed}
    outputs: {audio: Audio}
    params:
      voice: {type: voice, label: Voice, default: null, accepts: [preset, design]}
      language: {type: enum, label: Language, default: auto, enum: [auto, en, zh, ja, ko, de, fr, ru, pt, es, it]}
      style: {type: string, label: Style, default: "", max: 500, when: "model.variant == 'custom_voice'",
              help: "How to say it, in words: calm, angry, whispering."}
      temperature: {type: float, label: Variation, default: 0.3, min: 0.1, max: 1.2, step: 0.05,
                    help: "Low keeps the voice steady; high is livelier and drifts."}
    peak_gb: "1.2 + 0.02 * seconds"
  dialogue:
    title: Dialogue
    component: talker
    help: Speaks each line in its speaker's voice, with pauses, as one take; reports each line's start and end.
    uses: [talker, speech_tokenizer]
    inputs: {model: Model, seed: Seed}
    outputs: {audio: Audio}
    params:
      script: {type: text, script: true, label: Script, default: "", max: 8000}
      lines: {type: rows, label: Lines, default: [], min: 1, max: 40,
              item: {voice: {type: voice, default: null, accepts: [preset]}, text: {type: string, default: "", max: 600},
                     style: {type: string, default: "", max: 200}, pause_s: {type: float, default: 0.35, min: 0, max: 5}}}
      language: {type: enum, label: Language, default: auto, enum: [auto, en, zh, ja, ko, de, fr, ru, pt, es, it]}
      temperature: {type: float, label: Variation, default: 0.3, min: 0.1, max: 1.2, step: 0.05}
    peak_gb: "1.2 + 0.02 * seconds"
```

- A voice of `source: design` needs variant `voice_design`, a preset needs `custom_voice`: a `check_speak` refuses
  the wrong pair before loading (`speak: voice Ryan needs model variant custom_voice`).
- **Qwen dialogue takes presets only.** A designed voice drifts from line to line even at a fixed seed (AI-EXP),
  and Qwen cannot clone it without the Base model, so a scene with a designed voice is refused with the fix: save
  that voice (it keeps its clip) and use it in a ZONOS3 dialogue, which clones it. Qwen Base would lift this.
- zonos3's stages are the same two with `voice accepts: [preset, clip]`, language (43), and the controls a user can
  read: **Emotion** (valence, arousal), **Energy** (loudness, tempo), **Whisper**, **Speaking rate**, **Guidance**
  (cfg) as floats mapped to `--control ID=DOSE` and `--condition-buckets`; `uses: []`, `peak_gb` the child's whole
  footprint.
- **Studio layout and presets** (in each workflow's `studio` block): composer `[prompt.text]` (voice-over) or
  `[dialogue.script]`; quick `[speak.voice, speak.language, seed]`; inspector `Voice` (style, temperature),
  zonos3 `Delivery` (emotion, energy, whisper, rate). Presets in pack order: Qwen **Steady** (0.3) and **Lively**
  (0.9); ZONOS3 **Neutral**, **Warm**, **Tense**, **Whisper** (control doses).
- Cache: `speak` keys on the text, voice (its resolved value: a clip's owner, key, etag, in, out), params and
  seed, so a new seed re-runs only that; a ZONOS3 clip voice's embedding (`spk-encoder-cli`, CPU, seconds) is kept
  by the node cache, not stored in the library.

### 19.5 How it fits the product

**Voice-over from text.** Create gains a third switch value: Image | Video | **Audio** (`Workflow.media` filter).
The model chip offers Qwen3-TTS and ZONOS3. The prompt box holds the text; quick tiles are Voice (opens the voice
picker), Language and Seed; Generate reads "≈ time · N GB". The stage is a waveform player; the filmstrip shows
sound tiles. Take actions on an audio take: **Use in…** (Audio to video · LTX, MiniMax H3 references: the offers
work for audio takes today; the H3 one needs the offers change in 19.3), **Add to timeline**, **Save as voice**.

**Saved voices.** One voice picker (the shared visual picker, 8.4) with three tabs: **Presets** (starter voices per
family, with a preview), **Mine** (cloned and designed), and two buttons: **Clone from a clip** (pick an audio take
or a video's sound, trim 3–30 s, name it) and **Design a voice** (runs the design-voice template with a short
sample line; audition, then Save as voice). A character element's editor gets a Voice tile.

**Dialogue.** The dialogue template's composer is a script:

```text
@mara (quiet): You said you'd be back by noon.
@jon: I know. The road was closed.

@mara: Then you should have called.
```

Studio expands it to lines from each element's voice. The take is one audio file of the scene; its cues show as
line markers on the stage and later on the Timeline. One family per run: every voice in it must fit that family
(on Qwen, presets only; on ZONOS3, presets and any saved voice with a clip).

**Into audio to video.** An audio take goes into LTX audio to video (its `audio.source`, up to 13.4 s, trimmed)
through Use in… as it is today, or into an H3 audio reference (2–15 s each, beside an image or video reference)
after the offers change. A pipeline "line → voice → shot" binds the voice
step's take into `audio.source` (the `bind_type` change). Lip movement is only as good as LTX audio-to-video's: it
follows the sound, it is not a lip-sync model (out of scope, 19.8).

**Timeline.** Audio takes land on A2–A9 at the playhead (Add to timeline, the add-clips tray), trimmed and moved on
the sample grid; gain and mute as clips have now. A dialogue take's cues draw as ticks on its clip. **New line** is
a fifth thing the timeline generates (beside New shot, take actions, Replace, Re-render): at the playhead on an
audio track it opens the voice composer docked, prefilled with the sequence's cast; the result fills that slot. A
clip on an audio track offers **Make shot** (Audio to video at that clip's `at` and length, into V1), a take action
that needs only the bind above.

**Agents and pipelines.** A Write node writes a script (template `write-script/v1`: `@handle (style): text` lines
from a brief and the library, which now lists voices), bound into `dialogue.script`; a fan-out over a shot list
makes one voice-over per shot. The director can do the same through `rows.add`. No new pipeline node kinds.

### 19.6 Screens and boards

Three new boards (new files; existing boards are never edited), drawn 2026-11-21 and on the UI canvas, waiting
for the owner's approval before the screens are built:

| Board | Shows |
|---|---|
| `docs/mock/CreateAudio.dc.html` | Create in Audio mode: voice-over composer, waveform stage, sound-tile filmstrip, Delivery inspector; the dialogue composer with the script and line cues |
| `docs/mock/VoicePicker.dc.html` | the voice picker: Presets / Mine, Clone from a clip (trim), Design a voice (audition, Save as voice); an element's Voice tile |
| `docs/mock/TimelineAudio.dc.html` | audio takes on A tracks with cues, New line docked at the playhead, Make shot on an audio clip |

### 19.7 Goldens, checks and steps

**Goldens** (owner's GPU limits: short and small; here one or two short sentences, at most 4 s of audio, clips 3 s):

- `qwen3-tts`: recorded from AI-EXP's own `Synthesizer` on MPS bf16 at a fixed seed and temperature: a preset with
  a style, a design voice, a 3-line dialogue. Compare the generated codec codes exactly (same device, same seed) and
  the waveform by SNR ≥ 40 dB; fall back to a bar the owner accepts if MPS sampling is not repeatable (as H3's PSNR
  bars).
- `zonos3`: the vendored CLI against the duplex checkout's own build at the same commit and seed: bit-exact WAV
  expected (same binary); a clone from a 3 s clip; one control preset.
- A listen check by the owner on each golden, once; an ASR word check with Qwen3-ASR is a later option, not a gate.

**Checks** (the lean M7 form): per step one combined reviewer, the contract suite once per step on
`helmstudio-test`, each service's `make test lint`, `make contracts-check`, the screens against the new boards.

**Steps** (a proposed milestone, M8 "Voices"; the milestone table in 08 is changed only when the owner takes it up):

1. **Scout** (no product code): build `zonos3-cli` in scratch from the pinned commit; check its progress output on
   stderr (core.child needs `<phase> n/total` lines, else a small vendored patch), whether one process can speak
   several lines (else the dialogue stage runs `zonos3-server` as the child for the run), its memory and time on a
   3 s line; run the AI-EXP port on the torch env's pins with the core loader; record peaks at two lengths.
2. **Contracts** (1.30): everything in 19.3, the echo voice stages, fixtures, `check.py` references (script
   expansion, voice fit, audio takes on audio tracks).
3. **Qwen3-TTS pack + Create Audio + voice picker** (presets and designed voices), goldens.
4. **ZONOS3 pack + cloning + Save as voice**, goldens.
5. **Dialogue + Timeline audio**: scripts for both families, cues, audio takes on A tracks, New line, Make shot,
   the `bind_type` change, `write-script/v1`.

### 19.8 Out of scope

- ZONOS3 speech editing (replace a word in a take): it needs the edit backbone, FuseAlign and an ASR worker in
  Python with torch 2.9. Later, if wanted.
- Streaming playback while a line generates (the backlog's live preview covers video only).
- A lip-sync model; music and sound effects; mixing several families in one dialogue run.
- Downloading Qwen3-TTS Base or 0.6B variants: declared as `source` so an admin can fetch them from the Models
  screen later, never fetched by the build.

### 19.9 Open questions for the owner

Each with the recommended answer first.

1. **The unused 8 GB of ZONOS3 files.** Answered (owner, 2026-11-21): leave them. The whole folder stays in
   `~/GenAI/models`; nothing is deleted.
2. **Voice cloning on Qwen too?** Recommended: not now. ZONOS3 clones; Qwen's Base 1.7B (about 4.5 GB) is declared
   in the pack as a downloadable variant and added when you choose to fetch it.
3. **Where dialogue is written.** Recommended: a script in the dialogue composer (`@handle: text`), which Write
   nodes and the director also write; the Timeline's New line uses the same composer.
4. **One take per dialogue or one per line?** Recommended: one take per scene with per-line cues; a cue can be
   split into its own take later if you want per-line timeline clips.
5. **Voices on elements.** Recommended: a character element carries one voice (`voice_id`); a voice is its own
   library item with no `@` handle.
6. **Lip-sync.** Recommended: out of scope; LTX audio to video and H3 audio references are the way sound drives
   picture.
7. **Default temperature for Qwen3-TTS.** Recommended: 0.3 (steady voice, from AI-EXP), not the checkpoint's 0.9;
   the Lively preset gives 0.9.
8. **ZONOS3 weights licence.** No model card in the SSD folder. Recommended: check the Hub card when the pack is written
   and ship its text in `licences/` before any download is offered.
9. **New line on the Timeline.** Recommended: yes, as a fifth generating action (15.3 lists four "and only four").
10. **Milestone.** Recommended: take it up as M8 after the screens-to-boards work, in the five steps of 19.7.
11. **ZONOS3's 27 default voice recordings.** The repo's licence covers its code; the voices' licence and the
    speakers' consent are not written down. Recommended: ship them as starter voices only once the Hub card or
    Zyphra's docs allow it; until then ZONOS3 starts with no presets and you clone your own clips.
12. **Cloning someone's voice.** Recommended: Clone from a clip asks the user to tick "I have the right to use this
    voice" (stored on the voice, like voiceflow's `consent` field); no other check.
