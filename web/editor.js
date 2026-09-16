// The editor (03 §13a, docs/decisions.md M7 Q16, Q17, Q19).
//
// Most repositories worth running will never ship a manifest, so writing one
// has to be a first-class act rather than a fallback. Form on the left for the
// fields, YAML on the right for the parts that are really text, both live, with
// the criteria underneath as you type.
//
// **The page never parses YAML.** Every verdict on this screen came from the
// daemon: the errors with their lines and pointers, the criteria, and the
// document the form renders from. A form edit goes out as a JSON pointer and a
// value and comes back as new text with the author's comments and key order
// intact. A JavaScript YAML parser would be a second implementation, and the
// day it disagreed with `yaml.v3` about an anchor or a duplicate key this
// screen would show one manifest and save another.
//
// There is no Test button. A smoke test builds and runs the studio, and the
// harness that would do it safely does not exist yet (Q16) — so the criteria
// that need it read "Not checked", and the button that would lie about them is
// not drawn.

import { chip, el, failure, toast } from "./ui.js";
import { SECTIONS, YAML_ONLY, label } from "./sections.js";
import { control, loadSchema, missingAncestors, valueAt } from "./schemaform.js";
import { importDialog } from "./importer.js";

/** A new manifest starts as the fields the schema requires, and nothing else. */
export const STARTER = `# A studio helmstudio can install and run.
# Everything below is checked as you type; nothing is saved until you say so.
id: new-studio
name: New studio
kinds: [image]
repo: https://github.com/someone/something
requires:
  os: [darwin]
  arch: [arm64]
runtime:
  framework: other
  backends: [cpu]
processes:
  - name: studio
    role: main
    cmd: "./studio --port {port}"
    port: { prefer: 8730 }
    health: { tcp: true, timeout_s: 60 }
    ui: /
`;

/**
 * editorState is what the screen owns between redraws: the text, the last
 * verdict on it, and the digest the file had when it was opened.
 *
 * The digest is the whole of the concurrency story. Two tabs open on one
 * manifest cannot silently overwrite each other, because a save carries the
 * digest of what the editor read and the daemon refuses it when the file has
 * moved on.
 */
function editorState(ctx, id) {
  return ctx.keep("editor:" + id, () => ({
    id, text: "", digest: "", check: null, schema: null,
    loaded: false, busy: false, error: null, dirty: false,
  }));
}

/**
 * seed hands the editor a document it did not read for itself — a manifest
 * found in a repository, or a folder on this Mac. The screen is the same one
 * either way, because what someone does next is the same thing.
 */
export function seed(ctx, id, { text, digest = "", from = "" }) {
  const st = editorState(ctx, id);
  st.text = text;
  st.digest = digest;
  st.from = from;
  st.check = null;
  st.loaded = false;
  st.busy = false;
  st.dirty = true;
  st.seeded = true;
}

async function load(ctx, st) {
  st.busy = true;
  try {
    st.schema = await loadSchema();
    if (st.seeded) {
      // Already given its text; only the verdict is missing.
    } else if (st.id === "new") {
      st.text = STARTER;
      st.digest = "";
    } else {
      const m = await ctx.client.studios.manifest(st.id);
      st.text = m.text || "";
      // A digest is only an If-Match when the file being saved is the one it
      // came from. An entry resolved from the registry or a repository has no
      // local file yet, so saving it creates one — and creating needs no
      // digest, while pretending to have one would be refused.
      st.digest = m.source === "local" ? m.digest : "";
    }
    st.check = await ctx.client.manifests.validate({ text: st.text });
    st.error = null;
  } catch (err) {
    st.error = failure(err, "This manifest could not be opened.");
  }
  st.loaded = true;
  st.busy = false;
  ctx.redraw(true);
}

/** apply sends one field edit and takes the text and the verdict back. */
async function apply(ctx, st, pointer, value) {
  st.busy = true;
  try {
    // A pointer whose parent does not exist is refused, on purpose: an editor
    // that guesses the shape of what is missing writes something nobody asked
    // for. So the page asks for each empty parent by name first.
    for (const parent of missingAncestors(st.check && st.check.document, pointer)) {
      const made = await ctx.client.manifests.edit({ text: st.text, pointer: parent, value: {} });
      st.text = made.text;
      st.check = made;
    }
    const res = await ctx.client.manifests.edit({ text: st.text, pointer, value });
    st.text = res.text;
    st.check = res;
    st.dirty = true;
    st.error = null;
  } catch (err) {
    st.error = failure(err, `${label(pointer)} could not be changed.`);
  }
  st.busy = false;
  ctx.redraw(true);
}

/** revalidate is what the YAML pane does when someone stops typing in it. */
async function revalidate(ctx, st, text) {
  if (text === st.text) return;
  st.text = text;
  st.dirty = true;
  st.busy = true;
  try {
    st.check = await ctx.client.manifests.validate({ text });
    st.error = null;
  } catch (err) {
    st.error = failure(err, "This text could not be checked.");
  }
  st.busy = false;
  ctx.redraw(true);
}

async function save(ctx, st) {
  const id = declaredID(st.check) || st.id;
  if (id === "new") {
    toast("Give this studio an id before saving it.", "error");
    return;
  }
  st.busy = true;
  try {
    const res = await ctx.client.manifests.save(id, { text: st.text }, { ifMatch: st.digest || undefined });
    st.digest = res.digest;
    st.dirty = false;
    st.error = null;
    toast(`Saved to ${res.file}.`, "info");
    if (id !== st.id) ctx.go(`#/studios/${id}/edit`);
    await ctx.refresh();
  } catch (err) {
    st.error = failure(err, "This manifest was not saved.");
    toast(st.error, "error");
  }
  st.busy = false;
  ctx.redraw(true);
}

/**
 * exportManifest hands back the same bytes, to commit upstream (Q19).
 *
 * helmstudio opens no pull request: there is no GitHub account here and
 * nowhere to keep a token, so the file is downloaded and copied and the person
 * commits it. That is the sharing loop 03 §13a describes — someone gets a
 * model running, exports the manifest, posts it; the next person imports it.
 */
function exportManifest(st) {
  const id = declaredID(st.check) || st.id;
  const blob = new Blob([st.text], { type: "text/yaml" });
  const url = URL.createObjectURL(blob);
  const a = el("a", { href: url, download: `${id}.yaml` });
  document.body.append(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 10000);
  if (navigator.clipboard) navigator.clipboard.writeText(st.text).catch(() => {});
  toast(`${id}.yaml downloaded and copied.`, "info");
}

function declaredID(check) {
  const doc = (check || {}).document;
  if (!doc) return "";
  return valueAt(doc, "/id") || valueAt(doc, "/manifest/id") || "";
}

// ------------------------------------------------------------------ drawing

/** The verdict line above the YAML pane: valid, or the count of what is not. */
function verdict(check) {
  if (!check) return chip("Checking", "idle");
  const errors = (check.errors || []).length;
  if (check.valid) return chip("Valid", "running");
  return chip(`${errors} error${errors === 1 ? "" : "s"}`, "error");
}

/**
 * problems lists what is wrong, each with its line and its pointer. The line
 * is what a person looks for; the pointer is what the form uses to find the
 * field. Both come from the daemon, which is the only thing here that has read
 * the document.
 */
function problems(check) {
  const errors = (check || {}).errors || [];
  if (!errors.length) return null;
  return el("div", { class: "helm-stack" },
    ...errors.map((e) => el("div", { class: "helm-stack", style: "gap: 0" },
      el("p", { class: "helm-mono helm-status-error", text: [e.line ? `line ${e.line}` : null, e.pointer].filter(Boolean).join(" · ") }),
      el("p", { class: "helm-body", text: e.message }))));
}

/**
 * criteria is 05 §9's list, scored out of what a manifest alone can answer.
 *
 * "7 of 15" reads as a failing grade for a studio that did everything a
 * manifest can do, so the count is out of the checkable ones and the rest say
 * what they are waiting for.
 */
function criteria(check) {
  const c = (check || {}).criteria;
  if (!c) return null;
  const rows = c.items || [];
  return el("div", { class: "helm-panel" },
    el("div", { class: "helm-panel-header" },
      el("span", { class: "helm-section-label", text: "Certification criteria" }),
      el("span", { class: "helm-spacer" }),
      el("span", { class: "helm-mono", text: `${c.passed} of ${c.checkable} checkable pass` })),
    el("div", { class: "helm-panel-body helm-stack" },
      ...rows.map((r) => el("div", { class: "helm-row helm-criterion" },
        chip(r.state === "pass" ? "pass" : r.state === "fail" ? "fail" : "not checked",
          r.state === "pass" ? "running" : r.state === "fail" ? (r.required ? "error" : "warning") : "idle"),
        el("div", { class: "helm-stack", style: "gap: 0" },
          el("span", { class: "helm-body", text: `${r.number}. ${r.title}` }),
          r.detail ? el("span", { class: "helm-hint", text: r.detail }) : null)))));
}

function form(ctx, st) {
  const doc = (st.check || {}).document || {};
  const out = [];
  for (const s of SECTIONS) {
    const fields = s.fields
      .map((p) => control(st.schema, p, doc, (pointer, value) => apply(ctx, st, pointer, value)))
      .filter(Boolean);
    if (!fields.length) continue;
    out.push(el("div", { class: "helm-panel" },
      el("div", { class: "helm-panel-header" }, el("span", { class: "helm-section-label", text: s.label })),
      el("div", { class: "helm-panel-body helm-stack" },
        s.hint ? el("p", { class: "helm-hint", text: s.hint }) : null,
        ...fields)));
  }
  out.push(el("p", { class: "helm-hint", text:
    `Edited as text on the right: ${Object.keys(YAML_ONLY).map((p) => p.slice(1)).join(", ")}.` }));
  return out;
}

function yamlPane(ctx, st) {
  // Kept across redraws so the caret and the scroll position survive a poll.
  const area = ctx.keep("editor-yaml:" + st.id, () => el("textarea", {
    class: "helm-input helm-yaml", spellcheck: "false", "aria-label": "The manifest as text",
    onchange: (e) => revalidate(ctx, st, e.target.value),
  }));
  if (document.activeElement !== area) area.value = st.text;

  return el("div", { class: "helm-panel" },
    el("div", { class: "helm-panel-header" },
      el("span", { class: "helm-section-label", text: "helmstudio.yaml" }),
      el("span", { class: "helm-spacer" }),
      verdict(st.check)),
    el("div", { class: "helm-panel-body helm-stack" },
      area,
      problems(st.check)));
}

export function editor(ctx, id) {
  const st = editorState(ctx, id || "new");
  if (!st.loaded && !st.busy) load(ctx, st);

  const declared = declaredID(st.check) || (id === "new" ? "" : id);
  const file = (st.check && st.check.valid && declared)
    ? `studios/${declared}.yaml`
    : "not saved yet";
  const localPath = valueAt((st.check || {}).document || {}, "/local_path");

  const header = el("div", { class: "helm-page-header" },
    el("h1", { class: "helm-title", text: id === "new" ? "New studio" : declared || id }),
    el("span", { class: "helm-mono", text: file }),
    el("span", { class: "helm-spacer" }),
    el("button", { class: "helm-btn helm-btn-secondary helm-btn-sm", text: "Import file",
      onclick: () => importDialog(ctx) }),
    el("button", { class: "helm-btn helm-btn-secondary helm-btn-sm", text: "Export",
      onclick: () => exportManifest(st) }),
    el("button", {
      class: "helm-btn helm-btn-primary helm-btn-sm", text: "Save to library",
      disabled: st.busy || !st.check || !st.check.valid,
      onclick: () => save(ctx, st),
    }));

  if (!st.loaded) {
    return el("div", { class: "helm-stack" }, header,
      el("div", { class: "helm-panel" }, el("div", { class: "helm-panel-body" },
        el("p", { class: "helm-micro", text: "Reading…" }))));
  }

  return el("div", { class: "helm-stack" },
    header,
    st.error ? el("p", { class: "helm-body helm-status-error", text: st.error }) : null,
    // Export hands back a file to commit upstream, and a manifest that builds
    // a directory on this Mac describes nothing anyone else can clone.
    localPath ? el("p", { class: "helm-hint", text:
      `This manifest builds ${localPath} on this Mac. Exported as it is, it will not work on anyone else's.` }) : null,
    st.check && !st.check.valid && st.dirty
      ? el("p", { class: "helm-hint", text: "Saving is offered once this is valid. The library lists an invalid manifest rather than hiding it, but writing one from here would be helmstudio breaking your library for you." })
      : null,
    el("div", { class: "helm-editor" },
      el("div", { class: "helm-stack" }, ...form(ctx, st)),
      el("div", { class: "helm-stack" }, yamlPane(ctx, st), criteria(st.check))));
}
