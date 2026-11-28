import { readdirSync, readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import scanFixture from "../../contracts/fixtures/mentions/scan.json";
import fitFixture from "../../contracts/fixtures/mentions/fit.json";
import mediaFixture from "../../contracts/fixtures/voices/media.json";
import palette from "../../contracts/fixtures/graphs/palette.json";
import type { S } from "@/lib/api";
import { paletteOf } from "@/lib/graph";
import {
  fieldErrors,
  handleFrom,
  HANDLE,
  insertMention,
  lookFit,
  looksNote,
  mentionAt,
  mentionNote,
  mentionRanges,
  referenceRows,
  resolveMentions,
  autoSize,
  scan,
  withInputs,
  workflowMedia,
  workflowSound,
  type Resolvable,
} from "@/lib/mentions";

const schemas = paletteOf(palette as unknown as S<"NodePalette">);
const dir = new URL("../../contracts/fixtures/mentions/", import.meta.url);

describe("scan (fixtures/mentions/scan.json)", () => {
  it.each(scanFixture.cases)("$text", ({ text, parts }) => {
    const got = scan(text);
    expect(got).toEqual(parts);
    expect(got.map((p) => ("text" in p ? p.text : p.raw)).join("")).toBe(text);
  });
});

describe("lookFit (fixtures/mentions/fit.json)", () => {
  const fit = fitFixture as unknown as {
    workflows: Record<string, { nodes: never }>;
    cases: {
      description: string;
      workflow: string;
      item: never;
      loras?: never;
      expect: string | null;
    }[];
  };
  it.each(fit.cases)("$description", (c) => {
    expect(lookFit(fit.workflows[c.workflow].nodes, schemas, c.item, c.loras ?? {})).toBe(c.expect);
  });
});

describe("autoSize", () => {
  it("rounds as the runner (Python round: half to even)", () => {
    expect(autoSize(1920, 1080)).toEqual([1376, 768]);
    expect(autoSize(64, 32)).toEqual([1440, 736]);
    expect(autoSize(4225, 4096)).toEqual([1024, 1024]); // 1040 / 32 = 32.5: to even
  });
});

describe("resolveMentions (fixtures/mentions/resolve.*.json)", () => {
  const files = readdirSync(dir).filter((f) => f.startsWith("resolve."));
  it("has the fixtures", () => expect(files.length).toBeGreaterThanOrEqual(10));
  it.each(files)("%s", (f) => {
    const fx = JSON.parse(readFileSync(new URL(f, dir), "utf8"));
    const got = resolveMentions(
      fx.workflow.nodes,
      schemas,
      fx.library,
      fx.sizes,
      fx.looks,
      fx.chosen,
      fx.loras,
    );
    if (fx.expect.errors) expect(got.errors).toEqual(fx.expect.errors);
    else
      expect({
        inputs: got.inputs,
        typed: got.typed,
        elements: got.elements,
        looks: got.looks,
        errors: got.errors,
      }).toEqual({
        inputs: {},
        typed: {},
        elements: [],
        looks: [],
        errors: [],
        ...fx.expect,
      });
  });
});

const fox: Resolvable = {
  id: "e1",
  handle: "fox",
  name: "Fox",
  version: 1,
  text: "a red fox",
  images: ["t1", "t2"],
};
const qwen = JSON.parse(readFileSync(new URL("resolve.qwen-basic.json", dir), "utf8")).workflow;

describe("the preview", () => {
  it("merges the form's inputs over the workflow and says what the mentions add", () => {
    const nodes = withInputs(qwen.nodes, { "prompt.text": "@fox at dusk" });
    expect(referenceRows(nodes, schemas)?.node).toBe("encode");
    const r = resolveMentions(nodes, schemas, [fox]);
    expect(r.tokens.fox).toEqual(["<image1>", "<image2>"]);
    expect(mentionNote(r, [fox], false)).toBe("Mentions add @fox: <image1> <image2> + text");
    expect(
      mentionNote(
        resolveMentions(withInputs(qwen.nodes, { "prompt.text": "x" }), schemas, [fox]),
        [fox],
        false,
      ),
    ).toBeNull();
  });
  it("names the elements only for a text-only family", () => {
    const echoless = { prompt: { kind: "prompt", params: { text: "@fox" } } };
    const r = resolveMentions(echoless, schemas, [{ ...fox, text: "" }]);
    expect(mentionNote(r, [{ ...fox, text: "" }], true)).toBe(
      "Text only in this workflow. Mentions add @fox: its name",
    );
  });
});

describe("what the looks add", () => {
  it("lists text, image tokens and LoRAs per look", () => {
    const fx = JSON.parse(readFileSync(new URL("resolve.looks-images-loras.json", dir), "utf8"));
    const r = resolveMentions(
      fx.workflow.nodes,
      schemas,
      [],
      fx.sizes,
      fx.looks,
      fx.chosen,
      fx.loras,
    );
    expect(looksNote(r, fx.looks)).toBe(
      "Looks add Noir: text + <image2> <image1> + LoRA noir-lut · Ink wash: text",
    );
    expect(looksNote(resolveMentions(fx.workflow.nodes, schemas, []), fx.looks)).toBeNull();
  });
});

describe("the @ menu at the caret", () => {
  it("opens right after an @ and follows the query", () => {
    expect(mentionAt("@", 1)).toEqual({ start: 0, end: 1, query: "" });
    expect(mentionAt("a @Fo", 5)).toEqual({ start: 2, end: 5, query: "fo" });
    expect(mentionAt("@fo x", 2)).toEqual({ start: 0, end: 3, query: "f" });
    expect(mentionAt("(@red-f", 7)).toEqual({ start: 1, end: 7, query: "red-f" });
  });
  it("stays closed for escapes, words, digits and after the token", () => {
    expect(mentionAt("@@", 2)).toBeNull();
    expect(mentionAt("@@fo", 4)).toBeNull();
    expect(mentionAt("me@fo", 5)).toBeNull();
    expect(mentionAt("@3", 2)).toBeNull();
    expect(mentionAt("@fox ", 5)).toBeNull();
    expect(mentionAt("abc", 3)).toBeNull();
    expect(mentionAt("@@@f", 4)).toEqual({ start: 2, end: 4, query: "f" });
  });
});

describe("inserting a mention", () => {
  it("replaces the caret's token and adds a space", () => {
    expect(insertMention("a @fo", 2, 5, "fox")).toEqual({ text: "a @fox ", caret: 7 });
    expect(insertMention("@fo, then", 0, 3, "fox")).toEqual({ text: "@fox , then", caret: 5 });
    expect(insertMention("@f in", 0, 2, "fox")).toEqual({ text: "@fox in", caret: 4 });
  });
  it("inserts at the caret, apart from a word or an @", () => {
    expect(insertMention("a fox", 5, 5, "owl")).toEqual({ text: "a fox @owl ", caret: 11 });
    expect(insertMention("", 0, 0, "owl")).toEqual({ text: "@owl ", caret: 5 });
    expect(insertMention("x@", 2, 2, "owl").text).toBe("x@ @owl ");
    expect(insertMention("see  now", 4, 4, "owl").text).toBe("see @owl now");
    expect(scan(insertMention("x@", 2, 2, "owl").text)).toContainEqual({
      mention: "owl",
      raw: "@owl",
    });
  });
});

describe("chips and handles", () => {
  it("finds the mention ranges", () => {
    expect(mentionRanges("@fox in @@x @Snow!")).toEqual([
      { start: 0, end: 4, handle: "fox" },
      { start: 12, end: 17, handle: "snow" },
    ]);
  });
  it("proposes a handle from a name", () => {
    expect(handleFrom("Red Fox (cub)")).toBe("red-fox-cub");
    expect(handleFrom("Café Noir")).toBe("cafe-noir");
    expect(handleFrom("3 owls")).toBe("owls");
    expect(handleFrom("!!!")).toBe("");
    for (const n of ["Red Fox (cub)", "a".repeat(60), "x--y__"])
      expect(HANDLE.test(handleFrom(n))).toBe(true);
  });
  it("puts a 422 on its fields", () => {
    const keys = ["prompt.text", "encode.refs"];
    expect(
      fieldErrors("prompt.text: unknown element @wolf; prompt.text: unknown element @owl", keys),
    ).toEqual({
      "prompt.text": "unknown element @wolf; unknown element @owl",
    });
    expect(fieldErrors("encode.refs: at most 10 images; 9 used, @fox +2 do not fit", keys)).toEqual(
      {
        "encode.refs": "at most 10 images; 9 used, @fox +2 do not fit",
      },
    );
    expect(
      fieldErrors("encode.refs[2].seconds: frame 50 is past the clip's last frame 48", keys),
    ).toEqual({ "encode.refs": "row 2: frame 50 is past the clip's last frame 48" });
    expect(fieldErrors("workflow not found", keys)).toBeNull();
  });
});

describe("workflowMedia (fixtures/voices/media.json)", () => {
  it.each(mediaFixture.cases)("$description", (c) => {
    const wf = (
      mediaFixture.workflows as Record<string, { nodes: Parameters<typeof workflowMedia>[0] }>
    )[c.workflow];
    expect(workflowMedia(wf.nodes, schemas)).toBe(c.expect);
    if ("sound" in c) expect(workflowSound(wf.nodes, schemas) ?? null).toBe(c.sound); // API 1.79
  });
});
