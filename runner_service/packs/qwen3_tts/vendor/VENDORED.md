# Vendored: qwen3_tts (the AI-EXP port)

`qwen3_tts/` here is the package `src/qwen3_tts/` of AI-EXP (`~/GenAI/AI-EXP`, the owner's repo), commit
`16b31cc3cfaefb51fab07049a86677c5a1e4f0f1` (2026-09-27, the last commit touching it; the folder was clean), copied
**unchanged** (byte for byte): `__init__.py`, `talker.py`, `codec.py`, `decoder.py`, `synthesizer.py` (sha256 of
each in the table). It runs Qwen3-TTS without the `qwen-tts` package (which pins transformers 4.57) on the torch
env's own torch 2.15 + transformers 5.23 (docs/reference-notes/voices.md section 8).

| file | sha256 |
|---|---|
| `__init__.py` | `2f06ad6c02684ff9a0391ce5eb7ff46ba88c3d2d988a6c576fea800bc30773bb` |
| `codec.py` | `29cddac114087ebb616ce2238b69dab8fd029c67697d1258e015b421958a815f` |
| `decoder.py` | `8eef390789309124442bcb9f0ced0efe46adb78d2acb8f0ad5284509adf341bb` |
| `synthesizer.py` | `d41612a92d2ca6c8881fdc4a453bf512baaec9220ad4d1a08781dcfbcfaa4551` |
| `talker.py` | `4312f5b128fdd044775a8829cf94dc974df35e3c65c462855ef0413dc788bbc5` |

- Licence: the port follows the official `qwen-tts` package (Apache-2.0), as its docstrings say; AI-EXP itself has no
  licence file. The weights are Apache-2.0 (`../licences/apache-2.0.md`, the model card).
- What the pack (`../torch.py`) does differently, at the call sites only: modules are built on meta and filled by the
  core loader (`core/loaders_torch.load`, with a real 1-row `embed_tokens` per `Qwen3Model`, which the checkpoint
  lacks), never by `Synthesizer.load` / `load_weights`; `warm_up` is never called and the code predictor runs
  `enable_fast_decoding(compile=False)` (torch.compile on torch 2.15 MPS breaks end-of-speech: the scout's runaway
  257-frame take); sampling is seeded with `torch.manual_seed(seed)` before each piece of text, as `_seed()` does.
- To refresh: copy the same files from a newer commit, re-record `tests/golden/record_qwen3_tts.py` and run
  `tests/test_qwen3_tts_golden.py`, update this note.
