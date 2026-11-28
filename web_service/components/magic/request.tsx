"use client";
// The standard request of a block (MagicBoard frames 2c-2h and 3's middle; contracts/README.md "Magic: the standard
// request"): the model, the kind's shared fields (the same few for every model), each one the picked model can't
// fill greyed with the reason, the model's own params under Advanced, and the GPU-time estimate before Generate.
import { useEffect, useState } from "react";
import { Clock3, Plus, X } from "lucide-react";
import { openMenu } from "@/components/context-menu";
import { RunOnChip } from "@/components/create/run-on";
import { useHosts } from "@/components/create/memory-chip";
import { TextArea } from "@/components/expand-text";
import { AtText, BOX } from "@/components/magic/at-text";
import { LinkGlyph } from "@/components/magic/icons";
import { STATUS, refText } from "@/components/magic/lanes";
import { turnMenu } from "@/components/magic/menus";
import { api, ok, type S } from "@/lib/api";
import { useVoices } from "@/lib/library";
import { machines, pinnedMachine, runnerWhy } from "@/lib/run-on";
import {
  estText,
  secs,
  type Block,
  type Doc,
  type MagicRequest,
  type Ref,
  type Status,
} from "@/lib/magic";
import {
  FIELD_LABEL,
  KEYS,
  advancedOf,
  kindOf,
  modelOf,
  packSeconds,
  VARIANT,
  variantChoices,
  variantOf,
  type Estimate,
  type Field,
  type Kinds,
  type Model,
} from "@/lib/magic-standard";

/** A pick's key in a select. */
const keyOf = (r?: Ref) => (!r ? "" : "asset" in r ? `a:${r.asset}` : `b:${r.block}:${r.part}`);
const refOf = (k: string): Ref | undefined => {
  if (k.startsWith("a:")) return { asset: k.slice(2) };
  const [, block, part] = k.split(":");
  return block ? { block, part: part as "first" | "last" | "take" } : undefined;
};

/** What a frame or ref can pick on the board: other shots' first and last frames, stills' takes, assets. */
function picks(d: Doc, self: string): Ref[] {
  const out: Ref[] = [];
  for (const b of d.blocks) {
    if (b.id === self) continue;
    if (b.kind === "video") out.push({ block: b.id, part: "last" }, { block: b.id, part: "first" });
    else if (b.kind === "image" || b.kind === "frame") out.push({ block: b.id, part: "take" });
  }
  for (const a of d.assets) out.push({ asset: a.id });
  return out;
}

/** The estimate line: studio's for the block when it has one, else the pack's; both follow the variant (API 1.94),
 * named last ("≈ 6 s each · mac-studio · Turbo", MagicBoard 7). */
export function EstimateLine({
  est,
  model,
  block,
  doc,
  variant,
}: {
  est?: Estimate;
  model?: Model;
  block: Pick<Block, "kind" | "length" | "request">;
  doc: Doc;
  /** the variant's title */
  variant?: string;
}) {
  const seconds = est?.seconds ?? packSeconds(model, block, doc);
  return (
    <span
      className="est"
      title={
        est?.source === "yours"
          ? `From your own recent runs of this model${variant ? ` at ${variant}` : ""}`
          : seconds == null
            ? `No runs of yours${variant ? ` at ${variant}` : ""} yet, and the model declares no rate for it`
            : "From the model's declared rate (no runs of yours yet)"
      }
    >
      <Clock3 size={13} aria-hidden />
      <b>{seconds == null ? "Time unknown" : estText(seconds)}</b>
      {est?.host ? ` · ${est.host}` : ""}
      {variant ? ` · ${variant}` : ""}
    </span>
  );
}

// GET /models, shared by the dialogs' Variant fields for 30 s (titles, devices, which host has which variant)
let models: { at: number; p: Promise<S<"ModelList"> | null> } | null = null;

/** The models list (null before it comes and without studio). */
export function useModelList() {
  const [list, setList] = useState<S<"ModelList"> | null>(null);
  useEffect(() => {
    if (!models || Date.now() - models.at > 30_000)
      models = { at: Date.now(), p: ok(api.GET("/v1/studio/models")).catch(() => null) };
    let live = true;
    void models.p.then((l) => live && setList(l));
    return () => {
      live = false;
    };
  }, []);
  return list;
}

/** A request's variant (API 1.94): the choice, the picked one's title and why one cannot run (the picked one's
 * first). */
export function useVariant(model: Model | undefined, req: MagicRequest) {
  const list = useModelList();
  const mcs = machines(useHosts());
  const choices = variantChoices(model, req, list, mcs, pinnedMachine(mcs, req.host ?? ""));
  const cur = variantOf(model, req);
  const picked = choices.find((c) => c.id === cur);
  return {
    choices,
    cur,
    title: picked?.title,
    why: picked?.why ?? choices.find((c) => c.why)?.why,
  };
}

/** Variant beside Model (MagicBoard 3 and 7, API 1.94): the model's variants, one that cannot run greyed with why;
 * the pick is the request's Advanced model.variant (absent: the workflow's own). */
export function VariantField({
  model,
  req,
  set,
}: {
  model?: Model;
  req: MagicRequest;
  set: (p: Partial<MagicRequest>) => void;
}) {
  const { choices, cur, why } = useVariant(model, req);
  if (!choices.length) return null;
  const own = variantOf(model, { ...req, advanced: undefined });
  const pick = (id: string) => {
    const adv = { ...req.advanced };
    if (id === own) delete adv[VARIANT];
    else adv[VARIANT] = id;
    set({ advanced: Object.keys(adv).length ? adv : undefined });
  };
  return (
    <div className="fld">
      <span className="lb">Variant</span>
      <div className="seg flex-wrap" role="group" aria-label="Variant">
        {choices.map((c) => (
          <button
            key={c.id}
            type="button"
            aria-pressed={cur === c.id}
            disabled={!!c.why && cur !== c.id}
            className="disabled:opacity-45"
            title={c.why}
            onClick={() => pick(c.id)}
          >
            {c.title}
          </button>
        ))}
      </div>
      {why && <span className="text-[11px] text-t2">{why}</span>}
    </div>
  );
}

/** What a block is tied to, as its form says it ("Tied to Shot 2 · moves with it", MagicBoard 2e, 2f), and Unlink. */
export type Tie = { to: string[]; onUnlink: () => void };

/** "Tied to Shot 2 · moves with it", "Tied to Shots 1–3 · stretches with them". */
export function tieText(d: Doc, to: string[]): string {
  const names = to.map(
    (id) => d.blocks.find((b) => b.id === id)?.name.split(" · ")[0] ?? "a gone shot",
  );
  if (names.length === 1) return `Tied to ${names[0]} · moves with it`;
  const nums = names.map((n) => /(\d+)$/.exec(n)?.[1]);
  const run = nums.every(Boolean) ? `Shots ${nums[0]}–${nums.at(-1)}` : names.join(", ");
  return `Tied to ${run} · stretches with them`;
}

/** Run on (MagicBoard frame 3, as Create's: docs/mock/RunOn.dc.html): Auto, or a host, greyed when it can't take the
 * model's workflow. */
function RunOnField({
  req,
  env,
  set,
}: {
  req: MagicRequest;
  env?: string;
  set: (p: Partial<MagicRequest>) => void;
}) {
  const hosts = useHosts();
  const list = machines(hosts);
  const chosen = pinnedMachine(list, req.host ?? "");
  return (
    <div className="fld">
      <span className="lb">Run on</span>
      <span className="flex">
        <RunOnChip
          id="magic-run-on"
          list={list}
          chosen={chosen}
          env={env}
          picks={[]}
          // ErrorStates b2: the warn look when no runner for its env is on
          why={chosen && !chosen.live ? undefined : runnerWhy(hosts, env, chosen)?.line}
          onPick={(host) => set({ host: host || undefined })}
        />
      </span>
    </div>
  );
}

/** A dialogue's lines (a Voice block with turns, API 1.89): each a speaker (a board asset with a voice) and its words,
 * its own status; Add a line; each row's menu moves or removes it. */
function Turns({
  doc,
  req,
  set,
  statuses,
}: {
  doc: Doc;
  req: MagicRequest;
  set: (p: Partial<MagicRequest>) => void;
  statuses?: Status[];
}) {
  const turns = req.turns ?? [];
  const speakers = doc.assets.filter((a) => a.voice_id);
  const put = (next: { speaker: string; text: string }[]) => set({ turns: next });
  const move = (i: number, by: number) => {
    const next = [...turns];
    const [t] = next.splice(i, 1);
    next.splice(i + by, 0, t);
    put(next);
  };
  return (
    <div className="fld col-span-full">
      <span className="lb">Lines · {turns.length}</span>
      <div className="flex flex-col gap-1.5" role="list" aria-label="Lines">
        {turns.map((t, i) => {
          const st = statuses?.[i];
          return (
            <div
              key={i}
              role="listitem"
              aria-label={`Line ${i + 1}`}
              className="flex items-center gap-1.5"
              onContextMenu={(e) =>
                openMenu(
                  e,
                  turnMenu(i, turns.length, {
                    up: () => move(i, -1),
                    down: () => move(i, 1),
                    remove: () => put(turns.filter((_, j) => j !== i)),
                  }),
                )
              }
            >
              <select
                aria-label={`Line ${i + 1} speaker`}
                className="in h-8 w-[110px] shrink-0 px-2 text-[13px]"
                value={t.speaker}
                onChange={(e) =>
                  put(turns.map((x, j) => (j === i ? { ...x, speaker: e.target.value } : x)))
                }
              >
                <option value="">Speaker</option>
                {speakers.map((a) => (
                  <option key={a.id} value={a.id}>
                    @{a.handle}
                  </option>
                ))}
              </select>
              <input
                aria-label={`Line ${i + 1}`}
                className="in h-8 min-w-0 grow px-2 text-[13px]"
                value={t.text}
                placeholder="What they say"
                onChange={(e) =>
                  put(turns.map((x, j) => (j === i ? { ...x, text: e.target.value } : x)))
                }
              />
              {st &&
                (st.s === "running" ? (
                  <span className="mst run">{Math.round(st.progress)}%</span>
                ) : (
                  <span
                    className={`mst ${STATUS[st.s][0]}`}
                    title={st.s === "failed" ? st.error : undefined}
                  >
                    {STATUS[st.s][1]}
                  </span>
                ))}
              <button
                type="button"
                aria-label={`Remove line ${i + 1}`}
                className="grid size-7 shrink-0 place-items-center rounded-lg text-t2 hover:bg-s3"
                onClick={() => put(turns.filter((_, j) => j !== i))}
              >
                <X size={14} aria-hidden />
              </button>
            </div>
          );
        })}
        {!speakers.length && (
          <span className="text-[11px] text-t2">Give an asset a voice to make it a speaker.</span>
        )}
        <button
          type="button"
          className="chip bevel self-start rounded-[10px] bg-s4"
          disabled={turns.length >= 100}
          onClick={() =>
            put([...turns, { speaker: turns.at(-2)?.speaker ?? speakers[0]?.id ?? "", text: "" }])
          }
        >
          <Plus size={13} aria-hidden />
          Add a line
        </button>
      </div>
    </div>
  );
}

export function RequestForm({
  doc,
  block,
  kinds,
  request: req,
  onChange,
  advancedOpen = false,
  tie,
  runOn = false,
  turnStatuses,
}: {
  doc: Doc;
  /** the block it is for (its id, kind and length; a draft in Add) */
  block: Pick<Block, "id" | "kind" | "length">;
  kinds: Kinds | null;
  request: MagicRequest;
  onChange: (req: MagicRequest) => void;
  advancedOpen?: boolean;
  /** a voice or music block's tie, said under its fields with Unlink */
  tie?: Tie | null;
  /** the block dialog's Run on (MagicBoard frame 3) */
  runOn?: boolean;
  /** a dialogue's turn statuses (the block dialog) */
  turnStatuses?: Status[];
}) {
  const voices = useVoices();
  const kind = kindOf(kinds, block.kind);
  const models = kind?.models ?? [];
  const model = modelOf(kinds, req);
  const fps = doc.target.fps;
  const set = (patch: Partial<MagicRequest>) => onChange({ ...req, ...patch });
  const greyed = (f: Field) => (model ? model.greyed[f] : undefined);
  const choices = picks(doc, block.id);

  // music's prompt is its style (MagicBoard 2f)
  const label = (f: Field) => (f === "prompt" && block.kind === "music" ? "Style" : FIELD_LABEL[f]);
  const field = (f: Field, body: React.ReactNode) => {
    const why = greyed(f);
    return (
      <div
        key={f}
        className={`fld ${f === "seed" && !req.turns ? "sm:col-span-2" : ""} ${f === "prompt" || f === "line" || f === "refs" || f === "lyrics" ? "col-span-full" : ""} ${why ? "opacity-50" : ""}`}
      >
        <span className="lb">{label(f)}</span>
        <fieldset disabled={!!why} className="m-0 contents border-0 p-0">
          {body}
        </fieldset>
        {why && <span className="text-[11px] text-t2">{why}</span>}
      </div>
    );
  };
  const framePick = (f: "first" | "last") =>
    field(
      f,
      <select
        aria-label={FIELD_LABEL[f]}
        className="in h-8 px-2 text-[13px]"
        value={keyOf(req[f])}
        onChange={(e) => set({ [f]: refOf(e.target.value) })}
      >
        <option value="">None</option>
        {choices.map((r) => (
          <option key={keyOf(r)} value={keyOf(r)}>
            {refText(doc, r)}
          </option>
        ))}
      </select>,
    );

  const body: Record<Field, () => React.ReactNode> = {
    prompt: () =>
      field(
        "prompt",
        <AtText
          doc={doc}
          self={block.id}
          label={label("prompt")}
          value={req.prompt ?? ""}
          refs={req.refs ?? []}
          // a model that takes no refs keeps them out (its @ picks are then words only)
          onChange={(prompt, refs) => set(greyed("refs") ? { prompt } : { prompt, refs })}
        />,
      ),
    line: () =>
      field(
        "line",
        <TextArea
          expand="Line"
          aria-label="Line"
          className={BOX}
          value={req.line ?? ""}
          onChange={(e) => set({ line: e.target.value })}
        />,
      ),
    refs: () =>
      field(
        "refs",
        <div className="flex flex-wrap gap-1.5" role="group" aria-label="Refs">
          {choices
            .filter((r) => "asset" in r || r.part !== "first")
            .map((r) => {
              const on = (req.refs ?? []).some((x) => keyOf(x) === keyOf(r));
              return (
                <button
                  key={keyOf(r)}
                  type="button"
                  className={`rc big ${on ? "lk" : ""}`}
                  aria-pressed={on}
                  onClick={() =>
                    set({
                      refs: on
                        ? (req.refs ?? []).filter((x) => keyOf(x) !== keyOf(r))
                        : [...(req.refs ?? []), r],
                    })
                  }
                >
                  {refText(doc, r)}
                </button>
              );
            })}
          {!choices.length && (
            <span className="text-[12px] text-t2">Nothing on the board to pick yet</span>
          )}
        </div>,
      ),
    first: () => framePick("first"),
    last: () => framePick("last"),
    length: () =>
      field(
        "length",
        <span className="mono text-[13px]">
          {/* audio runs for the block's seconds; a video on its frames */}
          {block.kind === "music" || block.kind === "sound"
            ? secs(block.length, fps)
            : `${secs(block.length, fps)} · ${block.length} f`}
        </span>,
      ),
    // API 1.85: music's lyrics (empty: an instrumental), key and BPM; a sound made from a shot's video
    lyrics: () =>
      field(
        "lyrics",
        <TextArea
          expand="Lyrics"
          aria-label="Lyrics"
          placeholder="Instrumental"
          className={BOX}
          value={req.lyrics ?? ""}
          onChange={(e) => set({ lyrics: e.target.value || undefined })}
        />,
      ),
    key: () =>
      field(
        "key",
        <select
          aria-label="Key"
          className="in h-8 px-2 text-[13px]"
          value={req.key ?? ""}
          onChange={(e) => set({ key: e.target.value || undefined })}
        >
          <option value="">Auto</option>
          {KEYS.map((k) => (
            <option key={k}>{k}</option>
          ))}
        </select>,
      ),
    bpm: () =>
      field(
        "bpm",
        <input
          type="number"
          aria-label="BPM"
          placeholder="Auto"
          className="in h-8 w-full px-2 text-[13px]"
          min={30}
          max={300}
          value={req.bpm ?? ""}
          onChange={(e) =>
            set({
              bpm:
                e.target.value === ""
                  ? undefined
                  : Math.min(300, Math.max(30, Math.round(+e.target.value))),
            })
          }
        />,
      ),
    from: () =>
      field(
        "from",
        <select
          aria-label="From"
          className="in h-8 px-2 text-[13px]"
          value={req.from ?? ""}
          // a model that takes no video keeps its prompt only; Advanced is the workflow's that runs
          onChange={(e) => set({ from: e.target.value || undefined, advanced: undefined })}
        >
          <option value="">A prompt</option>
          {doc.blocks
            .filter((x) => x.kind === "video")
            .map((x) => (
              <option key={x.id} value={x.id}>
                {x.name.split(" · ")[0]} video
              </option>
            ))}
        </select>,
      ),
    size: () =>
      field(
        "size",
        <span className="flex items-center gap-1.5 text-[13px]">
          <input
            type="number"
            aria-label="Width"
            className="in h-8 w-[68px] px-2"
            min={16}
            max={8192}
            value={req.width ?? doc.target.width}
            onChange={(e) => set({ width: Math.max(16, Math.round(+e.target.value)) || undefined })}
          />
          ×
          <input
            type="number"
            aria-label="Height"
            className="in h-8 w-[68px] px-2"
            min={16}
            max={8192}
            value={req.height ?? doc.target.height}
            onChange={(e) =>
              set({ height: Math.max(16, Math.round(+e.target.value)) || undefined })
            }
          />
        </span>,
      ),
    seed: () =>
      field(
        "seed",
        // "42 · 2 seeds" (MagicBoard 2c): the seed, then how many runs one Generate makes (seed, seed + 1, …)
        <span className="flex items-center gap-1.5 text-[13px]">
          <input
            type="number"
            aria-label="Seed"
            placeholder="Random"
            className="in mono h-8 min-w-[84px] flex-1 px-2"
            min={-1}
            value={req.seed == null || req.seed < 0 ? "" : req.seed}
            onChange={(e) =>
              set({
                seed: e.target.value === "" ? undefined : Math.max(-1, Math.round(+e.target.value)),
              })
            }
          />
          {!req.turns && (
            <>
              <span className="text-t2">·</span>
              <select
                aria-label="Seeds"
                className="in h-8 w-auto shrink-0 px-1.5 text-[13px]"
                value={req.count ?? 1}
                onChange={(e) =>
                  set({ count: +e.target.value === 1 ? undefined : +e.target.value })
                }
              >
                {[1, 2, 3, 4].map((n) => (
                  <option key={n} value={n}>
                    {n === 1 ? "1 seed" : `${n} seeds`}
                  </option>
                ))}
              </select>
            </>
          )}
        </span>,
      ),
    voice: () =>
      field(
        "voice",
        <select
          aria-label="Voice"
          className="in h-8 px-2 text-[13px]"
          value={req.voice ? `a:${req.voice}` : req.voice_id ? `v:${req.voice_id}` : ""}
          onChange={(e) => {
            const v = e.target.value;
            set({
              voice: v.startsWith("a:") ? v.slice(2) : undefined,
              voice_id: v.startsWith("v:") ? v.slice(2) : undefined,
            });
          }}
        >
          <option value="">Pick a voice</option>
          {doc.assets
            .filter((a) => a.voice_id)
            .map((a) => (
              <option key={a.id} value={`a:${a.id}`}>
                @{a.handle}
              </option>
            ))}
          {(voices ?? []).map((v) => (
            <option key={v.id} value={`v:${v.id}`}>
              {v.name}
            </option>
          ))}
        </select>,
      ),
  };

  if (kind?.off) return <p className="m-0 text-[13px] text-t2">{kind.off}</p>;
  const adv = advancedOf(model, req);
  const advValues = (req.advanced ?? {}) as Record<string, unknown>;
  const others = models.filter((m) => m.workflow_id !== model?.workflow_id).map((m) => m.name);
  // a dialogue's lines take the place of the line and the voice (API 1.89)
  const fields = (kind?.fields ?? []).filter((f) => !req.turns || (f !== "line" && f !== "voice"));
  return (
    <div className="flex flex-col gap-3">
      <div
        className={runOn ? "grid grid-cols-[repeat(auto-fit,minmax(150px,1fr))] gap-3" : "contents"}
      >
        <div className="fld">
          <span className="lb">Model</span>
          {models.length ? (
            // a select with the other models as its hint (MagicBoard 2c: "or MiniMax H3")
            <span className="flex min-w-0 flex-wrap items-center gap-2">
              <select
                aria-label="Model"
                className="in h-8 min-w-0 px-2 text-[13px]"
                value={model?.workflow_id ?? ""}
                title={model?.title}
                // a model that takes no video drops the shot it was made from (its From would stay greyed)
                onChange={(e) => {
                  const m = models.find((x) => x.workflow_id === e.target.value)!;
                  set({
                    model: m.workflow_id,
                    advanced: undefined,
                    ...(m.fields.includes("from") ? {} : { from: undefined }),
                  });
                }}
              >
                {models.map((m) => (
                  <option key={m.workflow_id} value={m.workflow_id}>
                    {m.name}
                  </option>
                ))}
              </select>
              {others.length > 0 && (
                <span className="text-[11px] text-t2">or {others.join(", ")}</span>
              )}
            </span>
          ) : (
            <span className="text-[12px] text-t2">No model for this kind on any host</span>
          )}
        </div>
        <VariantField model={model} req={req} set={set} />
        {runOn && <RunOnField req={req} env={model?.env} set={set} />}
      </div>
      <div className="grid grid-cols-[repeat(auto-fill,minmax(150px,1fr))] gap-3">
        {req.turns && <Turns doc={doc} req={req} set={set} statuses={turnStatuses} />}
        {fields.map((f) => body[f]())}
        {/* a still's place on the board (MagicBoard 2h) */}
        {block.kind === "image" && (
          <div className="fld">
            <span className="lb">Length on board</span>
            <span className="mono text-[13px]">{secs(block.length, fps)}</span>
          </div>
        )}
        {/* Loop (MagicBoard 2g, API 1.89): the take repeats to fill the block; a sound from a shot's video follows it */}
        {block.kind === "sound" && (
          <div className={`fld ${req.from ? "opacity-50" : ""}`}>
            <span className="lb">Loop</span>
            <span className="seg" role="group" aria-label="Loop">
              {[false, true].map((on) => (
                <button
                  key={String(on)}
                  type="button"
                  disabled={!!req.from}
                  aria-pressed={!!req.loop === on}
                  onClick={() => set({ loop: on || undefined })}
                >
                  {on ? "On" : "Off"}
                </button>
              ))}
            </span>
            {req.from && <span className="text-[11px] text-t2">Not with a shot&apos;s video</span>}
          </div>
        )}
      </div>
      {tie && (
        <div className="nr">
          <span className="lkm" aria-hidden>
            <LinkGlyph />
          </span>
          {tieText(doc, tie.to)}
          <span className="grow" />
          <button type="button" className="chip bevel rounded-[10px] bg-s4" onClick={tie.onUnlink}>
            Unlink
          </button>
        </div>
      )}
      {adv.length > 0 && (
        <details className="fld" open={advancedOpen}>
          <summary className="lb cursor-pointer">
            Advanced · {adv.length} more · {model!.name}&apos;s own
          </summary>
          <div className="grid grid-cols-[repeat(auto-fill,minmax(150px,1fr))] gap-2 pt-1.5">
            {adv.map(({ path, param }) => {
              const value = advValues[path] ?? param.default;
              const put = (v: unknown) => {
                const next = { ...advValues };
                if (v === param.default || v === "" || v === undefined) delete next[path];
                else next[path] = v;
                set({ advanced: Object.keys(next).length ? next : undefined });
              };
              const label = param.label ?? path;
              return (
                <label key={path} className="flex flex-col gap-1 text-[11px] text-t2">
                  {label}
                  {param.type === "bool" ? (
                    <input
                      type="checkbox"
                      aria-label={label}
                      checked={!!value}
                      onChange={(e) => put(e.target.checked)}
                    />
                  ) : param.type === "enum" ? (
                    <select
                      aria-label={label}
                      className="in h-7 px-1.5 text-[12px]"
                      value={String(value ?? "")}
                      onChange={(e) => put(e.target.value)}
                    >
                      {(param.enum ?? []).map((o) => (
                        <option key={o}>{o}</option>
                      ))}
                    </select>
                  ) : param.type === "string" || param.type === "text" ? (
                    <input
                      aria-label={label}
                      className="in h-7 px-1.5 text-[12px]"
                      value={String(value ?? "")}
                      onChange={(e) => put(e.target.value)}
                    />
                  ) : (
                    <input
                      type="number"
                      aria-label={label}
                      className="in h-7 px-1.5 text-[12px]"
                      min={param.min ?? undefined}
                      max={param.max ?? undefined}
                      step={param.step ?? (param.type === "float" ? "any" : 1)}
                      value={value == null || value === "auto" ? "" : Number(value)}
                      onChange={(e) => put(e.target.value === "" ? undefined : +e.target.value)}
                    />
                  )}
                </label>
              );
            })}
          </div>
        </details>
      )}
    </div>
  );
}
