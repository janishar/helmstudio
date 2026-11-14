"use client";
// The gallery picker (plan 9.1: the one way to choose media): the Gallery's grid, search and filters in a dialog,
// images only, picking up to `max` takes.
import { useState } from "react";
import { ErrorNote, Modal } from "@/components/kit";
import { Chip, TakeCard, useTakePages } from "@/components/gallery/gallery";
import type { S } from "@/lib/api";
import type { Filter } from "@/lib/takes";

type Take = S<"Take">;

const SOURCES: [S<"TakeSource">, string][] = [
  ["create", "From Create"],
  ["pipeline", "From Pipelines"],
  ["upload", "Uploads"],
];

/** The picker dialog; `onPick` gets the takes in the order they were picked. */
export function GalleryPicker({
  open,
  onClose,
  max,
  onPick,
}: {
  open: boolean;
  onClose: () => void;
  max: number;
  onPick: (takes: Take[]) => void;
}) {
  return (
    <Modal open={open} onClose={onClose} title="Add from gallery" width="w-[min(94vw,880px)]">
      <Picker max={max} onClose={onClose} onPick={onPick} />
    </Modal>
  );
}

/** The dialog's content, mounted while it is open (so each opening starts with nothing picked). */
function Picker({
  max,
  onClose,
  onPick,
}: {
  max: number;
  onClose: () => void;
  onPick: (takes: Take[]) => void;
}) {
  const [filter, setFilter] = useState<Filter>({ kind: "image" });
  const [q, setQ] = useState("");
  const { takes, next, loading, error, fetchPage, sentinel } = useTakePages(
    filter,
    q,
    undefined,
    40,
  );
  const [picked, setPicked] = useState<Take[]>([]);
  const toggle = (t: Take) =>
    setPicked((p) =>
      p.some((x) => x.id === t.id) ? p.filter((x) => x.id !== t.id) : [...p, t].slice(0, max),
    );
  return (
    <div className="flex max-h-[76dvh] flex-col gap-3">
      <div className="flex flex-wrap items-center gap-2">
        <label htmlFor="pick-q" className="sr-only">
          Search prompts
        </label>
        <input
          id="pick-q"
          type="search"
          placeholder="Search prompts and titles"
          value={q}
          maxLength={200}
          autoFocus
          onChange={(e) => setQ(e.target.value)}
          // the picker sits inside the composer's form: Enter must not submit it
          onKeyDown={(e) => e.key === "Enter" && e.preventDefault()}
          className="h-9 flex-[1_1_220px] rounded-xl border border-s5 bg-s1 px-3 text-t1"
        />
        <div role="group" aria-label="Filters" className="flex flex-wrap gap-1.5">
          <Chip
            pressed={!!filter.starred}
            onClick={() => setFilter((f) => ({ ...f, starred: !f.starred }))}
          >
            Starred
          </Chip>
          {SOURCES.map(([s, label]) => (
            <Chip
              key={s}
              pressed={filter.source === s}
              onClick={() => setFilter((f) => ({ ...f, source: f.source === s ? undefined : s }))}
            >
              {label}
            </Chip>
          ))}
        </div>
      </div>
      {error && (
        <ErrorNote onRetry={() => fetchPage(takes?.length ? next : null)}>{error}</ErrorNote>
      )}
      <div className="min-h-40 overflow-y-auto p-0.5">
        <div
          role="list"
          aria-label="Images"
          aria-busy={loading}
          className="grid grid-cols-[repeat(auto-fill,minmax(130px,1fr))] gap-2.5"
        >
          {takes?.map((t) => {
            const on = picked.some((x) => x.id === t.id);
            return (
              <div key={t.id} role="listitem" className="min-w-0">
                <TakeCard
                  take={t}
                  on={on}
                  selecting
                  disabled={!on && picked.length >= max}
                  onClick={() => toggle(t)}
                />
              </div>
            );
          })}
        </div>
        {takes === null && !error && (
          <p role="status" className="text-t2">
            Loading…
          </p>
        )}
        {takes?.length === 0 && !error && (
          <p className="py-8 text-center text-t2">
            {q.trim() || filter.starred || filter.source
              ? "No images match."
              : "No images yet: upload some, or make some in Create."}
          </p>
        )}
        <div ref={sentinel} aria-hidden className="h-px" />
      </div>
      <div className="flex flex-wrap items-center justify-end gap-2">
        <span className="mono mr-auto text-[12px] text-t2" aria-live="polite">
          {picked.length} picked · up to {max}
        </span>
        <button type="button" className="chip bevel" onClick={onClose}>
          Cancel
        </button>
        <button
          type="button"
          className="chip bevel bg-a1 font-semibold text-on1"
          disabled={!picked.length}
          onClick={() => {
            onPick(picked);
            onClose();
          }}
        >
          Add {picked.length || ""}
        </button>
      </div>
    </div>
  );
}
