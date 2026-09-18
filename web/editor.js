// The editor (03 §13a, docs/decisions.md M7 Q16, Q17, Q19, and the launcher
// redesign of 2026-09-17).
//
// Most repositories worth running will never ship a manifest, so writing one
// has to be a first-class act rather than a fallback. A menu of sections on the
// left and one section at a time in the centre: the form's four sections, the
// whole text as helmstudio.yaml, and the criteria. Both views stay live — a
// form edit comes back as text, and the text's verdict counts against the
// sections it is about. The section is in the address, so a link, Back and a
// reload land on it.
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

import { chip, el, failure, saveText, toast } from "./ui.js";
import { CHECKS, SECTIONS, TEXT, YAML_ONLY, label, sectionOf } from "./sections.js";
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
    st.error = "The text is not YAML yet, so the form cannot tell what it would be changing. Fix it in helmstudio.yaml first.";
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
    if (id !== st.id) ctx.go(`#/edit/${encodeURIComponent(id)}`);
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
  saveText(`${id}.yaml`, st.text);
  if (navigator.clipboard) navigator.clipboard.writeText(st.text).catch(() => {});
  toast(`${id}.yaml downloaded and copied.`, "info");
}

/** localWeights names the weights this manifest links from a folder. */
function localWeights(st) {
  const list = valueAt(fields(st.check), "/weights");
  if (!Array.isArray(list)) return [];
  return list.filter((w) => w && w.local_path).map((w) => w.name || "a weight");
}

function declaredID(check) {
  const doc = (check || {}).document;
  if (!doc) return "";
  return valueAt(doc, "/id") || valueAt(doc, "/manifest/id") || "";
}

// ------------------------------------------------------------------ drawing

/** The verdict chip in the header: valid, or the count of what is not. */
function verdict(check) {
  if (!check) return chip("Checking", "idle");
  const errors = (check.errors || []).length;
  if (check.valid) return chip("Valid", "running");
  return chip(`${errors} error${errors === 1 ? "" : "s"}`, "error");
}

/** errorsBySection counts what is wrong against the section it is about. */
function errorsBySection(check) {
  const out = {};
  for (const e of (check || {}).errors || []) {
    const slug = sectionOf(e.pointer);
    out[slug] = (out[slug] || 0) + 1;
  }
  return out;
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
  return el("div", { class: "helm-stack helm-editor-problems" },
    ...errors.map((e) => el("div", { class: "helm-stack", style: "gap: 0" },
      el("p", { class: "helm-mono helm-status-error", text: [e.line ? `line ${e.line}` : null, e.pointer].filter(Boolean).join(" · ") }),
      el("p", { class: "helm-body", text: e.message }))));
}

/** The criteria score, out of what a manifest alone can answer. */
function score(check) {
  const c = (check || {}).criteria;
  return c ? `${c.passed} of ${c.checkable}` : null;
}

/**
 * textOnly names the parts that have no form, read from the map rather than
 * written out here: a list in prose goes on naming a field the day it gains a
 * section, and this one cannot.
 */
function textOnly() {
  const top = Object.keys(YAML_ONLY).map((p) => p.slice(1).split("/")[0]);
  const names = [...new Set(top)].filter((n) => !["schema_version", "hue"].includes(n));
  return names.length > 1 ? `${names.slice(0, -1).join(", ")} and ${names[names.length - 1]}` : names[0];
}

/** link is the address of one of this editor's sections. */
function link(id, slug) {
  return `${id === "new" ? "#/edit" : `#/edit/${encodeURIComponent(id)}`}?section=${slug}`;
}

/**
 * The menu (03 §13a, amended): the form's sections with their error counts,
 * then the text with its verdict, then the criteria with their score.
 */
function sectionMenu(st, current) {
  const errors = errorsBySection(st.check);
  const item = (slug, labelText, aside, mono = false) => el("a", {
    class: "helm-editor-link", href: link(st.id, slug), "data-key": slug,
    "aria-current": slug === current ? "true" : undefined,
  },
    el("span", { class: mono ? "helm-mono" : undefined, text: labelText }),
    el("span", { class: "helm-spacer" }),
    aside);
  const count = (slug) => errors[slug]
    ? el("span", { class: "helm-editor-count helm-status-error", text: `${errors[slug]} error${errors[slug] === 1 ? "" : "s"}` })
    : null;

  const verdictWord = !st.check ? "checking" : st.check.valid ? "valid" : `${(st.check.errors || []).length} errors`;
  return el("nav", { class: "helm-editor-menu", "aria-label": "Manifest sections" },
    el("span", { class: "helm-editor-menu-label", text: "Form" }),
    ...SECTIONS.map((s) => item(s.slug, s.label, count(s.slug))),
    el("span", { class: "helm-editor-menu-label", text: "Text" }),
    item(TEXT.slug, TEXT.label, el("span", {
      class: "helm-editor-count" + (st.check && !st.check.valid ? " helm-status-error" : ""), text: verdictWord,
    }), true),
    el("span", { class: "helm-hint helm-editor-menu-note", text: `${textOnly()} are edited as text` }),
    el("span", { class: "helm-editor-menu-label", text: "Checks" }),
    item(CHECKS.slug, CHECKS.label, score(st.check) ? el("span", { class: "helm-mono helm-editor-count", text: score(st.check) }) : null));
}

/** heading is a section's title and sentence; the title is where focus lands. */
function heading(id, title, hint) {
  return el("div", { class: "helm-editor-section-head" },
    el("h2", { class: "helm-editor-section-title", id, "data-section-heading": "", tabindex: "-1", text: title }),
    hint ? el("p", { class: "helm-body helm-editor-section-hint", text: hint }) : null);
}

/** formSection is one of the form's sections, with the next and previous linked. */
function formSection(ctx, st, section) {
  const doc = fields(st.check);
  // No document means the text is not YAML: every control is shown, and none
  // of them can be used until the text parses again.
  const disabled = !(st.check && st.check.document);
  const controls = section.fields
    .map((p) => control(st.schema, p, doc, (pointer, value) => apply(ctx, st, pointer, value), { disabled }))
    .filter(Boolean);
  const at = SECTIONS.indexOf(section);
  const prev = SECTIONS[at - 1];
  const next = SECTIONS[at + 1] || TEXT;

  const notes = [];
  if (disabled && st.check) {
    notes.push(el("p", { class: "helm-hint helm-status-warning", text: "The text is not YAML yet, so the form cannot tell what it would be changing. Fix it in helmstudio.yaml, and the form comes back." }));
  }
  if (isPointer(st.check)) {
    notes.push(el("p", { class: "helm-hint", text: (st.check.document || {}).manifest
      ? "This is a registry entry. The fields below are the manifest it carries inline, and its id, repository and ref are kept in step with the entry's own."
      : "This is a registry entry with no inline manifest: the repository's own helmstudio.yaml describes it. Filling in the form writes a manifest here, which takes precedence." }));
  }

  return el("section", { class: "helm-panel helm-editor-section", "aria-labelledby": `section-${section.slug}`, "data-key": section.slug },
    heading(`section-${section.slug}`, section.label, section.hint),
    notes.length ? el("div", { class: "helm-editor-notes helm-stack" }, ...notes) : null,
    el("div", { class: "helm-editor-fields" }, ...controls),
    el("div", { class: "helm-editor-pager" },
      prev ? el("a", { class: "helm-editor-prev", href: link(st.id, prev.slug), text: prev.label }) : null,
      el("span", { class: "helm-spacer" }),
      el("a", { class: "helm-editor-next", href: link(st.id, next.slug), text: next.label })));
}

/** errorLines are the lines the daemon faulted, each once, in the order given. */
function errorLines(check) {
  const out = new Set();
  for (const e of (check || {}).errors || []) if (e.line) out.add(e.line);
  return [...out];
}

/** paneOf is the box's own wrapper; ruleOf is the numbered rule inside it. */
function paneOf(area) {
  return area.closest(".helm-editor-yaml-wrap");
}

function ruleOf(area) {
  return paneOf(area).querySelector(".helm-editor-rule");
}

/**
 * paint draws the rule: one row per line of the box, numbered, and the rows
 * the daemon faulted marked.
 *
 * It counts the box's newlines and reads nothing else out of the text. Which
 * line a fault is on came from the daemon, which is the only thing here that
 * has read the document — the rule only puts the number where the eye is.
 *
 * The box is read rather than `st.text` because between a keystroke and the
 * next verdict the box is the newer of the two, and a line that has just been
 * typed should be numbered before it has been checked.
 */
function paint(area) {
  const rows = ruleOf(area);
  const want = area.value.split("\n").length;
  while (rows.childElementCount > want) rows.lastElementChild.remove();
  while (rows.childElementCount < want) {
    rows.append(el("div", { class: "helm-editor-line" },
      el("span", { class: "helm-editor-line-no", text: String(rows.childElementCount + 1) })));
  }
  // The marks are read back from the pane because an input event has no st:
  // typing renumbers the rule, and the verdict it is marked against is
  // whichever one the last redraw left here.
  const marked = new Set((paneOf(area).dataset.errorLines || "").split(" "));
  for (let i = 0; i < rows.childElementCount; i++) {
    const row = rows.children[i];
    if (marked.has(String(i + 1))) row.setAttribute("data-state", "error");
    else row.removeAttribute("data-state");
  }
  track(area);
}

/** track keeps the rule level with the text as the box scrolls. */
function track(area) {
  ruleOf(area).style.transform = `translateY(${-area.scrollTop}px)`;
}

/**
 * textSection is the whole manifest as text, in a pane of its own (canvas
 * option B's, inside option A's centre). The parts that are really text are
 * edited here, and a form edit shows up here as the daemon wrote it.
 *
 * The box and its rule are kept together across redraws: the box so the caret
 * and the scroll position survive a poll, the rule because it is painted from
 * what the box holds rather than drawn from st, and a redraw must not put a
 * stale set of numbers back.
 */
function textSection(ctx, st) {
  const pane = ctx.keep("editor-yaml:" + st.id, () => {
    const box = el("textarea", {
      class: "helm-yaml helm-editor-yaml", spellcheck: "false", "aria-label": "The manifest as text",
      onchange: (e) => revalidate(ctx, st, e.target.value),
      // A line typed now is a line the rule has to number now; the verdict
      // that will mark it is a round trip away.
      oninput: (e) => paint(e.currentTarget),
      onscroll: (e) => track(e.currentTarget),
    });
    // The rule is spoken by the error list under the box, which says the same
    // lines in words, so it is hidden from a reader rather than read twice.
    //
    // It is clipped by a layer of its own rather than by the pane, so that the
    // box's focus ring — which is drawn outside its edge — is not clipped away
    // with it (03 §15: never suppressed).
    return el("div", { class: "helm-editor-yaml-wrap" },
      el("div", { class: "helm-editor-rule-clip", "aria-hidden": "true" },
        el("div", { class: "helm-editor-rule" })), box);
  });
  const area = pane.querySelector("textarea");
  if (document.activeElement !== area) area.value = st.text;
  pane.dataset.errorLines = errorLines(st.check).join(" ");
  paint(area);

  const verdictLine = !st.check ? "checking"
    : st.check.valid ? "valid · edits here and in the form stay in step"
      : `${(st.check.errors || []).length} error${(st.check.errors || []).length === 1 ? "" : "s"} · listed under the text`;
  return el("section", { class: "helm-panel helm-editor-section helm-editor-text", "aria-labelledby": "section-yaml", "data-key": TEXT.slug },
    el("div", { class: "helm-editor-text-head" },
      el("h2", { class: "helm-mono helm-editor-text-title", id: "section-yaml", "data-section-heading": "", tabindex: "-1", text: TEXT.label }),
      el("span", { class: "helm-micro", text: verdictLine })),
    pane,
    problems(st.check),
    el("p", { class: "helm-hint helm-editor-text-note", text:
      `Edited only as text: ${Object.keys(YAML_ONLY).map((p) => p.slice(1)).join(", ")}.` }));
}

/**
 * criteriaSection is 05 §9's list, scored out of what a manifest alone can
 * answer, with each criterion that is about a field linked to its section.
 *
 * "7 of 15" reads as a failing grade for a studio that did everything a
 * manifest can do, so the count is out of the checkable ones and the rest say
 * what they are waiting for.
 */
function criteriaSection(st) {
  const c = (st.check || {}).criteria;
  const where = (pointer) => {
    const slug = sectionOf(pointer);
    const s = SECTIONS.find((x) => x.slug === slug) || TEXT;
    return el("a", { class: "helm-link helm-micro", href: link(st.id, s.slug), text: `Go to ${s.label}` });
  };
  const rows = c ? c.items || [] : [];
  return el("section", { class: "helm-panel helm-editor-section", "aria-labelledby": "section-criteria", "data-key": CHECKS.slug },
    heading("section-criteria", CHECKS.label, c
      ? `${c.passed} of ${c.checkable} checkable pass. The rest need what a manifest cannot give: a smoke harness, the studio's source or its stylesheets.`
      : "Criteria are scored once the manifest is valid."),
    rows.length ? el("ol", { class: "helm-criteria" },
      ...rows.map((r) => el("li", { class: "helm-criterion" },
        chip(r.state === "pass" ? "pass" : r.state === "fail" ? "fail" : "not checked",
          r.state === "pass" ? "running" : r.state === "fail" ? (r.required ? "error" : "warning") : "idle"),
        el("div", { class: "helm-stack", style: "gap: 2px" },
          el("span", { class: "helm-body", text: `${r.number}. ${r.title}${r.required ? "" : " (recommended)"}` }),
          r.detail ? el("span", { class: "helm-hint", text: r.detail }) : null,
          r.pointer ? where(r.pointer) : null)))) : null);
}

export function editor(ctx, id) {
  const st = editorState(ctx, id || "new");
  if (!st.loaded && !st.busy) load(ctx, st);

  const declared = declaredID(st.check) || (id === "new" ? "" : id);
  const file = (st.check && st.check.valid && declared)
    ? `studios/${declared}.yaml`
    : "not saved yet";
  const localPath = valueAt(fields(st.check), "/local_path");
  const asked = ctx.query.get("section") || SECTIONS[0].slug;
  const current = [...SECTIONS.map((s) => s.slug), TEXT.slug, CHECKS.slug].includes(asked) ? asked : SECTIONS[0].slug;

  const header = el("div", { class: "helm-page-header helm-editor-header" },
    el("h1", { class: "helm-title", text: (id || "new") === "new" ? "New studio" : declared || id }),
    el("span", { class: "helm-mono helm-meta", text: file }),
    st.loaded ? el("span", { class: "helm-editor-verdict" }, verdict(st.check)) : null,
    st.loaded && score(st.check) ? el("span", { class: "helm-mono helm-meta", text: `${score(st.check)} checkable pass` }) : null,
    st.dirty ? el("span", { class: "helm-unsaved" }, el("span", { class: "helm-dot", "aria-hidden": "true" }), document.createTextNode("Unsaved changes")) : null,
    el("span", { class: "helm-spacer" }),
    el("button", { class: "helm-btn helm-btn-secondary", type: "button", text: "Import file",
      onclick: () => importDialog(ctx) }),
    el("button", { class: "helm-btn helm-btn-secondary", type: "button", text: "Export",
      onclick: () => exportManifest(st) }),
    el("button", {
      class: "helm-btn helm-btn-primary", type: "button", text: "Save to library",
      disabled: st.busy || !st.check || !st.check.valid,
      onclick: () => save(ctx, st),
    }));

  if (!st.loaded) {
    return el("div", { class: "helm-stack" }, header,
      el("div", { class: "helm-panel" }, el("div", { class: "helm-panel-body" },
        el("p", { class: "helm-micro", text: "Reading…" }))));
  }

  const section = SECTIONS.find((s) => s.slug === current);
  return el("div", { class: "helm-stack" },
    header,
    st.error ? el("p", { class: "helm-body helm-status-error", text: st.error }) : null,
    // Export hands back a file to commit upstream, and a manifest that builds
    // a directory on this Mac describes nothing anyone else can clone.
    localPath ? el("p", { class: "helm-hint", text:
      `This manifest builds ${localPath} on this Mac. Exported as it is, it will not work on anyone else's.` }) : null,
    // The same is true of a weight linked from a folder: it is this machine's
    // path, and nobody else has it.
    localWeights(st).length ? el("p", { class: "helm-hint", text:
      `${localWeights(st).join(" and ")} ${localWeights(st).length === 1 ? "is linked" : "are linked"} from a folder on this Mac, so ${localWeights(st).length === 1 ? "it is" : "they are"} not downloaded. Exported as it is, that path is nobody else's.` }) : null,
    st.check && !st.check.valid && st.dirty
      ? el("p", { class: "helm-hint", text: "Saving is offered once this is valid. The library lists an invalid manifest rather than hiding it, but writing one from here would be helmstudio breaking your library for you." })
      : null,
    el("div", { class: "helm-editor" },
      sectionMenu(st, current),
      section ? formSection(ctx, st, section)
        : current === TEXT.slug ? textSection(ctx, st)
          : criteriaSection(st)));
}
