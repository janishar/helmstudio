// The Timeline's screen actions and summary for the assistant (plan 18 §22.4, §22.5; contracts/tools/actions.json
// `timeline`; board frame 2). Edits are the typed Op union saved as the screen saves them (a 409 reloads and tries
// once more), each with Undo (the sequence's own undo); the playhead and range are lines; New shot, every take
// action (extend, retake, insert, replace, redo), New line, Re-render and Export are cards whose Yes is the docked
// form's own Generate (or the dialog's own button); Save as voice opens the clone dialog prefilled.
import { ApiError, api, editTimeline, getTimeline, message, ok, type S } from "@/lib/api";
import { done, failed, ordinal, placeInLine, type Asked, type Outcome } from "@/lib/assistant";
import type { ScreenSpec } from "@/components/assistant/tools";
import { hasSound, takeById, type LibraryAsk } from "@/components/assistant/library";
import type { VoiceCtx } from "@/components/create/voices";
import type { ShotSlot } from "@/components/timeline/shot";
import { mb } from "@/lib/format";
import { withIds } from "@/lib/timeline-tools";
import {
  Grid,
  apply,
  factsOf,
  shortTime,
  shotBusy,
  timecode,
  type Clip,
  type Norm,
  type Op,
} from "@/lib/timeline";

type Timeline = S<"Timeline">;

/** What a refused edit says (lib/timeline codes; studio's 422 carries its own messages). */
const WORDS: Record<string, string> = {
  split_none: "No clip is under the playhead.",
  split_edge: "The playhead is on a cut: move it into a clip to split.",
  split_shot: "A clip being generated cannot be split.",
  too_many_tracks: "A sequence has at most 8 audio tracks.",
  not_alternative: "That take was not made for this slot.",
  dissolve_first: "The first clip cannot dissolve in.",
  dissolve_gap: "A dissolve needs a clip on both sides of the cut.",
  dissolve_handles: "Not enough footage past the cut for that dissolve: trim the clips first.",
  dissolve_length: "The dissolve is longer than the clip.",
  overlap: "Clips on an audio track cannot overlap.",
  past_end: "Sound cannot run past the end of the picture.",
  trim: "That trim is outside the take.",
  hold: "A still lasts at least one frame.",
  no_audio: "That take has no sound.",
  // 1.50, the tools (plan 23)
  roll_first: "The first clip has no cut before it to roll.",
  roll_track: "Roll works on V1's cuts.",
  slide_end: "Slide needs a clip on both sides.",
  slide_track: "Slide works on V1.",
  slip_still: "A still or an empty slot has nothing to slip.",
  least: "That leaves a clip shorter than its dissolves.",
  overwrite_shot: "A clip being generated cannot be overwritten.",
  overwrite_track: "Overwrite keeps a clip on its kind of track.",
  range_empty: "The range is empty.",
  range_shot: "A clip being generated is inside the range.",
  link_start: "A linked sound clip would start before 0.",
  marker_past_end: "A marker would be past the end of the picture.",
  too_many_markers: "A sequence has at most 200 markers.",
};
export const say = (code: string) =>
  WORDS[code] ?? `The edit was refused (${code.replace(/_/g, " ")}).`;

/** A docked form's Generate started: the sequence with the slot's placeholder, or why not. */
export type Started = { error: string } | { timeline: Timeline; clip: string };

/** A docked form (New shot, a take action's form, New line) as the assistant reads it for its card; Generate is
 * the form's own. */
export type Docked = {
  model: string;
  size?: string;
  /** "at 0:12 · 2.0 s · 1 seed" */
  detail: string;
  prompt: string;
  /** the run for /runs/plan (time and memory); null when the form does not resolve yet */
  run: S<"RunRequest"> | null;
  generate: () => Promise<Started>;
};
/** What the assistant asks of a docked form: its prompt, length, where the result goes, the voice. */
export type Want = {
  kind?: "extend" | "retake" | "insert" | "replace";
  prompt?: string;
  seconds?: number;
  voice?: string;
};
/** The form the assistant asked for, once it is open; "none" when the clip offers no such action. */
export type FormSlot = Docked | "none" | null;

/** What the Timeline already works out, read when an action runs. */
export type TimelineCtx = {
  tl: Timeline | null;
  doc: Norm | null;
  frame: number;
  range: [number, number] | null;
  sel: string[];
  plan: S<"ExportPlan"> | null;
  open: "shot" | "line" | null;
  /** the latest stored sequence (tlRef) */
  get: () => Timeline | null;
  set: (t: Timeline) => void;
  /** runs `f` after the edits in flight, as the screen's own saves */
  inQueue: <T>(f: () => Promise<T>) => Promise<T>;
  seek: (frame: number) => void;
  setRange: (r: [number, number] | null) => void;
  select: (ids: string[]) => void;
  goTo: (id: string) => void;
  /** after the sequence is deleted: the next one, else the empty screen */
  reopen: () => void;
  notice: (text: string) => void;
  /** the audio track New line would go on, null when none is free at the playhead */
  lineTrack: string | null;
  /** opens New shot, a clip's take action or New line, prefilled; resolves with its form once it is open */
  dock: (
    to: { shot: ShotSlot } | { clip: string } | { line: true },
    want: Want,
  ) => Promise<FormSlot>;
  dialog: (a: LibraryAsk, progress: (text: string) => void) => Promise<Outcome>;
  voiceCtx: VoiceCtx;
};

const VERB: Record<Op["op"], [string, string]> = {
  move: ["Moved", "clip"],
  trim: ["Trimmed", "clip"],
  split: ["Split", "clip"],
  remove: ["Removed", "clip"],
  lift: ["Lifted", "clip"],
  replace: ["Replaced the take of", "clip"],
  set: ["Set", "clip"],
  add: ["Added", "clip"],
  insert: ["Inserted", "slot"],
  place: ["Placed", "slot"],
  fill: ["Filled", "slot"],
  add_track: ["Added", "audio track"],
  remove_track: ["Removed", "audio track"],
  track: ["Set", "track"],
  roll: ["Rolled", "cut"],
  slip: ["Slipped", "clip"],
  slide: ["Slid", "clip"],
  overwrite: ["Overwrote with", "clip"],
  split_all: ["Split every track", "time"],
  remove_range: ["Removed", "range"],
  lift_range: ["Lifted", "range"],
  marker: ["Set", "marker"],
  remove_marker: ["Removed", "marker"],
};
const plural = (n: number, w: string) => `${n} ${w}${n === 1 ? "" : "s"}`;

/** The edits in words, like the board's "Placed shots 5, 6, 7 at 0:12": runs of one kind, joined by " · ". */
export function editText(ops: Op[]): string {
  const runs: Op[][] = [];
  for (const o of ops)
    if (runs.at(-1)?.[0].op === o.op) runs.at(-1)!.push(o);
    else runs.push([o]);
  return runs
    .map((r) => {
      const o = r[0];
      const [verb, noun] = VERB[o.op];
      const n =
        o.op === "add"
          ? r.reduce((k, x) => k + (x as { take_ids: string[] }).take_ids.length, 0)
          : r.length;
      const at =
        "at" in o &&
        typeof o.at === "number" &&
        (o.op === "place" ||
          o.op === "insert" ||
          o.op === "split" ||
          o.op === "split_all" ||
          o.op === "marker")
          ? ` at ${shortTime(o.at)}`
          : "";
      return `${verb} ${plural(n, noun)}${at}`;
    })
    .join(" · ");
}

/** Saves `ops` on the stored sequence as the screen does, in one POST …/edits with the revision they were made on
 * (labelled `assistant`; studio applies all or nothing, so a refusal saves nothing): checked here op by op first so
 * a refusal says why, up to the first `add` (its takes are studio's to read; from there studio checks and makes the
 * new clip ids); a 409 reloads and tries once more. Returns how many revisions it made (what Undo takes back), or
 * why not. */
async function save(c: TimelineCtx, ops: Op[]): Promise<{ revisions: number } | { error: string }> {
  for (let attempt = 0; attempt < 2; attempt++) {
    const cur = c.get();
    if (!cur) return { error: "no sequence is open" };
    let d: Norm | null = cur;
    const list: Op[] = [];
    for (const [i, raw] of ops.entries()) {
      if (!d) {
        list.push(raw);
        continue;
      }
      const o = withIds(raw, d);
      list.push(o);
      if (o.op === "add") {
        d = null;
        continue;
      }
      let r: ReturnType<typeof apply>;
      try {
        r = apply(d, o, factsOf(cur.takes));
      } catch {
        return { error: `edit ${i + 1} is not a Timeline edit` };
      }
      if ("error" in r) return { error: say(r.error) };
      if ("problems" in r) return { error: say(r.problems[0][0]) };
      d = r.document;
    }
    try {
      c.set(await editTimeline(cur, list, "assistant"));
      return { revisions: 1 };
    } catch (e) {
      if (e instanceof ApiError && e.status === 409 && !attempt) {
        const now = c.get();
        if (now) c.set(await getTimeline(now.id));
        c.notice("The sequence changed since you opened it: reloaded, and the edit applied to it.");
        continue;
      }
      return { error: message(e) };
    }
  }
  return { error: "the sequence keeps changing" };
}

/** The sequence's own undo (or redo) `n` times, refused when it changed since (then it is the screen's Undo). */
function history(c: TimelineCtx, n: number, redo: boolean, at: () => number) {
  return c.inQueue(async () => {
    const start = c.get();
    if (!start || start.revision !== at())
      throw new Error("The sequence changed since: use Undo on the screen");
    for (let i = 0; i < n; i++) {
      const cur = c.get()!;
      c.set(
        await ok(
          api.POST(
            redo
              ? "/v1/studio/timelines/{timeline_id}/redo"
              : "/v1/studio/timelines/{timeline_id}/undo",
            { params: { path: { timeline_id: cur.id } }, body: { revision: cur.revision } },
          ),
        ),
      );
    }
    return c.get()!.revision;
  });
}

const nameOf = (tl: Timeline, c: Clip) =>
  c.number ? `Shot ${c.number}` : (c.take_id && tl.takes[c.take_id]?.title) || "the clip";

/** The Generate card of a docked form: model, size, the shot, machine and place in line, time and memory. */
async function dockCard(title: string, f: Docked, fine: string, what: string): Promise<Asked> {
  const plan = f.run
    ? await ok(api.POST("/v1/studio/runs/plan", { body: f.run })).catch(() => null)
    : null;
  const host = f.run?.host ?? plan?.host ?? null;
  const q = host ? await ok(api.GET("/v1/studio/queue")).catch(() => null) : null;
  const place = placeInLine(q, host);
  const est = [
    host ?? plan?.reason ?? "Auto",
    place && `${ordinal(place)} in line`,
    plan?.peak_gb != null && `${Math.round(plan.peak_gb)} GB`,
  ]
    .filter(Boolean)
    .join(" · ");
  const rows: [string, string][] = [["Model", f.model]];
  if (f.size) rows.push(["Size", f.size]);
  rows.push(["Shot", f.detail]);
  const prompt = f.prompt.trim();
  return {
    card: {
      kind: "generate",
      title,
      rows,
      tags: ["uses the GPU"],
      est,
      quote: prompt ? (prompt.length > 90 ? `${prompt.slice(0, 90)}…` : prompt) : undefined,
      go: "GENERATE",
      fine,
    },
    yes: async () => {
      const r = await f.generate();
      if ("error" in r) return failed(`${what} failed · ${r.error}`);
      const run = r.timeline.tracks.flatMap((t) => t.clips).find((x) => x.id === r.clip)
        ?.shot?.run_id;
      return done(`You clicked GENERATE · ${what}`, {
        result: { clip: r.clip, ...(run && { run_id: run }) },
      });
    },
  };
}

/** The Timeline's registration: its actions, Sees chips, ids and summary. */
export function timelineTools(c: TimelineCtx): ScreenSpec {
  const tl = c.tl;
  const doc = c.doc;
  const g = doc ? new Grid(doc.target) : null;
  const v1 = doc?.tracks[0].clips ?? [];
  const at = g ? g.s(c.frame) : 0;
  const clipOf = (id: unknown) =>
    doc?.tracks.flatMap((t, ti) => t.clips.map((x) => ({ x, ti }))).find((y) => y.x.id === id);
  const need = <T>(f: (tl: Timeline, doc: Norm, g: Grid) => T) =>
    !tl || !doc || !g ? failed("No sequence is open: make one with New sequence") : f(tl, doc, g);
  return {
    screen: "timeline",
    sees: tl
      ? [
          `Timeline · ${tl.name}`,
          ...(c.open === "shot" ? ["New shot"] : c.open === "line" ? ["New line"] : []),
          ...(c.range ? [`Range ${shortTime(c.range[0])} – ${shortTime(c.range[1])}`] : []),
        ]
      : ["Timeline"],
    ids: { timeline_id: tl?.id ?? "", session_id: tl?.session_id ?? "" },
    examples: [
      { text: "Turn this script into shots", sub: "places slots, then waits for your OK" },
      { text: "Trim the last clip to 2 s", sub: "edits with Undo" },
      { text: "Export the sequence", sub: "asks first" },
    ],
    summary: () =>
      tl && doc && g
        ? {
            sequence: { id: tl.id, name: tl.name },
            size: `${doc.target.width} × ${doc.target.height} · ${doc.target.fps} fps`,
            length_s: doc.duration_s,
            playhead_s: Math.round(at * 100) / 100,
            range: c.range,
            selected: c.sel,
            open: c.open,
            can_undo: tl.can_undo,
            can_redo: tl.can_redo,
            tracks: doc.tracks.map((t) => ({
              name: t.name,
              kind: t.kind,
              clips: t.clips.slice(0, 40).map((x) => ({
                id: x.id,
                number: x.number,
                take: x.take_id ? (tl.takes[x.take_id]?.title ?? x.take_id) : null,
                ...(x.at !== undefined && { at: x.at }),
                ...(x.in !== undefined && { in: x.in, out: x.out }),
                ...(x.hold !== undefined && { hold: x.hold }),
                ...(x.shot && { shot: x.shot.status }),
                ...(x.offline && { offline: true }),
              })),
            })),
            shots_running: tl.tracks.flatMap((t) => t.clips).filter(shotBusy).length,
            export_plan: c.plan
              ? {
                  stream_copy: c.plan.stream_copy,
                  blocked: c.plan.blocked.map((b) => b.message),
                  host: c.plan.host,
                }
              : null,
          }
        : { sequence: null },
    actions: {
      edit: ({ ops }) =>
        need(async () => {
          const list = ops as Op[];
          const r = await c.inQueue(() => save(c, list));
          if ("error" in r) return failed(`Edit · ${r.error}`);
          let after = c.get()!.revision;
          const text = editText(list);
          return done(text, {
            undo: {
              undo: async () => (after = await history(c, r.revisions, false, () => after)),
              redo: async () => (after = await history(c, r.revisions, true, () => after)),
            },
          });
        }),
      newSequence: async ({ title }) => {
        const t = await ok(
          api.POST("/v1/studio/timelines", { body: { name: String(title ?? "") } }),
        );
        c.goTo(t.id);
        return done(`New sequence · ${t.name}`, { result: { timeline_id: t.id } });
      },
      renameSequence: ({ title }) =>
        need(async (tl) => {
          const t = await ok(
            api.PATCH("/v1/studio/timelines/{timeline_id}", {
              params: { path: { timeline_id: tl.id } },
              body: { name: String(title).trim() },
            }),
          );
          const cur = c.get();
          if (cur) c.set({ ...cur, name: t.name });
          return done(`Renamed: ${t.name}`);
        }),
      deleteSequence: () =>
        need((tl) => ({
          card: {
            kind: "delete",
            title: `Delete ${tl.name}?`,
            rows: [
              ["Goes", `sequence ${tl.name} · its clips and edits`],
              ["Stays", "its takes in the Gallery"],
            ],
            tags: [],
            go: "DELETE",
            danger: true,
            fine: "This can't be undone. Same as Delete in Sequences.",
          },
          yes: async () => {
            await ok(
              api.DELETE("/v1/studio/timelines/{timeline_id}", {
                params: { path: { timeline_id: tl.id } },
              }),
            );
            c.reopen();
            return done(`Deleted ${tl.name}`);
          },
        })),
      setPlayhead: ({ at_s }) =>
        need((_, doc, g) => {
          const s = Number(at_s);
          if (s > doc.duration_s)
            return failed(`Playhead · the sequence is ${timecode(doc.duration_s)} long`);
          c.seek(g.f(s));
          return done(`Playhead at ${timecode(g.s(g.f(s)))}`);
        }),
      setRange: ({ in_s, out_s }) =>
        need((_, doc) => {
          const [a, b] = [Number(in_s), Number(out_s)];
          if (b <= a) return failed("Range · out must be after in");
          if (b > doc.duration_s)
            return failed(`Range · the sequence is ${timecode(doc.duration_s)} long`);
          c.setRange([a, b]);
          return done(`Range ${timecode(a)} – ${timecode(b)}`);
        }),
      shot: ({ kind, clip, at_s, seconds, prompt }) =>
        need(async (tl, doc) => {
          const k = String(kind);
          const want: Want = {
            prompt: prompt as string | undefined,
            seconds: seconds as number | undefined,
          };
          const y = clip === undefined ? undefined : clipOf(clip);
          if (clip !== undefined && (!y || y.ti !== 0)) return failed("Shot · no such clip on V1");
          let to: Parameters<TimelineCtx["dock"]>[0];
          let title: string;
          let what: string;
          let fine =
            "Same as the Generate shot button on the screen. Change anything there first if you like.";
          if (k === "new") {
            if (y && (y.x.take_id || y.x.shot))
              return failed("Shot · that clip is not an empty slot");
            const next = Math.max(0, ...v1.map((x) => x.number ?? 0)) + 1;
            to = { shot: y ? { clip: y.x.id } : { at: Number(at_s ?? at) } };
            title = `New shot ${next}?`;
            what = `New shot ${next}`;
          } else {
            if (!y || !y.x.take_id || y.x.offline)
              return failed(`Shot · ${k} needs a clip with a take`);
            if (shotBusy(y.x)) return failed("Shot · that clip is being generated");
            const name = nameOf(tl, y.x);
            if (k === "redo") {
              to = { shot: { clip: y.x.id, redo: true } };
              title = `Redo ${name}?`;
              what = `Redo ${name}`;
            } else {
              if (tl.takes[y.x.take_id]?.kind !== "video")
                return failed(`Shot · ${k} works on a video clip`);
              c.select([y.x.id]);
              to = { clip: y.x.id };
              want.kind = k as Want["kind"];
              const verb = {
                extend: "Extend",
                retake: "Retake",
                insert: "Insert after",
                replace: "Replace",
              }[k];
              title = `${verb} ${name}?`;
              what = `${verb} ${name}`;
              fine =
                "Same as Generate on the clip's take action. Change anything there first if you like.";
            }
          }
          const f = await c.dock(to, want);
          if (f === "none")
            return failed(
              "clip" in to
                ? `Shot · that clip offers no ${k}`
                : "Shot · no video workflow is installed",
            );
          if (!f) return failed("Shot · the form did not open");
          void doc;
          return dockCard(title, f, fine, what);
        }),
      newLine: ({ text, voice }) =>
        need(async () => {
          if (!c.lineTrack) return failed("New line · no audio track is free at the playhead");
          const f = await c.dock(
            { line: true },
            { prompt: String(text), voice: voice as string | undefined },
          );
          if (!f || f === "none") return failed("New line · no voice workflow is installed");
          return dockCard(
            "New line?",
            f,
            "Same as the Generate line button on the screen. Change anything there first if you like.",
            "New line",
          );
        }),
      saveAsVoice: ({ clip }, { progress }) =>
        need(async (tl) => {
          const y = clipOf(clip);
          if (!y?.x.take_id) return failed("Save as voice · no such clip with a take");
          const t = await takeById(y.x.take_id, []);
          if (!t || !hasSound(t)) return failed("Save as voice · that clip has no sound");
          void tl;
          return c.dialog({ kind: "clone", take: t, ctx: c.voiceCtx }, progress);
        }),
      rerender: () =>
        need((tl) => {
          const chosen = v1.filter((x) => c.sel.includes(x.id) && x.take_id && !x.offline);
          const all = v1.filter((x) => x.take_id && !x.offline);
          const clips = chosen.length ? chosen : all;
          if (!clips.length) return failed("Re-render · no clip here was made by a workflow");
          const n = clips.length;
          return {
            card: {
              kind: "batch",
              title: chosen.length ? `Re-render ${plural(n, "clip")}?` : "Re-render the sequence?",
              rows: [
                [
                  "Clips",
                  clips
                    .map((x) => nameOf(tl, x))
                    .join(", ")
                    .slice(0, 300),
                ],
                ["Preset", "each clip's own settings"],
                ["Runs as", "a batch: each result becomes its slot's take"],
              ],
              tags: ["uses the GPU"],
              go: "RE-RENDER",
              fine: "Same as Re-render on the screen.",
            },
            yes: async () => {
              const cur = c.get()!;
              const r = await ok(
                api.POST("/v1/studio/timelines/{timeline_id}/rerender", {
                  params: { path: { timeline_id: cur.id } },
                  body: {
                    revision: cur.revision,
                    ...(chosen.length && { clips: chosen.map((x) => x.id) }),
                  },
                }),
              );
              c.set(r.timeline);
              c.notice("Re-rendering as a batch: each result becomes its slot's take.");
              return done(`You clicked RE-RENDER · ${plural(n, "clip")}`, {
                result: { batch_id: r.batch.id },
              });
            },
          };
        }),
      export: ({ preset }) =>
        need(async (tl, doc) => {
          if (preset !== undefined)
            return failed("Export · there are no presets: it exports at the sequence's size");
          const p = await ok(
            api.POST("/v1/studio/timelines/{timeline_id}/export/plan", {
              params: { path: { timeline_id: tl.id } },
              body: {},
            }),
          );
          if (p.blocked.length) return failed(`Export · ${p.blocked[0].message}`);
          const t = doc.target;
          return {
            card: {
              kind: "export",
              title: `Export ${tl.name}?`,
              rows: [
                ["Mode", p.stream_copy ? "stream copy" : "re-encode"],
                ["Size", `${t.width} × ${t.height} · ${t.fps} fps`],
                [
                  "Length",
                  `${timecode(p.duration_s)} · about ${mb(p.estimated_bytes)} MB`,
                ],
                ["Runs on", p.host ?? "no host can run it now"],
              ],
              tags: ["uses the machine"],
              go: "EXPORT",
              fine: "Same as Export on the screen. It lands in the Gallery.",
            },
            yes: async () => {
              const cur = c.get();
              if (!cur || cur.revision !== p.revision)
                return failed("Export · the sequence changed since: ask again");
              const j = await ok(
                api.POST("/v1/studio/timelines/{timeline_id}/export", {
                  params: { path: { timeline_id: cur.id } },
                  body: { revision: p.revision },
                }),
              );
              return done(`You clicked EXPORT · ${tl.name}`, { result: { job_id: j.id } });
            },
          };
        }),
      cancelExport: async ({ job_id }) => {
        await ok(
          api.POST("/v1/studio/media-jobs/{job_id}/cancel", {
            params: { path: { job_id: String(job_id) } },
          }),
        );
        return done("Cancelled the export");
      },
    },
  };
}
