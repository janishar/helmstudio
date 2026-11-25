// @vitest-environment jsdom
// The Plugins page's Add as provider (components/plugins/plugins.tsx; docs/mock/Plugins.dc.html ADD AS PROVIDER):
// from API 1.73 a plugin whose key comes from its host (Your Claude subscription) has no Key field and Add just works;
// any other model-provider plugin still asks for its key.
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import list from "../../../contracts/fixtures/http/PluginList.valid.json";
import { answer } from "./helpers";

vi.mock("next/navigation", () => ({
  useRouter: () => ({ push: vi.fn(), replace: vi.fn(), refresh: vi.fn() }),
  usePathname: () => "/admin",
  useSearchParams: () => new URLSearchParams(),
}));

const { PluginsScreen } = await import("@/components/plugins/plugins");

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

const bridge = (over: object) => ({
  ...list,
  plugins: list.plugins.map((p) =>
    p.name === "claude-code-bridge" ? { ...p, provider: null, ...over } : p,
  ),
});

describe("Plugins: Add as provider", () => {
  it("a key from its host: no Key field, Add sends no key", async () => {
    answer("GET", { "/v1/studio/admin/plugins": () => ({ data: bridge({}) }) });
    const post = answer("POST", {
      "/v1/studio/admin/plugins/{plugin}/provider": () => ({
        data: { name: "claude-code-bridge-7d1e5c2a", title: "Your Claude subscription" },
      }),
    });
    render(<PluginsScreen />);
    fireEvent.click(await screen.findByRole("button", { name: "Your Claude subscription" }));
    const aside = within(
      screen.getByRole("complementary", { name: "Plugin: Your Claude subscription" }),
    );
    expect(aside.getByText("Add as provider", { selector: "h3" })).toBeTruthy();
    expect(aside.queryByLabelText("Key")).toBeNull();
    fireEvent.click(aside.getByRole("button", { name: "Add" }));
    await waitFor(() => expect(post).toHaveBeenCalled());
    const body = (post.mock.calls[0][1] as { body: Record<string, unknown> }).body;
    expect(body.key).toBeUndefined();
    expect(body.default_model).toBe("claude-sonnet");
  });

  it("any other plugin: the Key field, Add waits for a key", async () => {
    answer("GET", {
      "/v1/studio/admin/plugins": () => ({ data: bridge({ key_from_host: undefined }) }),
    });
    render(<PluginsScreen />);
    fireEvent.click(await screen.findByRole("button", { name: "Your Claude subscription" }));
    const aside = within(
      screen.getByRole("complementary", { name: "Plugin: Your Claude subscription" }),
    );
    expect(aside.getByLabelText("Key")).toBeTruthy();
    expect((aside.getByRole("button", { name: "Add" }) as HTMLButtonElement).disabled).toBe(true);
  });
});
