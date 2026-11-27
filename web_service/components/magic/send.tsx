"use client";
// Send to Timeline (MagicBoard frame 9; plan 39 phase G, API 1.82): the dialog (what goes, the joins kept or baked,
// the effects, the warnings, the bake estimate), the sends list with each one's revision and its right-click menu, the
// toast while a send bakes and once it is sent, and the morph (frame 4b): its block over its join and its dialog.
import Link from "next/link";
import { useCallback, useEffect, useState } from "react";
import { Clapperboard, Clock } from "lucide-react";
import { openMenu } from "@/components/context-menu";
import { ErrorNote, Modal } from "@/components/kit";
import { DissolveIcon, FadeIcon, KIND_NAME, MorphIcon } from "@/components/magic/icons";
import { LENGTHS, joinMenu, sendMenu, type Act } from "@/components/magic/menus";
import { api, ApiError, message, ok, type S } from "@/lib/api";
import {
  joins,
  secs,
  type Doc,
  type Lane,
  type MagicRequest,
  type Takes,
  type Transition,
} from "@/lib/magic";
import { estText } from "@/lib/magic-fixture";
import type { Estimate, Kinds } from "@/lib/magic-standard";

export type Send = S<"MagicSend">;
export type SendPlan = S<"MagicSendPlan">;

/** What the page sends through: studio's routes for one board (null in the fake mode). */
export type SendApi = {
  /** the board's id: a new SendApi each render is the same one */
  key: string;
  /** the dry run: what a send would make, nothing stored */
  plan: () => Promise<SendPlan>;
  send: (name: string) => Promise<Send>;
  list: () => Promise<Send[]>;
  remove: (id: string) => Promise<void>;
};

const web = { "X-Helm-Client": "web" };
/** The Timeline's 409 at 100 sequences (its own words). */
const FULL = /You have \d+ sequences/;

/** Studio's sends of board `id` at `revision` (the board as the page has it). */
export function studioSends(id: string, revision: number): SendApi {
  const path = { params: { path: { board_id: id } } };
  return {
    key: id,
    plan: async () =>
      (
        await ok(
          api.POST("/v1/studio/magic/boards/{board_id}/send", {
            ...path,
            headers: web,
            body: { revision, dry_run: true },
          }),
        )
      ).plan,
    send: async (name) => {
      const r = await ok(
        api.POST("/v1/studio/magic/boards/{board_id}/send", {
          ...path,
          headers: web,
          body: { revision, name },
        }),
      );
      return r.send!;
    },
    list: async () =>
      (await ok(api.GET("/v1/studio/magic/boards/{board_id}/sends", path))).sends ?? [],
    remove: async (send) => {
      await ok(
        api.DELETE("/v1/studio/magic/boards/{board_id}/sends/{send_id}", {
          params: { path: { board_id: id, send_id: send } },
        }),
      );
    },
  };
}

export const timelineHref = (id: string) => `/timeline?t=${encodeURIComponent(id)}`;

/** "now", "10:48", "yesterday", "26 Nov": when a send was made. */
export function when(at: string, now = new Date()): string {
  const t = new Date(at);
  if (now.getTime() - t.getTime() < 60_000) return "now";
  const day = (d: Date) => new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();
  const days = Math.round((day(now) - day(t)) / 86_400_000);
  if (days === 0) return t.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  if (days === 1) return "yesterday";
  return t.toLocaleDateString([], { day: "numeric", month: "short" });
}

const NOUN: Record<string, [string, string]> = {
  video: ["shot", "shots"],
  voice: ["line", "lines"],
  music: ["cue", "cues"],
  sound: ["sound", "sounds"],
  audio: ["sound", "sounds"],
};
const BADGE: Record<string, keyof typeof KIND_NAME> = {
  video: "video",
  voice: "voice",
  music: "music",
  sound: "sound",
  audio: "sound",
};
const JOIN_NAME: Record<string, string> = {
  dissolve: "Dissolve",
  fade: "Fade",
  wipe: "Wipe",
  morph: "Morph",
  fade_in: "Fade in",
  fade_out: "Fade out",
};
const HOW: [S<"MagicSendJoin">["how"], string][] = [
  ["kept", "kept as they are"],
  ["baked", "baked into a short clip"],
  ["clip", "its own clip"],
  ["cut", "sent as a cut"],
  ["skipped", "not sent"],
];

/** What a plan says goes, row by row (frame 9's Goes). */
export function goesRows(p: SendPlan): { kind: string; text: string; note: string }[] {
  return p.goes.map((g) => {
    const [one, many] = NOUN[g.kind] ?? ["block", "blocks"];
    return {
      kind: BADGE[g.kind] ?? "sound",
      text: `${g.blocks} ${g.blocks === 1 ? one : many} on ${g.track}`,
      note:
        g.kind === "video"
          ? "picked takes"
          : g.linked
            ? g.linked === g.blocks
              ? "linked to their shots"
              : `${g.linked} linked to their shots`
            : "",
    };
  });
}

/** The joins grouped by what happens to them (frame 9's Joins): "Dissolve, Cut" kept as they are. */
export function joinRows(p: SendPlan, cuts: number): { how: string; names: string; note: string }[] {
  const out: { how: string; names: string; note: string }[] = [];
  for (const [how, note] of HOW) {
    const names = [...new Set(p.joins.filter((j) => j.how === how).map((j) => JOIN_NAME[j.kind]))];
    if (how === "kept" && cuts) names.push("Cut");
    if (names.length) out.push({ how, names: names.join(", "), note });
  }
  return out;
}

/** Frame 9's icon per row of Joins: a bowtie kept, a half bowtie baked, the sparkle a morph, a pill the effects. */
const JOIN_HOW_ICON: Record<string, (p: { size: number }) => React.ReactNode> = {
  kept: DissolveIcon,
  baked: FadeIcon,
  clip: MorphIcon,
};
const EffectIcon = () => (
  <svg
    aria-hidden
    width={14}
    height={14}
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    strokeWidth="2.2"
    strokeLinecap="round"
    strokeLinejoin="round"
    style={{ flex: "0 0 auto" }}
  >
    <rect x="3" y="7" width="18" height="10" rx="5" />
  </svg>
);

/** The cuts V1 keeps: joins on the board's first Shots lane that have no transition. */
function cutsOf(d: Doc): number {
  const shots = d.lanes.find((l) => l.role === "shots");
  return shots ? joins(d, shots.id).filter((j) => j.kind === "cut" && j.between).length : 0;
}

/** Send to Timeline: the key button and its dialog. `onSent` gets the send it made. */
export function SendButton({
  doc,
  sends,
  onSent,
}: {
  doc: Doc;
  sends: SendApi | null;
  onSent: (s: Send) => void;
}) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <button
        type="button"
        className="key flex h-12 flex-col items-center justify-center gap-px rounded-xl px-5 text-sm"
        disabled={!sends}
        title={sends ? "Make a new sequence from the board" : "Sends need a board in studio"}
        onClick={() => setOpen(true)}
      >
        <span className="max-md:hidden">Send to Timeline</span>
        <span className="md:hidden">Send</span>
        <small className="mono text-[11px] font-medium tracking-normal max-md:hidden">
          new sequence
        </small>
      </button>
      {sends && (
        <SendDialog
          open={open}
          doc={doc}
          sends={sends}
          onClose={() => setOpen(false)}
          onSent={(s) => {
            setOpen(false);
            onSent(s);
          }}
        />
      )}
    </>
  );
}

function SendDialog({
  open,
  doc,
  sends,
  onClose,
  onSent,
}: {
  open: boolean;
  doc: Doc;
  sends: SendApi;
  onClose: () => void;
  onSent: (s: Send) => void;
}) {
  return (
    <Modal
      open={open}
      onClose={onClose}
      title="Send to Timeline"
      aside={<span className="text-[12px] text-t2">a new sequence</span>}
      width="w-[min(96vw,560px)]"
    >
      {open && <SendBody doc={doc} sends={sends} onClose={onClose} onSent={onSent} />}
    </Modal>
  );
}

/** The dialog's body: studio's dry run of the board as it is now, then Send. */
function SendBody({
  doc,
  sends,
  onClose,
  onSent,
}: {
  doc: Doc;
  sends: SendApi;
  onClose: () => void;
  onSent: (s: Send) => void;
}) {
  const [plan, setPlan] = useState<SendPlan | null>(null);
  const [name, setName] = useState("");
  const [error, setError] = useState("");
  const [full, setFull] = useState(false);
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    let gone = false;
    sends.plan().then(
      (p) => {
        if (gone) return;
        setPlan(p);
        setName(p.name);
      },
      (e) => {
        if (gone) return;
        if (e instanceof ApiError && e.status === 409 && FULL.test(e.message)) setFull(true);
        else setError(message(e));
      },
    );
    return () => {
      gone = true;
    };
    // once, as it opens (the board as it is now); a new SendApi each render is the same board
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sends.key]);
  const go = async () => {
    setBusy(true);
    setError("");
    try {
      onSent(await sends.send(name.trim() || plan!.name));
    } catch (e) {
      if (e instanceof ApiError && e.status === 409 && FULL.test(e.message)) setFull(true);
      else setError(message(e));
    } finally {
      setBusy(false);
    }
  };
  const bakes = plan?.bakes ?? 0;
  return (
    <div className="mg-side flex flex-col gap-2.5">
      {full && <Full />}
      {error && <ErrorNote>{error}</ErrorNote>}
      {!plan && !full && !error && <p className="text-[13px] text-t2">Looking at the board…</p>}
      {plan && (
        <>
          <div className="grid gap-2.5 sm:grid-cols-2">
            <label className="flex flex-col gap-1 text-[12px] text-t2">
              Name
              <input
                className="in h-9 px-2.5 text-[13px] text-t1"
                value={name}
                maxLength={80}
                onChange={(e) => setName(e.target.value)}
              />
            </label>
            <div className="flex flex-col gap-1 text-[12px] text-t2">
              From
              <span className="in mono flex h-9 items-center px-2.5 text-[13px] text-t1">
                rev {plan.revision} · now
              </span>
            </div>
          </div>
          <h3 className="m-0 mt-1 text-[13px]">Goes</h3>
          {goesRows(plan).map((g, i) => (
            <div key={i} className="nr">
              <span className={`kb k-${g.kind}`}>
                {KIND_NAME[g.kind as keyof typeof KIND_NAME]}
              </span>
              {g.text}
              {g.note && <span className="mono">{g.note}</span>}
            </div>
          ))}
          {!plan.goes.length && <span className="text-[12px] text-t2">Nothing has a take yet</span>}
          {(plan.joins.length > 0 || plan.effects.length > 0 || cutsOf(doc) > 0) && (
            <h3 className="m-0 mt-1 text-[13px]">Joins</h3>
          )}
          {joinRows(plan, cutsOf(doc)).map((j) => (
            <div key={j.note} className="nr">
              {JOIN_HOW_ICON[j.how]?.({ size: 14 })}
              {j.names}
              <span className="mono">{j.note}</span>
            </div>
          ))}
          {plan.effects.map((e, i) => (
            <div key={`${e.block}-${i}`} className="nr">
              <EffectIcon />
              Effects · {e.name.split(" · ")[0]} on{" "}
              {doc.blocks.find((b) => b.id === e.block)?.name.split(" · ")[0] ?? e.block}
              <span className="mono">
                {e.how === "baked" ? "baked into its clip" : "not baked"}
              </span>
            </div>
          ))}
          {plan.warnings.map((w) => {
            const [head, rest] = w.split(": ");
            return (
              <div key={w} className="warn" role="note">
                <b>{head}</b>
                {rest && <span>{rest}</span>}
              </div>
            );
          })}
          <p className="m-0 text-[12px] text-t2">
            Storyboard frames and assets stay here. The board stays as it is.
          </p>
        </>
      )}
      <div className="flex flex-wrap items-center gap-2">
        {plan && bakes > 0 && (
          <span className="est" title="How long the bakes take on the media host">
            <Clock size={13} aria-hidden />
            <b>
              Bake {bakes} {bakes === 1 ? "join" : "joins"}
              {plan.bake_seconds != null ? ` · ${estText(plan.bake_seconds, true)}` : ""}
            </b>
            {plan.host ? ` · ${plan.host}` : ""}
          </span>
        )}
        <span className="grow" />
        <button type="button" className="chip bevel rounded-[10px] bg-s4" onClick={onClose}>
          Cancel
        </button>
        <button
          type="button"
          className="key h-9 rounded-xl px-4 text-[13px]"
          disabled={!plan || full || busy}
          onClick={() => void go()}
        >
          {busy ? "Sending…" : "Send"}
        </button>
      </div>
    </div>
  );
}

/** At 100 sequences (frame 9). */
function Full() {
  return (
    <div className="warn" role="alert">
      <b>100 sequences is the most</b>
      <span>Delete one in Timeline to send again</span>
    </div>
  );
}

/** The sends of a board: a list that follows a baking one until it is done. */
export function useSends(sends: SendApi | null) {
  const [list, setList] = useState<Send[]>([]);
  const [tick, setTick] = useState(0);
  const key = sends?.key;
  useEffect(() => {
    if (!sends) return;
    let gone = false;
    sends.list().then(
      (l) => !gone && setList(Array.isArray(l) ? l : []),
      () => undefined,
    );
    return () => {
      gone = true;
    };
    // the board's sends: read again for another board and on reload, not for every render's new SendApi
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key, tick]);
  // a send still baking: read the list again until it is done
  const baking = list.some((s) => s.status === "baking");
  useEffect(() => {
    if (!baking) return;
    const t = setTimeout(() => setTick((n) => n + 1), 1500);
    return () => clearTimeout(t);
  }, [baking, tick]);
  const reload = useCallback(() => setTick((n) => n + 1), []);
  return { list: key ? list : [], reload };
}

/** "Sends · 2": the chip and the list (frame 9). */
export function SendsChip({
  list,
  sends,
  onRestore,
  onChanged,
}: {
  list: Send[];
  sends: SendApi | null;
  onRestore: (revision: number) => void;
  onChanged: () => void;
}) {
  const [open, setOpen] = useState(false);
  const [error, setError] = useState("");
  const act = {
    open: (s: Send) => s.timeline_id && window.location.assign(timelineHref(s.timeline_id)),
    restore: (s: Send) => {
      setOpen(false);
      onRestore(s.revision);
    },
    remove: (s: Send) => sends?.remove(s.id).then(onChanged, (e) => setError(message(e))),
  };
  return (
    <>
      <button
        type="button"
        className="chip bevel rounded-[10px] bg-s4 max-md:hidden"
        disabled={!sends}
        title={sends ? undefined : "Sends need a board in studio"}
        onClick={() => setOpen(true)}
      >
        Sends · {list.length}
      </button>
      <Modal
        open={open}
        onClose={() => setOpen(false)}
        title="Sends"
        aside={<span className="text-[12px] text-t2">{list.length}</span>}
        width="w-[min(94vw,420px)]"
      >
        <div className="mg-side flex flex-col gap-2">
          {error && <ErrorNote>{error}</ErrorNote>}
          {!list.length && <span className="text-[13px] text-t2">Nothing sent yet</span>}
          {list.map((s) => (
            <div key={s.id} className="nr" onContextMenu={(e) => openMenu(e, sendMenu(act, s))}>
              <Clapperboard size={14} aria-hidden />
              <span className="min-w-0 truncate">
                <b>{s.name}</b>
              </span>
              <span className="mono">
                rev {s.revision} ·{" "}
                {s.status === "baking"
                  ? `baking ${s.baked}/${s.bakes}`
                  : s.status === "failed"
                    ? "not sent"
                    : when(s.made_at)}
              </span>
              {s.status === "done" && s.timeline_id && !s.timeline_gone ? (
                <Link
                  className="chip bevel h-7 rounded-[9px] bg-s4 text-[12px]"
                  href={timelineHref(s.timeline_id)}
                >
                  Open
                </Link>
              ) : (
                <button
                  type="button"
                  className="chip bevel h-7 rounded-[9px] bg-s4 text-[12px]"
                  title={s.failure ?? (s.timeline_gone ? "Its sequence was deleted" : undefined)}
                  onClick={(e) => openMenu(e, sendMenu(act, s))}
                >
                  ⋯
                </button>
              )}
            </div>
          ))}
          <p className="m-0 text-[12px] text-t2">
            Each send is its own sequence; editing it there never changes the board.
          </p>
        </div>
      </Modal>
    </>
  );
}

/** The toast while a send bakes and once it is sent (one at a time). */
export function SendToast({ send, onClose }: { send: Send | null; onClose: () => void }) {
  useEffect(() => {
    if (send?.status !== "done" && send?.status !== "failed") return;
    const t = setTimeout(onClose, 10_000);
    return () => clearTimeout(t);
  }, [send, onClose]);
  if (!send) return null;
  return (
    <div
      role="status"
      className="fixed bottom-5 left-1/2 z-50 flex w-[min(92vw,420px)] -translate-x-1/2 items-center gap-2.5 rounded-xl bg-s3 px-3 py-2.5 text-[13px] shadow-lg"
    >
      {send.status === "baking" ? (
        <>
          <span className="inline-block size-2 rounded-full bg-[#f2c14e]" aria-hidden />
          <span className="min-w-0 grow truncate">
            Baking {send.baked}/{send.bakes} · {send.name}
          </span>
        </>
      ) : send.status === "failed" ? (
        <span className="min-w-0 grow">
          Not sent · {send.name}
          {send.failure ? `: ${send.failure}` : ""}
        </span>
      ) : (
        <>
          <span className="min-w-0 grow truncate">Sent · {send.name}</span>
          {send.timeline_id && (
            <Link
              className="chip bevel h-7 rounded-[9px] text-[12px]"
              href={timelineHref(send.timeline_id)}
            >
              Open in Timeline
            </Link>
          )}
        </>
      )}
      <button
        type="button"
        className="chip h-7 rounded-[9px] text-[12px]"
        aria-label="Close"
        onClick={onClose}
      >
        ×
      </button>
    </div>
  );
}

// ------------------------------------------------------------------------------------------------ morphs

/** A morph's state as the board shows it: running (its newest open run), done (a take), failed, draft. */
export function morphStatus(
  t: Transition,
  takes: Takes,
): { s: string; progress?: number; error?: string } {
  const mine = Object.values(takes).filter((x) => x.block === t.id);
  const run = mine.findLast((x) => x.status === "running");
  if (run) return { s: "running", progress: run.progress ?? 0 };
  if (t.takes?.length) return { s: "done" };
  const failed = mine.findLast((x) => x.status === "failed");
  return failed ? { s: "failed", error: failed.error } : { s: "draft" };
}

/** The take a morph shows: its pinned one, else its latest. */
export const morphTake = (t: Transition) => t.pinned ?? t.takes?.at(-1);

/** Each morph of a lane as its own block over its join (frame 4b): its take's picture, its length, its status. */
export function MorphBlocks({ act, lane, pf }: { act: Act; lane: Lane; pf: number }) {
  const d = act.doc;
  return (
    <>
      {joins(d, lane.id)
        .filter((j) => j.kind === "morph" && j.between)
        .map((j) => {
          const t = d.transitions.find((x) => x.between?.join() === j.between!.join());
          if (!t) return null;
          const st = morphStatus(t, act.takes);
          const take = morphTake(t);
          const w = Math.max(j.length * pf, 104);
          const label =
            st.s === "running"
              ? `${st.progress}%`
              : st.s === "done"
                ? "Done"
                : st.s === "failed"
                  ? "Failed"
                  : "Draft";
          return (
            <button
              key={t.id}
              type="button"
              className="absolute top-[62px] z-[3] flex h-[22px] items-center gap-1.5 overflow-hidden rounded-[10px] bg-s3 px-1.5 text-[11px] font-semibold"
              style={{ left: j.at * pf + 16, width: w, boxShadow: "inset 0 0 0 1.5px #ff9ad0" }}
              aria-label={`Morph ${secs(j.length, d.target.fps)}, ${label}`}
              title={st.error ?? `Morph · ${secs(j.length, d.target.fps)}`}
              onClick={() => act.openMorph?.(t.id)}
              onContextMenu={(e) => openMenu(e, joinMenu(act, j))}
            >
              <span
                className="size-4 flex-none rounded-[5px] bg-s4 bg-cover bg-center"
                style={
                  take && act.takes[take]?.art
                    ? { backgroundImage: act.takes[take].art }
                    : undefined
                }
                aria-hidden
              />
              <span className="min-w-0 truncate">Morph</span>
              <span className="mono ml-auto text-[10px] font-medium text-t2">{label}</span>
            </button>
          );
        })}
    </>
  );
}

/** A morph opened (frame 4b): its model (one that takes a first and a last frame), its length, the estimate and
 * Generate; its takes, the shown one outlined, a click pins one. */
export function MorphDialog({
  act,
  id,
  kinds,
  estimate,
  onClose,
}: {
  act: Act;
  id: string | null;
  kinds: Kinds | null;
  estimate?: Estimate;
  onClose: () => void;
}) {
  const d = act.doc;
  const t = id ? d.transitions.find((x) => x.id === id && x.kind === "morph") : undefined;
  const short = (bid: string) => d.blocks.find((b) => b.id === bid)?.name.split(" · ")[0] ?? bid;
  const models = (kinds?.kinds.find((k) => k.kind === "video")?.models ?? []).filter(
    (m) => m.fields.includes("first") && m.fields.includes("last"),
  );
  const fps = d.target.fps;
  const set = (more: { request?: MagicRequest; pinned?: string | null; length?: number }) =>
    t?.between && act.edit({ op: "transition", between: t.between, kind: "morph", ...more });
  const model = t?.request?.model ?? models[0]?.workflow_id;
  const st = t ? morphStatus(t, act.takes) : { s: "draft" };
  return (
    <Modal
      open={!!t}
      onClose={onClose}
      title={t?.between ? `Morph · ${short(t.between[0])} → ${short(t.between[1])}` : "Morph"}
      width="w-[min(94vw,460px)]"
    >
      {t && (
        <div className="mg-side flex flex-col gap-2.5">
          <label className="flex flex-col gap-1 text-[12px] text-t2">
            Model
            <select
              className="in h-9 px-2 text-[13px] text-t1"
              value={model ?? ""}
              disabled={!models.length}
              onChange={(e) =>
                set({
                  request: {
                    ...(t.request ?? { kind: "video" }),
                    kind: "video",
                    model: e.target.value,
                  },
                })
              }
            >
              {models.map((m) => (
                <option key={m.workflow_id} value={m.workflow_id}>
                  {m.name} · first + last
                </option>
              ))}
              {!models.length && <option value="">No model takes a first and a last frame</option>}
            </select>
          </label>
          <div className="flex flex-col gap-1 text-[12px] text-t2">
            Length
            <div className="flex gap-1.5" role="group" aria-label="Length">
              {LENGTHS.map((s) => {
                const f = Math.round(s * fps);
                return (
                  <button
                    key={s}
                    type="button"
                    className={`chip bevel h-8 rounded-[9px] text-[12px] ${t.length === f ? "on" : ""}`}
                    aria-pressed={t.length === f}
                    onClick={() => set({ length: f })}
                  >
                    {s} s
                  </button>
                );
              })}
            </div>
          </div>
          <div className="flex flex-col gap-1 text-[12px] text-t2">
            Takes
            <div className="flex flex-wrap gap-1.5">
              {(t.takes ?? []).map((tid, i) => {
                const shown = morphTake(t) === tid;
                return (
                  <button
                    key={tid}
                    type="button"
                    className="h-12 w-20 rounded-[9px] bg-s4 bg-cover bg-center"
                    style={{
                      backgroundImage: act.takes[tid]?.art,
                      boxShadow: shown ? "0 0 0 2px var(--acc, #ff9ad0)" : undefined,
                    }}
                    aria-pressed={t.pinned === tid}
                    aria-label={`Take ${i + 1}${t.pinned === tid ? ", pinned" : ""}`}
                    title={t.pinned === tid ? "Pinned: click to unpin" : "Pin this take"}
                    onClick={() => set({ pinned: t.pinned === tid ? null : tid })}
                  />
                );
              })}
              {!t.takes?.length && (
                <span className="text-[12px] text-t2">
                  {st.s === "running"
                    ? `Generating… ${st.progress}%`
                    : st.s === "failed"
                      ? `Failed: ${st.error}`
                      : "No takes yet"}
                </span>
              )}
            </div>
          </div>
          <p className="m-0 text-[12px] text-t2">
            From {short(t.between![0])}&rsquo;s last frame to {short(t.between![1])}&rsquo;s first;
            sent as its own clip.
          </p>
          <div className="flex items-center gap-2">
            {estimate?.seconds != null && (
              <span className="est">
                <b>{estText(estimate.seconds, true)}</b>
                {estimate.host ? ` · ${estimate.host}` : ""}
              </span>
            )}
            <span className="grow" />
            <button
              type="button"
              className="key h-9 rounded-xl px-4 text-[13px]"
              disabled={!!act.runOff || !models.length || st.s === "running"}
              title={act.runOff}
              onClick={() => act.run([t.id])}
            >
              Generate
            </button>
          </div>
        </div>
      )}
    </Modal>
  );
}
