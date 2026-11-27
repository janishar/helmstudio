// @vitest-environment jsdom
// Every Create text box keeps a fixed height, scrolls inside and has the shared Expand, as the prompt (owner,
// 2026-11-27: "same is the case with voice, check all the prompt and text section in create"; boards CreateAudio,
// CreateAudioZonos3, CreateMusicSound 3b): the dialogue script in both views (its hint rows scroll inside it), and
// the Cover / Vary style. Magic's request boxes are in magic-board.test.tsx.
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { useState } from "react";
import workflows from "../../../contracts/fixtures/registry/workflows.valid.json";
import coreNodes from "../../../contracts/fixtures/registry/nodes.valid.json";
import { SCRIPT_HEIGHT, ScriptEditor } from "@/components/create/dialogue";
import { OfferPop } from "@/components/create/source";
import type { S } from "@/lib/api";
import type { Field } from "@/lib/workflow";
import { audio } from "./helpers";

beforeAll(() => {
  HTMLElement.prototype.showPopover ??= () => undefined;
  HTMLElement.prototype.hidePopover ??= () => undefined;
  globalThis.requestAnimationFrame ??= (f: FrameRequestCallback) =>
    setTimeout(() => f(0), 0) as unknown as number;
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

/** opens a box's Expand as a click does; the dialog's editor */
function expand(host: HTMLElement, name: string) {
  const b = within(host).getByRole("button", { name: "Expand", hidden: true });
  fireEvent.mouseDown(b);
  fireEvent.click(b);
  const d = screen.getAllByRole("dialog", { hidden: true }).at(-1)!;
  return within(d).getByLabelText(name) as HTMLTextAreaElement;
}

const f = {
  key: "dialogue.script",
  node: "dialogue",
  param: { name: "script", type: "text", label: "Script", max: 4000 },
} as unknown as Field;
const SCRIPT = Array.from({ length: 12 }, (_, i) => `@mara: Line ${i + 1}.`).join("\n");

function Host({ errors = {} }: { errors?: Record<number, string> }) {
  const [v, setV] = useState(SCRIPT);
  return (
    <ScriptEditor f={f} value={v} onChange={setV} errors={errors} cast={[]} onVoice={() => {}} />
  );
}

describe("the dialogue script keeps a fixed height", () => {
  it("its lines: a fixed 8 lines that scroll, the hint rows inside, and Expand edits the script", () => {
    render(<Host errors={{ 3: "write @name: text or @name (style): text" }} />);
    const list = screen.getByRole("list", { name: "Script" });
    expect(SCRIPT_HEIGHT).toBe(176);
    expect(list.style.height).toBe("176px");
    expect(list.className).toContain("overflow-y-auto");
    // the hint row scrolls inside the same box
    expect(within(list).getByText(/^Line 3: write @name/)).toBeTruthy();
    const big = expand(list.parentElement!, "Script");
    expect(big.value).toBe(SCRIPT);
    expect(big.getAttribute("role")).toBe("combobox");
    fireEvent.change(big, { target: { value: "@jon: Late.", selectionStart: 0 } });
    // the lines show the new script; the box did not turn into the editor
    expect(within(list).getByText("Late.")).toBeTruthy();
    expect(screen.getByRole("list", { name: "Script" })).toBe(list);
  });

  it("editing: the same fixed height, however many lines", () => {
    render(<Host />);
    fireEvent.click(screen.getByText("Line 1."));
    const box = screen.getByRole("combobox", { name: "Script" }) as HTMLTextAreaElement;
    expect(box.style.height).toBe("176px");
    expect(box.getAttribute("rows")).toBeNull();
  });

  it("the @ colour layer is as tall as the box (a block textarea, no inline gap), so it scrolls as far", () => {
    render(<Host />);
    fireEvent.click(screen.getByText("Line 1."));
    const box = screen.getByRole("combobox", { name: "Script" }) as HTMLTextAreaElement;
    expect(box.style.display).toBe("block");
  });
});

describe("a take's Cover / Vary style", () => {
  it("keeps 3 lines with Expand", () => {
    const schemas = Object.fromEntries(coreNodes.nodes.map((n) => [n.kind, n]));
    const workflow = {
      ...workflows.workflows.find((w) => w.id === "echo-vary"),
      node_schemas: schemas,
      source: "template",
      sound: "sound",
    } as unknown as S<"Workflow">;
    const offer = {
      workflow_id: "echo-vary",
      title: "echo-vary",
      action: "Vary",
      family: "echo",
      media: "audio",
      param: "vary.source",
      inputs: {},
    } as S<"TakeOffer">;
    const take = audio(6, {
      made_with: { seed: 3, workflow_id: "echo-sound", inputs: {}, prompt: "soft rain" },
    } as never);
    render(
      <OfferPop id="op" offer={offer} workflow={workflow} take={take} onRun={async () => {}} />,
    );
    const pop = document.getElementById("op")!;
    const box = within(pop).getByRole("textbox", { name: "Style", hidden: true });
    expect(box.getAttribute("rows")).toBe("3");
    expect(box.className).toContain("resize-none");
    const big = expand(box.parentElement!, "Style");
    fireEvent.change(big, { target: { value: "heavy rain" } });
    expect((box as HTMLTextAreaElement).value).toBe("heavy rain");
  });
});
