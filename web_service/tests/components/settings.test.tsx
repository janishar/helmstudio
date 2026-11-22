// @vitest-environment jsdom
// Settings: the dock links to it, the account and theme cards, the password rules and the server's answers.
import { afterEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { AppShell } from "@/components/shell";
import { SettingsScreen } from "@/components/settings";
import type { S } from "@/lib/api";

const theme = vi.hoisted(() => ({ value: "forest", set: vi.fn() }));
const change = vi.hoisted(() => vi.fn());
vi.mock("next/navigation", () => ({
  usePathname: () => "/settings",
  useRouter: () => ({ push: vi.fn() }),
}));
vi.mock("next-themes", () => ({
  useTheme: () => ({ theme: theme.value, setTheme: theme.set }),
}));
vi.mock("@/app/(auth)/actions", () => ({ signout: () => undefined, changePassword: change }));

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

const admin = {
  id: "u1",
  name: "Root admin",
  email: "a@x.test",
  roles: ["admin", "dev", "user"],
} as unknown as S<"User">;

const page = () => render(<AppShell user={admin}>{<SettingsScreen />}</AppShell>);
const field = (name: string) => screen.getByLabelText(name) as HTMLInputElement;
const type = (name: string, v: string) => fireEvent.change(field(name), { target: { value: v } });
const submit = () =>
  act(async () => {
    fireEvent.click(screen.getByRole("button", { name: "Change password" }));
  });
const fill = (cur: string, next: string, cfm = next) => {
  type("Current password", cur);
  type("New password", next);
  type("Confirm", cfm);
};

describe("settings page", () => {
  it("is a dock link, current on /settings, with no popup", () => {
    page();
    const link = screen.getByRole("link", { name: "Settings" });
    expect(link.getAttribute("href")).toBe("/settings");
    expect(link.getAttribute("aria-current")).toBe("page");
    expect(document.getElementById("settings")).toBeNull();
  });
  it("shows the account with role chips, admin in the accent", () => {
    page();
    expect(screen.getByText("Root admin")).toBeTruthy();
    expect(screen.getByText("a@x.test")).toBeTruthy();
    expect(screen.getByText("admin").className).toContain("tag-accent");
    expect(screen.getByText("dev").className).not.toContain("tag-accent");
    expect(screen.getByRole("main").querySelector("a")).toBeNull(); // no Admin link in Account
  });
  it("lists the seven themes as radios and applies one at once", () => {
    page();
    const radios = screen.getAllByRole("radio") as HTMLInputElement[];
    expect(radios.map((r) => r.value)).toEqual([
      "forest",
      "ocean",
      "aubergine",
      "espresso",
      "oxblood",
      "indigo",
      "parchment",
    ]);
    expect(radios[0].checked).toBe(true);
    fireEvent.click(screen.getByLabelText(/Deep ocean/));
    expect(theme.set).toHaveBeenCalledWith("ocean");
  });
  it("checks the rules before sending: too short, does not match", async () => {
    page();
    fill("old-password", "short12", "short1");
    await submit();
    expect(change).not.toHaveBeenCalled();
    expect(screen.getByRole("alert").textContent).toContain("Password not changed.");
    expect(screen.getByText("Does not match.")).toBeTruthy();
    expect(field("New password").getAttribute("aria-invalid")).toBe("true");
    expect(field("Confirm").getAttribute("aria-invalid")).toBe("true");
  });
  it("shows a wrong current password with the 5-tries note, until it is typed again", async () => {
    change.mockResolvedValue({ wrong: true });
    page();
    fill("wrongpass1", "a-new-password");
    await submit();
    expect(change).toHaveBeenCalledTimes(1);
    const fd = change.mock.calls[0][1] as FormData;
    expect([fd.get("current"), fd.get("new"), fd.get("confirm")]).toEqual([
      "wrongpass1",
      "a-new-password",
      "a-new-password",
    ]);
    expect(screen.getByText("Current password is incorrect.")).toBeTruthy();
    expect(screen.getByText(/5 wrong tries in 15 minutes sign you out here too/)).toBeTruthy();
    expect(field("Current password").value).toBe("wrongpass1"); // kept
    type("Current password", "another-try");
    expect(screen.queryByText("Current password is incorrect.")).toBeNull();
  });
  it("marks a common password from the server's answer", async () => {
    change.mockResolvedValue({ rule: "common" });
    page();
    fill("old-password", "1234567890");
    await submit();
    expect(screen.getByText("Not a common password").className).toContain("text-bad");
    expect(field("New password").getAttribute("aria-invalid")).toBe("true");
  });
  it("clears the form and says so once changed", async () => {
    change.mockResolvedValue({ done: true });
    page();
    fill("old-password", "a-new-password");
    await submit();
    expect(screen.getByRole("status").textContent).toContain("Password changed");
    expect(field("Current password").value).toBe("");
    expect(field("New password").value).toBe("");
    expect(screen.queryByRole("alert")).toBeNull();
  });
  it("has Sign out", () => {
    page();
    expect(screen.getByRole("button", { name: "Sign out" })).toBeTruthy();
  });
});
