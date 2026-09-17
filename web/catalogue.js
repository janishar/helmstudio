// The library (03 §6, §13a): the grid of studio cards.
//
// The catalogue is a library, not an install list. Every id this machine knows
// about is here — installed or not, valid or not — because a studio whose
// override has a typo in it vanishing from the screen is the worst possible
// outcome of a typo. It is listed, with what is wrong and a way to fix it.
//
// Card anatomy: a 3px identity stripe, the title with a kind badge, the three
// facts, the description in full, a mono facts line, then the state chip
// and the primary action. During a download only the lower rows swap — the
// title and the stripe never move, so the grid does not reflow while four
// cards tick.
//
// **Source, certification level and install state are three independent facts
// and the card shows all three.** A Local entry can be Verified if it passes
// the harness; a Registry entry is still only a description until someone
// installs it. Collapsing them into one badge would be the product deciding
// that where a file came from is the same question as whether it works.

import { announce, bytes, chip, dialog, el, facts, failure, progress, state, toast } from "./ui.js";

/**
 * The identity hue a card's stripe wears (03 §6, M6 Q7).
 *
 * `Studio` does not carry `hue`, so every card falls back to the accent today
 * — see the finding in M6b's report. This reads the field the API would serve,
 * so that adding it is a one-line change here rather than a search for where
 * the stripe is drawn.
 */
function hue(studio) {
  return studio.hue ? `--_helm-hue: ${studio.hue}` : "";
}

/** The chip a source wears, and what it means (03 §13a's table). */
const SOURCE = {
  local: { label: "Local", means: "You wrote it." },
  repo: { label: "From repo", means: "The studio's author ships helmstudio.yaml." },
  registry: { label: "Registry", means: "Curated and reviewed at a pinned ref." },
  running: { label: "Running", means: "Still running from a previous daemon." },
};

/** A level is derived from what has been checked, never declared in a file. */
const LEVEL = {
  draft: "Draft",
  unverified: "Unverified",
  verified: "Verified",
  registry: "Registry",
};

/** withTitle puts the table's sentence on the chip that stands for it. */
function withTitle(node, text) {
  node.setAttribute("title", text);
  return node;
}

function actionButton(ctx, studio, action, primary) {
  if (!action) return null;
  return el("button", {
    class: "helm-btn " + (primary ? "helm-btn-primary" : "helm-btn-secondary") + " helm-btn-sm",
    text: action.label,
    onclick: () => ctx.act(studio, action.action),
  });
}

/** where a Local entry came from: a note, and never a trust signal. */
function provenance(studio) {
  const p = studio.provenance;
  if (!p) return null;
  const text = {
    imported: p.url ? `Imported from ${p.url}` : "Imported",
    duplicated: p.from_id ? `Duplicated from ${p.from_id}` : "Duplicated",
    written: "Written here",
    folder: p.path ? `Read from ${p.path}` : "Read from a folder",
  }[p.kind];
  return text ? el("p", { class: "helm-micro", text }) : null;
}

/**
 * manage is Override, Revert and Duplicate.
 *
 * Override reads "Edit" once the entry is already yours: both open the same
 * screen, and the word is the one that describes what saving will do. Revert
 * is offered only where there is something underneath to go back to.
 */
function manage(ctx, studio) {
  const local = studio.source === "local";
  const row = el("div", { class: "helm-row helm-card-manage" },
    el("button", {
      class: "helm-btn helm-btn-ghost helm-btn-sm",
      text: local ? "Edit" : "Override",
      title: local ? "Edit your copy" : "Write your own version, which takes precedence over this one",
      onclick: () => ctx.go(`#/edit/${encodeURIComponent(studio.id)}`),
    }),
    el("button", {
      class: "helm-btn helm-btn-ghost helm-btn-sm", text: "Duplicate",
      onclick: () => duplicate(ctx, studio),
    }));
  if (local && studio.overrides) {
    row.append(el("button", {
      class: "helm-btn helm-btn-ghost helm-btn-sm", text: "Revert",
      title: `Go back to the ${studio.overrides} version`,
      onclick: () => revert(ctx, studio),
    }));
  }
  return row;
}

async function revert(ctx, studio) {
  const chosen = await dialog({
    title: `Revert ${studio.name} to the ${studio.overrides} version?`,
    body: [
      el("p", { class: "helm-body", text: `Your own ${studio.id}.yaml is moved aside, not deleted, and the ${studio.overrides} version resolves again.` }),
      el("p", { class: "helm-micro", text: "Nothing that is installed is touched. If the two disagree about the build steps, the studio will want rebuilding." }),
    ],
    actions: [
      { label: "Cancel", value: null },
      { label: "Revert", value: "go", class: "helm-btn-danger-fill", primary: true },
    ],
  });
  if (chosen !== "go") return;
  try {
    const res = await ctx.client.manifests.revert(studio.id);
    toast(`Moved to ${res.moved_to}.`, "info");
  } catch (err) {
    toast(failure(err, `${studio.name} could not be reverted.`), "error");
  }
  return ctx.refresh();
}

async function duplicate(ctx, studio) {
  const input = el("input", { class: "helm-input", type: "text", value: `${studio.id}-mine`,
    "aria-label": "The new studio's id" });
  const chosen = await dialog({
    title: `Duplicate ${studio.name}`,
    body: [
      el("p", { class: "helm-body", text: "A copy in your own library, under a new id. Its comments and key order are kept; only the id changes." }),
      el("div", { class: "helm-field" }, el("label", { class: "helm-label", text: "New id" }), input),
    ],
    actions: [
      { label: "Cancel", value: null },
      { label: "Duplicate", value: "go", class: "helm-btn-primary", primary: true },
    ],
  });
  if (chosen !== "go") return;
  const newID = input.value.trim();
  try {
    await ctx.client.manifests.duplicate(studio.id, { new_id: newID });
    await ctx.refresh();
    ctx.go(`#/edit/${encodeURIComponent(newID)}`);
  } catch (err) {
    toast(failure(err, `${studio.name} could not be duplicated.`), "error");
  }
}

/**
 * checkpoint is the choice of which selectable weight a studio launches with,
 * on the card, so it can be made before pressing Launch (M7 Q21).
 *
 * The approval screen offers it too — but only when an approval is needed. A
 * studio whose approval is current launches without showing that screen, and a
 * choice that is reachable only when something else has changed is not really
 * a choice. The selection is not covered by the digest, so making it here asks
 * for nothing.
 *
 * Disabled while the studio runs or has work in flight: the running process was
 * handed a path when it started, and the daemon refuses the change rather than
 * let the record disagree with what is loaded.
 */
function checkpoint(ctx, studio) {
  const list = studio.selectable || [];
  if (list.length < 2) return null;
  const busy = ["starting", "running", "stopping"].includes((studio.group || {}).state) || !!studio.job_id;
  const id = `checkpoint-${studio.id}`;
  return el("div", { class: "helm-row helm-card-checkpoint" },
    el("label", { class: "helm-micro", for: id, text: "Checkpoint" }),
    el("select", {
      class: "helm-select", id, disabled: busy,
      title: busy ? "Stop the studio to change its checkpoint" : null,
      onchange: async (e) => {
        const weight = e.target.value;
        try {
          await ctx.client.studios.select(studio.id, { weight });
          announce(`${studio.name} will launch with ${weight}.`);
        } catch (err) {
          // Not downloaded is the usual answer, and the daemon's sentence
          // names :fetch and :link, which is what to do about it.
          toast(failure(err, `${weight} could not be chosen.`), "error");
        }
        await ctx.refresh();
        ctx.redraw(true);
      },
    },
      studio.selection ? null : el("option", { value: "", text: "— choose —", selected: true, disabled: true }),
      ...list.map((w) => el("option", { value: w.name, text: w.name, selected: w.name === studio.selection }))));
}

/** An invalid entry says what is wrong with it, because only that is fixable. */
function invalid(studio) {
  const errors = studio.errors || [];
  const box = el("div", { class: "helm-stack", style: "gap: 2px" },
    el("p", { class: "helm-body helm-status-error", text: "This manifest does not validate, so the studio cannot be installed or launched." }));
  for (const e of errors.slice(0, 3)) {
    box.append(el("p", { class: "helm-hint", text: [e.line ? `line ${e.line}` : e.pointer, e.message].filter(Boolean).join(": ") }));
  }
  if (errors.length > 3) box.append(el("p", { class: "helm-hint", text: `and ${errors.length - 3} more.` }));
  return box;
}

export function card(ctx, studio) {
  const job = (ctx.store.jobs || {})[studio.id] || null;
  const s = state(studio, job);
  const src = SOURCE[studio.source];
  const valid = studio.manifest_valid !== false;

  const lower = el("div", { class: "helm-stack" },
    el("p", { class: "helm-meta", text: facts(studio) || "—" }));

  // Only this block changes while an install runs, which is why it is the one
  // that carries the bar and the numbers. A progress bar never carries text
  // inside it: the number sits in the mono line beneath (03 §15).
  if (job && job.progress_den && ["running", "queued"].includes(job.state)) {
    lower.append(progress(job.progress_num, job.progress_den));
    lower.append(el("p", { class: "helm-mono", text: job.kind === "download"
      ? `${bytes(job.progress_num)} of ${bytes(job.progress_den)}`
      : `step ${job.progress_num} of ${job.progress_den}` }));
  }

  return el("article", {
    class: "helm-card",
    style: hue(studio),
    "aria-label": studio.name,
  },
    el("span", { class: "helm-card-stripe" }),
    el("div", { class: "helm-row" },
      el("h3", { class: "helm-card-title", text: studio.name }),
      (studio.kinds || []).length ? el("span", { class: "helm-kind", text: studio.kinds.join("+") }) : null),

    // Two of the three facts. The third is the state chip below, beside the
    // action it governs.
    el("div", { class: "helm-row helm-card-facts" },
      src ? withTitle(chip(src.label, "idle"), src.means) : null,
      studio.level ? chip(LEVEL[studio.level] || studio.level, "idle") : null,
      studio.overrides ? el("span", { class: "helm-micro", text: `overrides ${studio.overrides}` }) : null),

    el("p", { class: "helm-card-desc", text: studio.description || "" }),
    valid ? lower : invalid(studio),
    provenance(studio),
    studio.rebuild_needed && studio.rebuild_needed_reason
      ? el("p", { class: "helm-hint helm-status-warning", text: studio.rebuild_needed_reason })
      : null,
    // Only where an approval could already exist. A studio that is not
    // installed has never been approved, and telling someone that what it
    // would run "has changed" when they have never seen it is a sentence
    // about nothing.
    studio.approval_required && studio.install_state && studio.install_state !== "listed"
      ? el("p", { class: "helm-hint helm-status-warning", text: "What this would run has changed since you approved it. The next install or launch will show it again." })
      : null,

    valid ? checkpoint(ctx, studio) : null,
    el("div", { class: "helm-card-actions" },
      chip(s.chip, s.tone),
      el("span", { class: "helm-spacer" }),
      valid ? actionButton(ctx, studio, s.secondary, false) : null,
      valid ? actionButton(ctx, studio, s.primary, true) : null),
    s.note ? el("p", { class: "helm-hint", text: s.note }) : null,
    manage(ctx, studio));
}

export function catalogue(ctx) {
  const studios = ctx.store.studios;
  const installed = studios.filter((s) => ["ready", "update_available"].includes(s.install_state)).length;
  const onDisk = studios.reduce((n, s) => n + (s.size_bytes || 0), 0);

  return el("div", { class: "helm-stack" },
    el("div", { class: "helm-page-header" },
      el("h1", { class: "helm-title", text: "Studios" }),
      el("span", { class: "helm-meta", text: `${studios.length} studio${studios.length === 1 ? "" : "s"} · ${installed} installed · ${bytes(onDisk)} on disk` }),
      el("span", { class: "helm-spacer" }),
      el("a", { class: "helm-btn helm-btn-primary helm-btn-sm", href: "#/add", text: "Add a studio" })),
    studios.length
      ? el("div", { class: "helm-grid-cards" }, ...studios.map((s) => card(ctx, s)))
      : el("div", { class: "helm-panel" },
        el("div", { class: "helm-panel-body helm-stack" },
          el("p", { class: "helm-body", text: "No studios yet." }),
          el("p", { class: "helm-micro", text: "Add one from a repository, from a folder, or by writing a manifest." }),
          el("a", { class: "helm-btn helm-btn-primary helm-btn-sm", href: "#/add", text: "Add a studio" }))));
}
