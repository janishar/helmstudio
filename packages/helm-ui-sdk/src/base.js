// What the three components share (docs/design/04-packages.md §5, §11).
//
// Nothing here imports anything. A component receives a client and calls
// methods on it; it never constructs one, never names a URL, never sets a
// header and never reads a status code. The one thing it knows about a
// failure is the `kind` the runtime SDK puts on it — Invalid, Unauthenticated,
// Forbidden, NotFound, Conflict, QuotaExceeded, Unsupported, Unavailable,
// Internal (04 §4) — which is a vocabulary, not a wire format.
//
// Components render in Shadow DOM, so helm-css's classes do not reach them.
// Custom properties do, which is what themes them: every colour, space and
// duration below is a var(--helm-…) with no fallback literal, so a token that
// moves moves the components with it. Overrides go through ::part().

/** The kinds a component branches on. Anything else is a failure to report. */
export const UNSUPPORTED = "Unsupported";
export const FORBIDDEN = "Forbidden";

/** kindOf reads the runtime SDK's error kind, or "" for anything else. */
export function kindOf(err) {
  return (err && typeof err.kind === "string" && err.kind) || "";
}

/** message is what a component shows for a failure, in 03 §18's terms. */
export function message(err, fallback) {
  const m = err && typeof err.message === "string" ? err.message : "";
  // HelmError's own message is "code (status): text"; the text is the part
  // written for a person.
  const i = m.indexOf("): ");
  const text = i >= 0 ? m.slice(i + 3) : m;
  return text || fallback;
}

/** el builds an element with attributes and children in one call. */
export function el(tag, attrs, ...children) {
  const node = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs || {})) {
    if (v === undefined || v === null || v === false) continue;
    if (k === "text") node.textContent = String(v);
    else if (k.startsWith("on")) node.addEventListener(k.slice(2), v);
    else node.setAttribute(k, v === true ? "" : String(v));
  }
  for (const c of children.flat()) {
    if (c === undefined || c === null || c === false) continue;
    node.append(c);
  }
  return node;
}

/** The surface every component sits on, and the controls in its header. */
export const surface = `
  :host {
    display: block;
    color: var(--helm-text-primary);
    font: var(--helm-type-body);
    contain: layout style;
  }
  :host([hidden]) { display: none; }
  * { box-sizing: border-box; }
  .panel {
    display: flex;
    flex-direction: column;
    min-height: 0;
    height: 100%;
    background: var(--helm-ground-panel);
    border: 1px solid var(--helm-border-hairline);
    border-radius: var(--helm-radius-md);
    overflow: hidden;
  }
  .head {
    display: flex;
    align-items: center;
    gap: var(--helm-space-2);
    padding: var(--helm-space-2) var(--helm-space-3);
    border-bottom: 1px solid var(--helm-border-hairline);
    background: var(--helm-ground-raised);
    flex: 0 0 auto;
  }
  .label {
    font: var(--helm-type-section);
    letter-spacing: var(--helm-tracking-section);
    text-transform: uppercase;
    color: var(--helm-text-muted);
  }
  .micro {
    font: var(--helm-type-mono);
    color: var(--helm-text-muted);
    font-variant-numeric: tabular-nums;
  }
  .spacer { flex: 1 1 auto; }
  button {
    font: var(--helm-type-micro);
    height: var(--helm-control-sm);
    padding: 0 var(--helm-space-2);
    color: var(--helm-text-secondary);
    background: transparent;
    border: 1px solid var(--helm-border-hairline);
    border-radius: var(--helm-radius-sm);
    cursor: pointer;
    transition: color var(--helm-duration-hover) var(--helm-ease-standard),
                border-color var(--helm-duration-hover) var(--helm-ease-standard),
                background-color var(--helm-duration-hover) var(--helm-ease-standard);
  }
  button:hover { color: var(--helm-text-primary); border-color: var(--helm-border-strong); }
  button[aria-pressed="true"] {
    color: var(--helm-text-primary);
    background: var(--helm-accent-subtle);
    border-color: var(--helm-border-strong);
  }
  button:disabled { opacity: 0.5; cursor: default; }
  :focus-visible {
    outline: 2px solid var(--helm-focus-ring);
    outline-offset: 2px;
  }
  .empty {
    display: flex;
    flex-direction: column;
    gap: var(--helm-space-1);
    align-items: center;
    justify-content: center;
    text-align: center;
    padding: var(--helm-space-6) var(--helm-space-4);
    color: var(--helm-text-secondary);
    flex: 1 1 auto;
  }
  .empty .why { color: var(--helm-text-muted); font: var(--helm-type-micro); max-width: 46ch; }
  .sr {
    position: absolute;
    width: 1px; height: 1px;
    margin: -1px; padding: 0; border: 0;
    clip-path: inset(50%);
    overflow: hidden; white-space: nowrap;
  }
  @media (prefers-reduced-motion: reduce) {
    button { transition: none; }
  }
`;

/**
 * The last rule in every component's stylesheet. `hidden` is how a component
 * takes a control out of the page, and a flex or grid `display` anywhere above
 * would quietly beat the user-agent's [hidden] rule — the filmstrip an image
 * has no use for would stay on screen. Last wins, at one class of specificity,
 * with no !important.
 */
const hideLast = `
  [hidden] { display: none; }
`;

/**
 * HelmElement is the base every component extends.
 *
 * The client is the one dependency, and it arrives rather than being built:
 * `el.client = …`, or the page sets `window.helm` once and every component
 * picks it up (04 §5). Setting it re-runs the component's load.
 */
export class HelmElement extends HTMLElement {
  constructor(styles) {
    super();
    this.attachShadow({ mode: "open" });
    // The frame holds either the component or one of the two states that
    // replace it whole — no client, or a failure. Keeping them as siblings
    // inside a frame is what lets a component come back: a client arriving
    // after the element connected is the ordinary case for a page that builds
    // its elements first and assigns window.helm second.
    // Non-writable, so a subclass that reuses the name — `frame` was a video
    // frame counter once — fails at construction rather than at first render.
    Object.defineProperty(this, "region", { value: el("div", { style: "display: contents" }) });
    this.shadowRoot.append(el("style", { text: surface + styles + hideLast }), this.region);
    /** Everything to undo on disconnect: aborts, timers, object URLs. */
    this._cleanup = [];
    this._client = null;
  }

  /** mount makes node the component's body, and what restore() puts back. */
  mountPanel(node) {
    this.panel = node;
    this.region.replaceChildren(node);
  }

  /** restore puts the component back after an empty state replaced it. */
  restore() {
    if (this.panel && this.region.firstChild !== this.panel) this.region.replaceChildren(this.panel);
  }

  get client() {
    return this._client || globalThis.helm || null;
  }

  set client(c) {
    this._client = c;
    if (this.isConnected) this.reload();
  }

  /** live reports whether this element is still the one to render into. */
  get live() {
    return this.isConnected;
  }

  onCleanup(fn) {
    this._cleanup.push(fn);
  }

  disconnectedCallback() {
    for (const fn of this._cleanup.splice(0)) {
      try {
        fn();
      } catch {
        // Cleanup runs on teardown; a failure here must not stop the rest.
      }
    }
  }

  /** reload is what a subclass implements to (re)start against the client. */
  reload() {}

  /**
   * needClient renders the one state that is the page's fault rather than the
   * daemon's, and reports whether a client is there.
   */
  needClient(into) {
    if (this.client) {
      this.restore();
      return true;
    }
    into.replaceChildren(
      el("div", { class: "empty", part: "empty" },
        el("p", { text: "No client" }),
        el("p", { class: "why", text: "This component needs a helmstudio client. Set window.helm once on the page, or assign element.client." })),
    );
    return false;
  }

  /** fail renders a failure in the component's empty state. */
  fail(into, err, fallback) {
    const unsupported = kindOf(err) === UNSUPPORTED;
    into.replaceChildren(
      el("div", { class: "empty", part: "empty", role: "status" },
        el("p", { text: unsupported ? "Not available here" : "Something failed" }),
        el("p", { class: "why", text: message(err, fallback) })),
    );
  }
}

/** define registers a custom element once, so a page may load the bundle twice. */
export function define(name, ctor) {
  if (!customElements.get(name)) customElements.define(name, ctor);
}

/** bytes renders a byte count the way 03 §15's metadata line does. */
export function bytes(n) {
  if (!Number.isFinite(n) || n < 0) return "";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let i = 0;
  let v = n;
  while (v >= 1000 && i < units.length - 1) {
    v /= 1000;
    i++;
  }
  return (i === 0 ? String(v) : v.toFixed(v < 10 ? 1 : 0)) + " " + units[i];
}

/** clock renders seconds as m:ss, the launcher's elapsed format. */
export function clock(seconds) {
  const s = Math.max(0, Math.floor(seconds || 0));
  return Math.floor(s / 60) + ":" + String(s % 60).padStart(2, "0");
}

/**
 * timecode renders a frame count as HH:MM:SS:FF at the given rate. The frame
 * is authoritative and the timecode derived from it (04 §5), never the other
 * way round, so a rounded time can never move the frame.
 */
export function timecode(frame, fps) {
  const rate = Math.max(1, Math.round(fps || 0) || 1);
  const f = Math.max(0, Math.floor(frame || 0));
  const total = Math.floor(f / rate);
  const two = (n) => String(n).padStart(2, "0");
  return `${two(Math.floor(total / 3600))}:${two(Math.floor(total / 60) % 60)}:${two(total % 60)}:${two(f % rate)}`;
}
