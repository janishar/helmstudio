// Magic's assets and storyboard (plan 39 phase E; contracts/README.md "Magic: assets and the storyboard"): a free
// handle, an Element and takes imported as a copy, the @ menu's matches, the handles typed in full, the views Make
// views makes, the rough cut's frames and lines; a deleted asset's mentions in a prompt (lib/magic.ts).
import { describe, expect, it } from "vitest";
import {
  atMatches,
  freeHandle,
  fromElement,
  fromTakes,
  mentionedRefs,
  missingViews,
  roughCut,
  viewName,
} from "@/lib/magic-assets";
import { apply, replaceMention, usedIn } from "@/lib/magic";
import { sampleBoard } from "@/lib/magic-fixture";
import type { S } from "@/lib/api";

describe("assets", () => {
  it("a free handle: the wanted one, else from the name, -2 when taken", () => {
    const { doc } = sampleBoard();
    expect(freeHandle(doc, "Old man")).toBe("old-man");
    expect(freeHandle(doc, "Mara")).toBe("mara-2");
    expect(freeHandle(doc, "x", "fox")).toBe("fox");
    expect(freeHandle(doc, "Mara", "Not A Handle")).toBe("mara-2");
  });

  it("an Element is copied in: its pictures, voice and words; a location is a place", () => {
    const { doc } = sampleBoard();
    const e = {
      id: "e1",
      kind: "element",
      group: "location",
      name: "Pier",
      handle: "pier",
      text: "wet planks at dawn",
      images: [{ take_id: "t1" }, { take_id: "t2" }],
      voice_id: null,
    } as unknown as S<"LibraryItem">;
    expect(fromElement(doc, e)).toEqual({
      kind: "place",
      handle: "pier",
      name: "Pier",
      sheet: ["t1", "t2"],
      note: "wet planks at dawn",
      imported_from: "e1",
    });
    expect(fromTakes(doc, [{ id: "t9", title: "Mara" }])).toMatchObject({
      handle: "mara-2",
      sheet: ["t9"],
      imported_from: "t9",
    });
  });

  it("the @ menu: assets by handle or name, then other blocks' pictures", () => {
    const { doc } = sampleBoard();
    expect(atMatches(doc, "ma").map((m) => m.label)).toEqual(["@mara"]);
    expect(atMatches(doc, "shot 2", "s3").map((m) => m.label)).toEqual([
      "@Shot 2 · last frame",
      "@Shot 2 · first frame",
    ]);
    expect(atMatches(doc, "", "f1").some((m) => m.label === "@Frame 1")).toBe(false);
  });

  it("handles typed in full join the refs once; @@ and an @ after a word don't", () => {
    const { doc } = sampleBoard();
    expect(mentionedRefs(doc, "@mara and @Bus-stop, again @mara", [])).toEqual([
      { asset: "mara" },
      { asset: "stop" },
    ]);
    expect(mentionedRefs(doc, "@@mara x@mara", [])).toEqual([]);
    expect(mentionedRefs(doc, "@mara", [{ asset: "mara" }])).toEqual([]);
  });

  it("Make views: the views of its kind not on the sheet, all when every one is", () => {
    const { doc } = sampleBoard();
    const mara = doc.assets[0];
    expect(missingViews(mara)).toEqual(["Front", "Side", "Back", "Full figure"]);
    const named = { ...mara, views: { "mara-1": "Front", "mara-2": "Side" } };
    expect(missingViews(named)).toEqual(["Back", "Full figure"]);
    expect(missingViews(named, ["Back"])).toEqual(["Full figure"]);
    expect(viewName(named, "mara-2")).toBe("Side");
    expect(viewName(named, "mara-3")).toBe("View 3");
  });

  it("delete: its first and last frame picks go, its mentions become its name; used in counts them", () => {
    const { doc } = sampleBoard();
    const s3 = doc.blocks.find((b) => b.id === "s3")!;
    let r = apply(doc, {
      op: "block",
      id: "s3",
      set: { request: { ...s3.request, prompt: "@mara runs", first: { asset: "mara" } } },
    });
    if ("error" in r) throw new Error(r.error);
    expect(usedIn(r.doc, "mara").some((b) => b.id === "s3")).toBe(true);
    r = apply(r.doc, { op: "asset_delete", id: "mara" });
    if ("error" in r) throw new Error(r.error);
    const after = r.doc.blocks.find((b) => b.id === "s3")!;
    expect(after.request.first).toBeUndefined();
    expect(after.request.prompt).toBe("Mara runs");
    expect(replaceMention("@Mara, @@mara, x@mara, @maraa @mara-", "mara", "M")).toBe(
      "M, @@mara, x@mara, @maraa M-",
    );
  });
});

describe("the rough cut", () => {
  it("frames in time order from a frame, each with its line, else the lines under it", () => {
    const { doc } = sampleBoard();
    const cut = roughCut(doc);
    expect(cut.map((c) => c.block.id)).toEqual(["f1", "f2", "f3"]);
    expect(cut[0].line).toBe("Every night, the same bus.");
    expect(cut[0].voices.map((v) => v.id)).toEqual(["v1"]);
    const f2 = doc.blocks.find((b) => b.id === "f2")!;
    f2.request.line = "Her own line.";
    expect(roughCut(doc, "f2").map((c) => [c.block.id, c.line])[0]).toEqual(["f2", "Her own line."]);
  });
});
