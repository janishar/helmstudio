// The picked kind tab stands out (owner, 2026-11-27: "make selected one more differentiated from on active ones";
// docs/mock Main, TextToVideo, CreateMusicSound .ktab): a thin accent ring inside, the others' orbs dimmed until hover
// or focus, the focus ring still the outline outside.
import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const css = readFileSync(new URL("../../app/globals.css", import.meta.url), "utf8").replace(
  /\s+/g,
  " ",
);
const rule = (sel: string) =>
  css.match(new RegExp(`${sel.replace(/[[\]().:"]/g, "\\$&")} \\{([^}]*)\\}`))?.[1] ?? "";

describe("the kind tabs' CSS", () => {
  it("the picked tab has the accent ring; the others' orbs are dimmed, at full strength on hover or focus", () => {
    expect(rule('.ktab[aria-selected="true"]')).toContain("inset 0 0 0 1px var(--a1)");
    expect(rule('.ktab[aria-selected="false"] .ki')).toMatch(/opacity: 0\.4/);
    expect(rule('.ktab[aria-selected="false"]:is(:hover, :focus-visible) .ki')).toMatch(
      /opacity: 1/,
    );
    expect(rule('.ktab[aria-selected="false"]:is(:hover, :focus-visible)')).toContain("var(--t1)");
    // the focus ring stays the outline outside, not the ring inside
    expect(css).toMatch(/:focus-visible \{ outline: 2px solid var\(--a1\); outline-offset: 2px;/);
  });
});
