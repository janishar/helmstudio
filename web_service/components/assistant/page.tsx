"use client";
// Hub › Assistant (plan 32, plan 33; docs/mock/Assistant.dc.html frames 19, 20 and 23): everything about the
// Assistant in one place, admins only: the on/off switch for everyone, Model (who answers, set up in one form),
// Tools and Skills (the plugins' on/off switches: their only home), Auto mode (the caps), Context (the chat window and
// the prompt cache), and their own Memory. Behind it are the admin routes as they are (providers, provider-keys, /admin/assistant, the plugins and their
// switches, Add as provider); keys are checked by KeyArea, never kept in React state.
import Link from "next/link";
import { useRouter } from "next/navigation";
import { useCallback, useEffect, useRef, useState } from "react";
import { Brain } from "lucide-react";
import { ErrorNote, Info } from "@/components/kit";
import { Head, Switch, hubHref } from "@/components/hub";
import { KeyArea } from "@/components/admin/providers";
import { MemoryList } from "@/components/assistant/assistant";
import { useAssistantApi, useUser } from "@/components/shell";
import { api, message, ok, type S } from "@/lib/api";
import { ASSISTANT_CHANGED } from "@/lib/assistant";
import { statusOf, unusable } from "@/lib/plugins";
import {
  MAX_MODELS,
  blankForm,
  formOf,
  pickable,
  presetForm,
  putOf,
  searchable,
  titleOf,
  type Preset,
  type ProviderForm,
} from "@/lib/providers";

type Settings = S<"AssistantSettings">;
type Put = S<"AssistantSettingsPut">;
type Limits = S<"AssistantLimits">;
type Provider = S<"Provider">;
type Plugin = S<"Plugin">;
type Cache = S<"AssistantCacheStats">;
type MemoryItem = S<"AssistantMemoryItem">;

const WINDOW = 60000; // a model's chat window until set (AssistantSettings.window_tokens default)
const BRIDGE = "claude-code-bridge";

/** The built-in plugins' lines, as short as the board's; anything else shows its own description. */
const SHORT: Record<string, string> = {
  "web-search": "Free, no key. It asks before each search.",
  "media-tools": "Crop, resize, trim, join, pull frames. Each edit is a new take.",
  "cinematic-filmmaker": "A short live-action film, script to MP4.",
  animator: "A cartoon film in one style, script to MP4.",
  [BRIDGE]: "Your own Claude plan, through Claude Code on this Mac",
};

/** Each card's letter tile (board frame 20). */
const TILE: Record<string, string> = {
  [BRIDGE]: "linear-gradient(155deg,#ffb98a,#c8553d)",
  deepseek: "linear-gradient(155deg,#7aa8ff,#2f4fd0)",
  anthropic: "linear-gradient(155deg,#e9c9a0,#8a6440)",
  openai: "linear-gradient(155deg,#9fe3c8,#1f8a66)",
  local: "linear-gradient(155deg,#c9ced9,#5b6478)",
};
const REMOTE_PRESETS = ["deepseek", "anthropic", "openai"];
const LOCAL_PRESETS = ["ollama", "lmstudio"];

const tileOf = (id: string) => TILE[id] ?? TILE.local;
const Tile = ({ id, title }: { id: string; title: string }) => (
  <i className="lg" style={{ background: tileOf(id) }}>
    {id === BRIDGE ? "C" : title[0]?.toUpperCase()}
  </i>
);

const changed = () => window.dispatchEvent(new Event(ASSISTANT_CHANGED)); // the drawer follows at once

/** "12 chats · 9 likes · 2 projects", or "empty". */
function countLine(items: MemoryItem[]) {
  const n = (k: MemoryItem["kind"]) => items.filter((x) => x.kind === k).length;
  return items.length
    ? `${n("summary")} chats · ${n("preference")} likes · ${n("project")} projects`
    : "empty";
}

/** A provider's model and its thinking, as Model providers set it: "deepseek-chat · thinking off". */
function modelLine(p: Provider, model: string) {
  const info = p.thinking_levels?.[model];
  if (info?.always) return `${model} · always thinks`;
  return `${model} · thinking ${p.thinking?.[model] ?? info?.default ?? "off"}`;
}

const sendsLine = (title: string, server: boolean) =>
  `Sends your messages, the screen summary and tool results to ${title}${server ? ", on this network" : ""}; never images.`;

// the caps (API 1.68: an older studio leaves the Auto ones out)
const AUTO: [keyof Limits, string][] = [
  ["auto_takes_per_hour", "Auto takes, an hour"],
  ["auto_gpu_minutes_per_hour", "Auto GPU minutes, an hour"],
];
const PER_USER: [keyof Limits, string][] = [
  ["model_calls_per_day", "Model calls a day"],
  ["tokens_per_day", "Tokens a day"],
  ["look_at_per_hour", "Look at, an hour"],
  ["messages_per_minute", "Messages a minute"],
];
const CAPS = [...AUTO, ...PER_USER];
const AUTO_CAPS: Partial<Record<keyof Limits, number>> = {
  auto_takes_per_hour: 8,
  auto_gpu_minutes_per_hour: 30,
};
const capOf = (l: Limits, k: keyof Limits) => l[k] ?? AUTO_CAPS[k];
const capsText = (l: Limits) =>
  Object.fromEntries(CAPS.map(([k]) => [k, String(capOf(l, k) ?? "")])) as Record<
    keyof Limits,
    string
  >;

/** Hub › Assistant (plan 33; frames 19, 20 and 23), admins only: the page. `setup`: open the setup form (the drawer's
 * Set up). */
export function AssistantScreen({ setup }: { setup: boolean }) {
  const me = useUser();
  if (!me) return <main id="main" className="apage flex-1" aria-busy />;
  return <AdminPage setup={setup} />;
}

function Row({
  title,
  sub,
  on,
  readOnly,
  onToggle,
  children,
}: {
  title: string;
  sub?: string;
  on: boolean;
  readOnly?: boolean;
  onToggle?: () => void;
  children?: React.ReactNode;
}) {
  return (
    <div className="trow">
      <div>
        <b>{title}</b>
        {sub && <small>{sub}</small>}
        {children}
      </div>
      <Switch on={on} label={`${title} on`} disabled={readOnly} onClick={onToggle} />
    </div>
  );
}

function MemorySection() {
  const [items, setItems] = useState<MemoryItem[] | null>(null);
  return (
    <section aria-label="Memory" className="panel sec">
      <div className="pgh">
        <h2>Memory</h2>
        {items && <span className="line">{countLine(items)}</span>}
      </div>
      <div className="ast inline">
        <MemoryList onItems={setItems} />
      </div>
      <p className="fine flex items-center gap-1">
        Only yours. Also in the drawer, under <Brain size={12} strokeWidth={2} aria-hidden />{" "}
        Memory.
      </p>
    </section>
  );
}

// ------------------------------------------------------------------ the admin's page (frames 19 and 20)

function AdminPage({ setup }: { setup: boolean }) {
  const a = useAssistantApi();
  const router = useRouter();
  const [settings, setSettings] = useState<Settings | null>(null);
  const [list, setList] = useState<S<"ProviderList"> | null>(null);
  const [plugins, setPlugins] = useState<Plugin[] | null>(null);
  const [presets, setPresets] = useState<Preset[]>([]);
  const [cache, setCache] = useState<Cache | null>(null);
  const [error, setError] = useState("");
  const [form, setForm] = useState(setup);
  const loadPlugins = useCallback(
    () =>
      ok(api.GET("/v1/studio/admin/plugins"))
        .then((l) => {
          setPlugins(l.plugins);
          return l.plugins;
        })
        .catch((e) => {
          setError(message(e));
          return null;
        }),
    [],
  );
  const load = useCallback(() => {
    Promise.all([
      ok(api.GET("/v1/studio/admin/assistant")),
      ok(api.GET("/v1/studio/admin/providers")),
    ])
      .then(([s, l]) => {
        setSettings(s);
        setList(l);
        setError("");
      })
      .catch((e) => setError(message(e)));
    // after the rest: the plugins list health-checks each plugin (up to 2 s each)
    void loadPlugins();
    ok(api.GET("/v1/studio/admin/provider-presets"))
      .then((l) => setPresets(l.presets))
      .catch(() => setPresets([]));
    ok(api.GET("/v1/studio/admin/assistant/cache"))
      .then(setCache)
      .catch(() => setCache(null));
  }, [loadPlugins]);
  useEffect(() => {
    load();
  }, [load]);
  // the drawer's Turn on: the switch follows
  useEffect(() => {
    const again = () =>
      void ok(api.GET("/v1/studio/admin/assistant"))
        .then(setSettings)
        .catch(() => undefined);
    window.addEventListener(ASSISTANT_CHANGED, again);
    return () => window.removeEventListener(ASSISTANT_CHANGED, again);
  }, []);
  // the drawer's Set up while this page is open: the form opens
  const [asked, setAsked] = useState(setup);
  if (asked !== setup) {
    setAsked(setup);
    if (setup) setForm(true);
  }

  if (!settings || !list)
    return (
      <main id="main" className="apage flex min-w-0 flex-1 flex-col gap-4 px-5 pt-4" aria-busy>
        {error && <ErrorNote onRetry={load}>{error}</ErrorNote>}
      </main>
    );
  const remoteOn = settings.remote_enabled;
  const providers = list.providers;
  // a plugin-made provider (Your Claude subscription) counts only while its plugin is on, as the server reads it
  const live = pickable(providers, remoteOn).find(
    (p) =>
      p.name === settings.provider &&
      (!p.plugin || !plugins || plugins.some((g) => g.name === p.plugin && g.enabled)),
  );
  const model = live && (settings.model ?? live.default_model);
  const setUp = !!live && live.models.includes(model!);
  const on = setUp && settings.enabled;

  /** PUT /admin/assistant with the saved settings and `over`. */
  async function put(over: Partial<Put>) {
    // web search only while remote is on: the server refuses (409) a saved one after REMOTE_MODELS went off
    const web = settings!.web_search && remoteOn;
    const s = await ok(
      api.PUT("/v1/studio/admin/assistant", {
        body: {
          enabled: settings!.enabled,
          provider: settings!.provider,
          model: settings!.model,
          web_search: web,
          search_provider: web ? settings!.search_provider : null,
          ...over,
        },
      }),
    );
    setSettings(s);
    changed();
    return s;
  }
  const tryPut = (over: Partial<Put>) =>
    put(over).then(
      () => setError(""),
      (e) => setError(message(e)),
    );
  async function togglePlugin(p: Plugin) {
    try {
      const x = await ok(
        api.PUT("/v1/studio/admin/plugins/{plugin}", {
          params: { path: { plugin: p.name } },
          body: { enabled: !p.enabled },
        }),
      );
      setPlugins((l) => l && l.map((y) => (y.name === x.name ? x : y)));
      setError("");
      changed();
    } catch (e) {
      setError(message(e));
    }
  }
  const tools = (plugins ?? []).filter((p) => p.kind === "assistant-tools" && !unusable(p));
  const skills = (plugins ?? []).filter((p) => p.kind === "skill" && !unusable(p));
  const open = form || !setUp;

  return (
    <main
      id="main"
      className="apage flex min-w-0 flex-[999_1_560px] flex-col gap-4 px-5 pt-4 pb-16"
    >
      <Head
        tab="assistant"
        chip={on ? "On" : setUp ? "Off" : "Not set up"}
        line={
          open
            ? "Pick who answers, then it is ready. You can change it later."
            : "Who answers, what it can use and what it remembers. Set here for everyone; Memory is yours."
        }
      >
        <Switch
          on={on}
          label="Assistant on"
          disabled={!setUp}
          onClick={() => void tryPut({ enabled: !on })}
        />
      </Head>
      {error && <ErrorNote onRetry={load}>{error}</ErrorNote>}
      {open ? (
        <div className="flex max-w-[860px] flex-col gap-4">
          <SetupForm
            settings={settings}
            providers={providers}
            presets={presets}
            plugins={plugins}
            reloadPlugins={loadPlugins}
            onCancel={setUp ? () => setForm(false) : undefined}
            onSaved={(s) => {
              setSettings(s);
              setForm(false);
              load();
              changed();
              router.replace("/hub");
              a?.setOpen(true); // done: the drawer opens
            }}
          />
          <Summary settings={settings} tools={tools} skills={skills} />
        </div>
      ) : (
        <div className="pgg">
          <div className="pgc">
            <section aria-label="Model" className="panel sec">
              <div className="pgh">
                <h2>Model</h2>
                <button type="button" className="chip bevel h-[30px]" onClick={() => setForm(true)}>
                  Change
                </button>
              </div>
              <div className="who">
                <Tile id={live!.preset ?? live!.plugin ?? live!.name} title={titleOf(live!)} />
                <div>
                  <b>{titleOf(live!)}</b>
                  <small>{modelLine(live!, model!)}</small>
                </div>
                <span className={`tag ${live!.kind === "remote" ? "tag-accent" : ""}`}>
                  {live!.kind === "remote" ? "remote" : "on this network"}
                </span>
              </div>
              <span className="line">
                Thinking is set per model in <Link href={hubHref("providers")}>Providers</Link>.
              </span>
              <span className={live!.kind === "remote" ? "warn" : "line"}>
                {sendsLine(titleOf(live!), live!.kind === "server")}
              </span>
            </section>
            <section aria-label="Tools" className="panel sec">
              <div className="pgh">
                <h2>Tools</h2>
                <Link href={hubHref("plugins")}>Plugins</Link>
              </div>
              {plugins && !tools.length && (
                <span className="line">No assistant tools in Plugins.</span>
              )}
              {tools.map((p) => (
                <Row
                  key={p.name}
                  title={p.title}
                  sub={SHORT[p.name] ?? p.description}
                  on={p.enabled}
                  onToggle={() => void togglePlugin(p)}
                >
                  {p.name === "web-search" && (
                    <SearchWith settings={settings} providers={providers} put={tryPut} />
                  )}
                </Row>
              ))}
              <p className="fine">Turned on and off only here.</p>
            </section>
            <section aria-label="Skills" className="panel sec">
              <div className="pgh">
                <h2>Skills</h2>
                <Link href={hubHref("plugins")}>Plugins</Link>
              </div>
              {plugins && !skills.length && <span className="line">No skills in Plugins.</span>}
              {skills.map((p) => (
                <Row
                  key={p.name}
                  title={p.title}
                  sub={SHORT[p.name] ?? p.when ?? p.description}
                  on={p.enabled}
                  onToggle={() => void togglePlugin(p)}
                />
              ))}
              <p className="fine">
                It reads a skill when an ask fits, or when you type @ and pick one.
              </p>
            </section>
          </div>
          <div className="pgc">
            <AutoSection key={`a${settings.updated_at}`} settings={settings} put={put} />
            <ContextSection
              key={`c${settings.updated_at}`}
              settings={settings}
              model={model!}
              cache={cache}
              providers={providers}
              put={put}
            />
            <MemorySection />
          </div>
        </div>
      )}
    </main>
  );
}

/** Web search's "Search with": the plugin's free search, or a search provider of Model providers (remote on). */
function SearchWith({
  settings,
  providers,
  put,
}: {
  settings: Settings;
  providers: Provider[];
  put: (over: Partial<Put>) => Promise<void>;
}) {
  const search = searchable(providers, settings.remote_enabled);
  if (!search.length) return null;
  return (
    <div className="fl mt-2">
      <label htmlFor="t-search">Search with</label>
      <select
        id="t-search"
        className="in"
        value={settings.web_search ? (settings.search_provider ?? "") : ""}
        onChange={(e) =>
          void put({ web_search: !!e.target.value, search_provider: e.target.value || null })
        }
      >
        <option value="">DuckDuckGo, then Bing, Google or Yahoo</option>
        {search.map((p) => (
          <option key={p.name} value={p.name}>
            {titleOf(p)} (key in Providers)
          </option>
        ))}
      </select>
    </div>
  );
}

/** Saves a section: the error under it, Cancel puts the saved values back. */
function useSave(put: (over: Partial<Put>) => Promise<unknown>) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const save = async (over: Partial<Put>) => {
    setBusy(true);
    setError("");
    try {
      await put(over);
    } catch (e) {
      setError(message(e));
    } finally {
      setBusy(false);
    }
  };
  return { busy, error, save };
}

function AutoSection({
  settings,
  put,
}: {
  settings: Settings;
  put: (over: Partial<Put>) => Promise<unknown>;
}) {
  const [caps, setCaps] = useState(() => capsText(settings.limits));
  const { busy, error, save } = useSave(put);
  const field = ([k, label]: [keyof Limits, string]) => (
    <div key={k} className="fl">
      <label htmlFor={`c-${k}`}>{label}</label>
      <input
        id={`c-${k}`}
        className="in"
        type="number"
        min={k.startsWith("auto_") ? 0 : 1}
        required
        value={caps[k]}
        onChange={(e) => setCaps({ ...caps, [k]: e.target.value })}
      />
    </div>
  );
  return (
    <form
      aria-label="Auto mode"
      className="panel sec"
      onSubmit={(e) => {
        e.preventDefault();
        void save({
          limits: Object.fromEntries(CAPS.map(([k]) => [k, Number(caps[k])])) as unknown as Limits,
        });
      }}
    >
      <div className="pgh">
        <h2>Auto mode</h2>
      </div>
      <div className="g4">{AUTO.map(field)}</div>
      <span className="sub">Caps per user</span>
      <div className="g4">{PER_USER.map(field)}</div>
      {error && <ErrorNote>{error}</ErrorNote>}
      <div className="sv">
        <p className="fine flex-auto">
          Auto still asks for deletes, overwrites and anything sent off the machine.
        </p>
        <button
          type="button"
          className="chip bevel h-[34px]"
          onClick={() => setCaps(capsText(settings.limits))}
        >
          Cancel
        </button>
        <button type="submit" className="key h-[34px] px-[18px] text-[13px]" disabled={busy}>
          Save
        </button>
      </div>
    </form>
  );
}

function ContextSection({
  settings,
  model,
  cache,
  providers,
  put,
}: {
  settings: Settings;
  model: string;
  cache: Cache | null;
  providers: Provider[];
  put: (over: Partial<Put>) => Promise<unknown>;
}) {
  const [win, setWin] = useState(String(settings.window_tokens ?? WINDOW));
  const { busy, error, save } = useSave(put);
  return (
    <form
      aria-label="Context"
      className="panel sec"
      onSubmit={(e) => {
        e.preventDefault();
        void save({ window_tokens: Number(win) });
      }}
    >
      <div className="pgh">
        <h2>Context</h2>
      </div>
      <div className="g2">
        <div className="fl">
          <label htmlFor="c-win" className="flex items-center">
            Chat window, tokens
            <Info>
              The model&apos;s real limit. Past 70% the oldest half of a chat folds once into a
              short summary, so plans and decisions stay. A smaller window costs less per answer.
            </Info>
          </label>
          <input
            id="c-win"
            className="in"
            type="number"
            min={8000}
            max={1000000}
            required
            value={win}
            onChange={(e) => setWin(e.target.value)}
          />
        </div>
        <span className="line pb-[9px]">
          for {model} · {WINDOW} until set
        </span>
      </div>
      <span className="line">
        Each model keeps its own. Past 70% the oldest half folds into a summary.
      </span>
      {cache && <CacheTable cache={cache} providers={providers} />}
      {error && <ErrorNote>{error}</ErrorNote>}
      <div className="sv">
        <span className="flex-auto" />
        <button
          type="button"
          className="chip bevel h-[34px]"
          onClick={() => setWin(String(settings.window_tokens ?? WINDOW))}
        >
          Cancel
        </button>
        <button type="submit" className="key h-[34px] px-[18px] text-[13px]" disabled={busy}>
          Save
        </button>
      </div>
    </form>
  );
}

/** The prompt cache hits per model over the last 30 days (a model with no prompt tokens is not listed). */
function CacheTable({ cache, providers }: { cache: Cache; providers: Provider[] }) {
  const rows = cache.models.filter((m) => m.prompt_tokens > 0);
  const cell = "border-b border-s3 px-2.5 py-2 align-middle";
  return (
    <>
      <span className="sub">Prompt cache, last {cache.days} days</span>
      {rows.length ? (
        <table className="w-full border-collapse text-[13px]">
          <thead>
            <tr className="text-left text-[11px] tracking-[0.04em] text-t2">
              <th scope="col" className={`${cell} font-semibold`}>
                Model
              </th>
              <th scope="col" className={`${cell} font-semibold`}>
                Calls
              </th>
              <th scope="col" className={`${cell} font-semibold`}>
                <span className="flex items-center">
                  Cache hits
                  <Info>
                    The share of prompt tokens the provider read from its cache, as each reply
                    reported. Hits cost less. A model whose replies report none is not listed.
                  </Info>
                </span>
              </th>
            </tr>
          </thead>
          <tbody>
            {rows.map((m) => {
              const pr = providers.find((x) => x.name === m.provider);
              const pct = Math.round((m.hit_rate ?? m.hit_tokens / m.prompt_tokens) * 100);
              return (
                <tr key={`${m.provider}/${m.model}`} className="[&:last-child>td]:border-b-0">
                  <td className={`${cell} mono text-[12px] text-t2`}>
                    {pr ? titleOf(pr) : m.provider} · {m.model}
                  </td>
                  <td className={`${cell} mono text-[12px] text-t2`}>
                    {m.calls.toLocaleString("en-US")}
                  </td>
                  <td className={cell}>
                    <span className="flex items-center gap-2">
                      <span
                        className="h-1.5 flex-[0_0_96px] overflow-hidden rounded-full bg-s4"
                        role="meter"
                        aria-label={`Cache hits, ${m.model}`}
                        aria-valuemin={0}
                        aria-valuemax={100}
                        aria-valuenow={pct}
                      >
                        <span className="block h-full bg-a1" style={{ width: `${pct}%` }} />
                      </span>
                      <b className="font-semibold">{pct}%</b>
                    </span>
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      ) : (
        <p className="m-0 text-[13px] text-t2">
          No cache hits reported yet. They show here once the assistant&apos;s replies report them.
        </p>
      )}
    </>
  );
}

/** Frame 20's faded lines under the form: the rest of the page, in one line each. */
function Summary({
  settings,
  tools,
  skills,
}: {
  settings: Settings;
  tools: Plugin[];
  skills: Plugin[];
}) {
  const [mem, setMem] = useState<MemoryItem[] | null>(null);
  useEffect(() => {
    ok(api.GET("/v1/studio/assistant/memory"))
      .then((l) => setMem(l.items))
      .catch(() => setMem(null));
  }, []);
  const names = (l: Plugin[]) =>
    l
      .filter((p) => p.enabled)
      .map((p) => p.title)
      .join(" · ") || "none on";
  const l = settings.limits;
  return (
    <section
      aria-label="Tools, Skills, Auto mode, Context, Memory"
      className="flex flex-col gap-2 opacity-55"
    >
      <div className="sum2">
        Tools<span>{names(tools)}</span>
      </div>
      <div className="sum2">
        Skills<span>{names(skills)}</span>
      </div>
      <div className="sum2">
        Auto mode
        <span>
          {capOf(l, "auto_takes_per_hour")} takes · {capOf(l, "auto_gpu_minutes_per_hour")} GPU
          minutes an hour
        </span>
      </div>
      <div className="sum2">
        Context<span>{settings.window_tokens ?? WINDOW} tokens</span>
      </div>
      <div className="sum2">
        Memory<span>{mem ? countLine(mem) : ""}</span>
      </div>
    </section>
  );
}

// ------------------------------------------------------------------ the setup form (frame 20)

/** One card of "Who answers". */
type Card =
  | { id: string; kind: "plugin"; title: string; plugin: Plugin }
  | { id: string; kind: "preset"; title: string; preset: Preset; saved?: Provider }
  | { id: "local"; kind: "local"; title: string }
  | { id: string; kind: "saved"; title: string; saved: Provider };

const isRemote = (c: Card) =>
  c.kind === "preset" || (c.kind === "saved" && c.saved.kind === "remote");

function cardsOf(providers: Provider[], presets: Preset[], plugins: Plugin[] | null): Card[] {
  const out: Card[] = [];
  for (const p of plugins ?? [])
    if (p.kind === "model-provider" && !unusable(p))
      out.push({ id: p.name, kind: "plugin", title: p.title, plugin: p });
  for (const id of REMOTE_PRESETS) {
    const preset = presets.find((x) => x.id === id);
    if (preset)
      out.push({
        id,
        kind: "preset",
        title: preset.title,
        preset,
        saved: providers.find((x) => x.name === id),
      });
  }
  out.push({ id: "local", kind: "local", title: "Local server" });
  // an existing remote provider is a pick too (plan 32 36.2)
  for (const p of providers)
    if (p.kind === "remote" && !p.plugin && !REMOTE_PRESETS.includes(p.name))
      out.push({ id: p.name, kind: "saved", title: titleOf(p), saved: p });
  return out;
}

/** The card the saved settings point at. */
function cardOfProvider(cards: Card[], p: Provider | undefined) {
  if (!p) return undefined;
  if (p.plugin) return cards.find((c) => c.id === p.plugin);
  if (p.kind === "server") return cards.find((c) => c.id === "local");
  return cards.find((c) => c.id === p.name);
}

function SetupForm({
  settings,
  providers,
  presets,
  plugins,
  reloadPlugins,
  onCancel,
  onSaved,
}: {
  settings: Settings;
  providers: Provider[];
  presets: Preset[];
  plugins: Plugin[] | null;
  reloadPlugins: () => Promise<Plugin[] | null>;
  onCancel?: () => void;
  onSaved: (s: Settings) => void;
}) {
  const remoteOn = settings.remote_enabled;
  const cards = cardsOf(providers, presets, plugins);
  const usable = (c: Card) => !isRemote(c) || remoteOn;
  const [pick, setPick] = useState<string | null>(null);
  const saved = providers.find((p) => p.name === settings.provider);
  const card =
    cards.find((c) => c.id === pick) ??
    [cardOfProvider(cards, saved)].find((c) => c && usable(c)) ??
    cards.find((c) => c.kind === "preset" && usable(c)) ??
    cards.find((c) => c.kind === "local")!;
  const radios = useRef<HTMLDivElement>(null);

  /** Save's last step: the Assistant on with this provider and model (web search kept while the switch is on). */
  async function saveWith(provider: string, model: string) {
    const web = settings.web_search && remoteOn;
    onSaved(
      await ok(
        api.PUT("/v1/studio/admin/assistant", {
          body: {
            enabled: true,
            provider,
            model,
            web_search: web,
            search_provider: web ? settings.search_provider : null,
          },
        }),
      ),
    );
  }
  const step = { onCancel, saveWith, providers, settings };
  return (
    <section aria-label="Set up the Model" className="panel sec">
      <div className="pgh">
        <h2>Model</h2>
      </div>
      <span className="stp2">
        <b>1</b>Who answers
      </span>
      <div
        ref={radios}
        role="radiogroup"
        aria-label="Who answers"
        className="pks"
        onKeyDown={(e) => {
          // arrow keys move the pick among the usable cards
          const open = cards.filter(usable);
          const i = open.findIndex((c) => c.id === card.id);
          const d =
            e.key === "ArrowRight" || e.key === "ArrowDown"
              ? 1
              : e.key === "ArrowLeft" || e.key === "ArrowUp"
                ? -1
                : 0;
          if (!d || i < 0) return;
          e.preventDefault();
          const next = open[(i + d + open.length) % open.length];
          setPick(next.id);
          radios.current?.querySelector<HTMLElement>(`[data-card="${next.id}"]`)?.focus();
        }}
      >
        {cards.map((c) => {
          const off = !usable(c);
          const st = c.kind === "plugin" ? statusOf(c.plugin) : null;
          return (
            <button
              key={c.id}
              type="button"
              role="radio"
              data-card={c.id}
              aria-checked={c.id === card.id}
              aria-disabled={off || undefined}
              tabIndex={c.id === card.id ? 0 : -1}
              title={off ? "remote models are off on this deployment" : undefined}
              className="pk"
              onClick={() => !off && setPick(c.id)}
            >
              <Tile
                id={c.kind === "local" ? "local" : c.id}
                title={c.kind === "local" ? "L" : c.title}
              />
              <span>
                <b>{c.title}</b>
                <small>
                  {c.kind === "plugin"
                    ? (SHORT[c.id] ?? c.plugin.description)
                    : c.kind === "local"
                      ? "Ollama or LM Studio · sends nothing out"
                      : c.kind === "saved"
                        ? "Remote · saved in Providers"
                        : "Remote · needs a key"}
                </small>
                {st && (
                  <span
                    className="pst"
                    style={{ color: st.ok ? "#7fd88f" : st.tone === "red" ? "#ff8a8a" : "#f2c14e" }}
                  >
                    <span
                      className="dt"
                      style={{
                        background: st.ok ? "#7fd88f" : st.tone === "red" ? "#ff8a8a" : "#f2c14e",
                      }}
                    />
                    {st.text}
                  </span>
                )}
              </span>
            </button>
          );
        })}
      </div>
      {!remoteOn && (
        <span className="line">Remote ones: remote models are off on this deployment.</span>
      )}
      {card.kind === "plugin" ? (
        <PluginStep key={card.id} plugin={card.plugin} reload={reloadPlugins} {...step} />
      ) : card.kind === "preset" ? (
        <RemoteStep key={card.id} preset={card.preset} saved={card.saved} {...step} />
      ) : card.kind === "saved" ? (
        <SavedStep key={card.id} saved={card.saved} {...step} />
      ) : (
        <LocalStep key="local" presets={presets} {...step} />
      )}
    </section>
  );
}

type Step = {
  onCancel?: () => void;
  saveWith: (provider: string, model: string) => Promise<void>;
  providers: Provider[];
  settings: Settings;
};

/** The model select: the saved model when it is this provider's, else the default (the preset's first). */
function ModelPick({
  id,
  n,
  models,
  def,
  value,
  onChange,
  disabled,
}: {
  id: string;
  n: number;
  models: string[];
  def: string | undefined;
  value: string;
  onChange: (m: string) => void;
  disabled?: boolean;
}) {
  return (
    <>
      <span className="stp2">
        <b>{n}</b>Model
      </span>
      <div className="g2">
        <div className="fl">
          <label htmlFor={id}>Model</label>
          <select
            id={id}
            className="in"
            value={value}
            disabled={disabled || !models.length}
            onChange={(e) => onChange(e.target.value)}
          >
            {models.map((m) => (
              <option key={m} value={m}>
                {m}
                {m === def ? " (default)" : ""}
              </option>
            ))}
          </select>
        </div>
        <span />
      </div>
    </>
  );
}

/** The model chosen: `model` when listed, else the default when listed, else the first. */
const chosenOf = (models: string[], model: string, def?: string) =>
  models.includes(model) ? model : def && models.includes(def) ? def : (models[0] ?? "");

/** Save, Cancel and the error, at the form's foot. */
function Foot({
  fine,
  error,
  busy,
  disabled,
  onCancel,
  onSave,
}: {
  fine?: string;
  error: string;
  busy: boolean;
  disabled: boolean;
  onCancel?: () => void;
  onSave: () => void;
}) {
  return (
    <>
      {error && <ErrorNote>{error}</ErrorNote>}
      <div className="sv">
        {fine ? <p className="fine flex-auto">{fine}</p> : <span className="flex-auto" />}
        {onCancel && (
          <button type="button" className="chip bevel h-[38px]" onClick={onCancel}>
            Cancel
          </button>
        )}
        <button
          type="button"
          className="key h-[38px] px-[18px] text-[13px]"
          disabled={busy || disabled}
          onClick={onSave}
        >
          {busy ? "Saving…" : "Save"}
        </button>
      </div>
    </>
  );
}

function useBusy() {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const run = async (f: () => Promise<unknown>) => {
    setBusy(true);
    setError("");
    try {
      await f();
    } catch (e) {
      setError(message(e));
    } finally {
      setBusy(false);
    }
  };
  return { busy, error, setError, run };
}

/** Creates or replaces a provider from the form (enabled), unless it is saved, on and has the model already. */
async function ensureProvider(f: ProviderForm, saved: Provider | undefined, model: string) {
  if (saved?.enabled && saved.models.includes(model)) return;
  const models = f.models.includes(model) ? f.models : [model, ...f.models].slice(0, MAX_MODELS);
  const r = putOf({ ...f, models, enabled: true });
  if ("error" in r) throw new Error(r.error);
  await ok(
    api.PUT("/v1/studio/admin/providers/{name}", {
      params: { path: { name: f.name } },
      body: r.body,
    }),
  );
}

/** A remote preset (DeepSeek, Anthropic, OpenAI): the key, checked with the provider before it is saved (KeyArea), the
 * models fetched by themselves once it is found, then the model. */
function RemoteStep({
  preset,
  saved,
  settings,
  onCancel,
  saveWith,
}: Step & { preset: Preset; saved?: Provider }) {
  const keyName = saved?.key_secret ?? preset.key_secret ?? preset.id;
  const [key, setKey] = useState<S<"ProviderKey"> | null>(null);
  const [recheck, setRecheck] = useState(0);
  const [fetched, setFetched] = useState<{ models?: string[]; error?: string } | null>(null);
  const [again, setAgain] = useState(0);
  const [model, setModel] = useState(settings.provider === preset.id ? (settings.model ?? "") : "");
  const { busy, error, run } = useBusy();
  useEffect(() => {
    let live = true;
    ok(
      api.GET("/v1/studio/admin/provider-keys/{key_secret}", {
        params: { path: { key_secret: keyName } },
      }),
    )
      .then((k) => live && setKey(k))
      .catch(() => live && setKey(null));
    return () => {
      live = false;
    };
  }, [keyName, recheck]);
  const found = !!key?.found;
  useEffect(() => {
    if (!found) return;
    let live = true;
    ok(
      api.POST("/v1/studio/admin/provider-presets/{id}/models", {
        params: { path: { id: preset.id } },
        body: { key_secret: keyName },
      }),
    )
      .then(
        (r) =>
          live &&
          setFetched(
            r.ok ? { models: r.models } : { error: r.error ?? r.failure ?? "Fetch failed" },
          ),
      )
      .catch((e) => live && setFetched({ error: message(e) }));
    return () => {
      live = false;
    };
  }, [found, preset.id, keyName, again]);
  const models = fetched?.models ?? saved?.models ?? preset.models;
  const def = preset.models[0];
  const chosen = chosenOf(models, model, saved?.default_model ?? def);
  return (
    <>
      <span className="stp2">
        <b>2</b>Key
      </span>
      {key && (
        <KeyArea
          status={key}
          source={saved ? { saved } : { preset }}
          title={preset.title}
          label={`${preset.title} API key`}
          onStatus={setKey}
          onRecheck={() => setRecheck((n) => n + 1)}
        />
      )}
      {fetched?.models && (
        <span className="text-[12px] text-a1">
          ✓ Checked with {preset.title} · {fetched.models.length} models
        </span>
      )}
      {fetched?.error && (
        <div role="alert" className="flex items-center gap-2 text-[12px] text-[#ff8a8a]">
          <span className="flex-auto">{fetched.error}</span>
          <button
            type="button"
            className="chip h-[26px] text-[12px] text-t1"
            onClick={() => setAgain((n) => n + 1)}
          >
            Retry
          </button>
        </div>
      )}
      <ModelPick id="f-model" n={3} models={models} def={def} value={chosen} onChange={setModel} />
      <span className="warn">{sendsLine(preset.title, false)}</span>
      <Foot
        fine="The key is saved on the server and never shown again."
        error={error}
        busy={busy}
        disabled={!found || !chosen}
        onCancel={onCancel}
        onSave={() =>
          void run(async () => {
            const base = saved
              ? formOf(saved)
              : { ...presetForm(preset, blankForm("remote")), default_model: chosen };
            await ensureProvider({ ...base, key_secret: keyName }, saved, chosen);
            await saveWith(preset.id, chosen);
          })
        }
      />
    </>
  );
}

/** An existing remote provider of Model providers: only its model. */
function SavedStep({ saved, settings, onCancel, saveWith }: Step & { saved: Provider }) {
  const [model, setModel] = useState(
    settings.provider === saved.name ? (settings.model ?? "") : "",
  );
  const { busy, error, run } = useBusy();
  const chosen = chosenOf(saved.models, model, saved.default_model);
  return (
    <>
      <span className="line">
        Its key is set in <Link href={hubHref("providers")}>Providers</Link>.
      </span>
      <ModelPick
        id="f-saved"
        n={2}
        models={saved.models}
        def={saved.default_model}
        value={chosen}
        onChange={setModel}
      />
      <span className="warn">{sendsLine(titleOf(saved), false)}</span>
      <Foot
        error={error}
        busy={busy}
        disabled={!chosen}
        onCancel={onCancel}
        onSave={() =>
          void run(async () => {
            await ensureProvider(formOf(saved), saved, chosen);
            await saveWith(saved.name, chosen);
          })
        }
      />
    </>
  );
}

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

/** A model-provider plugin (Your Claude subscription): no API key, its state and disclaimer; Save turns the plugin on,
 * waits for it to run, adds it as the caller's own provider once (Add as provider; its token from its host, API 1.73) and
 * saves the Assistant with it. */
function PluginStep({
  plugin: p,
  reload,
  settings,
  providers,
  onCancel,
  saveWith,
}: Step & { plugin: Plugin; reload: () => Promise<Plugin[] | null> }) {
  const st = statusOf(p);
  const running = p.status === "running";
  const mine = providers.find((x) => x.name === p.provider);
  const models = p.models ?? [];
  const [model, setModel] = useState(
    mine && settings.provider === mine.name ? (settings.model ?? "") : "",
  );
  const chosen = chosenOf(models, model, mine?.default_model);
  // the plugin's token (its key_hint), only in the field itself
  const token = useRef<HTMLInputElement>(null);
  const [typed, setTyped] = useState(false);
  const needsKey = !p.provider && !p.key_from_host; // API 1.73: studio gets its key from its host
  const { busy, error, setError, run } = useBusy();
  const tone = st.tone === "red" ? "#ff8a8a" : "#f2c14e";
  const [rest, link] = [p.disclaimer?.text ?? "", p.disclaimer?.link];
  return (
    <>
      {!running && p.enabled && (
        <div className="bar2">
          <span className="dt mt-[5px]" style={{ background: tone }} />
          <span className="flex flex-auto flex-col">
            <b className="text-[13px]">{st.text}</b>
            <span className="text-t1b">
              {p.status === "starting"
                ? "Starting on its own. Wait a moment, then Try again."
                : "Install Claude Code and sign in, then Try again."}
            </span>
          </span>
          <button
            type="button"
            className="chip h-[30px] flex-none text-[12px]"
            onClick={() =>
              void (p.status === "failed"
                ? // on while on: its runner starts it again at once (API 1.67 retry)
                  api
                    .PUT("/v1/studio/admin/plugins/{plugin}", {
                      params: { path: { plugin: p.name } },
                      body: { enabled: true },
                    })
                    .finally(reload)
                : reload())
            }
          >
            Try again
          </button>
        </div>
      )}
      <span className="line">No key: it uses your own Claude login. Save turns the plugin on.</span>
      {needsKey && (
        <div className="fl">
          <label htmlFor="f-token">Bridge token</label>
          <input
            id="f-token"
            ref={token}
            className="in"
            type="password"
            autoComplete="off"
            spellCheck={false}
            placeholder="Paste the token"
            onChange={(e) => setTyped(e.target.value.trim() !== "")}
          />
          {p.key_hint && <span className="line">{p.key_hint}. Never your Claude login.</span>}
        </div>
      )}
      {p.disclaimer && (
        <span className="warn">
          {rest}{" "}
          {link && (
            <a href={link} target="_blank" rel="noreferrer" className="text-inherit underline">
              Terms
            </a>
          )}
        </span>
      )}
      <ModelPick
        id="f-plugin"
        n={2}
        models={models}
        def={models[0]}
        value={chosen}
        onChange={setModel}
        disabled={!running}
      />
      <Foot
        error={error}
        busy={busy}
        disabled={(p.enabled && !running) || !chosen || (needsKey && !typed)}
        onCancel={onCancel}
        onSave={() =>
          void run(async () => {
            let cur = p;
            if (!cur.enabled)
              cur = await ok(
                api.PUT("/v1/studio/admin/plugins/{plugin}", {
                  params: { path: { plugin: p.name } },
                  body: { enabled: true },
                }),
              );
            // it starts itself: wait up to about 20 s for it to answer
            for (let i = 0; cur.status !== "running" && i < 10; i++) {
              await sleep(2000);
              cur = (await reload())?.find((x) => x.name === p.name) ?? cur;
            }
            if (cur.status !== "running") {
              await reload();
              return setError(
                `${p.title} is not running yet. Install Claude Code and sign in, then Try again.`,
              );
            }
            const provider =
              cur.provider ??
              (
                await ok(
                  api.POST("/v1/studio/admin/plugins/{plugin}/provider", {
                    params: { path: { plugin: p.name } },
                    body: {
                      key: needsKey ? token.current?.value : undefined,
                      default_model: chosen,
                    },
                  }),
                )
              ).name;
            if (token.current) token.current.value = "";
            await saveWith(provider, chosen);
          })
        }
      />
    </>
  );
}

/** A local server: a saved one (Model providers), else the Ollama or LM Studio preset, its models fetched by
 * themselves; a new one shares the first GPU host's memory. */
function LocalStep({
  presets,
  providers,
  settings,
  onCancel,
  saveWith,
}: Step & { presets: Preset[] }) {
  type Choice = { saved: Provider } | { preset: Preset };
  const choices: Choice[] = [
    ...providers.filter((p) => p.kind === "server" && !p.plugin).map((saved) => ({ saved })),
    ...LOCAL_PRESETS.flatMap((id) => {
      const preset = presets.find((x) => x.id === id);
      return preset && !providers.some((p) => p.name === id) ? [{ preset }] : [];
    }),
  ];
  const nameOf = (c: Choice) => ("saved" in c ? c.saved.name : c.preset.id);
  const [which, setWhich] = useState(
    choices.some((c) => nameOf(c) === settings.provider) ? settings.provider! : "",
  );
  const c = choices.find((x) => nameOf(x) === which) ?? choices[0];
  const [fetched, setFetched] = useState<{ id: string; models?: string[]; error?: string } | null>(
    null,
  );
  const [again, setAgain] = useState(0);
  const [host, setHost] = useState("");
  const [model, setModel] = useState(settings.model ?? "");
  const { busy, error, run } = useBusy();
  const presetId = c && "preset" in c ? c.preset.id : null;
  useEffect(() => {
    if (!presetId) return;
    let live = true;
    ok(
      api.POST("/v1/studio/admin/provider-presets/{id}/models", {
        params: { path: { id: presetId } },
        body: {},
      }),
    )
      .then(
        (r) =>
          live &&
          setFetched(
            r.ok
              ? { id: presetId, models: r.models }
              : { id: presetId, error: r.error ?? r.failure ?? "Fetch failed" },
          ),
      )
      .catch((e) => live && setFetched({ id: presetId, error: message(e) }));
    ok(api.GET("/v1/studio/hosts"))
      .then((h) => live && setHost(h.hosts[0]?.host ?? ""))
      .catch(() => undefined);
    return () => {
      live = false;
    };
  }, [presetId, again]);
  if (!c)
    return (
      <span className="line">
        No local server yet: add one in <Link href={hubHref("providers")}>Providers</Link>.
      </span>
    );
  const title = "saved" in c ? titleOf(c.saved) : c.preset.title;
  const url = "saved" in c ? c.saved.base_url : c.preset.base_url;
  const got = fetched?.id === presetId ? fetched : null;
  const models = ("saved" in c ? c.saved.models : got?.models) ?? [];
  const def = "saved" in c ? c.saved.default_model : models[0];
  const chosen = chosenOf(models, model, def);
  return (
    <>
      {choices.length > 1 ? (
        <div className="fl max-w-[420px]">
          <label htmlFor="f-server">Server</label>
          <select
            id="f-server"
            className="in"
            value={nameOf(c)}
            onChange={(e) => setWhich(e.target.value)}
          >
            {choices.map((x) => (
              <option key={nameOf(x)} value={nameOf(x)}>
                {"saved" in x ? titleOf(x.saved) : x.preset.title} ·{" "}
                {"saved" in x ? x.saved.base_url : x.preset.base_url}
              </option>
            ))}
          </select>
        </div>
      ) : (
        <span className="line">
          Server: {title} · {url}
        </span>
      )}
      {presetId && !got && <span className="line">Loading models…</span>}
      {got?.error && (
        <div role="alert" className="flex items-center gap-2 text-[12px] text-[#ff8a8a]">
          <span className="flex-auto">{got.error}</span>
          <button
            type="button"
            className="chip h-[26px] text-[12px] text-t1"
            onClick={() => setAgain((n) => n + 1)}
          >
            Retry
          </button>
        </div>
      )}
      <ModelPick id="f-local" n={2} models={models} def={def} value={chosen} onChange={setModel} />
      <span className="line">Runs on your GPU hosts. Sends nothing out.</span>
      <Foot
        error={error}
        busy={busy}
        disabled={!chosen || (!!presetId && !host)}
        onCancel={onCancel}
        onSave={() =>
          void run(async () => {
            const f =
              "saved" in c
                ? formOf(c.saved)
                : {
                    // ponytail: a new local server reserves 8 GB on the first host; Model providers edits it
                    ...presetForm(c.preset, { ...blankForm("server"), host, memory_gb: "8" }),
                    models: [chosen, ...models.filter((m) => m !== chosen)].slice(0, MAX_MODELS),
                    default_model: chosen,
                  };
            await ensureProvider(f, "saved" in c ? c.saved : undefined, chosen);
            await saveWith(nameOf(c), chosen);
          })
        }
      />
    </>
  );
}
