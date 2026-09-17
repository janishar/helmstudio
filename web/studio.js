// Studio detail and install (03 §7): the three-column working layout.
//
// Left, what this studio is made of. Centre, the install as a checklist whose
// step glyphs are distinct shapes, so it survives greyscale, and whose failed
// row takes an error bar and expands in place — recovery buttons sit inside
// the failure, not in a toolbar elsewhere. Right, the output, which is
// <helm-terminal>: the launcher uses the component a studio uses, over the
// launcher client's own job logs (M6 Q12).
//
// Not drawn: 03 §7's Requirements block, which compares what the manifest
// requires with what this Mac has. Neither side is served — `Studio` carries
// no `requires`, and no endpoint reports the host's OS version, memory, free
// disk or which tools are present. See this milestone's report; inventing an
// endpoint here would be the divergence the rule against it exists to stop.

import { bytes, chip, dialog, el, failure, progress, row, section, state, toast } from "./ui.js";

const GLYPH = {
  pending: "pending", running: "running", succeeded: "done",
  failed: "failed", skipped: "skipped", cancelled: "skipped", interrupted: "failed",
};

/** One row of the install checklist. */
function step(ctx, studio, s, job) {
  const failed = s.state === "failed" || s.state === "interrupted";
  const node = el("li", { class: "helm-step", "data-state": s.state === "succeeded" ? "done" : s.state },
    el("span", { class: "helm-step-glyph", "data-state": GLYPH[s.state] || "pending", "aria-hidden": "true" }),
    el("span", { class: "helm-step-name", text: s.step_name }),
    el("span", { class: "helm-micro", text: stepTiming(s) }),
    el("code", { class: "helm-step-command", text: s.command || "" }));

  if (failed) {
    const err = (job && job.last_error) || {};
    node.append(el("div", { class: "helm-stack", style: "grid-column: 2 / 4; margin-top: var(--helm-space-2)" },
      el("p", { class: "helm-body", text: err.message || `${s.step_name} failed${s.exit_code != null ? ` with code ${s.exit_code}` : ""}.` }),
      el("div", { class: "helm-row" },
        el("button", {
          class: "helm-btn helm-btn-primary helm-btn-sm",
          text: "Retry from here",
          onclick: () => ctx.act(studio, "retry"),
        }))));
  }
  return node;
}

function stepTiming(s) {
  if (!s.started_at) return "";
  const end = s.finished_at ? new Date(s.finished_at).getTime() : Date.now();
  const secs = Math.max(0, (end - new Date(s.started_at).getTime()) / 1000);
  return secs < 60 ? `${secs.toFixed(1)}s` : `${Math.floor(secs / 60)}m ${Math.round(secs % 60)}s`;
}

/** The weights panel, from the artifacts bound to this studio. */
function weights(ctx, studio) {
  const mine = (ctx.store.models || []).filter((m) => (m.studios || []).includes(studio.id));
  if (!mine.length) return null;
  const rows = mine.map((m) => {
    const ready = m.state === "ready" || m.state === "linked";
    const done = m.total_bytes ? `${bytes(m.bytes_on_disk)} of ${bytes(m.total_bytes)}` : bytes(m.bytes_on_disk);
    return el("div", { class: "helm-stack", style: "gap: 2px" },
      row(m.hf_repo, ready ? bytes(m.total_bytes || m.bytes_on_disk) : done),
      el("div", { class: "helm-row" },
        chip(ready ? (m.source === "linked" ? "Linked" : "Ready") : m.state, ready ? "running" : "info"),
        el("span", { class: "helm-spacer" }),
        el("span", { class: "helm-micro", text: m.source === "linked" ? m.external_path || "" : "" })),
      !ready && m.total_bytes ? progress(m.bytes_on_disk, m.total_bytes) : null);
  });
  return section("Weights", ...rows);
}

/** The runtime panel: what the build resolved (02 §4's runtime_env). */
function runtime(studio) {
  const env = studio.runtime_env || {};
  const keys = Object.keys(env).filter((k) => typeof env[k] !== "object");
  if (!keys.length) return null;
  return section("Runtime", ...keys.map((k) => row(k, String(env[k]))));
}

export function studioDetail(ctx, id) {
  const studio = ctx.store.studios.find((s) => s.id === id);
  if (!studio) {
    return el("div", { class: "helm-panel" },
      el("div", { class: "helm-panel-body helm-stack" },
        el("h1", { class: "helm-title", text: "No such studio" }),
        el("p", { class: "helm-body", text: `Nothing in the library is called ${id}.` })));
  }
  const job = (ctx.store.jobs || {})[studio.id];
  const s = state(studio, job);
  const steps = (job && job.steps) || [];
  const done = steps.filter((x) => x.state === "succeeded").length;

  const header = el("div", { class: "helm-page-header" },
    el("h1", { class: "helm-title", text: studio.name }),
    chip(s.chip, s.tone),
    el("span", { class: "helm-spacer" }),
    (studio.group || {}).state === "running"
      ? el("button", { class: "helm-btn helm-btn-secondary", text: "Processes", onclick: () => ctx.act(studio, "processes") })
      : null,
    s.primary ? el("button", {
      class: "helm-btn helm-btn-primary",
      text: s.primary.label,
      onclick: () => ctx.act(studio, s.primary.action),
    }) : null,
    ["ready", "update_available"].includes(studio.install_state)
      ? el("button", { class: "helm-btn helm-btn-danger", text: "Uninstall", onclick: () => uninstall(ctx, studio) })
      : null);

  const identity = el("p", { class: "helm-mono" },
    document.createTextNode([studio.root_path || studio.root, studio.commit_sha ? studio.commit_sha.slice(0, 7) : null]
      .filter(Boolean).join(" · ")));

  const left = el("aside", { class: "helm-work-left helm-rail helm-stack" },
    section("Studio",
      row("id", studio.id),
      row("kinds", (studio.kinds || []).join(", ") || "—"),
      row("memory", studio.peak_ram_gb ? `peak ~${studio.peak_ram_gb} GB` : studio.heavy ? "undeclared" : "not heavy"),
      row("checkout", studio.root_present ? "present" : "absent"),
      studio.size_bytes ? row("on disk", bytes(studio.size_bytes)) : null,
      studio.rebuild_needed ? row("manifest", "changed since the build") : null),
    runtime(studio),
    weights(ctx, studio));

  const centre = el("section", { class: "helm-work-centre helm-panel" },
    el("div", { class: "helm-panel-header" },
      el("span", { class: "helm-section-label", text: "Install" }),
      el("span", { class: "helm-spacer" }),
      el("span", { class: "helm-micro", text: steps.length ? `step ${Math.min(done + 1, steps.length)} of ${steps.length}` : "" })),
    el("div", { class: "helm-panel-body helm-stack" },
      steps.length ? progress(done, steps.length) : null,
      steps.length
        ? el("ol", { class: "helm-steps" }, ...steps.map((x) => step(ctx, studio, x, job)))
        : el("p", { class: "helm-micro", text: studio.install_state === "listed" ? "Not installed yet." : "No install has run in this daemon." }),
      s.note && !steps.some((x) => x.state === "failed") ? el("p", { class: "helm-hint", text: s.note }) : null));

  const right = el("section", { class: "helm-work-right" }, terminalFor(ctx, studio, job));

  return el("div", { class: "helm-stack" }, header, identity,
    el("div", { class: "helm-work" }, left, centre, right));
}

/**
 * terminalFor gives <helm-terminal> a source rather than an id: install logs
 * are a launcher operation, so they arrive through the launcher client, and
 * the component still never names a path (M6 Q12).
 */
function terminalFor(ctx, studio, job) {
  if (!job) {
    return el("div", { class: "helm-panel" },
      el("div", { class: "helm-panel-header" }, el("span", { class: "helm-section-label", text: "Output" })),
      el("div", { class: "helm-panel-body" },
        el("p", { class: "helm-micro", text: "Output appears here while an install or a build runs." })));
  }
  const node = ctx.keep("job-log:" + job.id, () => {
    const t = document.createElement("helm-terminal");
    t.setAttribute("follow", "");
    return t;
  });
  node.source = { key: job.id, logs: (params) => ctx.client.jobs.logs(job.id, params) };
  return node;
}

async function uninstall(ctx, studio) {
  const chosen = await dialog({
    title: `Remove ${studio.name}?`,
    body: [
      el("p", { class: "helm-body", text: `The checkout helmstudio cloned is deleted. Weights stay in the cache, and anything ${studio.name} has written to the library stays where it is.` }),
    ],
    actions: [
      { label: "Cancel", value: null },
      { label: `Remove ${studio.name}`, value: "go", class: "helm-btn-danger-fill", primary: true },
    ],
  });
  if (chosen !== "go") return;
  try {
    await ctx.act(studio, "uninstall");
  } catch (err) {
    toast(failure(err, `${studio.name} could not be removed.`), "error");
  }
}
