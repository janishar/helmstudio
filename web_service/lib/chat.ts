// The director's chat (contracts/README.md "The director and its chat"): the messages as GET …/chat gives them and as
// `chat` events add them, the director's state in words, the rows it added or changed (marked in the run table), and
// whether a message can be sent now. Everything the model wrote is text: the screen renders it as text, never HTML.
import type { S } from "@/lib/api";

type Message = S<"ChatMessage">;
type Director = S<"BatchDirector">;
type Call = S<"ToolCall">;
export type ChatView = { director: Director; messages: Message[] };

/** A message added (from a `chat` event or POST's 202): once per id, oldest first. */
export function addMessage(v: ChatView, m: Message): ChatView {
  if (v.messages.some((x) => x.id === m.id)) return v;
  const messages = [...v.messages, m].sort((a, b) => a.at.localeCompare(b.at));
  return { ...v, messages };
}

/** The view after a `batch` event: its director's state. */
export const withDirector = (v: ChatView, d: Director | null | undefined): ChatView =>
  d ? { ...v, director: d } : v;

/** The director's state in words (Batch.director.state). */
export function directorState(d: Director): string {
  switch (d.state) {
    case "idle":
      return "Idle · wakes on a message or a row ending";
    case "waiting":
      return "Waiting · its turn is in the queue";
    case "thinking":
      return "Thinking";
    default:
      return d.wakes >= d.max_wakes ? `Off · used its ${d.max_wakes} wakes` : "Off";
  }
}

/** Why a message can't be sent now (POST …/chat answers 409 then), or "". */
export function chatClosed(b: S<"Batch">, d: Director): string {
  if (["done", "failed", "stopped"].includes(b.status)) return "The batch has ended.";
  if (d.state === "off") return "The director is off.";
  return "";
}

const rowOf = (r: Call["result"]): number | null => {
  const x = (r as unknown as { row?: unknown } | null)?.row;
  if (typeof x === "number") return x;
  const n = (x as { row?: unknown } | null)?.row;
  return typeof n === "number" ? n : null;
};

/** The rows the director added or changed, from its messages' tool calls (a refused call marks nothing): row → how
 * and in which wake. A row it changed after adding it stays "added". */
export function rowMarks(
  messages: Message[],
): Record<number, { how: "added" | "changed"; wake: number }> {
  const out: Record<number, { how: "added" | "changed"; wake: number }> = {};
  for (const m of messages) {
    if (m.role !== "director") continue;
    for (const c of m.tool_calls) {
      if (c.error || (c.tool !== "rows_add" && c.tool !== "rows_edit")) continue;
      const row =
        rowOf(c.result) ??
        (c.tool === "rows_edit" ? ((c.args as Record<string, unknown>).row as number) : null);
      if (typeof row !== "number") continue;
      const how = c.tool === "rows_add" || out[row]?.how === "added" ? "added" : "changed";
      out[row] = { how, wake: m.wake ?? 0 };
    }
  }
  return out;
}

const TOOL_WORDS: Record<Call["tool"], string> = {
  gallery_search: "searched the gallery",
  library_read: "read the library",
  memory_read: "read memory",
  memory_write: "wrote a note",
  rows_add: "added a row",
  rows_edit: "changed a row",
  route: "routed",
  set_items: "answered a list",
};

/** One tool call in words: "added row 15", "searched the gallery · 3 found", "changed a row: refused · <why>". */
export function callLine(c: Call): string {
  const what = TOOL_WORDS[c.tool] ?? c.tool;
  if (c.error) return `${what}: refused · ${c.error}`;
  const row = rowOf(c.result);
  if ((c.tool === "rows_add" || c.tool === "rows_edit") && row !== null)
    return `${c.tool === "rows_add" ? "added" : "changed"} row ${row}`;
  const found = (c.result as unknown as { items?: unknown[]; takes?: unknown[] } | null) ?? {};
  const list = found.items ?? found.takes;
  if (Array.isArray(list)) return `${what} · ${list.length} found`;
  if (c.tool === "route")
    return `routed to ${String((c.args as Record<string, unknown>).to ?? "")}`;
  if (c.tool === "set_items") {
    const items = (c.args as Record<string, unknown>).items;
    const k = Array.isArray(items) ? items.length : 0;
    return `answered ${k} item${k === 1 ? "" : "s"}`;
  }
  return what;
}
