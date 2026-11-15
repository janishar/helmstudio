import { describe, expect, it } from "vitest";
import fitFixture from "../../contracts/fixtures/mentions/fit.json";
import palette from "../../contracts/fixtures/graphs/palette.json";
import type { S } from "@/lib/api";
import { paletteOf } from "@/lib/graph";
import {
  chosenFrom,
  chosenIds,
  filterItems,
  liveChosen,
  lookLine,
  looksReuseNote,
  reasonOf,
  tileState,
  tilesOf,
} from "@/lib/looks";

type Item = S<"LibraryItem">;
const schemas = paletteOf(palette as unknown as S<"NodePalette">);
const wfs = fitFixture.workflows as unknown as Record<string, { nodes: S<"Workflow">["nodes"] }>;
const w = (name: string, studio?: S<"StudioLayout">) => ({
  nodes: wfs[name].nodes,
  node_schemas: schemas,
  studio,
});
const ink = "3f0a6c1e9b2d4a7c8e5f1b3d6a9c2e4f7b0d3a6c9e2f5b8d1a4c7e0f3b6d9a2c";
const loras = { [ink]: "qwen-image" };

const look = (id: string, group: Item["group"], more: Partial<Item> = {}): Item => ({
  id,
  kind: "look",
  group,
  name: id,
  handle: null,
  text: "some words",
  images: [],
  thumb_url: `https://x/${id}.webp`,
  version: 1,
  created_at: "2026-11-01T00:00:00Z",
  updated_at: "2026-11-01T00:00:00Z",
  source: id.includes("/") ? "starter" : "mine",
  family: null,
  loras: [],
  needs: ["text"],
  ...more,
});
const window = look("qwen-image/window", "lighting");
const portra = look("qwen-image/portra", "film", {
  text: "",
  needs: ["lora"],
  loras: [{ lora: { lora_id: ink }, name: "ink-wash", strength: 0.8 }],
});
const noir = look("10000000-0000-4000-8000-000000000003", "colour", { name: "Noir" });

describe("composer tiles", () => {
  it("default: Elements with a prompt, then each look group with a fitting item, in group order", () => {
    expect(tilesOf(w("qwen-lora"), [window, portra, noir], loras)).toEqual([
      "elements",
      "look:film",
      "look:colour",
      "look:lighting",
    ]);
    // no LoRA stack: the LoRA-only film look does not fit, so no Film tile
    expect(tilesOf(w("qwen-basic"), [window, portra], loras)).toEqual([
      "elements",
      "look:lighting",
    ]);
    // a group with chosen looks keeps its tile (Reuse), a graph without a prompt has no Elements
    expect(tilesOf(w("noise"), [window], loras, { lighting: [window.id] })).toEqual([
      "look:lighting",
    ]);
  });
  it("with a Studio block: exactly its quick tiles, in order", () => {
    const studio = { quick: ["look:lighting", "seed.seed", "elements", "look:bogus"] };
    expect(tilesOf(w("echo", studio), [window], loras)).toEqual(["look:lighting", "elements"]);
  });
  it("sends the chosen looks in tile order, then pick order, each once", () => {
    const chosen = { lighting: ["b", "a"], film: ["c", "a"], camera: ["d"] };
    expect(chosenIds(["elements", "look:lighting", "look:film"], chosen)).toEqual(["b", "a", "c"]);
  });
  it("shows the first chosen look on its tile", () => {
    expect(tileState("lighting", {}, [window])).toEqual({ thumb: null, name: "Auto", count: 0 });
    expect(tileState("lighting", { lighting: [window.id, "x"] }, [window])).toEqual({
      thumb: window.thumb_url,
      name: "qwen-image/window +1",
      count: 2,
    });
  });
  it("puts reused ids under their groups and drops unknown ones", () => {
    expect(chosenFrom([noir.id, "gone", window.id, noir.id], [window, noir])).toEqual({
      colour: [noir.id],
      lighting: [window.id],
    });
  });
});

describe("the visual picker", () => {
  it("filters by group, source and words", () => {
    const all = [window, portra, noir];
    expect(filterItems(all, { group: "film" })).toEqual([portra]);
    expect(filterItems(all, { source: "mine" })).toEqual([noir]);
    expect(filterItems(all, { q: "  NOI " })).toEqual([noir]);
    expect(filterItems(all, { q: "qwen window" })).toEqual([window]);
  });
  it("greys what does not fit with the contract's reason", () => {
    expect(reasonOf(w("qwen-basic"), portra, loras)).toBe("this workflow has no LoRA stack");
    expect(reasonOf(w("qwen-lora"), portra, {})).toBe("LoRA ink-wash is not available");
    expect(reasonOf(w("noise"), window, loras)).toBe("this workflow has no prompt");
    expect(reasonOf(w("echo"), window, loras)).toBeNull();
  });
  it("says what a look brings", () => {
    expect(lookLine(portra)).toBe("LoRA · starter");
    expect(lookLine({ ...noir, needs: ["text", "images", "lora"] })).toBe("text + images + LoRA");
  });
});

describe("chosen looks", () => {
  it("drop a look deleted or on no live host since, and keep all while the list loads", () => {
    const chosen = { film: ["gone", portra.id], lighting: [window.id] };
    expect(liveChosen(chosen, [window, portra])).toEqual({
      film: [portra.id],
      lighting: [window.id],
    });
    expect(chosenIds(["look:film"], liveChosen(chosen, [portra]))).toEqual([portra.id]);
    expect(liveChosen(chosen, null)).toBe(chosen);
  });
});

describe("reuse", () => {
  it("names the looks that changed or went", () => {
    const made = [
      { id: "a", source: "mine" as const, name: "Noir", version: 2 },
      { id: "b", source: "starter" as const, name: "Window", version: 1 },
      { id: "c", source: "mine" as const, name: "Old", version: 1 },
    ];
    expect(
      looksReuseNote(
        [
          { id: "a", status: "changed" },
          { id: "b", status: "missing" },
          { id: "c", status: "deleted" },
        ],
        made,
      ),
    ).toBe(
      "Since this take: look Noir was edited: a new run uses it as it is now; look Window is on no live host now, so it is left out; look Old was deleted, so it is left out.",
    );
    expect(looksReuseNote([{ id: "a", status: "current" }], made)).toBe("");
  });
});
