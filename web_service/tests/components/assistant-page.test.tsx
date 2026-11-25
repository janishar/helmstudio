// @vitest-environment jsdom
// The Assistant's own page (components/assistant/page.tsx; plan 32; docs/mock/Assistant.dc.html frames 19, 20, 22):
// the admin's sections in the board's order and what each saves (the on/off switch, a plugin's switch, web search's
// Search with, Auto mode's caps, Context's chat window and the prompt cache table), the setup form (cards greyed
// while remote models are off, a remote preset's key checked before Save, the models fetched by themselves, Save
// creating the provider, saving the Assistant on and opening the drawer) and the read-only page anyone else sees.
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import assistantOn from "../../../contracts/fixtures/http/AssistantSettings.valid.1.json";
import remote from "../../../contracts/fixtures/http/Provider.valid.2.json";
import cacheStats from "../../../contracts/fixtures/http/AssistantCacheStats.valid.json";
import type { S } from "@/lib/api";
import { answer } from "./helpers";

vi.mock("next/link", () => ({
  default: ({ href, children, ...rest }: { href: string; children: React.ReactNode }) => (
    <a href={href} {...rest}>
      {children}
    </a>
  ),
}));
const replace = vi.fn();
vi.mock("next/navigation", () => ({ useRouter: () => ({ push: vi.fn(), replace }) }));
let roles = ["user", "admin"];
let status: S<"AssistantStatus"> | null = null;
const setOpen = vi.fn();
vi.mock("@/components/shell", () => ({
  useUser: () => ({ roles }),
  useAssistantApi: () => ({ status, setOpen }),
}));
const { AssistantScreen } = await import("@/components/assistant/page");

beforeAll(() => {
  Element.prototype.scrollIntoView ??= () => undefined;
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  roles = ["user", "admin"];
  status = null;
  setOpen.mockReset();
  replace.mockReset();
});

const settings = (over: Partial<S<"AssistantSettings">> = {}): S<"AssistantSettings"> => ({
  ...assistantOn,
  web_search: false,
  search_provider: null,
  window_tokens: 60000,
  ...over,
});
const deepseek = {
  ...remote,
  name: "deepseek",
  title: "DeepSeek",
  preset: "deepseek",
  key_secret: "deepseek",
  models: ["deepseek-chat", "deepseek-reasoner"],
  default_model: "deepseek-chat",
} as S<"Provider">;
const brave = { ...remote, name: "brave", title: "Brave", kind: "search", models: ["web"] };
const plugin = (over: Partial<S<"Plugin">>): S<"Plugin"> =>
  ({
    version: "0.1.0",
    description: "d",
    author: "helmstudio",
    may_do: [],
    personal: false,
    enabled: true,
    status: "on",
    provider: null,
    updated_at: "2026-11-25T00:00:00Z",
    ...over,
  }) as S<"Plugin">;
const plugins = [
  plugin({ name: "web-search", title: "Web search", kind: "assistant-tools" }),
  plugin({ name: "media-tools", title: "Media tools", kind: "assistant-tools" }),
  plugin({ name: "animator", title: "Animator", kind: "skill", enabled: false, status: "off" }),
];
const presets = [
  {
    id: "deepseek",
    title: "DeepSeek",
    kind: "remote",
    base_url: "https://api.deepseek.com",
    models: ["deepseek-chat", "deepseek-reasoner"],
    key_secret: "deepseek",
    docs: "https://api-docs.deepseek.com/",
    note: "n",
  },
  {
    id: "ollama",
    title: "Ollama",
    kind: "server",
    base_url: "http://host.docker.internal:11434/v1",
    models: [],
    docs: "https://docs.ollama.com/",
    note: "n",
  },
];

/** The settings GET answers: the last PUT's (the page reads them again after each save). */
let saved: S<"AssistantSettings">;
function serve(
  s: S<"AssistantSettings">,
  providers: unknown[],
  more: Record<string, () => unknown> = {},
) {
  saved = s;
  answer("GET", {
    "/v1/studio/admin/assistant": () => ({ data: saved }),
    "/v1/studio/admin/providers": () => ({ data: { providers, remote_enabled: s.remote_enabled } }),
    "/v1/studio/admin/plugins": () => ({ data: { plugins } }),
    "/v1/studio/admin/provider-presets": () => ({
      data: { presets, remote_enabled: s.remote_enabled },
    }),
    "/v1/studio/admin/assistant/cache": () => ({ data: cacheStats }),
    "/v1/studio/assistant/memory": () => ({ data: { items: [], counts: {} } }),
    ...Object.fromEntries(Object.entries(more).map(([k, f]) => [k, () => ({ data: f() })])),
  });
}
const echo = () =>
  answer("PUT", {
    "/v1/studio/admin/assistant": (init) => ({
      data: (saved = {
        ...settings(),
        ...(init.body as object),
        updated_at: "2026-11-25T11:00:00Z",
      }),
    }),
    "/v1/studio/admin/plugins/{plugin}": (init) => ({
      data: plugin({
        name: "media-tools",
        title: "Media tools",
        kind: "assistant-tools",
        ...(init.body as object),
      }),
    }),
    "/v1/studio/admin/providers/{name}": (init) => ({
      data: { ...deepseek, ...(init.body as object) },
    }),
  });
const section = (name: string) => screen.getByRole("region", { name });

describe("the admin's page", () => {
  it("set up: the sections in the board's order, the model and what it sends", async () => {
    serve(settings(), [deepseek, brave]);
    render(<AssistantScreen setup={false} />);
    const model = await screen.findByRole("region", { name: "Model" });
    expect(
      screen
        .getAllByRole("region")
        .map((r) => r.getAttribute("aria-label"))
        .filter(Boolean),
    ).toEqual(["Model", "Tools", "Skills", "Memory"]);
    expect(screen.getAllByRole("form").map((f) => f.getAttribute("aria-label"))).toEqual([
      "Auto mode",
      "Context",
    ]);
    expect(screen.getByText("On")).toBeTruthy();
    expect(within(model).getByText("deepseek-chat · thinking off")).toBeTruthy();
    expect(within(model).getByText("remote")).toBeTruthy();
    expect(
      within(model).getByText(
        "Sends your messages, the screen summary and tool results to DeepSeek; never images.",
      ),
    ).toBeTruthy();
    expect(within(model).getByRole("link", { name: "Model providers" }).getAttribute("href")).toBe(
      "/admin?tab=providers",
    );
    const put = echo();
    fireEvent.click(screen.getByRole("switch", { name: "Assistant on" }));
    await waitFor(() => expect(put).toHaveBeenCalled());
    expect(put.mock.calls[0][1]).toEqual({
      body: {
        enabled: false,
        provider: "deepseek",
        model: null,
        web_search: false,
        search_provider: null,
      },
    });
    expect(await screen.findByText("Off")).toBeTruthy();
  });

  it("Tools and Skills: the same switches as Plugins; Search with picks web search's provider", async () => {
    serve(settings(), [deepseek, brave]);
    render(<AssistantScreen setup={false} />);
    const tools = await screen.findByRole("region", { name: "Tools" });
    await within(tools).findByText("Media tools");
    expect(within(tools).getByText("Free, no key. It asks before each search.")).toBeTruthy();
    expect(
      within(section("Skills"))
        .getByRole("switch", { name: "Animator on" })
        .getAttribute("aria-checked"),
    ).toBe("false");
    const put = echo();
    fireEvent.click(within(tools).getByRole("switch", { name: "Media tools on" }));
    await waitFor(() => expect(put).toHaveBeenCalled());
    expect(put.mock.calls[0]).toEqual([
      "/v1/studio/admin/plugins/{plugin}",
      { params: { path: { plugin: "media-tools" } }, body: { enabled: false } },
    ]);
    fireEvent.change(within(tools).getByLabelText("Search with"), { target: { value: "brave" } });
    await waitFor(() => expect(put).toHaveBeenCalledTimes(2));
    expect(put.mock.calls[1][1]).toMatchObject({
      body: { web_search: true, search_provider: "brave" },
    });
  });

  it("Auto mode saves the caps; Context saves the window and lists the prompt cache", async () => {
    serve(settings(), [deepseek, brave]);
    render(<AssistantScreen setup={false} />);
    const auto = await screen.findByRole("form", { name: "Auto mode" });
    expect((within(auto).getByLabelText("Auto takes, an hour") as HTMLInputElement).value).toBe(
      "8",
    );
    fireEvent.change(within(auto).getByLabelText("Messages a minute"), { target: { value: "3" } });
    const put = echo();
    fireEvent.click(within(auto).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(put).toHaveBeenCalled());
    expect(put.mock.calls[0][1]).toMatchObject({
      body: {
        enabled: true,
        provider: "deepseek",
        limits: {
          ...assistantOn.limits,
          messages_per_minute: 3,
          auto_takes_per_hour: 8,
          auto_gpu_minutes_per_hour: 30,
        },
      },
    });
    const ctx = screen.getByRole("form", { name: "Context" });
    expect(within(ctx).getByText("for deepseek-chat · 60000 until set")).toBeTruthy();
    const rows = within(ctx).getAllByRole("row").slice(1); // qwen3-8b has no prompt tokens: not listed
    expect(rows.map((r) => r.textContent)).toEqual(["DeepSeek · deepseek-chat4285%"]);
    fireEvent.change(within(ctx).getByRole("spinbutton", { name: /Chat window, tokens/ }), {
      target: { value: "128000" },
    });
    fireEvent.click(within(ctx).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(put).toHaveBeenCalledTimes(2));
    expect(put.mock.calls[1][1]).toMatchObject({
      body: { provider: "deepseek", window_tokens: 128000 },
    });
  });

  it("not set up: the form in place of Model; remote cards greyed while remote models are off", async () => {
    serve(
      settings({ enabled: false, provider: null, remote_enabled: false, updated_at: null }),
      [],
    );
    render(<AssistantScreen setup={false} />);
    const form = await screen.findByRole("region", { name: "Set up the Model" });
    expect(screen.getByText("Not set up")).toBeTruthy();
    expect(
      (screen.getByRole("switch", { name: "Assistant on" }) as HTMLButtonElement).disabled,
    ).toBe(true);
    const cards = within(form).getAllByRole("radio");
    expect(cards.map((c) => c.querySelector("b")?.textContent)).toEqual([
      "DeepSeek",
      "Local server",
    ]);
    expect(cards[0].getAttribute("aria-disabled")).toBe("true");
    expect(cards[1].getAttribute("aria-checked")).toBe("true"); // the usable one
    expect(
      within(form).getByText("Remote ones: remote models are off on this deployment."),
    ).toBeTruthy();
    expect(
      screen.getByRole("region", { name: "Tools, Skills, Auto mode, Context, Memory" }),
    ).toBeTruthy();
  });

  it("setup with DeepSeek: the key found, the models fetched by themselves, Save creates it, saves it on and opens the drawer", async () => {
    serve(settings({ enabled: false, provider: null, updated_at: null }), [], {
      "/v1/studio/admin/provider-keys/{key_secret}": () => ({
        key_secret: "deepseek",
        path: "deploy/secrets/deepseek",
        found: true,
        writable: true,
      }),
    });
    const fetch = answer("POST", {
      "/v1/studio/admin/provider-presets/{id}/models": () => ({
        data: { ok: true, models: ["deepseek-chat", "deepseek-reasoner"], seconds: 0.2 },
      }),
    });
    render(<AssistantScreen setup={false} />);
    const form = await screen.findByRole("region", { name: "Set up the Model" });
    expect(await within(form).findByText("✓ Checked with DeepSeek · 2 models")).toBeTruthy();
    expect(fetch.mock.calls[0][1]).toEqual({
      params: { path: { id: "deepseek" } },
      body: { key_secret: "deepseek" },
    });
    const model = within(form).getByLabelText("Model") as HTMLSelectElement;
    expect(model.value).toBe("deepseek-chat");
    expect(
      within(model)
        .getAllByRole("option")
        .map((o) => o.textContent),
    ).toEqual(["deepseek-chat (default)", "deepseek-reasoner"]);
    fireEvent.change(model, { target: { value: "deepseek-reasoner" } });
    const put = echo();
    fireEvent.click(within(form).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(setOpen).toHaveBeenCalledWith(true));
    expect(put.mock.calls[0][0]).toBe("/v1/studio/admin/providers/{name}");
    expect(put.mock.calls[0][1]).toMatchObject({
      params: { path: { name: "deepseek" } },
      body: {
        kind: "remote",
        key_secret: "deepseek",
        enabled: true,
        default_model: "deepseek-reasoner",
      },
    });
    expect(put.mock.calls[1]).toEqual([
      "/v1/studio/admin/assistant",
      {
        body: {
          enabled: true,
          provider: "deepseek",
          model: "deepseek-reasoner",
          web_search: false,
          search_provider: null,
        },
      },
    ]);
    expect(replace).toHaveBeenCalledWith("/assistant");
  });

  it("Change opens the form; Cancel goes back", async () => {
    serve(settings(), [deepseek]);
    render(<AssistantScreen setup={false} />);
    fireEvent.click(await screen.findByRole("button", { name: "Change" }));
    const form = screen.getByRole("region", { name: "Set up the Model" });
    expect(
      within(form)
        .getByRole("radio", { name: /DeepSeek/ })
        .getAttribute("aria-checked"),
    ).toBe("true");
    fireEvent.click(within(form).getByRole("button", { name: "Cancel" }));
    expect(screen.getByRole("region", { name: "Model" })).toBeTruthy();
  });
});

describe("anyone else's page", () => {
  it("Model, Tools and Skills read-only, set by the admin; their own Memory; no switch, Auto mode or Context", async () => {
    roles = ["user"];
    status = {
      enabled: true,
      set_up: true,
      can_set_up: false,
      first_launch: false,
      tools: [{ name: "web-search", title: "Web search" }],
      provider: { title: "DeepSeek", remote: true, model: "deepseek-chat" },
      web_search: null,
      limits: assistantOn.limits,
      thinks: false,
      sees: false,
    };
    answer("GET", {
      "/v1/studio/skills": () => ({
        data: { skills: [{ name: "animator", title: "Animator", when: "w", version: "1" }] },
      }),
      "/v1/studio/assistant/memory": () => ({ data: { items: [], counts: {} } }),
    });
    render(<AssistantScreen setup={false} />);
    expect(
      await within(section("Skills")).findByText("A cartoon film in one style, script to MP4."),
    ).toBeTruthy();
    expect(screen.getAllByText("Set by your admin")).toHaveLength(3);
    expect(within(section("Model")).getByText("deepseek-chat · thinking off")).toBeTruthy();
    expect(
      screen.getByText("What it talks to and what it can use, set by your admin. Memory is yours."),
    ).toBeTruthy();
    for (const s of screen.getAllByRole("switch"))
      expect((s as HTMLButtonElement).disabled).toBe(true);
    expect(screen.queryByRole("switch", { name: "Assistant on" })).toBeNull();
    expect(screen.queryByRole("form", { name: "Auto mode" })).toBeNull();
    expect(screen.queryByRole("form", { name: "Context" })).toBeNull();
    expect(section("Memory")).toBeTruthy();
  });
});
