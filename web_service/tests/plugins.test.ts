import { describe, expect, it } from "vitest";
import list from "../../contracts/fixtures/http/PluginList.valid.json";
import rescan from "../../contracts/fixtures/http/PluginRescan.valid.json";
import type { S } from "@/lib/api";
import { providerTitle, rescanLine, statusOf } from "@/lib/plugins";

const [bridge, search] = (list as S<"PluginList">).plugins;

describe("plugins", () => {
  it("says each status as the board does", () => {
    expect(statusOf(bridge)).toEqual({
      text: "Running",
      sub: "host.docker.internal:8420",
      ok: true,
    });
    expect(statusOf({ ...bridge, status: "not_running" })).toEqual({
      text: "Not running",
      sub: "enabled · start it",
      ok: false,
    });
    expect(statusOf(search)).toEqual({ text: "Off", ok: false });
  });
  it("names a plugin's provider after the plugin, never (personal)", () => {
    expect(providerTitle(bridge)).toBe("Claude Code bridge");
    expect(providerTitle(search)).toBe("Web search");
  });
  it("says what a Rescan found", () => {
    const r = rescan as S<"PluginRescan">;
    expect(rescanLine(r)).toBe("1 new plugin: Web search. It stays Off.");
    expect(rescanLine({ ...r, new: [] })).toBe("No new plugins.");
    expect(rescanLine({ ...r, new: ["web-search", "x"] })).toBe(
      "2 new plugins: Web search, x. They stay Off.",
    );
  });
});
