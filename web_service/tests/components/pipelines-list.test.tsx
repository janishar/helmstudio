// @vitest-environment jsdom
// The Pipelines list (components/pipelines/pipelines.tsx; docs/mock/Pipelines.dc.html): card tags led by the agent
// nodes ("No agent nodes"), the aside's slug · version · author · date line and its (i) naming the running batch, the
// 4 newest batches until All batches, Retry failed on a batch with failed rows, and Open collection on a done one.
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import pipelineFixture from "../../../contracts/fixtures/http/Pipeline.valid.1.json";
import agentsFixture from "../../../contracts/fixtures/http/Pipeline.valid.3.json";
import batchFixture from "../../../contracts/fixtures/http/Batch.valid.3.json";
import { Pipelines } from "@/components/pipelines/pipelines";
import type { S } from "@/lib/api";
import { answer } from "./helpers";

vi.mock("next/navigation", () => ({ useRouter: () => ({ push: vi.fn(), replace: vi.fn() }) }));
vi.mock("next/link", () => ({
  default: ({ href, children, ...rest }: { href: string; children: React.ReactNode }) => (
    <a href={href} {...rest}>
      {children}
    </a>
  ),
}));
vi.mock("@/components/shell", () => ({ useUser: () => ({ name: "Ada" }) }));
// an unfinished batch is followed live: a stream that never speaks
vi.stubGlobal(
  "EventSource",
  class {
    addEventListener() {}
    close() {}
  },
);

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

const plain = pipelineFixture as unknown as S<"Pipeline">;
const agents = agentsFixture as unknown as S<"Pipeline">;
const base = batchFixture as unknown as S<"Batch">;
const batch = (i: number, over: Partial<S<"Batch">> = {}): S<"Batch"> => ({
  ...base,
  id: `00000000-0000-4000-8000-00000000000${i}`,
  title: `Batch ${i}`,
  status: "done",
  counts: { ...base.counts, waiting: 0, failed: 0 },
  ...over,
});

function show(batches: S<"Batch">[]) {
  return render(
    <Pipelines
      initial={{ batches, next_cursor: null }}
      pipelines={[plain, agents]}
      workflows={[]}
    />,
  );
}

describe("Pipelines list", () => {
  it("leads the card tags with the agent nodes and drops the step and gate counts", () => {
    answer("GET", {});
    show([]);
    const tags = [...document.querySelectorAll(".tag")].map((t) => t.textContent);
    expect(tags).toContain("No agent nodes");
    expect(tags.some((t) => /^\d+ agent nodes?$/.test(t ?? ""))).toBe(true);
    expect(tags.some((t) => /steps?$|gates?$/.test(t ?? ""))).toBe(false);
  });

  it("shows the aside's collection, version and author, and names the running batch", async () => {
    answer("GET", {});
    show([]);
    const aside = screen.getByRole("complementary", { name: "Selected pipeline" });
    await waitFor(() => expect(aside.textContent).toMatch(/Fox at night · v3 · Ada · /));
    expect(aside.textContent).toContain(`Running batch ${plain.last_batch!.title} keeps`);
  });

  it("shows the 4 newest batches until All batches", () => {
    answer("GET", {});
    show([1, 2, 3, 4, 5, 6].map((i) => batch(i)));
    const table = screen.getByRole("table");
    expect(within(table).getAllByRole("row")).toHaveLength(1 + 4);
    fireEvent.click(screen.getByRole("button", { name: "All batches" }));
    expect(within(table).getAllByRole("row")).toHaveLength(1 + 6);
  });

  it("retries a batch's failed rows from its line", async () => {
    const failed = batch(1, {
      status: "paused",
      counts: { ...base.counts, waiting: 0, failed: 2 },
    });
    const rows = [1, 2, 3].map((row) => ({ row, status: row === 2 ? "succeeded" : "failed" }));
    const get = answer("GET", {
      "/v1/studio/batches/{batch_id}/rows": () => ({ data: { rows, next_cursor: null } }),
      "/v1/studio/batches/{batch_id}": () => ({ data: { ...failed, status: "running" } }),
    });
    const post = answer("POST", {
      "/v1/studio/batches/{batch_id}/rows/{row}/retry": () => ({ status: 202, data: {} }),
    });
    show([failed, batch(2)]);
    fireEvent.click(screen.getByRole("button", { name: "Retry failed" }));
    await waitFor(() => expect(screen.getByText("Running")).toBeTruthy());
    const retried = post.mock.calls.map(
      (c) => (c[1] as { params: { path: { row: string } } }).params.path.row,
    );
    expect(retried).toEqual(["1", "3"]);
    expect(get).toHaveBeenCalledWith("/v1/studio/batches/{batch_id}/rows", expect.anything());
    // a done batch with no failed rows has no Retry failed
    expect(screen.getAllByRole("button", { name: "Retry failed" })).toHaveLength(1);
  });

  it("opens a done batch's collection in the Gallery; an unfinished one opens its run view", () => {
    answer("GET", {});
    show([batch(1), batch(2, { status: "running" })]);
    const [done, running] = within(screen.getByRole("table")).getAllByRole("row").slice(1);
    const link = within(done).getByRole("link", { name: "Open collection" });
    expect(link.getAttribute("href")).toBe(`/gallery?batch=${batch(1).id}`);
    expect(within(done).queryByRole("link", { name: "Open" })).toBeNull();
    expect(within(running).getByRole("link", { name: "Open" }).getAttribute("href")).toBe(
      `/pipelines/batches/${batch(2).id}`,
    );
  });
});
