// Models and disk (03 §12).
//
// "Used by" is the reference count made visible. An artifact used by two
// studios is not deletable without a confirmation naming both; one used by
// none is chipped Orphaned and is the only thing Reclaim touches. Orphaned is
// a word, not a stored state — M3 removed the stored count, not the idea.
//
// No Verify: weight verification is still open, and a button that claims to
// have checked something it has not is worse than no button (M6 Q21).

import { ago, bytes, chip, dialog, el, failure, progress, toast } from "./ui.js";

function used(m) {
  const studios = m.studios || [];
  if (!studios.length) return { text: "Orphaned", tone: "warning" };
  return { text: studios.join(", "), tone: "idle" };
}

export function modelsAndDisk(ctx) {
  const models = ctx.store.models || [];
  const onDisk = models.reduce((n, m) => n + (m.bytes_on_disk || 0), 0);
  const orphans = models.filter((m) => m.source === "managed" && !(m.studios || []).length);
  const reclaimable = orphans.reduce((n, m) => n + (m.bytes_on_disk || 0), 0);

  const header = el("div", { class: "helm-page-header" },
    el("h1", { class: "helm-title", text: "Models & disk" }),
    el("span", { class: "helm-meta", text: `${bytes(onDisk)} cache` }),
    el("span", { class: "helm-spacer" }),
    orphans.length
      ? el("button", {
        class: "helm-btn helm-btn-danger",
        text: `Reclaim orphaned (${bytes(reclaimable)})`,
        onclick: () => reclaim(ctx),
      })
      : null);

  const rows = models.map((m) => {
    const u = used(m);
    const downloading = m.state === "downloading" || m.state === "interrupted";
    return el("tr", {},
      el("td", {},
        el("div", { class: "helm-stack", style: "gap: var(--helm-space-1)" },
          el("span", { class: "helm-body", text: m.hf_repo }),
          el("span", { class: "helm-micro", text: [m.revision, m.source === "linked" ? "linked" : null].filter(Boolean).join(" · ") }),
          downloading && m.total_bytes ? progress(m.bytes_on_disk, m.total_bytes) : null,
          downloading && m.total_bytes
            ? el("span", { class: "helm-mono", text: `${bytes(m.bytes_on_disk)} of ${bytes(m.total_bytes)}` })
            : null)),
      el("td", {}, el("span", { class: "helm-mono", text: m.source === "linked" ? "—" : bytes(m.bytes_on_disk) })),
      el("td", {}, chip(u.text, u.tone)),
      el("td", {}, el("span", { class: "helm-mono", text: ago(m.verified_at || m.created_at) })),
      el("td", {}, el("button", {
        class: "helm-btn helm-btn-danger helm-btn-sm",
        text: m.source === "linked" ? "Unlink" : "Delete",
        onclick: () => remove(ctx, m),
      })));
  });

  const table = el("table", { class: "helm-table" },
    el("thead", {}, el("tr", {}, ...["Artifact", "Size", "Used by", "Last used", ""].map((h) => el("th", { text: h })))),
    el("tbody", {}, ...rows));

  return el("div", { class: "helm-stack" }, header,
    el("div", { class: "helm-panel" },
      el("div", { class: "helm-panel-body" },
        models.length ? table : el("p", { class: "helm-micro", text: "No weights have been downloaded or linked yet." }))));
}

/**
 * remove unlinks a linked directory — never the user's own files — or reclaims
 * a download, which needs the confirm from its own preview so that what is
 * deleted is exactly what was shown.
 */
async function remove(ctx, m) {
  const studios = m.studios || [];
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
    body.push(el("p", { class: "helm-body", text: `${studios.length === 1 ? "This is used by" : "This is used by"} ${studios.join(" and ")}. ${studios.length === 1 ? "That studio" : "Those studios"} will have to download it again before launching.` }));
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
      el("p", { class: "helm-body", text: `${bytes(preview.bytes || 0)} is freed. Only downloads no studio is bound to are touched; linked directories are never deleted.` }),
      el("ul", { class: "helm-stack", style: "margin: var(--helm-space-2) 0 0; padding-left: var(--helm-space-4)" },
        ...items.map((i) => el("li", { class: "helm-mono", text: `${i.hf_repo} · ${bytes(i.bytes_on_disk || 0)}` }))),
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
  return ctx.refresh();
}
