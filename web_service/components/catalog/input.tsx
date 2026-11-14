"use client";
// Catalog Agent, Input: one guided chat that walks through raw images and six optional inputs, with each step's
// panel (pickers, uploads, links). Ported from the demo's CatalogAgentV2.jsx; every add goes to the server through
// `ops` (uploads, links, PATCHes of the session's inputs).
import { Fragment, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { toast } from "sonner";
import { message } from "@/lib/api";
import { safeUrl } from "@/lib/format";
import {
  NOUN,
  SINGLE_CHOICE,
  STEPS,
  TAB_LABEL,
  activeCategories,
  asLink,
  attemptsFor,
  catName,
  catOf,
  choices,
  LOOKS,
  SHOT_COUNT,
  kindTip,
  keepCustom,
  shotTags,
  lookImage,
  lookName,
  crWorst,
  fit,
  inputStatus,
  plural,
  poseName,
  poseNoun,
  withNoun,
  prettyLink,
  sectionState,
  setupRows,
  skuSummary,
  suitsWearers,
  wearersOf,
  type Action,
  type Item,
  type Msg,
  type PoseNoun,
  type Step,
  type StepInput,
} from "@/lib/catalog";
import {
  Bubble,
  DescribeBox,
  ICON,
  InfoTip,
  PoseTile,
  usePoseNoun,
  TypingDots,
  UpdateLine,
  UserLine,
  railIcon,
  toneOf,
  toneVarsOf,
  useCatalog,
  useStaggered,
} from "./kit";

/** How an option asks for its input. */
type OptionType = "folder" | "file" | "link" | "picker" | "shots" | "brief" | "output" | "note";
/** One way to fill a step, with the agent's reply once it is added. */
type Option = {
  key: string;
  label: string;
  icon: string;
  type: OptionType;
  accept?: string;
  multiple?: boolean;
  placeholder?: string;
  reply: (i: Item) => string;
};
/** A step's card: title, hint and the ways to fill it. */
type Section = { key: Step; title: string; hint: string; required?: boolean; options: Option[] };

/** Document types guidelines and briefs take. */
const DOCS = ".pdf,.doc,.docx,.ppt,.pptx,.key,.xls,.xlsx,.csv,.txt,.md";
/** "Got it — Aisha, Meera." or "…, mixed across SKUs." */
const pickerReply = (i: Item) =>
  i.randomize ? `Got it — ${i.detail}, mixed across SKUs.` : `Got it — ${i.detail}.`;

/** The Input steps and their options. Only raw images are required. */
const SECTIONS: Section[] = [
  {
    key: "raw",
    title: "Upload your raw images",
    hint: "Product photos — I’ll sort them into SKUs.",
    required: true,
    options: [
      { key: "folder", label: "Upload folder", icon: ICON.folder, type: "folder", reply: (i) => `Got ${plural(i.image_count ?? 0, "image")}.` },
      { key: "csv", label: "Upload CSV", icon: ICON.csv, type: "file", accept: ".csv,.tsv,.txt", reply: () => "Got the CSV." },
      {
        key: "drive",
        label: "Google Drive link",
        icon: ICON.drive,
        type: "link",
        placeholder: "https://drive.google.com/drive/folders/…",
        reply: () => "Got the folder. Make sure it’s shared with “anyone with the link”.",
      },
      {
        key: "website",
        label: "Website link",
        icon: ICON.globe,
        type: "link",
        placeholder: "https://yourbrand.com/collections/new-arrivals",
        reply: () => "Got the page.",
      },
    ],
  },
  {
    key: "guidelines",
    title: "Add brand guidelines",
    hint: "Brand book, lookbook, colours and tone.",
    options: [
      { key: "doc", label: "Upload document", icon: ICON.doc, type: "file", accept: DOCS, multiple: true, reply: () => "Got it." },
      {
        key: "link",
        label: "Share link",
        icon: ICON.link,
        type: "link",
        placeholder: "Notion, Figma, Canva, Google Docs or Drive link",
        reply: () => "Got the link.",
      },
    ],
  },
  {
    key: "output",
    title: "Resolution & aspect ratio",
    hint: "E.g. 2K at 1:1 for marketplaces, 4K at 3:4 for your site.",
    options: [
      { key: "specs", label: "Choose resolution & ratio", icon: ICON.frame, type: "output", reply: (i) => `Got it — ${i.label}.` },
    ],
  },
  {
    key: "poses",
    title: "Poses selection",
    hint: "Choose from the shots library, or give us links, images or a brief.",
    options: [
      { key: "pick", label: "Shots library", icon: ICON.camera, type: "shots", reply: () => "Poses saved." },
      { key: "brief", label: "Links, images or brief", icon: ICON.pen, type: "brief", reply: () => "Got it — I’ll suggest poses from these." },
    ],
  },
  {
    key: "models",
    title: "Choose your models",
    hint: "Pick from your favorites or the full library.",
    options: [{ key: "library", label: "Model library", icon: ICON.grid, type: "picker", reply: pickerReply }],
  },
  {
    key: "backgrounds",
    title: "Choose your backgrounds",
    hint: "Pick from your favorites or the full library.",
    options: [{ key: "library", label: "Background library", icon: ICON.grid, type: "picker", reply: pickerReply }],
  },
  {
    key: "details",
    title: "Additional details",
    hint: "A brief, shot list, specs, or do’s and don’ts.",
    options: [
      { key: "note", label: "Write additional details", icon: ICON.pen, type: "note", reply: () => "Noted." },
      { key: "brief", label: "Upload project brief", icon: ICON.doc, type: "file", accept: DOCS, multiple: true, reply: () => "Got the brief." },
      {
        key: "brief-link",
        label: "Share project brief link",
        icon: ICON.link,
        type: "link",
        placeholder: "Notion, Google Docs or Drive link to the brief",
        reply: () => "Got the link.",
      },
    ],
  },
];
/** A step's card definition. */
export const sectionOf = (key: Step) => SECTIONS.find((s) => s.key === key)!;
/** The agent's reply to an item added through one of a step's options (or the first option). */
export const replyFor = (step: Step, item: Item, noun: PoseNoun) =>
  withNoun((sectionOf(step).options.find((o) => o.key === item.option) ?? sectionOf(step).options[0]).reply(item), noun);

/** "Use a sample instead" on the raw images cards: the bundled demo photos, uploaded as a folder. */
const SAMPLE_RAW = ["jewellery/02", "jewellery/03", "jewellery/04", "jewellery/05", "jewellery/06", "necklace-set/01", "necklace-set/02", "necklace-set/06"];
/** Raw image types the API stores. */
const IMAGE_TYPES = ["image/jpeg", "image/png", "image/webp"];
/** At most this many raw images per upload (the API's limit per call). */
const MAX_RAW = 500;

/** The details an option asks for: a picker, a link field, files or a form. `onDone` is called once it's added. */
function OptionForm({
  step,
  option,
  initial,
  families,
  onDone,
  onCancel,
}: {
  step: Step;
  option: Option;
  initial: Item | null;
  /** raw images: the "What are we shooting?" families, empty = let the agent decide */
  families: string[];
  onDone: (items: Item[], replace: boolean) => void;
  onCancel: () => void;
}) {
  const { ops } = useCatalog();
  const [link, setLink] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState("");
  const fileRef = useRef<HTMLInputElement>(null);

  /** Runs one server call with a busy note, reporting a failure in the form. */
  const run = async (note: string, fn: () => Promise<Item[]>) => {
    setBusy(note);
    setError("");
    try {
      onDone(await fn(), false);
    } catch (e) {
      setError(message(e));
    } finally {
      setBusy("");
    }
  };

  /** Sends the link: raw Drive/website links start processing, the others are guideline or brief links. */
  const addLink = () => {
    const url = asLink(link);
    if (!url) return setError("That doesn’t look like a link.");
    if (step === "raw")
      return run("Adding the link…", async () => [(await ops.addRaw({ kind: option.key as "drive" | "website", url, families })).item]);
    return run("Adding the link…", async () => [
      (await ops.addGuideline(step === "details" ? "details" : "guidelines", { url })).item,
    ]);
  };

  /** Uploads the chosen files: raw images (a folder, sub-folders included), a CSV, or documents one by one. */
  const onFiles = (list: FileList | null) => {
    const files = [...(list ?? [])];
    if (fileRef.current) fileRef.current.value = "";
    if (!files.length) return;
    if (option.type === "folder") {
      const images = files.filter((f) => IMAGE_TYPES.includes(f.type));
      if (!images.length) return setError("No JPEG, PNG or WEBP images in that folder.");
      if (images.length > MAX_RAW) toast(`Uploading the first ${MAX_RAW} of ${images.length} images.`);
      const folder = files[0].webkitRelativePath?.split("/")[0] || "folder";
      const pick = images.slice(0, MAX_RAW); // ponytail: one call of 500; add more later from the Input list
      return run(`Uploading ${plural(pick.length, "image")}…`, async () => [
        (await ops.addRaw({ files: pick, kind: "folder", label: `Folder · ${folder}`, families })).item,
      ]);
    }
    if (step === "raw")
      return run("Reading the CSV…", async () => [
        (await ops.addRaw({ files: [files[0]], kind: "csv", label: files[0].name, families })).item,
      ]);
    return run(`Uploading ${plural(files.length, "file")}…`, async () => {
      const items: Item[] = [];
      for (const file of files)
        items.push((await ops.addGuideline(step === "details" ? "details" : "guidelines", { file })).item);
      return items;
    });
  };

  /** Uploads the sample photos as a folder, whichever raw option is open. */
  const addSample = () =>
    run(`Uploading ${plural(SAMPLE_RAW.length, "image")}…`, async () => {
      const files = await Promise.all(
        SAMPLE_RAW.map(async (path) => {
          const r = await fetch(`/catalog/home-shots/${path}.jpg`);
          if (!r.ok) throw new Error("The sample photos couldn’t be loaded.");
          return new File([await r.blob()], `${path.replace("/", "-")}.jpg`, { type: "image/jpeg" });
        }),
      );
      return [(await ops.addRaw({ files, kind: "folder", label: "Folder · sample_drop", families })).item];
    });

  /** A picked choice (models, poses, output, note): saved by the screen, replacing a one-choice input. */
  const pick = (item: Omit<Item, "id">) => onDone([{ ...item, id: "" }], !!initial && SINGLE_CHOICE.includes(step));

  return (
    <div className="mt-3 rounded-xl border border-accent/40 bg-surface-raised/60 p-3">
      <div className="mb-2 flex items-center justify-between">
        <span className="text-xs font-semibold text-white">{option.label}</span>
        <button type="button" onClick={onCancel} className="text-xs text-gray-400 hover:text-white">
          Cancel
        </button>
      </div>

      {(option.type === "folder" || option.type === "file") && (
        <>
          <input
            ref={fileRef}
            type="file"
            className="hidden"
            accept={option.type === "folder" ? undefined : option.accept}
            multiple={option.multiple || option.type === "folder"}
            {...(option.type === "folder" ? { webkitdirectory: "", directory: "" } : {})}
            onChange={(e) => onFiles(e.target.files)}
          />
          <button
            type="button"
            disabled={!!busy}
            onClick={() => fileRef.current?.click()}
            className="flex w-full flex-col items-center gap-1 rounded-lg border border-dashed border-surface-border px-4 py-5 text-center hover:border-accent/60 disabled:opacity-60"
          >
            <span className="text-sm text-gray-200">
              {busy || (option.type === "folder" ? "Choose a folder" : option.multiple ? "Choose files" : "Choose a file")}
            </span>
            <span className="text-[11px] text-gray-500">
              {option.type === "folder" ? "All images inside are picked up, sub-folders too" : option.accept}
            </span>
          </button>
          {error && <p className="mt-2 text-xs text-rose-400">{error}</p>}
        </>
      )}

      {option.type === "link" && (
        <>
          <div className="flex gap-2">
            <input
              autoFocus
              value={link}
              onChange={(e) => {
                setLink(e.target.value);
                setError("");
              }}
              onKeyDown={(e) => {
                if (e.key === "Enter") addLink();
              }}
              placeholder={option.placeholder}
              aria-label={option.label}
              className="min-w-0 flex-1 rounded-lg border border-surface-border bg-surface px-3 py-2 text-sm text-gray-100 placeholder:text-gray-500 focus:border-accent focus:outline-none"
            />
            <button
              type="button"
              onClick={addLink}
              disabled={!link.trim() || !!busy}
              className="rounded-lg bg-accent px-3 py-2 text-xs font-semibold text-white hover:bg-accent-hover disabled:opacity-40"
            >
              {busy ? "Adding…" : "Add"}
            </button>
          </div>
          {error && <p className="mt-2 text-xs text-rose-400">{error}</p>}
        </>
      )}

      {step === "raw" && (
        <button type="button" onClick={() => void addSample()} disabled={!!busy} className="mt-2 text-xs text-accent-hover hover:underline disabled:opacity-40">
          Use a sample instead
        </button>
      )}

      {option.type === "picker" && (
        <PickerForm kind={step === "models" ? "models" : "backgrounds"} initial={initial} onAdd={pick} />
      )}
      {option.type === "shots" && <ShotsForm initial={initial} onAdd={pick} />}
      {option.type === "brief" && <BriefForm onAdd={pick} />}
      {option.type === "output" && <OutputForm initial={initial} onAdd={pick} />}
      {option.type === "note" && <NoteForm onAdd={pick} />}
    </div>
  );
}

/** "What are we shooting?" on the raw images card: the library's families (several may be picked), or none to let the
 * agent decide; sent with the raw upload to limit the products the images are sorted into. */
function FamilyChoice({ value, onChange }: { value: string[]; onChange: (v: string[]) => void }) {
  const { lib } = useCatalog();
  const chip = (on: boolean) =>
    `rounded-full border px-3 py-1 text-xs transition ${on ? "border-accent bg-accent/15 text-white" : "border-surface-border text-gray-300 hover:border-gray-500 hover:text-white"}`;
  if (!lib?.families.length) return null;
  return (
    <div className="mb-3">
      <div className="mb-1.5 text-xs text-gray-400">What are we shooting?</div>
      <div className="flex flex-wrap gap-1.5">
        {lib.families.map((f) => {
          const on = value.includes(f.id);
          return (
            <button
              key={f.id}
              type="button"
              aria-pressed={on}
              onClick={() => onChange(on ? value.filter((x) => x !== f.id) : [...value, f.id])}
              className={chip(on)}
            >
              {f.name}
            </button>
          );
        })}
        <button type="button" aria-pressed={!value.length} onClick={() => onChange([])} className={chip(!value.length)}>
          Let the agent decide
        </button>
      </div>
    </div>
  );
}

/** The tabs of the model and background pickers; Ethnicity (looks) is the models picker's only. */
const PICKER_TABS = [
  ["favorites", "Favorites"],
  ["looks", "Ethnicity"],
  ["all", "Full library"],
] as const;

/**
 * Pick models or backgrounds from the library (and the brand's own uploads), with tabs for favorites and the full
 * library. Picks carry across tabs; a heart marks a favorite. Pick several and randomize so each SKU gets one. The
 * brand's description, below both tabs, goes with every SKU: alone it is the choice, with picks it adds to them.
 */
function PickerForm({
  kind,
  initial,
  onAdd,
}: {
  kind: "models" | "backgrounds";
  initial: Item | null;
  onAdd: (i: Omit<Item, "id">) => void;
}) {
  const { lib, detail, ops } = useCatalog();
  // Backgrounds made for other families (a living room in a jewellery-only session) are hidden, and models of a wear no
  // SKU is (the men's in a women's session); no SKUs yet: all.
  const fams = new Set(detail.skus.map((s) => catOf(lib, s.category)?.family));
  const wearers = wearersOf(detail.skus, lib);
  const all = choices(lib, detail.session.uploads, kind).filter(
    (x) => (!fams.size || !x.families || x.families.some((f) => fams.has(f))) && suitsWearers(x, wearers),
  );
  const favIds = lib?.favorites[kind] ?? [];
  const favs = all.filter((x) => favIds.includes(x.id));
  const [picked, setPicked] = useState<string[]>(initial?.ids ?? []);
  const [randomize, setRandomize] = useState(!!initial?.randomize);
  const [text, setText] = useState(initial?.text ?? "");
  const said = text.trim();
  // Editing a choice that isn't all favorites opens on the full library, so every pick shows; a look on Ethnicity.
  const [tab, setTab] = useState<(typeof PICKER_TABS)[number][0]>(() =>
    kind === "models" && !picked.length && LOOKS.some((l) => text.startsWith(l.text))
      ? "looks"
      : favs.length && picked.every((id) => favIds.includes(id))
        ? "favorites"
        : "all",
  );
  const [uploading, setUploading] = useState(false);
  const uploadRef = useRef<HTMLInputElement>(null);
  const items = tab === "favorites" ? favs : all;
  const noun = kind === "models" ? "model" : "background";
  /** Picks or unpicks a tile; a photo replaces a picked look (the photo is the person), keeping what the brand added. */
  const toggle = (id: string) => {
    const look = LOOKS.find((l) => text.startsWith(l.text));
    if (look) setText(text.slice(look.text.length).replace(/^[\s,;.]+/, ""));
    setPicked((p) => (p.includes(id) ? p.filter((x) => x !== id) : [...p, id]));
  };
  /** Uploads the brand's own photo and picks it. */
  const upload = async (file?: File) => {
    if (!file) return;
    setUploading(true);
    try {
      const u = await ops.upload(kind === "models" ? "model" : "background", file);
      setPicked((p) => [...p, u.id]);
      setTab("all");
    } catch (e) {
      toast.error(message(e));
    } finally {
      setUploading(false);
    }
  };
  const look = kind === "models" ? LOOKS.find((l) => said.startsWith(l.text)) : undefined; // a picked Ethnicity tile
  const add = () =>
    onAdd({
      option: "library",
      label: picked.length
        ? `${plural(picked.length, noun)}${randomize ? " · randomized" : ""}${said ? " + described" : ""}`
        : `${look ? lookName(look.text) : "Described"} ${noun}`,
      detail: fit([...all.filter((x) => picked.includes(x.id)).map((x) => x.name), ...(said ? [`“${said}”`] : [])].join(", ")),
      ids: picked,
      randomize,
      ...(said && { text: said }),
    });
  return (
    <>
      <div className="mb-2 flex items-center gap-1.5">
        {PICKER_TABS.filter(([key]) => key !== "looks" || kind === "models").map(([key, label]) => (
          <button
            key={key}
            type="button"
            onClick={() => setTab(key)}
            aria-pressed={tab === key}
            className={`rounded-lg border px-2.5 py-1 text-xs transition ${tab === key ? "border-accent bg-accent/15 text-white" : "border-surface-border text-gray-300 hover:border-gray-500 hover:text-white"}`}
          >
            {label} <span className="text-gray-500">{key === "looks" ? LOOKS.length : (key === "favorites" ? favs : all).length}</span>
          </button>
        ))}
        <span className="ml-auto text-[11px] text-gray-500">{picked.length} selected</span>
      </div>
      {tab === "looks" ? (
        <div className="grid grid-cols-3 gap-2 sm:grid-cols-5">
          {LOOKS.map((l) => {
            // a look is a described-only model: its text becomes the description (the brand may add to it)
            const on = !picked.length && text.startsWith(l.text);
            return (
              <button
                key={l.id}
                type="button"
                onClick={() => {
                  setPicked([]);
                  setRandomize(false);
                  setText(on ? "" : l.text);
                }}
                aria-pressed={on}
                title={l.text}
                className={`relative block w-full overflow-hidden rounded-lg border-2 bg-surface-raised text-left ${on ? "border-accent" : "border-transparent hover:border-surface-border"}`}
              >
                <img
                  src={lookImage(l.id, detail.skus)}
                  alt=""
                  loading="lazy"
                  onError={(e) => (e.currentTarget.style.visibility = "hidden")}
                  className="aspect-square w-full object-cover"
                />
                <span className="absolute inset-x-0 bottom-0 truncate bg-black/60 px-1 py-0.5 text-center text-[10px] text-white">{lookName(l.text)}</span>
              </button>
            );
          })}
        </div>
      ) : (
      <div className="grid grid-cols-3 gap-2 sm:grid-cols-5">
        {items.map((x) => {
          const on = picked.includes(x.id);
          const fav = favIds.includes(x.id);
          return (
            <div key={x.id} className="group relative">
              <button
                type="button"
                onClick={() => toggle(x.id)}
                aria-pressed={on}
                className={`relative block w-full overflow-hidden rounded-lg border-2 text-left ${on ? "border-accent" : "border-transparent hover:border-surface-border"}`}
              >
                {x.image ? (
                  <img src={x.image} alt={x.name} className="aspect-square w-full object-cover" />
                ) : (
                  <span className="block aspect-square w-full" style={{ background: x.swatch ?? "#2f3446" }} />
                )}
                {x.hover && (
                  <img src={x.hover} alt="" loading="lazy" className="absolute inset-0 h-full w-full object-cover object-top opacity-0 transition group-hover:opacity-100" />
                )}
                <span className="absolute inset-x-0 bottom-0 truncate bg-black/60 px-1 py-0.5 text-center text-[10px] text-white">
                  {x.name}
                </span>
                {on && (
                  <span className="absolute top-1 right-1 flex h-4 w-4 items-center justify-center rounded-full bg-accent text-[10px] text-white">
                    ✓
                  </span>
                )}
              </button>
              {lib?.[kind].some((m) => m.id === x.id) && (
                <button
                  type="button"
                  onClick={() => ops.favorite(kind, x.id, !fav)}
                  aria-label={fav ? `Remove ${x.name} from favorites` : `Add ${x.name} to favorites`}
                  aria-pressed={fav}
                  className={`absolute top-1 left-1 rounded-full bg-black/50 p-0.5 transition ${fav ? "text-pink-300" : "text-white/70 opacity-0 group-hover:opacity-100 focus:opacity-100"}`}
                >
                  <svg className="h-3.5 w-3.5" viewBox="0 0 24 24" fill={fav ? "currentColor" : "none"} stroke="currentColor" strokeWidth={1.8} aria-hidden>
                    <path strokeLinecap="round" strokeLinejoin="round" d={ICON.heart} />
                  </svg>
                </button>
              )}
            </div>
          );
        })}
        <input
          ref={uploadRef}
          type="file"
          accept={IMAGE_TYPES.join(",")}
          className="hidden"
          onChange={(e) => {
            void upload(e.target.files?.[0]);
            e.target.value = "";
          }}
        />
        <button
          type="button"
          disabled={uploading}
          onClick={() => uploadRef.current?.click()}
          className="flex aspect-square flex-col items-center justify-center gap-1 rounded-lg border-2 border-dashed border-surface-border text-[10px] text-gray-400 hover:border-accent/60 hover:text-white disabled:opacity-50"
        >
          {railIcon(ICON.upload, "h-4 w-4")}
          {uploading ? "Uploading…" : `Upload your own`}
        </button>
      </div>
      )}
      <DescribeBox kind={noun} value={text} onChange={setText} />
      <div className="mt-3 flex flex-wrap items-center justify-between gap-2">
        <label className="flex cursor-pointer items-center gap-2 text-xs text-gray-300">
          <input
            type="checkbox"
            checked={randomize}
            onChange={(e) => {
              setRandomize(e.target.checked);
              if (e.target.checked && picked.length < 2) setPicked(items.map((x) => x.id));
            }}
            className="h-4 w-4 accent-accent"
          />
          Pick multiple &amp; randomize
          <span className="text-gray-500">— each SKU gets one at random</span>
        </label>
        <button
          type="button"
          disabled={!(picked.length || said) || (randomize && picked.length < 2)}
          onClick={add}
          className="rounded-lg bg-accent px-3 py-1.5 text-xs font-semibold text-white hover:bg-accent-hover disabled:opacity-40"
        >
          {randomize
            ? `Randomize ${picked.length} ${noun}s`
            : picked.length
              ? `Use ${picked.length} ${noun}${picked.length === 1 ? "" : "s"}`
              : `Use described ${noun}`}
        </button>
      </div>
    </>
  );
}

/** Where the shots library's tabs come from: what was used last time, the brand's favorites, or every pose. */
const POSE_TABS = [
  ["last", "Last used"],
  ["favorites", "Favorites"],
  ["all", "All poses"],
] as const;
type PoseTab = (typeof POSE_TABS)[number][0];

/**
 * The shots library, one category at a time: Last used, Favorites or All poses, pick by picture, then Next to the next
 * category (or back). Starts from last time's poses, else ORA's defaults.
 */
function ShotsForm({ initial, onAdd }: { initial: Item | null; onAdd: (i: Omit<Item, "id">) => void }) {
  const { lib, detail, memory, ops } = useCatalog();
  const cats = activeCategories(lib, detail.skus);
  const lastItem = memory.find((m) => m.step === "poses")?.items.find((i) => i.poses);
  const lastPlan = lastItem?.poses;
  /** The poses a tab offers for a category. */
  const posesIn = (tab: PoseTab, cat: string): string[] => {
    const all = (lib?.poses ?? []).filter((p) => p.category === cat).map((p) => p.id);
    if (tab === "all") return all;
    if (tab === "favorites") return all.filter((p) => lib?.favorites.poses.includes(p));
    return lastPlan?.[cat] ?? lib?.categories.find((c) => c.id === cat)?.default_poses ?? [];
  };
  const [at, setAt] = useState(0);
  const [tab, setTab] = useState<PoseTab>("last");
  const noun = poseNoun(lib, cats[at] ?? cats);
  const [plan, setPlan] = useState<Record<string, string[]>>(() =>
    Object.fromEntries(cats.map((c) => [c, initial?.poses?.[c] ?? posesIn("last", c)])),
  );
  const [said, setSaid] = useState<Record<string, string>>(() => Object.fromEntries(cats.map((c) => [c, ""])));
  const [busy, setBusy] = useState(false);
  if (!cats.length) return <p className="text-xs text-gray-400">{withNoun("The shots library isn’t available yet.", noun)}</p>;
  const cat = cats[at];
  const toggle = (p: string) =>
    setPlan((x) => ({ ...x, [cat]: x[cat].includes(p) ? x[cat].filter((y) => y !== p) : [...x[cat], p] }));
  const go = (i: number) => {
    setAt(i);
    setTab("last");
  };
  const last = at === cats.length - 1;
  const total = cats.reduce((n, c) => n + plan[c].length, 0);
  /** Saves the plan. A category with described shots asks the agent for exactly those (library poses or custom
   * shots): they are its plan, after the picked tiles only when the description sets no number. */
  const save = async () => {
    const asks = cats.filter((c) => said[c].trim()).map((c) => ({ category: c, text: said[c].trim(), upload_ids: [], exact: true }));
    setBusy(true);
    try {
      const s = asks.length ? await ops.suggestPoses(asks) : null;
      const final = Object.fromEntries(
        cats.map((c) => {
          const described = s?.poses[c];
          if (!described) return [c, plan[c]];
          return [c, SHOT_COUNT.test(said[c]) ? described : [...new Set([...plan[c], ...described])]];
        }),
      );
      // custom shots picked here (the session's, last time's, this item's) keep their definitions; the described
      // list is the plan itself, so it is no shots brief
      const { poses, custom } = keepCustom(final, [
        ...detail.session.inputs.poses.items.flatMap((i) => i.custom_poses ?? []),
        ...(lastItem?.custom_poses ?? []),
        ...(initial?.custom_poses ?? []),
        ...(s?.custom_poses ?? []),
      ]);
      onAdd({
        option: "pick",
        label: withNoun(`Poses · ${plural(cats.length, "category").replace("categorys", "categories")}`, poseNoun(lib, cats)),
        detail: fit(cats.map((c) => `${catName(lib, c)}: ${shotTags(lib, poses[c], custom)}`).join(" · ")),
        poses,
        ...(custom.length && { custom_poses: custom }),
      });
    } catch (e) {
      toast.error(message(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <>
      <div className="mb-2 flex flex-wrap items-center gap-1.5">
        {POSE_TABS.map(([key, label]) => (
          <button
            key={key}
            type="button"
            onClick={() => setTab(key)}
            aria-pressed={tab === key}
            className={`rounded-lg border px-2.5 py-1 text-xs transition ${tab === key ? "border-accent bg-accent/15 text-white" : "border-surface-border text-gray-300 hover:border-gray-500 hover:text-white"}`}
          >
            {withNoun(label, noun)} <span className="text-gray-500">{posesIn(key, cat).length}</span>
          </button>
        ))}
        <span className="ml-auto text-[11px] text-gray-500">
          <span className="font-medium text-gray-200">{catName(lib, cat)}</span> · {at + 1} of {cats.length} ·{" "}
          {plan[cat].length} selected
        </span>
      </div>
      <div className="grid grid-cols-3 gap-2 sm:grid-cols-5">
        {posesIn(tab, cat).map((p) => (
          <PoseTile key={p} lib={lib} pose={p} on={plan[cat].includes(p)} onToggle={() => toggle(p)} />
        ))}
      </div>
      <DescribeBox
        kind="shots"
        name={cats.length > 1 ? catName(lib, cat).toLowerCase() : undefined}
        value={said[cat]}
        onChange={(v) => setSaid((x) => ({ ...x, [cat]: v }))}
      />
      <div className="mt-3 flex items-center justify-between">
        <button
          type="button"
          onClick={() => go(at - 1)}
          disabled={at === 0}
          className="text-xs text-gray-400 hover:text-white disabled:invisible"
        >
          ← {at > 0 ? catName(lib, cats[at - 1]) : ""}
        </button>
        <button
          type="button"
          disabled={(!plan[cat].length && !said[cat].trim()) || busy}
          onClick={() => (last ? void save() : go(at + 1))}
          className="rounded-lg bg-accent px-3 py-1.5 text-xs font-semibold text-white hover:bg-accent-hover disabled:opacity-40"
        >
          {busy
            ? "Reading your shots…"
            : last
              ? cats.some((c) => said[c].trim())
                ? "Use described shots"
                : withNoun(`Save poses · ${total}`, poseNoun(lib, cats))
              : `Next: ${catName(lib, cats[at + 1])} →`}
        </button>
      </div>
    </>
  );
}

/** How to write a brief that gets useful poses back, with a worked example. */
/** How each input of the "Links, images or brief" panel is used. */
const BRIEF_USES = [
  ["Brief", "We pick matching shots from the Shots library; a shot it doesn't have becomes a custom shot. Your words also go to every image."],
  ["Links", "We read the page to understand the shots you want. Images on linked pages aren't copied into your images."],
  ["Images", "Each image is matched to one shot and guides its angle, crop and pose — not its product, model or background."],
  ["Describe your shots", "Sets the exact shots and their number."],
];
const BRIEF_TIPS = [
  "The angles you must have — close-up, on the model, packshot",
  "How it is worn, held or placed — layered, on the hand, in a room",
  "The mood or setting — studio, outdoor, festive, warm light",
  "Anything to avoid — e.g. no full-body shots",
];
/** The "Use a sample instead" brief of a category: the example's shape, for that category. */
const sampleBrief = (name: string) =>
  `${name}: two close-ups that show the detail, one on the model, one packshot on white, one outdoor lifestyle shot in warm light. No full-body shots.`;
/** Links in a brief box, shown as chips. */
const LINK_RE = /\b(?:https?:\/\/|www\.)\S+|\b[a-z0-9-]+\.(?:com|in|co|net|org|io|app|so)(?:\/\S*)?/gi;

/** A brief, links or reference photos per category; the agent (LLM) suggests the poses from all of it. */
function BriefForm({ onAdd }: { onAdd: (i: Omit<Item, "id">) => void }) {
  const { lib, detail, ops } = useCatalog();
  const cats = activeCategories(lib, detail.skus);
  const [text, setText] = useState<Record<string, string>>(() => Object.fromEntries(cats.map((c) => [c, ""])));
  const [files, setFiles] = useState<Record<string, { id: string; name: string }[]>>(() =>
    Object.fromEntries(cats.map((c) => [c, []])),
  );
  const [said, setSaid] = useState<Record<string, string>>(() => Object.fromEntries(cats.map((c) => [c, ""])));
  const [busy, setBusy] = useState(false);
  const links = (c: string) => text[c].match(LINK_RE) ?? [];
  const filled = cats.filter((c) => text[c].trim() || files[c].length || said[c].trim());
  /** Uploads reference photos for one category. */
  const addFiles = async (c: string, list: FileList | null) => {
    for (const f of [...(list ?? [])].filter((x) => IMAGE_TYPES.includes(x.type))) {
      try {
        const u = await ops.upload("pose_ref", f, c);
        setFiles((x) => ({ ...x, [c]: [...x[c], { id: u.id, name: u.name }] }));
      } catch (e) {
        toast.error(message(e));
      }
    }
  };
  /** Asks the agent for poses, then adds them as the poses input. */
  const send = async () => {
    setBusy(true);
    try {
      // described shots are the exact plan; else the agent picks from the brief, links and images
      const briefs = filled.map((c) => ({
        category: c,
        text: said[c].trim() || text[c].trim(),
        upload_ids: files[c].map((f) => f.id),
        exact: !!said[c].trim(),
      }));
      const s = await ops.suggestPoses(briefs);
      const plan = Object.fromEntries(
        cats.map((c) => [c, s.poses[c] ?? lib?.categories.find((x) => x.id === c)?.default_poses ?? []]),
      );
      const nFiles = cats.reduce((n, c) => n + files[c].length, 0);
      const nLinks = cats.reduce((n, c) => n + links(c).length, 0);
      const parts = [
        nFiles && plural(nFiles, "image"),
        nLinks && plural(nLinks, "link"),
        cats.some((c) => text[c].replace(LINK_RE, "").trim()) && "brief",
        cats.some((c) => said[c].trim()) && "described shots",
      ].filter(Boolean);
      onAdd({
        option: "brief",
        label: fit(withNoun(`Poses from ${parts.join(" + ") || "brief"}`, poseNoun(lib, cats))),
        detail: fit(cats.map((c) => `${catName(lib, c)}: ${shotTags(lib, plan[c], s.custom_poses, s.refs)}`).join(" · ")),
        poses: plan,
        // the shots brief every image of the product gets (general look): the brief, never the described shot list
        briefs: Object.fromEntries(filled.filter((c) => text[c].trim()).map((c) => [c, text[c].trim()])),
        upload_ids: cats.flatMap((c) => files[c].map((f) => f.id)),
        ...(s.custom_poses.length && { custom_poses: s.custom_poses }),
        ...(Object.keys(s.refs).length && { pose_refs: s.refs }),
      });
    } catch (e) {
      toast.error(message(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <>
      <details className="group mb-3 rounded-lg border border-surface-border bg-surface px-3 py-2">
        <summary className="flex cursor-pointer list-none items-center justify-between text-xs text-gray-300 hover:text-white">
          How to write a good brief
          <span className="text-gray-500 transition group-open:rotate-180">⌄</span>
        </summary>
        <ul className="mt-2 list-disc space-y-0.5 pl-4 text-[11px] text-gray-400">
          {BRIEF_TIPS.map((t) => (
            <li key={t}>{t}</li>
          ))}
        </ul>

        <p className="mt-2 text-[11px] text-gray-500">
          <span className="text-gray-300">Example — </span>
          {sampleBrief(catName(lib, cats[0] ?? "Products"))}
        </p>
      </details>
      <ul className="mb-3 space-y-0.5 text-[11px] text-gray-400" aria-label="How we use what you add">
        {BRIEF_USES.map(([what, how]) => (
          <li key={what}>
            <span className="text-gray-300">{what}:</span> {how}
          </li>
        ))}
      </ul>
      <div className="space-y-3">
        {cats.map((c) => (
          <BriefBox
            key={c}
            name={catName(lib, c)}
            value={text[c]}
            onChange={(v) => setText((t) => ({ ...t, [c]: v }))}
            files={files[c]}
            links={links(c)}
            onFiles={(l) => void addFiles(c, l)}
            onRemoveFile={(i) => setFiles((f) => ({ ...f, [c]: f[c].filter((_, j) => j !== i) }))}
            said={said[c]}
            onSaid={(v) => setSaid((x) => ({ ...x, [c]: v }))}
          />
        ))}
      </div>
      <div className="mt-3 flex items-center justify-between">
        <button
          type="button"
          onClick={() => setText(Object.fromEntries(cats.map((c) => [c, sampleBrief(catName(lib, c))])))}
          className="text-xs text-accent-hover hover:underline"
        >
          Use a sample instead
        </button>
        <button
          type="button"
          onClick={() => void send()}
          disabled={!filled.length || busy}
          className="rounded-lg bg-accent px-3 py-1.5 text-xs font-semibold text-white hover:bg-accent-hover disabled:opacity-40"
        >
          {busy ? "Reading your brief…" : "Use these"}
        </button>
      </div>
    </>
  );
}

/** One category: a box for the brief or links, and a button to add reference photos. */
function BriefBox({
  name,
  value,
  onChange,
  files,
  links,
  onFiles,
  onRemoveFile,
  said,
  onSaid,
}: {
  name: string;
  value: string;
  onChange: (v: string) => void;
  files: { id: string; name: string }[];
  links: string[];
  onFiles: (l: FileList | null) => void;
  onRemoveFile: (i: number) => void;
  said: string;
  onSaid: (v: string) => void;
}) {
  const imagesRef = useRef<HTMLInputElement>(null);
  return (
    <div className="rounded-lg border border-surface-border bg-surface focus-within:border-accent/60">
      <div className="px-3 pt-2 text-xs font-medium text-gray-200">{name}</div>
      <textarea
        rows={3}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        aria-label={`Brief for ${name}`}
        placeholder={`Write a brief or paste links — e.g. close-ups, one lifestyle shot, pinterest.com/…`}
        className="w-full resize-y bg-transparent px-3 py-1.5 text-sm text-gray-100 placeholder:text-gray-500 focus:outline-none"
      />
      {(files.length > 0 || links.length > 0) && (
        <div className="flex flex-wrap gap-1.5 px-3 pb-2">
          {links.map((l) => (
            <span key={l} className="flex items-center gap-1 rounded-md bg-white/[0.06] px-1.5 py-0.5 text-[11px] text-accent-hover">
              {railIcon(ICON.link, "h-3 w-3")}
              {prettyLink(l)}
            </span>
          ))}
          {files.map((f, i) => (
            <span key={f.id} className="flex items-center gap-1 rounded-md bg-white/[0.06] px-1.5 py-0.5 text-[11px] text-gray-300">
              🖼 {f.name}
              <button type="button" onClick={() => onRemoveFile(i)} aria-label={`Remove ${f.name}`} className="text-gray-500 hover:text-white">
                ×
              </button>
            </span>
          ))}
        </div>
      )}
      <div className="flex items-center gap-1.5 border-t border-surface-border px-3 py-1.5">
        <input
          ref={imagesRef}
          type="file"
          accept={IMAGE_TYPES.join(",")}
          multiple
          className="hidden"
          onChange={(e) => {
            onFiles(e.target.files);
            e.target.value = "";
          }}
        />
        <button
          type="button"
          onClick={() => imagesRef.current?.click()}
          className="flex items-center gap-1.5 rounded-md border border-accent/50 bg-accent/10 px-2 py-1 text-[11px] text-accent-hover hover:border-accent hover:bg-accent/20"
        >
          {railIcon(ICON.upload, "h-3 w-3")}Upload images
        </button>
      </div>
      <div className="border-t border-surface-border px-3 pb-2">
        <DescribeBox kind="shots" value={said} onChange={onSaid} />
      </div>
    </div>
  );
}

/** Resolution and aspect ratio for the final images, from the library (default 2K · 1:1). */
function OutputForm({ initial, onAdd }: { initial: Item | null; onAdd: (i: Omit<Item, "id">) => void }) {
  const { lib, detail } = useCatalog();
  // the most renders one of this session's images can take (its product's max attempts); the library's before SKUs
  const attempts = detail.skus.length ? Math.max(...detail.skus.map((s) => attemptsFor(lib, s.category))) : attemptsFor(lib);
  const [resolution, setResolution] = useState(initial?.resolution ?? lib?.defaults.resolution ?? "2K");
  const [ratio, setRatio] = useState(initial?.ratio ?? lib?.defaults.ratio ?? "1:1");
  const chip = (on: boolean) =>
    `rounded-lg border px-3 py-1.5 text-xs transition ${on ? "border-accent bg-accent/15 text-white" : "border-surface-border text-gray-300 hover:border-gray-500 hover:text-white"}`;
  const res = lib?.resolutions.find((r) => r.id === resolution);
  return (
    <>
      <div className="text-xs font-medium text-gray-200">Resolution</div>
      <div className="mt-1.5 flex flex-wrap items-center gap-1.5">
        {(lib?.resolutions ?? []).map((x) => (
          <button key={x.id} type="button" onClick={() => setResolution(x.id)} className={chip(resolution === x.id)}>
            {x.label || x.id}
          </button>
        ))}
        {res && <span className="ml-1 text-[11px] text-gray-500">{crWorst({ credits: res.credits_per_image, worst: res.credits_per_image * attempts }, `${res.credits_per_image} cr per image`)}</span>}
      </div>
      <div className="mt-3 text-xs font-medium text-gray-200">Aspect ratio</div>
      <div className="mt-1.5 grid grid-cols-3 gap-1.5 sm:grid-cols-6">
        {(lib?.ratios ?? []).map((x) => (
          <button
            key={x.id}
            type="button"
            onClick={() => setRatio(x.id)}
            className={`${chip(ratio === x.id)} flex flex-col items-center gap-1.5 py-2`}
          >
            <span className="flex h-8 items-center">
              <span
                className="block rounded-sm border border-current opacity-70"
                style={{ width: `${(x.w / Math.max(x.w, x.h)) * 28}px`, height: `${(x.h / Math.max(x.w, x.h)) * 28}px` }}
              />
            </span>
            <span>{x.label || x.id}</span>
            <span className="text-[10px] text-gray-500">{x.use}</span>
          </button>
        ))}
      </div>
      <div className="mt-3 flex justify-end">
        <button
          type="button"
          onClick={() => onAdd({ option: "specs", label: `${resolution} · ${ratio}`, detail: "Resolution & ratio", resolution, ratio })}
          className="rounded-lg bg-accent px-3 py-1.5 text-xs font-semibold text-white hover:bg-accent-hover"
        >
          Use {resolution} · {ratio}
        </button>
      </div>
    </>
  );
}

/** An additional-details item from typed text. */
export const noteItem = (t: string): Omit<Item, "id"> => ({
  option: "note",
  label: t.length > 60 ? `${t.slice(0, 58)}…` : t,
  detail: "Additional details",
  text: t,
});

/** Additional details: free text for the agent. */
function NoteForm({ onAdd }: { onAdd: (i: Omit<Item, "id">) => void }) {
  const [text, setText] = useState("");
  return (
    <>
      <textarea
        autoFocus
        rows={4}
        value={text}
        onChange={(e) => setText(e.target.value)}
        aria-label="Additional details"
        placeholder="e.g. “Keep skin tones warm”, “No hands with nail paint”, “Show the back of every piece in one shot”, “Deliver by Friday”"
        className="w-full resize-none rounded-lg border border-surface-border bg-surface px-3 py-2 text-sm text-gray-100 placeholder:text-gray-500 focus:border-accent focus:outline-none"
      />
      <div className="mt-2 flex justify-end">
        <button
          type="button"
          onClick={() => text.trim() && onAdd(noteItem(text.trim()))}
          disabled={!text.trim()}
          className="rounded-lg bg-accent px-3 py-1.5 text-xs font-semibold text-white hover:bg-accent-hover disabled:opacity-40"
        >
          Add details
        </button>
      </div>
    </>
  );
}

/** The left list's icon per input status. */
const STATUS_ICON = {
  added: { d: ICON.check, cls: "text-emerald-400", title: "Added" },
  reused: { d: ICON.refresh, cls: "text-sky-400", title: "Reused from last time" },
  skipped: { d: ICON.skip, cls: "text-gray-500", title: "Skipped" },
} as const;

/** The inputs nested under Input in the left rail: an icon for added / reused / skipped, the current step marked
 * and a star on the required one. */
export function SectionList({
  inputs,
  selected,
  current,
  onSelect,
}: {
  inputs: Record<Step, StepInput>;
  selected: Step | null;
  current: string | null;
  onSelect: (s: Step) => void;
}) {
  const noun = usePoseNoun();
  return (
    <div className="flex flex-col gap-0.5">
      {SECTIONS.map((sec) => {
        const input = inputs[sec.key];
        const st = inputStatus(input);
        const gapBefore = sec.key === "poses" || sec.key === "models" || sec.key === "details";
        const tone = toneOf(sec.key);
        const on = selected === sec.key;
        const isCurrent = current === sec.key;
        const icon = st === "open" ? null : STATUS_ICON[st];
        return (
          <button
            key={sec.key}
            type="button"
            onClick={() => onSelect(sec.key)}
            aria-pressed={on}
            title={icon ? `${icon.title}${input.items.length ? ` · ${input.items.length}` : ""}` : isCurrent ? "Current step" : sec.required ? "Required" : "Optional"}
            className={`relative flex w-full items-center gap-2 rounded-lg px-2 py-1.5 text-left text-[13px] transition ${gapBefore ? "mt-2.5" : ""} ${
              on ? `${tone.sel} text-white` : isCurrent ? "bg-white/[0.04] text-white" : st === "open" ? "text-gray-400 hover:bg-white/5 hover:text-gray-200" : "text-gray-300 hover:bg-white/5"
            }`}
          >
            {isCurrent && <span className={`absolute inset-y-1.5 left-0 w-0.5 rounded-full ${tone.dot}`} />}
            <span className="flex w-3 shrink-0 justify-center">
              {icon ? (
                <span className={icon.cls}>{railIcon(icon.d, "h-3 w-3")}</span>
              ) : (
                <span className={`h-1.5 w-1.5 rounded-full ${isCurrent ? tone.dot : "bg-gray-600"}`} />
              )}
            </span>
            <span className={`truncate ${st === "skipped" ? "line-through decoration-gray-600" : ""}`}>{withNoun(TAB_LABEL[sec.key], noun)}</span>
            {sec.required && (
              <span className="-ml-1 text-amber-400" aria-label="required">
                *
              </span>
            )}
          </button>
        );
      })}
      <div className="mt-2.5 flex flex-wrap gap-x-2.5 gap-y-1 px-2 text-[10.5px] text-gray-500">
        {Object.entries(STATUS_ICON).map(([k, v]) => (
          <span key={k} className="flex items-center gap-1">
            <span className={v.cls}>{railIcon(v.d, "h-2.5 w-2.5")}</span>
            {k === "added" ? "Uploaded" : k === "reused" ? "Reused" : "Skipped"}
          </span>
        ))}
      </div>
    </div>
  );
}

/**
 * The selected step's card. Once something's chosen it shows what was picked, with Edit; editing a one-choice input
 * opens its form on the current choice and replaces it, the others list what's there to remove or add to.
 */
export function SectionPanel({
  step,
  input,
  generated,
  onAdded,
  onRemove,
  onSkip,
}: {
  step: Step;
  input: StepInput;
  generated: boolean;
  onAdded: (items: Item[], replace: boolean) => void;
  onRemove: (itemId: string) => void;
  onSkip: (skipped: boolean) => void;
}) {
  const section = sectionOf(step);
  const noun = usePoseNoun();
  const single = SINGLE_CHOICE.includes(step);
  const current = single ? (input.items[input.items.length - 1] ?? null) : null;
  const startOption = () =>
    section.options.length === 1
      ? section.options[0].key
      : section.options.some((o) => o.key === current?.option)
        ? current!.option
        : null;
  // A section with one way in opens its form straight away.
  const [openOption, setOpenOption] = useState<string | null>(startOption);
  const [editing, setEditing] = useState(!input.items.length);
  const [families, setFamilies] = useState<string[]>([]);
  const state = sectionState(input);
  const option = section.options.find((o) => o.key === openOption);
  const edit = () => {
    setOpenOption(startOption());
    setEditing(true);
  };

  return (
    <section className="rounded-2xl border border-surface-border bg-surface p-4" style={toneVarsOf(step)}>
      <div className="mb-3 flex items-start justify-between gap-3">
        <div>
          <div className="flex items-center gap-2 text-sm font-semibold text-white">
            {withNoun(section.title, noun)}
            <span
              className={`rounded-full px-1.5 py-px text-[10px] font-medium ${section.required ? "bg-amber-500/15 text-amber-300" : "bg-white/5 text-gray-400"}`}
            >
              {section.required ? "Required" : "Optional"}
            </span>
          </div>
          <div className="text-xs text-gray-500">{state === "skipped" ? "Skipped" : editing ? withNoun(section.hint, noun) : "Selected"}</div>
        </div>
        {!editing && (
          <button
            type="button"
            onClick={edit}
            className="flex shrink-0 items-center gap-1.5 rounded-lg bg-accent px-3 py-1.5 text-xs font-semibold text-white hover:bg-accent-hover"
          >
            {railIcon(ICON.pen, "h-3.5 w-3.5")}Edit
          </button>
        )}
      </div>

      {!editing ? (
        <>
          <div className="space-y-2">
            {input.items.map((i) => (
              <div key={i.id} className="rounded-lg bg-surface-raised px-3 py-2.5">
                <div className="flex items-center gap-2 text-xs">
                  <span className="shrink-0 text-emerald-400">{railIcon(ICON.check, "h-3.5 w-3.5")}</span>
                  <span className="truncate text-gray-100">{i.label}</span>
                  <span className="truncate text-gray-500">{i.detail}</span>
                  {i.reused && <span className="ml-auto shrink-0 text-[10px] text-gray-500">Reused</span>}
                </div>
                {step !== "raw" && (
                  <div className="mt-2 pl-5">
                    <ReusedDetails step={step} item={i} />
                  </div>
                )}
              </div>
            ))}
          </div>
          {generated && <p className="mt-2 text-[11px] text-gray-500">Changes apply to images you run from now on.</p>}
        </>
      ) : (
        <>
          {!single && input.items.length > 0 && (
            <ul className="mb-3 space-y-1.5">
              {input.items.map((i) => (
                <li key={i.id} className="flex items-center gap-2 rounded-lg bg-surface-raised px-3 py-2 text-xs">
                  <span className="shrink-0 text-emerald-400">{railIcon(ICON.check, "h-3.5 w-3.5")}</span>
                  <span className="truncate text-gray-100">{i.label}</span>
                  <span className="truncate text-gray-500">{i.detail}</span>
                  <button
                    type="button"
                    onClick={() => onRemove(i.id)}
                    className="ml-auto text-gray-500 hover:text-white"
                    aria-label={`Remove ${i.label}`}
                  >
                    ×
                  </button>
                </li>
              ))}
            </ul>
          )}

          {step === "raw" && <FamilyChoice value={families} onChange={setFamilies} />}

          <div className={`grid grid-cols-2 gap-2 ${section.options.length === 3 ? "xl:grid-cols-3" : "xl:grid-cols-4"}`}>
            {section.options.map((o) => (
              <button
                key={o.key}
                type="button"
                onClick={() => setOpenOption((cur) => (cur === o.key ? null : o.key))}
                className={`flex items-center gap-2 rounded-xl border px-3 py-2.5 text-left text-xs font-medium transition ${openOption === o.key ? "border-accent bg-accent/15 text-white" : "border-surface-border text-gray-200 hover:border-accent/60 hover:text-white"}`}
              >
                <span className="shrink-0 text-accent-hover">{railIcon(o.icon)}</span>
                {withNoun(o.label, noun)}
              </button>
            ))}
          </div>

          {option && (
            <OptionForm
              key={option.key}
              step={step}
              option={option}
              initial={current && (section.options.length === 1 || current.option === option.key) ? current : null}
              families={families}
              onCancel={() => {
                setOpenOption(null);
                if (input.items.length) setEditing(false);
              }}
              onDone={(items, replace) => {
                onAdded(items, replace);
                setOpenOption(null);
                setEditing(false);
              }}
            />
          )}

          <div className="mt-3 flex justify-end">
            {state === "skipped" ? (
              <button type="button" onClick={() => onSkip(false)} className="text-xs text-gray-400 hover:text-white">
                Undo skip
              </button>
            ) : state === "open" && !section.required ? (
              <button
                type="button"
                onClick={() => onSkip(true)}
                className="text-xs text-gray-500 underline-offset-2 hover:text-gray-300 hover:underline"
              >
                Skip for now
              </button>
            ) : input.items.length > 0 ? (
              <button
                type="button"
                onClick={() => {
                  setOpenOption(null);
                  setEditing(false);
                }}
                className="text-xs text-gray-400 hover:text-white"
              >
                Done
              </button>
            ) : null}
          </div>
        </>
      )}
    </section>
  );
}

/** What an item actually holds: the models, backgrounds, poses, files, links or text. */
function ReusedDetails({ step, item }: { step: Step; item?: Item }) {
  const { lib, detail } = useCatalog();
  if (!item) return null;
  if ((step === "models" || step === "backgrounds") && (item.ids?.length || item.text)) {
    const all = choices(lib, detail.session.uploads, step);
    return (
      <div>
        {item.text && <p className="mb-2 text-[11px] text-gray-300">“{item.text}”</p>}
        <div className="flex flex-wrap gap-2">
          {all
            .filter((x) => item.ids?.includes(x.id)) // a described-only choice has no ids
            .map((x) => (
              <div key={x.id} className="w-14 text-center">
                {x.image ? (
                  <img src={x.image} alt={x.name} className="aspect-square w-full rounded-lg object-cover" />
                ) : (
                  <span className="block aspect-square w-full rounded-lg border border-white/10" style={{ background: x.swatch ?? "#2f3446" }} />
                )}
                <span className="mt-1 block truncate text-[10px] text-gray-400">{x.name}</span>
              </div>
            ))}
        </div>
        {item.randomize && <p className="mt-2 text-[11px] text-gray-500">Randomized — each SKU gets one of these.</p>}
      </div>
    );
  }
  if (step === "poses") {
    const plan = item.poses ?? {};
    return (
      <div className="space-y-1.5">
        {Object.entries(plan).map(([cat, poses]) => (
          <div key={cat} className="text-[11px]">
            <span className="text-gray-300">{catName(lib, cat)}</span>
            <span className="text-gray-500"> · {poses.map((p) => poseName(lib, p)).join(", ")}</span>
          </div>
        ))}
      </div>
    );
  }
  if (step === "output")
    return (
      <p className="text-[11px] text-gray-300">
        {item.resolution ?? item.label} · {item.ratio ? `${item.ratio} aspect ratio` : item.detail}
      </p>
    );
  if (step === "details" && item.option === "note")
    return <p className="text-[11px] text-gray-300 italic">“{item.text || item.label}”</p>;
  if (item.url) return <LinkRow url={item.url} detail={item.detail} />;
  const upload = detail.session.uploads.find((u) => item.upload_ids?.includes(u.id));
  return <FileRow name={item.label} detail={item.detail} href={upload?.url} />;
}

/** A link from an input: opens in a new tab. */
function LinkRow({ url, detail }: { url: string; detail?: string | null }) {
  const href = /^https?:\/\//i.test(url) ? url : `https://${url}`;
  return (
    <a href={href} target="_blank" rel="noreferrer" className="flex items-center gap-1.5 text-[12px] text-accent-hover hover:underline">
      <span className="shrink-0">{railIcon(ICON.link, "h-3.5 w-3.5")}</span>
      <span className="truncate">{prettyLink(href)}</span>
      {detail && <span className="shrink-0 text-gray-500">· {detail}</span>}
      <span className="shrink-0">↗</span>
    </a>
  );
}

/** A stored file, with View (its signed link) when it belongs to this session. */
function FileRow({ name, detail, href }: { name?: string; detail?: string | null; href?: string }) {
  return (
    <div className="flex items-center gap-1.5 text-[12px] text-gray-200">
      <span className="shrink-0 text-gray-500">{railIcon(ICON.doc, "h-3.5 w-3.5")}</span>
      <span className="truncate">{name}</span>
      {detail && <span className="shrink-0 text-gray-500">· {detail}</span>}
      {href && (
        <a href={href} target="_blank" rel="noreferrer" className="ml-1 shrink-0 text-accent-hover hover:underline">
          View
        </a>
      )}
    </div>
  );
}

/** Under a step's question: exactly what was used last time, so Reuse is clear before clicking it. */
function LastUsed({ step, items, project }: { step: Step; items: Item[]; project?: string }) {
  return (
    <div className="ml-1 max-w-md rounded-xl border border-surface-border bg-white/[0.02] p-3">
      <div className="mb-2 text-[10.5px] font-semibold tracking-wider text-gray-500 uppercase">
        Last used{project ? ` · ${project}` : ""}
      </div>
      <div className="space-y-2">
        {items.map((it, i) => (
          <div key={i}>
            {!["guidelines", "details", "output"].includes(step) && <div className="mb-1 text-[12px] text-gray-300">{it.label}</div>}
            <ReusedDetails step={step} item={it} />
          </div>
        ))}
      </div>
    </div>
  );
}

/** "Reused …" in the chat, with View details to see exactly what came back from last time. */
function ReusedItem({ m }: { m: Msg }) {
  const [open, setOpen] = useState(false);
  return (
    <div className="flex justify-end">
      <div className="max-w-[80%] rounded-2xl bg-white/[0.06] px-3.5 py-2 text-sm text-gray-200">
        <div>
          Reused {m.label}
          {m.detail ? <span className="text-gray-400"> · {m.detail}</span> : null}
          {m.item && (
            <button type="button" onClick={() => setOpen((o) => !o)} aria-expanded={open} className="ml-2 text-xs text-accent-hover hover:underline">
              {open ? "Hide details" : "View details"}
            </button>
          )}
        </div>
        {open && m.section && (
          <div className="mt-2 border-t border-white/10 pt-2">
            <ReusedDetails step={m.section} item={m.item} />
          </div>
        )}
      </div>
    </div>
  );
}

/** Additional details: a big text box right in the card; reuse, a brief or Skip are small links under it. */
function WriteAsk({ m, onAction }: { m: Msg; onAction: OnAction }) {
  const [text, setText] = useState("");
  const send = () => {
    if (text.trim()) onAction(m.id, { key: "write", label: text.trim() });
  };
  const tone = toneOf(m.step);
  const lastText = m.last?.map((i) => i.text || i.label).join("; ");
  return (
    <div className={`space-y-2.5 rounded-xl border p-3 ${tone.card}`}>
      <div className={`flex items-center gap-1.5 text-[11px] font-semibold tracking-wider uppercase ${tone.text}`}>
        {railIcon(ICON.spark, "h-3.5 w-3.5")}
        Next step · {TAB_LABEL.details}
      </div>
      <p className="text-sm leading-relaxed text-gray-200">{m.text}</p>
      <textarea
        rows={5}
        value={text}
        onChange={(e) => setText(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) send();
        }}
        aria-label="Additional details"
        placeholder="e.g. Keep skin tones warm. No hands covering the product. Marketplace needs a white first image."
        className="w-full resize-y rounded-lg border border-surface-border bg-surface px-3 py-2.5 text-sm text-gray-100 placeholder:text-gray-500 focus:border-accent focus:outline-none"
      />
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1.5">
        <button
          type="button"
          onClick={send}
          disabled={!text.trim()}
          className={`rounded-lg border px-3 py-1.5 text-[13px] text-white transition disabled:opacity-40 ${tone.primary}`}
        >
          Continue →
        </button>
        {m.actions?.map((a) => (
          <button
            key={a.key}
            type="button"
            onClick={() => onAction(m.id, a)}
            title={a.key === "reuse" && lastText ? `“${lastText}”` : undefined}
            className="text-xs text-gray-400 underline-offset-2 hover:text-white hover:underline"
          >
            {a.key === "reuse"
              ? `Use last time’s${lastText ? `: “${lastText.length > 40 ? `${lastText.slice(0, 38)}…` : lastText}”` : ""}`
              : a.key === "skip"
                ? "Skip"
                : a.label}
          </button>
        ))}
      </div>
    </div>
  );
}

/** What processing found, before Start processing: SKUs per category and the setup each will run with. */
function SkuSummary() {
  const { lib, detail } = useCatalog();
  const cats = skuSummary(detail.skus, lib);
  return (
    <div className="space-y-2">
      <Bubble from="agent">Here’s what I found.</Bubble>
      <div className="max-w-xl rounded-2xl border border-surface-border bg-surface-raised/40 p-4">
        <div className="flex items-baseline justify-between">
          <span className="text-sm font-semibold text-white">{plural(detail.skus.length, "SKU")} found</span>
          <span className="text-[11px] text-gray-500">{plural(cats.length, "category").replace("categorys", "categories")}</span>
        </div>
        <div className="mt-3 grid gap-2 sm:grid-cols-3">
          {cats.map((c) => (
            <div key={c.cat} className="rounded-xl border border-surface-border bg-surface px-3 py-2.5">
              <div className="flex items-baseline justify-between">
                <span className="text-[13px] font-medium text-gray-100">{c.name}</span>
                <span className="text-lg font-semibold text-white">{c.count}</span>
              </div>
              <div className="font-mono text-[10.5px] text-gray-500">{c.range}</div>
              {c.subs.length > 0 && (
                <div className="mt-1 text-[11px] text-gray-300">{c.subs.map((x) => `${x.name} ${x.count}`).join(" · ")}</div>
              )}
              <div className="mt-1 truncate text-[11px] text-gray-400">
                {c.names.join(", ")}
                {c.count > 3 ? "…" : ""}
              </div>
            </div>
          ))}
        </div>
        <dl className="mt-3 grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 border-t border-surface-border pt-3 text-xs">
          {setupRows(detail.session.inputs, lib, detail.skus).map(([k, v]) => (
            <Fragment key={k}>
              <dt className="text-gray-500">{k}</dt>
              <dd className="truncate text-gray-200">{v}</dd>
            </Fragment>
          ))}
        </dl>
      </div>
    </div>
  );
}

/** A processing question: two answers while open (amber, no default), then the question and the chosen answer. */
/** The raw photos of the SKUs a question is about (each SKU's first four, with its code), so the brand can answer
 * without leaving the chat; a photo opens full size in a new tab. */
function QuestionPhotos({ skuIds }: { skuIds: string[] }) {
  const { detail } = useCatalog();
  const skus = skuIds.flatMap((id) => detail.skus.filter((s) => s.id === id && s.raw_urls.length));
  if (!skus.length) return null;
  return (
    <div className="flex flex-wrap gap-3">
      {skus.slice(0, 3).map((s) => (
        <div key={s.id} className="flex items-center gap-1.5">
          {s.raw_urls.slice(0, 4).map((u, i) => (
            <a key={i} href={safeUrl(u)} target="_blank" rel="noreferrer" title={`${s.code} · ${s.name}`}>
              <img src={safeUrl(u)} alt="" className="size-20 rounded-lg object-cover transition hover:opacity-80" />
            </a>
          ))}
          {skuIds.length > 1 && <span className="font-mono text-[10.5px] text-gray-400">{s.code}</span>}
        </div>
      ))}
    </div>
  );
}

function QuestionCard({ m, onAction }: { m: Msg; onAction: OnAction }) {
  const { detail, lib } = useCatalog();
  const q = detail.questions.find((x) => x.id === m.question_id);
  if (!q) return null;
  if (q.answer !== null)
    return (
      <div className="space-y-1.5">
        <Bubble from="agent">{q.text}</Bubble>
        <div className="pl-1 text-xs text-gray-500">✓ {q.options[q.answer]}</div>
      </div>
    );
  return (
    <div className="space-y-2.5 rounded-xl border border-amber-500/30 bg-amber-500/[0.04] p-3">
      <div className="flex items-center gap-1.5 text-[11px] font-semibold tracking-wider text-amber-400 uppercase">
        {railIcon(ICON.alert, "h-3.5 w-3.5")}
        {q.blocking ? "Needs your decision" : "Worth a look"}
      </div>
      <p className="text-sm leading-relaxed text-gray-200">{q.text}</p>
      <QuestionPhotos skuIds={q.sku_ids} />
      <div className="flex max-w-sm flex-col gap-1.5">
        {q.options.map((label, i) => {
          const tip = kindTip(lib, label); // a kind option ("Tops › Kurtis"): what that type is
          return (
            <div key={i} className="flex items-center gap-1.5">
              <button
                type="button"
                onClick={() => onAction(m.id, { key: `answer:${i}`, label })}
                className="flex min-w-0 flex-1 items-center justify-between gap-3 rounded-lg border border-surface-border bg-surface/60 px-3 py-2 text-left text-[13px] text-gray-200 transition hover:border-gray-500 hover:text-white"
              >
                <span className="min-w-0">
                  <span className="mr-2 text-[10.5px] font-semibold tracking-wider text-gray-500 uppercase">Option {i + 1}</span>
                  {label}
                </span>
                <span className="text-gray-500">→</span>
              </button>
              {tip && <InfoTip text={tip} label={`What is ${label}?`} />}
            </div>
          );
        })}
      </div>
    </div>
  );
}

/** What a button in the chat does: the message it belongs to and the action. */
export type OnAction = (msgId: string, action: Action) => void;

/** One Input chat entry, by kind. */
export function ChatMessage({ m, onAction }: { m: Msg; onAction: OnAction }) {
  const noun = usePoseNoun();
  if (m.kind === "hero")
    return (
      <div className="pt-10 pb-3">
        <h1 className="text-3xl font-bold tracking-tight text-white md:text-4xl">{m.title}</h1>
        {m.text && <p className="mt-3 text-base text-gray-300">{m.text}</p>}
        <div className="mt-8 text-[10.5px] font-semibold tracking-wider text-gray-500 uppercase">Recommended next step</div>
        <div className="mt-2 flex flex-wrap gap-2">
          <button
            type="button"
            onClick={() => onAction(m.id, { key: "open:raw", label: "Upload raw images" })}
            className="flex items-center gap-2 rounded-xl border border-accent/60 bg-accent/10 px-3.5 py-2.5 text-sm text-white transition hover:border-accent hover:bg-accent/15"
          >
            <span className="flex h-7 w-7 items-center justify-center rounded-lg bg-accent/20 text-accent-hover">{railIcon(ICON.folder)}</span>
            Upload raw images
          </button>
        </div>
      </div>
    );
  if (m.kind === "step" && m.section)
    return (
      <div className="flex items-center gap-3 pt-4 text-[11px] text-gray-500">
        <span className="h-px flex-1 bg-surface-border" />
        <span className={toneOf(m.section).text}>{withNoun(TAB_LABEL[m.section], noun)}</span>
        <span className="h-px flex-1 bg-surface-border" />
      </div>
    );
  if (m.kind === "ask" && m.write && !m.answered) return <WriteAsk m={m} onAction={onAction} />;
  if (m.kind === "ask") return <AskCard m={m} onAction={onAction} />;
  if (m.kind === "item") {
    if (m.reused) return <ReusedItem m={m} />;
    return (
      <UserLine>
        Added {m.label}
        {m.detail ? <span className="text-gray-400"> · {m.detail}</span> : null}
      </UserLine>
    );
  }
  if (m.kind === "skip" && m.section) return <UserLine>Skipped {withNoun(TAB_LABEL[m.section], noun).toLowerCase()}</UserLine>;
  if (m.kind === "status") return <UpdateLine text={m.text} done={m.done} error={m.error} detail={m.detail} />;
  if (m.kind === "skuSummary") return <SkuSummary />;
  if (m.kind === "question") return <QuestionCard m={m} onAction={onAction} />;
  if (m.kind === "user") return <UserLine>{m.text}</UserLine>;
  return <Bubble from="agent">{m.text}</Bubble>;
}

/** A step's question card: stacked, spelled-out options (the recommended one first, in the step's colour), Skip as a
 * quiet link; once answered, the question stays as a plain line. */
function AskCard({ m, onAction }: { m: Msg; onAction: OnAction }) {
  const noun = usePoseNoun();
  const actions = m.actions ?? [];
  const skipAction = actions.find((a) => a.key === "skip");
  const main = actions.filter((a) => a.key !== "skip");
  if (m.answered) {
    const chosen = ["change", "generation"].includes(m.answered) && main.find((a) => a.key === m.answered);
    return (
      <div className="space-y-1.5">
        <Bubble from="agent">{m.text}</Bubble>
        {chosen && <div className="pl-1 text-xs text-gray-500">✓ {chosen.label}</div>}
      </div>
    );
  }
  const recommended = main.find((a) => ["reuse", "generation"].includes(a.key)) ?? main[0];
  const options = recommended ? [recommended, ...main.filter((a) => a !== recommended)] : main;
  const title = `Next step · ${m.step === "summary" ? "Start processing" : withNoun(isStepLabel(m.step), noun)}`;
  const tone = toneOf(m.step);
  const step = m.step as Step;
  return (
    <div className={`space-y-2.5 rounded-xl border p-3 ${tone.card}`}>
      <div className={`flex items-center gap-1.5 text-[11px] font-semibold tracking-wider uppercase ${tone.text}`}>
        {railIcon(ICON.spark, "h-3.5 w-3.5")}
        {title}
      </div>
      <p className="text-sm leading-relaxed text-gray-200">{m.text}</p>
      {m.last && STEPS.includes(step) && <LastUsed step={step} items={m.last} project={m.project} />}
      <div className="flex max-w-sm flex-col gap-1.5">
        {options.map((a, i) => {
          const primary = a === recommended;
          return (
            <button
              key={a.key}
              type="button"
              onClick={() => onAction(m.id, a)}
              className={`flex items-center justify-between gap-3 rounded-lg border px-3 py-2 text-left text-[13px] transition ${
                primary ? `${tone.primary} text-white` : "border-surface-border bg-surface/60 text-gray-200 hover:border-gray-500 hover:text-white"
              }`}
            >
              <span className="min-w-0">
                {options.length > 1 && (
                  <span className={`mr-2 text-[10.5px] font-semibold tracking-wider uppercase ${primary ? tone.text : "text-gray-500"}`}>
                    Option {i + 1}
                  </span>
                )}
                {a.label}
              </span>
              <span className={primary ? tone.text : "text-gray-500"}>→</span>
            </button>
          );
        })}
        {skipAction && (
          <button
            type="button"
            onClick={() => onAction(m.id, skipAction)}
            className="self-start px-1 pt-0.5 text-xs text-gray-500 underline-offset-2 hover:text-gray-300 hover:underline"
          >
            {skipAction.label}
          </button>
        )}
      </div>
    </div>
  );
}
/** A step's list name, or "Input". */
const isStepLabel = (s?: string) => (STEPS.includes(s as Step) ? TAB_LABEL[s as Step] : "Input");

/**
 * Input as one guided chat: the conversation follows the latest message, finished steps fade back and the current
 * step (from its heading down) stays clear. No chat box: the flow runs on the inputs and the agent's buttons.
 */
export function GuidedChat({
  msgs,
  ready,
  readyN,
  projectName,
  selected,
  panel,
  onAction,
  onRefresh,
  onReset,
  onRename,
  inputsDone,
}: {
  msgs: Msg[];
  ready: boolean;
  readyN: number;
  projectName: string;
  selected: Step | null;
  panel: ReactNode;
  onAction: OnAction;
  onRefresh: () => void;
  onReset: () => void;
  onRename: (name: string) => void;
  inputsDone: (s: Step) => boolean;
}) {
  const scrollRef = useRef<HTMLDivElement>(null);
  const endRef = useRef<HTMLDivElement>(null);
  // The brand's own lines and step headings show at once; the agent's wait behind typing dots.
  const { visible, typing } = useStaggered(msgs, (m) => ["user", "item", "skip", "step", "status", "hero"].includes(m.kind));
  // Keep the newest message just in view; room below lets the brand scroll it up the screen.
  useLayoutEffect(() => {
    const el = scrollRef.current;
    const end = endRef.current;
    if (el && end) el.scrollTop = Math.max(0, end.offsetTop - el.clientHeight + 24);
  }, [visible.length, typing, selected]);
  // Everything before the latest step heading is done: shown faded, the current step stays clear.
  const currentFrom = visible.map((m) => m.kind).lastIndexOf("step");
  const [name, setName] = useState(projectName);

  return (
    <main className="flex min-w-0 flex-1 flex-col overflow-hidden rounded-2xl border border-accent/40 bg-surface">
      <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2 border-b border-surface-border px-4 py-3 sm:px-6">
        <span className="flex min-w-0 items-center gap-2 text-sm font-semibold text-white">
          <span className="h-2 w-2 shrink-0 rounded-full bg-accent" /> Set up your catalog
          <input
            value={name}
            onChange={(e) => setName(e.target.value)}
            onBlur={() => name.trim() && name.trim() !== projectName && onRename(name.trim())}
            onKeyDown={(e) => e.key === "Enter" && (e.target as HTMLInputElement).blur()}
            aria-label="Project name"
            maxLength={200}
            className="min-w-0 rounded-md border border-transparent bg-transparent px-1.5 py-0.5 text-xs font-normal text-gray-400 hover:border-surface-border focus:border-accent focus:text-white focus:outline-none"
          />
        </span>
        <div className="flex items-center gap-4">
          <button
            type="button"
            onClick={onReset}
            title="Clear everything, Processing included, and run the flow again from the start"
            className="rounded-lg px-2.5 py-1.5 text-xs text-gray-400 transition hover:bg-white/5 hover:text-white"
          >
            Reset
          </button>
          {ready && (
            <button
              type="button"
              onClick={onRefresh}
              title="Clear everything here and start a new batch. Processing keeps its SKUs."
              className="flex items-center gap-1.5 rounded-lg border border-surface-border px-3 py-1.5 text-xs text-gray-200 transition hover:border-accent/60 hover:text-white"
            >
              <span className="text-accent-hover">{railIcon(ICON.refresh, "h-3.5 w-3.5")}</span>
              Refresh
            </button>
          )}
          <div className="flex items-center gap-2 text-xs text-gray-400">
            <span>
              <span className="font-semibold text-emerald-400">{readyN}</span>/{STEPS.length} ready
            </span>
            <span className="hidden gap-1 sm:flex">
              {STEPS.map((s) => (
                <span key={s} className={`h-1.5 w-4 rounded-full ${inputsDone(s) ? "bg-emerald-500" : "bg-surface-raised"}`} />
              ))}
            </span>
          </div>
        </div>
      </div>

      <div ref={scrollRef} className="catalog-scroll relative flex-1 overflow-y-auto">
        <div className="mx-auto max-w-3xl space-y-3 px-4 py-6 sm:px-6">
          {visible.map((m, i) => (
            <div key={m.id} className={`animate-msg-in ${i < currentFrom ? "opacity-45 transition-opacity hover:opacity-80" : ""}`}>
              <ChatMessage m={m} onAction={onAction} />
            </div>
          ))}
          {typing ? <TypingDots /> : panel}
          <div ref={endRef} />
          <div className="h-[55vh]" aria-hidden="true" />
        </div>
      </div>
    </main>
  );
}

/** Asking again about a skipped step: "You skipped models earlier. Want to add something now?" */
export const skippedAsk = (step: Step, noun: PoseNoun): Omit<Msg, "id"> => ({
  kind: "ask",
  step,
  text: withNoun(`You skipped ${TAB_LABEL[step].toLowerCase()} earlier. Want to add something now?`, noun),
  actions: [{ key: "change", label: withNoun(`Add new ${NOUN[step]}`, noun) }],
});
