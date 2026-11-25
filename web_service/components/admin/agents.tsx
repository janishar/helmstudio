"use client";
// Admin > AI agents (docs/mock/AdminAgents.dc.html; plan 21 25.6): each AI job and the model that does it, a table
// with the editor beside it, as Model providers. Session names: Off · Local LLM (the llm pack, greyed while no host has
// it) · Remote (an enabled provider of Model providers: a remote one sends the prompt out and needs the deployment's
// switch, a local server is "on this network"); Edit marks its row and moves focus to the panel beside. Plan 32: the
// Assistant has its own page (/assistant); a line at the top links there.
import Link from "next/link";
import { useCallback, useEffect, useRef, useState } from "react";
import { Sparkles } from "lucide-react";
import { ErrorNote, Info } from "@/components/kit";
import { AdminTabs } from "@/components/admin/users";
import { useUser } from "@/components/shell";
import { api, message, ok, type S } from "@/lib/api";
import { pickable, sendsTo, titleOf, withThinking } from "@/lib/providers";

type Names = S<"SessionNames">;
type Provider = S<"Provider">;
type Mode = Names["mode"];

const LOCAL = "Qwen3.8 27B";

export function AgentsAdmin() {
  const me = useUser();
  const [names, setNames] = useState<Names | null>(null);
  const [providers, setProviders] = useState<Provider[]>([]);
  const [error, setError] = useState("");
  const [editing, setEditing] = useState(false);
  const namesPanel = useRef<HTMLElement>(null);
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
  // a saved provider that was turned off (or is remote while the switch is off) is shown as off, not as in use
  const live = (p: Provider | undefined, remoteOn = false) =>
    !!p && pickable([p], remoteOn).length > 0;
  const offNote = (title: string) => (
    <span className="text-[#f2c14e]">{title} is off · Edit to pick another</span>
  );
  const nLive = live(provider, names?.remote_enabled);
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
        <div
          role="note"
          className="flex items-center gap-2.5 rounded-xl bg-s1 px-3.5 py-2.5 text-[13px]"
        >
          <Sparkles size={16} strokeWidth={2} aria-hidden className="shrink-0 text-a1" />
          <span className="flex-auto">The Assistant has its own page.</span>
          <Link
            href="/assistant"
            aria-label="Open the Assistant page"
            className="font-semibold text-t1"
          >
            Open
          </Link>
        </div>
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
                <tr aria-selected={editing} className={editing ? "bg-s3" : undefined}>
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
                        : nLive
                          ? `${titleOf(provider!)} · ${names.model}`
                          : offNote(provider ? titleOf(provider) : (names.provider ?? "Provider"))}
                  </td>
                  <td className={cell}>
                    {names.mode === "remote" && provider && nLive ? (
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
                    <button
                      type="button"
                      className="chip bevel h-[30px]"
                      aria-controls="session-names"
                      onClick={() => {
                        setEditing(true);
                        const panel = namesPanel.current;
                        panel?.scrollIntoView?.({ block: "nearest" });
                        // a selector list matches in page order, so the checked option is asked first
                        (
                          panel?.querySelector<HTMLElement>("input[type=radio]:checked") ??
                          panel?.querySelector<HTMLElement>("input, select")
                        )?.focus();
                      }}
                    >
                      Edit
                    </button>
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </main>
      {names && (
        <NamesPanel
          ref={namesPanel}
          key={names.updated_at ?? "unset"}
          names={names}
          providers={pickable(providers, names.remote_enabled).filter((p) => !p.owner_id)}
          onSaved={(n) => {
            setNames(n);
            setEditing(false);
          }}
        />
      )}
    </>
  );
}

/** The Session names editor: Off · Local LLM · Remote, then Save. */
function NamesPanel({
  ref,
  names,
  providers,
  onSaved,
}: {
  ref: React.Ref<HTMLElement>;
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
      ref={ref}
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
                    <option key={m} value={m}>
                      {withThinking(m, p.thinking?.[m])}
                      {m === p.default_model ? " (default)" : ""}
                    </option>
                  ))}
                </select>
              </div>
            </div>
            {p.thinking_levels && (
              <span className="text-[12px] text-t2">Set per model in Model providers.</span>
            )}
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
