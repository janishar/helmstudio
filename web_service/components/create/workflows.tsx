"use client";
// The workflow picker in Create's top bar: the templates and the user's own, with Rename, Duplicate and Delete for
// their own (contracts/README.md, User workflows).
import Link from "next/link";
import { useState } from "react";
import {
  AudioLines,
  ChevronDown,
  Film,
  Image,
  MessageSquare,
  Mic,
  Workflow as WorkflowGlyph,
  type LucideIcon,
} from "lucide-react";
import { Modal } from "@/components/kit";
import { api, message, ok, type S } from "@/lib/api";
import { Marked, NoMatch, SearchField, useSearch } from "@/components/search";
import { mediaOf } from "@/lib/media";

type Workflow = S<"Workflow">;

const ICONS: Record<string, LucideIcon> = {
  image: Image,
  video: Film,
  audio: AudioLines,
  voice: Mic,
  text: MessageSquare,
};

/** What a workflow makes, for its icon: its media, and voice for audio that takes a voice param. */
export function makes(w: Pick<Workflow, "media" | "nodes" | "node_schemas">): string {
  const m = mediaOf(w);
  const voice = Object.values(w.nodes ?? {}).some((n) =>
    w.node_schemas?.[n.kind]?.params?.some((p) => p.type === "voice"),
  );
  return m === "audio" && voice ? "voice" : m;
}

/** A workflow's icon (docs/mock/Main.dc.html, the workflow picker): by what it makes; the workflow glyph otherwise. */
export function WorkflowIcon({ w, size = 16 }: { w: Workflow; size?: number }) {
  const Icon = ICONS[makes(w)] ?? WorkflowGlyph;
  return <Icon size={size} strokeWidth={1.75} aria-hidden className="shrink-0" />;
}

/** The badge that opens the picker; `label` is what it reads (the open workflow and its state). */
export function WorkflowPicker({
  workflows,
  current,
  label,
  onPick,
  onChanged,
  onDeleted,
  onError,
  available = () => true,
}: {
  workflows: Workflow[];
  current?: Workflow;
  label: string;
  onPick: (id: string) => void;
  /** a workflow made or renamed here */
  onChanged: (w: Workflow) => void;
  onDeleted: (id: string) => void;
  onError: (msg: string) => void;
  /** false: no variant of its model is on a live host (greyed, "Not downloaded · Models"; still opens) */
  available?: (w: Workflow) => boolean;
}) {
  const [renaming, setRenaming] = useState<string | null>(null);
  const [deleting, setDeleting] = useState<Workflow | null>(null);
  const [busy, setBusy] = useState(false);
  const hide = () => document.getElementById("workflows")?.hidePopover();
  // past 8 workflows, the browser searches their titles (all loaded)
  // templates first, as drawn: ↑ ↓ and Enter follow the rows on screen
  const search = useSearch({
    items: [
      ...workflows.filter((w) => w.source !== "mine"),
      ...workflows.filter((w) => w.source === "mine"),
    ],
    text: (w) => [w.title],
    id: (w) => w.id,
    onPick: (w) => (hide(), onPick(w.id)),
  });
  const templates = search.shown.filter((w) => w.source !== "mine");
  const mine = search.shown.filter((w) => w.source === "mine");
  const act = async (f: () => Promise<void>) => {
    setBusy(true);
    try {
      await f();
    } catch (e) {
      onError(message(e));
    } finally {
      setBusy(false);
    }
  };
  const item = (w: Workflow) => {
    const pick = (
      <button
        type="button"
        role="menuitemradio"
        id={search.rowId(w)}
        aria-checked={w.id === current?.id}
        className={`flex min-w-0 flex-1 items-center gap-2 rounded-[10px] px-3 py-2 text-left text-[13px] hover:bg-s3 aria-checked:bg-s4 aria-checked:font-semibold ${available(w) ? "" : "text-t2"} ${search.rows[search.active] === w ? "sf-on" : ""}`}
        onClick={() => {
          hide();
          onPick(w.id);
        }}
      >
        <WorkflowIcon w={w} />
        <span className="truncate">
          <Marked text={w.title} q={search.q} />
        </span>
      </button>
    );
    if (available(w)) return pick;
    return (
      <div className="flex min-w-0 flex-1 flex-col">
        {pick}
        <span className="px-3 pb-1.5 text-[11px] text-t2">
          Not downloaded ·{" "}
          <Link href="/models" className="underline" onClick={hide}>
            Models
          </Link>
        </span>
      </div>
    );
  };
  return (
    <>
      <button
        type="button"
        popoverTarget="workflows"
        aria-label={`Workflow: ${label}. Change workflow`}
        className="flex max-w-[48ch] min-w-0 items-center gap-1.5 rounded-full bg-s2 px-2.5 py-1 text-[12px] font-medium text-t2"
        style={{ anchorName: "--workflows" } as React.CSSProperties}
      >
        {current && <WorkflowIcon w={current} size={14} />}
        <span className="truncate">{label}</span>
        <ChevronDown size={12} aria-hidden className="shrink-0" />
      </button>
      <div
        id="workflows"
        popover="auto"
        role="menu"
        aria-label="Workflows"
        className="pop w-80"
        style={
          {
            positionAnchor: "--workflows",
            positionArea: "bottom span-right",
            marginTop: 6,
          } as React.CSSProperties
        }
      >
        <div className="flex max-h-[60vh] flex-col gap-1 overflow-y-auto [&>*]:shrink-0">
          {search.show && <SearchField {...search.field} label="Search workflows" />}
          {search.q && !search.shown.length && (
            <NoMatch what="workflows" onClear={() => search.setQ("")} />
          )}
          {(!search.q || templates.length > 0) && (
            <span className="heading px-3 pt-1">Templates</span>
          )}
          {templates.map((w) => (
            <div key={w.id} className="flex">
              {item(w)}
            </div>
          ))}
          {(!search.q || mine.length > 0) && <span className="heading px-3 pt-3">Mine</span>}
          {!search.q && !mine.length && (
            <span className="px-3 py-1 text-[12px] text-t2">
              None yet: edit a template in Graph to make your own.
            </span>
          )}
          {mine.map((w) =>
            renaming === w.id ? (
              <form
                key={w.id}
                className="flex gap-1.5 px-1"
                onSubmit={(e) => {
                  e.preventDefault();
                  const title = String(new FormData(e.currentTarget).get("title") ?? "").trim();
                  if (!title) return;
                  void act(async () => {
                    const r = await ok(
                      api.PATCH("/v1/studio/workflows/{workflow_id}", {
                        params: { path: { workflow_id: w.id } },
                        body: { title },
                      }),
                    );
                    onChanged(r);
                    setRenaming(null);
                  });
                }}
              >
                <input
                  name="title"
                  aria-label={`New name for ${w.title}`}
                  defaultValue={w.title}
                  maxLength={80}
                  autoFocus
                  className="in h-9"
                  onKeyDown={(e) => {
                    if (e.key === "Escape") {
                      e.stopPropagation();
                      setRenaming(null);
                    }
                  }}
                />
                <button type="submit" disabled={busy} className="chip h-9 shrink-0">
                  Save
                </button>
              </form>
            ) : (
              <div key={w.id} className="flex items-center gap-1">
                {item(w)}
                <button
                  type="button"
                  className="shrink-0 rounded-lg px-2 py-1 text-[12px] text-t2 hover:bg-s3 hover:text-t1"
                  aria-label={`Rename ${w.title}`}
                  onClick={() => setRenaming(w.id)}
                >
                  Rename
                </button>
                <button
                  type="button"
                  disabled={busy}
                  className="shrink-0 rounded-lg px-2 py-1 text-[12px] text-t2 hover:bg-s3 hover:text-t1"
                  aria-label={`Duplicate ${w.title}`}
                  onClick={() =>
                    void act(async () => {
                      const r = await ok(
                        api.POST("/v1/studio/workflows", { body: { based_on: w.id } }),
                      );
                      onChanged(r);
                    })
                  }
                >
                  Duplicate
                </button>
                <button
                  type="button"
                  className="shrink-0 rounded-lg px-2 py-1 text-[12px] text-t2 hover:bg-s3 hover:text-bad"
                  aria-label={`Delete ${w.title}`}
                  onClick={() => {
                    hide();
                    setDeleting(w);
                  }}
                >
                  Delete
                </button>
              </div>
            ),
          )}
        </div>
      </div>
      <Modal open={!!deleting} onClose={() => setDeleting(null)} title="Delete this workflow?">
        <p className="mb-4 text-[13px] text-t2">
          “{deleting?.title}” goes for good. Takes made with it keep their settings.
        </p>
        <div className="flex justify-end gap-2">
          <button type="button" className="chip" onClick={() => setDeleting(null)}>
            Keep
          </button>
          <button
            type="button"
            disabled={busy}
            className="chip bg-a2 text-on2"
            onClick={() => {
              const id = deleting!.id;
              setDeleting(null);
              void act(async () => {
                await ok(
                  api.DELETE("/v1/studio/workflows/{workflow_id}", {
                    params: { path: { workflow_id: id } },
                  }),
                );
                onDeleted(id);
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
