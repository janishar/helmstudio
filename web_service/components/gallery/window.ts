// Windowing for a take grid (alpha.2 item 32): only the rows near the screen are drawn; padding on the grid stands
// for the rest, so the scroll height, the scroll position and the selection (kept by id) stay as they were.
import { useEffect, useRef, useState } from "react";

/** The rows drawn above and below the screen. */
const OVERSCAN = 4;

export type GridWindow = { start: number; end: number; padTop: number; padBottom: number };

/** Which items of `count` to draw, for a grid of `cols` columns whose rows are `stride` apart, with the screen from
 * `top` to `bottom` in the grid's own coordinates; unmeasured (nothing has a size, as in tests) every item. */
export function windowOf(
  count: number,
  cols: number,
  stride: number,
  top: number,
  bottom: number,
): GridWindow {
  if (cols < 1 || stride <= 0) return { start: 0, end: count, padTop: 0, padBottom: 0 };
  const rows = Math.ceil(count / cols);
  // the grid off the screen: no rows drawn, its height kept by the padding
  const first = Math.min(rows, Math.max(0, Math.floor(top / stride) - OVERSCAN));
  const last = Math.max(first, Math.min(rows, Math.ceil(bottom / stride) + OVERSCAN));
  return {
    start: first * cols,
    end: Math.min(count, last * cols),
    padTop: first * stride,
    padBottom: (rows - last) * stride,
  };
}

/** The window's part of a list that starts at item `offset` of the grid (the Gallery's takes after its uploads), as
 * slice bounds; empty when the window ends before it. */
export const sliceFrom = (w: GridWindow, offset: number): [number, number] => [
  Math.max(0, w.start - offset),
  Math.max(0, w.end - offset),
];

/** The window of the grid `g` now: its columns, its row stride (from its first two rows drawn; nothing drawn: `last`)
 * and the screen in its own coordinates (its top padding stands for the rows above). */
function measure(g: HTMLElement, count: number, last: number) {
  const kids = g.children as HTMLCollectionOf<HTMLElement>;
  const css = getComputedStyle(g);
  const cols = css.gridTemplateColumns.split(" ").filter(Boolean).length;
  const gap = parseFloat(css.rowGap) || 0;
  const stride =
    (kids.length > cols && cols > 0
      ? kids[cols].offsetTop - kids[0].offsetTop
      : kids[0]
        ? kids[0].offsetHeight + gap
        : 0) || last;
  const r = g.getBoundingClientRect();
  return { win: windowOf(count, cols, stride, -r.top, window.innerHeight - r.top), stride };
}

const same = (a: GridWindow, b: GridWindow) =>
  a.start === b.start && a.end === b.end && a.padTop === b.padTop && a.padBottom === b.padBottom;

/** Windows `count` items of the grid `el` (its children: one per item drawn, from `start`); it draws again only when
 * the window moves by a row, not on every scroll. */
export function useGridWindow(el: React.RefObject<HTMLElement | null>, count: number): GridWindow {
  const [win, setWin] = useState<GridWindow>({ start: 0, end: count, padTop: 0, padBottom: 0 });
  const stride = useRef(0);
  useEffect(() => {
    let raf = 0;
    const update = () => {
      const g = el.current;
      if (!g) return;
      const m = measure(g, count, stride.current);
      stride.current = m.stride;
      setWin((o) => (same(o, m.win) ? o : m.win));
    };
    const on = () => {
      cancelAnimationFrame(raf);
      raf = requestAnimationFrame(update);
    };
    on();
    // capture: the grid may scroll in the page or in a dialog's own box
    document.addEventListener("scroll", on, { capture: true, passive: true });
    window.addEventListener("resize", on);
    // the grid's own width changes too (a panel opens, a dialog's box): its columns change
    const ro = typeof ResizeObserver === "undefined" ? null : new ResizeObserver(on);
    if (el.current) ro?.observe(el.current);
    return () => {
      ro?.disconnect();
      cancelAnimationFrame(raf);
      document.removeEventListener("scroll", on, { capture: true });
      window.removeEventListener("resize", on);
    };
  }, [el, count]);
  // a page just added is drawn while the window catches up (its rows were at the end)
  return win.padBottom === 0 && win.end < count ? { ...win, end: count } : win;
}
