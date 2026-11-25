// The assistant's pure parts (lib/assistant.ts; plan 18 §22.3, §22.4): navigate's route table, the place in line,
// the chat's event fold, read-only in another tab, the screen summary's cap, the action argument check, the daily
// limit and a result's line.
import { describe, expect, it } from "vitest";
import {
  about,
  answerBlocks,
  applyEvent,
  argsError,
  chatTime,
  elsewhere,
  fitScreen,
  inlineParts,
  limitOf,
  mentionParts,
  plainAnswer,
  plainError,
  stepSummary,
  timecode,
  undoName,
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
  auto: false,
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
    expect(routeUrl({ to: "/admin" }, ["user"])).toEqual({ error: "System is for admins only" });
    // plan 33: the catalog's /admin opens System; its providers tab is Hub's
    expect(routeUrl({ to: "/admin", query: { tab: "hosts" } }, ["admin"])).toEqual({
      url: "/system?tab=hosts",
      path: "/system",
    });
    expect(routeUrl({ to: "/admin", query: { tab: "providers" } }, ["admin"])).toEqual({
      url: "/hub?tab=providers",
      path: "/hub",
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
    expect(screenLabel("/hub")).toBe("Hub");
    expect(screenLabel("/system")).toBe("System");
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

describe("chatTime", () => {
  it("names the day of a chat's last message, then its time", () => {
    const now = new Date(2026, 10, 25, 15, 0).getTime();
    expect(chatTime(new Date(2026, 10, 25, 14, 2).toISOString(), now)).toBe("today · 14:02");
    expect(chatTime(new Date(2026, 10, 24, 18, 15).toISOString(), now)).toBe("yesterday · 18:15");
    expect(chatTime(new Date(2026, 10, 21, 9, 3).toISOString(), now)).toBe("21 Nov · 09:03");
  });
});

describe("answers that read well (alpha.2 items 21, 22, 24, 25)", () => {
  it("splits an answer into paragraphs, lists, a plan and code; headings and tables stay plain", () => {
    const text = [
      "# Plan",
      "Two lines",
      "of one paragraph",
      "",
      "- a",
      "* b",
      "1. one",
      "2) two",
      "- [x] done",
      "- [ ] next",
      "| a | b |",
      "```prompt",
      "  keep  spaces",
      "```",
    ].join("\n");
    expect(answerBlocks(text)).toEqual([
      { k: "p", text: "Plan" },
      { k: "p", text: "Two lines\nof one paragraph" },
      { k: "ul", items: ["a", "b"] },
      { k: "ol", items: ["one", "two"] },
      {
        k: "plan",
        items: [
          { done: true, text: "done" },
          { done: false, text: "next" },
        ],
      },
      { k: "p", text: "| a | b |" },
      { k: "code", lang: "prompt", text: "  keep  spaces" },
    ]);
    expect(answerBlocks("```\nnever closed")).toEqual([
      { k: "code", lang: "", text: "never closed" },
    ]);
  });

  it("marks bold, italic, code, timecodes and @handles; a link is its text", () => {
    expect(
      inlineParts("**Bold** and *it* or _it_ `code` at 0:17 with @mara, [guide](https://x.test)"),
    ).toEqual([
      { t: "Bold", k: "b" },
      { t: " and " },
      { t: "it", k: "i" },
      { t: " or " },
      { t: "it", k: "i" },
      { t: " " },
      { t: "code", k: "code" },
      { t: " at " },
      { t: "0:17", k: "tc" },
      { t: " with " },
      { t: "@mara", k: "at" },
      { t: ", " },
      { t: "guide" },
    ]);
    // aspect ratios and multiplications are not timecodes or italics
    expect(inlineParts("16:9 and 2 * 3 * 4").every((p) => !p.k)).toBe(true);
    expect(timecode("1:05")).toBe(65);
  });

  it("Copy's text, a folded turn's line and Undo's name", () => {
    expect(plainAnswer("**Done**: `x` [see](https://x.test)\n```\ncode\n```")).toBe(
      "Done: x see\n\ncode",
    );
    expect(
      stepSummary([
        { text: "Opened Create" },
        { text: "Run done · 4 takes" },
        { text: "Run done · 1 take" },
        { text: "Remembered: warm light", memory_id: "m" },
      ]),
    ).toBe("4 steps · 5 takes · 1 memory");
    expect(stepSummary([{ text: "Opened Create" }])).toBe("1 step");
    expect(undoName("Set Lighting: Blue hour")).toBe("Set Lighting: Blue hour");
    expect(undoName("Generated Elias at the window · 4 takes")).toBe(
      "Generated Elias at the window",
    );
  });
});

describe("failed lines and provider errors in plain words (item 23)", () => {
  it("says what happened, the raw text kept for Details", () => {
    expect(plainError("switchTo · name resolution failed")).toEqual({
      plain: "Lost the connection to the studio for a moment; try again",
      raw: "switchTo · name resolution failed",
    });
    expect(plainError("seeds · extra is not an argument")).toEqual({
      plain: "Failed · extra is not an argument",
      raw: "seeds · extra is not an argument",
    });
    expect(plainError("deepseek: the provider is busy or down (HTTP 429)").plain).toBe(
      "deepseek is busy. Try again in a minute.",
    );
    expect(plainError("deepseek: HTTP 400").plain).toBe(
      "deepseek turned the request down. Try again or rephrase.",
    );
    expect(plainError("deepseek: cannot resolve api.x: [Errno 8] nodename").plain).toBe(
      "Could not reach deepseek. Try again in a minute.",
    );
    expect(plainError("Thumbnails · thumbs failed · Thumbs: timed out").plain).toBe(
      "Thumbnails · thumbs failed · Thumbs took too long to answer. Try again.",
    );
    expect(plainError("Thumbnails · thumbs failed · timed out").plain).toBe(
      "Thumbnails · thumbs failed · It took too long to answer. Try again.",
    );
    expect(plainError("Thumbnails did not answer").plain).toBe(
      "Thumbnails took too long to answer. Try again.",
    );
    expect(plainError("timelineSave · The server had a problem (502). Try again.").plain).toBe(
      "The studio had a problem. Try again.",
    );
    expect(plainError("generate · Load failed").plain).toBe(
      "Lost the connection to the studio for a moment; try again",
    );
    // a job's own "download failed" or "load failed" is not a lost connection
    for (const t of [
      "Run failed · the download failed (ConnectionError)",
      "Run failed · LoRA load failed",
    ])
      expect(plainError(t)).toEqual({ plain: t });
    // already plain: no Details
    expect(plainError("Search failed · Brave")).toEqual({ plain: "Search failed · Brave" });
    expect(plainError("Length: the longest is 13.4 s")).toEqual({
      plain: "Length: the longest is 13.4 s",
    });
  });
});
