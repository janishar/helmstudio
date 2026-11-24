// Create's useIn (contracts/tools/actions.json `create.useIn`): a still goes into a video workflow's First frame the
// way the strip's "Use in…" does (GET …/uses, the offer applied in place), with Undo; frameIn on an image names useIn.
import { afterEach, describe, expect, it, vi } from "vitest";
import actions from "../../../contracts/tools/actions.json";
import { createTools, type CreateCtx } from "@/components/create/assistant";
import { type S } from "@/lib/api";
import { answer, take } from "./helpers";

afterEach(() => vi.restoreAllMocks());

const still = take({ title: "Party in the park" });
const animate: S<"TakeOffer"> = {
  workflow_id: "ltx-image-to-video",
  title: "LTX-2.5 Image to video",
  action: "Animate",
  family: "ltx",
  media: "video",
  param: "keyframes.images",
  inputs: { "keyframes.images": [{ image: { take_id: still.id } }] },
};
const edit: S<"TakeOffer"> = {
  workflow_id: "qwen-image",
  title: "Qwen Image",
  action: "Edit",
  family: "qwen-image",
  media: "image",
  param: "encode.refs",
  inputs: { "encode.refs": [{ image: { take_id: still.id } }] },
};

function form() {
  const s = { workflow: "qwen-image", raws: { "encode.refs": "old" } as Record<string, unknown> };
  const c = {
    workflow: { id: "qwen-image", family: "qwen-image" },
    takes: [still],
    media: "image",
    plan: { host: null },
    machines: [],
    picks: [],
    handles: [],
    held: { ...s.raws },
    applyOffer: (o: S<"TakeOffer">) => {
      s.workflow = o.workflow_id;
      Object.assign(s.raws, o.inputs);
    },
    restore: (k: string, v: unknown) => {
      if (v === undefined) delete s.raws[k];
      else s.raws[k] = v;
    },
    switchTo: (id: string) => void (s.workflow = id),
  } as unknown as CreateCtx;
  answer("GET", { "/v1/studio/takes/{take_id}/uses": () => ({ data: { uses: [edit, animate] } }) });
  return { s, a: createTools(c).actions };
}

describe("useIn", () => {
  it("puts an image take in LTX's First frame; Undo brings the Qwen form back", async () => {
    const { s, a } = form();
    const o = await a.useIn({ take_id: still.id, workflow: "ltx" }, { progress: () => undefined });
    expect(o).toMatchObject({
      status: "done",
      text: "Put Party in the park in Animate · LTX-2.5 Image to video",
    });
    expect(s.workflow).toBe("ltx-image-to-video");
    expect(s.raws["keyframes.images"]).toEqual([{ image: { take_id: still.id } }]);
    (o as { undo: { undo: () => void } }).undo.undo();
    expect(s.workflow).toBe("qwen-image");
    expect(s.raws["keyframes.images"]).toBeUndefined();
    expect(s.raws["encode.refs"]).toBe("old");
  });

  it("names the workflows a take goes in when none fits", async () => {
    const { a } = form();
    expect(await a.useIn({ take_id: still.id, workflow: "wan" }, { progress: () => undefined })).toMatchObject({
      status: "failed",
      text: "Use in · Party in the park goes in Qwen Image, LTX-2.5 Image to video",
    });
  });

  it("frameIn on an image take points to useIn", async () => {
    const { a } = form();
    expect(await a.frameIn({ take_id: still.id, at: "first" }, { progress: () => undefined })).toMatchObject({
      status: "failed",
      text: "Frame in · Party in the park is not a video: put it in with useIn",
    });
  });

  it("is in actions.json with Undo, and frameIn's description points to it", () => {
    const c = actions.screens.create.actions;
    expect(c.useIn.effect).toBe("undo");
    expect(c.useIn.input.required).toEqual(["take_id"]);
    expect(c.frameIn.description).toMatch(/useIn/);
  });
});
