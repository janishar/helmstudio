"use client";
// The LoRA stack's rows editor (docs/mock/Main.dc.html, the inspector's LoRA section; docs/mock/LoraPicker.dc.html):
// one card per row (name, strength slider over the schema's range, the mode as a toggle), remove, reorder by drag or
// Alt + arrows, the problem of each row; "Add LoRA" opens the picker: shared and own LoRAs, those that do not fit the
// family greyed with the reason, upload of an own .safetensors with progress, rename and delete with confirm.
// Generic: it reads the rows param's item schema (a `Lora` field, a float, an enum), nothing of one family.
import { useEffect, useRef, useState } from "react";
import { Pencil, X } from "lucide-react";
import { ErrorNote, Info, Modal } from "@/components/kit";
import { message, type S } from "@/lib/api";
import { safeUrl } from "@/lib/format";
import {
  deleteLora,
  lorasError,
  patchLoras,
  reloadLoras,
  renameLora,
  useLoras,
} from "@/lib/lora-library";
import {
  addRows,
  clampStrength,
  fitReason,
  modeOf,
  rowIds,
  rowIssues,
  strengthCaption,
  strengthOf,
  tileLine,
  uploadLora,
  type UploadState,
} from "@/lib/loras";
import { move } from "@/lib/refs";

type Row = S<"RowItem">;

/** The rows of one stack param: cards, Add LoRA and the picker. `pick` opens the picker (the graph card's Add LoRA). */
export function LoraRows({
  param,
  family,
  rows,
  onRows,
  error,
  pick = false,
  onPickClosed,
}: {
  param: S<"ParamSchema">;
  family: string | null;
  rows: Row[];
  onRows: (rows: Row[]) => void;
  error?: string;
  pick?: boolean;
  onPickClosed?: () => void;
}) {
  const library = useLoras();
  const [picking, setPicking] = useState(false);
  const open = picking || pick;
  const ids = rowIds(param, rows);
  const issues = rowIssues(param, rows, library, family);
  const strength = strengthOf(param);
  const mode = modeOf(param);
  const max = param.max ?? 8;
  const list = useRef<HTMLOListElement>(null);
  const dragFrom = useRef<number | null>(null);
  const focusRow = (i: number) =>
    requestAnimationFrame(() =>
      list.current?.querySelectorAll<HTMLElement>(":scope > li")[i]?.focus(),
    );
  const setRow = (i: number, patch: Row) =>
    onRows(rows.map((r, j) => (j === i ? { ...r, ...patch } : r)));
  const remove = (i: number) => onRows(rows.filter((_, j) => j !== i));

  return (
    <>
      {rows.length > 0 && (
        <ol
          ref={list}
          aria-label="LoRAs, in order"
          className="m-0 flex list-none flex-col gap-2 p-0"
        >
          {rows.map((r, i) => {
            const l = library?.find((x) => x.id === ids[i]);
            const name = l?.name ?? (ids[i] ? `LoRA ${ids[i].slice(0, 8)}` : "no LoRA");
            const s = strength ? clampStrength(Number(r[strength[0]]), strength[1]) : null;
            const m = mode && String(r[mode[0]] ?? mode[1].default);
            const issue = issues[i];
            const sid = `lora-${param.name}-${i}`;
            return (
              <li
                key={ids[i] || i}
                tabIndex={0}
                draggable
                aria-label={`${i + 1}: ${name}`}
                aria-keyshortcuts="Alt+ArrowUp Alt+ArrowDown Delete"
                onDragStart={(e) => {
                  dragFrom.current = i;
                  e.dataTransfer.effectAllowed = "move";
                  e.dataTransfer.setData("text/plain", name);
                }}
                onDragEnd={() => (dragFrom.current = null)}
                onDragOver={(e) => dragFrom.current !== null && e.preventDefault()}
                onDrop={(e) => {
                  if (dragFrom.current === null) return;
                  e.preventDefault();
                  onRows(move(rows, dragFrom.current, i));
                  dragFrom.current = null;
                }}
                onKeyDown={(e) => {
                  if (e.target !== e.currentTarget) return;
                  const d =
                    e.altKey && e.key === "ArrowUp"
                      ? -1
                      : e.altKey && e.key === "ArrowDown"
                        ? 1
                        : 0;
                  if (d) {
                    e.preventDefault();
                    onRows(move(rows, i, i + d));
                    focusRow(Math.max(0, Math.min(rows.length - 1, i + d)));
                  } else if (e.key === "Delete" || e.key === "Backspace") {
                    e.preventDefault();
                    remove(i);
                    focusRow(Math.min(i, rows.length - 2));
                  }
                }}
                className={`flex flex-col gap-2 rounded-[10px] border bg-s1 px-3 py-2.5 ${issue ? "border-bad" : "border-s3"}`}
              >
                <div className="flex items-center justify-between gap-2">
                  <span className="min-w-0 truncate" title={name}>
                    {name}
                  </span>
                  <span className="flex shrink-0 items-center gap-1.5">
                    {s !== null && <span className="mono text-[12px] text-t2">{s.toFixed(2)}</span>}
                    <button
                      type="button"
                      aria-label={`Remove ${name}`}
                      onClick={() => remove(i)}
                      className="grid size-[22px] place-items-center rounded-lg bg-s3 text-t2 hover:text-t1"
                    >
                      <X size={12} aria-hidden />
                    </button>
                  </span>
                </div>
                {strength && s !== null && (
                  <>
                    <span className="text-[12px] text-t2">
                      <label htmlFor={sid}>{strengthCaption(l)}</label>
                      {mode && m && (
                        <button
                          type="button"
                          title={`Mode: ${m}. ${mode[1].help ?? ""}`}
                          aria-label={`Mode ${m}, change`}
                          onClick={() => {
                            const opts = mode[1].enum ?? [];
                            setRow(i, { [mode[0]]: opts[(opts.indexOf(m) + 1) % opts.length] });
                          }}
                          className="text-t1 underline decoration-dotted underline-offset-2"
                        >
                          {m}
                        </button>
                      )}
                    </span>
                    <input
                      id={sid}
                      type="range"
                      min={strength[1].min}
                      max={strength[1].max}
                      step={strength[1].step ?? "any"}
                      value={s}
                      aria-valuetext={s.toFixed(2)}
                      onChange={(e) =>
                        setRow(i, {
                          [strength[0]]: clampStrength(Number(e.target.value), strength[1]),
                        })
                      }
                      className="w-full accent-a1"
                    />
                  </>
                )}
                {issue && (
                  <span role="alert" className="text-[12px] text-bad">
                    {issue}
                  </span>
                )}
              </li>
            );
          })}
        </ol>
      )}
      {rows.length < max && (
        <button
          type="button"
          onClick={() => setPicking(true)}
          className="h-10 rounded-[10px] border border-dashed border-dash bg-transparent text-[13px] text-t1"
        >
          Add LoRA
        </button>
      )}
      {error && <span className="text-[12px] text-bad">{error}</span>}
      <LoraPicker
        open={open}
        onClose={() => {
          setPicking(false);
          onPickClosed?.();
        }}
        family={family}
        max={Math.max(0, max - rows.length)}
        taken={ids}
        onPick={(more) => onRows(addRows(param, rows, more))}
      />
    </>
  );
}

/** The picker dialog: `onPick` gets the LoRA ids in the order picked. */
export function LoraPicker(props: {
  open: boolean;
  onClose: () => void;
  family: string | null;
  max: number;
  taken: string[];
  onPick: (ids: string[]) => void;
}) {
  return (
    <Modal open={props.open} onClose={props.onClose} title="Add LoRA" width="w-[min(94vw,880px)]">
      <Picker {...props} />
    </Modal>
  );
}

type Upload = { key: number; state: UploadState };

/** The dialog's content, mounted while it is open (each opening starts with nothing picked). */
function Picker({
  onClose,
  family,
  max,
  taken,
  onPick,
}: {
  onClose: () => void;
  family: string | null;
  max: number;
  taken: string[];
  onPick: (ids: string[]) => void;
}) {
  const library = useLoras();
  // the list may be stale (another tab, a host came or went): read it again on each opening
  useEffect(() => void reloadLoras(), []);
  const [source, setSource] = useState<S<"LoraSource"> | "">("");
  const [q, setQ] = useState("");
  const [picked, setPicked] = useState<string[]>([]);
  const [uploads, setUploads] = useState<Upload[]>([]);
  const [renaming, setRenaming] = useState<{ id: string; name: string } | null>(null);
  const [deleting, setDeleting] = useState("");
  const [error, setError] = useState("");
  const fileInput = useRef<HTMLInputElement>(null);
  const seq = useRef(0);
  const words = q.trim().toLowerCase().split(/\s+/).filter(Boolean);
  const shown = (library ?? []).filter(
    (l) =>
      !taken.includes(l.id) &&
      (!source || l.source === source) &&
      words.every((w) => l.name.toLowerCase().includes(w)),
  );
  const toggle = (id: string) =>
    setPicked((p) => (p.includes(id) ? p.filter((x) => x !== id) : [...p, id].slice(0, max)));

  function upload(files: File[]) {
    for (const file of files) {
      const key = ++seq.current;
      setUploads((u) => [...u, { key, state: { phase: "sending", name: file.name, progress: 0 } }]);
      void uploadLora(file, (state) => {
        if (state.phase === "done") {
          setUploads((u) => u.filter((x) => x.key !== key));
          patchLoras(state.lora);
          if (!fitReason(state.lora, family)) setPicked((p) => [...p, state.lora.id].slice(0, max));
          void reloadLoras();
        } else if (state.phase === "failed" && state.id) {
          // it was created: the library lists it, rejected with the reason (or still pending)
          setUploads((u) => u.filter((x) => x.key !== key));
          void reloadLoras();
        } else setUploads((u) => u.map((x) => (x.key === key ? { key, state } : x)));
      });
    }
  }
  async function act(f: () => Promise<unknown>) {
    try {
      setError("");
      await f();
    } catch (e) {
      setError(message(e));
    }
  }

  const tab = (on: boolean) =>
    `h-8 rounded-full px-3 text-[12px] ${on ? "bg-t1 font-semibold text-panel" : "bg-s3 font-medium"}`;
  const icon = "grid size-6 place-items-center rounded-lg bg-s3 text-t2 hover:text-t1";
  const small = "chip h-6 px-2 text-[11px]";
  return (
    <div className="flex max-h-[76dvh] flex-col gap-3.5">
      <div className="-mt-11 flex items-center justify-end gap-2">
        <Info>
          Shared LoRAs are files on the GPU hosts, for everyone. Yours are your uploads: only you
          can use them, and any host fetches one the first time it runs. A LoRA&apos;s picture is
          the first take you made with it.
        </Info>
        <button
          type="button"
          className="chip bevel bg-a1 font-semibold text-on1"
          onClick={() => fileInput.current?.click()}
        >
          Upload .safetensors
        </button>
        <input
          ref={fileInput}
          type="file"
          accept=".safetensors"
          multiple
          hidden
          onChange={(e) => {
            if (e.target.files) upload(Array.from(e.target.files));
            e.target.value = "";
          }}
        />
      </div>
      <div className="flex flex-wrap items-center gap-2">
        <label htmlFor="lora-q" className="sr-only">
          Search names
        </label>
        <input
          id="lora-q"
          type="search"
          placeholder="Search names"
          value={q}
          maxLength={80}
          autoFocus
          onChange={(e) => setQ(e.target.value)}
          // the picker sits inside the composer's form: Enter must not submit it
          onKeyDown={(e) => e.key === "Enter" && e.preventDefault()}
          className="h-9 flex-[1_1_260px] rounded-xl border border-s5 bg-s1 px-3 text-t1"
        />
        <div role="group" aria-label="Source" className="flex gap-1.5">
          {(
            [
              ["", "All"],
              ["shared", "Shared"],
              ["mine", "Mine"],
            ] as const
          ).map(([s, label]) => (
            <button
              key={label}
              type="button"
              aria-pressed={source === s}
              className={tab(source === s)}
              onClick={() => setSource(s)}
            >
              {label}
            </button>
          ))}
        </div>
      </div>
      {error && <ErrorNote>{error}</ErrorNote>}
      <div className="min-h-32 overflow-y-auto p-0.5">
        {library === null &&
          (lorasError() ? (
            <ErrorNote onRetry={() => void reloadLoras()}>{lorasError()}</ErrorNote>
          ) : (
            <p role="status" className="text-t2">
              Loading…
            </p>
          ))}
        {library && !shown.length && !uploads.length && (
          <p className="py-8 text-center text-t2">
            {library.length > taken.length
              ? "No LoRA matches."
              : "No LoRAs yet: upload a .safetensors file, or ask an admin to put shared ones on a host."}
          </p>
        )}
        <ul
          aria-label="LoRAs"
          className="m-0 grid list-none grid-cols-[repeat(auto-fill,minmax(130px,1fr))] gap-3 p-0"
        >
          {uploads.map(({ key, state: u }) => (
            <li key={`u${key}`} className="flex min-w-0 flex-col gap-1.5">
              <i
                className={`flex aspect-[16/10] flex-col items-center justify-center gap-1.5 rounded-[10px] border border-dashed border-dash bg-s1 px-2 text-center text-[11px] not-italic ${u.phase === "failed" ? "text-bad" : "text-t2"}`}
              >
                {u.phase === "sending" ? (
                  <>
                    <span aria-live="polite">Uploading {Math.round(u.progress * 100)}%</span>
                    <span
                      role="progressbar"
                      aria-label={`Uploading ${u.name}`}
                      aria-valuenow={Math.round(u.progress * 100)}
                      aria-valuemin={0}
                      aria-valuemax={100}
                      className="block h-1 w-full overflow-hidden rounded-[2px] bg-in"
                    >
                      <span
                        className="block h-full bg-a2"
                        style={{ width: `${u.progress * 100}%` }}
                      />
                    </span>
                  </>
                ) : u.phase === "failed" ? (
                  <span role="alert">{u.error}</span>
                ) : (
                  <span aria-live="polite">checking…</span>
                )}
              </i>
              <b className="truncate text-[12px] font-semibold">
                {u.name.replace(/\.safetensors$/i, "")}
              </b>
              {u.phase === "failed" && (
                <small>
                  <button
                    type="button"
                    className={small}
                    onClick={() => setUploads((x) => x.filter((y) => y.key !== key))}
                  >
                    Dismiss
                  </button>
                </small>
              )}
            </li>
          ))}
          {shown.map((l) => {
            const why = fitReason(l, family);
            const n = picked.indexOf(l.id);
            const thumb = safeUrl(l.thumb_url);
            const pic = (
              <span className="relative block w-full">
                {thumb ? (
                  <img
                    src={thumb}
                    alt=""
                    loading="lazy"
                    className={`block aspect-[16/10] w-full rounded-[10px] bg-s1 object-cover ${n >= 0 ? "outline-2 -outline-offset-2 outline-a1" : ""}`}
                  />
                ) : (
                  <i
                    className={`flex aspect-[16/10] items-center justify-center rounded-[10px] bg-s1 text-[11px] text-t2 not-italic ${n >= 0 ? "border-2 border-a1" : "border border-dashed border-dash"}`}
                  >
                    no take yet
                  </i>
                )}
                {n >= 0 && (
                  <span className="absolute top-1.5 left-1.5 grid size-5 place-items-center rounded-full bg-a1 text-[11px] font-bold text-on1">
                    {n + 1}
                  </span>
                )}
              </span>
            );
            const save = () => {
              const name = renaming?.name.trim();
              if (name)
                void act(async () => {
                  await renameLora(l.id, name);
                  setRenaming(null);
                });
            };
            return (
              <li key={l.id} className="relative flex min-w-0 flex-col gap-1.5">
                {renaming?.id === l.id ? (
                  <div
                    className="flex flex-col gap-1.5"
                    onKeyDown={(e) => {
                      // a div, not a form: the picker may sit inside the composer's form
                      if (e.key === "Enter") {
                        e.preventDefault();
                        save();
                      } else if (e.key === "Escape") {
                        e.stopPropagation();
                        setRenaming(null);
                      }
                    }}
                  >
                    {pic}
                    <input
                      aria-label="New name"
                      value={renaming.name}
                      maxLength={80}
                      autoFocus
                      onChange={(e) => setRenaming({ id: l.id, name: e.target.value })}
                      className="h-[26px] min-w-0 rounded-lg border border-a1 bg-s1 px-2 text-[12px] text-t1"
                    />
                    <small className="flex gap-1">
                      <button type="button" className={small} onClick={() => setRenaming(null)}>
                        Cancel
                      </button>
                      <button
                        type="button"
                        className={`${small} bg-a1 text-on1`}
                        disabled={!renaming.name.trim()}
                        onClick={save}
                      >
                        Save
                      </button>
                    </small>
                  </div>
                ) : (
                  <button
                    type="button"
                    aria-pressed={n >= 0}
                    disabled={!!why || (n < 0 && picked.length >= max)}
                    title={why ?? l.name}
                    onClick={() => toggle(l.id)}
                    className="flex w-full flex-col gap-1.5 text-left disabled:cursor-not-allowed disabled:opacity-50"
                  >
                    {pic}
                    <b className="truncate text-[12px] font-semibold">{l.name}</b>
                    <small
                      className={`mono text-[10px] break-words text-t2 ${why ? "line-clamp-3" : "truncate"}`}
                    >
                      {deleting === l.id ? "" : tileLine(l, family)}
                    </small>
                  </button>
                )}
                {deleting === l.id && (
                  <>
                    <small className="text-[11px]">
                      Delete {l.name}? Takes made with it keep it.
                    </small>
                    <small className="flex gap-1">
                      <button type="button" className={small} onClick={() => setDeleting("")}>
                        Cancel
                      </button>
                      <button
                        type="button"
                        className={`${small} text-bad`}
                        onClick={() =>
                          void act(async () => {
                            await deleteLora(l);
                            setDeleting("");
                            setPicked((p) => p.filter((x) => x !== l.id));
                          })
                        }
                      >
                        Delete
                      </button>
                    </small>
                  </>
                )}
                {l.source === "mine" && renaming?.id !== l.id && deleting !== l.id && (
                  <span className="absolute top-1.5 right-1.5 flex gap-1">
                    <button
                      type="button"
                      aria-label={`Rename ${l.name}`}
                      className={icon}
                      onClick={() => setRenaming({ id: l.id, name: l.name })}
                    >
                      <Pencil size={12} aria-hidden />
                    </button>
                    <button
                      type="button"
                      aria-label={`Delete ${l.name}`}
                      className={icon}
                      onClick={() => setDeleting(l.id)}
                    >
                      <X size={13} aria-hidden />
                    </button>
                  </span>
                )}
              </li>
            );
          })}
        </ul>
      </div>
      <div className="flex flex-wrap items-center justify-end gap-2">
        <span className="mono mr-auto text-[12px] text-t2" aria-live="polite">
          {picked.length} picked · up to {max}
        </span>
        <button type="button" className="chip bevel" onClick={onClose}>
          Cancel
        </button>
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
      </div>
      <p className="m-0 text-[12px] text-t2">
        LoRAs that do not fit{family ? ` ${family}` : ""}, or are not ready, are greyed with the
        reason. Picks join the stack in the order picked, at strength 1. Uploads: .safetensors only,
        up to 2 GB each; 50 LoRAs and 20 GB per person.
      </p>
    </div>
  );
}
