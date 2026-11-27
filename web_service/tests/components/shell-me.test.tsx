// @vitest-environment jsdom
// The shell asks /auth/me again when the layout could not load the user (auth restarting): the Admin item appears
// without a reload; a 401 stops asking; the workflow picker greys a workflow whose model is not on any live host.
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import { act, cleanup, render, screen } from "@testing-library/react";
import { AppShell } from "@/components/shell";
import { WorkflowPicker } from "@/components/create/workflows";
import { api, type S } from "@/lib/api";
import { answer } from "./helpers";

vi.mock("next/navigation", () => ({
  usePathname: () => "/",
  useRouter: () => ({ push: vi.fn() }),
}));
vi.mock("@/app/(auth)/actions", () => ({ signout: () => undefined }));

beforeAll(() => {
  HTMLElement.prototype.hidePopover ??= () => undefined;
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.useRealTimers();
});

const admin = { id: "u1", email: "a@x.test", roles: ["admin"] } as unknown as S<"User">;

describe("sign-in check retry", () => {
  it("retries /auth/me with a backoff until it answers, then shows System", async () => {
    vi.useFakeTimers();
    let calls = 0;
    answer("GET", {
      "/auth/me": () =>
        ++calls < 3 ? { status: 503, error: { detail: "down" } } : { data: admin },
    });
    render(<AppShell user={null}>page</AppShell>);
    expect(screen.queryByRole("link", { name: "System" })).toBeNull();
    await act(() => vi.advanceTimersByTimeAsync(1000 + 2000 + 4000));
    expect(calls).toBe(3);
    expect(screen.getByRole("link", { name: "System" })).toBeTruthy();
  });
  it("stops after about 30 s, and at once on a 401", async () => {
    vi.useFakeTimers();
    const spy = answer("GET", { "/auth/me": () => ({ status: 503, error: { detail: "down" } }) });
    render(<AppShell user={null}>page</AppShell>);
    await act(() => vi.advanceTimersByTimeAsync(60000));
    // 1, 2, 4, 8, 16 s (the status tile reads the hosts on its own)
    expect(spy.mock.calls.filter((c) => c[0] === "/auth/me")).toHaveLength(5);
    cleanup();
    spy.mockClear();
    const gone = answer("GET", { "/auth/me": () => ({ status: 401, error: { detail: "no" } }) });
    render(<AppShell user={null}>page</AppShell>);
    await act(() => vi.advanceTimersByTimeAsync(60000));
    expect(gone.mock.calls.filter((c) => c[0] === "/auth/me")).toHaveLength(1);
  });
  it("asks nothing when the layout had the user", () => {
    const spy = vi.spyOn(api, "GET");
    render(<AppShell user={admin}>page</AppShell>);
    // (the assistant's status is asked once the user is known)
    expect(spy.mock.calls.filter((c) => c[0] === "/auth/me")).toEqual([]);
    expect(screen.getByRole("link", { name: "System" })).toBeTruthy();
  });
});

describe("rail logo", () => {
  it("links home from its own panel above the menu, not in the phone bar", () => {
    render(<AppShell user={admin}>page</AppShell>);
    const logo = screen.getByRole("link", { name: "helmstudio, home (Create)" });
    expect(logo.getAttribute("href")).toBe("/");
    const nav = screen.getByRole("navigation", { name: "Sections" });
    expect(nav.contains(logo)).toBe(false);
    // two panels in one rail: the logo panel (desktop only) first, then the menu
    const panel = logo.parentElement!;
    expect(panel.className).toMatch(/\bhidden\b.*\bmd:flex\b/);
    expect(panel.parentElement).toBe(nav.parentElement);
    expect(panel.nextElementSibling).toBe(nav);
  });
});

describe("workflow picker", () => {
  it("greys a workflow whose model is not downloaded, with a link to Models, and still opens it", () => {
    const w = (id: string, family: string) =>
      ({ id, title: id, family, source: "template" }) as unknown as S<"Workflow">;
    const onPick = vi.fn();
    render(
      <WorkflowPicker
        workflows={[w("qwen", "qwen-image"), w("h3", "minimax-h3")]}
        label="qwen"
        onPick={onPick}
        onChanged={() => undefined}
        onDeleted={() => undefined}
        onError={() => undefined}
        available={(x) => x.family !== "minimax-h3"}
      />,
    );
    const items = screen.getAllByRole("menuitemradio", { hidden: true });
    expect(items.map((i) => i.className.includes("text-t2"))).toEqual([false, true]);
    const link = screen.getByRole("link", { name: "Models", hidden: true });
    expect(link.getAttribute("href")).toBe("/models");
    expect(link.parentElement?.textContent).toBe("Not downloaded · Models");
    items[1].click();
    expect(onPick).toHaveBeenCalledWith("h3");
  });
});
