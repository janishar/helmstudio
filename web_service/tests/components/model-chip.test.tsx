// @vitest-environment jsdom
// The model chip's tooltip with one variant left (alpha.5 item 4): the workflow's own `only`, or only one downloaded.
import { afterEach, describe, expect, it } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
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

describe("Music's and Sound's chips (docs/mock/CreateMusicSound.dc.html 2a-2e)", () => {
  const lang: Field = {
    key: "music.language",
    node: "music",
    nodeTitle: "Music",
    param: {
      name: "language",
      type: "enum",
      enum: ["auto", "en", "hi"],
      default: "auto",
    } as Field["param"],
    initial: "auto",
    canvas: false,
    model: false,
  };
  it("the model chip names the open model; a missing setting greys in place; Instrumental greys Language", () => {
    const picked: string[] = [];
    render(
      <Chips
        fields={[lang]}
        raws={{}}
        onChange={() => undefined}
        show={() => undefined}
        kindModels={{
          title: "MiniMax Music 3",
          rows: [
            { key: "a", title: "MiniMax Music 3", checked: true, onPick: () => picked.push("a") },
            {
              key: "b",
              title: "ACE-Step 1.5 · 2B",
              sub: "fast",
              checked: false,
              onPick: () => picked.push("b"),
            },
          ],
        }}
        values={[lang]}
        off={{ "music.language": "Off while Instrumental is on" }}
        greyed={[{ label: "BPM · key", why: "Not on MiniMax Music 3" }]}
      />,
    );
    expect(screen.getByRole("button", { name: "MiniMax Music 3" })).toBeTruthy();
    const grey = screen.getByText("BPM · key");
    expect([grey.getAttribute("aria-disabled"), grey.getAttribute("title")]).toEqual([
      "true",
      "Not on MiniMax Music 3",
    ]);
    const l = screen.getByText("Auto");
    expect(l.getAttribute("title")).toBe("Off while Instrumental is on");
  });
  it("Language's popup leads with Auto from the lyrics (2e); a Length on Auto beside a clip reads it (5)", () => {
    const seconds: Field = {
      ...lang,
      key: "foley.seconds",
      node: "foley",
      param: {
        name: "seconds",
        type: "float",
        label: "Length",
        default: 0,
        auto: true,
        min: 1,
        max: 30,
      } as Field["param"],
      initial: 0,
    };
    const withHelp = {
      ...lang,
      param: { ...lang.param, option_help: { auto: "from the lyrics" } },
    };
    render(
      <Chips
        fields={[withHelp, seconds]}
        raws={{ "foley.seconds": "0" }}
        onChange={() => undefined}
        show={() => undefined}
        values={[withHelp, seconds]}
        autoTexts={{ "foley.seconds": { text: "0:05 · video", title: "From the video" } }}
      />,
    );
    expect(screen.getByText("0:05 · video").closest("[title]")?.getAttribute("title")).toBe(
      "From the video",
    );
    fireEvent.click(screen.getAllByText("Auto")[0]);
    expect(screen.getByText("from the lyrics")).toBeTruthy();
  });
});
