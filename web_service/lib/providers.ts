// Admin > Model providers (contracts/README.md "Model sources: providers"): the form a provider is edited in, the
// PUT body built from it (with the checks the API makes that the browser can make first), and a test result in words.
// A remote provider's key is never typed here: `key_secret` names the secret file pipelines_service reads.
import type { S } from "@/lib/api";

type Provider = S<"Provider">;
type Put = S<"ProviderPut">;

export type ProviderForm = {
  kind: "server" | "remote";
  name: string;
  title: string;
  base_url: string;
  /** one per line; the first is the default */
  models: string;
  enabled: boolean;
  timeout_s: string;
  host: string;
  memory_gb: string;
  key_secret: string;
  max_in_flight: string;
};

export const NAME = /^[a-z][a-z0-9-]{0,31}$/;
const MODEL = /^[A-Za-z0-9][A-Za-z0-9._:/-]{0,127}$/;
const URL_RE = /^https?:\/\/[^\s/?#@]+(\/[^\s?#]*)?$/;

export const blankForm = (kind: ProviderForm["kind"] = "server"): ProviderForm => ({
  kind,
  name: "",
  title: "",
  base_url: "",
  models: "",
  enabled: true,
  timeout_s: "120",
  host: "",
  memory_gb: "",
  key_secret: "",
  max_in_flight: "2",
});

/** A provider as the form edits it (its default model first). */
export function formOf(p: Provider): ProviderForm {
  const models = [p.default_model, ...p.models.filter((m) => m !== p.default_model)];
  return {
    kind: p.kind,
    name: p.name,
    title: p.title ?? "",
    base_url: p.base_url,
    models: models.join("\n"),
    enabled: p.enabled,
    timeout_s: String(p.timeout_s),
    host: p.host ?? "",
    memory_gb: p.memory_gb == null ? "" : String(p.memory_gb),
    key_secret: p.key_secret ?? "",
    max_in_flight: String(p.max_in_flight ?? 2),
  };
}

/** The PUT body for the form, or the first thing wrong with it (the server checks again, and the host rule after DNS). */
export function putOf(f: ProviderForm): { body: Put } | { error: string } {
  const models = [
    ...new Set(
      f.models
        .split(/\r?\n|,/)
        .map((m) => m.trim())
        .filter(Boolean),
    ),
  ];
  const url = f.base_url.trim();
  const num = (v: string) => (v.trim() === "" ? NaN : Number(v));
  if (!NAME.test(f.name))
    return { error: "Name: lower-case letters, digits and -, from a letter." };
  if (!URL_RE.test(url))
    return { error: "Base URL: http(s)://host[:port]/path, no user, query or #." };
  if (f.kind === "remote" && !url.startsWith("https://"))
    return { error: "Base URL: a remote provider is https." };
  if (!models.length || models.length > 16) return { error: "Models: 1 to 16." };
  const bad = models.find((m) => !MODEL.test(m));
  if (bad) return { error: `Models: "${bad}" is not a model name.` };
  const timeout = num(f.timeout_s);
  if (!Number.isInteger(timeout) || timeout < 5 || timeout > 600)
    return { error: "Timeout: 5 to 600 seconds." };
  const body: Put = {
    kind: f.kind,
    base_url: url,
    models,
    default_model: models[0],
    enabled: f.enabled,
    timeout_s: timeout,
    ...(f.title.trim() && { title: f.title.trim().slice(0, 60) }),
  };
  if (f.kind === "server") {
    const mem = num(f.memory_gb);
    if (!/^[a-z0-9]{1,32}$/.test(f.host))
      return { error: "GPU host: its name, as Admin > Hosts lists it." };
    if (!(mem >= 0.5 && mem <= 1024)) return { error: "Memory: 0.5 to 1024 GB." };
    return { body: { ...body, host: f.host, memory_gb: mem } };
  }
  const inFlight = num(f.max_in_flight);
  if (!/^[a-z0-9][a-z0-9_.-]{0,63}$/.test(f.key_secret))
    return { error: "Key file: the secret file's name in deploy/secrets (never the key itself)." };
  if (!Number.isInteger(inFlight) || inFlight < 1 || inFlight > 8)
    return { error: "In flight: 1 to 8 calls at once." };
  return { body: { ...body, key_secret: f.key_secret, max_in_flight: inFlight } };
}

/** The same provider with `enabled` changed, as a PUT body. */
export const toggled = (p: Provider): Put => {
  const r = putOf({ ...formOf(p), enabled: !p.enabled });
  if ("error" in r) throw new Error(r.error);
  return r.body;
};

/** A test result in words: "answered · 0.4 s", "network · connection refused". */
export function testLine(t: S<"ProviderTest">): string {
  if (t.ok)
    return `${t.answer ? "answered" : `${t.models.length} models listed`} · ${Math.round(t.seconds * 10) / 10} s`;
  return [t.failure ?? "failed", t.error].filter(Boolean).join(" · ");
}
