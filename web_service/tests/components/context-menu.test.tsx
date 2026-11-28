// @vitest-environment jsdom
// Our right-click menu (alpha.5 item 17, docs/mock/ContextMenus.dc.html frames 0 and 6): opens at the pointer and
// stays on screen, Esc / outside closes and focus goes back, the keys, a submenu, Shift passes to the browser, a
// greyed item keeps its reason, and the text fields' menu.
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import {
  closeMenu,
  installMenus,
  openMenu,
  spellItems,
  type Menu,
} from "@/components/context-menu";

beforeAll(() => {
  installMenus();
});
afterEach(() => {
  act(closeMenu);
  cleanup();
  vi.restoreAllMocks();
});

function Box({ menu }: { menu: Menu }) {
  return (
    <button type="button" onContextMenu={(e) => openMenu(e, menu)}>
      target
    </button>
  );
}
const rightClick = (el: Element, init: MouseEventInit = {}) =>
  fireEvent.contextMenu(el, { button: 2, clientX: 100, clientY: 80, ...init });

describe("the menu", () => {
  it("opens at the pointer with its header, items, keys and a red danger item; a pick closes it", () => {
    const split = vi.fn();
    const remove = vi.fn();
    render(
      <Box
        menu={{
          title: "Shot 2",
          items: [
            { label: "Split at playhead", keys: "S", onPick: split },
            "-",
            { label: "Remove clip", keys: "Delete", danger: true, onPick: remove },
          ],
        }}
      />,
    );
    rightClick(screen.getByText("target"));
    const menu = screen.getByRole("menu", { name: "Shot 2" });
    expect(menu.parentElement!.style.left).toBe("100px");
    expect(menu.parentElement!.style.top).toBe("80px");
    expect(screen.getByText("Shot 2").className).toBe("hd");
    expect(screen.getByRole("menuitem", { name: /Remove clip/ }).className).toContain("danger");
    expect(screen.getByRole("menuitem", { name: /Split at playhead/ }).textContent).toContain("S");
    expect(screen.getAllByRole("separator")).toHaveLength(1);
    fireEvent.click(screen.getByRole("menuitem", { name: /Split/ }));
    expect(split).toHaveBeenCalledOnce();
    expect(screen.queryByRole("menu")).toBeNull();
  });

  it("is kept on screen: flips left and up near the edge", () => {
    vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockReturnValue({
      width: 240,
      height: 300,
      left: 0,
      top: 0,
      right: 240,
      bottom: 300,
    } as DOMRect);
    render(<Box menu={{ items: [{ label: "One" }] }} />);
    rightClick(screen.getByText("target"), { clientX: innerWidth - 20, clientY: innerHeight - 10 });
    const box = screen.getByRole("menu").parentElement!;
    expect(parseFloat(box.style.left)).toBe(innerWidth - 20 - 240);
    expect(parseFloat(box.style.top)).toBe(innerHeight - 10 - 300);
  });

  it("Esc and a click outside close it; focus goes back", () => {
    render(
      <>
        <Box menu={{ items: [{ label: "One" }] }} />
        <p>outside</p>
      </>,
    );
    const t = screen.getByText("target");
    t.focus();
    rightClick(t);
    // a right-click moves focus into the menu, so its keys reach it (not the field or a dialog under it)
    expect(screen.getByRole("menu")).toBe(document.activeElement);
    fireEvent.keyDown(document.activeElement!, { key: "Escape" });
    expect(screen.queryByRole("menu")).toBeNull();
    expect(document.activeElement).toBe(t);
    rightClick(t);
    fireEvent.pointerDown(screen.getByText("outside"));
    expect(screen.queryByRole("menu")).toBeNull();
  });

  it("the keys: ↑ ↓ move, a letter jumps, Enter picks; → opens a submenu and ← closes it", () => {
    const pick = vi.fn();
    render(
      <Box
        menu={{
          items: [
            { label: "Extend" },
            {
              label: "Use in",
              items: [{ label: "Last frame in…", onPick: pick }, { label: "First frame in…" }],
            },
            { label: "Redo this shot" },
          ],
        }}
      />,
    );
    const t = screen.getByText("target");
    t.focus();
    // the menu key: opens at the focused element, the first item focused
    fireEvent.keyDown(t, { key: "ContextMenu" });
    const items = () => screen.getAllByRole("menuitem");
    expect(document.activeElement).toBe(items()[0]);
    fireEvent.keyDown(document.activeElement!, { key: "ArrowDown" });
    expect(document.activeElement?.textContent).toContain("Use in");
    fireEvent.keyDown(document.activeElement!, { key: "ArrowRight" });
    expect(screen.getAllByRole("menu")).toHaveLength(2);
    expect(document.activeElement?.textContent).toBe("Last frame in…");
    fireEvent.keyDown(document.activeElement!, { key: "ArrowLeft" });
    expect(screen.getAllByRole("menu")).toHaveLength(1);
    expect(document.activeElement?.textContent).toContain("Use in");
    fireEvent.keyDown(document.activeElement!, { key: "r" });
    expect(document.activeElement?.textContent).toBe("Redo this shot");
    fireEvent.keyDown(document.activeElement!, { key: "ArrowDown" });
    expect(document.activeElement?.textContent).toBe("Extend");
    // a submenu by hover, then a pick in it
    fireEvent.mouseEnter(screen.getByRole("menuitem", { name: /Use in/ }));
    expect(screen.getByRole("menuitem", { name: /Use in/ }).getAttribute("aria-expanded")).toBe(
      "true",
    );
    fireEvent.click(screen.getByRole("menuitem", { name: "Last frame in…" }));
    expect(pick).toHaveBeenCalledOnce();
    expect(screen.queryByRole("menu")).toBeNull();
  });

  it("Shift + right-click is the browser's own menu: nothing opens, nothing cancelled", () => {
    render(<Box menu={{ items: [{ label: "One" }] }} />);
    const ev = new MouseEvent("contextmenu", {
      bubbles: true,
      cancelable: true,
      shiftKey: true,
      button: 2,
    });
    screen.getByText("target").dispatchEvent(ev);
    expect(ev.defaultPrevented).toBe(false);
    expect(screen.queryByRole("menu")).toBeNull();
  });

  it("a greyed item keeps its place and its reason, and does nothing", () => {
    const pick = vi.fn();
    render(<Box menu={{ items: [{ label: "Clear range", off: "No range set", onPick: pick }] }} />);
    rightClick(screen.getByText("target"));
    const it = screen.getByRole("menuitem", { name: "Clear range" });
    expect(it.getAttribute("aria-disabled")).toBe("true");
    expect(it.title).toBe("No range set");
    fireEvent.click(it);
    expect(pick).not.toHaveBeenCalled();
    expect(screen.getByRole("menu")).toBeTruthy();
  });

  it("empty page space has no menu, and the browser's is cancelled too", () => {
    render(<p>a heading</p>);
    const ev = new MouseEvent("contextmenu", { bubbles: true, cancelable: true, button: 2 });
    screen.getByText("a heading").dispatchEvent(ev);
    expect(ev.defaultPrevented).toBe(true);
    expect(screen.queryByRole("menu")).toBeNull();
  });

  it("a scroll queued before it opened (the strip scrolled to the tile) leaves it open; a later scroll closes it", () => {
    let frame: FrameRequestCallback | undefined;
    vi.spyOn(window, "requestAnimationFrame").mockImplementation((f) => ((frame = f), 1));
    render(<Box menu={{ title: "Take", items: [{ label: "Sound only", onPick: () => {} }] }} />);
    rightClick(screen.getByText("target"));
    // the scroll that was queued lands before the next frame's callbacks
    fireEvent.scroll(document.body);
    expect(screen.getByRole("menu", { name: "Take" })).toBeTruthy();
    act(() => frame!(0));
    fireEvent.scroll(document.body);
    expect(screen.queryByRole("menu")).toBeNull();
  });

  it("an update redraws the open menu (items read after it opened)", () => {
    let h: ReturnType<typeof openMenu> = null;
    render(
      <button
        type="button"
        onContextMenu={(e) => (h = openMenu(e, { items: [{ label: "Loading…" }] }))}
      >
        t
      </button>,
    );
    rightClick(screen.getByText("t"));
    act(() => h!.update({ items: [{ label: "Extend · LTX" }] }));
    expect(screen.getByRole("menuitem", { name: "Extend · LTX" })).toBeTruthy();
  });
});

describe("text fields", () => {
  it("Cut, Copy, Paste, Select all: Cut and Copy greyed with nothing selected", () => {
    render(<input aria-label="name" defaultValue="fox at dusk" />);
    rightClick(screen.getByLabelText("name"));
    const names = screen.getAllByRole("menuitem").map((b) => b.querySelector(".lb")!.textContent);
    expect(names).toEqual(["Cut", "Copy", "Paste", "Select all"]);
    expect(screen.getByRole("menuitem", { name: /Cut/ }).title).toBe("Select some text first");
    expect(screen.getByRole("menuitem", { name: /Copy/ }).title).toBe("Select some text first");
    fireEvent.click(screen.getByRole("menuitem", { name: /Select all/ }));
    const f = screen.getByLabelText<HTMLInputElement>("name");
    expect([f.selectionStart, f.selectionEnd]).toEqual([0, 11]);
  });

  it("read-only: Cut and Paste greyed; a selection lets Copy run", () => {
    render(<textarea aria-label="log" readOnly defaultValue="a fox" />);
    const f = screen.getByLabelText<HTMLTextAreaElement>("log");
    f.setSelectionRange(2, 5);
    rightClick(f);
    expect(screen.getByRole("menuitem", { name: /Cut/ }).title).toBe("This field is read-only");
    expect(screen.getByRole("menuitem", { name: /Paste/ }).title).toBe("This field is read-only");
    expect(screen.getByRole("menuitem", { name: /Copy/ }).getAttribute("aria-disabled")).toBeNull();
  });

  it("a password or key field: Cut and Copy greyed even with a selection, Paste runs", () => {
    render(<input aria-label="key" type="password" defaultValue="sk-secret" />);
    const f = screen.getByLabelText<HTMLInputElement>("key");
    f.setSelectionRange(0, 9);
    rightClick(f);
    expect(screen.getByRole("menuitem", { name: /Cut/ }).title).toBe("Hidden text can't be copied");
    expect(screen.getByRole("menuitem", { name: /Copy/ }).title).toBe(
      "Hidden text can't be copied",
    );
    expect(
      screen.getByRole("menuitem", { name: /Paste/ }).getAttribute("aria-disabled"),
    ).toBeNull();
  });

  it("Paste reads the clipboard and types it at the caret; a refused read greys Paste with the reason", async () => {
    const readText = vi
      .fn()
      .mockResolvedValueOnce(" snow")
      .mockRejectedValueOnce(new Error("denied"));
    Object.defineProperty(navigator, "clipboard", { value: { readText }, configurable: true });
    render(<input aria-label="p" defaultValue="fox" />);
    const f = screen.getByLabelText<HTMLInputElement>("p");
    f.setSelectionRange(3, 3);
    rightClick(f);
    await act(async () => fireEvent.click(screen.getByRole("menuitem", { name: /Paste/ })));
    expect(f.value).toBe("fox snow");
    rightClick(f);
    await act(async () => fireEvent.click(screen.getByRole("menuitem", { name: /Paste/ })));
    rightClick(f);
    expect(screen.getByRole("menuitem", { name: /Paste/ }).title).toMatch(
      /the browser blocked the clipboard$/,
    );
  });

  it("a prompt that takes @ mentions adds Insert @…, which types @", () => {
    const change = vi.fn();
    render(
      <div data-mentions>
        <textarea
          aria-label="prompt"
          defaultValue="a fox "
          onChange={(e) => change(e.target.value)}
        />
      </div>,
    );
    const f = screen.getByLabelText<HTMLTextAreaElement>("prompt");
    f.setSelectionRange(6, 6);
    rightClick(f);
    fireEvent.click(screen.getByRole("menuitem", { name: /Insert @/ }));
    expect(f.value).toBe("a fox @");
    expect(change).toHaveBeenCalledWith("a fox @");
  });

  it("the Mac app's spelling: suggestions and Add to dictionary first", () => {
    const d = { replaceMisspelling: vi.fn(), addToDictionary: vi.fn(), onSpelling: vi.fn() };
    (globalThis as { helmDesktop?: unknown }).helmDesktop = d;
    try {
      const items = spellItems("snwo", ["snow", "sown", "snows", "sow"]);
      expect(items.map((x) => (x && x !== "-" ? x.label : x))).toEqual([
        "snow",
        "sown",
        "snows",
        "Add to dictionary",
        "-",
      ]);
      (items[0] as { onPick: () => void }).onPick();
      expect(d.replaceMisspelling).toHaveBeenCalledWith("snow");
      expect(spellItems("", [])).toEqual([]);
    } finally {
      delete (globalThis as { helmDesktop?: unknown }).helmDesktop;
    }
    // the browser: no spelling items
    expect(spellItems("snwo", ["snow"])).toEqual([]);
  });
});
