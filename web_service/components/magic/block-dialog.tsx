"use client";
// A block opened (MagicBoard frame 3; plan 39 phases D and H: music and sound too): left, its @ references, what is tied to it, notes the
// Assistant also reads and the run history (who ran it, how long); middle, the standard request with Advanced open
// and Generate with its estimate; right, the take on stage, the takes (the picked one outlined, the pinned one
// marked, a running one with its progress) and Pin. Takes and runs have their right-click menus. API 1.89: a
// dialogue's lines with their statuses, Run on, the tie with Unlink, and Compare takes (two side by side).
import { useState } from "react";
import { Columns2, Pin, TriangleAlert } from "lucide-react";
import { useHosts } from "@/components/create/memory-chip";
import { machines, pinnedMachine, runnerWhy } from "@/lib/run-on";
import { DOWN_KEY, useHealth } from "@/lib/health";
import { failWords } from "@/lib/runs";
import { openMenu } from "@/components/context-menu";
import { Modal } from "@/components/kit";
import { KIND_NAME, LinkGlyph } from "@/components/magic/icons";
import { StatusChip, refText } from "@/components/magic/lanes";
import { runMenu, takeMenu, type Act } from "@/components/magic/menus";
import { EstimateLine, RequestForm, useVariant } from "@/components/magic/request";
import { SoundMark, soundLook } from "@/components/magic/sound-mark";
import { estText } from "@/lib/magic-fixture";
import {
  clock,
  end,
  shown,
  turnStatuses,
  type Block,
  type MagicRequest,
  type Ref,
  type TakeInfo,
} from "@/lib/magic";
import {
  FORM_KINDS,
  madeBy,
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
      heading={block && <KindTitle block={block} />}
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

/** A dialog's title as the board draws it: the kind's badge, then the name (MagicBoard 2c, 3). */
export function KindTitle({ block: b }: { block: Pick<Block, "kind" | "name" | "request"> }) {
  return (
    <span className="mg-title flex items-center gap-2">
      <span className={`kb k-${b.kind}`}>{b.request.turns ? "Dialogue" : KIND_NAME[b.kind]}</span>
      {b.name}
    </span>
  );
}

/** A take on a stage: its player (a video, or an audio take's cover or orb over its player) or its picture. */
function Stage({ b, t, n }: { b: Block; t?: TakeInfo; n: number }) {
  const look = t ? soundLook(b, t) : null;
  return (
    <div
      className="flex aspect-video items-end overflow-hidden rounded-xl bg-cover bg-center p-2"
      style={{ backgroundColor: "var(--s3)", backgroundImage: look ? undefined : t?.art }}
    >
      {t?.kind === "video" && t.url ? (
        <video
          src={t.url}
          controls
          className="h-full w-full object-contain"
          aria-label={`Take ${n}`}
        />
      ) : t?.kind === "audio" && t.url ? (
        // plan 39 phase H: the take's cover or orb over its player
        <span className="flex w-full flex-col items-center gap-2">
          {look && <SoundMark look={look} art={t.art} large />}
          <audio src={t.url} controls aria-label={`Take ${n}`} className="w-full" />
        </span>
      ) : (
        !t && <span className="text-[12px] text-t2">No take yet</span>
      )}
    </div>
  );
}

/** Compare takes (MagicBoard frames 3 and 10, API 1.89): two of a block's takes side by side, each picked from its
 * takes, with Pin. */
export function CompareDialog({
  act: a,
  block: b,
  onClose,
}: {
  act: Act;
  block: Block | null;
  onClose: () => void;
}) {
  return (
    <Modal
      open={!!b}
      onClose={onClose}
      title={b ? `Compare takes · ${b.name}` : "Compare takes"}
      width="w-[min(96vw,980px)]"
    >
      {b && <CompareBody key={b.id} a={a} b={b} />}
    </Modal>
  );
}

function CompareBody({ a, b }: { a: Act; b: Block }) {
  const done = b.takes.filter((id) => a.takes[id]?.status === "done");
  const shownId = shown(b, a.takes)?.id;
  // the shown take, and the newest other one
  const [pair, setPair] = useState<[string, string]>(() => {
    const first = shownId ?? done.at(-1) ?? "";
    return [first, done.findLast((id) => id !== first) ?? first];
  });
  const n = (id: string) => b.takes.indexOf(id) + 1;
  return (
    <div className="mg-add grid gap-4 md:grid-cols-2">
      {pair.map((id, side) => (
        <section
          key={side}
          className="flex min-w-0 flex-col gap-2"
          aria-label={side ? "Right" : "Left"}
        >
          <select
            aria-label={side ? "Right take" : "Left take"}
            className="in h-8 px-2 text-[13px]"
            value={id}
            onChange={(e) =>
              setPair((p) => (side ? [p[0], e.target.value] : [e.target.value, p[1]]))
            }
          >
            {done.map((t) => {
              const seed = a.takes[t]?.seed;
              return (
                <option key={t} value={t}>
                  Take {n(t)}
                  {seed != null && seed >= 0 ? ` · seed ${seed}` : ""}
                  {b.pinned === t ? " · pinned" : ""}
                </option>
              );
            })}
          </select>
          <div
            onContextMenu={(e) =>
              openMenu(
                e,
                takeMenu(a, b, id, n(id), () => undefined),
              )
            }
          >
            <Stage b={b} t={a.takes[id]} n={n(id)} />
          </div>
          <button
            type="button"
            className="chip bevel self-start rounded-[10px] bg-s4"
            disabled={b.pinned === id}
            onClick={() => a.edit({ op: "block", id: b.id, set: { pinned: id } })}
          >
            <Pin size={13} aria-hidden />
            {b.pinned === id ? `Take ${n(id)} is pinned` : `Pin take ${n(id)}`}
          </button>
        </section>
      ))}
    </div>
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
  const [chosen, setPicked] = useState<string | undefined>(shown(b, a.takes)?.id);
  // a take that arrives while the dialog is open shows too
  const picked = chosen ?? shown(b, a.takes)?.id;
  const stage = picked ? a.takes[picked] : undefined;
  const n = (id?: string) => (id ? b.takes.indexOf(id) + 1 : 0);
  const model = modelOf(kinds, req);
  // why Generate is greyed, a visible line under it (docs/mock/ErrorStates.dc.html b2): the studio down, no model of
  // the kind on any host, no runner for its env (the hosts now) or its Run on host offline
  const hosts = useHosts();
  const down = useHealth().down;
  const why = down
    ? null
    : !model
      ? { key: "No model", line: "No model for this kind on any host. Download one in Models." }
      : runnerWhy(hosts, model.env, pinnedMachine(machines(hosts), req.host ?? ""));
  const form = FORM_KINDS.includes(b.kind);
  const refs: { r: Ref; why: string }[] = [
    ...(req.first ? [{ r: req.first, why: "first frame of this shot" }] : []),
    ...(req.last ? [{ r: req.last, why: "last frame of this shot" }] : []),
    ...(req.refs ?? []).map((r) => ({ r, why: "a reference" })),
  ];
  const tied = d.links.filter((l) => l.block === b.id || l.to.includes(b.id));
  const link = d.links.find((l) => l.block === b.id);
  const seconds = (changed ? undefined : estimate?.seconds) ?? undefined;
  const variant = useVariant(model, req).title;
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
        <StatusChip d={d} b={b} takes={a.takes} onStop={a.stop && (() => a.stop!([b.id]))} />
        <span className="grow" />
        {takes.length > 1 && a.compare && !b.request.turns && (
          <button
            type="button"
            className="chip bevel rounded-[10px] bg-s4"
            onClick={() => {
              save();
              a.compare!(b.id);
            }}
          >
            <Columns2 size={13} aria-hidden />
            Compare
          </button>
        )}
        {picked && b.pinned !== picked && !b.request.turns && (
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
                      // ErrorStates c2: a failed run's plain words (the raw text in its menu's details)
                      title={
                        r.status === "failed"
                          ? `${failWords(r.error)} ${r.error ?? ""}`.trim()
                          : undefined
                      }
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
                runOn={!!a.gallery} // in studio: the fake mode has no hosts
                turnStatuses={
                  req.turns ? turnStatuses(d, { ...b, request: req }, a.takes) : undefined
                }
                tie={
                  link && (b.kind === "voice" || b.kind === "music")
                    ? { to: link.to, onUnlink: () => a.edit({ op: "unlink", block: b.id }) }
                    : null
                }
              />
              <div className="flex items-center gap-2">
                <EstimateLine
                  est={changed ? undefined : estimate}
                  model={model}
                  block={{ ...b, request: req }}
                  doc={d}
                  variant={variant}
                />
                <span className="grow" />
                <button
                  type="button"
                  className="key h-9 rounded-xl px-3.5 text-[13px]"
                  disabled={!!a.runOff || !model || !!why || down}
                  title={a.runOff}
                  onClick={generate}
                >
                  Generate
                  {down || (why && model) ? (
                    <small className="ml-1.5 font-normal opacity-75">
                      {down ? DOWN_KEY : why!.key}
                    </small>
                  ) : (
                    /* "≈ 1:50" from your own recent runs (MagicBoard frame 3) */
                    seconds != null && (
                      <small className="ml-1.5 font-normal opacity-75">
                        {estText(seconds, true)}
                      </small>
                    )
                  )}
                </button>
              </div>
              {why && !a.runOff && (
                <p className="why" role="status">
                  <TriangleAlert size={14} aria-hidden />
                  {why.line}
                </p>
              )}
            </>
          ) : (
            <p className="m-0 text-[13px] text-t2">
              {kinds?.kinds.find((k) => k.kind === b.kind)?.off ?? ""}
            </p>
          )}
        </section>
        <section className="flex min-w-0 flex-col gap-2.5" aria-label="Takes">
          <Stage b={b} t={stage} n={n(picked)} />
          {stage && (
            <span className="text-[12px] text-t2">
              Take {n(picked)}
              {b.pinned === picked ? " · pinned" : ""}
              {/* the model line (API 1.94): what made it, "LTX 2.5 · Distilled · seed 43" (MagicBoard 3) */}
              {madeBy(kinds, runs, picked!) && ` · ${madeBy(kinds, runs, picked!)}`}
              {stage.seed != null && stage.seed >= 0 ? ` · seed ${stage.seed}` : ""}
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
              const label = `Take ${n(id)}${t.seed != null && t.seed >= 0 ? ` · seed ${t.seed}` : ""}${t.variant ? ` · ${t.variant}` : ""}`;
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
                  {soundLook(b, t) ? (
                    <SoundMark look={soundLook(b, t)!} art={t.art} />
                  ) : (
                    <span
                      className="h-6 w-10 shrink-0 rounded bg-s3 bg-cover"
                      style={{ backgroundImage: t.art }}
                    />
                  )}
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
