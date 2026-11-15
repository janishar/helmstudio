"use client";
// Form controls rendered from node schemas: text params go in the composer, the rest in the inspector, grouped
// generically (the canvas node, label-over-value tiles, switches, the rest); the composer's chips summarise them.
import { useEffect, useRef } from "react";
import { ChevronRight, Dices } from "lucide-react";
import { Info } from "@/components/kit";
import { PromptArea } from "@/components/create/prompt";
import { RowsEditor, type RefUploads } from "@/components/create/refs";
import { LoraRows } from "@/components/create/loras";
import { autoSize, imageInputs, mentionOf, refIds } from "@/lib/refs";
import { useTakes } from "@/lib/take-cache";
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
  type Update,
} from "@/lib/workflow";

/** Props shared by the form parts: the fields, what the controls hold, and the errors by field key. */
export type FormProps = {
  fields: Field[];
  raws: Record<string, Raw>;
  errors: Record<string, string>;
  onChange: (key: string, u: Update) => void;
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
  const ref = imageInputs(fields).find((f) => refIds(f, raws[f.key]).length);
  return fields
    .filter((f) => f.param.type === "text")
    .map((f) => (
      <div key={f.key} className="flex min-w-0 flex-1 flex-col gap-1">
        <label htmlFor={idOf(f.key)} className="sr-only">
          {f.param.label ?? f.param.name}
        </label>
        <PromptArea
          id={idOf(f.key)}
          maxLength={f.param.max}
          placeholder={
            f.param.name === "text"
              ? ref
                ? `Describe the edit; name the references ${mentionOf(ref.param, 1)}, ${mentionOf(ref.param, 2)}…`
                : "Describe what you want to see"
              : (f.param.label ?? f.param.name)
          }
          rows={2}
          className="w-full resize-none border-0 bg-transparent text-[15px] leading-[22px] text-t1 outline-0 placeholder:text-t2"
          value={String(rawOf(f, raws))}
          aria-invalid={!!errors[f.key] || undefined}
          aria-describedby={errors[f.key] ? idOf(f.key) + "-err" : undefined}
          onChange={(v) => onChange(f.key, v)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && (e.metaKey || e.ctrlKey))
              e.currentTarget.form?.requestSubmit();
          }}
        />
        <FieldError k={f.key} errors={errors} />
      </div>
    ));
}

/** The canvas node's width and height params (and `mode`, when it has one) and the size now, when it has both.
 * `auto`: mode auto, else the workflow's own size. Auto with a reference (`ref`, the last one's size) is about 1 MP
 * at its aspect, as the runner sizes an edit (contracts/README.md). */
export function canvasOf(
  fs: Field[],
  raws: Record<string, Raw>,
  ref?: { width: number; height: number } | null,
) {
  const find = (name: string, type: string) =>
    fs.find((f) => f.canvas && f.param.name === name && f.param.type === type);
  const width = find("width", "int");
  const height = find("height", "int");
  const mode = find("mode", "enum");
  if (!width || !height) return null;
  const w = Number(rawOf(width, raws));
  const h = Number(rawOf(height, raws));
  const auto = mode ? rawOf(mode, raws) === "auto" : w === width.initial && h === height.initial;
  const fromRef = auto && !!mode && !!ref;
  const size = fromRef ? autoSize(ref.width, ref.height) : { width: w, height: h };
  return { width, height, mode, w: size.width, h: size.height, auto, fromRef };
}

/** The last reference's size (canvas auto follows it), once its take is known. */
function useLastRef(fs: Field[], raws: Record<string, Raw>) {
  const last = imageInputs(fs)
    .flatMap((f) => refIds(f, raws[f.key]))
    .at(-1);
  const t = useTakes(last ? [last] : [])(last ?? "");
  return t?.width && t.height ? { width: t.width, height: t.height } : null;
}

/** "16:9 · 4 MP", else "1000 × 700 · 0.7 MP". */
const sizeText = (w: number, h: number) =>
  `${aspectOf(w, h) ?? `${w} × ${h}`} · ${+((w * h) / 1e6).toFixed(1)} MP`;

/** Which inspector editor is open (a field key), and how to open or close one. */
type OpenProps = { open: string | null; setOpen: (key: string | null) => void };

/** A LoRA stack's rows as the inspector edits them (the mock's LoRA section). */
function Loras({ f, raws, errors, onChange }: { f: Field } & Omit<FormProps, "fields">) {
  const raw = rawOf(f, raws);
  return (
    <LoraRows
      param={f.param}
      family={f.family ?? null}
      rows={Array.isArray(raw) ? raw : []}
      onRows={(rows) => onChange(f.key, rows)}
      error={errors[f.key]}
    />
  );
}

/** The LoRA section's note (the mock's): what fits and what a strength change re-runs. */
const loraHelp = (title: string) =>
  `Only LoRAs made for ${title} can be added. Changing strength re-runs Sample without reloading the model. Drag a LoRA, or focus it and press Alt with an arrow key, to change the order; Delete removes it.`;

/** The params other than the prompt: References (rows editors), Canvas, Quality (tiles), LoRA, Options (switches),
 * More. With `groups` (a workflow's Studio block), its sections instead, in its order. `familyTitle` names the
 * workflow's family in the LoRA note ("Qwen Image 2.1"). */
export function Inspector({
  fields,
  raws,
  errors,
  onChange,
  open,
  setOpen,
  uploads,
  groups,
  familyTitle,
}: FormProps &
  OpenProps & {
    uploads: RefUploads;
    groups?: { title: string; fields: Field[] }[];
    familyTitle?: string;
  }) {
  const s = sections(fields);
  const c = canvasOf(fields, raws, useLastRef(fields, raws));
  const extra = s.canvas.filter((f) => f !== c?.width && f !== c?.height && f !== c?.mode);
  if (
    !s.refs.length &&
    !s.loras.length &&
    !s.canvas.length &&
    !s.tiles.length &&
    !s.switches.length &&
    !s.more.length
  )
    return <p className="text-[13px] text-t2">Nothing to set.</p>;
  const control = (f: Field) => {
    const raw = rawOf(f, raws);
    if (f.param.type === "bool") return <Switch key={f.key} f={f} raw={raw} onChange={onChange} />;
    if (f.param.type === "string" || f.param.type === "text")
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
  if (groups)
    return groups.map((g, i) => {
      const refs = g.fields.filter((f) => s.refs.includes(f));
      const loras = g.fields.filter((f) => s.loras.includes(f));
      const canvas = c && g.fields.includes(c.width) && g.fields.includes(c.height);
      const rest = g.fields.filter(
        (f) =>
          !refs.includes(f) &&
          !loras.includes(f) &&
          !(canvas && (f === c.width || f === c.height || f === c.mode)),
      );
      return section(
        `sec-g${i}`,
        g.title,
        rest,
        (refs.length > 0 || loras.length > 0 || canvas) && (
          <>
            {loras.map((f) => (
              <Loras key={f.key} f={f} raws={raws} errors={errors} onChange={onChange} />
            ))}
            {refs.map((f) => (
              <RowsEditor
                key={f.key}
                f={f}
                raw={raws[f.key]}
                error={errors[f.key]}
                onChange={onChange}
                uploads={uploads}
              />
            ))}
            {canvas && <CanvasPicker c={c} errors={errors} onChange={onChange} />}
          </>
        ),
      );
    });
  return (
    <>
      {s.refs.map((f, i) =>
        section(
          `sec-refs-${i}`,
          f.param.label ?? f.param.name,
          [],
          <RowsEditor
            f={f}
            raw={raws[f.key]}
            error={errors[f.key]}
            onChange={onChange}
            uploads={uploads}
          />,
          f.param.help,
        ),
      )}
      {section(
        "sec-canvas",
        "Canvas",
        extra,
        c && <CanvasPicker c={c} errors={errors} onChange={onChange} />,
        s.canvas[0]?.nodeHelp,
      )}
      {section("sec-quality", "Quality", s.tiles, undefined, undefined, "gap-3")}
      {s.loras.map((f, i) =>
        section(
          `sec-lora-${i}`,
          s.loras.length > 1 ? `LoRA (${f.node})` : "LoRA",
          [],
          <Loras f={f} raws={raws} errors={errors} onChange={onChange} />,
          loraHelp(familyTitle ?? f.family ?? "this model"),
        ),
      )}
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
  const { width, height, mode, w, h } = c;
  const grid = width.param.step ?? 1;
  const fit = (ratio: number, mp: number) =>
    sizeFor(
      ratio,
      mp,
      grid,
      Math.max(width.param.min ?? 0, height.param.min ?? 0),
      Math.min(width.param.max ?? Infinity, height.param.max ?? Infinity),
    );
  // a size picked is fixed: in an edit, Auto would follow the last reference instead
  const set = (s: { width: number; height: number }) => {
    if (mode) onChange(mode.key, "fixed");
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
          title={
            mode
              ? "The workflow's own size; with references, about 1 MP at the last one's aspect"
              : "The workflow's own size"
          }
          onClick={() => {
            if (mode) onChange(mode.key, "auto");
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
        {w} × {h} · {+((w * h) / 1e6).toFixed(1)} MP
        {c.fromRef ? " · from the last reference" : grid > 1 && ` · on the ${grid} px grid`}
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
export function Switch({
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
  const c = canvasOf(fields, raws, useLastRef(fields, raws));
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
          {c.fromRef ? `Auto · ${sizeText(c.w, c.h)}` : sizeText(c.w, c.h)}
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

/** The composer's first row: each image input's references as chips with "Add from gallery" (the mock's chips row). */
export function RefChips({
  fields,
  raws,
  errors,
  onChange,
  uploads,
  extra,
}: FormProps & { uploads: RefUploads; extra?: React.ReactNode }) {
  return imageInputs(fields).map((f, i) => (
    <RowsEditor
      extra={i === 0 ? extra : undefined}
      key={f.key}
      f={f}
      raw={raws[f.key]}
      error={errors[f.key]}
      onChange={onChange}
      uploads={uploads}
      compact
    />
  ));
}
