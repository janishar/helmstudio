"use client";
// The workflow picker in Create's top bar: the templates and the user's own, with Rename, Duplicate and Delete for
// their own (contracts/README.md, User workflows).
import { useState } from "react";
import { ChevronDown } from "lucide-react";
import { Modal } from "@/components/kit";
import { api, message, ok, type S } from "@/lib/api";

type Workflow = S<"Workflow">;

/** The badge that opens the picker; `label` is what it reads (the open workflow and its state). */
export function WorkflowPicker({
  workflows,
  current,
  label,
  onPick,
  onChanged,
  onDeleted,
  onError,
}: {
  workflows: Workflow[];
  current?: Workflow;
  label: string;
  onPick: (id: string) => void;
  /** a workflow made or renamed here */
  onChanged: (w: Workflow) => void;
  onDeleted: (id: string) => void;
  onError: (msg: string) => void;
}) {
  const [renaming, setRenaming] = useState<string | null>(null);
  const [deleting, setDeleting] = useState<Workflow | null>(null);
  const [busy, setBusy] = useState(false);
  const hide = () => document.getElementById("workflows")?.hidePopover();
  const templates = workflows.filter((w) => w.source !== "mine");
  const mine = workflows.filter((w) => w.source === "mine");
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
  const item = (w: Workflow) => (
    <button
      type="button"
      role="menuitemradio"
      aria-checked={w.id === current?.id}
      className="min-w-0 flex-1 truncate rounded-[10px] px-3 py-2 text-left text-[13px] hover:bg-s3 aria-checked:bg-s4 aria-checked:font-semibold"
      onClick={() => {
        hide();
        onPick(w.id);
      }}
    >
      {w.title}
    </button>
  );
  return (
    <>
      <button
        type="button"
        popoverTarget="workflows"
        aria-label={`Workflow: ${label}. Change workflow`}
        className="flex max-w-[48ch] min-w-0 items-center gap-1.5 rounded-full bg-s2 px-2.5 py-1 text-[12px] font-medium text-t2"
        style={{ anchorName: "--workflows" } as React.CSSProperties}
      >
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
        <div className="flex max-h-[60vh] flex-col gap-1 overflow-y-auto">
          <span className="heading px-3 pt-1">Templates</span>
          {templates.map((w) => (
            <div key={w.id} className="flex">
              {item(w)}
            </div>
          ))}
          <span className="heading px-3 pt-3">Mine</span>
          {!mine.length && (
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
