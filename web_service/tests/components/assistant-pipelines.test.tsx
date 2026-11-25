// @vitest-environment jsdom
// The pipeline screens' actions for the assistant (components/pipelines/assistant.ts; plan 18 §22.5, §22.6): the
// editor's edits checked by the pipeline's rules (one Undo for all, none per line), delete and the judge test as
// cards with the judge's egress line, the Studio view's Run step as a card and its pick with Undo, the batch's
// resume, retry, again and stop as cards with what leaves the machine, pause as a line, a gate's pick as a card, and
// New batch filled (Undo) then started by a card. No costly action runs before its card's Yes.
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import pipelineFixture from "../../../contracts/fixtures/http/Pipeline.valid.1.json";
import batchFixture from "../../../contracts/fixtures/http/Batch.valid.3.json";
import { Registry, ToolsContext } from "@/components/assistant/tools";
import { asked, type Asked, type Outcome } from "@/lib/assistant";
import { api, type S } from "@/lib/api";
import { blankPipeline, type PDoc } from "@/lib/pipeline";
import {
  batchTools,
  editorTools,
  egressNote,
  reviewTools,
  studioTools,
  toCsv,
  type EditorCtx,
  type StudioLive,
} from "@/components/pipelines/assistant";
import { answer, take } from "./helpers";

const push = vi.fn();
vi.mock("next/navigation", () => ({ useRouter: () => ({ push, replace: vi.fn() }) }));
vi.mock("next/link", () => ({
  default: ({ href, children, ...rest }: { href: string; children: React.ReactNode }) => (
    <a href={href} {...rest}>
      {children}
    </a>
  ),
}));
vi.mock("@/components/shell", () => ({ useUser: () => ({ name: "Ada" }) }));
vi.stubGlobal(
  "EventSource",
  class {
    addEventListener() {}
    close() {}
  },
);

beforeAll(() => {
  HTMLElement.prototype.hidePopover ??= () => undefined;
  HTMLDialogElement.prototype.showModal ??= function (this: HTMLDialogElement) {
    this.open = true;
  };
  HTMLDialogElement.prototype.close ??= function (this: HTMLDialogElement) {
    this.open = false;
    this.dispatchEvent(new Event("close"));
  };
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  push.mockReset();
});

const plain = pipelineFixture as unknown as S<"Pipeline">;
const base = batchFixture as unknown as S<"Batch">;
const ctx = { progress: () => undefined };
const out = (o: Outcome | Asked) => {
  if (asked(o)) throw new Error("a card");
  return o;
};
const card = (o: Outcome | Asked) => {
  if (!asked(o)) throw new Error(`no card: ${o.text}`);
  return o;
};

/** A pipeline with a Decide that calls DeepSeek, as Submit's egress shows it. */
const remote = {
  ...plain,
  nodes: {
    ...plain.nodes,
    check: { kind: "decide", question: "q", from: ["$prompt"], routes: { a: "x", b: "y" } },
  },
  egress: [
    {
      node: "check",
      where: "remote",
      provider: "DeepSeek",
      model: "deepseek-chat",
      fields: ["question", "routes", "$prompt"],
      thumbnails: 0,
      per_row: { calls: 6, tokens: 53952 },
      chat: null,
    },
  ],
} as unknown as S<"Pipeline">;

describe("what leaves the machine", () => {
  it("is one line naming the node, what it sends, where, and the worst case", () => {
    expect(egressNote(remote, 10)).toEqual({
      tags: ["uses the GPU", "sends to DeepSeek"],
      note: "What leaves the machine: Decide sends its question, its routes, the prompt input to DeepSeek; no thumbnails. Worst case 60 calls, 539,520 tokens.",
    });
    expect(egressNote(plain, 10)).toEqual({ tags: ["uses the GPU"] });
  });
  it("New batch rows become a CSV table, quoted where needed", () => {
    expect(toCsv([{ prompt: 'a "fox", at night' }, { prompt: "rain", edit: "blue" }])).toBe(
      'prompt,edit\n"a ""fox"", at night",\nrain,blue',
    );
  });
});

describe("the pipeline editor's actions", () => {
  function editor(over: Partial<EditorCtx> = {}) {
    let d: PDoc = blankPipeline("Rain");
    const c: EditorCtx = {
      pipeline: { ...plain, title: "Rain", version: 2 },
      doc: () => d,
      edit: (f) => (d = f(d)),
      wfDocs: {},
      palette: {},
      env: {},
      titles: {},
      models: {},
      problems: [],
      selected: null,
      select: () => undefined,
      view: "graph",
      unsaved: () => false,
      custom: [],
      save: vi.fn(async () => undefined),
      newPipeline: vi.fn(),
      deletePipeline: vi.fn(async () => undefined),
      memory: [],
      reloadMemory: vi.fn(async () => undefined),
      addNote: vi.fn(async () => "m1"),
      ...over,
    };
    const spec = () => editorTools(c);
    const call = (name: string, args: Record<string, unknown> = {}) =>
      Promise.resolve(spec().actions[name](args, ctx));
    return { c, call, doc: () => d, spec };
  }

  it("edits the document, answers with its problems, and refuses what the rules refuse", async () => {
    const { call, doc, spec } = editor();
    expect(out(await call("addNode", { kind: "robot" }))).toMatchObject({ status: "failed" });
    const step = out(await call("addNode", { kind: "step", id: "still" }));
    expect(step).toMatchObject({ status: "done", result: { node: "still" } });
    expect(step.undo).toBeUndefined();
    expect(out(await call("addNode", { kind: "output" }))).toMatchObject({ status: "done" });
    const wire = out(await call("connect", { from: "inputs", to: "still" }));
    expect(wire).toMatchObject({ status: "done", result: { valid: false } });
    expect((wire.result as { problems: unknown[] }).problems.length).toBeGreaterThan(0);
    expect(doc().edges).toContainEqual(["inputs", "still"]);
    expect(out(await call("connect", { from: "nope", to: "still" }))).toMatchObject({
      status: "failed",
    });
    expect(out(await call("setNode", { id: "still", fields: { kind: "gate" } }))).toMatchObject({
      status: "failed",
    });
    expect(out(await call("setNode", { id: "still", fields: { seeds: 3 } }))).toMatchObject({
      status: "done",
    });
    expect(doc().nodes.still).toMatchObject({ kind: "step", seeds: 3 });
    expect(out(await call("disconnect", { from: "inputs", to: "output" }))).toMatchObject({
      status: "failed",
    });
    expect(out(await call("disconnect", { from: "inputs", to: "still" }))).toMatchObject({
      status: "done",
    });
    expect(out(await call("renamePipeline", { title: "Rain v2" }))).toMatchObject({
      text: "Renamed pipeline: Rain v2",
    });
    expect(spec().sees).toEqual(["Pipeline · Graph", "Rain v2 · v2"]);
    expect(out(await call("removeNode", { id: "still" }))).toMatchObject({ status: "done" });
    expect(doc().nodes.still).toBeUndefined();
  });

  it("delete and memory delete are cards; nothing goes until Yes", async () => {
    const m = { id: "m1", kind: "note", text: "keep the fox orange" } as S<"MemoryItem">;
    const { c, call } = editor({ memory: [m] });
    const del = card(await call("deletePipeline"));
    expect(del.card).toMatchObject({ kind: "delete", title: "Delete Rain?", danger: true });
    expect(c.deletePipeline).not.toHaveBeenCalled();
    await del.yes();
    expect(c.deletePipeline).toHaveBeenCalledTimes(1);
    const deletes: string[] = [];
    vi.spyOn(api, "DELETE").mockImplementation(((
      _p: string,
      i: { params: { path: { item_id: string } } },
    ) => {
      deletes.push(i.params.path.item_id);
      return Promise.resolve({
        data: undefined,
        error: undefined,
        response: new Response(null, { status: 204 }),
      });
    }) as never);
    const mem = card(await call("deleteMemory", { item_id: "m1" }));
    expect(mem.card.rows[0]).toEqual(["Goes", "note “keep the fox orange”"]);
    expect(deletes).toEqual([]);
    await mem.yes();
    expect(deletes).toEqual(["m1"]);
    expect(out(await call("addMemory", { text: "no watermarks" }))).toMatchObject({
      status: "done",
      result: { item_id: "m1" },
    });
    expect(c.addNote).toHaveBeenCalledWith("no watermarks");
  });

  it("the judge test is a card with the judge's egress line; it posts only on Yes", async () => {
    const t = take({ title: "Fox 1" });
    let d: PDoc = {
      ...blankPipeline("Rain"),
      nodes: {
        ...blankPipeline("Rain").nodes,
        judge: {
          kind: "judge",
          takes: "still",
          keep: 1,
          criteria: { must: ["sharp"], nice: ["on prompt"] },
        },
      },
    } as PDoc;
    const { c, call } = editor({
      doc: () => d,
      edit: (f) => (d = f(d)),
      models: { judge: "remote:DeepSeek/deepseek-chat" },
    });
    const posts: unknown[] = [];
    answer("GET", {
      "/v1/studio/takes/{take_id}": () => ({ data: t }),
      "/v1/studio/judge-tests/{test_id}": () => ({
        data: {
          id: "jt1",
          status: "succeeded",
          verdict: "pass",
          takes: [
            { take_id: t.id, passes: true, picked: true, rank: 1, reason: "sharp", checks: [] },
          ],
        },
      }),
    });
    answer("POST", {
      "/v1/studio/pipelines/{pipeline_id}/judge-tests": ({ body }) => {
        posts.push(body);
        return { status: 202, data: { id: "jt1", status: "queued", takes: [] } };
      },
    });
    expect(out(await call("judgeTest", { node: "inputs", take_ids: [t.id] }))).toMatchObject({
      status: "failed",
    });
    expect(out(await call("judgeTest", { node: "judge", take_ids: [] }))).toMatchObject({
      status: "failed",
    });
    const o = card(await call("judgeTest", { node: "judge", take_ids: [t.id] }));
    expect(o.card).toMatchObject({
      kind: "judge_test",
      title: "Test the judge?",
      tags: ["uses the GPU", "sends to DeepSeek"],
      note: "Sends the criteria and the judge's reasons to DeepSeek; no thumbnails.",
      go: "TEST",
      fine: "Same as Test on the Judge node.",
    });
    expect(o.card.rows).toContainEqual(["Criteria", "sharp, on prompt"]);
    expect(posts).toEqual([]);
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      const r = o.yes();
      await vi.advanceTimersByTimeAsync(2000);
      expect(await r).toMatchObject({
        status: "done",
        result: { test_id: "jt1", verdict: "pass" },
      });
    } finally {
      vi.useRealTimers();
    }
    expect(c.save).toHaveBeenCalled();
    expect(posts).toEqual([{ node: "judge", take_ids: [t.id] }]);
  });
});

describe("the Studio view's actions", () => {
  it("Run step is a card that runs only on Yes; a pick has Undo", async () => {
    const a = take({ title: "seed 1" });
    const b = take({ title: "seed 2" });
    const picks: Record<string, string | undefined> = {};
    let step = "still";
    const run = vi.fn(async () => ["r1", "r2"]);
    const live = (): StudioLive => ({
      title: "Fox at night",
      step,
      steps: ["still", "edit"],
      label: (id) => (id === "still" ? "Qwen Image" : "Qwen edit"),
      nums: { still: 1, edit: 2 },
      seeds: 4,
      missing: step === "edit" && !picks.still ? ["still"] : [],
      estimate: "Mac · about 40 s",
      picks,
      takesOf: (id) => (id === "still" ? [a, b] : []),
      states: {},
      choose: (id) => (step = id),
      pick: (s, t) => (picks[s] = t),
      run,
    });
    const spec = () => studioTools(live(), live);
    expect(spec().sees).toEqual(["Pipeline · Studio", "Fox at night", "Step 1 · Qwen Image"]);
    const call = (name: string, args: Record<string, unknown>) =>
      Promise.resolve(spec().actions[name](args, ctx));
    expect(out(await call("runStep", { node: "edit" }))).toMatchObject({
      status: "failed",
      text: "Run step 2 · pick a take from step 1 first",
    });
    expect(out(await call("pickTake", { node: "still", take_id: "x" }))).toMatchObject({
      status: "failed",
    });
    const p = out(await call("pickTake", { node: "still", take_id: b.id }));
    expect(picks.still).toBe(b.id);
    await p.undo!.undo();
    expect(picks.still).toBeUndefined();
    await p.undo!.redo!();
    const o = card(await call("runStep", { node: "edit" }));
    expect(o.card).toMatchObject({
      kind: "generate",
      title: "Run step 2?",
      tags: ["uses the GPU"],
      est: "Mac · about 40 s",
      go: "RUN STEP 2",
    });
    expect(run).not.toHaveBeenCalled();
    expect(await o.yes()).toMatchObject({ status: "done", result: { run_ids: ["r1", "r2"] } });
    expect(run).toHaveBeenCalledTimes(1);
  });
});

describe("a batch's actions", () => {
  const row = (n: number, over: Partial<S<"BatchRow">> = {}) =>
    ({
      row: n,
      status: "succeeded",
      attempt: 1,
      take_ids: [],
      inputs: {},
      ...over,
    }) as S<"BatchRow">;
  function batch(over: Partial<S<"Batch">> = {}) {
    const b = {
      ...base,
      pipeline_id: remote.id,
      pipeline_title: remote.title,
      pipeline_version: 3,
      status: "paused",
      control: "pause",
      counts: { ...base.counts, total: 10, succeeded: 4, failed: 1, cancelled: 0 },
      ...over,
    } as S<"Batch">;
    const control = vi.fn(async () => b);
    const retry = vi.fn(async (n: number) => row(n, { status: "queued" }));
    const again = vi.fn(async () => ({ ...b, id: "b2" }));
    const spec = batchTools({
      view: { batch: b, rows: [row(1), row(2, { status: "failed", error: "out of memory" })] },
      pipeline: remote,
      control,
      retry,
      again,
    });
    const call = (name: string, args: Record<string, unknown> = {}) =>
      Promise.resolve(spec.actions[name](args, ctx));
    return { call, control, retry, again };
  }

  it("resume, retry and again are Batch cards with what leaves the machine; nothing runs before Yes", async () => {
    const { call, control, retry } = batch();
    expect(out(await call("pause"))).toMatchObject({ status: "failed" });
    const r = card(await call("resume"));
    expect(r.card).toMatchObject({
      kind: "batch",
      title: "Resume the batch?",
      tags: ["uses the GPU", "sends to DeepSeek"],
      go: "RESUME",
    });
    expect(r.card.note).toMatch(/^What leaves the machine: Decide sends/);
    expect(r.card.rows[1][1]).toMatch(/^5 to go · at most/);
    expect(control).not.toHaveBeenCalled();
    await r.yes();
    expect(control).toHaveBeenCalledWith("resume");
    expect(out(await call("retry", { row: 1 }))).toMatchObject({ status: "failed" });
    expect(out(await call("retry", { row: 2, from_step: "two" }))).toMatchObject({
      status: "failed",
    });
    const t = card(await call("retry", { row: 2, from_step: "2" }));
    expect(t.card.rows).toContainEqual(["From", "step 2"]);
    expect(retry).not.toHaveBeenCalled();
    await t.yes();
    expect(retry).toHaveBeenCalledWith(2, 2);
    expect(out(await call("again"))).toMatchObject({ status: "failed" });
  });

  it("stop is a card that can't be undone; again opens the new batch; pause is a line", async () => {
    const run = batch({ status: "running", control: "run" });
    const s = card(await run.call("stop"));
    expect(s.card).toMatchObject({ kind: "stop", danger: true, go: "STOP" });
    expect(s.card.fine).toMatch(/can't be undone/);
    expect(run.control).not.toHaveBeenCalled();
    await s.yes();
    expect(run.control).toHaveBeenCalledWith("stop");
    expect(out(await run.call("pause"))).toMatchObject({
      status: "done",
      text: "Paused the batch",
    });
    const ended = batch({ status: "done", control: "run" });
    const a = card(await ended.call("again"));
    expect(a.card).toMatchObject({ title: "Run the batch again?", go: "RUN AGAIN" });
    expect(ended.again).not.toHaveBeenCalled();
    expect(await a.yes()).toMatchObject({ result: { batch_id: "b2" } });
    expect(ended.again).toHaveBeenCalledWith("same");
  });

  it("a gate's pick is a card naming the row and the take", async () => {
    const item = {
      row: 3,
      attempt: 1,
      gate: "review",
      gate_title: "Pick one still",
      visit: 1,
      keep: 1,
      kind: "gate",
      inputs: {},
      candidates: [
        { take_id: "t1", kind: "image", thumb_url: "/t1.jpg" },
        { take_id: "t2", kind: "image", thumb_url: "/t2.jpg" },
      ],
    } as unknown as S<"BatchReviewItem">;
    const pick = vi.fn(async () => undefined);
    const spec = reviewTools({ batch: base, items: [item], pick });
    const call = (args: Record<string, unknown>) => Promise.resolve(spec.actions.pick(args, ctx));
    expect(out(await call({ row: 9, take_ids: ["t1"] }))).toMatchObject({ status: "failed" });
    expect(out(await call({ row: 3, take_ids: ["zz"] }))).toMatchObject({ status: "failed" });
    expect(out(await call({ row: 3, take_ids: ["t1", "t2"] }))).toMatchObject({ status: "failed" });
    const o = card(await call({ row: 3, take_ids: ["t2"] }));
    expect(o.card).toMatchObject({
      kind: "pick",
      title: "Pick for row 3?",
      tags: ["uses the GPU"],
      thumbs: ["/t2.jpg"],
      go: "PICK",
    });
    expect(o.card.rows).toContainEqual(["Take", "take 2 of 2"]);
    expect(pick).not.toHaveBeenCalled();
    await o.yes();
    expect(pick).toHaveBeenCalledWith(item, ["t2"]);
  });
});

describe("New batch on the Pipelines list", () => {
  it("newBatch opens and fills it with Undo; start is a card and Submit runs only on Yes", async () => {
    const posts: unknown[] = [];
    answer("GET", {});
    answer("POST", {
      "/v1/studio/pipelines/validate": () => ({
        data: { problems: [], can_save: true, egress: [], reserved: [] },
      }),
      "/v1/studio/batches": ({ body }) => {
        posts.push(body);
        return { status: 201, data: { ...base, id: "nb1" } };
      },
    });
    const reg = new Registry();
    reg.setPath("/pipelines");
    const { Pipelines } = await import("@/components/pipelines/pipelines");
    render(
      <ToolsContext value={reg}>
        <Pipelines
          initial={{ batches: [], next_cursor: null }}
          pipelines={[plain]}
          workflows={[]}
        />
      </ToolsContext>,
    );
    let n = 0;
    const call = async (name: string, args: Record<string, unknown> = {}) =>
      (await act(() =>
        reg
          .run(
            { id: `11111111-2222-4333-8444-555555555555:1:${n++}`, name, args: args as never },
            () => undefined,
          )
          .then((r) => r.out),
      )) as Outcome | Asked;
    expect(reg.current()?.spec.sees).toEqual(["Pipelines", "Fox at night"]);
    expect(out(await call("start"))).toMatchObject({ status: "failed" });
    // opened by the action itself (outside act: its effects must run while it waits for the draft)
    const bad = await reg.run(
      {
        id: "11111111-2222-4333-8444-555555555555:1:99",
        name: "newBatch",
        args: { rows: [{ colour: "red" }] } as never,
      },
      () => undefined,
    );
    expect(out(bad.out).text).toMatch(/^New batch · no input colour: prompt, edit/);
    await screen.findByRole("dialog", { name: "New batch" });
    const fill = out(
      await call("newBatch", {
        rows: [
          { prompt: "a fox", edit: "make it night" },
          { prompt: "a cat", edit: "make it rain" },
        ],
      }),
    );
    expect(fill).toMatchObject({ status: "done", text: "Filled New batch · 2 rows" });
    await waitFor(() =>
      expect(
        (reg.summary().fields as unknown as { new_batch: { rows: number } }).new_batch.rows,
      ).toBe(2),
    );
    const s = card(await call("start"));
    expect(s.card).toMatchObject({
      kind: "batch",
      title: "Start the batch?",
      go: "START BATCH",
      fine: "Same as Submit on New batch. Change the rows there first if you like.",
    });
    expect(s.card.rows[0]).toEqual(["Pipeline", "Fox at night"]);
    expect(posts).toEqual([]);
    expect(await act(() => s.yes())).toMatchObject({ status: "done", result: { batch_id: "nb1" } });
    expect(posts).toHaveLength(1);
    expect(posts[0]).toMatchObject({ pipeline_id: plain.id });
    expect(push).toHaveBeenCalledWith("/pipelines/batches/nb1");
    await act(async () => void (await fill.undo!.undo()));
  });
});
