// Small presentational pieces of the design system (StatusPill, PageHeader, EmptyState, StatCard, …).
import type { ReactNode } from "react";
import { cn } from "cn";
import { Skeleton } from "@/components/ui/skeleton";

/** Text and background classes per tone. */
const TONES = {
  success: "text-success bg-success/10",
  warning: "text-warning bg-warning/10",
  danger: "text-danger bg-danger/10",
  info: "text-info bg-info/10",
  neutral: "text-muted-foreground bg-muted",
} as const;
/** A StatusPill colour. */
export type Tone = keyof typeof TONES;

/** The tone of each known status; any other status is neutral. */
const STATUS_TONE: Record<string, Tone> = {
  succeeded: "success",
  ready: "success",
  passed: "success",
  running: "info",
  queued: "neutral",
  failed: "danger",
  cancelled: "neutral",
  "needs review": "warning",
};

/** Dot + text, so the colour is never the only signal. */
export function StatusPill({
  status,
  tone,
  children,
  className,
}: {
  status: string;
  tone?: Tone;
  children?: ReactNode;
  className?: string;
}) {
  const t = tone ?? STATUS_TONE[status] ?? "neutral";
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1.5 rounded-full px-2.5 py-0.5 text-xs font-medium whitespace-nowrap",
        TONES[t],
        className,
      )}
    >
      <span
        aria-hidden
        className={cn("size-1.5 rounded-full bg-current", status === "running" && "animate-pulse")}
      />
      {children ?? status}
    </span>
  );
}

/** The page's title row: h1, optional description, `children` under them and action buttons on the right. */
export function PageHeader({
  title,
  description,
  actions,
  children,
}: {
  title: ReactNode;
  description?: ReactNode;
  actions?: ReactNode;
  children?: ReactNode;
}) {
  return (
    <div className="mb-6 flex flex-wrap items-start gap-4">
      <div className="min-w-0 flex-1">
        <h1 className="text-2xl font-bold tracking-tight break-words">{title}</h1>
        {description && <p className="mt-1 text-sm text-muted-foreground">{description}</p>}
        {children}
      </div>
      {actions && <div className="flex flex-wrap items-center gap-2">{actions}</div>}
    </div>
  );
}

/** Dashed placeholder for a list with nothing in it, with an optional icon and action. */
export function EmptyState({
  icon,
  title,
  children,
  action,
}: {
  icon?: ReactNode;
  title: string;
  children?: ReactNode;
  action?: ReactNode;
}) {
  return (
    <div className="flex flex-col items-center gap-2 rounded-xl border border-dashed px-6 py-12 text-center">
      {icon && (
        <div className="text-muted-foreground [&_svg]:size-8" aria-hidden>
          {icon}
        </div>
      )}
      <p className="font-medium">{title}</p>
      {children && <p className="max-w-md text-sm text-muted-foreground">{children}</p>}
      {action && <div className="mt-2">{action}</div>}
    </div>
  );
}

/** A labelled number with an optional hint. */
export function StatCard({
  label,
  value,
  hint,
}: {
  label: string;
  value: ReactNode;
  hint?: ReactNode;
}) {
  return (
    <div className="rounded-xl border bg-card p-4 shadow-card">
      <p className="text-xs text-muted-foreground">{label}</p>
      <p className="tabular mt-1 text-xl font-semibold">{value}</p>
      {hint && <p className="mt-0.5 text-xs text-muted-foreground">{hint}</p>}
    </div>
  );
}

/** The "Beta" tag beside the product name: the platform is in a closed beta. */
export function BetaBadge({ className }: { className?: string }) {
  return (
    <span
      className={cn(
        "rounded-full border border-amber-500/40 bg-amber-500/10 px-1.5 py-px text-[10px] font-semibold tracking-wider text-amber-500 uppercase",
        className,
      )}
    >
      Beta
    </span>
  );
}

/** Error banner (role="alert") with a "Try again" button when `onRetry` is given. */
export function ErrorNote({ children, onRetry }: { children: ReactNode; onRetry?: () => void }) {
  return (
    <div
      role="alert"
      className="flex flex-wrap items-center gap-3 rounded-lg border border-danger/30 bg-danger/5 px-4 py-3 text-sm text-danger"
    >
      <span className="flex-1">{children}</span>
      {onRetry && (
        <button
          type="button"
          onClick={onRetry}
          className="font-medium underline underline-offset-4"
        >
          Try again
        </button>
      )}
    </div>
  );
}

/** Loading placeholder for lists and tables. */
export function SkeletonRows({ rows = 5 }: { rows?: number }) {
  return (
    <div className="space-y-2" aria-busy="true" aria-label="Loading">
      {Array.from({ length: rows }, (_, i) => (
        <Skeleton key={i} className="h-14 w-full rounded-lg" />
      ))}
    </div>
  );
}

/** Form row: label, control and a hint or error. An error hides the hint; their ids are `<htmlFor>-hint` and
 * `<htmlFor>-error`, for aria-describedby. */
export function Field({
  label,
  htmlFor,
  hint,
  error,
  children,
}: {
  label: ReactNode;
  htmlFor?: string;
  hint?: ReactNode;
  error?: string;
  children: ReactNode;
}) {
  return (
    <div className="grid gap-1.5">
      <label htmlFor={htmlFor} className="text-sm font-medium">
        {label}
      </label>
      {children}
      {hint && !error && (
        <p className="text-xs text-muted-foreground" id={htmlFor && `${htmlFor}-hint`}>
          {hint}
        </p>
      )}
      {error && (
        <p className="text-xs text-danger" id={htmlFor && `${htmlFor}-error`}>
          {error}
        </p>
      )}
    </div>
  );
}

/** The standard bordered panel. */
export const Card = ({ className, children }: { className?: string; children: ReactNode }) => (
  <section
    className={cn("rounded-xl border bg-card p-5 text-card-foreground shadow-card", className)}
  >
    {children}
  </section>
);
