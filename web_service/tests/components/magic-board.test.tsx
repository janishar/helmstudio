// @vitest-environment jsdom
// The Magic page on the sample board (MagicBoard frames 1, 2, 5, 11): lanes and blocks with their statuses, select
// and delete then Undo, Add at a point with Music and Sound greyed, the shorten warning, the side panel's toggle
// remembered, a new board's empty state.
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { MagicBoard, loadStore } from "@/components/magic/board";
import { NOT_YET } from "@/components/magic/menus";
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
    render(<MagicBoard />);
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
    render(<MagicBoard />);
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

  it("Add at a point: the kind with its orb, Music and Sound greyed, Insert pushes what is after", () => {
    render(<MagicBoard />);
    fireEvent.click(screen.getByText("Empty · click to add"), { clientX: 0 });
    const dlg = screen.getByRole("dialog", { name: /Add at 0:00 · Lane 7/ });
    const music = within(dlg).getByRole("button", { name: /Music/ }) as HTMLButtonElement;
    expect(music.disabled).toBe(true);
    expect(music.title).toBe(NOT_YET);
    expect(within(dlg).getByRole("button", { name: /Video/ }).getAttribute("aria-pressed")).toBe(
      "true",
    );
    fireEvent.click(within(dlg).getByRole("button", { name: /Image/ }));
    fireEvent.click(within(dlg).getByRole("button", { name: "8 s" }));
    fireEvent.click(within(dlg).getByRole("button", { name: "Add" }));
    const still = blockEl(/Image block Still 1, 8 s/);
    expect(within(still).getByText("Draft")).toBeTruthy();
    expect(screen.getByRole("group", { name: "Lane Lane 7" }).textContent).toContain("1 block");
  });

  it("shortening a shot shows the warning; Trim fits the line (frame 5b)", () => {
    const s = loadStore();
    const b = s.boards[0];
    b.doc.blocks.find((x) => x.id === "s1")!.length = 4 * 24;
    render(<MagicBoard initial={s} />);
    const warn = screen.getByRole("alert");
    expect(warn.textContent).toContain("Line 1 runs 3.1 s past Shot 1");
    fireEvent.click(within(warn).getByRole("button", { name: "Trim" }));
    expect(screen.queryByRole("alert")).toBeNull();
    expect(blockEl(/Voice block Line 1, 3.4 s/)).toBeTruthy();
  });

  it("the side panel's toggle is remembered in this browser", () => {
    const { unmount } = render(<MagicBoard />);
    const toggle = screen.getByRole("button", { name: "Side panel" });
    expect(toggle.getAttribute("aria-expanded")).toBe("true");
    fireEvent.click(toggle);
    expect(screen.queryByRole("complementary", { name: "Block details" })).toBeNull();
    expect(localStorage.getItem("magic-side-panel")).toBe("0");
    unmount();
    render(<MagicBoard />);
    expect(screen.getByRole("button", { name: "Side panel" }).getAttribute("aria-expanded")).toBe(
      "false",
    );
  });

  it("a new board is empty, with a lane to start (frame 11)", () => {
    render(<MagicBoard />);
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
