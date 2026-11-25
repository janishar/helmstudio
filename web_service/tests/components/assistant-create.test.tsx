// @vitest-environment jsdom
// Create's screen actions for the assistant (components/create/assistant.ts; plan 18 §22.4, §22.5, §22.6), run
// through the registry as the drawer runs them: change refuses what the form greys or hides, with its reason; Run on
// picks only a machine the form allows, with a reason, and Undo; seeds, select, star, stop, switchTo, view, new
// session; Generate is a card naming the machine and place in line and queues nothing until its Yes; templates
// refuse rename and delete; the Sees chips and the summary.
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import { act, cleanup, render, screen, waitFor, within } from "@testing-library/react";
import ltxWorkflows from "../../../contracts/fixtures/registry/ltx.workflows.valid.json";
import ltxNodes from "../../../contracts/fixtures/registry/ltx.nodes.valid.json";
import coreNodes from "../../../contracts/fixtures/registry/nodes.valid.json";
import ttsWorkflows from "../../../contracts/fixtures/registry/qwen3-tts.workflows.valid.json";
import ttsNodes from "../../../contracts/fixtures/registry/qwen3-tts.nodes.valid.json";
import { Create } from "@/components/create/create";
import { Registry, ToolsContext } from "@/components/assistant/tools";
import { idOf } from "@/components/create/form";
import { leftForm } from "@/lib/workflow";
import { asked, type Asked, type Outcome } from "@/lib/assistant";
import { api, type S } from "@/lib/api";
import { answer, take } from "./helpers";

vi.mock("@/lib/runs", async (orig) => ({
  ...(await orig<typeof import("@/lib/runs")>()),
  followSession: () => () => undefined,
}));

beforeAll(() => {
  globalThis.requestAnimationFrame ??= (f: FrameRequestCallback) =>
    setTimeout(() => f(0), 0) as unknown as number;
  HTMLElement.prototype.hidePopover ??= () => undefined;
  Element.prototype.scrollIntoView ??= () => undefined;
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  localStorage.clear();
});

const schemas = Object.fromEntries(
  [...coreNodes.nodes, ...ltxNodes.nodes].map((n) => [n.kind, n]),
) as unknown as S<"Workflow">["node_schemas"];
const workflow = {
  ...ltxWorkflows.workflows.find((w) => w.id === "ltx-text-to-video"),
  env: "torch",
  node_schemas: schemas,
  source: "template",
} as unknown as S<"Workflow">;
const variant = (id: string, title: string, devices: string[], on: string) => ({
  id,
  title,
  devices,
  precisions: ["bf16", "int8"],
  dir: id,
  licence: null,
  gated: false,
  download_bytes: null,
  components: [],
  hosts: [{ host: on, state: "present" }],
});
const modelHost = (h: string) => ({
  host: h,
  live: true,
  envs: ["torch"],
  models_root: null,
  free_bytes: null,
  total_bytes: null,
});
const models = {
  hosts: [modelHost("gcp"), modelHost("mac")],
  licences: [],
  models: [
    {
      id: "ltx-2.5",
      family: "ltx",
      title: "LTX-2.5",
      media: ["video"],
      runtimes: [{ runtime: "torch", env: "torch" }],
      variants: [
        variant("distilled", "Distilled", ["mps"], "mac"),
        variant("distilled-fp8", "Distilled fp8", ["cuda"], "gcp"),
      ],
    },
  ],
};
const host = (name: string, kind: string, device: string, state: string) => ({
  host: name,
  env: "torch",
  state,
  updated_at: "2026-11-22T00:00:00Z",
  device: { kind, name: device, memory_total_gb: 96 },
  host_memory: { budget_gb: 64, used_gb: 0 },
});
const RUN = "00000000-0000-4000-8000-0000000000aa";

function start({
  gcp = "idle",
  takes = [] as S<"Take">[],
  runs = [] as S<"Run">[],
  list = models as unknown,
  reuse = undefined as string | undefined,
  workflows = [workflow],
} = {}) {
  const queued: Record<string, unknown>[] = [];
  const patches: unknown[] = [];
  const posts: string[] = [];
  answer("GET", {
    "/v1/studio/sessions/{session_id}/runs": () => ({ data: { runs } }),
    "/v1/studio/sessions/{session_id}/takes": () => ({ data: { takes } }),
    "/v1/studio/library": () => ({ data: { items: [] } }),
    "/v1/studio/loras": () => ({ data: { loras: [] } }),
    "/v1/studio/models": () => ({ data: typeof list === "function" ? list() : list }),
    "/v1/studio/queue": () => ({
      data: { runs: [], hosts: [{ host: "mac", state: "idle", others_queued: 1 }] },
    }),
    "/v1/studio/hosts": () => ({
      data: {
        hosts: [
          host("mac", "mps", "Apple M5 Pro", "idle"),
          host("gcp", "cuda", "RTX PRO 6000", gcp),
        ],
      },
    }),
  });
  answer("POST", {
    "/v1/studio/runs/plan": () => ({ data: { host: "mac", peak_gb: 24, reason: null } }),
    "/v1/studio/takes/{take_id}/reuse": () => ({
      data: { workflow_id: workflow.id, inputs: { "prompt.text": "reused words" }, looks: [] },
    }),
    "/v1/studio/sessions": () => {
      posts.push("session");
      return { status: 201, data: { id: "s2", title: "Untitled", created_at: "", updated_at: "" } };
    },
    "/v1/studio/runs/{run_id}/cancel": () => {
      posts.push("cancel");
      return { data: { ...runs[0], status: "cancelled" } };
    },
    "/v1/studio/sessions/{session_id}/runs": ({ body }) => {
      queued.push(body as Record<string, unknown>);
      return {
        status: 202,
        data: {
          id: `r${queued.length}`,
          session_id: "s1",
          workflow_id: workflow.id,
          inputs: {},
          status: "queued",
          progress: null,
          error: null,
          outputs: [],
          host: null,
        },
      };
    },
  });
  vi.spyOn(api, "PATCH").mockImplementation(((
    _p: string,
    init: { body: unknown; params: { path: { take_id: string } } },
  ) => {
    patches.push(init.body);
    return Promise.resolve({
      data: { ...takes.find((t) => t.id === init.params.path.take_id), ...(init.body as object) },
      error: undefined,
      response: new Response(null, { status: 200 }),
    });
  }) as never);
  const reg = new Registry();
  reg.setPath("/");
  render(
    <ToolsContext value={reg}>
      <Create
        reuse={reuse}
        initial={{
          sessions: [{ id: "s1", title: "Rain", created_at: "", updated_at: "" }],
          workflows,
          sessionId: "s1",
          runs,
          takes,
        }}
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
  const composer = () => screen.getByRole("form", { name: "Composer" });
  return { reg, call, queued, patches, posts, composer };
}
const out = (o: Outcome | Asked) => {
  if (asked(o)) throw new Error("a card");
  return o;
};
const undo = (o: Outcome | Asked) => act(async () => void (await out(o).undo!.undo()));

describe("Create's actions", () => {
  it("registers its Sees chips, ids and summary (Run on with each machine's reason)", async () => {
    const { reg } = start({ gcp: "offline" });
    await waitFor(() => expect(reg.current()?.spec.sees).toContain("Run on: Auto → Mac"));
    const spec = reg.current()!.spec;
    expect(spec.sees.slice(0, 2)).toEqual(["Create · Video", "LTX-2.5 · Distilled"]);
    expect(spec.sees).toContain("Rain");
    const s = reg.summary();
    expect(s.ids).toEqual({ session_id: "s1", workflow_id: "ltx-text-to-video" });
    expect(s.actions).toContain("generate");
    expect(s.actions).toEqual(expect.arrayContaining(["saveAsLook", "cloneVoice", "hear"]));
    const f = () => reg.summary().fields as unknown as Record<string, Record<string, unknown>>;
    await waitFor(() =>
      expect(f().run_on).toEqual({
        chosen: "auto",
        machines: [
          { host: "mac", label: "Mac", enabled: true },
          { host: "gcp", label: "NVIDIA", enabled: false, reason: "offline" },
        ],
      }),
    );
    expect((f().fields as unknown as { key: string }[]).some((x) => x.key === "prompt.text")).toBe(
      true,
    );
    // the workflows switchTo takes, by title, so the model never guesses a name such as "Text to image"
    expect(f().workflows).toEqual([
      {
        id: "ltx-text-to-video",
        title: workflow.title,
        media: workflow.media ?? "image",
        downloaded: true,
      },
    ]);
  });

  it("change writes the prompt with Undo, and refuses a setting it does not have or a value not offered", async () => {
    const { call } = start();
    const box = () => document.getElementById(idOf("prompt.text")) as HTMLTextAreaElement;
    const o = await call("change", { key: "prompt.text", value: "rain on a bus stop" });
    expect(out(o)).toMatchObject({ status: "done", text: "Wrote prompt" });
    expect(box().value).toBe("rain on a bus stop");
    await undo(o);
    expect(box().value).toBe("");
    expect(out(await call("change", { key: "nope.x", value: 1 }))).toMatchObject({
      status: "failed",
      text: "nope.x: no such setting",
    });
    expect(out(await call("change", { key: "model.variant", value: "dev" }))).toMatchObject({
      status: "failed",
    });
  });

  it("says a clip's length in seconds; the longest refused in seconds, the model also told frames", async () => {
    const { call } = start();
    expect(out(await call("change", { key: "duration.frames", value: 121 }))).toMatchObject({
      status: "done",
      text: "Set Length: 5.0 s",
    });
    expect(out(await call("change", { key: "duration.frames", value: 401 }))).toMatchObject({
      status: "failed",
      text: "Length: the longest is 13.4 s",
      error: "Length: the longest is 13.4 s (321 frames at 24 fps)",
    });
  });

  it("an unknown workflow is not installed: get it in Models", async () => {
    const { call } = start();
    expect(out(await call("switchTo", { workflow: "LTX 2.3" }))).toMatchObject({
      status: "failed",
      text: "LTX 2.3 is not installed. Get it in Models.",
      error: "LTX 2.3 is not installed. Get it in Models.",
    });
  });

  it("Run on picks only a machine the form allows, with the reason, and Undo goes back", async () => {
    const { call, composer } = start();
    await within(composer()).findByRole("button", { name: "Run on Auto" });
    await waitFor(async () =>
      expect(out(await call("runOn", { host: "gcp", reason: "Mac busy" }))).toMatchObject({
        status: "failed",
        text: "Run on: NVIDIA · Distilled is Mac only",
      }),
    );
    expect(
      out(await call("change", { key: "model.variant", value: "distilled-fp8" })),
    ).toMatchObject({
      text: "Set Model: Distilled fp8",
    });
    let o: Outcome | Asked = { status: "failed", text: "" };
    await waitFor(async () => {
      o = await call("runOn", { host: "gcp", reason: "Mac busy, 3 in line" });
      expect(out(o)).toMatchObject({
        status: "done",
        text: "Set Run on: NVIDIA · Mac busy, 3 in line",
      });
    });
    expect(within(composer()).getByRole("button", { name: "Run on NVIDIA" })).toBeTruthy();
    await undo(o);
    expect(within(composer()).getByRole("button", { name: "Run on Auto" })).toBeTruthy();
  });

  it("Generate is a card with the machine and place in line, and queues nothing until Yes", async () => {
    const { call, queued, reg } = start();
    await waitFor(() => expect(reg.current()?.spec.sees).toContain("Run on: Auto → Mac"));
    expect(out(await call("seeds", { n: 3 }))).toMatchObject({ text: "Set seeds: 3" });
    await call("change", { key: "prompt.text", value: "@mara waits at a bus stop in heavy rain" });
    const o = await call("generate");
    if (!asked(o)) throw new Error("no card");
    expect(o.card).toMatchObject({
      kind: "generate",
      title: "Generate 3 clips?",
      tags: ["uses the GPU"],
      go: "GENERATE",
      quote: "@mara waits at a bus stop in heavy rain",
      est: "Mac · 2nd in line · 24 of 64 GB",
    });
    expect(o.card.rows).toEqual([
      ["Model", "LTX-2.5 · Distilled"],
      ["Size", "704 × 448 · 24 fps"],
      ["Seeds", "3 · random"],
    ]);
    await new Promise((r) => setTimeout(r, 20));
    expect(queued).toHaveLength(0);
    const done = await act(() => o.yes());
    expect(done).toMatchObject({
      status: "done",
      text: "You clicked GENERATE · 3 clips",
      result: { run_ids: ["r1", "r2", "r3"], session_id: "s1" },
    });
    expect(queued).toHaveLength(3);
  });

  it("Generate is refused with the form's reason when it cannot run", async () => {
    const none = { ...models, models: [{ ...models.models[0], variants: [] }] };
    const { call, queued } = start({ list: none });
    await waitFor(async () =>
      expect(out(await call("generate"))).toMatchObject({
        status: "failed",
        text: "Generate · not downloaded · Models",
      }),
    );
    expect(queued).toHaveLength(0);
  });

  it("reads /models again when a host comes up, so a Mac back after a restart is not 'not downloaded'", async () => {
    // the owner's run (2026-11-24): Create opened while the Mac was not live; studio had it live a minute later
    let calls = 0;
    const list = () =>
      ++calls === 1
        ? { ...models, hosts: models.hosts.map((h) => ({ ...h, live: false })) }
        : models;
    const { call, reg } = start({ list });
    await waitFor(() => expect(reg.current()?.spec.sees).toContain("Run on: Auto → Mac"));
    await waitFor(async () => expect(asked(await call("generate"))).toBe(true));
  });

  it("keeps the form when Create is left and opened again on the same session", async () => {
    // the owner's run (2026-11-24): to Models and back, the prompt, look and size had to be set again
    const box = () => document.getElementById(idOf("prompt.text")) as HTMLTextAreaElement;
    const first = start();
    await first.call("change", { key: "prompt.text", value: "a portrait at golden hour" });
    cleanup();
    start();
    await waitFor(() => expect(box().value).toBe("a portrait at golden hour"));
  });

  it("keeps a half-written prompt over a reload, in this browser per session, until Generate", async () => {
    // owner, 2026-11-25 (Q8): a reload dropped the draft
    const box = () => document.getElementById(idOf("prompt.text")) as HTMLTextAreaElement;
    const first = start();
    await first.call("change", { key: "prompt.text", value: "a half-written prompt" });
    cleanup();
    leftForm.current = null; // a reload: memory is gone, the browser's storage stays
    const again = start();
    await waitFor(() => expect(box().value).toBe("a half-written prompt"));
    const card = await again.call("generate");
    if (!asked(card)) throw new Error("no card");
    await act(() => card.yes());
    expect(again.queued).toHaveLength(1);
    cleanup();
    leftForm.current = null;
    start();
    await waitFor(() => expect(box()).toBeTruthy());
    expect(box().value).toBe("");
  });

  it("switching to another session in the same tab brings back that session's draft", async () => {
    // reviewer, 2026-11-25: the draft came back only on the first load
    const box = () => document.getElementById(idOf("prompt.text")) as HTMLTextAreaElement;
    localStorage.setItem(
      "create-draft:s2:picture",
      JSON.stringify({ key: "prompt.text", text: "s2 words" }),
    );
    const { call } = start();
    await call("change", { key: "prompt.text", value: "s1 words" });
    await call("newSession");
    await waitFor(() => expect(box().value).toBe("s2 words"));
    expect(JSON.parse(localStorage.getItem("create-draft:s1:picture")!).text).toBe("s1 words");
  });

  // an audio workflow with the same prompt path, as Qwen3-TTS Voice design's prompt.text (alpha.2 item 14)
  const voice = {
    ...workflow,
    id: "voice-design",
    title: "Voice design",
    media: "audio",
  } as S<"Workflow">;
  const tab = (name: string) => act(() => screen.getByRole("tab", { name }).click());

  it("keeps a draft per side over a reload: the picture prompt never lands in the audio workflow", async () => {
    // reviewer, 2026-11-25: one draft per session lost the picture prompt, or put a script in it
    const box = () => document.getElementById(idOf("prompt.text")) as HTMLTextAreaElement;
    const { call } = start({ workflows: [workflow, voice] });
    await call("change", { key: "prompt.text", value: "@elias @biscuit at the door" });
    await tab("Audio");
    await waitFor(() => expect(box().value).toBe(""));
    await call("change", { key: "prompt.text", value: "Hello there." });
    await waitFor(() =>
      expect(JSON.parse(localStorage.getItem("create-draft:s1:sound")!).text).toBe("Hello there."),
    );
    expect(JSON.parse(localStorage.getItem("create-draft:s1:picture")!).text).toBe(
      "@elias @biscuit at the door",
    );
    cleanup();
    leftForm.current = null; // a reload: the session opens on its picture workflow
    start({ workflows: [workflow, voice] });
    await waitFor(() => expect(box().value).toBe("@elias @biscuit at the door"));
    await tab("Audio");
    await waitFor(() => expect(box().value).toBe("Hello there."));
  });

  it("a voice field refused says what it takes and what it got (item 23)", async () => {
    const tts = {
      ...ttsWorkflows.workflows.find((w) => w.id === "qwen3-tts-voice-over"),
      node_schemas: Object.fromEntries(
        [...coreNodes.nodes, ...ttsNodes.nodes].map((n) => [n.kind, n]),
      ),
      source: "template",
    } as unknown as S<"Workflow">;
    const { call } = start({ workflows: [workflow, tts] });
    await tab("Audio");
    await waitFor(() => expect(document.getElementById(idOf("prompt.text"))).toBeTruthy());
    expect(out(await call("change", { key: "speak.voice", value: "narrator 2" }))).toMatchObject({
      status: "failed",
      text: 'Voice: expected one of your voices; got "narrator 2"',
      error: expect.stringContaining('{"voice_id": "<id>"}'),
    });
    // a wrong shape stays out of the line: no JSON or ids, only in the model's error
    expect(
      out(await call("change", { key: "speak.voice", value: { id: "0b1c-uuid" } })),
    ).toMatchObject({
      status: "failed",
      text: "Voice: expected one of your voices",
      error: expect.stringContaining('{"id":"0b1c-uuid"}'),
    });
  });

  it("another session never gets this session's script", async () => {
    const box = () => document.getElementById(idOf("prompt.text")) as HTMLTextAreaElement;
    const { call } = start({ workflows: [workflow, voice] });
    await tab("Audio");
    await call("change", { key: "prompt.text", value: "s1 script" });
    await tab("Video");
    await call("newSession");
    await waitFor(() => expect(box().value).toBe(""));
    await tab("Audio");
    await waitFor(() => expect(box()).toBeTruthy());
    expect(box().value).toBe("");
  });

  it("Reuse settings shows the reused take on the stage, not the newest", async () => {
    // owner, 2026-11-25 (Q9): the form and the picture must match
    const old = take({ session_id: "s1", title: "Old one" });
    const newest = take({ session_id: "s1", title: "Newest", created_at: "2026-11-02T00:00:00Z" });
    start({ takes: [newest, old], reuse: old.id });
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "Take, Old one" }).getAttribute("aria-pressed"),
      ).toBe("true"),
    );
    expect(screen.getByRole("button", { name: "Take, Newest" }).getAttribute("aria-pressed")).toBe(
      "false",
    );
  });

  it("seeds, select, star and stop; view and a new session; a template refuses rename and delete", async () => {
    const t = take({ session_id: "s1", title: "Shot 2" });
    const running = {
      id: RUN,
      session_id: "s1",
      workflow_id: workflow.id,
      inputs: {},
      status: "running",
      progress: null,
      error: null,
      outputs: [],
      host: "mac",
    } as unknown as S<"Run">;
    const { call, patches, posts } = start({ takes: [t], runs: [running] });
    expect(out(await call("seeds", { n: 5 }))).toMatchObject({ status: "failed" });
    const sel = await call("select", { take_id: t.id });
    expect(out(sel)).toMatchObject({ text: "Selected Shot 2" });
    const st = await call("star", { take_id: t.id, on: true });
    expect(out(st)).toMatchObject({ text: "Starred Shot 2" });
    await undo(st);
    expect(patches).toEqual([{ starred: true }, { starred: false }]);
    expect(out(await call("stop", { run_id: RUN }))).toMatchObject({ text: "Stopped the run" });
    expect(posts).toContain("cancel");
    expect(out(await call("view", { view: "studio" }))).toMatchObject({
      text: "Opened Studio view",
    });
    expect(out(await call("newSession"))).toMatchObject({ result: { session_id: "s2" } });
    expect(out(await call("renameWorkflow", { title: "Mine" }))).toMatchObject({
      status: "failed",
    });
    expect(out(await call("deleteWorkflow"))).toMatchObject({
      status: "failed",
      text: "Delete · only your own workflows",
    });
    expect(out(await call("switchTo", { workflow: "nothing" }))).toMatchObject({
      status: "failed",
    });
    expect(out(await call("change", { key: "look:colour", value: "Teal" }))).toMatchObject({
      status: "failed",
      text: "Colour: not on this form",
    });
  });
});
