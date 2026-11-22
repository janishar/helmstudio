// @vitest-environment jsdom
// Expand on every multi-line text box (components/expand-text.tsx; docs/mock/CreateAudio.dc.html "The prompt,
// expanded"): the shared button and dialog on a plain textarea, and its hosts: the dialogue script (edit mode kept
// while the dialog edits it), a plain box (Memory's Add a note) and a mention field (the graph inspector's text).
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { useState } from "react";
import template from "../../../runner_service/packs/qwen_image/workflows/qwen-image.json";
import paletteFixture from "../../../contracts/fixtures/graphs/palette.json";
import { TextArea } from "@/components/expand-text";
import { ScriptEditor } from "@/components/create/dialogue";
import { MemoryPanel } from "@/components/pipelines/memory";
import { Panel } from "@/components/graph/panel";
import { paletteOf, type Doc } from "@/lib/graph";
import type { S } from "@/lib/api";
import type { Field } from "@/lib/workflow";
import { answer } from "./helpers";

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

const dialog = () => screen.queryByRole("dialog", { hidden: true }) as HTMLDialogElement | null;
/** opens the dialog as a click does (the box keeps the focus through the mousedown) */
function open(name: string) {
  const b = screen.getByRole("button", { name: "Expand" });
  fireEvent.mouseDown(b);
  fireEvent.click(b);
  return within(dialog()!).getByLabelText(name) as HTMLTextAreaElement;
}

function Plain({ initial = "" }: { initial?: string }) {
  const [v, setV] = useState(initial);
  return (
    <TextArea
      aria-label="Box"
      expand="Note"
      rows={2}
      maxLength={2000}
      value={v}
      onChange={(e) => setV(e.target.value)}
    />
  );
}

describe("TextArea: Expand", () => {
  it("shows the button while the text overflows, else on focus only", () => {
    vi.spyOn(HTMLTextAreaElement.prototype, "scrollHeight", "get").mockReturnValue(0);
    const { unmount } = render(<Plain initial="short" />);
    expect(screen.getByRole("button", { name: "Expand" }).className).toContain("opacity-0");
    unmount();
    vi.spyOn(HTMLTextAreaElement.prototype, "scrollHeight", "get").mockReturnValue(300);
    vi.spyOn(HTMLTextAreaElement.prototype, "clientHeight", "get").mockReturnValue(44);
    render(<Plain initial={"a long note\n".repeat(20)} />);
    expect(screen.getByRole("button", { name: "Expand" }).className).not.toContain("opacity-0");
  });

  it("edits the same value with its count, no mentions, and closes back to the box", async () => {
    render(<Plain initial="hi" />);
    const box = screen.getByLabelText("Box") as HTMLTextAreaElement;
    const big = open("Note");
    expect(big.tagName).toBe("TEXTAREA");
    expect(big.getAttribute("role")).toBeNull();
    expect(big.value).toBe("hi");
    expect(document.activeElement).toBe(big);
    fireEvent.change(big, { target: { value: "hi there" } });
    expect(box.value).toBe("hi there");
    expect(screen.getByText("8 / 2000")).toBeTruthy();
    expect(screen.getByText(/Esc closes/).textContent).not.toContain("@");
    fireEvent.click(screen.getByRole("button", { name: "Done" }));
    expect(dialog()).toBeNull();
    await waitFor(() => expect(document.activeElement).toBe(box));
  });

  it("has no Expand while the box is disabled", () => {
    render(<TextArea aria-label="Box" expand="Message" disabled value="" onChange={() => {}} />);
    expect(screen.queryByRole("button", { name: "Expand" })).toBeNull();
  });
});

describe("Expand on its hosts", () => {
  it("dialogue script: the dialog edits the raw script and editing goes on", async () => {
    const f = {
      key: "dialogue.script",
      node: "dialogue",
      param: { name: "script", type: "text", label: "Script", max: 4000 },
    } as unknown as Field;
    function Host() {
      const [v, setV] = useState("@mara: Noon.");
      return (
        <ScriptEditor f={f} value={v} onChange={setV} errors={{}} cast={[]} onVoice={() => {}} />
      );
    }
    render(<Host />);
    fireEvent.click(screen.getByText("Noon."));
    const box = screen.getByRole("combobox", { name: "Script" }) as HTMLTextAreaElement;
    box.focus();
    const big = open("Script");
    expect(big.value).toBe("@mara: Noon.");
    expect(document.activeElement).toBe(big);
    fireEvent.change(big, { target: { value: "@mara: Noon.\n@jon: Late.", selectionStart: 0 } });
    // still editing: the box (not the line list) holds the new script
    expect(screen.queryByRole("list", { name: "Script" })).toBeNull();
    expect(box.value).toBe("@mara: Noon.\n@jon: Late.");
    expect(screen.getByText(/@ adds an element/)).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Done" }));
    expect(dialog()).toBeNull();
    await waitFor(() => expect(document.activeElement).toBe(box));
    fireEvent.blur(box);
    expect(await screen.findByRole("list", { name: "Script" })).toBeTruthy();
  });

  it("a plain box: Memory's Add a note", async () => {
    answer("GET", {
      "/v1/studio/memory": () => ({ data: { items: [], counts: { judge_example: 0, note: 0 } } }),
    });
    render(<MemoryPanel pipelineId="p1" title="Ads" />);
    const box = (await screen.findByLabelText("Add a note")) as HTMLTextAreaElement;
    const add = screen.getByRole("button", { name: "Add note" }) as HTMLButtonElement;
    expect(add.disabled).toBe(true);
    const big = open("Note");
    fireEvent.change(big, { target: { value: "Keep the fox red" } });
    expect(screen.getByText("16 / 2000")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Done" }));
    expect(box.value).toBe("Keep the fox red");
    expect(add.disabled).toBe(false);
  });

  it("a mention field: the graph inspector's text param", () => {
    const palette = paletteOf(paletteFixture as S<"NodePalette">);
    const doc = template as unknown as Doc;
    const set = vi.fn();
    render(
      <Panel
        doc={{ ...doc, source: "template" } as unknown as Doc}
        schemas={Object.fromEntries(Object.values(doc.nodes).map((n) => [n.kind, palette[n.kind]]))}
        palette={palette}
        id="prompt"
        problems={[]}
        overrides={{}}
        groupTitle="Qwen Image"
        edit={() => {}}
        onParamSet={set}
      />,
    );
    const box = document.querySelector<HTMLTextAreaElement>("textarea")!;
    const label = box.labels![0].textContent!;
    const big = open(label);
    expect(big.getAttribute("role")).toBe("combobox");
    expect(big.value).toBe(box.value);
    expect(screen.getByText(/@ adds an element/)).toBeTruthy();
    fireEvent.change(big, { target: { value: "a heron", selectionStart: 7 } });
    expect(box.value).toBe("a heron");
  });
});
