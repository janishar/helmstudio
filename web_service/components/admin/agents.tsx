"use client";
// Hub › In pipelines (plan 33 37.9; docs/mock/Assistant.dc.html frame 24): the silent assistant, AI work done without a
// chat. A table of each job and the model that does it, then its two editors side by side:
// - Default model: what a pipeline write, decide or agent step's Runs on · Auto runs on (API 1.74): Local LLM (as
//   before; a judge always keeps its family's vision model), a shared provider model, or Same as Assistant. An
//   Only-you provider (Your Claude subscription) is never offered: pipelines run for every account.
// - Session names (was Admin › AI agents): Off · Local LLM (greyed while no host has it) · a provider model.
// Edit marks its row and moves focus to its editor.
import Link from "next/link";
import { useCallback, useEffect, useRef, useState } from "react";
import { ErrorNote, Info } from "@/components/kit";
import { HubHead, hubHref } from "@/components/hub";
import { api, message, ok, type S } from "@/lib/api";
import { pickable, sendsTo, titleOf, withThinking } from "@/lib/providers";

type Names = S<"SessionNames">;
type Default = S<"PipelineDefaultModel">;
type Provider = S<"Provider">;

const LOCAL = "Qwen3.8 27B";
const WARN =
  "rounded-[10px] bg-[color-mix(in_srgb,#f2c14e_14%,transparent)] px-3 py-2 text-[12px] text-[#f2c14e]";
const MONO = "mono text-[12px] text-t2";

export function InPipelines() {
  const [names, setNames] = useState<Names | null>(null);
  const [pick, setPick] = useState<Default | null>(null);
  const [providers, setProviders] = useState<Provider[]>([]);
  const [assistant, setAssistant] = useState<S<"AssistantSettings"> | null>(null);
  const [error, setError] = useState("");
  const [editing, setEditing] = useState<"steps" | "names" | null>(null);
  const stepsPanel = useRef<HTMLElement>(null);
  const namesPanel = useRef<HTMLElement>(null);
  const load = useCallback(() => {
    Promise.all([
      ok(api.GET("/v1/studio/admin/pipeline-model")),
      ok(api.GET("/v1/studio/admin/session-names")),
      ok(api.GET("/v1/studio/admin/providers")),
      ok(api.GET("/v1/studio/admin/assistant")),
    ])
      .then(([d, n, p, a]) => {
        setPick(d);
        setNames(n);
        setProviders(p.providers);
        setAssistant(a);
        setError("");
      })
      .catch((e) => setError(message(e)));
  }, []);
  useEffect(() => {
    load();
  }, [load]);

  const remoteOn = !!names?.remote_enabled;
  // shared ones only: pipelines and session names run for every account
  const shared = pickable(providers, remoteOn).filter((p) => !p.owner_id);
  const live = (name: string | null | undefined) => shared.find((p) => p.name === name);
  const offNote = (title: string) => (
    <span className="text-[#f2c14e]">{title} is off · Edit to pick another</span>
  );
  const edit = (which: "steps" | "names") => {
    setEditing(which);
    const panel = (which === "steps" ? stepsPanel : namesPanel).current;
    panel?.scrollIntoView?.({ block: "nearest" });
    // a selector list matches in page order, so the checked option is asked first
    (
      panel?.querySelector<HTMLElement>("input[type=radio]:checked") ??
      panel?.querySelector<HTMLElement>("input, select")
    )?.focus();
  };
  // the Assistant's provider as Same as Assistant follows it (the deployment's, never an admin's Only-you one)
  const asst = assistant && !assistant.personal ? live(assistant.provider) : undefined;
  const asstModel = asst && (assistant?.model ?? asst.default_model);
  const dp =
    pick?.mode === "remote" ? live(pick.provider) : pick?.mode === "assistant" ? asst : undefined;
  const dModel = pick?.mode === "remote" ? pick.model : asstModel;
  const np = names?.mode === "remote" ? live(names.provider) : undefined;
  const cell = "border-b border-s3 p-2.5 align-top";
  const sends = (p: Provider | undefined, local: boolean) =>
    p ? (
      <>
        <span className={`tag ${p.kind === "remote" ? "tag-accent" : ""}`}>
          {p.kind === "remote" ? "remote" : "on this network"}
        </span>
        <br />
        <span className={`text-[12px] ${p.kind === "remote" ? "text-[#f2c14e]" : "text-t2"}`}>
          {sendsTo(p)}
        </span>
      </>
    ) : local ? (
      <>
        <span className="tag">local</span>
        <br />
        <span className="text-[12px] text-t2">nothing out</span>
      </>
    ) : (
      <span className="text-[12px] text-t2">—</span>
    );
  return (
    <main
      id="main"
      className="apage flex min-w-0 flex-[999_1_560px] flex-col gap-4 px-5 pt-4 pb-16"
    >
      <HubHead
        tab="pipelines"
        line={
          <span className="flex items-center">
            The silent assistant: AI work done without a chat, in pipelines and on sessions.
            <Info>
              Local runs on your own GPU hosts in the shared queue and sends nothing out. A provider
              model sends what the row says to that provider, and only while the deployment&apos;s
              remote switch (REMOTE_MODELS) is on.
            </Info>
          </span>
        }
      />
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
            {pick && (
              <tr
                aria-selected={editing === "steps"}
                className={editing === "steps" ? "bg-s3" : undefined}
              >
                <td className={`${cell} font-semibold`}>
                  Pipeline steps
                  <br />
                  <span className={`${MONO} font-normal`}>
                    write, decide, agent and judge, on Runs on · Auto
                  </span>
                </td>
                <td className={`${cell} ${MONO}`}>
                  {pick.mode === "local"
                    ? `Local LLM · ${LOCAL}`
                    : dp
                      ? `${pick.mode === "assistant" ? "Same as Assistant · " : ""}${titleOf(dp)} · ${dModel}`
                      : offNote(
                          pick.mode === "assistant"
                            ? "The Assistant's model"
                            : (pick.provider ?? "Provider"),
                        )}
                  <br />
                  judge: its family&apos;s vision model
                </td>
                <td className={cell}>{sends(dp, pick.mode === "local" || !dp)}</td>
                <td className={cell}>
                  On
                  <br />
                  <span className="text-[12px] text-t2">a step that names a model keeps it</span>
                </td>
                <td className={cell}>
                  <button
                    type="button"
                    className="chip bevel h-[30px]"
                    aria-controls="default-model"
                    onClick={() => edit("steps")}
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
                <td className={`${cell} font-semibold`}>
                  Session names
                  <br />
                  <span className={`${MONO} font-normal`}>
                    names an untitled session after its first run
                  </span>
                </td>
                <td className={`${cell} ${MONO}`}>
                  {names.mode === "off"
                    ? "Off"
                    : names.mode === "local"
                      ? `Local LLM · ${LOCAL}`
                      : np
                        ? `${titleOf(np)} · ${names.model}`
                        : offNote(names.provider ?? "Provider")}
                </td>
                <td className={cell}>{sends(np, names.mode === "local")}</td>
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
                    onClick={() => edit("names")}
                  >
                    Edit
                  </button>
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
      {pick && names && (
        <div className="pgg">
          <div className="pgc">
            <DefaultPanel
              ref={stepsPanel}
              key={pick.updated_at ?? "unset"}
              saved={pick}
              providers={shared}
              assistant={asst && { p: asst, model: asstModel! }}
              assistantWhy={
                !assistant?.provider
                  ? "the Assistant is not set up"
                  : assistant.personal
                    ? "the Assistant answers only for you"
                    : !asst
                      ? "the Assistant is remote; remote models are off"
                      : ""
              }
              personal={providers.some((p) => p.owner_id)}
              remoteOn={remoteOn}
              onSaved={(d) => {
                setPick(d);
                setEditing(null);
              }}
            />
          </div>
          <div className="pgc">
            <NamesPanel
              ref={namesPanel}
              key={names.updated_at ?? "unset"}
              names={names}
              providers={shared}
              onSaved={(n) => {
                setNames(n);
                setEditing(null);
              }}
            />
          </div>
        </div>
      )}
    </main>
  );
}

/** One choice of a radio group, as the board draws it: a tile, ringed while checked. */
function Option({
  name,
  checked,
  disabled,
  title,
  lines,
  onPick,
}: {
  name: string;
  checked: boolean;
  disabled?: boolean;
  title: string;
  /** [text, mono] */
  lines: [React.ReactNode, boolean?][];
  onPick: () => void;
}) {
  return (
    <label
      aria-disabled={disabled || undefined}
      className="flex items-start gap-2.5 rounded-xl bg-s2 px-3 py-2.5 text-[13px] has-[:checked]:shadow-[inset_0_0_0_2px_var(--a1)] aria-disabled:text-t2"
    >
      <input
        type="radio"
        name={name}
        className="mt-[3px] accent-[var(--a1)]"
        checked={checked}
        disabled={disabled && !checked}
        onChange={onPick}
      />
      <span className="flex min-w-0 flex-col gap-0.5">
        <b className={`font-semibold ${disabled ? "opacity-55" : ""}`}>{title}</b>
        {lines.map(([l, mono], i) => (
          <span key={i} className={mono ? MONO : "text-[12px] text-t2"}>
            {l}
          </span>
        ))}
      </span>
    </label>
  );
}

/** Provider and Model pickers (greyed unless a provider model is picked). */
function ProviderModel({
  id,
  providers,
  p,
  model,
  disabled,
  setName,
  setModel,
}: {
  id: string;
  providers: Provider[];
  p: Provider | undefined;
  model: string | undefined;
  disabled: boolean;
  setName: (n: string) => void;
  setModel: (m: string) => void;
}) {
  return (
    <div className="g2">
      <div className="fl">
        <label htmlFor={`${id}-prov`}>Provider</label>
        <select
          id={`${id}-prov`}
          className="in"
          disabled={disabled || !p}
          value={p?.name ?? ""}
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
      <div className="fl">
        <label htmlFor={`${id}-model`}>Model</label>
        <select
          id={`${id}-model`}
          className="in"
          disabled={disabled || !p}
          value={model ?? ""}
          onChange={(e) => setModel(e.target.value)}
        >
          {p?.models.map((m) => (
            <option key={m} value={m}>
              {withThinking(m, p.thinking?.[m])}
              {m === p.default_model ? " (default)" : ""}
            </option>
          ))}
        </select>
      </div>
    </div>
  );
}

/** Cancel (back to what is saved) and Save. */
function Foot({ busy, onCancel }: { busy: boolean; onCancel: () => void }) {
  return (
    <div className="sv">
      <span className="flex-auto" />
      <button type="button" className="chip bevel h-[34px]" onClick={onCancel}>
        Cancel
      </button>
      <button type="submit" className="key h-[34px] px-[18px] text-[13px]" disabled={busy}>
        Save
      </button>
    </div>
  );
}

function useSaving<T>(save: () => Promise<T>, onSaved: (t: T) => void) {
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      onSaved(await save());
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  };
  return { error, busy, submit };
}

/** The Default model editor (frame 24): Local LLM · Provider model · Same as Assistant, then Save. */
function DefaultPanel({
  ref,
  saved,
  providers,
  assistant,
  assistantWhy,
  personal,
  remoteOn,
  onSaved,
}: {
  ref: React.Ref<HTMLElement>;
  saved: Default;
  providers: Provider[];
  assistant: { p: Provider; model: string } | undefined;
  /** why Same as Assistant is greyed */
  assistantWhy: string;
  /** an Only-you provider exists (Your Claude subscription): say why it is not offered */
  personal: boolean;
  remoteOn: boolean;
  onSaved: (d: Default) => void;
}) {
  const [mode, setMode] = useState(saved.mode);
  const [name, setName] = useState(saved.provider ?? providers[0]?.name ?? "");
  const [model, setModel] = useState(saved.model ?? "");
  const p = providers.find((x) => x.name === name) ?? providers[0];
  const chosen = p?.models.includes(model) ? model : p?.default_model;
  const { error, busy, submit } = useSaving(
    () =>
      ok(
        api.PUT("/v1/studio/admin/pipeline-model", {
          body:
            mode === "remote" && p
              ? { mode, provider: p.name, ...(chosen && { model: chosen }) }
              : { mode },
        }),
      ),
    onSaved,
  );
  const goes = mode === "remote" ? p : mode === "assistant" ? assistant?.p : undefined;
  return (
    <section ref={ref} id="default-model" aria-label="Default model" className="panel sec">
      <form onSubmit={submit} className="flex flex-col gap-3">
        <div className="pgh">
          <h2>Default model</h2>
        </div>
        <span className="line">
          What Runs on · Auto means for write, decide, agent and judge steps.
        </span>
        <div role="radiogroup" aria-label="Default model" className="flex flex-col gap-2">
          <Option
            name="pd"
            checked={mode === "local"}
            title="Local LLM"
            lines={[
              [LOCAL, true],
              ["A judge uses its family's own vision model"],
              ["Uses the GPU queue · sends nothing out"],
            ]}
            onPick={() => setMode("local")}
          />
          <Option
            name="pd"
            checked={mode === "remote"}
            disabled={!providers.length}
            title="Provider model"
            lines={[
              mode === "remote" && p
                ? [`${titleOf(p)} · ${chosen}`, true]
                : [
                    !providers.length
                      ? "No shared provider: add one in Providers"
                      : remoteOn
                        ? "Pick one from Providers"
                        : "local servers only",
                  ],
            ]}
            onPick={() => setMode("remote")}
          />
          <Option
            name="pd"
            checked={mode === "assistant"}
            disabled={!assistant}
            title="Same as Assistant"
            lines={
              assistant
                ? [
                    [`${titleOf(assistant.p)} · ${assistant.model}`, true],
                    ["Follows the Assistant tab when it changes"],
                  ]
                : [[assistantWhy]]
            }
            onPick={() => setMode("assistant")}
          />
        </div>
        <ProviderModel
          id="pd"
          providers={providers}
          p={p}
          model={chosen}
          disabled={mode !== "remote"}
          setName={setName}
          setModel={setModel}
        />
        {goes?.kind === "remote" ? (
          <span className={WARN}>
            Sends the step&apos;s prompt and text to {titleOf(goes)}. Never images.
          </span>
        ) : (
          <span className="line">{goes ? `Sends ${sendsTo(goes)}.` : "Sends nothing out."}</span>
        )}
        {personal && (
          <p className="fine">
            Your Claude subscription is not offered here: it answers only for you.
          </p>
        )}
        {error && <ErrorNote>{error}</ErrorNote>}
        <Foot
          busy={busy}
          onCancel={() => {
            setMode(saved.mode);
            setName(saved.provider ?? providers[0]?.name ?? "");
            setModel(saved.model ?? "");
          }}
        />
      </form>
    </section>
  );
}

/** The Session names editor: Off · Local LLM · Provider model, then Save. */
function NamesPanel({
  ref,
  names,
  providers,
  onSaved,
}: {
  ref: React.Ref<HTMLElement>;
  names: Names;
  /** the ones a provider model may pick */
  providers: Provider[];
  onSaved: (n: Names) => void;
}) {
  const [mode, setMode] = useState(names.mode);
  const [name, setName] = useState(names.provider ?? providers[0]?.name ?? "");
  const [model, setModel] = useState(names.model ?? "");
  const p = providers.find((x) => x.name === name) ?? providers[0];
  const chosen = p?.models.includes(model) ? model : p?.default_model;
  const { error, busy, submit } = useSaving(
    () =>
      ok(
        api.PUT("/v1/studio/admin/session-names", {
          body:
            mode === "remote" && p
              ? { mode, provider: p.name, ...(chosen && { model: chosen }) }
              : { mode },
        }),
      ),
    onSaved,
  );
  const noRemote = !providers.length;
  return (
    <section ref={ref} id="session-names" aria-label="Session names" className="panel sec">
      <form onSubmit={submit} className="flex flex-col gap-3">
        <div className="pgh">
          <h2 className="flex items-center">
            Session names
            <Info>
              When a session nobody titled finishes its first run, this model writes a 2-5 word
              title from that run&apos;s prompt. A title you typed is never replaced. Off until you
              turn it on.
            </Info>
          </h2>
        </div>
        <span className="line">Names an untitled session from its first prompt.</span>
        <div role="radiogroup" aria-label="Session names" className="flex flex-col gap-2">
          <Option
            name="sn"
            checked={mode === "off"}
            title="Off"
            lines={[["Titles stay as they are"]]}
            onPick={() => setMode("off")}
          />
          <Option
            name="sn"
            checked={mode === "local"}
            disabled={!names.local_available}
            title="Local LLM"
            lines={
              names.local_available
                ? [[`${LOCAL} · uses the GPU queue · sends nothing out`]]
                : [
                    [
                      <>
                        {LOCAL} not downloaded · <Link href="/models">Models</Link>
                      </>,
                    ],
                    ["Uses the GPU queue · sends nothing out"],
                  ]
            }
            onPick={() => setMode("local")}
          />
          <Option
            name="sn"
            checked={mode === "remote"}
            disabled={noRemote}
            title="Provider model"
            lines={[
              noRemote
                ? [
                    names.remote_enabled
                      ? "No enabled provider: add one in Providers"
                      : "remote models are off on this deployment",
                  ]
                : [p && `${titleOf(p)} · ${chosen}`, true],
            ]}
            onPick={() => setMode("remote")}
          />
        </div>
        {mode === "remote" && p && (
          <>
            <ProviderModel
              id="sn"
              providers={providers}
              p={p}
              model={chosen}
              disabled={false}
              setName={setName}
              setModel={setModel}
            />
            {p.thinking_levels && (
              <span className="line">
                Set per model in <Link href={hubHref("providers")}>Providers</Link>.
              </span>
            )}
            <span className={p.kind === "remote" ? WARN : "line"}>
              Sends {sendsTo(p)}. Never images or references.
            </span>
            <span className="line">
              Users see &quot;named by {titleOf(p)}&quot; once
              {p.kind === "remote" ? ", and a line in the session menu" : ""}.
            </span>
          </>
        )}
        {error && <ErrorNote>{error}</ErrorNote>}
        <Foot
          busy={busy}
          onCancel={() => {
            setMode(names.mode);
            setName(names.provider ?? providers[0]?.name ?? "");
            setModel(names.model ?? "");
          }}
        />
      </form>
    </section>
  );
}
