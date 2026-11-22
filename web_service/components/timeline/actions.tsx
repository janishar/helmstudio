"use client";
// Take actions on a V1 clip (contracts/README.md "Timeline: Take actions", docs/mock/TakeActions.dc.html): the
// offers of GET …/clips/{clip}/actions for the timeline's range (only those offered), each a form under "Generate
// from this clip" (the prompt, what the range or extension comes to, the same node's switches, Replace or Insert
// after / before), run by POST …/shots: the slot holds the shot's placeholder until the take fills it.
import Link from "next/link";
import { useEffect, useMemo, useState } from "react";
import { ErrorNote } from "@/components/kit";
import { PromptArea } from "@/components/create/prompt";
import { Switch } from "@/components/create/form";
import { extensionOf, useFrameGrid } from "@/components/create/source";
import { api, message, ok, type S } from "@/lib/api";
import {
  actionSlot,
  audioMap,
  estimateLength,
  extendMap,
  floorGrid,
  insertsBefore,
  progressive,
  retakeMap,
  type Placement,
} from "@/lib/source";
import { newId, timecode, type Clip, type Norm } from "@/lib/timeline";
import {
  buildInputs,
  display,
  fields as fieldsOf,
  isRef,
  isSpan,
  optionBlocked,
  pretty,
  prettyModel,
  rawsFrom,
  readValue,
  toRaw,
  type Field,
  type Raw,
} from "@/lib/workflow";

type Timeline = S<"Timeline">;
type Offer = S<"TakeOffer">;

/** The action whose shot a slot holds (clip id → its action and the source clip's shot number), for the
 * placeholder's title; this tab's only. */
const shots = new Map<string, { action: string; replace: boolean; source?: number }>();

/** The placeholder of an action's shot: "Retaking Shot 3", "Extending Shot 3" (an inserted clip names its source),
 * "Retake failed"; null for another shot. */
export function actionShot(c: Clip, name?: string): { title: string; failed: boolean } | null {
  const a = c.shot && shots.get(c.id);
  if (!a || !c.shot) return null;
  if (c.shot.status === "failed")
    return { title: `${a.action.split(" ")[0]} failed`, failed: true };
  return {
    title: [progressive(a.action), a.replace ? name : a.source && `Shot ${a.source}`]
      .filter(Boolean)
      .join(" "),
    failed: false,
  };
}

/** The clip's actions for the range (sequence seconds; null: the whole clip), each opening its form. */
export function ClipActions({
  tl,
  doc,
  clip,
  range,
  workflows,
  disabled,
  onDone,
  openFirst,
}: {
  tl: Timeline;
  doc: Norm;
  clip: Clip;
  range: [number, number] | null;
  workflows: S<"Workflow">[];
  disabled: boolean;
  onDone: (t: Timeline) => void;
  /** opens the first action's form once they are read (an audio clip's menu: Make shot) */
  openFirst?: boolean;
}) {
  const [got, setGot] = useState<{ key: string; actions: Offer[] } | null>(null);
  // null: none chosen yet ("" closed)
  const [chosen, setOpen] = useState<string | null>(null);
  const key = JSON.stringify([tl.id, tl.revision, clip.id, range]);
  useEffect(() => {
    let stop = false;
    const [timeline, cid, r] = [tl.id, clip.id, range];
    ok(
      api.GET("/v1/studio/timelines/{timeline_id}/clips/{clip_id}/actions", {
        params: {
          path: { timeline_id: timeline, clip_id: cid },
          query: r ? { start: r[0], end: r[1] } : {},
        },
      }),
    )
      .then((a) => !stop && setGot({ key, actions: a.actions }))
      .catch(() => !stop && setGot({ key, actions: [] }));
    return () => {
      stop = true;
    };
    // the key holds every input of the request
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key]);
  // an extend first when it applies (docs/mock/TakeActions: Extend, Retake a section, Audio to video)
  const extend = (o: Offer) => Object.keys(o.inputs).some((k) => k.endsWith(".direction"));
  const actions = got?.key === key ? [...got.actions].sort((a, b) => +extend(b) - +extend(a)) : [];
  const idOf = (o: Offer) => `${o.workflow_id} ${o.param}`;
  const open = chosen ?? (openFirst && actions[0] ? idOf(actions[0]) : null);
  // an audio clip's offers are Make shot (API 1.32): one per family, named by it when there are several
  const makes = actions.filter((o) => o.slot).length;
  return actions.map((o) => {
    const id = idOf(o);
    const w = workflows.find((x) => x.id === o.workflow_id);
    return (
      <div key={id} className="flex flex-col gap-2">
        <button
          type="button"
          className="act"
          aria-expanded={open === id}
          disabled={disabled}
          onClick={() => setOpen(open === id ? "" : id)}
        >
          {o.slot ? (makes > 1 ? `Make shot · ${o.title}` : "Make shot") : (o.action ?? o.title)}
        </button>
        {open === id &&
          (w ? (
            <ActionForm
              key={key}
              tl={tl}
              doc={doc}
              clip={clip}
              offer={o}
              workflow={w}
              onDone={(t) => {
                setOpen("");
                onDone(t);
              }}
            />
          ) : (
            <ErrorNote>The workflow {o.workflow_id} is not available any more.</ErrorNote>
          ))}
      </div>
    );
  });
}

/** One action's form: the prompt, the range's or extension's readout, the same node's switches, where the result
 * goes and Generate. */
function ActionForm({
  tl,
  doc,
  clip,
  offer,
  workflow,
  onDone,
}: {
  tl: Timeline;
  doc: Norm;
  clip: Clip;
  offer: Offer;
  workflow: S<"Workflow">;
  onDone: (t: Timeline) => void;
}) {
  const fields = useMemo(() => fieldsOf(workflow), [workflow]);
  const [raws, setRaws] = useState<Record<string, Raw>>(() => rawsFrom(fields, offer.inputs));
  const before = insertsBefore(offer);
  const extendsAfter = Object.entries(offer.inputs).some(
    ([k, v]) => k.endsWith(".direction") && v === "after",
  );
  const [place, setPlace] = useState<Placement>(extendsAfter ? "insert" : "replace");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const grid = useFrameGrid(offer.family);
  const take = clip.take_id ? tl.takes[clip.take_id] : undefined;
  const v = take?.video;
  const raw = (f: Field) => raws[f.key] ?? toRaw(f.initial, f.param);
  const value = (f: Field) => {
    const r = readValue(f.param, raw(f));
    return "value" in r ? r.value : null;
  };
  const prompt = fields.find((f) => f.param.type === "text");
  const span = fields.find((f) => f.param.type === "span");
  const trim = fields.find((f) => f.param.type === "media" && f.param.trim);
  const ext = extensionOf(fields);
  const model = fields.filter((f) => f.model && f.param.type === "enum");
  // the model row (docs/mock/LtxFeatures frame 8): a variant with a choice; an option another input's values_by
  // refuses (Extend before on Distilled) is faded with its reason (API 1.39)
  const variant = model.find((f) => f.param.name === "variant" && (f.param.enum?.length ?? 0) > 1);
  const blocked = Object.fromEntries(
    (variant?.param.enum ?? []).map((o) => [o, variant && optionBlocked(variant, o, fields, raws)]),
  );
  const name = take?.title ?? "this clip";

  // what the run makes of the range, the extension or the part (lib/source: check.py's maps)
  const sp = span && raw(span);
  const stretch =
    sp && isSpan(sp) && v && grid ? retakeMap(sp.start, sp.end, v.fps, v.frames, grid) : null;
  const seconds = ext ? Number(raw(ext.seconds)) : 0;
  const grown =
    ext && v && grid
      ? extendMap(seconds, before ? "before" : "after", v.fps, v.frames, grid)
      : null;
  const part = trim && raw(trim);
  const audioFps = trim ? Number(trim.siblings?.fps) : 0;
  const clipOfPart =
    part && isRef(part) && part.out != null && grid && audioFps > 0
      ? audioMap(part.in ?? 0, part.out, audioFps, grid)
      : null;
  const all = Object.fromEntries(
    fields.map((f) => [f.key, value(f)]).filter(([, x]) => x !== null),
  );
  const length = estimateLength(all, v?.fps ?? 24, grid, trim?.param.max_s);
  const s1 = (x: number) => `${x.toFixed(1)} s`;
  const inserted = before
    ? `Insert before: a ${s1(length)} clip before ${name}.`
    : `Insert after: a ${s1(length)} clip after ${name} holds only the new part.`;
  const replaced = ext
    ? before
      ? `Replace: ${name} starts at the new part and keeps its end.`
      : `Replace: ${name} runs on to ${grown && "frames" in grown && v ? `${(grown.frames / v.fps).toFixed(2)} s` : "the extension's end"}; later clips move.`
    : span
      ? "Replace keeps the clip's in and out; nothing moves."
      : `Replace: the result takes ${name}'s slot.`;
  // an extend shows both effects, the chosen one first (docs/mock/TakeActions); the rest the chosen one alone
  const chosen = place === "insert" ? [inserted, replaced] : [replaced, inserted];
  const note = ext ? chosen.join(" ") : chosen[0];

  async function generate() {
    setBusy(true);
    setError("");
    try {
      const built = buildInputs(fields, raws);
      const bad = Object.entries(built.errors)[0];
      if (bad) throw new Error(`${bad[0]}: ${bad[1]}`);
      const id = newId(doc);
      // Make shot (API 1.32): the offer's own slot, placed exactly at the audio clip's start on V1
      const slot = offer.slot
        ? { ...offer.slot, id }
        : actionSlot(offer, { id: clip.id, at: clip.at ?? 0 }, place, length, id);
      const res = await ok(
        api.POST("/v1/studio/timelines/{timeline_id}/shots", {
          params: { path: { timeline_id: tl.id } },
          body: {
            slot,
            run: {
              workflow_id: workflow.id,
              inputs: built.inputs,
              ...(tl.defaults.looks.length && { looks: tl.defaults.looks }),
            },
          },
        }),
      );
      shots.set("clip" in slot ? slot.clip : id, {
        action: offer.action ?? offer.title,
        replace: place === "replace",
        source: clip.number,
      });
      onDone(res.timeline);
    } catch (e) {
      setError(message(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="flex flex-col gap-2.5 rounded-[18px_18px_18px_8px] bg-s2 p-3">
      {variant && (
        <>
          <div className="flex items-center justify-between gap-2">
            <span>{display(variant, "").label}</span>
            <div className="seg" role="group" aria-label={display(variant, "").label}>
              {variant.param.enum?.map((o) => (
                <button
                  key={o}
                  type="button"
                  aria-pressed={raw(variant) === o}
                  aria-disabled={blocked[o] ? true : undefined}
                  title={blocked[o] ?? undefined}
                  className={blocked[o] ? "opacity-55" : undefined}
                  onClick={() => !blocked[o] && setRaws((r) => ({ ...r, [variant.key]: o }))}
                >
                  {pretty(o)}
                </button>
              ))}
            </div>
          </div>
          {Object.values(blocked)
            .filter(Boolean)
            .map((why) => (
              <span key={String(why)} className="text-[12px] text-t2">
                {why}
              </span>
            ))}
        </>
      )}
      {prompt && (
        <>
          <label htmlFor={`act-${prompt.key}`} className="text-[12px] text-t2">
            {prompt.param.label ?? "Prompt"}
          </label>
          <PromptArea
            id={`act-${prompt.key}`}
            rows={2}
            value={String(raw(prompt))}
            onChange={(x) => setRaws((r) => ({ ...r, [prompt.key]: x }))}
            placeholder="What happens in this stretch"
            className="w-full resize-none rounded-[10px] border-0 bg-s4 px-2.5 py-2 text-[13px] text-t1 outline-0"
          />
        </>
      )}
      {stretch && grid && (
        <span className={`mono text-[12px] ${"error" in stretch ? "text-bad" : "text-t2"}`}>
          {"error" in stretch
            ? stretch.error
            : `Regenerates frames ${Math.round(stretch.map.new_in_s * v!.fps)} – ${Math.round(stretch.map.new_out_s * v!.fps) - 1} · ${stretch.map.new_in_s.toFixed(2)} – ${stretch.map.new_out_s.toFixed(2)} s on the ${grid.step}-frame grid`}
        </span>
      )}
      {ext && (
        <>
          <label
            htmlFor={`act-${ext.seconds.key}`}
            className="flex items-center justify-between gap-2"
          >
            <span>{ext.seconds.param.label ?? "Seconds"}</span>
            <span className="mono text-[12px]">
              {s1(seconds)}
              {grown && "frames" in grown && v && grid
                ? ` · + ${grown.frames - floorGrid(v.frames, grid)} frames`
                : ""}
              {` · ${before ? "before" : "after"}`}
            </span>
          </label>
          <input
            id={`act-${ext.seconds.key}`}
            type="range"
            min={ext.seconds.param.min}
            max={ext.seconds.param.max}
            step={ext.seconds.param.step ?? 0.1}
            value={seconds}
            onChange={(e) => setRaws((r) => ({ ...r, [ext.seconds.key]: e.target.value }))}
            className="w-full accent-a2"
          />
          {grown && "error" in grown && (
            <span className="mono text-[12px] text-bad">{grown.error}</span>
          )}
        </>
      )}
      {clipOfPart && part && isRef(part) && (
        <span className="mono text-[12px] text-t2">
          {"error" in clipOfPart
            ? clipOfPart.error
            : `${(part.in ?? 0).toFixed(2)} – ${part.out!.toFixed(2)} s → ${clipOfPart.frames} frames · ${clipOfPart.map.new_out_s.toFixed(2)} s`}
        </span>
      )}
      {span &&
        fields
          .filter((f) => f.node === span.node && f.param.type === "bool")
          .map((f) => (
            <Switch
              key={f.key}
              f={f}
              raw={raw(f)}
              onChange={(k, x) => setRaws((r) => ({ ...r, [k]: x as Raw }))}
            />
          ))}
      {offer.slot && "at" in offer.slot && (
        <>
          <span className="mono order-first text-[12px]">
            {[
              offer.action ?? offer.title,
              model[0]?.nodeTitle,
              `at ${timecode(offer.slot.at)}`,
              `${(offer.slot.length ?? 0).toFixed(2)} s, into V1`,
            ]
              .filter(Boolean)
              .join(" · ")}
          </span>
          <span className="text-[12px] text-t2">
            The clip&apos;s sound goes in as the shot&apos;s audio
            {trim?.param.max_s ? `, up to ${trim.param.max_s.toFixed(1)} s` : ""}. The picture
            follows the sound; it is not lip sync.
          </span>
        </>
      )}
      <div className={`flex items-center justify-between gap-2 ${offer.slot ? "hidden" : ""}`}>
        <span>Result</span>
        <div className="seg" role="group" aria-label="Where the result goes">
          <button
            type="button"
            aria-pressed={place === "replace"}
            onClick={() => setPlace("replace")}
          >
            Replace
          </button>
          <button
            type="button"
            aria-pressed={place === "insert"}
            onClick={() => setPlace("insert")}
          >
            {before ? "Insert before" : "Insert after"}
          </button>
        </div>
      </div>
      {!offer.slot && <span className="text-[12px] text-t2">{note}</span>}
      <div className="flex flex-wrap items-center gap-2">
        {model.length > 0 && (
          <span className="chip bevel rounded-[10px]" title={model[0].nodeHelp}>
            {[model[0].nodeTitle, ...prettyModel(model.map((f) => String(raw(f))))].join(" · ")}
          </span>
        )}
        {tl.session_id && (
          <Link
            href={`/?s=${encodeURIComponent(tl.session_id)}`}
            className="chip bevel rounded-[10px]"
          >
            Open in Create
          </Link>
        )}
        <div className="grow" />
        <button
          type="button"
          className="key h-11 rounded-xl px-5 text-sm"
          disabled={busy}
          onClick={() => void generate()}
        >
          {busy ? "Starting…" : "Generate"}
        </button>
      </div>
      {error && <ErrorNote>{error}</ErrorNote>}
    </div>
  );
}

/** The inspector's note when a range narrows the offers (docs/mock/TakeActions.dc.html). */
export const ActionsNote = () => (
  <span className="text-[12px] text-t2">
    Extend shows when the range reaches the take&apos;s end (after) or starts at its first frame
    (before).
  </span>
);
