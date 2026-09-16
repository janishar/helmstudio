// Catalogue (03 §6): the grid of studio cards.
//
// Card anatomy: a 3px identity stripe, the title with a kind badge, one
// clamped description line, a mono facts line, then the state chip and the
// primary action. During a download only the lower two rows swap — the title
// and the stripe never move, so the grid does not reflow while four cards tick.

import { bytes, chip, el, facts, progress, state } from "./ui.js";

/**
 * The identity hue a card's stripe wears (03 §6, M6 Q7).
 *
 * `Studio` does not carry `hue`, so every card falls back to the accent today
 * — see the finding in this milestone's report. This reads the field the API
 * would serve, so that adding it is a one-line change here rather than a
 * search for where the stripe is drawn.
 */
function hue(studio) {
  return studio.hue ? `--_helm-hue: ${studio.hue}` : "";
}

function actionButton(ctx, studio, action, primary) {
  if (!action) return null;
  return el("button", {
    class: "helm-btn " + (primary ? "helm-btn-primary" : "helm-btn-secondary") + " helm-btn-sm",
    text: action.label,
    onclick: () => ctx.act(studio, action.action),
  });
}

export function card(ctx, studio) {
  const job = (ctx.store.jobs || {})[studio.id] || null;
  const s = state(studio, job);

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
    el("p", { class: "helm-card-desc", text: studio.description || "" }),
    lower,
    el("div", { class: "helm-card-actions" },
      chip(s.chip, s.tone),
      el("span", { class: "helm-spacer" }),
      actionButton(ctx, studio, s.secondary, false),
      actionButton(ctx, studio, s.primary, true)),
    s.note ? el("p", { class: "helm-hint", text: s.note }) : null);
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
      // Adding a studio is M7's screen. Until it exists, saying where a studio
      // comes from is better than a button that goes nowhere.
      el("span", { class: "helm-micro", text: "Studios come from studios/*.yaml" })),
    studios.length
      ? el("div", { class: "helm-grid-cards" }, ...studios.map((s) => card(ctx, s)))
      : el("div", { class: "helm-panel" },
        el("div", { class: "helm-panel-body helm-stack" },
          el("p", { class: "helm-body", text: "No studios yet." }),
          el("p", { class: "helm-micro", text: "helmstudio reads its registry from studios/*.yaml beside the daemon." }))));
}
