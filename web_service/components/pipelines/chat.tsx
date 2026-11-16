"use client";
// The director's chat (docs/mock/DirectorChat.dc.html's aside, plan 16.4 Chat): the batch's messages oldest first, the
// director's state, each director message's tool calls collapsed, notices (a rejected row's reason, a cap) in the
// warning tone, and the composer. Live from the batch's events (`chat`, and `batch` for the state). Every message is
// rendered as text: React escapes it, nothing is parsed as HTML.
import { useCallback, useEffect, useRef, useState } from "react";
import { ErrorNote, Info } from "@/components/kit";
import { followBatch } from "@/components/pipelines/pipelines";
import { api, message, ok, type S } from "@/lib/api";
import {
  addMessage,
  callLine,
  chatClosed,
  directorState,
  withDirector,
  type ChatView,
} from "@/lib/chat";

const TONE: Record<S<"BatchDirector">["state"], string> = {
  idle: "st",
  waiting: "st st-wait",
  thinking: "st st-run",
  off: "st text-t2",
};

/** The batch's chat, loaded once `batchId` names a batch whose pipeline has a director, and kept live. */
export function useChat(batchId: string | null) {
  const [view, setView] = useState<ChatView | null>(null);
  const [error, setError] = useState("");
  const load = useCallback(async () => {
    if (!batchId) return;
    try {
      const p = await ok(
        api.GET("/v1/studio/batches/{batch_id}/chat", { params: { path: { batch_id: batchId } } }),
      );
      setView({ director: p.director, messages: p.messages });
      setError("");
    } catch (e) {
      setError(message(e));
    }
  }, [batchId]);
  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- the chat is read when its batch is known
    void load();
  }, [load]);
  const loaded = view !== null;
  useEffect(() => {
    if (!batchId || !loaded) return;
    return followBatch(batchId, ["chat", "batch"], (e) =>
      setView((v) =>
        !v
          ? v
          : e.type === "chat"
            ? addMessage(v, e.data)
            : e.type === "batch"
              ? withDirector(v, e.data.director)
              : v,
      ),
    );
  }, [batchId, loaded]);
  const send = useCallback(
    async (text: string) => {
      if (!batchId) return false;
      try {
        const m = await ok(
          api.POST("/v1/studio/batches/{batch_id}/chat", {
            params: { path: { batch_id: batchId } },
            body: { text },
          }),
        );
        setView((v) => v && addMessage(v, m));
        setError("");
        return true;
      } catch (e) {
        setError(message(e));
        return false;
      }
    },
    [batchId],
  );
  return { view, error, send, load };
}

export function ChatPanel({
  batch,
  chat,
  onClose,
}: {
  batch: S<"Batch">;
  chat: ReturnType<typeof useChat>;
  onClose?: () => void;
}) {
  const [text, setText] = useState("");
  const [sending, setSending] = useState(false);
  const end = useRef<HTMLDivElement>(null);
  const v = chat.view;
  const count = v?.messages.length ?? 0;
  useEffect(() => {
    end.current?.scrollIntoView({ block: "nearest" }); // an effect returns nothing (a returned promise would be called as cleanup)
  }, [count]);
  const closed = v ? chatClosed(batch, v.director) : "";
  async function submit(e: React.FormEvent) {
    e.preventDefault();
    const t = text.trim();
    if (!t || sending) return;
    setSending(true);
    if (await chat.send(t)) setText("");
    setSending(false);
  }
  return (
    <section aria-label="Chat with director" className="flex min-h-0 flex-1 flex-col gap-3">
      <div className="flex items-center justify-between gap-2">
        <h2 className="m-0 flex items-center font-display text-base font-bold">
          Director
          <Info>
            It has its own thread for this batch. It never waits on runs: it answers, searches the
            gallery, reads the library and memory, adds rows and changes rows not yet started, each
            through Submit&apos;s checks. It wakes on your message or when a row ends.
          </Info>
        </h2>
        <span className="flex items-center gap-2">
          {v && (
            <span className={TONE[v.director.state]}>
              {directorState(v.director).split(" · ")[0]}
            </span>
          )}
          {onClose && (
            <button type="button" className="chip h-7" onClick={onClose}>
              Close
            </button>
          )}
        </span>
      </div>
      {v && (
        <span className="text-[12px] text-t2">
          wake {v.director.wakes} of at most {v.director.max_wakes} · {directorState(v.director)}
        </span>
      )}
      {chat.error && <ErrorNote onRetry={chat.load}>{chat.error}</ErrorNote>}
      <div className="flex min-h-0 flex-1 flex-col gap-2.5 overflow-y-auto" aria-live="polite">
        {v?.messages.length === 0 && (
          <p className="m-0 text-[13px] text-t2">
            No messages yet. Ask it something, or tell it which rows to add or change.
          </p>
        )}
        {v?.messages.map((m) => (
          <div
            key={m.id}
            className={`max-w-[92%] rounded-[18px] px-3 py-2 text-[13px] leading-[19px] break-words whitespace-pre-wrap ${
              m.role === "user"
                ? "self-end rounded-br-md bg-s4"
                : m.role === "notice"
                  ? "self-start text-[12px] text-[#f2c14e] shadow-[inset_0_0_0_1px_#f2c14e]"
                  : "self-start rounded-bl-md bg-s2"
            }`}
          >
            {m.text}
            {m.tool_calls.length > 0 && (
              <details className="mt-1.5 text-[12px] text-t2">
                <summary className="cursor-pointer">
                  {m.tool_calls.length} tool call{m.tool_calls.length === 1 ? "" : "s"}
                </summary>
                {m.tool_calls.map((c) => (
                  <div key={c.id} className="mono pt-0.5 pl-3 text-[11px]">
                    {callLine(c)}
                  </div>
                ))}
              </details>
            )}
          </div>
        ))}
        <div ref={end} />
      </div>
      <form className="flex flex-col gap-2" onSubmit={(e) => void submit(e)}>
        <label htmlFor="chat-in" className="sr-only">
          Message the director
        </label>
        <textarea
          id="chat-in"
          className="in h-auto py-2"
          rows={3}
          maxLength={2000}
          disabled={!!closed || !v}
          placeholder={closed || "Ask, or tell it which rows to add or change"}
          value={text}
          onChange={(e) => setText(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && (e.metaKey || e.ctrlKey))
              (e.currentTarget.form as HTMLFormElement).requestSubmit();
          }}
        />
        <div className="flex items-center justify-between gap-2">
          <small className="text-[11px] text-t2">at most 6 a minute</small>
          <button
            type="submit"
            className="key h-10 px-[18px]"
            disabled={!!closed || !v || sending || !text.trim()}
          >
            Send
          </button>
        </div>
      </form>
    </section>
  );
}
