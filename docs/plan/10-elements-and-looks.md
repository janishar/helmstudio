# helmstudio plan — Elements and Looks

[← Plan index](../README.md)

## 14. Elements and Looks

Adapted from Higgsfield Cinema Studio (looked at 2026-11-14) for local
open-weight models. Both are **library items** (section 4): one record shape,
one table, one YAML schema. They can be used in any composer — Create, and the
Timeline's shot composer (section 15.3).

### 14.1 In the UI

- **Elements** — characters, locations, props. Type `@` in a prompt to insert
  one (section 4.1). Made from the gallery: pick images, give a name.
- **Looks** — film, camera, colour, lighting. Chosen from the visual picker
  (section 8.4) through a composer tile. Packs ship starter looks; "Save as
  look" on a take (section 9.1) makes your own.
- Which groups a template shows is its `studio.quick` list (section 7).

### 14.2 How they are applied

studio_service's resolver builds the run in this order (it owns presets and
library items):

1. parameter defaults
2. the chosen preset
3. library items — text appended to the prompt, images appended to the
   family's reference rows, LoRAs appended to the Model's LoRA set at their
   saved strength
4. the user's own edits

The resolved graph is what is hashed, cached and stored in the take, so a take
stays reproducible after a look or element is edited. No node does this
expansion; the graph stays typed.

```yaml
# packs/ltx/looks/lighting.yaml
- id: window-light
  group: lighting
  name: Window
  text: "soft daylight from a single window, deep falloff into shadow"
- id: silhouette
  group: lighting
  name: Silhouette
  text: "strong backlight, subject in silhouette, rim light on edges"
```

Write nodes (section 16.2) are given the library and answer with `@element`
mentions and look ids; this resolver expands them unchanged.

A library item declares what it needs: `text` works with every family; `lora`
and `images` only where the family supports them.

### 14.3 Limits

- Higgsfield's camera, lens and lighting controls steer a model trained for
  them. Here a look is words, sometimes backed by a LoRA: it nudges, it does
  not guarantee. The UI says "Look", not "Camera control", unless a control
  LoRA is attached.
- Camera **movement** exists only where a family has it (LTX control LoRAs,
  later Wan/lingbot camera conditioning).
- Element consistency is as good as the model's reference-image support:
  strong on Qwen Image edit and MiniMax H3, prompt-only elsewhere.
