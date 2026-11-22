"use client";
// The Assistant drawer (plan 18 §22.1, §22.6; docs/mock/Assistant.dc.html): over the right edge of every screen of
// the shell, from the dock item or ⌘J (Ctrl J); on a phone a bottom sheet that drops to one line while actions run.
// It shows the chat (your messages, its words, the action lines with Undo, the cards), runs a turn's actions in the
// tab that sent the message (others show it read-only with Continue here) and posts their results. Every text is
// rendered as text: React escapes it, nothing is parsed as HTML.
import { useCallback, useEffect, useRef, useState, useSyncExternalStore } from "react";
import { useRouter } from "next/navigation";
import {
  ArrowRight,
  ArrowUp,
  Brain,
  Check,
  ChevronDown,
  ChevronUp,
  CircleAlert,
  CircleMinus,
  Clock3,
  Cpu,
  Eye,
  Globe,
  LoaderCircle,
  Monitor,
  Redo2,
  Sparkles,
  SquarePen,
  Undo2,
  X,
} from "lucide-react";
import { api, message, ok, type S } from "@/lib/api";
import {
  MEMORY_TABS,
  ago,
  applyEvent,
  asked,
  elsewhere,
  limitOf,
  mentionParts,
  newClientId,
  sourceText,
  toResult,
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
import { Registry, awaitIt, navigateTo } from "@/components/assistant/tools";
import { TextArea } from "@/components/expand-text";

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

const ackKey = (p: string) => `helm.assistant.remote.${p}`;
function readAck(p: string) {
  try {
    return localStorage.getItem(ackKey(p)) === "1";
  } catch {
    return false;
  }
}

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
  const [cid] = useState(newClientId);
  const [status, setStatus] = useState<S<"AssistantStatus"> | null>(null);
  const [view, setView] = useState<ThreadView | null>(null);
  const [open, setOpenState] = useState(false);
  const [shrunk, setShrunk] = useState(false);
  const [local, setLocalState] = useState<Record<string, Local>>({});
  const [undone, setUndone] = useState<Record<string, boolean>>({});
  const [asking, setAsking] = useState<Asking | null>(null);
  const [running, setRunning] = useState<{ n: number; of: number; text: string } | null>(null);
  const [error, setError] = useState("");
  const [, setAckTick] = useState(0);
  const undoFns = useRef<Record<string, NonNullable<Outcome["undo"]>>>({});
  const answerRef = useRef<((a: "yes" | "no" | "gone") => void) | null>(null);
  const handled = useRef(new Set<string>());
  const stop = useRef(false);
  const threadId = view?.thread.id;

  useEffect(() => {
    reg.configure(me?.roles ?? [], {
      navigate: (a) =>
        navigateTo(reg, a as Parameters<typeof navigateTo>[1], (url) => router.push(url)),
      await: (a, { progress }) => awaitIt(a as { what: string; id: string }, progress),
    });
  }, [reg, me, router]);

  const setOpen = useCallback((o: boolean | ((x: boolean) => boolean)) => {
    setOpenState(o);
    setShrunk(false);
  }, []);
  const setLocal = (id: string, l: Local) => setLocalState((m) => ({ ...m, [id]: l }));

  // the status and the newest chat, once the user is known
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
        if (!stop) setView({ thread: d, entries: d.entries });
      })
      .catch(() => undefined); // no assistant: no dock item
    return () => {
      stop = true;
    };
  }, [me]);

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
              thread: r.thread,
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
    (id: string, a: Asked, key: number | null) =>
      new Promise<Outcome>((resolve) => {
        setOpenState(true);
        setShrunk(false);
        setAsking({ id, card: a.card });
        setLocal(id, { status: "waiting", text: waitingText(a.card) });
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

  /** The parked turn's actions, in order, in this tab; then their results (with the new screen after a navigate,
   * whose later actions are dropped). */
  const runBatch = useCallback(
    async (t: Thread, p: S<"AssistantPending">) => {
      stop.current = false;
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
        const first = `${words(a.name)}…`;
        setRunning({ n: i + 1, of: p.actions.length, text: first });
        setLocal(a.id, { status: "running", text: first });
        const progress = (text: string) => {
          setLocal(a.id, { status: "running", text });
          setRunning({ n: i + 1, of: p.actions.length, text });
        };
        const { out, key } = await reg.run(a as Action, progress);
        const o = asked(out) ? await ask(a.id, out, key) : out;
        if (o.undo) undoFns.current[a.id] = o.undo;
        setLocal(a.id, {
          status: o.status === "still_running" ? "running" : (o.status as Line["status"]),
          text: o.text,
        });
        results.push(toResult(a.id, o));
        if (a.name === "navigate" && o.status === "done") moved = true;
      }
      setRunning(null);
      await post(t.id, {
        kind: "results",
        client_id: cid,
        turn: p.turn,
        results,
        ...(moved && { screen: reg.summary() }),
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

  const send = useCallback(
    async (text: string) => {
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
      return post(id, { kind: "message", client_id: cid, text: t, screen: reg.summary() });
    },
    [cid, post, reg, view?.thread.id],
  );

  const newChat = useCallback(async () => {
    try {
      const n = await ok(api.POST("/v1/studio/assistant/threads", { body: { client_id: cid } }));
      answerRef.current?.("gone");
      stop.current = true;
      setView({ thread: n, entries: [] });
      setLocalState({});
      setError("");
    } catch (e) {
      setError(message(e));
    }
  }, [cid]);

  const answerCard = (cardId: string, yes: boolean) =>
    threadId &&
    void post(threadId, {
      kind: "answer",
      client_id: cid,
      card_id: cardId,
      answer: yes ? "yes" : "not_now",
    });
  const continueHere = () => threadId && void post(threadId, { kind: "continue", client_id: cid });
  const stopAll = () => {
    stop.current = true;
    answerRef.current?.("no");
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
        if (!f) return;
        await (was ? f.redo() : f.undo());
      }
      setUndone((u) => ({ ...u, [l.id]: !was }));
    } catch (e) {
      setError(message(e));
    }
  };
  const ackRemote = () => {
    try {
      localStorage.setItem(ackKey(status?.provider?.title ?? ""), "1");
    } catch {
      /* the notice shows again next time */
    }
    setAckTick((n) => n + 1);
  };

  const enabled = !!status?.enabled;
  // ⌘J (Ctrl J) opens and closes it on any screen
  useEffect(() => {
    if (!enabled) return;
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && !e.altKey && !e.shiftKey && e.key.toLowerCase() === "j") {
        e.preventDefault();
        setOpen((o) => !o);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [enabled, setOpen]);

  const readOnly = elsewhere(view?.thread, cid);
  const serverCardWaits =
    !readOnly && !!view?.entries.some((e) => e.kind === "card" && e.card?.state === "waiting");
  return {
    reg,
    cid,
    status,
    enabled,
    view,
    open,
    setOpen,
    shrunk,
    setShrunk,
    local,
    undone,
    canUndo: (l: Line) => !!l.memory_id || !!undoFns.current[l.id],
    asking,
    answerAsk: (yes: boolean) => answerRef.current?.(yes ? "yes" : "no"),
    running,
    error,
    send,
    newChat,
    answerCard,
    continueHere,
    stopAll,
    toggleUndo,
    readOnly,
    cardWaits: !!asking || serverCardWaits,
    remoteAcked: status?.provider ? readAck(status.provider.title) : true,
    ackRemote,
  };
}
export type AssistantApi = ReturnType<typeof useAssistant>;

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

function Lines({ lines, a }: { lines: Line[]; a: AssistantApi }) {
  return (
    <ul className="al" aria-label="What it did">
      {lines.map((l) => {
        const off = !!a.undone[l.id];
        const status = off ? "undone" : l.status;
        return (
          <li key={l.id}>
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
              <Text text={l.text} />
            </span>
            {l.undo && (l.status === "done" || off) && a.canUndo(l) && !(off && l.memory_id) && (
              <button type="button" className="un" onClick={() => void a.toggleUndo(l)}>
                {off ? <Redo2 size={12} aria-hidden /> : <Undo2 size={12} aria-hidden />}
                {off ? "Redo" : "Undo"}
              </button>
            )}
          </li>
        );
      })}
    </ul>
  );
}

const GO: Record<S<"AssistantCard">["kind"], string> = {
  look: "LOOK",
  web_search: "SEARCH",
  delete: "DELETE",
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
}: {
  card: ScreenCard;
  waiting: boolean;
  busy?: boolean;
  onYes: () => void;
  onNo: () => void;
  after?: string;
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
            <img key={i} src={u} alt="" />
          ))}
        </div>
      )}
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
        : "Runs on your machines.",
  danger: c.kind === "delete",
});

/** The chat as the board lays it out: your messages, its words with their lines under them, cards, notices. */
function Thread({ a, entries }: { a: AssistantApi; entries: Entry[] }) {
  const known = new Set(entries.map((e) => e.id));
  const out: React.ReactNode[] = [];
  let words: Entry[] = [];
  let lines: Line[] = [];
  const flush = (key: string) => {
    if (!words.length && !lines.length) return;
    out.push(
      <div key={`b${key}`} className="msg">
        {words.map((w) => (
          <p key={w.id}>
            <Text text={w.text ?? ""} />
          </p>
        ))}
        {lines.length > 0 && <Lines lines={lines} a={a} />}
        {lines
          .filter((l) => l.sources?.length)
          .map((l) => (
            <ol key={`s${l.id}`} className="src">
              {l.sources!.map((s) => (
                <li key={s.url}>{sourceText(s)}</li>
              ))}
            </ol>
          ))}
      </div>,
    );
    words = [];
    lines = [];
  };
  for (const e of entries) {
    if (e.kind === "assistant") words.push(e);
    else if (e.kind === "line" && e.line) lines.push(e.line);
    else {
      flush(e.id);
      if (e.kind === "user")
        out.push(
          <div key={e.id} className="msg me">
            <Text text={e.text ?? ""} />
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
            onYes={() => a.answerCard(c.id, true)}
            onNo={() => a.answerCard(c.id, false)}
          />,
        );
      } else if (e.kind === "notice" && !["cap_day", "cap_tokens"].includes(e.error?.code ?? ""))
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
  return <>{out}</>;
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
        <ul className="al m-0 bg-s2" aria-label={MEMORY_TABS.find(([k]) => k === kind)![1]}>
          {shown.map((x) =>
            editing?.id === x.id ? (
              <li key={x.id} className="min-h-10">
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
              <li key={x.id} className="min-h-10">
                <span>
                  {x.text}
                  <small className="block text-[10px] text-t2">
                    chat · {x.thread?.title || "deleted"} · {ago(x.updated_at)}
                  </small>
                </span>
                <button
                  type="button"
                  className="un"
                  onClick={() => setEditing({ id: x.id, text: x.text })}
                >
                  Edit
                </button>
                <button
                  type="button"
                  className="un"
                  disabled={busy}
                  onClick={() => void remove(x.id)}
                >
                  Delete
                </button>
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

/** The drawer (or, on a phone, the bottom sheet). */
export function AssistantDrawer({ a }: { a: AssistantApi }) {
  const [text, setText] = useState("");
  const [sending, setSending] = useState(false);
  const [top, setTop] = useState<number | null>(null);
  const [memory, setMemory] = useState(false);
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
  useEffect(() => {
    end.current?.scrollIntoView({ block: "nearest" });
  }, [count]);
  useEffect(() => {
    if (a.open) input.current?.focus();
  }, [a.open]);
  if (!a.open) return null;
  const limit = limitOf(entries);
  const st = a.status;
  const t = a.view?.thread;
  const chip = a.readOnly ? (
    <span className="st ro-chip">Read-only</span>
  ) : a.cardWaits ? (
    <span className="st st-ask">Waiting for you</span>
  ) : a.running || t?.state === "working" || t?.state === "waiting" ? (
    <span className="st st-go">Working</span>
  ) : null;
  async function submit(e?: React.FormEvent) {
    e?.preventDefault();
    if (!text.trim() || sending) return;
    setSending(true);
    if (await a.send(text)) setText("");
    setSending(false);
  }
  const sendExample = async (x: string) => {
    setSending(true);
    await a.send(x);
    setSending(false);
  };
  const remote = st?.provider?.remote;
  const where = st?.provider
    ? remote
      ? `${st.provider.title} · your messages leave this machine`
      : "Runs on this machine"
    : "";

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
            <button type="button" className="no h-8 px-3 text-xs" onClick={a.stopAll}>
              Stop
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
      className={`ast ${phone ? "sheet" : "drw"}`}
      role="complementary"
      aria-label="Assistant"
      style={phone && top !== null ? { top } : undefined}
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
            <h2>
              <Sparkles aria-hidden strokeWidth={2} />
              Assistant
            </h2>
            {chip}
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
              type="button"
              className="ib"
              aria-label="New chat"
              title="New chat"
              onClick={() => void a.newChat()}
            >
              <SquarePen size={15} strokeWidth={1.75} aria-hidden />
            </button>
            {phone && (
              <button
                type="button"
                className="ib"
                aria-label="Shrink"
                onClick={() => a.setShrunk(true)}
              >
                <ChevronDown size={15} strokeWidth={2} aria-hidden />
              </button>
            )}
            <button
              type="button"
              className="x"
              aria-label={`Close (${shortcut()})`}
              title={`Close (${shortcut()})`}
              onClick={() => a.setOpen(false)}
            >
              <X size={16} strokeWidth={1.75} aria-hidden />
            </button>
          </div>
          {!phone && sees.length > 0 && (
            <div className="where">
              Sees
              {sees.map((s) => (
                <span key={s}>{s}</span>
              ))}
            </div>
          )}
          {remote && !a.remoteAcked ? (
            <div role="note" className="bar2 remote">
              <span className="flex items-center gap-2 text-[13px] font-semibold text-[#f2c14e]">
                <Globe size={16} strokeWidth={1.75} aria-hidden />
                Remote provider: your messages leave this machine ({st!.provider!.title})
              </span>
              <span>
                Sent: what you type, a short summary of this screen, and what it looks up.
              </span>
              <span>Never sent: images, video, audio or files.</span>
              <span className="flex justify-end">
                <button type="button" className="no h-8 text-xs" onClick={a.ackRemote}>
                  OK
                </button>
              </span>
            </div>
          ) : (
            where && (
              <div className={`rem ${remote ? "" : "local"}`}>
                <Globe size={13} strokeWidth={1.75} aria-hidden />
                {where}
              </div>
            )
          )}

          <div className={`thr ${a.readOnly ? "ro" : ""}`} aria-live="polite">
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
          {!a.readOnly && (
            <form className={`cmp ${limit ? "opacity-50" : ""}`} onSubmit={(e) => void submit(e)}>
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
                placeholder={limit ? limit.until : "Ask, or say what to do"}
                value={text}
                onChange={(e) => setText(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
                    e.preventDefault();
                    void submit();
                  }
                }}
              />
              <div className="cmpb">
                <small>
                  {!phone && (
                    <>
                      <span className="kbd">{shortcut()}</span> open or close
                      {screen?.screen === "create" && (
                        <>
                          {" · "}
                          <span className="kbd">@</span> elements
                        </>
                      )}
                    </>
                  )}
                </small>
                <button
                  type="submit"
                  className="snd"
                  aria-label="Send"
                  disabled={!!limit || sending || !text.trim()}
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
