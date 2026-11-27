"use client";
// The screen-action registry (plan 18 §22.3, §22.5): a context in the app shell next to UserContext. Each screen
// registers its actions and its summary once it has loaded (useScreenTools), always calling its own entry points; the
// shell adds navigate and await. The drawer runs a turn's actions through it, in the tab that sent the message.
import { createContext, use, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { api, message, ok, okOr, type S } from "@/lib/api";
import {
  SCREENS,
  argsError,
  asked,
  done,
  failed,
  fitScreen,
  onRoute,
  ordinal,
  routeUrl,
  screenLabel,
  type Action,
  type Asked,
  type Handler,
  type Outcome,
  type Screen,
} from "@/lib/assistant";
import { clock } from "@/lib/runs";

/** What a screen registers: its actions (names from tools/actions.json), the "Sees" chips, the open ids, the
 * summary's fields (§22.4, built from values the screen already works out) and the empty drawer's examples. */
export type ScreenSpec = {
  screen: keyof typeof SCREENS;
  sees: string[];
  ids?: Record<string, string>;
  summary: () => Record<string, unknown>;
  actions: Record<string, Handler>;
  examples?: { text: string; sub: string }[];
  /** the Assistant's runs still going on this screen (Magic: its runs on the board) and how to stop them: its Stop
   * offers "Stop its runs too" */
  runs?: () => Promise<{ going: number; stop: () => void }>;
};

type Entry = { key: number; get: () => ScreenSpec };
type AssistantCard = S<"AssistantCard">;
export type ServerCards = {
  cards: AssistantCard[];
  key: string;
  answer: (cardId: string, yes: boolean) => void;
};

/** The registry: screens in mount order (the newest is the screen now), the path, and listeners. */
export class Registry {
  private entries: Entry[] = [];
  private subs = new Set<() => void>();
  private seq = 0;
  path = "";
  roles: string[] = [];
  shell: Record<string, Handler> = {};
  /** API 1.87: the chat's server cards that wait (the Magic page draws a board_edit card on its board), a key that
   * moves with every change of the chat (a screen reads its data again) and how to answer a card */
  server: ServerCards = { cards: [], key: "", answer: () => undefined };

  register(get: () => ScreenSpec) {
    const key = ++this.seq;
    this.entries.push({ key, get });
    this.notify();
    return () => {
      this.entries = this.entries.filter((e) => e.key !== key);
      this.notify();
    };
  }
  subscribe(fn: () => void) {
    this.subs.add(fn);
    return () => void this.subs.delete(fn);
  }
  notify() {
    this.subs.forEach((f) => f());
  }
  /** The user's roles and the shell's actions (navigate, await). */
  configure(roles: string[], shell: Record<string, Handler>) {
    this.roles = roles;
    this.shell = shell;
  }
  /** The drawer's chat (API 1.87): its waiting server cards and its key; listeners hear only a change. */
  setServer(cards: AssistantCard[], key: string, answer: ServerCards["answer"]) {
    const same =
      key === this.server.key &&
      cards.map((c) => c.id).join() === this.server.cards.map((c) => c.id).join();
    this.server = { cards, key, answer };
    if (!same) this.notify();
  }
  setPath(p: string) {
    this.path = p;
    this.notify();
  }
  /** The screens on the path, newest first: a screen inside another on the same route (Graph in Create, the Studio
   * view in the pipeline editor) is on top, the one under it still answers its own actions. */
  private stack(): { key: number; spec: ScreenSpec }[] {
    const out = [];
    for (let i = this.entries.length - 1; i >= 0; i--) {
      const spec = this.entries[i].get();
      if (onRoute(SCREENS[spec.screen]?.routes ?? [], this.path))
        out.push({ key: this.entries[i].key, spec });
    }
    return out;
  }
  /** The screen now: the newest registration whose routes hold the path. */
  current(): { key: number; spec: ScreenSpec } | null {
    return this.stack()[0] ?? null;
  }
  alive = (key: number) => this.entries.some((e) => e.key === key);

  /** The summary sent with a message (§22.4): route, open ids, roles, as of now, hidden or not, the actions. */
  summary(): Screen {
    const all = this.stack();
    const c = all[0]?.spec;
    let fields: Record<string, unknown> = {};
    try {
      fields = c
        ? Object.assign({}, ...[...all].reverse().map((x) => x.spec.summary()), {
            screen: c.screen,
          })
        : { screen: screenLabel(this.path) };
    } catch (e) {
      fields = { screen: c?.screen, error: message(e) };
    }
    const route = typeof location === "undefined" ? this.path : location.pathname + location.search;
    return fitScreen({
      route: route.slice(0, 200) || "/",
      ids: Object.fromEntries(
        Object.entries(Object.assign({}, ...[...all].reverse().map((x) => x.spec.ids ?? {})))
          .filter(([, v]) => !!v)
          .slice(0, 16)
          .map(([k, v]) => [k, String(v).slice(0, 80)]),
      ),
      roles: this.roles.slice(0, 8),
      as_of: new Date().toISOString(),
      hidden: typeof document !== "undefined" && document.hidden,
      actions: [
        ...new Set([
          ...Object.keys(this.shell),
          ...all.flatMap((x) => Object.keys(x.spec.actions)),
        ]),
      ],
      fields: fields as Screen["fields"],
    });
  }

  /** Runs one action: the shell's or the screen's, its arguments checked first. `key` is the screen that owns it
   * (a card of it is cancelled once that screen is gone). */
  async run(
    a: Action,
    progress: (text: string) => void,
  ): Promise<{ out: Outcome | Asked; key: number | null }> {
    const shellOwn = a.name in this.shell;
    const c = shellOwn ? null : this.stack().find((x) => a.name in x.spec.actions);
    const h = shellOwn ? this.shell[a.name] : c?.spec.actions[a.name];
    if (!h) return { out: failed(`${a.name} · not on this screen`), key: null };
    const why = argsError(shellOwn ? "shell" : (c?.spec.screen ?? ""), a.name, a.args);
    if (why) return { out: failed(`${a.name} · ${why}`), key: null };
    try {
      const out = await h(a.args, { progress, auto: !!a.auto });
      return { out, key: shellOwn || !asked(out) ? null : (c?.key ?? null) };
    } catch (e) {
      return { out: failed(`${a.name} · ${message(e)}`), key: null };
    }
  }
}

/** An Auto generation's Undo (API 1.68): its runs still going stop, the takes they made are deleted. */
export async function undoRuns(ids: string[], wait = 1000) {
  for (const id of ids) {
    const path = { params: { path: { run_id: id } } };
    // cancel is async and keeps what is already stored (409: it already ended); wait for the end, then delete all
    await okOr(api.POST("/v1/studio/runs/{run_id}/cancel", path), 409);
    let r = await ok(api.GET("/v1/studio/runs/{run_id}", path));
    while (r.status === "queued" || r.status === "running") {
      await new Promise((go) => setTimeout(go, wait));
      r = await ok(api.GET("/v1/studio/runs/{run_id}", path));
    }
    for (const o of r.outputs)
      if (o.take_id)
        await api.DELETE("/v1/studio/takes/{take_id}", {
          params: { path: { take_id: o.take_id } },
        });
  }
}

/** An Auto library save's Undo (API 1.68): the new item is deleted. */
export const undoItem = (id: string) =>
  ok(api.DELETE("/v1/studio/library/{item_id}", { params: { path: { item_id: id } } }));

export const ToolsContext = createContext<Registry | null>(null);
export const useRegistry = () => use(ToolsContext);

/** Registers the screen's actions while it is mounted; `spec` is read fresh at every use. */
export function useScreenTools(spec: ScreenSpec) {
  const reg = use(ToolsContext);
  const ref = useRef(spec);
  useEffect(() => {
    ref.current = spec;
  });
  useEffect(() => {
    if (!reg) return;
    const off = reg.register(() => ref.current);
    return () => off();
  }, [reg]);
}

/** The assistant's edits of a document (Graph, the pipeline editor; plan 18 §22.5): each is an edit of the screen's
 * own history made on the freshest document (several in one batch run before a render), the document before the
 * first one kept for one "Undo assistant changes", and the nodes it added (ringed on the canvas). The screen calls
 * `clear` on a hand edit or a reload: from then on ⌘Z is the undo. */
export function useAssistantChanges<D extends { nodes: object }>(
  latestRef: React.RefObject<D>,
  apply: (d: D) => void,
) {
  const base = useRef<D | null>(null);
  const [state, setState] = useState<{ base: D | null; added: string[] }>({
    base: null,
    added: [],
  });
  const clear = useCallback(() => {
    base.current = null;
    setState((s) => (s.base ? { base: null, added: [] } : s));
  }, []);
  return {
    changed: !!state.base,
    added: state.added,
    clear,
    /** `f` on the freshest document; the result (unchanged when `f` changed nothing) */
    edit(f: (d: D) => D): D {
      const before = latestRef.current;
      const next = f(before);
      if (next === before) return next;
      base.current ??= before;
      const fresh = Object.keys(next.nodes).filter((k) => !(k in before.nodes));
      const b = base.current;
      setState((s) => ({ base: b, added: [...s.added, ...fresh] }));
      latestRef.current = next;
      apply(next);
      return next;
    },
    /** "Undo assistant changes": back to the document before its first edit, as one more step of the history */
    undo() {
      const b = base.current;
      if (!b) return;
      latestRef.current = b;
      apply(b);
      clear();
    },
  };
}

/** A dialog of the screen the assistant opened prefilled (plan 18 §22.5, "dialog"): `wait` resolves once it closes,
 * with what its Save made (the last one), else null: cancelled. A screen that unmounts cancels it. */
export function useDialogWait<T>() {
  const cur = useRef<{ saved: T | null; done: (v: T | null) => void } | null>(null);
  useEffect(() => () => cur.current?.done(null), []);
  return useMemo(
    () => ({
      wait: () =>
        new Promise<T | null>((done) => {
          cur.current?.done(null);
          cur.current = { saved: null, done };
        }),
      saved: (v: T) => {
        if (cur.current) cur.current.saved = v;
      },
      closed: () => {
        const c = cur.current;
        cur.current = null;
        c?.done(c.saved);
      },
    }),
    [],
  );
}

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));
/** Resolves once `cond` holds (checked every 100 ms), false after `ms`. */
export async function until(cond: () => boolean, ms: number) {
  for (const end = Date.now() + ms; Date.now() < end; await sleep(100)) if (cond()) return true;
  return cond();
}
const visible = () =>
  new Promise<void>((r) => {
    const on = () => {
      if (document.hidden) return;
      document.removeEventListener("visibilitychange", on);
      r();
    };
    document.addEventListener("visibilitychange", on);
  });

/** await's cap (§22.3): 10 minutes of the tab shown, then "still running". */
export const AWAIT_CAP_MS = 10 * 60_000;
export const POLL_MS = 2000;

type Ended = { end: "done" | "failed"; text: string; result?: Record<string, unknown> };
type Waiting = { text: string };
/** One read of what await waits on. */
type Probe = () => Promise<Ended | Waiting>;

const what = {
  run:
    (id: string): Probe =>
    async () => {
      const r = await ok(api.GET("/v1/studio/runs/{run_id}", { params: { path: { run_id: id } } }));
      const n = r.outputs.length;
      // an ask run (look_at) answers in text, never a take: the answer comes back for the model to read
      if (r.status === "succeeded" && r.answer) {
        const a = r.answer.text.trim();
        return {
          end: "done",
          text: `Answered · ${a.length > 120 ? `${a.slice(0, 120)}…` : a}`,
          result: { status: r.status, answer: a.slice(0, 4000) },
        };
      }
      if (r.status === "succeeded")
        return {
          end: "done",
          text: `Run done · ${n} take${n === 1 ? "" : "s"}`,
          result: { status: r.status, take_ids: r.outputs.flatMap((o) => o.take_id ?? []) },
        };
      if (r.status === "failed" || r.status === "cancelled")
        return {
          end: "failed",
          text: r.status === "cancelled" ? "Run stopped" : `Run failed · ${r.error ?? "no reason"}`,
          result: { status: r.status },
        };
      return {
        text: r.status === "queued" && r.position ? `${ordinal(r.position)} in line` : "running",
      };
    },
  job:
    (id: string): Probe =>
    async () => {
      const j = await ok(
        api.GET("/v1/studio/admin/model-jobs/{job_id}", { params: { path: { job_id: id } } }),
      );
      if (j.state === "succeeded")
        return { end: "done", text: `Done · ${j.title}`, result: { state: j.state } };
      if (j.state === "failed" || j.state === "cancelled")
        return { end: "failed", text: `${j.title} · ${j.state}`, result: { state: j.state } };
      return { text: j.state };
    },
  export:
    (id: string): Probe =>
    async () => {
      const j = await ok(
        api.GET("/v1/studio/media-jobs/{job_id}", { params: { path: { job_id: id } } }),
      );
      if (j.status === "succeeded")
        return {
          end: "done",
          text: "Export done",
          result: { status: j.status, take_id: j.take_id },
        };
      if (j.status === "failed" || j.status === "cancelled")
        return { end: "failed", text: `Export ${j.status}`, result: { status: j.status } };
      return { text: j.status };
    },
};

/** await (§22.3): reads what it waits on every 2 s from the tab, the line saying where it is; a hidden tab waits
 * until it shows ("Waiting for the tab"), and after 10 minutes shown it answers still_running.
 * ponytail: polls the run, job or media job; the session SSE in lib/runs.ts if a tab ever awaits many at once */
export async function awaitIt(
  args: { what: string; id: string },
  progress: (text: string) => void,
  cap = AWAIT_CAP_MS,
): Promise<Outcome> {
  const make =
    args.what === "run"
      ? what.run
      : args.what === "job" || args.what === "download"
        ? what.job
        : args.what === "export"
          ? what.export
          : null;
  if (!make) return failed(`Nothing to wait on · ${args.what} is not on this screen`);
  const probe = make(args.id);
  const label = args.what === "run" ? "run" : args.what;
  let shown = 0;
  for (;;) {
    if (document.hidden) {
      progress("Waiting for the tab · it is hidden");
      await visible();
    }
    const p = await probe().catch((e: unknown) => ({ end: "failed" as const, text: message(e) }));
    if ("end" in p) {
      const result = "result" in p ? p.result : undefined;
      return p.end === "done" ? done(p.text, { result }) : { ...failed(p.text), result };
    }
    if (shown >= cap)
      return {
        status: "still_running",
        text: "Still running · 10 min passed. Say go on when it is done",
      };
    progress(`Waiting for ${label} · ${p.text} · ${clock(shown / 1000)}`);
    await sleep(POLL_MS);
    shown += POLL_MS;
  }
}

/** navigate (§22.4): to a route of the table with its params; done once the URL is the new screen's and it had a
 * moment to register, else "screen did not load" after about 10 s. */
export async function navigateTo(
  reg: Registry,
  args: { to: string; id?: string; row?: number; query?: Record<string, string> },
  push: (url: string) => void,
): Promise<Outcome> {
  const r = routeUrl(args, reg.roles);
  if ("error" in r) return failed(`Can't go there · ${r.error}`);
  push(r.url);
  // the asked params are in the URL (Create then puts its open session there too)
  const want = [...new URLSearchParams(r.url.split("?")[1] ?? "")];
  const there = await until(() => {
    const q = new URLSearchParams(location.search);
    return (
      reg.path === r.path && location.pathname === r.path && want.every(([k, v]) => q.get(k) === v)
    );
  }, 10_000);
  if (!there) return failed("Did not open · the screen did not load");
  await sleep(150); // the new screen's effects register its actions
  const c = reg.current()?.spec;
  return done(`Opened ${c?.sees[0] ?? screenLabel(r.path)}`, { result: { route: r.url } });
}
