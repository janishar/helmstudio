"use client";
// The standard request of a block (MagicBoard frames 2c-2h and 3's middle; contracts/README.md "Magic: the standard
// request"): the model, the kind's shared fields (the same few for every model), each one the picked model can't
// fill greyed with the reason, the model's own params under Advanced, and the GPU-time estimate before Generate.
import { Clock3 } from "lucide-react";
import { refText } from "@/components/magic/lanes";
import { useVoices } from "@/lib/library";
import { secs, type Block, type Doc, type MagicRequest, type Ref } from "@/lib/magic";
import { estText } from "@/lib/magic-fixture";
import {
  FIELD_LABEL,
  kindOf,
  modelOf,
  packSeconds,
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

/** The estimate line: studio's for the block when it has one, else the pack's. */
export function EstimateLine({
  est,
  model,
  block,
  doc,
}: {
  est?: Estimate;
  model?: Model;
  block: Pick<Block, "kind" | "length" | "request">;
  doc: Doc;
}) {
  const seconds = est?.seconds ?? packSeconds(model, block, doc);
  return (
    <span
      className="est"
      title={
        est?.source === "yours"
          ? "From your own recent runs of this model"
          : "From the model's declared rate (no runs of yours yet)"
      }
    >
      <Clock3 size={13} aria-hidden />
      <b>{seconds == null ? "Time unknown" : estText(seconds)}</b>
      {est?.host ? ` · ${est.host}` : ""}
    </span>
  );
}

export function RequestForm({
  doc,
  block,
  kinds,
  request: req,
  onChange,
  advancedOpen = false,
}: {
  doc: Doc;
  /** the block it is for (its id, kind and length; a draft in Add) */
  block: Pick<Block, "id" | "kind" | "length">;
  kinds: Kinds | null;
  request: MagicRequest;
  onChange: (req: MagicRequest) => void;
  advancedOpen?: boolean;
}) {
  const voices = useVoices();
  const kind = kindOf(kinds, block.kind);
  const models = kind?.models ?? [];
  const model = modelOf(kinds, req);
  const fps = doc.target.fps;
  const set = (patch: Partial<MagicRequest>) => onChange({ ...req, ...patch });
  const greyed = (f: Field) => (model ? model.greyed[f] : undefined);
  const choices = picks(doc, block.id);

  const field = (f: Field, body: React.ReactNode) => {
    const why = greyed(f);
    return (
      <div key={f} className={`fld ${f === "prompt" || f === "line" || f === "refs" ? "col-span-full" : ""} ${why ? "opacity-50" : ""}`}>
        <span className="lb">{FIELD_LABEL[f]}</span>
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
        <textarea
          aria-label="Prompt"
          className="in min-h-16 px-2.5 py-2 text-[13px]"
          value={req.prompt ?? ""}
          onChange={(e) => set({ prompt: e.target.value })}
        />,
      ),
    line: () =>
      field(
        "line",
        <textarea
          aria-label="Line"
          className="in min-h-12 px-2.5 py-2 text-[13px]"
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
          {secs(block.length, fps)} · {block.length} f
        </span>,
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
        <span className="flex flex-wrap items-center gap-2 text-[13px]">
          <input
            type="number"
            aria-label="Seed"
            placeholder="Random"
            className="in h-8 w-full px-2"
            min={-1}
            value={req.seed == null || req.seed < 0 ? "" : req.seed}
            onChange={(e) =>
              set({
                seed: e.target.value === "" ? undefined : Math.max(-1, Math.round(+e.target.value)),
              })
            }
          />
          <span className="seg" role="group" aria-label="Seeds">
            {[1, 2, 3, 4].map((n) => (
              <button
                key={n}
                type="button"
                aria-pressed={(req.count ?? 1) === n}
                aria-label={n === 1 ? "1 seed" : `${n} seeds`}
                onClick={() => set({ count: n === 1 ? undefined : n })}
              >
                {n}
              </button>
            ))}
          </span>
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
  const adv = model?.advanced ?? [];
  const advValues = (req.advanced ?? {}) as Record<string, unknown>;
  return (
    <div className="flex flex-col gap-3">
      <div className="fld">
        <span className="lb">Model</span>
        {models.length ? (
          <div className="seg flex-wrap" role="group" aria-label="Model">
            {models.map((m) => (
              <button
                key={m.workflow_id}
                type="button"
                aria-pressed={model?.workflow_id === m.workflow_id}
                title={m.title}
                onClick={() => set({ model: m.workflow_id, advanced: undefined })}
              >
                {m.name}
              </button>
            ))}
          </div>
        ) : (
          <span className="text-[12px] text-t2">No model for this kind on any host</span>
        )}
      </div>
      <div className="grid grid-cols-[repeat(auto-fill,minmax(150px,1fr))] gap-3">
        {(kind?.fields ?? []).map((f) => body[f]())}
      </div>
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
