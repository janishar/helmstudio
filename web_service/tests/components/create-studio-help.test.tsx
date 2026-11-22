// @vitest-environment jsdom
// Create on the LTX templates, the owner's answers of 2026-11-20 (docs/handoff.md, second round): a section's (i)
// from the Studio block (Q18), presets in the pack's order (Q19), Generate's "≈ <time> · <N> GB" from the planned
// peak (Q20, POST /v1/studio/runs/plan), Extend's one "CFG · STG" tile (Q21) and Audio to video's
// "Frames · No keyframes" tile (Q22).
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import ltxWorkflows from "../../../contracts/fixtures/registry/ltx.workflows.valid.json";
import ltxNodes from "../../../contracts/fixtures/registry/ltx.nodes.valid.json";
import coreNodes from "../../../contracts/fixtures/registry/nodes.valid.json";
import { Create } from "@/components/create/create";
import { estimateText } from "@/components/create/memory-chip";
import type { S } from "@/lib/api";
import { remember } from "@/lib/take-cache";
import { answer, take } from "./helpers";

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
function serve(gb: number | null, picked?: S<"Take">) {
  answer("GET", {
    "/v1/studio/sessions/{session_id}/runs": () => ({ data: { runs: [] } }),
    "/v1/studio/sessions/{session_id}/takes": () => ({ data: { takes: [] } }),
    "/v1/studio/library": () => ({ data: { items: [] } }),
    "/v1/studio/hosts": () => ({ data: { hosts: [] } }),
    "/v1/studio/loras": () => ({ data: { loras: [] } }),
    "/v1/studio/takes": () => ({ data: { takes: picked ? [picked] : [], next_cursor: null } }),
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

describe("Create: the Studio block's help, preset order and planned peak", () => {
  it("text to video: presets in the pack's order, Quality's (i), the planned peak on Generate", async () => {
    const plans = serve(45.87);
    start(ltx("ltx-text-to-video"));
    const presets = within(section("Preset")).getAllByRole("button");
    expect(presets.map((b) => b.textContent)).toEqual(["Fast", "Quality", "Low memory"]);
    expect(section("Quality").querySelector("h2")!.textContent).toContain(
      "Distilled models run without guidance. Switch the model to Dev for guidance and a negative prompt.",
    );
    expect(section("Prompt beats").querySelector("h2")!.textContent).toContain(
      "A beat steers one part of the clip.",
    );
    const submit = screen
      .getByRole("form", { name: "Composer" })
      .querySelector<HTMLElement>('button[type="submit"]')!;
    await waitFor(() => expect(submit.textContent).toContain("46 GB"), { timeout: 2000 });
    expect(plans.at(-1)?.workflow_id).toBe("ltx-text-to-video");
  });

  it("the estimate: time and memory, either alone, or nothing", () => {
    expect(estimateText("40 s", 21.4)).toBe("≈ 40 s · 21 GB");
    expect(estimateText("2:40", null)).toBe("≈ 2:40");
    expect(estimateText(null, 21.6)).toBe("22 GB");
    expect(estimateText(null, null)).toBeNull();
  });

  it("extend: one CFG · STG tile that opens both editors (on Dev: Distilled has none)", () => {
    serve(null);
    start(ltx("ltx-extend"));
    expect(within(section("Quality")).queryByRole("button", { name: /CFG · STG/ })).toBeNull();
    fireEvent.click(screen.getByRole("menuitemradio", { name: /^Dev/, hidden: true }));
    const quality = section("Quality");
    const tile = within(quality).getByRole("button", { name: /CFG · STG\s*3\.0 · 1\.0/ });
    expect(within(quality).queryByRole("button", { name: /^STG/ })).toBeNull();
    fireEvent.click(tile);
    expect(within(quality).getByRole("group", { name: "CFG" })).toBeTruthy();
    expect(within(quality).getByRole("group", { name: "STG" })).toBeTruthy();
  });

  it("audio to video: Audio and Frames · No keyframes; a frame switches keyframes on, clearing it off", async () => {
    const frame = take();
    remember([frame]);
    const plans = serve(null, frame);
    start(ltx("ltx-audio-to-video"));
    const composer = screen.getByRole("form", { name: "Composer" });
    const tile = within(composer).getByRole("button", { name: "Frames: No keyframes" });
    expect(within(composer).queryByRole("button", { name: /^Elements/ })).toBeNull();
    expect(within(composer).queryByRole("button", { name: "No keyframes" })).toBeNull();
    expect(within(inspector()).queryByText("No keyframes")).toBeNull(); // no switch of its own
    expect(within(composer).getByRole("button", { name: "Insert element" })).toBeTruthy();
    fireEvent.click(tile);
    const dialog = screen
      .getAllByRole("dialog", { hidden: true })
      .find((d) => (d as HTMLDialogElement).open)!;
    await waitFor(() => within(dialog).getByText(frame.title, { selector: "b" }));
    fireEvent.click(within(dialog).getByText(frame.title, { selector: "b" }).closest("button")!);
    fireEvent.click(within(dialog).getByRole("button", { name: "Add 1", hidden: true }));
    await waitFor(() =>
      expect(within(composer).getByRole("button", { name: `Frames: ${frame.title}` })).toBeTruthy(),
    );
    await waitFor(() => expect(plans.at(-1)?.inputs?.["keyframes.bypass"]).toBe(false), {
      timeout: 2000,
    });
    fireEvent.click(within(inspector()).getByRole("button", { name: /^Remove .* 1$/ }));
    expect(within(composer).getByRole("button", { name: "Frames: No keyframes" })).toBeTruthy();
    await waitFor(() => expect(plans.at(-1)?.inputs?.["keyframes.bypass"]).not.toBe(false), {
      timeout: 2000,
    });
  });
});
