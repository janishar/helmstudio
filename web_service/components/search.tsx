"use client";
// SearchField (docs/mock/Search.dc.html frame 0, owner 2026-11-27 "approved"): one field for every list that can grow.
// The Field look with a search glyph, the match count, a spinner while the server is asked, a clear ×, and the / hint.
// useSearch filters the loaded rows at once (every word, any order, case-insensitive: contracts/README.md "Search in
// lists") and, with a `server`, asks it 300 ms after the last key; matches not loaded come back as `older`.
import { Fragment, useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { LoaderCircle, Search, X } from "lucide-react";
import { message } from "@/lib/api";

/** The query's words, lower-cased (blank: none). */
export const words = (q: string) => q.toLowerCase().split(/\s+/).filter(Boolean);

/** Whether every word of `q` occurs in one of `fields` (a blank query matches everything). */
export function matches(q: string, fields: (string | null | undefined)[]): boolean {
  const ws = words(q);
  if (!ws.length) return true;
  const hay = fields.filter(Boolean).map((f) => f!.toLowerCase());
  return ws.every((w) => hay.some((h) => h.includes(w)));
}

/** `text` with each word of `q` it holds marked. */
export function Marked({ text, q }: { text: string; q: string }) {
  const ws = words(q).map((w) => w.replace(/[.*+?^${}()|[\]\\]/g, "\\$&"));
  if (!ws.length || !text) return <>{text}</>;
  const parts = text.split(new RegExp(`(${ws.join("|")})`, "gi"));
  return (
    <>
      {parts.map((p, i) => (i % 2 ? <mark key={i}>{p}</mark> : <Fragment key={i}>{p}</Fragment>))}
    </>
  );
}

// ------------------------------------------------------------------ the / key

const fields = new Set<HTMLInputElement>();
const openers: (() => void)[] = [];
const visible = (el: HTMLElement) => el.getClientRects().length > 0;

function onSlash(e: KeyboardEvent) {
  if (e.key !== "/" || e.metaKey || e.ctrlKey || e.altKey || e.defaultPrevented) return;
  const t = e.target as HTMLElement | null;
  if (t?.closest?.("input, textarea, select, [contenteditable]:not([contenteditable=false])"))
    return; // typing in a field
  // the open popover's or dialog's field first, then the screen's closed list (its opener), then a list in view
  const shown = [...fields].filter(visible);
  const f = shown.filter((x) => x.closest("dialog[open], [popover]")).at(-1);
  const opener = openers.at(-1);
  if (!f && opener) {
    e.preventDefault();
    return opener();
  }
  const g = f ?? shown.at(-1);
  if (!g) return;
  e.preventDefault();
  g.focus();
  g.select();
}

function followSlash<T>(set: { add: (x: T) => void }, x: T, drop: () => void) {
  if (!fields.size && !openers.length) window.addEventListener("keydown", onSlash);
  set.add(x);
  return () => {
    drop();
    if (!fields.size && !openers.length) window.removeEventListener("keydown", onSlash);
  };
}

/** What / does on a screen whose list is closed (Create's sessions, Magic's boards): open it, field focused. */
export function useSlashOpens(open: () => void) {
  const ref = useRef(open);
  useEffect(() => {
    ref.current = open;
  });
  useEffect(() => {
    const fn = () => ref.current();
    return followSlash({ add: (f: () => void) => openers.push(f) }, fn, () =>
      openers.splice(openers.indexOf(fn), 1),
    );
  }, []);
}

// ------------------------------------------------------------------ the field

export type FieldProps = {
  value: string;
  onChange: (q: string) => void;
  /** "Search sessions": the placeholder and the label */
  label: string;
  /** the matches so far (shown while something is typed) */
  count?: number;
  /** the server is being asked */
  busy?: boolean;
  /** Enter: open or pick the first match */
  onEnter?: () => void;
  /** ↑ / ↓ move the ring */
  onMove?: (by: 1 | -1) => void;
  /** Esc on an empty field: close the popover or dialog (a native one closes itself when this is left out) */
  onClose?: () => void;
  autoFocus?: boolean;
  className?: string;
  id?: string;
};

/** The SearchField: glyph, the input, the count, a spinner, × (once typed) and the / hint (empty, not focused). */
export function SearchField({
  value,
  onChange,
  label,
  count,
  busy,
  onEnter,
  onMove,
  onClose,
  autoFocus,
  className = "",
  id,
}: FieldProps) {
  const ref = useRef<HTMLInputElement>(null);
  useEffect(() => {
    const el = ref.current!;
    return followSlash(fields, el, () => fields.delete(el));
  }, []);
  // after the dialog it sits in has opened (showModal focuses its first button)
  useEffect(() => {
    if (!autoFocus) return;
    const f = requestAnimationFrame(() => ref.current?.focus());
    return () => cancelAnimationFrame(f);
  }, [autoFocus]);
  return (
    <div role="search" className={`sf ${className}`}>
      <Search size={16} aria-hidden className="g" />
      <input
        ref={ref}
        id={id}
        type="search"
        aria-label={label}
        placeholder={label}
        value={value}
        maxLength={200}
        autoComplete="off"
        onChange={(e) => onChange(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Escape") {
            if (value) {
              e.preventDefault(); // clears; the popover or dialog stays
              e.stopPropagation();
              onChange("");
            } else if (onClose) {
              e.preventDefault();
              e.stopPropagation();
              onClose();
            }
          } else if (e.key === "Enter") {
            e.preventDefault(); // a search never submits the form it sits in (the composer's)
            if (!onEnter) return;
            e.stopPropagation();
            onEnter();
          } else if ((e.key === "ArrowDown" || e.key === "ArrowUp") && onMove) {
            e.preventDefault(); // focus stays in the field (a menu's own arrows would move it)
            e.stopPropagation();
            onMove(e.key === "ArrowDown" ? 1 : -1);
          }
        }}
      />
      {value && count !== undefined && <span className="sfh">{count}</span>}
      {busy && <LoaderCircle size={14} aria-label="Searching" className="spin text-t2" />}
      {value ? (
        <button
          type="button"
          className="sfx"
          aria-label="Clear search"
          title="Clear (Esc)"
          onClick={() => (onChange(""), ref.current?.focus())}
        >
          <X size={14} strokeWidth={2.2} aria-hidden />
        </button>
      ) : (
        <kbd className="k" title="Press / to search">
          /
        </kbd>
      )}
    </div>
  );
}

/** "No sessions match." and Clear search. */
export function NoMatch({ what, onClear }: { what: string; onClear: () => void }) {
  return (
    <div className="sf-none">
      <p>No {what} match.</p>
      <button type="button" className="chip h-7 rounded-lg px-2.5 text-[12px]" onClick={onClear}>
        Clear search
      </button>
    </div>
  );
}

/** "From older sessions": the server's matches not loaded, its spinner line, and Load more matches. */
export function Older({
  heading,
  what,
  searching,
  more,
  onMore,
  children,
}: {
  /** "From older sessions", "From your library" */
  heading: string;
  /** "older sessions" in "Searching older sessions…" */
  what: string;
  searching: boolean;
  more: boolean;
  onMore: () => void;
  children?: ReactNode;
}) {
  return (
    <div className="sf-older">
      <span className="oh">{heading}</span>
      {children}
      {searching ? (
        <div className="srch" role="status">
          <LoaderCircle size={14} aria-hidden className="spin" />
          Searching {what}…
        </div>
      ) : (
        more && (
          <button
            type="button"
            className="chip h-8 self-start rounded-lg text-[12px]"
            onClick={onMore}
          >
            Load more matches
          </button>
        )
      )}
    </div>
  );
}

// ------------------------------------------------------------------ the hook

/** One page of a server search: its items and the cursor for the next. */
export type Found<T> = { items: T[]; next: string | null };

export function useSearch<T>({
  items,
  text,
  id,
  server,
  scope = "",
  more = false,
  onPick,
  min = 8,
}: {
  /** the rows loaded (null: still loading), already narrowed by any chip the list has */
  items: T[] | null | undefined;
  /** the row's searched fields */
  text: (t: T) => (string | null | undefined)[];
  id: (t: T) => string;
  /** the server's search for a query and a cursor: its matches not in `items` come back as `older` */
  server?: (q: string, cursor: string | null) => Promise<Found<T>>;
  /** what else narrows the server's search (a state chip): a change asks again, and asks even with no words */
  scope?: string;
  /** the list has more pages than loaded (a cursor): the field shows */
  more?: boolean;
  /** Enter on the ringed row */
  onPick?: (t: T) => void;
  /** the field shows past this many rows */
  min?: number;
}) {
  const [q, setQState] = useState("");
  const [found, setFound] = useState<{ key: string; items: T[]; next: string | null } | null>(null);
  // what the server is being asked for (a newer query makes it stale)
  const [asking, setAsking] = useState<string | null>(null);
  const [error, setError] = useState("");
  const [active, setActive] = useState(-1);
  const seq = useRef(0);
  const serverRef = useRef(server);
  useEffect(() => {
    serverRef.current = server;
  });
  const query = q.trim();
  const key = `${scope}\n${query}`;
  const asked = !!(query || scope);

  const shown = useMemo(
    () => (items ?? []).filter((t) => matches(q, text(t))),
    // eslint-disable-next-line react-hooks/exhaustive-deps -- text is a field picker, read with the rows
    [items, q],
  );
  const older = useMemo(() => {
    if (!found || found.key !== key) return [];
    const have = new Set((items ?? []).map(id));
    return found.items.filter((t) => !have.has(id(t)));
    // eslint-disable-next-line react-hooks/exhaustive-deps -- id is a field picker
  }, [found, items, key]);
  const rows = useMemo(() => [...shown, ...older], [shown, older]);

  const ask = (cursor: string | null) => {
    const n = ++seq.current;
    const k = key;
    setAsking(k);
    setError("");
    serverRef.current!(query, cursor)
      .then((p) => {
        if (n !== seq.current) return; // an answer for an older query
        setFound((f) => ({
          key: k,
          items: cursor && f?.key === k ? [...f.items, ...p.items] : p.items,
          next: p.next,
        }));
      })
      .catch((e) => n === seq.current && setError(message(e)))
      .finally(() => n === seq.current && setAsking(null));
  };

  // the server 300 ms after the last key; a new query drops the answer in flight (a blank one asks nothing)
  const searching = asked && asking === key;
  useEffect(() => {
    seq.current++;
    if (!asked || !serverRef.current) return;
    const t = setTimeout(() => ask(null), 300);
    return () => clearTimeout(t);
    // eslint-disable-next-line react-hooks/exhaustive-deps -- ask reads this render's key
  }, [key]);

  const setQ = useCallback((v: string) => {
    setQState(v);
    setActive(v.trim() ? 0 : -1);
  }, []);
  const count = rows.length;
  const show = !!q || (items?.length ?? 0) > min || more;
  return {
    q,
    setQ,
    show,
    shown,
    older,
    rows,
    active,
    setActive,
    count,
    searching,
    error,
    /** the server's matches have a next page */
    olderNext: !!found?.next && found.key === key,
    moreOlder: () => found?.next && found.key === key && ask(found.next),
    /** the SearchField's props (add label, onClose) */
    field: {
      value: q,
      onChange: setQ,
      count,
      busy: searching,
      onMove: (by: 1 | -1) =>
        count && setActive((a) => (a < 0 ? 0 : Math.min(count - 1, Math.max(0, a + by)))),
      onEnter: () => {
        const t = rows[Math.max(active, 0)];
        if (t && onPick && (query || active >= 0)) onPick(t);
      },
    },
  };
}
