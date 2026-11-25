// The in-app assistant (plan 18 §22; docs/mock/Assistant.dc.html): what the drawer folds from a chat's events, the
// screen actions' outcomes and cards, navigate's route table (§22.4), the screen summary's size cap, and the words
// its lines use. Everything the model wrote or a tool returned is text: the drawer renders it as text, never HTML.
import actionsJson from "@/gen/actions.json";
import type { S } from "@/lib/api";
import { refText, splitOptions } from "@/lib/answer-refs";

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
  /** no redo: Undo only once (an Auto step's undo deletes what it made, API 1.68) */
  undo?: { undo: () => unknown; redo?: () => unknown };
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
/** `yes(true)`: Auto mode ran it without the card (API 1.68); its line says what it made, not "You clicked". */
export type Asked = { card: ScreenCard; yes: (auto?: boolean) => Promise<Outcome> };
/** A screen action's handler: an outcome now, or a card whose Yes makes it. `auto`: Auto mode lets it run by itself
 * (a dialog saves itself once ready). */
export type Handler = (
  args: Record<string, unknown>,
  ctx: { progress: (text: string) => void; auto?: boolean },
) => Outcome | Asked | Promise<Outcome | Asked>;

/** An Auto step's line (board frame 13): "Auto · " first, never "You clicked". */
export const autoText = (text: string) =>
  text.startsWith("Auto · ") ? text : `Auto · ${text.replace(/^You clicked /, "")}`;

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

/** A fresh id. */
export const newClientId = () =>
  (globalThis.crypto?.randomUUID?.() ?? `${Date.now()}${Math.random()}`).replace(
    /[^A-Za-z0-9]/g,
    "",
  );

const CID_KEY = "hs.assistant.cid";
export const OPEN_KEY = "hs.assistant.open";

/** This tab's drawer id: kept in sessionStorage, so a reload (or a redeploy) stays the same tab and keeps its chat;
 * another tab gets its own. Without storage, a fresh id per load (Continue here moves the chat).
 * ponytail: a duplicated browser tab copies sessionStorage and so shares the id; add a BroadcastChannel check if
 * that ever matters. */
export function tabClientId(): string {
  try {
    const s = globalThis.sessionStorage;
    const had = s?.getItem(CID_KEY);
    if (had && /^[A-Za-z0-9]{8,64}$/.test(had)) return had;
    const id = newClientId();
    s?.setItem(CID_KEY, id);
    return id;
  } catch {
    return newClientId();
  }
}

/** Whether this tab had the drawer open before a reload. */
export function tabOpen(): boolean {
  try {
    return globalThis.sessionStorage?.getItem(OPEN_KEY) === "1";
  } catch {
    return false;
  }
}

export function rememberOpen(open: boolean) {
  try {
    globalThis.sessionStorage?.setItem(OPEN_KEY, open ? "1" : "0");
  } catch {
    /* no storage: the drawer starts closed after a reload */
  }
}

/** A chat's events folded: an entry with a known id replaces it (a line going from running to done), a thread
 * event replaces the chat's state. */
export function applyEvent(v: ThreadView, e: S<"AssistantEvent">): ThreadView {
  // the log is replayed from its start when a tab opens the chat: an older snapshot never replaces a newer one (a
  // reload would run old parked turns again)
  if (e.type === "thread" && e.thread)
    return Date.parse(e.thread.updated_at) < Date.parse(v.thread.updated_at)
      ? v
      : { ...v, thread: e.thread };
  if (e.type !== "entry" || !e.entry) return v;
  const en = e.entry;
  const i = v.entries.findIndex((x) => x.id === en.id);
  const entries = i < 0 ? [...v.entries, en] : v.entries.map((x, j) => (j === i ? en : x));
  // a steer read or sent as the next message moves to where that happened (its `at`, API 1.59)
  if (i >= 0 && v.entries[i].at !== en.at)
    entries.sort((x, y) => Date.parse(x.at) - Date.parse(y.at));
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
  if (q.has("w") && !q.has("use"))
    return { error: "w goes with use (a take); to pick a workflow, go to / and switchTo it" };
  if (q.has("use") && !q.has("w")) return { error: "use and w go together" };
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

/** The newest daily cap notice of the chat, else undefined. */
export const capNoticeId = (entries: Entry[] | undefined) =>
  entries?.findLast(
    (e) => e.kind === "notice" && ["cap_day", "cap_tokens"].includes(e.error?.code ?? ""),
  )?.id;

/** A cap notice (a daily limit reached): its title and when it lifts, else null. It lifts at once when the cap now
 * (`limits`, the status) is above the one it hit: the admin raised it. */
export function limitOf(
  entries: Entry[],
  limits?: S<"AssistantLimits">,
  now = Date.now(),
): { title: string; text: string; until: string } | null {
  const last = [...entries].reverse().find((e) => e.kind === "notice" || e.kind === "user");
  const err = last?.kind === "notice" ? last.error : undefined;
  if (!err || !["cap_day", "cap_tokens"].includes(err.code)) return null;
  if (err.resets_at && Date.parse(err.resets_at) <= now) return null;
  const cap = err.code === "cap_day" ? limits?.model_calls_per_day : limits?.tokens_per_day;
  if (err.limit !== undefined && cap !== undefined && cap > err.limit) return null;
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

/** A web source as its line lists it: "example-forum.org · LTX camera prompts" (text, never a link). */
export function sourceText(s: { title: string; url: string }) {
  let host = "";
  try {
    host = new URL(s.url).hostname.replace(/^www\./, "");
  } catch {
    /* not a URL: the title alone */
  }
  return [host, s.title.trim()].filter(Boolean).join(" · ") || s.url;
}

/** When a memory item was saved, as the Memory list says it: "today", "yesterday", "3 days ago". */
export function ago(iso: string, now = Date.now()) {
  const day = (t: number) =>
    Math.floor((t - new Date(t).getTimezoneOffset() * 60_000) / 86_400_000);
  const n = day(now) - day(Date.parse(iso));
  return n <= 0 ? "today" : n === 1 ? "yesterday" : `${n} days ago`;
}

/** A chat's time in the Chats list: the day of its last message, then the time ("today · 14:02", "21 Nov · 09:03"). */
export function chatTime(iso: string, now = Date.now()) {
  const d = new Date(iso);
  const day = ago(iso, now);
  const hm = `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`;
  const on =
    day === "today" || day === "yesterday"
      ? day
      : `${d.getDate()} ${d.toLocaleString("en-GB", { month: "short" })}`;
  return `${on} · ${hm}`;
}

/** The Memory list's tabs (§22.8): the kinds as the board names them. */
export const MEMORY_TABS = [
  ["preference", "Likes"],
  ["project", "Projects"],
  ["worked", "Worked"],
  ["summary", "Chats"],
] as const;

/** Sent by Admin › AI agents after a save, so the dock shows or hides the Assistant without a reload. */
export const ASSISTANT_CHANGED = "hs.assistant.changed";

/** An answer's blocks (alpha.2 item 22, board frame 10a): paragraphs, bullet and numbered lists, a plan checklist
 * ("- [ ]" / "- [x]", item 24) and code blocks. Headings and table rows stay plain lines. Text only: the drawer builds
 * React elements from them, never HTML. */
export type Block =
  | { k: "p"; text: string }
  | { k: "ul" | "ol"; items: string[] }
  | { k: "plan"; items: { done: boolean; text: string }[] }
  | { k: "code"; lang: string; text: string }
  | { k: "take"; id: string };

export function answerBlocks(text: string): Block[] {
  const out: Block[] = [];
  const lines = text.replace(/\r\n?/g, "\n").split("\n");
  let blank = true;
  const add = (k: "ul" | "ol" | "plan", item: string | { done: boolean; text: string }) => {
    const last = out.at(-1);
    if (last?.k === k) (last.items as unknown[]).push(item);
    else out.push({ k, items: [item] } as Block);
  };
  for (let i = 0; i < lines.length; i++) {
    const l = lines[i];
    const fence = /^\s*```\s*([\w-]*)/.exec(l);
    let m: RegExpExecArray | null;
    if (fence) {
      const body: string[] = [];
      for (i++; i < lines.length && !/^\s*```/.test(lines[i]); i++) body.push(lines[i]);
      out.push({ k: "code", lang: fence[1], text: body.join("\n") });
    } else if (!l.trim()) {
      blank = true;
      continue;
    } else if ((m = /^\s*\[\[take:([^\]\s|]{1,128})\]\]\s*$/.exec(l)) && m[1] !== "?") {
      out.push({ k: "take", id: m[1] }); // alone on its line: the take itself (item 10, board frame 12)
      blank = true;
      continue;
    } else if ((m = /^\s*[-*+]\s+\[( |x|X)\]\s+(.*)$/.exec(l)))
      add("plan", { done: m[1] !== " ", text: m[2] });
    else if ((m = /^\s*[-*+•]\s+(.*)$/.exec(l))) add("ul", m[1]);
    else if ((m = /^\s*\d+[.)]\s+(.*)$/.exec(l))) add("ol", m[1]);
    else if ((m = /^\s*#{1,6}\s+(.*)$/.exec(l))) {
      out.push({ k: "p", text: m[1].trim() }); // a heading: a line of its own, plain
      blank = true;
      continue;
    } else {
      const last = out.at(-1);
      if (last?.k === "p" && !blank) last.text += `\n${l.trim()}`;
      else out.push({ k: "p", text: l.trim() });
    }
    blank = false;
  }
  return out;
}

/** A piece of an answer's line: bold, italic, inline code, a timecode ("0:12"), an @handle, else text. A link
 * [text](url) is its text only. */
export type Inline = { t: string; k?: "b" | "i" | "code" | "tc" | "at" };
const INLINE =
  /(\[\[(?:take|element|voice|workflow):[^\]\s|]{1,128}\]\]|\*\*[^*\n]+\*\*|`[^`\n]+`|\[[^\]\n]+\]\([^)\s]+\)|(?<![\w*])\*[^*\s][^*\n]*?\*(?![\w*])|(?<!\w)_[^_\s][^_\n]*?_(?!\w)|(?<![\d:])\d{1,2}:[0-5]\d(?![\d:]))/;
export function inlineParts(text: string): Inline[] {
  return text.split(INLINE).flatMap((s, j): Inline[] => {
    if (!s) return [];
    if (j % 2 === 0) return mentionParts(s).map((p) => (p.at ? { t: p.t, k: "at" } : { t: p.t }));
    if (s.startsWith("**")) return [{ t: s.slice(2, -2), k: "b" }];
    if (s.startsWith("`")) return [{ t: s.slice(1, -1), k: "code" }];
    if (s.startsWith("[[")) return [{ t: s }]; // a ref, kept whole for its chip (no italics inside an id)
    if (s.startsWith("[")) return [{ t: s.slice(1, s.indexOf("](")) }];
    if (/^\d/.test(s)) return [{ t: s, k: "tc" }];
    return [{ t: s.slice(1, -1), k: "i" }];
  });
}

/** "1:05" → 65 (seconds). */
export const timecode = (t: string) => {
  const [m, s] = t.split(":").map(Number);
  return m * 60 + s;
};

/** An answer as Copy puts it on the clipboard: the text without its markers, refs as their titles, options as a
 * numbered list. */
export const plainAnswer = (answer: string) => {
  const { text, options } = splitOptions(answer);
  const opts = (options ?? []).map((o, i) => `${i + 1}. ${o.title}${o.line ? ` — ${o.line}` : ""}`);
  return refText([text, ...opts].join("\n"))
    .replace(/^\s*```[\w-]*\s*$/gm, "")
    .replace(/\*\*([^*\n]+)\*\*/g, "$1")
    .replace(/`([^`\n]+)`/g, "$1")
    .replace(/\[([^\]\n]+)\]\([^)\s]+\)/g, "$1")
    .trim();
};

/** A folded turn's line (item 25, board frame 10h): "14 steps · 2 takes · 1 memory". */
export function stepSummary(lines: { text: string; memory_id?: string }[]) {
  const plural = (n: number, one: string, many: string) => `${n} ${n === 1 ? one : many}`;
  const takes = lines.reduce(
    (s, l) => s + Number(/^(?:Run done ·|Generated) (\d+) takes?\b/.exec(l.text)?.[1] ?? 0),
    0,
  );
  const memories = lines.filter((l) => l.memory_id || /^Remembered\b/.test(l.text)).length;
  return [
    plural(lines.length, "step", "steps"),
    takes && plural(takes, "take", "takes"),
    memories && plural(memories, "memory", "memories"),
  ]
    .filter(Boolean)
    .join(" · ");
}

/** What a line's Undo names (board frame 14c): its text up to the first " · ". */
export const undoName = (text: string) => text.replace(/^Auto · /, "").split(" · ")[0];
