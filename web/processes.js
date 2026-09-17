// Process group (03 §8), and the starting screen (03 §9).
//
// One group, one row per declared process, in dependency order. The heavy
// member carries the memory badge, because that is the process the
// one-at-a-time rule watches. Stop walks the list in reverse.
//
// While the group is starting this screen is 03 §9's: elapsed against the
// budget, never a check count, and an indeterminate bar. If the budget runs
// out it becomes "didn't come up" with the last lines the daemon kept.

import { chip, clock, el, elapsed, indeterminate } from "./ui.js";

/** The health column: the probe and how long it has had, counting. */
function health(p) {
  if (!p.health_state || p.health_state === "") return ["—"];
  if (p.state === "starting" && p.health_timeout_s) {
    return [`${p.health_state} · `, elapsed(p.started_at), ` of ${clock(p.health_timeout_s)}`];
  }
  return [p.health_state];
}

function processState(p) {
  switch (p.state) {
    case "running":
      return { text: ["Running · ", elapsed(p.started_at)], tone: "running" };
    case "starting":
      return { text: ["Starting · ", elapsed(p.started_at)], tone: "info" };
    case "stopping":
      return { text: "Stopping", tone: "info" };
    case "failed": {
      const code = p.exit_code !== undefined && p.exit_code !== null ? ` · exit ${p.exit_code}` : "";
      return { text: `${p.exit_reason || "Failed"}${code}`, tone: "error" };
    }
    case "exited":
      return { text: p.exit_reason === "clean" ? "Stopped" : `Exited · ${p.exit_reason || ""}`, tone: "idle" };
    case "queued":
      return { text: "Queued", tone: "idle" };
    default:
      // The fallback 03 §6 asks for: a raw name in an idle chip, never blank.
      return { text: p.state || "Unknown", tone: "idle" };
  }
}

export function processGroup(ctx, id) {
  const studio = ctx.store.studios.find((s) => s.id === id);
  if (!studio) {
    return el("div", { class: "helm-panel" },
      el("div", { class: "helm-panel-body helm-stack" },
        el("h1", { class: "helm-title", text: "No such studio" }),
        el("p", { class: "helm-body", text: `Nothing in the library is called ${id}.` })));
  }
  const group = studio.group || {};
  const procs = group.processes || [];
  const live = procs.filter((p) => p.state === "running").length;
  const starting = group.state === "starting";
  const selected = ctx.query.get("log") || "";
  // A page to open: a running process that declares one, with a port.
  const open = procs.some((p) => p.state === "running" && p.ui && p.port);

  const header = el("div", { class: "helm-page-header" },
    el("h1", { class: "helm-title", text: studio.name }),
    chip(groupChip(group), groupTone(group)),
    el("span", { class: "helm-meta", text: group.group_run_id ? `group run ${group.group_run_id}` : "" }),
    el("span", { class: "helm-spacer" }),
    el("a", { class: "helm-btn helm-btn-secondary", href: `#/studios/${studio.id}`, text: "Details" }),
    ["starting", "running"].includes(group.state)
      ? el("button", { class: "helm-btn helm-btn-danger", text: "Stop group", onclick: () => ctx.act(studio, "stop") })
      : el("button", { class: "helm-btn helm-btn-primary", text: "Launch", onclick: () => ctx.act(studio, "launch") }),
    // The page the studio serves, which is the reason to be on this screen at
    // all. It is the screen's one accent while something is up to open.
    open ? el("button", { class: "helm-btn helm-btn-primary", text: "Open", onclick: () => ctx.act(studio, "open") }) : null);

  const blocks = [header];

  if (starting) {
    const main = procs.find((p) => p.state === "starting") || procs[0] || {};
    blocks.push(el("div", { class: "helm-panel" },
      el("div", { class: "helm-panel-body helm-stack" },
        el("p", { class: "helm-body", text: `Starting ${studio.name}` }),
        el("p", { class: "helm-micro", text: "Loading the model into memory. The first start after an install usually takes about a minute." }),
        el("p", { class: "helm-mono" },
          main.port ? `:${main.port} · ` : "", elapsed(main.started_at),
          main.health_timeout_s ? ` of ${clock(main.health_timeout_s)}` : ""),
        indeterminate())));
  }

  if (group.failure) {
    const f = group.failure;
    blocks.push(el("div", { class: "helm-panel" },
      el("div", { class: "helm-panel-header" },
        el("span", { class: "helm-section-label", text: `${studio.name} didn't come up` })),
      el("div", { class: "helm-panel-body helm-stack" },
        el("p", { class: "helm-body", text: `${f.process} ${f.exit_reason}${f.exit_code != null ? ` with code ${f.exit_code}` : ""}.` }),
        el("pre", { class: "helm-terminal", style: "min-height: 0", text: (f.last_lines || []).join("\n") }))));
  }

  const table = el("table", { class: "helm-table" },
    el("thead", {}, el("tr", {},
      ...["Process", "Role", "Port", "Health", "State", ""].map((h) => el("th", { text: h })))),
    el("tbody", {}, ...procs.map((p) => {
      const st = processState(p);
      return el("tr", { "data-key": p.spec_name },
        el("td", {}, el("span", { class: "helm-body", text: p.spec_name })),
        el("td", {}, el("span", { class: "helm-micro", text: [p.role, p.heavy ? "heavy" : null].filter(Boolean).join(" · ") })),
        el("td", {}, el("span", { class: "helm-mono", text: p.port ? ":" + p.port : "—" })),
        el("td", {}, el("span", { class: "helm-mono" }, ...health(p))),
        el("td", {}, chip(st.text, st.tone)),
        el("td", {}, el("button", {
          class: "helm-btn helm-btn-ghost helm-btn-sm",
          text: "Log",
          "aria-pressed": String(selected === p.spec_name),
          onclick: () => ctx.go(`#/studios/${studio.id}/processes?log=${encodeURIComponent(p.spec_name)}`),
        })));
    })));

  blocks.push(el("div", { class: "helm-panel" },
    el("div", { class: "helm-panel-header" },
      el("span", { class: "helm-section-label", text: "Processes" }),
      el("span", { class: "helm-spacer" }),
      el("span", { class: "helm-micro", text: `${procs.length} declared · ${live} running` })),
    el("div", { class: "helm-panel-body" }, table)));

  if (selected) blocks.push(processLog(ctx, studio, selected));

  return el("div", { class: "helm-stack" }, ...blocks);
}

function groupChip(group) {
  switch (group.state) {
    case "starting": return "Group starting";
    case "running": return "Group running";
    case "stopping": return "Group stopping";
    case "failed": return "Group failed";
    case "exited": return "Stopped";
    default: return "Not launched";
  }
}

function groupTone(group) {
  switch (group.state) {
    case "running": return "running";
    case "starting": case "stopping": return "info";
    case "failed": return "error";
    default: return "idle";
  }
}

/**
 * One process's output, through the same <helm-terminal> a studio uses. The
 * component is handed a source rather than an id, because a process log takes
 * a studio and a process name (M6 Q12).
 */
function processLog(ctx, studio, process) {
  const key = `proc-log:${studio.id}:${process}`;
  const node = ctx.keep(key, () => {
    const t = document.createElement("helm-terminal");
    t.setAttribute("follow", "");
    return t;
  });
  node.source = { key, logs: (params) => ctx.client.studios.processLog(studio.id, process, params) };
  return el("div", { class: "helm-stack", style: "min-height: 320px" },
    el("p", { class: "helm-section-label", text: `${studio.name} · ${process}` }),
    node);
}
