// @vitest-environment jsdom
// Thumbnails (contracts/README.md "Thumbnails", API 1.48): lists draw thumb_url lazily; the viewer and compare play a
// video with its full-size poster and show an image's thumbnail under it until the full file loads.
import { afterEach, describe, expect, it } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { Media, Thumb } from "@/components/media";
import { Compare } from "@/components/create/compare";
import { audio, take, video } from "./helpers";

afterEach(cleanup);

const small = { thumb_url: "/files/u/x/t.webp" };

describe("thumbnails", () => {
  it("a list tile loads the thumbnail lazily, off the main thread", () => {
    const { container } = render(<Thumb take={take(small)} />);
    const img = container.querySelector("img")!;
    expect(img.getAttribute("src")).toBe("/files/u/x/t.webp");
    expect(img.getAttribute("loading")).toBe("lazy");
    expect(img.getAttribute("decoding")).toBe("async");
  });

  it("the viewer plays a video with its full-size poster, not the thumbnail", () => {
    const v = video(2, 24, small);
    const { container } = render(<Media take={v} url={v.url} className="size-full" />);
    expect(container.querySelector("video")!.getAttribute("poster")).toBe("/files/u/x/p.png");
  });

  it("the viewer paints an image's thumbnail under it until the full file loads", () => {
    const t = take(small);
    render(<Media take={t} url={t.url} className="object-contain" />);
    const img = screen.getByRole("img");
    expect(img.getAttribute("src")).toBe(t.url);
    expect(img.style.backgroundImage).toContain("/files/u/x/t.webp");
    fireEvent.load(img);
    expect(img.style.backgroundImage).toBe("");
  });

  it("compare plays its videos with their full-size posters", () => {
    const a = video(2, 24, small);
    const b = video(2, 24, small);
    const { container } = render(<Compare a={a} takes={[a, b]} onPick={() => undefined} />);
    const posters = [...container.querySelectorAll("video")].map((x) => x.getAttribute("poster"));
    expect(posters.length).toBeGreaterThan(0);
    expect(posters.every((p) => p === "/files/u/x/p.png")).toBe(true);
  });

  it("music is an album cover with its title, badge and length; sound and voice an orb (CreateMusicSound 7a)", () => {
    const song = audio(130, { sound: "music", title: "Rain on the window" });
    const { container, rerender } = render(<Thumb take={song} />);
    const look = container.querySelector('[data-sound="music"]')!;
    expect(look.textContent).toBe("Rain on the windowMusic2:10");
    expect((look.firstElementChild as HTMLElement).style.background).toContain("linear-gradient");
    rerender(<Thumb take={audio(6, { sound: "sound" })} />);
    expect(container.querySelector('[data-sound="sound"]')!.textContent).toBe("Sound0:06");
    rerender(<Thumb take={audio(8)} />);
    expect(container.querySelector('[data-sound="voice"]')!.textContent).toBe("Voice0:08");
    // a strip tile (no badge): the look alone
    rerender(<Thumb take={song} badge={false} />);
    expect(container.querySelector('[data-sound="music"]')!.textContent).toBe("");
  });

  it("a run output's audio keeps the wave (it carries no class)", () => {
    const out = {
      index: 0,
      key: "k",
      kind: "audio",
      url: "/f",
      width: null,
      height: null,
    } as never;
    const { container } = render(<Thumb take={out} />);
    expect(container.querySelector(".wave")).toBeTruthy();
    expect(container.querySelector("[data-sound]")).toBeNull();
  });
});
