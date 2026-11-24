// An answer's options block and refs (lib/answer-refs.ts; alpha.2 items 17, 19).
import { describe, expect, it } from "vitest";
import { liveOptions, pickOf, refParts, splitOptions } from "@/lib/answer-refs";
import type { Entry } from "@/lib/assistant";

const OPTS = [
  { title: "Fix a part", line: "Repaint the hands" },
  { title: "Shot 2", best: true },
];
const block = (o: unknown) => "Next, pick one:\n```options\n" + JSON.stringify(o) + "\n```";
const e = (id: string, kind: Entry["kind"], text: string) =>
  ({ id, turn: 1, kind, at: "2026-11-25T00:00:00Z", text }) as Entry;

describe("options", () => {
  it("splits a good block from the text; a broken one stays text", () => {
    expect(splitOptions(block(OPTS))).toEqual({ text: "Next, pick one:", options: OPTS });
    for (const bad of ["```options\n[{]\n```", block([OPTS[0]]), block([{ title: "" }, OPTS[0]])])
      expect(splitOptions(bad)).toEqual({ text: bad, options: null });
    expect(splitOptions("plain")).toEqual({ text: "plain", options: null });
  });

  it("live until a message comes; then the one it named is the pick", () => {
    const a = e("a", "assistant", block(OPTS));
    expect(pickOf([a], "a", OPTS)).toEqual({ live: true, picked: null });
    expect(liveOptions([e("u", "user", "go"), a])).toEqual(OPTS);
    const said = [a, e("l", "line", ""), e("u", "user", " Shot 2 ")];
    expect(pickOf(said, "a", OPTS)).toEqual({ live: false, picked: "Shot 2" });
    expect(liveOptions(said)).toBeNull();
    expect(pickOf([a, e("u", "user", "something else")], "a", OPTS)).toEqual({
      live: false,
      picked: null,
    });
  });
});

describe("refs", () => {
  it("splits text and refs; a bad kind or an id with a space stays text", () => {
    expect(
      refParts(
        "Animate [[take:9a1b]] with [[voice:qwen3-tts/ryan]] [[film:x]] [[take:a b]] [[element:?]]",
      ),
    ).toEqual([
      { t: "Animate " },
      { kind: "take", id: "9a1b" },
      { t: " with " },
      { kind: "voice", id: "qwen3-tts/ryan" },
      { t: " [[film:x]] [[take:a b]] " },
      { kind: "element", id: "?" },
    ]);
  });
});
