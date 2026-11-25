import { describe, expect, it } from "vitest";
import list from "../../contracts/fixtures/http/PluginList.valid.json";
import rescan from "../../contracts/fixtures/http/PluginRescan.valid.json";
import type { S } from "@/lib/api";
import {
  failedAlert,
  onHost,
  problemAlert,
  providerTitle,
  rescanLine,
  statusOf,
} from "@/lib/plugins";

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
    expect(statusOf({ ...bridge, status: "off" })).toEqual({ text: "Off", ok: false });
  });
  it("says how one that starts itself is doing (board STARTS ITSELF)", () => {
    expect(statusOf({ ...search, status: "starting" })).toEqual({
      text: "Starting…",
      sub: "on the Mac",
      ok: false,
      tone: "amber",
    });
    const since = new Date(2026, 10, 25, 10, 42).toISOString();
    expect(
      statusOf({
        ...search,
        status: "running",
        url: "http://host.docker.internal:8421",
        starts: { host: "mac", since },
      }),
    ).toEqual({
      text: "Running",
      sub: "host.docker.internal:8421",
      ok: true,
      note: "Starts on its own · since 10:42",
    });
    expect(statusOf(search)).toEqual({
      text: "Couldn't start",
      sub: "on the Mac",
      ok: false,
      tone: "red",
    });
    expect(statusOf({ ...search, status: "not_running", starts: { host: "gpu1" } })).toEqual({
      text: "Not running",
      sub: "no runner on gpu1 is up",
      ok: false,
    });
    expect(statusOf({ ...search, status: "off", enabled: false })).toEqual({
      text: "Off",
      sub: "stopped on the Mac",
      ok: false,
    });
    expect(onHost("mac")).toBe("on the Mac");
    expect(failedAlert(search)).toEqual({
      lead: "Couldn't start · it stopped 3 times: ",
      why: "ModuleNotFoundError: ddgs",
    });
    expect(failedAlert(bridge)).toBeNull();
    expect(problemAlert(search)).toBeNull();
  });
  it("names a plugin's provider after the plugin, never (personal)", () => {
    expect(providerTitle(bridge)).toBe("Your Claude subscription");
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
