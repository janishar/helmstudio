"use client";
import Link from "next/link";
import { useCallback, useEffect, useState } from "react";
import { useDevViews } from "@/components/app-shell";
import { Button } from "@/components/ui/button";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { ErrorNote, PageHeader, SkeletonRows, StatCard, StatusPill } from "@/components/kit";
import { api, message, ok, type S } from "@/lib/api";
import { jobNodes, type Image, type SessionEvent } from "@/lib/catalog";
import { duration, safeUrl, usd, when } from "@/lib/format";
import { NoDevRole, active } from "../jobs-list";

/** Seconds since `from` until `to` (or now while running). */
const took = (from: string, to: string | null) =>
  duration((to ? Date.parse(to) : Date.now()) - Date.parse(from));

/** One line per event for the timeline: what happened, in a few words. */
function summary(e: SessionEvent): string {
  const d = e.data as Record<string, unknown>;
  switch (e.type) {
    case "step":
      return `${d.step} ${d.state}${d.detail ? ` — ${d.detail}` : ""}`;
    case "log":
      return `${d.key}: ${d.text}`;
    case "status":
      return `${d.work} ${d.status}${d.error ? ` — ${d.error}` : ""}`;
    case "question":
      return String(d.text ?? "");
    case "skus":
      return `${(d.skus as unknown[] | undefined)?.length ?? 0} SKUs (found ${d.found ?? "?"} of ${d.expected ?? "?"})`;
    case "image":
      return d.id
        ? `${d.shot} r${d.revision} ${d.status}${d.error ? ` — ${d.error}` : ""}`
        : "image (gone)";
    case "usage":
      return `${d.kind} ${d.model} · ${d.purpose} · ${duration(d.duration_ms as number)}${
        d.kind === "llm"
          ? ` · ${d.input_tokens} / ${d.output_tokens} tokens · ${usd(d.cost_usd as number | null)}`
          : ` · ${d.credits ?? "–"} cr`
      }`;
    case "run":
      return String(d.line ?? "");
    default:
      return "";
  }
}

/** Route /dev/jobs/<id>: one job's steps (nodes), a run's SKUs and images with every try's review, and the raw event
 *  timeline. Polls every 2.5 s while the job is unfinished. */
export function JobView({ id }: { id: string }) {
  const dev = useDevViews();
  const [job, setJob] = useState<S<"JobDetail"> | null>(null);
  const [codes, setCodes] = useState<Record<string, string>>({});
  const [error, setError] = useState("");
  const load = useCallback(() => {
    ok(api.GET("/v1/catalog/jobs/{job_id}", { params: { path: { job_id: id } } }))
      .then((j) => {
        setJob(j);
        setError("");
        return ok(
          api.GET("/v1/catalog/sessions/{session_id}", {
            params: { path: { session_id: j.job.session_id } },
          }),
        );
      })
      .then((d) => setCodes(Object.fromEntries(d.skus.map((k) => [k.id, k.code]))))
      .catch((e) => setError(message(e)));
  }, [id]);
  useEffect(load, [load]);
  const live = !!job && active(job.job.status);
  useEffect(() => {
    if (!live) return;
    const t = setInterval(load, 2500);
    return () => clearInterval(t);
  }, [live, load]);

  if (!dev) return <NoDevRole />;
  if (error && !job) return <ErrorNote onRetry={load}>Couldn’t load this job: {error}</ErrorNote>;
  if (!job) return <SkeletonRows />;
  const j = job.job;
  const { steps, skus, images } = jobNodes(job.events);
  const u = job.usage;
  const byShot = (k: string) => images.filter((im) => im.sku_id === k);
  return (
    <>
      <PageHeader
        title={j.kind === "run" ? `Run · ${j.scope}` : "Processing"}
        description={
          <>
            <Link href={`/sessions/${j.session_id}`} className="hover:underline">
              {j.project_name}
            </Link>{" "}
            · <span className="font-mono">{j.id}</span>
          </>
        }
        actions={
          <Button variant="outline" asChild>
            <Link href="/dev/jobs">All jobs</Link>
          </Button>
        }
      />
      {error && <ErrorNote onRetry={load}>Couldn’t refresh: {error}</ErrorNote>}
      <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
        <StatCard
          label="Status"
          value={<StatusPill status={j.status} />}
          hint={j.error ?? undefined}
        />
        <StatCard label="Started" value={when(j.created_at)} />
        <StatCard
          label="Took"
          value={took(j.created_at, live ? null : j.updated_at)}
          hint="until its last event"
        />
        <StatCard
          label="Events"
          value={job.events.length}
          hint={job.events.length >= 2000 ? "the first 2000" : undefined}
        />
        <StatCard label="LLM cost" value={usd(u.cost_usd)} hint="known prices (LLM_PRICES)" />
        <StatCard
          label="Image credits"
          value={u.credits ?? "–"}
          hint={`${u.image_calls} image calls · quote units, not charged`}
        />
        <StatCard
          label="LLM calls"
          value={u.llm_calls}
          hint={`${(u.input_tokens + u.output_tokens).toLocaleString()} tokens`}
        />
        <StatCard
          label="Tokens in / out"
          value={`${u.input_tokens.toLocaleString()} / ${u.output_tokens.toLocaleString()}`}
        />
      </div>

      {u.models.length > 0 && (
        <section aria-labelledby="models-h" className="mt-6">
          <h2 id="models-h" className="mb-2 text-sm font-medium">
            Models
          </h2>
          <Table className="tabular">
            <TableHeader>
              <TableRow>
                <TableHead>Model</TableHead>
                <TableHead>Kind</TableHead>
                <TableHead className="text-right">Calls</TableHead>
                <TableHead className="text-right">Tokens in / out</TableHead>
                <TableHead className="text-right">Cache read</TableHead>
                <TableHead className="text-right">Time</TableHead>
                <TableHead className="text-right">Cost</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {u.models.map((m) => (
                <TableRow key={`${m.kind}:${m.model}`}>
                  <TableCell className="font-medium">{m.model}</TableCell>
                  <TableCell>{m.kind}</TableCell>
                  <TableCell className="text-right">{m.calls}</TableCell>
                  <TableCell className="text-right">
                    {m.kind === "llm"
                      ? `${m.input_tokens.toLocaleString()} / ${m.output_tokens.toLocaleString()}`
                      : "–"}
                  </TableCell>
                  <TableCell className="text-right">
                    {m.kind === "llm" ? m.cache_read_tokens.toLocaleString() : "–"}
                  </TableCell>
                  <TableCell className="text-right">{duration(m.duration_ms)}</TableCell>
                  <TableCell className="text-right">
                    {m.kind === "llm"
                      ? usd(m.cost_usd)
                      : m.credits != null
                        ? `${m.credits} cr`
                        : "–"}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </section>
      )}

      <section aria-labelledby="nodes-h" className="mt-6">
        <h2 id="nodes-h" className="mb-2 text-sm font-medium">
          Nodes
        </h2>
        {!steps.length ? (
          <p className="text-sm text-muted-foreground">
            {live ? "Waiting for the first step…" : "No steps reported."}
          </p>
        ) : (
          <ol className="grid gap-2">
            {steps.map((s) => (
              <li
                key={s.step}
                className="flex flex-wrap items-baseline gap-x-3 rounded-lg border bg-card px-3 py-2 text-sm"
              >
                <span className="font-medium">{s.step}</span>
                <StatusPill status={s.state} />
                <span className="min-w-0 flex-1 truncate text-muted-foreground">{s.detail}</span>
                <span className="tabular text-xs text-muted-foreground">
                  {took(s.started, s.ended)}
                </span>
              </li>
            ))}
          </ol>
        )}
      </section>

      {j.kind === "run" && (
        <section aria-labelledby="skus-h" className="mt-6">
          <h2 id="skus-h" className="mb-2 text-sm font-medium">
            render_sku · {skus.length} SKU{skus.length === 1 ? "" : "s"}
          </h2>
          <div className="grid gap-3">
            {skus.map((k) => (
              <div key={k.sku_id} className="rounded-xl border bg-card p-3">
                <div className="flex flex-wrap items-baseline gap-x-3 text-sm">
                  <span className="font-medium">{codes[k.sku_id] ?? k.sku_id.slice(0, 8)}</span>
                  <StatusPill status={k.sku_status === "Running" ? "running" : "succeeded"}>
                    {k.sku_status}
                  </StatusPill>
                  <span className="text-muted-foreground">
                    {k.images_done} of {k.images_total} images · {k.line}
                  </span>
                </div>
                <ul className="mt-2 grid gap-2">
                  {byShot(k.sku_id).map((im) => (
                    <ImageRow key={im.id} im={im} />
                  ))}
                </ul>
              </div>
            ))}
          </div>
        </section>
      )}

      <section aria-labelledby="events-h" className="mt-6">
        <h2 id="events-h" className="mb-2 text-sm font-medium">
          Events
        </h2>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="w-14">Seq</TableHead>
              <TableHead className="w-24">Time</TableHead>
              <TableHead className="w-20">Type</TableHead>
              <TableHead>What</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {job.events.map((e) => (
              <TableRow key={e.seq} className="align-top">
                <TableCell className="tabular text-muted-foreground">{e.seq}</TableCell>
                <TableCell className="tabular whitespace-nowrap text-muted-foreground">
                  {new Date(e.ts).toLocaleTimeString()}
                </TableCell>
                <TableCell>{e.type}</TableCell>
                <TableCell className="max-w-0 whitespace-normal">
                  <details>
                    <summary className="cursor-pointer break-words">{summary(e)}</summary>
                    <pre className="mt-1 max-h-80 overflow-auto rounded-lg bg-muted p-2 font-mono text-xs">
                      {JSON.stringify(e.data, null, 2)}
                    </pre>
                  </details>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </section>
    </>
  );
}

/** One image of a run: its shot, status and each try with the reviewer's verdict and failed goals. */
function ImageRow({ im }: { im: Image }) {
  return (
    <li className="rounded-lg bg-muted/40 p-2 text-sm">
      <div className="flex flex-wrap items-baseline gap-x-3">
        <span className="font-medium">{im.shot}</span>
        <span className="text-xs text-muted-foreground">revision {im.revision}</span>
        <StatusPill
          status={
            im.status === "to_review" || im.status === "approved"
              ? "succeeded"
              : im.status === "generating"
                ? "running"
                : im.status
          }
        >
          {im.status}
        </StatusPill>
        {im.error && <span className="text-xs text-destructive">{im.error}</span>}
      </div>
      {im.attempts.length > 0 && (
        <ol className="mt-2 flex flex-wrap gap-3">
          {im.attempts.map((a) => (
            <li key={a.index} className="flex w-56 gap-2">
              <a href={safeUrl(a.url)} target="_blank" rel="noopener" className="shrink-0">
                <img
                  src={safeUrl(a.url)}
                  alt={`${im.shot}, try ${a.index}`}
                  className="size-16 rounded object-cover"
                />
              </a>
              <div className="min-w-0 text-xs">
                <p className="font-medium">
                  Try {a.index}
                  {a.best && " · best"} ·{" "}
                  {a.review ? (a.review.passed ? "passed" : "failed") : "not reviewed"}
                </p>
                {a.review?.goals
                  ?.filter((g) => !g.passed)
                  .map((g) => (
                    <p key={g.goal} className="text-muted-foreground" title={g.note}>
                      {g.goal}: {g.note}
                    </p>
                  ))}
              </div>
            </li>
          ))}
        </ol>
      )}
    </li>
  );
}
