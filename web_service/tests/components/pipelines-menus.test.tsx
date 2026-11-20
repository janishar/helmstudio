// @vitest-environment jsdom
// The owner's second-round answers on the pipeline screens: the title menu (Rename, All pipelines; Q4), a judge's
// checkpoints folded under one line that opens on click (Q8), saved positions kept unless crowded (Q10), and the
// Pipelines panel's ⋯ menu holding Rename and Delete (Q12).
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import execFixture from "../../../contracts/fixtures/http/RowExecution.valid.2.json";
import batchFixture from "../../../contracts/fixtures/http/Batch.valid.3.json";
import pipelineFixture from "../../../contracts/fixtures/http/Pipeline.valid.1.json";
import { Execution } from "@/components/pipelines/execution";
import { Pipelines } from "@/components/pipelines/pipelines";
import type { CanvasProps } from "@/components/pipelines/canvas";
import { api, type S } from "@/lib/api";
import { needsLayout } from "@/lib/pipeline-run";
import type { PDoc } from "@/lib/pipeline";
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
vi.mock("@/components/shell", () => ({ useUser: () => ({ name: "Ada" }) }));
vi.stubGlobal(
  "EventSource",
  class {
    addEventListener() {}
    close() {}
  },
);

beforeAll(() => {
  HTMLElement.prototype.hidePopover ??= () => undefined;
  HTMLDialogElement.prototype.showModal ??= function (this: HTMLDialogElement) {
    this.open = true;
  };
  HTMLDialogElement.prototype.close ??= function (this: HTMLDialogElement) {
    this.open = false;
  };
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  canvas.length = 0;
});

const exec = execFixture as unknown as S<"RowExecution">;
const batch = batchFixture as unknown as S<"Batch">;
type Step = S<"RowExecution">["steps"][number];

/** The fixture's judge (step 4, its verdict) with its submit and wait before it and its route after. */
function judged(): S<"RowExecution"> {
  const [inputs, submit, wait, verdict] = exec.steps;
  const part = (step: number, p: Step["part"], over: Partial<Step> = {}): Step => ({
    ...verdict,
    step,
    part: p,
    decision: undefined,
    memory: undefined,
    what: `judge ${p}`,
    changed: [],
    status: "finished",
    ...over,
  });
  return {
    ...exec,
    steps: [
      inputs,
      submit,
      wait,
      part(4, "submit"),
      part(5, "wait"),
      { ...verdict, step: 6, status: "finished" },
      part(7, "route", { changed: ["judge.picked"] }),
      part(8, "apply", { node: "rewrite", status: "current" }),
    ],
  };
}

describe("Execution", () => {
  it("opens Rename and All pipelines from the title, and renames the pipeline", async () => {
    answer("GET", { "/v1/studio/hosts": () => ({ data: { hosts: [] } }) });
    const patch = vi.spyOn(api, "PATCH").mockResolvedValue({
      data: { ...pipelineFixture, title: "Night fox" },
      error: undefined,
      response: new Response(null, { status: 200 }),
    } as never);
    render(<Execution batch={{ ...batch, pipeline_id: pipelineFixture.id }} initial={exec} />);
    const items = screen.getAllByRole("menuitem", { hidden: true }).map((m) => m.textContent);
    expect(items[0]).toBe("Rename");
    expect(items.at(-1)).toBe("All pipelines");
    fireEvent.click(screen.getByRole("menuitem", { name: "Rename", hidden: true }));
    const input = screen.getByRole("textbox", { name: "Title", hidden: true });
    fireEvent.change(input, { target: { value: "Night fox" } });
    fireEvent.submit(input.closest("form")!);
    await waitFor(() => expect(screen.getByRole("button", { name: /Night fox/ })).toBeTruthy());
    expect(patch.mock.calls[0][1]).toMatchObject({ body: { title: "Night fox" } });
  });

  it("folds a judge's checkpoints under one line that opens on click", () => {
    answer("GET", { "/v1/studio/hosts": () => ({ data: { hosts: [] } }) });
    const { container } = render(<Execution batch={batch} initial={judged()} />);
    const names = () =>
      [...container.querySelectorAll("tbody tr td:nth-child(2)")].map((t) => t.textContent);
    expect(names()).toEqual(["Inputs", "Step 1 · submit", "Step 1 · wait", "Judge", "Write"]);
    const head = screen.getByRole("button", { name: /^Judge, steps 4 to 7/ });
    expect(head.textContent).toBe("4–7");
    expect(head.getAttribute("aria-expanded")).toBe("false");
    fireEvent.click(head);
    expect(names()).toEqual([
      "Inputs",
      "Step 1 · submit",
      "Step 1 · wait",
      "Judge",
      "submit",
      "wait",
      "verdict · memory read 1",
      "route",
      "Write",
    ]);
    // each checkpoint is still a step to pick (and to retry from)
    fireEvent.click(screen.getByRole("button", { name: "Step 5: wait" }));
    expect(screen.getByText(/Judge · wait · step 5/)).toBeTruthy();
  });

  it("keeps saved positions unless cards overlap or sit off-screen", () => {
    const at = (x: number, y: number) => ({ x, y });
    const placed = (ps: { x: number; y: number }[]) =>
      ({
        ...exec.pipeline,
        nodes: Object.fromEntries(
          Object.entries(exec.pipeline.nodes).map(([id, n], i) => [id, { ...n, position: ps[i] }]),
        ),
      }) as PDoc;
    const grid = Object.keys(exec.pipeline.nodes).map((_, i) => at((i % 4) * 220, (i >> 2) * 200));
    expect(needsLayout(exec.pipeline as PDoc)).toBe(true); // none saved
    expect(needsLayout(placed(grid))).toBe(false);
    expect(needsLayout(placed([grid[0], at(40, 30), ...grid.slice(2)]))).toBe(
      true, // the first two overlap
    );
    expect(needsLayout(placed([...grid.slice(0, -1), at(9000, 0)]))).toBe(true); // off-screen
    answer("GET", { "/v1/studio/hosts": () => ({ data: { hosts: [] } }) });
    render(<Execution batch={batch} initial={{ ...exec, pipeline: placed(grid) }} />);
    expect(canvas.at(-1)!.doc.nodes.judge.position).toEqual(grid[3]);
  });
});

describe("Pipelines panel", () => {
  it("shows Run, Open graph and Duplicate; Rename and Delete in the ⋯ menu", () => {
    answer("GET", {});
    render(
      <Pipelines
        initial={{ batches: [], next_cursor: null }}
        pipelines={[pipelineFixture as unknown as S<"Pipeline">]}
        workflows={[]}
      />,
    );
    const aside = screen.getByRole("complementary", { name: "Selected pipeline" });
    const shown = within(aside)
      .getAllByRole("button")
      .concat(within(aside).getAllByRole("link"))
      .map((b) => b.textContent);
    expect(shown).toEqual(expect.arrayContaining(["Run", "Duplicate", "Open graph"]));
    expect(shown).not.toContain("Rename");
    const menu = within(aside).getByRole("menu", { hidden: true });
    expect(
      within(menu)
        .getAllByRole("menuitem", { hidden: true })
        .map((m) => m.textContent),
    ).toEqual(["Rename", "Delete"]);
    fireEvent.click(within(menu).getByRole("menuitem", { name: "Rename", hidden: true }));
    expect(screen.getByRole("dialog", { name: "Rename pipeline", hidden: true })).toBeTruthy();
    expect(screen.getByRole("button", { name: "New pipeline" })).toBeTruthy();
  });
});
