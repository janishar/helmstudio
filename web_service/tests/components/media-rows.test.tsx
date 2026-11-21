// @vitest-environment jsdom
// MediaRows (components/create/source.tsx): MiniMax H3's video and audio references in the inspector and the
// composer: add, remove, trim by keyboard, Sound, the total and the errors (docs/mock/CreateMediaRefs.dc.html).
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { MediaRows } from "@/components/create/source";
import { remember } from "@/lib/take-cache";
import type { S } from "@/lib/api";
import type { Raw } from "@/lib/workflow";
import { answer, audio, field, Host, video } from "./helpers";

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

// as runner_service/packs/minimax_h3/model.yaml declares them
const videos = field("generate", {
  name: "videos",
  type: "rows",
  label: "Videos",
  default: [],
  max: 3,
  mention: "<Video {n}>",
  max_total_s: 15,
  item: {
    video: {
      type: "media",
      label: "Video",
      default: null,
      accepts: ["video"],
      trim: true,
      min_s: 2,
      max_s: 15,
    },
    sound: { type: "enum", label: "Sound", default: "keep", enum: ["keep", "silent"] },
  } as unknown as S<"ParamSchema">["item"],
});
const audios = field("generate", {
  name: "audios",
  type: "rows",
  label: "Audios",
  default: [],
  max: 3,
  mention: "<Audio {n}>",
  max_total_s: 15,
  item: {
    audio: {
      type: "media",
      label: "Audio",
      default: null,
      accepts: ["audio"],
      trim: true,
      min_s: 2,
      max_s: 15,
    },
  } as unknown as S<"ParamSchema">["item"],
});

const rowsOf = (...vs: S<"MediaRef">[]) => vs.map((v) => ({ video: v })) as S<"RowItem">[];

function mount(f = videos, initial?: Raw, compact = false) {
  const seen: Raw[] = [];
  render(
    <Host
      initial={initial}
      seen={seen}
      render={(raw, onChange) => (
        <MediaRows f={f} raw={raw} onChange={onChange} compact={compact} />
      )}
    />,
  );
  return seen;
}

describe("MediaRows: the inspector's cards", () => {
  it("shows each take with its mention, facts, part and the total", () => {
    const a = video(8);
    const b = video(4, 30);
    remember([a, b]);
    mount(videos, rowsOf({ take_id: a.id, in: 0, out: 8 }, { take_id: b.id, in: 1, out: 3 }));
    expect(screen.queryByText(/In the prompt/)).toBeNull(); // the (i) help says it
    expect(screen.getByLabelText("2 of 3").textContent).toBe("2 / 3");
    const list = screen.getByRole("list", { name: "Videos, in order" });
    const items = within(list).getAllByRole("listitem");
    expect(items).toHaveLength(2);
    expect(within(items[0]).getByText(a.title)).toBeTruthy();
    expect(within(items[0]).getByText("448 × 256 · 192 f · 8.00 s")).toBeTruthy();
    expect(within(items[1]).getByText("0:01.000 – 0:03.000")).toBeTruthy();
    expect(within(items[1]).getByText("2.0 s · 2 to 15 s")).toBeTruthy();
    // 8 + 2 seconds of 15
    expect(screen.getByText("10.0 s of 15.0 s in all").className).toContain("text-t2");
    expect(screen.getByRole("button", { name: "Add video" })).toBeTruthy();
  });

  it("marks the total and says why when the parts pass max_total_s", () => {
    const a = video(10);
    const b = video(10);
    remember([a, b]);
    mount(videos, rowsOf({ take_id: a.id, in: 0, out: 10 }, { take_id: b.id, in: 0, out: 10 }));
    expect(screen.getByText("20.0 s of 15.0 s in all").className).toContain("text-bad");
    expect(screen.getByText("the takes last 20.0 s in all; at most 15.0 s")).toBeTruthy();
  });

  it("says on the row why a take does not fit (too short), not under the list", () => {
    const short = video(1.5);
    remember([short]);
    mount(videos, rowsOf({ take_id: short.id, in: 0, out: 1.5 }));
    expect(screen.getByRole("alert").textContent).toBe(
      `take ${short.id} lasts 1.5 s; this input takes at least 2.0 s`,
    );
    // the row's own error is not repeated as the field's
    expect(screen.queryByText(/^row 1:/)).toBeNull();
  });

  it("shows a deleted take as gone, with studio's not-found text", async () => {
    answer("GET", {});
    const id = "00000000-0000-4000-8000-00000000dead";
    mount(videos, rowsOf({ take_id: id }));
    expect(screen.getByText("Loading…")).toBeTruthy();
    await waitFor(() => expect(screen.getByText("The take was deleted.")).toBeTruthy());
    expect(screen.getByRole("alert").textContent).toBe(`take ${id} not found`);
  });

  it("removes a row; Add hides once the rows are at max", () => {
    const [a, b, c] = [video(3), video(3), video(3)];
    remember([a, b, c]);
    const seen = mount(
      videos,
      rowsOf(
        { take_id: a.id, in: 0, out: 3 },
        { take_id: b.id, in: 0, out: 3 },
        { take_id: c.id, in: 0, out: 3 },
      ),
    );
    expect(screen.queryByRole("button", { name: "Add video" })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Remove <Video 2>" }));
    const last = seen.at(-1) as S<"RowItem">[];
    expect(last.map((r) => (r.video as S<"MediaRef">).take_id)).toEqual([a.id, c.id]);
    expect(screen.getByRole("button", { name: "Add video" })).toBeTruthy();
  });

  it("trims by keyboard on the take's frames, never past its end", () => {
    const a = video(4); // 96 frames at 24 fps
    remember([a]);
    const seen = mount(videos, rowsOf({ take_id: a.id, in: 0, out: 4 }));
    const out = screen.getByRole("slider", { name: "Out" });
    fireEvent.keyDown(out, { key: "ArrowLeft" });
    let v = (seen.at(-1) as S<"RowItem">[])[0].video as S<"MediaRef">;
    expect(v.out).toBeCloseTo(4 - 1 / 24, 9);
    fireEvent.keyDown(screen.getByRole("slider", { name: "Out" }), { key: "ArrowRight" });
    fireEvent.keyDown(screen.getByRole("slider", { name: "Out" }), { key: "ArrowRight" });
    v = (seen.at(-1) as S<"RowItem">[])[0].video as S<"MediaRef">;
    expect(v.out).toBe(4);
    fireEvent.keyDown(screen.getByRole("slider", { name: "In" }), {
      key: "ArrowRight",
      shiftKey: true,
    });
    v = (seen.at(-1) as S<"RowItem">[])[0].video as S<"MediaRef">;
    expect(v.in).toBeCloseTo(10 / 24, 9);
  });

  it("keeps a trimmed part within max_s when Out moves (lib/source trimmed's contract)", () => {
    const a = video(20); // longer than max_s 15: the pick starts as 0 – 15
    remember([a]);
    const seen = mount(videos, rowsOf({ take_id: a.id, in: 0, out: 15 }));
    fireEvent.keyDown(screen.getByRole("slider", { name: "Out" }), {
      key: "ArrowRight",
      shiftKey: true,
    });
    const v = (seen.at(-1) as S<"RowItem">[])[0].video as S<"MediaRef">;
    // "out within the take … and at most `max_s` past in"
    expect(v.out! - (v.in ?? 0)).toBeLessThanOrEqual(15 + 1e-9);
  });

  it("sets Sound per row", () => {
    const a = video(3);
    remember([a]);
    const seen = mount(videos, rowsOf({ take_id: a.id, in: 0, out: 3 }));
    const group = screen.getByRole("group", { name: "Sound" });
    expect(within(group).getByRole("button", { name: "Keep" }).getAttribute("aria-pressed")).toBe(
      "true",
    );
    fireEvent.click(within(group).getByRole("button", { name: "Silent" }));
    expect((seen.at(-1) as S<"RowItem">[])[0].sound).toBe("silent");
    expect(within(group).getByRole("button", { name: "Silent" }).getAttribute("aria-pressed")).toBe(
      "true",
    );
  });

  it("trims audio to the millisecond and says where to get it", () => {
    const s = audio(6);
    remember([s]);
    const seen = mount(audios, [{ audio: { take_id: s.id, in: 0, out: 6 } }]);
    expect(screen.getByText("wav · 6.0 s")).toBeTruthy();
    expect(screen.getByText(/Add audio also uploads wav or\s+mp3/)).toBeTruthy();
    fireEvent.keyDown(screen.getByRole("slider", { name: "In" }), { key: "ArrowRight" });
    const v = (seen.at(-1) as S<"RowItem">[])[0].audio as S<"MediaRef">;
    expect(v.in).toBeCloseTo(0.01, 9);
  });

  it("opens the picker of the kind the rows take", () => {
    answer("GET", {
      "/v1/studio/takes": () => ({ data: { takes: [], next_cursor: null } }),
    });
    HTMLDialogElement.prototype.showModal ??= function (this: HTMLDialogElement) {
      this.open = true;
    };
    mount(audios, []);
    fireEvent.click(screen.getByRole("button", { name: "Add audio" }));
    expect(screen.getByRole("dialog", { name: "Add audio", hidden: true })).toBeTruthy();
  });
});

describe("MediaRows: the composer's chips", () => {
  it("draws a chip per row with its mention and Remove", () => {
    const a = video(3);
    remember([a]);
    const seen = mount(videos, rowsOf({ take_id: a.id, in: 0, out: 3 }), true);
    const chip = screen.getByRole("listitem", { name: `<Video 1>: ${a.title}` });
    fireEvent.click(within(chip).getByRole("button", { name: "Remove <Video 1>" }));
    expect(seen.at(-1)).toEqual([]);
    expect(screen.getByRole("button", { name: "Add video" })).toBeTruthy();
  });
});
