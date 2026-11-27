// Magic's board (plan 39 §45.6; contracts/README.md "Magic"): the document one board is (openapi.yaml MagicDoc), and
// the named ops that edit it (MagicOp). Pure: no React, no storage, no runtime imports (contracts/replay_magic.mjs runs
// it under node). Studio applies the same ops (studio_service/api/magic/ops.go); contracts/fixtures/magic/edits.json
// keeps the two the same. Times are frames at the board's fps.

export type Kind = "frame" | "image" | "video" | "voice" | "music" | "sound";
export type Role = "storyboard" | "shots" | "voice" | "music" | "sound" | "free";
export type TransitionKind = "cut" | "dissolve" | "fade" | "wipe" | "morph";

export type Target = { width: number; height: number; fps: number; sample_rate: number };
/** A lane; its place in `lanes` is its order. `kind`: the kind Add picks first (any kind goes on any lane). */
export type Lane = { id: string; name: string; role: Role; hidden?: boolean; kind?: Kind };
/** An @ pick inside a request: an asset, or another block's first frame, last frame or take. */
export type Ref = { asset: string } | { block: string; part: "first" | "last" | "take" };
/** The standard generation request (API 1.81, contracts/README.md "Magic: the standard request"): the same shared
 * fields for every model of a kind; `model` is a workflow id from GET /magic/kinds; Advanced is the model's own params. */
export type MagicRequest = {
  kind: Kind;
  model?: string;
  /** a shot's first and last frame, a still's size, the seed and how many runs one Generate makes */
  first?: Ref;
  last?: Ref;
  width?: number;
  height?: number;
  seed?: number;
  count?: number;
  /** a saved voice when `voice` names no asset with one */
  voice_id?: string;
  prompt?: string;
  /** a voice block's line; a dialogue's turns (owner: Dialogue is a Voice block with turns) */
  line?: string;
  turns?: { speaker: string; text: string }[];
  /** a voice block's voice (an asset id), a frame's style */
  voice?: string;
  style?: "sketch" | "rough" | "full";
  /** a sound made from a shot's video */
  from?: string;
  /** a storyboard frame's shot size, angle and move (API 1.83), and the model of the shot made from it */
  framing?: Framing;
  shot_model?: string;
  refs?: Ref[];
  advanced?: Record<string, unknown>;
};
export type Framing = {
  size?: "wide" | "medium" | "close" | "over_shoulder";
  angle?: "eye" | "low" | "high";
  move?: "still" | "push_in" | "pan" | "follow";
};
/** An effect badge; baked when sent. */
export type Effect = { name: string; params?: Record<string, number | string>; off?: boolean };
export type Block = {
  id: string;
  lane: string;
  at: number;
  length: number;
  kind: Kind;
  name: string;
  request: MagicRequest;
  /** take ids from its runs, oldest first */
  takes: string[];
  pinned?: string;
  note?: string;
  effects: Effect[];
  /** a shot made from a storyboard frame */
  from_frame?: string;
};
/** A voice or music block tied to a shot, or to a run of shots (music stretches with the run). */
export type Link = { block: string; to: string[] };
export type Transition = {
  id: string;
  between?: [string, string];
  start?: string;
  end?: string;
  kind: TransitionKind;
  length: number;
  /** a morph's own (API 1.82): its video request, the takes its runs made and the one it shows */
  request?: MagicRequest;
  takes?: string[];
  pinned?: string;
};
export type Asset = {
  id: string;
  kind: "character" | "prop" | "place";
  handle: string;
  name: string;
  sheet: string[];
  voice_id?: string;
  note?: string;
  imported_from?: string;
  /** names of sheet takes ("Front", "Side"), by take id */
  views?: Record<string, string>;
};
export type AssetKind = Asset["kind"];
export type Doc = {
  target: Target;
  lanes: Lane[];
  blocks: Block[];
  links: Link[];
  transitions: Transition[];
  assets: Asset[];
  notes: string[];
};

/** Insert pushes what is after the point along (and a move closes the gap it leaves); Overwrite covers it. */
export type Mode = "insert" | "overwrite";

export type Op =
  | { op: "add"; block: Omit<Block, "id"> & { id?: string }; mode: Mode }
  | { op: "move"; ids: string[]; dt: number; lane?: string; mode: Mode }
  | { op: "trim"; id: string; at?: number; length?: number }
  | { op: "delete"; ids: string[]; mode: Mode }
  | { op: "duplicate"; ids: string[] }
  | { op: "split"; id: string; at: number }
  | { op: "link"; block: string; to: string[] }
  | { op: "unlink"; block: string }
  | {
      op: "transition";
      between?: [string, string];
      start?: string;
      end?: string;
      kind: TransitionKind;
      length?: number;
      /** a morph's request (API 1.82; absent: kept) and its pin (null unpins) */
      request?: MagicRequest;
      pinned?: string | null;
    }
  | { op: "effect"; ids: string[]; effect: Effect }
  | { op: "effect_set"; id: string; index: number; effect: Effect | null }
  | {
      op: "block";
      id: string;
      /** pinned null unpins (an absent key is no change once sent as JSON) */
      set: Partial<Pick<Block, "name" | "note" | "request">> & { pinned?: string | null };
    }
  | { op: "lane_add"; lane: Omit<Lane, "id"> & { id?: string }; after?: string }
  | { op: "lane"; id: string; set: Partial<Omit<Lane, "id">> }
  | { op: "lane_move"; id: string; by: -1 | 1 }
  | { op: "lane_delete"; id: string }
  | { op: "asset_delete"; id: string }
  | { op: "asset_add"; asset: Omit<Asset, "id" | "sheet"> & { id?: string; sheet?: string[] } }
  | {
      op: "asset";
      id: string;
      /** voice_id null takes it off */
      set: Partial<Pick<Asset, "kind" | "name" | "note" | "sheet" | "views">> & {
        voice_id?: string | null;
      };
    }
  /** a block's own picture (an upload, a Gallery take): added to its takes and pinned */
  | { op: "take_add"; id: string; take: string };

export type Applied = { doc: Doc } | { error: string };

export const end = (b: Block) => b.at + b.length;
const clone = (d: Doc): Doc => structuredClone(d);
/** Every block, lane and transition id (one namespace). */
const ids = (d: Doc) => [
  ...d.blocks.map((b) => b.id),
  ...d.lanes.map((l) => l.id),
  ...d.transitions.map((t) => t.id),
];

/** A fresh id with this prefix, not used in the document nor in `taken` (blocks an op holds off the document). */
export function freshId(d: Doc, prefix: string, taken: string[] = []): string {
  const used = new Set([...ids(d), ...taken]);
  let n = 1;
  while (used.has(`${prefix}${n}`)) n++;
  return `${prefix}${n}`;
}

/** The lane's blocks in time. */
export const inLane = (d: Doc, lane: string) =>
  d.blocks.filter((b) => b.lane === lane).sort((a, b) => a.at - b.at);

/** Frames → "8 s", "6.5 s". */
export const secs = (frames: number, fps: number) => `${+(frames / fps).toFixed(1)} s`;

/** Frames → "0:10.5" (one decimal) or "0:24" (whole: `whole`). */
export function clock(frames: number, fps: number, whole = false): string {
  const s = Math.max(0, frames / fps);
  const m = Math.floor(s / 60);
  const r = s - m * 60;
  return `${m}:${whole ? String(Math.floor(r)).padStart(2, "0") : r.toFixed(1).padStart(4, "0")}`;
}

// ------------------------------------------------------------------------------------------------ placing blocks

/** Removes `ids` from the document; Insert closes each gap on its lane. Returns, per lane, the map from an old time
 * to its time after the gaps closed (identity for Overwrite). */
function lift(d: Doc, ids: Set<string>, mode: Mode): Record<string, (t: number) => number> {
  const gone = d.blocks.filter((b) => ids.has(b.id));
  d.blocks = d.blocks.filter((b) => !ids.has(b.id));
  const maps: Record<string, (t: number) => number> = {};
  if (mode === "overwrite") return maps;
  for (const lane of new Set(gone.map((b) => b.lane))) {
    const holes = gone.filter((b) => b.lane === lane);
    // a time inside a hole goes to the hole's start; past the lane's last block, a drop point stays where it is (a
    // move into empty space after everything is not pulled back by the gap it leaves)
    const last = Math.max(0, ...d.blocks.filter((b) => b.lane === lane).map(end));
    const close = (t: number) =>
      t - holes.reduce((n, h) => n + (end(h) <= t ? h.length : h.at < t ? t - h.at : 0), 0);
    maps[lane] = (t) => (t > last ? t : close(t));
    for (const b of d.blocks) if (b.lane === lane) b.at = close(b.at);
  }
  return maps;
}

/** Makes room for [at, at + len) on `lane`: Insert pushes what starts there or later along (a block the point falls
 * inside moves the point to its end); Overwrite cuts the span out (covered blocks go, others are trimmed or split).
 * Returns where the group starts. */
function room(d: Doc, lane: string, at: number, len: number, mode: Mode, taken: string[]): number {
  const here = d.blocks.filter((b) => b.lane === lane);
  if (mode === "insert") {
    // ponytail: a point inside a block goes to its end; splitting it to insert there is the Overwrite tool's job
    const inside = here.find((b) => b.at < at && at < end(b));
    const start = inside ? end(inside) : at;
    for (const b of here) if (b.at >= start) b.at += len;
    return start;
  }
  const stop = at + len;
  for (const b of here) {
    if (b.at >= stop || end(b) <= at) continue;
    if (b.at >= at && end(b) <= stop) d.blocks = d.blocks.filter((x) => x !== b);
    else if (b.at < at && end(b) > stop) {
      // split: the part after the span is a new block with the same request and takes
      const rest = {
        ...structuredClone(b),
        id: freshId(d, "b", taken),
        at: stop,
        length: end(b) - stop,
      };
      b.length = at - b.at;
      d.blocks.push(rest);
    } else if (b.at < at) b.length = at - b.at;
    else {
      b.length = end(b) - stop;
      b.at = stop;
    }
  }
  return at;
}

/** Puts `group` (blocks already off the document) on `lane` from `at`, keeping their spacing. */
function place(d: Doc, group: Block[], lane: string, at: number, mode: Mode) {
  const lo = Math.min(...group.map((b) => b.at));
  const hi = Math.max(...group.map(end));
  const start = Math.max(0, at);
  const taken = group.map((b) => b.id);
  let s = start;
  if (mode === "insert") s = room(d, lane, start, hi - lo, mode, taken);
  else for (const b of group) room(d, lane, start + b.at - lo, b.length, mode, taken);
  for (const b of group) d.blocks.push({ ...b, lane, at: s + b.at - lo });
}

// ------------------------------------------------------------------------------------------------ ties and joins

/** Moves each tied block with its shot: by the shot's own move (a run of shots: the first one's), or fitted to the
 * run when it was fitted before. `before`: the blocks as they were; `moved`: blocks the op moved itself. */
function follow(d: Doc, before: Map<string, Block>, moved: Set<string>) {
  const now = new Map(d.blocks.map((b) => [b.id, b]));
  for (const l of d.links) {
    const b = now.get(l.block);
    if (!b || moved.has(b.id)) continue;
    const was = l.to.map((id) => before.get(id));
    const is = l.to.map((id) => now.get(id));
    if (was.some((x) => !x) || is.some((x) => !x)) continue;
    const span = (xs: (Block | undefined)[]) => [
      Math.min(...xs.map((x) => x!.at)),
      Math.max(...xs.map((x) => end(x!))),
    ];
    const [a0, e0] = span(was);
    const [a1, e1] = span(is);
    // its shots stayed put: leave it where the op put it (Insert on its own lane may have pushed it along)
    if (a0 === a1 && e0 === e1 && was.every((x, i) => x!.at === is[i]!.at)) continue;
    const old = before.get(b.id) ?? b;
    if (l.to.length > 1 && old.at === a0 && end(old) === e0) {
      b.at = a1;
      b.length = e1 - a1;
    } else b.at = old.at + (is[0]!.at - was[0]!.at);
  }
}

/** Drops what points at blocks or lanes that are gone, and joins that no longer touch; blocks in lane, time order. */
function tidy(d: Doc): Doc {
  const ids = new Set(d.blocks.map((b) => b.id));
  const lanes = new Set(d.lanes.map((l) => l.id));
  d.links = d.links.filter((l) => ids.has(l.block) && l.to.every((t) => ids.has(t)));
  const byId = new Map(d.blocks.map((b) => [b.id, b]));
  d.transitions = d.transitions.filter((t) => {
    if (t.between) {
      const [a, b] = t.between.map((x) => byId.get(x));
      return !!a && !!b && a.lane === b.lane && end(a) === b.at;
    }
    return lanes.has(t.start ?? t.end ?? "");
  });
  const order = new Map(d.lanes.map((l, i) => [l.id, i]));
  d.blocks.sort((a, b) => order.get(a.lane)! - order.get(b.lane)! || a.at - b.at);
  return d;
}

// ------------------------------------------------------------------------------------------------ the ops

/** Applies one op, all or nothing (as studio will): the new document, or why it can't. */
/** A morph's request, takes and pin (API 1.82): kept from `was` while it stays a morph, then the op's request and
 * pinned; another kind takes neither (studio_service/api/magic morphSet). */
function morphSet(
  t: Transition,
  was: Transition | undefined,
  op: { request?: MagicRequest; pinned?: string | null },
): string {
  if (t.kind !== "morph")
    return op.request !== undefined || op.pinned !== undefined ? "Only a morph has a request." : "";
  if (was?.kind === "morph") {
    if (was.request) t.request = was.request;
    if (was.takes?.length) t.takes = was.takes;
    if (was.pinned) t.pinned = was.pinned;
  }
  if (op.request !== undefined) {
    if (op.request?.kind !== "video") return "A morph's request is a video's.";
    t.request = op.request;
  }
  if (op.pinned !== undefined) {
    if (op.pinned !== null && !(t.takes ?? []).includes(op.pinned)) return "That take isn't the morph's.";
    if (op.pinned === null) delete t.pinned;
    else t.pinned = op.pinned;
  }
  return "";
}

export function apply(doc: Doc, op: Op): Applied {
  const d = clone(doc);
  const before = new Map(doc.blocks.map((b) => [b.id, b]));
  const get = (id: string) => d.blocks.find((b) => b.id === id);
  const lane = (id: string) => d.lanes.find((l) => l.id === id);
  switch (op.op) {
    case "add": {
      if (!lane(op.block.lane)) return { error: "That lane is gone." };
      if (op.block.length < 1) return { error: "A block needs a length." };
      if (op.block.id && ids(d).includes(op.block.id)) return { error: "That id is taken." };
      const b = { ...structuredClone(op.block), id: op.block.id ?? freshId(d, "b") } as Block;
      place(d, [b], b.lane, b.at, op.mode);
      follow(d, before, new Set([b.id]));
      return { doc: tidy(d) };
    }
    case "move": {
      const group = op.ids.map(get).filter((b): b is Block => !!b);
      if (!group.length) return { error: "Nothing to move." };
      const from = new Set(group.map((b) => b.lane));
      if (op.lane && !lane(op.lane)) return { error: "That lane is gone." };
      if (op.lane && from.size > 1 && [...from].some((l) => l !== op.lane))
        return { error: "Move blocks from one lane at a time." };
      const copies = group.map((b) => structuredClone(b));
      const maps = lift(d, new Set(op.ids), op.mode);
      // per target lane, the group's blocks on it; a drop point counts in the lane as it looked while dragging
      for (const l of op.lane ? [op.lane] : [...from]) {
        const part = copies.filter((b) => op.lane || b.lane === l);
        const at = Math.max(0, Math.min(...part.map((b) => b.at)) + op.dt);
        place(d, part, l, (maps[l] ?? ((t: number) => t))(at), op.mode);
      }
      follow(d, before, new Set(op.ids));
      return { doc: tidy(d) };
    }
    case "trim": {
      const b = get(op.id);
      if (!b) return { error: "That block is gone." };
      const at = op.at ?? b.at;
      const length = op.length ?? end(b) - at;
      if (at < 0 || length < 1) return { error: "A block needs a length." };
      const others = d.blocks.filter((x) => x.lane === b.lane && x !== b);
      if (others.some((x) => x.at < at + length && end(x) > at))
        return { error: "It would cover the block next to it." };
      b.at = at;
      b.length = length;
      follow(d, before, new Set([b.id]));
      return { doc: tidy(d) };
    }
    case "delete": {
      if (!op.ids.some(get)) return { error: "Nothing to delete." };
      lift(d, new Set(op.ids), op.mode);
      follow(d, before, new Set());
      return { doc: tidy(d) };
    }
    case "duplicate": {
      const group = op.ids.map(get).filter((b): b is Block => !!b);
      if (!group.length) return { error: "Nothing to duplicate." };
      for (const l of new Set(group.map((b) => b.lane))) {
        const part = group.filter((b) => b.lane === l);
        const copies: Block[] = [];
        for (const b of part) {
          // fresh one by one: each copy's id is taken before the next is made
          const c = { ...structuredClone(b), id: freshId(d, "b"), name: `${b.name} copy` };
          delete c.pinned;
          d.blocks.push(c);
          copies.push(c);
        }
        d.blocks = d.blocks.filter((b) => !copies.includes(b));
        place(d, copies, l, Math.max(...part.map(end)), "insert");
      }
      follow(d, before, new Set());
      return { doc: tidy(d) };
    }
    case "split": {
      const b = get(op.id);
      if (!b || !(b.at < op.at && op.at < end(b))) return { error: "The playhead isn't on it." };
      const rest: Block = {
        ...structuredClone(b),
        id: freshId(d, "b"),
        at: op.at,
        length: end(b) - op.at,
      };
      b.length = op.at - b.at;
      d.blocks.push(rest);
      // the join after it now follows the second part
      for (const t of d.transitions)
        if (t.between?.[0] === b.id) t.between = [rest.id, t.between[1]];
      return { doc: tidy(d) };
    }
    case "link": {
      const b = get(op.block);
      if (!b || !op.to.length || op.to.some((t) => !get(t) || t === op.block))
        return { error: "Tie it to a shot." };
      d.links = [...d.links.filter((l) => l.block !== op.block), { block: op.block, to: op.to }];
      return { doc: tidy(d) };
    }
    case "unlink":
      d.links = d.links.filter((l) => l.block !== op.block);
      return { doc: d };
    case "transition": {
      if ([op.between, op.start, op.end].filter((x) => x !== undefined).length !== 1)
        return { error: "Pick a join." };
      const same = (t: Transition) =>
        op.between
          ? t.between?.join() === op.between.join()
          : op.start
            ? t.start === op.start
            : t.end === op.end;
      const was = d.transitions.find(same);
      d.transitions = d.transitions.filter((t) => !same(t));
      if (op.between) {
        const [a, b] = op.between.map(get);
        if (!a || !b || a.lane !== b.lane || end(a) !== b.at)
          return { error: "Those blocks don't meet." };
      }
      // a cut between two blocks is no transition; at a lane's start or end, a cut is none either
      if (op.kind !== "cut") {
        const t: Transition = {
          id: was?.id ?? freshId(d, "t"),
          ...(op.between
            ? { between: op.between }
            : op.start
              ? { start: op.start }
              : { end: op.end }),
          kind: op.kind,
          length: op.length ?? was?.length ?? Math.round(DEFAULT_LENGTH[op.kind] * d.target.fps),
        };
        const why = morphSet(t, was, op);
        if (why) return { error: why };
        d.transitions.push(t);
      }
      return { doc: tidy(d) };
    }
    case "effect": {
      for (const id of op.ids) get(id)?.effects.push({ ...op.effect });
      return { doc: d };
    }
    case "effect_set": {
      const b = get(op.id);
      if (!b?.effects[op.index]) return { error: "That effect is gone." };
      if (op.effect) b.effects[op.index] = op.effect;
      else b.effects.splice(op.index, 1);
      return { doc: d };
    }
    case "block": {
      const b = get(op.id);
      if (!b) return { error: "That block is gone." };
      Object.assign(b, structuredClone(op.set));
      if ("pinned" in op.set && !op.set.pinned) delete b.pinned;
      return { doc: d };
    }
    case "lane_add": {
      if (op.lane.id && ids(d).includes(op.lane.id)) return { error: "That id is taken." };
      const l = { ...op.lane, id: op.lane.id ?? freshId(d, "l") } as Lane;
      const i = op.after ? d.lanes.findIndex((x) => x.id === op.after) + 1 : d.lanes.length;
      d.lanes.splice(i || d.lanes.length, 0, l);
      return { doc: d };
    }
    case "lane": {
      const l = lane(op.id);
      if (!l) return { error: "That lane is gone." };
      if (op.set.name !== undefined && !op.set.name.trim())
        return { error: "Give the lane a name." };
      Object.assign(l, op.set);
      return { doc: d };
    }
    case "lane_move": {
      const i = d.lanes.findIndex((l) => l.id === op.id);
      const j = i + op.by;
      if (i < 0 || j < 0 || j >= d.lanes.length) return { error: "It can't go further." };
      [d.lanes[i], d.lanes[j]] = [d.lanes[j], d.lanes[i]];
      return { doc: tidy(d) };
    }
    case "lane_delete": {
      if (!lane(op.id)) return { error: "That lane is gone." };
      d.lanes = d.lanes.filter((l) => l.id !== op.id);
      d.blocks = d.blocks.filter((b) => b.lane !== op.id);
      return { doc: tidy(d) };
    }
    case "asset_delete": {
      const gone = d.assets.find((a) => a.id === op.id);
      d.assets = d.assets.filter((a) => a.id !== op.id);
      const picks = (r?: Ref) => !!r && "asset" in r && r.asset === op.id;
      // its @ picks go (refs, the first and last frame; a voice stays as it is); its mentions in the prompt become its name
      for (const b of d.blocks) {
        const q = b.request;
        if (q.refs) q.refs = q.refs.filter((r) => !picks(r));
        if (picks(q.first)) delete q.first;
        if (picks(q.last)) delete q.last;
        if (gone && q.prompt !== undefined) q.prompt = replaceMention(q.prompt, gone.handle, gone.name);
      }
      return { doc: d };
    }
    case "asset_add": {
      const n = op.asset;
      if (n.id && d.assets.some((a) => a.id === n.id)) return { error: "That id is taken." };
      if (!ASSET_KINDS.includes(n.kind)) return { error: "kind must be character, prop or place" };
      if (!n.name.trim()) return { error: "Give it a name." };
      if (!HANDLE.test(n.handle))
        return { error: "A handle starts with a letter: letters, digits, - or _." };
      if (d.assets.some((a) => a.handle.toLowerCase() === n.handle.toLowerCase()))
        return { error: "That handle is taken." };
      const a = structuredClone({ ...n, id: n.id ?? freshAssetId(d) }) as Asset;
      a.sheet = unique(a.sheet ?? []);
      keepViews(a);
      d.assets.push(a);
      return { doc: d };
    }
    case "asset": {
      const a = d.assets.find((x) => x.id === op.id);
      if (!a) return { error: "That asset is gone." };
      const s = structuredClone(op.set);
      if (s.kind !== undefined && !ASSET_KINDS.includes(s.kind))
        return { error: "kind must be character, prop or place" };
      if (s.name !== undefined && !s.name.trim()) return { error: "Give it a name." };
      if (s.kind !== undefined) a.kind = s.kind;
      if (s.name !== undefined) a.name = s.name;
      if (s.note !== undefined) a.note = s.note;
      if ("voice_id" in s) {
        if (s.voice_id) a.voice_id = s.voice_id;
        else delete a.voice_id;
      }
      if (s.sheet !== undefined) a.sheet = unique(s.sheet);
      if (s.views !== undefined) a.views = s.views;
      keepViews(a);
      return { doc: d };
    }
    case "take_add": {
      const b = get(op.id);
      if (!b) return { error: "That block is gone." };
      if (!b.takes.includes(op.take)) {
        if (b.takes.length >= 200) return { error: "A block keeps at most 200 takes." };
        b.takes.push(op.take);
      }
      b.pinned = op.take;
      return { doc: d };
    }
  }
}

export const ASSET_KINDS: AssetKind[] = ["character", "prop", "place"];
/** An asset's @ handle (LibraryItem.handle's rule, lib/mentions.ts HANDLE). */
export const HANDLE = /^[a-z](?:[a-z0-9_-]{0,38}[a-z0-9])?$/;
const unique = (ids: string[]) => [...new Set(ids)];
/** Drops the names of takes no longer on its sheet (none: no views). */
function keepViews(a: Asset) {
  if (a.views)
    for (const id of Object.keys(a.views)) if (!a.sheet.includes(id)) delete a.views[id];
  if (a.views && !Object.keys(a.views).length) delete a.views;
}
/** The first unused a<n> among the board's assets. */
export function freshAssetId(d: Doc): string {
  let n = 1;
  while (d.assets.some((a) => a.id === `a${n}`)) n++;
  return `a${n}`;
}
const WORD = /[A-Za-z0-9_]/;
/** The text with every mention of handle h (the Mentions grammar, lib/mentions.ts: `@@` is a literal @, an @ after a
 * word character is text, a token is the run of letters, digits, _ and - minus trailing - and _) written as `to`. */
export function replaceMention(text: string, h: string, to: string): string {
  let out = "";
  let i = 0;
  while (i < text.length) {
    if (text.startsWith("@@", i)) {
      out += "@@";
      i += 2;
      continue;
    }
    if (text[i] === "@" && (i === 0 || !WORD.test(text[i - 1]))) {
      let j = i + 1;
      while (j < text.length && /[A-Za-z0-9_-]/.test(text[j])) j++;
      const tok = text.slice(i + 1, j).replace(/[-_]+$/, "");
      out += tok && /^[A-Za-z]/.test(tok) && tok.toLowerCase() === h.toLowerCase() ? to : `@${tok}`;
      i += 1 + tok.length;
      continue;
    }
    out += text[i];
    i++;
  }
  return out;
}

/** Seconds a new transition of each kind lasts. */
export const DEFAULT_LENGTH: Record<TransitionKind, number> = {
  cut: 0,
  dissolve: 0.5,
  fade: 1,
  wipe: 0.5,
  morph: 1,
};

// ------------------------------------------------------------------------------------------------ snapping

export type SnapKind = "edge" | "playhead" | "ruler";
/** Where a time snaps: the nearest block edge (blocks in `skip` aside), the playhead or a whole second on the ruler,
 * within `tol` frames; edges and the playhead win over the ruler. */
export function snap(
  d: Doc,
  t: number,
  tol: number,
  playhead: number,
  skip: Set<string> = new Set(),
): { t: number; kind: SnapKind } | null {
  const pts: { t: number; kind: SnapKind }[] = [{ t: playhead, kind: "playhead" }];
  for (const b of d.blocks)
    if (!skip.has(b.id)) pts.push({ t: b.at, kind: "edge" }, { t: end(b), kind: "edge" });
  let best: { t: number; kind: SnapKind } | null = null;
  for (const p of pts)
    if (Math.abs(p.t - t) <= tol && (!best || Math.abs(p.t - t) < Math.abs(best.t - t))) best = p;
  if (best) return best;
  const sec = Math.round(t / d.target.fps) * d.target.fps;
  return Math.abs(sec - t) <= tol / 2 ? { t: sec, kind: "ruler" } : null;
}

/** A dragged group's start snapped by either of its ends (the group is `len` long). */
export function snapGroup(
  d: Doc,
  at: number,
  len: number,
  tol: number,
  playhead: number,
  skip: Set<string>,
): { at: number; snapped: { t: number; kind: SnapKind } | null } {
  const a = snap(d, at, tol, playhead, skip);
  const b = snap(d, at + len, tol, playhead, skip);
  if (a && (!b || Math.abs(a.t - at) <= Math.abs(b.t - at - len))) return { at: a.t, snapped: a };
  if (b) return { at: b.t - len, snapped: b };
  return { at, snapped: null };
}

// ------------------------------------------------------------------------------------------------ what the board shows

/** A join on a lane: two blocks that meet (`between`), or the lane's start or end (its first or last block). */
export type Join = {
  key: string;
  at: number;
  lane: string;
  between?: [string, string];
  start?: string;
  end?: string;
  kind: TransitionKind;
  length: number;
};

/** The lane's joins with their transitions (a meeting without one is a cut); a start or end only when it has one. */
export function joins(d: Doc, laneId: string): Join[] {
  const bs = inLane(d, laneId);
  const out: Join[] = [];
  const find = (f: (t: Transition) => boolean) => d.transitions.find(f);
  const st = find((t) => t.start === laneId);
  if (st && bs.length)
    out.push({
      key: `s:${laneId}`,
      at: bs[0].at,
      lane: laneId,
      start: laneId,
      kind: st.kind,
      length: st.length,
    });
  for (let i = 0; i + 1 < bs.length; i++) {
    const [a, b] = [bs[i], bs[i + 1]];
    if (end(a) !== b.at) continue;
    const t = find((x) => x.between?.[0] === a.id && x.between?.[1] === b.id);
    out.push({
      key: `${a.id}>${b.id}`,
      at: b.at,
      lane: laneId,
      between: [a.id, b.id],
      kind: t?.kind ?? "cut",
      length: t?.length ?? 0,
    });
  }
  const en = find((t) => t.end === laneId);
  if (en && bs.length)
    out.push({
      key: `e:${laneId}`,
      at: end(bs.at(-1)!),
      lane: laneId,
      end: laneId,
      kind: en.kind,
      length: en.length,
    });
  return out;
}

/** A tied block that runs past its shot ("Line 1 runs 3 s past Shot 1"). */
export type Overrun = { block: string; shot: string; by: number };
export function overruns(d: Doc): Overrun[] {
  const byId = new Map(d.blocks.map((b) => [b.id, b]));
  const out: Overrun[] = [];
  for (const l of d.links) {
    const b = byId.get(l.block);
    const last = byId.get(l.to.at(-1)!);
    if (b && last && l.to.length === 1 && end(b) > end(last))
      out.push({ block: b.id, shot: last.id, by: end(b) - end(last) });
  }
  return out;
}

/** The board's length: its last block's end. */
export const duration = (d: Doc) => Math.max(0, ...d.blocks.map(end));

/** The blocks an asset is used in (its @ picks, a voice that is it). */
export const usedIn = (d: Doc, asset: string) => {
  const picks = (r?: Ref) => !!r && "asset" in r && r.asset === asset;
  return d.blocks.filter(
    (b) =>
      b.request.voice === asset ||
      picks(b.request.first) ||
      picks(b.request.last) ||
      (b.request.refs ?? []).some(picks),
  );
};

// ------------------------------------------------------------------------------------------------ takes and status

/** A take as the board needs it. Phase B's are fake (lib/magic-fixture.ts); phase C reads them from studio. */
export type TakeInfo = {
  id: string;
  block: string;
  status: "running" | "done" | "failed";
  progress?: number;
  /** the block's inputs when its run started (studio keeps a hash on the run, §45.7) */
  inputs: string;
  error?: string;
  seed?: number;
  /** the picture (a CSS background): a fake take's gradients, a studio take's thumbnail */
  art?: string;
  /** a studio take's signed link and kind (the block dialog's stage) */
  url?: string;
  kind?: "image" | "video" | "audio";
};
export type Takes = Record<string, TakeInfo>;

/** The take a block shows: its pinned one, else its latest done one. */
export function shown(b: Block, takes: Takes): TakeInfo | undefined {
  if (b.pinned && takes[b.pinned]?.status === "done") return takes[b.pinned];
  return [...b.takes]
    .reverse()
    .map((id) => takes[id])
    .find((t) => t?.status === "done");
}

/** What a block's take depends on: its request, the frame it was made from, the takes of blocks it @-picks. */
export function inputsOf(d: Doc, b: Block, takes: Takes): string {
  const byId = new Map(d.blocks.map((x) => [x.id, x]));
  const take = (id?: string) => {
    const x = id ? byId.get(id) : undefined;
    return x ? (shown(x, takes)?.id ?? null) : null;
  };
  const asset = (id: string) => {
    const a = d.assets.find((x) => x.id === id);
    return a ? [a.sheet, a.voice_id ?? null] : null;
  };
  const pick = (r?: Ref) => (!r ? null : "block" in r ? take(r.block) : asset(r.asset));
  const picks = (b.request.refs ?? []).map(pick);
  // a shot made from a frame follows the frame's request too (its words make the shot's prompt)
  const frame = b.from_frame ? byId.get(b.from_frame) : undefined;
  return JSON.stringify([
    b.request,
    take(b.from_frame),
    picks,
    pick(b.request.first),
    pick(b.request.last),
    b.request.voice ? asset(b.request.voice) : null,
    frame?.request ?? null,
  ]);
}

export type Status =
  | { s: "draft" }
  | { s: "running"; progress: number }
  | { s: "done" }
  | { s: "old" }
  | { s: "failed"; error: string };

/** Derived, never stored (§45.7): running when a run of it is going, failed when its last run failed and nothing is
 * done, draft with no take, out of date when its inputs changed after the take shown was made, else done. */
export function statusOf(d: Doc, b: Block, takes: Takes): Status {
  // a block's takes, then its runs that have none yet (in studio: running or failed, `run:<id>`)
  const ts = [
    ...b.takes.map((id) => takes[id]).filter(Boolean),
    ...Object.values(takes).filter((t) => t.block === b.id && !b.takes.includes(t.id)),
  ];
  const run = ts.findLast((t) => t.status === "running");
  if (run) return { s: "running", progress: run.progress ?? 0 };
  const t = shown(b, takes);
  if (!t) {
    const last = ts.at(-1);
    return last?.status === "failed"
      ? { s: "failed", error: last.error ?? "failed" }
      : { s: "draft" };
  }
  return t.inputs === inputsOf(d, b, takes) ? { s: "done" } : { s: "old" };
}

// ------------------------------------------------------------------------------------------------ undo

/** The board's history in this browser: Undo and Redo step through documents. Studio writes (a run adding a take)
 * change every document in it instead, so undo never takes a take back (§45.12). */
export type History = { past: Doc[]; now: Doc; future: Doc[] };
export const MAX_UNDO = 100;
export const history = (now: Doc): History => ({ past: [], now, future: [] });
export const commit = (h: History, doc: Doc): History => ({
  past: [...h.past, h.now].slice(-MAX_UNDO),
  now: doc,
  future: [],
});
export const undo = (h: History): History =>
  h.past.length
    ? { past: h.past.slice(0, -1), now: h.past.at(-1)!, future: [h.now, ...h.future] }
    : h;
export const redo = (h: History): History =>
  h.future.length ? { past: [...h.past, h.now], now: h.future[0], future: h.future.slice(1) } : h;
export const everywhere = (h: History, f: (d: Doc) => Doc): History => ({
  past: h.past.map(f),
  now: f(h.now),
  future: h.future.map(f),
});
