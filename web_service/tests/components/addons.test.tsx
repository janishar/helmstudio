// @vitest-environment jsdom
// Optional add-ons in Create (API 1.36; docs/mock/CreateEnhance.dc.html, Main.dc.html "Other states"): an enhancer on
// no live host greys the No enhance chip (held on), the presets that need it and Think, with the reason, and the run
// goes without enhance; on one host only, Run on greys the other host while enhance is on. While No enhance is on the
// Enhance settings show dimmed with "Enhance is off"; turning Think on switches No enhance off and says so.
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import h3iWorkflows from "../../../contracts/fixtures/registry/minimax-h3-image.workflows.valid.json";
import h3iNodes from "../../../contracts/fixtures/registry/minimax-h3-image.nodes.valid.json";
import { Create } from "@/components/create/create";
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
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

const workflow = {
  ...h3iWorkflows.workflows[0],
  node_schemas: Object.fromEntries(h3iNodes.nodes.map((n) => [n.kind, n])),
  source: "template",
} as unknown as S<"Workflow">;

const modelHost = (h: string) => ({
  host: h,
  live: true,
  envs: ["torch"],
  models_root: null,
  free_bytes: null,
  total_bytes: null,
});
const models = (enhancerOn: string[]) => ({
  hosts: [modelHost("gcp"), modelHost("mac")],
  licences: [],
  models: [
    {
      id: "minimax-h3-image",
      family: "minimax-h3-image",
      title: "MiniMax H3 Image",
      media: ["image"],
      runtimes: [{ runtime: "torch", env: "torch" }],
      variants: [
        {
          id: "base",
          title: "Base",
          precisions: ["int8"],
          dir: "base",
          licence: null,
          gated: false,
          download_bytes: null,
          components: [],
          hosts: ["gcp", "mac"].map((host) => ({ host, state: "present" })),
        },
      ],
      addons: [
        {
          component: "enhancer",
          title: "Enhancer",
          dir: null,
          licence: null,
          gated: false,
          download_bytes: null,
          hosts: ["gcp", "mac"].map((host) => ({
            host,
            state: enhancerOn.includes(host) ? "present" : "missing",
          })),
        },
      ],
    },
  ],
});
const host = (name: string, kind: string, device: string) => ({
  host: name,
  env: "torch",
  state: "idle",
  updated_at: "2026-11-22T00:00:00Z",
  device: { kind, name: device, memory_total_gb: 96 },
});

// a run already in the session, so the aside is the Inspector (not the empty session's Elements)
const done = {
  id: "r0",
  session_id: "s1",
  workflow_id: workflow.id,
  inputs: {},
  status: "succeeded",
  progress: null,
  error: null,
  outputs: [],
  host: "mac",
} as unknown as S<"Run">;

function start(enhancerOn: string[]) {
  const runs: Record<string, unknown>[] = [];
  answer("GET", {
    "/v1/studio/sessions/{session_id}/runs": () => ({ data: { runs: [done] } }),
    "/v1/studio/sessions/{session_id}/takes": () => ({ data: { takes: [] } }),
    "/v1/studio/library": () => ({ data: { items: [] } }),
    "/v1/studio/loras": () => ({ data: { loras: [] } }),
    "/v1/studio/models": () => ({ data: models(enhancerOn) }),
    "/v1/studio/hosts": () => ({
      data: {
        hosts: [host("mac", "mps", "Apple M5 Pro"), host("gcp", "cuda", "RTX PRO 6000")],
      },
    }),
  });
  answer("POST", {
    "/v1/studio/runs/plan": () => ({ data: { host: "mac", peak_gb: 21, reason: null } }),
    "/v1/studio/sessions/{session_id}/runs": ({ body }) => {
      runs.push(body as Record<string, unknown>);
      return {
        status: 202,
        data: {
          id: `r${runs.length}`,
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
  render(
    <Create
      initial={{ sessions: [], workflows: [workflow], sessionId: "s1", runs: [done], takes: [] }}
    />,
  );
  const form = screen.getByRole("form", { name: "Composer" });
  const box = form.querySelector("textarea");
  if (box) fireEvent.change(box, { target: { value: "a fox at dusk" } }); // Generate waits for a prompt
  return { runs, composer: form };
}

const inspector = () => screen.getByRole("complementary", { name: "Inspector" });
const enhanceChip = (c: HTMLElement) => within(c).getByRole("button", { name: /^No enhance/ });
const think = () => within(inspector()).getByRole("switch", { name: "Think" });
const heading = () => within(inspector()).getByRole("heading", { name: /^Enhance/ });
const rows = () =>
  [...document.querySelectorAll<HTMLElement>("#run-on [role=menuitemradio]")].map((b) => ({
    text: b.textContent,
    disabled: b.getAttribute("aria-disabled") === "true",
  }));

describe("Create: optional add-ons", () => {
  it("on no live host: the Enhance controls greyed with the reason, the run goes without enhance", async () => {
    const { runs, composer } = start([]);
    await waitFor(() => expect(enhanceChip(composer).getAttribute("aria-disabled")).toBe("true"));
    const chip = enhanceChip(composer);
    expect([chip.textContent, chip.getAttribute("aria-pressed")]).toEqual([
      "No enhance onEnhancer not downloaded",
      "true",
    ]);
    expect(chip.title).toBe(
      "Enhancer not downloaded. An admin downloads it in Models, under the model's optional add-ons.",
    );
    fireEvent.click(chip); // greyed: held on
    expect(enhanceChip(composer).textContent).toMatch(/^No enhance on/);
    // the presets that switch the enhancer on are greyed; Plain is not
    const preset = (name: string) => within(inspector()).getByRole("button", { name });
    expect(preset("Plain").getAttribute("aria-disabled")).toBeNull();
    for (const name of ["Enhanced", "Enhanced, think"]) {
      expect(preset(name).getAttribute("aria-disabled")).toBe("true");
      expect(preset(name).title).toBe("Enhancer not downloaded");
    }
    fireEvent.click(preset("Enhanced, think"));
    // Think: greyed with the reason under it, and inert
    expect(think().getAttribute("aria-disabled")).toBe("true");
    expect(within(inspector()).getByText("Enhancer not downloaded")).toBeTruthy();
    fireEvent.click(think());
    expect(think().getAttribute("aria-checked")).toBe("false");
    expect(heading().textContent).not.toContain("is off"); // the reason is the add-on, not the switch
    fireEvent.click(composer.querySelector<HTMLButtonElement>('button[type="submit"]')!);
    await waitFor(() => expect(runs).toHaveLength(1));
    const inputs = runs[0].inputs as Record<string, unknown>;
    expect([inputs["enhance.bypass"], inputs["enhance.think"]]).toEqual([undefined, undefined]);
  });

  it("on one host only: Run on greys the other host while enhance is on", async () => {
    const { composer } = start(["mac"]);
    await waitFor(() => expect(rows()).toHaveLength(3));
    expect(enhanceChip(composer).getAttribute("aria-disabled")).toBeNull();
    expect(rows()[2]).toEqual({ text: "NVIDIAgcp · RTX PRO 6000", disabled: false });
    fireEvent.click(enhanceChip(composer)); // No enhance off: the enhancer runs
    expect(enhanceChip(composer).textContent).toBe("No enhance off");
    await waitFor(() =>
      expect(rows()[2]).toEqual({
        text: "NVIDIAgcp · RTX PRO 6000enhancer not downloaded on gcp",
        disabled: true,
      }),
    );
    expect(rows()[1].disabled).toBe(false);
  });

  it("No enhance on: Enhance is off, dimmed; Think on switches No enhance off and says why", async () => {
    const { composer } = start(["gcp", "mac"]);
    await waitFor(() => expect(heading().textContent).toContain("Enhance is off"));
    expect(enhanceChip(composer).textContent).toBe("No enhance on");
    expect(think().className).toContain("opacity-55");
    expect(think().getAttribute("aria-disabled")).toBeNull();
    fireEvent.click(think());
    expect(think().getAttribute("aria-checked")).toBe("true");
    expect(enhanceChip(composer).textContent).toBe("No enhance off");
    expect(await screen.findByText("No enhance turned off: Think needs the enhancer")).toBeTruthy();
    expect(heading().textContent).not.toContain("is off");
    expect(think().className).not.toContain("opacity-55");
  });
});
