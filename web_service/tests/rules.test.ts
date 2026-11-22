// API 1.47 (contracts/README.md, Param rules): the expression language and the rules the form shows before Generate,
// against contracts/fixtures/video/rules.json (check.py expr_eval, rule_errors).
import { describe, expect, it } from "vitest";
import fx from "../../contracts/fixtures/video/rules.json";
import h3Nodes from "../../contracts/fixtures/registry/minimax-h3.nodes.valid.json";
import h3Workflows from "../../contracts/fixtures/registry/minimax-h3.workflows.valid.json";
import type { S } from "@/lib/api";
import { evaluate, fields, presetHelp, ruleErrors, toRaw, type Raw } from "@/lib/workflow";

const schemas = Object.fromEntries(
  h3Nodes.nodes.map((n) => [n.kind, n]),
) as unknown as S<"Workflow">["node_schemas"];
const workflow = (id: string) =>
  ({
    ...h3Workflows.workflows.find((w) => w.id === id)!,
    node_schemas: schemas,
  }) as unknown as S<"Workflow">;

describe("rules.json expressions", () => {
  for (const c of fx.expressions)
    it(c.expr, () => {
      const values = c.values as Record<string, unknown>;
      let got: unknown;
      try {
        got = evaluate(c.expr, (ref) => {
          if (!(ref in values)) throw new Error(`no ${ref}`);
          return values[ref];
        });
      } catch {
        got = "error";
      }
      expect(got).toEqual(c.expect);
    });
});

describe("rules.json cases: every rule the form's values break", () => {
  for (const c of fx.cases)
    it(c.description, () => {
      const w = workflow(c.workflow);
      const all = fields(w);
      const raws: Record<string, Raw> = Object.fromEntries(
        Object.entries(c.inputs).map(([k, v]) => {
          const f = all.find((x) => x.key === k)!;
          return [k, toRaw(v as S<"ParamValue">, f.param)];
        }),
      );
      expect(ruleErrors(w, all, raws).map((e) => e.text)).toEqual(c.errors);
    });
});

describe("preset help and option help (H3Options frames 1, 3, 11)", () => {
  const w = workflow("minimax-h3-text-to-video");
  const all = fields(w);
  it("the Preset heading's (i) follows the variant", () => {
    expect(presetHelp(w.studio, all, {})).toMatch(/^Draft 4 steps;/);
    expect(presetHelp(w.studio, all, { "model.variant": "base-fp8" })).toMatch(
      /^NVIDIA: a preset sets only/,
    );
  });
  it("the LoRA menu's lines", () =>
    expect(all.find((f) => f.key === "generate.lora")?.param.option_help).toEqual({
      none: "the model as it is",
      turbo: "8 steps, near the 50-step look",
    }));
});
