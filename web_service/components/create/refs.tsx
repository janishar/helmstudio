"use client";
// The rows editor for image references (plan 8.4): an ordered list of takes, added from the gallery picker or by
// uploading (picker, drop, paste: the file becomes a gallery take first), removed, and reordered by drag or
// Alt + arrow keys. Each item shows how the prompt names it (the param's `mention`, "<image1>").
import { useEffect, useRef, useState } from "react";
import {
  AudioLines,
  Film,
  Image as ImageIcon,
  ImageOff,
  ImagePlus,
  Loader2,
  Upload,
  X,
} from "lucide-react";
import { Info, UploadStatus } from "@/components/kit";
import { GalleryPicker } from "@/components/gallery/picker";
import { frameTake } from "@/components/create/frame-in";
import { message, type S } from "@/lib/api";
import {
  addIds,
  capacity,
  carriesImage,
  dropped,
  inOrder,
  mentionOf,
  move,
  refIds,
  withIds,
} from "@/lib/refs";
import { remember, useTakes } from "@/lib/take-cache";
import { ClipTrack, ItemFields } from "@/components/create/rows";
import { itemSpecs, positionErrors, positionOf, type Clip } from "@/lib/video";
import { safeUrl } from "@/lib/format";
import {
  progressText,
  UPLOAD_START,
  UPLOAD_TYPES,
  uploadFile,
  type UploadProgress,
} from "@/lib/takes";
import type { Field, Raw, Update } from "@/lib/workflow";

type Change = (key: string, u: Update) => void;
type Pending = {
  id: number;
  key: string;
  name: string;
  p: UploadProgress;
  stop: AbortController;
  error?: string;
};

/** Adds takes to a field's references (each once, up to its max), onto the list as it is when they arrive. */
export const addRefs = (onChange: Change, f: Field, ids: string[]) =>
  onChange(f.key, (old) => withIds(f, old, addIds(refIds(f, old), ids, capacity(f))));

/** Uploads into reference fields: each file becomes a gallery take, then a reference. One per form, shared by its
 * editors and drop targets, so progress shows wherever the field is. */
export function useRefUploads(onChange: Change) {
  const [list, setList] = useState<Pending[]>([]);
  const seq = useRef(0);
  const upload = (f: Field, files: File[]) => {
    const ids = files.map(() => ++seq.current);
    const stops = files.map(() => new AbortController());
    const set = (id: number, p: Partial<Pending>) =>
      setList((l) => l.map((x) => (x.id === id ? { ...x, ...p } : x)));
    setList((l) => [
      ...l,
      ...files.map((file, i) => ({
        id: ids[i],
        key: f.key,
        name: file.name,
        p: UPLOAD_START,
        stop: stops[i],
      })),
    ]);
    // all upload at once; each becomes a reference in drop order once those before it are settled
    const jobs = files.map((file, i) =>
      uploadFile(file, (p) => set(ids[i], { p }), undefined, { signal: stops[i].signal }),
    );
    void inOrder(
      jobs,
      (t, i) => {
        remember([t]);
        setList((l) => l.filter((x) => x.id !== ids[i]));
        addRefs(onChange, f, [t.id]);
      },
      (e, i) => set(ids[i], { error: message(e) }),
    );
  };
  // × on a pending tile stops its optimising or PUT; on a failed one it dismisses
  const dismiss = (id: number) =>
    setList((l) => {
      l.find((x) => x.id === id)?.stop.abort();
      return l.filter((x) => x.id !== id);
    });
  return { list, upload, dismiss };
}
export type RefUploads = ReturnType<typeof useRefUploads>;

/** Takes a drop or paste of image files or dragged takes into field f; false when it carries none. */
export function takeDrop(
  dt: DataTransfer,
  f: Field,
  room: number,
  onChange: Change,
  uploads: RefUploads,
) {
  const d = dropped(dt);
  if (!d.files.length && !d.takeIds.length) return false;
  if (d.takeIds.length) addRefs(onChange, f, d.takeIds);
  if (d.files.length) uploads.upload(f, d.files.slice(0, Math.max(0, room - d.takeIds.length)));
  return true;
}

/** The editor: `compact` is the composer's chips row, else the inspector's grid with Upload and a hint. */
export function RowsEditor({
  f,
  raw,
  error,
  onChange,
  uploads,
  compact = false,
  extra,
  clip = null,
}: {
  f: Field;
  raw: Raw | undefined;
  error?: string;
  onChange: Change;
  uploads: RefUploads;
  compact?: boolean;
  /** compact: shown between the references and "Add from gallery" (the composer's mention chips) */
  extra?: React.ReactNode;
  /** the clip the form makes: where an item's time on the clip (`position: seconds`) can go */
  clip?: Clip | null;
}) {
  const ids = refIds(f, raw);
  const get = useTakes(ids);
  const max = capacity(f);
  const pending = uploads.list.filter((u) => u.key === f.key);
  const room = max - ids.length - pending.filter((u) => !u.error).length;
  const [picking, setPicking] = useState(false);
  const [over, setOver] = useState(false);
  const box = useRef<HTMLDivElement>(null);
  const fileInput = useRef<HTMLInputElement>(null);
  const dragFrom = useRef<number | null>(null);
  const set = (next: string[]) => onChange(f.key, (old) => withIds(f, old, next));
  const focusItem = (id: string) =>
    requestAnimationFrame(() =>
      box.current?.querySelector<HTMLElement>(`[data-ref="${CSS.escape(id)}"]`)?.focus(),
    );
  const label = (i: number) => mentionOf(f.param, i + 1);
  const name = f.param.label ?? f.param.name;
  // an item with fields besides its image (keyframes: a time and a strength) is a card per row
  const cards = !compact && itemSpecs(f.param).length > 0;
  const rows = Array.isArray(raw) ? raw : Array.isArray(f.initial) ? f.initial : [];
  const issues = positionErrors(f.param, rows, clip);
  const imageKey =
    Object.keys(f.param.item ?? {}).find((k) => f.param.item?.[k] === "Image") ?? "image";

  const items = ids.map((id, i) => {
    const t = get(id);
    const thumb = t ? safeUrl(t.thumb_url) : undefined;
    return (
      <div
        key={id}
        role="listitem"
        data-ref={id}
        tabIndex={0}
        draggable
        aria-label={`${label(i)}: ${t ? t.title : t === null ? "gone" : "loading"}`}
        aria-keyshortcuts="Alt+ArrowLeft Alt+ArrowRight Delete"
        title={`${t?.title ?? ""} · drag or Alt + arrow keys to move`}
        onDragStart={(e) => {
          dragFrom.current = i;
          e.dataTransfer.effectAllowed = "move";
          e.dataTransfer.setData("text/plain", label(i));
        }}
        onDragEnd={() => (dragFrom.current = null)}
        onDragOver={(e) => dragFrom.current !== null && e.preventDefault()}
        onDrop={(e) => {
          if (dragFrom.current === null) return;
          e.preventDefault();
          e.stopPropagation();
          set(move(ids, dragFrom.current, i));
          dragFrom.current = null;
        }}
        onKeyDown={(e) => {
          const d =
            e.altKey && (e.key === "ArrowLeft" || e.key === "ArrowUp")
              ? -1
              : e.altKey && (e.key === "ArrowRight" || e.key === "ArrowDown")
                ? 1
                : 0;
          if (d) {
            e.preventDefault();
            set(move(ids, i, i + d));
            focusItem(id);
          } else if (e.key === "Delete" || e.key === "Backspace") {
            e.preventDefault();
            set(ids.filter((x) => x !== id));
            const nextId = ids[i + 1] ?? ids[i - 1];
            if (nextId) focusItem(nextId);
          }
        }}
        className={
          compact
            ? "flex h-8 cursor-grab items-center gap-1.5 rounded-lg bg-s5 pr-1 pl-1 text-[12px]"
            : "relative aspect-square cursor-grab overflow-hidden rounded-[10px] bg-s1"
        }
      >
        {thumb ? (
          <img
            loading="lazy"
            decoding="async"
            src={thumb}
            alt=""
            draggable={false}
            className={compact ? "size-6 rounded object-cover" : "size-full object-cover"}
          />
        ) : (
          <span
            className={`grid place-items-center text-[10px] text-t2 ${compact ? "size-6 rounded bg-s3" : "size-full"}`}
          >
            {t === null ? "gone" : ""}
          </span>
        )}
        <span
          className={
            compact
              ? "mono"
              : "mono absolute bottom-1 left-1 rounded bg-s2/90 px-1 text-[11px] text-t1"
          }
        >
          {label(i)}
        </span>
        <button
          type="button"
          aria-label={`Remove ${label(i)}`}
          onClick={() => set(ids.filter((x) => x !== id))}
          className={
            compact
              ? "grid size-6 place-items-center rounded text-t2 hover:text-t1"
              : "absolute top-1 right-1 grid size-6 place-items-center rounded-full bg-s3 text-t1"
          }
        >
          <X size={12} aria-hidden />
        </button>
      </div>
    );
  });

  const card = (id: string, i: number) => {
    const t = get(id);
    const thumb = t ? safeUrl(t.thumb_url) : undefined;
    const title = t ? t.title : t === null ? "gone" : "loading";
    const issue = issues[i];
    return (
      <li
        key={id}
        data-ref={id}
        tabIndex={-1}
        className={`flex flex-col gap-2 rounded-[10px] border bg-s1 px-3 py-2.5 ${issue ? "border-bad" : "border-s3"}`}
      >
        <div className="flex items-center gap-2">
          {thumb ? (
            <img
              loading="lazy"
              decoding="async"
              src={thumb}
              alt=""
              className="h-7 w-11 shrink-0 rounded-md object-cover"
            />
          ) : (
            <span
              aria-hidden
              className="grid h-7 w-11 shrink-0 place-items-center rounded-md border border-dashed border-dash bg-s3 text-t2"
            >
              {t?.kind === "image" ? (
                <ImageIcon size={14} />
              ) : t?.kind === "video" ? (
                <Film size={14} />
              ) : t?.kind === "audio" ? (
                <AudioLines size={14} />
              ) : (
                <ImageOff size={14} />
              )}
            </span>
          )}
          <span className="min-w-0 grow truncate" title={title}>
            {title}
          </span>
          <button
            type="button"
            aria-label={`Remove ${title}`}
            onClick={() => set(ids.filter((x) => x !== id))}
            className="grid size-[22px] shrink-0 place-items-center rounded-lg bg-s3 text-t2 hover:text-t1"
          >
            <X size={12} aria-hidden />
          </button>
        </div>
        <ItemFields
          p={f.param}
          row={rows[i] ?? {}}
          id={`f-${f.key.replace(/\W/g, "-")}-${i}`}
          clip={clip}
          skip={[imageKey]}
          onRow={(patch) =>
            onChange(f.key, (old) => {
              const list = Array.isArray(old) ? old : rows;
              return list.map((r, j) => (j === i ? { ...r, ...patch } : r));
            })
          }
        />
        {issue && (
          <span role="alert" className="text-[12px] text-bad">
            {issue}
          </span>
        )}
      </li>
    );
  };

  const busy = pending.map((u) => (
    <div
      key={u.id}
      role="listitem"
      className={`flex items-center justify-center gap-1 rounded-lg border border-dashed border-dash text-[11px] ${u.error ? "text-bad" : "text-t2"} ${compact ? "h-8 px-2" : "relative aspect-square flex-col p-1.5 text-center"}`}
    >
      {u.error ? (
        <span role="alert">{u.error}</span>
      ) : compact ? (
        <span aria-live="polite">{progressText(u.p)}</span>
      ) : (
        <UploadStatus p={u.p} name={u.name} />
      )}
      <button
        type="button"
        aria-label={u.error ? `Dismiss ${u.name}` : `Cancel the upload of ${u.name}`}
        className={`grid size-6 shrink-0 place-items-center rounded-full bg-s3 text-t1 ${compact || u.error ? "" : "absolute top-1 right-1"}`}
        onClick={() => uploads.dismiss(u.id)}
      >
        <X size={12} aria-hidden />
      </button>
    </div>
  ));

  const addButton = room > 0 && (
    <button
      type="button"
      onClick={() => setPicking(true)}
      className={
        compact
          ? "h-8 rounded-lg border border-dashed border-dash bg-transparent px-2.5 text-[12px] text-t2"
          : "flex h-10 items-center justify-center gap-1.5 rounded-[10px] border border-dashed border-dash text-[13px]"
      }
    >
      {!compact && <ImagePlus size={14} aria-hidden />}
      Add from gallery
    </button>
  );

  return (
    <div
      ref={box}
      className={`${compact ? "flex flex-wrap items-center gap-2" : "flex flex-col gap-2.5"} ${over ? "rounded-lg outline-2 outline-offset-2 outline-a1 outline-dashed" : ""}`}
      onDragOver={(e) => {
        if (dragFrom.current !== null || !carriesImage(e.dataTransfer) || room <= 0) return;
        e.preventDefault();
        setOver(true);
      }}
      onDragLeave={(e) => {
        if (!e.currentTarget.contains(e.relatedTarget as Node | null)) setOver(false);
      }}
      onDrop={(e) => {
        setOver(false);
        if (dragFrom.current !== null) return;
        if (takeDrop(e.dataTransfer, f, room, onChange, uploads)) e.preventDefault();
      }}
      onPaste={(e) => {
        if (takeDrop(e.clipboardData, f, room, onChange, uploads)) e.preventDefault();
      }}
    >
      {!compact && (
        <span className="mono flex justify-between gap-2 text-[12px] text-t2">
          <span className="min-w-0">{f.optional && "Optional: may stay empty"}</span>
          <span aria-label={`${ids.length} of ${max}`} className="shrink-0">
            {ids.length} / {max}
          </span>
        </span>
      )}
      {compact ? (
        <>
          <div role="list" aria-label={name} className="contents">
            {items}
            {busy}
          </div>
          {extra}
          {addButton}
        </>
      ) : (
        <>
          {cards && clip && positionOf(f.param) && rows.length > 0 && (
            <ClipTrack p={f.param} rows={rows} imageKey={imageKey} clip={clip} />
          )}
          {cards && ids.length > 0 && (
            <ol aria-label={`${name}, in order`} className="m-0 flex list-none flex-col gap-2 p-0">
              {ids.map(card)}
            </ol>
          )}
          {(cards ? pending.length > 0 : ids.length > 0 || pending.length > 0) && (
            <div role="list" aria-label={name} className="grid grid-cols-3 gap-2">
              {!cards && items}
              {busy}
            </div>
          )}
          {room > 0 && (
            <div className="grid grid-cols-2 gap-2">
              {addButton}
              <button
                type="button"
                onClick={() => fileInput.current?.click()}
                className="flex h-10 items-center justify-center gap-1.5 rounded-[10px] border border-dashed border-dash text-[13px]"
              >
                <Upload size={14} aria-hidden />
                Upload
              </button>
            </div>
          )}
          <input
            ref={fileInput}
            type="file"
            accept={UPLOAD_TYPES.join(",")}
            multiple={max > 1}
            hidden
            onChange={(e) => {
              if (e.target.files) uploads.upload(f, Array.from(e.target.files).slice(0, room));
              e.target.value = "";
            }}
          />
          <span className="flex items-center text-[12px] text-t2">
            Drop or paste images here, or onto the stage.
            {positionOf(f.param) && " A new one goes on the first free end of the clip."}
            <Info>
              Each image is added to the gallery first. Drag to reorder, or focus one and press Alt
              with an arrow key; Delete removes it.
            </Info>
          </span>
        </>
      )}
      {error && <span className="text-[12px] text-bad">{error}</span>}
      <GalleryPicker
        open={picking}
        onClose={() => setPicking(false)}
        max={Math.max(room, 0)}
        help={`Images only. Picks join the references in the order picked; the limit is what is left of the workflow's maximum (${max} here, ${max - room} already added).`}
        onPick={(takes) => {
          remember(takes);
          addRefs(
            onChange,
            f,
            takes.map((t) => t.id),
          );
        }}
      />
    </div>
  );
}

/** A take's references as small thumbnails (the gallery's "Made from"); `onPick` selects one. */
export function RefThumbs({ ids, onPick }: { ids: string[]; onPick: (t: S<"Take">) => void }) {
  const get = useTakes(ids);
  return (
    <span className="flex flex-wrap justify-end gap-1.5">
      {ids.map((id, i) => {
        const t = get(id);
        return t ? (
          <button
            key={id}
            type="button"
            title={t.title}
            aria-label={`Reference ${i + 1}: ${t.title}`}
            onClick={() => onPick(t)}
            className="bevel size-10 overflow-hidden rounded-md bg-s1"
          >
            <img
              loading="lazy"
              decoding="async"
              src={safeUrl(t.thumb_url)}
              alt=""
              className="size-full object-cover"
            />
          </button>
        ) : (
          <span
            key={id}
            className="grid size-10 place-items-center rounded-md bg-s1 text-t2"
            aria-hidden
          >
            {t === null ? <ImageOff size={16} /> : <Loader2 size={16} className="animate-spin" />}
          </span>
        );
      })}
    </span>
  );
}

/** What opens a tile's picker: the composer's + calls the first one. */
export type Opener = React.RefObject<(() => void) | null>;

/** The composer's "First frame" tile (docs/mock/TextToVideo.dc.html): the take on the clip's first frame (`id`) and
 * how many other frames there are, or "None · text only"; it opens the gallery picker for one image. */
export function FirstFrameTile({
  id,
  more = 0,
  onPick,
  label = "First frame",
  empty = "None · text only",
  card,
  openerRef,
  frames,
}: {
  id?: string;
  more?: number;
  onPick: (takeId: string) => void;
  /** API 1.37 (docs/mock/H3Options.dc.html frame 8): the picker shows videos too, and a video offers its last or
   * first frame (made as an image take) */
  frames?: boolean;
  /** the tile's label (an image param's: "Last frame") and what it says with no take */
  label?: string;
  empty?: string;
  /** a video workflow's tile (docs/mock/ImageToVideo.dc.html): the label over the value, the picture only with a take */
  card?: boolean;
  /** set to what opens the picker (the composer's +) */
  openerRef?: Opener;
}) {
  const [picking, setPicking] = useState(false);
  useEffect(() => {
    if (openerRef) openerRef.current = () => setPicking(true);
  }, [openerRef]);
  const t = useTakes(id ? [id] : [])(id ?? "");
  const thumb = t ? safeUrl(t.thumb_url) : undefined;
  const name = id ? (t?.title ?? (t === null ? "gone" : "…")) : empty;
  const shown = more ? `${name} +${more}` : name;
  return (
    <>
      <button
        type="button"
        aria-haspopup="dialog"
        aria-label={`${label}: ${shown}`}
        onClick={() => setPicking(true)}
        className={
          card
            ? "bevel flex min-h-12 flex-[1_1_130px] items-center gap-2 rounded-xl bg-s3 px-3 py-1.5 text-left"
            : "bevel flex h-8 max-w-[28ch] items-center gap-2 rounded-lg bg-s3 pr-2.5 pl-1 text-[12px]"
        }
      >
        {thumb ? (
          <img
            loading="lazy"
            decoding="async"
            src={thumb}
            alt=""
            className={
              card ? "h-7 w-11 shrink-0 rounded-md object-cover" : "size-6 rounded-md object-cover"
            }
          />
        ) : card ? (
          <span
            aria-hidden
            className="grid h-7 w-11 shrink-0 place-items-center rounded-md border border-dashed border-dash text-t2"
          >
            {id ? <ImageOff size={14} /> : <ImagePlus size={14} />}
          </span>
        ) : (
          <span
            aria-hidden
            className="grid size-6 shrink-0 place-items-center rounded-md border border-dashed border-dash text-t2"
          >
            {id ? <ImageOff size={12} /> : <ImagePlus size={12} />}
          </span>
        )}
        {card ? (
          <span className="flex min-w-0 flex-col">
            <small className="mono text-[10px] text-t2">{label}</small>
            <b className="truncate text-[13px] font-semibold">{shown}</b>
          </span>
        ) : (
          <>
            <span className="mono text-[10px] text-t2">{label}</span>
            <b className="truncate font-semibold">{shown}</b>
          </>
        )}
      </button>
      <GalleryPicker
        open={picking}
        onClose={() => setPicking(false)}
        max={1}
        title={frames ? label : undefined}
        frames={
          frames
            ? async (v, at) => {
                remember([v]);
                onPick(await frameTake(v.id, at));
              }
            : undefined
        }
        onPick={(takes) => {
          remember(takes);
          if (takes[0]) onPick(takes[0].id);
        }}
      />
    </>
  );
}
