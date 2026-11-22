// @vitest-environment jsdom
// The reference grid (components/create/refs.tsx RowsEditor), the composer's image tiles (form.tsx RefChips: First
// frame, the optional Last frame, References as chips) and keyframe cards on the clip.
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { FirstFrameTile, RowsEditor, type RefUploads } from "@/components/create/refs";
import { RefChips } from "@/components/create/form";
import { remember } from "@/lib/take-cache";
import { buildInputs, type Field, type Raw, type Update } from "@/lib/workflow";
import type { S } from "@/lib/api";
import { answer, field, Host, take } from "./helpers";

beforeAll(() => {
  HTMLDialogElement.prototype.showModal ??= function (this: HTMLDialogElement) {
    this.open = true;
  };
  HTMLDialogElement.prototype.close ??= function (this: HTMLDialogElement) {
    this.open = false;
  };
  globalThis.requestAnimationFrame ??= (f: FrameRequestCallback) =>
    setTimeout(() => f(0), 0) as unknown as number;
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

const uploads: RefUploads = { list: [], upload: () => undefined, dismiss: () => undefined };

// MiniMax H3 video's image inputs (runner_service/packs/minimax_h3/model.yaml)
const refs = field("generate", {
  name: "refs",
  type: "rows",
  label: "References",
  default: [],
  min: 0,
  max: 9,
  item: { image: "Image" } as unknown as S<"ParamSchema">["item"],
  mention: "<Picture {n}>",
});
const first = field("first", { name: "image", type: "image", label: "First frame" });
const last = field(
  "last",
  { name: "image", type: "image", label: "Last frame" },
  { optional: true, initial: null },
);

const rowsOf = (...ids: string[]) => ids.map((id) => ({ image: { take_id: id } }));
const idsOf = (raw: Raw | undefined) =>
  (raw as S<"RowItem">[]).map((r) => (r.image as { take_id: string }).take_id);

function grid(f: Field, initial: Raw | undefined, seen: Raw[] = []) {
  render(
    <Host
      initial={initial}
      seen={seen}
      render={(raw, onChange) => (
        <RowsEditor f={f} raw={raw} onChange={onChange} uploads={uploads} />
      )}
    />,
  );
  return seen;
}

describe("RowsEditor: the References grid", () => {
  it("names each reference as the prompt does, with the count", () => {
    const [a, b] = [take(), take()];
    remember([a, b]);
    grid(refs, rowsOf(a.id, b.id));
    expect(screen.queryByText(/In the prompt/)).toBeNull(); // the (i) help says it
    expect(screen.getByLabelText("2 of 9").textContent).toBe("2 / 9");
    const list = screen.getByRole("list", { name: "References" });
    expect(
      within(list)
        .getAllByRole("listitem")
        .map((x) => x.getAttribute("aria-label")),
    ).toEqual([`<Picture 1>: ${a.title}`, `<Picture 2>: ${b.title}`]);
  });

  it("reorders with Alt + arrows and removes with Delete, keeping the mention order", () => {
    const [a, b, c] = [take(), take(), take()];
    remember([a, b, c]);
    const seen = grid(refs, rowsOf(a.id, b.id, c.id));
    fireEvent.keyDown(screen.getByRole("listitem", { name: `<Picture 1>: ${a.title}` }), {
      key: "ArrowRight",
      altKey: true,
    });
    expect(idsOf(seen.at(-1))).toEqual([b.id, a.id, c.id]);
    expect(screen.getByRole("listitem", { name: `<Picture 2>: ${a.title}` })).toBeTruthy();
    fireEvent.keyDown(screen.getByRole("listitem", { name: `<Picture 1>: ${b.title}` }), {
      key: "Delete",
    });
    expect(idsOf(seen.at(-1))).toEqual([a.id, c.id]);
    fireEvent.click(screen.getByRole("button", { name: "Remove <Picture 2>" }));
    expect(idsOf(seen.at(-1))).toEqual([a.id]);
  });

  it("hides Add and Upload when full", () => {
    const ts = Array.from({ length: 9 }, () => take());
    remember(ts);
    grid(refs, rowsOf(...ts.map((t) => t.id)));
    expect(screen.queryByRole("button", { name: "Add from gallery" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Upload" })).toBeNull();
  });

  it("adds picks from the gallery after the ones there, each once", async () => {
    const [a, b] = [take(), take()];
    remember([a]);
    answer("GET", {
      "/v1/studio/takes": () => ({ data: { takes: [a, b], next_cursor: null } }),
    });
    const seen = grid(refs, rowsOf(a.id));
    fireEvent.click(screen.getByRole("button", { name: "Add from gallery" }));
    const dialog = screen.getByRole("dialog", { name: "Add from gallery", hidden: true });
    await waitFor(() => within(dialog).getByText(b.title, { selector: "b" }));
    // docs/mock/GalleryPicker: the help line under the footer
    expect(
      within(dialog).getByText(
        "Images only. Picks join the references in the order picked; the limit is what is left of the workflow's maximum (9 here, 1 already added).",
      ),
    ).toBeTruthy();
    fireEvent.click(within(dialog).getByText(a.title, { selector: "b" }).closest("button")!);
    fireEvent.click(within(dialog).getByText(b.title, { selector: "b" }).closest("button")!);
    fireEvent.click(within(dialog).getByRole("button", { name: "Add 2", hidden: true }));
    expect(idsOf(seen.at(-1))).toEqual([a.id, b.id]);
  });

  it("shows a take that is gone", async () => {
    answer("GET", {});
    grid(refs, rowsOf("00000000-0000-4000-8000-0000000000aa"));
    await waitFor(() =>
      expect(screen.getByRole("listitem", { name: "<Picture 1>: gone" })).toBeTruthy(),
    );
  });

  it("an optional image says so and clears to empty, which the run sends as null", () => {
    const a = take();
    remember([a]);
    const seen = grid(last, a.id);
    expect(screen.getByText("Optional: may stay empty")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Remove image 1" }));
    expect(seen.at(-1)).toBe("");
    expect(buildInputs([last], { [last.key]: "" })).toEqual({ inputs: {}, errors: {} });
    // a required one left empty is an error
    expect(buildInputs([first], { [first.key]: "" }).errors).toEqual({
      [first.key]: "an image is required",
    });
  });
});

describe("RefChips: the composer's image tiles", () => {
  function composer(raws: Record<string, Raw>, fs = [first, last, refs]) {
    const changes: [string, Update][] = [];
    render(
      <RefChips
        fields={fs}
        raws={raws}
        errors={{}}
        onChange={(k, u) => changes.push([k, u])}
        uploads={uploads}
      />,
    );
    return changes;
  }

  it("draws First frame and the optional Last frame as tiles, References as chips", () => {
    const a = take();
    remember([a]);
    composer({ [first.key]: a.id, [last.key]: "", [refs.key]: [] });
    expect(screen.getByRole("button", { name: `First frame: ${a.title}` })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Last frame: None · optional" })).toBeTruthy();
    expect(screen.getByRole("list", { name: "References" })).toBeTruthy();
  });

  it("a required image tile with nothing says None", () => {
    composer({ [first.key]: "" }, [first]);
    expect(screen.getByRole("button", { name: "First frame: None" })).toBeTruthy();
  });

  it("picking a Last frame sets that field to the take", async () => {
    const a = take();
    answer("GET", {
      "/v1/studio/takes": () => ({ data: { takes: [a], next_cursor: null } }),
    });
    const changes = composer({ [first.key]: "", [last.key]: "" }, [first, last]);
    fireEvent.click(screen.getByRole("button", { name: "Last frame: None · optional" }));
    const dialog = screen
      .getAllByRole("dialog", { hidden: true })
      .find((d) => (d as HTMLDialogElement).open)!;
    await waitFor(() => within(dialog).getByText(a.title, { selector: "b" }));
    fireEvent.click(within(dialog).getByText(a.title, { selector: "b" }).closest("button")!);
    fireEvent.click(within(dialog).getByRole("button", { name: "Add 1", hidden: true }));
    expect(changes).toEqual([[last.key, a.id]]);
  });
});

describe("RefChips: the First frame tile of keyframe rows", () => {
  const keys = field("keys", {
    name: "frames",
    type: "rows",
    label: "Keyframes",
    default: [],
    max: 4,
    item: {
      image: "Image",
      seconds: { type: "float", label: "At", default: 0, min: 0, max: 20, position: "seconds" },
    } as unknown as S<"ParamSchema">["item"],
  });
  const frames = field("sample", {
    name: "frames",
    type: "frames",
    default: 49,
    min: 9,
    step: 8,
    max: 321,
    fps: "fps",
  });
  const fps = field("sample", { name: "fps", type: "int", default: 24 });

  it("shows the take on frame 0 and how many more; a pick replaces frame 0's image", async () => {
    const [a, b, c] = [take(), take(), take()];
    remember([a, b]);
    answer("GET", {
      "/v1/studio/takes": () => ({ data: { takes: [c], next_cursor: null } }),
    });
    const rows = [
      { image: { take_id: b.id }, seconds: 1 },
      { image: { take_id: a.id }, seconds: 0 },
    ];
    const seen: Raw[] = [];
    render(
      <Host
        initial={rows}
        seen={seen}
        render={(raw, onChange) => (
          <RefChips
            fields={[keys, frames, fps]}
            raws={raw === undefined ? {} : { [keys.key]: raw }}
            errors={{}}
            onChange={onChange}
            uploads={uploads}
          />
        )}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: `First frame: ${a.title} +1` }));
    const dialog = screen
      .getAllByRole("dialog", { hidden: true })
      .find((d) => (d as HTMLDialogElement).open)!;
    await waitFor(() => within(dialog).getByText(c.title, { selector: "b" }));
    fireEvent.click(within(dialog).getByText(c.title, { selector: "b" }).closest("button")!);
    fireEvent.click(within(dialog).getByRole("button", { name: "Add 1", hidden: true }));
    expect(seen.at(-1)).toEqual([
      { image: { take_id: b.id }, seconds: 1 },
      { image: { take_id: c.id }, seconds: 0 },
    ]);
  });

  it("with no row on frame 0, a pick adds one first", () => {
    const a = take();
    remember([a]);
    render(
      <RefChips
        fields={[keys, frames, fps]}
        raws={{ [keys.key]: [{ image: { take_id: a.id }, seconds: 1 }] }}
        errors={{}}
        onChange={() => undefined}
        uploads={uploads}
      />,
    );
    expect(screen.getByRole("button", { name: "First frame: None · text only +1" })).toBeTruthy();
  });
});

describe("RowsEditor: keyframe cards on the clip", () => {
  // LTX image to video: rows of {image, seconds (position), strength} on a 49-frame clip at 24 fps
  const keys = field("keys", {
    name: "frames",
    type: "rows",
    label: "Keyframes",
    default: [],
    max: 4,
    item: {
      image: "Image",
      seconds: { type: "float", label: "At", default: 0, min: 0, max: 20, position: "seconds" },
      strength: { type: "float", label: "Strength", default: 1, min: 0, max: 1, step: 0.05 },
    } as unknown as S<"ParamSchema">["item"],
  });
  const clip = { frames: 49, fps: 24, last: 48 };

  it("reads each row's time as its frame and flags a frame two rows share", () => {
    const [a, b] = [take(), take()];
    remember([a, b]);
    render(
      <RowsEditor
        f={keys}
        raw={[
          { image: { take_id: a.id }, seconds: 0 },
          { image: { take_id: b.id }, seconds: 0.01 },
        ]}
        onChange={() => undefined}
        uploads={uploads}
        clip={clip}
      />,
    );
    const cards = within(screen.getByRole("list", { name: "Keyframes, in order" })).getAllByRole(
      "listitem",
    );
    expect(cards).toHaveLength(2);
    expect(within(cards[0]).getByText("frame 0 · 0.0 s")).toBeTruthy();
    expect(within(cards[1]).getByRole("alert").textContent).toBe(
      "frame 0 is already used by row 1",
    );
    expect(screen.getByRole("img", { name: "Frames on the clip: 0.0 s, 0.0 s" })).toBeTruthy();
  });

  it("moves a keyframe by its slider in whole frames", () => {
    const a = take();
    remember([a]);
    const seen: Raw[] = [];
    render(
      <Host
        initial={[{ image: { take_id: a.id }, seconds: 0 }]}
        seen={seen}
        render={(raw, onChange) => (
          <RowsEditor f={keys} raw={raw} onChange={onChange} uploads={uploads} clip={clip} />
        )}
      />,
    );
    const at = screen.getByRole("slider", { name: /At/ });
    expect(at.getAttribute("max")).toBe("48");
    fireEvent.change(at, { target: { value: "48" } });
    expect((seen.at(-1) as S<"RowItem">[])[0].seconds).toBe(2);
    expect(screen.getByText("frame 48 · 2.0 s")).toBeTruthy();
  });

  it("an enum item field is a segmented choice on its row (LTX binding)", () => {
    const a = take();
    remember([a]);
    const seen: Raw[] = [];
    const bound = field("bound", {
      ...keys.param,
      // the API sends the item's keys sorted, so the enum comes first; it is drawn under the sliders
      item: {
        binding: {
          type: "enum",
          label: "Binding",
          default: "auto",
          enum: ["auto", "replace", "guide"],
        },
        ...(keys.param.item as object),
      } as unknown as S<"ParamSchema">["item"],
    });
    render(
      <Host
        initial={[{ image: { take_id: a.id }, seconds: 0 }]}
        seen={seen}
        render={(raw, onChange) => (
          <RowsEditor f={bound} raw={raw} onChange={onChange} uploads={uploads} clip={clip} />
        )}
      />,
    );
    const seg = screen.getByRole("group", { name: "Binding" });
    expect(seg.className).toContain("seg");
    const slider = screen.getAllByRole("slider").at(-1)!;
    expect(slider.compareDocumentPosition(seg) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(within(seg).getByRole("button", { name: "Auto" }).getAttribute("aria-pressed")).toBe(
      "true",
    );
    fireEvent.click(within(seg).getByRole("button", { name: "Replace" }));
    expect((seen.at(-1) as S<"RowItem">[])[0].binding).toBe("replace");
  });
});

// API 1.37, as the board draws it (docs/mock/H3Options.dc.html frame 8): the Last frame tile's picker has tabs Images |
// Video, and on Video its footer's Use first frame / Use last frame make the frame (an image take) and pick it
describe("FirstFrameTile: a frame of a video from the picker", () => {
  it("Video tab, a video picked, Use last frame", async () => {
    const v = take({ kind: "video", title: "Shot 3 · tree line" });
    const kinds: unknown[] = [];
    const asked: unknown[] = [];
    answer("GET", {
      "/v1/studio/takes": (init) => {
        const q = (init.params as { query: { kind?: string } }).query;
        kinds.push(q.kind);
        return { data: { takes: q.kind === "video" ? [v] : [], next_cursor: null } };
      },
    });
    answer("POST", {
      "/v1/studio/takes/{take_id}/frame": (init) => {
        asked.push(init);
        return { data: { take: { id: "frame-1" } } };
      },
    });
    const picked: string[] = [];
    render(<FirstFrameTile label="Last frame" frames onPick={(id) => picked.push(id)} />);
    fireEvent.click(screen.getByRole("button", { name: /^Last frame:/ }));
    const tabs = screen.getByRole("group", { name: "Kind" });
    expect(
      within(tabs)
        .getAllByRole("button")
        .map((b) => b.textContent),
    ).toEqual(["Images", "Video"]);
    fireEvent.click(within(tabs).getByRole("button", { name: "Video" }));
    fireEvent.click(await screen.findByText("Shot 3 · tree line"));
    expect(screen.queryByRole("button", { name: /^Add/ })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Use last frame" }));
    await waitFor(() => expect(picked).toEqual(["frame-1"]));
    expect(asked).toEqual([{ params: { path: { take_id: v.id } }, body: { at: "last" } }]);
    expect(kinds).toContain("video");
  });
});
