"use client";
// The caller's elements in this tab (GET /v1/studio/library?kind=element, at most 200, not paginated): what marks
// unknown mentions and previews what the mentions add. Fetched once, again after an edit here.
import { useEffect, useReducer } from "react";
import { useUser } from "@/components/shell";
import { api, message, ok, type S } from "@/lib/api";

type Item = S<"LibraryItem">;

// ponytail: an edit in another tab shows here only after the next reload (picker open, edit, page load)
let items: Item[] | null = null;
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
  loading = null;
  failed = "";
  generation++;
}

/** Fetches the list again (after a create, edit or delete, or when the picker opens). */
export function reloadLibrary(): Promise<void> {
  const mine = generation;
  const read = ok(api.GET("/v1/studio/library", { params: { query: { kind: "element" } } }))
    .then((l) => {
      if (mine !== generation) return;
      items = l.items;
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

/** A group's label ("character" → "Character"), and the picker's tabs. */
export const GROUPS: [S<"LibraryGroup">, string, string][] = [
  ["character", "Character", "Characters"],
  ["location", "Location", "Locations"],
  ["prop", "Prop", "Props"],
];
export const groupLabel = (g: string) => GROUPS.find((x) => x[0] === g)?.[1] ?? g;
