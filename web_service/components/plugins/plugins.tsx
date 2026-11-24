"use client";
// Plugins (docs/mock/Plugins.dc.html; API 1.61): the admin-only rail page. The table lists what studio found in
// plugins/ (Rescan reads it again); a row opens its aside: the switch, its rows, MAY DO, the disclaimer and, for a
// model-provider plugin that is on, Add as provider (a personal plugin's provider is its admin's own).
import Link from "next/link";
import { useCallback, useEffect, useId, useState } from "react";
import { ErrorNote, Info, Modal } from "@/components/kit";
import { ApiError, api, message, ok, type S } from "@/lib/api";
import { KEY_REFUSED, KIND, hostOf, providerTitle, rescanLine, statusOf } from "@/lib/plugins";

type Plugin = S<"Plugin">;
const path = (plugin: string) => ({ params: { path: { plugin } } });
const ME_TAG = "tag bg-[color-mix(in_srgb,#f2c14e_22%,transparent)] text-[#f2c14e]";
const KIND_TAG = "tag bg-[color-mix(in_srgb,var(--a2)_22%,transparent)] text-t1";
const CHIP = "chip bevel h-[30px] text-[12px]";

export function PluginsScreen() {
  const [list, setList] = useState<Plugin[] | null>(null);
  const [error, setError] = useState("");
  const [note, setNote] = useState("");
  const [sel, setSel] = useState("");
  const [added, setAdded] = useState<{ plugin: string; title: string } | null>(null); // its aside says so
  const [removing, setRemoving] = useState<Plugin | null>(null);
  const load = useCallback(() => {
    ok(api.GET("/v1/studio/admin/plugins"))
      .then((l) => {
        setList(l.plugins);
        setError("");
      })
      .catch((e) => setError(message(e)));
  }, []);
  useEffect(() => {
    load();
  }, [load]);
  const upsert = (p: Plugin) => setList((l) => l && l.map((x) => (x.name === p.name ? p : x)));

  async function rescan() {
    try {
      const r = await ok(api.POST("/v1/studio/admin/plugins/rescan"));
      setList(r.plugins);
      setNote(rescanLine(r));
      setError("");
      return r.new;
    } catch (e) {
      setError(message(e));
      return [];
    }
  }
  async function toggle(p: Plugin) {
    try {
      upsert(
        await ok(
          api.PUT("/v1/studio/admin/plugins/{plugin}", {
            ...path(p.name),
            body: { enabled: !p.enabled },
          }),
        ),
      );
    } catch (e) {
      setError(message(e));
    }
  }
  async function remove(p: Plugin) {
    try {
      await ok(api.DELETE("/v1/studio/admin/plugins/{plugin}", path(p.name)));
      setList((l) => l && l.filter((x) => x.name !== p.name));
    } catch (e) {
      setError(message(e));
    }
    setRemoving(null);
  }

  const shown = list?.find((p) => p.name === sel) ?? list?.[0];
  const cell = "border-b border-s3 p-2.5 align-top";
  return (
    <>
      <main id="main" className="flex min-w-0 flex-[999_1_560px] flex-col gap-4 px-5 pt-4 pb-6">
        <div className="flex flex-wrap items-center gap-3">
          <h1 className="font-display text-[22px] font-bold tracking-[0.01em]">Plugins</h1>
          <div className="grow" />
          <button type="button" className="chip bevel" onClick={() => void rescan()}>
            Rescan
          </button>
          <AddMenu onRescan={() => void rescan()} />
        </div>
        <div className="-mt-1.5 flex flex-col gap-0.5">
          <span className="text-[13px] text-t2">
            Add-ons that plug new features in. Found in plugins/ on start; off until you turn them
            on.
            <Info>
              A plugin is a folder in plugins/ with a manifest: name, kind, version, source, what it
              may do. The studio lists it on start and never runs it until an admin turns it on.
              Kinds: Model provider, Assistant tools.
            </Info>
          </span>
          <span className="text-[11px] text-t2 opacity-80">
            Admins only: other users don&apos;t see this page.
          </span>
        </div>
        {error && <ErrorNote onRetry={load}>{error}</ErrorNote>}
        {note && (
          <p role="status" className="flex flex-wrap items-center gap-2.5 text-[13px]">
            {note}
            <button type="button" className={`${CHIP} h-[26px]`} onClick={() => setNote("")}>
              Done
            </button>
          </p>
        )}
        {list?.length === 0 ? (
          <div className="panel flex flex-col items-center gap-2 px-5 py-6 text-center">
            <span className="font-semibold">No plugins found</span>
            <span className="text-[13px] text-t2">
              Plugins are add-ons you copy into plugins/. They stay off until you turn them on.
            </span>
            <button type="button" className={CHIP} onClick={() => void rescan()}>
              Rescan
            </button>
          </div>
        ) : (
          <div className="panel min-h-0 overflow-auto px-2 py-1.5" aria-busy={!list && !error}>
            <table className="w-full min-w-[760px] border-collapse text-[13px]">
              <thead>
                <tr className="text-left text-[11px] tracking-[0.04em] text-t2">
                  {["Plugin", "Kind", "May do", "Status", "Actions"].map((h) => (
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
                {list?.map((p) => {
                  const st = statusOf(p);
                  const on = p.name === shown?.name;
                  return (
                    <tr
                      key={p.name}
                      aria-selected={on}
                      className={on ? "bg-s3" : undefined}
                      onClick={() => setSel(p.name)}
                    >
                      <td className={cell}>
                        <button
                          type="button"
                          className="text-left font-semibold"
                          onClick={() => setSel(p.name)}
                        >
                          {p.title}
                        </button>
                        <br />
                        <span className="mono text-[12px] text-t2">
                          {[p.version, p.author, p.source ?? `plugins/${p.name}`].join(" · ")}
                        </span>
                        {p.personal && (
                          <>
                            <br />
                            <span className={`${ME_TAG} mt-1 inline-block`}>Personal use only</span>
                          </>
                        )}
                      </td>
                      <td className={cell}>
                        <span className={KIND_TAG}>{KIND[p.kind]}</span>
                      </td>
                      <td className={`${cell} max-w-[260px] text-[12px] text-t1b`}>
                        {p.may_do.join(" · ")}
                      </td>
                      <td className={cell}>
                        <Dot ok={st.ok} />{" "}
                        <span className={`font-semibold ${st.ok ? "text-[#7fd88f]" : "text-t2"}`}>
                          {st.text}
                        </span>
                        {st.sub && (
                          <>
                            <br />
                            <span className="mono text-[11px] text-t2">{st.sub}</span>
                          </>
                        )}
                      </td>
                      <td className={cell} onClick={(e) => e.stopPropagation()}>
                        <div className="flex flex-wrap items-center gap-1.5">
                          {p.provider ? (
                            <>
                              <Dot ok />
                              <span className="text-[#7fd88f]">Added</span>
                              <Link href="/admin?tab=providers" className={CHIP}>
                                Open in Model providers
                              </Link>
                            </>
                          ) : (
                            p.kind === "model-provider" &&
                            p.status === "running" && (
                              <button type="button" className={CHIP} onClick={() => setSel(p.name)}>
                                Add as provider
                              </button>
                            )
                          )}
                          <button type="button" className={CHIP} onClick={() => void toggle(p)}>
                            {p.enabled ? "Disable" : "Enable"}
                          </button>
                          <button type="button" className={CHIP} onClick={() => setRemoving(p)}>
                            Remove
                          </button>
                        </div>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
      </main>
      {shown && (
        <PluginPanel
          key={shown.name}
          p={shown}
          added={added?.plugin === shown.name ? added.title : ""}
          onToggle={() => void toggle(shown)}
          onRetry={load}
          onAdded={(title) => {
            setAdded({ plugin: shown.name, title });
            load(); // its row shows Added
          }}
          onDone={() => setAdded(null)}
        />
      )}
      <Modal
        open={!!removing}
        onClose={() => setRemoving(null)}
        title={`Remove ${removing?.title}?`}
      >
        <p className="mb-4 text-[13px] text-t2">
          {removing?.provider && "Its provider goes too. "}The folder stays in plugins/.
        </p>
        <div className="flex justify-end gap-2">
          <button
            type="button"
            className="chip bevel bg-[#ff6b6b] font-semibold text-[#2b0505]"
            onClick={() => removing && void remove(removing)}
          >
            Remove
          </button>
          <button type="button" className="chip bevel" onClick={() => setRemoving(null)}>
            Cancel
          </button>
        </div>
      </Modal>
    </>
  );
}

const Dot = ({ ok }: { ok: boolean }) => (
  <span
    aria-hidden
    className={`inline-block size-2 shrink-0 rounded-full ${ok ? "bg-[#7fd88f]" : "bg-t2"}`}
  />
);

/** Add plugin: from the plugins/ folder (copy it there, then Rescan); from a URL comes later. */
function AddMenu({ onRescan }: { onRescan: () => void }) {
  const id = useId().replace(/:/g, "");
  const close = () => document.getElementById(id)?.hidePopover?.();
  return (
    <>
      <button
        type="button"
        popoverTarget={id}
        aria-haspopup="menu"
        className="chip bevel bg-a1 font-semibold text-on1"
        style={{ anchorName: `--${id}` } as React.CSSProperties}
      >
        Add plugin
      </button>
      <div
        id={id}
        popover="auto"
        role="menu"
        aria-label="Add plugin"
        className="pop w-[240px] flex-col gap-0.5 p-1.5 open:flex"
        style={
          {
            positionAnchor: `--${id}`,
            positionArea: "bottom span-left",
            marginTop: 4,
          } as React.CSSProperties
        }
      >
        <button
          type="button"
          role="menuitem"
          className="mi"
          onClick={() => {
            close();
            onRescan();
          }}
        >
          <span>
            From the plugins/ folder
            <small>Copy its folder there, then Rescan.</small>
          </span>
        </button>
        <button type="button" role="menuitem" className="mi" aria-disabled="true">
          <span>From a URL · later</span>
        </button>
      </div>
    </>
  );
}

/** The selected plugin's aside: switch, rows, MAY DO, disclaimer, then Add as provider (model-provider, on). */
function PluginPanel({
  p,
  added,
  onToggle,
  onRetry,
  onAdded,
  onDone,
}: {
  p: Plugin;
  /** the title of the provider just added, else "" */
  added: string;
  onToggle: () => void;
  onRetry: () => void;
  onAdded: (title: string) => void;
  onDone: () => void;
}) {
  const st = statusOf(p);
  const [link, rest] = [p.disclaimer?.link, p.disclaimer?.text ?? ""];
  const cut = rest.indexOf(". ");
  const rows: [string, React.ReactNode][] = [
    ["Kind", KIND[p.kind]],
    ["Version", p.version],
    ["Source", p.source ?? `plugins/${p.name}`],
    ["Author", p.author],
    [
      "Status",
      <span key="s" className={st.ok ? "text-[#7fd88f]" : "text-t2"}>
        {st.ok ? `Running · ${hostOf(p.url)}` : st.text}
      </span>,
    ],
  ];
  if (p.models?.length) rows.push(["Models", p.models.join(" · ")]);
  if (p.tools?.length) rows.push(["Tools", p.tools.map((t) => t.name).join(" · ")]);
  return (
    <aside
      aria-label={`Plugin: ${p.title}`}
      className="aside m-3.5 flex flex-[1_1_340px] flex-col gap-3 px-[18px] pt-4 pb-6 md:ml-0"
    >
      <div className="flex items-center gap-2">
        <h2 className="grow font-display text-base leading-5 font-bold">{p.title}</h2>
        <button
          type="button"
          role="switch"
          aria-checked={p.enabled}
          aria-label="Enabled"
          onClick={onToggle}
          className={`flex h-6 w-10 shrink-0 rounded-full p-0.5 ${p.enabled ? "justify-end bg-a1" : "bg-s4"}`}
        >
          <span className={`block size-5 rounded-full ${p.enabled ? "bg-on1" : "bg-t2"}`} />
        </button>
      </div>
      <span className="text-[13px] text-t1b">{p.description}</span>
      <div className="flex flex-col gap-1 text-[13px]">
        {rows.map(([k, v]) => (
          <div key={k} className="flex justify-between gap-3">
            <span className="text-t2">{k}</span>
            <span className="min-w-0 text-right break-words">{v}</span>
          </div>
        ))}
      </div>
      {p.may_do.length > 0 && (
        <div className="flex flex-col gap-1">
          <span className="heading">MAY DO</span>
          <ul className="m-0 list-disc pl-4 text-[12px] leading-[18px] text-t1b">
            {p.may_do.map((m) => (
              <li key={m}>{m}</li>
            ))}
          </ul>
        </div>
      )}
      {p.disclaimer && (
        <div className="rounded-[10px] px-2.5 py-2 text-[12px] leading-[17px] text-[#f2c14e] shadow-[inset_0_0_0_1px_#f2c14e]">
          {cut > 0 ? (
            <>
              <b>{rest.slice(0, cut + 1)}</b>
              {rest.slice(cut + 1)}
            </>
          ) : (
            rest
          )}
          {link && (
            <>
              {" "}
              <a href={link} target="_blank" rel="noreferrer" className="underline">
                Anthropic&apos;s terms
              </a>
            </>
          )}
        </div>
      )}
      {p.kind === "model-provider" && p.enabled && (
        <>
          <div className="h-px bg-s3" />
          {added ? (
            <Added title={added} onDone={onDone} />
          ) : p.status === "not_running" ? (
            <NotRunning p={p} onRetry={onRetry} />
          ) : (
            <AddProvider p={p} onAdded={onAdded} />
          )}
        </>
      )}
    </aside>
  );
}

function NotRunning({ p, onRetry }: { p: Plugin; onRetry: () => void }) {
  const [copied, setCopied] = useState(false);
  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-center gap-2 text-[13px]">
        <Dot ok={false} />
        <span className="font-semibold text-t2">Not running</span>
        <span className="text-[12px] text-t2">start it in a terminal</span>
      </div>
      {p.start && (
        <div className="mono flex items-center gap-2 rounded-[10px] bg-[var(--in)] py-1.5 pr-1.5 pl-2.5 text-[12px]">
          <span className="grow break-all">{p.start}</span>
          <button
            type="button"
            className={`${CHIP} h-[26px]`}
            onClick={() =>
              void navigator.clipboard?.writeText(p.start ?? "").then(() => setCopied(true))
            }
          >
            {copied ? "Copied" : "Copy"}
          </button>
        </div>
      )}
      <div className="flex gap-1.5">
        <button type="button" className={CHIP} onClick={onRetry}>
          Try again
        </button>
        <button type="button" className={CHIP} disabled>
          Add as provider
        </button>
      </div>
    </div>
  );
}

function Added({ title, onDone }: { title: string; onDone: () => void }) {
  return (
    <div role="status" className="flex flex-col gap-2 text-[13px]">
      <div className="flex items-center gap-2 font-semibold">
        <span className="text-[#7fd88f]">✓</span>
        {title} added
      </div>
      <span className="text-[12px] text-t2">Next: use it for the Assistant in AI agents.</span>
      <div className="flex gap-1.5">
        <Link href="/admin?tab=agents" className={`${CHIP} bg-a1 font-semibold text-on1`}>
          Open AI agents
        </Link>
        <button type="button" className={CHIP} onClick={onDone}>
          Done
        </button>
      </div>
    </div>
  );
}

function AddProvider({ p, onAdded }: { p: Plugin; onAdded: (title: string) => void }) {
  const [title, setTitle] = useState(providerTitle(p));
  const [key, setKey] = useState("");
  const [model, setModel] = useState(p.models?.[0] ?? "");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<{ text: string; red: boolean } | null>(null);
  async function add() {
    setBusy(true);
    setErr(null);
    try {
      const made = await ok(
        api.POST("/v1/studio/admin/plugins/{plugin}/provider", {
          ...path(p.name),
          body: { key, title: title.trim() || undefined, default_model: model || undefined },
        }),
      );
      setKey("");
      onAdded(made.title || made.name);
    } catch (e) {
      const red = e instanceof ApiError && e.status === 422 && e.message.startsWith(KEY_REFUSED);
      if (red) setKey(""); // nothing is saved: paste it again
      setErr({ text: message(e), red });
    }
    setBusy(false);
  }
  const keyPath = p.key_hint?.match(/\S*\/\S*/)?.[0];
  return (
    <form
      className="flex flex-col gap-3"
      onSubmit={(e) => {
        e.preventDefault();
        void add();
      }}
    >
      <h3 className="flex items-center text-sm font-bold">Add as provider</h3>
      <div className="flex flex-col gap-1.5">
        <label htmlFor="pl-name" className="label">
          Name
        </label>
        <input
          id="pl-name"
          className="in"
          value={title}
          onChange={(e) => setTitle(e.target.value)}
        />
      </div>
      <div className="flex flex-col gap-1.5">
        <label htmlFor="pl-key" className="label">
          Key
        </label>
        <input
          id="pl-key"
          className="in"
          type="password"
          autoComplete="off"
          required
          placeholder={p.start ? `Paste the key from ${p.start.split("/").pop()}` : "Paste the key"}
          value={key}
          onChange={(e) => setKey(e.target.value)}
        />
        {p.key_hint && (
          <span className="text-[12px] text-t2">
            {keyPath ? (
              <>
                {p.key_hint.slice(0, p.key_hint.indexOf(keyPath))}
                <span className="mono">{keyPath}</span>
                {p.key_hint.slice(p.key_hint.indexOf(keyPath) + keyPath.length)}
              </>
            ) : (
              p.key_hint
            )}
          </span>
        )}
      </div>
      {!!p.models?.length && (
        <div className="flex flex-col gap-1.5">
          <label htmlFor="pl-model" className="label">
            Default model
          </label>
          <select
            id="pl-model"
            className="in"
            value={model}
            onChange={(e) => setModel(e.target.value)}
          >
            {p.models.map((m) => (
              <option key={m}>{m}</option>
            ))}
          </select>
        </div>
      )}
      {err && (
        <div
          role="alert"
          className={`flex items-center gap-2 rounded-[10px] px-2.5 py-2 text-[12px] ${err.red ? "bg-[color-mix(in_srgb,#ff6b6b_14%,transparent)] text-[#ff8a8a]" : "bg-[color-mix(in_srgb,#f2c14e_14%,transparent)] text-[#f2c14e]"}`}
        >
          <span className="grow">{err.text}</span>
          {!err.red && (
            <button type="submit" className={`${CHIP} h-[26px]`} disabled={busy}>
              Try again
            </button>
          )}
        </div>
      )}
      {busy && (
        <div className="flex items-center gap-2 text-[13px] text-t2">
          <Dot ok={false} />
          Checking the key with {p.title}…
        </div>
      )}
      <button type="submit" className="key h-12 rounded-[14px] text-sm" disabled={busy || !key}>
        {busy ? "Adding…" : "Add"}
      </button>
    </form>
  );
}
