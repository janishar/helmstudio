"use client";
// A block opened (MagicBoard frame 3; plan 39 phase D): left, its @ references, what is tied to it, notes the
// Assistant also reads and the run history (who ran it, how long); middle, the standard request with Advanced open
// and Generate with its estimate; right, the take on stage, the takes (the picked one outlined, the pinned one
// marked, a running one with its progress) and Pin. Takes and runs have their right-click menus.
import { useState } from "react";
import { Pin } from "lucide-react";
import { openMenu } from "@/components/context-menu";
import { Modal } from "@/components/kit";
import { KIND_NAME, LinkGlyph } from "@/components/magic/icons";
import { StatusChip, refText } from "@/components/magic/lanes";
import { runMenu, takeMenu, type Act } from "@/components/magic/menus";
import { EstimateLine, RequestForm } from "@/components/magic/request";
import { clock, end, shown, type Block, type MagicRequest, type Ref } from "@/lib/magic";
import {
  FORM_KINDS,
  modelOf,
  runLine,
  type BlockRun,
  type Estimate,
  type Kinds,
} from "@/lib/magic-standard";

export function BlockDialog({
  act,
  block,
  kinds,
  runs,
  estimate,
  onClose,
}: {
  act: Act;
  block: Block | null;
  kinds: Kinds | null;
  runs: BlockRun[];
  estimate?: Estimate;
  onClose: () => void;
}) {
  return (
    <Modal
      open={!!block}
      onClose={onClose}
      title={block ? `${KIND_NAME[block.kind]} · ${block.name}` : "Block"}
      width="w-[min(96vw,1120px)]"
    >
      {block && (
        <Body
          key={block.id}
          act={act}
          b={block}
          kinds={kinds}
          runs={runs.filter((r) => r.block === block.id)}
          estimate={estimate}
          onClose={onClose}
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
  onClose,
}: {
  act: Act;
  b: Block;
  kinds: Kinds | null;
  runs: BlockRun[];
  estimate?: Estimate;
  onClose: () => void;
}) {
  const d = a.doc;
  const fps = d.target.fps;
  const [req, setReq] = useState<MagicRequest>(b.request);
  const changed = JSON.stringify(req) !== JSON.stringify(b.request);
  const lane = d.lanes.find((l) => l.id === b.lane);
  const takes = b.takes.filter((id) => a.takes[id]?.status === "done");
  const running = Object.values(a.takes).filter((t) => t.block === b.id && t.status === "running");
  const [picked, setPicked] = useState<string | undefined>(shown(b, a.takes)?.id);
  const stage = picked ? a.takes[picked] : undefined;
  const n = (id?: string) => (id ? b.takes.indexOf(id) + 1 : 0);
  const model = modelOf(kinds, req);
  const form = FORM_KINDS.includes(b.kind) && !req.turns?.length;
  const refs: { r: Ref; why: string }[] = [
    ...(req.first ? [{ r: req.first, why: "first frame of this shot" }] : []),
    ...(req.last ? [{ r: req.last, why: "last frame of this shot" }] : []),
    ...(req.refs ?? []).map((r) => ({ r, why: "a reference" })),
  ];
  const tied = d.links.filter((l) => l.block === b.id || l.to.includes(b.id));
  const save = () => changed && a.edit({ op: "block", id: b.id, set: { request: req } });
  const generate = () => {
    save();
    a.run([b.id]);
  };
  const close = () => {
    save();
    onClose();
  };
  return (
    <div className="mg-add flex flex-col gap-3">
      <div className="flex flex-wrap items-center gap-2 text-[12px] text-t2">
        <span>
          {lane?.name} ·{" "}
          <span className="mono">
            {clock(b.at, fps)} – {clock(end(b), fps)}
          </span>
        </span>
        <StatusChip d={d} b={b} takes={a.takes} />
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
      </div>
      <div className="grid gap-4 md:grid-cols-[minmax(0,0.8fr)_minmax(0,1.2fr)_minmax(0,1fr)]">
        <section className="flex min-w-0 flex-col gap-3" aria-label="About it">
          <div className="flex flex-col gap-1.5">
            <h3 className="m-0 text-[13px]">@ References</h3>
            {refs.length ? (
              refs.map(({ r, why }, i) => (
                <div key={i} className="flex flex-col">
                  <span className={`rc big self-start ${"block" in r ? "lk" : "ch"}`}>
                    {refText(d, r)}
                  </span>
                  <small className="text-[11px] text-t2">{why}</small>
                </div>
              ))
            ) : (
              <span className="text-[12px] text-t2">None</span>
            )}
          </div>
          {tied.length > 0 && (
            <div className="flex flex-col gap-1.5">
              <h3 className="m-0 text-[13px]">Tied</h3>
              {tied.map((l) => {
                const other = d.blocks.find((x) => x.id === (l.block === b.id ? l.to[0] : l.block));
                return (
                  other && (
                    <div key={l.block} className="nr">
                      <span className="lkm" aria-hidden>
                        <LinkGlyph />
                      </span>
                      <span className={`kb k-${other.kind}`}>{KIND_NAME[other.kind]}</span>
                      {other.name}
                      <span className="mono">moves with it</span>
                    </div>
                  )
                );
              })}
            </div>
          )}
          <div className="flex flex-col gap-1.5">
            <h3 className="m-0 text-[13px]">Notes</h3>
            <textarea
              aria-label="Notes"
              className="in min-h-14 px-2.5 py-2 text-[13px]"
              defaultValue={b.note ?? ""}
              onBlur={(e) =>
                e.target.value !== (b.note ?? "") &&
                a.edit({ op: "block", id: b.id, set: { note: e.target.value } })
              }
            />
          </div>
          <div className="flex flex-col gap-1.5">
            <h3 className="m-0 text-[13px]">Run history</h3>
            {runs.length ? (
              runs
                .map((r, i) => ({ r, i }))
                .reverse()
                .map(({ r, i }) => {
                  const line = runLine(r, i + 1);
                  return (
                    <div
                      key={r.run_id}
                      className="flex flex-col rounded-[10px] bg-s2 px-2.5 py-1.5 text-[12px]"
                      role="group"
                      aria-label={line.title}
                      onContextMenu={(e) => openMenu(e, runMenu(a, b, r, i + 1))}
                    >
                      <b className="text-t1">{line.title}</b>
                      <span className="mono min-w-0 truncate text-[11px] text-t2">{line.sub}</span>
                    </div>
                  );
                })
            ) : (
              <span className="text-[12px] text-t2">No runs yet</span>
            )}
          </div>
        </section>
        <section className="flex min-w-0 flex-col gap-3" aria-label="Request">
          {form ? (
            <>
              <RequestForm
                doc={d}
                block={b}
                kinds={kinds}
                request={req}
                onChange={setReq}
                advancedOpen
              />
              <div className="flex items-center gap-2">
                <EstimateLine
                  est={changed ? undefined : estimate}
                  model={model}
                  block={{ ...b, request: req }}
                  doc={d}
                />
                <span className="grow" />
                <button
                  type="button"
                  className="key h-9 rounded-xl px-3.5 text-[13px]"
                  disabled={!!a.runOff || !model}
                  title={a.runOff ?? (model ? undefined : "No model for this kind on any host")}
                  onClick={generate}
                >
                  Generate
                </button>
              </div>
            </>
          ) : (
            <p className="m-0 text-[13px] text-t2">
              {kinds?.kinds.find((k) => k.kind === b.kind)?.off ??
                (req.turns?.length ? "Dialogue blocks can't run yet" : "")}
            </p>
          )}
        </section>
        <section className="flex min-w-0 flex-col gap-2.5" aria-label="Takes">
          <div
            className="flex aspect-video items-end overflow-hidden rounded-xl bg-cover bg-center p-2"
            style={{ backgroundColor: "var(--s2)", backgroundImage: stage?.art }}
          >
            {stage?.kind === "video" && stage.url ? (
              <video
                src={stage.url}
                controls
                className="h-full w-full object-contain"
                aria-label={`Take ${n(picked)}`}
              />
            ) : stage?.kind === "audio" && stage.url ? (
              <audio src={stage.url} controls aria-label={`Take ${n(picked)}`} />
            ) : (
              !stage && <span className="text-[12px] text-t2">No take yet</span>
            )}
          </div>
          {stage && (
            <span className="text-[12px] text-t2">
              Take {n(picked)}
              {b.pinned === picked ? " · pinned" : ""}
            </span>
          )}
          <h3 className="m-0 text-[13px]">
            Takes · <span className="mono">{takes.length}</span>
          </h3>
          <div className="flex flex-col gap-1" role="list" aria-label="Takes">
            {running.map((t) => (
              <div key={t.id} className="nr" role="listitem">
                <span className="mst run">Running · {Math.round(t.progress ?? 0)}%</span>
              </div>
            ))}
            {[...takes].reverse().map((id) => {
              const t = a.takes[id];
              const label = `Take ${n(id)}${t.seed != null && t.seed >= 0 ? ` · seed ${t.seed}` : ""}`;
              return (
                <button
                  key={id}
                  type="button"
                  role="listitem"
                  aria-label={label}
                  className={`flex items-center gap-2 rounded-[10px] bg-s2 px-2 py-1.5 text-left text-[12px] ${picked === id ? "shadow-[inset_0_0_0_1px_var(--a1)]" : ""}`}
                  aria-current={picked === id}
                  onClick={() => setPicked(id)}
                  onContextMenu={(e) =>
                    openMenu(
                      e,
                      takeMenu(a, b, id, n(id), () => setPicked(id)),
                    )
                  }
                >
                  <span
                    className="h-6 w-10 shrink-0 rounded bg-s3 bg-cover"
                    style={{ backgroundImage: t.art }}
                  />
                  <b className="min-w-0 grow truncate">{label}</b>
                  {b.pinned === id && <Pin size={12} aria-label="pinned" />}
                </button>
              );
            })}
          </div>
        </section>
      </div>
      {changed && (
        <div className="flex items-center gap-2">
          <span className="grow" />
          <button
            type="button"
            className="h-9 rounded-[10px] px-3.5 text-[13px] font-medium text-t2"
            onClick={close}
          >
            Save and close
          </button>
        </div>
      )}
    </div>
  );
}
