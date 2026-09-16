// What every launcher screen shares: the element helper, the state vocabulary
// 03 §6 defines, dialogs, toasts and the live region.
//
// This is the daemon's own page, so unlike helm-ui-sdk it may know the API
// exists — but it still reaches it only through the generated client in
// launcher.js. Nothing here writes a path.

/** el builds an element with attributes, listeners and children in one call. */
export function el(tag, attrs, ...children) {
  const node = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs || {})) {
    if (v === undefined || v === null || v === false) continue;
    if (k === "text") node.textContent = String(v);
    else if (k === "html") node.innerHTML = String(v);
    else if (k.startsWith("on")) node.addEventListener(k.slice(2), v);
    else node.setAttribute(k, v === true ? "" : String(v));
  }
  for (const c of children.flat()) {
    if (c === undefined || c === null || c === false) continue;
    node.append(c);
  }
  return node;
}

export function bytes(n) {
  if (!Number.isFinite(n) || n <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let i = 0;
  let v = n;
  while (v >= 1000 && i < units.length - 1) {
    v /= 1000;
    i++;
  }
  return (i === 0 ? String(Math.round(v)) : v.toFixed(v < 10 ? 1 : 0)) + " " + units[i];
}

export function clock(seconds) {
  const s = Math.max(0, Math.floor(seconds || 0));
  return Math.floor(s / 60) + ":" + String(s % 60).padStart(2, "0");
}

export function since(when) {
  if (!when) return "";
  return clock((Date.now() - new Date(when).getTime()) / 1000);
}

/** ago is 03 §12's "2h ago", for a time a table shows rather than counts. */
export function ago(when) {
  if (!when) return "never";
  const s = Math.max(0, (Date.now() - new Date(when).getTime()) / 1000);
  if (s < 90) return "just now";
  if (s < 3600) return Math.round(s / 60) + "m ago";
  if (s < 86400) return Math.round(s / 3600) + "h ago";
  return Math.round(s / 86400) + "d ago";
}

/**
 * The card vocabulary (03 §6), amended 2026-09-16 by M6's defaults for
 * auth_required, a studio re-adopted without its manifest, and env_failed.
 *
 * Every row here is chip text, a status colour, a primary action and a
 * secondary one. A state with no row falls back to its own name in an idle
 * chip: a state with no chip is a bug, not a blank space, and a raw name is
 * at least true.
 */
export const STATUS = {
  idle: "helm-status-idle",
  running: "helm-status-running",
  info: "helm-status-info",
  warning: "helm-status-warning",
  error: "helm-status-error",
};

/**
 * state describes a studio as one row of that table. The process group wins
 * over the install state, because a running studio is running whatever its
 * checkout says.
 */
export function state(studio, job) {
  const group = studio.group || {};
  const install = studio.install_state || "";

  if (!studio.manifest_loaded) {
    return {
      chip: "Running · manifest not loaded", tone: "warning",
      primary: { label: "Stop", action: "stop" },
      note: "Still running from the last daemon. Its manifest is gone or invalid, so it can be stopped but not launched.",
    };
  }

  switch (group.state) {
    case "starting": {
      const p = (group.processes || []).find((x) => x.state === "starting") || {};
      const budget = p.health_timeout_s ? ` of ${clock(p.health_timeout_s)}` : "";
      return {
        chip: `Starting · ${since(p.started_at)}${budget}`, tone: "info",
        primary: { label: "View progress", action: "processes" },
        secondary: { label: "Cancel", action: "stop" },
      };
    }
    case "running": {
      const p = (group.processes || []).find((x) => x.role === "main") || (group.processes || [])[0] || {};
      return {
        chip: `Running · ${since(p.started_at)}`, tone: "running",
        primary: { label: "Open", action: "open" },
        secondary: { label: "Stop", action: "stop" },
      };
    }
    case "stopping":
      return { chip: "Stopping", tone: "info" };
    case "failed": {
      const f = group.failure || {};
      const code = f.exit_code !== undefined && f.exit_code !== null ? ` · exit ${f.exit_code}` : "";
      return {
        chip: `Crashed${code}`, tone: "error",
        primary: { label: "Restart", action: "launch" },
        secondary: { label: "View log", action: "processes" },
      };
    }
    default:
      break;
  }

  switch (install) {
    case "listed":
      return {
        chip: "Not installed", tone: "idle",
        primary: { label: "Install", action: "install" },
        secondary: { label: "Details", action: "detail" },
      };
    case "cloning":
    case "building":
      return {
        chip: "Installing" + steps(job), tone: "info",
        primary: { label: "View progress", action: "detail" },
        secondary: { label: "Cancel", action: "cancel" },
      };
    case "fetching_weights":
      return {
        chip: "Downloading" + percent(job), tone: "info",
        primary: { label: "View progress", action: "detail" },
        secondary: { label: "Cancel", action: "cancel" },
      };
    case "auth_required":
      return {
        chip: "Needs a Hugging Face token", tone: "warning",
        primary: { label: "Add token", action: "token" },
        secondary: { label: "Details", action: "detail" },
        note: "A weight this studio needs is on a gated repository. Add a token in Settings, then retry.",
      };
    case "ready":
      return {
        chip: "Installed", tone: "idle",
        primary: { label: "Launch", action: "launch" },
        secondary: { label: "Details", action: "detail" },
      };
    case "update_available":
      return {
        chip: "Update available", tone: "warning",
        primary: { label: "Launch", action: "launch" },
        secondary: { label: "Update", action: "install" },
      };
    default:
      break;
  }

  // failed_clone, failed_build, failed_weights, and env_failed, which M5 Q7
  // gave a code rather than a state and which reads as a build failure with
  // its own message.
  if (install.startsWith("failed_")) {
    const failure = studio.last_failure || {};
    const phase = failure.phase || install.slice("failed_".length);
    return {
      chip: `Install failed · ${phase}`, tone: "error",
      primary: { label: "Retry", action: "retry" },
      secondary: { label: "View log", action: "detail" },
      note: failure.message || "",
    };
  }

  // Not in the table (03 §6's own fallback). `cloned`, `built` and `removing`
  // land here: see the finding in this milestone's report.
  return {
    chip: install || "Unknown", tone: "idle",
    secondary: { label: "Details", action: "detail" },
  };
}

/** "· step 3 of 5", when a job says which step it is on. */
function steps(job) {
  const list = (job && job.steps) || [];
  if (!list.length) return "";
  const at = list.findIndex((s) => s.state === "running");
  const n = at >= 0 ? at + 1 : list.filter((s) => s.state === "succeeded").length + 1;
  return ` · step ${Math.min(n, list.length)} of ${list.length}`;
}

/** " 43%", when a job counts bytes rather than steps. */
function percent(job) {
  if (!job || !job.progress_den) return "";
  return ` ${Math.floor((job.progress_num / job.progress_den) * 100)}%`;
}

/** chip renders a state chip. Never colour alone: the dot always has a label. */
export function chip(text, tone) {
  return el("span", { class: `helm-chip ${STATUS[tone] || STATUS.idle}` },
    el("span", { class: "helm-dot" }), document.createTextNode(text));
}

/** The facts line under a card title: what the manifest declares (03 §6). */
export function facts(studio) {
  const out = [];
  const env = studio.runtime_env || {};
  if (env.engine) out.push(env.engine);
  if (env.backend) out.push(env.backend);
  if (studio.size_bytes) out.push(bytes(studio.size_bytes));
  if (studio.peak_ram_gb) out.push(`peak ~${studio.peak_ram_gb} GB`);
  else if (studio.heavy) out.push("peak undeclared");
  return out.join(" · ");
}

/** progress is a determinate bar; the number goes in the mono line beneath. */
export function progress(num, den) {
  const pct = den > 0 ? Math.max(0, Math.min(100, (num / den) * 100)) : 0;
  return el("div", {
    class: "helm-progress",
    role: "progressbar",
    "aria-valuemin": "0",
    "aria-valuemax": String(den || 0),
    "aria-valuenow": String(num || 0),
  }, el("div", { class: "helm-progress-fill", style: `--_helm-progress: ${pct}%` }));
}

export function indeterminate() {
  return el("div", { class: "helm-progress" }, el("div", { class: "helm-progress-indeterminate" }));
}

/**
 * dialog opens a modal and resolves to the action the user chose, or null.
 * It is a real <dialog>, so Escape, the backdrop and focus containment are
 * the platform's rather than ours.
 */
export function dialog({ title, body, actions, extra }) {
  return new Promise((resolve) => {
    let chosen = null;
    const buttons = actions.map((a) =>
      el("button", {
        class: "helm-btn " + (a.class || "helm-btn-secondary") + (a.primary ? " helm-btn-lg" : ""),
        text: a.label,
        onclick: () => { chosen = a.value; node.close(); },
      }));
    const node = el("dialog", { class: "helm-dialog", "aria-labelledby": "dlg-title" },
      el("h2", { class: "helm-dialog-title", id: "dlg-title", text: title }),
      el("div", { class: "helm-dialog-body" }, ...[].concat(body)),
      extra,
      el("div", { class: "helm-dialog-actions" }, ...buttons));
    node.addEventListener("close", () => {
      node.remove();
      resolve(chosen);
    });
    document.body.append(node);
    node.showModal();
    const first = buttons[buttons.length - 1];
    if (first) first.focus();
  });
}

let toastHost = null;

/**
 * toast says what happened. Failures are assertive, everything else polite
 * (03 §17).
 */
export function toast(text, tone) {
  if (!toastHost) {
    toastHost = el("div", { style: "position: fixed; right: var(--helm-space-4); bottom: var(--helm-space-4); display: flex; flex-direction: column; gap: var(--helm-space-2); z-index: 10" });
    document.body.append(toastHost);
  }
  const node = el("div", {
    class: "helm-toast",
    role: tone === "error" ? "alert" : "status",
    "aria-live": tone === "error" ? "assertive" : "polite",
    text,
  });
  toastHost.append(node);
  setTimeout(() => node.remove(), 6000);
}

/** The one polite live region for state changes (03 §17). */
let liveRegion = null;

export function announce(text) {
  if (!liveRegion) {
    liveRegion = el("p", { class: "helm-visually-hidden", role: "status", "aria-live": "polite" });
    document.body.append(liveRegion);
  }
  liveRegion.textContent = text;
}

/**
 * failure turns an error from the client into the sentence 03 §18 asks for:
 * name the thing that failed and the thing to do.
 */
export function failure(err, what) {
  const text = err && err.message ? String(err.message) : "";
  const i = text.indexOf("): ");
  const detail = i >= 0 ? text.slice(i + 3) : text;
  return detail ? `${what} ${detail}` : what;
}

/** section is a labelled block in a rail or a panel. */
export function section(label, ...children) {
  return el("div", { class: "helm-stack" }, el("p", { class: "helm-section-label", text: label }), ...children);
}

/** row is a left-hand label and a right-hand machine fact. */
export function row(label, value, tone) {
  return el("div", { class: "helm-row" },
    el("span", { class: "helm-body", text: label }),
    el("span", { class: "helm-spacer" }),
    el("span", { class: "helm-mono" + (tone ? " " + STATUS[tone] : ""), text: value }));
}

/**
 * The theme control: System, Light, Dark as a labelled three-state control,
 * never an unlabelled icon (03 §17). Every running studio follows it through
 * its theme stream (M6 Q8, Q9).
 */
export function themeControl(ctx) {
  const group = el("div", {
    class: "helm-segmented",
    role: "radiogroup",
    "aria-label": "Theme, for the launcher and every running studio",
  });
  for (const value of ["system", "light", "dark"]) {
    group.append(el("button", {
      class: "helm-segment",
      role: "radio",
      "aria-checked": String(ctx.store.theme === value),
      text: value[0].toUpperCase() + value.slice(1),
      onclick: () => ctx.setTheme(value),
    }));
  }
  return group;
}
