// @vitest-environment jsdom
// "Last frame in…" / "First frame in…" (components/create/frame-in.tsx; docs/mock/H3Options.dc.html frame 7): the
// frame take is made, then its Use in… offers open Create with the frame in that workflow.
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { FrameIn } from "@/components/create/frame-in";
import { answer } from "./helpers";

vi.mock("next/link", () => ({
  default: ({ href, children, ...rest }: { href: string; children: React.ReactNode }) => (
    <a href={href} {...rest}>
      {children}
    </a>
  ),
}));

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("FrameIn", () => {
  it("makes the last frame, then links its offers", async () => {
    const asked: unknown[] = [];
    answer("POST", {
      "/v1/studio/takes/{take_id}/frame": (init) => {
        asked.push(init);
        return { data: { take: { id: "frame-1" } } };
      },
    });
    answer("GET", {
      "/v1/studio/takes/{take_id}/uses": () => ({
        data: {
          uses: [
            {
              workflow_id: "minimax-h3-image-to-video",
              title: "MiniMax H3 Image to video",
              family: "minimax-h3",
              media: "video",
              param: "first.image",
              inputs: {},
            },
          ],
        },
      }),
    });
    render(<FrameIn takeId="video-1" href={(id, o) => `/?use=${id}&w=${o.workflow_id}`} />);
    fireEvent.click(screen.getByRole("button", { name: "Last frame in…" }));
    const link = await screen.findByRole("menuitem");
    expect(link.getAttribute("href")).toBe("/?use=frame-1&w=minimax-h3-image-to-video");
    expect(asked).toEqual([{ params: { path: { take_id: "video-1" } }, body: { at: "last" } }]);
  });

  it("says why when the frame cannot be made", async () => {
    answer("POST", {
      "/v1/studio/takes/{take_id}/frame": () => ({
        status: 409,
        error: { detail: "no live host can export (ffmpeg)" },
      }),
    });
    const onUse = vi.fn();
    render(<FrameIn takeId="video-1" onUse={onUse} />);
    fireEvent.click(screen.getByRole("button", { name: "First frame in…" }));
    await waitFor(() =>
      expect(screen.getByRole("alert").textContent).toBe("no live host can export (ffmpeg)"),
    );
    expect(onUse).not.toHaveBeenCalled();
  });
});
