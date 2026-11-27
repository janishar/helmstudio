// Magic's standard request in the web (plan 39 phase D; contracts/README.md "Magic: the standard request", "Magic:
// runs"): the kinds studio serves (GET /v1/studio/magic/kinds), a model's greyed fields, the pack's estimate before a
// block has its own, and the board's takes and run placeholders made from studio's answer (MagicBoard runs, takes).
import type { S } from "@/lib/api";
import { drivesOf, misfit, type Machine } from "@/lib/run-on";
import { coverFor } from "@/lib/takes";
import {
  inputsOf,
  turnBlock,
  type Block,
  type Doc,
  type Kind,
  type MagicRequest,
  type Takes,
} from "@/lib/magic";

export type Kinds = S<"MagicKinds">;
export type Model = S<"MagicModel">;
export type Field = S<"MagicField">;
export type BlockRun = S<"MagicBlockRun">;
export type AssetRun = S<"MagicAssetRun">;
export type Plan = S<"MagicPlan">;
/** a block's estimate as the page shows it */
export type Estimate = { seconds: number | null; host: string | null; source: string };

/** The kinds a request form serves (Add's step 2 and the block dialog); Music and Sound from API 1.85. */
export const FORM_KINDS: Kind[] = ["image", "video", "voice", "music", "sound"];

/** Music's keys (ACE-Step's, the one model that takes a key); absent is Auto. */
export const KEYS = ["C", "C#", "D", "Eb", "E", "F", "F#", "G", "Ab", "A", "Bb", "B"].flatMap(
  (n) => [`${n} major`, `${n} minor`],
);

export const kindOf = (kinds: Kinds | null, k: Kind) => kinds?.kinds.find((x) => x.kind === k);

/** The model a request runs: its `model`, else the kind's first (studio's default); a sound from a shot's video with
 * no model picked, the first model that takes a video (API 1.85). */
export function modelOf(kinds: Kinds | null, req: MagicRequest): Model | undefined {
  const ms = kindOf(kinds, req.kind)?.models ?? [];
  if (req.model)
    return ms.find((m) => m.workflow_id === req.model || m.video?.workflow_id === req.model);
  return (req.from && ms.find((m) => m.fields.includes("from"))) || ms[0];
}

/** Every Advanced param of a model for a request: its video workflow's when the request is made from a shot's video. */
const allAdvanced = (m: Model | undefined, req: MagicRequest) =>
  (req.from && m?.video ? m.video.advanced : m?.advanced) ?? [];

/** The model variant's param (API 1.94: every standard workflow's model node is `model`). */
export const VARIANT = "model.variant";

/** A model's Advanced for a request, less its variant (the form's Variant beside Model, API 1.94). */
export const advancedOf = (m: Model | undefined, req: MagicRequest) =>
  allAdvanced(m, req).filter((p) => p.path !== VARIANT);

/** The variant param of a model for a request, when it has more than one value. */
export function variantParam(m: Model | undefined, req: MagicRequest) {
  const p = allAdvanced(m, req).find((x) => x.path === VARIANT)?.param;
  return p?.enum && p.enum.length > 1 ? p : undefined;
}

/** The variant a request runs (API 1.94, as studio's): its Advanced value when one of the param's, else the
 * workflow's own; undefined for a model without one. */
export function variantOf(m: Model | undefined, req: MagicRequest): string | undefined {
  const p = allAdvanced(m, req).find((x) => x.path === VARIANT)?.param;
  if (!p) return undefined;
  const v = req.advanced?.[VARIANT];
  return typeof v === "string" && p.enum?.includes(v) ? v : String(p.default ?? "");
}

/** One value of the Variant choice: its title (GET /models's, the id without) and why it cannot run. */
export type VariantChoice = { id: string; title: string; why?: string };

/** The Variant choice of a request (API 1.94; MagicBoard 3 and 7): the param's values with their titles; one that runs
 * only on a device no connected host has (fp8: NVIDIA) left out unless picked; one that cannot run where the request
 * may go greyed with why: its Run on host's reason, under Auto when no live host that runs the model has it. */
export function variantChoices(
  m: Model | undefined,
  req: MagicRequest,
  list: S<"ModelList"> | null,
  mcs: Machine[],
  pinned?: Machine,
): VariantChoice[] {
  const p = variantParam(m, req);
  if (!p) return [];
  const cur = variantOf(m, req);
  const ms = list?.models.filter((x) => x.family === m?.family) ?? [];
  const devices = new Set(mcs.map((x) => x.device));
  const live = mcs.filter((x) => x.live && (!m?.env || x.envs.includes(m.env)));
  return (p.enum ?? []).flatMap((id) => {
    const fm = ms.find((x) => x.variants.some((v) => v.id === id));
    const v = fm?.variants.find((x) => x.id === id);
    if (!fm || !v) return [{ id, title: id }];
    // only a variant that names its own devices (fp8: cuda) is left out: a CPU host (the fake one) has every other
    if (id !== cur && mcs.length && v.devices && !v.devices.some((d) => devices.has(d))) return [];
    const drives = drivesOf(list!, v);
    const miss = (mc: Machine) => misfit(mc, fm, v, drives);
    const off = pinned
      ? miss(pinned)
      : live.length && live.every((mc) => miss(mc))
        ? { device: false, text: "not downloaded" }
        : null;
    return [
      {
        id,
        title: v.title,
        why: off ? (off.device ? off.text : `${v.title} ${off.text}`) : undefined,
      },
    ];
  });
}

/** A field's label in the form. */
export const FIELD_LABEL: Record<Field, string> = {
  prompt: "Prompt",
  line: "Line",
  refs: "Refs",
  first: "First frame",
  last: "Last frame",
  length: "Length",
  size: "Size",
  seed: "Seed",
  voice: "Voice",
  lyrics: "Lyrics",
  key: "Key",
  bpm: "BPM",
  from: "From",
};

/** A block's units of GPU work, as studio counts them (§45.10). */
export function units(b: Pick<Block, "kind" | "length" | "request">, d: Doc): number {
  const w = b.request.width ?? d.target.width;
  const h = b.request.height ?? d.target.height;
  const s = b.length / d.target.fps;
  if (b.kind === "image") return (w * h) / 1e6;
  if (b.kind === "video") return (s * w * h) / (1280 * 720);
  return s;
}

/** The pack's estimate of a block on its model (its declared seconds per unit × units × count), null without; the
 * pack measured its workflow's own variant, so another one is unknown (API 1.94, as studio's plan). */
export function packSeconds(
  m: Model | undefined,
  b: Pick<Block, "kind" | "length" | "request">,
  d: Doc,
) {
  if (!m?.seconds) return null;
  if (variantOf(m, b.request) !== variantOf(m, { ...b.request, advanced: undefined })) return null;
  return m.seconds * units(b, d) * Math.min(4, Math.max(1, b.request.count ?? 1));
}

const CURRENT = "\u0000current";

/** The board's takes as the page draws them, from studio's answer: each run's takes (done; out of date when its run
 * no longer matches the block), and a run with no take yet as `run:<id>` (running, or failed with why). */
export function takesOf(doc: Doc, runs: BlockRun[], takes: Record<string, S<"MagicTake">>): Takes {
  const out: Takes = {};
  for (const r of runs) {
    for (const id of r.takes)
      out[id] = {
        id,
        block: r.block,
        status: "done",
        inputs: r.current ? CURRENT : `old:${r.run_id}`,
        seed: r.seed ?? undefined,
        turn: r.turn ?? undefined,
        variant: r.variant,
      };
    if (r.takes.length) continue;
    if (r.status === "queued" || r.status === "running")
      out[`run:${r.run_id}`] = {
        id: `run:${r.run_id}`,
        block: r.block,
        status: "running",
        progress: r.progress ?? 0,
        inputs: "",
        turn: r.turn ?? undefined,
      };
    else if (r.status === "failed")
      out[`run:${r.run_id}`] = {
        id: `run:${r.run_id}`,
        block: r.block,
        status: "failed",
        error: r.error ?? "failed",
        inputs: "",
        turn: r.turn ?? undefined,
      };
  }
  const owner = new Map<string, string>();
  for (const b of doc.blocks) for (const t of b.takes) owner.set(t, b.id);
  for (const [id, t] of Object.entries(takes)) {
    const thumb = t.kind === "audio" ? undefined : (t.thumb_url ?? t.poster_url);
    const block = out[id]?.block ?? owner.get(id) ?? "";
    const b = doc.blocks.find((x) => x.id === block);
    out[id] = {
      ...(out[id] ?? { id, block, status: "done", inputs: CURRENT }),
      // API 1.85: a music take's album cover, its gradient from the block's style
      art: thumb
        ? `url("${thumb}")`
        : t.sound === "music"
          ? coverFor(b?.request.prompt || b?.name || id)
          : undefined,
      url: t.url,
      kind: t.kind,
      sound: t.sound,
      seed: t.seed ?? out[id]?.seed,
      duration: t.duration_s ?? undefined,
      sections: t.sections,
    };
  }
  // made from the inputs studio has now: they are the block's inputs as studio's document has them (a dialogue
  // turn's: its line block's, API 1.89)
  for (const t of Object.values(out))
    if (t.inputs === CURRENT) {
      const b = doc.blocks.find((x) => x.id === t.block);
      t.inputs = !b
        ? ""
        : t.turn != null
          ? inputsOf(doc, turnBlock(b, t.turn), out)
          : inputsOf(doc, b, out);
    }
  return out;
}

/** What made a take, from its run: its model's name and variant ("LTX 2.5 · Distilled"); "" without a run. */
export function madeBy(kinds: Kinds | null, runs: BlockRun[], take: string): string {
  const r = runs.find((x) => x.takes.includes(take));
  if (!r) return "";
  const m = kinds?.kinds
    .flatMap((k) => k.models)
    .find((x) => x.workflow_id === r.workflow_id || x.video?.workflow_id === r.workflow_id);
  return [m?.name, r.variant].filter(Boolean).join(" · ");
}

/** "Run 3 · Distilled · seed 46 · by Assistant · 1:52 · 10:42": a run history row's parts. */
export function runLine(r: BlockRun, n: number): { title: string; sub: string } {
  const seed = r.seed != null && r.seed >= 0 ? `seed ${r.seed}` : "random seed";
  const took =
    r.render_s != null
      ? `${Math.floor(r.render_s / 60)}:${String(Math.round(r.render_s % 60)).padStart(2, "0")}`
      : r.status === "running" || r.status === "queued"
        ? `now${r.progress != null ? ` · ${r.progress}%` : ""}`
        : r.status;
  const at = new Date(r.created_at).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  return {
    title: `Run ${n}`,
    sub: [
      r.turn != null ? `line ${r.turn + 1}` : "",
      r.variant ?? "",
      seed,
      r.by === "assistant" ? "by Assistant" : "",
      took,
      at,
    ]
      .filter(Boolean)
      .join(" · "),
  };
}

// the fake mode's Qwen Image variants (API 1.94; MagicBoard 7)
const QWEN_VARIANT = {
  path: VARIANT,
  param: { name: "variant", type: "enum" as const, default: "base", enum: ["base", "turbo"] },
};

/** The fake mode's kinds (MagicBoard frame 2's models; nothing runs them but the fake runs). */
export const FAKE_KINDS: Kinds = {
  kinds: [
    {
      kind: "video",
      fields: ["prompt", "first", "last", "length", "size", "seed"],
      models: [
        {
          workflow_id: "ltx-image-to-video",
          name: "LTX 2.5",
          title: "LTX-2.5 Image to video",
          fields: ["prompt", "first", "last", "length", "size", "seed"],
          greyed: {},
          seconds: 14,
          advanced: [],
        },
        {
          workflow_id: "minimax-h3-image-to-video",
          name: "MiniMax H3",
          title: "MiniMax H3 Image to video",
          fields: ["prompt", "first", "length", "size", "seed"],
          greyed: { last: "MiniMax H3 can't take a last frame" },
          seconds: 30,
          advanced: [],
        },
      ],
    },
    {
      kind: "image",
      fields: ["prompt", "refs", "size", "seed"],
      models: [
        {
          workflow_id: "qwen-image",
          name: "Qwen Image 2.1",
          title: "Qwen Image",
          family: "qwen-image",
          fields: ["prompt", "refs", "size", "seed"],
          greyed: {},
          seconds: 6,
          advanced: [QWEN_VARIANT],
        },
      ],
    },
    {
      kind: "voice",
      fields: ["line", "voice", "seed"],
      models: [
        {
          workflow_id: "qwen3-tts-voice-over",
          name: "Qwen3-TTS",
          title: "Qwen3-TTS Voice-over",
          fields: ["line", "voice", "seed"],
          greyed: {},
          seconds: 1.5,
          advanced: [],
        },
      ],
    },
    {
      kind: "music",
      fields: ["prompt", "lyrics", "length", "key", "bpm", "seed"],
      models: [
        {
          workflow_id: "minimax-music3",
          name: "MiniMax Music 3",
          title: "MiniMax Music 3",
          fields: ["prompt", "lyrics", "length", "seed"],
          greyed: {
            key: "MiniMax Music 3 can't take a key",
            bpm: "MiniMax Music 3 can't take a BPM",
          },
          seconds: 4.9,
          advanced: [],
        },
        {
          workflow_id: "ace-step-music",
          name: "ACE-Step 1.5",
          title: "ACE-Step Music",
          fields: ["prompt", "lyrics", "length", "key", "bpm", "seed"],
          greyed: {},
          seconds: 0.55,
          advanced: [],
        },
      ],
    },
    {
      kind: "sound",
      fields: ["from", "prompt", "length", "seed"],
      models: [
        {
          workflow_id: "sa3-sound",
          name: "Stable Audio 3 Small-SFX",
          title: "Stable Audio 3 Sound",
          fields: ["prompt", "length", "seed"],
          greyed: { from: "Stable Audio 3 Small-SFX can't take a shot's video" },
          seconds: 0.1,
          advanced: [],
        },
        {
          workflow_id: "mmaudio-sound",
          name: "MMAudio",
          title: "MMAudio Sound",
          fields: ["from", "prompt", "length", "seed"],
          greyed: {},
          seconds: 0.5,
          advanced: [],
          video: {
            workflow_id: "mmaudio-video",
            title: "MMAudio Sound from a video",
            advanced: [],
          },
        },
      ],
    },
    {
      kind: "frame",
      fields: ["prompt", "refs", "size", "seed"],
      models: [
        {
          workflow_id: "qwen-image",
          name: "Qwen Image 2.1",
          title: "Qwen Image",
          family: "qwen-image",
          fields: ["prompt", "refs", "size", "seed"],
          greyed: {},
          seconds: 6,
          advanced: [QWEN_VARIANT],
        },
      ],
    },
  ],
};
