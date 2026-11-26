// @vitest-environment jsdom
// New shot (components/timeline/shot.tsx; docs/mock/Timeline.dc.html "New shot"): the model chip, the Seeds stepper
// (the first seed fills the slot, the others run in the sequence's session) and its × or Escape leaving it.
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

function serve(gcp = "idle") {
  const host = (name: string, kind: string, state: string) => ({
    host: name,
    env: "mlx",
    state,
    updated_at: "2026-11-22T00:00:00Z",
    device: { kind, name: kind === "mps" ? "Apple M5 Pro" : "RTX PRO 6000", memory_total_gb: 96 },
  });
  answer("GET", {
    "/v1/studio/library": () => ({ data: { items: [] } }),
    "/v1/studio/hosts": () => ({
      data: { hosts: [host("mac", "mps", "idle"), host("gcp", "cuda", gcp)] },
    }),
  });
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
  it("names the model on the workflow chip", () => {
    serve();
    mount();
    // the fixture's model node has only a variant: no precision to name
    expect(screen.getByRole("button", { name: "Distilled" })).toBeTruthy();
    expect(screen.getByRole("group", { name: "Seeds to queue" }).textContent).toContain("1 / 4");
  });

  it("Escape leaves the composer", () => {
    serve();
    const { close } = mount();
    fireEvent.keyDown(screen.getByRole("region", { name: "New shot" }), { key: "Escape" });
    expect(close).toHaveBeenCalled();
  });

  it('its × leaves the composer (owner: "new shot is missing close button")', () => {
    serve();
    const { close } = mount();
    fireEvent.click(screen.getByRole("button", { name: "Close New shot" }));
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

  it("Run on after the workflow chip: a pick sends the host with every seed; offline queues nothing", async () => {
    const { shots, runs } = serve();
    const { done } = mount();
    const chip = screen.getByRole("button", { name: "Run on Auto" });
    expect(chip.previousElementSibling?.id).toBe("shot-workflow");
    const pick = () => document.querySelectorAll<HTMLElement>("#shot-run-on [role=menuitemradio]");
    await waitFor(() => expect(pick()).toHaveLength(3));
    fireEvent.click(pick()[2]);
    expect(screen.getByRole("button", { name: "Run on NVIDIA" })).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "More seeds" }));
    fireEvent.click(screen.getByRole("button", { name: /^Generate shot/ }));
    await waitFor(() => expect(done).toHaveBeenCalled());
    expect([shots[0].run.host, runs[0].body.host]).toEqual(["gcp", "gcp"]);
  });

  it("Run on a host that is offline: greyed in the menu", async () => {
    serve("offline");
    mount();
    const pick = () => document.querySelectorAll<HTMLElement>("#shot-run-on [role=menuitemradio]");
    await waitFor(() => expect(pick()[2]?.getAttribute("aria-disabled")).toBe("true"));
    expect(pick()[2].textContent).toBe("NVIDIAgcp · RTX PRO 6000offline");
  });
});

// API 1.37 (docs/mock/H3Options.dc.html frame 9): MiniMax H3 Image to video's frames are two image params
// (Workflow.keyframes {first, last}): Start from / End on put the frame takes into them
describe("ShotComposer: keyframes as image params", () => {
  it("binds the previous clip's last frame to First frame and the next one's first to Last frame", async () => {
    const A = "aaaaaaaa-0000-4000-8000-000000000001";
    const C = "cccccccc-0000-4000-8000-000000000001";
    const h3 = {
      id: "minimax-h3-image-to-video",
      title: "MiniMax H3 Image to video",
      family: "minimax-h3",
      media: "video",
      source: "template",
      keyframes: { first: "first.image", last: "last.image" },
      nodes: {
        prompt: { kind: "prompt" },
        first: { kind: "image" },
        last: { kind: "image", optional: true },
      },
      node_schemas: {
        prompt: { title: "Prompt", params: [P("text", "text", { default: "" })], outputs: [] },
        image: { title: "Image", params: [P("image", "image", { default: null })], outputs: [] },
      },
    } as unknown as S<"Workflow">;
    const clip = (id: string, take: string | null, at: number) => ({
      id,
      take_id: take,
      in: 0,
      out: 2,
      at,
      gain_db: 0,
      mute: false,
      alternatives: take ? [take] : [],
    });
    const three: Norm = {
      ...doc,
      tracks: [
        {
          name: "V1",
          kind: "video",
          clips: [clip("a", A, 0), clip("b", null, 2), clip("c", C, 4)],
        },
      ],
    } as unknown as Norm;
    const { shots } = serve();
    const frames: Record<string, string> = { [`${A}:last`]: "f-last", [`${C}:first`]: "f-first" };
    answer("POST", {
      "/v1/studio/takes/{take_id}/frame": (init) => {
        const p = init.params as { path: { take_id: string } };
        const at = (init.body as { at: string }).at;
        return { status: 200, data: { take: { id: frames[`${p.path.take_id}:${at}`] } } };
      },
      "/v1/studio/timelines/{timeline_id}/shots": (init) => {
        shots.push(init.body as (typeof shots)[number]);
        return { status: 202, data: { timeline: tl, run: {} } };
      },
    });
    const done = vi.fn();
    const vt = (title: string) => ({ kind: "video", title, video: { fps: 24, frames: 48 } });
    const withTakes = {
      ...tl,
      takes: { [A]: vt("Shot 2"), [C]: vt("Shot 3") },
    } as unknown as S<"Timeline">;
    render(
      <ShotComposer
        tl={withTakes}
        doc={three}
        facts={{}}
        slot={{ clip: "b" }}
        workflows={[h3]}
        onDone={done}
        onClose={vi.fn()}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: "Start from last frame of Shot 2" }));
    fireEvent.click(screen.getByRole("button", { name: "End on first frame of Shot 3" }));
    fireEvent.click(screen.getByRole("button", { name: /^Generate shot/ }));
    await waitFor(() => expect(done).toHaveBeenCalled());
    expect(shots[0].run.inputs?.["first.image"]).toEqual({ take_id: "f-last" });
    expect(shots[0].run.inputs?.["last.image"]).toEqual({ take_id: "f-first" });
  });
});
