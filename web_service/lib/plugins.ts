// Plugins page texts (docs/mock/Plugins.dc.html; contracts/README.md "Plugins", API 1.61; "Skills", API 1.64).
import type { S } from "@/lib/api";

type Plugin = S<"Plugin">;

export const KIND: Record<Plugin["kind"], string> = {
  "model-provider": "Model provider",
  "assistant-tools": "Assistant tools",
  skill: "Skill",
};

/** The address without the scheme: "host.docker.internal:8420" (the url as is when it doesn't parse). */
export function hostOf(url: string): string {
  try {
    return new URL(url).host;
  } catch {
    return url;
  }
}

/** "on the Mac" for host mac, else "on <host>". */
export const onHost = (host: string) => `on ${host === "mac" ? "the Mac" : host}`;

/** "10:42": an ISO time as local HH:MM ("" when missing or unparsable). */
export function hhmm(iso?: string) {
  const d = new Date(iso ?? "");
  if (Number.isNaN(d.getTime())) return "";
  return `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`;
}

/** The Status column: running → "Running" over its host; on but no answer → "Not running" over "enabled · start it";
 * a skill that is on → "On"; one it can't use → "Needs a newer helmstudio" / "Can't be read"; off → "Off". One that
 * starts itself (API 1.67, board STARTS ITSELF): "Starting…" / "Couldn't start" on its host; running adds "Starts on
 * its own · since 10:42"; not running says no runner on its host is up; off is "stopped on the Mac". */
export function statusOf(p: Plugin): {
  text: string;
  sub?: string;
  ok: boolean;
  tone?: "amber" | "red";
  note?: string;
} {
  const s = p.starts;
  if (p.status === "running") {
    const since = hhmm(s?.since);
    return {
      text: "Running",
      sub: hostOf(p.url ?? ""),
      ok: true,
      ...(s && { note: `Starts on its own${since ? ` · since ${since}` : ""}` }),
    };
  }
  if (p.status === "starting")
    return { text: "Starting…", sub: onHost(s?.host ?? "mac"), ok: false, tone: "amber" };
  if (p.status === "failed")
    return { text: "Couldn't start", sub: onHost(s?.host ?? "mac"), ok: false, tone: "red" };
  if (p.status === "not_running")
    return s
      ? { text: "Not running", sub: `no runner ${onHost(s.host)} is up`, ok: false }
      : { text: "Not running", sub: "enabled · start it", ok: false };
  if (p.status === "on") return { text: "On", ok: true };
  if (p.status === "needs_newer") return { text: "Needs a newer helmstudio", ok: false };
  if (p.status === "broken") return { text: "Can't be read", ok: false };
  return s
    ? { text: "Off", sub: `stopped ${onHost(s.host)}`, ok: false }
    : { text: "Off", ok: false };
}

/** A failed one's aside alert, as the board: "Couldn't start · it stopped 3 times: " then the reason (mono), then
 * "Trying again in 1 min." (the runner's backoff tops out at 60 s); null unless failed. */
export function failedAlert(p: Plugin): { lead: string; why: string } | null {
  if (p.status !== "failed") return null;
  const why = (p.problem ?? "").replace(/\.$/, "");
  const cut = why.indexOf(": ");
  return cut < 0
    ? { lead: `Couldn't start · ${why}`, why: "" }
    : { lead: `Couldn't start · ${why.slice(0, cut + 2)}`, why: why.slice(cut + 2) };
}

/** One this helmstudio can't use (a skill of a newer format, or yours that could not be read): faded, only Remove. */
export const unusable = (p: Plugin) => p.status === "needs_newer" || p.status === "broken";

const yours = (p: Plugin) => p.origin === "yours";

/** Where it is: its source, else its folder under plugins/ (built in) or ~/.helm/plugins/ (yours). */
export const sourceOf = (p: Plugin) =>
  p.source ?? (yours(p) ? `~/.helm/plugins/${p.name}` : `plugins/${p.name}`);

/** The row's line under the title: "0.1.0 · built in · plugins/animator" for a skill, else version · author · source. */
export const subline = (p: Plugin) =>
  [p.version, p.kind === "skill" ? (yours(p) ? "yours" : "built in") : p.author, sourceOf(p)].join(
    " · ",
  );

/** A skill's aside Source row: "Built in · plugins/animator" / "Yours · ~/.helm/plugins/x.zip". */
export const sourceRow = (p: Plugin) => `${yours(p) ? "Yours" : "Built in"} · ${sourceOf(p)}`;

/** A skill's aside Recipes row: "2" / "2 · 1 skipped: <why>" / "0 · 6 skipped: <first why>, and 5 more" (one short
 * line, as the board's). */
export function recipesRow(r: NonNullable<Plugin["recipes"]>) {
  if (!r.skipped.length) return String(r.ok);
  const more = r.skipped.length > 1 ? `, and ${r.skipped.length - 1} more` : "";
  return `${r.ok} · ${r.skipped.length} skipped: ${r.skipped[0].why}${more}`;
}

/** A skill's aside line from its `when`: "Use when planning …". */
export const whenLine = (when: string) =>
  `Use when ${when.charAt(0).toLowerCase()}${when.slice(1)}`;

const bare = (s: string) => s.replace(/\.$/, "");

/** The May do cell of one it can't use: its problem as a sentence. */
export function problemCell(p: Plugin) {
  const s = bare(p.problem ?? "");
  return s.charAt(0).toUpperCase() + s.slice(1);
}

/** The aside's alert for one it can't use (amber: needs a newer helmstudio; red: can't be read), else null. */
export function problemAlert(p: Plugin): { text: string; red: boolean } | null {
  const why = bare(p.problem ?? "");
  if (p.status === "needs_newer")
    return { text: `${p.title} is ${why}. Update helmstudio to use it.`, red: false };
  if (p.status === "broken")
    return {
      text: `${sourceOf(p).split("/").pop()} can't be read: ${why}. Nothing from it is used.`,
      red: true,
    };
  if (p.status === "failed") return null; // failedAlert says it
  // API 1.64: a built-in in use whose copy of yours can't be: "Your copy is broken: …; using built in."
  if (why) return { text: `${why}.`, red: false };
  return null;
}

/** The provider's default title: the plugin's (the API's own default; owner, 2026-11-25: no "(personal)"). */
export const providerTitle = (p: Plugin) => p.title;

/** After Rescan: "1 new plugin: Web search. It stays Off." / "2 new plugins: A, B. They stay Off." / "No new plugins.";
 * skills on their own line: "New skill in ~/.helm/plugins: Cinematographer. It stays Off." (in plugins/ when built
 * in). */
export function rescanLine(r: S<"PluginRescan">): string {
  const found = r.new.map((n) => r.plugins.find((p) => p.name === n) ?? n);
  if (!found.length) return "No new plugins.";
  const title = (p: Plugin | string) => (typeof p === "string" ? p : p.title);
  const off = (n: number) => `${n === 1 ? "It stays" : "They stay"} Off.`;
  const skills = found.filter((p): p is Plugin => typeof p !== "string" && p.kind === "skill");
  const rest = found.filter((p) => typeof p === "string" || p.kind !== "skill");
  const out: string[] = [];
  if (rest.length)
    out.push(
      `${rest.length} new plugin${rest.length === 1 ? "" : "s"}: ${rest.map(title).join(", ")}. ${off(rest.length)}`,
    );
  if (skills.length) {
    const where = skills.every(yours)
      ? " in ~/.helm/plugins"
      : skills.some(yours)
        ? ""
        : " in plugins/";
    out.push(
      `New skill${skills.length === 1 ? "" : "s"}${where}: ${skills.map(title).join(", ")}. ${off(skills.length)}`,
    );
  }
  return out.join(" ");
}

/** The first new skill a Rescan found (its note's Show selects it), else "". */
export const newSkill = (r: S<"PluginRescan">) =>
  r.new.find((n) => r.plugins.find((p) => p.name === n)?.kind === "skill") ?? "";

/** The Add-as-provider refusal that empties the key field (red); any other error shows as is (amber, Try again). */
export const KEY_REFUSED = "The key doesn't match";
