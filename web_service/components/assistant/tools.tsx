"use client";
// The screen-action registry (plan 18 §22.3, §22.5): a context in the app shell next to UserContext. Each screen
// registers its actions and its summary once it has loaded (useScreenTools), always calling its own entry points; the
// shell adds navigate and await. The drawer runs a turn's actions through it, in the tab that sent the message.
import { createContext, use, useEffect, useRef } from "react";
import { api, message, ok } from "@/lib/api";
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
};

type Entry = { key: number; get: () => ScreenSpec };

/** The registry: screens in mount order (the newest is the screen now), the path, and listeners. */
export class Registry {
  private entries: Entry[] = [];
  private subs = new Set<() => void>();
  private seq = 0;
  path = "";
  roles: string[] = [];
  shell: Record<string, Handler> = {};

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
  setPath(p: string) {
    this.path = p;
    this.notify();
  }
  /** The screen now: the newest registration whose routes hold the path. */
  current(): { key: number; spec: ScreenSpec } | null {
    for (let i = this.entries.length - 1; i >= 0; i--) {
      const spec = this.entries[i].get();
      if (onRoute(SCREENS[spec.screen]?.routes ?? [], this.path))
        return { key: this.entries[i].key, spec };
    }
    return null;
  }
  alive = (key: number) => this.entries.some((e) => e.key === key);

  /** The summary sent with a message (§22.4): route, open ids, roles, as of now, hidden or not, the actions. */
  summary(): Screen {
    const c = this.current()?.spec;
    let fields: Record<string, unknown> = {};
    try {
      fields = c ? { screen: c.screen, ...c.summary() } : { screen: screenLabel(this.path) };
    } catch (e) {
      fields = { screen: c?.screen, error: message(e) };
    }
    const route = typeof location === "undefined" ? this.path : location.pathname + location.search;
    return fitScreen({
      route: route.slice(0, 200) || "/",
      ids: Object.fromEntries(
        Object.entries(c?.ids ?? {})
          .filter(([, v]) => !!v)
          .slice(0, 16)
          .map(([k, v]) => [k, v.slice(0, 80)]),
      ),
      roles: this.roles.slice(0, 8),
      as_of: new Date().toISOString(),
      hidden: typeof document !== "undefined" && document.hidden,
      actions: [...new Set([...Object.keys(this.shell), ...Object.keys(c?.actions ?? {})])],
      fields: fields as Screen["fields"],
    });
  }

  /** Runs one action: the shell's or the screen's, its arguments checked first. `key` is the screen that owns it
   * (a card of it is cancelled once that screen is gone). */
  async run(
    a: Action,
    progress: (text: string) => void,
  ): Promise<{ out: Outcome | Asked; key: number | null }> {
    const c = this.current();
    const shellOwn = a.name in this.shell;
    const h = shellOwn ? this.shell[a.name] : c?.spec.actions[a.name];
    if (!h) return { out: failed(`${a.name} · not on this screen`), key: null };
    const why = argsError(shellOwn ? "shell" : (c?.spec.screen ?? ""), a.name, a.args);
    if (why) return { out: failed(`${a.name} · ${why}`), key: null };
    try {
      const out = await h(a.args, { progress });
      return { out, key: shellOwn || !asked(out) ? null : (c?.key ?? null) };
    } catch (e) {
      return { out: failed(`${a.name} · ${message(e)}`), key: null };
    }
  }
}

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
