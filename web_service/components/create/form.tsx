"use client";
// Form controls rendered from node schemas: text params go in the composer, the rest in the inspector.
import { Info } from "@/components/kit";
import { toRaw, type Field, type Raw } from "@/lib/workflow";

/** Props shared by the form parts: the fields, what the controls hold, and the errors by field key. */
export type FormProps = {
  fields: Field[];
  raws: Record<string, Raw>;
  errors: Record<string, string>;
  onChange: (key: string, raw: Raw) => void;
};

/** The control's id for a field key. */
const idOf = (key: string) => "f-" + key.replace(/[^a-z0-9_]/gi, "-");
/** What a control holds now. */
const rawOf = (f: Field, raws: Record<string, Raw>) => raws[f.key] ?? toRaw(f.initial);

/** The field's error line, tied to its control by id. */
function FieldError({ k, errors }: { k: string; errors: Record<string, string> }) {
  return errors[k] ? (
    <span id={idOf(k) + "-err"} className="text-[12px] text-bad">
      {errors[k]}
    </span>
  ) : null;
}

/** The multi-line text params (the prompt), borderless inside the composer; ⌘/Ctrl+Enter submits the form. */
export function TextFields({ fields, raws, errors, onChange }: FormProps) {
  return fields
    .filter((f) => f.param.type === "text")
    .map((f) => (
      <div key={f.key} className="flex flex-col gap-1">
        <label htmlFor={idOf(f.key)} className="sr-only">
          {f.param.label ?? f.param.name}
        </label>
        <textarea
          id={idOf(f.key)}
          rows={2}
          maxLength={f.param.max}
          placeholder={f.param.label ?? f.param.name}
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

/** The other params, one section per node, each a label-over-value tile. */
export function Inspector({ fields, raws, errors, onChange }: FormProps) {
  const rest = fields.filter((f) => f.param.type !== "text");
  const nodes = [...new Set(rest.map((f) => f.node))];
  if (!nodes.length) return <p className="text-[13px] text-t2">Nothing to set.</p>;
  return nodes.map((node) => {
    const fs = rest.filter((f) => f.node === node);
    return (
      <section key={node} aria-labelledby={`n-${node}`} className="flex flex-col gap-2.5">
        <h2 id={`n-${node}`} className="heading flex items-center">
          {fs[0].nodeTitle}
          {fs[0].nodeHelp && <Info>{fs[0].nodeHelp}</Info>}
        </h2>
        <div className="grid grid-cols-2 gap-2">
          {fs.map((f) => (
            <Control key={f.key} f={f} raw={rawOf(f, raws)} errors={errors} onChange={onChange} />
          ))}
        </div>
      </section>
    );
  });
}

/** One param tile: a number, a switch, a select or a line of text. */
function Control({
  f,
  raw,
  errors,
  onChange,
}: {
  f: Field;
  raw: Raw;
  errors: Record<string, string>;
  onChange: (key: string, raw: Raw) => void;
}) {
  const p = f.param;
  const id = idOf(f.key);
  const label = p.label ?? p.name;
  const error = errors[f.key];
  const described = error ? id + "-err" : undefined;
  const control =
    p.type === "bool" ? (
      <button
        id={id}
        type="button"
        role="switch"
        aria-checked={raw === true}
        aria-labelledby={id + "-l"}
        onClick={() => onChange(f.key, raw !== true)}
        className="flex h-[26px] w-11 shrink-0 rounded-full border border-line p-0.5"
        style={{ background: raw === true ? "var(--a1)" : "var(--s1)" }}
      >
        <span
          className="block size-5 rounded-full transition-transform"
          style={{
            background: raw === true ? "var(--on1)" : "var(--t2)",
            transform: raw === true ? "translateX(18px)" : undefined,
          }}
        />
      </button>
    ) : p.type === "enum" ? (
      <select
        id={id}
        className="in h-8 bg-s4 px-2"
        value={String(raw)}
        onChange={(e) => onChange(f.key, e.target.value)}
        aria-describedby={described}
      >
        {p.enum?.map((o) => (
          <option key={o}>{o}</option>
        ))}
      </select>
    ) : (
      <input
        id={id}
        className="mono h-8 w-full rounded-lg border-0 bg-s4 px-2 text-[13px] font-semibold text-t1"
        type={p.type === "string" ? "text" : "number"}
        inputMode={p.type === "int" ? "numeric" : p.type === "float" ? "decimal" : undefined}
        min={p.type === "string" ? undefined : p.min}
        max={p.type === "string" ? undefined : p.max}
        maxLength={p.type === "string" ? p.max : undefined}
        step={p.type === "float" ? "any" : undefined}
        value={String(raw)}
        aria-invalid={!!error || undefined}
        aria-describedby={described}
        onChange={(e) => onChange(f.key, e.target.value)}
      />
    );
  return (
    <div
      className={`flex flex-col gap-1.5 bg-s2 px-3 py-2.5 ${p.type === "bool" ? "justify-between" : ""}`}
      style={{ borderRadius: "18px 18px 18px 8px" }}
    >
      <span className="flex items-center text-[12px] text-t2">
        {p.type === "bool" ? (
          <span id={id + "-l"}>{label}</span>
        ) : (
          <label htmlFor={id}>{label}</label>
        )}
        {p.help && <Info>{p.help}</Info>}
      </span>
      {control}
      <FieldError k={f.key} errors={errors} />
    </div>
  );
}
