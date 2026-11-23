// @vitest-environment jsdom
// The landing page (docs/mock/Landing.dc.html): the board's words, every Sign in going to /signin, and the nav's
// anchors landing on the page's own sections.
import { afterEach, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import Landing from "@/app/welcome/page";
import { Logo } from "@/components/kit";

vi.mock("@/app/welcome/landing.css", () => ({}));

afterEach(cleanup);

it("shows the board's sections and sends every Sign in to /signin", () => {
  const { container } = render(<Landing />);
  expect(screen.getByRole("heading", { level: 1 }).textContent).toBe(
    "Wire the models.Direct the film.",
  );
  expect(screen.getAllByRole("heading", { level: 2 }).map((h) => h.textContent)).toEqual([
    "Four rooms. One gallery between them.",
    "Batches that check their own work.",
    "Runs where your GPUs are.",
    "A model is one pack.",
    "Two commands, then sign in.",
  ]);
  const signIns = screen.getAllByRole("link", { name: "Sign in" });
  expect(signIns).toHaveLength(3);
  for (const a of signIns) expect(a.getAttribute("href")).toBe("/signin");

  const nav = screen.getByRole("navigation", { name: "Site" });
  for (const a of nav.querySelectorAll<HTMLAnchorElement>('a[href^="#"]'))
    expect(container.querySelector(a.getAttribute("href")!)).not.toBeNull();
  expect(
    container.querySelector(
      screen.getByRole("link", { name: "Host it yourself" }).getAttribute("href")!,
    ),
  ).not.toBeNull();

  expect(screen.getByLabelText("Theme").tagName).toBe("SELECT");
  expect(container.querySelector("#selfhost pre")?.textContent).toBe(
    "# services, in containers\n$ docker compose up -d\n# GPU runners, native on each Mac\n$ make runners",
  );
  expect(container.querySelector("#models pre")?.textContent).toContain(
    "  turbo: {dir: Qwen-Image-2.1-Turbo, shares: [text_encoder, vae]}\ncomponents:",
  );
});

it("shows logo A's lockup in the header and footer, and the mark in the sign-in tile", () => {
  render(<Landing />);
  const top = screen.getByRole("link", { name: "helmstudio, top of page" });
  expect(top.getAttribute("href")).toBe("#top");
  const lockups = screen.getAllByRole("img", { name: "helmstudio" });
  expect(lockups).toHaveLength(2);
  expect(top.contains(lockups[0])).toBe(true);
  for (const svg of lockups) {
    expect(svg.getAttribute("height")).toBe("24");
    // the mark follows the theme (landing.css .bm), the word is --t1: no fixed colours
    expect(svg.innerHTML).not.toMatch(/#[0-9a-f]{3,6}/i);
    expect(svg.innerHTML).toContain("var(--bA)");
  }
  cleanup();

  const { container } = render(<Logo />);
  const tile = container.firstElementChild!;
  expect(tile.getAttribute("aria-hidden")).toBe("true");
  expect(tile.className).toContain("text-on1");
  const mark = tile.querySelector("svg")!;
  expect(mark.getAttribute("width")).toBe("28");
  expect(mark.querySelectorAll("path")).toHaveLength(3);
  expect(mark.innerHTML).toContain("currentcolor");
});
