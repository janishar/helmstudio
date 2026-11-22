"use client";
// The gallery picker (plan 9.1: the one way to choose media): the Gallery's grid, search and filters in a dialog,
// images only (or every kind: `kind` null), picking up to `max` takes. A media input's picker (API 1.19,
// docs/mock/VideoSource.dc.html) narrows it further (`only`: audio takes and videos with sound), says each tile's
// facts and may upload wav, mp3 or m4a (a wav becomes m4a: lib/optimise.ts).
import { useRef, useState } from "react";
import { X } from "lucide-react";
import { ErrorNote, Modal, UploadStatus } from "@/components/kit";
import { Chip, TakeCard, useTakePages } from "@/components/gallery/gallery";
import { message, type S } from "@/lib/api";
import {
  AUDIO_ACCEPT,
  MAX_AUDIO_S,
  UPLOAD_START,
  uploadFile,
  type Filter,
  type UploadProgress,
} from "@/lib/takes";

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
  kind = "image",
  title = "Add from gallery",
  only,
  sub,
  audioUpload,
  help,
  frames,
}: {
  open: boolean;
  onClose: () => void;
  max: number;
  onPick: (takes: Take[]) => void;
  /** the kind offered; null: images and videos */
  kind?: S<"TakeKind"> | null;
  title?: string;
  /** the takes shown (of those the kind lets through) */
  only?: (t: Take) => boolean;
  /** a tile's facts line ("448 × 256 · 49 f · 2.04 s"), else the gallery's */
  sub?: (t: Take) => string;
  /** offers "Upload wav, mp3 or m4a" (an audio input) */
  audioUpload?: boolean;
  /** a line under the footer (docs/mock/GalleryPicker: what the picks join, the limit) */
  help?: string;
  /** API 1.37 (docs/mock/H3Options.dc.html frame 8): tabs Images | Video; a video picked offers Use first frame and
   * Use last frame, which make the frame (an image take) */
  frames?: (t: Take, at: "first" | "last") => Promise<void>;
}) {
  return (
    <Modal open={open} onClose={onClose} title={title} width="w-[min(94vw,880px)]">
      <Picker
        max={max}
        onClose={onClose}
        onPick={onPick}
        kind={kind ?? undefined}
        only={only}
        sub={sub}
        audioUpload={audioUpload}
        help={help}
        frames={frames}
      />
    </Modal>
  );
}

/** "Upload wav, mp3 or m4a": the length is read here, a wav becomes m4a (docs/mock/UploadOptimise.dc.html frame 2:
 * the pending card, × cancels), the take picked once it is stored. */
function AudioUpload({ onDone }: { onDone: (t: Take) => void }) {
  const input = useRef<HTMLInputElement>(null);
  const [state, setState] = useState<{
    name?: string;
    p?: UploadProgress;
    stop?: AbortController;
    error?: string;
  }>({});
  return (
    <div className="flex flex-wrap items-center gap-2">
      <input
        ref={input}
        type="file"
        accept={AUDIO_ACCEPT}
        hidden
        onChange={(e) => {
          const f = e.target.files?.[0];
          e.target.value = "";
          if (!f) return;
          const stop = new AbortController();
          // a cancelled upload's late reports are dropped
          const mine = (next: (s: typeof state) => typeof state) =>
            setState((s) => (s.stop === stop ? next(s) : s));
          setState({ name: f.name, p: UPLOAD_START, stop });
          uploadFile(f, (p) => mine((s) => ({ ...s, p })), undefined, {
            audio: true,
            signal: stop.signal,
          })
            .then((t) => {
              mine(() => ({}));
              onDone(t);
            })
            .catch((err) => mine(() => ({ error: message(err) })));
        }}
      />
      <button
        type="button"
        className="h-10 rounded-[10px] border border-dashed border-dash bg-transparent px-3.5 text-[13px]"
        disabled={!!state.p}
        onClick={() => input.current?.click()}
      >
        Upload wav, mp3 or m4a
      </button>
      <span className="mono text-[12px] text-t2">
        Up to {MAX_AUDIO_S} s; a wav uploads as m4a, the length is read in the browser
      </span>
      {state.p && state.name && (
        <div className="flex w-full flex-col gap-1.5 rounded-[10px] border border-line bg-s1 p-2.5 text-[12px] text-t2">
          <div className="flex items-center gap-2">
            <b className="min-w-0 flex-1 truncate text-[13px] text-t1">{state.name}</b>
            <button
              type="button"
              aria-label="Cancel the upload"
              className="grid size-6 shrink-0 place-items-center rounded-full bg-s3 text-t1"
              onClick={() => {
                state.stop?.abort();
                setState({});
              }}
            >
              <X size={12} aria-hidden />
            </button>
          </div>
          <UploadStatus p={state.p} name={state.name} />
        </div>
      )}
      {state.error && <p className="mono m-0 w-full text-[12px] text-bad">{state.error}</p>}
    </div>
  );
}

/** The dialog's content, mounted while it is open (so each opening starts with nothing picked). */
function Picker({
  max,
  onClose,
  onPick,
  kind,
  only,
  sub,
  audioUpload,
  help,
  frames,
}: {
  max: number;
  onClose: () => void;
  onPick: (takes: Take[]) => void;
  kind?: S<"TakeKind">;
  only?: (t: Take) => boolean;
  sub?: (t: Take) => string;
  audioUpload?: boolean;
  help?: string;
  frames?: (t: Take, at: "first" | "last") => Promise<void>;
}) {
  const [filter, setFilter] = useState<Filter>({ kind });
  const [frameError, setFrameError] = useState<string | null>(null);
  const [making, setMaking] = useState(false);
  const video = !!frames && filter.kind === "video";
  const [q, setQ] = useState("");
  const pages = useTakePages(filter, q, undefined, 40);
  const { next, loading, error, fetchPage, sentinel } = pages;
  // ponytail: an audio input filters a page of every kind here; a kind list in the API if galleries grow
  const takes = only ? (pages.takes?.filter(only) ?? null) : pages.takes;
  const [picked, setPicked] = useState<Take[]>([]);
  const toggle = (t: Take) =>
    setPicked((p) =>
      p.some((x) => x.id === t.id) ? p.filter((x) => x.id !== t.id) : [...p, t].slice(0, max),
    );
  return (
    <div className="flex max-h-[76dvh] flex-col gap-3">
      {frames && (
        <div role="group" aria-label="Kind" className="seg self-start">
          {(["image", "video"] as const).map((k) => (
            <button
              key={k}
              type="button"
              aria-pressed={filter.kind === k}
              onClick={() => {
                setPicked([]);
                setFrameError(null);
                setFilter((f) => ({ ...f, kind: k }));
              }}
            >
              {k === "image" ? "Images" : "Video"}
            </button>
          ))}
        </div>
      )}
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
          aria-label={kind === "image" ? "Images" : "Takes"}
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
                  sub={sub?.(t)}
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
              ? `No ${kind === "image" ? "images" : "takes"} match.`
              : `No ${kind === "image" ? "images" : "takes"} yet: upload some, or make some in Create.`}
          </p>
        )}
        <div ref={sentinel} aria-hidden className="h-px" />
      </div>
      {audioUpload && (
        <AudioUpload
          onDone={(t) => {
            onPick([t]);
            onClose();
          }}
        />
      )}
      <div className="flex flex-wrap items-center justify-end gap-2">
        <span className="mono mr-auto text-[12px] text-t2" aria-live="polite">
          {picked.length} picked · up to {max}
        </span>
        <button type="button" className="chip bevel" onClick={onClose}>
          Cancel
        </button>
        {video ? (
          (["first", "last"] as const).map((at) => (
            <button
              key={at}
              type="button"
              className={`chip bevel ${at === "last" ? "bg-a1 font-semibold text-on1" : ""}`}
              disabled={!picked.length || making}
              onClick={() => {
                setMaking(true);
                setFrameError(null);
                frames(picked[0], at)
                  .then(onClose)
                  .catch((e) => setFrameError(message(e)))
                  .finally(() => setMaking(false));
              }}
            >
              Use {at} frame
            </button>
          ))
        ) : (
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
        )}
      </div>
      {frameError && (
        <p role="alert" className="mono m-0 text-[12px] text-bad">
          {frameError}
        </p>
      )}
      {help && <p className="m-0 text-[12px] text-t2">{help}</p>}
    </div>
  );
}
