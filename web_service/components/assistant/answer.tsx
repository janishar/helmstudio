"use client";
// Pick one cards and id chips in the Assistant's answers (docs/mock/Assistant.dc.html frames 10b and 11; alpha.2 items
// 17, 19; contracts/README.md, Assistant: pick one and refs). Plain elements, never HTML; never a raw id.
import { useEffect, useReducer, useState } from "react";
import { AudioLines, Check, Trash2, Workflow } from "lucide-react";
import { ElementEditor } from "@/components/create/elements";
import { FullScreen } from "@/components/media";
import { api, ok, type S } from "@/lib/api";
import { pickOf, refParts, type Option, type RefKind } from "@/lib/answer-refs";
import type { Entry } from "@/lib/assistant";
import { safeUrl } from "@/lib/format";
import { reloadLibrary, useLibrary, useVoices } from "@/lib/library";
import { useTakes } from "@/lib/take-cache";

type Switch = ((id: string) => void) | undefined;

/** An answer's options as cards (frame 11): title and one line, the suggested one ringed "Best next", keys 1–4. A
 * click sends the title as the user's message; once a message came after it the pick stays marked, the rest fade. */
export function Options({
  entry,
  entries,
  options,
  readOnly,
  send,
}: {
  entry: Entry;
  entries: Entry[];
  options: Option[];
  readOnly: boolean;
  send: (text: string) => unknown;
}) {
  const { live, picked } = pickOf(entries, entry.id, options);
  const off = !live || readOnly;
  return (
    <div className="opts" role="group" aria-label="Pick one">
      {options.map((o, i) => {
        const on = o.title === picked;
        return (
          <button
            key={o.title}
            type="button"
            disabled={off}
            className={`opt${o.best ? " rec" : ""}${on ? " on" : ""}${picked && !on ? " fade" : ""}`}
            onClick={() => void send(o.title)}
          >
            <span className="kbd">{i + 1}</span>
            <span className={`min-w-0${o.best && !off ? " pr-16" : ""}`}>
              <b>{o.title}</b>
              {o.line && <small>{o.line}</small>}
            </span>
            {o.best && !off && <span className="tag">Best next</span>}
            {on && <Check size={14} strokeWidth={2.5} aria-hidden className="ml-auto shrink-0" />}
          </button>
        );
      })}
    </div>
  );
}

/** Text with its refs as chips; the text between them goes to `plain`. */
export function Refs({
  text,
  plain,
  onWorkflow,
}: {
  text: string;
  plain: (t: string, key: number) => React.ReactNode;
  onWorkflow?: Switch;
}) {
  return (
    <>
      {refParts(text).map((p, i) =>
        "t" in p ? plain(p.t, i) : <Chip key={i} kind={p.kind} id={p.id} onWorkflow={onWorkflow} />,
      )}
    </>
  );
}

// the caller's workflows by id, read once per tab (again once when a ref names one not in it)
let flows: S<"WorkflowList">["workflows"] | null = null;
let reading: Promise<void> | null = null;
const flowSubs = new Set<() => void>();
function readFlows() {
  reading ??= ok(api.GET("/v1/studio/workflows"))
    .then((r) => void (flows = r.workflows))
    .catch(() => undefined)
    .finally(() => {
      reading = null;
      flowSubs.forEach((f) => f());
    });
}
function useWorkflows() {
  const [, bump] = useReducer((n: number) => n + 1, 0);
  useEffect(() => {
    flowSubs.add(bump);
    if (!flows) readFlows();
    return () => void flowSubs.delete(bump);
  }, []);
  return flows;
}

const again = new Set<string>(); // ids not found once: the list was read again for them

/** One ref as a chip: picture (or icon) and title; a take opens its preview, an element its editor, a workflow is
 * switched to (on Create). A deleted one says "deleted take"; one the model was never given, "unknown take". */
function Chip({ kind, id, onWorkflow }: { kind: RefKind; id: string; onWorkflow?: Switch }) {
  const take = useTakes(kind === "take" && id !== "?" ? [id] : [])(id);
  const elements = useLibrary();
  const voices = useVoices();
  const workflows = useWorkflows();
  const [open, setOpen] = useState(false);
  const list =
    kind === "element"
      ? elements
      : kind === "voice"
        ? voices
        : kind === "workflow"
          ? workflows
          : null;
  const item = list?.find((x) => x.id === id);
  useEffect(() => {
    if (id === "?" || kind === "take" || !list || item || again.has(id)) return;
    again.add(id); // made since the list was read?
    if (kind === "workflow") readFlows();
    else void reloadLibrary();
  }, [id, kind, list, item]);
  const lib =
    kind === "element" || kind === "voice" ? (item as S<"LibraryItem"> | undefined) : undefined;
  const title =
    kind === "take"
      ? take?.title
      : kind === "element"
        ? lib && `@${lib.handle ?? lib.name}`
        : kind === "voice"
          ? lib?.name
          : (item as { title: string } | undefined)?.title;
  const gone = id === "?" || (kind === "take" ? take === null : !!list && !item && again.has(id));
  if (gone || !title)
    return (
      <span
        className={`idc${gone ? " gone" : ""}`}
        title={
          id === "?"
            ? `The assistant named a ${kind} it was not given`
            : gone
              ? `This ${kind} was deleted`
              : undefined
        }
      >
        <i>{gone && <Trash2 size={11} strokeWidth={2} aria-hidden />}</i>
        {gone ? `${id === "?" ? "unknown" : "deleted"} ${kind}` : "…"}
      </span>
    );
  const thumb = safeUrl(
    kind === "take" ? take?.thumb_url : kind === "element" ? lib?.thumb_url : null,
  );
  const pic = thumb ? (
    <i className="pic" style={{ backgroundImage: `url("${thumb}")` }} />
  ) : (
    <i>
      {kind === "voice" ? (
        <AudioLines size={11} strokeWidth={2} aria-hidden />
      ) : kind === "workflow" ? (
        <Workflow size={11} strokeWidth={2} aria-hidden />
      ) : null}
    </i>
  );
  const act =
    kind === "take" || kind === "element"
      ? () => setOpen(true)
      : kind === "workflow" && onWorkflow
        ? () => onWorkflow(id)
        : undefined;
  if (!act)
    return (
      <span className="idc" title={title} aria-label={`${kind}: ${title}`}>
        {pic}
        {title}
      </span>
    );
  return (
    <>
      <button
        type="button"
        className="idc"
        title={`Open ${title}`}
        aria-label={`${kind}: ${title}`}
        onClick={act}
      >
        {pic}
        {title}
      </button>
      {open && take && (
        <FullScreen take={take} onClose={() => setOpen(false)} onStep={() => undefined} />
      )}
      {kind === "element" && (
        <ElementEditor open={open} onClose={() => setOpen(false)} item={lib} />
      )}
    </>
  );
}
