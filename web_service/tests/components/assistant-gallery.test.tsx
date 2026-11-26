// @vitest-environment jsdom
// The Gallery's screen actions for the assistant (components/gallery/assistant.ts; plan 18 §22.5): search, filter
// and sort send the list's own query (sort is the API 1.44 `order`, shown as a clearable chip); select; star and
// rename with Undo; Save as pipeline opens it; delete is a card and deletes nothing until its Yes.
import { afterEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import { Registry, ToolsContext } from "@/components/assistant/tools";
import { asked, type Asked, type Outcome } from "@/lib/assistant";
import { api, type S } from "@/lib/api";
import { answer, take } from "./helpers";

const push = vi.fn();
vi.mock("next/navigation", () => ({
  useRouter: () => ({ push, replace: vi.fn(), refresh: vi.fn() }),
  usePathname: () => "/gallery",
  useSearchParams: () => new URLSearchParams(),
}));
vi.mock("next/link", () => ({
  default: ({ href, children, ...rest }: { href: string; children: React.ReactNode }) => (
    <a href={href} {...rest}>
      {children}
    </a>
  ),
}));

const { Gallery } = await import("@/components/gallery/gallery");

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  push.mockReset();
});

function start() {
  const a = take({ title: "Rain street" });
  const b = take({ title: "Bus stop", kind: "video" });
  const queries: Record<string, unknown>[] = [];
  const patches: unknown[] = [];
  const deletes: string[] = [];
  answer("GET", {
    "/v1/studio/takes": (init) => {
      queries.push((init.params as { query: Record<string, unknown> }).query);
      return { data: { takes: [a, b], next_cursor: null } };
    },
    "/v1/studio/takes/{take_id}": () => ({ data: a }),
    "/v1/studio/takes/{take_id}/uses": () => ({ data: { uses: [] } }),
  });
  answer("POST", {
    "/v1/studio/takes/{take_id}/pipeline": () => ({
      status: 201,
      data: { id: "p1", title: "From Rain street" },
    }),
  });
  vi.spyOn(api, "PATCH").mockImplementation(((
    _p: string,
    init: { body: object; params: { path: { take_id: string } } },
  ) => {
    patches.push(init.body);
    const t = [a, b].find((x) => x.id === init.params.path.take_id)!;
    return Promise.resolve({
      data: { ...t, ...init.body },
      error: undefined,
      response: new Response(null, { status: 200 }),
    });
  }) as never);
  vi.spyOn(api, "DELETE").mockImplementation(((
    _p: string,
    init: { params: { path: { take_id: string } } },
  ) => {
    deletes.push(init.params.path.take_id);
    return Promise.resolve({
      data: undefined,
      error: undefined,
      response: new Response(null, { status: 204 }),
    });
  }) as never);
  const reg = new Registry();
  reg.setPath("/gallery");
  render(
    <ToolsContext value={reg}>
      <Gallery initial={{ takes: [a, b], next_cursor: null } as S<"TakePage">} />
    </ToolsContext>,
  );
  let n = 0;
  const call = async (name: string, args: Record<string, unknown> = {}) =>
    (await act(() =>
      reg
        .run(
          { id: `11111111-2222-4333-8444-555555555555:1:${n++}`, name, args: args as never },
          () => undefined,
        )
        .then((r) => r.out),
    )) as Outcome | Asked;
  return { a, b, reg, call, queries, patches, deletes };
}
const out = (o: Outcome | Asked) => {
  if (asked(o)) throw new Error("a card");
  return o;
};

describe("the Gallery's actions", () => {
  it("search, filter and sort send the list's query; sort shows a chip; Undo puts them back", async () => {
    const { call, queries, reg } = start();
    expect(reg.current()?.spec.sees).toEqual(["Gallery", "2 shown"]);
    const s = await call("search", { q: "rain" });
    expect(out(s)).toMatchObject({ text: "Searched: rain" });
    expect((screen.getByRole("searchbox") as HTMLInputElement).value).toBe("rain");
    await waitFor(() => expect(queries.at(-1)).toMatchObject({ q: "rain" }));
    await act(async () => void (await out(s).undo!.undo()));
    expect((screen.getByRole("searchbox") as HTMLInputElement).value).toBe("");
    expect(out(await call("filter", { kind: "video", starred: true }))).toMatchObject({
      text: "Filtered: Video · Starred",
    });
    await waitFor(() => expect(queries.at(-1)).toMatchObject({ kind: "video", starred: true }));
    expect(out(await call("filter", { kind: "music" }))).toMatchObject({ text: "Filtered: Music" });
    await waitFor(() => expect(queries.at(-1)).toMatchObject({ kind: "music" }));
    expect(screen.getByRole("button", { name: "Music" }).getAttribute("aria-pressed")).toBe("true");
    expect(out(await call("filter", { kind: "video", starred: true }))).toMatchObject({
      text: "Filtered: Video · Starred",
    });
    expect(out(await call("filter", { source: "nowhere" }))).toMatchObject({ status: "failed" });
    const o = await call("sort", { order: "oldest" });
    expect(out(o)).toMatchObject({ text: "Sorted: Oldest" });
    await waitFor(() => expect(queries.at(-1)).toMatchObject({ order: "oldest", kind: "video" }));
    expect(screen.getByRole("button", { name: "Clear sort Oldest" })).toBeTruthy();
    expect(reg.current()?.spec.sees).toContain("Sort: Oldest");
    expect((reg.summary().fields as unknown as { sort: string }).sort).toBe("oldest");
    await act(async () => void (await out(o).undo!.undo()));
    expect(screen.queryByRole("button", { name: "Clear sort Oldest" })).toBeNull();
  });

  it("select, star and rename with Undo, Save as pipeline opens it", async () => {
    const { a, b, call, patches } = start();
    expect(out(await call("select", { take_ids: [a.id, b.id] }))).toMatchObject({
      text: "Selected 2 takes",
    });
    expect(screen.getByText("2 selected")).toBeTruthy();
    expect(
      out(await call("select", { take_ids: ["00000000-0000-4000-8000-999999999999"] })),
    ).toMatchObject({
      status: "failed",
    });
    const st = await call("star", { take_id: a.id, on: true });
    expect(out(st)).toMatchObject({ text: "Starred Rain street" });
    await act(async () => void (await out(st).undo!.undo()));
    const rn = await call("rename", { take_id: a.id, title: "Wet street" });
    expect(out(rn)).toMatchObject({ text: "Renamed: Wet street" });
    await act(async () => void (await out(rn).undo!.undo()));
    expect(patches).toEqual([
      { starred: true },
      { starred: false },
      { title: "Wet street" },
      { title: "Rain street" },
    ]);
    expect(out(await call("saveAsPipeline", { take_id: a.id }))).toMatchObject({
      text: "Saved as pipeline · From Rain street",
      result: { pipeline_id: "p1" },
    });
    expect(push).toHaveBeenCalledWith("/pipelines/p1");
  });

  it("delete is a card: nothing goes until Yes", async () => {
    const { a, b, call, deletes } = start();
    const o = await call("delete", { take_ids: [a.id, b.id] });
    if (!asked(o)) throw new Error("no card");
    expect(o.card).toMatchObject({
      kind: "delete",
      title: "Delete 2 takes?",
      go: "DELETE 2",
      danger: true,
      rows: [
        ["Goes", "Rain street, Bus stop"],
        ["Stays", "every other take in the Gallery"],
      ],
    });
    expect(o.card.thumbs).toHaveLength(2);
    expect(deletes).toEqual([]);
    expect(await act(() => o.yes())).toMatchObject({ status: "done", text: "Deleted 2 takes" });
    expect(deletes).toEqual([a.id, b.id]);
    await waitFor(() => expect(screen.queryByText("Rain street")).toBeNull());
  });
});
