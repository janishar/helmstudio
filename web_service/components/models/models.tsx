"use client";
// Models (docs/mock/Models.dc.html, ModelDownload.dc.html, ModelsLoras.dc.html): every model family and variant with
// its components and sizes, and its presence on each host (present, partial, missing, downloading with progress,
// hash mismatch), for everyone; admins download a variant to a host (licence shown and accepted first, the gated
// note) and delete it from one; a model's optional add-ons (API 1.36) are listed under it with their own Download and
// Delete. The LoRAs tab is the library (components/models/loras.tsx). While anything is
// downloading the list is read every 2 s (GET /v1/studio/models carries each job's bytes), else every 15 s.
import Link from "next/link";
import { useCallback, useEffect, useRef, useState } from "react";
import { until, useDialogWait, useScreenTools } from "@/components/assistant/tools";
import { modelsTools, type Row } from "@/components/models/assistant";
import { ErrorNote, Info, Modal } from "@/components/kit";
import { openMenu, type Item } from "@/components/context-menu";
import {
  Check,
  Download,
  FileText,
  FolderOpen,
  HardDrive,
  PanelRight,
  Trash2,
  X,
} from "lucide-react";
import {
  DownloadsTag,
  FolderStrip,
  inMacApp,
  reveal,
  revealOff,
} from "@/components/models/folders";
import { LorasTab } from "@/components/models/loras";
import { useUser } from "@/components/shell";
import { api, message, ok, type S } from "@/lib/api";
import { safeUrl } from "@/lib/format";
import {
  anyDownloading,
  bytesText,
  canDelete,
  copyNote,
  deleteCopies,
  diskText,
  downloadFolders,
  downloadPlan,
  folderById,
  folderOf,
  foldersOf,
  forHost,
  hostText,
  jobOpen,
  deviceNote,
  mediaText,
  parts,
  percent,
  precisionTags,
  variantStored,
  runtimeTags,
  sharesText,
  stateText,
  titleOf,
  variantBytes,
} from "@/lib/models";

type List = S<"ModelList">;
type Model = S<"Model">;
type Variant = S<"ModelVariant">;
type VariantHost = S<"ModelVariantHost">;
type Addon = S<"ModelAddon">;
/** A right-click item the screen asks the selected variant's panel to open (its dialog). */
type Ask = { m: string; v: string; d: "download" | "delete" | "licence" };

/** The panel's target host at first: the assistant's, the picked host, else the first host it is not on. */
const firstTarget = (list: List, v: Variant, aim: string, host: string) =>
  aim || host || v.hosts.find((h) => h.state !== "present")?.host || list.hosts[0]?.host || "";

/** The model list, polled (fast while downloading) when the tab is visible; admins also get the model jobs. */
function useModels(admin: boolean) {
  const [list, setList] = useState<List | null>(null);
  const [jobs, setJobs] = useState<S<"ModelJob">[]>([]);
  const [error, setError] = useState("");
  const reload = useCallback(() => {
    ok(api.GET("/v1/studio/models"))
      .then((l) => {
        setList(l);
        setError("");
      })
      .catch((e) => setError(message(e)));
    if (admin)
      ok(api.GET("/v1/studio/admin/model-jobs"))
        .then((j) => setJobs(j.jobs))
        .catch(() => undefined);
  }, [admin]);
  const fast = !!list && anyDownloading(list.models);
  useEffect(() => {
    const tick = () => !document.hidden && reload();
    tick();
    const t = setInterval(tick, fast ? 2_000 : 15_000);
    // a download just ended: look again soon rather than in 15 s, so Download is not offered on what is present
    const settle = fast ? undefined : setTimeout(tick, 3_000);
    document.addEventListener("visibilitychange", tick);
    return () => {
      clearInterval(t);
      clearTimeout(settle);
      document.removeEventListener("visibilitychange", tick);
    };
  }, [reload, fast]);
  return { list, jobs, error, reload };
}

export function ModelsScreen({ tab }: { tab: "models" | "loras" }) {
  const admin = !!useUser()?.roles.includes("admin");
  const { list, jobs, error, reload } = useModels(admin);
  const [host, setHost] = useState("");
  const [sel, setSel] = useState<{ model: string; variant: string } | null>(null);
  // the assistant's openRow: the row's target host, and the row as the panel shows it
  const [aim, setAim] = useState("");
  const [ask, setAsk] = useState<Ask | null>(null);
  const [menuError, setMenuError] = useState("");
  const row = useRef<Row | null>(null);
  const bind = useCallback((r: Row | null) => {
    row.current = r;
  }, []);
  const models = forHost(list?.models ?? [], host);
  const model = models.find((m) => m.id === sel?.model) ?? models[0];
  const variant = model?.variants.find((v) => v.id === sel?.variant) ?? model?.variants[0];
  useScreenTools(
    // eslint-disable-next-line react-hooks/refs -- the row is read when an action runs, after render
    modelsTools({
      admin,
      tab,
      list,
      jobs,
      host,
      shown: model && variant ? { m: model, v: variant, host: aim || host } : null,
      row: () => row.current,
      openRow: async (model, variant, h) => {
        setHost((x) => (x && x !== h ? h : x));
        setSel({ model, variant });
        setAim(h);
        return until(
          () =>
            row.current?.m.id === model && row.current.v.id === variant && row.current.target === h,
          3000,
        );
      },
      reload,
    }),
  );
  // a variant card's right-click (docs/mock/ContextMenus.dc.html frame 10): selects it, then the panel's actions
  function cardMenu(e: React.MouseEvent, m: Model, v: Variant) {
    if (!list) return;
    const run = (d: Ask["d"]) => () => setAsk({ m: m.id, v: v.id, d });
    // the open panel's target host when it shows this variant, else the one it opens on
    const r = row.current;
    const target = r?.m.id === m.id && r.v.id === v.id ? r.target : firstTarget(list, v, aim, host);
    const plan = downloadPlan(list, v, target);
    const on = v.hosts.find((h) => h.host === target);
    const h = openMenu(e, {
      title: `${m.title} · ${v.title}`,
      items: [
        { label: "Open", icon: PanelRight, onPick: () => setSel({ model: m.id, variant: v.id }) },
        {
          label: "Licence",
          icon: FileText,
          off: plan.licence ? undefined : "No licence named",
          onPick: run("licence"),
        },
        inMacApp() && {
          label: "Show in Finder",
          icon: FolderOpen,
          off: (on?.folder ? revealOff(target) : `Not on ${target}`) ?? undefined,
          onPick: () => reveal(folderOf(list, v, target)),
        },
        ...copyItems(list, v, target),
        admin && "-",
        admin &&
          (on?.state === "downloading" && on.job
            ? {
                label: "Cancel download",
                icon: X,
                // the panel's Cancel download's call
                onPick: () =>
                  void ok(
                    api.POST("/v1/studio/admin/model-jobs/{job_id}/cancel", {
                      params: { path: { job_id: on.job!.id } },
                    }),
                  )
                    .then(() => (setMenuError(""), reload()))
                    .catch((e) => setMenuError(message(e))),
              }
            : {
                label: "Download to host…",
                icon: Download,
                off: plan.blocked
                  ? plan.blocked[0].toUpperCase() + plan.blocked.slice(1)
                  : undefined,
                onPick: run("download"),
              }),
        admin && {
          label: "Delete from host…",
          icon: Trash2,
          danger: true,
          off: canDelete(v, target) ? undefined : `Not on ${target}`,
          onPick: run("delete"),
        },
      ],
    });
    if (h) setSel({ model: m.id, variant: v.id });
  }
  // frame 1: the picked host's folders (Edit… is there), or every host with more than one
  const strips = !list
    ? []
    : host
      ? foldersOf(list, host).length
        ? [host]
        : []
      : list.hosts.filter((h) => (h.folders ?? []).length > 1).map((h) => h.host);
  const multi = list?.hosts.find((h) => strips.includes(h.host) && (h.folders ?? []).length > 1);
  const states = new Set(
    models.flatMap((m) => parts(m).flatMap((v) => v.hosts.map((h) => h.state))),
  );

  const header = (
    <div className="flex flex-wrap items-center gap-3">
      <h1 className="font-display text-[22px] font-bold tracking-[0.01em]">Models</h1>
      <div role="tablist" aria-label="Library" className="flex gap-1 rounded-full bg-s1 p-1">
        {(
          [
            ["models", "Models", "/models"],
            ["loras", "LoRAs", "/models?tab=loras"],
          ] as const
        ).map(([id, label, href]) => (
          <Link
            key={id}
            href={href}
            role="tab"
            aria-selected={tab === id}
            className={`chip h-8 ${tab === id ? "bg-t1 font-semibold text-panel" : "bg-transparent"}`}
          >
            {label}
          </Link>
        ))}
      </div>
      <div className="grow" />
      <label htmlFor="m-host" className="label">
        Host
      </label>
      <select
        id="m-host"
        className="in w-[180px]"
        value={host}
        onChange={(e) => setHost(e.target.value)}
      >
        <option value="">All hosts</option>
        {list?.hosts.map((h) => (
          <option key={h.host} value={h.host}>
            {h.host}
            {h.live ? "" : " (offline)"}
          </option>
        ))}
      </select>
      {admin && <span className="tag tag-accent">Admin</span>}
    </div>
  );

  if (tab === "loras")
    return <LorasTab header={header} admin={admin} hosts={list?.hosts ?? []} host={host} />;

  return (
    <>
      <main id="main" className="flex min-w-0 flex-[999_1_560px] flex-col gap-4 px-5 pt-4 pb-6">
        {header}
        <div className="flex flex-wrap gap-2">
          <Legend dot="bg-a1" text="present" />
          <Legend dot="shadow-[inset_0_0_0_1.5px_var(--t2)]" text="missing" />
          <Legend dot="bg-a2" text="downloading" />
          {states.has("partial") && (
            <Legend
              dot="bg-[linear-gradient(90deg,var(--a1)_50%,transparent_50%)] shadow-[inset_0_0_0_1.5px_var(--a1)]"
              text="partial"
            />
          )}
          {states.has("mismatch") && <Legend dot="bg-bad" text="hash mismatch" />}
          {multi && (
            <span className="tag inline-flex items-center gap-1.5">
              <DiskTag name={multi.folders!.at(-1)!.name} />
              folder
            </span>
          )}
        </div>
        {list &&
          strips.map((h) => (
            <FolderStrip key={h} list={list} host={h} admin={admin} onChanged={reload} />
          ))}
        {error && <ErrorNote onRetry={reload}>{error}</ErrorNote>}
        {menuError && <ErrorNote>{menuError}</ErrorNote>}
        {list && !models.length && <p className="text-t2">No host has published any model yet.</p>}
        {models.map((m) => (
          <ModelSection
            key={m.id}
            list={list}
            m={m}
            selected={m.id === model?.id}
            picked={m.id === model?.id ? variant?.id : undefined}
            onPick={(v) => setSel({ model: m.id, variant: v })}
            onMenu={(e, v) => cardMenu(e, m, v)}
            addons={
              list && (m.addons ?? []).length > 0 ? (
                <Addons list={list} m={m} admin={admin} onChanged={reload} />
              ) : null
            }
          />
        ))}
      </main>
      {list && model && variant && (
        <VariantPanel
          key={`${model.id}/${variant.id}/${aim}`}
          aim={aim}
          bind={bind}
          list={list}
          m={model}
          v={list.models.find((x) => x.id === model.id)!.variants.find((x) => x.id === variant.id)!}
          admin={admin}
          host={host}
          jobs={jobs}
          onChanged={reload}
          ask={ask?.m === model.id && ask.v === variant.id ? ask : null}
        />
      )}
    </>
  );
}

/** A copy's word in Copies (frames 1, 5c): used, spare, partial, hash mismatch, not connected. */
const copyWord = (c: S<"ModelCopy">, used: string | null | undefined) =>
  c.state === "unplugged"
    ? "not connected"
    : c.folder === used
      ? "used"
      : c.state === "present"
        ? "spare"
        : c.state === "mismatch"
          ? "hash mismatch"
          : c.state;

/** The model card menu's Copies on <host> (frame 5c), when it is in more than one folder there. */
function copyItems(list: List, v: Variant, host: string): Item[] {
  const h = v.hosts.find((x) => x.host === host);
  if ((h?.copies?.length ?? 0) < 2) return [];
  return [
    "-",
    { label: `Copies on ${host}`, heading: true },
    ...h!.copies!.map((c) => ({
      label: folderById(list, host, c.folder)?.name ?? c.folder,
      icon: c.folder === h!.folder ? Check : HardDrive,
      keys: copyWord(c, h!.folder),
    })),
  ];
}

function Legend({ dot, text }: { dot: string; text: string }) {
  return (
    <span className="tag inline-flex items-center gap-1.5">
      <span className={`size-2 shrink-0 rounded-full ${dot}`} />
      {text}
    </span>
  );
}

/** The model's cube colour by what it makes. */
const cube = (m: Model) =>
  m.media.includes("video")
    ? "linear-gradient(155deg, #ffb07a, #e0457a)"
    : "linear-gradient(155deg, #7af5d6, #12967f)";

function ModelSection({
  list,
  m,
  selected,
  picked,
  onPick,
  onMenu,
  addons,
}: {
  list: List | null;
  m: Model;
  selected: boolean;
  picked: string | undefined;
  onPick: (variant: string) => void;
  onMenu?: (e: React.MouseEvent, v: Variant) => void;
  /** the model's optional add-ons, under its variants */
  addons?: React.ReactNode;
}) {
  const licences = new Set(m.variants.map((v) => v.licence?.id).filter(Boolean));
  return (
    <section
      aria-label={m.title}
      className={`panel flex flex-col gap-3 p-4 ${selected ? "outline-2 outline-offset-2 outline-a1" : ""}`}
    >
      <div className="flex flex-wrap items-center gap-2.5">
        <span
          aria-hidden
          className="tile size-[30px] rounded-[10px]"
          style={{ "--tile": cube(m) } as React.CSSProperties}
        />
        <h2 className="m-0 font-display text-lg font-bold">{m.title}</h2>
        <span className="tag">{mediaText(m)}</span>
        {runtimeTags(m).map((r) => (
          <span key={r} className="tag">
            {r}
          </span>
        ))}
        <span className="mono text-[12px] text-t2">{m.id}</span>
        <div className="grow" />
        {licences.size > 1 && (
          <span className="flex items-center text-[12px] text-t2">
            Licence per variant
            <Info>
              Each variant carries its own licence. It is shown before an admin downloads it.
            </Info>
          </span>
        )}
      </div>
      <div className="grid grid-cols-1 gap-2.5 sm:grid-cols-2">
        {m.variants.map((v) => (
          <VariantCard
            key={v.id}
            list={list}
            m={m}
            v={v}
            picked={picked === v.id}
            onPick={() => onPick(v.id)}
            onMenu={onMenu && ((e) => onMenu(e, v))}
          />
        ))}
      </div>
      {addons}
    </section>
  );
}

/** docs/mock/Models.dc.html: "Optional add-ons" under a model, a dashed card each with its size, help, hosts and (for
 * admins) its own Download (to the first host it can go to: pick a host above for another) and Delete. */
function Addons({
  list,
  m,
  admin,
  onChanged,
}: {
  list: List;
  m: Model;
  admin: boolean;
  onChanged: () => void;
}) {
  return (
    <div className="flex flex-col gap-2">
      <h3 className="heading flex items-center">
        Optional add-ons
        <Info>
          Parts a few controls need, downloaded and deleted on their own. Every variant works
          without them; the controls that need one are greyed with &quot;… not downloaded&quot;
          until it is on the host.
        </Info>
      </h3>
      <div className="grid grid-cols-1 gap-2.5 sm:grid-cols-2">
        {(m.addons ?? []).map((a) => (
          <AddonCard
            key={a.component}
            list={list}
            m={m}
            a={a}
            admin={admin}
            onChanged={onChanged}
          />
        ))}
      </div>
    </div>
  );
}

function AddonCard({
  list,
  m,
  a,
  admin,
  onChanged,
}: {
  list: List;
  m: Model;
  a: Addon;
  admin: boolean;
  onChanged: () => void;
}) {
  const [dialog, setDialog] = useState<"download" | "delete" | null>(null);
  const [notice, setNotice] = useState("");
  const down = a.hosts.find((h) => !downloadPlan(list, a, h.host).blocked)?.host;
  const del = a.hosts.find((h) => canDelete(a, h.host))?.host;
  const btn = "h-7 rounded-lg bg-s4 px-2.5 text-[12px] font-medium disabled:opacity-50";
  return (
    <div
      onContextMenu={(e) =>
        admin &&
        openMenu(e, {
          title: `${m.title} · ${a.title}`,
          items: [
            {
              label: "Download…",
              icon: Download,
              off: down ? undefined : "Nothing to download",
              onPick: () => setDialog("download"),
            },
            "-",
            {
              label: "Delete…",
              icon: Trash2,
              danger: true,
              off: del ? undefined : "On no host",
              onPick: () => setDialog("delete"),
            },
          ],
        })
      }
      className="flex min-w-0 flex-col gap-2 rounded-xl border border-dashed border-line bg-s2 px-3 py-2.5"
    >
      <div className="flex items-center gap-2">
        <span className="tag">add-on</span>
        <b className="text-[13px]">{a.title}</b>
        <span className="mono truncate text-[12px] text-t2">{a.tag ?? a.component}</span>
        <div className="grow" />
        <span className="mono text-[12px]">{bytesText(a.download_bytes)}</span>
      </div>
      {a.help && <span className="text-[12px] text-t2">{a.help}</span>}
      <div className="flex flex-wrap items-center gap-1.5">
        {a.hosts.map((h) => (
          <HostChip key={h.host} list={list} h={h} label={`${a.title} on ${h.host}`} />
        ))}
        <div className="grow" />
        {admin && (
          <>
            <button
              type="button"
              className={btn}
              disabled={!down}
              title={down ? `Download to ${down}` : undefined}
              onClick={() => setDialog("download")}
            >
              Download…
            </button>
            <button
              type="button"
              className={`${btn} text-a2`}
              disabled={!del}
              title={del ? `Delete from ${del}` : undefined}
              onClick={() => setDialog("delete")}
            >
              Delete
            </button>
          </>
        )}
      </div>
      {notice && (
        <p role="status" className="m-0 text-[12px] text-t2">
          {notice}
        </p>
      )}
      {down && (
        <Modal
          open={dialog === "download"}
          onClose={() => setDialog(null)}
          title={`Download ${m.title} · ${a.title}`}
          width="w-[min(94vw,560px)]"
        >
          <DownloadDialog
            list={list}
            m={m}
            v={a}
            host={down}
            onClose={() => setDialog(null)}
            onDone={(j) => {
              setDialog(null);
              setNotice(`Download queued on ${j.host}.`);
              onChanged();
            }}
          />
        </Modal>
      )}
      {del && (
        <Modal
          open={dialog === "delete"}
          onClose={() => setDialog(null)}
          title={`Delete ${m.title} · ${a.title} from ${del}?`}
          width="w-[min(94vw,440px)]"
        >
          <DeleteDialog
            list={list}
            m={m}
            v={a}
            host={del}
            folder={folderOf(list, a, del)}
            onClose={() => setDialog(null)}
            onDone={(j) => {
              setDialog(null);
              setNotice(`Delete queued on ${j.host}.`);
              onChanged();
            }}
          />
        </Modal>
      )}
    </div>
  );
}

const TAG_Q = "tag bg-[color-mix(in_srgb,var(--a1)_22%,var(--s4))]";
const TAG_SH = "tag bg-[color-mix(in_srgb,var(--a2)_22%,var(--s4))]";

function VariantCard({
  list,
  m,
  v,
  picked,
  onPick,
  onMenu,
}: {
  list: List | null;
  m: Model;
  v: Variant;
  picked: boolean;
  onPick: () => void;
  onMenu?: (e: React.MouseEvent) => void;
}) {
  const tags = precisionTags(v.precisions, variantStored(v));
  const note = deviceNote(m, v);
  return (
    <div
      onClick={onPick}
      onContextMenu={onMenu}
      className={`flex min-w-0 cursor-pointer flex-col gap-2.5 rounded-[18px_18px_18px_8px] bg-[linear-gradient(180deg,var(--s3),var(--s2))] p-3 shadow-[inset_0_1px_0_rgba(255,255,255,.08),0_12px_22px_-12px_color-mix(in_srgb,var(--shd)_80%,transparent)] ${picked ? "outline-2 outline-t2" : ""}`}
    >
      <div className="flex items-center gap-2">
        <button
          type="button"
          aria-pressed={picked}
          className="border-0 bg-transparent p-0 text-left text-sm font-bold"
          onClick={(e) => {
            e.stopPropagation();
            onPick();
          }}
        >
          {v.title}
        </button>
        <span className="mono truncate text-[12px] text-t2">
          {v.licence?.id ?? "no download source"}
        </span>
        <div className="grow" />
        {note && (
          <span className="flex shrink-0 items-center text-[12px] text-t2">
            {note}
            <Info>
              {note === "Mac only"
                ? "This variant runs only on Apple silicon hosts."
                : "This variant runs only on NVIDIA (CUDA) hosts."}
            </Info>
          </span>
        )}
        <span className="mono text-[12px]">{bytesText(variantBytes(v))}</span>
      </div>
      <div className="flex flex-wrap items-center gap-1.5">
        {tags.map((t) => (
          <span key={t.text} className={t.q ? TAG_Q : `tag ${t.dim ? "text-t2" : ""}`}>
            {t.text}
          </span>
        ))}
        {sharesText(m, v).map((s) => (
          <span key={s} className={TAG_SH}>
            {s}
          </span>
        ))}
        {tags[1]?.q ? (
          <Info>
            Stored in {tags[0].text}. It is quantized to{" "}
            {v.precisions.filter((p) => p !== tags[0].text).join(" or ")} while loading, with no
            second copy on disk.
          </Info>
        ) : tags[1] ? (
          <Info>This variant is stored already quantized, so it loads as it is.</Info>
        ) : (
          <Info>This variant loads as it is stored.</Info>
        )}
      </div>
      <div className="flex flex-wrap gap-1.5">
        {v.hosts.map((h) => (
          <HostChip key={h.host} list={list} h={h} label={`${v.title} on ${h.host}`} />
        ))}
      </div>
    </div>
  );
}

/** The presence dot of one state. */
function Dot({ state }: { state: VariantHost["state"] }) {
  const cls = {
    present: "bg-a1",
    downloading: "bg-a2",
    missing: "shadow-[inset_0_0_0_1.5px_var(--t2)]",
    partial:
      "bg-[linear-gradient(90deg,var(--a1)_50%,transparent_50%)] shadow-[inset_0_0_0_1.5px_var(--a1)]",
    mismatch: "bg-bad",
    unplugged: "shadow-[inset_0_0_0_1.5px_var(--t2)]", // API 1.78: only on a folder not connected
  }[state];
  return <span className={`size-2 shrink-0 rounded-full ${cls}`} />;
}

/** A host chip's folder tag (frame 1): `Mac`, `T9`. */
function DiskTag({ name }: { name: string }) {
  return (
    <span
      title="Loaded from this folder"
      className="mono rounded-md bg-s4 px-1.5 text-[10px] leading-4 font-medium text-t1"
    >
      {name}
    </span>
  );
}

function HostChip({ list, h, label }: { list: List | null; h: VariantHost; label: string }) {
  const chip =
    "inline-flex min-w-0 items-center gap-1.5 rounded-full bg-s1 px-2.5 py-1 text-[12px]";
  if (h.state === "downloading") {
    const p = percent(h.job);
    return (
      <span className={`${chip} flex-col items-stretch gap-1 rounded-[14px] py-1.5`}>
        <span className="flex items-center gap-1.5">
          <Dot state="downloading" />
          {h.host}
          <span className="mono ml-auto pl-2 text-[12px]">{p == null ? "queued" : `${p}%`}</span>
        </span>
        <span
          role="progressbar"
          aria-label={label}
          aria-valuenow={p ?? undefined}
          aria-valuemin={0}
          aria-valuemax={100}
          className="block h-1 overflow-hidden rounded-[2px] bg-inset"
        >
          <span className="block h-full bg-a2" style={{ width: `${p ?? 0}%` }} />
        </span>
      </span>
    );
  }
  const { tag, note } = list ? copyNote(list, h) : { tag: null, note: null };
  if (h.state === "unplugged")
    return (
      <span
        className={`${chip} bg-transparent text-t2 shadow-[inset_0_0_0_1px_var(--ln)]`}
        title={
          note
            ? `Its only copy is on ${note.replace(/ not connected$/, "")}, which is unplugged`
            : "unplugged"
        }
      >
        <Dot state="unplugged" />
        {h.host}
        {note && ` · ${note}`}
      </span>
    );
  return (
    <span
      className={`${chip} ${h.state === "present" ? "" : h.state === "mismatch" ? "text-bad" : "text-t2"}`}
      title={stateText(h) + (h.mismatch?.length ? `: ${h.mismatch.join(", ")}` : "")}
    >
      <Dot state={h.state} />
      {h.host}
      {h.state === "partial" || h.state === "mismatch" ? (
        <span className="mono text-[11px]">{h.state === "partial" ? "partial" : "mismatch"}</span>
      ) : null}
      {tag && <DiskTag name={tag} />}
      {note && <span className="text-[11px] text-t2">{note}</span>}
    </span>
  );
}

/** The selected variant: its folder, components, per-host state, and the admin's Download and Delete. */
function VariantPanel({
  list,
  m,
  v,
  admin,
  host,
  jobs,
  onChanged,
  aim,
  bind,
  ask,
}: {
  list: List;
  m: Model;
  v: Variant;
  admin: boolean;
  host: string;
  jobs: S<"ModelJob">[];
  onChanged: () => void;
  /** a right-click item's dialog to open (the card's menu): each new one opens once */
  ask?: Ask | null;
  /** the target host the assistant opened the row on */
  aim?: string;
  bind?: (r: Row | null) => void;
}) {
  const targets = list.hosts.map((h) => h.host);
  const [target, setTarget] = useState(firstTarget(list, v, aim ?? "", host));
  const [dialog, setDialog] = useState<"download" | "delete" | "licence" | null>(null);
  // the assistant's Download: this row's own dialog, resolved with its job when it closes
  const dl = useDialogWait<S<"ModelJob">>();
  const closeDownload = () => {
    setDialog(null);
    dl.closed();
  };
  useEffect(() => {
    bind?.({
      m,
      v,
      target,
      download: () => {
        const p = dl.wait();
        setDialog("download");
        return p;
      },
    });
    return () => bind?.(null);
  });
  const [notice, setNotice] = useState<{ job: string; text: string } | null>(null);
  const [error, setError] = useState("");
  const shown = host || target;
  const on = v.hosts.find((h) => h.host === shown);
  const onTarget = v.hosts.find((h) => h.host === target);
  const last = jobs.find(
    (j) => j.host === target && j.model === m.id && !j.addon && (j.variant ?? v.id) === v.id,
  );
  const plan = downloadPlan(list, v, target);
  const [asked, setAsked] = useState(ask);
  if (ask !== asked) {
    setAsked(ask);
    if (ask) setDialog(ask.d);
  }

  async function cancel(id: string) {
    try {
      await ok(
        api.POST("/v1/studio/admin/model-jobs/{job_id}/cancel", {
          params: { path: { job_id: id } },
        }),
      );
      setError("");
      onChanged();
    } catch (e) {
      setError(message(e));
    }
  }
  return (
    <aside
      aria-label="Selected model"
      className="aside m-3.5 flex flex-[1_1_340px] flex-col gap-[18px] px-[18px] pt-4 pb-6 md:ml-0"
    >
      <div className="flex flex-col gap-1">
        <h2 className="font-display text-base leading-5 font-bold">
          {m.title} · {v.title}
        </h2>
        <span className="mono text-[12px] break-all text-t2">{folderOf(list, v, shown)}</span>
      </div>

      <div className="flex flex-col gap-2">
        <h3 className="heading flex items-center">
          Components
          <Info>
            A component marked shared is one set of files on disk and one copy in memory for every
            model that uses it.
          </Info>
        </h3>
        {v.components.map((c) => {
          const separate = on?.separate?.includes(c.component);
          return (
            <div
              key={c.component}
              className="flex flex-col gap-2 rounded-[18px_18px_18px_8px] bg-s2 px-3 py-2.5"
            >
              <div className="flex justify-between gap-2.5 text-[13px]">
                <span className="font-semibold">{c.component}</span>
                <span className="mono text-[12px] text-t2">{bytesText(c.bytes) || "–"}</span>
              </div>
              <div className="flex flex-wrap gap-1.5">
                {precisionTags(c.precisions, c.stored).map((t) =>
                  t.dim ? null : (
                    <span key={t.text} className={t.q ? TAG_Q : "tag"}>
                      {t.text}
                    </span>
                  ),
                )}
                {c.from_variant !== v.id && !separate && (
                  <span className={TAG_SH}>shared with {titleOf(m, c.from_variant)}</span>
                )}
                {separate && <span className="tag">own copy on {shown}: hashes differ</span>}
              </div>
            </div>
          );
        })}
        <div className="flex justify-between gap-2.5 text-[13px]">
          <span>On disk for this variant</span>
          <span className="mono text-[12px] text-t2">{bytesText(on?.bytes) || "–"}</span>
        </div>
      </div>

      <div className="flex flex-col gap-2">
        <h3 className="heading">Hosts</h3>
        {v.hosts.map((h) => (
          <div key={h.host} className="flex justify-between gap-2.5 text-[13px]">
            <span>{h.host}</span>
            <span
              className={`mono text-right text-[12px] ${h.state === "downloading" ? "text-a2" : h.state === "mismatch" ? "text-bad" : "text-t2"}`}
              title={h.mismatch?.join(", ")}
            >
              {hostText(list, h)}
            </span>
          </div>
        ))}
        {!v.hosts.length && <p className="m-0 text-[13px] text-t2">No host has an env for it.</p>}
      </div>

      {v.hosts
        .filter((h) => (h.copies?.length ?? 0) > 1)
        .map((h) => (
          <div key={h.host} className="flex flex-col gap-2">
            <h3 className="heading flex items-center">
              Copies on {h.host}
              <Info>
                The runner loads the copy in the first folder of the list. The other is used when
                that folder is unplugged.
              </Info>
            </h3>
            {h.copies!.map((c) => {
              const f = folderById(list, h.host, c.folder);
              const n = foldersOf(list, h.host).findIndex((x) => x.id === c.folder) + 1;
              return (
                <div
                  key={c.folder}
                  className="flex flex-col gap-1 rounded-[18px_18px_18px_8px] bg-s2 px-3 py-2.5"
                >
                  <div className="flex justify-between gap-2.5 text-[13px]">
                    <span className="font-semibold">
                      {n} · {f?.name ?? c.folder}
                    </span>
                    <span className="mono text-[12px] text-t2">{copyWord(c, h.folder)}</span>
                  </div>
                  {f && v.dir && (
                    <span className="mono text-[12px] break-all text-t2">
                      {f.root.replace(/\/$/, "")}/{v.dir}
                    </span>
                  )}
                </div>
              );
            })}
          </div>
        ))}

      {admin && (
        <div className="mt-auto flex flex-col gap-2">
          <h3 className="heading flex items-center">
            Admin
            <Info>
              Weights are files on each host&apos;s own disk. A download is a queued job; shared
              components already on the host are skipped.
            </Info>
          </h3>
          <label htmlFor="m-target" className="label">
            Target host
          </label>
          <select
            id="m-target"
            className="in"
            value={target}
            onChange={(e) => setTarget(e.target.value)}
          >
            {targets.map((t) => (
              <option key={t} value={t}>
                {t}
              </option>
            ))}
          </select>
          {last?.state === "failed" && last.failure && (
            <p role="status" className="m-0 text-[12px] text-bad">
              Last {last.kind === "download" ? "download" : "delete"} failed: {last.failure.detail}
            </p>
          )}
          {notice && jobOpen(jobs, notice.job) && (
            <p role="status" className="m-0 text-[12px] text-t2">
              {notice.text}
            </p>
          )}
          {error && <ErrorNote>{error}</ErrorNote>}
          {onTarget?.state === "downloading" && onTarget.job ? (
            <button
              type="button"
              className="key h-12 text-sm"
              onClick={() => void cancel(onTarget.job!.id)}
            >
              Cancel download
            </button>
          ) : (
            <button
              type="button"
              className="key h-12 text-sm"
              disabled={!!plan.blocked}
              title={plan.blocked ?? undefined}
              onClick={() => setDialog("download")}
            >
              Download to host
            </button>
          )}
          <button
            type="button"
            className="act justify-center disabled:opacity-50 text-a2"
            disabled={!canDelete(v, target)}
            onClick={() => setDialog("delete")}
          >
            Delete from host
          </button>
        </div>
      )}
      {plan.licence && (
        <Modal
          open={dialog === "licence"}
          onClose={() => setDialog(null)}
          title={`Licence · ${plan.licence.title}`}
          width="w-[min(94vw,560px)]"
        >
          <pre className="mono m-0 max-h-[50vh] overflow-auto rounded-[10px] bg-inset px-3 py-2.5 text-[12px] leading-[18px] whitespace-pre-wrap">
            {plan.licence.text}
          </pre>
        </Modal>
      )}
      <Modal
        open={dialog === "download"}
        onClose={closeDownload}
        title={`Download ${m.title} · ${v.title}`}
        width="w-[min(94vw,560px)]"
      >
        <DownloadDialog
          list={list}
          m={m}
          v={v}
          host={target}
          onClose={closeDownload}
          onDone={(j) => {
            dl.saved(j);
            closeDownload();
            setNotice({ job: j.id, text: `Download queued on ${j.host}.` });
            onChanged();
          }}
        />
      </Modal>
      <Modal
        open={dialog === "delete"}
        onClose={() => setDialog(null)}
        title={`Delete ${m.title} · ${v.title} from ${target}?`}
        width="w-[min(94vw,440px)]"
      >
        <DeleteDialog
          list={list}
          m={m}
          v={v}
          host={target}
          folder={folderOf(list, v, target)}
          onClose={() => setDialog(null)}
          onDone={(j) => {
            setDialog(null);
            setNotice({
              job: j.id,
              text:
                `Delete queued on ${j.host}.` +
                (j.keeps.length
                  ? ` Kept: ${j.keeps.map((k) => `${k.component} (for ${k.for.map((f) => titleOf(m, f)).join(", ")})`).join(", ")}.`
                  : ""),
            });
            onChanged();
          }}
        />
      </Modal>
    </aside>
  );
}

const ROW = "flex justify-between gap-2.5 text-[13px]";
const VAL = "mono min-w-0 text-right text-[12px] break-all text-t2";

/** docs/mock/ModelDownload.dc.html: where it goes, its size, the licence text with an explicit accept, the gated note. */
function DownloadDialog({
  list,
  m,
  v,
  host,
  onClose,
  onDone,
}: {
  list: List;
  m: Model;
  /** a variant, or an optional add-on (its `component`) */
  v: Variant | Addon;
  host: string;
  onClose: () => void;
  onDone: (j: S<"ModelJob">) => void;
}) {
  const plan = downloadPlan(list, v, host);
  // frame 4: a Folder pick on a host with more than one
  const pickable = downloadFolders(list, host);
  const many = pickable.folders.length > 1;
  const [folder, setFolder] = useState(pickable.pick);
  const into = folderById(list, host, folder);
  const free = many ? into?.free_bytes : list.hosts.find((h) => h.host === host)?.free_bytes;
  const [accepted, setAccepted] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  async function start() {
    setBusy(true);
    try {
      onDone(
        await ok(
          api.POST("/v1/studio/admin/models/{model}/download", {
            params: { path: { model: m.id } },
            body: {
              host,
              ...("component" in v ? { addon: v.component } : { variant: v.id }),
              ...(plan.mustAccept && { accept_licence: accepted }),
              ...(many && folder && { folder }),
            },
          }),
        ),
      );
    } catch (e) {
      setError(message(e));
    } finally {
      setBusy(false);
    }
  }
  const url = safeUrl(plan.licence?.url);
  return (
    <div className="flex flex-col gap-3.5">
      <div className={ROW}>
        <span>To host</span>
        <span className={VAL}>{host}</span>
      </div>
      <div className={ROW}>
        <span>Size</span>
        <span className={VAL}>
          {[bytesText(v.download_bytes) || "unknown", free != null && `${bytesText(free)} free`]
            .filter(Boolean)
            .join(" · ")}
        </span>
      </div>
      {many && (
        <div className="flex flex-col gap-1.5">
          <h3 className="heading">Folder</h3>
          <div role="radiogroup" aria-label="Folder" className="flex flex-col gap-1.5">
            {pickable.folders.map((f) => {
              const can = f.connected && f.writable;
              return (
                <Pick
                  key={f.id}
                  name="download-folder"
                  label={f.name}
                  path={f.root}
                  checked={folder === f.id}
                  off={!can}
                  onPick={() => setFolder(f.id)}
                  tag={f.downloads && <DownloadsTag />}
                  end={
                    !f.connected
                      ? "Not connected"
                      : !f.writable
                        ? "Read-only"
                        : `${diskText(f.free_bytes)} free`
                  }
                />
              );
            })}
          </div>
        </div>
      )}
      <div className={ROW}>
        <span>Into</span>
        <span className={VAL}>
          {many && into && v.dir
            ? `${into.root.replace(/\/$/, "")}/${v.dir}`
            : folderOf(list, v, host)}
        </span>
      </div>
      {plan.licence && (
        <div className="flex flex-col gap-1.5">
          <h3 className="heading flex items-center">
            Licence · {plan.licence.title}
            <Info>
              The variant&apos;s licence text, as the host&apos;s pack ships it. The job records
              which text you accepted.
            </Info>
          </h3>
          <pre
            aria-label="Licence text"
            className="mono m-0 max-h-[220px] overflow-auto rounded-[10px] bg-inset px-3 py-2.5 text-[12px] leading-[18px] whitespace-pre-wrap"
          >
            {plan.licence.text}
          </pre>
          {url && (
            <a
              href={url}
              target="_blank"
              rel="noreferrer"
              className="text-[12px] text-t2 hover:text-t1"
            >
              {plan.licence.url}
            </a>
          )}
        </div>
      )}
      {plan.mustAccept && (
        <label className="flex items-center gap-2.5 text-[13px]">
          <input
            type="checkbox"
            className="accent-[var(--a1)]"
            checked={accepted}
            onChange={(e) => setAccepted(e.target.checked)}
          />
          I accept this licence
        </label>
      )}
      {plan.gatedNote && (
        <div className="flex items-center gap-2 rounded-xl bg-s3 px-3 py-2.5 text-[13px]">
          <span className="tag tag-accent shrink-0">Gated</span>
          <span>{plan.gatedNote}</span>
        </div>
      )}
      {error && <ErrorNote>{error}</ErrorNote>}
      <div className="flex justify-end gap-2">
        <button type="button" className="chip bevel" onClick={onClose}>
          Cancel
        </button>
        <button
          type="button"
          className="chip bevel bg-a1 font-semibold text-on1"
          disabled={busy || (plan.mustAccept && !accepted)}
          onClick={() => void start()}
        >
          Download
        </button>
      </div>
    </div>
  );
}

/** A folder or copy pick (frames 4, 6): a radio row with its name, path, a tag and what it says at the end. */
function Pick({
  name,
  label,
  path,
  checked,
  off,
  onPick,
  tag,
  end,
}: {
  name: string;
  label: string;
  path?: string;
  checked: boolean;
  off?: boolean;
  onPick: () => void;
  tag?: React.ReactNode;
  end: string;
}) {
  return (
    <label
      className={`grid grid-cols-[18px_minmax(0,1fr)_auto] items-center gap-2.5 rounded-xl px-3 py-2.5 text-[13px] ${off ? "text-t2 shadow-[inset_0_0_0_1px_var(--ln)]" : checked ? "bg-s4 shadow-[inset_0_0_0_1.5px_var(--a1)]" : "bg-s3"}`}
    >
      <input
        type="radio"
        name={name}
        className="m-0 accent-[var(--a1)]"
        checked={checked}
        disabled={off}
        onChange={onPick}
      />
      <span className="flex min-w-0 items-center gap-2">
        <b className="font-semibold">{label}</b>
        {path && <span className="mono truncate text-[12px] text-t2">{path}</span>}
        {tag}
      </span>
      <span className="mono text-[12px] text-t2">{end}</span>
    </label>
  );
}

/** Delete with confirm; the server's refusal (loaded, in use, downloading) is shown as it words it. A variant in more
 * than one folder of the host asks which copy (frame 6): each with the space it gives back and the free space after, the
 * used one marked and picked first, Both (off while a copy is unplugged). */
function DeleteDialog({
  list,
  m,
  v,
  host,
  folder,
  onClose,
  onDone,
}: {
  list: List;
  m: Model;
  v: Variant | Addon;
  host: string;
  folder: string;
  onClose: () => void;
  onDone: (j: S<"ModelJob">) => void;
}) {
  const on = v.hosts.find((h) => h.host === host);
  const copies = deleteCopies(list, on);
  const many = copies.length > 1;
  const live = copies.filter((c) => c.copy.state !== "unplugged");
  const away = copies.find((c) => c.copy.state === "unplugged");
  const [pick, setPick] = useState<string>(live[0]?.copy.folder ?? "");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const both = pick === "*";
  const picked = both ? live : live.filter((c) => c.copy.folder === pick);
  const nameOf = (c: (typeof copies)[number] | undefined) =>
    c?.folder?.name ?? c?.copy.folder ?? "";
  const title = v.title;
  async function remove() {
    setBusy(true);
    try {
      onDone(
        await ok(
          api.DELETE("/v1/studio/admin/models/{model}", {
            params: {
              path: { model: m.id },
              query: {
                host,
                ...("component" in v ? { addon: v.component } : { variant: v.id }),
                ...(many && { folder: picked.map((c) => c.copy.folder) }),
              },
            },
          }),
        ),
      );
    } catch (e) {
      setError(message(e));
    } finally {
      setBusy(false);
    }
  }
  const sum = (cs: typeof copies) => cs.reduce((n, c) => n + (c.copy.freed_bytes ?? 0), 0);
  const rest = copies.filter((c) => !picked.includes(c));
  const after = !many
    ? ""
    : rest.length === 0
      ? `After this, ${title} is not on ${host}: it shows missing there.`
      : !picked.some((c) => c.used)
        ? `After this, ${host} still loads ${title} from ${nameOf(copies.find((c) => c.used))}.`
        : rest.every((c) => c.copy.state === "unplugged")
          ? `After this, ${title} is only on ${nameOf(rest[0])}: it shows ${nameOf(rest[0])} not connected until ${nameOf(rest[0])} is back.`
          : `After this, ${host} loads ${title} from ${nameOf(rest.find((c) => c.copy.state !== "unplugged"))}, the other copy.`;
  return (
    <>
      {many ? (
        <div className="mb-4 flex flex-col gap-3">
          <div className={ROW}>
            <span>On host</span>
            <span className={VAL}>{host}</span>
          </div>
          <div className="flex flex-col gap-1.5">
            <h3 className="heading">Copy</h3>
            <div role="radiogroup" aria-label="Copy" className="flex flex-col gap-1.5">
              {copies.map((c) => {
                const gone = c.copy.state === "unplugged";
                return (
                  <Pick
                    key={c.copy.folder}
                    name="delete-copy"
                    label={nameOf(c)}
                    path={c.folder?.root}
                    checked={pick === c.copy.folder}
                    off={gone}
                    onPick={() => setPick(c.copy.folder)}
                    tag={
                      c.used && (
                        <span className="tag" title="Loaded from here: first in the list">
                          used
                        </span>
                      )
                    }
                    end={
                      gone
                        ? "Not connected"
                        : `${bytesText(c.copy.freed_bytes)} back · ${diskText((c.folder?.free_bytes ?? 0) + (c.copy.freed_bytes ?? 0))} free`
                    }
                  />
                );
              })}
              <Pick
                name="delete-copy"
                label={copies.length === 2 ? "Both" : "All"}
                checked={both}
                off={!!away}
                onPick={() => setPick("*")}
                end={away ? `${nameOf(away)} not connected` : `${bytesText(sum(live))} back`}
              />
            </div>
          </div>
          <p className="m-0 text-[12px] text-t2">{after}</p>
          <span className="text-[11px] text-t2">
            Space back counts only the files that go: shared parts another variant loads stay.
          </span>
        </div>
      ) : (
        <p className="mb-4 text-[13px] text-t2">
          {"component" in v
            ? `The files of this add-on under ${folder || "its folder"} on ${host} are removed. Every variant keeps working without it. A download can fetch it again.`
            : `The files of this variant under ${folder || "its folder"} on ${host} are removed. Shared components another variant on the host loads are kept. A download can fetch it again.`}
        </p>
      )}
      {error && <ErrorNote>{error}</ErrorNote>}
      <div className="mt-3 flex justify-end gap-2">
        <button type="button" className="chip bevel" onClick={onClose}>
          Keep it
        </button>
        <button
          type="button"
          className={`chip bevel font-semibold ${many ? "bg-bad text-[#2b0c02]" : "bg-a1 text-on1"}`}
          disabled={busy || (many && !picked.length)}
          onClick={() => void remove()}
        >
          {!many
            ? "Delete"
            : both
              ? copies.length === 2
                ? "Delete both"
                : "Delete all"
              : `Delete ${nameOf(picked[0])} copy`}
        </button>
      </div>
    </>
  );
}
