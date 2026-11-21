"use client";
// The caller's elements, looks and voices in this tab (GET /v1/studio/library?kind=element, look and voice: own items at most
// 200, plus the live hosts' starter looks; not paginated): what marks unknown mentions, previews what the mentions and
// the chosen looks add, and fills the visual picker. Fetched once, again after an edit here.
import { useEffect, useReducer } from "react";
import { useUser } from "@/components/shell";
import { api, message, ok, type S } from "@/lib/api";

type Item = S<"LibraryItem">;

// ponytail: an edit in another tab shows here only after the next reload (picker open, edit, page load)
let items: Item[] | null = null;
// looks are read apart: an older studio that knows no looks still serves the elements
let looks: Item[] | null = null;
let looksFailed = "";
// voices (API 1.30) are read apart too
let voices: Item[] | null = null;
let voicesFailed = "";
let loading: Promise<void> | null = null;
let failed = "";
// whose list this is: a sign-out and sign-in in the same tab is a soft navigation that keeps this module
let owner: string | null = null;
let generation = 0;
const subs = new Set<() => void>();

/** Forgets the list (and drops a read in flight) when the signed-in user is another one. */
function ownedBy(id: string | null) {
  if (id === owner) return;
  owner = id;
  items = null;
  looks = null;
  voices = null;
  loading = null;
  failed = "";
  looksFailed = "";
  voicesFailed = "";
  generation++;
}

/** Fetches the list again (after a create, edit or delete, or when the picker opens). */
export function reloadLibrary(): Promise<void> {
  const mine = generation;
  const kind = (k: "element" | "look" | "voice") =>
    ok(api.GET("/v1/studio/library", { params: { query: { kind: k } } }));
  const read = Promise.allSettled([kind("element"), kind("look"), kind("voice")])
    .then(([e, l, v]) => {
      if (mine !== generation) return;
      if (e.status === "fulfilled") [items, failed] = [e.value.items, ""];
      else failed = message(e.reason);
      if (l.status === "fulfilled") [looks, looksFailed] = [l.value.items, ""];
      else looksFailed = message(l.reason);
      if (v.status === "fulfilled") [voices, voicesFailed] = [v.value.items, ""];
      else voicesFailed = message(v.reason);
    })
    .finally(() => {
      if (loading === read) loading = null;
      subs.forEach((f) => f());
    });
  loading = read;
  return read;
}

/** The signed-in user's elements, or null until they are known (or when they could not be read: nothing is marked
 * then). */
export function useLibrary(): Item[] | null {
  const [, bump] = useReducer((n: number) => n + 1, 0);
  const id = useUser()?.id ?? null;
  ownedBy(id);
  useEffect(() => {
    subs.add(bump);
    if (!items && !loading) void reloadLibrary();
    return () => void subs.delete(bump);
  }, [id]);
  return items;
}

/** Why the last read failed ("" when it did not); read while rendering after useLibrary. */
export const libraryError = () => failed;

/** The signed-in user's looks and the live starter looks, or null until they are known (or could not be read). */
export function useLooks(): Item[] | null {
  useLibrary();
  return looks;
}

/** The signed-in user's voices and the live starter voices (API 1.30), or null until they are known. */
export function useVoices(): Item[] | null {
  useLibrary();
  return voices;
}

/** Why the last read of the voices failed ("" when it did not): a voice tile says so instead of waiting. */
export const voicesError = () => voicesFailed;

/** Why the last read of the looks failed ("" when it did not). */
export const looksError = () => looksFailed;

/** A group's label ("character" → "Character"), and the picker's tabs. */
export const GROUPS: [S<"LibraryGroup">, string, string][] = [
  ["character", "Character", "Characters"],
  ["location", "Location", "Locations"],
  ["prop", "Prop", "Props"],
];
export const groupLabel = (g: string) => GROUPS.find((x) => x[0] === g)?.[1] ?? g;
