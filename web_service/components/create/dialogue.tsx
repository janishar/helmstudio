"use client";
// Dialogue (API 1.32, docs/mock/CreateAudio.dc.html "Dialogue", CreateAudioZonos3.dc.html bottom): the script editor
// (each line's @element chip with its voice badge, its style and text; a line's error under it with its fix), the
// Cast tile and the inspector's cast rows, Style per line, and the Voice-over | Dialogue switch. A script line speaks in
// its element's voice (`voice_id`); the voice is set on the element (PUT /v1/studio/library/{id}).
import { useMemo, useState } from "react";
import { Mic } from "lucide-react";
import { Modal } from "@/components/kit";
import { PromptArea } from "@/components/create/prompt";
import { TileWords, tileClass } from "@/components/create/elements";
import { VoicePicker, type VoiceCtx } from "@/components/create/voices";
import { api, message, ok, type S } from "@/lib/api";
import { reloadLibrary, useLibrary, useVoices } from "@/lib/library";
import {
  clipText,
  expandScript,
  scriptLines,
  voiceParams,
  voiceReason,
  type ScriptLine,
} from "@/lib/voices";
import type { Field } from "@/lib/workflow";

type Item = S<"LibraryItem">;
type Workflow = S<"Workflow">;

/** A workflow's script param (a text param with `script: true`), if any. */
export const scriptFieldOf = (fs: Field[]) =>
  fs.find((f) => f.param.type === "text" && f.param.script);

/** The rows param a script writes (`<node>.lines`): its tile and editor are the script's, not a rows editor. */
export const linesKeyOf = (fs: Field[]) => {
  const s = scriptFieldOf(fs);
  return s ? `${s.node}.lines` : undefined;
};

/** The params of a workflow's own nodes. */
export const paramsOf = (x: Pick<Workflow, "nodes" | "node_schemas">) =>
  Object.values(x.nodes).flatMap((n) => x.node_schemas[n.kind]?.params ?? []);

/** The Voice-over | Dialogue switch's two templates of a family: the one with a script, and the voice-over (a voice
 * param that is not design-only; `<family>-voice-over` first). */
export function speechModes(ws: Workflow[], w?: Workflow) {
  if (!w || w.media !== "audio") return null;
  const mine = ws.filter((x) => x.family === w.family && x.media === "audio");
  const params = paramsOf;
  const dialogue = mine.find((x) => params(x).some((p) => p.script));
  const voice = mine
    .filter(
      (x) =>
        !params(x).some((p) => p.script) &&
        params(x).some((p) => p.type === "voice" && p.accepts?.some((a) => a !== "design")),
    )
    .sort((a, b) => +b.id.endsWith("voice-over") - +a.id.endsWith("voice-over"))[0];
  return dialogue && voice ? { voice, dialogue } : null;
}

/** One speaker of a script: its handle, element, voice and why the voice does not fit (null: it does). */
export type Speaker = { handle: string; el?: Item; voice?: Item; reason: string | null };

/** The script's speakers in first-line order, with their voices and the fit for this workflow. */
export function useCast(script: string, w?: Pick<Workflow, "nodes" | "node_schemas">) {
  const library = useLibrary();
  const voices = useVoices();
  return useMemo(() => {
    const field =
      w && voiceParams(w.nodes, w.node_schemas).find(([, p]) => p.name === "lines.voice");
    const out: Speaker[] = [];
    for (const ln of scriptLines(script))
      if ("handle" in ln && !out.some((s) => s.handle === ln.handle)) {
        const el = library?.find((x) => x.handle === ln.handle);
        const voice = el?.voice_id ? voices?.find((v) => v.id === el.voice_id) : undefined;
        const reason =
          !el || !voices
            ? null
            : !voice
              ? `@${ln.handle} has no voice`
              : field
                ? voiceReason(w!.nodes[field[0]].kind, field[1], voice)
                : null;
        out.push({ handle: ln.handle, el, voice, reason });
      }
    return out;
  }, [script, w, library, voices]);
}

/** Each script line's error before studio answers (check.py expand_script on the form's values), line → text. */
export function useLocalErrors(
  w: Pick<Workflow, "nodes" | "node_schemas"> | undefined,
  inputs: S<"RunInputs">,
): Record<number, string> {
  const library = useLibrary();
  const voices = useVoices();
  return useMemo(() => {
    if (!w || !library || !voices) return {};
    const nodes = Object.fromEntries(
      Object.entries(w.nodes).map(([id, n]) => [
        id,
        {
          ...n,
          params: {
            ...n.params,
            ...Object.fromEntries(
              Object.entries(inputs)
                .filter(([k]) => k.startsWith(`${id}.`))
                .map(([k, v]) => [k.slice(id.length + 1), v]),
            ),
          },
        },
      ]),
    );
    const r = expandScript(
      nodes,
      w.node_schemas,
      library,
      Object.fromEntries(voices.map((v) => [v.id, v])),
    );
    const out: Record<number, string> = {};
    for (const e of "errors" in r ? r.errors : []) {
      const m = /line (\d+): (.*)$/.exec(e);
      if (m) out[Number(m[1])] ??= m[2];
    }
    return out;
  }, [w, inputs, library, voices]);
}

/** Saves an element's voice (null: none), then rereads the library. */
export async function setElementVoice(el: Item, voiceId: string | null) {
  await ok(
    api.PUT("/v1/studio/library/{item_id}", {
      params: { path: { item_id: el.id } },
      body: {
        group: el.group,
        name: el.name,
        handle: el.handle,
        text: el.text,
        images: el.images.map((i) => ({ take_id: i.take_id })),
        voice_id: voiceId,
        version: el.version,
      },
    }),
  );
  await reloadLibrary();
}

/** The voice picker for one element's voice: Use saves it on the element. */
export function ElementVoicePicker({
  el,
  ctx,
  onClose,
  onError,
}: {
  el: Item | null;
  ctx: VoiceCtx;
  onClose: () => void;
  onError: (e: string) => void;
}) {
  return (
    <VoicePicker
      open={!!el}
      onClose={onClose}
      value={el?.voice_id ?? ""}
      onPick={(id) => el && setElementVoice(el, id).catch((e) => onError(message(e)))}
      ctx={ctx}
    />
  );
}

/** A speaker's badge: "Mara · Serena", or the element and its voice's kind when that does not fit ("Kai · design"). */
const badge = (s: Speaker) =>
  s.voice
    ? `${s.el?.name ?? s.handle} · ${s.reason ? s.voice.group.replace("designed", "design") : s.voice.name}`
    : s.el
      ? `${s.el.name} · no voice`
      : "unknown";

/** A speaker's chip (the board's `.vc`): its initial, @handle and voice badge; dashed red when it cannot speak. */
function Chip({ s, bad, onClick }: { s: Speaker; bad: boolean; onClick?: () => void }) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-label={`@${s.handle}, ${s.voice ? `voice ${s.voice.name}` : "no voice"}`}
      className={`mono inline-flex h-[30px] items-center gap-1.5 rounded-lg border pr-1 pl-[3px] text-[12px] ${bad ? "border-dashed border-bad bg-s3" : "border-transparent bg-s5"}`}
    >
      <i
        aria-hidden
        className="grid size-6 place-items-center rounded-md border border-dashed border-dash bg-s1 font-sans text-[10px] font-semibold text-t2 not-italic"
      >
        {(s.el?.name ?? s.handle).slice(0, 1).toUpperCase()}
      </i>
      @{s.handle}
      <span
        className={`inline-flex h-[22px] items-center gap-1 rounded-md bg-s2 px-[7px] font-sans text-[11px] font-medium ${bad ? "text-bad" : "text-[var(--t1b)]"}`}
      >
        {!bad && <Mic size={11} aria-hidden />}
        {badge(s)}
      </span>
    </button>
  );
}

/** The script: the board's lines while not editing (click to edit), a mentions textarea while editing. `errors` are
 * studio's (or the local check's) per line; a line whose speaker has no fitting voice offers Pick a voice. */
export function ScriptEditor({
  f,
  value,
  onChange,
  errors,
  cast,
  onVoice,
}: {
  f: Field;
  value: string;
  onChange: (v: string) => void;
  errors: Record<number, string>;
  cast: Speaker[];
  /** opens the voice picker for a speaker's element */
  onVoice: (el: Item) => void;
}) {
  const [editing, setEditing] = useState(false);
  const lines = scriptLines(value);
  const id = `script-${f.node}`;
  if (editing || !value.trim())
    return (
      <div className="flex min-w-0 flex-1 flex-col gap-1">
        <label htmlFor={id} className="sr-only">
          {f.param.label ?? "Script"}
        </label>
        <PromptArea
          id={id}
          autoFocus={editing}
          rows={Math.max(3, lines.length)}
          maxLength={f.param.max}
          placeholder={
            "@mara (quiet): You said you'd be back by noon.\n@jon: I know. The road was closed."
          }
          value={value}
          onChange={onChange}
          onBlur={() => setEditing(false)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && (e.metaKey || e.ctrlKey))
              e.currentTarget.form?.requestSubmit();
            if (e.key === "Escape") setEditing(false);
          }}
          className="mono w-full resize-none border-0 bg-transparent text-[14px] leading-[22px] text-t1 outline-0 placeholder:text-t2"
        />
        {f.param.help && !value.trim() && (
          <span className="text-[12px] text-t2">{f.param.help}</span>
        )}
      </div>
    );
  // the line numbers count spoken lines (the board's 1 2 3 around a blank line); errors name the script's lines
  const nums = new Map<number, number>();
  for (const ln of lines) if (!("blank" in ln)) nums.set(ln.line, nums.size + 1);
  return (
    <ol
      aria-label="Script"
      className="m-0 flex list-none flex-col gap-1.5 px-1.5 py-1 text-[15px] leading-[22px]"
      onClick={(e) => {
        if (!(e.target as HTMLElement).closest("button")) setEditing(true);
      }}
      title="Click to edit the script"
    >
      {lines.map((ln: ScriptLine) => {
        if ("blank" in ln) return <li key={ln.line} aria-label="Blank line" className="h-2.5" />;
        const n = nums.get(ln.line)!;
        const err = errors[ln.line];
        const s = "handle" in ln ? cast.find((c) => c.handle === ln.handle) : undefined;
        const errId = `${id}-err-${ln.line}`;
        const fix = err && s?.el && /has no voice|does not fit/.test(err) ? s.el : undefined;
        return [
          <li key={ln.line} className="flex flex-wrap items-center gap-2">
            <span
              className={`mono w-4 text-right text-[11px] ${err ? "text-bad" : ""}`}
              aria-label={`Line ${n}`}
            >
              {n}
            </span>
            {"handle" in ln ? (
              <>
                {s && (
                  <Chip
                    s={s}
                    bad={!!err || !!s.reason}
                    onClick={s.el ? () => onVoice(s.el!) : () => setEditing(true)}
                  />
                )}
                {ln.style ? (
                  <span className="text-t2 italic">({ln.style}):</span>
                ) : (
                  <span className="-ml-[5px] text-t2">:</span>
                )}
                <span aria-describedby={err ? errId : undefined}>{ln.text}</span>
              </>
            ) : (
              <span className="mono text-[13px] text-bad" aria-describedby={errId}>
                {value.split("\n")[ln.line - 1]}
              </span>
            )}
          </li>,
          err && (
            <li
              key={`${ln.line}-err`}
              className="flex flex-wrap items-center gap-2.5 rounded-[10px] bg-[color-mix(in_srgb,var(--bad)_10%,transparent)] py-1.5 pr-2 pl-6"
            >
              <p
                id={errId}
                className="mono m-0 flex-[1_1_260px] text-[12px] leading-[17px] text-bad"
              >
                Line {n}: {err}
              </p>
              {fix && (
                <button
                  type="button"
                  className="chip bevel h-8 rounded-[10px] bg-s5"
                  onClick={() => onVoice(fix)}
                >
                  Pick a voice
                </button>
              )}
            </li>
          ),
        ];
      })}
    </ol>
  );
}

/** "Mara, Jon · 2 presets": the Cast tile's value. */
export function castText(cast: Speaker[]) {
  if (!cast.length) return "Write a script";
  const kinds = new Map<string, number>();
  for (const s of cast) if (s.voice) kinds.set(s.voice.group, (kinds.get(s.voice.group) ?? 0) + 1);
  const missing = cast.filter((s) => s.reason).length;
  const sum =
    kinds.size === 1 && !missing
      ? `${cast.length} ${[...kinds.keys()][0]}${cast.length === 1 ? "" : "s"}`
      : missing
        ? `${missing} to fix`
        : `${cast.length} voices`;
  return `${cast.map((s) => s.el?.name ?? `@${s.handle}`).join(", ")} · ${sum}`;
}

/** The composer's Cast tile: the speakers and their voices in a dialog, each changed with the voice picker. */
export function CastTile({
  cast,
  label,
  onVoice,
}: {
  cast: Speaker[];
  label: string;
  onVoice: (el: Item) => void;
}) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <button
        type="button"
        aria-haspopup="dialog"
        className={tileClass("card")}
        onClick={() => setOpen(true)}
      >
        <TileWords big="card" label={label} value={castText(cast)} />
      </button>
      <Modal open={open} onClose={() => setOpen(false)} title={label}>
        <div className="flex flex-col gap-2">
          {!cast.length && (
            <p className="m-0 text-[13px] text-t2">
              Write a line as @name: text. Each @name speaks in its element&apos;s voice.
            </p>
          )}
          <CastRows
            cast={cast}
            onVoice={(el) => {
              setOpen(false);
              onVoice(el);
            }}
          />
        </div>
      </Modal>
    </>
  );
}

/** The inspector's cast (the board's kv rows): each @handle and its voice, with its source; a speaker that cannot
 * speak here offers its voice picker. `familyTitle` names a preset's family; `takeTitle` a clip's take. */
export function CastRows({
  cast,
  onVoice,
  familyTitle,
  takeTitle,
}: {
  cast: Speaker[];
  onVoice: (el: Item) => void;
  familyTitle?: (f: string) => string;
  takeTitle?: (id: string) => string | undefined;
}) {
  return cast.map((s) => {
    const v = s.voice;
    const src =
      v?.voice?.source === "preset"
        ? `${familyTitle?.(v.voice.family ?? "") ?? v.voice.family} preset`
        : v?.voice?.clip
          ? `clip: ${takeTitle?.(v.voice.clip.take_id) ?? "a take"} · ${clipText(v.voice.clip)}`
          : undefined;
    return (
      <div key={s.handle} className="flex justify-between gap-2.5 text-[13px]">
        <span>@{s.handle}</span>
        {s.reason || !v ? (
          s.el ? (
            <button
              type="button"
              className="mono rounded-lg border border-dashed border-bad px-2 py-0.5 text-[12px] text-bad"
              onClick={() => onVoice(s.el!)}
              title={s.reason ?? undefined}
            >
              {v ? `${v.group.replace("designed", "Design")} · pick a voice` : "Pick a voice"}
            </button>
          ) : (
            <span className="mono text-[12px] text-bad">no such element</span>
          )
        ) : (
          <button
            type="button"
            className="mono flex flex-col items-end text-right text-[12px] text-t2"
            onClick={() => s.el && onVoice(s.el)}
            aria-label={`@${s.handle}: ${v.name}, change the voice`}
          >
            <span>
              {v.name} · {v.source === "mine" ? "mine" : v.group}
            </span>
            {src && <span className="text-[11px]">{src}</span>}
          </button>
        )}
      </div>
    );
  });
}

/** Style per line (the board's Voice section): each spoken line's `(style)`, edited in the script. */
export function StylePerLine({
  value,
  onChange,
}: {
  value: string;
  onChange: (v: string) => void;
}) {
  const rows = value.split("\n");
  const spoken = scriptLines(value).filter((l) => "handle" in l) as Extract<
    ScriptLine,
    { handle: string }
  >[];
  if (!spoken.length) return null;
  const set = (line: number, style: string) => {
    const l = spoken.find((x) => x.line === line)!;
    const s = style.trim();
    rows[line - 1] = `@${l.handle}${s ? ` (${s.replace(/[()]/g, "")})` : ""}: ${l.text}`;
    onChange(rows.join("\n"));
  };
  return (
    <div className="col-span-2 flex flex-col gap-2">
      <span>Style per line</span>
      <div className="grid grid-cols-[72px_minmax(0,1fr)] items-center gap-x-2.5 gap-y-2">
        {spoken.map((l, i) => (
          <StyleInput key={l.line} n={i + 1} l={l} onSet={(s) => set(l.line, s)} />
        ))}
      </div>
      <span className="text-[12px] text-t2">
        The (style) after a line&apos;s @name, in words: quiet, angry, whispering.
      </span>
    </div>
  );
}

function StyleInput({
  n,
  l,
  onSet,
}: {
  n: number;
  l: { line: number; handle: string; style: string };
  onSet: (s: string) => void;
}) {
  const [draft, setDraft] = useState<string | null>(null);
  const id = `style-${l.line}`;
  return (
    <>
      <label htmlFor={id} className="mono truncate text-[12px]">
        {n} @{l.handle}
      </label>
      <input
        id={id}
        type="text"
        maxLength={200}
        placeholder="as written"
        value={draft ?? l.style}
        onChange={(e) => setDraft(e.target.value)}
        onBlur={() => {
          if (draft !== null && draft !== l.style) onSet(draft);
          setDraft(null);
        }}
        onKeyDown={(e) => e.key === "Enter" && e.currentTarget.blur()}
        className="h-9 min-w-0 rounded-[10px] border border-s5 bg-s1 px-3 text-[14px] text-t1"
      />
    </>
  );
}

/** A cue's speaker: the element that speaks in that voice ("Mara"), else "". */
export const speakerOf = (library: Item[] | null, voiceId: string) =>
  library?.find((e) => e.voice_id === voiceId)?.name ?? "";
