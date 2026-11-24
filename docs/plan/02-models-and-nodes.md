# helmstudio plan — Model config and nodes

[← Plan index](../README.md)

## 5. Model config and node implementation

```yaml
# packs/qwen_image/model.yaml
id: qwen-image-2.1
family: qwen-image
hue: {dark: "#45c4b0", light: "#1a7f70"}
runtimes:
  torch: {env: torch, devices: [mps, cuda]}      # mlx: added when a port exists
variants:
  base:  {dir: Qwen-Image-2.1, repo: Qwen/Qwen-Image-2.1,
          license: {id: apache-2.0}}
  turbo: {dir: Qwen-Image-2.1-Turbo, repo: Qwen/Qwen-Image-2.1-Turbo,
          shares: [text_encoder, vae], license: {id: qwen-research}}
  # a pre-quantized variant: loads as is, offers no load-time quantization (section 9.2)
  # base-int8: {dir: Qwen-Image-2.1-int8, quantized: int8, shares: [text_encoder, vae]}
components:                                       # files are globs inside the model folder
  transformer:  {files: "transformer/*.safetensors", dtype: bf16,
                 precision: [bf16, int8], quantize: ["transformer_blocks.*"]}
  text_encoder: {files: "text_encoder/*.safetensors", dtype: bf16,
                 precision: [bf16, int8], quantize: ["*.language_model.layers.*"]}
  vae:          {files: "vae/*.safetensors", dtype: bf16, precision: [bf16]}
stages:
  enhance:
    uses: [enhancer]
    params: {think: {type: bool, default: false, help: "Slower, better rewrites"}}
  encode:
    uses: [text_encoder, vae]
    params:
      refs: {type: rows, max: 10, item: {image: Image}, mention: "<image{n}>"}
  sample:
    uses: [transformer]
    roles: {single: {grid: 32, min: 256, max: 4096, default_mp: 4}}
    params:
      steps: {type: int, default: 40, min: 1, max: 200,
              when: "model.variant != 'turbo'"}   # turbo uses the sigmas baked in its config
    peak_gb: "0.9 + 5.1 * mp * (1 + refs)"        # formula over the node's own inputs
  decode:
    uses: [vae]
    params: {tiled: {type: enum, values: [auto, on, off], default: auto}}
    peak_gb: "tiled ? 6 : 16 * mp"
lora:
  component: transformer
  renames: []                                     # family-specific key fixes only
```

- `when` (visibility) and `requires` (a constraint with a human message that
  disables a field or blocks Run) are written in the small expression grammar of `contracts/` and evaluated by
  studio_service against the
  current graph, including upstream values such as the Model's variant. The UI
  asks the API for the resolved schema; it never re-implements model rules
  in JavaScript (today every studio duplicates its size maths in JS).
- LoRA key style (PEFT, kohya, ComfyUI) is detected from the file, not configured.
- The numbers in `peak_gb` above are placeholders; real formulas are fitted
  from measured runs (section 6.2).

```python
# packs/qwen_image/torch.py — the model-specific 300–500 lines, nothing else
from helm.node import stage

@stage("qwen-image", "encode")
def encode(ctx, model, prompt, refs):
    te = ctx.component(model, "text_encoder")   # loaded, cached, evicted by the scheduler
    return Conditioning(...)

@stage("qwen-image", "sample")
def sample(ctx, model, cond, canvas, seed, steps):
    dit = ctx.component(model, "transformer")   # this Model's LoRA set is active here
    for i in ctx.steps(steps):                  # progress, ETA, cancel point, honest sync
        ...
        ctx.preview(latents)
    return Latent(...)
```

A **pack** is a folder: `model.yaml`, one module per runtime (`torch.py`,
`mlx.py`), `workflows/*.json`, and a `GOTCHAS.md` carrying the hard-won notes
from the old studio. Packs are plain trusted Python. A pack may also ship `looks/*.yaml` (section 14).
