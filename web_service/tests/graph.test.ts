import { readdirSync, readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import ELK from "elkjs/lib/elk.bundled.js";
import paletteFixture from "../../contracts/fixtures/graphs/palette.json";
import type { S } from "@/lib/api";
import {
  autowire,
  canConnect,
  graphProblems,
  mergeProblems,
  newId,
  paletteOf,
  passthrough,
  wireDrop,
  type Doc,
} from "@/lib/graph";
import {
  addFromDrop,
  addNode,
  connect,
  elkInput,
  elkPositions,
  history,
  historyOf,
  removeNodes,
  setBypass,
} from "@/lib/graph-edit";
import { deriveStudio, hideFromStudio, placement, showInStudio, studioView } from "@/lib/studio";
import { fields } from "@/lib/workflow";

const DIR = new URL("../../contracts/fixtures/graphs/", import.meta.url);
const palette = paletteOf(paletteFixture as S<"NodePalette">);
const schemas = Object.fromEntries(Object.entries(palette).map(([k, v]) => [k, v]));
const load = (name: string) => JSON.parse(readFileSync(new URL(name, DIR), "utf8"));
const files = readdirSync(DIR).filter((f) => f !== "palette.json");
const qwen = load("valid.qwen-image.json").workflow as Doc;
const echo = load("wiredrop.1.json").workflow as Doc;

describe("graph rules against contracts/fixtures/graphs", () => {
  for (const f of files.filter((x) => /^(valid|invalid)\./.test(x)))
    it(f, () => {
      const fx = load(f);
      const got = graphProblems(fx.workflow, palette).map((p) => ({
        code: p.code,
        ...(p.node !== undefined && { node: p.node }),
        ...(p.port !== undefined && { port: p.port }),
        ...(p.param !== undefined && { param: p.param }),
      }));
      expect(new Set(got.map((p) => JSON.stringify(p)))).toEqual(
        new Set(
          fx.problems.map((p: Record<string, string>) =>
            JSON.stringify({
              code: p.code,
              ...(p.node !== undefined && { node: p.node }),
              ...(p.port !== undefined && { port: p.port }),
              ...(p.param !== undefined && { param: p.param }),
            }),
          ),
        ),
      );
      expect(got.length).toBe(fx.problems.length); // each at most once
    });

  for (const f of files.filter((x) => x.startsWith("autowire.")))
    it(f, () => {
      const fx = load(f);
      expect(autowire(fx.workflow, palette, fx.selected, fx.add.id, fx.add.kind)).toEqual(
        fx.expect_in,
      );
    });

  for (const f of files.filter((x) => x.startsWith("wiredrop.")))
    it(f, () => {
      const fx = load(f);
      expect(wireDrop(fx.workflow, palette, fx.from)).toEqual(fx.expect);
      if (fx.pick) {
        const { doc, id } = addFromDrop(fx.workflow, palette, fx.from, fx.pick.kind);
        expect(id).toBe(fx.pick.id);
        expect(doc.nodes[id].in ?? {}).toEqual(fx.expect_in);
        expect(graphProblems(doc, palette).filter((p) => p.code === "cycle")).toEqual([]);
      }
    });

  it("marks what blocks saving and sorts whole-workflow problems first", () => {
    const doc: Doc = { ...echo, nodes: { prompt: { kind: "prompt", params: { text: 5 } } } };
    const got = graphProblems(doc, palette);
    expect(got.map((p) => [p.code, p.blocks])).toEqual([
      ["no_output", "run"],
      ["param_invalid", "save"],
    ]);
  });
});

describe("type compatibility", () => {
  it("joins equal types, values into value params, handles within one family, never a cycle", () => {
    const d = qwen;
    expect(canConnect(d, palette, "prompt", "text", "encode", "prompt")).toBe(true);
    expect(canConnect(d, palette, "prompt", "text", "sample", "cond")).toBe(false);
    // a Seed output into a seed param would be Seed→Seed: the seed node's own param is a wireable value
    expect(canConnect(d, palette, "seed", "seed", "sample", "seed")).toBe(true);
    // a Model of qwen-image into test/lora (Model of test) is another family
    const mixed: Doc = { ...d, nodes: { ...d.nodes, lora: { kind: "test/lora" } } };
    expect(canConnect(mixed, palette, "model", "model", "lora", "model")).toBe(false);
    // decode feeds output: output back into decode's ancestors would loop
    expect(canConnect(d, palette, "decode", "image", "decode", "latent")).toBe(false);
    const t: Doc = {
      title: "t",
      family: "test",
      env: "fake",
      nodes: { a: { kind: "test/enhance" }, b: { kind: "test/enhance", in: { prompt: "a.text" } } },
    };
    expect(canConnect(t, palette, "b", "text", "a", "prompt")).toBe(false);
    expect(canConnect(t, palette, "a", "text", "b", "prompt")).toBe(true);
  });

  it("bypass passes each output's first same-type input", () => {
    expect(passthrough(palette["test/enhance"])).toEqual(["prompt"]);
    expect(passthrough(palette["prompt"])).toBeNull();
    expect(passthrough(palette["output"])).toBeNull();
  });
});

describe("edits", () => {
  it("names new nodes from the kind, then _2", () => {
    expect(newId(qwen, "qwen-image/sample")).toBe("sample_2");
    expect(newId(qwen, "test/upscale")).toBe("upscale");
  });

  it("adds an auto-wired node and wires a dropped one", () => {
    const { doc, id } = addNode(removeNodes(qwen, ["sample"]), palette, "qwen-image/sample", null, {
      x: 10.4,
      y: 3,
    });
    expect(id).toBe("sample");
    expect(doc.nodes.sample).toEqual({
      kind: "qwen-image/sample",
      in: { model: "model.model", cond: "encode.cond", canvas: "canvas.canvas", seed: "seed.seed" },
      position: { x: 10, y: 3 },
    });
    const drop = addFromDrop(
      echo,
      palette,
      { node: "prompt", port: "text", side: "output" },
      "echo/echo",
    );
    expect(drop.id).toBe("echo_2");
    expect(drop.doc.nodes.echo_2.in).toEqual({ text: "prompt.text" });
    const back = addFromDrop(
      echo,
      palette,
      { node: "output", port: "media", side: "input" },
      "echo/echo",
    );
    expect(back.doc.nodes.output.in).toEqual({ media: "echo_2.image" });
    expect(back.doc.nodes.echo_2.in).toEqual({ text: "prompt.text" });
  });

  it("removing a node drops its wires, Studio paths and preset keys; wiring a param drops its path", () => {
    const d = load("valid.qwen-image.json").workflow as Doc;
    const gone = removeNodes(d, ["sample"]);
    expect(gone.nodes.decode.in).toEqual({ model: "model.model" });
    const all = JSON.stringify(gone.studio);
    expect(all).not.toContain("sample.");
    const t: Doc = {
      title: "t",
      family: "test",
      env: "fake",
      nodes: { c: { kind: "test/count" }, m: { kind: "test/mix" } },
      studio: {
        inspector: [{ title: "Mix", items: ["m.strength"] }],
        labels: { "m.strength": "S" },
      },
    };
    const w = connect(t, "m", "strength", "c.value");
    expect(w.studio).toEqual({ inspector: [{ title: "Mix", items: [] }], labels: {} });
    expect(setBypass(t, "m", true).nodes.m.bypass).toBe(true);
    expect("bypass" in setBypass(setBypass(t, "m", true), "m", false).nodes.m).toBe(false);
  });

  it("undo and redo step over edits", () => {
    const a = qwen;
    const b = removeNodes(a, ["seed"]);
    const c = removeNodes(b, ["canvas"]);
    let h = history(history(historyOf(a), { type: "edit", doc: b }), { type: "edit", doc: c });
    h = history(h, { type: "undo" });
    expect(h.present).toBe(b);
    h = history(h, { type: "undo" });
    expect(h.present).toBe(a);
    expect(history(h, { type: "undo" })).toBe(h);
    h = history(h, { type: "redo" });
    expect(h.present).toBe(b);
    h = history(h, { type: "edit", doc: c });
    expect(h.future).toEqual([]);
    expect(history(h, { type: "edit", doc: c })).toBe(h);
  });
});

describe("the Studio block", () => {
  const echoWf = { ...echo, node_schemas: schemas } as unknown as S<"Workflow">;

  it("derives the form Create draws", () => {
    expect(deriveStudio(echo, schemas)).toEqual({
      composer: ["prompt.text"],
      inspector: [
        { title: "References", items: ["echo.refs"] },
        { title: "Quality", items: ["echo.steps", "echo.delay_ms"] },
        { title: "Options", items: ["echo.fail"] },
      ],
    });
  });

  it("Show in Studio writes the derived block first, then appends to the node's section", () => {
    const hidden = hideFromStudio(echo, schemas, "echo.steps");
    expect(placement(hidden.studio!, "echo.steps")).toBeNull();
    const shown = showInStudio(hidden, schemas, "echo.steps", "Echo");
    expect(placement(shown.studio!, "echo.steps")).toBe("Inspector › Echo");
    expect(placement(shown.studio!, "prompt.text")).toBe("Composer");
    expect(showInStudio(shown, schemas, "echo.steps", "Echo").studio).toEqual(shown.studio);
  });

  it("shows exactly the placed paths with labels, and a bypass switch", () => {
    const t = {
      title: "t",
      family: "test",
      env: "fake",
      nodes: { p: { kind: "prompt" }, e: { kind: "test/enhance", in: { prompt: "p.text" } } },
      node_schemas: schemas,
      studio: {
        composer: ["p.text"],
        quick: ["e.bypass"],
        inspector: [{ title: "Enhance", items: ["e.think"] }],
        labels: { "e.think": "Think hard" },
      },
    } as unknown as S<"Workflow">;
    const v = studioView(t, fields(t))!;
    expect(v.text.map((f) => f.key)).toEqual(["p.text"]);
    expect(v.groups.map((g) => [g.title, g.fields.map((f) => f.key)])).toEqual([
      ["Quick", ["e.bypass"]],
      ["Enhance", ["e.think"]],
    ]);
    expect(v.groups[1].fields[0].param.label).toBe("Think hard");
    expect(studioView(echoWf, fields(echoWf))).toBeNull();
  });

  it("leaves a wired param out of the form", () => {
    const t = {
      nodes: { c: { kind: "test/count" }, m: { kind: "test/mix", in: { strength: "c.value" } } },
      node_schemas: schemas,
    } as unknown as S<"Workflow">;
    expect(fields(t).map((f) => f.key)).toEqual(["c.value", "m.blend"]);
  });

  it("names the node of a field when two nodes share a kind", () => {
    const t = {
      nodes: { c: { kind: "test/count" }, c_2: { kind: "test/count" }, p: { kind: "prompt" } },
      node_schemas: schemas,
    } as unknown as S<"Workflow">;
    const label = (key: string) => fields(t).find((f) => f.key === key)!.param;
    expect([label("c.value"), label("c_2.value")].map((p) => p.label ?? p.name)).toEqual([
      `${schemas["test/count"].params[0].label ?? "value"} (c)`,
      `${schemas["test/count"].params[0].label ?? "value"} (c_2)`,
    ]);
    expect(label("p.text")).toBe(schemas.prompt.params[0]); // one prompt: unchanged
  });
});

describe("layout", () => {
  it("feeds elkjs the cards and wires and lays them out left to right", async () => {
    const input = elkInput(qwen, schemas);
    expect(input.children.map((c) => c.id)).toEqual(Object.keys(qwen.nodes));
    expect(input.edges).toContainEqual({
      id: "sample.cond",
      sources: ["encode"],
      targets: ["sample"],
    });
    const pos = elkPositions(await new ELK().layout(input));
    expect(Object.keys(pos).sort()).toEqual(Object.keys(qwen.nodes).sort());
    for (const e of input.edges) expect(pos[e.sources[0]].x).toBeLessThan(pos[e.targets[0]].x);
  });
});

describe("problems", () => {
  it("merges server and client problems once", () => {
    const a = graphProblems({ ...echo, nodes: {} as Doc["nodes"] }, palette);
    expect(mergeProblems(a, a)).toEqual(a);
  });
});
