"use client";
// Admin › Hosts (docs/mock/Hosts.dc.html): each GPU host with its envs, the shared memory budget and what fills it,
// the current run, its resident components with Pin and Unload, Unload all; the selected host's settings (what stays
// loaded after a run, the idle timeout, the memory reserve), applied by its runners live, and Evict all. Refreshed
// every 3 s while the tab is visible.
import { useCallback, useEffect, useState } from "react";
import { Monitor, Pin, PinOff, Server, X } from "lucide-react";
import { openMenu } from "@/components/context-menu";
import { ErrorNote, Info, Modal } from "@/components/kit";
import { SystemTabs } from "@/components/admin/users";
import { api, message, ok, type S } from "@/lib/api";
import { MAC_RUNTIMES } from "@/lib/models";
import {
  AFTER_RUN,
  afterRunTag,
  ago,
  autoBudget,
  componentText,
  deviceLine,
  duration,
  hostCards,
  isPinned,
  machineText,
  pinRef,
  queuedCount,
  settingsOf,
  togglePin,
  type HostCard,
  type HostEntry,
} from "@/lib/hosts";
import { Range } from "@/components/range";

type Confirm = { host: string; title: string; text: string; body: S<"HostUnload"> };
const ALL =
  "Unloads every component on this host, pinned ones included, and its cached handles. A running run finishes first. Pins stay set.";
const unloadAll = (host: string): Confirm => ({
  host,
  title: `Unload all on ${host}?`,
  text: ALL,
  body: { all: true },
});
const evictAll = (host: string): Confirm => ({
  host,
  title: `Evict all on ${host}?`,
  text: ALL,
  body: { all: true },
});

/** The hosts, polled every 3 s while visible; `reload` reads them at once (after a change). */
function useAdminHosts() {
  const [hosts, setHosts] = useState<S<"Host">[] | null>(null);
  const [queued, setQueued] = useState<number | null>(null);
  const [now, setNow] = useState(0);
  const [error, setError] = useState("");
  const reload = useCallback(() => {
    // the summary's "· N queued"; not needed to work
    ok(api.GET("/v1/studio/queue"))
      .then((q) => setQueued(queuedCount(q)))
      .catch(() => setQueued(null));
    ok(api.GET("/v1/studio/hosts"))
      .then((h) => {
        setHosts(h.hosts);
        setNow(Date.now());
        setError("");
      })
      .catch((e) => setError(message(e)));
  }, []);
  useEffect(() => {
    const tick = () => !document.hidden && reload();
    tick();
    const t = setInterval(tick, 3_000);
    document.addEventListener("visibilitychange", tick);
    return () => {
      clearInterval(t);
      document.removeEventListener("visibilitychange", tick);
    };
  }, [reload]);
  return { hosts, queued, now, error, reload };
}

export function HostsAdmin() {
  const { hosts, queued, now, error, reload } = useAdminHosts();
  const [titles, setTitles] = useState<Record<string, string>>({});
  const [names, setNames] = useState<Record<string, string>>({});
  const [picked, setPicked] = useState("");
  const [confirm, setConfirm] = useState<Confirm | null>(null);
  const [notice, setNotice] = useState("");
  const [actionError, setActionError] = useState("");
  useEffect(() => {
    // model titles for the components, the users' names for the current run; neither is needed to work
    ok(api.GET("/v1/studio/models"))
      .then((l) => setTitles(Object.fromEntries(l.models.map((m) => [m.id, m.title]))))
      .catch(() => undefined);
    ok(api.GET("/auth/admin/users"))
      .then((l) => setNames(Object.fromEntries(l.users.map((u) => [u.id, u.name]))))
      .catch(() => undefined);
  }, []);
  const cards = hostCards(hosts ?? []);
  const card = cards.find((c) => c.host === picked) ?? cards[0];

  async function unload(c: Confirm) {
    setConfirm(null);
    try {
      const r = await ok(
        api.POST("/v1/studio/admin/hosts/{host}/unload", {
          params: { path: { host: c.host } },
          body: c.body,
        }),
      );
      setNotice(
        `Unload on ${r.host}: ` +
          r.requests
            .map((q) => `env ${q.env} ${q.state === "asked" ? "asked now" : "after its run"}`)
            .join(", ") +
          ".",
      );
      setActionError("");
      reload();
    } catch (e) {
      setActionError(message(e));
    }
  }
  async function pin(c: HostCard, e: HostEntry) {
    try {
      await ok(
        api.PUT("/v1/studio/admin/hosts/{host}/pins", {
          params: { path: { host: c.host } },
          body: { pins: togglePin(c, e) },
        }),
      );
      setActionError("");
      reload();
    } catch (err) {
      setActionError(message(err));
    }
  }
  const entryName = (e: HostEntry) =>
    `${titles[e.r.model] ?? e.r.model} · ${componentText(e.r.component)}`;

  return (
    <>
      <main id="main" className="flex min-w-0 flex-[999_1_560px] flex-col gap-4 px-5 pt-4 pb-6">
        <div className="flex flex-wrap items-center gap-3">
          <h1 className="font-display text-[22px] font-bold tracking-[0.01em]">System</h1>
          <div className="grow" />
          <span className="tag tag-accent">Signed in as root admin</span>
        </div>
        <SystemTabs current="hosts" />
        <div className="flex items-center gap-2 text-[13px] text-t2">
          <span className="mono text-[12px]">
            {cards.length} host{cards.length === 1 ? "" : "s"} ·{" "}
            {cards.filter((c) => c.state === "running").length} running
            {queued !== null && ` · ${queued} queued`}
          </span>
          <Info>
            One run at a time per host, across its envs, because they share its memory. The
            dispatcher prefers a host that already has the run&apos;s components loaded, then the
            one idle longest. Only admins can pin or unload.
          </Info>
          {notice && (
            <span role="status" className="mono text-[12px]">
              {notice}
            </span>
          )}
        </div>
        {error && <ErrorNote onRetry={reload}>{error}</ErrorNote>}
        {actionError && <ErrorNote>{actionError}</ErrorNote>}
        {hosts && !cards.length && <p className="text-t2">No runner has reported a host yet.</p>}
        <div className="grid grid-cols-[repeat(auto-fit,minmax(min(380px,100%),1fr))] items-start gap-[18px]">
          {cards.map((c) => (
            <HostSection
              key={c.host}
              c={c}
              selected={c.host === card?.host}
              onSelect={() => setPicked(c.host)}
              now={now}
              owner={c.run ? (names[c.run.owner_id] ?? "a user") : ""}
              entryName={entryName}
              onPin={(e) => void pin(c, e)}
              onConfirm={setConfirm}
            />
          ))}
        </div>
      </main>
      {card && (
        <SettingsPanel
          key={card.host}
          c={card}
          onSaved={reload}
          onEvict={() => setConfirm(evictAll(card.host))}
        />
      )}
      <Modal open={!!confirm} onClose={() => setConfirm(null)} title={confirm?.title ?? ""}>
        <p className="mb-4 text-[13px] text-t2">{confirm?.text}</p>
        <div className="flex justify-end gap-2">
          <button type="button" className="chip bevel" onClick={() => setConfirm(null)}>
            Cancel
          </button>
          <button
            type="button"
            className="chip bevel bg-a1 font-semibold text-on1"
            onClick={() => confirm && void unload(confirm)}
          >
            Unload
          </button>
        </div>
      </Modal>
    </>
  );
}

const ROW = "flex justify-between gap-2.5 text-[13px]";
const VAL = "mono text-right text-[12px] text-t2";

function HostSection({
  c,
  selected,
  onSelect,
  now,
  owner,
  entryName,
  onPin,
  onConfirm,
}: {
  c: HostCard;
  selected: boolean;
  onSelect: () => void;
  now: number;
  owner: string;
  entryName: (e: HostEntry) => string;
  onPin: (e: HostEntry) => void;
  onConfirm: (c: Confirm) => void;
}) {
  const mac =
    c.machine?.os === "macos" || (!c.machine && c.envs.some((e) => MAC_RUNTIMES.includes(e.env)));
  const Icon = mac ? Monitor : Server;
  const pct = (gb: number) => (c.budget_gb ? `${Math.min(100, (gb / c.budget_gb) * 100)}%` : "0%");
  const cachedEnvs = c.envs.filter((e) => (e.cached_gb ?? 0) > 0);
  const pending = c.envs.some((e) => e.admin?.unload_pending);
  const off = c.state === "offline" ? "Offline" : undefined;
  return (
    <section
      aria-label={`${c.host} ${machineText(c.machine)}`}
      // docs/mock/ContextMenus.dc.html frame 12: the card's Unload all and the settings' Evict all, with their cards
      onContextMenu={(ev) =>
        openMenu(ev, {
          title: c.host,
          items: [
            {
              label: "Unload all…",
              icon: X,
              danger: true,
              off,
              onPick: () => onConfirm(unloadAll(c.host)),
            },
            {
              label: "Evict all…",
              icon: X,
              danger: true,
              off,
              onPick: () => onConfirm(evictAll(c.host)),
            },
          ],
        })
      }
      className={`panel flex min-w-0 flex-col gap-3.5 p-4 ${selected ? "outline-2 outline-offset-3 outline-a1" : ""}`}
    >
      <div className="flex items-center gap-3">
        <span
          aria-hidden
          className="tile size-10 rounded-[13px]"
          style={
            {
              "--tile": mac
                ? "linear-gradient(155deg,#c9ced9,#5b6478)"
                : "linear-gradient(155deg,#7af5d6,#12967f)",
            } as React.CSSProperties
          }
        >
          <Icon size={20} strokeWidth={1.75} />
        </span>
        <div className="flex min-w-0 flex-auto flex-col gap-0.5">
          <h2 className="m-0 font-display text-base font-bold">
            <button
              type="button"
              aria-pressed={selected}
              className="border-0 bg-transparent p-0 text-left"
              onClick={onSelect}
            >
              {c.host}
            </button>
          </h2>
          <span className="mono text-[12px] text-t2">{machineText(c.machine)}</span>
        </div>
        {c.state === "running" ? (
          <span className="tag inline-flex items-center gap-1.5 bg-[color-mix(in_srgb,var(--a1)_22%,var(--s3))]">
            <span className="size-[7px] rounded-full bg-a1 shadow-[0_0_8px_var(--a1)]" />
            Running
          </span>
        ) : (
          <span className="tag inline-flex items-center gap-1.5">
            <span className="size-[7px] rounded-full bg-t2" />
            {c.state === "idle" ? "Idle" : "Offline"}
          </span>
        )}
      </div>
      <div className="flex flex-wrap gap-1.5">
        {c.envs.map((e) => (
          <span key={e.env} className="tag">
            env {e.env}
          </span>
        ))}
        {c.policy && <span className="tag">{afterRunTag(c.policy.after_run)}</span>}
      </div>
      <div className="flex flex-col gap-1.5">
        <div className={ROW}>
          <span>Memory</span>
          <span className={VAL}>
            {c.used_gb} of {c.budget_gb} GB
          </span>
        </div>
        <div
          role="meter"
          aria-label="Memory used"
          aria-valuemin={0}
          aria-valuemax={c.budget_gb}
          aria-valuenow={c.used_gb}
          className="flex h-3 overflow-hidden rounded-full bg-inset shadow-[inset_0_2px_3px_color-mix(in_srgb,var(--shd)_60%,transparent)]"
        >
          <span className="block h-full bg-a1" style={{ width: pct(c.components_gb) }} />
          <span className="block h-full bg-a2" style={{ width: pct(c.cached_gb) }} />
        </div>
        <div className="flex flex-wrap gap-3 text-[11px] text-t2">
          <span>
            <span className="mr-[5px] inline-block size-2 rounded-[3px] bg-a1" />
            components
          </span>
          {c.cached_gb > 0 && (
            <span>
              <span className="mr-[5px] inline-block size-2 rounded-[3px] bg-a2" />
              cached handles
            </span>
          )}
          {c.policy && (
            <span className="mono">
              reserve {c.policy.reserve_mode === "auto" ? "auto" : `${c.policy.reserve_gb} GB`}
            </span>
          )}
        </div>
      </div>
      {c.run ? (
        <div className="flex flex-col gap-2 rounded-[18px_18px_18px_8px] bg-s2 p-3">
          <div className={ROW}>
            <span className="font-semibold">Current run</span>
            <span className={VAL}>{c.run.run_id.slice(0, 8)}</span>
          </div>
          <div className="flex flex-wrap gap-1.5">
            <span className="tag">env {c.run.env}</span>
            <span className="tag">{c.run.lane}</span>
            <span className="tag">{owner}</span>
            {pending && <span className="tag">unload after this run</span>}
          </div>
          <div className={ROW}>
            <span>
              {c.run.phase ?? c.run.node ?? "Starting"}
              {c.run.step && c.run.of ? ` · step ${c.run.step} of ${c.run.of}` : ""}
            </span>
            <span className={VAL}>
              {c.run.eta_s != null ? `≈ ${duration(c.run.eta_s * 1000)} left` : ""}
            </span>
          </div>
          {c.run.step && c.run.of ? (
            <div
              role="progressbar"
              aria-label="Run progress"
              aria-valuemin={0}
              aria-valuemax={c.run.of}
              aria-valuenow={c.run.step}
              className="flex h-1.5 overflow-hidden rounded-full bg-inset"
            >
              <span
                className="block h-full bg-t1"
                style={{ width: `${(c.run.step / c.run.of) * 100}%` }}
              />
            </div>
          ) : null}
        </div>
      ) : (
        <div className="flex flex-col gap-2 rounded-[18px_18px_18px_8px] border border-dashed border-line p-3">
          <div className={ROW}>
            <span className="font-semibold">Current run</span>
            <span className={VAL}>none</span>
          </div>
          <div className={ROW}>
            <span className="text-t2">Idle for</span>
            <span className={VAL}>
              {c.admin?.idle_since ? duration(now - Date.parse(c.admin.idle_since)) : "–"}
            </span>
          </div>
        </div>
      )}
      <div className="flex flex-col gap-2">
        <div className="flex items-center gap-2">
          <h3 className="heading flex-auto">Resident components</h3>
          <button
            type="button"
            className="chip bevel h-[30px]"
            disabled={c.state === "offline"}
            onClick={() => onConfirm(unloadAll(c.host))}
          >
            Unload all
          </button>
        </div>
        {c.entries.map((e, i) => {
          const name = entryName(e);
          const pinned = isPinned(c, e);
          const ref = pinRef(e);
          const baked = e.r.loras?.length ?? 0;
          const why = ref ? undefined : "This baked set cannot be named";
          const unload = () =>
            ref &&
            onConfirm({
              host: c.host,
              title: `Unload ${name}?`,
              text: `${e.env} unloads it${pinned ? " even though it is pinned; the pin stays set" : ""}. A running run finishes first.`,
              body: { entries: [ref] },
            });
          return (
            <div
              key={i}
              onContextMenu={(ev) => {
                ev.stopPropagation(); // not the card's menu
                openMenu(ev, {
                  title: name,
                  items: [
                    {
                      label: pinned ? "Unpin" : "Pin",
                      icon: pinned ? PinOff : Pin,
                      off: why,
                      onPick: () => onPin(e),
                    },
                    "-",
                    { label: "Unload…", icon: X, danger: true, off: why, onPick: unload },
                  ],
                });
              }}
              className="flex min-h-11 items-center gap-2.5 rounded-[14px_14px_14px_6px] bg-s2 py-1.5 pr-2 pl-3"
            >
              <div className="flex min-w-0 flex-auto flex-col">
                <b className="truncate text-[13px] font-semibold">{name}</b>
                <small className="mono truncate text-[10px] text-t2">
                  {e.env} · {e.r.precision}
                  {e.r.compute && e.r.compute !== "float32" && ` · ${e.r.compute}`}
                  {e.r.device === "cpu" ? " · host RAM" : ""}
                  {baked ? ` · ${baked} LoRA${baked === 1 ? "" : "s"} baked` : ""}
                  {e.r.pinned_why === "in_use" ? " · in use" : ""}
                  {e.r.last_used ? ` · used ${ago(e.r.last_used, now)}` : ""}
                </small>
              </div>
              <span className="mono w-16 shrink-0 text-right text-[12px] text-t2">
                {e.r.gb.toFixed(1)} GB
              </span>
              <button
                type="button"
                role="switch"
                aria-checked={pinned}
                aria-label={`Pin ${name}`}
                disabled={!ref}
                title={ref ? undefined : "This baked set cannot be named, so it cannot be pinned"}
                onClick={() => onPin(e)}
                className={`flex h-[26px] w-11 shrink-0 rounded-full border bg-s1 p-0.5 disabled:opacity-50 ${pinned ? "justify-end border-a1" : "border-line"}`}
              >
                <span className={`block size-5 rounded-full ${pinned ? "bg-a1" : "bg-t2"}`} />
              </button>
              <button
                type="button"
                aria-label={`Unload ${name}`}
                disabled={!ref}
                className="grid size-[30px] shrink-0 place-items-center rounded-[10px] border-0 bg-s4 p-0 text-t2"
                onClick={unload}
              >
                <X size={14} aria-hidden />
              </button>
            </div>
          );
        })}
        {cachedEnvs.map((e) => (
          <div
            key={`cached-${e.env}`}
            className="flex min-h-11 items-center gap-2.5 rounded-[14px_14px_14px_6px] bg-s2 py-1.5 pr-2 pl-3"
          >
            <div className="flex min-w-0 flex-auto flex-col">
              <b className="truncate text-[13px] font-semibold">Cached handles</b>
              <small className="mono truncate text-[10px] text-t2">{e.env} · evicted first</small>
            </div>
            <span className="mono w-16 shrink-0 text-right text-[12px] text-t2">
              {e.cached_gb!.toFixed(1)} GB
            </span>
            <span className="w-11 shrink-0" />
            <button
              type="button"
              aria-label={`Unload cached handles of ${e.env}`}
              className="grid size-[30px] shrink-0 place-items-center rounded-[10px] border-0 bg-s4 p-0 text-t2"
              onClick={() =>
                onConfirm({
                  host: c.host,
                  title: `Unload cached handles of ${e.env}?`,
                  text: "Drops its cached node results, idle adapters and the device cache. Components stay loaded.",
                  body: { cached_env: e.env },
                })
              }
            >
              <X size={14} aria-hidden />
            </button>
          </div>
        ))}
        {!c.entries.length && !cachedEnvs.length && (
          <p className="m-0 text-[13px] text-t2">Nothing loaded.</p>
        )}
      </div>
      <div className={`${ROW} text-t2`}>
        <span>Heartbeat</span>
        <span className={VAL}>{ago(c.heartbeat, now)}</span>
      </div>
    </section>
  );
}

/** The selected host's settings: saved shortly after each change and applied by its runners between runs. */
function SettingsPanel({
  c,
  onSaved,
  onEvict,
}: {
  c: HostCard;
  onSaved: () => void;
  onEvict: () => void;
}) {
  const [draft, setDraft] = useState(() => settingsOf(c));
  const [dirty, setDirty] = useState(false);
  const [error, setError] = useState("");
  const change = (p: Partial<S<"HostSettingsPut">>) => {
    setDraft((d) => ({ ...d, ...p }));
    setDirty(true);
  };
  useEffect(() => {
    if (!dirty) return;
    const t = setTimeout(() => {
      ok(
        api.PUT("/v1/studio/admin/hosts/{host}/settings", {
          params: { path: { host: c.host } },
          body: draft,
        }),
      )
        .then(() => {
          setError("");
          setDirty(false);
          onSaved();
        })
        .catch((e) => {
          setError(message(e));
          setDirty(false);
        });
    }, 600);
    return () => clearTimeout(t);
  }, [draft, dirty, c.host, onSaved]);
  const reserveMax = Math.max(32, draft.reserve_gb);
  return (
    <aside
      aria-label="Host settings"
      className="aside m-3.5 flex flex-[1_1_340px] flex-col gap-3.5 px-[18px] pt-4 pb-6 md:ml-0"
    >
      <div className="flex flex-col gap-0.5">
        <h2 className="flex items-center font-display text-base leading-5 font-bold">
          {c.host}
          <Info>Settings for the selected host. They apply to every env on it.</Info>
        </h2>
        <span className="mono text-[12px] text-t2">{deviceLine(c)}</span>
      </div>
      <fieldset className="m-0 flex flex-col gap-1 rounded-[18px_18px_18px_8px] border-0 bg-s2 p-3">
        <legend className="float-left flex items-center pb-1.5 text-[12px] font-semibold text-t2">
          After the queue drains
          <Info>
            Unload everything stops the worker processes, the only way to guarantee memory goes back
            to the system. Keep warm leaves components loaded until memory is needed or the idle
            timeout passes. Keep pinned only keeps pinned components and unloads the rest.
          </Info>
        </legend>
        {AFTER_RUN.map(([v, label]) => (
          <label key={v} className="flex min-h-8 items-center gap-2.5 text-[13px]">
            <input
              type="radio"
              name="after"
              className="accent-[var(--a1)]"
              checked={draft.after_run === v}
              onChange={() => change({ after_run: v })}
            />
            {label}
          </label>
        ))}
        <div className="flex flex-col gap-1.5 pt-1.5 pl-7">
          <label htmlFor="h-idle" className="label">
            Idle timeout, minutes (0 = never)
          </label>
          <input
            id="h-idle"
            className="in mono w-[120px]"
            type="number"
            min={0}
            max={1440}
            value={draft.idle_unload_min}
            onChange={(e) =>
              change({
                idle_unload_min: Math.min(1440, Math.max(0, Math.round(+e.target.value || 0))),
              })
            }
          />
        </div>
      </fieldset>
      <div className="flex flex-col gap-1.5">
        <span className="label flex items-center">
          Memory budget
          <Info>
            Automatic uses the macOS GPU memory limit when one is set, else RAM minus the default
            reserve. On an NVIDIA host it is VRAM minus the default reserve. Custom is physical
            memory minus the reserve you set. Both are also checked against live system memory
            pressure.
          </Info>
        </span>
        <div className="seg" role="group" aria-label="Memory budget">
          {(["auto", "custom"] as const).map((m) => (
            <button
              key={m}
              type="button"
              aria-pressed={draft.reserve_mode === m}
              onClick={() => change({ reserve_mode: m })}
            >
              {m === "auto" ? "Automatic" : "Custom"}
            </button>
          ))}
        </div>
        {draft.reserve_mode === "auto" ? (
          <div className={ROW}>
            <span className="text-t2">Budget</span>
            <span className={VAL}>{autoBudget(c)}</span>
          </div>
        ) : (
          <>
            <label htmlFor="h-reserve" className="label flex items-center pt-1.5">
              Memory reserve, GB
              <Info>
                Budget is physical memory (the GPU&apos;s memory on a CUDA host) minus this reserve,
                also checked against live system memory pressure.
              </Info>
            </label>
            <div className="flex items-center gap-2.5">
              <Range
                id="h-reserve"

                min={0}
                max={reserveMax}
                step={0.5}
                value={draft.reserve_gb}
                onChange={(e) => change({ reserve_gb: +e.target.value })}
                className="min-w-0 flex-auto accent-[var(--a1)]"
              />
              <span className="mono shrink-0 text-[12px]">{draft.reserve_gb} GB</span>
            </div>
            <div className={ROW}>
              <span className="text-t2">Budget</span>
              <span className={VAL}>{c.budget_gb} GB</span>
            </div>
          </>
        )}
      </div>
      {error && <ErrorNote>{error}</ErrorNote>}
      <div className="flex flex-col gap-2 rounded-[18px_18px_18px_8px] bg-s2 p-3">
        <h3 className="heading">Envs</h3>
        {c.envs.map((e) => (
          <div key={e.env} className={ROW}>
            <span>{e.env}</span>
            <span className={VAL}>
              {e.state === "offline" ? "offline" : "worker up"}
              {e.memory ? ` · ${e.memory.used_gb} GB` : ""}
            </span>
          </div>
        ))}
      </div>
      <div className="grow" />
      <button
        type="button"
        className="key flex h-12 items-center justify-center bg-a2 text-sm text-[var(--on2)]"
        disabled={c.state === "offline"}
        onClick={onEvict}
      >
        Evict all
      </button>
    </aside>
  );
}
