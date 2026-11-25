// @vitest-environment jsdom
// The Plugins page's Media tools aside (components/plugins/plugins.tsx; docs/mock/Plugins.dc.html MEDIA TOOLS, alpha.2
// items 11 and 13): Status, Takes and Asks first rows (Starts, Needs and Log only for a plugin that has them), then a row per tool with its name for people.
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
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

describe("Plugins: Media tools", () => {
  it("its aside has the board's rows and tools", async () => {
    answer("GET", { "/v1/studio/admin/plugins": () => ({ data: list }) });
    render(<PluginsScreen />);
    fireEvent.click(await screen.findByRole("button", { name: "Media tools" }));
    const aside = within(screen.getByRole("complementary", { name: "Plugin: Media tools" }));
    const row = (k: string) =>
      aside.getByText(k, { selector: "span.text-t2" }).nextSibling?.textContent;
    expect(row("Status")).toBe("Running · media-tools:8422"); // the stack's container (owner, 2026-11-25)
    expect(row("Takes")).toBe("Reads yours, saves new ones");
    expect(row("Asks first")).toBe("Never (stays on this Mac)");
    // nothing to install and no runner: no Starts, Needs or Log rows
    for (const k of ["Starts", "Needs", "Log"])
      expect(aside.queryByText(k, { selector: "span.text-t2" })).toBeNull();
    expect(aside.queryByText("Tools")).toBeNull(); // a row per tool instead
    const tools = within(aside.getByLabelText("Tools"));
    expect(tools.getByText("Edit image").nextSibling?.textContent).toBe(
      "Crop, resize, rotate, flip, extend, convert",
    );
    expect(tools.getByText("Frames").nextSibling?.textContent).toBe(
      "For the Assistant to look at · no take",
    );
    expect(tools.getAllByText(/./, { selector: "span.text-t2" }).map((s) => s.textContent)).toEqual(
      ["Edit image", "Edit video", "Frame as image", "Edit audio", "Join audio", "Frames"],
    );
  });
});
