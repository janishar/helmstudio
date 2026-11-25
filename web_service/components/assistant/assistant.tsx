"use client";
// The Assistant drawer (plan 18 §22.1, §22.6; docs/mock/Assistant.dc.html): over the right edge of every screen of
// the shell, from the dock item or ⌘J (Ctrl J); on a phone a bottom sheet that drops to one line while actions run.
// It shows the chat (your messages, its words, the action lines with Undo, the cards), runs a turn's actions in the
// tab that sent the message (others show it read-only with Continue here) and posts their results. Every text is
// rendered as text: React escapes it, nothing is parsed as HTML.
import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  useSyncExternalStore,
} from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import {
  ArrowRight,
  ArrowUp,
  Brain,
  Check,
  ChevronDown,
  ChevronRight,
  ChevronUp,
  CircleAlert,
  CircleMinus,
  Clock3,
  Copy,
  Cpu,
  Ellipsis,
  Eye,
  EyeOff,
  Globe,
  ImagePlus,
  LoaderCircle,
  Maximize2,
  MessagesSquare,
  Minimize2,
  Minus,
  Monitor,
  Paperclip,
  Pencil,
  Plus,
  Redo2,
  RotateCcw,
  Sparkles,
  Trash2,
  Undo2,
  Zap,
  X,
} from "lucide-react";
import { api, message, ok, type S } from "@/lib/api";
import { safeUrl } from "@/lib/format";
import { useTakes } from "@/lib/take-cache";
import {
  ASSISTANT_CHANGED,
  MEMORY_TABS,
  ago,
  answerBlocks,
  applyEvent,
  asked,
  autoText,
  elsewhere,
  capNoticeId,
  chatTime,
  inlineParts,
  limitOf,
  mentionParts,
  plainAnswer,
  stepSummary,
  tabClientId,
  tabOpen,
  timecode,
  rememberOpen,
  sourceText,
  toResult,
  undoName,
  waitingText,
  type Action,
  type Asked,
  type Entry,
  type Outcome,
  type ScreenCard,
  type Thread,
  type ThreadView,
} from "@/lib/assistant";
import { followEvents } from "@/lib/runs";
import { Registry, awaitIt, navigateTo, undoRuns } from "@/components/assistant/tools";
import { Options, Refs } from "@/components/assistant/answer";
import { atQuery, liveOptions, splitOptions, withPicks } from "@/lib/answer-refs";
import {
  AtPicker,
  AttChip,
  SentAtt,
  TakeMedia,
  type AtKey,
  type Pick,
} from "@/components/assistant/media";
import { uploadFile } from "@/lib/takes";
import { TextArea } from "@/components/expand-text";
import { SkillLine } from "@/components/assistant/skills";
import { skillLines } from "@/lib/skills";

type Line = S<"AssistantLine">;
/** A line this tab ran (or is running): shown until the server's entry of the same id comes. */
type Local = { status: Line["status"]; text: string };
type Asking = { id: string; card: ScreenCard; busy?: boolean };

const page = (id: string) => (since: number) =>
  ok(
    api.GET("/v1/studio/assistant/threads/{thread_id}/events", {
      params: { path: { thread_id: id }, query: { since } },
    }),
  ) as Promise<S<"AssistantEventPage">>;

/** A moment for the screen to render what the last action changed and register its fresh actions and summary. */
const settle = () => new Promise((r) => setTimeout(r, 150));
/** Phone width: the dock is the bar at the top and the drawer a bottom sheet. */
const PHONE = "(max-width: 767px)";
const usePhone = () =>
  useSyncExternalStore(
    (f) => {
      const m = matchMedia?.(PHONE);
      m?.addEventListener("change", f);
      return () => m?.removeEventListener("change", f);
    },
    () => typeof matchMedia === "function" && matchMedia(PHONE).matches,
    () => false,
  );

const words = (name: string) =>
  name.replace(/[A-Z]/g, (c) => ` ${c.toLowerCase()}`).replace(/^./, (c) => c.toUpperCase());

/** The assistant of the signed-in user: status, the chat, the screen registry, and the tab's runner. Nothing is asked
 * until the user is known; the dock item shows only when the admin has it on. */
export function useAssistant(me: S<"User"> | null) {
  const router = useRouter();
  const [reg] = useState(() => new Registry());
  const [cid] = useState(tabClientId);
  const [status, setStatus] = useState<S<"AssistantStatus"> | null>(null);
  const [view, setView] = useState<ThreadView | null>(null);
  const [capRead, setCapRead] = useState<string | undefined>(); // the cap notice the status was read after
  // reopen after a reload of this tab (false on the server; the drawer only renders once the status has loaded)
  const [open, setOpenState] = useState(tabOpen);
  const [shrunk, setShrunk] = useState(false);
  const [local, setLocalState] = useState<Record<string, Local>>({});
  const [undone, setUndone] = useState<Record<string, boolean>>({});
  const [asking, setAsking] = useState<Asking | null>(null);
  const [running, setRunning] = useState<{ n: number; of: number; text: string } | null>(null);
  // Stop pressed while a turn works: "Stopping…" until the chat is idle (board frame 15; item 27)
  const [stopFor, setStopFor] = useState<string | null>(null); // the thread it stops
  const [error, setError] = useState("");
  const undoFns = useRef<Record<string, NonNullable<Outcome["undo"]>>>({});
  const answerRef = useRef<((a: "yes" | "no" | "gone") => void) | null>(null);
  const handled = useRef(new Set<string>());
  const stop = useRef(false);
  // Stop pressed while the turn works: its next park, if it parks before ending, runs nothing
  const stopNext = useRef(false);
  const threadId = view?.thread.id;

  useEffect(() => {
    reg.configure(me?.roles ?? [], {
      navigate: (a) =>
        navigateTo(reg, a as Parameters<typeof navigateTo>[1], (url) => router.push(url)),
      await: (a, { progress }) => awaitIt(a as { what: string; id: string }, progress),
    });
  }, [reg, me, router]);

  const setOpen = useCallback((o: boolean | ((x: boolean) => boolean)) => {
    setOpenState((x) => {
      const next = typeof o === "function" ? o(x) : o;
      rememberOpen(next);
      return next;
    });
    setShrunk(false);
  }, []);
  const setLocal = (id: string, l: Local) => setLocalState((m) => ({ ...m, [id]: l }));
  // size (alpha.2 item 4, board frame 9): full screen, and minimized to the small bar; Close ends both, ⌘J and Esc
  // go back to the bar when it was minimized
  const [full, setFull] = useState(false);
  const [min, setMin] = useState(false);
  const close = useCallback(() => {
    setOpen(false);
    setMin(false);
  }, [setOpen]);
  const minimize = useCallback(() => {
    setOpen(false);
    setMin(true);
  }, [setOpen]);

  // the status and the newest chat, once the user is known, and again when Admin › AI agents saves (no reload)
  const [rev, setRev] = useState(0);
  useEffect(() => {
    const bump = () => setRev((n) => n + 1);
    window.addEventListener(ASSISTANT_CHANGED, bump);
    return () => window.removeEventListener(ASSISTANT_CHANGED, bump);
  }, []);
  useEffect(() => {
    if (!me) return;
    let stop = false;
    ok(api.GET("/v1/studio/assistant/threads"))
      .then(async (l) => {
        if (stop) return;
        setStatus(l.status);
        const t = l.status.enabled ? l.threads[0] : undefined;
        if (!t) return;
        const d = await ok(
          api.GET("/v1/studio/assistant/threads/{thread_id}", {
            params: { path: { thread_id: t.id } },
          }),
        );
        if (stop) return;
        setView({ thread: d, entries: d.entries });
        setCapRead(capNoticeId(d.entries));
      })
      .catch(() => undefined); // no assistant: no dock item
    return () => {
      stop = true;
    };
  }, [me, rev]);

  // a cap notice is checked against the caps read after it (the admin may have raised or lowered them since the
  // status was read): until that read, the notice alone decides
  const capId = capNoticeId(view?.entries);
  useEffect(() => {
    if (!capId || capId === capRead) return;
    let stop = false;
    ok(api.GET("/v1/studio/assistant/threads"))
      .then((l) => {
        if (stop) return;
        setStatus(l.status);
        setCapRead(capId);
      })
      .catch(() => undefined);
    return () => {
      stop = true;
    };
  }, [capId, capRead]);

  // the chat's events, live
  useEffect(() => {
    if (!threadId) return;
    return followEvents<S<"AssistantEvent">>(
      `/api/v1/studio/assistant/threads/${encodeURIComponent(threadId)}/events`,
      ["entry", "thread"],
      (e) => setView((v) => (v && v.thread.id === threadId ? applyEvent(v, e) : v)),
      page(threadId),
    );
  }, [threadId]);

  const post = useCallback(async (id: string, body: S<"AssistantPost">) => {
    try {
      const r = await ok(
        api.POST("/v1/studio/assistant/threads/{thread_id}/messages", {
          params: { path: { thread_id: id } },
          body,
        }),
      );
      setError("");
      setView((v) =>
        v && v.thread.id === id
          ? {
              // a steer or Stop leaves the running turn as the events have it (the reply's snapshot may be older)
              thread: r.steer || body.kind === "stop" ? v.thread : r.thread,
              entries:
                r.entry && !v.entries.some((x) => x.id === r.entry!.id)
                  ? [...v.entries, r.entry]
                  : v.entries,
            }
          : v,
      );
      return true;
    } catch (e) {
      setError(message(e));
      return false;
    }
  }, []);

  /** A screen card: the drawer opens on it; Yes calls the screen's own function, Not now cancels, and leaving the
   * screen cancels it too. */
  const ask = useCallback(
    (id: string, a: Asked, key: number | null, asks?: string) =>
      new Promise<Outcome>((resolve) => {
        setOpenState(true);
        setShrunk(false);
        setAsking({ id, card: a.card });
        // Auto on but this one still asks (API 1.68): its line says why, above the card
        setLocal(id, { status: "waiting", text: asks ?? waitingText(a.card) });
        const off = reg.subscribe(() => key !== null && !reg.alive(key) && answer("gone"));
        function answer(how: "yes" | "no" | "gone") {
          off();
          answerRef.current = null;
          if (how !== "yes") {
            setAsking(null);
            return resolve({
              status: "cancelled",
              text:
                how === "gone"
                  ? "Cancelled: you left the screen"
                  : `Not now · ${a.card.go[0]}${a.card.go.slice(1).toLowerCase()}`,
            });
          }
          setAsking({ id, card: a.card, busy: true });
          a.yes()
            .catch((e: unknown) => ({ status: "failed" as const, text: message(e) }))
            .then((o) => {
              setAsking(null);
              resolve(o);
            });
        }
        answerRef.current = answer;
      }),
    [reg],
  );

  /** The parked turn's actions, in order, in this tab; then their results with the screen now (after a navigate,
   * whose later actions are dropped, the new screen). */
  const runBatch = useCallback(
    async (t: Thread, p: S<"AssistantPending">) => {
      stop.current = stopNext.current;
      stopNext.current = false;
      const results = [];
      let moved = false;
      if (typeof matchMedia === "function" && matchMedia(PHONE).matches) setShrunk(true);
      for (const [i, a] of p.actions.entries()) {
        if (moved || stop.current) {
          const text = moved
            ? "Did not run · went to another screen"
            : "Did not run · you stopped it";
          setLocal(a.id, { status: "did_not_run", text });
          results.push(toResult(a.id, { status: "did_not_run", text }));
          continue;
        }
        // each action acts on the screen as the ones before it left it (Create's actions read the last render)
        if (i > 0) await settle();
        const first = `${words(a.name)}…`;
        setRunning({ n: i + 1, of: p.actions.length, text: first });
        setLocal(a.id, { status: "running", text: first });
        const progress = (text: string) => {
          setLocal(a.id, { status: "running", text });
          setRunning({ n: i + 1, of: p.actions.length, text });
        };
        const { out, key } = await reg.run(a as Action, progress);
        // Auto (API 1.68, board frame 13): pipelines let it run by itself: its Yes without the card, "Auto · " first,
        // and a generation's Undo stops its runs and deletes their takes
        let o = !asked(out)
          ? out
          : a.auto
            ? await out
                .yes(true)
                .catch((e: unknown): Outcome => ({ status: "failed", text: message(e) }))
            : await ask(a.id, out, key, a.asks);
        if (a.auto && o.status === "done") {
          const runs = (o.result as { run_ids?: unknown } | undefined)?.run_ids;
          o = {
            ...o,
            text: autoText(o.text),
            undo:
              o.undo ??
              (Array.isArray(runs) && runs.length
                ? { undo: () => undoRuns(runs as string[]) }
                : undefined),
          };
        }
        if (o.undo) undoFns.current[a.id] = o.undo;
        setLocal(a.id, {
          status: o.status === "still_running" ? "running" : (o.status as Line["status"]),
          text: o.text,
        });
        results.push(toResult(a.id, o));
        if (a.name === "navigate" && o.status === "done") moved = true;
      }
      setRunning(null);
      // the screen as the actions left it, after their renders (a switchTo's new form): without it the model reads
      // the summary sent with the message, sees the old workflow and switches again (the switch loop)
      await settle();
      await post(t.id, {
        kind: "results",
        client_id: cid,
        turn: p.turn,
        results,
        screen: reg.summary(),
      });
    },
    [ask, cid, post, reg],
  );

  // a parked turn whose actions this tab runs
  const thread = view?.thread;
  useEffect(() => {
    const p = thread?.pending;
    if (!thread || thread.state !== "waiting" || thread.client_id !== cid || !p?.actions.length)
      return;
    const k = `${thread.id}:${p.turn}:${p.actions.map((a) => a.id).join()}`;
    if (handled.current.has(k)) return;
    handled.current.add(k);
    void runBatch(thread, p);
  }, [thread, cid, runBatch]);
  // the stopped turn ended without parking: a later turn's batch runs as usual
  useEffect(() => {
    if (thread?.state === "idle") stopNext.current = false;
  }, [thread?.state]);
  // it ends once the chat is idle, or another chat opens
  if (
    stopFor &&
    (stopFor !== thread?.id || (thread.state !== "working" && thread.state !== "waiting"))
  )
    setStopFor(null);

  const send = useCallback(
    async (text: string, takes: string[] = []) => {
      const t = text.trim();
      if (!t) return false;
      let id = view?.thread.id;
      if (!id) {
        try {
          const n = await ok(
            api.POST("/v1/studio/assistant/threads", { body: { client_id: cid } }),
          );
          setView({ thread: n, entries: [] });
          id = n.id;
        } catch (e) {
          setError(message(e));
          return false;
        }
      }
      return post(id, {
        kind: "message",
        client_id: cid,
        text: t,
        screen: reg.summary(),
        ...(takes.length > 0 && { takes }),
      });
    },
    [cid, post, reg, view?.thread.id],
  );

  /** Leaves the chat on screen: a card it waits on is cancelled ("you left the screen"), the rest of its batch stops. */
  const leave = useCallback(() => {
    answerRef.current?.("gone");
    stop.current = true;
    stopNext.current = false;
    setLocalState({});
    setError("");
  }, []);

  const newChat = useCallback(async () => {
    try {
      const n = await ok(api.POST("/v1/studio/assistant/threads", { body: { client_id: cid } }));
      leave();
      setView({ thread: n, entries: [] });
    } catch (e) {
      setError(message(e));
    }
  }, [cid, leave]);

  /** Auto mode's switch (API 1.68, board frame 13): per chat, starts off; a chat is made first when there is none. */
  const setAuto = useCallback(
    async (on: boolean) => {
      try {
        const id =
          threadId ??
          (await ok(api.POST("/v1/studio/assistant/threads", { body: { client_id: cid } }))).id;
        const t = await ok(
          api.PATCH("/v1/studio/assistant/threads/{thread_id}", {
            params: { path: { thread_id: id } },
            body: { auto: on },
          }),
        );
        setView((v) =>
          v && v.thread.id === t.id ? { ...v, thread: t } : { thread: t, entries: [] },
        );
      } catch (e) {
        setError(message(e));
      }
    },
    [cid, threadId],
  );

  // the Chats list (board frame 7): the chats newest first, read when the list opens
  const [chats, setChats] = useState<Thread[] | null>(null);
  const loadChats = useCallback(() => {
    ok(api.GET("/v1/studio/assistant/threads"))
      .then((l) => setChats(l.threads))
      .catch((e) => setError(message(e)));
  }, []);
  /** An older chat picked: its whole thread comes back; writing in it works as in the newest one. */
  const openChat = useCallback(
    async (id: string) => {
      try {
        const d = await ok(
          api.GET("/v1/studio/assistant/threads/{thread_id}", {
            params: { path: { thread_id: id } },
          }),
        );
        leave();
        setView({ thread: d, entries: d.entries });
        setCapRead(capNoticeId(d.entries));
      } catch (e) {
        setError(message(e));
      }
    },
    [leave],
  );

  const answerCard = (cardId: string, yes: boolean) =>
    threadId &&
    void post(threadId, {
      kind: "answer",
      client_id: cid,
      card_id: cardId,
      answer: yes ? "yes" : "not_now",
    });
  const continueHere = () => threadId && void post(threadId, { kind: "continue", client_id: cid });
  // the running turn takes a message as a steer (board frame 8): while it works, or runs this tab's actions
  const t = view?.thread;
  const steering =
    !!t &&
    !elsewhere(t, cid) &&
    (t.state === "working" || (t.state === "waiting" && !!t.pending?.actions.length));
  /** Stop: this tab's actions stop, a screen card is cancelled, and the turn ends at its next step. */
  const stopAll = () => {
    stop.current = true;
    answerRef.current?.("no");
    if (threadId && steering) {
      setStopFor(threadId);
      stopNext.current = true;
      void post(threadId, { kind: "stop", client_id: cid });
    }
  };
  const toggleUndo = async (l: Line) => {
    const was = !!undone[l.id];
    try {
      if (l.memory_id && !was)
        await ok(
          api.DELETE("/v1/studio/assistant/memory/{item_id}", {
            params: { path: { item_id: l.memory_id } },
          }),
        );
      else {
        const f = undoFns.current[l.id];
        if (!f || (was && !f.redo)) return;
        await (was ? f.redo?.() : f.undo());
      }
      setUndone((u) => ({ ...u, [l.id]: !was }));
    } catch (e) {
      setError(message(e));
    }
  };

  const enabled = !!status?.enabled;
  // ⌘J (Ctrl J) opens and closes it on any screen; ⇧⌘J toggles full screen (opening it full when closed)
  useEffect(() => {
    if (!enabled) return;
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && !e.altKey && e.key.toLowerCase() === "j") {
        e.preventDefault();
        if (e.shiftKey) {
          setFull(!(open && full));
          setOpen(true);
        } else setOpen((o) => !o);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [enabled, setOpen, open, full]);

  // a new answer is one that came while the drawer was not open (the minimized bar's green dot)
  const lastAnswer = view?.entries.findLast((e) => e.kind === "assistant");
  const lastId = lastAnswer?.id ?? null;
  const [seen, setSeen] = useState<string | null | undefined>(undefined);
  if (view && (seen === undefined || (open && seen !== lastId))) setSeen(lastId);

  /** Retry on a broken answer (item 24): your last message again, the one that started the turn (a steer read
   * while it worked is not it); steers sent as the next message are that message together. */
  const retry = useCallback(() => {
    const mine = (view?.entries ?? []).filter(
      (e) => e.kind === "user" && (!e.steer || e.steer === "next"),
    );
    let i = mine.length - 1;
    if (mine[i]?.steer === "next") while (i > 0 && mine[i - 1].steer === "next") i--;
    const text = mine
      .slice(Math.max(0, i))
      .map((e) => e.text ?? "")
      .join("\n\n");
    if (text.trim()) void send(text);
  }, [view, send]);

  const readOnly = elsewhere(view?.thread, cid);
  const serverCardWaits =
    !readOnly && !!view?.entries.some((e) => e.kind === "card" && e.card?.state === "waiting");
  return {
    reg,
    cid,
    status,
    capLimits: capId === capRead ? status?.limits : undefined,
    enabled,
    view,
    open,
    setOpen,
    close,
    full,
    setFull,
    min,
    minimize,
    newAnswer: !open && !!lastId && lastId !== seen ? (lastAnswer?.text ?? "") : null,
    retry,
    shrunk,
    setShrunk,
    local,
    undone,
    // an Auto step's Undo deletes what it made: no Redo after it
    canUndo: (l: Line) =>
      !!l.memory_id || (!!undoFns.current[l.id] && (!undone[l.id] || !!undoFns.current[l.id].redo)),
    asking,
    answerAsk: (yes: boolean) => answerRef.current?.(yes ? "yes" : "no"),
    running,
    error,
    setError,
    send,
    newChat,
    setAuto,
    chats,
    loadChats,
    openChat,
    answerCard,
    continueHere,
    stopAll,
    stopping: !!stopFor,
    steering,
    toggleUndo,
    readOnly,
    cardWaits: !!asking || serverCardWaits,
  };
}
export type AssistantApi = ReturnType<typeof useAssistant>;

/** "Thinking… N s" (plan 29, 33.7), "Working… N s" for a model that does not think (owner, 2026-11-25): a quiet reply
 * bubble under the last message, whole seconds since this tab saw the model call start; the reply replaces it. */
function Thinking({ since, thinks }: { since: number; thinks: boolean }) {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(id);
  }, []);
  return (
    <div className="msg think" role="status" aria-live="off">
      <LoaderCircle size={12} strokeWidth={3} aria-hidden className="run" />
      {thinks ? "Thinking…" : "Working…"} {Math.max(0, Math.floor((now - since) / 1000))} s
    </div>
  );
}

const TONE: Record<Line["status"], string> = {
  running: "run",
  waiting: "wait",
  done: "ok",
  undone: "skip",
  did_not_run: "skip",
  failed: "bad",
  cancelled: "skip",
};
function LineIcon({ status, text }: { status: Line["status"]; text: string }) {
  const p = { size: 14, strokeWidth: 2.5, "aria-hidden": true, className: TONE[status] } as const;
  if (status === "running")
    return /^Look/.test(text) ? (
      <Eye {...p} strokeWidth={2} />
    ) : /^Waiting for you/.test(text) ? (
      <Clock3 {...p} strokeWidth={2} className={TONE.waiting} />
    ) : (
      <LoaderCircle {...p} />
    );
  if (status === "waiting") return <Clock3 {...p} strokeWidth={2} />;
  if (status === "failed") return <X {...p} />;
  if (status === "did_not_run" || status === "cancelled")
    return <CircleMinus {...p} strokeWidth={2} />;
  if (/^(Opened|Went to)/.test(text)) return <ArrowRight {...p} strokeWidth={2} />;
  if (/^Remembered/.test(text)) return <Brain {...p} strokeWidth={2} />;
  if (/^Auto · /.test(text)) return <Zap {...p} strokeWidth={2} />; // ran by itself (board frame 13)
  return <Check {...p} />;
}

// Models answer in light markdown despite the prompt: show **bold** as bold and drop `code` ticks.
function Text({ text }: { text: string }) {
  return (
    <>
      {text
        .split(/(\*\*[^*\n]+\*\*)/)
        .map((s, j) =>
          j % 2 ? (
            <b key={j}>{s.slice(2, -2)}</b>
          ) : (
            <Mentions key={j} text={s.replace(/`([^`\n]+)`/g, "$1")} />
          ),
        )}
    </>
  );
}

function Mentions({ text }: { text: string }) {
  return (
    <>
      {mentionParts(text).map((p, i) =>
        p.at ? (
          <span key={i} className="mn">
            {p.t}
          </span>
        ) : (
          p.t
        ),
      )}
    </>
  );
}

/** Moves the Timeline's playhead; `max` is the open sequence's length (s). */
type Seek = ((s: number) => void) & { max: number };

/** Renders a run of plain answer text: refs as chips (items 17, 19); never given code. */
type RefsHook = (t: string, key: number) => React.ReactNode;

/** An answer's line (item 22): bold, italic, inline code, @handles; a timecode is a chip that jumps the Timeline's
 * playhead when `seek` is given (on the Timeline) and the time is within the sequence, else text. Plain, bold and
 * italic text goes through `refs` when given. */
function Inlines({ text, seek, refs }: { text: string; seek?: Seek; refs?: RefsHook }) {
  const t = (x: string, k: number) => (refs ? refs(x, k) : x);
  return (
    <>
      {inlineParts(text).map((p, i) =>
        p.k === "b" ? (
          <b key={i}>{t(p.t, 0)}</b>
        ) : p.k === "i" ? (
          <i key={i}>{t(p.t, 0)}</i>
        ) : p.k === "code" ? (
          <code key={i}>{p.t}</code>
        ) : p.k === "at" ? (
          <span key={i} className="mn">
            {p.t}
          </span>
        ) : p.k === "tc" && seek && timecode(p.t) <= seek.max ? (
          <button
            key={i}
            type="button"
            className="tc"
            aria-label={`Jump the Timeline to ${p.t}`}
            title={`Jump the Timeline to ${p.t}`}
            onClick={() => seek(timecode(p.t))}
          >
            {p.t}
          </button>
        ) : p.k ? (
          p.t
        ) : (
          t(p.t, i)
        ),
      )}
    </>
  );
}

function CopyButton({
  text,
  label,
  children,
}: {
  text: string | (() => string);
  label?: string;
  children: string;
}) {
  const [did, setDid] = useState(false);
  return (
    <button
      type="button"
      className="un"
      aria-label={label}
      onClick={() =>
        void navigator.clipboard?.writeText(typeof text === "function" ? text() : text).then(
          () => {
            setDid(true);
            setTimeout(() => setDid(false), 1500);
          },
          () => undefined,
        )
      }
    >
      <Copy size={12} aria-hidden />
      {did ? "Copied" : children}
    </button>
  );
}

/** Text clamped to 3 lines with a chevron to show all when it is longer (items 5, 24; board frame 17): measured, so
 * short text has none. A reply clamps once, as a whole bubble, never per paragraph (owner, 2026-11-25). */
function Clamp({
  children,
  k,
  whole = false,
}: {
  children: React.ReactNode;
  k: string;
  /** shown in full (a plan or a code block: board frames 10a, 10f) */
  whole?: boolean;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const [over, setOver] = useState(false);
  const [open, setMore] = useState(false);
  const more = open || whole;
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el || more) return;
    const measure = () => setOver(el.scrollHeight > el.clientHeight + 1);
    measure();
    // measured again when its width changes (the drawer opening, full screen)
    const ro = typeof ResizeObserver === "function" ? new ResizeObserver(measure) : null;
    ro?.observe(el);
    return () => ro?.disconnect();
  }, [k, more]);
  return (
    <>
      <div ref={ref} className={more ? "unclamp" : over ? "clamp over" : "clamp"}>
        {children}
      </div>
      {!whole && (over || more) && (
        <button
          type="button"
          className="mmore"
          aria-expanded={more}
          aria-label={more ? "Show less" : "Show all"}
          title={more ? "Show less" : "Show all"}
          onClick={() => setMore(!more)}
        >
          {more ? (
            <ChevronUp size={14} strokeWidth={2.25} aria-hidden />
          ) : (
            <ChevronDown size={14} strokeWidth={2.25} aria-hidden />
          )}
        </button>
      )}
    </>
  );
}

/** One answer (item 22, board frame 10a): its blocks as plain elements, never HTML; no headings or tables. */
function Answer({ text, seek, refs }: { text: string; seek?: Seek; refs?: RefsHook }) {
  const blocks = answerBlocks(text);
  return (
    <>
      {blocks.map((b, i) =>
        b.k === "p" ? (
          <p key={i}>
            <Inlines text={b.text} seek={seek} refs={refs} />
          </p>
        ) : b.k === "take" ? (
          <TakeMedia key={i} id={b.id} />
        ) : b.k === "code" ? (
          <div key={i} className="cb">
            <div>
              {b.lang || "code"}
              <CopyButton text={b.text} label={`Copy the ${b.lang || "code"}`}>
                Copy
              </CopyButton>
            </div>
            <pre>{b.text}</pre>
          </div>
        ) : b.k === "plan" ? (
          <ul key={i} className="plan" aria-label="Plan">
            {b.items.map((x, j) => (
              <li key={j} className={x.done ? undefined : "todo"}>
                {x.done ? (
                  <Check size={14} strokeWidth={2.5} aria-hidden className="ok" />
                ) : (
                  <Clock3 size={14} strokeWidth={2} aria-hidden />
                )}
                <span>
                  <span className="sr-only">{x.done ? "Done: " : "To do: "}</span>
                  <Inlines text={x.text} seek={seek} refs={refs} />
                </span>
              </li>
            ))}
          </ul>
        ) : b.k === "ol" ? (
          <ol key={i}>
            {b.items.map((x, j) => (
              <li key={j}>
                <Inlines text={x} seek={seek} refs={refs} />
              </li>
            ))}
          </ol>
        ) : (
          <ul key={i} className="md">
            {b.items.map((x, j) => (
              <li key={j}>
                <Inlines text={x} seek={seek} refs={refs} />
              </li>
            ))}
          </ul>
        ),
      )}
    </>
  );
}

function LineRow({ l, a }: { l: Line; a: AssistantApi }) {
  const [wide, setWide] = useState(false);
  const off = !!a.undone[l.id];
  const status = off ? "undone" : l.status;
  const text = / in Models\.$/.test(l.text) ? (
    <>
      <Text text={l.text.slice(0, -"Models.".length)} />
      <Link href="/models" className="underline">
        Models
      </Link>
      .
    </>
  ) : (
    <Text text={l.text} />
  );
  // one line with "…", tap opens it in full, hover shows it (item 21, board frame 10c); a memory line wraps in full
  const long = l.text.length > 36 && !/^Remembered\b/.test(l.text);
  const verb = off ? "Redo" : "Undo";
  return (
    <li>
      <LineIcon status={status} text={l.text} />
      <span
        className={
          off
            ? "text-t2 line-through"
            : status === "did_not_run" || status === "cancelled"
              ? "text-t2"
              : ""
        }
      >
        {long ? (
          <span
            className={wide ? "wide" : "one"}
            role="button"
            tabIndex={0}
            aria-expanded={wide}
            title={l.text}
            onClick={() => setWide(!wide)}
            onKeyDown={(e) => {
              if (e.key === "Enter" || e.key === " ") {
                e.preventDefault();
                setWide(!wide);
              }
            }}
          >
            {text}
          </span>
        ) : (
          text
        )}
      </span>
      {l.undo && (l.status === "done" || off) && a.canUndo(l) && !(off && l.memory_id) && (
        <button
          type="button"
          className="un"
          aria-label={`${verb}: ${undoName(l.text)}`}
          title={`${verb}: ${undoName(l.text)}`}
          onClick={() => void a.toggleUndo(l)}
        >
          {off ? <Redo2 size={12} aria-hidden /> : <Undo2 size={12} aria-hidden />}
          {verb}
        </button>
      )}
    </li>
  );
}

function Lines({
  lines,
  a,
  more = 0,
  onMore,
}: {
  lines: Line[];
  a: AssistantApi;
  more?: number;
  onMore?: () => void;
}) {
  return (
    <ul className="al" aria-label="What it did">
      {lines.map((l) => (
        <LineRow key={l.id} l={l} a={a} />
      ))}
      {more > 0 && (
        <li>
          <button type="button" className="un" onClick={onMore}>
            + {more} more step{more === 1 ? "" : "s"}
          </button>
        </li>
      )}
    </ul>
  );
}

const FOLD_SHOWN = 4; // a folded turn opened shows its last steps, then "+ N more steps"

/** An earlier turn's steps folded into one line (item 25, board frame 10h–i); tap opens them again. */
function Folded({ lines, a }: { lines: Line[]; a: AssistantApi }) {
  const [open, setOpen] = useState(false);
  const [all, setAll] = useState(false);
  const more = all ? 0 : Math.max(0, lines.length - FOLD_SHOWN);
  return (
    <>
      <button type="button" className="sum" aria-expanded={open} onClick={() => setOpen(!open)}>
        <Check size={14} strokeWidth={2.5} aria-hidden className="ok" />
        <span>{stepSummary(lines)}</span>
        {open ? (
          <ChevronDown size={14} strokeWidth={2} aria-hidden />
        ) : (
          <ChevronRight size={14} strokeWidth={2} aria-hidden />
        )}
      </button>
      {open && (
        <Lines
          lines={more ? lines.slice(-FOLD_SHOWN) : lines}
          a={a}
          more={more}
          onMore={() => setAll(true)}
        />
      )}
    </>
  );
}

/** The takes a look-at card is about (item 23), from the tab's take cache. */
function TakeThumbs({ ids }: { ids: string[] }) {
  const take = useTakes(ids);
  return (
    <div className="thumbs" aria-label="The takes it is about">
      {ids.map((id) => {
        const t = take(id);
        const src = safeUrl(t?.thumb_url);
        return src ? (
          <img loading="lazy" decoding="async" key={id} src={src} alt={t?.title ?? ""} />
        ) : (
          <span key={id} aria-hidden />
        );
      })}
    </div>
  );
}

const GO: Record<S<"AssistantCard">["kind"], string> = {
  look: "LOOK",
  web_search: "SEARCH",
  delete: "DELETE",
  plugin: "PLUGIN",
};
const CARD_STATE: Record<S<"AssistantCard">["state"], string> = {
  waiting: "",
  yes: "You said yes.",
  not_now: "Not now.",
  cancelled: "Cancelled.",
};

/** One card: the screen's (Yes runs its own button) or the server's (Yes is an answer). */
function Card({
  card,
  waiting,
  busy,
  onYes,
  onNo,
  after,
  takeIds,
}: {
  card: ScreenCard;
  waiting: boolean;
  busy?: boolean;
  onYes: () => void;
  onNo: () => void;
  after?: string;
  takeIds?: string[];
}) {
  return (
    <section className={`cf ${card.danger ? "danger" : ""}`} aria-label={`Confirm: ${card.title}`}>
      <h3>
        {card.kind === "generate" && <Cpu size={16} aria-hidden />}
        <b>{card.title}</b>
        {card.tags.map((t) => (
          <span key={t} className="gpu">
            {t}
          </span>
        ))}
      </h3>
      {card.rows.length > 0 && (
        <dl>
          {card.rows.map(([k, v]) => (
            <div key={k} className="contents">
              <dt>{k}</dt>
              <dd>{v}</dd>
            </div>
          ))}
        </dl>
      )}
      {card.thumbs && card.thumbs.length > 0 && (
        <div className="thumbs" aria-label="The takes it is about">
          {card.thumbs.map((u, i) => (
            <img loading="lazy" decoding="async" key={i} src={u} alt="" />
          ))}
        </div>
      )}
      {!!takeIds?.length && <TakeThumbs ids={takeIds} />}
      {card.est && (
        <div className="est">
          <Clock3 size={13} strokeWidth={1.75} aria-hidden />
          {card.est}
        </div>
      )}
      {card.quote && (
        <p className="qt">
          <Text text={card.quote} />
        </p>
      )}
      {(card.note || card.off) && (
        <div className="rem items-start">
          <Globe size={13} strokeWidth={1.75} aria-hidden className="mt-px shrink-0" />
          <span>{card.off ?? card.note}</span>
        </div>
      )}
      {waiting ? (
        <div className="cfb">
          <button type="button" className="go" disabled={busy || !!card.off} onClick={onYes}>
            {card.go}
          </button>
          <button type="button" className="no" disabled={busy} onClick={onNo}>
            Not now
          </button>
        </div>
      ) : (
        after && <p className="fine">{after}</p>
      )}
      <p className="fine">{card.fine}</p>
    </section>
  );
}

const serverCard = (c: S<"AssistantCard">): ScreenCard => ({
  kind: c.kind,
  title: c.title,
  rows: c.rows.map((r) => [r.label, r.value]),
  tags: c.tags,
  note: c.note,
  go: GO[c.kind],
  fine:
    c.kind === "web_search"
      ? "Off unless your admin turns it on."
      : c.kind === "delete"
        ? "This can't be undone."
        : c.kind === "plugin"
          ? "Runs a plugin's tool."
          : "Runs on your machines.",
  danger: c.kind === "delete",
});

/** The model answered wrongly twice (board frame 7): the plain line, and the checker's own words behind Details,
 * closed by default and after a reload. */
function Broken({ raw, retry }: { raw: string; retry?: () => void }) {
  const [open, setOpen] = useState(false);
  const [retried, setRetried] = useState(false);
  const rawRef = useRef<HTMLParagraphElement>(null);
  // opened on the last message, its words come into view, not under the composer
  useEffect(() => {
    if (open) rawRef.current?.scrollIntoView({ block: "nearest" });
  }, [open]);
  return (
    <div role="note" className="msg note">
      <p>The model gave a broken answer twice.</p>
      <div className="ma">
        {/* Retry (item 24, board frame 10d): your last message again; nothing ran the first time */}
        {retry && !retried && (
          <button
            type="button"
            className="un det"
            onClick={() => {
              setRetried(true); // once: a second click would send it again
              retry();
            }}
          >
            <RotateCcw size={12} aria-hidden />
            Retry
          </button>
        )}
        <button
          type="button"
          className="un det"
          aria-expanded={open}
          onClick={() => setOpen(!open)}
        >
          Details
          {open ? <ChevronUp size={12} aria-hidden /> : <ChevronDown size={12} aria-hidden />}
        </button>
      </div>
      {open && (
        <p className="raw mono" ref={rawRef}>
          {raw}
        </p>
      )}
    </div>
  );
}

const SHOWN = 5; // chats before Show older

/** The Chats list (board frame 7): New chat first, then the chats newest first (title from the first message, time
 * of the last), this one Open, one waiting on you Waiting. Esc or a click outside closes it; Delete stays in Memory. */
function ChatsList({
  a,
  btn,
  close,
  pick,
}: {
  a: AssistantApi;
  btn: React.RefObject<HTMLButtonElement | null>;
  close: () => void;
  pick: (id: string | null) => void;
}) {
  const [older, setOlder] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    ref.current?.querySelector("button")?.focus();
    const out = (e: PointerEvent) => {
      const t = e.target as Node;
      if (!ref.current?.contains(t) && !btn.current?.contains(t)) close();
    };
    document.addEventListener("pointerdown", out);
    return () => document.removeEventListener("pointerdown", out);
  }, [btn, close]);
  const list = a.chats ?? [];
  const cur = a.view?.thread.id;
  return (
    <div
      ref={ref}
      role="menu"
      aria-label="Chats"
      className="chats"
      onKeyDown={(e) => {
        const all = [...(ref.current?.querySelectorAll<HTMLElement>("[role^=menuitem]") ?? [])];
        const at = all.indexOf(document.activeElement as HTMLElement);
        const to = { ArrowDown: at + 1, ArrowUp: at - 1, Home: 0, End: all.length - 1 }[e.key];
        if (e.key === "Escape") {
          e.preventDefault();
          close();
          btn.current?.focus();
        } else if (to !== undefined) {
          e.preventDefault();
          all[(to + all.length) % all.length]?.focus();
        }
      }}
    >
      <div className="chh">
        <span>Chats</span>
        <span className="mono">newest first</span>
      </div>
      <button type="button" role="menuitem" className="new" onClick={() => pick(null)}>
        <Plus size={14} strokeWidth={2} aria-hidden />
        New chat
      </button>
      {(older ? list : list.slice(0, SHOWN)).map((t) => (
        <button
          key={t.id}
          type="button"
          role="menuitemradio"
          aria-checked={t.id === cur}
          onClick={() => pick(t.id)}
        >
          <span className="cht">
            <span>{t.title}</span>
            <small>{chatTime(t.updated_at)}</small>
          </span>
          {t.id === cur ? (
            <span className="cho">
              <Check size={14} strokeWidth={2.5} aria-hidden />
              Open
            </span>
          ) : (
            t.state === "waiting" && <span className="chw">Waiting</span>
          )}
        </button>
      ))}
      {!older && list.length > SHOWN && (
        <button type="button" role="menuitem" className="un" onClick={() => setOlder(true)}>
          Show older
        </button>
      )}
    </div>
  );
}

/** The chat as the board lays it out: your messages, its words with their lines under them, cards, notices. */
function Thread({ a, entries }: { a: AssistantApi; entries: Entry[] }) {
  const known = new Set(entries.map((e) => e.id));
  const out: React.ReactNode[] = [];
  let words: Entry[] = [];
  let lines: Line[] = [];
  let fold: number | null = null;
  const steers: React.ReactNode[] = []; // sent while it works: at the bottom, dimmed, until the turn reads them
  // a workflow chip switches to it where the screen can (Create)
  const switchTo = a.reg.current()?.spec.actions.switchTo;
  const pickWorkflow = switchTo
    ? async (id: string) => {
        // not installed or not downloaded: say so (the drawer's error line), never a silent click
        const r = await switchTo({ workflow: id }, { progress: () => undefined });
        a.setError(!asked(r) && r.status === "failed" ? r.text : "");
      }
    : undefined;
  // refs in an answer's text as chips (item 19), inline wherever the text is formatted (not in code)
  const refs = (t: string, k: number) => (
    <Refs key={k} text={t} onWorkflow={pickWorkflow} plain={(x) => x} />
  );
  // an earlier turn's steps fold into one line once a newer turn starts (item 25); the newest turn stays open. A turn
  // is what one message of yours started (an entry's `turn` counts inputs, results too, so it is not this); a steer
  // read while it worked stays in its turn
  const turnOf = new Map<string, number>();
  let last = 0;
  for (const e of entries) {
    if (e.kind === "user" && (!e.steer || e.steer === "next")) last++;
    turnOf.set(e.id, last);
  }
  const older = new Map<number, Line[]>();
  for (const e of entries)
    if (e.kind === "line" && e.line && turnOf.get(e.id)! < last)
      older.set(turnOf.get(e.id)!, [...(older.get(turnOf.get(e.id)!) ?? []), e.line]);
  const folded = new Set<number>();
  // timecodes jump the playhead on the Timeline (item 24), plain text elsewhere
  const tl = a.reg.current()?.spec;
  const setPlayhead = tl?.screen === "timeline" ? tl.actions.setPlayhead : undefined;
  // a time past the open sequence's end stays text (setPlayhead would refuse it)
  let max = NaN;
  try {
    max = Number(tl?.summary().length_s);
  } catch {
    /* no sequence open */
  }
  const seek: Seek | undefined =
    setPlayhead && max >= 0
      ? Object.assign((s: number) => void setPlayhead({ at_s: s }, { progress: () => undefined }), {
          max,
        })
      : undefined;
  const latestReply = entries.findLast((e) => e.kind === "assistant")?.id;
  const flush = (key: string) => {
    if (!words.length && !lines.length && fold === null) return;
    const said = words;
    // read on click: by then the chips have their titles (Copy says titles, never [[…]])
    const copy = said.length > 0 && (() => said.map((w) => plainAnswer(w.text ?? "")).join("\n\n"));
    // a Using skill line leads the reply, the files it read in its box (board frame 16)
    const { used, rest } = skillLines(lines);
    const parts = said.map((w) => ({ w, ...splitOptions(w.text ?? "") }));
    out.push(
      <div key={`b${key}`} className="msg">
        {used.map((u) => (
          <SkillLine key={u.line.id} line={u.line} reads={u.reads} />
        ))}
        {parts.some((x) => x.text) && (
          // one clamp for the whole reply (board frame 17): every note of the turn and the answer, never per paragraph
          <Clamp
            k={parts.map((x) => x.text).join("\n\n")}
            whole={
              // the latest reply always shows in full; older ones collapse (owner, 2026-11-25)
              parts.some((x) => x.w.id === latestReply) ||
              parts.some((x) => answerBlocks(x.text).some((b) => b.k === "code" || b.k === "plan"))
            }
          >
            {parts.map(
              (x) => x.text && <Answer key={x.w.id} text={x.text} seek={seek} refs={refs} />,
            )}
          </Clamp>
        )}
        {(() => {
          // alpha.2 items 17, 19: one set of cards per reply, the newest (a reply that repeats its choices after a
          // step shows them once), outside the clamp so they always show
          const x = parts.findLast((y) => y.options);
          return (
            x?.options && (
              <Options
                key={`o${x.w.id}`}
                entry={x.w}
                entries={entries}
                options={x.options}
                readOnly={a.readOnly}
                send={a.send}
              />
            )
          );
        })()}
        {fold !== null && <Folded key={`f${fold}`} lines={older.get(fold) ?? []} a={a} />}
        {rest.length > 0 && <Lines lines={rest} a={a} />}
        {lines
          .filter((l) => l.sources?.length)
          .map((l) => (
            // three sources as small text, the rest counted (board frame 6): never links, nothing in them acts
            <ol key={`s${l.id}`} className="src">
              {l.sources!.slice(0, 3).map((s) => (
                <li key={s.url} title={sourceText(s)}>
                  {sourceText(s)}
                </li>
              ))}
              {l.sources!.length > 3 && (
                <li className="list-none">+ {l.sources!.length - 3} more</li>
              )}
            </ol>
          ))}
        {copy && (
          <div className="ma">
            <CopyButton text={copy} label="Copy the answer">
              Copy
            </CopyButton>
          </div>
        )}
      </div>,
    );
    words = [];
    lines = [];
    fold = null;
  };
  const lastEntry = entries.at(-1)?.id;
  for (const e of entries) {
    if (e.kind === "assistant") words.push(e);
    else if (e.kind === "line" && e.line && turnOf.get(e.id)! < last) {
      const n = turnOf.get(e.id)!;
      if (!folded.has(n) && fold === null) {
        folded.add(n);
        fold = n;
      }
    } else if (e.kind === "line" && e.line) lines.push(e.line);
    else {
      flush(e.id);
      if (e.kind === "user" && e.steer === "waiting")
        steers.push(
          <div key={e.id} className="msg me steer" aria-label="Waiting for the next step">
            <Said e={e} />
            <small>
              <Clock3 size={11} strokeWidth={2} aria-hidden />
              at the next step
            </small>
          </div>,
        );
      else if (e.kind === "user")
        out.push(
          <div key={e.id} className="msg me">
            <Said e={e} />
            {e.steer === "next" && (
              <small>
                <ChevronRight size={11} strokeWidth={2} aria-hidden />
                sent as the next message
              </small>
            )}
          </div>,
        );
      else if (e.kind === "card" && e.card) {
        const c = e.card;
        out.push(
          <Card
            key={e.id}
            card={serverCard(c)}
            waiting={c.state === "waiting" && !a.readOnly}
            after={CARD_STATE[c.state]}
            takeIds={c.take_ids}
            onYes={() => a.answerCard(c.id, true)}
            onNo={() => a.answerCard(c.id, false)}
          />,
        );
      } else if (e.kind === "notice" && e.error?.code === "answer")
        out.push(
          <Broken
            key={e.id}
            raw={e.text ?? e.error.text}
            retry={e.id === lastEntry && !a.readOnly && !a.steering ? a.retry : undefined}
          />,
        );
      else if (e.kind === "notice" && !["cap_day", "cap_tokens"].includes(e.error?.code ?? ""))
        out.push(
          <div key={e.id} role="note" className="msg note">
            <Text text={e.text ?? ""} />
          </div>,
        );
    }
  }
  // what this tab runs now, until the server's lines come
  const pending = a.view?.thread.pending?.actions ?? [];
  for (const act of pending)
    if (!known.has(act.id) && a.local[act.id])
      lines.push({ id: act.id, undo: true, ...a.local[act.id] });
  flush("end");
  if (a.asking)
    out.push(
      <Card
        key={`ask${a.asking.id}`}
        card={a.asking.card}
        waiting
        busy={a.asking.busy}
        onYes={() => a.answerAsk(true)}
        onNo={() => a.answerAsk(false)}
      />,
    );
  return (
    <>
      {out}
      {steers}
    </>
  );
}

/** Your message: its attached images (item 8), then your words with the takes you picked with @ as chips (item 7). */
function Said({ e }: { e: Entry }) {
  return (
    <>
      {e.takes?.map((id) => (
        <SentAtt key={id} id={id} />
      ))}
      <Refs text={e.text ?? ""} plain={(x, k) => <Text key={k} text={x} />} />
    </>
  );
}

/** A memory item's ⋯ menu: Edit and Delete (Delete is two taps: ⋯, then Delete). Esc or a click outside closes it. */
function RowMenu({ busy, edit, remove }: { busy: boolean; edit: () => void; remove: () => void }) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return;
    ref.current?.querySelector<HTMLElement>("[role=menuitem]")?.focus();
    const out = (e: PointerEvent) => !ref.current?.contains(e.target as Node) && setOpen(false);
    document.addEventListener("pointerdown", out);
    return () => document.removeEventListener("pointerdown", out);
  }, [open]);
  const pick = (f: () => void) => () => {
    setOpen(false);
    f();
  };
  return (
    <div ref={ref} className="relative shrink-0">
      <button
        type="button"
        className="ib"
        aria-label="More for this item"
        title="More for this item"
        aria-haspopup="menu"
        aria-expanded={open}
        onClick={() => setOpen(!open)}
      >
        <Ellipsis size={15} strokeWidth={2} aria-hidden />
      </button>
      {open && (
        <div
          role="menu"
          aria-label="More for this item"
          className="pop"
          onKeyDown={(e) => {
            if (e.key !== "Escape") return;
            e.preventDefault();
            setOpen(false);
            ref.current?.querySelector<HTMLElement>("button")?.focus();
          }}
        >
          <button type="button" role="menuitem" className="pr" onClick={pick(edit)}>
            <Pencil size={14} strokeWidth={1.75} aria-hidden />
            Edit
          </button>
          <button
            type="button"
            role="menuitem"
            className="pr"
            disabled={busy}
            onClick={pick(remove)}
          >
            <Trash2 size={14} strokeWidth={1.75} aria-hidden />
            Delete
          </button>
        </div>
      )}
    </div>
  );
}

type MemoryItem = S<"AssistantMemoryItem">;
type MemoryKind = S<"AssistantMemoryKind">;

/** The Memory list (§22.8, board frame 6): the four kinds as tabs, each item with its chat, Edit and Delete (your
 * clicks), and Clear all behind a card. Items are text, never orders. */
function MemoryList() {
  const [kind, setKind] = useState<MemoryKind>("preference");
  const [items, setItems] = useState<MemoryItem[] | null>(null);
  const [editing, setEditing] = useState<{ id: string; text: string } | null>(null);
  const [clearing, setClearing] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const load = useCallback(
    () =>
      ok(api.GET("/v1/studio/assistant/memory"))
        .then((l) => {
          setItems(l.items);
          setError("");
        })
        .catch((e) => setError(message(e))),
    [],
  );
  useEffect(() => {
    void load();
  }, [load]);
  const act = async (f: () => Promise<unknown>) => {
    setBusy(true);
    try {
      await f();
      setError("");
    } catch (e) {
      setError(message(e));
    } finally {
      setBusy(false);
    }
  };
  const save = (id: string, text: string) =>
    act(async () => {
      const x = await ok(
        api.PATCH("/v1/studio/assistant/memory/{item_id}", {
          params: { path: { item_id: id } },
          body: { text: text.trim() },
        }),
      );
      setItems((l) => l?.map((y) => (y.id === id ? x : y)) ?? null);
      setEditing(null);
    });
  const remove = (id: string) =>
    act(async () => {
      await ok(
        api.DELETE("/v1/studio/assistant/memory/{item_id}", { params: { path: { item_id: id } } }),
      );
      setItems((l) => l?.filter((y) => y.id !== id) ?? null);
    });
  const shown = (items ?? []).filter((x) => x.kind === kind);
  return (
    <div className="thr justify-start! overflow-y-auto [mask-image:none]!">
      <div role="tablist" aria-label="Memory" className="flex gap-1 rounded-xl bg-s3 p-[3px]">
        {MEMORY_TABS.map(([k, label]) => (
          <button
            key={k}
            type="button"
            role="tab"
            aria-selected={kind === k}
            onClick={() => setKind(k)}
            className={`h-[30px] flex-1 rounded-[9px] border-0 text-[11px] font-semibold shadow-none! ${kind === k ? "bg-s5" : "bg-transparent text-t2"}`}
          >
            {label}
          </button>
        ))}
      </div>
      {items === null && !error ? (
        <p className="note">Reading what it remembers…</p>
      ) : shown.length === 0 ? (
        <p className="note">Nothing here yet.</p>
      ) : (
        <ul className="mem" aria-label={MEMORY_TABS.find(([k]) => k === kind)![1]}>
          {shown.map((x) =>
            editing?.id === x.id ? (
              <li key={x.id} className="mrow items-center">
                <label htmlFor={`mem-${x.id}`} className="sr-only">
                  Edit
                </label>
                <input
                  id={`mem-${x.id}`}
                  className="in h-8 min-w-0 flex-1 text-[13px]"
                  maxLength={500}
                  value={editing.text}
                  autoFocus
                  onChange={(e) => setEditing({ id: x.id, text: e.target.value })}
                  onKeyDown={(e) => {
                    if (e.key === "Enter") void save(x.id, editing.text);
                    if (e.key === "Escape") setEditing(null);
                  }}
                />
                <button
                  type="button"
                  className="un"
                  disabled={busy || !editing.text.trim()}
                  onClick={() => void save(x.id, editing.text)}
                >
                  Save
                </button>
                <button type="button" className="un" onClick={() => setEditing(null)}>
                  Cancel
                </button>
              </li>
            ) : (
              // the text takes the full width, 3 lines then More; Edit and Delete in ⋯ (item 5, board frame 14a)
              <li key={x.id} className="mrow">
                <div>
                  <Clamp k={x.text}>{x.text}</Clamp>
                  <small>
                    chat · {x.thread?.title || "deleted"} · {ago(x.updated_at)}
                  </small>
                </div>
                <RowMenu
                  busy={busy}
                  edit={() => setEditing({ id: x.id, text: x.text })}
                  remove={() => void remove(x.id)}
                />
              </li>
            ),
          )}
        </ul>
      )}
      {error && (
        <p role="alert" className="fine text-bad">
          {error}
        </p>
      )}
      <p className="fine">
        Yours only; never sent as orders. Deleting a chat deletes its summary in Chats.
      </p>
      {clearing ? (
        <Card
          card={{
            kind: "delete",
            title: "Clear all memory?",
            rows: [
              ["Goes", "every item in Likes, Projects, Worked and Chats"],
              ["Stays", "your chats"],
            ],
            tags: [],
            go: "CLEAR ALL",
            danger: true,
            fine: "This can't be undone.",
          }}
          waiting
          busy={busy}
          onYes={() =>
            void act(async () => {
              await ok(api.DELETE("/v1/studio/assistant/memory"));
              setItems([]);
              setClearing(false);
            })
          }
          onNo={() => setClearing(false)}
        />
      ) : (
        <div className="flex justify-end">
          <button
            type="button"
            className="no h-8 text-xs text-[#ff8a80]"
            disabled={!items?.length}
            onClick={() => setClearing(true)}
          >
            Clear all
          </button>
        </div>
      )}
    </div>
  );
}

const mac = () =>
  typeof navigator !== "undefined" && /Mac|iPhone|iPad/.test(navigator.platform ?? "");
export const shortcut = () => (mac() ? "⌘J" : "Ctrl J");
const fullKey = () => (mac() ? "⇧⌘J" : "Ctrl Shift J");

/** A dialog or menu is open (Esc is its own then): a screen's closed popover (Run on, Memory) does not count. */
const overlayOpen = () =>
  [...document.querySelectorAll('dialog[open], [role="dialog"], [role="menu"]')].some((el) => {
    if (!el.hasAttribute("popover")) return true;
    try {
      return el.matches(":popover-open");
    } catch {
      return false;
    }
  });

/** The minimized bar (item 4, board frame 9): bottom right, 44 px; Working with Stop, Waiting for you and what, a
 * new answer, else idle. A click opens the drawer as it was left. It moves up rather than cover a Generate button
 * (the screens' primary `.key` buttons). */
function MinBar({ a }: { a: AssistantApi }) {
  const ref = useRef<HTMLDivElement>(null);
  const [lift, setLift] = useState(0);
  const liftRef = useRef(0);
  // it shows only after Minimize (or Esc, ⌘J back to it) in the drawer: the focus moves to it, not to the page
  useEffect(() => {
    ref.current?.querySelector("button")?.focus();
  }, []);
  useEffect(() => {
    // ponytail: checked every 400 ms and on scroll/resize; a ResizeObserver on the keys if that ever costs
    const check = () => {
      const el = ref.current;
      if (!el) return;
      const r = el.getBoundingClientRect();
      const top = r.top + liftRef.current;
      const bottom = r.bottom + liftRef.current;
      let up = 0;
      for (const k of document.querySelectorAll<HTMLElement>("button.key")) {
        const b = k.getBoundingClientRect();
        if (b.width && b.right > r.left && b.left < r.right && b.bottom > top && b.top < bottom)
          up = Math.max(up, bottom - b.top + 8);
      }
      if (up !== liftRef.current) setLift((liftRef.current = up));
    };
    const id = setInterval(check, 400);
    const first = setTimeout(check);
    addEventListener("resize", check);
    addEventListener("scroll", check, true);
    return () => {
      clearInterval(id);
      clearTimeout(first);
      removeEventListener("resize", check);
      removeEventListener("scroll", check, true);
    };
  }, []);
  const t = a.view?.thread;
  const card =
    a.asking?.card.title ??
    a.view?.entries.findLast((e) => e.kind === "card" && e.card?.state === "waiting")?.card?.title;
  const working = !a.readOnly && (!!a.running || t?.state === "working" || t?.state === "waiting");
  const [state, text, sub, dot] = a.cardWaits
    ? ["Waiting for you", "Waiting for you", card && `${card.replace(/\?$/, "")}?`, "ask"]
    : working && a.stopping
      ? ["Stopping", "Stopping…", "", ""]
      : working
        ? [
            "Working",
            a.running?.text ?? (a.status?.thinks ? "Thinking…" : "Working…"),
            a.running ? `Working · ${a.running.n} of ${a.running.of}` : "Working",
            "",
          ]
        : a.newAnswer !== null
          ? ["new answer", "New answer", plainAnswer(a.newAnswer).split("\n")[0], "new"]
          : ["", "Assistant", "", ""];
  return (
    <div ref={ref} className="ast-min" style={lift ? { marginBottom: lift } : undefined}>
      <button
        type="button"
        aria-label={`Open the Assistant${state ? ` · ${state}` : ""}`}
        onClick={() => a.setOpen(true)}
      >
        <span className="lg">
          <Sparkles size={18} strokeWidth={2} aria-hidden />
          {dot && <span className={`dot ${dot}`} />}
        </span>
        <span className="tx">
          <span className="truncate">{text}</span>
          {sub && <small>{sub}</small>}
        </span>
      </button>
      {working && a.steering && (
        <button type="button" className="no" disabled={a.stopping} onClick={a.stopAll}>
          {a.stopping ? "Stopping…" : "Stop"}
        </button>
      )}
    </div>
  );
}

/** Screen readers hear only what is new (item 23): the newest words or card since the chat was first shown, in a
 * region of their own outside the drawer (open, minimized or closed), never the whole thread. */
function Announce({ a }: { a: AssistantApi }) {
  const said = a.view?.entries.findLast((e) => e.kind === "assistant" || e.kind === "card");
  const [heard, setHeard] = useState<{ t: string; id: string | null } | null>(null);
  const tid = a.view?.thread.id;
  if (tid && heard?.t !== tid) setHeard({ t: tid, id: said?.id ?? null });
  return (
    <div className="sr-only" aria-live="polite">
      {said && heard && heard.t === tid && said.id !== heard.id
        ? said.kind === "card"
          ? `Asks: ${said.card?.title ?? ""}`
          : plainAnswer(said.text ?? "")
        : ""}
    </div>
  );
}

/** The drawer (or, on a phone, the bottom sheet), and what screen readers hear. */
export function AssistantDrawer({ a }: { a: AssistantApi }) {
  return (
    <>
      <Announce a={a} />
      <Drawer a={a} />
    </>
  );
}

/** An image attached in the box: uploading until it has its take's id. */
type Att = { key: string; name: string; url: string; id?: string };

function Drawer({ a }: { a: AssistantApi }) {
  const [text, setText] = useState("");
  const [sending, setSending] = useState(false);
  // media in the box (board frame 12): images attached (uploaded at once, sent as takes) and takes picked with @ (the
  // box shows their titles in brackets; sent as refs)
  const [atts, setAtts] = useState<Att[]>([]);
  const [chosen, setChosen] = useState<{ label: string; id: string }[]>([]);
  const [caret, setCaret] = useState(0);
  const [atOff, setAtOff] = useState<number | null>(null); // Escape closed the picker of the @ at this place
  const atKeys = useRef<((k: AtKey) => void) | null>(null);
  const [atCur, setAtCur] = useState(""); // the @ picker's highlighted option, for aria-activedescendant
  const [dropping, setDropping] = useState(false);
  const [told, setTold] = useState(false); // "This model can't see images" shows once
  const [autoTip, setAutoTip] = useState(false); // the Auto switch's line (board frame 13a): hover, focus or hold
  const hold = useRef<ReturnType<typeof setTimeout> | null>(null);
  const file = useRef<HTMLInputElement>(null);
  const [top, setTop] = useState<number | null>(null);
  const [memory, setMemory] = useState(false);
  const [chats, setChats] = useState(false);
  const chatsBtn = useRef<HTMLButtonElement>(null);
  const closeChats = useCallback(() => setChats(false), []);
  const openChats = () => {
    a.loadChats();
    setChats(true);
  };
  const phone = usePhone();
  const end = useRef<HTMLDivElement>(null);
  const input = useRef<HTMLTextAreaElement>(null);
  // the screen's Sees chips follow the screen as it changes
  const seen = useSyncExternalStore(
    (f) => a.reg.subscribe(f),
    () => (a.reg.current()?.spec.sees ?? []).join("\u0000"),
    () => "",
  );
  const sees = seen ? seen.split("\u0000") : [];
  const screen = a.reg.current()?.spec;
  const entries = a.view?.entries ?? [];
  const count = entries.length + Object.keys(a.local).length + (a.asking ? 1 : 0);
  // the Thinking start, kept here (above the closed-drawer return) so closing and reopening keeps the count; a new
  // thread, actions running or the state leaving working start it again
  const thinkId = a.view?.thread.state === "working" && !a.running ? a.view.thread.id : null;
  const [since, setSince] = useState<{ id: string; at: number } | null>(null);
  if ((since?.id ?? null) !== thinkId)
    setSince(() => (thinkId ? { id: thinkId, at: Date.now() } : null));
  const thinking = !!since && !a.readOnly && !a.cardWaits;
  useEffect(() => {
    end.current?.scrollIntoView({ block: "nearest" });
  }, [count, thinking]);
  useEffect(() => {
    if (a.open) input.current?.focus();
  }, [a.open]);
  // Stop greys out: focus goes to the message box (board frame 15)
  useEffect(() => {
    if (a.stopping) input.current?.focus();
  }, [a.stopping]);
  if (!a.open) {
    if (chats) setChats(false); // the list never comes back open with the drawer
    return a.min ? <MinBar a={a} /> : null;
  }
  const full = a.full && !phone;
  const limit = limitOf(entries, a.capLimits);
  const st = a.status;
  const t = a.view?.thread;
  const chip = a.readOnly ? (
    <span className="st ro-chip">Read-only</span>
  ) : a.stopping ? (
    <span className="st st-go">Stopping…</span>
  ) : a.cardWaits ? (
    <span className="st st-ask">Waiting</span>
  ) : a.running || t?.state === "working" || t?.state === "waiting" ? (
    <span className="st st-go">Working</span>
  ) : null;
  const uploading = atts.some((x) => !x.id);
  async function submit(e?: React.FormEvent) {
    e?.preventDefault();
    if (!text.trim() || sending || uploading) return;
    const said = withPicks(text, chosen); // each picked title becomes a 45-character ref
    if (said.length > 4000) {
      a.setError("Too long with its picked takes: shorten the message.");
      return;
    }
    setSending(true);
    if (
      await a.send(
        said,
        atts.map((x) => x.id!),
      )
    ) {
      setText("");
      setChosen([]);
      if (atts.length) setTold(true);
      atts.forEach((x) => URL.revokeObjectURL(x.url));
      setAtts([]);
    }
    setSending(false);
  }
  /** Paperclip, paste or drop: each image uploads at once (to Uploads); the message names it once it has. */
  const attach = (files: File[]) => {
    const ok = files
      .filter((f) => /^image\/(png|jpeg|webp)$/.test(f.type))
      .slice(0, 8 - atts.length);
    if (ok.length < files.length) a.setError("Attach PNG, JPEG or WebP images, at most 8.");
    for (const f of ok) {
      const key = `${Date.now()}-${Math.random()}`;
      // a pasted screenshot has no name of its own
      const name = f.name && f.name !== "image.png" ? f.name : "Pasted image";
      setAtts((l) => [...l, { key, name, url: URL.createObjectURL(f) }]);
      uploadFile(f, () => undefined)
        .then((t) => setAtts((l) => l.map((x) => (x.key === key ? { ...x, id: t.id } : x))))
        .catch((err) => {
          setAtts((l) => l.filter((x) => x.key !== key));
          a.setError(message(err));
        });
    }
  };
  // the @ picker (item 7, board frames 12c and 16c: skills, elements, takes, uploads): open while the caret ends an @word, until Escape closes that one
  const at = a.readOnly ? null : atQuery(text, caret);
  const atOpen = !!at && at.start !== atOff;
  const pick = (p: Pick) => {
    if (!at) return;
    const ins =
      p.kind === "skill"
        ? `@${p.skill.name} `
        : p.kind === "element"
          ? `@${p.handle} `
          : `[${p.take.title}] `;
    if (p.kind === "take") setChosen((l) => [...l, { label: p.take.title, id: p.take.id }]);
    setText(text.slice(0, at.start) + ins + text.slice(caret));
    const c = at.start + ins.length;
    setCaret(c);
    requestAnimationFrame(() => {
      input.current?.focus();
      input.current?.setSelectionRange(c, c);
    });
  };
  const sendExample = async (x: string) => {
    setSending(true);
    await a.send(x);
    setSending(false);
  };
  const onMachine = st?.provider && !st.provider.remote;
  const picks = a.readOnly ? null : liveOptions(entries); // keys 1–4 send one (board frame 11)

  if (phone && a.shrunk)
    return (
      <div className="ast sheet" role="complementary" aria-label="Assistant">
        <span className="grab" aria-hidden />
        <div role="status" className="flex min-h-10 items-center gap-2.5">
          {a.running ? (
            <LoaderCircle size={18} strokeWidth={2.5} aria-hidden className="run" />
          ) : (
            <Sparkles size={18} aria-hidden className="ok" />
          )}
          <span className="min-w-0 flex-1 truncate text-sm font-medium">
            {a.running?.text ?? "Assistant"}
          </span>
          {a.running && (
            <span className="mono text-[11px] text-t2">
              {a.running.n} of {a.running.of}
            </span>
          )}
          {a.running && (
            <button
              type="button"
              className="no h-8 px-3 text-xs"
              disabled={a.stopping}
              onClick={a.stopAll}
            >
              {a.stopping ? "Stopping…" : "Stop"}
            </button>
          )}
          <button
            type="button"
            className="ib"
            aria-label="Open the chat"
            onClick={() => a.setShrunk(false)}
          >
            <ChevronUp size={15} strokeWidth={2} aria-hidden />
          </button>
        </div>
      </div>
    );

  const drag = (e: React.PointerEvent) => {
    const el = e.currentTarget as HTMLElement;
    el.setPointerCapture(e.pointerId);
    const move = (m: PointerEvent) =>
      setTop(Math.min(innerHeight * 0.85, Math.max(innerHeight * 0.08, m.clientY)));
    const up = () => {
      el.removeEventListener("pointermove", move);
      el.removeEventListener("pointerup", up);
    };
    el.addEventListener("pointermove", move);
    el.addEventListener("pointerup", up);
  };

  return (
    <div
      className={`ast ${phone ? "sheet" : "drw"} ${full ? "full" : ""}`}
      role="complementary"
      aria-label="Assistant"
      style={phone && top !== null ? { top } : undefined}
      // Escape closes it (owner, 2026-11-25) unless a dialog, menu or card is open; the work keeps going. Full screen
      // goes back to the drawer; opened from the minimized bar, it goes back to the bar
      onKeyDown={(e) => {
        if (
          e.key === "Escape" &&
          !e.defaultPrevented &&
          !memory &&
          !a.cardWaits &&
          !overlayOpen()
        ) {
          if (full) a.setFull(false);
          else a.setOpen(false);
        }
      }}
    >
      {phone && <span className="grab" aria-hidden onPointerDown={drag} />}
      {memory ? (
        <>
          <div className="dh">
            <h2>
              <Sparkles aria-hidden strokeWidth={2} />
              Memory
            </h2>
            <button
              type="button"
              className="x"
              aria-label="Close Memory"
              title="Close Memory"
              onClick={() => setMemory(false)}
            >
              <X size={16} strokeWidth={1.75} aria-hidden />
            </button>
          </div>
          <MemoryList />
        </>
      ) : (
        <>
          <div className="dh">
            {/* while a chip shows (Working, Waiting) it takes the title's place, so every button fits */}
            <h2 aria-label="Assistant" className={chip ? "shrink-0 grow-0" : ""}>
              <Sparkles aria-hidden strokeWidth={2} />
              {!chip && <span className="truncate">Assistant</span>}
            </h2>
            {chip}
            {chip && <span className="flex-1" />}
            <button
              type="button"
              className="ib"
              aria-label="Memory"
              title="Memory"
              onClick={() => setMemory(true)}
            >
              <Brain size={15} strokeWidth={1.75} aria-hidden />
            </button>
            <button
              ref={chatsBtn}
              type="button"
              className="ib"
              aria-label="Chats"
              title="Chats"
              aria-haspopup="menu"
              aria-expanded={chats}
              onClick={() => (chats ? setChats(false) : openChats())}
            >
              <MessagesSquare size={15} strokeWidth={1.75} aria-hidden />
            </button>
            {phone ? (
              <button
                type="button"
                className="ib"
                aria-label="Shrink"
                onClick={() => a.setShrunk(true)}
              >
                <ChevronDown size={15} strokeWidth={2} aria-hidden />
              </button>
            ) : (
              <button
                type="button"
                className="ib"
                aria-label={`${full ? "Back to the drawer" : "Full screen"} (${fullKey()})`}
                title={`${full ? "Back to the drawer" : "Full screen"} (${fullKey()})`}
                onClick={() => a.setFull(!full)}
              >
                {full ? (
                  <Minimize2 size={15} strokeWidth={1.75} aria-hidden />
                ) : (
                  <Maximize2 size={15} strokeWidth={1.75} aria-hidden />
                )}
              </button>
            )}
            <button
              type="button"
              className="ib"
              aria-label="Minimize"
              title="Minimize"
              onClick={a.minimize}
            >
              <Minus size={15} strokeWidth={2} aria-hidden />
            </button>
            <button
              type="button"
              className="x"
              aria-label={`Close (${shortcut()})`}
              title={`Close (${shortcut()})`}
              onClick={a.close}
            >
              <X size={16} strokeWidth={1.75} aria-hidden />
            </button>
          </div>
          {chats && (
            <ChatsList
              a={a}
              btn={chatsBtn}
              close={closeChats}
              pick={(id) => {
                setChats(false);
                if (id === null) void a.newChat();
                else if (id !== a.view?.thread.id) void a.openChat(id);
              }}
            />
          )}
          {!phone && sees.length > 0 && (
            <div className="where">
              Sees
              {sees.map((s) => (
                <span key={s}>{s}</span>
              ))}
            </div>
          )}
          {onMachine && (
            <div className="rem local">
              <Globe size={13} strokeWidth={1.75} aria-hidden />
              Runs on this machine
            </div>
          )}

          <div className={`thr ${a.readOnly ? "ro" : ""}`}>
            {entries.length === 0 && !a.asking ? (
              <div className="flex flex-col gap-2.5">
                <p className="m-0 font-display text-lg font-bold">Hi. What should we make?</p>
                <p className="note">
                  I can fill this screen for you. Anything that uses the GPU waits for your OK.
                </p>
                {(screen?.examples ?? EXAMPLES).map((x) => (
                  <button
                    key={x.text}
                    type="button"
                    className="ex"
                    disabled={sending || !!limit}
                    onClick={() => void sendExample(x.text)}
                  >
                    <Text text={x.text} />
                    <small>{x.sub}</small>
                  </button>
                ))}
              </div>
            ) : (
              <Thread a={a} entries={entries} />
            )}
            {a.stopping ? (
              <div className="msg think" role="status">
                <LoaderCircle size={12} strokeWidth={3} aria-hidden className="run" />
                Stopping…
              </div>
            ) : (
              since && thinking && <Thinking since={since.at} thinks={!!a.status?.thinks} />
            )}
            <div ref={end} />
          </div>

          {a.readOnly && (
            <>
              <div className="bar2 bg-s2">
                <Monitor size={18} strokeWidth={1.75} aria-hidden className="shrink-0 text-t2" />
                <span className="flex-1">
                  This chat is working in another tab. Its actions run there.
                </span>
              </div>
              <button
                type="button"
                className="go normal-case tracking-normal"
                onClick={a.continueHere}
              >
                Continue here
              </button>
              <p className="fine">
                Continue here moves the chat to this tab. The other tab turns read-only.
              </p>
            </>
          )}
          {limit && (
            <>
              <div role="alert" className="bar2 limit">
                <CircleAlert size={18} strokeWidth={1.75} aria-hidden className="shrink-0" />
                <span className="cap">
                  <b>{limit.title}</b>
                  <span>{limit.text}</span>
                </span>
              </div>
              <p className="fine">Your message is kept. The screen still works as usual.</p>
            </>
          )}
          {a.error && (
            <p role="alert" className="fine text-bad">
              {a.error}
            </p>
          )}
          {!a.readOnly && atts.length > 0 && !told && st && !st.sees && (
            <div className="bar2 bg-s2">
              <EyeOff size={18} strokeWidth={1.75} aria-hidden className="shrink-0 text-t2" />
              <span className="flex-1">This model can&apos;t see images; it will use Look at.</span>
            </div>
          )}
          {!a.readOnly && t?.auto && a.steering && typeof t.caps.auto_takes_hour === "number" && (
            <div className="est">
              <Zap size={12} strokeWidth={2} aria-hidden />
              <span>
                {t.caps.auto_takes_hour} of {st?.limits.auto_takes_per_hour ?? 8} takes this hour
              </span>
            </div>
          )}
          {!a.readOnly && autoTip && (
            <div className="tip" role="tooltip" id="auto-tip">
              <b>Auto</b>
              Runs safe steps without asking: generations on your machines, Hear it, saves you can
              undo. Up to the cap. Deletes, anything off the machine and big runs still ask.
            </div>
          )}
          {!a.readOnly && dropping && (
            <div className="drop" aria-hidden>
              <ImagePlus size={18} strokeWidth={1.75} />
              Drop to attach
            </div>
          )}
          {!a.readOnly && (
            <form
              className={`cmp relative ${limit ? "opacity-50" : ""}`}
              onSubmit={(e) => void submit(e)}
              onDragOver={(e) => {
                if (!limit && e.dataTransfer.types.includes("Files")) {
                  e.preventDefault();
                  setDropping(true);
                }
              }}
              onDragLeave={() => setDropping(false)}
              onDrop={(e) => {
                setDropping(false);
                if (limit || !e.dataTransfer.files.length) return;
                e.preventDefault();
                attach([...e.dataTransfer.files]);
              }}
            >
              {atOpen && <AtPicker q={at.q} keysRef={atKeys} onPick={pick} onActive={setAtCur} />}
              {atts.length > 0 && (
                <div className="flex flex-wrap gap-1.5">
                  {atts.map((x) => (
                    <AttChip
                      key={x.key}
                      name={x.name}
                      url={x.url}
                      busy={!x.id}
                      onRemove={() => {
                        URL.revokeObjectURL(x.url);
                        setAtts((l) => l.filter((y) => y.key !== x.key));
                      }}
                    />
                  ))}
                </div>
              )}
              <label htmlFor="assistant-in" className="sr-only">
                Message the assistant
              </label>
              <TextArea
                id="assistant-in"
                expand="Message"
                ref={input}
                rows={phone ? 1 : 2}
                maxLength={4000}
                disabled={!!limit}
                placeholder={
                  limit ? limit.until : a.steering ? "Steer the answer…" : "Ask, or say what to do"
                }
                value={text}
                aria-autocomplete="list"
                aria-expanded={atOpen}
                aria-haspopup="listbox"
                aria-controls={atOpen ? "at-list" : undefined}
                aria-activedescendant={atOpen && atCur ? atCur : undefined}
                onChange={(e) => {
                  setText(e.target.value);
                  setCaret(e.target.selectionStart);
                }}
                onSelect={(e) => setCaret(e.currentTarget.selectionStart)}
                onPaste={(e) => {
                  const files = [...e.clipboardData.files];
                  if (files.length && !limit) {
                    e.preventDefault();
                    attach(files);
                  }
                }}
                onKeyDown={(e) => {
                  if (atOpen && (e.key === "ArrowDown" || e.key === "ArrowUp")) {
                    e.preventDefault();
                    atKeys.current?.(e.key === "ArrowDown" ? "down" : "up");
                    return;
                  }
                  if (atOpen && ((e.key === "Enter" && !e.shiftKey) || e.key === "Tab")) {
                    e.preventDefault();
                    atKeys.current?.("enter");
                    return;
                  }
                  if (atOpen && e.key === "Escape") {
                    e.preventDefault(); // the drawer stays open
                    setAtOff(at.start);
                    return;
                  }
                  const n = Number(e.key);
                  if (
                    picks &&
                    !text &&
                    n >= 1 &&
                    n <= picks.length &&
                    !e.metaKey &&
                    !e.ctrlKey &&
                    !e.altKey
                  ) {
                    e.preventDefault();
                    void sendExample(picks[n - 1].title);
                  } else if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
                    e.preventDefault();
                    void submit();
                  }
                }}
              />
              <div className="cmpb">
                <button
                  type="button"
                  className="att"
                  aria-label="Attach an image"
                  title="Attach an image"
                  disabled={!!limit}
                  style={atts.length ? { background: "var(--s4)", color: "var(--t1)" } : undefined}
                  onClick={() => file.current?.click()}
                >
                  <Paperclip size={16} strokeWidth={1.75} aria-hidden />
                </button>
                <input
                  ref={file}
                  type="file"
                  accept="image/png,image/jpeg,image/webp"
                  multiple
                  hidden
                  onChange={(e) => {
                    attach([...(e.target.files ?? [])]);
                    e.target.value = "";
                  }}
                />
                <button
                  type="button"
                  className="aut"
                  role="switch"
                  aria-checked={!!t?.auto}
                  aria-describedby={autoTip ? "auto-tip" : undefined}
                  title="Auto: safe steps run without asking"
                  onClick={() => void a.setAuto(!t?.auto)}
                  onMouseEnter={() => setAutoTip(true)}
                  onMouseLeave={() => setAutoTip(false)}
                  onFocus={() => setAutoTip(true)}
                  onBlur={() => setAutoTip(false)}
                  onPointerDown={(e) => {
                    if (e.pointerType === "touch")
                      hold.current = setTimeout(() => setAutoTip(true), 500);
                  }}
                  onPointerUp={() => {
                    if (hold.current) clearTimeout(hold.current);
                  }}
                >
                  <Zap size={11} strokeWidth={2} aria-hidden />
                  Auto
                </button>
                <small>
                  {a.stopping ? null : atOpen ? (
                    <>
                      <span className="kbd">↑↓</span> pick · <span className="kbd">Enter</span>
                    </>
                  ) : a.steering ? (
                    <>
                      <span className="kbd">Enter</span> steers this answer
                    </>
                  ) : picks ? (
                    !phone && (
                      <>
                        <span className="kbd">1</span>–<span className="kbd">{picks.length}</span>{" "}
                        picks
                      </>
                    )
                  ) : (
                    !phone && (
                      <>
                        {atts.length > 0 ? (
                          "or paste, or drop"
                        ) : (
                          <>
                            <span className="kbd">{shortcut()}</span> open or close ·{" "}
                            <span className="kbd">@</span> takes
                          </>
                        )}
                      </>
                    )
                  )}
                </small>
                {a.steering && (
                  <button
                    type="button"
                    className="no h-[34px] px-3 text-xs"
                    disabled={a.stopping}
                    onClick={a.stopAll}
                  >
                    {a.stopping ? "Stopping…" : "Stop"}
                  </button>
                )}
                <button
                  type="submit"
                  className="snd"
                  aria-label="Send"
                  disabled={!!limit || sending || uploading || !text.trim()}
                >
                  <ArrowUp size={16} strokeWidth={2} aria-hidden />
                </button>
              </div>
            </form>
          )}
        </>
      )}
    </div>
  );
}

const EXAMPLES = [
  { text: "Take me to my newest batch", sub: "opens it" },
  { text: "What is on this screen?", sub: "reads it and explains" },
  { text: "Why did my last run fail?", sub: "reads the run and explains" },
];
