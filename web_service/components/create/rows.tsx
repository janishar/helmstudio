"use client";
// The rows editor's per-item fields (plan 05-ui.md 8.4; docs/mock/ImageToVideo.dc.html): each item field that is a
// param schema drawn from it (a number or a time on the clip as a slider with its readout, a line of text, an enum
// with a choice; a one-value enum is not drawn), the rows without an image (prompt beats) as cards with Add, and the
// clip track that shows where the keyframes sit. Generic: nothing of one family.
import { useRef } from "react";
import { X } from "lucide-react";
import { Info } from "@/components/kit";
import { safeUrl } from "@/lib/format";
import { clampStrength } from "@/lib/loras";
import { move } from "@/lib/refs";
import { secondsText } from "@/lib/media";
import { useTakes } from "@/lib/take-cache";
import { choosable, frameAt, itemSpecs, itemValue, positionOf, type Clip } from "@/lib/video";
import { pretty, type Field, type Raw, type Update } from "@/lib/workflow";
import type { S } from "@/lib/api";

type Row = S<"RowItem">;
type Spec = ReturnType<typeof itemSpecs>[number][1];

const places = (step?: number) => (step ? (String(step).split(".")[1]?.length ?? 0) : 2);
/** A number as its readout shows it: on its step's decimals ("1.00"), " s" for a time. */
const numText = (k: string, s: Spec, v: number) =>
  `${v.toFixed(places(s.step))}${k === "seconds" ? " s" : ""}`;

/** One row's fields other than `skip` (the Image, the header line's text). */
export function ItemFields({
  p,
  row,
  id,
  clip,
  skip = [],
  onRow,
}: {
  p: S<"ParamSchema">;
  row: Row;
  id: string;
  clip: Clip | null;
  skip?: string[];
  onRow: (patch: Row) => void;
}) {
  const pos = positionOf(p);
  return itemSpecs(p)
    .filter(([k, s]) => !skip.includes(k) && (s.type !== "enum" || choosable(s)))
    // a choice goes under the sliders (docs/mock/LtxFeatures.dc.html frame 4): the item's keys reach the page sorted
    .sort(([, a], [, b]) => Number(a.type === "enum") - Number(b.type === "enum"))
    .map(([k, s]) => {
      const fid = `${id}-${k}`;
      const label = (
        <span className="flex items-center">
          {s.label ?? k}
          {s.help && <Info>{s.help}</Info>}
        </span>
      );
      const v = itemValue(row, [k, s]);
      // an enum: its label, then a segmented choice on the same line (docs/mock/LtxFeatures.dc.html frame 4)
      if (s.type === "enum")
        return (
          <div key={k} className="flex items-center justify-between gap-2">
            <span className="text-[12px] text-t2">{label}</span>
            <div className="seg" role="group" aria-label={s.label ?? k}>
              {s.enum?.map((o) => (
                <button
                  key={o}
                  type="button"
                  aria-pressed={v === o}
                  onClick={() => onRow({ [k]: o })}
                >
                  {pretty(o)}
                </button>
              ))}
            </div>
          </div>
        );
      if (s.type === "string" || s.type === "text")
        return (
          <div key={k} className="flex flex-col gap-1.5">
            <label htmlFor={fid} className="text-[12px] text-t2">
              {label}
            </label>
            <input
              id={fid}
              className="in"
              maxLength={s.max}
              value={String(v ?? "")}
              onChange={(e) => onRow({ [k]: e.target.value })}
            />
          </div>
        );
      // a time on the clip: a notch per frame, read as the frame and its time
      const n = Number(v);
      const onClip = pos?.[0] === k && clip;
      const frame = onClip ? frameAt(n, clip) : 0;
      const text = onClip ? `frame ${frame} · ${secondsText(frame / clip.fps)}` : numText(k, s, n);
      return (
        <div key={k} className="flex flex-col gap-1">
          <label htmlFor={fid} className="flex justify-between gap-2 text-[12px] text-t2">
            {label}
            <span className="mono">{text}</span>
          </label>
          <input
            id={fid}
            type="range"
            min={onClip ? 0 : s.min}
            max={onClip ? clip.last : s.max}
            step={onClip ? 1 : (s.step ?? "any")}
            value={onClip ? frame : n}
            aria-valuetext={text}
            onChange={(e) => {
              const x = Number(e.target.value);
              onRow({ [k]: onClip ? x / clip.fps : clampStrength(x, s) });
            }}
            className={`w-full ${onClip ? "accent-a2" : "accent-a1"}`}
          />
        </div>
      );
    });
}

/** Where the keyframes sit on the clip: a marker per row with its picture, at its frame. */
export function ClipTrack({
  p,
  rows,
  imageKey,
  clip,
}: {
  p: S<"ParamSchema">;
  rows: Row[];
  imageKey: string;
  clip: Clip;
}) {
  const pos = positionOf(p)!;
  const ids = rows.map((r) => (r[imageKey] as { take_id?: string } | undefined)?.take_id ?? "");
  const get = useTakes(ids.filter(Boolean));
  const at = rows.map((r) => frameAt(Number(itemValue(r, pos)), clip));
  return (
    <div
      role="img"
      aria-label={`Frames on the clip: ${at.map((f) => secondsText(f / clip.fps)).join(", ")}`}
      className="relative h-10 rounded-[10px] border border-s3 bg-s1"
    >
      {at.map((f, i) => {
        const src = safeUrl(get(ids[i])?.thumb_url);
        return (
          <span
            key={i}
            className="absolute top-[5px] h-7 w-11 overflow-hidden rounded-md border-2 border-a2 bg-s3"
            // ends inset by half a marker, so the first and last frames stay inside the track
            style={{
              left: `calc(${(f / Math.max(clip.last, 1)) * 100}% - ${(f / Math.max(clip.last, 1)) * 44}px)`,
            }}
          >
            {src && <img src={src} alt="" className="size-full object-cover" />}
          </span>
        );
      })}
    </div>
  );
}

/** A rows param whose item has no Image or Lora (prompt beats): a card per row, its first text field on the header
 * line with Remove, its other fields below; Add appends a row of defaults. Reorder by drag or Alt + arrows. */
export function PlainRows({
  f,
  raw,
  error,
  clip,
  onChange,
}: {
  f: Field;
  raw: Raw | undefined;
  error?: string;
  clip: Clip | null;
  onChange: (key: string, u: Update) => void;
}) {
  const p = f.param;
  const rows = Array.isArray(raw) ? raw : Array.isArray(f.initial) ? f.initial : [];
  const specs = itemSpecs(p);
  const head = specs.find(([, s]) => s.type === "string" || s.type === "text");
  const name = (head?.[1].label ?? p.label ?? p.name).toLowerCase();
  const list = useRef<HTMLOListElement>(null);
  const dragFrom = useRef<number | null>(null);
  const set = (next: Row[]) => onChange(f.key, next);
  const fresh = (): Row => Object.fromEntries(specs.map(([k, s]) => [k, s.default as Row[string]]));
  return (
    <>
      {rows.length > 0 && (
        <ol
          ref={list}
          aria-label={`${p.label ?? p.name}, in order`}
          className="m-0 flex list-none flex-col gap-2 p-0"
        >
          {rows.map((r, i) => {
            const id = `${f.key.replace(/\W/g, "-")}-${i}`;
            return (
              <li
                key={i}
                tabIndex={-1}
                draggable
                aria-keyshortcuts="Alt+ArrowUp Alt+ArrowDown"
                onDragStart={(e) => {
                  dragFrom.current = i;
                  e.dataTransfer.effectAllowed = "move";
                }}
                onDragEnd={() => (dragFrom.current = null)}
                onDragOver={(e) => dragFrom.current !== null && e.preventDefault()}
                onDrop={(e) => {
                  if (dragFrom.current === null) return;
                  e.preventDefault();
                  set(move(rows, dragFrom.current, i));
                  dragFrom.current = null;
                }}
                onKeyDown={(e) => {
                  const d =
                    e.altKey && e.key === "ArrowUp"
                      ? -1
                      : e.altKey && e.key === "ArrowDown"
                        ? 1
                        : 0;
                  if (!d) return;
                  e.preventDefault();
                  set(move(rows, i, i + d));
                  requestAnimationFrame(() =>
                    list.current
                      ?.querySelectorAll<HTMLElement>(":scope > li")
                      [Math.max(0, Math.min(rows.length - 1, i + d))]?.querySelector("input")
                      ?.focus(),
                  );
                }}
                className="flex flex-col gap-2 rounded-[10px] border border-s3 bg-s1 px-3 py-2.5"
              >
                <div className="flex items-center gap-2">
                  {head && (
                    <>
                      <label htmlFor={`${id}-${head[0]}`} className="sr-only">
                        {head[1].label ?? head[0]} {i + 1}
                      </label>
                      <input
                        id={`${id}-${head[0]}`}
                        className="h-[30px] min-w-0 grow rounded-lg border-0 bg-s3 px-2 text-t1"
                        maxLength={head[1].max}
                        value={String(r[head[0]] ?? "")}
                        placeholder={head[1].label ?? head[0]}
                        onChange={(e) =>
                          set(
                            rows.map((x, j) => (j === i ? { ...x, [head[0]]: e.target.value } : x)),
                          )
                        }
                      />
                    </>
                  )}
                  <button
                    type="button"
                    aria-label={`Remove ${name} ${i + 1}`}
                    onClick={() => set(rows.filter((_, j) => j !== i))}
                    className="ml-auto grid size-[22px] shrink-0 place-items-center rounded-lg bg-s3 text-t2 hover:text-t1"
                  >
                    <X size={12} aria-hidden />
                  </button>
                </div>
                <ItemFields
                  p={p}
                  row={r}
                  id={id}
                  clip={clip}
                  skip={head ? [head[0]] : []}
                  onRow={(patch) => set(rows.map((x, j) => (j === i ? { ...x, ...patch } : x)))}
                />
              </li>
            );
          })}
        </ol>
      )}
      {rows.length < (p.max ?? 100) && (
        <button
          type="button"
          onClick={() => {
            set([...rows, fresh()]);
            // straight into the new row's text
            requestAnimationFrame(() =>
              list.current?.querySelector<HTMLElement>(":scope > li:last-child input")?.focus(),
            );
          }}
          className="h-10 rounded-[10px] border border-dashed border-dash bg-transparent text-[13px] text-t1"
        >
          Add {name}
        </button>
      )}
      {error && <span className="text-[12px] text-bad">{error}</span>}
    </>
  );
}
