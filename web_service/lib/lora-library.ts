"use client";
// The caller's LoRA library in this tab (GET /v1/studio/loras, not paginated): the picker, the rows' names and
// problems, the graph rules' lora_* codes. Fetched once, again when the picker opens and after an edit here.
import { useEffect, useReducer } from "react";
import { useUser } from "@/components/shell";
import { api, message, ok, type S } from "@/lib/api";

type Lora = S<"Lora">;

// ponytail: like lib/library.ts, a change in another tab shows here only after a reload (the picker reloads on open)
let items: Lora[] | null = null;
let usage: S<"LoraUsage"> | null = null;
let loading: Promise<void> | null = null;
let failed = "";
let owner: string | null = null;
let generation = 0;
const subs = new Set<() => void>();

function ownedBy(id: string | null) {
  if (id === owner) return;
  owner = id;
  items = null;
  usage = null;
  loading = null;
  failed = "";
  generation++;
}

/** Fetches the library again (after an upload, rename or delete, or when the picker opens). */
export function reloadLoras(): Promise<void> {
  const mine = generation;
  const read = ok(api.GET("/v1/studio/loras"))
    .then((l) => {
      if (mine !== generation) return;
      items = l.loras;
      usage = l.usage ?? null;
      failed = "";
    })
    .catch((e) => {
      if (mine !== generation) return;
      failed = message(e);
    })
    .finally(() => {
      if (loading === read) loading = null;
      subs.forEach((f) => f());
    });
  loading = read;
  return read;
}

/** Puts a LoRA into the list as it is now (an upload's progress, a rename), or drops it (`gone`). */
export function patchLoras(l: Lora, gone = false) {
  if (!items) return;
  items = gone
    ? items.filter((x) => x.id !== l.id)
    : items.some((x) => x.id === l.id)
      ? items.map((x) => (x.id === l.id ? l : x))
      : [l, ...items];
  subs.forEach((f) => f());
}

/** The signed-in user's LoRA library, or null until it is known (or when it could not be read). */
export function useLoras(): Lora[] | null {
  const [, bump] = useReducer((n: number) => n + 1, 0);
  const id = useUser()?.id ?? null;
  ownedBy(id);
  useEffect(() => {
    subs.add(bump);
    if (!items && !loading) void reloadLoras();
    return () => void subs.delete(bump);
  }, [id]);
  return items;
}

/** The caller's own LoRAs against the quota, from the last read (null before it, or from an older server). */
export const lorasUsage = () => usage;

/** Why the last read failed ("" when it did not). */
export const lorasError = () => failed;

/** Renames an own LoRA; the answer replaces it in the list. */
export async function renameLora(id: string, name: string) {
  const l = await ok(
    api.PATCH("/v1/studio/loras/{lora_id}", { params: { path: { lora_id: id } }, body: { name } }),
  );
  patchLoras(l);
  return l;
}

/** Deletes an own LoRA (never refused) and drops it from the list. */
export async function deleteLora(l: Lora) {
  await ok(api.DELETE("/v1/studio/loras/{lora_id}", { params: { path: { lora_id: l.id } } }));
  patchLoras(l, true);
}
