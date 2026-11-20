"use client";
// The Timeline screen (docs/mock/Timeline.dc.html, its dialogs in TimelineDialogs.dc.html; plan 11-timeline 15): the
// sequence bar (switcher, size · rate · length, cast and look defaults, undo / redo, New sequence, Export with its
// plan), the player (preview across cuts, nothing rendered), New shot docked, the lanes and the clip inspector.
// Every edit is lib/timeline's `apply` drawn at once, then a PUT with the revision it was made on; a 409 reloads,
// says so and re-applies the edit to the new revision once.
import { useRouter } from "next/navigation";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Pause, Play } from "lucide-react";
import { ErrorNote, Info, Modal } from "@/components/kit";
import { ActionsNote, ClipActions } from "@/components/timeline/actions";
import { markRange, rangeInTake, removeTrack } from "@/lib/source";
import { ElementsPicker } from "@/components/create/elements";
import { LooksPicker } from "@/components/create/looks";
import { GalleryPicker } from "@/components/gallery/picker";
import { Lanes, clipText, shotName } from "@/components/timeline/lanes";
import { ShotComposer, type ShotSlot } from "@/components/timeline/shot";
import { lastTimeline, rememberTimeline } from "@/components/timeline/add";
import {
  BatchLink,
  Export,
  NewSequence,
  Rerender,
  Sequences,
  planLine,
} from "@/components/timeline/dialogs";
import { ApiError, api, message, ok, type S } from "@/lib/api";
import { safeUrl } from "@/lib/format";
import { useLibrary, useLooks } from "@/lib/library";
import { MAX_LOOKS, chosenFrom, type Chosen } from "@/lib/looks";
import { followSession } from "@/lib/runs";
import { Preview } from "@/lib/timeline-preview";
import {
  Grid,
  apply,
  clipAt,
  factsOf,
  frameAt,
  kindOf,
  newId,
  shortTime,
  span,
  timecode,
  writable,
  type Clip,
  type Norm,
  type Op,
  type Problem,
  type Track,
  shotBusy,
} from "@/lib/timeline";

type Timeline = S<"Timeline">;

/** What a refused edit says (lib/timeline codes; studio's 422 carries its own messages). */
const WORDS: Record<string, string> = {
  split_none: "No clip is under the playhead.",
  split_edge: "The playhead is on a cut: move it into a clip to split.",
  split_shot: "A clip being generated cannot be split.",
  too_many_tracks: "A sequence has at most 8 audio tracks.",
  not_alternative: "That take was not made for this slot.",
  dissolve_first: "The first clip cannot dissolve in.",
  dissolve_gap: "A dissolve needs a clip on both sides of the cut.",
  dissolve_handles: "Not enough footage past the cut for that dissolve: trim the clips first.",
  dissolve_length: "The dissolve is longer than the clip.",
  overlap: "Clips on an audio track cannot overlap.",
  past_end: "Sound cannot run past the end of the picture.",
  trim: "That trim is outside the take.",
  hold: "A still lasts at least one frame.",
  no_audio: "That take has no sound.",
};
const say = (code: string) => WORDS[code] ?? `The edit was refused (${code.replace(/_/g, " ")}).`;
const sayProblems = (p: Problem[]) => say(p[0][0]);

/** The editor; `id` is the sequence to open (else the one opened last, else the newest). */
export function TimelineEditor({ id }: { id?: string }) {
  const router = useRouter();
  const [tl, setTl] = useState<Timeline | null>(null);
  const [none, setNone] = useState(false);
  const [draft, setDraft] = useState<Norm | null>(null);
  const [dragOp, setDragOp] = useState<Op | null>(null);
  const [frame, setFrame] = useState(0);
  const [playing, setPlaying] = useState(false);
  const [unplayable, setUnplayable] = useState<string[]>([]);
  const [sel, setSel] = useState<string[]>([]);
  const [track, setTrack] = useState<string | null>(null);
  const [pps, setPps] = useState(40);
  const [notice, setNotice] = useState<React.ReactNode>("");
  const [error, setError] = useState("");
  const [dialog, setDialog] = useState<
    "list" | "new" | "export" | "rerender" | "cast" | "look" | null
  >(null);
  const [picker, setPicker] = useState<{ track: string } | null>(null);
  const [shot, setShot] = useState<ShotSlot | null>(null);
  const [replacing, setReplacing] = useState(false);
  const [workflows, setWorkflows] = useState<S<"Workflow">[]>([]);
  const [runProgress, setRunProgress] = useState<Record<string, string>>({});
  const [plan, setPlan] = useState<S<"ExportPlan"> | null>(null);
  const [recent, setRecent] = useState<S<"Take">[]>([]);
  // the range marked on the sequence (Mark in / Mark out, sequence seconds): what a take action works on
  const [range, setRange] = useState<[number, number] | null>(null);
  // the audio track whose removal waits for the confirm (it has clips)
  const [removing, setRemoving] = useState<string | null>(null);
  const library = useLibrary();
  const looks = useLooks();

  const tlRef = useRef<Timeline | null>(null);
  useEffect(() => {
    tlRef.current = tl;
  }, [tl]);
  const queue = useRef<Promise<void>>(Promise.resolve());
  const pending = useRef(0);
  const pv = useRef<Preview | null>(null);

  // ------------------------------------------------------------------ loading
  const open = useCallback(async (want?: string) => {
    try {
      let tid = want;
      if (!tid) {
        const { timelines } = await ok(api.GET("/v1/studio/timelines"));
        tid = (timelines.find((t) => t.id === lastTimeline()) ?? timelines[0])?.id;
      }
      if (!tid) {
        setTl(null);
        setNone(true);
        return;
      }
      const t = await ok(
        api.GET("/v1/studio/timelines/{timeline_id}", { params: { path: { timeline_id: tid } } }),
      );
      rememberTimeline(t.id);
      tlRef.current = t;
      setError("");
      setNone(false);
      setTl(t);
      setDraft(null);
      setSel([]);
      setTrack(null);
      setShot(null);
      pv.current?.seek(0);
    } catch (e) {
      setError(message(e));
    }
  }, []);
  // every state change of `open` comes after an await: a then keeps the effect itself free of them
  useEffect(() => void Promise.resolve(id).then(open), [id, open]);
  const goTo = (tid: string) => {
    router.replace(`/timeline?t=${encodeURIComponent(tid)}`);
    void open(tid);
  };

  useEffect(() => {
    ok(api.GET("/v1/studio/workflows"))
      .then((r) => setWorkflows(r.workflows))
      .catch(() => undefined);
    ok(api.GET("/v1/studio/takes", { params: { query: { limit: 3 } } }))
      .then((r) => setRecent(r.takes.filter((x) => x.kind !== "audio")))
      .catch(() => undefined);
  }, []);

  const reload = useCallback(async () => {
    const cur = tlRef.current;
    if (!cur) return;
    const t = await ok(
      api.GET("/v1/studio/timelines/{timeline_id}", { params: { path: { timeline_id: cur.id } } }),
    );
    if (!pending.current) setTl(t);
  }, []);

  // ------------------------------------------------------------------ the document shown
  const facts = useMemo(() => (tl ? factsOf(tl.takes) : {}), [tl]);
  const base: Norm | null = draft ?? tl;
  const doc: Norm | null = useMemo(() => {
    if (!base || !dragOp) return base;
    const r = apply(base, dragOp, facts);
    return "document" in r ? r.document : base;
  }, [base, dragOp, facts]);
  const g = doc ? new Grid(doc.target) : null;
  const urls = useMemo(
    () => Object.fromEntries(Object.entries(tl?.takes ?? {}).map(([k, t]) => [k, safeUrl(t.url)])),
    [tl],
  );
  useEffect(() => {
    if (doc) pv.current?.setDoc(doc, facts, urls);
  }, [doc, facts, urls]);
  const stage = useCallback((el: HTMLDivElement | null) => {
    if (el && !pv.current)
      pv.current = new Preview(el, setFrame, (s) => {
        setPlaying(s.playing);
        setUnplayable(s.unplayable);
      });
    if (!el) {
      pv.current?.destroy();
      pv.current = null;
    }
  }, []);

  // ------------------------------------------------------------------ edits
  /** PUT `op` applied to the latest stored revision; a 409 reloads and tries once more on the new one. */
  const save = useCallback(
    async (op: Op): Promise<void> => {
      for (let attempt = 0; attempt < 2; attempt++) {
        const cur = tlRef.current;
        if (!cur) return;
        const r = apply(cur, op, factsOf(cur.takes));
        if (!("document" in r)) return; // the clip it addressed is gone: the edit is dropped
        try {
          const t = await ok(
            api.PUT("/v1/studio/timelines/{timeline_id}", {
              params: { path: { timeline_id: cur.id } },
              body: { revision: cur.revision, ...writable(r.document) } as S<"TimelineUpdate">,
            }),
          );
          tlRef.current = t;
          setTl(t);
          return;
        } catch (e) {
          if (e instanceof ApiError && e.status === 409 && !attempt) {
            const t = await ok(
              api.GET("/v1/studio/timelines/{timeline_id}", {
                params: { path: { timeline_id: cur.id } },
              }),
            );
            tlRef.current = t;
            setTl(t);
            setNotice(
              "The sequence changed since you opened it: reloaded, and your edit applied to it.",
            );
            continue;
          }
          setError(message(e));
          await reload().catch(() => undefined);
          return;
        }
      }
    },
    [reload],
  );

  const commit = useCallback(
    (op: Op) => {
      const b = draft ?? tlRef.current;
      if (!b) return;
      const r = apply(b, op, facts);
      if ("error" in r) return setNotice(say(r.error));
      if ("problems" in r) return setNotice(sayProblems(r.problems));
      setNotice("");
      setError("");
      setDraft(r.document);
      pending.current++;
      queue.current = queue.current.then(async () => {
        await save(op);
        if (!--pending.current) setDraft(null);
      });
    },
    [draft, facts, save],
  );

  /** A server-side change (undo, redo, add clips…) after the edits in flight. */
  const server = useCallback(
    (f: (t: Timeline) => Promise<Timeline>) => {
      queue.current = queue.current.then(async () => {
        const cur = tlRef.current;
        if (!cur) return;
        try {
          const t = await f(cur);
          tlRef.current = t;
          setTl(t);
          setDraft(null);
        } catch (e) {
          if (e instanceof ApiError && e.status === 409) {
            await reload().catch(() => undefined);
            setNotice("The sequence changed since you opened it: reloaded.");
          } else setError(message(e));
        }
      });
    },
    [reload],
  );

  const undo = (redo = false) =>
    server((t) =>
      ok(
        api.POST(
          redo
            ? "/v1/studio/timelines/{timeline_id}/redo"
            : "/v1/studio/timelines/{timeline_id}/undo",
          {
            params: { path: { timeline_id: t.id } },
            body: { revision: t.revision },
          },
        ),
      ),
    );
  const addClips = (takeIds: string[], to = "V1") =>
    takeIds.length &&
    server((t) =>
      ok(
        api.POST("/v1/studio/timelines/{timeline_id}/clips", {
          params: { path: { timeline_id: t.id } },
          body: { take_ids: takeIds, ...(to !== "V1" && { track: to }) },
        }),
      ),
    );
  const patch = (body: S<"TimelinePatch">) =>
    tl &&
    ok(
      api.PATCH("/v1/studio/timelines/{timeline_id}", {
        params: { path: { timeline_id: tl.id } },
        body,
      }),
    )
      .then((t) => setTl((cur) => (cur ? { ...cur, name: t.name, defaults: t.defaults } : t)))
      .catch((e) => setError(message(e)));

  // ------------------------------------------------------------------ shots: progress and fills
  const shots = (tl?.tracks[0].clips ?? []).filter(shotBusy);
  const live = shots.length > 0;
  useEffect(() => {
    if (!live) return;
    const t = setInterval(() => void reload().catch(() => undefined), 3000);
    return () => clearInterval(t);
  }, [live, reload]);
  const session = tl?.session_id;
  useEffect(() => {
    if (!session || !live) return;
    return followSession(session, (e) => {
      if (e.type === "progress")
        setRunProgress((p) => ({
          ...p,
          [e.run_id]: `${e.data.phase ?? "Sample"} ${e.data.step} / ${e.data.of}`,
        }));
      if (e.type === "status" && ["succeeded", "failed", "cancelled"].includes(e.data.status))
        void reload().catch(() => undefined);
    });
  }, [session, live, reload]);
  const progress = useMemo(
    () =>
      Object.fromEntries(
        shots.flatMap((c) =>
          c.shot?.run_id && runProgress[c.shot.run_id] ? [[c.id, runProgress[c.shot.run_id]]] : [],
        ),
      ),
    [shots, runProgress],
  );

  // ------------------------------------------------------------------ the export plan (the Export key's line)
  const rev = tl?.revision;
  const tid = tl?.id;
  useEffect(() => {
    if (!tid) return;
    const t = setTimeout(() => {
      ok(
        api.POST("/v1/studio/timelines/{timeline_id}/export/plan", {
          params: { path: { timeline_id: tid } },
          body: { revision: rev },
        }),
      )
        .then(setPlan)
        .catch(() => undefined);
    }, 400);
    return () => clearTimeout(t);
  }, [tid, rev]);

  // ------------------------------------------------------------------ selection and helpers
  const all = doc?.tracks.flatMap((t, ti) => t.clips.map((c) => ({ c, ti, t }))) ?? [];
  const selected = all.filter((x) => sel.includes(x.c.id));
  const one = selected.length === 1 ? selected[0] : null;
  const v1 = doc?.tracks[0].clips ?? [];
  const at = g ? g.s(frame) : 0;

  function splitHere() {
    if (!doc || !g) return;
    const onAudio =
      one &&
      one.ti > 0 &&
      span(one.c, one.ti, facts, g)[0] <= at &&
      at < span(one.c, one.ti, facts, g)[1];
    commit({ op: "split", track: onAudio ? one.t.name! : "V1", at, id: newId(doc) });
  }
  function nudge(dir: -1 | 1) {
    if (!one || !g || shotBusy(one.c)) return;
    if (one.ti === 0) {
      const i = v1.findIndex((c) => c.id === one.c.id);
      const index = Math.max(0, Math.min(v1.length - 1, i + dir));
      if (index !== i) commit({ op: "move", clip: one.c.id, index });
    } else commit({ op: "move", clip: one.c.id, at: Math.max(0, g.s(g.f(one.c.at!) + dir)) });
  }
  function remove() {
    for (const x of selected) if (!shotBusy(x.c)) commit({ op: "remove", clip: x.c.id });
    setSel([]);
  }
  /** Removes an audio track: an empty one at once, one with clips after the confirm. */
  function askRemove(name: string) {
    const r = doc && removeTrack(doc.tracks, name);
    if (!r) return;
    if (r.confirm) return setRemoving(name);
    commit({ op: "remove_track", track: name });
    setTrack(null);
  }
  const mark = (edge: "in" | "out") =>
    doc && setRange((r) => markRange(r, edge, at, doc.duration_s));

  // keys (contracts/README.md, Timeline rules: interaction): never while typing or in a dialog
  const keys = useRef<(e: KeyboardEvent) => void>(() => {});
  const onKey = (e: KeyboardEvent) => {
    const el = e.target as HTMLElement;
    if (!doc || !g || el.closest("input, textarea, select, dialog, [contenteditable]")) return;
    const mod = e.metaKey || e.ctrlKey;
    if (mod && e.key.toLowerCase() === "z") {
      e.preventDefault();
      if (e.shiftKey ? tl?.can_redo : tl?.can_undo) undo(e.shiftKey);
      return;
    }
    if (mod) return;
    const k = e.key;
    if (k === " " && !el.closest("button, a")) {
      e.preventDefault();
      if (playing) pv.current?.pause();
      else pv.current?.play();
    } else if ((k === "ArrowLeft" || k === "ArrowRight") && e.altKey) {
      e.preventDefault();
      nudge(k === "ArrowLeft" ? -1 : 1);
    } else if (k === "ArrowLeft" || k === "ArrowRight") {
      e.preventDefault();
      const step = e.shiftKey ? g.f(1) : 1;
      pv.current?.seek(frame + (k === "ArrowLeft" ? -step : step));
    } else if ((k === "[" || k === "]" || k === "{" || k === "}") && one && !shotBusy(one.c)) {
      e.preventDefault();
      const rev = e.shiftKey ? -1 : 1;
      const edge = k === "[" || k === "{" ? "in" : "out";
      commit({ op: "trim", clip: one.c.id, edge, delta: g.s(edge === "in" ? rev : -rev) });
    } else if ((k === "Delete" || k === "Backspace") && selected.length) {
      e.preventDefault();
      remove();
    } else if (k === "s" || k === "S") {
      e.preventDefault();
      splitHere();
    } else if (k === "i" || k === "I" || k === "o" || k === "O") {
      e.preventDefault();
      mark(k.toLowerCase() === "i" ? "in" : "out");
    } else if (k === "=" || k === "+" || k === "-") {
      setPps((p) => Math.min(400, Math.max(5, p * (k === "-" ? 0.8 : 1.25))));
    } else if (k === "Escape") {
      setSel([]);
      setTrack(null);
      setRange(null);
    }
  };
  useEffect(() => {
    keys.current = onKey;
  });
  useEffect(() => {
    const f = (e: KeyboardEvent) => keys.current(e);
    window.addEventListener("keydown", f);
    return () => window.removeEventListener("keydown", f);
  }, []);

  // ------------------------------------------------------------------ empty states
  if (error && !tl && !none)
    return (
      <main id="main" className="flex flex-1 items-center justify-center p-6">
        <ErrorNote onRetry={() => void open(id)}>{error}</ErrorNote>
      </main>
    );
  if (none)
    return (
      <main
        id="main"
        className="flex flex-1 flex-col items-center justify-center gap-3 p-6 text-center"
      >
        <h1 className="m-0 font-display text-[22px] font-bold">Timeline</h1>
        <p className="m-0 text-t2">
          No sequence yet. Make one, or use &quot;Add to timeline&quot; on a take in the gallery.
        </p>
        <button
          type="button"
          className="chip bevel rounded-[10px]"
          onClick={() => setDialog("new")}
        >
          New sequence
        </button>
        <NewSequence
          open={dialog === "new"}
          onClose={() => setDialog(null)}
          onMade={(t) => {
            setDialog(null);
            goTo(t.id);
          }}
        />
      </main>
    );
  if (!tl || !doc || !g)
    return (
      <main id="main" className="flex flex-1 items-center justify-center p-6 text-t2">
        Loading the timeline…
      </main>
    );

  // ------------------------------------------------------------------ the screen
  const t = doc.target;
  const cast = (library ?? []).filter((x) => tl.defaults.cast.includes(x.id));
  const lookItems = (looks ?? []).filter((x) => tl.defaults.looks.includes(x.id));
  const fa = frameAt(doc, Math.min(frame, Math.max(0, doc.frames - 1)), facts);
  const offlineHere = fa.pictures.some((p) => p.offline);
  const under = v1[clipAt(doc, frame, facts)];
  const rangeClip = range ? v1[clipAt(doc, g.f(range[0]), facts)] : undefined;
  const stageNote = !v1.length
    ? "Sequence preview · plays across cuts without rendering"
    : offlineHere
      ? "Offline: this clip's take was deleted"
      : under && !under.take_id
        ? under.shot
          ? "Generating this shot…"
          : "Empty slot: black and silence"
        : under?.take_id && unplayable.includes(under.take_id)
          ? "This browser cannot play this clip"
          : "";

  return (
    <>
      <main id="main" className="flex min-w-0 flex-[999_1_560px] flex-col gap-3.5 px-5 pt-4 pb-5">
        <h1 className="sr-only">Timeline: {tl.name}</h1>
        <div className="flex flex-wrap items-center gap-3">
          <button
            type="button"
            aria-haspopup="dialog"
            onClick={() => setDialog("list")}
            className="flex h-10 items-center gap-2 rounded-[10px] bg-transparent px-3 font-display text-lg font-bold tracking-[0.02em]"
          >
            {tl.name}
            <svg
              aria-hidden
              width="16"
              height="16"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              strokeWidth="2"
              strokeLinecap="round"
              strokeLinejoin="round"
            >
              <path d="m6 9 6 6 6-6" />
            </svg>
          </button>
          <span className="mono rounded-full bg-s2 px-2.5 py-1 text-[12px] text-t2">
            {t.width} × {t.height} · {t.fps} fps · {shortTime(doc.duration_s)}
          </span>
          <button
            type="button"
            className="chip bevel rounded-[10px]"
            onClick={() => setDialog("cast")}
          >
            Cast · {cast.length ? cast.map((x) => `@${x.handle}`).join(" ") : "none"}
          </button>
          <button
            type="button"
            className="chip bevel rounded-[10px]"
            onClick={() => setDialog("look")}
          >
            Look ·{" "}
            {lookItems.length
              ? `${lookItems[0].name}${lookItems.length > 1 ? ` +${lookItems.length - 1}` : ""}`
              : "none"}
          </button>
          <div className="grow" />
          <button
            type="button"
            className="chip bevel rounded-[10px]"
            disabled={!tl.can_undo}
            aria-keyshortcuts="Meta+Z Control+Z"
            onClick={() => undo()}
          >
            Undo
          </button>
          <button
            type="button"
            className="chip bevel rounded-[10px]"
            disabled={!tl.can_redo}
            aria-keyshortcuts="Meta+Shift+Z Control+Shift+Z"
            onClick={() => undo(true)}
          >
            Redo
          </button>
          {/* New sequence wraps with Export, left of it (docs/mock/Timeline, TakeActions) */}
          <div className="flex items-center gap-3">
            <button
              type="button"
              className="chip bevel rounded-[10px]"
              onClick={() => setDialog("new")}
            >
              New sequence
            </button>
            <button
              type="button"
              onClick={() => setDialog("export")}
              className="key flex h-14 flex-col items-center justify-center gap-px rounded-[14px] px-6"
            >
              <span className="text-sm">Export</span>
              <span className="mono text-[11px] font-medium tracking-normal">
                {planLine(plan?.revision === tl.revision ? plan : null)}
              </span>
            </button>
          </div>
        </div>

        {(notice || error) && (
          <div className="flex flex-wrap gap-2">
            {notice && (
              <p role="status" className="m-0 rounded-xl bg-s2 px-3 py-2 text-[13px]">
                {notice}
              </p>
            )}
            {error && <ErrorNote>{error}</ErrorNote>}
          </div>
        )}

        <div className="panel flex min-h-[230px] grow flex-col items-center justify-center gap-3 rounded-[28px_28px_28px_12px] bg-inset! p-[18px]">
          <div
            className="relative w-full max-w-[720px] overflow-hidden rounded-[10px] bg-black"
            style={{ aspectRatio: `${t.width} / ${t.height}` }}
          >
            <div
              ref={stage}
              className="absolute inset-0"
              aria-label="Sequence preview"
              role="img"
            />
            {stageNote && (
              <span
                className={`absolute inset-0 flex items-center justify-center p-3 text-center text-[13px] ${offlineHere ? "text-bad" : "text-t2"} ${!v1.length ? "rounded-[10px] border border-dashed border-dash bg-inset" : ""}`}
              >
                {stageNote}
              </span>
            )}
          </div>
          <div className="flex flex-wrap items-center justify-center gap-2">
            <button
              type="button"
              className="chip bevel rounded-[10px]"
              aria-label="Previous frame"
              onClick={() => pv.current?.seek(frame - 1)}
            >
              ‹
            </button>
            <button
              type="button"
              aria-label={playing ? "Pause" : "Play"}
              aria-keyshortcuts="Space"
              onClick={() => (playing ? pv.current?.pause() : pv.current?.play())}
              className="flex size-11 items-center justify-center rounded-full bg-t1 text-ground"
            >
              {playing ? (
                <Pause size={16} aria-hidden fill="currentColor" />
              ) : (
                <Play size={16} aria-hidden fill="currentColor" />
              )}
            </button>
            <button
              type="button"
              className="chip bevel rounded-[10px]"
              aria-label="Next frame"
              onClick={() => pv.current?.seek(frame + 1)}
            >
              ›
            </button>
            <span className="mono px-2 text-[13px]" aria-live="off">
              {timecode(at)} <span className="text-t2">/ {timecode(doc.duration_s)}</span>
            </span>
            <button
              type="button"
              className="chip bevel rounded-[10px]"
              aria-keyshortcuts="S"
              disabled={!v1.length}
              onClick={splitHere}
            >
              Split at playhead
            </button>
            <button
              type="button"
              className="chip bevel rounded-[10px]"
              onClick={() => {
                setReplacing(false);
                setShot({ at });
              }}
            >
              New shot
            </button>
            <button
              type="button"
              className="chip bevel rounded-[10px]"
              title="Mark in (I)"
              aria-keyshortcuts="I"
              onClick={() => mark("in")}
            >
              Mark in
            </button>
            <button
              type="button"
              className="chip bevel rounded-[10px]"
              title="Mark out (O)"
              aria-keyshortcuts="O"
              onClick={() => mark("out")}
            >
              Mark out
            </button>
            <button
              type="button"
              className="chip bevel rounded-[10px]"
              disabled={!range}
              onClick={() => setRange(null)}
            >
              Clear range
            </button>
          </div>
          {range && (
            <span className="mono text-[12px] text-t2" aria-live="polite">
              Range {timecode(range[0])} – {timecode(range[1])} · {(range[1] - range[0]).toFixed(2)}{" "}
              s
              {rangeClip
                ? ` · on ${rangeClip.number ? shotName(rangeClip) : clipText(rangeClip, false, tl.takes, facts, g).title}`
                : ""}
            </span>
          )}
        </div>

        {shot && (
          <ShotComposer
            key={JSON.stringify(shot)}
            tl={tl}
            doc={doc}
            facts={facts}
            slot={shot}
            workflows={workflows}
            onClose={() => setShot(null)}
            onDone={(nt, note) => {
              tlRef.current = nt;
              setTl(nt);
              setDraft(null);
              setShot(null);
              if (note) setNotice(note);
            }}
          />
        )}

        <Lanes
          doc={doc}
          takes={tl.takes}
          facts={facts}
          pps={pps}
          frame={frame}
          sel={sel}
          track={track}
          progress={progress}
          onSelect={(cid, add) => {
            setTrack(null);
            setReplacing(false);
            setSel((s) =>
              add ? (s.includes(cid) ? s.filter((x) => x !== cid) : [...s, cid]) : [cid],
            );
          }}
          onTrack={(name) => {
            setSel([]);
            setTrack((x) => (x === name ? null : name));
          }}
          onSeek={(f) => pv.current?.seek(f)}
          onPreview={setDragOp}
          onCommit={commit}
          onZoom={setPps}
          onAddTrack={() => commit({ op: "add_track" })}
          onRemoveTrack={askRemove}
          range={range}
        />
      </main>

      <aside
        aria-label="Clip inspector"
        className="aside m-3.5 flex flex-[1_1_320px] flex-col gap-5 px-[18px] pt-4 pb-6 md:ml-0"
      >
        {one ? (
          <ClipPanel
            key={one.c.id}
            tl={tl}
            doc={doc}
            clip={one.c}
            ti={one.ti}
            progress={progress[one.c.id]}
            replacing={replacing}
            onReplacing={setReplacing}
            range={range}
            workflows={workflows}
            onShotStarted={(nt) => {
              tlRef.current = nt;
              setTl(nt);
              setDraft(null);
            }}
            onCommit={commit}
            onShot={(s) => setShot(s)}
            onRerender={() => setDialog("rerender")}
            onDeselect={() => setSel([])}
          />
        ) : selected.length > 1 ? (
          <div className="flex flex-col gap-2">
            <h2 className="m-0 font-display text-base font-bold tracking-[0.03em]">
              {selected.length} clips
            </h2>
            {selected.every((x) => x.ti === 0) && (
              <button type="button" className="act" onClick={() => setDialog("rerender")}>
                Re-render…
              </button>
            )}
            <button type="button" className="act text-bad" onClick={remove}>
              Remove clips
            </button>
          </div>
        ) : track ? (
          <TrackPanel
            track={doc.tracks.find((x) => x.name === track)!}
            onCommit={commit}
            onAdd={() => setPicker({ track })}
            onRemove={() => askRemove(track)}
          />
        ) : (
          <div className="flex flex-col gap-2">
            <h2 className="m-0 font-display text-base font-bold tracking-[0.03em]">{tl.name}</h2>
            <span className="text-[12px] text-t2">
              Select a clip to trim, dissolve, set its gain or replace it. Keys: Space plays, ← →
              step a frame, [ ] trim, Alt+← → move, S splits, Delete removes.
            </span>
            {v1.some((c) => c.take_id && !c.offline) && (
              <button type="button" className="act" onClick={() => setDialog("rerender")}>
                Re-render the sequence…
              </button>
            )}
          </div>
        )}

        <div className="flex flex-col gap-2.5">
          <h3 className="heading">{track ? `Add clips to ${track}` : "Add clips"}</h3>
          {!track && recent.length > 0 && (
            <div className="grid grid-cols-3 gap-2">
              {recent.map((r) => (
                <button
                  key={r.id}
                  type="button"
                  aria-label={`Add take ${r.title}`}
                  onClick={() => addClips([r.id])}
                  className="mono aspect-[16/10] overflow-hidden rounded-lg border border-dashed border-dash bg-s1 bg-cover bg-center text-[10px] text-t2"
                  style={{
                    backgroundImage: safeUrl(r.thumb_url)
                      ? `url("${safeUrl(r.thumb_url)}")`
                      : undefined,
                  }}
                >
                  <span className="rounded bg-s1/80 px-1">{r.title}</span>
                </button>
              ))}
            </div>
          )}
          <button
            type="button"
            className="chip bevel w-fit rounded-[10px]"
            onClick={() => setPicker({ track: track ?? "V1" })}
          >
            Browse gallery
          </button>
        </div>
      </aside>

      <GalleryPicker
        open={!!picker}
        onClose={() => setPicker(null)}
        max={50}
        kind={picker && picker.track !== "V1" ? "video" : null}
        // audio is not a timeline clip yet (contracts/README.md, Media inputs: audio uploads)
        only={(x) => x.kind !== "audio"}
        onPick={(ts) => {
          const to = picker?.track ?? "V1";
          setPicker(null);
          addClips(
            ts.map((x) => x.id),
            to,
          );
        }}
      />
      <Sequences
        open={dialog === "list"}
        onClose={() => setDialog(null)}
        current={tl}
        onOpen={goTo}
        onNew={() => setDialog("new")}
        onRenamed={(nt) => setTl((cur) => (cur ? { ...cur, name: nt.name } : nt))}
        onDeleted={() => {
          router.replace("/timeline");
          void open();
        }}
      />
      <NewSequence
        open={dialog === "new"}
        onClose={() => setDialog(null)}
        onMade={(nt) => {
          setDialog(null);
          goTo(nt.id);
        }}
      />
      <Export open={dialog === "export"} onClose={() => setDialog(null)} tl={tl} />
      <Modal
        open={!!removing}
        onClose={() => setRemoving(null)}
        title={(removing && removeTrack(doc.tracks, removing)?.confirm) || "Remove the track?"}
      >
        <p className="mb-4 text-[13px] text-t2">
          Later tracks move up and are renamed. Undo brings it back.
        </p>
        <div className="flex justify-end gap-2">
          <button type="button" className="chip bevel" onClick={() => setRemoving(null)}>
            Cancel
          </button>
          <button
            type="button"
            className="chip bevel bg-a1 font-semibold text-on1"
            onClick={() => {
              if (removing) commit({ op: "remove_track", track: removing });
              setRemoving(null);
              setTrack(null);
            }}
          >
            Remove
          </button>
        </div>
      </Modal>
      <Rerender
        open={dialog === "rerender"}
        onClose={() => setDialog(null)}
        tl={tl}
        clips={selected.filter((x) => x.ti === 0).map((x) => x.c.id)}
        workflows={workflows}
        onDone={(r) => {
          tlRef.current = r.timeline;
          setTl(r.timeline);
          setDraft(null);
          setDialog(null);
          setNotice(
            <>
              Re-rendering as a batch: each result becomes its slot&apos;s take.{" "}
              <BatchLink id={r.batch.id} />
            </>,
          );
        }}
      />
      <ElementsPicker
        open={dialog === "cast"}
        onClose={() => setDialog(null)}
        inPrompt={cast.flatMap((x) => (x.handle ? [x.handle] : []))}
        onInsert={(handle) => {
          const x = library?.find((i) => i.handle === handle);
          if (!x) return;
          const ids = tl.defaults.cast.includes(x.id)
            ? tl.defaults.cast.filter((c) => c !== x.id)
            : [...tl.defaults.cast, x.id].slice(0, 16);
          void patch({ defaults: { cast: ids, looks: tl.defaults.looks } });
        }}
      />
      <LooksPicker
        group={dialog === "look" ? (lookItems[0]?.group ?? "film") : null}
        onClose={() => setDialog(null)}
        chosen={looks ? chosenFrom(tl.defaults.looks, looks) : ({} as Chosen)}
        reason={() => null}
        onChosen={(c) => {
          setDialog(null);
          const ids = [...new Set(Object.values(c).flat())].slice(0, MAX_LOOKS);
          void patch({ defaults: { cast: tl.defaults.cast, looks: ids } });
        }}
      />
    </>
  );
}

/** A labelled value in the inspector's grid (the board's In / Out / Transition in / Gain tiles). */
function Cell({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="rounded-xl bg-s2 px-3 py-2.5">
      <span className="block text-[12px] text-t2">{label}</span>
      {children}
    </div>
  );
}

const DISSOLVES = [0, 6, 12, 24, 48];
const num = "mono w-full bg-transparent font-medium text-t1 outline-0";

/** The inspector for one clip: its trims, dissolve, gain and mute, Replace, Redo, Re-render, gap and remove. */
function ClipPanel({
  tl,
  doc,
  clip: c,
  ti,
  progress,
  replacing,
  onReplacing,
  range,
  workflows,
  onShotStarted,
  onCommit,
  onShot,
  onRerender,
  onDeselect,
}: {
  tl: Timeline;
  doc: Norm;
  clip: Clip;
  ti: number;
  progress?: string;
  replacing: boolean;
  onReplacing: (on: boolean) => void;
  /** the sequence's marked range (sequence seconds) */
  range: [number, number] | null;
  workflows: S<"Workflow">[];
  /** a take action's shot started: the sequence with its placeholder */
  onShotStarted: (t: Timeline) => void;
  onCommit: (op: Op) => void;
  onShot: (s: ShotSlot) => void;
  onRerender: () => void;
  onDeselect: () => void;
}) {
  const facts = factsOf(tl.takes);
  const g = new Grid(doc.target);
  const kind = kindOf(c, facts);
  const take = c.take_id ? tl.takes[c.take_id] : undefined;
  const { title, line } = clipText(c, ti > 0, tl.takes, facts, g, progress);
  const first = ti === 0 && doc.tracks[0].clips[0]?.id === c.id;
  const alts = (c.alternatives ?? []).filter((a) => tl.takes[a]);
  const busy = shotBusy(c);
  const others = alts.some((a) => a !== c.take_id); // Replace needs a take besides the current one
  const [gain, setGain] = useDraft(String(c.gain_db ?? 0));
  const [hold, setHold] = useDraft(c.hold !== undefined ? c.hold.toFixed(2) : "");
  const sub = take
    ? [
        `From take ${take.seed != null ? `seed ${take.seed}` : take.title}`,
        take.workflow_title,
        take.video && `${take.video.frames} frames`, // docs/mock/TakeActions.dc.html
      ]
        .filter(Boolean)
        .join(" · ")
    : line;
  // the marked range on this clip, in its take's time and frames (the take actions work on it)
  const fps = take?.video?.fps ?? 0;
  const inTake =
    range && kind === "video" && fps
      ? rangeInTake(range, { at: c.at ?? 0, in: c.in ?? 0, out: c.out ?? 0 }, fps)
      : null;
  const clipRange = inTake && range ? range : null;
  const setGainDb = () => {
    const v = Math.max(-60, Math.min(12, Number(gain)));
    if (Number.isFinite(v) && v !== (c.gain_db ?? 0))
      onCommit({ op: "set", clip: c.id, gain_db: v });
    setGain(String(Number.isFinite(v) ? v : (c.gain_db ?? 0)));
  };
  return (
    <>
      <div className="flex flex-col gap-1">
        <h2 className="m-0 font-display text-base font-bold tracking-[0.03em]">{title}</h2>
        <span className="text-[12px] text-t2">{sub}</span>
      </div>

      <div className="grid grid-cols-2 gap-2">
        {kind === "video" ? (
          <>
            <Cell label="In">
              <span className="mono font-medium">{timecode(c.in ?? 0)}</span>
            </Cell>
            <Cell label="Out">
              <span className="mono font-medium">{timecode(c.out ?? 0)}</span>
            </Cell>
          </>
        ) : (
          <Cell label={kind === "image" ? "Duration (s)" : "Length (s)"}>
            <input
              aria-label="Duration in seconds"
              className={num}
              inputMode="decimal"
              value={hold}
              disabled={busy}
              onChange={(e) => setHold(e.target.value)}
              onBlur={() => {
                const v = Number(hold);
                if (Number.isFinite(v) && v > 0 && g.f(v) !== g.f(c.hold ?? 0))
                  onCommit({ op: "set", clip: c.id, hold: g.snap(v) });
                else setHold((c.hold ?? 0).toFixed(2));
              }}
              onKeyDown={(e) => e.key === "Enter" && e.currentTarget.blur()}
            />
          </Cell>
        )}
        {ti === 0 && (
          <Cell label="Transition in">
            <select
              aria-label="Transition in"
              className="w-full bg-transparent font-semibold text-t1 outline-0"
              disabled={first || busy}
              value={c.transition_in?.frames ?? 0}
              onChange={(e) => {
                const f = Number(e.target.value);
                onCommit({
                  op: "set",
                  clip: c.id,
                  transition_in: f ? { type: "dissolve", frames: f } : null,
                });
              }}
            >
              {[...new Set([...DISSOLVES, c.transition_in?.frames ?? 0])].map((f) => (
                <option key={f} value={f} className="bg-s2">
                  {f ? `Dissolve · ${f} f` : first ? "None (first clip)" : "Cut"}
                </option>
              ))}
            </select>
          </Cell>
        )}
        {inTake && (
          <div className="col-span-2 flex justify-between gap-2 rounded-xl bg-s2 px-3 py-2.5">
            <span className="text-[12px] text-t2">Range in the take</span>
            <span className="mono text-[12px]">
              {timecode(inTake[0])} – {timecode(inTake[1])} · frames {Math.round(inTake[0] * fps)} –{" "}
              {Math.round(inTake[1] * fps)}
            </span>
          </div>
        )}
        {(kind === "video" || ti > 0) && (
          <Cell label="Gain">
            <div className="flex items-center gap-2">
              {/* the board's tile reads "0 dB": the number stays editable */}
              <span className="mono flex grow items-baseline gap-1 font-medium">
                <input
                  aria-label="Gain in decibels"
                  className="mono min-w-0 bg-transparent font-medium text-t1 outline-0"
                  size={Math.max(1, gain.length)}
                  inputMode="decimal"
                  value={gain}
                  onChange={(e) => setGain(e.target.value)}
                  onBlur={setGainDb}
                  onKeyDown={(e) => e.key === "Enter" && e.currentTarget.blur()}
                />
                dB
              </span>
              <button
                type="button"
                aria-pressed={!!c.mute}
                className="chip h-7 rounded-lg px-2 text-[12px] aria-pressed:bg-a2 aria-pressed:text-[var(--on2)]"
                onClick={() => onCommit({ op: "set", clip: c.id, mute: !c.mute })}
              >
                Mute
              </button>
            </div>
          </Cell>
        )}
      </div>

      {ti === 0 && (
        <div className="flex flex-col gap-2">
          <h3 className="heading flex items-center">
            Generate from this clip
            <Info>
              Each one queues a normal run. The result drops into the sequence and keeps its lineage
              to this clip.
            </Info>
          </h3>
          {c.take_id && !c.offline && (
            <ClipActions
              tl={tl}
              doc={doc}
              clip={c}
              range={clipRange}
              workflows={workflows}
              disabled={busy}
              onDone={onShotStarted}
            />
          )}
          {!c.take_id && (
            <button
              type="button"
              className="act"
              disabled={busy}
              onClick={() => onShot({ clip: c.id })}
            >
              {c.shot?.status === "failed" ? "Try this shot again" : "New shot here"}
            </button>
          )}
          {others && (
            <button
              type="button"
              className="act"
              aria-expanded={replacing}
              disabled={busy}
              onClick={() => onReplacing(!replacing)}
            >
              Replace with another take
            </button>
          )}
          {take?.workflow_id && !c.offline && (
            <button
              type="button"
              className="act"
              disabled={busy}
              onClick={() => onShot({ clip: c.id, redo: true })}
            >
              Redo this shot
            </button>
          )}
          {clipRange && <ActionsNote />}
        </div>
      )}

      {replacing && others && (
        <div className="flex flex-col gap-2">
          <h3 className="heading">Replace with another take</h3>
          <div className="grid grid-cols-3 gap-2">
            {alts.map((a) => {
              const x = tl.takes[a];
              const now = a === c.take_id;
              const name = x.seed != null ? `seed ${x.seed}` : x.title;
              return (
                <button
                  key={a}
                  type="button"
                  aria-pressed={now}
                  aria-label={now ? `Current take ${name}` : `Use take ${name}`}
                  onClick={() => !now && onCommit({ op: "replace", clip: c.id, take_id: a })}
                  className={`mono aspect-[16/10] overflow-hidden rounded-lg bg-s1 bg-cover bg-center text-[10px] ${now ? "border-2 border-a1 text-t1" : "border border-dashed border-dash text-t2"}`}
                  style={{
                    backgroundImage: safeUrl(x.thumb_url)
                      ? `url("${safeUrl(x.thumb_url)}")`
                      : undefined,
                  }}
                >
                  <span className="rounded bg-s1/80 px-1">{now ? `${name} · now` : name}</span>
                </button>
              );
            })}
          </div>
        </div>
      )}

      <div className="flex flex-col gap-2">
        <h3 className="heading">Clip</h3>
        <div className="grid grid-cols-2 gap-2">
          {ti === 0 && take?.workflow_id && !c.offline && (
            <button type="button" className="act" disabled={busy} onClick={onRerender}>
              Re-render…
            </button>
          )}
          {ti === 0 && c.take_id && (
            <button
              type="button"
              className="act"
              disabled={busy}
              onClick={() => onCommit({ op: "lift", clip: c.id })}
            >
              Leave a gap
            </button>
          )}
          <button
            type="button"
            className="act text-bad"
            onClick={() => {
              onCommit({ op: "remove", clip: c.id });
              onDeselect();
            }}
          >
            Remove clip
          </button>
        </div>
      </div>
    </>
  );
}

/** An audio track selected by its lane name: gain, mute, Add clips (below) and Remove track. */
function TrackPanel({
  track: t,
  onCommit,
  onAdd,
  onRemove,
}: {
  track: Track;
  onCommit: (op: Op) => void;
  onAdd: () => void;
  /** removes it (asking first when it has clips) */
  onRemove: () => void;
}) {
  const [gain, setGain] = useDraft(String(t.gain_db ?? 0));
  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-col gap-1">
        <h2 className="m-0 font-display text-base font-bold tracking-[0.03em]">Track {t.name}</h2>
        <span className="text-[12px] text-t2">
          {t.clips.length === 1 ? "1 clip" : `${t.clips.length} clips`} · a video take&apos;s sound
        </span>
      </div>
      <div className="grid grid-cols-2 gap-2">
        <Cell label="Gain (dB)">
          <input
            aria-label={`Track ${t.name} gain in decibels`}
            className={num}
            inputMode="decimal"
            value={gain}
            onChange={(e) => setGain(e.target.value)}
            onBlur={() => {
              const v = Math.max(-60, Math.min(12, Number(gain)));
              if (Number.isFinite(v) && v !== (t.gain_db ?? 0))
                onCommit({ op: "track", track: t.name!, gain_db: v });
              else setGain(String(t.gain_db ?? 0));
            }}
            onKeyDown={(e) => e.key === "Enter" && e.currentTarget.blur()}
          />
        </Cell>
        <Cell label="Sound">
          <button
            type="button"
            aria-pressed={!!t.mute}
            className="chip h-7 rounded-lg px-2 text-[12px] aria-pressed:bg-a2 aria-pressed:text-[var(--on2)]"
            onClick={() => onCommit({ op: "track", track: t.name!, mute: !t.mute })}
          >
            {t.mute ? "Muted" : "Mute"}
          </button>
        </Cell>
      </div>
      <div className="grid grid-cols-2 gap-2">
        <button type="button" className="act" onClick={onAdd}>
          Add clips to {t.name}
        </button>
        <button type="button" className="act text-bad" onClick={onRemove}>
          Remove track
        </button>
      </div>
    </div>
  );
}

/** A field's typed text, reset to the document's value whenever that changes (undo, redo, a reload after 409). */
function useDraft(value: string): [string, (v: string) => void] {
  const [draft, setDraft] = useState(value);
  const [seen, setSeen] = useState(value);
  if (seen !== value) {
    setSeen(value);
    setDraft(value);
  }
  return [draft, setDraft];
}
