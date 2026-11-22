// @vitest-environment jsdom
// The prompt's Expand (components/create/prompt.tsx; docs/mock/CreateAudio.dc.html "The prompt, expanded"): the
// corner button while the text overflows, a dialog over the same value, its close ways returning the focus, and
// `@` mentions inside it.
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { useState } from "react";
import { PromptArea } from "@/components/create/prompt";
import type { S } from "@/lib/api";
import { answer } from "./helpers";

beforeAll(() => {
  HTMLElement.prototype.showPopover ??= () => undefined;
  HTMLElement.prototype.hidePopover ??= () => undefined;
  document.execCommand ??= () => false;
  globalThis.requestAnimationFrame ??= (f: FrameRequestCallback) =>
    setTimeout(() => f(0), 0) as unknown as number;
  HTMLDialogElement.prototype.showModal ??= function (this: HTMLDialogElement) {
    this.open = true;
  };
  HTMLDialogElement.prototype.close ??= function (this: HTMLDialogElement) {
    this.open = false;
    this.dispatchEvent(new Event("close"));
  };
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

function Area({ initial = "" }: { initial?: string }) {
  const [v, setV] = useState(initial);
  return (
    <>
      <PromptArea
        aria-label="Prompt"
        expand="Words to speak"
        maxLength={4000}
        value={v}
        onChange={setV}
      />
      <output data-testid="value">{v}</output>
    </>
  );
}

const prompt = () => screen.getByRole("combobox", { name: "Prompt" }) as HTMLTextAreaElement;
const expand = () => screen.getByRole("button", { name: "Expand" });

function open() {
  fireEvent.click(expand());
  return screen.getByRole("combobox", { name: "Words to speak" }) as HTMLTextAreaElement;
}

describe("PromptArea: Expand", () => {
  it("shows the button only while the text overflows (or on focus)", () => {
    vi.spyOn(HTMLTextAreaElement.prototype, "scrollHeight", "get").mockReturnValue(0);
    const { unmount } = render(<Area initial="short" />);
    expect(expand().className).toContain("opacity-0");
    unmount();
    vi.spyOn(HTMLTextAreaElement.prototype, "scrollHeight", "get").mockReturnValue(300);
    vi.spyOn(HTMLTextAreaElement.prototype, "clientHeight", "get").mockReturnValue(44);
    render(<Area initial={"a long script\n".repeat(20)} />);
    expect(expand().className).not.toContain("opacity-0");
  });

  it("edits the same value, with its count, and Esc, ✕ and Done close it back to the prompt", async () => {
    render(<Area initial="hello" />);
    const big = open();
    expect(big.value).toBe("hello");
    expect(document.activeElement).toBe(big);
    fireEvent.change(big, { target: { value: "hello world", selectionStart: 11 } });
    expect(screen.getByTestId("value").textContent).toBe("hello world");
    expect(prompt().value).toBe("hello world");
    expect(screen.getByText("11 / 4000")).toBeTruthy();
    const dialog = () => screen.queryByRole("dialog", { hidden: true }) as HTMLDialogElement | null;

    fireEvent.click(screen.getByRole("button", { name: "Done" }));
    expect(dialog()).toBeNull();
    await waitFor(() => expect(document.activeElement).toBe(prompt()));

    open();
    fireEvent.click(within(dialog()!).getByRole("button", { name: "Close" }));
    expect(dialog()).toBeNull();
    await waitFor(() => expect(document.activeElement).toBe(prompt()));

    // Escape: the browser cancels the dialog, which closes it
    open();
    act(() => dialog()!.close());
    expect(dialog()).toBeNull();
    await waitFor(() => expect(document.activeElement).toBe(prompt()));
  });

  it("offers @ mentions in the dialog", async () => {
    const fox = {
      id: "id-fox",
      kind: "element",
      handle: "fox",
      name: "Red fox",
      group: "character",
    } as unknown as S<"LibraryItem">;
    answer("GET", { "/v1/studio/library": () => ({ data: { items: [fox] } }) });
    render(<Area />);
    const big = open();
    big.focus();
    fireEvent.change(big, { target: { value: "a @fo", selectionStart: 5 } });
    big.setSelectionRange(5, 5);
    fireEvent.select(big);
    await waitFor(() => expect(big.getAttribute("aria-expanded")).toBe("true"));
    fireEvent.keyDown(big, { key: "Enter" });
    await waitFor(() => expect(screen.getByTestId("value").textContent).toBe("a @fox "));
  });
});
