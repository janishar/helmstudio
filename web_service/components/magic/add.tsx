"use client";
// Add at a point (MagicBoard frame 2a): the kind with its orb, the length, Insert or Overwrite and what it starts from;
// then (frames 2c-2h, plan 39 phases D and H) the standard request for an image, a video, a voice, music or a sound,
// and Generate; a dialogue (API 1.89) its lines. A board frame is added as a draft. A line or a cue added under a shot
// is tied to it (2e, 2f: "Tied to Shot 2 · Unlink").
import { useEffect, useRef, useState } from "react";
import { Link2 } from "lucide-react";
import { Modal } from "@/components/kit";
import { KindTitle } from "@/components/magic/block-dialog";
import { KIND_ICON } from "@/components/magic/icons";
import { defaultKind, SOON } from "@/components/magic/menus";
import { EstimateLine, RequestForm } from "@/components/magic/request";
import { KIND_NAME } from "@/components/magic/icons";
import {
  clock,
  end,
  inLane,
  secs,
  type Block,
  type Doc,
  type Kind,
  type MagicRequest,
  type Mode,
} from "@/lib/magic";
import { FORM_KINDS, kindOf, modelOf, type Kinds } from "@/lib/magic-standard";

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

export type AddAt = { lane: string; at: number; kind?: Kind };

/** The new block's name: "Shot 4", "Line 3" (one more than that kind on the board). */
function nameFor(d: Doc, k: Pick): string {
  const kind = k === "dialogue" ? "voice" : k;
  return `${NAMES[k]} ${d.blocks.filter((b) => b.kind === kind).length + 1}`;
}

type AddProps = {
  doc: Doc;
  kinds: Kinds | null;
  mode: Mode;
  /** a draft (a board frame, a dialogue) */
  onAdd: (block: Omit<Block, "id">, mode: Mode) => void;
  /** step 2's Generate: the block with its request (tied to `tie` when given), then its run; null while runs can't go */
  onGenerate: ((block: Omit<Block, "id">, mode: Mode, tie?: string[]) => void) | null;
  /** From Gallery (frame 2a): a Gallery take at the point, at its own length, placed by `mode`; null: no Gallery */
  onGallery?: ((at: AddAt, mode: Mode) => void) | null;
  onClose: () => void;
};

export function AddDialog({ where, ...p }: AddProps & { where: AddAt | null }) {
  const lane = where && p.doc.lanes.find((l) => l.id === where.lane);
  const [step2, setStep2] = useState<{
    block: Omit<Block, "id">;
    mode: Mode;
    tie?: string[];
  } | null>(null);
  const close = () => {
    setStep2(null);
    p.onClose();
  };
  // Next and Back unmount the button that had the focus; with it on <body> Escape no longer closes the dialog
  const box = useRef<HTMLDivElement>(null);
  const on2 = !!step2;
  useEffect(() => {
    const d = box.current?.closest("dialog");
    if (d && document.activeElement === document.body) d.focus();
  }, [on2]);
  return (
    <Modal
      open={!!lane}
      onClose={close}
      title={
        step2
          ? `${KIND_NAME[step2.block.kind]} · ${step2.block.name}`
          : lane
            ? `Add at ${clock(where!.at, p.doc.target.fps, true)} · ${lane.name}`
            : "Add"
      }
      width="w-[min(94vw,560px)]"
      heading={step2 ? <KindTitle block={step2.block} /> : undefined}
      aside={step2 ? "2 of 2" : undefined}
    >
      <div ref={box} className="contents" />
      {lane && where && step2 ? (
        <Step2
          {...p}
          block={step2.block}
          mode={step2.mode}
          tie0={step2.tie}
          onBack={() => setStep2(null)}
          onClose={close}
        />
      ) : (
        lane &&
        where && (
          <Form
            key={`${where.lane}:${where.at}`}
            {...p}
            where={where}
            mode0={p.mode}
            onNext={(block, mode, tie) => setStep2({ block, mode, tie })}
            onClose={close}
          />
        )
      )}
    </Modal>
  );
}

/** Step 2 (frames 2c-2h): the standard request for the kind, its estimate, Back and Generate. */
function Step2({
  doc,
  kinds,
  block: b0,
  mode,
  tie0,
  onGenerate,
  onBack,
  onClose,
}: AddProps & { block: Omit<Block, "id">; tie0?: string[]; onBack: () => void }) {
  const [req, setReq] = useState<MagicRequest>(b0.request);
  const [tie, setTie] = useState(tie0);
  const model = modelOf(kinds, req);
  const block = { ...b0, request: req };
  const off = !onGenerate
    ? "Runs can't go here"
    : !model
      ? "No model for this kind on any host"
      : "";
  return (
    <div className="mg-add flex flex-col gap-3">
      <RequestForm
        doc={doc}
        block={{ ...block, id: "" }}
        kinds={kinds}
        request={req}
        onChange={setReq}
        tie={tie && { to: tie, onUnlink: () => setTie(undefined) }}
      />
      <div className="flex items-center gap-2">
        <EstimateLine model={model} block={block} doc={doc} />
        <span className="grow" />
        <button
          type="button"
          className="h-9 rounded-[10px] px-3.5 text-[13px] font-medium text-t2"
          onClick={onBack}
        >
          Back
        </button>
        <button
          type="button"
          className="key h-9 rounded-xl px-3.5 text-[13px]"
          disabled={!!off}
          title={off || undefined}
          onClick={() => {
            onGenerate!({ ...block, request: { ...req, model: model!.workflow_id } }, mode, tie);
            onClose();
          }}
        >
          Generate
        </button>
      </div>
    </div>
  );
}

function Form({
  doc,
  kinds,
  where,
  mode0,
  onAdd,
  onNext,
  onGallery,
  onClose,
}: AddProps & {
  where: AddAt;
  mode0: Mode;
  onNext: (block: Omit<Block, "id">, mode: Mode, tie?: string[]) => void;
}) {
  const fps = doc.target.fps;
  const lane = doc.lanes.find((l) => l.id === where.lane)!;
  const first = where.kind ?? defaultKind(lane);
  const [kind, setKind] = useState<Pick>(first);
  // the gap the point sits in, up to the next block on the lane
  const next = inLane(doc, lane.id).find((b) => b.at >= where.at);
  const prev = inLane(doc, lane.id).findLast((b) => end(b) <= where.at);
  const gap = next && next.at > where.at ? next.at - where.at : 0;
  const [len, setLen] = useState<number>(5 * fps);
  const [custom, setCustom] = useState(false);
  const [mode, setMode] = useState<Mode>(mode0);
  const shotKind = kind === "video";
  const [fromLast, setFromLast] = useState(!!prev && shotKind && prev.kind === "video");
  // a board frame at the same time on a storyboard lane: the shot can be made from it
  const frame = doc.blocks.find(
    (b) => b.kind === "frame" && b.at <= where.at && where.at < end(b) && b.lane !== lane.id,
  );
  // a sound added under a shot is made from its video (frame 2g: From · Shot 2 video; A prompt in step 2)
  const shotUnder = doc.blocks.find(
    (b) =>
      b.kind === "video" &&
      b.at <= where.at &&
      where.at < end(b) &&
      doc.lanes.find((l) => l.id === b.lane)?.role === "shots",
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

  const k: Kind = kind === "dialogue" ? "voice" : kind;
  // image, video, voice (a dialogue too), music and sound go on to their standard request (step 2); a board frame is a
  // draft
  const staged = FORM_KINDS.includes(k) && !kindOf(kinds, k)?.off;
  // a dialogue starts with two lines, its speakers the board's first two voices
  const voiced = doc.assets.filter((a) => a.voice_id);
  const lines = [0, 1].map((i) => ({
    speaker: voiced[i % Math.max(1, voiced.length)]?.id ?? "",
    text: "",
  }));
  const draft = (): Omit<Block, "id"> => ({
    lane: lane.id,
    at: where.at,
    length: Math.max(1, len),
    kind: k,
    name: nameFor(doc, kind),
    request: {
      kind: k,
      refs: [],
      ...(shotKind && fromLast && prev ? { first: { block: prev.id, part: "last" as const } } : {}),
      ...(kind === "dialogue" ? { turns: lines } : {}),
      ...(k === "frame" ? { style: "sketch" as const } : {}),
      // only when a model takes a video: Stable Audio alone would grey From with the shot stuck in it
      ...(k === "sound" &&
      shotUnder &&
      kindOf(kinds, "sound")?.models.some((m) => m.fields.includes("from"))
        ? { from: shotUnder.id }
        : {}),
    },
    takes: [],
    effects: [],
    ...(shotKind && fromFrame && frame ? { from_frame: frame.id } : {}),
  });
  // a line or a cue under a shot is tied to it
  const tie =
    (k === "voice" || k === "music") && shotUnder && shotUnder.lane !== lane.id
      ? [shotUnder.id]
      : undefined;
  const add = () => (staged ? onNext(draft(), mode, tie) : onAdd(draft(), mode));

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
              onClick={() => setKind(t.k)}
            >
              <span className="kn">
                <i className={`kio k-${t.k}`}>
                  <Icon size={15} strokeWidth={2} aria-hidden />
                </i>
                {t.name}
              </span>
              <small>{t.sub}</small>
            </button>
          );
        })}
      </div>
      <div className="fld">
        <span className="lb">Length</span>
        <div className="seg" role="group" aria-label="Length">
          {[5, 10].map((s) => (
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
        <button
          type="button"
          className="h-9 rounded-[10px] px-3.5 text-[13px] font-medium text-t2 disabled:opacity-50"
          disabled={!onGallery}
          title={onGallery ? "A take you already have, at its own length" : SOON.gallery}
          onClick={() => {
            onGallery?.(where, mode);
            onClose();
          }}
        >
          From Gallery
        </button>
        <span className="grow" />
        <button
          type="button"
          className="h-9 rounded-[10px] px-3.5 text-[13px] font-medium text-t2"
          onClick={onClose}
        >
          Cancel
        </button>
        <button type="button" className="key h-9 rounded-xl px-3.5 text-[13px]" onClick={add}>
          {staged ? "Next" : "Add"}
        </button>
      </div>
    </div>
  );
}
