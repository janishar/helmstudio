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
  addModel,
  levelOf,
  ruleInfo,
  withThinking,
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
    const f = {
      ...blankForm(),
      name: "ollama",
      base_url: "http://10.0.0.5:11434/v1",
      models: ["a"],
    };
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
      models: ["deepseek-flash", "deepseek-v4-pro"],
      default_model: "deepseek-flash",
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
      models: ["q"],
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
    const f = { ...blankForm(), models: ["a", "b"], default_model: "a" };
    const r = tickModel(f, "c", true);
    expect("form" in r && r.form.models).toEqual(["a", "b", "c"]);
    const u = tickModel(f, "b", false);
    expect("form" in u && u.form).toMatchObject({ models: ["a"], default_model: "a" });
    const full = { ...blankForm(), models: Array.from({ length: 16 }, (_, i) => `m${i}`) };
    expect(tickModel(full, "x", true)).toEqual({ error: "16 at most" });
    expect(tickModel(full, "m3", true)).toMatchObject({ form: { models: full.models } });
  });
});

describe("provider models (plan 28)", () => {
  const ds = () => presetForm(preset("deepseek"), blankForm());
  it("Default: the first chosen at start; removing the default moves it to the next chosen", () => {
    const f = { ...ds(), models: ["deepseek-flash", "deepseek-v4-pro", "x"] };
    expect(f.default_model).toBe("deepseek-flash");
    const r = tickModel(f, "deepseek-flash", false);
    expect("form" in r && r.form.default_model).toBe("deepseek-v4-pro");
    const last = tickModel({ ...f, default_model: "x" }, "x", false);
    expect("form" in last && last.form.default_model).toBe("deepseek-v4-pro");
    const first = tickModel(blankForm(), "a", true);
    expect("form" in first && first.form.default_model).toBe("a");
  });
  it("the preset's rules give levels: DeepSeek Off / Low / High (default High), Claude always thinks", () => {
    expect(ds().levels["deepseek-flash"]).toEqual({
      levels: ["off", "low", "high"],
      default: "high",
    });
    expect(ruleInfo(preset("anthropic"), "claude-opus-5-5")).toEqual({ levels: [], always: true });
    expect(ruleInfo(preset("mistral"), "mistral-small-latest")).toEqual({ levels: ["off", "on"] });
    expect(ruleInfo(preset("openai"), "gpt-6.2-sol")).toEqual({
      levels: ["low", "medium", "high"],
      default: "medium",
    });
    expect(ruleInfo(preset("openrouter"), "anthropic/claude-sonnet-5.5")).toBeUndefined();
    expect(ruleInfo(preset("lmstudio"), "qwen3")).toBeUndefined();
  });
  it("a custom name: the pattern, then ticked; it gets its id's rule when one fits", () => {
    const f = ds();
    expect(addModel(f, "bad name", preset("deepseek"))).toEqual({
      error: '"bad name" is not a model name.',
    });
    const r = addModel(f, " deepseek-v4-flash ", preset("deepseek"));
    expect("form" in r && r.form.models).toEqual([
      "deepseek-flash",
      "deepseek-v4-pro",
      "deepseek-v4-flash",
    ]);
    expect("form" in r && r.form.levels["deepseek-v4-flash"]?.levels).toEqual([
      "off",
      "low",
      "high",
    ]);
    const chat = addModel(f, "deepseek-chat", preset("deepseek"));
    expect("form" in chat && chat.form.levels["deepseek-chat"]).toBeUndefined();
  });
  it("Save sends models, default_model and each level shown (picked, else the default, else off)", () => {
    const f = {
      ...ds(),
      thinking: { "deepseek-flash": "off" as const },
      default_model: "deepseek-v4-pro",
    };
    expect(putOf(f)).toMatchObject({
      body: {
        models: ["deepseek-flash", "deepseek-v4-pro"],
        default_model: "deepseek-v4-pro",
        thinking: { "deepseek-flash": "off", "deepseek-v4-pro": "high" },
      },
    });
    expect(levelOf({ ...f, levels: { a: { levels: ["off", "on"] } } }, "a")).toBe("off");
    const claude = { ...presetForm(preset("anthropic"), blankForm()) };
    expect("thinking" in (putOf(claude) as { body: object }).body).toBe(false);
  });
  it("a saved provider reads back with its levels; toggling keeps its thinking exactly", () => {
    const p = P({
      ...remote,
      models: ["deepseek-flash", "deepseek-chat"],
      default_model: "deepseek-chat",
      preset: "deepseek",
      thinking: { "deepseek-flash": "low" },
      thinking_levels: { "deepseek-flash": { levels: ["off", "low", "high"], default: "high" } },
    });
    const f = formOf(p);
    expect(f).toMatchObject({
      default_model: "deepseek-chat",
      thinking: { "deepseek-flash": "low" },
    });
    expect(levelOf(f, "deepseek-flash")).toBe("low");
    expect(toggled(p)).toMatchObject({ enabled: false, thinking: { "deepseek-flash": "low" } });
    expect("thinking" in toggled(P(remote))).toBe(false); // a provider from before 1.53
  });
  it("where a model is picked: the level after the id", () => {
    expect(withThinking("deepseek-flash", "off")).toBe("deepseek-flash · Thinking Off");
    expect(withThinking("deepseek-chat")).toBe("deepseek-chat");
  });
});
