// Settings (03 §12a, added by the launcher redesign of 2026-09-17;
// docs/decisions.md M6 Q2, Q21).
//
// Three sections: the Hugging Face token, the directories helmstudio uses on
// this Mac, and what this daemon is. The theme is not here — it is the
// three-state control in the top bar, in one place.
//
// The token is write-only. It goes to the OS secret store and never comes back
// through the API, so this screen can say that one is stored and when — never
// what it is. The field is emptied the moment it has been sent.

import { copyButton, el, failure, toast } from "./ui.js";

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

/** What this daemon says about itself, read once. */
async function loadAbout(ctx) {
  try {
    ctx.store.about = await ctx.client.settings.about();
  } catch (err) {
    ctx.store.about = { error: failure(err, "What this daemon is could not be read.") };
  }
  ctx.redraw(true);
}

function panel(id, title, ...body) {
  return el("section", { class: "helm-panel", "aria-labelledby": id },
    el("div", { class: "helm-panel-header" }, el("h2", { class: "helm-section-label", id, text: title })),
    ...body);
}

function tokenSection(ctx) {
  const token = ctx.store.hfToken;
  const unsupported = !!(token && token.unsupported);
  const present = !!(token && token.present);

  const said = () => {
    if (!token) return "Reading…";
    if (token.error) return token.error;
    if (token.unsupported) return token.unsupported;
    if (!present) return "No token is stored. Public weights download without one; a gated repository refuses.";
    return token.added_at
      ? `A token is stored. helmstudio saved it on ${stamp(token.added_at)}.`
      : "A token is stored. It was added outside helmstudio, so there is no record of when.";
  };

  const save = async (e) => {
    e.preventDefault();
    const form = e.currentTarget;
    const field = form.elements.token;
    const value = field.value.trim();
    if (!value) {
      toast("Paste a token first.", "error");
      field.focus();
      return;
    }
    const button = form.querySelector('button[type="submit"]');
    button.disabled = true;
    try {
      ctx.store.hfToken = await ctx.client.settings.setHuggingFaceToken({ token: value });
      field.value = "";
      toast("The Hugging Face token is stored in the Keychain.");
    } catch (err) {
      toast(failure(err, "The token was not stored."), "error");
    } finally {
      button.disabled = false;
      ctx.redraw(true);
    }
  };

  const forget = async () => {
    try {
      await ctx.client.settings.deleteHuggingFaceToken();
      ctx.store.hfToken = { present: false };
      toast("The stored token is gone. Downloads will ask anonymously.");
    } catch (err) {
      toast(failure(err, "The token was not removed."), "error");
    }
    ctx.redraw(true);
  };

  return panel("settings-token", "Hugging Face token",
    el("div", { class: "helm-panel-body helm-stack" },
      el("p", { class: "helm-body", text: said() }),
      unsupported ? null : el("form", { class: "helm-token-form", onsubmit: save },
        el("div", { class: "helm-field helm-token-field" },
          el("label", { class: "helm-label", for: "hf-token", text: "Token" }),
          el("input", {
            class: "helm-input helm-input-mono", type: "password", id: "hf-token", name: "token",
            autocomplete: "off", spellcheck: "false", placeholder: "hf_…", "aria-describedby": "hf-hint",
          })),
        el("button", { class: "helm-btn helm-btn-primary helm-btn-lg", type: "submit", text: present ? "Replace token" : "Add token" }),
        // Drawn only when there is something to remove. It was hidden with
        // `hidden`, which a button's own display overrides.
        present
          ? el("button", { class: "helm-btn helm-btn-secondary helm-btn-lg", type: "button", text: "Remove", onclick: forget })
          : null),
      unsupported ? null : el("p", {
        class: "helm-hint", id: "hf-hint",
        text: "Kept in the macOS Keychain, never in helmstudio's database, and never shown again. Create one at huggingface.co under Settings, Access Tokens, with read access.",
      })));
}

const PATHS = [
  ["data", "Data"],
  ["models", "Models"],
  ["library", "Library"],
  ["logs", "Logs"],
  ["cache", "Cache"],
];

function macSection(ctx) {
  const about = ctx.store.about;
  let body;
  if (!about) body = el("p", { class: "helm-micro", text: "Reading…" });
  else if (about.error) body = el("p", { class: "helm-body", text: about.error });
  else {
    body = el("dl", { class: "helm-facts" },
      ...PATHS.flatMap(([key, label]) => [
        el("dt", { class: "helm-body", text: label }),
        el("dd", { class: "helm-mono", text: about.paths[key] }),
        el("dd", { class: "helm-facts-action" }, copyButton(about.paths[key], `Copy the ${label.toLowerCase()} directory`, `The ${label.toLowerCase()} directory`)),
      ]));
  }
  return panel("settings-mac", "This Mac",
    el("div", { class: "helm-panel-body helm-stack" },
      body,
      el("p", { class: "helm-hint", text: "Revealing a folder in Finder needs the Mac app. In the browser, copy its path and open it yourself." })));
}

function aboutSection(ctx) {
  const about = ctx.store.about;
  let body;
  if (!about) body = el("p", { class: "helm-micro", text: "Reading…" });
  else if (about.error) body = el("p", { class: "helm-body", text: about.error });
  else {
    const build = [about.daemon_version, about.commit ? about.commit.slice(0, 7) : null, about.modified ? "modified" : null]
      .filter(Boolean).join(" · ");
    const sdk = about.sdk_majors || {};
    body = el("dl", { class: "helm-facts helm-facts-plain" },
      el("dt", { class: "helm-body", text: "helmstudio" }),
      el("dd", { class: "helm-mono", text: build }),
      el("dt", { class: "helm-body", text: "Platform API" }),
      el("dd", { class: "helm-mono", text: about.api_version }),
      el("dt", { class: "helm-body", text: "SDK it serves" }),
      el("dd", { class: "helm-mono", text: `runtime ${sdk.runtime} · ui ${sdk.ui} · css ${sdk.css}` }));
  }
  return panel("settings-about", "About",
    el("div", { class: "helm-panel-body helm-stack" },
      body,
      about && !about.error
        ? el("p", { class: "helm-hint", text: "A studio's sdk pins are checked against these majors when it launches." })
        : null));
}

export function settings(ctx) {
  if (ctx.store.hfToken === undefined) {
    ctx.store.hfToken = null;
    loadToken(ctx);
  }
  if (ctx.store.about === undefined) {
    ctx.store.about = null;
    loadAbout(ctx);
  }
  return el("div", { class: "helm-stack helm-settings" },
    el("h1", { class: "helm-title", text: "Settings" }),
    tokenSection(ctx),
    macSection(ctx),
    aboutSection(ctx));
}
