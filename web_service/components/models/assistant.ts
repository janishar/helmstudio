// The Models screen's actions for the assistant (plan 18 §22.5, §22.6, decision 3; actions.json `models`, admins
// only; board frame 6 "Download"): open a row, Download as a card whose Yes is the row's own Download (its dialog:
// the licence tick and Download stay the admin's clicks; the action resolves with the job once it closes), cancel a
// job, and delete as a card. A user who is not an admin gets the screen's summary and no action: it says to ask one.
import { api, ok, type S } from "@/lib/api";
import { done, failed } from "@/lib/assistant";
import type { ScreenSpec } from "@/components/assistant/tools";
import { bytesText, canDelete, downloadPlan, folderOf } from "@/lib/models";

type List = S<"ModelList">;
type Model = S<"Model">;
type Variant = S<"ModelVariant">;
type Job = S<"ModelJob">;

/** The row the variant panel shows, as the assistant reads it; `download` opens its Download dialog and resolves
 * with the job its Download queued, or null (closed without it). */
export type Row = { m: Model; v: Variant; target: string; download: () => Promise<Job | null> };

export type ModelsCtx = {
  admin: boolean;
  tab: "models" | "loras";
  list: List | null;
  jobs: Job[];
  host: string;
  /** the row shown now (its target host when one is set), for the Sees chips and ids */
  shown: { m: Model; v: Variant; host: string } | null;
  row: () => Row | null;
  /** opens the variant on `host` (the row's target); resolves once the panel shows it */
  openRow: (model: string, variant: string, host: string) => Promise<boolean>;
  reload: () => void;
};

const find = (list: List, model: unknown, variant: unknown) => {
  const same = (a: string, b: unknown) => a.toLowerCase() === String(b).toLowerCase();
  const m = list.models.find((x) => same(x.id, model) || same(x.title, model));
  const v = m?.variants.find((x) => same(x.id, variant) || same(x.title, variant));
  return { m, v };
};

export function modelsTools(c: ModelsCtx): ScreenSpec {
  const r = c.shown;
  const running = c.jobs.filter((j) => j.state === "queued" || j.state === "running");
  const admin = c.admin;
  const deleteOf = async (model: unknown, variant: unknown, host: unknown) => {
    const list = c.list;
    if (!list) return failed("Delete · the models are not read yet");
    const { m, v } = find(list, model, variant);
    if (!m || !v) return failed("Delete · no such model or variant");
    const h = String(host);
    if (!canDelete(v, h)) return failed(`Delete · ${m.title} · ${v.title} has no files on ${h}`);
    return {
      card: {
        kind: "delete",
        title: `Delete ${m.title} · ${v.title} from ${h}?`,
        rows: [
          ["Goes", `its files under ${folderOf(list, v, h) || "its folder"} on ${h}`],
          ["Stays", "shared components another variant on the host loads"],
        ] as [string, string][],
        tags: [],
        go: "DELETE",
        danger: true,
        fine: "This can't be undone. Same as Delete from host. A download can fetch it again.",
      },
      yes: async () => {
        const j = await ok(
          api.DELETE("/v1/studio/admin/models/{model}", {
            params: { path: { model: m.id }, query: { host: h, variant: v.id } },
          }),
        );
        c.reload();
        return done(`Delete queued on ${j.host}`, { result: { job_id: j.id } });
      },
    };
  };
  return {
    screen: "models",
    sees: ["Models", ...(r ? [`${r.m.title} · ${r.v.title}`, ...(r.host ? [r.host] : [])] : [])],
    ids: r ? { model: r.m.id, variant: r.v.id, host: r.host } : {},
    examples: [
      { text: "Which variant fits my Mac?", sub: "reads the models and hosts" },
      { text: "What does this licence allow?", sub: "explains it" },
      {
        text: "Get LTX 2.3 fp8 on NVIDIA",
        sub: admin ? "opens the row, asks first" : "asks an admin",
      },
    ],
    summary: () => ({
      tab: c.tab,
      admin,
      host_filter: c.host || "all",
      selected: r ? { model: r.m.id, variant: r.v.id, target: c.row()?.target ?? r.host } : null,
      models: (c.list?.models ?? []).slice(0, 40).map((m) => ({
        id: m.id,
        title: m.title,
        variants: m.variants.map((v) => ({
          id: v.id,
          title: v.title,
          licence: v.licence?.id ?? null,
          size: bytesText(v.download_bytes) || null,
          hosts: v.hosts.map((h) => ({ host: h.host, state: h.state })),
        })),
      })),
      ...(admin && {
        jobs: running.map((j) => ({
          id: j.id,
          kind: j.kind,
          title: j.title,
          host: j.host,
          state: j.state,
        })),
      }),
    }),
    actions: !admin
      ? {}
      : {
          openRow: async ({ model, variant, host }) => {
            const list = c.list;
            if (!list) return failed("Open · the models are not read yet");
            const { m, v } = find(list, model, variant);
            if (!m || !v) return failed("Open · no such model or variant");
            const h = String(host);
            if (!list.hosts.some((x) => x.host === h)) return failed(`Open · no host ${h}`);
            if (!(await c.openRow(m.id, v.id, h))) return failed("Open · the row did not open");
            return done(`Opened Models · ${m.title} · ${v.title} · ${h}`);
          },
          download: () => {
            const row = c.row();
            const list = c.list;
            if (!row || !list) return failed("Download · open a row first");
            const plan = downloadPlan(list, row.v, row.target);
            if (plan.blocked) return failed(`Download · ${plan.blocked}`);
            const free = list.hosts.find((h) => h.host === row.target)?.free_bytes;
            const name = `${row.m.title} · ${row.v.title}`;
            return {
              card: {
                kind: "download",
                title: `Download ${name}?`,
                rows: [
                  ["Size", bytesText(row.v.download_bytes) || "unknown"],
                  [
                    "Machine",
                    [row.target, free != null && `${bytesText(free)} free`]
                      .filter(Boolean)
                      .join(" · "),
                  ],
                  [
                    "Licence",
                    plan.licence
                      ? plan.mustAccept
                        ? `${plan.licence.title} · not accepted yet`
                        : plan.licence.title
                      : "none",
                  ],
                ],
                tags: ["uses disk and network"],
                note: plan.mustAccept
                  ? "Yes opens the row's Download: tick I accept there first. Download turns on after."
                  : "Yes opens the row's Download: Download there is your click.",
                go: "DOWNLOAD",
                fine: "Same as Download on the row. After it, I wait for it and go back to Create.",
              },
              yes: async () => {
                const j = await row.download();
                return j
                  ? done(`Download queued on ${j.host} · ${name}`, { result: { job_id: j.id } })
                  : { status: "cancelled", text: "Download · cancelled" };
              },
            };
          },
          cancelJob: async ({ job_id }) => {
            await ok(
              api.POST("/v1/studio/admin/model-jobs/{job_id}/cancel", {
                params: { path: { job_id: String(job_id) } },
              }),
            );
            c.reload();
            return done("Cancelled the job");
          },
          delete: ({ model, variant, host }) => deleteOf(model, variant, host),
        },
  };
}
