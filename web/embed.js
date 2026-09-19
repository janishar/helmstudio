// A studio's own page, inside helmstudio (docs/decisions.md 2026-09-18).
//
// The page is the studio's, served by the studio's process on its own port, in
// a frame. It is not proxied under helmstudio's origin, and that is the whole
// decision: a studio is someone else's code running with your permissions, and
// on helmstudio's origin it would share this page's storage and defeat the
// origin check that stops a page launching and installing things at you. A
// different port is a different origin, which is the isolation. What it costs
// is that this is an embed: no page here reads inside that frame, the address
// bar does not follow the studio's own navigation, and "Open in a tab" is one
// click away for when someone wants the window to themselves.
//
// Inside the frame nothing changes for the studio: it talks to its own
// `/helm/` proxy on its own origin, with its own token, its own SDK files and
// the same theme stream it has in a tab.
//
// The frame is kept across redraws (`ctx.keep`), because the poll runs every
// two seconds and a frame rebuilt on each one would reload the studio's page
// under whoever is using it — mid-generation, at worst.

import { chip, clock, el, elapsed, indeterminate, state, weightBytes, toast } from "./ui.js";
import { grid } from "./gallery.js";

/**
 * fullScreen hands the whole display to the studio's page. Escape gives it
 * back, which is the browser's own contract and not something to reimplement.
 *
 * The frame carries `allow="fullscreen"`, which is what lets the studio's own
 * page ask; this is helmstudio asking on its behalf, from a click.
 */
function fullScreen(frame) {
  const ask = frame.requestFullscreen || frame.webkitRequestFullscreen;
  if (!ask) {
    toast("This browser will not put the studio full screen.", "error");
    return;
  }
  Promise.resolve(ask.call(frame)).catch((err) => {
    toast(`The studio could not go full screen. ${err && err.message ? err.message : ""}`.trim(), "error");
  });
}

/** page is the address a running studio serves, or nothing. */
function page(studio) {
  const p = (((studio || {}).group || {}).processes || []).find((x) => x.state === "running" && x.ui && x.port);
  return p ? `http://127.0.0.1:${p.port}${p.ui}` : "";
}

/**
 * frameFor keeps one frame per studio and points it at the page only when the
 * address changes. Setting `src` to what it already is reloads it.
 */
function frameFor(ctx, studio, src) {
  const node = ctx.keep("embed:" + studio.id, () => el("iframe", {
    class: "helm-embed",
    // The studio's page is its own; these are what a page cannot take for
    // itself from inside a frame and a studio may legitimately want.
    allow: "fullscreen; clipboard-write",
    title: `${studio.name}, running on this Mac`,
  }));
  if (node.dataset.src !== src) {
    node.dataset.src = src;
    node.src = src;
  }
  return node;
}

/** starting is 03 §9's wait: elapsed against the budget, never a check count. */
function starting(studio) {
  const procs = ((studio.group || {}).processes || []);
  const p = procs.find((x) => x.state === "starting") || procs[0] || {};
  return el("div", { class: "helm-panel helm-embed-wait" },
    el("div", { class: "helm-panel-body helm-stack" },
      el("p", { class: "helm-body", text: `Starting ${studio.name}` }),
      el("p", { class: "helm-micro", text: "Loading the model into memory. The first start after an install usually takes about a minute." }),
      el("p", { class: "helm-mono" },
        p.port ? `:${p.port} · ` : "", elapsed(p.started_at),
        p.health_timeout_s ? ` of ${clock(p.health_timeout_s)}` : ""),
      indeterminate()));
}

/** stopped is what the screen says when there is nothing to show. */
function stopped(ctx, studio, s) {
  const running = ["starting", "running", "stopping"].includes((studio.group || {}).state);
  return el("div", { class: "helm-panel helm-embed-wait" },
    el("div", { class: "helm-panel-body helm-stack" },
      el("p", { class: "helm-body", text: running
        ? `${studio.name} is running, and declares no page to open.`
        : `${studio.name} is not running, so it is serving nothing to show here.` }),
      s.primary && !running
        ? el("div", { class: "helm-row" },
          el("button", {
            class: "helm-btn helm-btn-primary", type: "button", text: s.primary.label,
            onclick: () => ctx.act(studio, s.primary.action),
          }))
        : null));
}

/**
 * views is the switch between the studio's page and its gallery (03 §7a,
 * amended 2026-09-19). They are links rather than buttons, so a view is a
 * place: a reload, a Back and a bookmark all land where you were.
 */
function views(studio, current) {
  const base = `#/studios/${encodeURIComponent(studio.id)}/open`;
  const one = (id, label, href) => el("a", {
    class: "helm-segment", href,
    "aria-current": id === current ? "true" : undefined,
    text: label,
  });
  return el("div", { class: "helm-segmented", role: "group", "aria-label": "View" },
    one("page", "Studio page", base),
    one("gallery", "Gallery", base + "?view=gallery"));
}

export function studioPage(ctx, id) {
  const studio = (ctx.store.studios || []).find((s) => s.id === id);
  if (!studio) {
    return el("div", { class: "helm-panel" },
      el("div", { class: "helm-panel-body helm-stack" },
        el("h1", { class: "helm-title", text: "No such studio" }),
        el("p", { class: "helm-body", text: `Nothing in the library is called ${id}.` })));
  }
  const job = (ctx.store.jobs || {})[studio.id] || null;
  const s = state(studio, job, weightBytes(ctx.store.models, studio.id));
  const src = page(studio);
  const group = studio.group || {};
  const showing = ctx.query.get("view") === "gallery" ? "gallery" : "page";

  const frame = src ? frameFor(ctx, studio, src) : null;
  const header = el("div", { class: "helm-page-header helm-embed-header" },
    el("h1", { class: "helm-title", text: studio.name }),
    chip(s.chip, s.tone),
    views(studio, showing),
    el("span", { class: "helm-spacer" }),
    frame && showing === "page"
      ? el("button", {
        class: "helm-btn helm-btn-secondary", type: "button", text: "Full screen",
        title: "The studio takes the whole display. Escape comes back.",
        onclick: () => fullScreen(frame),
      })
      : null,
    src
      ? el("button", {
        class: "helm-btn helm-btn-secondary", type: "button", text: "Open in a tab",
        // The studio's own window, for when a frame is not what someone wants.
        onclick: () => window.open(src, "_blank", "noopener"),
      })
      : null,
    el("a", { class: "helm-btn helm-btn-secondary", href: `#/studios/${encodeURIComponent(studio.id)}/processes`, text: "Processes" }),
    ["starting", "running"].includes(group.state)
      ? el("button", { class: "helm-btn helm-btn-danger", type: "button", text: "Stop", onclick: () => ctx.act(studio, "stop") })
      : null);

  // The gallery is this studio's work, and it needs nothing running: a studio
  // that is stopped still has everything it has ever made. It is drawn only
  // once it is asked for, and reloads when it is asked for again, which costs
  // a query. The frame is the other way round: it is hidden rather than taken
  // off the page, because rebuilding it would reload the studio's page under
  // whoever is using it — the same reason the poll does not rebuild it.
  const gallery = showing === "gallery" ? grid(ctx, studio.id, studio.id) : null;
  if (frame) frame.hidden = showing !== "page";

  // The frame takes the whole window, which is what 03 §4 asks for: a studio's
  // page is the page. Without one there is nothing to fill it with — a
  // sentence and a Launch button stretched across a display read as a page
  // that had failed to load — so the waiting states take the reading width
  // every other page under Studios uses. The gallery fills it as the frame
  // does: it is a grid, not a column to read.
  const filled = showing === "gallery" || frame;
  const waiting = showing === "page" && !frame
    ? (group.state === "starting" ? starting(studio) : stopped(ctx, studio, s))
    : null;
  return el("div", { class: `helm-stack helm-embed-page${filled ? "" : " helm-embed-idle"}` },
    header, frame, gallery, waiting);
}
