// @vitest-environment jsdom
// The shell's await and navigate (components/assistant/tools.tsx; plan 18 §22.3, §22.4): await reads the run from the
// tab every 2 s with its place in line, a hidden tab waits until shown ("Waiting for the tab"), past its cap it
// answers still_running; a job or an export ends the same way; navigate fails when the screen does not load.
import { afterEach, describe, expect, it, vi } from "vitest";
import { Registry, awaitIt, navigateTo } from "@/components/assistant/tools";
import { answer } from "./helpers";

afterEach(() => {
  vi.restoreAllMocks();
  vi.useRealTimers();
  Object.defineProperty(document, "hidden", { value: false, configurable: true });
});

const run = (status: string, over: Record<string, unknown> = {}) => ({
  data: {
    id: "r1",
    status,
    outputs: status === "succeeded" ? [{ take_id: "t1" }, { take_id: "t2" }] : [],
    error: status === "failed" ? "out of memory" : null,
    ...over,
  },
});

describe("await", () => {
  it("waits for the run with its place in line, then says what it made", async () => {
    vi.useFakeTimers();
    let n = 0;
    answer("GET", {
      "/v1/studio/runs/{run_id}": () =>
        ++n < 3 ? run("queued", { position: 2 }) : run("succeeded"),
    });
    const lines: string[] = [];
    const p = awaitIt({ what: "run", id: "r1" }, (t) => lines.push(t));
    await vi.advanceTimersByTimeAsync(5000);
    expect(await p).toEqual({
      status: "done",
      text: "Run done · 2 takes",
      result: { status: "succeeded", take_ids: ["t1", "t2"] },
    });
    expect(lines).toEqual([
      "Waiting for run · 2nd in line · 0:00",
      "Waiting for run · 2nd in line · 0:02",
    ]);
  });

  it("a failed run fails the line with its reason", async () => {
    answer("GET", { "/v1/studio/runs/{run_id}": () => run("failed") });
    expect(await awaitIt({ what: "run", id: "r1" }, () => undefined)).toMatchObject({
      status: "failed",
      text: "Run failed · out of memory",
    });
  });

  it("answers still_running past its cap", async () => {
    vi.useFakeTimers();
    answer("GET", { "/v1/studio/runs/{run_id}": () => run("running") });
    const p = awaitIt({ what: "run", id: "r1" }, () => undefined, 6000);
    await vi.advanceTimersByTimeAsync(10000);
    expect(await p).toEqual({
      status: "still_running",
      text: "Still running · 10 min passed. Say go on when it is done",
    });
  });

  it("a hidden tab waits until it is shown, and the time hidden does not count", async () => {
    vi.useFakeTimers();
    Object.defineProperty(document, "hidden", { value: true, configurable: true });
    const get = answer("GET", { "/v1/studio/runs/{run_id}": () => run("succeeded") });
    const lines: string[] = [];
    const p = awaitIt({ what: "run", id: "r1" }, (t) => lines.push(t), 2000);
    await vi.advanceTimersByTimeAsync(60_000);
    expect(lines).toEqual(["Waiting for the tab · it is hidden"]);
    expect(get).not.toHaveBeenCalled();
    Object.defineProperty(document, "hidden", { value: false, configurable: true });
    document.dispatchEvent(new Event("visibilitychange"));
    expect(await p).toMatchObject({ status: "done", text: "Run done · 2 takes" });
  });

  it("a model job and an export end the same way; a clone is not on this screen yet", async () => {
    answer("GET", {
      "/v1/studio/admin/model-jobs/{job_id}": () => ({
        data: { state: "succeeded", title: "LTX 2.3 · fp8" },
      }),
      "/v1/studio/media-jobs/{job_id}": () => ({ data: { status: "failed" } }),
    });
    expect(await awaitIt({ what: "download", id: "j" }, () => undefined)).toMatchObject({
      status: "done",
      text: "Done · LTX 2.3 · fp8",
    });
    expect(await awaitIt({ what: "export", id: "j" }, () => undefined)).toMatchObject({
      status: "failed",
      text: "Export failed",
    });
    expect(await awaitIt({ what: "clone", id: "j" }, () => undefined)).toMatchObject({
      status: "failed",
    });
  });
});

describe("navigate", () => {
  it("fails when the screen does not load", async () => {
    vi.useFakeTimers();
    const reg = new Registry();
    reg.setPath("/");
    const push = vi.fn();
    const p = navigateTo(reg, { to: "/timeline", query: { t: "s2" } }, push);
    await vi.advanceTimersByTimeAsync(11_000);
    expect(push).toHaveBeenCalledWith("/timeline?t=s2");
    expect(await p).toMatchObject({
      status: "failed",
      text: "Did not open · the screen did not load",
    });
  });
});
