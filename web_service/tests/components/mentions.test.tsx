// @vitest-environment jsdom
// `@` mentions in the prompt (components/create/prompt.tsx; docs/mock/Elements.dc.html): the menu at the caret from
// GET /v1/studio/library?kind=element&q=…, keyboard choice and insertion, Escape, and unknown handles marked.
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { useState } from "react";
import { MentionChips, MentionText, PromptArea } from "@/components/create/prompt";
import type { S } from "@/lib/api";
import { answer } from "./helpers";

beforeAll(() => {
  HTMLElement.prototype.showPopover ??= function (this: HTMLElement) {
    this.dataset.open = "1";
  };
  HTMLElement.prototype.hidePopover ??= function (this: HTMLElement) {
    delete this.dataset.open;
  };
  // jsdom has no editing commands: the area falls back to setting the value
  document.execCommand ??= () => false;
  globalThis.requestAnimationFrame ??= (f: FrameRequestCallback) =>
    setTimeout(() => f(0), 0) as unknown as number;
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

const el = (handle: string, name: string): S<"LibraryItem"> =>
  ({
    id: `id-${handle}`,
    kind: "element",
    handle,
    name,
    group: "character",
    thumb_url: null,
  }) as unknown as S<"LibraryItem">;
const library = [el("fox", "Red fox"), el("forest", "Pine forest")];

function serve() {
  const queries: unknown[] = [];
  answer("GET", {
    "/v1/studio/library": (init) => {
      const q = (init.params as { query: { q?: string } }).query;
      queries.push(q);
      const items = q.q == null ? library : library.filter((x) => x.handle!.startsWith(q.q!));
      return { data: { items } };
    },
  });
  return queries;
}

function Area({ initial = "" }: { initial?: string }) {
  const [v, setV] = useState(initial);
  return (
    <>
      <PromptArea aria-label="Prompt" value={v} onChange={setV} />
      <output data-testid="value">{v}</output>
    </>
  );
}

/** Types `text` as the whole value with the caret at its end. */
function type(text: string) {
  const ta = screen.getByRole("combobox") as HTMLTextAreaElement;
  ta.focus();
  fireEvent.change(ta, { target: { value: text, selectionStart: text.length } });
  ta.setSelectionRange(text.length, text.length);
  fireEvent.select(ta);
  return ta;
}

describe("PromptArea: the @ menu", () => {
  it("searches the handle typed and inserts the one chosen with Enter", async () => {
    const queries = serve();
    render(<Area />);
    const ta = type("a @fo");
    const menu = document.getElementById(ta.getAttribute("aria-controls")!)!;
    expect(menu.getAttribute("role")).toBe("listbox");
    await waitFor(() =>
      expect(within(menu).getAllByRole("option", { hidden: true })).toHaveLength(2),
    );
    expect(queries).toContainEqual({ kind: "element", q: "fo", limit: 8 });
    expect(ta.getAttribute("aria-expanded")).toBe("true");
    fireEvent.keyDown(ta, { key: "ArrowDown" });
    expect(ta.getAttribute("aria-activedescendant")).toBe(
      within(menu).getAllByRole("option", { hidden: true })[1].id,
    );
    fireEvent.keyDown(ta, { key: "Enter" });
    await waitFor(() => expect(screen.getByTestId("value").textContent).toBe("a @forest "));
  });

  it("Escape closes the menu for that token only", async () => {
    serve();
    render(<Area />);
    const ta = type("@f");
    await waitFor(() => expect(ta.getAttribute("aria-expanded")).toBe("true"));
    fireEvent.keyDown(ta, { key: "Escape" });
    expect(ta.getAttribute("aria-expanded")).toBe("false");
    // a new token opens it again
    type("@f and @fo");
    await waitFor(() => expect(ta.getAttribute("aria-expanded")).toBe("true"));
  });

  it("says when no element starts with the query; @@ opens nothing", async () => {
    serve();
    render(<Area />);
    const ta = type("@zz");
    await waitFor(() =>
      expect(screen.getByText("No element starts with @zz.", { exact: true })).toBeTruthy(),
    );
    type("@@fox");
    await act(() => new Promise((r) => setTimeout(r, 120)));
    expect(ta.getAttribute("aria-expanded")).toBe("false");
  });

  it("marks a mention the library does not know, once the library is in", async () => {
    serve();
    const { container } = render(<Area initial="the @fox meets @wolf" />);
    await waitFor(() =>
      expect(container.querySelector(".mention-unknown")?.textContent).toBe("@wolf"),
    );
    const chips = [...container.querySelectorAll(".mention-chip")].map((x) => x.textContent);
    expect(chips).toEqual(["@fox", "@wolf"]);
  });
});

describe("MentionChips and MentionText", () => {
  it("draws each handle once and marks the unknown one", async () => {
    serve();
    render(
      <div data-testid="chips">
        <MentionChips texts={["@fox runs", "@FOX and @wolf", "mail a@b.c, @@fox"]} />
      </div>,
    );
    const box = screen.getByTestId("chips");
    await waitFor(() => expect(within(box).getByText("@wolf").className).toContain("text-bad"));
    expect([...box.children].map((x) => x.textContent)).toEqual(["@fox", "@wolf"]);
    expect(within(box).getByText("@wolf").getAttribute("title")).toBe(
      "No element @wolf: make it in Elements, or write @@wolf for the text",
    );
  });

  it("keeps the text as written around the chips", async () => {
    serve();
    const { container } = render(
      <p>
        <MentionText text="a @Fox, @@lit" />
      </p>,
    );
    expect(container.textContent).toBe("a @Fox, @@lit");
    await waitFor(() => expect(container.querySelector(".mention-chip")?.textContent).toBe("@Fox"));
    expect(container.querySelector(".mention-unknown")).toBeNull();
  });
});
