"use client";
// Import from Gallery, as a copy (MagicBoard frame 6; plan 39 phase E): Takes (the Gallery's grid) or Elements (the
// library's, searched). Takes join an asset's sheet, or make a new asset; an Element becomes an asset with its
// pictures, voice and words copied in. The board keeps its own copy: later changes in the Gallery don't reach it.
import { useEffect, useState } from "react";
import { Modal } from "@/components/kit";
import { Picker } from "@/components/gallery/picker";
import { groupLabel, reloadLibrary, useLibrary } from "@/lib/library";
import { fromElement, fromTakes } from "@/lib/magic-assets";
import type { Act } from "@/components/magic/menus";
import { freshAssetId } from "@/lib/magic";
import type { S } from "@/lib/api";

export function ImportDialog({
  act: a,
  into,
  open,
  onClose,
  onMade,
}: {
  act: Act;
  /** the asset the picks join; none: a new asset */
  into?: string;
  open: boolean;
  onClose: () => void;
  /** a new asset was made (the page opens it) */
  onMade: (id: string) => void;
}) {
  const [tab, setTab] = useState<"takes" | "elements">("takes");
  const asset = into ? a.doc.assets.find((x) => x.id === into) : undefined;
  const takes = (picked: S<"Take">[]) => {
    if (!picked.length) return;
    if (asset) {
      const sheet = [
        ...asset.sheet,
        ...picked.map((t) => t.id).filter((t) => !asset.sheet.includes(t)),
      ];
      a.edit({ op: "asset", id: asset.id, set: { sheet: sheet.slice(0, 50) } });
      return;
    }
    const id = freshAssetId(a.doc);
    if (a.edit({ op: "asset_add", asset: { id, ...fromTakes(a.doc, picked) } }) !== false)
      onMade(id);
  };
  const element = (e: S<"LibraryItem">) => {
    if (asset) {
      const add = e.images.map((i) => i.take_id).filter((t) => !asset.sheet.includes(t));
      a.edit({
        op: "asset",
        id: asset.id,
        set: {
          sheet: [...asset.sheet, ...add].slice(0, 50),
          ...(e.voice_id && !asset.voice_id ? { voice_id: e.voice_id } : {}),
        },
      });
    } else {
      const id = freshAssetId(a.doc);
      if (a.edit({ op: "asset_add", asset: { id, ...fromElement(a.doc, e) } }) !== false)
        onMade(id);
    }
    onClose();
  };
  return (
    <Modal
      open={open}
      onClose={onClose}
      title={asset ? `Import into @${asset.handle}` : "Import from Gallery"}
      aside="as a copy"
      width="w-[min(94vw,880px)]"
    >
      <div className="flex flex-col gap-3">
        <div role="group" aria-label="From" className="seg self-start">
          {(["takes", "elements"] as const).map((t) => (
            <button key={t} type="button" aria-pressed={tab === t} onClick={() => setTab(t)}>
              {t === "takes" ? "Takes" : "Elements"}
            </button>
          ))}
        </div>
        {tab === "takes" ? (
          <Picker
            max={asset ? 12 : 6}
            onClose={onClose}
            onPick={takes}
            kind="image"
            action="Import"
          />
        ) : (
          <Elements onPick={element} onClose={onClose} />
        )}
        <p className="m-0 text-[11px] text-t2">
          The board keeps its own copy: later changes in the Gallery don&apos;t reach it.
        </p>
      </div>
    </Modal>
  );
}

/** The library's Elements, searched by @handle or name; one picked is imported. */
function Elements({
  onPick,
  onClose,
}: {
  onPick: (e: S<"LibraryItem">) => void;
  onClose: () => void;
}) {
  const lib = useLibrary();
  useEffect(() => void reloadLibrary(), []);
  const [q, setQ] = useState("");
  const [picked, setPicked] = useState<S<"LibraryItem"> | null>(null);
  const query = q.trim().replace(/^@/, "").toLowerCase();
  const shown = (lib ?? []).filter(
    (e) =>
      e.kind === "element" &&
      (!query || e.handle?.startsWith(query) || e.name.toLowerCase().includes(query)),
  );
  return (
    <div className="flex h-[76dvh] flex-col gap-3">
      <input
        type="search"
        aria-label="Search elements"
        placeholder="Search · mara"
        value={q}
        onChange={(e) => setQ(e.target.value)}
        className="h-9 rounded-xl border border-s5 bg-s1 px-3 text-t1"
      />
      <div
        role="listbox"
        aria-label="Elements"
        className="grid min-h-40 flex-1 content-start grid-cols-[repeat(auto-fill,minmax(150px,1fr))] gap-2.5 overflow-y-auto p-0.5"
      >
        {lib === null && <p className="text-t2">Loading…</p>}
        {lib && !shown.length && (
          <p className="text-t2">
            {query ? "No element matches." : "No elements yet: make some in Create."}
          </p>
        )}
        {shown.map((e) => (
          <button
            key={e.id}
            type="button"
            role="option"
            aria-selected={picked?.id === e.id}
            className={`flex flex-col gap-1 rounded-xl bg-s2 p-2 text-left text-[12px] ${picked?.id === e.id ? "shadow-[inset_0_0_0_2px_var(--a1)]" : ""}`}
            onClick={() => setPicked(e)}
            onDoubleClick={() => onPick(e)}
          >
            <span
              className="aspect-square w-full rounded-lg bg-s3 bg-cover bg-center"
              style={e.thumb_url ? { backgroundImage: `url("${e.thumb_url}")` } : undefined}
            />
            <b className="truncate">@{e.handle}</b>
            <span className="truncate text-t2">
              {groupLabel(e.group)} · {e.images.length} picture{e.images.length === 1 ? "" : "s"}
              {e.voice_id ? " · voice" : ""}
            </span>
          </button>
        ))}
      </div>
      <div className="flex flex-wrap items-center justify-end gap-2">
        <span className="mono mr-auto text-[12px] text-t2" aria-live="polite">
          {picked ? `1 picked · @${picked.handle}` : "Pick an element"}
        </span>
        <button type="button" className="chip bevel" onClick={onClose}>
          Cancel
        </button>
        <button
          type="button"
          className="chip bevel bg-a1 font-semibold text-on1"
          disabled={!picked}
          onClick={() => picked && onPick(picked)}
        >
          Import
        </button>
      </div>
    </div>
  );
}
