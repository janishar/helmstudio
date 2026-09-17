// The library (03 §6, §13a): every studio, one row each.
//
// The catalogue is a library, not an install list. Every id this machine knows
// about is here — installed or not, valid or not — because a studio whose
// override has a typo in it vanishing from the screen is the worst possible
// outcome of a typo. It is listed, with what is wrong and a way to fix it.
//
// A row (03 §6, amended 2026-09-17): the identity stripe; on the left the
// title, its kind, where it comes from and how far it is checked, the
// description in full and the facts line; on the right the state chip with the
// actions beneath it. During a download only the facts line swaps for the bar
// — the title, the stripe and the description never move, so the list does not
// reflow while a row ticks.
//
// **Source, certification level and install state are three independent facts
// and the row shows all three.** A Local entry can be Verified if it passes
// the harness; a Registry entry is still only a description until someone
// installs it. Collapsing them into one badge would be the product deciding
// that where a file came from is the same question as whether it works.

import { announce, bytes, chip, dialog, el, facts, failure, menu, progress, saveText, state, toast } from "./ui.js";

/**
 * The identity hue a row's stripe wears, for both themes (03 §2, amended
 * 2026-09-17). The daemon always serves one, so the stripe never falls back to
 * the accent; launcher.css picks the theme's.
 */
function hue(studio) {
  const h = studio.hue;
  return h && h.dark && h.light ? `--_hue-dark: ${h.dark}; --_hue-light: ${h.light}` : undefined;
}

/** How a source reads, and what it means (03 §13a's table). */
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

/**
 * origin is the source and the level as plain text — two facts, each its own
 * element, and neither a chip: two chips beside the state chip read as three
 * states.
 */
function origin(studio) {
  const src = SOURCE[studio.source];
  const parts = [];
  if (src) parts.push(el("span", { "data-fact": "source", title: src.means, text: src.label }));
  if (studio.level) parts.push(el("span", { "data-fact": "level", text: LEVEL[studio.level] || studio.level }));
  if (studio.overrides) parts.push(el("span", { text: `overrides ${studio.overrides}` }));
  if (!parts.length) return null;
  const out = [];
  parts.forEach((p, i) => {
    if (i) out.push(document.createTextNode(" · "));
    out.push(p);
  });
  return el("span", { class: "helm-studio-origin" }, ...out);
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
    id: "duplicate-id" });
  const chosen = await dialog({
    title: `Duplicate ${studio.name}`,
    body: [
      el("p", { class: "helm-body", text: "A copy in your own library, under a new id. Its comments and key order are kept; only the id changes." }),
      el("div", { class: "helm-field" }, el("label", { class: "helm-label", for: "duplicate-id", text: "New id" }), input),
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

/** exportManifest hands back the file the library holds, to share or commit. */
async function exportManifest(ctx, studio) {
  try {
    const m = await ctx.client.studios.manifest(studio.id);
    saveText(`${studio.id}.yaml`, m.text || "");
    toast(`${studio.id}.yaml downloaded.`, "info");
  } catch (err) {
    toast(failure(err, `${studio.name}'s manifest could not be exported.`), "error");
  }
}

/**
 * more is the row's ⋯ menu: what is done rarely. Edit reads "Override" for an
 * entry that is not yours yet — both open the same screen, and the word is the
 * one that says what saving will do. Revert is offered only where there is
 * something underneath to go back to.
 *
 * An invalid entry's Edit is its main action rather than an item here, since
 * it is the only thing that helps.
 */
function more(ctx, studio, valid) {
  const local = studio.source === "local";
  const edit = () => ctx.go(`#/edit/${encodeURIComponent(studio.id)}`);
  if (studio.source === "running") return [];
  return menu(`more-${studio.id}`, `More actions for ${studio.name}`, [
    valid ? { label: local ? "Edit" : "Override", run: edit } : null,
    valid ? { label: "Duplicate", run: () => duplicate(ctx, studio) } : null,
    local && studio.overrides ? { label: "Revert", run: () => revert(ctx, studio) } : null,
    { label: "Export", run: () => exportManifest(ctx, studio) },
  ]);
}

/**
 * checkpoint is the choice of which selectable weight a studio launches with,
 * on the row, so it can be made before pressing Launch (M7 Q21).
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

/**
 * missing is a linked weights folder that is not there: a drive unplugged, or
 * a folder moved. The studio cannot launch until it is back, and the row says
 * so before Launch does.
 */
function missing(ctx, studio) {
  const gone = (ctx.store.models || []).filter((m) => m.state === "missing" && (m.studios || []).includes(studio.id));
  if (!gone.length) return null;
  const where = gone.map((m) => m.external_path || m.hf_repo).join(", ");
  return el("p", { class: "helm-hint helm-status-warning", text:
    `The linked folder ${where} is not there. Reconnect the drive, or link or download the weights again, before launching.` });
}

/** The row's work in flight: a bar, and the numbers under it (03 §15). */
function inFlight(job) {
  if (!job || !job.progress_den || !["running", "queued"].includes(job.state)) return null;
  const pct = Math.floor((job.progress_num / job.progress_den) * 100);
  return el("div", { class: "helm-stack helm-studio-progress" },
    progress(job.progress_num, job.progress_den),
    el("p", { class: "helm-mono", text: job.kind === "download"
      ? `${bytes(job.progress_num)} of ${bytes(job.progress_den)} · ${pct}%`
      : `step ${job.progress_num} of ${job.progress_den}` }));
}

/**
 * action draws one of the row's buttons. The main action is a strong outline,
 * a plain one when it only navigates, and the accent only when it is the
 * screen's one (03 §1, amended 2026-09-17).
 */
function action(ctx, studio, a, kind, key) {
  if (!a) return null;
  const look = {
    accent: "helm-btn-primary",
    strong: "helm-btn-secondary helm-btn-strong",
    plain: "helm-btn-secondary",
  }[kind];
  return el("button", {
    class: `helm-btn ${look}`, type: "button", "data-key": key, text: a.label,
    onclick: () => (a.run ? a.run() : ctx.act(studio, a.action)),
  });
}

export function card(ctx, studio, accent) {
  const job = (ctx.store.jobs || {})[studio.id] || null;
  const s = state(studio, job);
  const valid = studio.manifest_valid !== false;
  const flight = valid ? inFlight(job) : null;

  // An invalid entry has one thing to offer, and it is the editor.
  const main = valid
    ? s.primary
    : { label: "Edit", run: () => ctx.go(`#/edit/${encodeURIComponent(studio.id)}`) };
  const kind = accent ? "accent" : main && main.quiet ? "plain" : "strong";
  const failed = valid && (studio.install_state || "").startsWith("failed_") && s.note;

  return el("article", {
    class: "helm-card helm-studio", style: hue(studio), "aria-label": studio.name, "data-key": studio.id,
    // What the row is doing, for the one thing colour says here: a studio
    // that is running is tinted with the running colour.
    "data-state": s.tone,
  },
    el("span", { class: "helm-card-stripe" }),
    el("div", { class: "helm-studio-head" },
      el("h3", { class: "helm-card-title", text: studio.name }),
      (studio.kinds || []).length ? el("span", { class: "helm-kind", text: studio.kinds.join("+") }) : null,
      origin(studio)),
    el("div", { class: "helm-studio-body" },
      el("p", { class: "helm-card-desc", text: studio.description || "" }),
      valid ? null : invalid(studio),
      failed ? el("p", { class: "helm-body helm-status-error", text: s.note }) : null,
      // Only this changes while an install runs: the bar and its numbers take
      // the facts line's place, and nothing above them moves.
      flight || (valid ? el("p", { class: "helm-meta helm-studio-facts", text: facts(studio, ctx.store.models) || "—" }) : null),
      missing(ctx, studio),
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
      s.note && !failed ? el("p", { class: "helm-hint", text: s.note }) : null,
      valid ? checkpoint(ctx, studio) : null),
    // The third fact, the state, above the actions it governs.
    el("div", { class: "helm-card-actions helm-studio-side" },
      chip(s.chip, s.tone),
      el("div", { class: "helm-studio-buttons" },
        valid ? action(ctx, studio, s.secondary, "plain", "secondary") : null,
        action(ctx, studio, main, kind, "main"),
        ...more(ctx, studio, valid))));
}

/**
 * The order the library reads in: by what a studio makes — video, then audio,
 * then image, then text — and by id within each. The daemon serves them by id,
 * which put an image studio between two video ones; this groups what a person
 * is choosing between. A studio that makes several kinds takes the first of
 * them in this order.
 */
const KINDS = ["video", "audio", "image", "text"];

function rank(studio) {
  const kinds = studio.kinds || [];
  for (const [i, kind] of KINDS.entries()) {
    if (kinds.includes(kind)) return i;
  }
  return KINDS.length;
}

function inOrder(studios) {
  return [...studios].sort((a, b) => rank(a) - rank(b) || a.id.localeCompare(b.id));
}

/**
 * accentFor is the one studio whose main action wears the accent: the running
 * studio's Open. With nothing running, none does (03 §1, amended 2026-09-17).
 */
function accentFor(studios) {
  const running = studios.find((s) => s.manifest_valid !== false && s.manifest_loaded && (s.group || {}).state === "running");
  return running ? running.id : null;
}

/** placeholder rows while the first list loads. They do not animate. */
function skeleton() {
  return el("div", { class: "helm-studios", "aria-busy": "true", "aria-label": "Reading the library" },
    ...[0, 1, 2].map((i) => el("div", { class: "helm-card helm-studio helm-skeleton", "data-key": `skeleton-${i}`, "aria-hidden": "true" },
      el("div", { class: "helm-studio-head" }, el("span", { class: "helm-skeleton-bar", style: "width: 28%" })),
      el("div", { class: "helm-studio-body" },
        el("span", { class: "helm-skeleton-bar", style: "width: 72%" }),
        el("span", { class: "helm-skeleton-bar", style: "width: 48%" })))));
}

export function catalogue(ctx) {
  const studios = ctx.store.studios;
  const loaded = ctx.store.loaded;
  const installed = studios.filter((s) => ["ready", "update_available"].includes(s.install_state)).length;
  const onDisk = studios.reduce((n, s) => n + (s.size_bytes || 0), 0);
  const accent = accentFor(studios);
  const empty = loaded && !studios.length;

  return el("div", { class: "helm-stack" },
    el("div", { class: "helm-page-header" },
      el("h1", { class: "helm-title", text: "Studios" }),
      loaded
        ? el("span", { class: "helm-meta", text: `${studios.length} studio${studios.length === 1 ? "" : "s"} · ${installed} installed · ${bytes(onDisk)} on disk` })
        : null,
      el("span", { class: "helm-spacer" }),
      // The accent only when there is nothing else to do (03 §1).
      el("a", { class: `helm-btn ${empty ? "helm-btn-primary" : "helm-btn-secondary"} helm-btn-add`, href: "#/add", text: "Add a studio" })),
    !loaded
      ? skeleton()
      : studios.length
        ? el("div", { class: "helm-studios" }, ...inOrder(studios).map((s) => card(ctx, s, s.id === accent)))
        : el("div", { class: "helm-panel" },
          el("div", { class: "helm-panel-body helm-stack" },
            el("p", { class: "helm-body", text: "No studios yet." }),
            el("p", { class: "helm-micro", text: "Add one from a repository, from a folder on this Mac, from a file someone sent you, or by writing a manifest." }))));
}
