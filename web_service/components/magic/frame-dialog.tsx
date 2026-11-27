"use client";
// A storyboard frame opened (MagicBoard frame 7; plan 39 phase E): left, the picture on stage, its style (Sketch,
// Rough or Full) and its takes by style, Redraw, or drop your own; right, who and where (assets: their pictures feed
// it), the framing (shot size, angle, move), what happens, the length, the line, the model and the shot's model, and
// the shot made from it with Generate from board. Rough cut plays the frames in order with their lines. Frames never
// go to the Timeline. Every change is kept when the dialog closes.
import { useEffect, useRef, useState } from "react";
import { Film, Pin, Plus, RefreshCw, Upload, X } from "lucide-react";
import { openMenu, type Item } from "@/components/context-menu";
import { ErrorNote, Modal } from "@/components/kit";
import { AtText } from "@/components/magic/at-text";
import { StatusChip } from "@/components/magic/lanes";
import { takeMenu, type Act } from "@/components/magic/menus";
import { EstimateLine, VariantField, useVariant } from "@/components/magic/request";
import { message } from "@/lib/api";
import {
  clock,
  end,
  secs,
  shown,
  type Block,
  type Framing,
  type MagicRequest,
  type Op,
  type Ref,
} from "@/lib/magic";
import {
  kindOf,
  madeBy,
  modelOf,
  type BlockRun,
  type Estimate,
  type Kinds,
} from "@/lib/magic-standard";
import { UPLOAD_TYPES, uploadFile } from "@/lib/takes";

const STYLES = [
  ["sketch", "Sketch"],
  ["rough", "Rough"],
  ["full", "Full"],
] as const;
const SIZES: [NonNullable<Framing["size"]>, string][] = [
  ["wide", "Wide"],
  ["medium", "Medium"],
  ["close", "Close"],
  ["over_shoulder", "Over the shoulder"],
];
const ANGLES: [NonNullable<Framing["angle"]>, string][] = [
  ["eye", "Eye level"],
  ["low", "Low"],
  ["high", "High"],
];
const MOVES: [NonNullable<Framing["move"]>, string][] = [
  ["still", "Still"],
  ["push_in", "Push in"],
  ["pan", "Pan"],
  ["follow", "Follow"],
];

export function FrameDialog({
  act,
  block,
  kinds,
  runs,
  estimate,
  shotEstimate,
  gallery,
  onClose,
}: {
  act: Act;
  block: Block | null;
  kinds: Kinds | null;
  runs: BlockRun[];
  estimate?: Estimate;
  /** the estimate of the shot made from it */
  shotEstimate: (id: string) => Estimate | undefined;
  gallery: boolean;
  onClose: () => void;
}) {
  const save = useRef<() => void>(() => undefined);
  return (
    <Modal
      open={!!block}
      onClose={() => {
        save.current();
        onClose();
      }}
      title={block ? `Board · ${block.name}` : "Board frame"}
      width="w-[min(96vw,1080px)]"
    >
      {block && (
        <Body
          key={block.id}
          act={act}
          b={block}
          kinds={kinds}
          runs={runs.filter((r) => r.block === block.id)}
          estimate={estimate}
          shotEstimate={shotEstimate}
          gallery={gallery}
          saveRef={save}
        />
      )}
    </Modal>
  );
}

function Body({
  act: a,
  b,
  kinds,
  runs,
  estimate,
  shotEstimate,
  gallery,
  saveRef,
}: {
  act: Act;
  b: Block;
  kinds: Kinds | null;
  runs: BlockRun[];
  estimate?: Estimate;
  shotEstimate: (id: string) => Estimate | undefined;
  gallery: boolean;
  saveRef: React.RefObject<() => void>;
}) {
  const d = a.doc;
  const fps = d.target.fps;
  const [req, setReq] = useState<MagicRequest>(b.request);
  const [len, setLen] = useState(b.length);
  const changed = JSON.stringify(req) !== JSON.stringify(b.request);
  /** what the dialog changed, as ops */
  const changes = (): Op[] => [
    ...(changed ? [{ op: "block" as const, id: b.id, set: { request: req } }] : []),
    ...(len !== b.length ? [{ op: "trim" as const, id: b.id, length: len }] : []),
  ];
  const save = () => {
    const ops = changes();
    if (ops.length) a.edit(ops);
  };
  // the dialog's close keeps what changed
  useEffect(() => {
    saveRef.current = save;
  });
  const set = (p: Partial<MagicRequest>) => setReq((r) => ({ ...r, ...p }));
  const setFraming = (p: Partial<Framing>) => set({ framing: { ...req.framing, ...p } });
  // the take on stage: the one clicked, else the one it shows (a take that came in since too)
  const [clicked, setPicked] = useState<string | undefined>();
  const picked = clicked && a.takes[clicked] ? clicked : shown(b, a.takes)?.id;
  const stage = picked ? a.takes[picked] : undefined;
  const n = (id?: string) => (id ? b.takes.indexOf(id) + 1 : 0);
  const done = b.takes.filter((id) => a.takes[id]?.status === "done");
  const running = Object.values(a.takes).filter((t) => t.block === b.id && t.status === "running");
  // each take by the style it was drawn in; a picture of your own is yours
  const styleOf = new Map(
    runs.flatMap((r) => r.takes.map((t) => [t, r.style ?? "sketch"] as const)),
  );
  // API 1.94: and by the variant it ran on ("Sketch · 2 · Turbo")
  const variantOfTake = (t: string) => a.takes[t]?.variant ?? "";
  const groups = [...STYLES, ["yours", "Yours"] as const].flatMap(([k, label]) => {
    const ids = done.filter((t) => (styleOf.get(t) ?? "yours") === k);
    return [...new Set(ids.map(variantOfTake))].map((variant) => ({
      k: `${k}:${variant}`,
      label,
      variant,
      ids: ids.filter((t) => variantOfTake(t) === variant),
    }));
  });
  const models = kindOf(kinds, "frame")?.models ?? [];
  const model = modelOf(kinds, req);
  const variant = useVariant(model, req).title;
  // the model line under the picture: "Sketch 2 · Qwen Image 2.1 · Turbo · seed 42"
  const stageLine =
    picked && stage
      ? [
          `${[...STYLES, ["yours", "Yours"] as const].find(([k]) => k === (styleOf.get(picked) ?? "yours"))?.[1]} ${n(picked)}`,
          madeBy(kinds, runs, picked),
          stage.seed != null && stage.seed >= 0 ? `seed ${stage.seed}` : "",
        ]
          .filter(Boolean)
          .join(" · ")
      : "";
  const shotModels = kindOf(kinds, "video")?.models ?? [];
  const shotModel = shotModels.find((m) => m.workflow_id === req.shot_model) ?? shotModels[0];
  const made = d.blocks.find((x) => x.from_frame === b.id);
  const shots = d.lanes.find((l) => l.role === "shots");
  const who = (req.refs ?? []).flatMap((r) =>
    "asset" in r ? d.assets.filter((x) => x.id === r.asset) : [],
  );
  const [error, setError] = useState("");
  const [upload, setUpload] = useState("");
  const file = useRef<HTMLInputElement>(null);
  async function own(f: File) {
    setError("");
    setUpload("Uploading…");
    try {
      const t = await uploadFile(f, () => undefined);
      a.edit({ op: "take_add", id: b.id, take: t.id });
      setPicked(t.id);
    } catch (e) {
      setError(message(e));
    } finally {
      setUpload("");
    }
  }
  const redraw = () => {
    save();
    a.run([b.id]);
  };
  // the frame's changes and the shot go in one edit
  const generate = () => a.fromBoard?.(b.id, changes());
  const seg = <T extends string>(
    label: string,
    opts: [T, string][],
    value: T | undefined,
    pick: (v: T) => void,
  ) => (
    <div className="fld">
      <span className="lb">{label}</span>
      <div className="seg flex-wrap" role="group" aria-label={label}>
        {opts.map(([v, text]) => (
          <button key={v} type="button" aria-pressed={value === v} onClick={() => pick(v)}>
            {text}
          </button>
        ))}
      </div>
    </div>
  );
  const addWho = (e: React.MouseEvent) =>
    openMenu(e, {
      title: "Who and where",
      items: [
        ...d.assets
          .filter((x) => !who.includes(x))
          .map((x) => ({
            label: `@${x.handle}`,
            keys: x.kind,
            onPick: () => set({ refs: [...(req.refs ?? []), { asset: x.id } as Ref] }),
          })),
        ...(d.assets.length > who.length ? (["-"] as Item[]) : []),
        {
          label: "New asset…",
          icon: Plus,
          off: a.openAsset ? undefined : "Comes with the board",
          onPick: () => a.openAsset?.("new"),
        },
      ],
    });
  return (
    <div className="mg-add flex flex-col gap-3">
      <div className="flex flex-wrap items-center gap-2 text-[12px] text-t2">
        <span>
          {d.lanes.find((l) => l.id === b.lane)?.name} ·{" "}
          <span className="mono">
            {clock(b.at, fps)} – {clock(end(b), fps)}
          </span>
        </span>
        <StatusChip d={d} b={b} takes={a.takes} onStop={a.stop && (() => a.stop!([b.id]))} />
        <span className="grow" />
        {picked && b.pinned !== picked && (
          <button
            type="button"
            className="chip bevel rounded-[10px] bg-s4"
            onClick={() => a.edit({ op: "block", id: b.id, set: { pinned: picked } })}
          >
            <Pin size={13} aria-hidden />
            Pin take {n(picked)}
          </button>
        )}
        <button
          type="button"
          className="chip bevel rounded-[10px] bg-s4"
          disabled={!a.rough}
          onClick={() => {
            save();
            a.rough?.(b.id);
          }}
        >
          <Film size={13} aria-hidden />
          Rough cut
        </button>
      </div>
      {error && <ErrorNote>{error}</ErrorNote>}
      <div className="grid gap-4 md:grid-cols-[minmax(0,1.15fr)_minmax(0,1fr)]">
        <section className="flex min-w-0 flex-col gap-2.5" aria-label="The frame">
          <div
            className="flex aspect-video items-center justify-center rounded-xl bg-cover bg-center p-2 text-center"
            style={{ backgroundColor: "var(--s3)", backgroundImage: stage?.art }}
            role="img"
            aria-label={stage ? `Take ${n(picked)}` : "No picture yet"}
            onDragOver={(e) => gallery && e.preventDefault()}
            onDrop={(e) => {
              e.preventDefault();
              const f = e.dataTransfer.files?.[0];
              if (gallery && f) void own(f);
            }}
          >
            {!stage && (
              <span className="text-[12px] text-t2">
                {upload || "No picture yet: Redraw, or drop your own"}
              </span>
            )}
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <div className="seg" role="group" aria-label="Style">
              {STYLES.map(([v, text]) => (
                <button
                  key={v}
                  type="button"
                  aria-pressed={(req.style ?? "sketch") === v}
                  onClick={() => set({ style: v })}
                >
                  {text}
                </button>
              ))}
            </div>
            <EstimateLine
              est={changed ? undefined : estimate}
              model={model}
              block={{ ...b, request: req }}
              doc={d}
              variant={variant}
            />
          </div>
          {stageLine && <span className="mono text-[11px] text-t2">{stageLine}</span>}
          {running.map((t) => (
            <span key={t.id} className="mst run self-start">
              Drawing · {Math.round(t.progress ?? 0)}%
            </span>
          ))}
          {groups.map((g) => (
            <div key={g.k} className="flex flex-col gap-1">
              <span className="text-[11px] text-t2">
                {g.label} · <span className="mono">{g.ids.length}</span>
                {g.variant && ` · ${g.variant}`}
              </span>
              <div
                className="flex flex-wrap gap-1.5"
                role="list"
                aria-label={`${g.label}${g.variant && ` ${g.variant}`} takes`}
              >
                {g.ids.map((id) => (
                  <button
                    key={id}
                    type="button"
                    role="listitem"
                    aria-label={`Take ${n(id)}${b.pinned === id ? ", pinned" : ""}`}
                    aria-current={picked === id}
                    className={`relative h-12 w-20 rounded-lg bg-s3 bg-cover bg-center ${picked === id ? "shadow-[inset_0_0_0_2px_var(--a1)]" : ""}`}
                    style={{ backgroundImage: a.takes[id]?.art }}
                    onClick={() => setPicked(id)}
                    onContextMenu={(e) =>
                      openMenu(
                        e,
                        takeMenu(a, b, id, n(id), () => setPicked(id)),
                      )
                    }
                  >
                    {b.pinned === id && (
                      <Pin size={11} className="absolute top-1 right-1" aria-hidden />
                    )}
                  </button>
                ))}
              </div>
            </div>
          ))}
          <div className="flex flex-wrap gap-2">
            <button
              type="button"
              className="chip bevel rounded-[10px] bg-s4"
              disabled={!!a.runOff || !model}
              title={a.runOff ?? (model ? undefined : "No image model on any host")}
              onClick={redraw}
            >
              <RefreshCw size={13} aria-hidden />
              Redraw
            </button>
            <button
              type="button"
              className="chip bevel rounded-[10px] bg-s4"
              disabled={!gallery || !!upload}
              title={gallery ? "A file, or drop one on the picture" : "Uploads are in studio"}
              onClick={(e) =>
                openMenu(e, {
                  title: "Your own image",
                  items: [
                    { label: "A file…", icon: Upload, onPick: () => file.current?.click() },
                    {
                      label: "From Gallery…",
                      off: a.gallery ? undefined : "The Gallery is in studio",
                      onPick: () =>
                        a.gallery?.({
                          kind: "image",
                          max: 1,
                          title: `A picture for ${b.name}`,
                          onPick: ([t]) => {
                            if (!t) return;
                            a.edit({ op: "take_add", id: b.id, take: t.id });
                            setPicked(t.id);
                          },
                        }),
                    },
                  ],
                })
              }
            >
              <Upload size={13} aria-hidden />
              Drop your own image
            </button>
            <input
              ref={file}
              type="file"
              hidden
              accept={UPLOAD_TYPES.join(",")}
              aria-label="Your own image"
              onChange={(e) => {
                const f = e.target.files?.[0];
                e.target.value = "";
                if (f) void own(f);
              }}
            />
          </div>
        </section>
        <section className="flex min-w-0 flex-col gap-3" aria-label="What it shows">
          <div className="fld">
            <span className="lb">Who and where</span>
            <div className="flex flex-wrap items-center gap-1.5">
              {who.map((x) => (
                <span
                  key={x.id}
                  className={`rc big ${x.kind === "character" ? "ch" : ""} inline-flex items-center gap-1`}
                >
                  @{x.handle}
                  <button
                    type="button"
                    aria-label={`Take @${x.handle} off`}
                    onClick={() =>
                      set({
                        refs: (req.refs ?? []).filter((r) => !("asset" in r) || r.asset !== x.id),
                      })
                    }
                  >
                    <X size={11} aria-hidden />
                  </button>
                </span>
              ))}
              <button
                type="button"
                className="chip h-7 rounded-[9px] bg-s4 px-2.5 text-[12px]"
                onClick={addWho}
              >
                <Plus size={12} aria-hidden />
                Add
              </button>
            </div>
            <span className="text-[11px] text-t2">Their pictures feed the frame.</span>
          </div>
          {seg("Framing", SIZES, req.framing?.size, (v) => setFraming({ size: v }))}
          {seg("Angle", ANGLES, req.framing?.angle, (v) => setFraming({ angle: v }))}
          {seg("Move", MOVES, req.framing?.move, (v) => setFraming({ move: v }))}
          <div className="fld">
            <span className="lb">What happens</span>
            <AtText
              doc={d}
              self={b.id}
              label="What happens"
              value={req.prompt ?? ""}
              refs={req.refs ?? []}
              onChange={(prompt, refs) => set({ prompt, refs })}
            />
          </div>
          <div className="grid grid-cols-2 gap-3">
            <label className="fld">
              <span className="lb">Length</span>
              <input
                type="number"
                aria-label="Length in seconds"
                className="in h-8 px-2 text-[13px]"
                min={0.5}
                step={0.5}
                value={+(len / fps).toFixed(2)}
                onChange={(e) => setLen(Math.max(1, Math.round(+e.target.value * fps)))}
              />
            </label>
            <label className="fld">
              <span className="lb">Line</span>
              <input
                aria-label="Line"
                className="in h-8 px-2 text-[13px]"
                placeholder="What is said over it"
                value={req.line ?? ""}
                onChange={(e) => set({ line: e.target.value })}
              />
            </label>
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div className="fld">
              <span className="lb">Model</span>
              {models.length ? (
                <div className="seg flex-wrap" role="group" aria-label="Model">
                  {models.map((m) => (
                    <button
                      key={m.workflow_id}
                      type="button"
                      aria-pressed={model?.workflow_id === m.workflow_id}
                      title={m.title}
                      // its variant is the model's own (API 1.94): another's Advanced goes (the picked one keeps it)
                      onClick={() =>
                        m.workflow_id !== model?.workflow_id &&
                        set({ model: m.workflow_id, advanced: undefined })
                      }
                    >
                      {m.name}
                    </button>
                  ))}
                </div>
              ) : (
                <span className="text-[12px] text-t2">No image model on any host</span>
              )}
            </div>
            <VariantField model={model} req={req} set={set} />
          </div>
          <div className="fld">
            <span className="lb">Shot model</span>
            {shotModels.length ? (
              <div className="seg flex-wrap" role="group" aria-label="Shot model">
                {shotModels.map((m) => (
                  <button
                    key={m.workflow_id}
                    type="button"
                    aria-pressed={shotModel?.workflow_id === m.workflow_id}
                    title={m.title}
                    onClick={() => set({ shot_model: m.workflow_id })}
                  >
                    {m.name}
                  </button>
                ))}
              </div>
            ) : (
              <span className="text-[12px] text-t2">No video model on any host</span>
            )}
          </div>
          <div className="flex flex-col gap-2 rounded-xl bg-s2 p-2.5">
            {made ? (
              <div className="flex flex-wrap items-center gap-2 text-[12px]">
                <span className="kb k-video">Video</span>
                <b>{made.name}</b>
                <span className="text-t2">was made from this frame</span>
                <StatusChip d={d} b={made} takes={a.takes} />
              </div>
            ) : (
              <span className="text-[12px] text-t2">
                {shots
                  ? "No shot from it yet: Generate from board makes one under it."
                  : "Add a Shots lane to make a shot from it."}
              </span>
            )}
            <div className="flex items-center gap-2">
              <EstimateLine
                est={made ? shotEstimate(made.id) : undefined}
                model={shotModel}
                block={made ?? { kind: "video", length: len, request: { kind: "video" } }}
                doc={d}
              />
              <span className="grow" />
              <button
                type="button"
                className="key h-9 rounded-xl px-3.5 text-[13px]"
                disabled={!a.fromBoard || (!made && !shots) || !shotModel}
                title={
                  !a.fromBoard
                    ? (a.runOff ?? "Runs come with the standard request")
                    : !made && !shots
                      ? "Add a Shots lane first"
                      : undefined
                }
                onClick={generate}
              >
                Generate from board
              </button>
            </div>
            <span className="text-[11px] text-t2">
              The shot stays tied to the frame: it goes out of date when the frame changes (
              {secs(len, fps)}).
            </span>
          </div>
        </section>
      </div>
    </div>
  );
}
