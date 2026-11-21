// @vitest-environment jsdom
// Create on an LTX video workflow (docs/mock/TextToVideo.dc.html, ImageToVideo.dc.html, VideoSource.dc.html): the
// composer's card tiles, + and @, the model chip "<family> · <variant>", the Seeds stepper, the Inspector without a
// Model or Frame rate tile, the canvas line with stage 1's size, and Retake's chips and switch line.
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import ltxWorkflows from "../../../contracts/fixtures/registry/ltx.workflows.valid.json";
import ltxNodes from "../../../contracts/fixtures/registry/ltx.nodes.valid.json";
import coreNodes from "../../../contracts/fixtures/registry/nodes.valid.json";
import { Create, videoTitle } from "@/components/create/create";
import type { S } from "@/lib/api";
import { answer } from "./helpers";

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
  };
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

const schemas = Object.fromEntries(
  [...coreNodes.nodes, ...ltxNodes.nodes].map((n) => [n.kind, n]),
) as unknown as S<"Workflow">["node_schemas"];
const ltx = (id: string) =>
  ({
    ...ltxWorkflows.workflows.find((w) => w.id === id),
    node_schemas: schemas,
    source: "template",
  }) as unknown as S<"Workflow">;

function serve() {
  answer("GET", {
    "/v1/studio/sessions/{session_id}/runs": () => ({ data: { runs: [] } }),
    "/v1/studio/sessions/{session_id}/takes": () => ({ data: { takes: [] } }),
    "/v1/studio/library": () => ({ data: { items: [] } }),
    "/v1/studio/hosts": () => ({ data: { hosts: [] } }),
    "/v1/studio/loras": () => ({ data: { loras: [] } }),
  });
}

const start = (w: S<"Workflow">) =>
  render(
    <Create
      initial={{
        sessions: [],
        // image to video: the template text to video's First frame tile opens
        workflows: [w, ltx("ltx-image-to-video")],
        sessionId: "s1",
        runs: [
          {
            id: "r1",
            session_id: "s1",
            workflow_id: w.id,
            inputs: {},
            status: "queued",
            progress: null,
            error: null,
            outputs: [],
            host: "mac",
          } as unknown as S<"Run">,
        ],
        takes: [],
      }}
    />,
  );

describe("Create on LTX video", () => {
  it("text to video: card tiles, + and @, the model chip, Seeds, no Model or Frame rate tile", async () => {
    serve();
    start(ltx("ltx-text-to-video"));
    const composer = screen.getByRole("form", { name: "Composer" });
    // the family leads the model chip and the top bar's label
    expect(
      await within(composer).findByRole("button", { name: "LTX-2.5 · Distilled" }),
    ).toBeTruthy();
    expect(
      screen.getByRole("button", {
        name: /^Workflow: LTX-2\.5 · Text to video · add a first frame/,
      }),
    ).toBeTruthy();
    // the chip's menu picks the variant (no Model tile in the Inspector), as rows (docs/mock/RunOn.dc.html)
    const menu = composer.querySelector<HTMLElement>("#model-pick")!;
    expect(menu.getAttribute("aria-label")).toBe("Model");
    expect([...menu.querySelectorAll("[role=menuitemradio]")].map((b) => b.textContent)).toEqual([
      "Distilled",
      "Dev",
      "Distilled fp8",
      "Dev int8",
    ]);
    const inspector = screen.getByRole("complementary", { name: "Inspector" });
    expect(within(inspector).queryByText("Model")).toBeNull();
    expect(within(inspector).queryByText("Frame rate")).toBeNull();
    // + and @ before the chips; Seeds although the block does not place the seed
    expect(
      within(composer).getByRole("button", { name: "Add image, video or audio" }),
    ).toBeTruthy();
    expect(within(composer).getByRole("button", { name: "Insert element" })).toBeTruthy();
    expect(within(composer).getByRole("group", { name: "Seeds to queue" })).toBeTruthy();
    // the tiles: label over value, Elements Add (the owner's Q16: "Add" everywhere)
    const elements = within(composer).getByRole("button", { name: /^Elements/ });
    expect(elements.textContent).toBe("ElementsAdd");
    expect(elements.className).toContain("flex-[1_1_130px]");
    expect(
      within(composer).getByRole("button", { name: "First frame: None · text only" }).textContent,
    ).toBe("First frameNone · text only");
    // the canvas line: no megapixels, stage 1's size
    expect(
      within(inspector).getByText("704 × 448 · on the 64 px grid · stage 1 runs at 352 × 224"),
    ).toBeTruthy();
    expect(within(composer).getByRole("button", { name: "Audio on" })).toBeTruthy();
  });

  it("Seeds 3 queues three runs with consecutive seeds although the layout does not place the seed", async () => {
    serve();
    const bodies: { inputs: Record<string, unknown> }[] = [];
    answer("POST", {
      "/v1/studio/sessions/{session_id}/runs": ({ body }) => {
        bodies.push(body as (typeof bodies)[number]);
        return {
          status: 202,
          data: {
            id: `r${bodies.length + 1}`,
            session_id: "s1",
            workflow_id: "ltx-text-to-video",
            inputs: {},
            status: "queued",
            progress: null,
            error: null,
            outputs: [],
            host: "mac",
          },
        };
      },
    });
    const w = ltx("ltx-text-to-video");
    start({
      ...w,
      nodes: {
        ...w.nodes,
        prompt: { ...w.nodes.prompt, params: { text: "a fox" } },
        seed: { ...w.nodes.seed, params: { seed: 42 } },
      },
    });
    const composer = screen.getByRole("form", { name: "Composer" });
    const more = within(composer).getByRole("button", { name: "More seeds" });
    fireEvent.click(more);
    fireEvent.click(more);
    fireEvent.click(composer.querySelector<HTMLElement>('button[type="submit"]')!);
    await waitFor(() => expect(bodies).toHaveLength(3));
    expect(bodies.map((b) => b.inputs["seed.seed"])).toEqual([42, 43, 44]);
  });

  it("+ opens the First frame picker", async () => {
    serve();
    start(ltx("ltx-text-to-video"));
    const composer = screen.getByRole("form", { name: "Composer" });
    const picker = () =>
      [...document.querySelectorAll("dialog")]
        .filter((d) => d.open)
        .map((d) => d.getAttribute("aria-label"));
    expect(picker()).toEqual([]);
    fireEvent.click(within(composer).getByRole("button", { name: "Add image, video or audio" }));
    await waitFor(() => expect(picker()).toHaveLength(1));
  });

  it("retake: the Dev-only chip, Regenerate audio pressed, its line, CFG to one decimal", async () => {
    serve();
    start(ltx("ltx-retake"));
    const composer = screen.getByRole("form", { name: "Composer" });
    const chip = await within(composer).findByText("LTX-2.5 · Dev");
    expect(chip.getAttribute("title")).toBe("This workflow runs on Dev only");
    // a label without its state looks pressed, and clicking it lets go
    const regen = within(composer).getByRole("button", { name: "Regenerate audio" });
    expect(regen.getAttribute("aria-pressed")).toBe("true");
    expect(regen.className).toContain("aria-pressed:bg-t1");
    fireEvent.click(regen);
    expect(regen.getAttribute("aria-pressed")).toBe("false");
    expect(
      screen.getByRole("button", { name: /^Workflow: LTX-2\.5 · Retake a section · the rest/ }),
    ).toBeTruthy();
    const inspector = screen.getByRole("complementary", { name: "Inspector" });
    expect(
      within(inspector).getByText(
        "Off: the sound is not regenerated (it is re-encoded through the audio VAE).",
      ),
    ).toBeTruthy();
    expect(within(inspector).getByRole("button", { name: /CFG\s*3\.0/ })).toBeTruthy();
  });

  it("videoTitle: the family, then the action when it says more than the title's rest", () => {
    expect(videoTitle({ title: "LTX-2.5 Retake", action: "Retake a section" }, "LTX-2.5")).toBe(
      "LTX-2.5 · Retake a section",
    );
    expect(videoTitle({ title: "LTX-2.5 Image to video", action: "Animate" }, "LTX-2.5")).toBe(
      "LTX-2.5 · Image to video",
    );
    expect(videoTitle({ title: "Echo video" }, "LTX-2.5")).toBe("Echo video");
  });
});
