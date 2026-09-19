// The launcher's Gallery (03 §10, amended 2026-09-19): every studio's work in
// one grid, and the same grid scoped to one studio beside that studio's page.
//
// The grid is <helm-gallery> — the component a studio mounts in its own page —
// given the launcher's client, which reads every studio because the daemon
// answers its three operations under a principal that holds gallery.read_all.
// One query serves both views, which is what 03 §10 asks of them.
//
// What helmstudio draws around it is the one filter a component cannot have:
// which studio made it. A component is given a client and never learns what is
// installed (04 §11 rule 2), so the chips are the launcher's and the choice
// reaches the component as its `studio` attribute.
//
// The launcher's client has no event stream and no gallery update, so there is
// no live insertion and no star here. Both are 04 §9's smaller component
// rather than a broken one: an item belongs to the studio that made it, and a
// PATCH of another's is refused, so a star the launcher drew could only fail.

import { el } from "./ui.js";

/**
 * grid keeps one <helm-gallery> per key across redraws. A poll runs every two
 * seconds; a gallery rebuilt on each one would lose its scroll, its filters
 * and whatever is playing in its viewer.
 */
export function grid(ctx, key, studioID) {
  const gallery = ctx.keep("gallery:" + key, () => {
    const g = document.createElement("helm-gallery");
    // Every studio, always: the launcher has no items of its own, and its
    // operation has no scope to send anyway.
    g.setAttribute("scope", "all");
    g.client = ctx.client;
    return g;
  });
  if (studioID) gallery.setAttribute("studio", studioID);
  else gallery.removeAttribute("studio");
  return gallery;
}

/**
 * chips are the scope: All studios, then one per installed studio in its own
 * hue. They are links, so a scope is a place you can come back to.
 */
export function chips(ctx, current, href) {
  // Anything that has ever been installed, which is not the "ready or
  // update_available" test the action buttons use: a studio whose build broke
  // this morning still made everything it made before that, and a chip is a
  // filter over what exists rather than a statement about what can run.
  const studios = (ctx.store.studios || []).filter((s) => s.install_state && s.install_state !== "listed");
  const one = (id, label, hue) => el("a", {
    class: "helm-chip helm-scope" + (id === current ? " helm-scope-on" : ""),
    href: href(id),
    "aria-current": id === current ? "true" : undefined,
    style: hue && hue.dark && hue.light ? `--_hue-dark: ${hue.dark}; --_hue-light: ${hue.light}` : undefined,
  }, hue ? el("span", { class: "helm-hue-dot" }) : null, label);
  return [
    one("", "All studios", null),
    ...studios.map((s) => one(s.id, s.name, s.hue)),
  ];
}

/** galleryScreen is the nav's Gallery: every studio, narrowed by a chip. */
export function galleryScreen(ctx) {
  const studioID = ctx.query.get("studio") || "";
  return el("div", { class: "helm-stack helm-gallery-screen" },
    el("div", { class: "helm-page-header" },
      el("h1", { class: "helm-title", text: "Gallery" }),
      ...chips(ctx, studioID, (id) => (id ? `#/gallery?studio=${encodeURIComponent(id)}` : "#/gallery"))),
    grid(ctx, "launcher", studioID));
}
