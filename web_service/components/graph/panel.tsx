"use client";
// The Graph view's inspector (docs/mock/Graph.dc.html aside): the selected node's parameters (Create's widgets; a
// wired value is read-only), its inputs (a keyboard way to connect), where its params show in Studio, and Bypass.
import { useState } from "react";
import { Dices } from "lucide-react";
import { Info } from "@/components/kit";
import { Switch } from "@/components/create/form";
import { PromptArea } from "@/components/create/prompt";
import { LoraRows } from "@/components/create/loras";
import { loraFieldOf } from "@/lib/loras";
import { allowedInts } from "@/lib/video";
import type { S } from "@/lib/api";
import {
  WIREABLE,
  canConnect,
  passthrough,
  splitRef,
  type Doc,
  type Palette,
  type ParamValue,
  type Problem,
} from "@/lib/graph";
import { connect, disconnect, setBypass, setParam } from "@/lib/graph-edit";
import { deriveStudio, hideFromStudio, placement, showInStudio } from "@/lib/studio";
import { pretty, readValue, toRaw, type Field } from "@/lib/workflow";

type Schema = S<"NodeSchema">;
type Schemas = Record<string, Schema>;

/** A param value as a node card shows it. */
export function valueText(p: S<"ParamSchema">, v: unknown): string {
  if (p.type === "seed" && (v == null || v === -1)) return "Random";
  if (p.type === "bool") return v ? "On" : "Off";
  if (p.type === "rows") {
    const n = Array.isArray(v) ? v.length : 0;
    const item = Object.values(p.item ?? {});
    const what = item.includes("Image") ? "image" : item.includes("Lora") ? "LoRA" : "item";
    return `${n} ${what}${n === 1 ? "" : "s"}`;
  }
  if (p.type === "image") return v ? "an image" : "none";
  if (p.type === "enum") return pretty(String(v));
  return String(v ?? "");
}

const ENV_LABELS: Record<string, string> = { mlx: "MLX", native: "Native" };
const DEVICES = { cuda: "CUDA", mps: "MPS", cpu: "CPU" } as const;
/** A runner env as the board names it ("MLX"); torch by the devices of its hosts ("PyTorch CUDA"), MPS when none
 * reports one (Apple silicon, the board's); another env as it is. */
export function envText(env: string, hosts?: S<"Host">[] | null) {
  if (env !== "torch") return ENV_LABELS[env] ?? env;
  const kinds = new Set(
    (hosts ?? []).filter((h) => h.env === env).map((h) => DEVICES[h.device?.kind ?? "mps"]),
  );
  return `PyTorch ${kinds.size ? [...kinds].join("/") : "MPS"}`;
}

/** Title of a node for lists: its schema's title, with the id when the title is shared. */
export function nodeName(doc: Doc, schemas: Schemas, id: string): string {
  const title = schemas[doc.nodes[id]?.kind]?.title ?? id;
  const same = Object.values(doc.nodes).filter(
    (n) => (schemas[n.kind]?.title ?? "") === title,
  ).length;
  return same > 1 ? `${title} (${id})` : title;
}

export function Panel({
  doc,
  schemas,
  palette,
  id,
  problems,
  overrides,
  groupTitle,
  edit,
  onParamSet,
  loraPick = false,
  onLoraPicked,
  hosts,
}: {
  doc: Doc;
  schemas: Schemas;
  palette: Palette;
  id: string;
  problems: Problem[];
  /** Studio's values that differ from the document (this run's settings) */
  overrides: Record<string, ParamValue>;
  groupTitle?: string;
  edit: (f: (d: Doc) => Doc) => void;
  onParamSet: (key: string) => void;
  /** open this node's LoRA picker (its card's Add LoRA) */
  loraPick?: boolean;
  onLoraPicked?: () => void;
  /** the runner hosts: what torch runs on */
  hosts?: S<"Host">[] | null;
}) {
  const n = doc.nodes[id];
  const s = schemas[n.kind];
  const live = palette[n.kind];
  const mine = problems.filter((p) => p.node === id);
  const studio = doc.studio ?? deriveStudio(doc, schemas);
  if (!s)
    return (
      <>
        <Head title={id} sub={`${n.kind} · no runner offers it now`} />
        <Problems list={mine} />
      </>
    );
  const wires = n.in ?? {};
  const settable = s.params.filter((p) => !(p.name in wires));
  const bypassable = !!passthrough(s);
  const paths = [
    ...settable.map((p) => ({ path: `${id}.${p.name}`, label: p.label ?? p.name })),
    ...(bypassable ? [{ path: `${id}.bypass`, label: `Bypass ${s.title}` }] : []),
  ];
  const shown = paths.filter((p) => placement(studio, p.path));
  const hidden = paths.filter((p) => !placement(studio, p.path));
  // the ports a wire can come into: inputs, and value params that have a source or a wire now
  const sources = (key: string) =>
    Object.entries(doc.nodes).flatMap(([m, mn]) =>
      (palette[mn.kind]?.outputs ?? [])
        .filter((o) => canConnect(doc, palette, m, o.name, id, key))
        .map((o) => `${m}.${o.name}`),
    );
  const ports = [
    ...s.inputs.map((i) => ({ key: i.name, label: i.name, type: i.type, optional: !!i.optional })),
    ...s.params
      .filter((p) => WIREABLE[p.type])
      .map((p) => ({
        key: p.name,
        label: p.label ?? p.name,
        type: WIREABLE[p.type],
        optional: true,
      })),
  ].filter((p) => p.key in wires || (live && sources(p.key).length));
  return (
    <>
      <Head
        title={s.title}
        sub={[
          groupTitle,
          live?.envs.length
            ? `runs on ${live.envs.map((e) => envText(e, hosts)).join(", ")}`
            : "no runner offers it now",
          s.component && `uses the ${s.component}`,
        ]
          .filter(Boolean)
          .join(" · ")}
      />
      <Problems
        list={mine}
        fix={(p) => {
          const name = p.param;
          if (!name) return null;
          if (p.code === "unknown_param")
            return () => edit((d) => setParam(d, id, name, undefined));
          if (p.code === "studio_unknown_path")
            return () => edit((d) => hideFromStudio(d, schemas, `${id}.${name}`));
          return null;
        }}
      />
      {s.params.length > 0 && (
        <section aria-labelledby="gp-params" className="flex flex-col gap-3">
          <h3 id="gp-params" className="heading flex items-center">
            Parameters
            <Info>A wired value is read-only here. Unplug the wire to edit it on this node.</Info>
          </h3>
          {s.params.map((p) => {
            const key = `${id}.${p.name}`;
            const ref = wires[p.name];
            if (ref) {
              const [src] = splitRef(ref);
              const sn = doc.nodes[src];
              const sp =
                sn && schemas[sn.kind]?.params.length === 1 ? schemas[sn.kind].params[0] : null;
              const v = sp
                ? valueText(
                    sp,
                    overrides[`${src}.${sp.name}`] ?? sn.params?.[sp.name] ?? sp.default,
                  )
                : "";
              return (
                <div key={p.name} className="flex items-center justify-between gap-3">
                  <span>{p.label ?? p.name}</span>
                  <span className="mono text-right text-[12px] text-t2">
                    {[v, `from ${sn ? nodeName(doc, schemas, src) : src} node`]
                      .filter(Boolean)
                      .join(" · ")}
                  </span>
                </div>
              );
            }
            const value = overrides[key] ?? n.params?.[p.name] ?? p.default;
            const bad = mine.find((x) => x.param === p.name);
            if (loraFieldOf(p))
              return (
                <div key={p.name} className="flex flex-col gap-2">
                  <span className="flex items-center">
                    {p.label ?? p.name}
                    {p.help && <Info>{p.help}</Info>}
                  </span>
                  <LoraRows
                    param={p}
                    family={s.family}
                    rows={Array.isArray(value) ? value : []}
                    onRows={(rows) => {
                      edit((d) => setParam(d, id, p.name, rows));
                      onParamSet(key);
                    }}
                    error={bad?.message}
                    pick={loraPick}
                    onPickClosed={onLoraPicked}
                  />
                </div>
              );
            return (
              <ParamControl
                key={p.name}
                field={{
                  key,
                  node: id,
                  nodeTitle: s.title,
                  param: p,
                  initial: value,
                  canvas: false,
                  model: false,
                }}
                error={bad?.message}
                onSet={(v) => {
                  edit((d) => setParam(d, id, p.name, v));
                  onParamSet(key);
                }}
              />
            );
          })}
        </section>
      )}
      {ports.length > 0 && (
        <section aria-labelledby="gp-inputs" className="flex flex-col gap-3">
          <h3 id="gp-inputs" className="heading">
            Inputs
          </h3>
          {ports.map((p) => {
            const now = wires[p.key] ?? "";
            const list = sources(p.key);
            if (now && !list.includes(now)) list.unshift(now);
            const bad = mine.find((x) => x.port === p.key);
            return (
              <div key={p.key} className="flex flex-col gap-1">
                <div className="flex items-center justify-between gap-3">
                  <label htmlFor={`gp-in-${p.key}`}>
                    {p.label} <span className="text-[12px] text-t2">{p.type}</span>
                  </label>
                  <select
                    id={`gp-in-${p.key}`}
                    value={now}
                    aria-invalid={!!bad || undefined}
                    onChange={(e) => {
                      const v = e.target.value;
                      edit((d) => (v ? connect(d, id, p.key, v) : disconnect(d, id, p.key)));
                    }}
                    className="h-[34px] max-w-[60%] rounded-lg border border-line bg-s1 px-2 text-[13px] text-t1"
                  >
                    <option value="">
                      {p.optional ? "Not connected" : "Not connected (needed)"}
                    </option>
                    {list.map((r) => {
                      const [m, o] = splitRef(r);
                      return (
                        <option key={r} value={r}>
                          {doc.nodes[m] ? nodeName(doc, schemas, m) : m} · {o}
                        </option>
                      );
                    })}
                  </select>
                </div>
                {bad && <span className="text-[12px] text-bad">{bad.message}</span>}
              </div>
            );
          })}
        </section>
      )}
      {paths.length > 0 && (
        <section aria-labelledby="gp-studio" className="flex flex-col gap-2.5">
          <h3 id="gp-studio" className="heading flex items-center">
            Shown in Studio
            {!doc.studio && <Info>Every parameter shows until you hide one here.</Info>}
          </h3>
          {shown.map((p) => (
            <div key={p.path} className="flex items-center justify-between gap-3">
              <span>{studio.labels?.[p.path] ?? p.label}</span>
              <span className="flex items-center gap-2 text-[12px] text-t2">
                {placement(studio, p.path)}
                <button
                  type="button"
                  className="rounded-md px-1.5 py-0.5 text-t2 hover:bg-s3 hover:text-t1"
                  aria-label={`Hide ${p.label} in Studio`}
                  onClick={() => edit((d) => hideFromStudio(d, schemas, p.path))}
                >
                  Hide
                </button>
              </span>
            </div>
          ))}
          {hidden.length > 0 && (
            <>
              <button
                type="button"
                popoverTarget="gp-show"
                className="h-10 rounded-[10px] border border-dashed border-dash bg-transparent text-[13px]"
                style={{ anchorName: "--gp-show" } as React.CSSProperties}
              >
                Show another parameter in Studio
              </button>
              <div
                id="gp-show"
                popover="auto"
                role="menu"
                aria-label="Show in Studio"
                className="pop w-64"
                style={
                  {
                    positionAnchor: "--gp-show",
                    positionArea: "bottom span-left",
                    marginTop: 6,
                  } as React.CSSProperties
                }
              >
                <div className="flex flex-col gap-1">
                  {hidden.map((p) => (
                    <button
                      key={p.path}
                      type="button"
                      role="menuitem"
                      className="rounded-[10px] px-3 py-2 text-left text-[13px] hover:bg-s3"
                      onClick={() => {
                        document.getElementById("gp-show")?.hidePopover();
                        edit((d) => showInStudio(d, schemas, p.path, s.title));
                      }}
                    >
                      {p.label}
                    </button>
                  ))}
                </div>
              </div>
            </>
          )}
        </section>
      )}
      {bypassable && (
        <section aria-labelledby="gp-run" className="flex flex-col gap-2.5">
          <h3 id="gp-run" className="heading">
            This run
          </h3>
          <div className="flex gap-2">
            <button
              type="button"
              aria-pressed={!!n.bypass}
              title={`Bypassed, ${s.title} passes ${passthrough(s)!.join(", ")} straight through`}
              onClick={() => edit((d) => setBypass(d, id, !n.bypass))}
              className="bevel h-10 flex-1 rounded-[10px] border border-line bg-transparent text-[13px] aria-pressed:border-a1 aria-pressed:bg-s1"
            >
              {n.bypass ? "Bypassed" : "Bypass"}
            </button>
          </div>
        </section>
      )}
    </>
  );
}

/** The panel's title and the line under it. */
export function Head({ title, sub }: { title: string; sub?: string }) {
  return (
    <div className="flex flex-col gap-1">
      <h2 className="m-0 text-base font-semibold">{title}</h2>
      {sub && <span className="text-[12px] text-t2">{sub}</span>}
    </div>
  );
}

/** Problems, as a list the screen reader hears. */
/** The problems listed; `fix` gives a Remove action for one the editor can clear (a param or Studio path a runner
 * update left behind, which no other control shows). */
export function Problems({
  list,
  fix,
}: {
  list: Problem[];
  fix?: (p: Problem) => (() => void) | null;
}) {
  if (!list.length) return null;
  return (
    <ul role="list" aria-label="Problems" className="m-0 flex list-none flex-col gap-1 p-0">
      {list.map((p) => {
        const remove = fix?.(p);
        return (
          <li
            key={[p.code, p.node, p.port, p.param].join(".")}
            className="flex items-center justify-between gap-2 rounded-xl bg-s2 px-3 py-2 text-[12px] text-bad"
          >
            {p.message}
            {remove && (
              <button
                type="button"
                className="rounded-md px-1.5 py-0.5 text-t2 hover:bg-s3 hover:text-t1"
                aria-label={`Remove ${p.node}.${p.param}`}
                onClick={remove}
              >
                Remove
              </button>
            )}
          </li>
        );
      })}
    </ul>
  );
}

const freshSeed = () => Math.floor(Math.random() * 2 ** 32);

/** One param's editor, as Create edits it: number, seed with Random, the enum's values, a switch, a line or
 * text. A value that is not valid stays in the box with its error and is not set. Rows and images hold takes,
 * which a workflow does not: they are set in Studio. */
function ParamControl({
  field: f,
  error,
  onSet,
}: {
  field: Field;
  error?: string;
  onSet: (v: ParamValue) => void;
}) {
  const p = f.param;
  const label = p.label ?? p.name;
  const id = `gp-${f.key.replace(/\W/g, "-")}`;
  const [raw, setRaw] = useState(() => toRaw(f.initial, p));
  // what was typed and is not a value (shown instead of the graph's own problem with it)
  const [typed, setTyped] = useState<string>();
  const err = typed ?? error;
  // an undo or another view changed the value: the box follows unless it already reads as that value
  const initial = JSON.stringify(f.initial ?? -1);
  const [seen, setSeen] = useState(initial);
  if (seen !== initial) {
    setSeen(initial);
    const v = readValue(p, raw);
    if (!("value" in v && JSON.stringify(v.value ?? -1) === initial)) {
      setRaw(toRaw(f.initial, p));
      setTyped(undefined);
    }
  }
  const put = (r: string) => {
    setRaw(r);
    const v = readValue(p, r);
    if ("error" in v) setTyped(v.error);
    else {
      setTyped(undefined);
      onSet(v.value);
    }
  };
  const errLine = err && (
    <span id={id + "-err"} className="text-[12px] text-bad">
      {err}
    </span>
  );
  const box =
    "mono h-[34px] rounded-lg border border-line bg-s1 px-2.5 text-[13px] text-t1 placeholder:text-t2";
  if (p.type === "bool")
    return (
      <Switch
        f={{ ...f, param: { ...p, label } }}
        raw={raw}
        onChange={(_, r) => onSet(r === true)}
      />
    );
  if (p.type === "rows" || p.type === "image")
    return (
      <div className="flex items-center justify-between gap-3">
        <span>{label}</span>
        <span className="text-[12px] text-t2">{valueText(p, f.initial)} · set in Studio</span>
      </div>
    );
  // an int with `values` is a choice of the ones its node allows now (LTX's stage steps)
  const allowed = allowedInts(f, {});
  if (p.type === "enum" || allowed)
    return (
      <div role="group" aria-label={label} className="flex flex-col gap-1.5">
        <span className="flex items-center">
          {label}
          {p.help && <Info>{p.help}</Info>}
        </span>
        <div className="flex flex-wrap gap-1.5">
          {(allowed?.map(String) ?? p.enum)?.map((o) => (
            <button
              key={o}
              type="button"
              className="opt bevel min-w-16 px-3 text-[13px]"
              aria-pressed={raw === o}
              onClick={() => put(o)}
            >
              {pretty(o)}
            </button>
          ))}
        </div>
        {errLine}
      </div>
    );
  if (p.type === "text" || p.type === "string")
    return (
      <div className="flex flex-col gap-1.5">
        <label htmlFor={id} className="flex items-center">
          {label}
          {p.help && <Info>{p.help}</Info>}
        </label>
        {p.type === "text" ? (
          <PromptArea
            id={id}
            expand={label}
            rows={3}
            maxLength={p.max}
            value={String(raw)}
            aria-invalid={!!err || undefined}
            aria-describedby={err ? id + "-err" : undefined}
            onChange={put}
            wrapClassName="flex flex-col"
            className="resize-y rounded-lg border border-line bg-s1 px-2.5 py-2 text-[13px] text-t1"
          />
        ) : (
          <input
            id={id}
            maxLength={p.max}
            value={String(raw)}
            aria-invalid={!!err || undefined}
            aria-describedby={err ? id + "-err" : undefined}
            onChange={(e) => put(e.target.value)}
            className={box}
          />
        )}
        {errLine}
      </div>
    );
  return (
    <div className="flex flex-col gap-1">
      <div className="flex items-center justify-between gap-3">
        <label htmlFor={id} className="flex items-center">
          {label}
          {p.help && <Info>{p.help}</Info>}
        </label>
        <span className="flex items-center gap-1.5">
          <input
            id={id}
            type="number"
            inputMode={p.type === "float" ? "decimal" : "numeric"}
            min={p.type === "seed" ? 0 : p.min}
            max={p.max}
            step={p.step ?? (p.type === "float" ? "any" : undefined)}
            placeholder={p.type === "seed" ? "Random" : undefined}
            value={String(raw)}
            aria-invalid={!!err || undefined}
            aria-describedby={err ? id + "-err" : undefined}
            onChange={(e) => put(e.target.value)}
            className={`${box} ${p.type === "seed" ? "w-28" : "w-[72px]"}`}
          />
          {p.type === "seed" && (
            <button
              type="button"
              aria-pressed={raw === ""}
              aria-label="Random seed"
              title="Random seed"
              onClick={() => put(raw === "" ? String(freshSeed()) : "")}
              className={`opt bevel grid h-[34px] w-9 place-items-center ${raw === "" ? "text-a1" : ""}`}
            >
              <Dices size={14} strokeWidth={1.75} aria-hidden />
            </button>
          )}
        </span>
      </div>
      {errLine}
    </div>
  );
}
