// @vitest-environment jsdom
// The workflow picker's icons (docs/mock/Main.dc.html, the workflow picker): each row and the chip show what the
// workflow makes (image, video, audio, voice, text), from its media; an unknown media gets the workflow glyph.
import { afterEach, describe, expect, it } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { WorkflowPicker } from "@/components/create/workflows";
import type { S } from "@/lib/api";

afterEach(cleanup);

const wf = (id: string, media?: string, voice = false, source = "template") =>
  ({
    id,
    title: id,
    source,
    media,
    nodes: voice ? { speak: { kind: "q/speak" } } : {},
    node_schemas: voice ? { "q/speak": { params: [{ name: "voice", type: "voice" }] } } : {},
  }) as unknown as S<"Workflow">;

const iconOf = (name: string) =>
  screen
    .getByRole("menuitemradio", { name, hidden: true })
    .querySelector("svg")
    ?.getAttribute("class") ?? "";

describe("workflow picker icons", () => {
  it("gives each kind its icon, a fallback, and the chip the open one's", () => {
    const ws = [
      wf("pic", "image"),
      wf("old"), // no media: image
      wf("clip", "video"),
      wf("song", "audio"),
      wf("speak", "audio", true),
      wf("ask", "text"),
      wf("new", "hologram"),
      wf("mine", "video", false, "mine"),
    ];
    render(
      <WorkflowPicker
        workflows={ws}
        current={ws[2]}
        label="clip"
        onPick={() => undefined}
        onChanged={() => undefined}
        onDeleted={() => undefined}
        onError={() => undefined}
      />,
    );
    expect(iconOf("pic")).toContain("lucide-image");
    expect(iconOf("old")).toContain("lucide-image");
    expect(iconOf("clip")).toContain("lucide-film");
    expect(iconOf("song")).toContain("lucide-audio-lines");
    expect(iconOf("speak")).toContain("lucide-mic");
    expect(iconOf("ask")).toContain("lucide-message-square");
    expect(iconOf("new")).toContain("lucide-workflow");
    expect(iconOf("mine")).toContain("lucide-film");
    expect(screen.getByRole("button", { name: "Rename mine", hidden: true })).toBeTruthy();
    const chip = screen.getByRole("button", { name: "Workflow: clip. Change workflow" });
    expect(chip.querySelector("svg")?.getAttribute("class")).toContain("lucide-film");
  });
});
