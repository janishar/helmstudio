"use client";
// Admin > Model providers (docs/mock/Providers.dc.html): local servers (any OpenAI-compatible endpoint on a loopback or
// private address, with the memory their GPU host keeps reserved) and remote providers (https, only while the
// deployment's remote switch is on). Test, Edit, Enable/Disable, Delete per row; the form adds or replaces one. A
// provider names the key file pipelines_service reads. API 1.51 (plan 25): a Preset fills the form, Fetch lists the
// provider's models (asked by pipelines, from the preset's or the saved URL only) as a tick list, and the key file's
// status line says Found or Missing with Recheck. API 1.52 (plan 27): the key area takes a key (a password field, its
// value in its own state, never in the form, a URL or storage), checked with the provider, then saved to the file and
// never shown again; Replace key and Remove key once Found.
import { useCallback, useEffect, useRef, useState } from "react";
import { ErrorNote, Info, Modal } from "@/components/kit";
import { AdminTabs } from "@/components/admin/users";
import { useUser } from "@/components/shell";
import { api, message, ok, type S } from "@/lib/api";
import {
  blankForm,
  compatLine,
  fetchFrom,
  formOf,
  addModel,
  KEY,
  levelOf,
  presetForm,
  putOf,
  testLine,
  tickModel,
  toggled,
  type Level,
  type Preset,
  type ProviderForm,
} from "@/lib/providers";

type Provider = S<"Provider">;
const path = (name: string) => ({ params: { path: { name } } });

export function ProvidersAdmin() {
  const me = useUser();
  const [list, setList] = useState<S<"ProviderList"> | null>(null);
  const [error, setError] = useState("");
  const [form, setForm] = useState<ProviderForm>(blankForm());
  const [tests, setTests] = useState<Record<string, string>>({});
  const [deleting, setDeleting] = useState<Provider | null>(null);
  const [presets, setPresets] = useState<Preset[]>([]);
  const load = useCallback(() => {
    ok(api.GET("/v1/studio/admin/providers"))
      .then((l) => {
        setList(l);
        setError("");
      })
      .catch((e) => setError(message(e)));
    ok(api.GET("/v1/studio/admin/provider-presets"))
      .then((l) => setPresets(l.presets))
      .catch(() => setPresets([])); // the form still works by hand
  }, []);
  useEffect(() => {
    load();
  }, [load]);
  const upsert = (p: Provider) =>
    setList(
      (l) =>
        l && {
          ...l,
          providers: l.providers.some((x) => x.name === p.name)
            ? l.providers.map((x) => (x.name === p.name ? p : x))
            : [...l.providers, p],
        },
    );
  async function test(p: Provider) {
    setTests((t) => ({ ...t, [p.name]: "testing…" }));
    try {
      const r = await ok(
        api.POST("/v1/studio/admin/providers/{name}/test", {
          params: { path: { name: p.name }, query: { model: p.default_model } },
        }),
      );
      setTests((t) => ({ ...t, [p.name]: testLine(r) }));
    } catch (e) {
      setTests((t) => ({ ...t, [p.name]: message(e) }));
    }
  }
  async function toggle(p: Provider) {
    try {
      upsert(
        await ok(
          api.PUT("/v1/studio/admin/providers/{name}", { ...path(p.name), body: toggled(p) }),
        ),
      );
    } catch (e) {
      setError(message(e));
    }
  }
  async function remove(p: Provider) {
    try {
      await ok(api.DELETE("/v1/studio/admin/providers/{name}", path(p.name)));
      setList((l) => l && { ...l, providers: l.providers.filter((x) => x.name !== p.name) });
      setDeleting(null);
    } catch (e) {
      setError(message(e));
      setDeleting(null);
    }
  }

  if (me && !me.roles.includes("admin"))
    return (
      <main id="main" className="flex flex-1 items-center justify-center p-6 text-t2">
        Admins only.
      </main>
    );
  const cell = "border-b border-s3 p-2.5 align-top"; // cells wrap as the board's do; the actions stay on one line
  return (
    <>
      <main id="main" className="flex min-w-0 flex-[999_1_560px] flex-col gap-4 px-5 pt-4 pb-6">
        <div className="flex flex-wrap items-center gap-3">
          <h1 className="font-display text-[22px] font-bold tracking-[0.01em]">Admin</h1>
          <div className="grow" />
          <span className="tag tag-accent">Signed in as root admin</span>
        </div>
        <AdminTabs current="providers" />
        {list && !list.remote_enabled && (
          <p className="m-0 text-[13px] text-t2">
            Remote models are off on this deployment (REMOTE_MODELS in deploy/.env). Local servers
            still work.
          </p>
        )}
        {error && <ErrorNote onRetry={load}>{error}</ErrorNote>}
        <div className="panel min-h-0 overflow-auto px-2 py-1.5" aria-busy={!list && !error}>
          <table className="w-full min-w-[760px] border-collapse text-[13px]">
            <thead>
              <tr className="text-left text-[11px] tracking-[0.04em] text-t2">
                {["Name", "Kind", "Address", "Models", "Limits", "Status", "Actions"].map((h) => (
                  <th
                    key={h}
                    scope="col"
                    className="sticky -top-1.5 z-[1] border-b border-s3 bg-s1 p-2.5 font-semibold"
                  >
                    {h}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {list?.providers.map((p) => (
                <tr key={p.name}>
                  <td className={cell}>
                    <b className="font-semibold">{p.title || p.name}</b>
                    <br />
                    <span className="mono text-[12px] text-t2">{p.name}</span>
                  </td>
                  <td className={cell}>
                    <span className={`tag ${p.kind === "remote" ? "tag-accent" : ""}`}>
                      {p.kind === "server" ? "local server" : "remote"}
                    </span>
                  </td>
                  <td className={`${cell} mono max-w-[240px] truncate text-[12px] text-t2`}>
                    {p.base_url}
                  </td>
                  <td className={`${cell} mono text-[12px] text-t2`}>
                    {p.models.map((m) => (
                      <div key={m}>
                        {m}
                        {m === p.default_model && " (default)"}
                      </div>
                    ))}
                  </td>
                  <td className={`${cell} mono text-[12px] text-t2`}>
                    {p.kind === "server"
                      ? `${p.host} · ${p.memory_gb} GB reserved`
                      : `key file ${p.key_secret}`}
                    <br />
                    {p.kind === "server" ? "" : `${p.max_in_flight ?? 2} in flight · `}timeout{" "}
                    {p.timeout_s} s
                  </td>
                  <td className={cell}>
                    {p.enabled ? "Enabled" : <span className="text-t2">Disabled</span>}
                    {p.kind === "remote" && !list.remote_enabled && (
                      <div className="text-[12px] text-[#f2c14e]">off · remote models are off</div>
                    )}
                    {tests[p.name] && (
                      <div
                        className="max-w-[220px] truncate text-[12px] text-t2"
                        title={tests[p.name]}
                      >
                        {tests[p.name]}
                      </div>
                    )}
                  </td>
                  <td className={`${cell} whitespace-nowrap`}>
                    <span className="flex gap-1.5">
                      <button
                        type="button"
                        className="chip bevel h-[30px]"
                        onClick={() => void test(p)}
                      >
                        Test
                      </button>
                      <button
                        type="button"
                        className="chip bevel h-[30px]"
                        onClick={() => setForm(formOf(p))}
                      >
                        Edit
                      </button>
                      <button
                        type="button"
                        className="chip bevel h-[30px]"
                        onClick={() => void toggle(p)}
                      >
                        {p.enabled ? "Disable" : "Enable"}
                      </button>
                      <button
                        type="button"
                        className="chip bevel h-[30px] text-bad"
                        onClick={() => setDeleting(p)}
                      >
                        Delete
                      </button>
                    </span>
                  </td>
                </tr>
              ))}
              {list?.providers.length === 0 && (
                <tr>
                  <td colSpan={7} className="p-2.5 text-t2">
                    No providers yet. Pipelines run only on the local models of the hosts.
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </main>
      <ProviderPanel
        form={form}
        setForm={setForm}
        remoteOn={list?.remote_enabled ?? false}
        exists={!!list?.providers.some((p) => p.name === form.name)}
        presets={presets}
        saved={list?.providers ?? []}
        onSaved={(p) => {
          upsert(p);
          setForm(blankForm());
        }}
      />
      <Modal open={!!deleting} onClose={() => setDeleting(null)} title="Delete this provider?">
        <p className="mb-4 text-[13px] text-t2">
          “{deleting?.title || deleting?.name}” is deleted; pipelines that name it can&apos;t run
          until it is back. A batch still running with it keeps it.
        </p>
        <div className="flex justify-end gap-2">
          <button type="button" className="chip bevel" onClick={() => setDeleting(null)}>
            Keep it
          </button>
          <button
            type="button"
            className="chip bevel bg-a1 font-semibold text-on1"
            onClick={() => deleting && void remove(deleting)}
          >
            Delete
          </button>
        </div>
      </Modal>
    </>
  );
}

/** Fetch's answer for one provider in the form: its models, or the error; the tick note and the filter. */
type Fetched = {
  tag: string;
  models?: string[];
  error?: string;
  tickError?: string;
  filter: string;
};

/** Add a model provider (or edit one: same name replaces it). */
function ProviderPanel({
  form: f,
  setForm,
  remoteOn,
  exists,
  presets,
  saved,
  onSaved,
}: {
  form: ProviderForm;
  setForm: React.Dispatch<React.SetStateAction<ProviderForm>>;
  remoteOn: boolean;
  exists: boolean;
  presets: Preset[];
  saved: Provider[];
  onSaved: (p: Provider) => void;
}) {
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  // Fetch's answer belongs to the provider in the form (kind, preset, name): another one shows none
  const tag = `${f.kind}|${f.preset}|${f.name}`;
  const [fx, setFx] = useState<Fetched>({ tag: "", filter: "" });
  const cur: Fetched = fx.tag === tag ? fx : { tag, filter: "" };
  const { models: fetched, error: fetchError, tickError, filter } = cur;
  const [fetching, setFetching] = useState(false);
  const [key, setKey] = useState<S<"ProviderKey"> | null>(null);
  const [recheck, setRecheck] = useState(0);
  const set = (k: keyof ProviderForm) => (v: string | boolean) => setForm({ ...f, [k]: v });
  const preset = presets.find((p) => p.id === f.preset);
  const from = fetchFrom(f, presets, saved);
  const keyName = f.key_secret.trim();
  const showKey = f.kind === "remote" || keyName !== "";
  // the key line: asked when the Key file field settles (500 ms), on Recheck and after Save; a malformed name: nothing
  useEffect(() => {
    if (!showKey || !KEY.test(keyName)) return;
    let live = true;
    const t = setTimeout(() => {
      ok(
        api.GET("/v1/studio/admin/provider-keys/{key_secret}", {
          params: { path: { key_secret: keyName } },
        }),
      )
        .then((k) => live && setKey(k))
        .catch(() => live && setKey(null));
    }, 500);
    return () => {
      live = false;
      clearTimeout(t);
    };
  }, [keyName, showKey, recheck]);
  async function fetchModels() {
    if (!from) return;
    setFetching(true);
    setFx({ ...cur, error: "", tickError: "" });
    try {
      const r =
        "preset" in from
          ? await ok(
              api.POST("/v1/studio/admin/provider-presets/{id}/models", {
                params: { path: { id: from.preset.id } },
                body: KEY.test(keyName) ? { key_secret: keyName } : {},
              }),
            )
          : await ok(api.POST("/v1/studio/admin/providers/{name}/test", path(from.saved.name)));
      if (r.ok && r.thinking) {
        const known = r.thinking;
        setForm((g) => ({ ...g, levels: { ...g.levels, ...known } }));
      }
      setFx(
        r.ok
          ? { ...cur, models: r.models, error: "" }
          : {
              ...cur,
              error:
                r.failure === "off"
                  ? "remote models are off on this deployment"
                  : (r.error ?? r.failure ?? "Fetch failed"),
            },
      );
    } catch (err) {
      setFx({ ...cur, error: message(err) });
    } finally {
      setFetching(false);
    }
  }
  // plan 28: Fetch by itself once the provider can be asked: a preset or saved provider whose key is Found (after Save
  // key or Recheck too), a local one on pick, a saved one on opening; Refresh asks again
  const ask = from
    ? "preset" in from
      ? `preset:${from.preset.id}`
      : `saved:${from.saved.name}`
    : "";
  const keyReady = f.kind !== "remote" || (key?.key_secret === keyName && key.found);
  const autoKey = ask && keyReady ? `${ask}|${keyName}` : "";
  const asked = useRef("");
  useEffect(() => {
    if (!autoKey || asked.current === autoKey) return;
    asked.current = autoKey;
    void fetchModels();
    // eslint-disable-next-line react-hooks/exhaustive-deps -- once per provider and key
  }, [autoKey]);
  async function submit(e: React.FormEvent) {
    e.preventDefault();
    const r = putOf(f);
    if ("error" in r) return setError(r.error);
    setBusy(true);
    setError("");
    try {
      onSaved(
        await ok(api.PUT("/v1/studio/admin/providers/{name}", { ...path(f.name), body: r.body })),
      );
      setRecheck((n) => n + 1);
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  }
  const field = (k: keyof ProviderForm, label: string, placeholder: string, extra = {}) => (
    <div className="flex min-w-0 flex-col gap-1.5">
      <label htmlFor={`p-${k}`} className="label">
        {label}
      </label>
      <input
        id={`p-${k}`}
        className="in"
        autoComplete="off"
        placeholder={placeholder}
        value={String(f[k] ?? "")}
        onChange={(e) => set(k)(e.target.value)}
        {...extra}
      />
    </div>
  );
  const group = (kind: Preset["kind"]) => presets.filter((p) => p.kind === kind);
  return (
    <aside
      aria-label="Add a model provider"
      className="aside m-3.5 flex flex-[1_1_340px] flex-col px-[18px] pt-4 pb-6 md:ml-0"
    >
      <form onSubmit={submit} className="flex flex-col gap-3">
        <h2 className="flex items-center font-display text-base leading-5 font-bold">
          {exists ? `Edit ${f.name}` : "Add a model provider"}
          <Info>
            Pick a preset to fill the form; every field stays editable. Models load from the
            provider: tick the ones to use, pick the default and each one&apos;s thinking. A local
            server is any OpenAI-compatible endpoint you run (Ollama, LM Studio, llama-server) on a
            loopback or private address; the host keeps its memory reserved. A remote provider is
            https and needs the deployment&apos;s remote switch. Paste its key under Key file: it is
            checked with the provider, saved to that file on the server and never shown again.
          </Info>
        </h2>
        <div className="flex min-w-0 flex-col gap-1.5">
          <label htmlFor="p-preset" className="label">
            Preset
          </label>
          <select
            id="p-preset"
            className="in"
            value={f.preset}
            onChange={(e) => {
              const p = presets.find((x) => x.id === e.target.value);
              setForm(p ? presetForm(p, f) : { ...f, preset: "", thinking: {}, levels: {} }); // no preset: no rules
            }}
          >
            <option value="">None</option>
            {(["server", "remote"] as const).map(
              (kind) =>
                group(kind).length > 0 && (
                  <optgroup
                    key={kind}
                    label={
                      kind === "server"
                        ? "Local"
                        : remoteOn
                          ? "Remote"
                          : "Remote · remote models are off"
                    }
                  >
                    {group(kind).map((p) => (
                      <option key={p.id} value={p.id} disabled={kind === "remote" && !remoteOn}>
                        {p.title}
                      </option>
                    ))}
                  </optgroup>
                ),
            )}
          </select>
          {preset && (
            <span className="text-[12px] text-t2">
              {preset.note}{" "}
              <a href={preset.docs} target="_blank" rel="noreferrer" className="underline">
                Docs
              </a>
            </span>
          )}
        </div>
        <div
          role="group"
          aria-label="Kind"
          className="flex rounded-[10px] border border-s3 bg-s1 p-[3px]"
        >
          {(["server", "remote"] as const).map((k) => (
            <button
              key={k}
              type="button"
              aria-pressed={f.kind === k}
              disabled={k === "remote" && !remoteOn && f.kind !== "remote"}
              className="h-[34px] flex-1 rounded-lg border-0 font-medium text-t2 aria-pressed:bg-s5 aria-pressed:text-t1"
              onClick={() => setForm({ ...f, kind: k })}
            >
              {k === "server" ? "Local server" : "Remote"}
            </button>
          ))}
        </div>
        <div className="grid grid-cols-2 gap-2">
          {field("name", "Name", "ollama", { maxLength: 32, readOnly: exists })}
          {field("title", "Title users see", "Ollama on the Mac", { maxLength: 60 })}
        </div>
        {exists && f.preset && (
          <span className="text-[12px] text-t2">{f.name} exists: Save replaces it</span>
        )}
        {field(
          "base_url",
          "Base URL",
          f.kind === "server"
            ? "http://host.docker.internal:11434/v1"
            : "https://api.example.com/v1",
          { maxLength: 300 },
        )}
        <ModelsArea
          form={f}
          setForm={setForm}
          preset={preset}
          fetched={fetched}
          fetchError={fetchError}
          note={tickError}
          setNote={(n) => setFx({ ...cur, tickError: n })}
          filter={filter}
          setFilter={(v) => setFx({ ...cur, filter: v })}
          fetching={fetching}
          canFetch={!!from}
          noKey={f.kind === "remote" && key?.key_secret === keyName && !key.found}
          onFetch={() => void fetchModels()}
        />
        {!from && f.base_url.trim() && (
          <span className="text-[12px] text-t2">
            Refresh asks only the preset&apos;s or the saved URL. Save first.
          </span>
        )}
        {compatLine(f.compat) && (
          <span className="text-[12px] text-t2">{compatLine(f.compat)}</span>
        )}
        {f.kind === "server" ? (
          <>
            <div className="grid grid-cols-3 gap-2">
              {field("host", "GPU host", "mac", { maxLength: 32 })}
              {field("memory_gb", "Memory, GB", "6.5", { inputMode: "decimal" })}
              {field("timeout_s", "Timeout, s", "120", { inputMode: "numeric" })}
            </div>
            {field("key_secret", "Key file (optional)", "none", { maxLength: 64 })}
          </>
        ) : (
          <div className="grid grid-cols-3 gap-2">
            {field("key_secret", "Key file", "provider-name", { maxLength: 64 })}
            {field("max_in_flight", "In flight", "2", { inputMode: "numeric" })}
            {field("timeout_s", "Timeout, s", "120", { inputMode: "numeric" })}
          </div>
        )}
        {showKey && key?.key_secret === keyName && (
          <KeyArea
            key={keyName}
            status={key}
            source={from}
            title={f.title.trim() || f.name.trim() || keyName}
            onStatus={setKey}
            onRecheck={() => setRecheck((n) => n + 1)}
          />
        )}
        {f.kind === "server" && (
          <span className="text-[12px] text-t2">
            Remote: the key&apos;s secret file name (in deploy/secrets) and calls in flight at once,
            instead of host and memory.
          </span>
        )}
        <label className="flex min-h-8 items-center gap-2.5 text-[13px]">
          <input
            type="checkbox"
            className="accent-[var(--a1)]"
            checked={f.enabled}
            onChange={(e) => set("enabled")(e.target.checked)}
          />
          Enabled
        </label>
        {error && <ErrorNote>{error}</ErrorNote>}
        <div className="flex gap-2">
          {exists && (
            <button type="button" className="chip bevel h-12" onClick={() => setForm(blankForm())}>
              Cancel
            </button>
          )}
          <button type="submit" className="key h-12 flex-1 text-sm" disabled={busy}>
            Save provider
          </button>
        </div>
      </form>
    </aside>
  );
}

/** A key is printable ASCII without spaces (0x21-0x7E): anything else (a "…" from a cut copy, a curly quote, a
 * non-breaking or zero-width space) gets this note at once and no Save key; studio's 422 says the same. */
export const KEY_CHARS_TEXT =
  "the key has a character keys never have (like … or a curly quote): copy it again from the provider";
const badKey = (v: string) => /[^!-~]/.test(v.trim());

/** The key area (plan 27, the Providers board): the key file's line (a dot, Found or Missing, the path, Recheck); while
 * Missing an API key field with Save key; once Found, Replace key and Remove key (asked first). The check's answers:
 * Checking…, Refused (red), Could not check (amber, Save anyway). Not writable: plan 25's note, no field. */
function KeyArea({
  status,
  source,
  title,
  onStatus,
  onRecheck,
}: {
  status: S<"ProviderKey">;
  source: ReturnType<typeof fetchFrom>;
  title: string;
  onStatus: (k: S<"ProviderKey">) => void;
  onRecheck: () => void;
}) {
  // the key: only in the field itself (uncontrolled: React would mirror a controlled value into the value attribute,
  // putting the key in the page's markup), cleared after every answer but Could not check
  const input = useRef<HTMLInputElement>(null);
  const [typed, setTyped] = useState(false);
  const [bad, setBad] = useState(false); // a character keys never have: the note, never the key or the character
  const clear = () => {
    if (input.current) input.current.value = "";
    setTyped(false);
    setBad(false);
  };
  const [mode, setMode] = useState<"" | "replace" | "remove">("");
  const [busy, setBusy] = useState(false);
  const [note, setNote] = useState<{
    kind: "refused" | "unreachable" | "error";
    text: string;
  } | null>(null);
  const [skipped, setSkipped] = useState(false);
  const keyPath = { params: { path: { key_secret: status.key_secret } } };
  const colour = status.found ? "var(--a1)" : "#f2c14e";
  async function save(force: boolean) {
    if (!source) return;
    setBusy(true);
    setNote(null);
    const from =
      "preset" in source ? { preset: source.preset.id } : { provider: source.saved.name };
    try {
      const r = await ok(
        api.PUT("/v1/studio/admin/provider-keys/{key_secret}", {
          ...keyPath,
          body: { key: input.current?.value ?? "", ...from, ...(force ? { force } : {}) },
        }),
      );
      onStatus({ key_secret: r.key_secret, path: r.path, found: r.found, writable: true });
      if (r.check === "unreachable") return setNote({ kind: "unreachable", text: r.detail ?? "" });
      clear();
      if (r.check === "refused")
        return setNote({
          kind: "refused",
          text: r.detail ?? "the provider refused this key; not saved",
        });
      setMode("");
      setSkipped(r.check === "skipped");
    } catch (err) {
      clear();
      setNote({ kind: "error", text: message(err) });
    } finally {
      setBusy(false);
    }
  }
  async function remove() {
    setBusy(true);
    setNote(null);
    try {
      onStatus(await ok(api.DELETE("/v1/studio/admin/provider-keys/{key_secret}", keyPath)));
      setMode("");
      setSkipped(false);
    } catch (err) {
      setNote({ kind: "error", text: message(err) });
    } finally {
      setBusy(false);
    }
  }
  const cancel = () => {
    setMode("");
    clear();
    setNote(null);
  };
  const field = status.writable && (!status.found || mode === "replace");
  return (
    <div className="flex flex-col gap-2">
      <div className="flex flex-wrap items-center gap-2 text-[12px]">
        <span className="size-2 rounded-full" style={{ background: colour }} />
        <span className="font-semibold" style={{ color: colour }}>
          {status.found ? "Found" : "Missing"}
        </span>
        <span className="mono text-t2">{status.path}</span>
        <div className="grow" />
        <button type="button" className="chip h-[26px] text-[12px]" onClick={onRecheck}>
          Recheck
        </button>
      </div>
      {!status.writable && !status.found && (
        <span className="text-[12px] text-t2">
          Put the key in this file, then Recheck. The key is never shown here.
        </span>
      )}
      {status.writable && status.found && mode === "" && (
        <>
          {skipped && (
            <span className="text-[12px] text-t2">Saved without a check; tried on first use.</span>
          )}
          <div className="flex gap-2">
            <button
              type="button"
              className="chip h-[30px] text-[12px]"
              onClick={() => setMode("replace")}
            >
              Replace key
            </button>
            <button
              type="button"
              className="chip h-[30px] text-[12px]"
              onClick={() => setMode("remove")}
            >
              Remove key
            </button>
          </div>
        </>
      )}
      {status.writable && status.found && mode === "remove" && (
        <div
          role="alertdialog"
          aria-label="Remove key"
          className="flex flex-col gap-2 rounded-xl bg-s2 px-3 py-2.5 text-[12px]"
        >
          <span>
            Remove <span className="mono text-t2">{status.path}</span>? Calls to {title} fail until
            a key is saved.
          </span>
          <div className="flex justify-end gap-2">
            <button type="button" className="chip h-7 text-[12px]" onClick={cancel}>
              Cancel
            </button>
            <button
              type="button"
              className="chip h-7 bg-[#ff6b6b] text-[12px] font-semibold text-[#2b0505]"
              disabled={busy}
              onClick={() => void remove()}
            >
              Remove
            </button>
          </div>
        </div>
      )}
      {field && (
        <div className="flex flex-col gap-1.5">
          <label htmlFor="p-api-key" className="label">
            {status.found ? "New API key" : "API key"}
          </label>
          <div className="flex gap-2">
            <input
              id="p-api-key"
              className="in min-w-0 flex-auto disabled:opacity-60"
              type="password"
              autoComplete="new-password"
              spellCheck={false}
              placeholder="Paste the key"
              ref={input}
              disabled={busy}
              onChange={(e) => {
                setTyped(e.target.value.trim() !== "");
                setBad(badKey(e.target.value));
              }}
              onKeyDown={(e) => {
                if (e.key !== "Enter") return;
                e.preventDefault(); // Enter saves the key, never the provider form
                if (typed && !bad && source) void save(false);
              }}
            />
            <button
              type="button"
              className="chip h-[38px] flex-none text-[12px]"
              disabled={busy || !typed || bad || !source}
              onClick={() => void save(false)}
            >
              {busy ? "Checking…" : "Save key"}
            </button>
            {status.found && (
              <button
                type="button"
                className="chip h-[38px] flex-none text-[12px]"
                onClick={cancel}
              >
                Cancel
              </button>
            )}
          </div>
        </div>
      )}
      {note?.kind === "refused" && (
        <div
          role="alert"
          className="flex items-center gap-2 rounded-[10px] bg-[color-mix(in_srgb,#ff6b6b_14%,transparent)] px-2.5 py-2 text-[12px] text-[#ff8a8a]"
        >
          <span className="flex-auto">{note.text}</span>
        </div>
      )}
      {note?.kind === "unreachable" && (
        <div
          role="alert"
          className="flex items-center gap-2 rounded-[10px] bg-[color-mix(in_srgb,#f2c14e_14%,transparent)] px-2.5 py-2 text-[12px] text-[#f2c14e]"
        >
          <span className="flex-auto">could not check: {note.text}</span>
          <button
            type="button"
            className="chip h-[26px] text-[12px] text-t1"
            disabled={busy || !typed || bad || !source}
            onClick={() => void save(true)}
          >
            Save anyway
          </button>
        </div>
      )}
      {note?.kind === "error" && <ErrorNote>{note.text}</ErrorNote>}
      {field && bad && <ErrorNote>{KEY_CHARS_TEXT}</ErrorNote>}
      {field && !note && !bad && (
        <span className="text-[12px] text-t2">
          {status.found
            ? "The old key stays until the new one is saved."
            : "Checked with the provider, then saved to this file. Never shown again."}
        </span>
      )}
    </div>
  );
}

/** The Models area, layout A · Rows (docs/mock/ProvidersModels.dc.html; plan 28): one row per model, chosen first: a
 * tick to use it, the id, a Default radio; under a chosen model its thinking (that model's levels only), Always thinks,
 * or No thinking setting; a custom one carries a tag and ×. A filter past 12 rows, Model id + Add for a custom name,
 * Refresh; Loading models…, Fetch failed (Retry, the list keeps what it had) and No key yet. */
function ModelsArea({
  form: f,
  setForm,
  preset,
  fetched,
  fetchError,
  note,
  setNote,
  filter,
  setFilter,
  fetching,
  canFetch,
  noKey,
  onFetch,
}: {
  form: ProviderForm;
  setForm: (f: ProviderForm) => void;
  preset: Preset | undefined;
  fetched: string[] | undefined;
  fetchError: string | undefined;
  note: string | undefined;
  setNote: (n: string) => void;
  filter: string;
  setFilter: (v: string) => void;
  fetching: boolean;
  canFetch: boolean;
  noKey: boolean;
  onFetch: () => void;
}) {
  const [typed, setTyped] = useState("");
  const offered = fetched ?? preset?.models ?? [];
  const rows = [...f.models, ...offered.filter((m) => !f.models.includes(m))];
  const q = filter.trim().toLowerCase();
  const shown = rows.filter((m) => m.toLowerCase().includes(q));
  const apply = (r: { form: ProviderForm } | { error: string }) => {
    if ("error" in r) return setNote(r.error);
    setNote("");
    setForm(r.form);
  };
  const add = () => {
    const r = addModel(f, typed, preset);
    apply(r);
    if ("form" in r) setTyped("");
  };
  const status = fetching
    ? "Loading models…"
    : fetched
      ? `${fetched.length} found${q ? ` · ${shown.length} match · chosen first` : ""}`
      : "";
  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-center gap-2">
        <span id="p-models" className="label flex-auto">
          Models
        </span>
        {status && <span className="text-[12px] text-t2">{status}</span>}
        <button
          type="button"
          className="chip h-[26px] text-[12px] disabled:opacity-50"
          disabled={!canFetch || fetching}
          onClick={onFetch}
        >
          Refresh
        </button>
      </div>
      {fetchError && (
        <>
          <div
            role="alert"
            className="flex items-center gap-2 rounded-[10px] bg-[color-mix(in_srgb,#ff6b6b_14%,transparent)] px-2.5 py-2 text-[12px] text-[#ff8a8a]"
          >
            <span className="flex-auto">{fetchError}</span>
            <button type="button" className="chip h-[26px] text-[12px] text-t1" onClick={onFetch}>
              Retry
            </button>
          </div>
          <span className="text-[12px] text-t2">The list keeps what it had.</span>
        </>
      )}
      {noKey && !fetched && (
        <span className="text-[12px] text-[#f2c14e]">Add the key to load the full list.</span>
      )}
      {rows.length > 12 && (
        <input
          className="in h-[30px] text-[12px]"
          aria-label="Filter models"
          placeholder="Filter"
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
        />
      )}
      {note && <span className="text-[12px] text-bad">{note}</span>}
      {(shown.length > 0 || fetching) && (
        <div
          role="group"
          aria-labelledby="p-models"
          aria-busy={fetching}
          className="flex max-h-[260px] flex-col gap-0.5 overflow-auto rounded-xl border border-s3 bg-s1 p-1.5"
        >
          {shown.map((m) => (
            <ModelRow
              key={m}
              form={f}
              id={m}
              custom={!offered.includes(m)}
              onTick={(on) => apply(on ? addModel(f, m, preset) : tickModel(f, m, false))}
              onDefault={() => setForm({ ...f, default_model: m })}
              onLevel={(l) => setForm({ ...f, thinking: { ...f.thinking, [m]: l } })}
            />
          ))}
          {fetching && (
            <div aria-hidden className="flex flex-col gap-1.5 px-2 py-1.5">
              <div className="h-2.5 w-[70%] rounded-md bg-s3" />
              <div className="h-2.5 w-1/2 rounded-md bg-s3" />
            </div>
          )}
        </div>
      )}
      <div className="flex gap-2">
        <input
          className="in h-8 min-w-0 flex-auto text-[12px]"
          aria-label="Model id"
          placeholder="Model id"
          maxLength={128}
          value={typed}
          onChange={(e) => setTyped(e.target.value)}
          onKeyDown={(e) => {
            if (e.key !== "Enter") return;
            e.preventDefault(); // Enter adds the model, never saves the provider
            if (typed.trim()) add();
          }}
        />
        <button
          type="button"
          className="chip h-8 text-[12px]"
          disabled={!typed.trim()}
          onClick={add}
        >
          Add
        </button>
      </div>
    </div>
  );
}

const LEVEL_WORD: Record<Level, string> = {
  off: "Off",
  on: "On",
  low: "Low",
  medium: "Medium",
  high: "High",
};

/** One model's row: tick, id, custom tag, Default radio, × (custom); its thinking under it when chosen. */
function ModelRow({
  form: f,
  id,
  custom,
  onTick,
  onDefault,
  onLevel,
}: {
  form: ProviderForm;
  id: string;
  custom: boolean;
  onTick: (on: boolean) => void;
  onDefault: () => void;
  onLevel: (l: Level) => void;
}) {
  const chosen = f.models.includes(id);
  const isDefault = chosen && f.default_model === id;
  const info = f.levels[id];
  const level = levelOf(f, id);
  return (
    <div className={`flex flex-col gap-1.5 rounded-lg px-2 py-1.5 ${isDefault ? "bg-s2" : ""}`}>
      <div className="flex items-center gap-2.5 text-[12px]">
        <input
          type="checkbox"
          className="accent-[var(--a1)]"
          aria-label={`Use ${id}`}
          checked={chosen}
          onChange={(e) => onTick(e.target.checked)}
        />
        <span className="mono min-w-0 [overflow-wrap:anywhere]" title={id}>
          {id}
        </span>
        {custom && <span className="tag shrink-0">custom</span>}
        <div className="grow" />
        {chosen && (
          <label className={`flex shrink-0 items-center gap-1.5 ${isDefault ? "" : "text-t2"}`}>
            <input
              type="radio"
              name="p-default"
              aria-label={`Default for ${id}`}
              className="accent-[var(--a1)]"
              checked={isDefault}
              onChange={onDefault}
            />
            Default
          </label>
        )}
        {custom && (
          <button
            type="button"
            aria-label={`Remove ${id}`}
            className="size-[22px] shrink-0 rounded-full border-0 bg-s4 p-0 text-[12px]"
            onClick={() => onTick(false)}
          >
            ×
          </button>
        )}
      </div>
      {chosen && (
        <div className="flex flex-wrap items-center gap-2 pl-[26px] text-[12px]">
          {info?.always ? (
            <span className="text-t2">Always thinks</span>
          ) : info && info.levels.length > 0 ? (
            <>
              <span className="text-t2">Thinking</span>
              <div
                role="radiogroup"
                aria-label={`Thinking for ${id}`}
                className="flex rounded-lg bg-s1 p-0.5"
              >
                {info.levels.map((l) => (
                  <button
                    key={l}
                    type="button"
                    role="radio"
                    aria-checked={level === l}
                    className="h-6 rounded-md border-0 px-2.5 text-[12px] text-t2 aria-checked:bg-s5 aria-checked:text-t1"
                    onClick={() => onLevel(l)}
                  >
                    {LEVEL_WORD[l]}
                  </button>
                ))}
              </div>
              {level && level === info.default && (
                <span className="whitespace-nowrap text-t2">provider default</span>
              )}
            </>
          ) : (
            <span className="text-t2">No thinking setting</span>
          )}
        </div>
      )}
    </div>
  );
}
