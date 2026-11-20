"use client";
// The visual picker (plan 05-ui.md 8.4; docs/mock/Elements.dc.html, LoraPicker.dc.html, Looks.dc.html): one dialog
// body for elements, LoRAs and looks: the info and the kind's "new / upload" action, search, group or source filters,
// a thumbnail grid (arrow keys move between items), greyed items with the reason, and a footer. Each picker is a thin
// configuration of it; `Tile` is the thumbnail card the LoRA and look grids share.
import type { ReactNode } from "react";
import { ErrorNote, Info } from "@/components/kit";
import { safeUrl } from "@/lib/format";

/** One row of filter buttons: tabs (`role=tab`, a group) or toggles (`aria-pressed`, a source). */
export type Filter<V extends string> = {
  label: string;
  kind: "tab" | "press";
  options: readonly (readonly [V, string])[];
  value: V;
  onChange: (v: V) => void;
};

const chip = (on: boolean) =>
  `h-8 rounded-full px-3 text-[12px] ${on ? "bg-t1 font-semibold text-panel" : "bg-s3 font-medium"}`;

function Filters<V extends string>({ f }: { f: Filter<V> }) {
  const tab = f.kind === "tab";
  return (
    <div
      role={tab ? "tablist" : "group"}
      aria-label={f.label}
      className={`flex gap-1.5 ${tab ? "flex-wrap" : ""}`}
    >
      {f.options.map(([v, text]) => (
        <button
          key={text}
          type="button"
          {...(tab
            ? { role: "tab", "aria-selected": f.value === v }
            : { "aria-pressed": f.value === v })}
          className={chip(f.value === v)}
          onClick={() => f.onChange(v)}
        >
          {text}
        </button>
      ))}
    </div>
  );
}

/** Arrow keys move focus between the grid's items (`data-pick` buttons), skipping disabled ones, Home and End to the
 * ends; up and down go a row (the columns counted over every item, greyed ones too, laid out on the first one's line). */
function gridKeys(e: React.KeyboardEvent<HTMLUListElement>) {
  const all = [...e.currentTarget.querySelectorAll<HTMLButtonElement>("[data-pick]")];
  const i = all.indexOf(document.activeElement as HTMLButtonElement);
  if (i < 0) return;
  const cols = Math.max(1, all.filter((b) => b.offsetTop === all[0].offsetTop).length);
  const [from, step] = {
    ArrowRight: [i + 1, 1],
    ArrowLeft: [i - 1, -1],
    ArrowDown: [i + cols, cols],
    ArrowUp: [i - cols, -cols],
    Home: [0, 1],
    End: [all.length - 1, -1],
  }[e.key] ?? [-1, 0];
  if (!step) return;
  let to = from;
  while (all[to]?.disabled) to += step;
  if (!all[to]) return;
  e.preventDefault();
  all[to].focus();
}

export function VisualPicker<A extends string, B extends string>({
  head,
  info,
  action,
  search,
  filters = [],
  error,
  status,
  listLabel,
  grid,
  children,
  footer,
}: {
  /** set when the picker sits in the page, not a dialog (the empty Create screen, docs/mock/Direct.dc.html): its own
   * title row in place of the dialog's info and action, and no scrolling of its own */
  head?: ReactNode;
  info: ReactNode;
  /** the kind's "New element" / "Upload .safetensors" / "New look" button (and what it needs, a file input) */
  action?: ReactNode;
  search?: {
    id: string;
    label: string;
    placeholder: string;
    value: string;
    onChange: (q: string) => void;
    className: string;
  };
  filters?: [Filter<A>?, Filter<B>?];
  error?: string;
  /** loading, the read's error, or an empty list's words, shown above the grid */
  status?: ReactNode;
  listLabel: string;
  /** the grid's columns */
  grid: string;
  /** the items, each an `<li>` whose main button has `data-pick` */
  children: ReactNode;
  footer: ReactNode;
}) {
  return (
    <div className={`flex flex-col gap-3.5 ${head ? "" : "max-h-[76dvh]"}`}>
      {head ?? (
        <div className="-mt-11 flex items-center justify-end gap-2">
          <Info>{info}</Info>
          {action}
        </div>
      )}
      {(search || filters[0]) && (
        <div className="flex flex-wrap items-center gap-2">
          {search && (
            <>
              <label htmlFor={search.id} className="sr-only">
                {search.label}
              </label>
              <input
                id={search.id}
                type="search"
                placeholder={search.placeholder}
                value={search.value}
                maxLength={80}
                autoFocus
                onChange={(e) => search.onChange(e.target.value)}
                // the picker may sit inside the composer's form: Enter must not submit it
                onKeyDown={(e) => e.key === "Enter" && e.preventDefault()}
                className={`h-9 rounded-xl border border-s5 bg-s1 px-3 text-t1 ${search.className}`}
              />
            </>
          )}
          {filters[0] && <Filters f={filters[0]} />}
          {filters[1] && <Filters f={filters[1]} />}
        </div>
      )}
      {error && <ErrorNote>{error}</ErrorNote>}
      <div className={`p-0.5 ${head ? "" : "min-h-32 overflow-y-auto"}`}>
        {status}
        <ul
          aria-label={listLabel}
          onKeyDown={gridKeys}
          className={`m-0 grid list-none p-0 ${grid}`}
        >
          {children}
        </ul>
      </div>
      {footer}
    </div>
  );
}

/** The thumbnail of a tile: the image, else a dashed box with `empty`; `n` (from 0) badges a pick. */
export function Pic({ thumb, empty, n = -1 }: { thumb: string | null; empty: string; n?: number }) {
  const src = safeUrl(thumb);
  return (
    <span className="relative block w-full">
      {src ? (
        <img
          src={src}
          alt=""
          loading="lazy"
          className={`block aspect-[16/10] w-full rounded-[10px] bg-s1 object-cover ${n >= 0 ? "outline-2 -outline-offset-2 outline-a1" : ""}`}
        />
      ) : (
        <i
          className={`flex aspect-[16/10] items-center justify-center rounded-[10px] bg-s1 text-[11px] text-t2 not-italic ${n >= 0 ? "border-2 border-a1" : "border border-dashed border-dash"}`}
        >
          {empty}
        </i>
      )}
      {n >= 0 && (
        <span className="absolute top-1.5 left-1.5 grid size-5 place-items-center rounded-full bg-a1 text-[11px] font-bold text-on1">
          {n + 1}
        </span>
      )}
    </span>
  );
}

/** A thumbnail card's button: the picture, the name, a small line (the reason when greyed). */
export function Tile({
  pic,
  name,
  line,
  reason,
  pressed,
  disabled,
  onClick,
}: {
  pic: ReactNode;
  name: string;
  line: string;
  /** why it cannot be picked: greys it and is its title (the line says it too) */
  reason?: string | null;
  pressed: boolean;
  disabled?: boolean;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      data-pick
      aria-pressed={pressed}
      // a pressed item stays enabled, greyed or not, so it can always be unpicked
      disabled={!pressed && (!!reason || disabled)}
      title={reason ?? name}
      onClick={onClick}
      className={`flex w-full flex-col gap-1.5 text-left disabled:cursor-not-allowed disabled:opacity-50 ${reason ? "opacity-50" : ""}`}
    >
      {pic}
      <b className="truncate text-[12px] font-semibold">{name}</b>
      <small
        className={`mono text-[10px] break-words text-t2 ${reason ? "line-clamp-3" : "truncate"}`}
      >
        {line}
      </small>
    </button>
  );
}
