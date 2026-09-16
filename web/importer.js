// Import (03 §13a, docs/decisions.md M7 Q14, Q18).
//
// Import reports before it acts. It names a collision rather than resolving it
// silently, and it states plainly that adding to the library is not installing
// — the approval screen still stands between a stranger's manifest and
// anything executing.
//
// Files, drops and pasted text are read here and sent as text. The daemon
// never opens a path a request names, because a browser's file picker never
// gives a page an absolute path and a daemon that accepted one would be
// offering to read any file on the machine to whatever reached it first. A URL
// is fetched by the daemon under the rules in `internal/library/fetch.go`, and
// those are the strictest in the codebase for the same reason.

import { chip, el, failure, toast } from "./ui.js";

/**
 * importDialog collects items, reports on them, and adds the ones the person
 * agreed to. It resolves to the number added.
 */
export function importDialog(ctx) {
  return new Promise((resolve) => {
    // items are what will be sent: each is {text} or {url}, plus a label.
    const items = [];
    let report = null;     // the daemon's verdict per item
    let confirm = "";      // the digest that ties an add to the report it read
    const choices = new Map(); // index -> {action, new_id}
    let busy = false;
    let error = null;
    let added = 0;

    const node = el("dialog", { class: "helm-dialog helm-dialog-wide", "aria-labelledby": "import-title" });
    node.addEventListener("close", () => { node.remove(); resolve(added); });

    const add = (item) => { items.push(item); report = null; confirm = ""; choices.clear(); render(); };

    async function check() {
      if (!items.length) return;
      busy = true; error = null; render();
      try {
        const res = await ctx.client.manifests.import({ items: items.map(({ text, url }) => (url ? { url } : { text })) });
        report = res.items || [];
        confirm = res.confirm || "";
      } catch (err) {
        error = failure(err, "These could not be read.");
      }
      busy = false;
      render();
    }

    async function commit() {
      busy = true; error = null; render();
      try {
        const resolutions = [...choices].map(([index, c]) => ({ index, action: c.action, new_id: c.new_id || undefined }));
        const res = await ctx.client.manifests.import({
          items: items.map(({ text, url }) => (url ? { url } : { text })),
          confirm, resolutions,
        });
        report = res.items || [];
        added = report.filter((r) => r.added).length;
        await ctx.refresh();
        toast(added === 1 ? "1 manifest added to the library. Nothing was installed."
          : `${added} manifests added to the library. Nothing was installed.`, "info");
        node.close();
        return;
      } catch (err) {
        // `preview_changed` carries the new report, which is the one to read.
        const details = (err && err.details) || {};
        if (details.items) { report = details.items; confirm = details.confirm || ""; }
        error = failure(err, "Nothing was added.");
      }
      busy = false;
      render();
    }

    // ------------------------------------------------------------ the rows

    /** willAdd counts what pressing the button would actually do. */
    function willAdd() {
      return (report || []).filter((r) => {
        if (!r.valid || !r.id) return false;
        const c = choices.get(r.index);
        if (c && c.action === "skip") return false;
        if (r.collides_with && !(c && (c.action === "override" || (c.action === "rename" && c.new_id)))) return false;
        return true;
      }).length;
    }

    function row(r) {
      const item = items[r.index] || {};
      const tone = !r.valid ? "error" : r.collides_with ? "warning" : "running";
      const facts = [];
      if (r.valid) facts.push(r.kind === "pointer" ? "registry entry" : "manifest");
      if (r.collides_with) facts.push(`id already in your library from ${r.collides_with}`);
      else if (r.valid) facts.push("new entry");

      const body = el("div", { class: "helm-stack", style: "gap: var(--helm-space-1)" },
        el("div", { class: "helm-row" },
          chip(r.added ? "added" : r.valid ? (r.collides_with ? "collides" : "valid") : "not a manifest", r.added ? "running" : tone),
          el("span", { class: "helm-body", text: r.name || r.id || item.label || `item ${r.index + 1}` }),
          el("span", { class: "helm-spacer" }),
          el("span", { class: "helm-micro", text: item.label || "" })),
        el("p", { class: "helm-micro", text: facts.join(" · ") }));

      for (const e of r.errors || []) {
        body.append(el("p", { class: "helm-hint", text: [e.line ? `line ${e.line}` : e.pointer, e.message].filter(Boolean).join(": ") }));
      }
      for (const f of r.flags || []) {
        body.append(el("p", { class: "helm-hint helm-status-warning", text: f }));
      }
      if (r.collides_with && !r.added) body.append(collision(r));
      return el("div", { class: "helm-import-row" }, body);
    }

    /**
     * collision offers the three answers and no default. A collision with no
     * instruction is skipped rather than guessed at: silently replacing an
     * entry someone already has is the one thing import must not do.
     */
    function collision(r) {
      const c = choices.get(r.index) || {};
      const pick = (action) => { choices.set(r.index, { ...c, action }); render(); };
      const box = el("div", { class: "helm-row", style: "gap: var(--helm-space-2); flex-wrap: wrap" },
        ...["override", "rename", "skip"].map((a) => el("button", {
          class: "helm-btn helm-btn-sm " + (c.action === a ? "helm-btn-primary" : "helm-btn-secondary"),
          text: a[0].toUpperCase() + a.slice(1),
          "aria-pressed": String(c.action === a),
          onclick: () => pick(a),
        })));
      if (c.action === "rename") {
        box.append(el("input", {
          class: "helm-input", style: "max-width: 22ch", type: "text", value: c.new_id || "",
          "aria-label": `A new id for ${r.id}`, placeholder: `${r.id}-mine`,
          onchange: (e) => { choices.set(r.index, { ...c, new_id: e.target.value.trim() }); render(); },
        }));
      }
      if (!c.action) {
        box.append(el("span", { class: "helm-hint", text: "Nothing happens to this one until you choose." }));
      }
      return box;
    }

    // ----------------------------------------------------------- the dialog

    function inputs() {
      const paste = el("textarea", { class: "helm-input helm-yaml", rows: "4", spellcheck: "false",
        "aria-label": "Paste a manifest", placeholder: "Paste a manifest or a registry entry here" });
      const url = el("input", { class: "helm-input", type: "url", "aria-label": "A manifest's URL",
        placeholder: "https://example.com/studio.yaml" });
      const picker = el("input", { type: "file", multiple: true, accept: ".yaml,.yml,text/yaml",
        "aria-label": "Choose manifest files", onchange: async (e) => {
          for (const f of [...e.target.files]) add({ text: await f.text(), label: f.name });
          e.target.value = "";
        } });

      return el("div", { class: "helm-stack" },
        el("div", { class: "helm-field" },
          el("label", { class: "helm-label", text: "Paste text" }), paste,
          el("button", { class: "helm-btn helm-btn-secondary helm-btn-sm", text: "Add pasted text",
            onclick: () => { if (paste.value.trim()) add({ text: paste.value, label: "pasted" }); paste.value = ""; } })),
        el("div", { class: "helm-field" },
          el("label", { class: "helm-label", text: "From URL" }), url,
          el("span", { class: "helm-hint", text: "https only, and never an address on this machine or this network." }),
          el("button", { class: "helm-btn helm-btn-secondary helm-btn-sm", text: "Add URL",
            onclick: () => { if (url.value.trim()) add({ url: url.value.trim(), label: url.value.trim() }); url.value = ""; } })),
        el("div", { class: "helm-field" },
          el("label", { class: "helm-label", text: "Choose files" }), picker,
          el("span", { class: "helm-hint", text: "Files are read here and sent as text. helmstudio never opens a path a page names." })));
    }

    function render() {
      const n = willAdd();
      const total = (report || []).length;
      const body = el("div", { class: "helm-dialog-body helm-stack" });

      if (!report) {
        body.append(inputs());
        if (items.length) {
          body.append(el("p", { class: "helm-micro", text: `${items.length} to check: ${items.map((i) => i.label).join(", ")}` }));
        }
      } else {
        body.append(...report.map(row));
        body.append(el("p", { class: "helm-body", text:
          `${n} of ${total} will be added to the library. Nothing is installed by importing.` }));
        body.append(el("p", { class: "helm-hint", text:
          "A manifest is a list of commands. Nothing in one runs until you install the studio, and the approval screen shows every command before that happens." }));
      }
      if (error) body.append(el("p", { class: "helm-body helm-status-error", text: error }));

      const actions = el("div", { class: "helm-dialog-actions" },
        el("button", { class: "helm-btn helm-btn-secondary", text: "Cancel", onclick: () => node.close() }),
        report
          ? el("button", {
            class: "helm-btn helm-btn-primary helm-btn-lg", text: n === 1 ? "Add 1 to library" : `Add ${n} to library`,
            disabled: busy || n === 0, onclick: commit,
          })
          : el("button", {
            class: "helm-btn helm-btn-primary helm-btn-lg", text: "Check",
            disabled: busy || !items.length, onclick: check,
          }));

      node.replaceChildren(
        el("h2", { class: "helm-dialog-title", id: "import-title", text: "Import manifests" }),
        body, actions);
    }

    // A dropped file is the same as a chosen one: read here, sent as text.
    node.addEventListener("dragover", (e) => e.preventDefault());
    node.addEventListener("drop", async (e) => {
      e.preventDefault();
      for (const f of [...(e.dataTransfer.files || [])]) add({ text: await f.text(), label: f.name });
    });

    render();
    document.body.append(node);
    node.showModal();
  });
}
