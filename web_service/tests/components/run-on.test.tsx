// @vitest-environment jsdom
// Create's Run on (docs/mock/RunOn.dc.html): Auto by default sends no host; a host row is greyed with its reason; a
// pick sends the host (the run and the plan); the version menu's rows, dimmed under a host and still pickable (Run on
// back to Auto, with the note); a chosen host that went offline warns and Generate queues nothing.
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import ltxWorkflows from "../../../contracts/fixtures/registry/ltx.workflows.valid.json";
import ltxNodes from "../../../contracts/fixtures/registry/ltx.nodes.valid.json";
import coreNodes from "../../../contracts/fixtures/registry/nodes.valid.json";
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

const schemas = Object.fromEntries(
  [...coreNodes.nodes, ...ltxNodes.nodes].map((n) => [n.kind, n]),
) as unknown as S<"Workflow">["node_schemas"];
// a torch workflow here, so both hosts have its env and only the versions decide
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
        variant("dev", "Dev", ["mps"], "mac"),
        variant("distilled-fp8", "Distilled fp8", ["cuda"], "gcp"),
        variant("dev-int8", "Dev int8", ["cuda"], "gcp"),
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
});

function start(gcp = "idle") {
  let gcpState = gcp;
  const runs: Record<string, unknown>[] = [];
  const plans: Record<string, unknown>[] = [];
  answer("GET", {
    "/v1/studio/sessions/{session_id}/runs": () => ({ data: { runs: [] } }),
    "/v1/studio/sessions/{session_id}/takes": () => ({ data: { takes: [] } }),
    "/v1/studio/library": () => ({ data: { items: [] } }),
    "/v1/studio/loras": () => ({ data: { loras: [] } }),
    "/v1/studio/models": () => ({ data: models }),
    "/v1/studio/hosts": () => ({
      data: {
        hosts: [
          host("mac", "mps", "Apple M5 Pro", "idle"),
          host("gcp", "cuda", "RTX PRO 6000", gcpState),
        ],
      },
    }),
  });
  answer("POST", {
    "/v1/studio/runs/plan": ({ body }) => {
      plans.push(body as Record<string, unknown>);
      return { data: { host: "mac", peak_gb: 21, reason: null } };
    },
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
    <Create initial={{ sessions: [], workflows: [workflow], sessionId: "s1", runs: [], takes: [] }} />,
  );
  return {
    runs,
    plans,
    composer: screen.getByRole("form", { name: "Composer" }),
    setGcp: (s: string) => (gcpState = s),
  };
}

const menu = () => document.getElementById("run-on")!;
const rows = () =>
  [...menu().querySelectorAll<HTMLElement>("[role=menuitemradio]")].map((b) => ({
    text: b.textContent,
    checked: b.getAttribute("aria-checked") === "true",
    disabled: b.getAttribute("aria-disabled") === "true",
  }));
const versions = () =>
  [...document.querySelectorAll<HTMLElement>("#model-pick [role=menuitemradio]")].map(
    (b) => `${b.textContent}${b.classList.contains("dim") ? " (dim)" : ""}`,
  );
const pickVersion = (title: string) =>
  fireEvent.click(
    [...document.querySelectorAll<HTMLElement>("#model-pick [role=menuitemradio]")].find((b) =>
      b.textContent?.startsWith(title),
    )!,
  );
const generate = (c: HTMLElement) => c.querySelector<HTMLButtonElement>('button[type="submit"]')!;

describe("Create: Run on", () => {
  it("Auto by default: the chip after the model chip, an offline host greyed, no host sent", async () => {
    const { runs, plans, composer } = start("offline");
    const chip = await within(composer).findByRole("button", { name: "Run on Auto" });
    expect(chip.previousElementSibling?.id).toBe("model-pick"); // right after the model chip and its menu
    await waitFor(() => expect(rows()).toHaveLength(3));
    expect(rows()).toEqual([
      { text: "Autopicks a free machine", checked: true, disabled: false },
      { text: "Macmac · Apple M5 Pro", checked: false, disabled: false },
      { text: "NVIDIAgcp · RTX PRO 6000offline", checked: false, disabled: true },
    ]);
    fireEvent.click(menu().querySelectorAll("button")[2]); // greyed: nothing changes
    expect(within(composer).getByRole("button", { name: "Run on Auto" })).toBeTruthy();
    fireEvent.click(generate(composer));
    await waitFor(() => expect(runs).toHaveLength(1));
    expect("host" in runs[0]).toBe(false);
    await waitFor(() => expect(plans.length).toBeGreaterThan(0));
    expect(plans.some((p) => "host" in p)).toBe(false);
  });

  it("a host is greyed by the version; a pick sends the host; a version it cannot run resets to Auto", async () => {
    const { runs, plans, composer } = start();
    await waitFor(() => expect(versions()).toHaveLength(4));
    // Distilled (Mac only) is chosen: NVIDIA cannot take it
    await waitFor(() =>
      expect(rows()[2]).toEqual({
        text: "NVIDIAgcp · RTX PRO 6000Distilled is Mac only",
        checked: false,
        disabled: true,
      }),
    );
    // under Auto each version keeps its note, nothing dimmed
    expect(versions()).toEqual([
      "DistilledMac only",
      "DevMac only",
      "Distilled fp8NVIDIA only",
      "Dev int8NVIDIA only",
    ]);
    pickVersion("Distilled fp8");
    expect(rows()[1].text).toBe("Macmac · Apple M5 ProDistilled fp8 is NVIDIA only");
    expect(rows()[2].disabled).toBe(false);
    fireEvent.click(menu().querySelectorAll("button")[2]);
    expect(within(composer).getByRole("button", { name: "Run on NVIDIA" })).toBeTruthy();
    expect(versions()).toEqual([
      "Distillednot on NVIDIA (dim)",
      "Devnot on NVIDIA (dim)",
      "Distilled fp8NVIDIA only",
      "Dev int8NVIDIA only",
    ]);
    // the estimate is planned on that host
    await waitFor(() => expect(plans.at(-1)?.host).toBe("gcp"));
    fireEvent.click(generate(composer));
    await waitFor(() => expect(runs).toHaveLength(1));
    expect(runs[0].host).toBe("gcp");
    // a dimmed version is still picked: Run on goes back to Auto and says why
    pickVersion("Dev");
    expect(within(composer).getByRole("button", { name: "Run on Auto" })).toBeTruthy();
    expect(screen.getByText("Run on set to Auto: Dev is Mac only")).toBeTruthy();
    expect(within(composer).getByRole("button", { name: "LTX-2.5 · Dev" })).toBeTruthy();
  });

  it("the chosen host went offline: the chip warns, Generate says so and queues nothing", async () => {
    const { runs, composer, setGcp } = start();
    await waitFor(() => expect(versions()).toHaveLength(4));
    pickVersion("Distilled fp8");
    await waitFor(() => expect(rows()[2].disabled).toBe(false));
    fireEvent.click(menu().querySelectorAll("button")[2]);
    expect(generate(composer).disabled).toBe(false);
    setGcp("offline");
    await act(async () => {
      document.dispatchEvent(new Event("visibilitychange")); // the hosts poll
    });
    const chip = await within(composer).findByRole("button", { name: "Run on NVIDIA" });
    await waitFor(() => expect(chip.className).toContain("warn"));
    expect(chip.getAttribute("title")).toBe(
      "gcp is offline. Pick Auto, or wait for it to come back.",
    );
    expect(generate(composer).disabled).toBe(true);
    expect(generate(composer).textContent).toContain("NVIDIA is offline");
    fireEvent.submit(composer);
    await new Promise((r) => setTimeout(r, 50));
    expect(runs).toHaveLength(0);
  });
});
