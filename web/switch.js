// Launching, and the dialog that stands between two heavy studios (03 §9,
// docs/decisions.md M5 Q13 and M6 Q22).
//
// Only one studio may hold a model in memory at a time. When a second is
// launched the daemon refuses with the arithmetic, what the running studio's
// busy probe answered, and a digest of exactly that picture. Confirming sends
// the digest back; if the running studio changed in between — it started or
// finished work — the daemon answers `preview_changed` with what is true now
// and stops nothing, and the dialog is shown again. A render is never stopped
// under a confirmation nobody saw.

import { announce, dialog, el, failure, toast } from "./ui.js";
import { guard } from "./approval.js";

/** Consent remembered for this session, per running studio, and only while it
 *  reported idle (Q22). Reloading the page forgets it, which is the point. */
const remembered = new Set();

/**
 * body states the arithmetic from the conflict, and claims the models do not
 * fit only when their sum exceeds host memory. The one-heavy-at-a-time rule
 * applies whether or not they fit.
 */
export function switchBody(h) {
  const sum = (h.running_peak_gb || 0) + (h.wanted_peak_gb || 0);
  const known = h.running_peak_gb && h.wanted_peak_gb && h.host_gb;
  const fits = known && sum <= h.host_gb;

  const lines = [];
  if (known && !fits) {
    lines.push(`Only one studio can hold a model in memory at a time. Your Mac has ${h.host_gb} GB of unified memory; ${h.running_name} needs about ${h.running_peak_gb} GB and ${h.wanted_name} about ${h.wanted_peak_gb} GB, and both sets of activations do not fit alongside each other.`);
  } else if (known) {
    lines.push(`Only one studio can hold a model in memory at a time. ${h.running_name} needs about ${h.running_peak_gb} GB and ${h.wanted_name} about ${h.wanted_peak_gb} GB, which your ${h.host_gb} GB Mac has room for — but a second model loaded alongside the first is not something helmstudio allows.`);
  } else {
    lines.push(`Only one studio can hold a model in memory at a time. ${h.running_name} does not declare how much memory it needs, so helmstudio cannot say whether both would fit.`);
  }

  const busy = h.busy || {};
  if (busy.state === "busy") {
    lines.push(`${h.running_name} says it is busy${busy.message ? ": " + busy.message : "."}${busy.progress ? ` (${Math.round(busy.progress * 100)}%)` : ""}`);
  } else if (busy.state === "unknown") {
    lines.push(`helmstudio cannot tell whether ${h.running_name} is working${busy.message ? ": " + busy.message : "."}`);
  }

  lines.push(`${h.running_name} will be stopped. Anything it has written to disk is kept; anything mid-generation is lost.`);
  return lines.map((t) => el("p", { class: "helm-body", style: "margin: 0 0 var(--helm-space-2)", text: t }));
}

/**
 * launch starts a studio, showing the switch dialog for a heavy conflict and
 * fetching a fresh conflict every time — even when consent was remembered,
 * because a remembered consent is a confirmation nobody read.
 */
export async function launch(ctx, studio, preempt, tries = 0) {
  // A daemon that keeps answering preview_changed means the running studio
  // keeps changing under us. Asking twice is a race worth retrying; asking
  // forever is a loop, and stopping with the dialog on screen is the honest
  // end of it.
  if (tries > 3) {
    toast(`${studio.name} was not launched: what the other studio is doing keeps changing. Try again in a moment.`, "error");
    return ctx.refresh();
  }
  try {
    // cmd and health.exec run at every launch and never passed through
    // install, so a launch is gated by approval too (M7 Q10). The switch
    // dialog below is a different question — whose model holds the memory —
    // and both can be asked on one launch.
    const started = await guard(ctx, studio, "Launch", (approval) =>
      ctx.client.studios.launch(studio.id, { preempt, approval }));
    if (started === null) return ctx.refresh();
    announce(`${studio.name} is starting.`);
    ctx.go(`#/studios/${studio.id}/processes`);
    return ctx.refresh();
  } catch (err) {
    const heavy = err && err.details && err.details.heavy;
    if (!heavy || !["heavy_conflict", "preview_changed"].includes(err.code)) {
      toast(failure(err, `${studio.name} could not be launched.`), "error");
      return ctx.refresh();
    }
    const idle = (heavy.busy || {}).state === "idle";
    if (idle && remembered.has(heavy.running_studio)) {
      // Remembered, and the studio is still idle — so this digest, freshly
      // fetched, is what the consent was given for.
      return launch(ctx, studio, heavy.confirm, tries + 1);
    }
    const again = await askToSwitch(heavy, studio);
    if (!again) return ctx.refresh();
    if (again.remember) remembered.add(heavy.running_studio);
    return launch(ctx, studio, heavy.confirm, tries + 1);
  }
}

async function askToSwitch(heavy, studio) {
  const idle = (heavy.busy || {}).state === "idle";
  // "Don't ask again this session" is offered only while the running studio
  // reports idle — never for busy, never for unknown (Q22).
  const box = idle
    ? el("label", { class: "helm-label", style: "display: flex; gap: var(--helm-space-2); align-items: center; margin-top: var(--helm-space-3)" },
      el("input", { type: "checkbox", id: "helm-remember" }),
      document.createTextNode("Don't ask again this session"))
    : null;

  const chosen = await dialog({
    title: `Stop ${heavy.running_name} to launch ${studio.name}?`,
    body: switchBody(heavy),
    extra: box,
    actions: [
      { label: "Cancel", value: null },
      // Danger-filled, not accent-filled: this destroys running work, which
      // is the one place a status colour becomes a button (03 §9).
      { label: `Stop ${heavy.running_name} and launch ${studio.name}`, value: "go", class: "helm-btn-danger-fill", primary: true },
    ],
  });
  if (chosen !== "go") return null;
  return { remember: !!(box && box.querySelector("input").checked) };
}

export async function stop(ctx, studio) {
  try {
    await ctx.client.studios.stop(studio.id);
    announce(`${studio.name} is stopping.`);
  } catch (err) {
    toast(failure(err, `${studio.name} could not be stopped.`), "error");
  }
  return ctx.refresh();
}
