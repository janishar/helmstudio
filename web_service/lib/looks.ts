// Looks (contracts/README.md "Looks"): the composer's tiles (`studio.quick`'s `elements` / `look:<group>`, or the
// default), the chosen looks per group and the run's `looks` in tile order, the visual picker's filter and reasons,
// and Reuse's word on looks that changed since. The resolver itself is lib/mentions.ts (one resolver).
import type { S } from "@/lib/api";
import { lookFit, promptParams, resolvableLooks } from "@/lib/mentions";

type Item = S<"LibraryItem">;
type Group = S<"LibraryGroup">;
type W = Pick<S<"Workflow">, "nodes" | "node_schemas" | "studio">;
export type Tile = S<"StudioTile">;
/** chosen look ids per look group, in pick order */
export type Chosen = Partial<Record<Group, string[]>>;

/** The look groups in their order, with labels. */
export const LOOK_GROUPS: [Group, string][] = [
  ["film", "Film"],
  ["camera", "Camera"],
  ["colour", "Colour"],
  ["lighting", "Lighting"],
];
export const lookGroupLabel = (g: string) => LOOK_GROUPS.find((x) => x[0] === g)?.[1] ?? g;

/** RunRequest.looks takes at most this many. */
export const MAX_LOOKS = 8;

/** The group of a `look:<group>` tile, else null. */
export const tileGroup = (t: string): Group | null =>
  t.startsWith("look:") && LOOK_GROUPS.some(([g]) => `look:${g}` === t)
    ? (t.slice(5) as Group)
    : null;

/** Why a library item cannot be applied to this graph now, null when it can (README, Looks: fit). `loras`: LoRA id →
 * detected family of the caller's usable LoRAs. */
export function reasonOf(
  w: Pick<W, "nodes" | "node_schemas">,
  item: Item,
  loras: Record<string, string>,
) {
  if (item.kind !== "look")
    return lookFit(w.nodes, w.node_schemas, { kind: "element", text: "", images: [] }, loras);
  return lookFit(w.nodes, w.node_schemas, resolvableLooks([item])[0], loras);
}

/** The composer's tiles: exactly `studio.quick`'s tiles when the workflow has a Studio block; else Elements (with a
 * prompt) and each look group with at least one item that fits, plus any group that has chosen looks. */
export function tilesOf(
  w: W,
  looks: Item[],
  loras: Record<string, string>,
  chosen: Chosen = {},
): Tile[] {
  if (w.studio)
    return (w.studio.quick ?? []).filter(
      (t): t is Tile => t === "elements" || tileGroup(t) !== null,
    );
  const prompt = promptParams(w.nodes, w.node_schemas).length > 0;
  return [
    ...(prompt ? (["elements"] as Tile[]) : []),
    ...LOOK_GROUPS.filter(
      ([g]) =>
        chosen[g]?.length ||
        looks.some((x) => x.kind === "look" && x.group === g && !reasonOf(w, x, loras)),
    ).map(([g]) => `look:${g}` as Tile),
  ];
}

/** The chosen looks still listed (a look deleted or on no live host since is dropped); `looks` null (loading):
 * unchanged. */
export function liveChosen(chosen: Chosen, looks: Item[] | null): Chosen {
  if (!looks) return chosen;
  const ids = new Set(looks.map((x) => x.id));
  return Object.fromEntries(
    Object.entries(chosen).map(([g, list]) => [g, (list ?? []).filter((id) => ids.has(id))]),
  );
}

/** RunRequest.looks: the chosen looks of the tiles' groups, in tile order then pick order, each once. */
export const chosenIds = (tiles: Tile[], chosen: Chosen) => [
  ...new Set(tiles.flatMap((t) => chosen[tileGroup(t) as Group] ?? [])),
];

/** What a look tile shows: the first chosen look's thumbnail and name ("Window +1"), or the group's word for none. */
export function tileState(group: Group, chosen: Chosen, looks: Item[] | null) {
  const ids = chosen[group] ?? [];
  const first = looks?.find((x) => x.id === ids[0]);
  if (!ids.length) return { thumb: null, name: "Auto", count: 0 };
  return {
    thumb: first?.thumb_url ?? null,
    name: `${first?.name ?? "a look"}${ids.length > 1 ? ` +${ids.length - 1}` : ""}`,
    count: ids.length,
  };
}

/** The chosen looks per group from a list of ids (Reuse): each id under its look's group, in order; ids no listed
 * look has are left out. */
export function chosenFrom(ids: string[], looks: Item[]): Chosen {
  const out: Chosen = {};
  for (const id of ids) {
    const x = looks.find((l) => l.id === id && l.kind === "look");
    if (x && !out[x.group]?.includes(id)) out[x.group] = [...(out[x.group] ?? []), id];
  }
  return out;
}

/** The picker's filter: one group ("" = all), one source ("" = both), every search word in the name (or the handle,
 * for elements: a leading @ is dropped). */
export function filterItems(
  items: Item[],
  {
    group = "",
    source = "",
    q = "",
  }: { group?: Group | ""; source?: S<"LibrarySource"> | ""; q?: string },
) {
  const words = q.trim().replace(/^@/, "").toLowerCase().split(/\s+/).filter(Boolean);
  return items.filter(
    (x) =>
      (!group || x.group === group) &&
      (!source || (x.source ?? "mine") === source) &&
      words.every((w) => x.name.toLowerCase().includes(w) || x.handle?.includes(w)),
  );
}

/** A look card's line: what it brings ("text + LoRA"), and "starter" for a pack's. */
export function lookLine(x: Item) {
  const n = x.needs ?? [];
  const what =
    [n.includes("text") && "text", n.includes("images") && "images", n.includes("lora") && "LoRA"]
      .filter(Boolean)
      .join(" + ") || "nothing";
  return x.source === "starter" ? `${what} · starter` : what;
}

/** Reuse's word on the take's looks: which changed, were deleted, or are on no live host now ("" when all are
 * current). */
export function looksReuseNote(st: S<"LookStatus">[], made: S<"MadeWithLook">[]) {
  const name = (id: string) => made.find((m) => m.id === id)?.name ?? "a look";
  const said = st.flatMap((s) =>
    s.status === "changed"
      ? [`look ${name(s.id)} was edited: a new run uses it as it is now`]
      : s.status === "deleted"
        ? [`look ${name(s.id)} was deleted, so it is left out`]
        : s.status === "missing"
          ? [`look ${name(s.id)} is on no live host now, so it is left out`]
          : [],
  );
  return said.length ? `Since this take: ${said.join("; ")}.` : "";
}
