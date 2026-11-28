import { afterEach, describe, expect, it, vi } from "vitest";
import { NextRequest } from "next/server";
import { jwtExp, needsRefresh, safeNext } from "@/lib/jwt";
import { allowed, proxy as bff } from "@/lib/proxy";
import {
  ACCESS_COOKIE,
  PATH_HEADER,
  REFRESH_COOKIE,
  clientIp,
  kongHeaders,
  publicOrigin,
  refreshResult,
} from "@/lib/session";
import { errorText, okOr } from "@/lib/api";
import { safeUrl } from "@/lib/format";
import { config, proxy } from "@/proxy";
import { GET as expiredGET } from "@/app/(auth)/signin/expired/route";

const b64 = (o: object) =>
  btoa(JSON.stringify(o)).replace(/=+$/, "").replace(/\+/g, "-").replace(/\//g, "_");
const jwt = (exp: number) => `${b64({ alg: "RS256" })}.${b64({ sub: "u", exp })}.sig`;

describe("tokens", () => {
  it("reads exp from a JWT and nothing from junk", () => {
    expect(jwtExp(jwt(123))).toBe(123);
    expect(jwtExp("string")).toBeNull();
    expect(jwtExp("a.%%%.c")).toBeNull();
    expect(jwtExp(undefined)).toBeNull();
  });
  it("refreshes missing or expiring tokens only", () => {
    expect(needsRefresh(undefined)).toBe(true);
    expect(needsRefresh(jwt(1000), 900)).toBe(false);
    expect(needsRefresh(jwt(1000), 980)).toBe(true); // within the 30 s skew
    expect(needsRefresh("opaque", 1e12)).toBe(false);
  });
  it("only redirects to same-site paths", () => {
    expect(safeNext("/admin?x=1")).toBe("/admin?x=1");
    expect(safeNext("/s/1#run")).toBe("/s/1#run");
    for (const bad of [
      "//evil.com",
      "https://evil.com",
      "/\\evil.com",
      "/\t/evil.com",
      "/\n/evil.com",
      "/\r\n/evil.com",
      "/x\\..\\/evil.com",
      "",
      null,
      undefined,
    ])
      expect(safeNext(bad)).toBe("/");
  });
  it("names the cookies helm_at and helm_rt", () => {
    expect([ACCESS_COOKIE, REFRESH_COOKIE]).toEqual(["helm_at", "helm_rt"]);
  });
});

describe("BFF allow-list", () => {
  it("passes the API and the user's own auth routes", () => {
    for (const p of [
      "/v1/studio/sessions",
      "/v1/studio/sessions/1/events",
      "/auth/me",
      "/auth/password",
      "/auth/admin/users",
      "/auth/admin/users/00000000-0000-4000-8000-000000000000/roles",
      "/auth/admin/users/00000000-0000-4000-8000-000000000000/disabled",
      "/auth/admin/users/00000000-0000-4000-8000-000000000000/password",
    ])
      expect(allowed(p)).toBe(true);
  });
  it("blocks session routes, API keys, other paths and dot segments", () => {
    for (const p of [
      "/auth/signin",
      "/auth/refresh",
      "/auth/signout",
      "/auth/admin/users/x/roles",
      "/files/x",
      "/v1/../auth/signin",
      "/v1/x",
      "/v1/other/sessions",
      "/v1/studio/x%2F..%2F..%2Fauth%2Fme",
      "/v1/studio/%2E%2E/x",
      "/v1/studio/a%25b",
      "/v1/./x",
      "/internal",
    ])
      expect(allowed(p)).toBe(false);
  });
});

describe("BFF API keys", () => {
  it("lists and revokes keys, never makes one", () => {
    const id = "/auth/admin/apikeys/00000000-0000-4000-8000-000000000000";
    expect(allowed("/auth/admin/apikeys", "GET")).toBe(true);
    expect(allowed(id, "DELETE")).toBe(true);
    expect(allowed("/auth/admin/apikeys", "POST")).toBe(false);
    expect(allowed(id, "GET")).toBe(false);
    expect(allowed("/auth/admin/apikeys/x", "DELETE")).toBe(false);
  });
});

describe("BFF responses", () => {
  afterEach(() => vi.unstubAllGlobals());
  const upstream = (headers: Record<string, string>) =>
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => new Response("{}", { headers })),
    );
  const call = (withAuth: boolean) =>
    bff(new NextRequest("http://web/api/v1/studio/sessions"), "/v1/studio/sessions", withAuth);

  it("drops Kong's own headers and keeps per-user answers out of caches", async () => {
    upstream({
      server: "kong/3.9.4",
      via: "1.1 kong/3.9.4",
      "x-kong-proxy-latency": "1",
      "x-correlation-id": "c",
      "content-type": "application/json",
    });
    const res = await call(true);
    for (const h of ["server", "via", "x-kong-proxy-latency"])
      expect(res.headers.has(h)).toBe(false);
    expect(res.headers.get("x-correlation-id")).toBe("c");
    expect(res.headers.get("cache-control")).toBe("private, no-store");
  });
  it("keeps the upstream's own cache-control, and adds none to signed links", async () => {
    upstream({ "cache-control": "private, max-age=60" });
    expect((await call(true)).headers.get("cache-control")).toBe("private, max-age=60");
    upstream({});
    expect((await call(false)).headers.get("cache-control")).toBeNull();
  });
  it("passes X-Helm-Client (a timeline revision's label) to Kong, not a browser's other headers", async () => {
    upstream({});
    await bff(
      new NextRequest("http://web/api/v1/studio/timelines/t/edits", {
        method: "POST",
        headers: { "x-helm-client": "web", "x-client-id": "spoof" },
      }),
      "/v1/studio/timelines/t/edits",
      true,
    );
    const sent = (vi.mocked(fetch).mock.calls[0][1] as RequestInit).headers as Headers;
    expect(sent.get("x-helm-client")).toBe("web");
    expect(sent.has("x-client-id")).toBe(false);
  });
  it("labels a request without X-Helm-Client web (undo, redo, add: the web's writes)", async () => {
    upstream({});
    await bff(
      new NextRequest("http://web/api/v1/studio/timelines/t/undo", { method: "POST" }),
      "/v1/studio/timelines/t/undo",
      true,
    );
    const sent = (vi.mocked(fetch).mock.calls[0][1] as RequestInit).headers as Headers;
    expect(sent.get("x-helm-client")).toBe("web");
  });
});

describe("kongHeaders", () => {
  it("forwards the browser's IP from Kong's X-Real-IP, never a browser-sent X-Forwarded-For", () => {
    const fromKong = new Headers({
      "x-real-ip": "198.51.100.7",
      "x-forwarded-for": "6.6.6.6, 198.51.100.7",
    });
    const h = kongHeaders("tok", undefined, clientIp(fromKong));
    expect(h.get("x-forwarded-for")).toBe("198.51.100.7");
    expect(h.get("authorization")).toBe("Bearer tok");
    expect(kongHeaders(undefined, undefined, clientIp(new Headers())).has("x-forwarded-for")).toBe(
      false,
    );
  });
});

describe("api helpers", () => {
  const answer = (status: number) =>
    Promise.resolve({
      data: status < 300 ? { a: 1 } : undefined,
      error: { detail: "ended" },
      response: new Response(null, { status }),
    });
  it("okOr is the data, null for the expected status, and an error otherwise", async () => {
    expect(await okOr(answer(200), 409)).toEqual({ a: 1 });
    expect(await okOr(answer(409), 409)).toBeNull();
    await expect(okOr(answer(404), 409)).rejects.toThrow("ended");
  });
  it("errorText reads every error shape", () => {
    expect(errorText(404, { detail: "run not found" })).toBe("run not found");
    expect(errorText(429, { message: "API rate limit exceeded" })).toBe("API rate limit exceeded");
    expect(errorText(500, null)).toMatch(/500/);
    // an outage is the banner's words (docs/mock/ErrorStates.dc.html a2r); studio's own 503 detail stays
    expect(errorText(502, null)).toBe("The studio isn’t answering.");
    expect(errorText(502, { detail: "the API is unreachable" })).toBe("The studio isn’t answering.");
    expect(errorText(503, { message: "failure to get a peer from the ring-balancer" })).toBe(
      "The studio isn’t answering.",
    );
    expect(errorText(503, { detail: "the registry is loading" })).toBe("the registry is loading");
  });
  it("safeUrl never lets a javascript: link through", () => {
    expect(safeUrl("/files/a.png?sig=1")).toBe("/files/a.png?sig=1");
    expect(safeUrl("https://s3/x")).toBe("https://s3/x");
    expect(safeUrl("http://localhost:8300/files/u/a.png?exp=1&sig=2")).toBe(
      "/files/u/a.png?exp=1&sig=2",
    );
    expect(safeUrl("javascript:alert(1)")).toBeUndefined();
    expect(safeUrl("//evil.com/x")).toBeUndefined();
    expect(safeUrl("http://")).toBeUndefined(); // malformed: no throw during render
  });
});

describe("publicOrigin", () => {
  const req = (h: Record<string, string>) =>
    ({ headers: new Headers(h), nextUrl: new URL("http://localhost:3000/admin") }) as never;
  it("uses Kong's forwarded host and port", () => {
    expect(
      publicOrigin(
        req({
          host: "web:3000",
          "x-forwarded-host": "127.0.0.1",
          "x-forwarded-port": "8300",
          "x-forwarded-proto": "http",
        }),
      ),
    ).toBe("http://127.0.0.1:8300");
    expect(
      publicOrigin(
        req({
          host: "web:3000",
          "x-forwarded-host": "studio.example.com",
          "x-forwarded-port": "443",
          "x-forwarded-proto": "https",
        }),
      ),
    ).toBe("https://studio.example.com");
    expect(publicOrigin(req({ host: "127.0.0.1:3100" }))).toBe("http://127.0.0.1:3100");
  });
});

describe("proxy on an expiring session", () => {
  /** Runs one request carrying a refresh cookie, with auth_service answering status. */
  async function run(token: string, status: number) {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => new Response("{}", { status })),
    );
    return proxy(
      new NextRequest("http://web/admin", { headers: { cookie: `${REFRESH_COOKIE}=${token}` } }),
    );
  }

  afterEach(() => vi.unstubAllGlobals());

  it("treats only a 401 as signed out", () => {
    expect(refreshResult(401, null)).toBe("refused");
    for (const status of [403, 429, 500, 502, 503])
      expect(refreshResult(status, null)).toBe("unavailable");
    const session = { access_token: "a", refresh_token: "r" };
    expect(refreshResult(200, session)).toBe(session);
  });

  it("signs out when the refresh is refused", async () => {
    const res = await run("refused-token", 401);
    expect(res.headers.get("location")).toContain("/signin");
    expect(res.cookies.get(REFRESH_COOKIE)?.value).toBe("");
    expect(res.cookies.get(ACCESS_COOKIE)?.value).toBe("");
  });

  it("answers an API call with a 401, not a redirect", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => new Response("{}", { status: 401 })),
    );
    const res = await proxy(
      new NextRequest("http://web/api/v1/studio/sessions", {
        headers: { cookie: `${REFRESH_COOKIE}=api-token` },
      }),
    );
    expect(res.status).toBe(401);
  });

  it.each([500, 503, 429])("keeps the cookies when auth_service answers %i", async (status) => {
    const res = await run(`down-token-${status}`, status);
    expect(res.headers.get("location")).toBeNull();
    expect(res.cookies.get(REFRESH_COOKIE)).toBeUndefined();
  });

  it("keeps the cookies when auth_service can't be reached", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => {
        throw new Error("connection refused");
      }),
    );
    const res = await proxy(
      new NextRequest("http://web/admin", {
        headers: { cookie: `${REFRESH_COOKIE}=unreachable-token` },
      }),
    );
    expect(res.cookies.get(REFRESH_COOKIE)).toBeUndefined();
  });

  it("asks again after an outage instead of reusing the failure", async () => {
    await run("retry-token", 503);
    const res = await run("retry-token", 401);
    expect(res.headers.get("location")).toContain("/signin");
  });

  it("sets both cookies from a refreshed session", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(
        async () =>
          new Response(JSON.stringify({ access_token: "a2", refresh_token: "r2", user: {} }), {
            status: 200,
          }),
      ),
    );
    const res = await proxy(
      new NextRequest("http://web/", { headers: { cookie: `${REFRESH_COOKIE}=good-token` } }),
    );
    expect(res.cookies.get(ACCESS_COOKIE)?.value).toBe("a2");
    expect(res.cookies.get(REFRESH_COOKIE)?.value).toBe("r2");
  });

  it("doesn't let an old failure's timer drop a later refresh", async () => {
    vi.useFakeTimers();
    try {
      await run("single-use-token", 503);
      await vi.advanceTimersByTimeAsync(29_900);
      const fetchSession = vi.fn(
        async () =>
          new Response(JSON.stringify({ access_token: "a", refresh_token: "r", user: {} }), {
            status: 200,
          }),
      );
      vi.stubGlobal("fetch", fetchSession);
      const request = () =>
        proxy(
          new NextRequest("http://web/admin", {
            headers: { cookie: `${REFRESH_COOKIE}=single-use-token` },
          }),
        );
      await request();
      await vi.advanceTimersByTimeAsync(200); // the failed attempt's 30 s timer fires here
      await request();
      expect(fetchSession).toHaveBeenCalledTimes(1); // a second call would resend a spent token
    } finally {
      vi.useRealTimers();
    }
  });
});

describe("a signed-out visitor", () => {
  it("sees the landing page at /, and goes to /signin from anywhere else", async () => {
    const home = await proxy(new NextRequest("http://web/"));
    expect(home.headers.get("x-middleware-rewrite")).toBe("http://web/welcome");
    expect(home.headers.get("location")).toBeNull();
    const admin = await proxy(new NextRequest("http://web/admin"));
    expect(admin.headers.get("location")).toBe("http://web/signin?next=%2Fadmin");
    const welcome = await proxy(new NextRequest("http://web/welcome"));
    expect(welcome.headers.get("x-middleware-next")).toBe("1");
  });

  it("gets the tab and app icons and the showcase pictures without a session (the proxy never sees them)", () => {
    const matched = new RegExp(`^${config.matcher[0]}$`);
    for (const p of [
      "/favicon.ico",
      "/icon.svg",
      "/apple-icon.png",
      "/icon-32.png",
      "/icon-512.png",
      "/showcase/mara-street.jpg",
    ])
      expect(matched.test(p), p).toBe(false);
    for (const p of ["/", "/admin", "/welcome", "/signin"]) expect(matched.test(p), p).toBe(true);
  });
});

describe("a signed-in user at /welcome", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("goes to Create, also after a refresh; a refused or unreachable session sees the landing page", async () => {
    const live = `${ACCESS_COOKIE}=${jwt(Math.floor(Date.now() / 1000) + 3600)}`;
    const res = await proxy(new NextRequest("http://web/welcome", { headers: { cookie: live } }));
    expect(res.headers.get("location")).toBe("http://web/");
    const answer = (status: number, body = "{}") =>
      vi.stubGlobal(
        "fetch",
        vi.fn(async () => new Response(body, { status })),
      );
    const expiring = (token: string) =>
      proxy(
        new NextRequest("http://web/welcome", {
          headers: { cookie: `${REFRESH_COOKIE}=${token}` },
        }),
      );
    answer(200, JSON.stringify({ access_token: "a3", refresh_token: "r3", user: {} }));
    const refreshed = await expiring("welcome-good");
    expect(refreshed.headers.get("location")).toBe("http://web/");
    expect(refreshed.cookies.get(ACCESS_COOKIE)?.value).toBe("a3");
    for (const [token, status] of [
      ["welcome-refused", 401],
      ["welcome-down", 503],
    ] as const) {
      answer(status);
      const landing = await expiring(token);
      expect(landing.headers.get("location")).toBeNull();
      expect(landing.headers.get("x-middleware-next")).toBe("1");
    }
  });
});

describe("a session the API refused", () => {
  it("clears both cookies and keeps a safe next", () => {
    const res = expiredGET(new NextRequest("http://web/signin/expired?next=%2Fadmin%3Fx%3D1"));
    expect(res.headers.get("location")).toBe("http://web/signin?next=%2Fadmin%3Fx%3D1");
    expect(res.cookies.get(ACCESS_COOKIE)?.value).toBe("");
    expect(res.cookies.get(REFRESH_COOKIE)?.value).toBe("");
    const evil = expiredGET(new NextRequest("http://web/signin/expired?next=%2F%2Fevil.com"));
    expect(evil.headers.get("location")).toBe("http://web/signin?next=%2F");
  });

  it("tells the layout the page's path, whatever the browser sent", async () => {
    const res = await proxy(
      new NextRequest("http://web/signin?next=%2F", { headers: { [PATH_HEADER]: "//evil.com" } }),
    );
    expect(res.headers.get(`x-middleware-request-${PATH_HEADER}`)).toBe("/signin?next=%2F");
  });
});
