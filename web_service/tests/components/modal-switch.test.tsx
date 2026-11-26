// @vitest-environment jsdom
// The shared Modal (components/kit.tsx): one dialog opening another keeps the new one open.
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { useState } from "react";
import { afterEach, beforeAll, expect, it } from "vitest";
import { Modal } from "@/components/kit";

beforeAll(() => {
  HTMLDialogElement.prototype.showModal ??= function (this: HTMLDialogElement) {
    this.open = true;
  };
  // the browser fires "close" as a task after close(), not at once
  HTMLDialogElement.prototype.close ??= function (this: HTMLDialogElement) {
    this.open = false;
    setTimeout(() => this.dispatchEvent(new Event("close")), 0);
  };
});
afterEach(cleanup);

function Two() {
  const [d, setD] = useState<"list" | "new" | null>("list");
  return (
    <>
      <Modal open={d === "list"} onClose={() => setD(null)} title="Sequences">
        <button type="button" onClick={() => setD("new")}>
          + New sequence
        </button>
      </Modal>
      <Modal open={d === "new"} onClose={() => setD(null)} title="New sequence">
        <p>the form</p>
      </Modal>
      <span data-testid="d">{String(d)}</span>
    </>
  );
}

it("one dialog opening another stays open (the Timeline's Sequences → + New sequence)", async () => {
  render(<Two />);
  fireEvent.click(screen.getByText("+ New sequence"));
  await new Promise((f) => setTimeout(f, 20));
  expect(screen.getByTestId("d").textContent).toBe("new");
  expect((screen.getByLabelText("New sequence") as HTMLDialogElement).open).toBe(true);
});

it("its own close (Esc, ×, the backdrop) still tells the parent", async () => {
  render(<Two />);
  const d = screen.getByLabelText("Sequences") as HTMLDialogElement;
  d.dispatchEvent(new Event("close"));
  await new Promise((f) => setTimeout(f, 0));
  expect(screen.getByTestId("d").textContent).toBe("null");
});
