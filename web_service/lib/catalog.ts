// Catalog Agent, the pure parts of the screen: the Input flow and its copy, server state → what the UI shows, and
// session events → chat lines. Everything here is plain data in, plain data out (tests/lib.test.ts).
import type { S } from "@/lib/api";

export type Detail = S<"SessionDetail">;
export type CatalogSession = S<"CatalogSession">;
export type Sku = S<"Sku">;
export type Image = S<"Image">;
export type Run = S<"Run">;
export type Library = S<"Library">;
export type Item = S<"InputItem">;
export type StepInput = S<"StepInput">;
export type Inputs = S<"Inputs">;
export type Step = keyof Inputs;
export type Flow = S<"FlowPosition">;
export type SessionEvent = S<"SessionEvent">;
export type MemoryEntry = S<"MemoryEntry">;
export type Upload = S<"Upload">;
export type PoseNoun = S<"PoseNoun">;

/** A button on a question card: `key` says what it does, `label` is its text. */
export type Action = { key: string; label: string };

/** One chat entry, stored on the server in `session.messages` as it is here. Input lines have no `phase`;
 * Processing's have `phase: "p"`. Which other fields are set depends on `kind`. */
export type Msg = {
  id: string;
  kind: string;
  phase?: "p";
  text?: string;
  title?: string;
  section?: Step;
  step?: Flow;
  actions?: Action[];
  answered?: string;
  write?: boolean;
  last?: Item[];
  project?: string;
  label?: string;
  detail?: string | null;
  reused?: boolean;
  item?: Item;
  done?: boolean;
  error?: boolean;
  question_id?: string;
  steps?: string[];
  examples?: string[];
  icon?: string;
  category?: string;
  seq?: number;
  held?: boolean;
  credits?: number;
  worst?: number;
};

/** The Input steps, in the order of the left list. */
export const STEPS: Step[] = ["raw", "guidelines", "output", "poses", "models", "backgrounds", "details"];
/** The guided flow: raw images (processing starts in the background), guidelines, a wait for processing and its
 * questions, the other inputs one by one, the summary, then Processing (`done`). */
export const FLOW: Flow[] = [
  "raw",
  "guidelines",
  "processing",
  "output",
  "poses",
  "models",
  "backgrounds",
  "details",
  "summary",
  "done",
];
/** True for a flow position that is one of the Input steps. */
export const isStep = (f: string | undefined): f is Step => STEPS.includes(f as Step);
/** The flow position after `f`. */
export const nextFlow = (f: Flow): Flow => FLOW[Math.min(FLOW.indexOf(f) + 1, FLOW.length - 1)];

/** Each step's name in the left list. */
export const TAB_LABEL: Record<Step, string> = {
  raw: "Raw images",
  guidelines: "Brand guidelines",
  output: "Resolution & ratio",
  poses: "Poses selection",
  models: "Models",
  backgrounds: "Backgrounds",
  details: "Additional details",
};
/** What each input is called on its buttons: "Add more raw images", "Skip brand guidelines". */
export const NOUN: Record<Step, string> = {
  raw: "raw images",
  guidelines: "brand guidelines",
  output: "resolution & ratio",
  poses: "poses",
  models: "models",
  backgrounds: "backgrounds",
  details: "additional details",
};
/** What the agent says when a step is skipped. */
export const SKIP_NOTE: Record<Step, string> = {
  raw: "",
  guidelines: "Skipped — I’ll use ORA defaults.",
  output: "Skipped — 2K at 1:1.",
  poses: "Skipped — ORA default poses.",
  models: "Skipped — I’ll pick models for you.",
  backgrounds: "Skipped — each product’s default background.",
  details: "Skipped.",
};
/** Inputs that hold one choice: editing them replaces it. The others (images, guidelines, details) add up. */
export const SINGLE_CHOICE: Step[] = ["output", "poses", "models", "backgrounds"];

/** done (something added), skipped, or open. */
export const sectionState = (input: StepInput) =>
  input.items.length ? "done" : input.skipped ? "skipped" : "open";
/** How an input was filled for the left list's icon: added, reused from last time, skipped, or not yet. */
export function inputStatus(input: StepInput): "added" | "reused" | "skipped" | "open" {
  if (input.items.length) return input.items.every((i) => i.reused) ? "reused" : "added";
  return input.skipped ? "skipped" : "open";
}
/** The "{n}/7 ready" count: steps added, reused or skipped. */
export const readyCount = (inputs: Inputs) =>
  STEPS.filter((s) => sectionState(inputs[s]) !== "open").length;

/** A client-chosen id for a chat line or an input item. */
export const newId = () => `m${Date.now().toString(36)}${Math.random().toString(36).slice(2, 6)}`;

/** The opening screen: a headline and the recommended next step (raw images). */
export const WELCOME: Msg = {
  id: "welcome",
  kind: "hero",
  title: "Let's create catalog images for you",
  text: "I’ll guide you step by step.",
};
/** Opens Input again after Refresh clears it for the next batch. */
export const NEXT_BATCH: Msg = {
  id: "next-batch",
  kind: "hero",
  title: "Let's create your next batch of catalog images",
  text: "Your last batch is in the SKU list.",
};
/** How Processing works, posted once when Processing first opens; `name` is the first category's, e.g. "rings",
 * `noun` what its poses are called. */
export const guideStart = (name: string, noun: PoseNoun = GENERIC_NOUN): Omit<Msg, "id"> => ({
  kind: "guide",
  phase: "p",
  title: "How Processing works",
  text: "A sample first, then the rest in batches. Images show on the right. You can also type a request:",
  examples: [`run the rest of the ${name}`, `remove the lifestyle ${noun.singular}`],
});
/** The "What I can do" guide, the answer to a request the agent did not understand. */
export const help = (name: string, noun: PoseNoun = GENERIC_NOUN): Omit<Msg, "id"> => ({
  kind: "guide",
  phase: "p",
  title: "What I can do",
  text: `Run, approve, change ${noun.plural} or download. For example:`,
  examples: [`run the rest of the ${name}`, `add a close-up ${noun.singular}`, `approve the ${name}`],
});

/** A heading in the chat for a step; steps before the latest heading show as done. */
export const marker = (section: Step): Msg => ({ id: newId(), kind: "step", section });

/** The agent's question when the flow reaches a step, with last time's choice when memory has one; `noun` is what
 * the brand's products call a pose. */
export function stepIntro(step: Flow, mem: MemoryEntry | undefined, noun: PoseNoun = GENERIC_NOUN): Msg | null {
  const last = mem?.items.length ? { last: mem.items, project: mem.project_name } : {};
  const ask = (text: string, actions: Action[]): Msg => ({ id: newId(), kind: "ask", step, text, actions, ...last });
  if (step === "summary")
    return {
      id: newId(),
      kind: "ask",
      step,
      text: "All set? I’ll run a sample first, then the rest.",
      actions: [{ key: "generation", label: "Start processing" }],
    };
  if (!isStep(step) || step === "raw") return null;
  if (step === "poses") return { id: newId(), kind: "agent", text: `Which ${noun.plural} do you want?` }; // its panel opens at once
  const name = NOUN[step];
  if (step === "details")
    return {
      ...ask("Write any additional details for this project — do’s, don’ts, anything specific.", [
        ...(mem?.items.length ? [{ key: "reuse", label: "Reuse last time’s additional details" }] : []),
        { key: "change", label: "Attach a brief" },
        { key: "skip", label: "Skip additional details" },
      ]),
      write: true,
    };
  const choose: Action[] = [
    ...(mem?.items.length ? [{ key: "reuse", label: `Reuse last time's ${name}` }] : []),
    { key: "change", label: `Add new ${name}` },
    { key: "skip", label: `Skip ${name}` },
  ];
  const text = {
    guidelines: "Any brand guidelines? Brand book, lookbook, colours.",
    output: "What size and ratio do you need?",
    models: "Which models should we use?",
    backgrounds: "Which backgrounds should we use?",
  }[step];
  return ask(text, choose);
}

/** Questions from earlier steps can't be answered once the flow moves on. */
export const retire = (ms: Msg[]) =>
  ms.map((m) => (m.kind === "ask" && !m.answered && !m.phase ? { ...m, answered: "_" } : m));

/** Adds Input lines before the questions still waiting, so an open question stays the last thing in the chat. */
export function insertBeforeWaiting(ms: Msg[], added: Msg[]): Msg[] {
  const waiting = ms.filter((m) => m.kind === "ask" && !m.answered && !m.phase);
  return [...ms.filter((m) => !waiting.includes(m)), ...added, ...waiting];
}

/** The chat with a card for every open question it doesn't show (its event was missed, or it is still held), so an
 * unanswered blocking question can't hide behind Start processing. Unchanged when nothing is missing. */
export function withOpenQuestions(ms: Msg[], questions: { id: string; answer: number | null }[]): Msg[] {
  const shown = new Set(ms.filter((m) => m.kind === "question" && !m.held).map((m) => m.question_id));
  const missing = questions.filter((q) => q.answer === null && !shown.has(q.id));
  if (!missing.length) return ms;
  const ids = new Set(missing.map((q) => `q:${q.id}`));
  const cards: Msg[] = missing.map((q) => ({ id: `q:${q.id}`, kind: "question", question_id: q.id }));
  return [...ms.filter((m) => !ids.has(m.id)), ...cards];
}

/** The id of the hidden chat entry that holds the last event seq already turned into chat lines. */
const CURSOR = "_cursor";
/** The last event seq the chat has seen (0 when none). */
export const cursorOf = (ms: Msg[]) => ms.find((m) => m.id === CURSOR)?.seq ?? 0;
/** The chat with its cursor set to `seq`. */
export const withCursor = (ms: Msg[], seq: number): Msg[] => [
  ...ms.filter((m) => m.id !== CURSOR),
  { id: CURSOR, kind: "cursor", seq },
];

/**
 * Turns one session event into chat lines. Processing logs become status lines (replacing the "Processing images…"
 * placeholder of that job), questions a question card, a failed job an error line, a finished run's SKUs their
 * "R-001 done" line and a fresh status bar. Events at or below the cursor are already in the chat. With `hold`
 * (Brand guidelines not answered yet) sorting results and questions are kept hidden until `release`: the demo asks
 * its conflict questions after that step.
 */
export function applyEvent(ms: Msg[], ev: SessionEvent, processing = false, hold = false): Msg[] {
  if (ev.seq <= cursorOf(ms)) return ms;
  const d = ev.data as Record<string, unknown>;
  const work = ev.work_id ?? "";
  const placeholder = `bg:${work}`;
  let out = ms;
  /** Updates the line with the same id in place, else adds it. */
  const put = (m: Msg) => {
    const i = out.findIndex((x) => x.id === m.id);
    if (i >= 0) out = out.map((x, j) => (j === i ? { ...x, ...m } : x));
    else out = m.phase ? [...out, m] : insertBeforeWaiting(out, [m]);
  };
  if (ev.type === "log") {
    const line: Msg = {
      ...(processing ? { phase: "p" as const } : {}),
      id: `log:${work}:${String(d.key)}`,
      kind: "status",
      text: String(d.text ?? ""),
      done: d.state !== "running",
      error: d.state === "error",
      ...(d.detail ? { detail: String(d.detail) } : {}), // the longer text behind the line (View details)
      ...(hold && d.key !== "ingest" ? { held: true } : {}),
    };
    const ph = processing ? -1 : out.findIndex((x) => x.id === placeholder && !x.done);
    const seen = (x: Msg) => x.id !== line.id && x.id.endsWith(`:${String(d.key)}`) && x.text === line.text;
    if (line.done && out.some(seen)) out = out.filter((x) => x.id !== line.id); // a re-check says the same: once
    else if (ph >= 0 && !out.some((x) => x.id === line.id)) out = out.map((x, j) => (j === ph ? line : x));
    else put(line);
  } else if (ev.type === "question" && d.id) {
    put({ id: `q:${String(d.id)}`, kind: "question", question_id: String(d.id), ...(hold ? { held: true } : {}) });
  } else if (ev.type === "status" && d.status !== "running") {
    const failed = d.status === "failed";
    const why = `${String(d.error ?? "unknown error")}`;
    if (d.work === "process") {
      const had = out.some((x) => x.id === placeholder && !x.done);
      out = out.map((x) =>
        x.id === placeholder && !x.done
          ? { ...x, done: true, error: failed, text: failed ? `Processing failed — ${why}` : "Images processed." }
          : x,
      );
      if (failed && !had)
        put({ id: `status:${work}`, kind: "status", done: true, error: true, text: `Processing failed — ${why}` });
    } else if (failed) {
      const text = d.error === STOPPED ? stoppedLine() : `The run failed — ${why}`;
      put({ id: `status:${work}`, kind: "update", phase: "p", icon: "redo", text });
    } else {
      out = out.filter((x) => x.kind !== "statusbar"); // one status bar: the newest
      put({ id: `bar:${work}`, kind: "statusbar", phase: "p" });
    }
  } else if (ev.type === "run" && d.sku_status === "To review" && d.line) {
    put({ id: `run:${ev.seq}`, kind: "update", phase: "p", icon: "done", text: String(d.line) });
  }
  return withCursor(out, ev.seq);
}

/** The held lines (processing results that arrived before Brand guidelines was answered), now shown. */
export const released = (ms: Msg[]) => ms.filter((m) => m.held).map((m) => ({ ...m, held: false }));

/** `list` with each of `add` replacing the entry of the same id, new ones appended. */
export function upsert<T extends { id: string }>(list: T[], add: T[] = []): T[] {
  const byId = new Map(add.map((a) => [a.id, a]));
  return [...list.map((x) => byId.get(x.id) ?? x), ...add.filter((a) => !list.some((x) => x.id === a.id))];
}

/** A mutation's answer folded into the session detail: changed SKUs, images, questions and a run (newest first);
 * `removed` images (a stopped run's) are dropped. */
export function mergeDetail(
  d: Detail,
  u: { skus?: Sku[]; images?: Image[]; questions?: S<"Question">[]; run?: Run; session?: CatalogSession; removed?: string[] },
): Detail {
  const run = u.run;
  return {
    session: u.session ?? d.session,
    skus: upsert(d.skus, u.skus),
    images: upsert(u.removed ? d.images.filter((im) => !u.removed!.includes(im.id)) : d.images, u.images),
    questions: upsert(d.questions, u.questions),
    runs: run
      ? d.runs.some((r) => r.id === run.id)
        ? d.runs.map((r) => (r.id === run.id ? run : r))
        : [run, ...d.runs]
      : d.runs,
  };
}

/** The key of one shot of one SKU. */
export const shotKey = (skuId: string, shot: string) => `${skuId}|${shot}`;
/** Every revision of one SKU × shot, oldest first: the feedback thread of that image (each revision carries the
 * feedback that produced it and its tries). */
export const revisionsOf = (images: Image[], skuId: string, shot: string): Image[] =>
  images.filter((im) => im.sku_id === skuId && im.shot === shot).sort((a, b) => a.revision - b.revision);
/** The live image per SKU × shot: its newest revision. */
export function liveImages(images: Image[]): Map<string, Image> {
  const live = new Map<string, Image>();
  for (const im of images) {
    const k = shotKey(im.sku_id, im.shot);
    const cur = live.get(k);
    if (!cur || im.revision > cur.revision) live.set(k, im);
  }
  return live;
}
/** A SKU's shots: its brief's poses, then any other shot that has an image. */
export function shotsOf(sku: Sku, live: Map<string, Image>): string[] {
  const extra = [...live.values()].filter((im) => im.sku_id === sku.id && !sku.brief.poses.includes(im.shot));
  return [...sku.brief.poses, ...extra.map((im) => im.shot)];
}
/** A stopped run's error (catalog_service sets it on Stop). */
export const STOPPED = "Stopped";
/** The chat line of a Stop: what stopped (a SKU code, "1 image"; none = the run), what stays, and the charge caveat. */
export const stoppedLine = (what?: string) =>
  `Stopped${what ? ` ${what}` : ""}. Finished images stay. Images already in progress may still incur charges.`;
/** The runs still queued or running: what Stop stops. */
export const activeRuns = (runs: Run[]) => runs.filter((r) => r.status === "queued" || r.status === "running");

/** Queued or Running. */
export const isBusy = (s: Pick<Sku, "status">) => s.status === "Running" || s.status === "Queued";

/** Where the whole catalog stands, for the status bar. */
export const statusCounts = (skus: Sku[]) => ({
  total: skus.length,
  approved: skus.filter((s) => s.status === "Approved").length,
  review: skus.filter((s) => s.status === "To review").length,
  running: skus.filter(isBusy).length,
  notRun: skus.filter((s) => s.status === "Not run" || s.status === "Redo").length,
});

/** "1 image", "2 images": regular plurals only. */
export const plural = (n: number, word: string) => `${n} ${word}${n === 1 ? "" : "s"}`;
/** Up to three SKU codes by name, else "{n} SKUs". */
export const idList = (codes: string[]) => (codes.length <= 3 ? codes.join(", ") : plural(codes.length, "SKU"));
/** A credit quote as "~300 cr". */
export const cr = (credits: number) => `~${Math.round(credits).toLocaleString("en")} cr`;
/** A quote: `credits` for one render of each image, `worst` when every review fails and each image is rendered its
 * product's `max_attempts` times (catalog's `worst_credits`, or quoteSkus / quoteImages). */
export type Quote = { credits: number; worst: number };
/** A quote with its worst case for tight buttons, "~300–1,200 cr". Just "~300 cr" when there are no retries. */
export function crRange({ credits, worst }: Quote): string {
  return worst > credits && credits > 0 ? `${cr(credits).slice(0, -3)}–${Math.round(worst).toLocaleString("en")} cr` : cr(credits);
}
/** The same in words where there is room: "~300 cr (up to 1,200 if every review fails)". */
export function crWorst({ credits, worst }: Quote, c?: string): string {
  c ??= cr(credits);
  return worst > credits && credits > 0 ? `${c} (up to ${Math.round(worst).toLocaleString("en")} if every review fails)` : c;
}

/** A category (product) of the library. */
export const catOf = (lib: Library | null, id: string) => lib?.categories.find((c) => c.id === id);
/** A category's name from the library, else its id. */
export const catName = (lib: Library | null, id: string) => catOf(lib, id)?.name ?? id;
/** A SKU's subcategory name from the library ("Kurtis"), else null. */
export const subName = (lib: Library | null, sku: Pick<Sku, "category" | "subcategory">) =>
  catOf(lib, sku.category)?.subcategories.find((s) => s.id === sku.subcategory)?.name ?? null;
/** The try of an image revision shown big that the brand may keep instead of the reviewer's best: one that is not
 * the image, never while the revision is generating. */
export const pickableTry = (im: Image, shownUrl?: string) =>
  im.status === "generating" ? undefined : im.attempts.find((t) => !t.best && t.url === shownUrl);
/** What a generating image's tile shows once a try has rendered: the newest try's picture and where it stands ("Try 2
 * of 3 · reviewing…", or "Rejected — <the first failed goal's note, else the summary> · trying again (3 of 3)"); null
 * before the first try. `max` is the product's renders per image (attemptsFor). */
export function liveTry(im: Image, max: number): { url: string; text: string } | null {
  const t = im.attempts.at(-1);
  if (im.status !== "generating" || !t) return null;
  if (!t.review) return { url: t.url, text: `Try ${t.index} of ${max} · reviewing…` };
  const why = t.review.goals?.find((g) => !g.passed)?.note || t.review.summary;
  return { url: t.url, text: `Rejected — ${fit(why, 60)} · trying again (${t.index + 1} of ${max})` };
}
/** Whether the brand, not the reviewer, chose the try kept as the image. */
export const pickedByBrand = (im: Image) => im.attempts.some((t) => t.best && t.picked);

/** A subcategory's info tip ("Kurtis: A shorter, casual tunic top, hip to knee length. Styled with: …"; jewellery
 * "Worn: …", furniture "Shown: <where the room scene puts it>"), so a brand tells close types apart; undefined when the library says
 * nothing about it. */
export function subcategoryTip(lib: Library | null, category: string, id: string | null | undefined): string | undefined {
  const cat = catOf(lib, category);
  const s = cat?.subcategories.find((x) => x.id === id);
  const how = { furniture: "Shown", jewellery: "Worn" }[cat?.family ?? ""] ?? "Styled with";
  return s?.about ? `${s.name}: ${s.about}.${s.styling ? ` ${how}: ${s.styling}.` : ""}` : undefined;
}
/** The info tip of a kind question's option "<Product> › <Subcategory>" (names, as kind_name writes them); undefined
 * for any other label. */
export function kindTip(lib: Library | null, label: string): string | undefined {
  const [product, sub] = label.split(" › ");
  const cat = lib?.categories.find((c) => c.name === product);
  return cat && subcategoryTip(lib, cat.id, cat.subcategories.find((s) => s.name === sub)?.id);
}
/** What a SKU is: "Tops › Kurtis", or just the category when it has no subcategory. */
export const skuKind = (lib: Library | null, sku: Pick<Sku, "category" | "subcategory">) =>
  [catName(lib, sku.category), subName(lib, sku)].filter(Boolean).join(" › ");
/** False for a product shot without a model (furniture); unknown products count as using one. */
export const usesModel = (lib: Library | null, id: string) => catOf(lib, id)?.uses_model ?? true;
/** "No model — furniture is shot as a scene" for these SKUs' products that use no model, else null: a family with no
 * model at all is named and shot as a scene, a product-only group of another family (coins) is shot on its own. */
export function sceneNote(lib: Library | null, skus: Pick<Sku, "category">[]): string | null {
  const notes = skus.filter((s) => !usesModel(lib, s.category)).map((s) => {
    const fam = catOf(lib, s.category)?.family ?? "";
    return lib?.categories.some((c) => c.family === fam && c.uses_model)
      ? `${catName(lib, s.category).toLowerCase()} are shot on their own`
      : `${(lib?.families.find((x) => x.id === fam)?.name ?? fam).toLowerCase()} is shot as a scene`;
  });
  return notes.length ? `No model — ${[...new Set(notes)].join(", ")}` : null;
}
/** True when the Models step matters: no SKUs yet, or one of them is a product with a model. */
export const needsModel = (lib: Library | null, skus: Pick<Sku, "category">[]) =>
  !skus.length || skus.some((s) => usesModel(lib, s.category));
/** What a pose is called where products don't agree on a word (several families, or none known yet). */
export const GENERIC_NOUN: PoseNoun = { singular: "shot", plural: "shots" };
/** What these products call a pose ("angle" for furniture, else "shot"): their library pose_noun when they all
 * share one, else the generic "shot". */
export function poseNoun(lib: Library | null, cats: string | string[]): PoseNoun {
  const nouns = [cats].flat().map((c) => catOf(lib, c)?.pose_noun);
  const first = nouns[0];
  return first && nouns.every((n) => n?.singular === first.singular && n.plural === first.plural) ? first : GENERIC_NOUN;
}
/** "{n} angle(s)" in a product's own pose noun. */
export const nounCount = (n: number, noun: PoseNoun) => `${n} ${n === 1 ? noun.singular : noun.plural}`;
/** `text` with its words "pose"/"poses" (any capital) said in `noun`, for the fixed copy written with "pose". */
export const withNoun = (text: string, noun: PoseNoun) =>
  text.replace(/\b([Pp])os(es?)\b/g, (_, p: string, e: string) => {
    const w = e === "es" ? noun.plural : noun.singular;
    return p === "P" ? w[0].toUpperCase() + w.slice(1) : w;
  });
/** A pose's name from the library, else its id. */
export const poseName = (lib: Library | null, id: string) => lib?.poses.find((p) => p.id === id)?.name ?? id;
/** Credits per image at a resolution (the library default when none). */
export function creditsPerImage(lib: Library | null, resolution?: string | null): number {
  const res = resolution ?? lib?.defaults.resolution;
  return lib?.resolutions.find((r) => r.id === res)?.credits_per_image ?? 0;
}
/** Renders per image at most for a product: its own `max_attempts`, else the library's. */
export const attemptsFor = (lib: Library | null, category?: string) =>
  (category ? catOf(lib, category)?.max_attempts : undefined) ?? lib?.max_attempts ?? 1;
/** One image of a SKU: credits at its resolution (else its product's default), times its product's attempts. */
function skuQuote(lib: Library | null, sku: Pick<Sku, "category" | "brief"> | undefined, images = 1): Quote {
  const c = images * creditsPerImage(lib, sku?.brief.resolution ?? (sku && catOf(lib, sku.category)?.default_resolution));
  return { credits: c, worst: c * attemptsFor(lib, sku?.category) };
}
const sum = (qs: Quote[]): Quote => qs.reduce((a, q) => ({ credits: a.credits + q.credits, worst: a.worst + q.worst }), { credits: 0, worst: 0 });
/** The quote for regenerating these images: each at its SKU's resolution and product's attempts. */
export const quoteImages = (imageIds: string[], d: Detail, lib: Library | null): Quote =>
  sum(imageIds.map((id) => skuQuote(lib, d.skus.find((s) => s.id === d.images.find((x) => x.id === id)?.sku_id))));
/** The quote for running these SKUs with their brief poses. */
export const quoteSkus = (skus: Sku[], lib: Library | null): Quote =>
  sum(skus.map((s) => skuQuote(lib, s, Math.max(1, s.brief.poses.length))));

/** A model or background the brand can pick: from the library or one of this session's uploads. `hover` is a second
 * picture shown on hover (a model's full-body view). */
export type Choice = { id: string; name: string; image: string | null; hover?: string | null; swatch?: string | null; tags?: string[]; families?: string[]; wearer?: string };
/** The models (or backgrounds) to pick from: the library's, then the session's own uploads of that kind. */
export function choices(lib: Library | null, uploads: Upload[], kind: "models" | "backgrounds"): Choice[] {
  const own = uploads
    .filter((u) => u.kind === (kind === "models" ? "model" : "background"))
    .map((u) => ({ id: u.id, name: u.name, image: u.url }));
  const builtIn: Choice[] =
    kind === "models"
      ? (lib?.models ?? []).map((m) => ({ id: m.id, name: m.name, image: m.image_url, hover: m.full_body_url, tags: m.category_tags, wearer: m.wearer }))
      : (lib?.backgrounds ?? []).map((b) => ({ id: b.id, name: b.name, image: b.image_url, swatch: b.swatch, families: b.families }));
  return [...builtIn, ...own];
}

/** Whose wear a session's SKUs on a model are, one per SKU: "men" for men's wear, else "women" (wear not known yet
 * shows the women's models, the library's defaults); a product shot without a model (intimates, a sofa) has no say. */
export const wearersOf = (skus: Sku[], lib: Library | null) =>
  new Set(skus.filter((s) => usesModel(lib, s.category)).map((s) => (s.attributes.wearer === "men" ? "men" : "women")));
/** Whether a model (or background) choice suits these wearers: one without a wearer (an upload) always, and every
 * choice when none are known (no SKUs yet). */
export const suitsWearers = (c: Choice, wearers: Set<string>) => !c.wearer || !wearers.size || wearers.has(c.wearer);

/** A SKU's model or background as the SKU list shows it: the pick's name, "Aisha + described" when the brand also
 * described it, "Described" when only described. */
export const choiceLabel = (name: string | null | undefined, described?: string | null) =>
  described ? (name ? `${name} + described` : "Described") : (name ?? "—");

/** The models picker's Ethnicity tiles: picking one makes its text the (described-only) model's description. The
 * texts are gender-neutral; the wearer comes from the products. */
export const LOOKS: { id: string; text: string }[] = [
  { id: "north-indian", text: "North Indian appearance: wheatish to light-brown skin, dark hair, dark brown eyes" },
  { id: "south-indian", text: "South Indian appearance: medium to deep brown skin, dark wavy hair, dark eyes" },
  { id: "east-indian", text: "East Indian (Bengali) appearance: light-brown to medium skin, dark hair, large dark eyes" },
  { id: "northeast-indian", text: "Northeast Indian appearance: light skin with warm undertones, straight dark hair, monolid or hooded eyes" },
  { id: "west-indian", text: "West Indian (Gujarati/Maharashtrian) appearance: wheatish to medium-brown skin, dark hair, dark eyes" },
  { id: "east-asian", text: "East Asian appearance: light skin, straight black hair, monolid eyes" },
  { id: "southeast-asian", text: "Southeast Asian appearance: light-tan to golden-brown skin, straight dark hair" },
  { id: "middle-eastern", text: "Middle Eastern appearance: olive to light-tan skin, dark wavy hair, dark eyes" },
  { id: "african", text: "African appearance: deep brown skin, natural coily hair, dark eyes" },
  { id: "western-european", text: "Western European appearance: fair to light skin, light-brown to blonde hair" },
  { id: "northern-european", text: "Northern European (Scandinavian) appearance: very fair skin, blonde hair, light blue or grey eyes" },
  { id: "southern-european", text: "Southern European (Mediterranean) appearance: olive skin, dark brown hair, brown eyes" },
  { id: "eastern-european", text: "Eastern European appearance: fair skin, light-brown hair, high cheekbones, light eyes" },
];
/** A look tile's name ("North Indian") and its sketch: the men's when every SKU of the session is men's wear (its
 * `wearer` attribute), else the women's. */
export const lookName = (text: string) => text.split(" appearance")[0];
export const lookImage = (id: string, skus: Sku[]) =>
  skus.length && skus.every((s) => s.attributes.wearer === "men")
    ? `/catalog/looks/men/${id}.webp`
    : `/catalog/looks/${id}.webp`;

/** What the edit drawer sends for a model or background description: undefined (leave it) unless it changed (null
 * clears it), or with Apply to all when it says something; an untouched empty box never wipes the step's description
 * from a whole category. */
export const descriptionPatch = (now: string, was: string | null | undefined, applyAll: boolean) =>
  now !== (was ?? "") || (applyAll && now) ? now || null : undefined;

/** A session's custom shot: one the brand's brief asked for that the Shots library doesn't have. */
export type CustomPose = S<"CustomPose">;
/** The library with the session's custom shots added as poses (no example picture: "No example yet"), so every pose
 * lookup (names, plans, the SKU drawer) finds them. */
export function withCustomPoses(lib: Library | null, d: Detail | null): Library | null {
  const custom = d?.session.inputs.poses.items.flatMap((i) => i.custom_poses ?? []) ?? [];
  if (!lib || !custom.length) return lib;
  return { ...lib, poses: [...lib.poses, ...custom.map((c) => ({ id: c.id, category: c.category, name: c.name, example_url: null }))] };
}
/** A poses plan with its custom shots' definitions: each custom id in the plan keeps the newest definition of it in
 * `defs` (later wins), and one with no definition left is dropped (catalog would refuse it). */
export function keepCustom(plan: Record<string, string[]>, defs: CustomPose[]) {
  const byId = new Map(defs.map((c) => [c.id, c]));
  const poses = Object.fromEntries(
    Object.entries(plan).map(([cat, ids]) => [cat, ids.filter((p) => !p.startsWith("custom-") || byId.has(p))]),
  );
  const used = new Set(Object.values(poses).flat());
  return { poses, custom: [...byId.values()].filter((c) => used.has(c.id)) };
}
/** A "Describe your shots" text that sets its own number of shots ("4 shots: …"). */
export const SHOT_COUNT = /\b\d{1,2}\s+(?:shots?|poses?|angles?|looks?|images?)\b/i;
/** Each planned shot with where it came from: "Hand close-up (Library), On a swing (Custom + your reference image)". */
export function shotTags(lib: Library | null, poses: string[], custom: CustomPose[], refs: Record<string, string> = {}) {
  const referenced = new Set(Object.values(refs));
  return poses
    .map((p) => {
      const c = custom.find((x) => x.id === p);
      return `${c?.name ?? poseName(lib, p)} (${c ? "Custom" : "Library"}${referenced.has(p) ? " + your reference image" : ""})`;
    })
    .join(", ");
}

/** The categories the brand works in: those of the SKUs found, else every library category. */
export function activeCategories(lib: Library | null, skus: Sku[]): string[] {
  const all = lib?.categories.map((c) => c.id) ?? [];
  const found = all.filter((c) => skus.some((s) => s.category === c));
  const unknown = [...new Set(skus.map((s) => s.category))].filter((c) => !all.includes(c));
  return found.length || unknown.length ? [...found, ...unknown] : all;
}

/** The first category the brand works in, lower case, for example requests ("run the rest of the rings"). */
export const exampleCat = (lib: Library | null, skus: Sku[]) =>
  catName(lib, activeCategories(lib, skus)[0] ?? "products").toLowerCase();

/** The summary card: SKUs per category (library order), with their code range and first names. */
export function skuSummary(skus: Sku[], lib: Library | null) {
  return activeCategories(lib, skus)
    .map((cat) => {
      const list = skus.filter((s) => s.category === cat);
      return {
        cat,
        name: catName(lib, cat),
        count: list.length,
        range: list.length > 1 ? `${list[0].code} – ${list[list.length - 1].code}` : (list[0]?.code ?? ""),
        names: list.slice(0, 3).map((s) => s.name),
        // "Kurtis 2 · Shirts 1", most first; SKUs without a subcategory are left out
        subs: [...Map.groupBy(list, (s) => subName(lib, s) ?? "")]
          .filter(([n]) => n)
          .map(([n, l]) => ({ name: n, count: l.length }))
          .sort((a, b) => b.count - a.count),
      };
    })
    .filter((c) => c.count > 0);
}
/** The summary card's setup rows: each input as chosen, its default when skipped, "—" when open. */
export function setupRows(inputs: Inputs, lib: Library | null = null, skus: Sku[] = []): [string, string][] {
  const val = (step: Step, fallback: string) =>
    inputs[step].items.length
      ? inputs[step].items.map((i) => i.label).join(", ")
      : inputs[step].skipped
        ? fallback
        : "—";
  return [
    ["Brand guidelines", val("guidelines", "ORA defaults")],
    ["Resolution & ratio", val("output", "2K · 1:1")],
    [withNoun("Poses", poseNoun(lib, activeCategories(lib, skus))), val("poses", "ORA defaults, 4 per subcategory")],
    ["Models", needsModel(lib, skus) ? [val("models", "Suggested per category"), sceneNote(lib, skus)].filter(Boolean).join(" · ") : sceneNote(lib, skus)!],
    ["Backgrounds", val("backgrounds", "Category defaults")],
    ["Additional details", val("details", "None")],
  ];
}

/** True when Input can leave its processing step: the job finished (a failed one too, once there are SKUs to go on
 * with: its failure line is in the chat) and no blocking question is open. */
export const processingReady = (d: Detail) =>
  (d.session.processing.status === "succeeded" ||
    d.session.processing.status === "idle" ||
    (d.session.processing.status === "failed" && d.skus.length > 0)) &&
  !d.questions.some((q) => q.blocking && q.answer === null);

/** The setup steps the "Working on it" card ticks through when Processing first opens. */
export function thinkingSteps(skus: Sku[], lib: Library | null, sampleId?: string | null): string[] {
  const cats = skuSummary(skus, lib);
  const sample = skus.find((s) => s.id === sampleId)?.code ?? skus[0]?.code ?? "one SKU";
  return [
    "Reading your brand guidelines and project details",
    `Setting up ${skus.length} SKUs — ${cats.map((c) => `${c.name} ${c.count}`).join(" · ")}`,
    withNoun("Matching poses, models and backgrounds to each SKU", poseNoun(lib, activeCategories(lib, skus))),
    `Lining up a sample to run first — ${sample}`,
  ];
}

/** A link without its scheme, www and trailing slash. */
export const prettyLink = (url: string) =>
  url.replace(/^https?:\/\//, "").replace(/^www\./, "").replace(/\/$/, "");
/** The http(s) URL for what was typed in a link field, or null when it doesn't look like a link. */
export function asLink(raw: string): string | null {
  const t = raw.trim();
  const url = /^https?:\/\//i.test(t) ? t : `https://${t}`;
  return /^https?:\/\/[^\s.]+\.[^\s]+$/i.test(url) ? url : null;
}
/** A byte count as "12 KB" or "1.5 MB". */
export const kb = (n: number) =>
  n > 1024 * 1024 ? `${(n / 1024 / 1024).toFixed(1)} MB` : `${Math.max(1, Math.round(n / 1024))} KB`;
/** Cuts text to fit a contract field. */
export const fit = (s: string, max = 200) => (s.length > max ? `${s.slice(0, max - 1)}…` : s);

/** One pipeline step of a job (evt.catalog.step), folded from its started and done/failed events. */
export type JobStep = { step: string; state: string; started: string; ended: string | null; detail: string | null };
/** One SKU of a run, as its latest run event left it. */
export type JobSku = { sku_id: string; sku_status: string; images_done: number; images_total: number; line: string };

/** A job's events (oldest first) → its steps in the order they started, its SKUs' progress and its images as they
 *  are now (one per image, in the order they first appeared), for the dev Jobs view. */
export function jobNodes(events: SessionEvent[]): { steps: JobStep[]; skus: JobSku[]; images: Image[] } {
  const steps = new Map<string, JobStep>();
  const skus = new Map<string, JobSku>();
  const images = new Map<string, Image>();
  for (const e of events) {
    const d = e.data as Record<string, unknown>;
    if (e.type === "step") {
      const step = String(d.step);
      const detail = (d.detail as string | null) ?? null;
      if (d.state === "started" || !steps.has(step))
        steps.set(step, { step, state: "running", started: e.ts, ended: null, detail });
      if (d.state !== "started")
        Object.assign(steps.get(step)!, { state: d.state === "done" ? "succeeded" : "failed", ended: e.ts, detail });
    } else if (e.type === "run" && typeof d.sku_id === "string") skus.set(d.sku_id, d as unknown as JobSku);
    else if (e.type === "image" && typeof d.id === "string") images.set(d.id, d as unknown as Image);
  }
  return { steps: [...steps.values()], skus: [...skus.values()], images: [...images.values()] };
}

/** The SKU a sample run of `category` renders: the first one not run yet in list order, as catalog_service's runSkus
 * picks it. */
export const sampleSku = (skus: Sku[], category?: string | null): Sku | undefined =>
  skus.find((s) => s.status === "Not run" && (!category || s.category === category));
