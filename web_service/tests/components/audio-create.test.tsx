// @vitest-environment jsdom
// Create on an audio workflow (docs/mock/CreateAudio.dc.html, VoicePicker.dc.html): the Audio switch, the Voice and
// Seed tiles, the voice picker (presets by family, greyed with the reason, Use), the voice sent with the run, the
// waveform stage and the sound tile, and Design a voice (Hear it saves and runs; Cancel deletes the unsaved voice).
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import workflows from "../../../contracts/fixtures/registry/workflows.valid.json";
import coreNodes from "../../../contracts/fixtures/registry/nodes.valid.json";
import { Create } from "@/components/create/create";
import { api, type S } from "@/lib/api";
import { answer, audio } from "./helpers";

vi.mock("@/lib/runs", async (orig) => ({
  ...(await orig<typeof import("@/lib/runs")>()),
  followSession: () => () => undefined,
}));

beforeAll(() => {
  globalThis.requestAnimationFrame ??= (f: FrameRequestCallback) =>
    setTimeout(() => f(0), 0) as unknown as number;
  HTMLElement.prototype.hidePopover ??= () => undefined;
  Element.prototype.scrollIntoView ??= () => undefined;
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
});

type Item = S<"LibraryItem">;
const schemas = Object.fromEntries(
  coreNodes.nodes.map((n) => [n.kind, n]),
) as unknown as S<"Workflow">["node_schemas"];
const wf = (id: string) =>
  ({
    ...workflows.workflows.find((w) => w.id === id),
    node_schemas: schemas,
    source: "template",
  }) as unknown as S<"Workflow">;

const voice = (id: string, name: string, more: Partial<Item> = {}): Item =>
  ({
    id,
    kind: "voice",
    group: "preset",
    name,
    handle: null,
    text: "",
    images: [],
    thumb_url: null,
    version: 1,
    created_at: "2026-11-21T10:00:00Z",
    updated_at: "2026-11-21T10:00:00Z",
    source: "starter",
    family: id.split("/")[0],
    loras: [],
    needs: [],
    voice: { source: "preset", family: id.split("/")[0], speaker: name, language: "en" },
    ...more,
  }) as Item;
const ada = voice("echo/ada", "Ada", { fits: true, reason: null });
const ryan = voice("qwen3-tts/ryan", "Ryan", {
  thumb_url: "/files/packs/qwen3-tts/voices/ryan.wav",
  fits: false,
  reason: "voice Ryan is for qwen3-tts",
});

function serve(items: Item[] = [ada, ryan]) {
  answer("GET", {
    "/v1/studio/sessions/{session_id}/runs": () => ({ data: { runs: [] } }),
    "/v1/studio/sessions/{session_id}/takes": () => ({ data: { takes: [] } }),
    "/v1/studio/library": ({ params }) => {
      const kind = (params as { query?: { kind?: string } })?.query?.kind;
      return { data: { items: kind === "voice" ? items : [] } };
    },
    "/v1/studio/hosts": () => ({ data: { hosts: [] } }),
    "/v1/studio/loras": () => ({ data: { loras: [] } }),
    "/v1/studio/runs/{run_id}": () => ({
      data: {
        id: "d1",
        session_id: "s1",
        workflow_id: "echo-voice-over",
        inputs: {},
        status: "succeeded",
        progress: null,
        error: null,
        outputs: [
          {
            index: 0,
            key: "u/x/runs/d1/1/0.wav",
            kind: "audio",
            url: "/files/u/x/d1.wav",
            width: null,
            height: null,
          },
        ],
        host: "fake",
      },
    }),
  });
}

const run = (over: Partial<S<"Run">> = {}) =>
  ({
    id: "r1",
    session_id: "s1",
    workflow_id: "echo-voice-over",
    inputs: {},
    status: "succeeded",
    progress: null,
    error: null,
    outputs: [],
    host: "fake",
    ...over,
  }) as unknown as S<"Run">;

const start = (takes: S<"Take">[] = [], runs = [run({ status: "queued" })]) =>
  render(
    <Create
      initial={{
        sessions: [],
        workflows: [wf("echo"), wf("echo-voice-over")],
        sessionId: "s1",
        runs,
        takes,
      }}
    />,
  );

describe("Create on an audio workflow", () => {
  it("the Audio switch, the Voice and Seed tiles, no Elements tile; the picker and the run's voice", async () => {
    serve();
    const bodies: { inputs: Record<string, unknown> }[] = [];
    answer("POST", {
      "/v1/studio/sessions/{session_id}/runs": ({ body }) => {
        bodies.push(body as (typeof bodies)[number]);
        return { status: 202, data: run({ id: "r2", status: "queued" }) };
      },
    });
    start();
    const tabs = screen.getByRole("tablist", { name: "Output kind" });
    expect(
      within(tabs)
        .getAllByRole("tab")
        .map((t) => t.textContent),
    ).toEqual(["Image", "Audio"]);
    fireEvent.click(within(tabs).getByRole("tab", { name: "Audio" }));
    const composer = screen.getByRole("form", { name: "Composer" });
    const tile = await within(composer).findByRole("button", { name: "Voice: Pick a voice" });
    expect(within(composer).getByRole("button", { name: /^Seed/ }).textContent).toBe("SeedRandom");
    expect(within(composer).queryByRole("button", { name: /^Elements/ })).toBeNull();
    expect(within(composer).queryByRole("button", { name: "Insert element" })).toBeNull();
    expect(within(composer).getByPlaceholderText("Type the words to speak")).toBeTruthy();

    fireEvent.change(within(composer).getByPlaceholderText("Type the words to speak"), {
      target: { value: "Hello there" },
    });
    // no voice yet: Generate waits and says so
    const go = composer.querySelector<HTMLButtonElement>('button[type="submit"]')!;
    expect(go.disabled).toBe(true);
    expect(go.textContent).toContain("Pick a voice");

    fireEvent.click(tile);
    const dialog = await screen.findByRole("dialog", { name: "Voice" });
    expect(within(dialog).getByRole("tab", { name: "Presets · 2" })).toBeTruthy();
    expect(within(dialog).getByRole("tab", { name: "Mine · 0 fit" })).toBeTruthy();
    // grouped by family, each with its count; Ryan greyed with the reason, his preview playable
    expect(within(dialog).getByText("echo · 1 preset")).toBeTruthy();
    expect(within(dialog).getByText("qwen3-tts · 1 preset")).toBeTruthy();
    // a family none of whose voices fit is folded to one line with the reason (VoicePickerZonos3.dc.html)
    const fold = within(dialog).getByRole("button", { name: /^qwen3-tts · 1 preset/ });
    expect(fold.getAttribute("aria-expanded")).toBe("false");
    expect(fold.textContent).toContain("Ryan is for qwen3-tts · none of these fit echo");
    fireEvent.click(fold);
    const r = within(dialog).getByRole("button", { name: /^Ryan/ });
    // greyed, still focusable, its reason read with it; a click picks nothing
    expect(r.getAttribute("aria-disabled")).toBe("true");
    expect(r.hasAttribute("disabled")).toBe(false);
    expect(document.getElementById(r.getAttribute("aria-describedby")!)?.textContent).toBe(
      "voice Ryan is for qwen3-tts",
    );
    fireEvent.click(r);
    expect(within(dialog).getByText("Nothing picked")).toBeTruthy();
    expect(within(dialog).getByRole("button", { name: "Play the preview of Ryan" })).toBeTruthy();
    expect(within(dialog).queryByRole("button", { name: "Play the preview of Ada" })).toBeNull();
    // no design template among these workflows that takes only designed voices, but echo's takes them
    expect(within(dialog).getByRole("button", { name: "Design a voice" })).toBeTruthy();

    fireEvent.click(within(dialog).getByRole("button", { name: /^Ada/ }));
    expect(within(dialog).getByText("Ada picked")).toBeTruthy();
    fireEvent.click(within(dialog).getByRole("button", { name: "Use Ada" }));
    await waitFor(() =>
      expect(within(composer).getByRole("button", { name: "Voice: Ada · preset" })).toBeTruthy(),
    );

    fireEvent.click(composer.querySelector<HTMLElement>('button[type="submit"]')!);
    await waitFor(() => expect(bodies).toHaveLength(1));
    expect(bodies[0].inputs).toMatchObject({
      "prompt.text": "Hello there",
      "speak.voice": { voice_id: "echo/ada" },
    });
  });

  it("an audio take: the waveform stage, its badge, the sound tile, Add to timeline (API 1.32)", async () => {
    serve();
    const t = audio(2, {
      run_id: "r1",
      url: "/files/u/x/a.wav",
      audio: { duration_s: 2, sample_rate: 24000 },
      made_with: { seed: 12 } as S<"Take">["made_with"],
    });
    start([t], [run({ outputs: [] })]);
    expect(await screen.findByRole("group", { name: "Waveform of the take" })).toBeTruthy();
    expect(screen.getAllByText("seed 12 · 24 kHz mono")).toHaveLength(2);
    expect(screen.getByRole("button", { name: "Play" })).toBeTruthy();
    expect(screen.getByLabelText("Scrub the take")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Loop" }).getAttribute("aria-pressed")).toBe("false");
    const strip = screen.getByRole("group", { name: "Takes" });
    expect(within(strip).getByRole("button", { name: "Take, seed 12, 0:02" }).textContent).toBe(
      "seed 12 · 0:02",
    );
    expect(screen.getAllByRole("button", { name: /Add to timeline/ }).length).toBeGreaterThan(0);
    expect(screen.queryByRole("button", { name: "Full screen" })).toBeNull();
    const inspector = screen.getByRole("complementary", { name: "Inspector" });
    expect(within(inspector).getByText("24 kHz · mono · wav")).toBeTruthy();
  });

  it("Design a voice: Hear it saves the description and runs it; Cancel deletes the voice not saved", async () => {
    serve();
    const made: unknown[] = [];
    const runs: { workflow_id: string; inputs: Record<string, unknown> }[] = [];
    answer("POST", {
      "/v1/studio/library": ({ body }) => {
        made.push(body);
        return {
          status: 201,
          data: voice("5a0c1e2f-3b4d-4e5f-8a6b-7c8d9e0f1a21", "Old captain", {
            group: "designed",
            source: "mine",
            voice: { source: "designed", design: "Male, about 60" },
          }),
        };
      },
      "/v1/studio/sessions/{session_id}/runs": ({ body }) => {
        runs.push(body as (typeof runs)[number]);
        return { status: 202, data: run({ id: "d1", status: "queued" }) };
      },
    });
    const del = vi.spyOn(api, "DELETE").mockResolvedValue({
      data: undefined,
      error: undefined,
      response: new Response(null, { status: 204 }),
    } as never);
    vi.useFakeTimers({ shouldAdvanceTime: true });
    start([], [run({ workflow_id: "echo-voice-over", status: "queued" })]);
    fireEvent.click(await screen.findByRole("button", { name: "Voice: Pick a voice" }));
    const dialog = await screen.findByRole("dialog", { name: "Voice" });
    fireEvent.click(within(dialog).getByRole("button", { name: "Design a voice" }));
    const d = await screen.findByRole("dialog", { name: "Design a voice" });
    const hear = within(d).getByRole("button", { name: "Hear it" });
    expect(hear.hasAttribute("disabled")).toBe(true);
    expect(within(d).getByRole("button", { name: "Save as voice" }).hasAttribute("disabled")).toBe(
      true,
    );
    fireEvent.change(within(d).getByLabelText(/Describe the voice/), {
      target: { value: "Male, about 60" },
    });
    fireEvent.change(within(d).getByLabelText("A line to hear it say"), {
      target: { value: "The tide waits." },
    });
    fireEvent.change(within(d).getByLabelText("Name"), { target: { value: "Old captain" } });
    fireEvent.click(hear);
    await waitFor(() => expect(runs).toHaveLength(1));
    expect(made).toEqual([
      {
        kind: "voice",
        group: "designed",
        name: "Old captain",
        voice: { design: "Male, about 60" },
      },
    ]);
    expect(runs[0]).toEqual({
      workflow_id: "echo-voice-over",
      inputs: {
        "speak.voice": { voice_id: "5a0c1e2f-3b4d-4e5f-8a6b-7c8d9e0f1a21" },
        "prompt.text": "The tide waits.",
      },
    });
    await vi.advanceTimersByTimeAsync(1100);
    expect(await within(d).findByText("Try 1")).toBeTruthy();
    expect(within(d).getByRole("button", { name: "Play try 1" })).toBeTruthy();
    fireEvent.click(within(d).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(del).toHaveBeenCalledTimes(1));
    expect(del.mock.calls[0][1]).toEqual({
      params: { path: { item_id: "5a0c1e2f-3b4d-4e5f-8a6b-7c8d9e0f1a21" } },
    });
    vi.useRealTimers();
  });

  it("a Studio block: Voice, Language and Seed tiles; Preset, Style and Variation in the Inspector", async () => {
    serve();
    const speak = {
      kind: "q/speak",
      title: "Speak",
      family: "q",
      params: [
        { name: "voice", type: "voice", label: "Voice", default: null, accepts: ["preset"] },
        {
          name: "language",
          type: "enum",
          label: "Language",
          default: "auto",
          enum: ["auto", "en"],
        },
        {
          name: "style",
          type: "string",
          label: "Style",
          default: "",
          max: 500,
          help: "How to say it.",
        },
        {
          name: "temperature",
          type: "float",
          label: "Variation",
          default: 0.3,
          min: 0.1,
          max: 1.2,
          step: 0.05,
          help: "Low keeps the voice steady.",
        },
      ],
      inputs: [{ name: "text", type: "Text" }],
      outputs: [{ name: "audio", type: "Audio" }],
    };
    const w = {
      id: "q-voice-over",
      title: "Q voice-over",
      family: "q",
      env: "fake",
      media: "audio",
      source: "template",
      node_schemas: {
        ...schemas,
        "q/speak": speak,
        seed: {
          kind: "seed",
          title: "Seed",
          family: null,
          params: [
            { name: "seed", type: "seed", label: "Seed", default: -1, min: -1, max: 2 ** 53 - 1 },
          ],
          inputs: [],
          outputs: [{ name: "seed", type: "Seed" }],
        },
      },
      nodes: {
        prompt: { kind: "prompt", params: { text: "" } },
        seed: { kind: "seed", params: { seed: -1 } },
        speak: { kind: "q/speak", in: { text: "prompt.text", seed: "seed.seed" } },
        output: { kind: "output", in: { media: "speak.audio" } },
      },
      studio: {
        composer: ["prompt.text"],
        quick: ["speak.voice", "speak.language", "seed.seed"],
        inspector: [{ title: "Voice", items: ["speak.style", "speak.temperature"] }],
        presets: { Steady: { "speak.temperature": 0.3 }, Lively: { "speak.temperature": 0.9 } },
        preset_order: ["Steady", "Lively"],
      },
    } as unknown as S<"Workflow">;
    render(
      <Create
        initial={{
          sessions: [],
          workflows: [w],
          sessionId: "s1",
          runs: [run({ workflow_id: "q-voice-over", status: "queued" })],
          takes: [],
        }}
      />,
    );
    const composer = screen.getByRole("form", { name: "Composer" });
    expect(
      await within(composer).findByRole("button", { name: "Voice: Pick a voice" }),
    ).toBeTruthy();
    expect(within(composer).getByRole("button", { name: /^Language/ }).textContent).toBe(
      "LanguageAuto",
    );
    expect(within(composer).getByRole("button", { name: /^Seed/ }).textContent).toBe("SeedRandom");
    const inspector = screen.getByRole("complementary", { name: "Inspector" });
    expect(
      within(inspector)
        .getAllByRole("heading")
        .map((h) => h.textContent),
    ).toEqual(["Preset", "Voice"]); // the board's Voice section leads; Language and Seed are composer tiles only
    expect(within(inspector).getByRole("button", { name: /^Voice/ }).textContent).toMatch(/^Voice/);
    expect(
      within(inspector).getByRole("button", { name: "Steady" }).getAttribute("aria-pressed"),
    ).toBe("true");
    expect(within(inspector).getByText("0 / 500")).toBeTruthy();
    expect(within(inspector).getByText("How to say it.")).toBeTruthy();
    const vary = within(inspector).getByLabelText(/Variation/);
    expect(vary.getAttribute("type")).toBe("range");
    fireEvent.change(vary, { target: { value: "0.9" } });
    expect(
      within(inspector).getByRole("button", { name: "Lively" }).getAttribute("aria-pressed"),
    ).toBe("true");
    // the language's tile opens its editor, under its own name, which names its languages
    fireEvent.click(within(composer).getByRole("button", { name: /^Language/ }));
    expect(await within(inspector).findByRole("button", { name: "English" })).toBeTruthy();
    expect(within(inspector).getByRole("heading", { name: "Language" })).toBeTruthy();
  });
});

describe("Design a voice: closing and tries", () => {
  const mine = voice("5a0c1e2f-3b4d-4e5f-8a6b-7c8d9e0f1a21", "Old captain", {
    group: "designed",
    source: "mine",
    voice: { source: "designed", design: "Male, about 60" },
  });
  const openDesigner = async () => {
    fireEvent.click(
      within(screen.getByRole("tablist", { name: "Output kind" })).getByRole("tab", {
        name: "Audio",
      }),
    );
    fireEvent.click(await screen.findByRole("button", { name: "Voice: Pick a voice" }));
    const dialog = await screen.findByRole("dialog", { name: "Voice" });
    fireEvent.click(within(dialog).getByRole("button", { name: "Design a voice" }));
    const d = await screen.findByRole("dialog", { name: "Design a voice" });
    fireEvent.change(within(d).getByLabelText(/Describe the voice/), {
      target: { value: "Male, about 60" },
    });
    fireEvent.change(within(d).getByLabelText("A line to hear it say"), {
      target: { value: "Hi." },
    });
    fireEvent.change(within(d).getByLabelText("Name"), { target: { value: "Old captain" } });
    return d;
  };
  const noSession = () =>
    render(
      <Create
        initial={{
          sessions: [],
          workflows: [wf("echo"), wf("echo-voice-over")],
          sessionId: null,
          runs: [],
          takes: [],
        }}
      />,
    );

  it("closed while saving: the voice made then is deleted, nothing runs", async () => {
    serve();
    let saved: (v: unknown) => void = () => undefined;
    const posts: string[] = [];
    vi.spyOn(api, "POST").mockImplementation(((path: string) => {
      posts.push(path);
      return path === "/v1/studio/library"
        ? new Promise((r) => (saved = r))
        : Promise.resolve({
            data: undefined,
            error: { detail: "no" },
            response: new Response(null, { status: 500 }),
          });
    }) as never);
    const del = vi.spyOn(api, "DELETE").mockResolvedValue({
      data: undefined,
      error: undefined,
      response: new Response(null, { status: 204 }),
    } as never);
    noSession();
    const d = await openDesigner();
    fireEvent.click(within(d).getByRole("button", { name: "Hear it" }));
    await waitFor(() => expect(posts).toEqual(["/v1/studio/library"]));
    fireEvent.click(within(d).getByRole("button", { name: "Cancel" }));
    saved({ data: mine, error: undefined, response: new Response(null, { status: 201 }) });
    await waitFor(() => expect(del).toHaveBeenCalledTimes(1));
    expect(del.mock.calls[0][1]).toEqual({ params: { path: { item_id: mine.id } } });
    expect(posts).toEqual(["/v1/studio/library"]);
  });

  it("tries share one Voice design session; closing during a try cancels its run", async () => {
    serve();
    const posts: string[] = [];
    let n = 0;
    answer("POST", {
      "/v1/studio/library": () => {
        posts.push("library");
        return { status: 201, data: mine };
      },
      "/v1/studio/sessions": () => {
        posts.push("session");
        return { status: 201, data: { id: "vd", title: "Voice design" } };
      },
      "/v1/studio/sessions/{session_id}/runs": ({ params }) => {
        posts.push(`run in ${(params as { path: { session_id: string } }).path.session_id}`);
        return { status: 202, data: run({ id: `t${++n}`, status: "queued" }) };
      },
      "/v1/studio/runs/{run_id}/cancel": ({ params }) => {
        posts.push(`cancel ${(params as { path: { run_id: string } }).path.run_id}`);
        return { status: 202, data: {} };
      },
    });
    vi.spyOn(api, "DELETE").mockResolvedValue({
      data: undefined,
      error: undefined,
      response: new Response(null, { status: 204 }),
    } as never);
    vi.useFakeTimers({ shouldAdvanceTime: true });
    noSession();
    const d = await openDesigner();
    fireEvent.click(within(d).getByRole("button", { name: "Hear it" }));
    await vi.advanceTimersByTimeAsync(1100);
    expect(await within(d).findByText("Try 1")).toBeTruthy();
    const again = within(d).getByRole("button", { name: "Try again" });
    await waitFor(() => expect(again.hasAttribute("disabled")).toBe(false));
    fireEvent.click(again);
    await waitFor(() => expect(posts.filter((p) => p.startsWith("run"))).toHaveLength(2));
    fireEvent.click(within(d).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(posts).toContain("cancel t2"));
    expect(posts.filter((p) => p === "session")).toHaveLength(1);
    expect(posts.filter((p) => p.startsWith("run"))).toEqual(["run in vd", "run in vd"]);
    expect(posts).not.toContain("cancel t1");
    vi.useRealTimers();
  });

  it("the ⋮ menu of a voice closes on Escape, focus back on its button", async () => {
    serve([ada, ryan, { ...mine, fits: true, reason: null }]);
    start();
    fireEvent.click(
      within(screen.getByRole("tablist", { name: "Output kind" })).getByRole("tab", {
        name: "Audio",
      }),
    );
    fireEvent.click(await screen.findByRole("button", { name: "Voice: Pick a voice" }));
    const dialog = await screen.findByRole("dialog", { name: "Voice" });
    fireEvent.click(await within(dialog).findByRole("tab", { name: "Mine · 1 fit" }));
    const more = within(dialog).getByRole("button", { name: "More for Old captain" });
    fireEvent.click(more);
    const menu = within(dialog).getByRole("menu", { name: "Old captain" });
    fireEvent.keyDown(within(menu).getByRole("menuitem", { name: "Rename" }), { key: "Escape" });
    expect(within(dialog).queryByRole("menu")).toBeNull();
    expect(document.activeElement).toBe(more);
    expect(screen.getByRole("dialog", { name: "Voice" })).toBeTruthy();
  });
});

describe("WavePlayer", () => {
  it("says so when the take's sound cannot be played", async () => {
    const { WavePlayer } = await import("@/components/create/audio");
    const { container } = render(<WavePlayer url="/files/u/x/gone.wav" duration={2} />);
    expect(screen.queryByRole("alert")).toBeNull();
    fireEvent.error(container.querySelector("audio")!);
    expect(screen.getByRole("alert").textContent).toContain("could not be played");
  });
});
