"use client";
// Takes known by id in this tab (references show their thumbnails from here): fetched once each from
// GET /v1/studio/takes/{id}, or remembered from a pick or an upload.
import { useEffect, useReducer } from "react";
import { api, ApiError, ok, type S } from "@/lib/api";

type Take = S<"Take">;

// ponytail: kept for the tab's life; signed thumb links expire, so a page open for hours may show broken thumbnails
const cache = new Map<string, Take | null>();
const pending = new Set<string>();
const subs = new Set<() => void>();
const notify = () => subs.forEach((f) => f());

/** Takes already in hand (a pick, an upload) need no fetch. */
export function remember(takes: Take[]) {
  takes.forEach((t) => cache.set(t.id, t));
  notify();
}

/** Looks up takes by id, fetching the ones not known yet: a take, undefined while loading, null when it is gone
 * (deleted, or not the caller's). */
export function useTakes(ids: string[]): (id: string) => Take | null | undefined {
  const [, bump] = useReducer((n: number) => n + 1, 0);
  useEffect(() => {
    subs.add(bump);
    return () => void subs.delete(bump);
  }, []);
  const key = ids.join(",");
  useEffect(() => {
    for (const id of key ? key.split(",") : []) {
      if (cache.has(id) || pending.has(id)) continue;
      pending.add(id);
      ok(api.GET("/v1/studio/takes/{take_id}", { params: { path: { take_id: id } } }))
        .then((t) => cache.set(id, t))
        .catch((e) => e instanceof ApiError && e.status === 404 && cache.set(id, null))
        .finally(() => {
          pending.delete(id);
          notify();
        });
    }
  }, [key]);
  return (id) => cache.get(id);
}
