// Browser-side typed client: every call goes to the BFF (/api/*), which adds the API key and the user's token.
import createClient from "openapi-fetch";
import type { components, paths } from "@/gen/api";

/** Shorthand for a contract schema: S<"Run"> is components["schemas"]["Run"]. */
export type S<K extends keyof components["schemas"]> = components["schemas"][K];

/** The typed client: paths and bodies follow contracts/openapi.yaml. */
export const api = createClient<paths>({ baseUrl: "/api" });

/** A failed API call: `message` is fit to show the user, `status` is the HTTP status. */
export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message);
  }
}

/** A readable message for an error response: `{detail}` or Kong's `{message}`. */
export function errorText(status: number, body: unknown): string {
  const b = body as { detail?: unknown; message?: unknown } | null;
  if (typeof b?.detail === "string" && b.detail) return b.detail;
  if (typeof b?.message === "string") return b.message;
  if (status === 401) return "Your session ended. Sign in again.";
  if (status === 404) return "Not found.";
  if (status === 413) return "Too large.";
  if (status >= 500) return `The server had a problem (${status}). Try again.`;
  return `Request failed (${status}).`;
}

/** On a 401 in the browser, goes to /signin and comes back to this page afterwards. */
function signedOut(status: number) {
  if (status === 401 && typeof window !== "undefined" && !location.pathname.startsWith("/signin")) {
    // eslint-disable-next-line @next/next/no-location-assign-relative-destination -- outside React: a full load
    location.href = `/signin?next=${encodeURIComponent(location.pathname + location.search)}`;
  }
}

/** Unwraps an openapi-fetch result: the data, or an ApiError with the server's message. */
export async function ok<T>(
  p: Promise<{ data?: T; error?: unknown; response: Response }>,
): Promise<T> {
  const { data, error, response } = await p;
  if (response.ok) return data as T;
  signedOut(response.status);
  throw new ApiError(response.status, errorText(response.status, error));
}

/** `ok`, but an answer with status `expected` (e.g. 409: already done) is null instead of an error. */
export async function okOr<T>(
  p: Promise<{ data?: T; error?: unknown; response: Response }>,
  expected: number,
): Promise<T | null> {
  try {
    return await ok(p);
  } catch (e) {
    if (e instanceof ApiError && e.status === expected) return null;
    throw e;
  }
}

/** Any caught value as text for a toast or error note. */
export const message = (e: unknown) => (e instanceof Error ? e.message : String(e));
