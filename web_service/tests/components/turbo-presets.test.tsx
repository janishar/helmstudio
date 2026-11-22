// @vitest-environment jsdom
// MiniMax H3's Turbo preset without its add-on (API 1.41; docs/mock/H3Options.dc.html frame 11): the pill is greyed
// (outlined, no fill) with a tooltip naming the model, and the reason shows under the chips.
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import h3Workflows from "../../../contracts/fixtures/registry/minimax-h3.workflows.valid.json";
import h3Nodes from "../../../contracts/fixtures/registry/minimax-h3.nodes.valid.json";
import h3Models from "../../../contracts/fixtures/registry/minimax-h3.models.valid.json";
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
  ...h3Workflows.workflows[0],
  node_schemas: Object.fromEntries(h3Nodes.nodes.map((n) => [n.kind, n])),
  source: "template",
} as unknown as S<"Workflow">;

// the Mac has the model, no add-on
const [model] = h3Models.models;
const models = {
  hosts: [
    {
      host: "mac",
      live: true,
      envs: ["native"],
      models_root: null,
      free_bytes: null,
      total_bytes: null,
    },
  ],
  licences: [],
  models: [
    {
      id: model.id,
      family: model.family,
      title: model.title,
      media: model.media,
      runtimes: model.runtimes,
      variants: [
        {
          id: "base",
          title: "Base",
          precisions: ["int8"],
          dir: "MiniMax-H3",
          licence: null,
          gated: false,
          download_bytes: null,
          components: [],
          hosts: [{ host: "mac", state: "present" }],
        },
      ],
      addons: ["turbo", "turbo_ref"].map((component) => ({
        component,
        title: component === "turbo" ? "Turbo LoRA" : "Turbo Ref LoRA",
        dir: "MiniMax-H3-Turbo",
        licence: null,
        gated: false,
        download_bytes: null,
        hosts: [{ host: "mac", state: "missing" }],
      })),
    },
  ],
};

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

describe("Create: Turbo without its add-on", () => {
  it("greys the Turbo pill, names the model in its tooltip and shows the reason", async () => {
    answer("GET", {
      "/v1/studio/sessions/{session_id}/runs": () => ({ data: { runs: [done] } }),
      "/v1/studio/sessions/{session_id}/takes": () => ({ data: { takes: [] } }),
      "/v1/studio/library": () => ({ data: { items: [] } }),
      "/v1/studio/loras": () => ({ data: { loras: [] } }),
      "/v1/studio/models": () => ({ data: models }),
      "/v1/studio/hosts": () => ({
        data: {
          hosts: [
            {
              host: "mac",
              env: "native",
              state: "idle",
              updated_at: "2026-11-22T00:00:00Z",
              device: { kind: "mps", name: "Apple M5 Pro", memory_total_gb: 64 },
            },
          ],
        },
      }),
    });
    answer("POST", {
      "/v1/studio/runs/plan": () => ({ data: { host: "mac", peak_gb: 27, reason: null } }),
    });
    render(
      <Create
        initial={{ sessions: [], workflows: [workflow], sessionId: "s1", runs: [done], takes: [] }}
      />,
    );
    const inspector = () => screen.getByRole("complementary", { name: "Inspector" });
    const turbo = () => within(inspector()).getByRole("button", { name: "Turbo" });
    await waitFor(() => expect(turbo().getAttribute("aria-disabled")).toBe("true"));
    expect(turbo().title).toBe(
      "Turbo LoRA not downloaded. An admin downloads it in Models, under MiniMax H3's optional add-ons.",
    );
    expect(turbo().className).toContain("border-line");
    expect(turbo().className).toContain("bg-transparent");
    const section = turbo().closest("section")!;
    expect(within(section).getByText("Turbo LoRA not downloaded")).toBeTruthy();
    expect(
      within(inspector()).getByRole("button", { name: "Fast" }).getAttribute("aria-disabled"),
    ).toBeNull();
  });
});
