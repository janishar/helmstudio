// @vitest-environment jsdom
// Hub › In pipelines (components/admin/agents.tsx; plan 33; docs/mock/Assistant.dc.html frame 24): the Pipeline steps
// and Session names rows and their editors. Default model: Local LLM by default, a shared provider model (never an
// Only-you one), Same as Assistant (greyed while it is not set up); Save's PUT. Session names: Local greyed while
// Qwen3.8 is not downloaded, Provider model greyed while remote models are off and no server is on this network, a
// remote provider's "sends the prompt to …", and Save's PUT.
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { InPipelines } from "@/components/admin/agents";
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
vi.mock("@/components/shell", () => ({
  useUser: () => ({ roles: ["admin"] }),
  useAssistantApi: () => null,
}));

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

const local: S<"PipelineDefaultModel"> = {
  mode: "local",
  provider: null,
  model: null,
  remote_enabled: false,
  updated_at: null,
};
const noAssistant = { provider: null, model: null, personal: false } as S<"AssistantSettings">;

function serve(
  n: S<"SessionNames">,
  providers: unknown[],
  d: S<"PipelineDefaultModel"> = local,
  assistant: Partial<S<"AssistantSettings">> = noAssistant,
) {
  answer("GET", {
    "/v1/studio/admin/pipeline-model": () => ({ data: { ...d, remote_enabled: n.remote_enabled } }),
    "/v1/studio/admin/session-names": () => ({ data: n }),
    "/v1/studio/admin/providers": () => ({
      data: { providers, remote_enabled: n.remote_enabled },
    }),
    "/v1/studio/admin/assistant": () => ({ data: assistant }),
  });
  render(<InPipelines />);
}

const row = (job: string) =>
  screen
    .getAllByText(job)
    .map((e) => e.closest("tr"))
    .find(Boolean) as HTMLElement;
const edit = (job: string) =>
  fireEvent.click(within(row(job)).getByRole("button", { name: "Edit" }));

const radio = (name: RegExp, group = "Session names") =>
  within(screen.getByRole("radiogroup", { name: group })).getByRole("radio", { name });
const section = (name: string) => screen.getByRole("region", { name });

describe("InPipelines", () => {
  it("Off by default; Local and Remote greyed with their reasons", async () => {
    serve(names(), [remote]);
    expect(await screen.findByText("titles stay as they are")).toBeTruthy();
    expect((radio(/Local LLM/) as HTMLInputElement).disabled).toBe(true);
    expect(screen.getByText(/Qwen3\.8 27B not downloaded/)).toBeTruthy();
    expect(screen.getByRole("link", { name: "Models" }).getAttribute("href")).toBe("/models");
    expect((radio(/Provider model/) as HTMLInputElement).disabled).toBe(true);
    expect(screen.getByText("remote models are off on this deployment")).toBeTruthy();
    expect(screen.getByRole("tab", { name: "In pipelines" }).getAttribute("aria-selected")).toBe(
      "true",
    );
  });

  it("Default model: Local LLM by default; a judge keeps its family's vision model; nothing goes out", async () => {
    serve(names(), [server]);
    await screen.findAllByText("Pipeline steps");
    expect(screen.getAllByRole("row")).toHaveLength(3); // the head, Pipeline steps, Session names
    expect(within(row("Pipeline steps")).getByText(/Local LLM · Qwen3\.8 27B/)).toBeTruthy();
    expect(within(row("Pipeline steps")).getByText("nothing out")).toBeTruthy();
    expect((radio(/^Local LLM/, "Default model") as HTMLInputElement).checked).toBe(true);
    expect(within(section("Default model")).getByText("Sends nothing out.")).toBeTruthy();
    const same = radio(/Same as Assistant/, "Default model") as HTMLInputElement;
    expect(same.disabled).toBe(true);
    expect(within(section("Default model")).getByText("the Assistant is not set up")).toBeTruthy();
  });

  it("Default model: a shared provider model, never an Only-you one; Save sends it", async () => {
    const mine = {
      ...remote,
      name: "claude-code-bridge",
      title: "Your Claude subscription",
      owner_id: "u1",
    };
    serve(names({ remote_enabled: true }), [remote, server, mine]);
    await screen.findAllByText("Pipeline steps");
    const dm = section("Default model");
    expect(
      within(dm).getByText(
        "Your Claude subscription is not offered here: it answers only for you.",
      ),
    ).toBeTruthy();
    fireEvent.click(radio(/Provider model/, "Default model"));
    const prov = within(dm).getByLabelText("Provider") as HTMLSelectElement;
    expect([...prov.options].map((o) => o.value)).toEqual(["fakeai", "ollama"]);
    expect(
      within(dm).getByText("Sends the step's prompt and text to fakeai. Never images."),
    ).toBeTruthy();
    const put = answer("PUT", {
      "/v1/studio/admin/pipeline-model": (init) => ({
        data: {
          ...local,
          ...(init.body as object),
          remote_enabled: true,
          updated_at: "2026-11-25T10:00:00Z",
        },
      }),
    });
    fireEvent.click(within(dm).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(put).toHaveBeenCalled());
    expect(put.mock.calls[0][1]).toEqual({
      body: { mode: "remote", provider: "fakeai", model: "fake-large" },
    });
    expect(await within(row("Pipeline steps")).findByText("the prompt to fakeai")).toBeTruthy();
  });

  it("Default model: Same as Assistant follows the Assistant's model", async () => {
    serve(names({ local_available: true }), [server], local, {
      provider: "ollama",
      model: null,
      personal: false,
    });
    await screen.findAllByText("Pipeline steps");
    const same = radio(/Same as Assistant/, "Default model") as HTMLInputElement;
    expect(same.disabled).toBe(false);
    expect(
      within(section("Default model")).getByText("Ollama on the Mac · qwen2.5:7b"),
    ).toBeTruthy();
    fireEvent.click(same);
    const put = answer("PUT", {
      "/v1/studio/admin/pipeline-model": () => ({
        data: { ...local, mode: "assistant", updated_at: "2026-11-25T10:00:00Z" },
      }),
    });
    fireEvent.click(within(section("Default model")).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(put).toHaveBeenCalled());
    expect(put.mock.calls[0][1]).toEqual({ body: { mode: "assistant" } });
    expect(
      await within(row("Pipeline steps")).findByText(/Same as Assistant · Ollama on the Mac/),
    ).toBeTruthy();
  });

  it("a server on this network needs no switch", () => {
    expect(pickable([remote, server] as S<"Provider">[], false).map((p) => p.name)).toEqual([
      "ollama",
    ]);
    expect(pickable([remote, server] as S<"Provider">[], true)).toHaveLength(2);
  });

  it("Session names, a provider model: says what is sent where, and saves the provider and model", async () => {
    serve(names({ remote_enabled: true, local_available: true }), [remote, server]);
    await screen.findAllByText("Session names");
    fireEvent.click(radio(/Provider model/));
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
      within(section("Session names")).getByRole("button", {
        name: "Save",
      }),
    );
    await waitFor(() => expect(screen.getByText("never over your titles")).toBeTruthy());
    expect(put.mock.calls[0][1]).toEqual({
      body: { mode: "remote", provider: "fakeai", model: "fake-large" },
    });
    expect(within(row("Session names")).getByText("the prompt to fakeai")).toBeTruthy();
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
    expect(document.activeElement).toBe(radio(/^Provider model/));
  });
});
