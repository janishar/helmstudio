// The Timeline editor's edits (plan 24 28.8 A): each op shows at once as the draft, is saved in order through
// /edits on the revision it was made on (a 409 reloads, says so and re-applies it), and the server's answer replaces
// the draft; server-side changes (undo, redo, add clips) queue after the edits in flight.
import {
  useCallback,
  useRef,
  type Dispatch,
  type ReactNode,
  type RefObject,
  type SetStateAction,
} from "react";
import { shotName } from "@/components/timeline/lanes";
import { ApiError, editTimeline, editTimelineRetry, getTimeline, message, type S } from "@/lib/api";
import { apply, factsOf, say, sayProblems, type Facts, type Norm, type Op } from "@/lib/timeline";
import { describe, noun, withIds } from "@/lib/timeline-tools";

type Timeline = S<"Timeline">;

/** A clip's name in the live region ("Shot 3", a take's title). */
function nameIn(d: Norm, cid: string, takes: Timeline["takes"]): string {
  const ti = d.tracks.findIndex((t) => t.clips.some((c) => c.id === cid));
  const c = d.tracks[ti]?.clips.find((x) => x.id === cid);
  if (!c) return "the clip";
  const title = c.take_id ? takes[c.take_id]?.title : undefined;
  return ti === 0 ? shotName(c, title ?? "the empty slot") : (title ?? "the line");
}

export function useTimelineEdits({
  tlRef,
  setTl,
  draft,
  setDraft,
  facts,
  setNotice,
  setError,
  setSaid,
}: {
  /** the latest stored sequence */
  tlRef: RefObject<Timeline | null>;
  setTl: Dispatch<SetStateAction<Timeline | null>>;
  draft: Norm | null;
  setDraft: Dispatch<SetStateAction<Norm | null>>;
  facts: Record<string, Facts>;
  setNotice: Dispatch<SetStateAction<ReactNode>>;
  setError: (e: string) => void;
  setSaid: (s: string) => void;
}) {
  const queue = useRef<Promise<void>>(Promise.resolve());
  const pending = useRef(0);

  const reload = useCallback(async () => {
    const cur = tlRef.current;
    if (!cur) return;
    const t = await getTimeline(cur.id);
    if (!pending.current) setTl(t);
  }, [tlRef, setTl]);

  /** Saves `op` on the latest stored revision through /edits; a 409 reloads and tries once more on the new one. */
  const save = useCallback(
    async (op: Op): Promise<void> => {
      let cur = tlRef.current;
      try {
        await editTimelineRetry(
          async (retried) => {
            cur = tlRef.current;
            if (!cur) return;
            const r = apply(cur, op, factsOf(cur.takes));
            // it no longer fits the newer revision (a slide past a handle another tab just trimmed): dropped, and said
            // (plan 23 §27.7)
            if (!("document" in r))
              return setNotice(
                `The sequence changed: your ${noun(op)} no longer fits and was not saved.`,
              );
            const t = await editTimeline(cur, [op]);
            tlRef.current = t;
            setTl(t);
            if (retried)
              setNotice(
                "The sequence changed since you opened it: reloaded, and your edit applied to it.",
              );
          },
          async () => {
            const t = await getTimeline(cur!.id);
            tlRef.current = t;
            setTl(t);
          },
        );
      } catch (e) {
        setError(message(e));
        await reload().catch(() => undefined);
      }
    },
    [reload, tlRef, setTl, setNotice, setError],
  );

  const commit = useCallback(
    (raw: Op) => {
      const b = draft ?? tlRef.current;
      if (!b) return;
      const op = withIds(raw, b); // the ids it makes, the same on a 409 re-apply
      const r = apply(b, op, facts);
      if ("error" in r) return setNotice(say(r.error));
      if ("problems" in r) return setNotice(sayProblems(r.problems));
      setNotice("");
      setError("");
      setSaid(describe(op, b, facts, (cid) => nameIn(b, cid, tlRef.current?.takes ?? {})));
      setDraft(r.document);
      pending.current++;
      queue.current = queue.current.then(async () => {
        await save(op);
        if (!--pending.current) setDraft(null);
      });
    },
    [draft, facts, save, tlRef, setDraft, setNotice, setError, setSaid],
  );

  /** A server-side change (undo, redo, add clips…) after the edits in flight. */
  const server = useCallback(
    (f: (t: Timeline) => Promise<Timeline>) => {
      queue.current = queue.current.then(async () => {
        const cur = tlRef.current;
        if (!cur) return;
        try {
          const t = await f(cur);
          tlRef.current = t;
          setTl(t);
          setDraft(null);
        } catch (e) {
          if (e instanceof ApiError && e.status === 409) {
            await reload().catch(() => undefined);
            setNotice("The sequence changed since you opened it: reloaded.");
          } else setError(message(e));
        }
      });
    },
    [reload, tlRef, setTl, setDraft, setNotice, setError],
  );

  return { queueRef: queue, reload, commit, server };
}
