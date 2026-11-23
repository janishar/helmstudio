// Admin > Model providers (contracts/README.md "Model sources: providers"): the form a provider is edited in, the
// PUT body built from it (with the checks the API makes that the browser can make first), and a test result in words.
// A remote provider's key is never typed here: `key_secret` names the secret file pipelines_service reads.
// API 1.53 (plan 28): the models are a list of rows (chosen, a default, a thinking level per model that has levels).
import type { S } from "@/lib/api";

type Provider = S<"Provider">;
type Put = S<"ProviderPut">;
export type Preset = S<"ProviderPreset">;
type Compat = S<"ProviderCompat">;
export type Level = S<"ThinkingLevel">;
export type ThinkingInfo = S<"ThinkingInfo">;

export type ProviderForm = {
  kind: Provider["kind"];
  name: string;
  title: string;
  base_url: string;
  /** the chosen models, in order */
  models: string[];
  default_model: string;
  /** API 1.53: the level picked per model; one not picked shows its rule's default, else off */
  thinking: Record<string, Level>;
  /** what each known model offers (saved thinking_levels, the preset's rules, Fetch's answer) */
  levels: Record<string, ThinkingInfo>;
  enabled: boolean;
  timeout_s: string;
  host: string;
  memory_gb: string;
  key_secret: string;
  max_in_flight: string;
  /** API 1.51: the preset it was filled from ("" none) and what that preset's OpenAI layer refuses */
  preset: string;
  compat?: Compat;
};

export const NAME = /^[a-z][a-z0-9-]{0,31}$/;
const MODEL = /^[A-Za-z0-9][A-Za-z0-9._:/-]{0,127}$/;
const URL_RE = /^https?:\/\/[^\s/?#@]+(\/[^\s?#]*)?$/;
export const KEY = /^[a-z0-9][a-z0-9_.-]{0,63}$/;
/** at most this many models per provider (ProviderPut.models) */
export const MAX_MODELS = 16;

export const blankForm = (kind: ProviderForm["kind"] = "server"): ProviderForm => ({
  kind,
  name: "",
  title: "",
  base_url: "",
  models: [],
  default_model: "",
  thinking: {},
  levels: {},
  enabled: true,
  timeout_s: "120",
  host: "",
  memory_gb: "",
  key_secret: "",
  max_in_flight: "2",
  preset: "",
});

/** A provider as the form edits it. */
export function formOf(p: Provider): ProviderForm {
  return {
    kind: p.kind,
    name: p.name,
    title: p.title ?? "",
    base_url: p.base_url,
    models: [...p.models],
    default_model: p.default_model,
    thinking: { ...p.thinking },
    levels: { ...p.thinking_levels },
    enabled: p.enabled,
    timeout_s: String(p.timeout_s),
    host: p.host ?? "",
    memory_gb: p.memory_gb == null ? "" : String(p.memory_gb),
    key_secret: p.key_secret ?? "",
    max_in_flight: String(p.max_in_flight ?? 2),
    preset: p.preset ?? "",
    ...(p.compat && { compat: p.compat }),
  };
}

/** The form filled from a preset: kind, name, title, base URL, models, key file and compat; host, memory, in flight,
 * timeout and enabled are the admin's and stay. */
export const presetForm = (p: Preset, f: ProviderForm): ProviderForm => ({
  ...f,
  kind: p.kind,
  name: p.id,
  title: p.title,
  base_url: p.base_url,
  models: [...p.models],
  default_model: p.models[0] ?? "",
  thinking: {},
  levels: Object.fromEntries(
    p.models.flatMap((m) => {
      const i = ruleInfo(p, m);
      return i ? [[m, i]] : [];
    }),
  ),
  key_secret: p.key_secret ?? "",
  preset: p.id,
  compat: p.compat,
});

/** What a preset's rules give a model id (the first rule whose match fits), else undefined. "models" (OpenRouter):
 * only Fetch knows. */
export function ruleInfo(p: Preset | undefined, id: string): ThinkingInfo | undefined {
  if (!Array.isArray(p?.thinking)) return undefined;
  const r = p.thinking.find((x) => new RegExp(x.match).test(id));
  if (!r) return undefined;
  if (r.always) return { levels: [], always: true };
  const order: Level[] = ["off", "on", "low", "medium", "high"];
  return {
    levels: order.filter((l) => r.levels && l in r.levels),
    ...(r.default && { default: r.default }),
  };
}

/** The level a chosen model runs at: the one picked, else its rule's default, else off; none for a model without
 * levels or one that always thinks. */
export function levelOf(f: ProviderForm, id: string): Level | undefined {
  const i = f.levels[id];
  if (!i || i.always || !i.levels.length) return undefined;
  const l = f.thinking[id] ?? i.default ?? (i.levels.includes("off") ? "off" : undefined);
  return l && i.levels.includes(l) ? l : undefined;
}

/** Tick (add at the end) or untick (remove) a model; the 17th is refused. The first chosen is the default; removing
 * the default moves it to the next chosen. */
export function tickModel(
  f: ProviderForm,
  id: string,
  on: boolean,
): { form: ProviderForm } | { error: string } {
  const ms = f.models;
  if (on && !ms.includes(id) && ms.length >= MAX_MODELS) return { error: `${MAX_MODELS} at most` };
  if (on) {
    const models = ms.includes(id) ? ms : [...ms, id];
    return { form: { ...f, models, default_model: f.default_model || id } };
  }
  const at = ms.indexOf(id);
  const models = ms.filter((m) => m !== id);
  const default_model =
    f.default_model === id ? (models[Math.min(at, models.length - 1)] ?? "") : f.default_model;
  return { form: { ...f, models, default_model } };
}

/** A custom model id (Model id + Add): the pattern, then ticked; the preset's rules give it levels when one fits. */
export function addModel(
  f: ProviderForm,
  raw: string,
  p: Preset | undefined,
): { form: ProviderForm } | { error: string } {
  const id = raw.trim();
  if (!MODEL.test(id)) return { error: `"${id}" is not a model name.` };
  const r = tickModel(f, id, true);
  if ("error" in r) return r;
  const info = f.levels[id] ?? ruleInfo(p, id);
  return { form: info ? { ...r.form, levels: { ...r.form.levels, [id]: info } } : r.form };
}

/** "deepseek-flash · Thinking Off" where a model is picked (nothing when no level is set). */
export const withThinking = (model: string, level?: string) =>
  level ? `${model} · Thinking ${level[0].toUpperCase()}${level.slice(1)}` : model;

/** What Fetch asks: the preset's route while the URL is still the preset's, the saved provider's Test while it is the
 * saved one's, else nothing (no URL from the browser is ever fetched). */
export function fetchFrom(
  f: ProviderForm,
  presets: Preset[],
  saved: Provider[],
): { preset: Preset } | { saved: Provider } | null {
  const url = f.base_url.trim();
  const p = presets.find((x) => x.id === f.preset);
  if (p && p.base_url === url && p.kind === f.kind) return { preset: p };
  const s = saved.find((x) => x.name === f.name);
  if (s && s.base_url === url && s.kind === f.kind) return { saved: s };
  return null;
}

/** compat in words, under Models. */
export const compatLine = (c?: Compat) =>
  [c?.temperature === false && "Sends no temperature", c?.strict_schema && "strict JSON schema"]
    .filter(Boolean)
    .join("; ");

/** The PUT body for the form, or the first thing wrong with it (the server checks again, and the host rule after DNS). */
export function putOf(f: ProviderForm): { body: Put } | { error: string } {
  const models = f.models;
  const url = f.base_url.trim();
  const num = (v: string) => (v.trim() === "" ? NaN : Number(v));
  if (!NAME.test(f.name))
    return { error: "Name: lower-case letters, digits and -, from a letter." };
  if (!URL_RE.test(url))
    return { error: "Base URL: http(s)://host[:port]/path, no user, query or #." };
  if (f.kind === "remote" && !url.startsWith("https://"))
    return { error: "Base URL: a remote provider is https." };
  if (!models.length || models.length > MAX_MODELS) return { error: "Models: 1 to 16." };
  const bad = models.find((m) => !MODEL.test(m));
  if (bad) return { error: `Models: "${bad}" is not a model name.` };
  const thinking = Object.fromEntries(
    models.flatMap((m) => {
      const l = levelOf(f, m);
      return l ? [[m, l]] : [];
    }),
  );
  const timeout = num(f.timeout_s);
  if (!Number.isInteger(timeout) || timeout < 5 || timeout > 600)
    return { error: "Timeout: 5 to 600 seconds." };
  const body: Put = {
    kind: f.kind,
    base_url: url,
    models,
    default_model: models.includes(f.default_model) ? f.default_model : models[0],
    enabled: f.enabled,
    timeout_s: timeout,
    ...(f.title.trim() && { title: f.title.trim().slice(0, 60) }),
    ...(f.compat && { compat: f.compat }),
    ...(f.preset && { preset: f.preset }),
    ...(Object.keys(thinking).length > 0 && { thinking }),
  };
  if (f.kind === "server") {
    const mem = num(f.memory_gb);
    const key = f.key_secret.trim();
    if (!/^[a-z0-9]{1,32}$/.test(f.host))
      return { error: "GPU host: its name, as Admin > Hosts lists it." };
    if (!(mem >= 0.5 && mem <= 1024)) return { error: "Memory: 0.5 to 1024 GB." };
    if (key && !KEY.test(key))
      return {
        error: "Key file: the secret file's name in deploy/secrets (never the key itself).",
      };
    return { body: { ...body, host: f.host, memory_gb: mem, ...(key && { key_secret: key }) } };
  }
  const inFlight = num(f.max_in_flight);
  if (!KEY.test(f.key_secret))
    return { error: "Key file: the secret file's name in deploy/secrets (never the key itself)." };
  if (!Number.isInteger(inFlight) || inFlight < 1 || inFlight > 8)
    return { error: "In flight: 1 to 8 calls at once." };
  return { body: { ...body, key_secret: f.key_secret, max_in_flight: inFlight } };
}

/** The same provider with `enabled` changed, as a PUT body (its thinking exactly as saved). */
export const toggled = (p: Provider): Put => {
  const r = putOf({ ...formOf(p), enabled: !p.enabled, levels: {} }); // no levels: no thinking resolved
  if ("error" in r) throw new Error(r.error);
  const saved = p.thinking ?? {};
  return { ...r.body, ...(Object.keys(saved).length > 0 && { thinking: saved }) };
};

/** A test result in words: "answered · 0.4 s", "network · connection refused". */
export function testLine(t: S<"ProviderTest">): string {
  if (t.ok)
    return `${t.answer ? "answered" : `${t.models.length} models listed`} · ${Math.round(t.seconds * 10) / 10} s`;
  return [t.failure ?? "failed", t.error].filter(Boolean).join(" · ");
}
