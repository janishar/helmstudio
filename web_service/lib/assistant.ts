// The in-app assistant (plan 18 §22; docs/mock/Assistant.dc.html): what the drawer folds from a chat's events, the
// screen actions' outcomes and cards, navigate's route table (§22.4), the screen summary's size cap, and the words
// its lines use. Everything the model wrote or a tool returned is text: the drawer renders it as text, never HTML.
import actionsJson from "@/gen/actions.json";
import type { S } from "@/lib/api";

export type Entry = S<"AssistantEntry">;
export type Thread = S<"AssistantThread">;
export type Action = S<"AssistantAction">;
export type Result = S<"AssistantActionResult">;
export type Screen = S<"AssistantScreen">;
export type ThreadView = { thread: Thread; entries: Entry[] };

/** What one screen action did: its line, and how to take it back (a line with Undo). */
export type Outcome = {
  status: Result["status"];
  text: string;
  result?: Record<string, unknown>;
  error?: string;
  undo?: { undo: () => unknown; redo: () => unknown };
};
/** A card the screen asks before its own button runs (§22.6): Yes is the user's click. */
export type ScreenCard = {
  kind: string;
  title: string;
  rows: [string, string][];
  tags: string[];
  /** the estimate line (machine, place in line, time, memory) */
  est?: string;
  /** the prompt, quoted */
  quote?: string;
  /** the takes it is about (thumb links) */
  thumbs?: string[];
  /** what leaves the machine, or what to do first */
  note?: string;
  go: string;
  fine: string;
  danger?: boolean;
  /** Yes is off, with why (a licence not ticked yet) */
  off?: string;
};
export type Asked = { card: ScreenCard; yes: () => Promise<Outcome> };
/** A screen action's handler: an outcome now, or a card whose Yes makes it. */
export type Handler = (
  args: Record<string, unknown>,
  ctx: { progress: (text: string) => void },
) => Outcome | Asked | Promise<Outcome | Asked>;

export const asked = (o: Outcome | Asked): o is Asked => "card" in o;
export const done = (text: string, more: Partial<Outcome> = {}): Outcome => ({
  status: "done",
  text,
  ...more,
});
export const failed = (text: string, error = text): Outcome => ({ status: "failed", text, error });

/** A result for the server: the line is at most 300 characters (assistant.schema.json). */
export function toResult(id: string, o: Outcome): Result {
  return {
    id,
    status: o.status,
    line: { text: o.text.slice(0, 300) || o.status, undo: !!o.undo },
    ...(o.result && { result: o.result as Result["result"] }),
    ...(o.error && { error: o.error.slice(0, 500) }),
  };
}

/** This tab's drawer (a fresh id per page load: a reloaded tab is a new tab, and Continue here moves a chat to it). */
export const newClientId = () =>
  (globalThis.crypto?.randomUUID?.() ?? `${Date.now()}${Math.random()}`).replace(
    /[^A-Za-z0-9]/g,
    "",
  );

/** A chat's events folded: an entry with a known id replaces it (a line going from running to done), a thread
 * event replaces the chat's state. */
export function applyEvent(v: ThreadView, e: S<"AssistantEvent">): ThreadView {
  if (e.type === "thread" && e.thread) return { ...v, thread: e.thread };
  if (e.type !== "entry" || !e.entry) return v;
  const en = e.entry;
  const i = v.entries.findIndex((x) => x.id === en.id);
  const entries = i < 0 ? [...v.entries, en] : v.entries.map((x, j) => (j === i ? en : x));
  return { ...v, entries };
}

/** The chat runs its actions in another tab: read-only here, with Continue here. */
export const elsewhere = (t: Thread | null | undefined, me: string) =>
  !!t && t.state !== "idle" && !!t.client_id && t.client_id !== me;

/** "1st", "2nd", "3rd", "4th" … */
export function ordinal(n: number) {
  const s = n % 100 >= 11 && n % 100 <= 13 ? "th" : (["th", "st", "nd", "rd"][n % 10] ?? "th");
  return `${n}${s}`;
}

/** Where a new run on `host` would be in line (GET /queue): 1 + the caller's queued runs there + everyone else's. */
export function placeInLine(q: S<"Queue"> | null, host: string | null | undefined): number | null {
  if (!q || !host) return null;
  const mine = q.runs.filter((r) => r.status === "queued" && r.host === host).length;
  return 1 + mine + (q.hosts.find((h) => h.host === host)?.others_queued ?? 0);
}

/** "about 1 min 20 s" from seconds. */
export function about(s: number) {
  const m = Math.floor(s / 60);
  const r = Math.round(s % 60);
  return `about ${m ? `${m} min${r ? ` ${r} s` : ""}` : `${r} s`}`;
}

/** The screen of a path: actions.json's screens (one route list each; graph shares Create's). */
type Screens = Record<
  string,
  { routes: string[]; roles?: string[]; actions: Record<string, Spec> }
>;
type Spec = { description: string; effect: string; card?: string; input: Schema };
type Schema = {
  properties?: Record<string, { enum?: unknown[]; type?: string }>;
  required?: string[];
};
export const SCREENS = (actionsJson as unknown as { screens: Screens }).screens;

const routeRe = (r: string) =>
  new RegExp(`^${r.replace(/\{[a-z]+\}/g, "[^/]+").replace(/\//g, "\\/")}$`);
/** Whether `path` (no query) is one of `routes` ("*" = any). */
export const onRoute = (routes: string[], path: string) =>
  routes.some((r) => r === "*" || routeRe(r).test(path));

/** The arguments' check against the action's input: required keys, enums, and no unknown key. */
export function argsError(screen: string, name: string, args: Record<string, unknown>) {
  const input = SCREENS[screen]?.actions[name]?.input;
  if (!input) return null;
  for (const k of input.required ?? []) if (args[k] === undefined) return `${k} is required`;
  for (const [k, v] of Object.entries(args)) {
    const p = input.properties?.[k];
    if (!p) return `${k} is not an argument`;
    if (p.enum && !p.enum.includes(v)) return `${k} must be one of ${p.enum.join(", ")}`;
  }
  return null;
}

/** navigate's targets (§22.4) and the query each one takes, with its allowed values (null: any text). */
const QUERY: Record<string, Record<string, string[] | null>> = {
  "/": { s: null, reuse: null, use: null, w: null },
  "/gallery": { batch: null },
  "/timeline": { t: null },
  "/pipelines": {},
  "/pipelines/{id}": { view: ["studio"] },
  "/pipelines/batches/{id}": {},
  "/pipelines/batches/{id}/review": {},
  "/pipelines/batches/{id}/rows/{row}": { attempt: null },
  "/models": { tab: ["loras"] },
  "/admin": { tab: ["hosts", "keys", "providers"] },
};
const LABEL: [string, string][] = [
  ["/pipelines/batches/{id}/review", "Review"],
  ["/pipelines/batches/{id}/rows/{row}", "Row"],
  ["/pipelines/batches/{id}", "Batch"],
  ["/pipelines/{id}", "Pipeline"],
  ["/pipelines", "Pipelines"],
  ["/gallery", "Gallery"],
  ["/timeline", "Timeline"],
  ["/models", "Models"],
  ["/admin", "Admin"],
  ["/", "Create"],
];
/** A screen's name for a line ("Gallery", "Batch"). */
export const screenLabel = (path: string) =>
  LABEL.find(([r]) => routeRe(r).test(path))?.[1] ?? path;

/** navigate's URL, or why it can't go there. */
export function routeUrl(
  a: { to: string; id?: string; row?: number; query?: Record<string, string> },
  roles: string[],
): { url: string; path: string } | { error: string } {
  const allowed = QUERY[a.to];
  if (!allowed) return { error: `${a.to} is not a screen` };
  if (a.to === "/admin" && !roles.includes("admin")) return { error: "Admin is for admins only" };
  if (a.to.includes("{id}") && !a.id) return { error: "id is required" };
  if (a.to.includes("{row}") && !a.row) return { error: "row is required" };
  const path = a.to
    .replace("{id}", encodeURIComponent(a.id ?? ""))
    .replace("{row}", String(a.row ?? ""));
  const q = new URLSearchParams();
  for (const [k, v] of Object.entries(a.query ?? {})) {
    if (!(k in allowed)) return { error: `${a.to} takes no ${k}` };
    const vs = allowed[k];
    if (vs && !vs.includes(v)) return { error: `${k} must be ${vs.join(", ")}` };
    q.set(k, v);
  }
  if (q.has("use") !== q.has("w")) return { error: "use and w go together" };
  const qs = q.toString();
  return { url: qs ? `${path}?${qs}` : path, path };
}

/** The screen summary is at most 16 000 characters of JSON (studio's 422 above): long texts are cut, then long
 * lists, then the fields go (the route, ids and actions stay). */
export function fitScreen(s: Screen, max = 15000): Screen {
  const size = (x: unknown) => JSON.stringify(x).length;
  if (size(s) <= max) return s;
  const cut = (x: unknown, text: number, list: number): unknown =>
    typeof x === "string"
      ? x.length > text
        ? `${x.slice(0, text)}…`
        : x
      : Array.isArray(x)
        ? x.slice(0, list).map((y) => cut(y, text, list))
        : x && typeof x === "object"
          ? Object.fromEntries(Object.entries(x).map(([k, v]) => [k, cut(v, text, list)]))
          : x;
  for (const [text, list] of [
    [400, 40],
    [160, 16],
    [60, 6],
  ]) {
    const fields = cut(s.fields, text, list) as Screen["fields"];
    if (size({ ...s, fields }) <= max) return { ...s, fields };
  }
  return {
    ...s,
    fields: { cut: "the screen summary was too long" } as unknown as Screen["fields"],
  };
}

/** The line of a card the user was asked, as it waits. */
export const waitingText = (c: ScreenCard) =>
  `Waiting for your OK · ${c.go[0]}${c.go.slice(1).toLowerCase()}`;

/** A cap notice (a daily limit reached): its title and when it lifts, else null. */
export function limitOf(
  entries: Entry[],
  now = Date.now(),
): { title: string; text: string; until: string } | null {
  const last = [...entries].reverse().find((e) => e.kind === "notice" || e.kind === "user");
  const err = last?.kind === "notice" ? last.error : undefined;
  if (!err || !["cap_day", "cap_tokens"].includes(err.code)) return null;
  if (err.resets_at && Date.parse(err.resets_at) <= now) return null;
  const at = err.resets_at ? new Date(err.resets_at) : null;
  const until = at
    ? at.getHours() === 0 && at.getMinutes() === 0
      ? "Back at midnight"
      : `Back at ${at.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}`
    : "Back later";
  return { title: "Daily limit reached", text: err.text, until };
}

/** The text with each @handle marked (rendered as text: the parts, never HTML). */
export const mentionParts = (text: string) =>
  text.split(/(@[a-z][a-z0-9_-]*)/g).map((t) => ({ t, at: /^@[a-z]/.test(t) }));
