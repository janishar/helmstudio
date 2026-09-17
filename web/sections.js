// The map from schema pointers to the editor's sections (03 §13a, M7 Q17).
//
// The form is *generated* from schema/manifest.json — the daemon serves the
// same bytes it validates against — so this file says nothing about types,
// enums, patterns or which fields are required. All it says is where each
// field belongs on screen, and which fields are deliberately not on it.
//
// That division is the whole point. A hand-written form would be a second copy
// of the schema and would silently lose a field the day the schema gained one.
// A generated form with no map would be a flat list of twenty-six properties
// in alphabetical order, which is not a form anybody wants to fill in. So the
// schema owns what a field *is*, and this owns where it *goes*.
//
// `sections_test.go` walks the schema and fails on any property that is in
// neither list. That is what makes "the schema gained a field" a failing test
// rather than a field nobody notices for a year.

/**
 * SECTIONS is the form, in the order it is drawn. Each field is a JSON pointer
 * into the manifest; the control is whatever the schema says that pointer is.
 *
 * The editor shows one section at a time (03 §13a, amended 2026-09-17), and
 * `slug` is how the address names it: `#/edit/<id>?section=runtime`.
 */
export const SECTIONS = [
  {
    slug: "identity",
    label: "Identity & source",
    hint: "What this studio is, and where its code comes from.",
    fields: ["/id", "/name", "/description", "/kinds", "/repo", "/ref", "/submodules", "/local_path", "/license", "/license_url"],
  },
  {
    slug: "runtime",
    label: "Runtime",
    hint: "How inference runs, and how much of the machine it wants.",
    fields: ["/runtime/framework", "/runtime/backends", "/runtime/precision", "/runtime/language", "/peak_ram_gb", "/python/version"],
  },
  {
    slug: "host",
    label: "Host requirements",
    hint: "Checked before the first build step, so a failure happens here rather than inside make.",
    fields: ["/requires/os", "/requires/arch", "/requires/tools", "/requires/ram_gb", "/requires/disk_gb"],
  },
  {
    slug: "weights",
    label: "Weights",
    hint: "The model files this studio runs on: what to download, and what it already has on this Mac.",
    fields: ["/weights"],
  },
  {
    slug: "platform",
    label: "Platform",
    hint: "What the studio asks helmstudio for. Every capability is read out as a sentence before anyone installs it.",
    fields: ["/capabilities", "/network", "/sdk/runtime", "/sdk/ui", "/sdk/css"],
  },
];

/**
 * YAML_ONLY is the other half of the map: the parts that are really text, with
 * the reason each one is.
 *
 * A pointer here covers everything beneath it. The reasons are not decoration
 * — they are what makes this list reviewable, because the alternative to a
 * stated reason is a field that fell off the form by accident.
 */
export const YAML_ONLY = {
  "/schema_version": "Set by the tooling, not described by hand.",
  "/hue": "Two OKLCH triples. A colour picker that wrote them would be a theme editor, which this is not.",
  "/build": "An ordered list of commands with their own cwd, shell and environment. Reordering steps is what editing it mostly is, and text does that better than a form.",
  "/processes": "The part with the most structure and the most comments — ports, health probes, dependencies, environment.",
  "/run": "Sugar for a single process, so it is the same text as `processes`.",
  "/test": "The smoke profile and its command, which the harness will own when it exists.",
  "/import": "One command with its cwd and shell, read next to the build steps it follows.",
  "/storage": "Collection declarations, which are a schema of their own.",
  "/python/extras": "A list of extras that reads next to the build steps that install them.",
};

/** The two sections that are not form: the whole text, and the criteria. */
export const TEXT = { slug: "yaml", label: "helmstudio.yaml" };
export const CHECKS = { slug: "criteria", label: "Certification criteria" };

/**
 * sectionOf is the slug of the section a pointer's field is edited in: a form
 * section when the pointer is one of its fields, under one, or a parent of
 * one (`/requires` is Host requirements), and the text when it is not on the
 * form. A registry entry's pointers are under /manifest, which is where its
 * form reads from, so that prefix is set aside first.
 */
export function sectionOf(pointer) {
  let p = String(pointer || "");
  if (p === "/manifest" || p.startsWith("/manifest/")) p = p.slice("/manifest".length);
  if (!p || p === "/") return TEXT.slug;
  for (const s of SECTIONS) {
    for (const f of s.fields) {
      if (f === p || f.startsWith(p + "/") || p.startsWith(f + "/")) return s.slug;
    }
  }
  return TEXT.slug;
}

/** A pointer's label: the last segment, spelled the way a person says it. */
const LABELS = {
  "/id": "Id",
  "/ref": "Ref",
  "/repo": "Repository",
  "/local_path": "Local path",
  "/license_url": "Licence URL",
  "/license": "Licence",
  "/peak_ram_gb": "Peak memory (GB)",
  "/requires/os": "Operating systems",
  "/requires/arch": "Architectures",
  "/requires/tools": "Tools",
  "/requires/ram_gb": "Minimum memory (GB)",
  "/requires/disk_gb": "Minimum disk (GB)",
  "/runtime/framework": "Framework",
  "/runtime/backends": "Backends",
  "/runtime/precision": "Precision",
  "/runtime/language": "Language",
  "/python/version": "Python",
  "/sdk/runtime": "Runtime SDK",
  "/sdk/ui": "UI SDK",
  "/sdk/css": "helm-css",
};

export function label(pointer) {
  if (LABELS[pointer]) return LABELS[pointer];
  const last = pointer.slice(pointer.lastIndexOf("/") + 1).replace(/_/g, " ");
  return last.charAt(0).toUpperCase() + last.slice(1);
}
