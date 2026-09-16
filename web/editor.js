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

/**
 * A registry entry is a pointer — `id`, `repo`, `ref` — carrying its manifest
 * inline under `manifest` when the repository ships none. Every bundled studio
 * is one, so Override opens one.
 *
 * The form's fields are a manifest's, so on a pointer they live under
 * /manifest. And `id`, `repo` and `ref` are stated twice there and must agree
 * (the entry-agreement rule), so a form edit to one of them writes both copies,
 * as Duplicate's rename does. Writing only one would turn a valid entry into an
 * invalid one with a single keystroke, and leave the fix to the YAML pane.
 */
const AGREE = ["/id", "/repo", "/ref"];

function isPointer(check) {
  return !!check && check.kind === "pointer";
}

/** fields is the object the form reads its values from. */
function fields(check) {
  const doc = (check || {}).document || {};
  return isPointer(check) ? (doc.manifest || {}) : doc;
}

/** targets is where one form edit lands in the document. */
function targets(check, pointer) {
  if (!isPointer(check)) return [pointer];
  const inline = "/manifest" + pointer;
  if (!AGREE.includes(pointer)) return [inline];
  // A pointer with no inline manifest states these once. Writing a second
  // copy would create an inline manifest holding nothing but an id.
  return (check.document || {}).manifest ? [pointer, inline] : [pointer];
}

/** apply sends one field edit and takes the text and the verdict back. */
async function apply(ctx, st, pointer, value) {
  if (!st.check || !st.check.document) {
    st.error = "The text on the right is not YAML yet, so the form cannot tell what it would be changing. Fix it there first.";
    ctx.redraw(true);
    return;
  }
  st.busy = true;
  try {
    for (const target of targets(st.check, pointer)) {
      // Each empty parent is asked for by name, and only when it has been
      // seen to be absent. See missingAncestors for why a guess is worse.
      const parents = missingAncestors(st.check.document, target);
      if (parents === null) {
        throw new Error(`${target} sits under something that is not a mapping, so the form will not write it. Change it in the text instead.`);
      }
      for (const parent of parents) {
        const made = await ctx.client.manifests.edit({ text: st.text, pointer: parent, value: {} });
        st.text = made.text;
        st.check = made;
      }
      const res = await ctx.client.manifests.edit({ text: st.text, pointer: target, value });
      st.text = res.text;
      st.check = res;
    }
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
  const doc = fields(st.check);
  // No document means the text is not YAML: every control is shown, and none
  // of them can be used until the text parses again.
  const disabled = !(st.check && st.check.document);
  const out = [];
  if (disabled && st.check) {
    out.push(el("p", { class: "helm-hint helm-status-warning", text: "The text on the right is not YAML yet. The form comes back when it is." }));
  }
  if (isPointer(st.check)) {
    out.push(el("p", { class: "helm-hint", text: (st.check.document || {}).manifest
      ? "This is a registry entry. The fields below are the manifest it carries inline, and its id, repository and ref are kept in step with the entry's own."
      : "This is a registry entry with no inline manifest: the repository's own helmstudio.yaml describes it. Filling in the form writes a manifest here, which takes precedence." }));
  }
  for (const s of SECTIONS) {
    const controls = s.fields
      .map((p) => control(st.schema, p, doc, (pointer, value) => apply(ctx, st, pointer, value), { disabled }))
      .filter(Boolean);
    if (!controls.length) continue;
    out.push(el("div", { class: "helm-panel" },
      el("div", { class: "helm-panel-header" }, el("span", { class: "helm-section-label", text: s.label })),
      el("div", { class: "helm-panel-body helm-stack" },
        s.hint ? el("p", { class: "helm-hint", text: s.hint }) : null,
        ...controls)));
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
  const localPath = valueAt(fields(st.check), "/local_path");

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
