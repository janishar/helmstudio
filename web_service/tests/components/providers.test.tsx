// @vitest-environment jsdom
// Admin > Model providers (components/admin/providers.tsx; docs/mock/Providers.dc.html): a remote provider says it is
// off while the deployment's remote switch is, and the form explains what Remote asks for.
import { afterEach, describe, expect, it, vi } from "vitest";
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

const serve = (remote_enabled: boolean, found = true) =>
  answer("GET", {
    "/v1/studio/admin/providers": () => ({ data: { providers: [remote], remote_enabled } }),
    "/v1/studio/admin/provider-presets": () => ({ data: { ...presetFile, remote_enabled } }),
    "/v1/studio/admin/provider-keys/{key_secret}": (init) => {
      const k = (init.params as { path: { key_secret: string } }).path.key_secret;
      return { data: { key_secret: k, path: `deploy/secrets/providers/${k}`, found } };
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
});
