import { describe, expect, it } from "vitest";
import server from "../../contracts/fixtures/http/Provider.valid.1.json";
import remote from "../../contracts/fixtures/http/Provider.valid.2.json";
import testOk from "../../contracts/fixtures/http/ProviderTest.valid.1.json";
import testBad from "../../contracts/fixtures/http/ProviderTest.valid.2.json";
import presetFile from "../../contracts/providers/presets.json";
import type { S } from "@/lib/api";
import {
  blankForm,
  compatLine,
  fetchFrom,
  formOf,
  presetForm,
  putOf,
  testLine,
  tickModel,
  toggled,
  type Preset,
} from "@/lib/providers";

const presets = presetFile.presets as Preset[];
const preset = (id: string) => presets.find((p) => p.id === id)!;

const P = (x: unknown) => x as S<"Provider">;

describe("provider form", () => {
  it("round-trips a local server and a remote provider into a PUT body", () => {
    expect(putOf(formOf(P(server)))).toEqual({
      body: {
        kind: "server",
        title: "Ollama on the Mac",
        base_url: "http://host.docker.internal:11434/v1",
        models: ["qwen2.5:7b", "llama3.2:3b"],
        default_model: "qwen2.5:7b",
        enabled: true,
        timeout_s: 120,
        host: "mac",
        memory_gb: 6.5,
      },
    });
    const r = putOf(formOf(P(remote)));
    expect(r).toMatchObject({
      body: { kind: "remote", key_secret: "provider-fakeai", max_in_flight: 2, timeout_s: 60 },
    });
    expect("body" in r && "host" in r.body).toBe(false);
  });
  it("refuses what the API would", () => {
    const f = { ...blankForm(), name: "ollama", base_url: "http://10.0.0.5:11434/v1", models: "a" };
    expect(putOf({ ...f, name: "Ollama" })).toEqual({ error: expect.stringMatching(/^Name/) });
    expect(putOf({ ...f, base_url: "http://u:p@10.0.0.5/v1" })).toEqual({
      error: expect.stringMatching(/^Base URL/),
    });
    expect(putOf({ ...f, memory_gb: "6", host: "" })).toEqual({
      error: expect.stringMatching(/^GPU host/),
    });
    expect(putOf({ ...f, host: "mac", memory_gb: "0.1" })).toEqual({
      error: expect.stringMatching(/^Memory/),
    });
    const rf = { ...f, kind: "remote" as const, key_secret: "provider-x" };
    expect(putOf(rf)).toEqual({ error: "Base URL: a remote provider is https." });
    expect(putOf({ ...rf, base_url: "https://x.test/v1", key_secret: "sk-THE KEY" })).toEqual({
      error: expect.stringMatching(/^Key file/),
    });
  });
  it("toggles enabled, keeping the rest", () => {
    expect(toggled(P(server))).toMatchObject({ enabled: false, host: "mac", memory_gb: 6.5 });
  });
  it("says a test result in words", () => {
    expect(testLine(testOk as S<"ProviderTest">)).toBe("answered · 0.8 s");
    expect(testLine(testBad as S<"ProviderTest">)).toBe("network · fakeai: could not connect");
  });
});

describe("provider presets (plan 25)", () => {
  it("picking DeepSeek fills the form and keeps host, memory, in flight, timeout and enabled", () => {
    const mine = { ...blankForm(), host: "mac", memory_gb: "6.5", timeout_s: "90", enabled: false };
    const f = presetForm(preset("deepseek"), mine);
    expect(f).toMatchObject({
      kind: "remote",
      name: "deepseek",
      title: "DeepSeek",
      base_url: "https://api.deepseek.com",
      models: "deepseek-flash\ndeepseek-v4-pro",
      key_secret: "deepseek",
      preset: "deepseek",
      host: "mac",
      memory_gb: "6.5",
      timeout_s: "90",
      enabled: false,
    });
    expect(putOf(f)).toMatchObject({ body: { preset: "deepseek", key_secret: "deepseek" } });
  });
  it("Anthropic's compat goes into the body and reads as one line", () => {
    const f = presetForm(preset("anthropic"), blankForm());
    expect(putOf(f)).toMatchObject({
      body: { compat: { temperature: false, strict_schema: true } },
    });
    expect(compatLine(f.compat)).toBe("Sends no temperature; strict JSON schema");
    expect(compatLine(undefined)).toBe("");
  });
  it("a server may name a key file, optional", () => {
    const f = {
      ...presetForm(preset("llama-server"), blankForm()),
      host: "mac",
      memory_gb: "4",
      models: "q",
    };
    expect(putOf(f)).toMatchObject({ body: { kind: "server" } });
    expect("key_secret" in (putOf(f) as { body: object }).body).toBe(false);
    expect(putOf({ ...f, key_secret: "llama-server" })).toMatchObject({
      body: { key_secret: "llama-server" },
    });
    expect(putOf({ ...f, key_secret: "sk THE KEY" })).toEqual({
      error: expect.stringMatching(/^Key file/),
    });
  });
  it("Fetch asks the preset's URL or the saved one's, never a changed one", () => {
    const f = presetForm(preset("ollama"), blankForm());
    expect(fetchFrom(f, presets, [])).toEqual({ preset: preset("ollama") });
    const moved = { ...f, base_url: "http://host.docker.internal:11435/v1" };
    expect(fetchFrom(moved, presets, [])).toBeNull();
    expect(fetchFrom(formOf(P(server)), presets, [P(server)])).toEqual({ saved: server });
    expect(
      fetchFrom({ ...formOf(P(server)), base_url: "http://10.0.0.9/v1" }, presets, [P(server)]),
    ).toBeNull();
  });
  it("ticks add at the end and unticks remove; the 17th is refused", () => {
    const f = { ...blankForm(), models: "a\nb" };
    const r = tickModel(f, "c", true);
    expect("form" in r && r.form.models).toBe("a\nb\nc");
    const u = tickModel(f, "a", false);
    expect("form" in u && u.form.models).toBe("b");
    const full = {
      ...blankForm(),
      models: Array.from({ length: 16 }, (_, i) => `m${i}`).join("\n"),
    };
    expect(tickModel(full, "x", true)).toEqual({ error: "16 models at most" });
    expect(tickModel(full, "m3", true)).toMatchObject({ form: { models: full.models } });
  });
});
