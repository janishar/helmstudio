// @vitest-environment jsdom
// Magic's variants (API 1.94; owner, 2026-11-27: "yes add them"; MagicBoard 3 and 7): Variant beside Model sets the
// request's Advanced model.variant (the workflow's own drops it), Advanced no longer lists it, and the estimate
// follows it (Turbo with no runs of yours is unknown; the line names it).
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { EstimateLine, RequestForm } from "@/components/magic/request";
import { emptyDoc } from "@/lib/magic-fixture";
import type { MagicRequest } from "@/lib/magic";
import { FAKE_KINDS, type Kinds, type Model } from "@/lib/magic-standard";

vi.mock("@/components/magic/magic.css", () => ({}));
vi.mock("@/lib/library", async (orig) => ({
  ...(await orig<typeof import("@/lib/library")>()),
  useVoices: () => [],
}));
const variant = (id: string, title: string) => ({
  id,
  title,
  precisions: ["int8"],
  dir: id,
  licence: null,
  gated: false,
  download_bytes: null,
  components: [],
  hosts: [],
});
vi.mock("@/lib/api", async (orig) => ({
  ...(await orig<typeof import("@/lib/api")>()),
  api: {
    GET: async () => ({
      response: { ok: true },
      data: {
        hosts: [],
        licences: [],
        models: [
          {
            id: "qwen-image-2.1",
            family: "qwen-image",
            title: "Qwen Image 2.1",
            media: ["image"],
            runtimes: [{ runtime: "torch", env: "torch" }],
            variants: [variant("base", "Base"), variant("turbo", "Turbo")],
            addons: [],
          },
        ],
      },
    }),
  },
}));
afterEach(cleanup);

const qwen: Model = {
  ...FAKE_KINDS.kinds[1].models[0],
  family: "qwen-image",
  advanced: [
    {
      path: "model.variant",
      param: { name: "variant", type: "enum", default: "base", enum: ["base", "turbo"] },
    },
    { path: "sample.steps", param: { name: "steps", type: "int", default: 40 } },
  ],
};
const kinds: Kinds = { kinds: [{ kind: "image", fields: ["prompt", "seed"], models: [qwen] }] };

describe("Variant beside Model", () => {
  it("sets the request's variant, the workflow's own clears it; Advanced leaves it out", async () => {
    const doc = emptyDoc();
    const seen: MagicRequest[] = [];
    const block = { id: "b1", kind: "image" as const, length: 48 };
    const { rerender } = render(
      <RequestForm
        doc={doc}
        block={block}
        kinds={kinds}
        request={{ kind: "image" }}
        onChange={(r) => seen.push(r)}
      />,
    );
    const seg = screen.getByRole("group", { name: "Variant" });
    await waitFor(() => expect(within(seg).getByRole("button", { name: "Turbo" })).toBeTruthy());
    expect(within(seg).getByRole("button", { name: "Base" }).getAttribute("aria-pressed")).toBe(
      "true",
    );
    expect(screen.getByText(/Advanced · 1 more/)).toBeTruthy();
    fireEvent.click(within(seg).getByRole("button", { name: "Turbo" }));
    expect(seen.at(-1)?.advanced).toEqual({ "model.variant": "turbo" });
    rerender(
      <RequestForm
        doc={doc}
        block={block}
        kinds={kinds}
        request={seen.at(-1)!}
        onChange={(r) => seen.push(r)}
      />,
    );
    fireEvent.click(
      within(screen.getByRole("group", { name: "Variant" })).getByRole("button", { name: "Base" }),
    );
    expect(seen.at(-1)?.advanced).toBeUndefined();
  });

  it("the estimate follows it: the pack's rate is Base's, Turbo's unknown until you run it", () => {
    const doc = emptyDoc();
    const b = { kind: "image" as const, length: 48, request: { kind: "image" as const } };
    render(<EstimateLine model={qwen} block={b} doc={doc} variant="Base" />);
    expect(screen.getByText(/· Base$/)).toBeTruthy();
    cleanup();
    render(
      <EstimateLine
        model={qwen}
        block={{ ...b, request: { kind: "image", advanced: { "model.variant": "turbo" } } }}
        doc={doc}
        variant="Turbo"
      />,
    );
    expect(screen.getByText("Time unknown")).toBeTruthy();
    expect(screen.getByText("Time unknown").parentElement?.title).toMatch(
      /No runs of yours at Turbo/,
    );
  });
});
