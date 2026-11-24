// @vitest-environment jsdom
// New line with no voice yet (docs/mock/TimelineAudio.dc.html card "No voice yet", QA Q10): a voice-over's Generate
// line is off and says "Pick a voice first" where the time and memory line sits, the empty Voice tile is ringed;
// picking a voice turns it on.
import { afterEach, beforeAll, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import fixture from "../../../contracts/fixtures/http/Timeline.valid.json";
import qwenWfs from "../../../contracts/fixtures/registry/qwen3-tts.workflows.valid.json";
import qwenNodes from "../../../contracts/fixtures/registry/qwen3-tts.nodes.valid.json";
import coreNodes from "../../../contracts/fixtures/registry/nodes.valid.json";
import { type S } from "@/lib/api";
import { LineComposer } from "@/components/timeline/line";
import { answer } from "./helpers";

vi.mock("next/link", () => ({
  default: ({ href, children }: { href: string; children: React.ReactNode }) => (
    <a href={href}>{children}</a>
  ),
}));

beforeAll(() => {
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

const schemas = Object.fromEntries(
  [...coreNodes.nodes, ...qwenNodes.nodes].map((n) => [n.kind, n]),
);
const qwen = {
  ...qwenWfs.workflows.find((w) => w.id === "qwen3-tts-voice-over")!,
  node_schemas: schemas,
  source: "template",
} as unknown as S<"Workflow">;
const TL = fixture as unknown as S<"Timeline">;

const ryan = { id: "v-ryan", kind: "voice", name: "Ryan", source: "starter", group: "preset" };
function line(voice?: string) {
  answer("GET", {
    "/v1/studio/library": (init) => {
      const kind = (init.params as { query: { kind: string } }).query.kind;
      return { data: { items: kind === "voice" ? [ryan] : [] } };
    },
  });
  render(
    <LineComposer
      tl={TL}
      doc={{} as never}
      at={15}
      track="A1"
      workflows={[qwen]}
      ask={{ prompt: "Night came early to the harbour.", voice }}
      onDone={() => undefined}
      onClose={() => undefined}
    />,
  );
}

it("a voice-over with no voice: Generate line is off with the hint, the Voice tile ringed", async () => {
  line();
  const gen = await screen.findByRole("button", { name: /Pick a voice first/ });
  expect(gen.hasAttribute("disabled")).toBe(true);
  expect(gen.textContent).toContain("Generate line");
  expect(gen.textContent).not.toContain("≈");
  const tile = screen.getByRole("button", { name: /^Voice:/ });
  expect(tile.className).toContain("outline-a2");
});

it("with a voice picked Generate line is on, the time line back and no ring", async () => {
  line("Ryan");
  const gen = await screen.findByRole("button", { name: /Generate line\s*≈/ });
  expect(gen.hasAttribute("disabled")).toBe(false);
  expect(screen.getByRole("button", { name: /^Voice:/ }).className).not.toContain("outline-a2");
});
