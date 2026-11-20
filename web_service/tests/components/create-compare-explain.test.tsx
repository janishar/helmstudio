// @vitest-environment jsdom
// Create's Compare (A/B wipe, seed grid) and Explain run drawer (the owner's Q23, docs/handoff.md second round), read
// from the takes' made_with and the runs the API already returns.
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import qwenWorkflows from "../../../contracts/fixtures/registry/qwen-image.workflows.valid.json";
import qwenNodes from "../../../contracts/fixtures/registry/qwen-image.nodes.valid.json";
import coreNodes from "../../../contracts/fixtures/registry/nodes.valid.json";
import { Create } from "@/components/create/create";
import { helmRun, seedGroup } from "@/lib/explain";
import type { S } from "@/lib/api";
import { answer, take } from "./helpers";

vi.mock("@/lib/runs", async (orig) => ({
  ...(await orig<typeof import("@/lib/runs")>()),
  followSession: () => () => undefined,
}));

beforeAll(() => {
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

const schemas = Object.fromEntries(
  [...coreNodes.nodes, ...qwenNodes.nodes].map((n) => [n.kind, n]),
) as unknown as S<"Workflow">["node_schemas"];
const qwen = {
  ...qwenWorkflows.workflows[0],
  node_schemas: schemas,
  source: "template",
} as unknown as S<"Workflow">;

const LORA = "a".repeat(64);
const run = (id: string, seed: number): S<"Run"> =>
  ({
    id,
    session_id: "s1",
    workflow_id: "qwen-image",
    inputs: {
      "prompt.text": "a fox @mira",
      "sample.steps": 20,
      "lora.rows": [{ lora: { lora_id: LORA }, strength: 0.8 }],
    },
    looks: ["qwen-image/window-light"],
    status: "succeeded",
    progress: null,
    error: null,
    outputs: [],
    host: "mac",
    resolved: { seed },
    created_at: "2026-11-20T00:00:00Z",
  }) as unknown as S<"Run">;
const made = (seed: number, prompt = "a fox @mira"): S<"MadeWith"> => ({
  workflow_id: "qwen-image",
  workflow_title: "Qwen Image",
  inputs: {
    "prompt.text": "a fox, Mira, a girl in a red coat, soft window light",
    "seed.seed": seed,
    "sample.steps": 20,
    "canvas.mode": "auto",
    "canvas.width": 1024,
    "canvas.height": 1024,
  },
  typed: { "prompt.text": prompt },
  prompt,
  seed,
  model: { id: "qwen-image-2.1", variant: "base", precision: "int8" },
  elements: [{ id: "e1", handle: "mira", name: "Mira", version: 2 }],
  looks: [{ id: "qwen-image/window-light", source: "starter", name: "Window light", version: 1 }],
  loras: [
    {
      node: "lora",
      id: LORA,
      source: "shared",
      name: "film grain",
      strength: 0.8,
      mode: "adapter",
      sha256: LORA,
    },
  ],
  host: "mac",
});

const takes = [
  take({ run_id: "r2", made_with: made(43), created_at: "2026-11-20T00:02:00Z" }),
  take({ run_id: "r1", made_with: made(42), created_at: "2026-11-20T00:01:00Z" }),
  take({ run_id: "r0", made_with: made(7, "a cat"), created_at: "2026-11-20T00:00:00Z" }),
];

function start() {
  answer("GET", {
    "/v1/studio/library": ({ params }) => {
      const kind = (params as { query: { kind: string } }).query.kind;
      return {
        data: {
          items:
            kind === "element"
              ? [
                  {
                    id: "e1",
                    kind: "element",
                    handle: "mira",
                    name: "Mira",
                    text: "a girl in a red coat",
                    version: 3,
                    images: [],
                    loras: [],
                  },
                ]
              : [
                  {
                    id: "qwen-image/window-light",
                    kind: "look",
                    name: "Window light",
                    text: "soft window light",
                    version: 1,
                    images: [],
                    loras: [],
                  },
                ],
        },
      };
    },
    "/v1/studio/hosts": () => ({ data: { hosts: [] } }),
    "/v1/studio/loras": () => ({ data: { loras: [] } }),
  });
  answer("POST", {
    "/v1/studio/runs/plan": () => ({ data: { host: "mac", peak_gb: 21.34, reason: null } }),
  });
  return render(
    <Create
      initial={{
        sessions: [],
        workflows: [qwen],
        sessionId: "s1",
        runs: [run("r2", 43), run("r1", 42), run("r0", 7)],
        takes,
      }}
    />,
  );
}

describe("Create: Compare and Explain run (Q23)", () => {
  it("the A/B wipe sets A against the take before it, B can be changed, the wipe moves", () => {
    start();
    fireEvent.click(screen.getByRole("button", { name: "Compare" }));
    expect(screen.getByRole("button", { name: "Compare", pressed: true })).toBeTruthy();
    expect(screen.getByText("A · seed 43")).toBeTruthy();
    expect(screen.getByText("B · seed 42")).toBeTruthy();
    fireEvent.change(screen.getByRole("combobox", { name: "B" }), {
      target: { value: takes[2].id },
    });
    expect(screen.getByText("B · seed 7")).toBeTruthy();
    fireEvent.change(screen.getByRole("slider", { name: "Wipe" }), { target: { value: 30 } });
    expect(screen.getByAltText("seed 7").getAttribute("style")).toContain("inset(0 0 0 30%)");
  });

  it("the seed grid shows the takes of the same prompt; a pick becomes the shown take", () => {
    start();
    fireEvent.click(screen.getByRole("button", { name: "Compare" }));
    fireEvent.click(screen.getByRole("button", { name: "Seed grid" }));
    const grid = screen.getByRole("group", { name: "Seed grid" });
    expect(
      within(grid)
        .getAllByRole("button")
        .map((b) => b.getAttribute("aria-label")),
    ).toEqual(["Show seed 43", "Show seed 42"]);
    fireEvent.click(within(grid).getByRole("button", { name: "Show seed 42" }));
    expect(screen.queryByRole("group", { name: "Seed grid" })).toBeNull();
    expect(screen.getByRole("button", { name: /^Take, seed 42/, pressed: true })).toBeTruthy();
  });

  it("explain run: model, settings and where they came from, elements, looks, LoRAs, peak, helm run", async () => {
    start();
    fireEvent.click(screen.getByRole("button", { name: "Explain run" }));
    const d = screen.getByRole("dialog", { name: "Explain run" });
    expect(d.textContent).toContain("qwen-image-2.1 · base · int8");
    const sample = within(d).getByRole("region", { name: "Sample" });
    expect(sample.textContent).toMatch(/Steps20you set it/);
    expect(within(d).getByRole("region", { name: "Seed" }).textContent).toMatch(/43random/);
    expect(within(d).getByRole("region", { name: "Elements" }).textContent).toContain(
      "version 2, edited since",
    );
    expect(within(d).getByRole("region", { name: "Looks" }).textContent).toContain(
      "adds “soft window light”",
    );
    expect(within(d).getByRole("region", { name: "LoRAs" }).textContent).toContain(
      "film grain0.80 · adapter",
    );
    expect(within(d).getByRole("region", { name: "Prompt" }).textContent).toContain(
      "a fox, Mira, a girl in a red coat",
    );
    await waitFor(() => expect(d.textContent).toContain("≈ 21.3 GB"), { timeout: 2000 });
    expect(within(d).getByRole("region", { name: "Copy as helm run" }).textContent).toContain(
      `helm run qwen-image -p prompt.text='a fox @mira' -p sample.steps=20 -p seed.seed=43 --look qwen-image/window-light --lora ${LORA}:0.8`,
    );
    fireEvent.click(within(d).getByRole("button", { name: "Close" }));
    expect(screen.queryByRole("dialog", { name: "Explain run" })).toBeNull();
  });
});

describe("explain helpers", () => {
  it("helm run quotes what a shell would split and writes references as take ids", () => {
    expect(
      helmRun(
        qwen,
        { "prompt.text": "it's", "encode.refs": [{ image: { take_id: "t1" } }] },
        null,
        [],
      ),
    ).toBe(`helm run qwen-image -p encode.refs=t1 -p prompt.text='it'\\''s'`);
  });
  it("a take without made_with is its own seed group", () => {
    const t = take();
    expect(seedGroup([t, take()], t)).toEqual([t]);
  });
});
