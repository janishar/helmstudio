// @vitest-environment jsdom
// The error states' words and timing (docs/mock/ErrorStates.dc.html): the banner's checks and back-off (a1, a2), what
// raises it (a2r), no runner (b), a run whose runner was lost or that failed (c1, c2), live updates paused (d), a run
// that waits (e), a model on an unplugged drive (f) and the status tile (g).
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  arm,
  isDown,
  isOutage,
  onBack,
  reportAnswer,
  reportOutage,
  retryNow,
  statusOf,
  useHealth,
  whenUp,
} from "@/lib/health";
import { ApiError, api, errorText, message } from "@/lib/api";
import { failDetails, failWords, followSession, waitText } from "@/lib/runs";
import {
  drivesOf,
  machineReason,
  machines,
  misfit,
  planWhy,
  runnerWhy,
  unpluggedDrive,
} from "@/lib/run-on";
import type { S } from "@/lib/api";

void useHealth; // (the hook is the components' test)
const flush = async () => {
  for (let i = 0; i < 6; i++) await Promise.resolve();
};

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe("a · the studio isn't answering", () => {
  it("a blip shows nothing: one failed call and a good check", async () => {
    const off = arm();
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => new Response("{}", { status: 200 })),
    );
    reportOutage();
    await flush();
    expect(isDown()).toBe(false);
    off();
  });

  it("down after a failed check; backs off 2, 4, 8, 15, 30, 30 s; Retry now resets; back online once", async () => {
    vi.useFakeTimers();
    const off = arm();
    const at: number[] = [];
    let up = false;
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => {
        at.push(Date.now());
        return new Response("{}", { status: up ? 200 : 503 });
      }),
    );
    const backs = vi.fn();
    const unsub = onBack(backs);
    const t0 = Date.now();
    reportOutage();
    await flush();
    expect(isDown()).toBe(true);
    reportOutage(); // already down: no second check
    for (const s of [2, 4, 8, 15, 30, 30]) {
      await vi.advanceTimersByTimeAsync(s * 1000);
    }
    expect(at.map((x) => (x - t0) / 1000)).toEqual([0, 2, 6, 14, 29, 59, 89]);
    // Retry now: at once, and the next wait is 2 s again
    retryNow();
    await flush();
    await vi.advanceTimersByTimeAsync(2000);
    expect(at.length).toBe(9);
    // a good answer to any call is the studio back
    let waited = false;
    void whenUp().then(() => (waited = true));
    up = true;
    reportAnswer();
    await flush();
    expect(isDown()).toBe(false);
    expect(waited).toBe(true);
    expect(backs).toHaveBeenCalledTimes(1);
    unsub();
    off();
  });

  it("a hidden tab pauses the checks; it checks at once when it shows", async () => {
    vi.useFakeTimers();
    const off = arm();
    const fetch = vi.fn(async () => new Response("{}", { status: 502 }));
    vi.stubGlobal("fetch", fetch);
    reportOutage();
    await flush();
    let hidden = true;
    Object.defineProperty(document, "hidden", { configurable: true, get: () => hidden });
    await vi.advanceTimersByTimeAsync(60_000);
    expect(fetch).toHaveBeenCalledTimes(1);
    hidden = false;
    document.dispatchEvent(new Event("visibilitychange"));
    await flush();
    expect(fetch).toHaveBeenCalledTimes(2);
    off();
  });

  it("nothing is checked without a banner on the page", async () => {
    const fetch = vi.fn();
    vi.stubGlobal("fetch", fetch);
    reportOutage();
    await flush();
    expect(fetch).not.toHaveBeenCalled();
  });

  it("a call the page stopped asks no check; a TypeError of the code is no outage", async () => {
    const off = arm();
    const probe = vi.fn(async () => new Response("{}", { status: 503 }));
    vi.stubGlobal("fetch", probe);
    const fails = (e: Error) =>
      api
        .GET("/v1/studio/hosts", {
          baseUrl: "http://web.test/api", // (node's Request needs a whole URL)
          fetch: async () => {
            throw e;
          },
        })
        .catch(() => undefined);
    await fails(new DOMException("stopped", "AbortError"));
    await flush();
    expect(probe).not.toHaveBeenCalled();
    await fails(new TypeError("Failed to fetch"));
    await flush();
    expect(probe).toHaveBeenCalledTimes(1);
    expect(isDown()).toBe(true);
    expect(isOutage(new TypeError("Cannot read properties of undefined (reading 'id')"))).toBe(
      false,
    );
    off();
  });

  it("a check still out when a call answers says nothing when it ends", async () => {
    vi.useFakeTimers();
    const off = arm();
    const ends: ((r: Response) => void)[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(() => new Promise<Response>((r) => ends.push(r))),
    );
    reportOutage();
    await flush();
    ends[0](new Response("{}", { status: 503 }));
    await flush();
    expect(isDown()).toBe(true);
    retryNow(); // a second check, still out
    await flush();
    reportAnswer(); // a call answered meanwhile: back
    expect(isDown()).toBe(false);
    ends[1](new Response("{}", { status: 504 }));
    await flush();
    expect(isDown()).toBe(false);
    off();
  });

  it("raises on 502, 503, 504 and a network failure, never on a 4xx; their words are the banner's", () => {
    expect(isOutage(new ApiError(503, "x"))).toBe(true);
    expect(isOutage(new TypeError("Failed to fetch"))).toBe(true);
    expect(isOutage(new ApiError(404, "x"))).toBe(false);
    expect(isOutage(new ApiError(500, "x"))).toBe(false);
    expect(message(new TypeError("Failed to fetch"))).toBe("The studio isn’t answering.");
    expect(errorText(504, null)).toBe("The studio isn’t answering.");
    expect(errorText(409, { detail: "changed" })).toBe("changed");
  });
});

const host = (h: string, env: string, state: S<"Host">["state"], over: Partial<S<"Host">> = {}) =>
  ({ host: h, env, state, updated_at: "2026-11-27T10:12:00Z", ...over }) as S<"Host">;
const NOW = Date.parse("2026-11-27T10:20:00Z");

describe("b · no runner for this model", () => {
  const mac = (state: S<"Host">["state"]) => [
    host("mac-studio", "mlx", state, {
      device: { kind: "mps", name: "Apple M5 Pro", memory_total_gb: 64 },
    }),
    host("mac-studio", "torch", "idle", {
      device: { kind: "mps", name: "Apple M5 Pro", memory_total_gb: 64 },
    }),
    host("gcp", "torch", "idle", {
      device: { kind: "cuda", name: "NVIDIA RTX PRO 6000", memory_total_gb: 96, driver: "580" },
    }),
  ];

  it("Generate's words and the line under the composer", () => {
    expect(runnerWhy(mac("offline"), "mlx")).toEqual({
      key: "No runner for mlx",
      line: "No runner for mlx is on. Start helmstudio on your Mac, or pick another model.",
    });
    expect(runnerWhy(mac("idle"), "mlx")).toBeNull();
    // an empty list is left to the plan's reason
    expect(runnerWhy([], "mlx")).toBeNull();
    // an env no host publishes (a workflow served some other way) is the plan's to say
    expect(runnerWhy(mac("offline"), "server")).toBeNull();
    const [m] = machines(mac("idle"));
    expect(runnerWhy(mac("idle"), "mlx", { ...m, live: false })).toEqual({
      key: "Mac is offline",
      line: "mac-studio is offline. Pick Auto, or wait for it to come back.",
    });
  });

  it("the plan's reason: no live host is noRunner's, any other is said as it is", () => {
    expect(planWhy("no live host for env mlx")?.key).toBe("No runner for mlx");
    expect(planWhy("host gcp does not have the distilled LoRA")).toEqual({
      key: "Can’t run now",
      line: "Host gcp does not have the distilled LoRA.",
    });
    expect(planWhy(null)).toBeNull();
  });

  it("each Run on row says why: its mlx runner stopped, mlx runs on a Mac only", () => {
    const [mc, gcp] = machines(mac("offline"));
    expect(machineReason(mc, "mlx", [])).toBe("mlx runner stopped");
    expect(machineReason(gcp, "mlx", [])).toBe("mlx runs on a Mac only");
  });
});

describe("c · runner lost mid-run", () => {
  const run = (over: Partial<S<"Run">>) =>
    ({
      id: "7f3c2a99",
      status: "queued",
      attempt: 1,
      host: null,
      error: null,
      ...over,
    }) as S<"Run">;

  it("trying again (2 of 2), with the host that stopped", () => {
    expect(waitText(run({ attempt: 2, lost_host: "mac-studio" }))).toEqual({
      line: "Runner on mac-studio stopped · trying again (2 of 2)",
      tile: ["Trying again", "2 of 2"],
      retry: true,
    });
  });

  it("the failure in plain words, the raw text under Show details", () => {
    const label = (h: string) => (h === "mac-studio" ? "Mac" : h);
    expect(
      failWords("runner on host mac-studio stopped responding; gave up after 2 attempts"),
    ).toBe("The runner on mac-studio stopped twice. Retry starts it again.");
    expect(
      failWords("runner on host mac-studio stopped responding; no other live host for env mlx"),
    ).toBe(
      "The runner on mac-studio stopped and no other runner for mlx is on. Start helmstudio on your Mac, then Retry.",
    );
    expect(
      failWords(
        "host mac-studio went offline; a run set to mac-studio never moves to another host",
        label,
      ),
    ).toBe(
      "mac-studio went offline. A run set to Mac never moves to another machine. Pick Auto, or Retry when it’s back.",
    );
    expect(failWords("no live host for env mlx")).toBe(
      "No runner for mlx was on when this run started. Start helmstudio on your Mac, then Retry.",
    );
    expect(failWords("out of memory")).toBe("This run stopped with an error.");
    expect(failDetails({ id: "7f3c2a99", attempt: 2, error: "gave up after 2 attempts" })).toBe(
      "run 7f3c2a · attempt 2 of 2\ngave up after 2 attempts",
    );
  });
});

describe("d · live updates paused", () => {
  class Source {
    static CLOSED = 2;
    static all: Source[] = [];
    readyState = 0;
    onerror: (() => void) | null = null;
    onopen: (() => void) | null = null;
    constructor() {
      Source.all.push(this);
    }
    addEventListener() {}
    close() {
      this.readyState = 2;
    }
  }

  it("shows after polling failed 5 s, goes when a page answers; now tries the stream", async () => {
    vi.useFakeTimers();
    vi.stubGlobal("EventSource", Source);
    Source.all = [];
    let up = false;
    const states: boolean[] = [];
    let now: () => void = () => undefined;
    const stop = followSession(
      "s1",
      () => undefined,
      async () => {
        if (!up) throw new Error("502");
        return { events: [] } as unknown as S<"EventPage">;
      },
      (on, n) => {
        states.push(on);
        now = n;
      },
    );
    const s = Source.all[0];
    s.readyState = 2;
    s.onerror?.();
    await vi.advanceTimersByTimeAsync(4000);
    expect(states).toEqual([]); // a dropped stream that polling covers shows nothing
    await vi.advanceTimersByTimeAsync(1500);
    expect(states).toEqual([true]);
    now();
    expect(Source.all.length).toBe(2);
    up = true;
    await vi.advanceTimersByTimeAsync(20_000);
    expect(states).toEqual([true, false]);
    stop();
  });
});

describe("e · queued behind other work", () => {
  it("Waiting · 2 ahead on mac-studio; first in line is plain Queued unless a model unloads", () => {
    const r = { status: "queued", attempt: 1, host: "mac-studio", error: null } as S<"Run">;
    expect(waitText({ ...r, position: 3 })?.line).toBe("Waiting · 2 ahead on mac-studio");
    expect(waitText({ ...r, position: 3 })?.tile).toEqual(["Waiting", "2 ahead"]);
    expect(waitText({ ...r, position: 1 })).toBeNull();
    expect(
      waitText({
        ...r,
        position: 1,
        unloads_pinned: [
          { env: "mlx", component: "vae", model: "m", variant: "v", precision: "bf16", gb: 1 },
        ],
      })?.line,
    ).toBe("Waiting for mac-studio");
    expect(waitText({ ...r, status: "running" })).toBeNull();
  });

  it("a busy machine is busy on its Run on row", () => {
    const [m] = machines([host("mac-studio", "mlx", "busy")]);
    expect(m.busy).toBe(true);
  });
});

describe("f · model on an unplugged drive", () => {
  const list = {
    hosts: [
      {
        host: "mac-studio",
        live: true,
        folders: [
          { id: "f1", name: "Mac", connected: true },
          { id: "f2", name: "T9", connected: false },
        ],
      },
    ],
    models: [
      {
        family: "ltx",
        variants: [
          {
            id: "base",
            title: "Base",
            hosts: [
              {
                host: "mac-studio",
                state: "unplugged",
                copies: [{ folder: "f2", state: "unplugged" }],
              },
            ],
          },
        ],
      },
    ],
  } as unknown as S<"ModelList">;

  it("names the drive by its folder's label, on Generate and the Run on row", () => {
    expect(unpluggedDrive(list, "ltx")).toBe("T9");
    const v = list.models[0].variants[0];
    expect(drivesOf(list, v)).toEqual({ "mac-studio": "T9" });
    const [mc] = machines([host("mac-studio", "mlx", "idle")]);
    expect(misfit(mc, list.models[0], v, drivesOf(list, v))?.text).toBe("on T9 · not connected");
    // a copy present elsewhere clears it
    const present = structuredClone(list) as S<"ModelList">;
    (present.models[0].variants[0].hosts[0] as { state: string }).state = "present";
    expect(unpluggedDrive(present, "ltx")).toBeNull();
  });
});

describe("g · the status tile", () => {
  const hosts = [
    host("mac-studio", "mlx", "offline", { updated_at: "2026-11-27T10:19:50Z" }),
    host("mac-studio", "torch", "idle"),
    host("gcp", "torch", "offline", {
      updated_at: "2026-11-27T08:12:00Z",
      device: {
        kind: "cuda",
        name: "NVIDIA RTX PRO 6000 Blackwell Server Edition",
        memory_total_gb: 96,
      },
    }),
  ];

  it("Runner down for a runner that stopped in the last hour; one row each", () => {
    const s = statusOf(hosts, false, NOW);
    expect(s.tile).toBe("Runner down");
    expect(s.rows.map((r) => [r.name, r.sub, r.text])).toEqual([
      ["Studio", "", "Answering"],
      ["NVIDIA", "gcp · RTX PRO 6000", expect.stringMatching(/^Offline since \d\d:\d\d$/)],
      ["mlx runner", "mac-studio", "Stopped"],
      ["torch runner", "mac-studio", "Ready"],
    ]);
  });

  it("an old offline host raises nothing; none up is No runner; the studio down is Studio down", () => {
    expect(statusOf([hosts[1], hosts[2]], false, NOW).tile).toBeNull();
    expect(statusOf([hosts[0]], false, NOW).tile).toBe("No runner");
    expect(statusOf(hosts, true, NOW).tile).toBe("Studio down");
    expect(statusOf(null, false, NOW).tile).toBeNull();
  });
});
