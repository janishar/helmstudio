// Models and disk (03 §12, amended by the launcher redesign of 2026-09-17).
//
// "Used by" is the reference count made visible. An artifact used by two
// studios is not deletable without a confirmation naming both; one used by
// none is marked Orphaned and is the only thing Reclaim touches. Orphaned is
// a word, not a stored state — M3 removed the stored count, not the idea.
//
// A linked folder is the user's: helmstudio never counts, moves or deletes
// it, so its size reads "not counted" and its action is Unlink. Row actions
// are quiet; red appears only in the confirmation, because a red Delete on
// every row makes a list of models look like a list of hazards.
//
// No Verify: weight verification is still open, and a button that claims to
// have checked something it has not is worse than no button (M6 Q21).

import { ago, bytes, chip, copyButton, dialog, el, failure, progress, toast } from "./ui.js";

/** disk is the free space on the models volume, read when the screen is. */
async function loadDisk(ctx) {
  try {
    ctx.store.disk = await ctx.client.models.disk();
  } catch (err) {
    ctx.store.disk = { error: failure(err, "Free space could not be read.") };
  }
  ctx.redraw(true);
}

function source(m) {
  if (m.source === "linked") {
    return el("div", { class: "helm-stack helm-model-source" },
      el("span", { class: "helm-body", text: m.state === "missing" ? "Linked · not there" : "Linked" }),
      el("span", { class: "helm-mono helm-model-path", text: m.external_path || m.path }));
  }
  const downloading = m.state === "downloading" || m.state === "interrupted";
  if (downloading && m.total_bytes) {
    const pct = Math.floor((m.bytes_on_disk / m.total_bytes) * 100);
    return el("div", { class: "helm-stack helm-model-source" },
      el("span", { class: "helm-body", text: `${m.state === "interrupted" ? "Interrupted" : "Downloading"} · ${pct}%` }),
      progress(m.bytes_on_disk, m.total_bytes),
      el("span", { class: "helm-mono", text: `${bytes(m.bytes_on_disk)} of ${bytes(m.total_bytes)}` }));
  }
  const word = { ready: "Downloaded", declared: "Not downloaded", auth_required: "Needs a Hugging Face token" }[m.state] || m.state;
  return el("div", { class: "helm-stack helm-model-source" },
    el("span", { class: "helm-body", text: word }),
    el("span", { class: "helm-mono helm-model-path", text: m.path }));
}

function usedBy(ctx, m) {
  const ids = m.studios || [];
  if (!ids.length) return chip("Orphaned", "warning");
  const names = ids.map((id) => (ctx.store.studios.find((s) => s.id === id) || { name: id }).name);
  return el("span", { class: "helm-body", text: names.join(", ") });
}

export function modelsAndDisk(ctx) {
  if (ctx.store.disk === undefined) {
    ctx.store.disk = null;
    loadDisk(ctx);
  }
  const models = ctx.store.models || [];
  const used = models.reduce((n, m) => n + (m.source === "managed" ? m.bytes_on_disk || 0 : 0), 0);
  const orphans = models.filter((m) => m.source === "managed" && !(m.studios || []).length);
  const reclaimable = orphans.reduce((n, m) => n + (m.bytes_on_disk || 0), 0);
  const disk = ctx.store.disk;
  const summary = [`${bytes(used)} used`, disk && disk.free_bytes !== undefined ? `${bytes(disk.free_bytes)} free` : null]
    .filter(Boolean).join(" · ");

  const header = el("div", { class: "helm-page-header" },
    el("h1", { class: "helm-title", text: "Models & disk" }),
    ctx.store.loaded ? el("span", { class: "helm-meta", text: summary }) : null,
    el("span", { class: "helm-spacer" }),
    orphans.length
      ? el("button", {
        class: "helm-btn helm-btn-secondary", type: "button",
        text: `Reclaim orphaned · ${bytes(reclaimable)}`,
        onclick: () => reclaim(ctx),
      })
      : null);

  const rows = models.map((m) => {
    const downloading = m.state === "downloading" || m.state === "interrupted";
    const where = m.source === "linked" ? m.external_path || m.path : m.path;
    return el("tr", { "data-key": m.id },
      el("td", {},
        el("div", { class: "helm-stack helm-model-name" },
          el("span", { class: "helm-body", text: m.hf_repo }),
          el("span", { class: "helm-mono", text: m.revision || "" }))),
      el("td", {}, source(m)),
      el("td", { class: "helm-num" }, el("span", { class: "helm-mono", text: m.source === "linked" ? "not counted" : bytes(m.bytes_on_disk) })),
      el("td", {}, usedBy(ctx, m)),
      el("td", {}, el("span", { class: "helm-mono", text: ago(m.last_used_at) })),
      el("td", { class: "helm-row-actions" },
        where ? copyButton(where, `Copy the path of ${m.hf_repo}`, "The path") : null,
        downloading
          ? null
          : el("button", {
            class: "helm-btn helm-btn-ghost helm-btn-sm", type: "button",
            text: m.source === "linked" ? "Unlink" : "Delete…",
            "aria-label": `${m.source === "linked" ? "Unlink" : "Delete"} ${m.hf_repo}`,
            onclick: () => remove(ctx, m),
          })));
  });

  const table = el("div", { class: "helm-table-scroll" },
    el("table", { class: "helm-table helm-models" },
      el("thead", {}, el("tr", {},
        ...["Model", "Source", "Size", "Used by", "Last used"].map((h) => el("th", { scope: "col", text: h })),
        el("th", { scope: "col", class: "helm-num" }, el("span", { class: "helm-visually-hidden", text: "Actions" })))),
      el("tbody", {}, ...rows)));

  return el("div", { class: "helm-stack" }, header,
    disk && disk.error ? el("p", { class: "helm-hint helm-status-warning", text: disk.error }) : null,
    el("div", { class: "helm-panel" },
      !ctx.store.loaded
        ? el("div", { class: "helm-panel-body" }, el("p", { class: "helm-micro", text: "Reading…" }))
        : models.length
          ? table
          : el("div", { class: "helm-panel-body" }, el("p", { class: "helm-micro", text: "No weights have been downloaded or linked yet." }))),
    models.length
      ? el("p", { class: "helm-hint", text: "A linked folder is yours: helmstudio never counts, moves or deletes it. Delete asks first, and names every studio that uses the model." })
      : null);
}

/**
 * remove unlinks a linked directory — never the user's own files — or reclaims
 * a download, which needs the confirm from its own preview so that what is
 * deleted is exactly what was shown.
 */
async function remove(ctx, m) {
  const studios = (m.studios || []).map((id) => (ctx.store.studios.find((s) => s.id === id) || { name: id }).name);
  if (m.source === "linked") {
    const chosen = await dialog({
      title: `Unlink ${m.hf_repo}?`,
      body: [el("p", { class: "helm-body", text: `helmstudio stops using ${m.external_path || "that directory"}. The directory itself is not touched — helmstudio has never written to it.` })],
      actions: [{ label: "Cancel", value: null }, { label: "Unlink", value: "go", class: "helm-btn-danger-fill", primary: true }],
    });
    if (chosen !== "go") return;
    return call(ctx, () => ctx.client.models.delete(m.id), `${m.hf_repo} could not be unlinked.`);
  }

  let preview;
  try {
    const view = await ctx.client.models.get(m.id);
    preview = view.reclaim;
  } catch (err) {
    return toast(failure(err, "That artifact could not be read."), "error");
  }

  const body = [
    el("p", { class: "helm-body", text: `${bytes(m.bytes_on_disk)} is deleted from the cache. Downloading it again needs the network and the time it took the first time.` }),
  ];
  if (studios.length) {
    body.push(el("p", { class: "helm-body", text: `This is used by ${studios.join(" and ")}. ${studios.length === 1 ? "That studio" : "Those studios"} will have to download it again before launching.` }));
  }
  const chosen = await dialog({
    title: `Delete ${m.hf_repo}?`,
    body,
    actions: [{ label: "Cancel", value: null }, { label: "Delete", value: "go", class: "helm-btn-danger-fill", primary: true }],
  });
  if (chosen !== "go") return;
  return call(ctx, () => ctx.client.models.delete(m.id, { confirm: preview && preview.confirm }), `${m.hf_repo} could not be deleted.`);
}

/** Reclaim deletes exactly the previewed set, or nothing. */
async function reclaim(ctx) {
  let preview;
  try {
    preview = await ctx.client.models.reclaimPreview();
  } catch (err) {
    return toast(failure(err, "What reclaim would delete could not be read."), "error");
  }
  const items = preview.items || [];
  if (!items.length) return toast("Nothing is orphaned. Reclaim would delete nothing.");

  const chosen = await dialog({
    title: `Delete ${items.length} unused download${items.length === 1 ? "" : "s"}?`,
    body: [
      el("p", { class: "helm-body", text: `${bytes(preview.total_bytes || 0)} is freed. Only downloads no studio is bound to are touched; linked directories are never deleted.` }),
      el("ul", { class: "helm-stack", style: "margin: var(--helm-space-2) 0 0; padding-left: var(--helm-space-4)" },
        ...items.map((i) => el("li", { class: "helm-mono", text: `${i.hf_repo} · ${bytes(i.bytes || 0)}` }))),
    ],
    actions: [{ label: "Cancel", value: null }, { label: "Delete them", value: "go", class: "helm-btn-danger-fill", primary: true }],
  });
  if (chosen !== "go") return;
  return call(ctx, () => ctx.client.models.reclaim({ confirm: preview.confirm }), "Reclaim did not run.");
}

async function call(ctx, fn, what) {
  try {
    await fn();
  } catch (err) {
    // preview_changed means the picture moved between being shown and being
    // confirmed, so nothing was deleted. Saying that is the whole point.
    toast(err && err.code === "preview_changed"
      ? "Nothing was deleted: what is on disk changed while the confirmation was open. Try again."
      : failure(err, what), "error");
  }
  await loadDisk(ctx);
  return ctx.refresh();
}
