// Media inputs and take actions against contracts/fixtures/video (check.py retake_map, extend_map, audio_map,
// media_fit) and the timeline's rules for them.
import { describe, expect, it } from "vitest";
import sources from "../../contracts/fixtures/video/sources.json";
import uses from "../../contracts/fixtures/video/uses.json";
import mediaRows from "../../contracts/fixtures/video/media-rows.json";
import h3Nodes from "../../contracts/fixtures/registry/minimax-h3.nodes.valid.json";
import h3Workflows from "../../contracts/fixtures/registry/minimax-h3.workflows.valid.json";
import ltxNodes from "../../contracts/fixtures/registry/ltx.nodes.valid.json";
import ltxWorkflows from "../../contracts/fixtures/registry/ltx.workflows.valid.json";
import type { S } from "@/lib/api";
import {
  blockNotes,
  buildInputs,
  fields,
  optionBlocked,
  rawsFrom,
  readValue,
  replaceUnmet,
  visible,
} from "@/lib/workflow";
import {
  actionSlot,
  audioMap,
  estimateLength,
  extendMap,
  familyGrid,
  floorGrid,
  markRange,
  mediaFit,
  mediaItem,
  mediaRowsError,
  progressive,
  rangeInTake,
  removeTrack,
  retakeMap,
  spanEdge,
  trimmed,
  type Facts,
} from "@/lib/source";

const LTX = { min: 9, step: 8, max: 321 };

describe("sources.json (the runner's maps)", () => {
  for (const c of sources.cases) {
    const a = c.args as [number, number | string, number, number?];
    const got =
      c.kind === "retake"
        ? retakeMap(a[0], a[1] as number, a[2], a[3]!, LTX)
        : c.kind === "extend"
          ? extendMap(a[0], a[1] as string, a[2], a[3]!, LTX)
          : audioMap(a[0], a[1] as number, a[2], LTX);
    // the runner's text names the node: the form shows it under the field
    const want =
      "error" in c.expect ? { error: (c.expect.error as string).split(": ")[1] } : c.expect;
    it(c.description, () => expect(got).toEqual(want));
  }
});

describe("uses.json media_fit texts", () => {
  const takes = uses.takes as Record<string, Omit<Facts, "id">>;
  for (const c of uses.cases.filter((x) => "param" in x))
    it(c.description, () =>
      expect(
        mediaFit((c as { param: S<"ParamSchema"> }).param, { ...takes[c.take], id: c.take }),
      ).toEqual((c.expect as { error: string | null }).error),
    );
});

describe("media-rows.json (API 1.22: rows of media, per item then in all)", () => {
  const takes = mediaRows.takes as Record<string, Omit<Facts, "id">>;
  const facts = (id: string) => (takes[id] ? { ...takes[id], id } : null);
  const schema = h3Nodes.nodes.find((n) => n.kind === "minimax-h3/generate")!;
  const rowsParams = (schema.params as S<"ParamSchema">[]).filter((p) => mediaItem(p));
  it("finds the two media rows params", () =>
    expect(rowsParams.map((p) => [p.name, mediaItem(p)![0]])).toEqual([
      ["videos", "video"],
      ["audios", "audio"],
    ]));
  it("Replace without a soundtrack blocks Generate (frame 6), with one it does not", () => {
    const videos = rowsParams.find((p) => p.name === "videos")!;
    const v = { take_id: "v1" };
    expect(
      replaceUnmet(videos, [
        { video: v, sound: "keep" },
        { video: v, sound: "replace" },
      ]),
    ).toBe("row 2: Replace needs a soundtrack");
    expect(
      replaceUnmet(videos, [{ video: v, sound: "replace", soundtrack: { take_id: "a1" } }]),
    ).toBe(null);
  });
  // the item and total checks; the drop of an optional node and not_with are studio's (the form sends neither)
  for (const c of mediaRows.cases.filter((x) => x.workflow === "minimax-h3-references"))
    it(c.description, () => {
      const inputs = c.inputs as unknown as Record<string, S<"RowItem">[]>;
      const got = rowsParams
        .map((p) =>
          mediaRowsError(`generate.${p.name}`, p, inputs[`generate.${p.name}`] ?? [], facts),
        )
        .find(Boolean);
      expect(got ?? null).toEqual(c.expect.error);
    });
  it("the references template places both in its composer", () =>
    expect(h3Workflows.workflows[2].studio.composer).toEqual([
      "prompt.text",
      "generate.refs",
      "generate.videos",
      "generate.audios",
    ]));
});

describe("the board's numbers", () => {
  it("Create: in 0.50 out 1.50 of 49 frames regenerates 9-32, 0.38-1.38 s", () => {
    const r = retakeMap(0.5, 1.5, 24, 49, LTX);
    expect("map" in r && [r.map.new_in_s.toFixed(2), r.map.new_out_s.toFixed(2)]).toEqual([
      "0.38",
      "1.38",
    ]);
  });
  it("Timeline: take 1.50-3.00 regenerates frames 33-64, 1.38-2.71 s", () => {
    const r = retakeMap(1.5, 3, 24, 121, LTX);
    expect("map" in r && [r.map.new_in_s * 24, r.map.new_out_s * 24 - 1]).toEqual([33, 64]);
  });
  it("Extend 2 s on 49 frames: 6 steps, 97 frames; 289 + 2 s is past 321", () => {
    expect(extendMap(2, "after", 24, 49, LTX)).toMatchObject({ latent: 6, frames: 97 });
    expect(extendMap(2, "after", 24, 289, LTX)).toEqual({
      error: "the clip would be 337 frames; at most 321",
    });
  });
  it("a 3 s audio part is 65 frames", () =>
    expect(audioMap(4, 7, 24, LTX)).toMatchObject({ frames: 65 }));
  it("floors like ltx_floor", () =>
    expect([5, 9, 49, 50, 57, 300].map((n) => floorGrid(n, LTX))).toEqual([9, 9, 49, 49, 57, 297]));
  it("the family's grid comes from its frames param", () =>
    expect(
      familyGrid(
        [
          { family: "qwen", params: [] },
          {
            family: "ltx",
            params: [{ name: "frames", type: "frames", default: 49, min: 9, step: 8, max: 321 }],
          },
        ],
        "ltx",
      ),
    ).toEqual(LTX));
});

describe("trim and span controls", () => {
  const video: Facts = { id: "v", kind: "video", fps: 24, frames: 49, duration_s: 49 / 24 };
  const audio: Facts = { id: "a", kind: "audio", duration_s: 20 };
  const p = { name: "source", type: "media", default: null, trim: true, max_s: 13.375 } as const;
  it("snaps a video's in to its frames, audio to the ms, never past out", () => {
    expect(trimmed({ take_id: "v", in: 0, out: 2 }, video, p, "in", 0.51)).toEqual({
      take_id: "v",
      in: 0.5,
      out: 2,
    });
    expect(trimmed({ take_id: "a" }, audio, p, "in", 4.0004)).toEqual({
      take_id: "a",
      in: 4,
      out: 13.375,
    });
    expect(trimmed({ take_id: "a", in: 0 }, { ...audio, duration_s: 5.0416 }, p, "out", 9)).toEqual(
      { take_id: "a", in: 0, out: 5.041 },
    );
    expect(trimmed({ take_id: "a", in: 4, out: 7 }, audio, p, "out", 2)).toEqual({
      take_id: "a",
      in: 4,
      out: 4,
    });
  });
  it("moves a span's edge on frames within the take", () => {
    expect(spanEdge({ start: 0.5, end: 1.5 }, "end", 9, 24, 49 / 24)).toEqual({
      start: 0.5,
      end: 49 / 24,
    });
    expect(spanEdge({ start: 0.5, end: 1.5 }, "start", 0.52, 24, 49 / 24)).toEqual({
      start: 0.5,
      end: 1.5,
    });
  });
});

describe("take actions on the timeline", () => {
  const clip = { id: "c3", at: 11, in: 0, out: 5.041666666666667 };
  it("maps the sequence range to the take's time, clamped to the clip", () => {
    expect(rangeInTake([12.5, 14], clip, 24)).toEqual([1.5, 3]);
    expect(rangeInTake([10, 12], { ...clip, in: 1 }, 24)).toEqual([1, 2]);
    expect(rangeInTake(null, clip, 24)).toEqual([0, clip.out]);
    expect(rangeInTake([0, 5], clip, 24)).toBeNull();
  });
  const offer = (inputs: S<"RunInputs">) =>
    ({
      workflow_id: "ltx-extend",
      title: "LTX-2.5 Extend",
      family: "ltx",
      media: "video",
      param: "source.source",
      inputs,
    }) as S<"TakeOffer">;
  it("Replace is the clip; insert goes after it, or at its start for an extend before", () => {
    const o = offer({ "extend.direction": "after" });
    expect(actionSlot(o, clip, "replace", 2, "n1")).toEqual({ clip: "c3" });
    expect(actionSlot(o, clip, "insert", 2, "n1")).toEqual({ after: "c3", length: 2, id: "n1" });
    expect(actionSlot(offer({ "extend.direction": "before" }), clip, "insert", 1, "n1")).toEqual({
      at: 11,
      length: 1,
      id: "n1",
    });
  });
  it("estimates the new part's length", () => {
    expect(estimateLength({ "extend.seconds": 2 }, 24, LTX)).toBe(2);
    expect(estimateLength({ "extend.seconds": 0.3 }, 24, LTX)).toBe(8 / 24);
    expect(estimateLength({ "retake.span": { start: 1.5, end: 3 } }, 24, LTX)).toBe(1.5);
    expect(
      estimateLength({ "audio.source": { take_id: "a", in: 0, out: 20 } }, 24, LTX, 13.375),
    ).toBe(13.375);
  });
  it("names the placeholder by its action", () =>
    expect(["Retake a section", "Extend", "Audio to video"].map(progressive)).toEqual([
      "Retaking",
      "Extending",
      "Audio to video",
    ]));
});

describe("removing an audio track", () => {
  const tracks = [
    { name: "V1", kind: "video", clips: [1] },
    { name: "A2", kind: "audio", clips: [1] },
    { name: "A3", kind: "audio", clips: [1, 2] },
    { name: "A4", kind: "audio", clips: [] },
  ];
  it("asks first only when the track has clips; V1 and A1 never go", () => {
    expect(removeTrack(tracks, "A3")).toEqual({ confirm: "Remove A3 and its 2 clips?" });
    expect(removeTrack(tracks, "A2")).toEqual({ confirm: "Remove A2 and its 1 clip?" });
    expect(removeTrack(tracks, "A4")).toEqual({ confirm: null });
    expect(removeTrack(tracks, "V1")).toBeNull();
    expect(removeTrack(tracks, "A1")).toBeNull();
  });
});

describe("the timeline's range", () => {
  it("marks in and out at the playhead, the other edge moving out of the way", () => {
    expect(markRange(null, "in", 12.5, 21)).toEqual([12.5, 21]);
    expect(markRange([12.5, 21], "out", 14, 21)).toEqual([12.5, 14]);
    expect(markRange([12.5, 14], "in", 15, 21)).toEqual([15, 21]);
    expect(markRange([12.5, 14], "out", 10, 21)).toEqual([0, 10]);
    expect(markRange(null, "out", 0, 21)).toBeNull();
  });
});

describe("the form of a template with media inputs", () => {
  const schemas = Object.fromEntries(ltxNodes.nodes.map((n) => [n.kind, n])) as unknown as Record<
    string,
    S<"NodeSchema">
  >;
  const of = (id: string) => {
    const w = ltxWorkflows.workflows.find((x) => x.id === id)!;
    return fields({ nodes: w.nodes as unknown as S<"Workflow">["nodes"], node_schemas: schemas });
  };
  const retake = of("ltx-retake");
  const byKey = (k: string) => retake.find((f) => f.key === k)!;
  it("offers only the values `only` allows", () => {
    expect(byKey("model.variant").param.enum).toEqual(["distilled", "dev"]);
    expect(byKey("model.precision").param.enum).toEqual(["int8", "int4"]);
  });
  it("needs a take and a range, worded as studio's 422", () => {
    expect(buildInputs(retake, {}).errors).toMatchObject({
      "source.source": "a take is required",
      "retake.span": "a range is required",
    });
    expect(readValue(byKey("retake.span").param, { start: 1, end: 1 })).toEqual({
      error: "start must be before end",
    });
  });
  // API 1.39 (README, Video: Distilled actions; docs/mock/LtxFeatures frames 6-8)
  it("hides Steps, CFG and STG on Distilled (the default) and shows them on Dev", () => {
    const guided = ["retake.steps", "retake.cfg", "retake.stg"];
    const shown = (raws: Record<string, string>) =>
      visible(retake, raws)
        .map((f) => f.key)
        .filter((k) => guided.includes(k));
    expect(byKey("model.variant").initial).toBe("distilled");
    expect(shown({})).toEqual([]);
    expect(shown({ "model.variant": "dev" })).toEqual(guided);
  });
  it("fades Extend's Before on Distilled with its reason, and Distilled under Before", () => {
    const extend = of("ltx-extend");
    const f = (k: string) => extend.find((x) => x.key === k)!;
    const dir = f("extend.direction");
    expect(optionBlocked(dir, "before", extend, {})).toBe("Before needs Dev");
    expect(optionBlocked(dir, "after", extend, {})).toBeNull();
    expect(optionBlocked(dir, "before", extend, { "model.variant": "dev" })).toBeNull();
    const model = f("model.variant");
    expect(optionBlocked(model, "distilled", extend, { "extend.direction": "before" })).toBe(
      "Extend before needs Dev",
    );
    expect(optionBlocked(model, "dev", extend, { "extend.direction": "before" })).toBeNull();
  });
  it("says why under the Model row by the values_by note (API 1.40, board LtxFeatures frame 8)", () => {
    const extend = of("ltx-extend");
    const model = extend.find((x) => x.key === "model.variant")!;
    expect(
      blockNotes(model, extend, { "extend.direction": "before", "model.variant": "dev" }),
    ).toEqual(["Dev: the only model that extends before. About 10× slower."]);
    expect(blockNotes(model, extend, {})).toEqual([]);
  });
  it("takes an offer's inputs as sent", () => {
    const inputs = {
      "source.source": { take_id: "t1" },
      "retake.span": { start: 0, end: 2.0416666666666665 },
    };
    expect(buildInputs(retake, rawsFrom(retake, inputs)).inputs).toEqual(inputs);
  });
});
