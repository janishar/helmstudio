// @vitest-environment jsdom
// The landing page (docs/mock/Landing.dc.html): the board's words, every Sign in going to /signin, and the nav's
// anchors landing on the page's own sections.
import { afterEach, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import Landing from "@/app/welcome/page";

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
