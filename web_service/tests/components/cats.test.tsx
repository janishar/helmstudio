// @vitest-environment jsdom
// The cats (plan 30): a double-click on the rail logo plays a scene in an overlay that takes no input and leaves when
// the scene ends; a single click on Create does not navigate; never the same scene twice in a row; at most one per
// 10 s; reduced motion shows the still pair and animates nothing; leaving Create or hiding the page ends it.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { AppShell } from "@/components/shell";
import type { S } from "@/lib/api";
import { answer } from "./helpers";

let path = "/";
vi.mock("next/navigation", () => ({
  usePathname: () => path,
  useRouter: () => ({ push: vi.fn() }),
}));
vi.mock("@/app/(auth)/actions", () => ({ signout: () => undefined }));

const user = { id: "u1", email: "a@x.test", roles: [] } as unknown as S<"User">;
const animate = vi.fn(() => ({ cancel: vi.fn() }));
let reduced = false;
let day = 0;

beforeEach(() => {
  path = "/";
  reduced = false;
  // module state (the last scene, the 10 s rule) lives on across tests: each test starts a day later
  vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout", "Date"] });
  vi.setSystemTime(new Date(2030, 0, ++day));
  answer("GET", {});
  Element.prototype.animate = animate as never;
  window.matchMedia = ((q: string) => ({
    matches: reduced && q.includes("reduce"),
    addEventListener: () => undefined,
    removeEventListener: () => undefined,
  })) as never;
});
afterEach(() => {
  cleanup();
  document.querySelector(".cats")?.remove();
  vi.restoreAllMocks();
  animate.mockClear();
  vi.useRealTimers();
});

const logo = () => screen.getByRole("link", { name: /helmstudio, home/i });
const layer = () => document.querySelector<HTMLElement>(".cats");
/** Double-clicks the logo and waits for the chunk to load and the scene to start. */
async function dbl() {
  fireEvent.doubleClick(logo());
  await act(() => import("@/components/cats").then(() => undefined));
  await act(() => Promise.resolve());
}
const tick = (ms: number) => act(() => vi.advanceTimersByTime(ms));

describe("the cats", () => {
  it("a double-click plays a scene in a layer that takes no input, gone when it ends", async () => {
    render(<AppShell user={user}>page</AppShell>);
    expect(layer()).toBeNull();
    await dbl();
    const el = layer()!;
    expect(el).toBeTruthy();
    expect(el.dataset.scene).toMatch(/^[1-4]$/);
    expect(el.getAttribute("aria-hidden")).toBe("true");
    expect(getComputedStyle(el).pointerEvents).toBe("none");
    expect(getComputedStyle(el).position).toBe("fixed");
    expect(animate).toHaveBeenCalled();
    await tick(8100);
    expect(layer()).toBeNull();
  });

  it("a single click on Create does not navigate and plays nothing", () => {
    render(<AppShell user={user}>page</AppShell>);
    expect(fireEvent.click(logo())).toBe(false); // default prevented
    expect(layer()).toBeNull();
    cleanup();
    path = "/gallery";
    render(<AppShell user={user}>page</AppShell>);
    expect(fireEvent.click(logo())).toBe(true); // from another page it goes home
  });

  it("one at a time, at most one per 10 s, never the same scene twice in a row", async () => {
    vi.spyOn(Math, "random").mockReturnValue(0);
    render(<AppShell user={user}>page</AppShell>);
    await dbl();
    const first = layer()!.dataset.scene;
    await tick(1000);
    await dbl(); // while it plays: ignored
    expect(document.querySelectorAll(".cats")).toHaveLength(1);
    expect(layer()!.dataset.scene).toBe(first);
    await tick(8000); // 9 s: over, but within 10 s of the start
    expect(layer()).toBeNull();
    await dbl();
    expect(layer()).toBeNull();
    await tick(1000);
    await dbl();
    expect(layer()!.dataset.scene).not.toBe(first);
  });

  it("with reduced motion the still pair shows for 2 s and nothing animates", async () => {
    reduced = true;
    render(<AppShell user={user}>page</AppShell>);
    await dbl();
    expect(layer()!.dataset.scene).toBe("still");
    expect(layer()!.querySelectorAll(".kit.wave, .kit.sit")).toHaveLength(2);
    expect(animate).not.toHaveBeenCalled();
    // the app's global reduced-motion rule cuts every animation to 0.01ms !important: the fade must win over it
    expect(document.getElementById("cats-css")!.textContent).toMatch(/\.cats \.rm\{[^}]*animation-duration:2s!important/);
    await tick(2000);
    expect(layer()).toBeNull();
  });

  it("leaving Create ends the scene", async () => {
    const { rerender } = render(<AppShell user={user}>page</AppShell>);
    await dbl();
    expect(layer()).toBeTruthy();
    path = "/gallery";
    rerender(<AppShell user={user}>page</AppShell>);
    expect(layer()).toBeNull();
  });

  it("hiding the page ends the scene", async () => {
    render(<AppShell user={user}>page</AppShell>);
    await dbl();
    expect(layer()).toBeTruthy();
    vi.spyOn(document, "hidden", "get").mockReturnValue(true);
    document.dispatchEvent(new Event("visibilitychange"));
    expect(layer()).toBeNull();
  });

  it("a page already hidden when the chunk arrives plays nothing", async () => {
    render(<AppShell user={user}>page</AppShell>);
    vi.spyOn(document, "hidden", "get").mockReturnValue(true);
    await dbl();
    expect(layer()).toBeNull();
    expect(animate).not.toHaveBeenCalled();
  });
});
