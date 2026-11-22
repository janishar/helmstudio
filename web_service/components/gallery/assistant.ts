// The Gallery's screen actions and summary for the assistant (plan 18 §22.4, §22.5; contracts/tools/actions.json
// `gallery`): search, filters, sort and selection are lines; star and rename have Undo; delete is a card whose Yes
// is the same delete as the take menu's.
import { api, ok, type S } from "@/lib/api";
import { done, failed } from "@/lib/assistant";
import type { ScreenSpec } from "@/components/assistant/tools";
import { safeUrl } from "@/lib/format";
import type { Filter } from "@/lib/takes";

type Take = S<"Take">;
export type Order = NonNullable<Filter["order"]>;

export const ORDERS: Record<Order, string> = {
  newest: "Newest",
  oldest: "Oldest",
  starred: "Starred first",
  name: "Name",
};
const KIND: Record<string, string> = { image: "Images", video: "Video", audio: "Audio" };
const SOURCE: Record<string, string> = {
  create: "From Create",
  pipeline: "From Pipelines",
  export: "From Timeline",
  upload: "Uploads",
  import: "Imports",
  frame: "Frames",
};

/** The filter in words: "Images · Starred". */
export const filterWords = (f: Filter, batch?: string) =>
  [
    f.kind && KIND[f.kind],
    f.starred && "Starred",
    f.source && SOURCE[f.source],
    f.session_id && "This project only",
    f.batch_id && `Batch · ${batch || f.batch_id.slice(0, 8)}`,
  ].filter(Boolean) as string[];

export type GalleryCtx = {
  q: string;
  setQ: (q: string) => void;
  filter: Filter;
  setFilter: (f: Filter) => void;
  batchTitle?: string;
  takes: Take[] | null;
  current?: Take | null;
  setCurrent: (id: string | null) => void;
  selecting: boolean;
  picked: Set<string>;
  pick: (ids: string[] | null) => void;
  replace: (t: Take) => void;
  remove: (ids: string[]) => void;
  push: (url: string) => void;
};

const patch = (id: string, body: S<"TakePatch">) =>
  ok(api.PATCH("/v1/studio/takes/{take_id}", { params: { path: { take_id: id } }, body }));

export function galleryTools(c: GalleryCtx): ScreenSpec {
  const shown = c.takes ?? [];
  const name = (id: unknown) => shown.find((t) => t.id === id)?.title || "the take";
  const words = filterWords(c.filter, c.batchTitle);
  return {
    screen: "gallery",
    sees: [
      "Gallery",
      ...(c.q.trim() ? [`Search: ${c.q.trim()}`] : []),
      ...words,
      ...(c.filter.order && c.filter.order !== "newest" ? [`Sort: ${ORDERS[c.filter.order]}`] : []),
      `${shown.length} shown`,
    ],
    ids: { take_id: c.current?.id ?? "", batch_id: c.filter.batch_id ?? "" },
    examples: [
      { text: "Show my starred videos", sub: "filters the Gallery" },
      { text: "Find the takes of the rainy street", sub: "searches prompts" },
      { text: "Which of these four is sharpest?", sub: "looks at the takes" },
    ],
    summary: () => ({
      search: c.q,
      filters: { ...c.filter, order: undefined },
      sort: c.filter.order ?? "newest",
      selecting: c.selecting,
      selection: [...c.picked],
      current: c.current ? { id: c.current.id, title: c.current.title } : null,
      count_shown: shown.length,
      takes: shown.slice(0, 40).map((t) => ({
        id: t.id,
        title: t.title,
        kind: t.kind,
        source: t.source,
        starred: t.starred,
        created_at: t.created_at,
      })),
    }),
    actions: {
      search: ({ q }) => {
        const text = String(q ?? "").slice(0, 200);
        const prev = c.q;
        c.setQ(text);
        return done(text.trim() ? `Searched: ${text.trim()}` : "Cleared the search", {
          undo: { undo: () => c.setQ(prev), redo: () => c.setQ(text) },
        });
      },
      filter: ({ kind, source, starred, batch_id }) => {
        if (source !== undefined && !(String(source) in SOURCE))
          return failed(`Filter · source must be one of ${Object.keys(SOURCE).join(", ")}`);
        const f: Filter = {
          ...(kind ? { kind: kind as Filter["kind"] } : {}),
          ...(source ? { source: source as Filter["source"] } : {}),
          ...(starred ? { starred: true } : {}),
          ...(batch_id ? { batch_id: String(batch_id) } : {}),
          ...(c.filter.order && { order: c.filter.order }),
        };
        const prev = c.filter;
        c.setFilter(f);
        const w = filterWords(f);
        return done(w.length ? `Filtered: ${w.join(" · ")}` : "Showing every take", {
          undo: { undo: () => c.setFilter(prev), redo: () => c.setFilter(f) },
        });
      },
      sort: ({ order }) => {
        const o = order as Order;
        const prev = c.filter;
        const f = { ...c.filter, order: o === "newest" ? undefined : o };
        c.setFilter(f);
        return done(`Sorted: ${ORDERS[o]}`, {
          undo: { undo: () => c.setFilter(prev), redo: () => c.setFilter(f) },
        });
      },
      select: ({ take_ids }) => {
        const ids = [...new Set((take_ids as string[]) ?? [])];
        const missing = ids.filter((id) => !shown.some((t) => t.id === id));
        if (missing.length) return failed(`Select · ${missing.length} not shown here`);
        if (ids.length <= 1) {
          c.pick(null);
          c.setCurrent(ids[0] ?? null);
          return done(ids.length ? `Selected ${name(ids[0])}` : "Cleared the selection");
        }
        c.pick(ids);
        return done(`Selected ${ids.length} takes`);
      },
      star: async ({ take_id, on }) => {
        const id = String(take_id);
        const set = async (v: boolean) => c.replace(await patch(id, { starred: v }));
        await set(!!on);
        return done(`${on ? "Starred" : "Unstarred"} ${name(id)}`, {
          undo: { undo: () => set(!on), redo: () => set(!!on) },
        });
      },
      rename: async ({ take_id, title }) => {
        const id = String(take_id);
        const old = shown.find((t) => t.id === id)?.title;
        const t = await patch(id, { title: String(title).trim() });
        c.replace(t);
        return done(`Renamed: ${t.title}`, {
          undo: {
            undo: async () => old && c.replace(await patch(id, { title: old })),
            redo: async () => c.replace(await patch(id, { title: t.title })),
          },
        });
      },
      saveAsPipeline: async ({ take_id }) => {
        const p = await ok(
          api.POST("/v1/studio/takes/{take_id}/pipeline", {
            params: { path: { take_id: String(take_id) } },
            body: {},
          }),
        );
        c.push(`/pipelines/${p.id}`);
        return done(`Saved as pipeline · ${p.title}`, { result: { pipeline_id: p.id } });
      },
      delete: ({ take_ids }) => {
        const ids = [...new Set(take_ids as string[])];
        const n = ids.length;
        const list = ids.map((id) => shown.find((t) => t.id === id));
        const titles = list.map((t, i) => t?.title || `take ${i + 1}`);
        return {
          card: {
            kind: "delete",
            title: `Delete ${n === 1 ? titles[0] : `${n} takes`}?`,
            rows: [
              ["Goes", titles.join(", ").slice(0, 300)],
              ["Stays", "every other take in the Gallery"],
            ],
            thumbs: list.flatMap((t) => (t?.thumb_url ? [safeUrl(t.thumb_url) ?? ""] : [])),
            tags: [],
            go: n === 1 ? "DELETE" : `DELETE ${n}`,
            danger: true,
            fine: "This can't be undone. Same as Delete on the take menu.",
          },
          yes: async () => {
            const r = await Promise.allSettled(
              ids.map((id) =>
                ok(api.DELETE("/v1/studio/takes/{take_id}", { params: { path: { take_id: id } } })),
              ),
            );
            const gone = ids.filter((_, i) => r[i].status === "fulfilled");
            c.remove(gone);
            return gone.length === n
              ? done(`Deleted ${n === 1 ? titles[0] : `${n} takes`}`)
              : failed(`Deleted ${gone.length} of ${n} · the rest failed`);
          },
        };
      },
    },
  };
}
