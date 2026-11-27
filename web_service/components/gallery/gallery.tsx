"use client";
// Gallery: every take of the user (docs/mock/Gallery.dc.html, plan 9.1). Search and chips filter the list (the API
// ANDs them), pages load as the grid scrolls, images (and wav, mp3 or m4a audio) upload from the picker or by dropping them
// on the grid, and the side panel shows the selected take with the actions that exist so far.
import Link from "next/link";
import { useRouter } from "next/navigation";
import { useCallback, useEffect, useRef, useState, useSyncExternalStore } from "react";
import {
  ArrowRight,
  AudioLines,
  Check,
  Download,
  FileText,
  Film,
  Maximize,
  Palette,
  Pencil,
  Play as PlayIcon,
  Plus,
  RotateCcw,
  Star,
  Trash2,
  UserRound,
  Wand2,
  Workflow,
  X,
} from "lucide-react";
import { openMenu, type Item } from "@/components/context-menu";
import { FullScreen, Media, Thumb, useFullScreenKey } from "@/components/media";
import { videoLine } from "@/lib/media";
import { renderTime } from "@/lib/runs";
import { ErrorNote, Info, Modal, UploadStatus } from "@/components/kit";
import { RefThumbs } from "@/components/create/refs";
import { ElementEditor } from "@/components/create/elements";
import { AddToTimeline, AddedNote, addToTimeline } from "@/components/timeline/add";
import { SaveAsLook } from "@/components/create/looks";
import { lookGroupLabel } from "@/lib/looks";
import { useLooks } from "@/lib/library";
import { offerLabel } from "@/lib/source";
import { remember, useTakes, useUses } from "@/lib/take-cache";
import { api, message, ok, type S } from "@/lib/api";
import { SearchField } from "@/components/search";
import { mb, safeUrl } from "@/lib/format";
import { useLoras } from "@/lib/lora-library";
import { madeWithLine } from "@/lib/loras";
import { FrameIn } from "@/components/create/frame-in";
import { useScreenTools } from "@/components/assistant/tools";
import { useLibraryDialog } from "@/components/assistant/library";
import { ORDERS, galleryTools } from "@/components/gallery/assistant";
import { sliceFrom, useGridWindow } from "@/components/gallery/window";
import {
  EditPanel,
  SavedToast,
  canEdit,
  saveAsNew,
  useMediaTools,
} from "@/components/gallery/edit";
import {
  AUDIO_ACCEPT,
  UPLOAD_START,
  UPLOAD_TYPES,
  appendPage,
  fits,
  lastProject,
  lyricsOf,
  sizeSeed,
  songLine,
  soundOf,
  soundTakeOf,
  splitOffers,
  subLine,
  takeQuery,
  templateLine,
  uploadFile,
  type Filter,
  type UploadProgress,
} from "@/lib/takes";

type Take = S<"Take">;
type Upload = {
  id: number;
  name: string;
  p: UploadProgress;
  stop: AbortController;
  error?: string;
};

/** A page of the grid. */
export const PAGE = 60;

/** API 1.79 (docs/mock/CreateMusicSound.dc.html frame 6): Voice, Music and Sound in place of Audio. */
const KINDS: [NonNullable<Filter["kind"]>, string][] = [
  ["image", "Images"],
  ["video", "Video"],
  ["voice", "Voice"],
  ["music", "Music"],
  ["sound", "Sound"],
];
const CLASSES: readonly string[] = ["music", "sound", "voice"];
const SOURCES: [S<"TakeSource">, string][] = [
  ["create", "From Create"],
  ["pipeline", "From Pipelines"],
  ["export", "From Timeline"],
  ["upload", "Uploads"],
];

/** A filter chip: pressed is the light chip of the mock. */
export function Chip({
  pressed,
  onClick,
  children,
  disabled,
  title,
}: {
  pressed: boolean;
  onClick: () => void;
  children: React.ReactNode;
  disabled?: boolean;
  title?: string;
}) {
  return (
    <button
      type="button"
      aria-pressed={pressed}
      disabled={disabled}
      title={title}
      onClick={onClick}
      className="chip bevel aria-pressed:bg-t1 aria-pressed:font-semibold aria-pressed:text-panel"
    >
      {children}
    </button>
  );
}

/** Save as pipeline: the take's lineage as a chain of steps (the new pipeline, or the 422 says why not). */
const pipelineOf = (id: string) =>
  ok(
    api.POST("/v1/studio/takes/{take_id}/pipeline", {
      params: { path: { take_id: id } },
      body: {},
    }),
  );
const usesOf = (id: string) =>
  ok(api.GET("/v1/studio/takes/{take_id}/uses", { params: { path: { take_id: id } } })).then(
    (r) => r.uses,
  );

const del = (id: string) =>
  ok(api.DELETE("/v1/studio/takes/{take_id}", { params: { path: { take_id: id } } }));
const patch = (id: string, body: S<"TakePatch">) =>
  ok(api.PATCH("/v1/studio/takes/{take_id}", { params: { path: { take_id: id } }, body }));

/** The take list for a filter and a search (typed text, waited on): pages load as `sentinel` nears the view, a new
 * filter or search replaces the list. `initial` stands for the first page of `filter` with no search. Shared by the
 * Gallery and the gallery picker. */
export function useTakePages(filter: Filter, q: string, initial?: S<"TakePage">, limit = PAGE) {
  const [query, setQuery] = useState(q);
  const [takes, setTakes] = useState<Take[] | null>(initial?.takes ?? null);
  const [next, setNext] = useState<string | null>(initial?.next_cursor ?? null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const sentinel = useRef<HTMLDivElement>(null);
  const seq = useRef(0);

  // the search waits for a pause in typing
  useEffect(() => {
    const t = setTimeout(() => setQuery(q), 300);
    return () => clearTimeout(t);
  }, [q]);

  /** Loads a page (null: the first, replacing the list); an answer for an older filter is dropped. */
  const fetchPage = useCallback(
    (cursor: string | null) => {
      const n = ++seq.current;
      setLoading(true);
      setError("");
      ok(
        api.GET("/v1/studio/takes", {
          params: { query: { ...takeQuery(filter, query, cursor), limit } },
        }),
      )
        .then((p) => {
          if (n !== seq.current) return;
          setTakes((old) => (cursor && old ? appendPage(old, p.takes) : p.takes));
          setNext(p.next_cursor);
        })
        .catch((e) => n === seq.current && setError(message(e)))
        .finally(() => n === seq.current && setLoading(false));
    },
    [filter, query, limit],
  );
  // a new filter or search: the first page replaces the list (the old one stays until it comes)
  // the server's first page stands for the unfiltered list (the first fetchPage) until the filter or search changes
  const serverPage = useRef(initial ? fetchPage : null);
  useEffect(() => {
    if (fetchPage !== serverPage.current) fetchPage(null);
  }, [fetchPage]);

  // the next page once the end of the grid comes near; again after each page while the end is still in view
  useEffect(() => {
    const el = sentinel.current;
    if (!el || !next || loading || error) return;
    const io = new IntersectionObserver((es) => es[0].isIntersecting && fetchPage(next), {
      rootMargin: "600px",
    });
    io.observe(el);
    return () => io.disconnect();
  }, [next, loading, error, fetchPage]);

  return { takes, setTakes, next, loading, error, query, fetchPage, sentinel };
}

/** One take in a grid: its picture (or kind), star, the selection tick, title and sub line. */
export function TakeCard({
  take: t,
  on,
  selecting,
  disabled,
  onClick,
  onDoubleClick,
  sub,
}: {
  take: Take;
  on: boolean;
  selecting: boolean;
  disabled?: boolean;
  onClick: () => void;
  /** a double-click (the Gallery: its full preview) */
  onDoubleClick?: () => void;
  /** the mono line, else subLine */
  sub?: string;
}) {
  return (
    <button
      type="button"
      aria-pressed={on}
      disabled={disabled}
      onClick={onClick}
      onDoubleClick={onDoubleClick}
      className="group bevel flex w-full flex-col gap-1.5 rounded-[10px] bg-transparent text-left disabled:opacity-50"
    >
      <span className="relative block w-full">
        {t.kind === "image" || t.kind === "video" || t.kind === "audio" ? (
          <Thumb
            take={t}
            imgClassName={`block aspect-[16/10] w-full rounded-[10px] bg-s1 object-cover ${on ? "outline-2 -outline-offset-2 outline-a1" : ""}`}
          />
        ) : (
          <i
            className={`flex aspect-[16/10] flex-col items-center justify-center gap-1 rounded-[10px] bg-s1 text-[11px] text-t2 not-italic ${on ? "border-2 border-a1" : "border border-dashed border-dash"}`}
          >
            <FileText size={16} aria-hidden />
            {t.kind}
          </i>
        )}
        {t.starred && (
          <Star
            size={14}
            aria-label="starred"
            className="absolute top-2 right-2 fill-a1 text-a1 drop-shadow"
          />
        )}
        {selecting && on && (
          <Check
            size={14}
            strokeWidth={3}
            aria-hidden
            className="absolute top-2 left-2 rounded-full bg-a1 p-0.5 text-on1"
          />
        )}
      </span>
      <b className="truncate px-2 text-[12px] font-semibold">{t.title}</b>
      <small className="mono truncate px-2 pb-2 text-[10px] text-t2">{sub ?? subLine(t)}</small>
    </button>
  );
}

const noSubscribe = () => () => {};

/** The Gallery screen; `initial` is the first page (of `batch` when given), read on the server. `batch`: the
 * Pipelines list's Open collection, shown as a clearable "Batch · <title>" chip. */
export function Gallery({
  initial,
  batch,
}: {
  initial?: S<"TakePage">;
  batch?: { id: string; title: string };
}) {
  const [filter, setFilter] = useState<Filter>(batch ? { batch_id: batch.id } : {});
  // "This project only": the session last opened in Create in this browser (none on the server)
  const project = useSyncExternalStore(noSubscribe, lastProject, () => null);
  // a cleared batch leaves the URL too, so a reload shows the whole gallery
  useEffect(() => {
    if (!filter.batch_id && new URLSearchParams(location.search).has("batch"))
      history.replaceState(null, "", "/gallery");
  }, [filter.batch_id]);
  const [q, setQ] = useState("");
  const { takes, setTakes, next, loading, error, query, fetchPage, sentinel } = useTakePages(
    filter,
    q,
    initial,
  );
  const [currentId, setCurrentId] = useState<string | null>(null);
  const [selecting, setSelecting] = useState(false);
  const [picked, setPicked] = useState<Set<string>>(new Set());
  const [uploads, setUploads] = useState<Upload[]>([]);
  const [dragging, setDragging] = useState(false);
  const [modal, setModal] = useState<"rename" | "delete" | "bulk-delete" | null>(null);
  const [actionError, setActionError] = useState("");
  // the takes a new element is being made from (Save as element)
  const [making, setMaking] = useState<Take[] | null>(null);
  const [lookOf, setLookOf] = useState<Take | null>(null);
  const [full, setFull] = useState(false);
  // Edit (alpha.2 item 11): the take being edited, Save as new's toast and why it failed
  const tools = useMediaTools();
  const [editing, setEditing] = useState<string | null>(null);
  const [saved, setSaved] = useState<
    { saving: true } | { saving: false; take: Take; from: string } | null
  >(null);
  const [editError, setEditError] = useState("");
  useEffect(() => {
    if (!saved || saved.saving) return;
    const t = setTimeout(() => setSaved(null), 8000);
    return () => clearTimeout(t);
  }, [saved]);
  const fileInput = useRef<HTMLInputElement>(null);
  const uploadSeq = useRef(0);
  // only the rows near the screen are drawn (item 32): uploads first, then the takes, as the grid holds them
  const grid = useRef<HTMLDivElement>(null);
  const win = useGridWindow(grid, uploads.length + (takes?.length ?? 0));

  // a take picked from another one's lineage may not be in the list loaded
  const outside = currentId && !takes?.some((t) => t.id === currentId) ? currentId : null;
  const lookup = useTakes(outside ? [outside] : []);
  const current =
    takes?.find((t) => t.id === currentId) ??
    (currentId ? lookup(currentId) : null) ??
    (selecting ? undefined : takes?.[0]);
  useFullScreenKey(!!current && !modal && !full, () => setFull(true));
  const madeFrom = takes?.filter((t) => picked.has(t.id) && t.kind === "image");
  const replace = (t: Take) => setTakes((list) => list && list.map((x) => (x.id === t.id ? t : x)));
  const remove = (ids: string[]) =>
    setTakes((list) => list && list.filter((x) => !ids.includes(x.id)));

  /** Save as new: the new take goes next to the original and is selected, with the toast. */
  async function saveNew(from: Take, tool: string, input: Record<string, unknown>) {
    setSaved({ saving: true });
    setEditError("");
    try {
      const t = await saveAsNew(tool, input);
      remember([t]);
      setTakes((list) => {
        if (!list) return list;
        const rest = list.filter((x) => x.id !== t.id);
        const i = rest.findIndex((x) => x.id === from.id) + 1;
        return [...rest.slice(0, i), t, ...rest.slice(i)];
      });
      setEditing(null);
      setCurrentId(t.id);
      setSaved({ saving: false, take: t, from: from.id });
    } catch (e) {
      setSaved(null);
      setEditError(message(e));
    }
  }

  async function act(f: () => Promise<unknown>) {
    setActionError("");
    try {
      await f();
    } catch (e) {
      setActionError(message(e));
    }
  }

  function upload(files: FileList | File[]) {
    for (const f of Array.from(files)) {
      const id = ++uploadSeq.current;
      const set = (u: Partial<Upload>) =>
        setUploads((list) => list.map((x) => (x.id === id ? { ...x, ...u } : x)));
      const stop = new AbortController();
      setUploads((list) => [...list, { id, name: f.name, p: UPLOAD_START, stop }]);
      // an audio file takes the class of the filter it is dropped on (API 1.79; else voice)
      const sound = CLASSES.includes(filter.kind ?? "")
        ? (filter.kind as S<"SoundClass">)
        : undefined;
      uploadFile(f, (p) => set({ p }), undefined, { audio: true, signal: stop.signal, sound })
        .then((t) => {
          setUploads((list) => list.filter((x) => x.id !== id));
          if (fits(t, filter) && !query.trim())
            setTakes((list) => list && [t, ...list.filter((x) => x.id !== t.id)]);
          setCurrentId(t.id);
        })
        .catch((e) => set({ error: message(e) }));
    }
  }

  async function bulk(
    f: (id: string) => Promise<unknown>,
    after: (ids: string[]) => void,
    ids = [...picked],
  ) {
    const results = await Promise.allSettled(ids.map(f));
    const done = ids.filter((_, i) => results[i].status === "fulfilled");
    after(done);
    const failed = results.find((r) => r.status === "rejected");
    setActionError(
      failed
        ? `${ids.length - done.length} of ${ids.length} failed: ${message(failed.reason)}`
        : "",
    );
  }

  // Add to timeline from a menu: its note under the selection bar
  const [added, setAdded] = useState<{ id: string; name: string; n: number } | null>(null);
  const addTo = (ids: string[], audio = false) =>
    void act(async () => setAdded({ ...(await addToTimeline(ids, audio)), n: ids.length }));
  const [frameAt, setFrameAt] = useState<{ id: string; at: "first" | "last" } | null>(null);
  // a menu's Play: the panel plays that take once it shows it
  const [play, setPlay] = useState<string | null>(null);
  const starAll = (ids: string[]) =>
    void bulk(
      (id) => patch(id, { starred: true }),
      (done) =>
        setTakes(
          (list) => list && list.map((t) => (done.includes(t.id) ? { ...t, starred: true } : t)),
        ),
      ids,
    );

  /** A take's right-click menu (ContextMenus frame 4): the side panel's actions; with Select several on, the
   * selection bar's (a card outside the picked ones is picked alone first). */
  function takeMenu(e: React.MouseEvent, t: Take) {
    if (selecting) {
      const ids = picked.has(t.id) ? [...picked] : [t.id];
      const list = (takes ?? []).filter((x) => ids.includes(x.id));
      const images = list.every((x) => x.kind === "image") && ids.length <= 4;
      const n = ids.length;
      const h = openMenu(e, {
        title: n === 1 ? "1 take" : `${n} takes`,
        items: [
          { label: "Star", icon: Star, onPick: () => starAll(ids) },
          { label: "Add to timeline", icon: Plus, onPick: () => addTo(ids) },
          {
            label: "Save as element",
            icon: UserRound,
            off: images ? undefined : "An element takes 1 to 4 images",
            onPick: () => setMaking(list),
          },
          { label: "Done", icon: Check, onPick: () => (setSelecting(false), setPicked(new Set())) },
          "-",
          {
            label: n === 1 ? "Delete 1 take" : `Delete ${n} takes`,
            icon: Trash2,
            danger: true,
            onPick: () => setModal("bulk-delete"),
          },
        ],
      });
      if (h && !picked.has(t.id)) setPicked(new Set([t.id]));
      return;
    }
    const m = t.made_with;
    const session = t.session_id ? `s=${encodeURIComponent(t.session_id)}&` : "";
    const url = safeUrl(t.url);
    const inItem = (offers: S<"TakeOffer">[] | null): Item => {
      const frames: Item[] =
        t.kind === "video"
          ? [
              {
                label: "Last frame in…",
                icon: Film,
                onPick: () => setFrameAt({ id: t.id, at: "last" }),
              },
              {
                label: "First frame in…",
                icon: Film,
                onPick: () => setFrameAt({ id: t.id, at: "first" }),
              },
            ]
          : [];
      const list: Item[] = splitOffers(t, offers ?? []).rest.map((o) => ({
        label: offerLabel(o),
        onPick: () =>
          router.push(
            `/?${session}use=${encodeURIComponent(t.id)}&w=${encodeURIComponent(o.workflow_id)}`,
          ),
      }));
      if (!offers && !frames.length)
        return { label: "Use in", icon: ArrowRight, off: "Reading what it fits" };
      if (!list.length && !frames.length) return null;
      return {
        label: "Use in",
        icon: ArrowRight,
        items: [...list, ...(list.length && frames.length ? ["-" as const] : []), ...frames],
      };
    };
    const openOffer = (o: S<"TakeOffer">) =>
      router.push(
        `/?${session}use=${encodeURIComponent(t.id)}&w=${encodeURIComponent(o.workflow_id)}`,
      );
    // music and sound (docs/mock/CreateMusicSound.dc.html 6a-6c): Play, the edits, Sound only; no looks or pipelines
    const sound = soundOf(t);
    const plain = sound === "music" || sound === "sound";
    const menu = (offers: S<"TakeOffer">[] | null, alone?: Take) => ({
      title: t.title,
      items: [
        t.kind !== "image" && {
          label: "Play",
          icon: PlayIcon,
          keys: "Space",
          onPick: () => {
            setCurrentId(t.id);
            setPlay(t.id);
          },
        },
        t.kind === "video" && {
          label: "Full screen",
          icon: Maximize,
          keys: "F",
          onPick: () => setFull(true),
        },
        inItem(offers),
        canEdit(tools, t) && {
          label: "Edit",
          icon: Pencil,
          onPick: () => (setEditError(""), setEditing(t.id)),
        },
        { label: "Add to timeline", icon: Plus, onPick: () => addTo([t.id], t.kind === "audio") },
        m && {
          label: "Reuse settings",
          icon: RotateCcw,
          onPick: () => router.push(`/?${session}reuse=${encodeURIComponent(t.id)}`),
        },
        ...splitOffers(t, offers ?? []).edits.map((o) => ({
          label: `${o.action}…`,
          icon: Wand2,
          onPick: () => openOffer(o),
        })),
        alone && {
          label: "Sound only",
          icon: AudioLines,
          onPick: () => setCurrentId(alone.id),
        },
        "-",
        {
          label: t.starred ? "Unstar" : "Star",
          icon: Star,
          onPick: () => void act(async () => replace(await patch(t.id, { starred: !t.starred }))),
        },
        { label: "Rename", icon: Pencil, onPick: () => setModal("rename") },
        t.kind === "image" && {
          label: "Full screen",
          icon: Maximize,
          keys: "F",
          onPick: () => setFull(true),
        },
        t.kind === "image" && {
          label: "Save as element",
          icon: UserRound,
          onPick: () => setMaking([t]),
        },
        m && !plain && { label: "Save as look", icon: Palette, onPick: () => setLookOf(t) },
        m &&
          !plain && {
            label: "Save as pipeline",
            icon: Workflow,
            onPick: () =>
              void act(async () => router.push(`/pipelines/${(await pipelineOf(t.id)).id}`)),
          },
        url && {
          label: "Export file",
          icon: Download,
          onPick: () => {
            const a = document.createElement("a");
            a.href = url;
            a.download = t.title;
            a.click();
          },
        },
        "-",
        { label: "Delete", icon: Trash2, danger: true, onPick: () => setModal("delete") },
      ] as Item[],
    });
    const near = soundTakeOf(t, takes ?? []);
    const h = openMenu(e, menu(null, near));
    if (!h) return;
    setCurrentId(t.id);
    // a video's sound take may not be in the list loaded: its session's sound takes are looked through once
    const alone: Promise<Take | undefined> =
      near || t.kind !== "video" || !t.run_id || !t.session_id
        ? Promise.resolve(near)
        : ok(
            api.GET("/v1/studio/takes", {
              params: { query: { session_id: t.session_id, kind: "sound", limit: 100 } },
            }),
          )
            .then((p) => soundTakeOf(t, p.takes))
            .catch(() => undefined);
    Promise.all([usesOf(t.id).catch(() => []), alone]).then(([o, a]) => h.update(menu(o, a)));
  }

  const toggle = (id: string) =>
    setPicked((s) => {
      const n = new Set(s);
      if (!n.delete(id)) n.add(id);
      return n;
    });
  const router = useRouter();
  const lib = useLibraryDialog();
  useScreenTools(
    galleryTools({
      q,
      setQ,
      filter,
      setFilter,
      batchTitle: batch?.title,
      takes,
      current,
      setCurrent: setCurrentId,
      selecting,
      picked,
      pick: (ids) => {
        setSelecting(!!ids);
        setPicked(new Set(ids ?? []));
      },
      replace,
      remove,
      push: (url) => router.push(url),
      dialog: lib.ask,
    }),
  );
  const filtered = !!(
    filter.kind ||
    filter.starred ||
    filter.source ||
    filter.session_id ||
    filter.batch_id ||
    query.trim()
  );

  return (
    <>
      <main
        id="main"
        className={`flex min-w-0 flex-[999_1_560px] flex-col gap-4 px-5 pt-4 pb-6 ${dragging ? "rounded-[28px] outline-2 -outline-offset-4 outline-a1 outline-dashed" : ""}`}
        onDragOver={(e) => {
          if (!e.dataTransfer.types.includes("Files")) return;
          e.preventDefault();
          setDragging(true);
        }}
        onDragLeave={(e) => {
          if (!e.currentTarget.contains(e.relatedTarget as Node | null)) setDragging(false);
        }}
        onDrop={(e) => {
          if (!e.dataTransfer.files.length) return;
          e.preventDefault();
          setDragging(false);
          upload(e.dataTransfer.files);
        }}
      >
        <div className="flex flex-wrap items-center gap-3">
          <h1 className="m-0 font-display text-[22px] font-bold tracking-[0.02em]">Gallery</h1>
          <SearchField
            id="q"
            label="Search prompts, elements, models"
            value={q}
            onChange={setQ}
            busy={!!q.trim() && loading}
            className="h-10 flex-[1_1_260px]"
          />
          <input
            ref={fileInput}
            type="file"
            accept={[...UPLOAD_TYPES, AUDIO_ACCEPT].join(",")}
            multiple
            hidden
            onChange={(e) => {
              if (e.target.files) upload(e.target.files);
              e.target.value = "";
            }}
          />
          <button
            type="button"
            className="chip bevel h-10 rounded-xl"
            onClick={() => fileInput.current?.click()}
          >
            Upload
          </button>
          <button
            type="button"
            aria-pressed={selecting}
            className="chip bevel h-10 rounded-xl aria-pressed:bg-t1 aria-pressed:text-panel"
            onClick={() => {
              setSelecting((s) => !s);
              setPicked(new Set());
            }}
          >
            {selecting ? "Done" : "Select several"}
          </button>
        </div>

        <div role="group" aria-label="Filters" className="flex flex-wrap gap-2">
          <Chip
            pressed={
              !filter.kind &&
              !filter.starred &&
              !filter.source &&
              !filter.session_id &&
              !filter.batch_id
            }
            onClick={() => setFilter({})}
          >
            All
          </Chip>
          {KINDS.map(([k, label]) => (
            <Chip
              key={k}
              pressed={filter.kind === k}
              onClick={() => setFilter((f) => ({ ...f, kind: f.kind === k ? undefined : k }))}
            >
              {label}
            </Chip>
          ))}
          <Chip
            pressed={!!filter.starred}
            onClick={() => setFilter((f) => ({ ...f, starred: !f.starred }))}
          >
            Starred
          </Chip>
          <span aria-hidden className="mx-1 my-1 w-px bg-s5" />
          {SOURCES.map(([s, label]) => (
            <Chip
              key={s}
              pressed={filter.source === s}
              onClick={() => setFilter((f) => ({ ...f, source: f.source === s ? undefined : s }))}
            >
              {label}
            </Chip>
          ))}
          <Chip
            pressed={!!filter.session_id}
            disabled={!project && !filter.session_id}
            title={project ? undefined : "Open a project in Create first"}
            onClick={() =>
              setFilter((f) => ({
                ...f,
                session_id: f.session_id ? undefined : (project ?? undefined),
              }))
            }
          >
            This project only
          </Chip>
          {filter.order && filter.order !== "newest" && (
            <button
              type="button"
              aria-label={`Clear sort ${ORDERS[filter.order]}`}
              onClick={() => setFilter((f) => ({ ...f, order: undefined }))}
              className="chip bevel bg-t1 font-semibold text-panel"
            >
              Sort · {ORDERS[filter.order]}
              <X size={12} aria-hidden />
            </button>
          )}
          {filter.batch_id && (
            <button
              type="button"
              aria-label={`Clear batch ${batch?.title || ""}`.trim()}
              onClick={() => setFilter((f) => ({ ...f, batch_id: undefined }))}
              className="chip bevel bg-t1 font-semibold text-panel"
            >
              Batch · {batch?.title || filter.batch_id.slice(0, 8)}
              <X size={12} aria-hidden />
            </button>
          )}
        </div>

        {selecting && (
          <div role="group" aria-label="Selection" className="flex flex-wrap items-center gap-2">
            <span className="mr-1 text-[13px] text-t2" aria-live="polite">
              {picked.size} selected
            </span>
            <button
              type="button"
              className="chip bevel"
              disabled={!picked.size}
              onClick={() =>
                void bulk(
                  (id) => patch(id, { starred: true }),
                  (ids) =>
                    setTakes(
                      (list) =>
                        list && list.map((t) => (ids.includes(t.id) ? { ...t, starred: true } : t)),
                    ),
                )
              }
            >
              <Star size={14} aria-hidden /> Star
            </button>
            <AddToTimeline takeIds={[...picked]} className="chip bevel" />
            <button
              type="button"
              className="chip bevel"
              title="An element takes 1 to 4 images"
              disabled={!madeFrom || madeFrom.length !== picked.size || picked.size > 4}
              onClick={() => setMaking(madeFrom ?? null)}
            >
              Save as element
            </button>
            <button
              type="button"
              className="chip bevel text-bad"
              disabled={!picked.size}
              onClick={() => setModal("bulk-delete")}
            >
              Delete
            </button>
          </div>
        )}

        {added && <AddedNote {...added} />}
        {actionError && <ErrorNote>{actionError}</ErrorNote>}
        {error && (
          <ErrorNote onRetry={() => fetchPage(takes?.length ? next : null)}>{error}</ErrorNote>
        )}

        <div
          role="list"
          aria-label="Takes"
          aria-busy={loading}
          ref={grid}
          style={{ paddingTop: win.padTop, paddingBottom: win.padBottom }}
          className="grid grid-cols-[repeat(auto-fill,minmax(190px,1fr))] gap-3.5"
        >
          {uploads.slice(win.start, win.end).map((u) => (
            <div key={u.id} role="listitem" className="flex flex-col gap-1.5">
              <i
                className={`relative flex aspect-[16/10] flex-col items-center justify-center gap-1 rounded-[10px] border border-dashed border-dash bg-s1 p-2 text-center text-[11px] not-italic ${u.error ? "text-bad" : "text-t2"}`}
              >
                {u.error ? (
                  <span role="alert">{u.error}</span>
                ) : (
                  <span className="flex w-3/5 flex-col items-center gap-1">
                    <UploadStatus p={u.p} name={u.name} />
                  </span>
                )}
                <button
                  type="button"
                  aria-label={u.error ? `Dismiss ${u.name}` : `Cancel the upload of ${u.name}`}
                  className="absolute top-1.5 right-1.5 grid size-6 place-items-center rounded-full bg-s3 text-t1"
                  onClick={() => {
                    u.stop.abort();
                    setUploads((list) => list.filter((x) => x.id !== u.id));
                  }}
                >
                  <X size={12} aria-hidden />
                </button>
              </i>
              <b className="truncate text-[12px] font-semibold">{u.name}</b>
              <small className="mono text-[10px] text-t2">Upload</small>
            </div>
          ))}
          {takes?.slice(...sliceFrom(win, uploads.length)).map((t) => {
            const on = selecting ? picked.has(t.id) : t.id === current?.id;
            return (
              <div
                key={t.id}
                role="listitem"
                className="min-w-0"
                onContextMenu={(e) => takeMenu(e, t)}
              >
                <TakeCard
                  take={t}
                  on={on}
                  selecting={selecting}
                  onClick={() => (selecting ? toggle(t.id) : setCurrentId(t.id))}
                  // a double-click opens the full preview, as the expand button (item 31)
                  onDoubleClick={
                    selecting
                      ? undefined
                      : () => {
                          setCurrentId(t.id);
                          setFull(true);
                        }
                  }
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
        {takes?.length === 0 && !uploads.length && !error && (
          <p className="py-10 text-center text-t2">
            {filtered
              ? "No takes match."
              : "Nothing here yet. What you make in Create lands here; you can also upload images or drop them here."}
          </p>
        )}
        {takes !== null && next && loading && (
          <p role="status" className="text-center text-[13px] text-t2">
            Loading more…
          </p>
        )}
        <div ref={sentinel} aria-hidden className="h-px shrink-0" />
      </main>

      <aside
        aria-label="Selected take"
        className="aside m-3.5 flex flex-[1_1_340px] flex-col gap-[18px] px-[18px] pt-4 pb-6 md:ml-0"
      >
        {current && editing === current.id && canEdit(tools, current) ? (
          <EditPanel
            key={current.id}
            take={current}
            saving={!!saved?.saving}
            error={editError}
            onCancel={() => setEditing(null)}
            onSave={(tool, input) => void saveNew(current, tool, input)}
          />
        ) : current ? (
          <TakePanel
            take={current}
            onStar={() =>
              void act(async () => replace(await patch(current.id, { starred: !current.starred })))
            }
            onRename={() => setModal("rename")}
            onDelete={() => setModal("delete")}
            onFull={() => setFull(true)}
            onMakeElement={() => setMaking([current])}
            frameStart={frameAt?.id === current.id ? frameAt.at : undefined}
            play={play === current.id}
            onPlayed={() => setPlay(null)}
            onSaveLook={() => setLookOf(current)}
            onEdit={
              canEdit(tools, current)
                ? () => {
                    setEditError("");
                    setEditing(current.id);
                  }
                : undefined
            }
            onPick={(t) => {
              if ("url" in t) remember([t]);
              setCurrentId(t.id);
            }}
          />
        ) : (
          <p className="text-[13px] text-t2">
            {selecting
              ? "Pick takes to star or delete them together."
              : "Pick a take to see how it was made."}
          </p>
        )}
      </aside>
      {saved && (
        <SavedToast
          state={saved.saving ? saved : { saving: false, title: saved.take.title }}
          onUndo={() => {
            if (saved.saving) return;
            setSaved(null);
            void act(async () => {
              await del(saved.take.id);
              remove([saved.take.id]);
              setCurrentId(saved.from);
            });
          }}
        />
      )}
      {full && current && takes && (
        <FullScreen
          take={current}
          onClose={() => setFull(false)}
          onStep={(d) => {
            const i = takes.findIndex((t) => t.id === current.id) + d;
            if (i >= 0 && i < takes.length) setCurrentId(takes[i].id);
          }}
        />
      )}

      <ElementEditor open={!!making} onClose={() => setMaking(null)} takes={making ?? undefined} />
      <SaveAsLook take={lookOf} onClose={() => setLookOf(null)} />
      {lib.node}
      <Modal
        open={modal === "rename" && !!current}
        onClose={() => setModal(null)}
        title="Rename take"
      >
        {current && (
          <RenameForm
            title={current.title}
            onCancel={() => setModal(null)}
            onSave={async (title) => {
              replace(await patch(current.id, { title }));
              setModal(null);
            }}
          />
        )}
      </Modal>
      <Modal
        open={modal === "delete" || modal === "bulk-delete"}
        onClose={() => setModal(null)}
        title={modal === "bulk-delete" ? `Delete ${picked.size} takes?` : "Delete this take?"}
      >
        <p className="mb-4 text-[13px] text-t2">
          {modal === "bulk-delete" ? "They leave" : "It leaves"} the gallery and Create for good.
        </p>
        <div className="flex justify-end gap-2">
          <button type="button" className="chip bevel" onClick={() => setModal(null)}>
            Cancel
          </button>
          <button
            type="button"
            className="chip bevel bg-a1 font-semibold text-on1"
            onClick={() => {
              setModal(null);
              if (modal === "bulk-delete")
                void bulk(del, (ids) => {
                  remove(ids);
                  setPicked((p) => new Set([...p].filter((id) => !ids.includes(id))));
                });
              else if (current)
                void act(async () => {
                  await del(current.id);
                  remove([current.id]);
                  setCurrentId(null);
                });
            }}
          >
            Delete
          </button>
        </div>
      </Modal>
    </>
  );
}

/** The side panel: the take large, how it was made, and its actions. */
function TakePanel({
  take: t,
  onStar,
  onRename,
  onDelete,
  onFull,
  onMakeElement,
  onSaveLook,
  onEdit,
  onPick,
  frameStart,
  play,
  onPlayed,
}: {
  /** a menu's Last / First frame in…: opens it */
  frameStart?: "first" | "last";
  /** a menu's Play: plays the take now (then onPlayed) */
  play?: boolean;
  onPlayed?: () => void;
  take: Take;
  onStar: () => void;
  onRename: () => void;
  onDelete: () => void;
  onFull: () => void;
  /** opens the element editor with this take */
  onMakeElement: () => void;
  /** opens Save as look with this take */
  onSaveLook: () => void;
  /** opens Edit (Media tools on and running), else undefined */
  onEdit?: () => void;
  /** selects a take of its lineage */
  onPick: (t: Take | S<"TakeRef">) => void;
}) {
  const url = safeUrl(t.url);
  const m = t.made_with;
  const loras = useLoras();
  const looks = useLooks();
  const size = sizeSeed(t);
  // "Used in" is only on the take itself (GET /takes/{id}), not in lists
  const [children, setChildren] = useState<{ id: string; list: S<"TakeRef">[] } | null>(null);
  useEffect(() => {
    let stop = false;
    ok(api.GET("/v1/studio/takes/{take_id}", { params: { path: { take_id: t.id } } }))
      .then((d) => !stop && setChildren({ id: t.id, list: d.children ?? [] }))
      .catch(() => undefined);
    return () => {
      stop = true;
    };
  }, [t.id]);
  const used = children?.id === t.id ? children.list : [];
  const session = t.session_id ? `s=${encodeURIComponent(t.session_id)}&` : "";
  // Save as pipeline: the lineage as a chain of steps; the new pipeline opens, or the 422 says why not
  const router = useRouter();
  const [saving, setSaving] = useState<{ id: string; error: string } | null>(null);
  async function saveAsPipeline() {
    setSaving({ id: t.id, error: "" });
    try {
      const p = await pipelineOf(t.id);
      router.push(`/pipelines/${p.id}`);
    } catch (e) {
      setSaving({ id: t.id, error: message(e) });
    }
  }
  const saveError = saving?.id === t.id ? saving.error : "";
  const offers = useUses(t.id);
  const { edits, rest } = splitOffers(t, offers ?? []);
  const sound = soundOf(t);
  const song = sound === "music" ? songLine(t) : "";
  const lyrics = sound === "music" ? lyricsOf(t) : "";
  const plain = sound === "music" || sound === "sound";
  const media = useRef<HTMLMediaElement>(null);
  // Play from the menu, and Space while the grid (or nothing) has the focus: plays or pauses the take shown
  useEffect(() => {
    if (!play) return;
    void media.current?.play().catch(() => undefined);
    onPlayed?.();
  }, [play, onPlayed]);
  useEffect(() => {
    if (t.kind === "image") return;
    const onKey = (e: KeyboardEvent) => {
      const el = e.target as HTMLElement;
      if (e.key !== " " || !(el === document.body || el.closest('[role="list"]'))) return;
      const m = media.current;
      if (!m) return;
      e.preventDefault();
      if (m.paused) void m.play().catch(() => undefined);
      else m.pause();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [t.kind]);
  return (
    <>
      {t.kind === "audio" ? (
        // docs/mock/CreateMusicSound.dc.html frame 6: the cover or orb large, the player under it
        <div className="flex shrink-0 flex-col gap-2">
          <Thumb take={t} imgClassName="block aspect-video w-full rounded-[18px_18px_18px_8px]" />
          <audio
            ref={media as React.Ref<HTMLAudioElement>}
            key={t.id}
            src={url}
            controls
            className="w-full"
          />
        </div>
      ) : (
        <div className="relative flex aspect-video shrink-0 items-center justify-center overflow-hidden rounded-[18px_18px_18px_8px] text-[13px] text-t2">
          <Media
            key={t.id}
            take={t}
            url={url}
            videoRef={media as React.Ref<HTMLVideoElement>}
            className="max-h-full max-w-full rounded-[10px] object-contain"
          />
          <button
            type="button"
            aria-label="Full screen"
            title="Full screen (F)"
            onClick={onFull}
            className="absolute right-2 bottom-2 grid size-8 place-items-center rounded-lg bg-s3 text-t1"
          >
            <Maximize aria-hidden size={16} strokeWidth={1.75} />
          </button>
        </div>
      )}

      <div className="flex flex-col gap-1.5">
        <h2 className="heading">{m ? "Made with" : t.source === "tool" ? "Edited" : "Uploaded"}</h2>
        {m?.prompt && <p className="m-0 text-[13px] break-words">{m.prompt}</p>}
        {m && <Row label="Template" value={templateLine(m)} />}
        {m?.host && <Row label="Host" value={m.host} />}
        {!!m?.looks?.length && (
          <Row
            label="Looks"
            value={m.looks
              .map((x) => {
                const g = looks?.find((l) => l.id === x.id)?.group;
                return g ? `${x.name} (${lookGroupLabel(g).toLowerCase()})` : x.name;
              })
              .join(" · ")}
          />
        )}
        {!!m?.loras?.length && (
          <Row label="LoRAs" value={m.loras.map((l) => madeWithLine(l, loras)).join("\n")} />
        )}
        {!m && <Row label="File" value={`${t.content_type} · ${mb(t.bytes)} MB`} />}
        {size && <Row label={m?.seed != null ? "Size · seed" : "Size"} value={size} />}
        {m?.render_s != null && <Row label="Render time" value={renderTime(m.render_s)} />}
        {t.video && <Row label="Length" value={videoLine(t.video)} />}
        {song ? (
          <Row label="Song" value={song} />
        ) : (
          t.audio && (
            <Row
              label="Kind · length"
              value={`${t.source === "upload" ? `Upload · ${t.content_type === "audio/mpeg" ? "mp3" : "wav"}` : sound === "sound" ? "Sound" : "Voice"} · ${t.audio.duration_s.toFixed(1)} s`}
            />
          )
        )}
      </div>

      {lyrics && (
        <details className="flex flex-col gap-1.5">
          <summary className="heading cursor-pointer">Lyrics</summary>
          <p className="mono m-0 mt-1.5 text-[12px] break-words whitespace-pre-line text-t2">
            {lyrics}
          </p>
        </details>
      )}

      {(t.parents.length > 0 || used.length > 0 || !!m?.elements?.length) && (
        <div className="flex flex-col gap-1.5">
          <h2 className="heading">Lineage</h2>
          {t.parents.length > 0 && (
            <div className="flex justify-between gap-2.5 text-[13px]">
              <span>Made from</span>
              <RefThumbs ids={t.parents} onPick={onPick} />
            </div>
          )}
          {!!m?.elements?.length && (
            <Row
              label={t.parents.length ? "" : "Elements"}
              value={m.elements.map((e) => `@${e.handle}`).join(" · ")}
            />
          )}
          {used.length > 0 && (
            <div className="flex justify-between gap-2.5 text-[13px]">
              <span className="shrink-0">Used in</span>
              <span className="mono flex flex-wrap justify-end gap-x-1 text-right text-[12px] text-t2">
                {used.map((c, i) => (
                  <span key={c.id}>
                    <button
                      type="button"
                      className="underline-offset-2 hover:text-t1 hover:underline"
                      onClick={() => onPick(c)}
                    >
                      {c.title}
                    </button>
                    {i < used.length - 1 && " ·"}
                  </span>
                ))}
              </span>
            </div>
          )}
        </div>
      )}

      {!!rest.length && (
        <div className="flex flex-col gap-2">
          <h2 className="heading flex items-center">
            Use in…
            <Info>
              The same actions appear on this take wherever it shows up: under a stage, in a
              pipeline step, on a timeline clip. Every image, video or audio input in the app is
              picked from here.
            </Info>
          </h2>
          {rest.map((o, i) => (
            <Link
              key={`${o.workflow_id} ${o.param}`}
              href={`/?${session}use=${encodeURIComponent(t.id)}&w=${encodeURIComponent(o.workflow_id)}`}
              className={`act ${i === 0 && t.kind === "image" ? "bg-a1 font-semibold text-on1" : ""}`}
            >
              {offerLabel(o)}
            </Link>
          ))}
        </div>
      )}

      {onEdit && (
        // alpha.2 item 11 (docs/mock/TakeActions.dc.html): Edit under Use in…, while Media tools runs
        <button type="button" className="act" onClick={onEdit}>
          Edit
        </button>
      )}

      {t.kind === "video" && (
        // API 1.37 (docs/mock/H3Options.dc.html frame 7): a video's first or last frame as another workflow's input
        <div className="flex flex-col gap-2">
          <FrameIn
            takeId={t.id}
            start={frameStart}
            href={(id, o) =>
              `/?${session}use=${encodeURIComponent(id)}&w=${encodeURIComponent(o.workflow_id)}`
            }
          />
        </div>
      )}

      {!!edits.length && (
        // a music or sound take's edits (frame 6): Repaint, Cover or Vary, Extend open Create with the offer
        <div className="grid grid-cols-3 gap-2">
          {edits.map((o) => (
            <Link
              key={o.workflow_id}
              href={`/?${session}use=${encodeURIComponent(t.id)}&w=${encodeURIComponent(o.workflow_id)}`}
              className="act"
            >
              {o.action}
            </Link>
          ))}
        </div>
      )}

      <div className="grid grid-cols-2 gap-2">
        {/* an audio take goes on an audio track (API 1.32) */}
        <AddToTimeline key={t.id} takeIds={[t.id]} audio={t.kind === "audio"} />
        {m && (
          <Link href={`/?${session}reuse=${encodeURIComponent(t.id)}`} className="act">
            Reuse settings
          </Link>
        )}
        <button type="button" className="act" aria-pressed={t.starred} onClick={onStar}>
          {t.starred ? "Unstar" : "Star"}
        </button>
        <button type="button" className="act" onClick={onRename}>
          Rename
        </button>
        {t.kind === "image" && (
          <button type="button" className="act" onClick={onMakeElement}>
            Save as element
          </button>
        )}
        {m && !plain && (
          <button type="button" className="act" onClick={onSaveLook}>
            Save as look
          </button>
        )}
        {m && !plain && (
          <button
            type="button"
            className="act"
            disabled={saving?.id === t.id && !saving.error}
            onClick={() => void saveAsPipeline()}
          >
            Save as pipeline
          </button>
        )}
        <a href={url} download={t.title} className="act">
          Export file
        </a>
        <button type="button" className="act text-bad" onClick={onDelete}>
          Delete
        </button>
      </div>
      {saveError && <ErrorNote>{saveError}</ErrorNote>}
    </>
  );
}

/** A label and its mono value (the mock's .row). */
function Row({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex justify-between gap-2.5 text-[13px]">
      <span>{label}</span>
      <span className="mono text-right text-[12px] break-words whitespace-pre-line text-t2">
        {value}
      </span>
    </div>
  );
}

/** The rename dialog's form: the title, trimmed; blank can't be saved. */
function RenameForm({
  title,
  onSave,
  onCancel,
}: {
  title: string;
  onSave: (title: string) => Promise<void>;
  onCancel: () => void;
}) {
  const [value, setValue] = useState(title);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  return (
    <form
      className="flex flex-col gap-3"
      onSubmit={async (e) => {
        e.preventDefault();
        setBusy(true);
        setError("");
        try {
          await onSave(value.trim());
        } catch (err) {
          setError(message(err));
        } finally {
          setBusy(false);
        }
      }}
    >
      <label htmlFor="take-title" className="label">
        Title
      </label>
      <input
        id="take-title"
        className="in"
        value={value}
        maxLength={200}
        autoFocus
        onFocus={(e) => e.target.select()}
        onChange={(e) => setValue(e.target.value)}
      />
      {error && <ErrorNote>{error}</ErrorNote>}
      <div className="flex justify-end gap-2">
        <button type="button" className="chip bevel" onClick={onCancel}>
          Cancel
        </button>
        <button
          type="submit"
          className="chip bevel bg-a1 font-semibold text-on1"
          disabled={busy || !value.trim()}
        >
          Save
        </button>
      </div>
    </form>
  );
}
