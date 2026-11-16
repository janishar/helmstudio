"use client";
// Models › LoRAs (docs/mock/ModelsLoras.dc.html): the library. Yours: upload (the picker's signed-PUT upload with
// progress), the quota, rename and delete; shared: the files on the hosts' disks (the header's host filter), which an
// admin deletes from one host at a time. Tiles are the visual picker's.
import { useEffect, useRef, useState, type ReactNode } from "react";
import { Pencil, X } from "lucide-react";
import { ErrorNote, Info, Modal } from "@/components/kit";
import { UploadTile } from "@/components/create/loras";
import { Pic, Tile } from "@/components/create/visual-picker";
import { api, message, ok, type S } from "@/lib/api";
import { when } from "@/lib/format";
import { useHydrated } from "@/lib/hydrated";
import {
  deleteLora,
  lorasError,
  lorasUsage,
  patchLoras,
  reloadLoras,
  renameLora,
  useLoras,
} from "@/lib/lora-library";
import { libraryLine, quotaText, sizeText, uploadLora, type UploadState } from "@/lib/loras";

type Lora = S<"Lora">;

export function LorasTab({
  header,
  admin,
  hosts,
  host,
}: {
  header: ReactNode;
  admin: boolean;
  hosts: S<"ModelHost">[];
  host: string;
}) {
  const library = useLoras();
  useEffect(() => void reloadLoras(), []);
  const usage = lorasUsage();
  const [q, setQ] = useState("");
  const [picked, setPicked] = useState("");
  const [uploads, setUploads] = useState<{ key: number; state: UploadState }[]>([]);
  const [deleting, setDeleting] = useState<Lora | null>(null);
  const [error, setError] = useState("");
  const fileInput = useRef<HTMLInputElement>(null);
  const nameInput = useRef<HTMLInputElement>(null);
  const seq = useRef(0);
  const words = q.trim().toLowerCase().split(/\s+/).filter(Boolean);
  const match = (l: Lora) => words.every((w) => l.name.toLowerCase().includes(w));
  const mine = (library ?? []).filter((l) => l.source === "mine" && match(l));
  const shared = (library ?? []).filter(
    (l) => l.source === "shared" && (!host || l.hosts.includes(host)) && match(l),
  );
  const lora = library?.find((l) => l.id === picked) ?? mine[0] ?? shared[0];

  function upload(files: File[]) {
    for (const file of files) {
      const key = ++seq.current;
      setUploads((u) => [...u, { key, state: { phase: "sending", name: file.name, progress: 0 } }]);
      void uploadLora(file, (state) => {
        if (state.phase === "done") {
          setUploads((u) => u.filter((x) => x.key !== key));
          patchLoras(state.lora);
          setPicked(state.lora.id);
          void reloadLoras();
        } else if (state.phase === "failed" && state.id) {
          setUploads((u) => u.filter((x) => x.key !== key));
          void reloadLoras();
        } else setUploads((u) => u.map((x) => (x.key === key ? { key, state } : x)));
      });
    }
  }
  async function remove(l: Lora) {
    try {
      await deleteLora(l);
      setDeleting(null);
      setError("");
      void reloadLoras();
    } catch (e) {
      setDeleting(null);
      setError(message(e));
    }
  }

  const tile = (l: Lora) => (
    <li
      key={l.id}
      className={`relative flex min-w-0 flex-col gap-1.5 ${l.state === "ready" ? "" : "opacity-50"} ${l.id === lora?.id ? "[&_i]:border-2 [&_i]:border-solid [&_i]:border-a1 [&_img]:outline-2 [&_img]:outline-offset-2 [&_img]:outline-a1" : ""}`}
    >
      <Tile
        pic={<Pic thumb={l.thumb_url} empty="no take yet" n={-1} />}
        name={l.name}
        line={libraryLine(l)}
        pressed={l.id === lora?.id}
        onClick={() => setPicked(l.id)}
      />
      {l.source === "mine" && (
        <span className="absolute top-1.5 right-1.5 flex gap-1">
          <button
            type="button"
            aria-label={`Rename ${l.name}`}
            className="grid size-6 place-items-center rounded-lg bg-s3 text-t2 hover:text-t1"
            onClick={() => {
              setPicked(l.id);
              setTimeout(() => nameInput.current?.focus(), 0);
            }}
          >
            <Pencil size={12} aria-hidden />
          </button>
          <button
            type="button"
            aria-label={`Delete ${l.name}`}
            className="grid size-6 place-items-center rounded-lg bg-s3 text-t2 hover:text-t1"
            onClick={() => setDeleting(l)}
          >
            <X size={13} aria-hidden />
          </button>
        </span>
      )}
    </li>
  );
  const grid = "m-0 grid list-none grid-cols-[repeat(auto-fill,minmax(150px,1fr))] gap-3 p-0";
  const section = "panel flex flex-col gap-3 p-4";
  const h2 = "m-0 font-display text-lg font-bold";
  return (
    <>
      <main id="main" className="flex min-w-0 flex-[999_1_560px] flex-col gap-4 px-5 pt-4 pb-6">
        {header}
        {error && <ErrorNote>{error}</ErrorNote>}
        {library === null &&
          (lorasError() ? (
            <ErrorNote onRetry={() => void reloadLoras()}>{lorasError()}</ErrorNote>
          ) : (
            <p role="status" className="text-t2">
              Loading…
            </p>
          ))}
        <section aria-label="Yours" className={section}>
          <div className="flex flex-wrap items-center gap-2.5">
            <h2 className={h2}>Yours</h2>
            {usage && <span className="mono text-[12px] text-t2">{quotaText(usage)}</span>}
            <Info>
              Your uploads: only you can use them, and any host fetches one the first time it runs.
              At most 50 LoRAs and 20 GB per person, 2 GB each.
            </Info>
            <div className="grow" />
            <label htmlFor="l-q" className="sr-only">
              Search names
            </label>
            <input
              id="l-q"
              type="search"
              placeholder="Search names"
              className="in h-[34px] w-[220px]"
              value={q}
              maxLength={80}
              onChange={(e) => setQ(e.target.value)}
            />
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
          {usage && (
            <div
              role="meter"
              aria-label="Your LoRA storage"
              aria-valuemin={0}
              aria-valuemax={usage.max_bytes}
              aria-valuenow={usage.bytes}
              className="block h-1 overflow-hidden rounded-[2px] bg-inset"
            >
              <span
                className="block h-full bg-a2"
                style={{ width: `${Math.min(100, (usage.bytes / usage.max_bytes) * 100)}%` }}
              />
            </div>
          )}
          <ul aria-label="Your LoRAs" className={grid}>
            {uploads.map(({ key, state }) => (
              <UploadTile
                key={`u${key}`}
                u={state}
                onDismiss={() => setUploads((x) => x.filter((y) => y.key !== key))}
              />
            ))}
            {mine.map(tile)}
          </ul>
          {library && !mine.length && !uploads.length && (
            <p className="m-0 text-[13px] text-t2">
              {q ? "No LoRA of yours matches." : "None yet: upload a .safetensors file."}
            </p>
          )}
        </section>
        <section aria-label="Shared" className={section}>
          <div className="flex flex-wrap items-center gap-2.5">
            <h2 className={h2}>Shared</h2>
            <span className="mono text-[12px] text-t2">on the hosts&apos; disks</span>
            <Info>
              Files under each host&apos;s models root, in loras/ or a model&apos;s loras/ folder,
              usable by everyone. A file copied in by hand appears within a minute. Only admins
              delete them, one host at a time.
            </Info>
          </div>
          <ul aria-label="Shared LoRAs" className={grid}>
            {shared.map(tile)}
          </ul>
          {library && !shared.length && (
            <p className="m-0 text-[13px] text-t2">
              {q || host ? "No shared LoRA matches." : "No host has shared LoRAs."}
            </p>
          )}
        </section>
      </main>
      {lora && (
        <LoraPanel
          key={lora.id}
          l={lora}
          admin={admin}
          hosts={hosts}
          nameInput={nameInput}
          onDelete={() => setDeleting(lora)}
        />
      )}
      <Modal open={!!deleting} onClose={() => setDeleting(null)} title="Delete this LoRA?">
        <p className="mb-4 text-[13px] text-t2">
          {deleting?.name} is deleted. Takes made with it keep it in their settings.
        </p>
        <div className="flex justify-end gap-2">
          <button type="button" className="chip bevel" onClick={() => setDeleting(null)}>
            Keep it
          </button>
          <button
            type="button"
            className="chip bevel bg-a1 font-semibold text-on1"
            onClick={() => deleting && void remove(deleting)}
          >
            Delete
          </button>
        </div>
      </Modal>
    </>
  );
}

const ROW = "flex justify-between gap-2.5 text-[13px]";
const VAL = "mono min-w-0 text-right text-[12px] break-all text-t2";

/** The selected LoRA: its details and hosts; yours: rename and delete; shared, for admins: delete from a host. */
function LoraPanel({
  l,
  admin,
  hosts,
  nameInput,
  onDelete,
}: {
  l: Lora;
  admin: boolean;
  hosts: S<"ModelHost">[];
  nameInput: React.RefObject<HTMLInputElement | null>;
  onDelete: () => void;
}) {
  const hydrated = useHydrated();
  const [name, setName] = useState(l.name);
  const [target, setTarget] = useState(l.hosts[0] ?? "");
  const [confirm, setConfirm] = useState(false);
  const [notice, setNotice] = useState("");
  const [error, setError] = useState("");
  const mine = l.source === "mine";
  async function rename() {
    try {
      await renameLora(l.id, name.trim());
      setError("");
      setNotice("Renamed.");
    } catch (e) {
      setError(message(e));
    }
  }
  async function removeFromHost() {
    setConfirm(false);
    try {
      const j = await ok(
        api.DELETE("/v1/studio/admin/loras/{lora_id}", {
          params: { path: { lora_id: l.id }, query: { host: target } },
        }),
      );
      setError("");
      setNotice(`Delete queued on ${j.host}; the library drops it within a minute.`);
    } catch (e) {
      setError(message(e));
    }
  }
  const details: [string, string][] = [
    ["Fits", l.family],
    ["Rank", l.rank ? String(l.rank) : "–"],
    ["Size", sizeText(l.size_bytes)],
    ["Key style", l.key_style ?? "–"],
    ["Licence", l.licence ?? "none named"],
    ["Added", hydrated ? when(l.created_at) : ""],
  ];
  return (
    <aside
      aria-label="Selected LoRA"
      className="aside m-3.5 flex flex-[1_1_340px] flex-col gap-[18px] px-[18px] pt-4 pb-6 md:ml-0"
    >
      <div className="flex flex-col gap-1">
        <h2 className="font-display text-base leading-5 font-bold break-words">{l.name}</h2>
        <span className="mono text-[12px] text-t2">
          {mine ? "yours" : "shared"} · {l.state}
        </span>
      </div>
      <div className="flex flex-col gap-2">
        <h3 className="heading">Details</h3>
        {details.map(([k, v]) => (
          <div key={k} className={ROW}>
            <span>{k}</span>
            <span className={VAL}>{v}</span>
          </div>
        ))}
        {l.reason && <p className="m-0 text-[12px] text-bad">rejected: {l.reason}</p>}
      </div>
      <div className="flex flex-col gap-2">
        <h3 className="heading flex items-center">
          Hosts
          <Info>
            Shared: the hosts that have the file. Yours: none, any host fetches it the first time it
            runs.
          </Info>
        </h3>
        {mine ? (
          <div className={ROW}>
            <span>any host</span>
            <span className={VAL}>fetched on first use</span>
          </div>
        ) : (
          l.hosts.map((h) => (
            <div key={h} className={ROW}>
              <span>{h}</span>
              <span className={VAL}>has the file</span>
            </div>
          ))
        )}
      </div>
      {notice && (
        <p role="status" className="m-0 text-[12px] text-t2">
          {notice}
        </p>
      )}
      {error && <ErrorNote>{error}</ErrorNote>}
      {mine && (
        <div className="mt-auto flex flex-col gap-2">
          <label htmlFor="l-name" className="label">
            Name
          </label>
          <input
            id="l-name"
            ref={nameInput}
            className="in"
            value={name}
            maxLength={80}
            onChange={(e) => setName(e.target.value)}
            onKeyDown={(e) => e.key === "Enter" && name.trim() && void rename()}
          />
          <button
            type="button"
            className="act justify-center disabled:opacity-50"
            disabled={!name.trim() || name.trim() === l.name}
            onClick={() => void rename()}
          >
            Rename
          </button>
          <button
            type="button"
            className="act justify-center disabled:opacity-50 text-a2"
            onClick={onDelete}
          >
            Delete
          </button>
        </div>
      )}
      {!mine && admin && (
        <div className="mt-auto flex flex-col gap-2">
          <h3 className="heading flex items-center">
            Admin · shared LoRA
            <Info>
              Removes its file from one host&apos;s disk. Refused while it is baked into a loaded
              component, part of a model&apos;s weights, or named by a queued or running run there.
            </Info>
          </h3>
          <label htmlFor="l-target" className="label">
            Host
          </label>
          <select
            id="l-target"
            className="in"
            value={target}
            onChange={(e) => setTarget(e.target.value)}
          >
            {l.hosts.map((h) => (
              <option key={h} value={h}>
                {h}
                {hosts.find((x) => x.host === h)?.live === false ? " (offline)" : ""}
              </option>
            ))}
          </select>
          <button
            type="button"
            className="act justify-center disabled:opacity-50 text-a2"
            disabled={!target}
            onClick={() => setConfirm(true)}
          >
            Delete from host
          </button>
        </div>
      )}
      <Modal
        open={confirm}
        onClose={() => setConfirm(false)}
        title={`Delete ${l.name} from ${target}?`}
      >
        <p className="mb-4 text-[13px] text-t2">
          Its file is removed from {target}&apos;s disk for everyone. Takes made with it keep it in
          their settings.
        </p>
        <div className="flex justify-end gap-2">
          <button type="button" className="chip bevel" onClick={() => setConfirm(false)}>
            Keep it
          </button>
          <button
            type="button"
            className="chip bevel bg-a1 font-semibold text-on1"
            onClick={() => void removeFromHost()}
          >
            Delete
          </button>
        </div>
      </Modal>
    </aside>
  );
}
