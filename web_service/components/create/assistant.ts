// Create's screen actions and summary for the assistant (plan 18 §22.4, §22.5; contracts/tools/actions.json `create`).
// Each action calls the screen's own entry point and refuses what the form shows greyed or hidden, with the form's
// own reason (addonBlocks, the Run on rules, `when`, a version's option rules). Generate and the deletes are cards:
// Yes is the user's click and calls the same function as the screen's button.
import { api, ok, type S } from "@/lib/api";
import {
  about,
  done,
  failed,
  ordinal,
  placeInLine,
  type Asked,
  type Outcome,
} from "@/lib/assistant";
import type { ScreenSpec } from "@/components/assistant/tools";
import { hasSound, takeById, type LibraryAsk } from "@/components/assistant/library";
import type { VoiceCtx } from "@/components/create/voices";
import { canvasOf } from "@/components/create/form";
import { frameTake } from "@/components/create/frame-in";
import { reloadLibrary } from "@/lib/library";
import { deleteLora, renameLora } from "@/lib/lora-library";
import { LOOK_GROUPS, lookGroupLabel, reasonOf, type Chosen, type Tile } from "@/lib/looks";
import { downloaded } from "@/lib/models";
import { machineReason, type AddonNeed, type Machine } from "@/lib/run-on";
import { gridOf, secondsText } from "@/lib/media";
import { clipOf } from "@/lib/video";
import {
  aspectOf,
  display,
  optionBlocked,
  readValue,
  toRaw,
  type Field,
  type Raw,
} from "@/lib/workflow";
import { mentioned } from "@/lib/mentions";

type Workflow = S<"Workflow">;
type Take = S<"Take">;
type Run = S<"Run">;
type Item = S<"LibraryItem">;
type Value = S<"ParamValue">;

/** What Create already works out, handed to its tools each render. */
export type CreateCtx = {
  workflow?: Workflow;
  workflows: Workflow[] | null;
  all: Field[];
  fields: Field[];
  raws: Record<string, Raw>;
  held: Record<string, Raw>;
  inputs: Record<string, unknown>;
  change: (key: string, raw: Raw) => void;
  restore: (key: string, raw: Raw | undefined) => void;
  switchTo: (id: string) => void;
  media: string;
  family?: string;
  count: number;
  setCount: (n: number) => void;
  seedField?: Field;
  machines: Machine[];
  runOn?: Machine;
  setRunOn: (host: string) => void;
  picks: { m: S<"Model">; v: S<"ModelVariant"> }[];
  needs: AddonNeed[];
  blocks: Record<string, string>;
  modelList: S<"ModelList"> | null;
  hosts: S<"Host">[] | null;
  view: "studio" | "graph";
  setView: (v: "studio" | "graph") => void;
  newSession: () => Promise<string>;
  session?: S<"StudioSession">;
  takes: Take[];
  shownTake?: Take;
  selected: string | null;
  select: (id: string | null) => void;
  runs: Run[] | null;
  star: (t: Take, on: boolean) => Promise<void>;
  stop: (id: string) => Promise<void>;
  /** the latest form's Generate (read at the click, not when the card was made) */
  generate: () => Promise<{ ids: string[]; error: string }>;
  /** why Generate can't run now, at the click */
  cannot: () => string | null;
  plan: { gb: number | null; host: string | null };
  took: number | null;
  budgetGb: number | null;
  looks: Item[] | null;
  chosenLooks: Chosen;
  setLooks: (f: (c: Chosen) => Chosen) => void;
  tiles: Tile[];
  loras: S<"Lora">[] | null;
  loraFamilies: Record<string, string>;
  library: Item[] | null;
  onWorkflow: (w: Workflow, open: boolean) => void;
  onDeleted: (id: string) => void;
  applyOffer: (o: S<"TakeOffer">) => void;
  handles: string[];
  prompt: string;
  /** opens a library dialog prefilled; resolves on its close (components/assistant/library) */
  dialog: (a: LibraryAsk, progress: (text: string) => void) => Promise<Outcome>;
  /** the voice picker's workflows (Design, Clone) for the open workflow */
  voiceCtx?: VoiceCtx;
};

// API 1.79 (plan 38): the Create kinds as the tabs name them; today's Audio is Voice, music and sound are classes
const MEDIA: Record<string, string> = {
  image: "Image",
  video: "Video",
  audio: "Voice",
  voice: "Voice",
  music: "Music",
  sound: "Sound",
};
const label = (f: Field) => f.param.label ?? f.param.name;
const takeName = (t?: Take) => t?.title || "the take";

/** "Qwen Image · Turbo": the family and the chosen version. */
const modelText = (c: CreateCtx) =>
  [c.family ?? c.workflow?.title, c.picks[0]?.v.title].filter(Boolean).join(" · ");

/** Why the preset is greyed (it switches on a node whose add-on is missing), as the Preset chips say. */
const presetBlock = (values: Record<string, Value>, blocks: Record<string, string>) =>
  Object.entries(values).flatMap(([k, v]) => {
    const [node, param] = k.split(".");
    return param === "bypass" && v === false && blocks[node] ? [blocks[node]] : [];
  })[0];

function setLook(c: CreateCtx, g: string, value: unknown): Outcome {
  const name = lookGroupLabel(g);
  if (!LOOK_GROUPS.some(([x]) => x === g) || !c.tiles.includes(`look:${g}` as Tile))
    return failed(`${name}: not on this form`);
  const want =
    value == null || value === "" || value === "auto" || value === "Auto"
      ? []
      : [value].flat().map(String);
  const items: Item[] = [];
  for (const v of want) {
    const it = (c.looks ?? []).find(
      (x) =>
        x.kind === "look" &&
        x.group === g &&
        (x.id === v || x.name.toLowerCase() === v.toLowerCase()),
    );
    if (!it) return failed(`${name}: no look ${v}`);
    const why = c.workflow && reasonOf(c.workflow, it, c.loraFamilies);
    if (why) return failed(`${name}: ${it.name} · ${why}`);
    items.push(it);
  }
  const old = c.chosenLooks[g as keyof Chosen] ?? [];
  const ids = items.map((x) => x.id);
  const set = (list: string[]) => c.setLooks((x) => ({ ...x, [g]: list }));
  set(ids);
  return done(`Set ${name}: ${items.map((x) => x.name).join(", ") || "Auto"}`, {
    undo: { undo: () => set(old), redo: () => set(ids) },
  });
}

function change(c: CreateCtx, key: string, value: unknown): Outcome {
  if (key.startsWith("look:")) return setLook(c, key.slice(5), value);
  const f = c.all.find((x) => x.key === key);
  if (!f) return failed(`${key}: no such setting`);
  if (!c.fields.includes(f)) return failed(`${label(f)}: not shown now`);
  if (c.blocks[f.node]) return failed(`${label(f)}: ${c.blocks[f.node]}`);
  const raw = toRaw(value as Value, f.param);
  const opts = f.param.enum;
  if (opts && !opts.includes(String(raw)))
    return failed(`${label(f)}: ${String(raw)} is not one of ${opts.join(", ")}`);
  const blocked = opts && optionBlocked(f, String(raw), c.fields, c.raws);
  if (blocked) return failed(`${label(f)}: ${blocked}`);
  const read = readValue(f.param, raw);
  // a clip's length reads in seconds (owner, 2026-11-25); the model is also told the frames
  const fps = f.param.type === "frames" ? clipOf(c.fields, c.raws)?.fps : undefined;
  const max = gridOf(f.param).max;
  if ("error" in read && fps && Number(raw) > max) {
    const why = `${label(f)}: the longest is ${secondsText(max / fps)}`;
    return failed(why, `${why} (${max} frames at ${fps} fps)`);
  }
  // a voice says what it takes and what it got (item 23), never only "a voice is required"
  if ("error" in read && f.param.type === "voice") {
    const why = `${label(f)}: expected one of your voices`;
    const got = typeof value === "string" && value.length <= 40 ? `${why}; got "${value}"` : why;
    const json = `${why}; got ${JSON.stringify(value ?? null)}`; // the JSON is for the model only
    return failed(got, `${json} (a voice is {"voice_id": "<id>"}: a voice's id in your library)`);
  }
  if ("error" in read) return failed(`${label(f)}: ${read.error}`);
  const old = c.held[key];
  const before = String(c.raws[key] ?? "");
  c.change(key, raw);
  const d = display(f, raw);
  const added =
    f.param.type === "text"
      ? mentioned(String(raw)).filter((h) => !mentioned(before).includes(h))
      : [];
  const text =
    f.param.type === "text"
      ? `Wrote ${label(f).toLowerCase()}${added.length ? ` · added ${added.map((h) => `@${h}`).join(" ")}` : ""}`
      : typeof raw === "object"
        ? `Set ${label(f)}`
        : fps && typeof read.value === "number"
          ? `Set ${d.label}: ${secondsText(read.value / fps)}`
          : `Set ${d.label}: ${d.value}`;
  return done(text, { undo: { undo: () => c.restore(key, old), redo: () => c.change(key, raw) } });
}

/** The Generate card (§22.6): model and version, size, seeds, machine and place in line, time and memory. */
async function generateCard(c: CreateCtx): Promise<Outcome | Asked> {
  const w = c.workflow;
  if (!w) return failed("Generate · no workflow");
  const why = c.cannot();
  if (why) return failed(`Generate · ${why}`);
  const n = c.seedField ? c.count : 1;
  const host = c.runOn?.host ?? c.plan.host;
  const q = await ok(api.GET("/v1/studio/queue")).catch(() => null);
  const place = placeInLine(q, host);
  const mc = c.machines.find((m) => m.host === host);
  const est = [
    mc?.label ?? host ?? "no machine yet",
    place && `${ordinal(place)} in line`,
    c.took != null && about(c.took),
    c.plan.gb != null &&
      (c.budgetGb
        ? `${Math.round(c.plan.gb)} of ${Math.round(c.budgetGb)} GB`
        : `${Math.round(c.plan.gb)} GB`),
  ]
    .filter(Boolean)
    .join(" · ");
  const cv = canvasOf(c.fields, c.raws);
  const clip = clipOf(c.fields, c.raws);
  const secs =
    clip && clip.frames !== "auto" ? Math.round((clip.frames / clip.fps) * 10) / 10 : null;
  const seed = c.seedField ? String(c.raws[c.seedField.key] ?? "") : "";
  const what =
    c.media === "video"
      ? n === 1
        ? secs
          ? `a ${secs} s clip`
          : "a clip"
        : `${n} clips`
      : c.media === "audio"
        ? n === 1
          ? "the audio"
          : `${n} audio takes`
        : `${n} image${n === 1 ? "" : "s"}`;
  const rows: [string, string][] = [["Model", modelText(c)]];
  if (cv)
    rows.push([
      "Size",
      `${c.media === "video" ? "" : `${aspectOf(cv.w, cv.h) ?? "custom"} · `}${cv.w} × ${cv.h}${clip && c.media === "video" ? ` · ${clip.fps} fps` : ""}`,
    ]);
  if (c.seedField) rows.push(["Seeds", `${n} · ${seed ? `from ${seed}` : "random"}`]);
  const prompt = c.prompt.trim();
  return {
    card: {
      kind: "generate",
      title: `Generate ${what}?`,
      rows,
      tags: ["uses the GPU"],
      est,
      quote: prompt ? (prompt.length > 90 ? `${prompt.slice(0, 90)}…` : prompt) : undefined,
      go: "GENERATE",
      fine: "Same as the Generate button on the screen. Change anything there first if you like.",
    },
    yes: async (auto) => {
      const now = c.cannot();
      if (now) return failed(`Generate · ${now}`);
      const r = await c.generate();
      // Auto (board frame 13): "Auto · Generated 1 take · Mac"
      const made = auto
        ? `Generated ${n} take${n === 1 ? "" : "s"} · ${mc?.label ?? host ?? "Auto"}`
        : `You clicked GENERATE · ${what.replace(/^a /, "1 ")}`;
      return r.ids.length
        ? done(made, { result: { run_ids: r.ids, session_id: c.session?.id ?? null } })
        : failed(`Generate failed · ${r.error || "see the screen"}`);
    },
  };
}

const deleteCard = (
  title: string,
  goes: string,
  stays: string,
  fine: string,
  yes: () => Promise<Outcome>,
): Asked => ({
  card: {
    kind: "delete",
    title,
    rows: [
      ["Goes", goes],
      ["Stays", stays],
    ],
    tags: [],
    go: "DELETE",
    danger: true,
    fine,
  },
  yes,
});

/** Create's registration: its actions, Sees chips, ids and summary. */
export function createTools(c: CreateCtx): ScreenSpec {
  const w = c.workflow;
  const own = w?.source === "mine";
  const takeOf = (id: unknown) => c.takes.find((t) => t.id === id);
  const runOnLabel = c.runOn
    ? c.runOn.label
    : `Auto${c.plan.host ? ` → ${c.machines.find((m) => m.host === c.plan.host)?.label ?? c.plan.host}` : ""}`;
  return {
    screen: "create",
    sees: [
      `Create · ${c.view === "graph" ? "Graph" : (MEDIA[c.media] ?? c.media)}`,
      ...(w ? [modelText(c)] : []),
      `Run on: ${runOnLabel}`,
      c.session?.title ?? "New session",
      ...c.handles.map((h) => `@${h}`),
    ],
    ids: {
      session_id: c.session?.id ?? "",
      workflow_id: w?.id ?? "",
      take_id: c.shownTake?.id ?? "",
    },
    examples: [
      { text: "Make a portrait at golden hour", sub: "fills the prompt, model and look" },
      { text: "Turn my last take into a 5 s clip", sub: "switches to Video" },
      { text: "Why did my last run fail?", sub: "reads the run and explains" },
    ],
    summary: () => ({
      view: c.view,
      session: c.session ? { id: c.session.id, title: c.session.title } : null,
      workflow: w && {
        id: w.id,
        title: w.title,
        family: w.family,
        media: c.media,
        source: w.source,
        version: c.picks[0]?.v.title ?? null,
      },
      // the workflows switchTo takes, by title: without them the model guessed a name ("Text to image") and was told
      // it was not installed while Qwen Image sat downloaded
      workflows: (c.workflows ?? []).map((x) => ({
        id: x.id,
        title: x.title,
        media: x.media ?? "image",
        ...(x.sound && { sound: x.sound }), // API 1.79: music and sound prompts are not spoken (pipelines)
        downloaded: downloaded(c.modelList, x.family),
      })),
      fields: c.fields.map((f) => ({
        key: f.key,
        label: label(f),
        type: f.param.type,
        value: c.inputs[f.key] ?? null,
        ...(f.param.enum && { options: f.param.enum }),
        ...(c.blocks[f.node] && { greyed: c.blocks[f.node] }),
      })),
      looks: c.tiles
        .filter((t) => t.startsWith("look:"))
        .map((t) => {
          const g = t.slice(5) as keyof Chosen;
          const fit = (c.looks ?? []).filter(
            (x) => x.kind === "look" && x.group === g && !(w && reasonOf(w, x, c.loraFamilies)),
          );
          return {
            key: t,
            chosen: (c.chosenLooks[g] ?? []).map((id) => fit.find((x) => x.id === id)?.name ?? id),
            offers: fit.map((x) => x.name),
          };
        }),
      elements: c.handles.map((h) => `@${h}`),
      variants: (c.modelList?.models ?? [])
        .filter((m) => m.family === w?.family)
        .flatMap((m) =>
          m.variants.map((v) => ({
            id: v.id,
            title: v.title,
            on: v.hosts.filter((h) => h.state === "present").map((h) => h.host),
          })),
        ),
      presets: Object.entries(w?.studio?.presets ?? {}).map(([name, values]) => ({
        name,
        ...(presetBlock(values, c.blocks) && { greyed: presetBlock(values, c.blocks) }),
      })),
      run_on: {
        chosen: c.runOn?.host ?? "auto",
        machines: c.machines.map((m) => {
          const why = machineReason(m, w?.env, c.picks, c.needs);
          return { host: m.host, label: m.label, enabled: !why, ...(why && { reason: why }) };
        }),
      },
      hosts: (c.hosts ?? []).map((h) => ({
        host: h.host,
        env: h.env,
        state: h.state,
        ...((h.host_memory ?? h.memory) && {
          memory_gb: `${(h.host_memory ?? h.memory)!.used_gb} of ${(h.host_memory ?? h.memory)!.budget_gb}`,
        }),
      })),
      addons: c.needs.map((n) => ({
        title: n.title,
        node: n.node,
        ...(c.blocks[n.node] && { greyed: c.blocks[n.node] }),
      })),
      plan: {
        host: c.plan.host,
        peak_gb: c.plan.gb,
        seconds: c.took,
        runs: c.seedField ? c.count : 1,
      },
      selected: c.shownTake ? { id: c.shownTake.id, title: c.shownTake.title } : null,
      takes: c.takes
        .slice(0, 12)
        .map((t) => ({ id: t.id, title: t.title, kind: t.kind, starred: t.starred })),
      runs: (c.runs ?? [])
        .filter((r) => r.status === "queued" || r.status === "running")
        .map((r) => ({ id: r.id, status: r.status, host: r.host, position: r.position ?? null })),
    }),
    actions: {
      change: ({ key, value }) => change(c, String(key), value),
      switchTo: ({ workflow }) => {
        const v = String(workflow).toLowerCase();
        const to = c.workflows?.find((x) => x.id === workflow || x.title.toLowerCase() === v);
        if (!to) return failed(`${String(workflow)} is not installed. Get it in Models.`);
        if (!downloaded(c.modelList, to.family))
          return failed(`${to.title}: not downloaded · Models`);
        const from = w?.id;
        c.switchTo(to.id);
        return done(`Switched to ${MEDIA[to.sound ?? to.media ?? "image"] ?? ""} · ${to.title}`, {
          undo: { undo: () => from && c.switchTo(from), redo: () => c.switchTo(to.id) },
        });
      },
      preset: ({ name }) => {
        const all = w?.studio?.presets ?? {};
        const key = Object.keys(all).find((k) => k.toLowerCase() === String(name).toLowerCase());
        if (!key) return failed(`Preset · no preset ${String(name)}`);
        const why = presetBlock(all[key], c.blocks);
        if (why) return failed(`Preset ${key}: ${why}`);
        const set = Object.entries(all[key]).flatMap(([k, v]) => {
          const f = c.all.find((x) => x.key === k);
          return f ? [[k, toRaw(v, f.param), c.held[k]] as const] : [];
        });
        set.forEach(([k, raw]) => c.change(k, raw));
        return done(`Applied preset: ${key}`, {
          undo: {
            undo: () => set.forEach(([k, , old]) => c.restore(k, old)),
            redo: () => set.forEach(([k, raw]) => c.change(k, raw)),
          },
        });
      },
      runOn: ({ host, reason }) => {
        const prev = c.runOn?.host ?? "";
        const why = String(reason);
        if (host === "auto") {
          c.setRunOn("");
          return done(`Set Run on: Auto · ${why}`, {
            undo: { undo: () => c.setRunOn(prev), redo: () => c.setRunOn("") },
          });
        }
        const m = c.machines.find((x) => x.host === host || x.label.toLowerCase() === host);
        if (!m) return failed(`Run on: no machine ${String(host)}`);
        const no = machineReason(m, w?.env, c.picks, c.needs);
        if (no) return failed(`Run on: ${m.label} · ${no}`);
        c.setRunOn(m.host);
        return done(`Set Run on: ${m.label} · ${why}`, {
          undo: { undo: () => c.setRunOn(prev), redo: () => c.setRunOn(m.host) },
        });
      },
      seeds: ({ n }) => {
        const k = Number(n);
        if (!c.seedField) return failed("Seeds: this workflow has no seed");
        if (!Number.isInteger(k) || k < 1 || k > 4)
          return failed(`Seeds: ${String(n)} · the most is 4`);
        const prev = c.count;
        c.setCount(k);
        return done(`Set seeds: ${k}`, {
          undo: { undo: () => c.setCount(prev), redo: () => c.setCount(k) },
        });
      },
      view: ({ view }) => {
        c.setView(view === "graph" ? "graph" : "studio");
        return done(`Opened ${view === "graph" ? "Graph" : "Studio"} view`);
      },
      newSession: async () => {
        const id = await c.newSession();
        return done("Started a new session", { result: { session_id: id } });
      },
      renameWorkflow: async ({ title }) => {
        if (!w || !own)
          return failed("Rename · only your own workflows: duplicate a template first");
        const r = await ok(
          api.PATCH("/v1/studio/workflows/{workflow_id}", {
            params: { path: { workflow_id: w.id } },
            body: { title: String(title).trim().slice(0, 80) },
          }),
        );
        c.onWorkflow(r, false);
        return done(`Renamed workflow: ${r.title}`);
      },
      duplicateWorkflow: async () => {
        if (!w) return failed("Duplicate · no workflow");
        const r = await ok(api.POST("/v1/studio/workflows", { body: { based_on: w.id } }));
        c.onWorkflow(r, true);
        return done(`Duplicated as ${r.title}`, { result: { workflow_id: r.id } });
      },
      deleteWorkflow: () => {
        if (!w || !own) return failed("Delete · only your own workflows");
        return deleteCard(
          `Delete ${w.title}?`,
          `workflow ${w.title}`,
          "its takes in the Gallery",
          "This can't be undone. Same as Delete on the workflow menu.",
          async () => {
            await ok(
              api.DELETE("/v1/studio/workflows/{workflow_id}", {
                params: { path: { workflow_id: w.id } },
              }),
            );
            c.onDeleted(w.id);
            return done(`Deleted workflow ${w.title}`);
          },
        );
      },
      frameIn: async ({ take_id, at }) => {
        const t = takeOf(take_id);
        if (t && t.kind !== "video")
          return failed(`Frame in · ${takeName(t)} is not a video: put it in with useIn`);
        const end = at === "last" ? "last" : "first";
        const frame = await frameTake(String(take_id), end);
        const uses = await ok(
          api.GET("/v1/studio/takes/{take_id}/uses", { params: { path: { take_id: frame } } }),
        );
        const o =
          uses.uses.find((x) => x.workflow_id === w?.id) ??
          uses.uses.find((x) => x.family === w?.family) ??
          uses.uses[0];
        if (!o) return failed(`Frame in · nothing takes the ${end} frame`);
        c.applyOffer(o);
        return done(`Set ${end} frame: ${takeName(t)}`, { result: { take_id: frame } });
      },
      // the strip's and the First frame tile's "Use in…": the take's offers (GET …/uses), applied in place
      useIn: async ({ take_id, workflow }) => {
        const t = c.takes.find((x) => x.id === take_id);
        const want = String(workflow ?? w?.id ?? "").toLowerCase();
        const { uses } = await ok(
          api.GET("/v1/studio/takes/{take_id}/uses", {
            params: { path: { take_id: String(take_id) } },
          }),
        );
        const fits = (o: S<"TakeOffer">) =>
          o.workflow_id === workflow || o.title.toLowerCase() === want;
        const o = uses.find(fits) ?? uses.find((x) => x.family === (workflow ? want : w?.family));
        if (!o)
          return failed(
            `Use in · ${takeName(t)} goes in ${uses.map((x) => x.title).join(", ") || "no workflow"}`,
          );
        const from = w?.id;
        const old = Object.keys(o.inputs).map((k) => [k, c.held[k]] as const);
        c.applyOffer(o);
        return done(`Put ${takeName(t)} in ${o.action ? `${o.action} · ` : ""}${o.title}`, {
          undo: {
            undo: () => {
              old.forEach(([k, v]) => c.restore(k, v));
              if (from) c.switchTo(from);
            },
            redo: () => c.applyOffer(o),
          },
        });
      },
      select: ({ take_id }) => {
        const t = takeOf(take_id);
        if (!t) return failed("Select · not a take of this session");
        const prev = c.selected;
        c.select(t.id);
        return done(`Selected ${takeName(t)}`, {
          undo: { undo: () => c.select(prev), redo: () => c.select(t.id) },
        });
      },
      star: async ({ take_id, on }) => {
        const t = takeOf(take_id);
        if (!t) return failed("Star · not a take of this session");
        await c.star(t, !!on);
        return done(`${on ? "Starred" : "Unstarred"} ${takeName(t)}`, {
          undo: { undo: () => c.star(t, !on), redo: () => c.star(t, !!on) },
        });
      },
      stop: async ({ run_id }) => {
        const r = c.runs?.find((x) => x.id === run_id);
        if (!r || (r.status !== "queued" && r.status !== "running"))
          return failed("Stop · no run of this session in progress");
        await c.stop(r.id);
        return done("Stopped the run");
      },
      generate: () => generateCard(c),
      renameLora: async ({ lora_id, name }) => {
        const l = c.loras?.find((x) => x.id === lora_id);
        if (!l) return failed("Rename · no such LoRA");
        if (l.source !== "mine")
          return failed(`Rename · ${l.name} is shared: only an admin renames it`);
        await renameLora(l.id, String(name).trim());
        return done(`Renamed LoRA: ${String(name).trim()}`);
      },
      deleteLora: ({ lora_id }) => {
        const l = c.loras?.find((x) => x.id === lora_id);
        if (!l) return failed("Delete · no such LoRA");
        if (l.source !== "mine")
          return failed(`Delete · ${l.name} is shared: only an admin deletes it`);
        return deleteCard(
          `Delete ${l.name}?`,
          `LoRA ${l.name}`,
          "the takes made with it",
          "This can't be undone. Same as Delete on the LoRA.",
          async () => {
            await deleteLora(l);
            return done(`Deleted LoRA ${l.name}`);
          },
        );
      },
      saveAsLook: async ({ take_id, name }, { progress, auto }) => {
        const t = take_id ? await takeById(take_id, c.takes) : c.shownTake;
        if (!t) return failed("Save as look · no such take");
        return c.dialog(
          { kind: "look", take: t, name: name as string | undefined, auto },
          progress,
        );
      },
      saveAsElement: async ({ take_id, handle }, { progress, auto }) => {
        const t = take_id ? await takeById(take_id, c.takes) : c.shownTake;
        if (take_id && !t) return failed("Save as element · no such take");
        if (t && t.kind !== "image")
          return failed("Save as element · an element's pictures are images");
        const h = handle === undefined ? undefined : String(handle).replace(/^@/, "");
        return c.dialog({ kind: "element", takes: t ? [t] : [], handle: h, auto }, progress);
      },
      editItem: ({ item_id }, { progress }) => {
        const it = [...(c.library ?? []), ...(c.looks ?? [])].find((x) => x.id === item_id);
        if (!it) return failed("Edit · no such library item");
        return c.dialog({ kind: "edit", item: it }, progress);
      },
      cloneVoice: async ({ take_id, from_s, to_s, name }, { progress }) => {
        const t = await takeById(take_id, c.takes);
        if (!t) return failed("Clone · no such take");
        if (!hasSound(t)) return failed("Clone · that take has no sound");
        if (!c.voiceCtx) return failed("Clone · open an audio workflow first");
        return c.dialog(
          {
            kind: "clone",
            take: t,
            ctx: c.voiceCtx,
            from_s: from_s as number | undefined,
            to_s: to_s as number | undefined,
            name: name as string | undefined,
          },
          progress,
        );
      },
      designVoice: ({ description, name }, { progress, auto }) => {
        if (!c.voiceCtx?.design)
          return failed("Design · no workflow here designs a voice: open an audio workflow");
        return c.dialog(
          {
            kind: "design",
            ctx: c.voiceCtx,
            text: description as string | undefined,
            name: name as string | undefined,
            auto,
          },
          progress,
        );
      },
      // the drawer can't be clicked while a dialog is open: Hear it stays the dialog's own button
      hear: () => failed("Hear · Hear it is yours: click it in Clone a voice or Design a voice"),
      deleteItem: ({ item_id }) => {
        const it = [...(c.library ?? []), ...(c.looks ?? [])].find((x) => x.id === item_id);
        if (!it) return failed("Delete · no such library item");
        if (it.source === "starter")
          return failed(`Delete · ${it.name} is a starter ${it.kind}: it can't be deleted`);
        const name = it.handle ? `@${it.handle}` : it.name;
        return deleteCard(
          `Delete ${name}?`,
          `${it.kind} ${name}`,
          "the takes made with it",
          "This can't be undone. Same as Delete in the library.",
          async () => {
            await ok(
              api.DELETE("/v1/studio/library/{item_id}", { params: { path: { item_id: it.id } } }),
            );
            await reloadLibrary();
            return done(`Deleted ${name}`);
          },
        );
      },
    },
  };
}
