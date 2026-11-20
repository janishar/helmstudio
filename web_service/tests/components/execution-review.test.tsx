// @vitest-environment jsdom
// The execution view and the judge review grid as their boards draw them (docs/mock/Execution.dc.html,
// JudgeReview.dc.html): the title as a menu (no back chevron), "Step 1 · submit" lines, a passing judge in green,
// the judge's words and tiles, the graph laid out afresh; the review's note placeholder and "gave up after" line.
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import execFixture from "../../../contracts/fixtures/http/RowExecution.valid.2.json";
import reviewFixture from "../../../contracts/fixtures/http/BatchReview.valid.2.json";
import batchFixture from "../../../contracts/fixtures/http/Batch.valid.3.json";
import { Execution } from "@/components/pipelines/execution";
import { Review } from "@/components/pipelines/review";
import type { CanvasProps } from "@/components/pipelines/canvas";
import type { S } from "@/lib/api";
import { answer } from "./helpers";

const canvas: CanvasProps[] = [];
vi.mock("next/dynamic", () => ({
  default: () => (p: CanvasProps) => {
    canvas.push(p);
    return null;
  },
}));
vi.mock("next/navigation", () => ({ useRouter: () => ({ push: vi.fn(), replace: vi.fn() }) }));
vi.mock("next/link", () => ({
  default: ({ href, children, ...rest }: { href: string; children: React.ReactNode }) => (
    <a href={href} {...rest}>
      {children}
    </a>
  ),
}));
vi.mock("@/components/pipelines/pipelines", () => ({
  followBatch: () => () => undefined,
  StateChip: () => <span>state</span>,
}));
vi.mock("@/components/pipelines/memory", () => ({ MemoryPanel: () => null }));

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  canvas.length = 0;
});

const exec = execFixture as unknown as S<"RowExecution">;
const batch = batchFixture as unknown as S<"Batch">;

describe("Execution", () => {
  it("draws the board's header, step lines and judge details", () => {
    answer("GET", { "/v1/studio/hosts": () => ({ data: { hosts: [] } }) });
    const { container } = render(<Execution batch={batch} initial={exec} />);
    // the title is a menu button; no back chevron
    expect(screen.queryByLabelText("Back to the batch")).toBeNull();
    expect(screen.getByRole("button", { name: exec.pipeline.title }).tagName).toBe("BUTTON");
    // the menu is Rename and All pipelines (Q4); the batch is a link in the row's line
    expect(screen.queryByRole("menuitem", { name: /batch/i, hidden: true })).toBeNull();
    expect(screen.getByRole("link", { name: batch.title }).getAttribute("href")).toBe(
      `/pipelines/batches/${exec.batch_id}`,
    );
    // step lines without the workflow title; a passing judge in green
    const node = [...container.querySelectorAll("tbody tr td:nth-child(2)")].map(
      (t) => t.textContent,
    );
    expect(node).toEqual(["Inputs", "Step 1 · submit", "Step 1 · wait", "Judge"]);
    const what = container.querySelector("tbody tr:nth-child(4) td:nth-child(3)")!;
    expect(what.className).toContain("text-a1");
    // the judge (the current step) in words, and only Model and Memory read
    expect(screen.getByText(/Row 6 · attempt 1 · checkpoint 4 of 4/)).toBeTruthy();
    const tiles = [...container.querySelectorAll("aside .set small")].map((t) => t.textContent);
    expect(tiles).toEqual(["Model", "Memory read"]);
    expect(screen.getByText(/^\d+ examples?$/)).toBeTruthy();
    // no saved positions in the fixture: the canvas lays it out (elkjs)
    const doc = canvas.at(-1)!.doc;
    expect(Object.values(doc.nodes).every((n) => !n.position)).toBe(true);
  });

  it("draws dashed take tiles when the judge's thumbnails are gone", () => {
    answer("GET", { "/v1/studio/hosts": () => ({ data: { hosts: [] } }) });
    const steps = exec.steps.map((s) =>
      s.decision ? { ...s, decision: { ...s.decision, seen: [] } } : s,
    );
    render(<Execution batch={batch} initial={{ ...exec, steps }} />);
    expect(screen.getByText("take 1")).toBeTruthy();
    expect(screen.queryByText("No thumbnails.")).toBeNull();
  });
});

describe("Review", () => {
  const review = reviewFixture as unknown as S<"BatchReview">;
  it("has no back chevron, the note's placeholder and a judge that gave up", () => {
    const gaveUp: S<"BatchReviewItem"> = {
      ...review.items[0],
      row: 7,
      visit: 3,
      verdict: "fail",
      suggested: [],
    };
    render(
      <Review
        id={review.batch_id}
        initial={{ batch, review: { ...review, items: [review.items[0], gaveUp] } }}
      />,
    );
    expect(screen.queryByLabelText("Back to the batch")).toBeNull();
    expect(screen.getByText("2 rows waiting for a pick")).toBeTruthy();
    const note = screen.getAllByLabelText(/Why this one\?/)[0] as HTMLInputElement;
    expect(note.placeholder).toBe("The softer one keeps the fox's face; sharpness matters less");
    expect(screen.getByText(/· attempt 3 · gave up after 2 retries/)).toBeTruthy();
    fireEvent.change(note, { target: { value: "x" } });
  });
});
