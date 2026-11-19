# minimax_h3_image pack: gotchas

Measured 2026-11-19 (M-series 64 GB, torch 2.15 MPS, int8; `tests/test_minimax_golden.py`, phys_footprint per node).

- **Same pixels as the old code.** On this stack the pack's PNG is byte-identical to the old studio's own code run here
  (`record_minimax.py` with runner_service's venv): `t2i-1024-s42.torch-2.15.png`, and the enhancer's text
  `enhance-t2i-s42.torch-2.15.txt`. Against the old stack's goldens (torch 2.14.1, transformers 5.22.2): edit
  bit-exact, T2I 35.09 dB (the prompt embeddings are identical; the first velocity differs: kernels), the enhancer's
  sampled text another one (ratio 4:5, not 3:4). The pack cannot run on the old stack's venv (Python 3.12; the core
  needs 3.14).
- **Never two models.** Declared int8 sizes: enhancer 27.12 GB, text encoder 25.72 (51 layers), transformer 30.98 +
  VAE 2.18 (fp32). enhance and encode are `alone` + `unload_after`; the plan's peak is 38.9 GB at 4 MP.
- **Per node** (T2I 1024², a 4 MP canvas): enhance 117-122 s, 36.0-36.2 GB (3516 prompt tokens); encode 21-24 s,
  30.1-30.6 GB (load ~17 s); sample 84-87 s cold (transformer + VAE load ~30 s, 9.4 s/step), 38.4-38.8 GB; decode
  6.3 s, 37.4 GB. Edit (one 768² ref): encode 24 s, 30.6; sample 114 s, 38.7; decode 6.2 s. Total 112-145 s.
- **The footprint falls late** after an unload (14-22 GB still counted right after the text encoder went): the
  residency manager waits up to 10 s for it before evicting or failing, else sample's admission fails.
- **A seed change** re-runs sample and decode only (the encoding is a small cached result; Cache.shed keeps it when
  memory is short). It re-runs enhance when enhance is on (its seed is an input).
