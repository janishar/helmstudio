import { describe, expect, it } from "vitest";
import fitFixture from "../../contracts/fixtures/voices/fit.json";
import scriptsFixture from "../../contracts/fixtures/voices/scripts.json";
import palette from "../../contracts/fixtures/graphs/palette.json";
import type { S } from "@/lib/api";
import { paletteOf } from "@/lib/graph";
import { reasonOf } from "@/lib/looks";
import { peaksOf } from "@/components/create/audio";
import {
  clipError,
  expandScript,
  scriptErrors,
  languageName,
  presetGroups,
  voiceBody,
  voiceFit,
  voiceLine,
  voiceSources,
  voiceTileText,
} from "@/lib/voices";

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

describe("voices with a clip (API 1.31)", () => {
  const clip = { take_id: "2b3c4d5e-6f70-4a81-9b92-a3b4c5d6e7f8", in: 4, out: 16 };
  const designed = voice("5a0c1e2f-3b4d-4e5f-8a6b-7c8d9e0f1a21", {
    name: "Teen",
    group: "designed",
    source: "mine",
    voice: { source: "designed", design: "Male, 17", language: "en", clip, consent: true },
  });
  const cloned = voice("5a0c1e2f-3b4d-4e5f-8a6b-7c8d9e0f1a22", {
    name: "Jon",
    group: "cloned",
    source: "mine",
    voice: { source: "cloned", clip, consent: true },
  });
  it("sources, lines, the PUT body and the board's language words", () => {
    expect(voiceSources(designed)).toEqual(["design", "clip"]);
    expect(voiceSources(cloned)).toEqual(["clip"]);
    expect(voiceLine(cloned)).toBe("0:04–0:16");
    expect(voiceBody(designed)).toEqual({
      design: "Male, 17",
      clip,
      consent: true,
      language: "en",
    });
    expect(voiceBody(cloned)).toEqual({ clip, consent: true });
    expect(languageName("en-US")).toBe("English (US)");
    // ZONOS3 names zh Mandarin (its board); Qwen3-TTS keeps Chinese
    expect([languageName("zh", "zonos3"), languageName("zh", "qwen3_tts")]).toEqual([
      "Mandarin",
      "Chinese",
    ]);
    expect(
      voiceTileText(
        voice("zonos3/savannah", {
          name: "Savannah",
          voice: { source: "preset", family: "zonos3", speaker: "x", language: "en-US" },
        }),
        true,
      ),
    ).toBe("Savannah · preset · English (US)");
  });
  it("a clip's limits: 3–30 s of a take with sound", () => {
    const t = { id: "t", kind: "audio", duration_s: 40 };
    expect(clipError({ take_id: "t", in: 4, out: 16 }, t)).toBeNull();
    // 4.1 − 1.1 is a hair under 3 in floating point: still 3 s, as studio snaps it
    expect(clipError({ take_id: "t", in: 1.1, out: 4.1 }, t)).toBeNull();
    expect(clipError({ take_id: "t", in: 4, out: 6 }, t)).toBe(
      "clip: the part lasts 2.0 s; a voice needs 3 to 30 s",
    );
    expect(clipError({ take_id: "t", in: 0, out: 31 }, t)).toBe(
      "clip: the part lasts 31.0 s; a voice needs 3 to 30 s",
    );
    expect(clipError({ take_id: "t", in: 5, out: 5 }, t)).toBe("in must be before out");
    // a whole take too short says so in the same words, never by its id
    expect(clipError({ take_id: "s", in: 0, out: 1.3 }, { ...t, id: "s", duration_s: 1.3 })).toBe(
      "clip: the part lasts 1.3 s; a voice needs 3 to 30 s",
    );
    expect(
      clipError(
        { take_id: "v", in: 0, out: 5 },
        { id: "v", kind: "video", duration_s: 8, has_audio: false },
      ),
    ).toBe("take v has no sound");
    expect(clipError({ take_id: "x" }, null)).toBe("take x not found");
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

describe("dialogue scripts (fixtures/voices/scripts.json)", () => {
  const fx = scriptsFixture as unknown as {
    workflows: Record<
      string,
      { nodes: Record<string, { kind: string; params?: Record<string, unknown> }> }
    >;
    library: Parameters<typeof expandScript>[2];
    voices: Record<string, Item>;
    cases: {
      description: string;
      workflow: string;
      params?: Record<string, unknown>;
      expect: unknown;
    }[];
  };
  it.each(fx.cases)("$description", (c) => {
    const nodes = structuredClone(fx.workflows[c.workflow].nodes);
    for (const [k, v] of Object.entries(c.params ?? {})) {
      const [n, p] = k.split(".");
      nodes[n].params = { ...nodes[n].params, [p]: v };
    }
    expect(expandScript(nodes, schemas, fx.library, fx.voices)).toEqual(c.expect);
  });
  it("reads studio's 422 by line", () => {
    expect(
      scriptErrors(
        "dialogue.script line 3: unknown element @jon; dialogue.script line 4: @kai has no voice; dialogue.lines: at most 40 lines",
      ),
    ).toEqual({
      lines: { 3: "unknown element @jon", 4: "@kai has no voice" },
      other: ["dialogue.lines: at most 40 lines"],
    });
  });
});
