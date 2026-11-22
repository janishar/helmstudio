"use client";
// New line (API 1.32, docs/mock/TimelineAudio.dc.html "New line", TimelineAudioZonos3.dc.html): the voice composer
// docked at the playhead, the timeline's fifth generating action. Voice-over (the text, Voice, Language, Seed, the
// presets) or Dialogue (the sequence's cast with their voices, the script prefilled one `@handle:` line each, the
// Delivery presets and sliders). POST …/timelines/{id}/lines puts the line's placeholder on an audio track at the
// playhead; the run's take fills it.
import Link from "next/link";
import { X } from "lucide-react";
import { useMemo, useState } from "react";
import { ErrorNote } from "@/components/kit";
import { TileWords, tileClass } from "@/components/create/elements";
import { VoiceTile } from "@/components/create/voices";
import { usePlannedPeak } from "@/components/create/memory-chip";
import {
  linesKeyOf,
  paramsOf,
  scriptFieldOf,
  speechModes,
  useCast,
} from "@/components/create/dialogue";
import { api, message, ok, type S } from "@/lib/api";
import { useLibrary, useVoices } from "@/lib/library";
import { mediaOf } from "@/lib/media";
import { presetNames, studioView } from "@/lib/studio";
import { newId, shortTime, type Norm } from "@/lib/timeline";
import { scriptLines } from "@/lib/voices";
import {
  buildInputs,
  fields as fieldsOf,
  pretty,
  toRaw,
  type Field,
  type Raw,
} from "@/lib/workflow";
import { TextArea } from "@/components/expand-text";

type Workflow = S<"Workflow">;
type Timeline = S<"Timeline">;

/** Planned speech: characters ÷ 15 (the `speech_seconds` rule), a dialogue's pauses between lines added. */
function plannedSeconds(text: string, script: boolean, pause: number): number {
  if (!script) return [...text.trim()].length / 15;
  const spoken = scriptLines(text).filter((l) => "handle" in l) as { text: string }[];
  return (
    spoken.reduce((n, l) => n + [...l.text].length / 15, 0) + pause * Math.max(0, spoken.length - 1)
  );
}

export function LineComposer({
  tl,
  doc,
  at,
  track,
  workflows,
  onDone,
  onClose,
}: {
  tl: Timeline;
  doc: Norm;
  /** the playhead (sequence seconds) */
  at: number;
  /** the audio track it goes on; null: none is free there */
  track: string | null;
  workflows: Workflow[];
  onDone: (t: Timeline) => void;
  onClose: () => void;
}) {
  // the voice templates: a voice-over or a dialogue (not the design one, whose voice is only words)
  const speech = useMemo(
    () =>
      workflows.filter(
        (w) =>
          mediaOf(w) === "audio" &&
          paramsOf(w).some(
            (p) => p.script || (p.type === "voice" && p.accepts?.some((a) => a !== "design")),
          ),
      ),
    [workflows],
  );
  // a voice-over first (docs/mock/TimelineAudio.dc.html): the dialogue is the toggle's other side
  const [workflowId, setWorkflowId] = useState(
    (speech.find((w) => !paramsOf(w).some((p) => p.script)) ?? speech[0])?.id ?? "",
  );
  const workflow = speech.find((w) => w.id === workflowId);
  const all = useMemo(() => (workflow ? fieldsOf(workflow) : []), [workflow]);
  const layout = workflow ? studioView(workflow, all) : null;
  const modes = speechModes(workflows, workflow);
  const scriptField = scriptFieldOf(all);
  const linesKey = linesKeyOf(all);
  const textField = scriptField ?? all.find((f) => f.param.type === "text");
  const voiceField = all.find((f) => f.param.type === "voice");
  const langField = all.find((f) => f.param.name === "language" && f.param.type === "enum");
  const seedField = all.find((f) => f.param.type === "seed");
  const pauseField = all.find((f) => f.param.name === "pause");
  const delivery =
    layout?.groups
      .find((g) => g.title === "Delivery")
      ?.fields.filter((f) => f.param.type === "float" || f.param.type === "int") ?? [];
  const library = useLibrary();
  const voices = useVoices();
  // the sequence's cast: characters speak (one `@handle:` line each), places and props have no voice
  const cast = useMemo(
    () => (library ?? []).filter((x) => tl.defaults.cast.includes(x.id)),
    [library, tl.defaults.cast],
  );
  const prefill = cast
    .filter((x) => x.group === "character")
    .map((x) => `@${x.handle}: `)
    .join("\n");
  const [raws, setRaws] = useState<Record<string, Raw>>({});
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const raw = (f: Field) => raws[f.key] ?? toRaw(f.initial, f.param);
  const text = textField ? String(raws[textField.key] ?? (scriptField ? prefill : "")) : "";
  const speakers = useCast(scriptField ? text : "", workflow);
  const pause = pauseField ? Number(raw(pauseField)) : 0.35;
  const seconds = Math.max(1, plannedSeconds(text, !!scriptField, pause));
  const variant = all.find((f) => f.model && f.param.name === "variant");
  const modelText = variant
    ? `${variant.nodeTitle} · ${pretty(String(raw(variant)))}`
    : (workflow?.title ?? "Workflow");
  const presets = workflow?.studio?.presets ?? {};
  const presetOn = (name: string) =>
    Object.entries(presets[name] ?? {}).every(([k, v]) => {
      const f = all.find((x) => x.key === k);
      return f && JSON.stringify(raw(f)) === JSON.stringify(toRaw(v, f.param));
    });

  /** Switches to `w`, keeping what was typed when both take text. */
  function pick(w: Workflow) {
    const fs = fieldsOf(w);
    const t = scriptFieldOf(fs) ?? fs.find((f) => f.param.type === "text");
    setWorkflowId(w.id);
    setRaws(t && !!scriptFieldOf(fs) === !!scriptField ? { [t.key]: text } : {});
  }

  async function generate() {
    if (!workflow || !track) return;
    setBusy(true);
    setError("");
    try {
      const r = { ...raws, ...(textField && { [textField.key]: text }) };
      const built = buildInputs(all, r);
      const bad = Object.entries(built.errors).find(([k]) => k !== linesKey);
      if (bad) throw new Error(`${bad[0]}: ${bad[1]}`);
      const res = await ok(
        api.POST("/v1/studio/timelines/{timeline_id}/lines", {
          params: { path: { timeline_id: tl.id } },
          body: {
            slot: { track, at, length: seconds, id: newId(doc) },
            run: { workflow_id: workflow.id, inputs: built.inputs },
          },
        }),
      );
      onDone(res.timeline);
    } catch (e) {
      setError(message(e));
    } finally {
      setBusy(false);
    }
  }

  // the planned peak beside the time, as Create's Generate ("≈ 0:08 · 6 GB")
  const built = workflow
    ? buildInputs(all, { ...raws, ...(textField && { [textField.key]: text }) })
    : null;
  const gb = usePlannedPeak(
    workflow && built ? { workflow_id: workflow.id, inputs: built.inputs } : null,
  );
  const ctx = workflow && {
    workflow,
    familyTitle: (f: string) => speech.find((w) => w.family === f)?.title.split(" ")[0] ?? f,
  };
  const voiceName = (s: (typeof speakers)[number]) =>
    s.voice ? `${s.voice.name} (${s.voice.source === "mine" ? s.voice.group : "preset"})` : "";
  const places = cast.filter((x) => x.group !== "character");
  return (
    <section
      aria-label="New line"
      onKeyDown={(e) => e.key === "Escape" && onClose()}
      className="flex flex-col gap-2.5 rounded-[28px_28px_28px_12px] border border-s5 bg-s2 px-3.5 py-3 shadow-[0_16px_40px_color-mix(in_srgb,var(--shd)_50%,transparent)]"
    >
      <div className="flex flex-wrap items-center gap-2">
        <h2 className="m-0 font-display text-sm font-bold tracking-[0.04em]">New line</h2>
        <span className="mono text-[12px] text-t2">
          {[
            workflow?.title,
            `at ${shortTime(at)}${track ? ` on ${track}` : ""}`,
            scriptField && tl.defaults.cast.length && "uses this sequence's cast",
          ]
            .filter(Boolean)
            .join(" · ")}
        </span>
        <div className="grow" />
        {modes && (
          <div className="seg" role="group" aria-label="Line">
            {(
              [
                ["Voice-over", modes.voice],
                ["Dialogue", modes.dialogue],
              ] as const
            ).map(([label, w]) => (
              <button
                key={label}
                type="button"
                aria-pressed={w.id === workflowId}
                onClick={() => w.id !== workflowId && pick(w)}
              >
                {label}
              </button>
            ))}
          </div>
        )}
        <button
          type="button"
          aria-label="Close New line"
          onClick={onClose}
          className="grid size-7 place-items-center rounded-lg text-t2"
        >
          <X size={16} aria-hidden />
        </button>
      </div>

      {scriptField && (
        <div className="flex flex-wrap items-center gap-2 text-[12px]">
          <span className="text-t2">Cast</span>
          {speakers.map((s) => (
            <span
              key={s.handle}
              className={`mono inline-flex h-[30px] items-center gap-1.5 rounded-lg px-2 ${s.reason ? "border border-dashed border-bad text-bad" : "bg-s5"}`}
            >
              @{s.handle}
              <span className="rounded-md bg-s2 px-1.5 font-sans text-[11px]">
                {s.reason ?? voiceName(s)}
              </span>
            </span>
          ))}
          {places.map((x) => (
            <span key={x.id} className="text-t2">
              @{x.handle} is a {x.group === "location" ? "place" : "prop"}: it has no voice
            </span>
          ))}
        </div>
      )}

      {textField && (
        <>
          <label htmlFor="line-text" className="sr-only">
            {scriptField ? "Script" : "Text to speak"}
          </label>
          <TextArea
            id="line-text"
            expand={scriptField ? "Script" : "Words to speak"}
            rows={scriptField ? Math.max(3, text.split("\n").length) : 2}
            value={text}
            maxLength={textField.param.max}
            placeholder={
              scriptField
                ? "@mara (quiet): You said you'd be back by noon."
                : "Type the words to speak"
            }
            onChange={(e) => setRaws((r) => ({ ...r, [textField.key]: e.target.value }))}
            onKeyDown={(e) => {
              if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) void generate();
            }}
            className={`w-full resize-none border-0 bg-transparent text-t1 outline-0 placeholder:text-t2 ${scriptField ? "mono text-[13px] leading-5" : "text-[15px] leading-[22px]"}`}
          />
          {!scriptField && (
            <span className="text-[12px] text-t2">
              Mentions are not read aloud: the voice comes from the Voice tile.
            </span>
          )}
        </>
      )}

      {(Object.keys(presets).length > 0 || delivery.length > 0) && (
        <div className="flex flex-wrap items-center gap-2">
          <span className="text-[12px] text-t2">{delivery.length ? "Delivery" : "Preset"}</span>
          {presetNames(workflow?.studio).map((name) => (
            <button
              key={name}
              type="button"
              aria-pressed={presetOn(name)}
              className="h-9 rounded-full border border-transparent bg-s3 px-3.5 text-[13px] aria-pressed:border-a2 aria-pressed:bg-s1"
              onClick={() =>
                setRaws((r) => {
                  const n = { ...r };
                  for (const [k, v] of Object.entries(presets[name] ?? {})) {
                    const f = all.find((x) => x.key === k);
                    if (f) n[k] = toRaw(v, f.param);
                  }
                  return n;
                })
              }
            >
              {name}
            </button>
          ))}
          {scriptField && (
            <span className="mono ml-auto text-[11px] text-t2">
              {scriptLines(text).filter((l) => "handle" in l).length} lines · an empty line is a
              pause · a line&apos;s (style) adds to these
            </span>
          )}
        </div>
      )}
      {delivery.length > 0 && (
        <div className="grid grid-cols-2 gap-x-4 gap-y-1 sm:grid-cols-4">
          {delivery.map((f) => (
            <label key={f.key} className="flex flex-col gap-0.5 text-[11px] text-t2">
              <span className="flex justify-between">
                {f.param.label ?? f.param.name}
                <span className="mono">{String(raw(f))}</span>
              </span>
              <input
                type="range"
                min={f.param.min}
                max={f.param.max}
                step={f.param.step ?? (f.param.type === "int" ? 1 : 0.05)}
                value={Number(raw(f))}
                onChange={(e) => setRaws((r) => ({ ...r, [f.key]: e.target.value }))}
                className="w-full accent-a2"
              />
            </label>
          ))}
        </div>
      )}

      <div className="flex flex-wrap items-center gap-2">
        {scriptField && linesKey && (
          <span className={tileClass("card")}>
            <TileWords
              big="card"
              label="Voices"
              value={`From the cast · ${speakers.filter((s) => !s.reason).length}`}
            />
          </span>
        )}
        {voiceField && ctx && (
          <VoiceTile
            f={voiceField}
            raws={raws}
            onChange={(k, v) => setRaws((r) => ({ ...r, [k]: v }))}
            ctx={ctx}
          />
        )}
        {langField && (
          <label className={tileClass("card")}>
            <span className="flex min-w-0 flex-col">
              <span className="mono text-[10px] text-t2">
                {langField.param.label ?? "Language"}
              </span>
              <select
                className="truncate bg-transparent text-[13px] font-semibold text-t1 outline-0"
                value={String(raw(langField))}
                onChange={(e) => setRaws((r) => ({ ...r, [langField.key]: e.target.value }))}
              >
                {(langField.param.enum ?? []).map((v) => (
                  <option key={v} value={v} className="bg-s2">
                    {pretty(v)}
                  </option>
                ))}
              </select>
            </span>
          </label>
        )}
        {seedField && (
          <label className={tileClass("card")}>
            <span className="flex min-w-0 flex-col">
              <span className="mono text-[10px] text-t2">Seed</span>
              <input
                inputMode="numeric"
                placeholder="Random"
                value={String(raws[seedField.key] ?? "")}
                onChange={(e) =>
                  setRaws((r) => ({ ...r, [seedField.key]: e.target.value.replace(/\D/g, "") }))
                }
                className="w-full bg-transparent text-[13px] font-semibold text-t1 outline-0 placeholder:text-t1"
              />
            </span>
          </label>
        )}
        <label className="sr-only" htmlFor="line-model">
          Voice model
        </label>
        <select
          id="line-model"
          className="chip bevel appearance-none rounded-[10px] text-t1"
          value={workflowId}
          onChange={(e) => {
            const w = speech.find((x) => x.id === e.target.value);
            if (w) pick(w);
          }}
          title={modelText}
        >
          {speech.map((w) => (
            <option key={w.id} value={w.id} className="bg-s2">
              {w.title}
            </option>
          ))}
        </select>
        {tl.session_id && (
          <Link
            href={`/?s=${encodeURIComponent(tl.session_id)}`}
            className="chip bevel rounded-[10px]"
          >
            Open in Create
          </Link>
        )}
        {/* bottom right even when the row wraps */}
        <button
          type="button"
          className="key ml-auto flex h-12 flex-col items-center justify-center gap-px rounded-xl px-[22px] text-sm"
          disabled={busy || !workflow || !track || !text.trim() || (!!voiceField && !voices)}
          onClick={() => void generate()}
        >
          <span>{busy ? "Starting…" : "Generate line"}</span>
          <span className="mono text-[11px] font-medium">
            ≈ {Math.floor(Math.round(seconds) / 60)}:
            {String(Math.round(seconds) % 60).padStart(2, "0")}
            {gb != null && ` · ${Math.round(gb)} GB`}
          </span>
        </button>
      </div>
      {!speech.length && <p className="m-0 text-[13px] text-t2">No voice workflow is installed.</p>}
      {!track && (
        <p className="m-0 text-[13px] text-t2">
          No audio track is free at the playhead: add an audio track, or move the playhead.
        </p>
      )}
      {error && <ErrorNote>{error}</ErrorNote>}
    </section>
  );
}
