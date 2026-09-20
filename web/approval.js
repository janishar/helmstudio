// The approval screen (03 §13, docs/decisions.md M7 Q10-Q13, Q21).
//
// The one screen deliberately not optimised for clicks. Installing a studio
// runs someone else's code on your Mac with your permissions; a pretty
// installer does not change that, so the obligation is to make the decision
// visible before it happens rather than quick.
//
// Three things about the layout are decisions, not taste:
//
//   - It is a screen, not a dialog. A dialog is something you dismiss.
//   - It is one column, read top to bottom, with the buttons last in it and
//     pinned to the foot of the window while the column continues below them
//     (03 §13, amended 2026-09-19). They used to be reachable only by
//     scrolling past every command, which made reading a precondition of
//     answering; two and a half windows of screen made that read as a page
//     with no answer on it. What the amendment gives up is written down where
//     it was decided, and it is not nothing.
//   - Every command is here verbatim, grouped by *when* it runs. "This runs
//     every time you launch it" is a different question from "this runs once".
//
// Only checks that execute nothing are run. Theme conformance and the smoke
// test are listed as not run, with the reason: a smoke test builds and runs
// the studio, which is the thing this screen is asking permission for.

import { el, failure } from "./ui.js";

const WHEN = {
  install: "Runs once, when you install it",
  launch: "Runs every time you launch it",
  first_launch: "Runs once, the first time it starts",
};

/** A flag's sentence. What a reader cannot see is the whole reason to say it. */
const FLAG = {
  control_characters: "contains control characters",
  newline: "spans more than one line",
  zero_width: "contains zero-width characters",
  bidirectional: "contains characters that change the direction text reads in",
};

/**
 * escapeInvisible renders a command so that what is shown is what will run.
 *
 * A command carrying a right-to-left override displays as something other than
 * what executes — which is exactly the trick this screen exists to defeat — so
 * the invisible characters are written out as escapes rather than rendered.
 */
export function escapeInvisible(text) {
  let out = "";
  for (const ch of String(text)) {
    const c = ch.codePointAt(0);
    const hidden =
      c < 0x20 || c === 0x7f ||            // control characters
      (c >= 0x200b && c <= 0x200f) ||      // zero-width and directional marks
      c === 0x2028 || c === 0x2029 ||      // line and paragraph separators
      (c >= 0x202a && c <= 0x202e) ||      // bidirectional overrides
      (c >= 0x2066 && c <= 0x2069);        // bidirectional isolates
    if (!hidden) {
      out += ch;
      continue;
    }
    if (ch === "\n") out += "\\n";
    else if (ch === "\t") out += "\\t";
    else if (ch === "\r") out += "\\r";
    else out += "\\u" + c.toString(16).padStart(4, "0");
  }
  return out;
}

/**
 * commandBlock is one command, drawn as a command (03 §13, amended
 * 2026-09-20): the console ground, primary text and a `$` gutter, with the
 * directory it runs in attached to the block rather than floating at the far
 * end of the line above it.
 *
 * It used to be `.helm-step-command` — `--helm-text-muted` at 12px, the token
 * for de-emphasis — on the one screen whose purpose is to make these visible.
 * What is drawn is unchanged: the same string, byte for byte, escaped and
 * flagged. Only the ink is.
 */
function commandBlock(c) {
  const node = el("div", { class: "helm-command" },
    el("div", { class: "helm-command-head" },
      el("span", { class: "helm-command-name", text: c.label || "" }),
      c.cwd ? el("span", { class: "helm-command-where", text: c.cwd }) : null,
      c.shell && c.shell !== "sh" ? el("span", { class: "helm-command-where", text: c.shell }) : null),
    el("pre", { class: "helm-command-text" },
      // The prompt is decoration and never selects, so copying the block
      // copies the command and not a shell prompt with it.
      el("span", { class: "helm-command-gutter", "aria-hidden": "true", text: "$ " }),
      document.createTextNode(escapeInvisible(c.command))));

  for (const f of c.flags || []) {
    node.append(el("p", { class: "helm-hint", text: "This command " + (FLAG[f] || f) + "." }));
  }
  return node;
}

/**
 * section is one labelled part of the column. `key` is what the verdict line
 * scrolls to; a section nothing points at passes "".
 */
function section(label, key, ...children) {
  if (!children.filter(Boolean).length) return null;
  return el("div", { class: "helm-approve-section", "data-section": key || null },
    el("p", { class: "helm-section-label", text: label }), ...children);
}

/**
 * checks is the list 03 §13 leads with, including the two that deliberately
 * did not run. `not_run` is a first-class outcome here and never a pass: a
 * check reported as passing because nobody ran it is worse than no check.
 */
function checks(p) {
  const list = p.checks || [];
  if (!list.length) return null;
  return el("div", { class: "helm-checks" },
    ...list.map((c) => {
      // A pass says its detail on the line it is on; anything else keeps a
      // paragraph and a rule down the side, so the eye finds it first. That
      // is what `not_run` being first-class should have meant all along.
      const quiet = c.state === "pass";
      return el("div", { class: "helm-check" + (quiet ? "" : " helm-check-loud") },
        el("span", { class: "helm-check-dot", "data-state": c.state || "not_run" }),
        // Name and detail share one cell, so a name long enough to wrap
        // wraps under itself rather than pushing the dot onto its own line.
        el("div", { class: "helm-check-body" },
          el("span", { class: "helm-check-name", text: c.name }),
          c.detail ? el("span", { class: "helm-check-detail", text: c.detail }) : null));
    }));
}

/**
 * verdict is the line of counts under the title (03 §13, amended 2026-09-20):
 * an entry point for an eye that had none.
 *
 * Every tile is derived from what is already below it and scrolls to the
 * section that says it in full. A tile that cannot be derived is not drawn —
 * this line never knows anything the screen does not.
 *
 * The tiles are buttons and not links: the launcher is hash-routed, so an
 * `href="#checks"` would be an address, and this one would leave the screen.
 */
function verdict(p) {
  const tiles = [];
  const commands = p.commands || [];
  if (commands.length) {
    const by = (when) => commands.filter((c) => c.when === when).length;
    const parts = [];
    if (by("install")) parts.push(`${by("install")} at install`);
    if (by("first_launch")) parts.push(`${by("first_launch")} on first start`);
    if (by("launch")) parts.push(`${by("launch")} at each launch`);
    tiles.push(tile(count(commands.length, "command"), parts.join(", "), "commands"));
  }

  const selectable = (p.weights || []).filter((w) => w.selectable);
  if (selectable.length > 1) {
    const chosen = p.selection || selectable[0].name;
    const w = selectable.find((x) => x.name === chosen) || selectable[0];
    tiles.push(tile(`1 of ${selectable.length} checkpoints`,
      w.local_path ? `${w.name} · linked, not downloaded` : w.name, "weights"));
  } else if ((p.weights || []).length) {
    tiles.push(tile(count(p.weights.length, "weight"), "it needs to run", "weights"));
  }

  const caps = p.capabilities || [];
  tiles.push(caps.length
    ? tile(count(caps.length, "thing"), "it may do with your library", "capabilities")
    : tile("No services", "it gets no access token", "capabilities"));

  // Whichever of these there is something to say about; a screen where every
  // check ran and passed says so, which is also worth a tile.
  const bad = (p.checks || []).filter((c) => c.state === "fail");
  const held = (p.checks || []).filter((c) => c.state === "not_run");
  if (bad.length) tiles.push(tile(count(bad.length, "check"), "failed", "checks", true));
  else if (held.length) tiles.push(tile(count(held.length, "check"), "could not run before install", "checks", true));
  else if ((p.checks || []).length) tiles.push(tile(count(p.checks.length, "check"), "all ran, all passed", "checks"));

  if (!tiles.length) return null;
  return el("div", { class: "helm-verdict" }, ...tiles);
}

function count(n, word) {
  return `${n} ${word}${n === 1 ? "" : "s"}`;
}

function tile(headline, note, key, warn) {
  return el("button", {
    class: "helm-verdict-tile",
    type: "button",
    onclick: () => {
      const target = document.querySelector(`[data-section="${key}"]`);
      if (!target) return;
      const still = matchMedia("(prefers-reduced-motion: reduce)").matches;
      target.scrollIntoView({ behavior: still ? "auto" : "smooth", block: "start" });
    },
  },
    el("span", { class: "helm-verdict-headline" + (warn ? " helm-verdict-warn" : ""), text: headline }),
    el("span", { class: "helm-verdict-note", text: note }));
}

/** body is the preview, laid out. Exported so a fixture can draw it. */
export function approvalBody(p, onPick = () => {}) {
  const out = [];

  out.push(verdict(p));
  out.push(section("Checks", "checks", checks(p)));

  // Commands, grouped by when they run. The group is one section each, and
  // the verdict line points at the first of them.
  let first = true;
  for (const when of ["install", "first_launch", "launch"]) {
    const group = (p.commands || []).filter((c) => c.when === when);
    if (!group.length) continue;
    out.push(section(WHEN[when], first ? "commands" : "", ...group.map(commandBlock)));
    first = false;
  }

  if ((p.submodules || []).length) {
    out.push(section("It also clones", "",
      ...p.submodules.map((s) => el("p", { class: "helm-mono", text: `${s.path} — ${s.url}` }))));
  }

  if ((p.weights || []).length) {
    // The choice is above the table, not below it (03 §13, amended
    // 2026-09-20): five checkpoints read as five downloads until you reach
    // the picker that says only one of them is fetched.
    out.push(section("Weights", "weights", checkpoint(p, onPick), weightTable(p)));
  }

  const caps = p.capabilities || [];
  out.push(section("What it may do", "capabilities",
    caps.length
      ? el("ul", { class: "helm-may" },
        ...caps.map((c) => el("li", {
          class: c.warning ? "helm-may-warning" : "",
          text: c.sentence,
        })))
      : el("p", { class: "helm-micro", text: "Uses no helmstudio services. It gets no access token." })));

  if ((p.network_hosts || []).length) {
    out.push(section("Network", "network",
      el("p", { class: "helm-micro", text: "The manifest says it contacts these hosts. helmstudio does not restrict network access." }),
      el("div", { class: "helm-hosts" }, ...p.network_hosts.map((h) => el("code", { text: h })))));
  }
  return out;
}

/**
 * weightTable is the weights as one table rather than a stack of pairs.
 *
 * Where the files come from is the difference between a download and a folder
 * this Mac already holds, so it is still said in full for a linked weight
 * rather than implied by a word. What the table cannot say is how big any of
 * this is: `ApprovalWeight` carries `bytes`, the daemon sets it for nothing,
 * and this screen does not estimate. That is an open entry in
 * docs/decisions.md, not an omission here.
 */
function weightTable(p) {
  const selectable = (p.weights || []).filter((w) => w.selectable);
  const chosen = selectable.length > 1 ? (p.selection || selectable[0].name) : null;
  const linked = (p.weights || []).filter((w) => w.local_path);

  return el("div", { class: "helm-stack", style: "gap: var(--helm-space-2)" },
    el("table", { class: "helm-weights" },
      el("thead", {}, el("tr", {},
        el("th", { text: "Weight" }),
        el("th", { text: "Hugging Face" }),
        el("th", { text: "Where the files come from" }))),
      el("tbody", {}, ...(p.weights || []).map((w) => el("tr", { class: w.name === chosen ? "helm-weight-chosen" : "" },
        el("td", {},
          document.createTextNode(w.name),
          w.name === chosen ? el("span", { class: "helm-weight-mark", text: "chosen" }) : null,
          w.optional ? el("span", { class: "helm-weight-note", text: "optional" }) : null),
        el("td", { text: w.hf_repo || "—" }),
        el("td", { class: "helm-weight-source" }, w.local_path
          ? el("span", { text: `Linked from ${w.local_path}` })
          : el("span", { text: chosen && w.selectable && w.name !== chosen ? "Downloaded if chosen" : "Downloaded from Hugging Face" })))))),
    // Said once under the table rather than repeated per row, and only when
    // there is a linked weight for it to be about.
    linked.length
      ? el("p", { class: "helm-hint", text: "helmstudio links the files a local folder names, downloads nothing for it, and never writes to that folder." })
      : null);
}

/**
 * checkpoint is the selectable-weight picker. The choice is made here, before
 * install, because install downloads only the chosen one — iris's five
 * checkpoints are a whole repository each (Q21).
 *
 * It is shown but not covered by the digest: every selectable weight was
 * approved with the manifest, and a digest over the choice would ask for
 * approval again at every switch, which trains people to click through it.
 */
function checkpoint(p, onPick) {
  const selectable = (p.weights || []).filter((w) => w.selectable);
  if (selectable.length < 2) return null;
  const chosen = p.selection || selectable[0].name;
  return el("div", { class: "helm-field helm-checkpoint" },
    el("label", { class: "helm-label", for: "helm-checkpoint", text: "Which checkpoint" }),
    el("select", { class: "helm-select", id: "helm-checkpoint", onchange: (e) => onPick(e.target.value) },
      ...selectable.map((w) => el("option", { value: w.name, text: w.name, selected: w.name === chosen }))),
    el("span", { class: "helm-hint", text: "Only this one is downloaded. The others can be fetched later, and the choice can be changed while the studio is stopped." }));
}

// ----------------------------------------------------------------- consent

/**
 * granted holds the digest a person approved, for the moment between pressing
 * the button and the operation running.
 *
 * It is not a record of consent — the daemon keeps that. It exists because the
 * screen approves and the action runs, and the two need one value between
 * them. It is cleared as soon as it is used, so a second operation cannot
 * inherit a consent given for the first.
 */
const granted = new Map();

export function grant(id, digest) {
  granted.set(id, digest);
}

function take(id) {
  const d = granted.get(id);
  granted.delete(id);
  return d;
}

/**
 * guard runs an operation that needs approval, sending the person to the
 * screen when the daemon asks for one.
 *
 * It does not ask inline and it does not retry silently. `preview_changed`
 * means what would run moved between the screen being drawn and the answer
 * coming back, so the new picture is the one that needs reading — which is the
 * same screen again, not a second attempt with the old answer.
 */
export async function guard(ctx, studio, verb, run) {
  try {
    return await run(take(studio.id));
  } catch (err) {
    if (!err || !["approval_required", "preview_changed"].includes(err.code)) throw err;
    ctx.go(`#/studios/${studio.id}/approve?do=${encodeURIComponent(verb)}`);
    return null;
  }
}

// ------------------------------------------------------------------ screen

const VERBS = { install: "Install", retry: "Retry", launch: "Launch", update: "Update" };

function approvalState(ctx, id) {
  return ctx.keep("approve:" + id, () => ({ preview: null, selection: null, busy: false, error: null, loaded: false }));
}

/**
 * fetchPreview reads what the operation being approved would run.
 *
 * `for` matters: install, retry and launch run the commit an installed studio
 * already has, and an update runs the ref's tip. The digest covers the commit,
 * so a screen built for the wrong one would authorise the wrong thing.
 */
async function fetchPreview(ctx, st, id, verb) {
  st.busy = true;
  try {
    st.preview = await ctx.client.studios.approval(id, { for: verb || "install" });
    st.error = null;
  } catch (err) {
    st.error = failure(err, "What this would run could not be read.");
  }
  st.loaded = true;
  st.busy = false;
  ctx.redraw(true);
}

/**
 * approve records the choice of checkpoint, grants the digest and hands
 * control back to the action the person originally asked for.
 *
 * The screen does not run the operation itself. It knows what was approved,
 * not what to do about it, and an approval screen that also knew how to
 * install would have to know how to launch and how to retry as well.
 */
async function approve(ctx, st, studio, verb) {
  st.busy = true;
  ctx.redraw(true);
  const p = st.preview;
  if (st.selection && st.selection !== p.selection) {
    try {
      await ctx.client.studios.select(studio.id, { weight: st.selection });
    } catch (err) {
      st.error = failure(err, "That checkpoint could not be chosen.");
      st.busy = false;
      ctx.redraw(true);
      return;
    }
  }
  grant(studio.id, p.digest);
  st.loaded = false;
  st.busy = false;
  await ctx.act(studio, verb);
}

/**
 * approvalActions is what this screen puts in the shell's header row (03 §4,
 * §13, both amended 2026-09-20): the manifest itself, as a quiet button.
 *
 * It is a page action and is drawn as one. It used to be an accent link at
 * the right-hand end of the title row, which spent the screen's accent on
 * something that is not the screen's answer and left it 900px from the title
 * it belongs to. Install is the accent here, and nothing else is.
 */
export function approvalActions(ctx, id) {
  return [el("a", {
    class: "helm-btn helm-btn-secondary helm-btn-sm",
    href: `#/edit/${encodeURIComponent(id)}`,
    text: "View manifest",
  })];
}

export function approvalScreen(ctx, id) {
  const st = approvalState(ctx, id);
  const verb = ctx.query.get("do") || "install";
  if (!st.loaded && !st.busy) fetchPreview(ctx, st, id, verb);

  const studio = (ctx.store.studios || []).find((s) => s.id === id) || { id, name: id };
  const back = () => ctx.go("#/studios");

  if (!st.loaded) {
    return el("div", { class: "helm-stack" },
      el("h1", { class: "helm-title", text: `${VERBS[verb] || "Install"} ${studio.name}?` }),
      el("p", { class: "helm-micro", text: "Reading what this would run…" }));
  }
  const p = st.preview;
  if (!p) {
    return el("div", { class: "helm-stack" },
      el("h1", { class: "helm-title", text: `${VERBS[verb] || "Install"} ${studio.name}?` }),
      el("p", { class: "helm-body helm-status-error", text: st.error || "There is nothing to show." }));
  }

  const failed = (p.checks || []).some((c) => c.state === "fail" && c.required);
  const label = VERBS[verb] || "Install";

  // The studio's own hue (03 §2, §13 amended 2026-09-20). It comes from the
  // launcher's library, which has every studio's; the preview carries none
  // and is not asked to, because a hue changes nothing about what runs.
  const h = studio.hue;
  return el("div", { class: "helm-stack helm-approve", style: h && h.dark && h.light
    ? `--_hue-dark: ${h.dark}; --_hue-light: ${h.light}` : null },
    el("div", { class: "helm-page-header" },
      el("span", { class: "helm-approve-dot" }),
      el("h1", { class: "helm-title", text: `${label} ${p.name || studio.name}?` })),

    // The level and the source, as two facts and not a badge — and as plain
    // text, as a studio's row states them (03 §13, amended 2026-09-17). A
    // level is derived from what has been checked; it is never declared in a
    // file, and it is a label rather than a gate on your own machine.
    // The repository and the ref are not repeated here: the source line
    // under it says where the code comes from and how it is fetched, which
    // is the same fact told better.
    el("p", { class: "helm-studio-origin" },
      el("span", { "data-fact": "level", text: LEVEL[p.level] || p.level || "Unverified" }),
      document.createTextNode(" · "),
      el("span", { "data-fact": "source", text: sourceLabel(p.source) })),

    // The transport and the commit: where this code comes from and which of
    // it. It was under the title in mono with nothing marking it as the
    // subject of "View manifest"; it belongs with the level and the source.
    el("p", { class: "helm-approve-source", text:
      [p.transport, p.commit ? p.commit.slice(0, 7) : null].filter(Boolean).join(" · ") }),

    el("div", { class: "helm-approve-rule" }),

    // The one line on this screen set larger than body (03 §13, amended
    // 2026-09-20). It was the size of "Downloaded from Hugging Face."
    el("p", { class: "helm-approve-lede", text:
      `Installing ${p.name || studio.name} runs the commands below on this Mac, with your permissions. helmstudio does not sandbox them.` }),

    st.error ? el("p", { class: "helm-body helm-status-error", text: st.error }) : null,
    p.already_approved
      ? el("p", { class: "helm-hint", text: "You have approved exactly this before. Nothing about it has changed since." })
      : null,

    ...approvalBody(p, (name) => { st.selection = name; }),

    // A required failure is read before the button, not under it: the row
    // below is pinned to the foot of the window, so anything after it in the
    // column would be the one line on this screen that can be missed.
    failed
      ? el("p", { class: "helm-hint", text: "A required check failed. That blocks a registry merge; it never stops you installing your own work." })
      : null,

    // Two buttons, last in the column and pinned to the foot of the window.
    el("div", { class: "helm-row helm-approve-actions" },
      el("button", { class: "helm-btn helm-btn-secondary helm-btn-lg", text: "Cancel", onclick: back }),
      el("span", { class: "helm-spacer" }),
      el("button", {
        class: "helm-btn helm-btn-lg " + (failed ? "helm-btn-danger-fill" : "helm-btn-primary"),
        text: failed ? `${label} anyway` : label,
        disabled: st.busy,
        onclick: () => approve(ctx, st, studio, verb),
      })));
}

const LEVEL = { draft: "Draft", unverified: "Unverified", verified: "Verified", registry: "Registry" };

function sourceLabel(source) {
  return { local: "from a file you wrote", repo: "from a repository", registry: "from the registry" }[source] || "from a repository";
}
