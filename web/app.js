// The launcher: the shell, the router and the one poll that keeps every screen
// current (docs/decisions.md M6 Q2).
//
// A screen is a function of a context and its route arguments, returning a
// node. It owns no timer and no state: the poll replaces the studio list and
// the current screen is drawn again from it. That is what keeps four rows
// ticking through an install while the title and stripe never move (03 §6) —
// and it is what lets a fixture page draw any screen from canned data by
// handing it a context whose client is a fake.
//
// A drawing is applied to the page in place (morph.js), so what someone has
// focused, opened or scrolled survives the poll (the launcher redesign,
// docs/decisions.md 2026-09-17).
//
// Not here, by their own milestones: Gallery and Timeline, which read every
// studio's items and bytes and wait for M9's cookie (M6 Q11, M8 Q20); Doctor,
// which belongs to no milestone yet and is hidden rather than shown empty.

import { connect } from "./launcher.js";
import { kept as keptNode, morphChildren } from "./morph.js";
import { announce, chip, el, elapsed, failure, themeControl, tick, toast } from "./ui.js";
import { catalogue } from "./catalogue.js";
import { studioDetail } from "./studio.js";
import { processGroup } from "./processes.js";
import { modelsAndDisk } from "./models.js";
import { settings } from "./settings.js";
import { launch, stop } from "./switch.js";
import { approvalScreen, guard } from "./approval.js";
import { addStudio } from "./add.js";
import { studioPage } from "./embed.js";
import { galleryScreen } from "./gallery.js";
import { editor } from "./editor.js";

/** How often the list is refreshed: cheap enough to leave running, short
 *  enough that a state change is seen before it is wondered about. */
const POLL_MS = 2000;

/**
 * The routes. `width` is 03 §4's (amended 2026-09-17): a page that is read is
 * a 960px column, a workspace is up to 1440px. `back` opens the page with a
 * link to Studios, which every page under Studios has.
 */
const ROUTES = [
  { path: /^\/studios$/, screen: catalogue, nav: "studios", width: "reading" },
  { path: /^\/studios\/([^/]+)\/approve$/, screen: approvalScreen, nav: "studios", width: "reading", back: true },
  // The studio's own page, in a frame: its own origin, inside helmstudio.
  { path: /^\/studios\/([^/]+)\/open$/, screen: studioPage, nav: "studios", width: "full", back: true },
  { path: /^\/studios\/([^/]+)\/processes$/, screen: processGroup, nav: "studios", width: "workspace", back: true },
  // Reading width, not workspace (03 §4, amended 2026-09-19): with the output
  // under the install rather than beside it, the page is a column to read down.
  { path: /^\/studios\/([^/]+)$/, screen: studioDetail, nav: "studios", width: "reading", back: true },
  // Adding and editing are their own paths rather than /studios/new, because
  // `new` is a legal studio id and a route that shadowed one would be a bug
  // nobody found until somebody wrote it.
  { path: /^\/add$/, screen: addStudio, nav: "studios", width: "reading", back: true },
  { path: /^\/edit$/, screen: editor, nav: "studios", width: "workspace", back: true },
  { path: /^\/edit\/([^/]+)$/, screen: editor, nav: "studios", width: "workspace", back: true },
  // A grid of every studio's work, which is a workspace rather than a column
  // to read (03 §4, §10).
  { path: /^\/gallery$/, screen: galleryScreen, nav: "gallery", width: "grid" },
  { path: /^\/models$/, screen: modelsAndDisk, nav: "models", width: "reading" },
  { path: /^\/settings$/, screen: settings, nav: "settings", width: "reading" },
];

const NAV = [
  { id: "studios", label: "Studios", href: "#/studios" },
  { id: "gallery", label: "Gallery", href: "#/gallery" },
  { id: "models", label: "Models & disk", href: "#/models" },
  { id: "settings", label: "Settings", href: "#/settings" },
];

/** newStore is the whole of the launcher's state: what the daemon last said. */
export function newStore() {
  return {
    studios: [],
    models: [],
    jobs: {},
    error: null,
    // loaded is whether the daemon has answered at all. Before it has, a list
    // is not empty; it is not known yet, and is drawn as placeholders.
    loaded: false,
    theme: "system",
    get running() {
      return this.studios.filter((s) => ["starting", "running", "stopping"].includes((s.group || {}).state));
    },
  };
}

/**
 * newContext is what every screen is given. Everything a screen can do to the
 * daemon goes through it, which is what makes a screen drawable from a fixture.
 */
export function newContext({ client, store, redraw }) {
  const kept = new Map();
  const ctx = {
    client,
    store,
    redraw,
    query: new URLSearchParams(),
    /**
     * keep returns the same node across redraws. A <helm-terminal> rebuilt
     * every poll would restart its stream twice a minute; the same instance
     * moved into the new tree keeps its buffer and resumes. A kept node is
     * marked, so a redraw puts it in place as itself (morph.js). A screen
     * keeps its state objects here too.
     */
    keep(key, make) {
      if (!kept.has(key)) {
        const made = make();
        kept.set(key, made instanceof Node ? keptNode(made) : made);
      }
      return kept.get(key);
    },
    go(hash) {
      location.hash = hash;
    },
    async refresh() {
      try {
        const items = await all((params) => client.studios.list(params));
        announceChanges(store.studios, items);
        store.studios = items;
        store.models = await all((params) => client.models.list(params));
        // Only a studio with work in flight has a job to read, which is
        // almost always none of them and never more than a few.
        const jobs = {};
        for (const s of items) {
          if (!s.job_id) continue;
          try {
            jobs[s.id] = await client.jobs.get(s.job_id);
          } catch {
            // A job the daemon has forgotten leaves the screen without its
            // steps, which is better than the screen without its studio.
          }
        }
        store.jobs = jobs;
        store.error = null;
        store.loaded = true;
      } catch (err) {
        store.error = failure(err, "The daemon could not be reached.");
      }
      redraw();
    },
    async setTheme(value) {
      const before = store.theme;
      store.theme = value;
      applyTheme(value);
      redraw();
      try {
        await client.settings.setTheme({ theme: value });
      } catch (err) {
        store.theme = before;
        applyTheme(before);
        redraw();
        toast(failure(err, "The theme could not be set."), "error");
      }
    },
    act: (studio, action) => act(ctx, studio, action),
  };
  return ctx;
}

/** all reads every page of a collection: {items, next_cursor} throughout. */
async function all(fetchPage) {
  const items = [];
  let cursor = null;
  do {
    const page = await fetchPage({ limit: 200, cursor });
    items.push(...(page.items || []));
    cursor = page.next_cursor || null;
  } while (cursor);
  return items;
}

/** Studio state changes are announced politely, one sentence each (03 §17). */
function announceChanges(before, after) {
  const was = new Map(before.map((s) => [s.id, (s.group || {}).state || s.install_state]));
  for (const s of after) {
    const now = (s.group || {}).state || s.install_state;
    if (was.has(s.id) && was.get(s.id) !== now) announce(`${s.name} is now ${now}.`);
  }
}

/** act runs one of the card actions the state vocabulary names (03 §6). */
export async function act(ctx, studio, action) {
  switch (action) {
    case "launch":
      return launch(ctx, studio);
    case "stop":
      return stop(ctx, studio);
    case "open":
      // Inside helmstudio, on the studio's own origin (web/embed.js). The tab
      // is still one click away, from there and from the row's menu.
      return ctx.go(`#/studios/${encodeURIComponent(studio.id)}/open`);
    case "open-tab": {
      const p = ((studio.group || {}).processes || []).find((x) => x.ui && x.port);
      if (!p) return toast(`${studio.name} declares no page to open.`, "error");
      return window.open(`http://127.0.0.1:${p.port}${p.ui}`, "_blank", "noopener");
    }
    case "install":
    case "retry":
      try {
        // Installing runs someone else's build steps, so it goes through the
        // approval screen unless what would run is already approved (M7 Q10).
        const started = await guard(ctx, studio, action, (approval) =>
          action === "install"
            ? ctx.client.studios.install(studio.id, { approval })
            : ctx.client.studios.retry(studio.id, { approval }));
        if (started) {
          announce(`${studio.name} is installing.`);
          ctx.go(`#/studios/${studio.id}`);
        }
      } catch (err) {
        toast(failure(err, `${studio.name} could not be installed.`), "error");
      }
      return ctx.refresh();
    case "uninstall":
      try {
        await ctx.client.studios.uninstall(studio.id);
        announce(`${studio.name} is being removed.`);
      } catch (err) {
        toast(failure(err, `${studio.name} could not be removed.`), "error");
      }
      return ctx.refresh();
    case "update":
      try {
        // New code from upstream, so the approval screen stands in front of it
        // exactly as it does for an install — for the commit the update would
        // take, which is what `do=update` asks the screen to show.
        const started = await guard(ctx, studio, "update", (approval) =>
          ctx.client.studios.update(studio.id, { approval }));
        if (started) {
          announce(`${studio.name} is updating.`);
          ctx.go(`#/studios/${studio.id}`);
        }
      } catch (err) {
        // Already at the tip, no repository to pull from, or one that could
        // not be reached: every one of those is a sentence the daemon wrote,
        // and none of them may be swallowed into a button that does nothing.
        toast(failure(err, `${studio.name} could not be updated.`), "error");
      }
      return ctx.refresh();
    case "check":
      try {
        const after = await ctx.client.studios.checkUpdate(studio.id);
        // A check that says nothing is a check nobody can trust: up to date
        // and cannot-be-reached look identical when both are silent.
        toast(after && after.install_state === "update_available"
          ? `${studio.name} has an update.`
          : `${studio.name} is up to date.`);
      } catch (err) {
        toast(failure(err, `Whether ${studio.name} has an update could not be read.`), "error");
      }
      return ctx.refresh();
    case "cancel":
      if (!studio.job_id) return;
      try {
        await ctx.client.jobs.cancel(studio.job_id);
      } catch (err) {
        toast(failure(err, "That could not be cancelled."), "error");
      }
      return ctx.refresh();
    case "detail":
      return ctx.go(`#/studios/${studio.id}`);
    case "processes":
      return ctx.go(`#/studios/${studio.id}/processes`);
    case "token":
      return ctx.go("#/settings");
    default:
      return undefined;
  }
}

// ---------------------------------------------------------------- the shell

/**
 * route splits a hash into the screen, its path arguments and its query. The
 * query is where a screen keeps what it has open — which process log, say —
 * so that the address bar carries it and a reload comes back to the same view.
 */
export function route(hash) {
  const [path, search] = (hash || "#/studios").slice(1).split("?");
  const query = new URLSearchParams(search || "");
  for (const r of ROUTES) {
    const m = r.path.exec(path);
    if (m) return { ...r, args: m.slice(1), query };
  }
  return { ...ROUTES[0], args: [], query };
}

const GROUP_WORD = { starting: "Starting", running: "Running", stopping: "Stopping" };

function topbar(ctx, address) {
  const running = ctx.store.running;
  const first = running[0];
  const bar = el("header", { class: "helm-topbar" },
    el("span", { class: "helm-brand", text: "helmstudio" }),
    el("span", { class: "helm-meta helm-topbar-address", text: address }),
    el("span", { class: "helm-spacer" }));

  if (first) {
    const group = first.group || {};
    const p = (group.processes || []).find((x) => x.role === "main") || (group.processes || [])[0] || {};
    bar.append(
      chip([`${GROUP_WORD[group.state]} · ${first.name} · `, elapsed(p.started_at), p.port ? ` · :${p.port}` : ""],
        group.state === "running" ? "running" : "info"),
      el("button", {
        class: "helm-btn helm-btn-secondary helm-btn-sm",
        text: "Stop",
        disabled: group.state === "stopping",
        onclick: () => ctx.act(first, "stop"),
      }));
    // Only worth saying when it is not the one the bar already names.
    if (running.length > 1) bar.append(el("span", { class: "helm-micro", text: `${running.length} running` }));
  } else {
    bar.append(el("span", { class: "helm-micro", text: "Nothing running" }));
  }
  bar.append(themeControl(ctx));
  return bar;
}

/** applyTheme sets data-theme, or removes it so the OS preference applies. */
export function applyTheme(value) {
  if (value === "system") document.documentElement.removeAttribute("data-theme");
  else document.documentElement.setAttribute("data-theme", value);
}

function nav(current) {
  const bar = el("nav", { class: "helm-nav", "aria-label": "Main" });
  for (const item of NAV) {
    bar.append(el("a", {
      class: "helm-nav-link",
      href: item.href,
      text: item.label,
      "aria-current": item.id === current ? "page" : undefined,
    }));
  }
  return bar;
}

/**
 * unreachable is the one sentence a failed poll earns. The page under it keeps
 * the last rows the daemon served, greyed and inert, rather than a blank.
 */
function unreachable(ctx) {
  return el("div", { class: "helm-panel helm-unreachable", role: "alert" },
    el("div", { class: "helm-panel-body helm-stack" },
      el("p", { class: "helm-body helm-status-error", text: ctx.store.error }),
      el("p", { class: "helm-micro", text: ctx.store.loaded
        ? "What is below is what it last said. The page keeps trying, and nothing you have installed is affected."
        : "The page keeps trying. Nothing you have installed is affected." })));
}

/**
 * shell renders the whole page: skip link first, then the top bar, the nav and
 * the screen, which is the focus order 03 §17 asks for.
 */
export function shell(ctx, hash, address) {
  const r = route(hash);
  ctx.query = r.query;
  const stale = !!ctx.store.error;
  const page = el("div", { class: `helm-page helm-page-${r.width}` },
    r.back ? el("a", { class: "helm-back", href: "#/studios", "data-key": "back", text: "Studios" }) : null,
    stale ? unreachable(ctx) : null,
    // Always the same wrapper, so a failed poll greys the screen in place
    // rather than drawing it again from nothing.
    stale && !ctx.store.loaded
      ? null
      : el("div", {
        class: "helm-screen" + (stale ? " helm-stale" : ""), inert: stale,
        // Keyed by the page, so another page is drawn fresh rather than made
        // out of this one's elements, while a new section or log of the same
        // page changes in place.
        "data-key": "screen " + (hash || "#/studios").split("?")[0],
      }, r.screen(ctx, ...r.args)));
  // A page's title is where focus lands when the page changes (arrive), so
  // it is focusable in every drawing. Set only by arrive, the next redraw
  // would take it away again and focus would fall to the body.
  for (const h of page.querySelectorAll("h1")) h.setAttribute("tabindex", "-1");
  return [
    el("a", {
      class: "helm-skip-link", href: "#main", text: "Skip to content",
      // A link to #main is a route to this router, and it answered with
      // Studios. Skipping moves focus; it goes nowhere.
      onclick: (e) => {
        e.preventDefault();
        document.getElementById("main").focus();
      },
    }),
    topbar(ctx, address),
    nav(r.nav),
    el("main", { class: "helm-main", id: "main", tabindex: "-1" }, page),
  ];
}

/** render draws the page into app in place: what has not changed is not touched. */
export function render(app, ctx, hash, address) {
  morphChildren(app, shell(ctx, hash, address));
}

/**
 * arrive moves focus after a navigation (03 §17, amended 2026-09-17): to the
 * page's title when the page changed, and to a section's heading when only
 * the editor's section did. A poll never calls it, so a poll never moves focus.
 */
export function arrive(app, from, to) {
  const path = (h) => (h || "#/studios").split("?")[0];
  let target = null;
  if (path(from) !== path(to)) {
    window.scrollTo(0, 0);
    target = app.querySelector("main h1");
  } else if (route(from).query.get("section") !== route(to).query.get("section")) {
    target = app.querySelector("main [data-section-heading]");
  }
  if (target) target.focus({ preventScroll: true });
}

export async function start() {
  const client = connect();
  const store = newStore();
  const app = document.getElementById("app");
  let drawn = "";
  const redraw = (force) => {
    // A poll that changed nothing draws nothing, and a poll never draws while
    // someone is typing: the field under the caret is left alone either way,
    // but a page that moves under a sentence being typed is still a page
    // moving under a sentence being typed.
    const now = signature(ctx, location.hash);
    if (!force && now === drawn) return;
    if (!force && typing(app)) return;
    drawn = now;
    render(app, ctx, location.hash, location.host);
    document.documentElement.dataset.ready = "1";
  };
  const ctx = newContext({ client, store, redraw });

  let at = location.hash;
  window.addEventListener("hashchange", () => {
    const from = at;
    at = location.hash;
    redraw(true);
    arrive(app, from, at);
  });
  // Elapsed times count between polls, a digit at a time.
  setInterval(() => tick(app), 1000);
  try {
    const t = await client.settings.theme();
    store.theme = t.theme || "system";
    applyTheme(store.theme);
  } catch {
    // The launcher's own theme is a convenience; without it the page follows
    // the OS, which is the default anyway.
  }
  redraw(true);
  await ctx.refresh();
  setInterval(() => ctx.refresh(), POLL_MS);
}

/**
 * signature is everything the page draws, flattened. Two polls with the same
 * signature draw the same page, so the second one is skipped — which means a
 * field a screen draws and this leaves out is a field that never updates.
 */
export function signature(ctx, hash) {
  const s = ctx.store;
  return JSON.stringify([
    hash, s.theme, s.error, s.loaded, s.hfToken, s.about, s.disk,
    s.studios.map((x) => [x.id, x.install_state, (x.group || {}).state, x.job_id, x.size_bytes,
      // What a library card states beyond its install state (03 §13a). A
      // Revert changes the source and nothing else, so without these the card
      // would go on saying "Local" until something unrelated moved.
      x.name, x.description, x.source, x.overrides, x.level, x.manifest_valid, (x.errors || []).length,
      (x.provenance || {}).kind, x.approval_required, x.rebuild_needed, x.selection,
      // And where its code comes from, which its facts line says, and its hue.
      x.repo, x.ref, x.local_path, (x.hue || {}).dark, (x.hue || {}).light,
      ((x.group || {}).processes || []).map((p) => [p.spec_name, p.state, p.health_state, p.port, p.started_at])]),
    s.models.map((m) => [m.id, m.state, m.source, m.bytes_on_disk, m.total_bytes, m.last_used_at, m.external_path, (m.studios || []).join(",")]),
    Object.values(s.jobs).map((j) => [j.id, j.state, j.progress_num, j.progress_den,
      (j.steps || []).map((t) => [t.step_index, t.state, t.exit_code])]),
  ]);
}

/** typing reports whether the caret is in a field this page owns. */
function typing(app) {
  const active = document.activeElement;
  if (!active || !app.contains(active)) return false;
  return ["INPUT", "TEXTAREA", "SELECT"].includes(active.tagName) || active.isContentEditable;
}
