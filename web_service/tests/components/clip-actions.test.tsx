// @vitest-environment jsdom
// Take actions on a V1 clip (components/timeline/actions.tsx; docs/mock/TakeActions.dc.html): the offers of GET
// …/clips/{clip}/actions for the range, each a form whose Generate posts a shot (Replace, Insert after / before).
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { ClipActions, actionShot } from "@/components/timeline/actions";
import { estimateLength, extendMap, floorGrid, retakeMap } from "@/lib/source";
import type { S } from "@/lib/api";
import type { Clip, Norm } from "@/lib/timeline";
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

const T = "00000000-0000-4000-8000-0000000000a1";
const P = (name: string, type: string, more: object = {}) => ({ name, type, ...more });
const GRID = { min: 9, step: 8, max: 321 };
// LTX nodes, as runner_service/packs/ltx/model.yaml declares them (the params these forms read)
const schemas = {
  "ltx/prompt": {
    title: "Prompt",
    family: "ltx",
    params: [P("text", "text", { label: "Prompt", default: "" })],
    outputs: [{ name: "cond", type: "Conditioning" }],
  },
  "ltx/encode-video": {
    title: "Source video",
    family: "ltx",
    params: [P("source", "media", { label: "Source video", default: null, accepts: ["video"] })],
    outputs: [{ name: "latent", type: "Latent" }],
  },
  "ltx/encode-audio": {
    title: "Source audio",
    family: "ltx",
    params: [
      P("source", "media", {
        label: "Audio",
        default: null,
        accepts: ["audio"],
        trim: true,
        max_s: 13.375,
      }),
      P("fps", "int", { default: 24, values: [24] }),
    ],
    outputs: [{ name: "latent", type: "Latent" }],
  },
  "ltx/retake": {
    title: "Retake",
    family: "ltx",
    params: [
      P("span", "span", { label: "Retake", default: null }),
      P("audio", "bool", { label: "Regenerate audio", default: true }),
    ],
    outputs: [{ name: "latent", type: "Latent" }],
  },
  "ltx/extend": {
    title: "Extend",
    family: "ltx",
    params: [
      P("seconds", "float", { label: "Add", default: 2, min: 0.1, max: 10, step: 0.1 }),
      P("direction", "enum", { label: "Where", default: "after", enum: ["after", "before"] }),
    ],
    outputs: [{ name: "latent", type: "Latent" }],
  },
  "ltx/sample": {
    title: "Sample",
    family: "ltx",
    params: [
      P("frames", "frames", { default: 49, ...GRID, fps: "fps" }),
      P("fps", "int", { default: 24 }),
    ],
    outputs: [{ name: "latent", type: "Latent" }],
  },
};
const wf = (id: string, title: string, nodes: Record<string, object>) =>
  ({
    id,
    title,
    media: "video",
    source: "template",
    nodes,
    node_schemas: schemas,
  }) as unknown as S<"Workflow">;
const retakeWf = wf("ltx-retake", "LTX Retake", {
  prompt: { kind: "ltx/prompt" },
  source: { kind: "ltx/encode-video" },
  retake: { kind: "ltx/retake", in: { latent: "source.latent", cond: "prompt.cond" } },
});
const extendWf = wf("ltx-extend", "LTX Extend", {
  prompt: { kind: "ltx/prompt" },
  source: { kind: "ltx/encode-video" },
  extend: { kind: "ltx/extend", in: { latent: "source.latent", cond: "prompt.cond" } },
});
const a2vWf = wf("ltx-a2v", "LTX Audio to video", {
  prompt: { kind: "ltx/prompt" },
  source: { kind: "ltx/encode-audio" },
});

// a 97-frame (4.04 s) clip at 2 s on V1, after a 2 s clip
const video = { duration_s: 97 / 24, fps: 24, frames: 97, has_audio: true, poster_url: "/p.png" };
const clip: Clip = { id: "c2", take_id: T, in: 0, out: 97 / 24, at: 2 };
const doc: Norm = {
  target: { width: 448, height: 256, fps: 24, sample_rate: 48000 },
  tracks: [{ name: "V1", kind: "video", clips: [{ id: "c1", take_id: null, at: 0 }, clip] }],
  duration_s: 2 + 97 / 24,
  frames: 48 + 97,
};
const tl = {
  id: "11111111-0000-4000-8000-000000000001",
  name: "Cut",
  session_id: "11111111-0000-4000-8000-000000000002",
  revision: 3,
  defaults: { cast: [], looks: [] },
  can_undo: false,
  can_redo: false,
  takes: {
    [T]: {
      id: T,
      kind: "video",
      source: "create",
      title: "Fox run",
      url: "/v.mp4",
      thumb_url: "/p.png",
      width: 448,
      height: 256,
      video,
      seed: 7,
      workflow_id: null,
      workflow_title: null,
    },
  },
  created_at: "2026-11-01T00:00:00Z",
  updated_at: "2026-11-01T00:00:00Z",
  ...doc,
} as unknown as S<"Timeline">;

const retake: S<"TakeOffer"> = {
  workflow_id: "ltx-retake",
  title: "LTX Retake",
  action: "Retake a section",
  family: "ltx",
  media: "video",
  param: "source.source",
  inputs: { "source.source": { take_id: T }, "retake.span": { start: 1, end: 2 } },
};
const extend = (direction: "after" | "before"): S<"TakeOffer"> => ({
  workflow_id: "ltx-extend",
  title: "LTX Extend",
  action: "Extend",
  family: "ltx",
  media: "video",
  param: "source.source",
  inputs: { "source.source": { take_id: T }, "extend.direction": direction, "extend.seconds": 2 },
});
const a2v: S<"TakeOffer"> = {
  workflow_id: "ltx-a2v",
  title: "LTX Audio to video",
  action: "Audio to video",
  family: "ltx",
  media: "video",
  param: "source.source",
  inputs: { "source.source": { take_id: T, in: 0, out: 3 } },
};

function serve(actions: S<"TakeOffer">[]) {
  const asked: unknown[] = [];
  answer("GET", {
    "/v1/studio/timelines/{timeline_id}/clips/{clip_id}/actions": (init) => {
      asked.push((init.params as { query: unknown }).query);
      return { data: { actions } };
    },
    "/v1/studio/nodes": () => ({
      data: {
        groups: [
          {
            title: "LTX",
            nodes: [{ kind: "ltx/sample", family: "ltx", params: schemas["ltx/sample"].params }],
          },
        ],
      },
    }),
    "/v1/studio/library": () => ({ data: { items: [] } }),
  });
  const posted: { slot: S<"TimelineSlot">; run: S<"RunRequest"> }[] = [];
  answer("POST", {
    "/v1/studio/timelines/{timeline_id}/shots": (init) => {
      posted.push(init.body as (typeof posted)[number]);
      return { data: { timeline: tl } };
    },
  });
  return { asked, posted };
}

function mount(
  range: [number, number] | null,
  disabled = false,
  workflows = [retakeWf, extendWf, a2vWf],
  c: Clip = clip,
) {
  const done = vi.fn();
  render(
    <ClipActions
      tl={tl}
      doc={doc}
      clip={c}
      range={range}
      workflows={workflows}
      disabled={disabled}
      onDone={done}
    />,
  );
  return done;
}

describe("ClipActions", () => {
  it("asks for the range's offers and lists them by action", async () => {
    const { asked } = serve([retake, extend("after")]);
    mount([3, 4]);
    expect(await screen.findByRole("button", { name: "Retake a section" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Extend" })).toBeTruthy();
    expect(asked).toEqual([{ start: 3, end: 4 }]);
  });

  it("lists Extend first, as the board does, whatever order studio answers in", async () => {
    serve([retake, a2v, extend("after")]);
    mount(null);
    await screen.findByRole("button", { name: "Extend" });
    expect(screen.getAllByRole("button").map((b) => b.textContent)).toEqual([
      "Extend",
      "Retake a section",
      "Audio to video",
    ]);
  });

  it("asks without a range for the whole clip; disabled while busy", async () => {
    const { asked } = serve([retake]);
    mount(null, true);
    const b = await screen.findByRole("button", { name: "Retake a section" });
    expect((b as HTMLButtonElement).disabled).toBe(true);
    expect(asked).toEqual([{}]);
  });

  it("Retake: reads what the span regenerates and replaces the clip", async () => {
    const { posted } = serve([retake]);
    const done = mount([3, 4]);
    fireEvent.click(await screen.findByRole("button", { name: "Retake a section" }));
    const m = retakeMap(1, 2, 24, 97, GRID);
    if ("error" in m) throw new Error(m.error);
    const text = `Regenerates frames ${Math.round(m.map.new_in_s * 24)} – ${Math.round(m.map.new_out_s * 24) - 1} · ${m.map.new_in_s.toFixed(2)} – ${m.map.new_out_s.toFixed(2)} s on the 8-frame grid`;
    expect(await screen.findByText(text)).toBeTruthy();
    expect(screen.getByText("Replace keeps the clip's in and out; nothing moves.")).toBeTruthy();
    expect(screen.getByRole("switch", { name: /Regenerate audio/ })).toBeTruthy();
    fireEvent.change(screen.getByLabelText("Prompt"), { target: { value: "the fox jumps" } });
    fireEvent.click(screen.getByRole("button", { name: "Generate" }));
    await waitFor(() => expect(done).toHaveBeenCalledWith(tl));
    expect(posted).toHaveLength(1);
    expect(posted[0].slot).toEqual({ clip: "c2" });
    expect(posted[0].run.workflow_id).toBe("ltx-retake");
    expect(posted[0].run.inputs).toMatchObject({
      "prompt.text": "the fox jumps",
      "source.source": { take_id: T },
      "retake.span": { start: 1, end: 2 },
    });
  });

  it("Retake inserted after: a new slot after the clip, the span's length", async () => {
    const { posted } = serve([retake]);
    mount(null);
    fireEvent.click(await screen.findByRole("button", { name: "Retake a section" }));
    fireEvent.click(screen.getByRole("button", { name: "Insert after" }));
    expect(
      screen.getByText("Insert after: a 1.0 s clip after Fox run holds only the new part."),
    ).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Generate" }));
    await waitFor(() => expect(posted).toHaveLength(1));
    expect(posted[0].slot).toMatchObject({ after: "c2", length: 1 });
    expect((posted[0].slot as { id: string }).id).toMatch(/^[a-z0-9]{1,16}$/);
  });

  it("Extend after starts on Insert after; its readout adds whole 8-frame steps", async () => {
    const { posted } = serve([extend("after")]);
    mount(null);
    fireEvent.click(await screen.findByRole("button", { name: "Extend" }));
    expect(screen.getByRole("button", { name: "Insert after" }).getAttribute("aria-pressed")).toBe(
      "true",
    );
    const g = extendMap(2, "after", 24, 97, GRID);
    if ("error" in g) throw new Error(g.error);
    expect(
      await screen.findByText(`2.0 s · + ${g.frames - floorGrid(97, GRID)} frames · after`),
    ).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Generate" }));
    await waitFor(() => expect(posted).toHaveLength(1));
    const length = estimateLength({ "extend.seconds": 2 }, 24, GRID);
    expect(posted[0].slot).toMatchObject({ after: "c2", length });
  });

  it("Extend inserted after: the new slot's placeholder names the source shot (Q25)", async () => {
    const { posted } = serve([extend("after")]);
    mount(null, false, undefined, { ...clip, number: 3 });
    fireEvent.click(await screen.findByRole("button", { name: "Extend" }));
    fireEvent.click(screen.getByRole("button", { name: "Generate" }));
    await waitFor(() => expect(posted).toHaveLength(1));
    const id = (posted[0].slot as { id: string }).id;
    const placeholder: Clip = { id, take_id: null, shot: { status: "queued" } as Clip["shot"] };
    expect(actionShot(placeholder)?.title).toBe("Extending Shot 3");
  });

  it("Extend before: Replace by default, Insert before puts the slot at the clip's start", async () => {
    const { posted } = serve([extend("before")]);
    mount(null);
    fireEvent.click(await screen.findByRole("button", { name: "Extend" }));
    expect(screen.getByRole("button", { name: "Replace" }).getAttribute("aria-pressed")).toBe(
      "true",
    );
    // both effects, the chosen one first (docs/mock/TakeActions)
    expect(
      screen.getByText(
        "Replace: Fox run starts at the new part and keeps its end. Insert before: a 2.0 s clip before Fox run.",
      ),
    ).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Insert before" }));
    expect(
      screen.getByText(
        "Insert before: a 2.0 s clip before Fox run. Replace: Fox run starts at the new part and keeps its end.",
      ),
    ).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Generate" }));
    await waitFor(() => expect(posted).toHaveLength(1));
    expect(posted[0].slot).toMatchObject({ at: 2, length: 2 });
    expect("after" in posted[0].slot).toBe(false);
  });

  it("Audio to video: the part's clip on the grid", async () => {
    serve([a2v]);
    mount(null);
    fireEvent.click(await screen.findByRole("button", { name: "Audio to video" }));
    // 3 s at 24 fps = 72 frames, floored to 8k + 1 = 65 frames
    expect(await screen.findByText(/^0\.00 – 3\.00 s → 65 frames · 2\.71 s$/)).toBeTruthy();
  });

  it("says when the offer's workflow is gone", async () => {
    serve([retake]);
    mount(null, false, []);
    fireEvent.click(await screen.findByRole("button", { name: "Retake a section" }));
    expect(screen.getByRole("alert").textContent).toBe(
      "The workflow ltx-retake is not available any more.",
    );
  });

  it("shows studio's refusal and keeps the form", async () => {
    serve([retake]);
    answer("POST", {
      "/v1/studio/timelines/{timeline_id}/shots": () => ({
        status: 409,
        error: { detail: "the timeline changed" },
      }),
    });
    const done = mount(null);
    fireEvent.click(await screen.findByRole("button", { name: "Retake a section" }));
    fireEvent.click(screen.getByRole("button", { name: "Generate" }));
    expect((await screen.findByRole("alert")).textContent).toBe("the timeline changed");
    expect(done).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: "Generate" })).toBeTruthy();
  });
});
