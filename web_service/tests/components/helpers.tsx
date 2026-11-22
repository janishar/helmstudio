// Shared fixtures for the component tests (jsdom): takes, form fields, a stateful host for a control, and the api
// spy (the browser client goes to /api, which jsdom cannot reach).
import { useState } from "react";
import { vi } from "vitest";
import { api, type S } from "@/lib/api";
import type { Field, Raw, Update } from "@/lib/workflow";

type Take = S<"Take">;

let n = 0;
/** A take; `id` defaults to a fresh uuid-like id. */
export function take(over: Partial<Take> = {}): Take {
  n++;
  return {
    id: `00000000-0000-4000-8000-${String(n).padStart(12, "0")}`,
    kind: "image",
    source: "create",
    title: `Take ${n}`,
    url: `/files/u/x/${n}.png`,
    thumb_url: `/files/u/x/${n}.png`,
    content_type: "image/png",
    width: 512,
    height: 512,
    bytes: 1000,
    created_at: "2026-11-01T00:00:00Z",
    starred: false,
    session_id: null,
    run_id: null,
    made_with: null,
    parents: [],
    ...over,
  };
}

/** A video take of `seconds` at `fps` (448 × 256, with sound unless said). */
export const video = (seconds: number, fps = 24, over: Partial<Take> = {}) =>
  take({
    kind: "video",
    content_type: "video/mp4",
    width: 448,
    height: 256,
    video: {
      duration_s: seconds,
      fps,
      frames: Math.round(seconds * fps),
      has_audio: true,
      poster_url: "/files/u/x/p.png",
    },
    ...over,
  });

/** An audio take of `seconds`. */
export const audio = (seconds: number, over: Partial<Take> = {}) =>
  take({
    kind: "audio",
    content_type: "audio/wav",
    width: null,
    height: null,
    audio: { duration_s: seconds },
    ...over,
  });

/** A form field for `param` on node `node`. */
type Param = S<"ParamSchema">;
export function field(
  node: string,
  param: Omit<Param, "default"> & Partial<Pick<Param, "default">>,
  over: Partial<Field> = {},
): Field {
  return {
    key: `${node}.${param.name}`,
    node,
    nodeTitle: node,
    param: param as Param,
    initial: param.default ?? null,
    canvas: false,
    model: false,
    ...over,
  };
}

/** Renders `render(raw, onChange)` with the raw in state, as the form holds it; `seen` records every value set. */
export function Host({
  initial,
  render,
  seen,
}: {
  initial?: Raw;
  render: (raw: Raw | undefined, onChange: (k: string, u: Update) => void) => React.ReactNode;
  seen?: Raw[];
}) {
  const [raw, setRaw] = useState<Raw | undefined>(initial);
  const onChange = (_k: string, u: Update) =>
    setRaw((old) => {
      const next = typeof u === "function" ? u(old) : u;
      seen?.push(next);
      return next;
    });
  return <>{render(raw, onChange)}</>;
}

type Reply = { status?: number; data?: unknown; error?: unknown };
/** Answers api calls by path (the openapi path, e.g. "/v1/studio/takes/{take_id}"); unknown paths 404. */
export function answer(
  method: "GET" | "POST" | "PUT" | "PATCH" | "DELETE",
  routes: Record<string, (init: { params?: unknown; body?: unknown }) => Reply>,
) {
  return vi.spyOn(api, method).mockImplementation(((path: string, init: never) => {
    const r = routes[path]?.(init ?? {}) ?? { status: 404, error: { detail: "Not found." } };
    const status = r.status ?? 200;
    return Promise.resolve({
      data: status < 300 ? r.data : undefined,
      error: status < 300 ? undefined : r.error,
      response: new Response(null, { status }),
    });
  }) as never);
}
