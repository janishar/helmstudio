// The Studio block of a workflow (contracts/README.md "User workflows", plan 04-workflows.md section 7): which params
// Create shows and where. Absent = the derived form; Graph view's "Show in Studio" writes it out first.
import type { S } from "@/lib/api";
import type { Doc } from "@/lib/graph";
import { passthrough } from "@/lib/graph";
import { fields, sections, type Field } from "@/lib/workflow";

type Layout = S<"StudioLayout">;
type Schemas = Record<string, S<"NodeSchema">>;

const MAX_ITEMS = 32; // StudioLayout inspector section items

/** The block Create's derived form amounts to: text params in the composer; the inspector's References, Canvas,
 * Duration, Quality, Options and More sections as Create draws them (an over-long section goes on as "<title> 2"). */
export function deriveStudio(doc: Pick<Doc, "nodes">, schemas: Schemas): Layout {
  const fs = fields({ nodes: doc.nodes, node_schemas: schemas });
  const s = sections(fs);
  const keys = (list: Field[]) => list.map((f) => f.key);
  const groups: [string, string[]][] = [
    ...[...s.refs, ...s.rows].map((f): [string, string[]] => [
      (f.param.label ?? f.param.name).slice(0, 40),
      [f.key],
    ]),
    ["Canvas", keys(s.canvas)],
    ["Duration", keys(s.duration)],
    ["Quality", keys(s.tiles)],
    ["Options", keys(s.switches)],
    ["More", keys(s.more)],
  ];
  const inspector = groups.flatMap(([title, items]) =>
    Array.from({ length: Math.ceil(items.length / MAX_ITEMS) }, (_, i) => ({
      title: i ? `${title} ${i + 1}` : title,
      items: items.slice(i * MAX_ITEMS, (i + 1) * MAX_ITEMS),
    })),
  );
  return { composer: keys(fs.filter((f) => f.param.type === "text")), inspector };
}

/** The presets' names in the order Create shows them (`preset_order`, API 1.25), else as the object lists them. */
export const presetNames = (studio?: Layout | null): string[] =>
  studio?.preset_order ?? Object.keys(studio?.presets ?? {});

/** Where a path is placed: "Composer", "Quick", "Inspector › <section>", or null. */
export function placement(studio: Layout, path: string): string | null {
  if (studio.composer?.includes(path)) return "Composer";
  if (studio.quick?.includes(path)) return "Quick";
  const sec = studio.inspector?.find((s) => s.items.includes(path));
  return sec ? `Inspector › ${sec.title}` : null;
}

/** "Show in Studio": the path appended to the inspector section titled `title` (made last when missing); a doc
 * without a block first gets the derived one. */
export function showInStudio(doc: Doc, schemas: Schemas, path: string, title: string): Doc {
  const studio = doc.studio ?? deriveStudio(doc, schemas);
  if (placement(studio, path)) return { ...doc, studio };
  const inspector = studio.inspector ?? [];
  const i = inspector.findIndex((s) => s.title === title);
  return {
    ...doc,
    studio: {
      ...studio,
      inspector:
        i < 0
          ? [...inspector, { title: title.slice(0, 40), items: [path] }]
          : inspector.map((s, j) => (j === i ? { ...s, items: [...s.items, path] } : s)),
    },
  };
}

/** The block without the paths `drop` says (and their labels); the presets and the choices' options lose those keys
 * too (an emptied preset or option goes, and a choice left with one option). */
export function unplace(studio: Layout, drop: (path: string) => boolean): Layout {
  const out: Layout = { ...studio };
  const keep = (l?: string[]) => l?.filter((p) => !drop(p));
  if (studio.composer) out.composer = keep(studio.composer);
  if (studio.quick) out.quick = keep(studio.quick);
  if (studio.inspector)
    out.inspector = studio.inspector.map(({ choices, ...s }) => {
      const kept = choices
        ?.map((c) => ({
          ...c,
          options: c.options.flatMap((o) => {
            const set = Object.fromEntries(Object.entries(o.set).filter(([p]) => !drop(p)));
            return Object.keys(set).length ? [{ ...o, set }] : [];
          }),
        }))
        .filter((c) => c.options.length > 1);
      return { ...s, items: keep(s.items)!, ...(kept?.length && { choices: kept }) };
    });
  if (studio.labels)
    out.labels = Object.fromEntries(Object.entries(studio.labels).filter(([p]) => !drop(p)));
  if (studio.presets)
    out.presets = Object.fromEntries(
      Object.entries(studio.presets).flatMap(([name, values]) => {
        const v = Object.fromEntries(Object.entries(values).filter(([p]) => !drop(p)));
        return Object.keys(v).length ? [[name, v]] : [];
      }),
    );
  if (studio.preset_order) out.preset_order = studio.preset_order.filter((n) => n in out.presets!);
  return out;
}

/** "Hide" in Graph view: the path out of the block (a doc without one first gets the derived one). */
export const hideFromStudio = (doc: Doc, schemas: Schemas, path: string): Doc => ({
  ...doc,
  studio: unplace(doc.studio ?? deriveStudio(doc, schemas), (p) => p === path),
});

/** The switch for `<node>.bypass` as a form field (on = switched off). */
export function bypassField(w: Pick<S<"Workflow">, "nodes" | "node_schemas">, node: string): Field {
  const s = w.node_schemas[w.nodes[node].kind];
  return {
    key: `${node}.bypass`,
    node,
    nodeTitle: s.title,
    param: { name: "bypass", type: "bool", default: false, label: `Bypass ${s.title}` },
    initial: !!w.nodes[node].bypass,
    canvas: false,
    model: false,
  };
}

/** What Create shows for a workflow with a block: the placed fields in order (labels applied), the composer's
 * text fields, and the inspector's sections (quick paths and non-text composer paths lead as "Quick"). Null when
 * the workflow has no block (the derived form). */
export function studioView(w: S<"Workflow">, all: Field[]) {
  const st = w.studio;
  if (!st) return null;
  const byKey = new Map(all.map((f) => [f.key, f]));
  const resolve = (path: string): Field[] => {
    const node = path.split(".")[0];
    const s = w.nodes[node] && w.node_schemas[w.nodes[node].kind];
    let f = byKey.get(path);
    if (!f && path.endsWith(".bypass") && s && passthrough(s)) f = bypassField(w, node);
    if (!f) return [];
    const label = st.labels?.[path];
    return [label ? { ...f, param: { ...f.param, label } } : f];
  };
  const composer = (st.composer ?? []).flatMap(resolve);
  const text = composer.filter((f) => f.param.type === "text");
  const quick = [
    ...composer.filter((f) => f.param.type !== "text"),
    ...(st.quick ?? []).flatMap(resolve),
  ];
  type Choices = NonNullable<Layout["inspector"]>[number]["choices"];
  type Group = { title: string; help?: string; fields: Field[]; choices?: Choices };
  const groups = [
    { title: "Quick", fields: quick } as Group,
    ...(st.inspector ?? []).map((s): Group => ({
      title: s.title,
      help: s.help,
      fields: s.items.flatMap(resolve),
      choices: s.choices,
    })),
  ].filter((g) => g.fields.length || g.choices?.length);
  const shown = [...text, ...groups.flatMap((g) => g.fields)];
  // a choice's paths are set and sent like placed ones, though not drawn (API 1.26)
  const keys = new Set(shown.map((f) => f.key));
  const choicePaths = groups.flatMap((g) =>
    (g.choices ?? []).flatMap((c) => c.options.flatMap((o) => Object.keys(o.set))),
  );
  for (const path of choicePaths)
    if (!keys.has(path)) {
      keys.add(path);
      shown.push(...resolve(path));
    }
  return { text, groups, shown };
}
