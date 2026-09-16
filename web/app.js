// The launcher: the shell, the router and the one poll that keeps every screen
// current (docs/decisions.md M6 Q2).
//
// A screen is a function of a context and its route arguments, returning a
// node. It owns no timer and no state: the poll replaces the studio list and
// the current screen is drawn again from it. That is what keeps four cards
// ticking through an install while the title and stripe never move (03 §6) —
// and it is what lets a fixture page draw any screen from canned data by
// handing it a context whose client is a fake.
//
// Not here, by their own milestones: Gallery and Timeline, which read every
// studio's items and bytes and wait for M9's cookie (M6 Q11, M8 Q20); Adding a
// studio and the manifest editor, which are M7's; Doctor, which belongs to no
// milestone yet and is hidden rather than shown empty.

import { connect } from "./launcher.js";
import { announce, chip, el, failure, since, themeControl, toast } from "./ui.js";
import { catalogue } from "./catalogue.js";
import { studioDetail } from "./studio.js";
import { processGroup } from "./processes.js";
import { modelsAndDisk } from "./models.js";
import { settings } from "./settings.js";
import { launch, stop } from "./switch.js";
import { guard } from "./approval.js";
import { addStudio } from "./add.js";
import { editor } from "./editor.js";

/** How often the list is refreshed: cheap enough to leave running, short
 *  enough that a state change is seen before it is wondered about. */
const POLL_MS = 2000;

const ROUTES = [
  { path: /^\/studios$/, screen: catalogue, nav: "studios" },
  { path: /^\/studios\/([^/]+)\/processes$/, screen: processGroup, nav: "studios" },
  { path: /^\/studios\/([^/]+)$/, screen: studioDetail, nav: "studios" },
  // Adding and editing are their own paths rather than /studios/new, because
  // `new` is a legal studio id and a route that shadowed one would be a bug
  // nobody found until somebody wrote it.
  { path: /^\/add$/, screen: addStudio, nav: "studios" },
  { path: /^\/edit$/, screen: editor, nav: "studios" },
  { path: /^\/edit\/([^/]+)$/, screen: editor, nav: "studios" },
  { path: /^\/models$/, screen: modelsAndDisk, nav: "models" },
  { path: /^\/settings$/, screen: settings, nav: "settings" },
];

const NAV = [
  { id: "studios", label: "Studios", href: "#/studios" },
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
     * moved into the new tree keeps its buffer and resumes.
     */
    keep(key, make) {
      if (!kept.has(key)) kept.set(key, make());
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
    case "open": {
      const p = ((studio.group || {}).processes || []).find((x) => x.ui && x.port);
      if (!p) return toast(`${studio.name} declares no page to open.`, "error");
      return window.open(`http://127.0.0.1:${p.port}${p.ui}`, "_blank", "noopener");
    }
    case "install":
    case "retry":
      try {
        // Installing runs someone else's build steps, so it goes through the
        // approval screen unless what would run is already approved (M7 Q10).
        const started = await guard(ctx, studio, action === "retry" ? "Retry" : "Install", (approval) =>
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

function topbar(ctx, address) {
  const running = ctx.store.running;
  const first = running[0];
  const bar = el("header", { class: "helm-topbar" },
    el("span", { class: "helm-brand", text: "helmstudio" }),
    el("span", { class: "helm-micro", text: address }),
    el("span", { class: "helm-spacer" }));

  if (first) {
    const p = ((first.group || {}).processes || []).find((x) => x.role === "main") || (first.group.processes || [])[0] || {};
    bar.append(
      chip(`${first.name} · ${since(p.started_at)}`, first.group.state === "running" ? "running" : "info"),
      el("button", {
        class: "helm-btn helm-btn-secondary helm-btn-sm",
        text: "Stop",
        onclick: () => ctx.act(first, "stop"),
      }),
      el("span", { class: "helm-micro", text: `${running.length} running` }));
  } else {
    bar.append(el("span", { class: "helm-micro", text: "nothing running" }));
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
 * shell renders the whole page: skip link first, then the top bar, the nav and
 * the screen, which is the focus order 03 §17 asks for.
 */
export function shell(ctx, hash, address) {
  const r = route(hash);
  const main = el("main", { class: "helm-main", id: "main", tabindex: "-1" });
  if (ctx.store.error) {
    main.append(el("div", { class: "helm-panel" },
      el("div", { class: "helm-panel-body helm-stack" },
        el("p", { class: "helm-body", text: ctx.store.error }),
        el("p", { class: "helm-micro", text: "The page keeps trying. Nothing you have installed is affected." }))));
  } else {
    ctx.query = r.query;
    main.append(r.screen(ctx, ...r.args));
  }
  return [
    el("a", { class: "helm-skip-link", href: "#main", text: "Skip to content" }),
    topbar(ctx, address),
    nav(r.nav),
    main,
  ];
}

export async function start() {
  const client = connect();
  const store = newStore();
  const app = document.getElementById("app");
  let drawn = "";
  const redraw = (force) => {
    // Redrawing on a poll that changed nothing would throw away the caret and
    // any open menu twice a minute, so the page is rebuilt only when what it
    // draws has actually moved — and never while someone is typing into it.
    const now = signature(ctx, location.hash);
    if (!force && now === drawn) return;
    if (!force && typing(app)) return;
    drawn = now;
    app.replaceChildren(...shell(ctx, location.hash, location.host));
    document.documentElement.dataset.ready = "1";
  };
  const ctx = newContext({ client, store, redraw });

  window.addEventListener("hashchange", () => redraw(true));
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
 * signature draw the same page, so the second one is skipped.
 */
function signature(ctx, hash) {
  const s = ctx.store;
  return JSON.stringify([
    hash, s.theme, s.error, s.hfToken,
    s.studios.map((x) => [x.id, x.install_state, (x.group || {}).state, x.job_id, x.size_bytes,
      ((x.group || {}).processes || []).map((p) => [p.spec_name, p.state, p.health_state, p.port, p.started_at])]),
    s.models.map((m) => [m.id, m.state, m.bytes_on_disk, (m.studios || []).join(",")]),
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
