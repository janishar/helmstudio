// Image inputs (contracts/README.md "Image inputs"): an `image` param holds one take, a `rows` param whose item has an
// Image field holds an ordered list of them (the references). Over HTTP an image is always `{take_id}`.
import type { S } from "@/lib/api";
import { roundEven } from "@/lib/video";
import { isRef, toRaw, type Field, type Raw } from "@/lib/workflow";

type Param = S<"ParamSchema">;

/** A take dragged inside the app (from the takes strip) carries its id as this type. */
export const TAKE_MIME = "application/x-helm-take";

/** The Image field of a rows param's item ("image"), if it has one. */
export const imageFieldOf = (p: Param) =>
  p.type === "rows" ? Object.keys(p.item ?? {}).find((k) => p.item?.[k] === "Image") : undefined;

/** The fields that take images: `image` params and rows params with an Image item field. */
export const imageInputs = (fs: Field[]) =>
  fs.filter((f) => f.param.type === "image" || !!imageFieldOf(f.param));

/** How many images a field can hold. */
export const capacity = (f: Field) => (f.param.type === "image" ? 1 : (f.param.max ?? 100));

/** The take ids a control holds, in order. */
export function refIds(f: Field, raw: Raw | undefined): string[] {
  const r = raw ?? toRaw(f.initial, f.param);
  if (f.param.type === "image") return typeof r === "string" && r ? [r] : [];
  const k = imageFieldOf(f.param);
  if (!k || !Array.isArray(r)) return [];
  return r.flatMap((it) => (isRef(it[k]) ? [it[k].take_id] : []));
}

/** The control's content holding these take ids; a rows item already there keeps its other fields. */
export function withIds(f: Field, raw: Raw | undefined, ids: string[]): Raw {
  if (f.param.type === "image") return ids[0] ?? "";
  const k = imageFieldOf(f.param) ?? "image";
  const r = raw ?? toRaw(f.initial, f.param);
  const items = Array.isArray(r) ? r : [];
  return ids.map(
    (id) => items.find((it) => isRef(it[k]) && it[k].take_id === id) ?? { [k]: { take_id: id } },
  );
}

/** Ids added at the end: each once, no more than `max` in all. */
export const addIds = (ids: string[], more: string[], max: number) =>
  [...new Set([...ids, ...more])].slice(0, Math.max(max, ids.length));

/** The list with item `from` moved to index `to` (clamped). */
/** Calls done (or failed) for each job in the jobs' order, whatever order they settle in: files dropped together
 * become references in drop order, not in the order their uploads finish. */
export function inOrder<T>(
  jobs: Promise<T>[],
  done: (t: T, i: number) => void,
  failed: (e: unknown, i: number) => void,
) {
  return jobs.reduce<Promise<void>>((prev, job, i) => {
    const settled = job.then(
      (t) => () => done(t, i),
      (e) => () => failed(e, i),
    );
    return prev.then(() => settled).then((call) => call());
  }, Promise.resolve());
}

export function move<T>(list: T[], from: number, to: number): T[] {
  const t = Math.max(0, Math.min(list.length - 1, to));
  if (from === t || from < 0 || from >= list.length) return list;
  const out = list.slice();
  const [x] = out.splice(from, 1);
  out.splice(t, 0, x);
  return out;
}

/** How the prompt names item n (from 1): the param's `mention` ("<image{n}>" → "<image2>"), else "image 2". */
export const mentionOf = (p: Param, n: number) =>
  p.mention ? p.mention.replaceAll("{n}", String(n)) : `image ${n}`;

/** Canvas `auto` in an edit: about 1 MP (or `area`) at the reference's aspect, sides on the 32 px grid (the diffusers
 * pipeline's calculate_dimensions(1024², w / h)). */
export function autoSize(width: number, height: number, area = 1024 * 1024) {
  const r = width / height;
  const w = Math.sqrt(area * r);
  return { width: roundEven(w / 32) * 32, height: roundEven(w / r / 32) * 32 };
}

/** Canvas kinds whose `auto` follows the FIRST reference, clamped to 1:4…4:1, at the canvas's own width × height
 * (contracts/README.md, the minimax-h3-image family); the core `canvas` follows the last one at 1 MP.
 * ponytail: by kind, as the contract states it; a schema hint on the canvas would make this generic. */
export const FIRST_REF_CANVAS = new Set(["minimax-h3-image/canvas"]);

/** The runner's minimax size_for: `area` at `ratio`, worked out on the long side (3:4 is 4:3 turned), the short
 * side from the rounded long side, both on the 32 px grid. */
function sizeFor(ratio: number, area: number): { width: number; height: number } {
  if (ratio < 1) {
    const s = sizeFor(1 / ratio, area);
    return { width: s.height, height: s.width };
  }
  const width = roundEven(Math.sqrt(area * ratio) / 32) * 32;
  return { width, height: roundEven(width / ratio / 32) * 32 };
}

/** What canvas auto gives with a reference of this size: 1 MP at its aspect, or for a FIRST_REF_CANVAS its aspect
 * clamped to 1:4…4:1 at the canvas's own width × height (as the runner's size_for). */
export const autoCanvas = (
  ref: { width: number; height: number },
  w: number,
  h: number,
  first: boolean,
) =>
  first
    ? sizeFor(Math.min(4, Math.max(1 / 4, ref.width / ref.height)), w * h)
    : autoSize(ref.width, ref.height);

/** The top bar's workflow label: "Qwen Image · Edit · 2 references", or the title alone without references. */
export const editLabel = (title: string, n: number) =>
  n ? `${title} · Edit · ${n} reference${n === 1 ? "" : "s"}` : title;

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

/** A batch cell (a take id) as an image field's value: `{take_id}`, or the one-item list for a rows param. */
export function cellRef(f: Field, text: string): { value: S<"ParamValue"> } | { error: string } {
  if (!UUID.test(text)) return { error: "must be a take id" };
  const ref = { take_id: text.toLowerCase() };
  return { value: f.param.type === "image" ? ref : [{ [imageFieldOf(f.param) ?? "image"]: ref }] };
}

/** What was dropped: image files, and takes dragged from inside the app. */
export function dropped(dt: DataTransfer) {
  const take = dt.getData(TAKE_MIME);
  return {
    files: Array.from(dt.files).filter((f) => f.type.startsWith("image/")),
    takeIds: take ? [take] : [],
  };
}

/** Whether a drag carries something a reference list takes. */
export const carriesImage = (dt: DataTransfer) =>
  dt.types.includes("Files") || dt.types.includes(TAKE_MIME);

/** How many references the form holds now, over every image input. */
export const refCount = (fs: Field[], raws: Record<string, Raw>) =>
  imageInputs(fs).reduce((n, f) => n + refIds(f, raws[f.key]).length, 0);
