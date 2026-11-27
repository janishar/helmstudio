// Magic's right-click menus (MagicBoard frame 10), in the shared menu (components/context-menu.tsx): a block of each
// kind, several blocks, a lane header, the empty lane at a time, an asset, a join, a tie mark, an effect badge, a
// snapshot. Pure builders over the board's actions, so tests can read the items. What phase B can't do yet is greyed
// with the reason (plan 39 §45.11: the block and asset dialogs and Import come later, Music and Sound with plan 38).
import {
  ArrowDown,
  ArrowDownUp,
  ArrowUp,
  AtSign,
  AudioLines,
  Blend,
  Brush,
  Check,
  Clapperboard,
  ClipboardPaste,
  Columns2,
  Copy,
  Cpu,
  Eye,
  EyeOff,
  FastForward,
  Film,
  GitCompare,
  ImagePlus,
  ImageUp,
  Images,
  Link2,
  ListPlus,
  LocateFixed,
  Maximize2,
  Mic,
  MoveHorizontal,
  PencilLine,
  Pin,
  PinOff,
  Play,
  Plus,
  Power,
  RefreshCw,
  Repeat,
  RotateCcw,
  Scissors,
  Shapes,
  Timer,
  Trash2,
  Unlink,
  WandSparkles,
  X,
  type LucideIcon,
} from "lucide-react";
import type { Item, MenuItem } from "@/components/context-menu";
import type { S } from "@/lib/api";
import { mod } from "@/components/context-menu";
import {
  CutIcon,
  DissolveIcon,
  FadeIcon,
  KIND_ICON,
  MorphIcon,
  WipeIcon,
} from "@/components/magic/icons";
import {
  clock,
  end,
  freshId,
  inLane,
  secs,
  shown,
  usedIn,
  type Asset,
  type Block,
  type Doc,
  type Effect,
  type Join,
  type Kind,
  type Lane,
  type Link,
  type Mode,
  type Op,
  type Takes,
  type TransitionKind,
} from "@/lib/magic";
import { EST_S, estText } from "@/lib/magic-fixture";
import { missingViews, viewName } from "@/lib/magic-assets";

const RUNS_OFF = "Runs come with the standard request";

export const SOON = {
  dialog: "Comes with the block dialog",
  compare: "Comes with saved boards",
  /** the fake mode has no Gallery */
  gallery: "The Gallery is in studio",
};
/** The effects a block can take as badges (MagicBoard frame 4c). */
export const EFFECTS: Effect[] = [
  { name: "Grade · warm", params: { temperature: 12, contrast: 4 } },
  { name: "Slow · 0.5×", params: { rate: 0.5 } },
  { name: "Stabilize" },
];
export const LENGTHS = [0.5, 1, 2];
const STYLES = ["sketch", "rough", "full"] as const;

/** What the menus act through: the board's own actions. */
export type Act = {
  doc: Doc;
  takes: Takes;
  mode: Mode;
  playhead: number;
  sel: string[];
  edit: (op: Op | Op[]) => unknown;
  run: (ids: string[]) => void;
  /** a morph opened (its dialog, API 1.82) */
  openMorph?: (id: string) => void;
  /** why runs can't go yet; estimates hide with it */
  runOff?: string;
  /** run a block once an edit just made has put it on the board (absent: run it now) */
  runWhenAdded?: (id: string) => void;
  /** API 1.85: the models studio serves a kind (`video`: one takes a shot's video), and a model's name by its id */
  models?: (k: Kind) => { id: string; name: string; video: boolean }[];
  modelName?: (k: Kind, id?: string) => string | undefined;
  /** a block's estimate as a menu shows it ("≈ 1:50"); absent: the kind's guess (the fake mode) */
  estimate?: (id: string) => string | undefined;
  select: (ids: string[]) => void;
  /** select it and open it (the block dialog, frame 3) */
  open: (id: string) => void;
  /** the Add dialog at a lane and time, a kind picked */
  add: (lane: string, at: number, kind?: Kind) => void;
  play: (from: number) => void;
  seek: (f: number) => void;
  rename: (lane: string) => void;
  used: (asset: string | null) => void;
  paste: ((lane: string, at: number) => void) | null;
  copyText: (s: string) => void;
  /** phase E (absent: greyed with why): an asset opened (the asset dialog, frame 6), "new" makes one */
  openAsset?: (id: string) => void;
  /** Import from Gallery as a copy: into an asset, or (none) as a new one */
  importTo?: (asset?: string) => void;
  /** Generate from board: the shot under a frame, made from it, run (after `before`, the frame's own changes) */
  fromBoard?: (frame: string, before?: Op[]) => void;
  /** the rough cut, from a frame */
  rough?: (from?: string) => void;
  /** Gallery takes picked (null in the fake mode): of a kind (null: images and video), up to max */
  gallery?:
    | ((o: {
        kind: "image" | "video" | "audio" | null;
        max: number;
        title: string;
        onPick: (takes: { id: string; kind: string; duration?: number; title?: string }[]) => void;
      }) => void)
    | null;
  /** Make views of an asset (null while runs can't go), on an image model (absent: the kind's first) */
  views?: ((asset: string, views: string[], model?: string) => void) | null;
  /** Compare takes of a block (API 1.89: its dialog's Compare) */
  compare?: (id: string) => void;
};

const icon = (C: (p: { className?: string }) => React.ReactNode) => C as unknown as LucideIcon;
const kindIcon = (k: Kind) => KIND_ICON[k];
const blockById = (d: Doc, id: string) => d.blocks.find((b) => b.id === id);
const laneOf = (d: Doc, id: string) => d.lanes.find((l) => l.id === id);
/** a block's run estimate, as the menu shows it ("≈ 1:50", "≈ 12 s") */
const est = (k: Kind) => estText(EST_S[k], true);
/** a run item, off with the board's reason while runs can't go (its estimate hidden) */
const gate = (a: Act, it: MenuItem): MenuItem =>
  a.runOff ? { ...it, keys: undefined, off: it.off ?? a.runOff } : it;
/** the shots on the board (its Shots lanes), in time */
const shotsOf = (d: Doc) =>
  d.lanes.filter((l) => l.role === "shots").flatMap((l) => inLane(d, l.id));
const linkOf = (d: Doc, id: string) => d.links.find((l) => l.block === id);

/** "Tie to ›": each shot; music also the run of every shot. */
function tieItems(a: Act, b: Block): Item[] {
  const shots = shotsOf(a.doc).filter((s) => s.id !== b.id);
  const tied = linkOf(a.doc, b.id)?.to.join();
  return [
    ...shots.map((s) => ({
      label: s.name,
      checked: tied === s.id,
      onPick: () => a.edit({ op: "link", block: b.id, to: [s.id] }),
    })),
    b.kind === "music" &&
      shots.length > 1 && {
        label: `Shots 1–${shots.length}`,
        checked: tied === shots.map((s) => s.id).join(),
        onPick: () => a.edit({ op: "link", block: b.id, to: shots.map((s) => s.id) }),
      },
  ];
}

/** "Move to lane ›": every other lane. */
const laneItems = (a: Act, ids: string[], from?: string): Item[] =>
  a.doc.lanes
    .filter((l) => l.id !== from)
    .map((l) => ({
      label: l.name,
      onPick: () => a.edit({ op: "move", ids, dt: 0, lane: l.id, mode: a.mode }),
    }));

const effectItems = (a: Act, ids: string[]): Item[] =>
  EFFECTS.map((e) => ({ label: e.name, onPick: () => a.edit({ op: "effect", ids, effect: e }) }));

/** Fits a tied block to what it is tied to: a line to its shot's end, music to the run. */
function fit(a: Act, b: Block) {
  const l = linkOf(a.doc, b.id);
  const to = l?.to.map((id) => blockById(a.doc, id)).filter((x): x is Block => !!x) ?? [];
  if (!to.length) return;
  const at = l!.to.length > 1 ? Math.min(...to.map((x) => x.at)) : b.at;
  const stop = Math.max(...to.map(end));
  if (stop > at) a.edit({ op: "trim", id: b.id, at, length: stop - at });
}

/** A block's menu, by its kind; several selected blocks get the several menu. */
export function blockMenu(a: Act, b: Block): { title: string; items: Item[] } {
  if (a.sel.length > 1 && a.sel.includes(b.id)) return multiMenu(a, a.sel);
  const d = a.doc;
  const link = linkOf(d, b.id);
  const tiedTo = link && blockById(d, link.to[0]);
  const lane = laneOf(d, b.lane);
  const open: MenuItem = { label: "Open", icon: Maximize2, keys: "↵", onPick: () => a.open(b.id) };
  const run: MenuItem = gate(a, {
    label: "Run",
    icon: WandSparkles,
    keys: a.estimate ? a.estimate(b.id) : est(b.kind),
    onPick: () => a.run([b.id]),
  });
  const play: MenuItem = { label: "Play", icon: Play, keys: "Space", onPick: () => a.play(b.at) };
  const dup: MenuItem = {
    label: "Duplicate",
    icon: Copy,
    keys: mod("D"),
    onPick: () => a.edit({ op: "duplicate", ids: [b.id] }),
  };
  const move: MenuItem = {
    label: "Move to lane",
    icon: ArrowDownUp,
    items: laneItems(a, [b.id], b.lane),
  };
  const del: MenuItem = {
    label: "Delete",
    icon: Trash2,
    keys: "⌫",
    danger: true,
    onPick: () => a.edit({ op: "delete", ids: [b.id], mode: a.mode }),
  };
  const tie: MenuItem = { label: "Tie to", icon: Link2, items: tieItems(a, b) };
  const unlink: MenuItem | false = !!link && {
    label: "Unlink",
    icon: Unlink,
    onPick: () => a.edit({ op: "unlink", block: b.id }),
  };
  const title = b.name;
  if (b.kind === "voice")
    return {
      title,
      items: [
        open,
        run,
        play,
        b.request.turns
          ? { label: "Voice", icon: Mic, off: "Each line has its speaker: Open" }
          : {
              label: "Voice",
              icon: Mic,
              items: d.assets
                .filter((x) => x.voice_id)
                .map((x) => ({
                  label: `@${x.handle}`,
                  checked: b.request.voice === x.id,
                  onPick: () =>
                    a.edit({
                      op: "block",
                      id: b.id,
                      set: { request: { ...b.request, voice: x.id } },
                    }),
                })),
              off: d.assets.some((x) => x.voice_id) ? undefined : "No asset has a voice yet",
            },
        "-",
        tiedTo
          ? {
              label: `Go to ${tiedTo.name.split(" · ")[0]}`,
              icon: LocateFixed,
              onPick: () => a.select([tiedTo.id]),
            }
          : tie,
        !!tiedTo && { label: "Fit to shot", icon: MoveHorizontal, onPick: () => fit(a, b) },
        unlink,
        "-",
        dup,
        move,
        "-",
        del,
      ],
    };
  if (b.kind === "music")
    return {
      title,
      items: [
        open,
        run,
        play,
        {
          label: "Model",
          icon: Cpu,
          items: (a.models?.("music") ?? []).map((m, i) => ({
            label: m.name,
            checked: b.request.model ? b.request.model === m.id : i === 0,
            onPick: () =>
              a.edit({
                op: "block",
                id: b.id,
                set: { request: { ...b.request, model: m.id, advanced: undefined } },
              }),
          })),
          off: a.models?.("music").length ? undefined : "No music model on any host",
        },
        "-",
        tie,
        !!link && { label: "Fit to shots", icon: MoveHorizontal, onPick: () => fit(a, b) },
        unlink,
        "-",
        dup,
        move,
        "-",
        del,
      ],
    };
  if (b.kind === "sound") {
    const shot = (b.request.from && blockById(d, b.request.from)) || tiedTo;
    const video = a.models?.("sound").find((m) => m.video);
    return {
      title,
      items: [
        open,
        run,
        play,
        gate(a, {
          label: shot ? `Make from ${shot.name.split(" · ")[0]} video` : "Make from a shot's video",
          icon: Film,
          off: !shot
            ? "Tie it to a shot first"
            : !video
              ? "No sound model takes a video"
              : shown(shot, a.takes)
                ? undefined
                : `${shot.name.split(" · ")[0]} has no take yet`,
          // the request made from the shot's video on a model that takes one (MMAudio), then its run
          onPick: () => {
            const keep = a.models?.("sound").some((m) => m.video && m.id === b.request.model);
            a.edit({
              op: "block",
              id: b.id,
              set: {
                request: {
                  ...b.request,
                  from: shot!.id,
                  model: keep ? b.request.model : video!.id,
                  advanced: keep ? b.request.advanced : undefined,
                },
              },
            });
            a.run([b.id]);
          },
        }),
        {
          // API 1.89: the take repeats to fill the block (MagicBoard 2g)
          label: "Loop",
          icon: Repeat,
          checked: !!b.request.loop,
          off: b.request.from ? "Not with a shot's video" : undefined,
          onPick: () =>
            a.edit({
              op: "block",
              id: b.id,
              set: { request: { ...b.request, loop: b.request.loop ? undefined : true } },
            }),
        },
        "-",
        tie,
        unlink,
        "-",
        dup,
        move,
        "-",
        del,
      ],
    };
  }
  if (b.kind === "frame") {
    const made = d.blocks.find((x) => x.from_frame === b.id);
    const shots = d.lanes.find((l) => l.role === "shots");
    return {
      title,
      items: [
        open,
        gate(a, {
          label: "Redraw",
          icon: RefreshCw,
          keys: est("frame"),
          onPick: () => a.run([b.id]),
        }),
        {
          label: "Style",
          icon: Brush,
          items: STYLES.map((s) => ({
            label: s[0].toUpperCase() + s.slice(1),
            checked: (b.request.style ?? "sketch") === s,
            onPick: () =>
              a.edit({ op: "block", id: b.id, set: { request: { ...b.request, style: s } } }),
          })),
        },
        {
          label: "Replace with an image",
          icon: ImageUp,
          off: a.gallery ? undefined : SOON.gallery,
          onPick: () =>
            a.gallery?.({
              kind: "image",
              max: 1,
              title: `Replace ${b.name}'s picture`,
              onPick: ([t]) => t && a.edit({ op: "take_add", id: b.id, take: t.id }),
            }),
        },
        "-",
        gate(a, {
          label: "Generate from board",
          icon: Clapperboard,
          keys: a.runOff ? undefined : est("video"),
          off: made
            ? `${made.name} is made from it`
            : shots
              ? a.fromBoard
                ? undefined
                : (a.runOff ?? RUNS_OFF)
              : "Add a Shots lane first",
          onPick: () => a.fromBoard?.(b.id),
        }),
        {
          label: "Rough cut from here",
          icon: Film,
          off: a.rough ? undefined : "Comes with the board",
          onPick: () => a.rough?.(b.id),
        },
        "-",
        dup,
        "-",
        del,
      ],
    };
  }
  // a shot (video) or a still (image)
  const pinned = !!b.pinned;
  const take = shown(b, a.takes);
  const split = a.playhead > b.at && a.playhead < end(b);
  const sounds = d.lanes.find((l) => l.role === "sound");
  return {
    title,
    items: [
      open,
      run,
      gate(a, {
        label: "Run up to here",
        icon: FastForward,
        onPick: () =>
          a.run(
            inLane(d, b.lane)
              .filter((x) => x.at <= b.at)
              .map((x) => x.id),
          ),
      }),
      compareItem(a, b),
      pinned
        ? {
            label: "Unpin take",
            icon: PinOff,
            onPick: () => a.edit({ op: "block", id: b.id, set: { pinned: null } }),
          }
        : {
            label: "Pin take",
            icon: Pin,
            off: take ? undefined : "No take yet",
            onPick: () => take && a.edit({ op: "block", id: b.id, set: { pinned: take.id } }),
          },
      "-",
      {
        label: "Split at playhead",
        icon: Scissors,
        keys: "S",
        off: split ? undefined : "Put the playhead on it",
        onPick: () => a.edit({ op: "split", id: b.id, at: a.playhead }),
      },
      dup,
      { label: "Add effect", icon: Blend, items: effectItems(a, [b.id]) },
      {
        // a shot ties a line, a cue or a sound to itself
        label: "Tie to",
        icon: Link2,
        items: d.blocks
          .filter((x) => x.kind === "voice" || x.kind === "music" || x.kind === "sound")
          .map((x) => ({
            label: x.name,
            checked: linkOf(d, x.id)?.to.join() === b.id,
            onPick: () => a.edit({ op: "link", block: x.id, to: [b.id] }),
          })),
        off: d.blocks.some((x) => x.kind === "voice" || x.kind === "music" || x.kind === "sound")
          ? undefined
          : "No line, cue or sound to tie yet",
      },
      lane?.role !== "shots" && lane && move,
      b.kind === "video" &&
        gate(a, {
          label: "Make sound from video",
          icon: AudioLines,
          off: !sounds
            ? "Add a Sound lane first"
            : !a.models?.("sound").some((m) => m.video)
              ? "No sound model takes a video"
              : take
                ? undefined
                : "No take yet",
          // a sound under the shot on the Sound lane, tied to it, made from its video, then run (MagicBoard frame 10)
          onPick: () => {
            const id = freshId(d, "b");
            a.edit([
              {
                op: "add",
                mode: "overwrite",
                block: {
                  id,
                  lane: sounds!.id,
                  at: b.at,
                  length: b.length,
                  kind: "sound",
                  name: `Sound of ${b.name.split(" · ")[0]}`,
                  request: {
                    kind: "sound",
                    model: a.models?.("sound").find((m) => m.video)?.id,
                    from: b.id,
                  },
                  takes: [],
                  effects: [],
                },
              },
              { op: "link", block: id, to: [b.id] },
            ]);
            if (a.runWhenAdded) a.runWhenAdded(id);
            else a.run([id]);
          },
        }),
      b.kind === "video" && {
        label: "Use last frame in a new shot",
        icon: ImagePlus,
        onPick: () =>
          a.edit({
            op: "add",
            mode: "insert",
            block: {
              lane: b.lane,
              at: end(b),
              length: b.length,
              kind: "video",
              name: `After ${b.name.split(" · ")[0]}`,
              request: {
                kind: "video",
                model: b.request.model,
                first: { block: b.id, part: "last" },
              },
              takes: [],
              effects: [],
            },
          }),
      },
      "-",
      del,
    ],
  };
}

/** Several blocks: run them, move, effects, duplicate, delete. */
export function multiMenu(a: Act, ids: string[]): { title: string; items: Item[] } {
  const bs = ids.map((id) => blockById(a.doc, id)).filter((b): b is Block => !!b);
  const runnable = bs;
  const lanes = new Set(bs.map((b) => b.lane));
  return {
    title: `${bs.length} blocks`,
    items: [
      gate(a, {
        label: `Run ${runnable.length}`,
        icon: WandSparkles,
        keys: estText(
          runnable.reduce((n, b) => n + EST_S[b.kind], 0),
          true,
        ),
        onPick: () => a.run(runnable.map((b) => b.id)),
      }),
      {
        label: "Move to lane",
        icon: ArrowDownUp,
        off: lanes.size > 1 ? "Pick blocks from one lane" : undefined,
        items: laneItems(a, ids, [...lanes][0]),
      },
      { label: "Add effect", icon: Blend, items: effectItems(a, ids) },
      {
        label: "Duplicate",
        icon: Copy,
        keys: mod("D"),
        onPick: () => a.edit({ op: "duplicate", ids }),
      },
      "-",
      {
        label: `Delete ${bs.length}`,
        icon: Trash2,
        keys: "⌫",
        danger: true,
        onPick: () => a.edit({ op: "delete", ids, mode: a.mode }),
      },
    ],
  };
}

const KINDS: Kind[] = ["video", "image", "voice", "music", "sound", "frame"];
const KIND_LABEL: Record<Kind, string> = {
  video: "Video",
  image: "Image",
  voice: "Voice",
  music: "Music",
  sound: "Sound",
  frame: "Board frame",
};

/** A lane header's menu. */
export function laneMenu(a: Act, lane: Lane): { title: string; items: Item[] } {
  const d = a.doc;
  const bs = inLane(d, lane.id);
  const i = d.lanes.indexOf(lane);
  const runnable = bs;
  return {
    title: `Lane · ${lane.name}`,
    items: [
      { label: "Rename", icon: PencilLine, onPick: () => a.rename(lane.id) },
      {
        label: "Add here",
        icon: Plus,
        items: KINDS.map((k) => ({
          label: KIND_LABEL[k],
          icon: kindIcon(k),
          onPick: () => a.add(lane.id, a.playhead, k),
        })),
      },
      gate(a, {
        label: "Run lane",
        icon: WandSparkles,
        keys: runnable.length
          ? estText(
              runnable.reduce((n, b) => n + EST_S[b.kind], 0),
              true,
            )
          : undefined,
        off: bs.length ? undefined : "Nothing on it",
        onPick: () => a.run(runnable.map((b) => b.id)),
      }),
      {
        label: "Default kind",
        icon: Shapes,
        items: KINDS.map((k) => ({
          label: KIND_LABEL[k],
          checked: (lane.kind ?? defaultKind(lane)) === k,
          onPick: () => a.edit({ op: "lane", id: lane.id, set: { kind: k } }),
        })),
      },
      "-",
      {
        label: "Move up",
        icon: ArrowUp,
        off: i > 0 ? undefined : "It's the first lane",
        onPick: () => a.edit({ op: "lane_move", id: lane.id, by: -1 }),
      },
      {
        label: "Move down",
        icon: ArrowDown,
        off: i < d.lanes.length - 1 ? undefined : "It's the last lane",
        onPick: () => a.edit({ op: "lane_move", id: lane.id, by: 1 }),
      },
      {
        label: "Hide",
        icon: EyeOff,
        onPick: () => a.edit({ op: "lane", id: lane.id, set: { hidden: true } }),
      },
      {
        label: "New lane below",
        icon: ListPlus,
        onPick: () =>
          a.edit({ op: "lane_add", after: lane.id, lane: { name: nextLaneName(d), role: "free" } }),
      },
      "-",
      {
        label: "Delete lane",
        icon: Trash2,
        keys: bs.length ? `${bs.length} block${bs.length === 1 ? "" : "s"}` : undefined,
        danger: true,
        onPick: () => a.edit({ op: "lane_delete", id: lane.id }),
      },
    ],
  };
}

/** The kind Add picks first on a lane (plan 39 45.14 point 6: the role only picks the default). */
export function defaultKind(lane: Lane): Kind {
  if (lane.kind) return lane.kind;
  return (
    {
      storyboard: "frame",
      shots: "video",
      voice: "voice",
      music: "music",
      sound: "sound",
      free: "video",
    } as const
  )[lane.role];
}

/** "Lane 8": the next free lane's name. */
export function nextLaneName(d: Doc): string {
  let n = d.lanes.length + 1;
  while (d.lanes.some((l) => l.name === `Lane ${n}`)) n++;
  return `Lane ${n}`;
}

/** From Gallery (the empty lane's menu, Add's step 1: MagicBoard frame 2a): a Gallery take placed on the lane at the
 * point, at its own length, Insert or Overwrite (`mode`, else the board's). An audio lane picks audio. */
export function fromGallery(a: Act, lane: Lane, at: number, mode: Mode = a.mode) {
  const k = defaultKind(lane);
  const kind = k === "voice" || k === "music" || k === "sound" ? "audio" : null;
  a.gallery?.({
    kind,
    max: 1,
    title: `From Gallery · ${lane.name} at ${clock(at, a.doc.target.fps, true)}`,
    onPick: ([t]) => {
      if (!t) return;
      const bk: Kind =
        t.kind === "audio" ? (kind ? k : "voice") : t.kind === "video" ? "video" : "image";
      const fps = a.doc.target.fps;
      a.edit({
        op: "add",
        mode,
        block: {
          lane: lane.id,
          at,
          length: Math.max(1, Math.round((t.duration ?? 4) * fps)),
          kind: bk,
          name:
            (t.title ?? "").slice(0, 60) ||
            `${KIND_LABEL[bk]} ${a.doc.blocks.filter((x) => x.kind === bk).length + 1}`,
          request: { kind: bk },
          takes: [t.id],
          pinned: t.id,
          effects: [],
        },
      });
    },
  });
}

/** The empty lane at a time. */
export function emptyMenu(a: Act, lane: Lane, at: number): { title: string; items: Item[] } {
  return {
    title: `${lane.name} · at ${clock(at, a.doc.target.fps, true)}`,
    items: [
      ...KINDS.map((k) => ({
        label: `Add ${KIND_LABEL[k]}`,
        icon: kindIcon(k),
        onPick: () => a.add(lane.id, at, k),
      })),
      "-",
      {
        label: "Paste",
        icon: ClipboardPaste,
        keys: mod("V"),
        off: a.paste ? undefined : "Copy a block first",
        onPick: () => a.paste?.(lane.id, at),
      },
      {
        label: "From Gallery",
        icon: Images,
        off: a.gallery ? undefined : SOON.gallery,
        onPick: () => fromGallery(a, lane, at),
      },
      { label: "Playhead here", icon: LocateFixed, onPick: () => a.seek(at) },
    ],
  };
}

/** An asset in the Assets row. */
export function assetMenu(a: Act, x: Asset): { title: string; items: Item[] } {
  const n = usedIn(a.doc, x.id).length;
  return {
    title: `@${x.handle}`,
    items: [
      {
        label: "Open",
        icon: Maximize2,
        keys: "↵",
        off: a.openAsset ? undefined : "Comes with the board",
        onPick: () => a.openAsset?.(x.id),
      },
      {
        label: "Show where used",
        icon: Eye,
        keys: String(n),
        off: n ? undefined : "Not used yet",
        onPick: () => a.used(x.id),
      },
      { label: `Copy @${x.handle}`, icon: AtSign, onPick: () => a.copyText(`@${x.handle}`) },
      {
        label: "Make views",
        icon: Images,
        off: a.views ? undefined : (a.runOff ?? RUNS_OFF),
        onPick: () => a.views?.(x.id, missingViews(x)),
      },
      {
        label: "Voice",
        icon: Mic,
        keys: x.voice_id ? undefined : "none",
        off: a.openAsset ? undefined : "Comes with the board",
        onPick: () => a.openAsset?.(x.id),
      },
      {
        label: "Import a view",
        icon: ImageUp,
        off: a.importTo ? undefined : SOON.gallery,
        onPick: () => a.importTo?.(x.id),
      },
      {
        label: "Rename",
        icon: PencilLine,
        onPick: () => {
          const name = prompt("Asset name", x.name)?.trim();
          if (name && name !== x.name) a.edit({ op: "asset", id: x.id, set: { name } });
        },
      },
      "-",
      {
        label: "Delete",
        icon: Trash2,
        keys: n ? `used in ${n}` : undefined,
        danger: true,
        onPick: () => a.edit({ op: "asset_delete", id: x.id }),
      },
    ],
  };
}

/** A view on an asset's sheet (the asset dialog, frame 6): rename it, make it the first (the picture @ picks send),
 * take it off the sheet. */
export function viewMenu(a: Act, x: Asset, take: string): { title: string; items: Item[] } {
  const i = x.sheet.indexOf(take);
  return {
    title: `@${x.handle} · ${viewName(x, take)}`,
    items: [
      {
        label: "Rename view",
        icon: PencilLine,
        onPick: () => {
          const name = prompt("View name", viewName(x, take))?.trim().slice(0, 40);
          if (name) a.edit({ op: "asset", id: x.id, set: { views: { ...x.views, [take]: name } } });
        },
      },
      {
        label: "Make it the first",
        icon: ArrowUp,
        keys: i === 0 ? undefined : "@ picks send it",
        off: i === 0 ? "It's the first" : undefined,
        onPick: () =>
          a.edit({
            op: "asset",
            id: x.id,
            set: { sheet: [take, ...x.sheet.filter((t) => t !== take)] },
          }),
      },
      "-",
      {
        label: "Take off the sheet",
        icon: Trash2,
        danger: true,
        onPick: () =>
          a.edit({ op: "asset", id: x.id, set: { sheet: x.sheet.filter((t) => t !== take) } }),
      },
    ],
  };
}

const JOIN_KINDS: { kind: TransitionKind; label: string; icon: LucideIcon; note?: string }[] = [
  { kind: "cut", label: "Cut", icon: icon(CutIcon) },
  { kind: "dissolve", label: "Dissolve", icon: icon(DissolveIcon) },
  { kind: "fade", label: "Fade", icon: icon(FadeIcon) },
  { kind: "wipe", label: "Wipe", icon: icon(WipeIcon) },
  { kind: "morph", label: "Morph", icon: icon(MorphIcon), note: "generated" },
];
const DEFAULT_S: Record<TransitionKind, number> = {
  cut: 0,
  dissolve: 0.5,
  fade: 1,
  wipe: 0.5,
  morph: 1,
};

/** A join's name: "Join · Shot 2 → Shot 3", "Start · Shots". */
export function joinTitle(d: Doc, j: Join): string {
  const short = (id: string) => blockById(d, id)?.name.split(" · ")[0] ?? "?";
  if (j.between) return `Join · ${short(j.between[0])} → ${short(j.between[1])}`;
  return `${j.start ? "Start" : "End"} · ${laneOf(d, j.lane)?.name}`;
}

/** A join (a transition point): its kind, its length, Remove (a cut). A lane's start or end: Fade or none. */
/** A morph's own items (API 1.82): open it, generate its take. */
function morphItems(a: Act, j: Join): Item[] {
  const t = a.doc.transitions.find(
    (x) => x.kind === "morph" && x.between?.join() === j.between?.join(),
  );
  if (!t) return [];
  return [
    "-",
    {
      label: "Open",
      icon: Maximize2,
      keys: "↵",
      off: a.openMorph ? undefined : "Comes with the board",
      onPick: () => a.openMorph?.(t.id),
    },
    gate(a, {
      label: t.takes?.length ? "Generate again" : "Generate",
      icon: WandSparkles,
      keys: a.estimate?.(t.id) ?? "generated",
      onPick: () => a.run([t.id]),
    }),
  ];
}

/** A send (frame 9's sends list): open its sequence, put the board back as it was sent, take it off the list. */
export function sendMenu(
  on: {
    open: (s: S<"MagicSend">) => void;
    restore: (s: S<"MagicSend">) => void;
    remove: (s: S<"MagicSend">) => void;
  },
  s: S<"MagicSend">,
): { title: string; items: Item[] } {
  return {
    title: s.name,
    items: [
      {
        label: "Open in Timeline",
        icon: Film,
        off:
          s.status === "baking"
            ? "Still baking"
            : s.status === "failed"
              ? (s.failure ?? "Not sent")
              : s.timeline_gone
                ? "Its sequence was deleted"
                : undefined,
        onPick: () => on.open(s),
      },
      { label: `Restore board to rev ${s.revision}`, icon: RotateCcw, onPick: () => on.restore(s) },
      "-",
      {
        label: "Remove from list",
        icon: Trash2,
        keys: s.status === "baking" ? "stops it" : undefined,
        onPick: () => on.remove(s),
      },
    ],
  };
}

export function joinMenu(a: Act, j: Join): { title: string; items: Item[] } {
  const fps = a.doc.target.fps;
  const where = j.between ? { between: j.between } : j.start ? { start: j.start } : { end: j.end };
  const set = (kind: TransitionKind, length?: number) =>
    a.edit({ op: "transition", ...where, kind, ...(length ? { length } : {}) });
  const kinds = j.between ? JOIN_KINDS : JOIN_KINDS.filter((k) => k.kind === "fade");
  return {
    title: joinTitle(a.doc, j),
    items: [
      ...kinds.map((k) => ({
        label: k.label,
        icon: k.icon,
        checked: j.kind === k.kind,
        keys:
          k.kind === "cut"
            ? undefined
            : (k.note ?? secs(j.kind === k.kind ? j.length : DEFAULT_S[k.kind] * fps, fps)),
        onPick: () => set(k.kind),
      })),
      ...(j.kind === "morph" && j.between ? morphItems(a, j) : []),
      "-",
      {
        label: "Length",
        icon: Timer,
        off: j.kind === "cut" ? "A cut has no length" : undefined,
        items: LENGTHS.map((s) => ({
          label: `${s} s`,
          checked: j.length === Math.round(s * fps),
          onPick: () => set(j.kind, Math.round(s * fps)),
        })),
      },
      {
        label: "Remove",
        icon: Trash2,
        keys: j.between ? "makes a Cut" : undefined,
        off: j.kind === "cut" ? "It's a cut" : undefined,
        onPick: () => set("cut"),
      },
    ],
  };
}

/** A tie mark on a voice, music or sound block. */
export function tieMenu(a: Act, link: Link): { title: string; items: Item[] } {
  const b = blockById(a.doc, link.block)!;
  const to = link.to.map((id) => blockById(a.doc, id)).filter((x): x is Block => !!x);
  const short = (x: Block) => x.name.split(" · ")[0];
  const target =
    to.length > 1 ? `${short(to[0])}–${short(to.at(-1)!).replace(/^\D+/, "")}` : short(to[0]);
  return {
    title: `Tie · ${short(b)} → ${target}`,
    items: [
      { label: `Go to ${short(to[0])}`, icon: LocateFixed, onPick: () => a.select([to[0].id]) },
      { label: "Tie to", icon: Link2, items: tieItems(a, b) },
      {
        label: to.length > 1 ? "Fit to shots" : "Fit to shot",
        icon: MoveHorizontal,
        onPick: () => fit(a, b),
      },
      "-",
      { label: "Unlink", icon: Unlink, onPick: () => a.edit({ op: "unlink", block: b.id }) },
    ],
  };
}

/** An effect badge. Edit shows the block's effects in the side panel. */
export function effectMenu(a: Act, b: Block, index: number): { title: string; items: Item[] } {
  const e = b.effects[index];
  const others = a.sel.filter((id) => id !== b.id);
  return {
    title: e.name,
    items: [
      { label: "Edit", icon: PencilLine, onPick: () => a.open(b.id) },
      {
        label: e.off ? "Turn on" : "Turn off",
        icon: Power,
        onPick: () => a.edit({ op: "effect_set", id: b.id, index, effect: { ...e, off: !e.off } }),
      },
      {
        label: "Copy to selected",
        icon: Copy,
        off: others.length ? undefined : "Select other blocks first",
        onPick: () => a.edit({ op: "effect", ids: others, effect: e }),
      },
      "-",
      {
        label: "Remove",
        icon: Trash2,
        danger: true,
        onPick: () => a.edit({ op: "effect_set", id: b.id, index, effect: null }),
      },
    ],
  };
}

/** A snapshot: a named copy of the board to come back to (kept in this browser until phase C keeps revisions). */
export function snapshotMenu(
  name: string,
  on: { restore: () => void; rename: () => void; remove: () => void },
): { title: string; items: Item[] } {
  return {
    title: name,
    items: [
      { label: "Restore", icon: RotateCcw, onPick: on.restore },
      { label: "Compare with now", icon: GitCompare, off: SOON.compare },
      { label: "Rename", icon: PencilLine, onPick: on.rename },
      "-",
      { label: "Delete", icon: Trash2, danger: true, onPick: on.remove },
    ],
  };
}

/** A take in the block dialog (frame 3's takes): pin or unpin it, show it, copy its id. */
export function takeMenu(
  a: Act,
  b: Block,
  take: string,
  n: number,
  show: () => void,
): { title: string; items: Item[] } {
  const pinned = b.pinned === take;
  return {
    title: `Take ${n}`,
    items: [
      { label: "Show", icon: Eye, onPick: show },
      pinned
        ? {
            label: "Unpin",
            icon: Pin,
            onPick: () => a.edit({ op: "block", id: b.id, set: { pinned: null } }),
          }
        : {
            label: `Pin take ${n}`,
            icon: Pin,
            onPick: () => a.edit({ op: "block", id: b.id, set: { pinned: take } }),
          },
      "-",
      { label: "Copy take id", icon: Copy, onPick: () => a.copyText(take) },
    ],
  };
}

/** Compare takes (frame 10): its dialog's two takes side by side; off with fewer than two. */
export function compareItem(a: Act, b: Block): MenuItem {
  const n = b.takes.filter((id) => a.takes[id]?.status === "done").length;
  return {
    label: "Compare takes",
    icon: Columns2,
    off: n > 1 ? (a.compare ? undefined : SOON.dialog) : n ? "Only one take yet" : "No takes yet",
    onPick: () => a.compare?.(b.id),
  };
}

/** A dialogue's line in its form (API 1.89): move it, remove it. */
export function turnMenu(
  i: number,
  n: number,
  on: { up: () => void; down: () => void; remove: () => void },
): { title: string; items: Item[] } {
  return {
    title: `Line ${i + 1}`,
    items: [
      {
        label: "Move up",
        icon: ArrowUp,
        off: i > 0 ? undefined : "It's the first line",
        onPick: on.up,
      },
      {
        label: "Move down",
        icon: ArrowDown,
        off: i < n - 1 ? undefined : "It's the last line",
        onPick: on.down,
      },
      "-",
      { label: "Remove line", icon: Trash2, danger: true, onPick: on.remove },
    ],
  };
}

/** A song's section marker on its music block (API 1.89): the playhead to it, play from it. */
export function sectionMenu(
  a: Act,
  b: Block,
  label: string,
  at: number,
): { title: string; items: Item[] } {
  return {
    title: `${b.name.split(" · ")[0]} · ${label}`,
    items: [
      { label: "Play from here", icon: Play, keys: "Space", onPick: () => a.play(at) },
      { label: "Playhead here", icon: LocateFixed, onPick: () => a.seek(at) },
    ],
  };
}

/** A run in the block dialog's run history: run it again with its seed, copy its id. */
export function runMenu(
  a: Act,
  b: Block,
  run: { run_id: string; seed?: number | null },
  n: number,
): { title: string; items: Item[] } {
  const seed = run.seed != null && run.seed >= 0 ? run.seed : undefined;
  return {
    title: `Run ${n}`,
    items: [
      gate(a, {
        label: seed != null ? `Run again · seed ${seed}` : "Run again",
        icon: WandSparkles,
        onPick: () => {
          if (seed != null && seed !== b.request.seed)
            a.edit({ op: "block", id: b.id, set: { request: { ...b.request, seed } } });
          a.run([b.id]);
        },
      }),
      "-",
      { label: "Copy run id", icon: Copy, onPick: () => a.copyText(run.run_id) },
    ],
  };
}

// ------------------------------------------------------------------ the Assistant (plan 39 phase F, frame 8)

/** The change's own menu (on the bar and on a block it draws). */
export function proposalMenu(
  on: { apply: () => void; discard: () => void },
  title: string,
): { title: string; items: Item[] } {
  return {
    title,
    items: [
      { label: "Apply", icon: Check, onPick: on.apply },
      { label: "Discard", icon: X, onPick: on.discard },
    ],
  };
}

/** A revision's own menu: Restore rev N (not the board as it is now). */
export function historyMenu(
  on: { restore: (r: S<"MagicRevisionItem">) => void },
  r: S<"MagicRevisionItem">,
  now: number,
): { title: string; items: Item[] } {
  return {
    title: `rev ${r.revision}`,
    items: [
      {
        label: `Restore rev ${r.revision}`,
        icon: RotateCcw,
        off: r.revision === now ? "It's the board now" : undefined,
        onPick: () => on.restore(r),
      },
    ],
  };
}
