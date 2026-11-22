"use client";
// A pipeline's memory (docs/mock/JudgeReview.dc.html's aside, plan 16.6): its judge examples (your picks at a judge,
// with your note) and notes (a memory node's, or typed here), each with where it came from and Delete; Add a note.
// A batch reads the snapshot taken when it starts, so changes count from the next batch. Shown as the review page's
// aside, or in a dialog from the editor's Judge panel and the pipelines list (MemoryDialog).
import { useCallback, useEffect, useState } from "react";
import { ErrorNote, Info, Modal } from "@/components/kit";
import { api, message, ok, type S } from "@/lib/api";
import { safeUrl, when } from "@/lib/format";
import { useHydrated } from "@/lib/hydrated";
import { memorySource } from "@/lib/pipeline-run";
import { TextArea } from "@/components/expand-text";

type List = S<"MemoryList">;

/** The memory of one pipeline: loaded on mount (and after each change); `onCounts` hears the counts. */
export function useMemory(pipelineId: string | null) {
  const [list, setList] = useState<List | null>(null);
  const [error, setError] = useState("");
  const load = useCallback(async () => {
    if (!pipelineId) return;
    try {
      setList(
        await ok(api.GET("/v1/studio/memory", { params: { query: { pipeline_id: pipelineId } } })),
      );
      setError("");
    } catch (e) {
      setError(message(e));
    }
  }, [pipelineId]);
  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- the first read
    void load();
  }, [load]);
  return { list, error, load };
}

export function MemoryPanel({
  pipelineId,
  title,
  name = (x) => x,
  text: prefill = "",
}: {
  pipelineId: string;
  /** the pipeline's title */
  title: string;
  /** a node id as the panel names it */
  name?: (node: string) => string;
  /** Add a note prefilled (the assistant's addMemory) */
  text?: string;
}) {
  const { list, error, load } = useMemory(pipelineId);
  const hydrated = useHydrated();
  const [text, setText] = useState(prefill);
  const [busy, setBusy] = useState("");
  const [failed, setFailed] = useState("");

  async function remove(id: string) {
    setBusy(id);
    try {
      await ok(api.DELETE("/v1/studio/memory/{item_id}", { params: { path: { item_id: id } } }));
      await load();
    } catch (e) {
      setFailed(message(e));
    } finally {
      setBusy("");
    }
  }
  async function add() {
    if (!text.trim()) return;
    setBusy("add");
    try {
      await ok(
        api.POST("/v1/studio/memory", { body: { pipeline_id: pipelineId, text: text.trim() } }),
      );
      setText("");
      await load();
    } catch (e) {
      setFailed(message(e));
    } finally {
      setBusy("");
    }
  }

  const items = list?.items ?? [];
  const total = (list?.counts.judge_example ?? 0) + (list?.counts.note ?? 0);
  const section = (kind: S<"MemoryItem">["kind"], heading: string) => {
    const xs = items.filter((m) => m.kind === kind);
    return (
      <>
        <h3 className="heading">
          {heading} · {list?.counts[kind] ?? 0}
        </h3>
        {xs.length === 0 && (
          <p className="m-0 text-[12px] text-t2">
            {kind === "judge_example"
              ? "None yet: a pick at a judge with a note becomes one."
              : "None yet."}
          </p>
        )}
        {xs.map((m) => (
          <div key={m.id} className="set flex flex-col gap-1.5 text-[13px]">
            <span>“{m.text}”</span>
            {(m.kept.length > 0 || m.rejected.length > 0) && (
              <span
                className="flex flex-wrap gap-1"
                aria-label={`${m.kept.length} kept, ${m.rejected.length} passed over`}
              >
                {[
                  ...m.kept.map((t) => [t, true] as const),
                  ...m.rejected.map((t) => [t, false] as const),
                ].map(([t, kept]) => (
                  <img
                    key={t.id}
                    src={safeUrl(t.thumb_url)}
                    alt=""
                    title={kept ? "kept" : "passed over"}
                    className="h-6 w-9 rounded-[5px] bg-s4 object-cover"
                    style={kept ? undefined : { opacity: 0.45 }}
                  />
                ))}
              </span>
            )}
            <small className="text-[11px] text-t2">
              {kind === "note" && m.key ? `${m.key} · ` : ""}
              {memorySource(m, name)}
              {hydrated && ` · ${when(m.updated_at)}`}
            </small>
            <span className="flex justify-end">
              <button
                type="button"
                className="chip bevel h-[30px]"
                disabled={busy === m.id}
                aria-label={`Delete “${m.text.slice(0, 40)}”`}
                onClick={() => void remove(m.id)}
              >
                Delete
              </button>
            </span>
          </div>
        ))}
      </>
    );
  };

  return (
    <div className="flex flex-col gap-3.5">
      <div className="flex flex-col gap-1">
        <h2 className="m-0 flex items-center font-display text-base font-bold">
          Memory · {title}
          <Info>
            What this pipeline&apos;s judges and memory nodes know. A batch reads the snapshot taken
            when it starts; changes here count from the next batch.
          </Info>
        </h2>
        <span className="text-[12px] text-t2">{total} of 500 entries · read by the next batch</span>
      </div>
      {(error || failed) && (
        <ErrorNote onRetry={error ? load : undefined}>{error || failed}</ErrorNote>
      )}
      {list && section("judge_example", "Judge examples")}
      {list && section("note", "Notes")}
      <label htmlFor={`mem-note-${pipelineId}`} className="heading">
        Add a note
      </label>
      <TextArea
        id={`mem-note-${pipelineId}`}
        expand="Note"
        className="in h-auto py-2"
        rows={2}
        maxLength={2000}
        placeholder="What every judge of this pipeline should keep in mind"
        value={text}
        onChange={(e) => setText(e.target.value)}
      />
      <button
        type="button"
        className="chip bevel self-end"
        disabled={busy === "add" || !text.trim()}
        onClick={() => void add()}
      >
        Add note
      </button>
    </div>
  );
}

/** The Memory panel in a dialog (Open memory). */
export function MemoryDialog({
  open,
  onClose,
  ...props
}: Parameters<typeof MemoryPanel>[0] & { open: boolean; onClose: () => void }) {
  return (
    <Modal open={open} onClose={onClose} title="Memory" width="w-[min(92vw,440px)]">
      <div className="flex max-h-[70dvh] flex-col gap-3 overflow-y-auto">
        <MemoryPanel {...props} />
        <button type="button" className="chip bevel self-end" onClick={onClose}>
          Close
        </button>
      </div>
    </Modal>
  );
}
