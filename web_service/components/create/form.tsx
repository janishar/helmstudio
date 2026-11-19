"use client";
// Form controls rendered from node schemas: text params go in the composer, the rest in the inspector, grouped
// generically (the canvas node, label-over-value tiles, switches, the rest); the composer's chips summarise them.
import { Fragment, useEffect, useRef, useState } from "react";
import { ChevronRight, Dices } from "lucide-react";
import { Info } from "@/components/kit";
import { PromptArea } from "@/components/create/prompt";
import { FirstFrameTile, RowsEditor, type RefUploads } from "@/components/create/refs";
import { LoraRows } from "@/components/create/loras";
import { ExtendControl, MediaControl, SpanControl, extensionOf } from "@/components/create/source";
import type { FrameGrid } from "@/lib/source";
import { PlainRows } from "@/components/create/rows";
import {
  allowedInts,
  autoBounds,
  clipOf,
  firstFrameRow,
  positionOf,
  setFirstFrame,
} from "@/lib/video";
import { modeOf } from "@/lib/loras";
import type { S } from "@/lib/api";
import { autoSize, imageInputs, mentionOf, refIds } from "@/lib/refs";
import { useTakes } from "@/lib/take-cache";
import { durationText, gridOf, secondsText, seconds } from "@/lib/media";
import {
  ASPECTS,
  aspectOf,
  display,
  fpsKey,
  megapixelChoices,
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
  // the prompt names references only where the param says how (`mention`)
  const ref = imageInputs(fields).find((f) => f.param.mention && refIds(f, raws[f.key]).length);
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
  // without a mode param there is no Auto: the size is what it reads
  const auto = !!mode && rawOf(mode, raws) === "auto";
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
const sizeText = (w: number, h: number, grid = 1) =>
  `${aspectOf(w, h, grid) ?? `${w} × ${h}`} · ${mpText(w, h)}`;
/** "0.3 MP"; under 0.1 MP two significant digits ("0.033 MP"), never "0 MP". */
const mpText = (w: number, h: number) => {
  const mp = (w * h) / 1e6;
  return `${+(mp < 0.1 ? mp.toPrecision(2) : mp.toFixed(1))} MP`;
};

/** A frames field's frame rate now (its `fps` field's value), 0 when it names none. */
const fpsOf = (f: Field, fs: Field[], raws: Record<string, Raw>) => {
  const r = fs.find((x) => x.key === fpsKey(f));
  return r ? Number(rawOf(r, raws)) || 0 : 0;
};

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

/** The LoRA section's note (the mock's): what fits and what a strength change re-runs (a stack that only bakes
 * reloads the model). */
const loraHelp = (title: string, f?: Field) =>
  `Only LoRAs made for ${title} can be added. ${
    f && modeOf(f.param)?.[1].enum?.join() === "bake"
      ? "Each is baked into the model at load: a strength change reloads it."
      : "Changing strength re-runs Sample without reloading the model."
  } Drag a LoRA, or focus it and press Alt with an arrow key, to change the order; Delete removes it.`;

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
  compact,
  resolved,
  presets,
  grid = null,
}: FormProps &
  OpenProps & {
    uploads: RefUploads;
    groups?: { title: string; fields: Field[] }[];
    familyTitle?: string;
    /** the canvas as Aspect and Size tiles (a video workflow, docs/mock/TextToVideo.dc.html) */
    compact?: boolean;
    /** what the runner chose for params set to auto in the shown run (`Run.resolved.params`) */
    resolved?: Record<string, number>;
    /** the workflow's presets (`studio.presets`): chips that set their values */
    presets?: Record<string, Record<string, S<"ParamValue">>>;
    /** the family's frame grid: what a span, an extension or a part comes to */
    grid?: FrameGrid | null;
  }) {
  const s = sections(fields);
  const ext = extensionOf(fields);
  const clip = clipOf(fields, raws);
  const c = canvasOf(fields, raws, useLastRef(fields, raws));
  const extra = s.canvas.filter((f) => f !== c?.width && f !== c?.height && f !== c?.mode);
  if (
    !s.refs.length &&
    !s.rows.length &&
    !s.loras.length &&
    !s.canvas.length &&
    !s.duration.length &&
    !s.tiles.length &&
    !s.switches.length &&
    !s.more.length &&
    !s.media.length
  )
    return <p className="text-[13px] text-t2">Nothing to set.</p>;
  const control = (f: Field) => {
    const raw = rawOf(f, raws);
    const shared = { fields, raws, error: errors[f.key], grid };
    if (f.param.type === "media")
      return <MediaControl key={f.key} f={f} {...shared} onChange={onChange} />;
    if (f.param.type === "span")
      return (
        <SpanControl
          key={f.key}
          f={f}
          {...shared}
          switches={fields
            .filter((x) => x.node === f.node && x.param.type === "bool")
            .map((x) => (
              <Switch key={x.key} f={x} raw={rawOf(x, raws)} onChange={onChange} />
            ))}
        />
      );
    if (ext && f === ext.direction) return null; // drawn with its seconds
    if (ext && f === ext.seconds)
      return <ExtendControl key={f.key} {...ext} {...shared} onChange={onChange} />;
    if (f.param.type === "bool") return <Switch key={f.key} f={f} raw={raw} onChange={onChange} />;
    if (f.param.type === "frames")
      return (
        <Duration
          key={f.key}
          f={f}
          raw={raw}
          fps={fpsOf(f, fields, raws)}
          error={errors[f.key]}
          onChange={onChange}
          resolved={resolved?.[f.key]}
        />
      );
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
        choices={allowedInts(f, raws)?.map(String)}
      />
    );
  };
  const rowsEditor = (f: Field) =>
    s.rows.includes(f) ? (
      <PlainRows
        key={f.key}
        f={f}
        raw={raws[f.key]}
        error={errors[f.key]}
        clip={clip}
        onChange={onChange}
      />
    ) : (
      <RowsEditor
        key={f.key}
        f={f}
        raw={raws[f.key]}
        error={errors[f.key]}
        onChange={onChange}
        uploads={uploads}
        clip={clip}
      />
    );
  const presetChips = presets && Object.keys(presets).length > 0 && (
    <Presets presets={presets} fields={fields} raws={raws} onChange={onChange} />
  );
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
    return [
      presetChips,
      ...groups.map((g, i) => {
        const refs = g.fields.filter((f) => s.refs.includes(f) || s.rows.includes(f));
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
              {refs.map(rowsEditor)}
              {canvas && (
                <CanvasPicker c={c} errors={errors} onChange={onChange} compact={compact} />
              )}
            </>
          ),
          loras[0]
            ? loraHelp(familyTitle ?? loras[0].family ?? "this model", loras[0])
            : (refs[0]?.param.help ??
                (g.fields.length === 1 && s.media.includes(g.fields[0])
                  ? g.fields[0].param.help
                  : undefined)),
        );
      }),
    ];
  return (
    <>
      {presetChips}
      {[...s.refs, ...s.rows].map((f, i) =>
        section(`sec-refs-${i}`, f.param.label ?? f.param.name, [], rowsEditor(f), f.param.help),
      )}
      {s.media.map((f, i) =>
        section(`sec-media-${i}`, f.param.label ?? f.param.name, [f], undefined, f.param.help),
      )}
      {section(
        "sec-canvas",
        "Canvas",
        extra,
        c && <CanvasPicker c={c} errors={errors} onChange={onChange} compact={compact} />,
        s.canvas[0]?.nodeHelp,
      )}
      {section("sec-duration", "Duration", s.duration, undefined, s.duration[0]?.nodeHelp)}
      {section("sec-quality", "Quality", s.tiles, undefined, undefined, "gap-3")}
      {s.loras.map((f, i) =>
        section(
          `sec-lora-${i}`,
          s.loras.length > 1 ? `LoRA (${f.node})` : "LoRA",
          [],
          <Loras f={f} raws={raws} errors={errors} onChange={onChange} />,
          loraHelp(familyTitle ?? f.family ?? "this model", f),
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
  compact,
}: {
  c: NonNullable<ReturnType<typeof canvasOf>>;
  errors: Record<string, string>;
  onChange: (key: string, raw: Raw) => void;
  compact?: boolean;
}) {
  const { width, height, mode, w, h } = c;
  // compact: which tile's choices are open
  const [opened, setOpened] = useState<"aspect" | "size" | null>(null);
  const grid = width.param.step ?? 1;
  const min = Math.max(width.param.min ?? 0, height.param.min ?? 0);
  const max = Math.min(width.param.max ?? Infinity, height.param.max ?? Infinity);
  const choices = megapixelChoices(min, max);
  const fit = (ratio: number, mp: number) => sizeFor(ratio, mp, grid, min, max);
  // a size picked is fixed: in an edit, Auto would follow the last reference instead
  const set = (s: { width: number; height: number }) => {
    if (mode) onChange(mode.key, "fixed");
    onChange(width.key, String(s.width));
    onChange(height.key, String(s.height));
  };
  const aspect = c.auto ? "Auto" : aspectOf(w, h, grid);
  const mp = megapixelsOf(w, h, choices);
  // the exact ratio of the aspect it is, so a size change keeps it (the grid rounds w / h)
  const ratio = ASPECTS.find(([l]) => l === aspectOf(w, h, grid))?.[1] ?? w / h;
  const tile = (k: "aspect" | "size", label: string, value: string) => (
    <button
      type="button"
      className="set bevel"
      aria-expanded={opened === k}
      onClick={() => setOpened(opened === k ? null : k)}
    >
      <small className="text-[12px] text-t2">{label}</small>
      <b className="font-semibold">{value}</b>
    </button>
  );
  return (
    <>
      {compact && (
        <div className="grid grid-cols-2 gap-2">
          {tile("aspect", "Aspect", aspect ?? `${w} × ${h}`)}
          {tile("size", "Size", mpText(w, h))}
        </div>
      )}
      <div
        id="canvas-pick"
        role="group"
        aria-label="Aspect"
        className={`grid gap-1.5 ${mode ? "grid-cols-5" : "grid-cols-4"}`}
        hidden={compact && opened !== "aspect"}
      >
        {mode && (
          <button
            type="button"
            className="opt bevel"
            aria-pressed={aspect === "Auto"}
            title="The workflow's own size; with references, about 1 MP at the last one's aspect"
            onClick={() => {
              onChange(mode.key, "auto");
              onChange(width.key, toRaw(width.initial, width.param));
              onChange(height.key, toRaw(height.initial, height.param));
            }}
          >
            Auto
          </button>
        )}
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
      <div
        role="group"
        aria-label="Size"
        className="grid grid-flow-col gap-1.5"
        hidden={compact && opened !== "size"}
      >
        {choices.map((m) => (
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
        {w} × {h} · {mpText(w, h)}
        {c.fromRef ? " · from the last reference" : grid > 1 && ` · on the ${grid} px grid`}
      </span>
      <FieldError k={width.key} errors={errors} />
      <FieldError k={height.key} errors={errors} />
    </>
  );
}

/** An auto length's span as its readouts show it: "1.0–13.4 s" (its bounds in frames on the grid). */
export function autoText(p: S<"ParamSchema">, fps: number) {
  const b = autoBounds(p, fps);
  return `${(b.lo / fps).toFixed(1)}–${secondsText(b.hi / fps)}`;
}

/** The Duration slider (plan 05-ui.md 8.4): a frames param on its grid, one notch per step, read as frames and
 * seconds at the node's frame rate; with auto length (the param declares `auto`) a switch that lets the model pick
 * it (the slider off meanwhile) and, after a run, the length it picked. */
function Duration({
  f,
  raw,
  fps,
  error,
  onChange,
  resolved,
}: {
  f: Field;
  raw: Raw;
  fps: number;
  error?: string;
  onChange: (key: string, raw: Raw) => void;
  resolved?: number;
}) {
  const id = idOf(f.key);
  const g = gridOf(f.param);
  const auto = raw === "auto";
  const n = Number(raw);
  const span = f.param.auto && fps > 0 ? autoText(f.param, fps) : null;
  const d0 = Number(f.param.default);
  const fixed = Number.isFinite(n) ? n : (resolved ?? (Number.isFinite(d0) ? d0 : g.min));
  const text = auto ? `Auto${span ? ` · ${span}` : ""}` : durationText(fixed, fps);
  return (
    <div id={id + "-ed"} className="col-span-2 flex flex-col gap-2">
      <label htmlFor={id} className="flex justify-between gap-2">
        <span className="flex items-center">
          {f.param.label ?? f.param.name}
          {f.param.help && <Info>{f.param.help}</Info>}
        </span>
        <span className="mono text-[12px] text-t2">{text}</span>
      </label>
      <input
        id={id}
        type="range"
        min={g.min}
        max={g.max}
        step={g.step}
        value={fixed}
        disabled={auto}
        aria-valuetext={text}
        aria-invalid={!!error || undefined}
        aria-describedby={error ? id + "-err" : undefined}
        onChange={(e) => onChange(f.key, e.target.value)}
        className="w-full accent-a1 disabled:opacity-50"
      />
      {f.param.auto && (
        <div className="flex items-center justify-between gap-3">
          <span className="flex flex-col gap-0.5">
            <span id={id + "-auto-l"}>Auto length</span>
            {span && (
              <span className="text-[12px] text-t2">The model picks {span} from the prompt</span>
            )}
          </span>
          <button
            type="button"
            role="switch"
            aria-checked={auto}
            aria-labelledby={id + "-auto-l"}
            onClick={() => onChange(f.key, auto ? String(fixed) : "auto")}
            className="flex h-[26px] w-11 shrink-0 rounded-full border border-line p-0.5"
            style={{ background: auto ? "var(--a1)" : "var(--s1)" }}
          >
            <span
              className="block size-5 rounded-full transition-transform"
              style={{
                background: auto ? "var(--on1)" : "var(--t2)",
                transform: auto ? "translateX(18px)" : undefined,
              }}
            />
          </button>
        </div>
      )}
      {auto && resolved != null && fps > 0 && (
        <span className="mono text-[12px] text-t2">Last run: {durationText(resolved, fps)}</span>
      )}
      <FieldError k={f.key} errors={error ? { [f.key]: error } : {}} />
    </div>
  );
}

/** The workflow's presets (`studio.presets`) as chips: one sets its values; it reads pressed while the form holds
 * them all. */
function Presets({
  presets,
  fields,
  raws,
  onChange,
}: Omit<FormProps, "errors"> & { presets: Record<string, Record<string, S<"ParamValue">>> }) {
  const holds = (values: Record<string, S<"ParamValue">>) =>
    Object.entries(values).every(([k, v]) => {
      const f = fields.find((x) => x.key === k);
      return f && JSON.stringify(rawOf(f, raws)) === JSON.stringify(toRaw(v, f.param));
    });
  return (
    <section aria-labelledby="sec-preset" className="flex flex-col gap-2">
      <h2 id="sec-preset" className="heading">
        Preset
      </h2>
      <div className="flex flex-wrap gap-2">
        {Object.entries(presets).map(([name, values]) => (
          <button
            key={name}
            type="button"
            aria-pressed={holds(values)}
            onClick={() =>
              Object.entries(values).forEach(([k, v]) => {
                const f = fields.find((x) => x.key === k);
                if (f) onChange(k, toRaw(v, f.param));
              })
            }
            className="h-9 rounded-full border border-transparent bg-s3 px-3.5 text-[13px] aria-pressed:border-a2 aria-pressed:bg-s1"
          >
            {name}
          </button>
        ))}
      </div>
    </section>
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
  choices,
}: {
  f: Field;
  raw: Raw;
  error?: string;
  open: boolean;
  setOpen: (key: string | null) => void;
  onChange: (key: string, raw: Raw) => void;
  /** the only values it takes (an int's `values`): one is a readout, several a choice */
  choices?: string[];
}) {
  const id = idOf(f.key);
  const d = display(f, raw);
  const shown = f.param.type === "seed" && raw === "" ? "Random" : d.value || "—";
  if (choices?.length === 1)
    return (
      <div id={id + "-ed"} className="set flex flex-col gap-0.5" title={f.param.help}>
        <span className="text-[12px] text-t2">{d.label}</span>
        <span className="font-semibold">{choices[0]}</span>
        {error && (
          <span id={id + "-err"} className="text-[12px] text-bad">
            {error}
          </span>
        )}
      </div>
    );
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
          choices={choices}
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
  choices,
}: {
  f: Field;
  raw: Raw;
  onChange: (key: string, raw: Raw) => void;
  close: () => void;
  choices?: string[];
}) {
  const p = f.param;
  const id = idOf(f.key);
  const label = p.label ?? p.name;
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    ref.current?.querySelector<HTMLElement>("input, .opt")?.focus();
  }, []);
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
        {p.type === "enum" || choices ? label : <label htmlFor={id}>{label}</label>}
        {p.help && <Info>{p.help}</Info>}
      </span>
      {p.type === "enum" || choices ? (
        <div className="flex flex-wrap gap-1.5">
          {(choices ?? p.enum)?.map((o) => (
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
  compact,
  toggles = [],
}: Omit<FormProps, "errors"> & {
  show: (key: string) => void;
  /** a video workflow's chips (docs/mock/TextToVideo.dc.html): the size as w × h, no seed chip */
  compact?: boolean;
  /** bool params placed on the composer (`studio.quick`): a chip each that switches it ("Audio on") */
  toggles?: Field[];
}) {
  const s = sections(fields);
  const model = s.tiles.filter((f) => f.model && f.param.type === "enum");
  const first = model[0];
  const c = canvasOf(fields, raws, useLastRef(fields, raws));
  const seed = compact ? undefined : s.tiles.find((f) => f.param.type === "seed");
  const frames = s.duration.filter((f) => f.param.type === "frames");
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
          {compact
            ? `${c.w} × ${c.h}`
            : c.fromRef
              ? `Auto · ${sizeText(c.w, c.h, c.width.param.step)}`
              : sizeText(c.w, c.h, c.width.param.step)}
        </button>
      )}
      {frames.map((f) => {
        const fps = fpsOf(f, fields, raws);
        return (
          <button
            key={f.key}
            type="button"
            className={chip}
            title={f.param.label ?? f.param.name}
            onClick={() => show(f.key)}
          >
            {[
              rawOf(f, raws) === "auto"
                ? "Auto"
                : secondsText(seconds(Number(rawOf(f, raws)), fps)),
              fps && `${fps} fps`,
            ]
              .filter(Boolean)
              .join(" · ")}
          </button>
        );
      })}
      {toggles.map((f) => {
        const on = rawOf(f, raws) === true;
        return (
          <button
            key={f.key}
            type="button"
            aria-pressed={on}
            title={f.param.help}
            className={chip}
            onClick={() => onChange(f.key, !on)}
          >
            {f.param.label ?? f.param.name} {on ? "on" : "off"}
          </button>
        );
      })}
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
  const clip = clipOf(fields, raws);
  return imageInputs(fields).map((f, i) => {
    if (!positionOf(f.param))
      return (
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
      );
    // a time on the clip: the composer shows the first frame (docs/mock/TextToVideo.dc.html); the rest is the
    // inspector's rows editor
    const rows = rawOf(f, raws) as S<"RowItem">[];
    const first = firstFrameRow(f.param, rows, clip);
    return (
      <Fragment key={f.key}>
        {i === 0 && extra}
        <FirstFrameTile
          id={first ? refIds(f, [first])[0] : undefined}
          more={rows.length - (first ? 1 : 0)}
          onPick={(id) =>
            onChange(f.key, (old) =>
              setFirstFrame(f.param, Array.isArray(old) ? old : rows, id, clip),
            )
          }
        />
      </Fragment>
    );
  });
}
