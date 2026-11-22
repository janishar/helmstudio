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
import { MapPin, Maximize2, Package, UserRound } from "lucide-react";
import { Modal } from "@/components/kit";
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
  const [over, setOver] = useState(false);
  const [big, setBig] = useState(false);
  useLayoutEffect(() => {
    const t = ta.current;
    // ponytail: measured on each change of the text only, not when the box is resized
    if (expand && t) setOver(t.scrollHeight > t.clientHeight);
  }, [expand, value]);
  // the dialog's textarea takes the focus once the dialog is open (its own effect runs first)
  useEffect(() => {
    if (big) document.getElementById(`${uid}-x`)?.focus();
  }, [big, uid]);

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
    <div className={`group relative ${wrapClassName ?? ""}`}>
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
      {expand && (
        <>
          <button
            type="button"
            aria-label="Expand"
            title="Expand"
            aria-haspopup="dialog"
            onClick={() => setBig(true)}
            className={`absolute top-0 right-0 grid size-7 place-items-center rounded-lg bg-s3 text-t2 hover:text-t1 ${over ? "" : "opacity-0 group-focus-within:opacity-100 focus-visible:opacity-100"}`}
          >
            <Maximize2 size={16} aria-hidden />
          </button>
          {big && (
            <Modal
              open
              onClose={() => {
                setBig(false);
                // after the browser puts the focus back on the button
                requestAnimationFrame(() => ta.current?.focus());
              }}
              title={expand}
              width="w-[min(94vw,880px)]"
            >
              <div className="flex flex-col gap-3.5">
                <PromptArea
                  id={`${uid}-x`}
                  aria-label={expand}
                  maxLength={rest.maxLength}
                  placeholder={rest.placeholder}
                  value={value}
                  onChange={onChange}
                  className="block h-[min(420px,60vh)] w-full resize-none rounded-xl border border-a1 bg-s1 px-4 py-3.5 text-[15px] leading-6 text-t1 outline-0 placeholder:text-t2"
                />
                <div className="flex items-center gap-2 text-[12px] text-t2">
                  {rest.maxLength != null && (
                    <span className="mono">
                      {value.length} / {rest.maxLength}
                    </span>
                  )}
                  <span>· @ adds an element · Esc closes</span>
                  <button
                    type="button"
                    onClick={(e) => e.currentTarget.closest("dialog")?.close()}
                    className="ml-auto h-9 rounded-[10px] bg-a1 px-4 text-[13px] font-semibold text-[var(--on1)]"
                  >
                    Done
                  </button>
                </div>
              </div>
            </Modal>
          )}
        </>
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
        className={`mono rounded-lg bg-s5 px-2.5 py-1 text-[12px] ${bad ? "text-bad underline decoration-wavy" : ""}`}
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
