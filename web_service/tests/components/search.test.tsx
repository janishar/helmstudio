// @vitest-environment jsdom
// SearchField and useSearch (components/search.tsx; docs/mock/Search.dc.html frame 0): every word in any order, the
// count, Esc clears then closes, Enter opens the ringed row, ↑ ↓ move it, / focuses the field (never while typing),
// the server 300 ms after the last key with a late answer dropped, and its matches not loaded under "From older".
import { afterEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import {
  Marked,
  NoMatch,
  Older,
  SearchField,
  matches,
  useSearch,
  type Found,
} from "@/components/search";

afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

type Row = { id: string; title: string };
const rows = (...t: string[]): Row[] => t.map((title, i) => ({ id: `r${i}`, title }));

function List({
  items,
  server,
  onPick = () => {},
  onClose,
}: {
  items: Row[];
  server?: (q: string, c: string | null) => Promise<Found<Row>>;
  onPick?: (r: Row) => void;
  onClose?: () => void;
}) {
  const s = useSearch({ items, text: (r) => [r.title], id: (r) => r.id, server, onPick });
  return (
    <div>
      {s.show && <SearchField {...s.field} label="Search sessions" onClose={onClose} />}
      {s.shown.map((r) => (
        <div key={r.id} data-row className={s.rows[s.active] === r ? "sf-on" : ""}>
          <Marked text={r.title} q={s.q} />
        </div>
      ))}
      {s.q && !s.rows.length && !s.searching && (
        <NoMatch what="sessions" onClear={() => s.setQ("")} />
      )}
      {s.q && server && (s.searching || s.older.length > 0) && (
        <Older
          heading="From older sessions"
          what="older sessions"
          searching={s.searching}
          more={s.olderNext}
          onMore={s.moreOlder}
        >
          {s.older.map((r) => (
            <div key={r.id} data-older className={s.rows[s.active] === r ? "sf-on" : ""}>
              {r.title}
            </div>
          ))}
        </Older>
      )}
    </div>
  );
}

const nine = rows(
  "Fox at dusk",
  "Snow owl",
  "Arctic fox run",
  "Mara, rooftop",
  "e",
  "f",
  "g",
  "h",
  "i",
);
const box = () => screen.getByRole("searchbox", { name: "Search sessions" }) as HTMLInputElement;

describe("matching", () => {
  it("needs every word, any order, any case", () => {
    expect(matches("rooftop mara", ["Mara, rooftop"])).toBe(true);
    expect(matches("mara bus", ["Mara, rooftop"])).toBe(false);
    expect(matches("  ", ["x"])).toBe(true);
    expect(matches("mara", [null, "@mara"])).toBe(true);
  });
  it("marks each word", () => {
    const { container } = render(<Marked text="Arctic fox (run)" q="fox ( " />);
    expect([...container.querySelectorAll("mark")].map((m) => m.textContent)).toEqual(["fox", "("]);
  });
});

describe("SearchField", () => {
  it("shows past 8 rows only, filters at once, counts and marks", () => {
    const { unmount } = render(<List items={nine.slice(0, 8)} />);
    expect(screen.queryByRole("search")).toBeNull();
    unmount();
    render(<List items={nine} />);
    expect(screen.getByText("/")).toBeTruthy(); // the hint while empty
    fireEvent.change(box(), { target: { value: "FOX" } });
    expect(screen.getAllByText((_, el) => el?.hasAttribute("data-row") ?? false)).toHaveLength(2);
    expect(screen.getByText("2")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Clear search" })).toBeTruthy();
    expect(document.querySelectorAll("mark")).toHaveLength(2);
    fireEvent.change(box(), { target: { value: "zebra" } });
    expect(screen.getByText("No sessions match.")).toBeTruthy();
    fireEvent.click(screen.getByText("Clear search"));
    expect(box().value).toBe("");
    expect(document.querySelectorAll("[data-row]")).toHaveLength(9);
  });

  it("Enter opens the ringed row, arrows move it, Esc clears then closes", () => {
    const pick = vi.fn();
    const close = vi.fn();
    render(<List items={nine} onPick={pick} onClose={close} />);
    fireEvent.keyDown(box(), { key: "Enter" });
    expect(pick).not.toHaveBeenCalled(); // nothing typed, nothing ringed
    fireEvent.change(box(), { target: { value: "o" } }); // Fox, Snow owl, Arctic fox, rooftop
    expect(document.querySelector(".sf-on")?.textContent).toBe("Fox at dusk");
    fireEvent.keyDown(box(), { key: "ArrowDown" });
    fireEvent.keyDown(box(), { key: "ArrowDown" });
    expect(document.querySelector(".sf-on")?.textContent).toBe("Arctic fox run");
    fireEvent.keyDown(box(), { key: "ArrowUp" });
    fireEvent.keyDown(box(), { key: "Enter" });
    expect(pick).toHaveBeenCalledWith(nine[1]);
    fireEvent.keyDown(box(), { key: "Escape" });
    expect(box().value).toBe("");
    expect(close).not.toHaveBeenCalled();
    fireEvent.keyDown(box(), { key: "Escape" });
    expect(close).toHaveBeenCalledTimes(1);
  });

  it("/ focuses the field, never while typing in another", () => {
    render(
      <>
        <textarea aria-label="Prompt" />
        <SearchField value="" onChange={() => {}} label="Search boards" />
      </>,
    );
    const field = screen.getByRole("searchbox", { name: "Search boards" });
    // jsdom draws nothing: every element counts as shown
    vi.spyOn(field, "getClientRects").mockReturnValue([{}] as unknown as DOMRectList);
    const prompt = screen.getByLabelText("Prompt");
    prompt.focus();
    fireEvent.keyDown(prompt, { key: "/" });
    expect(document.activeElement).toBe(prompt);
    fireEvent.keyDown(document.body, { key: "/" });
    expect(document.activeElement).toBe(field);
  });
});

describe("the server", () => {
  it("asks 300 ms after the last key, drops a late answer, and puts unloaded matches under From older", async () => {
    vi.useFakeTimers();
    const answers: Record<string, (v: Found<Row>) => void> = {};
    const server = vi.fn(
      (q: string, c: string | null) =>
        new Promise<Found<Row>>((res) => (answers[`${q}|${c ?? ""}`] = res)),
    );
    render(<List items={nine} server={server} />);
    fireEvent.change(box(), { target: { value: "fo" } });
    act(() => void vi.advanceTimersByTime(200));
    fireEvent.change(box(), { target: { value: "fox" } });
    act(() => void vi.advanceTimersByTime(299));
    expect(server).not.toHaveBeenCalled();
    act(() => void vi.advanceTimersByTime(1));
    expect(server).toHaveBeenCalledTimes(1);
    expect(server).toHaveBeenLastCalledWith("fox", null);
    expect(screen.getByText("Searching older sessions…")).toBeTruthy();
    fireEvent.change(box(), { target: { value: "fox cub" } });
    act(() => void vi.advanceTimersByTime(300));
    // the answer for "fox" comes late: dropped
    await act(async () =>
      answers["fox|"]({ items: [{ id: "old1", title: "Fox stale" }], next: null }),
    );
    expect(screen.queryByText("Fox stale")).toBeNull();
    await act(async () =>
      answers["fox cub|"]({
        items: [nine[0], { id: "old2", title: "Fox cub in snow" }],
        next: "c1",
      }),
    );
    expect(screen.getByText("From older sessions")).toBeTruthy();
    expect([...document.querySelectorAll("[data-older]")].map((e) => e.textContent)).toEqual([
      "Fox cub in snow",
    ]);
    fireEvent.click(screen.getByRole("button", { name: "Load more matches" }));
    expect(server).toHaveBeenLastCalledWith("fox cub", "c1");
    await act(async () =>
      answers["fox cub|c1"]({ items: [{ id: "old3", title: "Fox cub den" }], next: null }),
    );
    expect(document.querySelectorAll("[data-older]")).toHaveLength(2);
    expect(screen.queryByRole("button", { name: "Load more matches" })).toBeNull();
  });
});
