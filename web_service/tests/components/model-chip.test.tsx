// @vitest-environment jsdom
// The model chip's tooltip with one variant left (alpha.5 item 4): the workflow's own `only`, or only one downloaded.
import { afterEach, describe, expect, it } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { Chips } from "@/components/create/form";
import type { Field } from "@/lib/workflow";

afterEach(cleanup);

const variant = (present?: boolean): Field => ({
  key: "model.variant",
  node: "model",
  nodeTitle: "Model",
  param: { name: "variant", type: "enum", enum: ["base-fp8"], default: "base" } as Field["param"],
  initial: "base",
  canvas: false,
  model: true,
  ...(present && { present }),
});

describe("the model chip with one variant", () => {
  it("names the workflow's own limit, else what is downloaded", () => {
    const props = {
      raws: { "model.variant": "base-fp8" },
      onChange: () => undefined,
      show: () => undefined,
    };
    render(<Chips fields={[variant()]} {...props} />);
    expect(screen.getByTitle("This workflow runs on Base fp8 only")).toBeTruthy();
    cleanup();
    render(<Chips fields={[variant(true)]} {...props} />);
    expect(screen.getByTitle("Only Base fp8 is downloaded")).toBeTruthy();
  });
});
