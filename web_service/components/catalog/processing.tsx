"use client";
// Catalog Agent, Processing: the next-step chat with the output panel on its right, the SKU list and the Gallery.
// Ported from the demo's GenerationV2.jsx (and the parts of CreatePanel.jsx it used); runs, reviews, feedback, pose
// changes and downloads go to the server through `ops`, and images are the real ones (signed /files links).
import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { ChevronDown } from "lucide-react";
import { safeUrl } from "@/lib/format";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import {
  catName,
  catOf,
  choiceLabel,
  choices,
  crRange,
  crWorst,
  exampleCat,
  activeRuns,
  isBusy,
  liveImages,
  plural,
  poseName,
  nounCount,
  poseNoun,
  quoteImages,
  quoteSkus,
  sampleSku,
  sceneNote,
  revisionsOf,
  shotKey,
  shotsOf,
  statusCounts,
  attemptsFor,
  liveTry,
  pickableTry,
  pickedByBrand,
  subcategoryTip,
  suitsWearers,
  usesModel,
  withNoun,
  type Image,
  type Msg,
  type Sku,
  skuKind,
  subName,
} from "@/lib/catalog";
import type { S } from "@/lib/api";
import { Bubble, DescribeBox, ICON, IconButton, InfoTip, PoseTile, TypingDots, railIcon, useCatalog, usePoseNoun, useStaggered } from "./kit";

/** The Processing panes picked in the left rail. */
export type Pane = "processing" | "skus" | "output";

/** SKU status pill colours. */
const STATUS_STYLE: Record<string, string> = {
  "Not run": "border-surface-border text-gray-400",
  Queued: "border-sky-500/40 text-sky-300",
  Running: "border-accent/50 text-accent-hover",
  "To review": "border-amber-500/40 text-amber-300",
  Approved: "border-emerald-500/40 text-emerald-300",
  Redo: "border-rose-500/40 text-rose-300",
};
/** Update-line icons by kind. */
const UPDATE_ICON: Record<string, string> = { run: ICON.play, done: ICON.check, redo: ICON.refresh, download: ICON.download };

/** The status bar: approved / to review / running / not run across every SKU. */
function StatusLine({ skus }: { skus: Sku[] }) {
  const m = statusCounts(skus);
  const parts = (
    [
      [m.approved, "approved", "text-emerald-300", "bg-emerald-400"],
      [m.review, "to review", "text-amber-300", "bg-amber-400"],
      [m.running, "running", "text-accent-hover", "bg-accent"],
      [m.notRun, "not run yet", "text-gray-400", "bg-gray-500"],
    ] as const
  ).filter(([n]) => n > 0);
  return (
    <div className="rounded-xl border border-surface-border px-3 py-2">
      <div className="flex items-center justify-between text-[11px]">
        <span className="font-semibold tracking-wider text-gray-500 uppercase">Status</span>
        <span className="text-gray-500">
          {m.approved}/{m.total} SKUs done
        </span>
      </div>
      <div className="mt-1.5 flex h-1.5 overflow-hidden rounded-full bg-white/5">
        {parts.map(([n, , , bar]) => (
          <span key={bar} className={bar} style={{ width: `${(n / Math.max(1, m.total)) * 100}%` }} />
        ))}
      </div>
      <div className="mt-1.5 flex flex-wrap gap-x-3 gap-y-0.5 text-xs">
        {parts.map(([n, label, cls]) => (
          <span key={label} className={cls}>
            {n} {label}
          </span>
        ))}
      </div>
    </div>
  );
}

/** The agent's setup steps when Processing first opens: "Thinking", then each step spins and ticks in turn; calls
 * onDone once the last one ticks. Presentation only: Start processing already did the work. */
function ThinkingSteps({ m, onDone }: { m: Msg; onDone: (id: string) => void }) {
  const steps = m.steps ?? [];
  const [at, setAt] = useState(m.done ? steps.length + 1 : 0); // 0 = thinking, i = step i running, n+1 = all done
  const finished = at > steps.length;
  useEffect(() => {
    if (finished) {
      if (!m.done) onDone(m.id);
      return undefined;
    }
    const t = setTimeout(() => setAt((x) => x + 1), at === 0 ? 900 : 850);
    return () => clearTimeout(t);
  }, [at, finished, m.done, m.id, onDone]);
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!m.done) ref.current?.scrollIntoView({ block: "nearest" });
  }, [at, m.done]);
  return (
    <div ref={ref} className="rounded-xl border border-surface-border bg-white/[0.02] p-3">
      <div className="flex items-center gap-2 text-[11px] font-semibold tracking-wider text-gray-400 uppercase">
        {finished ? (
          <span className="text-emerald-400">{railIcon(ICON.check, "h-3.5 w-3.5")}</span>
        ) : (
          <span className="h-3 w-3 animate-spin rounded-full border-2 border-accent border-t-transparent" />
        )}
        {finished ? "Ready to process" : at === 0 ? "Thinking" : "Working on it"}
      </div>
      {at > 0 && (
        <ul className="mt-2 space-y-1.5">
          {steps.slice(0, at).map((text, i) => {
            const done = i + 1 < at;
            return (
              <li key={text} className="flex animate-msg-in items-start gap-2 text-[13px]">
                <span className="mt-0.5 flex h-4 w-4 shrink-0 items-center justify-center">
                  {done ? (
                    <span className="text-emerald-400">{railIcon(ICON.check, "h-3.5 w-3.5")}</span>
                  ) : (
                    <span className="h-3 w-3 animate-spin rounded-full border-2 border-accent border-t-transparent" />
                  )}
                </span>
                <span className={done ? "text-gray-300" : "text-gray-100"}>{text}</span>
              </li>
            );
          })}
        </ul>
      )}
    </div>
  );
}

/** A guide in the chat: how things work, with example requests to tap. */
function GuideCard({ m, onTry }: { m: Msg; onTry: (t: string) => void }) {
  return (
    <div className="rounded-xl border border-surface-border bg-white/[0.02] p-3">
      <div className="flex items-center gap-1.5 text-[11px] font-semibold tracking-wider text-gray-400 uppercase">
        <span className="text-sky-400">{railIcon(ICON.info, "h-3.5 w-3.5")}</span>
        {m.title}
      </div>
      <p className="mt-1.5 text-sm leading-relaxed text-gray-300">{m.text}</p>
      {!!m.examples?.length && (
        <div className="mt-2 flex flex-wrap gap-1.5">
          {m.examples.map((ex) => (
            <button
              key={ex}
              type="button"
              onClick={() => onTry(ex)}
              className="rounded-full border border-surface-border px-2.5 py-1 text-[11px] text-gray-300 transition hover:border-accent/50 hover:text-white"
            >
              “{ex}”
            </button>
          ))}
        </div>
      )}
    </div>
  );
}

/** A typed request that would run or regenerate: its credits, and Go ahead (once). */
function QuoteCard({ m, onConfirm }: { m: Msg; onConfirm: () => void }) {
  return (
    <div className="space-y-2">
      <Bubble from="agent">{m.text}</Bubble>
      {!m.done && (
        <button
          type="button"
          onClick={onConfirm}
          className="flex max-w-md items-center gap-3 rounded-xl border border-accent/60 bg-accent/10 px-4 py-2.5 text-left text-sm font-medium text-white hover:border-accent hover:bg-accent/15"
        >
          Go ahead
          <span className="text-[11px] font-normal text-gray-400">{crWorst({ credits: m.credits ?? 0, worst: m.worst ?? m.credits ?? 0 })}</span>
          <span className="text-accent-hover">→</span>
        </button>
      )}
    </div>
  );
}

/** An update in the chat: a short line about what ran or changed. */
function UpdateRow({ m }: { m: Msg }) {
  const done = m.icon === "done";
  return (
    <div className="flex items-start gap-2 pl-1 text-[13px] text-gray-300">
      <span
        className={`mt-0.5 flex h-5 w-5 shrink-0 items-center justify-center rounded-full ${done ? "bg-emerald-500/15 text-emerald-400" : "bg-white/[0.06] text-gray-400"}`}
      >
        {railIcon(UPDATE_ICON[m.icon ?? "run"] ?? ICON.play, "h-3 w-3")}
      </span>
      <span className="min-w-0 flex-1 leading-relaxed">{m.text}</span>
    </div>
  );
}

/** The output panel slides in from the right the first time it shows. */
function SlideIn({ children }: { children: ReactNode }) {
  const [shown, setShown] = useState(false);
  useEffect(() => {
    const id = requestAnimationFrame(() => setShown(true));
    return () => cancelAnimationFrame(id);
  }, []);
  return (
    <div
      className={`flex min-h-0 min-w-0 flex-1 transition-all duration-500 ease-out ${shown ? "translate-x-0 opacity-100" : "translate-x-16 opacity-0"}`}
    >
      {children}
    </div>
  );
}

/** The pose editor posted in the chat (a typed "change the ring poses"): pick the poses for one category. */
function PoseEditor({ m, current, onSave }: { m: Msg; current: string[]; onSave: (m: Msg, poses: string[] | null) => void }) {
  const { lib } = useCatalog();
  const [plan, setPlan] = useState(current);
  if (m.done || !m.category) return null;
  const cat = m.category;
  const name = catName(lib, cat);
  const noun = poseNoun(lib, cat);
  const toggle = (p: string) => setPlan((x) => (x.includes(p) ? x.filter((y) => y !== p) : [...x, p]));
  const all = (lib?.poses ?? []).filter((p) => p.category === cat).map((p) => p.id);
  return (
    <div className="rounded-xl border border-surface-border bg-surface-raised/60 p-3">
      <div className="text-xs font-semibold text-gray-200">{withNoun(`Poses for ${name}`, noun)}</div>
      <p className="mt-0.5 mb-2 text-[11px] text-gray-500">Applies to every {name.replace(/s$/, "").toLowerCase()} not approved yet.</p>
      <div className="grid grid-cols-3 gap-2">
        {all.map((p) => (
          <PoseTile key={p} lib={lib} pose={p} on={plan.includes(p)} onToggle={() => toggle(p)} />
        ))}
      </div>
      <div className="mt-3 flex items-center justify-end gap-2">
        <button type="button" onClick={() => onSave(m, null)} className="text-xs text-gray-500 hover:text-gray-300">
          Cancel
        </button>
        <button
          type="button"
          disabled={!plan.length}
          onClick={() => onSave(m, plan)}
          className="rounded-lg border border-accent/60 bg-accent/10 px-3 py-1.5 text-xs font-medium text-white hover:bg-accent/15 disabled:opacity-40"
        >
          {withNoun(`Use ${plural(plan.length, "pose")}`, noun)}
        </button>
      </div>
    </div>
  );
}

/** The SKU a "Run sample" option will render: its first raw photo and code, so the brand sees what goes first. */
function SampleThumb({ sku }: { sku?: Sku }) {
  if (!sku?.raw_urls[0]) return null;
  return (
    <span className="flex shrink-0 items-center gap-1.5" title={`${sku.code} · ${sku.name}`}>
      <img src={safeUrl(sku.raw_urls[0])} alt="" className="size-8 rounded-md object-cover" />
      <span className="font-mono text-[10.5px] text-gray-400">{sku.code}</span>
    </span>
  );
}

/** The Next step card's last row: run any one SKU of the session, picked from a menu grouped by product (each row its
 * first raw photo, code, name, and status once it has run; the picked one shows its photo too), with its quote. */
function SkuPicker({ onRun }: { onRun: (sku: Sku) => void }) {
  const { lib, detail } = useCatalog();
  const [id, setId] = useState("");
  const sku = detail.skus.find((k) => k.id === id);
  const byProduct = Map.groupBy(detail.skus, (k) => k.category);
  return (
    <div className="flex w-full max-w-lg items-center gap-2 rounded-xl border border-surface-border bg-surface/60 px-3 py-2">
      {/* a menu, not a <select>: options can't hold pictures */}
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <button
            type="button"
            aria-label="Pick a SKU to run"
            className="flex min-w-0 flex-1 items-center gap-2 rounded-lg border border-surface-border bg-surface px-2 py-1 text-left text-sm text-gray-200 hover:border-gray-500 focus:border-accent focus:outline-none"
          >
            {sku ? (
              <>
                <SampleThumb sku={sku} />
                <span className="min-w-0 flex-1 truncate">{sku.name}</span>
              </>
            ) : (
              <span className="flex-1 py-1 text-gray-400">Or run any SKU…</span>
            )}
            <ChevronDown aria-hidden className="size-4 shrink-0 text-gray-500" />
          </button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="start" className="dark max-h-80 w-[var(--radix-dropdown-menu-trigger-width)] overflow-y-auto">
          {[...byProduct].map(([cat, skus]) => (
            <DropdownMenuGroup key={cat}>
              <DropdownMenuLabel className="text-[10.5px] tracking-wider text-muted-foreground uppercase">{catName(lib, cat)}</DropdownMenuLabel>
              {skus.map((k) => (
                <DropdownMenuItem key={k.id} onSelect={() => setId(k.id)} className="gap-2">
                  {k.raw_urls[0] ? (
                    <img src={safeUrl(k.raw_urls[0])} alt="" className="size-8 shrink-0 rounded-md object-cover" />
                  ) : (
                    <span className="size-8 shrink-0 rounded-md bg-white/5" />
                  )}
                  <span className="min-w-0 flex-1">
                    <span className="block truncate text-sm">
                      <span className="font-mono text-[11px] text-gray-400">{k.code}</span> {k.name}
                    </span>
                    {(subName(lib, k) || k.status !== "Not run") && (
                      <span className="block text-[11px] text-gray-500">
                        {[subName(lib, k), k.status !== "Not run" && k.status].filter(Boolean).join(" · ")}
                      </span>
                    )}
                  </span>
                </DropdownMenuItem>
              ))}
            </DropdownMenuGroup>
          ))}
        </DropdownMenuContent>
      </DropdownMenu>
      <button
        type="button"
        disabled={!sku}
        onClick={() => {
          if (!sku) return;
          onRun(sku);
          setId("");
        }}
        className="shrink-0 rounded-lg bg-accent px-3 py-1.5 text-xs font-semibold text-white hover:bg-accent-hover disabled:opacity-40"
      >
        {sku ? `${sku.status === "Not run" ? "Run" : "Run again"} · ${crRange(quoteSkus([sku], lib))}` : "Run"}
      </button>
    </div>
  );
}

/** What Processing hands to the screen: the user's actions. */
export type ProcessingOps = {
  onStep: (o: S<"NextOption">) => void;
  onRunSku: (sku: Sku) => void;
  onStop: () => Promise<void>;
  onSend: (text: string, imageIds: string[]) => Promise<void>;
  onConfirm: (m: Msg) => void;
  onSavePoses: (m: Msg, poses: string[] | null) => void;
  onThought: (id: string) => void;
};

/**
 * Processing's chat column: Input's history (faded), a divider, then the Processing chat; at the bottom one Next step
 * card from the server. The composer takes typed requests, or feedback for the images selected on the right.
 */
function ProcessingChat({
  log,
  history,
  full,
  next,
  picked,
  focus,
  p,
  onRemovePick,
  onClearPicks,
  onApprovePicked,
}: {
  log: Msg[];
  history: ReactNode;
  full: boolean;
  next: S<"NextStep"> | null;
  picked: string[];
  focus: boolean;
  p: ProcessingOps;
  onRemovePick: (id: string) => void;
  onClearPicks: () => void;
  onApprovePicked: () => void;
}) {
  const { lib, detail } = useCatalog();
  const noun = usePoseNoun();
  const scrollRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const [text, setText] = useState("");
  const [sending, setSending] = useState(false);
  const [stopping, setStopping] = useState(false);
  // Messages come in one at a time behind typing dots; the next step comes last. While the agent is still thinking,
  // nothing after it shows yet.
  const thinkingAt = log.findIndex((m) => m.kind === "thinking" && !m.done);
  const list: Msg[] =
    thinkingAt === -1
      ? next
        ? [...log, { id: `next:${next.line}`, kind: "next", text: next.line }]
        : log
      : log.slice(0, thinkingAt + 1);
  const { visible, typing } = useStaggered(list, (m) => m.kind === "user" || m.kind === "poses" || m.kind === "thinking");
  const showStep = visible.at(-1)?.kind === "next" && next;
  useEffect(() => {
    const el = scrollRef.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [visible.length, typing]);
  useEffect(() => {
    if (focus || picked.length) inputRef.current?.focus();
  }, [focus, picked.length]);
  const send = async () => {
    const t = text.trim();
    if (!t || sending) return;
    setSending(true);
    setText("");
    await p.onSend(t, picked);
    setSending(false);
  };
  /** Stop: the runs in progress end; the card follows from the events. */
  const stop = async () => {
    setStopping(true);
    await p.onStop();
    setStopping(false);
  };
  /** A category's poses now: those of its first SKU not approved yet. */
  const posesNow = (cat: string) =>
    (detail.skus.find((s) => s.category === cat && s.status !== "Approved") ?? detail.skus.find((s) => s.category === cat))?.brief.poses ?? [];
  const pad = full ? "px-[max(1rem,calc((100%_-_48rem)/2))]" : "px-4";
  return (
    <aside
      className={`flex min-h-0 flex-col ${full ? "min-w-0 flex-1" : "w-full shrink-0 border-surface-border lg:w-[44%] lg:min-w-[400px] lg:max-w-[620px] lg:border-r"}`}
    >
      <div ref={scrollRef} className={`catalog-scroll flex-1 space-y-3 overflow-y-auto py-4 ${pad}`}>
        {history && (
          <>
            <div className="space-y-3 opacity-50 transition-opacity hover:opacity-80">{history}</div>
            <div className="flex items-center gap-3 py-2 text-[11px] font-medium text-accent-hover">
              <span className="h-px flex-1 bg-accent/30" />
              Processing
              <span className="h-px flex-1 bg-accent/30" />
            </div>
          </>
        )}
        {visible.map((m) => {
          if (m.kind === "next") return null;
          let body: ReactNode;
          if (m.kind === "thinking") body = <ThinkingSteps m={m} onDone={p.onThought} />;
          else if (m.kind === "poses")
            body = <PoseEditor m={m} current={m.category ? posesNow(m.category) : []} onSave={p.onSavePoses} />;
          else if (m.kind === "statusbar") body = <StatusLine skus={detail.skus} />;
          else if (m.kind === "guide") body = <GuideCard m={m} onTry={(t) => void p.onSend(t, [])} />;
          else if (m.kind === "update") body = <UpdateRow m={m} />;
          else if (m.kind === "quote") body = <QuoteCard m={m} onConfirm={() => p.onConfirm(m)} />;
          else if (m.kind === "user")
            body = (
              <div className="flex justify-end">
                <div className="max-w-[85%] rounded-2xl bg-white/[0.06] px-3.5 py-2 text-sm text-gray-200">{m.text}</div>
              </div>
            );
          else body = <Bubble from="agent">{m.text}</Bubble>;
          return (
            <div key={m.id} className="animate-msg-in">
              {body}
            </div>
          );
        })}
        {(typing || sending) && <TypingDots />}
        {showStep && (
          <div key={next.line} className="animate-msg-in space-y-2.5 rounded-xl border border-accent/30 bg-accent/[0.05] p-3">
            <div className="flex items-center gap-1.5 text-[11px] font-semibold tracking-wider text-accent-hover uppercase">
              {railIcon(ICON.spark, "h-3.5 w-3.5")}
              {next.state === "in_progress" ? "In progress" : next.state === "review" ? "Review" : next.heading}
            </div>
            <p className="text-sm leading-relaxed text-gray-200">{next.line}</p>
            {next.state === "in_progress" && activeRuns(detail.runs).length > 0 && (
              <button
                type="button"
                disabled={stopping}
                onClick={() => void stop()}
                className="rounded-lg border border-surface-border bg-surface/60 px-3 py-1.5 text-xs font-semibold text-gray-200 hover:border-gray-500 hover:text-white disabled:opacity-40"
              >
                {stopping ? "Stopping…" : "Stop"}
              </button>
            )}
            {next.options.length > 0 && (
              <div className="flex flex-col gap-2">
                {next.options.map((o, i) => (
                  <button
                    key={o.label}
                    type="button"
                    onClick={() => p.onStep(o)}
                    className={`flex w-full max-w-lg items-center gap-3 rounded-xl border px-4 py-3 text-left text-sm transition ${
                      i === 0
                        ? "border-accent/60 bg-accent/10 font-medium text-white hover:border-accent hover:bg-accent/15"
                        : "border-surface-border bg-surface/60 text-gray-200 hover:border-gray-500 hover:text-white"
                    }`}
                  >
                    {next.options.length > 1 && (
                      <span className={`shrink-0 text-[10.5px] font-semibold tracking-wider uppercase ${i === 0 ? "text-accent-hover" : "text-gray-500"}`}>
                        Option {i + 1}
                      </span>
                    )}
                    {o.action.scope === "sample" && <SampleThumb sku={sampleSku(detail.skus, o.action.category)} />}
                    <span className="min-w-0 flex-1 leading-snug">{o.label}</span>
                    {o.credits != null && <span className="shrink-0 text-[11px] font-normal text-gray-500">{crRange({ credits: o.credits, worst: o.worst_credits ?? o.credits })}</span>}
                    <span className={i === 0 ? "text-accent-hover" : "text-gray-500"}>→</span>
                  </button>
                ))}
              </div>
            )}
            {next.state !== "in_progress" && detail.session.stage === "ready" && detail.skus.length > 0 && <SkuPicker onRun={p.onRunSku} />}
          </div>
        )}
      </div>
      {/* One composer: typed requests, or feedback for the images selected on the right. */}
      <div className={`border-t border-surface-border py-3 ${pad}`}>
        <div className={`rounded-xl border bg-surface ${picked.length ? "border-accent/50" : "border-surface-border focus-within:border-accent/50"}`}>
          {picked.length > 0 && (
            <>
              <div className="flex items-center gap-2 border-b border-surface-border px-3 py-2">
                <span className="text-[11px] text-gray-400">{plural(picked.length, "image")} selected</span>
                <button type="button" onClick={onClearPicks} className="ml-auto text-[11px] text-gray-500 hover:text-white">
                  Clear
                </button>
              </div>
              <div className="flex max-h-28 flex-wrap gap-1.5 overflow-y-auto px-3 py-2">
                {picked.map((id) => {
                  const im = detail.images.find((x) => x.id === id);
                  const sku = detail.skus.find((s) => s.id === im?.sku_id);
                  if (!im || !sku) return null;
                  const label = `${sku.code} · ${poseName(lib, im.shot)}`;
                  return (
                    <span key={id} className="flex items-center gap-1.5 rounded-lg border border-surface-border bg-surface-raised py-0.5 pr-1.5 pl-0.5 text-[11px] text-gray-200">
                      <span className="h-6 w-6 overflow-hidden rounded-md bg-surface-frame">
                        {im.url && <img src={safeUrl(im.url)} alt="" className="h-full w-full object-cover" />}
                      </span>
                      {label}
                      <button type="button" onClick={() => onRemovePick(id)} aria-label={`Remove ${label}`} className="text-gray-500 hover:text-white">
                        ×
                      </button>
                    </span>
                  );
                })}
              </div>
            </>
          )}
          <textarea
            ref={inputRef}
            rows={2}
            value={text}
            onChange={(e) => setText(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter" && !e.shiftKey) {
                e.preventDefault();
                void send();
              }
            }}
            aria-label="Request"
            placeholder={
              picked.length
                ? `What should change in ${picked.length === 1 ? "this image" : `these ${picked.length} images`}? e.g. “the product looks too big”`
                : `Type a request — e.g. “run the rest of the ${exampleCat(lib, detail.skus)}”, “remove the lifestyle ${noun.singular}”`
            }
            className="w-full resize-none bg-transparent px-3 pt-2 text-sm text-gray-100 placeholder:text-gray-500 focus:outline-none"
          />
          <div className="flex items-center justify-end gap-2 px-3 pb-2.5">
            {picked.length > 0 && (
              <button
                type="button"
                onClick={onApprovePicked}
                className="rounded-lg border border-emerald-500/50 px-3 py-1.5 text-xs font-semibold text-emerald-300 hover:bg-emerald-500/10"
              >
                Approve {picked.length}
              </button>
            )}
            <button
              type="button"
              onClick={() => void send()}
              disabled={!text.trim() || sending}
              className="rounded-lg bg-accent px-3 py-1.5 text-xs font-semibold text-white hover:bg-accent-hover disabled:opacity-40"
            >
              {picked.length ? `Send feedback · ${crRange(quoteImages(picked, detail, lib))}` : "Send"}
            </button>
          </div>
        </div>
      </div>
    </aside>
  );
}

/** Image review badges. */
const REVIEW_BADGE: Record<string, { label: string; dot: string; text: string }> = {
  to_review: { label: "Pending review", dot: "bg-amber-400", text: "text-amber-300" },
  approved: { label: "Approved", dot: "bg-emerald-400", text: "text-emerald-300" },
  rejected: { label: "Rejected", dot: "bg-rose-400", text: "text-rose-300" },
  failed: { label: "Failed", dot: "bg-rose-400", text: "text-rose-300" },
};

/** An icon action on an image tile: small enough that all three fit on the narrowest tile and leave it visible; its
 * name shows above it on hover at once (a native title waits a second). Right-aligned so it never runs off the tile. */
function TileAction({
  name,
  icon,
  label,
  onClick,
  cls,
  disabled,
}: {
  name: string;
  icon: string;
  label: string;
  onClick: () => void;
  cls: string;
  disabled?: boolean;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      aria-label={label}
      className={`group/act relative flex h-7 w-7 items-center justify-center rounded-md text-white disabled:opacity-60 ${cls}`}
    >
      {railIcon(icon, "h-3.5 w-3.5")}
      <span className="pointer-events-none absolute right-0 bottom-full mb-1.5 rounded bg-black/85 px-1.5 py-0.5 text-[10px] font-semibold whitespace-nowrap opacity-0 transition group-hover/act:opacity-100 group-focus-visible/act:opacity-100">
        {name}
      </span>
    </button>
  );
}

/** One output image: click to view it big; hover to select, approve or give feedback. */
function ImageTile({
  label,
  im,
  selected,
  onToggle,
  onApprove,
  onFeedback,
  onOpen,
  onCompare,
}: {
  label: string;
  im: Image;
  selected: boolean;
  onToggle: () => void;
  onApprove: () => void;
  onFeedback: () => void;
  onOpen: () => void;
  onCompare?: () => void; // opens the viewer beside a raw photo; absent when there is nothing to compare
}) {
  const b = REVIEW_BADGE[im.status] ?? REVIEW_BADGE.to_review;
  return (
    <div className="min-w-0">
      <div
        className={`group relative aspect-[3/4] overflow-hidden rounded-lg border bg-surface-frame transition hover:border-accent ${selected ? "border-accent ring-2 ring-accent" : "border-surface-border"}`}
      >
        <button type="button" onClick={onOpen} className="absolute inset-0" aria-label={`View ${label}`} title={im.error ?? im.review?.summary ?? undefined}>
          {im.url ? (
            <img src={safeUrl(im.url)} alt="" className="h-full w-full object-cover" />
          ) : (
            <span className="flex h-full items-center justify-center p-3 text-center text-[11px] text-rose-300">{im.error ?? "No image"}</span>
          )}
        </button>
        <span className={`pointer-events-none absolute top-2 left-2 inline-flex items-center gap-1.5 rounded-full bg-black/60 px-2 py-0.5 text-[11px] font-medium ${b.text}`}>
          <span className={`h-1.5 w-1.5 rounded-full ${b.dot}`} />
          {b.label}
        </span>
        <button
          type="button"
          onClick={onToggle}
          aria-pressed={selected}
          aria-label={`${selected ? "Unselect" : "Select"} ${label}`}
          className={`absolute top-2 right-2 z-10 flex h-6 w-6 items-center justify-center rounded-md border-2 shadow-md transition ${
            selected ? "border-accent bg-accent text-white" : "border-white/70 bg-black/50 text-transparent opacity-0 group-hover:opacity-100 focus:opacity-100"
          }`}
        >
          {railIcon(ICON.check)}
        </button>
        <div className="absolute right-2 bottom-2 flex gap-1 rounded-lg bg-black/60 p-1 opacity-0 backdrop-blur transition group-hover:opacity-100 focus-within:opacity-100">
          {im.status === "to_review" && (
            <TileAction name="Approve" icon={ICON.check} label={`Approve ${label}`} onClick={onApprove} cls="bg-emerald-600 hover:bg-emerald-500" />
          )}
          <TileAction name="Feedback" icon={ICON.chat} label={`Give feedback on ${label}`} onClick={onFeedback} cls="hover:bg-white/20" />
          {onCompare && (
            <TileAction name="Compare with raw" icon={ICON.compare} label={`Compare ${label} with its raw photo`} onClick={onCompare} cls="hover:bg-white/20" />
          )}
        </div>
      </div>
    </div>
  );
}

/** A shot still rendering (with a Stop for just that image) or not run yet (with a Run for the SKU's missing shots,
 * e.g. after a Stop; `canRun` false while the SKU has a run going). Once a try has rendered (`live`) the tile shows it,
 * with where it stands, and opens the viewer. */
function PendingTile({
  running,
  label,
  stopping,
  onStop,
  canRun,
  onRun,
  live,
  onOpen,
}: {
  running: boolean;
  label: string;
  stopping: boolean;
  onStop: () => void;
  canRun: boolean;
  onRun: () => void;
  live?: { url: string; text: string } | null;
  onOpen?: () => void;
}) {
  return (
    <div className="relative flex aspect-[3/4] flex-col items-center justify-center gap-2 overflow-hidden rounded-lg border border-accent/30 bg-linear-to-br from-indigo-950 via-slate-900 to-purple-950">
      {live && (
        <button type="button" onClick={onOpen} className="absolute inset-0" aria-label={`View ${label}`} title={live.text}>
          <img src={safeUrl(live.url)} alt="" className="h-full w-full object-cover" />
        </button>
      )}
      {running && live ? (
        <>
          <span className="pointer-events-none absolute inset-x-2 top-2 flex items-start gap-1.5 rounded-lg bg-black/70 px-2 py-1 text-[11px] leading-snug text-white">
            <span className="mt-0.5 h-3 w-3 shrink-0 animate-spin rounded-full border-[1.5px] border-accent/40 border-t-accent" />
            {stopping ? "Stopping…" : live.text}
          </span>
          <div className="absolute right-2 bottom-2 rounded-lg bg-black/60 p-1 backdrop-blur">
            <TileAction name={stopping ? "Stopping…" : "Stop"} icon={ICON.stop} label={`Stop ${label}`} onClick={onStop} disabled={stopping} cls="hover:bg-white/20" />
          </div>
        </>
      ) : running ? (
        <>
          <span className="h-6 w-6 animate-spin rounded-full border-2 border-accent/40 border-t-accent" />
          <span className="animate-pulse text-xs font-semibold text-white">{stopping ? "Stopping…" : "Generating…"}</span>
          <div className="absolute right-2 bottom-2 rounded-lg bg-black/60 p-1 backdrop-blur">
            <TileAction name={stopping ? "Stopping…" : "Stop"} icon={ICON.stop} label={`Stop ${label}`} onClick={onStop} disabled={stopping} cls="hover:bg-white/20" />
          </div>
        </>
      ) : (
        <>
          <span className="text-[11px] text-gray-400">Not run yet</span>
          {canRun && (
            <div className="absolute right-2 bottom-2 rounded-lg bg-black/60 p-1 backdrop-blur">
              <TileAction name="Run missing shots" icon={ICON.play} label={`Run ${label}`} onClick={onRun} cls="hover:bg-white/20" />
            </div>
          )}
        </>
      )}
    </div>
  );
}

/** One picture in the big viewer: an output image (with its thread), or a SKU's raw photo (`photo`, no image). */
type ZoomItem = { im?: Image; sku: Sku; label: string; photo?: string };

/** A small clickable picture in the viewer's strips: a raw photo or one try; `on` marks the one shown. */
function Thumb({ url, label, on, best, tag, onClick }: { url: string; label: string; on: boolean; best?: boolean; tag?: string; onClick: () => void }) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-label={label}
      aria-pressed={on}
      title={label}
      className={`relative h-14 w-11 shrink-0 overflow-hidden rounded-md border-2 bg-surface-frame transition ${on ? "border-accent" : best ? "border-emerald-500/70" : "border-transparent hover:border-white/40"}`}
    >
      <img src={safeUrl(url)} alt="" className="h-full w-full object-cover" />
      {best && <span className="absolute inset-x-0 bottom-0 bg-emerald-600/90 text-center text-[8px] font-semibold text-white uppercase">best</span>}
      {tag && <ViewTag view={tag} />}
    </button>
  );
}

/** How agents' main-image gate starts the error of each shot it skipped (the SKU's main image has not passed review). */
const GATED = "Main image ";

/** Whether a SKU's live shots were skipped by the main-image gate and its main image is still not fixed: no live image
 * passed review or was approved after the newest skipped shot (catalog_service's gatedCodes, so the note and the Next
 * step line clear together). */
function gateBlocked(images: (Image | null | undefined)[]): boolean {
  const at = (im: Image) => Date.parse(im.updated_at);
  const live = images.filter((im): im is Image => !!im);
  const gated = live.filter((im) => im.status === "failed" && im.error?.startsWith(GATED)).map(at);
  if (!gated.length) return false;
  const fixed = live.filter((im) => im.status === "approved" || (im.status === "to_review" && im.review?.passed)).map(at);
  return Math.max(...fixed, 0) <= Math.max(...gated);
}

/** The view a SKU's i-th raw photo shows (processing tagged it: Hero, Side, Model …), "" when untagged. */
function rawView(sku: Sku, i: number): string {
  const v = sku.raw_views?.[i];
  return v ? v[0].toUpperCase() + v.slice(1) : "";
}

/** A raw photo's view, as a small label over its thumbnail. */
function ViewTag({ view }: { view: string }) {
  return <span className="absolute inset-x-0 bottom-0 truncate bg-black/70 px-0.5 text-center text-[8px] font-semibold text-white">{view}</span>;
}

/** An aspect ratio pick: `keep` (the empty value: each shot's brief ratio, not the ratio of a past one-off
 * regeneration) or one of the library's ratios. */
function RatioSelect({ value, onChange, keep = "Brief ratio" }: { value: string; onChange: (r: string) => void; keep?: string }) {
  const { lib } = useCatalog();
  return (
    <select
      value={value}
      onChange={(e) => onChange(e.target.value)}
      aria-label="Aspect ratio"
      className="rounded-lg border border-surface-border bg-surface px-2 py-1.5 text-xs text-gray-200 focus:border-accent focus:outline-none"
    >
      <option value="">{keep}</option>
      {(lib?.ratios ?? []).map((r) => (
        <option key={r.id} value={r.id}>
          {r.label || r.id} · {r.use}
        </option>
      ))}
    </select>
  );
}

/** A bigger view of one output image (or raw photo); arrows step through the pictures on screen. Beside an image:
 * its SKU's raw photos and its feedback thread (every revision, the feedback that made it, its tries with the best
 * marked), and a box for the next feedback. Clicking a raw photo or a try shows it big; the live image is the default.
 * With `compare` on (the header button or "c"), an image shows beside one of its SKU's raw photos: the raw strip then
 * picks that photo, and a try still shows on the image side. */
function Lightbox({
  items,
  index,
  onIndex,
  onClose,
  compare,
  onCompare,
  onApprove,
  onRegenerate,
  onPick,
}: {
  items: ZoomItem[];
  index: number;
  onIndex: (n: number) => void;
  onClose: () => void;
  compare: boolean;
  onCompare: (on: boolean) => void;
  onApprove: (it: ZoomItem) => void;
  onRegenerate: (it: ZoomItem, feedback: string, ratio?: string) => void;
  onPick: (imageId: string, attempt: number) => void;
}) {
  const { lib, detail } = useCatalog();
  const item = items[index];
  // a raw photo or try shown instead of the live image, until the viewer moves on
  const [peek, setPeek] = useState<{ at: number; url: string; note?: string } | null>(null);
  // a half-written feedback belongs to the image it was typed for, like peek
  const [draft, setDraft] = useState({ at: index, text: "", ratio: "" });
  const { text, ratio } = draft.at === index ? draft : { text: "", ratio: "" };
  // the raw photo compared against, picked per SKU (else the one whose view the shot names, else the first)
  const [rawPick, setRawPick] = useState<{ sku: string; at: number } | null>(null);
  const setText = (t: string) => setDraft({ at: index, text: t, ratio });
  const setRatio = (r: string) => setDraft({ at: index, text, ratio: r });
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (["TEXTAREA", "SELECT", "INPUT"].includes((e.target as HTMLElement).tagName)) return; // typing, not viewing
      if (e.key === "Escape") onClose();
      if (e.key === "c" && items[index]?.im && items[index].sku.raw_urls.length) {
        setPeek(null); // a raw photo peeked at would otherwise sit on the image side
        onCompare(!compare);
      }
      if (e.key === "ArrowLeft" && index > 0) onIndex(index - 1);
      if (e.key === "ArrowRight" && index < items.length - 1) onIndex(index + 1);
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [index, items, onClose, onIndex, compare, onCompare]);
  if (!item) return null;
  const im = item.im;
  const shown = peek?.at === index ? peek : null;
  const thread = im ? revisionsOf(detail.images, item.sku.id, im.shot) : [];
  // while it renders: its newest try, else the newest revision with a picture
  const url = shown?.url ?? item.photo ?? im?.url ?? im?.attempts.at(-1)?.url ?? thread.findLast((r) => r.url)?.url;
  const note = shown ? shown.note : im?.review?.summary;
  const show = (u: string, n?: string) => setPeek(shown?.url === u ? null : { at: index, url: u, note: n });
  const raws = item.sku.raw_urls;
  const comparing = compare && !!im && raws.length > 0;
  const rawAt =
    rawPick?.sku === item.sku.id
      ? Math.min(rawPick.at, raws.length - 1)
      : Math.max(0, raws.findIndex((_, i) => !!item.sku.raw_views?.[i] && !!im?.shot.includes(item.sku.raw_views[i])));
  const pane = (src: string | undefined, caption: string) => (
    <figure className="flex h-full min-h-0 min-w-0 flex-1 flex-col items-center gap-1.5">
      <figcaption className="text-[11px] font-semibold tracking-wider text-gray-400 uppercase">{caption}</figcaption>
      <div className="flex min-h-0 w-full flex-1 items-center justify-center overflow-hidden rounded-xl">
        {src && <img src={safeUrl(src)} alt={`${item.label} · ${caption}`} className="h-full w-full object-contain" />}
      </div>
    </figure>
  );
  const send = () => {
    if (!im || !text.trim()) return;
    onRegenerate(item, text.trim(), ratio || undefined);
    setText("");
  };
  const navBtn = (dir: number) => {
    const to = index + dir;
    return (
      <button
        type="button"
        disabled={to < 0 || to >= items.length}
        onClick={() => onIndex(to)}
        aria-label={dir < 0 ? "Previous image" : "Next image"}
        className="flex h-10 w-10 shrink-0 items-center justify-center rounded-full bg-white/10 text-white hover:bg-white/20 disabled:opacity-20"
      >
        {railIcon(dir < 0 ? ICON.left : ICON.right, "h-5 w-5")}
      </button>
    );
  };
  return (
    <div role="dialog" aria-label={item.label} className="fixed inset-0 z-[60] flex flex-col bg-black/85 p-4 sm:p-6" onClick={onClose}>
      <div className="flex flex-wrap items-center justify-between gap-2 text-white" onClick={(e) => e.stopPropagation()}>
        <div>
          <div className="font-mono text-xs text-gray-400">
            {item.sku.code} · {index + 1}/{items.length}
          </div>
          <div className="text-sm font-semibold">{item.label}</div>
        </div>
        <div className="flex items-center gap-2">
          {im && raws.length > 0 && (
            <button
              type="button"
              onClick={() => {
                setPeek(null);
                onCompare(!compare);
              }}
              aria-pressed={compare}
              title="Compare with raw (c)"
              className={`rounded-lg px-3 py-2 text-xs font-semibold ${compare ? "bg-accent text-white hover:bg-accent-hover" : "bg-white/10 text-gray-200 hover:bg-white/20"}`}
            >
              Compare with raw
            </button>
          )}
          {im?.status === "approved" ? (
            <span className="rounded-lg bg-emerald-500/20 px-3 py-2 text-xs font-semibold text-emerald-300">Approved ✓</span>
          ) : (
            im?.status === "to_review" && (
              <button type="button" onClick={() => onApprove(item)} className="rounded-lg bg-emerald-600 px-3 py-2 text-xs font-semibold text-white hover:bg-emerald-500">
                Approve
              </button>
            )
          )}
          <button type="button" onClick={onClose} aria-label="Close" className="ml-2 rounded-lg p-2 text-gray-300 hover:bg-white/10 hover:text-white">
            {railIcon(ICON.close, "h-5 w-5")}
          </button>
        </div>
      </div>
      <div className="flex min-h-0 flex-1 flex-col gap-4 pt-4 md:flex-row" onClick={(e) => e.stopPropagation()}>
        <div className="flex min-h-0 min-w-0 flex-1 flex-col">
          <div className="flex min-h-0 flex-1 items-center justify-center gap-4">
            {navBtn(-1)}
            {comparing ? (
              <div className="flex h-full min-w-0 flex-1 gap-3">
                {pane(raws[rawAt], `Raw ${rawAt + 1}${rawView(item.sku, rawAt) ? ` · ${rawView(item.sku, rawAt)}` : ""}`)}
                {pane(url ?? undefined, shown ? "Try" : "Generated")}
              </div>
            ) : (
              <div className="flex h-full max-h-full min-w-0 items-center justify-center overflow-hidden rounded-xl">
                {url && <img src={safeUrl(url)} alt={item.label} className="max-h-full max-w-full object-contain" />}
              </div>
            )}
            {navBtn(1)}
          </div>
          {note && <p className="mx-auto mt-3 max-w-2xl text-center text-xs text-gray-400">{note}</p>}
        </div>
        {im && (
          <aside className="catalog-scroll flex max-h-[40vh] w-full shrink-0 flex-col gap-4 overflow-y-auto rounded-xl border border-white/10 bg-surface/90 p-3 md:max-h-none md:w-80">
            {item.sku.raw_urls.length > 0 && (
              <section>
                <h4 className="mb-1.5 text-[11px] font-semibold tracking-wider text-gray-400 uppercase">{comparing ? "Raw photo to compare" : "Raw photos"}</h4>
                <div className="flex flex-wrap gap-1.5">
                  {item.sku.raw_urls.map((u, i) => (
                    <Thumb
                      key={u}
                      url={u}
                      label={`Raw photo ${i + 1}`}
                      tag={rawView(item.sku, i)}
                      on={comparing ? i === rawAt : shown?.url === u}
                      onClick={() => (comparing ? setRawPick({ sku: item.sku.id, at: i }) : show(u))}
                    />
                  ))}
                </div>
              </section>
            )}
            <section>
              <h4 className="mb-1.5 text-[11px] font-semibold tracking-wider text-gray-400 uppercase">Feedback thread</h4>
              <ol className="space-y-3">
                {thread.map((rev) => {
                  const b = REVIEW_BADGE[rev.status];
                  const tries = rev.attempts.length ? rev.attempts : rev.url ? [{ index: 1, url: rev.url, review: rev.review, best: true }] : [];
                  // a try shown big that is not the image: the brand may keep it instead (not while generating)
                  const other = pickableTry(rev, shown?.url);
                  const picked = pickedByBrand(rev);
                  return (
                    <li key={rev.id} className={`rounded-lg border p-2 ${rev.id === im.id ? "border-accent/50" : "border-surface-border"}`}>
                      <div className="flex items-center gap-2 text-xs">
                        <span className="font-semibold text-white">Revision {rev.revision}</span>
                        {rev.id === im.id && <span className="text-[10px] text-accent-hover">live</span>}
                        <span className={`ml-auto text-[10px] ${b?.text ?? "text-gray-400"}`}>{rev.status === "generating" ? "Generating…" : (b?.label ?? rev.status)}</span>
                      </div>
                      <p className="mt-1 text-[11px] text-gray-400">{rev.feedback ? `“${rev.feedback}”` : rev.revision === 1 ? "First render" : "Rerun, no feedback"}</p>
                      {rev.error && <p className="mt-1 text-[11px] text-rose-300">{rev.error}</p>}
                      {tries.length > 0 && (
                        <div className="mt-1.5 flex flex-wrap gap-1.5">
                          {tries.map((t) => (
                            <Thumb
                              key={t.index}
                              url={t.url}
                              label={`Try ${t.index}${t.best ? " (best)" : ""}${t.review ? ` · ${t.review.passed ? "passed" : "failed"} review` : ""}`}
                              on={shown?.url === t.url}
                              best={t.best && tries.length > 1}
                              onClick={() => show(t.url, t.review?.summary)}
                            />
                          ))}
                        </div>
                      )}
                      {(other || picked) && (
                        <div className="mt-1.5 flex items-center gap-2 text-[11px]">
                          {picked && <span className="text-gray-400">Best picked by you, not the reviewer</span>}
                          {other && (
                            <button
                              type="button"
                              onClick={() => onPick(rev.id, other.index)}
                              title={`Keep try ${other.index} as this image instead of the reviewer's best`}
                              className="ml-auto rounded-md border border-surface-border px-2 py-0.5 text-gray-200 hover:border-accent hover:text-white"
                            >
                              Use this one
                            </button>
                          )}
                        </div>
                      )}
                    </li>
                  );
                })}
              </ol>
            </section>
            {im.status !== "approved" && im.status !== "generating" && (
              <section className="space-y-2">
                <label htmlFor="catalog-thread-feedback" className="text-xs font-medium text-gray-300">
                  Next feedback
                </label>
                <textarea
                  id="catalog-thread-feedback"
                  rows={3}
                  maxLength={2000}
                  value={text}
                  onChange={(e) => setText(e.target.value)}
                  placeholder="e.g. “the product looks too big”"
                  className="w-full resize-none rounded-lg border border-surface-border bg-surface px-2.5 py-2 text-sm text-gray-100 placeholder:text-gray-500 focus:border-accent focus:outline-none"
                />
                <div className="flex items-center gap-2">
                  <RatioSelect value={ratio} onChange={setRatio} keep={`Brief ratio (${item.sku.brief.pose_ratios[im.shot] || item.sku.brief.ratio || lib?.defaults.ratio})`} />
                  <button
                    type="button"
                    onClick={send}
                    disabled={!text.trim()}
                    className="ml-auto rounded-lg bg-accent px-3 py-1.5 text-xs font-semibold text-white hover:bg-accent-hover disabled:opacity-40"
                  >
                    Regenerate · {crRange(quoteImages([im.id], detail, lib))}
                  </button>
                </div>
              </section>
            )}
          </aside>
        )}
      </div>
    </div>
  );
}

/** By SKU cover-tile states. */
const SET_STATUS = {
  review: { label: "Needs review", dot: "bg-amber-400", text: "text-amber-300" },
  generating: { label: "Generating", dot: "bg-accent", text: "text-indigo-300" },
  ready: { label: "Ready", dot: "bg-emerald-400", text: "text-emerald-300" },
};
type SetStatus = keyof typeof SET_STATUS;

/** A By SKU tile's badge. */
function SetStatusBadge({ status }: { status: SetStatus }) {
  const st = SET_STATUS[status];
  return (
    <span className={`inline-flex max-w-full items-center gap-1 truncate rounded-full bg-black/65 px-1.5 py-0.5 text-[10px] font-medium backdrop-blur ${st.text}`}>
      <span className={`h-1.5 w-1.5 shrink-0 rounded-full ${st.dot}`} />
      {st.label}
    </span>
  );
}

/** The Gallery's two views. */
const VIEWS = [
  ["comfortable", "Comfortable"],
  ["sku", "By SKU"],
] as const;
type View = (typeof VIEWS)[number][0];

/** Comfortable · By SKU — a segmented switch. */
function ViewToggle({ view, onView }: { view: View; onView: (v: View) => void }) {
  return (
    <div className="flex rounded-lg border border-surface-border bg-surface-frame p-0.5">
      {VIEWS.map(([id, label]) => (
        <button
          key={id}
          type="button"
          onClick={() => onView(id)}
          aria-pressed={view === id}
          className={`rounded-md px-3 py-1 text-xs font-semibold transition ${view === id ? "bg-white/10 text-white" : "text-gray-400 hover:text-white"}`}
        >
          {label}
        </button>
      ))}
    </div>
  );
}

/** Gallery status filters. */
const STATUS_FILTERS = ["All", "To review", "Approved", "Running"];

/** One menu for filters, progress and the less-used actions, so the header stays small. */
function WorkspaceMenu({
  tab,
  categories,
  approvedCount,
  total,
  toReviewCount,
  category,
  onCategory,
  status,
  onStatus,
  onApproveAll,
  onDownload,
  children,
}: {
  tab: Pane;
  categories: { id: string; name: string }[];
  approvedCount: number;
  total: number;
  toReviewCount: number;
  category: string;
  onCategory: (c: string) => void;
  status: string;
  onStatus: (s: string) => void;
  onApproveAll: () => void;
  onDownload: () => void;
  children?: ReactNode;
}) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return undefined;
    const onDown = (e: MouseEvent) => {
      if (!ref.current?.contains(e.target as Node)) setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpen(false);
    };
    document.addEventListener("mousedown", onDown);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onDown);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);
  const active = (category !== "All" ? 1 : 0) + (tab === "output" && status !== "All" ? 1 : 0);
  const act = (fn: () => void) => () => {
    fn();
    setOpen(false);
  };
  const chip = (on: boolean) => `rounded-full px-2.5 py-1 text-xs ${on ? "bg-accent/25 text-white" : "text-gray-400 hover:bg-white/5 hover:text-white"}`;
  return (
    <div ref={ref} className="relative">
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        className={`flex h-9 items-center gap-2 rounded-lg border px-3 text-xs transition ${open ? "border-accent bg-accent/10 text-white" : "border-surface-border text-gray-300 hover:border-accent/60 hover:text-white"}`}
        aria-expanded={open}
      >
        {railIcon("M4 6h16M7 12h10M10 18h4")}
        <span>
          {approvedCount}/{total} approved
        </span>
        {active > 0 && <span className="rounded-full bg-accent px-1.5 text-[10px] text-white">{active}</span>}
      </button>
      {open && (
        <div className="absolute top-full right-0 z-30 mt-2 w-72 space-y-3 rounded-xl border border-surface-border bg-surface-raised p-3 shadow-2xl shadow-black/50">
          <div>
            <div className="mb-1 flex justify-between text-[11px] text-gray-400">
              <span>Progress</span>
              <span>
                {approvedCount}/{total} approved · {toReviewCount} to review
              </span>
            </div>
            <div className="h-1.5 overflow-hidden rounded-full bg-white/10">
              <div className="h-full rounded-full bg-emerald-500" style={{ width: `${(approvedCount / Math.max(1, total)) * 100}%` }} />
            </div>
          </div>
          <div>
            <div className="mb-1 text-[11px] font-semibold tracking-wider text-gray-500 uppercase">Category</div>
            <div className="flex flex-wrap gap-1">
              {[{ id: "All", name: "All" }, ...categories].map((c) => (
                <button key={c.id} type="button" className={chip(category === c.id)} onClick={() => onCategory(c.id)}>
                  {c.name}
                </button>
              ))}
            </div>
          </div>
          {tab === "output" && (
            <div>
              <div className="mb-1 text-[11px] font-semibold tracking-wider text-gray-500 uppercase">Show</div>
              <div className="flex flex-wrap gap-1">
                {STATUS_FILTERS.map((f) => (
                  <button key={f} type="button" className={chip(status === f)} onClick={() => onStatus(f)}>
                    {f}
                  </button>
                ))}
              </div>
            </div>
          )}
          {children}
          <div className="space-y-0.5 border-t border-surface-border pt-2">
            <button type="button" disabled={!toReviewCount} onClick={act(onApproveAll)} className="flex w-full items-center justify-between rounded-lg px-2 py-1.5 text-xs text-gray-200 hover:bg-white/5 disabled:opacity-40">
              Approve all to review <span className="text-emerald-400">{toReviewCount}</span>
            </button>
            <button type="button" disabled={!approvedCount} onClick={act(onDownload)} className="flex w-full items-center justify-between rounded-lg px-2 py-1.5 text-xs text-gray-200 hover:bg-white/5 disabled:opacity-40">
              Download approved <span className="text-gray-400">{approvedCount}</span>
            </button>
          </div>
        </div>
      )}
    </div>
  );
}

/** A SKU's product, model and background picks, for the edit drawer. */
export type SkuEdit = {
  category?: string;
  subcategory?: string | null; // its id within the (new) category; null = none
  wearer?: "women" | "men" | null; // whose wear it is; null = unknown
  model: string | null;
  background: string | null;
  // the brand's own words for this SKU's model and background, trimmed ("" = none)
  modelDescription: string;
  backgroundDescription: string;
  // set only when the edit changed them (the whole map replaces the brief's, also on every SKU with Apply to all)
  poseRatios?: Record<string, string>;
  poseNotes?: Record<string, string>;
  applyAll: boolean;
  rerun: boolean;
};

/** The per-SKU drawer: read-only details from processing, its raw photos (click to view), then the product (grouped by
 * family) and its subcategory, each pose's own ratio and note, model and background pickers, Apply to all, Save and Save & rerun. A product
 * without a model (furniture) shows a note instead of models. */
/** A SKU's raw photos from the SKU list: every photo (click one for the viewer) and a way to its generated images in
 * the Gallery, once it has some. */
function RawPhotos({ sku, onClose, onPhoto, onGallery }: { sku: Sku; onClose: () => void; onPhoto: (at: number) => void; onGallery?: () => void }) {
  return (
    <div className="fixed inset-0 z-50 flex justify-end bg-black/50" onClick={onClose}>
      <aside role="dialog" aria-label={`${sku.code} raw photos`} className="flex h-full w-full max-w-md flex-col border-l border-surface-border bg-surface" onClick={(e) => e.stopPropagation()}>
        <div className="flex items-center justify-between border-b border-surface-border px-5 py-4">
          <div>
            <div className="text-xs text-gray-400">
              {sku.code} · {plural(sku.raw_urls.length, "raw photo")}
            </div>
            <h3 className="text-base font-semibold text-white">{sku.name}</h3>
          </div>
          <button type="button" onClick={onClose} className="btn-ghost" aria-label="Close">
            ✕
          </button>
        </div>
        <div className="catalog-scroll flex-1 overflow-auto p-5">
          {sku.raw_urls.length ? (
            <div className="grid grid-cols-3 gap-2">
              {sku.raw_urls.map((u, i) => (
                <button
                  key={u}
                  type="button"
                  onClick={() => onPhoto(i)}
                  aria-label={`View raw photo ${i + 1}`}
                  className="relative aspect-[3/4] overflow-hidden rounded-lg border border-surface-border bg-surface-frame transition hover:border-white/40"
                >
                  <img src={safeUrl(u)} alt="" className="h-full w-full object-cover" />
                  {rawView(sku, i) && <ViewTag view={rawView(sku, i)} />}
                </button>
              ))}
            </div>
          ) : (
            <p className="text-sm text-gray-400">This SKU has no raw photos.</p>
          )}
        </div>
        <div className="border-t border-surface-border px-5 py-4">
          <button type="button" disabled={!onGallery} onClick={onGallery} className="btn-primary w-full py-2 text-sm disabled:opacity-40">
            {onGallery ? "Open generated images in the Gallery" : "No generated images yet"}
          </button>
        </div>
      </aside>
    </div>
  );
}

function EditDrawer({ sku, onClose, onSave, onPhoto }: { sku: Sku; onClose: () => void; onSave: (e: SkuEdit) => void; onPhoto: (at: number) => void }) {
  const { lib, detail } = useCatalog();
  const [category, setCategory] = useState(sku.category);
  const [subcategory, setSubcategory] = useState(sku.subcategory);
  const [wearer, setWearer] = useState(sku.attributes.wearer ?? null);
  const subcategories = catOf(lib, category)?.subcategories ?? [];
  const subTip = subcategoryTip(lib, category, subcategory); // what the picked subcategory is
  const [poseRatios, setPoseRatios] = useState<Record<string, string>>(sku.brief.pose_ratios);
  const [poseNotes, setPoseNotes] = useState<Record<string, string>>(sku.brief.pose_notes);
  const noun = poseNoun(lib, category);
  const [model, setModel] = useState(sku.brief.model);
  const [background, setBackground] = useState(sku.brief.background);
  const [modelText, setModelText] = useState(sku.brief.model_description ?? "");
  const [backgroundText, setBackgroundText] = useState(sku.brief.background_description ?? "");
  const [applyAll, setApplyAll] = useState(false);
  const family = catOf(lib, category)?.family;
  // the models of the SKU's wear (every model while it is unknown)
  const models = choices(lib, detail.session.uploads, "models").filter((m) => suitsWearers(m, new Set(wearer ? [wearer] : [])));
  const backgrounds = choices(lib, detail.session.uploads, "backgrounds").filter((b) => !b.families || !family || b.families.includes(family));
  const a = sku.attributes;
  const counts = Object.entries(a.counts ?? {}).map(([k, v]) => `${v} ${k}`).join(", ");
  const rows: [string, string | null | undefined][] = [
    ["Description", a.description],
    ["Size", a.size_mm != null ? `${a.size_mm} mm` : null],
    ["Dimensions", a.dimensions_cm && `${a.dimensions_cm} cm`],
    ["Pieces", a.pieces?.join(", ")],
    ["Details", a.details?.join(", ")],
    ["Material", a.material],
    ["Fabric", a.fabric],
    ["Colour", a.colour],
    ["Print", a.print],
    ["Work", a.work],
    ["Finish", a.finish],
    ["Style", a.style],
    ["Counts", counts],
    ["Motifs", a.motifs?.join(", ")],
    ["Source", a.source],
    ["Shots", sku.brief.poses.map((p) => poseName(lib, p)).join(", ")],
  ];
  const catLabel = skuKind(lib, sku);
  // a background made for the old product's family is not sent: the server gives the new product its default
  // per-pose ratios and notes of the poses it has now, without the empty ones
  const ofPoses = (m: Record<string, string>) =>
    Object.fromEntries(sku.brief.poses.flatMap((p) => (m[p]?.trim() ? [[p, m[p].trim()]] : [])));
  // compared over the current poses only, so stale entries of removed poses never count as an edit
  const changed = (edited: Record<string, string>, stored: Record<string, string>) =>
    JSON.stringify(ofPoses(edited)) === JSON.stringify(ofPoses(stored)) ? undefined : ofPoses(edited);
  const save = (rerun: boolean) =>
    onSave({
      category,
      subcategory,
      wearer,
      model,
      background: backgrounds.some((b) => b.id === background) ? background : null,
      modelDescription: modelText.trim(),
      backgroundDescription: backgroundText.trim(),
      poseRatios: changed(poseRatios, sku.brief.pose_ratios),
      poseNotes: changed(poseNotes, sku.brief.pose_notes),
      applyAll,
      rerun,
    });
  return (
    <div className="fixed inset-0 z-50 flex justify-end bg-black/50" onClick={onClose}>
      <aside role="dialog" aria-label={`Edit ${sku.code}`} className="flex h-full w-full max-w-md flex-col border-l border-surface-border bg-surface" onClick={(e) => e.stopPropagation()}>
        <div className="flex items-center justify-between border-b border-surface-border px-5 py-4">
          <div>
            <div className="text-xs text-gray-400">
              {sku.code} · {catLabel}
            </div>
            <h3 className="text-base font-semibold text-white">{sku.name}</h3>
          </div>
          <button type="button" onClick={onClose} className="btn-ghost" aria-label="Close">
            ✕
          </button>
        </div>
        <div className="catalog-scroll flex-1 space-y-6 overflow-y-auto px-5 py-5">
          <section>
            <h4 className="mb-2 text-xs font-semibold tracking-wider text-gray-400 uppercase">Details · from pre-processing</h4>
            <dl className="card divide-y divide-surface-border text-sm">
              {rows
                .filter(([, v]) => v)
                .map(([k, v]) => (
                  <div key={k} className="flex gap-3 px-3 py-2">
                    <dt className="w-24 shrink-0 text-gray-400">{k}</dt>
                    <dd className="text-gray-200">{v}</dd>
                  </div>
                ))}
            </dl>
            <p className="mt-1.5 text-[11px] text-gray-500">Read-only. These come from pre-processing.</p>
          </section>
          {sku.raw_urls.length > 0 && (
            <section>
              <h4 className="mb-2 text-xs font-semibold tracking-wider text-gray-400 uppercase">Raw photos</h4>
              <div className="flex flex-wrap gap-1.5">
                {sku.raw_urls.map((u, i) => (
                  <Thumb key={u} url={u} label={`View raw photo ${i + 1}`} tag={rawView(sku, i)} on={false} onClick={() => onPhoto(i)} />
                ))}
              </div>
            </section>
          )}
          <section>
            <h4 className="mb-2 text-xs font-semibold tracking-wider text-gray-400 uppercase">Product</h4>
            <select
              value={category}
              onChange={(e) => {
                setCategory(e.target.value);
                setSubcategory(null); // the old product's subcategories don't apply
              }}
              aria-label="Product"
              className="w-full rounded-lg border border-surface-border bg-surface-raised px-3 py-2 text-sm text-gray-100 focus:border-accent focus:outline-none"
            >
              {(lib?.families ?? []).map((f) => (
                <optgroup key={f.id} label={f.name}>
                  {lib!.categories
                    .filter((c) => c.family === f.id)
                    .map((c) => (
                      <option key={c.id} value={c.id}>
                        {c.name}
                      </option>
                    ))}
                </optgroup>
              ))}
              {!catOf(lib, sku.category) && <option value={sku.category}>{sku.category}</option>}
            </select>
            {subcategories.length > 0 && (
              <div className="mt-2 flex items-center gap-1.5">
                <select
                  value={subcategory ?? ""}
                  onChange={(e) => setSubcategory(e.target.value || null)}
                  aria-label="Subcategory"
                  className="min-w-0 flex-1 rounded-lg border border-surface-border bg-surface-raised px-3 py-2 text-sm text-gray-100 focus:border-accent focus:outline-none"
                >
                  <option value="">No subcategory</option>
                  {subcategories.map((s) => (
                    <option key={s.id} value={s.id}>
                      {s.name}
                    </option>
                  ))}
                </select>
                {subTip && <InfoTip text={subTip} label="What is this subcategory?" />}
              </div>
            )}
            {category !== sku.category && (
              <p className="mt-1.5 text-[11px] text-gray-500">{withNoun("Poses", noun)}, prompt and review follow the new product. The code stays {sku.code}.</p>
            )}
            {(family === "garments" || family === "jewellery") && (
              <div className="mt-2 flex items-center gap-1.5" role="group" aria-label="Wear">
                {(["women", "men"] as const).map((w) => (
                  <button
                    key={w}
                    type="button"
                    aria-pressed={wearer === w}
                    onClick={() => {
                      const next = wearer === w ? null : w; // the picked one again: not known
                      setWearer(next);
                      // a model of the other wear goes: the server gives the SKU its default for the new one
                      const was = lib?.models.find((m) => m.id === model)?.wearer;
                      if (next && was && was !== next) setModel(null);
                    }}
                    className={`rounded-lg border px-2.5 py-1 text-xs transition ${wearer === w ? "border-accent bg-accent/15 text-white" : "border-surface-border text-gray-300 hover:border-gray-500 hover:text-white"}`}
                  >
                    {w === "women" ? "Women's wear" : "Men's wear"}
                  </button>
                ))}
              </div>
            )}
          </section>
          {category === sku.category && sku.brief.poses.length > 0 && (
            <section>
              <h4 className="mb-2 text-xs font-semibold tracking-wider text-gray-400 uppercase">{withNoun("Poses", noun)}</h4>
              <ul className="space-y-2.5">
                {sku.brief.poses.map((p) => (
                  <li key={p} className="space-y-1.5">
                    <div className="flex items-center gap-2">
                      <span className="min-w-0 flex-1 truncate text-sm text-gray-200">{poseName(lib, p)}</span>
                      <RatioSelect
                        value={poseRatios[p] ?? ""}
                        onChange={(r) => setPoseRatios((x) => ({ ...x, [p]: r }))}
                        keep={`SKU ratio${sku.brief.ratio ? ` (${sku.brief.ratio})` : ""}`}
                      />
                    </div>
                    <input
                      value={poseNotes[p] ?? ""}
                      maxLength={500}
                      onChange={(e) => setPoseNotes((x) => ({ ...x, [p]: e.target.value }))}
                      aria-label={`Instruction for ${poseName(lib, p)}`}
                      placeholder={withNoun("Instruction for this pose (optional), e.g. “closer crop, softer shadow”", noun)}
                      className="w-full rounded-lg border border-surface-border bg-surface-raised px-2.5 py-1.5 text-xs text-gray-100 placeholder:text-gray-500 focus:border-accent focus:outline-none"
                    />
                  </li>
                ))}
              </ul>
            </section>
          )}
          <section>
            <h4 className="mb-2 text-xs font-semibold tracking-wider text-gray-400 uppercase">Model</h4>
            {!usesModel(lib, category) ? (
              <p className="text-sm text-gray-400">{sceneNote(lib, [{ category }])}.</p>
            ) : (
              <div className="grid grid-cols-4 gap-2">
                {models.map((m) => (
                  <button
                    key={m.id}
                    type="button"
                    onClick={() => setModel(m.id)}
                    aria-pressed={model === m.id}
                    title={m.tags?.length ? m.tags.join(", ") : undefined}
                    className={`rounded-lg border p-1.5 text-center transition ${model === m.id ? "border-accent bg-accent/10" : "border-surface-border hover:border-gray-500"}`}
                  >
                    {m.image ? <img src={m.image} alt="" className="aspect-square w-full rounded object-cover" /> : <span className="block aspect-square w-full rounded bg-surface-raised" />}
                    <div className="mt-1 truncate text-[11px] text-gray-200">{m.name}</div>
                  </button>
                ))}
              </div>
            )}
            {usesModel(lib, category) && <DescribeBox kind="model" value={modelText} onChange={setModelText} />}
          </section>
          <section>
            <h4 className="mb-2 text-xs font-semibold tracking-wider text-gray-400 uppercase">Background</h4>
            <div className="flex flex-wrap gap-3">
              {backgrounds.map((b) => (
                <button key={b.id} type="button" onClick={() => setBackground(b.id)} aria-pressed={background === b.id} className="flex w-12 flex-col items-center gap-1">
                  <span
                    className={`h-10 w-10 rounded-full border-2 bg-cover bg-center ${background === b.id ? "border-accent" : "border-transparent"}`}
                    style={{ background: b.image ? `center / cover url("${b.image}")` : (b.swatch ?? "#2f3446") }}
                  />
                  <span className={`w-full truncate text-center text-[11px] ${background === b.id ? "text-white" : "text-gray-400"}`}>{b.name}</span>
                </button>
              ))}
            </div>
            <DescribeBox kind="background" value={backgroundText} onChange={setBackgroundText} />
          </section>
          <label className="flex items-center gap-2 text-sm text-gray-300">
            <input type="checkbox" checked={applyAll} onChange={(e) => setApplyAll(e.target.checked)} className="accent-accent" />
            Apply to every SKU in {catLabel}
          </label>
        </div>
        <div className="flex justify-end gap-2 border-t border-surface-border px-5 py-4">
          <button type="button" onClick={() => save(false)} className="btn-ghost border border-surface-border px-4 py-2">
            Save
          </button>
          <button type="button" onClick={() => save(true)} className="btn-primary px-4 py-2 text-xs">
            Save &amp; rerun
          </button>
        </div>
      </aside>
    </div>
  );
}

/** Right-side panel for the selected images in the Gallery: approve them, or one feedback for all. */
function FeedbackPanel({
  picked,
  focus,
  onRemove,
  onApprove,
  onSend,
  onClear,
}: {
  picked: string[];
  focus: boolean;
  onRemove: (id: string) => void;
  onApprove: () => void;
  onSend: (text: string, ratio?: string) => void;
  onClear: () => void;
}) {
  const { lib, detail } = useCatalog();
  const [text, setText] = useState("");
  const [ratio, setRatio] = useState("");
  const inputRef = useRef<HTMLTextAreaElement>(null);
  useEffect(() => {
    if (focus) inputRef.current?.focus();
  }, [focus, picked.length]);
  const images = picked.map((id) => detail.images.find((x) => x.id === id)).filter((x): x is Image => !!x);
  const skuCount = new Set(images.map((im) => im.sku_id)).size;
  const send = () => {
    if (text.trim()) {
      onSend(text.trim(), ratio || undefined);
      setText("");
    }
  };
  return (
    <aside className="flex w-full shrink-0 flex-col border-t border-accent/40 bg-surface-raised/40 md:w-[22rem] md:border-t-0 md:border-l xl:w-96">
      <div className="flex items-center justify-between border-b border-surface-border px-4 py-3">
        <div>
          <div className="text-sm font-semibold text-white">Feedback</div>
          <div className="text-[11px] text-gray-400">
            {plural(picked.length, "image")} selected · {plural(skuCount, "product")}
          </div>
        </div>
        <button type="button" onClick={onClear} title="Clear selection" aria-label="Clear selection" className="rounded-lg p-1.5 text-gray-400 hover:bg-white/5 hover:text-white">
          {railIcon(ICON.close)}
        </button>
      </div>
      <div className="catalog-scroll flex-1 overflow-y-auto p-4">
        <div className="grid grid-cols-4 gap-2">
          {images.map((im) => {
            const sku = detail.skus.find((s) => s.id === im.sku_id);
            const label = `${sku?.code} · ${poseName(lib, im.shot)}`;
            return (
              <div key={im.id} className="group relative">
                <div className="aspect-square overflow-hidden rounded-md bg-surface-frame">
                  {im.url && <img src={safeUrl(im.url)} alt="" className="h-full w-full object-cover" />}
                </div>
                <div className="mt-0.5 truncate text-[9.5px] text-gray-500">{label}</div>
                <button
                  type="button"
                  onClick={() => onRemove(im.id)}
                  aria-label={`Remove ${label}`}
                  className="absolute -top-1 -right-1 flex h-4 w-4 items-center justify-center rounded-full bg-black/80 text-[10px] text-white opacity-0 transition group-hover:opacity-100 focus:opacity-100"
                >
                  ×
                </button>
              </div>
            );
          })}
        </div>
      </div>
      <div className="space-y-2 border-t border-surface-border p-4">
        <label htmlFor="catalog-feedback" className="text-xs font-medium text-gray-300">
          What should change?
        </label>
        <textarea
          id="catalog-feedback"
          ref={inputRef}
          rows={4}
          value={text}
          onChange={(e) => setText(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && !e.shiftKey) {
              e.preventDefault();
              send();
            }
          }}
          placeholder={`Feedback for ${picked.length === 1 ? "this image" : `all ${picked.length} images`} — e.g. “the product looks too big”, “softer shadow”`}
          className="w-full resize-none rounded-xl border border-accent/50 bg-surface px-3 py-2 text-sm text-gray-100 placeholder:text-gray-500 focus:border-accent focus:outline-none"
        />
        <div className="flex items-center gap-2 text-[11px] text-gray-400">
          Aspect ratio <RatioSelect value={ratio} onChange={setRatio} />
        </div>
        <button type="button" onClick={send} disabled={!text.trim()} className="w-full rounded-lg bg-accent px-3 py-2.5 text-xs font-semibold text-white hover:bg-accent-hover disabled:opacity-40">
          Regenerate {plural(picked.length, "image")} · {crRange(quoteImages(picked, detail, lib))}
        </button>
        <button type="button" onClick={onApprove} className="w-full rounded-lg border border-emerald-500/50 px-3 py-2 text-xs font-semibold text-emerald-300 hover:bg-emerald-500/10">
          Approve {picked.length} instead
        </button>
      </div>
    </aside>
  );
}

/** The screen's actions Processing needs beyond the chat. */
export type OutputOps = {
  approve: (imageIds: string[]) => Promise<void>;
  regenerate: (imageIds: string[], feedback: string, ratio?: string) => Promise<void>;
  pickTry: (imageId: string, attempt: number) => Promise<void>; // keep another render try as the image
  runSkus: (skuIds: string[], missingOnly?: boolean) => Promise<void>; // missingOnly: only shots with no live image
  stopImages: (imageIds: string[], what: string) => Promise<void>; // a tile's one, a SKU row's generating ones
  stopAll: () => Promise<void>; // every queued or running run
  editSku: (sku: Sku, e: SkuEdit) => Promise<void>;
  download: () => Promise<void>;
};

/**
 * Processing's three panes: Processing (chat + the latest run's output), SKU list (statuses, run, edit) and Gallery
 * (every image; Comfortable or By SKU; select one shot across products; feedback panel).
 */
export function Generation({
  pane: tab,
  onPane,
  history,
  log,
  next,
  chat,
  out,
}: {
  pane: Pane;
  onPane: (p: Pane) => void;
  history: ReactNode;
  log: Msg[];
  next: S<"NextStep"> | null;
  chat: ProcessingOps;
  out: OutputOps;
}) {
  const { lib, detail } = useCatalog();
  const skus = detail.skus;
  const live = useMemo(() => liveImages(detail.images), [detail.images]);
  const [outOpen, setOutOpen] = useState(true);
  const [selected, setSelected] = useState<Set<string>>(() => new Set());
  const [filter, setFilter] = useState("All");
  const [outFilter, setOutFilter] = useState("All");
  const [editing, setEditing] = useState<string | null>(null);
  const [rawOf, setRawOf] = useState<string | null>(null); // the SKU whose raw photos the SKU list shows
  const [stopping, setStopping] = useState<Set<string>>(() => new Set()); // keys (image or SKU id, "all") being stopped
  const [picked, setPicked] = useState<string[]>([]);
  const [feedbackOpen, setFeedbackOpen] = useState(false);
  const [view, setView] = useState<View>("comfortable");
  const [openSet, setOpenSet] = useState<string | null>(null);
  const [scrollTo, setScrollTo] = useState<string | null>(null);
  const [zoom, setZoom] = useState<string | null>(null); // the shotKey of the image in the viewer
  const [rawZoom, setRawZoom] = useState<{ sku: string; at: number } | null>(null); // a SKU's raw photo in the viewer
  const [compare, setCompare] = useState(false); // the viewer shows each image beside a raw photo (stays on until turned off)

  useEffect(() => {
    if (tab === "skus" || !scrollTo) return;
    document.getElementById(`catalog-sku-${scrollTo}`)?.scrollIntoView({ block: "start" });
    const t = setTimeout(() => setScrollTo(null), 0);
    return () => clearTimeout(t);
  }, [tab, scrollTo, view]);

  /** A SKU's live images per shot. */
  const imagesOf = (s: Sku) => shotsOf(s, live).map((shot) => ({ shot, im: live.get(shotKey(s.id, shot)) }));
  const hasImages = (s: Sku) => imagesOf(s).some((x) => x.im);
  const toReviewOf = (s: Sku) => imagesOf(s).flatMap(({ im }) => (im?.status === "to_review" ? [im.id] : []));
  const latest = detail.runs[0]?.sku_ids ?? [];
  const latestCodes = skus.filter((s) => latest.includes(s.id)).map((s) => s.code);
  const hasOutput = latest.length > 0;
  const categories = (lib?.categories ?? []).filter((c) => skus.some((s) => s.category === c.id));

  /** Runs a Stop under `key`, so its button shows Stopping… and is disabled until it answers. */
  const stopAs = async (key: string, fn: () => Promise<void>) => {
    setStopping((p) => new Set(p).add(key));
    await fn();
    setStopping((p) => {
      const n = new Set(p);
      n.delete(key);
      return n;
    });
  };
  /** A SKU's images still generating: what its row Stop stops. */
  const generatingOf = (s: Sku) => imagesOf(s).flatMap(({ im }) => (im?.status === "generating" ? [im.id] : []));

  /** Approves images and drops them from the selection. */
  const approve = async (ids: string[]) => {
    if (!ids.length) return;
    await out.approve(ids);
    setPicked((p) => p.filter((x) => !ids.includes(x)));
  };
  /** Regenerates the selected images with one feedback (at `ratio` when one is picked). */
  const sendFeedback = async (text: string, ratio?: string) => {
    const ids = picked;
    setPicked([]);
    setFeedbackOpen(false);
    await out.regenerate(ids, text, ratio);
  };
  const togglePick = (id: string) => setPicked((p) => (p.includes(id) ? p.filter((x) => x !== id) : [...p, id]));
  const feedbackOn = (id: string) => {
    setPicked([id]);
    setFeedbackOpen(true);
  };
  /** SKU list status → that SKU in the Gallery (popup in By SKU, scrolled-to card in Comfortable). */
  const showInOutput = (id: string) => {
    setOutFilter("All");
    if (view === "sku") setOpenSet(id);
    else setScrollTo(id);
    onPane("output");
  };

  const visible = skus.filter((s) => filter === "All" || s.category === filter);
  const toReview = skus.filter((s) => s.status === "To review");
  const approvedSkus = skus.filter((s) => s.status === "Approved");
  const reviewed = visible.filter(
    (s) => (hasImages(s) || isBusy(s)) && (outFilter === "All" || (outFilter === "Running" ? isBusy(s) : s.status === outFilter)),
  );
  // Processing shows only the latest run's SKUs; the Gallery shows everything.
  const shown = tab === "processing" ? skus.filter((s) => latest.includes(s.id)) : reviewed;
  const paneView: View = tab === "processing" ? "comfortable" : view;
  const allVisibleSelected = visible.length > 0 && visible.every((s) => selected.has(s.id));

  // Quick-select one shot across every product waiting for review.
  const reviewable = reviewed.flatMap((s) => imagesOf(s).flatMap(({ shot, im }) => (im?.status === "to_review" ? [{ shot, id: im.id }] : [])));
  const shotGroups = [...new Set(reviewable.map((r) => r.shot))].map((shot) => ({
    shot: poseName(lib, shot),
    items: reviewable.filter((r) => r.shot === shot).map((r) => r.id),
  }));
  const groupOn = (items: string[]) => items.length > 0 && items.every((id) => picked.includes(id));
  const toggleGroup = (items: string[]) =>
    setPicked((p) => (groupOn(items) ? p.filter((x) => !items.includes(x)) : [...p, ...items.filter((id) => !p.includes(id))]));

  // What the viewer steps through: one SKU's raw photos, else every image on screen with a picture (a revision
  // rendering keeps its place, so the viewer stays open on its thread).
  const rawSku = skus.find((s) => s.id === rawZoom?.sku);
  const zoomItems: ZoomItem[] = rawSku
    ? rawSku.raw_urls.map((u, i) => ({ sku: rawSku, photo: u, label: `${rawSku.name} · raw photo ${i + 1}${rawView(rawSku, i) ? ` · ${rawView(rawSku, i)}` : ""}` }))
    : shown.flatMap((s) =>
        imagesOf(s).flatMap(({ shot, im }) =>
          im && (im.url ? im.status !== "generating" : im.revision > 1 || im.attempts.length > 0) ? [{ im, sku: s, label: `${s.name} · ${poseName(lib, shot)}` }] : [],
        ),
      );
  const zoomIndex = rawSku ? Math.min(rawZoom!.at, zoomItems.length - 1) : zoom ? zoomItems.findIndex((it) => it.im && shotKey(it.sku.id, it.im.shot) === zoom) : -1;
  /** Opens the viewer on a SKU's raw photo. */
  const openRaw = (s: Sku, at: number) => setRawZoom({ sku: s.id, at });

  const editingSku = skus.find((s) => s.id === editing);
  const rawOfSku = skus.find((s) => s.id === rawOf);
  const gridCols = tab === "processing" ? "lg:grid-cols-3 xl:grid-cols-4" : picked.length ? "lg:grid-cols-5" : "lg:grid-cols-6";
  const openSetSku = paneView === "sku" ? shown.find((s) => s.id === openSet) : undefined;
  const nameOf = (kind: "models" | "backgrounds", id: string | null, described?: string | null) =>
    choiceLabel(id && (choices(lib, detail.session.uploads, kind).find((x) => x.id === id)?.name ?? id), described);

  /** A SKU's header actions: edit, and approve what's waiting. */
  const skuActions = (s: Sku) => {
    const busy = isBusy(s);
    const open = toReviewOf(s);
    const shots = shotsOf(s, live).length;
    return (
      <>
        <IconButton disabled={busy} onClick={() => setEditing(s.id)} title="Edit product, model & background" d={ICON.pen} />
        <button
          type="button"
          disabled={busy || !open.length}
          onClick={() => void approve(open)}
          className="h-8 rounded-lg bg-emerald-600/80 px-3 text-xs font-medium text-white hover:bg-emerald-500 disabled:opacity-40"
        >
          {s.status === "Approved" ? "Approved ✓" : `Approve ${open.length === shots ? "all" : open.length}`}
        </button>
      </>
    );
  };

  /** One SKU's card: a slim header row, then a 3:4 grid of every shot. */
  const skuCard = (s: Sku, hideHeader = false) => {
    const busy = isBusy(s);
    const cells = imagesOf(s);
    const open = toReviewOf(s).length;
    const feedback = cells.find(({ im }) => im?.feedback)?.im?.feedback;
    return (
      <div
        key={s.id}
        id={hideHeader ? undefined : `catalog-sku-${s.id}`}
        className={`card scroll-mt-4 overflow-hidden ${s.status === "Approved" ? "ring-1 ring-emerald-500/50" : ""}`}
      >
        {!hideHeader && (
          <div className="flex flex-wrap items-center gap-x-2.5 gap-y-1 border-b border-surface-border px-3 py-2">
            <h3 className="truncate text-sm font-semibold text-white">{s.code}</h3>
            <span className="truncate text-xs text-gray-400">{s.name}</span>
            {subName(lib, s) && <span className="rounded-md bg-white/5 px-1.5 py-0.5 text-[11px] text-gray-300">{subName(lib, s)}</span>}
            <span className="rounded-md border border-surface-border bg-surface px-1.5 py-0.5 text-[11px] text-gray-300">
              {usesModel(lib, s.category) ? nameOf("models", s.brief.model, s.brief.model_description) : "No model"} ·{" "}
              {nameOf("backgrounds", s.brief.background, s.brief.background_description)}
            </span>
            <span className="text-xs text-gray-500">{nounCount(cells.length, poseNoun(lib, s.category))}</span>
            {open > 0 && !busy && <span className="text-xs text-amber-300">{open} to review</span>}
            <div className="ml-auto flex items-center gap-1.5">{skuActions(s)}</div>
          </div>
        )}
        {busy && (
          <div className="mx-3 mt-3 flex items-center gap-2 rounded-lg border border-accent/40 bg-accent/10 px-3 py-1.5 text-xs text-accent-hover">
            <span className="h-3.5 w-3.5 shrink-0 animate-spin rounded-full border-2 border-accent/40 border-t-accent" />
            <span className="font-semibold">
              {cells.some(({ im }) => im && im.revision > 1 && im.status === "generating")
                ? `Regenerating ${plural(cells.filter(({ im }) => im?.status === "generating").length, "image")}…`
                : "Generating the set…"}
            </span>
          </div>
        )}
        {feedback && <p className="mx-3 mt-3 truncate text-[11px] text-gray-500">Last feedback: “{feedback}”</p>}
        {s.raw_urls.length > 0 && (
          <div className="mx-3 mt-3 flex items-center gap-1.5 overflow-x-auto">
            <span className="shrink-0 text-[11px] text-gray-500">Raw</span>
            {s.raw_urls.map((u, i) => (
              <Thumb key={u} url={u} label={`View ${s.code} raw photo ${i + 1}`} tag={rawView(s, i)} on={false} onClick={() => openRaw(s, i)} />
            ))}
          </div>
        )}
        <div className={`grid grid-cols-2 gap-2.5 p-3 sm:grid-cols-4 ${hideHeader ? "lg:grid-cols-6" : gridCols}`}>
          {cells.map(({ shot, im }) => (
            <div key={shot} className="min-w-0">
              {!im || im.status === "generating" ? (
                <PendingTile
                  running={!!im}
                  label={`${s.code} ${poseName(lib, shot)}`}
                  stopping={!!im && stopping.has(im.id)}
                  onStop={() => im && void stopAs(im.id, () => out.stopImages([im.id], "1 image"))}
                  canRun={!isBusy(s)}
                  onRun={() => void out.runSkus([s.id], true)}
                  live={im && liveTry(im, attemptsFor(lib, s.category))}
                  onOpen={() => setZoom(shotKey(s.id, shot))}
                />
              ) : (
                <ImageTile
                  label={`${s.code} ${poseName(lib, shot)}`}
                  im={im}
                  selected={picked.includes(im.id)}
                  onToggle={() => togglePick(im.id)}
                  onApprove={() => void approve([im.id])}
                  onFeedback={() => feedbackOn(im.id)}
                  onOpen={() => (im.url ? setZoom(shotKey(s.id, shot)) : feedbackOn(im.id))}
                  onCompare={
                    im.url && s.raw_urls.length
                      ? () => {
                          setCompare(true);
                          setZoom(shotKey(s.id, shot));
                        }
                      : undefined
                  }
                />
              )}
              <p className="mt-1.5 truncate text-xs font-medium text-gray-300">{poseName(lib, shot)}</p>
            </div>
          ))}
        </div>
      </div>
    );
  };

  /** A By SKU cover tile: the first image, a strip of the next ones, its state and Approve. */
  const setTile = (s: Sku) => {
    const cells = imagesOf(s);
    const status: SetStatus = isBusy(s) ? "generating" : toReviewOf(s).length ? "review" : "ready";
    const open = toReviewOf(s);
    const approvedN = cells.filter(({ im }) => im?.status === "approved").length;
    const urls = cells.flatMap(({ im }) => (im?.url ? [im.url] : []));
    const strip = urls.slice(1, 4);
    const more = cells.length - 1 - strip.length;
    return (
      <div key={s.id} className="min-w-0">
        <button type="button" onClick={() => setOpenSet(s.id)} className="group w-full text-left" aria-label={`Open ${s.code} images`}>
          <div
            className={`relative aspect-square overflow-hidden rounded-xl border bg-surface-frame transition group-hover:border-accent ${status === "ready" ? "border-emerald-500/60 ring-1 ring-emerald-500/40" : "border-surface-border"}`}
          >
            {urls[0] && <img src={safeUrl(urls[0])} alt="" className="h-full w-full object-cover object-top transition duration-300 group-hover:scale-[1.03]" />}
            {status === "generating" && (
              <span className="absolute inset-0 flex items-center justify-center bg-black/50">
                <span className="h-6 w-6 animate-spin rounded-full border-2 border-accent/40 border-t-accent" />
              </span>
            )}
            <span className="absolute top-1.5 left-1.5">
              <SetStatusBadge status={status} />
            </span>
            <div className="absolute inset-x-1.5 bottom-1.5 flex gap-0.5">
              {strip.map((u) => (
                <span key={u} className="h-7 w-5 overflow-hidden rounded-sm border border-white/30">
                  <img src={safeUrl(u)} alt="" className="h-full w-full object-cover" />
                </span>
              ))}
              {more > 0 && (
                <span className="flex h-7 w-5 items-center justify-center rounded-sm border border-white/30 bg-black/70 text-[9px] font-semibold text-white">
                  +{more}
                </span>
              )}
            </div>
          </div>
          <p className="mt-1.5 truncate text-xs font-semibold text-white">{s.code}</p>
          <p className="truncate text-[11px] text-gray-500">
            {nounCount(cells.length, poseNoun(lib, s.category))} · {approvedN} approved
          </p>
        </button>
        {open.length > 0 && status !== "generating" && (
          <button
            type="button"
            onClick={() => void approve(open)}
            className="mt-1 w-full rounded-lg border border-emerald-500/40 px-1.5 py-1 text-[10px] font-semibold text-emerald-300 transition hover:bg-emerald-500/10"
          >
            Approve {open.length === cells.length ? "all" : open.length}
          </button>
        )}
      </div>
    );
  };

  // The Gallery's body; Processing shows it to the right of the chat.
  const outputPane = (
    <div className="flex min-h-0 min-w-0 flex-1 flex-col md:flex-row">
      <div className="relative min-h-0 min-w-0 flex-1">
        <div className="catalog-scroll h-full overflow-y-auto p-4">
          {shown.length === 0 && (
            <div className="card p-8 text-center text-sm text-gray-400">
              {tab === "processing"
                ? "Nothing here yet. Pick a next step on the left — its images show up here."
                : outFilter === "All"
                  ? "Nothing to review yet. Run a sample from Processing or the SKU list."
                  : `No SKUs in “${outFilter}”.`}
            </div>
          )}
          {paneView === "sku" ? (
            <div className="space-y-8">
              {categories
                .map((c) => [c, shown.filter((s) => s.category === c.id)] as const)
                .filter(([, list]) => list.length)
                .map(([c, list]) => (
                  <section key={c.id}>
                    <h3 className="mb-3 flex items-baseline gap-2 text-sm font-semibold text-white">
                      {c.name}
                      <span className="text-xs font-normal text-gray-500">{plural(list.length, "set")}</span>
                    </h3>
                    <div className={`grid grid-cols-2 gap-3 sm:grid-cols-4 ${gridCols}`}>{list.map(setTile)}</div>
                  </section>
                ))}
            </div>
          ) : (
            <div className="space-y-3">{shown.map((s) => skuCard(s))}</div>
          )}
        </div>
        {openSetSku && (
          <div className="absolute inset-0 z-20 overflow-y-auto bg-black/80 p-4 backdrop-blur-sm sm:p-6" onClick={() => setOpenSet(null)}>
            <div className="mx-auto w-full max-w-5xl" onClick={(e) => e.stopPropagation()}>
              <div className="mb-3 flex flex-wrap items-center justify-between gap-3">
                <div className="flex min-w-0 flex-wrap items-center gap-2">
                  <h2 className="truncate text-lg font-semibold text-white">{openSetSku.code}</h2>
                  <span className="truncate text-sm text-gray-400">{openSetSku.name}</span>
                  <span className="rounded-full border border-surface-border bg-surface px-2 py-0.5 text-[11px] font-medium text-gray-300">
                    {skuKind(lib, openSetSku)}
                  </span>
                </div>
                <div className="flex items-center gap-2">
                  {skuActions(openSetSku)}
                  <button
                    type="button"
                    onClick={() => {
                      setView("comfortable");
                      setScrollTo(openSetSku.id);
                      setOpenSet(null);
                    }}
                    className="inline-flex h-8 items-center gap-1.5 rounded-lg border border-surface-border bg-surface px-3 text-xs font-semibold text-gray-200 transition hover:border-accent hover:text-white"
                  >
                    {railIcon(ICON.expand, "h-3.5 w-3.5")}
                    Comfortable view
                  </button>
                  <button type="button" onClick={() => setOpenSet(null)} aria-label="Close" className="flex h-8 w-8 items-center justify-center rounded-lg text-xl leading-none text-gray-400 hover:bg-white/10 hover:text-white">
                    ×
                  </button>
                </div>
              </div>
              {skuCard(openSetSku, true)}
            </div>
          </div>
        )}
      </div>
      {picked.length > 0 && tab !== "processing" && (
        <FeedbackPanel
          picked={picked}
          focus={feedbackOpen}
          onRemove={(id) => setPicked((p) => p.filter((x) => x !== id))}
          onApprove={() => void approve(picked)}
          onSend={(t, r) => void sendFeedback(t, r)}
          onClear={() => {
            setPicked([]);
            setFeedbackOpen(false);
          }}
        />
      )}
    </div>
  );

  return (
    <section className="relative flex h-full min-w-0 flex-col overflow-hidden rounded-2xl border border-surface-border bg-surface">
      <div className="flex flex-wrap items-center justify-between gap-3 border-b border-surface-border px-3 py-2.5">
        <h2 className="flex items-center gap-2 px-1 text-sm font-semibold text-white">
          {{ processing: "Processing", skus: "SKU list", output: "Gallery" }[tab]}
          <span className="rounded-full bg-white/10 px-1.5 text-[11px] font-medium text-gray-300">
            {tab === "skus"
              ? skus.length
              : tab === "processing"
                ? hasOutput
                  ? `Latest run · ${latestCodes.length === 1 ? latestCodes[0] : plural(latestCodes.length, "SKU")}`
                  : "No run yet"
                : `${toReview.length} to review`}
          </span>
        </h2>
        <div className="flex items-center gap-2">
          {tab === "skus" && selected.size > 0 && (
            <button
              type="button"
              onClick={() => {
                void out.runSkus([...selected]);
                setSelected(new Set());
              }}
              className="btn-primary px-4 py-2 text-xs"
            >
              Run selected ({selected.size})
              <span className="font-normal text-white/70"> · {crRange(quoteSkus(skus.filter((s) => selected.has(s.id)), lib))}</span>
            </button>
          )}
          {tab === "processing" && hasOutput && (
            <button
              type="button"
              onClick={() => setOutOpen((o) => !o)}
              aria-expanded={outOpen}
              title={outOpen ? "Hide the output panel" : "Show the output panel"}
              className="inline-flex h-8 items-center gap-1.5 rounded-lg border border-surface-border px-3 text-xs text-gray-300 transition hover:border-gray-500 hover:text-white"
            >
              {railIcon(outOpen ? ICON.right : ICON.left, "h-3.5 w-3.5")}
              {outOpen ? "Hide output" : `Show output · ${latestCodes.length === 1 ? latestCodes[0] : plural(latestCodes.length, "SKU")}`}
            </button>
          )}
          {tab === "output" && (
            <ViewToggle
              view={view}
              onView={(v) => {
                setView(v);
                setOpenSet(null);
              }}
            />
          )}
          {tab === "skus" && activeRuns(detail.runs).length > 0 && (
            <button
              type="button"
              disabled={stopping.has("all")}
              onClick={() => void stopAs("all", out.stopAll)}
              className="flex h-9 items-center gap-2 rounded-lg border border-surface-border px-3 text-xs text-gray-300 transition hover:border-accent/60 hover:text-white disabled:opacity-40"
            >
              {railIcon(ICON.stop)}
              {stopping.has("all") ? "Stopping…" : "Stop all"}
            </button>
          )}
          {tab !== "processing" && (
            <WorkspaceMenu
              tab={tab}
              categories={categories}
              approvedCount={approvedSkus.length}
              total={skus.length}
              toReviewCount={toReview.length}
              category={filter}
              onCategory={setFilter}
              status={outFilter}
              onStatus={setOutFilter}
              onApproveAll={() => void approve(toReview.flatMap(toReviewOf))}
              onDownload={() => void out.download()}
            >
              {tab === "output" && (
                <div>
                  <div className="mb-1 text-[11px] font-semibold tracking-wider text-gray-500 uppercase">Select a shot across products</div>
                  {filter === "All" ? (
                    <p className="px-1 text-[11px] text-gray-500">Pick a category above to select one shot for all its products.</p>
                  ) : shotGroups.length ? (
                    <div className="flex flex-wrap gap-1">
                      {[{ shot: "All to review", items: reviewable.map((r) => r.id) }, ...shotGroups].map((g) => (
                        <button
                          key={g.shot}
                          type="button"
                          onClick={() => toggleGroup(g.items)}
                          className={`rounded-full px-2.5 py-1 text-xs ${groupOn(g.items) ? "bg-accent/25 text-white" : "text-gray-400 hover:bg-white/5 hover:text-white"}`}
                        >
                          {g.shot} ({g.items.length})
                        </button>
                      ))}
                    </div>
                  ) : (
                    <p className="px-1 text-[11px] text-gray-500">No {catName(lib, filter).toLowerCase()} images waiting for review.</p>
                  )}
                </div>
              )}
            </WorkspaceMenu>
          )}
        </div>
      </div>

      {tab === "skus" ? (
        <div className="catalog-scroll flex-1 overflow-auto pt-3 pb-4">
          <div className="px-4">
            <table className="w-full text-left text-[13px]">
              <thead className="sticky top-0 z-10 bg-surface text-[11px] text-gray-500">
                <tr className="border-b border-surface-border">
                  <th className="w-9 py-2.5 pl-1">
                    <input
                      type="checkbox"
                      checked={allVisibleSelected}
                      onChange={() => setSelected(allVisibleSelected ? new Set() : new Set(visible.filter((s) => !isBusy(s)).map((s) => s.id)))}
                      className="h-4 w-4 accent-accent"
                      aria-label="Select all"
                    />
                  </th>
                  <th className="py-2.5 font-medium">SKU</th>
                  <th className="py-2.5 font-medium">Category</th>
                  <th className="py-2.5 font-medium">Status</th>
                  <th className="w-20 py-2.5 pr-1 text-right font-medium" />
                </tr>
              </thead>
              <tbody>
                {visible.map((s) => {
                  const busy = isBusy(s);
                  const cells = imagesOf(s);
                  const done = cells.filter(({ im }) => im && im.status !== "generating").length;
                  const ran = hasImages(s);
                  const pill = (
                    <>
                      {busy && <span className="h-2.5 w-2.5 animate-spin rounded-full border-[1.5px] border-current border-t-transparent" />}
                      {busy ? `Running ${done}/${cells.length}` : s.status}
                    </>
                  );
                  const cls = `inline-flex items-center gap-1.5 rounded-full border px-2 py-0.5 text-[11px] ${STATUS_STYLE[s.status]}`;
                  return (
                    <tr key={s.id} className={`border-b border-surface-border/50 ${selected.has(s.id) ? "bg-accent/[0.06]" : "hover:bg-white/[0.02]"}`}>
                      <td className="py-2.5 pl-1">
                        <input
                          type="checkbox"
                          checked={selected.has(s.id)}
                          disabled={busy}
                          onChange={() =>
                            setSelected((prev) => {
                              const n = new Set(prev);
                              if (n.has(s.id)) n.delete(s.id);
                              else n.add(s.id);
                              return n;
                            })
                          }
                          className="h-4 w-4 accent-accent"
                          aria-label={`Select ${s.code}`}
                        />
                      </td>
                      <td className="py-2.5">
                        <div className="flex items-center gap-3">
                          <button
                            type="button"
                            onClick={() => setRawOf(s.id)}
                            title={`${s.code} raw photos`}
                            aria-label={`Show ${s.code} raw photos`}
                            className="relative h-12 w-10 shrink-0 overflow-hidden rounded-md border border-surface-border bg-surface-frame transition hover:border-white/40"
                          >
                            {s.raw_urls[0] && <img src={safeUrl(s.raw_urls[0])} alt="" className="h-full w-full object-cover" />}
                            {s.raw_urls.length > 1 && (
                              <span className="absolute right-0.5 bottom-0.5 rounded bg-black/70 px-1 text-[9px] font-semibold text-white">{s.raw_urls.length}</span>
                            )}
                          </button>
                          <div className="min-w-0">
                            <div className="font-mono text-[11px] text-gray-500">{s.code}</div>
                            <div className="font-medium text-gray-100">{s.name}</div>
                          </div>
                        </div>
                      </td>
                      <td className="py-2.5 text-gray-400">{skuKind(lib, s)}</td>
                      <td className="py-2.5">
                        {ran || busy ? (
                          <button type="button" onClick={() => showInOutput(s.id)} title={`Open ${s.code} in the Gallery`} className={`${cls} transition hover:bg-white/5`}>
                            {pill}
                            <span className="opacity-60">{railIcon(ICON.right, "h-3 w-3")}</span>
                          </button>
                        ) : (
                          <span className={cls}>{pill}</span>
                        )}
                        {gateBlocked(cells.map(({ im }) => im)) && (
                          <div className="mt-1 text-[11px] text-rose-300">Main image hasn&apos;t passed review — fix it first</div>
                        )}
                      </td>
                      <td className="py-2.5 pr-1">
                        <div className="flex justify-end gap-1">
                          {busy && (
                            <IconButton
                              disabled={stopping.has(s.id)}
                              onClick={() => void stopAs(s.id, () => out.stopImages(generatingOf(s), s.code))}
                              title={`Stop ${s.code}`}
                              hint={stopping.has(s.id) ? "Stopping…" : "Stop"}
                              d={ICON.stop}
                            />
                          )}
                          <IconButton
                            disabled={busy || detail.session.stage !== "ready"}
                            onClick={() => void out.runSkus([s.id])}
                            title={`${ran ? "Run again" : "Run"} · ${crWorst(quoteSkus([s], lib))}`}
                            d={ran ? ICON.refresh : ICON.play}
                          />
                          <IconButton disabled={busy} onClick={() => setEditing(s.id)} title="Edit product, model & background" d={ICON.pen} />
                        </div>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        </div>
      ) : tab === "output" ? (
        outputPane
      ) : (
        <div className="flex min-h-0 flex-1 flex-col lg:flex-row">
          <ProcessingChat
            log={log}
            history={history}
            full={!outOpen || !hasOutput}
            next={next}
            picked={picked}
            focus={feedbackOpen}
            p={{
              ...chat,
              onSend: async (t, ids) => {
                if (ids.length) return sendFeedback(t);
                await chat.onSend(t, []);
              },
            }}
            onRemovePick={(id) => setPicked((p) => p.filter((x) => x !== id))}
            onClearPicks={() => {
              setPicked([]);
              setFeedbackOpen(false);
            }}
            onApprovePicked={() => void approve(picked)}
          />
          {outOpen && hasOutput && <SlideIn>{outputPane}</SlideIn>}
        </div>
      )}

      {zoomIndex >= 0 && (
        <Lightbox
          key={rawSku ? `raw:${rawSku.id}` : "images"}
          items={zoomItems}
          index={zoomIndex}
          onIndex={(n) => {
            const it = zoomItems[n];
            if (rawZoom) setRawZoom({ ...rawZoom, at: n });
            else if (it.im) setZoom(shotKey(it.sku.id, it.im.shot));
          }}
          onClose={() => (rawZoom ? setRawZoom(null) : setZoom(null))}
          compare={compare}
          onCompare={setCompare}
          onApprove={(it) => it.im && void approve([it.im.id])}
          onRegenerate={(it, t, r) => it.im && void out.regenerate([it.im.id], t, r)}
          onPick={(id, n) => void out.pickTry(id, n)}
        />
      )}

      {rawOfSku && !rawZoom && (
        <RawPhotos
          sku={rawOfSku}
          onClose={() => setRawOf(null)}
          onPhoto={(at) => openRaw(rawOfSku, at)}
          onGallery={
            hasImages(rawOfSku) || isBusy(rawOfSku)
              ? () => {
                  setRawOf(null);
                  showInOutput(rawOfSku.id);
                }
              : undefined
          }
        />
      )}

      {editingSku && (
        <EditDrawer
          key={editingSku.id}
          sku={editingSku}
          onClose={() => setEditing(null)}
          onPhoto={(at) => openRaw(editingSku, at)}
          onSave={(e) => {
            setEditing(null);
            void out.editSku(editingSku, e);
          }}
        />
      )}
    </section>
  );
}
