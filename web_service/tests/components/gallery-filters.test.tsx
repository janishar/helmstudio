// @vitest-environment jsdom
// The Gallery's search and chips (components/gallery/gallery.tsx; docs/mock/Gallery.dc.html): the placeholder of the
// wider search (API 1.27), "This project only" (the session Create opened last, kept in the browser) and the Pipelines
// list's Open collection: a clearable "Batch · <title>" chip that sends batch_id.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { S } from "@/lib/api";
import { rememberProject } from "@/lib/takes";
import { answer, take } from "./helpers";

vi.mock("next/navigation", () => ({
  useRouter: () => ({ push: vi.fn(), replace: vi.fn(), refresh: vi.fn() }),
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

const SESSION = "11111111-2222-4333-8444-555555555555";
const BATCH = "22222222-3333-4444-8555-666666666666";
const page = (...takes: S<"Take">[]): S<"TakePage"> => ({ takes, next_cursor: null });

/** Serves the takes list; returns the query of every list call. */
function serve() {
  const queries: Record<string, unknown>[] = [];
  answer("GET", {
    "/v1/studio/takes": (init) => {
      queries.push((init.params as { query: Record<string, unknown> }).query);
      return { data: page() };
    },
    "/v1/studio/takes/{take_id}": () => ({ data: take() }),
    "/v1/studio/takes/{take_id}/uses": () => ({ data: { uses: [] } }),
  });
  return queries;
}

beforeEach(() => localStorage.clear());
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("Gallery filters", () => {
  it("searches prompts, elements and models", () => {
    serve();
    render(<Gallery initial={page(take())} />);
    expect(screen.getByRole("searchbox").getAttribute("placeholder")).toBe(
      "Search prompts, elements, models",
    );
  });

  it("This project only waits for a session opened in Create", () => {
    serve();
    render(<Gallery initial={page(take())} />);
    const chip = screen.getByRole("button", { name: "This project only" }) as HTMLButtonElement;
    expect(chip.disabled).toBe(true);
  });

  it("This project only filters to the session Create opened last", async () => {
    rememberProject(SESSION);
    const queries = serve();
    render(<Gallery initial={page(take())} />);
    fireEvent.click(screen.getByRole("button", { name: "This project only" }));
    await waitFor(() => expect(queries.at(-1)).toMatchObject({ session_id: SESSION }));
    expect(
      screen.getByRole("button", { name: "This project only" }).getAttribute("aria-pressed"),
    ).toBe("true");
    fireEvent.click(screen.getByRole("button", { name: "All" }));
    await waitFor(() => expect(queries.at(-1)).not.toHaveProperty("session_id"));
  });

  it("a batch's collection shows a clearable chip and clearing it lists everything", async () => {
    const queries = serve();
    render(<Gallery initial={page(take())} batch={{ id: BATCH, title: "Shot list × 14" }} />);
    // the server's first page stands for the batch: no list call until the filter changes
    expect(queries).toHaveLength(0);
    expect(screen.getByRole("button", { name: "All" }).getAttribute("aria-pressed")).toBe("false");
    const chip = screen.getByRole("button", { name: "Clear batch Shot list × 14" });
    expect(chip.textContent).toBe("Batch · Shot list × 14");
    fireEvent.click(screen.getByRole("button", { name: "Starred" }));
    await waitFor(() => expect(queries.at(-1)).toMatchObject({ batch_id: BATCH, starred: true }));
    fireEvent.click(chip);
    await waitFor(() => expect(queries.at(-1)).not.toHaveProperty("batch_id"));
    expect(screen.queryByRole("button", { name: /Clear batch/ })).toBeNull();
  });

  it("Voice, Music and Sound in place of Audio send the class (API 1.79, CreateMusicSound frame 6)", async () => {
    const queries = serve();
    render(<Gallery initial={page(take())} />);
    const chips = screen.getAllByRole("button").map((b) => b.textContent);
    expect(chips).toEqual(expect.arrayContaining(["Images", "Video", "Voice", "Music", "Sound"]));
    expect(chips).not.toContain("Audio");
    fireEvent.click(screen.getByRole("button", { name: "Music" }));
    await waitFor(() => expect(queries.at(-1)).toMatchObject({ kind: "music" }));
    fireEvent.click(screen.getByRole("button", { name: "Sound" }));
    await waitFor(() => expect(queries.at(-1)).toMatchObject({ kind: "sound" }));
    expect(screen.getByRole("button", { name: "Music" }).getAttribute("aria-pressed")).toBe(
      "false",
    );
  });
});
