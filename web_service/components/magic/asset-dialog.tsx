"use client";
// An asset opened (MagicBoard frame 6; plan 39 phase E): left, its sheet (views and expressions: made here with Make
// views, imported from the Gallery as a copy, or a file), its voice as an orb to play or change, a note; right, where
// it is used, lit on a small board. "new" makes one first (kind, name, handle). Each view has its right-click menu.
import { useRef, useState } from "react";
import { Images, Mic, Play, Upload } from "lucide-react";
import { openMenu } from "@/components/context-menu";
import { ErrorNote, Modal } from "@/components/kit";
import { KIND_NAME } from "@/components/magic/icons";
import { assetMenu, viewMenu, type Act } from "@/components/magic/menus";
import { message } from "@/lib/api";
import { useVoices } from "@/lib/library";
import {
  ASSET_KIND_NAME,
  VIEWS,
  freeHandle,
  missingViews,
  viewName,
} from "@/lib/magic-assets";
import { ASSET_KINDS, HANDLE, duration, end, freshAssetId, usedIn, type Asset, type AssetKind } from "@/lib/magic";
import { estText } from "@/lib/magic-fixture";
import { kindOf, packSeconds, type AssetRun, type Kinds } from "@/lib/magic-standard";
import { UPLOAD_TYPES, uploadFile } from "@/lib/takes";
import { voiceLine } from "@/lib/voices";

const KIND_CLS = { character: "k-char", prop: "k-prop", place: "k-place" } as const;

export function AssetDialog({
  act,
  id,
  kinds,
  runs,
  gallery,
  onImport,
  onOpen,
  onClose,
}: {
  act: Act;
  /** the asset, or "new" */
  id: string | null;
  kinds: Kinds | null;
  runs: AssetRun[];
  /** the Gallery and uploads are there */
  gallery: boolean;
  onImport: (asset?: string) => void;
  /** a block it is used in, to select on the board */
  onOpen: (block: string) => void;
  onClose: () => void;
}) {
  const [made, setMade] = useState<string | null>(null);
  const shownId = id === "new" ? made : id;
  const asset = shownId ? act.doc.assets.find((a) => a.id === shownId) : undefined;
  const close = () => {
    setMade(null);
    onClose();
  };
  return (
    <Modal
      open={!!id}
      onClose={close}
      title={asset ? `${ASSET_KIND_NAME[asset.kind]} · @${asset.handle}` : "New asset"}
      aside={asset ? "this board's own" : undefined}
      width={asset ? "w-[min(96vw,1040px)]" : "w-[min(94vw,460px)]"}
    >
      {asset ? (
        <Body
          key={asset.id}
          act={act}
          x={asset}
          kinds={kinds}
          runs={runs.filter((r) => r.asset === asset.id)}
          gallery={gallery}
          onImport={onImport}
          onOpen={(b) => {
            onOpen(b);
            close();
          }}
        />
      ) : (
        id === "new" && <NewAsset act={act} onMade={setMade} onCancel={close} />
      )}
    </Modal>
  );
}

/** A new asset: its kind, name and @ handle (a free one proposed from the name). */
function NewAsset({
  act: a,
  onMade,
  onCancel,
}: {
  act: Act;
  onMade: (id: string) => void;
  onCancel: () => void;
}) {
  const [kind, setKind] = useState<AssetKind>("character");
  const [name, setName] = useState("");
  const [handle, setHandle] = useState<string | null>(null);
  const h = handle ?? freeHandle(a.doc, name || "asset");
  const bad = !name.trim()
    ? "Give it a name."
    : !HANDLE.test(h)
      ? "A handle starts with a letter: letters, digits, - or _."
      : a.doc.assets.some((x) => x.handle.toLowerCase() === h.toLowerCase())
        ? "That handle is taken."
        : "";
  const make = () => {
    const id = freshAssetId(a.doc);
    if (!bad && a.edit({ op: "asset_add", asset: { id, kind, name: name.trim(), handle: h } }) !== false)
      onMade(id);
  };
  return (
    <form
      className="mg-add flex flex-col gap-3"
      onSubmit={(e) => {
        e.preventDefault();
        make();
      }}
    >
      <div className="fld">
        <span className="lb">Kind</span>
        <div className="seg" role="group" aria-label="Kind">
          {ASSET_KINDS.map((k) => (
            <button key={k} type="button" aria-pressed={kind === k} onClick={() => setKind(k)}>
              {ASSET_KIND_NAME[k]}
            </button>
          ))}
        </div>
      </div>
      <label className="fld">
        <span className="lb">Name</span>
        <input
          className="in h-9 px-2.5 text-[13px]"
          value={name}
          maxLength={80}
          autoFocus
          placeholder={kind === "place" ? "Bus stop" : kind === "prop" ? "Red umbrella" : "Mara"}
          onChange={(e) => setName(e.target.value)}
        />
      </label>
      <label className="fld">
        <span className="lb">Handle</span>
        <input
          className="in mono h-9 px-2.5 text-[13px]"
          value={`@${h}`}
          maxLength={41}
          onChange={(e) => setHandle(e.target.value.replace(/^@/, "").toLowerCase())}
        />
        <span className="text-[11px] text-t2">What @ picks it by in a prompt.</span>
      </label>
      {name && bad && <span className="text-[12px] text-bad">{bad}</span>}
      <div className="flex items-center gap-2">
        <span className="grow" />
        <button type="button" className="h-9 rounded-[10px] px-3.5 text-[13px] font-medium text-t2" onClick={onCancel}>
          Cancel
        </button>
        <button type="submit" className="key h-9 rounded-xl px-3.5 text-[13px]" disabled={!!bad}>
          Make asset
        </button>
      </div>
    </form>
  );
}

function Body({
  act: a,
  x,
  kinds,
  runs,
  gallery,
  onImport,
  onOpen,
}: {
  act: Act;
  x: Asset;
  kinds: Kinds | null;
  runs: AssetRun[];
  gallery: boolean;
  onImport: (asset?: string) => void;
  onOpen: (block: string) => void;
}) {
  const d = a.doc;
  const voices = useVoices();
  const voice = voices?.find((v) => v.id === x.voice_id);
  const used = usedIn(d, x.id);
  const file = useRef<HTMLInputElement>(null);
  const [upload, setUpload] = useState<string | null>(null);
  const [error, setError] = useState("");
  const running = runs.filter((r) => (r.status === "queued" || r.status === "running") && !r.takes.length);
  const failed = runs.findLast((r) => r.status === "failed" && !r.takes.length);
  const want = missingViews(x, running.map((r) => r.view));
  const image = kindOf(kinds, "image")?.models[0];
  const side = Math.min(d.target.width, d.target.height, 1024);
  const est = packSeconds(
    image,
    { kind: "image", length: 1, request: { kind: "image", width: side, height: side } },
    d,
  );
  const set = (s: { voice_id?: string | null; note?: string; name?: string }) =>
    a.edit({ op: "asset", id: x.id, set: s });
  async function addFile(f: File) {
    setError("");
    setUpload("Optimising…");
    try {
      const t = await uploadFile(f, (p) => setUpload(p.step === "uploading" ? "Uploading…" : "Optimising…"));
      a.edit({ op: "asset", id: x.id, set: { sheet: [...x.sheet, t.id].slice(0, 50) } });
    } catch (e) {
      setError(message(e));
    } finally {
      setUpload(null);
    }
  }
  return (
    <div className="mg-add flex flex-col gap-3">
      <div className="flex flex-wrap items-center gap-2 text-[12px] text-t2">
        <span className={`kb ${KIND_CLS[x.kind]}`}>{ASSET_KIND_NAME[x.kind]}</span>
        <b className="text-t1">{x.name}</b>
        <span>
          · {x.sheet.length} view{x.sheet.length === 1 ? "" : "s"}
          {x.imported_from ? " · imported, a copy" : ""}
        </span>
        <span className="grow" />
        <button
          type="button"
          className="chip bevel rounded-[10px] bg-s4"
          onClick={() => {
            const name = prompt("Asset name", x.name)?.trim();
            if (name && name !== x.name) set({ name });
          }}
        >
          Rename
        </button>
        <button
          type="button"
          className="chip bevel rounded-[10px] bg-s4"
          aria-label="More"
          onClick={(e) => openMenu(e, assetMenu(a, x))}
        >
          ⋯
        </button>
      </div>
      {error && <ErrorNote>{error}</ErrorNote>}
      <div className="grid gap-4 md:grid-cols-[minmax(0,1.25fr)_minmax(0,1fr)]">
        <section className="flex min-w-0 flex-col gap-3" aria-label="Sheet">
          <h3 className="m-0 text-[13px]">Sheet</h3>
          <div className="grid grid-cols-[repeat(auto-fill,minmax(112px,1fr))] gap-2" role="list" aria-label="Views">
            {x.sheet.map((t, i) => (
              <button
                key={t}
                type="button"
                role="listitem"
                aria-label={viewName(x, t)}
                className="flex flex-col gap-1 rounded-xl bg-s2 p-1.5 text-left text-[11px]"
                onClick={(e) => openMenu(e, viewMenu(a, x, t))}
                onContextMenu={(e) => openMenu(e, viewMenu(a, x, t))}
              >
                <span
                  className="aspect-square w-full rounded-lg bg-s3 bg-cover bg-center"
                  style={{ backgroundImage: a.takes[t]?.art }}
                />
                <span className="truncate">
                  {viewName(x, t)}
                  {i === 0 ? " · first" : ""}
                </span>
              </button>
            ))}
            {running.map((r) => (
              <div key={r.run_id} className="flex flex-col gap-1 rounded-xl bg-s2 p-1.5 text-[11px]" role="listitem">
                <span className="grid aspect-square w-full place-items-center rounded-lg bg-s3">
                  <span className="mst run">{r.progress ?? 0}%</span>
                </span>
                <span className="truncate">{r.view} · making</span>
              </div>
            ))}
            {upload && (
              <div className="grid aspect-square place-items-center rounded-xl bg-s2 text-[11px] text-t2" role="status">
                {upload}
              </div>
            )}
          </div>
          {!x.sheet.length && !running.length && (
            <p className="m-0 text-[12px] text-t2">No views yet: make them here, import a take or an Element, or add a file.</p>
          )}
          {failed && <span className="text-[12px] text-bad">{failed.view} failed: {failed.error ?? "failed"}</span>}
          <div className="flex flex-wrap items-center gap-2">
            <button
              type="button"
              className="chip bevel rounded-[10px] bg-s4"
              disabled={!a.views || !image}
              title={!a.views ? (a.runOff ?? "Runs come with the standard request") : !image ? "No image model on any host" : `Makes ${want.join(", ")}`}
              onClick={(e) =>
                openMenu(e, {
                  title: "Make views",
                  items: [
                    ...VIEWS[x.kind].map((v) => ({
                      label: v,
                      keys: est == null ? undefined : estText(est, true),
                      off: running.some((r) => r.view === v) ? "Being made" : undefined,
                      onPick: () => a.views?.(x.id, [v]),
                    })),
                    {
                      label: "Another view…",
                      keys: est == null ? undefined : estText(est, true),
                      onPick: () => {
                        const v = prompt("A view or an expression", x.kind === "character" ? "Smile" : "Top")
                          ?.trim()
                          .slice(0, 40);
                        if (v) a.views?.(x.id, [v]);
                      },
                    },
                    "-",
                    {
                      label: `Make ${want.length === VIEWS[x.kind].length ? "all" : "the missing"} ${want.length}`,
                      keys: est == null ? undefined : estText(est * want.length, true),
                      off: want.length ? undefined : "Being made",
                      onPick: () => a.views?.(x.id, want),
                    },
                  ],
                })
              }
            >
              <Images size={13} aria-hidden />
              Make views{est != null ? ` · ${estText(est * want.length)}` : ""}
            </button>
            <button
              type="button"
              className="chip bevel rounded-[10px] bg-s4"
              disabled={!gallery}
              title={gallery ? "A take or an Element, as a copy" : "The Gallery is in studio"}
              onClick={() => onImport(x.id)}
            >
              Import from Gallery
            </button>
            <button
              type="button"
              className="chip bevel rounded-[10px] bg-s4"
              disabled={!gallery || !!upload}
              title={gallery ? "PNG, JPEG or WebP" : "Uploads are in studio"}
              onClick={() => file.current?.click()}
            >
              <Upload size={13} aria-hidden />A file
            </button>
            <input
              ref={file}
              type="file"
              hidden
              accept={UPLOAD_TYPES.join(",")}
              aria-label="Add a file to the sheet"
              onChange={(e) => {
                const f = e.target.files?.[0];
                e.target.value = "";
                if (f) void addFile(f);
              }}
            />
          </div>
          {x.kind === "character" && (
            <div className="flex flex-col gap-1.5">
              <h3 className="m-0 text-[13px]">Voice</h3>
              <div className="flex min-w-0 items-center gap-2 rounded-xl bg-s3 px-2.5 py-1.5 text-[12px]">
                <span className="mo k8v" aria-hidden>
                  <Mic size={12} />
                </span>
                <b className="min-w-0 truncate">{voice ? voice.name : x.voice_id ? x.voice_id : "No voice"}</b>
                {voice && <span className="mono min-w-0 truncate">{voiceLine(voice)}</span>}
                <span className="grow" />
                <button
                  type="button"
                  className="chip h-7 rounded-[9px] bg-s4 px-2.5 text-[12px]"
                  disabled={!voice?.thumb_url}
                  title={voice?.thumb_url ? "Play its sample" : "No sample to play"}
                  onClick={() => voice?.thumb_url && void new Audio(voice.thumb_url).play().catch(() => undefined)}
                >
                  <Play size={11} aria-hidden />
                  Play
                </button>
                <select
                  aria-label="Change voice"
                  className="in h-7 max-w-40 px-1.5 text-[12px]"
                  value={x.voice_id ?? ""}
                  onChange={(e) => set({ voice_id: e.target.value || null })}
                >
                  <option value="">No voice</option>
                  {x.voice_id && !voice && <option value={x.voice_id}>{x.voice_id}</option>}
                  {(voices ?? []).map((v) => (
                    <option key={v.id} value={v.id}>
                      {v.name}
                    </option>
                  ))}
                </select>
              </div>
              <span className="text-[11px] text-t2">Voice blocks that pick @{x.handle} speak in it.</span>
            </div>
          )}
          <label className="flex flex-col gap-1.5">
            <h3 className="m-0 text-[13px]">Note</h3>
            <textarea
              aria-label="Note"
              className="in min-h-14 px-2.5 py-2 text-[13px]"
              defaultValue={x.note ?? ""}
              placeholder="What it looks like: Make views and the Assistant read it"
              onBlur={(e) => e.target.value !== (x.note ?? "") && set({ note: e.target.value })}
            />
          </label>
        </section>
        <section className="flex min-w-0 flex-col gap-2.5" aria-label="Used in">
          <h3 className="m-0 text-[13px]">
            Used in · <span className="mono">{used.length}</span>
          </h3>
          <div className="flex flex-wrap gap-1.5">
            {used.map((b) => (
              <button key={b.id} type="button" className="rc big lk" onClick={() => onOpen(b.id)}>
                {b.name.split(" · ")[0]}
              </button>
            ))}
            {!used.length && <span className="text-[12px] text-t2">Not used yet: pick @{x.handle} in a prompt.</span>}
          </div>
          <MiniBoard act={a} lit={new Set(used.map((b) => b.id))} />
          <p className="m-0 text-[11px] text-t2">
            Only the asset → frame → first frame chain keeps @{x.handle} looking the same.
          </p>
        </section>
      </div>
    </div>
  );
}

/** The board small (frame 6, right): its lanes with the blocks it is used in lit. */
function MiniBoard({ act: a, lit }: { act: Act; lit: Set<string> }) {
  const d = a.doc;
  const total = Math.max(1, duration(d));
  return (
    <div className="flex flex-col gap-1 rounded-xl bg-s2 p-2" aria-label="Where it is used, on the board" role="img">
      {d.lanes
        .filter((l) => !l.hidden)
        .map((l) => (
          <div key={l.id} className="flex items-center gap-2">
            <span className="w-16 shrink-0 truncate text-[10px] text-t2">{l.name}</span>
            <span className="relative h-3.5 grow rounded bg-s3">
              {d.blocks
                .filter((b) => b.lane === l.id)
                .map((b) => (
                  <span
                    key={b.id}
                    title={`${KIND_NAME[b.kind]} · ${b.name}`}
                    className="absolute top-0.5 bottom-0.5 rounded-sm"
                    style={{
                      left: `${(b.at / total) * 100}%`,
                      width: `max(3px, ${((end(b) - b.at) / total) * 100}%)`,
                      background: lit.has(b.id) ? "var(--a1)" : "var(--s5)",
                    }}
                  />
                ))}
            </span>
          </div>
        ))}
    </div>
  );
}
