// @vitest-environment jsdom
// Add's From Gallery (MagicBoard frame 2a, owner 2026-11-27): the step-1 footer's ghost button hands the point and the
// Place picked to the same From Gallery the empty lane's menu has (menus.ts fromGallery), and closes Add.
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { AddDialog } from "@/components/magic/add";
import { fromGallery, type Act } from "@/components/magic/menus";
import { sampleBoard } from "@/lib/magic-fixture";

beforeAll(() => {
  HTMLDialogElement.prototype.showModal ??= function (this: HTMLDialogElement) {
    this.open = true;
  };
  HTMLDialogElement.prototype.close ??= function (this: HTMLDialogElement) {
    this.open = false;
    this.dispatchEvent(new Event("close"));
  };
});
afterEach(cleanup);

describe("Add's From Gallery", () => {
  const { doc } = sampleBoard();
  const where = { lane: "l7", at: 18 * 24 };
  const open = (onGallery: ((w: typeof where, m: string) => void) | null, onClose = vi.fn()) =>
    render(
      <AddDialog
        doc={doc}
        kinds={null}
        mode="insert"
        where={where}
        onAdd={vi.fn()}
        onGenerate={null}
        onGallery={onGallery}
        onClose={onClose}
      />,
    );

  it("hands the point and the Place picked on, and closes Add", () => {
    const onGallery = vi.fn();
    const onClose = vi.fn();
    open(onGallery, onClose);
    fireEvent.click(screen.getByRole("button", { name: "Overwrite" }));
    fireEvent.click(screen.getByRole("button", { name: "From Gallery" }));
    expect(onGallery).toHaveBeenCalledWith(where, "overwrite");
    expect(onClose).toHaveBeenCalled();
  });

  it("is greyed with no Gallery", () => {
    open(null);
    const b = screen.getByRole("button", { name: "From Gallery" });
    expect(b).toHaveProperty("disabled", true);
    expect(b.getAttribute("title")).toBe("The Gallery is in studio");
  });

  it("places the take by the mode it is given, the lane menu's way", () => {
    const gallery = vi.fn();
    const edit = vi.fn();
    const a = { doc, mode: "insert", gallery, edit } as unknown as Act;
    fromGallery(
      a,
      doc.lanes.find((l) => l.id === "l7")!,
      where.at,
      "overwrite",
    );
    gallery.mock.calls[0][0].onPick([{ id: "t-v", kind: "video", duration: 2 }]);
    expect(edit).toHaveBeenCalledWith({
      op: "add",
      mode: "overwrite",
      block: expect.objectContaining({
        lane: "l7",
        at: 432,
        length: 48,
        takes: ["t-v"],
        pinned: "t-v",
      }),
    });
  });
});
