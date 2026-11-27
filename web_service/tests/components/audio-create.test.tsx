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
import { answer, audio, video } from "./helpers";

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
    ).toEqual(["Image", "Voice"]);
    // each tab shows its kind's orb above the label, hidden from screen readers (Audio: the voice orb)
    expect(
      within(tabs)
        .getAllByRole("tab")
        .map((t) => {
          const orb = t.querySelector(".ki")!;
          return [orb.className, orb.getAttribute("aria-hidden"), !!orb.querySelector("svg")];
        }),
    ).toEqual([
      ["ki k-image", "true", true],
      ["ki k-voice", "true", true],
    ]);
    fireEvent.click(within(tabs).getByRole("tab", { name: "Voice" }));
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
        name: "Voice",
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
        name: "Voice",
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

// ---------------------------------------------------------------- Music and Sound (plan 38 P4, docs/mock/CreateMusicSound.dc.html)
describe("Create's Music and Sound (frames 1-5)", () => {
  // the echo fakes as studio serves them (Workflow.sound is computed by studio; the fixtures leave it out)
  const SOUND: Record<string, S<"SoundClass">> = {
    "echo-music": "music",
    "echo-repaint": "music",
    "echo-music-extend": "music",
    "echo-sound": "sound",
    "echo-vary": "sound",
    "echo-sound-video": "sound",
  };
  const cls = (id: string) => ({ ...wf(id), sound: SOUND[id] }) as S<"Workflow">;
  const all = () => [wf("echo"), wf("echo-voice-over"), ...Object.keys(SOUND).map(cls)];
  const offer = (workflow_id: string, action: string, param: string, inputs = {}) =>
    ({
      workflow_id,
      title: workflow_id,
      action,
      family: "echo",
      media: "audio",
      param,
      inputs,
    }) as S<"TakeOffer">;
  const song = audio(10, {
    run_id: "m1",
    sound: "music",
    title: "Rain on the window",
    url: "/files/u/x/song.wav",
    audio: {
      duration_s: 10,
      sample_rate: 48000,
      channels: 2,
      sections: [
        { label: "Intro", start_s: 0, end_s: 2.5 },
        { label: "Verse 1", start_s: 2.5, end_s: 5 },
        { label: "Chorus", start_s: 5, end_s: 7.5 },
        { label: "Outro", start_s: 7.5, end_s: 10 },
      ],
    },
    made_with: {
      seed: 31,
      prompt: "Warm lo-fi pop",
      workflow_id: "echo-music",
      inputs: {},
    } as unknown as S<"Take">["made_with"],
  });
  function serveMusic(uses: S<"TakeOffer">[] = [], extra: Record<string, () => unknown> = {}) {
    answer("GET", {
      "/v1/studio/sessions/{session_id}/runs": () => ({ data: { runs: [] } }),
      "/v1/studio/sessions/{session_id}/takes": () => ({ data: { takes: [] } }),
      "/v1/studio/library": () => ({ data: { items: [] } }),
      "/v1/studio/hosts": () => ({ data: { hosts: [] } }),
      "/v1/studio/loras": () => ({ data: { loras: [] } }),
      "/v1/studio/takes/{take_id}/uses": () => ({ data: { uses } }),
      "/v1/studio/takes/{take_id}": () => ({ data: song }),
      ...Object.fromEntries(Object.entries(extra).map(([k, f]) => [k, () => ({ data: f() })])),
    });
  }
  // a popup by its label (popovers are not shown in jsdom, so role queries skip them)
  const popOf = (label: string) => {
    const d = [...document.querySelectorAll<HTMLElement>("[role=dialog]")].find(
      (x) => x.getAttribute("aria-label") === label,
    );
    if (!d) throw new Error(`no popup ${label}`);
    return d;
  };
  const open = (takes: S<"Take">[], runs: S<"Run">[]) =>
    render(<Create initial={{ sessions: [], workflows: all(), sessionId: "s1", runs, takes }} />);

  it("tabs Image, Voice, Music, Sound with their orbs; Music: style, lyrics with tags, Instrumental, the group chip", async () => {
    serveMusic();
    const bodies: { workflow_id: string; inputs: Record<string, unknown> }[] = [];
    answer("POST", {
      "/v1/studio/sessions/{session_id}/runs": ({ body }) => {
        bodies.push(body as (typeof bodies)[number]);
        return {
          status: 202,
          data: run({ id: `q${bodies.length}`, workflow_id: "echo-music", status: "queued" }),
        };
      },
    });
    open([], [run({ status: "queued" })]);
    const tabs = screen.getByRole("tablist", { name: "Output kind" });
    expect(
      within(tabs)
        .getAllByRole("tab")
        .map((t) => t.textContent),
    ).toEqual(["Image", "Voice", "Music", "Sound"]);
    // Music is a small black record (no glyph), Sound the sky orb with wave bars
    const orb = (name: string) => within(tabs).getByRole("tab", { name }).querySelector(".ki")!;
    expect(orb("Music").querySelector("svg")).toBeNull();
    expect(orb("Sound").querySelector("svg")).toBeTruthy();
    fireEvent.click(within(tabs).getByRole("tab", { name: "Music" }));
    const composer = screen.getByRole("form", { name: "Composer" });
    const style = await within(composer).findByPlaceholderText(
      "Style: genre, instruments, voice, mood",
    );
    fireEvent.change(style, { target: { value: "Warm lo-fi pop" } });
    // a tag goes in at the caret, on its own line
    const lyrics = within(composer).getByRole("textbox", { name: "Lyrics" }) as HTMLTextAreaElement;
    fireEvent.change(lyrics, { target: { value: "first line" } });
    lyrics.setSelectionRange(10, 10);
    fireEvent.click(within(composer).getByRole("button", { name: "[chorus]" }));
    expect(lyrics.value).toBe("first line\n[chorus]\n");
    // the lyrics keep 6 lines the least and scroll, and Expand opens them big, as the prompt (owner, 2026-11-27);
    // they take the composer's free height (grow, never their text's)
    expect(lyrics.style.height).toBe("126px");
    expect(lyrics.className).toMatch(/\bgrow\b/);
    expect(lyrics.className).toMatch(/\bshrink-0\b/);
    fireEvent.click(within(lyrics.parentElement!).getByRole("button", { name: "Expand" }));
    const big = await screen.findByRole("dialog", { name: "Lyrics" });
    const bigText = within(big).getByRole("textbox", { name: "Lyrics" }) as HTMLTextAreaElement;
    expect(bigText.value).toBe("first line\n[chorus]\n");
    fireEvent.change(bigText, { target: { value: "first line\n[chorus]\nsecond" } });
    expect(lyrics.value).toBe("first line\n[chorus]\nsecond");
    fireEvent.click(within(big).getByRole("button", { name: "Done" }));
    // Auto length and BPM share one chip ("Length · BPM"), its popup has one Auto for both
    const chip = within(composer).getByRole("button", { name: "Length · BPM" });
    fireEvent.click(chip);
    const pop = await waitFor(() => popOf("Length · BPM"));
    const auto = within(pop).getByRole("switch", { name: "Auto", hidden: true });
    expect(auto.getAttribute("aria-checked")).toBe("true");
    expect(within(pop).getByText("The plan picks both")).toBeTruthy();
    fireEvent.click(auto);
    await waitFor(() => expect(chip.textContent).toBe("0:01 · 30 BPM"));
    fireEvent.click(within(pop).getByRole("switch", { name: "Auto", hidden: true }));
    await waitFor(() => expect(chip.textContent).toBe("Length · BPM"));
    fireEvent.keyDown(document, { key: "Escape" });
    // Instrumental hides the lyrics; the run sends no lyrics then
    fireEvent.click(within(composer).getByRole("switch", { name: "Instrumental" }));
    expect(within(composer).queryByRole("textbox", { name: "Lyrics" })).toBeNull();
    // one take a Generate (Music has no count of its own)
    expect(within(composer).getByRole("group", { name: "Seeds to queue" }).textContent).toContain(
      "1 / 4",
    );
    fireEvent.click(composer.querySelector<HTMLButtonElement>('button[type="submit"]')!);
    await waitFor(() => expect(bodies).toHaveLength(1));
    expect(bodies[0].workflow_id).toBe("echo-music");
    expect(bodies[0].inputs).toMatchObject({
      "prompt.text": "Warm lo-fi pop",
      "music.instrumental": true,
    });
    expect(bodies[0].inputs).not.toHaveProperty("music.lyrics");
  });

  it("every kind: the main text box takes the composer's free height, no empty rows; the picked tab stands out", async () => {
    // owner, 2026-11-27: "text only in two line it waster bottom empty area", "switch between audio, video etc make
    // blank spaces due to other modes", "make selected one more differentiated from on active ones"
    serveMusic();
    open([], [run({ status: "queued" })]);
    const tabs = screen.getByRole("tablist", { name: "Output kind" });
    // the tabs fill the column beside the composer (63px the least each), so a taller composer leaves no blank
    expect(tabs.className).not.toContain("self-start");
    const composer = screen.getByRole("form", { name: "Composer" });
    const grows = (el: Element) =>
      el.classList.contains("grow") && el.classList.contains("shrink-0");
    /** the composer's direct child that holds `el` */
    const row = (el: Element) => {
      let n = el;
      while (n.parentElement !== composer) n = n.parentElement!;
      return n;
    };
    for (const [kind, box] of [
      ["Image", () => within(composer).getByRole("combobox", { name: /Prompt|text/i })],
      ["Voice", () => within(composer).getByPlaceholderText("Type the words to speak")],
      ["Music", () => within(composer).getByRole("textbox", { name: "Lyrics" })],
      ["Sound", () => within(composer).getByPlaceholderText("Describe the sound")],
    ] as const) {
      fireEvent.click(within(tabs).getByRole("tab", { name: kind }));
      await waitFor(() =>
        expect(within(tabs).getByRole("tab", { name: kind }).getAttribute("aria-selected")).toBe(
          "true",
        ),
      );
      const el = await waitFor(box);
      // the box grows from its fixed height (never shrinks below it), and so does every box up to the composer
      expect(grows(el), kind).toBe(true);
      for (let n = el.parentElement!; n !== composer; n = n.parentElement!)
        expect(/\b(grow|grow-\[2\]|flex-1)\b/.test(n.className), `${kind}: ${n.className}`).toBe(
          true,
        );
      expect(row(el).className).toMatch(/\bflex-col\b/);
      // a tiles row with nothing in it takes no place (Music has none until an @mention)
      for (const c of composer.children)
        if (!c.childNodes.length && c.tagName === "DIV")
          expect(c.className, kind).toContain("empty:hidden");
      // the picked tab: bold, the ring is CSS on aria-selected (globals.css .ktab); every tab at least 63px, growing
      for (const t of within(tabs).getAllByRole("tab")) {
        expect(t.className).toContain("min-h-[63px]");
        expect(t.className).toContain("flex-1");
        expect(t.getAttribute("aria-selected")).toBe(String(t.textContent === kind));
      }
    }
    // Music: the style keeps its share (flex-1), the lyrics take twice it
    fireEvent.click(within(tabs).getByRole("tab", { name: "Music" }));
    const lyricsBox = await within(composer).findByRole("textbox", { name: "Lyrics" });
    expect(row(lyricsBox).className).toContain("grow-[2]");
    const style = within(composer).getByPlaceholderText("Style: genre, instruments, voice, mood");
    expect(row(style).className).toContain("flex-1");
    // Instrumental hides the lyrics: their box no longer grows
    fireEvent.click(within(composer).getByRole("switch", { name: "Instrumental" }));
    await waitFor(() =>
      expect(within(composer).queryByRole("textbox", { name: "Lyrics" })).toBeNull(),
    );
    const bar = within(composer).getByRole("group", { name: "Lyrics tags" });
    expect(bar.parentElement!.className).not.toContain("grow-[2]");
  });

  it("a song on the stage: its badge, sections, the cover tile; Repaint picks a range, Extend runs from its popup", async () => {
    const uses = [
      offer("echo-repaint", "Repaint", "repaint.source", {
        "repaint.source": { take_id: song.id },
        "repaint.span": { start: 0, end: 10 },
      }),
      offer("echo-music-extend", "Extend", "lengthen.source", {
        "lengthen.source": { take_id: song.id },
      }),
    ];
    serveMusic(uses);
    const bodies: { workflow_id: string; inputs: Record<string, unknown> }[] = [];
    answer("POST", {
      "/v1/studio/sessions/{session_id}/runs": ({ body }) => {
        bodies.push(body as (typeof bodies)[number]);
        return { status: 202, data: run({ id: `q${bodies.length}`, status: "queued" }) };
      },
    });
    open([song], [run({ id: "m1", workflow_id: "echo-music" })]);
    expect((await screen.findAllByText("seed 31 · 0:10 · 48 kHz stereo")).length).toBeGreaterThan(
      0,
    );
    const sections = screen.getByRole("group", { name: "Sections" });
    expect(
      within(sections)
        .getAllByRole("button")
        .map((b) => b.textContent),
    ).toEqual(["Intro", "Verse 1", "Chorus", "Outro"]);
    // the strip: the cover tile, then Repaint · Cover · Extend (Cover not offered: greyed, why on hover)
    const strip = screen.getByRole("group", { name: "Takes" });
    expect(
      within(strip)
        .getByRole("button", { name: "Take, seed 31, 0:10" })
        .querySelector('[data-sound="music"]'),
    ).toBeTruthy();
    const cover = await within(strip).findByRole("button", { name: "Cover" });
    expect(cover.getAttribute("aria-disabled")).toBe("true");
    expect(cover.getAttribute("title")).toBe("Not on Echo music");
    // Extend: its popup with Add and Where, the run beside the take
    fireEvent.click(within(strip).getByRole("button", { name: "Extend" }));
    const ext = popOf("Extend · seed 31");
    expect(within(ext).getByText("Add")).toBeTruthy();
    fireEvent.click(within(ext).getByRole("button", { name: "Start", hidden: true }));
    fireEvent.click(
      within(ext)
        .getAllByRole("button", { name: /^Extend/, hidden: true })
        .at(-1)!,
    );
    await waitFor(() => expect(bodies).toHaveLength(1));
    expect(bodies[0]).toMatchObject({
      workflow_id: "echo-music-extend",
      inputs: { "lengthen.source": { take_id: song.id }, "lengthen.where": "start" },
    });
    // Repaint (the song picked again: the new run is shown after Extend): the stage turns into the range picker; a
    // section takes its range
    fireEvent.click(within(strip).getByRole("button", { name: "Take, seed 31, 0:10" }));
    fireEvent.click(await within(strip).findByRole("button", { name: "Repaint" }));
    const range = await screen.findByRole("group", { name: /^Repaint range on the song/ });
    expect(range).toBeTruthy();
    fireEvent.click(
      within(screen.getByRole("group", { name: "Sections" })).getByRole("button", {
        name: /^Chorus/,
      }),
    );
    // the stage's readout, and the drawer's Repaint section
    expect((await screen.findAllByText("0:05 – 0:08 · 3 s")).length).toBe(2);
    fireEvent.click(
      screen.getAllByRole("button", { name: /^Repaint/ }).find((b) => b.classList.contains("key"))!,
    );
    await waitFor(() => expect(bodies).toHaveLength(2));
    expect(bodies[1]).toMatchObject({
      workflow_id: "echo-repaint",
      inputs: { "repaint.source": { take_id: song.id }, "repaint.span": { start: 5, end: 7.5 } },
    });
  });

  it("Sound: 4 of 8 variations a Generate, a grid on the stage; a click keeps one in the strip, Keep all the rest", async () => {
    serveMusic();
    const bodies: { workflow_id: string; inputs: Record<string, unknown> }[] = [];
    answer("POST", {
      "/v1/studio/sessions/{session_id}/runs": ({ body }) => {
        bodies.push(body as (typeof bodies)[number]);
        return {
          status: 202,
          data: run({ id: `v${bodies.length}`, workflow_id: "echo-sound", status: "queued" }),
        };
      },
    });
    open([], [run({ status: "queued" })]);
    fireEvent.click(
      within(screen.getByRole("tablist", { name: "Output kind" })).getByRole("tab", {
        name: "Sound",
      }),
    );
    const composer = screen.getByRole("form", { name: "Composer" });
    expect(
      (await within(composer).findByRole("group", { name: "How many variations" })).textContent,
    ).toContain("4 / 8");
    // Avoid is a line on the composer; Match a video is a tile
    expect(within(composer).getByRole("textbox", { name: "Avoid" })).toBeTruthy();
    expect(within(composer).getByRole("button", { name: /Match a video/ })).toBeTruthy();
    fireEvent.change(within(composer).getByPlaceholderText("Describe the sound"), {
      target: { value: "A door creaks" },
    });
    fireEvent.click(composer.querySelector<HTMLButtonElement>('button[type="submit"]')!);
    await waitFor(() => expect(bodies).toHaveLength(4));
    expect(new Set(bodies.map((b) => b.workflow_id))).toEqual(new Set(["echo-sound"]));
  });

  it("the grid of variations: plays each, keeps one, Keep all", async () => {
    serveMusic();
    localStorage.setItem("create-variations:s1", JSON.stringify({ runs: ["v1", "v2"], kept: [] }));
    const fx = (n: number) =>
      audio(2, {
        run_id: `v${n}`,
        sound: "sound",
        created_at: `2026-11-27T00:00:0${n}Z`,
        audio: { duration_s: 2, sample_rate: 44100, channels: 2 },
        made_with: {
          seed: 40 + n,
          workflow_id: "echo-sound",
          inputs: {},
        } as unknown as S<"Take">["made_with"],
      });
    const [a, b] = [fx(1), fx(2)];
    open(
      [b, a],
      [run({ id: "v2", workflow_id: "echo-sound" }), run({ id: "v1", workflow_id: "echo-sound" })],
    );
    const grid = await screen.findByRole("group", { name: "Variations" });
    expect(screen.getByText("2 variations · 0:02 · 44.1 kHz stereo")).toBeTruthy();
    const strip = () => screen.queryByRole("group", { name: "Takes" });
    // none kept yet: none in the strip
    expect(
      strip() && within(strip()!).queryByRole("button", { name: /^Take, seed 41/ }),
    ).toBeFalsy();
    fireEvent.click(within(grid).getByRole("button", { name: "Variation, seed 41 · 0:02" }));
    expect(
      within(grid).getByRole("button", { name: "Variation, seed 41 · 0:02, kept" }),
    ).toBeTruthy();
    expect(within(strip()!).getByRole("button", { name: /^Take, seed 41/ })).toBeTruthy();
    expect(JSON.parse(localStorage.getItem("create-variations:s1")!).kept).toEqual([a.id]);
    fireEvent.click(screen.getByRole("button", { name: "Keep all" }));
    expect(within(strip()!).getByRole("button", { name: /^Take, seed 42/ })).toBeTruthy();
    localStorage.removeItem("create-variations:s1");
  });

  it("a sound from a video: the stage switches Sound / Video with sound; the video's tile reads ▶ with sound", async () => {
    serveMusic();
    const fx = audio(1, {
      run_id: "f1",
      sound: "sound",
      audio: { duration_s: 1, sample_rate: 44100, channels: 2 },
      made_with: {
        seed: 7,
        workflow_id: "echo-sound-video",
        inputs: {},
      } as unknown as S<"Take">["made_with"],
    });
    const vid = video(1, 25, { run_id: "f1", title: "Fox trots in · with sound" });
    open([vid, fx], [run({ id: "f1", workflow_id: "echo-sound-video" })]);
    const show = await screen.findByRole("group", { name: "Show" });
    expect(
      within(show)
        .getAllByRole("button")
        .map((b) => b.textContent),
    ).toEqual(["Sound", "Video with sound"]);
    const strip = screen.getByRole("group", { name: "Takes" });
    expect(within(strip).getByRole("button", { name: /with sound/ }).textContent).toBe(
      "▶ with sound",
    );
    fireEvent.click(within(show).getByRole("button", { name: "Sound" }));
    expect(await screen.findByRole("group", { name: "Waveform of the take" })).toBeTruthy();
    expect(
      within(screen.getByRole("group", { name: "Show" }))
        .getByRole("button", { name: "Sound" })
        .getAttribute("aria-pressed"),
    ).toBe("true");
  });

  it("the model chip lists the kind's models, MiniMax Music 3 first; what this one lacks stays greyed", async () => {
    serveMusic();
    const minimax = {
      ...cls("echo-music"),
      id: "minimax-music3",
      title: "MiniMax Music 3",
      family: "minimax-music3",
      studio: {
        composer: ["prompt.text", "music.lyrics"],
        quick: ["music.instrumental", "music.seconds"],
      },
    } as S<"Workflow">;
    render(
      <Create
        initial={{
          sessions: [],
          workflows: [...all(), minimax],
          sessionId: "s1",
          runs: [run({ status: "queued" })],
          takes: [],
        }}
      />,
    );
    fireEvent.click(
      within(screen.getByRole("tablist", { name: "Output kind" })).getByRole("tab", {
        name: "Music",
      }),
    );
    const composer = screen.getByRole("form", { name: "Composer" });
    // Music opens on MiniMax (owner, 2026-11-27); Echo music's BPM grouped with Length is not on it
    const greyed = await within(composer).findByText("Length · BPM");
    expect(greyed.getAttribute("aria-disabled")).toBe("true");
    expect(greyed.getAttribute("title")).toBe("Not on MiniMax Music 3");
    fireEvent.click(within(composer).getByRole("button", { name: "MiniMax Music 3" }));
    const pop = await waitFor(() => popOf("Model"));
    const rows = within(pop).getAllByRole("menuitemradio", { hidden: true });
    expect(rows.map((r) => r.firstChild?.firstChild?.textContent)).toEqual([
      "MiniMax Music 3",
      "Echo music",
    ]);
    expect(rows[0].getAttribute("aria-checked")).toBe("true");
    fireEvent.click(rows[1]);
    expect(await within(composer).findByRole("button", { name: "Length · BPM" })).toBeTruthy();
  });
});
