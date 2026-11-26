"use client";
// Add at a point (MagicBoard frame 2a, with 2b's greyed kinds until plan 38 ships Create's Music and Sound): the kind
// with its orb, the length, Insert or Overwrite and what it starts from. Phase B adds a draft block; the standard
// request for the kind (frame 2's step 2) comes with phase D.
import { useState } from "react";
import { Link2 } from "lucide-react";
import { Modal } from "@/components/kit";
import { KIND_ICON } from "@/components/magic/icons";
import { NOT_YET, defaultKind } from "@/components/magic/menus";
import { clock, end, inLane, secs, type Block, type Doc, type Kind, type Mode } from "@/lib/magic";

type Pick = Kind | "dialogue";
const TILES: { k: Pick; name: string; sub: string }[] = [
  { k: "video", name: "Video", sub: "a shot" },
  { k: "image", name: "Image", sub: "a still" },
  { k: "voice", name: "Voice", sub: "a line" },
  { k: "dialogue", name: "Dialogue", sub: "lines, two or more" },
  { k: "music", name: "Music", sub: "a cue" },
  { k: "sound", name: "Sound", sub: "effects" },
  { k: "frame", name: "Board frame", sub: "a sketch" },
];
const NAMES: Record<Pick, string> = {
  video: "Shot",
  image: "Still",
  voice: "Line",
  dialogue: "Dialogue",
  music: "Cue",
  sound: "Sound",
  frame: "Frame",
};
const greyed = (k: Pick) => k === "music" || k === "sound";

export type AddAt = { lane: string; at: number; kind?: Kind };

/** The new block's name: "Shot 4", "Line 3" (one more than that kind on the board). */
function nameFor(d: Doc, k: Pick): string {
  const kind = k === "dialogue" ? "voice" : k;
  return `${NAMES[k]} ${d.blocks.filter((b) => b.kind === kind).length + 1}`;
}

export function AddDialog({
  doc,
  where,
  mode: mode0,
  onAdd,
  onClose,
}: {
  doc: Doc;
  where: AddAt | null;
  mode: Mode;
  onAdd: (block: Omit<Block, "id">, mode: Mode) => void;
  onClose: () => void;
}) {
  const lane = where && doc.lanes.find((l) => l.id === where.lane);
  return (
    <Modal
      open={!!lane}
      onClose={onClose}
      title={lane ? `Add at ${clock(where!.at, doc.target.fps, true)} · ${lane.name}` : "Add"}
      width="w-[min(94vw,560px)]"
    >
      {lane && where && (
        <Form
          key={`${where.lane}:${where.at}`}
          doc={doc}
          where={where}
          mode0={mode0}
          onAdd={onAdd}
          onClose={onClose}
        />
      )}
    </Modal>
  );
}

function Form({
  doc,
  where,
  mode0,
  onAdd,
  onClose,
}: {
  doc: Doc;
  where: AddAt;
  mode0: Mode;
  onAdd: (block: Omit<Block, "id">, mode: Mode) => void;
  onClose: () => void;
}) {
  const fps = doc.target.fps;
  const lane = doc.lanes.find((l) => l.id === where.lane)!;
  const first = where.kind ?? defaultKind(lane);
  const [kind, setKind] = useState<Pick>(greyed(first) ? "video" : first);
  // the gap the point sits in, up to the next block on the lane
  const next = inLane(doc, lane.id).find((b) => b.at >= where.at);
  const prev = inLane(doc, lane.id).findLast((b) => end(b) <= where.at);
  const gap = next && next.at > where.at ? next.at - where.at : 0;
  const [len, setLen] = useState<number>(4 * fps);
  const [custom, setCustom] = useState(false);
  const [mode, setMode] = useState<Mode>(mode0);
  const shotKind = kind === "video";
  const [fromLast, setFromLast] = useState(!!prev && shotKind && prev.kind === "video");
  // a board frame at the same time on a storyboard lane: the shot can be made from it
  const frame = doc.blocks.find(
    (b) => b.kind === "frame" && b.at <= where.at && where.at < end(b) && b.lane !== lane.id,
  );
  const [fromFrame, setFromFrame] = useState(false);
  const after = next
    ? doc.links
        .filter((l) => l.to.includes(next.id))
        .map((l) => doc.blocks.find((b) => b.id === l.block)?.name)
    : [];
  const pushes =
    mode === "insert" && next
      ? `Insert moves ${next.name.split(" · ")[0]}${after.length ? " and its tied line" : ""} ${secs(len, fps)} right`
      : mode === "overwrite" && next && next.at < where.at + len
        ? `Overwrite covers ${next.name.split(" · ")[0]} from ${clock(next.at, fps)}`
        : "";

  const add = () => {
    const k: Kind = kind === "dialogue" ? "voice" : kind;
    onAdd(
      {
        lane: lane.id,
        at: where.at,
        length: Math.max(1, len),
        kind: k,
        name: nameFor(doc, kind),
        request: {
          kind: k,
          refs: shotKind && fromLast && prev ? [{ block: prev.id, part: "last" }] : [],
          ...(kind === "dialogue" ? { turns: [] } : {}),
          ...(k === "frame" ? { style: "sketch" as const } : {}),
        },
        takes: [],
        effects: [],
        ...(shotKind && fromFrame && frame ? { from_frame: frame.id } : {}),
      },
      mode,
    );
  };

  return (
    <div className="mg-add flex flex-col gap-3">
      <div className="kg" role="group" aria-label="Kind">
        {TILES.map((t) => {
          const Icon = KIND_ICON[t.k];
          return (
            <button
              key={t.k}
              type="button"
              className="kt"
              aria-pressed={kind === t.k}
              disabled={greyed(t.k)}
              title={greyed(t.k) ? NOT_YET : undefined}
              onClick={() => setKind(t.k)}
            >
              <span className="kn">
                <i className={`kio k-${t.k}`}>
                  <Icon size={15} strokeWidth={2} aria-hidden />
                </i>
                {t.name}
              </span>
              <small>{greyed(t.k) ? "Not yet" : t.sub}</small>
            </button>
          );
        })}
      </div>
      <p className="m-0 text-[12px] text-t2">
        Music and Sound come with Create&apos;s Music and Sound. Until then their lanes take a file
        or a Gallery take.
      </p>
      <div className="fld">
        <span className="lb">Length</span>
        <div className="seg" role="group" aria-label="Length">
          {[4, 8].map((s) => (
            <button
              key={s}
              type="button"
              aria-pressed={!custom && len === s * fps}
              onClick={() => {
                setCustom(false);
                setLen(s * fps);
              }}
            >
              {s} s
            </button>
          ))}
          {gap > 0 && (
            <button
              type="button"
              aria-pressed={!custom && len === gap}
              onClick={() => {
                setCustom(false);
                setLen(gap);
              }}
            >
              Fill gap · {secs(gap, fps)}
            </button>
          )}
          <button type="button" aria-pressed={custom} onClick={() => setCustom(true)}>
            Custom
          </button>
        </div>
        {custom && (
          <label className="flex items-center gap-2 text-[12px] text-t2">
            Seconds
            <input
              type="number"
              min={0.5}
              step={0.5}
              className="in h-8 w-24 px-2"
              value={+(len / fps).toFixed(2)}
              onChange={(e) => setLen(Math.max(1, Math.round(+e.target.value * fps)))}
            />
          </label>
        )}
      </div>
      <div className="fld">
        <span className="lb">Place</span>
        <div className="seg" role="group" aria-label="Place">
          {(["insert", "overwrite"] as const).map((m) => (
            <button key={m} type="button" aria-pressed={mode === m} onClick={() => setMode(m)}>
              {m === "insert" ? "Insert" : "Overwrite"}
            </button>
          ))}
        </div>
        {pushes && <span className="text-[11px] text-t2">{pushes}</span>}
      </div>
      {shotKind && (prev?.kind === "video" || frame) && (
        <div className="flex flex-wrap gap-1.5">
          {prev?.kind === "video" && (
            <button
              type="button"
              className={`chip bevel rounded-[10px] bg-s4 ${fromLast ? "on shadow-[inset_0_0_0_1px_var(--a1)]" : ""}`}
              aria-pressed={fromLast}
              onClick={() => setFromLast((x) => !x)}
            >
              <Link2 size={13} aria-hidden />
              Start from {prev.name.split(" · ")[0]} · last frame
            </button>
          )}
          {frame && (
            <button
              type="button"
              className={`chip bevel rounded-[10px] bg-s4 ${fromFrame ? "shadow-[inset_0_0_0_1px_var(--a1)]" : ""}`}
              aria-pressed={fromFrame}
              onClick={() => setFromFrame((x) => !x)}
            >
              Make from {frame.name}
            </button>
          )}
        </div>
      )}
      <div className="flex items-center gap-2">
        <span className="grow" />
        <button
          type="button"
          className="h-9 rounded-[10px] px-3.5 text-[13px] font-medium text-t2"
          onClick={onClose}
        >
          Cancel
        </button>
        <button type="button" className="key h-9 rounded-xl px-3.5 text-[13px]" onClick={add}>
          Add
        </button>
      </div>
    </div>
  );
}
