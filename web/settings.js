// Settings: the theme, and the Hugging Face token (docs/decisions.md M6 Q2,
// Q8, Q21).
//
// The token is write-only. It goes to the OS secret store and never comes back
// through the API, so this screen can say that one is stored and when — never
// what it is. The field is emptied the moment it has been sent.

import { el, failure, row, section, themeControl, toast } from "./ui.js";

/** stamp is a fixed, unambiguous time. Deliberately not the machine's locale:
 *  this is a record of when a secret was stored, and it is read across
 *  machines and pinned in screenshots. */
function stamp(iso) {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? String(iso) : d.toISOString().replace("T", " ").slice(0, 16) + " UTC";
}

/** The token's state, kept on the store so a redraw does not lose it. */
async function loadToken(ctx) {
  try {
    ctx.store.hfToken = await ctx.client.settings.huggingFaceToken();
  } catch (err) {
    ctx.store.hfToken = { present: false, error: failure(err, "Whether a token is stored could not be read.") };
  }
  ctx.redraw(true);
}

export function settings(ctx) {
  if (ctx.store.hfToken === undefined) {
    ctx.store.hfToken = null;
    loadToken(ctx);
  }
  const token = ctx.store.hfToken;

  const field = el("input", {
    class: "helm-input",
    type: "password",
    id: "hf-token",
    autocomplete: "off",
    spellcheck: "false",
    placeholder: "hf_…",
    "aria-describedby": "hf-hint",
  });

  const save = el("button", {
    class: "helm-btn helm-btn-primary",
    text: token && token.present ? "Replace token" : "Add token",
    onclick: async () => {
      const value = field.value.trim();
      if (!value) return toast("Paste a token first.", "error");
      save.disabled = true;
      try {
        ctx.store.hfToken = await ctx.client.settings.setHuggingFaceToken({ token: value });
        field.value = "";
        toast("The Hugging Face token is stored in the Keychain.");
      } catch (err) {
        toast(failure(err, "The token was not stored."), "error");
      } finally {
        save.disabled = false;
        ctx.redraw(true);
      }
    },
  });

  const forget = el("button", {
    class: "helm-btn helm-btn-danger",
    text: "Remove",
    hidden: !(token && token.present),
    onclick: async () => {
      try {
        await ctx.client.settings.deleteHuggingFaceToken();
        ctx.store.hfToken = { present: false };
        toast("The stored token is gone. Downloads will ask anonymously.");
      } catch (err) {
        toast(failure(err, "The token was not removed."), "error");
      }
      ctx.redraw(true);
    },
  });

  const tokenState = () => {
    if (!token) return "Reading…";
    if (token.error) return token.error;
    if (token.unsupported) return token.unsupported;
    if (!token.present) return "No token is stored. Public weights download without one; a gated repository will refuse.";
    return token.added_at
      ? `A token is stored. helmstudio saved it on ${stamp(token.added_at)}.`
      : "A token is stored. It was added outside helmstudio, so there is no record of when.";
  };

  const unsupported = !!(token && token.unsupported);

  return el("div", { class: "helm-stack" },
    el("div", { class: "helm-page-header" }, el("h1", { class: "helm-title", text: "Settings" })),

    el("div", { class: "helm-panel" },
      el("div", { class: "helm-panel-header" }, el("span", { class: "helm-section-label", text: "Theme" })),
      el("div", { class: "helm-panel-body helm-stack" },
        el("p", { class: "helm-body", text: "System follows this Mac. Light and Dark override it here and in every running studio, without a reload." }),
        themeControl(ctx))),

    el("div", { class: "helm-panel" },
      el("div", { class: "helm-panel-header" },
        el("span", { class: "helm-section-label", text: "Hugging Face token" })),
      el("div", { class: "helm-panel-body helm-stack" },
        el("p", { class: "helm-body", text: tokenState() }),
        unsupported ? null : el("div", { class: "helm-field" },
          el("label", { class: "helm-label", for: "hf-token", text: "Token" }),
          field,
          el("span", {
            class: "helm-hint",
            id: "hf-hint",
            text: "Kept in the macOS Keychain, never in helmstudio's database and never shown again. Create one at huggingface.co under Settings, Access Tokens, with read access.",
          })),
        unsupported ? null : el("div", { class: "helm-row" }, save, forget))),

    el("div", { class: "helm-panel" },
      el("div", { class: "helm-panel-header" }, el("span", { class: "helm-section-label", text: "This daemon" })),
      el("div", { class: "helm-panel-body helm-stack" },
        section("Where things are",
          row("studios", String(ctx.store.studios.length)),
          row("weights", String((ctx.store.models || []).length))))));
}
