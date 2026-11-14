"use client";
// Catalog Agent, the screen: the left rail (Run: Input, Processing · Repository: SKU list, Gallery · Sessions ·
// the account), and Input or Processing beside it. The server session is the source of truth: this component loads
// it, follows its events (SSE, falling back to polling with ?since=), keeps the chat (session.messages) in step and
// makes every call through the BFF (/api/v1/catalog/*).
import { ChevronsUpDown, LayoutDashboard, ListTree, LogOut, Plus, RotateCcw, User, Users } from "lucide-react";
import Link from "next/link";
import { Fragment, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { toast } from "sonner";
import { signout } from "@/app/(auth)/actions";
import { useDevViews, useUser } from "@/components/app-shell";
import { BetaBadge } from "@/components/kit";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogTitle } from "@/components/ui/dialog";
import { ApiError, api, message, ok, okOr, postForm, type S } from "@/lib/api";
import { safeUrl, when } from "@/lib/format";
import {
  NEXT_BATCH,
  SKIP_NOTE,
  activeRuns,
  stoppedLine,
  activeCategories,
  poseNoun,
  withNoun,
  STEPS,
  WELCOME,
  applyEvent,
  released,
  withOpenQuestions,
  catName,
  cursorOf,
  descriptionPatch,
  exampleCat,
  withCustomPoses,
  guideStart,
  help,
  needsModel,
  sceneNote,
  usesModel,
  idList,
  insertBeforeWaiting,
  isStep,
  marker,
  mergeDetail,
  newId,
  nextFlow,
  plural,
  poseName,
  processingReady,
  readyCount,
  retire,
  sectionState,
  stepIntro,
  thinkingSteps,
  withCursor,
  type Action,
  type Detail,
  type Flow,
  type Item,
  type Library,
  type MemoryEntry,
  type Msg,
  type SessionEvent,
  type Sku,
  type Step,
  type Upload,
} from "@/lib/catalog";
import { ChatMessage, GuidedChat, SectionList, SectionPanel, noteItem, replyFor, skippedAsk } from "./input";
import { CatalogContext, ICON, railIcon } from "./kit";
import { Generation, type Pane, type SkuEdit } from "./processing";

/** The server calls the Input forms make; each answers the contract's slice and keeps the screen's copy current. */
export type Ops = {
  addRaw: (
    b: ({ files: File[]; kind: "folder" | "files" | "csv"; label: string } | { kind: "drive" | "website"; url: string }) & {
      families?: string[];
    },
  ) => Promise<S<"RawAccepted">>;
  addGuideline: (target: "guidelines" | "details", b: { file: File } | { url: string }) => Promise<S<"InputAdded">>;
  upload: (kind: "model" | "background" | "pose_ref", file: File, category?: string) => Promise<Upload>;
  suggestPoses: (briefs: S<"PoseSuggestRequest">["briefs"]) => Promise<S<"PoseSuggestion">>;
  favorite: (kind: "models" | "backgrounds" | "poses", id: string, on: boolean) => Promise<void>;
};

/** Which part of the screen is open: Input or one of Processing's panes. */
type ViewKey = "input" | Pane;
/** Every SessionEvent type: SSE sends each as a named event. */
const EVENT_TYPES = ["status", "log", "step", "question", "skus", "image", "run"];
/** Shorthand for a session path parameter. */
const sid = (session_id: string) => ({ params: { path: { session_id } } });
/** The opening screen shows no card until a step is picked; otherwise the panel follows the flow. */
const openingFor = (d: Detail, ms: Msg[]): Step | null =>
  ms.filter((m) => !m.phase && m.kind !== "cursor").length <= 1 ? null : isStep(d.session.flow_position) ? d.session.flow_position : null;

/** Saves a file the API signed a link for. */
function saveLink(url: string, name: string) {
  const a = document.createElement("a");
  a.href = safeUrl(url) ?? url;
  a.download = name;
  a.click();
}

/** Input steps whose processing results and questions stay hidden until Brand guidelines is answered (the demo asks
 * its conflict questions after that step). */
const HOLD_STEPS: string[] = ["raw", "guidelines"];

/** Catalog Agent for the signed-in user; `initialId` is the session to open (/sessions/<id>), else the newest. */

export function CatalogAgent({ initialId }: { initialId?: string }) {
  const [sessions, setSessions] = useState<S<"SessionSummary">[]>([]);
  const [detail, setDetail] = useState<Detail | null>(null);
  const [library, setLib] = useState<Library | null>(null);
  const libRef = useRef<Library | null>(null); // for callbacks that outlive a render (advance)
  const [libError, setLibError] = useState("");
  const [memory, setMemory] = useState<MemoryEntry[]>([]);
  const [next, setNext] = useState<S<"NextStep"> | null>(null);
  const [msgs, setMsgs] = useState<Msg[]>([]);
  const [view, setView] = useState<ViewKey>("input");
  const [expanded, setExpanded] = useState<Step | null>(null);
  const [railOpen, setRailOpen] = useState<boolean | null>(null); // null: open from md up, folded below
  const [confirmReset, setConfirmReset] = useState(false);
  const [loadError, setLoadError] = useState("");
  // the library as every view sees it: with this session's custom shots as poses
  const lib = useMemo(() => withCustomPoses(library, detail), [library, detail]);

  // The latest values, for callbacks that run after awaits and from the event stream.
  const dRef = useRef<Detail | null>(null);
  /** What the session's products call a pose, for copy written now (callbacks read the refs). */
  const nounNow = () => poseNoun(libRef.current, activeCategories(libRef.current, dRef.current?.skus ?? []));
  const msgsRef = useRef<Msg[]>([]);
  const memRef = useRef<MemoryEntry[]>([]);
  const processRef = useRef<string | null>(null);
  const saveTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const refreshTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

  /** Sets the session detail (state and ref); the rail's row of that session follows it (SKU count, stage, name). */
  const setD = useCallback((d: Detail | null) => {
    dRef.current = d;
    setDetail(d);
    if (!d) return;
    const { id, stage, project_name, updated_at } = d.session;
    const skus = d.skus.length;
    setSessions((l) =>
      l.map((x) => (x.id === id ? { ...x, stage, project_name, updated_at, sku_count: skus, counts: { ...x.counts, skus } } : x)),
    );
  }, []);
  /** Folds a mutation's answer into the detail. */
  const merge = useCallback(
    (u: Parameters<typeof mergeDetail>[1]) => dRef.current && setD(mergeDetail(dRef.current, u)),
    [setD],
  );

  /** Saves the chat: at once with `patch` (inputs, flow position) and the server's session back, else debounced. */
  const commit = useCallback(
    async (nextMsgs: Msg[], patch?: Omit<S<"SessionPatch">, "messages">) => {
      msgsRef.current = nextMsgs;
      setMsgs(nextMsgs);
      const d = dRef.current;
      if (!d) return;
      clearTimeout(saveTimer.current);
      const send = () =>
        ok(
          api.PATCH("/v1/catalog/sessions/{session_id}", {
            ...sid(d.session.id),
            body: { ...patch, messages: msgsRef.current.slice(-2000) as S<"ChatMessage">[] },
          }),
        );
      if (!patch) {
        saveTimer.current = setTimeout(() => void send().catch(() => {}), 600);
        return;
      }
      try {
        const s = await send();
        if (dRef.current?.session.id === s.id) setD({ ...dRef.current, session: s });
      } catch (e) {
        toast.error(message(e));
        throw e;
      }
    },
    [setD],
  );

  /** The Next step card, once Processing is open. */
  const loadNext = useCallback(async () => {
    const d = dRef.current;
    if (!d || d.session.stage !== "ready") return setNext(null);
    const n = await ok(api.GET("/v1/catalog/sessions/{session_id}/next-step", sid(d.session.id))).catch(() => null);
    if (n && dRef.current?.session.id === d.session.id) setNext(n);
  }, []);

  /** Moves the flow on from `from`: adds `extra`, then the next step's heading and question, and saves it all with
   * `patch`. Reaching the processing step while processing is already done goes straight on. */
  const advance = useCallback(
    async (from: Flow, extra: Msg[] = [], patch: Omit<S<"SessionPatch">, "messages"> = {}, update = (ms: Msg[]) => ms) => {
      const d = dRef.current;
      if (!d) return;
      let to = nextFlow(from);
      const added = [...extra];
      const enter = (step: Flow) => {
        if (isStep(step)) added.push(marker(step));
        if (step === "summary") added.push({ id: newId(), kind: "skuSummary" });
        const intro = stepIntro(step, memRef.current.find((m) => m.step === step), poseNoun(libRef.current, activeCategories(libRef.current, d.skus)));
        if (intro) added.push(intro);
      };
      if (to === "processing") {
        const done = processingReady(d);
        added.push(...released(msgsRef.current)); // sorting results and questions held during Brand guidelines
        added.push({
          id: `processing:${newId()}`,
          kind: "status",
          text: done ? "Done — SKUs and brand guidelines ready" : "Sorting images and reading brand guidelines…",
          done,
        });
        if (done) to = nextFlow(to);
      }
      // A batch of products shot without a model (furniture) passes the Models step with a note.
      if (to === "models" && !needsModel(libRef.current, d.skus)) {
        added.push(marker("models"), { id: newId(), kind: "agent", text: `${sceneNote(libRef.current, d.skus)}.` });
        patch = { ...patch, inputs: { ...patch.inputs, models: { skipped: true } } };
        to = nextFlow(to);
      }
      if (to !== "processing") enter(to);
      setD({ ...d, session: { ...d.session, flow_position: to } });
      setExpanded(to === "poses" ? "poses" : null);
      const kept = retire(update(msgsRef.current)).filter((m) => !(m.held && added.some((a) => a.id === m.id)));
      await commit([...kept, ...added], { ...patch, flow_position: to }).catch(() => {});
    },
    [commit, setD],
  );

  /** Leaves the processing step once the job is done and every blocking question is answered. The waiting line
   * moves below the questions it waited for and ticks there. */
  const maybeFinishProcessing = useCallback(
    (d: Detail) => {
      if (d.session.flow_position !== "processing" || !processingReady(d)) return;
      const line = msgsRef.current.find((m) => m.id.startsWith("processing:") && !m.done);
      const done = line ? [{ ...line, done: true, text: "Done — SKUs and brand guidelines ready" }] : [];
      void advance("processing", done, {}, (ms) => ms.filter((m) => m !== line));
    },
    [advance],
  );

  /** Reloads the session detail (after events), then the Next step card. */
  const refresh = useCallback(async () => {
    const id = dRef.current?.session.id;
    if (!id) return;
    const d = await ok(api.GET("/v1/catalog/sessions/{session_id}", sid(id))).catch(() => null);
    if (!d || dRef.current?.session.id !== id) return;
    setD({ ...d, session: { ...d.session, flow_position: dRef.current.session.flow_position } });
    if (!HOLD_STEPS.includes(dRef.current!.session.flow_position)) {
      const ms = withOpenQuestions(msgsRef.current, d.questions);
      if (ms !== msgsRef.current) await commit(ms).catch(() => {});
    }
    maybeFinishProcessing(dRef.current!);
    await loadNext();
  }, [commit, loadNext, maybeFinishProcessing, setD]);

  /** Turns an event into chat lines and schedules a reload of the detail. */
  const onEvent = useCallback(
    (ev: SessionEvent) => {
      if (ev.seq <= cursorOf(msgsRef.current)) return;
      const hold = HOLD_STEPS.includes(dRef.current?.session.flow_position ?? "");
      void commit(applyEvent(msgsRef.current, ev, false, hold));
      clearTimeout(refreshTimer.current);
      refreshTimer.current = setTimeout(() => void refresh(), 300);
    },
    [commit, refresh],
  );

  /** Opens a session: its detail, its chat and where the flow is. */
  const open = useCallback(
    async (id: string) => {
      const d = await ok(api.GET("/v1/catalog/sessions/{session_id}", sid(id)));
      const saved = (d.session.messages as Msg[]) ?? [];
      const base = saved.some((m) => !m.phase && m.kind !== "cursor") ? saved : [WELCOME, ...saved];
      const ms = HOLD_STEPS.includes(d.session.flow_position) ? base : withOpenQuestions(base, d.questions);
      msgsRef.current = ms;
      setMsgs(ms);
      setD(d);
      setNext(null);
      setView(d.session.flow_position === "done" ? "processing" : "input");
      setExpanded(openingFor(d, ms));
      history.replaceState(null, "", `/sessions/${id}`);
      await loadNext();
    },
    [loadNext, setD],
  );

  /** A new, empty session, opened. */
  const newSession = useCallback(async () => {
    const d = await ok(api.POST("/v1/catalog/sessions", { body: {} }));
    setSessions((l) => [
      {
        id: d.session.id,
        project_name: d.session.project_name,
        stage: d.session.stage,
        sku_count: 0,
        counts: { skus: 0, not_run: 0, running: 0, to_review: 0, approved: 0, images_approved: 0 },
        created_at: d.session.created_at,
        updated_at: d.session.updated_at,
      },
      ...l,
    ]);
    await open(d.session.id);
  }, [open]);

  /** The library (agents publish it; 503 until then). */
  const loadLibrary = useCallback(async () => {
    try {
      const l = await ok(api.GET("/v1/catalog/library"));
      libRef.current = l;
      setLib(l);
      setLibError("");
    } catch (e) {
      setLibError(message(e));
    }
  }, []);
  /** The brand's last-used inputs. */
  const loadMemory = useCallback(async () => {
    const m = await ok(api.GET("/v1/catalog/memory")).catch(() => null);
    if (m) {
      memRef.current = m.memory;
      setMemory(m.memory);
    }
  }, []);

  // First load: library, memory and the sessions list, then the session to open (a new one when there is none).
  useEffect(() => {
    let live = true;
    (async () => {
      await Promise.all([loadLibrary(), loadMemory()]);
      const list = await ok(api.GET("/v1/catalog/sessions"));
      if (!live) return;
      setSessions(list.sessions);
      const id = list.sessions.find((s) => s.id === initialId)?.id ?? list.sessions[0]?.id;
      await (id ? open(id) : newSession());
    })().catch((e) => live && setLoadError(message(e)));
    return () => {
      live = false;
    };
  }, [initialId, loadLibrary, loadMemory, newSession, open]);

  // The session's events: SSE from the chat's cursor; when the stream can't be opened, JSON polling with ?since=.
  const sessionId = detail?.session.id;
  useEffect(() => {
    if (!sessionId) return undefined;
    const base = `/api/v1/catalog/sessions/${sessionId}/events`;
    let stop = false;
    let poll: ReturnType<typeof setTimeout> | undefined;
    const es = new EventSource(`${base}?since=${cursorOf(msgsRef.current)}`);
    const handle = (e: MessageEvent) => {
      try {
        onEvent(JSON.parse(e.data) as SessionEvent);
      } catch {
        /* a keep-alive or a broken line */
      }
    };
    for (const t of EVENT_TYPES) es.addEventListener(t, handle);
    /** One poll, then the next in 2 s. */
    const tick = async () => {
      const r = await fetch(`${base}?since=${cursorOf(msgsRef.current)}`, { headers: { accept: "application/json" } }).catch(() => null);
      const page = r?.ok ? ((await r.json()) as S<"EventPage">) : null;
      page?.events.forEach(onEvent);
      if (!stop) poll = setTimeout(() => void tick(), 2000);
    };
    es.onerror = () => {
      if (es.readyState === EventSource.CLOSED && !stop && !poll) void tick();
    };
    return () => {
      stop = true;
      es.close();
      clearTimeout(poll);
    };
  }, [sessionId, onEvent]);

  /** Runs a call, showing its error as a toast; resolves to undefined on failure. */
  const attempt = async <T,>(fn: () => Promise<T>): Promise<T | undefined> => {
    try {
      return await fn();
    } catch (e) {
      toast.error(e instanceof ApiError && e.status === 503 ? `${message(e)} Try again in a moment.` : message(e));
      return undefined;
    }
  };

  const ops: Ops = {
    addRaw: async (b) => {
      const id = dRef.current!.session.id;
      const r =
        "files" in b
          ? await (() => {
              const fd = new FormData();
              for (const f of b.files) fd.append("files", f);
              // a file's path inside the picked folder: the server only sees base names, and each sub-folder is a product
              for (const f of b.files) fd.append("paths", (f.webkitRelativePath || f.name).slice(-512));
              fd.append("kind", b.kind);
              fd.append("label", b.label.slice(0, 200));
              for (const f of b.families ?? []) fd.append("families", f);
              return postForm<S<"RawAccepted">>(`/v1/catalog/sessions/${id}/raw`, fd);
            })()
          : await ok(api.POST("/v1/catalog/sessions/{session_id}/raw", { ...sid(id), body: b }));
      processRef.current = r.process_id;
      setD({ ...dRef.current!, session: { ...r.session, flow_position: dRef.current!.session.flow_position } });
      return r;
    },
    addGuideline: async (target, b) => {
      const id = dRef.current!.session.id;
      let r: S<"InputAdded">;
      if ("file" in b) {
        const fd = new FormData();
        fd.append("file", b.file);
        fd.append("target", target);
        r = await postForm<S<"InputAdded">>(`/v1/catalog/sessions/${id}/guidelines`, fd);
      } else r = await ok(api.POST("/v1/catalog/sessions/{session_id}/guidelines", { ...sid(id), body: { url: b.url, target } }));
      setD({ ...dRef.current!, session: { ...r.session, flow_position: dRef.current!.session.flow_position } });
      return r;
    },
    upload: async (kind, file, category) => {
      const fd = new FormData();
      fd.append("file", file);
      fd.append("kind", kind);
      fd.append("name", file.name.slice(0, 200));
      if (category) fd.append("category", category);
      const u = await postForm<Upload>(`/v1/catalog/sessions/${dRef.current!.session.id}/uploads`, fd);
      const d = dRef.current!;
      setD({ ...d, session: { ...d.session, uploads: [...d.session.uploads, u] } });
      return u;
    },
    suggestPoses: (briefs) =>
      ok(api.POST("/v1/catalog/sessions/{session_id}/poses/suggest", { ...sid(dRef.current!.session.id), body: { briefs } })),
    favorite: async (kind, item_id, on) => {
      const req = { params: { path: { kind, item_id } } };
      const f = await attempt(() =>
        ok(on ? api.PUT("/v1/catalog/favorites/{kind}/{item_id}", req) : api.DELETE("/v1/catalog/favorites/{kind}/{item_id}", req)),
      );
      if (f) setLib((l) => l && { ...l, favorites: f });
    },
  };

  if (loadError)
    return (
      <div className="flex h-full items-center justify-center">
        <div className="card max-w-md p-6 text-center text-sm text-gray-300">
          <p>Couldn’t open Catalog Agent: {loadError}</p>
          <button type="button" onClick={() => location.reload()} className="btn-primary mt-4 px-4 py-2 text-xs">
            Try again
          </button>
        </div>
      </div>
    );
  if (!detail)
    return (
      <div className="flex h-full items-center justify-center text-sm text-gray-400">
        <span className="mr-2 h-4 w-4 animate-spin rounded-full border-2 border-accent border-t-transparent" /> Opening your catalog…
      </div>
    );

  const s = detail.session;
  const flow = s.flow_position;
  const inputs = s.inputs;
  const readyN = readyCount(inputs);
  const inputMsgs = msgs.filter((m) => !m.phase && m.kind !== "cursor" && !m.held);
  const log = msgs.filter((m) => m.phase === "p");

  /** Adds Processing lines to the chat (saved debounced). */
  const say = (...lines: Omit<Msg, "id">[]) =>
    void commit([...msgsRef.current, ...lines.map((l) => ({ id: newId(), phase: "p" as const, ...l }))]);

  /** Choosing a step from the list puts its heading in the chat; a skipped one asks whether to add something now. */
  const select = (key: Step) => {
    const last = [...msgsRef.current].reverse().find((m) => m.kind === "step");
    const heading = last?.section !== key ? [marker(key)] : [];
    setView("input");
    if (s.stage === "setup" && sectionState(inputs[key]) === "skipped") {
      setExpanded(null);
      void commit([...msgsRef.current, ...heading, { id: newId(), ...skippedAsk(key, nounNow()) }]);
      return;
    }
    setExpanded(key);
    if (heading.length) void commit([...msgsRef.current, ...heading]);
  };

  /** After something was added to a step (by the server or a PATCH): the chat confirms it, and on the current step
   * the flow moves on. `patch` saves picked items. */
  const afterAdded = async (step: Step, items: Item[], replace: boolean, patch?: S<"SessionPatch">["inputs"]) => {
    const count = (i: Item) => (i.image_count != null ? plural(i.image_count, "image") : null);
    const extra: Msg[] = items.map((i) => ({ id: newId(), kind: "item", section: step, label: i.label, detail: i.detail ?? count(i) }));
    extra.push({ id: newId(), kind: "agent", text: replace ? withNoun(`Updated your ${step === "output" ? "resolution & ratio" : step}.`, nounNow()) : replyFor(step, items[0], nounNow()) });
    if (step === "raw" && processRef.current)
      extra.push({ id: `bg:${processRef.current}`, kind: "status", text: "Processing images…", done: false });
    const p = patch ? { inputs: patch } : undefined;
    if (step === flow && !replace) return advance(step, extra, p ?? {});
    setExpanded(null);
    await commit(insertBeforeWaiting(msgsRef.current, extra), p).catch(() => {});
  };

  /** A step's panel finished: server-added items arrive with ids, picks (id "") are saved here first. */
  const onAdded = (step: Step) => async (items: Item[], replace: boolean) => {
    if (items[0]?.id) return afterAdded(step, items, false);
    const entry = { ...items[0], id: newId() };
    const list = replace ? [entry] : [...inputs[step].items, entry];
    await afterAdded(step, [entry], replace, { [step]: { items: list, skipped: false } });
  };

  /** × on an item. */
  const removeItem = (step: Step, itemId: string) =>
    void commit(msgsRef.current, { inputs: { [step]: { items: inputs[step].items.filter((i) => i.id !== itemId) } } }).catch(() => {});

  /** Skip (or Undo skip) from a step's panel. */
  const skip = (step: Step, skipped: boolean) => {
    const patch = { inputs: { [step]: { skipped } } };
    if (skipped && step === flow)
      return void advance(step, [{ id: newId(), kind: "skip", section: step }, { id: newId(), kind: "agent", text: withNoun(SKIP_NOTE[step], nounNow()) }], patch);
    void commit(skipped ? [...msgsRef.current, { id: newId(), kind: "skip", section: step }] : msgsRef.current, patch).catch(() => {});
  };

  /** Start processing: the server resolves every brief and saves memory; Processing opens with its setup card. */
  const start = async (msgId: string) => {
    const r = await attempt(() => ok(api.POST("/v1/catalog/sessions/{session_id}/start", sid(s.id))));
    if (!r) return;
    setD({ ...mergeDetail(dRef.current!, { skus: r.skus }), session: { ...r.session, flow_position: "done" } });
    const ms = msgsRef.current.map((m) => (m.id === msgId ? { ...m, answered: "generation" } : m));
    const opening: Msg[] = ms.some((m) => m.phase === "p")
      ? []
      : [
          { id: newId(), kind: "thinking", phase: "p", steps: thinkingSteps(r.skus, lib, r.summary.sample_sku_id), done: false },
          { ...guideStart(exampleCat(lib, r.skus), poseNoun(lib, activeCategories(lib, r.skus))), id: newId() },
        ];
    await commit([...ms, ...opening], { flow_position: "done" }).catch(() => {});
    setView("processing");
    void loadMemory();
    void loadNext();
  };

  /** A button in the Input chat. */
  const onAction = async (msgId: string, action: Action) => {
    const m = msgsRef.current.find((x) => x.id === msgId);
    const answered = (ms: Msg[]) => ms.map((x) => (x.id === msgId ? { ...x, answered: action.key } : x));
    if (action.key.startsWith("open:")) return select(action.key.slice(5) as Step);
    if (action.key.startsWith("answer:")) {
      const r = await attempt(() =>
        ok(
          api.POST("/v1/catalog/questions/{question_id}/answer", {
            params: { path: { question_id: m!.question_id! } },
            body: { option: Number(action.key.slice(7)) as 0 | 1 },
          }),
        ),
      );
      if (!r) return;
      merge({ questions: [r.question], skus: r.skus });
      const ms = msgsRef.current;
      const at = ms.findIndex((x) => x.id === msgId) + 1; // the answer goes under its question
      const reply: Msg[] = [
        { id: newId(), kind: "user", text: action.label },
        { id: newId(), kind: "agent", text: `Got it — ${action.label.charAt(0).toLowerCase()}${action.label.slice(1)}.` },
      ];
      await commit([...ms.slice(0, at), ...reply, ...ms.slice(at)]);
      maybeFinishProcessing(dRef.current!);
      return;
    }
    if (action.key === "generation") return start(msgId);
    if (action.key === "write") return onAdded("details")([{ ...noteItem(action.label), id: "" }], false);
    const step = m?.step;
    if (!isStep(step)) return;
    if (action.key === "change") {
      await commit(answered(msgsRef.current));
      setExpanded(step);
      return;
    }
    if (action.key === "skip")
      return advance(
        step,
        [{ id: newId(), kind: "user", text: action.label }, { id: newId(), kind: "agent", text: withNoun(SKIP_NOTE[step], nounNow()) }],
        { inputs: { [step]: { skipped: true } } },
        answered,
      );
    if (action.key === "reuse") {
      const items = (memRef.current.find((x) => x.step === step)?.items ?? []).map((i) => ({ ...i, id: newId(), reused: true }));
      return advance(
        step,
        [
          { id: newId(), kind: "user", text: action.label },
          ...items.map((i) => ({ id: newId(), kind: "item", section: step, label: i.label, detail: i.detail, reused: true, item: i })),
        ],
        { inputs: { [step]: { items: [...inputs[step].items, ...items], skipped: false } } },
        answered,
      );
    }
  };

  /** Refresh: Input clears for the next batch; SKUs, runs, images and the Processing chat stay. */
  const refreshInput = async () => {
    const d = await attempt(() => ok(api.POST("/v1/catalog/sessions/{session_id}/refresh", sid(s.id))));
    if (!d) return;
    setD(d);
    await commit(withCursor([NEXT_BATCH, ...msgsRef.current.filter((x) => x.phase === "p")], cursorOf(msgsRef.current)), {}).catch(() => {});
    setView("input");
    setExpanded(null);
  };

  /** Start Over (after the confirmation): this session's inputs, chat, SKUs and images are deleted in
   * place (same id, so its history entry shows it empty); memory stays. */
  const reset = async () => {
    setConfirmReset(false);
    const d = await attempt(() => ok(api.POST("/v1/catalog/sessions/{session_id}/reset", sid(s.id))));
    if (!d) return;
    setD(d);
    setNext(null);
    await commit(withCursor([WELCOME], cursorOf(msgsRef.current)), {}).catch(() => {});
    setView("input");
    setExpanded(null);
  };

  /** A run was queued: fold it in, say so, and reload the card. */
  const ranRun = (r: S<"RunAccepted">, line = true) => {
    merge({ run: r.run, images: r.images, skus: r.skus });
    const codes = r.skus.map((x) => x.code);
    toast(`Running ${idList(codes)} — ${plural(r.images.length, "image")}.`);
    if (line) say({ kind: "update", icon: "run", text: `Running ${idList(codes)}…` });
    void loadNext();
  };

  /** Zips the approved images and hands over the file. */
  const download = async () => {
    const r = await attempt(() => ok(api.POST("/v1/catalog/sessions/{session_id}/download", sid(s.id))));
    if (!r) return;
    saveLink(r.url, r.file_name);
    toast(`Packed ${plural(r.skus, "SKU")} (${plural(r.images, "image")}) into ${r.file_name}.`);
    say({ kind: "update", icon: "download", text: `Packed the approved images into ${r.file_name}.` });
  };

  /** Runs exactly these SKUs (reruns allowed). */
  const runSkus = async (ids: string[], missingOnly = false) => {
    const r = await attempt(() =>
      ok(api.POST("/v1/catalog/sessions/{session_id}/runs", { ...sid(s.id), body: { scope: "skus", sku_ids: ids, missing_only: missingOnly } })),
    );
    if (r) ranRun(r);
  };

  /** Stop: every queued or running run fails "Stopped" and its generating images go, so each SKU shows what it showed
   * before; images already finished stay. The chat line comes with the status event. A run that ended meanwhile
   * (409) needs nothing: the detail is reloaded. */
  const stop = async () => {
    let ended = false;
    for (const run of activeRuns(dRef.current?.runs ?? [])) {
      const r = await attempt(() =>
        okOr(api.POST("/v1/catalog/sessions/{session_id}/runs/{run_id}/stop", { params: { path: { session_id: s.id, run_id: run.id } } }), 409),
      );
      if (r === null) ended = true;
      else if (r) merge({ run: r.run, skus: r.skus, removed: r.removed_image_ids });
    }
    await (ended ? refresh() : loadNext());
  };

  /** Stops just these images (a tile's, a SKU row's generating ones); the rest of their runs keep going. A run left
   * with nothing generating is stopped whole and its status event says so; otherwise the chat line is said here.
   * Nothing generating any more (409) needs nothing: the detail is reloaded. */
  const stopImages = async (ids: string[], what: string) => {
    const r = await attempt(() => okOr(api.POST("/v1/catalog/sessions/{session_id}/images/stop", { ...sid(s.id), body: { image_ids: ids } }), 409));
    if (r === null) return refresh();
    if (!r) return;
    r.runs.forEach((run) => merge({ run }));
    merge({ skus: r.skus, removed: r.removed_image_ids });
    if (!r.runs.some((run) => run.status === "failed")) say({ kind: "update", icon: "redo", text: stoppedLine(what) });
    void loadNext();
  };

  /** Approves images: one with PATCH, several in one call. A fresh status bar follows. */
  const approve = async (ids: string[]) => {
    const r = await attempt(async () => {
      if (ids.length === 1) {
        const x = await ok(api.PATCH("/v1/catalog/images/{image_id}", { params: { path: { image_id: ids[0] } }, body: { status: "approved" } }));
        return { approved: 1, images: [x.image], skus: [x.sku] };
      }
      return ok(api.POST("/v1/catalog/sessions/{session_id}/approve", { ...sid(s.id), body: { image_ids: ids } }));
    });
    if (!r) return;
    merge({ images: r.images, skus: r.skus });
    toast(`Approved ${plural(r.approved, "image")}.`);
    void commit([...msgsRef.current.filter((x) => x.kind !== "statusbar"), { id: newId(), kind: "statusbar", phase: "p" }]);
    void loadNext();
  };

  /** Keeps another render try of an image as the image (the brand's pick over the reviewer's best). */
  const pickTry = async (imageId: string, n: number) => {
    const r = await attempt(() => ok(api.POST("/v1/catalog/images/{image_id}/pick", { params: { path: { image_id: imageId } }, body: { attempt: n } })));
    if (!r) return;
    merge({ images: [r.image], skus: [r.sku] });
    toast(`Using try ${n} for this image.`);
  };

  /** Regenerates only these images with the feedback (at `ratio` when one is picked); the old versions stay. */
  const regenerate = async (ids: string[], feedback: string, ratio?: string) => {
    const r = await attempt(() =>
      ok(api.POST("/v1/catalog/sessions/{session_id}/regenerate", { ...sid(s.id), body: { image_ids: ids, feedback, ratio } })),
    );
    if (!r) return;
    const images = ids.map((id) => detail.images.find((x) => x.id === id));
    const which =
      ids.length <= 3
        ? images.map((im) => `${detail.skus.find((k) => k.id === im?.sku_id)?.code} ${poseName(lib, im?.shot ?? "")}`).join(", ")
        : plural(ids.length, "image");
    const products = new Set(images.map((im) => im?.sku_id)).size;
    merge({ run: r.run, images: r.images, skus: r.skus });
    toast(`Regenerating ${plural(ids.length, "image")} across ${plural(products, "product")} with “${feedback}”${ratio ? ` at ${ratio}` : ""}. Other shots stay as they are.`);
    say({ kind: "user", text: `${which}: “${feedback}”` }, { kind: "update", icon: "redo", text: `Regenerating ${plural(ids.length, "image")}.` });
    void loadNext();
  };

  /** The edit drawer's Save / Save & rerun. */
  const editSku = async (sku: Sku, e: SkuEdit) => {
    const body: S<"SkuPatch"> = {};
    const cat = e.category ?? sku.category;
    if (cat !== sku.category) body.category = cat;
    // a move clears the subcategory unless one is picked; else only a changed one is sent
    if (e.subcategory !== undefined && e.subcategory !== (body.category ? null : sku.subcategory)) body.subcategory = e.subcategory;
    if (e.wearer !== undefined && e.wearer !== (sku.attributes.wearer ?? null)) body.wearer = e.wearer;
    if (e.model && usesModel(lib, cat) && (e.model !== sku.brief.model || e.applyAll)) body.model = e.model;
    if (e.background && (e.background !== sku.brief.background || e.applyAll)) body.background = e.background;
    const modelText = descriptionPatch(e.modelDescription, sku.brief.model_description, e.applyAll);
    if (modelText !== undefined && usesModel(lib, cat)) body.model_description = modelText;
    const backgroundText = descriptionPatch(e.backgroundDescription, sku.brief.background_description, e.applyAll);
    if (backgroundText !== undefined) body.background_description = backgroundText;
    // the drawer sets a whole map only when it changed: it replaces the brief's
    if (!body.category && e.poseRatios) body.pose_ratios = e.poseRatios;
    if (!body.category && e.poseNotes) body.pose_notes = e.poseNotes;
    let changed = [sku];
    if (Object.keys(body).length) {
      if (e.applyAll) body.apply_to_category = true;
      const r = await attempt(() => ok(api.PATCH("/v1/catalog/skus/{sku_id}", { params: { path: { sku_id: sku.id } }, body })));
      if (!r) return;
      merge({ skus: r.skus });
      changed = r.skus.length ? r.skus : changed;
      toast(`Saved ${idList(changed.map((x) => x.code))}.`);
    }
    if (e.rerun) await runSkus(changed.map((x) => x.id));
  };

  /** The pose editor's Use {n} poses (or Cancel): the category's SKUs not approved take them; SKUs already run
   * generate only the added poses. */
  const savePoses = async (m: Msg, poses: string[] | null) => {
    const ms = msgsRef.current.map((x) => (x.id === m.id ? { ...x, done: true } : x));
    const cat = m.category ?? "";
    const name = catName(lib, cat);
    const noun = poseNoun(lib, cat);
    if (!poses) return void commit([...ms, { id: newId(), phase: "p", kind: "agent", text: withNoun("Keeping the poses as they are.", noun) }]);
    void commit([...ms, { id: newId(), phase: "p", kind: "user", text: withNoun(`Poses for ${name}: `, noun) + poses.map((p) => poseName(lib, p)).join(", ") }]);
    const target = detail.skus.find((k) => k.category === cat && k.status !== "Approved");
    if (!target) return say({ kind: "agent", text: `${name}: every SKU is approved already.` });
    const before = target.brief.poses;
    const r = await attempt(() =>
      ok(api.PATCH("/v1/catalog/skus/{sku_id}", { params: { path: { sku_id: target.id } }, body: { poses, apply_to_category: true } })),
    );
    if (!r) return;
    merge({ skus: r.skus });
    const added = poses.filter((p) => !before.includes(p));
    const ran = r.skus.filter((k) => k.status !== "Not run");
    const names = poses.map((p) => poseName(lib, p)).join(", ");
    const more = added.length && ran.length ? ` Generating the new ${added.length === 1 ? noun.singular : noun.plural} for ${idList(ran.map((k) => k.code))}.` : "";
    say({ kind: "update", icon: more ? "redo" : "done", text: `${name} ${noun.plural} are now: ${names}.${more}` });
    if (more) {
      const run = await attempt(() =>
        ok(api.POST("/v1/catalog/sessions/{session_id}/runs", { ...sid(s.id), body: { scope: "skus", sku_ids: ran.map((k) => k.id), missing_only: true } })),
      );
      if (run) ranRun(run, false);
    }
  };

  /** A typed request: the agent (LLM) turns it into one action and a reply; unknown text gets the guide. One that
   * would run or regenerate comes back as a quote first; `confirm` sends it again with the credits shown. */
  const typed = async (text: string, confirm?: number) => {
    if (confirm === undefined) say({ kind: "user", text });
    const r = await attempt(() =>
      ok(api.POST("/v1/catalog/sessions/{session_id}/requests", { ...sid(s.id), body: { text, confirm_credits: confirm } })),
    );
    if (!r) return;
    const u = r.update;
    if (u.quote) return say({ kind: "quote", text: r.reply, label: text, credits: u.quote.credits, worst: u.quote.worst_credits });
    merge({ run: u.run, images: u.images, skus: u.skus });
    if (r.action === "not_understood") say(help(exampleCat(lib, detail.skus), nounNow()));
    else if (r.action === "open_pose_editor" && u.pose_editor) say({ kind: "poses", category: u.pose_editor.category });
    else if (r.action === "reply") say({ kind: "agent", text: r.reply });
    else {
      const icon = r.action === "download" ? "download" : r.action === "feedback" ? "redo" : r.action === "run" ? "run" : "done";
      say({ kind: "update", icon, text: r.reply });
    }
    if (u.download) saveLink(u.download.url, u.download.file_name);
    void loadNext();
  };

  /** A Next step button: a run (sample, batch, all) or the download. */
  const onStep = async (o: S<"NextOption">) => {
    say({ kind: "user", text: o.label });
    if (o.action.type === "download") return download();
    const r = await attempt(() =>
      ok(
        api.POST("/v1/catalog/sessions/{session_id}/runs", {
          ...sid(s.id),
          body: { scope: o.action.scope ?? "all", category: o.action.category, group_key: o.action.group_key },
        }),
      ),
    );
    if (r) ranRun(r);
  };

  /** Renames the project (memory is saved under this name). */
  const rename = (project_name: string) => {
    void commit(msgsRef.current, { project_name }).then(() =>
      setSessions((l) => l.map((x) => (x.id === s.id ? { ...x, project_name } : x))),
    ).catch(() => {});
  };

  const selected = expanded;
  const panel = selected && view === "input" && (
    <SectionPanel
      key={`${s.id}-${s.updated_at.slice(0, 0)}-${selected}-${inputMsgs[0]?.id}`}
      step={selected}
      input={inputs[selected]}
      generated={s.stage === "ready"}
      onAdded={(items, replace) => void onAdded(selected)(items, replace)}
      onRemove={(id) => removeItem(selected, id)}
      onSkip={(v) => skip(selected, v)}
    />
  );

  return (
    <CatalogContext value={{ lib, detail, memory, ops }}>
      <div className="flex h-full gap-2">
        <StepRail
          view={view}
          onView={(v) => {
            setView(v);
            if (v !== "input") void loadNext();
          }}
          detail={detail}
          readyN={readyN}
          selectedInput={expanded}
          onSelectInput={select}
          onReset={() => setConfirmReset(true)}
          open={railOpen}
          onToggle={(o) => setRailOpen(o)}
          sessions={sessions}
          onOpenSession={(id) => void attempt(() => open(id))}
          onNewSession={() => void attempt(newSession)}
        />
        <div className="flex min-w-0 flex-1 flex-col gap-2">
          {libError && (
            <div className="flex items-center gap-3 rounded-xl border border-amber-500/30 bg-amber-500/[0.06] px-4 py-2 text-xs text-amber-200">
              The pose, model and background library isn’t available ({libError}).
              <button type="button" onClick={() => void loadLibrary()} className="ml-auto underline">
                Retry
              </button>
            </div>
          )}
          {view === "input" ? (
            <GuidedChat
              key={s.id}
              msgs={inputMsgs}
              ready={s.stage === "ready"}
              readyN={readyN}
              projectName={s.project_name}
              selected={expanded}
              panel={panel}
              onAction={(id, a) => void onAction(id, a)}
              onRefresh={() => void refreshInput()}
              onReset={() => setConfirmReset(true)}
              onRename={rename}
              inputsDone={(k) => sectionState(inputs[k]) !== "open"}
            />
          ) : (
            <div className="min-h-0 min-w-0 flex-1">
              <Generation
                key={s.id}
                pane={view}
                onPane={setView}
                history={inputMsgs.map((m) => (
                  <ChatMessage key={m.id} m={m} onAction={() => {}} />
                ))}
                log={log}
                next={next}
                chat={{
                  onStep: (o) => void onStep(o),
                  onRunSku: (k) => {
                    say({ kind: "user", text: `Run ${k.code}` });
                    void runSkus([k.id]);
                  },
                  onStop: stop,
                  onSend: (t) => typed(t),
                  onConfirm: (m) => {
                    void commit(msgsRef.current.map((x) => (x.id === m.id ? { ...x, done: true } : x)));
                    void typed(m.label ?? "", m.credits ?? 0);
                  },
                  onSavePoses: (m, p) => void savePoses(m, p),
                  onThought: (id) => void commit(msgsRef.current.map((x) => (x.id === id ? { ...x, done: true } : x))),
                }}
                out={{ approve, regenerate, pickTry, runSkus, stopImages, stopAll: stop, editSku, download }}
              />
            </div>
          )}
        </div>
      </div>
      <Dialog open={confirmReset} onOpenChange={setConfirmReset}>
        <DialogContent>
          <DialogTitle>Delete this session and start over?</DialogTitle>
          <DialogDescription>
            This permanently deletes every input, the chat, the SKUs and all images of this session; it can&apos;t be undone.
            To keep this work, open a New session from Sessions instead. What you used last time stays, so you can
            reuse it.
          </DialogDescription>
          <DialogFooter>
            <Button variant="outline" onClick={() => setConfirmReset(false)}>
              Cancel
            </Button>
            <Button variant="destructive" onClick={() => void reset()}>
              Start Over
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </CatalogContext>
  );
}

/** Run is where a batch gets made; Repository is where everything made so far lives; Sessions lists earlier ones. */
const RAIL_GROUPS = [
  { key: "run", label: "Run", sub: "Set up and generate" },
  { key: "repo", label: "Repository", sub: "Everything in one place" },
  { key: "sessions", label: "Sessions", sub: "Your projects" },
] as const;
type GroupKey = (typeof RAIL_GROUPS)[number]["key"];

/** One step in the rail. */
type RailStep = { key: ViewKey; n: number; group: GroupKey; label: string; sub: string; done: boolean; locked: boolean };

/** The left rail: the steps (Input with its inputs nested), Repository, the sessions list, Start Over and the account.
 * Folds to an icon column; `open` null means open from md up and folded below. */
function StepRail({
  view,
  onView,
  detail,
  readyN,
  selectedInput,
  onSelectInput,
  onReset,
  open,
  onToggle,
  sessions,
  onOpenSession,
  onNewSession,
}: {
  view: ViewKey;
  onView: (v: ViewKey) => void;
  detail: Detail;
  readyN: number;
  selectedInput: Step | null;
  onSelectInput: (s: Step) => void;
  onReset: () => void;
  open: boolean | null;
  onToggle: (open: boolean) => void;
  sessions: S<"SessionSummary">[];
  onOpenSession: (id: string) => void;
  onNewSession: () => void;
}) {
  const user = useUser();
  const dev = useDevViews();
  const groupOf = (v: ViewKey): GroupKey => (v === "skus" || v === "output" ? "repo" : "run");
  const [openGroup, setOpenGroup] = useState<GroupKey | null>(groupOf(view));
  const [lastView, setLastView] = useState(view);
  if (lastView !== view) {
    // Accordion follows the page you're on.
    setLastView(view);
    setOpenGroup(groupOf(view));
  }
  const s = detail.session;
  const ready = s.stage === "ready";
  const hasGeneration = ready || detail.runs.length > 0;
  const skus = detail.skus;
  const toReview = skus.filter((k) => k.status === "To review").length;
  const running = skus.filter((k) => k.status === "Running" || k.status === "Queued").length;
  const steps: RailStep[] = [
    {
      key: "input",
      n: 1,
      group: "run",
      label: "Input",
      sub: s.flow_position === "processing" ? "Processing…" : s.flow_position === "done" ? "Processed" : `${readyN}/${STEPS.length} ready`,
      done: s.flow_position === "done",
      locked: false,
    },
    { key: "processing", group: "run", n: 2, label: "Processing", sub: !hasGeneration ? "After input" : running ? `${running} running` : "Next steps", done: false, locked: !hasGeneration },
    { key: "skus", group: "repo", n: 3, label: "SKU list", sub: hasGeneration ? plural(skus.length, "SKU") : "After input", done: false, locked: !hasGeneration },
    { key: "output", group: "repo", n: 4, label: "Gallery", sub: !hasGeneration ? "After input" : toReview ? `${toReview} to review` : "Review images", done: false, locked: !hasGeneration },
  ];
  const badge = (st: RailStep, active: boolean) => (
    <span
      className={`flex h-6 w-6 shrink-0 items-center justify-center rounded-full text-[11px] font-semibold ${
        st.done ? "bg-emerald-500 text-white" : active ? "bg-accent text-white" : "border border-surface-border text-gray-400"
      }`}
    >
      {st.done ? railIcon(ICON.check, "h-3.5 w-3.5") : st.locked ? railIcon(ICON.lock, "h-3 w-3") : st.n}
    </span>
  );
  const folded = (
    <aside
      className={`${open === null ? "flex md:hidden" : "flex"} w-12 shrink-0 flex-col items-center gap-2 rounded-2xl border border-surface-border bg-surface py-3`}
    >
      <button type="button" onClick={() => onToggle(true)} title="Show steps" aria-label="Show steps" className="rounded-lg p-2 text-gray-400 hover:bg-white/5 hover:text-white">
        {railIcon(ICON.right)}
      </button>
      {RAIL_GROUPS.filter((g) => g.key !== "sessions").map((g, gi) => (
        <Fragment key={g.key}>
          {gi > 0 && <span className="my-1 h-px w-6 bg-surface-border" />}
          {steps
            .filter((st) => st.group === g.key)
            .map((st) => (
              <button key={st.key} type="button" disabled={st.locked} onClick={() => onView(st.key)} title={`${g.label} · ${st.label}`} aria-label={st.label} className="rounded-lg p-1 disabled:opacity-40">
                {badge(st, view === st.key)}
              </button>
            ))}
        </Fragment>
      ))}
    </aside>
  );
  if (open === false) return folded;
  return (
    <>
      {open === null && folded}
      <aside
        className={`${open === null ? "hidden md:flex" : "flex max-md:fixed max-md:inset-y-2 max-md:left-2 max-md:z-40 max-md:shadow-2xl"} w-[15.4rem] shrink-0 flex-col rounded-2xl border border-surface-border bg-surface`}
      >
        <div className="flex items-center justify-between px-3 pt-3 pb-2">
          <img src="/brand/mark.png" alt="" className="h-6 w-6" />
          <span className="flex items-center gap-1.5 text-xs font-semibold tracking-wider text-gray-400 uppercase">
            Catalog Agent <BetaBadge />
          </span>
          <button type="button" onClick={() => onToggle(false)} title="Hide steps" aria-label="Hide steps" className="rounded-lg p-1.5 text-gray-400 hover:bg-white/5 hover:text-white">
            {railIcon(ICON.left)}
          </button>
        </div>
        <nav aria-label="Steps" className="catalog-scroll flex-1 space-y-2 overflow-y-auto px-2 pt-2">
          {RAIL_GROUPS.map((g) => {
            const isOpen = openGroup === g.key;
            return (
              <div key={g.key}>
                <button
                  type="button"
                  onClick={() => setOpenGroup(isOpen ? null : g.key)}
                  aria-expanded={isOpen}
                  className="flex w-full items-center gap-2 rounded-lg px-3 py-1.5 text-left hover:bg-white/[0.03]"
                >
                  <span className="min-w-0 flex-1">
                    <span className="block text-[10.5px] font-semibold tracking-wider text-gray-400 uppercase">{g.label}</span>
                    <span className="block text-[11px] text-gray-500">{g.sub}</span>
                  </span>
                  <span className="text-gray-500">{railIcon(isOpen ? "M18 15l-6-6-6 6" : "M6 9l6 6 6-6", "h-3.5 w-3.5")}</span>
                </button>
                {g.key === "sessions"
                  ? isOpen && (
                      <div className="mt-1 space-y-0.5">
                        <button type="button" onClick={onNewSession} className="flex w-full items-center gap-2 rounded-lg px-3 py-2 text-left text-[13px] text-accent-hover hover:bg-white/5">
                          {railIcon(ICON.plus, "h-3.5 w-3.5")} New session
                        </button>
                        {sessions.map((x) => (
                          <button
                            key={x.id}
                            type="button"
                            onClick={() => onOpenSession(x.id)}
                            aria-current={x.id === s.id ? "page" : undefined}
                            className={`block w-full rounded-lg px-3 py-1.5 text-left transition ${x.id === s.id ? "bg-accent/15 text-white" : "text-gray-300 hover:bg-white/5 hover:text-white"}`}
                          >
                            <span className="block truncate text-[13px]">{x.id === s.id ? s.project_name : x.project_name}</span>
                            <span className="block truncate text-[11px] text-gray-500">
                              {plural(x.sku_count, "SKU")} · {when(x.updated_at)}
                            </span>
                          </button>
                        ))}
                      </div>
                    )
                  : (isOpen || g.key === "run") && (
                      <div className="mt-1 space-y-1">
                        {steps
                          .filter((st) => st.group === g.key)
                          .map((st) => (
                            <Fragment key={st.key}>
                              <button
                                type="button"
                                disabled={st.locked}
                                onClick={() => onView(st.key)}
                                className={`flex w-full items-center gap-3 rounded-xl px-3 py-2.5 text-left transition disabled:cursor-not-allowed disabled:opacity-50 ${
                                  view === st.key ? "bg-accent/15 text-white" : "text-gray-300 hover:bg-white/5 hover:text-white"
                                }`}
                              >
                                {badge(st, view === st.key)}
                                <span className="min-w-0">
                                  <span className="block text-sm font-medium">{st.label}</span>
                                  <span className={`block truncate text-[11px] ${st.done ? "text-emerald-400" : "text-gray-500"}`}>{st.sub}</span>
                                </span>
                              </button>
                              {st.key === "input" && view === "input" && openGroup === "run" && (
                                <div className="ml-6 border-l border-surface-border pb-1 pl-2">
                                  <SectionList
                                    inputs={s.inputs}
                                    selected={selectedInput}
                                    current={s.stage === "setup" || s.flow_position !== "done" ? s.flow_position : null}
                                    onSelect={onSelectInput}
                                  />
                                </div>
                              )}
                            </Fragment>
                          ))}
                      </div>
                    )}
              </div>
            );
          })}
        </nav>
        <div className="space-y-1.5 px-3 pt-2 pb-3">
          {/* the safe way to begin again: the open session stays in Sessions, untouched */}
          <button
            type="button"
            onClick={onNewSession}
            className="flex w-full items-center justify-center gap-1.5 rounded-lg border border-accent/50 bg-accent/10 px-3 py-1.5 text-xs font-medium text-white hover:bg-accent/20"
          >
            <Plus aria-hidden className="size-3.5" />
            New session
          </button>
          <button
            type="button"
            onClick={onReset}
            className="flex w-full items-center justify-center gap-1.5 rounded-lg border border-surface-border px-3 py-1.5 text-xs text-gray-400 hover:bg-white/5 hover:text-gray-200"
          >
            <RotateCcw aria-hidden className="size-3.5" />
            Start Over
          </button>
        </div>
        <div className="border-t border-surface-border p-3">
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <button
                type="button"
                aria-label="Account menu"
                className="flex w-full items-center gap-2 rounded-lg px-2 py-1.5 text-left transition hover:bg-white/5"
              >
                <span aria-hidden className="grid size-7 shrink-0 place-items-center rounded-full bg-accent/20 text-xs font-semibold text-accent-hover">
                  {(user?.name || user?.email || "?").charAt(0).toUpperCase()}
                </span>
                <span className="min-w-0 flex-1">
                  <span className="block truncate text-xs text-gray-200">{user?.name || "Account"}</span>
                  <span className="block truncate text-[11px] text-gray-500">{user?.email}</span>
                </span>
                <ChevronsUpDown aria-hidden className="size-3.5 shrink-0 text-gray-500" />
              </button>
            </DropdownMenuTrigger>
            <DropdownMenuContent side="top" align="start" className="dark w-60">
              <DropdownMenuLabel className="truncate text-xs font-normal text-muted-foreground">{user?.email}</DropdownMenuLabel>
              <DropdownMenuItem asChild>
                <Link href="/dashboard">
                  <LayoutDashboard /> Dashboard
                </Link>
              </DropdownMenuItem>
              <DropdownMenuItem asChild>
                <Link href="/account">
                  <User /> Account
                </Link>
              </DropdownMenuItem>
              {dev && (
                <DropdownMenuItem asChild>
                  <Link href="/dev/jobs">
                    <ListTree /> Jobs (dev)
                  </Link>
                </DropdownMenuItem>
              )}
              {user?.roles.includes("admin") && (
                <DropdownMenuItem asChild>
                  <Link href="/admin/users">
                    <Users /> Users (admin)
                  </Link>
                </DropdownMenuItem>
              )}
              <DropdownMenuSeparator />
              <DropdownMenuItem onSelect={() => signout()}>
                <LogOut /> Sign out
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        </div>
      </aside>
    </>
  );
}
