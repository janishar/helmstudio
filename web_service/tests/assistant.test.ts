// The assistant's pure parts (lib/assistant.ts; plan 18 §22.3, §22.4): navigate's route table, the place in line,
// the chat's event fold, read-only in another tab, the screen summary's cap, the action argument check, the daily
// limit and a result's line.
import { describe, expect, it } from "vitest";
import {
  about,
  applyEvent,
  argsError,
  elsewhere,
  fitScreen,
  limitOf,
  mentionParts,
  onRoute,
  ordinal,
  placeInLine,
  routeUrl,
  SCREENS,
  screenLabel,
  toResult,
  type Entry,
  type Screen,
  type Thread,
} from "@/lib/assistant";
import type { S } from "@/lib/api";

const T = "11111111-2222-4333-8444-555555555555";
const thread = (over: Partial<Thread> = {}): Thread => ({
  id: T,
  title: "New chat",
  state: "idle",
  client_id: null,
  pending: null,
  caps: { model_calls: 0, tool_calls: 0, memory_writes: 0 },
  created_at: "2026-11-22T00:00:00Z",
  updated_at: "2026-11-22T00:00:00Z",
  ...over,
});

describe("navigate's route table", () => {
  it("builds each target with its params", () => {
    expect(routeUrl({ to: "/", query: { s: "abc" } }, [])).toEqual({ url: "/?s=abc", path: "/" });
    expect(routeUrl({ to: "/", query: { use: "t1", w: "w1" } }, [])).toEqual({
      url: "/?use=t1&w=w1",
      path: "/",
    });
    expect(routeUrl({ to: "/gallery", query: { batch: "b" } }, [])).toEqual({
      url: "/gallery?batch=b",
      path: "/gallery",
    });
    expect(routeUrl({ to: "/pipelines/{id}", id: "p 1", query: { view: "studio" } }, [])).toEqual({
      url: "/pipelines/p%201?view=studio",
      path: "/pipelines/p%201",
    });
    expect(
      routeUrl(
        { to: "/pipelines/batches/{id}/rows/{row}", id: "b", row: 3, query: { attempt: "2" } },
        [],
      ),
    ).toEqual({
      url: "/pipelines/batches/b/rows/3?attempt=2",
      path: "/pipelines/batches/b/rows/3",
    });
    expect(routeUrl({ to: "/models", query: { tab: "loras" } }, [])).toEqual({
      url: "/models?tab=loras",
      path: "/models",
    });
  });
  it("refuses what is not a target, a wrong param, a missing id, and Admin to a non-admin", () => {
    expect(routeUrl({ to: "/signin" }, [])).toEqual({ error: "/signin is not a screen" });
    expect(routeUrl({ to: "/gallery", query: { t: "x" } }, [])).toEqual({
      error: "/gallery takes no t",
    });
    expect(routeUrl({ to: "/models", query: { tab: "users" } }, [])).toEqual({
      error: "tab must be loras",
    });
    expect(routeUrl({ to: "/pipelines/{id}" }, [])).toEqual({ error: "id is required" });
    expect(routeUrl({ to: "/", query: { use: "t" } }, [])).toEqual({
      error: "use and w go together",
    });
    // the owner's run (2026-11-24): w alone to open Create on a workflow says how to do it instead
    expect(routeUrl({ to: "/", query: { w: "qwen-image" } }, [])).toEqual({
      error: "w goes with use (a take); to pick a workflow, go to / and switchTo it",
    });
    expect(routeUrl({ to: "/admin" }, ["user"])).toEqual({ error: "Admin is for admins only" });
    expect(routeUrl({ to: "/admin", query: { tab: "hosts" } }, ["admin"])).toEqual({
      url: "/admin?tab=hosts",
      path: "/admin",
    });
  });
  it("tells the model what each query param is, and that a workflow is picked with switchTo", () => {
    const q = (
      SCREENS.shell.actions.navigate.input.properties as Record<string, { description?: string }>
    ).query;
    expect(q.description).toContain("use (a take id) with w (a workflow id)");
    expect(q.description).toContain("switchTo");
  });
  it("names screens and matches routes", () => {
    expect(screenLabel("/pipelines/batches/x/review")).toBe("Review");
    expect(screenLabel("/pipelines/x")).toBe("Pipeline");
    expect(screenLabel("/")).toBe("Create");
    expect(onRoute(["/pipelines/{id}"], "/pipelines/abc")).toBe(true);
    expect(onRoute(["/pipelines/{id}"], "/pipelines/batches/abc")).toBe(false);
    expect(onRoute(["*"], "/anything")).toBe(true);
  });
});

describe("words", () => {
  it("ordinals, times and @handles", () => {
    expect([1, 2, 3, 4, 11, 12, 13, 21, 22].map(ordinal)).toEqual([
      "1st",
      "2nd",
      "3rd",
      "4th",
      "11th",
      "12th",
      "13th",
      "21st",
      "22nd",
    ]);
    expect(about(80)).toBe("about 1 min 20 s");
    expect(about(40)).toBe("about 40 s");
    expect(about(120)).toBe("about 2 min");
    expect(mentionParts("hi @mara and <b>x</b>")).toEqual([
      { t: "hi ", at: false },
      { t: "@mara", at: true },
      { t: " and <b>x</b>", at: false },
    ]);
  });
  it("the place in line counts the caller's queued runs there and everyone else's", () => {
    const q = {
      runs: [
        { status: "queued", host: "mac" },
        { status: "queued", host: "gcp" },
        { status: "running", host: "mac" },
      ],
      hosts: [{ host: "mac", state: "busy", others_queued: 2 }],
    } as unknown as S<"Queue">;
    expect(placeInLine(q, "mac")).toBe(4);
    expect(placeInLine(q, "gcp")).toBe(2);
    expect(placeInLine(q, null)).toBeNull();
    expect(placeInLine(null, "mac")).toBeNull();
  });
});

describe("the chat's events", () => {
  const entry = (id: string, over: Partial<Entry> = {}): Entry => ({
    id,
    turn: 1,
    kind: "line",
    at: "2026-11-22T00:00:00Z",
    line: { id: `${T}:1:0`, status: "running", text: "Setting…", undo: false },
    ...over,
  });
  it("an entry of a known id replaces it; a thread event replaces the chat's state", () => {
    let v = { thread: thread(), entries: [] as Entry[] };
    v = applyEvent(v, { seq: 1, type: "entry", entry: entry("e1") });
    v = applyEvent(v, {
      seq: 2,
      type: "entry",
      entry: entry("e1", { line: { id: `${T}:1:0`, status: "done", text: "Set", undo: true } }),
    });
    expect(v.entries).toHaveLength(1);
    expect(v.entries[0].line?.status).toBe("done");
    v = applyEvent(v, { seq: 3, type: "thread", thread: thread({ state: "working" }) });
    expect(v.thread.state).toBe("working");
  });
  it("a replayed older snapshot never replaces the newer one (a reload re-ran old parked turns)", () => {
    const now = thread({ state: "idle", updated_at: "2026-11-22T10:00:00.123456Z" });
    let v = { thread: now, entries: [] as Entry[] };
    const old = thread({
      state: "waiting",
      client_id: "tabA-1234",
      updated_at: "2026-11-22T09:00:00Z",
      pending: { turn: 2, actions: [{ id: `${T}:2:1`, name: "edit", args: {} }], cards: [] },
    });
    v = applyEvent(v, { seq: 5, type: "thread", thread: old });
    expect(v.thread).toBe(now);
    const later = thread({ state: "working", updated_at: "2026-11-22T10:00:01Z" });
    expect(applyEvent(v, { seq: 9, type: "thread", thread: later }).thread).toBe(later);
  });
  it("another tab's chat is read-only here, an idle one is not", () => {
    expect(elsewhere(thread({ state: "waiting", client_id: "tabB-1234" }), "tabA-1234")).toBe(true);
    expect(elsewhere(thread({ state: "waiting", client_id: "tabA-1234" }), "tabA-1234")).toBe(
      false,
    );
    expect(elsewhere(thread({ state: "idle", client_id: "tabB-1234" }), "tabA-1234")).toBe(false);
  });
  it("a daily cap notice is the limit until it lifts; a later message ends it", () => {
    const at = "2026-11-22T10:00:00Z";
    const cap = entry("n", {
      kind: "notice",
      text: "You used today's 200 model calls.",
      error: {
        code: "cap_day",
        text: "You used today's 200 model calls.",
        resets_at: "2999-01-01T00:00:00Z",
      },
    });
    expect(limitOf([cap])?.title).toBe("Daily limit reached");
    expect(limitOf([cap], undefined, Date.parse("3000-01-01T00:00:00Z"))).toBeNull();
    expect(limitOf([cap, entry("u", { kind: "user", text: "hi", at })])).toBeNull();
    const other = entry("n2", {
      kind: "notice",
      text: "x",
      error: { code: "provider", text: "x" },
    });
    expect(limitOf([other])).toBeNull();
  });
  it("the cap lifts at once when the admin raised it above the one hit", () => {
    const hit = (code: "cap_day" | "cap_tokens", limit: number) =>
      entry("n", {
        kind: "notice",
        text: "x",
        error: { code, text: "x", resets_at: "2999-01-01T00:00:00Z", limit },
      });
    const limits = {
      model_calls_per_day: 2000,
      tokens_per_day: 1000000,
      look_at_per_hour: 20,
      messages_per_minute: 6,
    };
    expect(limitOf([hit("cap_day", 1)], limits)).toBeNull();
    expect(limitOf([hit("cap_day", 2000)], limits)?.title).toBe("Daily limit reached");
    expect(limitOf([hit("cap_tokens", 1000)], limits)).toBeNull();
    expect(limitOf([hit("cap_tokens", 1000000)], limits)?.title).toBe("Daily limit reached");
  });
});

describe("the screen summary and the actions", () => {
  const s = (fields: Record<string, unknown>): Screen => ({
    route: "/",
    ids: {},
    roles: [],
    as_of: "2026-11-22T00:00:00Z",
    hidden: false,
    actions: ["navigate"],
    fields: fields as Screen["fields"],
  });
  it("cuts long texts, then lists, then the fields, under the cap", () => {
    const small = s({ a: "x" });
    expect(fitScreen(small)).toBe(small);
    const long = fitScreen(s({ prompt: "p".repeat(20000) }));
    expect(JSON.stringify(long).length).toBeLessThan(15000);
    expect((long.fields as unknown as { prompt: string }).prompt.endsWith("…")).toBe(true);
    const many = fitScreen(s({ takes: Array.from({ length: 5000 }, (_, i) => ({ id: `t${i}` })) }));
    expect(JSON.stringify(many).length).toBeLessThan(15000);
    expect(many.route).toBe("/");
  });
  it("checks an action's arguments against tools/actions.json", () => {
    expect(argsError("create", "seeds", { n: 2 })).toBeNull();
    expect(argsError("create", "seeds", {})).toBe("n is required");
    expect(argsError("create", "view", { view: "timeline" })).toBe(
      "view must be one of studio, graph",
    );
    expect(argsError("gallery", "sort", { order: "newest", x: 1 })).toBe("x is not an argument");
    expect(argsError("shell", "await", { what: "run", id: "r" })).toBeNull();
  });
  it("a result's line is at most 300 characters and says whether it has Undo", () => {
    const r = toResult(`${T}:1:0`, {
      status: "done",
      text: "x".repeat(400),
      undo: { undo: () => 0, redo: () => 0 },
    });
    expect(r.line.text).toHaveLength(300);
    expect(r.line.undo).toBe(true);
    expect(toResult(`${T}:1:1`, { status: "failed", text: "no", error: "no" })).toEqual({
      id: `${T}:1:1`,
      status: "failed",
      line: { text: "no", undo: false },
      error: "no",
    });
  });
});

// A reload (or a redeploy) keeps this tab's drawer id and its open state; another tab gets its own id.
describe("the tab's id and drawer state survive a reload", () => {
  it("keeps the id in sessionStorage and remembers open", async () => {
    const { tabClientId, tabOpen, rememberOpen } = await import("@/lib/assistant");
    sessionStorage.clear();
    const a = tabClientId();
    expect(a).toMatch(/^[A-Za-z0-9]{8,64}$/);
    expect(tabClientId()).toBe(a);
    sessionStorage.clear();
    expect(tabClientId()).not.toBe(a);
    expect(tabOpen()).toBe(false);
    rememberOpen(true);
    expect(tabOpen()).toBe(true);
    rememberOpen(false);
    expect(tabOpen()).toBe(false);
  });
});
