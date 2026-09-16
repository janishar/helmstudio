// The approval screen, as a dialog (03 §13, docs/decisions.md M7 Q10-Q13).
//
// This is the one screen the product should not optimise for clicks. Installing
// a studio is running someone else's code on your Mac with your permissions; a
// pretty installer does not change that, so the obligation is to make the
// decision visible rather than quick.
//
// It is a dialog rather than the full screen 03 §13 draws because M7b draws
// that one. What it must not be is a summary: every command that would run is
// here, verbatim, grouped by when it runs.

import { chip, dialog, el, failure, toast } from "./ui.js";

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
  return el("div", { class: "helm-stack", style: "margin-top: var(--helm-space-3)" },
    el("p", { class: "helm-section-label", text: label }), ...children);
}

/** body is the preview, laid out. Exported so a fixture can draw it. */
export function approvalBody(p) {
  const out = [];

  out.push(el("p", { class: "helm-mono", text: [p.transport, p.commit ? p.commit.slice(0, 7) : null].filter(Boolean).join(" · ") }));

  // Commands, grouped by when they run. The grouping is the point: "this runs
  // every time you launch it" is a different question from "this runs once".
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

  // The checks, including the two that deliberately did not run.
  const checks = p.checks || [];
  if (checks.length) {
    out.push(section("Checks", ...checks.map((c) => el("div", { class: "helm-stack", style: "gap: 0" },
      el("div", { class: "helm-row" },
        chip(c.name, c.state === "pass" ? "running" : c.state === "warn" ? "warning" : c.state === "fail" ? "error" : "idle")),
      c.detail ? el("p", { class: "helm-hint", text: c.detail }) : null))));
  }
  return out;
}

/**
 * chooseCheckpoint is the selectable-weight picker. The choice is made here,
 * before install, because install downloads only the chosen one — iris's five
 * checkpoints are a whole repository each (Q21).
 */
function chooseCheckpoint(p) {
  const selectable = (p.weights || []).filter((w) => w.selectable);
  if (selectable.length < 2) return null;
  const field = el("div", { class: "helm-field", style: "margin-top: var(--helm-space-3)" },
    el("label", { class: "helm-label", for: "helm-checkpoint", text: "Checkpoint" }),
    el("select", { class: "helm-select", id: "helm-checkpoint" },
      ...selectable.map((w) => el("option", { value: w.name, text: w.name, selected: w.name === p.selection }))),
    el("span", { class: "helm-hint", text: "Only this one is downloaded. The others can be fetched later, and the choice can be changed while the studio is stopped." }));
  return field;
}

/**
 * ask shows the preview and resolves to the digest when the person says yes,
 * or null when they do not.
 *
 * `required` failures make the button read "Install anyway" rather than
 * blocking: a required failure warns loudly and blocks a registry merge, but
 * never stops someone installing their own work (R63).
 */
export async function ask(p, verb) {
  const failed = (p.checks || []).some((c) => c.state === "fail" && c.required);
  const extra = chooseCheckpoint(p);

  const chosen = await dialog({
    title: `${verb} ${p.name || p.studio_id}?`,
    body: approvalBody(p),
    extra,
    actions: [
      { label: "Cancel", value: null },
      {
        label: failed ? `${verb} anyway` : verb,
        value: "go",
        class: failed ? "helm-btn-danger-fill" : "helm-btn-primary",
        primary: true,
      },
    ],
  });
  if (chosen !== "go") return null;
  const select = extra && extra.querySelector("select");
  return { approval: p.digest, selection: select ? select.value : p.selection };
}

/**
 * guard runs an operation that needs approval, showing the screen when the
 * daemon asks for one and retrying once with the digest.
 *
 * It asks again on `preview_changed` rather than looping: what would run moved
 * between the screen being drawn and the answer coming back, so the new picture
 * is the one that needs reading.
 */
export async function guard(ctx, studio, verb, run) {
  for (let attempt = 0; attempt < 2; attempt++) {
    try {
      return await run(undefined);
    } catch (err) {
      const preview = err && err.details && err.details.approval;
      if (!preview || !["approval_required", "preview_changed"].includes(err.code)) throw err;

      const answer = await ask(preview, verb);
      if (!answer) return null;
      if (answer.selection && answer.selection !== preview.selection) {
        try {
          await ctx.client.studios.select(studio.id, { weight: answer.selection });
        } catch (e) {
          toast(failure(e, "That checkpoint could not be chosen."), "error");
          return null;
        }
      }
      try {
        return await run(answer.approval);
      } catch (again) {
        if (again && again.code === "preview_changed" && attempt === 0) continue;
        throw again;
      }
    }
  }
  return null;
}
