import { describe, expect, it } from "vitest";
import list from "../../contracts/fixtures/http/PluginList.valid.json";
import skills from "../../contracts/fixtures/http/SkillList.valid.json";
import type { S } from "@/lib/api";
import {
  newSkill,
  problemAlert,
  problemCell,
  recipesRow,
  rescanLine,
  sourceRow,
  statusOf,
  subline,
  unusable,
  whenLine,
} from "@/lib/plugins";
import { guideLines, skillLines, skillsMatching } from "@/lib/skills";

const [bridge, , animator, storyboard] = (list as S<"PluginList">).plugins;
const line = (id: string, text: string, more: Partial<S<"AssistantLine">> = {}) =>
  ({ id, status: "done", text, undo: false, ...more }) as S<"AssistantLine">;

describe("skills on the Plugins page", () => {
  it("says a skill's status and row as the board does", () => {
    expect(statusOf(animator)).toEqual({ text: "On", ok: true });
    expect(statusOf(storyboard)).toEqual({ text: "Needs a newer helmstudio", ok: false });
    expect(statusOf({ ...storyboard, status: "broken" })).toEqual({
      text: "Can't be read",
      ok: false,
    });
    expect(subline(animator)).toBe("0.1.0 · built in · plugins/animator");
    expect(subline({ ...storyboard, source: "~/.helm/plugins/storyboard.zip" })).toBe(
      "1.0.0 · yours · ~/.helm/plugins/storyboard.zip",
    );
    expect(subline(bridge)).toBe("0.1.0 · this repo · plugins/claude-code-bridge");
    expect(unusable(animator)).toBe(false);
    expect(unusable(storyboard)).toBe(true);
    expect(problemCell(storyboard)).toBe(
      "Made for a newer helmstudio (format 2; this one reads 1)",
    );
  });
  it("fills a skill's aside", () => {
    expect(sourceRow(animator)).toBe("Built in · plugins/animator");
    expect(sourceRow(storyboard)).toBe("Yours · ~/.helm/plugins/storyboard");
    expect(recipesRow(animator.recipes!)).toBe(
      "1 · 1 skipped: this install has no workflow wan-2.5-image-to-video",
    );
    expect(recipesRow({ ok: 6, skipped: [] })).toBe("6");
    expect(whenLine("Planning a shot.")).toBe("Use when planning a shot.");
    expect(problemAlert(animator)).toBeNull();
    // API 1.64: a built-in in use whose copy of yours is broken says so
    expect(
      problemAlert({
        ...animator,
        problem: "Your copy is broken: it has no SKILL.md; using built in",
      }),
    ).toEqual({ text: "Your copy is broken: it has no SKILL.md; using built in.", red: false });
    expect(problemAlert(storyboard)).toEqual({
      text: "Storyboard artist is made for a newer helmstudio (format 2; this one reads 1). Update helmstudio to use it.",
      red: false,
    });
    expect(
      problemAlert({
        ...storyboard,
        status: "broken",
        source: "~/.helm/plugins/storyboard.zip",
        problem: "a file in it points outside its folder.",
      }),
    ).toEqual({
      text: "storyboard.zip can't be read: a file in it points outside its folder. Nothing from it is used.",
      red: true,
    });
  });
  it("says a new skill on its own line after a Rescan", () => {
    const cine = { ...storyboard, name: "cine", title: "Cinematographer" };
    const r = { plugins: [bridge, cine, animator], new: ["cine"] } as S<"PluginRescan">;
    expect(rescanLine(r)).toBe("New skill in ~/.helm/plugins: Cinematographer. It stays Off.");
    expect(newSkill(r)).toBe("cine");
    expect(rescanLine({ ...r, new: ["animator"] })).toBe(
      "New skill in plugins/: Animator. It stays Off.",
    );
    expect(rescanLine({ ...r, new: ["claude-code-bridge", "cine"] })).toBe(
      "1 new plugin: Claude Code bridge. It stays Off. New skill in ~/.helm/plugins: Cinematographer. It stays Off.",
    );
    expect(newSkill({ ...r, new: ["claude-code-bridge"] })).toBe("");
  });
});

describe("skills in the drawer", () => {
  it("puts the files a skill read in its Using skill line's box", () => {
    const use = line("1", "Using skill: Animator", { skill: "animator" });
    const read = line("2", "Read camera-moves.md · Animator");
    const other = line("3", "Read notes.md · Someone else");
    const failed = line("4", "Read x.md · Animator", { status: "failed" });
    const { used, rest } = skillLines([use, read, other, failed]);
    expect(used).toEqual([{ line: use, reads: ["camera-moves.md"] }]);
    expect(rest).toEqual([other, failed]);
    expect(skillLines([read]).rest).toEqual([read]); // no skill line before it: a plain line
  });
  it("shows the guide's first lines", () => {
    const text = `# Animator

A guide for **planning** shots,
wrapped here.

## 1. Plan

- One shot = one action.
  More of it.
- Write a \`shot list\`.
- Third.
- Fourth.`;
    expect(guideLines(text)).toEqual([
      { li: false, text: "A guide for planning shots, wrapped here." },
      { li: true, text: "One shot = one action. More of it." },
      { li: true, text: "Write a shot list." },
      { li: true, text: "Third." },
    ]);
  });
  it("lists the skills the @ picker matches", () => {
    const all = (skills as S<"SkillList">).skills;
    expect(skillsMatching(all, "anim").map((s) => s.name)).toEqual(["animator"]);
    expect(skillsMatching(all, "")).toHaveLength(1);
    expect(skillsMatching(all, "zzz")).toEqual([]);
  });
});
