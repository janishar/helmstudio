// The launcher's Timeline (03 §11, amended 2026-09-19): the sequences every
// studio has made, edited here, with clips from any of them.
//
// The editor is <helm-timeline> over the launcher's client, which reaches
// every sequence for the same reason the gallery reaches every item. Two
// things are the page's rather than the component's:
//
//   - What goes into a sequence (04 §11 rule 5). "Add" asks, and the page
//     opens the gallery as a picker over every studio — which is the whole
//     difference between assembling here and assembling inside one studio,
//     where the picker can only offer that studio's own work.
//   - A sequence to start from, when no studio has made one.
//
// There is no Export on this screen, and that is not an omission to be tidied
// away later without a decision: who owns a launcher export — the item, the
// asset's origin, the library folder — was left to be decided with this screen
// and has not been. The client carries no export, so the editor draws none
// (04 §9). A sequence assembled here is exported from a studio until then.

import { el, toast } from "./ui.js";
import { pickAsset } from "./gallery.js";

/** DEFAULT is what a new sequence targets until it holds anything (03 §11). */
const DEFAULT = { width: 1920, height: 1080, fps: 24, sample_rate: 48000 };

export function timelineScreen(ctx) {
  const id = ctx.query.get("id") || "";
  const editor = ctx.keep("timeline", () => {
    const node = document.createElement("helm-timeline");
    node.setAttribute("chooser", "");
    node.setAttribute("editable", "");
    node.client = ctx.client;
    // Which hue a clip wears is the library's to say, and the launcher is the
    // page that has read it: the studio API serves no studio's hue but its
    // own, so inside a studio every other studio's clip is neutral. Here they
    // are not (03 §11).
    node.hueFor = (clip) => {
      const studio = (ctx.store.studios || []).find((s) => s.id === clip.studio_id);
      return studio ? studio.hue : null;
    };
    node.addEventListener("add-request", async (e) => {
      const asset = await pickAsset(ctx);
      if (asset) node.append(asset, e.detail && e.detail.track);
    });
    return node;
  });
  if (id) editor.setAttribute("timeline", id);

  return el("div", { class: "helm-stack helm-gallery-screen" },
    el("div", { class: "helm-page-header" },
      el("h1", { class: "helm-title", text: "Timeline" }),
      el("span", { class: "helm-micro", text: "Every studio's sequences. A clip can come from any of them." }),
      el("span", { class: "helm-spacer" }),
      el("button", {
        class: "helm-btn helm-btn-secondary", type: "button", text: "New sequence",
        onclick: () => create(ctx, editor),
      })),
    editor);
}

/**
 * create starts an empty sequence the launcher owns — owned by no studio,
 * which is what lets it hold four studios' footage without one of them being
 * the one it belongs to.
 */
async function create(ctx, editor) {
  try {
    const made = await ctx.client.timeline.create({ name: "Untitled sequence", target: DEFAULT });
    editor.setAttribute("timeline", made.id);
    ctx.go(`#/timeline?id=${encodeURIComponent(made.id)}`);
    toast("New sequence.");
  } catch (err) {
    toast(`The sequence could not be created. ${err && err.message ? err.message : ""}`.trim(), "error");
  }
}
