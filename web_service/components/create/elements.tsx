"use client";
// Elements (docs/mock/Elements.dc.html): the composer's Elements button, the picker (a grid by group with search,
// insert at the caret, New element, edit, delete with confirm) and the editor (name, handle, group, text, 1-4
// images from the gallery picker). Saved through /v1/studio/library.
import { useEffect, useState } from "react";
import { Pencil, X } from "lucide-react";
import { ErrorNote, Info, Modal } from "@/components/kit";
import { VisualPicker } from "@/components/create/visual-picker";
import { GalleryPicker } from "@/components/gallery/picker";
import { api, message, ok, type S } from "@/lib/api";
import { safeUrl } from "@/lib/format";
import {
  GROUPS,
  groupLabel,
  libraryError,
  reloadLibrary,
  useLibrary,
  useLooks,
} from "@/lib/library";
import { HANDLE, handleFrom } from "@/lib/mentions";

type Item = S<"LibraryItem">;
type Img = { take_id: string; thumb_url: string };

/** A composer tile: compact (h-8, the Stage layout), big (the empty Create screen, docs/mock/Direct.dc.html) or a
 * card (a video workflow, docs/mock/TextToVideo.dc.html: the label over the value, no picture). */
export type TileSize = boolean | "card";
export const tileClass = (big: TileSize) =>
  big === "card"
    ? "bevel flex min-h-12 flex-[1_1_130px] items-center gap-2 rounded-xl bg-s3 px-3 py-1.5 text-left"
    : big
      ? "bevel flex min-h-[52px] flex-[1_1_150px] items-center gap-2.5 rounded-xl bg-s3 px-3 py-2 text-left"
      : "bevel flex h-8 items-center gap-2 rounded-lg bg-s3 pr-2.5 pl-1 text-[12px]";
export const picClass = (big: TileSize) =>
  big === "card"
    ? "h-7 w-11 shrink-0 rounded-md"
    : big
      ? "size-8 shrink-0 rounded-lg"
      : "size-6 rounded-md";
export function TileWords({ big, label, value }: { big: TileSize; label: string; value: string }) {
  return (
    <span className={big ? "flex min-w-0 flex-col" : "contents"}>
      <span className="mono text-[10px] text-t2">{label}</span>
      <b
        className={`font-semibold ${big === "card" ? "truncate text-[13px]" : big ? "truncate text-sm" : ""}`}
      >
        {value}
      </b>
    </span>
  );
}

/** The composer's Elements button: how many distinct elements the prompt mentions; `big` on the empty Create
 * screen. A card (the video boards) reads "None" without any, and has no picture. */
export function ElementsButton({
  count,
  onClick,
  big = false,
}: {
  count: number;
  onClick: () => void;
  big?: TileSize;
}) {
  return (
    <button type="button" aria-haspopup="dialog" onClick={onClick} className={tileClass(big)}>
      {big !== "card" && <span aria-hidden className={`${picClass(big)} bg-line`} />}
      <TileWords big={big} label="Elements" value={count ? `${count} in prompt` : "Add"} />
    </button>
  );
}

/** The Elements panel in the aside of the empty Create screen (docs/mock/Direct.dc.html): the picker's cards by
 * group, without search; a click puts the @handle in the prompt. */
export function ElementsPanel({
  onInsert,
  inPrompt,
}: {
  onInsert: (handle: string) => void;
  inPrompt: string[];
}) {
  return <Picker onClose={() => undefined} onInsert={onInsert} inPrompt={inPrompt} inline />;
}

/** The Elements picker; `onInsert` puts a handle in the prompt (the dialog closes); `inPrompt` marks the cards. */
export function ElementsPicker({
  open,
  onClose,
  onInsert,
  inPrompt,
}: {
  open: boolean;
  onClose: () => void;
  onInsert: (handle: string) => void;
  inPrompt: string[];
}) {
  return (
    <Modal open={open} onClose={onClose} title="Elements" width="w-[min(94vw,700px)]">
      <Picker onClose={onClose} onInsert={onInsert} inPrompt={inPrompt} />
    </Modal>
  );
}

function Picker({
  onClose,
  onInsert,
  inPrompt,
  inline = false,
}: {
  onClose: () => void;
  onInsert: (handle: string) => void;
  inPrompt: string[];
  inline?: boolean;
}) {
  const lib = useLibrary();
  // elements and own looks share the 200 library items
  const ownLooks = useLooks()?.filter((x) => x.source === "mine").length ?? 0;
  // the list may be stale (another tab): read it again on each opening
  useEffect(() => void reloadLibrary(), []);
  const [group, setGroup] = useState<S<"LibraryGroup"> | "">("");
  const [q, setQ] = useState("");
  const [editing, setEditing] = useState<Item | "new" | null>(null);
  const [deleting, setDeleting] = useState("");
  const [error, setError] = useState("");
  const query = q.trim().replace(/^@/, "").toLowerCase();
  const shown = (lib ?? []).filter(
    (e) =>
      (!group || e.group === group) &&
      (!query || e.handle?.startsWith(query) || e.name.toLowerCase().startsWith(query)),
  );
  async function remove(e: Item) {
    try {
      await ok(api.DELETE("/v1/studio/library/{item_id}", { params: { path: { item_id: e.id } } }));
      setDeleting("");
      await reloadLibrary();
    } catch (err) {
      setError(message(err));
    }
  }
  const info = (
    <>
      An element is a name, 1 to 4 images from your gallery and optional text. Type @ in any prompt
      to use it: it adds its images as references where the workflow takes them, and its text after
      the prompt.
    </>
  );
  const editor = (
    <ElementEditor
      open={editing !== null}
      onClose={() => setEditing(null)}
      item={editing === "new" ? undefined : (editing ?? undefined)}
    />
  );
  return (
    <VisualPicker
      head={
        inline ? (
          <div className="flex items-center justify-between gap-2">
            <h2 className="m-0 font-display text-base font-bold tracking-[0.03em]">Elements</h2>
            <button type="button" className="chip bevel" onClick={() => setEditing("new")}>
              New element
            </button>
          </div>
        ) : undefined
      }
      info={info}
      action={
        <button
          type="button"
          className="chip bevel bg-a1 font-semibold text-on1"
          onClick={() => setEditing("new")}
        >
          New element
        </button>
      }
      search={
        inline
          ? undefined
          : {
              id: "el-q",
              label: "Search names and handles",
              placeholder: "Search names and @handles",
              value: q,
              onChange: setQ,
              className: "flex-[1_1_220px]",
            }
      }
      filters={[
        {
          label: "Group",
          kind: "tab",
          options: [["", "All"], ...GROUPS.map(([g, , plural]) => [g, plural] as const)],
          value: group,
          onChange: setGroup,
        },
      ]}
      error={error}
      status={
        <>
          {lib === null &&
            (libraryError() ? (
              <ErrorNote onRetry={() => void reloadLibrary()}>{libraryError()}</ErrorNote>
            ) : (
              <p role="status" className="text-t2">
                Loading…
              </p>
            ))}
          {lib?.length === 0 && (
            <p className="py-8 text-center text-t2">
              No elements yet. Make one from images of your gallery with New element.
            </p>
          )}
          {!!lib?.length && !shown.length && (
            <p className="py-8 text-center text-t2">No element matches.</p>
          )}
        </>
      }
      listLabel="Elements"
      grid={inline ? "grid-cols-1 gap-2.5" : "grid-cols-1 gap-2.5 sm:grid-cols-2"}
      footer={
        inline ? (
          <span>
            <Info>{info}</Info>
            {editor}
          </span>
        ) : (
          <>
            <p className="m-0 text-[12px] text-t2">
              Click an element to put its @handle in the prompt at the caret.
              {lib ? ` ${lib.length + ownLooks} of 200.` : ""}
            </p>
            {editor}
          </>
        )
      }
    >
      {shown.map((e) => {
        const on = !!e.handle && inPrompt.includes(e.handle);
        const n = e.images.length;
        return (
          <li
            key={e.id}
            className={`flex items-center gap-3 rounded-[18px_18px_18px_8px] border p-2.5 ${on ? "border-a1 bg-s2" : "border-s5 bg-s1"}`}
          >
            <button
              type="button"
              data-pick
              className="flex min-w-0 flex-1 items-center gap-3 text-left"
              title={`Put @${e.handle} in the prompt`}
              onClick={() => {
                if (e.handle) onInsert(e.handle);
                onClose();
              }}
            >
              {e.thumb_url ? (
                <img
                  src={safeUrl(e.thumb_url)}
                  alt=""
                  className="size-16 shrink-0 rounded-[10px] object-cover"
                />
              ) : (
                <span className="grid size-16 shrink-0 place-items-center rounded-[10px] border border-dashed border-dash text-[10px] text-t2">
                  no image
                </span>
              )}
              <span className="flex min-w-0 flex-col gap-0.5">
                <b className={`mono truncate font-medium ${on ? "text-a1" : ""}`}>@{e.handle}</b>
                <small className={`text-[12px] text-t2 ${inline ? "" : "truncate"}`}>
                  {deleting === e.id
                    ? `Delete @${e.handle}? Takes made with it keep it.`
                    : `${inline ? "" : `${e.name} · `}${groupLabel(e.group)}${on ? " · in this prompt" : ""}${n ? "" : " · its images were deleted"}`}
                </small>
              </span>
            </button>
            {deleting === e.id ? (
              <span className="flex gap-1.5">
                <button type="button" className="chip h-7" onClick={() => setDeleting("")}>
                  Cancel
                </button>
                <button type="button" className="chip h-7 text-bad" onClick={() => void remove(e)}>
                  Delete
                </button>
              </span>
            ) : (
              <span className="flex flex-col gap-1">
                <button
                  type="button"
                  aria-label={`Edit @${e.handle}`}
                  className="grid size-7 place-items-center rounded-lg bg-s3 text-t2"
                  onClick={() => setEditing(e)}
                >
                  <Pencil size={13} aria-hidden />
                </button>
                <button
                  type="button"
                  aria-label={`Delete @${e.handle}`}
                  className="grid size-7 place-items-center rounded-lg bg-s3 text-t2"
                  onClick={() => setDeleting(e.id)}
                >
                  <X size={14} aria-hidden />
                </button>
              </span>
            )}
          </li>
        );
      })}
    </VisualPicker>
  );
}

/** The element editor: a new element (from `takes` when the gallery's Save as element opened it) or `item`. */
export function ElementEditor({
  open,
  onClose,
  item,
  takes,
}: {
  open: boolean;
  onClose: () => void;
  item?: Item;
  takes?: S<"Take">[];
}) {
  return (
    <Modal
      open={open}
      onClose={onClose}
      title={item ? "Edit element" : "New element"}
      width="w-[min(94vw,600px)]"
    >
      <Editor key={item?.id ?? "new"} item={item} takes={takes} onClose={onClose} />
    </Modal>
  );
}

const MAX_IMAGES = 4;
const fromTakes = (ts: S<"Take">[]): Img[] =>
  ts.map((t) => ({ take_id: t.id, thumb_url: t.thumb_url }));

function Editor({
  item,
  takes,
  onClose,
}: {
  item?: Item;
  takes?: S<"Take">[];
  onClose: () => void;
}) {
  const [name, setName] = useState(item?.name ?? "");
  const [handle, setHandle] = useState(item?.handle ?? "");
  const [touched, setTouched] = useState(!!item);
  const [group, setGroup] = useState<S<"LibraryGroup">>(item?.group ?? "character");
  const [text, setText] = useState(item?.text ?? "");
  const [images, setImages] = useState<Img[]>(
    item?.images ?? fromTakes((takes ?? []).filter((t) => t.kind === "image").slice(0, MAX_IMAGES)),
  );
  const [picking, setPicking] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const h = touched ? handle : handleFrom(name);
  const problem = !name.trim()
    ? "Give it a name."
    : !HANDLE.test(h)
      ? "The handle is a-z, 0-9, - and _, starting with a letter, ending with a letter or digit, at most 40 characters."
      : !images.length
        ? "Add 1 to 4 images from the gallery."
        : "";
  async function save(e: React.FormEvent) {
    e.preventDefault();
    if (problem || busy) return;
    setBusy(true);
    setError("");
    const body = {
      group,
      name: name.trim(),
      handle: h,
      text: text.trim(),
      images: images.map((i) => ({ take_id: i.take_id })),
    };
    try {
      await ok(
        item
          ? api.PUT("/v1/studio/library/{item_id}", {
              params: { path: { item_id: item.id } },
              body: { ...body, version: item.version },
            })
          : api.POST("/v1/studio/library", { body: { ...body, kind: "element" } }),
      );
      await reloadLibrary();
      onClose();
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  }
  const box = "h-[38px] w-full rounded-[10px] bg-s4 px-3 text-[13px] text-t1";
  return (
    <form onSubmit={save} noValidate className="flex flex-col gap-3.5">
      <div className="grid grid-cols-1 gap-2.5 sm:grid-cols-2">
        <label className="label flex flex-col gap-1.5">
          Name
          <input
            className={box}
            value={name}
            maxLength={80}
            autoFocus
            onChange={(e) => setName(e.target.value)}
          />
        </label>
        <label className="label flex flex-col gap-1.5">
          Handle
          <span className="relative">
            <span aria-hidden className="mono absolute top-[9px] left-3 text-[13px] text-t2">
              @
            </span>
            <input
              className={`${box} mono pl-7`}
              value={h}
              maxLength={40}
              spellCheck={false}
              onChange={(e) => {
                setTouched(true);
                setHandle(e.target.value.toLowerCase());
              }}
            />
          </span>
        </label>
      </div>
      <div className="label flex flex-col gap-1.5">
        Group
        <div role="group" aria-label="Group" className="flex gap-1.5">
          {GROUPS.map(([g, label]) => (
            <button
              key={g}
              type="button"
              className="opt bevel px-3 text-t1"
              aria-pressed={group === g}
              onClick={() => setGroup(g)}
            >
              {label}
            </button>
          ))}
        </div>
      </div>
      <div className="label flex flex-col gap-1.5">
        <span className="flex justify-between">
          Images
          <span className="mono">
            {images.length} / {MAX_IMAGES}
          </span>
        </span>
        <ol aria-label="Images, in order" className="m-0 grid list-none grid-cols-4 gap-1.5 p-0">
          {images.map((im, i) => (
            <li
              key={im.take_id}
              className="relative aspect-square overflow-hidden rounded-lg bg-s1"
            >
              <img src={safeUrl(im.thumb_url)} alt="" className="size-full object-cover" />
              <button
                type="button"
                aria-label={`Remove image ${i + 1}`}
                className="absolute top-1 right-1 grid size-5 place-items-center rounded-full bg-s4 text-[12px] text-t1"
                onClick={() => setImages(images.filter((x) => x.take_id !== im.take_id))}
              >
                ×
              </button>
            </li>
          ))}
          {images.length < MAX_IMAGES && (
            <li>
              <button
                type="button"
                className="aspect-square w-full rounded-lg border border-dashed border-dash text-[12px] text-t2"
                onClick={() => setPicking(true)}
              >
                Add from gallery
              </button>
            </li>
          )}
        </ol>
      </div>
      <label className="label flex flex-col gap-1.5">
        <span className="flex justify-between">
          Text
          <span className="mono">{[...text.trim()].length} / 2000</span>
        </span>
        <textarea
          rows={3}
          maxLength={2000}
          value={text}
          placeholder="Optional: words added after the prompt where it is mentioned"
          onChange={(e) => setText(e.target.value)}
          className="resize-y rounded-[10px] bg-s4 px-3 py-2 text-[13px] text-t1"
        />
      </label>
      <p className="m-0 text-[12px] text-t2">
        The text is added after the prompt as “{name.trim() || "Name"}: …”. Takes already made keep
        the version they used.
      </p>
      {error && <ErrorNote>{error}</ErrorNote>}
      {!error && problem && (name || touched || item) && (
        <p className="m-0 text-[12px] text-t2">{problem}</p>
      )}
      <div className="flex items-center justify-end gap-2">
        <button type="button" className="chip bevel" onClick={onClose}>
          Cancel
        </button>
        <button
          type="submit"
          className="chip bevel bg-a1 font-semibold text-on1"
          disabled={!!problem || busy}
        >
          {busy ? "Saving…" : "Save"}
        </button>
      </div>
      <GalleryPicker
        open={picking}
        onClose={() => setPicking(false)}
        max={MAX_IMAGES - images.length}
        onPick={(ts) =>
          setImages((old) =>
            [
              ...old,
              ...fromTakes(ts).filter((t) => !old.some((o) => o.take_id === t.take_id)),
            ].slice(0, MAX_IMAGES),
          )
        }
      />
    </form>
  );
}
