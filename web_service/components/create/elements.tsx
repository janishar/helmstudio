"use client";
// Elements (docs/mock/Elements.dc.html): the composer's Elements button, the picker (a grid by group with search,
// insert at the caret, New element, edit, delete with confirm) and the editor (name, handle, group, text, 1-4
// images from the gallery picker). Saved through /v1/studio/library.
import { useEffect, useRef, useState } from "react";
import { Mic, Pencil, UsersRound, X } from "lucide-react";
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
  useVoices,
} from "@/lib/library";
import { HANDLE, handleFrom } from "@/lib/mentions";
import { VoicePicker, type VoiceCtx } from "@/components/create/voices";
import { voiceLine } from "@/lib/voices";
import { TextArea } from "@/components/expand-text";

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
      {big !== "card" && (
        <span aria-hidden className={`${picClass(big)} grid place-items-center bg-line text-t2`}>
          <UsersRound size={big ? 16 : 13} strokeWidth={1.75} />
        </span>
      )}
      <TileWords big={big} label="Elements" value={count ? `${count} in prompt` : "Add"} />
    </button>
  );
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
}: {
  onClose: () => void;
  onInsert: (handle: string) => void;
  inPrompt: string[];
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
      search={{
        id: "el-q",
        label: "Search names and handles",
        placeholder: "Search names and @handles",
        value: q,
        onChange: setQ,
        className: "flex-[1_1_220px]",
      }}
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
      grid="grid-cols-1 gap-2.5 sm:grid-cols-2"
      footer={
        <>
          <p className="m-0 text-[12px] text-t2">
            Click an element to put its @handle in the prompt at the caret.
            {lib ? ` ${lib.length + ownLooks} of 200.` : ""}
          </p>
          {editor}
        </>
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
                  loading="lazy"
                  decoding="async"
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
                <small className="truncate text-[12px] text-t2">
                  {deleting === e.id
                    ? `Delete @${e.handle}? Takes made with it keep it.`
                    : `${e.name} · ${groupLabel(e.group)}${on ? " · in this prompt" : ""}${n ? "" : " · its images were deleted"}`}
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
  handle,
  auto,
  onSaved,
}: {
  open: boolean;
  onClose: () => void;
  item?: Item;
  takes?: S<"Take">[];
  /** the handle to start from (the assistant's Save as element, prefilled) */
  handle?: string;
  /** Auto mode: a new element saves itself and closes */
  auto?: boolean;
  /** the element its Save made or changed */
  onSaved?: (x: Item) => void;
}) {
  return (
    <Modal
      open={open}
      onClose={onClose}
      title={item ? "Edit element" : "New element"}
      width="w-[min(94vw,600px)]"
    >
      <Editor
        key={item?.id ?? "new"}
        item={item}
        takes={takes}
        handle={handle}
        auto={auto && !item}
        onSaved={onSaved}
        onClose={onClose}
      />
    </Modal>
  );
}

const MAX_IMAGES = 4;
const fromTakes = (ts: S<"Take">[]): Img[] =>
  ts.map((t) => ({ take_id: t.id, thumb_url: t.thumb_url }));

function Editor({
  item,
  takes,
  handle: h0,
  auto,
  onSaved,
  onClose,
}: {
  item?: Item;
  takes?: S<"Take">[];
  handle?: string;
  auto?: boolean;
  onSaved?: (x: Item) => void;
  onClose: () => void;
}) {
  const [name, setName] = useState(item?.name ?? h0 ?? "");
  const [handle, setHandle] = useState(item?.handle ?? h0 ?? "");
  const [touched, setTouched] = useState(!!item || !!h0);
  const [group, setGroup] = useState<S<"LibraryGroup">>(item?.group ?? "character");
  const [text, setText] = useState(item?.text ?? "");
  const [images, setImages] = useState<Img[]>(
    item?.images ?? fromTakes((takes ?? []).filter((t) => t.kind === "image").slice(0, MAX_IMAGES)),
  );
  const [picking, setPicking] = useState(false);
  // a character's voice (API 1.32): who speaks its script lines
  const [voiceId, setVoiceId] = useState<string | null>(item?.voice_id ?? null);
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
  async function save(e?: React.FormEvent) {
    e?.preventDefault();
    if (problem || busy) return;
    setBusy(true);
    setError("");
    const body = {
      group,
      name: name.trim(),
      handle: h,
      text: text.trim(),
      images: images.map((i) => ({ take_id: i.take_id })),
      // PUT replaces it: only a character has one
      voice_id: group === "character" ? voiceId : null,
    };
    try {
      const x = await ok(
        item
          ? api.PUT("/v1/studio/library/{item_id}", {
              params: { path: { item_id: item.id } },
              body: { ...body, version: item.version },
            })
          : api.POST("/v1/studio/library", { body: { ...body, kind: "element" } }),
      );
      await reloadLibrary();
      onSaved?.(x);
      onClose();
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  }
  // Auto mode (API 1.68): saves itself once, as soon as it opens; a problem leaves it open for you
  const ran = useRef(false);
  useEffect(() => {
    if (!auto || ran.current) return;
    ran.current = true;
    void save();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [auto]);
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
              <img
                loading="lazy"
                decoding="async"
                src={safeUrl(im.thumb_url)}
                alt=""
                className="size-full object-cover"
              />
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
      {group === "character" && (
        <ElementVoice
          value={voiceId}
          onChange={setVoiceId}
          handle={h}
          name={name.trim() || "Name"}
        />
      )}
      <label className="label flex flex-col gap-1.5">
        <span className="flex justify-between">
          Text
          <span className="mono">{[...text.trim()].length} / 2000</span>
        </span>
        <TextArea
          expand="Text"
          aria-label="Text"
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

/** The voice picker outside a workflow: every voice, none greyed (no fit to read). */
const ANY: VoiceCtx = {
  workflow: { id: "", title: "", family: "", nodes: {}, node_schemas: {} },
  familyTitle: (f) => f,
};

/** A character's Voice tile (docs/mock/VoicePicker.dc.html, Edit element): its voice with Change and remove, else
 * Pick a voice; in a dialogue script its @handle speaks in it. */
function ElementVoice({
  value,
  onChange,
  handle,
  name,
}: {
  value: string | null;
  onChange: (id: string | null) => void;
  handle: string;
  name: string;
}) {
  const [open, setOpen] = useState(false);
  const voices = useVoices();
  const v = value ? voices?.find((x) => x.id === value) : undefined;
  const fam = v?.voice?.family ?? v?.family;
  const sub = [fam && `${fam} ${v?.group}`, v && v.voice?.source !== "designed" && voiceLine(v)]
    .filter(Boolean)
    .join(" · ");
  return (
    <div className="label flex flex-col gap-1.5">
      <span className="flex items-center">
        Voice
        <Info>
          Characters only. In a dialogue script, each @handle line is spoken in its element&apos;s
          voice. A deleted voice leaves the element with none.
        </Info>
      </span>
      {value ? (
        <div className="flex items-center gap-2 rounded-xl bg-s3 px-3 py-1.5 text-t1">
          <span aria-hidden className="wave h-7 w-11 shrink-0 rounded-md" />
          <span className="flex min-w-0 flex-1 flex-col">
            <small className="mono text-[10px] text-t2">Voice</small>
            <b className="truncate text-[13px] font-semibold">
              {name} · {v ? v.name : voices ? "gone" : "…"}
            </b>
            {sub && <small className="truncate text-[11px] text-t2">{sub}</small>}
          </span>
          <button
            type="button"
            aria-haspopup="dialog"
            className="chip h-7 rounded-lg"
            onClick={() => setOpen(true)}
          >
            Change
          </button>
          <button
            type="button"
            aria-label="Remove the voice"
            className="grid size-7 place-items-center rounded-lg text-t2"
            onClick={() => onChange(null)}
          >
            <X size={16} aria-hidden />
          </button>
        </div>
      ) : (
        <button
          type="button"
          aria-haspopup="dialog"
          onClick={() => setOpen(true)}
          className="flex h-12 items-center justify-center gap-2 rounded-xl border border-dashed border-dash text-[13px] text-t1"
        >
          <Mic size={16} aria-hidden />
          Pick a voice
        </button>
      )}
      <p className="m-0 text-[12px] text-t2">
        In a dialogue script, @{handle} speaks in this voice:
      </p>
      <p className="mono m-0 rounded-[10px] bg-s1 px-2.5 py-2 text-[12px] leading-[18px] text-t1">
        @{handle} (quiet): You said you&apos;d be back by noon.
      </p>
      <VoicePicker
        open={open}
        onClose={() => setOpen(false)}
        value={value ?? ""}
        onPick={(id) => onChange(id)}
        ctx={ANY}
      />
    </div>
  );
}
