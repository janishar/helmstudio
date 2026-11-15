# Vendored: ltx-core-mlx

`ltx_core_mlx/` here is a subset of the package `ltx-core-mlx` 0.15.4 from ltx-2-studio
(`packages/ltx-core-mlx/src/ltx_core_mlx`), commit `b63613e0a896e6809ec989af7c533fc11aed924c` (2026-09-24), copied
**unchanged** (byte for byte): the 61 modules that the ltx pack (`../mlx.py`) imports and everything they import, found
by walking their imports. Nothing else of the package (pipelines, trainer, CLI, web) is copied: the pipeline itself is
re-written in `../mlx.py`, following `ltx_pipelines_mlx.distilled` and its helpers.

- Licence: MIT, `LICENSE` next to this file (Copyright (c) 2025 dgrauet, (c) 2026 Janishar Ali). It is a port of
  Lightricks' LTX-2 reference code.
- `ltx_core_mlx/loader/official_pack.py` holds key-renaming, transpose and quantization rules vendored from
  mlx-forge (https://github.com/dgrauet/mlx-forge, Apache-2.0: `recipes/ltx_25.py`, `recipes/ltx_25_text_encoder.py`,
  `transpose.py`, `quantize.py`), as its docstring says. The pack uses only those rules from it (no virtual pack is
  ever built or written).
- The weights (`~/GenAI/models/ltx-2.5`) are under Lightricks' own model licence, which none of this covers.
- Runtime dependency: `mlx-arsenal` (timestep embedding, Euler step), pinned to 0.2.4 as in the studio's lockfile.

M4 stage B added, also unchanged: `conditioning/prompt_relay.py` (Prompt Relay) and `duration_head/` (the
DurationHead for auto length). `loader/fuse_loras.py` is the reference the core's MLX bake follows
(core/loaders_mlx.py); the image preprocessing (`ltx_pipelines_mlx.utils.media_io`) and the image conditioning
order (`_orchestration.combined_image_conditionings`, `helpers.create_noised_state`) are re-written in `../mlx.py`.

To refresh: copy the same files from a newer commit, re-run `tests/golden/compare_ltx.py` and update this note.
