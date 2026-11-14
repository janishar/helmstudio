"use client";
// Gallery: every take of the user (docs/mock/Gallery.dc.html, plan 9.1). Search and chips filter the list (the API
// ANDs them), pages load as the grid scrolls, images upload from the picker or by dropping them on the grid, and the
// side panel shows the selected take with the actions that exist so far.
import Link from "next/link";
import { useCallback, useEffect, useRef, useState } from "react";
import { Check, Maximize, Star, X } from "lucide-react";
import { FullScreen, Media, useFullScreenKey } from "@/components/media";
import { ErrorNote, Modal } from "@/components/kit";
import { api, message, ok, type S } from "@/lib/api";
import { safeUrl } from "@/lib/format";
import {
  UPLOAD_TYPES,
  appendPage,
  fits,
  sizeSeed,
  subLine,
  takeQuery,
  templateLine,
  uploadFile,
  type Filter,
} from "@/lib/takes";

type Take = S<"Take">;
type Upload = { id: number; name: string; progress: number; error?: string };

/** A page of the grid. */
const PAGE = 60;

const KINDS: [S<"TakeKind">, string][] = [
  ["image", "Images"],
  ["video", "Video"],
  ["audio", "Audio"],
];
const SOURCES: [S<"TakeSource">, string][] = [
  ["create", "From Create"],
  ["upload", "Uploads"],
];

/** A filter chip: pressed is the light chip of the mock. */
function Chip({
  pressed,
  onClick,
  children,
}: {
  pressed: boolean;
  onClick: () => void;
  children: React.ReactNode;
}) {
  return (
    <button
      type="button"
      aria-pressed={pressed}
      onClick={onClick}
      className="chip bevel aria-pressed:bg-t1 aria-pressed:font-semibold aria-pressed:text-panel"
    >
      {children}
    </button>
  );
}

const del = (id: string) =>
  ok(api.DELETE("/v1/studio/takes/{take_id}", { params: { path: { take_id: id } } }));
const patch = (id: string, body: S<"TakePatch">) =>
  ok(api.PATCH("/v1/studio/takes/{take_id}", { params: { path: { take_id: id } }, body }));

/** The Gallery screen. */
export function Gallery() {
  const [filter, setFilter] = useState<Filter>({});
  const [q, setQ] = useState("");
  const [query, setQuery] = useState("");
  const [takes, setTakes] = useState<Take[] | null>(null);
  const [next, setNext] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [currentId, setCurrentId] = useState<string | null>(null);
  const [selecting, setSelecting] = useState(false);
  const [picked, setPicked] = useState<Set<string>>(new Set());
  const [uploads, setUploads] = useState<Upload[]>([]);
  const [dragging, setDragging] = useState(false);
  const [modal, setModal] = useState<"rename" | "delete" | "bulk-delete" | null>(null);
  const [actionError, setActionError] = useState("");
  const [full, setFull] = useState(false);
  const fileInput = useRef<HTMLInputElement>(null);
  const sentinel = useRef<HTMLDivElement>(null);
  const seq = useRef(0);
  const uploadSeq = useRef(0);

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
          params: { query: { ...takeQuery(filter, query, cursor), limit: PAGE } },
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
    [filter, query],
  );
  // a new filter or search: the first page replaces the list (the old one stays until it comes)
  // eslint-disable-next-line react-hooks/set-state-in-effect -- the list is fetched: loading is set as it starts
  useEffect(() => fetchPage(null), [fetchPage]);

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

  const current = takes?.find((t) => t.id === currentId) ?? (selecting ? undefined : takes?.[0]);
  useFullScreenKey(!!current && !modal && !full, () => setFull(true));
  const replace = (t: Take) => setTakes((list) => list && list.map((x) => (x.id === t.id ? t : x)));
  const remove = (ids: string[]) =>
    setTakes((list) => list && list.filter((x) => !ids.includes(x.id)));

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
      setUploads((list) => [...list, { id, name: f.name, progress: 0 }]);
      uploadFile(f, (progress) => set({ progress }))
        .then((t) => {
          setUploads((list) => list.filter((x) => x.id !== id));
          if (fits(t, filter) && !query.trim())
            setTakes((list) => list && [t, ...list.filter((x) => x.id !== t.id)]);
          setCurrentId(t.id);
        })
        .catch((e) => set({ error: message(e) }));
    }
  }

  async function bulk(f: (id: string) => Promise<unknown>, after: (ids: string[]) => void) {
    const ids = [...picked];
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

  const toggle = (id: string) =>
    setPicked((s) => {
      const n = new Set(s);
      if (!n.delete(id)) n.add(id);
      return n;
    });
  const filtered = !!(filter.kind || filter.starred || filter.source || query.trim());

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
          <label htmlFor="q" className="sr-only">
            Search prompts
          </label>
          <input
            id="q"
            type="search"
            placeholder="Search prompts and titles"
            value={q}
            maxLength={200}
            onChange={(e) => setQ(e.target.value)}
            className="h-10 flex-[1_1_260px] rounded-xl border border-s5 bg-s1 px-3.5 text-t1"
          />
          <input
            ref={fileInput}
            type="file"
            accept={UPLOAD_TYPES.join(",")}
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
            pressed={!filter.kind && !filter.starred && !filter.source}
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

        {actionError && <ErrorNote>{actionError}</ErrorNote>}
        {error && (
          <ErrorNote onRetry={() => fetchPage(takes?.length ? next : null)}>{error}</ErrorNote>
        )}

        <div
          role="list"
          aria-label="Takes"
          aria-busy={loading}
          className="grid grid-cols-[repeat(auto-fill,minmax(190px,1fr))] gap-3.5"
        >
          {uploads.map((u) => (
            <div key={u.id} role="listitem" className="flex flex-col gap-1.5">
              <i
                className={`relative flex aspect-[16/10] flex-col items-center justify-center gap-1 rounded-[10px] border border-dashed border-dash bg-s1 p-2 text-center text-[11px] not-italic ${u.error ? "text-bad" : "text-t2"}`}
              >
                {u.error ? (
                  <>
                    <span role="alert">{u.error}</span>
                    <button
                      type="button"
                      aria-label={`Dismiss ${u.name}`}
                      className="absolute top-1.5 right-1.5 grid size-6 place-items-center rounded-full bg-s3 text-t1"
                      onClick={() => setUploads((list) => list.filter((x) => x.id !== u.id))}
                    >
                      <X size={12} aria-hidden />
                    </button>
                  </>
                ) : (
                  <>
                    <span>Uploading {Math.round(u.progress * 100)}%</span>
                    <span
                      role="progressbar"
                      aria-label={`Uploading ${u.name}`}
                      aria-valuemin={0}
                      aria-valuemax={100}
                      aria-valuenow={Math.round(u.progress * 100)}
                      className="block h-1 w-3/5 rounded-[2px] bg-s5"
                    >
                      <span
                        className="block h-1 rounded-[2px] bg-a1"
                        style={{ width: `${u.progress * 100}%` }}
                      />
                    </span>
                  </>
                )}
              </i>
              <b className="truncate text-[12px] font-semibold">{u.name}</b>
              <small className="mono text-[10px] text-t2">Upload</small>
            </div>
          ))}
          {takes?.map((t) => {
            const on = selecting ? picked.has(t.id) : t.id === current?.id;
            return (
              <div key={t.id} role="listitem" className="min-w-0">
                <button
                  type="button"
                  aria-pressed={on}
                  onClick={() => (selecting ? toggle(t.id) : setCurrentId(t.id))}
                  className="bevel flex w-full flex-col gap-1.5 rounded-[10px] bg-transparent text-left"
                >
                  <span className="relative block w-full">
                    {t.kind === "image" ? (
                      <img
                        src={safeUrl(t.thumb_url)}
                        alt=""
                        loading="lazy"
                        className={`block aspect-[16/10] w-full rounded-[10px] bg-s1 object-cover ${on ? "outline-2 -outline-offset-2 outline-a1" : ""}`}
                      />
                    ) : (
                      <i
                        className={`flex aspect-[16/10] items-center justify-center rounded-[10px] bg-s1 text-[11px] text-t2 not-italic ${on ? "border-2 border-a1" : "border border-dashed border-dash"}`}
                      >
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
                  <b className="truncate text-[12px] font-semibold">{t.title}</b>
                  <small className="mono truncate text-[10px] text-t2">{subLine(t)}</small>
                </button>
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
        {current ? (
          <TakePanel
            take={current}
            onStar={() =>
              void act(async () => replace(await patch(current.id, { starred: !current.starred })))
            }
            onRename={() => setModal("rename")}
            onDelete={() => setModal("delete")}
            onFull={() => setFull(true)}
          />
        ) : (
          <p className="text-[13px] text-t2">
            {selecting
              ? "Pick takes to star or delete them together."
              : "Pick a take to see how it was made."}
          </p>
        )}
      </aside>
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
}: {
  take: Take;
  onStar: () => void;
  onRename: () => void;
  onDelete: () => void;
  onFull: () => void;
}) {
  const url = safeUrl(t.url);
  const m = t.made_with;
  const size = sizeSeed(t);
  return (
    <>
      <div className="relative flex aspect-video items-center justify-center overflow-hidden rounded-[18px_18px_18px_8px] text-[13px] text-t2">
        <Media take={t} url={url} className="max-h-full max-w-full rounded-[10px] object-contain" />
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

      <div className="flex flex-col gap-1.5">
        <h2 className="heading">{m ? "Made with" : "Uploaded"}</h2>
        {m?.prompt && <p className="m-0 text-[13px] break-words">{m.prompt}</p>}
        {m && <Row label="Template" value={templateLine(m)} />}
        {!m && <Row label="File" value={`${t.content_type} · ${mb(t.bytes)}`} />}
        {size && <Row label={m?.seed != null ? "Size · seed" : "Size"} value={size} />}
      </div>

      <div className="grid grid-cols-2 gap-2">
        {m && (
          <Link
            href={`/?${t.session_id ? `s=${encodeURIComponent(t.session_id)}&` : ""}reuse=${encodeURIComponent(t.id)}`}
            className="act"
          >
            Reuse settings
          </Link>
        )}
        <button type="button" className="act" aria-pressed={t.starred} onClick={onStar}>
          {t.starred ? "Unstar" : "Star"}
        </button>
        <button type="button" className="act" onClick={onRename}>
          Rename
        </button>
        <a href={url} download={t.title} className="act">
          Export file
        </a>
        <button type="button" className="act text-bad" onClick={onDelete}>
          Delete
        </button>
      </div>
    </>
  );
}

const mb = (n: number) =>
  n >= 1e6 ? `${(n / 1e6).toFixed(1)} MB` : `${Math.max(1, Math.round(n / 1e3))} KB`;

/** A label and its mono value (the mock's .row). */
function Row({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex justify-between gap-2.5 text-[13px]">
      <span>{label}</span>
      <span className="mono text-right text-[12px] break-words text-t2">{value}</span>
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
