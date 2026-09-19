// <helm-gallery scope="self" kind="video" picker>
//
// The grid of past outputs, over the same query the launcher's cross-studio
// view uses — the two differ only by scope (03 §10, 04 §5). Thumbnails, params,
// star and tag, cursor paging, live insertion when a new item lands on the
// event stream, and a picker mode that resolves to an asset, which is how a
// studio offers "use one of my earlier renders" without building a browser.
//
// Structural interface (04 §5, M6 Q12):
//
//   gallery.query({ scope, kind, q, limit, cursor }) → { items, next_cursor }
//   gallery.update(id, patch)                       → the item      (optional)
//   assets.thumb(id, { w })                         → a fetch Response
//   events.subscribe({ lastEventId })               → async iterable (optional)
//
// The component never builds a URL. An asset's bytes are reached by calling
// the client's own method and reading the URL the client resolved off the
// response it returns — `Asset.url` tells a *page* to prefix its proxy, and a
// component may not know a proxy exists (04 §11 rule 2).
//
// What can be done with a selected item is the studio's business, not a
// component's (rule 5): selecting emits `select`, and a studio puts its own
// buttons in the `actions` slot.

import { HelmElement, bytes, define, el, kindOf, message } from "./base.js";
// Registers <helm-player>, which the viewer below mounts. A gallery imported
// on its own would otherwise open a dialog around an element that never
// upgrades.
import "./player.js";

const PAGE = 48;
const THUMB_W = 320;

const styles = `
  :host { height: 100%; }
  .filters { display: flex; align-items: center; gap: var(--helm-space-2); flex-wrap: wrap; }
  select, input[type="search"] {
    font: var(--helm-type-body);
    height: var(--helm-control-sm);
    padding: 0 var(--helm-space-2);
    color: var(--helm-text-primary);
    background: var(--helm-ground-page);
    border: 1px solid var(--helm-border-strong);
    border-radius: var(--helm-radius-sm);
  }
  input[type="search"] { min-width: 12ch; }
  input[type="search"]::placeholder { color: var(--helm-text-muted); }
  .body { flex: 1 1 auto; min-height: 0; overflow: auto; padding: var(--helm-space-3); }
  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(168px, 1fr));
    gap: var(--helm-space-3);
  }
  :host([list]) .grid { grid-template-columns: 1fr; gap: var(--helm-space-1); }
  .item {
    display: flex;
    flex-direction: column;
    gap: var(--helm-space-1);
    padding: 0;
    text-align: left;
    background: var(--helm-ground-raised);
    border: 1px solid var(--helm-border-hairline);
    border-radius: var(--helm-radius-md);
    overflow: hidden;
    height: auto;
    cursor: pointer;
  }
  .item[aria-selected="true"] { border-color: var(--helm-accent-base); }
  :host([list]) .item { flex-direction: row; align-items: center; gap: var(--helm-space-3); padding: var(--helm-space-1) var(--helm-space-2); }
  .thumb {
    position: relative;
    aspect-ratio: 16 / 9;
    background: var(--helm-ground-inset);
    display: flex; align-items: center; justify-content: center;
    overflow: hidden;
  }
  :host([list]) .thumb { width: 64px; aspect-ratio: 16 / 9; flex: 0 0 auto; border-radius: var(--helm-radius-sm); }
  .thumb img { position: absolute; inset: 0; width: 100%; height: 100%; object-fit: cover; display: block; }
  .glyph { color: var(--helm-log-muted); font: var(--helm-type-section); letter-spacing: var(--helm-tracking-section); }
  /* Over a thumbnail the kind is a badge, .helm-kind's on the star's ground. */
  .thumb img ~ .glyph {
    position: absolute; left: var(--helm-space-1); bottom: var(--helm-space-1);
    display: flex; align-items: center;
    height: 18px; padding: 0 6px;
    font: var(--helm-type-micro); letter-spacing: normal;
    color: var(--helm-text-secondary);
    background: var(--helm-ground-panel);
    border: 1px solid var(--helm-border-hairline);
    border-radius: var(--helm-radius-sm);
  }
  :host([list]) .thumb img ~ .glyph { display: none; }
  .star {
    position: absolute; top: var(--helm-space-1); right: var(--helm-space-1);
    height: var(--helm-control-sm); width: var(--helm-control-sm);
    padding: 0; line-height: 1;
    background: var(--helm-ground-panel);
  }
  .star[aria-pressed="true"] { color: var(--helm-accent-text); }
  .text { padding: 0 var(--helm-space-2) var(--helm-space-2); display: flex; flex-direction: column; gap: 2px; min-width: 0; }
  :host([list]) .text { padding: 0; flex: 1 1 auto; }
  .name {
    font: var(--helm-type-card-title);
    color: var(--helm-text-primary);
    overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
  }
  .facts { font: var(--helm-type-mono); color: var(--helm-text-muted); font-variant-numeric: tabular-nums;
           overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .origin { color: var(--helm-studio-accent); }
  .tags { display: flex; gap: var(--helm-space-1); flex-wrap: wrap; }
  .tag {
    font: var(--helm-type-micro);
    color: var(--helm-text-secondary);
    background: var(--helm-ground-page);
    border: 1px solid var(--helm-border-hairline);
    border-radius: var(--helm-radius-sm);
    padding: 0 var(--helm-space-1);
  }
  .bar {
    display: flex; align-items: center; gap: var(--helm-space-2);
    padding: var(--helm-space-2) var(--helm-space-3);
    border-top: 1px solid var(--helm-border-hairline);
    background: var(--helm-ground-raised);
    flex: 0 0 auto;
  }
  .more { display: flex; justify-content: center; padding: var(--helm-space-4) 0; }
  dialog.viewer {
    width: min(1100px, 92vw);
    max-width: 92vw;
    border: 1px solid var(--helm-border-hairline);
    border-radius: var(--helm-radius-md);
    background: var(--helm-ground-panel);
    color: var(--helm-text-primary);
    padding: 0;
  }
  dialog.viewer::backdrop { background: var(--helm-scrim); }
  dialog.viewer .head { display: flex; align-items: center; gap: var(--helm-space-2); padding: var(--helm-space-3); }
  dialog.viewer helm-player { display: block; }
  dialog.viewer:fullscreen { width: 100vw; max-width: 100vw; height: 100vh; border: 0; border-radius: 0; }

`;

/** The label under an item: the studio, or "timeline" for a sequence's export. */
export function origin(item) {
  if (item.timeline_id) return "timeline";
  return item.studio_id || "";
}

/** facts is 03 §15's metadata line for one item. */
export function facts(item) {
  const a = item.asset || {};
  const out = [origin(item)];
  if (a.width && a.height) out.push(`${a.width}×${a.height}`);
  if (a.duration_s) out.push(`${Number(a.duration_s).toFixed(1)}s`);
  const seed = item.params && (item.params.seed ?? item.params.Seed);
  if (seed !== undefined && seed !== null) out.push("seed " + seed);
  if (a.bytes) out.push(bytes(a.bytes));
  return out.filter(Boolean).join(" · ");
}

export class HelmGallery extends HelmElement {
  static get observedAttributes() {
    return ["scope", "kind", "picker", "studio"];
  }

  constructor() {
    super(styles);
    this.items = [];
    this.byId = new Map();
    this.urls = [];
    this.cursor = null;
    this.selected = null;
    this.build();
  }

  build() {
    this.kindSel = el("select", { "aria-label": "Kind", onchange: () => this.reload() },
      el("option", { value: "", text: "All kinds" }),
      // The kinds AssetKind has. "text" was offered here and is not one of
      // them, so choosing it could only ever be refused as an invalid
      // parameter.
      ...["video", "image", "audio", "other"].map((k) => el("option", { value: k, text: k })));
    this.search = el("input", { type: "search", placeholder: "Search prompts", "aria-label": "Search prompts" });
    this.search.addEventListener("change", () => this.reload());
    this.listBtn = el("button", {
      text: "List", "aria-pressed": "false",
      onclick: () => {
        this.toggleAttribute("list");
        this.listBtn.setAttribute("aria-pressed", String(this.hasAttribute("list")));
      },
    });
    this.count = el("span", { class: "micro", part: "count" });

    this.grid = el("div", { class: "grid", role: "listbox", "aria-label": "Items", part: "grid" });
    this.moreBtn = el("button", { text: "Load more", onclick: () => this.page() });
    this.more = el("div", { class: "more" }, this.moreBtn);
    this.more.hidden = true;
    this.body = el("div", { class: "body", part: "body", onscroll: () => this.onScroll() }, this.grid, this.more);

    this.selectionText = el("span", { class: "micro", part: "selection" });
    this.pickBtn = el("button", { text: "Use this", hidden: true, onclick: () => this.pick() });
    this.bar = el("div", { class: "bar" },
      this.selectionText, el("span", { class: "spacer" }),
      el("slot", { name: "actions" }), this.pickBtn);

    this.panel = el("div", { class: "panel" },
      el("div", { class: "head" },
        el("span", { class: "label", text: "Gallery" }),
        this.count,
        el("span", { class: "spacer" }),
        el("div", { class: "filters" }, this.kindSel, this.search, this.listBtn)),
      this.body,
      this.bar);
    this.mountPanel(this.panel);
  }

  connectedCallback() {
    const kind = this.getAttribute("kind");
    if (kind) this.kindSel.value = kind;
    this.pickBtn.hidden = !this.hasAttribute("picker");
    this.reload();
  }

  attributeChangedCallback(name, before, after) {
    if (before === after || !this.isConnected) return;
    if (name === "picker") this.pickBtn.hidden = !this.hasAttribute("picker");
    else this.reload();
  }

  disconnectedCallback() {
    this.releaseURLs();
    super.disconnectedCallback();
  }

  releaseURLs() {
    for (const u of this.urls.splice(0)) URL.revokeObjectURL(u);
  }

  query() {
    return {
      scope: this.getAttribute("scope") || undefined,
      studio: this.getAttribute("studio") || undefined,
      kind: this.kindSel.value || undefined,
      q: this.search.value || undefined,
      limit: PAGE,
    };
  }

  async reload() {
    if (!this.needClient(this.region)) return;
    this.releaseURLs();
    this.items = [];
    this.byId.clear();
    this.cursor = null;
    this.selected = null;
    this.grid.replaceChildren();
    this.paint();
    const generation = (this.generation = (this.generation || 0) + 1);
    // Any page still in flight belongs to the generation just left behind, and
    // its result is discarded below on arrival. Leaving `loading` set would
    // make the fetch under it return without asking for anything: the grid was
    // cleared a moment ago, so a filter changed while the first page was still
    // loading emptied the gallery and left it empty, with no count and no
    // error to say why.
    this.loading = false;
    await this.page();
    if (generation === this.generation) this.watch();
  }

  async page() {
    const client = this.client;
    if (!client || this.loading) return;
    this.loading = true;
    this.moreBtn.disabled = true;
    const generation = this.generation;
    try {
      const params = this.query();
      if (this.cursor) params.cursor = this.cursor;
      const page = await client.gallery.query(params);
      if (generation !== this.generation || !this.live) return;
      this.cursor = page.next_cursor || null;
      for (const item of page.items || []) this.add(item, false);
      this.paint();
    } catch (err) {
      if (generation !== this.generation || !this.live) return;
      if (this.items.length === 0) {
        const scope = this.getAttribute("scope");
        this.fail(this.region, err, kindOf(err) === "Forbidden" && scope === "all"
          ? "This studio may only see what it made itself. Reading every studio's work needs the gallery.read_all capability, declared in its manifest."
          : "The gallery could not be read.");
        return;
      }
      this.selectionText.textContent = message(err, "Loading more failed.");
    } finally {
      this.loading = false;
      this.moreBtn.disabled = false;
    }
  }

  /**
   * watch inserts items as they are made. A client without an event stream —
   * or one whose token does not carry it — simply does not get live insertion,
   * which is a reduced gallery rather than a broken one (04 §9).
   */
  async watch() {
    const client = this.client;
    if (!client || !client.events || typeof client.events.subscribe !== "function") return;
    const generation = this.generation;
    let lastEventId;
    try {
      const stream = await client.events.subscribe({ lastEventId });
      for await (const ev of stream) {
        if (generation !== this.generation || !this.live) return;
        if (ev.id) lastEventId = ev.id;
        if (ev.name !== "item") continue;
        const change = ev.json() || {};
        const item = change.item;
        if (!item) continue;
        if (change.change === "deleted") this.remove(item.id);
        else this.add(item, change.change === "added");
        this.paint();
      }
    } catch {
      // The stream is an improvement, not a dependency. Paging still works.
    }
  }

  matches(item) {
    const kind = this.kindSel.value;
    return !kind || item.kind === kind;
  }

  add(item, atTop) {
    if (!this.matches(item)) return;
    const seen = this.byId.get(item.id);
    if (seen) {
      Object.assign(seen, item);
      return;
    }
    this.byId.set(item.id, item);
    if (atTop) this.items.unshift(item);
    else this.items.push(item);
  }

  remove(id) {
    if (!this.byId.delete(id)) return;
    this.items = this.items.filter((i) => i.id !== id);
    if (this.selected === id) this.selected = null;
  }

  onScroll() {
    if (!this.cursor || this.loading) return;
    const remaining = this.body.scrollHeight - this.body.scrollTop - this.body.clientHeight;
    if (remaining < this.body.clientHeight) this.page();
  }

  paint() {
    const total = this.items.length;
    const size = this.items.reduce((n, i) => n + ((i.asset && i.asset.bytes) || 0), 0);
    this.count.textContent = total
      ? `${total.toLocaleString()} item${total === 1 ? "" : "s"}${size ? " · " + bytes(size) : ""}`
      : "";
    this.grid.replaceChildren(...this.items.map((item) => this.card(item)));
    this.more.hidden = !this.cursor;
    if (total === 0 && !this.loading) {
      this.grid.replaceChildren(el("div", { class: "empty", part: "empty" },
        el("p", { text: "Nothing here yet" }),
        el("p", { class: "why", text: "Renders appear here as they are made." })));
    }
    const chosen = this.selected && this.byId.get(this.selected);
    this.selectionText.textContent = chosen ? this.provenance(chosen) : "";
    this.pickBtn.disabled = !chosen;
  }

  /** The provenance chip: where this came from, which only a central store knows. */
  provenance(item) {
    const from = (item.inputs || []).find((i) => i.item_id || i.asset_id);
    const name = from && (from.item_id || from.asset_id);
    return name ? `from ${name}` : facts(item);
  }

  card(item) {
    const thumb = el("div", { class: "thumb" }, el("span", { class: "glyph", text: item.kind || "" }));
    this.loadThumb(item, thumb);
    if (this.client && this.client.gallery && typeof this.client.gallery.update === "function") {
      thumb.append(el("button", {
        class: "star",
        part: "star",
        text: item.starred ? "★" : "☆",
        title: item.starred ? "Starred" : "Star",
        "aria-label": item.starred ? "Starred" : "Star",
        "aria-pressed": String(!!item.starred),
        onclick: (e) => { e.stopPropagation(); this.star(item); },
      }));
    }
    const node = el("div", {
      class: "item",
      role: "option",
      tabindex: "0",
      "aria-selected": String(this.selected === item.id),
      onclick: () => this.activate(item),
      onkeydown: (e) => {
        if (e.key === "Enter" || e.key === " ") { e.preventDefault(); this.activate(item); }
        if (e.key === "Enter" && this.hasAttribute("picker")) this.pick();
      },
      ondblclick: () => { this.select(item); this.pick(); },
    },
      thumb,
      el("div", { class: "text" },
        el("span", { class: "name", text: item.title || item.id }),
        el("span", { class: "facts" }, el("span", { class: "origin", text: origin(item) }),
          document.createTextNode(facts(item).slice(origin(item).length))),
        (item.tags || []).length
          ? el("span", { class: "tags" }, ...item.tags.map((t) => el("span", { class: "tag", text: t.name || t })))
          : null));
    return node;
  }

  async loadThumb(item, into) {
    const client = this.client;
    const id = item.asset_id;
    if (!client || !client.assets || typeof client.assets.thumb !== "function" || !id) return;
    const generation = this.generation;
    try {
      const res = await client.assets.thumb(id, { w: THUMB_W });
      const blob = await res.blob();
      if (generation !== this.generation || !this.live || !into.isConnected) return;
      const url = URL.createObjectURL(blob);
      this.urls.push(url);
      // One that does not decode goes again, and the kind is the well's
      // placeholder once more.
      const img = el("img", { src: url, alt: "", loading: "lazy", onerror: () => img.remove() });
      into.prepend(img);
    } catch {
      // No thumbnail for this kind, or not yet. The kind glyph stays, which
      // is a reduced card rather than a broken one.
    }
  }

  async star(item) {
    const next = !item.starred;
    item.starred = next;
    this.paint();
    try {
      await this.client.gallery.update(item.id, { starred: next });
    } catch (err) {
      item.starred = !next;
      this.paint();
      this.selectionText.textContent = message(err, "That item could not be changed.");
    }
  }

  /**
   * activate is what a click or Enter on an item does: it selects, and in a
   * gallery that is not a picker it opens the item to be looked at (04 §11,
   * amended 2026-09-19). Picking is unchanged, and so is `select`.
   */
  activate(item) {
    this.select(item);
    if (!this.hasAttribute("picker")) this.view(item);
  }

  /**
   * view shows an item in a <helm-player>, in one dialog the component keeps.
   *
   * The player is given the client this gallery was given, so a page that
   * handed one over is not made to hand it over twice. What it does with it —
   * the one-byte range that resolves an address for the media element — is the
   * player's business and not repeated here.
   */
  view(item) {
    if (!item || !item.asset_id) return;
    const dialog = this.viewer();
    this.viewerTitle.textContent = item.title || item.id;
    this.player.client = this.client;
    const fps = item.params && item.params.fps;
    if (fps) this.player.setAttribute("fps", String(fps));
    else this.player.removeAttribute("fps");
    this.player.setAttribute("asset", item.asset_id);
    // A second activation while it is open changes what is playing rather
    // than opening a dialog that is already open, which throws.
    if (!dialog.open) dialog.showModal();
  }

  /** The viewer dialog, built the first time something is looked at. */
  viewer() {
    if (this.viewerDialog) return this.viewerDialog;
    this.viewerTitle = el("span", { class: "label", part: "viewer-title" });
    this.viewerDialog = el("dialog", { class: "viewer", part: "viewer" },
      el("div", { class: "head" },
        this.viewerTitle,
        el("span", { class: "spacer" }),
        el("button", { text: "Full screen", onclick: () => this.fullScreen() }),
        el("button", { text: "Close", onclick: () => this.viewerDialog.close() })));
    // The parser makes the player, not createElement. WebKit hands back an
    // HTMLUnknownElement for <helm-player> from createElement even with the
    // definition registered — `new` works, the parser works, createElement
    // does not — and an element that never upgrades renders nothing at all,
    // which is a dialog that opens on emptiness. h3 studio's own page carries
    // the same workaround for the same two tags.
    //
    // The markup is one fixed tag with no attributes and no data in it, so
    // this is not the innerHTML that CONTRIBUTING warns about.
    this.viewerDialog.insertAdjacentHTML("beforeend", "<helm-player></helm-player>");
    this.player = this.viewerDialog.lastElementChild;
    // A closed viewer holds nothing: dropping the asset stops the media
    // element and releases what it was playing.
    this.viewerDialog.addEventListener("close", () => this.player.removeAttribute("asset"));
    this.shadowRoot.append(this.viewerDialog);
    return this.viewerDialog;
  }

  /** Full screen is the dialog's, so the player keeps its own controls. */
  fullScreen() {
    const dialog = this.viewerDialog;
    if (!dialog) return;
    if (this.shadowRoot.fullscreenElement || document.fullscreenElement) document.exitFullscreen();
    else if (dialog.requestFullscreen) dialog.requestFullscreen();
  }

  select(item) {
    this.selected = item.id;
    this.paint();
    this.dispatchEvent(new CustomEvent("select", { detail: { item, asset: item.asset_id }, bubbles: true, composed: true }));
  }

  pick() {
    const item = this.selected && this.byId.get(this.selected);
    if (!item) return;
    this.dispatchEvent(new CustomEvent("pick", { detail: { item, asset: item.asset_id }, bubbles: true, composed: true }));
  }
}

define("helm-gallery", HelmGallery);
