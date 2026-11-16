"use client";
// Models (docs/mock/Models.dc.html, ModelDownload.dc.html, ModelsLoras.dc.html): every model family and variant with
// its components and sizes, and its presence on each host (present, partial, missing, downloading with progress,
// hash mismatch), for everyone; admins download a variant to a host (licence shown and accepted first, the gated
// note) and delete it from one. The LoRAs tab is the library (components/models/loras.tsx). While anything is
// downloading the list is read every 2 s (GET /v1/studio/models carries each job's bytes), else every 15 s.
import Link from "next/link";
import { useCallback, useEffect, useState } from "react";
import { ErrorNote, Info, Modal } from "@/components/kit";
import { LorasTab } from "@/components/models/loras";
import { useUser } from "@/components/shell";
import { api, message, ok, type S } from "@/lib/api";
import { safeUrl } from "@/lib/format";
import {
  anyDownloading,
  bytesText,
  canDelete,
  downloadPlan,
  folderOf,
  forHost,
  macOnly,
  mediaText,
  percent,
  precisionTags,
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
    document.addEventListener("visibilitychange", tick);
    return () => {
      clearInterval(t);
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
  const models = forHost(list?.models ?? [], host);
  const model = models.find((m) => m.id === sel?.model) ?? models[0];
  const variant = model?.variants.find((v) => v.id === sel?.variant) ?? model?.variants[0];
  const states = new Set(
    models.flatMap((m) => m.variants.flatMap((v) => v.hosts.map((h) => h.state))),
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
        </div>
        {error && <ErrorNote onRetry={reload}>{error}</ErrorNote>}
        {list && !models.length && <p className="text-t2">No host has published any model yet.</p>}
        {models.map((m) => (
          <ModelSection
            key={m.id}
            m={m}
            selected={m.id === model?.id}
            picked={m.id === model?.id ? variant?.id : undefined}
            onPick={(v) => setSel({ model: m.id, variant: v })}
          />
        ))}
      </main>
      {list && model && variant && (
        <VariantPanel
          key={`${model.id}/${variant.id}`}
          list={list}
          m={model}
          v={list.models.find((x) => x.id === model.id)!.variants.find((x) => x.id === variant.id)!}
          admin={admin}
          host={host}
          jobs={jobs}
          onChanged={reload}
        />
      )}
    </>
  );
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
  m,
  selected,
  picked,
  onPick,
}: {
  m: Model;
  selected: boolean;
  picked: string | undefined;
  onPick: (variant: string) => void;
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
        {macOnly(m) ? (
          <span className="flex items-center text-[12px] text-t2">
            Mac only
            <Info>
              Only an MLX runtime exists for this model, so hosts without Apple silicon cannot run
              it.
            </Info>
          </span>
        ) : (
          licences.size > 1 && (
            <span className="flex items-center text-[12px] text-t2">
              Licence per variant
              <Info>
                Each variant carries its own licence. It is shown before an admin downloads it.
              </Info>
            </span>
          )
        )}
      </div>
      <div className="grid grid-cols-1 gap-2.5 sm:grid-cols-2">
        {m.variants.map((v) => (
          <VariantCard
            key={v.id}
            m={m}
            v={v}
            picked={picked === v.id}
            onPick={() => onPick(v.id)}
          />
        ))}
      </div>
    </section>
  );
}

const TAG_Q = "tag bg-[color-mix(in_srgb,var(--a1)_22%,var(--s4))]";
const TAG_SH = "tag bg-[color-mix(in_srgb,var(--a2)_22%,var(--s4))]";

function VariantCard({
  m,
  v,
  picked,
  onPick,
}: {
  m: Model;
  v: Variant;
  picked: boolean;
  onPick: () => void;
}) {
  const tags = precisionTags(v.precisions);
  return (
    <div
      onClick={onPick}
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
            Stored in {tags[0].text}. It can be quantized to {v.precisions.slice(1).join(" or ")}{" "}
            while loading, with no second copy on disk.
          </Info>
        ) : (
          <Info>This variant is stored already quantized, so it loads as it is.</Info>
        )}
      </div>
      <div className="flex flex-wrap gap-1.5">
        {v.hosts.map((h) => (
          <HostChip key={h.host} h={h} label={`${v.title} on ${h.host}`} />
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
  }[state];
  return <span className={`size-2 shrink-0 rounded-full ${cls}`} />;
}

function HostChip({ h, label }: { h: VariantHost; label: string }) {
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
}: {
  list: List;
  m: Model;
  v: Variant;
  admin: boolean;
  host: string;
  jobs: S<"ModelJob">[];
  onChanged: () => void;
}) {
  const targets = list.hosts.map((h) => h.host);
  const [target, setTarget] = useState(
    host || v.hosts.find((h) => h.state !== "present")?.host || targets[0] || "",
  );
  const [dialog, setDialog] = useState<"download" | "delete" | null>(null);
  const [notice, setNotice] = useState("");
  const [error, setError] = useState("");
  const shown = host || target;
  const on = v.hosts.find((h) => h.host === shown);
  const onTarget = v.hosts.find((h) => h.host === target);
  const last = jobs.find(
    (j) => j.host === target && j.model === m.id && (j.variant ?? v.id) === v.id,
  );
  const plan = downloadPlan(list, v, target);

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
                {precisionTags(c.precisions).map((t) =>
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
              {stateText(h)}
            </span>
          </div>
        ))}
        {!v.hosts.length && <p className="m-0 text-[13px] text-t2">No host has an env for it.</p>}
      </div>

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
          {notice && (
            <p role="status" className="m-0 text-[12px] text-t2">
              {notice}
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
      <Modal
        open={dialog === "download"}
        onClose={() => setDialog(null)}
        title={`Download ${m.title} · ${v.title}`}
        width="w-[min(94vw,560px)]"
      >
        <DownloadDialog
          list={list}
          m={m}
          v={v}
          host={target}
          onClose={() => setDialog(null)}
          onDone={(j) => {
            setDialog(null);
            setNotice(`Download queued on ${j.host}.`);
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
          m={m}
          v={v}
          host={target}
          folder={folderOf(list, v, target)}
          onClose={() => setDialog(null)}
          onDone={(j) => {
            setDialog(null);
            setNotice(
              `Delete queued on ${j.host}.` +
                (j.keeps.length
                  ? ` Kept: ${j.keeps.map((k) => `${k.component} (for ${k.for.map((f) => titleOf(m, f)).join(", ")})`).join(", ")}.`
                  : ""),
            );
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
  v: Variant;
  host: string;
  onClose: () => void;
  onDone: (j: S<"ModelJob">) => void;
}) {
  const plan = downloadPlan(list, v, host);
  const free = list.hosts.find((h) => h.host === host)?.free_bytes;
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
            body: { host, variant: v.id, ...(plan.mustAccept && { accept_licence: accepted }) },
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
      <div className={ROW}>
        <span>Into</span>
        <span className={VAL}>{folderOf(list, v, host)}</span>
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

/** Delete with confirm; the server's refusal (loaded, in use, downloading) is shown as it words it. */
function DeleteDialog({
  m,
  v,
  host,
  folder,
  onClose,
  onDone,
}: {
  m: Model;
  v: Variant;
  host: string;
  folder: string;
  onClose: () => void;
  onDone: (j: S<"ModelJob">) => void;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  async function remove() {
    setBusy(true);
    try {
      onDone(
        await ok(
          api.DELETE("/v1/studio/admin/models/{model}", {
            params: { path: { model: m.id }, query: { host, variant: v.id } },
          }),
        ),
      );
    } catch (e) {
      setError(message(e));
    } finally {
      setBusy(false);
    }
  }
  return (
    <>
      <p className="mb-4 text-[13px] text-t2">
        The files of this variant under {folder || "its folder"} on {host} are removed. Shared
        components another variant on the host loads are kept. A download can fetch it again.
      </p>
      {error && <ErrorNote>{error}</ErrorNote>}
      <div className="mt-3 flex justify-end gap-2">
        <button type="button" className="chip bevel" onClick={onClose}>
          Keep it
        </button>
        <button
          type="button"
          className="chip bevel bg-a1 font-semibold text-on1"
          disabled={busy}
          onClick={() => void remove()}
        >
          Delete
        </button>
      </div>
    </>
  );
}
