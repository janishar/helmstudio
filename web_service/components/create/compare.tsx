"use client";
// The stage's Compare (an A/B wipe of two takes, a seed grid) and the Explain run drawer (plan 05-ui.md 8.2; the
// owner's Q23; the buttons are docs/mock/Main.dc.html's and TextToVideo.dc.html's).
import { useEffect, useRef, useState } from "react";
import { X } from "lucide-react";
import { Thumb } from "@/components/media";
import { usePlannedPeak } from "@/components/create/memory-chip";
import { useLibrary, useLooks } from "@/lib/library";
import { explain, seedGroup, type Origin } from "@/lib/explain";
import { safeUrl } from "@/lib/format";
import type { S } from "@/lib/api";
import { Range } from "@/components/range";

type Take = S<"Take">;

/** A take's name in Compare: its seed, else its title; `short` keeps a 16-digit random seed's ends (the strip's). */
function nameOf(t: Take, short = false) {
  const seed = t.made_with?.seed;
  if (seed == null) return t.title;
  const d = String(seed);
  return `seed ${short && d.length > 8 ? `${d.slice(0, 4)}…${d.slice(-3)}` : d}`;
}

/** A take's picture filling its box: an image, or its video playing muted in a loop (two play side by side). */
function Picture({ take, style }: { take: Take; style?: React.CSSProperties }) {
  const cls = "absolute inset-0 size-full object-cover";
  return take.kind === "video" ? (
    <video
      src={safeUrl(take.url)}
      poster={safeUrl(take.video?.poster_url)}
      aria-label={nameOf(take)}
      autoPlay
      muted
      loop
      playsInline
      className={cls}
      style={style}
    />
  ) : (
    <img src={safeUrl(take.url)} alt={nameOf(take)} className={cls} style={style} />
  );
}

/** Compare on the stage: A (the shown take) against B with a wipe, or the seed grid of A's prompt; a grid pick
 * becomes the shown take. `takes`: the session's image and video takes, newest first. */
export function Compare({
  a,
  takes,
  onPick,
}: {
  a: Take;
  takes: Take[];
  onPick: (id: string) => void;
}) {
  const others = takes.filter((t) => t.id !== a.id && t.kind === a.kind);
  const group = seedGroup(takes, a);
  const [mode, setMode] = useState<"ab" | "grid">("ab");
  const [bId, setB] = useState<string>("");
  const [at, setAt] = useState(50);
  // B: the one chosen, else the take made before A, else the newest other
  const b =
    others.find((t) => t.id === bId) ??
    others.find((t) => t.created_at <= a.created_at) ??
    others[0];
  const size = a.width && a.height ? { w: a.width, h: a.height } : { w: 16, h: 9 };
  return (
    <div className="flex size-full flex-col items-center gap-3">
      <div className="flex flex-wrap items-center gap-2 text-[13px]">
        <div
          role="group"
          aria-label="Compare"
          className="flex rounded-[10px] border border-s3 bg-s1 p-[3px]"
        >
          {(
            [
              ["ab", "A/B wipe"],
              ["grid", "Seed grid"],
            ] as const
          ).map(([m, label]) => (
            <button
              key={m}
              type="button"
              aria-pressed={mode === m}
              onClick={() => setMode(m)}
              className="h-8 rounded-lg px-3 font-medium text-t2 aria-pressed:bg-s5 aria-pressed:text-t1"
            >
              {label}
            </button>
          ))}
        </div>
        {mode === "ab" && b && (
          <label className="flex items-center gap-2 text-t2">
            B
            <select
              className="h-9 rounded-lg border border-s5 bg-s2 px-2 text-t1"
              value={b.id}
              onChange={(e) => setB(e.target.value)}
            >
              {others.map((t) => (
                <option key={t.id} value={t.id}>
                  {nameOf(t)}
                </option>
              ))}
            </select>
          </label>
        )}
      </div>
      {mode === "ab" ? (
        b ? (
          <div
            className="flex min-h-0 w-full grow flex-col items-center gap-2"
            style={{ containerType: "size" }}
          >
            <div
              className="relative overflow-hidden rounded-[10px] bg-black"
              style={{
                width: `min(100cqw, (100cqh - 40px) * ${size.w / size.h})`,
                aspectRatio: `${size.w} / ${size.h}`,
              }}
            >
              <Picture take={a} />
              <Picture take={b} style={{ clipPath: `inset(0 0 0 ${at}%)` }} />
              <span
                aria-hidden
                className="absolute inset-y-0 w-0.5 bg-white/80"
                style={{ left: `calc(${at}% - 1px)` }}
              />
              <span className="mono absolute top-2 left-2 rounded-lg bg-s2/90 px-2 py-1 text-[12px]">
                A · {nameOf(a, true)}
              </span>
              <span className="mono absolute top-2 right-2 rounded-lg bg-s2/90 px-2 py-1 text-[12px]">
                B · {nameOf(b, true)}
              </span>
            </div>
            <Range
              aria-label="Wipe"
              min={0}
              max={100}
              value={at}
              onChange={(e) => setAt(Number(e.target.value))}
              className="w-full max-w-[480px] accent-[var(--a1)]"
            />
          </div>
        ) : (
          <p className="m-auto text-t2">Make another take to compare with this one.</p>
        )
      ) : (
        <div
          role="group"
          aria-label="Seed grid"
          className="grid w-full grow auto-rows-min gap-2.5 overflow-y-auto"
          style={{ gridTemplateColumns: "repeat(auto-fill, minmax(160px, 1fr))" }}
        >
          {group.length < 2 && (
            <p className="col-span-full m-0 text-[13px] text-t2">
              Only one take of this prompt yet: raise the seed count to make a grid.
            </p>
          )}
          {group.map((t) => (
            <button
              key={t.id}
              type="button"
              aria-label={`Show ${nameOf(t)}`}
              aria-current={t.id === a.id || undefined}
              onClick={() => onPick(t.id)}
              className="flex flex-col gap-1.5 rounded-[10px] border border-s3 bg-s1 p-1.5 text-left aria-[current]:border-a1"
            >
              <Thumb
                take={t}
                className="block w-full overflow-hidden rounded-lg"
                imgClassName="aspect-square w-full object-cover"
              />
              <span className="mono px-1 text-[12px] text-t2">{nameOf(t)}</span>
            </button>
          ))}
        </div>
      )}
    </div>
  );
}

const ORIGIN: Record<Origin, string> = {
  default: "default",
  you: "you set it",
  preset: "preset",
  random: "random",
  model: "chosen by the model",
};

/** The Explain run drawer: the resolved run (settings after defaults and where each came from, the preset, the
 * prompt as typed and as read, what each element, look and LoRA added, the model, the planned peak) and "copy as
 * `helm run`". */
export function ExplainDrawer({
  workflow,
  take,
  run,
  onClose,
}: {
  workflow: S<"Workflow">;
  take?: Take;
  run?: S<"Run">;
  onClose: () => void;
}) {
  const ref = useRef<HTMLDialogElement>(null);
  const library = useLibrary();
  const looks = useLooks();
  const x = explain(workflow, take, run, library, looks);
  const peak = usePlannedPeak(
    run
      ? {
          workflow_id: run.workflow_id,
          inputs: run.inputs,
          ...(run.looks?.length && { looks: run.looks }),
        }
      : null,
  );
  const [copied, setCopied] = useState(false);
  useEffect(() => {
    const d = ref.current;
    if (d && !d.open) d.showModal();
  }, []);
  const facts: [string, string][] = [
    ["Workflow", take?.made_with?.workflow_title ?? workflow.title],
    ["Model", x.model],
    ["Seed", x.seed == null ? "" : String(x.seed)],
    ["Preset", x.preset],
    ["Host", x.host],
    ["Planned peak", peak != null ? `≈ ${Math.round(peak * 10) / 10} GB` : ""],
  ];
  return (
    <dialog
      ref={ref}
      onClose={onClose}
      aria-label="Explain run"
      className="pop fixed inset-y-0 right-0 left-auto m-0 h-full max-h-none w-[min(92vw,440px)] overflow-y-auto rounded-none rounded-l-2xl p-5 text-[13px] text-t1 backdrop:bg-black/40"
    >
      <div className="mb-4 flex items-center justify-between">
        <h2 className="m-0 font-display text-base font-bold">Explain run</h2>
        <button
          type="button"
          aria-label="Close"
          onClick={() => ref.current?.close()}
          className="grid size-8 place-items-center rounded-lg hover:bg-s3"
        >
          <X size={16} aria-hidden />
        </button>
      </div>
      <dl className="m-0 grid grid-cols-[auto_1fr] gap-x-4 gap-y-1.5">
        {facts
          .filter(([, v]) => v)
          .map(([k, v]) => (
            <div key={k} className="contents">
              <dt className="text-t2">{k}</dt>
              <dd className="mono m-0 break-words">{v}</dd>
            </div>
          ))}
      </dl>

      {x.prompt && (
        <Section title="Prompt">
          <p className="m-0 whitespace-pre-wrap">{x.prompt.typed || "(empty)"}</p>
          {x.prompt.sent !== x.prompt.typed && (
            <>
              <p className="m-0 mt-2 text-t2">
                What the model read, with elements and looks added:
              </p>
              <p className="m-0 whitespace-pre-wrap text-[var(--t1b)]">{x.prompt.sent}</p>
            </>
          )}
        </Section>
      )}

      {!!x.elements.length && (
        <Section title="Elements">
          {x.elements.map((e) => (
            <Line
              key={e.handle}
              name={`@${e.handle} · ${e.name}`}
              tag={`version ${e.version}${e.changed ? ", edited since" : ""}`}
              note={e.text && `adds “${e.text}”`}
            />
          ))}
        </Section>
      )}

      {!!x.looks.length && (
        <Section title="Looks">
          {x.looks.map((l) => (
            <Line
              key={l.name}
              name={l.name}
              tag={`version ${l.version}${l.changed ? ", edited since" : ""}`}
              note={[l.text && `adds “${l.text}”`, l.loras.length && `LoRA ${l.loras.join(", ")}`]
                .filter(Boolean)
                .join(" · ")}
            />
          ))}
        </Section>
      )}

      {!!x.loras.length && (
        <Section title="LoRAs">
          {x.loras.map((l, i) => (
            <Line
              key={i}
              name={l.name}
              tag={`${l.strength.toFixed(2)} · ${l.mode}`}
              note={l.from ? `from the look ${l.from}` : "from the LoRA stack"}
            />
          ))}
        </Section>
      )}

      {x.nodes.map((n) => (
        <Section key={n.title} title={n.title}>
          {n.settings.map((s) => (
            <Line
              key={s.key}
              name={s.label}
              value={s.value}
              tag={s.origin === "preset" ? `preset ${x.preset}` : ORIGIN[s.origin]}
              note={s.help}
            />
          ))}
        </Section>
      ))}

      <Section title="Copy as helm run">
        <pre className="mono m-0 rounded-lg bg-s1 p-2.5 text-[12px] break-all whitespace-pre-wrap">
          {x.command}
        </pre>
        <button
          type="button"
          className="chip bevel mt-2 h-8"
          onClick={() =>
            void navigator.clipboard?.writeText(x.command).then(
              () => setCopied(true),
              () => undefined,
            )
          }
        >
          {copied ? "Copied" : "Copy"}
        </button>
      </Section>
    </dialog>
  );
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section aria-label={title} className="mt-5">
      <h3 className="heading m-0 mb-2">{title}</h3>
      <div className="flex flex-col gap-2">{children}</div>
    </section>
  );
}

function Line({
  name,
  value,
  tag,
  note,
}: {
  name: string;
  value?: string;
  tag: string;
  note?: string;
}) {
  return (
    <div>
      <div className="flex items-baseline gap-2">
        <span className="min-w-0 truncate">{name}</span>
        {value != null && <span className="mono">{value}</span>}
        <span className="grow" />
        <span className="mono shrink-0 text-[12px] text-t2">{tag}</span>
      </div>
      {note && <p className="m-0 text-[12px] text-t2">{note}</p>}
    </div>
  );
}
