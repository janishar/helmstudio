"use client";
// Admin > AI agents (docs/mock/AdminAgents.dc.html; plan 21 25.6): each AI job and the model that does it, a table
// with the editor beside it, as Model providers. Until branch assistant merges the table holds Session names alone
// (the board's "Before branch assistant merges"). Session names: Off · Local LLM (the llm pack, greyed while no host
// has it) · Remote (an enabled provider of Model providers: a remote one sends the prompt out and needs the
// deployment's switch, a local server is "on this network").
import Link from "next/link";
import { useCallback, useEffect, useState } from "react";
import { ErrorNote, Info } from "@/components/kit";
import { AdminTabs } from "@/components/admin/users";
import { useUser } from "@/components/shell";
import { api, message, ok, type S } from "@/lib/api";

type Names = S<"SessionNames">;
type Provider = S<"Provider">;
type Mode = Names["mode"];

const LOCAL = "Qwen3.8 27B";

/** The providers Remote offers: enabled ones, a remote one only while the deployment's switch is on. */
export const pickable = (ps: Provider[], remoteOn: boolean) =>
  ps.filter((p) => p.enabled && (p.kind === "server" || remoteOn));

const titleOf = (p: Provider) => p.title || p.name;

/** What a provider is sent: "the prompt to DeepSeek", "the prompt to Ollama, on this network". */
export const sendsTo = (p: Provider) =>
  `the prompt to ${titleOf(p)}${p.kind === "server" ? ", on this network" : ""}`;

export function AgentsAdmin() {
  const me = useUser();
  const [names, setNames] = useState<Names | null>(null);
  const [providers, setProviders] = useState<Provider[]>([]);
  const [error, setError] = useState("");
  const load = useCallback(() => {
    Promise.all([
      ok(api.GET("/v1/studio/admin/session-names")),
      ok(api.GET("/v1/studio/admin/providers")),
    ])
      .then(([n, p]) => {
        setNames(n);
        setProviders(p.providers);
        setError("");
      })
      .catch((e) => setError(message(e)));
  }, []);
  useEffect(() => {
    load();
  }, [load]);

  if (me && !me.roles.includes("admin"))
    return (
      <main id="main" className="flex flex-1 items-center justify-center p-6 text-t2">
        Admins only.
      </main>
    );
  const provider = providers.find((p) => p.name === names?.provider);
  const cell = "border-b border-s3 p-2.5 align-top";
  return (
    <>
      <main id="main" className="flex min-w-0 flex-[999_1_560px] flex-col gap-4 px-5 pt-4 pb-6">
        <div className="flex flex-wrap items-center gap-3">
          <h1 className="font-display text-[22px] font-bold tracking-[0.01em]">Admin</h1>
          <div className="grow" />
          <span className="tag tag-accent">Signed in as root admin</span>
        </div>
        <AdminTabs current="agents" />
        <span className="flex items-center text-[13px] text-t2">
          Each AI job and the model that does it. Providers and their keys are set in Model
          providers.
          <Info>
            Local runs on your own GPU hosts in the shared queue and sends nothing out. Remote sends
            what the row says to that provider, and only while the deployment&apos;s remote switch
            (REMOTE_MODELS) is on.
          </Info>
        </span>
        {error && <ErrorNote onRetry={load}>{error}</ErrorNote>}
        <div className="panel min-h-0 overflow-auto px-2 py-1.5" aria-busy={!names && !error}>
          <table className="w-full min-w-[620px] border-collapse text-[13px]">
            <thead>
              <tr className="text-left text-[11px] tracking-[0.04em] text-t2">
                {["Job", "Model", "Sends", "Status", "Actions"].map((h) => (
                  <th key={h} scope="col" className="border-b border-s3 p-2.5 font-semibold">
                    {h}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {names && (
                <tr>
                  <td className={cell}>
                    Session names
                    <br />
                    <span className="mono text-[12px] text-t2">
                      names an untitled session after its first run
                    </span>
                  </td>
                  <td className={`${cell} mono text-[12px] text-t2`}>
                    {names.mode === "off"
                      ? "Off"
                      : names.mode === "local"
                        ? LOCAL
                        : `${provider ? titleOf(provider) : names.provider} · ${names.model}`}
                  </td>
                  <td className={cell}>
                    {names.mode === "remote" && provider ? (
                      <>
                        <span className={`tag ${provider.kind === "remote" ? "tag-accent" : ""}`}>
                          {provider.kind === "remote" ? "remote" : "on this network"}
                        </span>
                        <br />
                        <span className="text-[12px] text-t2">{sendsTo(provider)}</span>
                      </>
                    ) : (
                      <span className="text-[12px] text-t2">
                        {names.mode === "local" ? "nothing out" : "—"}
                      </span>
                    )}
                  </td>
                  <td className={cell}>
                    {names.mode === "off" ? "Off" : "On"}
                    <br />
                    <span className="text-[12px] text-t2">
                      {names.mode === "off" ? "titles stay as they are" : "never over your titles"}
                    </span>
                  </td>
                  <td className={cell}>
                    <a
                      href="#session-names"
                      className="chip bevel inline-flex h-[30px] items-center"
                    >
                      Edit
                    </a>
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </main>
      {names && (
        <NamesPanel
          key={names.updated_at ?? "unset"}
          names={names}
          providers={pickable(providers, names.remote_enabled)}
          onSaved={setNames}
        />
      )}
    </>
  );
}

/** The Session names editor: Off · Local LLM · Remote, then Save. */
function NamesPanel({
  names,
  providers,
  onSaved,
}: {
  names: Names;
  /** the ones Remote may pick */
  providers: Provider[];
  onSaved: (n: Names) => void;
}) {
  const [mode, setMode] = useState<Mode>(names.mode);
  const [name, setName] = useState(names.provider ?? providers[0]?.name ?? "");
  const p = providers.find((x) => x.name === name) ?? providers[0];
  const [model, setModel] = useState(names.model ?? p?.default_model ?? "");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const chosenModel = p?.models.includes(model) ? model : p?.default_model;
  async function save(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      onSaved(
        await ok(
          api.PUT("/v1/studio/admin/session-names", {
            body:
              mode === "remote" && p
                ? { mode, provider: p.name, ...(chosenModel && { model: chosenModel }) }
                : { mode },
          }),
        ),
      );
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  }
  const noRemote = !providers.length;
  const option = (
    m: Mode,
    title: string,
    lines: React.ReactNode[],
    disabled = false,
    hint?: string,
  ) => (
    <label
      aria-disabled={disabled || undefined}
      title={hint}
      className="flex items-start gap-2.5 rounded-[10px] bg-s3 px-3 py-2.5 text-[13px] has-[:checked]:bg-s5 aria-disabled:opacity-55"
    >
      <input
        type="radio"
        name="session-names"
        className="mt-0.5 accent-[var(--a1)]"
        checked={mode === m}
        disabled={disabled && mode !== m}
        onChange={() => setMode(m)}
      />
      <span className="flex flex-col gap-0.5">
        <b className="font-semibold">{title}</b>
        {lines.map((l, i) => (
          <span key={i} className="text-[12px] text-t2">
            {l}
          </span>
        ))}
      </span>
    </label>
  );
  return (
    <aside
      id="session-names"
      aria-label="Session names"
      className="aside m-3.5 flex flex-[1_1_340px] flex-col px-[18px] pt-4 pb-6 md:ml-0"
    >
      <form onSubmit={save} className="flex flex-col gap-3">
        <h2 className="flex items-center font-display text-base leading-5 font-bold">
          Session names
          <Info>
            When a session nobody titled finishes its first run, this model writes a 2-5 word title
            from that run&apos;s prompt. A title you typed is never replaced. Off until you turn it
            on.
          </Info>
        </h2>
        <span className="text-[13px] text-t2">
          Names an untitled session from its first prompt.
        </span>
        <div role="radiogroup" aria-label="Session names" className="flex flex-col gap-1.5">
          {option("off", "Off", ["Titles stay as they are"])}
          {option(
            "local",
            "Local LLM",
            names.local_available
              ? [`${LOCAL} · uses the GPU queue · sends nothing out`]
              : [
                  <>
                    {LOCAL} not downloaded · <Link href="/models">Models</Link>
                  </>,
                  "Uses the GPU queue · sends nothing out",
                ],
            !names.local_available,
            names.local_available ? undefined : `An admin downloads ${LOCAL} in Models`,
          )}
          {option(
            "remote",
            "Remote",
            [
              noRemote
                ? names.remote_enabled
                  ? "No enabled provider: add one in Model providers"
                  : "remote models are off on this deployment"
                : p && `${titleOf(p)} · ${chosenModel}`,
            ],
            noRemote,
          )}
        </div>
        {mode === "remote" && p && (
          <>
            <div className="grid grid-cols-2 gap-2">
              <div className="flex min-w-0 flex-col gap-1.5">
                <label htmlFor="s-prov" className="label">
                  Provider
                </label>
                <select
                  id="s-prov"
                  className="in"
                  value={p.name}
                  onChange={(e) => {
                    setName(e.target.value);
                    setModel("");
                  }}
                >
                  {providers.map((x) => (
                    <option key={x.name} value={x.name}>
                      {titleOf(x)}
                      {x.kind === "server" ? " (on this network)" : ""}
                    </option>
                  ))}
                </select>
              </div>
              <div className="flex min-w-0 flex-col gap-1.5">
                <label htmlFor="s-model" className="label">
                  Model
                </label>
                <select
                  id="s-model"
                  className="in"
                  value={chosenModel}
                  onChange={(e) => setModel(e.target.value)}
                >
                  {p.models.map((m) => (
                    <option key={m}>{m}</option>
                  ))}
                </select>
              </div>
            </div>
            <span className="text-[12px] text-t2">
              Sends {sendsTo(p)}. Never images or references.
            </span>
            <span className="text-[12px] text-t2">
              Users see &quot;named by {titleOf(p)}&quot; once
              {p.kind === "remote" ? ", and a line in the session menu" : ""}.
            </span>
          </>
        )}
        {error && <ErrorNote>{error}</ErrorNote>}
        <button type="submit" className="key h-12 text-sm" disabled={busy}>
          Save
        </button>
      </form>
    </aside>
  );
}
