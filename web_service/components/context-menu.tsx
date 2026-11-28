"use client";
// Our own right-click menu (alpha.5 item 17, docs/mock/ContextMenus.dc.html, plan 36 §43): one shared menu in the app's
// menu look. A place opens it from its onContextMenu (`openMenu(e, { title, items })`); everywhere else
// `installMenus` (the root layout) gives text fields theirs (Cut, Copy, Paste, Select all, Insert @…, and the Mac
// app's spelling) and empty space none. Shift + right-click is always the browser's own menu. The menu key and ⇧F10
// open it on the focused element (in a field, at its corner). Opens at the pointer, kept on screen; ↑ ↓, → ←
// (submenus), Enter, Esc, a typed letter; focus goes back where it was.
import { useLayoutEffect, useRef, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { AtSign, BookPlus, ClipboardPaste, Copy, Scissors, TextSelect } from "lucide-react";
import type { LucideIcon } from "lucide-react";
import { desktop } from "@/lib/desktop";

export type MenuItem = {
  label: string;
  icon?: LucideIcon;
  /** the shortcut shown on the right ("S", "⌘X") */
  keys?: string;
  onPick?: () => void;
  /** greyed, with this reason on hover */
  off?: string;
  /** red; danger items go last */
  danger?: boolean;
  /** a toggle: ticked when true */
  checked?: boolean;
  /** a submenu (›) */
  items?: Item[];
  /** the Mac app's spelling suggestions */
  bold?: boolean;
  /** a section's name inside the menu (ModelFolders 5c "Copies on mac"): not an item */
  heading?: boolean;
  /** a length in seconds typed in a small popover beside the menu, with quick picks (AudioFades frame 4: Fade in…) */
  seconds?: Seconds;
};
export type Seconds = {
  title: string;
  value: number;
  picks: number[];
  max: number;
  /** default 0 */
  min?: number;
  /** default "s" (the Duck music amount: "dB") */
  unit?: string;
  note: string;
  onSet: (s: number) => void;
};
/** "-" is a separator; false / null / undefined are left out (`cond && {…}`). */
export type Item = MenuItem | "-" | false | null | undefined;
/** `beside`: a click opens it to the right of the panel the button sits in, bottom-aligned with the button (the rail's
 * Setup gear), not under the button over its neighbours */
export type Menu = { title?: string; items: Item[]; beside?: boolean };
export type MenuHandle = { update: (m: Menu) => void };

type Open = {
  root: Root;
  el: HTMLDivElement;
  back: Element | null;
  menu: Menu;
  x: number;
  y: number;
  kbd: boolean;
};
let open: Open | null = null;

const isMac = () => typeof navigator !== "undefined" && /Mac|iPhone|iPad/.test(navigator.userAgent);
/** ⌘X on a Mac, Ctrl+X elsewhere. */
export const mod = (k: string) => (isMac() ? `⌘${k}` : `Ctrl+${k}`);

/** Closes the open menu; focus goes back where it was. */
export function closeMenu() {
  const o = open;
  if (!o) return;
  open = null;
  o.root.unmount();
  o.el.remove();
  if (o.back instanceof HTMLElement && o.back.isConnected) o.back.focus({ preventScroll: true });
}

type Ev = {
  shiftKey: boolean;
  button: number;
  detail: number;
  type: string;
  clientX: number;
  clientY: number;
  target: EventTarget | null;
  currentTarget: EventTarget | null;
  preventDefault: () => void;
};

/** Opens `menu` for a contextmenu event (at the pointer) or a ⋯ button's click (under the button). Shift: the
 * browser's own menu, nothing opens (null). The handle's update redraws it (items read after opening). */
export function openMenu(e: Ev, menu: Menu, keep = false): MenuHandle | null {
  if (e.shiftKey && e.type === "contextmenu") return null;
  // the Mac app reads its spelling from Electron's context-menu event, which a cancelled event never fires
  if (!keep) e.preventDefault();
  const click = e.type !== "contextmenu";
  const r =
    click && e.currentTarget instanceof Element ? e.currentTarget.getBoundingClientRect() : null;
  // a contextmenu from the keyboard (the menu key, ⇧F10) has no right button; a ⋯ clicked with Enter no detail
  const kbd = click ? e.detail === 0 : e.button !== 2;
  closeMenu();
  const target = e.target instanceof Element ? e.target : null;
  // inside a modal dialog or a popover the menu goes in it, so it is neither inert nor a click outside it
  const host = target?.closest("dialog[open], [popover]") ?? document.body;
  const el = document.createElement("div");
  host.appendChild(el);
  const o: Open = {
    root: createRoot(el),
    el,
    back: document.activeElement,
    menu,
    // beside: past the right edge of the button's panel; the bottom edge flips it up to sit level with the button
    x: r
      ? menu.beside
        ? (target?.closest(".panel") ?? (e.currentTarget as Element)).getBoundingClientRect()
            .right + 8
        : r.left
      : e.clientX,
    y: r ? (menu.beside ? r.bottom : r.bottom + 4) : e.clientY,
    kbd,
  };
  open = o;
  draw(o);
  return { update: (m) => open === o && ((o.menu = m), draw(o)) };
}

function draw(o: Open) {
  o.root.render(<MenuRoot o={o} />);
}

const pick = (it: MenuItem) => {
  closeMenu();
  it.onPick?.();
};

function MenuRoot({ o }: { o: Open }) {
  const ref = useRef<HTMLDivElement>(null);
  const [at, setAt] = useState({ x: o.x, y: o.y });
  // kept on screen: flips left or up near an edge
  useLayoutEffect(() => {
    const m = ref.current;
    if (!m) return;
    try {
      m.showPopover?.();
    } catch {
      /* not supported, or shown already */
    }
    const r = m.getBoundingClientRect();
    const x = o.x + r.width > innerWidth - 4 ? Math.max(4, o.x - r.width) : o.x;
    const y = o.y + r.height > innerHeight - 4 ? Math.max(4, o.y - r.height) : o.y;
    if (x !== at.x || y !== at.y) setAt({ x, y });
  }, [o.x, o.y, o.menu, at.x, at.y]);
  useLayoutEffect(() => {
    const m = ref.current;
    if (!m) return;
    // the list takes focus (the outer div can't), so ↑ ↓, a letter and Esc work after a right-click too
    const first = m.querySelector<HTMLElement>('[data-lv="0"]');
    const list = m.querySelector<HTMLElement>('[role="menu"]');
    (o.kbd && first ? first : (list ?? m)).focus({ preventScroll: true });
    const outside = (e: Event) => !o.el.contains(e.target as Node) && closeMenu();
    const gone = () => closeMenu();
    // a scroll queued before the menu opened (the strip scrolled to the tile right-clicked) is dispatched in the next
    // frame, ahead of its animation callbacks: only a scroll after that closes the menu
    let armed = false;
    const arm = requestAnimationFrame(() => (armed = true));
    const scrolled = (e: Event) => armed && outside(e);
    addEventListener("pointerdown", outside, true);
    addEventListener("scroll", scrolled, true);
    addEventListener("blur", gone);
    addEventListener("resize", gone);
    return () => {
      cancelAnimationFrame(arm);
      removeEventListener("pointerdown", outside, true);
      removeEventListener("scroll", scrolled, true);
      removeEventListener("blur", gone);
      removeEventListener("resize", gone);
    };
  }, [o]);
  return (
    <div
      ref={ref}
      // the top layer, over dialogs and popovers, where the browser has it
      popover={"showPopover" in HTMLElement.prototype ? "manual" : undefined}
      className="cm"
      style={{ left: at.x, top: at.y }}
      onContextMenu={(e) => e.preventDefault()}
    >
      <List items={o.menu.items} title={o.menu.title} lv={0} />
    </div>
  );
}

function List({
  items,
  title,
  lv,
  onBack,
  focusFirst,
}: {
  items: Item[];
  title?: string;
  lv: number;
  onBack?: () => void;
  focusFirst?: boolean;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const [sub, setSub] = useState<{ i: number; focus: boolean } | null>(null);
  const [left, setLeft] = useState(false);
  // no separator first, last or twice
  const list: (MenuItem | "-")[] = [];
  for (const x of items) if (x && (x !== "-" || (list.length && list.at(-1) !== "-"))) list.push(x);
  if (list.at(-1) === "-") list.pop();
  useLayoutEffect(() => {
    const m = ref.current;
    if (!m || !lv) return;
    const r = m.getBoundingClientRect();
    if (r.right > innerWidth - 4) setLeft(true);
    if (r.bottom > innerHeight - 4) m.style.top = `${innerHeight - 4 - r.bottom}px`;
    if (focusFirst) m.querySelector<HTMLElement>(`[data-lv="${lv}"]`)?.focus();
  }, [lv, focusFirst]);
  const acts = list.filter((x): x is MenuItem => x !== "-" && !x.heading);
  const buttons = () => [
    ...(ref.current?.querySelectorAll<HTMLElement>(`[data-lv="${lv}"]`) ?? []),
  ];
  return (
    <div
      ref={ref}
      role="menu"
      aria-label={title}
      tabIndex={-1}
      className={lv ? `cm sub ${left ? "left" : ""}` : "cm-in"}
      onKeyDown={(e) => {
        const bs = buttons();
        const i = bs.indexOf(document.activeElement as HTMLElement);
        const go = (j: number) => bs[(j + bs.length) % bs.length]?.focus();
        const it = acts[i] as MenuItem | undefined;
        if (e.key === "ArrowDown") go(i + 1);
        else if (e.key === "ArrowUp") go(i < 0 ? -1 : i - 1);
        else if (e.key === "Home") go(0);
        else if (e.key === "End") go(-1);
        else if (e.key === "ArrowRight" && (it?.items || it?.seconds) && !it.off)
          setSub({ i: list.indexOf(it), focus: true });
        else if (e.key === "ArrowLeft" && onBack) onBack();
        else if (e.key === "Escape" || e.key === "Tab") closeMenu();
        else if (e.key.length === 1 && /\S/.test(e.key) && !e.metaKey && !e.ctrlKey) {
          // a typed letter: the next item starting with it
          const k = e.key.toLowerCase();
          const labels = acts.map((x) => x.label.toLowerCase());
          const j = [...labels.keys()]
            .map((n) => (i + 1 + n) % labels.length)
            .find((n) => labels[n].startsWith(k));
          if (j === undefined) return;
          go(j);
        } else return;
        e.preventDefault();
        e.stopPropagation();
      }}
    >
      {title && <span className="hd">{title}</span>}
      {list.map((x, n) => {
        if (x === "-") return <span key={n} className="sep" role="separator" />;
        if (x.heading)
          return (
            <span key={n} className="hd pt-0.5">
              {x.label}
            </span>
          );
        const Icon = x.icon;
        const on = sub?.i === n;
        const nest = !!(x.items || x.seconds);
        return (
          <div key={n} className="relative">
            <button
              type="button"
              role={x.checked === undefined ? "menuitem" : "menuitemcheckbox"}
              aria-checked={x.checked}
              aria-disabled={x.off ? true : undefined}
              aria-haspopup={x.items ? "menu" : x.seconds ? "dialog" : undefined}
              aria-expanded={nest ? on : undefined}
              title={x.off}
              tabIndex={-1}
              data-lv={lv}
              className={`mi ${x.danger ? "danger" : ""} ${x.bold ? "font-bold" : ""} ${on ? "on" : ""}`}
              onMouseEnter={(e) => {
                e.currentTarget.focus({ preventScroll: true });
                setSub(nest && !x.off ? { i: n, focus: false } : null);
              }}
              onClick={() => {
                if (x.off) return;
                if (nest) setSub({ i: n, focus: true });
                else pick(x);
              }}
            >
              {Icon ? <Icon className="ic" aria-hidden /> : <span className="ic" aria-hidden />}
              <span className="lb">{x.label}</span>
              {x.checked && <span className="tick">✓</span>}
              {x.keys && <kbd>{x.keys}</kbd>}
              {x.items && <span className="chev">›</span>}
            </button>
            {on && x.seconds && <SecondsPanel s={x.seconds} focus={sub.focus} />}
            {on && x.items && (
              <List
                items={x.items}
                title={undefined}
                lv={lv + 1}
                focusFirst={sub.focus}
                onBack={() => {
                  setSub(null);
                  buttons()[acts.indexOf(x)]?.focus();
                }}
              />
            )}
          </div>
        );
      })}
    </div>
  );
}

const dbWord = (v: number) => `${v < 0 ? "−" : ""}${Math.abs(v)} dB`;

/** A Seconds item's popover: the length (Enter sets it), the quick picks, the limits. */
function SecondsPanel({ s, focus }: { s: Seconds; focus: boolean }) {
  const ref = useRef<HTMLDivElement>(null);
  const [v, setV] = useState(String(+s.value.toFixed(2)));
  const [left, setLeft] = useState(false);
  useLayoutEffect(() => {
    const r = ref.current?.getBoundingClientRect();
    if (r && r.right > innerWidth - 4) setLeft(true);
    if (focus) ref.current?.querySelector("input")?.focus();
  }, [focus]);
  const unit = s.unit ?? "s";
  const set = (x: number) => {
    if (!Number.isFinite(x)) return;
    closeMenu();
    s.onSet(Math.max(s.min ?? 0, Math.min(s.max, x)));
  };
  return (
    <div
      ref={ref}
      role="dialog"
      aria-label={s.title}
      className={`cm sub secs ${left ? "left" : ""}`}
      onKeyDown={(e) => e.stopPropagation()}
    >
      <span className="hd">{s.title}</span>
      <label className="secs-in">
        <input
          type="number"
          inputMode="decimal"
          min={s.min ?? 0}
          max={s.max}
          step={unit === "s" ? 0.1 : 1}
          value={v}
          aria-label={`${s.title} (${unit})`}
          onChange={(e) => setV(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") set(parseFloat(v));
            if (e.key === "Escape") closeMenu();
          }}
        />
        <span>{unit}</span>
      </label>
      <div className="secs-picks">
        {s.picks.map((p) => (
          <button
            key={p}
            type="button"
            className={`chip ${p === s.value ? "on" : ""}`}
            onClick={() => set(p)}
          >
            {unit === "dB" ? dbWord(p) : `${p} s`}
          </button>
        ))}
      </div>
      <p className="secs-note">{s.note}</p>
    </div>
  );
}

// ------------------------------------------------------------------ text fields (frame 6) and the rest of the page

type Field = HTMLInputElement | HTMLTextAreaElement | HTMLElement;
const TEXT_INPUTS = new Set(["text", "search", "email", "url", "tel", "password", "number", ""]);

/** The text field the event is in, or null. */
function fieldOf(t: Element | null): Field | null {
  const f = t?.closest<HTMLElement>("input, textarea, [contenteditable]");
  if (!f) return null;
  if (f instanceof HTMLInputElement)
    return TEXT_INPUTS.has(f.getAttribute("type") ?? "") ? f : null;
  if (f instanceof HTMLTextAreaElement) return f;
  return f.isContentEditable ? f : null;
}
const isInput = (f: Field): f is HTMLInputElement | HTMLTextAreaElement =>
  f instanceof HTMLInputElement || f instanceof HTMLTextAreaElement;
const readOnly = (f: Field) => (isInput(f) ? f.readOnly || f.disabled : !f.isContentEditable);
function selected(f: Field): string {
  if (isInput(f)) {
    try {
      const [a, b] = [f.selectionStart, f.selectionEnd];
      if (a !== null && b !== null) return f.value.slice(a, b);
    } catch {
      /* a number input has no selection range */
    }
  }
  return getSelection()?.toString() ?? "";
}
const exec = (cmd: string, value?: string) => {
  try {
    return !!document.execCommand?.(cmd, false, value);
  } catch {
    return false;
  }
};
/** Types `text` over the selection, as typing would: the field's undo takes it back. */
export function insertText(f: Field, text: string) {
  f.focus();
  if (exec("insertText", text) || !isInput(f)) return;
  const [a, b] = [f.selectionStart ?? f.value.length, f.selectionEnd ?? f.value.length];
  f.setRangeText(text, a, b, "end");
  f.dispatchEvent(new Event("input", { bubbles: true }));
}

// the browser blocked reading the clipboard (its permission, or a Paste refused): Paste greys with the reason
let pasteBlocked = false;
const BLOCKED = `Paste with ${mod("V")}: the browser blocked the clipboard`;
const NO_TEXT = "Select some text first";
const READ_ONLY = "This field is read-only";
const HIDDEN = "Hidden text can't be copied";

async function paste(f: Field) {
  try {
    const s = await navigator.clipboard.readText();
    insertText(f, s);
  } catch {
    pasteBlocked = true;
  }
}
function copy(text: string, f: Field | null, cut = false) {
  f?.focus();
  if (exec(cut ? "cut" : "copy")) return;
  void navigator.clipboard?.writeText(text).then(() => cut && exec("delete"));
}

/** The text field's items: Cut, Copy, Paste, Select all, and Insert @… in a prompt that takes @ mentions. */
export function textItems(f: Field): Item[] {
  const ro = readOnly(f);
  const sel = selected(f);
  const mentions = !ro && !!f.closest("[data-mentions]");
  // a password or key field: never onto the clipboard, as the browser's own menu has it
  const hidden = f instanceof HTMLInputElement && f.type === "password" ? HIDDEN : undefined;
  return [
    {
      label: "Cut",
      icon: Scissors,
      keys: mod("X"),
      off: ro ? READ_ONLY : (hidden ?? (!sel ? NO_TEXT : undefined)),
      onPick: () => copy(sel, f, true),
    },
    {
      label: "Copy",
      icon: Copy,
      keys: mod("C"),
      off: hidden ?? (!sel ? NO_TEXT : undefined),
      onPick: () => copy(sel, f),
    },
    {
      label: "Paste",
      icon: ClipboardPaste,
      keys: mod("V"),
      off: ro ? READ_ONLY : pasteBlocked ? BLOCKED : undefined,
      onPick: () => void paste(f),
    },
    {
      label: "Select all",
      icon: TextSelect,
      keys: mod("A"),
      onPick: () => {
        f.focus();
        if (isInput(f)) f.select();
        else exec("selectAll");
      },
    },
    mentions && "-",
    mentions && { label: "Insert @…", icon: AtSign, keys: "@", onPick: () => insertText(f, "@") },
  ];
}

let spelling: { h: MenuHandle; menu: Menu } | null = null;

/** The spelling items first (up to three suggestions, Add to dictionary), then the field's. */
export function spellItems(word: string, suggestions: string[]): Item[] {
  const d = desktop();
  if (!word || !d) return [];
  return [
    ...suggestions
      .slice(0, 3)
      .map((s) => ({ label: s, bold: true, onPick: () => d.replaceMisspelling?.(s) })),
    { label: "Add to dictionary", icon: BookPlus, onPick: () => d.addToDictionary?.(word) },
    "-",
  ];
}

function onContextMenu(e: MouseEvent) {
  // Shift: the browser's own menu; a place's own menu cancelled it already
  if (e.shiftKey || e.defaultPrevented) return;
  const t = e.target instanceof Element ? e.target : null;
  const f = fieldOf(t);
  if (f) {
    const menu = { items: textItems(f) };
    const d = desktop();
    const h = openMenu(e, menu, !!d);
    if (d && h) spelling = { h, menu };
    return;
  }
  // selected text elsewhere (a chat message): Copy; else nothing (empty space, a heading)
  const sel = getSelection();
  const text = sel?.toString() ?? "";
  if (text.trim() && t && sel?.containsNode(t, true))
    return void openMenu(e, {
      items: [{ label: "Copy", icon: Copy, keys: mod("C"), onPick: () => copy(text, null) }],
    });
  e.preventDefault();
}

/** The menu key or ⇧F10: the focused element's menu, at its bottom-left corner. */
function onKey(e: KeyboardEvent) {
  if (e.key !== "ContextMenu" && !(e.key === "F10" && e.shiftKey)) return;
  const el = document.activeElement instanceof HTMLElement ? document.activeElement : document.body;
  if (open?.el.contains(el)) return;
  e.preventDefault();
  // ponytail: a field's corner, not its caret (a textarea has no caret rectangle without a mirror element)
  const r = el.getBoundingClientRect();
  el.dispatchEvent(
    new MouseEvent("contextmenu", {
      bubbles: true,
      cancelable: true,
      clientX: r.left,
      clientY: r.bottom,
      button: 0,
    }),
  );
}

let installed = false;
/** Once per page (the root layout): text fields' menus, nothing on empty space, the keyboard, the Mac app's spelling. */
export function installMenus() {
  if (installed || typeof window === "undefined") return;
  installed = true;
  // after every element's handler, so a place's own menu goes first
  addEventListener("contextmenu", onContextMenu);
  addEventListener("keydown", onKey, true);
  desktop()?.onSpelling?.(({ word, suggestions }) => {
    const s = spelling;
    spelling = null;
    if (s && word)
      s.h.update({ ...s.menu, items: [...spellItems(word, suggestions), ...s.menu.items] });
  });
  navigator.permissions
    ?.query({ name: "clipboard-read" as PermissionName })
    .then((p) => {
      pasteBlocked = p.state === "denied";
      p.onchange = () => (pasteBlocked = p.state === "denied");
    })
    .catch(() => undefined);
}

/** The root layout's: installs the menus once mounted. */
export function ContextMenus() {
  useLayoutEffect(installMenus, []);
  return null;
}
