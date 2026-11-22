// The password action: PUT /auth/password, then this browser signs in again (the change ends every session); a wrong
// current password, the 5th of which ends this session; auth_service's rules.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const jar = vi.hoisted(() => ({
  set: vi.fn(),
  delete: vi.fn(),
  get: (n: string) => (n === "helm_at" ? { value: "old-at" } : undefined),
}));
vi.mock("next/headers", () => ({ cookies: async () => jar, headers: async () => new Headers() }));
vi.mock("next/navigation", () => ({
  redirect: (to: string) => {
    throw new Error(`redirect ${to}`);
  },
}));

import { changePassword } from "@/app/(auth)/actions";

type Reply = [number, unknown?];
let replies: Record<string, Reply[]>;
const calls: { method: string; path: string; body?: unknown }[] = [];
beforeEach(() => {
  calls.length = 0;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, init: RequestInit) => {
      const path = new URL(url).pathname;
      const method = init.method ?? "GET";
      calls.push({ method, path, body: init.body && JSON.parse(String(init.body)) });
      const [status, body] = replies[`${method} ${path}`].shift()!;
      return new Response(body === undefined ? null : JSON.stringify(body), { status });
    }),
  );
});
afterEach(() => {
  vi.unstubAllGlobals();
  vi.clearAllMocks();
});

const me: Reply = [200, { email: "a@x.test" }];
const form = (current: string, next: string, confirm = next) => {
  const fd = new FormData();
  fd.set("current", current);
  fd.set("new", next);
  fd.set("confirm", confirm);
  return fd;
};

describe("changePassword", () => {
  it("changes it and signs this browser in again with the new password", async () => {
    replies = {
      "GET /auth/me": [me],
      "PUT /auth/password": [[204]],
      "POST /auth/signin": [[200, { access_token: "at2", refresh_token: "rt2", user: {} }]],
    };
    expect(await changePassword(undefined, form("old-password", "a-new-password"))).toEqual({
      done: true,
    });
    expect(calls[1].body).toEqual({
      current_password: "old-password",
      new_password: "a-new-password",
    });
    expect(calls[2].body).toEqual({ email: "a@x.test", password: "a-new-password" });
    expect(jar.set.mock.calls.map((c) => [c[0], c[1]])).toEqual([
      ["helm_at", "at2"],
      ["helm_rt", "rt2"],
    ]);
  });
  it("answers a wrong current password, and signs out after the one that ended the session", async () => {
    const wrong: Reply = [401, { detail: "current password is incorrect" }];
    replies = { "GET /auth/me": [me, me], "PUT /auth/password": [wrong] };
    expect(await changePassword(undefined, form("bad-password", "a-new-password"))).toEqual({
      wrong: true,
    });
    replies = { "GET /auth/me": [me, [401]], "PUT /auth/password": [wrong] };
    await expect(changePassword(undefined, form("bad-password", "a-new-password"))).rejects.toThrow(
      "redirect /signin/expired?next=%2Fsettings",
    );
  });
  it("maps auth_service's rules and checks its own first", async () => {
    replies = {
      "GET /auth/me": [me],
      "PUT /auth/password": [[422, { detail: "password is too common" }]],
    };
    expect(await changePassword(undefined, form("old-password", "1234567890"))).toEqual({
      rule: "common",
    });
    expect(await changePassword(undefined, form("old-password", "short"))).toEqual({
      error: "Password not changed.",
    });
    expect(await changePassword(undefined, form("old-password", "a-new-password", "x"))).toEqual({
      error: "Password not changed.",
    });
  });
});
