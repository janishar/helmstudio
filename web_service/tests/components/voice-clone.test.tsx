// @vitest-environment jsdom
// Clone from a clip (API 1.31, docs/mock/VoicePickerZonos3.dc.html, VoicePicker.dc.html): the clip's limits (3–30 s,
// takes with sound only), the consent tick before Hear it and Save voice, Save as voice prefilled from a take (a
// designed voice's take saves that designed voice), and the fit on a workflow that takes clips (Mine · N fit, why
// each fits, a family that does not fit folded).
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import workflows from "../../../contracts/fixtures/registry/workflows.valid.json";
import coreNodes from "../../../contracts/fixtures/registry/nodes.valid.json";
import zonosWfs from "../../../contracts/fixtures/registry/zonos3.workflows.valid.json";
import zonosNodes from "../../../contracts/fixtures/registry/zonos3.nodes.valid.json";
import qwenWfs from "../../../contracts/fixtures/registry/qwen3-tts.workflows.valid.json";
import qwenNodes from "../../../contracts/fixtures/registry/qwen3-tts.nodes.valid.json";
import { Create } from "@/components/create/create";
import { VoicePicker, type VoiceCtx } from "@/components/create/voices";
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
  vi.useRealTimers();
});

type Item = S<"LibraryItem">;
type Workflow = S<"Workflow">;
const byKind = (ns: { kind: string }[]) => Object.fromEntries(ns.map((n) => [n.kind, n]));
const schemas = byKind([...coreNodes.nodes, ...zonosNodes.nodes, ...qwenNodes.nodes]);
const asWf = (w: object) =>
  ({ ...w, node_schemas: schemas, source: "template" }) as unknown as Workflow;
const echo = asWf(workflows.workflows.find((w) => w.id === "echo-voice-over")!);
const zonos = asWf(zonosWfs.workflows[0]);
const qwen = asWf(qwenWfs.workflows.find((w) => w.id === "qwen3-tts-voice-over")!);
const TITLES: Record<string, string> = { zonos3: "ZONOS3", "qwen3-tts": "Qwen3-TTS" };
const ctxOf = (w: Workflow, clone: Workflow): VoiceCtx => ({
  workflow: w,
  familyTitle: (f) => TITLES[f] ?? f,
  clone: { workflow: clone, label: clone.title, session: async () => "s1" },
});

const item = (id: string, name: string, more: Partial<Item>): Item =>
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
    source: "mine",
    family: null,
    loras: [],
    needs: [],
    ...more,
  }) as Item;

const letter = audio(28, { title: "Letter, read aloud", created_at: "2026-11-21T09:00:00Z" });
const interview = audio(41.5, { title: "Interview · take 2", created_at: "2026-11-21T08:00:00Z" });
const short = audio(2, { title: "Too short", created_at: "2026-11-21T09:30:00Z" });
const shot = video(5, 24, { title: "Shot 3 · tree line", created_at: "2026-11-21T07:00:00Z" });
const silent = video(8, 24, {
  title: "No sound",
  video: { duration_s: 8, fps: 24, frames: 192, has_audio: false, poster_url: "/p.png" },
});

/** The API: the library (what POST made joins it), the takes by kind, one take by id, runs that succeed. */
function serve(voices: Item[] = []) {
  const made: unknown[] = [];
  const runs: { workflow_id: string; inputs: Record<string, unknown> }[] = [];
  const lib = [...voices];
  answer("GET", {
    "/v1/studio/library": () => ({ data: { items: lib } }),
    "/v1/studio/takes": ({ params }) => {
      const kind = (params as { query: { kind: string } }).query.kind;
      return {
        data: {
          takes: kind === "audio" ? [letter, interview, short] : [shot, silent],
          next_cursor: null,
        },
      };
    },
    "/v1/studio/takes/{take_id}": ({ params }) => {
      const id = (params as { path: { take_id: string } }).path.take_id;
      const t = [letter, interview, shot].find((x) => x.id === id);
      return t ? { data: t } : { status: 404, error: { detail: "Take not found." } };
    },
    "/v1/studio/sessions/{session_id}/runs": () => ({ data: { runs: [] } }),
    "/v1/studio/sessions/{session_id}/takes": () => ({ data: { takes: [] } }),
    "/v1/studio/hosts": () => ({ data: { hosts: [] } }),
    "/v1/studio/loras": () => ({ data: { loras: [] } }),
    "/v1/studio/takes/{take_id}/uses": () => ({ data: { offers: [] } }),
    "/v1/studio/runs/{run_id}": () => ({
      data: {
        id: "t1",
        status: "succeeded",
        error: null,
        outputs: [{ index: 0, kind: "audio", url: "/files/u/x/t1.wav" }],
      },
    }),
  });
  answer("POST", {
    "/v1/studio/library": ({ body }) => {
      made.push(body);
      const b = body as { group: string; name: string; from_take: S<"VoiceClip"> };
      const x = item("5a0c1e2f-3b4d-4e5f-8a6b-7c8d9e0f1a99", b.name, {
        group: b.group as Item["group"],
        voice: {
          source: b.group as "cloned",
          clip: { take_id: b.from_take.take_id, in: b.from_take.in, out: b.from_take.out },
          consent: true,
        },
      });
      lib.push(x);
      return { status: 201, data: x };
    },
    "/v1/studio/sessions/{session_id}/runs": ({ body }) => {
      runs.push(body as (typeof runs)[number]);
      return { status: 202, data: { id: "t1", status: "queued", outputs: [] } };
    },
  });
  return { made, runs };
}

const pick = (ctx: VoiceCtx) =>
  render(
    <VoicePicker open onClose={() => undefined} value="" onPick={() => undefined} ctx={ctx} />,
  );

async function openClone() {
  const list = await screen.findByRole("dialog", { name: "Voice" });
  fireEvent.click(within(list).getByRole("button", { name: "Clone from a clip" }));
  return screen.findByRole("dialog", { name: "Clone from a clip" });
}
const length = (d: HTMLElement) =>
  within(d).getByText("Length · 3 – 30 s").nextSibling?.textContent;
const button = (d: HTMLElement, name: string) =>
  within(d).getByRole("button", { name }) as HTMLButtonElement;

describe("Clone from a clip on a workflow that clones", () => {
  it("offers takes with sound of 3 s or more; the part stays 3–30 s; consent before Hear it and Save voice", async () => {
    const { made, runs } = serve();
    vi.useFakeTimers({ shouldAdvanceTime: true });
    pick(ctxOf(echo, echo));
    const d = await openClone();
    const takes = within(d).getByRole("list", { name: "Takes" });
    await waitFor(() => expect(within(takes).getAllByRole("button")).toHaveLength(3));
    // newest first; the 2 s take and the silent video are not offered
    expect(
      within(takes)
        .getAllByRole("button")
        .map((b) => b.querySelector("b")?.textContent),
    ).toEqual(["Letter, read aloud", "Interview · take 2", "Shot 3 · tree line"]);
    expect(within(d).getByRole("status").textContent).toBe("Pick a take");

    // a 41.5 s take: out starts at 30 s and cannot pass in + 30
    fireEvent.click(within(takes).getByRole("button", { name: /Interview/ }));
    expect(length(d)).toBe("30.0 s");
    fireEvent.change(within(d).getByLabelText(/^Out ·/), { target: { value: "41" } });
    expect(length(d)).toBe("30.0 s");

    fireEvent.click(within(takes).getByRole("button", { name: /Letter/ }));
    expect(within(d).getByText("audio · wav · 28.0 s", { selector: "span" })).toBeTruthy();
    expect(length(d)).toBe("28.0 s");
    fireEvent.change(within(d).getByLabelText(/^Out ·/), { target: { value: "2" } });
    expect(length(d)).toBe("2.0 s");
    expect(within(d).getByText("clip: the part lasts 2.0 s; a voice needs 3 to 30 s")).toBeTruthy();
    expect(within(d).getByRole("status").textContent).toBe("Tick the box to save");
    fireEvent.click(within(d).getByLabelText(/I have the right to use this voice/));
    expect(within(d).getByRole("status").textContent).toBe("Trim to 3 – 30 s");
    expect(button(d, "Hear it").disabled).toBe(true);
    expect(button(d, "Save voice").disabled).toBe(true);

    fireEvent.change(within(d).getByLabelText(/^Out ·/), { target: { value: "16" } });
    fireEvent.change(within(d).getByLabelText(/^In ·/), { target: { value: "4" } });
    expect(length(d)).toBe("12.0 s");
    expect(within(d).getByRole("status").textContent).toBe("Hear it first");
    expect(button(d, "Save voice").disabled).toBe(true);
    // consent off again: nothing can be saved or heard
    fireEvent.click(within(d).getByLabelText(/I have the right to use this voice/));
    expect(button(d, "Hear it").disabled).toBe(true);
    fireEvent.click(within(d).getByLabelText(/I have the right to use this voice/));

    fireEvent.change(within(d).getByLabelText("Name"), { target: { value: "Kai" } });
    fireEvent.click(button(d, "Hear it"));
    await waitFor(() => expect(runs).toHaveLength(1));
    expect(made).toEqual([
      {
        kind: "voice",
        group: "cloned",
        name: "Kai",
        from_take: { take_id: letter.id, in: 4, out: 16 },
        voice: { consent: true },
      },
    ]);
    expect(runs[0].workflow_id).toBe("echo-voice-over");
    expect(runs[0].inputs["speak.voice"]).toEqual({
      voice_id: "5a0c1e2f-3b4d-4e5f-8a6b-7c8d9e0f1a99",
    });
    expect(String(runs[0].inputs["prompt.text"])).toMatch(/\w/);
    await vi.advanceTimersByTimeAsync(1100);
    expect(await within(d).findByText("Try 1")).toBeTruthy();
    await waitFor(() => expect(button(d, "Save voice").disabled).toBe(false));
    fireEvent.click(button(d, "Save voice"));
    // the voice saved is picked in Mine
    const list = await screen.findByRole("dialog", { name: "Voice" });
    await waitFor(() => expect(within(list).getByText("Kai picked")).toBeTruthy());
    expect(within(list).getByRole("tab", { name: "Mine · 1 fit" })).toBeTruthy();
    expect(made).toHaveLength(1);
  });
});

describe("Clone from a clip on Qwen3-TTS", () => {
  it("no Hear it: the note names the model that clones; Save voice waits only for the tick", async () => {
    const { made } = serve();
    pick(ctxOf(qwen, zonos));
    const d = await openClone();
    fireEvent.click(await within(d).findByRole("button", { name: /Letter/ }));
    expect(within(d).queryByRole("button", { name: "Hear it" })).toBeNull();
    expect(within(d).queryByLabelText("A line to hear it say")).toBeNull();
    expect(
      within(d).getByText("ZONOS3 clones this voice. Qwen3-TTS presets cannot be cloned."),
    ).toBeTruthy();
    expect(button(d, "Save voice").disabled).toBe(true);
    fireEvent.click(within(d).getByLabelText(/I have the right to use this voice/));
    expect(within(d).getByRole("status").textContent).toBe("Ready");
    fireEvent.change(within(d).getByLabelText("Name"), { target: { value: "Grandad" } });
    fireEvent.click(button(d, "Save voice"));
    await waitFor(() => expect(made).toHaveLength(1));
    expect(made[0]).toMatchObject({
      group: "cloned",
      name: "Grandad",
      from_take: { take_id: letter.id, in: 0, out: 28 },
    });
    // Qwen takes no clips: Mine counts every voice, no "fit"
    const list = await screen.findByRole("dialog", { name: "Voice" });
    expect(await within(list).findByRole("tab", { name: "Mine · 1" })).toBeTruthy();
  });
});

describe("The fit on ZONOS3", () => {
  const savannah = item("zonos3/savannah", "Savannah", {
    source: "starter",
    family: "zonos3",
    voice: {
      source: "preset",
      family: "zonos3",
      speaker: "default_0",
      language: "en-US",
      style: "American female",
    },
  });
  const ryan = item("qwen3-tts/ryan", "Ryan", {
    source: "starter",
    family: "qwen3-tts",
    voice: { source: "preset", family: "qwen3-tts", speaker: "Ryan", language: "en" },
  });
  const jon = item("5a0c1e2f-3b4d-4e5f-8a6b-7c8d9e0f1a01", "Jon", {
    group: "cloned",
    voice: {
      source: "cloned",
      clip: { take_id: interview.id, in: 4, out: 16 },
      consent: true,
    },
  });
  const radio = item("5a0c1e2f-3b4d-4e5f-8a6b-7c8d9e0f1a02", "Night radio host", {
    group: "designed",
    voice: {
      source: "designed",
      design: "Female, 40s, smooth and low",
      clip: { take_id: letter.id, in: 0, out: 6 },
      consent: true,
    },
  });
  const teen = item("5a0c1e2f-3b4d-4e5f-8a6b-7c8d9e0f1a03", "Nervous teen", {
    group: "designed",
    voice: { source: "designed", design: "Male, 17, tenor, nervous" },
  });

  it("presets: ZONOS3 first with its language chips, Qwen3-TTS folded with the reason", async () => {
    // more than nine: the family gets its language chips
    const more = [
      "Imogen",
      "Jett",
      "Morgana",
      "Rupert",
      "Sakura",
      "Thaddeus",
      "Willow",
      "Wyatt",
    ].map((n) => item(`zonos3/${n.toLowerCase()}`, n, { ...savannah, id: `zonos3/${n}`, name: n }));
    const gaspard = item("zonos3/gaspard", "Gaspard", {
      ...savannah,
      id: "zonos3/gaspard",
      name: "Gaspard",
      voice: { source: "preset", family: "zonos3", speaker: "x", language: "fr", style: "Male" },
    });
    serve([ryan, savannah, ...more, gaspard]);
    pick(ctxOf(zonos, zonos));
    const list = await screen.findByRole("dialog", { name: "Voice" });
    const chips = await within(list).findByRole("group", { name: "Language" });
    expect(within(chips).getByRole("button", { name: "English" })).toBeTruthy();
    const groups = within(list).getAllByRole("heading", { level: 3 });
    expect(groups[0].textContent).toBe("ZONOS3 · 10 presets");
    expect(within(chips).getByRole("button", { name: "French" })).toBeTruthy();
    // the chips by their names, as the board
    expect(
      within(chips)
        .getAllByRole("button")
        .map((b) => b.textContent),
    ).toEqual(["All", "English", "French"]);
    expect(within(list).getAllByText("American female · English")).toHaveLength(9);
    // English by default: Gaspard waits for French
    expect(within(list).queryByText("Gaspard")).toBeNull();
    const fold = within(list).getByRole("button", { name: /^Qwen3-TTS · 1 preset/ });
    expect(fold.getAttribute("aria-expanded")).toBe("false");
    expect(fold.textContent).toContain("Ryan is for Qwen3-TTS · none of these fit ZONOS3");
    expect(within(list).queryByRole("button", { name: /^Ryan/ })).toBeNull();
  });

  it("Mine · N fit, why each fits, and the reason of one that does not", async () => {
    serve([jon, radio, teen]);
    pick(ctxOf(zonos, zonos));
    const list = await screen.findByRole("dialog", { name: "Voice" });
    fireEvent.click(await within(list).findByRole("tab", { name: "Mine · 2 fit" }));
    const card = (name: string) =>
      [...list.querySelectorAll("[data-pick]")].find((b) => b.textContent?.includes(name))
        ?.textContent;
    await waitFor(() => expect(card("Jon")).toContain("from Interview · take 2 · 0:04–0:16"));
    expect(card("Jon")).toContain("Cloned");
    expect(card("Jon")).toContain("cloned · a voice print of its clip");
    expect(card("Night radio host")).toContain("designed · has its clip");
    expect(card("Nervous teen")).toContain("this workflow cannot use a designed voice");
    expect(within(list).getByText(/^2 of 3 fit ZONOS3\./)).toBeTruthy();
  });
});

describe("Save as voice on an audio take", () => {
  const start = (t: S<"Take">) =>
    render(
      <Create
        initial={{
          sessions: [],
          workflows: [echo],
          sessionId: "s1",
          runs: [
            {
              id: "r1",
              session_id: "s1",
              workflow_id: "echo-voice-over",
              inputs: {},
              status: "succeeded",
              progress: null,
              error: null,
              outputs: [],
              host: "fake",
            } as unknown as S<"Run">,
          ],
          takes: [t],
        }}
      />,
    );

  it("opens Clone from a clip on the take, its part 0 to min(length, 30)", async () => {
    serve();
    start(audio(12, { run_id: "r1", title: "Line 4", made_with: { seed: 3 } as never }));
    fireEvent.click(await screen.findByRole("button", { name: "Save as voice" }));
    const d = await screen.findByRole("dialog", { name: "Clone from a clip" });
    const takes = within(d).getByRole("list", { name: "Takes" });
    expect(within(takes).getAllByRole("button")[0].getAttribute("aria-pressed")).toBe("true");
    expect(within(takes).getAllByRole("button")[0].textContent).toContain("Line 4");
    expect(length(d)).toBe("12.0 s");
    expect((within(d).getByLabelText("Name") as HTMLInputElement).value).toBe("");
  });

  it("a take a designed voice made saves that designed voice with its clip, after the tick", async () => {
    const { made } = serve();
    const t = audio(8, {
      run_id: "r1",
      title: "Old captain try",
      made_with: {
        seed: 3,
        voices: [
          {
            id: "5a0c1e2f-3b4d-4e5f-8a6b-7c8d9e0f1a21",
            name: "Old captain",
            version: 1,
            source: "designed",
          },
        ],
      } as never,
    });
    start(t);
    fireEvent.click(await screen.findByRole("button", { name: "Save as voice" }));
    const d = await screen.findByRole("dialog", { name: "Save as voice" });
    expect((within(d).getByLabelText("Name") as HTMLInputElement).value).toBe("Old captain");
    expect(button(d, "Hear it").disabled).toBe(true);
    fireEvent.click(within(d).getByLabelText(/I have the right to use this voice/));
    fireEvent.click(button(d, "Hear it"));
    await waitFor(() => expect(made).toHaveLength(1));
    expect(made[0]).toEqual({
      kind: "voice",
      group: "designed",
      name: "Old captain",
      from_take: { take_id: t.id, in: 0, out: 8 },
      voice: { consent: true },
    });
  });

  it("another take after a designed try drops the designed draft and makes a cloned voice", async () => {
    const { made } = serve();
    const gone: string[] = [];
    vi.spyOn(api, "DELETE").mockImplementation(((
      _: string,
      init: { params: { path: { item_id: string } } },
    ) => {
      gone.push(init.params.path.item_id);
      return Promise.resolve({
        data: undefined,
        error: undefined,
        response: new Response(null, { status: 204 }),
      });
    }) as never);
    const t = audio(8, {
      run_id: "r1",
      title: "Old captain try",
      made_with: {
        seed: 3,
        voices: [
          {
            id: "5a0c1e2f-3b4d-4e5f-8a6b-7c8d9e0f1a21",
            name: "Old captain",
            version: 1,
            source: "designed",
          },
        ],
      } as never,
    });
    start(t);
    fireEvent.click(await screen.findByRole("button", { name: "Save as voice" }));
    const d = await screen.findByRole("dialog", { name: "Save as voice" });
    fireEvent.click(within(d).getByLabelText(/I have the right to use this voice/));
    fireEvent.click(button(d, "Hear it"));
    await waitFor(() => expect(made).toHaveLength(1));
    const takes = within(d).getByRole("list", { name: "Takes" });
    fireEvent.click(await within(takes).findByRole("button", { name: /Letter, read aloud/ }));
    await waitFor(() => expect(button(d, "Hear it").disabled).toBe(false));
    fireEvent.click(button(d, "Hear it"));
    await waitFor(() => expect(made).toHaveLength(2));
    expect(gone).toEqual(["5a0c1e2f-3b4d-4e5f-8a6b-7c8d9e0f1a99"]);
    expect(made[1]).toMatchObject({
      group: "cloned",
      from_take: { take_id: letter.id, in: 0, out: 28 },
    });
  });
});
