// @vitest-environment jsdom
// The Magic page on the sample board (MagicBoard frames 1, 2, 5, 11): lanes and blocks with their statuses, select
// and delete then Undo, Add at a point with Music and Sound greyed, the shorten warning, the side panel's toggle
// remembered, a new board's empty state.
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { MagicBoard } from "@/components/magic/board";
import { loadStore } from "@/components/magic/use-board";
import { closeMenu } from "@/components/context-menu";

vi.mock("@/components/magic/magic.css", () => ({}));

beforeAll(() => {
  globalThis.ResizeObserver ??= class {
    observe() {}
    unobserve() {}
    disconnect() {}
  } as unknown as typeof ResizeObserver;
  window.matchMedia ??= ((q: string) => ({
    matches: false,
    media: q,
    addEventListener() {},
    removeEventListener() {},
  })) as unknown as typeof matchMedia;
  HTMLDialogElement.prototype.showModal ??= function (this: HTMLDialogElement) {
    this.open = true;
  };
  HTMLDialogElement.prototype.close ??= function (this: HTMLDialogElement) {
    this.open = false;
  };
  HTMLElement.prototype.hidePopover ??= () => undefined;
  document.elementsFromPoint ??= () => [];
});
beforeEach(() => localStorage.clear());
afterEach(() => {
  act(closeMenu);
  cleanup();
});

const blockEl = (name: RegExp) => screen.getByRole("button", { name });
/** a click on a block: down on it, up on the window (the board's drag listens there) */
function click(el: Element, init: PointerEventInit = {}) {
  fireEvent.pointerDown(el, { button: 0, clientX: 100, clientY: 10, ...init });
  fireEvent.pointerUp(window, { clientX: 100, clientY: 10, ...init });
}

describe("the board", () => {
  it("draws every lane, its blocks and their statuses (frame 1)", () => {
    render(<MagicBoard local />);
    for (const lane of ["Storyboard", "Shots", "Mara's lines", "Music", "Sound", "Lane 7"])
      expect(screen.getByRole("group", { name: `Lane ${lane}` })).toBeTruthy();
    const shot3 = blockEl(/Video block Shot 3/);
    expect(within(shot3).getByText("Out of date")).toBeTruthy();
    expect(shot3.className).toContain("old");
    expect(within(blockEl(/Voice block Line 2/)).getByText("Draft")).toBeTruthy();
    expect(within(blockEl(/Video block Shot 2/)).getByText("62%")).toBeTruthy();
    // the joins: a fade at the Shots lane's start, the dissolve, a cut
    expect(screen.getByRole("button", { name: "Fade 1 s, lane start" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Dissolve 0.5 s, join" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Cut, join" })).toBeTruthy();
    expect(screen.getByText("Empty · click to add")).toBeTruthy();
    expect(screen.getByRole("region", { name: "Player, floating" })).toBeTruthy();
  });

  it("select a shot: its details and what is tied to it; Delete closes the gap; Undo puts it back", () => {
    render(<MagicBoard local />);
    click(blockEl(/Video block Shot 2/));
    const side = screen.getByRole("complementary", { name: "Block details" });
    expect(within(side).getByRole("heading", { name: "Shot 2 · umbrella" })).toBeTruthy();
    expect(within(side).getByText("Dissolve 0.5 s")).toBeTruthy();
    expect(within(side).getByText("Tied")).toBeTruthy();
    expect(blockEl(/Voice block Line 2/).className).toContain("tied");
    fireEvent.keyDown(window, { key: "Backspace" });
    expect(screen.queryByRole("button", { name: /Video block Shot 2/ })).toBeNull();
    expect(screen.getByRole("status").textContent).toBe("Deleted Shot 2 · umbrella");
    // Shot 3 moved into the gap: it now meets Shot 1 at 0:08
    expect(blockEl(/Video block Shot 3/).style.left).toBe(`${8 * 26 + 2}px`);
    fireEvent.click(screen.getByRole("button", { name: "Undo" }));
    expect(blockEl(/Video block Shot 2/)).toBeTruthy();
    expect(blockEl(/Video block Shot 3/).style.left).toBe(`${16 * 26 + 2}px`);
    fireEvent.click(screen.getByRole("button", { name: "Redo" }));
    expect(screen.queryByRole("button", { name: /Video block Shot 2/ })).toBeNull();
  });

  it("Add at a point: the kind with its orb, Music and Sound on (phase H), Insert pushes what is after", () => {
    render(<MagicBoard local />);
    fireEvent.click(screen.getByText("Empty · click to add"), { clientX: 0 });
    const dlg = screen.getByRole("dialog", { name: /Add at 0:00 · Lane 7/ });
    const music = within(dlg).getByRole("button", { name: /Music/ }) as HTMLButtonElement;
    expect(music.disabled).toBe(false);
    expect(within(dlg).getByRole("button", { name: /Video/ }).getAttribute("aria-pressed")).toBe(
      "true",
    );
    fireEvent.click(within(dlg).getByRole("button", { name: /Image/ }));
    fireEvent.click(within(dlg).getByRole("button", { name: "8 s" }));
    // step 2 (frame 2h): the standard request for an image, then Generate adds it and runs it
    fireEvent.click(within(dlg).getByRole("button", { name: "Next" }));
    const two = screen.getByRole("dialog", { name: "Image · Still 1" });
    expect(within(two).getByRole("button", { name: "Qwen Image 2.1" })).toBeTruthy();
    expect(within(two).getByRole("spinbutton", { name: "Width" })).toBeTruthy();
    fireEvent.change(within(two).getByRole("textbox", { name: "Prompt" }), {
      target: { value: "a title card" },
    });
    fireEvent.click(within(two).getByRole("button", { name: "Generate" }));
    const still = blockEl(/Image block Still 1, 8 s/);
    expect(still.textContent).toMatch(/\d+%/);
    expect(screen.getByRole("group", { name: "Lane Lane 7" }).textContent).toContain("1 block");
  });

  it("phase H: Music's step 2 (frame 2f) with Style, Lyrics, Key and BPM; a sound from a shot's video (2g)", () => {
    render(<MagicBoard local />);
    fireEvent.click(screen.getByText("Empty · click to add"), { clientX: 0 });
    let dlg = screen.getByRole("dialog", { name: /Add at 0:00 · Lane 7/ });
    fireEvent.click(within(dlg).getByRole("button", { name: /Music/ }));
    fireEvent.click(within(dlg).getByRole("button", { name: "Next" }));
    dlg = screen.getByRole("dialog", { name: /^Music · Cue/ });
    // MiniMax Music 3 is the default: no key or BPM, with why
    expect(
      within(dlg).getByRole("button", { name: "MiniMax Music 3" }).getAttribute("aria-pressed"),
    ).toBe("true");
    expect(within(dlg).getByRole("textbox", { name: "Style" })).toBeTruthy();
    expect(within(dlg).getByRole("textbox", { name: "Lyrics" })).toBeTruthy();
    expect(within(dlg).getByText("MiniMax Music 3 can't take a key")).toBeTruthy();
    expect(within(dlg).getByRole("combobox", { name: "Key" }).matches(":disabled")).toBe(true);
    fireEvent.click(within(dlg).getByRole("button", { name: "ACE-Step 1.5" }));
    expect(within(dlg).getByRole("combobox", { name: "Key" }).matches(":disabled")).toBe(false);
    fireEvent.change(within(dlg).getByRole("combobox", { name: "Key" }), {
      target: { value: "D minor" },
    });
    fireEvent.click(within(dlg).getByRole("button", { name: "Generate" }));
    expect(blockEl(/Music block Cue 2, 4 s/).textContent).toMatch(/\d+%/);
  });

  it("phase H: a sound added under Shot 2 starts from its video, on MMAudio (frame 2g)", () => {
    render(<MagicBoard local />);
    fireEvent.click(screen.getByText("Empty · click to add"), { clientX: 120 + 10 * 26 });
    let dlg = screen.getByRole("dialog", { name: /Add at/ });
    fireEvent.click(within(dlg).getByRole("button", { name: /Sound/ }));
    fireEvent.click(within(dlg).getByRole("button", { name: "Next" }));
    dlg = screen.getByRole("dialog", { name: /^Sound · Sound/ });
    expect((within(dlg).getByRole("combobox", { name: "From" }) as HTMLSelectElement).value).toBe(
      "s2",
    );
    expect(within(dlg).getByRole("button", { name: "MMAudio" }).getAttribute("aria-pressed")).toBe(
      "true",
    );
  });

  it("Add step 2 greys what the model can't take, with why (frame 2d); a board frame is added as a draft", () => {
    render(<MagicBoard local />);
    fireEvent.click(screen.getByText("Empty · click to add"), { clientX: 0 });
    let dlg = screen.getByRole("dialog", { name: /Add at 0:00 · Lane 7/ });
    fireEvent.click(within(dlg).getByRole("button", { name: "Next" }));
    dlg = screen.getByRole("dialog", { name: /^Video · Shot/ });
    fireEvent.click(within(dlg).getByRole("button", { name: "MiniMax H3" }));
    expect(within(dlg).getByText("MiniMax H3 can't take a last frame")).toBeTruthy();
    expect(within(dlg).getByRole("combobox", { name: "Last frame" }).matches(":disabled")).toBe(
      true,
    );
    expect(within(dlg).getByRole("combobox", { name: "First frame" }).matches(":disabled")).toBe(
      false,
    );
    fireEvent.click(within(dlg).getByRole("button", { name: "Back" }));
    dlg = screen.getByRole("dialog", { name: /Add at 0:00 · Lane 7/ });
    fireEvent.click(within(dlg).getByRole("button", { name: /Board frame/ }));
    fireEvent.click(within(dlg).getByRole("button", { name: "Add" }));
    expect(within(blockEl(/Board block Frame 4/)).getByText("Draft")).toBeTruthy();
  });

  it("shortening a shot shows the warning; Trim fits the line (frame 5b)", () => {
    const s = loadStore();
    const b = s.boards[0];
    b.doc.blocks.find((x) => x.id === "s1")!.length = 4 * 24;
    render(<MagicBoard initial={s} />);
    const warn = screen.getByText("Doesn't fit").closest('[role="alert"]') as HTMLElement;
    expect(warn.textContent).toContain("Line 1 runs 3.1 s past Shot 1");
    fireEvent.click(within(warn).getByRole("button", { name: "Trim" }));
    expect(screen.queryByText("Doesn't fit")).toBeNull();
    expect(blockEl(/Voice block Line 1, 3.4 s/)).toBeTruthy();
  });

  it("the side panel's toggle is remembered in this browser", () => {
    const { unmount } = render(<MagicBoard local />);
    const toggle = screen.getByRole("button", { name: "Side panel" });
    expect(toggle.getAttribute("aria-expanded")).toBe("true");
    fireEvent.click(toggle);
    expect(screen.queryByRole("complementary", { name: "Block details" })).toBeNull();
    expect(localStorage.getItem("magic-side-panel")).toBe("0");
    unmount();
    render(<MagicBoard local />);
    expect(screen.getByRole("button", { name: "Side panel" }).getAttribute("aria-expanded")).toBe(
      "false",
    );
  });

  it("a new board is empty, with a lane to start (frame 11)", () => {
    render(<MagicBoard local />);
    fireEvent.click(screen.getByRole("button", { name: "The Last Tide" }));
    fireEvent.click(screen.getByRole("menuitem", { name: "New board" }));
    expect(screen.getByRole("heading", { name: "A new board" })).toBeTruthy();
    expect(
      (screen.getByRole("button", { name: "Build from a brief" }) as HTMLButtonElement).disabled,
    ).toBe(true);
    fireEvent.click(screen.getByRole("button", { name: "Lane" }));
    expect(screen.getByRole("group", { name: "Lane Shots" })).toBeTruthy();
  });
});
