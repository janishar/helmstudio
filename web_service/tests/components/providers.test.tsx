// @vitest-environment jsdom
// Admin > Model providers (components/admin/providers.tsx; docs/mock/Providers.dc.html): a remote provider says it is
// off while the deployment's remote switch is, and the form explains what Remote asks for.
import { afterEach, describe, expect, it, vi } from "vitest";
import { api } from "@/lib/api";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { KEY_CHARS_TEXT, ProvidersAdmin } from "@/components/admin/providers";
import remote from "../../../contracts/fixtures/http/Provider.valid.2.json";
import presetFile from "../../../contracts/providers/presets.json";
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

const fetchOk = (models: string[], thinking?: object) => ({
  data: {
    ok: true,
    models,
    answer: null,
    error: null,
    failure: null,
    seconds: 0.3,
    ...(thinking && { thinking }),
  },
});
const deepseekLevels = { levels: ["off", "low", "high"], default: "high" };

const serve = (
  remote_enabled: boolean,
  found = true,
  writable = false,
  providers: object[] = [remote],
) => {
  // Fetch by itself (plan 28): an answer unless a test scripts its own
  answer("POST", {
    "/v1/studio/admin/provider-presets/{id}/models": () => fetchOk([]),
    "/v1/studio/admin/providers/{name}/test": () => fetchOk([]),
  });
  return answer("GET", {
    "/v1/studio/admin/providers": () => ({ data: { providers, remote_enabled } }),
    "/v1/studio/admin/provider-presets": () => ({ data: { ...presetFile, remote_enabled } }),
    "/v1/studio/admin/provider-keys/{key_secret}": (init) => {
      const k = (init.params as { path: { key_secret: string } }).path.key_secret;
      return { data: { key_secret: k, path: `deploy/secrets/providers/${k}`, found, writable } };
    },
  });
};
const models = () => screen.getByRole("group", { name: "Models" });
const ticked = () =>
  within(models())
    .getAllByRole("checkbox")
    .filter((c) => (c as HTMLInputElement).checked)
    .map((c) => c.getAttribute("aria-label")!.replace(/^Use /, ""));

const pick = async (title: string) => {
  const select = (await screen.findByLabelText("Preset")) as HTMLSelectElement;
  await screen.findByRole("option", { name: title });
  fireEvent.change(select, {
    target: { value: presetFile.presets.find((p) => p.title === title)!.id },
  });
};
const value = (label: string) => (screen.getByLabelText(label) as HTMLInputElement).value;

describe("ProvidersAdmin", () => {
  it("notes a remote provider is off while remote models are off", async () => {
    serve(false);
    render(<ProvidersAdmin />);
    expect(await screen.findByText("off · remote models are off")).toBeTruthy();
    expect(
      screen.getByText(/^Remote: the key's secret file name \(in deploy\/secrets\)/),
    ).toBeTruthy();
  });

  it("no note while they are on", async () => {
    serve(true);
    render(<ProvidersAdmin />);
    expect(await screen.findByText("Enabled")).toBeTruthy();
    expect(screen.queryByText("off · remote models are off")).toBeNull();
  });

  it("the picker: Local and Remote groups; remote presets greyed while remote is off", async () => {
    serve(false);
    render(<ProvidersAdmin />);
    expect(
      await screen.findByRole("group", { name: "Remote · remote models are off" }),
    ).toBeTruthy();
    expect((screen.getByRole("option", { name: "DeepSeek" }) as HTMLOptionElement).disabled).toBe(
      true,
    );
    expect((screen.getByRole("option", { name: "Ollama" }) as HTMLOptionElement).disabled).toBe(
      false,
    );
  });

  it("picking DeepSeek fills the form; the key file is Found, Recheck asks again", async () => {
    const get = serve(true);
    render(<ProvidersAdmin />);
    await pick("DeepSeek");
    expect(value("Name")).toBe("deepseek");
    expect(value("Base URL")).toBe("https://api.deepseek.com");
    expect(value("Key file")).toBe("deepseek");
    expect(ticked()).toEqual(["deepseek-flash", "deepseek-v4-pro"]);
    expect(screen.getByText(/Refuses json_schema/)).toBeTruthy();
    expect(screen.getByRole("link", { name: "Docs" }).getAttribute("href")).toBe(
      "https://api-docs.deepseek.com/",
    );
    expect(await screen.findByText("Found", {}, { timeout: 2000 })).toBeTruthy();
    expect(screen.getByText("deploy/secrets/providers/deepseek")).toBeTruthy();
    const asked = () => get.mock.calls.filter((c) => String(c[0]).includes("provider-keys")).length;
    const before = asked();
    fireEvent.click(screen.getByRole("button", { name: "Recheck" }));
    await waitFor(() => expect(asked()).toBe(before + 1), { timeout: 2000 });
  });

  it("a missing key file says where to put it", async () => {
    serve(true, false);
    render(<ProvidersAdmin />);
    await pick("OpenAI");
    expect(await screen.findByText("Missing", {}, { timeout: 2000 })).toBeTruthy();
    expect(
      screen.getByText("Put the key in this file, then Recheck. The key is never shown here."),
    ).toBeTruthy();
  });

  // ---- plan 28: the Models area, layout A · Rows (docs/mock/ProvidersModels.dc.html)

  it("key Found: Fetch by itself on the preset's route; rows with tick, Default and the model's thinking", async () => {
    serve(true);
    const post = answer("POST", {
      "/v1/studio/admin/provider-presets/{id}/models": () =>
        fetchOk(["deepseek-flash", "deepseek-v4-pro"], {
          "deepseek-flash": deepseekLevels,
          "deepseek-v4-pro": deepseekLevels,
        }),
    });
    render(<ProvidersAdmin />);
    await pick("DeepSeek");
    expect(await screen.findByText("2 found", {}, { timeout: 2000 })).toBeTruthy();
    expect(post.mock.calls[0][1]).toEqual({
      params: { path: { id: "deepseek" } },
      body: { key_secret: "deepseek" },
    });
    const flash = screen.getByRole("radiogroup", { name: "Thinking for deepseek-flash" });
    expect(
      within(flash)
        .getAllByRole("radio")
        .map((r) => r.textContent),
    ).toEqual(["Off", "Low", "High"]);
    // nothing picked: the rule's default, marked as the provider's
    expect(within(flash).getByRole("radio", { name: "High" }).getAttribute("aria-checked")).toBe(
      "true",
    );
    expect(within(models()).getAllByText("provider default")).toHaveLength(2);
    fireEvent.click(within(flash).getByRole("radio", { name: "Off" }));
    expect(within(flash).getByRole("radio", { name: "Off" }).getAttribute("aria-checked")).toBe(
      "true",
    );
    const defaults = within(models()).getAllByRole("radio", {
      name: /^Default for /,
    }) as HTMLInputElement[];
    expect(defaults.map((d) => d.checked)).toEqual([true, false]);

    // a custom name: tagged, ×, no thinking setting; Default moves to it, then back when it is removed
    fireEvent.change(screen.getByLabelText("Model id"), { target: { value: "deepseek-chat" } });
    fireEvent.click(screen.getByRole("button", { name: "Add" }));
    expect(ticked()).toEqual(["deepseek-flash", "deepseek-v4-pro", "deepseek-chat"]);
    expect(within(models()).getByText("custom")).toBeTruthy();
    expect(within(models()).getByText("No thinking setting")).toBeTruthy();
    fireEvent.change(screen.getByLabelText("Model id"), { target: { value: "bad name" } });
    fireEvent.click(screen.getByRole("button", { name: "Add" }));
    expect(screen.getByText('"bad name" is not a model name.')).toBeTruthy();

    const put = answer("PUT", {
      "/v1/studio/admin/providers/{name}": (init) => ({
        data: { ...remote, ...(init.body as object) },
      }),
    });
    fireEvent.click(within(models()).getByRole("radio", { name: "Default for deepseek-chat" }));
    fireEvent.click(screen.getByRole("button", { name: "Save provider" }));
    await waitFor(() => expect(put).toHaveBeenCalledTimes(1));
    expect(put.mock.calls[0][1]).toMatchObject({
      body: {
        models: ["deepseek-flash", "deepseek-v4-pro", "deepseek-chat"],
        default_model: "deepseek-chat",
        thinking: { "deepseek-flash": "off", "deepseek-v4-pro": "high" },
      },
    });
  });

  it("removing the default moves it to the next chosen; the 17th model is refused", async () => {
    serve(true);
    const many = Array.from({ length: 15 }, (_, i) => `deepseek-x${i}`);
    answer("POST", {
      "/v1/studio/admin/provider-presets/{id}/models": () =>
        fetchOk(["deepseek-flash", "deepseek-v4-pro", ...many]),
    });
    render(<ProvidersAdmin />);
    await pick("DeepSeek");
    await screen.findByText("17 found", {}, { timeout: 2000 });
    fireEvent.click(within(models()).getByRole("checkbox", { name: "Use deepseek-flash" }));
    const defaults = within(models()).getAllByRole("radio", {
      name: /^Default for /,
    }) as HTMLInputElement[];
    expect(defaults).toHaveLength(1);
    expect(defaults[0].checked).toBe(true); // deepseek-v4-pro
    for (const m of many)
      fireEvent.click(within(models()).getByRole("checkbox", { name: `Use ${m}` }));
    fireEvent.click(within(models()).getByRole("checkbox", { name: "Use deepseek-flash" }));
    expect(screen.getByText("16 at most")).toBeTruthy();
    expect(ticked()).toHaveLength(16);
  });

  it("a long list: a filter, the count that matches, chosen first", async () => {
    serve(true);
    const many = Array.from({ length: 14 }, (_, i) => `deepseek-x${i}`);
    answer("POST", {
      "/v1/studio/admin/provider-presets/{id}/models": () => fetchOk([...many, "deepseek-flash"]),
    });
    render(<ProvidersAdmin />);
    await pick("DeepSeek");
    await screen.findByText("15 found", {}, { timeout: 2000 });
    const rows = within(models())
      .getAllByRole("checkbox")
      .map((c) => c.getAttribute("aria-label"));
    expect(rows.slice(0, 3)).toEqual([
      "Use deepseek-flash",
      "Use deepseek-v4-pro",
      "Use deepseek-x0",
    ]);
    fireEvent.change(screen.getByLabelText("Filter models"), { target: { value: "x1" } });
    expect(screen.getByText("15 found · 5 match · chosen first")).toBeTruthy();
    expect(within(models()).getAllByRole("checkbox")).toHaveLength(5);
  });

  it("Loading: the list shows with Loading models… and Refresh greyed; Refresh asks again", async () => {
    serve(true);
    let done: (v: unknown) => void = () => {};
    const post = vi.spyOn(api, "POST").mockImplementation(
      (() =>
        new Promise((r) => {
          done = r;
        })) as never,
    );
    render(<ProvidersAdmin />);
    await pick("DeepSeek");
    expect(await screen.findByText("Loading models…", {}, { timeout: 2000 })).toBeTruthy();
    expect((screen.getByRole("button", { name: "Refresh" }) as HTMLButtonElement).disabled).toBe(
      true,
    );
    expect(ticked()).toEqual(["deepseek-flash", "deepseek-v4-pro"]);
    done({ data: fetchOk(["deepseek-flash"]).data, response: new Response(null, { status: 200 }) });
    expect(await screen.findByText("1 found")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Refresh" }));
    expect(post).toHaveBeenCalledTimes(2);
  });

  it("Fetch failed: the error with Retry; the list keeps what it had; Refresh greyed off the preset's URL", async () => {
    serve(true);
    const post = answer("POST", {
      "/v1/studio/admin/provider-presets/{id}/models": () => ({
        data: {
          ok: false,
          models: [],
          answer: null,
          error: "openai: timed out",
          failure: "network",
          seconds: 15,
        },
      }),
    });
    render(<ProvidersAdmin />);
    await pick("OpenAI");
    expect(await screen.findByText("openai: timed out", {}, { timeout: 2000 })).toBeTruthy();
    expect(screen.getByText("The list keeps what it had.")).toBeTruthy();
    expect(ticked()).toEqual(["gpt-6.2-sol", "gpt-6-luna", "gpt-6.2-astra"]);
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    await waitFor(() => expect(post).toHaveBeenCalledTimes(2));
    fireEvent.change(screen.getByLabelText("Base URL"), {
      target: { value: "https://proxy.example.com/v1" },
    });
    expect((screen.getByRole("button", { name: "Refresh" }) as HTMLButtonElement).disabled).toBe(
      true,
    );
    expect(
      screen.getByText("Refresh asks only the preset's or the saved URL. Save first."),
    ).toBeTruthy();
  });

  it("No key yet: the preset's models, ticked, and no fetch; OpenAI shows Low / Medium / High, no Off", async () => {
    const post = vi.fn();
    serve(true, false);
    vi.spyOn(api, "POST").mockImplementation(post as never);
    render(<ProvidersAdmin />);
    await pick("OpenAI");
    expect(
      await screen.findByText("Add the key to load the full list.", {}, { timeout: 2000 }),
    ).toBeTruthy();
    expect(ticked()).toEqual(["gpt-6.2-sol", "gpt-6-luna", "gpt-6.2-astra"]);
    expect(post).not.toHaveBeenCalled();
    const sol = screen.getByRole("radiogroup", { name: "Thinking for gpt-6.2-sol" });
    expect(
      within(sol)
        .getAllByRole("radio")
        .map((r) => r.textContent),
    ).toEqual(["Low", "Medium", "High"]);
    expect(within(sol).getByRole("radio", { name: "Medium" }).getAttribute("aria-checked")).toBe(
      "true",
    );
  });

  it("Mistral Small shows Off / On; Claude 5.5 says Always thinks", async () => {
    serve(true, false);
    render(<ProvidersAdmin />);
    await pick("Mistral");
    const small = await screen.findByRole("radiogroup", {
      name: "Thinking for mistral-small-latest",
    });
    expect(
      within(small)
        .getAllByRole("radio")
        .map((r) => r.textContent),
    ).toEqual(["Off", "On"]);
    expect(within(small).getByRole("radio", { name: "Off" }).getAttribute("aria-checked")).toBe(
      "true",
    );
    await pick("Anthropic");
    await waitFor(() => expect(within(models()).getAllByText("Always thinks")).toHaveLength(3));
  });

  it("opening a saved provider fetches through its Test; its levels and custom names read back", async () => {
    const saved = {
      ...remote,
      name: "deepseek",
      base_url: "https://api.deepseek.com/v1", // not the preset's URL: Fetch asks the saved one
      key_secret: "deepseek",
      preset: "deepseek",
      models: ["deepseek-flash", "deepseek-chat"],
      default_model: "deepseek-chat",
      thinking: { "deepseek-flash": "low" },
      thinking_levels: { "deepseek-flash": deepseekLevels },
    };
    serve(true, true, false, [saved]);
    const post = answer("POST", {
      "/v1/studio/admin/providers/{name}/test": () =>
        fetchOk(["deepseek-flash", "deepseek-v4-pro"], { "deepseek-flash": deepseekLevels }),
    });
    render(<ProvidersAdmin />);
    fireEvent.click(await screen.findByRole("button", { name: "Edit" }));
    expect(await screen.findByText("2 found", {}, { timeout: 2000 })).toBeTruthy();
    expect(post.mock.calls[0][0]).toBe("/v1/studio/admin/providers/{name}/test");
    expect(ticked()).toEqual(["deepseek-flash", "deepseek-chat"]);
    const flash = screen.getByRole("radiogroup", { name: "Thinking for deepseek-flash" });
    expect(within(flash).getByRole("radio", { name: "Low" }).getAttribute("aria-checked")).toBe(
      "true",
    );
    expect(within(models()).getByText("custom")).toBeTruthy();
    fireEvent.click(within(models()).getByRole("button", { name: "Remove deepseek-chat" }));
    expect(ticked()).toEqual(["deepseek-flash"]);
    const def = within(models()).getAllByRole("radio", {
      name: /^Default for /,
    }) as HTMLInputElement[];
    expect(def.map((d) => d.checked)).toEqual([true]);
  });

  it("review: Preset None drops the preset's thinking, so Save is not refused for it", async () => {
    serve(true, false);
    render(<ProvidersAdmin />);
    await pick("DeepSeek");
    await screen.findByRole("radiogroup", { name: "Thinking for deepseek-flash" });
    fireEvent.change(screen.getByLabelText("Preset"), { target: { value: "" } });
    expect(within(models()).getAllByText("No thinking setting")).toHaveLength(2);
    const put = answer("PUT", {
      "/v1/studio/admin/providers/{name}": (init) => ({
        data: { ...remote, ...(init.body as object) },
      }),
    });
    fireEvent.click(screen.getByRole("button", { name: "Save provider" }));
    await waitFor(() => expect(put).toHaveBeenCalledTimes(1));
    const body = (put.mock.calls[0][1] as { body: object }).body;
    expect("thinking" in body || "preset" in body).toBe(false);
  });

  it("review: ticking a preset model on a saved provider shows its rule's levels before any Fetch", async () => {
    const saved = {
      ...remote,
      name: "deepseek",
      base_url: "https://api.deepseek.com/v1",
      key_secret: "deepseek",
      preset: "deepseek",
      models: ["deepseek-flash"],
      default_model: "deepseek-flash",
      thinking_levels: { "deepseek-flash": deepseekLevels },
    };
    serve(true, false, false, [saved]);
    render(<ProvidersAdmin />);
    fireEvent.click(await screen.findByRole("button", { name: "Edit" }));
    fireEvent.click(await screen.findByRole("checkbox", { name: "Use deepseek-v4-pro" }));
    const pro = screen.getByRole("radiogroup", { name: "Thinking for deepseek-v4-pro" });
    expect(within(pro).getByRole("radio", { name: "High" }).getAttribute("aria-checked")).toBe(
      "true",
    );
  });

  // ---- plan 27: the key area (docs/mock/Providers.dc.html, Other states)

  const saved = (check: string, found: boolean, detail?: string) => (init: { body?: unknown }) => {
    const body = init.body as { key: string };
    expect(body.key).toBe(KEY);
    return {
      data: {
        key_secret: "openai",
        path: "deploy/secrets/providers/openai",
        found,
        saved: check === "ok" || check === "skipped",
        check,
        ...(detail ? { detail } : {}),
      },
    };
  };
  const KEY = "sk-test-0123456789";
  const keyField = () => screen.getByLabelText(/API key$/) as HTMLInputElement;
  const button = (name: string) => screen.getByRole("button", { name }) as HTMLButtonElement;

  it("Missing: a password field and Save key; a checked key is saved, then Found with Replace and Remove", async () => {
    serve(true, false, true);
    const put = answer("PUT", { "/v1/studio/admin/provider-keys/{key_secret}": saved("ok", true) });
    render(<ProvidersAdmin />);
    await pick("OpenAI");
    expect(await screen.findByText("Missing", {}, { timeout: 2000 })).toBeTruthy();
    const field = keyField();
    expect(screen.getByText("API key")).toBeTruthy();
    expect(field.type).toBe("password");
    expect(field.getAttribute("autocomplete")).toBe("new-password");
    expect(field.getAttribute("placeholder")).toBe("Paste the key");
    expect(
      screen.getByText("Checked with the provider, then saved to this file. Never shown again."),
    ).toBeTruthy();
    expect(screen.queryByText(/Put the key in this file/)).toBeNull();
    expect(button("Save key").disabled).toBe(true);
    fireEvent.change(field, { target: { value: KEY } });
    fireEvent.click(button("Save key"));
    expect(await screen.findByText("Found")).toBeTruthy();
    expect(put.mock.calls[0][1]).toEqual({
      params: { path: { key_secret: "openai" } },
      body: { key: KEY, preset: "openai" },
    });
    expect(screen.queryByLabelText(/API key$/)).toBeNull();
    expect(button("Replace key")).toBeTruthy();
    expect(button("Remove key")).toBeTruthy();
    expect(document.body.innerHTML).not.toContain(KEY);
  });

  it("a key with a character keys never have: the note at once, Save key greyed, nothing sent", async () => {
    serve(true, false, true);
    const put = answer("PUT", { "/v1/studio/admin/provider-keys/{key_secret}": saved("ok", true) });
    render(<ProvidersAdmin />);
    await pick("OpenAI");
    await screen.findByText("Missing", {}, { timeout: 2000 });
    for (const bad of [`${KEY}\u2026`, `\u201c${KEY}\u201d`, `${KEY}\u00a0x`, `${KEY}\u200bx`]) {
      fireEvent.change(keyField(), { target: { value: bad } });
      expect(screen.getByText(KEY_CHARS_TEXT)).toBeTruthy();
      expect(button("Save key").disabled).toBe(true);
      fireEvent.keyDown(keyField(), { key: "Enter" });
    }
    expect(put).not.toHaveBeenCalled();
    expect(document.body.innerHTML).not.toContain(KEY);
    fireEvent.change(keyField(), { target: { value: KEY } });
    expect(screen.queryByText(KEY_CHARS_TEXT)).toBeNull();
    expect(button("Save key").disabled).toBe(false);
  });

  it("Checking: the field and the button are greyed, the button reads Checking…", async () => {
    serve(true, false, true);
    vi.spyOn(api, "PUT").mockImplementation((() => new Promise(() => {})) as never);
    render(<ProvidersAdmin />);
    await pick("OpenAI");
    await screen.findByText("Missing", {}, { timeout: 2000 });
    fireEvent.change(keyField(), { target: { value: KEY } });
    fireEvent.click(button("Save key"));
    expect(button("Checking…").disabled).toBe(true);
    expect(keyField().disabled).toBe(true);
  });

  it("the typed key stays out of the page's markup and the browser's saved passwords", async () => {
    serve(true, false, true);
    render(<ProvidersAdmin />);
    await pick("OpenAI");
    await screen.findByText("Missing", {}, { timeout: 2000 });
    // new-password: a browser never fills the admin's saved sign-in password into the key field
    expect(keyField().getAttribute("autocomplete")).toBe("new-password");
    fireEvent.change(keyField(), { target: { value: KEY } });
    expect(keyField().value).toBe(KEY);
    expect(button("Save key").disabled).toBe(false);
    // React mirrors a controlled input's value into its value attribute: the key would sit in the DOM's markup
    expect(document.body.innerHTML).not.toContain(KEY);
  });

  it("Refused: a red note, the field empties, the line stays Missing", async () => {
    serve(true, false, true);
    answer("PUT", {
      "/v1/studio/admin/provider-keys/{key_secret}": saved(
        "refused",
        false,
        "the provider refused this key; not saved",
      ),
    });
    render(<ProvidersAdmin />);
    await pick("OpenAI");
    await screen.findByText("Missing", {}, { timeout: 2000 });
    fireEvent.change(keyField(), { target: { value: KEY } });
    fireEvent.click(button("Save key"));
    expect(await screen.findByText("the provider refused this key; not saved")).toBeTruthy();
    expect(keyField().value).toBe("");
    expect(screen.getByText("Missing")).toBeTruthy();
  });

  it("Could not check: an amber note with Save anyway (force); the key is kept until then", async () => {
    serve(true, false, true);
    const put = answer("PUT", {
      "/v1/studio/admin/provider-keys/{key_secret}": (init) =>
        (init.body as { force?: boolean }).force
          ? saved("skipped", true)(init)
          : saved("unreachable", false, "timeout")(init),
    });
    render(<ProvidersAdmin />);
    await pick("OpenAI");
    await screen.findByText("Missing", {}, { timeout: 2000 });
    fireEvent.change(keyField(), { target: { value: KEY } });
    fireEvent.click(button("Save key"));
    expect(await screen.findByText("could not check: timeout")).toBeTruthy();
    expect(keyField().value).toBe(KEY);
    fireEvent.click(button("Save anyway"));
    expect(await screen.findByText("Saved without a check; tried on first use.")).toBeTruthy();
    expect(put.mock.calls[1][1]).toEqual({
      params: { path: { key_secret: "openai" } },
      body: { key: KEY, preset: "openai", force: true },
    });
    expect(screen.getByText("Found")).toBeTruthy();
  });

  it("Replace open: an empty New API key field, Save key and Cancel; the old key stays", async () => {
    serve(true, true, true);
    render(<ProvidersAdmin />);
    await pick("DeepSeek");
    await screen.findByText("Found", {}, { timeout: 2000 });
    expect(screen.queryByLabelText(/API key$/)).toBeNull();
    fireEvent.click(button("Replace key"));
    expect(screen.getByText("New API key")).toBeTruthy();
    expect(keyField().value).toBe("");
    expect(screen.getByText("The old key stays until the new one is saved.")).toBeTruthy();
    fireEvent.change(keyField(), { target: { value: KEY } });
    fireEvent.click(button("Cancel"));
    expect(screen.queryByLabelText(/API key$/)).toBeNull();
    fireEvent.click(button("Replace key"));
    expect(keyField().value).toBe("");
  });

  it("Remove asks first, then removes: Missing with the field", async () => {
    serve(true, true, true);
    const del = answer("DELETE", {
      "/v1/studio/admin/provider-keys/{key_secret}": () => ({
        data: {
          key_secret: "deepseek",
          path: "deploy/secrets/providers/deepseek",
          found: false,
          writable: true,
        },
      }),
    });
    render(<ProvidersAdmin />);
    await pick("DeepSeek");
    await screen.findByText("Found", {}, { timeout: 2000 });
    fireEvent.click(button("Remove key"));
    const dialog = screen.getByRole("alertdialog", { name: "Remove key" });
    expect(dialog.textContent).toBe(
      "Remove deploy/secrets/providers/deepseek? Calls to DeepSeek fail until a key is saved.CancelRemove",
    );
    expect(del).not.toHaveBeenCalled();
    fireEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));
    expect(screen.queryByRole("alertdialog")).toBeNull();
    fireEvent.click(button("Remove key"));
    fireEvent.click(
      within(screen.getByRole("alertdialog")).getByRole("button", { name: "Remove" }),
    );
    expect(await screen.findByText("Missing")).toBeTruthy();
    expect(del.mock.calls[0][1]).toEqual({ params: { path: { key_secret: "deepseek" } } });
    expect(screen.getByText("API key")).toBeTruthy();
  });

  it("not writable: plan 25's note, no field, no Replace or Remove", async () => {
    serve(true, false, false);
    render(<ProvidersAdmin />);
    await pick("OpenAI");
    await screen.findByText("Missing", {}, { timeout: 2000 });
    expect(screen.queryByLabelText(/API key$/)).toBeNull();
    cleanup();
    serve(true, true, false);
    render(<ProvidersAdmin />);
    await pick("DeepSeek");
    await screen.findByText("Found", {}, { timeout: 2000 });
    expect(screen.queryByRole("button", { name: "Replace key" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Remove key" })).toBeNull();
  });

  it("Save key is greyed once the Base URL is neither the preset's nor the saved one", async () => {
    serve(true, false, true);
    render(<ProvidersAdmin />);
    await pick("OpenAI");
    await screen.findByText("Missing", {}, { timeout: 2000 });
    fireEvent.change(keyField(), { target: { value: KEY } });
    expect(button("Save key").disabled).toBe(false);
    fireEvent.change(screen.getByLabelText("Base URL"), {
      target: { value: "https://proxy.example.com/v1" },
    });
    expect(button("Save key").disabled).toBe(true);
  });
});
