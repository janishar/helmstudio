"use client";
// The review grid (docs/mock/Pipeline.dc.html's pick, plan 16.2 Gate): every row parked on a gate with its
// candidates; pick 1 to keep of them, or none (try again, move on or fail, as the gate's branch says), per row and in
// any order. Keyboard: arrows move between candidates, Space picks, Enter sends the pick, R rejects all, F opens the
// focused take full screen. Live from the batch's events. The mock's step strip, Add step, the motion prompt and the
// memory and step-settings panel belong to the pipeline's Studio view of a later milestone and are left out.
import Link from "next/link";
import { useCallback, useEffect, useRef, useState } from "react";
import { ChevronLeft } from "lucide-react";
import { ErrorNote, Info } from "@/components/kit";
import { FullScreen } from "@/components/media";
import { StateChip, followBatch } from "@/components/pipelines/pipelines";
import { api, message, ok, okOr, type S } from "@/lib/api";
import { inputSummary } from "@/lib/batches";
import { safeUrl } from "@/lib/format";
import { gridMove, rejectLabel, togglePick } from "@/lib/pipeline-run";

type Item = S<"BatchReviewItem">;

export function Review({
  id,
  initial,
}: {
  id: string;
  initial?: { batch: S<"Batch">; review: S<"BatchReview"> };
}) {
  const [batch, setBatch] = useState(initial?.batch ?? null);
  const [items, setItems] = useState<Item[] | null>(initial?.review.items ?? null);
  const [error, setError] = useState("");
  const [full, setFull] = useState<{ item: Item; i: number } | null>(null);

  const load = useCallback(async () => {
    try {
      const path = { params: { path: { batch_id: id } } };
      const [b, r] = await Promise.all([
        ok(api.GET("/v1/studio/batches/{batch_id}", path)),
        ok(api.GET("/v1/studio/batches/{batch_id}/review", path)),
      ]);
      setBatch(b);
      setItems(r.items);
      setError("");
    } catch (e) {
      setError(message(e));
    }
  }, [id]);
  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- only when the server's read failed
    if (!initial) void load();
  }, [initial, load]);

  // live: a row leaving its gate drops out; a row newly parked (or parked again) reloads the grid
  const itemsRef = useRef(items);
  useEffect(() => {
    itemsRef.current = items;
  });
  const reload = useRef<ReturnType<typeof setTimeout>>(undefined);
  useEffect(
    () =>
      followBatch(id, ["batch", "row"], (e) => {
        if (e.type === "batch") return setBatch(e.data);
        const r = e.data;
        const have = itemsRef.current?.find((x) => x.row === r.row);
        if (r.status !== "waiting") {
          if (have) setItems((l) => l?.filter((x) => x.row !== r.row) ?? l);
        } else if (!have || have.visit !== r.parked?.visit || have.attempt !== r.attempt) {
          clearTimeout(reload.current);
          reload.current = setTimeout(() => void load(), 300);
        }
      }),
    [id, load],
  );

  const done = (row: number) => {
    // keyboard review goes on: focus moves to the next waiting row's first take
    const next =
      items?.find((x) => x.row !== row && x.row > row) ?? items?.find((x) => x.row !== row);
    setItems((l) => l?.filter((x) => x.row !== row) ?? l);
    if (next)
      requestAnimationFrame(() =>
        document.querySelector<HTMLElement>(`#row-${next.row} [role=group] button`)?.focus(),
      );
  };
  const summarise = inputSummary(
    (items ?? []).map((x) => ({ inputs: x.inputs }) as unknown as S<"BatchRow">),
  );

  return (
    <main id="main" className="flex min-w-0 flex-1 flex-col gap-3.5 px-5 pt-4 pb-5">
      <div className="flex flex-wrap items-center gap-3">
        <Link
          href={`/pipelines/batches/${id}`}
          aria-label="Back to the batch"
          className="grid size-9 place-items-center rounded-[10px] text-t2 hover:bg-s2"
        >
          <ChevronLeft size={18} aria-hidden />
        </Link>
        <h1 className="m-0 flex items-center font-display text-[18px] font-bold tracking-[0.02em]">
          {batch?.title ?? "Review"}
          <Info>
            Parked rows hold nothing in the queue: pick in any order. Arrows move, Space picks,
            Enter sends, R rejects all, F shows a take full screen.
          </Info>
        </h1>
        {items && (
          <span className="rounded-full bg-s2 px-2.5 py-1 text-[12px] font-medium text-t2">
            {items.length} row{items.length === 1 ? "" : "s"} waiting for a pick
          </span>
        )}
        <div className="grow" />
        {batch && <StateChip batch={batch} />}
      </div>
      {error && <ErrorNote onRetry={load}>{error}</ErrorNote>}
      {items?.length === 0 && (
        <p className="text-t2">
          No row is waiting for a pick.{" "}
          <Link href={`/pipelines/batches/${id}`} className="underline">
            Back to the batch
          </Link>
        </p>
      )}
      {items?.map((it) => (
        <ReviewRow
          key={`${it.row}:${it.attempt}:${it.gate}:${it.visit}`}
          batchId={id}
          item={it}
          inputs={summarise({ inputs: it.inputs } as unknown as S<"BatchRow">)}
          onDone={() => done(it.row)}
          onError={setError}
          onFull={(i) => setFull({ item: it, i })}
        />
      ))}
      {full && (
        <FullScreen
          take={
            {
              id: full.item.candidates[full.i].take_id,
              kind: "image",
              title: `Row ${full.item.row}${full.item.candidates[full.i].seed != null ? ` · seed ${full.item.candidates[full.i].seed}` : ""}`,
              url: full.item.candidates[full.i].url,
            } as S<"Take">
          }
          onClose={() => setFull(null)}
          onStep={(d) =>
            setFull(
              (f) =>
                f && {
                  ...f,
                  i: (f.i + d + f.item.candidates.length) % f.item.candidates.length,
                },
            )
          }
        />
      )}
    </main>
  );
}

function ReviewRow({
  batchId,
  item,
  inputs,
  onDone,
  onError,
  onFull,
}: {
  batchId: string;
  item: Item;
  inputs: string;
  onDone: () => void;
  onError: (m: string) => void;
  onFull: (i: number) => void;
}) {
  const [picked, setPicked] = useState<string[]>([]);
  const [focus, setFocus] = useState(0);
  const [busy, setBusy] = useState(false);
  const refs = useRef<(HTMLButtonElement | null)[]>([]);
  const n = item.candidates.length;

  async function send(body: S<"BatchPick">) {
    if (busy) return;
    setBusy(true);
    try {
      // 409: the row moved on meanwhile (another tab picked, or it was stopped); it leaves the grid either way
      await okOr(
        api.POST("/v1/studio/batches/{batch_id}/rows/{row}/pick", {
          params: { path: { batch_id: batchId, row: String(item.row) } },
          body,
        }),
        409,
      );
      onDone();
    } catch (e) {
      onError(message(e));
      setBusy(false);
    }
  }
  const pick = () => picked.length && void send({ gate: item.gate, take_ids: picked });
  const reject = () => void send({ gate: item.gate, reject: true });

  return (
    <section
      id={`row-${item.row}`}
      aria-labelledby={`rv-${item.row}`}
      className="panel flex flex-col gap-3.5 p-5"
      style={{ background: "var(--in)" }}
    >
      <div className="flex flex-wrap items-baseline justify-between gap-2">
        <h2 id={`rv-${item.row}`} className="m-0 font-display text-[20px] font-bold">
          Row {item.row} ·{" "}
          {item.gate_title ?? `Pick ${item.keep === 1 ? "one" : `up to ${item.keep}`}`}
        </h2>
        <span className="max-w-[60%] truncate text-[12px] text-t2" title={inputs}>
          {inputs}
          {inputs && " · "}
          {item.keep === 1 ? "pick 1" : `pick 1 to ${item.keep}`} of {n}
          {item.visit > 1 && ` · try ${item.visit}`}
        </span>
      </div>
      <div
        role="group"
        aria-label={`Row ${item.row} candidates`}
        className="grid grid-cols-2 gap-3 lg:grid-cols-4"
        onKeyDown={(e) => {
          const k = e.key;
          if (["ArrowLeft", "ArrowRight", "ArrowUp", "ArrowDown", "Home", "End"].includes(k)) {
            e.preventDefault();
            // the columns as drawn (2 below lg, 4 from lg)
            const cols = getComputedStyle(e.currentTarget).gridTemplateColumns.split(" ").length;
            const to = gridMove(focus, k, n, cols);
            setFocus(to);
            refs.current[to]?.focus();
          } else if (k === "Enter") {
            e.preventDefault();
            pick();
          } else if (k.toLowerCase() === "r" && !e.metaKey && !e.ctrlKey) {
            e.preventDefault();
            reject();
          } else if (k.toLowerCase() === "f" && !e.metaKey && !e.ctrlKey) {
            e.preventDefault();
            onFull(focus);
          }
        }}
      >
        {item.candidates.map((c, i) => {
          const on = picked.includes(c.take_id);
          return (
            <button
              key={c.take_id}
              ref={(el) => {
                refs.current[i] = el;
              }}
              type="button"
              tabIndex={i === focus ? 0 : -1}
              aria-pressed={on}
              aria-label={`Take ${i + 1}${c.seed != null ? `, seed ${c.seed}` : ""}${on ? ", picked" : ""}`}
              className={`flex min-h-[150px] flex-col overflow-hidden rounded-[10px] p-0 text-[12px] ${on ? "border-2 border-a2 bg-s1 text-t1" : "border border-dashed border-dash bg-panel text-t2"}`}
              onFocus={() => setFocus(i)}
              onClick={() => setPicked((p) => togglePick(p, c.take_id, item.keep))}
              onKeyDown={(e) => {
                // Enter sends the pick (the grid handles it): no click, so it does not toggle too
                if (e.key === "Enter") e.preventDefault();
              }}
              onDoubleClick={() => onFull(i)}
            >
              <img
                src={safeUrl(c.thumb_url)}
                alt=""
                className="min-h-0 w-full flex-1 object-contain"
                style={{
                  background: "var(--in)",
                  aspectRatio: c.width && c.height ? `${c.width} / ${c.height}` : "1",
                }}
              />
              <span className="mono px-2 py-1.5">
                take{c.seed != null && ` · seed ${c.seed}`}
                {on && " · picked"}
              </span>
            </button>
          );
        })}
      </div>
      <div className="flex flex-wrap items-center gap-2">
        <span className="rounded-lg bg-s5 px-2.5 py-1 font-mono text-[12px]">
          {picked.length} of {item.keep} picked
        </span>
        <div className="grow" />
        <button type="button" className="chip bevel" disabled={busy} title="R" onClick={reject}>
          {rejectLabel(item)}
        </button>
        <button
          type="button"
          className="key h-11 px-6 text-[14px]"
          style={{ background: "var(--a2)", color: "var(--on2)" }}
          disabled={busy || !picked.length}
          title="Enter"
          onClick={pick}
        >
          Pick {picked.length > 1 ? picked.length : ""}
        </button>
      </div>
    </section>
  );
}
