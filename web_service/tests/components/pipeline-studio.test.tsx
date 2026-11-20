// @vitest-environment jsdom
// A pipeline's Studio view (docs/mock/Pipeline.dc.html; owner's Q2, Q3): steps run as ordinary runs in the
// pipeline's own session, the take picked from a step's results goes into the next step's bound input, and the
// aside's memory controls are for admins only.
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import echoFixture from "../../../contracts/fixtures/http/Workflow.valid.1.json";
import hostsFixture from "../../../contracts/fixtures/http/HostList.valid.2.json";
import { MemoryCard, PipelineStudio, ViewSwitch } from "@/components/pipelines/studio";
import { api, type S } from "@/lib/api";
import type { PDoc } from "@/lib/pipeline";
import { currentStep, saveStudio, sourceStep, stepRaws, takeBinds } from "@/lib/pipeline-studio";
import { fields } from "@/lib/workflow";
import { answer, take } from "./helpers";

let roles: string[] = [];
vi.mock("@/components/shell", () => ({ useUser: () => ({ name: "Ada", roles }) }));
vi.mock("next/link", () => ({
  default: ({ href, children, ...rest }: { href: string; children: React.ReactNode }) => (
    <a href={href} {...rest}>
      {children}
    </a>
  ),
}));
vi.stubGlobal(
  "EventSource",
  class {
    addEventListener() {}
    close() {}
  },
);

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  localStorage.clear();
  roles = [];
});

const echo = echoFixture as unknown as S<"Workflow">;
const hosts = (hostsFixture as unknown as S<"HostList">).hosts;
const SID = "11111111-1111-4111-8111-111111111111";
const doc: PDoc = {
  title: "Two echoes",
  nodes: {
    inputs: { kind: "inputs", inputs: [{ name: "prompt", type: "Text" }] },
    a: { kind: "step", workflow: "echo", seeds: 2, bind: { "prompt.text": "$prompt" } },
    pick: { kind: "gate", takes: "a" },
    b: { kind: "step", workflow: "echo", bind: { "echo.refs": "pick.picked" } },
    out: { kind: "output", from: "b.takes", collection: "x" },
  },
  edges: [
    ["inputs", "a"],
    ["a", "pick"],
    ["pick", { pass: "b", fail: "a", max: 2 }],
    ["b", "out"],
  ],
} as PDoc;

const run = (id: string, status: S<"Run">["status"] = "succeeded") =>
  ({
    id,
    session_id: SID,
    workflow_id: "echo",
    status,
    attempt: 1,
    outputs: [],
  }) as unknown as S<"Run">;

describe("the step plan", () => {
  it("passes the gate's pick of step 1 into step 2's rows", () => {
    expect(sourceStep(doc, "pick.picked")).toBe("a");
    expect(sourceStep(doc, "$prompt")).toBeNull();
    expect(takeBinds(doc, "b")).toEqual({ "echo.refs": "a" });
    const raws = stepRaws(doc, "b", echo, fields(echo), { a: "t1" });
    expect(raws["echo.refs"]).toEqual([{ image: { take_id: "t1" } }]);
  });
  it("opens step 1, then step 2 once step 1 ran, waiting for a pick", () => {
    expect(currentStep(doc, { session: null, steps: {} })).toBe("a");
    expect(currentStep(doc, { session: SID, steps: { a: { runs: ["r1"] } } })).toBe("b");
  });
});

function studio(admin = false) {
  roles = admin ? ["admin"] : [];
  return render(
    <PipelineStudio
      pipelineId="p1"
      doc={doc}
      workflows={[echo]}
      titles={{ echo: "Echo" }}
      hosts={null}
      head={null}
      onAddStep={() => undefined}
    />,
  );
}

describe("PipelineStudio", () => {
  it("runs step 1 as ordinary runs, one per seed, in a new session of the pipeline", async () => {
    const runs: unknown[] = [];
    answer("GET", {
      "/v1/studio/sessions/{session_id}/runs": () => ({ data: { runs: [] } }),
      "/v1/studio/sessions/{session_id}/takes": () => ({ data: { takes: [] } }),
    });
    answer("POST", {
      "/v1/studio/sessions": () => ({ status: 201, data: { id: SID, title: "Two echoes" } }),
      "/v1/studio/sessions/{session_id}/runs": ({ body }) => {
        runs.push(body);
        return { status: 202, data: run(`r${runs.length}`, "queued") };
      },
      "/v1/studio/runs/plan": () => ({ data: { host: null, peak_gb: null, reason: "x" } }),
    });
    studio();
    const cards = screen.getByLabelText("Pipeline steps");
    expect(within(cards).getByText("Waiting for you to pick an image")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Run step 1" }));
    await waitFor(() => expect(runs).toHaveLength(2));
    expect(runs[0]).toMatchObject({ workflow_id: "echo" });
    expect(JSON.parse(localStorage.getItem("pipeline-studio:p1")!)).toMatchObject({
      session: SID,
      steps: { a: { runs: ["r2", "r1"] } },
    });
  });

  it("passes the take picked from step 1 to step 2", async () => {
    saveStudio("p1", { session: SID, steps: { a: { runs: ["r1", "r2"] } } });
    const t1 = take({ run_id: "r1", made_with: { seed: 42 } as S<"MadeWith"> });
    const t2 = take({ run_id: "r2", made_with: { seed: 43 } as S<"MadeWith"> });
    const sent: S<"RunRequest">[] = [];
    answer("GET", {
      "/v1/studio/sessions/{session_id}/runs": () => ({ data: { runs: [run("r1"), run("r2")] } }),
      "/v1/studio/sessions/{session_id}/takes": () => ({ data: { takes: [t1, t2] } }),
    });
    answer("POST", {
      "/v1/studio/sessions/{session_id}/runs": ({ body }) => {
        sent.push(body as S<"RunRequest">);
        return { status: 202, data: run("r3", "queued") };
      },
      "/v1/studio/runs/plan": () => ({ data: { host: null, peak_gb: null, reason: "x" } }),
    });
    studio();
    const grid = await screen.findByRole("group", { name: "Pick one image to use" });
    const go = screen.getByRole("button", { name: "Run step 2" }) as HTMLButtonElement;
    expect(go.disabled).toBe(true);
    fireEvent.click(within(grid).getByRole("button", { name: "Take, seed 43" }));
    expect(screen.getByText("References · take seed 43")).toBeTruthy();
    fireEvent.click(go);
    await waitFor(() => expect(sent).toHaveLength(1));
    expect(sent[0].inputs?.["echo.refs"]).toEqual([{ image: { take_id: t2.id } }]);
  });
});

describe("the Memory card", () => {
  it("gives admins Pin, Unload, the after-queue choice and Unload all now", async () => {
    roles = ["admin"];
    const put = vi.spyOn(api, "PUT").mockResolvedValue({
      data: { ...hosts[0].admin!.settings!, after_run: "unload" },
      response: new Response(null, { status: 200 }),
    } as never);
    const post = answer("POST", {
      "/v1/studio/admin/hosts/{host}/unload": () => ({
        status: 202,
        data: { host: "mac", requests: [] },
      }),
    });
    render(<MemoryCard hosts={hosts} env="mlx" />);
    expect(screen.getAllByRole("button", { name: /^Unload ltx-2.5/ }).length).toBeGreaterThan(0);
    const pinned = screen.getByRole("button", { name: "Unpin ltx-2.5 transformer" });
    expect(pinned.getAttribute("aria-pressed")).toBe("true");
    fireEvent.click(screen.getByLabelText("Unload everything"));
    await waitFor(() =>
      expect(put).toHaveBeenCalledWith(
        "/v1/studio/admin/hosts/{host}/settings",
        expect.objectContaining({ body: expect.objectContaining({ after_run: "unload" }) }),
      ),
    );
    fireEvent.click(screen.getByRole("button", { name: "Unload all now" }));
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith(
        "/v1/studio/admin/hosts/{host}/unload",
        expect.objectContaining({ body: { all: true } }),
      ),
    );
  });

  it("is read-only for others", () => {
    render(<MemoryCard hosts={hosts} env="mlx" />);
    expect(screen.queryByRole("button", { name: /^(Pin|Unpin|Unload) ltx/ })).toBeNull();
    expect(
      (screen.getByRole("button", { name: "Unload all now" }) as HTMLButtonElement).disabled,
    ).toBe(true);
    // the host's settings are admin-only: a line, not radios with none checked
    expect(screen.queryByRole("radio")).toBeNull();
    expect(screen.getByText("What stays after the queue is set by an admin.")).toBeTruthy();
  });
});

describe("ViewSwitch", () => {
  it("links the execution view's Studio to the pipeline's Studio view", () => {
    render(<ViewSwitch view="graph" hrefs={{ studio: "/pipelines/p1?view=studio" }} />);
    expect(screen.getByRole("link", { name: "Studio" }).getAttribute("href")).toBe(
      "/pipelines/p1?view=studio",
    );
    expect(screen.getByRole("button", { name: "Graph" }).getAttribute("aria-pressed")).toBe("true");
  });
});
