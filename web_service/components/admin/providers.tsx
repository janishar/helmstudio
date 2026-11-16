"use client";
// Admin > Model providers (docs/mock/Providers.dc.html): local servers (any OpenAI-compatible endpoint on a loopback or
// private address, with the memory their GPU host keeps reserved) and remote providers (https, only while the
// deployment's remote switch is on). Test, Edit, Enable/Disable, Delete per row; the form adds or replaces one. Keys
// are never shown or typed: a remote provider names the secret file pipelines_service reads.
import { useCallback, useEffect, useState } from "react";
import { ErrorNote, Info, Modal } from "@/components/kit";
import { AdminTabs } from "@/components/admin/users";
import { useUser } from "@/components/shell";
import { api, message, ok, type S } from "@/lib/api";
import { blankForm, formOf, putOf, testLine, toggled, type ProviderForm } from "@/lib/providers";

type Provider = S<"Provider">;
const path = (name: string) => ({ params: { path: { name } } });

export function ProvidersAdmin() {
  const me = useUser();
  const [list, setList] = useState<S<"ProviderList"> | null>(null);
  const [error, setError] = useState("");
  const [form, setForm] = useState<ProviderForm>(blankForm());
  const [tests, setTests] = useState<Record<string, string>>({});
  const [deleting, setDeleting] = useState<Provider | null>(null);
  const load = useCallback(() => {
    ok(api.GET("/v1/studio/admin/providers"))
      .then((l) => {
        setList(l);
        setError("");
      })
      .catch((e) => setError(message(e)));
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

/** Add a model provider (or edit one: same name replaces it). */
function ProviderPanel({
  form: f,
  setForm,
  remoteOn,
  exists,
  onSaved,
}: {
  form: ProviderForm;
  setForm: (f: ProviderForm) => void;
  remoteOn: boolean;
  exists: boolean;
  onSaved: (p: Provider) => void;
}) {
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const set = (k: keyof ProviderForm) => (v: string | boolean) => setForm({ ...f, [k]: v });
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
        value={String(f[k])}
        onChange={(e) => set(k)(e.target.value)}
        {...extra}
      />
    </div>
  );
  return (
    <aside
      aria-label="Add a model provider"
      className="aside m-3.5 flex flex-[1_1_340px] flex-col px-[18px] pt-4 pb-6 md:ml-0"
    >
      <form onSubmit={submit} className="flex flex-col gap-3">
        <h2 className="flex items-center font-display text-base leading-5 font-bold">
          {exists ? `Edit ${f.name}` : "Add a model provider"}
          <Info>
            A local server is any OpenAI-compatible endpoint you run (Ollama, LM Studio,
            llama-server) on a loopback or private address; its GPU host keeps its memory reserved.
            A remote provider is https and needs the deployment&apos;s remote switch. Keys are never
            entered here: name the secret file the server reads.
          </Info>
        </h2>
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
        {field(
          "base_url",
          "Base URL",
          f.kind === "server"
            ? "http://host.docker.internal:11434/v1"
            : "https://api.example.com/v1",
          { maxLength: 300 },
        )}
        <div className="flex flex-col gap-1.5">
          <label htmlFor="p-models" className="label">
            Models, one per line (the first is the default)
          </label>
          <textarea
            id="p-models"
            className="in h-auto py-2"
            rows={2}
            value={f.models}
            onChange={(e) => set("models")(e.target.value)}
          />
        </div>
        {f.kind === "server" ? (
          <div className="grid grid-cols-3 gap-2">
            {field("host", "GPU host", "mac", { maxLength: 32 })}
            {field("memory_gb", "Memory, GB", "6.5", { inputMode: "decimal" })}
            {field("timeout_s", "Timeout, s", "120", { inputMode: "numeric" })}
          </div>
        ) : (
          <div className="grid grid-cols-3 gap-2">
            {field("key_secret", "Key file", "provider-name", { maxLength: 64 })}
            {field("max_in_flight", "In flight", "2", { inputMode: "numeric" })}
            {field("timeout_s", "Timeout, s", "120", { inputMode: "numeric" })}
          </div>
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
