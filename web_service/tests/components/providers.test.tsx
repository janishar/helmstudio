// @vitest-environment jsdom
// Admin > Model providers (components/admin/providers.tsx; docs/mock/Providers.dc.html): a remote provider says it is
// off while the deployment's remote switch is, and the form explains what Remote asks for.
import { afterEach, describe, expect, it, vi } from "vitest";
import { api } from "@/lib/api";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { ProvidersAdmin } from "@/components/admin/providers";
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

const serve = (remote_enabled: boolean, found = true, writable = false) =>
  answer("GET", {
    "/v1/studio/admin/providers": () => ({ data: { providers: [remote], remote_enabled } }),
    "/v1/studio/admin/provider-presets": () => ({ data: { ...presetFile, remote_enabled } }),
    "/v1/studio/admin/provider-keys/{key_secret}": (init) => {
      const k = (init.params as { path: { key_secret: string } }).path.key_secret;
      return { data: { key_secret: k, path: `deploy/secrets/providers/${k}`, found, writable } };
    },
  });

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
    expect((screen.getByLabelText(/^Models/) as HTMLTextAreaElement).value).toBe(
      "deepseek-flash\ndeepseek-v4-pro",
    );
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

  it("Fetch: the preset's route with the key file, a tick list with a filter, ticks add lines", async () => {
    serve(true);
    const many = Array.from({ length: 14 }, (_, i) => `deepseek-x${i}`);
    const post = answer("POST", {
      "/v1/studio/admin/provider-presets/{id}/models": () => ({
        data: {
          ok: true,
          models: ["deepseek-flash", ...many],
          answer: null,
          error: null,
          failure: null,
          seconds: 0.3,
        },
      }),
    });
    render(<ProvidersAdmin />);
    await pick("DeepSeek");
    fireEvent.click(screen.getByRole("button", { name: "Fetch" }));
    const list = await screen.findByRole("group", { name: "Fetched models" });
    expect(post.mock.calls[0][1]).toEqual({
      params: { path: { id: "deepseek" } },
      body: { key_secret: "deepseek" },
    });
    expect(within(list).getByText("15 found · tick to add")).toBeTruthy();
    expect(
      (within(list).getByRole("checkbox", { name: "deepseek-flash" }) as HTMLInputElement).checked,
    ).toBe(true);
    fireEvent.change(within(list).getByLabelText("Filter models"), { target: { value: "x13" } });
    expect(within(list).getAllByRole("checkbox")).toHaveLength(1);
    fireEvent.click(within(list).getByRole("checkbox", { name: "deepseek-x13" }));
    expect((screen.getByLabelText(/^Models/) as HTMLTextAreaElement).value).toBe(
      "deepseek-flash\ndeepseek-v4-pro\ndeepseek-x13",
    );
  });

  it("Fetch is greyed once the URL is not the preset's; a failed fetch keeps the models and offers Retry", async () => {
    serve(true);
    const post = answer("POST", {
      "/v1/studio/admin/provider-presets/{id}/models": () => ({
        data: {
          ok: false,
          models: [],
          answer: null,
          error: "openai: the key was refused",
          failure: "answer",
          seconds: 0.2,
        },
      }),
    });
    render(<ProvidersAdmin />);
    await pick("OpenAI");
    fireEvent.click(screen.getByRole("button", { name: "Fetch" }));
    expect(await screen.findByText("openai: the key was refused")).toBeTruthy();
    expect(screen.getByText("The preset's models stay in the list.")).toBeTruthy();
    expect((screen.getByLabelText(/^Models/) as HTMLTextAreaElement).value).toContain(
      "gpt-6.2-sol",
    );
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    await waitFor(() => expect(post).toHaveBeenCalledTimes(2));
    fireEvent.change(screen.getByLabelText("Base URL"), {
      target: { value: "https://proxy.example.com/v1" },
    });
    expect((screen.getByRole("button", { name: "Fetch" }) as HTMLButtonElement).disabled).toBe(
      true,
    );
    expect(
      screen.getByText("Fetch asks only the preset's or the saved URL. Save first."),
    ).toBeTruthy();
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
    expect(field.getAttribute("autocomplete")).toBe("off");
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
