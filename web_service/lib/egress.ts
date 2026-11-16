// What leaves the machine (contracts/README.md "Model sources": egress), as Submit shows it: per node where its calls
// go and exactly which parts of the prompt are sent (never thumbnails), the worst case for the batch's rows beside the
// pipeline's remote caps, and the memory local servers keep reserved.
import type { S } from "@/lib/api";

type Egress = S<"PipelineEgress">;

const n = (x: number) => x.toLocaleString("en-US");
const s = (x: number, w: string) => `${n(x)} ${w}${x === 1 ? "" : "s"}`;

/** One prompt part by name: `instruction`, `question`, `routes`, a source (`$prompt`, `judge.reasons`), a tool's
 * results (`tool:gallery.search`), the director's `chat`, a custom node's `input:<field>`. `source` names a source. */
export function fieldLabel(f: string, source: (src: string) => string = (x) => x): string {
  if (f === "instruction" || f === "question" || f === "routes") return `its ${f}`;
  if (f === "chat") return "the chat";
  if (f === "items") return "its list";
  if (f.startsWith("tool:")) return `${f.slice(5).replace(".", " ")} results`;
  if (f.startsWith("input:")) return `field ${f.slice(6)}`;
  return source(f);
}

export type EgressLine = {
  node: string;
  /** "Remote · fakeai · fake-large", "Local server · ollama · qwen2.5:7b", "Network" */
  where: string;
  remote: boolean;
  /** "its question, its routes, Judge · reasons; no thumbnails" */
  sends: string;
  /** "up to 6 calls, 53,952 tokens a row; 60 calls, 539,520 tokens for 10 rows" ("" for a network node) */
  load: string;
};

/** The egress entries in words, for `rows` rows. */
export function egressLines(
  egress: Egress[],
  rows: number,
  source?: (src: string) => string,
): EgressLine[] {
  return egress.map((e) => {
    const where =
      e.where === "remote"
        ? `Remote · ${e.provider} · ${e.model}`
        : e.where === "server"
          ? `Local server · ${e.provider} · ${e.model}`
          : "Network";
    const sends = e.fields.length
      ? `${e.fields.map((f) => fieldLabel(f, source)).join(", ")}; no thumbnails`
      : "no thumbnails";
    const r = e.per_row;
    const load = [
      r.calls
        ? `up to ${s(r.calls, "call")}, ${n(r.tokens)} tokens a row` +
          (rows > 1
            ? `; ${s(r.calls * rows, "call")}, ${n(r.tokens * rows)} tokens for ${rows} rows`
            : "")
        : "",
      e.chat ? `chat: up to ${s(e.chat.calls, "call")}, ${n(e.chat.tokens)} tokens` : "",
    ]
      .filter(Boolean)
      .join(" · ");
    return { node: e.node, where, remote: e.where !== "server", sends, load };
  });
}

/** The remote total for the batch beside the pipeline's caps: "at most 60 calls, 539,520 tokens; the batch stops at
 * 100 calls, 200,000 tokens" (the caps, not the estimate, stop it); "" when nothing goes to a remote provider. */
export function remoteLine(egress: Egress[], rows: number, remote?: S<"PipelineRemote">): string {
  const r = egress.filter((e) => e.where === "remote");
  if (!r.length) return "";
  const calls = r.reduce((x, e) => x + e.per_row.calls * rows + (e.chat?.calls ?? 0), 0);
  const tokens = r.reduce((x, e) => x + e.per_row.tokens * rows + (e.chat?.tokens ?? 0), 0);
  const capCalls = remote?.max_calls ?? 200;
  const capTokens = remote?.max_tokens ?? 400000;
  return `Remote, worst case: ${s(calls, "call")}, ${n(tokens)} tokens; the batch stops at ${s(capCalls, "call")}, ${n(capTokens)} tokens`;
}

/** The local servers' reserved memory: "ollama keeps 6.5 GB on mac". */
export const reservedLines = (r: S<"PipelineReserved">[]) =>
  r.map((x) => `${x.provider} keeps ${x.memory_gb} GB on ${x.host}`);
