"use client";
// The Timeline screen's dialogs (docs/mock/TimelineDialogs.dc.html): Sequences (switch, rename, delete), New
// sequence, Re-render (a preset over the selected clips) and Export (the plan first, then the job's progress, cancel
// and the exported take in the media viewer).
import Link from "next/link";
import { useEffect, useState } from "react";
import { ErrorNote, Modal } from "@/components/kit";
import { FullScreen } from "@/components/media";
import { api, message, ok, type S } from "@/lib/api";
import { mb } from "@/lib/format";
import { presetNames } from "@/lib/studio";
import { RATES, shortTime, timecode } from "@/lib/timeline";

type Timeline = S<"Timeline">;
type Summary = S<"TimelineSummary">;
type Job = S<"MediaJob">;

const field = "in h-[38px]";

/** The sequence list (the name's ⌄): open one, + New sequence, rename or delete the current one. */
export function Sequences({
  open,
  onClose,
  current,
  onOpen,
  onNew,
  onRenamed,
  onDeleted,
}: {
  open: boolean;
  onClose: () => void;
  current: Timeline | null;
  onOpen: (id: string) => void;
  onNew: () => void;
  onRenamed: (t: Timeline) => void;
  onDeleted: () => void;
}) {
  return (
    <Modal open={open} onClose={onClose} title="Sequences" width="w-[min(92vw,440px)]">
      <SequenceList
        current={current}
        onOpen={onOpen}
        onNew={onNew}
        onRenamed={onRenamed}
        onDeleted={onDeleted}
        onClose={onClose}
      />
    </Modal>
  );
}

function SequenceList({
  current,
  onOpen,
  onNew,
  onRenamed,
  onDeleted,
  onClose,
}: {
  current: Timeline | null;
  onOpen: (id: string) => void;
  onNew: () => void;
  onRenamed: (t: Timeline) => void;
  onDeleted: () => void;
  onClose: () => void;
}) {
  const [list, setList] = useState<Summary[] | null>(null);
  const [error, setError] = useState("");
  const [name, setName] = useState<string | null>(null);
  const [confirm, setConfirm] = useState(false);
  useEffect(() => {
    ok(api.GET("/v1/studio/timelines"))
      .then((r) => setList(r.timelines))
      .catch((e) => setError(message(e)));
  }, []);
  const act = (f: () => Promise<void>) => f().catch((e) => setError(message(e)));
  return (
    <div className="flex flex-col gap-3">
      <button type="button" className="chip bevel justify-center rounded-[10px]" onClick={onNew}>
        + New sequence
      </button>
      {list === null && !error && <p className="m-0 text-t2">Loading…</p>}
      <ul className="m-0 flex max-h-[50dvh] list-none flex-col gap-1 overflow-y-auto p-0">
        {list?.map((t) => (
          <li key={t.id}>
            <button
              type="button"
              aria-current={t.id === current?.id}
              onClick={() => {
                onOpen(t.id);
                onClose();
              }}
              className="h-9 w-full rounded-[10px] px-3 text-left text-[13px] aria-current:bg-s4 aria-current:font-semibold"
            >
              {t.name}{" "}
              <span className="mono text-[12px] font-normal text-t2">
                · {t.clips === 1 ? "1 clip" : `${t.clips} clips`} · {shortTime(t.duration_s)}
              </span>
            </button>
          </li>
        ))}
      </ul>
      {current && name !== null && (
        <form
          className="flex gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            void act(async () => {
              const t = await ok(
                api.PATCH("/v1/studio/timelines/{timeline_id}", {
                  params: { path: { timeline_id: current.id } },
                  body: { name },
                }),
              );
              onRenamed(t);
              setList((l) => l && l.map((x) => (x.id === t.id ? { ...x, name: t.name } : x)));
              setName(null);
            });
          }}
        >
          <label htmlFor="tl-name" className="sr-only">
            Name
          </label>
          <input
            id="tl-name"
            className={field}
            value={name}
            maxLength={120}
            autoFocus
            onChange={(e) => setName(e.target.value)}
          />
          <button type="submit" className="chip bevel rounded-[10px]" disabled={!name.trim()}>
            Save
          </button>
        </form>
      )}
      {current && name === null && !confirm && (
        <div className="flex gap-2">
          <button
            type="button"
            className="chip bevel rounded-[10px]"
            onClick={() => setName(current.name)}
          >
            Rename
          </button>
          <button
            type="button"
            className="chip bevel rounded-[10px] text-bad"
            onClick={() => setConfirm(true)}
          >
            Delete
          </button>
        </div>
      )}
      {current && confirm && (
        <div role="alert" className="flex flex-col gap-2 rounded-xl bg-s3 p-3 text-[13px]">
          Delete {current.name}? Its clips stay in the gallery; an unfinished export is cancelled.
          <div className="flex gap-2">
            <button
              type="button"
              className="chip bevel rounded-[10px]"
              onClick={() => setConfirm(false)}
            >
              Keep it
            </button>
            <button
              type="button"
              className="chip bevel rounded-[10px] text-bad"
              onClick={() =>
                void act(async () => {
                  await ok(
                    api.DELETE("/v1/studio/timelines/{timeline_id}", {
                      params: { path: { timeline_id: current.id } },
                    }),
                  );
                  onDeleted();
                  onClose();
                })
              }
            >
              Delete
            </button>
          </div>
        </div>
      )}
      {error && <ErrorNote>{error}</ErrorNote>}
    </div>
  );
}

/** New sequence: name, size and frame rate (or from the first clip); sound is 48 kHz. */
export function NewSequence({
  open,
  onClose,
  onMade,
}: {
  open: boolean;
  onClose: () => void;
  onMade: (t: Timeline) => void;
}) {
  return (
    <Modal open={open} onClose={onClose} title="New sequence" width="w-[min(92vw,440px)]">
      <NewForm onClose={onClose} onMade={onMade} />
    </Modal>
  );
}

function NewForm({ onClose, onMade }: { onClose: () => void; onMade: (t: Timeline) => void }) {
  const [name, setName] = useState("");
  const [w, setW] = useState("1280");
  const [h, setH] = useState("720");
  const [fps, setFps] = useState("24");
  const [auto, setAuto] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const dim = (v: string) => Number.isInteger(Number(v)) && Number(v) >= 16 && Number(v) <= 4096;
  return (
    <form
      className="flex flex-col gap-3"
      onSubmit={(e) => {
        e.preventDefault();
        setBusy(true);
        ok(
          api.POST("/v1/studio/timelines", {
            body: {
              name,
              ...(!auto && {
                target: {
                  width: Number(w),
                  height: Number(h),
                  fps: Number(fps) as S<"TimelineTarget">["fps"],
                  sample_rate: 48000,
                },
              }),
            },
          }),
        )
          .then(onMade)
          .catch((e) => setError(message(e)))
          .finally(() => setBusy(false));
      }}
    >
      <label htmlFor="ns-name" className="text-[13px]">
        Name
      </label>
      <input
        id="ns-name"
        className={field}
        value={name}
        maxLength={120}
        placeholder="Untitled sequence"
        onChange={(e) => setName(e.target.value)}
      />
      <div className="grid grid-cols-3 gap-2">
        <div>
          <label htmlFor="ns-w" className="label">
            Width
          </label>
          <input
            id="ns-w"
            className={field}
            inputMode="numeric"
            value={w}
            disabled={auto}
            onChange={(e) => setW(e.target.value)}
          />
        </div>
        <div>
          <label htmlFor="ns-h" className="label">
            Height
          </label>
          <input
            id="ns-h"
            className={field}
            inputMode="numeric"
            value={h}
            disabled={auto}
            onChange={(e) => setH(e.target.value)}
          />
        </div>
        <div>
          <label htmlFor="ns-fps" className="label">
            Frame rate
          </label>
          <select
            id="ns-fps"
            className={field}
            value={fps}
            disabled={auto}
            onChange={(e) => setFps(e.target.value)}
          >
            {RATES.map(([r]) => (
              <option key={r} value={r}>
                {r}
              </option>
            ))}
          </select>
        </div>
      </div>
      <label className="flex items-center gap-2.5 text-[13px]">
        <input
          type="checkbox"
          checked={auto}
          onChange={(e) => setAuto(e.target.checked)}
          className="accent-a1"
        />
        Size and frame rate from the first clip
      </label>
      <p className="m-0 text-[12px] text-t2">
        With no clip yet: 1280 × 720 · 24 fps. Sound is 48 kHz.
      </p>
      {error && <ErrorNote>{error}</ErrorNote>}
      <div className="flex justify-end gap-2">
        <button type="button" className="chip bevel rounded-[10px]" onClick={onClose}>
          Cancel
        </button>
        <button
          type="submit"
          className="h-10 rounded-[10px] bg-a1 px-[18px] text-[13px] font-semibold text-on1"
          disabled={busy || (!auto && !(dim(w) && dim(h)))}
        >
          Create
        </button>
      </div>
    </form>
  );
}

/** Re-render the selected clips (or the sequence) with one preset all their workflows have, or their own settings. */
export function Rerender({
  open,
  onClose,
  tl,
  clips,
  workflows,
  onDone,
}: {
  open: boolean;
  onClose: () => void;
  tl: Timeline;
  /** V1 clip ids; empty = every clip with a take */
  clips: string[];
  workflows: S<"Workflow">[];
  onDone: (r: S<"TimelineRerenderAnswer">) => void;
}) {
  const v1 = tl.tracks[0].clips.filter((c) => c.take_id && !c.offline);
  const chosen = clips.length ? v1.filter((c) => clips.includes(c.id)) : v1;
  const wf = chosen.map((c) => workflows.find((w) => w.id === tl.takes[c.take_id!]?.workflow_id));
  const presets = wf.length
    ? presetNames(wf[0]?.studio).filter((p) =>
        wf.every((w) => w?.studio?.presets && p in w.studio.presets),
      )
    : [];
  const [preset, setPreset] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const n = chosen.length;
  return (
    <Modal
      open={open}
      onClose={onClose}
      title={
        clips.length ? `Re-render ${n === 1 ? "1 clip" : `${n} clips`}` : "Re-render the sequence"
      }
      width="w-[min(92vw,460px)]"
    >
      <div className="flex flex-col gap-3">
        <p className="m-0 text-[12px] text-t2">
          One pipeline, a step per clip with its own template, seed and inputs; a clip that starts
          from another&apos;s last frame waits for it. Runs as a batch; each result becomes the
          slot&apos;s current take.
        </p>
        <span className="text-[13px]" id="rr-preset">
          Preset
        </span>
        <div role="group" aria-labelledby="rr-preset" className="flex flex-wrap gap-2">
          {[null, ...presets].map((p) => (
            <button
              key={p ?? ""}
              type="button"
              aria-pressed={preset === p}
              onClick={() => setPreset(p)}
              className="chip bevel rounded-[10px] aria-pressed:outline-2 aria-pressed:outline-a1"
            >
              {p ?? "Each clip's own settings"}
            </button>
          ))}
        </div>
        {!n && <p className="m-0 text-[13px] text-t2">No clip here was made by a workflow.</p>}
        {error && <ErrorNote>{error}</ErrorNote>}
        <div className="flex justify-end gap-2">
          <button type="button" className="chip bevel rounded-[10px]" onClick={onClose}>
            Cancel
          </button>
          <button
            type="button"
            className="h-10 rounded-[10px] bg-a1 px-[18px] text-[13px] font-semibold text-on1"
            disabled={busy || !n}
            onClick={() => {
              setBusy(true);
              setError("");
              ok(
                api.POST("/v1/studio/timelines/{timeline_id}/rerender", {
                  params: { path: { timeline_id: tl.id } },
                  body: {
                    revision: tl.revision,
                    ...(clips.length && { clips: chosen.map((c) => c.id) }),
                    ...(preset && { preset }),
                  },
                }),
              )
                .then(onDone)
                .catch((e) => setError(message(e)))
                .finally(() => setBusy(false));
            }}
          >
            Re-render
          </button>
        </div>
      </div>
    </Modal>
  );
}

const ended = (j: Job) => j.status !== "queued" && j.status !== "running";
const size = (b: number) => `${mb(b)} MB`;

/** The Export dialog: the plan (stream copy, or why it re-encodes; what blocks it), then the job. */
export function Export({
  open,
  onClose,
  tl,
}: {
  open: boolean;
  onClose: () => void;
  tl: Timeline;
}) {
  return (
    <Modal open={open} onClose={onClose} title={`Export ${tl.name}`} width="w-[min(92vw,480px)]">
      <ExportBody tl={tl} onClose={onClose} />
    </Modal>
  );
}

function ExportBody({ tl, onClose }: { tl: Timeline; onClose: () => void }) {
  const [plan, setPlan] = useState<S<"ExportPlan"> | null>(null);
  const [job, setJob] = useState<Job | null>(null);
  const [take, setTake] = useState<S<"Take"> | null>(null);
  const [full, setFull] = useState(false);
  const [error, setError] = useState("");
  const id = tl.id;

  useEffect(() => {
    const p = { params: { path: { timeline_id: id } } };
    Promise.all([
      ok(api.POST("/v1/studio/timelines/{timeline_id}/export/plan", { ...p, body: {} })),
      ok(api.GET("/v1/studio/timelines/{timeline_id}/exports", p)),
    ])
      .then(([pl, list]) => {
        setPlan(pl);
        const going = list.jobs.find((j) => !ended(j));
        if (going) setJob(going);
      })
      .catch((e) => setError(message(e)));
  }, [id]);

  // follow the job until it ends; then the take it made
  const jobId = job?.id;
  const done = job ? ended(job) : false;
  useEffect(() => {
    if (!jobId || done) return;
    const t = setInterval(() => {
      ok(api.GET("/v1/studio/media-jobs/{job_id}", { params: { path: { job_id: jobId } } }))
        .then(setJob)
        .catch((e) => setError(message(e)));
    }, 1000);
    return () => clearInterval(t);
  }, [jobId, done]);
  const takeId = job?.status === "succeeded" ? job.take_id : null;
  useEffect(() => {
    if (!takeId) return;
    ok(api.GET("/v1/studio/takes/{take_id}", { params: { path: { take_id: takeId } } }))
      .then(setTake)
      .catch((e) => setError(message(e)));
  }, [takeId]);

  if (job) {
    const pr = job.progress;
    return (
      <div className="flex flex-col gap-3" aria-live="polite">
        {!done && (
          <>
            <span className="flex items-center gap-2">
              <span className="block h-1.5 flex-1 rounded-[3px] bg-s5">
                <span
                  className="block h-1.5 rounded-[3px] bg-a1"
                  style={{ width: `${pr ? (100 * pr.frames) / Math.max(1, pr.total) : 0}%` }}
                />
              </span>
              <small className="mono text-[12px] text-t2">
                {pr ? `frame ${pr.frames} / ${pr.total}` : job.status}
              </small>
            </span>
            <p className="m-0 text-[12px] text-t2">
              You can leave this screen; the export keeps going and lands in the gallery.
            </p>
            <div className="flex justify-end gap-2">
              <button
                type="button"
                className="chip bevel rounded-[10px]"
                onClick={() =>
                  ok(
                    api.POST("/v1/studio/media-jobs/{job_id}/cancel", {
                      params: { path: { job_id: job.id } },
                    }),
                  )
                    .then(setJob)
                    .catch((e) => setError(message(e)))
                }
              >
                Cancel export
              </button>
              <button type="button" className="chip bevel rounded-[10px]" onClick={onClose}>
                Close
              </button>
            </div>
          </>
        )}
        {job.status === "succeeded" && (
          <>
            <p className="m-0 text-[13px]">
              Exported · {job.mode === "copy" ? "stream copy" : "re-encode"}
              {take ? ` · ${size(take.bytes)}` : ""}
            </p>
            <div className="flex justify-end gap-2">
              <button type="button" className="chip bevel rounded-[10px]" onClick={onClose}>
                Close
              </button>
              <button
                type="button"
                disabled={!take}
                onClick={() => setFull(true)}
                className="h-10 rounded-[10px] bg-a1 px-[18px] text-[13px] font-semibold text-on1"
              >
                Open the take
              </button>
            </div>
          </>
        )}
        {(job.status === "failed" || job.status === "cancelled") && (
          <>
            <p className="m-0 text-[13px] text-bad">
              {job.status === "cancelled"
                ? "Export cancelled."
                : `Export failed: ${job.failure?.detail ?? "the runner gave no reason"}`}
            </p>
            <div className="flex justify-end gap-2">
              <button
                type="button"
                className="chip bevel rounded-[10px]"
                onClick={() => setJob(null)}
              >
                Back to the plan
              </button>
            </div>
          </>
        )}
        {error && <ErrorNote>{error}</ErrorNote>}
        {full && take && (
          <FullScreen take={take} onClose={() => setFull(false)} onStep={() => {}} />
        )}
      </div>
    );
  }

  if (!plan)
    return error ? <ErrorNote>{error}</ErrorNote> : <p className="m-0 text-t2">Planning…</p>;
  const o = plan.output;
  const host = plan.host
    ? `${plan.host} · ${plan.hardware ? "VideoToolbox" : "software (libx264)"}`
    : "no host can run it now";
  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center gap-2 rounded-xl bg-s3 px-3 py-2.5 text-[13px]">
        <b>{plan.stream_copy ? "Stream copy" : "Re-encode"}</b>
        <span className="mono text-[12px] text-t2">
          H.264
          {o.video_bitrate ? ` · ${(o.video_bitrate / 1e6).toFixed(1)} Mb/s` : " · copied"} · AAC
          192 kb/s
        </span>
      </div>
      {plan.reasons.length > 0 && (
        <ul className="m-0 flex flex-col gap-1 pl-[18px] text-[13px]">
          {plan.reasons.map((r, i) => (
            <li key={i}>{r.message}</li>
          ))}
        </ul>
      )}
      {plan.blocked.length > 0 && (
        <ul role="alert" className="m-0 flex flex-col gap-1 pl-[18px] text-[13px] text-bad">
          {plan.blocked.map((r, i) => (
            <li key={i}>{r.message}</li>
          ))}
        </ul>
      )}
      <p className="m-0 text-[12px] text-t2">Sound is always re-encoded, so cuts stay in sync.</p>
      <div className="flex justify-between gap-2.5 text-[13px]">
        <span>Length · size</span>
        <span className="mono text-[12px] text-t2">
          {timecode(plan.duration_s)} · about {size(plan.estimated_bytes)}
        </span>
      </div>
      <div className="flex justify-between gap-2.5 text-[13px]">
        <span>Runs on</span>
        <span className="mono text-[12px] text-t2">{host}</span>
      </div>
      {error && <ErrorNote>{error}</ErrorNote>}
      <div className="flex justify-end gap-2">
        <button type="button" className="chip bevel rounded-[10px]" onClick={onClose}>
          Cancel
        </button>
        <button
          type="button"
          disabled={plan.blocked.length > 0 || plan.revision !== tl.revision}
          className="h-10 rounded-[10px] bg-a1 px-[18px] text-[13px] font-semibold text-on1 disabled:opacity-55"
          onClick={() => {
            setError("");
            ok(
              api.POST("/v1/studio/timelines/{timeline_id}/export", {
                params: { path: { timeline_id: id } },
                body: { revision: plan.revision },
              }),
            )
              .then(setJob)
              .catch((e) => setError(message(e)));
          }}
        >
          Export
        </button>
      </div>
    </div>
  );
}

/** The Export key's second line: "stream copy", "re-encode · 1 still, 3 trimmed", or what blocks it. */
export function planLine(p: S<"ExportPlan"> | null): string {
  if (!p) return "planning…";
  if (p.blocked.length)
    return p.blocked.some((b) => b.code === "offline") ? "blocked · offline clip" : "blocked";
  if (p.stream_copy) return "stream copy";
  const n = (code: string) => p.reasons.filter((r) => r.code === code).length;
  const words: [number, string, string][] = [
    [n("image"), "still", "stills"],
    [n("transition"), "dissolve", "dissolves"],
    [n("gap"), "gap", "gaps"],
    [n("trimmed"), "trimmed", "trimmed"],
  ];
  const other = p.reasons.length - words.reduce((s, [k]) => s + k, 0);
  words.push([other, "other", "other"]);
  const parts = words.filter(([k]) => k).map(([k, one, many]) => `${k} ${k === 1 ? one : many}`);
  return `re-encode · ${parts.join(", ")}`;
}

/** A link to a re-render's batch. */
export const BatchLink = ({ id }: { id: string }) => (
  <Link href={`/pipelines/batches/${encodeURIComponent(id)}`} className="underline">
    Open the batch
  </Link>
);
