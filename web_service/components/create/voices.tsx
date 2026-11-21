"use client";
// Voices (docs/mock/VoicePicker.dc.html, CreateAudio.dc.html): a `voice` param's composer tile and inspector tile,
// and the voice picker: Presets (the live hosts' starter voices by family, each with its preview), Mine (designed
// voices: Rename, Delete), greyed with the fit's reason; and Design a voice (Hear it runs the workflow that designs
// a voice, then Save as voice). Clone from a clip and an element's Voice tile come with API 1.31 and 1.32.
import { useEffect, useRef, useState } from "react";
import { Mic, MoreVertical, Pause, Play, Wand2 } from "lucide-react";
import { ErrorNote, Modal } from "@/components/kit";
import { VisualPicker } from "@/components/create/visual-picker";
import { TileWords, tileClass } from "@/components/create/elements";
import { usePlayer } from "@/components/create/audio";
import { api, message, ok, type S } from "@/lib/api";
import { safeUrl } from "@/lib/format";
import { reloadLibrary, useVoices, voicesError } from "@/lib/library";
import { isActive } from "@/lib/runs";
import {
  languageName,
  matches,
  presetGroups,
  reasonFor,
  voiceLine,
  voiceTileText,
} from "@/lib/voices";
import { fields as fieldsOf, isVoice, type Field, type Raw } from "@/lib/workflow";

type Item = S<"LibraryItem">;
type Workflow = S<"Workflow">;

/** What a voice picker needs from Create: the open workflow (the fit), and for Design a voice the workflow that
 * designs one, its name, and the session its runs go to (none yet: a "Voice design" session is made). */
export type VoiceCtx = {
  workflow: Pick<Workflow, "id" | "title" | "nodes" | "node_schemas">;
  familyTitle: (family: string) => string;
  /** `stay`: keeps Create on the open workflow while the tries land in its session (else the newest run's
   * workflow, the design one, would open under the dialog) */
  design?: {
    workflow: Workflow;
    label: string;
    /** the session the tries go to: the open one, else one "Voice design" session made once and kept */
    session: () => Promise<string>;
    stay?: () => void;
  };
};

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
}: {
  f: Field;
  raws: Record<string, Raw>;
  onChange: (key: string, raw: Raw) => void;
  ctx: VoiceCtx;
  error?: string;
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

/** The voice picker (docs/mock/VoicePicker.dc.html): Presets and Mine, or Design a voice. */
export function VoicePicker({
  open,
  onClose,
  value,
  onPick,
  ctx,
}: {
  open: boolean;
  onClose: () => void;
  value: string;
  onPick: (voiceId: string) => void;
  ctx: VoiceCtx;
}) {
  const [view, setView] = useState<"list" | "design">("list");
  // a designed voice Hear it made here and not saved: Cancel deletes it
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
  // each opening starts on the voice the param holds
  const [was, setWas] = useState(open);
  if (was !== open) {
    setWas(open);
    if (open) setPicked(value);
  }
  return (
    <Modal
      open={open}
      onClose={close}
      title={view === "design" ? "Design a voice" : "Voice"}
      width="w-[min(94vw,880px)]"
      back={
        view === "design"
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
          onCancel={close}
          onUse={(id) => {
            onPick(id);
            close();
          }}
        />
      ) : (
        ctx.design && (
          <Designer
            design={ctx.design}
            draft={draft}
            setDraft={setDraft}
            onCancel={close}
            onSaved={(x) => {
              setDraft(null);
              setPicked(x.id);
              setTab("mine");
              setView("list");
            }}
          />
        )
      )}
    </Modal>
  );
}

function VoiceList({
  ctx,
  picked,
  setPicked,
  tab,
  setTab,
  onDesign,
  onCancel,
  onUse,
}: {
  ctx: VoiceCtx;
  picked: string;
  setPicked: (id: string) => void;
  tab: "presets" | "mine";
  setTab: (t: "presets" | "mine") => void;
  onDesign?: () => void;
  onCancel: () => void;
  onUse: (id: string) => void;
}) {
  const [items, setItems] = useState<Item[] | null>(null);
  const [error, setError] = useState("");
  const [q, setQ] = useState("");
  const [lang, setLang] = useState<Record<string, string>>({});
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
        params: { query: { kind: "voice", workflow: wf.id } },
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
  const groups = presetGroups(all);
  const mine = all.filter((x) => x.source === "mine");
  const presets = groups.reduce((n, g) => n + g.voices.length, 0);
  const reason = (x: Item) => reasonFor(wf, x);
  const pv = all.find((x) => x.id === picked);
  const pickable = pv && !reason(pv) ? pv : undefined;
  const fail = (e: unknown) => setError(message(e));

  const preview = (x: Item, label: string) => {
    const url = safeUrl(x.thumb_url);
    return url ? (
      <PlayButton label={label} on={playing === x.id} onClick={() => toggle(x.id, url)} />
    ) : null;
  };
  const pickButton = (x: Item, line: string, badge?: string) => {
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
        {r && (
          <em id={why} className="mono truncate text-[11px] text-t2 not-italic">
            {r}
          </em>
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
            voice: {
              design: x.voice?.design ?? "",
              ...(x.voice?.language && { language: x.voice.language }),
              ...(x.voice?.style && { style: x.voice.style }),
            },
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
      No voices of your own yet{onDesign ? ": design one from words." : "."}
    </p>
  ) : tab === "presets" && !presets ? (
    <p className="text-[13px] text-t2">No runner publishes starter voices now.</p>
  ) : null;

  return (
    <VisualPicker<"presets" | "mine", never>
      info="Presets come with the models; play one to hear it. Yours are voices you designed from words. Saved voices count toward the 200 items you can keep."
      action={
        onDesign && (
          <button
            type="button"
            onClick={onDesign}
            className="flex h-8 items-center gap-1.5 rounded-full bg-s3 px-3 text-[12px] font-semibold"
          >
            <Wand2 size={14} aria-hidden />
            Design a voice
          </button>
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
            ["mine", `Mine · ${mine.length}`],
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
            const langs = [...new Set(g.voices.map((x) => x.voice?.language ?? ""))].filter(
              Boolean,
            );
            const many = g.voices.length > ROWS_OF_THREE;
            // a long family opens on English when it has some, else on all
            const at = lang[g.family] ?? (langs.includes("en") ? "en" : "");
            const shown = g.voices.filter(
              (x) => (!many || !at || x.voice?.language === at) && matches(x, q),
            );
            const title = `${ctx.familyTitle(g.family)} · ${g.voices.length} preset${g.voices.length === 1 ? "" : "s"}`;
            return (
              <li key={g.family} className="flex flex-col gap-2">
                <div className="flex flex-wrap items-baseline gap-2">
                  <h3 className="heading mr-1.5">{title}</h3>
                  {many ? (
                    <div role="group" aria-label="Language" className="flex flex-wrap gap-1">
                      {[["", "All"], ...langs.map((l) => [l, languageName(l)])].map(([l, name]) => (
                        <button
                          key={l}
                          type="button"
                          aria-pressed={at === l}
                          onClick={() => setLang((m) => ({ ...m, [g.family]: l }))}
                          className={`h-7 rounded-full px-2.5 text-[12px] ${at === l ? "bg-t1 font-semibold text-panel" : "bg-s3 font-medium"}`}
                        >
                          {name}
                        </button>
                      ))}
                    </div>
                  ) : (
                    langs.length > 1 && (
                      <span className="mono text-[11px] text-t2">
                        each with its native language
                      </span>
                    )
                  )}
                </div>
                <ul
                  aria-label={ctx.familyTitle(g.family)}
                  className={`m-0 grid list-none grid-cols-3 gap-2.5 p-0.5 ${many ? "max-h-[178px] overflow-y-auto" : ""}`}
                >
                  {shown.map((x) => (
                    <li key={x.id} className={card(picked === x.id, reason(x))}>
                      {preview(x, `Play the preview of ${x.name}`)}
                      {pickButton(x, voiceLine(x))}
                    </li>
                  ))}
                </ul>
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
                      Designed
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
                    {pickButton(x, voiceLine(x), "Designed")}
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

/** Design a voice: the description, a line to hear it say, Hear it (saves the description as a designed voice, then
 * runs the design workflow with it and the line), the try's clip, Try again, the name and Save as voice. */
function Designer({
  design,
  draft,
  setDraft,
  onCancel,
  onSaved,
}: {
  design: NonNullable<VoiceCtx["design"]>;
  draft: Item | null;
  setDraft: (x: Item | null) => void;
  onCancel: () => void;
  onSaved: (x: Item) => void;
}) {
  const [text, setText] = useState("");
  const [line, setLine] = useState("");
  const [name, setName] = useState("");
  const [tries, setTries] = useState(0);
  const [clip, setClip] = useState<string | null>(null);
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  const { playing, toggle } = usePlayer();
  const alive = useRef(true);
  // the try's run while it runs: closing the dialog (which drops the draft) cancels it
  const runId = useRef<string | null>(null);
  useEffect(
    () => () => {
      alive.current = false;
      if (runId.current) void cancelRun(runId.current);
    },
    [],
  );
  const fs = fieldsOf(design.workflow);
  const voiceKey = fs.find((f) => f.param.type === "voice")?.key;
  const lineKey = fs.find((f) => f.param.type === "text")?.key;
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

  async function hear() {
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
      design.stay?.();
      const sid = await design.session();
      if (!alive.current) return;
      let run = await ok(
        api.POST("/v1/studio/sessions/{session_id}/runs", {
          params: { path: { session_id: sid } },
          body: {
            workflow_id: design.workflow.id,
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
      setTries((n) => n + 1);
    } catch (e) {
      if (alive.current) setError(message(e));
    } finally {
      if (alive.current) setBusy("");
    }
  }

  async function keep() {
    setError("");
    try {
      const x = await save();
      void reloadLibrary();
      onSaved(x);
    } catch (e) {
      setError(message(e));
    }
  }

  const field = "flex flex-col gap-1.5 text-[12px] text-t2";
  const input = "rounded-[10px] border-0 bg-s4 px-3 text-[13px] text-t1";
  return (
    <div className="flex flex-col gap-3.5">
      <label className={field}>
        <span className="flex justify-between">
          Describe the voice
          <span className="mono">{text.length} / 2000</span>
        </span>
        <textarea
          rows={3}
          maxLength={2000}
          value={text}
          placeholder="Male, about 60, low and warm, slow, a slight rasp"
          onChange={(e) => setText(e.target.value)}
          className={`${input} resize-none py-2`}
        />
      </label>
      <label className={field}>
        A line to hear it say
        <input
          value={line}
          onChange={(e) => setLine(e.target.value)}
          className={`${input} h-[38px]`}
        />
      </label>
      <div className="flex items-center gap-2">
        <button
          type="button"
          disabled={!!busy || !text.trim() || !line.trim()}
          onClick={() => void hear()}
          className="bevel flex h-10 items-center gap-2 rounded-[10px] bg-a2 px-4 text-[13px] font-semibold text-[var(--on2)] disabled:opacity-50"
        >
          <Play size={14} aria-hidden className="fill-current" />
          Hear it
        </button>
        <span className="mono text-[12px] text-t2">{busy || design.label}</span>
      </div>
      {error && <ErrorNote>{error}</ErrorNote>}
      {tries > 0 ? (
        <div className="flex items-center gap-2.5 rounded-xl bg-s1 px-3 py-2.5">
          {clip && (
            <PlayButton
              label={`Play try ${tries}`}
              on={playing === "try"}
              onClick={() => toggle("try", clip)}
            />
          )}
          <span aria-hidden className="wave block h-9 min-w-0 flex-auto" />
          <span className="mono text-[12px] whitespace-nowrap text-t2">Try {tries}</span>
          <button
            type="button"
            disabled={!!busy || !text.trim() || !line.trim()}
            onClick={() => void hear()}
            className="h-8 rounded-full bg-s3 px-3 text-[12px] font-medium disabled:opacity-50"
          >
            Try again
          </button>
        </div>
      ) : (
        <p className="m-0 text-[12px] text-t2">
          Hear it first. Saves the description; each line spoken from it may sound a little
          different.
        </p>
      )}
      <label className={field}>
        Name
        <input
          value={name}
          maxLength={80}
          placeholder="Untitled voice"
          onChange={(e) => setName(e.target.value)}
          className={`${input} h-[38px]`}
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
          disabled={!tries || !!busy}
          onClick={() => void keep()}
          className="bevel h-9 rounded-[10px] bg-a1 px-4 text-[13px] font-semibold text-on1 disabled:opacity-50"
        >
          Save as voice
        </button>
      </div>
    </div>
  );
}
