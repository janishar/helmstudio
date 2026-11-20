// @vitest-environment jsdom
// New shot (components/timeline/shot.tsx; docs/mock/Timeline.dc.html "New shot"): the model chip, the Seeds stepper
// (the first seed fills the slot, the others run in the sequence's session) and no Close (Escape leaves).
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { ShotComposer } from "@/components/timeline/shot";
import type { S } from "@/lib/api";
import type { Norm } from "@/lib/timeline";
import { answer } from "./helpers";

vi.mock("next/link", () => ({
  default: ({ href, children, ...rest }: { href: string; children: React.ReactNode }) => (
    <a href={href} {...rest}>
      {children}
    </a>
  ),
}));

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

const P = (name: string, type: string, more: object = {}) => ({ name, type, ...more });
const schemas = {
  "ltx/prompt": {
    title: "Prompt",
    params: [P("text", "text", { default: "" })],
    outputs: [{ name: "cond", type: "Conditioning" }],
  },
  "ltx/model": {
    title: "LTX-2.5",
    params: [P("variant", "enum", { default: "distilled", enum: ["distilled", "dev"] })],
    outputs: [{ name: "model", type: "Model" }],
  },
  "ltx/sample": {
    title: "Sample",
    params: [
      P("seed", "seed", { default: 42 }),
      P("frames", "frames", { default: 49, min: 9, step: 8, max: 321, fps: "fps" }),
      P("fps", "int", { default: 24 }),
    ],
    outputs: [{ name: "latent", type: "Latent" }],
  },
};
const t2v = {
  id: "ltx-t2v",
  title: "LTX Text to video",
  family: "ltx",
  media: "video",
  source: "template",
  nodes: {
    prompt: { kind: "ltx/prompt" },
    model: { kind: "ltx/model" },
    sample: { kind: "ltx/sample", in: { cond: "prompt.cond", model: "model.model" } },
  },
  node_schemas: schemas,
} as unknown as S<"Workflow">;

const doc: Norm = {
  target: { width: 448, height: 256, fps: 24, sample_rate: 48000 },
  tracks: [{ name: "V1", kind: "video", clips: [] }],
  duration_s: 0,
  frames: 0,
};
const SID = "11111111-0000-4000-8000-000000000002";
const tl = {
  id: "11111111-0000-4000-8000-000000000001",
  name: "Cut",
  session_id: SID,
  revision: 1,
  defaults: { cast: [], looks: [] },
  can_undo: false,
  can_redo: false,
  takes: {},
  created_at: "2026-11-01T00:00:00Z",
  updated_at: "2026-11-01T00:00:00Z",
  ...doc,
} as unknown as S<"Timeline">;

function serve() {
  answer("GET", { "/v1/studio/library": () => ({ data: { items: [] } }) });
  const shots: { run: S<"RunRequest"> }[] = [];
  const runs: { session: string; body: S<"RunRequest"> }[] = [];
  answer("POST", {
    "/v1/studio/timelines/{timeline_id}/shots": (init) => {
      shots.push(init.body as (typeof shots)[number]);
      return { status: 202, data: { timeline: tl, run: {} } };
    },
    "/v1/studio/sessions/{session_id}/runs": (init) => {
      const p = init.params as { path: { session_id: string } };
      runs.push({ session: p.path.session_id, body: init.body as S<"RunRequest"> });
      return { status: 202, data: {} };
    },
  });
  return { shots, runs };
}

function mount() {
  const done = vi.fn();
  const close = vi.fn();
  render(
    <ShotComposer
      tl={tl}
      doc={doc}
      facts={{}}
      slot={{ at: 0 }}
      workflows={[t2v]}
      onDone={done}
      onClose={close}
    />,
  );
  return { done, close };
}

describe("ShotComposer", () => {
  it("names the model on the workflow chip and draws no Close", () => {
    serve();
    mount();
    expect(screen.getByRole("button", { name: "LTX-2.5 · Distilled" })).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Close" })).toBeNull();
    expect(screen.getByRole("group", { name: "Seeds to queue" }).textContent).toContain("1 / 4");
  });

  it("Escape leaves the composer", () => {
    serve();
    const { close } = mount();
    fireEvent.keyDown(screen.getByRole("region", { name: "New shot" }), { key: "Escape" });
    expect(close).toHaveBeenCalled();
  });

  it("queues consecutive seeds: the first fills the slot, the rest run in the session", async () => {
    const { shots, runs } = serve();
    const { done } = mount();
    const more = screen.getByRole("button", { name: "More seeds" });
    fireEvent.click(more);
    fireEvent.click(more);
    fireEvent.click(more);
    expect((more as HTMLButtonElement).disabled).toBe(true);
    fireEvent.click(screen.getByRole("button", { name: "Generate shot" }));
    await waitFor(() => expect(done).toHaveBeenCalled());
    expect(shots.map((s) => s.run.inputs?.["sample.seed"])).toEqual([42]);
    expect(runs.map((r) => [r.session, r.body.inputs?.["sample.seed"]])).toEqual([
      [SID, 43],
      [SID, 44],
      [SID, 45],
    ]);
  });
});
