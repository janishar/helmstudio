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
//   - It is one column, read top to bottom, and the buttons are at the bottom.
//     You reach Install by scrolling past every command that would run. A
//     button beside the title would let someone approve a screen they never
//     read, which is the failure this whole screen exists to prevent.
//   - Every command is here verbatim, grouped by *when* it runs. "This runs
//     every time you launch it" is a different question from "this runs once".
//
// Only checks that execute nothing are run. Theme conformance and the smoke
// test are listed as not run, with the reason: a smoke test builds and runs
// the studio, which is the thing this screen is asking permission for.

import { chip, el, failure } from "./ui.js";

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

function commandBlock(c) {
  const node = el("div", { class: "helm-stack", style: "gap: 2px; margin-bottom: var(--helm-space-2)" },
    el("div", { class: "helm-row" },
      el("span", { class: "helm-body", style: "font-weight: 500", text: c.label || "" }),
      el("span", { class: "helm-spacer" }),
      c.cwd ? el("span", { class: "helm-micro", text: "in " + c.cwd }) : null,
      c.shell && c.shell !== "sh" ? el("span", { class: "helm-micro", text: c.shell }) : null),
    el("code", { class: "helm-step-command", style: "display: block", text: escapeInvisible(c.command) }));

  for (const f of c.flags || []) {
    node.append(el("p", { class: "helm-hint", text: "This command " + (FLAG[f] || f) + "." }));
  }
  return node;
}

function section(label, ...children) {
  if (!children.filter(Boolean).length) return null;
  return el("div", { class: "helm-stack", style: "margin-top: var(--helm-space-4)" },
    el("p", { class: "helm-section-label", text: label }), ...children);
}

const CHECK_TONE = { pass: "running", warn: "warning", fail: "error" };

/**
 * checks is the list 03 §13 leads with, including the two that deliberately
 * did not run. `not_run` is a first-class outcome here and never a pass: a
 * check reported as passing because nobody ran it is worse than no check.
 */
function checks(p) {
  const list = p.checks || [];
  if (!list.length) return null;
  return el("div", { class: "helm-stack" },
    ...list.map((c) => el("div", { class: "helm-stack", style: "gap: 0" },
      el("div", { class: "helm-row" }, chip(c.name, CHECK_TONE[c.state] || "idle")),
      c.detail ? el("p", { class: "helm-hint", text: c.detail }) : null)));
}

/** body is the preview, laid out. Exported so a fixture can draw it. */
export function approvalBody(p) {
  const out = [];

  out.push(el("p", { class: "helm-mono", text: [p.transport, p.commit ? p.commit.slice(0, 7) : null].filter(Boolean).join(" · ") }));
  out.push(section("Checks", checks(p)));

  // Commands, grouped by when they run.
  for (const when of ["install", "first_launch", "launch"]) {
    const group = (p.commands || []).filter((c) => c.when === when);
    if (!group.length) continue;
    out.push(section(WHEN[when], ...group.map(commandBlock)));
  }

  if ((p.submodules || []).length) {
    out.push(section("It also clones",
      ...p.submodules.map((s) => el("p", { class: "helm-mono", text: `${s.path} — ${s.url}` }))));
  }

  const caps = p.capabilities || [];
  out.push(section("What it may do",
    caps.length
      ? el("div", { class: "helm-stack", style: "gap: var(--helm-space-1)" },
        ...caps.map((c) => el("p", {
          class: c.warning ? "helm-body" : "helm-micro",
          style: c.warning ? "color: var(--helm-status-warning)" : "",
          text: c.sentence,
        })))
      : el("p", { class: "helm-micro", text: "Uses no helmstudio services. It gets no access token." })));

  if ((p.network_hosts || []).length) {
    out.push(section("Network",
      el("p", { class: "helm-micro", text: "The manifest says it contacts these hosts. helmstudio does not restrict network access." }),
      el("p", { class: "helm-mono", text: p.network_hosts.join(", ") })));
  }

  if ((p.weights || []).length) {
    out.push(section("Weights",
      ...p.weights.map((w) => el("p", { class: "helm-mono", text: [w.name, w.hf_repo, w.selectable ? "selectable" : null, w.optional ? "optional" : null].filter(Boolean).join(" · ") }))));
  }
  return out;
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
  return el("div", { class: "helm-field", style: "margin-top: var(--helm-space-4)" },
    el("label", { class: "helm-label", for: "helm-checkpoint", text: "Checkpoint" }),
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

const VERBS = { install: "Install", retry: "Retry", launch: "Launch" };

function approvalState(ctx, id) {
  return ctx.keep("approve:" + id, () => ({ preview: null, selection: null, busy: false, error: null, loaded: false }));
}

async function fetchPreview(ctx, st, id) {
  st.busy = true;
  try {
    st.preview = await ctx.client.studios.approval(id);
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

export function approvalScreen(ctx, id) {
  const st = approvalState(ctx, id);
  if (!st.loaded && !st.busy) fetchPreview(ctx, st, id);

  const verb = ctx.query.get("do") || "install";
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

  return el("div", { class: "helm-stack helm-approve" },
    el("div", { class: "helm-page-header" },
      el("h1", { class: "helm-title", text: `${label} ${p.name || studio.name}?` }),
      el("span", { class: "helm-spacer" }),
      el("a", { class: "helm-link", href: `#/edit/${encodeURIComponent(id)}`, text: "View manifest" })),

    // The level and the source, as two facts and not a badge — and as plain
    // text, as a studio's row states them (03 §13, amended 2026-09-17). A
    // level is derived from what has been checked; it is never declared in a
    // file, and it is a label rather than a gate on your own machine.
    // The repository and the ref are not repeated here: the transport line
    // below says where the code comes from and how it is fetched, which is
    // the same fact told better.
    el("p", { class: "helm-studio-origin" },
      el("span", { "data-fact": "level", text: LEVEL[p.level] || p.level || "Unverified" }),
      document.createTextNode(" · "),
      el("span", { "data-fact": "source", text: sourceLabel(p.source) })),

    el("p", { class: "helm-body", text:
      `Installing ${p.name || studio.name} runs the commands below on this Mac, with your permissions. helmstudio does not sandbox them.` }),

    st.error ? el("p", { class: "helm-body helm-status-error", text: st.error }) : null,
    p.already_approved
      ? el("p", { class: "helm-hint", text: "You have approved exactly this before. Nothing about it has changed since." })
      : null,

    ...approvalBody(p),
    checkpoint(p, (name) => { st.selection = name; }),

    // Two buttons, at the bottom, after everything above.
    el("div", { class: "helm-row helm-approve-actions" },
      el("button", { class: "helm-btn helm-btn-secondary helm-btn-lg", text: "Cancel", onclick: back }),
      el("span", { class: "helm-spacer" }),
      el("button", {
        class: "helm-btn helm-btn-lg " + (failed ? "helm-btn-danger-fill" : "helm-btn-primary"),
        text: failed ? `${label} anyway` : label,
        disabled: st.busy,
        onclick: () => approve(ctx, st, studio, verb),
      })),
    failed
      ? el("p", { class: "helm-hint", text: "A required check failed. That blocks a registry merge; it never stops you installing your own work." })
      : null);
}

const LEVEL = { draft: "Draft", unverified: "Unverified", verified: "Verified", registry: "Registry" };

function sourceLabel(source) {
  return { local: "from a file you wrote", repo: "from a repository", registry: "from the registry" }[source] || "from a repository";
}
