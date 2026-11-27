"use client";
// A prompt with @ picks (MagicBoard frames 3 and 7; plan 39 phase E): type @ and the menu offers the board's assets
// (@mara) and other blocks' pictures (@Shot 1 · last frame). An asset is written as @handle and joins the refs; a
// picture joins the refs only (a block's name is no handle). The text stays plain; studio writes the handles as names.
import { useId, useRef, useState } from "react";
import { insertMention, mentionAt } from "@/lib/mentions";
import { atMatches, mentionedRefs } from "@/lib/magic-assets";
import type { Doc, Ref } from "@/lib/magic";

export function AtText({
  doc,
  self,
  value,
  refs,
  onChange,
  label,
  placeholder,
  className = "in min-h-16 px-2.5 py-2 text-[13px]",
}: {
  doc: Doc;
  /** the block it is for (its own picture is not offered) */
  self?: string;
  value: string;
  refs: Ref[];
  /** the text and the refs with what it picks */
  onChange: (text: string, refs: Ref[]) => void;
  label: string;
  placeholder?: string;
  className?: string;
}) {
  const ta = useRef<HTMLTextAreaElement>(null);
  const uid = "at" + useId().replace(/[^a-zA-Z0-9]/g, "");
  const [caret, setCaret] = useState<number | null>(null);
  const [closed, setClosed] = useState<number | null>(null);
  const [active, setActive] = useState(0);
  const at = caret == null ? null : mentionAt(value, caret);
  const open = !!at && at.start !== closed;
  const items = open ? atMatches(doc, at.query, self) : [];
  const key = (r: Ref) => ("asset" in r ? `a:${r.asset}` : `b:${r.block}:${r.part}`);
  const withRef = (rs: Ref[], r: Ref) => (rs.some((x) => key(x) === key(r)) ? rs : [...rs, r]);

  const change = (text: string, rs = refs) => onChange(text, [...rs, ...mentionedRefs(doc, text, rs)]);
  function pick(i: number) {
    const it = items[i];
    if (!it || !at) return;
    const asset = "asset" in it.ref ? doc.assets.find((a) => a.id === (it.ref as { asset: string }).asset) : null;
    // an asset is written as its handle; a picture only joins the refs (the typed @… goes)
    const out = asset
      ? insertMention(value, at.start, at.end, asset.handle)
      : { text: value.slice(0, at.start) + value.slice(at.end), caret: at.start };
    change(out.text, withRef(refs, it.ref));
    setCaret(out.caret);
    requestAnimationFrame(() => ta.current?.setSelectionRange(out.caret, out.caret));
  }
  return (
    <div className="relative flex flex-col">
      <textarea
        ref={ta}
        aria-label={label}
        aria-autocomplete="list"
        aria-controls={open && items.length ? uid : undefined}
        aria-activedescendant={open && items.length ? `${uid}-${active}` : undefined}
        placeholder={placeholder ?? "Type @ to pick an asset or a picture"}
        className={className}
        value={value}
        onChange={(e) => {
          change(e.target.value);
          setCaret(e.target.selectionStart);
          setActive(0);
        }}
        onSelect={(e) => setCaret(e.currentTarget.selectionStart)}
        onBlur={() => setTimeout(() => setCaret(null), 150)}
        onKeyDown={(e) => {
          if (!open || !items.length) return;
          if (e.key === "ArrowDown" || e.key === "ArrowUp") {
            e.preventDefault();
            setActive((i) => (i + (e.key === "ArrowDown" ? 1 : items.length - 1)) % items.length);
          } else if (e.key === "Enter" || e.key === "Tab") {
            e.preventDefault();
            pick(active);
          } else if (e.key === "Escape") {
            e.preventDefault();
            e.stopPropagation();
            setClosed(at!.start);
          }
        }}
      />
      {open && items.length > 0 && (
        <div
          id={uid}
          role="listbox"
          aria-label="Pick"
          className="absolute top-full left-0 z-20 mt-1 flex max-h-56 w-[min(100%,320px)] flex-col overflow-y-auto rounded-xl bg-s3 p-1 shadow-lg"
        >
          {items.map((it, i) => (
            <button
              key={key(it.ref)}
              id={`${uid}-${i}`}
              type="button"
              role="option"
              aria-selected={i === active}
              className={`flex items-center gap-2 rounded-lg px-2 py-1.5 text-left text-[12px] ${i === active ? "bg-s5" : ""}`}
              onMouseDown={(e) => e.preventDefault()}
              onClick={() => pick(i)}
            >
              <span className={`rc ${"asset" in it.ref ? "ch" : "lk"}`}>{it.label}</span>
              <span className="min-w-0 truncate text-t2">{it.sub}</span>
            </button>
          ))}
        </div>
      )}
    </div>
  );
}
