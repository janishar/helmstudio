// Magic's standard request in the web (plan 39 phase D; contracts/README.md "Magic: the standard request", "Magic:
// runs"): the kinds studio serves (GET /v1/studio/magic/kinds), a model's greyed fields, the pack's estimate before a
// block has its own, and the board's takes and run placeholders made from studio's answer (MagicBoard runs, takes).
import type { S } from "@/lib/api";
import {
  inputsOf,
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
export type Plan = S<"MagicPlan">;
/** a block's estimate as the page shows it */
export type Estimate = { seconds: number | null; host: string | null; source: string };

/** The kinds a request form serves (Add's step 2 and the block dialog). */
export const FORM_KINDS: Kind[] = ["image", "video", "voice"];

export const kindOf = (kinds: Kinds | null, k: Kind) => kinds?.kinds.find((x) => x.kind === k);

/** The model a request runs: its `model`, else the kind's first (studio's default). */
export function modelOf(kinds: Kinds | null, req: MagicRequest): Model | undefined {
  const ms = kindOf(kinds, req.kind)?.models ?? [];
  return ms.find((m) => m.workflow_id === req.model) ?? (req.model ? undefined : ms[0]);
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

/** The pack's estimate of a block on its model (its declared seconds per unit × units × count), null without. */
export function packSeconds(
  m: Model | undefined,
  b: Pick<Block, "kind" | "length" | "request">,
  d: Doc,
) {
  if (!m?.seconds) return null;
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
      };
    if (r.takes.length) continue;
    if (r.status === "queued" || r.status === "running")
      out[`run:${r.run_id}`] = {
        id: `run:${r.run_id}`,
        block: r.block,
        status: "running",
        progress: r.progress ?? 0,
        inputs: "",
      };
    else if (r.status === "failed")
      out[`run:${r.run_id}`] = {
        id: `run:${r.run_id}`,
        block: r.block,
        status: "failed",
        error: r.error ?? "failed",
        inputs: "",
      };
  }
  const owner = new Map<string, string>();
  for (const b of doc.blocks) for (const t of b.takes) owner.set(t, b.id);
  for (const [id, t] of Object.entries(takes)) {
    const thumb = t.thumb_url ?? t.poster_url;
    out[id] = {
      ...(out[id] ?? { id, block: owner.get(id) ?? "", status: "done", inputs: CURRENT }),
      art: thumb ? `url("${thumb}")` : undefined,
      url: t.url,
      kind: t.kind,
      seed: t.seed ?? out[id]?.seed,
    };
  }
  // made from the inputs studio has now: they are the block's inputs as studio's document has them
  for (const t of Object.values(out))
    if (t.inputs === CURRENT) {
      const b = doc.blocks.find((x) => x.id === t.block);
      t.inputs = b ? inputsOf(doc, b, out) : "";
    }
  return out;
}

/** "Run 3 · seed 46 · by Assistant · 1:52 · 10:42": a run history row's parts. */
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
    sub: [seed, r.by === "assistant" ? "by Assistant" : "", took, at].filter(Boolean).join(" · "),
  };
}

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
          fields: ["prompt", "refs", "size", "seed"],
          greyed: {},
          seconds: 6,
          advanced: [],
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
    { kind: "music", fields: [], models: [], off: "Comes with Create's Music and Sound" },
    { kind: "sound", fields: [], models: [], off: "Comes with Create's Music and Sound" },
    { kind: "frame", fields: [], models: [], off: "Board frames come with the storyboard" },
  ],
};
