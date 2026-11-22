"use client";
// Create: one session's runs of one workflow (docs/mock/Main.dc.html). The form comes from the workflow's node
// schemas; progress comes live from the session's event stream (SSE through the BFF, ?since= polling when the stream
// can't be held).
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import dynamic from "next/dynamic";
import {
  AtSign,
  Check,
  ChevronDown,
  Info as InfoIcon,
  Maximize,
  Minus,
  Pause,
  Play,
  Plus,
  Star,
  X,
} from "lucide-react";
import { FullScreen, Media, Thumb, useFullScreenKey } from "@/components/media";
import { ErrorNote, Info } from "@/components/kit";
import {
  Chips,
  Inspector,
  RefChips,
  TextFields,
  canvasOf,
  choiceBypass,
  idOf,
} from "@/components/create/form";
import { TileWords, tileClass } from "@/components/create/elements";
import { FirstFrameTile, takeDrop, useRefUploads } from "@/components/create/refs";
import {
  MediaRows,
  MediaTile,
  SourceStage,
  SpanTile,
  extensionOf,
  useFrameGrid,
  videoSource,
} from "@/components/create/source";
import { mediaItem, offerLabel } from "@/lib/source";
import { useTakes, useUses } from "@/lib/take-cache";
import { clipOf, placeRows, positionOf } from "@/lib/video";
import { WorkflowPicker } from "@/components/create/workflows";
import { FrameIn } from "@/components/create/frame-in";
import { Compare, ExplainDrawer } from "@/components/create/compare";
import { ElementsPanel, ElementsPicker } from "@/components/create/elements";
import { AddToTimeline } from "@/components/timeline/add";
import { LooksPicker, LooksSection, QuickTiles } from "@/components/create/looks";
import { MentionChips, insertAtCaret } from "@/components/create/prompt";
import { WavePlayer } from "@/components/create/audio";
import {
  CastRows,
  CastTile,
  ElementVoicePicker,
  ScriptEditor,
  StylePerLine,
  linesKeyOf,
  scriptFieldOf,
  speakerOf,
  speechModes,
  useCast,
  useLocalErrors,
} from "@/components/create/dialogue";
import { scriptErrors } from "@/lib/voices";
import {
  SaveAsVoice,
  VoiceTile,
  cloneWorkflow,
  designWorkflow,
  designedOf,
  type VoiceCtx,
} from "@/components/create/voices";
import { useLibrary, useLooks } from "@/lib/library";
import {
  chosenFrom,
  chosenIds,
  liveChosen,
  looksReuseNote,
  reasonOf,
  tileGroup,
  tilesOf,
  type Chosen,
} from "@/lib/looks";
import { useLoras } from "@/lib/lora-library";
import { lorasNote, usable } from "@/lib/loras";
import {
  fieldErrors,
  mentioned,
  mentionNote,
  referenceRows,
  looksNote,
  resolvable,
  resolvableLooks,
  resolveMentions,
  withInputs,
} from "@/lib/mentions";
import { docOf, graphProblems, paletteOf, type Palette, type Problem } from "@/lib/graph";
import { presetNames, studioView } from "@/lib/studio";
import { ApiError, api, message, ok, okOr, type S } from "@/lib/api";
import { downloaded, precisionPins } from "@/lib/models";
import { safeUrl } from "@/lib/format";
import { mediaKinds, mediaOf, secondsText, workflowFor, type MediaKind } from "@/lib/media";
import {
  applyEvent,
  clock,
  etaText,
  followSession,
  fraction,
  isActive,
  lastDuration,
  pinnedNote,
  mergeRuns,
  nodeLabel,
  seedOf,
  segments,
  track,
  type RunTrack,
} from "@/lib/runs";
import { rememberProject, stripItems } from "@/lib/takes";
import { hostMemory } from "@/lib/hosts";
import { estimateText, Memory, useHosts, usePlan } from "@/components/create/memory-chip";
import { RunOnChip } from "@/components/create/run-on";
import {
  addonBlocks,
  addonNeeds,
  needed,
  chosenVariants,
  clashNote,
  machineReason,
  machines,
  pinnedMachine,
  valueBlock,
  valueNeeds,
  valueNeedsAt,
  versionRow,
} from "@/lib/run-on";
import {
  TAKE_MIME,
  capacity,
  carriesImage,
  editLabel,
  refUnit,
  imageInputs,
  refCount,
  refIds,
} from "@/lib/refs";
import type { GraphProps } from "@/components/graph/graph";
import {
  buildInputs,
  carry,
  display,
  fields as fieldsOf,
  isVoice,
  queueInputs,
  rawsFrom,
  replaceUnmet,
  toRaw,
  visible,
  type Field,
  type Raw,
  type Update,
} from "@/lib/workflow";

type Run = S<"Run">;
type Take = S<"Take">;
type SessionEvent = S<"SessionEvent">;

/** The most runs one Generate queues (consecutive seeds). */
const MAX_QUEUE = 4;

/** Graph view: @xyflow/react and elkjs load only when it is opened. */
const GraphView = dynamic<GraphProps>(
  () => import("@/components/graph/graph").then((m) => m.GraphView),
  {
    ssr: false,
    loading: () => (
      <main className="flex min-w-0 flex-[999_1_560px] items-center justify-center p-5 text-t2">
        Loading the graph…
      </main>
    ),
  },
);

/** The node palette (GET /v1/studio/nodes), read once Create has a workflow; null until then or when studio can't
 * answer (an older studio: the graph then shows a workflow's own kinds). */
function usePalette(when: string | null) {
  const [p, setP] = useState<{ map: Palette; groups: S<"NodePalette">["groups"] } | null>(null);
  // read again each time `when` changes (Graph opened on a workflow): runners come and go
  useEffect(() => {
    if (!when) return;
    let stop = false;
    ok(api.GET("/v1/studio/nodes"))
      .then((r) => !stop && setP({ map: paletteOf(r), groups: r.groups }))
      .catch(() => undefined);
    return () => {
      stop = true;
    };
  }, [when]);
  return p;
}

/** Calls `onEvent` for each new event of the session (see followSession). */
function useSessionEvents(sessionId: string | null, onEvent: (e: SessionEvent) => void) {
  const handler = useRef(onEvent);
  useEffect(() => {
    handler.current = onEvent;
  });
  useEffect(
    () => (sessionId ? followSession(sessionId, (e) => handler.current(e)) : undefined),
    [sessionId],
  );
}

/** What the page read on the server: the sessions and workflows, the open session (null: none yet) and its runs and
 * takes, as Create's own first load would leave them. */
export type CreateInitial = {
  sessions: S<"StudioSession">[];
  workflows: S<"Workflow">[];
  sessionId: string | null;
  runs: Run[];
  takes: Take[];
};

/** The Create screen; `initialSession` comes from `?s=`, `reuse` (a take id whose settings fill the form) from
 * `?reuse=`, `use` (a take to use in a workflow: the gallery's "Use in…") from `?use=&w=`, `initial` from the server
 * (without it, Create loads everything itself). */
export function Create({
  initialSession,
  reuse,
  use,
  initial,
}: {
  initialSession?: string;
  reuse?: string;
  use?: { take: string; workflow: string };
  initial?: CreateInitial;
}) {
  const [sessions, setSessions] = useState<S<"StudioSession">[] | null>(initial?.sessions ?? null);
  const [sessionId, setSessionId] = useState<string | null>(initial?.sessionId ?? null);
  const [workflows, setWorkflows] = useState<S<"Workflow">[] | null>(initial?.workflows ?? null);
  // the workflow picked here or by a reuse; "" follows the session's newest run
  const [chosen, setChosen] = useState("");
  const [held, setRaws] = useState<Record<string, Raw>>({});
  // which variants are on a host and what each loads at: the precision pins and "Not downloaded" (GET models)
  const [modelList, setModelList] = useState<S<"ModelList"> | null>(null);
  useEffect(() => {
    ok(api.GET("/v1/studio/models")).then(setModelList, () => undefined);
  }, []);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [runs, setRuns] = useState<Run[] | null>(initial?.runs ?? null);
  const [takes, setTakes] = useState<Take[]>(initial?.takes ?? []);
  const [tracks, setTracks] = useState<Record<string, RunTrack>>({});
  const [selected, setSelected] = useState<string | null>(null);
  const [lastRun, setLastRun] = useState<string | null>(null); // the newest run Generate queued here
  const [full, setFull] = useState(false);
  const [comparing, setComparing] = useState(false);
  const [explaining, setExplaining] = useState(false);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [count, setCount] = useState(1);
  const [openKey, setOpenKey] = useState<string | null>(null);
  const [elementsOpen, setElementsOpen] = useState(false);
  // studio's 422 on a dialogue script (API 1.32): its lines' errors, for the script they were about
  const [scriptErrs, setScriptErrs] = useState<{
    script: string;
    lines: Record<number, string>;
  } | null>(null);
  // the element whose voice the picker sets (a script line's chip, the cast, a line's fix)
  const [voiceFor, setVoiceFor] = useState<S<"LibraryItem"> | null>(null);
  // the chosen looks per group (the composer's look tiles), and the group whose picker is open
  const [looksPicked, setLooksChosen] = useState<Chosen>({});
  const [lookGroup, setLookGroup] = useState<S<"LibraryGroup"> | null>(null);
  // the empty screen (no run, no take: docs/mock/Direct.dc.html): the look group shown under the composer, and
  // whether a chip opened the Inspector in the aside in place of the Elements panel
  const [inlineGroup, setInlineGroup] = useState<S<"LibraryGroup"> | null>(null);
  const [inspect, setInspect] = useState(false);
  // a reused take's look ids, put under their groups once the looks are known
  const [reusedLooks, setReusedLooks] = useState<string[] | null>(null);
  // what Reuse said about the elements the take was made with (changed, deleted, renamed since)
  const [reuseNote, setReuseNote] = useState("");
  const [view, setView] = useState<"studio" | "graph">("studio");
  // a workflow with a range on a source shows the source on the stage until a take is picked in the strip
  const [sourceView, setSourceView] = useState(true);
  // the graph's problems for the document it holds (its own check merged with studio's)
  const [lifted, setLifted] = useState<{ id: string; problems: Problem[] } | null>(null);
  const hosts = useHosts();
  // Run on (docs/mock/RunOn.dc.html): the host every run is set to, "" = Auto; kept across workflows like the form
  const [runOnHost, setRunOnHost] = useState("");
  // why a version pick set Run on back to Auto ("Run on set to Auto: Base fp8 is NVIDIA only")
  const [runNote, setRunNote] = useState("");
  // the run list as the event handler last left it, so an event and a fetch never fold onto a stale copy, and the
  // open session, so a late answer about another one is dropped
  const runsRef = useRef<Run[]>(initial?.runs ?? []);
  const sessionRef = useRef<string | null>(initial?.sessionId ?? null);
  // the session whose runs and takes came from the server: not fetched again until another one is opened
  const seeded = useRef(initial?.sessionId ?? null);
  const put = useCallback((next: Run[]) => {
    runsRef.current = next;
    setRuns(next);
  }, []);
  /** Opens a session (null: none yet); its runs and takes load in the effect below. The workflow follows the
   * session's newest run unless `keep` (a new session keeps the one picked). */
  const open = useCallback((id: string | null, keep = false) => {
    seeded.current = null;
    sessionRef.current = id;
    setSessionId(id);
    setSelected(null);
    setTracks({});
    setTakes([]);
    setComparing(false);
    setInspect(false);
    setInlineGroup(null);
    if (!keep) setChosen("");
    runsRef.current = [];
    setRuns(id ? null : []);
  }, []);

  const load = useCallback(() => {
    Promise.all([ok(api.GET("/v1/studio/sessions")), ok(api.GET("/v1/studio/workflows"))])
      .then(([s, w]) => {
        setError("");
        setSessions(s.sessions);
        open(s.sessions.find((x) => x.id === initialSession)?.id ?? s.sessions[0]?.id ?? null);
        setWorkflows(w.workflows);
      })
      .catch((e) => setError(message(e)));
  }, [initialSession, open]);
  // the server's read stands for the first load; a later `?s=` (a new `load`) loads again
  const serverLoad = useRef(initial ? load : null);
  useEffect(() => {
    if (load !== serverLoad.current) load();
  }, [load]);

  // ⌘\ (Ctrl+\) switches Studio and Graph (plan 05-ui.md 8.2)
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key === "\\") {
        e.preventDefault();
        setView((v) => (v === "graph" ? "studio" : "graph"));
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  // the open session is in the URL, without a server round trip
  useEffect(() => {
    if (!sessionId) return;
    history.replaceState(null, "", `/?s=${encodeURIComponent(sessionId)}`);
    rememberProject(sessionId); // the Gallery's "This project only"
  }, [sessionId]);

  const loadRuns = useCallback(
    (id: string) =>
      ok(api.GET("/v1/studio/sessions/{session_id}/runs", { params: { path: { session_id: id } } }))
        .then((r) => id === sessionRef.current && put(mergeRuns(runsRef.current, r.runs)))
        .catch((e) => setError(message(e))),
    [put],
  );
  const loadTakes = useCallback(
    (id: string) =>
      ok(
        api.GET("/v1/studio/sessions/{session_id}/takes", {
          // ponytail: the newest 200; page with next_cursor if a session's strip ever needs more
          params: { path: { session_id: id }, query: { limit: 200 } },
        }),
      )
        .then((p) => id === sessionRef.current && setTakes(p.takes))
        .catch((e) => setError(message(e))),
    [],
  );
  useEffect(() => {
    if (sessionId && sessionId !== seeded.current) {
      void loadRuns(sessionId);
      void loadTakes(sessionId);
    }
  }, [sessionId, loadRuns, loadTakes]);

  // a reused take's settings, once the workflows are known
  const reused = useRef(false);
  useEffect(() => {
    if (!reuse || !workflows || reused.current) return;
    reused.current = true;
    ok(api.POST("/v1/studio/takes/{take_id}/reuse", { params: { path: { take_id: reuse } } }))
      .then((r) => {
        const w = workflows.find((x) => x.id === r.workflow_id);
        if (!w) return setError(`The workflow ${r.workflow_id} is not available any more.`);
        setChosen(w.id);
        setRaws(rawsFrom(fieldsOf(w), r.inputs));
        setErrors({});
        setOpenKey(null);
        setReuseNote(elementsNote(r.elements_status ?? []));
        setReusedLooks(r.looks ?? []);
        if (
          [...(r.loras_status ?? []), ...(r.looks_status ?? [])].some((x) => x.status !== "current")
        )
          void ok(api.GET("/v1/studio/takes/{take_id}", { params: { path: { take_id: reuse } } }))
            .then((t) =>
              setReuseNote((n) =>
                [
                  n,
                  lorasNote(r.loras_status ?? [], t.made_with?.loras ?? []),
                  looksReuseNote(r.looks_status ?? [], t.made_with?.looks ?? []),
                ]
                  .filter(Boolean)
                  .join(" "),
              ),
            )
            .catch(() => undefined);
      })
      .catch((e) => setError(message(e)));
  }, [reuse, workflows]);

  const switchRef = useRef<(id: string) => void>(() => undefined);
  /** "Use in…": the workflow, with the take as its image input (merged over the form). */
  const applyUse = useCallback(
    (takeId: string, workflowId: string) =>
      ok(
        api.POST("/v1/studio/takes/{take_id}/use", {
          params: { path: { take_id: takeId } },
          body: { workflow_id: workflowId },
        }),
      )
        .then((r) => {
          const w = workflows?.find((x) => x.id === r.workflow_id);
          if (!w) return setError(`The workflow ${r.workflow_id} is not available any more.`);
          switchRef.current(w.id); // carries over only what suits w (a Qwen variant is no LTX variant)
          setRaws((old) => ({ ...old, ...rawsFrom(fieldsOf(w), r.inputs) }));
          setErrors({});
        })
        .catch((e) => setError(message(e))),
    [workflows],
  );
  /** A "Use in…" offer: its workflow, with the inputs it prefills merged over the form. */
  const applyOffer = (o: S<"TakeOffer">) => {
    const w = workflows?.find((x) => x.id === o.workflow_id);
    if (!w) return setError(`The workflow ${o.workflow_id} is not available any more.`);
    switchRef.current(w.id);
    setRaws((old) => ({ ...old, ...rawsFrom(fieldsOf(w), o.inputs) }));
    setErrors({});
    setSourceView(true);
  };
  const used = useRef(false);
  useEffect(() => {
    if (!use || !workflows || used.current) return;
    used.current = true;
    void applyUse(use.take, use.workflow);
  }, [use, workflows, applyUse]);

  async function star(t: Take) {
    try {
      const n = await ok(
        api.PATCH("/v1/studio/takes/{take_id}", {
          params: { path: { take_id: t.id } },
          body: { starred: !t.starred },
        }),
      );
      setTakes((list) => list.map((x) => (x.id === n.id ? n : x)));
    } catch (err) {
      setError(message(err));
    }
  }

  const refetchRun = useCallback(
    (id: string) =>
      ok(api.GET("/v1/studio/runs/{run_id}", { params: { path: { run_id: id } } }))
        .then((r) => put(runsRef.current.map((x) => (x.id === r.id ? r : x))))
        .catch(() => undefined),
    [put],
  );
  const listTimer = useRef<ReturnType<typeof setTimeout>>(undefined);
  useEffect(() => () => clearTimeout(listTimer.current), []);
  useSessionEvents(runs === null ? null : sessionId, (e) => {
    setTracks((t) => track(t, e));
    const r = applyEvent(runsRef.current, e);
    put(r.runs);
    if (r.ended && sessionId) {
      void refetchRun(e.run_id);
      void loadTakes(sessionId);
    }
    if (!r.known && sessionId) {
      clearTimeout(listTimer.current);
      listTimer.current = setTimeout(() => void loadRuns(sessionId), 300);
    }
  });

  const known = (id?: string) => (workflows?.some((w) => w.id === id) ? id : undefined);
  // until the session's runs are in, its workflow isn't known: no form rather than a flash of the first one
  const workflowId =
    known(chosen) ??
    (runs === null && sessionId ? "" : (known(runs?.[0]?.workflow_id) ?? workflows?.[0]?.id ?? ""));
  const workflow = workflows?.find((w) => w.id === workflowId);
  const unpinned = useMemo(() => (workflow ? fieldsOf(workflow) : []), [workflow]);
  // a variant that loads at one precision (fp8) fixes the Precision tile to it, and the run sends it
  const pins = useMemo(() => precisionPins(unpinned, held, modelList), [unpinned, held, modelList]);
  const all = useMemo(
    () =>
      unpinned.map((f) => (pins[f.key] ? { ...f, param: { ...f.param, enum: [pins[f.key]] } } : f)),
    [unpinned, pins],
  );
  const machineList = useMemo(() => machines(hosts), [hosts]);
  const runOn = pinnedMachine(machineList, runOnHost);
  // optional add-ons (API 1.36): a node whose add-on is not where the run may go has its controls greyed, and stays
  // bypassed so the run check never refuses it (contracts/README.md, Optional add-ons)
  // (API 1.40) an add-on scoped to other variants than the chosen one is not needed; one a node the workflow always
  // runs needs greys its variants' rows instead (Dev without the distilled LoRA)
  const allNeeds = useMemo(() => addonNeeds(workflow, modelList), [workflow, modelList]);
  const chosenModels = chosenVariants(all, { ...held, ...pins }, modelList);
  // (API 1.40) and one for the other task (`refs`): an edit needs Qwen's PE-I2I, text to image its PE-T2I
  const withRefs = refCount(all, held) > 0;
  const needs = allNeeds.filter((n) => needed(n, chosenModels, withRefs));
  const blocks = addonBlocks(needs, runOn);
  const forced = Object.fromEntries(Object.keys(blocks).map((n) => [`${n}.bypass`, true]));
  const raws =
    Object.keys(pins).length || Object.keys(forced).length ? { ...held, ...pins, ...forced } : held;
  // (API 1.41) an add-on one value of a param asks for (MiniMax H3's lora turbo: Turbo LoRA, with a reference Turbo
  // Ref LoRA): that option and every preset that picks it are greyed where the run may go, and a form that holds it
  // anyway keeps it, greyed, with Generate blocked (it never drops to another value by itself; H3Options 11-13)
  const allValueNeeds = useMemo(() => valueNeeds(workflow, modelList), [workflow, modelList]);
  const valueOn = (node: string) =>
    (raws[`${node}.bypass`] ?? workflow?.nodes[node]?.bypass) !== true;
  const wiredIn = (node: string) => (name: string) => {
    const v = raws[`${node}.${name}`] ?? all.find((f) => f.key === `${node}.${name}`)?.initial;
    return (Array.isArray(v) && v.length > 0) || !!workflow?.nodes[node]?.in?.[name];
  };
  const canRun = machineList.filter(
    (mc) => machineReason(mc, workflow?.env, chosenModels) === null,
  );
  const valueBlocks: Record<string, Record<string, string>> = {};
  const heldNeeds: typeof allValueNeeds = [];
  let heldBlock: string | null = null;
  for (const f of all.filter((x) =>
    allValueNeeds.some((n) => n.field === x.key && valueOn(n.node)),
  )) {
    const cur = String(raws[f.key] ?? toRaw(f.initial, f.param));
    for (const o of f.param.enum ?? []) {
      const why = valueBlock(allValueNeeds, f.key, o, wiredIn(f.node), canRun, runOn);
      if (why) (valueBlocks[f.key] ??= {})[o] = why;
    }
    heldNeeds.push(...valueNeedsAt(allValueNeeds, f.key, cur, wiredIn(f.node)));
    heldBlock ??= valueBlocks[f.key]?.[cur] ?? null;
  }
  // what the form held in each workflow when it was left: a value that did not suit the next one comes back
  const left = useRef<Record<string, Record<string, Raw>>>({});
  // a workflow with a Studio block shows exactly what it places (lib/studio.ts)
  const layout = useMemo(() => (workflow ? studioView(workflow, all) : null), [workflow, all]);
  // the params whose `when` holds now: the others are neither shown nor sent
  const fields = visible(layout ? layout.shown : all, raws, all);
  // a dialogue's script (API 1.32) and the rows it writes, which no editor of their own shows
  const scriptField = scriptFieldOf(fields);
  const linesKey = linesKeyOf(fields);
  const script = scriptField
    ? String(raws[scriptField.key] ?? toRaw(scriptField.initial, scriptField.param))
    : "";
  const cast = useCast(script, workflow);
  const modes = workflows ? speechModes(workflows, workflow) : null;
  const castTake = useTakes(
    cast.flatMap((c) => (c.voice?.voice?.clip ? [c.voice.voice.clip.take_id] : [])),
  );
  // an audio workflow reads it too: the voice picker names each family's starter voices by its title
  const palette = usePalette(
    view === "graph"
      ? `graph ${workflowId}`
      : workflow?.source === "mine"
        ? "mine"
        : workflow?.media === "audio"
          ? "audio"
          : null,
  );
  const loras = useLoras();
  const own = useMemo(
    () =>
      workflow?.source === "mine" && palette
        ? graphProblems(docOf(workflow), palette.map, loras ? usable(loras) : undefined)
        : [],
    [workflow, palette, loras],
  );
  // a workflow of one's own runs only without problems (studio refuses it with 422); templates are not checked
  const problems = lifted && lifted.id === workflow?.id ? lifted.problems : own;
  const blocked = workflow?.source === "mine" ? problems.length : 0;
  // no variant of its model on a live host: nothing can run it (the picker says "Not downloaded · Models")
  const notDownloaded = !!workflow && !downloaded(modelList, workflow.family);
  const onProblems = useCallback(
    (id: string, ps: Problem[]) => setLifted({ id, problems: ps }),
    [],
  );
  /** A workflow saved, copied, renamed or reloaded: into the list (new ones first among one's own) and open. */
  const onWorkflow = useCallback((w: S<"Workflow">, open: boolean) => {
    setWorkflows((list) =>
      list?.some((x) => x.id === w.id)
        ? list.map((x) => (x.id === w.id ? w : x))
        : [
            ...(list ?? []).filter((x) => x.source !== "mine"),
            w,
            ...(list ?? []).filter((x) => x.source === "mine"),
          ],
    );
    if (open) setChosen(w.id);
  }, []);
  // the upscale node of a two-stage workflow (its own bypass in the graph means it never runs)
  const upscale = Object.entries(workflow?.nodes ?? {}).find(
    ([, n]) => n.kind.endsWith("/upscale") && !n.bypass,
  )?.[0];
  // the Seeds stepper whenever the workflow has a seed, placed or not (docs/mock/TextToVideo.dc.html)
  const seedField = all.find((f) => f.param.type === "seed");
  // the chosen host is gone: nothing is queued (Generate says so), the chip warns
  const runOnOffline = !!runOn && !runOn.live;
  const change = (key: string, u: Update) => {
    // a version Run on's host cannot run is still picked: Run on goes back to Auto and says why, never the version
    const vf = all.find((x) => x.key === key && x.model && x.param.name === "variant");
    const clash =
      runOn && vf && typeof u === "string"
        ? chosenVariants([vf], { [key]: u }, modelList).map(({ m, v }) => clashNote(runOn, m, v))[0]
        : null;
    if (clash) {
      setRunOnHost("");
      setRunNote(clash);
    }
    setRaws((r) => {
      const v = typeof u === "function" ? u(r[key]) : u;
      // an image added to rows with a time on the clip (keyframes) goes on a free frame
      const f = fields.find((x) => x.key === key);
      const placed =
        f && Array.isArray(v) && positionOf(f.param)
          ? placeRows(f.param, v, r[key] ?? f.initial ?? [], clipOf(fields, r))
          : v;
      // rows whose node's bypass is placed too (LTX Audio to video's Frames · No keyframes): a frame picked
      // switches the node on, the last one cleared switches it off
      const off = f && Array.isArray(v) && fields.find((x) => x.key === `${f.node}.bypass`);
      // a setting of a bypassed node switched on (Think) switches the node back on, and says so
      // (docs/mock/CreateEnhance.dc.html: "No enhance turned off: Think needs the enhancer")
      const by =
        f?.param.type === "bool" &&
        f.param.name !== "bypass" &&
        v === true &&
        fields.find((x) => x.key === `${f.node}.bypass`);
      if (by && (r[by.key] ?? toRaw(by.initial, by.param)) === true) {
        const n = needs.filter((x) => x.node === f.node);
        const what =
          n.length === 1
            ? `the ${n[0].title.toLowerCase()}`
            : workflow?.node_schemas[workflow.nodes[f.node].kind]?.component
              ? `the ${workflow.node_schemas[workflow.nodes[f.node].kind].component}`
              : f.nodeTitle;
        const label = (x: Field) => x.param.label ?? x.param.name;
        queueMicrotask(() => setRunNote(`${label(by)} turned off: ${label(f)} needs ${what}`));
        return { ...r, [key]: placed, [by.key]: false };
      }
      return { ...r, [key]: placed, ...(off && { [off.key]: v.length === 0 }) };
    });
    setErrors((errs) => {
      const next = { ...errs };
      delete next[key];
      return next;
    });
    const t = fields.find((x) => x.key === key)?.param.type;
    if (t === "media" || t === "span") setSourceView(true);
  };
  const uploads = useRefUploads(change);
  /** Opens another workflow of the session (the picker, the Image / Video switch): what the form holds carries over
   * where the paths match (the session rule, lib/workflow.ts carry). */
  const switchTo = (id: string) => {
    const w = workflows?.find((x) => x.id === id);
    if (w)
      setRaws((r) => {
        if (workflow) left.current[workflow.id] = r;
        return carry(r, fieldsOf(w), all, left.current[id]);
      });
    setChosen(id);
    setOpenKey(null);
  };
  useEffect(() => {
    switchRef.current = switchTo;
  });
  const media = workflow ? mediaOf(workflow) : "image";
  const kinds = workflows ? mediaKinds(workflows) : [];
  const video = media === "video";
  // speech (API 1.30, docs/mock/CreateAudio.dc.html): the prompt is spoken, no elements or looks
  const audio = media === "audio";
  // a drop or paste on the stage or the composer goes to the first image input
  const refField = imageInputs(fields)[0];
  const intoRefs = (dt: DataTransfer) =>
    !!refField &&
    takeDrop(
      dt,
      refField,
      capacity(refField) - refIds(refField, raws[refField.key]).length,
      change,
      uploads,
    );
  const refs = refCount(fields, raws);
  // a workflow without image inputs whose family has a template with a first frame (text to video → image to
  // video): the composer's First frame tile opens that one with the image picked
  // a workflow with a media source (retake, extend, audio to video) is not text to video: no first frame offer
  const sourced = fields.some((f) => f.param.type === "media");
  const firstFrameIn =
    !refField && workflow && !sourced
      ? workflows?.find(
          (w) =>
            w.source === "template" &&
            w.family === workflow.family &&
            mediaOf(w) === media &&
            imageInputs(fieldsOf(w)).some((f) => positionOf(f.param)),
        )
      : undefined;
  // video and audio references (API 1.22) counted after the images: "1 video · 2 audio"
  const mediaCounts = fields.flatMap((f) => {
    const m = mediaItem(f.param);
    const n = Array.isArray(raws[f.key]) ? (raws[f.key] as unknown[]).length : 0;
    if (!m || !n) return [];
    const audio = !m[1].accepts?.includes("video");
    return [`${n} ${audio ? "audio" : `video${n === 1 ? "" : "s"}`}`];
  });
  // the family's title ("LTX-2.5": the palette's, else its model node's): the top bar's label and the model chip's
  // tooltip lead with it
  const familyName =
    palette?.groups.find((g) => g.family === workflow?.family)?.title ??
    all.find((f) => f.model)?.nodeTitle;
  // the top bar's label: an edit counts its references; a clip its frames, or offers a first frame
  const workflowLabel = !workflow
    ? ""
    : audio
      ? [
          videoTitle(workflow, familyName),
          scriptField && cast.length && `${cast.length} speaker${cast.length === 1 ? "" : "s"}`,
        ]
          .filter(Boolean)
          .join(" · ")
      : !video
        ? editLabel(workflow.title, refs)
        : [
            videoTitle(workflow, familyName),
            refs
              ? [`${refs} ${refUnit(refField)}${refs === 1 ? "" : "s"}`, ...mediaCounts].join(" · ")
              : mediaCounts.length
                ? mediaCounts.join(" · ")
                : fields.some((f) => f.param.type === "span")
                  ? "the rest of the clip is kept as context" // docs/mock/VideoSource.dc.html
                  : firstFrameIn && "add a first frame to animate it",
          ]
            .filter(Boolean)
            .join(" · ");
  // the workflow's model choices: the model chip names them ("Base · int8"), its menu picks a placed variant; a load
  // option beside them (API 1.39 `compute`) is a Quality tile only (docs/mock/LtxFeatures.dc.html: "Distilled · int8")
  const chipModels = all.filter(
    (f) => f.model && f.param.type === "enum" && ["variant", "precision"].includes(f.param.name),
  );
  // the composer holds the quick switches and one-value choices; a quick rows editor is its own inspector section
  const quick =
    layout?.groups.find((g) => g.title === "Quick")?.fields.filter((f) => fields.includes(f)) ?? [];
  // a node's bypass placed with its rows is the rows tile's "No keyframes", not a switch of its own
  const toggles = quick.filter(
    (f) =>
      f.param.type === "bool" &&
      !(
        f.param.name === "bypass" &&
        fields.some((x) => x.node === f.node && x.param.type === "rows")
      ),
  );
  const placed = layout?.groups.flatMap((g) => {
    const fs = g.fields.filter((f) => fields.includes(f));
    if (g.title !== "Quick") return [{ ...g, fields: fs }];
    // a quick model choice is picked from the model chip's menu (docs/mock/TextToVideo.dc.html has no Model tile)
    const rest = fs.filter(
      (f) =>
        f.param.type !== "bool" &&
        f.param.type !== "voice" &&
        f.key !== linesKey &&
        !(f.param.type === "enum" && (f.param.enum?.length ?? 0) < 2) &&
        !(f.model && f.param.type === "enum"),
    );
    const own = rest.filter((f) => ["rows", "image", "media", "span"].includes(f.param.type));
    // an extension's seconds and direction: a section named by its node ("Extend")
    const ext = extensionOf(rest);
    const extFields = ext ? [ext.seconds, ext.direction] : [];
    const left = rest.filter((f) => !own.includes(f) && !extFields.includes(f));
    return [
      ...(left.length ? [{ title: g.title, fields: left }] : []),
      ...own.map((f) => ({ title: f.param.label ?? f.param.name, fields: [f] })),
      ...(ext ? [{ title: ext.seconds.nodeTitle, fields: extFields }] : []),
    ];
  });
  // an audio workflow (docs/mock/CreateAudio.dc.html): the voice heads the Voice section (also a composer tile),
  // the first section (before Delivery, CreateAudioZonos3.dc.html)
  const spokenVoices = fields.filter((f) => f.param.type === "voice");
  // a dialogue (API 1.32): its Cast section lists the speakers, its Voice section each line's style
  const castHead = scriptField && (
    <CastRows
      cast={cast}
      onVoice={setVoiceFor}
      familyTitle={(f) => palette?.groups.find((g) => g.family === f)?.title ?? f}
      takeTitle={(id) => castTake(id)?.title}
    />
  );
  const styleHead = scriptField && (
    <StylePerLine value={script} onChange={(v) => change(scriptField.key, v)} />
  );
  const groups =
    audio && placed && (spokenVoices.length || scriptField)
      ? [
          ...(spokenVoices.length
            ? [
                {
                  title: "Voice",
                  fields: [
                    ...spokenVoices,
                    ...(placed.find((g) => g.title === "Voice")?.fields ?? []),
                  ],
                },
              ]
            : []),
          // Quick's Language and Seed are composer tiles: no Quick section (CreateAudio.dc.html); a tile opened
          // shows its one editor, under its own name
          ...placed.flatMap((g) =>
            g.title === "Voice" && spokenVoices.length
              ? []
              : g.title === "Quick"
                ? g.fields
                    .filter((f) => f.key === openKey)
                    .map((f) => ({ title: f.param.label ?? f.param.name, fields: [f] }))
                : [
                    {
                      ...g,
                      head:
                        g.title === "Cast" ? castHead : g.title === "Voice" ? styleHead : undefined,
                    },
                  ],
          ),
        ]
      : placed;
  // media inputs (API 1.19): composer tiles, the source on the stage, the family's frame grid for the readouts
  const mediaFields = fields.filter((f) => f.param.type === "media");
  // rows of video or audio takes (API 1.22, MiniMax H3 references): their chips in the composer
  const mediaRowFields = fields.filter((f) => mediaItem(f.param));
  const spanField = fields.find((f) => f.param.type === "span");
  const grid = useFrameGrid(mediaFields.length ? workflow?.family : undefined);
  const source = videoSource(fields);
  const sourceSet = !!source && typeof (raws[source.key] ?? "") === "object";
  const sourceId = sourceSet ? (raws[source.key] as { take_id: string }).take_id : undefined;
  const sourceTake = useTakes(sourceId ? [sourceId] : [])(sourceId ?? "");
  const wholeClip =
    spanField && sourceTake?.video ? `whole clip · ${sourceTake.video.frames} frames` : "";
  // the mentions: what they add for this workflow (the line under the prompt), checked as studio checks them
  const library = useLibrary();
  const looks = useLooks();
  // a chosen look deleted or gone from every live host since is dropped, never sent
  const looksChosen = liveChosen(looksPicked, looks);
  const lookList = useMemo(() => resolvableLooks(looks ?? []), [looks]);
  const loraFamilies = useMemo(() => usable(loras), [loras]);
  // Reuse's looks, once the looks are known (one gone since is left out; the note says so)
  if (reusedLooks && looks) {
    setLooksChosen(chosenFrom(reusedLooks, looks));
    setReusedLooks(null);
  }
  /** The run's looks for `w`: the chosen looks of its tiles, in tile order. */
  const lookIdsOf = (w: S<"Workflow">) =>
    chosenIds(tilesOf(w, looks ?? [], loraFamilies, looksChosen), looksChosen);
  const mentionsOf = (w: S<"Workflow">, inputs: S<"RunInputs">) =>
    library &&
    resolveMentions(
      withInputs(w.nodes, inputs),
      w.node_schemas,
      resolvable(library),
      {},
      lookList,
      looks ? lookIdsOf(w) : [],
      loraFamilies,
    );
  // a script is not a prompt: its own editor, no mentions scanned (contracts/README.md, Dialogue: scripts)
  const textFields = (layout ? fields.filter((f) => layout.text.includes(f)) : fields).filter(
    (f) => f.param.type === "text" && !f.param.script,
  );
  const texts = textFields.map((f) => String(raws[f.key] ?? toRaw(f.initial, f.param)));
  const handles = [...new Set(texts.flatMap(mentioned))];
  const formInputs = buildInputs(fields, raws).inputs;
  // the graph as the form sets it: what the picker's fit reasons are about
  const current = workflow && { ...workflow, nodes: withInputs(workflow.nodes, formInputs) };
  // an audio workflow's composer has neither tile (contracts/README.md, Voices: speech and the library)
  const tiles = (
    workflow && !audio ? tilesOf(workflow, looks ?? [], loraFamilies, looksChosen) : []
  ).filter((t) => t !== "elements" || textFields.length > 0);
  const lookIds = workflow ? lookIdsOf(workflow) : [];
  const preview =
    workflow && (handles.length || lookIds.length) ? mentionsOf(workflow, formInputs) : null;
  const note =
    preview && workflow
      ? preview.errors.length
        ? preview.errors.join("; ")
        : [
            mentionNote(
              preview,
              resolvable(library ?? []),
              !referenceRows(workflow.nodes, workflow.node_schemas),
            ),
            looksNote(preview, lookList),
          ]
            .filter(Boolean)
            .join("\n") || null
      : null;
  /** Opens a field's inspector control (or the canvas picker) and moves focus to it. */
  const show = (key: string) => {
    setInspect(true);
    if (key !== "canvas") setOpenKey(key);
    requestAnimationFrame(() => {
      // a size no aspect chip matches leaves none pressed: then the first one
      const q = (s: string) => document.querySelector<HTMLElement>(s);
      const el =
        key === "canvas"
          ? (q("#canvas-pick:not([hidden]) [aria-pressed=true]") ??
            q("#canvas-pick:not([hidden]) button") ??
            q("section:has(#canvas-pick) .set"))
          : q(`#${idOf(key)}-ed :is(input, .opt)`);
      el?.scrollIntoView({ block: "nearest" });
      el?.focus();
    });
  };

  // voice params (API 1.30): a composer tile each that opens the voice picker; an audio workflow's quick params and
  // its seed are composer tiles too (docs/mock/CreateAudio.dc.html: Voice, Language, Seed)
  const voiceFields = fields.filter((f) => f.param.type === "voice");
  // a run needs a voice (studio's `a voice is required`): Generate waits for one and says so, as the tile does
  const noVoice = voiceFields.some((f) => !isVoice(raws[f.key]));
  // a dialogue's line errors: studio's for this script, else the same check here (check.py expand_script)
  const localLines = useLocalErrors(scriptField ? workflow : undefined, formInputs);
  const lineErrors =
    scriptErrs && scriptErrs.script === script
      ? { ...localLines, ...scriptErrs.lines }
      : localLines;
  // "Fix line 4 first": the first wrong line, counted as the script's spoken lines are
  const badLine = (() => {
    const first = Math.min(...Object.keys(lineErrors).map(Number));
    if (!Number.isFinite(first)) return 0;
    return script
      .split("\n")
      .slice(0, first)
      .filter((l) => l.trim()).length;
  })();
  const familyTitle = (f: string) => palette?.groups.find((g) => g.family === f)?.title ?? f;
  const designSession = useRef<string | null>(null);
  const designWf = audio && workflows ? designWorkflow(workflows, workflow?.family) : undefined;
  // Clone from a clip's Hear it (API 1.31): the template that clones a clip (ZONOS3's voice-over)
  const cloneWf = audio && workflows ? cloneWorkflow(workflows, workflow?.family) : undefined;
  const tries = (w: S<"Workflow">) =>
    workflow && {
      workflow: w,
      label: videoTitle(w, familyTitle(w.family)),
      // the tries of both go to the open session, else one "Voice design" session made once
      session: async () =>
        sessionId ??
        (designSession.current ??= (
          await ok(api.POST("/v1/studio/sessions", { body: { title: "Voice design" } }))
        ).id),
      stay: () => setChosen(workflow.id),
    };
  const voiceCtx: VoiceCtx | undefined = workflow && {
    workflow,
    familyTitle,
    design: designWf && tries(designWf),
    clone: cloneWf && tries(cloneWf),
  };
  const paramCards = audio
    ? [
        ...quick.filter(
          (f) =>
            !["voice", "bool", "text", "rows", "image", "media", "span"].includes(f.param.type) &&
            !(f.model && f.param.type === "enum"),
        ),
        ...(seedField && !quick.includes(seedField) && fields.includes(seedField)
          ? [seedField]
          : []),
      ]
    : [];

  async function newSession(title?: string) {
    const s = await ok(api.POST("/v1/studio/sessions", { body: title ? { title } : {} }));
    setSessions((list) => [s, ...(list ?? [])]);
    open(s.id, true);
    return s.id;
  }

  // set at once (state lags a render), so a second ⌘Enter while a queue is being sent does nothing
  const sending = useRef(false);
  async function generate(e: React.FormEvent) {
    e.preventDefault();
    if (workflow && !blocked && !notDownloaded && !runOnOffline && !heldBlock)
      await send(workflow, fields);
  }
  /** Queues runs of `w` (Graph view passes the version it just saved) with the form's settings for it. */
  async function send(w: S<"Workflow">, fields: Field[]) {
    if (sending.current) return;
    const built = buildInputs(fields, raws);
    // an unplaced seed still steps from the workflow's own value
    const seed = fieldsOf(w).find((f) => f.param.type === "seed");
    const queued = queueInputs(
      seed && !fields.some((f) => f.key === seed.key) ? [...fields, seed] : fields,
      built.inputs,
      seed ? count : 1,
    );
    // the mention preview (the note under the prompt) is advisory: this tab's element list may be stale, so studio
    // decides and its 422 lands on the fields
    const errs = { ...built.errors, ...queued.errors };
    setErrors(errs);
    if (Object.keys(errs).length) {
      if (view === "graph")
        setError("Some Studio settings are not valid: open Studio to fix them.");
      return;
    }
    sending.current = true;
    setBusy(true);
    setError("");
    const ids = lookIdsOf(w);
    try {
      const prompt = fields.find((f) => f.param.type === "text");
      const title = prompt
        ? String(raws[prompt.key] ?? "")
            .trim()
            .slice(0, 60)
        : "";
      const sid = sessionId ?? (await newSession(title || undefined));
      for (const each of queued.runs) {
        const run = await ok(
          api.POST("/v1/studio/sessions/{session_id}/runs", {
            params: { path: { session_id: sid } },
            body: {
              workflow_id: w.id,
              inputs: each,
              ...(ids.length && { looks: ids }),
              ...(runOn && { host: runOn.host }),
            },
          }),
        );
        if (sid === sessionRef.current) put(mergeRuns(runsRef.current, [run]));
        setSelected(run.id);
        setLastRun(run.id);
      }
      setReuseNote("");
    } catch (err) {
      // a dialogue's line errors go on its lines (API 1.32), the rest as before
      const sc =
        scriptField && err instanceof ApiError && err.status === 422
          ? scriptErrors(err.message)
          : null;
      if (sc && Object.keys(sc.lines).length) setScriptErrs({ script, lines: sc.lines });
      const detail = sc ? sc.other.join("; ") : err instanceof Error ? err.message : "";
      // a 422 naming the form's fields (an unknown @handle, the references cap) goes on them
      const on =
        err instanceof ApiError &&
        err.status === 422 &&
        !!detail &&
        fieldErrors(
          detail,
          fields.map((f) => f.key),
        );
      if (on) setErrors(on);
      else if (!sc || detail) setError(sc ? detail : message(err));
    } finally {
      sending.current = false;
      setBusy(false);
    }
  }

  async function cancel(id: string) {
    try {
      const r = await okOr(
        api.POST("/v1/studio/runs/{run_id}/cancel", { params: { path: { run_id: id } } }),
        409,
      );
      // only into the open session: Stop may answer after another one was opened
      if (r) {
        if (r.session_id === sessionRef.current) put(mergeRuns(runsRef.current, [r]));
      } else await refetchRun(id); // it had already ended
    } catch (err) {
      setError(message(err));
    }
  }

  // the strip: takes, and the runs without one; a selected run that finished shows its take
  const items = runs ? stripItems(runs, takes) : [];
  const picked =
    items.find((i) => (i.take ?? i.run).id === selected) ??
    items.find((i) => i.take?.run_id === selected) ??
    items[0];
  const shownTake = picked?.take;
  useFullScreenKey(!!shownTake && !full, () => setFull(true));
  const offers = useUses(shownTake?.id);
  const takeFamily =
    workflows?.find((w) => w.id === shownTake?.made_with?.workflow_id)?.family ?? workflow?.family;
  // a video take's Extend beside Add to timeline (docs/mock/TextToVideo.dc.html): its family's, else the first
  const extendOffer =
    shownTake?.kind === "video"
      ? (offers ?? [])
          .filter((o) => o.action === "Extend")
          .sort((a, b) => +(b.family === takeFamily) - +(a.family === takeFamily))[0]
      : undefined;
  const shown = picked?.run ?? runs?.find((r) => r.id === shownTake?.run_id);
  // the session's pictures Compare offers: image and video takes, newest first
  const pictures = takes.filter((t) => t.kind === "image" || t.kind === "video");
  // no run and no take yet: the title, the composer under it with the open look group's looks, and the Elements
  // panel in the aside (docs/mock/Direct.dc.html); the first run brings the Stage and the Inspector
  const empty = !runs?.length && !takes.length;
  const lookTiles = tiles.flatMap((t) => tileGroup(t) ?? []);
  // the tile opened last, else the first look tile
  const openGroup =
    inlineGroup && lookTiles.includes(inlineGroup) ? inlineGroup : (lookTiles[0] ?? null);
  const elementsAside =
    empty &&
    !audio &&
    !!textFields[0] &&
    !inspect &&
    !fields.some((f) => f.param.type !== "text" && errors[f.key]);
  const insert = (h: string) =>
    scriptField
      ? // a new line for the element at the script's end (the board's @ chip by the model chip)
        change(scriptField.key, `${script.trimEnd()}${script.trim() ? "\n" : ""}@${h}: `)
      : textFields[0] &&
        insertAtCaret(idOf(textFields[0].key), texts[0], h, (v) => change(textFields[0].key, v));
  // the reference control and the prompt's @mentions: in the tiles row, or on the empty screen in the bottom row
  // by Generate, as the board's + and @ (docs/mock/Direct.dc.html)
  // a video workflow's composer tiles are cards, label over value (docs/mock/TextToVideo.dc.html)
  const card = (video || audio) && !empty;
  // what opens the first tile's picker (the + chip)
  const firstPicker = useRef<(() => void) | null>(null);
  // its + and @ chips (docs/mock/TextToVideo.dc.html, VideoSource.dc.html); a workflow with its own add controls
  // (reference rows, image params, video or audio rows: docs/mock/CreateMediaRefs.dc.html) has none
  const plusAt =
    card &&
    !audio &&
    !mediaRowFields.length &&
    !imageInputs(fields).some((f) => f.param.type === "image" || !positionOf(f.param));
  const refsAndMentions = (
    <>
      {refField ? (
        <RefChips
          fields={fields}
          raws={raws}
          errors={errors}
          onChange={change}
          uploads={uploads}
          extra={<MentionChips texts={texts} />}
          card={card}
          frames={video}
          openerRef={mediaFields.length ? undefined : firstPicker}
        />
      ) : (
        <>
          {firstFrameIn && (
            <FirstFrameTile
              onPick={(id) => void applyUse(id, firstFrameIn.id)}
              card={card}
              openerRef={mediaFields.length ? undefined : firstPicker}
            />
          )}
          <MentionChips texts={texts} />
        </>
      )}
    </>
  );
  const shownWorkflow =
    workflows?.find((w) => w.id === (shown?.workflow_id ?? shownTake?.made_with?.workflow_id)) ??
    workflow;
  // the shown take's sample rate is the model's only while that take is this family's (another model's take stays
  // on the stage after a switch: Qwen3-TTS speaks 24 kHz, ZONOS3 44.1 kHz)
  const rateTake =
    shownTake?.kind === "audio" && shownWorkflow?.family === workflow?.family
      ? shownTake
      : undefined;
  const anyActive = !!runs?.some((r) => isActive(r.status));
  const bypass = choiceBypass(groups, fields, raws);
  // the estimate follows Run on: the plan there and the last run on that host (under Auto, the host the plan picks)
  const plan = usePlan(
    workflow && !blocked && !runOnOffline
      ? {
          workflow_id: workflow.id,
          inputs: formInputs,
          ...(lookIds.length && { looks: lookIds }),
          ...(runOn && { host: runOn.host }),
        }
      : null,
  );
  const took =
    workflow && runs
      ? lastDuration(runs, workflow.id, bypass, runOn?.host ?? plan.host ?? undefined)
      : null;
  const estimate = estimateText(
    took != null ? clock(took) : null,
    plan.gb,
    Object.values(bypass).some(Boolean),
  );
  const pinned = pinnedNote(runs?.find((r) => r.id === lastRun));
  // with several hosts of the env: the host an active run is lined up on, else the shown run's (contracts "Several hosts")
  const host = hostMemory(
    hosts,
    workflow?.env,
    runOn?.host ?? (runs?.find((r) => isActive(r.status) && r.host) ?? shown)?.host,
  );
  const session = sessions?.find((s) => s.id === sessionId);

  const topBar = (badge?: string) => (
    <div className="flex flex-wrap items-center gap-3">
      <button
        type="button"
        popoverTarget="sessions"
        aria-label={`Session: ${session?.title ?? "new"}. Change session`}
        className="flex h-10 max-w-[40ch] min-w-0 items-center gap-2 rounded-[10px] px-3 font-display text-lg font-bold"
        style={{ anchorName: "--sessions" } as React.CSSProperties}
      >
        <span className="truncate">{session?.title ?? "New session"}</span>
        <ChevronDown size={16} strokeWidth={2} aria-hidden className="shrink-0" />
      </button>
      <div
        id="sessions"
        popover="auto"
        role="dialog"
        aria-label="Sessions"
        className="pop w-72"
        style={
          {
            positionAnchor: "--sessions",
            positionArea: "bottom span-right",
            marginTop: 6,
          } as React.CSSProperties
        }
      >
        <div className="flex max-h-[60vh] flex-col gap-1 overflow-y-auto [&>*]:shrink-0">
          <button
            type="button"
            className="chip bevel mb-1 justify-center rounded-[10px] bg-s4"
            onClick={() => {
              document.getElementById("sessions")?.hidePopover();
              newSession().catch((e) => setError(message(e)));
            }}
          >
            <Plus size={14} aria-hidden /> New session
          </button>
          {sessions?.map((s) => (
            <button
              key={s.id}
              type="button"
              aria-current={s.id === sessionId || undefined}
              className="truncate rounded-[10px] px-3 py-2 text-left text-[13px] hover:bg-s3 aria-[current]:bg-s4 aria-[current]:font-semibold"
              onClick={() => {
                document.getElementById("sessions")?.hidePopover();
                if (s.id !== sessionId) open(s.id);
              }}
            >
              {s.title}
            </button>
          ))}
        </div>
      </div>
      {workflows && workflows.length > 0 && (
        <WorkflowPicker
          // the Image / Video switch is a filter on what the chip offers
          workflows={workflows.filter((w) => mediaOf(w) === media)}
          current={workflow}
          label={badge ?? (workflow ? workflowLabel : "Workflow")}
          onPick={switchTo}
          onChanged={(w) => onWorkflow(w, false)}
          onDeleted={(id) => {
            setWorkflows((list) => list?.filter((x) => x.id !== id) ?? null);
            if (chosen === id) setChosen("");
          }}
          onError={setError}
          available={(w) => downloaded(modelList, w.family)}
        />
      )}
      {/* a family with a voice-over and a dialogue template: the switch between them (docs/mock/CreateAudio.dc.html) */}
      {modes && (
        <div role="group" aria-label="Template" className="seg">
          {(
            [
              ["Voice-over", modes.voice],
              ["Dialogue", modes.dialogue],
            ] as const
          ).map(([label, w]) => (
            <button
              key={label}
              type="button"
              aria-pressed={workflow?.id === w.id}
              onClick={() => workflow?.id !== w.id && switchTo(w.id)}
            >
              {label}
            </button>
          ))}
        </div>
      )}
      <div className="grow" />
      {workflow && (
        <div
          role="group"
          aria-label="View"
          className="flex rounded-[10px] border border-s3 bg-s1 p-[3px]"
        >
          {(["studio", "graph"] as const).map((v) => (
            <button
              key={v}
              type="button"
              aria-pressed={view === v}
              title={`${v === "studio" ? "Studio" : "Graph"} (⌘ or Ctrl + \\)`}
              onClick={() => setView(v)}
              className="h-[34px] rounded-lg px-4 font-medium text-t2 aria-pressed:bg-s5 aria-pressed:text-t1"
            >
              {v === "studio" ? "Studio" : "Graph"}
            </button>
          ))}
        </div>
      )}
      {host && <Memory memory={host} />}
    </div>
  );

  if (view === "graph" && workflow)
    return (
      <>
        <GraphView
          workflow={workflow}
          palette={palette}
          topBar={(badge) => (
            <>
              {topBar(badge)}
              {error && <ErrorNote>{error}</ErrorNote>}
            </>
          )}
          overrides={buildInputs(fields, raws).inputs}
          onParamSet={(key) =>
            setRaws((r) => {
              const next = { ...r };
              delete next[key];
              return next;
            })
          }
          onWorkflow={onWorkflow}
          onProblems={onProblems}
          onError={setError}
          run={shown}
          track={shown && tracks[shown.id]}
          take={shownTake}
          hosts={hosts}
          generate={(w) => {
            const fs = fieldsOf(w);
            return send(w, visible(studioView(w, fs)?.shown ?? fs, raws, fs));
          }}
          busy={busy}
          anyActive={anyActive}
        />
      </>
    );

  return (
    <>
      <main id="main" className="flex min-w-0 flex-[999_1_560px] flex-col gap-3.5 px-5 pt-4 pb-5">
        {topBar()}

        {error && (
          <ErrorNote
            onRetry={
              !sessions
                ? load
                : runs === null && sessionId
                  ? () => {
                      setError("");
                      void loadRuns(sessionId);
                    }
                  : undefined
            }
          >
            {error}
          </ErrorNote>
        )}
        {workflows?.length === 0 && (
          <ErrorNote onRetry={load}>No workflows yet: no runner has published any.</ErrorNote>
        )}

        {spanField && sourceSet && sourceView ? (
          <SourceStage span={spanField} fields={fields} raws={raws} onChange={change} grid={grid} />
        ) : empty ? (
          <div className="flex flex-col items-center gap-2 pt-7 pb-1 text-center">
            <h1 className="m-0 font-display text-[40px] leading-[44px] font-bold tracking-[0.01em]">
              What will you <span className="text-a1">make?</span>
            </h1>
            <p className="m-0 text-[15px] text-t2">
              Describe an image or a clip. Pull in characters and places with @. Runs on this Mac.
            </p>
          </div>
        ) : (
          <Stage
            run={shown}
            take={shownTake}
            workflow={shownWorkflow}
            onStar={shownTake && (() => void star(shownTake))}
            onFull={() => setFull(true)}
            onDrop={refField && intoRefs}
            compare={
              comparing &&
              shownTake &&
              pictures.length > 1 && (
                <Compare
                  a={shownTake}
                  takes={pictures}
                  onPick={(id) => {
                    setSelected(id);
                    setComparing(false);
                  }}
                />
              )
            }
            onCompare={
              shownTake && pictures.length > 1 && pictures.includes(shownTake)
                ? () => setComparing((c) => !c)
                : undefined
            }
            onExplain={
              shownWorkflow && (shownTake?.made_with || shown)
                ? () => setExplaining(true)
                : undefined
            }
          />
        )}
        {explaining && shownWorkflow && (
          <ExplainDrawer
            workflow={shownWorkflow}
            take={shownTake}
            run={shown}
            onClose={() => setExplaining(false)}
          />
        )}
        {full && shownTake && (
          <FullScreen
            take={shownTake}
            onClose={() => setFull(false)}
            onStep={(d) => {
              const list = items.flatMap((i) => (i.take ? [i.take] : []));
              const next = list[list.findIndex((t) => t.id === shownTake.id) + d];
              if (next) setSelected(next.id);
            }}
          />
        )}

        {shown && shownWorkflow && (
          <RunStrip
            run={shown}
            workflow={shownWorkflow}
            track={tracks[shown.id]}
            onCancel={() => void cancel(shown.id)}
          />
        )}
        {shown && shownWorkflow && <ResolvedTexts run={shown} workflow={shownWorkflow} />}

        {!!items.length && (
          <div
            role="group"
            aria-label="Takes"
            className="flex shrink-0 items-center gap-2.5 overflow-x-auto p-0.5"
          >
            {items.map((it, i) =>
              it.take ? (
                <TakeTile
                  key={it.take.id}
                  take={it.take}
                  current={it === picked}
                  onPick={() => {
                    setSelected(it.take.id);
                    setSourceView(false);
                  }}
                />
              ) : (
                <RunTile
                  key={it.run.id}
                  run={it.run}
                  thumb={takes.find((t) => t.id === it.run.outputs[0]?.take_id)?.thumb_url}
                  n={items.length - i}
                  seedKey={seedKeyOf(workflows?.find((w) => w.id === it.run.workflow_id))}
                  current={it === picked}
                  onPick={() => {
                    setSelected(it.run.id);
                    setSourceView(false);
                  }}
                />
              ),
            )}
            {extendOffer && (
              <button
                type="button"
                className="chip bevel h-11 shrink-0 rounded-[10px]"
                title={offerLabel(extendOffer)}
                onClick={() => applyOffer(extendOffer)}
              >
                Extend
              </button>
            )}
            {shownTake && (
              <AddToTimeline
                key={`add-${shownTake.id}`}
                takeIds={[shownTake.id]}
                audio={shownTake.kind === "audio"}
                className="chip bevel h-11 shrink-0 rounded-[10px]"
              />
            )}
            {/* Save as voice (API 1.31): a cloned voice where a workflow clones, or the designed voice the take was
                made with, kept with its clip */}
            {shownTake?.kind === "audio" &&
              voiceCtx &&
              (voiceCtx.clone || designedOf(shownTake)) && (
                <SaveAsVoice
                  key={`voice-${shownTake.id}`}
                  take={shownTake}
                  ctx={voiceCtx}
                  onPick={
                    voiceFields[0] && ((voice_id) => change(voiceFields[0].key, { voice_id }))
                  }
                />
              )}
            {offers && (
              <UseIn
                offers={offers}
                onUse={applyOffer}
                video={shownTake?.kind === "video" ? shownTake.id : undefined}
                seed={shownTake?.made_with?.seed}
                familyTitle={(f) => palette?.groups.find((g) => g.family === f)?.title ?? f}
              />
            )}
          </div>
        )}

        <div
          className={`flex shrink-0 flex-wrap items-stretch gap-2.5 ${empty ? "w-full max-w-[980px] self-center" : ""}`}
        >
          {kinds.length > 1 && (
            <MediaSwitch
              kinds={kinds}
              media={media}
              onPick={(m) => {
                const id = workflows && workflowFor(workflows, runs ?? [], m, workflow?.id);
                if (id) switchTo(id);
              }}
            />
          )}
          <form
            onSubmit={generate}
            onPaste={(e) => {
              if (!e.defaultPrevented && intoRefs(e.clipboardData)) e.preventDefault();
            }}
            noValidate
            aria-label="Composer"
            className="panel flex min-w-0 flex-[1_1_520px] flex-col gap-3 bg-s2 px-4 py-3.5 [box-shadow:0_16px_40px_color-mix(in_srgb,var(--shd)_45%,transparent)]"
          >
            {(refField ||
              textFields.length > 0 ||
              tiles.length > 0 ||
              mediaFields.length > 0 ||
              mediaRowFields.length > 0 ||
              voiceFields.length > 0 ||
              !!scriptField ||
              paramCards.length > 0) && (
              // the empty screen's big tiles: equal columns, so a tile that wraps keeps their width
              <div
                className={
                  empty
                    ? "grid grid-cols-[repeat(auto-fill,minmax(150px,1fr))] gap-2"
                    : "flex flex-wrap items-center gap-2"
                }
              >
                {scriptField && linesKey && (
                  <CastTile
                    cast={cast}
                    label={fields.find((f) => f.key === linesKey)?.param.label ?? "Cast"}
                    onVoice={setVoiceFor}
                  />
                )}
                {voiceCtx &&
                  voiceFields.map((f) => (
                    <VoiceTile
                      key={f.key}
                      f={f}
                      raws={raws}
                      onChange={(k, v) => change(k, v)}
                      ctx={voiceCtx}
                    />
                  ))}
                {paramCards.map((f) => {
                  const raw = raws[f.key] ?? toRaw(f.initial, f.param);
                  const d = display(f, raw);
                  return (
                    <button
                      key={f.key}
                      type="button"
                      className={tileClass("card")}
                      onClick={() => show(f.key)}
                    >
                      <TileWords
                        big="card"
                        label={d.label}
                        value={f.param.type === "seed" && raw === "" ? "Random" : d.value || "—"}
                      />
                    </button>
                  );
                })}
                {mediaFields.map((f, i) => (
                  <MediaTile
                    key={f.key}
                    f={f}
                    fields={fields}
                    raws={raws}
                    onChange={change}
                    openerRef={i === 0 ? firstPicker : undefined}
                  />
                ))}
                {spanField && (
                  <SpanTile
                    f={spanField}
                    raws={raws}
                    onOpen={() => {
                      setSourceView(true);
                      document
                        .querySelector(
                          `[aria-labelledby="sec-g${groups?.findIndex((g) => g.fields.includes(spanField)) ?? -1}"]`,
                        )
                        ?.scrollIntoView({ block: "nearest" });
                    }}
                  />
                )}
                {!empty && refsAndMentions}
                {mediaRowFields.map((f) => (
                  <MediaRows
                    key={f.key}
                    f={f}
                    raw={raws[f.key]}
                    error={errors[f.key]}
                    onChange={change}
                    compact
                  />
                ))}
                <QuickTiles
                  tiles={tiles}
                  chosen={looksChosen}
                  mentioned={handles.length}
                  onElements={() => setElementsOpen(true)}
                  onLook={empty ? setInlineGroup : setLookGroup}
                  open={empty ? openGroup : undefined}
                  card={card}
                />
              </div>
            )}
            {scriptField && (
              <ScriptEditor
                f={scriptField}
                value={script}
                onChange={(v) => change(scriptField.key, v)}
                errors={lineErrors}
                cast={cast}
                onVoice={setVoiceFor}
              />
            )}
            <TextFields
              fields={(layout ? fields.filter((f) => layout.text.includes(f)) : fields).filter(
                (f) => !f.param.script,
              )}
              raws={raws}
              errors={errors}
              onChange={change}
              spoken={audio}
            />
            {note && (
              <p
                role={preview?.errors.length ? "alert" : "status"}
                className={`mono m-0 text-[12px] whitespace-pre-line ${preview?.errors.length ? "text-bad" : "text-t2"}`}
              >
                {note}
              </p>
            )}
            {reuseNote && (
              <p role="status" className="m-0 text-[12px] text-t2">
                {reuseNote}
              </p>
            )}
            {runNote && (
              <p
                role="status"
                className="m-0 flex items-center gap-2 self-start rounded-[10px] bg-s4 px-2.5 py-1.5 text-[12px]"
              >
                <InfoIcon size={14} aria-hidden />
                {runNote}
              </p>
            )}
            {blocked > 0 && (
              <p role="status" className="text-[12px] text-bad">
                This workflow has {blocked} problem{blocked === 1 ? "" : "s"}: open Graph to fix{" "}
                {blocked === 1 ? "it" : "them"}.
              </p>
            )}
            {fields.some((f) => f.param.type !== "text" && errors[f.key]) && (
              <ErrorNote>Some settings are not valid: see the Inspector.</ErrorNote>
            )}
            {workflow &&
              hosts &&
              !hosts.some((h) => h.env === workflow.env && h.state !== "offline") && (
                <p role="status" className="text-[12px] text-t2">
                  No runner for {workflow.env} is online; runs wait until one is.
                </p>
              )}
            <div className="flex flex-wrap items-center gap-2">
              {empty && refsAndMentions}
              {plusAt && (
                <>
                  {/* the first tile that opens a picker: the first frame, the source video or the audio */}
                  {(refField || firstFrameIn || mediaFields.length > 0) && (
                    <button
                      type="button"
                      className="chip bevel rounded-[10px] bg-s4"
                      aria-label="Add image, video or audio"
                      onClick={() => firstPicker.current?.()}
                    >
                      <Plus size={14} aria-hidden />
                    </button>
                  )}
                  {textFields[0] && (
                    <button
                      type="button"
                      className="chip bevel rounded-[10px] bg-s4"
                      aria-label="Insert element"
                      onClick={() => setElementsOpen(true)}
                    >
                      <AtSign size={14} aria-hidden />
                    </button>
                  )}
                </>
              )}
              {scriptField && (
                <button
                  type="button"
                  className="chip bevel rounded-[10px] bg-s4"
                  aria-label="Insert element"
                  onClick={() => setElementsOpen(true)}
                >
                  <AtSign size={14} aria-hidden />
                </button>
              )}
              <Chips
                fields={fields}
                raws={raws}
                onChange={change}
                show={show}
                compact={video || audio}
                toggles={toggles}
                models={chipModels}
                family={familyName}
                spoken={audio}
                versions={(f, o) => versionRow(modelList, runOn, f, o, allNeeds)}
                blocks={blocks}
                runOn={
                  <RunOnChip
                    list={machineList}
                    chosen={runOn}
                    env={workflow?.env}
                    picks={chosenVariants(all, raws, modelList)}
                    needs={[
                      ...needs.filter(
                        (n) =>
                          (raws[`${n.node}.bypass`] ?? workflow?.nodes[n.node]?.bypass) !== true,
                      ),
                      ...heldNeeds,
                    ]}
                    onPick={(h) => {
                      setRunOnHost(h);
                      setRunNote("");
                    }}
                  />
                }
              />
              {/* the shown take's sample rate (docs/mock/CreateAudio.dc.html: "24 kHz"; none before a take) */}
              {audio && rateTake?.audio?.sample_rate && (
                <span className="chip bevel rounded-[10px] bg-s4">
                  {+(rateTake.audio.sample_rate / 1000).toFixed(1)} kHz
                </span>
              )}
              {seedField && (
                <div
                  role="group"
                  aria-label="Seeds to queue"
                  className="bevel flex h-9 items-center rounded-[10px] bg-s4 text-[13px] font-medium"
                >
                  <button
                    type="button"
                    aria-label="Fewer seeds"
                    disabled={count <= 1}
                    onClick={() => setCount((n) => n - 1)}
                    className="grid size-9 place-items-center rounded-[10px] disabled:text-t2 disabled:opacity-60"
                  >
                    <Minus size={14} aria-hidden />
                  </button>
                  <span className="mono" aria-live="polite">
                    {count} / {MAX_QUEUE}
                  </span>
                  <button
                    type="button"
                    aria-label="More seeds"
                    disabled={count >= MAX_QUEUE}
                    onClick={() => setCount((n) => n + 1)}
                    className="grid size-9 place-items-center rounded-[10px] disabled:text-t2 disabled:opacity-60"
                  >
                    <Plus size={14} aria-hidden />
                  </button>
                </div>
              )}
              {/* the Seeds stepper sits with the chips, Generate at the end, bottom right even when the row wraps
                  (docs/mock/Direct.dc.html) */}
              <button
                type="submit"
                title="Generate (⌘ or Ctrl + Enter)"
                // a video workflow's key is the second accent (docs/mock/TextToVideo.dc.html)
                className={`key ml-auto flex h-14 flex-col items-center justify-center gap-px px-6 ${video || audio ? "bg-a2 text-[var(--on2)]" : ""}`}
                disabled={
                  !workflow ||
                  busy ||
                  blocked > 0 ||
                  // a Replace sound without its soundtrack (the row says why; docs/mock/H3Options.dc.html frame 6)
                  fields.some((f) => replaceUnmet(f.param, raws[f.key] ?? f.initial)) ||
                  noVoice ||
                  badLine > 0 ||
                  notDownloaded ||
                  runOnOffline ||
                  !!heldBlock
                }
              >
                <span className="text-sm">
                  {busy
                    ? "Sending…"
                    : notDownloaded
                      ? "Not downloaded"
                      : blocked
                        ? `${blocked} problem${blocked === 1 ? "" : "s"}`
                        : noVoice
                          ? "Pick a voice"
                          : badLine
                            ? "Generate"
                            : anyActive
                              ? "Add to queue"
                              : "Generate"}
                </span>
                {badLine > 0 && !busy ? (
                  <span className="mono text-[11px] font-medium">Fix line {badLine} first</span>
                ) : heldBlock && !busy && !runOnOffline ? (
                  <span className="mono text-[11px] font-medium">{heldBlock}</span>
                ) : runOnOffline && !busy ? (
                  <span className="mono text-[11px] font-medium">{runOn.label} is offline</span>
                ) : pinned && !busy ? (
                  <span className="mono text-[11px] font-medium" title={`The queued run ${pinned}`}>
                    {pinned}
                  </span>
                ) : wholeClip && !busy ? (
                  // a retake runs over the whole source clip, whatever the span (docs/mock/VideoSource.dc.html)
                  <span className="mono text-[11px] font-medium">{wholeClip}</span>
                ) : (
                  estimate &&
                  !busy && (
                    <span
                      className="mono text-[11px] font-medium"
                      title={[
                        took != null && `The last run of this workflow took ${clock(took)}`,
                        plan.gb != null && `Planned peak ${plan.gb} GB`,
                      ]
                        .filter(Boolean)
                        .join("; ")}
                    >
                      {estimate}
                    </span>
                  )
                )}
              </button>
            </div>
          </form>
        </div>
        {empty && openGroup && current && (
          <LooksSection
            group={openGroup}
            chosen={looksChosen}
            onChosen={setLooksChosen}
            reason={(x) => reasonOf(current, x, loraFamilies)}
            prompt={texts[0] ?? ""}
          />
        )}
        {(textFields[0] || scriptField) && (
          <ElementsPicker
            open={elementsOpen}
            onClose={() => setElementsOpen(false)}
            inPrompt={scriptField ? cast.map((c) => c.handle) : handles}
            onInsert={insert}
          />
        )}
        {voiceCtx && (
          <ElementVoicePicker
            el={voiceFor}
            ctx={voiceCtx}
            onClose={() => setVoiceFor(null)}
            onError={setError}
          />
        )}
        {current && (
          <LooksPicker
            group={lookGroup}
            onClose={() => setLookGroup(null)}
            chosen={looksChosen}
            onChosen={setLooksChosen}
            reason={(x) => reasonOf(current, x, loraFamilies)}
          />
        )}
      </main>

      <aside
        aria-label={elementsAside ? "Elements" : "Inspector"}
        className="aside m-3.5 flex flex-[1_1_320px] flex-col gap-[22px] px-[18px] pt-4 pb-6 md:ml-0"
      >
        {elementsAside ? (
          <ElementsPanel inPrompt={handles} onInsert={insert} />
        ) : (
          <Inspector
            groups={groups}
            resolved={shown?.workflow_id === workflow?.id ? shown?.resolved?.params : undefined}
            presets={workflow?.studio?.presets}
            presetOrder={presetNames(workflow?.studio)}
            presetScope={all}
            fields={fields}
            raws={raws}
            errors={errors}
            onChange={change}
            open={openKey}
            setOpen={setOpenKey}
            uploads={uploads}
            compact={video}
            grid={grid}
            // ponytail: by kind (LTX's upscaler doubles the stage-1 latent); a schema hint would make it generic.
            // Bypassed (the Recipe's one-stage) the clip itself is stage 1's size
            stage1={
              video && upscale
                ? { by: 2, clip: (raws[`${upscale}.bypass`] ?? false) === true }
                : undefined
            }
            familyTitle={
              palette?.groups.find((g) => g.family === workflow?.family)?.title ??
              (workflow?.source === "mine" ? undefined : workflow?.title)
            }
            voice={audio ? voiceCtx : undefined}
            blocks={blocks}
            valueBlocks={valueBlocks}
          />
        )}
        {audio && !elementsAside && <SpeechOutput take={rateTake} text={texts[0] ?? ""} />}
      </aside>
    </>
  );
}

/** A video workflow's name in the top bar: "LTX-2.5 · Text to video", the family then the rest of the title, or the
 * action when it says more ("LTX-2.5 · Retake a section", docs/mock/VideoSource.dc.html); else the title. */
export function videoTitle(w: Pick<S<"Workflow">, "title" | "action">, family?: string) {
  if (!family || !w.title.startsWith(family + " ")) return w.title;
  const rest = w.title.slice(family.length + 1);
  return `${family} · ${w.action?.startsWith(rest) ? w.action : rest}`;
}

/** The key of a workflow's seed param, if it has one. */
const seedKeyOf = (w?: S<"Workflow">) => w && fieldsOf(w).find((f) => f.param.type === "seed")?.key;

/** The run's size: its first output's, else what it asked of the canvas, else the workflow's. */
function sizeOf(run: Run, workflow?: S<"Workflow">) {
  const out = run.outputs[0];
  if (out?.width && out.height) return { w: out.width, h: out.height };
  const c = workflow && canvasOf(fieldsOf(workflow), {});
  if (!c) return null;
  const v = (f: Field, d: number) => Number(run.inputs[f.key] ?? d);
  return { w: v(c.width, c.w), h: v(c.height, c.h) };
}

/** The stage: the take as large as fits (with its star), the run's state, or the empty-state headline. */
function Stage({
  run,
  take,
  workflow,
  onStar,
  onFull,
  onDrop,
  compare,
  onCompare,
  onExplain,
}: {
  run?: Run;
  take?: Take;
  workflow?: S<"Workflow">;
  onStar?: () => void;
  onFull: () => void;
  /** Compare's view, shown in place of the take while it is open */
  compare?: React.ReactNode;
  /** Compare and Explain run (docs/mock/Main.dc.html); absent: not offered for what is shown */
  onCompare?: () => void;
  onExplain?: () => void;
  /** takes a drop of images or dragged takes as references; absent: the workflow takes none */
  onDrop?: (dt: DataTransfer) => boolean;
}) {
  const [over, setOver] = useState(false);
  const library = useLibrary();
  const out = take ?? run?.outputs[0];
  // the prompt the run was made with: its input, else the workflow's (inputs hold only what differs)
  const text = workflow && fieldsOf(workflow).find((f) => f.param.type === "text");
  const prompt = take?.made_with?.prompt ?? (text && (run?.inputs[text.key] ?? text.initial));
  const size =
    take?.width && take.height ? { w: take.width, h: take.height } : run && sizeOf(run, workflow);
  const p = run?.progress;
  const node = run?.status === "running" && p ? nodeLabel(workflow, p.node) : "";
  // which output's video is playing (a new take shown starts paused)
  const outKey = out && ("key" in out ? out.key : out.id);
  const [playingKey, setPlayingKey] = useState<string | undefined>();
  const playing = !!outKey && playingKey === outKey;
  const videoRef = useRef<HTMLVideoElement>(null);
  // an audio take's badge: "seed 12 · 24 kHz mono" (docs/mock/CreateAudio.dc.html)
  const sound = out?.kind === "audio";
  const outSeed = take?.made_with?.seed ?? (out && "seed" in out ? out.seed : undefined);
  const audioBadge =
    sound && (!run || run.status === "succeeded")
      ? [
          outSeed != null && `seed ${outSeed}`,
          // a dialogue's length and lines (docs/mock/CreateAudio.dc.html: "seed 7 · 0:09 · 3 lines")
          out.audio?.cues?.length && clock(out.audio.duration_s),
          out.audio?.cues?.length && `${out.audio.cues.length} lines`,
          out.audio?.sample_rate && `${+(out.audio.sample_rate / 1000).toFixed(1)} kHz mono`,
        ]
          .filter(Boolean)
          .join(" · ")
      : "";
  const label = audioBadge
    ? audioBadge
    : !run
      ? take
        ? "Take"
        : ""
      : node && p
        ? [`${node} · step ${p.step} of ${p.of}`, p.phase].filter(Boolean).join(" · ")
        : {
            queued: "Queued",
            running: "Starting",
            succeeded: "Take",
            failed: "Failed",
            cancelled: "Stopped",
          }[run.status];
  // what a screen reader hears: the state and the node, not every step
  const said = node ? `Running ${node}` : label;
  return (
    <div
      className={`panel relative min-h-[400px] grow bg-inset ${over ? "outline-2 -outline-offset-4 outline-a1 outline-dashed" : ""}`}
      onDragOver={(e) => {
        if (!onDrop || !carriesImage(e.dataTransfer)) return;
        e.preventDefault();
        setOver(true);
      }}
      onDragLeave={(e) => {
        if (!e.currentTarget.contains(e.relatedTarget as Node | null)) setOver(false);
      }}
      onDrop={(e) => {
        setOver(false);
        if (onDrop?.(e.dataTransfer)) e.preventDefault();
      }}
    >
      {over && (
        <span className="absolute inset-x-0 bottom-4 z-10 text-center text-[13px] font-medium">
          Drop to add as a reference
        </span>
      )}
      <span className="sr-only" aria-live="polite">
        {said}
      </span>
      {(run || take) && (
        <div className="absolute inset-x-4 top-4 flex flex-wrap items-center justify-between gap-2">
          <span
            className={`mono rounded-lg bg-s2/90 px-2.5 py-1.5 text-[12px] ${run?.status === "failed" ? "text-bad" : ""}`}
          >
            {label}
          </span>
          <span className="flex items-center gap-2">
            {out?.kind === "video" && (
              <button
                type="button"
                aria-label={playing ? "Pause" : "Play"}
                title={playing ? "Pause" : "Play"}
                onClick={() => {
                  const v = videoRef.current;
                  if (v) void (v.paused ? v.play() : v.pause());
                }}
                className="bevel grid size-9 place-items-center rounded-lg border border-s5 bg-s2/90"
              >
                {playing ? (
                  <Pause size={16} aria-hidden className="fill-current" />
                ) : (
                  <Play size={16} aria-hidden className="fill-current" />
                )}
              </button>
            )}
            {take && !sound && (
              <button
                type="button"
                aria-label="Full screen"
                title="Full screen (F)"
                onClick={onFull}
                className="bevel grid size-9 place-items-center rounded-lg border border-s5 bg-s2/90"
              >
                <Maximize size={16} strokeWidth={1.75} aria-hidden />
              </button>
            )}
            {onCompare && (
              <button
                type="button"
                aria-pressed={!!compare}
                onClick={onCompare}
                className="bevel h-9 rounded-lg border border-s5 bg-s2/90 px-3 text-[13px] aria-pressed:bg-s5"
              >
                Compare
              </button>
            )}
            {onExplain && (
              <button
                type="button"
                onClick={onExplain}
                className="bevel h-9 rounded-lg border border-s5 bg-s2/90 px-3 text-[13px]"
              >
                Explain run
              </button>
            )}
            {size && (
              <span className="mono rounded-lg bg-s2/90 px-2.5 py-1.5 text-[12px] text-t2">
                {size.w} × {size.h}
              </span>
            )}
            {take && onStar && !sound && (
              <button
                type="button"
                aria-pressed={take.starred}
                onClick={onStar}
                className="bevel flex h-9 items-center gap-1.5 rounded-lg border border-s5 bg-s2/90 px-3 text-[13px]"
              >
                <Star
                  size={14}
                  aria-hidden
                  className={take.starred ? "fill-a1 text-a1" : undefined}
                />
                {take.starred ? "Starred" : "Star"}
              </button>
            )}
          </span>
        </div>
      )}
      <div
        className="absolute inset-x-6 top-[68px] bottom-6 flex items-center justify-center"
        style={{ containerType: "size" }}
      >
        {/* with neither, Create shows its empty screen, not the Stage */}
        {!run && !take ? null : compare ? (
          compare
        ) : out?.kind === "audio" ? (
          <WavePlayer
            key={outKey}
            url={safeUrl(out.url)}
            duration={out.audio?.duration_s}
            cues={out.audio?.cues}
            nameOf={(v) => speakerOf(library, v)}
          />
        ) : out?.kind === "image" || out?.kind === "video" ? (
          <Media
            key={outKey}
            take={out}
            url={safeUrl(out.url)}
            alt={take?.title || (prompt ? String(prompt) : "The take")}
            videoRef={videoRef}
            onPlaying={(on) => setPlayingKey(on ? outKey : undefined)}
            className={`rounded-[10px] object-cover ${out.kind === "video" ? "bg-black" : ""}`}
            // as large as fits the stage, keeping its aspect, so the corners round the picture itself
            style={
              size
                ? {
                    width: `min(100cqw, 100cqh * ${size.w / size.h})`,
                    aspectRatio: `${size.w} / ${size.h}`,
                  }
                : { maxWidth: "100%", maxHeight: "100%" }
            }
          />
        ) : !run ? (
          <span className="text-t2">{take?.kind}</span>
        ) : (
          <div
            className="flex max-h-full w-full max-w-[760px] flex-col items-center justify-center gap-1.5 rounded-[10px] border border-dashed border-dash p-4 text-center text-t2"
            style={{ aspectRatio: size ? `${size.w} / ${size.h}` : "16 / 9" }}
          >
            {run.status === "failed" ? (
              <>
                <span className="text-[15px] font-medium text-bad">Failed</span>
                <span className="mono text-[12px] break-words text-t1">{run.error}</span>
              </>
            ) : (
              <span className="text-[15px] font-medium text-t1">
                {isActive(run.status)
                  ? `${Math.round(fraction(run) * 100)}%`
                  : run.status === "cancelled"
                    ? "Stopped"
                    : "No output"}
              </span>
            )}
          </div>
        )}
      </div>
    </div>
  );
}

/** The run's progress: one piece per working node (done with its time, active with its bar, pending), the time
 * left and Stop. */
function RunStrip({
  run,
  workflow,
  track,
  onCancel,
}: {
  run: Run;
  workflow: S<"Workflow">;
  track?: RunTrack;
  onCancel: () => void;
}) {
  const p = run.progress;
  return (
    <div
      role="group"
      aria-label="Run progress"
      className="flex shrink-0 flex-wrap items-center gap-2 text-[13px]"
    >
      {segments(workflow, run, track).map((s) =>
        s.state === "done" ? (
          <span key={s.key} className="step">
            <Check size={14} strokeWidth={2.5} className="text-a1" aria-label="done" />
            {s.title}
            {s.seconds != null && (
              <span className="mono text-t2">
                {s.seconds >= 60 ? clock(s.seconds) : `${s.seconds.toFixed(1)} s`}
              </span>
            )}
          </span>
        ) : s.state === "active" && p ? (
          <span
            key={s.key}
            className="step gap-2.5 border border-s5 bg-s2 font-medium"
            aria-current="step"
          >
            {s.bar ? s.title : `${s.title}…`}
            {s.bar && (
              <>
                <span
                  role="progressbar"
                  aria-label={`${s.title} progress`}
                  aria-valuemin={0}
                  aria-valuemax={p.of}
                  aria-valuenow={p.step}
                  className="block h-1 w-[120px] rounded-[2px] bg-s5"
                >
                  <span
                    className="block h-1 rounded-[2px] bg-a1 transition-[width]"
                    style={{ width: `${fraction(run) * 100}%` }}
                  />
                </span>
                <span className="mono font-normal">
                  {p.step} / {p.of}
                </span>
              </>
            )}
          </span>
        ) : (
          <span
            key={s.key}
            className={`step text-t2 ${s.state === "stopped" || s.state === "bypassed" ? "line-through" : ""}`}
          >
            {s.title}
            {s.state === "pending" && s.steps != null && (
              <span className="mono">{s.steps} steps</span>
            )}
          </span>
        ),
      )}
      <div className="grow" />
      {etaText(run) && <span className="mono text-t2">{etaText(run)}</span>}
      {isActive(run.status) && (
        <button
          type="button"
          className="bevel h-9 rounded-lg border border-s5 px-3 text-[13px]"
          onClick={onCancel}
        >
          Stop
        </button>
      )}
    </div>
  );
}

/** The texts the runner wrote that the user did not (`Run.resolved.texts`: an enhancer's prompt), each under its
 * node's title, one line until opened (docs/mock/CreateEnhance.dc.html). */
function ResolvedTexts({ run, workflow }: { run: Run; workflow: S<"Workflow"> }) {
  return Object.entries(run.resolved?.texts ?? {}).map(([path, text]) => {
    const node = path.split(".")[0];
    const title = workflow.node_schemas[workflow.nodes[node]?.kind]?.title ?? node;
    return (
      <details
        key={path}
        aria-label={title}
        className="shrink-0 rounded-xl border border-s3 bg-s1 px-3.5 py-2.5 text-[13px]"
      >
        <summary className="flex cursor-pointer list-none items-baseline gap-2">
          <span className="heading flex shrink-0 items-center">
            {title}
            <Info>What it wrote and the model drew from; the take keeps your prompt.</Info>
          </span>
          <span className="min-w-0 truncate text-[var(--t1b)]">{text}</span>
        </summary>
        <p className="mt-2 whitespace-pre-wrap text-[var(--t1b)]">{text}</p>
      </details>
    );
  });
}

/** The strip's label for a seed: a random seed is 16 digits, so the middle goes (the title keeps the whole). */
const seedLabel = (seed: number) => {
  const digits = String(seed);
  return `seed ${digits.length > 8 ? `${digits.slice(0, 4)}…${digits.slice(-3)}` : digits}`;
};

/** One take in the strip: its picture labelled by seed (or title), a star when starred. */
function TakeTile({ take, current, onPick }: { take: Take; current: boolean; onPick: () => void }) {
  const seed = take.made_with?.seed;
  const label = seed != null ? seedLabel(seed) : take.title;
  // a sound tile: the wave over "seed 12 · 0:07" (docs/mock/CreateAudio.dc.html)
  if (take.kind === "audio")
    return (
      <button
        type="button"
        aria-label={[
          seed != null ? `Take, seed ${seed}` : `Take, ${take.title}`,
          take.audio && clock(take.audio.duration_s),
          take.starred && "starred",
        ]
          .filter(Boolean)
          .join(", ")}
        aria-pressed={current}
        title={label}
        onClick={onPick}
        className={`bevel mono flex h-[72px] w-28 shrink-0 flex-col justify-between rounded-lg bg-s1 px-2 pt-2 pb-1.5 text-left text-[11px] text-t2 ${
          current ? "border-2 border-a2" : "border border-s5"
        }`}
      >
        <span aria-hidden className="wave block h-[30px] w-full" />
        <span className="truncate">
          {take.audio ? `${label} · ${clock(take.audio.duration_s)}` : label}
        </span>
      </button>
    );
  return (
    <button
      type="button"
      aria-label={[
        seed != null ? `Take, seed ${seed}` : `Take, ${take.title}`,
        take.video && secondsText(take.video.duration_s),
        take.starred && "starred",
      ]
        .filter(Boolean)
        .join(", ")}
      aria-pressed={current}
      title={label}
      onClick={onPick}
      draggable={take.kind === "image"}
      onDragStart={(e) => e.dataTransfer.setData(TAKE_MIME, take.id)}
      className={`bevel mono relative grid h-[63px] w-28 shrink-0 place-items-center overflow-hidden rounded-lg bg-s1 text-[11px] text-t2 ${
        current ? "border-2 border-a1" : "border border-s5"
      }`}
    >
      <Thumb take={take} badge={false} className="size-full" />
      <span className="absolute bottom-1 left-1 max-w-[calc(100%-8px)] truncate rounded bg-s2/90 px-1 text-t1">
        {take.video ? `${label} · ${secondsText(take.video.duration_s)}` : label}
      </span>
      {take.starred && (
        <Star size={12} aria-hidden className="absolute top-1 right-1 fill-a1 text-a1" />
      )}
    </button>
  );
}

/** A run in the strip that has no take to show: its picture labelled by seed, else its state. */
function RunTile({
  run,
  thumb,
  n,
  seedKey,
  current,
  onPick,
}: {
  run: Run;
  /** its output's take thumbnail, once that take is loaded; else the output itself */
  thumb?: string;
  n: number;
  seedKey?: string;
  current: boolean;
  onPick: () => void;
}) {
  const out = run.outputs[0];
  const asked = seedKey ? run.inputs[seedKey] : undefined;
  const seed = seedOf(run) ?? (typeof asked === "number" && asked >= 0 ? asked : undefined);
  const label = seed == null ? `take ${n}` : seedLabel(seed);
  const state =
    run.status === "running"
      ? `${Math.round(fraction(run) * 100)}%`
      : run.status === "succeeded"
        ? ""
        : run.status === "cancelled"
          ? "stopped"
          : run.status;
  return (
    <button
      type="button"
      aria-label={[seed != null ? `Take, seed ${seed}` : `Take ${n}`, state]
        .filter(Boolean)
        .join(", ")}
      aria-pressed={current}
      title={seed != null ? `seed ${seed}` : undefined}
      onClick={onPick}
      className={`bevel mono relative grid h-[63px] w-28 shrink-0 place-items-center overflow-hidden rounded-lg bg-s1 text-[11px] ${
        current ? "border-2 border-a1" : "border border-s5"
      } ${run.status === "failed" ? "text-bad" : "text-t2"}`}
    >
      {out ? (
        <>
          {thumb ? (
            <img
              src={safeUrl(thumb)}
              alt=""
              loading="lazy"
              decoding="async"
              className="size-full object-cover"
            />
          ) : (
            <Thumb take={out} badge={false} className="size-full" />
          )}
          <span className="absolute bottom-1 left-1 rounded bg-s2/90 px-1 text-t1">
            {out.video ? `${label} · ${secondsText(out.video.duration_s)}` : label}
          </span>
        </>
      ) : (
        <span className="flex flex-col items-center">
          <span>{label}</span>
          {state && <span>{state}</span>}
        </span>
      )}
    </button>
  );
}

/** The Image / Video / Audio switch beside the composer (docs/mock/TextToVideo.dc.html "Output kind"): which media's
 * workflows the model chip offers. */
function MediaSwitch({
  kinds,
  media,
  onPick,
}: {
  kinds: MediaKind[];
  media: MediaKind;
  onPick: (m: MediaKind) => void;
}) {
  return (
    <div
      role="tablist"
      aria-label="Output kind"
      className="flex shrink-0 flex-col gap-1.5 rounded-2xl bg-s1 p-1.5"
    >
      {kinds.map((m) => (
        <button
          key={m}
          type="button"
          role="tab"
          aria-selected={m === media}
          onClick={() => m !== media && onPick(m)}
          className="min-h-11 w-16 flex-1 rounded-xl text-[12px] font-semibold text-t2 aria-selected:bg-s5 aria-selected:text-t1"
        >
          {{ image: "Image", video: "Video", audio: "Audio", text: "Text" }[m]}
        </button>
      ))}
    </div>
  );
}

/** The strip's "Use in…": a menu of the take's offers (GET …/takes/{id}/uses); choosing one opens its workflow
 * with the take in it. */
function UseIn({
  offers,
  onUse,
  video,
  seed,
  familyTitle,
}: {
  offers: S<"TakeOffer">[];
  onUse: (o: S<"TakeOffer">) => void;
  /** a video take's id: the menu also offers its Last / First frame in… (API 1.37, docs/mock/H3Options.dc.html) */
  video?: string;
  /** the take's seed: the header "Use seed 12 in…" (docs/mock/CreateAudio.dc.html, TakeActions.dc.html) */
  seed?: number | null;
  familyTitle: (family: string) => string;
}) {
  if (!offers.length && !video) return null;
  return (
    <>
      <button
        type="button"
        popoverTarget="use-in"
        className="flex h-11 shrink-0 items-center gap-1.5 rounded-[10px] border border-dashed border-dash bg-transparent px-3.5 text-[13px]"
        style={{ anchorName: "--use-in" } as React.CSSProperties}
      >
        <Plus size={16} aria-hidden />
        Use in…
      </button>
      <div
        id="use-in"
        popover="auto"
        role="menu"
        aria-label="Use in"
        className="pop w-64"
        style={
          {
            positionAnchor: "--use-in",
            positionArea: "top span-left",
            marginBottom: 6,
          } as React.CSSProperties
        }
      >
        <div className="flex flex-col gap-1">
          <div className="flex items-center justify-between px-1 pb-1 text-[12px] text-t2">
            <span>{seed != null ? `Use ${seedLabel(seed)} in…` : "Use in…"}</span>
            <button
              type="button"
              aria-label="Close"
              className="grid size-6 place-items-center rounded-md"
              onClick={() => document.getElementById("use-in")?.hidePopover()}
            >
              <X size={14} aria-hidden />
            </button>
          </div>
          {offers.map((o) => (
            <button
              key={`${o.workflow_id} ${o.param}`}
              type="button"
              role="menuitem"
              aria-label={offerLabel(o)}
              className="flex items-center justify-between gap-3 rounded-[10px] bg-s3 px-3 py-2 text-left text-[13px] hover:bg-s4"
              onClick={() => {
                document.getElementById("use-in")?.hidePopover();
                onUse(o);
              }}
            >
              <span className="font-semibold">{o.action ?? o.title}</span>
              <span className="mono text-[11px] text-t2">{familyTitle(o.family)}</span>
            </button>
          ))}
          {video && (
            <FrameIn
              key={video}
              takeId={video}
              className="rounded-[10px] bg-s3 px-3 py-2 text-left text-[13px] hover:bg-s4"
              onUse={(o) => {
                document.getElementById("use-in")?.hidePopover();
                onUse(o);
              }}
            />
          )}
        </div>
      </div>
    </>
  );
}

/** Reuse's word on the take's elements: which changed, were renamed or deleted since (empty when all are current). */
function elementsNote(st: S<"ElementStatus">[]) {
  const said = st.flatMap((e) =>
    e.status === "deleted"
      ? [`@${e.handle} was deleted (the prompt's @${e.handle} is unknown now)`]
      : e.current_handle && e.current_handle !== e.handle
        ? [
            `@${e.handle} is @${e.current_handle} now (the prompt's @${e.handle} no longer names it)`,
          ]
        : e.status === "changed"
          ? [`@${e.handle} was edited: a new run uses it as it is now`]
          : [],
  );
  return said.length ? `Since this take: ${said.join("; ")}.` : "";
}

/** An audio workflow's Output section (docs/mock/CreateAudio.dc.html): the shown take's sound, and the length the
 * text comes to at 15 characters a second (contracts/README.md, Voices: `speech_seconds`). */
function SpeechOutput({ take, text }: { take?: Take; text: string }) {
  const rate = take?.kind === "audio" ? take.audio?.sample_rate : undefined;
  const words = text.trim().split(/\s+/).filter(Boolean).length;
  const chars = [...text].length;
  if (!rate && !words) return null;
  return (
    <section aria-labelledby="sec-output" className="flex flex-col gap-2">
      <h2 id="sec-output" className="heading">
        Output
      </h2>
      {rate && (
        <div className="flex justify-between gap-2.5 text-[13px]">
          <span>Sound</span>
          <span className="mono text-[12px] text-t2">
            {+(rate / 1000).toFixed(1)} kHz · mono · wav
          </span>
        </div>
      )}
      {words > 0 && (
        <div className="flex justify-between gap-2.5 text-[13px]">
          <span>Length</span>
          <span className="mono text-[12px] text-t2">
            ≈ {clock(chars / 15)} from {words} word{words === 1 ? "" : "s"}
          </span>
        </div>
      )}
    </section>
  );
}
