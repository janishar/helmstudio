// @vitest-environment jsdom
// Audio fades, crossfades and ducking (plan 41, docs/mock/AudioFades.dc.html): the fade handles' drag (readout, one
// set per drag), the right-click rows on a Timeline clip and a Magic block (Fade in… opens its seconds popover), the
// crossfade badge's popover, and the Duck music switch.
import { afterEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { closeMenu, openMenu, type Item, type MenuItem } from "@/components/context-menu";
import { DuckRow, FadeCurve, FadeHandles, XfadeBadge, clipFadeItems } from "@/components/fades";
import { blockMenu, fadeItems, type Act } from "@/components/magic/menus";
import { sampleBoard } from "@/lib/magic-fixture";
import { normalize, type Norm } from "@/lib/timeline";

afterEach(() => {
  act(closeMenu);
  cleanup();
});

const labels = (items: Item[]) =>
  items.filter((x): x is MenuItem | "-" => !!x).map((x) => (x === "-" ? "-" : x.label));
const item = (items: Item[], label: string) =>
  items.find((x): x is MenuItem => !!x && x !== "-" && x.label === label)!;

describe("fade handles", () => {
  it("drag inward to lengthen: a yellow readout, 0.1 s steps, one set on release", () => {
    const onSet = vi.fn();
    render(
      <div>
        <FadeHandles len={10} pps={40} fadeIn={0} fadeOut={1} onSet={onSet} />
      </div>,
    );
    const h = screen.getByRole("slider", { name: "Fade in" });
    fireEvent.pointerDown(h, { button: 0, clientX: 100, pointerId: 1 });
    fireEvent.pointerMove(h, { clientX: 162, pointerId: 1 });
    expect(screen.getByRole("status").textContent).toBe("Fade in 1.6 s");
    fireEvent.pointerMove(h, { clientX: 1000, pointerId: 1 });
    // never past the other fade: 10 s − 1 s
    expect(screen.getByRole("status").textContent).toBe("Fade in 9 s");
    fireEvent.pointerUp(h, { pointerId: 1 });
    expect(onSet).toHaveBeenCalledTimes(1);
    expect(onSet).toHaveBeenCalledWith("fade_in", 9);
    expect(screen.queryByRole("status")).toBeNull();
  });
  it("the out handle sits at the fade's end and drags left to lengthen", () => {
    const onSet = vi.fn();
    render(<FadeHandles len={4} pps={50} fadeOut={1} onSet={onSet} />);
    const h = screen.getByRole("slider", { name: "Fade out" });
    expect(h.style.left).toBe("150px");
    fireEvent.pointerDown(h, { button: 0, clientX: 150, pointerId: 1 });
    fireEvent.pointerMove(h, { clientX: 125, pointerId: 1 });
    fireEvent.pointerUp(h, { pointerId: 1 });
    expect(onSet).toHaveBeenCalledWith("fade_out", 1.5);
  });
  it("the curve shades what each fade cuts", () => {
    const { container } = render(<FadeCurve w={200} fin={40} fout={0} />);
    expect(container.querySelectorAll("path")).toHaveLength(2);
    expect(render(<FadeCurve w={200} fin={0} fout={0} />).container.innerHTML).toBe("");
  });
});

describe("the Timeline clip's rows", () => {
  const takes = { m: { kind: "audio", duration_s: 20, has_audio: true } };
  const doc = (() => {
    const n = normalize(
      {
        target: { width: 1280, height: 720, fps: 24, sample_rate: 48000 },
        tracks: [
          { kind: "video", clips: [{ id: "g", take_id: null, hold: 10 }] },
          {
            kind: "audio",
            clips: [
              { id: "a", take_id: "m", in: 0, out: 3, at: 0, fade_in: 1.5 },
              { id: "b", take_id: "m", in: 0, out: 3, at: 3 },
            ],
          },
        ],
      },
      takes,
    );
    return (n as { document: Norm }).document;
  })();
  it("Fade in…, Fade out…, Crossfade into next (greyed with No clip after), Remove fades", () => {
    const commit = vi.fn();
    const a = clipFadeItems(doc, 1, doc.tracks[1].clips[0], commit);
    expect(labels(a)).toEqual([
      "-",
      "Fade in…",
      "Fade out…",
      "Crossfade into next",
      "Remove fades",
    ]);
    expect(item(a, "Fade in…").keys).toBe("1.5 s");
    item(a, "Crossfade into next").onPick!();
    expect(commit).toHaveBeenCalledWith({ op: "set", clip: "a", crossfade: 2 });
    item(a, "Remove fades").onPick!();
    expect(commit).toHaveBeenLastCalledWith({
      op: "set",
      clip: "a",
      fade_in: null,
      fade_out: null,
      crossfade: null,
    });
    const b = clipFadeItems(doc, 1, doc.tracks[1].clips[1], commit);
    expect(item(b, "Crossfade into next")).toMatchObject({
      off: "No clip after",
      keys: "No clip after",
    });
    expect(item(b, "Remove fades").off).toBe("No fades");
  });
  it("Fade in… opens its popover beside the menu: a quick pick sets it", () => {
    const commit = vi.fn();
    const items = clipFadeItems(doc, 1, doc.tracks[1].clips[1], commit);
    render(
      <button type="button" onContextMenu={(e) => openMenu(e, { title: "Night bus", items })}>
        clip
      </button>,
    );
    fireEvent.contextMenu(screen.getByText("clip"), { button: 2, clientX: 10, clientY: 10 });
    fireEvent.click(screen.getByRole("menuitem", { name: /Fade in/ }));
    const pop = screen.getByRole("dialog", { name: "Fade in" });
    expect((screen.getByLabelText("Fade in (s)") as HTMLInputElement).value).toBe("1");
    fireEvent.click(
      Array.from(pop.querySelectorAll("button")).find((x) => x.textContent === "2 s")!,
    );
    expect(commit).toHaveBeenCalledWith({ op: "set", clip: "b", fade_in: 2 });
  });
  it("a typed length and Enter set it", () => {
    const commit = vi.fn();
    const items = clipFadeItems(doc, 1, doc.tracks[1].clips[1], commit);
    render(
      <button type="button" onContextMenu={(e) => openMenu(e, { items })}>
        clip
      </button>,
    );
    fireEvent.contextMenu(screen.getByText("clip"), { button: 2, clientX: 10, clientY: 10 });
    fireEvent.click(screen.getByRole("menuitem", { name: /Fade out/ }));
    const input = screen.getByLabelText("Fade out (s)");
    fireEvent.change(input, { target: { value: "12" } });
    fireEvent.keyDown(input, { key: "Enter" });
    expect(commit).toHaveBeenCalledWith({ op: "set", clip: "b", fade_out: 10 });
  });
});

describe("the Magic block's rows", () => {
  const actOn = () => {
    const { doc, takes } = sampleBoard();
    return { doc, takes, edit: vi.fn(), sel: [] } as unknown as Act & {
      edit: ReturnType<typeof vi.fn>;
    };
  };
  it("a sound block: Crossfade into next adds a crossfade join; at a lane's last block it is greyed", () => {
    const a = actOn();
    const x1 = a.doc.blocks.find((b) => b.id === "x1")!;
    item(fadeItems(a, x1), "Crossfade into next").onPick!();
    expect(a.edit).toHaveBeenCalledWith({
      op: "transition",
      between: ["x1", "x2"],
      kind: "crossfade",
    });
    const x2 = a.doc.blocks.find((b) => b.id === "x2")!;
    expect(item(fadeItems(a, x2), "Crossfade into next").off).toBe("No block after");
  });
  it("with a crossfade: Crossfade 2 s…; Remove fades clears the fades and the crossfade", () => {
    const a = actOn();
    a.doc.transitions.push({ id: "xf", between: ["x1", "x2"], kind: "crossfade", length: 48 });
    const x1 = a.doc.blocks.find((b) => b.id === "x1")!;
    x1.fade_in = 1;
    const items = fadeItems(a, x1);
    expect(labels(items)).toContain("Crossfade 2 s…");
    item(items, "Remove fades").onPick!();
    expect(a.edit).toHaveBeenCalledWith([
      { op: "block", id: "x1", set: { fade_in: null, fade_out: null } },
      { op: "transition", between: ["x1", "x2"], kind: "cut" },
    ]);
  });
  it("a voice block has no crossfade row; a shot none of these", () => {
    const a = actOn();
    const v = blockMenu(
      a,
      a.doc.blocks.find((b) => b.id === "v2")!,
    );
    expect(labels(v.items)).not.toContain("Crossfade into next");
    expect(labels(v.items)).toContain("Fade in…");
    const s = blockMenu(
      a,
      a.doc.blocks.find((b) => b.id === "s2")!,
    );
    expect(labels(s.items)).not.toContain("Fade in…");
  });
});

describe("the crossfade badge", () => {
  it("sky with its length; a click opens the popover: a pick sets it, Remove takes it off", () => {
    const onSet = vi.fn();
    render(
      <XfadeBadge left={0} seconds={2} dip={false} title="t" note="Up to 10 s." onSet={onSet} />,
    );
    const badge = screen.getByRole("button", { name: "Crossfade 2 s" });
    expect(badge.className).toContain("xf");
    fireEvent.click(badge);
    fireEvent.click(screen.getByRole("button", { name: "4 s" }));
    expect(onSet).toHaveBeenCalledWith(4);
    fireEvent.click(badge);
    fireEvent.click(screen.getByRole("button", { name: "Remove" }));
    expect(onSet).toHaveBeenLastCalledWith(null);
  });
  it("amber for a dip", () => {
    render(<XfadeBadge left={0} seconds={2} dip title="t" note="n" onSet={vi.fn()} />);
    expect(screen.getByRole("button", { name: "Dip 2 s" }).className).toContain("dip");
  });
});

describe("Duck music", () => {
  it("the switch turns it on or off; the amount opens its picks", () => {
    const onSet = vi.fn();
    render(<DuckRow on={false} db={-12} onSet={onSet} />);
    const sw = screen.getByRole("switch", { name: "Duck music" });
    expect(sw.getAttribute("aria-checked")).toBe("false");
    fireEvent.click(sw);
    expect(onSet).toHaveBeenCalledWith({ on: true });
    fireEvent.click(screen.getByRole("button", { name: "Duck by −12 dB" }));
    fireEvent.click(screen.getByRole("menuitemcheckbox", { name: /−18 dB/ }));
    expect(onSet).toHaveBeenLastCalledWith({ db: -18 });
  });
});
