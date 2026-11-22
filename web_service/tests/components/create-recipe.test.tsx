// @vitest-environment jsdom
// Create on the LTX text and image to video templates: Quality's Recipe tile, two-stage or one-stage (the owner's
// Q17, docs/handoff.md second round; StudioLayout inspector choices, API 1.26).
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import ltxWorkflows from "../../../contracts/fixtures/registry/ltx.workflows.valid.json";
import ltxNodes from "../../../contracts/fixtures/registry/ltx.nodes.valid.json";
import coreNodes from "../../../contracts/fixtures/registry/nodes.valid.json";
import { Create } from "@/components/create/create";
import { unplace } from "@/lib/studio";
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

/** The GETs Create makes, the takes picker's one take, and the plan answering `gb`; the plan bodies sent. */
function serve(gb: number | null, models?: unknown) {
  answer("GET", {
    ...(models ? { "/v1/studio/models": () => ({ data: models }) } : {}),
    "/v1/studio/sessions/{session_id}/runs": () => ({ data: { runs: [] } }),
    "/v1/studio/sessions/{session_id}/takes": () => ({ data: { takes: [] } }),
    "/v1/studio/library": () => ({ data: { items: [] } }),
    "/v1/studio/hosts": () => ({ data: { hosts: [] } }),
    "/v1/studio/loras": () => ({ data: { loras: [] } }),
    "/v1/studio/takes": () => ({ data: { takes: [], next_cursor: null } }),
  });
  const plans: S<"RunRequest">[] = [];
  answer("POST", {
    "/v1/studio/runs/plan": ({ body }) => {
      plans.push(body as S<"RunRequest">);
      return { data: { host: "mac", peak_gb: gb, reason: null } };
    },
  });
  return plans;
}

const start = (w: S<"Workflow">) =>
  render(
    <Create
      initial={{
        sessions: [],
        workflows: [w],
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

const inspector = () => screen.getByRole("complementary", { name: "Inspector" });
const section = (title: string) =>
  within(inspector())
    .getAllByRole("heading", { level: 2 })
    .find((h) => h.textContent?.startsWith(title))!
    .closest("section")!;

describe("Create: LTX's Recipe tile (Q17)", () => {
  it("text to video: Recipe leads Quality; one-stage bypasses upscale and stage 2 and says the clip's size", async () => {
    const plans = serve(30);
    start(ltx("ltx-text-to-video"));
    const quality = section("Quality");
    const recipe = () => within(quality).getByRole("button", { name: /^Recipe/ });
    expect(recipe().textContent).toBe("RecipeTwo-stage");
    expect(within(quality).getByText("Stage 2 steps")).toBeTruthy();
    // it leads the section's tiles (docs/mock/TextToVideo.dc.html)
    expect(quality.querySelector(".grid")!.firstElementChild!.textContent).toContain("Recipe");
    expect(inspector().textContent).toContain("stage 1 runs at 352 × 224");
    fireEvent.click(recipe());
    const pick = within(quality).getByRole("group", { name: "Recipe" });
    fireEvent.click(within(pick).getByRole("button", { name: "One-stage" }));
    expect(recipe().textContent).toBe("RecipeOne-stage");
    expect(inspector().textContent).toContain("one-stage: the clip is 352 × 224");
    await waitFor(
      () => {
        expect(plans.at(-1)?.inputs?.["upscale.bypass"]).toBe(true);
        expect(plans.at(-1)?.inputs?.["sample2.bypass"]).toBe(true);
      },
      { timeout: 2000 },
    );
    expect(within(inspector()).queryByRole("switch", { name: /Bypass/ })).toBeNull(); // the paths are not drawn
    // the bypassed stage's tile goes; the plan is the whole workflow's, so the peak reads as a ceiling
    expect(within(quality).queryByText("Stage 2 steps")).toBeNull();
    expect(await screen.findByText("up to 30 GB")).toBeTruthy();
    if (!within(quality).queryByRole("group", { name: "Recipe" })) fireEvent.click(recipe());
    fireEvent.click(
      within(within(quality).getByRole("group", { name: "Recipe" })).getByRole("button", {
        name: "Two-stage",
      }),
    );
    expect(within(quality).getByText("Stage 2 steps")).toBeTruthy();
  });

  it("image to video has it too; a deleted node takes its keys out of the choice", () => {
    serve(null);
    start(ltx("ltx-image-to-video"));
    expect(within(section("Quality")).getByRole("button", { name: /^Recipe/ }).textContent).toBe(
      "RecipeTwo-stage",
    );
    const studio = ltx("ltx-image-to-video").studio!;
    const q = (l: typeof studio) => l.inspector!.find((s) => s.title === "Quality")!;
    expect(q(unplace(studio, (p) => p === "upscale.bypass")).choices![0].options[1].set).toEqual({
      "sample2.bypass": true,
      "detail.bypass": true,
      "sample1.detail": false,
      "sample2.detail": false,
    });
    // every key gone: no option left, so no choice
    expect(
      q(unplace(studio, (p) => p.endsWith(".bypass") || p.endsWith(".detail"))).choices,
    ).toBeUndefined();
  });

  it("Recipe: Detail, its line in the menu; greyed while the Detailer is on no live host (plan 19 phase 4)", async () => {
    const models = (detailer: string) => ({
      hosts: [
        {
          host: "mac",
          live: true,
          envs: ["mlx"],
          models_root: null,
          free_bytes: null,
          total_bytes: null,
        },
      ],
      licences: [],
      models: [
        {
          id: "ltx-2.5",
          family: "ltx",
          title: "LTX-2.5",
          media: ["video", "audio"],
          runtimes: [{ runtime: "mlx", env: "mlx" }],
          variants: [
            {
              id: "distilled",
              title: "Distilled",
              precisions: ["int8"],
              dir: "ltx-2.5",
              licence: null,
              gated: false,
              download_bytes: null,
              components: [],
              hosts: [{ host: "mac", state: "present" }],
            },
          ],
          addons: [
            {
              component: "ic_lora",
              title: "Detailer",
              dir: "ltx-2.5",
              licence: null,
              gated: true,
              download_bytes: null,
              hosts: [{ host: "mac", state: detailer }],
            },
          ],
        },
      ],
    });
    const quality = () => section("Quality");
    const option = (name: string) => {
      if (!within(quality()).queryByRole("group", { name: "Recipe" }))
        fireEvent.click(within(quality()).getByRole("button", { name: /^Recipe/ }));
      return within(within(quality()).getByRole("group", { name: "Recipe" })).getByRole("button", {
        name,
      });
    };
    serve(30, models("missing"));
    start(ltx("ltx-text-to-video"));
    await waitFor(() => expect(option("Detail").getAttribute("aria-disabled")).toBe("true"));
    expect(option("Detail").textContent).toContain("Detailer not downloaded");
    expect(option("Two-stage").textContent).toBe("Two-stagehalf size, then ×2 and 3 refine steps");
    fireEvent.click(option("Detail")); // greyed: nothing changes
    expect(within(quality()).getByRole("button", { name: /^Recipe/ }).textContent).toBe(
      "RecipeTwo-stage",
    );
    cleanup();
    const plans = serve(30, models("present"));
    start(ltx("ltx-text-to-video"));
    await waitFor(() => expect(option("Detail").getAttribute("aria-disabled")).toBeNull());
    fireEvent.click(option("Detail"));
    await waitFor(() => {
      const inputs = plans.at(-1)?.inputs ?? {};
      expect([inputs["detail.bypass"], inputs["sample1.detail"], inputs["sample2.detail"]]).toEqual(
        [false, true, true],
      );
    });
  });
});
