// Adding a studio (03 §13 and §13a, docs/decisions.md M7 Q14, Q18, and the
// launcher redesign of 2026-09-17).
//
// Four ways in, because the four are genuinely different situations: the
// repository already ships a `helmstudio.yaml`; it does not and you are going
// to write one; someone sent you a manifest; or the thing you want to run is a
// directory already on this Mac. One row each, with one button sized to what
// it says, and only Read repository wears the accent.
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
//
// A row's field is checked when the row is submitted, and what is wrong is
// written under the field. A button that stayed disabled until a box had
// something in it never said why.

import { el, failure, toast } from "./ui.js";
import { STARTER, seed } from "./editor.js";
import { importDialog } from "./importer.js";

function addState(ctx) {
  return ctx.keep("add", () => ({
    repo: "", ref: "", path: "", busy: null,
    // What is wrong, per row, and where the not-found answer belongs.
    errors: { repo: null, path: null },
    notFound: null,
  }));
}

/**
 * read looks for a manifest, and then gets out of the way. Found or not, what
 * happens next is the editor: with the repository's own text, or with a new
 * document that already knows where the code lives.
 */
async function read(ctx, st, row, body) {
  st.busy = row;
  st.errors[row] = null;
  st.notFound = null;
  ctx.redraw(true);
  try {
    const res = await ctx.client.repositories.read(body);
    if (res.found) {
      seed(ctx, "new", { text: res.text, from: body.repo || body.path });
      st.busy = null;
      ctx.go("#/edit");
      return;
    }
    st.notFound = { row, commit: res.commit || "" };
  } catch (err) {
    st.errors[row] = failure(err, "That could not be read.");
  }
  st.busy = null;
  ctx.redraw(true);
}

/** startFrom writes the starter document with what we already know filled in. */
async function startFrom(ctx, st, fields) {
  st.busy = "write";
  ctx.redraw(true);
  try {
    let text = STARTER;
    for (const [pointer, value] of Object.entries(fields)) {
      if (!value) continue;
      const res = await ctx.client.manifests.edit({ text, pointer, value });
      text = res.text;
    }
    seed(ctx, "new", { text });
    st.busy = null;
    ctx.go("#/edit");
  } catch (err) {
    toast(failure(err, "That could not be started."), "error");
    st.busy = null;
    ctx.redraw(true);
  }
}

/**
 * field is a labelled input whose error, when it has one, is written under it
 * and tied to it, so a screen reader reads the two together.
 */
function field({ id, name, label, value, placeholder, error, noted, mono, onchange, className }) {
  const describedBy = [error ? `${id}-error` : null, noted ? `${id}-hint` : null].filter(Boolean).join(" ");
  return el("div", { class: "helm-field" + (className ? " " + className : "") },
    el("label", { class: "helm-label", for: id, text: label }),
    el("input", {
      class: "helm-input" + (mono ? " helm-input-mono" : ""), type: "text", id, name, value, placeholder,
      spellcheck: "false", autocomplete: "off",
      "aria-invalid": error ? "true" : undefined,
      "aria-describedby": describedBy || undefined,
      onchange,
    }));
}

/** fieldNotes is what sits under a row's fields: its error, then its hint. */
function fieldNotes(id, error, hint) {
  return [
    error ? el("p", { class: "helm-body helm-status-error", id: `${id}-error`, text: error }) : null,
    hint ? el("p", { class: "helm-hint", id: `${id}-hint`, text: hint }) : null,
  ];
}

/** row is one way in: a heading, one sentence, and what it needs. */
function row(id, title, blurb, ...children) {
  return el("section", { class: "helm-add-row", "aria-labelledby": id },
    el("div", { class: "helm-add-heading" },
      el("h2", { class: "helm-add-title", id, text: title }),
      el("p", { class: "helm-body helm-add-blurb", text: blurb })),
    ...children);
}

/** notFound is the usual answer, and a way on from it. */
function notFound(ctx, st) {
  const nf = st.notFound;
  return el("div", { class: "helm-add-found helm-stack" },
    el("p", { class: "helm-body", text: nf.row === "repo"
      ? `No helmstudio.yaml in that repository${nf.commit ? ` at ${nf.commit.slice(0, 7)}` : ""} — which is the usual answer, and not a problem.`
      : "No helmstudio.yaml in that folder — which is the usual answer, and not a problem." }),
    el("div", { class: "helm-row" },
      el("button", {
        class: "helm-btn helm-btn-secondary", type: "button", text: "Write one for it", disabled: !!st.busy,
        onclick: () => startFrom(ctx, st, nf.row === "repo"
          ? { "/repo": st.repo || undefined, "/ref": st.ref || undefined }
          : { "/local_path": st.path || undefined }),
      })));
}

export function addStudio(ctx) {
  const st = addState(ctx);

  const readRepo = (e) => {
    e.preventDefault();
    const form = e.currentTarget;
    st.repo = form.elements.repo.value.trim();
    st.ref = form.elements.ref.value.trim();
    if (!st.repo) {
      st.errors.repo = "Enter a repository to read.";
      st.notFound = null;
      ctx.redraw(true);
      form.elements.repo.focus();
      return;
    }
    read(ctx, st, "repo", { repo: st.repo, ref: st.ref });
  };

  const readFolder = (e) => {
    e.preventDefault();
    const form = e.currentTarget;
    st.path = form.elements.path.value.trim();
    if (!st.path.startsWith("/")) {
      st.errors.path = st.path
        ? `${st.path} is not an absolute path. Start it at the root, as in /Users/you/code/wan.`
        : "Enter the folder to read.";
      st.notFound = null;
      ctx.redraw(true);
      form.elements.path.focus();
      return;
    }
    read(ctx, st, "path", { path: st.path });
  };

  return el("div", { class: "helm-stack helm-add" },
    el("h1", { class: "helm-title", text: "Add a studio" }),
    el("p", { class: "helm-body helm-add-lede", text:
      "Adding a studio to your library describes it. Nothing is cloned, built or run until you install it, and the approval screen shows every command before that happens." }),

    row("add-repo", "From a repository",
      "If it ships a helmstudio.yaml, that file is the manifest. Reading it fetches one file at one commit; nothing is cloned.",
      el("form", { class: "helm-add-form", novalidate: true, onsubmit: readRepo },
        el("div", { class: "helm-add-fields" },
          field({ id: "add-repo-url", name: "repo", label: "Repository", value: st.repo, placeholder: "https://github.com/someone/wan",
            error: st.errors.repo, noted: true, className: "helm-add-grow",
            onchange: (e) => { st.repo = e.target.value.trim(); } }),
          field({ id: "add-repo-ref", name: "ref", label: "Ref", value: st.ref, placeholder: "v0.3.1", mono: true, className: "helm-add-ref",
            onchange: (e) => { st.ref = e.target.value.trim(); } }),
          el("button", { class: "helm-btn helm-btn-primary helm-btn-lg", type: "submit",
            text: st.busy === "repo" ? "Reading…" : "Read repository", disabled: !!st.busy })),
        ...fieldNotes("add-repo-url", st.errors.repo, "An https or ssh git URL, or a path to a clone. An empty ref means the remote's HEAD.")),
      st.notFound && st.notFound.row === "repo" ? notFound(ctx, st) : null),

    row("add-folder", "From a folder on this Mac",
      "Code already on this Mac. helmstudio reads the folder's helmstudio.yaml and nothing else.",
      el("form", { class: "helm-add-form", novalidate: true, onsubmit: readFolder },
        el("div", { class: "helm-add-fields" },
          field({ id: "add-folder-path", name: "path", label: "Folder", value: st.path, placeholder: "/Users/you/code/wan", mono: true,
            error: st.errors.path, noted: true, className: "helm-add-grow",
            onchange: (e) => { st.path = e.target.value.trim(); } }),
          el("button", { class: "helm-btn helm-btn-secondary helm-btn-strong helm-btn-lg", type: "submit",
            text: st.busy === "path" ? "Reading…" : "Read folder", disabled: !!st.busy })),
        ...fieldNotes("add-folder-path", st.errors.path, "An absolute path. Only <path>/helmstudio.yaml is read.")),
      st.notFound && st.notFound.row === "path" ? notFound(ctx, st) : null),

    el("section", { class: "helm-add-row helm-add-row-inline", "aria-labelledby": "add-file" },
      el("div", { class: "helm-add-heading" },
        el("h2", { class: "helm-add-title", id: "add-file", text: "From a file someone sent you" }),
        el("p", { class: "helm-body helm-add-blurb", text: "Drop files, pick them, paste text or give a URL, several at once. Each one is reported before anything is added." })),
      el("button", { class: "helm-btn helm-btn-secondary helm-btn-lg", type: "button", text: "Import files…",
        onclick: () => importDialog(ctx) })),

    el("section", { class: "helm-add-row helm-add-row-inline", "aria-labelledby": "add-write" },
      el("div", { class: "helm-add-heading" },
        el("h2", { class: "helm-add-title", id: "add-write", text: "Write one" }),
        el("p", { class: "helm-body helm-add-blurb", text: "Most repositories worth running never ship a manifest. A manifest is a description of a repository, not a file only its author can provide." })),
      el("button", { class: "helm-btn helm-btn-secondary helm-btn-lg", type: "button", text: "Write a manifest",
        onclick: () => { seed(ctx, "new", { text: STARTER }); ctx.go("#/edit"); } })));
}
