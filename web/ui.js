// What every launcher screen shares: the element helper, the state vocabulary
// 03 §6 defines, dialogs, toasts and the live region.
//
// This is the daemon's own page, so unlike helm-ui-sdk it may know the API
// exists — but it still reaches it only through the generated client in
// launcher.js. Nothing here writes a path.

import { listen } from "./morph.js";

/**
 * el builds an element with attributes, listeners and children in one call.
 *
 * A listener is kept on the node rather than bound to it, so that a redraw
 * which keeps this element hands it the new screen's listener (morph.js). A
 * listener must not reach for an element its own render built: that one may
 * never be on the page. It reads `event.currentTarget`, state, or an id.
 */
export function el(tag, attrs, ...children) {
  const node = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs || {})) {
    if (v === undefined || v === null || v === false) continue;
    if (k === "text") node.textContent = String(v);
    else if (k === "html") node.innerHTML = String(v);
    else if (k.startsWith("on")) listen(node, k.slice(2), v);
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

/**
 * elapsed is a time since `when` that counts in the page between polls
 * (03 §17, amended 2026-09-17). tick() moves every one of them; a redraw
 * draws the same digits, so the two never disagree for more than a second.
 */
export function elapsed(when) {
  return el("span", { class: "helm-elapsed", "data-since": when || "", text: since(when) });
}

/** tick advances every elapsed time under root. It swaps digits; nothing animates. */
export function tick(root) {
  for (const node of root.querySelectorAll("[data-since]")) {
    const text = since(node.dataset.since);
    if (node.textContent !== text) node.textContent = text;
  }
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

  // An entry whose manifest does not validate is listed as invalid (R2 as
  // amended, M7 Q6) — not running, and with nothing to stop. This is checked
  // before `manifest_loaded`, which is false for both of them: a studio the
  // daemon is still running without a manifest, and a library entry whose
  // manifest never loaded in the first place.
  if (studio.manifest_valid === false) {
    return { chip: "Manifest invalid", tone: "error" };
  }

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
        chip: ["Starting · ", elapsed(p.started_at), budget], tone: "info",
        primary: { label: "View progress", action: "processes", quiet: true },
        secondary: { label: "Cancel", action: "stop" },
      };
    }
    case "running": {
      const p = (group.processes || []).find((x) => x.role === "main") || (group.processes || [])[0] || {};
      return {
        chip: ["Running · ", elapsed(p.started_at)], tone: "running",
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
        primary: { label: "View progress", action: "detail", quiet: true },
        secondary: { label: "Cancel", action: "cancel" },
      };
    case "fetching_weights":
      return {
        chip: "Downloading" + percent(job), tone: "info",
        primary: { label: "View progress", action: "detail", quiet: true },
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

/**
 * chip renders a state chip. Never colour alone: the dot always has a label.
 * The label is text, or a list of text and nodes — an elapsed time, say.
 */
export function chip(text, tone) {
  const parts = [].concat(text).filter((t) => t !== undefined && t !== null && t !== "");
  return el("span", { class: `helm-chip ${STATUS[tone] || STATUS.idle}` },
    el("span", { class: "helm-dot" }),
    ...parts.map((t) => (typeof t === "string" ? document.createTextNode(t) : t)));
}

/** repoName is a repository as a person reads it: no scheme, no `.git`. */
export function repoName(url) {
  return String(url || "").replace(/^[a-z+]+:\/\//, "").replace(/^git@([^:]+):/, "$1/").replace(/\.git$/, "");
}

/** shortRef is a commit as git abbreviates one, and any other ref as it is. */
export function shortRef(ref) {
  return /^[0-9a-f]{40}$/.test(ref) ? ref.slice(0, 7) : ref;
}

/**
 * The facts line under a studio's title (03 §6, amended 2026-09-17): what it
 * needs, where its code comes from and where its weights are. Where things
 * come from is a statement here and never a switch — that choice belongs to
 * the install screen.
 */
export function facts(studio, models) {
  const out = [];
  if (studio.peak_ram_gb) out.push(`peak ~${studio.peak_ram_gb} GB`);
  else if (studio.heavy) out.push("peak undeclared");
  if (studio.local_path) out.push(`builds the folder ${studio.local_path}`);
  else if (studio.repo) out.push(`clones ${repoName(studio.repo)}${studio.ref ? " at " + shortRef(studio.ref) : ""}`);
  const mine = (models || []).filter((m) => (m.studios || []).includes(studio.id));
  const linked = mine.filter((m) => m.source === "linked").length;
  const downloaded = mine.length - linked;
  const weights = [linked ? `${linked} linked` : null, downloaded ? `${downloaded} downloaded` : null].filter(Boolean);
  if (weights.length) out.push(`weights ${weights.join(", ")}`);
  if (studio.size_bytes) out.push(`${bytes(studio.size_bytes)} on disk`);
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
 * menu is a ⋯ button and the menu it opens (03 §6 and §17, amended
 * 2026-09-17): the actions a row offers that are done rarely.
 *
 * It is a popover, so it sits above the row rather than being clipped by it,
 * light dismiss and Escape close it, and closing it returns focus to the
 * button. Arrow keys, Home and End move through it, and Tab leaves it.
 *
 * Whether it is open lives here rather than in the row, so a redraw while it
 * is open draws it open, where it was.
 */
const openMenus = new Map();

export function menu(id, label, items) {
  const actions = items.filter(Boolean);
  if (!actions.length) return [];
  const at = openMenus.get(id);
  const invoker = () => document.querySelector(`[popovertarget="${CSS.escape(id)}"]`);
  const button = el("button", {
    class: "helm-btn helm-btn-ghost helm-btn-icon", type: "button", "data-key": "more",
    popovertarget: id, "aria-haspopup": "menu", "aria-expanded": String(at !== undefined), "aria-label": label,
  }, el("span", { class: "helm-dots", "aria-hidden": "true" }));
  const list = el("div", {
    id, class: "helm-menu", popover: "auto", role: "menu", "aria-label": label, "data-key": "menu", style: at,
    onbeforetoggle: (e) => {
      const from = invoker();
      if (e.newState !== "open") {
        openMenus.delete(id);
        if (from) from.setAttribute("aria-expanded", "false");
        return;
      }
      if (!from) return;
      // Below the button, its right edge on the button's. In the top layer an
      // absolute box is placed against the document, so it scrolls with it.
      const r = from.getBoundingClientRect();
      const style = `top: ${Math.round(r.bottom + window.scrollY + 4)}px; right: ${Math.round(document.documentElement.clientWidth - r.right - window.scrollX)}px`;
      e.currentTarget.setAttribute("style", style);
      openMenus.set(id, style);
      from.setAttribute("aria-expanded", "true");
    },
    ontoggle: (e) => {
      if (e.newState !== "open") return;
      const first = e.currentTarget.querySelector('[role="menuitem"]');
      if (first) first.focus();
    },
    onkeydown: (e) => {
      const list = e.currentTarget;
      const items = [...list.querySelectorAll('[role="menuitem"]')];
      const i = items.indexOf(document.activeElement);
      const to = {
        ArrowDown: items[(i + 1) % items.length],
        ArrowUp: items[(i - 1 + items.length) % items.length],
        Home: items[0],
        End: items[items.length - 1],
      }[e.key];
      // Escape is the platform's to handle for a popover, and not every
      // browser hosting this page handles it; closing here makes it certain.
      // Focus goes back to the button either way, so Tab moves on from there.
      if (e.key === "Escape" || e.key === "Tab") {
        if (e.key === "Escape") e.preventDefault();
        if (list.matches(":popover-open")) list.hidePopover();
        const from = invoker();
        if (from) from.focus();
        return;
      }
      if (to) {
        e.preventDefault();
        to.focus();
      }
    },
  }, ...actions.map((a) => el("button", {
    class: "helm-menu-item", type: "button", role: "menuitem", tabindex: "-1", text: a.label,
    onclick: (e) => {
      const list = e.currentTarget.closest("[popover]");
      if (list.matches(":popover-open")) list.hidePopover();
      a.run();
    },
  })));
  return [button, list];
}

/** saveText hands the person a file, as a download. */
export function saveText(name, text, type = "text/yaml") {
  const url = URL.createObjectURL(new Blob([text], { type }));
  const a = el("a", { href: url, download: name });
  document.body.append(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 10000);
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

/**
 * copyButton copies a value, and says so. Revealing a path in Finder needs
 * the Mac app (03 §14), so the browser copies it.
 */
export function copyButton(value, label, what) {
  return el("button", {
    class: "helm-btn helm-btn-ghost helm-btn-sm", type: "button", text: "Copy", "aria-label": label,
    onclick: async () => {
      try {
        await navigator.clipboard.writeText(value);
        toast(`${what} copied.`, "info");
      } catch {
        toast(`${what} could not be copied. Select it and copy it yourself.`, "error");
      }
    },
  });
}

/** section is a labelled block in a rail or a panel. */
export function section(label, ...children) {
  return el("div", { class: "helm-stack" }, el("p", { class: "helm-section-label", text: label }), ...children);
}

/**
 * row is a left-hand label and a right-hand machine fact.
 *
 * Two columns rather than a flex row with a spacer: a value too long for the
 * line — a path, a toolchain version — then wraps inside its own column
 * instead of dropping under the label and running the width of the rail.
 */
export function row(label, value, tone) {
  return el("div", { class: "helm-fact" },
    el("span", { class: "helm-body helm-fact-label", text: label }),
    el("span", { class: "helm-mono helm-fact-value" + (tone ? " " + STATUS[tone] : ""), text: value }));
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
