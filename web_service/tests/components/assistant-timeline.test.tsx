// @vitest-environment jsdom
// The Timeline's screen actions for the assistant (components/timeline/assistant.ts; plan 18 §22.5, §22.6; board
// frame 2), run through the registry as the drawer runs them: the playhead and range are lines; edits are the Op union
// saved through POST …/edits with the revision they were made on, labelled `assistant` (plan 24; a 409 reloads and
// saves once more), and Undo is the sequence's own undo; the screen's own gestures save through /edits too (`web`);
// a refused edit says why; New shot, a take action, Re-render, Export and Delete are cards that send nothing until
// Yes (Yes is the docked form's own Generate); Save as voice opens Clone from a clip and resolves "cancelled".
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import fixture from "../../../contracts/fixtures/http/Timeline.valid.json";
import planFixture from "../../../contracts/fixtures/http/ExportPlan.valid.json";
import { Registry, ToolsContext } from "@/components/assistant/tools";
import { asked, type Asked, type Outcome } from "@/lib/assistant";
import { api, type S } from "@/lib/api";
import { editText } from "@/components/timeline/assistant";
import { apply, factsOf, type Op } from "@/lib/timeline";
import { answer, take } from "./helpers";

const replace = vi.fn();
vi.mock("next/navigation", () => ({
  useRouter: () => ({ push: vi.fn(), replace, refresh: vi.fn() }),
  usePathname: () => "/timeline",
  useSearchParams: () => new URLSearchParams(),
}));
vi.mock("next/link", () => ({
  default: ({ href, children, ...rest }: { href: string; children: React.ReactNode }) => (
    <a href={href} {...rest}>
      {children}
    </a>
  ),
}));
vi.mock("@/lib/runs", async (orig) => ({
  ...(await orig<typeof import("@/lib/runs")>()),
  followSession: () => () => undefined,
}));

beforeAll(() => {
  HTMLDialogElement.prototype.showModal ??= function (this: HTMLDialogElement) {
    this.open = true;
  };
  HTMLDialogElement.prototype.close ??= function (this: HTMLDialogElement) {
    this.open = false;
    this.dispatchEvent(new Event("close"));
  };
  HTMLElement.prototype.hidePopover ??= () => undefined;
  Element.prototype.scrollIntoView ??= () => undefined;
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  replace.mockReset();
});

const { TimelineEditor } = await import("@/components/timeline/editor");

const P = (name: string, type: string, more: object = {}) => ({ name, type, ...more });
const t2v = {
  id: "ltx-t2v",
  title: "LTX Text to video",
  family: "ltx",
  media: "video",
  source: "template",
  nodes: {
    prompt: { kind: "ltx/prompt" },
    model: { kind: "ltx/model" },
    sample: { kind: "ltx/sample", in: { cond: "prompt.cond", model: "model.model" } },
  },
  node_schemas: {
    "ltx/prompt": {
      title: "Prompt",
      params: [P("text", "text", { default: "" })],
      outputs: [{ name: "cond", type: "Conditioning" }],
    },
    "ltx/model": {
      title: "LTX-2.5",
      params: [P("variant", "enum", { default: "distilled", enum: ["distilled", "dev"] })],
      outputs: [{ name: "model", type: "Model" }],
    },
    "ltx/sample": {
      title: "Sample",
      params: [
        P("seed", "seed", { default: 42 }),
        P("frames", "frames", { default: 49, min: 9, step: 8, max: 321, fps: "fps" }),
        P("fps", "int", { default: 24 }),
      ],
      outputs: [{ name: "latent", type: "Latent" }],
    },
  },
} as unknown as S<"Workflow">;

const TL = fixture as unknown as S<"Timeline">;
const plan = planFixture as unknown as S<"ExportPlan">;
const reply = (data: unknown, status = 200) =>
  Promise.resolve({
    data: status < 300 ? data : undefined,
    error: status < 300 ? undefined : { detail: "changed" },
    response: new Response(null, { status }),
  });

/** What /edits got: the body and its X-Helm-Client. */
type Sent = S<"TimelineEdits"> & { via?: string };

function start({
  workflows = [t2v] as S<"Workflow">[],
  conflict = false,
  // what a parallel write does to the stored sequence before the 409
  meanwhile = (t: S<"Timeline">) => t,
} = {}) {
  let cur: S<"Timeline"> = structuredClone(TL);
  const edits: Sent[] = [];
  const posts: { path: string; body: unknown }[] = [];
  const deletes: string[] = [];
  let conflicts = conflict ? 1 : 0;
  answer("GET", {
    "/v1/studio/timelines": () => ({ data: { timelines: [cur] } }),
    "/v1/studio/timelines/{timeline_id}": () => ({ data: cur }),
    "/v1/studio/workflows": () => ({ data: { workflows } }),
    "/v1/studio/takes": () => ({ data: { takes: [], next_cursor: null } }),
    "/v1/studio/takes/{take_id}": () => ({
      data: take({
        id: "aaaaaaaa-0000-4000-8000-000000000001",
        kind: "video",
        title: "a fox trots in",
        video: { duration_s: 6, fps: 24, frames: 144, has_audio: true, poster_url: "" },
      } as Partial<S<"Take">>),
    }),
    "/v1/studio/library": () => ({ data: { items: [] } }),
    "/v1/studio/hosts": () => ({ data: { hosts: [] } }),
    "/v1/studio/models": () => ({ data: { hosts: [], licences: [], models: [] } }),
    "/v1/studio/queue": () => ({ data: { runs: [], hosts: [] } }),
    "/v1/studio/timelines/{timeline_id}/clips/{clip_id}/actions": () => ({
      data: { actions: [] },
    }),
  });
  vi.spyOn(api, "POST").mockImplementation(((
    path: string,
    init: { body?: unknown; headers?: Record<string, string> },
  ) => {
    if (path.endsWith("/edits")) {
      // studio's apply, played by lib/timeline (the three are held equal by make contracts-check)
      const body = init.body as S<"TimelineEdits">;
      edits.push({ ...body, via: init.headers?.["X-Helm-Client"] });
      if (conflicts) {
        conflicts--;
        cur = { ...meanwhile(cur), revision: cur.revision + 1 };
        return reply(undefined, 409);
      }
      if (body.base_revision !== cur.revision) return reply(undefined, 409);
      let d: S<"Timeline"> = cur;
      for (const o of body.ops) {
        const r = apply(d, o as Op, factsOf(cur.takes));
        if (!("document" in r)) return reply(undefined, 422);
        d = { ...d, ...r.document } as S<"Timeline">;
      }
      cur = { ...d, revision: cur.revision + 1, can_undo: true };
      return reply({ revision: cur.revision, dry_run: false, timeline: cur, diff: {}, warnings: [] });
    }
    posts.push({ path, body: init?.body });
    if (path.endsWith("/export/plan")) return reply({ ...plan, revision: cur.revision });
    if (path === "/v1/studio/runs/plan") return reply({ host: "mac", peak_gb: 21, reason: null });
    if (path.endsWith("/undo") || path.endsWith("/redo")) {
      cur = { ...cur, revision: cur.revision + 1 };
      return reply(cur);
    }
    if (path.endsWith("/shots")) {
      const slot = (init.body as { slot: { id?: string; at?: number; length?: number } }).slot;
      cur = {
        ...cur,
        revision: cur.revision + 1,
        tracks: cur.tracks.map((t, i) =>
          i
            ? t
            : {
                ...t,
                clips: [
                  ...t.clips,
                  {
                    id: slot.id!,
                    take_id: null,
                    hold: slot.length ?? 2,
                    shot: {
                      run_id: "dddddddd-0000-4000-8000-000000000001",
                      status: "queued",
                      error: null,
                    },
                  } as S<"TimelineClip">,
                ],
              },
        ),
      };
      return reply({ timeline: cur, run: {} }, 202);
    }
    if (path.endsWith("/export"))
      return reply({ id: "9e4d2c1b-3a5f-4e6d-8c7b-0a1b2c3d4e5f", status: "queued" }, 202);
    if (path.endsWith("/rerender")) return reply({ timeline: cur, batch: { id: "b1" } }, 202);
    if (path === "/v1/studio/timelines")
      return reply({ ...cur, id: "11111111-0000-4000-8000-000000000009", name: "Bus stop" }, 201);
    if (path.endsWith("/cancel")) return reply({ id: "j", status: "cancelled" });
    return reply(undefined, 404);
  }) as never);
  vi.spyOn(api, "PATCH").mockImplementation(((_: string, init: { body: { name: string } }) => {
    cur = { ...cur, name: init.body.name };
    return reply(cur);
  }) as never);
  vi.spyOn(api, "DELETE").mockImplementation(((
    _: string,
    init: { params: { path: { timeline_id: string } } },
  ) => {
    deletes.push(init.params.path.timeline_id);
    return reply(undefined, 204);
  }) as never);
  const reg = new Registry();
  reg.setPath("/timeline");
  render(
    <ToolsContext value={reg}>
      <TimelineEditor id={TL.id} />
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
  // an action that waits for the screen to render (a docked form opening) runs outside act, as in the browser
  const live = (name: string, args: Record<string, unknown> = {}) =>
    reg
      .run(
        { id: `11111111-2222-4333-8444-555555555555:1:${n++}`, name, args: args as never },
        () => undefined,
      )
      .then((r) => r.out);
  return { reg, call, live, edits, posts, deletes, now: () => cur };
}
const out = (o: Outcome | Asked) => {
  if (asked(o)) throw new Error("a card");
  return o;
};
const card = (o: Outcome | Asked) => {
  if (!asked(o)) throw new Error(`no card: ${o.text}`);
  return o;
};
const ready = () => screen.findByRole("heading", { name: `Timeline: ${TL.name}` });

describe("the Timeline's actions", () => {
  it("registers its Sees chips and summary; the playhead and the range are lines", async () => {
    const { reg, call } = start();
    await ready();
    expect(reg.current()?.spec.sees).toEqual([`Timeline · ${TL.name}`]);
    const f = reg.summary().fields as unknown as Record<string, unknown>;
    expect(f).toMatchObject({ sequence: { id: TL.id, name: TL.name }, size: "256 × 128 · 24 fps" });
    expect(out(await call("setPlayhead", { at_s: 3 }))).toMatchObject({
      text: "Playhead at 0:03.00",
    });
    expect(screen.getByText("0:03.00")).toBeTruthy();
    expect(out(await call("setPlayhead", { at_s: 99 }))).toMatchObject({ status: "failed" });
    expect(out(await call("setRange", { in_s: 1, out_s: 2 }))).toMatchObject({
      text: "Range 0:01.00 – 0:02.00",
    });
    expect(screen.getByText(/^Range 0:01\.00 – 0:02\.00/)).toBeTruthy();
    expect(reg.current()?.spec.sees).toContain("Range 0:01 – 0:02");
    expect(out(await call("setRange", { in_s: 2, out_s: 1 }))).toMatchObject({ status: "failed" });
  });

  it("an edit is saved with its revision; Undo and Redo are the sequence's own", async () => {
    const { call, edits, posts, now } = start();
    await ready();
    const o = out(
      await call("edit", { ops: [{ op: "trim", clip: "c", edge: "out", delta: -0.25 }] }),
    );
    expect(o).toMatchObject({ status: "done", text: "Trimmed 1 clip" });
    expect(edits).toEqual([
      {
        base_revision: 4,
        ops: [{ op: "trim", clip: "c", edge: "out", delta: -0.25 }],
        via: "assistant",
      },
    ]);
    const c = now().tracks[0].clips.find((x) => x.id === "c")!;
    expect(c.out).toBeCloseTo(1.5);
    await act(async () => void (await o.undo!.undo()));
    expect(posts.at(-1)).toMatchObject({
      path: "/v1/studio/timelines/{timeline_id}/undo",
      body: { revision: 5 },
    });
    await act(async () => void (await o.undo!.redo()));
    expect(posts.at(-1)).toMatchObject({
      path: "/v1/studio/timelines/{timeline_id}/redo",
      body: { revision: 6 },
    });
  });

  it("a conflict reloads and saves once more; a refused edit says why and saves nothing", async () => {
    const { call, edits } = start({ conflict: true });
    await ready();
    const o = out(await call("edit", { ops: [{ op: "remove", clip: "b" }] }));
    expect(o).toMatchObject({ status: "done", text: "Removed 1 clip" });
    expect(edits.map((p) => p.base_revision)).toEqual([4, 5]);
    expect(screen.getByText(/reloaded, and the edit applied to it/)).toBeTruthy();
    const bad = out(await call("edit", { ops: [{ op: "split", track: "V1", at: 0 }] }));
    expect(bad.status).toBe("failed");
    expect(bad.text).toMatch(/^Edit · /);
    expect(edits).toHaveLength(2);
  });

  it("a gesture saves its op through /edits, labelled web; the answer is the sequence shown", async () => {
    const { call, edits, now } = start();
    await ready();
    await call("setPlayhead", { at_s: 1 });
    fireEvent.keyDown(document.body, { key: "s" });
    await waitFor(() => expect(edits).toHaveLength(1));
    expect(edits[0]).toMatchObject({
      base_revision: 4,
      ops: [{ op: "split", track: "V1", at: 1 }],
      via: "web",
    });
    expect(edits[0].ops[0]).toHaveProperty("id");
    await waitFor(() => expect(now().revision).toBe(5));
    expect(now().tracks[0].clips).toHaveLength(5);
  });

  it("a gesture's 409 reloads and re-applies once; when it no longer applies the notice says so", async () => {
    const a = start({ conflict: true });
    await ready();
    await a.call("setPlayhead", { at_s: 1 });
    fireEvent.keyDown(document.body, { key: "s" });
    await waitFor(() => expect(a.edits.map((p) => p.base_revision)).toEqual([4, 5]));
    expect(await screen.findByText(/reloaded, and your edit applied to it/)).toBeTruthy();
    cleanup();
    vi.restoreAllMocks();
    // a parallel write left only the running shot on V1: the split no longer applies, nothing more is sent
    const b = start({
      conflict: true,
      meanwhile: (t) => ({
        ...t,
        tracks: t.tracks.map((k, i) => (i ? k : { ...k, clips: k.clips.filter((c) => c.id === "s") })),
      }),
    });
    await ready();
    await b.call("setPlayhead", { at_s: 1 });
    fireEvent.keyDown(document.body, { key: "s" });
    expect(await screen.findByText(/reloaded, and your edit applied to it/)).toBeTruthy();
    expect(b.edits).toHaveLength(1);
  });

  it("New shot opens the composer prefilled and is a card: nothing runs until Yes, which is its Generate", async () => {
    const { live, posts, reg } = start();
    await ready();
    const a = card(
      await live("shot", { kind: "new", at_s: 1, seconds: 2, prompt: "Wide: rain at a bus stop" }),
    );
    expect(a.card).toMatchObject({
      kind: "generate",
      title: "New shot 1?",
      tags: ["uses the GPU"],
      go: "GENERATE",
    });
    expect(a.card.rows).toContainEqual(["Size", "256 × 128 · 24 fps"]);
    expect(a.card.est).toContain("21 GB");
    expect(a.card.quote).toBe("Wide: rain at a bus stop");
    const box = screen.getByRole("region", { name: "New shot" });
    expect((within(box).getByLabelText("Shot prompt") as HTMLTextAreaElement).value).toBe(
      "Wide: rain at a bus stop",
    );
    expect(reg.current()?.spec.sees).toContain("New shot");
    expect(posts.some((p) => p.path.endsWith("/shots"))).toBe(false);
    const o = await a.yes();
    expect(o).toMatchObject({ status: "done", text: "You clicked GENERATE · New shot 1" });
    expect(o.result).toMatchObject({ run_id: "dddddddd-0000-4000-8000-000000000001" });
    const shot = posts.find((p) => p.path.endsWith("/shots"))!.body as {
      run: S<"RunRequest">;
      slot: { at: number };
    };
    expect(shot.slot.at).toBe(1);
    expect(shot.run.inputs).toMatchObject({ "prompt.text": "Wide: rain at a bus stop" });
  });

  it("a take action the clip does not offer is refused; one needs a video clip", async () => {
    const { call, live } = start();
    await ready();
    expect(out(await live("shot", { kind: "extend", clip: "a" }))).toMatchObject({
      status: "failed",
      text: "Shot · that clip offers no extend",
    });
    expect(out(await call("shot", { kind: "retake", clip: "b" }))).toMatchObject({
      status: "failed",
      text: "Shot · retake works on a video clip",
    });
    expect(out(await call("shot", { kind: "new", clip: "a" }))).toMatchObject({ status: "failed" });
  });

  it("with no video workflow New shot says so; New line with no voice workflow too", async () => {
    const { live } = start({ workflows: [] });
    await ready();
    expect(out(await live("shot", { kind: "new" }))).toMatchObject({
      text: "Shot · no video workflow is installed",
    });
    expect(out(await live("newLine", { text: "Hello" }))).toMatchObject({
      text: "New line · no voice workflow is installed",
    });
  });

  it("Export and Re-render are cards; Yes posts what the screen's buttons post", async () => {
    const { call, posts } = start();
    await ready();
    const e = card(await call("export"));
    expect(e.card).toMatchObject({
      kind: "export",
      title: `Export ${TL.name}?`,
      tags: ["uses the machine"],
    });
    expect(e.card.rows).toContainEqual(["Mode", "re-encode"]);
    expect(posts.some((p) => p.path.endsWith("/export"))).toBe(false);
    const done = await act(() => e.yes());
    expect(done.result).toMatchObject({ job_id: "9e4d2c1b-3a5f-4e6d-8c7b-0a1b2c3d4e5f" });
    expect(posts.find((p) => p.path.endsWith("/export"))!.body).toEqual({ revision: 4 });
    expect(out(await call("export", { preset: "4k" }))).toMatchObject({ status: "failed" });
    const r = card(await call("rerender"));
    expect(r.card).toMatchObject({ kind: "batch", title: "Re-render the sequence?" });
    expect(posts.some((p) => p.path.endsWith("/rerender"))).toBe(false);
    expect((await act(() => r.yes())).result).toEqual({ batch_id: "b1" });
    expect(posts.find((p) => p.path.endsWith("/rerender"))!.body).toEqual({ revision: 4 });
    expect(
      out(await call("cancelExport", { job_id: "9e4d2c1b-3a5f-4e6d-8c7b-0a1b2c3d4e5f" })),
    ).toMatchObject({
      text: "Cancelled the export",
    });
  });

  it("New sequence and rename are lines; Delete is a card that deletes nothing until Yes", async () => {
    const { call, deletes } = start();
    await ready();
    expect(out(await call("renameSequence", { title: "Bus stop" }))).toMatchObject({
      text: "Renamed: Bus stop",
    });
    const d = card(await call("deleteSequence"));
    expect(d.card).toMatchObject({ kind: "delete", danger: true, title: "Delete Bus stop?" });
    expect(deletes).toEqual([]);
    await act(() => d.yes());
    expect(deletes).toEqual([TL.id]);
    expect(replace).toHaveBeenCalledWith("/timeline");
    const n = out(await call("newSequence", { title: "Bus stop" }));
    expect(n.result).toEqual({ timeline_id: "11111111-0000-4000-8000-000000000009" });
  });

  it("Save as voice opens Clone from a clip with the clip's take; closing it is cancelled", async () => {
    const { reg } = start();
    await ready();
    const lines: string[] = [];
    let o: Promise<{ out: Outcome | Asked }> | undefined;
    act(() => {
      o = reg.run(
        {
          id: "11111111-2222-4333-8444-555555555555:1:90",
          name: "saveAsVoice",
          args: { clip: "m" } as never,
        },
        (t) => void lines.push(t),
      );
    });
    const dialog = await screen.findByRole("dialog", { name: "Clone from a clip" });
    expect(lines).toEqual(["Waiting for you · Save in Clone a voice"]);
    fireEvent.click(within(dialog).getByRole("button", { name: "Close" }));
    await waitFor(async () =>
      expect((await o!).out).toMatchObject({
        status: "cancelled",
        text: "Clone a voice · cancelled",
      }),
    );
  });
});

describe("editText", () => {
  it("says the edits in runs, as the board's lines", () => {
    expect(
      editText([
        { op: "place", at: 12, hold: 2, id: "x", gap_id: "y" },
        { op: "place", at: 14, hold: 3, id: "z", gap_id: "w" },
        { op: "trim", clip: "a", edge: "in", delta: 0.5 },
      ]),
    ).toBe("Placed 2 slots at 0:12 · Trimmed 1 clip");
  });
});
