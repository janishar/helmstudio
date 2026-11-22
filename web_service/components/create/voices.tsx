"use client";
// Voices (docs/mock/VoicePicker.dc.html, CreateAudio.dc.html): a `voice` param's composer tile and inspector tile,
// and the voice picker: Presets (the live hosts' starter voices by family, each with its preview), Mine (designed
// voices: Rename, Delete), greyed with the fit's reason; Design a voice (Hear it runs the workflow that designs a
// voice, then Save as voice); and (API 1.31, docs/mock/VoicePickerZonos3.dc.html) Clone from a clip: a take's 3–30 s
// part, named, with the consent tick, heard through the workflow that clones (ZONOS3) before Save voice. The take
// action Save as voice opens it with that take. An element's Voice tile comes with API 1.32.
import { useEffect, useRef, useState } from "react";
import { ChevronRight, Mic, MoreVertical, Pause, Play, Scissors, Wand2 } from "lucide-react";
import { X } from "lucide-react";
import { ErrorNote, Info, Modal, UploadStatus } from "@/components/kit";
import { VisualPicker } from "@/components/create/visual-picker";
import { TileWords, tileClass } from "@/components/create/elements";
import { usePlayer } from "@/components/create/audio";
import { RangeBar } from "@/components/create/source";
import { api, message, ok, type S } from "@/lib/api";
import { safeUrl } from "@/lib/format";
import { reloadLibrary, useVoices, voicesError } from "@/lib/library";
import { audioFormat } from "@/lib/media";
import { isActive } from "@/lib/runs";
import { factsOf, mediaFit, pickable, trimmed } from "@/lib/source";
import { remember, useTakes } from "@/lib/take-cache";
import { AUDIO_ACCEPT, typeOf, uploadFile, type UploadProgress } from "@/lib/takes";
import {
  CLIP,
  baseLanguage,
  clipError,
  clipFitLine,
  clipText,
  languageName,
  matches,
  presetGroups,
  reasonFor,
  takesClips,
  voiceBody,
  voiceLine,
  voiceTileText,
} from "@/lib/voices";
import { fields as fieldsOf, isVoice, type Field, type Raw } from "@/lib/workflow";
import { TextArea } from "@/components/expand-text";

type Item = S<"LibraryItem">;
type Take = S<"Take">;
type Workflow = S<"Workflow">;

/** A workflow Hear it runs, its name, and the session its tries go to. `stay`: keeps Create on the open workflow
 * while the tries land in its session (else the newest run's workflow would open under the dialog). */
type Tries = {
  workflow: Workflow;
  label: string;
  /** the session the tries go to: the open one, else one "Voice design" session made once and kept */
  session: () => Promise<string>;
  stay?: () => void;
};

/** What a voice picker needs from Create: the open workflow (the fit); for Design a voice the workflow that designs
 * one; for Clone from a clip the workflow that clones one (ZONOS3's voice-over: absent when none takes clips). */
export type VoiceCtx = {
  workflow: Pick<Workflow, "id" | "title" | "family" | "nodes" | "node_schemas">;
  familyTitle: (family: string) => string;
  design?: Tries;
  clone?: Tries;
};

/** The first designed voice a take was made with (Save as voice keeps its design with the clip), if any. */
export const designedOf = (t: Pick<Take, "made_with">) =>
  t.made_with?.voices?.find((v) => v.source === "designed");

/** Hear it's line in Clone from a clip until it is changed. */
const SAMPLE_LINE = "Hello. This is how I sound when I read a line.";

/** A family's starter voices: at most this many show without the language filter and the scrolled grid. */
const ROWS_OF_THREE = 9;

/** The design workflow among `ws` for an audio workflow of `family`: a template whose voice param takes designed
 * voices, one that takes them only (its `only`: [design]) first, then the family's own. */
export function designWorkflow(ws: Workflow[], family?: string): Workflow | undefined {
  const score = (w: Workflow) => {
    const a = fieldsOf(w)
      .filter((f) => f.param.type === "voice")
      .map((f) => (f.param.accepts ?? []).join());
    if (!a.some((x) => x.split(",").includes("design"))) return -1;
    return (a.includes("design") ? 2 : 0) + (w.family === family ? 1 : 0);
  };
  return ws
    .filter((w) => w.source === "template" && w.media === "audio" && score(w) >= 0)
    .sort((a, b) => score(b) - score(a))[0];
}

/** The workflow Clone from a clip's Hear it runs among `ws`: a template whose voice param takes clips, the open
 * workflow's family first. */
export function cloneWorkflow(ws: Workflow[], family?: string): Workflow | undefined {
  return ws
    .filter(
      (w) =>
        w.source === "template" &&
        w.media === "audio" &&
        fieldsOf(w).some((f) => f.param.type === "voice" && f.param.accepts?.includes("clip")),
    )
    .sort((a, b) => +(b.family === family) - +(a.family === family))[0];
}

const valueOf = (f: Field, raws: Record<string, Raw>) => {
  const v = raws[f.key];
  return isVoice(v) ? v.voice_id : "";
};

/** A voice param's composer tile: "Voice · Ryan · preset"; it opens the picker. */
export function VoiceTile({
  f,
  raws,
  onChange,
  ctx,
}: {
  f: Field;
  raws: Record<string, Raw>;
  onChange: (key: string, raw: Raw) => void;
  ctx: VoiceCtx;
}) {
  const [open, setOpen] = useState(false);
  const voices = useVoices();
  const id = valueOf(f, raws);
  const v = voices?.find((x) => x.id === id);
  const label = f.param.label ?? f.param.name;
  const text = id && !v ? unknownVoice(voices) : voiceTileText(v);
  return (
    <>
      <button
        type="button"
        aria-haspopup="dialog"
        aria-label={`${label}: ${text}`}
        onClick={() => setOpen(true)}
        className={tileClass("card")}
      >
        <Mic size={18} strokeWidth={1.75} aria-hidden className="shrink-0" />
        <TileWords big="card" label={label} value={text} />
      </button>
      <VoicePicker
        open={open}
        onClose={() => setOpen(false)}
        value={id}
        onPick={(voice_id) => onChange(f.key, { voice_id })}
        ctx={ctx}
      />
    </>
  );
}

/** A voice param in the inspector: "Voice · Ryan · preset · English"; it opens the picker. */
export function VoiceSet({
  f,
  raws,
  onChange,
  ctx,
  error,
  help,
}: {
  f: Field;
  raws: Record<string, Raw>;
  onChange: (key: string, raw: Raw) => void;
  ctx: VoiceCtx;
  error?: string;
  /** a line under it (a Voice section of its own, docs/mock/CreateAudioZonos3.dc.html) */
  help?: string;
}) {
  const [open, setOpen] = useState(false);
  const voices = useVoices();
  const id = valueOf(f, raws);
  const v = voices?.find((x) => x.id === id);
  return (
    <div className="col-span-2 flex flex-col gap-1">
      <button
        type="button"
        aria-haspopup="dialog"
        onClick={() => setOpen(true)}
        className={`set bevel flex flex-col gap-0.5 ${error ? "outline-2 outline-bad" : ""}`}
      >
        <small className="text-[12px] text-t2">{f.param.label ?? f.param.name}</small>
        <b className="font-semibold">{id && !v ? unknownVoice(voices) : voiceTileText(v, true)}</b>
      </button>
      {help && <span className="text-[12px] text-t2">{help}</span>}
      {error && <span className="text-[12px] text-bad">{error}</span>}
      <VoicePicker
        open={open}
        onClose={() => setOpen(false)}
        value={id}
        onPick={(voice_id) => onChange(f.key, { voice_id })}
        ctx={ctx}
      />
    </div>
  );
}

/** The play button of a voice card (only where there is a clip to play). */
function PlayButton({ label, on, onClick }: { label: string; on: boolean; onClick: () => void }) {
  return (
    <button
      type="button"
      aria-label={label}
      aria-pressed={on}
      onClick={onClick}
      className="bevel grid size-9 shrink-0 place-items-center rounded-full bg-s4 text-t1 aria-pressed:bg-a2 aria-pressed:text-[var(--on2)]"
    >
      {on ? (
        <Pause size={14} aria-hidden className="fill-current" />
      ) : (
        <Play size={14} aria-hidden className="fill-current" />
      )}
    </button>
  );
}

/** A voice param's value the list does not hold: gone, not read (the read failed), or still loading. */
const unknownVoice = (voices: Item[] | null) =>
  voices ? "gone" : voicesError() ? "voices not read" : "…";

/** Deletes a designed voice (a draft not saved), then rereads the library. */
const deleteVoice = (id: string) =>
  api
    .DELETE("/v1/studio/library/{item_id}", { params: { path: { item_id: id } } })
    .then(() => reloadLibrary())
    .catch(() => undefined);

/** Cancels a try's run (it may have ended: the answer does not matter). */
const cancelRun = (id: string) =>
  api
    .POST("/v1/studio/runs/{run_id}/cancel", { params: { path: { run_id: id } } })
    .catch(() => undefined);

const card = (on: boolean, reason: string | null) =>
  `relative flex items-start gap-2.5 rounded-[14px] border p-2.5 ${on ? "border-a1 bg-s2" : "border-s5 bg-s1"} ${reason ? "opacity-50" : ""}`;

/** The voice picker (docs/mock/VoicePicker.dc.html, VoicePickerZonos3.dc.html): Presets and Mine, Design a voice,
 * or Clone from a clip (`take`: opened on it with that take, the take action Save as voice). */
export function VoicePicker({
  open,
  onClose,
  value,
  onPick,
  ctx,
  take,
}: {
  open: boolean;
  onClose: () => void;
  value: string;
  onPick: (voiceId: string) => void;
  ctx: VoiceCtx;
  take?: Take;
}) {
  const [view, setView] = useState<"list" | "design" | "clone">(take && open ? "clone" : "list");
  // a voice Hear it made here and not saved: Cancel deletes it
  const [draft, setDraft] = useState<Item | null>(null);
  const drop = () => {
    const d = draft;
    setDraft(null);
    if (d) void deleteVoice(d.id);
  };
  const close = () => {
    drop();
    setView("list");
    onClose();
  };
  const [picked, setPicked] = useState(value);
  const [tab, setTab] = useState<"presets" | "mine">("presets");
  // each opening starts on the voice the param holds (Save as voice: on the clone view)
  const [was, setWas] = useState(open);
  if (was !== open) {
    setWas(open);
    if (open) {
      setPicked(value);
      if (take) setView("clone");
    }
  }
  const saved = (x: Item) => {
    setDraft(null);
    setPicked(x.id);
    setTab("mine");
    setView("list");
  };
  const designed = view === "clone" && !!take && !!designedOf(take);
  return (
    <Modal
      open={open}
      onClose={close}
      title={
        view === "design"
          ? "Design a voice"
          : view === "clone"
            ? designed
              ? "Save as voice"
              : "Clone from a clip"
            : "Voice"
      }
      width="w-[min(94vw,880px)]"
      back={
        view !== "list"
          ? () => {
              drop();
              setView("list");
            }
          : undefined
      }
    >
      {view === "list" ? (
        <VoiceList
          ctx={ctx}
          picked={picked}
          setPicked={setPicked}
          tab={tab}
          setTab={setTab}
          onDesign={ctx.design && (() => setView("design"))}
          onClone={ctx.clone && (() => setView("clone"))}
          onCancel={close}
          onUse={(id) => {
            onPick(id);
            close();
          }}
        />
      ) : view === "design" ? (
        ctx.design && (
          <Designer
            design={ctx.design}
            draft={draft}
            setDraft={setDraft}
            onCancel={close}
            onSaved={saved}
          />
        )
      ) : (
        <Cloner
          ctx={ctx}
          start={take}
          draft={draft}
          setDraft={setDraft}
          onCancel={close}
          onSaved={saved}
        />
      )}
    </Modal>
  );
}

/** The take action Save as voice (docs/mock/CreateAudioZonos3.dc.html, beside Use in…): the picker opened on Clone
 * from a clip with this take; the voice saved is picked in Mine, and Use sets it on the open workflow. */
export function SaveAsVoice({
  take,
  ctx,
  onPick,
}: {
  take: Take;
  ctx: VoiceCtx;
  onPick?: (voiceId: string) => void;
}) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <button
        type="button"
        aria-haspopup="dialog"
        onClick={() => setOpen(true)}
        className="chip bevel h-11 shrink-0 rounded-[10px]"
      >
        Save as voice
      </button>
      {/* mounted only while open: a closed picker would read the library for every take shown */}
      {open && (
        <VoicePicker
          open
          onClose={() => setOpen(false)}
          value=""
          onPick={(id) => onPick?.(id)}
          ctx={ctx}
          take={take}
        />
      )}
    </>
  );
}

function VoiceList({
  ctx,
  picked,
  setPicked,
  tab,
  setTab,
  onDesign,
  onClone,
  onCancel,
  onUse,
}: {
  ctx: VoiceCtx;
  picked: string;
  setPicked: (id: string) => void;
  tab: "presets" | "mine";
  setTab: (t: "presets" | "mine") => void;
  onDesign?: () => void;
  onClone?: () => void;
  onCancel: () => void;
  onUse: (id: string) => void;
}) {
  const [items, setItems] = useState<Item[] | null>(null);
  const [error, setError] = useState("");
  const [q, setQ] = useState("");
  const [lang, setLang] = useState<Record<string, string>>({});
  // a family none of whose voices fit starts folded (docs/mock/VoicePickerZonos3.dc.html: Qwen3-TTS on ZONOS3)
  const [unfolded, setUnfolded] = useState<Record<string, boolean>>({});
  const [menu, setMenu] = useState<string | null>(null);
  const [renaming, setRenaming] = useState<string | null>(null);
  const [draft, setDraft] = useState("");
  const [confirming, setConfirming] = useState<string | null>(null);
  const { playing, toggle } = usePlayer();
  const wf = ctx.workflow;
  // the list may be stale (another tab, a host came or went): read it on each opening, with each voice's fit
  const load = () =>
    ok(
      api.GET("/v1/studio/library", {
        // no workflow (an element's Voice tile): every voice, none greyed
        params: { query: wf.id ? { kind: "voice", workflow: wf.id } : { kind: "voice" } },
      }),
    )
      .then((r) => {
        setItems(r.items.filter((x) => x.kind === "voice"));
        setError("");
      })
      .catch((e) => setError(message(e)));
  useEffect(() => {
    void load();
    void reloadLibrary();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [wf.id]);
  const all = items ?? [];
  const reason = (x: Item) => (wf.id ? reasonFor(wf, x) : null);
  // the families with a voice that fits first (the open workflow's own), each in the list's order
  const groups = presetGroups(all)
    .map((g, i) => ({ ...g, i, fits: g.voices.some((x) => !reason(x)) }))
    .sort((a, b) => +b.fits - +a.fits || a.i - b.i);
  const mine = all.filter((x) => x.source === "mine");
  const presets = groups.reduce((n, g) => n + g.voices.length, 0);
  // a workflow that takes clips (ZONOS3): Mine counts what fits and says why each does
  const clips = takesClips(wf);
  const mineFit = mine.filter((x) => !reason(x)).length;
  const takeOf = useTakes(mine.flatMap((x) => (x.voice?.clip ? [x.voice.clip.take_id] : [])));
  const wfTitle = ctx.familyTitle(wf.family);
  const pv = all.find((x) => x.id === picked);
  const pickable = pv && !reason(pv) ? pv : undefined;
  const fail = (e: unknown) => setError(message(e));
  /** A Mine card's line: a cloned voice's take and part ("from Interview · 0:04–0:16"), else its words. */
  const mineLine = (x: Item) => {
    const c = x.voice?.source === "cloned" ? x.voice.clip : undefined;
    if (!c) return voiceLine(x);
    const t = takeOf(c.take_id);
    // a long title is cut so the part stays in sight (the card's one line ellipsizes at its end)
    const title = t ? t.title : t === null ? "a deleted take" : "…";
    return `from ${title.length > 24 ? `${title.slice(0, 23).trimEnd()}…` : title} · ${clipText(c)}`;
  };

  const preview = (x: Item, label: string) => {
    // a voice with a clip plays its part of the take (a media fragment: the browser stops at out)
    const c = x.voice?.clip;
    const file = safeUrl(x.thumb_url);
    const url = file && c ? `${file}#t=${c.in},${c.out}` : file;
    return url ? (
      <PlayButton label={label} on={playing === x.id} onClick={() => toggle(x.id, url)} />
    ) : null;
  };
  const pickButton = (x: Item, line: string, badge?: string, ok?: string) => {
    const r = reason(x);
    // greyed, not disabled: it stays focusable and reads its reason (aria-describedby)
    const why = `why-${x.id.replace(/[^A-Za-z0-9_-]/g, "-")}`;
    return (
      <button
        type="button"
        data-pick
        aria-pressed={picked === x.id}
        aria-disabled={!!r || undefined}
        aria-describedby={r ? why : undefined}
        title={r ?? x.name}
        onClick={() => {
          if (r) return;
          setPicked(x.id);
          setMenu(null);
        }}
        className="flex min-w-0 flex-auto flex-col gap-0.5 text-left aria-disabled:cursor-not-allowed"
      >
        {badge && (
          <span className="mb-0.5 self-start rounded-full bg-s4 px-2 text-[11px] leading-[18px] font-semibold">
            {badge}
          </span>
        )}
        <b className="truncate text-[13px] font-semibold">{x.name}</b>
        <small className="truncate text-[12px] leading-4 text-t2">{line}</small>
        {r ? (
          <em id={why} className="mono truncate text-[11px] text-t2 not-italic">
            {r}
          </em>
        ) : (
          ok && <em className="mono truncate text-[11px] text-a1 not-italic">{ok}</em>
        )}
      </button>
    );
  };

  async function rename(x: Item) {
    try {
      await ok(
        api.PUT("/v1/studio/library/{item_id}", {
          params: { path: { item_id: x.id } },
          body: {
            group: x.group,
            name: draft,
            text: "",
            images: [],
            version: x.version,
            voice: voiceBody(x),
          },
        }),
      );
      setRenaming(null);
    } catch (e) {
      fail(e);
    }
    await load();
    void reloadLibrary();
  }
  async function remove(x: Item) {
    try {
      await ok(api.DELETE("/v1/studio/library/{item_id}", { params: { path: { item_id: x.id } } }));
      setConfirming(null);
      if (picked === x.id) setPicked("");
    } catch (e) {
      fail(e);
    }
    await load();
    void reloadLibrary();
  }

  const status = !items ? (
    !error && <p className="text-[13px] text-t2">Loading voices…</p>
  ) : tab === "mine" && !mine.length ? (
    <p className="text-[13px] text-t2">
      No voices of your own yet
      {onClone && onDesign
        ? ": clone one from a clip or design one from words."
        : onDesign
          ? ": design one from words."
          : onClone
            ? ": clone one from a clip."
            : "."}
    </p>
  ) : tab === "presets" && !presets ? (
    <p className="text-[13px] text-t2">No runner publishes starter voices now.</p>
  ) : null;

  return (
    <VisualPicker<"presets" | "mine", never>
      info="Presets come with the models; play one to hear it. Yours are voices you cloned from a clip or designed from words. Saved voices count toward the 200 items you can keep."
      action={
        (onClone || onDesign) && (
          <>
            {onClone && (
              <button
                type="button"
                onClick={onClone}
                className="flex h-8 items-center gap-1.5 rounded-full bg-s3 px-3 text-[12px] font-semibold"
              >
                <Scissors size={14} aria-hidden />
                Clone from a clip
              </button>
            )}
            {onDesign && (
              <button
                type="button"
                onClick={onDesign}
                className="flex h-8 items-center gap-1.5 rounded-full bg-s3 px-3 text-[12px] font-semibold"
              >
                <Wand2 size={14} aria-hidden />
                Design a voice
              </button>
            )}
          </>
        )
      }
      search={{
        id: "voice-q",
        label: "Search voices",
        placeholder: "Search names and styles",
        value: q,
        onChange: setQ,
        className: "flex-[1_1_260px]",
      }}
      filters={[
        {
          label: "Voices",
          kind: "tab",
          options: [
            ["presets", `Presets · ${presets}`],
            ["mine", clips ? `Mine · ${mineFit} fit` : `Mine · ${mine.length}`],
          ],
          value: tab,
          onChange: setTab,
        },
      ]}
      error={error}
      status={status}
      listLabel="Voices"
      grid={tab === "presets" ? "gap-3.5" : "grid-cols-2 gap-2.5"}
      footer={
        <>
          {tab === "mine" && clips && mine.length > 0 && (
            <p className="m-0 text-[12px] text-t2">
              {mineFit === mine.length ? `All ${mine.length}` : `${mineFit} of ${mine.length}`} fit{" "}
              {wfTitle}. A cloned voice is a voice print of its clip. A designed voice fits once it
              is saved with the clip you heard: {wfTitle} clones that clip, not the words.
            </p>
          )}
          <div className="flex items-center gap-2">
            <span className="mono text-[12px] text-t2">
              {pickable ? `${pickable.name} picked` : "Nothing picked"}
            </span>
            <span className="grow" />
            <button
              type="button"
              onClick={onCancel}
              className="bevel h-9 rounded-[10px] bg-s4 px-3.5 text-[13px]"
            >
              Cancel
            </button>
            <button
              type="button"
              disabled={!pickable}
              onClick={() => pickable && onUse(pickable.id)}
              className="bevel h-9 rounded-[10px] bg-a1 px-4 text-[13px] font-semibold text-on1 disabled:opacity-50"
            >
              {pickable ? `Use ${pickable.name}` : "Use"}
            </button>
          </div>
          <p className="m-0 text-[12px] text-t2">
            Voices that do not fit {wf.title} are greyed with the reason. Presets are read-only.
          </p>
        </>
      }
    >
      {tab === "presets"
        ? groups.map((g) => {
            // the chips by their names (VoicePickerZonos3.dc.html: English, French, … Telugu)
            const langs = [...new Set(g.voices.map((x) => baseLanguage(x.voice?.language)))]
              .filter(Boolean)
              .sort((a, b) => languageName(a, g.family).localeCompare(languageName(b, g.family)));
            const many = g.voices.length > ROWS_OF_THREE;
            // a long family opens on English when it has some, else on all
            const at = lang[g.family] ?? (langs.includes("en") ? "en" : "");
            const shown = g.voices.filter(
              (x) => (!many || !at || baseLanguage(x.voice?.language) === at) && matches(x, q),
            );
            const title = `${ctx.familyTitle(g.family)} · ${g.voices.length} preset${g.voices.length === 1 ? "" : "s"}`;
            // a short family none of whose voices fit folds to one line with the reason (a long one keeps its
            // language chips, docs/mock/VoicePicker.dc.html)
            const foldable = !g.fits && !many;
            const open = !foldable || !!unfolded[g.family] || !!q.trim();
            return (
              <li key={g.family} className="flex flex-col gap-2">
                {foldable ? (
                  <button
                    type="button"
                    aria-expanded={open}
                    onClick={() => setUnfolded((m) => ({ ...m, [g.family]: !open }))}
                    className="flex h-10 items-center gap-2 rounded-xl border border-dashed border-s5 px-2.5 text-left text-t2"
                  >
                    <ChevronRight
                      size={16}
                      aria-hidden
                      className={`shrink-0 transition-transform ${open ? "rotate-90" : ""}`}
                    />
                    <span className="heading">{title}</span>
                    <span className="mono truncate text-[11px]">
                      {/* the board's words: "Ryan is for Qwen3-TTS" (the reason names the family's id) */}
                      {(reason(g.voices[0]) ?? "").replace(
                        /^voice (.+) is for \S+$/,
                        (_, name) => `${name} is for ${ctx.familyTitle(g.family)}`,
                      )}{" "}
                      · none of these fit {wfTitle}
                    </span>
                  </button>
                ) : (
                  <div className="flex flex-wrap items-baseline gap-2">
                    <h3 className="heading mr-1.5">{title}</h3>
                    {many ? (
                      <div role="group" aria-label="Language" className="flex flex-wrap gap-1">
                        {[["", "All"], ...langs.map((l) => [l, languageName(l, g.family)])].map(
                          ([l, name]) => (
                            <button
                              key={l}
                              type="button"
                              aria-pressed={at === l}
                              onClick={() => setLang((m) => ({ ...m, [g.family]: l }))}
                              className={`h-7 rounded-full px-2.5 text-[12px] ${at === l ? "bg-t1 font-semibold text-panel" : "bg-s3 font-medium"}`}
                            >
                              {name}
                            </button>
                          ),
                        )}
                      </div>
                    ) : (
                      langs.length > 1 && (
                        <span className="mono text-[11px] text-t2">
                          each with its native language
                        </span>
                      )
                    )}
                  </div>
                )}
                {open && (
                  <ul
                    aria-label={ctx.familyTitle(g.family)}
                    className={`m-0 grid list-none grid-cols-3 gap-2.5 p-0.5 ${many ? "max-h-[274px] overflow-y-auto" : foldable ? "max-h-[178px] overflow-y-auto" : ""}`}
                  >
                    {shown.map((x) => (
                      <li key={x.id} className={card(picked === x.id, reason(x))}>
                        {preview(x, `Play the preview of ${x.name}`)}
                        {pickButton(x, voiceLine(x))}
                      </li>
                    ))}
                  </ul>
                )}
              </li>
            );
          })
        : mine
            .filter((x) => matches(x, q))
            .map((x) => (
              <li key={x.id} className={card(picked === x.id, reason(x))}>
                {preview(x, `Play ${x.name}`)}
                {renaming === x.id ? (
                  <span className="flex min-w-0 flex-auto flex-col gap-1.5">
                    <span className="self-start rounded-full bg-s4 px-2 text-[11px] leading-[18px] font-semibold">
                      {x.group === "cloned" ? "Cloned" : "Designed"}
                    </span>
                    <input
                      aria-label="New name"
                      value={draft}
                      maxLength={80}
                      autoFocus
                      onChange={(e) => setDraft(e.target.value)}
                      onKeyDown={(e) => {
                        if (e.key === "Enter") {
                          e.preventDefault();
                          void rename(x);
                        }
                      }}
                      className="h-[30px] min-w-0 rounded-lg border border-a1 bg-s2 px-2 text-[13px] text-t1"
                    />
                    <span className="flex gap-1.5">
                      <button
                        type="button"
                        onClick={() => setRenaming(null)}
                        className="h-7 rounded-full bg-s3 px-3 text-[12px]"
                      >
                        Cancel
                      </button>
                      <button
                        type="button"
                        disabled={!draft.trim()}
                        onClick={() => void rename(x)}
                        className="h-7 rounded-full bg-a1 px-3 text-[12px] text-on1 disabled:opacity-50"
                      >
                        Save
                      </button>
                    </span>
                  </span>
                ) : confirming === x.id ? (
                  <span className="flex min-w-0 flex-auto flex-col gap-1.5">
                    <b className="text-[13px] font-semibold">{x.name}</b>
                    <small className="text-[12px] text-t2">
                      Delete {x.name}? Takes made with it keep it.
                    </small>
                    <span className="flex gap-1.5">
                      <button
                        type="button"
                        onClick={() => setConfirming(null)}
                        className="h-7 rounded-full bg-s3 px-3 text-[12px]"
                      >
                        Cancel
                      </button>
                      <button
                        type="button"
                        onClick={() => void remove(x)}
                        className="h-7 rounded-full bg-s3 px-3 text-[12px] text-bad"
                      >
                        Delete
                      </button>
                    </span>
                  </span>
                ) : (
                  <>
                    {pickButton(
                      x,
                      mineLine(x),
                      x.group === "cloned" ? "Cloned" : "Designed",
                      clips ? clipFitLine(x) : undefined,
                    )}
                    {/* the menu closes on Escape (focus back on ⋮) and when focus or a click leaves it */}
                    <span
                      className="contents"
                      onBlur={(e) => {
                        if (!e.currentTarget.contains(e.relatedTarget as Node | null))
                          setMenu(null);
                      }}
                      onKeyDown={(e) => {
                        if (e.key !== "Escape" || menu !== x.id) return;
                        e.preventDefault();
                        e.stopPropagation(); // the dialog stays open
                        setMenu(null);
                        e.currentTarget.querySelector<HTMLButtonElement>("button")?.focus();
                      }}
                    >
                      <button
                        type="button"
                        aria-label={`More for ${x.name}`}
                        aria-haspopup="menu"
                        aria-expanded={menu === x.id}
                        onClick={() => setMenu(menu === x.id ? null : x.id)}
                        className="grid size-7 shrink-0 place-items-center rounded-lg bg-s3 text-t2"
                      >
                        <MoreVertical size={14} aria-hidden />
                      </button>
                      {menu === x.id && (
                        <div
                          role="menu"
                          aria-label={x.name}
                          className="absolute top-11 right-2 z-10 flex w-[150px] flex-col gap-0.5 rounded-xl bg-s4 p-1.5 shadow-lg"
                        >
                          <button
                            type="button"
                            role="menuitem"
                            onClick={() => {
                              setMenu(null);
                              setConfirming(null);
                              setDraft(x.name);
                              setRenaming(x.id);
                            }}
                            className="h-9 rounded-lg px-2.5 text-left text-[13px] hover:bg-s5"
                          >
                            Rename
                          </button>
                          <button
                            type="button"
                            role="menuitem"
                            onClick={() => {
                              setMenu(null);
                              setRenaming(null);
                              setConfirming(x.id);
                            }}
                            className="h-9 rounded-lg px-2.5 text-left text-[13px] text-bad hover:bg-s5"
                          >
                            Delete
                          </button>
                        </div>
                      )}
                    </span>
                  </>
                )}
              </li>
            ))}
    </VisualPicker>
  );
}

/** Hear it (Design a voice, Clone from a clip): saves the draft voice (`save`), then runs `tries.workflow` with it
 * and the line in one session, polling until the take is there; closing the dialog cancels the try's run, and a
 * voice made while it was closing is deleted. */
function useTries(
  tries: Tries | undefined,
  draft: Item | null,
  setDraft: (x: Item | null) => void,
) {
  const [n, setN] = useState(0);
  const [clip, setClip] = useState<string | null>(null);
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  const alive = useRef(true);
  // the try's run while it runs: closing the dialog (which drops the draft) cancels it
  const runId = useRef<string | null>(null);
  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
      if (runId.current) void cancelRun(runId.current);
    };
  }, []);
  const fs = tries ? fieldsOf(tries.workflow) : [];
  const voiceKey = fs.find((f) => f.param.type === "voice")?.key;
  const lineKey = fs.find((f) => f.param.type === "text")?.key;

  async function hear(save: () => Promise<Item>, line: string) {
    if (!tries) return;
    setError("");
    setBusy("Saving…");
    try {
      const made = !draft;
      const x = await save();
      if (!alive.current) {
        // closed while saving: the dialog dropped the draft it knew, so a voice made just now goes too
        if (made) await deleteVoice(x.id);
        return;
      }
      setDraft(x);
      void reloadLibrary();
      setBusy("Speaking…");
      // with no session open the tries go to one "Voice design" session (made once), which Create does not switch to
      tries.stay?.();
      const sid = await tries.session();
      if (!alive.current) return;
      let run = await ok(
        api.POST("/v1/studio/sessions/{session_id}/runs", {
          params: { path: { session_id: sid } },
          body: {
            workflow_id: tries.workflow.id,
            inputs: {
              ...(voiceKey && { [voiceKey]: { voice_id: x.id } }),
              ...(lineKey && { [lineKey]: line }),
            },
          },
        }),
      );
      runId.current = run.id;
      if (!alive.current) return void cancelRun(run.id);
      // ponytail: polls the run each second; Create's session stream could carry it if this ever matters
      while (isActive(run.status) && alive.current) {
        await new Promise((r) => setTimeout(r, 1000));
        run = await ok(
          api.GET("/v1/studio/runs/{run_id}", { params: { path: { run_id: run.id } } }),
        );
      }
      if (!alive.current) return;
      runId.current = null;
      const out = run.outputs[0];
      if (run.status !== "succeeded" || !out)
        throw new Error(run.error ?? `The run ${run.status}.`);
      setClip(safeUrl(out.url) ?? null);
      setN((k) => k + 1);
    } catch (e) {
      if (alive.current) setError(message(e));
    } finally {
      if (alive.current) setBusy("");
    }
  }
  return { n, clip, busy, error, setError, hear };
}

/** A try's row: its clip's play button, the wave, "Try 2" and Try again. */
function TryRow({
  n,
  clip,
  disabled,
  onAgain,
}: {
  n: number;
  clip: string | null;
  disabled: boolean;
  onAgain: () => void;
}) {
  const { playing, toggle } = usePlayer();
  return (
    <div className="flex min-w-0 flex-auto items-center gap-2.5 rounded-xl bg-s1 px-3 py-2.5">
      {clip && (
        <PlayButton
          label={`Play try ${n}`}
          on={playing === "try"}
          onClick={() => toggle("try", clip)}
        />
      )}
      <span aria-hidden className="wave block h-9 min-w-0 flex-auto" />
      <span className="mono text-[12px] whitespace-nowrap text-t2">Try {n}</span>
      <button
        type="button"
        disabled={disabled}
        onClick={onAgain}
        className="h-8 rounded-full bg-s3 px-3 text-[12px] font-medium disabled:opacity-50"
      >
        Try again
      </button>
    </div>
  );
}

const fieldClass = "flex flex-col gap-1.5 text-[12px] text-t2";
const inputClass = "rounded-[10px] border-0 bg-s4 px-3 text-[13px] text-t1";

/** Design a voice: the description, a line to hear it say, Hear it (saves the description as a designed voice, then
 * runs the design workflow with it and the line), the try's clip, Try again, the name and Save as voice. */
function Designer({
  design,
  draft,
  setDraft,
  onCancel,
  onSaved,
}: {
  design: Tries;
  draft: Item | null;
  setDraft: (x: Item | null) => void;
  onCancel: () => void;
  onSaved: (x: Item) => void;
}) {
  const [text, setText] = useState("");
  const [line, setLine] = useState("");
  const [name, setName] = useState("");
  const t = useTries(design, draft, setDraft);
  const named = name.trim() || "Untitled voice";

  /** The draft voice as the form says it now: made on the first Hear it, edited when the words changed. */
  async function save(): Promise<Item> {
    const d = draft;
    const voice = { design: text.trim() };
    if (!d)
      return ok(
        api.POST("/v1/studio/library", {
          body: { kind: "voice", group: "designed", name: named, voice },
        }),
      );
    if (d.voice?.design === voice.design && d.name === named) return d;
    return ok(
      api.PUT("/v1/studio/library/{item_id}", {
        params: { path: { item_id: d.id } },
        body: { group: "designed", name: named, text: "", images: [], voice, version: d.version },
      }),
    );
  }

  async function keep() {
    t.setError("");
    try {
      const x = await save();
      void reloadLibrary();
      onSaved(x);
    } catch (e) {
      t.setError(message(e));
    }
  }

  const cannot = !!t.busy || !text.trim() || !line.trim();
  return (
    <div className="flex flex-col gap-3.5">
      <label className={fieldClass}>
        <span className="flex justify-between">
          Describe the voice
          <span className="mono">{text.length} / 2000</span>
        </span>
        <TextArea
          expand="Description"
          rows={3}
          maxLength={2000}
          value={text}
          placeholder="Male, about 60, low and warm, slow, a slight rasp"
          onChange={(e) => setText(e.target.value)}
          className={`${inputClass} resize-none py-2`}
        />
      </label>
      <label className={fieldClass}>
        A line to hear it say
        <input
          value={line}
          onChange={(e) => setLine(e.target.value)}
          className={`${inputClass} h-[38px]`}
        />
      </label>
      <div className="flex items-center gap-2">
        <button
          type="button"
          disabled={cannot}
          onClick={() => void t.hear(save, line)}
          className="bevel flex h-10 items-center gap-2 rounded-[10px] bg-a2 px-4 text-[13px] font-semibold text-[var(--on2)] disabled:opacity-50"
        >
          <Play size={14} aria-hidden className="fill-current" />
          Hear it
        </button>
        <span className="mono text-[12px] text-t2">{t.busy || design.label}</span>
      </div>
      {t.error && <ErrorNote>{t.error}</ErrorNote>}
      {t.n > 0 ? (
        <TryRow n={t.n} clip={t.clip} disabled={cannot} onAgain={() => void t.hear(save, line)} />
      ) : (
        <p className="m-0 text-[12px] text-t2">
          Hear it first. Saves the description; each line spoken from it may sound a little
          different.
        </p>
      )}
      <label className={fieldClass}>
        Name
        <input
          value={name}
          maxLength={80}
          placeholder="Untitled voice"
          onChange={(e) => setName(e.target.value)}
          className={`${inputClass} h-[38px]`}
        />
      </label>
      <p className="m-0 text-[12px] text-t2">
        Save as voice keeps the description. Each try speaks the description again.
      </p>
      <div className="flex items-center gap-2">
        <span className="grow" />
        <button
          type="button"
          onClick={onCancel}
          className="bevel h-9 rounded-[10px] bg-s4 px-3.5 text-[13px]"
        >
          Cancel
        </button>
        <button
          type="button"
          disabled={!t.n || !!t.busy}
          onClick={() => void keep()}
          className="bevel h-9 rounded-[10px] bg-a1 px-4 text-[13px] font-semibold text-on1 disabled:opacity-50"
        >
          Save as voice
        </button>
      </div>
    </div>
  );
}

/** "0:04.0" (the trim's In / Out). */
const tenths = (s: number) => `${Math.floor(s / 60)}:${(s % 60).toFixed(1).padStart(4, "0")}`;

/** A take's line in the clone view: "audio · wav · 28.0 s", "video sound · 5.0 s". */
const soundLine = (t: Take) => {
  const d = (factsOf(t).duration_s ?? 0).toFixed(1);
  return t.kind === "audio"
    ? `audio · ${audioFormat(t.content_type)} · ${d} s`
    : `video sound · ${d} s`;
};

/** Whether a take can be a voice's clip: audio or a video with sound, at least 3 s. */
const clippable = (t: Take) => pickable(CLIP, t) && !mediaFit(CLIP, factsOf(t));

/** The caller's newest audio takes and videos with sound that can be a clip (one page of each), newest first. */
function useClipTakes(): { takes: Take[] | null; error: string } {
  const [got, setGot] = useState<{ takes: Take[] | null; error: string }>({
    takes: null,
    error: "",
  });
  useEffect(() => {
    let stop = false;
    const page = (kind: "audio" | "video") =>
      ok(api.GET("/v1/studio/takes", { params: { query: { kind, limit: 24 } } }));
    Promise.all([page("audio"), page("video")])
      .then(([a, v]) => {
        const all = [...a.takes, ...v.takes].filter(clippable);
        remember(all);
        all.sort((x, y) =>
          x.created_at < y.created_at ? 1 : x.created_at > y.created_at ? -1 : 0,
        );
        if (!stop) setGot({ takes: all, error: "" });
      })
      .catch((e) => !stop && setGot({ takes: [], error: message(e) }));
    return () => {
      stop = true;
    };
  }, []);
  return got;
}

/** Clone from a clip (docs/mock/VoicePickerZonos3.dc.html; on a workflow that does not take clips,
 * VoicePicker.dc.html): a take (an audio take or a video's sound), its part trimmed over the waveform (3–30 s), the
 * name, the consent tick; where the open workflow clones, a line, Hear it (saves the voice, then runs the cloning
 * workflow with it) and Try again; Save voice. `start`: the take of Save as voice (a take a designed voice made saves
 * that designed voice with its clip). */
function Cloner({
  ctx,
  start,
  draft,
  setDraft,
  onCancel,
  onSaved,
}: {
  ctx: VoiceCtx;
  start?: Take;
  draft: Item | null;
  setDraft: (x: Item | null) => void;
  onCancel: () => void;
  onSaved: (x: Item) => void;
}) {
  const list = useClipTakes();
  // Upload a clip (docs/mock/UploadOptimise.dc.html frame 3): kept as it is, a wav stays WAV; it leads the list
  const [added, setAdded] = useState<Take[]>([]);
  const [up, setUp] = useState<{
    name: string;
    facts: string;
    tag: string;
    p: UploadProgress;
    stop: AbortController;
    error?: string;
  } | null>(null);
  const fileInput = useRef<HTMLInputElement>(null);
  const first = [...(start ? [start] : []), ...added];
  const takes = [...first, ...(list.takes ?? []).filter((t) => !first.some((x) => x.id === t.id))];
  const whole = (t: Take): S<"MediaRef"> => {
    const d = factsOf(t).duration_s ?? 0;
    return { take_id: t.id, in: 0, out: Math.min(d, CLIP.max_s ?? d) };
  };
  const [part, setPart] = useState<S<"MediaRef"> | null>(start ? whole(start) : null);
  function uploadClip(f: File) {
    const stop = new AbortController();
    const mine = (next: (u: NonNullable<typeof up>) => typeof up) =>
      setUp((u) => (u?.stop === stop ? next(u) : u));
    const facts = `${audioFormat(typeOf(f))} · ${(f.size / 1_048_576).toFixed(1)} MB, uploaded as it is.`;
    const tag = typeOf(f) === "audio/wav" ? "voice clip · kept as WAV" : "voice clip · as it is";
    // never optimised, so no Optimising step (docs/mock/UploadOptimise.dc.html frame 3)
    setUp({ name: f.name, facts, tag, p: { step: "uploading", f: 0 }, stop });
    uploadFile(f, (p) => mine((u) => ({ ...u, p })), undefined, {
      audio: true,
      asIs: true,
      signal: stop.signal,
    })
      .then((t) => {
        remember([t]);
        setAdded((a) => [t, ...a]);
        setPart(whole(t));
        mine(() => null);
      })
      .catch((e) => mine((u) => ({ ...u, error: message(e) })));
  }
  const take = takes.find((t) => t.id === part?.take_id);
  const designed = start && part?.take_id === start.id ? designedOf(start) : undefined;
  const [name, setName] = useState(designed?.name ?? "");
  const [line, setLine] = useState(SAMPLE_LINE);
  const [consent, setConsent] = useState(false);
  // Hear it where the open workflow clones a clip (ZONOS3); elsewhere the note says which model does
  const hears = takesClips(ctx.workflow) && !!ctx.clone;
  const t = useTries(hears ? ctx.clone : undefined, draft, setDraft);
  const facts = take ? factsOf(take) : null;
  const d = facts?.duration_s ?? 0;
  const a = part?.in ?? 0;
  const b = part?.out ?? 0;
  const err = part && take ? clipError(part, facts) : null;
  const named = name.trim() || "Untitled voice";
  const cloner = ctx.clone && ctx.familyTitle(ctx.clone.workflow.family);
  const edge = (e: "in" | "out", x: number) =>
    part && facts && setPart(trimmed(part, facts, CLIP, e, x));

  /** The voice as the form says it now: made on the first Hear it (Save as voice of the take's part), its clip and
   * name changed by a PUT after. */
  async function save(): Promise<Item> {
    const p = part!;
    const clip = { take_id: p.take_id, in: a, out: b };
    const group = designed ? "designed" : "cloned";
    let dr = draft;
    // the take changed away from (or back to) the designed voice's own: that draft is the wrong kind of voice, so it
    // goes (made in this dialog, never saved) and a new one is made
    if (dr && dr.group !== group) {
      await deleteVoice(dr.id);
      setDraft(null);
      dr = null;
    }
    if (!dr)
      return ok(
        api.POST("/v1/studio/library", {
          body: {
            kind: "voice",
            group,
            name: named,
            from_take: clip,
            voice: { consent: true },
          },
        }),
      );
    const c = dr.voice?.clip;
    if (dr.name === named && c?.take_id === clip.take_id && c.in === clip.in && c.out === clip.out)
      return dr;
    return ok(
      api.PUT("/v1/studio/library/{item_id}", {
        params: { path: { item_id: dr.id } },
        body: {
          group: dr.group,
          name: named,
          text: "",
          images: [],
          voice: { ...voiceBody(dr), clip, consent: true },
          version: dr.version,
        },
      }),
    );
  }

  async function keep() {
    t.setError("");
    try {
      const x = await save();
      void reloadLibrary();
      onSaved(x);
    } catch (e) {
      t.setError(message(e));
    }
  }

  const ready = !!take && !err && consent;
  const status =
    t.busy ||
    (!take
      ? "Pick a take"
      : !consent
        ? "Tick the box to save"
        : err
          ? "Trim to 3 – 30 s"
          : hears && !t.n
            ? "Hear it first"
            : "Ready");
  return (
    <div className="flex flex-col gap-3.5">
      <div className="flex flex-col gap-2">
        <span className="text-[12px] text-t2">
          An audio take, or a video&apos;s sound
          <Info>Voice clips are never converted: the clone reads the WAV.</Info>
        </span>
        {list.error && <ErrorNote>{list.error}</ErrorNote>}
        {list.takes && !takes.length && !up ? (
          <p className="m-0 text-[13px] text-t2">
            No audio takes or videos with sound of 3 s or more yet.
          </p>
        ) : (
          <ul
            aria-label="Takes"
            className="m-0 grid list-none auto-cols-[calc((100%-30px)/4)] grid-flow-col gap-2.5 overflow-x-auto p-0.5"
          >
            {up && !up.error && (
              <li className="relative flex flex-col gap-1">
                <span className="flex h-12 w-full flex-col justify-center gap-1 rounded-[10px] border border-dashed border-s5 px-2.5 text-[11px]">
                  <UploadStatus p={{ ...up.p, line: undefined }} name={up.name} />
                </span>
                <button
                  type="button"
                  aria-label={`Cancel the upload of ${up.name}`}
                  className="absolute top-1 right-1 grid size-5 place-items-center rounded-full bg-s3 text-t1"
                  onClick={() => {
                    up.stop.abort();
                    setUp(null);
                  }}
                >
                  <X size={10} aria-hidden />
                </button>
                <b className="truncate text-[13px] font-semibold">{up.name}</b>
                <small className="mono text-[11px] text-t2">{up.tag}</small>
              </li>
            )}
            {takes.map((x) => (
              <li key={x.id}>
                <button
                  type="button"
                  aria-pressed={x.id === part?.take_id}
                  // another take after a try: the next Hear it puts the new clip on the same draft
                  onClick={() => setPart(whole(x))}
                  className="flex w-full flex-col gap-1 text-left"
                >
                  <span
                    aria-hidden
                    className={`wave block h-12 w-full rounded-[10px] border ${x.id === part?.take_id ? "border-2 border-a1" : "border-dashed border-s5"}`}
                  />
                  <b className="truncate text-[13px] font-semibold">{x.title}</b>
                  <small className="mono text-[11px] text-t2">{soundLine(x)}</small>
                </button>
              </li>
            ))}
          </ul>
        )}
        <input
          ref={fileInput}
          type="file"
          accept={AUDIO_ACCEPT}
          hidden
          onChange={(e) => {
            const f = e.target.files?.[0];
            e.target.value = "";
            if (f) uploadClip(f);
          }}
        />
        <button
          type="button"
          className="h-10 rounded-[10px] border border-dashed border-dash bg-transparent px-3.5 text-[13px]"
          disabled={!!up && !up.error}
          onClick={() => fileInput.current?.click()}
        >
          Upload a clip
        </button>
        {up &&
          (up.error ? (
            <p role="alert" className="mono m-0 text-[12px] text-bad">
              {up.error}
            </p>
          ) : (
            <span className="text-[12px] text-t2">{up.facts}</span>
          ))}
      </div>
      {take && part && (
        <div className="flex flex-col gap-2">
          <div className="flex items-center justify-between gap-2 text-[12px] text-t2">
            <span className="flex items-center">
              The part to clone
              <Info>
                Drag in and out over the waveform: 3 to 30 s of one clear speaker, no music.
              </Info>
            </span>
            <span className="mono">{soundLine(take)}</span>
          </div>
          <RangeBar
            label={`Part to clone, in ${a.toFixed(1)} s, out ${b.toFixed(1)} s`}
            duration={d}
            value={[a, b]}
            step={0.1}
            onEdge={(e, x) => edge(e ? "out" : "in", x)}
          />
          <div className="mono flex justify-between text-[11px] text-t2">
            <span>0:00</span>
            <span>{tenths(d)}</span>
          </div>
          <div className="grid grid-cols-[1fr_1fr_auto] items-end gap-3">
            <label className={fieldClass}>
              In · {tenths(a)}
              <input
                type="range"
                min={0}
                max={d}
                step={0.1}
                value={a}
                onChange={(e) => edge("in", Number(e.target.value))}
                className="w-full accent-a2"
              />
            </label>
            <label className={fieldClass}>
              Out · {tenths(b)}
              <input
                type="range"
                min={0}
                max={d}
                step={0.1}
                value={b}
                onChange={(e) => edge("out", Number(e.target.value))}
                className="w-full accent-a2"
              />
            </label>
            <div className="flex min-w-[130px] flex-col rounded-[10px] bg-s1 px-3 py-1.5">
              <span className="text-[12px] text-t2">Length · 3 – 30 s</span>
              <b className={`mono text-[13px] ${err ? "text-bad" : ""}`}>{(b - a).toFixed(1)} s</b>
            </div>
          </div>
          {err && <span className="mono text-[12px] text-bad">{err}</span>}
          {cloner && hears && (
            <p className="m-0 text-[12px] text-t2">
              {cloner} makes a voice print from this part. The part is kept with the voice, so
              deleting the take later doesn&apos;t break it.
            </p>
          )}
        </div>
      )}
      <div className={hears ? "grid grid-cols-2 gap-3" : ""}>
        <label className={fieldClass}>
          Name
          <input
            value={name}
            maxLength={80}
            placeholder="Untitled voice"
            onChange={(e) => setName(e.target.value)}
            className={`${inputClass} h-[38px]`}
          />
        </label>
        {hears && (
          <label className={fieldClass}>
            A line to hear it say
            <input
              value={line}
              onChange={(e) => setLine(e.target.value)}
              className={`${inputClass} h-[38px]`}
            />
          </label>
        )}
      </div>
      {hears && (
        <div className="flex flex-wrap items-center gap-2">
          <button
            type="button"
            disabled={!ready || !!t.busy || !line.trim()}
            onClick={() => void t.hear(save, line)}
            className="bevel flex h-10 items-center gap-2 rounded-[10px] bg-a2 px-4 text-[13px] font-semibold text-[var(--on2)] disabled:opacity-50"
          >
            <Play size={14} aria-hidden className="fill-current" />
            Hear it
          </button>
          {t.n > 0 ? (
            <TryRow
              n={t.n}
              clip={t.clip}
              disabled={!ready || !!t.busy || !line.trim()}
              onAgain={() => void t.hear(save, line)}
            />
          ) : (
            <span className="text-[12px] text-t2">
              Hear the line in the cloned voice before you save
            </span>
          )}
        </div>
      )}
      {t.error && <ErrorNote>{t.error}</ErrorNote>}
      <label className="flex items-start gap-2.5 text-[13px]">
        <input
          type="checkbox"
          checked={consent}
          onChange={(e) => setConsent(e.target.checked)}
          className="mt-0.5 size-4 accent-a1"
        />
        <span className="flex flex-col">
          I have the right to use this voice
          <small className="mono text-[11px] text-t2">required · kept with the voice</small>
        </span>
      </label>
      {!hears && cloner && (
        <p className="m-0 text-[12px] text-t2">
          {cloner} clones this voice. {ctx.familyTitle(ctx.workflow.family)} presets cannot be
          cloned.
        </p>
      )}
      <div className="flex items-center gap-2">
        <span role="status" className="mono text-[12px] text-t2">
          {status}
        </span>
        <span className="grow" />
        <button
          type="button"
          onClick={onCancel}
          className="bevel h-9 rounded-[10px] bg-s4 px-3.5 text-[13px]"
        >
          Cancel
        </button>
        <button
          type="button"
          disabled={!ready || !!t.busy || (hears && !t.n)}
          onClick={() => void keep()}
          className="bevel h-9 rounded-[10px] bg-a1 px-4 text-[13px] font-semibold text-on1 disabled:opacity-50"
        >
          Save voice
        </button>
      </div>
    </div>
  );
}
