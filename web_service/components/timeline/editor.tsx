"use client";
// The Timeline screen (docs/mock/Timeline.dc.html, its dialogs in TimelineDialogs.dc.html; plan 11-timeline 15): the
// sequence bar (switcher, size · rate · length, cast and look defaults, undo / redo, New sequence, Export with its
// plan), the player (preview across cuts, nothing rendered), New shot docked, the lanes and the clip inspector.
// Every edit is lib/timeline's `apply` drawn at once, then its op saved through POST …/edits with the revision it
// was made on (plan 24 28.8 A; the server's answer replaces the draft); a 409 reloads, says so and re-applies the
// edit to the new revision once.
import { useRouter } from "next/navigation";
import { useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore } from "react";
import { ChevronLeft, ChevronRight, Pause, Play } from "lucide-react";
import { ErrorNote, Info, Modal } from "@/components/kit";
import { ActionsNote, ClipActions } from "@/components/timeline/actions";
import { say, timelineTools, type FormSlot, type Want } from "@/components/timeline/assistant";
import { until, useScreenTools } from "@/components/assistant/tools";
import { useLibraryDialog } from "@/components/assistant/library";
import { markRange, rangeInTake, removeTrack } from "@/lib/source";
import { ElementsPicker } from "@/components/create/elements";
import { LooksPicker } from "@/components/create/looks";
import { GalleryPicker } from "@/components/gallery/picker";
import { Lanes, MARKER_COLORS, clipText, shotName } from "@/components/timeline/lanes";
import { ShortcutSheet, SlipSource, ToolRow } from "@/components/timeline/tools";
import { ShotComposer, type ShotSlot } from "@/components/timeline/shot";
import { FrameIn } from "@/components/create/frame-in";
import { LineComposer } from "@/components/timeline/line";
import { VoicePicker, type VoiceCtx } from "@/components/create/voices";
import { speakerOf } from "@/components/create/dialogue";
import { useTakes, useUses } from "@/lib/take-cache";
import { offerLabel } from "@/lib/source";
import Link from "next/link";
import { lastTimeline, rememberTimeline } from "@/components/timeline/add";
import {
  BatchLink,
  Export,
  NewSequence,
  Rerender,
  Sequences,
  planLine,
} from "@/components/timeline/dialogs";
import { ApiError, api, editTimeline, getTimeline, message, ok, type S } from "@/lib/api";
import { safeUrl } from "@/lib/format";
import { useLibrary, useLooks, useVoices } from "@/lib/library";
import { MAX_LOOKS, chosenFrom, type Chosen } from "@/lib/looks";
import { followSession, renderTime } from "@/lib/runs";
import { Preview } from "@/lib/timeline-preview";
import {
  Grid,
  apply,
  clipAt,
  cuts,
  factsOf,
  frameAt,
  kindOf,
  newId,
  shortTime,
  span,
  timecode,
  type Clip,
  type MarkerColor,
  type Norm,
  type Op,
  type Problem,
  type Track,
  shotBusy,
} from "@/lib/timeline";
import {
  MERGE_MS,
  describe,
  keyOf,
  merge,
  shuttle,
  noun,
  signed,
  spanText,
  sticky,
  withIds,
  zoomBounds,
  type KeyId,
  type Tool,
} from "@/lib/timeline-tools";

type Timeline = S<"Timeline">;

const sayProblems = (p: Problem[]) => say(p[0][0]);
const sameSet = (a: string[], b: string[]) =>
  a.length === b.length && a.every((x) => b.includes(x));

/** A clip's name in the live region ("Shot 3", a take's title). */
function nameIn(d: Norm, cid: string, takes: Timeline["takes"]): string {
  const ti = d.tracks.findIndex((t) => t.clips.some((c) => c.id === cid));
  const c = d.tracks[ti]?.clips.find((x) => x.id === cid);
  if (!c) return "the clip";
  const title = c.take_id ? takes[c.take_id]?.title : undefined;
  return ti === 0 ? shotName(c, title ?? "the empty slot") : (title ?? "the line");
}

/** The V1 clip ids and the audio-track clips linked to each (plan 23 §27.5: selecting either selects both). */
function linkedTo(d: Norm, cid: string): string[] {
  const audio = d.tracks.slice(1).flatMap((t) => t.clips);
  const v1 = d.tracks[0].clips.some((c) => c.id === cid)
    ? cid
    : audio.find((c) => c.id === cid)?.link;
  if (!v1) return [cid];
  return [
    cid,
    ...[v1, ...audio.filter((c) => c.link === v1).map((c) => c.id)].filter((x) => x !== cid),
  ];
}

/** The editor; `id` is the sequence to open (else the one opened last, else the newest). */

/** Whether the Timeline's side panel is closed ("0"), in this browser (alpha.5 item 11). */
const PANEL = "timeline-side-panel";
const noSub = () => () => undefined;
const readPanel = () => {
  try {
    return localStorage.getItem(PANEL) !== "0";
  } catch {
    return true;
  }
};
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
  const [ppsRaw, setPpsRaw] = useState(40);
  // the tools (plan 23): the active tool, snapping (N), hover scrub (⇧S, on by default: owner point 7), the sheet
  const [tool, setTool] = useState<Tool>("select");
  const [snap, setSnap] = useState(true);
  const [scrub, setScrub] = useState(true);
  const [sheet, setSheet] = useState(false);
  // the side panel on the right (alpha.5 item 11): open by default, a closed one remembered in this browser ("0");
  // selecting a clip or a track while it is closed opens it
  const storedPanel = useSyncExternalStore(noSub, readPanel, () => true);
  const [panelSet, setPanelState] = useState<boolean | null>(null);
  const panel = panelSet ?? storedPanel;
  const setPanel = (on: boolean) => {
    setPanelState(on);
    try {
      localStorage.setItem(PANEL, on ? "1" : "0");
    } catch {}
  };
  const [picked, setPicked] = useState({ sel, track });
  if (picked.sel !== sel || picked.track !== track) {
    setPicked({ sel, track });
    if (!panel && (sel.length || track)) setPanelState(true);
  }
  // the visible track width (px), the live region's last edit, the selected marker
  const [viewW, setViewW] = useState(0);
  const [said, setSaid] = useState("");
  const [marker, setMarker] = useState<string | null>(null);
  const [notice, setNotice] = useState<React.ReactNode>("");
  const [error, setError] = useState("");
  const [dialog, setDialog] = useState<
    "list" | "new" | "export" | "rerender" | "cast" | "look" | null
  >(null);
  const [picker, setPicker] = useState<{ track: string } | null>(null);
  const [shot, setShot] = useState<ShotSlot | null>(null);
  // New line docked at the playhead (API 1.32); an audio clip's ⋯ menu; what its menu opened in the inspector
  const [line, setLine] = useState(false);
  const [menuFor, setMenuFor] = useState<string | null>(null);
  const [opened, setOpened] = useState<"make" | "use" | null>(null);
  const [voiceTake, setVoiceTake] = useState<string | null>(null);
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
  // what the assistant asked of a docked form (New shot, a take action, New line), and that form once open
  const [ask, setAsk] = useState<Want | null>(null);
  const form = useRef<FormSlot>(null);
  const setForm = useCallback((f: FormSlot) => {
    form.current = f;
  }, []);
  const lib = useLibraryDialog();

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
      const t = await getTimeline(tid);
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
    const t = await getTimeline(cur.id);
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
  /** Saves `op` on the latest stored revision through /edits; a 409 reloads and tries once more on the new one. */
  const save = useCallback(
    async (op: Op): Promise<void> => {
      for (let attempt = 0; attempt < 2; attempt++) {
        const cur = tlRef.current;
        if (!cur) return;
        const r = apply(cur, op, factsOf(cur.takes));
        // it no longer fits the newer revision (a slide past a handle another tab just trimmed): dropped, and said
        // (plan 23 §27.7)
        if (!("document" in r))
          return setNotice(
            `The sequence changed: your ${noun(op)} no longer fits and was not saved.`,
          );
        try {
          const t = await editTimeline(cur, [op]);
          tlRef.current = t;
          setTl(t);
          if (attempt)
            setNotice(
              "The sequence changed since you opened it: reloaded, and your edit applied to it.",
            );
          return;
        } catch (e) {
          if (e instanceof ApiError && e.status === 409 && !attempt) {
            const t = await getTimeline(cur.id);
            tlRef.current = t;
            setTl(t);
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
    (raw: Op) => {
      const b = draft ?? tlRef.current;
      if (!b) return;
      const op = withIds(raw, b); // the ids it makes, the same on a 409 re-apply
      const r = apply(b, op, facts);
      if ("error" in r) return setNotice(say(r.error));
      if ("problems" in r) return setNotice(sayProblems(r.problems));
      setNotice("");
      setError("");
      setSaid(describe(op, b, facts, (cid) => nameIn(b, cid, tlRef.current?.takes ?? {})));
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
  // V1's shots and (1.32) the audio tracks' lines
  const shots = (tl?.tracks ?? []).flatMap((t) => t.clips).filter(shotBusy);
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
  // one clip, or one clip and its linked ones (selected together, plan 23 §27.5): the first is the one clicked
  const linkedSel =
    !!doc && sel.length > 1 && sameSet(sel, linkedTo(doc, sel[0]))
      ? selected.find((x) => x.c.id === sel[0])
      : undefined;
  const one = selected.length === 1 ? selected[0] : (linkedSel ?? null);
  const v1 = doc?.tracks[0].clips ?? [];
  const at = g ? g.s(frame) : 0;
  const menuClip = all.find((x) => x.c.id === menuFor && x.ti > 0)?.c;
  // New line's track: the selected audio track, else the first with nothing under the playhead
  const lineTrack = (() => {
    if (!doc || !g) return null;
    const free = (t: Track) =>
      t.clips.every((c) => {
        const [a, b] = span(c, 1, facts, g);
        return at < a || at >= b;
      });
    const audio = doc.tracks.slice(1);
    const picked = audio.find((t) => t.name === track);
    return (picked && free(picked) ? picked : audio.find(free))?.name ?? null;
  })();

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
  /** Delete: ripple delete (V1 closes the gap; an audio track's later clips move left); `lift`: ⇧Delete, V1 leaves
   * an empty slot, an audio clip a gap. A linked sound clip goes with its V1 clip. */
  function remove(lift = false) {
    const v1Ids = new Set(selected.filter((x) => x.ti === 0).map((x) => x.c.id));
    for (const x of selected) {
      if (shotBusy(x.c) || (x.ti > 0 && x.c.link && v1Ids.has(x.c.link))) continue;
      if (x.ti === 0) commit({ op: lift ? "lift" : "remove", clip: x.c.id });
      else commit({ op: "remove", clip: x.c.id, ...(!lift && { ripple: true }) });
    }
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

  // zoom (plan 23 §27.4): a log scale (each step ×1.25; a pinch by its delta) from fit to 64 px per frame; the lanes
  // keep the time under the pointer, else the playhead, still. Not laid out yet (no width): only the top bound.
  const [zLo, zHi] = doc ? zoomBounds(viewW, doc.duration_s, doc.target.fps) : [5, Infinity];
  const inZoom = (v: number) => Math.min(zHi, Math.max(viewW ? zLo : 5, v));
  const pps = inZoom(ppsRaw);
  const fitPps = doc && viewW ? zLo : 0;
  const zoomTo = (v: number) => doc && setPpsRaw(inZoom(v));
  const fit = () => fitPps && setPpsRaw(fitPps);
  const atFit = !!fitPps && Math.abs(pps - fitPps) / fitPps < 0.005;

  // hover scrub: the viewer shows the frame under the pointer, one seek per animation frame (plan 23 §27.3)
  const hoverAt = useRef<number | null>(null);
  const peekRaf = useRef(0);
  const onHover = useCallback((f: number | null) => {
    hoverAt.current = f;
    if (peekRaf.current) return;
    peekRaf.current = requestAnimationFrame(() => {
      peekRaf.current = 0;
      pv.current?.peek(hoverAt.current);
    });
  }, []);
  useEffect(() => {
    if (!scrub) pv.current?.peek(null);
  }, [scrub]);

  // K held: J / L step a frame (plan 23 §27.3)
  const kDown = useRef(false);

  // key repeats (holding [) merge into one op sent after MERGE_MS; the draft shows each press (plan 23 §27.7)
  const pend = useRef<{ op: Op; timer: number } | null>(null);
  const commitRef = useRef(commit);
  useEffect(() => {
    commitRef.current = commit;
  });
  const flush = () => {
    const p = pend.current;
    if (!p) return;
    clearTimeout(p.timer);
    pend.current = null;
    setDragOp(null);
    commitRef.current(p.op);
  };
  function press(op: Op) {
    if (!g) return;
    const p = pend.current;
    const m = merge(p?.op ?? null, op, g);
    if (p && !m) flush();
    const next = m ?? op;
    const b = draft ?? tl;
    const r = b && apply(b, next, facts);
    if (!r || !("document" in r)) {
      // past a bound: this press is refused, the presses before it stay
      if (r) setNotice("error" in r ? say(r.error) : sayProblems(r.problems));
      return;
    }
    if (pend.current) clearTimeout(pend.current.timer);
    pend.current = { op: next, timer: window.setTimeout(flush, MERGE_MS) };
    setDragOp(next);
  }

  // a tool key held past HOLD_MS is temporary: back to the tool before on release (plan 23 §27.2)
  const held = useRef<{ code: string; at: number; prev: Tool } | null>(null);

  // keys (plan 23's key map, lib/timeline-tools KEYMAP, on e.code): never while typing or in a dialog
  const keys = useRef<(e: KeyboardEvent) => void>(() => {});
  const onKey = (e: KeyboardEvent) => {
    const el = e.target as HTMLElement;
    // a modal open (focus can fall to the body, as after Rename's Save): its Esc must reach the dialog
    if (!doc || !g || el.closest("input, textarea, select, dialog, [contenteditable]")) return;
    if (document.querySelector("dialog[open]")) return;
    const id: KeyId | null = keyOf(e);
    if (!id) return;
    if (id === "play" && el.closest("button, a")) return;
    e.preventDefault(); // ⌘= / ⌘− (page zoom), ⇧⌘B (the bookmarks bar), Space (the page's scroll)
    if (!/^(trim_(in|out)(_back)?|(roll|slip|slide)_(back|fwd))$/.test(id)) flush();
    // the clip a key edit works on: the selected one, else V1's under the playhead
    const here = v1[clipAt(doc, frame, facts)];
    const target = one ?? (here ? { c: here, ti: 0, t: doc.tracks[0] } : null);
    const editable = target && !shotBusy(target.c) ? target : null;
    const f1 = g.s(1);
    const cutList = (withMarkers: boolean) =>
      [
        ...new Set([
          ...cuts(doc, facts),
          doc.duration_s,
          ...(withMarkers ? (doc.markers ?? []).map((m) => m.at) : []),
        ]),
      ]
        .map((t) => g.f(t))
        .sort((a, b) => a - b);
    if (id.startsWith("tool:")) {
      if (e.repeat) return;
      held.current = { code: e.code, at: e.timeStamp, prev: tool };
      setTool(id.slice(5) as Tool);
      return;
    }
    switch (id) {
      case "escape":
        if (tool !== "select") return setTool("select");
        setSel([]);
        setTrack(null);
        setRange(null);
        setMarker(null);
        return;
      case "undo":
      case "redo":
        if (id === "redo" ? tl?.can_redo : tl?.can_undo) undo(id === "redo");
        return;
      case "play":
        if (playing) pv.current?.pause();
        else pv.current?.play(1);
        return;
      case "stop":
        kDown.current = true;
        pv.current?.pause();
        return;
      case "back":
      case "fwd": {
        const p = pv.current;
        if (!p) return;
        // K+J / K+L: a frame; else the shuttle: again ×2, ×4, ×8 (J backward)
        if (kDown.current) return p.seek(frame + (id === "back" ? -1 : 1));
        const rate = shuttle(p.playing ? p.rate : 0, id);
        return p.play(rate);
      }
      case "frame_back":
      case "frame_fwd":
      case "second_back":
      case "second_fwd": {
        const n = id.startsWith("second") ? g.f(1) : 1;
        return pv.current?.seek(frame + (id.endsWith("back") ? -n : n));
      }
      case "move_back":
      case "move_fwd":
        return nudge(id === "move_back" ? -1 : 1);
      case "cut_prev":
      case "mark_prev":
        return pv.current?.seek(
          cutList(id === "mark_prev")
            .filter((x) => x < frame)
            .pop() ?? 0,
        );
      case "cut_next":
      case "mark_next":
        return pv.current?.seek(cutList(id === "mark_next").find((x) => x > frame) ?? doc.frames);
      case "start":
        return pv.current?.seek(0);
      case "end":
        return pv.current?.seek(doc.frames);
      case "trim_in":
      case "trim_out":
      case "trim_in_back":
      case "trim_out_back": {
        if (!editable) return;
        const edge = id.startsWith("trim_in") ? "in" : "out";
        const rev = id.endsWith("back") ? -1 : 1;
        return press({
          op: "trim",
          clip: editable.c.id,
          edge,
          delta: g.s(edge === "in" ? rev : -rev),
        });
      }
      case "trim_in_here":
      case "trim_out_here": {
        if (!editable) return;
        const [a, b] = span(editable.c, editable.ti, facts, g);
        const edge = id === "trim_in_here" ? "in" : "out";
        const delta = g.s(g.f(at) - g.f(edge === "in" ? a : b));
        if (g.f(delta)) commit({ op: "trim", clip: editable.c.id, edge, delta });
        return;
      }
      case "roll_back":
      case "roll_fwd":
      case "slip_back":
      case "slip_fwd":
      case "slide_back":
      case "slide_fwd": {
        if (!editable) return;
        const op = id.split("_")[0] as "roll" | "slip" | "slide";
        return press({ op, clip: editable.c.id, delta: id.endsWith("back") ? -f1 : f1 });
      }
      case "delete":
      case "lift":
        if (selected.length) return remove(id === "lift");
        if (range)
          return commit({
            op: id === "lift" ? "lift_range" : "remove_range",
            from: range[0],
            to: range[1],
          });
        if (marker && id === "delete") {
          commit({ op: "remove_marker", id: marker });
          setMarker(null);
        }
        return;
      case "split":
        return splitHere();
      case "blade":
      case "blade_all": {
        if (id === "blade_all") return commit({ op: "split_all", at });
        const name = one && one.ti > 0 ? one.t.name! : (track ?? "V1");
        return commit({ op: "split", track: name, at, id: "" });
      }
      case "mark_in":
      case "mark_out":
        return mark(id === "mark_in" ? "in" : "out");
      case "mark_clip":
        if (here) setRange(span(here, 0, facts, g));
        return;
      case "clear_range":
        return setRange(null);
      case "zoom_in":
      case "zoom_out":
        return zoomTo(pps * (id === "zoom_out" ? 0.8 : 1.25));
      case "fit":
        return void fit();
      case "snap":
        return setSnap((x) => !x);
      case "scrub":
        return setScrub((x) => !x);
      case "marker": {
        const f = scrub && hoverAt.current !== null ? hoverAt.current : frame;
        return commit({ op: "marker", id: "", at: g.s(Math.min(f, doc.frames)) });
      }
      case "sheet":
        return setSheet(true);
    }
  };
  const onKeyUp = (e: KeyboardEvent) => {
    if (e.code === "KeyK") kDown.current = false;
    const h = held.current;
    if (!h || h.code !== e.code) return;
    held.current = null;
    if (!sticky(h.at, e.timeStamp)) setTool(h.prev);
  };
  const keysUp = useRef<(e: KeyboardEvent) => void>(() => {});
  useEffect(() => {
    keys.current = onKey;
    keysUp.current = onKeyUp;
  });
  useEffect(() => {
    const f = (e: KeyboardEvent) => keys.current(e);
    const u = (e: KeyboardEvent) => keysUp.current(e);
    window.addEventListener("keydown", f);
    window.addEventListener("keyup", u);
    return () => {
      window.removeEventListener("keydown", f);
      window.removeEventListener("keyup", u);
    };
  }, []);

  // ------------------------------------------------------------------ the assistant (plan 18 §22.5)
  const stored = useCallback((t: Timeline) => {
    tlRef.current = t;
    setTl(t);
    setDraft(null);
  }, []);
  useScreenTools(
    // eslint-disable-next-line react-hooks/refs -- the refs are read when an action runs, after render
    timelineTools({
      tl,
      doc,
      frame,
      range,
      sel,
      plan: plan?.revision === tl?.revision ? plan : null,
      open: shot ? "shot" : line ? "line" : null,
      get: () => tlRef.current,
      set: stored,
      inQueue: (f) => {
        const p = queue.current.then(f);
        queue.current = p.then(
          () => undefined,
          () => undefined,
        );
        return p;
      },
      seek: (f) => pv.current?.seek(f),
      setRange,
      select: (ids) => {
        setTrack(null);
        setSel(ids);
      },
      goTo,
      reopen: () => {
        router.replace("/timeline");
        void open();
      },
      notice: setNotice,
      lineTrack,
      dock: async (to, want) => {
        form.current = null;
        setAsk(want);
        if ("shot" in to) {
          setLine(false);
          setReplacing(false);
          setShot(to.shot);
        } else if ("line" in to) {
          setShot(null);
          setLine(true);
        } else {
          setShot(null);
          setLine(false);
          setTrack(null);
          setSel([to.clip]);
        }
        await until(() => form.current !== null, 8000);
        return form.current;
      },
      dialog: lib.ask,
      voiceCtx: ANY,
    }),
  );

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
  // Slip's drag: the clip as slipped so far, for the source window
  const slipping =
    dragOp?.op === "slip" ? all.find((x) => x.c.id === dragOp.clip && x.c.take_id)?.c : undefined;
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
  // the sequence's render time: its clips' takes, each once, that have one (API 1.60; docs/mock/Timeline.dc.html)
  const renders = [...new Set(doc.tracks.flatMap((tr) => tr.clips.map((c) => c.take_id)))]
    .map((id) => (id ? tl.takes[id]?.render_s : undefined))
    .filter((s) => s != null);
  const renderTotal = renders.length ? renderTime(renders.reduce((a, b) => a + b, 0)) : "";

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
            {renderTotal && <span title="Render time of its shots, in all"> · {renderTotal}</span>}
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
              <ChevronLeft size={14} aria-hidden />
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
              <ChevronRight size={14} aria-hidden />
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
              aria-expanded={line}
              onClick={() => {
                setShot(null);
                setLine((x) => !x);
              }}
            >
              New line
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

        {line && (
          <LineComposer
            key={JSON.stringify(ask)}
            tl={tl}
            doc={doc}
            at={at}
            track={lineTrack}
            workflows={workflows}
            ask={ask ?? undefined}
            onForm={ask ? setForm : undefined}
            onClose={() => {
              setLine(false);
              setAsk(null);
            }}
            onDone={(nt) => {
              tlRef.current = nt;
              setTl(nt);
              setDraft(null);
              setLine(false);
              setAsk(null);
            }}
          />
        )}
        {shot && (
          <ShotComposer
            key={JSON.stringify([shot, ask])}
            tl={tl}
            doc={doc}
            facts={facts}
            slot={shot}
            workflows={workflows}
            ask={ask ?? undefined}
            onForm={ask ? setForm : undefined}
            onClose={() => {
              setShot(null);
              setAsk(null);
            }}
            onDone={(nt, note) => {
              tlRef.current = nt;
              setTl(nt);
              setDraft(null);
              setShot(null);
              setAsk(null);
              if (note) setNotice(note);
            }}
          />
        )}

        {slipping && (
          <SlipSource
            url={slipping.take_id ? urls[slipping.take_id] : undefined}
            clip={{ in: slipping.in ?? 0, out: slipping.out ?? 0 }}
            take={facts[slipping.take_id!]?.duration_s ?? slipping.out ?? 1}
            delta={dragOp?.op === "slip" ? signed(dragOp.delta, g) : ""}
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
          onSelect={(cid, add, alone) => {
            setAsk(null);
            setTrack(null);
            setMarker(null);
            setReplacing(false);
            // a linked pair selects together; ⌥ picks the one clip
            const ids = alone ? [cid] : linkedTo(doc, cid);
            setSel((s) =>
              add
                ? s.includes(cid)
                  ? s.filter((x) => !ids.includes(x))
                  : [...s, ...ids.filter((x) => !s.includes(x))]
                : ids,
            );
          }}
          onTrack={(name) => {
            setSel([]);
            setMarker(null);
            setTrack((x) => (x === name ? null : name));
          }}
          onSeek={(f) => pv.current?.seek(f)}
          onPreview={setDragOp}
          onCommit={commit}
          onZoom={zoomTo}
          onAddTrack={() => commit({ op: "add_track" })}
          onRemoveTrack={askRemove}
          range={range}
          onMenu={setMenuFor}
          nameOf={(v) => speakerOf(library, v)}
          tool={tool}
          snap={snap}
          scrub={scrub}
          onHover={onHover}
          onRange={setRange}
          marker={marker}
          onMarker={(mid) => {
            setSel([]);
            setTrack(null);
            setMarker(mid);
          }}
          onView={setViewW}
          toolbar={
            <ToolRow
              tool={tool}
              onTool={setTool}
              snap={snap}
              onSnap={() => setSnap((x) => !x)}
              scrub={scrub}
              onScrub={() => setScrub((x) => !x)}
              zoom={spanText(viewW / pps, t.fps, atFit ? doc.duration_s : null)}
              onZoomOut={() => zoomTo(pps * 0.8)}
              onZoomIn={() => zoomTo(pps * 1.25)}
              onFit={fit}
              onSheet={() => setSheet(true)}
              panel={panel}
              onPanel={() => setPanel(!panel)}
            />
          }
        />
        {/* the live region: each edit said (plan 23 §27.9) */}
        <p role="status" className="m-0 -mt-2 min-h-4 px-1 text-[12px] text-t2">
          {said}
        </p>
        <ShortcutSheet open={sheet} onClose={() => setSheet(false)} />
        <ClipMenu
          clip={menuClip}
          title={menuClip ? clipText(menuClip, true, tl.takes, facts, g!).title : ""}
          onPick={(what) => {
            const c = menuClip;
            document.getElementById("clip-menu")?.hidePopover?.();
            if (!c) return;
            if (what === "remove") return commit({ op: "remove", clip: c.id });
            if (what === "voice") return setVoiceTake(c.take_id);
            setTrack(null);
            setSel([c.id]);
            setReplacing(what === "replace");
            setOpened(what === "make" ? "make" : what === "use" ? "use" : null);
          }}
        />
      </main>

      {panel && (
        <aside
          id="timeline-side"
          aria-label="Clip inspector"
          className="aside m-3.5 flex flex-[1_1_320px] flex-col gap-5 px-[18px] pt-4 pb-6 md:ml-0"
        >
          {one ? (
            <ClipPanel
              key={`${one.c.id}:${JSON.stringify(ask?.kind ? ask : null)}`}
              ask={ask?.kind ? ask : undefined}
              onForm={ask?.kind ? setForm : undefined}
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
              opened={opened}
              onOpened={setOpened}
              onSaveVoice={setVoiceTake}
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
              <button type="button" className="act text-bad" onClick={() => remove()}>
                Remove clips
              </button>
            </div>
          ) : marker && doc.markers?.some((m) => m.id === marker) ? (
            <MarkerPanel
              marker={doc.markers.find((m) => m.id === marker)!}
              onCommit={commit}
              onRemoved={() => setMarker(null)}
            />
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
                step a frame, [ ] trim, Alt+← → move, S splits, M adds a marker, Delete removes; ?
                lists them all.
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
      )}

      <SaveVoice takeId={voiceTake} onClose={() => setVoiceTake(null)} />
      {lib.node}
      <GalleryPicker
        open={!!picker}
        onClose={() => setPicker(null)}
        max={50}
        kind={null}
        // V1 takes pictures; an audio track a video's sound or (API 1.32) an audio take
        only={(x) =>
          picker?.track === "V1" ? x.kind !== "audio" : x.kind === "audio" || !!x.video?.has_audio
        }
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
  opened,
  onOpened,
  onSaveVoice,
  ask,
  onForm,
}: {
  ask?: Want;
  onForm?: (f: FormSlot) => void;
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
  /** what an audio clip's menu opened: its Make shot form, or Use in… */
  opened: "make" | "use" | null;
  onOpened: (o: "make" | "use" | null) => void;
  onSaveVoice: (takeId: string) => void;
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
        take.audio?.cues?.length && `${take.audio.cues.length} lines`, // docs/mock/TimelineAudio.dc.html
        renderTime(take.render_s), // API 1.60: "⏱ 3:12" (docs/mock/Timeline.dc.html)
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
        {kind === "audio" ? (
          // an audio take (API 1.32, docs/mock/TimelineAudio.dc.html): in / out to the ms with their samples; its
          // model and sound first (TimelineAudioZonos3.dc.html)
          <>
            {take?.workflow_title && (
              <Cell label="Model">
                <span className="font-semibold">{take.workflow_title}</span>
              </Cell>
            )}
            {take?.audio?.sample_rate && (
              <Cell label="Sound">
                <span className="font-semibold">
                  {+(take.audio.sample_rate / 1000).toFixed(1)} kHz mono
                </span>
              </Cell>
            )}
            <Cell label="In">
              <span className="mono font-medium">{ms(c.in ?? 0)}</span>
              <span className="mono block text-[11px] text-t2">
                sample {samples(g.smp(c.in ?? 0))}
              </span>
            </Cell>
            <Cell label="Out">
              <span className="mono font-medium">{ms(c.out ?? 0)}</span>
              <span className="mono block text-[11px] text-t2">
                sample {samples(g.smp(c.out ?? 0))}
              </span>
            </Cell>
            <Cell label="At">
              <span className="mono font-medium">{timecode(c.at ?? 0)}</span>
            </Cell>
            <Cell label="Track">
              <span className="font-semibold">{doc.tracks[ti]?.name}</span>
            </Cell>
            <span className="col-span-2 text-[12px] text-t2">
              Trim snaps to the {+(doc.target.sample_rate / 1000).toFixed(1)} kHz sample grid
              {take?.audio?.sample_rate && take.audio.sample_rate !== doc.target.sample_rate
                ? `; the ${+(take.audio.sample_rate / 1000).toFixed(1)} kHz take is resampled on export`
                : ""}
              .
            </span>
          </>
        ) : kind === "video" ? (
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
        {ti > 0 && (
          // 1.50, linked sound (plan 23 §27.5): a sound clip that follows a V1 clip's edits
          <Cell label="Linked to">
            <select
              aria-label="Linked to"
              className="w-full bg-transparent font-semibold text-t1 outline-0"
              disabled={busy}
              value={c.link ?? ""}
              onChange={(e) => onCommit({ op: "set", clip: c.id, link: e.target.value || null })}
            >
              <option value="" className="bg-s2">
                None
              </option>
              {doc.tracks[0].clips.map((v) => (
                <option key={v.id} value={v.id} className="bg-s2">
                  {shotName(v, v.take_id ? tl.takes[v.take_id]?.title : "Empty slot")}
                </option>
              ))}
            </select>
          </Cell>
        )}
      </div>

      {ti > 0 && (
        <AudioClip
          tl={tl}
          doc={doc}
          clip={c}
          kind={kind}
          busy={busy}
          others={others}
          replacing={replacing}
          onReplacing={onReplacing}
          workflows={workflows}
          opened={opened}
          onOpened={onOpened}
          onShotStarted={onShotStarted}
          onSaveVoice={onSaveVoice}
          onCommit={onCommit}
          onDeselect={onDeselect}
        />
      )}

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
              ask={ask}
              onForm={onForm}
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
          {kind === "video" && c.take_id && !c.offline && (
            // API 1.37 (docs/mock/H3Options.dc.html frame 7): the clip's frame as another workflow's input
            <FrameIn
              takeId={c.take_id}
              href={(id, o) =>
                `/?${tl.session_id ? `s=${encodeURIComponent(tl.session_id)}&` : ""}use=${encodeURIComponent(id)}&w=${encodeURIComponent(o.workflow_id)}`
              }
            />
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

/** "0:05.200": seconds to the ms (an audio clip's in and out). */
const ms = (t: number) => `${Math.floor(t / 60)}:${(t % 60).toFixed(3).padStart(6, "0")}`;
/** "432 000": a sample index, grouped. */
const samples = (n: number) => String(n).replace(/\B(?=(\d{3})+(?!\d))/g, " ");

/** The voice picker outside a workflow (Save as voice on a timeline clip): every voice, none greyed. */
const ANY: VoiceCtx = {
  workflow: { id: "", title: "", family: "", nodes: {}, node_schemas: {} },
  familyTitle: (f) => f,
};

/** Save as voice… on an audio clip (docs/mock/TimelineAudio.dc.html): Clone from a clip with its take. */
function SaveVoice({ takeId, onClose }: { takeId: string | null; onClose: () => void }) {
  const take = useTakes(takeId ? [takeId] : [])(takeId ?? "");
  return (
    <VoicePicker
      open={!!takeId && !!take}
      onClose={onClose}
      value=""
      onPick={() => undefined}
      ctx={ANY}
      take={take ?? undefined}
    />
  );
}

/** An audio clip's ⋯ menu (docs/mock/TimelineAudio.dc.html): Make shot, Replace with another take, Use in…, Save as
 * voice…, Remove clip. */
function ClipMenu({
  clip,
  title,
  onPick,
}: {
  clip?: Clip;
  title: string;
  onPick: (what: "make" | "replace" | "use" | "voice" | "remove") => void;
}) {
  const item = "act h-8 bg-transparent text-left";
  return (
    <div
      id="clip-menu"
      popover="auto"
      role="menu"
      aria-label={title || "Clip"}
      className="pop w-[210px] p-1.5"
      style={
        {
          positionAnchor: clip ? `--menu-${clip.id}` : undefined,
          positionArea: "bottom span-left",
          marginTop: 4,
        } as React.CSSProperties
      }
    >
      <div className="flex flex-col gap-0.5">
        <span className="truncate px-3 pb-0.5 text-[12px] font-semibold text-t2">{title}</span>
        <button type="button" role="menuitem" className={item} onClick={() => onPick("make")}>
          Make shot
        </button>
        {(clip?.alternatives?.length ?? 0) > 1 && (
          <button type="button" role="menuitem" className={item} onClick={() => onPick("replace")}>
            Replace with another take
          </button>
        )}
        <button type="button" role="menuitem" className={item} onClick={() => onPick("use")}>
          Use in…
        </button>
        <button type="button" role="menuitem" className={item} onClick={() => onPick("voice")}>
          Save as voice…
        </button>
        <button
          type="button"
          role="menuitem"
          className={`${item} text-bad`}
          onClick={() => onPick("remove")}
        >
          Remove clip
        </button>
      </div>
    </div>
  );
}

/** An audio-track clip's own parts of the inspector (API 1.32): a dialogue's lines on the sequence, a line's
 * placeholder (Retry, Remove), and Generate from this clip (Make shot, Replace, Use in…, Save as voice…). */
function AudioClip({
  tl,
  doc,
  clip: c,
  kind,
  busy,
  others,
  replacing,
  onReplacing,
  workflows,
  opened,
  onOpened,
  onShotStarted,
  onSaveVoice,
  onCommit,
  onDeselect,
}: {
  tl: Timeline;
  doc: Norm;
  clip: Clip;
  kind: string;
  busy: boolean;
  others: boolean;
  replacing: boolean;
  onReplacing: (on: boolean) => void;
  workflows: S<"Workflow">[];
  opened: "make" | "use" | null;
  onOpened: (o: "make" | "use" | null) => void;
  onShotStarted: (t: Timeline) => void;
  onSaveVoice: (takeId: string) => void;
  onCommit: (op: Op) => void;
  onDeselect: () => void;
}) {
  const library = useLibrary();
  const voices = useVoices();
  const take = c.take_id ? tl.takes[c.take_id] : undefined;
  const offers = useUses(c.take_id && !c.offline ? c.take_id : undefined);
  const [error, setError] = useState("");
  const cues = (take?.audio?.cues ?? []).filter(
    (q) => q.start_s >= (c.in ?? 0) && q.start_s < (c.out ?? 0),
  );
  const onSeq = (t: number) => timecode((c.at ?? 0) + t - (c.in ?? 0));
  const who = (v: string) => {
    const el = library?.find((e) => e.voice_id === v);
    const vo = voices?.find((x) => x.id === v);
    return [el ? `@${el.handle}` : "", vo ? (vo.group === "preset" ? vo.name : vo.group) : ""]
      .filter(Boolean)
      .join(" · ");
  };
  async function retry() {
    setError("");
    try {
      const run = await ok(
        api.GET("/v1/studio/runs/{run_id}", { params: { path: { run_id: c.shot!.run_id! } } }),
      );
      const r = await ok(
        api.POST("/v1/studio/timelines/{timeline_id}/lines", {
          params: { path: { timeline_id: tl.id } },
          body: { slot: { clip: c.id }, run: { workflow_id: run.workflow_id, inputs: run.inputs } },
        }),
      );
      onShotStarted(r.timeline);
    } catch (e) {
      setError(message(e));
    }
  }
  if (!c.take_id)
    return (
      c.shot?.status === "failed" && (
        <div className="flex flex-col gap-2">
          <span className="mono text-[12px] text-bad">{c.shot.error ?? "The run failed."}</span>
          <div className="flex gap-2">
            {c.shot.run_id && (
              <button type="button" className="chip bevel" onClick={() => void retry()}>
                Retry
              </button>
            )}
            <button
              type="button"
              className="chip bevel"
              onClick={() => {
                onCommit({ op: "remove", clip: c.id });
                onDeselect();
              }}
            >
              Remove
            </button>
          </div>
          {error && <ErrorNote>{error}</ErrorNote>}
        </div>
      )
    );
  return (
    <>
      {kind === "audio" && (
        <div className="flex flex-col gap-2">
          <h3 className="heading">Lines</h3>
          {cues.map((q) => (
            <div key={q.line} className="flex flex-col gap-0.5 rounded-xl bg-s2 px-3 py-2">
              <div className="flex justify-between gap-2 text-[13px]">
                <span>
                  <b>{q.line}</b>
                  {who(q.voice_id) ? ` · ${who(q.voice_id)}` : ""}
                </span>
                <span className="mono text-[12px] text-t2">
                  {onSeq(q.start_s)}–{onSeq(Math.min(q.end_s, c.out ?? q.end_s))}
                </span>
              </div>
              <span className="text-[12px] text-t2">{q.text}</span>
            </div>
          ))}
          <span className="text-[12px] text-t2">
            {cues.length
              ? "Times on the sequence. The cues move with the clip."
              : "A voice-over is one line: no cues."}
          </span>
        </div>
      )}
      {!c.offline && (
        <div className="flex flex-col gap-2">
          <h3 className="heading flex items-center">
            Generate from this clip
            <Info>
              Each one queues a normal run. The result drops into the sequence and keeps its lineage
              to this clip.
            </Info>
          </h3>
          <ClipActions
            key={opened ?? ""}
            tl={tl}
            doc={doc}
            clip={c}
            range={null}
            workflows={workflows}
            disabled={busy}
            onDone={onShotStarted}
            openFirst={opened === "make"}
          />
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
          {!!offers?.length && (
            <button
              type="button"
              className="act"
              aria-expanded={opened === "use"}
              onClick={() => onOpened(opened === "use" ? null : "use")}
            >
              Use in…
            </button>
          )}
          {opened === "use" &&
            offers?.map((o) => (
              <Link
                key={`${o.workflow_id} ${o.param}`}
                href={`/?${tl.session_id ? `s=${encodeURIComponent(tl.session_id)}&` : ""}use=${encodeURIComponent(c.take_id!)}&w=${encodeURIComponent(o.workflow_id)}`}
                className="act ml-3"
              >
                {offerLabel(o)}
              </Link>
            ))}
          <button
            type="button"
            className="act"
            title="Opens Clone from a clip with this clip's take (3 – 30 s)"
            onClick={() => onSaveVoice(c.take_id!)}
          >
            Save as voice…
          </button>
        </div>
      )}
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
/** A marker (1.50, plan 23 §27.5): its name and colour, Remove; edits replace it whole (`marker` with its id). */
function MarkerPanel({
  marker: m,
  onCommit,
  onRemoved,
}: {
  marker: { id: string; at: number; name?: string; color?: MarkerColor };
  onCommit: (op: Op) => void;
  onRemoved: () => void;
}) {
  const [name, setName] = useDraft(m.name ?? "");
  const save = (patch: { name?: string; color?: MarkerColor | "" }) => {
    const next = { ...m, ...patch };
    onCommit({
      op: "marker",
      id: m.id,
      at: m.at,
      ...(next.name && { name: next.name }),
      ...(next.color && { color: next.color }),
    });
  };
  return (
    <div className="flex flex-col gap-2">
      <h2 className="m-0 font-display text-base font-bold tracking-[0.03em]">
        {m.name || "Marker"}
      </h2>
      <span className="mono text-[12px] text-t2">at {timecode(m.at)}</span>
      <div className="grid grid-cols-2 gap-2">
        <Cell label="Name">
          <input
            aria-label="Marker name"
            className={num}
            maxLength={80}
            value={name}
            onChange={(e) => setName(e.target.value)}
            onBlur={() => name !== (m.name ?? "") && save({ name: name.trim() })}
            onKeyDown={(e) => e.key === "Enter" && e.currentTarget.blur()}
          />
        </Cell>
        <Cell label="Colour">
          <select
            aria-label="Marker colour"
            className="w-full bg-transparent font-semibold text-t1 outline-0"
            value={m.color ?? ""}
            onChange={(e) => save({ color: e.target.value as MarkerColor | "" })}
          >
            <option value="" className="bg-s2">
              Accent
            </option>
            {Object.keys(MARKER_COLORS).map((k) => (
              <option key={k} value={k} className="bg-s2">
                {k[0].toUpperCase() + k.slice(1)}
              </option>
            ))}
          </select>
        </Cell>
      </div>
      <button
        type="button"
        className="act text-bad"
        onClick={() => {
          onCommit({ op: "remove_marker", id: m.id });
          onRemoved();
        }}
      >
        Remove marker
      </button>
    </div>
  );
}

function useDraft(value: string): [string, (v: string) => void] {
  const [draft, setDraft] = useState(value);
  const [seen, setSeen] = useState(value);
  if (seen !== value) {
    setSeen(value);
    setDraft(value);
  }
  return [draft, setDraft];
}
