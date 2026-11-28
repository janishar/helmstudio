// Magic's assets and storyboard in the web (plan 39 phase E; contracts/README.md "Magic: assets and the storyboard"):
// the views Make views offers, an Element or takes imported as a copy, a free handle, the @ menu's matches and the
// rough cut's order (the board's frames in time with their lines).
import type { S } from "@/lib/api";
import {
  HANDLE,
  end,
  type Asset,
  type AssetKind,
  type Block,
  type Doc,
  type Ref,
} from "@/lib/magic";
import { handleFrom } from "@/lib/mentions";

export const ASSET_KIND_NAME: Record<AssetKind, string> = {
  character: "Character",
  prop: "Prop",
  place: "Place",
};
/** The views Make views makes of each kind (MagicBoard frame 6's sheet). */
export const VIEWS: Record<AssetKind, string[]> = {
  character: ["Front", "Side", "Back", "Full figure"],
  prop: ["Front", "Side", "Back"],
  place: ["Wide", "Reverse angle"],
};

/** A view's size as studio makes it: 1K, a character 3:4, a prop square, a place the board's shape (studio then fits it
 * to the model's grid and max). */
export function viewSize(kind: AssetKind, t: { width: number; height: number }) {
  const [w, h] = kind === "character" ? [3, 4] : kind === "prop" ? [1, 1] : [t.width, t.height];
  const k = 1024 / Math.max(w, h);
  return { width: Math.round(w * k), height: Math.round(h * k) };
}

/** The views of its kind not on the sheet yet (by name); all of them when every one is; never one still being made
 * (`making`: studio refuses those, 409). */
export function missingViews(a: Asset, making: string[] = []): string[] {
  const have = new Set(Object.values(a.views ?? {}));
  const left = VIEWS[a.kind].filter((v) => !have.has(v));
  return (left.length ? left : VIEWS[a.kind]).filter((v) => !making.includes(v));
}

/** A view's name on the sheet: its own, else "View n". */
export const viewName = (a: Asset, take: string) =>
  a.views?.[take] ?? `View ${a.sheet.indexOf(take) + 1}`;

/** A handle no asset of the board has: the wanted one when it fits the rule, else one from the name ("Red umbrella"
 * → red-umbrella); -2, -3 … when taken. */
export function freeHandle(d: Doc, name: string, want?: string | null): string {
  const base = (want && HANDLE.test(want) ? want : handleFrom(name)) || "asset";
  const taken = new Set(d.assets.map((a) => a.handle.toLowerCase()));
  if (!taken.has(base)) return base;
  for (let n = 2; ; n++) {
    const h = `${base.slice(0, 36).replace(/[-_]+$/, "")}-${n}`;
    if (!taken.has(h)) return h;
  }
}

const GROUP_KIND: Record<string, AssetKind> = {
  character: "character",
  location: "place",
  prop: "prop",
};

/** An Element as a new asset of the board (Import, as a copy): its pictures, voice and words copied, never linked. */
export function fromElement(d: Doc, e: S<"LibraryItem">): Omit<Asset, "id"> {
  return {
    kind: GROUP_KIND[e.group] ?? "character",
    handle: freeHandle(d, e.name, e.handle),
    name: e.name,
    sheet: e.images.map((i) => i.take_id).slice(0, 50),
    ...(e.voice_id ? { voice_id: e.voice_id } : {}),
    ...(e.text ? { note: e.text } : {}),
    imported_from: e.id,
  };
}

/** Gallery takes as a new asset (Import from the Assets row): a character named after the first take. */
export function fromTakes(d: Doc, takes: { id: string; title?: string }[]): Omit<Asset, "id"> {
  const name = takes[0]?.title?.trim().slice(0, 80) || `Asset ${d.assets.length + 1}`;
  return {
    kind: "character",
    handle: freeHandle(d, name),
    name,
    sheet: takes.map((t) => t.id).slice(0, 50),
    imported_from: takes[0]?.id,
  };
}

/** What the @ menu offers for a query: the board's assets (a word of the handle or name), then pictures of other
 * blocks. */
export function atMatches(
  d: Doc,
  query: string,
  self?: string,
): { ref: Ref; label: string; sub: string }[] {
  const q = query.toLowerCase();
  // a word of its handle or name starts with the query ("um" finds @red-umbrella)
  const hit = (...texts: string[]) =>
    !q || texts.some((t) => t.toLowerCase().split(/[\s_-]+/).some((w) => w.startsWith(q)) || t.toLowerCase().startsWith(q));
  const out: { ref: Ref; label: string; sub: string }[] = [];
  for (const a of d.assets)
    if (hit(a.handle, a.name))
      out.push({ ref: { asset: a.id }, label: `@${a.handle}`, sub: `${ASSET_KIND_NAME[a.kind]} · ${a.name}` });
  for (const b of d.blocks) {
    if (b.id === self || !hit(b.name)) continue;
    const name = b.name.split(" · ")[0];
    if (b.kind === "video")
      for (const part of ["last", "first"] as const)
        out.push({ ref: { block: b.id, part }, label: `@${name} · ${part} frame`, sub: "a picture" });
    else if (b.kind === "image" || b.kind === "frame")
      out.push({ ref: { block: b.id, part: "take" }, label: `@${name}`, sub: "its take" });
  }
  return out.slice(0, 12);
}

/** The refs a prompt's @handles name that `refs` lacks (typed in full, not picked from the menu). */
export function mentionedRefs(d: Doc, prompt: string, refs: Ref[] = []): Ref[] {
  const have = new Set(refs.flatMap((r) => ("asset" in r ? [r.asset] : [])));
  const words = new Set(
    [...prompt.matchAll(/(?<![A-Za-z0-9_@])@([A-Za-z][A-Za-z0-9_-]*)/g)].map((m) =>
      m[1].replace(/[-_]+$/, "").toLowerCase(),
    ),
  );
  return d.assets
    .filter((a) => words.has(a.handle.toLowerCase()) && !have.has(a.id))
    .map((a) => ({ asset: a.id }));
}

/** One frame of the rough cut: its time on the board, its line, the voice blocks under it. */
export type CutFrame = { block: Block; line: string; voices: Block[] };

/** The rough cut (MagicBoard frame 7): the board's frames in time order (from `from`), each with its line: its own,
 * else the lines of the voice blocks under it. */
export function roughCut(d: Doc, from?: string): CutFrame[] {
  const lane = new Map(d.lanes.map((l, i) => [l.id, i]));
  const frames = d.blocks
    .filter((b) => b.kind === "frame" && !d.lanes.find((l) => l.id === b.lane)?.hidden)
    .sort((a, b) => a.at - b.at || (lane.get(a.lane) ?? 0) - (lane.get(b.lane) ?? 0));
  const i = Math.max(0, frames.findIndex((f) => f.id === from));
  return frames.slice(i).map((f) => {
    const voices = d.blocks.filter((v) => v.kind === "voice" && v.at < end(f) && end(v) > f.at);
    const line =
      f.request.line?.trim() ||
      voices
        .map((v) => v.request.line?.trim())
        .filter(Boolean)
        .join(" ");
    return { block: f, line, voices };
  });
}
