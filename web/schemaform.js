// The editor's form, generated from schema/manifest.json (03 §13a, M7 Q17).
//
// Nothing here knows what a manifest contains. It is handed a schema and a
// pointer and builds whatever control that pointer turns out to be — a select
// for an enum, a checkbox group for a list of them, a number for an integer.
// The day the schema gains a field, `web/sections.js` names where it goes and
// the control appears; nothing in this file changes.
//
// The schema is fetched from the daemon, which serves the same bytes it
// validates against. A copy compiled into the page would be a second contract,
// and the first person to notice the two had drifted would be someone whose
// valid-looking manifest was refused on save.

import { el } from "./ui.js";
import { label } from "./sections.js";

const SCHEMA_URL = "/schema/manifest.json";

let cached = null;

/** loadSchema fetches the contract once per page. */
export async function loadSchema(fetchImpl) {
  if (cached) return cached;
  const f = fetchImpl || ((...a) => fetch(...a));
  const res = await f(SCHEMA_URL);
  if (!res.ok) throw new Error(`the manifest schema could not be read (${res.status})`);
  cached = await res.json();
  return cached;
}

/** setSchema hands the page a schema directly, which is what a fixture does. */
export function setSchema(doc) {
  cached = doc;
}

/** deref follows a local $ref, so /sdk/runtime is the string it resolves to. */
function deref(schema, node) {
  if (!node || typeof node.$ref !== "string" || !node.$ref.startsWith("#/")) return node;
  let cur = schema;
  for (const seg of node.$ref.slice(2).split("/")) {
    if (!cur) return node;
    cur = cur[seg];
  }
  return cur ? deref(schema, cur) : node;
}

/**
 * nodeAt is the schema for a pointer, walking `properties` as it goes — and
 * `items` when the pointer names one of a list's entries, as `/weights/0/repo`
 * does.
 */
export function nodeAt(schema, pointer) {
  let node = schema;
  for (const seg of pointer.slice(1).split("/")) {
    if ((node || {}).type === "array" && /^\d+$/.test(seg)) {
      node = deref(schema, node.items);
      continue;
    }
    const props = (node || {}).properties;
    if (!props || !props[seg]) return null;
    node = deref(schema, props[seg]);
  }
  return node;
}

/**
 * requiredAt reports whether a field really has to be filled in.
 *
 * Every ancestor has to be required too. `python.version` is required *within*
 * `python`, but nothing requires a manifest to declare `python` at all — so a
 * form that put a star on it would be asking for something optional, and the
 * person filling it in has no way to know which kind of required it meant.
 */
function requiredAt(schema, pointer) {
  let at = "";
  for (const seg of pointer.slice(1).split("/")) {
    const parent = at === "" ? schema : nodeAt(schema, at);
    // An entry of a list is not required or optional; it is there. What the
    // entry requires is asked of the entry.
    if (!((parent || {}).type === "array" && /^\d+$/.test(seg))) {
      if (!((parent || {}).required || []).includes(seg)) return false;
    }
    at += "/" + seg;
  }
  return true;
}

/** valueAt reads a pointer out of the document the daemon decoded for us. */
export function valueAt(document, pointer) {
  let cur = document;
  for (const seg of pointer.slice(1).split("/")) {
    if (cur === null || typeof cur !== "object") return undefined;
    cur = cur[seg];
  }
  return cur;
}

/** enumOf is the list a node offers, whether it is the node or its items. */
function enumOf(node) {
  if (Array.isArray(node.enum)) return node.enum;
  if (node.type === "array" && node.items && Array.isArray(node.items.enum)) return node.items.enum;
  return null;
}

/**
 * missingAncestors lists the parents a pointer needs before it can be set, or
 * returns null when the edit cannot be made safely.
 *
 * The daemon refuses an edit whose parent does not exist, deliberately: an
 * editor that invents the shape of what is missing writes something nobody
 * asked for. So the page says exactly which empty maps it wants, in order, and
 * each one is an edit of its own.
 *
 * It only ever creates a parent it has *seen* to be absent. With no document —
 * text that is not YAML — it cannot see anything, and a parent it guessed was
 * missing would be written as an empty map over the real one, taking every
 * sibling field with it. The same goes for a parent that exists but is not a
 * mapping. Both answer null, and nothing is sent.
 */
export function missingAncestors(document, pointer) {
  const isMap = (v) => v !== null && typeof v === "object" && !Array.isArray(v);
  if (!isMap(document)) return null;
  const out = [];
  let cur = document;
  let at = "";
  let creating = false;
  for (const seg of pointer.slice(1).split("/").slice(0, -1)) {
    at += "/" + seg;
    if (creating) {
      out.push(at);
      continue;
    }
    const next = cur[seg];
    if (next === undefined || next === null) {
      // A list's entry is never invented: it is added by the button that adds
      // one, so that nothing appears in a manifest nobody asked for.
      if (Array.isArray(cur)) return null;
      out.push(at);
      creating = true;
    } else if (!isMap(next) && !Array.isArray(next)) {
      return null;
    } else {
      cur = next;
    }
  }
  return out;
}

/**
 * control builds one labelled field. onChange is called with the pointer and
 * the new value — never with text, and never with a path.
 *
 * Empty is sent as null, which removes the key rather than writing `field: ""`.
 * A manifest with `license: ""` in it says something different from one without
 * a licence, and only one of those is what a person clearing a box meant.
 */
export function control(schema, pointer, document, onChange, { disabled = false } = {}) {
  const node = nodeAt(schema, pointer);
  if (!node) return null;
  const value = valueAt(document, pointer);
  const id = "f" + pointer.replace(/\//g, "-");
  const required = requiredAt(schema, pointer);
  const choices = enumOf(node);

  // A list of objects — weights, build steps, collections — is a list of
  // entries rather than one control, so it takes the whole width and has its
  // own heading rather than a label beside a box.
  if (node.type === "array" && (itemsOf(schema, node) || {}).type === "object") {
    return objectList(schema, pointer, node, document, onChange, { disabled });
  }

  let input;
  if (node.type === "array" && choices) {
    input = choiceList(id, choices, Array.isArray(value) ? value : [], (v) => onChange(pointer, v.length ? v : null));
  } else if (choices) {
    input = el("select", { class: "helm-select", id, onchange: (e) => onChange(pointer, e.target.value || null) },
      el("option", { value: "", text: required ? "— choose —" : "— none —", selected: value === undefined || value === null }),
      ...choices.map((c) => el("option", { value: c, text: c, selected: c === value })));
  } else if (node.type === "array") {
    // A list of free text: hostnames, tool names. Comma-separated, because
    // none of them may contain a comma and a row of inputs for three strings
    // is more machinery than the strings are worth.
    input = el("input", {
      class: "helm-input", id, type: "text", value: (value || []).join(", "),
      onchange: (e) => {
        const list = e.target.value.split(",").map((s) => s.trim()).filter(Boolean);
        onChange(pointer, list.length ? list : null);
      },
    });
  } else if (node.type === "integer" || node.type === "number") {
    input = el("input", {
      class: "helm-input", id, type: "number", inputmode: "numeric",
      value: value === undefined || value === null ? "" : String(value),
      min: node.minimum === undefined ? undefined : String(node.minimum),
      onchange: (e) => onChange(pointer, e.target.value === "" ? null : Number(e.target.value)),
    });
  } else if (node.type === "boolean") {
    input = el("input", { class: "helm-checkbox", id, type: "checkbox", checked: value === true,
      onchange: (e) => onChange(pointer, e.target.checked ? true : null) });
  } else {
    input = el("input", {
      class: "helm-input", id, type: "text", value: value === undefined || value === null ? "" : String(value),
      onchange: (e) => onChange(pointer, e.target.value === "" ? null : e.target.value),
    });
  }
  if (required) input.setAttribute("aria-required", "true");
  if (disabled) {
    for (const c of [input, ...input.querySelectorAll("input")]) c.disabled = true;
  }

  return el("div", { class: "helm-field" },
    el("label", { class: "helm-label", for: id, text: label(pointer) + (required ? " *" : "") }),
    input,
    node.description ? el("span", { class: "helm-hint", text: node.description }) : null);
}

/** itemsOf is what a list holds, with its $ref followed. */
function itemsOf(schema, node) {
  return node.items ? deref(schema, node.items) : null;
}

/**
 * entryName is what an entry of a list is called in its heading: its own name
 * where it has one, and its place in the list where it does not.
 */
function entryName(item, i) {
  const named = item && typeof item === "object" ? item.name || item.run || item.repo : null;
  return named ? String(named) : `${i + 1}`;
}

/**
 * objectList draws a list of objects: one block per entry with the entry's own
 * fields inside it, a Remove on each, and an Add at the end.
 *
 * Every change still leaves as a JSON pointer and a value, which is what the
 * daemon applies to the YAML node tree: `/weights/2/local_path` sets a field,
 * `/weights/2` with null removes the entry, and the pointer one past the end
 * appends one. Nothing here rewrites the document.
 */
function objectList(schema, pointer, node, document, onChange, { disabled = false } = {}) {
  const items = valueAt(document, pointer);
  const list = Array.isArray(items) ? items : [];
  const item = itemsOf(schema, node) || {};
  const properties = Object.keys(item.properties || {});
  const singular = label(pointer).replace(/s$/, "").toLowerCase();

  const entries = list.map((entry, i) => {
    const at = `${pointer}/${i}`;
    const controls = properties
      .map((name) => control(schema, `${at}/${name}`, document, onChange, { disabled }))
      .filter(Boolean);
    return el("div", { class: "helm-entry", "data-key": at },
      el("div", { class: "helm-entry-head" },
        el("span", { class: "helm-entry-name", text: `${singular} ${entryName(entry, i)}` }),
        el("span", { class: "helm-spacer" }),
        el("button", {
          class: "helm-btn helm-btn-ghost helm-btn-sm", type: "button", text: "Remove", disabled,
          "aria-label": `Remove ${singular} ${entryName(entry, i)}`,
          onclick: () => onChange(at, null),
        })),
      el("div", { class: "helm-entry-fields" }, ...controls));
  });

  return el("div", { class: "helm-field helm-list" },
    el("div", { class: "helm-list-head" },
      el("span", { class: "helm-label", text: label(pointer) }),
      el("span", { class: "helm-spacer" }),
      el("button", {
        class: "helm-btn helm-btn-secondary helm-btn-sm", type: "button", text: `Add a ${singular}`, disabled,
        // One past the end appends; the entry starts empty and the errors
        // under the text say what it still needs.
        onclick: () => onChange(`${pointer}/${list.length}`, {}),
      })),
    node.description ? el("span", { class: "helm-hint", text: node.description }) : null,
    ...entries,
    list.length ? null : el("p", { class: "helm-hint", text: `No ${label(pointer).toLowerCase()} are declared.` }));
}

/**
 * choiceList is a list of enum values as checkboxes: `kinds` is a list because
 * ltx is video AND audio, and a multi-select that hides its options behind a
 * scroll is the control people get wrong.
 */
function choiceList(id, choices, selected, onChange) {
  const chosen = new Set(selected);
  const group = el("div", { class: "helm-choices", id, role: "group" });
  for (const c of choices) {
    const box = el("input", {
      type: "checkbox", checked: chosen.has(c),
      onchange: (e) => {
        if (e.target.checked) chosen.add(c);
        else chosen.delete(c);
        onChange(choices.filter((x) => chosen.has(x)));
      },
    });
    group.append(el("label", { class: "helm-choice" }, box, document.createTextNode(c)));
  }
  return group;
}
