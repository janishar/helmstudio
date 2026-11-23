"use client";
// Admin > AI agents (docs/mock/AdminAgents.dc.html; plan 21 25.6): each AI job and the model that does it, a table
// with the editor beside it, as Model providers. Assistant (plan 18, /v1/studio/admin/assistant): on or off, provider
// and model, web search and the caps, edited in its section under the table. Session names: Off · Local LLM (the llm
// pack, greyed while no host has it) · Remote (an enabled provider of Model providers: a remote one sends the prompt
// out and needs the deployment's switch, a local server is "on this network"). Plan 25 (29.9): the Assistant box is
// closed until its row's Edit, which opens it, marks the row and focuses its first field (Cancel or Save closes it);
// Session names' Edit marks its row and moves focus to the panel beside.
import Link from "next/link";
import { useCallback, useEffect, useRef, useState } from "react";
import { ErrorNote, Info } from "@/components/kit";
import { AdminTabs } from "@/components/admin/users";
import { useUser } from "@/components/shell";
import { api, message, ok, type S } from "@/lib/api";

type Names = S<"SessionNames">;
type Settings = S<"AssistantSettings">;
type Limits = S<"AssistantLimits">;
type Provider = S<"Provider">;
type Mode = Names["mode"];

const LOCAL = "Qwen3.8 27B";

/** The model providers a job may pick: enabled servers, and remote ones while the deployment's switch is on. */
export const pickable = (ps: Provider[], remoteOn: boolean) =>
  ps.filter((p) => p.enabled && (p.kind === "server" || (p.kind === "remote" && remoteOn)));

/** Web search's providers: enabled ones of kind search, only while the switch is on (a search leaves the machine). */
export const searchable = (ps: Provider[], remoteOn: boolean) =>
  remoteOn ? ps.filter((p) => p.enabled && p.kind === "search") : [];

const titleOf = (p: Provider) => p.title || p.name;

/** What a provider is sent: "the prompt to DeepSeek", "the prompt to Ollama, on this network". */
export const sendsTo = (p: Provider) =>
  `the prompt to ${titleOf(p)}${p.kind === "server" ? ", on this network" : ""}`;

export function AgentsAdmin() {
  const me = useUser();
  const [names, setNames] = useState<Names | null>(null);
  const [assistant, setAssistant] = useState<Settings | null>(null);
  const [providers, setProviders] = useState<Provider[]>([]);
  const [error, setError] = useState("");
  const [editing, setEditing] = useState<"assistant" | "names" | null>(null);
  const assistantBox = useRef<HTMLFormElement>(null);
  const namesPanel = useRef<HTMLElement>(null);
  // after Edit opens the Assistant box: scroll to it, focus its first field
  useEffect(() => {
    if (editing !== "assistant") return;
    const box = assistantBox.current;
    box?.scrollIntoView?.({ block: "nearest" });
    box?.querySelector<HTMLElement>("[role=switch], select, input")?.focus();
  }, [editing]);
  const load = useCallback(() => {
    Promise.all([
      ok(api.GET("/v1/studio/admin/session-names")),
      ok(api.GET("/v1/studio/admin/providers")),
      ok(api.GET("/v1/studio/admin/assistant")),
    ])
      .then(([n, p, a]) => {
        setNames(n);
        setAssistant(a);
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
  const aProvider = providers.find((p) => p.name === assistant?.provider);
  const aSearch = providers.find((p) => p.name === assistant?.search_provider);
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
              {assistant && (
                <tr
                  aria-selected={editing === "assistant"}
                  className={editing === "assistant" ? "bg-s3" : undefined}
                >
                  <td className={cell}>
                    Assistant
                    <br />
                    <span className="mono text-[12px] text-t2">the in-app assistant</span>
                  </td>
                  <td className={`${cell} mono text-[12px] text-t2`}>
                    {aProvider
                      ? `${titleOf(aProvider)} · ${assistant.model ?? aProvider.default_model}`
                      : "—"}
                    {assistant.web_search && (
                      <>
                        <br />
                        web search: {aSearch ? titleOf(aSearch) : assistant.search_provider}
                      </>
                    )}
                  </td>
                  <td className={cell}>
                    {aProvider ? (
                      <>
                        <span className={`tag ${aProvider.kind === "remote" ? "tag-accent" : ""}`}>
                          {aProvider.kind === "remote" ? "remote" : "on this network"}
                        </span>
                        <br />
                        <span
                          className={`text-[12px] ${aProvider.kind === "remote" ? "text-[#f2c14e]" : "text-t2"}`}
                        >
                          your messages to {titleOf(aProvider)}
                        </span>
                      </>
                    ) : (
                      <span className="text-[12px] text-t2">—</span>
                    )}
                  </td>
                  <td className={cell}>
                    {assistant.enabled ? "On" : "Off"}
                    <br />
                    <span className="text-[12px] text-t2">
                      {assistant.enabled ? capsLine(assistant.limits) : "no Assistant in the dock"}
                    </span>
                  </td>
                  <td className={cell}>
                    <button
                      type="button"
                      className="chip bevel h-[30px]"
                      aria-expanded={editing === "assistant"}
                      aria-controls="assistant-settings"
                      onClick={() => setEditing("assistant")}
                    >
                      Edit
                    </button>
                  </td>
                </tr>
              )}
              {names && (
                <tr
                  aria-selected={editing === "names"}
                  className={editing === "names" ? "bg-s3" : undefined}
                >
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
                    <button
                      type="button"
                      className="chip bevel h-[30px]"
                      aria-controls="session-names"
                      onClick={() => {
                        setEditing("names");
                        const panel = namesPanel.current;
                        panel?.scrollIntoView?.({ block: "nearest" });
                        panel
                          ?.querySelector<HTMLElement>("input[type=radio]:checked, input, select")
                          ?.focus();
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
        {assistant && editing === "assistant" && (
          <AssistantPanel
            ref={assistantBox}
            key={assistant.updated_at ?? "unset"}
            settings={assistant}
            providers={pickable(providers, assistant.remote_enabled)}
            search={searchable(providers, assistant.remote_enabled)}
            onSaved={(a) => {
              setAssistant(a);
              setEditing(null);
            }}
            onClose={() => setEditing(null)}
          />
        )}
      </main>
      {names && (
        <NamesPanel
          ref={namesPanel}
          key={names.updated_at ?? "unset"}
          names={names}
          providers={pickable(providers, names.remote_enabled)}
          onSaved={(n) => {
            setNames(n);
            setEditing((e) => (e === "names" ? null : e));
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

/** "200 calls · 2 M tokens a day" */
export const capsLine = (l: Limits) =>
  `${l.model_calls_per_day} calls · ${
    l.tokens_per_day >= 1e6 && l.tokens_per_day % 1e5 === 0
      ? `${l.tokens_per_day / 1e6} M`
      : l.tokens_per_day.toLocaleString("en")
  } tokens a day`;

const capsText = (l: Limits) =>
  Object.fromEntries(CAPS.map(([k]) => [k, String(l[k])])) as Record<keyof Limits, string>;

const CAPS: [keyof Limits, string][] = [
  ["model_calls_per_day", "Model calls a day"],
  ["tokens_per_day", "Tokens a day"],
  ["look_at_per_hour", "Look at, an hour"],
  ["messages_per_minute", "Messages a minute"],
];

/** The Assistant's settings under the table: on or off, provider and model, web search, caps, then Cancel and Save. */
function AssistantPanel({
  ref,
  settings,
  providers,
  search,
  onSaved,
  onClose,
}: {
  ref: React.Ref<HTMLFormElement>;
  settings: Settings;
  /** enabled servers, and remote ones while the switch is on */
  providers: Provider[];
  /** enabled search providers while the switch is on */
  search: Provider[];
  onSaved: (s: Settings) => void;
  /** Cancel: the box closes (it opens fresh from the saved settings) */
  onClose: () => void;
}) {
  const [enabled, setEnabled] = useState(settings.enabled);
  const [name, setName] = useState(settings.provider ?? providers[0]?.name ?? "");
  const p = providers.find((x) => x.name === name) ?? providers[0];
  const [model, setModel] = useState(settings.model ?? "");
  const [web, setWeb] = useState(settings.web_search && search.length > 0);
  const [sName, setSName] = useState(settings.search_provider ?? search[0]?.name ?? "");
  const sp = search.find((x) => x.name === sName) ?? search[0];
  const [caps, setCaps] = useState(() => capsText(settings.limits));
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  // "" = its default_model (sent as null)
  const chosenModel = p?.models.includes(model) && model !== p.default_model ? model : "";
  async function save(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      onSaved(
        await ok(
          api.PUT("/v1/studio/admin/assistant", {
            body: {
              enabled,
              provider: p?.name ?? null,
              model: chosenModel || null,
              web_search: web && !!sp,
              search_provider: web && sp ? sp.name : null,
              limits: Object.fromEntries(
                CAPS.map(([k]) => [k, Number(caps[k])]),
              ) as unknown as Limits,
            },
          }),
        ),
      );
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  }
  const field = "flex min-w-0 flex-col gap-1.5";
  return (
    <form
      ref={ref}
      id="assistant-settings"
      aria-label="Assistant settings"
      onSubmit={save}
      className="panel flex flex-col gap-3 px-[18px] py-4"
    >
      <div className="flex items-center justify-between gap-3">
        <h2 id="a-on-l" className="flex items-center font-display text-base font-bold">
          Assistant
          <Info>
            The in-app assistant: it answers in a drawer and can act on the screen. Applies from
            each chat&apos;s next message. Off hides it from the dock.
          </Info>
        </h2>
        <button
          type="button"
          role="switch"
          aria-checked={enabled}
          aria-label="Assistant on"
          onClick={() => setEnabled(!enabled)}
          className="flex h-[26px] w-11 shrink-0 rounded-full border border-line p-0.5"
          style={{ background: enabled ? "var(--a1)" : "var(--s1)" }}
        >
          <span
            className="block size-5 rounded-full transition-transform"
            style={{
              background: enabled ? "var(--on1)" : "var(--t2)",
              transform: enabled ? "translateX(18px)" : undefined,
            }}
          />
        </button>
      </div>
      {providers.length ? (
        <div className="grid grid-cols-2 gap-2">
          <div className={field}>
            <label htmlFor="a-prov" className="label">
              Provider
            </label>
            <select
              id="a-prov"
              className="in"
              value={p?.name}
              onChange={(e) => {
                setName(e.target.value);
                setModel("");
              }}
            >
              {providers.map((x) => (
                <option key={x.name} value={x.name}>
                  {titleOf(x)} ({x.kind === "remote" ? "remote" : "local server"})
                </option>
              ))}
            </select>
          </div>
          <div className={field}>
            <label htmlFor="a-model" className="label">
              Model
            </label>
            <select
              id="a-model"
              className="in"
              value={chosenModel}
              onChange={(e) => setModel(e.target.value)}
            >
              {p?.models.map((m) => (
                <option key={m} value={m === p.default_model ? "" : m}>
                  {m}
                  {m === p.default_model ? " (default)" : ""}
                </option>
              ))}
            </select>
          </div>
        </div>
      ) : (
        <span className="text-[13px] text-t2">
          {settings.remote_enabled
            ? "No enabled provider: add one in Model providers"
            : "No enabled server: add one in Model providers (remote models are off on this deployment)"}
        </span>
      )}
      <div className="grid grid-cols-2 items-end gap-2">
        <label
          aria-disabled={!search.length || undefined}
          title={
            search.length
              ? undefined
              : settings.remote_enabled
                ? "Add a search provider in Model providers"
                : "remote models are off on this deployment"
          }
          className="flex min-h-[38px] items-center gap-2.5 text-[13px] aria-disabled:opacity-55"
        >
          <input
            type="checkbox"
            className="accent-[var(--a1)]"
            checked={web}
            disabled={!search.length}
            onChange={(e) => setWeb(e.target.checked)}
          />
          Web search
        </label>
        {web && sp && (
          <div className={field}>
            <label htmlFor="a-search" className="label">
              Search provider
            </label>
            <select
              id="a-search"
              className="in"
              value={sp.name}
              onChange={(e) => setSName(e.target.value)}
            >
              {search.map((x) => (
                <option key={x.name} value={x.name}>
                  {titleOf(x)} (search)
                </option>
              ))}
            </select>
          </div>
        )}
      </div>
      <span className="text-[12px] font-semibold tracking-[0.04em] text-t2">Caps per user</span>
      <div className="grid grid-cols-2 gap-2 md:grid-cols-4">
        {CAPS.map(([k, label]) => (
          <div key={k} className={field}>
            <label htmlFor={`a-${k}`} className="label">
              {label}
            </label>
            <input
              id={`a-${k}`}
              className="in"
              type="number"
              min={1}
              required
              value={caps[k]}
              onChange={(e) => setCaps({ ...caps, [k]: e.target.value })}
            />
          </div>
        ))}
      </div>
      {error && <ErrorNote>{error}</ErrorNote>}
      <div className="flex flex-wrap items-center gap-2">
        {p && (
          <span className={`text-[12px] ${p.kind === "remote" ? "text-[#f2c14e]" : "text-t2"}`}>
            Sends your messages, the screen summary and tool results to {titleOf(p)}
            {p.kind === "server" ? ", on this network" : ""}; never images.
          </span>
        )}
        <div className="grow" />
        <button type="button" className="chip bevel" onClick={onClose}>
          Cancel
        </button>
        <button type="submit" className="key h-[38px] px-[18px] text-[13px]" disabled={busy}>
          Save
        </button>
      </div>
    </form>
  );
}
