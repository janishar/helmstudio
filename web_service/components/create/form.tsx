"use client";
// Form controls rendered from node schemas: text params go in the composer, the rest in the inspector, grouped
// generically (the canvas node, label-over-value tiles, switches, the rest); the composer's chips summarise them.
import { useEffect, useRef } from "react";
import { ChevronRight, Dices } from "lucide-react";
import { Info } from "@/components/kit";
import {
  ASPECTS,
  MEGAPIXELS,
  aspectOf,
  display,
  megapixelsOf,
  pretty,
  sections,
  sizeFor,
  toRaw,
  type Field,
  type Raw,
} from "@/lib/workflow";

/** Props shared by the form parts: the fields, what the controls hold, and the errors by field key. */
export type FormProps = {
  fields: Field[];
  raws: Record<string, Raw>;
  errors: Record<string, string>;
  onChange: (key: string, raw: Raw) => void;
};

/** The control's id for a field key. */
export const idOf = (key: string) => "f-" + key.replace(/[^a-z0-9_]/gi, "-");
/** What a control holds now. */
const rawOf = (f: Field, raws: Record<string, Raw>) => raws[f.key] ?? toRaw(f.initial, f.param);

/** The field's error line, tied to its control by id. */
function FieldError({ k, errors }: { k: string; errors: Record<string, string> }) {
  return errors[k] ? (
    <span id={idOf(k) + "-err"} className="text-[12px] text-bad">
      {errors[k]}
    </span>
  ) : null;
}

/** The multi-line text params (the prompt), borderless inside the composer, as in the mock; ⌘/Ctrl+Enter submits the form. */
export function TextFields({ fields, raws, errors, onChange }: FormProps) {
  return fields
    .filter((f) => f.param.type === "text")
    .map((f) => (
      <div key={f.key} className="flex min-w-0 flex-1 flex-col gap-1">
        <label htmlFor={idOf(f.key)} className="sr-only">
          {f.param.label ?? f.param.name}
        </label>
        <textarea
          id={idOf(f.key)}
          maxLength={f.param.max}
          placeholder={
            f.param.name === "text"
              ? "Describe what you want to see"
              : (f.param.label ?? f.param.name)
          }
          rows={2}
          className="w-full resize-none border-0 bg-transparent text-[15px] leading-[22px] text-t1 outline-0 placeholder:text-t2"
          value={String(rawOf(f, raws))}
          aria-invalid={!!errors[f.key] || undefined}
          aria-describedby={errors[f.key] ? idOf(f.key) + "-err" : undefined}
          onChange={(e) => onChange(f.key, e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && (e.metaKey || e.ctrlKey))
              e.currentTarget.form?.requestSubmit();
          }}
        />
        <FieldError k={f.key} errors={errors} />
      </div>
    ));
}

/** The canvas node's width and height params and their values now, when it has both. `auto`: the workflow's own. */
export function canvasOf(fs: Field[], raws: Record<string, Raw>) {
  const find = (name: string) =>
    fs.find((f) => f.canvas && f.param.name === name && f.param.type === "int");
  const width = find("width");
  const height = find("height");
  if (!width || !height) return null;
  const w = Number(rawOf(width, raws));
  const h = Number(rawOf(height, raws));
  return { width, height, w, h, auto: w === width.initial && h === height.initial };
}

/** "16:9 · 4 MP", else "1000 × 700 · 0.7 MP". */
const sizeText = (w: number, h: number) =>
  `${aspectOf(w, h) ?? `${w} × ${h}`} · ${+((w * h) / 1e6).toFixed(1)} MP`;

/** Which inspector editor is open (a field key), and how to open or close one. */
type OpenProps = { open: string | null; setOpen: (key: string | null) => void };

/** The params other than the prompt: Canvas, Quality (tiles), Options (switches), More. */
export function Inspector({
  fields,
  raws,
  errors,
  onChange,
  open,
  setOpen,
}: FormProps & OpenProps) {
  const s = sections(fields);
  const c = canvasOf(fields, raws);
  const extra = s.canvas.filter((f) => f !== c?.width && f !== c?.height);
  if (!s.canvas.length && !s.tiles.length && !s.switches.length && !s.more.length)
    return <p className="text-[13px] text-t2">Nothing to set.</p>;
  const control = (f: Field) => {
    const raw = rawOf(f, raws);
    if (f.param.type === "bool") return <Switch key={f.key} f={f} raw={raw} onChange={onChange} />;
    if (f.param.type === "string")
      return <Line key={f.key} f={f} raw={raw} errors={errors} onChange={onChange} />;
    return (
      <Tile
        key={f.key}
        f={f}
        raw={raw}
        error={errors[f.key]}
        open={open === f.key}
        setOpen={setOpen}
        onChange={onChange}
      />
    );
  };
  const section = (
    id: string,
    title: string,
    fs: Field[],
    head?: React.ReactNode,
    help?: string,
    gap = "gap-2.5",
  ) =>
    (fs.length > 0 || head) && (
      <section aria-labelledby={id} className={`flex flex-col ${gap}`}>
        <h2 id={id} className="heading flex items-center">
          {title}
          {help && <Info>{help}</Info>}
        </h2>
        {head}
        {fs.length > 0 && (
          <div className="grid grid-flow-row-dense grid-cols-2 gap-2">{fs.map(control)}</div>
        )}
      </section>
    );
  return (
    <>
      {section(
        "sec-canvas",
        "Canvas",
        extra,
        c && <CanvasPicker c={c} errors={errors} onChange={onChange} />,
        s.canvas[0]?.nodeHelp,
      )}
      {section("sec-quality", "Quality", s.tiles, undefined, undefined, "gap-3")}
      {section("sec-options", "Options", s.switches)}
      {section("sec-more", "More", s.more)}
    </>
  );
}

/** Aspect chips, megapixel chips and the resulting size, set on the canvas node's width and height. */
function CanvasPicker({
  c,
  errors,
  onChange,
}: {
  c: NonNullable<ReturnType<typeof canvasOf>>;
  errors: Record<string, string>;
  onChange: (key: string, raw: Raw) => void;
}) {
  const { width, height, w, h } = c;
  const grid = width.param.step ?? 1;
  const fit = (ratio: number, mp: number) =>
    sizeFor(
      ratio,
      mp,
      grid,
      Math.max(width.param.min ?? 0, height.param.min ?? 0),
      Math.min(width.param.max ?? Infinity, height.param.max ?? Infinity),
    );
  const set = (s: { width: number; height: number }) => {
    onChange(width.key, String(s.width));
    onChange(height.key, String(s.height));
  };
  const aspect = c.auto ? "Auto" : aspectOf(w, h);
  const mp = megapixelsOf(w, h);
  // the exact ratio of the aspect it is, so a size change keeps it (the grid rounds w / h)
  const ratio = ASPECTS.find(([l]) => l === aspectOf(w, h))?.[1] ?? w / h;
  return (
    <>
      <div id="canvas-pick" role="group" aria-label="Aspect" className="grid grid-cols-5 gap-1.5">
        <button
          type="button"
          className="opt bevel"
          aria-pressed={aspect === "Auto"}
          title="The workflow's own size"
          onClick={() => {
            onChange(width.key, toRaw(width.initial, width.param));
            onChange(height.key, toRaw(height.initial, height.param));
          }}
        >
          Auto
        </button>
        {ASPECTS.map(([label, ratio]) => (
          <button
            key={label}
            type="button"
            className="opt bevel"
            aria-pressed={aspect === label}
            onClick={() => set(fit(ratio, mp))}
          >
            {label}
          </button>
        ))}
      </div>
      <div role="group" aria-label="Size" className="grid grid-cols-3 gap-1.5">
        {MEGAPIXELS.map((m) => (
          <button
            key={m}
            type="button"
            className="opt bevel"
            aria-pressed={mp === m}
            onClick={() => set(fit(ratio, m))}
          >
            {m} MP
          </button>
        ))}
      </div>
      <span className="mono text-[12px] text-t2">
        {w} × {h} · {+((w * h) / 1e6).toFixed(1)} MP{grid > 1 && ` · on the ${grid} px grid`}
      </span>
      <FieldError k={width.key} errors={errors} />
      <FieldError k={height.key} errors={errors} />
    </>
  );
}

/** A label-over-value tile; it opens its editor below its row. */
function Tile({
  f,
  raw,
  error,
  open,
  setOpen,
  onChange,
}: {
  f: Field;
  raw: Raw;
  error?: string;
  open: boolean;
  setOpen: (key: string | null) => void;
  onChange: (key: string, raw: Raw) => void;
}) {
  const id = idOf(f.key);
  const d = display(f, raw);
  const shown = f.param.type === "seed" && raw === "" ? "Random" : d.value || "—";
  return (
    <>
      <div className="flex min-w-0 flex-col gap-1">
        <button
          type="button"
          id={id + "-tile"}
          aria-expanded={open}
          aria-controls={id + "-ed"}
          aria-describedby={error ? id + "-err" : undefined}
          onClick={() => setOpen(open ? null : f.key)}
          className={`set bevel flex items-center justify-between gap-2 ${error ? "outline-2 outline-bad" : ""}`}
        >
          <span className="flex min-w-0 flex-col gap-0.5">
            <span className="text-[12px] text-t2">{d.label}</span>
            <span className="truncate font-semibold">{shown}</span>
          </span>
          <ChevronRight
            size={14}
            aria-hidden
            className={`shrink-0 text-t2 transition-transform ${open ? "rotate-90" : ""}`}
          />
        </button>
        {error && (
          <span id={id + "-err"} className="text-[12px] text-bad">
            {error}
          </span>
        )}
      </div>
      {open && (
        <Editor
          f={f}
          raw={raw}
          onChange={onChange}
          close={() => {
            setOpen(null);
            document.getElementById(id + "-tile")?.focus();
          }}
        />
      )}
    </>
  );
}

/** A fresh seed for when Random is switched off (32 bits: easy to read back and type). */
const freshSeed = () => String(Math.floor(Math.random() * 2 ** 32));

/** A tile's inline editor: the enum's values, a number with its slider, or a seed with Random. Escape closes it. */
function Editor({
  f,
  raw,
  onChange,
  close,
}: {
  f: Field;
  raw: Raw;
  onChange: (key: string, raw: Raw) => void;
  close: () => void;
}) {
  const p = f.param;
  const id = idOf(f.key);
  const label = p.label ?? p.name;
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => ref.current?.querySelector<HTMLElement>("input, .opt")?.focus(), []);
  const num = (
    <input
      id={id}
      className="mono h-9 w-full min-w-0 rounded-lg border-0 bg-s4 px-2 text-[13px] font-semibold text-t1 placeholder:font-normal placeholder:text-t2"
      type="number"
      inputMode={p.type === "float" ? "decimal" : "numeric"}
      min={p.type === "seed" ? 0 : p.min}
      max={p.max}
      step={p.step ?? (p.type === "float" ? "any" : undefined)}
      placeholder={p.type === "seed" ? "Random" : undefined}
      value={String(raw)}
      onChange={(e) => onChange(f.key, e.target.value)}
    />
  );
  return (
    <div
      id={id + "-ed"}
      ref={ref}
      role="group"
      aria-label={label}
      className="col-span-2 flex flex-col gap-2 bg-s1 p-3"
      style={{ borderRadius: "18px 18px 18px 8px" }}
      onKeyDown={(e) => {
        if (e.key === "Escape") {
          e.stopPropagation();
          close();
        }
      }}
    >
      <span className="flex items-center text-[12px] text-t2">
        {p.type === "enum" ? label : <label htmlFor={id}>{label}</label>}
        {p.help && <Info>{p.help}</Info>}
      </span>
      {p.type === "enum" ? (
        <div className="flex flex-wrap gap-1.5">
          {p.enum?.map((o) => (
            <button
              key={o}
              type="button"
              className="opt bevel min-w-16 px-3 text-[13px]"
              aria-pressed={raw === o}
              onClick={() => onChange(f.key, o)}
            >
              {pretty(o)}
            </button>
          ))}
        </div>
      ) : p.type === "seed" ? (
        <div className="flex items-center gap-1.5">
          {num}
          <button
            type="button"
            aria-pressed={raw === ""}
            onClick={() => onChange(f.key, raw === "" ? freshSeed() : "")}
            className="opt bevel h-9 shrink-0 px-3 text-[13px]"
          >
            Random
          </button>
        </div>
      ) : (
        <>
          {num}
          {p.min != null && p.max != null && (
            <input
              type="range"
              aria-label={label}
              min={p.min}
              max={p.max}
              step={p.step ?? (p.type === "float" ? "any" : 1)}
              value={Number(raw) || p.min}
              onChange={(e) => onChange(f.key, e.target.value)}
              className="w-full accent-a1"
            />
          )}
        </>
      )}
    </div>
  );
}

/** A bool param: its label and a switch. */
function Switch({
  f,
  raw,
  onChange,
}: {
  f: Field;
  raw: Raw;
  onChange: (key: string, raw: Raw) => void;
}) {
  const id = idOf(f.key);
  const on = raw === true;
  return (
    <div className="col-span-2 flex items-center justify-between gap-3">
      <span className="flex items-center">
        <span id={id + "-l"}>{f.param.label ?? f.param.name}</span>
        {f.param.help && <Info>{f.param.help}</Info>}
      </span>
      <button
        id={id}
        type="button"
        role="switch"
        aria-checked={on}
        aria-labelledby={id + "-l"}
        onClick={() => onChange(f.key, !on)}
        className="flex h-[26px] w-11 shrink-0 rounded-full border border-line p-0.5"
        style={{ background: on ? "var(--a1)" : "var(--s1)" }}
      >
        <span
          className="block size-5 rounded-full transition-transform"
          style={{
            background: on ? "var(--on1)" : "var(--t2)",
            transform: on ? "translateX(18px)" : undefined,
          }}
        />
      </button>
    </div>
  );
}

/** A string param: a labelled line of text. */
function Line({
  f,
  raw,
  errors,
  onChange,
}: { f: Field; raw: Raw } & Omit<FormProps, "fields" | "raws">) {
  const id = idOf(f.key);
  return (
    <div className="col-span-2 flex flex-col gap-1.5">
      <span className="flex items-center text-[12px] text-t2">
        <label htmlFor={id}>{f.param.label ?? f.param.name}</label>
        {f.param.help && <Info>{f.param.help}</Info>}
      </span>
      <input
        id={id}
        className="in"
        maxLength={f.param.max}
        value={String(raw)}
        aria-invalid={!!errors[f.key] || undefined}
        aria-describedby={errors[f.key] ? id + "-err" : undefined}
        onChange={(e) => onChange(f.key, e.target.value)}
      />
      <FieldError k={f.key} errors={errors} />
    </div>
  );
}

/** The composer's quick chips: the model node's choices (the node that outputs a Model), the canvas, the seed (with Random). Each opens
 * its inspector control through `show` (a field key, or "canvas"). */
export function Chips({
  fields,
  raws,
  onChange,
  show,
}: Omit<FormProps, "errors"> & { show: (key: string) => void }) {
  const s = sections(fields);
  const model = s.tiles.filter((f) => f.model && f.param.type === "enum");
  const first = model[0];
  const c = canvasOf(fields, raws);
  const seed = s.tiles.find((f) => f.param.type === "seed");
  const chip = "chip bevel rounded-[10px] bg-s4";
  const random = seed && rawOf(seed, raws) === "";
  return (
    <>
      {first && (
        <button
          type="button"
          className={chip}
          title={model.map((f) => f.param.label ?? f.param.name).join(" · ")}
          onClick={() => show(first.key)}
        >
          {model.map((f) => pretty(String(rawOf(f, raws)))).join(" · ")}
        </button>
      )}
      {c && (
        <button type="button" className={chip} onClick={() => show("canvas")}>
          {sizeText(c.w, c.h)}
        </button>
      )}
      {seed && (
        <div
          role="group"
          aria-label="Seed"
          className="bevel flex h-9 items-center rounded-[10px] bg-s4 text-[13px] font-medium"
        >
          <button
            type="button"
            aria-pressed={random}
            aria-label="Random seed"
            title="Random seed"
            onClick={() => onChange(seed.key, random ? freshSeed() : "")}
            className={`grid h-9 w-[26px] place-items-center rounded-[10px] pl-3 ${random ? "text-a1" : ""}`}
          >
            <Dices size={14} strokeWidth={1.75} aria-hidden />
          </button>
          <button
            type="button"
            className="h-9 rounded-[10px] pr-3 pl-1.5"
            onClick={() => show(seed.key)}
          >
            Seed {random ? "Random" : String(rawOf(seed, raws))}
          </button>
        </div>
      )}
    </>
  );
}
