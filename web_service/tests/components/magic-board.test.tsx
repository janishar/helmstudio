// @vitest-environment jsdom
// The Magic page on the sample board (MagicBoard frames 1, 2, 5, 11), in a mock of studio (./magic-api.ts): lanes and
// blocks with their statuses, select and delete then Undo, Add at a point with Music and Sound, the shorten warning,
// the side panel's toggle remembered, a new board's empty state.
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { finishRuns, reset, seedSample, studio } from "./magic-api";

vi.mock("@/lib/api", async (orig) => ({
  ...(await orig<typeof import("@/lib/api")>()),
  api: (await import("./magic-api")).api,
}));
vi.mock("@/lib/runs", async (orig) => ({
  ...(await orig<typeof import("@/lib/runs")>()),
  followSession: (await import("./magic-api")).followSession,
}));
vi.mock("@/lib/library", async (orig) => ({
  ...(await orig<typeof import("@/lib/library")>()),
  useVoices: () => [],
}));

const { MagicBoard } = await import("@/components/magic/board");
const { closeMenu } = await import("@/components/context-menu");

/** The Model select (MagicBoard 2c: a select, the other models its hint): its picked name, and a pick by name. */
const modelName = (dlg: HTMLElement) => {
  const sel = within(dlg).getByRole("combobox", { name: "Model" }) as HTMLSelectElement;
  return sel.selectedOptions[0]?.textContent;
};
const pickModel = (dlg: HTMLElement, name: string) => {
  const sel = within(dlg).getByRole("combobox", { name: "Model" }) as HTMLSelectElement;
  const o = [...sel.options].find((x) => x.textContent === name);
  if (!o) throw new Error(`no model ${name}`);
  fireEvent.change(sel, { target: { value: o.value } });
};

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
beforeEach(() => {
  localStorage.clear();
  reset();
});
afterEach(() => {
  act(closeMenu);
  cleanup();
});

/** a popover by its label (popovers are not shown in jsdom, so role queries skip them) */
const popOf = (label: string) => document.querySelector<HTMLElement>(`[aria-label="${label}"]`)!;
const blockEl = (name: RegExp) => screen.getByRole("button", { name });
/** the page on the sample board, loaded */
async function board(ui = <MagicBoard />) {
  if (!studio.boards.length) seedSample();
  const r = render(ui);
  await screen.findByRole("toolbar", { name: "Board tools" });
  await act(async () => {}); // the kinds and the plan
  return r;
}
/** a click on a block: down on it, up on the window (the board's drag listens there) */
function click(el: Element, init: PointerEventInit = {}) {
  fireEvent.pointerDown(el, { button: 0, clientX: 100, clientY: 10, ...init });
  fireEvent.pointerUp(window, { clientX: 100, clientY: 10, ...init });
}

describe("the board", () => {
  it("draws every lane, its blocks and their statuses (frame 1)", async () => {
    await board();
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

  it("select a shot: its details and what is tied to it; Delete closes the gap; Undo puts it back", async () => {
    await board();
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
    const undo = screen.getByRole("button", { name: "Undo" }) as HTMLButtonElement;
    await waitFor(() => expect(undo.disabled).toBe(false));
    fireEvent.click(undo);
    expect(await screen.findByRole("button", { name: /Video block Shot 2/ })).toBeTruthy();
    expect(blockEl(/Video block Shot 3/).style.left).toBe(`${16 * 26 + 2}px`);
    const redo = screen.getByRole("button", { name: "Redo" }) as HTMLButtonElement;
    await waitFor(() => expect(redo.disabled).toBe(false));
    fireEvent.click(redo);
    await waitFor(() =>
      expect(screen.queryByRole("button", { name: /Video block Shot 2/ })).toBeNull(),
    );
  });

  it("Add at a point: the kind with its orb, Music and Sound on (phase H), Insert pushes what is after", async () => {
    await board();
    fireEvent.click(screen.getByText("Empty · click to add"), { clientX: 0 });
    const dlg = screen.getByRole("dialog", { name: /Add at 0:00 · Lane 7/ });
    const music = within(dlg).getByRole("button", { name: /Music/ }) as HTMLButtonElement;
    expect(music.disabled).toBe(false);
    expect(within(dlg).getByRole("button", { name: /Video/ }).getAttribute("aria-pressed")).toBe(
      "true",
    );
    fireEvent.click(within(dlg).getByRole("button", { name: /Image/ }));
    fireEvent.click(within(dlg).getByRole("button", { name: "10 s" }));
    // step 2 (frame 2h): the standard request for an image, then Generate adds it and runs it
    fireEvent.click(within(dlg).getByRole("button", { name: "Next" }));
    const two = screen.getByRole("dialog", { name: "Image · Still 1" });
    expect(modelName(two)).toBe("Qwen Image 2.1");
    expect(within(two).getByRole("spinbutton", { name: "Width" })).toBeTruthy();
    fireEvent.change(within(two).getByRole("textbox", { name: "Prompt" }), {
      target: { value: "a title card" },
    });
    fireEvent.click(within(two).getByRole("button", { name: "Generate" }));
    const still = blockEl(/Image block Still 1, 10 s/);
    await waitFor(() => expect(still.textContent).toMatch(/\d+%/));
    expect(screen.getByRole("group", { name: "Lane Lane 7" }).textContent).toContain("1 block");
  });

  it("phase H: Music's step 2 (frame 2f) with Style, Lyrics, Key and BPM; a sound from a shot's video (2g)", async () => {
    await board();
    fireEvent.click(screen.getByText("Empty · click to add"), { clientX: 0 });
    let dlg = screen.getByRole("dialog", { name: /Add at 0:00 · Lane 7/ });
    fireEvent.click(within(dlg).getByRole("button", { name: /Music/ }));
    fireEvent.click(within(dlg).getByRole("button", { name: "Next" }));
    dlg = screen.getByRole("dialog", { name: /^Music · Cue/ });
    // MiniMax Music 3 is the default: no key or BPM, with why
    expect(modelName(dlg)).toBe("MiniMax Music 3");
    expect(within(dlg).getByText("or ACE-Step 1.5")).toBeTruthy();
    expect(within(dlg).getByRole("textbox", { name: "Style" })).toBeTruthy();
    // the request's text boxes keep a fixed 66px and scroll, with Expand, as Create's prompt (owner, 2026-11-27)
    const lyrics = within(dlg).getByRole("textbox", { name: "Lyrics" }) as HTMLTextAreaElement;
    for (const box of [lyrics, within(dlg).getByRole("textbox", { name: "Style" })]) {
      expect(box.className).toContain("h-[66px]");
      expect(box.className).toContain("resize-none");
      expect(within(box.parentElement!).getByRole("button", { name: "Expand" })).toBeTruthy();
    }
    fireEvent.click(within(lyrics.parentElement!).getByRole("button", { name: "Expand" }));
    const big = screen.getAllByRole("dialog", { name: "Lyrics" }).at(-1)!;
    fireEvent.change(within(big).getByRole("textbox", { name: "Lyrics" }), {
      target: { value: "la la" },
    });
    expect(lyrics.value).toBe("la la");
    fireEvent.click(within(big).getByRole("button", { name: "Done" }));
    expect(within(dlg).getByText("MiniMax Music 3 can't take a key")).toBeTruthy();
    expect(within(dlg).getByRole("combobox", { name: "Key" }).matches(":disabled")).toBe(true);
    pickModel(dlg, "ACE-Step 1.5");
    expect(within(dlg).getByRole("combobox", { name: "Key" }).matches(":disabled")).toBe(false);
    fireEvent.change(within(dlg).getByRole("combobox", { name: "Key" }), {
      target: { value: "D minor" },
    });
    fireEvent.click(within(dlg).getByRole("button", { name: "Generate" }));
    await waitFor(() => expect(blockEl(/Music block Cue 2, 5 s/).textContent).toMatch(/\d+%/));
  });

  it("phase H: a sound added under Shot 2 starts from its video, on MMAudio (frame 2g)", async () => {
    await board();
    fireEvent.click(screen.getByText("Empty · click to add"), { clientX: 120 + 10 * 26 });
    let dlg = screen.getByRole("dialog", { name: /Add at/ });
    fireEvent.click(within(dlg).getByRole("button", { name: /Sound/ }));
    fireEvent.click(within(dlg).getByRole("button", { name: "Next" }));
    dlg = screen.getByRole("dialog", { name: /^Sound · Sound/ });
    expect((within(dlg).getByRole("combobox", { name: "From" }) as HTMLSelectElement).value).toBe(
      "s2",
    );
    expect(modelName(dlg)).toBe("MMAudio");
    // a model that takes no video drops the shot, so From is not stuck greyed on it
    pickModel(dlg, "Stable Audio 3 Small-SFX");
    expect((within(dlg).getByRole("combobox", { name: "From" }) as HTMLSelectElement).value).toBe(
      "",
    );
  });

  it("Add step 2 greys what the model can't take, with why (frame 2d); a board frame is added as a draft", async () => {
    await board();
    fireEvent.click(screen.getByText("Empty · click to add"), { clientX: 0 });
    let dlg = screen.getByRole("dialog", { name: /Add at 0:00 · Lane 7/ });
    fireEvent.click(within(dlg).getByRole("button", { name: "Next" }));
    dlg = screen.getByRole("dialog", { name: /^Video · Shot/ });
    pickModel(dlg, "MiniMax H3");
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

  it("API 1.89: a dialogue's lines run one each, with their statuses; Compare takes side by side", async () => {
    {
      await board();
      fireEvent.click(screen.getByText("Empty · click to add"), { clientX: 0 });
      let dlg = screen.getByRole("dialog", { name: /Add at 0:00 · Lane 7/ });
      fireEvent.click(within(dlg).getByRole("button", { name: /Dialogue/ }));
      fireEvent.click(within(dlg).getByRole("button", { name: "Next" }));
      dlg = screen.getByRole("dialog", { name: /^Voice · Dialogue \d/ });
      expect(within(dlg).getByText("Dialogue", { selector: ".kb" })).toBeTruthy();
      const lines = within(dlg).getByRole("list", { name: "Lines" });
      expect(within(lines).getAllByRole("listitem")).toHaveLength(2);
      fireEvent.change(within(dlg).getByRole("textbox", { name: "Line 1" }), {
        target: { value: "Hi." },
      });
      fireEvent.change(within(dlg).getByRole("textbox", { name: "Line 2" }), {
        target: { value: "Yo." },
      });
      fireEvent.click(within(dlg).getByRole("button", { name: "Generate" }));
      await waitFor(() => expect(blockEl(/Voice block Dialogue \d/).textContent).toMatch(/\d+%/));
      expect(studio.boards[0].runs.filter((r) => r.turn != null)).toHaveLength(2);
      finishRuns();
      await waitFor(() =>
        expect(within(blockEl(/Voice block Dialogue \d/)).getByText("Done")).toBeTruthy(),
      );
      fireEvent.doubleClick(blockEl(/Voice block Dialogue \d/));
      dlg = screen.getByRole("dialog", { name: /^Voice · Dialogue \d/ });
      const rows = within(within(dlg).getByRole("list", { name: "Lines" })).getAllByRole(
        "listitem",
      );
      expect(rows.map((r) => within(r).getByText(/Done|Draft/).textContent)).toEqual([
        "Done",
        "Done",
      ]);
      // one line changed: only it is out of date
      fireEvent.change(within(dlg).getByRole("textbox", { name: "Line 2" }), {
        target: { value: "Yo, later." },
      });
      expect(within(rows[1]).getByText("Out of date")).toBeTruthy();
      fireEvent.click(within(dlg).getByRole("button", { name: "Close" }));
      fireEvent(dlg, new Event("close")); // jsdom's close() fires none
      // Shot 2 again: two takes, compared side by side
      fireEvent.doubleClick(blockEl(/Video block Shot 2/));
      dlg = screen.getByRole("dialog", { name: /^Video · Shot 2/ });
      fireEvent.click(within(dlg).getByRole("button", { name: /^Generate/ }));
      await waitFor(() => expect(studio.boards[0].runs.at(-1)?.block).toBe("s2"));
      finishRuns();
      await waitFor(() =>
        expect(within(dlg).getByRole("button", { name: "Compare" })).toBeTruthy(),
      );
      fireEvent.click(within(dlg).getByRole("button", { name: "Compare" }));
      const cmp = screen.getByRole("dialog", { name: /Compare takes · Shot 2/ });
      const left = within(cmp).getByRole("combobox", { name: "Left take" }) as HTMLSelectElement;
      const right = within(cmp).getByRole("combobox", { name: "Right take" }) as HTMLSelectElement;
      expect(left.value).not.toBe(right.value);
      fireEvent.click(
        within(within(cmp).getByRole("region", { name: "Right" })).getByRole("button", {
          name: /Pin take/,
        }),
      );
      expect(within(cmp).getByText(/is pinned/)).toBeTruthy();
    }
  });

  it("shortening a shot shows the warning; Trim fits the line (frame 5b)", async () => {
    seedSample((d) => void (d.blocks.find((x) => x.id === "s1")!.length = 4 * 24));
    await board();
    // the bottom-left warning icon counts it; its list holds the row with the fixes
    expect(screen.getByRole("button", { name: /to fix: .*1 doesn't fit/ })).toBeTruthy();
    const list = popOf("Blocks to fix");
    const row = within(list)
      .getAllByRole("listitem", { hidden: true })
      .find((r) => /runs 3.1 s past Shot 1/.test(r.textContent ?? ""))!;
    fireEvent.click(within(row).getByText("Trim", { selector: "button" }));
    expect(screen.queryByText(/doesn't fit: runs/)).toBeNull();
    expect(blockEl(/Voice block Line 1, 3.4 s/)).toBeTruthy();
  });

  it("what to fix is one warning icon at the bottom left, its list upward with each fix (frame 11)", async () => {
    await board();
    const icon = screen.getByRole("button", { name: "1 block to fix: 1 out of date" });
    expect(icon.getAttribute("aria-expanded")).toBe("false");
    // no bar under the toolbar any more
    expect(screen.queryByText("Shot 3 out of date")).toBeNull();
    const list = popOf("Blocks to fix");
    const [row] = within(list).getAllByRole("listitem", { hidden: true });
    expect(row.textContent).toContain("Shot 3");
    expect(row.textContent).toContain("out of date");
    expect(within(row).getByText("Run", { selector: "button" })).toBeTruthy();
    expect(within(row).getByText("Pin take", { selector: "button" })).toBeTruthy();
    expect(within(list).getByText("Run all (1)", { selector: "button" })).toBeTruthy();
    // its name selects the block; a right-click gives the block's menu
    fireEvent.click(within(row).getByText("Shot 3").closest("button")!);
    expect(blockEl(/Video block Shot 3/).getAttribute("aria-pressed")).toBe("true");
    fireEvent.contextMenu(row, { button: 2, clientX: 10, clientY: 10 });
    expect(popOf("Shot 3 · puddle").getAttribute("role")).toBe("menu");
    closeMenu();
  });

  it("Play beside Run board plays the board from the playhead and turns into Pause (frame 1)", async () => {
    await board();
    const bar = screen.getByRole("toolbar", { name: "Board tools" });
    const play = within(bar).getByRole("button", { name: "Play the board" });
    expect(play.nextElementSibling?.textContent).toContain("Run board");
    fireEvent.click(play);
    expect(
      within(bar).getByRole("button", { name: /Pause the board|Play the board/ }),
    ).toBeTruthy();
  });
  it("the player goes full screen from ⛶ (frame 1)", async () => {
    const req = vi.fn(() => Promise.resolve());
    Object.defineProperty(HTMLElement.prototype, "requestFullscreen", {
      value: req,
      configurable: true,
    });
    await board();
    const fp = screen.getByRole("region", { name: "Player, floating" });
    fireEvent.click(within(fp).getByRole("button", { name: "Full screen" }));
    expect(req).toHaveBeenCalledTimes(1);
  });
  it("the side panel's toggle is remembered in this browser", async () => {
    const { unmount } = await board();
    const toggle = screen.getByRole("button", { name: "Side panel" });
    expect(toggle.getAttribute("aria-expanded")).toBe("true");
    fireEvent.click(toggle);
    expect(screen.queryByRole("complementary", { name: "Block details" })).toBeNull();
    expect(localStorage.getItem("magic-side-panel")).toBe("0");
    unmount();
    await board();
    expect(screen.getByRole("button", { name: "Side panel" }).getAttribute("aria-expanded")).toBe(
      "false",
    );
  });

  it("a new board is empty, with a lane to start (frame 11)", async () => {
    await board();
    fireEvent.click(screen.getByRole("button", { name: "The Last Tide" }));
    fireEvent.click(screen.getByRole("button", { name: "New board", hidden: true }));
    // frame 12: its size and fps first, set once
    fireEvent.change(screen.getByRole("combobox", { name: "Size", hidden: true }), {
      target: { value: "2" }, // 9:16 · 720 × 1280
    });
    fireEvent.change(screen.getByRole("combobox", { name: "Frame rate", hidden: true }), {
      target: { value: "30" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Create", hidden: true }));
    expect(await screen.findByRole("heading", { name: "A new board" })).toBeTruthy();
    expect(
      studio.calls.find((c) => c.method === "POST" && c.path === "/v1/studio/magic/boards")?.body,
    ).toEqual({
      target: { width: 720, height: 1280, fps: 30, sample_rate: 48000 },
    });
    expect(screen.getByTitle("Set when the board was made").textContent).toMatch(
      /^720 × 1280 · 30 fps/,
    );
    expect(
      (screen.getByRole("button", { name: "Build from a brief" }) as HTMLButtonElement).disabled,
    ).toBe(true);
    fireEvent.click(screen.getByRole("button", { name: "Lane" }));
    expect(screen.getByRole("group", { name: "Lane Shots" })).toBeTruthy();
  });
});
