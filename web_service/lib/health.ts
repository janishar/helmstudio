// Studio isn't answering (docs/mock/ErrorStates.dc.html a1-a3): one page-level state for 502/503/504 and network
// failures. A failed call asks one quick health check; only when that fails too is the studio down (a single blip
// shows nothing). Down, it checks again after 2, 4, 8, 15, 30 s, then every 30 s, paused while the tab is hidden;
// the first good check (or any good answer) is "Back online" for 3 s and tells the pages to read again.
import { useSyncExternalStore } from "react";

export type Health = {
  down: boolean;
  /** "Back online": the 3 s after the studio came back */
  back: boolean;
  /** when the next check runs (ms); null when not down */
  nextAt: number | null;
};

/** The statuses that mean the studio (or Kong, or the web server's proxy) isn't there; a 4xx keeps its own detail. */
export const OUTAGE = new Set([502, 503, 504]);
const BACKOFF = [2, 4, 8, 15, 30];
/** The quick check: the hosts list (cheap, every page's status tile reads it). */
const PROBE = "/api/v1/studio/hosts";

let state: Health = { down: false, back: false, nextAt: null };
const subs = new Set<() => void>();
const backs = new Set<() => void>();
let waiters: (() => void)[] = [];
let armed = 0;
let checking = false;
let step = 0;
let paused = false;
let timer: ReturnType<typeof setTimeout> | undefined;
let backTimer: ReturnType<typeof setTimeout> | undefined;

function set(next: Partial<Health>) {
  state = { ...state, ...next };
  subs.forEach((f) => f());
}
const hidden = () => typeof document !== "undefined" && document.hidden;

/** One check: true when the studio answers (any status but 502/503/504). */
async function probe(): Promise<boolean> {
  try {
    const r = await fetch(PROBE, { cache: "no-store" });
    return !OUTAGE.has(r.status);
  } catch {
    return false;
  }
}

async function check() {
  if (checking) return;
  checking = true;
  clearTimeout(timer);
  const up = await probe();
  checking = false;
  if (up) return recovered();
  set({ down: true, back: false });
  const s = BACKOFF[Math.min(step++, BACKOFF.length - 1)] * 1000;
  set({ nextAt: Date.now() + s });
  timer = setTimeout(() => {
    if (hidden()) paused = true;
    else void check();
  }, s);
}

function recovered() {
  const was = state.down;
  clearTimeout(timer);
  step = 0;
  paused = false;
  const w = waiters;
  waiters = [];
  w.forEach((f) => f());
  if (!was) return;
  set({ down: false, back: true, nextAt: null });
  clearTimeout(backTimer);
  backTimer = setTimeout(() => set({ back: false }), 3000);
  backs.forEach((f) => f());
}

const onVisible = () => {
  if (!hidden() && paused) {
    paused = false;
    void check();
  }
};

/** Turns the banner's checks on while a Banner is mounted (the shell's): without one, nothing is reported (tests that
 * render a page alone never start a check). Returns the off switch. */
export function arm(): () => void {
  if (armed++ === 0 && typeof document !== "undefined")
    document.addEventListener("visibilitychange", onVisible);
  return () => {
    if (--armed === 0) {
      if (typeof document !== "undefined")
        document.removeEventListener("visibilitychange", onVisible);
      clearTimeout(timer);
      clearTimeout(backTimer);
      state = { down: false, back: false, nextAt: null };
      step = 0;
      paused = false;
      waiters.forEach((f) => f());
      waiters = [];
    }
  };
}

/** A call failed with 502/503/504 or reached nothing: one check now (none while one is running or already down). */
export function reportOutage() {
  if (armed && !state.down) void check();
}

/** A call answered: the studio is back (no wait for the next check). */
export function reportAnswer() {
  if (state.down) recovered();
}

/** Retry now: checks at once and starts the back-off again. */
export function retryNow() {
  if (!armed) return;
  step = 0;
  void check();
}

/** The studio is down now. */
export const isDown = () => state.down;

/** Calls `f` each time the studio comes back (every page reads what it shows again). Returns the unsubscribe. */
export function onBack(f: () => void): () => void {
  backs.add(f);
  return () => void backs.delete(f);
}

/** Resolves once the studio answers: at once when it is not down and no check is running. */
export function whenUp(): Promise<void> {
  if (!state.down && !checking) return Promise.resolve();
  return new Promise((r) => waiters.push(r));
}

/** The caught value is the studio not answering: an ApiError of 502/503/504 or a network failure. */
export function isOutage(e: unknown): boolean {
  if (e instanceof TypeError) return true;
  const s = (e as { status?: unknown } | null)?.status;
  return typeof s === "number" && OUTAGE.has(s);
}

/** What a call that hit an outage says (lib/api.ts errorText and message); a page's ErrorNote with it is held back
 * while the banner shows (one message, not five). */
export const OUTAGE_TEXT = "The studio isn’t answering.";
/** Under a greyed key while the banner shows ("Generate · Studio isn't answering"). */
export const DOWN_KEY = "Studio isn’t answering";

const subscribe = (f: () => void) => {
  subs.add(f);
  return () => subs.delete(f);
};
const SERVER: Health = { down: false, back: false, nextAt: null };
/** The studio's state, for a component. */
export const useHealth = () =>
  useSyncExternalStore(
    subscribe,
    () => state,
    () => SERVER,
  );

// ---- the status tile (board g1, g2): the same words as the Mac menu bar

type Host = {
  host: string;
  env: string;
  state: "idle" | "busy" | "offline";
  updated_at: string;
  device?: { kind: string; name: string };
};
export type StatusRow = {
  key: string;
  /** "Studio", "mlx runner", "NVIDIA" */
  name: string;
  /** "mac-studio", "gcp · RTX PRO 6000" */
  sub: string;
  dot: "ok" | "bad" | "off";
  text: string;
  host?: string;
  env?: string;
};
/** A host off longer than this no longer raises the tile (an NVIDIA VM you turned off is not an alarm). */
export const STALE_MS = 60 * 60 * 1000;
/** The tile's words. */
export type TileWord = "Studio down" | "No runner" | "Runner down";

const hhmm = (iso: string) =>
  new Date(iso).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", hour12: false });

/** The popover's rows (Studio, each runner per host, each NVIDIA host as one) and the tile's word: Studio down while
 * the banner shows, No runner when none is up, Runner down when one stopped in the last hour; null when all is well
 * (or the hosts are not read yet). */
export function statusOf(
  hosts: Host[] | null,
  down: boolean,
  now: number,
): { tile: TileWord | null; rows: StatusRow[] } {
  const rows: StatusRow[] = [
    {
      key: "studio",
      name: "Studio",
      sub: "",
      dot: down ? "bad" : "ok",
      text: down ? "Not answering" : "Answering",
    },
  ];
  const runners = (hosts ?? []).filter((h) => h.env !== "server");
  const by = new Map<string, Host[]>();
  for (const h of runners) by.set(h.host, [...(by.get(h.host) ?? []), h]);
  let recent = false;
  for (const host of [...by.keys()].sort()) {
    const envs = by.get(host)!.toSorted((a, b) => a.env.localeCompare(b.env));
    const fresh = (h: Host) => now - Date.parse(h.updated_at) < STALE_MS;
    const gpu = envs.find((e) => e.device?.kind === "cuda");
    if (gpu) {
      const live = envs.some((e) => e.state !== "offline");
      const last = envs.map((e) => e.updated_at).reduce((a, b) => (a > b ? a : b));
      if (!live && envs.some(fresh)) recent = true;
      rows.push({
        key: host,
        name: "NVIDIA",
        sub: `${host} · ${gpu.device!.name.replace(/^NVIDIA /, "").replace(/ (Blackwell|Hopper|Ada|Ampere)\b.*$/, "")}`,
        dot: live ? "ok" : envs.some(fresh) ? "bad" : "off",
        text: live ? "Ready" : `Offline since ${hhmm(last)}`,
        host,
      });
      continue;
    }
    for (const e of envs) {
      const off = e.state === "offline";
      if (off && fresh(e)) recent = true;
      rows.push({
        key: `${host}.${e.env}`,
        name: `${e.env} runner`,
        sub: host,
        dot: !off ? "ok" : fresh(e) ? "bad" : "off",
        text: !off
          ? e.state === "busy"
            ? "Running"
            : "Ready"
          : fresh(e)
            ? "Stopped"
            : `Offline since ${hhmm(e.updated_at)}`,
        host,
        env: e.env,
      });
    }
  }
  const tile: TileWord | null = down
    ? "Studio down"
    : !hosts
      ? null
      : !runners.some((h) => h.state !== "offline")
        ? "No runner"
        : recent
          ? "Runner down"
          : null;
  return { tile, rows };
}
