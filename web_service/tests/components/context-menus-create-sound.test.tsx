// @vitest-environment jsdom
// Create's right-click menus of music and sound (plan 38 P4, docs/mock/CreateMusicSound.dc.html frames 3d, 4b, 5c,
// 5d; the shared menu of components/context-menu.tsx): a song's take (Repaint…, Cover…, Extend…; one not offered
// greyed with why), a variation (Vary… in place of Cover…), a sound made from a video (Save with the video, the mux
// job polled to its new video take) and the video made with it (Full screen, Sound only).
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import workflows from "../../../contracts/fixtures/registry/workflows.valid.json";
import coreNodes from "../../../contracts/fixtures/registry/nodes.valid.json";
import { Create } from "@/components/create/create";
import type { S } from "@/lib/api";
import { answer, audio, video } from "./helpers";

vi.mock("@/lib/runs", async (orig) => ({
  ...(await orig<typeof import("@/lib/runs")>()),
  followSession: () => () => undefined,
}));

beforeAll(() => {
  globalThis.requestAnimationFrame ??= (f: FrameRequestCallback) =>
    setTimeout(() => f(0), 0) as unknown as number;
  HTMLElement.prototype.hidePopover ??= () => undefined;
  HTMLElement.prototype.showPopover ??= () => undefined;
  Element.prototype.scrollIntoView ??= () => undefined;
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  localStorage.clear();
});

const schemas = Object.fromEntries(
  coreNodes.nodes.map((n) => [n.kind, n]),
) as unknown as S<"Workflow">["node_schemas"];
const SOUND: Record<string, S<"SoundClass">> = {
  "echo-music": "music",
  "echo-repaint": "music",
  "echo-music-extend": "music",
  "echo-sound": "sound",
  "echo-vary": "sound",
  "echo-sound-video": "sound",
};
const wf = (id: string) =>
  ({
    ...workflows.workflows.find((w) => w.id === id),
    node_schemas: schemas,
    source: "template",
    sound: SOUND[id],
  }) as unknown as S<"Workflow">;
const run = (id: string, workflow_id: string) =>
  ({
    id,
    session_id: "s1",
    workflow_id,
    inputs: {},
    status: "succeeded",
    progress: null,
    error: null,
    outputs: [],
    host: "fake",
  }) as unknown as S<"Run">;
const made = (workflow_id: string, seed: number, inputs = {}) =>
  ({ seed, workflow_id, inputs, prompt: "x" }) as unknown as S<"Take">["made_with"];
const offer = (workflow_id: string, action: string) =>
  ({
    workflow_id,
    title: workflow_id,
    action,
    family: "echo",
    media: "audio",
    param: "x.source",
    inputs: {},
  }) as S<"TakeOffer">;

const labels = () =>
  [...document.querySelectorAll<HTMLElement>('.cm [data-lv="0"]')].map(
    (b) => b.querySelector(".lb")!.textContent,
  );
const item = (name: string) =>
  [...document.querySelectorAll<HTMLElement>(".cm [data-lv]")].find((x) =>
    x.querySelector(".lb")!.textContent!.startsWith(name),
  )!;
const header = () => document.querySelector(".cm .hd")?.textContent;
const rightClick = (el: Element) =>
  fireEvent.contextMenu(el, { button: 2, clientX: 100, clientY: 10 });

function start(takes: S<"Take">[], runs: S<"Run">[], uses: S<"TakeOffer">[] = [], more = {}) {
  answer("GET", {
    "/v1/studio/sessions/{session_id}/runs": () => ({ data: { runs } }),
    "/v1/studio/sessions/{session_id}/takes": () => ({ data: { takes } }),
    "/v1/studio/library": () => ({ data: { items: [] } }),
    "/v1/studio/hosts": () => ({ data: { hosts: [] } }),
    "/v1/studio/loras": () => ({ data: { loras: [] } }),
    "/v1/studio/takes/{take_id}/uses": () => ({ data: { uses } }),
    ...more,
  });
  render(
    <Create
      initial={{
        sessions: [],
        workflows: Object.keys(SOUND).map(wf),
        sessionId: "s1",
        runs,
        takes,
      }}
    />,
  );
}
const strip = () => screen.getByRole("group", { name: "Takes" });

describe("Create's music and sound menus", () => {
  it("3d a song's take: Play, Use in › Timeline, Repaint…, Cover… greyed with why, Extend…, Download, Delete", async () => {
    const song = audio(130, { run_id: "m1", sound: "music", made_with: made("echo-music", 31) });
    start(
      [song],
      [run("m1", "echo-music")],
      [offer("echo-repaint", "Repaint"), offer("echo-music-extend", "Extend")],
    );
    rightClick(within(strip()).getByRole("button", { name: "Take, seed 31, 2:10" }));
    expect(header()).toBe("Seed 31 · 2:10");
    // greyed while its offers are read, then the offered ones open
    await waitFor(() => expect(item("Repaint…").getAttribute("aria-disabled")).toBeNull());
    expect(labels()).toEqual([
      "Play",
      "Use in",
      "Repaint…",
      "Cover…",
      "Extend…",
      "Download",
      "Delete",
    ]);
    expect(item("Cover…").getAttribute("aria-disabled")).toBe("true");
    expect(item("Play").textContent).toContain("Space");
    fireEvent.click(item("Use in"));
    expect(item("Timeline")).toBeTruthy();
  });

  it("4b a variation: the same menu with Vary… in place of Cover…; Repaint… opens the range on the stage", async () => {
    localStorage.setItem("create-variations:s1", JSON.stringify({ runs: ["v1", "v2"], kept: [] }));
    const fx = (n: number) =>
      audio(6, { run_id: `v${n}`, sound: "sound", made_with: made("echo-sound", 40 + n) });
    start(
      [fx(1), fx(2)],
      [run("v2", "echo-sound"), run("v1", "echo-sound")],
      [offer("echo-vary", "Vary")],
    );
    const grid = await screen.findByRole("group", { name: "Variations" });
    rightClick(within(grid).getByRole("button", { name: "Variation, seed 42 · 0:06" }));
    await waitFor(() => expect(item("Vary…").getAttribute("aria-disabled")).toBeNull());
    expect(labels()).toEqual([
      "Play",
      "Use in",
      "Repaint…",
      "Vary…",
      "Extend…",
      "Download",
      "Delete",
    ]);
    expect(item("Repaint…").getAttribute("aria-disabled")).toBe("true");
    expect(item("Vary…").getAttribute("aria-disabled")).toBeNull();
  });

  it("5c a sound made from a video: Save with the video makes the video take by a mux job", async () => {
    const src = video(5, 25, { title: "Fox trots in" });
    const fx = audio(5, {
      run_id: null as never,
      sound: "sound",
      made_with: made("echo-sound-video", 7, { "foley.source": { take_id: src.id } }),
    });
    const muxed = video(5, 25, { title: "Fox trots in", parents: [src.id, fx.id] });
    start([fx, src], [], [], {
      "/v1/studio/media-jobs/{job_id}": () => ({
        data: { id: "j1", status: "succeeded", take_id: muxed.id },
      }),
      "/v1/studio/takes/{take_id}": () => ({ data: muxed }),
    });
    const posts: unknown[] = [];
    answer("POST", {
      "/v1/studio/takes/{take_id}/mux": (init) => {
        posts.push(init);
        return { status: 202, data: { job: { id: "j1", status: "running", take_id: null } } };
      },
    });
    rightClick(within(strip()).getByRole("button", { name: "Take, seed 7, 0:05" }));
    await waitFor(() => expect(item("Use in").getAttribute("aria-disabled")).toBeNull());
    expect(labels()).toEqual(["Play", "Use in", "Save with the video", "Download", "Delete"]);
    fireEvent.click(item("Save with the video"));
    await waitFor(() =>
      expect(posts).toEqual([
        { params: { path: { take_id: fx.id } }, body: { video_take_id: src.id } },
      ]),
    );
    // the new video take joins the strip; it plays its sound, so Sound only opens the take it was made with
    await waitFor(
      () =>
        expect(
          within(strip()).getAllByRole("button", { name: /^Take, Fox trots in/ }),
        ).toHaveLength(2),
      { timeout: 4000 },
    );
  });

  it("5d the video made with its sound: Play, Full screen, Use in, Sound only (the sound take of its run)", async () => {
    const fx = audio(5, { run_id: "f1", sound: "sound", made_with: made("echo-sound-video", 7) });
    const vid = video(5, 25, { run_id: "f1", title: "Fox trots in · with sound" });
    start([vid, fx], [run("f1", "echo-sound-video")]);
    rightClick(within(strip()).getByRole("button", { name: /with sound/ }));
    await waitFor(() => expect(labels()).toContain("Sound only"));
    expect(labels()).toEqual(["Play", "Full screen", "Use in", "Sound only", "Download", "Delete"]);
    fireEvent.click(item("Sound only"));
    expect(await screen.findByRole("group", { name: "Waveform of the take" })).toBeTruthy();
  });
});
