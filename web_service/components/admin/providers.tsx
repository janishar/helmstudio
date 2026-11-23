"use client";
// Admin > Model providers (docs/mock/Providers.dc.html): local servers (any OpenAI-compatible endpoint on a loopback or
// private address, with the memory their GPU host keeps reserved) and remote providers (https, only while the
// deployment's remote switch is on). Test, Edit, Enable/Disable, Delete per row; the form adds or replaces one. Keys
// are never shown or typed: a remote provider names the secret file pipelines_service reads. API 1.51 (plan 25): a
// Preset fills the form, Fetch lists the provider's models (asked by pipelines, from the preset's or the saved URL only)
// as a tick list, and the key file's status line says Found or Missing with Recheck.
import { useCallback, useEffect, useState } from "react";
import { ErrorNote, Info, Modal } from "@/components/kit";
import { AdminTabs } from "@/components/admin/users";
import { useUser } from "@/components/shell";
import { TextArea } from "@/components/expand-text";
import { api, message, ok, type S } from "@/lib/api";
import {
  blankForm,
  compatLine,
  fetchFrom,
  formOf,
  KEY,
  modelsOf,
  presetForm,
  putOf,
  testLine,
  tickModel,
  toggled,
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
  setForm: (f: ProviderForm) => void;
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
  function tick(id: string, on: boolean) {
    const r = tickModel(f, id, on);
    if ("error" in r) return setFx({ ...cur, tickError: r.error });
    setFx({ ...cur, tickError: "" });
    setForm(r.form);
  }
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
  const models = modelsOf(f);
  const shown = fetched?.filter((m) => m.toLowerCase().includes(filter.trim().toLowerCase()));
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
            Pick a preset to fill the form; every field stays editable. A local server is any
            OpenAI-compatible endpoint you run (Ollama, LM Studio, llama-server) on a loopback or
            private address; the host keeps its memory reserved. A remote provider is https and
            needs the deployment&apos;s remote switch. Keys are never entered here: put the key in
            the key file the server reads.
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
              setForm(p ? presetForm(p, f) : { ...f, preset: "" });
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
        <div className="flex flex-col gap-1.5">
          <div className="flex items-center gap-2">
            <label htmlFor="p-models" className="label flex-auto">
              Models (the first is the default)
            </label>
            <button
              type="button"
              className="chip h-[26px] text-[12px]"
              disabled={!from || fetching}
              onClick={() => void fetchModels()}
            >
              {fetching ? "Fetching…" : "Fetch"}
            </button>
          </div>
          <TextArea
            id="p-models"
            expand="Models"
            className="in h-auto py-2"
            rows={2}
            value={f.models}
            onChange={(e) => set("models")(e.target.value)}
          />
          {!from && f.base_url.trim() && (
            <span className="text-[12px] text-t2">
              Fetch asks only the preset&apos;s or the saved URL. Save first.
            </span>
          )}
          {compatLine(f.compat) && (
            <span className="text-[12px] text-t2">{compatLine(f.compat)}</span>
          )}
        </div>
        {fetchError && (
          <div
            role="alert"
            className="flex items-center gap-2 rounded-[10px] bg-[color-mix(in_srgb,#ff6b6b_14%,transparent)] px-2.5 py-2 text-[12px] text-[#ff8a8a]"
          >
            <span className="flex-auto">{fetchError}</span>
            <button
              type="button"
              className="chip h-[26px] text-[12px] text-t1"
              onClick={() => void fetchModels()}
            >
              Retry
            </button>
          </div>
        )}
        {fetchError && preset && (
          <span className="text-[12px] text-t2">The preset&apos;s models stay in the list.</span>
        )}
        {fetched && shown && (
          <div
            role="group"
            aria-label="Fetched models"
            className="flex max-h-[260px] flex-col gap-0.5 overflow-auto rounded-xl border border-s3 bg-s1 p-1.5"
          >
            {fetched.length > 12 && (
              <input
                className="in mb-1 h-8 text-[12px]"
                aria-label="Filter models"
                placeholder="Filter"
                value={filter}
                onChange={(e) => setFx({ ...cur, filter: e.target.value })}
              />
            )}
            <span className="px-2 pt-0.5 pb-1 text-[12px] text-t2">
              {fetched.length} found · tick to add
            </span>
            {tickError && <span className="px-2 text-[12px] text-bad">{tickError}</span>}
            {shown.map((m) => (
              <label key={m} className="flex items-center gap-2.5 rounded-lg px-2 py-1 text-[12px]">
                <input
                  type="checkbox"
                  className="accent-[var(--a1)]"
                  checked={models.includes(m)}
                  onChange={(e) => tick(m, e.target.checked)}
                />
                <span className="mono">{m}</span>
              </label>
            ))}
          </div>
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
          <KeyLine status={key} onRecheck={() => setRecheck((n) => n + 1)} />
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

/** The key file's status: a dot, Found or Missing, the deploy-side path, Recheck (the key is never shown). */
function KeyLine({ status, onRecheck }: { status: S<"ProviderKey">; onRecheck: () => void }) {
  const colour = status.found ? "var(--a1)" : "#f2c14e";
  return (
    <div className="flex flex-col gap-1.5">
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
      {!status.found && (
        <span className="text-[12px] text-t2">
          Put the key in this file, then Recheck. The key is never shown here.
        </span>
      )}
    </div>
  );
}
