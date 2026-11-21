# zonos3 gotchas

- **The server, not the CLI.** Only `zonos3-server` normalizes text (numbers, abbreviations, in the run's language)
  and speaks many pieces in one load; the CLI reads the text as typed. The server is started per run (`core.child.serve`,
  `--port 0`, the port read from its ready line `zonos3-server: listening on http://127.0.0.1:<port>`) and killed
  after, on cancel or on error.
- **Only the pack's files.** `native.argv` names `--zac zac-keep2.gguf`, `--spk-encoder` and `--steer` and passes
  `--tts-default-voices-dir ""`: left out, the server also loads `zac.gguf` (2 GB) and the default voices folder.
  The raw-reference encoders (`zac-encoder.gguf`, `ssl-teacher.gguf`) are still auto-detected beside the backbone when
  present, but only loaded by a reference-mode request, and this pack always sends `speaker_mode: embedding`.
- **Speaker CFG needs two slots.** Guidance > 1 is the server's `speaker_cfg_scale`, which runs a twin pass in a
  second batch slot: `--batch 1` answers 400 "speaker CFG needs --batch >= 2". The pack runs `--batch 2` (one
  request at a time).
- **Delivery doses are raw.** The server applies no calibration: `mood.valence` / `mood.arousal` = (v - 0.5) * 8
  (the safe -4..4 at the slider's ends), `delivery.whisper` = 1.5 v, speaking rate as the condition value 15 v UTF-8
  bytes/s (not a control), guidance as speaker CFG. A control at its neutral point sends nothing; the speaking rate is always sent.
- **Seeds are 32-bit** on the server: the run's seed is sent modulo 2^32.
- **A preset is its embedding file**, found by name (`voices/<Language>/<Name>_*.embedding.npy`, the voice id lower
  case), sent inline; a clip voice's in..out is cut by ffmpeg to a bit-exact mono WAV and embedded by the server's
  speaker encoder on each run (the embedding is never stored). Both repeat bit for bit.
- **Repeats bit for bit**, and the vendored build speaks the same bytes as a build of the untouched duplex commit:
  the goldens (tests/golden/zonos3/, recorded by tests/golden/record_zonos3.py from that reference build) are exact.
- **Measured** (2026-11-21, M5 Pro, the vendored build, warm, child phys_footprint every 50 ms): one line 2.1-3.7 s
  of speech in 0.7-1.2 s wall, server start included; a 20 s text (3 pieces) in 2.1 s; peak 1.67-1.69 GB, flat in
  length: `peak_gb` 2.0. The first run after a build or a boot compiles the Metal libraries (~4 s more).
- **Previews are pinned bytes.** The 27 `voices/*.wav` are the weights' clips converted once with ffmpeg 9.0.2
  (`voices/defaults.yaml`); their sha256 is in the fixture's keys, so tests/test_zonos3.py fails if they change.
- **Dialogue (API 1.32): one server, a line's style as Delivery.** `zonos3/dialogue` starts one server for all lines,
  makes each voice's fields once and speaks every line at the run's seed. A line's `(style)` is read as words
  (lower-cased letters); each word `native.STYLE` knows sets sliders of that line's Delivery, **replacing** the run's
  values of those sliders for that line only (the others keep the run's; the next line starts from the run's again),
  later words winning:
  whisper(ing) → whisper 0.9, energy 0.25, rate 0.9 (the Whisper preset); quiet / soft / calm / gentle → energy 0.3;
  warm / kind / tender → emotion 0.7, energy 0.45 (Warm); happy / cheerful → emotion 0.8; sad / tired → emotion 0.25,
  energy 0.35; tense / urgent / nervous / scared → emotion 0.3, energy 0.75, rate 1.1 (Tense); angry / shouting /
  loud → emotion 0.2, energy 0.9; excited / eager → emotion 0.7, energy 0.8; slow(ly) → rate 0.85; fast / quick(ly)
  / hurried → rate 1.15. Any other word is ignored (`with a shrug` speaks as the run's Delivery).
- **A short line without a speaking rate runs long, so the rate is always sent.** With no rate condition (the
  bucket unknown) two- or three-word lines at seed 1 came out 2.4-8 s (`Ready when you are.` 8.0 s on Savannah); with
  one they are 0.7-1.5 s. Rate 1 sends 15 bytes/s, which falls in bucket 14 of the checkpoint's 30 (edges 14.89 and
  15.19: the middle, a normal pace).
