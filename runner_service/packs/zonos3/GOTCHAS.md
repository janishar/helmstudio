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
  bytes/s (not a control), guidance as speaker CFG. A value at its neutral point sends nothing.
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
