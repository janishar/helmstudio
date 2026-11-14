import { afterEach, describe, expect, it, vi } from "vitest";
import { jwtExp, needsRefresh, safeNext } from "@/lib/jwt";
import { allowed } from "@/lib/proxy";
import { clientIp, kongHeaders } from "@/lib/session";
import { errorText, okOr } from "@/lib/api";
import { safeUrl } from "@/lib/format";
import { strength } from "@/app/(auth)/auth-form";
import {
  LOOKS,
  SHOT_COUNT,
  keepCustom,
  shotTags,
  withCustomPoses,
  choiceLabel,
  descriptionPatch,
  kindTip,
  liveTry,
  pickableTry,
  pickedByBrand,
  lookImage,
  subcategoryTip,
  suitsWearers,
  wearersOf,
  lookName,
  activeRuns,
  applyEvent,
  stoppedLine,
  mergeDetail,
  crRange,
  crWorst,
  jobNodes,
  sampleSku,
  cursorOf,
  quoteSkus,
  needsModel,
  poseNoun,
  revisionsOf,
  withNoun,
  type Image,
  nextFlow,
  processingReady,
  released,
  sceneNote,
  setupRows,
  skuKind,
  skuSummary,
  upsert,
  withCursor,
  type Inputs,
  type Detail,
  type Library,
  type Sku,
  type Msg,
  type SessionEvent,
  withOpenQuestions,
} from "@/lib/catalog";

const b64 = (o: object) =>
  btoa(JSON.stringify(o)).replace(/=+$/, "").replace(/\+/g, "-").replace(/\//g, "_");
const jwt = (exp: number) => `${b64({ alg: "RS256" })}.${b64({ sub: "u", exp })}.sig`;

describe("credit quotes", () => {
  it("shows the worst case when every review fails", () => {
    expect(crRange({ credits: 300, worst: 1200 })).toBe("~300–1,200 cr");
    expect(crWorst({ credits: 300, worst: 1200 })).toBe("~300 cr (up to 1,200 if every review fails)");
    expect(crWorst({ credits: 75, worst: 300 }, "75 cr per image")).toBe("75 cr per image (up to 300 if every review fails)");
    expect(crRange({ credits: 300, worst: 300 })).toBe("~300 cr");
    expect(crWorst({ credits: 0, worst: 0 })).toBe("~0 cr");
  });
  it("uses each product's own max_attempts and default resolution", () => {
    const lib = {
      max_attempts: 4,
      defaults: { resolution: "2K" },
      resolutions: [{ id: "2K", credits_per_image: 100 }, { id: "4K", credits_per_image: 200 }],
      categories: [{ id: "sofas", max_attempts: 3, default_resolution: "4K" }, { id: "rings" }],
    } as unknown as Library;
    const sku = (category: string, poses: number) => ({ category, brief: { resolution: null, poses: Array(poses).fill("p") } }) as unknown as Sku;
    // sofa: 2 × 200 × 3 = 1,200; ring: 1 × 100 × 4 (the library's) = 400
    expect(quoteSkus([sku("sofas", 2), sku("rings", 1)], lib)).toEqual({ credits: 500, worst: 1600 });
    expect(crRange(quoteSkus([sku("sofas", 1)], lib))).toBe("~200–600 cr");
  });
});

describe("products without a model", () => {
  const lib = {
    families: [{ id: "jewellery", name: "Jewellery" }, { id: "furniture", name: "Furniture" }],
    categories: [
      { id: "rings", family: "jewellery", uses_model: true },
      { id: "sofas", family: "furniture", uses_model: false },
      { id: "coins", name: "Coins", family: "jewellery", uses_model: false },
    ],
  } as Library;
  const sofa = { category: "sofas" } as Sku;
  const ring = { category: "rings" } as Sku;
  const inputs = Object.fromEntries(
    ["guidelines", "output", "poses", "models", "backgrounds", "details"].map((k) => [k, { items: [], skipped: true }]),
  ) as unknown as Inputs;
  const modelsRow = (skus: Sku[]) => setupRows(inputs, lib, skus).find(([k]) => k === "Models")?.[1];
  it("skips the Models step and says why only when no SKU uses a model", () => {
    expect(needsModel(lib, [sofa])).toBe(false);
    expect(needsModel(lib, [sofa, ring])).toBe(true);
    expect(needsModel(lib, [])).toBe(true);
    expect(sceneNote(lib, [ring])).toBeNull();
    expect(modelsRow([sofa])).toBe("No model — furniture is shot as a scene");
    expect(modelsRow([sofa, ring])).toBe("Suggested per category · No model — furniture is shot as a scene");
    expect(modelsRow([ring])).toBe("Suggested per category");
    // a product-only group of a family with models is not "shot as a scene"
    expect(sceneNote(lib, [{ category: "coins" } as Sku, sofa])).toBe(
      "No model — coins are shot on their own, furniture is shot as a scene",
    );
  });
});

describe("pose nouns", () => {
  const lib = {
    categories: [
      { id: "ring", pose_noun: { singular: "pose", plural: "poses" } },
      { id: "bangle", pose_noun: { singular: "pose", plural: "poses" } },
      { id: "sofa", pose_noun: { singular: "angle", plural: "angles" } },
    ],
  } as unknown as Library;
  it("uses the product's word, a shared word, else the generic one", () => {
    expect(poseNoun(lib, "sofa")).toEqual({ singular: "angle", plural: "angles" });
    expect(poseNoun(lib, ["ring", "bangle"]).plural).toBe("poses");
    expect(poseNoun(lib, ["ring", "sofa"]).plural).toBe("shots");
    expect(poseNoun(lib, "unknown").singular).toBe("shot");
    expect(poseNoun(null, []).singular).toBe("shot");
  });
  it("rewrites pose words and keeps their capital", () => {
    const angle = poseNoun(lib, "sofa");
    expect(withNoun("Pose selection", angle)).toBe("Angle selection");
    expect(withNoun("Skipped — ORA default poses.", angle)).toBe("Skipped — ORA default angles.");
    expect(withNoun("Use 2 poses · purpose", angle)).toBe("Use 2 angles · purpose");
  });
});

describe("subcategories", () => {
  const lib = {
    categories: [{ id: "tops", name: "Tops", subcategories: [{ id: "kurtis", name: "Kurtis" }, { id: "shirts", name: "Shirts" }] }],
  } as unknown as Library;
  const sku = (code: string, subcategory: string | null) => ({ code, name: code, category: "tops", subcategory }) as unknown as Sku;
  it("names a SKU's category and subcategory, and counts subcategories most first", () => {
    expect(skuKind(lib, sku("TP-1", "kurtis"))).toBe("Tops › Kurtis");
    expect(skuKind(lib, sku("TP-2", null))).toBe("Tops");
    const [c] = skuSummary([sku("TP-1", "shirts"), sku("TP-2", "kurtis"), sku("TP-3", "kurtis"), sku("TP-4", null)], lib);
    expect(c.subs).toEqual([{ name: "Kurtis", count: 2 }, { name: "Shirts", count: 1 }]);
  });
});

describe("revisionsOf", () => {
  const im = (id: string, sku_id: string, shot: string, revision: number) => ({ id, sku_id, shot, revision }) as Image;
  it("gives one SKU × shot's revisions oldest first", () => {
    const images = [im("c", "s1", "front", 3), im("x", "s1", "side", 1), im("a", "s1", "front", 1), im("y", "s2", "front", 2), im("b", "s1", "front", 2)];
    expect(revisionsOf(images, "s1", "front").map((x) => x.id)).toEqual(["a", "b", "c"]);
    expect(revisionsOf(images, "s3", "front")).toEqual([]);
  });
});

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
    expect(safeNext("/jobs?x=1")).toBe("/jobs?x=1");
    for (const bad of ["//evil.com", "https://evil.com", "/\\evil.com", "", null, undefined])
      expect(safeNext(bad)).toBe("/");
  });
});

describe("BFF allow-list", () => {
  it("passes the API and the user's own auth routes", () => {
    for (const p of [
      "/v1/catalog/sessions",
      "/v1/catalog/sessions/1/events",
      "/auth/me",
      "/auth/password",
      "/auth/admin/users",
      "/auth/admin/users/00000000-0000-4000-8000-000000000000/roles",
    ])
      expect(allowed(p)).toBe(true);
  });
  it("blocks session routes, other paths and dot segments", () => {
    for (const p of [
      "/auth/signin",
      "/auth/refresh",
      "/auth/signout",
      "/auth/signup",
      "/auth/admin/apikeys",
      "/auth/admin/users/x/roles",
      "/files/x",
      "/v1/../auth/signin",
      "/v1/./x",
      "/internal",
    ])
      expect(allowed(p)).toBe(false);
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

describe("okOr", () => {
  const answer = (status: number) =>
    Promise.resolve({ data: status < 300 ? { a: 1 } : undefined, error: { detail: "ended" }, response: new Response(null, { status }) });
  it("is the data, null for the expected status, and an error otherwise", async () => {
    expect(await okOr(answer(200), 409)).toEqual({ a: 1 });
    expect(await okOr(answer(409), 409)).toBeNull();
    await expect(okOr(answer(404), 409)).rejects.toThrow("ended");
  });
});

describe("errorText", () => {
  it("reads every error shape", () => {
    expect(errorText(404, { detail: "job not found" })).toBe("job not found");
    expect(errorText(429, { message: "API rate limit exceeded" })).toBe("API rate limit exceeded");
    expect(errorText(502, null)).toMatch(/502/);
  });
});

describe("format", () => {
  it("never lets a javascript: link through", () => {
    expect(safeUrl("/files/a.png?sig=1")).toBe("/files/a.png?sig=1");
    expect(safeUrl("https://s3/x")).toBe("https://s3/x");
    expect(safeUrl("http://localhost:8100/files/u/a.png?exp=1&sig=2")).toBe(
      "/files/u/a.png?exp=1&sig=2",
    ); // same origin
    expect(safeUrl("javascript:alert(1)")).toBeUndefined();
    expect(safeUrl("//evil.com/x")).toBeUndefined();
  });
});

describe("password strength", () => {
  it("ranks", () => {
    expect(strength("")).toBe(0);
    expect(strength("short")).toBe(1);
    expect(strength("Abcdefgh12!xyz")).toBe(4);
  });
});

describe("publicOrigin", async () => {
  const { publicOrigin } = await import("@/proxy");
  const req = (h: Record<string, string>) =>
    ({ headers: new Headers(h), nextUrl: new URL("http://localhost:3000/jobs") }) as never;
  it("uses Kong's forwarded host and port", () => {
    expect(
      publicOrigin(
        req({
          host: "web:3000",
          "x-forwarded-host": "127.0.0.1",
          "x-forwarded-port": "8080",
          "x-forwarded-proto": "http",
        }),
      ),
    ).toBe("http://127.0.0.1:8080");
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

describe("catalog chat", () => {
  const ev = (seq: number, data: object, type = "log") =>
    ({ seq, type, work_id: "w1", data }) as unknown as SessionEvent;
  it("turns a log into a status line that replaces the placeholder, and skips events at or below the cursor", () => {
    const start: Msg[] = [{ id: "bg:w1", kind: "status", text: "Processing images…", done: false }];
    const once = applyEvent(start, ev(3, { key: "sort", text: "Sorted 4 images", state: "done" }));
    expect(once.filter((m) => m.kind === "status").map((m) => [m.id, m.text, m.done])).toEqual([
      ["log:w1:sort", "Sorted 4 images", true],
    ]);
    expect(cursorOf(once)).toBe(3);
    expect(applyEvent(once, ev(3, { key: "again", text: "x" }))).toBe(once);
    expect(cursorOf(withCursor(once, 9))).toBe(9);
  });
  it("keeps a log line's detail for View details, and none when it has none", () => {
    const detail = "brand.pdf: read\nhttps://shop.example/sets.html: the page has no readable text";
    const [line] = applyEvent([], ev(4, { key: "guidelines", text: "Couldn't read the brand guidelines.", state: "error", detail }));
    expect([line.kind, line.error, line.detail]).toEqual(["status", true, detail]);
    const [plain] = applyEvent([], ev(5, { key: "sort", text: "Sorted", state: "done" }));
    expect(plain.detail).toBeUndefined();
  });
  it("holds sorting results and questions during Brand guidelines, and says a repeated check once", () => {
    let ms = applyEvent([], ev(1, { key: "sort", text: "Images sorted", state: "done" }), false, true);
    ms = applyEvent(ms, ev(2, { id: "q1" }, "question"), false, true);
    expect(ms.filter((m) => m.held).map((m) => m.id)).toEqual(["log:w1:sort", "q:q1"]);
    expect(released(ms).map((m) => m.held)).toEqual([false, false]);
    const read = { key: "guidelines", text: "Brand guidelines read.", state: "done" };
    const twice = applyEvent(applyEvent([], ev(3, read)), { ...ev(4, read), work_id: "w2" } as SessionEvent);
    expect(twice.filter((m) => m.text === read.text)).toHaveLength(1);
  });
  it("says a stopped run plainly, and drops its removed images from the detail", () => {
    const stopped = applyEvent([], { ...ev(1, { work: "run", status: "failed", error: "Stopped" }, "status") });
    expect(stopped.find((m) => m.kind === "update")?.text).toBe(
      "Stopped. Finished images stay. Images already in progress may still incur charges.",
    );
    expect(stoppedLine("TP-001")).toBe("Stopped TP-001. Finished images stay. Images already in progress may still incur charges.");
    const failed = applyEvent([], { ...ev(1, { work: "run", status: "failed", error: "boom" }, "status") });
    expect(failed.find((m) => m.kind === "update")?.text).toBe("The run failed — boom");
    const runs = [{ id: "r1", status: "running" }, { id: "r2", status: "failed" }, { id: "r3", status: "queued" }];
    expect(activeRuns(runs as never).map((r) => r.id)).toEqual(["r1", "r3"]);
    const d = { session: {}, skus: [], questions: [], runs: [], images: [{ id: "a" }, { id: "b" }] };
    expect(mergeDetail(d as never, { removed: ["a"] }).images.map((im) => im.id)).toEqual(["b"]);
  });
  it("upserts by id and walks the flow without running past its end", () => {
    expect(
      upsert(
        [
          { id: "a", v: 1 },
          { id: "b", v: 1 },
        ],
        [
          { id: "b", v: 2 },
          { id: "c", v: 1 },
        ],
      ),
    ).toEqual([
      { id: "a", v: 1 },
      { id: "b", v: 2 },
      { id: "c", v: 1 },
    ]);
    expect(nextFlow("raw")).toBe("guidelines");
    expect(nextFlow("done")).toBe("done");
  });
  it("lets Input go on after a failed processing job once there are SKUs, never past an open blocking question", () => {
    const d = (status: string, skus: number, blocking = false) =>
      ({
        session: { processing: { status } },
        skus: Array.from({ length: skus }, () => ({})),
        questions: blocking ? [{ blocking: true, answer: null }] : [],
      }) as unknown as Parameters<typeof processingReady>[0];
    expect(processingReady(d("failed", 3))).toBe(true);
    expect(processingReady(d("failed", 0))).toBe(false);
    expect(processingReady(d("running", 3))).toBe(false);
    expect(processingReady(d("succeeded", 3, true))).toBe(false);
  });
});

describe("proxy on an expiring session", async () => {
  const { NextRequest } = await import("next/server");
  const { proxy, refreshResult } = await import("@/proxy");
  const { ACCESS_COOKIE, REFRESH_COOKIE } = await import("@/lib/session");

  /** Runs one request carrying a refresh cookie, with auth_service answering status. */
  async function run(token: string, status: number) {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => new Response("{}", { status })),
    );
    const req = new NextRequest("http://web/jobs", {
      headers: { cookie: `${REFRESH_COOKIE}=${token}` },
    });
    return proxy(req);
  }

  afterEach(() => vi.unstubAllGlobals());

  it("treats only a 401 as signed out", () => {
    expect(refreshResult(401, null)).toBe("refused");
    for (const status of [403, 429, 500, 502, 503]) {
      expect(refreshResult(status, null)).toBe("unavailable");
    }
    const session = { access_token: "a", refresh_token: "r" };
    expect(refreshResult(200, session)).toBe(session);
  });

  it("signs out when the refresh is refused", async () => {
    const res = await run("refused-token", 401);
    expect(res.headers.get("location")).toContain("/signin");
    expect(res.cookies.get(REFRESH_COOKIE)?.value).toBe("");
    expect(res.cookies.get(ACCESS_COOKIE)?.value).toBe("");
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
    const req = new NextRequest("http://web/jobs", {
      headers: { cookie: `${REFRESH_COOKIE}=unreachable-token` },
    });
    const res = await proxy(req);
    expect(res.cookies.get(REFRESH_COOKIE)).toBeUndefined();
  });

  it("asks again after an outage instead of reusing the failure", async () => {
    await run("retry-token", 503);
    const res = await run("retry-token", 401);
    expect(res.headers.get("location")).toContain("/signin");
  });

  it.each(["", "<html>bad gateway</html>", "{}", '{"detail":"x"}'])(
    "keeps the cookies when a 200 has the broken body %j",
    async (body) => {
      vi.stubGlobal(
        "fetch",
        vi.fn(async () => new Response(body, { status: 200 })),
      );
      const req = new NextRequest("http://web/jobs", {
        headers: { cookie: `${REFRESH_COOKIE}=broken-${body}` },
      });
      const res = await proxy(req);
      expect(res.headers.get("location")).toBeNull();
      expect(res.cookies.get(REFRESH_COOKIE)).toBeUndefined();
    },
  );

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
          new NextRequest("http://web/jobs", {
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

describe("open questions", () => {
  it("adds a card for an open question the chat doesn't show, and only once", () => {
    const qs = [{ id: "a", answer: null }, { id: "b", answer: 0 }];
    const ms = withOpenQuestions([{ id: "m1", kind: "agent", text: "hi" }], qs);
    expect(ms.map((m) => m.id)).toEqual(["m1", "q:a"]);
    expect(withOpenQuestions(ms, qs)).toBe(ms);
  });

  it("shows a held question again", () => {
    const ms = withOpenQuestions([{ id: "q:a", kind: "question", question_id: "a", held: true }], [{ id: "a", answer: null }]);
    expect(ms).toEqual([{ id: "q:a", kind: "question", question_id: "a" }]);
  });
});

describe("jobNodes", () => {
  const ev = (seq: number, type: string, data: object) =>
    ({ seq, type, work_id: "j", ts: `2026-01-01T00:00:0${seq}Z`, data }) as SessionEvent;
  it("folds steps, keeps each SKU's latest progress and each image's newest state", () => {
    const n = jobNodes([
      ev(1, "step", { step: "ingest", state: "started", detail: null }),
      ev(2, "step", { step: "ingest", state: "done", detail: "3 images" }),
      ev(3, "step", { step: "sort", state: "started", detail: null }),
      ev(4, "run", { sku_id: "k", sku_status: "Running", images_done: 1, images_total: 2, line: "1 of 2" }),
      ev(5, "image", { id: "i", status: "failed" }),
      ev(6, "image", { id: "i", status: "to_review" }),
      ev(7, "run", { sku_id: "k", sku_status: "To review", images_done: 2, images_total: 2, line: "2 of 2" }),
      ev(8, "step", { step: "sort", state: "failed", detail: "boom" }),
    ]);
    expect(n.steps).toEqual([
      { step: "ingest", state: "succeeded", started: "2026-01-01T00:00:01Z", ended: "2026-01-01T00:00:02Z", detail: "3 images" },
      { step: "sort", state: "failed", started: "2026-01-01T00:00:03Z", ended: "2026-01-01T00:00:08Z", detail: "boom" },
    ]);
    expect(n.skus.map((k) => k.line)).toEqual(["2 of 2"]);
    expect(n.images.map((i) => i.status)).toEqual(["to_review"]);
  });
});

describe("sampleSku", () => {
  it("is the product's first SKU not run yet, in list order, as the server picks it", () => {
    const sku = (code: string, category: string, status: string) => ({ code, category, status }) as unknown as Sku;
    const skus = [sku("R-001", "rings", "To review"), sku("R-002", "rings", "Not run"), sku("E-001", "earrings", "Not run")];
    expect(sampleSku(skus, "rings")?.code).toBe("R-002");
    expect(sampleSku(skus, "earrings")?.code).toBe("E-001");
    expect(sampleSku(skus, "bangles")).toBeUndefined();
  });
});

describe("described models and backgrounds", () => {
  it("labels a pick, a pick plus a description, and a description alone", () => {
    expect(choiceLabel("Aisha", null)).toBe("Aisha");
    expect(choiceLabel("Aisha", "wavy hair")).toBe("Aisha + described");
    expect(choiceLabel(null, "sunlit terrace")).toBe("Described");
    expect(choiceLabel(null)).toBe("—");
  });
  it("has 13 gender-neutral looks with women's sketches until the wear is men's", () => {
    expect(new Set(LOOKS.map((l) => l.id)).size).toBe(13);
    expect(LOOKS.some((l) => /\b(wo)?m[ae]n\b/i.test(l.text))).toBe(false);
    expect(lookName(LOOKS[0].text)).toBe("North Indian");
    const sku = (wearer?: string) => ({ attributes: { wearer } }) as unknown as Sku;
    expect(lookImage("african", [])).toBe("/catalog/looks/african.webp");
    expect(lookImage("african", [sku("men")])).toBe("/catalog/looks/men/african.webp");
    expect(lookImage("african", [sku("men"), sku()])).toBe("/catalog/looks/african.webp");
  });
  it("shows the models of the session's wear: men's for men's wear, women's for the rest, uploads always", () => {
    const sku = (wearer?: string | null, category = "tops") => ({ category, attributes: { wearer } }) as unknown as Sku;
    const lib = { categories: [{ id: "tops", uses_model: true }, { id: "intimates", uses_model: false }] } as unknown as Library;
    const arjun = { id: "m017", name: "Arjun", image: null, wearer: "men" };
    const meera = { id: "m002", name: "Meera", image: null, wearer: "women" };
    const upload = { id: "u1", name: "Our model", image: null };
    const shown = (skus: Sku[]) => [arjun, meera, upload].filter((m) => suitsWearers(m, wearersOf(skus, lib))).map((m) => m.id);
    expect(shown([sku("men")])).toEqual(["m017", "u1"]);
    expect(shown([sku(null), sku("women")])).toEqual(["m002", "u1"]);
    expect(shown([sku("men"), sku()])).toEqual(["m017", "m002", "u1"]);
    expect(shown([])).toEqual(["m017", "m002", "u1"]); // no SKUs yet
    expect(shown([sku("men"), sku(null, "intimates")])).toEqual(["m017", "u1"]); // shot without a model: no say
  });
  it("sends a description only when it changed, or with Apply to all when it says something", () => {
    expect(descriptionPatch("", "step text", false)).toBeNull(); // cleared
    expect(descriptionPatch("step text", "step text", false)).toBeUndefined();
    expect(descriptionPatch("", undefined, true)).toBeUndefined(); // an empty box never wipes a category
    expect(descriptionPatch("dusk", "dusk", true)).toBe("dusk");
  });
});

describe("custom shots", () => {
  const swing = { id: "custom-ring-on-a-swing", category: "rings", name: "On a swing", prompt: "p", on_model: true };
  const lib = { poses: [{ id: "ring-packshot", category: "rings", name: "Packshot", example_url: null }] } as unknown as Library;
  it("joins the session's custom shots to the library's poses", () => {
    const d = { session: { inputs: { poses: { items: [{ custom_poses: [swing] }] } } } } as unknown as Detail;
    expect(withCustomPoses(lib, d)?.poses.map((p) => p.id)).toEqual(["ring-packshot", swing.id]);
  });
  it("tags each planned shot with where it came from", () => {
    expect(shotTags(lib, ["ring-packshot", swing.id], [swing], { up1: swing.id })).toBe(
      "Packshot (Library), On a swing (Custom + your reference image)",
    );
  });
  it("keeps a picked custom shot's newest definition and drops one with none", () => {
    const newer = { ...swing, name: "Swing" };
    const out = keepCustom({ rings: ["ring-packshot", swing.id, "custom-ring-gone"] }, [swing, newer]);
    expect(out).toEqual({ poses: { rings: ["ring-packshot", swing.id] }, custom: [newer] });
  });
  it("tells a described shot list with a number from one without", () => {
    expect(SHOT_COUNT.test("4 shots: front, back")).toBe(true);
    expect(SHOT_COUNT.test("a front shot and a back shot")).toBe(false);
  });
});


describe("subcategory info tips", () => {
  const lib = {
    categories: [
      {
        id: "tops",
        name: "Tops",
        subcategories: [
          { id: "kurtis", name: "Kurtis", about: "A shorter, casual tunic top, hip to knee length", styling: "plain neutral leggings" },
          { id: "kurtas", name: "Kurtas", about: "A longer traditional tunic, knee length or below", styling: null },
          { id: "tops", name: "Tops" },
        ],
      },
    ],
  } as unknown as Library;
  it("says what a subcategory is and how it is styled", () => {
    expect(subcategoryTip(lib, "tops", "kurtis")).toBe("Kurtis: A shorter, casual tunic top, hip to knee length. Styled with: plain neutral leggings.");
    expect(subcategoryTip(lib, "tops", "kurtas")).toBe("Kurtas: A longer traditional tunic, knee length or below.");
    expect(subcategoryTip(lib, "tops", "tops")).toBeUndefined(); // no about
    expect(subcategoryTip(lib, "tops", null)).toBeUndefined();
    const sofas = { categories: [{ id: "sofas", family: "furniture", subcategories: [{ id: "sofa-beds", name: "Sofa Beds", about: "A sofa that pulls out into a bed", styling: "in a living room" }] }] } as unknown as Library;
    expect(subcategoryTip(sofas, "sofas", "sofa-beds")).toBe("Sofa Beds: A sofa that pulls out into a bed. Shown: in a living room.");
    const toes = { categories: [{ id: "anklets", family: "jewellery", subcategories: [{ id: "toe-rings", name: "Toe rings", about: "A ring worn on the toe", styling: "on the second toe" }] }] } as unknown as Library;
    expect(subcategoryTip(toes, "anklets", "toe-rings")).toBe("Toe rings: A ring worn on the toe. Worn: on the second toe.");
  });
  it("finds a kind question option's subcategory by its names", () => {
    expect(kindTip(lib, "Tops › Kurtas")).toBe("Kurtas: A longer traditional tunic, knee length or below.");
    expect(kindTip(lib, "Tops")).toBeUndefined();
    expect(kindTip(lib, "Keep as Tops › Kurtis")).toBeUndefined(); // not a bare kind label
    expect(kindTip(lib, "Follow the brand guidelines")).toBeUndefined();
  });
});

describe("picking another try", () => {
  const im = (status: string, picked = false) =>
    ({
      status,
      attempts: [
        { index: 1, url: "u1", review: null, best: false },
        { index: 2, url: "u2", review: null, best: true, picked },
      ],
    }) as unknown as Image;
  it("offers a shown try that is not the image, never while generating", () => {
    expect(pickableTry(im("to_review"), "u1")?.index).toBe(1);
    expect(pickableTry(im("to_review"), "u2")).toBeUndefined(); // already the image
    expect(pickableTry(im("approved"), "u1")?.index).toBe(1);
    expect(pickableTry(im("generating"), "u1")).toBeUndefined();
    expect(pickableTry(im("to_review"))).toBeUndefined(); // nothing shown
  });
  it("tells the brand's pick from the reviewer's", () => {
    expect(pickedByBrand(im("to_review"))).toBe(false);
    expect(pickedByBrand(im("to_review", true))).toBe(true);
  });
});

describe("tries as they land", () => {
  const im = (status: string, ...attempts: object[]) => ({ status, attempts }) as unknown as Image;
  const sent = { index: 1, url: "u1", best: false, review: { passed: false, summary: "off", goals: [{ goal: "count", passed: false, note: "3 of 4 stones" }] } };
  it("shows the newest try and where it stands while the image renders", () => {
    expect(liveTry(im("generating"), 3)).toBeNull(); // nothing rendered yet
    expect(liveTry(im("generating", { index: 1, url: "u1", best: false, review: null }), 3)).toEqual({ url: "u1", text: "Try 1 of 3 · reviewing…" });
    expect(liveTry(im("generating", sent), 3)).toEqual({ url: "u1", text: "Rejected — 3 of 4 stones · trying again (2 of 3)" });
    const noNote = { ...sent, review: { passed: false, summary: "too dark", goals: [] } };
    expect(liveTry(im("generating", noNote), 3)?.text).toBe("Rejected — too dark · trying again (2 of 3)");
    expect(liveTry(im("to_review", sent), 3)).toBeNull(); // final: the tile shows the image
  });
});
