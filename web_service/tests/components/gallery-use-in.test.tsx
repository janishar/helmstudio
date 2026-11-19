// @vitest-environment jsdom
// The Gallery's side panel "Use in…" (components/gallery/gallery.tsx TakePanel; docs/mock/Gallery.dc.html): the
// offers of GET …/takes/{id}/uses as links that open Create with the take in that workflow.
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import type { S } from "@/lib/api";
import { answer, take, video } from "./helpers";

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

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

const offer = (over: Partial<S<"TakeOffer">>): S<"TakeOffer"> => ({
  workflow_id: "wf",
  title: "Image to video",
  family: "minimax-h3",
  media: "video",
  param: "first.image",
  inputs: {},
  ...over,
});

function serve(uses: Record<string, S<"TakeOffer">[]>) {
  return answer("GET", {
    "/v1/studio/takes/{take_id}/uses": (init) => {
      const id = (init.params as { path: { take_id: string } }).path.take_id;
      return { data: { uses: uses[id] ?? [] } };
    },
  });
}

const page = (...takes: S<"Take">[]): S<"TakePage"> => ({ takes, next_cursor: null });
const useIn = () => screen.findByRole("heading", { name: /Use in…/ });

describe("Gallery: Use in…", () => {
  it("lists an image take's offers as links to Create, the first one lit", async () => {
    const t = take({ session_id: "11111111-2222-4333-8444-555555555555" });
    serve({
      [t.id]: [
        offer({ workflow_id: "minimax-h3-i2v", action: "Animate" }),
        offer({
          workflow_id: "minimax-h3-refs",
          title: "References to video",
          param: "generate.refs",
        }),
      ],
    });
    render(<Gallery initial={page(t)} />);
    const section = (await useIn()).parentElement!;
    const links = within(section).getAllByRole("link");
    expect(links.map((a) => a.textContent)).toEqual([
      "Animate · Image to video",
      "References to video",
    ]);
    expect(links[0].getAttribute("href")).toBe(`/?s=${t.session_id}&use=${t.id}&w=minimax-h3-i2v`);
    expect(links[1].getAttribute("href")).toBe(`/?s=${t.session_id}&use=${t.id}&w=minimax-h3-refs`);
    expect(links[0].className).toContain("bg-a1");
    expect(links[1].className).not.toContain("bg-a1");
  });

  it("an upload has no session in the link; a video's first offer is not lit", async () => {
    const v = video(3, 24, { source: "upload" });
    serve({
      [v.id]: [offer({ workflow_id: "ltx-retake", action: "Retake a section", title: "LTX" })],
    });
    render(<Gallery initial={page(v)} />);
    const link = within((await useIn()).parentElement!).getByRole("link");
    expect(link.getAttribute("href")).toBe(`/?use=${v.id}&w=ltx-retake`);
    expect(link.className).not.toContain("bg-a1");
  });

  it("shows no Use in… when nothing fits, and the new take's offers after a switch", async () => {
    const [a, b] = [take(), take()];
    serve({ [b.id]: [offer({ workflow_id: "qwen-edit", title: "Qwen Image Edit" })] });
    render(<Gallery initial={page(a, b)} />);
    await waitFor(() => expect(screen.getByRole("heading", { name: "Uploaded" })).toBeTruthy());
    expect(screen.queryByRole("heading", { name: /Use in…/ })).toBeNull();
    fireEvent.click(screen.getByText(b.title, { selector: "b" }).closest("button")!);
    const link = within((await useIn()).parentElement!).getByRole("link");
    expect(link.getAttribute("href")).toBe(`/?use=${b.id}&w=qwen-edit`);
  });
});
