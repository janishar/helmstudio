"use client";
// Model folders (docs/mock/ModelFolders.dc.html, plan 40, API 1.78): the Folders strip of a host on the Models page
// (frame 1, 3), the Edit folders card (frame 2: drag or Alt+↑↓ to reorder, the Downloads radio, Remove with its confirm,
// Add a path or Browse… in the Mac app), and a folder's right-click menu (frame 5a) on the strip's chips and the rows.
// Save replaces the host's list (PUT …/hosts/{host}/folders); the runner checks each path first.
import { useState } from "react";
import { ArrowDown, ArrowUp, Download, FolderOpen, Trash2 } from "lucide-react";
import { ErrorNote, Info, Modal } from "@/components/kit";
import { openMenu, type Item } from "@/components/context-menu";
import { api, message, ok, type S } from "@/lib/api";
import { diskText, foldersOf, pathName, pathProblem } from "@/lib/models";

type Folder = S<"ModelFolder">;
/** A row of the card: a folder of the list (`f`, as the runner reports it) or a path added and not saved yet. */
type Row = { path: string; f?: Folder };

/** The Mac app's bridge (desktop/src/ui/studio-preload.js): its folder dialog and Show in Finder, on its own host. */
type Mac = {
  localHost?: string;
  chooseFolder?: (current?: string) => Promise<string | null>;
  reveal?: (path: string) => Promise<boolean>;
};
const mac = () => (globalThis as { helmDesktop?: Mac }).helmDesktop;

/** Why Open / Show in Finder is greyed for a path on `host`, or null when it can open. */
export function revealOff(host: string, connected = true): string | null {
  const d = mac();
  if (!d?.reveal) return "In the Mac app";
  if (d.localHost !== host) return "Not on this Mac";
  return connected ? null : "Not connected";
}
export const reveal = (path: string) => void mac()?.reveal?.(path);

/** Replaces the host's list: the paths in priority order and the index downloads go to. */
const putFolders = (host: string, paths: string[], downloads: number) =>
  ok(
    api.PUT("/v1/studio/admin/hosts/{host}/folders", {
      params: { path: { host } },
      body: { folders: paths.map((path) => ({ path })), downloads },
    }),
  );

const moved = <T,>(xs: T[], from: number, to: number) => {
  const ys = [...xs];
  ys.splice(to, 0, ...ys.splice(from, 1));
  return ys;
};
/** The downloads index after the row at `from` moved to `to`. */
const follow = (dl: number, from: number, to: number) =>
  dl === from ? to : from < dl && to >= dl ? dl - 1 : from > dl && to <= dl ? dl + 1 : dl;

/** A folder's right-click items (frame 5a); the actions are the caller's (the card's state, or a PUT from the strip). */
function folderItems(o: {
  i: number;
  n: number;
  r: Row;
  host: string;
  admin: boolean;
  downloads: boolean;
  move: (to: number) => void;
  useForDownloads: () => void;
  remove: () => void;
}): Item[] {
  const { i, n, r, admin } = o;
  const off = (why: string | false) => (admin ? why || undefined : "Admins only");
  const known = r.f;
  return [
    {
      label: "Move up",
      icon: ArrowUp,
      off: off(i === 0 && "Already first"),
      onPick: () => o.move(i - 1),
    },
    {
      label: "Move down",
      icon: ArrowDown,
      off: off(i === n - 1 && "Already last"),
      onPick: () => o.move(i + 1),
    },
    {
      label: "Use for downloads",
      icon: Download,
      off: off(
        o.downloads
          ? "Downloads go here"
          : known && !known.connected
            ? "Not connected"
            : known && !known.writable
              ? "Read-only"
              : false,
      ),
      onPick: o.useForDownloads,
    },
    {
      label: "Open in Finder", // greyed outside the Mac app, saying why (ModelFolders 5a)
      icon: FolderOpen,
      off: (known ? revealOff(o.host, known.connected) : "Not saved yet") ?? undefined,
      onPick: () => reveal(r.path),
    },
    "-",
    {
      label: "Remove…",
      icon: Trash2,
      danger: true,
      off: off(n === 1 && "The last folder can't be removed"),
      onPick: o.remove,
    },
  ];
}

const nameOf = (r: Row, host: string) => r.f?.name ?? pathName(r.path, host);

/** "Remove T9 from the list?" (frame 5b): the files stay. */
function RemoveCard({
  name,
  host,
  onCancel,
  onRemove,
}: {
  name: string;
  host: string;
  onCancel: () => void;
  onRemove: () => void;
}) {
  return (
    <div
      role="alertdialog"
      aria-label={`Remove ${name}?`}
      className="flex flex-col gap-3 rounded-2xl border border-s5 bg-s2 p-4"
    >
      <h3 className="m-0 font-display text-[15px] font-bold">Remove {name} from the list?</h3>
      <p className="m-0 text-[13px] text-t2">
        The files stay on disk. Models only there show as missing on {host}.
      </p>
      <div className="flex justify-end gap-2">
        <button type="button" className="chip bevel" onClick={onCancel}>
          Cancel
        </button>
        <button
          type="button"
          className="chip bevel bg-bad font-semibold text-[#2b0c02]"
          onClick={onRemove}
        >
          Remove
        </button>
      </div>
    </div>
  );
}

/** The Folders strip of one host (frame 1): each folder's place, name, path, free space, the Downloads tag; Edit… for
 * admins; a chip's right-click acts on the saved list at once. */
export function FolderStrip({
  list,
  host,
  admin,
  onChanged,
}: {
  list: S<"ModelList">;
  host: string;
  admin: boolean;
  onChanged: () => void;
}) {
  const fs = foldersOf(list, host);
  const [edit, setEdit] = useState(false);
  const [removing, setRemoving] = useState<number | null>(null);
  const [error, setError] = useState("");
  const paths = fs.map((f) => f.root);
  const dl = Math.max(
    0,
    fs.findIndex((f) => f.downloads),
  );
  const save = (ps: string[], d: number) =>
    putFolders(host, ps, d)
      .then(() => (setError(""), onChanged()))
      .catch((e) => setError(message(e)));
  return (
    <div className="flex flex-col gap-2">
      <div
        role="group"
        aria-label={`Folders on ${host}`}
        className="flex flex-wrap items-center gap-2 rounded-[18px_18px_18px_8px] bg-s1 py-2 pr-2 pl-3.5"
      >
        <span className="heading">Folders · {host}</span>
        {fs.map((f, i) => (
          <span
            key={f.id}
            title={f.root}
            onContextMenu={(e) =>
              openMenu(e, {
                title: `${f.name} · ${f.root}`,
                items: folderItems({
                  i,
                  n: fs.length,
                  r: { path: f.root, f },
                  host,
                  admin,
                  downloads: f.downloads,
                  move: (to) => void save(moved(paths, i, to), follow(dl, i, to)),
                  useForDownloads: () => void save(paths, i),
                  remove: () => setRemoving(i),
                }),
              })
            }
            className={`inline-flex h-[34px] min-w-0 items-center gap-2 rounded-full pr-3 pl-1.5 text-[13px] font-medium ${f.connected ? "bg-s3" : "text-t2 shadow-[inset_0_0_0_1px_var(--ln)]"}`}
          >
            <span className="inline-grid size-[22px] shrink-0 place-items-center rounded-full bg-s5 text-[11px] font-semibold">
              {i + 1}
            </span>
            {f.name}
            <span className="mono truncate text-[12px] text-t2">{f.root}</span>
            {f.connected ? (
              <span className="mono text-[12px] text-t2">{diskText(f.free_bytes)} free</span>
            ) : (
              <span>Not connected</span>
            )}
            {f.downloads && <DownloadsTag />}
          </span>
        ))}
        <div className="grow" />
        {admin && (
          <button type="button" className="chip bevel" onClick={() => setEdit(true)}>
            Edit…
          </button>
        )}
        <Info>
          The order is the priority: a model in more than one folder loads from the first one that
          has it, then the next.
        </Info>
      </div>
      {removing !== null && fs[removing] && (
        <RemoveCard
          name={fs[removing].name}
          host={host}
          onCancel={() => setRemoving(null)}
          onRemove={() => {
            const i = removing;
            setRemoving(null);
            void save(
              paths.filter((_, j) => j !== i),
              dl === i ? 0 : dl > i ? dl - 1 : dl,
            );
          }}
        />
      )}
      {error && <ErrorNote>{error}</ErrorNote>}
      <Modal
        open={edit}
        onClose={() => setEdit(false)}
        title={`Folders · ${host}`}
        width="w-[min(94vw,680px)]"
      >
        <EditFolders
          list={list}
          host={host}
          onClose={() => setEdit(false)}
          onSaved={() => {
            setEdit(false);
            onChanged();
          }}
        />
      </Modal>
    </div>
  );
}

export function DownloadsTag() {
  return (
    <span className="rounded-full bg-[color-mix(in_srgb,var(--a1)_24%,var(--s4))] px-[7px] py-px text-[11px] font-medium">
      Downloads
    </span>
  );
}

/** Edit folders (frame 2): the list as the card holds it until Save. */
export function EditFolders({
  list,
  host,
  onClose,
  onSaved,
}: {
  list: S<"ModelList">;
  host: string;
  onClose: () => void;
  onSaved: () => void;
}) {
  const fs = foldersOf(list, host);
  const [rows, setRows] = useState<Row[]>(fs.map((f) => ({ path: f.root, f })));
  const [dl, setDl] = useState(
    Math.max(
      0,
      fs.findIndex((f) => f.downloads),
    ),
  );
  const [drag, setDrag] = useState<number | null>(null);
  const [removing, setRemoving] = useState<number | null>(null);
  const [path, setPath] = useState("");
  const [addError, setAddError] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const d = mac();
  const browse = d?.chooseFolder && d.localHost === host ? d.chooseFolder : null;

  const move = (from: number, to: number) => {
    if (to < 0 || to >= rows.length || from === to) return;
    setRows(moved(rows, from, to));
    setDl(follow(dl, from, to));
  };
  const remove = (i: number) => {
    setRows(rows.filter((_, j) => j !== i));
    setDl(dl === i ? 0 : dl > i ? dl - 1 : dl);
  };
  const add = (p = path.trim()) => {
    const why = pathProblem(
      p,
      rows.map((r) => r.path),
    );
    if (why) return setAddError(why);
    setRows([...rows, { path: p }]);
    setPath("");
    setAddError("");
  };
  async function save() {
    setBusy(true);
    try {
      await putFolders(
        host,
        rows.map((r) => r.path),
        dl,
      );
      onSaved();
    } catch (e) {
      setError(message(e));
    } finally {
      setBusy(false);
    }
  }
  const full = rows.length >= 8;
  return (
    <div className="flex flex-col gap-3.5">
      <p className="m-0 text-[12px] text-t2">
        Drag to reorder. A model loads from the first folder that has it, then the next.
      </p>
      <div role="list" className="flex flex-col gap-1.5">
        {rows.map((r, i) => {
          const f = r.f;
          const off = f && !f.connected;
          const name = nameOf(r, host);
          const menu = (e: React.MouseEvent | React.KeyboardEvent) =>
            openMenu(e as React.MouseEvent, {
              title: `${name} · ${r.path}`,
              items: folderItems({
                i,
                n: rows.length,
                r,
                host,
                admin: true,
                downloads: dl === i,
                move: (to) => move(i, to),
                useForDownloads: () => setDl(i),
                remove: () => setRemoving(i),
              }),
            });
          return (
            <div
              key={r.path}
              role="listitem"
              aria-label={name}
              tabIndex={0}
              draggable
              onDragStart={(e) => {
                setDrag(i);
                e.dataTransfer?.setData("text/plain", r.path);
              }}
              onDragEnd={() => setDrag(null)}
              onDragOver={(e) => e.preventDefault()}
              onDrop={(e) => {
                e.preventDefault();
                if (drag !== null) move(drag, i);
                setDrag(null);
              }}
              onKeyDown={(e) => {
                if (e.target !== e.currentTarget || !e.altKey) return;
                if (e.key === "ArrowUp") move(i, i - 1);
                else if (e.key === "ArrowDown") move(i, i + 1);
                else return;
                e.preventDefault();
              }}
              onContextMenu={menu}
              className={`grid grid-cols-[18px_24px_minmax(0,1fr)_150px_108px_28px] items-center gap-2.5 rounded-xl py-2.5 pr-2.5 pl-2 ${drag === i ? "-translate-y-[3px] bg-s4 shadow-[0_0_0_1px_var(--a2)]" : off ? "shadow-[inset_0_0_0_1px_var(--ln)]" : "bg-s3"}`}
            >
              <span
                aria-hidden
                title="Drag to reorder"
                className="cursor-grab text-[13px] font-bold tracking-[-2px] text-t2 select-none"
              >
                ⋮⋮
              </span>
              <span
                className={`inline-grid size-[22px] place-items-center rounded-full bg-s5 text-[11px] font-semibold ${off ? "opacity-60" : ""}`}
              >
                {i + 1}
              </span>
              <span className={`flex min-w-0 flex-col gap-0.5 ${off ? "opacity-60" : ""}`}>
                <b className="flex items-center gap-2 text-[13px] font-semibold">
                  {name}
                  {f?.connected && (
                    <span className="text-[11px] font-normal text-a1">Connected</span>
                  )}
                </b>
                <span className="mono truncate text-[11px] text-t2">{r.path}</span>
              </span>
              {!f ? (
                <span className="text-[11px] text-t2">Checked on Save</span>
              ) : f.connected ? (
                <span className="mono flex flex-col gap-1 text-[11px] text-t2">
                  <span>
                    {diskText(f.free_bytes)} free of {diskText(f.total_bytes)}
                  </span>
                  {f.total_bytes ? (
                    <span className="block h-1 overflow-hidden rounded-[2px] bg-inset">
                      <span
                        className="block h-full bg-a1"
                        style={{
                          width: `${Math.round((1 - (f.free_bytes ?? 0) / f.total_bytes) * 100)}%`,
                        }}
                      />
                    </span>
                  ) : null}
                </span>
              ) : (
                <span className="inline-flex items-center gap-1.5 text-[12px] text-t2">
                  <span className="size-1.5 rounded-full shadow-[inset_0_0_0_1.5px_var(--t2)]" />
                  Not connected
                </span>
              )}
              <label className="inline-flex items-center gap-1.5 text-[12px] whitespace-nowrap text-t2">
                <input
                  type="radio"
                  name={`downloads-${host}`}
                  className="m-0 accent-[var(--a1)]"
                  checked={dl === i}
                  disabled={!!f && (!f.connected || !f.writable)}
                  onChange={() => setDl(i)}
                />
                Downloads
              </label>
              <button
                type="button"
                aria-label={`Remove ${name}`}
                title={rows.length === 1 ? "The last folder can't be removed" : "Remove…"}
                disabled={rows.length === 1}
                onClick={() => setRemoving(i)}
                className="grid size-[26px] place-items-center rounded-lg text-sm text-t2 hover:bg-s4 disabled:opacity-40"
              >
                ✕
              </button>
            </div>
          );
        })}
      </div>
      {removing !== null && rows[removing] && (
        <RemoveCard
          name={nameOf(rows[removing], host)}
          host={host}
          onCancel={() => setRemoving(null)}
          onRemove={() => {
            remove(removing);
            setRemoving(null);
          }}
        />
      )}
      <div className="flex items-center gap-2">
        <input
          aria-label="Folder path"
          className="mono h-9 min-w-0 flex-auto rounded-[10px] border border-s4 bg-s1 px-2.5 text-[13px] text-t1"
          placeholder="/Volumes/"
          value={path}
          onChange={(e) => setPath(e.target.value)}
          onKeyDown={(e) => e.key === "Enter" && (e.preventDefault(), add())}
        />
        {browse && (
          <button
            type="button"
            className="chip bevel"
            disabled={full}
            onClick={() =>
              void browse(path || undefined).then((p) => {
                if (p) add(p);
              })
            }
          >
            Browse…
          </button>
        )}
        <button
          type="button"
          className="chip bevel"
          disabled={full || !path.trim()}
          title={full ? "At most 8 folders" : undefined}
          onClick={() => add()}
        >
          Add
        </button>
      </div>
      {addError && <ErrorNote>{addError}</ErrorNote>}
      {!browse && (
        <span className="text-[11px] text-t2">
          Browse is in the Mac app; in a browser, type the path as the host sees it.
        </span>
      )}
      {error && <ErrorNote>{error}</ErrorNote>}
      <div className="flex justify-end gap-2">
        <button type="button" className="chip bevel" onClick={onClose}>
          Cancel
        </button>
        <button
          type="button"
          className="chip bevel bg-a1 font-semibold text-on1"
          disabled={busy || !rows.length}
          onClick={() => void save()}
        >
          Save
        </button>
      </div>
    </div>
  );
}
