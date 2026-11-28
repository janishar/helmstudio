// @vitest-environment jsdom
// The error states on the page (docs/mock/ErrorStates.dc.html): the banner and Back online (a1, a2) with a page's own
// note held back, Create's no-runner line and greyed Generate (b1), trying again (c1), the failed run's plain words,
// Show details and Retry with its right-click rows (c2), Live updates paused (d), Waiting · N ahead (e), the
// unplugged drive (f), the rail logo's status dot and its popover (g), and the Timeline's Make shot with no runner (b3).
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import coreNodes from "../../../contracts/fixtures/registry/nodes.valid.json";
import h3Nodes from "../../../contracts/fixtures/registry/minimax-h3.nodes.valid.json";
import h3Workflows from "../../../contracts/fixtures/registry/minimax-h3.workflows.valid.json";
import { Create } from "@/components/create/create";
import { Banner, StatusLogo } from "@/components/status";
import { ErrorNote } from "@/components/kit";
import { ShotComposer } from "@/components/timeline/shot";
import { useHosts } from "@/components/create/memory-chip";
import { closeMenu } from "@/components/context-menu";
import { reportOutage } from "@/lib/health";
import type { S } from "@/lib/api";
import type { Norm } from "@/lib/timeline";
import { answer } from "./helpers";

let paused: ((on: boolean, now: () => void) => void) | undefined;
vi.mock("@/lib/runs", async (orig) => ({
  ...(await orig<typeof import("@/lib/runs")>()),
  followSession: (_s: string, _f: unknown, _p: unknown, onPaused?: typeof paused) => {
    paused = onPaused;
    return () => undefined;
  },
}));
vi.mock("next/link", () => ({
  default: ({ href, children, ...rest }: { href: string; children: React.ReactNode }) => (
    <a href={href} {...rest}>
      {children}
    </a>
  ),
}));
vi.mock("next/navigation", () => ({ useRouter: () => ({ push: vi.fn() }) }));

beforeAll(() => {
  globalThis.requestAnimationFrame ??= (f: FrameRequestCallback) =>
    setTimeout(() => f(0), 0) as unknown as number;
  HTMLElement.prototype.hidePopover ??= () => undefined;
  Element.prototype.scrollIntoView ??= () => undefined;
});
afterEach(() => {
  act(closeMenu);
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

describe("a · the banner", () => {
  it("Studio isn't answering, the countdown and Retry now; the page's own note held back; then Back online", async () => {
    let up = false;
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => new Response("{}", { status: up ? 200 : 503 })),
    );
    render(
      <>
        <Banner />
        <ErrorNote>The studio isn’t answering.</ErrorNote>
        <ErrorNote>That lane is gone.</ErrorNote>
      </>,
    );
    expect(screen.queryByRole("status")).toBeNull();
    expect(screen.getAllByRole("alert")).toHaveLength(2);
    act(() => reportOutage());
    const ban = await screen.findByText("Studio isn’t answering. Retrying…");
    // one message, not five: the note for the same cause hides; another one stays
    expect(screen.getAllByRole("alert").map((a) => a.textContent)).toEqual(["That lane is gone."]);
    await waitFor(() => expect(ban.parentElement!.textContent).toMatch(/next try in \d s/), {
      timeout: 4000,
    });
    up = true;
    fireEvent.click(screen.getByRole("button", { name: "Retry now" }));
    expect(await screen.findByText("Back online")).toBeTruthy();
    expect(screen.getAllByRole("alert")).toHaveLength(2);
  });
});

const h3 = structuredClone(h3Workflows.workflows[0]);
h3.nodes.generate.params.lora = "none"; // the Turbo LoRA (the default) is an add-on these states don't speak of
const wf = {
  ...h3,
  node_schemas: Object.fromEntries([...coreNodes.nodes, ...h3Nodes.nodes].map((n) => [n.kind, n])),
  source: "template",
} as unknown as S<"Workflow">;
const ENV = wf.env;
const host = (h: string, state: string, over: object = {}) => ({
  host: h,
  env: ENV,
  state,
  updated_at: new Date().toISOString(),
  device: { kind: "mps", name: "Apple M5 Pro", memory_total_gb: 64 },
  ...over,
});
const run = (over: Partial<S<"Run">>) =>
  ({
    id: "7f3c2a00-0000-4000-8000-000000000001",
    session_id: "s1",
    workflow_id: wf.id,
    inputs: { "sample.seed": 44 },
    status: "running",
    progress: null,
    error: null,
    outputs: [],
    host: "mac",
    attempt: 1,
    created_at: "2026-11-27T10:00:00Z",
    started_at: null,
    finished_at: null,
    lane: "interactive",
    ...over,
  }) as unknown as S<"Run">;

function create(r: S<"Run"> | null, opts: { hosts?: unknown[]; models?: unknown } = {}) {
  const posts: { path: string; body: unknown }[] = [];
  const deletes: string[] = [];
  answer("GET", {
    "/v1/studio/sessions/{session_id}/runs": () => ({ data: { runs: r ? [r] : [] } }),
    "/v1/studio/sessions/{session_id}/takes": () => ({ data: { takes: [] } }),
    "/v1/studio/library": () => ({ data: { items: [] } }),
    "/v1/studio/loras": () => ({ data: { loras: [] } }),
    "/v1/studio/hosts": () => ({ data: { hosts: opts.hosts ?? [host("mac", "idle")] } }),
    ...(opts.models ? { "/v1/studio/models": () => ({ data: opts.models }) } : {}),
    "/v1/studio/runs/{run_id}": () => ({ data: r }),
  });
  answer("POST", {
    "/v1/studio/runs/plan": () => ({ data: { host: "mac", peak_gb: null, reason: null } }),
    "/v1/studio/sessions/{session_id}/runs": (init) => {
      posts.push({ path: "runs", body: init.body });
      return {
        status: 202,
        data: run({ id: "7f3c2a00-0000-4000-8000-000000000002", status: "queued" }),
      };
    },
  });
  answer("DELETE", {
    "/v1/studio/runs/{run_id}": (init) => {
      deletes.push((init.params as { path: { run_id: string } }).path.run_id);
      return { status: 204 };
    },
  });
  render(
    <Create
      initial={{ sessions: [], workflows: [wf], sessionId: "s1", runs: r ? [r] : [], takes: [] }}
    />,
  );
  return { posts, deletes };
}
const generate = () => document.querySelector<HTMLButtonElement>("button[type=submit].key")!;

describe("b1 · Create · no runner for the model", () => {
  it("Generate greyed with the reason, the visible line, the Run on chip warns", async () => {
    create(null, { hosts: [host("mac", "offline")] });
    await waitFor(() => expect(generate().textContent).toContain(`No runner for ${ENV}`));
    expect((generate() as HTMLButtonElement).disabled).toBe(true);
    expect(screen.getByText(new RegExp(`^No runner for ${ENV} is on\\. `))).toBeTruthy();
    expect(screen.getByRole("button", { name: "Run on Auto" }).className).toContain("warn");
    // the wrong line is gone
    expect(screen.queryByText(/runs wait until one is/)).toBeNull();
  });
});

describe("c · runner lost mid-run", () => {
  it("c1: trying again (2 of 2), on the progress line and the tile", async () => {
    create(run({ status: "queued", attempt: 2, lost_host: "mac", host: null }));
    const strip = await screen.findByRole("group", { name: "Run progress" });
    expect(strip.textContent).toContain("Runner on mac stopped · trying again (2 of 2)");
    expect(screen.getAllByText("Waiting for a runner…").length).toBeGreaterThan(0);
    const tile = within(screen.getByRole("group", { name: "Takes" })).getByRole("button", {
      pressed: true,
    });
    expect(tile.textContent).toBe("Trying again2 of 2");
  });

  it("c2: This run stopped, in plain words; Show details with Copy; Retry replaces the tile", async () => {
    const failed = run({
      status: "failed",
      attempt: 2,
      error: "runner on host mac stopped responding; gave up after 2 attempts",
    });
    const { posts, deletes } = create(failed);
    const card = await screen.findByText("This run stopped");
    const panel = card.closest(".fpanel") as HTMLElement;
    expect(panel.textContent).toContain("The runner on mac stopped twice. Retry starts it again.");
    expect(panel.querySelector("pre")).toBeNull();
    fireEvent.click(within(panel).getByRole("button", { name: "Show details" }));
    expect(panel.querySelector("pre")!.textContent).toBe(
      "run 7f3c2a · attempt 2 of 2\nrunner on host mac stopped responding; gave up after 2 attempts",
    );
    expect(within(panel).getByRole("button", { name: "Copy" })).toBeTruthy();
    // the tile's right-click rows
    const strip = screen.getByRole("group", { name: "Takes" });
    fireEvent.contextMenu(within(strip).getByRole("button", { pressed: true }));
    const menu = screen.getByRole("menu");
    expect(
      within(menu)
        .getAllByRole("menuitem")
        .map((m) => m.textContent),
    ).toEqual(["Retry", "Show details", "Copy error", "Delete"]);
    act(closeMenu);
    // a double click makes one run
    fireEvent.click(within(panel).getByRole("button", { name: "Retry" }));
    fireEvent.click(within(panel).getByRole("button", { name: "Retry" }));
    await waitFor(() => expect(deletes).toEqual([failed.id]));
    expect(posts).toHaveLength(1);
    expect(posts[0].body).toMatchObject({ workflow_id: wf.id, inputs: { "sample.seed": 44 } });
    expect(screen.queryByText("This run stopped")).toBeNull();
  });
});

describe("d · live updates paused", () => {
  it("the chip after polling failed; a click tries the stream now", async () => {
    create(run({ progress: { node: "generate", step: 4, of: 6, phase_id: "sample" } }));
    await screen.findByRole("group", { name: "Run progress" });
    expect(screen.queryByText("Live updates paused · retrying")).toBeNull();
    const now = vi.fn();
    act(() => paused?.(true, now));
    fireEvent.click(screen.getByText("Live updates paused · retrying"));
    expect(now).toHaveBeenCalled();
    act(() => paused?.(false, now));
    expect(screen.queryByText("Live updates paused · retrying")).toBeNull();
  });
});

describe("e · queued behind other work", () => {
  it("Waiting · 2 ahead on mac, on the progress line and the tile; the busy Run on chip", async () => {
    create(run({ status: "queued", position: 3 }), { hosts: [host("mac", "busy")] });
    const strip = await screen.findByRole("group", { name: "Run progress" });
    expect(strip.textContent).toContain("Waiting · 2 ahead on mac");
    const tile = within(screen.getByRole("group", { name: "Takes" })).getByRole("button", {
      pressed: true,
    });
    expect(tile.textContent).toBe("Waiting2 ahead");
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Run on Auto · busy" })).toBeTruthy(),
    );
  });
});

describe("f · model on an unplugged drive", () => {
  it("On T9 · not connected, with Models", async () => {
    const models = {
      hosts: [
        {
          host: "mac",
          live: true,
          folders: [
            { id: "f1", name: "Mac", connected: true },
            { id: "f2", name: "T9", connected: false },
          ],
        },
      ],
      models: [
        {
          family: wf.family,
          runtimes: [],
          variants: [
            {
              id: "base",
              title: "Base",
              devices: ["mps"],
              hosts: [
                { host: "mac", state: "unplugged", copies: [{ folder: "f2", state: "unplugged" }] },
              ],
            },
          ],
        },
      ],
    };
    create(null, { models });
    await waitFor(() => expect(generate().textContent).toContain("On T9 · not connected"));
    expect((generate() as HTMLButtonElement).disabled).toBe(true);
    expect(screen.getByRole("link", { name: "Models" }).getAttribute("href")).toBe("/models");
  });
});

describe("g · the logo's status", () => {
  it("a plain logo when all is well; Runner down: a dot, the label, a click opens the popover", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    let hosts = [host("mac", "idle")];
    answer("GET", { "/v1/studio/hosts": () => ({ data: { hosts } }) });
    const logo = () => (
      <StatusLogo admin>
        {(w) => <button type="button">{w ? `helmstudio · ${w}` : "helmstudio"}</button>}
      </StatusLogo>
    );
    const { unmount } = render(logo());
    await act(() => vi.advanceTimersByTimeAsync(1500));
    expect(screen.getByRole("button", { name: "helmstudio" })).toBeTruthy();
    expect(document.querySelector(".logo-dot")).toBeNull();
    unmount();
    hosts = [host("mac", "offline"), host("mac", "idle", { env: "torch" })];
    render(logo());
    await act(() => vi.advanceTimersByTimeAsync(1500));
    expect(screen.getByRole("button", { name: "helmstudio · Runner down" })).toBeTruthy();
    expect(document.querySelector(".logo-dot")).toBeTruthy();
    const pop = document.getElementById("status")!;
    expect(
      within(pop)
        .getAllByRole("group", { hidden: true })
        .map((g) => g.getAttribute("aria-label")),
    ).toEqual(["Studio: Answering", `${ENV} runner on mac: Stopped`, "torch runner on mac: Ready"]);
    expect(pop.textContent).toContain("Runners on a Mac restart from helmstudio there.");
    expect(within(pop).getByRole("link", { name: "Open Models", hidden: true })).toBeTruthy();
    // a row's right-click menu: Open in Hosts (admins)
    fireEvent.contextMenu(within(pop).getAllByRole("group", { hidden: true })[1]);
    const menu = document.querySelector<HTMLElement>('[role="menu"]')!;
    expect([...menu.querySelectorAll('[role="menuitem"]')].map((m) => m.textContent)).toEqual([
      "Open in Hosts",
    ]);
  });
});

describe("g · one hosts poll", () => {
  it("every reader on the page shares the 10 s poll (the tile adds no call)", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const get = answer("GET", {
      "/v1/studio/hosts": () => ({ data: { hosts: [host("mac", "idle")] } }),
    });
    function Two() {
      const a = useHosts();
      const b = useHosts();
      return <p>{`${a?.length ?? "-"} ${b?.length ?? "-"}`}</p>;
    }
    render(<Two />);
    await act(() => vi.advanceTimersByTimeAsync(100));
    expect(screen.getByText("1 1")).toBeTruthy();
    const first = get.mock.calls.length;
    await act(() => vi.advanceTimersByTimeAsync(10_000));
    expect(get.mock.calls.length - first).toBe(1);
  });
});

describe("b3 · Timeline · Make shot", () => {
  it("no runner for its env greys Generate shot, with the line under it", async () => {
    const P = (name: string, type: string, more: object = {}) => ({ name, type, ...more });
    const t2v = {
      id: "ltx-t2v",
      title: "LTX Text to video",
      family: "ltx",
      env: "mlx",
      media: "video",
      source: "template",
      nodes: { prompt: { kind: "ltx/prompt" } },
      node_schemas: {
        "ltx/prompt": {
          title: "Prompt",
          params: [P("text", "text", { default: "" })],
          outputs: [{ name: "cond", type: "Conditioning" }],
        },
      },
    } as unknown as S<"Workflow">;
    const doc: Norm = {
      target: { width: 448, height: 256, fps: 24, sample_rate: 48000 },
      tracks: [{ name: "V1", kind: "video", clips: [] }],
      duration_s: 0,
      frames: 0,
    } as unknown as Norm;
    answer("GET", {
      "/v1/studio/library": () => ({ data: { items: [] } }),
      "/v1/studio/hosts": () => ({ data: { hosts: [{ ...host("mac", "offline"), env: "mlx" }] } }),
    });
    render(
      <ShotComposer
        tl={
          {
            id: "t1",
            session_id: null,
            defaults: { cast: [], looks: [] },
            takes: {},
          } as unknown as S<"Timeline">
        }
        doc={doc}
        facts={{}}
        slot={{ at: 0 }}
        workflows={[t2v]}
        onDone={vi.fn()}
        onClose={vi.fn()}
      />,
    );
    const key = await screen.findByRole("button", { name: /Generate shot/ });
    await waitFor(() => expect(key.textContent).toContain("No runner for mlx"));
    expect((key as HTMLButtonElement).disabled).toBe(true);
    expect(
      screen.getByText(
        "No runner for mlx is on. Start helmstudio on your Mac, or pick another model.",
      ),
    ).toBeTruthy();
  });
});
