import { describe, expect, it } from "vitest";
import fitFixture from "../../contracts/fixtures/voices/fit.json";
import palette from "../../contracts/fixtures/graphs/palette.json";
import type { S } from "@/lib/api";
import { paletteOf } from "@/lib/graph";
import { reasonOf } from "@/lib/looks";
import { peaksOf } from "@/components/create/audio";
import { presetGroups, voiceFit, voiceLine, voiceTileText } from "@/lib/voices";

type Item = S<"LibraryItem">;
const schemas = paletteOf(palette as unknown as S<"NodePalette">);
const wfs = fitFixture.workflows as unknown as Record<string, { nodes: S<"Workflow">["nodes"] }>;

describe("voice fit (fixtures/voices/fit.json)", () => {
  it.each(fitFixture.cases)("$description", (c) => {
    const w = { nodes: wfs[c.workflow].nodes, node_schemas: schemas };
    const item = c.item as unknown as Item;
    const got = item.kind === "voice" ? voiceFit(w, item) : reasonOf(w, item, {});
    expect(got).toBe(c.expect);
  });
});

const voice = (id: string, more: Partial<Item>): Item =>
  ({
    id,
    kind: "voice",
    group: "preset",
    name: id,
    handle: null,
    text: "",
    images: [],
    thumb_url: null,
    version: 1,
    created_at: "2026-11-21T10:00:00Z",
    updated_at: "2026-11-21T10:00:00Z",
    source: "starter",
    family: null,
    loras: [],
    needs: [],
    ...more,
  }) as Item;

describe("voice words", () => {
  const ryan = voice("qwen3-tts/ryan", {
    name: "Ryan",
    voice: {
      source: "preset",
      family: "qwen3-tts",
      speaker: "Ryan",
      language: "en",
      style: "lively",
    },
  });
  const ada = voice("echo/ada", {
    name: "Ada",
    voice: { source: "preset", family: "echo", speaker: "ada", language: "en" },
  });
  const mine = voice("5a0c1e2f-3b4d-4e5f-8a6b-7c8d9e0f1a21", {
    name: "Old captain",
    group: "designed",
    source: "mine",
    voice: { source: "designed", design: "Male, about 60, low and warm" },
  });
  it("lines, tiles and groups", () => {
    expect(voiceLine(ryan)).toBe("lively · English");
    expect(voiceLine(ada)).toBe("English");
    expect(voiceLine(mine)).toBe("Male, about 60, low and warm");
    expect(voiceTileText(ryan)).toBe("Ryan · preset");
    expect(voiceTileText(ryan, true)).toBe("Ryan · preset · English");
    expect(voiceTileText(undefined)).toBe("Pick a voice");
    expect(presetGroups([mine, ryan, ada]).map((g) => [g.family, g.voices.length])).toEqual([
      ["qwen3-tts", 1],
      ["echo", 1],
    ]);
  });
});

describe("peaksOf", () => {
  it("the loudest sample of each slice, against the loudest of all", () => {
    expect(peaksOf(new Float32Array([0, 0.25, -0.5, 0.125, 0, 0]), 3)).toEqual([0.5, 1, 0]);
    expect(peaksOf(new Float32Array(4), 2)).toEqual([0, 0]);
  });
});

describe("designWorkflow", () => {
  it("picks the template whose voice param takes designed voices only (its only), not the voice-over", async () => {
    const { designWorkflow } = await import("@/components/create/voices");
    const qwen = (await import("../../contracts/fixtures/registry/qwen3-tts.workflows.valid.json"))
      .workflows;
    const nodes = (await import("../../contracts/fixtures/registry/qwen3-tts.nodes.valid.json"))
      .nodes;
    const node_schemas = Object.fromEntries(nodes.map((n) => [n.kind, n]));
    const ws = qwen.map((w) => ({
      ...w,
      source: "template",
      node_schemas,
    })) as unknown as S<"Workflow">[];
    expect(designWorkflow(ws, "qwen3-tts")?.id).toBe("qwen3-tts-design-voice");
    expect(designWorkflow([...ws].reverse(), "qwen3-tts")?.id).toBe("qwen3-tts-design-voice");
  });
});
