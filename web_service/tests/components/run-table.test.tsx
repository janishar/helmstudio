// @vitest-environment jsdom
// The run table under the pipeline graph (components/pipelines/run-table.tsx; docs/mock/PipelineGraph.dc.html): step
// columns named by what they make, one line of text per cell (no thumbnails, seeds or times), the row's number
// opening its execution view, and Open in gallery on a done row in place of the execution icon.
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import rowFixture from "../../../contracts/fixtures/http/BatchRow.valid.3.json";
import execFixture from "../../../contracts/fixtures/http/RowExecution.valid.json";
import batchFixture from "../../../contracts/fixtures/http/Batch.valid.3.json";
import { RunTable } from "@/components/pipelines/run-table";
import type { S } from "@/lib/api";

vi.mock("next/link", () => ({
  default: ({ href, children, ...rest }: { href: string; children: React.ReactNode }) => (
    <a href={href} {...rest}>
      {children}
    </a>
  ),
}));

afterEach(cleanup);

const row = rowFixture as unknown as S<"BatchRow">;
const batch = batchFixture as unknown as S<"Batch">;
const doc = (execFixture as unknown as S<"RowExecution">).pipeline;
const wf = (doc.nodes.still as S<"PipelineStepNode">).workflow;

describe("RunTable", () => {
  it("draws the board's table: kind-named columns, text cells, Open in gallery", () => {
    const done: S<"BatchRow"> = {
      ...row,
      row: row.row + 1,
      status: "succeeded",
      take_ids: row.cells![0].attempts[0].take_ids.slice(0, 1),
      thumb_url: "/files/u/x/1.png",
    };
    const onOpen = vi.fn();
    const { container } = render(
      <RunTable
        batch={batch}
        rows={[row, done]}
        doc={doc}
        titles={{}}
        busy=""
        onRetry={() => undefined}
        onOpen={onOpen}
        media={{ [wf]: "image" }}
      />,
    );
    const heads = [...container.querySelectorAll("th")].map((t) => t.textContent);
    expect(heads).toContain("Step 1 · images");
    // a cell is its one line; it opens its takes
    const cell = screen.getAllByRole("button", {
      name: new RegExp(`Open the takes of row ${row.row}, Step 1`),
    })[0];
    expect(cell.textContent).toBe("done · 4 takes");
    fireEvent.click(cell);
    expect(onOpen).toHaveBeenCalledWith(row.cells![0].attempts[0].take_ids);
    // no per-cell thumbnails: the only images are the Takes column's
    expect(container.querySelectorAll("tbody td:not(:nth-last-child(2)) img")).toHaveLength(0);
    // the row waiting on its gate: Step 2 is parked
    expect(screen.getAllByText("parked").length).toBeGreaterThan(0);
    // the row's number opens its execution view; no execution icon in the actions
    expect(
      screen.getByRole("link", { name: `Row ${row.row}: execution view` }).getAttribute("href"),
    ).toBe(`/pipelines/batches/${batch.id}/rows/${row.row}`);
    expect(screen.getAllByRole("link", { name: /execution view/ })).toHaveLength(2);
    // Open in gallery only on the done row
    expect(screen.getAllByRole("link", { name: "Open in gallery" })).toHaveLength(1);
  });
});
