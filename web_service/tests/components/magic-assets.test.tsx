// @vitest-environment jsdom
// Magic's assets and storyboard on the sample board (MagicBoard frames 6 and 7; plan 39 phase E): + Asset makes one
// with a free handle and Make views fills its sheet; a frame opened with its style, framing and Generate from board,
// which makes the shot under it and runs it; the rough cut plays the frames with their lines; the @ menu in a prompt.
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { MagicBoard } from "@/components/magic/board";
import { closeMenu } from "@/components/context-menu";

vi.mock("@/components/magic/magic.css", () => ({}));
vi.mock("@/lib/library", async (orig) => ({
  ...(await orig<typeof import("@/lib/library")>()),
  useVoices: () => [],
  useLibrary: () => [],
}));

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
  HTMLElement.prototype.showPopover ??= () => undefined;
  document.elementsFromPoint ??= () => [];
});
beforeEach(() => localStorage.clear());
afterEach(() => {
  act(closeMenu);
  cleanup();
});

describe("assets", () => {
  it("+ Asset makes one with a free handle; Make views puts the views on its sheet (frame 6)", () => {
    render(<MagicBoard local />);
    fireEvent.click(screen.getByRole("button", { name: "+ Asset" }));
    let dlg = screen.getByRole("dialog", { name: /New asset/ });
    fireEvent.change(within(dlg).getByRole("textbox", { name: "Name" }), {
      target: { value: "Mara" },
    });
    expect((within(dlg).getByRole("textbox", { name: /^Handle/ }) as HTMLInputElement).value).toBe(
      "@mara-2",
    );
    fireEvent.click(within(dlg).getByRole("button", { name: "Make asset" }));
    dlg = screen.getByRole("dialog", { name: /Character · @mara-2/ });
    expect(within(dlg).getByText(/Not used yet/)).toBeTruthy();
    // the kind's views, the missing ones on; Back off, "Smiling" added
    const pick = within(dlg).getByRole("group", { name: "Views to make" });
    fireEvent.click(within(pick).getByRole("button", { name: "Back" }));
    const add = within(dlg).getByRole("textbox", { name: "Add a view" });
    fireEvent.change(add, { target: { value: "Smiling" } });
    fireEvent.keyDown(add, { key: "Enter" });
    expect(within(pick).getByRole("button", { name: "Smiling" }).getAttribute("aria-pressed")).toBe(
      "true",
    );
    // what it makes (API 1.98): studio's size, changed in the setup and kept for the board
    const line = within(dlg).getByRole("button", { name: /^768 × 1024/ });
    fireEvent.click(line);
    const w = within(dlg).getByRole("spinbutton", { name: "Width" });
    fireEvent.change(w, { target: { value: "640" } });
    fireEvent.blur(w);
    expect(within(dlg).getByRole("button", { name: /^640 × 1024/ })).toBeTruthy();
    fireEvent.click(within(dlg).getByRole("button", { name: /^Make 4/ }));
    const views = within(dlg).getByRole("list", { name: "Views" });
    expect(
      within(views)
        .getAllByRole("listitem")
        .map((x) => x.getAttribute("aria-label")),
    ).toEqual(["Front", "Side", "Full figure", "Smiling"]);
    // the Assets row has it, and Import is the studio's (no Gallery in the fake mode)
    expect(screen.getByRole("button", { name: "Asset @mara-2, Character" })).toBeTruthy();
    expect((screen.getByRole("button", { name: "Import" }) as HTMLButtonElement).disabled).toBe(
      true,
    );
  });

  it("an asset opened shows where it is used, lit on a small board", () => {
    render(<MagicBoard local />);
    fireEvent.doubleClick(screen.getByRole("button", { name: "Asset @mara, Character" }));
    const dlg = screen.getByRole("dialog", { name: /Character · @mara/ });
    expect(within(dlg).getByRole("heading", { name: /Used in · 7/ })).toBeTruthy();
    expect(within(dlg).getByRole("img", { name: "Where it is used, on the board" })).toBeTruthy();
    expect(within(dlg).getByText(/asset → frame → first frame chain/)).toBeTruthy();
  });
});

describe("the storyboard", () => {
  it("a frame opened: style, framing; Generate from board makes the shot under it and runs it (frame 7)", () => {
    render(<MagicBoard local />);
    fireEvent.keyDown(screen.getByRole("button", { name: /Board block Frame 3/ }), {
      key: "Enter",
    });
    const dlg = screen.getByRole("dialog", { name: /Board · Frame 3/ });
    fireEvent.click(within(dlg).getByRole("button", { name: "Rough" }));
    fireEvent.click(
      within(within(dlg).getByRole("group", { name: "Framing" })).getByRole("button", {
        name: "Close",
      }),
    );
    fireEvent.click(within(dlg).getByRole("button", { name: "Generate from board" }));
    // the frame's changes and the new shot, made from it, running
    const shot = screen.getByRole("button", { name: /Video block Shot 4/ });
    expect(within(shot).getByText(/%/)).toBeTruthy();
    expect(within(dlg).getByText("was made from this frame")).toBeTruthy();
  });

  it("the rough cut plays the frames in order with their lines", () => {
    render(<MagicBoard local />);
    fireEvent.keyDown(screen.getByRole("button", { name: /Board block Frame 1/ }), {
      key: "Enter",
    });
    fireEvent.click(
      within(screen.getByRole("dialog", { name: /Board · Frame 1/ })).getByRole("button", {
        name: "Rough cut",
      }),
    );
    const cut = screen.getByRole("dialog", { name: /Rough cut/ });
    expect(within(cut).getByText("Every night, the same bus.")).toBeTruthy();
    expect(within(cut).getAllByRole("listitem")).toHaveLength(3);
    fireEvent.click(within(cut).getByRole("button", { name: "Next frame" }));
    expect(within(cut).getByText(/2 of 3/)).toBeTruthy();
  });

  it("@ in a prompt offers the board's assets; a pick writes the handle and joins the refs", () => {
    render(<MagicBoard local />);
    fireEvent.keyDown(screen.getByRole("button", { name: /Board block Frame 3/ }), {
      key: "Enter",
    });
    const dlg = screen.getByRole("dialog", { name: /Board · Frame 3/ });
    const box = within(dlg).getByRole("textbox", { name: "What happens" }) as HTMLTextAreaElement;
    fireEvent.change(box, { target: { value: "then @um", selectionStart: 8 } });
    box.setSelectionRange(8, 8);
    fireEvent.select(box);
    fireEvent.click(within(dlg).getByRole("option", { name: /@red-umbrella/ }));
    expect(box.value).toBe("then @red-umbrella ");
    expect(within(dlg).getByText("@red-umbrella", { selector: ".rc.big" })).toBeTruthy();
  });
});

describe("the board's Stop runs", () => {
  it("says Stopping, then Stopped once none of them is going (it stayed on for good)", () => {
    render(<MagicBoard local />);
    fireEvent.click(screen.getByRole("button", { name: /^Stop runs/ }));
    const ask = screen.getAllByRole("alert").find((x) => /^Stop \d/.test(x.textContent ?? ""))!;
    fireEvent.click(within(ask).getByRole("button", { name: "Stop runs" }));
    expect(screen.getAllByRole("status").map((s) => s.textContent)).toContain("Stopped 1 run");
  });
});
