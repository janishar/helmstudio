// @vitest-environment jsdom
// Create settings in popups (alpha.5 item 10, plan 36 section 41, docs/mock/TextToVideo.dc.html frames 1-6): the
// settings drawer is closed by default and remembered; each chip opens a popup with only its setting, one at a time,
// closed by Esc or a click outside; All settings opens the drawer at the section; narrow windows open the drawer.
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
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
  HTMLDialogElement.prototype.showModal ??= function (this: HTMLDialogElement) {
    this.open = true;
  };
  HTMLDialogElement.prototype.close ??= function (this: HTMLDialogElement) {
    this.open = false;
  };
});
beforeEach(() => localStorage.removeItem("create-settings-drawer"));
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  // @ts-expect-error jsdom has no matchMedia; a test sets it for a narrow window
  delete globalThis.matchMedia;
});

const schemas = Object.fromEntries(
  [...coreNodes.nodes, ...ltxNodes.nodes].map((n) => [n.kind, n]),
) as unknown as S<"Workflow">["node_schemas"];
const t2v = {
  ...ltxWorkflows.workflows.find((w) => w.id === "ltx-text-to-video"),
  node_schemas: schemas,
  source: "template",
} as unknown as S<"Workflow">;

function start() {
  answer("GET", {
    "/v1/studio/sessions/{session_id}/runs": () => ({ data: { runs: [] } }),
    "/v1/studio/sessions/{session_id}/takes": () => ({ data: { takes: [] } }),
    "/v1/studio/library": () => ({ data: { items: [] } }),
    "/v1/studio/hosts": () => ({ data: { hosts: [] } }),
    "/v1/studio/loras": () => ({ data: { loras: [] } }),
  });
  render(
    <Create
      initial={{
        sessions: [],
        workflows: [t2v],
        sessionId: "s1",
        runs: [
          {
            id: "r1",
            session_id: "s1",
            workflow_id: t2v.id,
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
  return screen.getByRole("form", { name: "Composer" });
}

const inspector = () => screen.queryByRole("complementary", { name: "Inspector" });
const sliders = () => screen.getByRole("button", { name: "All settings", expanded: !!inspector() });
// jsdom draws a popover as hidden (it never opens one): the popups are read by id
const pop = (id: string) => document.getElementById(id)!;
const chipOf = (id: string) => document.querySelector<HTMLElement>(`[aria-controls="${id}"]`)!;
const isOpen = (id: string) => chipOf(id).getAttribute("aria-expanded") === "true";
const lengthId = () => document.querySelector<HTMLElement>("[id^=len-pop-]:not([id$=-in])")!.id;

describe("Create: settings in popups", () => {
  it("the drawer is closed by default; the sliders button opens it, and it is remembered", async () => {
    const composer = start();
    await within(composer).findByRole("button", { name: "Distilled · int8" });
    expect(inspector()).toBeNull();
    const btn = sliders();
    expect(btn.getAttribute("aria-expanded")).toBe("false");
    fireEvent.click(btn);
    expect(inspector()).toBeTruthy();
    expect(btn.getAttribute("aria-expanded")).toBe("true");
    expect(localStorage.getItem("create-settings-drawer")).toBe("1");
    cleanup();
    start();
    expect(inspector()).toBeTruthy();
    fireEvent.click(sliders());
    expect(inspector()).toBeNull();
    expect(localStorage.getItem("create-settings-drawer")).toBeNull();
  });

  it("each chip opens only its popup; a change shows on the chip at once", async () => {
    const composer = start();
    await within(composer).findByRole("button", { name: "Distilled · int8" });
    // Size: Aspect and Size as options, the pixel line
    fireEvent.click(within(composer).getByRole("button", { name: "704 × 448" }));
    expect(isOpen("canvas-pop")).toBe(true);
    expect(within(pop("canvas-pop")).getByText(/704 × 448 · on the 64 px grid/)).toBeTruthy();
    fireEvent.click(within(pop("canvas-pop")).getByText("1:1"));
    expect(chipOf("canvas-pop").textContent).not.toBe("704 × 448");
    expect(chipOf("canvas-pop").textContent).toMatch(/^(\d+) × \1$/);
    // Length: opening it closes Size (one at a time); the slider sets the chip
    const len = lengthId();
    fireEvent.click(chipOf(len));
    expect(isOpen(len)).toBe(true);
    expect(isOpen("canvas-pop")).toBe(false);
    expect(within(pop("canvas-pop")).queryByText("1:1")).toBeNull();
    const slider = within(pop(len)).getByRole("slider", { hidden: true }) as HTMLInputElement;
    const before = chipOf(len).textContent;
    fireEvent.change(slider, { target: { value: slider.max } });
    expect(chipOf(len).textContent).not.toBe(before);
    // Model: the variant rows
    fireEvent.click(chipOf("model-pick"));
    expect(isOpen(len)).toBe(false);
    expect(pop("model-pick").querySelectorAll("[role=menuitemradio]").length).toBeGreaterThan(1);
    fireEvent.click(within(pop("model-pick")).getByText("Distilled fp8"));
    expect(chipOf("model-pick").textContent).toMatch(/^Distilled fp8/);
    // the drawer stays closed throughout
    expect(inspector()).toBeNull();
  });

  it("Esc or a click outside closes a popup; focus goes back to the chip on Esc", async () => {
    const composer = start();
    await within(composer).findByRole("button", { name: "Distilled · int8" });
    fireEvent.click(chipOf("canvas-pop"));
    // (focus moves into it in a browser: jsdom draws a popover as hidden, so it cannot take focus here)
    fireEvent.keyDown(document, { key: "Escape" });
    expect(isOpen("canvas-pop")).toBe(false);
    expect(document.activeElement).toBe(chipOf("canvas-pop"));
    fireEvent.click(chipOf("canvas-pop"));
    fireEvent.pointerDown(within(pop("canvas-pop")).getByText("1:1"));
    expect(isOpen("canvas-pop")).toBe(true);
    fireEvent.pointerDown(document.body);
    expect(isOpen("canvas-pop")).toBe(false);
  });

  it("All settings opens the drawer at the section; Run on has none; Audio is a switch", async () => {
    const composer = start();
    await within(composer).findByRole("button", { name: "Distilled · int8" });
    fireEvent.click(chipOf(lengthId()));
    fireEvent.click(within(pop(lengthId())).getByText("All settings"));
    expect(inspector()).toBeTruthy();
    expect(isOpen(lengthId())).toBe(false);
    await vi.waitFor(() =>
      expect(
        within(inspector()!)
          .getByRole("region", { name: "Duration" })
          .contains(document.activeElement),
      ).toBe(true),
    );
    // Run on: today's menu, no All settings
    expect(within(pop("run-on")).queryByText("All settings")).toBeNull();
    // Audio: a one-click switch, no popup
    const audio = within(composer).getByRole("button", { name: "Audio on" });
    expect(audio.getAttribute("aria-haspopup")).toBeNull();
    fireEvent.click(audio);
    expect(within(composer).getByRole("button", { name: "Audio off" })).toBeTruthy();
  });

  it("on a narrow window a chip opens the drawer at its section instead of a popup", async () => {
    globalThis.matchMedia = ((q: string) => ({
      matches: q.includes("max-width"),
      addEventListener: () => undefined,
      removeEventListener: () => undefined,
    })) as unknown as typeof matchMedia;
    const composer = start();
    await within(composer).findByRole("button", { name: "Distilled · int8" });
    const size = within(composer).getByRole("button", { name: "704 × 448" });
    expect(size.getAttribute("aria-haspopup")).toBeNull();
    fireEvent.click(size);
    expect(inspector()).toBeTruthy();
  });
});
