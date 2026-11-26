"use client";
// Form controls rendered from node schemas: text params go in the composer, the rest in the inspector, grouped
// generically (the canvas node, label-over-value tiles, switches, the rest); the composer's chips summarise them.
import { Fragment, useEffect, useRef, useState } from "react";
import { ChevronRight, Dices } from "lucide-react";
import { Info } from "@/components/kit";
import { PromptArea } from "@/components/create/prompt";
import { FirstFrameTile, RowsEditor, type Opener, type RefUploads } from "@/components/create/refs";
import { LoraRows } from "@/components/create/loras";
import {
  ExtendControl,
  MediaControl,
  MediaRows,
  SpanControl,
  extensionOf,
} from "@/components/create/source";
import { mediaItem, type FrameGrid } from "@/lib/source";
import { PlainRows } from "@/components/create/rows";
import { VoiceSet, type VoiceCtx } from "@/components/create/voices";
import {
  allowedInts,
  autoBounds,
  clipOf,
  firstFrameRow,
  fpsOf,
  soundRowsOf,
  positionOf,
  setFirstFrame,
} from "@/lib/video";
import { modeOf } from "@/lib/loras";
import type { S } from "@/lib/api";
import { FIRST_REF_CANVAS, autoCanvas, imageInputs, mentionOf, refIds } from "@/lib/refs";
import { useTakes } from "@/lib/take-cache";
import { durationText, gridOf, secondsText, seconds } from "@/lib/media";
import {
  ASPECTS,
  aspectOf,
  chipText,
  display,
  fpsKey,
  isAuto,
  megapixelChoices,
  megapixelsOf,
  optionText,
  pretty,
  prettyModel,
  sections,
  sizeFor,
  toRaw,
  type Field,
  type Raw,
  type Update,
} from "@/lib/workflow";
import { TextArea } from "@/components/expand-text";

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
export function TextFields({
  fields,
  raws,
  errors,
  onChange,
  spoken,
  kind,
}: FormProps & {
  /** an audio workflow's text is spoken (docs/mock/CreateAudio.dc.html) */
  spoken?: boolean;
  /** music or sound (docs/mock/CreateMusicSound.dc.html): the prompt is a song's style or the sound in words */
  kind?: "music" | "sound";
}) {
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
              ? kind === "music"
                ? "Style: genre, instruments, voice, mood"
                : kind === "sound"
                  ? "Describe the sound"
                  : spoken
                    ? "Type the words to speak"
                    : ref
                      ? `Describe the edit; name the references ${mentionOf(ref.param, 1)}, ${mentionOf(ref.param, 2)}…`
                      : "Describe what you want to see"
              : (f.param.label ?? f.param.name)
          }
          rows={2}
          expand={
            f.param.name === "text"
              ? spoken
                ? "Words to speak"
                : "Prompt"
              : (f.param.label ?? f.param.name)
          }
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
  const first = firstRef(fs);
  const size = fromRef ? autoCanvas(ref, w, h, first) : { width: w, height: h };
  return { width, height, mode, w: size.width, h: size.height, auto, fromRef, first };
}

/** Whether the canvas's auto follows the first reference (else the last). */
const firstRef = (fs: Field[]) =>
  fs.some((f) => f.canvas && f.param.name === "mode" && FIRST_REF_CANVAS.has(f.kind ?? ""));

/** The size of the reference canvas auto follows (the last, or the first), once its take is known. */
function useAutoRef(fs: Field[], raws: Record<string, Raw>) {
  const last = imageInputs(fs)
    .flatMap((f) => refIds(f, raws[f.key]))
    .at(firstRef(fs) ? 0 : -1);
  const t = useTakes(last ? [last] : [])(last ?? "");
  return t?.width && t.height ? { width: t.width, height: t.height } : null;
}

/** "16:9 · 4 MP", else "1000 × 700 · 0.7 MP". */
export const sizeText = (w: number, h: number, grid = 1) =>
  `${aspectOf(w, h, grid) ?? `${w} × ${h}`} · ${mpText(w, h)}`;
/** "0.3 MP"; under 0.1 MP two significant digits ("0.033 MP"), never "0 MP". */
const mpText = (w: number, h: number) => {
  const mp = (w * h) / 1e6;
  return `${+(mp < 0.1 ? mp.toPrecision(2) : mp.toFixed(1))} MP`;
};

/** The `<node>.bypass` paths a choice option sets (LTX's Recipe), each with its value in the form: true = the
 * choice switched that node off (One-stage). */
export function choiceBypass(
  groups: { choices?: Choice[] }[] | undefined,
  fields: Field[],
  raws: Record<string, Raw>,
): Record<string, boolean> {
  const keys = new Set(
    (groups ?? [])
      .flatMap((g) => g.choices ?? [])
      .flatMap((c) => c.options.flatMap((o) => Object.keys(o.set)))
      .filter((k) => k.endsWith(".bypass")),
  );
  return Object.fromEntries(
    [...keys].map((k) => {
      const f = fields.find((x) => x.key === k);
      return [k, (f ? rawOf(f, raws) : raws[k]) === true];
    }),
  );
}

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
  presetOrder,
  presetHelp,
  presetScope,
  grid = null,
  stage1,
  voice,
  blocks = {},
  valueBlocks = {},
  addonOwner,
}: FormProps &
  OpenProps & {
    /** the nodes whose optional add-on is missing (API 1.36), each with why: their controls are greyed with it */
    blocks?: Record<string, string>;
    /** (API 1.41) field → value → why: an option whose add-on is missing where the run may go ("Turbo LoRA not
     * downloaded"); it and every preset that picks it are greyed */
    valueBlocks?: Record<string, Record<string, string>>;
    /** the model whose add-on a value block names ("MiniMax H3"): its tooltip says where to get it */
    addonOwner?: string;
    /** what a voice param's tile and picker need (API 1.30); with it, an audio workflow's text and number params
     * read as the board draws them (docs/mock/CreateAudio.dc.html: Style, Variation) */
    voice?: VoiceCtx;
    uploads: RefUploads;
    /** the Studio block's sections; `help` is the section's (i) text (API 1.25) */
    groups?: {
      title: string;
      help?: string;
      fields: Field[];
      choices?: Choice[];
      /** drawn under the title, before the fields (a dialogue's cast, its style per line) */
      head?: React.ReactNode;
    }[];
    familyTitle?: string;
    /** the canvas as Aspect and Size tiles (a video workflow, docs/mock/TextToVideo.dc.html) */
    compact?: boolean;
    /** what the runner chose for params set to auto in the shown run (`Run.resolved.params`) */
    resolved?: Record<string, number>;
    /** the workflow's presets (`studio.presets`): chips that set their values */
    presets?: Record<string, Record<string, S<"ParamValue">>>;
    /** the presets' names in the pack's order (`presetNames`); absent = as the object lists them */
    presetOrder?: string[];
    /** API 1.47: the Preset heading's (i) */
    presetHelp?: string;
    /** every field of the workflow, hidden ones too: a preset sets and reads a field its `when` hides (MiniMax H3's
     * Layers on Base fp8), so it reads picked on every variant; absent = `fields` */
    presetScope?: Field[];
    /** the family's frame grid: what a span, an extension or a part comes to */
    grid?: FrameGrid | null;
    /** a two-stage workflow: stage 1 runs at the canvas divided by `by` (LTX: 2); `clip`: the clip comes out at
     * that size (one-stage) */
    stage1?: Stage1;
  }) {
  // the presets that set an enum field to a value, in their order ("Quality, Low memory"; board LtxFeatures frame
  // 2's Compute menu): a preset sets only the keys it names
  const setBy = (f: Field) => (o: string) =>
    (presetOrder ?? Object.keys(presets ?? {}))
      .filter((n) => presets?.[n]?.[f.key] === o)
      .join(", ") || undefined;
  const s = sections(fields);
  const ext = extensionOf(fields);
  const clip = clipOf(fields, raws);
  const c = canvasOf(fields, raws, useAutoRef(fields, raws));
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
    !s.media.length &&
    !groups?.some((g) => g.fields.length || g.choices?.length)
  )
    return <p className="text-[13px] text-t2">Nothing to set.</p>;
  // the nodes switched off by their placed bypass ("No enhance" on): their settings show dimmed, still usable (a
  // bypass placed with its node's rows is that tile's "No keyframes", not a switch: not dimmed)
  const offNodes = new Set(
    fields
      .filter(
        (f) =>
          f.param.name === "bypass" &&
          rawOf(f, raws) === true &&
          !fields.some((x) => x.node === f.node && x.param.type === "rows"),
      )
      .map((f) => f.node),
  );
  const setting = (f: Field) => f.param.name !== "bypass";
  /** A control as `plain` draws it, dimmed while its node is off, greyed and inert while its add-on is missing. */
  const control = (f: Field, alone = false) => {
    const why = setting(f) ? blocks[f.node] : undefined;
    const dim = !why && setting(f) && offNodes.has(f.node);
    if (f.param.type === "bool" && (why || dim))
      return (
        <Switch key={f.key} f={f} raw={rawOf(f, raws)} onChange={onChange} dim={dim} why={why} />
      );
    const c = plain(f, alone);
    return c && (why || dim) ? (
      <div key={f.key} className="contents [&>*]:opacity-55" inert={!!why} title={why}>
        {c}
      </div>
    ) : (
      c
    );
  };
  /** `alone`: the only control of its section (a Voice section of its own draws its param's help under it) */
  const plain = (f: Field, alone = false) => {
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
              <Switch key={x.key} f={x} raw={rawOf(x, raws)} onChange={onChange} note />
            ))}
        />
      );
    if (ext && f === ext.direction) return null; // drawn with its seconds
    // the extension's cfg and stg: one "CFG · STG" tile that edits both (docs/mock/VideoSource.dc.html)
    const guide = (name: string) =>
      ext && fields.find((x) => x.node === ext.seconds.node && x.param.name === name);
    const cfg = guide("cfg");
    const stg = guide("stg");
    if (cfg && stg && f === stg) return null;
    if (ext && f === ext.seconds)
      return <ExtendControl key={f.key} {...ext} {...shared} onChange={onChange} />;
    if (f.param.type === "voice")
      return (
        voice && (
          <VoiceSet
            key={f.key}
            f={f}
            raws={raws}
            onChange={onChange}
            ctx={voice}
            error={errors[f.key]}
            help={alone ? f.param.help : undefined}
          />
        )
      );
    if (f.param.type === "bool")
      return <Switch key={f.key} f={f} raw={raw} onChange={onChange} error={errors[f.key]} />;
    if (
      voice &&
      (f.param.type === "float" || f.param.type === "int") &&
      f.param.min != null &&
      f.param.max != null
    )
      return <Slider key={f.key} f={f} raw={raw} error={errors[f.key]} onChange={onChange} />;
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
          plain={compact}
        />
      );
    if (f.param.type === "string" || f.param.type === "text")
      return (
        <Line key={f.key} f={f} raw={raw} errors={errors} onChange={onChange} counted={!!voice} />
      );
    return (
      <Tile
        key={f.key}
        f={f}
        raw={raw}
        error={errors[f.key]}
        open={open === f.key}
        setOpen={setOpen}
        onChange={onChange}
        choices={
          allowedInts(f, raws)?.map(String) ??
          // a model param with one value left (a variant that loads at one precision): a fixed readout
          (f.model && f.param.enum?.length === 1 ? [display(f, f.param.enum[0]).value] : undefined)
        }
        pair={cfg && stg && f === cfg ? { f: stg, raw: rawOf(stg, raws) } : undefined}
        setBy={f.param.type === "enum" ? setBy(f) : undefined}
        facts={
          valueBlocks[f.key]
            ? (o) => ({ why: valueBlocks[f.key][o], owner: addonOwner })
            : undefined
        }
      />
    );
  };
  const rowsEditor = (f: Field) =>
    mediaItem(f.param) ? (
      <MediaRows
        key={f.key}
        f={f}
        raw={raws[f.key]}
        error={errors[f.key]}
        onChange={onChange}
        sound={soundRowsOf(f, presetScope ?? fields, raws)}
      />
    ) : s.rows.includes(f) ? (
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
    <Presets
      key="presets"
      presets={presets}
      order={presetOrder ?? Object.keys(presets)}
      help={presetHelp}
      fields={presetScope ?? fields}
      raws={raws}
      onChange={onChange}
      blocks={blocks}
      valueBlocks={valueBlocks}
      owner={addonOwner}
    />
  );
  // a choice tile (API 1.26): an enum of its options' names, showing the first whose values hold; picking one sets
  // its values
  const choiceTile = (c: Choice) => {
    const f = choiceField(c);
    const holds = (set: Record<string, S<"ParamValue">>) =>
      Object.entries(set).every(([k, v]) => {
        const x = fields.find((y) => y.key === k);
        return x && JSON.stringify(rawOf(x, raws)) === JSON.stringify(toRaw(v, x.param));
      });
    // an option's line under its name, and why it is greyed: it switches on a node whose add-on is missing (Recipe:
    // Detail without the Detailer, docs/mock/LtxFeatures.dc.html frame 16)
    const facts = (name: string) => {
      const o = c.options.find((x) => x.name === name);
      const why = Object.entries(o?.set ?? {}).flatMap(([k, v]) => {
        const [node, param] = k.split(".");
        return param === "bypass" && v === false && blocks[node] ? [blocks[node]] : [];
      })[0];
      return { sub: o?.help, why };
    };
    return (
      <Tile
        key={f.key}
        f={f}
        raw={c.options.find((o) => holds(o.set))?.name ?? "custom"}
        open={open === f.key}
        setOpen={setOpen}
        facts={facts}
        onChange={(_, name) =>
          Object.entries(c.options.find((o) => o.name === name)?.set ?? {}).forEach(([k, v]) => {
            const x = fields.find((y) => y.key === k);
            if (x) onChange(k, toRaw(v, x.param));
          })
        }
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
    choices: Choice[] = [],
    note?: string,
  ) =>
    (fs.length > 0 || head || choices.length > 0) && (
      <section key={id} aria-labelledby={id} className={`flex flex-col ${gap}`}>
        <h2 id={id} className="heading flex items-center">
          {title}
          {help && <Info>{help}</Info>}
          {note && <span className="ml-2">{note}</span>}
        </h2>
        {head}
        {(fs.length > 0 || choices.length > 0) && (
          <div className="grid grid-flow-row-dense grid-cols-2 gap-2">
            {choices.map(choiceTile)}
            {fs.map((f) => control(f, fs.length === 1))}
          </div>
        )}
      </section>
    );
  if (groups) {
    // a node a choice switched off (One-stage's sample2) hides its tiles; a bypass the form sets itself (Audio to
    // video's keyframes) is not a choice's, so its tile stays
    const off = Object.entries(choiceBypass(groups, fields, raws))
      .filter(([, on]) => on)
      .map(([k]) => k.slice(0, -".bypass".length));
    return [
      presetChips,
      ...groups.map((all, i) => {
        const g = { ...all, fields: all.fields.filter((f) => !off.includes(f.node)) };
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
          (refs.length > 0 || loras.length > 0 || canvas || !!g.head) && (
            <>
              {g.head}
              {loras.map((f) => (
                <Loras key={f.key} f={f} raws={raws} errors={errors} onChange={onChange} />
              ))}
              {refs.map(rowsEditor)}
              {canvas && (
                <CanvasPicker
                  c={c}
                  errors={errors}
                  onChange={onChange}
                  compact={compact}
                  stage1={stage1}
                />
              )}
            </>
          ),
          g.help ??
            (loras[0]
              ? loraHelp(familyTitle ?? loras[0].family ?? "this model", loras[0])
              : (refs[0]?.param.help ??
                (g.fields.length === 1 &&
                s.media.includes(g.fields[0]) &&
                g.fields[0].param.type === "media"
                  ? g.fields[0].param.help
                  : undefined))),
          undefined,
          g.choices,
          // every setting here belongs to a node switched off (not for want of its add-on): "Enhance is off"
          g.fields.length > 0 &&
            g.fields.every((f) => setting(f) && offNodes.has(f.node) && !blocks[f.node])
            ? `${g.title} is off`
            : undefined,
        );
      }),
    ];
  }
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
        c && (
          <CanvasPicker
            c={c}
            errors={errors}
            onChange={onChange}
            compact={compact}
            stage1={stage1}
          />
        ),
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
  stage1,
  popup,
}: {
  c: NonNullable<ReturnType<typeof canvasOf>>;
  errors: Record<string, string>;
  onChange: (key: string, raw: Raw) => void;
  compact?: boolean;
  stage1?: Stage1;
  /** in a chip's popup (alpha.5 item 10, docs/mock/TextToVideo.dc.html frame 4): Aspect and Size as options */
  popup?: boolean;
}) {
  const { width, height, mode, w, h } = c;
  // compact: which tile's choices are open
  const [opened, setOpened] = useState<"aspect" | "size" | null>(null);
  const grid = width.param.step ?? 1;
  const min = Math.max(width.param.min ?? 0, height.param.min ?? 0);
  const max = Math.min(width.param.max ?? Infinity, height.param.max ?? Infinity);
  // ponytail: by kind, like FIRST_REF_CANVAS; a schema hint on the canvas would make this generic
  const choices = megapixelChoices(min, max, FIRST_REF_CANVAS.has(width.kind ?? "") ? Infinity : 4);
  const fit = (ratio: number, mp: number) => sizeFor(ratio, mp, grid, min, max);
  // a size picked is fixed: in an edit, Auto would follow a reference instead
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
      className="set bevel flex flex-col gap-0.5 text-left"
      aria-expanded={opened === k}
      onClick={() => setOpened(opened === k ? null : k)}
    >
      <small className="text-[12px] text-t2">{label}</small>
      <b className="font-semibold">{value}</b>
    </button>
  );
  const autoBtn = mode && (
    <button
      type="button"
      className="opt bevel"
      aria-pressed={aspect === "Auto"}
      title={
        mode.param.help ??
        "The workflow's own size; with references, about 1 MP at the last one's aspect"
      }
      onClick={() => {
        onChange(mode.key, "auto");
        onChange(width.key, toRaw(width.initial, width.param));
        onChange(height.key, toRaw(height.initial, height.param));
      }}
    >
      Auto
    </button>
  );
  const aspects = ASPECTS.map(([label, ratio]) => (
    <button
      key={label}
      type="button"
      className="opt bevel"
      aria-pressed={aspect === label}
      onClick={() => set(fit(ratio, mp))}
    >
      {label}
    </button>
  ));
  // a video's line has no megapixels (the Size tile says them); a two-stage one adds stage 1's size
  // (docs/mock/TextToVideo.dc.html)
  const line = (
    <span className="mono text-[12px] text-t2">
      {w} × {h}
      {!compact && ` · ${mpText(w, h)}`}
      {c.fromRef
        ? ` · from the ${c.first ? "first" : "last"} reference`
        : grid > 1 && ` · on the ${grid} px grid`}
      {stage1 &&
        ` · ${stage1.clip ? "one-stage: the clip is" : "stage 1 runs at"} ${Math.round(w / stage1.by)} × ${Math.round(h / stage1.by)}`}
    </span>
  );
  if (popup)
    return (
      <>
        <div role="group" aria-label="Aspect" className="flex flex-col gap-1.5">
          <span className="heading">Aspect</span>
          <div className="flex flex-wrap gap-1.5">
            {autoBtn}
            {aspects}
          </div>
        </div>
        <div role="group" aria-label="Size" className="flex flex-col gap-1.5">
          <span className="heading">Size</span>
          <div className="flex flex-wrap gap-1.5">
            {choices.map((x) => (
              <button
                key={x}
                type="button"
                className="opt bevel"
                aria-pressed={mp === x}
                onClick={() => set(fit(ratio, x))}
              >
                {x} MP
              </button>
            ))}
          </div>
        </div>
        {line}
      </>
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
        {autoBtn}
        {aspects}
      </div>
      {/* the size: a stepped slider, one stop per choice (seven chips did not fit the aside's width) */}
      <div className="flex flex-col gap-1.5" hidden={compact && opened !== "size"}>
        <label htmlFor="canvas-size" className="flex justify-between gap-2">
          <span>Size</span>
          <span className="mono text-[12px] text-t2">{mp} MP</span>
        </label>
        <input
          id="canvas-size"
          type="range"
          min={0}
          max={choices.length - 1}
          step={1}
          value={choices.indexOf(mp)}
          aria-valuetext={`${mp} MP`}
          onChange={(e) => set(fit(ratio, choices[Number(e.target.value)]))}
          className="w-full accent-a1"
        />
        <span className="mono flex justify-between text-[12px] text-t2" aria-hidden>
          <span>{choices[0]} MP</span>
          <span>{choices.at(-1)} MP</span>
        </span>
      </div>
      {line}
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
  plain,
  id = idOf(f.key),
}: {
  /** the control's id: a chip's popup gives its own, so the drawer's stays unique */
  id?: string;
  f: Field;
  raw: Raw;
  fps: number;
  error?: string;
  onChange: (key: string, raw: Raw) => void;
  resolved?: number;
  /** without the (i) (a video workflow's Length, docs/mock/TextToVideo.dc.html) */
  plain?: boolean;
}) {
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
          {f.param.help && !plain && <Info>{f.param.help}</Info>}
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

/** The workflow's presets (`studio.presets`) as chips in `order` (the pack's): one sets its values; it reads pressed
 * while the form holds them all. */
function Presets({
  presets,
  order,
  fields,
  raws,
  onChange,
  blocks = {},
  valueBlocks = {},
  owner,
  help,
}: Omit<FormProps, "errors"> & {
  presets: Record<string, Record<string, S<"ParamValue">>>;
  order: string[];
  help?: string;
  blocks?: Record<string, string>;
  valueBlocks?: Record<string, Record<string, string>>;
  /** the model's name in a value block's tooltip */
  owner?: string;
}) {
  // a preset that switches on a node whose add-on is missing ("Enhanced"), or (API 1.41) picks a value whose add-on
  // is (Turbo: "Turbo LoRA not downloaded"), is greyed with the reason
  const byValue = (values: Record<string, S<"ParamValue">>) =>
    Object.entries(values).flatMap(([k, v]) => valueBlocks[k]?.[String(v)] ?? [])[0];
  const blocked = (values: Record<string, S<"ParamValue">>) =>
    byValue(values) ??
    Object.entries(values).flatMap(([k, v]) => {
      const [node, param] = k.split(".");
      return param === "bypass" && v === false && blocks[node] ? [blocks[node]] : [];
    })[0];
  const holds = (values: Record<string, S<"ParamValue">>) =>
    Object.entries(values).every(([k, v]) => {
      const f = fields.find((x) => x.key === k);
      return f && JSON.stringify(rawOf(f, raws)) === JSON.stringify(toRaw(v, f.param));
    });
  // the first value block's reason, under the chips (H3Options frames 11-13: "Turbo LoRA not downloaded")
  const reason = order.map((n) => presets[n] && byValue(presets[n])).find(Boolean);
  return (
    <section aria-labelledby="sec-preset" className="flex flex-col gap-2">
      <h2 id="sec-preset" className="heading flex items-center">
        Preset
        {help && <Info>{help}</Info>}
      </h2>
      <div className="flex flex-wrap gap-2">
        {order
          .flatMap((name) => (presets[name] ? [[name, presets[name]] as const] : []))
          .map(([name, values]) => {
            const why = blocked(values);
            // a form that holds it anyway (a Remix of a Turbo take) shows it picked and greyed (H3Options frame 12)
            return why ? (
              <button
                key={name}
                type="button"
                aria-disabled
                aria-pressed={holds(values)}
                title={byValue(values) ? addonHint(why, owner) : why}
                className="h-9 cursor-not-allowed rounded-full border border-line bg-transparent px-3.5 text-[13px] text-t2 aria-pressed:border-dashed aria-pressed:border-a2"
              >
                <span className="opacity-55">{name}</span>
              </button>
            ) : (
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
            );
          })}
      </div>
      {reason && <span className="text-[12px] text-t2">{reason}</span>}
    </section>
  );
}

type Stage1 = { by: number; clip: boolean };

/** A choice option's line under its name (`help`) and why it is greyed (its add-on is missing). */
type OptionFacts = { sub?: string; why?: string; owner?: string };

/** A greyed control's tooltip: where an admin gets the missing add-on (`owner`: the model's name, H3Options). */
const addonHint = (why: string, owner?: string) =>
  `${why}. An admin downloads it in Models, under ${owner ? `${owner}'s` : "the model's"} optional add-ons.`;

type Choice = NonNullable<S<"StudioLayout">["inspector"]>[number]["choices"] extends
  (infer C)[] | undefined
  ? C
  : never;

/** A choice tile as an enum field of its options' names (never sent: its options set the real paths). */
const choiceField = (c: Choice): Field => ({
  key: `choice:${c.label}`,
  node: "",
  nodeTitle: "",
  param: {
    name: c.label,
    label: c.label,
    type: "enum",
    enum: c.options.map((o) => o.name),
    default: c.options[0].name,
    help: c.help,
  },
  initial: c.options[0].name,
  canvas: false,
  model: false,
});

/** A label-over-value tile; it opens its editor below its row. */
function Tile({
  f,
  raw,
  error,
  open,
  setOpen,
  onChange,
  choices,
  pair,
  setBy,
  facts,
}: {
  f: Field;
  raw: Raw;
  error?: string;
  open: boolean;
  setOpen: (key: string | null) => void;
  onChange: (key: string, raw: Raw) => void;
  /** the only values it takes (an int's `values`): one is a readout, several a choice */
  choices?: string[];
  /** a second field shown and edited with this one ("CFG · STG 3.0 · 1.0") */
  pair?: { f: Field; raw: Raw };
  /** an enum's value → the presets that set it (shown beside the value in its editor) */
  setBy?: (value: string) => string | undefined;
  /** a choice option's line under its name and why it is greyed */
  facts?: (value: string) => OptionFacts;
}) {
  const id = idOf(f.key);
  const d = display(f, raw);
  const p2 = pair && display(pair.f, pair.raw);
  const value = f.param.type === "seed" && raw === "" ? "Random" : d.value || "—";
  const shown = p2 ? `${value} · ${p2.value || "—"}` : value;
  const label = p2 ? `${d.label} · ${p2.label}` : d.label;
  if (choices?.length === 1)
    return (
      <div id={id + "-ed"} className="set flex flex-col gap-0.5" title={f.param.help}>
        <span className="text-[12px] text-t2">{d.label}</span>
        <span className={`font-semibold ${f.param.type === "enum" ? "text-t2" : ""}`}>
          {choices[0]}
        </span>
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
            <span className="text-[12px] text-t2">{label}</span>
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
      {open &&
        [{ f, raw }, ...(pair ? [pair] : [])].map((e, i) => (
          <Editor
            key={e.f.key}
            f={e.f}
            raw={e.raw}
            choices={i ? undefined : choices}
            setBy={i ? undefined : setBy}
            facts={i ? undefined : facts}
            onChange={onChange}
            focus={!i}
            close={() => {
              setOpen(null);
              document.getElementById(id + "-tile")?.focus();
            }}
          />
        ))}
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
  focus = true,
  setBy,
  facts,
  noAuto,
}: {
  f: Field;
  raw: Raw;
  onChange: (key: string, raw: Raw) => void;
  close: () => void;
  /** a group's popup has one Auto for all its settings (TempoKey): none of its own */
  noAuto?: boolean;
  choices?: string[];
  setBy?: (value: string) => string | undefined;
  facts?: (value: string) => OptionFacts;
  /** move focus into it when it opens (the first of a pair's editors) */
  focus?: boolean;
}) {
  const p = f.param;
  const id = idOf(f.key);
  const label = p.label ?? p.name;
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (focus) ref.current?.querySelector<HTMLElement>("input, .opt")?.focus();
  }, [focus]);
  // API 1.79 `auto: true`: 0 is Auto (the model or its plan picks it); the number shows empty, the slider idles
  const auto = p.auto === true && Number(raw) === 0 && String(raw) !== "";
  const num = (
    <input
      id={id}
      className="mono h-9 w-full min-w-0 rounded-lg border-0 bg-s4 px-2 text-[13px] font-semibold text-t1 placeholder:font-normal placeholder:text-t2"
      type="number"
      inputMode={p.type === "float" ? "decimal" : "numeric"}
      min={p.type === "seed" ? 0 : p.min}
      max={p.max}
      step={p.step ?? (p.type === "float" ? "any" : undefined)}
      placeholder={p.type === "seed" ? "Random" : auto ? "Auto" : undefined}
      value={auto ? "" : String(raw)}
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
        // API 1.47: a short menu whose values have lines is a list (docs/mock/H3Options.dc.html: Render, Reuse, LoRA)
        <div
          className={
            (choices ?? p.enum ?? []).length <= 6 &&
            (choices ?? p.enum)?.some((o) => facts?.(o)?.sub ?? p.option_help?.[o])
              ? "flex flex-col gap-1.5 [&>button]:w-full"
              : "flex flex-wrap gap-1.5"
          }
        >
          {(choices ?? p.enum)?.map((o) => {
            const { why, owner, sub: own } = facts?.(o) ?? {};
            // API 1.47: the value's line from the schema (option_help) unless the choice gives its own
            const sub = own ?? p.option_help?.[o];
            return (
              <button
                key={o}
                type="button"
                className={`opt bevel min-w-16 px-3 text-[13px] ${sub ? "flex h-auto min-h-9 flex-col items-start py-1.5 text-left" : ""} ${why ? "cursor-not-allowed" : ""}`}
                aria-pressed={raw === o}
                aria-label={sub || why ? optionText(p, o, f.kind) : undefined}
                aria-disabled={why ? true : undefined}
                title={why ? addonHint(why, owner) : undefined}
                onClick={() => !why && onChange(f.key, o)}
              >
                <span className={why ? "opacity-55" : undefined}>{optionText(p, o, f.kind)}</span>
                {sub && (
                  <small className={`text-[11px] text-t2 ${why ? "opacity-55" : ""}`}>{sub}</small>
                )}
                {why && <em className="text-[11px] text-t2 not-italic">{why}</em>}
                {setBy?.(o) && (
                  <em className="ml-1.5 text-[11px] text-t2 not-italic">{setBy(o)}</em>
                )}
              </button>
            );
          })}
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
              className={`w-full ${p.auto === true ? "accent-a2" : "accent-a1"} ${auto ? "opacity-55" : ""}`}
            />
          )}
          {p.auto === true && !noAuto && (
            <AutoRow
              on={auto}
              note={p.help}
              onChange={(on) => onChange(f.key, on ? "0" : String(p.min ?? 1))}
            />
          )}
        </>
      )}
    </div>
  );
}

/** The Auto row of an `auto` setting's editor (docs/mock/CreateMusicSound.dc.html 2c, 2d; TempoKey): "Auto", the
 * line on what picks it, and an a2 switch. */
export function AutoRow({
  on,
  note,
  onChange,
}: {
  on: boolean;
  note?: string;
  onChange: (on: boolean) => void;
}) {
  return (
    <div className="flex items-center justify-between gap-3">
      <span className="flex flex-col gap-0.5">
        <span className="text-[13px] text-t1">Auto</span>
        {note && <span className="text-[12px] text-t2">{note}</span>}
      </span>
      <button
        type="button"
        role="switch"
        aria-checked={on}
        aria-label="Auto"
        onClick={() => onChange(!on)}
        className="flex h-[26px] w-11 shrink-0 rounded-full border border-line p-0.5"
        style={{ background: on ? "var(--a2)" : "var(--s1)" }}
      >
        <span
          className="block size-5 rounded-full transition-transform"
          style={{
            background: on ? "var(--on2)" : "var(--t2)",
            transform: on ? "translateX(18px)" : undefined,
          }}
        />
      </button>
    </div>
  );
}

/** A bool param: its label and a switch. */
export function Switch({
  f,
  raw,
  onChange,
  note,
  dim,
  why,
  error,
}: {
  f: Field;
  raw: Raw;
  onChange: (key: string, raw: Raw) => void;
  /** API 1.47: a broken rule's text, in red under the switch (docs/mock/H3Options.dc.html frame 4) */
  error?: string;
  /** the help as a line under the label, not an (i) (a span's switch, docs/mock/VideoSource.dc.html) */
  note?: boolean;
  /** its node is bypassed: shown dimmed, still usable (docs/mock/CreateEnhance.dc.html, "Enhance is off") */
  dim?: boolean;
  /** why it cannot be used (its node's add-on is missing, "Enhancer not downloaded"): greyed, the reason under it */
  why?: string;
}) {
  const id = idOf(f.key);
  const on = raw === true;
  const row = (
    <div className={`col-span-2 flex items-center justify-between gap-3 ${why ? "text-t2" : ""}`}>
      {why ? (
        <span className="flex flex-col gap-0.5">
          <span id={id + "-l"} className="opacity-55">
            {f.param.label ?? f.param.name}
          </span>
          <span className="text-[12px]">{why}</span>
        </span>
      ) : note ? (
        <span className="flex flex-col gap-0.5">
          <span id={id + "-l"}>{f.param.label ?? f.param.name}</span>
          {f.param.help && <span className="text-[12px] text-t2">{f.param.help}</span>}
        </span>
      ) : (
        <span className={`flex items-center ${dim ? "opacity-55" : ""}`}>
          <span id={id + "-l"}>{f.param.label ?? f.param.name}</span>
          {f.param.help && <Info>{f.param.help}</Info>}
        </span>
      )}
      <button
        id={id}
        type="button"
        role="switch"
        aria-checked={on}
        aria-labelledby={id + "-l"}
        aria-disabled={why ? true : undefined}
        onClick={() => !why && onChange(f.key, !on)}
        className={`flex h-[26px] w-11 shrink-0 rounded-full border border-line p-0.5 ${why ? "cursor-not-allowed" : ""} ${dim ? "opacity-55" : ""}`}
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
  return error ? (
    <div className="col-span-2 flex flex-col gap-1">
      {row}
      <span id={id + "-err"} className="mono text-[12px] text-bad">
        {error}
      </span>
    </div>
  ) : (
    row
  );
}

/** A number with bounds as a slider: its label and value over it, the help under it (docs/mock/CreateAudio.dc.html,
 * Variation). */
function Slider({
  f,
  raw,
  error,
  onChange,
}: {
  f: Field;
  raw: Raw;
  error?: string;
  onChange: (key: string, raw: Raw) => void;
}) {
  const id = idOf(f.key);
  const p = f.param;
  const auto = isAuto(p, raw);
  return (
    <div id={id + "-ed"} className="col-span-2 flex flex-col gap-2.5">
      <label htmlFor={id} className="flex justify-between gap-2">
        <span>{p.label ?? p.name}</span>
        {/* the board's plain number ("0.45", "2"), not a tile's "2.0"; a speaking rate as "0.95×"
            (CreateAudioZonos3.dc.html); an Auto value as Auto (API 1.79), a length as a clock (Music's "2:10") */}
        <span className="mono text-[12px] text-t2">
          {auto || (p.type === "float" && p.name === "seconds" && p.auto)
            ? chipText(f, raw)
            : String(Math.round((Number(raw) || p.min!) * 100) / 100)}
          {p.name === "speaking_rate" && "×"}
        </span>
      </label>
      <input
        id={id}
        type="range"
        min={p.min}
        max={p.max}
        step={p.step ?? (p.type === "float" ? 0.05 : 1)}
        value={Number(raw) || p.min}
        aria-invalid={!!error || undefined}
        onChange={(e) => onChange(f.key, e.target.value)}
        className={`w-full accent-a2 ${auto ? "opacity-55" : ""}`}
      />
      {p.auto === true ? (
        <AutoRow
          on={auto}
          note={p.help}
          onChange={(on) => onChange(f.key, on ? "0" : String(p.min ?? 1))}
        />
      ) : (
        p.help && <span className="text-[12px] text-t2">{p.help}</span>
      )}
      <FieldError k={f.key} errors={error ? { [f.key]: error } : {}} />
    </div>
  );
}

/** A string param: a labelled line of text. `counted`: its length over its max beside the label and the help under
 * the line (docs/mock/CreateAudio.dc.html, Style). */
function Line({
  f,
  raw,
  errors,
  onChange,
  counted,
}: { f: Field; raw: Raw; counted?: boolean } & Omit<FormProps, "fields" | "raws">) {
  const id = idOf(f.key);
  // a multi-line text param (not a voice line): a box under its section's title, the label for screen readers, and
  // its count (docs/mock/LtxFeatures.dc.html frame 3, the Negative prompt)
  if (f.param.type === "text" && !counted)
    return (
      <div className="col-span-2 flex flex-col gap-1.5">
        <label htmlFor={id} className="sr-only">
          {f.param.label ?? f.param.name}
        </label>
        <TextArea
          id={id}
          expand={f.param.label ?? f.param.name}
          className="in h-auto resize-none py-2 leading-[19px]"
          rows={2}
          maxLength={f.param.max}
          title={f.param.help}
          value={String(raw)}
          aria-invalid={!!errors[f.key] || undefined}
          aria-describedby={errors[f.key] ? id + "-err" : undefined}
          onChange={(e) => onChange(f.key, e.target.value)}
        />
        {f.param.max != null && (
          <span className="mono text-[11px] text-t2">
            {String(raw).length} / {f.param.max}
          </span>
        )}
        <FieldError k={f.key} errors={errors} />
      </div>
    );
  if (counted)
    return (
      <div className="col-span-2 flex flex-col gap-2.5">
        <label htmlFor={id} className="flex justify-between gap-2">
          <span>{f.param.label ?? f.param.name}</span>
          {f.param.max != null && (
            <span className="mono text-[12px] text-t2">
              {String(raw).length} / {f.param.max}
            </span>
          )}
        </label>
        <input
          id={id}
          className="h-10 rounded-[10px] border border-s5 bg-s1 px-3 text-sm text-t1"
          maxLength={f.param.max}
          value={String(raw)}
          aria-invalid={!!errors[f.key] || undefined}
          aria-describedby={errors[f.key] ? id + "-err" : undefined}
          onChange={(e) => onChange(f.key, e.target.value)}
        />
        {f.param.help && <span className="text-[12px] text-t2">{f.param.help}</span>}
        <FieldError k={f.key} errors={errors} />
      </div>
    );
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

/** A chip and its popup, anchored under it (alpha.5 item 10, docs/mock/TextToVideo.dc.html frames 2-5): one open at
 * a time (`open` is the parent's), Esc or a click outside closes it and focus goes back to the chip; `all` adds the
 * All settings link at its foot. */
function ChipPop({
  id,
  label,
  text,
  title,
  open,
  setOpen,
  all,
  width = "w-75",
  children,
}: {
  id: string;
  label: string;
  text: React.ReactNode;
  title?: string;
  open: boolean;
  setOpen: (id: string | null) => void;
  all?: () => void;
  width?: string;
  children: React.ReactNode;
}) {
  const pop = useRef<HTMLDivElement>(null);
  const chipEl = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    const el = pop.current;
    if (!el) return;
    if (!open) {
      if (el.matches?.(":popover-open")) el.hidePopover?.();
      return;
    }
    el.showPopover?.();
    el.querySelector<HTMLElement>(
      "[aria-checked=true], [aria-pressed=true], input:not(:disabled), button",
    )?.focus();
    const close = (back: boolean) => {
      setOpen(null);
      if (back) chipEl.current?.focus();
    };
    const down = (e: PointerEvent) => {
      const t = e.target as Node;
      if (!el.contains(t) && !chipEl.current?.contains(t)) close(false);
    };
    const key = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.preventDefault();
        close(true);
      }
    };
    document.addEventListener("pointerdown", down, true);
    document.addEventListener("keydown", key);
    return () => {
      document.removeEventListener("pointerdown", down, true);
      document.removeEventListener("keydown", key);
    };
  }, [open, setOpen]);
  return (
    <>
      <button
        ref={chipEl}
        type="button"
        className={`${CHIP} aria-expanded:bg-s5`}
        aria-haspopup="dialog"
        aria-expanded={open}
        aria-controls={id}
        title={title}
        style={{ anchorName: `--${id}` } as React.CSSProperties}
        onClick={() => setOpen(open ? null : id)}
      >
        {text}
      </button>
      <div
        id={id}
        ref={pop}
        popover="manual"
        role="dialog"
        aria-label={label}
        className={`pop ${width}`}
        style={
          {
            positionAnchor: `--${id}`,
            positionArea: "bottom span-right",
            marginTop: 6,
            // a phone: it stays on screen (under the chip, centred when neither side fits), scrolling if tall
            positionTryFallbacks: "flip-inline, bottom span-all, flip-block, top span-all",
            maxWidth: "calc(100vw - 16px)",
            maxHeight: "calc(100dvh - 16px)",
            overflowY: "auto",
          } as React.CSSProperties
        }
      >
        {open && (
          <div className="flex flex-col gap-2.5">
            {children}
            {all && (
              <button
                type="button"
                className="h-7 self-start rounded-lg px-2.5 text-[12px] text-t2 underline underline-offset-3"
                onClick={() => {
                  setOpen(null);
                  all();
                }}
              >
                All settings
              </button>
            )}
          </div>
        )}
      </div>
    </>
  );
}

const CHIP = "chip bevel rounded-[10px] bg-s4";

/** A row of a music or sound model chip's popup (ModelPopup): the model, its line, picked or not. */
export type KindModel = {
  key: string;
  title: string;
  sub?: string;
  checked: boolean;
  /** why it cannot be picked (not downloaded) */
  why?: string;
  onPick: () => void;
};

/** The composer's quick chips: the model node's choices (the node that outputs a Model), the canvas, the length, the
 * seed (with Random). Each opens a popup with its setting (alpha.5 item 10), on narrow windows too (owner,
 * 2026-11-26); a popup's All settings `show`s the drawer at its section (a field key, or "canvas"). */
export function Chips({
  fields,
  raws,
  onChange,
  show,
  compact,
  toggles = [],
  models,
  family,
  spoken,
  runOn,
  versions,
  blocks = {},
  valueBlocks = {},
  stage1,
  values = [],
  groups = [],
  greyed = [],
  off = {},
  kindModels,
}: Omit<FormProps, "errors"> & {
  /** music and sound (docs/mock/CreateMusicSound.dc.html): quick int, float and enum settings as chips, each with its
   * popup (Length with Auto, Vocal language) */
  values?: Field[];
  /** quick settings shown as one chip with one popup (`studio.groups`, TempoKey: "BPM · key") */
  groups?: { label: string; fields: Field[] }[];
  /** chips another model of the kind has and this one lacks, greyed in place with why ("Not on MiniMax Music 3") */
  greyed?: { label: string; why: string }[];
  /** field → why its chip is greyed now (Language while Instrumental is on) */
  off?: Record<string, string>;
  /** the model chip of a music or sound kind (ModelPopup): the kind's models, each pickable, the open one first */
  kindModels?: { title: string; rows: KindModel[] };
  /** the nodes whose optional add-on is missing (API 1.36), each with why: its bypass chip is greyed, held on */
  blocks?: Record<string, string>;
  /** (API 1.41) field → value → why: a precision whose add-on is missing is greyed in the model popup */
  valueBlocks?: Record<string, Record<string, string>>;
  /** what the Size popup's pixel line says of stage 1 (as the drawer's Canvas) */
  stage1?: Stage1;
  show: (key: string) => void;
  /** a video workflow's chips (docs/mock/TextToVideo.dc.html): the size as w × h, no seed chip */
  compact?: boolean;
  /** bool params placed on the composer (`studio.quick`): a chip each that switches it ("Audio on") */
  toggles?: Field[];
  /** the workflow's model choices (variant, precision): the chip names them all ("Base · int8"); a placed variant
   * with several values is picked from the chip's menu, as rows (docs/mock/RunOn.dc.html, 2), a placed precision
   * as options (TextToVideo frame 2); the rest are the inspector's tiles (Quality) */
  models?: Field[];
  /** the family's title: the chip's tooltip leads with it (the chip itself does not repeat it) */
  family?: string;
  /** an audio workflow (docs/mock/CreateAudio.dc.html): the chip leaves out one-value choices ("Custom voice", not
   * "Custom voice · bf16") */
  spoken?: boolean;
  /** the Run on chip, right after the model chip (docs/mock/RunOn.dc.html) */
  runOn?: React.ReactNode;
  /** a variant value's row in the model chip's menu: its title, line (API 1.39), note ("NVIDIA only") and dim text
   * under Run on a host ("not on Mac") */
  versions?: (
    f: Field,
    o: string,
  ) => {
    title: string;
    sub?: string;
    note: string | null;
    dim: string | null;
    /** an add-on it needs is missing (API 1.40): greyed and not pickable */
    why?: string | null;
  } | null;
}) {
  const [pop, setPop] = useState<string | null>(null);
  const s = sections(fields);
  const model = (models ?? s.tiles).filter((f) => f.model && f.param.type === "enum");
  const first = model[0];
  const c = canvasOf(fields, raws, useAutoRef(fields, raws));
  const seed = compact ? undefined : s.tiles.find((f) => f.param.type === "seed");
  const frames = s.duration.filter((f) => f.param.type === "frames");
  const chip = CHIP;
  const random = seed && rawOf(seed, raws) === "";
  const placed = (f: Field) =>
    fields.some((x) => x.key === f.key) && (f.param.enum?.length ?? 0) > 1;
  // the chip's menu: the placed variants that have more than one value, then the placed precisions
  const pickable = model.filter((f) => f.param.name === "variant" && placed(f));
  const precisions = model.filter((f) => f.param.name !== "variant" && placed(f));
  const choosable = model.filter((f) => (f.param.enum?.length ?? 0) > 1 || f === first);
  const named = prettyModel((spoken ? choosable : model).map((f) => String(rawOf(f, raws))));
  const modelText = named.join(" · ");
  const modelTitle = [family, ...model.map((f) => f.param.label ?? f.param.name)]
    .filter(Boolean)
    .join(" · ");
  const sizeLabel = c
    ? compact
      ? `${c.w} × ${c.h}`
      : c.fromRef
        ? `Auto · ${sizeText(c.w, c.h, c.width.param.step)}`
        : sizeText(c.w, c.h, c.width.param.step)
    : "";
  return (
    <>
      {kindModels && (
        <ChipPop
          id="model-pick"
          label="Model"
          text={kindModels.title}
          title={family}
          open={pop === "model-pick"}
          setOpen={setPop}
          width="w-80"
          all={() => show(first?.key ?? "")}
        >
          <span className="heading">Model</span>
          <div role="menu" aria-label="Models" className="flex flex-col gap-0.5">
            {kindModels.rows.map((r) => (
              <button
                key={r.key}
                type="button"
                role="menuitemradio"
                aria-checked={r.checked}
                className={`mi ${r.why ? "dim" : ""}`}
                aria-disabled={r.why ? true : undefined}
                title={r.why}
                onClick={() => {
                  if (r.why) return;
                  setPop(null);
                  if (!r.checked) r.onPick();
                }}
              >
                <span>
                  {r.title}
                  {r.sub && <small>{r.sub}</small>}
                </span>
              </button>
            ))}
          </div>
        </ChipPop>
      )}
      {!kindModels && first && (pickable.length > 0 || precisions.length > 0) && (
        <ChipPop
          id="model-pick"
          label="Model"
          text={modelText}
          title={modelTitle}
          open={pop === "model-pick"}
          setOpen={setPop}
          width="w-72"
          all={() => show((precisions[0] ?? first).key)}
        >
          {pickable.map((f) => (
            <div
              key={f.key}
              role="group"
              aria-label={display(f, "").label}
              className="flex flex-col gap-0.5"
            >
              <span className="flex items-center pb-1 text-[12px] text-t2">
                {display(f, "").label}
                {f.param.help && <Info>{f.param.help}</Info>}
              </span>
              {f.param.enum?.map((o) => {
                const r = versions?.(f, o);
                const end = r?.why ?? r?.dim ?? r?.note;
                return (
                  <button
                    key={o}
                    type="button"
                    role="menuitemradio"
                    aria-checked={rawOf(f, raws) === o}
                    className={`mi ${r?.dim || r?.why ? "dim" : ""}`}
                    aria-disabled={r?.why ? true : undefined}
                    title={r?.why ? addonHint(r.why) : undefined}
                    onClick={() => !r?.why && onChange(f.key, o)}
                  >
                    <span>
                      {r?.title ?? pretty(o)}
                      {r?.sub && <small>{r.sub}</small>}
                    </span>
                    {end && <em>{end}</em>}
                  </button>
                );
              })}
            </div>
          ))}
          {precisions.map((f) => (
            <div
              key={f.key}
              role="group"
              aria-label={display(f, "").label}
              className="flex flex-col gap-1.5"
            >
              <span className="text-[12px] text-t2">{display(f, "").label}</span>
              <div className="flex flex-wrap gap-1.5">
                {f.param.enum?.map((o) => {
                  const why = valueBlocks[f.key]?.[o];
                  return (
                    <button
                      key={o}
                      type="button"
                      className={`opt bevel min-w-16 px-3 text-[13px] ${why ? "cursor-not-allowed" : ""}`}
                      aria-pressed={rawOf(f, raws) === o}
                      aria-disabled={why ? true : undefined}
                      title={why ? addonHint(why) : undefined}
                      onClick={() => !why && onChange(f.key, o)}
                    >
                      <span className={why ? "opacity-55" : undefined}>
                        {optionText(f.param, o, f.kind)}
                      </span>
                    </button>
                  );
                })}
              </div>
            </div>
          ))}
        </ChipPop>
      )}
      {!kindModels && first && !pickable.length && !precisions.length && (
        <span
          className={`${chip} inline-flex items-center`}
          title={
            // a workflow that allows one variant (its `only`, docs/mock/VideoSource.dc.html)
            // (alpha.5 item 4) one left because only it is downloaded: the workflow allows the others
            first.param.name === "variant" && first.param.enum?.length === 1
              ? first.present
                ? `Only ${named[0]} is downloaded`
                : `This workflow runs on ${named[0]} only`
              : modelTitle
          }
        >
          {modelText}
        </span>
      )}
      {runOn}
      {values.map((f) => {
        const id = `val-pop-${idOf(f.key)}`;
        const label = f.param.label ?? f.param.name;
        if (off[f.key])
          return (
            <span
              key={f.key}
              aria-disabled
              title={off[f.key]}
              className={`${chip} inline-flex cursor-not-allowed items-center text-t2 opacity-45`}
            >
              {chipText(f, rawOf(f, raws))}
            </span>
          );
        return (
          <ChipPop
            key={f.key}
            id={id}
            label={label}
            text={chipText(f, rawOf(f, raws))}
            title={label}
            open={pop === id}
            setOpen={setPop}
            all={() => show(f.key)}
          >
            <Editor f={f} raw={rawOf(f, raws)} onChange={onChange} close={() => setPop(null)} />
          </ChipPop>
        );
      })}
      {groups.map((g) => {
        const id = `grp-pop-${idOf(g.label)}`;
        const autos = g.fields.filter(
          (f) => f.param.auto === true || f.param.enum?.includes("auto"),
        );
        const allAuto = autos.length > 0 && autos.every((f) => isAuto(f.param, rawOf(f, raws)));
        // the values alone ("90 BPM · A minor"), 4/4 left out as the usual time; all on Auto: the group's name
        const text =
          g.fields
            .filter((f) => !isAuto(f.param, rawOf(f, raws)) && rawOf(f, raws) !== "4/4")
            .map((f) => chipText(f, rawOf(f, raws)))
            .join(" · ") || g.label;
        return (
          <ChipPop
            key={g.label}
            id={id}
            label={g.label}
            text={text}
            title={g.label}
            open={pop === id}
            setOpen={setPop}
            all={() => show(g.fields[0].key)}
          >
            {g.fields.map((f, i) => (
              <Editor
                key={f.key}
                f={f}
                raw={rawOf(f, raws)}
                onChange={onChange}
                close={() => setPop(null)}
                focus={i === 0}
                noAuto
              />
            ))}
            {autos.length > 0 && (
              <AutoRow
                on={allAuto}
                note={
                  autos.length > 1
                    ? `The plan picks ${autos.length === 2 ? "both" : autos.length === 3 ? "all three" : "them all"}`
                    : autos[0].param.help
                }
                onChange={(on) => {
                  for (const f of autos)
                    onChange(
                      f.key,
                      f.param.type === "enum"
                        ? on
                          ? "auto"
                          : (f.param.enum!.find((o) => o !== "auto") ?? "auto")
                        : on
                          ? "0"
                          : String(f.param.min ?? 1),
                    );
                }}
              />
            )}
          </ChipPop>
        );
      })}
      {greyed.map((g) => (
        <span
          key={g.label}
          aria-disabled
          title={g.why}
          className={`${chip} inline-flex cursor-not-allowed items-center text-t2 opacity-45`}
        >
          {g.label}
        </span>
      ))}
      {c && (
        <ChipPop
          id="canvas-pop"
          label="Canvas"
          text={sizeLabel}
          open={pop === "canvas-pop"}
          setOpen={setPop}
          all={() => show("canvas")}
        >
          <CanvasPicker
            c={c}
            errors={{}}
            onChange={onChange}
            compact={compact}
            stage1={stage1}
            popup
          />
        </ChipPop>
      )}
      {frames.map((f) => {
        const fps = fpsOf(f, fields, raws);
        const text = [
          rawOf(f, raws) === "auto" ? "Auto" : secondsText(seconds(Number(rawOf(f, raws)), fps)),
          fps && `${fps} fps`,
        ]
          .filter(Boolean)
          .join(" · ");
        const title = f.param.label ?? f.param.name;
        // Frame rate only when the workflow has an fps setting; else the chip just reads it
        const rate = fields.find((x) => x.key === fpsKey(f));
        const rates = rate && (allowedInts(rate, raws)?.map(String) ?? rate.param.enum);
        const id = `len-pop-${idOf(f.key)}`;
        return (
          <ChipPop
            key={f.key}
            id={id}
            label="Duration"
            text={text}
            title={title}
            open={pop === id}
            setOpen={setPop}
            all={() => show(f.key)}
          >
            <Duration
              id={`${id}-in`}
              f={f}
              raw={rawOf(f, raws)}
              fps={fps}
              onChange={onChange}
              plain
            />
            {rate &&
              (rates?.length ? (
                <div
                  role="group"
                  aria-label={rate.param.label ?? rate.param.name}
                  className="flex flex-col gap-1.5"
                >
                  <span className="text-[12px] text-t2">{rate.param.label ?? rate.param.name}</span>
                  <div className="flex flex-wrap gap-1.5">
                    {rates.map((o) => (
                      <button
                        key={o}
                        type="button"
                        className="opt bevel min-w-16 px-3 text-[13px]"
                        aria-pressed={String(rawOf(rate, raws)) === o}
                        onClick={() => onChange(rate.key, o)}
                      >
                        {rate.param.type === "enum"
                          ? optionText(rate.param, o, rate.kind)
                          : `${o} fps`}
                      </button>
                    ))}
                  </div>
                </div>
              ) : (
                <Editor
                  f={rate}
                  raw={rawOf(rate, raws)}
                  onChange={onChange}
                  close={() => setPop(null)}
                  focus={false}
                />
              ))}
          </ChipPop>
        );
      })}
      {toggles.map((f) => {
        const on = rawOf(f, raws) === true;
        const label = f.param.label ?? f.param.name;
        // a one-word label, or a node's bypass ("No enhance on"), says its state ("Audio on"); a longer one is
        // just pressed, and looks it ("Regenerate audio", docs/mock/VideoSource.dc.html)
        const bypass = f.param.name === "bypass";
        const words = label.includes(" ") && !bypass;
        const why = bypass ? blocks[f.node] : undefined;
        // its add-on is missing: greyed with the reason, held on (docs/mock/CreateEnhance.dc.html)
        if (why)
          return (
            <button
              key={f.key}
              type="button"
              aria-pressed
              aria-disabled
              title={addonHint(why)}
              className={`${chip} inline-flex cursor-not-allowed items-center gap-2 text-t2`}
            >
              <span className="opacity-55">{label} on</span>
              <em className="text-[11px] whitespace-nowrap not-italic">{why}</em>
            </button>
          );
        return (
          <button
            key={f.key}
            type="button"
            aria-pressed={on}
            title={f.param.help}
            className={
              words
                ? `${chip} aria-pressed:bg-t1 aria-pressed:font-semibold aria-pressed:text-panel`
                : bypass
                  ? `${chip} aria-pressed:bg-s5`
                  : chip
            }
            onClick={() => onChange(f.key, !on)}
          >
            {words ? label : `${label} ${on ? "on" : "off"}`}
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
  card,
  frames = card,
  openerRef,
}: FormProps & {
  uploads: RefUploads;
  extra?: React.ReactNode;
  /** a video workflow's tiles (docs/mock/ImageToVideo.dc.html) */
  card?: boolean;
  /** an image tile's picker also offers a video's first or last frame (API 1.37; a video workflow, empty stage too) */
  frames?: boolean;
  /** what opens the first tile's picker (the composer's +) */
  openerRef?: Opener;
}) {
  const clip = clipOf(fields, raws);
  return imageInputs(fields).map((f, i) => {
    // one image (MiniMax H3's First frame, its optional Last frame): a tile with its label, as the boards draw it
    if (f.param.type === "image")
      return (
        <Fragment key={f.key}>
          <FirstFrameTile
            label={f.param.label ?? f.param.name}
            empty={f.optional ? "None · optional" : "None"}
            id={refIds(f, raws[f.key])[0]}
            onPick={(id) => onChange(f.key, id)}
            card={card}
            frames={frames}
            openerRef={i === 0 ? openerRef : undefined}
          />
          {i === 0 && extra}
        </Fragment>
      );
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
    // its node's bypass placed too (LTX Audio to video): one "Frames · No keyframes" tile; picking a frame switches
    // the node on (Create's change), so a switched-off node reads as no keyframes
    const bypass = fields.find((x) => x.key === `${f.node}.bypass`);
    const off = bypass && rawOf(bypass, raws) === true;
    // with its bypass the tile shows any frame (the first on the clip, else the first row)
    const pic = off ? undefined : bypass ? (first ?? rows[0]) : first;
    return (
      <Fragment key={f.key}>
        {i === 0 && extra}
        <FirstFrameTile
          {...(bypass && { label: f.param.label ?? f.param.name, empty: "No keyframes" })}
          id={pic ? refIds(f, [pic])[0] : undefined}
          more={pic ? rows.length - 1 : off ? 0 : rows.length}
          card={card}
          openerRef={i === 0 ? openerRef : undefined}
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
