// <helm-terminal job="jb_01J…" follow>
//
// A log view every studio would otherwise rebuild badly (04 §5): a ring buffer
// so a diffusion loop emitting thirty lines a second never accumulates a
// hundred thousand DOM nodes, ANSI colour as spans, carriage-return progress
// lines rewriting in place, follow that releases when the user scrolls, copy,
// wrap, jump-to-latest, and reconnection from the last event it saw.
//
// Structural interface (04 §5, M6 Q12). It needs one method:
//
//   logs(ref, { lastEventId }) → async iterable of { id, name, data, json() }
//
// with events named `line` (text, and step_index on an install), `step` (a new
// step begins), `gap` (what this viewer lost) and `end` (nothing more is
// coming, with the reason). The runtime SDK's `jobs` group is one such source
// and the launcher client's is another, which is how the launcher's install
// and process screens use the same terminal a studio does.
//
// A page that wants something else — process logs, which take a studio and a
// process name — sets `.source` to an object with a `logs({lastEventId})` of
// its own. The component still never names a path.

import { HelmElement, define, el } from "./base.js";

/** How many lines the buffer keeps. Older lines are dropped, and said so. */
const CAP = 5000;

/** Rows drawn above and below the viewport, so a fast scroll is not blank. */
const OVERSCAN = 12;

/** How long to wait before resubscribing after a stream drops. */
const RETRY_MS = 1000;

const styles = `
  :host { height: 100%; }
  .view {
    position: relative;
    flex: 1 1 auto;
    min-height: 0;
    overflow: auto;
    background: var(--helm-ground-inset);
    color: var(--helm-log-text);
    font: var(--helm-type-log);
    font-variant-numeric: tabular-nums;
    padding: var(--helm-space-2) 0;
  }
  .sizer { position: relative; width: 100%; }
  .rows { position: absolute; left: 0; right: 0; top: 0; }
  .row {
    padding: 0 var(--helm-space-3);
    white-space: pre;
    min-height: var(--_helm-line, 17px);
  }
  :host([wrap]) .row { white-space: pre-wrap; overflow-wrap: anywhere; }
  .step {
    color: var(--helm-log-accent);
    padding: var(--helm-space-2) var(--helm-space-3) 0;
    white-space: pre-wrap;
  }
  .gap { color: var(--helm-log-muted); font-style: italic; }
  .err { color: var(--helm-log-error); }
  .warn { color: var(--helm-log-warning); }
  .accent { color: var(--helm-log-accent); }
  .dim { color: var(--helm-log-muted); }
  .bold { font-weight: 600; }
  .latest {
    position: absolute;
    right: var(--helm-space-4);
    bottom: var(--helm-space-3);
    background: var(--helm-ground-raised);
    border-color: var(--helm-border-strong);
    color: var(--helm-text-primary);
  }
  .foot {
    display: flex;
    align-items: center;
    gap: var(--helm-space-2);
    padding: var(--helm-space-1) var(--helm-space-3);
    border-top: 1px solid var(--helm-border-hairline);
    background: var(--helm-ground-raised);
    flex: 0 0 auto;
  }
`;

/**
 * The sixteen ANSI colours collapse onto the four the design gives a log
 * (03 §2a). Red is an error, yellow a warning, everything else that is not
 * plain text is the accent; dim is muted. A log has no palette of its own —
 * it wears the theme's.
 */
const SGR = {
  1: "bold", 2: "dim",
  31: "err", 91: "err",
  33: "warn", 93: "warn",
  32: "accent", 92: "accent",
  34: "accent", 94: "accent",
  35: "accent", 95: "accent",
  36: "accent", 96: "accent",
};

const ANSI = /\x1b\[([0-9;]*)m/g;

/**
 * spans splits one line into { text, cls } runs. Escape sequences that are not
 * SGR are removed rather than shown, because a cursor movement in a log panel
 * is noise either way.
 */
export function spans(text) {
  const out = [];
  let classes = new Set();
  let last = 0;
  ANSI.lastIndex = 0;
  let m;
  while ((m = ANSI.exec(text)) !== null) {
    if (m.index > last) out.push({ text: text.slice(last, m.index), cls: [...classes].join(" ") });
    last = ANSI.lastIndex;
    const codes = m[1] === "" ? [0] : m[1].split(";").map((c) => parseInt(c, 10) || 0);
    for (const code of codes) {
      if (code === 0) classes = new Set();
      else if (SGR[code]) classes.add(SGR[code]);
      else if (code === 22) { classes.delete("bold"); classes.delete("dim"); }
      else if (code === 39) { classes.delete("err"); classes.delete("warn"); classes.delete("accent"); }
    }
  }
  if (last < text.length) out.push({ text: text.slice(last), cls: [...classes].join(" ") });
  return out.length ? out : [{ text: "", cls: "" }];
}

/** strip removes every escape sequence, for Copy and for measuring. */
export function strip(text) {
  // eslint-disable-next-line no-control-regex
  return text.replace(/\x1b\[[0-9;]*[A-Za-z]/g, "");
}

/**
 * progress reduces a carriage-return line to what a terminal would show.
 *
 * A carriage return means "go back to the start of the line", so what is on
 * screen afterwards is what follows the last one — unless the line ends with
 * it, which is how a progress writer says "this is the line, and I will write
 * over it next". Both shapes arrive: `25%\r50%\r75%` in one read, and `25%\r`
 * as its own line when the daemon split on it.
 *
 * Either way the line rewrites, so it replaces the previous rewriting line
 * rather than adding a four-hundredth row.
 */
export function progress(text) {
  const rewrites = text.includes("\r");
  const body = text.replace(/\r+$/, "");
  const i = body.lastIndexOf("\r");
  return { text: i < 0 ? body : body.slice(i + 1), rewrites };
}

export class HelmTerminal extends HelmElement {
  static get observedAttributes() {
    return ["job", "follow"];
  }

  constructor() {
    super(styles);
    /** The ring buffer: { text, kind } where kind is line | step | gap. */
    this.lines = [];
    this.dropped = 0;
    this.following = false;
    this.lineHeight = 17;
    this.build();
  }

  build() {
    this.rows = el("div", { class: "rows", part: "rows" });
    this.sizer = el("div", { class: "sizer" }, this.rows);
    this.view = el("div", {
      class: "view",
      part: "view",
      tabindex: "0",
      role: "log",
      "aria-label": "Output",
      "aria-live": "off",
      onscroll: () => this.onScroll(),
    }, this.sizer);

    this.jump = el("button", {
      class: "latest",
      hidden: true,
      part: "jump",
      text: "Jump to latest",
      onclick: () => { this.following = true; this.scrollToEnd(); this.paint(); },
    });

    this.count = el("span", { class: "micro", part: "count" });
    this.state = el("span", { class: "micro", part: "state" });
    this.followBtn = el("button", {
      part: "follow",
      text: "Follow",
      "aria-pressed": "true",
      onclick: () => { this.following = !this.following; if (this.following) this.scrollToEnd(); this.paint(); },
    });
    this.wrapBtn = el("button", {
      part: "wrap",
      text: "Wrap",
      "aria-pressed": "false",
      onclick: () => { this.toggleAttribute("wrap"); this.wrapBtn.setAttribute("aria-pressed", String(this.hasAttribute("wrap"))); this.paint(); },
    });
    this.copyBtn = el("button", { part: "copy", text: "Copy", onclick: () => this.copy() });

    const panel = el("div", { class: "panel" },
      el("div", { class: "head" },
        el("span", { class: "label", text: "Output" }),
        this.count,
        el("span", { class: "spacer" }),
        this.followBtn, this.wrapBtn, this.copyBtn),
      el("div", { style: "position: relative; display: flex; flex: 1 1 auto; min-height: 0" }, this.view, this.jump),
      el("div", { class: "foot" }, this.state));
    this.mountPanel(panel);
  }

  connectedCallback() {
    // The line box the theme actually renders, rather than a number guessed
    // here: a token change must move the rows with it.
    const probe = el("div", { class: "row", text: "M" });
    this.rows.append(probe);
    this.lineHeight = probe.getBoundingClientRect().height || this.lineHeight;
    probe.remove();
    this.style.setProperty("--_helm-line", this.lineHeight + "px");
    // `follow` is what the design writes on the element, and it is what turns
    // following on. Without it the view stays where the reader put it.
    this.following = this.hasAttribute("follow");
    this.observer = new ResizeObserver(() => this.paint());
    this.observer.observe(this.view);
    this.onCleanup(() => this.observer.disconnect());
    // A page that moves this element — a launcher redrawing its screen, a
    // studio switching tabs — disconnects and reconnects it. That must not
    // read as a new log: the buffer stays, and the stream picks up from the
    // last event this viewer saw rather than from the top.
    if (this.lines.length) this.resume();
    else this.reload();
  }

  attributeChangedCallback(name, before, after) {
    if (before === after) return;
    if (name === "job" && this.isConnected) this.reload();
    if (name === "follow") this.following = this.hasAttribute("follow");
  }

  /** source is how a page supplies logs the `jobs` group does not cover. */
  get source() {
    return this._source || null;
  }

  set source(s) {
    // Being handed the same source again — which a page redrawing its screen
    // will do — must not restart the log.
    const same = this._source && s && this._source.key !== undefined && this._source.key === s.key;
    this._source = s;
    if (this.isConnected && !same) this.reload();
  }

  /** The log source: an explicit one, else the client's jobs group. */
  resolveSource() {
    if (this._source) return this._source;
    const job = this.getAttribute("job");
    const client = this.client;
    if (!job || !client || !client.jobs || typeof client.jobs.logs !== "function") return null;
    return { logs: (params) => client.jobs.logs(job, params) };
  }

  reload() {
    this.lines = [];
    this.dropped = 0;
    this.lastEventId = undefined;
    this.resume();
  }

  /** resume subscribes without discarding what is already on screen. */
  resume() {
    this.stop();
    this.ended = false;
    this.sizer.style.height = "";
    const source = this.resolveSource();
    if (!source) {
      this.state.textContent = this.getAttribute("job") || this._source
        ? "Waiting for a client."
        : "No job.";
      this.paint();
      return;
    }
    this.state.textContent = "Connecting…";
    this.run(source);
  }

  stop() {
    if (this.abort) this.abort();
    this.abort = null;
    if (this.retry) clearTimeout(this.retry);
    this.retry = null;
  }

  disconnectedCallback() {
    this.stop();
    super.disconnectedCallback();
  }

  /**
   * run consumes the stream until it ends or drops. A drop is not a failure —
   * the daemon restarts, a laptop sleeps — so it resubscribes from the last
   * event it saw rather than starting the log again.
   */
  async run(source) {
    const generation = (this.generation = (this.generation || 0) + 1);
    let stopped = false;
    this.abort = () => { stopped = true; };
    this.onCleanup(() => { stopped = true; });
    try {
      const stream = await source.logs({ lastEventId: this.lastEventId });
      if (stopped || generation !== this.generation) return;
      this.state.textContent = "Streaming.";
      for await (const ev of stream) {
        if (stopped || generation !== this.generation) return;
        if (ev.id) this.lastEventId = ev.id;
        this.handle(ev);
      }
      if (stopped || generation !== this.generation) return;
      if (!this.ended) this.reconnect(source, "The stream stopped.");
    } catch (err) {
      if (stopped || generation !== this.generation) return;
      if (this.lines.length === 0) {
        this.rows.replaceChildren();
        this.sizer.style.height = "auto";
        this.fail(this.region, err, "The log could not be read.");
        this.state.textContent = "Not streaming.";
        return;
      }
      this.reconnect(source, "Reconnecting…");
    }
  }

  reconnect(source, why) {
    this.state.textContent = why;
    this.retry = setTimeout(() => {
      if (this.isConnected) this.run(source);
    }, RETRY_MS);
  }

  handle(ev) {
    switch (ev.name) {
      case "line": {
        let data;
        try {
          data = ev.json();
        } catch {
          data = { text: ev.data };
        }
        this.append(String(data && data.text !== undefined ? data.text : ""));
        break;
      }
      case "step": {
        const s = ev.json() || {};
        this.push({ kind: "step", text: `==> ${s.step_name || ""}${s.command ? "  " + s.command : ""}` });
        break;
      }
      case "gap": {
        const g = ev.json() || {};
        const n = g.gap;
        const unit = g.gap_unit || "lines";
        this.push({ kind: "gap", text: n ? `[${n} ${unit} not shown]` : "[output not shown]" });
        break;
      }
      case "end": {
        const e = ev.json() || {};
        this.ended = true;
        const err = e.last_error;
        this.state.textContent = e.reason || (err ? `${e.state || "failed"}: ${err.message || err.code}` : e.state || "Ended.");
        break;
      }
      default:
        break;
    }
    this.paint();
  }

  append(text) {
    const p = progress(text);
    const last = this.lines[this.lines.length - 1];
    if (p.rewrites && last && last.kind === "line" && last.rewrites) {
      last.text = p.text;
      return;
    }
    this.push({ kind: "line", text: p.text, rewrites: p.rewrites });
  }

  push(line) {
    this.lines.push(line);
    if (this.lines.length > CAP) {
      this.dropped += this.lines.length - CAP;
      this.lines.splice(0, this.lines.length - CAP);
    }
  }

  onScroll() {
    // Follow releases the moment the user scrolls away, and takes hold again
    // when they come back to the bottom — never by stealing the scroll.
    const atEnd = this.view.scrollHeight - this.view.scrollTop - this.view.clientHeight <= this.lineHeight;
    if (this.following !== atEnd) {
      this.following = atEnd;
      this.paint();
    }
  }

  scrollToEnd() {
    this.view.scrollTop = this.view.scrollHeight;
  }

  /**
   * paint draws only the rows the viewport can show. The sizer keeps the
   * scrollbar honest about the whole buffer.
   */
  paint() {
    const n = this.lines.length;
    this.sizer.style.height = n * this.lineHeight + "px";
    const top = this.view.scrollTop;
    const height = this.view.clientHeight || 1;
    let first = Math.max(0, Math.floor(top / this.lineHeight) - OVERSCAN);
    let lastRow = Math.min(n, Math.ceil((top + height) / this.lineHeight) + OVERSCAN);
    if (this.following) {
      lastRow = n;
      first = Math.max(0, n - Math.ceil(height / this.lineHeight) - OVERSCAN);
    }
    this.rows.style.top = first * this.lineHeight + "px";
    const out = [];
    for (let i = first; i < lastRow; i++) {
      const line = this.lines[i];
      const row = el("div", { class: "row" + (line.kind === "step" ? " step" : line.kind === "gap" ? " gap" : "") });
      if (line.kind === "line") {
        for (const s of spans(line.text)) row.append(s.cls ? el("span", { class: s.cls, text: s.text }) : document.createTextNode(s.text));
      } else {
        row.textContent = line.text;
      }
      out.push(row);
    }
    this.rows.replaceChildren(...out);
    this.count.textContent = n === 0 ? "" : `${n.toLocaleString()} line${n === 1 ? "" : "s"}${this.dropped ? ` · ${this.dropped.toLocaleString()} dropped` : ""}`;
    this.followBtn.setAttribute("aria-pressed", String(this.following));
    this.jump.hidden = this.following || n === 0;
    if (this.following) this.scrollToEnd();
  }

  text() {
    return this.lines.map((l) => strip(l.text)).join("\n");
  }

  async copy() {
    const text = this.text();
    try {
      await navigator.clipboard.writeText(text);
      this.copyBtn.textContent = "Copied";
    } catch {
      // Clipboard access can be refused; saying so beats a button that lies.
      this.copyBtn.textContent = "Press ⌘C";
      const sel = window.getSelection();
      const range = document.createRange();
      range.selectNodeContents(this.rows);
      sel.removeAllRanges();
      sel.addRange(range);
    }
    setTimeout(() => { this.copyBtn.textContent = "Copy"; }, 1500);
  }
}

define("helm-terminal", HelmTerminal);
