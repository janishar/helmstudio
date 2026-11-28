"use client";
// Looks (docs/mock/Looks.dc.html): the composer's tiles (Elements and one per look group, from `studio.quick` or the
// default), the Looks picker (the shared visual picker: groups, starter vs mine, greyed with the reason, Auto clears a
// group, edit and delete one's own), the look editor (New look, ✎) and the gallery's Save as look.
import { useEffect, useRef, useState } from "react";
import { Camera, Film, Lightbulb, Palette, Pencil, X, type LucideIcon } from "lucide-react";
import { ErrorNote, Info, Modal } from "@/components/kit";
import {
  ElementsButton,
  TileWords,
  picClass,
  tileClass,
  type TileSize,
} from "@/components/create/elements";
import { LoraPicker } from "@/components/create/loras";
import { Pic, Tile, VisualPicker } from "@/components/create/visual-picker";
import { GalleryPicker } from "@/components/gallery/picker";
import { api, message, ok, type S } from "@/lib/api";
import { safeUrl } from "@/lib/format";
import { looksError, reloadLibrary, useLooks } from "@/lib/library";
import { useLoras } from "@/lib/lora-library";
import {
  LOOK_GROUPS,
  MAX_LOOKS,
  filterItems,
  liveChosen,
  lookGroupLabel,
  lookLine,
  tileGroup,
  tileState,
  type Chosen,
  type Tile as TileName,
} from "@/lib/looks";
import { TextArea } from "@/components/expand-text";
import { Range } from "@/components/range";

type Item = S<"LibraryItem">;
type Group = S<"LibraryGroup">;

/** One look group's composer tile: the first chosen look's picture and name, or Auto. */
function LookTile({
  group,
  chosen,
  looks,
  open,
  card,
  onClick,
}: {
  group: Group;
  chosen: Chosen;
  looks: Item[] | null;
  /** the tiles open their group in the page (the empty Create screen): whether this one is open */
  open?: boolean;
  /** a video workflow's tile: the label over the value, no picture (docs/mock/TextToVideo.dc.html) */
  card?: boolean;
  onClick: () => void;
}) {
  const st = tileState(group, chosen, looks);
  const src = safeUrl(st.thumb);
  const label = LOOK_GROUPS.find(([g]) => g === group)?.[1] ?? group;
  const big: TileSize = card ? "card" : open !== undefined;
  return (
    <button
      type="button"
      {...(big ? { "aria-expanded": open } : { "aria-haspopup": "dialog" as const })}
      aria-label={`${label}: ${st.name}`}
      onClick={onClick}
      className={`${tileClass(big)} ${open ? "outline-2 outline-a1" : ""}`}
    >
      {card ? null : src ? (
        <img
          loading="lazy"
          decoding="async"
          src={src}
          alt=""
          className={`${picClass(big)} object-cover`}
        />
      ) : (
        <span
          aria-hidden
          className={`${picClass(big)} grid place-items-center text-t2 ${st.count ? "bg-line" : "border border-dashed border-dash"}`}
        >
          <GroupIcon group={group} big={big} />
        </span>
      )}
      <TileWords big={big} label={label} value={st.name} />
    </button>
  );
}

const GROUP_ICONS: Record<string, LucideIcon> = {
  film: Film,
  camera: Camera,
  colour: Palette,
  lighting: Lightbulb,
};

/** The look group's icon inside a tile's picture slot while it has no picture. */
function GroupIcon({ group, big }: { group: string; big: TileSize }) {
  const Icon = GROUP_ICONS[group];
  return Icon ? <Icon size={big ? 16 : 13} strokeWidth={1.75} /> : null;
}

/** The composer's quick tiles in order: Elements (`elements`) and the look groups. */
export function QuickTiles({
  tiles,
  chosen,
  mentioned,
  onElements,
  onLook,
  open,
  card,
}: {
  tiles: TileName[];
  chosen: Chosen;
  /** distinct elements the prompt mentions */
  mentioned: number;
  onElements: () => void;
  onLook: (g: Group) => void;
  /** set when the look tiles open their group in the page, not the picker (the empty Create screen): the open one */
  open?: Group | null;
  /** a video workflow's tiles (docs/mock/TextToVideo.dc.html) */
  card?: boolean;
}) {
  const looks = useLooks();
  const big: TileSize = card ? "card" : open !== undefined;
  return tiles.map((t) => {
    const g = tileGroup(t);
    return g ? (
      <LookTile
        key={t}
        group={g}
        chosen={chosen}
        looks={looks}
        open={open !== undefined ? g === open : undefined}
        card={card}
        onClick={() => onLook(g)}
      />
    ) : (
      <ElementsButton key={t} count={mentioned} onClick={onElements} big={big} />
    );
  });
}

/** The looks of one group in the page, under the composer of the empty Create screen (docs/mock/Direct.dc.html): the
 * picker's grid, where a pick applies at once; Reset clears every group's looks, Save current as look starts a look
 * from the prompt as typed. */
export function LooksSection({
  group,
  chosen,
  onChosen,
  reason,
  prompt,
}: {
  group: Group;
  chosen: Chosen;
  onChosen: (c: Chosen) => void;
  reason: (x: Item) => string | null;
  prompt: string;
}) {
  return (
    <section
      aria-label={`${lookGroupLabel(group)} looks`}
      className="panel w-full max-w-[980px] shrink-0 self-center p-4"
    >
      <Picker
        start={group}
        chosen={chosen}
        onClose={() => undefined}
        onChosen={onChosen}
        reason={reason}
        inline={{ prompt }}
      />
    </section>
  );
}

/** The Looks picker, opened on `group`; `onChosen` gets every group's picks when the user applies them. `reason`:
 * why a look does not fit the current workflow (null: it fits). */
export function LooksPicker({
  group,
  onClose,
  chosen,
  onChosen,
  reason,
}: {
  group: Group | null;
  onClose: () => void;
  chosen: Chosen;
  onChosen: (c: Chosen) => void;
  reason: (x: Item) => string | null;
}) {
  return (
    <Modal open={!!group} onClose={onClose} title="Looks" width="w-[min(94vw,880px)]">
      {group && (
        <Picker
          start={group}
          chosen={chosen}
          onClose={onClose}
          onChosen={onChosen}
          reason={reason}
        />
      )}
    </Modal>
  );
}

function Picker({
  start,
  chosen,
  onClose,
  onChosen,
  reason,
  inline,
}: {
  start: Group;
  chosen: Chosen;
  onClose: () => void;
  onChosen: (c: Chosen) => void;
  reason: (x: Item) => string | null;
  /** in the page (LooksSection): `start` is the group, picks apply at once; `prompt` fills Save current as look */
  inline?: { prompt: string };
}) {
  const looks = useLooks();
  // the list may be stale (another tab, a host came or went): read it again on each opening
  useEffect(() => void reloadLibrary(), []);
  const [tab, setGroup] = useState<Group>(start);
  const group = inline ? start : tab;
  const [source, setSource] = useState<S<"LibrarySource"> | "">("");
  const [q, setQ] = useState("");
  const [local, setLocal] = useState<Chosen>(chosen);
  const picks = inline ? chosen : local;
  const setPicks = (f: (p: Chosen) => Chosen) => (inline ? onChosen(f(chosen)) : setLocal(f));
  // a look being edited, "new", or "current" (Save current as look: a new look with the prompt as its text)
  const [editing, setEditing] = useState<Item | "new" | "current" | null>(null);
  const [deleting, setDeleting] = useState("");
  const [error, setError] = useState("");
  const shown = filterItems(looks ?? [], { group, source, q });
  // a look deleted or gone from every live host while the picker is open is not counted nor kept
  const live = liveChosen(picks, looks);
  const mine = live[group] ?? [];
  const total = Object.values(live).flat().length;
  const set = (ids: string[]) => setPicks((p) => ({ ...p, [group]: ids }));
  const toggle = (id: string) =>
    set(mine.includes(id) ? mine.filter((x) => x !== id) : [...mine, id]);
  async function remove(x: Item) {
    try {
      setError("");
      await ok(api.DELETE("/v1/studio/library/{item_id}", { params: { path: { item_id: x.id } } }));
      setDeleting("");
      setPicks((p) =>
        Object.fromEntries(Object.entries(p).map(([g, ids]) => [g, ids.filter((i) => i !== x.id)])),
      );
      await reloadLibrary();
    } catch (e) {
      setError(message(e));
    }
  }
  const icon = "grid size-6 place-items-center rounded-lg bg-s3 text-t2 hover:text-t1";
  const small = "chip h-6 px-2 text-[11px]";
  const info = (
    <>
      A look adds its text after the prompt, its images as references and its LoRAs to the LoRA
      stack, at its strength. Starter looks come with the models; yours you save from a take or make
      here.
    </>
  );
  const editor = (
    <LookEditor
      open={editing !== null}
      onClose={() => setEditing(null)}
      item={typeof editing === "object" ? (editing ?? undefined) : undefined}
      group={group}
      text={editing === "current" ? inline?.prompt : undefined}
    />
  );
  return (
    <VisualPicker
      head={
        inline && (
          <div className="flex flex-wrap items-center justify-between gap-2.5">
            <h2 className="m-0 flex items-center text-base font-semibold">
              {lookGroupLabel(group)}
              <Info>{info}</Info>
            </h2>
            <div className="flex gap-2">
              <button
                type="button"
                className="chip bevel"
                disabled={!total}
                title="Clear the looks of every group"
                onClick={() => onChosen({})}
              >
                Reset
              </button>
              <button type="button" className="chip bevel" onClick={() => setEditing("current")}>
                Save current as look
              </button>
            </div>
          </div>
        )
      }
      info={info}
      action={
        <button
          type="button"
          className="chip bevel bg-a1 font-semibold text-on1"
          onClick={() => setEditing("new")}
        >
          New look
        </button>
      }
      search={
        inline
          ? undefined
          : {
              id: "look-q",
              label: "Search looks",
              value: q,
              onChange: setQ,
              count: shown.length,
              className: "flex-[1_1_200px]",
            }
      }
      filters={
        inline
          ? []
          : [
              {
                label: "Group",
                kind: "tab",
                options: LOOK_GROUPS,
                value: group,
                onChange: setGroup,
              },
              {
                label: "Source",
                kind: "press",
                options: [
                  ["", "All"],
                  ["starter", "Starter"],
                  ["mine", "Mine"],
                ] as const,
                value: source,
                onChange: setSource,
              },
            ]
      }
      error={error}
      status={
        <>
          {looks === null &&
            (looksError() ? (
              <ErrorNote onRetry={() => void reloadLibrary()}>{looksError()}</ErrorNote>
            ) : (
              <p role="status" className="text-t2">
                Loading…
              </p>
            ))}
          {looks && !shown.length && (
            <p className="py-8 text-center text-t2">
              {q || source
                ? "No look matches."
                : "No looks in this group yet: make one with New look."}
            </p>
          )}
        </>
      }
      listLabel="Looks"
      grid={
        inline
          ? "grid-cols-2 gap-3 sm:grid-cols-4"
          : "grid-cols-[repeat(auto-fill,minmax(130px,1fr))] gap-3"
      }
      footer={
        inline ? (
          editor
        ) : (
          <>
            <div className="flex flex-wrap items-center justify-end gap-2">
              <span className="mono mr-auto text-[12px] text-t2" aria-live="polite">
                {total} chosen · up to {MAX_LOOKS} in all
              </span>
              <button type="button" className="chip bevel" onClick={onClose}>
                Cancel
              </button>
              <button
                type="button"
                className="chip bevel bg-a1 font-semibold text-on1"
                onClick={() => {
                  onChosen(live);
                  onClose();
                }}
              >
                {total ? `Use ${total}` : "Use none"}
              </button>
            </div>
            <p className="m-0 text-[12px] text-t2">
              Looks that do not fit this workflow are greyed with the reason. Starter looks are
              read-only: save your own from a take. Auto adds nothing.
            </p>
            {editor}
          </>
        )
      }
    >
      {looks && (
        <li className="flex min-w-0 flex-col gap-1.5">
          <Tile
            pic={<Pic thumb={null} empty="no look" />}
            name="Auto"
            line="nothing added"
            pressed={!mine.length}
            onClick={() => set([])}
          />
        </li>
      )}
      {shown.map((x) => {
        const why = reason(x);
        const n = mine.indexOf(x.id);
        const own = x.source !== "starter";
        return (
          <li key={x.id} className="relative flex min-w-0 flex-col gap-1.5">
            <Tile
              pic={<Pic thumb={x.thumb_url} empty="no picture" n={n} />}
              name={x.name}
              line={deleting === x.id ? "" : (why ?? lookLine(x))}
              reason={why}
              pressed={n >= 0}
              disabled={n < 0 && total >= MAX_LOOKS}
              onClick={() => toggle(x.id)}
            />
            {deleting === x.id && (
              <>
                <small className="text-[11px]">Delete {x.name}? Takes made with it keep it.</small>
                <small className="flex gap-1">
                  <button type="button" className={small} onClick={() => setDeleting("")}>
                    Cancel
                  </button>
                  <button
                    type="button"
                    className={`${small} text-bad`}
                    onClick={() => void remove(x)}
                  >
                    Delete
                  </button>
                </small>
              </>
            )}
            {own && deleting !== x.id && (
              <span className="absolute top-1.5 right-1.5 flex gap-1">
                <button
                  type="button"
                  aria-label={`Edit ${x.name}`}
                  className={icon}
                  onClick={() => setEditing(x)}
                >
                  <Pencil size={12} aria-hidden />
                </button>
                <button
                  type="button"
                  aria-label={`Delete ${x.name}`}
                  className={icon}
                  onClick={() => setDeleting(x.id)}
                >
                  <X size={13} aria-hidden />
                </button>
              </span>
            )}
          </li>
        );
      })}
      {inline && looks && (
        <li className="flex min-w-0 flex-col gap-1.5">
          <Tile
            pic={<Pic thumb={null} empty="+" />}
            name="New look"
            line="from a take or from scratch"
            pressed={false}
            onClick={() => setEditing("new")}
          />
        </li>
      )}
    </VisualPicker>
  );
}

const MAX_IMAGES = 4;
const MAX_LORAS = 4;
type Img = { take_id: string; thumb_url: string };
type LoraRow = { id: string; name: string; strength: number };

/** The look editor: a new look (in `group`, with `text` to start from) or one's own `item`. */
export function LookEditor({
  open,
  onClose,
  item,
  group = "lighting",
  text,
  onSaved,
}: {
  open: boolean;
  onClose: () => void;
  item?: Item;
  group?: Group;
  text?: string;
  /** the look its Save made or changed */
  onSaved?: (x: Item) => void;
}) {
  return (
    <Modal
      open={open}
      onClose={onClose}
      title={item ? "Edit look" : "New look"}
      width="w-[min(94vw,600px)]"
    >
      <Editor
        key={item?.id ?? "new"}
        item={item}
        group={group}
        text={text}
        onSaved={onSaved}
        onClose={onClose}
      />
    </Modal>
  );
}

function GroupButtons({ value, onChange }: { value: Group; onChange: (g: Group) => void }) {
  return (
    <div className="label flex flex-col gap-1.5">
      Group
      <div role="group" aria-label="Group" className="flex flex-wrap gap-1.5">
        {LOOK_GROUPS.map(([g, label]) => (
          <button
            key={g}
            type="button"
            className="opt bevel px-3 text-t1"
            aria-pressed={value === g}
            onClick={() => onChange(g)}
          >
            {label}
          </button>
        ))}
      </div>
    </div>
  );
}

const box = "h-[38px] w-full rounded-[10px] bg-s4 px-3 text-[13px] text-t1";

function Editor({
  item,
  group: g0,
  text: t0 = "",
  onSaved,
  onClose,
}: {
  item?: Item;
  group: Group;
  text?: string;
  onSaved?: (x: Item) => void;
  onClose: () => void;
}) {
  const library = useLoras();
  const [name, setName] = useState(item?.name ?? "");
  const [group, setGroup] = useState<Group>(item?.group ?? g0);
  const [text, setText] = useState(item?.text ?? t0.slice(0, 2000));
  const [images, setImages] = useState<Img[]>(item?.images ?? []);
  const [loras, setLoras] = useState<LoraRow[]>(
    (item?.loras ?? []).flatMap((l) =>
      l.lora ? [{ id: l.lora.lora_id, name: l.name, strength: l.strength }] : [],
    ),
  );
  const [picking, setPicking] = useState<"images" | "loras" | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const problem = !name.trim()
    ? "Give it a name."
    : !text.trim() && !images.length && !loras.length
      ? "A look needs text, an image or a LoRA."
      : "";
  async function save(e: React.FormEvent) {
    e.preventDefault();
    if (problem || busy) return;
    setBusy(true);
    setError("");
    const body = {
      group,
      name: name.trim(),
      text: text.trim(),
      images: images.map((i) => ({ take_id: i.take_id })),
      loras: loras.map((l) => ({ lora: { lora_id: l.id }, strength: l.strength })),
    };
    try {
      const x = await ok(
        item
          ? api.PUT("/v1/studio/library/{item_id}", {
              params: { path: { item_id: item.id } },
              body: { ...body, version: item.version },
            })
          : api.POST("/v1/studio/library", { body: { ...body, kind: "look" } }),
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
  return (
    <form onSubmit={save} noValidate className="flex flex-col gap-3.5">
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
      <GroupButtons value={group} onChange={setGroup} />
      <label className="label flex flex-col gap-1.5">
        <span className="flex justify-between">
          Text
          <span className="mono">{[...text.trim()].length} / 2000</span>
        </span>
        <TextArea
          expand="Text"
          aria-label="Text"
          rows={2}
          maxLength={2000}
          value={text}
          placeholder="Words added after the prompt when the look is chosen"
          onChange={(e) => setText(e.target.value)}
          className="resize-none rounded-[10px] bg-s4 px-3 py-2 text-[13px] text-t1"
        />
      </label>
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
                onClick={() => setPicking("images")}
              >
                Add from gallery
              </button>
            </li>
          )}
        </ol>
      </div>
      <div className="label flex flex-col gap-1.5">
        <span className="flex justify-between">
          LoRAs
          <span className="mono">
            {loras.length} / {MAX_LORAS}
          </span>
        </span>
        {loras.map((l) => (
          <div
            key={l.id}
            className="flex flex-col gap-2 rounded-[10px] border border-s3 bg-s1 px-3 py-2.5 text-t1"
          >
            <div className="flex items-center justify-between gap-2">
              <span className="min-w-0 truncate">{l.name}</span>
              <span className="flex shrink-0 items-center gap-1.5">
                <span className="mono text-[12px] text-t2">{l.strength.toFixed(2)}</span>
                <button
                  type="button"
                  aria-label={`Remove ${l.name}`}
                  onClick={() => setLoras(loras.filter((x) => x.id !== l.id))}
                  className="grid size-[22px] place-items-center rounded-lg bg-s3 text-t2 hover:text-t1"
                >
                  <X size={12} aria-hidden />
                </button>
              </span>
            </div>
            <Range
              min={0}
              max={2}
              step={0.05}
              value={l.strength}
              aria-label={`Strength of ${l.name}`}
              aria-valuetext={l.strength.toFixed(2)}
              onChange={(e) =>
                setLoras(
                  loras.map((x) =>
                    x.id === l.id
                      ? { ...x, strength: Number(Number(e.target.value).toFixed(2)) }
                      : x,
                  ),
                )
              }
              className="w-full accent-a1"
            />
          </div>
        ))}
        {loras.length < MAX_LORAS && (
          <button
            type="button"
            onClick={() => setPicking("loras")}
            className="h-9 rounded-[10px] border border-dashed border-dash bg-transparent text-[13px] text-t1"
          >
            Add LoRA
          </button>
        )}
      </div>
      <p className="m-0 text-[12px] text-t2">Takes already made keep the version they used.</p>
      {error && <ErrorNote>{error}</ErrorNote>}
      {!error && problem && (name || item) && <p className="m-0 text-[12px] text-t2">{problem}</p>}
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
        open={picking === "images"}
        onClose={() => setPicking(null)}
        max={MAX_IMAGES - images.length}
        onPick={(ts) =>
          setImages((old) =>
            [
              ...old,
              ...ts
                .map((t) => ({ take_id: t.id, thumb_url: t.thumb_url }))
                .filter((t) => !old.some((o) => o.take_id === t.take_id)),
            ].slice(0, MAX_IMAGES),
          )
        }
      />
      <LoraPicker
        open={picking === "loras"}
        onClose={() => setPicking(null)}
        family={null}
        max={MAX_LORAS - loras.length}
        taken={loras.map((l) => l.id)}
        onPick={(ids) =>
          setLoras((old) =>
            [
              ...old,
              ...ids.map((id) => ({
                id,
                name: library?.find((l) => l.id === id)?.name ?? id.slice(0, 8),
                strength: 1,
              })),
            ].slice(0, MAX_LORAS),
          )
        }
      />
    </form>
  );
}

/** The gallery's Save as look: a name, a group, and what to copy from the take (its prompt and its LoRAs, which
 * studio copies; the take itself as a reference image). */
export function SaveAsLook({
  take,
  name,
  auto,
  onSaved,
  onClose,
}: {
  take: S<"Take"> | null;
  /** the name to start from (the assistant's Save as look, prefilled) */
  name?: string;
  /** Auto mode: saves itself and closes */
  auto?: boolean;
  onSaved?: (x: Item) => void;
  onClose: () => void;
}) {
  return (
    <Modal open={!!take} onClose={onClose} title="Save as look" width="w-[min(94vw,560px)]">
      {take && (
        <SaveForm
          key={take.id}
          take={take}
          name={name}
          auto={auto}
          onSaved={onSaved}
          onClose={onClose}
        />
      )}
    </Modal>
  );
}

function SaveForm({
  take: t,
  name: n0,
  auto,
  onSaved,
  onClose,
}: {
  take: S<"Take">;
  name?: string;
  auto?: boolean;
  onSaved?: (x: Item) => void;
  onClose: () => void;
}) {
  const m = t.made_with;
  const [name, setName] = useState((n0 ?? t.title).slice(0, 80));
  const [group, setGroup] = useState<Group>("lighting");
  const [prompt, setPrompt] = useState(!!m?.prompt);
  const [withLoras, setWithLoras] = useState(!!m?.loras?.length);
  const [image, setImage] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [done, setDone] = useState(false);
  // what studio copies: the first per id, at most 4 (one the caller cannot use now is left out there), strength in 0..2
  const loras = (m?.loras ?? [])
    .filter((l, i, all) => all.findIndex((x) => x.id === l.id) === i)
    .slice(0, MAX_LORAS);
  const problem = !name.trim()
    ? "Give it a name."
    : !prompt && !(withLoras && loras.length) && !image
      ? "Copy the prompt, the LoRAs or the take: a look needs one of them."
      : "";
  async function save(e?: React.FormEvent) {
    e?.preventDefault();
    if (problem || busy) return;
    setBusy(true);
    setError("");
    try {
      const x = await ok(
        api.POST("/v1/studio/library", {
          body: {
            kind: "look",
            group,
            name: name.trim(),
            ...(image && { images: [{ take_id: t.id }] }),
            from_take: { take_id: t.id, prompt, loras: withLoras },
          },
        }),
      );
      await reloadLibrary();
      onSaved?.(x);
      if (auto) onClose();
      else setDone(true);
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
  const ck = "flex items-start gap-2.5 text-[13px] text-t1";
  const sub = "mono block text-[11px] break-words text-t2";
  if (done)
    return (
      <div className="flex flex-col gap-3.5">
        <p role="status" className="m-0">
          Saved {name.trim()} in {LOOK_GROUPS.find(([g]) => g === group)?.[1]}. Choose it from the
          composer&apos;s {LOOK_GROUPS.find(([g]) => g === group)?.[1]} tile.
        </p>
        <div className="flex justify-end">
          <button type="button" className="chip bevel" onClick={onClose}>
            Close
          </button>
        </div>
      </div>
    );
  return (
    <form onSubmit={save} noValidate className="flex flex-col gap-3.5">
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
      <GroupButtons value={group} onChange={setGroup} />
      <div className="label flex flex-col gap-2">
        From this take
        <label className={ck}>
          <input
            type="checkbox"
            className="mt-[3px] accent-a1"
            checked={prompt}
            disabled={!m?.prompt}
            onChange={(e) => setPrompt(e.target.checked)}
          />
          <span>
            Its prompt, as typed
            <small className={`${sub} line-clamp-2`}>{m?.prompt || "it has no prompt"}</small>
          </span>
        </label>
        <label className={ck}>
          <input
            type="checkbox"
            className="mt-[3px] accent-a1"
            checked={withLoras}
            disabled={!loras.length}
            onChange={(e) => setWithLoras(e.target.checked)}
          />
          <span>
            Its LoRAs, at their strengths
            <small className={sub}>
              {loras.length
                ? loras
                    .map((l) => `${l.name} ${Math.min(2, Math.max(0, l.strength)).toFixed(2)}`)
                    .join(" · ")
                : "it was made without LoRAs"}
            </small>
          </span>
        </label>
        {t.kind === "image" && (
          <label className={ck}>
            <input
              type="checkbox"
              className="mt-[3px] accent-a1"
              checked={image}
              onChange={(e) => setImage(e.target.checked)}
            />
            <span>The take itself as a reference image</span>
          </label>
        )}
      </div>
      <p className="m-0 text-[12px] text-t2">
        A look is chosen from the composer&apos;s tiles: its text goes after the prompt, its LoRAs
        join the LoRA stack. The take is its picture.
      </p>
      {error && <ErrorNote>{error}</ErrorNote>}
      {!error && problem && <p className="m-0 text-[12px] text-t2">{problem}</p>}
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
    </form>
  );
}
