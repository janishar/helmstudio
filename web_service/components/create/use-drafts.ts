// Create's half-written prompt or script per session and side (owner, 2026-11-25; alpha.2 item 14), kept in this
// browser until Generate sends it.
import { useEffect, useRef, type Dispatch, type RefObject, type SetStateAction } from "react";
import type { S } from "@/lib/api";
import { stored } from "@/lib/hydrated";
import { SIDES, sideOf, type Side } from "@/lib/media";
import type { Field, Raw } from "@/lib/workflow";

/** The browser's key of a session's half-written prompt or script ("" before the first session), one per side
 * (picture, sound: sideOf), so neither replaces the other. */
const draftKey = (session: string | null, side: Side) => `create-draft:${session ?? ""}:${side}`;

/** Reads the session's drafts into the form (`restored`: the first open came back with a form of its own: no read)
 * and keeps the prompt as it is typed; returns what Generate calls once it sent `text` from session `sid`. */
export function useDrafts({
  sessionId,
  workflows,
  workflow,
  all,
  held,
  setRaws,
  keptRef,
  restored,
}: {
  sessionId: string | null;
  workflows: S<"Workflow">[] | null;
  workflow: S<"Workflow"> | undefined;
  all: Field[];
  held: Record<string, Raw>;
  setRaws: Dispatch<SetStateAction<Record<string, Raw>>>;
  /** the other side's draft waits here for a switch (Create's keptTexts) */
  keptRef: RefObject<Record<string, Record<string, Raw>>>;
  restored: boolean;
}) {
  // a half-written prompt survives a reload (owner, 2026-11-25): kept in this browser per session (a reload starts
  // with an empty leftForm), dropped once Generate sends it; switching to another session brings back its own
  const promptKey = (
    all.find((f) => f.param.type === "text" && f.param.name === "text") ??
    all.find((f) => f.param.type === "text")
  )?.key;
  const draft = promptKey ? held[promptKey] : undefined;
  const sentDraft = useRef<unknown>(undefined);
  const draftRead = useRef<string | null | undefined>(undefined); // the session whose draft was read
  // each side's draft (alpha.2 item 14): the open workflow's side goes into the form, the other waits in keptTexts
  // for a switch (carryKind), so a script never lands in a picture prompt after a reload, nor the reverse
  const side = workflow ? sideOf(workflow) : undefined;
  useEffect(() => {
    if (!workflows || !side || draftRead.current === sessionId) return;
    const first = draftRead.current === undefined;
    draftRead.current = sessionId;
    if (first && restored) return;
    try {
      for (const s of SIDES) {
        const d = JSON.parse(localStorage.getItem(draftKey(sessionId, s)) ?? "null");
        if (!d?.key || typeof d.text !== "string") continue;
        // only the words: the workflow follows the session as after any reload (its other inputs are not kept)
        if (s === side) setRaws((h) => ({ ...h, [d.key]: d.text }));
        else keptRef.current[s] = { [d.key]: d.text };
      }
    } catch {
      /* no storage here: the draft is not kept */
    }
  }, [workflows, side, restored, sessionId, setRaws, keptRef]);
  useEffect(() => {
    if (draftRead.current !== sessionId || !promptKey || !side) return;
    try {
      if (typeof draft === "string" && draft.trim() && draft !== sentDraft.current)
        localStorage.setItem(
          draftKey(sessionId, side),
          JSON.stringify({ key: promptKey, text: draft }),
        );
      else localStorage.removeItem(draftKey(sessionId, side));
    } catch {
      /* no storage here */
    }
  }, [draft, promptKey, sessionId, side]);
  /** Generate sent `text`: it is not kept again, and this session's and `sid`'s drafts of `s` are dropped. */
  return (text: unknown, sid: string | null, s: Side) => {
    sentDraft.current = text;
    stored.set(draftKey(sessionId, s), null);
    stored.set(draftKey(sid, s), null);
  };
}
