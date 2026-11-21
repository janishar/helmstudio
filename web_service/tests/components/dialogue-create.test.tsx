// @vitest-environment jsdom
// Create on a dialogue template (API 1.32, docs/mock/CreateAudio.dc.html Dialogue, CreateAudioZonos3.dc.html): the
// Voice-over | Dialogue switch, the script drawn with each speaker's chip and voice, a line's error inline with its
// fix and Generate waiting on it, the script sent as typed, and studio's 422 put on its line.
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import workflows from "../../../contracts/fixtures/registry/workflows.valid.json";
import coreNodes from "../../../contracts/fixtures/registry/nodes.valid.json";
import { Create } from "@/components/create/create";
import { Modal } from "@/components/kit";
import { type S } from "@/lib/api";
import { answer } from "./helpers";

vi.mock("@/lib/runs", async (orig) => ({
  ...(await orig<typeof import("@/lib/runs")>()),
  followSession: () => () => undefined,
}));

beforeAll(() => {
  globalThis.requestAnimationFrame ??= (f: FrameRequestCallback) =>
    setTimeout(() => f(0), 0) as unknown as number;
  HTMLElement.prototype.hidePopover ??= () => undefined;
  Element.prototype.scrollIntoView ??= () => undefined;
  HTMLDialogElement.prototype.showModal ??= function (this: HTMLDialogElement) {
    this.open = true;
  };
  HTMLDialogElement.prototype.close ??= function (this: HTMLDialogElement) {
    this.open = false;
  };
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

type Item = S<"LibraryItem">;
const schemas = Object.fromEntries(
  coreNodes.nodes.map((n) => [n.kind, n]),
) as unknown as S<"Workflow">["node_schemas"];
const wf = (id: string) =>
  ({
    ...workflows.workflows.find((w) => w.id === id),
    node_schemas: schemas,
    source: "template",
  }) as unknown as S<"Workflow">;
const base = {
  text: "",
  images: [],
  thumb_url: null,
  version: 1,
  created_at: "2026-11-21T10:00:00Z",
  updated_at: "2026-11-21T10:00:00Z",
  loras: [],
  needs: [],
};
const ada = {
  ...base,
  id: "echo/ada",
  kind: "voice",
  group: "preset",
  name: "Ada",
  handle: null,
  source: "starter",
  family: "echo",
  voice: { source: "preset", family: "echo", speaker: "ada", language: "en" },
} as unknown as Item;
const el = (handle: string, voice_id: string | null) =>
  ({
    ...base,
    id: `id-${handle}`,
    kind: "element",
    group: "character",
    name: handle[0].toUpperCase() + handle.slice(1),
    handle,
    source: "mine",
    family: null,
    voice_id,
  }) as unknown as Item;

describe("Create on a dialogue template", () => {
  it("the switch, the script's chips, a line's error and fix, the script sent, studio's 422 on its line", async () => {
    answer("GET", {
      "/v1/studio/sessions/{session_id}/runs": () => ({ data: { runs: [] } }),
      "/v1/studio/sessions/{session_id}/takes": () => ({ data: { takes: [] } }),
      "/v1/studio/library": ({ params }) => {
        const kind = (params as { query?: { kind?: string } })?.query?.kind;
        return {
          data: {
            items:
              kind === "voice"
                ? [ada]
                : kind === "element"
                  ? [el("mara", "echo/ada"), el("kai", null)]
                  : [],
          },
        };
      },
      "/v1/studio/hosts": () => ({ data: { hosts: [] } }),
      "/v1/studio/loras": () => ({ data: { loras: [] } }),
    });
    const bodies: { inputs: Record<string, unknown> }[] = [];
    answer("POST", {
      "/v1/studio/sessions/{session_id}/runs": ({ body }) => {
        bodies.push(body as (typeof bodies)[number]);
        return {
          status: 422,
          error: { detail: "dialogue.script line 1: voice Ada does not fit this workflow (x)" },
        };
      },
    });
    render(
      <Create
        initial={{
          sessions: [],
          workflows: [wf("echo-dialogue"), wf("echo-voice-over")],
          sessionId: "s1",
          runs: [],
          takes: [],
        }}
      />,
    );
    const modes = await screen.findByRole("group", { name: "Template" });
    expect(
      within(modes).getByRole("button", { name: "Dialogue" }).getAttribute("aria-pressed"),
    ).toBe("true");
    const composer = screen.getByRole("form", { name: "Composer" });
    const ta = within(composer).getByLabelText("Script");
    fireEvent.change(ta, { target: { value: "@mara (quiet): Noon.\n\n@kai: Hm." } });
    fireEvent.blur(ta);
    const script = await within(composer).findByRole("list", { name: "Script" });
    await waitFor(() =>
      expect(within(script).getByRole("button", { name: "@mara, voice Ada" })).toBeTruthy(),
    );
    expect(within(script).getByText("(quiet):")).toBeTruthy();
    expect(within(script).getByText("Line 2: @kai has no voice")).toBeTruthy();
    expect(within(script).getByRole("button", { name: "Pick a voice" })).toBeTruthy();
    const go = composer.querySelector<HTMLButtonElement>('button[type="submit"]')!;
    expect(go.disabled).toBe(true);
    expect(go.textContent).toContain("Fix line 2 first");
    expect(within(composer).getByRole("button", { name: /^Lines/ }).textContent).toContain(
      "Mara, Kai · 1 to fix",
    );

    // edit the script: Kai's line goes, and Generate sends the script as typed (studio expands it)
    fireEvent.click(within(script).getByText("Noon."));
    fireEvent.change(within(composer).getByLabelText("Script"), {
      target: { value: "@mara (quiet): Noon." },
    });
    fireEvent.blur(within(composer).getByLabelText("Script"));
    await waitFor(() => expect(go.disabled).toBe(false));
    fireEvent.click(go);
    await waitFor(() => expect(bodies).toHaveLength(1));
    expect(bodies[0].inputs).toMatchObject({ "dialogue.script": "@mara (quiet): Noon." });
    expect(
      await within(composer).findByText("Line 1: voice Ada does not fit this workflow (x)"),
    ).toBeTruthy();
  });
});

describe("a Modal inside a Modal", () => {
  it("closing the inner one (a voice picker in the element editor) leaves the outer one open", () => {
    const outer = vi.fn();
    const inner = vi.fn();
    render(
      <Modal open onClose={outer} title="Edit element">
        <Modal open onClose={inner} title="Voice">
          x
        </Modal>
      </Modal>,
    );
    fireEvent(screen.getByLabelText("Voice"), new Event("close"));
    expect(inner).toHaveBeenCalledOnce();
    expect(outer).not.toHaveBeenCalled();
  });
});
