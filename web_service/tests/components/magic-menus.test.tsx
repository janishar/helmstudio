// @vitest-environment jsdom
// Magic's right-click menus (MagicBoard frame 10): the items per thing, in the board's order, what is greyed and why,
// and the op each pick sends; one menu opened through the shared menu.
import { afterEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { closeMenu, openMenu, type Item, type MenuItem } from "@/components/context-menu";
import {
  assetMenu,
  blockMenu,
  emptyMenu,
  joinMenu,
  laneMenu,
  multiMenu,
  tieMenu,
  effectMenu,
  viewMenu,
  type Act,
} from "@/components/magic/menus";
import { joins, type Doc } from "@/lib/magic";
import { sampleBoard } from "@/lib/magic-fixture";

afterEach(() => {
  act(closeMenu);
  cleanup();
});

function actOn(
  over: Partial<Act> = {},
): Act & { edit: ReturnType<typeof vi.fn>; run: ReturnType<typeof vi.fn> } {
  const { doc, takes } = sampleBoard();
  return {
    doc,
    takes,
    mode: "insert",
    playhead: 10 * 24,
    sel: [],
    edit: vi.fn(),
    run: vi.fn(),
    select: vi.fn(),
    open: vi.fn(),
    add: vi.fn(),
    play: vi.fn(),
    seek: vi.fn(),
    rename: vi.fn(),
    used: vi.fn(),
    paste: null,
    copyText: vi.fn(),
    ...over,
  } as Act & { edit: ReturnType<typeof vi.fn>; run: ReturnType<typeof vi.fn> };
}
const block = (d: Doc, id: string) => d.blocks.find((b) => b.id === id)!;
/** studio's models (API 1.85): music and sound, MMAudio takes a shot's video */
const MODELS: Act["models"] = (k) =>
  k === "music"
    ? [
        { id: "minimax-music3", name: "MiniMax Music 3", video: false },
        { id: "ace-step-music", name: "ACE-Step 1.5", video: false },
      ]
    : k === "sound"
      ? [
          { id: "sa3-sound", name: "Stable Audio 3 Small-SFX", video: false },
          { id: "mmaudio-sound", name: "MMAudio", video: true },
        ]
      : [];
/** the menu's labels, "-" for a separator */
const labels = (items: Item[]) =>
  items.filter((x): x is MenuItem | "-" => !!x).map((x) => (x === "-" ? "-" : x.label));
const item = (items: Item[], label: string) =>
  items.find((x): x is MenuItem => !!x && x !== "-" && x.label === label)!;

describe("a block's menu, by kind", () => {
  it("a shot: Open, Run with its estimate, … Delete last and red", () => {
    const a = actOn();
    const m = blockMenu(a, block(a.doc, "s2"));
    expect(m.title).toBe("Shot 2 · umbrella");
    expect(labels(m.items)).toEqual([
      "Open",
      "Run",
      "Run up to here",
      "Compare takes",
      "Pin take",
      "-",
      "Split at playhead",
      "Duplicate",
      "Add effect",
      "Tie to",
      "Make sound from video",
      "Use last frame in a new shot",
      "-",
      "Delete",
    ]);
    expect(item(m.items, "Run").keys).toBe("≈ 1:50");
    expect(item(m.items, "Delete").danger).toBe(true);
    // plan 39 phase H: a sound under the shot, tied to it, made from its video on a model that takes one, then run
    expect(item(m.items, "Make sound from video").off).toBe("No sound model takes a video");
    const withModels = actOn({ models: MODELS, runWhenAdded: vi.fn() });
    const make = item(
      blockMenu(withModels, block(withModels.doc, "s2")).items,
      "Make sound from video",
    );
    expect(make.off).toBeUndefined();
    make.onPick!();
    const [add, link] = withModels.edit.mock.calls[0][0];
    expect(add).toMatchObject({
      op: "add",
      mode: "overwrite",
      block: {
        lane: "sound",
        kind: "sound",
        request: { kind: "sound", model: "mmaudio-sound", from: "s2" },
      },
    });
    expect(link).toEqual({ op: "link", block: add.block.id, to: ["s2"] });
    expect(withModels.runWhenAdded).toHaveBeenCalledWith(add.block.id);
    item(m.items, "Split at playhead").onPick!();
    expect(a.edit).toHaveBeenCalledWith({ op: "split", id: "s2", at: 240 });
    item(m.items, "Delete").onPick!();
    expect(a.edit).toHaveBeenLastCalledWith({ op: "delete", ids: ["s2"], mode: "insert" });
    // a pinned shot unpins
    expect(labels(blockMenu(a, block(a.doc, "s1")).items)).toContain("Unpin take");
  });

  it("a line: Play, Voice ›, Go to its shot, Fit to shot, Unlink, Move to lane ›", () => {
    const a = actOn();
    const m = blockMenu(a, block(a.doc, "v2"));
    expect(labels(m.items)).toEqual([
      "Open",
      "Run",
      "Play",
      "Voice",
      "-",
      "Go to Shot 2",
      "Fit to shot",
      "Unlink",
      "-",
      "Duplicate",
      "Move to lane",
      "-",
      "Delete",
    ]);
    expect(item(m.items, "Run").keys).toBe("≈ 12 s");
    item(m.items, "Unlink").onPick!();
    expect(a.edit).toHaveBeenCalledWith({ op: "unlink", block: "v2" });
    item(m.items, "Go to Shot 2").onPick!();
    expect(a.select).toHaveBeenCalledWith(["s2"]);
  });

  it("music and sound can't run before Create's Music and Sound", () => {
    const a = actOn();
    const music = blockMenu(a, block(a.doc, "m1"));
    expect(labels(music.items)).toEqual([
      "Open",
      "Run",
      "Play",
      "Model",
      "-",
      "Tie to",
      "Fit to shots",
      "Unlink",
      "-",
      "Duplicate",
      "Move to lane",
      "-",
      "Delete",
    ]);
    expect(item(music.items, "Run").off).toBeUndefined();
    // Model ›: studio's music models, the default checked while the block names none
    expect(item(music.items, "Model").off).toBe("No music model on any host");
    const withModels = actOn({ models: MODELS });
    const models = item(blockMenu(withModels, block(withModels.doc, "m1")).items, "Model")
      .items as MenuItem[];
    expect(models.map((x) => x.label)).toEqual(["MiniMax Music 3", "ACE-Step 1.5"]);
    models[1].onPick!();
    expect(withModels.edit.mock.calls[0][0].set.request.model).toBe("ace-step-music");
    const tie = item(music.items, "Tie to").items as MenuItem[];
    expect(tie.at(-1)).toMatchObject({ label: "Shots 1–3", checked: true });
    const sound = blockMenu(a, block(a.doc, "x2"));
    expect(labels(sound.items)).toContain("Make from Shot 2 video");
    const sa = actOn({ models: MODELS });
    item(blockMenu(sa, block(sa.doc, "x2")).items, "Make from Shot 2 video").onPick!();
    expect(sa.edit.mock.calls[0][0].set.request).toMatchObject({
      from: "s2",
      model: "mmaudio-sound",
    });
    expect(sa.run).toHaveBeenCalledWith(["x2"]);
  });

  it("a storyboard frame: Redraw, Style ›, Generate from board (greyed once a shot is made from it)", () => {
    const a = actOn();
    const f1 = blockMenu(a, block(a.doc, "f1"));
    expect(labels(f1.items)).toEqual([
      "Open",
      "Redraw",
      "Style",
      "Replace with an image",
      "-",
      "Generate from board",
      "Rough cut from here",
      "-",
      "Duplicate",
      "-",
      "Delete",
    ]);
    expect(item(f1.items, "Generate from board").off).toBe("Shot 1 · bus stop is made from it");
    // without the board's Generate from board (the fake mode's menus alone) it is greyed
    expect(item(blockMenu(a, block(a.doc, "f3")).items, "Generate from board").off).toBeTruthy();
    const fromBoard = vi.fn();
    const rough = vi.fn();
    const gallery = vi.fn();
    const b = actOn({ fromBoard, rough, gallery });
    const f3 = blockMenu(b, block(b.doc, "f3"));
    item(f3.items, "Generate from board").onPick!();
    expect(fromBoard).toHaveBeenCalledWith("f3");
    item(f3.items, "Rough cut from here").onPick!();
    expect(rough).toHaveBeenCalledWith("f3");
    // Replace with an image: a Gallery picture joins the frame's takes, pinned
    item(f3.items, "Replace with an image").onPick!();
    gallery.mock.calls[0][0].onPick([{ id: "t-own", kind: "image" }]);
    expect(b.edit).toHaveBeenCalledWith({ op: "take_add", id: "f3", take: "t-own" });
  });

  it("several selected blocks get the several menu", () => {
    const a = actOn({ sel: ["s1", "s2", "s3"] });
    const m = blockMenu(a, block(a.doc, "s2"));
    expect(m.title).toBe("3 blocks");
    expect(labels(m.items)).toEqual([
      "Run 3",
      "Move to lane",
      "Add effect",
      "Duplicate",
      "-",
      "Delete 3",
    ]);
    expect(item(m.items, "Run 3").keys).toBe("≈ 6 min");
    expect(
      multiMenu(actOn(), ["s1", "v1"]).items.find(
        (x) => x && x !== "-" && x.label === "Move to lane",
      ),
    ).toMatchObject({
      off: "Pick blocks from one lane",
    });
  });
});

describe("the other things on the board", () => {
  it("a lane header", () => {
    const a = actOn();
    const lines = a.doc.lanes.find((l) => l.id === "lines")!;
    const m = laneMenu(a, lines);
    expect(m.title).toBe("Lane · Mara's lines");
    expect(labels(m.items)).toEqual([
      "Rename",
      "Add here",
      "Run lane",
      "Default kind",
      "-",
      "Move up",
      "Move down",
      "Hide",
      "New lane below",
      "-",
      "Delete lane",
    ]);
    expect(item(m.items, "Delete lane").keys).toBe("2 blocks");
    item(m.items, "Hide").onPick!();
    expect(a.edit).toHaveBeenCalledWith({ op: "lane", id: "lines", set: { hidden: true } });
    expect(item(laneMenu(a, a.doc.lanes[0]).items, "Move up").off).toBe("It's the first lane");
  });

  it("the empty lane at a time: Add each kind (Music and Sound greyed), Paste, Playhead here", () => {
    const a = actOn();
    const l7 = a.doc.lanes.find((l) => l.id === "l7")!;
    const m = emptyMenu(a, l7, 18 * 24);
    expect(m.title).toBe("Lane 7 · at 0:18");
    expect(labels(m.items)).toEqual([
      "Add Video",
      "Add Image",
      "Add Voice",
      "Add Music",
      "Add Sound",
      "Add Board frame",
      "-",
      "Paste",
      "From Gallery",
      "Playhead here",
    ]);
    expect(item(m.items, "Add Music").off).toBeUndefined();
    expect(item(m.items, "Paste").off).toBe("Copy a block first");
    item(m.items, "Add Video").onPick!();
    expect(a.add).toHaveBeenCalledWith("l7", 432, "video");
  });

  it("an asset, a join, a tie mark and an effect badge", () => {
    const a = actOn();
    const mara = assetMenu(a, a.doc.assets[0]);
    expect(mara.title).toBe("@mara");
    expect(item(mara.items, "Show where used").keys).toBe("7");
    expect(item(mara.items, "Delete").keys).toBe("used in 7");

    const [, dis] = joins(a.doc, "shots");
    const j = joinMenu(a, dis);
    expect(j.title).toBe("Join · Shot 1 → Shot 2");
    expect(labels(j.items)).toEqual([
      "Cut",
      "Dissolve",
      "Fade",
      "Wipe",
      "Morph",
      "-",
      "Length",
      "Remove",
    ]);
    expect(item(j.items, "Dissolve")).toMatchObject({ checked: true, keys: "0.5 s" });
    expect(item(j.items, "Morph").keys).toBe("generated");
    item(j.items, "Remove").onPick!();
    expect(a.edit).toHaveBeenCalledWith({ op: "transition", between: ["s1", "s2"], kind: "cut" });

    const t = tieMenu(a, a.doc.links[1]);
    expect(t.title).toBe("Tie · Line 2 → Shot 2");
    expect(labels(t.items)).toEqual(["Go to Shot 2", "Tie to", "Fit to shot", "-", "Unlink"]);

    const fx = effectMenu(a, block(a.doc, "s2"), 0);
    expect(fx.title).toBe("Grade · warm");
    expect(labels(fx.items)).toEqual(["Edit", "Turn off", "Copy to selected", "-", "Remove"]);
    item(fx.items, "Turn off").onPick!();
    expect(a.edit).toHaveBeenLastCalledWith({
      op: "effect_set",
      id: "s2",
      index: 0,
      effect: { name: "Grade · warm", params: { temperature: 12, contrast: 4 }, off: true },
    });
  });

  it("opens in the shared menu with its header, estimate and greyed reason", () => {
    const a = actOn({ runOff: "Runs come with the standard request" });
    const m = blockMenu(a, block(a.doc, "m1"));
    render(
      <button type="button" onContextMenu={(e) => openMenu(e, m)}>
        music
      </button>,
    );
    fireEvent.contextMenu(screen.getByText("music"), { button: 2, clientX: 10, clientY: 10 });
    const menu = screen.getByRole("menu", { name: "Soft rain theme" });
    expect(menu).toBeTruthy();
    const run = screen.getByRole("menuitem", { name: /Run/ });
    expect(run.getAttribute("aria-disabled")).toBe("true");
    expect(run.title).toBe("Runs come with the standard request");
    expect(screen.getByRole("menuitem", { name: /Delete/ }).className).toContain("danger");
  });
});

describe("phase E: assets, views and the Gallery", () => {
  it("an asset: Open, Make views of the missing ones, Import a view, Rename", () => {
    const openAsset = vi.fn();
    const views = vi.fn();
    const importTo = vi.fn();
    const a = actOn({ openAsset, views, importTo });
    const mara = assetMenu(a, a.doc.assets[0]);
    expect(labels(mara.items)).toEqual([
      "Open",
      "Show where used",
      "Copy @mara",
      "Make views",
      "Voice",
      "Import a view",
      "Rename",
      "-",
      "Delete",
    ]);
    item(mara.items, "Open").onPick!();
    expect(openAsset).toHaveBeenCalledWith("mara");
    expect(item(mara.items, "Make views").keys).toBe("Front, Side, Back, Full figure");
    item(mara.items, "Make views").onPick!();
    expect(views).toHaveBeenCalledWith("mara", ["Front", "Side", "Back", "Full figure"]);
    item(mara.items, "Import a view").onPick!();
    expect(importTo).toHaveBeenCalledWith("mara");
    // without runs Make views is greyed with why
    expect(item(assetMenu(actOn(), a.doc.assets[0]).items, "Make views").off).toBe(
      "Runs come with the standard request",
    );
  });

  it("a view on the sheet: Rename view, Make it the first, Take off the sheet", () => {
    const a = actOn();
    const x = { ...a.doc.assets[0], views: { "mara-2": "Side" } };
    const m = viewMenu(a, x, "mara-2");
    expect(m.title).toBe("@mara · Side");
    expect(labels(m.items)).toEqual([
      "Rename view",
      "Make it the first",
      "-",
      "Take off the sheet",
    ]);
    item(m.items, "Make it the first").onPick!();
    expect(a.edit).toHaveBeenLastCalledWith({
      op: "asset",
      id: "mara",
      set: { sheet: ["mara-2", "mara-1", "mara-3", "mara-4", "mara-5", "mara-6"] },
    });
    expect(item(viewMenu(a, x, "mara-1").items, "Make it the first").off).toBe("It's the first");
    item(m.items, "Take off the sheet").onPick!();
    expect(a.edit.mock.lastCall![0].set.sheet).not.toContain("mara-2");
  });

  it("the empty lane's From Gallery adds the take as a block at the point, pinned", () => {
    const gallery = vi.fn();
    const a = actOn({ gallery });
    const l7 = a.doc.lanes.find((l) => l.id === "l7")!;
    item(emptyMenu(a, l7, 18 * 24).items, "From Gallery").onPick!();
    expect(gallery.mock.calls[0][0]).toMatchObject({ kind: null, max: 1 });
    gallery.mock.calls[0][0].onPick([{ id: "t-v", kind: "video", duration: 2, title: "Rain" }]);
    expect(a.edit).toHaveBeenCalledWith({
      op: "add",
      mode: "insert",
      block: expect.objectContaining({
        lane: "l7",
        at: 432,
        length: 48,
        kind: "video",
        name: "Rain",
        takes: ["t-v"],
        pinned: "t-v",
      }),
    });
    // a voice lane asks for audio
    const lines = a.doc.lanes.find((l) => l.id === "lines")!;
    item(emptyMenu(a, lines, 0).items, "From Gallery").onPick!();
    expect(gallery.mock.calls[1][0].kind).toBe("audio");
    expect(item(emptyMenu(actOn(), l7, 0).items, "From Gallery").off).toBe(
      "The Gallery is in studio",
    );
  });
});
