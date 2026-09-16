// Adding a studio (03 §13a, docs/decisions.md M7 Q14, Q18).
//
// Four ways in, because the four are genuinely different situations: the
// repository already ships a `helmstudio.yaml`; it does not and you are going
// to write one; someone sent you a manifest; or the thing you want to run is a
// directory already on this Mac.
//
// Reading a repository clones nothing and runs nothing. It fetches one file at
// one commit — `git fetch --depth 1 --filter=blob:none` and then `git show` —
// so pointing at a stranger's repository to see whether it describes itself is
// not an act with consequences. Installing it is, and that is a different
// screen.
//
// "From a folder" takes a typed absolute path and reads only
// `<path>/helmstudio.yaml`. There is no directory-listing endpoint and there
// will not be one: a browser picker never hands a page an absolute path, and a
// daemon that would list any directory for whatever reached it first is a
// worse thing than a text box.

import { el, failure, toast } from "./ui.js";
import { STARTER, seed } from "./editor.js";
import { importDialog } from "./importer.js";

function addState(ctx) {
  return ctx.keep("add", () => ({ repo: "", ref: "", path: "", busy: false, error: null, result: null }));
}

/**
 * read looks for a manifest, and then gets out of the way. Found or not, what
 * happens next is the editor: with the repository's own text, or with a new
 * document that already knows where the code lives.
 */
async function read(ctx, st, body) {
  st.busy = true;
  st.error = null;
  st.result = null;
  ctx.redraw(true);
  try {
    const res = await ctx.client.repositories.read(body);
    st.result = res;
    if (res.found) {
      seed(ctx, "new", { text: res.text, from: body.repo || body.path });
      ctx.go("#/edit");
      return;
    }
  } catch (err) {
    st.error = failure(err, "That could not be read.");
  }
  st.busy = false;
  ctx.redraw(true);
}

/** startFrom writes the starter document with what we already know filled in. */
async function startFrom(ctx, st, fields) {
  st.busy = true;
  ctx.redraw(true);
  try {
    let text = STARTER;
    for (const [pointer, value] of Object.entries(fields)) {
      if (!value) continue;
      const res = await ctx.client.manifests.edit({ text, pointer, value });
      text = res.text;
    }
    seed(ctx, "new", { text });
    ctx.go("#/edit");
  } catch (err) {
    toast(failure(err, "That could not be started."), "error");
    st.busy = false;
    ctx.redraw(true);
  }
}

function field(labelText, value, hint, onchange, attrs) {
  const input = el("input", { class: "helm-input", type: "text", value, onchange, ...(attrs || {}) });
  return {
    node: el("div", { class: "helm-field" },
      el("label", { class: "helm-label", text: labelText }), input,
      hint ? el("span", { class: "helm-hint", text: hint }) : null),
    input,
  };
}

function panel(title, blurb, ...children) {
  return el("div", { class: "helm-panel" },
    el("div", { class: "helm-panel-header" }, el("span", { class: "helm-section-label", text: title })),
    el("div", { class: "helm-panel-body helm-stack" },
      el("p", { class: "helm-body", text: blurb }), ...children));
}

export function addStudio(ctx) {
  const st = addState(ctx);

  const repo = field("Repository", st.repo, "An https or ssh git URL, or a path to a clone.",
    (e) => { st.repo = e.target.value.trim(); }, { placeholder: "https://github.com/someone/wan" });
  const ref = field("Ref", st.ref, "A tag, branch or commit. Empty means the remote's HEAD.",
    (e) => { st.ref = e.target.value.trim(); }, { placeholder: "v0.3.1" });
  const path = field("Folder", st.path, "An absolute path on this Mac. Only <path>/helmstudio.yaml is read.",
    (e) => { st.path = e.target.value.trim(); }, { placeholder: "/Users/you/code/wan" });

  const notFound = st.result && !st.result.found
    ? el("div", { class: "helm-stack" },
      el("p", { class: "helm-body", text: "No helmstudio.yaml there — which is the usual answer, and not a problem." }),
      el("p", { class: "helm-micro", text: st.result.commit ? `read at ${st.result.commit.slice(0, 7)}` : "" }),
      el("button", {
        class: "helm-btn helm-btn-primary helm-btn-sm", text: "Write one for it", disabled: st.busy,
        onclick: () => startFrom(ctx, st, { "/repo": st.repo || undefined, "/ref": st.ref || undefined, "/local_path": st.path || undefined }),
      }))
    : null;

  return el("div", { class: "helm-stack" },
    el("div", { class: "helm-page-header" },
      el("h1", { class: "helm-title", text: "Add a studio" }),
      el("span", { class: "helm-spacer" }),
      el("a", { class: "helm-link", href: "#/studios", text: "Back to the library" })),

    el("p", { class: "helm-body", text:
      "Adding a studio to your library describes it. Nothing is cloned, built or run until you install it, and the approval screen shows every command before that happens." }),
    st.error ? el("p", { class: "helm-body helm-status-error", text: st.error }) : null,
    notFound,

    el("div", { class: "helm-grid-cards" },
      panel("From a repository",
        "If it ships a helmstudio.yaml, that file is the manifest. Reading it fetches one file at one commit — nothing is cloned.",
        repo.node, ref.node,
        el("button", {
          class: "helm-btn helm-btn-primary helm-btn-sm", text: "Read it", disabled: st.busy || !st.repo,
          onclick: () => read(ctx, st, { repo: st.repo, ref: st.ref }),
        })),

      panel("From a folder",
        "Code already on this Mac. helmstudio reads one file — <path>/helmstudio.yaml — and nothing else.",
        path.node,
        el("button", {
          class: "helm-btn helm-btn-primary helm-btn-sm", text: "Read it", disabled: st.busy || !st.path,
          onclick: () => read(ctx, st, { path: st.path }),
        })),

      panel("From a file someone sent you",
        "A dropped file, a picker, pasted text or a URL — several at once. Each one is reported before anything is added.",
        el("button", {
          class: "helm-btn helm-btn-primary helm-btn-sm", text: "Import…",
          onclick: () => importDialog(ctx),
        })),

      panel("Write one",
        "Most repositories worth running will never ship a manifest. A manifest is a description of a repository, not a file only its author can provide.",
        el("button", {
          class: "helm-btn helm-btn-primary helm-btn-sm", text: "Start a new manifest",
          onclick: () => { seed(ctx, "new", { text: STARTER }); ctx.go("#/edit"); },
        }))));
}
