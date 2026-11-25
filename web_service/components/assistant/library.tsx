"use client";
// The library dialogs the assistant opens (plan 18 §22.5 "dialog", decision 5; board frame 6 "Clone voice"): the
// screen's own Save as look, New or Edit element, Edit look, Clone a voice and Design a voice, prefilled. Save (and
// the voice rights tick) is the user's click; the action resolves when the dialog closes, with the new id and
// @handle, or "cancelled". Starter looks and voices are refused.
import { useState } from "react";
import { ElementEditor } from "@/components/create/elements";
import { LookEditor, SaveAsLook } from "@/components/create/looks";
import { VoicePicker, type Prefill, type VoiceCtx } from "@/components/create/voices";
import { undoItem, useDialogWait } from "@/components/assistant/tools";
import { done, failed, type Outcome } from "@/lib/assistant";
import { api, ok, type S } from "@/lib/api";

type Take = S<"Take">;
type Item = S<"LibraryItem">;

/** `auto` (API 1.68): Auto mode let it save by itself (Save as look, a new element, Design a voice). */
export type LibraryAsk = (
  | { kind: "look"; take: Take; name?: string }
  | { kind: "element"; takes?: Take[]; handle?: string }
  | { kind: "edit"; item: Item }
  | ({ kind: "clone"; take: Take; ctx: VoiceCtx } & Prefill)
  | ({ kind: "design"; ctx: VoiceCtx } & Prefill)
) & { auto?: boolean };

/** The dialog's title, as its line says it: "Waiting for you · Save in Clone a voice". */
export function dialogTitle(a: LibraryAsk) {
  if (a.kind === "look") return "Save as look";
  if (a.kind === "element") return "New element";
  if (a.kind === "edit") return a.item.kind === "look" ? "Edit look" : "Edit element";
  return a.kind === "clone" ? "Clone a voice" : "Design a voice";
}

/** Why the item can't be edited here, else null. */
export function editRefusal(it: Item | undefined): string | null {
  if (!it) return "no such library item";
  if (it.source === "starter") return `${it.name} is a starter ${it.kind}: it can't be edited`;
  if (it.kind === "voice") return "a voice is renamed in the Voice picker's Mine";
  return null;
}

/** A take the screen shows, else read by its id (a take of another session); null when it is not the caller's. */
export async function takeById(id: unknown, shown: Take[]): Promise<Take | null> {
  if (typeof id !== "string") return null;
  return (
    shown.find((t) => t.id === id) ??
    (await ok(api.GET("/v1/studio/takes/{take_id}", { params: { path: { take_id: id } } })).catch(
      () => null,
    ))
  );
}

/** Whether a take can be a voice's clip: audio, or a video with sound. */
export const hasSound = (t: Take) =>
  t.kind === "audio" || (t.kind === "video" && !!t.video?.has_audio);

/** The screen's library dialogs for the assistant: `ask` opens one and resolves on its close; `node` renders it. */
export function useLibraryDialog() {
  const [open, setOpen] = useState<LibraryAsk | null>(null);
  const w = useDialogWait<Item>();
  const close = () => {
    setOpen(null);
    w.closed();
  };
  async function ask(a: LibraryAsk, progress: (text: string) => void): Promise<Outcome> {
    if (a.kind === "edit") {
      const why = editRefusal(a.item);
      if (why) return failed(`Edit · ${why}`);
    }
    const title = dialogTitle(a);
    const saved = w.wait();
    setOpen(a);
    progress(a.auto ? `Saving in ${title}…` : `Waiting for you · Save in ${title}`);
    const x = await saved;
    if (!x) return { status: "cancelled", text: `${title} · cancelled` };
    const name = x.handle ? `@${x.handle}` : x.name;
    // Auto (board frame 13): "Auto · Saved voice Elias", Undo deletes the new item
    return done(a.auto ? `Saved ${x.kind} ${name}` : `Saved ${name}`, {
      result: { item_id: x.id, kind: x.kind, name: x.name, ...(x.handle && { handle: name }) },
      ...(a.auto && { undo: { undo: () => undoItem(x.id) } }),
    });
  }
  const a = open;
  const node = !a ? null : a.kind === "look" ? (
    <SaveAsLook take={a.take} name={a.name} auto={a.auto} onSaved={w.saved} onClose={close} />
  ) : a.kind === "element" ? (
    <ElementEditor
      open
      takes={a.takes}
      handle={a.handle}
      auto={a.auto}
      onSaved={w.saved}
      onClose={close}
    />
  ) : a.kind === "edit" ? (
    a.item.kind === "look" ? (
      <LookEditor open item={a.item} onSaved={w.saved} onClose={close} />
    ) : (
      <ElementEditor open item={a.item} onSaved={w.saved} onClose={close} />
    )
  ) : (
    <VoicePicker
      open
      value=""
      onPick={() => undefined}
      onClose={close}
      onSaved={w.saved}
      ctx={a.ctx}
      take={a.kind === "clone" ? a.take : undefined}
      start={{
        view: a.kind,
        text: a.text,
        name: a.name,
        from_s: a.from_s,
        to_s: a.to_s,
        auto: a.kind === "design" && a.auto,
      }}
    />
  );
  return { ask, node };
}
