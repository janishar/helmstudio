"use client";
// A prompt textarea with `@` mentions (docs/mock/Elements.dc.html): a real textarea over a highlight layer that
// draws each mention as a chip (an unknown handle marked), and the `@` menu at the caret (GET
// /v1/studio/library?kind=element&q=…&limit=8). The value stays plain text with `@handle`.
import {
  useEffect,
  useId,
  useLayoutEffect,
  useRef,
  useState,
  type TextareaHTMLAttributes,
} from "react";
import {
  ArrowLeftRight,
  AtSign,
  MapPin,
  Package,
  Pencil,
  Plus,
  Trash2,
  Type,
  UserRound,
} from "lucide-react";
import { openMenu } from "@/components/context-menu";
import { ElementEditor } from "@/components/create/elements";
import { Expand } from "@/components/expand-text";
import { api, ok, type S } from "@/lib/api";
import { safeUrl } from "@/lib/format";
import { groupLabel, reloadLibrary, useLibrary } from "@/lib/library";
import { insertMention, mentionAt, mentionRanges, mentioned, scan } from "@/lib/mentions";

type Item = S<"LibraryItem">;

type Props = Omit<TextareaHTMLAttributes<HTMLTextAreaElement>, "value" | "onChange"> & {
  value: string;
  onChange: (v: string) => void;
  /** the wrapper's classes (it takes the textarea's place in the layout) */
  wrapClassName?: string;
  /** an Expand button that opens the same text in a dialog titled this (docs/mock/CreateAudio.dc.html, "The prompt,
   * expanded"): shown while the text overflows, or on focus */
  expand?: string;
};

/** The handles the library knows, or null while it is not known (nothing is marked then). */
function useKnown() {
  const lib = useLibrary();
  return lib && new Set(lib.map((e) => e.handle));
}

export function PromptArea({
  value,
  onChange,
  className,
  wrapClassName,
  onKeyDown,
  onBlur,
  expand,
  ...rest
}: Props) {
  const known = useKnown();
  const lib = useLibrary();
  // an @ chip's Edit @x or New element @x (ContextMenus frame 7)
  const [editing, setEditing] = useState<{ item?: Item; handle?: string } | null>(null);
  const ta = useRef<HTMLTextAreaElement>(null);
  const back = useRef<HTMLDivElement>(null);
  const menu = useRef<HTMLDivElement>(null);
  const uid = "m" + useId().replace(/[^a-zA-Z0-9]/g, "");
  // the caret while focused (null: not focused), and the token whose menu Escape closed
  const [caret, setCaret] = useState<number | null>(null);
  const [dismissed, setDismissed] = useState<number | null>(null);
  const at = caret == null ? null : mentionAt(value, caret);
  const q = at && at.start !== dismissed ? at.query : null;
  const [found, setFound] = useState<{ q: string; items: Item[] } | null>(null);
  const [active, setActive] = useState(0);
  useEffect(() => {
    if (q == null) return;
    let stop = false;
    const t = setTimeout(
      () =>
        ok(api.GET("/v1/studio/library", { params: { query: { kind: "element", q, limit: 8 } } }))
          .then((l) => {
            if (stop) return;
            setFound({ q, items: l.items });
            setActive(0);
          })
          .catch(() => undefined),
      found ? 80 : 0,
    );
    return () => {
      stop = true;
      clearTimeout(t);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps -- only the query starts a search
  }, [q]);
  const items = q != null && found ? found.items : [];
  const open = q != null && !!found;

  useLayoutEffect(() => {
    const m = menu.current;
    if (!m) return;
    if (open && !m.matches(":popover-open")) m.showPopover();
    if (!open && m.matches(":popover-open")) m.hidePopover();
  }, [open]);

  function pick(it: Item) {
    if (!at || !it.handle) return;
    // the menu searched the server: a handle this tab's list does not have yet is read again, not marked unknown
    if (known && !known.has(it.handle)) void reloadLibrary();
    setCaret(typeMention(ta.current, value, at.start, at.end, it.handle, onChange));
  }

  /** Puts `text` over [start, end) as typing would (⌘Z takes it back). */
  function typeOver(start: number, end: number, text: string) {
    const el = ta.current;
    el?.focus();
    el?.setSelectionRange(start, end);
    const done = (() => {
      try {
        return (
          !!el &&
          el.value === value &&
          document.execCommand(text ? "insertText" : "delete", false, text)
        );
      } catch {
        return false;
      }
    })();
    if (!done) onChange(value.slice(0, start) + text + value.slice(end));
  }
  /** A right-click on an @ chip (drawn under the textarea): the chip's menu; elsewhere the text field's. */
  function chipMenu(e: React.MouseEvent) {
    const hit = [...(back.current?.querySelectorAll<HTMLElement>("[data-start]") ?? [])].find(
      (el) =>
        [...el.getClientRects()].some(
          (r) =>
            e.clientX >= r.left &&
            e.clientX <= r.right &&
            e.clientY >= r.top &&
            e.clientY <= r.bottom,
        ),
    );
    const r = hit && ranges.find((x) => x.start === Number(hit.dataset.start));
    if (!r) return;
    const item = lib?.find((x) => x.handle === r.handle);
    const others = (lib ?? []).filter((x) => x.handle && x.handle !== r.handle);
    openMenu(e, {
      title: item ? `@${r.handle} · ${item.name}` : `@${r.handle} · no element`,
      items: [
        item
          ? { label: `Edit @${r.handle}`, icon: Pencil, onPick: () => setEditing({ item }) }
          : {
              label: `New element @${r.handle}`,
              icon: Plus,
              onPick: () => setEditing({ handle: r.handle }),
            },
        {
          label: "Replace with",
          icon: ArrowLeftRight,
          off: others.length ? undefined : "No other elements yet",
          items: others.map((x) => ({
            label: `@${x.handle}`,
            icon: AtSign,
            onPick: () =>
              setCaret(typeMention(ta.current, value, r.start, r.end, x.handle!, onChange)),
          })),
        },
        { label: "Write as text", icon: Type, onPick: () => typeOver(r.start, r.start, "@") },
        "-",
        {
          label: "Remove from prompt",
          icon: Trash2,
          danger: true,
          onPick: () => typeOver(r.start, r.end, ""),
        },
      ],
    });
  }

  // the highlight layer: the text cut at each mention and at the open token's @ (the menu's anchor)
  const ranges = mentionRanges(value);
  const cuts = [
    ...new Set([
      0,
      value.length,
      ...ranges.flatMap((r) => [r.start, r.end]),
      ...(open && at ? [at.start] : []),
    ]),
  ].sort((a, b) => a - b);
  const opt = (i: number) => `${uid}-${i}`;
  return (
    // data-mentions: the text field's menu has Insert @… (components/context-menu.tsx)
    <div className={`group relative ${wrapClassName ?? ""}`} data-mentions>
      <div
        ref={back}
        aria-hidden
        className={className}
        style={{
          position: "absolute",
          inset: 0,
          height: "auto",
          overflow: "hidden",
          whiteSpace: "pre-wrap",
          overflowWrap: "break-word",
          color: "transparent",
          pointerEvents: "none",
          // the same gutter on both layers, so they wrap alike when the textarea scrolls
          scrollbarGutter: "stable",
        }}
      >
        {cuts.slice(0, -1).map((a, i) => {
          const b = cuts[i + 1];
          const r = ranges.find((x) => x.start <= a && b <= x.end);
          // the token being typed is not marked yet
          const bad = r && known && !known.has(r.handle) && r.start !== at?.start;
          return (
            <span
              key={a}
              data-start={r?.start}
              className={r ? (bad ? "mention-chip mention-unknown" : "mention-chip") : undefined}
              style={
                open && at?.start === a
                  ? ({ anchorName: `--${uid}` } as React.CSSProperties)
                  : undefined
              }
            >
              {value.slice(a, b)}
            </span>
          );
        })}
        {/* a textarea draws a line after a final newline, a div does not: keep the layers the same height */}
        {value.endsWith("\n") && "\u200b"}
      </div>
      <textarea
        {...rest}
        ref={ta}
        role="combobox"
        aria-expanded={open}
        aria-controls={uid}
        aria-autocomplete="list"
        aria-activedescendant={open && items.length ? opt(active) : undefined}
        className={className}
        style={{
          ...rest.style,
          position: "relative",
          // block: an inline textarea leaves a gap under it, which made the colour layer 5px taller and drift
          display: "block",
          background: "transparent",
          scrollbarGutter: "stable",
        }}
        value={value}
        onChange={(e) => {
          onChange(e.target.value);
          setCaret(e.target.selectionStart);
          // Escape closed the menu of one token only: once it is gone or another one, a new @ opens it again
          if (mentionAt(e.target.value, e.target.selectionStart)?.start !== dismissed)
            setDismissed(null);
        }}
        onSelect={(e) => setCaret(e.currentTarget.selectionStart)}
        onContextMenu={chipMenu}
        onFocus={(e) => setCaret(e.currentTarget.selectionStart)}
        onBlur={(e) => {
          setCaret(null);
          setDismissed(null);
          onBlur?.(e);
        }}
        onScroll={(e) => {
          if (back.current) back.current.scrollTop = e.currentTarget.scrollTop;
        }}
        onKeyDown={(e) => {
          // an IME's Enter commits what is being composed: never the menu's
          if (e.nativeEvent.isComposing || e.keyCode === 229) return onKeyDown?.(e);
          if (open && e.key === "Escape") {
            e.preventDefault();
            e.stopPropagation();
            setDismissed(at?.start ?? null);
            return;
          }
          if (open && items.length && !e.metaKey && !e.ctrlKey && !e.altKey) {
            const n = items.length;
            if (e.key === "ArrowDown" || e.key === "ArrowUp") {
              e.preventDefault();
              setActive((i) => (i + (e.key === "ArrowDown" ? 1 : n - 1)) % n);
              return;
            }
            if ((e.key === "Enter" && !e.shiftKey) || e.key === "Tab") {
              e.preventDefault();
              pick(items[Math.min(active, n - 1)]);
              return;
            }
          }
          onKeyDown?.(e);
        }}
      />
      <div
        ref={menu}
        id={uid}
        popover="manual"
        role="listbox"
        aria-label="Elements"
        className="pop w-[300px] flex-col [&:popover-open]:flex gap-0.5 border border-s5 p-2"
        style={
          {
            positionAnchor: `--${uid}`,
            positionArea: "top span-right",
            positionTryFallbacks: "flip-block",
            marginBlock: 6,
          } as React.CSSProperties
        }
      >
        {items.map((it, i) => (
          <div
            key={it.id}
            id={opt(i)}
            role="option"
            aria-selected={i === active}
            tabIndex={-1}
            className={`flex cursor-pointer items-center gap-2.5 rounded-[10px] px-2 py-1.5 ${i === active ? "bg-s4" : ""}`}
            // keep the focus (and the caret) in the textarea
            onMouseDown={(e) => e.preventDefault()}
            onMouseEnter={() => setActive(i)}
            onClick={() => pick(it)}
          >
            {it.thumb_url ? (
              <img
                loading="lazy"
                decoding="async"
                src={safeUrl(it.thumb_url)}
                alt=""
                className="size-8 shrink-0 rounded-md object-cover"
              />
            ) : (
              <span
                aria-hidden
                className="grid size-8 shrink-0 place-items-center rounded-md border border-dashed border-dash bg-s1 text-t2"
              >
                {it.group === "location" ? (
                  <MapPin size={14} />
                ) : it.group === "prop" ? (
                  <Package size={14} />
                ) : (
                  <UserRound size={14} />
                )}
              </span>
            )}
            <span className="flex min-w-0 flex-col">
              <b className="mono truncate text-[13px] font-medium">@{it.handle}</b>
              <small className="truncate text-[12px] text-t2">
                {it.name} · {groupLabel(it.group)}
              </small>
            </span>
          </div>
        ))}
        {!items.length && (
          <span className="px-2 py-1.5 text-[13px] text-t2">
            {q ? `No element starts with @${q}.` : "No elements yet: make one from Elements."}
          </span>
        )}
        <span className="px-2 pt-1.5 pb-0.5 text-[11px] text-t2">
          ↑↓ choose · Enter or Tab inserts · Esc closes · @@ types a literal @
        </span>
      </div>
      {editing && (
        <ElementEditor
          open
          onClose={() => setEditing(null)}
          item={editing.item}
          handle={editing.handle}
          onSaved={() => void reloadLibrary()}
        />
      )}
      {expand && (
        <Expand
          title={expand}
          value={value}
          box={ta}
          maxLength={rest.maxLength}
          placeholder={rest.placeholder}
          mentions
          editor={(p) => (
            <PromptArea
              {...p}
              // a mono box (the dialogue script) stays mono in the dialog
              className={`${p.className} ${/\bmono\b/.test(className ?? "") ? "mono" : ""}`}
              value={value}
              onChange={onChange}
            />
          )}
        />
      )}
    </div>
  );
}

/** The composer's mention chips (the mock's `@fox` `@snowfield`): each distinct handle once, an unknown one marked. */
export function MentionChips({ texts }: { texts: string[] }) {
  const known = useKnown();
  return [...new Set(texts.flatMap(mentioned))].map((h) => {
    const bad = known && !known.has(h);
    return (
      <span
        key={h}
        title={
          bad ? `No element @${h}: make it in Elements, or write @@${h} for the text` : undefined
        }
        className={`mono self-center justify-self-start rounded-lg bg-s5 px-2.5 py-1 text-[12px] ${bad ? "text-bad underline decoration-wavy" : ""}`}
      >
        @{h}
      </span>
    );
  });
}

/** Text with its mentions drawn as chips (a batch table's prompt cells). */
export function MentionText({ text }: { text: string }) {
  const known = useKnown();
  return scan(text).map((p, i) => {
    const raw = "text" in p ? p.text : p.raw;
    if ("text" in p) return <span key={i}>{raw}</span>;
    const bad = known && !known.has(p.mention);
    return (
      <span
        key={i}
        className={`mono text-[12px] ${bad ? "mention-chip mention-unknown" : "mention-chip"}`}
      >
        {raw}
      </span>
    );
  });
}

/** Puts `@handle` over [start, end) of the textarea as typing would, so the browser's undo takes it back (its input
 * event reaches `set` through onChange); without the textarea or the command, through `set`. Answers the caret. */
function typeMention(
  el: HTMLTextAreaElement | null,
  value: string,
  start: number,
  end: number,
  handle: string,
  set: (v: string) => void,
) {
  const r = insertMention(value, start, end, handle);
  el?.focus();
  el?.setSelectionRange(start, end);
  // ponytail: execCommand is deprecated, yet the one way to keep the native undo stack of a textarea
  if (
    !el ||
    el.value !== value ||
    !document.execCommand("insertText", false, r.text.slice(start, r.caret))
  ) {
    set(r.text);
    requestAnimationFrame(() => el?.setSelectionRange(r.caret, r.caret));
  }
  return r.caret;
}

/** Puts `@handle` into the textarea `id` at its caret (or over its selection), and focuses it. */
export function insertAtCaret(id: string, value: string, handle: string, set: (v: string) => void) {
  const el = document.getElementById(id) as HTMLTextAreaElement | null;
  const s = el?.selectionStart ?? value.length;
  typeMention(el, value, s, el?.selectionEnd ?? s, handle, set);
}

/** `[tag]` put into `value` at [start, end), on a line of its own: the text and where the caret goes after it. */
export function withTag(value: string, start: number, end: number, tag: string) {
  const before = value.slice(0, start);
  const after = value.slice(end);
  const lead = before === "" || before.endsWith("\n") ? "" : "\n";
  const text = `${lead}[${tag}]\n`;
  return { text: before + text + after.replace(/^\n/, ""), caret: start + text.length };
}

/** The Lyrics box under a song's style (docs/mock/CreateMusicSound.dc.html frame 1, LyricsBox in the Design System):
 * the heading, one chip per section tag the model knows (`tags`, each put in at the caret as `[tag]`), Instrumental
 * at the end of the bar (on: the text goes, as its `when` says), and the text with its tag lines in the accent. */
export function LyricsBox({
  id,
  label,
  tags,
  value,
  onChange,
  instrumental,
  hidden,
  error,
}: {
  id: string;
  label: string;
  tags: string[];
  value: string;
  onChange: (v: string) => void;
  /** the Instrumental switch, when the model has one */
  instrumental?: { on: boolean; set: (on: boolean) => void };
  /** Instrumental is on: the text is not sent, so not shown */
  hidden?: boolean;
  error?: string;
}) {
  const ta = useRef<HTMLTextAreaElement>(null);
  const layer = useRef<HTMLDivElement>(null);
  const put = (tag: string) => {
    const el = ta.current;
    const s = el?.selectionStart ?? value.length;
    const e = el?.selectionEnd ?? s;
    const r = withTag(value, s, e, tag);
    // typed into the textarea itself (the native undo keeps it, and the caret is right at once, so keys pressed
    // straight after land after the tag); the state follows from its input event
    el?.focus();
    el?.setSelectionRange(s, Math.min(value.length, e + (value.slice(e).startsWith("\n") ? 1 : 0)));
    const inserted = r.text.slice(s, r.caret);
    let done = false;
    try {
      done = !!el && el.value === value && document.execCommand("insertText", false, inserted);
    } catch {
      done = false;
    }
    if (!done) {
      onChange(r.text);
      requestAnimationFrame(() => el?.setSelectionRange(r.caret, r.caret));
    }
  };
  const text = "text-[14px] leading-[21px] whitespace-pre-wrap break-words";
  return (
    // the lyrics take twice the style's share of the composer's free height (grow-2), 6 lines the least; not while
    // Instrumental hides them
    <div
      className={`flex flex-col gap-1.5 rounded-[10px] border border-s4 bg-s1 px-2.5 py-2 ${hidden ? "" : "grow-[2]"}`}
    >
      <div
        role="group"
        aria-label={`${label} tags`}
        className="flex flex-wrap items-center gap-1.5"
      >
        <span className="heading mr-1">{label}</span>
        {!hidden &&
          tags.map((t) => (
            <button
              key={t}
              type="button"
              title={`Put [${t}] at the cursor`}
              onClick={() => put(t)}
              className="mono h-[26px] rounded-[7px] border border-dashed border-dash bg-transparent px-2 text-[11px] text-t2"
            >
              [{t}]
            </button>
          ))}
        {instrumental && (
          <label className="ml-auto flex items-center gap-2 text-[13px] text-t2">
            Instrumental
            <button
              type="button"
              role="switch"
              aria-checked={instrumental.on}
              aria-label="Instrumental"
              onClick={() => instrumental.set(!instrumental.on)}
              className="flex h-[22px] w-9 shrink-0 rounded-full border border-line p-0.5"
              style={{ background: instrumental.on ? "var(--a2)" : "var(--s1)" }}
            >
              <span
                className="block size-4 rounded-full transition-transform"
                style={{
                  background: instrumental.on ? "var(--on2)" : "var(--t2)",
                  transform: instrumental.on ? "translateX(14px)" : undefined,
                }}
              />
            </button>
          </label>
        )}
      </div>
      {!hidden && (
        <div className="group relative flex grow flex-col">
          {/* the tag lines in the accent: a layer under a see-through textarea, same font and gutter so the lines
              wrap alike; it follows the textarea's scroll (up to 10 lines, then it scrolls; Expand opens it big) */}
          <div
            ref={layer}
            aria-hidden
            className={`${text} pointer-events-none absolute inset-0 overflow-hidden pr-9 text-t1`}
            style={{ scrollbarGutter: "stable" }}
          >
            {value.split("\n").map((l, i) => (
              <div key={i} className={/^\s*\[[^\]]+\]\s*$/.test(l) ? "text-a2" : undefined}>
                {l || " "}
              </div>
            ))}
          </div>
          <textarea
            ref={ta}
            id={id}
            aria-label={label}
            // 6 lines the least, then the composer's free height: more scrolls (the layer follows), Expand opens it big
            rows={6}
            value={value}
            placeholder="[verse] the words, a line each"
            aria-invalid={!!error || undefined}
            style={
              {
                height: 126,
                scrollbarGutter: "stable",
              } as React.CSSProperties
            }
            onChange={(e) => onChange(e.target.value)}
            onScroll={(e) => {
              if (layer.current) layer.current.scrollTop = e.currentTarget.scrollTop;
            }}
            onKeyDown={(e) => {
              if (e.key === "Enter" && (e.metaKey || e.ctrlKey))
                e.currentTarget.form?.requestSubmit();
            }}
            className={`${text} relative block w-full shrink-0 grow resize-none overflow-y-auto border-0 bg-transparent p-0 pr-9 text-transparent caret-t1 outline-0 placeholder:text-t2`}
          />
          <Expand
            title={label}
            value={value}
            box={ta}
            placeholder="[verse] the words, a line each"
            editor={(p) => (
              <textarea {...p} value={value} onChange={(e) => onChange(e.target.value)} />
            )}
          />
        </div>
      )}
      {error && <span className="text-[12px] text-bad">{error}</span>}
    </div>
  );
}
