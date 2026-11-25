// @vitest-environment jsdom
// Admin > AI agents (components/admin/agents.tsx; docs/mock/AdminAgents.dc.html): the Session names row and editor:
// Local greyed while Qwen3.8 is not downloaded, Remote greyed while remote models are off and no server is on this
// network, a remote provider's "sends the prompt to …", and Save's PUT. Plan 32: no Assistant row, a line linking to
// its own page (tests/components/assistant-page.test.tsx has its settings).
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { AgentsAdmin } from "@/components/admin/agents";
import { pickable, searchable } from "@/lib/providers";
import server from "../../../contracts/fixtures/http/Provider.valid.1.json";
import remote from "../../../contracts/fixtures/http/Provider.valid.2.json";
import type { S } from "@/lib/api";
import { answer } from "./helpers";

vi.mock("next/link", () => ({
  default: ({ href, children, ...rest }: { href: string; children: React.ReactNode }) => (
    <a href={href} {...rest}>
      {children}
    </a>
  ),
}));
vi.mock("@/components/shell", () => ({ useUser: () => ({ roles: ["admin"] }) }));

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

const names = (over: Partial<S<"SessionNames">> = {}): S<"SessionNames"> => ({
  mode: "off",
  provider: null,
  model: null,
  local_available: false,
  remote_enabled: false,
  updated_at: null,
  ...over,
});

const brave = {
  ...remote,
  name: "brave",
  title: "Brave",
  kind: "search",
  api: "brave",
  models: [],
  default_model: "",
};

function serve(n: S<"SessionNames">, providers: unknown[]) {
  answer("GET", {
    "/v1/studio/admin/session-names": () => ({ data: n }),
    "/v1/studio/admin/providers": () => ({
      data: { providers, remote_enabled: n.remote_enabled },
    }),
  });
  render(<AgentsAdmin />);
}

const row = (job: string) =>
  screen
    .getAllByText(job)
    .map((e) => e.closest("tr"))
    .find(Boolean) as HTMLElement;
const edit = (job: string) =>
  fireEvent.click(within(row(job)).getByRole("button", { name: "Edit" }));

const radio = (name: RegExp) =>
  within(screen.getByRole("radiogroup", { name: "Session names" })).getByRole("radio", { name });

describe("AgentsAdmin", () => {
  it("Off by default; Local and Remote greyed with their reasons", async () => {
    serve(names(), [remote]);
    expect(await screen.findByText("titles stay as they are")).toBeTruthy();
    expect((radio(/Local LLM/) as HTMLInputElement).disabled).toBe(true);
    expect(screen.getByText(/Qwen3\.8 27B not downloaded/)).toBeTruthy();
    expect(screen.getByRole("link", { name: "Models" }).getAttribute("href")).toBe("/models");
    expect((radio(/Remote/) as HTMLInputElement).disabled).toBe(true);
    expect(screen.getByText("remote models are off on this deployment")).toBeTruthy();
    expect(screen.getByRole("tab", { name: "AI agents" }).getAttribute("aria-selected")).toBe(
      "true",
    );
  });

  it("no Assistant row: one line links to its own page (plan 32)", async () => {
    serve(names(), [server]);
    await screen.findAllByText("Session names");
    expect(screen.queryByText("the in-app assistant")).toBeNull();
    expect(screen.getAllByRole("row")).toHaveLength(2); // the head and Session names
    expect(screen.getByRole("note").textContent).toContain("The Assistant has its own page.");
    expect(screen.getByRole("link", { name: "Open the Assistant page" }).getAttribute("href")).toBe(
      "/assistant",
    );
  });

  it("a server on this network needs no switch", () => {
    expect(pickable([remote, server] as S<"Provider">[], false).map((p) => p.name)).toEqual([
      "ollama",
    ]);
    expect(pickable([remote, server] as S<"Provider">[], true)).toHaveLength(2);
  });

  it("Remote: says what is sent where, and saves the provider and model", async () => {
    serve(names({ remote_enabled: true, local_available: true }), [remote, server]);
    fireEvent.click(await screen.findByRole("radio", { name: /Remote/ }));
    expect(
      screen.getByText("Sends the prompt to fakeai. Never images or references."),
    ).toBeTruthy();
    const put = answer("PUT", {
      "/v1/studio/admin/session-names": (init) => ({
        data: names({
          ...(init.body as object),
          remote_enabled: true,
          updated_at: "2026-11-22T10:00:00Z",
        }),
      }),
    });
    fireEvent.click(
      within(screen.getByRole("complementary", { name: "Session names" })).getByRole("button", {
        name: "Save",
      }),
    );
    await waitFor(() => expect(screen.getByText("never over your titles")).toBeTruthy());
    expect(put.mock.calls[0][1]).toEqual({
      body: { mode: "remote", provider: "fakeai", model: "fake-large" },
    });
    expect(screen.getByText("the prompt to fakeai")).toBeTruthy();
  });

  it("a local server is marked on this network", async () => {
    serve(
      names({ mode: "remote", provider: "ollama", model: "qwen2.5:7b", local_available: true }),
      [server],
    );
    expect(
      await screen.findByText("the prompt to Ollama on the Mac, on this network"),
    ).toBeTruthy();
    expect(screen.getAllByText("on this network")).toHaveLength(1);
    expect((radio(/Local LLM/) as HTMLInputElement).disabled).toBe(false);
  });

  it("pickers: a search provider is never a model, and web search needs the switch", () => {
    const ps = [remote, server, brave] as S<"Provider">[];
    expect(pickable(ps, true).map((p) => p.name)).toEqual(["fakeai", "ollama"]);
    expect(searchable(ps, true).map((p) => p.name)).toEqual(["brave"]);
    expect(searchable(ps, false)).toEqual([]);
  });

  it("Session names: Edit marks its row and moves focus to the panel's checked option", async () => {
    serve(names(), [server]);
    await screen.findAllByText("Session names");
    expect(row("Session names").getAttribute("aria-selected")).toBe("false");
    edit("Session names");
    expect(row("Session names").getAttribute("aria-selected")).toBe("true");
    expect(document.activeElement).toBe(radio(/^Off/));
  });

  it("Session names: Edit focuses the checked option when it is not the first", async () => {
    serve(
      names({ mode: "remote", provider: "ollama", model: "qwen2.5:7b", local_available: true }),
      [server],
    );
    await screen.findAllByText("Session names");
    edit("Session names");
    expect(document.activeElement).toBe(radio(/^Remote/));
  });
});
