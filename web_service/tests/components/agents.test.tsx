// @vitest-environment jsdom
// Admin > AI agents (components/admin/agents.tsx; docs/mock/AdminAgents.dc.html): the Session names row and editor:
// Local greyed while Qwen3.8 is not downloaded, Remote greyed while remote models are off and no server is on this
// network, a remote provider's "sends the prompt to …", and Save's PUT; the Assistant row and its settings (plan 18,
// moved here from Model providers): what it sends where, the caps, and Save's PUT /admin/assistant.
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { AgentsAdmin, capsLine, pickable, searchable } from "@/components/admin/agents";
import assistantOn from "../../../contracts/fixtures/http/AssistantSettings.valid.1.json";
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

const assistantOff: S<"AssistantSettings"> = {
  ...assistantOn,
  enabled: false,
  provider: null,
  web_search: false,
  search_provider: null,
  remote_enabled: false,
  updated_at: null,
};

const deepseek = {
  ...remote,
  name: "deepseek",
  title: "DeepSeek",
  models: ["deepseek-chat"],
  default_model: "deepseek-chat",
};
const brave = {
  ...remote,
  name: "brave",
  title: "Brave",
  kind: "search",
  api: "brave",
  models: [],
  default_model: "",
};

function serve(
  n: S<"SessionNames">,
  providers: unknown[],
  a: S<"AssistantSettings"> = assistantOff,
) {
  answer("GET", {
    "/v1/studio/admin/assistant": () => ({ data: a }),
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
    expect(capsLine(assistantOn.limits)).toBe("200 calls · 2 M tokens a day");
  });

  it("Assistant: its row says what is sent where, and Save PUTs its settings", async () => {
    serve(names({ remote_enabled: true }), [deepseek, brave], assistantOn);
    expect(await screen.findByText("your messages to DeepSeek")).toBeTruthy();
    expect(screen.getByText("200 calls · 2 M tokens a day")).toBeTruthy();
    expect(screen.getByText(/web search: Brave/)).toBeTruthy();
    edit("Assistant");
    const form = screen.getByRole("form", { name: "Assistant settings" });
    expect(
      within(form).getByText(
        "Sends your messages, the screen summary and tool results to DeepSeek; never images.",
      ),
    ).toBeTruthy();
    fireEvent.change(within(form).getByLabelText("Messages a minute"), { target: { value: "3" } });
    fireEvent.click(within(form).getByRole("switch", { name: "Assistant on" }));
    const put = answer("PUT", {
      "/v1/studio/admin/assistant": (init) => ({
        data: { ...assistantOn, ...(init.body as object), updated_at: "2026-11-22T11:00:00Z" },
      }),
    });
    fireEvent.click(within(form).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(put).toHaveBeenCalled());
    expect(put.mock.calls[0][1]).toEqual({
      body: {
        enabled: false,
        provider: "deepseek",
        model: null,
        web_search: true,
        search_provider: "brave",
        limits: { ...assistantOn.limits, messages_per_minute: 3 },
      },
    });
    expect(await screen.findByText("no Assistant in the dock")).toBeTruthy();
    expect(screen.queryByRole("form", { name: "Assistant settings" })).toBeNull(); // Save closes it
    expect(row("Assistant").getAttribute("aria-selected")).toBe("false");
  });

  it("Assistant: Cancel closes the box and puts the saved settings back; web search greyed while remote is off", async () => {
    serve(names(), [server, brave]);
    await screen.findAllByText("Assistant");
    edit("Assistant");
    let form = screen.getByRole("form", { name: "Assistant settings" });
    expect(
      (within(form).getByRole("checkbox", { name: "Web search" }) as HTMLInputElement).disabled,
    ).toBe(true);
    fireEvent.change(within(form).getByLabelText("Model calls a day"), { target: { value: "5" } });
    fireEvent.click(within(form).getByRole("button", { name: "Cancel" }));
    expect(screen.queryByRole("form", { name: "Assistant settings" })).toBeNull();
    // focus goes back to the row's Edit, not lost to the page
    expect(document.activeElement).toBe(
      within(row("Assistant")).getByRole("button", { name: "Edit" }),
    );
    edit("Assistant");
    form = screen.getByRole("form", { name: "Assistant settings" });
    expect((within(form).getByLabelText("Model calls a day") as HTMLInputElement).value).toBe(
      "200",
    );
    expect(
      within(form).getByText(/to Ollama on the Mac, on this network; never images/),
    ).toBeTruthy();
  });

  it("Assistant: closed until Edit, which opens it, marks the row and focuses its first field", async () => {
    serve(names(), [server], assistantOn);
    await screen.findAllByText("Assistant");
    expect(screen.queryByRole("form", { name: "Assistant settings" })).toBeNull();
    expect(row("Assistant").getAttribute("aria-selected")).toBe("false");
    edit("Assistant");
    const form = screen.getByRole("form", { name: "Assistant settings" });
    expect(row("Assistant").getAttribute("aria-selected")).toBe("true");
    expect(form.contains(document.activeElement)).toBe(true);
    expect(document.activeElement).toBe(within(form).getByRole("switch", { name: "Assistant on" }));
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
